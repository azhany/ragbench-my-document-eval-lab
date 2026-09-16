package documentintelligence

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"ragbench-my/backend/internal/providers"
)

type fakeTextTool struct {
	order *[]string
	e     TextEvidence
	err   error
}

func (f fakeTextTool) ExtractText(context.Context, SourceDocument) (TextEvidence, error) {
	*f.order = append(*f.order, StageExtractText)
	return f.e, f.err
}

type fakeStructuredTool struct {
	order   *[]string
	outputs []providers.GenerationResult
	errors  []error
	calls   int
}

func (f *fakeStructuredTool) ExtractStructured(context.Context, TextEvidence, providers.GenerationProfile, string) (providers.GenerationResult, error) {
	*f.order = append(*f.order, StageStructuredExtract)
	index := f.calls
	f.calls++
	if index >= len(f.outputs) {
		index = len(f.outputs) - 1
	}
	var err error
	if index >= 0 && index < len(f.errors) {
		err = f.errors[index]
	}
	return f.outputs[index], err
}

type fakeSchemaTool struct{ order *[]string }

func (f fakeSchemaTool) ValidateSchema(raw []byte) (FinancialData, error) {
	*f.order = append(*f.order, StageSchemaValidate)
	return ParseFinancialData(raw)
}

type fakeFinancialTool struct {
	order  *[]string
	err    error
	result ValidationResult
}

func (f fakeFinancialTool) ValidateFinancial(data FinancialData) (ValidationResult, error) {
	*f.order = append(*f.order, StageFinancialValidate)
	if f.err != nil {
		return ValidationResult{}, f.err
	}
	if f.result.Status != "" {
		return f.result, nil
	}
	return ValidationResult{PolicyVersion: ValidationPolicyVersionV1, Status: FindingStatusPass, Findings: []ValidationFinding{}}, nil
}

type fakeSummaryTool struct {
	order *[]string
	err   error
}

func (f fakeSummaryTool) Summarize(context.Context, FinancialData, ValidationResult, providers.GenerationProfile) (providers.GenerationResult, error) {
	*f.order = append(*f.order, StageSummarize)
	if f.err != nil {
		return providers.GenerationResult{}, f.err
	}
	return providers.GenerationResult{Text: "summary"}, nil
}

func workflowAgent(order *[]string, structured *fakeStructuredTool, financial fakeFinancialTool, summary fakeSummaryTool) Agent {
	structured.order = order
	return Agent{
		ExtractTextTool:       fakeTextTool{order: order, e: TextEvidence{Method: "pdf_text", Profile: "test", RawText: "Invoice evidence", SourceChecksum: "checksum"}},
		StructuredExtractTool: structured,
		SchemaValidateTool:    fakeSchemaTool{order: order},
		FinancialValidateTool: financial,
		SummaryTool:           summary,
		MaxStructuredRetries:  1,
		TraceID:               func() string { return "trace-1" },
	}
}

func TestAgentRunsNamedToolsInOrderAndOnlySummarizesAfterValidation(t *testing.T) {
	order := []string{}
	structured := &fakeStructuredTool{outputs: []providers.GenerationResult{{Text: validFinancialJSON}}}
	financial := fakeFinancialTool{order: &order}
	summary := fakeSummaryTool{order: &order}
	agent := workflowAgent(&order, structured, financial, summary)
	result, err := agent.Run(context.Background(), SourceDocument{DocumentID: "doc-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{StageExtractText, StageStructuredExtract, StageSchemaValidate, StageFinancialValidate, StageSummarize}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("tool order = %v, want %v", order, want)
	}
	if result.Summary != "summary" || result.TraceID != "trace-1" {
		t.Fatalf("result = %+v", result)
	}
}

func TestAgentRetriesMalformedStructuredOutputButNotFinancialValidation(t *testing.T) {
	order := []string{}
	structured := &fakeStructuredTool{outputs: []providers.GenerationResult{{Text: "not-json"}, {Text: validFinancialJSON}}}
	agent := workflowAgent(&order, structured, fakeFinancialTool{order: &order}, fakeSummaryTool{order: &order})
	if _, err := agent.Run(context.Background(), SourceDocument{}); err != nil {
		t.Fatal(err)
	}
	want := []string{StageExtractText, StageStructuredExtract, StageSchemaValidate, StageStructuredExtract, StageSchemaValidate, StageFinancialValidate, StageSummarize}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("retry order = %v, want %v", order, want)
	}
	if structured.calls != 2 {
		t.Fatalf("structured calls = %d, want bounded retry of 2", structured.calls)
	}
}

