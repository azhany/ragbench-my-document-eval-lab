package documentintelligence

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

const (
	FindingStatusPass    = "pass"
	FindingStatusWarning = "warning"
	FindingStatusFailure = "failure"

	FindingSeverityInfo  = "info"
	FindingSeverityWarn  = "warning"
	FindingSeverityError = "error"
)

// ValidationPolicy is persisted with an analysis result. The tolerance and
// required fields are data, not hidden constants in the validation function.
type ValidationPolicy struct {
	Version             string                    `json:"version"`
	AmountTolerance     string                    `json:"amount_tolerance"`
	SupportedCurrencies []string                  `json:"supported_currencies"`
	RequiredFields      map[DocumentType][]string `json:"required_fields"`
}

func DefaultValidationPolicy() ValidationPolicy {
	return ValidationPolicy{
		Version:             ValidationPolicyVersionV1,
		AmountTolerance:     "0.01",
		SupportedCurrencies: []string{"AUD", "CAD", "CNY", "EUR", "GBP", "HKD", "JPY", "MYR", "SGD", "USD"},
		RequiredFields: map[DocumentType][]string{
			DocumentTypeInvoice:    {"vendor", "invoice_number", "invoice_date", "currency", "total"},
			DocumentTypeReceipt:    {"vendor", "invoice_date", "currency", "total"},
			DocumentTypeCreditNote: {"vendor", "invoice_number", "invoice_date", "currency", "total"},
			DocumentTypeUnknown:    {"vendor", "currency", "total"},
		},
	}
}

func PolicyByVersion(version string) (ValidationPolicy, error) {
	policy := DefaultValidationPolicy()
	if version != policy.Version {
		return ValidationPolicy{}, fmt.Errorf("unknown validation policy version %q", version)
	}
	return policy, nil
}

type ValidationFinding struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

type ValidationResult struct {
	PolicyVersion string              `json:"policy_version"`
	Status        string              `json:"status"`
	Findings      []ValidationFinding `json:"findings"`
	Policy        ValidationPolicy    `json:"policy"`
}

