package documentintelligence

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// FinancialEvaluationCase is one labeled assessment example. The expected
// structured data is separate from the expected validation outcome so an
// evaluator can tell extraction quality from rule-detection quality.
type FinancialEvaluationCase struct {
	CaseID                   string        `json:"case_id"`
	Fixture                  string        `json:"fixture"`
	Description              string        `json:"description"`
	Expected                 FinancialData `json:"expected"`
	ExpectedValidationStatus string        `json:"expected_validation_status"`
	ExpectedValidationRules  []string      `json:"expected_validation_rules"`
}

// FinancialEvaluationSet is a small, versioned, provider-independent label
// set. It is intentionally data-only; provider calls and workflow execution
// remain in the normal analysis path.
type FinancialEvaluationSet struct {
	Version          string                    `json:"version"`
	SchemaVersion    string                    `json:"schema_version"`
	ValidationPolicy string                    `json:"validation_policy_version"`
	Cases            []FinancialEvaluationCase `json:"cases"`
}

// FinancialOperationalMetrics are copied from a persisted analysis when a
// case is scored. They are reported independently of extraction and
// validation quality; unavailable provider usage/cost remains nil.
type FinancialOperationalMetrics struct {
	TotalLatencyMS *int64           `json:"total_latency_ms,omitempty"`
	StageLatencyMS map[string]int64 `json:"stage_latency_ms,omitempty"`
	InputTokens    *int             `json:"input_tokens,omitempty"`
	OutputTokens   *int             `json:"output_tokens,omitempty"`
	EstimatedCost  *float64         `json:"estimated_cost,omitempty"`
	CostCurrency   string           `json:"cost_currency,omitempty"`
	PricingVersion string           `json:"pricing_version,omitempty"`
}

// FinancialCaseScore keeps schema/field quality, validation detection, and
// operational measurements as different dimensions. A nil accuracy/recall
// means the dimension was not evaluable, never an invented zero.
type FinancialCaseScore struct {
	CaseID                  string                      `json:"case_id"`
	SchemaValid             bool                        `json:"schema_valid"`
	SchemaError             string                      `json:"schema_error,omitempty"`
	FieldMatches            int                         `json:"field_matches"`
	FieldCount              int                         `json:"field_count"`
	FieldAccuracy           *float64                    `json:"field_accuracy,omitempty"`
	ExpectedValidation      string                      `json:"expected_validation_status"`
	ActualValidation        string                      `json:"actual_validation_status,omitempty"`
	ValidationStatusMatch   bool                        `json:"validation_status_match"`
	ValidationIssueExpected bool                        `json:"validation_issue_expected"`
	ValidationIssueDetected bool                        `json:"validation_issue_detected"`
	ExpectedRuleCount       int                         `json:"expected_rule_count"`
	MatchedRuleCount        int                         `json:"matched_rule_count"`
	ActualIssueRuleCount    int                         `json:"actual_issue_rule_count"`
	ValidationRulePrecision *float64                    `json:"validation_rule_precision,omitempty"`
	ValidationRuleRecall    *float64                    `json:"validation_rule_recall,omitempty"`
	Operational             FinancialOperationalMetrics `json:"operational"`
}

// FinancialEvaluationAggregate is the repeatable labeled-set summary. Its
// quality dimensions are intentionally not merged with latency or cost.
type FinancialEvaluationAggregate struct {
	SetVersion                  string   `json:"set_version"`
	TotalCases                  int      `json:"total_cases"`
	SchemaValidCases            int      `json:"schema_valid_cases"`
	SchemaValidRate             *float64 `json:"schema_valid_rate,omitempty"`
	FieldAccuracyPopulation     int      `json:"field_accuracy_population"`
	FieldAccuracyMean           *float64 `json:"field_accuracy_mean,omitempty"`
	ValidationStatusMatchCount  int      `json:"validation_status_match_count"`
	ValidationIssueCases        int      `json:"validation_issue_cases"`
	ValidationDetectedCases     int      `json:"validation_detected_cases"`
	ValidationDetectionRate     *float64 `json:"validation_detection_rate,omitempty"`
	ValidationRulePopulation    int      `json:"validation_rule_population"`
	ValidationRulePrecisionMean *float64 `json:"validation_rule_precision_mean,omitempty"`
	ValidationRuleRecallMean    *float64 `json:"validation_rule_recall_mean,omitempty"`
	LatencyPopulation           int      `json:"latency_population"`
	LatencyP50MS                *int64   `json:"latency_p50_ms,omitempty"`
	LatencyP95MS                *int64   `json:"latency_p95_ms,omitempty"`
	CostPopulation              int      `json:"cost_population"`
	CostTotal                   *float64 `json:"cost_total,omitempty"`
	CostCurrency                string   `json:"cost_currency,omitempty"`
}

