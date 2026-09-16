package documentintelligence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestScoreFinancialCaseSeparatesExtractionAndValidationQuality(t *testing.T) {
	data := validData()
	validation, err := ValidateFinancialData(data, DefaultValidationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	caseDef := FinancialEvaluationCase{
		CaseID:                   "valid-invoice",
		Expected:                 data,
		ExpectedValidationStatus: FindingStatusPass,
	}
	inputTokens, outputTokens, latency := 120, 35, int64(240)
	cost := 0.0012
	score := ScoreFinancialCase(caseDef, mustJSON(data), validation, FinancialOperationalMetrics{
		TotalLatencyMS: &latency, InputTokens: &inputTokens, OutputTokens: &outputTokens, EstimatedCost: &cost, CostCurrency: "USD",
	})
	if !score.SchemaValid || score.FieldAccuracy == nil || *score.FieldAccuracy != 1 {
		t.Fatalf("extraction score = %+v", score)
	}
	if !score.ValidationStatusMatch || score.ValidationIssueDetected {
		t.Fatalf("validation score = %+v", score)
	}
	if score.Operational.EstimatedCost == nil || *score.Operational.EstimatedCost != cost {
		t.Fatalf("operational score = %+v", score.Operational)
	}
}

func TestScoreFinancialCaseMeasuresValidationDetectionSeparately(t *testing.T) {
	data := validData()
	data.Total = str("106.02")
	data.LineItems[0].Amount = str("99.00")
	validation, err := ValidateFinancialData(data, DefaultValidationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	caseDef := FinancialEvaluationCase{
		CaseID:                   "inconsistent-invoice",
		Expected:                 data,
		ExpectedValidationStatus: FindingStatusFailure,
		ExpectedValidationRules:  []string{"line_item.extension", "subtotal.line_items", "total.reconciliation"},
	}
	score := ScoreFinancialCase(caseDef, mustJSON(data), validation, FinancialOperationalMetrics{})
	if !score.SchemaValid || score.FieldAccuracy == nil || *score.FieldAccuracy != 1 {
		t.Fatalf("schema/field score = %+v", score)
	}
	if !score.ValidationIssueDetected || !score.ValidationStatusMatch || score.ValidationRuleRecall == nil || *score.ValidationRuleRecall != 1 {
		t.Fatalf("validation detection score = %+v", score)
	}
	aggregate := AggregateFinancialEvaluation("financial-eval-v1", []FinancialCaseScore{score})
	if aggregate.SchemaValidRate == nil || *aggregate.SchemaValidRate != 1 || aggregate.ValidationDetectionRate == nil || *aggregate.ValidationDetectionRate != 1 {
		t.Fatalf("aggregate = %+v", aggregate)
	}
}

func TestScoreFinancialCaseCountsMalformedOutputAsSchemaFailure(t *testing.T) {
	caseDef := FinancialEvaluationCase{
		CaseID:                   "malformed",
		Expected:                 validData(),
		ExpectedValidationStatus: FindingStatusFailure,
		ExpectedValidationRules:  []string{"required.total"},
	}
	score := ScoreFinancialCase(caseDef, []byte("not json"), ValidationResult{}, FinancialOperationalMetrics{})
	if score.SchemaValid || score.FieldAccuracy != nil || score.SchemaError == "" {
		t.Fatalf("malformed output was not measured as schema failure: %+v", score)
	}
	if score.ValidationIssueDetected || score.ValidationStatusMatch {
		t.Fatalf("invalid output should not fabricate validation quality: %+v", score)
	}
}

func TestScorePersistedAnalysisUsesResultAndTraceMetrics(t *testing.T) {
	data := validData()
	validation, err := ValidateFinancialData(data, DefaultValidationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	findings, err := json.Marshal(validation.Findings)
	if err != nil {
		t.Fatal(err)
	}
	status := validation.Status
	latency := int64(400)
	inputTokens, outputTokens := 90, 20
	analysis := Analysis{
		StructuredData:     mustJSON(data),
		ValidationStatus:   &status,
		ValidationFindings: findings,
		InputTokens:        &inputTokens,
		OutputTokens:       &outputTokens,
		Spans:              []Span{{SpanName: StageRequestSpan, DurationMS: latency}, {SpanName: StageSummarize, DurationMS: 120}},
	}
	score := ScorePersistedAnalysis(FinancialEvaluationCase{
		CaseID: "persisted", Expected: data, ExpectedValidationStatus: FindingStatusPass,
	}, analysis)
	if !score.SchemaValid || score.FieldAccuracy == nil || !score.ValidationStatusMatch {
		t.Fatalf("score = %+v", score)
	}
	if score.Operational.TotalLatencyMS == nil || *score.Operational.TotalLatencyMS != latency || score.Operational.StageLatencyMS[StageSummarize] != 120 {
		t.Fatalf("trace metrics = %+v", score.Operational)
	}
}

func TestDecodeFinancialEvaluationSetValidatesLabels(t *testing.T) {
	data, err := json.Marshal(FinancialEvaluationSet{
		Version:          "financial-eval-v1",
		SchemaVersion:    SchemaVersionV1,
		ValidationPolicy: ValidationPolicyVersionV1,
		Cases: []FinancialEvaluationCase{{
			CaseID: "valid", Expected: validData(), ExpectedValidationStatus: FindingStatusPass,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeFinancialEvaluationSet(data); err != nil {
		t.Fatal(err)
	}
	data = []byte(`{"version":"financial-eval-v1","schema_version":"financial-v1","validation_policy_version":"financial-validation-v1","cases":[{"case_id":"x","expected_validation_status":"bogus","expected":{"schema_version":"financial-v1","document_type":"invoice","line_items":[]}}]}`)
	if _, err := DecodeFinancialEvaluationSet(data); err == nil {
		t.Fatal("expected invalid expected invoice labels to be rejected")
	}
}

func TestCheckedInFinancialEvaluationSet(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate the package source")
	}
	fixturePath := filepath.Join(filepath.Dir(sourceFile), "../../../db/fixtures/financial/financial_dataset_v1.json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	set, err := DecodeFinancialEvaluationSet(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Cases) != 3 {
		t.Fatalf("labeled case count = %d, want 3", len(set.Cases))
	}
}
