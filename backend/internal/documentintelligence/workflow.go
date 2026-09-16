package documentintelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ragbench-my/backend/internal/providers"
)

const (
	StageExtractText       = "extract_text"
	StageStructuredExtract = "structured_extract"
	StageSchemaValidate    = "schema_validate"
	StageFinancialValidate = "financial_validate"
	StageSummarize         = "summarize"
	StageRequestSpan       = "request"
)

var OrderedStages = []string{
	StageExtractText,
	StageStructuredExtract,
	StageSchemaValidate,
	StageFinancialValidate,
	StageSummarize,
}

// SourceDocument is the immutable identity passed to the extraction tool.
// The tool must use the saved checksum to prevent a path replacement from
// changing the evidence associated with an analysis.
type SourceDocument struct {
	DocumentID     string
	RevisionID     string
	Filename       string
	MIMEType       string
	StoragePath    string
	SourceChecksum string
}

type TextEvidence struct {
	Method         string            `json:"method"`
	Profile        string            `json:"profile"`
	SourceChecksum string            `json:"source_checksum"`
	RawText        string            `json:"raw_text"`
	Sections       []EvidenceSection `json:"sections"`
}

type EvidenceSection struct {
	Text     string         `json:"text"`
	Location map[string]any `json:"location"`
}

// Tool interfaces are intentionally named and narrow. They are the agent's
// complete tool surface; there is no general-purpose tool discovery.
type TextExtractionTool interface {
	ExtractText(context.Context, SourceDocument) (TextEvidence, error)
}

type StructuredExtractionTool interface {
	ExtractStructured(context.Context, TextEvidence, providers.GenerationProfile, string) (providers.GenerationResult, error)
}

type SchemaValidationTool interface {
	ValidateSchema([]byte) (FinancialData, error)
}

type FinancialValidationTool interface {
	ValidateFinancial(FinancialData) (ValidationResult, error)
}

type SummaryTool interface {
	Summarize(context.Context, FinancialData, ValidationResult, providers.GenerationProfile) (providers.GenerationResult, error)
}

// ToolEvent is persisted as part of an analysis trace. Attempts are explicit
// so a reviewer can distinguish a bounded retry from an unbounded loop.
type ToolEvent struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Attempt    int    `json:"attempt"`
	StartedAt  string `json:"started_at"`
	DurationMS int64  `json:"duration_ms"`
	ErrorCode  string `json:"error_code,omitempty"`
}

type WorkflowResult struct {
	TraceID        string
	Evidence       TextEvidence
	RawModelOutput string
	Data           FinancialData
	Validation     ValidationResult
	Summary        string
	Events         []ToolEvent
	InputTokens    *int
	OutputTokens   *int
}

type StageError struct {
	Stage string
	Code  string
	Err   error
}

func (e *StageError) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *StageError) Unwrap() error { return e.Err }

// DocumentIntelligenceAgent is the small explicit workflow orchestrator used
// by unit tests and by the persisted runner. MaxStructuredRetries is the only
// model retry budget; financial validation is never retried as a model error.
type DocumentIntelligenceAgent struct {
	ExtractTextTool       TextExtractionTool
	StructuredExtractTool StructuredExtractionTool
	SchemaValidateTool    SchemaValidationTool
	FinancialValidateTool FinancialValidationTool
	SummaryTool           SummaryTool
	ModelProfile          providers.GenerationProfile
	MaxStructuredRetries  int
	TraceID               func() string
	Now                   func() time.Time
}

// Agent is retained as a short alias for callers that used the initial PoC
// name; the assessment-facing contract is DocumentIntelligenceAgent.
type Agent = DocumentIntelligenceAgent