// DecodeFinancialEvaluationSet validates the on-disk labeled-set contract
// before it is used by a repeatable evaluator.
func DecodeFinancialEvaluationSet(raw []byte) (FinancialEvaluationSet, error) {
	var set FinancialEvaluationSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return FinancialEvaluationSet{}, fmt.Errorf("decode financial evaluation set: %w", err)
	}
	if err := ValidateFinancialEvaluationSet(set); err != nil {
		return FinancialEvaluationSet{}, err
	}
	return set, nil
}

func ValidateFinancialEvaluationSet(set FinancialEvaluationSet) error {
	if strings.TrimSpace(set.Version) == "" {
		return fmt.Errorf("financial evaluation set version is required")
	}
	if set.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf("financial evaluation set schema version must be %s", SchemaVersionV1)
	}
	if set.ValidationPolicy != ValidationPolicyVersionV1 {
		return fmt.Errorf("financial evaluation set validation policy must be %s", ValidationPolicyVersionV1)
	}
	if len(set.Cases) == 0 {
		return fmt.Errorf("financial evaluation set must contain at least one case")
	}
	seen := make(map[string]bool, len(set.Cases))
	for _, item := range set.Cases {
		if strings.TrimSpace(item.CaseID) == "" || seen[item.CaseID] {
			return fmt.Errorf("financial evaluation case IDs must be non-empty and unique")
		}
		seen[item.CaseID] = true
		if item.ExpectedValidationStatus != FindingStatusPass &&
			item.ExpectedValidationStatus != FindingStatusWarning &&
			item.ExpectedValidationStatus != FindingStatusFailure {
			return fmt.Errorf("financial evaluation case %s has an invalid expected validation status", item.CaseID)
		}
		if _, err := ParseFinancialData(mustJSON(item.Expected)); err != nil {
			return fmt.Errorf("financial evaluation case %s has invalid expected data: %w", item.CaseID, err)
		}
		seenRules := map[string]bool{}
		for _, rule := range item.ExpectedValidationRules {
			if strings.TrimSpace(rule) == "" || seenRules[rule] {
				return fmt.Errorf("financial evaluation case %s has invalid expected validation rules", item.CaseID)
			}
			seenRules[rule] = true
		}
	}
	return nil
}

