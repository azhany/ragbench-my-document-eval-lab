package documentintelligence

import "testing"

func str(value string) *string { return &value }

func validData() FinancialData {
	return FinancialData{
		SchemaVersion: SchemaVersionV1, DocumentType: DocumentTypeInvoice,
		Vendor: str("Acme"), InvoiceNumber: str("INV-1"), InvoiceDate: str("2026-01-01"),
		DueDate: str("2026-01-31"), Currency: str("MYR"), Subtotal: str("100.00"),
		Tax: str("6.00"), Total: str("106.00"),
		LineItems: []LineItem{{Description: str("Widget"), Quantity: str("2"), UnitPrice: str("50.00"), Amount: str("100.00")}},
	}
}

func findRule(result ValidationResult, rule string) *ValidationFinding {
	for i := range result.Findings {
		if result.Findings[i].RuleID == rule {
			return &result.Findings[i]
		}
	}
	return nil
}

func TestValidateFinancialDataPassesExactDecimalTotals(t *testing.T) {
	result, err := ValidateFinancialData(validData(), DefaultValidationPolicy())
	if err != nil || result.Status != FindingStatusPass {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if finding := findRule(result, "total.reconciliation"); finding == nil || finding.Status != FindingStatusPass {
		t.Fatalf("total finding = %+v", finding)
	}
}

func TestValidateFinancialDataUsesExplicitToleranceBoundary(t *testing.T) {
	data := validData()
	data.Total = str("106.01") // exactly one cent: accepted by the v1 policy
	result, err := ValidateFinancialData(data, DefaultValidationPolicy())
	if err != nil || result.Status != FindingStatusPass {
		t.Fatalf("one-cent boundary result = %+v, err = %v", result, err)
	}
	data.Total = str("106.02")
	result, err = ValidateFinancialData(data, DefaultValidationPolicy())
	if err != nil || result.Status != FindingStatusFailure {
		t.Fatalf("over-tolerance result = %+v, err = %v", result, err)
	}
	if finding := findRule(result, "total.reconciliation"); finding == nil || finding.Status != FindingStatusFailure {
		t.Fatalf("reconciliation finding = %+v", finding)
	}
}

func TestValidateFinancialDataSurfacesMissingFieldsDateAndLineMismatch(t *testing.T) {
	data := validData()
	data.Vendor = nil
	data.InvoiceDate = str("2026-02-01")
	data.DueDate = str("2026-01-01")
	data.LineItems[0].Amount = str("99.00")
	result, err := ValidateFinancialData(data, DefaultValidationPolicy())
	if err != nil || result.Status != FindingStatusFailure {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	for _, rule := range []string{"required.vendor", "date.order", "line_item.extension", "subtotal.line_items"} {
		if findRule(result, rule) == nil {
			t.Fatalf("missing rule %q in %+v", rule, result.Findings)
		}
	}
}

func TestValidateFinancialDataDoesNotUseBinaryFloatingPoint(t *testing.T) {
	data := validData()
	data.Subtotal = str("0.10")
	data.Tax = str("0.20")
	data.Total = str("0.30")
	data.LineItems = []LineItem{{Description: str("tiny"), Quantity: str("1"), UnitPrice: str("0.10"), Amount: str("0.10")}}
	result, err := ValidateFinancialData(data, DefaultValidationPolicy())
	if err != nil || result.Status != FindingStatusPass {
		t.Fatalf("decimal result = %+v, err = %v", result, err)
	}
}