func (a *Agent) Run(ctx context.Context, source SourceDocument) (WorkflowResult, error) {
	if a.ExtractTextTool == nil || a.StructuredExtractTool == nil ||
		a.SchemaValidateTool == nil || a.FinancialValidateTool == nil || a.SummaryTool == nil {
		return WorkflowResult{}, &StageError{Stage: StageRequestSpan, Code: "workflow_not_configured", Err: errors.New("all named document-intelligence tools are required")}
	}
	now := a.Now
	if now == nil {
		now = time.Now
	}
	traceID := ""
	if a.TraceID != nil {
		traceID = a.TraceID()
	}
	if traceID == "" {
		traceID = fmt.Sprintf("di-%d", now().UnixNano())
	}
	result := WorkflowResult{TraceID: traceID, Events: []ToolEvent{}}

	// Extraction is always the first tool. No later result is produced when
	// source evidence cannot be read.
	evidence, err := a.callExtract(ctx, source, now, &result)
	if err != nil {
		return result, err
	}
	result.Evidence = evidence

	maxRetries := a.MaxStructuredRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		prompt, promptErr := BuildExtractionPrompt(evidence.RawText)
		if promptErr != nil {
			return result, &StageError{Stage: StageStructuredExtract, Code: "prompt_failed", Err: promptErr}
		}
		raw, genResult, callErr := a.callStructured(ctx, evidence, prompt, attempt, now, &result)
		if callErr != nil {
			if attempt <= maxRetries && retryableStructuredError(callErr) {
				continue
			}
			return result, callErr
		}
		result.RawModelOutput = raw
		if genResult.InputTokens != nil {
			result.InputTokens = mergeUsage(result.InputTokens, genResult.InputTokens)
		}
		if genResult.OutputTokens != nil {
			result.OutputTokens = mergeUsage(result.OutputTokens, genResult.OutputTokens)
		}

		data, schemaErr := a.callSchema(raw, attempt, now, &result)
		if schemaErr == nil {
			result.Data = data
			break
		}
		if attempt > maxRetries {
			return result, schemaErr
		}
		// A malformed/schema-invalid candidate is the one bounded correction
		// case. The next attempt receives the same evidence and schema prompt;
		// it does not retry deterministic financial validation.
	}

	validationStarted := now()
	validation, err := a.FinancialValidateTool.ValidateFinancial(result.Data)
	validationEvent := ToolEvent{Name: StageFinancialValidate, Status: "succeeded", Attempt: 1,
		StartedAt: validationStarted.UTC().Format(time.RFC3339Nano), DurationMS: now().Sub(validationStarted).Milliseconds()}
	result.Events = append(result.Events, validationEvent)
	if err != nil {
		result.Events[len(result.Events)-1].Status = "failed"
		result.Events[len(result.Events)-1].ErrorCode = errorCode(err, "validation_failed")
		return result, &StageError{Stage: StageFinancialValidate, Code: errorCode(err, "validation_failed"), Err: err}
	}
	result.Validation = validation

	summaryStarted := now()
	summaryResult, err := a.SummaryTool.Summarize(ctx, result.Data, validation, a.ModelProfile)
	summaryEvent := ToolEvent{Name: StageSummarize, Status: "succeeded", Attempt: 1,
		StartedAt: summaryStarted.UTC().Format(time.RFC3339Nano), DurationMS: now().Sub(summaryStarted).Milliseconds()}
	if err != nil {
		summaryEvent.Status = "failed"
		summaryEvent.ErrorCode = errorCode(err, "summary_provider_failed")
		result.Events = append(result.Events, summaryEvent)
		return result, &StageError{Stage: StageSummarize, Code: summaryEvent.ErrorCode, Err: err}
	}
	result.Events = append(result.Events, summaryEvent)
	result.Summary = appendValidationNotice(summaryResult.Text, validation)
	result.InputTokens = mergeUsage(result.InputTokens, summaryResult.InputTokens)
	result.OutputTokens = mergeUsage(result.OutputTokens, summaryResult.OutputTokens)
	return result, nil
}

func (a *Agent) callExtract(ctx context.Context, source SourceDocument, now func() time.Time, result *WorkflowResult) (TextEvidence, error) {
	started := now()
	evidence, err := a.ExtractTextTool.ExtractText(ctx, source)
	event := ToolEvent{Name: StageExtractText, Attempt: 1, StartedAt: started.UTC().Format(time.RFC3339Nano), DurationMS: now().Sub(started).Milliseconds(), Status: "succeeded"}
	if err != nil {
		event.Status = "failed"
		event.ErrorCode = errorCode(err, "extraction_failed")
		result.Events = append(result.Events, event)
		return TextEvidence{}, &StageError{Stage: StageExtractText, Code: event.ErrorCode, Err: err}
	}
	if strings.TrimSpace(evidence.RawText) == "" {
		event.Status = "failed"
		event.ErrorCode = "empty_extraction"
		result.Events = append(result.Events, event)
		return TextEvidence{}, &StageError{Stage: StageExtractText, Code: event.ErrorCode, Err: errors.New("extraction produced no useful evidence")}
	}
	result.Events = append(result.Events, event)
	return evidence, nil
}

func (a *Agent) callStructured(ctx context.Context, evidence TextEvidence, prompt string, attempt int, now func() time.Time, result *WorkflowResult) (string, providers.GenerationResult, error) {
	started := now()
	genResult, err := a.StructuredExtractTool.ExtractStructured(ctx, evidence, a.ModelProfile, prompt)
	event := ToolEvent{Name: StageStructuredExtract, Attempt: attempt, StartedAt: started.UTC().Format(time.RFC3339Nano), DurationMS: now().Sub(started).Milliseconds(), Status: "succeeded"}
	if err != nil {
		event.Status = "failed"
		event.ErrorCode = errorCode(err, "structured_extract_failed")
		result.Events = append(result.Events, event)
		return "", genResult, &StageError{Stage: StageStructuredExtract, Code: event.ErrorCode, Err: err}
	}
	if len([]byte(genResult.Text)) > MaxStructuredOutputBytes {
		event.Status = "failed"
		event.ErrorCode = "malformed_model_output"
		result.Events = append(result.Events, event)
		return "", genResult, &StageError{Stage: StageStructuredExtract, Code: event.ErrorCode, Err: errors.New("structured model output exceeds the bounded size")}
	}
	result.Events = append(result.Events, event)
	return genResult.Text, genResult, nil
}

