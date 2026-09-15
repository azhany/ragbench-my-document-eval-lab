package httpapi

import (
	"net/http"
)

// Structured error envelope shared by all /api endpoints:
//
//	{"error": {"code": "...", "message": "...", "fields": [...]}}
//
// `fields` is present only for validation failures and lists one entry per
// offending field.

type fieldErrorPayload struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type errorPayload struct {
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Fields  []fieldErrorPayload `json:"fields,omitempty"`
	// TraceID correlates chat/evaluation errors to the persisted trace
	// that recorded the failed request; empty for pre-execution rejections.
	TraceID string `json:"trace_id,omitempty"`
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string, fields []fieldErrorPayload) {
	writeTraceError(w, status, code, message, fields, "")
}

// writeTraceError is writeError with an optional correlated trace id.
func writeTraceError(w http.ResponseWriter, status int, code, message string, fields []fieldErrorPayload, traceID string) {
	writeJSON(w, status, errorEnvelope{Error: errorPayload{
		Code:    code,
		Message: message,
		Fields:  fields,
		TraceID: traceID,
	}})
}