// ValidateFinancialData performs business validation after schema parsing.
// It always returns a result for a schema-valid document, including documents
// with warnings or failures. Those findings are data for the final report;
// they are not retried as model failures.
func ValidateFinancialData(data FinancialData, policy ValidationPolicy) (ValidationResult, error) {
	if policy.Version == "" {
		return ValidationResult{}, fmt.Errorf("validation policy version is required")
	}
	tolerance, ok := decimal(policy.AmountTolerance)
	if !ok || tolerance.Sign() < 0 {
		return ValidationResult{}, fmt.Errorf("validation policy has an invalid amount tolerance")
	}
	if data.DocumentType == "" {
		data.DocumentType = DocumentTypeUnknown
	}

	result := ValidationResult{PolicyVersion: policy.Version, Status: FindingStatusPass,
		Findings: []ValidationFinding{}, Policy: policy}
	add := func(f ValidationFinding) {
		result.Findings = append(result.Findings, f)
		switch f.Status {
		case FindingStatusFailure:
			result.Status = FindingStatusFailure
		case FindingStatusWarning:
			if result.Status != FindingStatusFailure {
				result.Status = FindingStatusWarning
			}
		}
	}
	pass := func(rule, path, message string) {
		add(ValidationFinding{RuleID: rule, Severity: FindingSeverityInfo, Status: FindingStatusPass, Path: path, Message: message})
	}
	fail := func(rule, path, message, expected, actual string) {
		add(ValidationFinding{RuleID: rule, Severity: FindingSeverityError, Status: FindingStatusFailure, Path: path, Message: message, Expected: expected, Actual: actual})
	}
	warn := func(rule, path, message, expected, actual string) {
		add(ValidationFinding{RuleID: rule, Severity: FindingSeverityWarn, Status: FindingStatusWarning, Path: path, Message: message, Expected: expected, Actual: actual})
	}

	required := policy.RequiredFields[data.DocumentType]
	if len(required) == 0 {
		required = policy.RequiredFields[DocumentTypeUnknown]
	}
	for _, field := range required {
		if financialField(data, field) == nil {
			fail("required."+field, field, "important field is missing from document evidence", "present", "null")
		} else {
			pass("required."+field, field, "important field is present")
		}
	}

	supported := make(map[string]bool, len(policy.SupportedCurrencies))
	for _, currency := range policy.SupportedCurrencies {
		supported[strings.ToUpper(currency)] = true
	}
	if data.Currency != nil {
		currency := strings.ToUpper(strings.TrimSpace(*data.Currency))
		if supported[currency] {
			pass("currency.supported", "currency", "currency is supported by the validation policy")
		} else {
			fail("currency.unsupported", "currency", "currency is not supported by the validation policy", strings.Join(sortedKeys(supported), ", "), currency)
		}
	}

	invoiceDate := parseDate(data.InvoiceDate)
	dueDate := parseDate(data.DueDate)
	if data.InvoiceDate != nil && invoiceDate == nil {
		fail("date.invoice_format", "invoice_date", "invoice date is not a real YYYY-MM-DD date", "calendar date", *data.InvoiceDate)
	}
	if data.DueDate != nil && dueDate == nil {
		fail("date.due_format", "due_date", "due date is not a real YYYY-MM-DD date", "calendar date", *data.DueDate)
	}
	if invoiceDate != nil && dueDate != nil {
		if dueDate.Before(*invoiceDate) {
			fail("date.order", "due_date", "due date must not precede invoice date", "due_date >= invoice_date", *data.DueDate+" < "+*data.InvoiceDate)
		} else {
			pass("date.order", "due_date", "invoice and due dates are in chronological order")
		}
	}

	amounts := []struct {
		name  string
		value *string
	}{
		{"subtotal", data.Subtotal}, {"tax", data.Tax}, {"discount", data.Discount}, {"total", data.Total},
	}
	parsedAmounts := make(map[string]*big.Rat, len(amounts))
	for _, amount := range amounts {
		if amount.value == nil {
			continue
		}
		value, valid := decimal(*amount.value)
		if !valid {
			fail("amount."+amount.name+"_format", amount.name, "amount is not a supported decimal representation", "plain decimal", *amount.value)
			continue
		}
		parsedAmounts[amount.name] = value
		if value.Sign() < 0 && data.DocumentType != DocumentTypeCreditNote {
			fail("amount."+amount.name+"_negative", amount.name, "invoice and receipt amounts must not be negative", ">= 0", *amount.value)
		}
	}

	lineAmounts := make([]*big.Rat, 0, len(data.LineItems))
	for index, item := range data.LineItems {
		path := fmt.Sprintf("line_items[%d]", index)
		quantity, quantityOK := decimalPointer(item.Quantity)
		unitPrice, unitPriceOK := decimalPointer(item.UnitPrice)
		amount, amountOK := decimalPointer(item.Amount)
		if item.Quantity != nil && !quantityOK {
			fail("line_item.quantity_format", path+".quantity", "quantity is not a supported decimal representation", "plain decimal", *item.Quantity)
		}
		if item.UnitPrice != nil && !unitPriceOK {
			fail("line_item.unit_price_format", path+".unit_price", "unit price is not a supported decimal representation", "plain decimal", *item.UnitPrice)
		}
		if item.Amount != nil && !amountOK {
			fail("line_item.amount_format", path+".amount", "line-item amount is not a supported decimal representation", "plain decimal", *item.Amount)
		}
		if quantityOK && quantity.Sign() < 0 {
			fail("line_item.quantity_negative", path+".quantity", "quantity must not be negative", ">= 0", *item.Quantity)
		}
		if unitPriceOK && unitPrice.Sign() < 0 {
			fail("line_item.unit_price_negative", path+".unit_price", "unit price must not be negative", ">= 0", *item.UnitPrice)
		}
		if amountOK {
			lineAmounts = append(lineAmounts, amount)
		}
		if quantityOK && unitPriceOK && amountOK {
			expected := new(big.Rat).Mul(quantity, unitPrice)
			if withinTolerance(expected, amount, tolerance) {
				pass("line_item.extension", path, "quantity multiplied by unit price matches line-item amount")
			} else {
				fail("line_item.extension", path, "quantity multiplied by unit price does not match line-item amount", expected.FloatString(6), amount.FloatString(6))
			}
		}
	}

	if data.Subtotal != nil && len(lineAmounts) == len(data.LineItems) && len(lineAmounts) > 0 {
		sum := new(big.Rat)
		for _, amount := range lineAmounts {
			sum.Add(sum, amount)
		}
		if withinTolerance(sum, parsedAmounts["subtotal"], tolerance) {
			pass("subtotal.line_items", "subtotal", "line-item amounts reconcile to subtotal")
		} else {
			fail("subtotal.line_items", "subtotal", "line-item amounts do not reconcile to subtotal", sum.FloatString(6), *data.Subtotal)
		}
	} else if len(data.LineItems) > 0 && data.Subtotal != nil {
		warn("subtotal.line_items_incomplete", "subtotal", "subtotal reconciliation is incomplete because one or more line-item amounts are missing", "all line-item amounts", "partial line-item evidence")
	}

	if subtotal, subtotalOK := parsedAmounts["subtotal"]; subtotalOK {
		if total, totalOK := parsedAmounts["total"]; totalOK {
			computed := new(big.Rat).Set(subtotal)
			complete := true
			if tax, ok := parsedAmounts["tax"]; ok {
				computed.Add(computed, tax)
			} else {
				complete = false
			}
			if discount, ok := parsedAmounts["discount"]; ok {
				computed.Sub(computed, discount)
			} else if data.Discount != nil {
				complete = false
			}
			if complete {
				if withinTolerance(computed, total, tolerance) {
					pass("total.reconciliation", "total", "subtotal, tax, and discount reconcile to total")
				} else {
					fail("total.reconciliation", "total", "subtotal, tax, and discount do not reconcile to total", computed.FloatString(6), *data.Total)
				}
			} else if withinTolerance(computed, total, tolerance) {
				warn("total.reconciliation_incomplete", "total", "total matches the evidenced components but one tax or discount component is absent", "all applicable components evidenced", "component absent")
			} else {
				warn("total.reconciliation_incomplete", "total", "total reconciliation cannot be conclusive because a tax or discount component is absent", "all applicable components evidenced", "component absent")
			}
		}
	}

	sort.SliceStable(result.Findings, func(i, j int) bool {
		if result.Findings[i].RuleID != result.Findings[j].RuleID {
			return result.Findings[i].RuleID < result.Findings[j].RuleID
		}
		return result.Findings[i].Path < result.Findings[j].Path
	})
	return result, nil
}