// ScoreFinancialCase parses one model response and scores it against one
// labeled case. Schema-invalid output is a measured invalid result, not a Go
// evaluator error, because malformed output is itself an important metric.
func ScoreFinancialCase(item FinancialEvaluationCase, actualJSON []byte, validation ValidationResult, operational FinancialOperationalMetrics) FinancialCaseScore {
	score := FinancialCaseScore{
		CaseID: item.CaseID, FieldCount: financialFieldCount(),
		ExpectedValidation:      item.ExpectedValidationStatus,
		ValidationIssueExpected: item.ExpectedValidationStatus != FindingStatusPass || len(item.ExpectedValidationRules) > 0,
		ExpectedRuleCount:       len(item.ExpectedValidationRules),
		Operational:             operational,
	}
	actual, err := ParseFinancialData(actualJSON)
	if err != nil {
		score.SchemaError = err.Error()
		return score
	}
	score.SchemaValid = true
	score.FieldMatches = matchingFinancialFields(item.Expected, actual)
	accuracy := float64(score.FieldMatches) / float64(score.FieldCount)
	score.FieldAccuracy = &accuracy
	score.ActualValidation = validation.Status
	score.ValidationStatusMatch = score.ExpectedValidation == score.ActualValidation
	score.ValidationIssueDetected = validation.Status != "" && validation.Status != FindingStatusPass
	actualRules := validationIssueRules(validation)
	expectedRules := make(map[string]bool, len(item.ExpectedValidationRules))
	for _, rule := range item.ExpectedValidationRules {
		expectedRules[rule] = true
	}
	for rule := range expectedRules {
		if actualRules[rule] {
			score.MatchedRuleCount++
		}
	}
	score.ActualIssueRuleCount = len(actualRules)
	if score.ExpectedRuleCount > 0 {
		recall := float64(score.MatchedRuleCount) / float64(score.ExpectedRuleCount)
		score.ValidationRuleRecall = &recall
	}
	if score.ActualIssueRuleCount > 0 {
		precision := float64(score.MatchedRuleCount) / float64(score.ActualIssueRuleCount)
		score.ValidationRulePrecision = &precision
	}
	return score
}

// ScorePersistedAnalysis adapts the public analysis snapshot to the same
// scorer. This keeps the labeled-set evaluator on the real persisted result
// shape instead of inventing a second execution or storage path.
func ScorePersistedAnalysis(item FinancialEvaluationCase, analysis Analysis) FinancialCaseScore {
	validation := ValidationResult{}
	if analysis.ValidationStatus != nil {
		validation.Status = *analysis.ValidationStatus
		_ = json.Unmarshal(analysis.ValidationFindings, &validation.Findings)
	}
	stageLatency := make(map[string]int64)
	var totalLatency *int64
	for _, span := range analysis.Spans {
		stageLatency[span.SpanName] = span.DurationMS
		if span.SpanName == StageRequestSpan {
			latency := span.DurationMS
			totalLatency = &latency
		}
	}
	return ScoreFinancialCase(item, analysis.StructuredData, validation, FinancialOperationalMetrics{
		TotalLatencyMS: totalLatency,
		StageLatencyMS: stageLatency,
		InputTokens:    analysis.InputTokens,
		OutputTokens:   analysis.OutputTokens,
		EstimatedCost:  analysis.EstimatedCost,
		CostCurrency:   valueOrEmpty(analysis.CostCurrency),
		PricingVersion: valueOrEmpty(analysis.PricingVersion),
	})
}

func AggregateFinancialEvaluation(setVersion string, scores []FinancialCaseScore) FinancialEvaluationAggregate {
	aggregate := FinancialEvaluationAggregate{SetVersion: setVersion, TotalCases: len(scores)}
	var fieldAccuracies, precisions, recalls, costs []float64
	var latencies []int64
	for _, score := range scores {
		if score.SchemaValid {
			aggregate.SchemaValidCases++
		}
		if score.FieldAccuracy != nil {
			aggregate.FieldAccuracyPopulation++
			fieldAccuracies = append(fieldAccuracies, *score.FieldAccuracy)
		}
		if score.ValidationStatusMatch {
			aggregate.ValidationStatusMatchCount++
		}
		if score.ValidationIssueExpected {
			aggregate.ValidationIssueCases++
			if score.ValidationIssueDetected {
				aggregate.ValidationDetectedCases++
			}
		}
		if score.ValidationRuleRecall != nil {
			aggregate.ValidationRulePopulation++
			recalls = append(recalls, *score.ValidationRuleRecall)
		}
		if score.ValidationRulePrecision != nil {
			precisions = append(precisions, *score.ValidationRulePrecision)
		}
		if score.Operational.TotalLatencyMS != nil {
			latencies = append(latencies, *score.Operational.TotalLatencyMS)
		}
		if score.Operational.EstimatedCost != nil {
			aggregate.CostPopulation++
			costs = append(costs, *score.Operational.EstimatedCost)
			if aggregate.CostCurrency == "" {
				aggregate.CostCurrency = score.Operational.CostCurrency
			}
		}
	}
	if len(scores) > 0 {
		rate := float64(aggregate.SchemaValidCases) / float64(len(scores))
		aggregate.SchemaValidRate = &rate
	}
	aggregate.FieldAccuracyMean = financialMean(fieldAccuracies)
	aggregate.ValidationDetectionRate = financialMeanRatio(aggregate.ValidationDetectedCases, aggregate.ValidationIssueCases)
	aggregate.ValidationRuleRecallMean = financialMean(recalls)
	aggregate.ValidationRulePrecisionMean = financialMean(precisions)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	aggregate.LatencyPopulation = len(latencies)
	if len(latencies) > 0 {
		p50, p95 := financialPercentile(latencies, 0.50), financialPercentile(latencies, 0.95)
		aggregate.LatencyP50MS, aggregate.LatencyP95MS = &p50, &p95
	}
	if len(costs) > 0 {
		total := 0.0
		for _, cost := range costs {
			total += cost
		}
		aggregate.CostTotal = &total
	}
	return aggregate
}