func TestAgentStopsAfterExtractionFailure(t *testing.T) {
	order := []string{}
	structured := &fakeStructuredTool{outputs: []providers.GenerationResult{{Text: validFinancialJSON}}}
	agent := workflowAgent(&order, structured, fakeFinancialTool{order: &order}, fakeSummaryTool{order: &order})
	agent.ExtractTextTool = fakeTextTool{order: &order, err: errors.New("source unreadable")}
	result, err := agent.Run(context.Background(), SourceDocument{})
	var stageErr *StageError
	if !errors.As(err, &stageErr) || stageErr.Stage != StageExtractText || result.Summary != "" {
		t.Fatalf("extraction failure = %v, result = %+v", err, result)
	}
	if structured.calls != 0 || len(order) != 1 || order[0] != StageExtractText {
		t.Fatalf("later tools ran after extraction failure: order=%v calls=%d", order, structured.calls)
	}
}

func TestAgentStopsBeforeSummaryOnStageFailure(t *testing.T) {
	order := []string{}
	structured := &fakeStructuredTool{outputs: []providers.GenerationResult{{Text: validFinancialJSON}}}
	agent := workflowAgent(&order, structured, fakeFinancialTool{order: &order, err: errors.New("validation tool unavailable")}, fakeSummaryTool{order: &order})
	result, err := agent.Run(context.Background(), SourceDocument{})
	var stageErr *StageError
	if !errors.As(err, &stageErr) || stageErr.Stage != StageFinancialValidate {
		t.Fatalf("error = %v, want financial validation StageError", err)
	}
	for _, name := range order {
		if name == StageSummarize {
			t.Fatalf("summary ran after validation failure: order=%v result=%+v", order, result)
		}
	}
	if result.Summary != "" {
		t.Fatalf("summary ran after validation failure: order=%v result=%+v", order, result)
	}
}

func TestAgentValidationWarningIsIncludedInSummary(t *testing.T) {
	order := []string{}
	structured := &fakeStructuredTool{outputs: []providers.GenerationResult{{Text: validFinancialJSON}}}
	financial := fakeFinancialTool{order: &order, result: ValidationResult{
		PolicyVersion: ValidationPolicyVersionV1, Status: FindingStatusWarning,
		Findings: []ValidationFinding{{RuleID: "required.total", Status: FindingStatusWarning, Message: "total is missing"}},
	}}
	agent := workflowAgent(&order, structured, financial, fakeSummaryTool{order: &order})
	result, err := agent.Run(context.Background(), SourceDocument{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Summary, "Validation warning") {
		t.Fatalf("summary did not preserve validation warning: %q", result.Summary)
	}
}

func TestAgentReportsSummaryProviderFailureAfterValidation(t *testing.T) {
	order := []string{}
	structured := &fakeStructuredTool{outputs: []providers.GenerationResult{{Text: validFinancialJSON}}}
	agent := workflowAgent(&order, structured, fakeFinancialTool{order: &order}, fakeSummaryTool{order: &order, err: errors.New("provider unavailable")})
	result, err := agent.Run(context.Background(), SourceDocument{})
	var stageErr *StageError
	if !errors.As(err, &stageErr) || stageErr.Stage != StageSummarize {
		t.Fatalf("summary failure = %v", err)
	}
	if result.Summary != "" || !reflect.DeepEqual(order, []string{StageExtractText, StageStructuredExtract, StageSchemaValidate, StageFinancialValidate, StageSummarize}) {
		t.Fatalf("summary failure order/result = %v/%+v", order, result)
	}
}