func financialField(data FinancialData, field string) *string {
	switch field {
	case "vendor":
		return data.Vendor
	case "invoice_number":
		return data.InvoiceNumber
	case "invoice_date":
		return data.InvoiceDate
	case "currency":
		return data.Currency
	case "subtotal":
		return data.Subtotal
	case "tax":
		return data.Tax
	case "discount":
		return data.Discount
	case "total":
		return data.Total
	default:
		return nil
	}
}

func decimal(value string) (*big.Rat, bool) {
	if !decimalPattern.MatchString(value) {
		return nil, false
	}
	rat, ok := new(big.Rat).SetString(value)
	return rat, ok
}

func decimalPointer(value *string) (*big.Rat, bool) {
	if value == nil {
		return nil, false
	}
	return decimal(*value)
}

func withinTolerance(left, right, tolerance *big.Rat) bool {
	if left == nil || right == nil {
		return false
	}
	difference := new(big.Rat).Sub(left, right)
	if difference.Sign() < 0 {
		difference.Neg(difference)
	}
	return difference.Cmp(tolerance) <= 0
}

func parseDate(value *string) *time.Time {
	if value == nil {
		return nil
	}
	parsed, err := time.Parse("2006-01-02", *value)
	if err != nil {
		return nil
	}
	return &parsed
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