func financialFieldCount() int { return 12 }

func matchingFinancialFields(expected, actual FinancialData) int {
	matched := 0
	if expected.SchemaVersion == actual.SchemaVersion {
		matched++
	}
	if expected.DocumentType == actual.DocumentType {
		matched++
	}
	for _, pair := range [][2]*string{
		{expected.Vendor, actual.Vendor}, {expected.InvoiceNumber, actual.InvoiceNumber},
		{expected.InvoiceDate, actual.InvoiceDate}, {expected.DueDate, actual.DueDate},
	} {
		if equalTextValue(pair[0], pair[1]) {
			matched++
		}
	}
	if equalCurrencyValue(expected.Currency, actual.Currency) {
		matched++
	}
	for _, pair := range [][2]*string{
		{expected.Subtotal, actual.Subtotal}, {expected.Tax, actual.Tax},
		{expected.Discount, actual.Discount}, {expected.Total, actual.Total},
	} {
		if equalDecimalValue(pair[0], pair[1]) {
			matched++
		}
	}
	if len(expected.LineItems) == len(actual.LineItems) {
		match := true
		for i := range expected.LineItems {
			left, right := expected.LineItems[i], actual.LineItems[i]
			if !equalTextValue(left.Description, right.Description) ||
				!equalDecimalValue(left.Quantity, right.Quantity) ||
				!equalDecimalValue(left.UnitPrice, right.UnitPrice) ||
				!equalDecimalValue(left.Amount, right.Amount) {
				match = false
			}
		}
		if match {
			matched++
		}
	}
	return matched
}

func equalTextValue(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return strings.TrimSpace(*left) == strings.TrimSpace(*right)
}

func equalCurrencyValue(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return strings.EqualFold(strings.TrimSpace(*left), strings.TrimSpace(*right))
}

func equalDecimalValue(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if l, lok := decimal(strings.TrimSpace(*left)); lok {
		if r, rok := decimal(strings.TrimSpace(*right)); rok {
			return l.Cmp(r) == 0
		}
	}
	return false
}

func validationIssueRules(result ValidationResult) map[string]bool {
	rules := make(map[string]bool)
	for _, finding := range result.Findings {
		if finding.Status != FindingStatusPass && finding.RuleID != "" {
			rules[finding.RuleID] = true
		}
	}
	return rules
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func financialMean(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	mean := total / float64(len(values))
	return &mean
}

func financialMeanRatio(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	ratio := float64(numerator) / float64(denominator)
	return &ratio
}

func financialPercentile(sortedValues []int64, percentile float64) int64 {
	if len(sortedValues) == 0 {
		return 0
	}
	index := int(percentile * float64(len(sortedValues)-1))
	if index < 0 {
		index = 0
	}
	if index >= len(sortedValues) {
		index = len(sortedValues) - 1
	}
	return sortedValues[index]
}
