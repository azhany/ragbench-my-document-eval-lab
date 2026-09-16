// Package documentintelligence contains the bounded Sprint 7 financial
// document workflow. It owns the structured data contract and deterministic
// business rules; Airflow only coordinates the persisted stages.
package documentintelligence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersionV1           = "financial-v1"
	ExtractionPromptV1        = "financial-extraction-v1"
	SummaryPromptV1           = "financial-summary-v1"
	ValidationPolicyVersionV1 = "financial-validation-v1"

	MaxStructuredOutputBytes = 256 << 10
	MaxEvidenceBytes         = 8 << 20
	MaxLineItems             = 500
)

// DocumentType is intentionally small. A document outside the supported
// invoice/receipt shapes is retained as unknown and is not silently treated
// as an invoice.
type DocumentType string

const (
	DocumentTypeInvoice    DocumentType = "invoice"
	DocumentTypeReceipt    DocumentType = "receipt"
	DocumentTypeCreditNote DocumentType = "credit_note"
	DocumentTypeUnknown    DocumentType = "unknown"
)

// FinancialData is the normalized, schema-valid representation returned by
// structured extraction. Monetary and quantity values are decimal strings so
// JSON and Go never introduce binary floating-point rounding into financial
// data. A nil pointer means the source did not evidence that value.
type FinancialData struct {
	SchemaVersion string       `json:"schema_version"`
	DocumentType  DocumentType `json:"document_type"`
	Vendor        *string      `json:"vendor"`
	InvoiceNumber *string      `json:"invoice_number"`
	InvoiceDate   *string      `json:"invoice_date"`
	DueDate       *string      `json:"due_date"`
	Currency      *string      `json:"currency"`
	Subtotal      *string      `json:"subtotal"`
	Tax           *string      `json:"tax"`
	Discount      *string      `json:"discount"`
	Total         *string      `json:"total"`
	LineItems     []LineItem   `json:"line_items"`
}

type LineItem struct {
	Description *string `json:"description"`
	Quantity    *string `json:"quantity"`
	UnitPrice   *string `json:"unit_price"`
	Amount      *string `json:"amount"`
}

// SchemaViolation is safe to expose to a reviewer. It never includes a raw
// provider response, which may contain source text or credentials.
type SchemaViolation struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type SchemaError struct {
	Violations []SchemaViolation `json:"violations"`
}

func (e *SchemaError) Error() string {
	if len(e.Violations) == 0 {
		return "structured output failed schema validation"
	}
	return "structured output failed schema validation: " + e.Violations[0].Path + " " + e.Violations[0].Message
}

