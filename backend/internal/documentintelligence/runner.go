package documentintelligence

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"ragbench-my/backend/internal/providers"
)

const (
	DefaultStructuredRetries = 1
	DefaultSummaryRetries    = 1
	StageTimeout             = 2 * time.Minute
	MaxSummaryChars          = 4000
)

// Runner executes one persisted Airflow stage. It keeps provider calls out of
// database row locks and writes the result, event, span, and state transition
// atomically through Store.
type Runner struct {
	Store                *Store
	Generator            providers.Generator
	MaxStructuredRetries int
	MaxSummaryRetries    int
	Now                  func() time.Time
}

func (r *Runner) RunStage(ctx context.Context, request StageRequest) (Analysis, error) {
	if r.Store == nil {
		return Analysis{}, &StageError{Stage: request.Stage, Code: "workflow_not_configured", Err: errors.New("analysis store is not configured")}
	}
	if request.Stage == "" {
		return Analysis{}, &StageError{Stage: StageRequestSpan, Code: "invalid_stage", Err: errors.New("stage is required")}
	}
	ctx, cancel := context.WithTimeout(ctx, StageTimeout)
	defer cancel()
	input, done, err := r.Store.claimStage(ctx, request)
	if err != nil {
		return Analysis{}, err
	}
	if done {
		return r.Store.Get(ctx, request.AnalysisID)
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	started := now()

	fail := func(code string, cause error, update StageUpdate) (Analysis, error) {
		if code == "" {
			code = errorCode(cause, "stage_failed")
		}
		if cause == nil {
			cause = errors.New(code)
		}
		update.DurationMS = now().Sub(started).Milliseconds()
		update.ToolEvent.Name = request.Stage
		if update.ToolEvent.Attempt <= 0 {
			update.ToolEvent.Attempt = 1
		}
		update.ToolEvent.StartedAt = started.UTC().Format(time.RFC3339Nano)
		update.ToolEvent.Status = "failed"
		update.ToolEvent.ErrorCode = code
		if persistErr := r.Store.FailStage(ctx, request.AnalysisID, request.Stage, code, safeErrorMessage(cause), update); persistErr != nil {
			return Analysis{}, persistErr
		}
		return Analysis{}, &StageError{Stage: request.Stage, Code: code, Err: cause}
	}

	switch request.Stage {
	case StageExtractText:
		return r.runExtractStage(ctx, request, input, started, now, fail)
	case StageStructuredExtract:
		return r.runStructuredStage(ctx, request, input, started, now, fail)
	case StageSchemaValidate:
		return r.runSchemaStage(ctx, request, input, started, now, fail)
	case StageFinancialValidate:
		return r.runFinancialStage(ctx, request, input, started, now, fail)
	case StageSummarize:
		return r.runSummaryStage(ctx, request, input, started, now, fail)
	default:
		return fail("invalid_stage", errors.New("unknown document-intelligence stage"), StageUpdate{})
	}
}

type failFunc func(string, error, StageUpdate) (Analysis, error)

func (r *Runner) completeStage(ctx context.Context, analysisID, stage string, update StageUpdate, started time.Time, now func() time.Time) (Analysis, error) {
	if err := r.Store.CompleteStage(ctx, analysisID, stage, update); err != nil {
		// The stage claim is committed before provider work begins. If the
		// result transaction itself fails, leave a classified terminal failure
		// so an Airflow retry cannot strand the analysis in processing. FailStage
		// is guarded against overwriting a completion in an ambiguous commit.
		failure := update
		failure.DurationMS = now().Sub(started).Milliseconds()
		failure.ToolEvent.Name = stage
		failure.ToolEvent.Status = "failed"
		failure.ToolEvent.ErrorCode = "persistence_failed"
		if failure.ToolEvent.Attempt <= 0 {
			failure.ToolEvent.Attempt = 1
		}
		if failure.ToolEvent.StartedAt == "" {
			failure.ToolEvent.StartedAt = started.UTC().Format(time.RFC3339Nano)
		}
		if failure.Metadata == nil {
			failure.Metadata = map[string]any{}
		}
		failure.Metadata["persistence_error"] = true
		if persistErr := r.Store.FailStage(ctx, analysisID, stage, "persistence_failed", "stage output could not be persisted", failure); persistErr == nil {
			return Analysis{}, &StageError{Stage: stage, Code: "persistence_failed", Err: errors.New("stage output could not be persisted")}
		}
		return Analysis{}, err
	}
	return r.Store.Get(ctx, analysisID)
}

func (r *Runner) runExtractStage(ctx context.Context, request StageRequest, input stageInput, started time.Time, now func() time.Time, fail failFunc) (Analysis, error) {
	if len(request.Evidence) == 0 {
		return fail("extraction_evidence_missing", errors.New("extract_text must persist extraction evidence"), StageUpdate{Metadata: map[string]any{"method": request.ExtractionMethod}})
	}
	var evidence TextEvidence
	decoder := json.NewDecoder(strings.NewReader(string(request.Evidence)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return fail("extraction_evidence_invalid", errors.New("extraction evidence has an invalid shape"), StageUpdate{})
	}
	if strings.TrimSpace(evidence.SourceChecksum) == "" {
		evidence.SourceChecksum = input.SourceChecksum
	}
	if evidence.SourceChecksum != input.SourceChecksum || (request.SourceChecksum != "" && request.SourceChecksum != input.SourceChecksum) {
		return fail("source_changed", errors.New("extraction evidence does not match the immutable source checksum"), StageUpdate{})
	}
	evidence.RawText = strings.TrimSpace(evidence.RawText)
	if evidence.RawText == "" && len(evidence.Sections) > 0 {
		parts := make([]string, 0, len(evidence.Sections))
		for _, section := range evidence.Sections {
			parts = append(parts, strings.TrimSpace(section.Text))
		}
		evidence.RawText = strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if evidence.RawText == "" {
		return fail("empty_extraction", errors.New("extraction produced no useful evidence"), StageUpdate{})
	}
	if len([]byte(evidence.RawText)) > MaxEvidenceBytes {
		return fail("extraction_limit", errors.New("extraction evidence exceeds the bounded size"), StageUpdate{})
	}
	if len(evidence.Sections) == 0 {
		evidence.Sections = []EvidenceSection{{Text: evidence.RawText, Location: map[string]any{"source": "document"}}}
	}
	if evidence.Method == "" {
		evidence.Method = request.ExtractionMethod
	}
	if evidence.Profile == "" {
		evidence.Profile = request.ExtractionProfile
	}
	if evidence.Method == "" {
		return fail("extraction_method_missing", errors.New("extraction method is required"), StageUpdate{})
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return fail("extraction_evidence_invalid", errors.New("extraction evidence could not be persisted"), StageUpdate{})
	}
	metadata := map[string]any{"method": evidence.Method, "profile": evidence.Profile,
		"characters": len([]rune(evidence.RawText)), "sections": len(evidence.Sections)}
	update := StageUpdate{Metadata: metadata, Evidence: evidenceJSON, ExtractionMethod: evidence.Method,
		ExtractionProfile: evidence.Profile, DurationMS: now().Sub(started).Milliseconds(),
		ToolEvent: ToolEvent{Name: StageExtractText, Attempt: 1, StartedAt: started.UTC().Format(time.RFC3339Nano), Status: "succeeded"}}
	return r.completeStage(ctx, request.AnalysisID, request.Stage, update, started, now)
}

func (r *Runner) runStructuredStage(ctx context.Context, request StageRequest, input stageInput, started time.Time, now func() time.Time, fail failFunc) (Analysis, error) {
	var evidence TextEvidence
	if err := json.Unmarshal(input.ExtractionEvidence, &evidence); err != nil || strings.TrimSpace(evidence.RawText) == "" {
		return fail("extraction_evidence_missing", errors.New("structured extraction requires persisted extraction evidence"), StageUpdate{})
	}
	profile, err := providers.GenerationProfileByName(input.ModelProfile)
	if err != nil {
		return fail("capability_unavailable", err, StageUpdate{})
	}
	if r.Generator == nil {
		return fail(providers.ErrCodeModelFailed, errors.New("structured extraction provider is not configured"), StageUpdate{})
	}
	prompt, err := BuildExtractionPrompt(evidence.RawText)
	if err != nil {
		return fail("prompt_failed", err, StageUpdate{})
	}
	promptSnapshot := mustJSON(map[string]any{"extraction": map[string]any{
		"version": ExtractionPromptV1, "identifier": ExtractionPromptIdentifier,
		"schema_version": SchemaVersionV1, "model_profile": input.ModelProfile, "prompt_text": prompt,
	}})
	maxRetries := r.MaxStructuredRetries
	if maxRetries == 0 {
		maxRetries = DefaultStructuredRetries
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	var (
		lastRaw                 string
		lastResult              providers.GenerationResult
		lastErr                 error
		schemaErr               error
		totalInput, totalOutput *int
	)
	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		result, callErr := ProviderStructuredTool{Generator: r.Generator}.ExtractStructured(ctx, evidence, profile, prompt)
		lastResult = result
		totalInput = mergeUsage(totalInput, result.InputTokens)
		totalOutput = mergeUsage(totalOutput, result.OutputTokens)
		if callErr != nil {
			schemaErr = nil
			lastErr = callErr
			if attempt <= maxRetries && retryableStructuredError(callErr) {
				continue
			}
			return fail(errorCode(callErr, "structured_extract_failed"), callErr, StageUpdate{
				Metadata:       map[string]any{"provider": profile.Provider, "model": profile.Model, "attempt": attempt},
				PromptSnapshot: promptSnapshot,
				InputTokens:    totalInput, OutputTokens: totalOutput,
			})
		}
		lastRaw = result.Text
		_, schemaErr = ParseFinancialData([]byte(lastRaw))
		if schemaErr == nil {
			lastErr = nil
			break
		}
		lastErr = schemaErr
		if attempt <= maxRetries {
			continue
		}
	}
	metadata := map[string]any{"provider": profile.Provider, "model": profile.Model, "attempts": maxRetries + 1}
	if lastResult.Model != "" {
		metadata["response_model"] = lastResult.Model
	}
	inputTokens := mergeUsage(input.InputTokens, totalInput)
	outputTokens := mergeUsage(input.OutputTokens, totalOutput)
	cost, currency, pricing, components := providerCost(profile, inputTokens, outputTokens)
	update := StageUpdate{Metadata: metadata, RawModelOutput: &lastRaw, PromptSnapshot: promptSnapshot,
		InputTokens: inputTokens, OutputTokens: outputTokens, EstimatedCost: cost, CostCurrency: currency,
		PricingVersion: pricing, CostComponents: components,
		ToolEvent: ToolEvent{Name: StageStructuredExtract, Attempt: maxRetries + 1, StartedAt: started.UTC().Format(time.RFC3339Nano)}}
	if lastErr != nil {
		code := schemaFailureCode(lastRaw, lastErr)
		if schemaErr != nil {
			update.SchemaErrors = schemaErrorJSON(schemaErr)
		}
		return fail(code, lastErr, update)
	}
	update.DurationMS = now().Sub(started).Milliseconds()
	update.ToolEvent.Status = "succeeded"
	return r.completeStage(ctx, request.AnalysisID, request.Stage, update, started, now)
}

func (r *Runner) runSchemaStage(ctx context.Context, request StageRequest, input stageInput, started time.Time, now func() time.Time, fail failFunc) (Analysis, error) {
	data, err := ParseFinancialData(input.RawModelBytes())
	if err != nil {
		return fail("schema_invalid", err, StageUpdate{SchemaErrors: schemaErrorJSON(err)})
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fail("schema_invalid", err, StageUpdate{})
	}
	update := StageUpdate{StructuredData: dataJSON, SchemaErrors: json.RawMessage(`[]`), DurationMS: now().Sub(started).Milliseconds(),
		Metadata:  map[string]any{"schema_version": SchemaVersionV1},
		ToolEvent: ToolEvent{Name: StageSchemaValidate, Attempt: 1, StartedAt: started.UTC().Format(time.RFC3339Nano), Status: "succeeded"}}
	return r.completeStage(ctx, request.AnalysisID, request.Stage, update, started, now)
}

func (r *Runner) runFinancialStage(ctx context.Context, request StageRequest, input stageInput, started time.Time, now func() time.Time, fail failFunc) (Analysis, error) {
	data, err := ParseFinancialData(input.StructuredData)
	if err != nil {
		return fail("schema_invalid", err, StageUpdate{SchemaErrors: schemaErrorJSON(err)})
	}
	policy, err := PolicyByVersion(input.ValidationPolicyVersion)
	if err != nil {
		return fail("validation_policy_unavailable", err, StageUpdate{})
	}
	validation, err := ValidateFinancialData(data, policy)
	if err != nil {
		return fail("validation_failed", err, StageUpdate{})
	}
	findingsJSON, err := json.Marshal(validation.Findings)
	if err != nil {
		return fail("validation_failed", err, StageUpdate{})
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return fail("validation_failed", err, StageUpdate{})
	}
	update := StageUpdate{ValidationStatus: validation.Status, ValidationFindings: findingsJSON, ValidationPolicy: policyJSON,
		DurationMS: now().Sub(started).Milliseconds(), Metadata: map[string]any{"status": validation.Status, "finding_count": len(validation.Findings)},
		ToolEvent: ToolEvent{Name: StageFinancialValidate, Attempt: 1, StartedAt: started.UTC().Format(time.RFC3339Nano), Status: "succeeded"}}
	return r.completeStage(ctx, request.AnalysisID, request.Stage, update, started, now)
}

func (r *Runner) runSummaryStage(ctx context.Context, request StageRequest, input stageInput, started time.Time, now func() time.Time, fail failFunc) (Analysis, error) {
	data, err := ParseFinancialData(input.StructuredData)
	if err != nil {
		return fail("schema_invalid", err, StageUpdate{})
	}
	if input.ValidationStatus == nil || len(input.ValidationFindings) == 0 || len(input.ValidationPolicy) == 0 {
		return fail("validation_missing", errors.New("summary requires persisted validation output"), StageUpdate{})
	}
	var findings []ValidationFinding
	if err := json.Unmarshal(input.ValidationFindings, &findings); err != nil {
		return fail("validation_missing", errors.New("persisted validation findings are unreadable"), StageUpdate{})
	}
	var policy ValidationPolicy
	if err := json.Unmarshal(input.ValidationPolicy, &policy); err != nil {
		return fail("validation_missing", errors.New("persisted validation policy is unreadable"), StageUpdate{})
	}
	validation := ValidationResult{PolicyVersion: policy.Version, Status: *input.ValidationStatus, Findings: findings, Policy: policy}
	profile, err := providers.GenerationProfileByName(input.ModelProfile)
	if err != nil {
		return fail("capability_unavailable", err, StageUpdate{})
	}
	if r.Generator == nil {
		return fail(providers.ErrCodeModelFailed, errors.New("summary provider is not configured"), StageUpdate{})
	}
	prompt, err := BuildSummaryPrompt(data, validation)
	if err != nil {
		return fail("summary_prompt_failed", err, StageUpdate{})
	}
	promptSnapshot := mustJSON(map[string]any{"summary": map[string]any{
		"version": SummaryPromptV1, "identifier": SummaryPromptIdentifier, "model_profile": input.ModelProfile, "prompt_text": prompt,
	}})
	maxRetries := r.MaxSummaryRetries
	if maxRetries == 0 {
		maxRetries = DefaultSummaryRetries
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	var result providers.GenerationResult
	var totalInput, totalOutput *int
	stageUpdate := func(metadata map[string]any, promptSnapshot json.RawMessage) StageUpdate {
		inputTokens := mergeUsage(input.InputTokens, totalInput)
		outputTokens := mergeUsage(input.OutputTokens, totalOutput)
		cost, currency, pricing, components := providerCost(profile, inputTokens, outputTokens)
		return StageUpdate{Metadata: metadata, PromptSnapshot: promptSnapshot,
			InputTokens: inputTokens, OutputTokens: outputTokens, EstimatedCost: cost,
			CostCurrency: currency, PricingVersion: pricing, CostComponents: components}
	}
	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		result, err = r.Generator.Generate(ctx, profile, prompt)
		totalInput = mergeUsage(totalInput, result.InputTokens)
		totalOutput = mergeUsage(totalOutput, result.OutputTokens)
		if err == nil {
			break
		}
		if attempt > maxRetries || !retryableStructuredError(err) {
			return fail(errorCode(err, "summary_provider_failed"), err, stageUpdate(
				map[string]any{"provider": profile.Provider, "model": profile.Model, "attempt": attempt}, promptSnapshot))
		}
	}
	if err != nil {
		return fail(errorCode(err, "summary_provider_failed"), err, stageUpdate(nil, promptSnapshot))
	}
	if strings.TrimSpace(result.Text) == "" {
		return fail("summary_malformed_response", errors.New("summary provider returned empty text"), stageUpdate(nil, promptSnapshot))
	}
	summary := appendValidationNotice(result.Text, validation)
	if len([]rune(summary)) > MaxSummaryChars {
		return fail("summary_malformed_response", errors.New("summary provider returned more than the bounded summary length"), stageUpdate(nil, promptSnapshot))
	}
	inputTokens := mergeUsage(input.InputTokens, totalInput)
	outputTokens := mergeUsage(input.OutputTokens, totalOutput)
	cost, currency, pricing, components := providerCost(profile, inputTokens, outputTokens)
	metadata := map[string]any{"provider": profile.Provider, "model": profile.Model, "validation_status": validation.Status}
	if result.Model != "" {
		metadata["response_model"] = result.Model
	}
	update := StageUpdate{Summary: &summary, PromptSnapshot: promptSnapshot, InputTokens: inputTokens, OutputTokens: outputTokens,
		EstimatedCost: cost, CostCurrency: currency, PricingVersion: pricing, CostComponents: components,
		DurationMS: now().Sub(started).Milliseconds(), Metadata: metadata,
		ToolEvent: ToolEvent{Name: StageSummarize, Attempt: maxRetries + 1, StartedAt: started.UTC().Format(time.RFC3339Nano), Status: "succeeded"}}
	return r.completeStage(ctx, request.AnalysisID, request.Stage, update, started, now)
}

func (input stageInput) RawModelBytes() []byte {
	if input.RawModelOutput == nil {
		return nil
	}
	return []byte(*input.RawModelOutput)
}

func schemaErrorJSON(err error) json.RawMessage {
	var schemaErr *SchemaError
	if errors.As(err, &schemaErr) {
		return mustJSON(schemaErr.Violations)
	}
	return mustJSON([]SchemaViolation{{Path: "$", Code: "schema_invalid", Message: "structured output failed schema validation"}})
}

func schemaFailureCode(raw string, err error) string {
	if !json.Valid([]byte(raw)) {
		return "malformed_model_output"
	}
	return errorCode(err, "schema_invalid")
}

func providerCost(profile providers.GenerationProfile, inputTokens, outputTokens *int) (*float64, string, string, json.RawMessage) {
	components := map[string]any{"available": false}
	if inputTokens == nil || outputTokens == nil {
		components["reason"] = "provider_usage_unavailable"
		return nil, "", "", mustJSON(components)
	}
	rate, ok := providers.RateFor(profile.Provider, profile.Model)
	if !ok {
		components["reason"] = "provider_pricing_unavailable"
		return nil, "", "", mustJSON(components)
	}
	inputCost := float64(*inputTokens) * rate.InputPerMillion / 1_000_000
	outputCost := float64(*outputTokens) * rate.OutputPerMillion / 1_000_000
	total := inputCost + outputCost
	components = map[string]any{"available": true, "input": inputCost, "output": outputCost, "provider": profile.Provider, "model": profile.Model}
	return &total, providers.PricingCurrency, providers.PricingVersion, mustJSON(components)
}