func (a *Agent) callSchema(raw string, attempt int, now func() time.Time, result *WorkflowResult) (FinancialData, error) {
	started := now()
	data, err := a.SchemaValidateTool.ValidateSchema([]byte(raw))
	event := ToolEvent{Name: StageSchemaValidate, Attempt: attempt, StartedAt: started.UTC().Format(time.RFC3339Nano), DurationMS: now().Sub(started).Milliseconds(), Status: "succeeded"}
	if err != nil {
		event.Status = "failed"
		event.ErrorCode = errorCode(err, "schema_invalid")
		result.Events = append(result.Events, event)
		return FinancialData{}, &StageError{Stage: StageSchemaValidate, Code: event.ErrorCode, Err: err}
	}
	result.Events = append(result.Events, event)
	return data, nil
}

func retryableStructuredError(err error) bool {
	var providerErr *providers.ProviderError
	if !errors.As(err, &providerErr) {
		return false
	}
	return providerErr.Code == providers.ErrCodeModelTimeout || providerErr.Code == providers.ErrCodeModelRateLimited
}

func errorCode(err error, fallback string) string {
	var stageErr *StageError
	if errors.As(err, &stageErr) && stageErr.Code != "" {
		return stageErr.Code
	}
	var providerErr *providers.ProviderError
	if errors.As(err, &providerErr) && providerErr.Code != "" {
		return providerErr.Code
	}
	var schemaErr *SchemaError
	if errors.As(err, &schemaErr) {
		return "schema_invalid"
	}
	return fallback
}

func mergeUsage(current, next *int) *int {
	if current == nil && next == nil {
		return nil
	}
	if current == nil {
		value := *next
		return &value
	}
	if next == nil {
		value := *current
		return &value
	}
	value := *current + *next
	return &value
}

func appendValidationNotice(summary string, validation ValidationResult) string {
	summary = strings.TrimSpace(summary)
	if validation.Status == FindingStatusPass {
		return summary
	}
	material := make([]string, 0)
	for _, finding := range validation.Findings {
		if finding.Status == FindingStatusPass {
			continue
		}
		material = append(material, finding.RuleID+": "+finding.Message)
	}
	if len(material) == 0 {
		return summary
	}
	return strings.TrimSpace(summary + " Validation " + validation.Status + ": " + strings.Join(material, "; ") + ".")
}

// ProviderStructuredTool adapts the existing provider abstraction to the
// named structured-extraction tool. Providers that implement the optional
// structured-output method receive a JSON response format; test doubles and
// older OpenAI-compatible endpoints safely use the ordinary Generate method.
type ProviderStructuredTool struct {
	Generator providers.Generator
}

func (t ProviderStructuredTool) ExtractStructured(ctx context.Context, evidence TextEvidence, profile providers.GenerationProfile, prompt string) (providers.GenerationResult, error) {
	if t.Generator == nil {
		return providers.GenerationResult{}, &providers.ProviderError{Code: providers.ErrCodeModelFailed, Message: "structured extraction provider is not configured"}
	}
	if generator, ok := t.Generator.(providers.StructuredGenerator); ok {
		return generator.GenerateStructured(ctx, profile, prompt, json.RawMessage(`{"type":"object"}`))
	}
	return t.Generator.Generate(ctx, profile, prompt)
}

type ProviderSummaryTool struct {
	Generator providers.Generator
}

func (t ProviderSummaryTool) Summarize(ctx context.Context, data FinancialData, validation ValidationResult, profile providers.GenerationProfile) (providers.GenerationResult, error) {
	prompt, err := BuildSummaryPrompt(data, validation)
	if err != nil {
		return providers.GenerationResult{}, err
	}
	if t.Generator == nil {
		return providers.GenerationResult{}, &providers.ProviderError{Code: providers.ErrCodeModelFailed, Message: "summary provider is not configured"}
	}
	return t.Generator.Generate(ctx, profile, prompt)
}

type SchemaTool struct{}

func (SchemaTool) ValidateSchema(raw []byte) (FinancialData, error) {
	return ParseFinancialData(raw)
}

type FinancialTool struct {
	Policy ValidationPolicy
}

func (t FinancialTool) ValidateFinancial(data FinancialData) (ValidationResult, error) {
	return ValidateFinancialData(data, t.Policy)
}
