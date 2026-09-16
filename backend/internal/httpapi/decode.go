package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// decodeStrict decodes exactly one JSON object of at most limit bytes,
// rejecting unknown fields — the shared request-body convention of every
// POST endpoint.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	body := http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		status, code := http.StatusBadRequest, "invalid_body"
		if errors.As(err, &mbe) {
			status, code = http.StatusRequestEntityTooLarge, "body_too_large"
			err = fmt.Errorf("request body exceeds %d bytes", limit)
		}
		writeError(w, status, code,
			"request body is not a valid request: "+err.Error(), nil)
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"request body must contain exactly one JSON object", nil)
		return false
	}
	return true
}
