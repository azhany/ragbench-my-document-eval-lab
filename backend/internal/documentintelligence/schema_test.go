package documentintelligence

import (
	"errors"
	"strings"
	"testing"
)

const validFinancialJSON = `{
  "schema_version":"financial-v1",
  "document_type":"invoice",
  "vendor":"Acme Sdn Bhd",
  "invoice_number":"INV-001",
  "invoice_date":"2026-01-15",
  "due_date":"2026-02-14",
  "currency":"myr",
  "subtotal":"100.00",
  "tax":"6.00",
  "discount":null,
  "total":"106.00",
  "line_items":[{"description":"Widget","quantity":"2","unit_price":"50.00","amount":"100.00"}]
}`

func TestParseFinancialDataNormalizesNullableTypedFields(t *testing.T) {
	data, err := ParseFinancialData([]byte(validFinancialJSON))
	if err != nil {
		t.Fatal(err)
	}
	if data.Currency == nil || *data.Currency != "MYR" {
		t.Fatalf("currency = %v, want normalized MYR", data.Currency)
	}
	if data.Discount != nil || data.InvoiceNumber == nil || *data.InvoiceNumber != "INV-001" {
		t.Fatalf("nullable fields = %+v", data)
	}
	if len(data.LineItems) != 1 || data.LineItems[0].Quantity == nil {
		t.Fatalf("line items = %+v", data.LineItems)
	}
}

func TestParseFinancialDataRejectsMalformedAndUnexpectedOutput(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		code string
	}{
		{"malformed", `{not-json`, "invalid_json"},
		{"unexpected top-level", strings.Replace(validFinancialJSON, `"total":"106.00"`, `"total":"106.00","secret":"do not store"`, 1), "unexpected_field"},
		{"unexpected nested", strings.Replace(validFinancialJSON, `"amount":"100.00"`, `"amount":"100.00","formula":"x"`, 1), "unexpected_field"},
		{"invalid date", strings.Replace(validFinancialJSON, `"invoice_date":"2026-01-15"`, `"invoice_date":"15/01/2026"`, 1), "invalid_date"},
		{"invalid amount type", strings.Replace(validFinancialJSON, `"total":"106.00"`, `"total":106`, 1), "wrong_type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseFinancialData([]byte(tc.raw))
			var schemaErr *SchemaError
			if !errors.As(err, &schemaErr) {
				t.Fatalf("error = %v, want SchemaError", err)
			}
			found := false
			for _, violation := range schemaErr.Violations {
				if violation.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("violations = %+v, want code %q", schemaErr.Violations, tc.code)
			}
		})
	}
}

func TestParseFinancialDataAllowsAbsentEvidenceAsNull(t *testing.T) {
	raw := `{"schema_version":"financial-v1","document_type":"receipt","line_items":[]}`
	data, err := ParseFinancialData([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if data.Vendor != nil || data.Total != nil || data.LineItems == nil {
		t.Fatalf("missing evidence was not represented explicitly: %+v", data)
	}
}