var (
	datePattern     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	decimalPattern  = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]{1,6})?$`)
)

var allowedFinancialFields = map[string]bool{
	"schema_version": true, "document_type": true, "vendor": true,
	"invoice_number": true, "invoice_date": true, "due_date": true,
	"currency": true, "subtotal": true, "tax": true, "discount": true,
	"total": true, "line_items": true,
}

var allowedLineItemFields = map[string]bool{
	"description": true, "quantity": true, "unit_price": true, "amount": true,
}

// ParseFinancialData strictly parses one provider response. Known nullable
// fields may be omitted or null; required structural fields cannot be
// omitted. The returned value is normalized so omitted nullable fields are
// serialized as explicit nulls in the persisted result.
func ParseFinancialData(raw []byte) (FinancialData, error) {
	if len(raw) == 0 || len(raw) > MaxStructuredOutputBytes {
		return FinancialData{}, &SchemaError{Violations: []SchemaViolation{{
			Path: "$", Code: "output_size", Message: "structured output is empty or exceeds the bounded output size",
		}}}
	}

	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&object); err != nil {
		return FinancialData{}, &SchemaError{Violations: []SchemaViolation{{
			Path: "$", Code: "invalid_json", Message: "structured output must be one JSON object",
		}}}
	}
	if object == nil {
		return FinancialData{}, &SchemaError{Violations: []SchemaViolation{{
			Path: "$", Code: "object_required", Message: "structured output must be one JSON object",
		}}}
	}
	if err := ensureEOF(decoder); err != nil {
		return FinancialData{}, err
	}

	violations := make([]SchemaViolation, 0)
	unknown := make([]string, 0)
	for key := range object {
		if !allowedFinancialFields[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		violations = append(violations, SchemaViolation{Path: key, Code: "unexpected_field", Message: "field is not in financial-v1"})
	}
	for _, key := range []string{"schema_version", "document_type", "line_items"} {
		if _, ok := object[key]; !ok {
			violations = append(violations, SchemaViolation{Path: key, Code: "required_field", Message: "field is required"})
		}
	}
	if len(violations) > 0 {
		return FinancialData{}, &SchemaError{Violations: violations}
	}

	var data FinancialData
	decoder = json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&data); err != nil {
		return FinancialData{}, &SchemaError{Violations: []SchemaViolation{{
			Path: "$", Code: "wrong_type", Message: "structured output contains a value with the wrong type",
		}}}
	}
	if err := ensureEOF(decoder); err != nil {
		return FinancialData{}, err
	}

	data.SchemaVersion = strings.TrimSpace(data.SchemaVersion)
	data.DocumentType = DocumentType(strings.ToLower(strings.TrimSpace(string(data.DocumentType))))
	data.Vendor = normalizeString(data.Vendor)
	data.InvoiceNumber = normalizeString(data.InvoiceNumber)
	data.InvoiceDate = normalizeString(data.InvoiceDate)
	data.DueDate = normalizeString(data.DueDate)
	data.Currency = normalizeCurrency(data.Currency)
	data.Subtotal = normalizeString(data.Subtotal)
	data.Tax = normalizeString(data.Tax)
	data.Discount = normalizeString(data.Discount)
	data.Total = normalizeString(data.Total)
	if data.LineItems == nil {
		// A JSON null is not an acceptable line-item collection. It is handled
		// below so the normalized result never conflates null with an empty list.
		data.LineItems = []LineItem{}
	}

	if data.SchemaVersion != SchemaVersionV1 {
		violations = append(violations, SchemaViolation{Path: "schema_version", Code: "unsupported_version", Message: "schema_version must be financial-v1"})
	}
	switch data.DocumentType {
	case DocumentTypeInvoice, DocumentTypeReceipt, DocumentTypeCreditNote, DocumentTypeUnknown:
	default:
		violations = append(violations, SchemaViolation{Path: "document_type", Code: "unsupported_value", Message: "document_type must be invoice, receipt, credit_note, or unknown"})
	}

	validateString(&violations, "vendor", data.Vendor, 500)
	validateString(&violations, "invoice_number", data.InvoiceNumber, 200)
	validateDate(&violations, "invoice_date", data.InvoiceDate)
	validateDate(&violations, "due_date", data.DueDate)
	if data.Currency != nil && !currencyPattern.MatchString(*data.Currency) {
		violations = append(violations, SchemaViolation{Path: "currency", Code: "invalid_currency", Message: "currency must be a three-letter uppercase code"})
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"subtotal", data.Subtotal}, {"tax", data.Tax}, {"discount", data.Discount}, {"total", data.Total},
	} {
		validateDecimal(&violations, field.name, field.value)
	}

	if len(data.LineItems) > MaxLineItems {
		violations = append(violations, SchemaViolation{Path: "line_items", Code: "too_many_items", Message: "line_items exceeds the bounded item count"})
	}
	if rawItems, ok := object["line_items"]; ok && bytes.Equal(bytes.TrimSpace(rawItems), []byte("null")) {
		violations = append(violations, SchemaViolation{Path: "line_items", Code: "array_required", Message: "line_items must be an array, not null"})
	}
	for i, item := range data.LineItems {
		path := fmt.Sprintf("line_items[%d]", i)
		validateString(&violations, path+".description", item.Description, 1000)
		validateDecimal(&violations, path+".quantity", item.Quantity)
		validateDecimal(&violations, path+".unit_price", item.UnitPrice)
		validateDecimal(&violations, path+".amount", item.Amount)
		if rawItem, ok := rawLineItem(object, i); ok {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(rawItem, &fields); err != nil || fields == nil {
				violations = append(violations, SchemaViolation{Path: path, Code: "object_required", Message: "line item must be an object"})
				continue
			}
			unknownItem := make([]string, 0)
			for key := range fields {
				if !allowedLineItemFields[key] {
					unknownItem = append(unknownItem, key)
				}
			}
			sort.Strings(unknownItem)
			for _, key := range unknownItem {
				violations = append(violations, SchemaViolation{Path: path + "." + key, Code: "unexpected_field", Message: "field is not in financial-v1 line_items"})
			}
		}
	}

	if len(violations) > 0 {
		return FinancialData{}, &SchemaError{Violations: violations}
	}
	return data, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &SchemaError{Violations: []SchemaViolation{{Path: "$", Code: "trailing_json", Message: "structured output must contain exactly one JSON object"}}}
	}
	return nil
}

func rawLineItem(object map[string]json.RawMessage, index int) (json.RawMessage, bool) {
	raw, ok := object["line_items"]
	if !ok {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || index < 0 || index >= len(items) {
		return nil, false
	}
	return items[index], true
}

func normalizeString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeCurrency(value *string) *string {
	value = normalizeString(value)
	if value == nil {
		return nil
	}
	upper := strings.ToUpper(*value)
	return &upper
}

func validateString(violations *[]SchemaViolation, path string, value *string, max int) {
	if value == nil {
		return
	}
	if !utf8.ValidString(*value) || len([]rune(*value)) > max {
		*violations = append(*violations, SchemaViolation{Path: path, Code: "invalid_string", Message: "string is invalid or exceeds its bounded length"})
	}
}

func validateDate(violations *[]SchemaViolation, path string, value *string) {
	if value == nil {
		return
	}
	if !datePattern.MatchString(*value) {
		*violations = append(*violations, SchemaViolation{Path: path, Code: "invalid_date", Message: "date must use YYYY-MM-DD"})
		return
	}
	if _, err := time.Parse("2006-01-02", *value); err != nil {
		*violations = append(*violations, SchemaViolation{Path: path, Code: "invalid_date", Message: "date is not a real calendar date"})
	}
}

func validateDecimal(violations *[]SchemaViolation, path string, value *string) {
	if value == nil {
		return
	}
	if !decimalPattern.MatchString(*value) {
		*violations = append(*violations, SchemaViolation{Path: path, Code: "invalid_decimal", Message: "value must be a plain decimal string with at most six fractional digits"})
	}
}
