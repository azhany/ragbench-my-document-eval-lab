package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"ragbench-my/backend/internal/rag"
)

// maxChatBodyBytes bounds chat payloads; a question plus config id is small,
// so anything larger is a client error.
const maxChatBodyBytes = 64 << 10 // 64 KiB

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	var req rag.ChatRequest
	body := http.MaxBytesReader(w, r.Body, maxChatBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"request body is not a valid chat request: "+err.Error(), nil)
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"request body must contain exactly one JSON object", nil)
		return
	}

	resp, err := s.chatPipeline.Ask(r.Context(), req)
	if err != nil {
		s.writeChatError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// chatErrorStatus maps documented pipeline codes to HTTP statuses. Provider
// failures are upstream problems (502/504); insufficient evidence is a
// request that cannot be satisfied (422); state conflicts are 409.
func chatErrorStatus(code string) int {
	switch code {
	case rag.ErrCodeNotFound:
		return http.StatusNotFound
	case rag.ErrCodeValidationFailed:
		return http.StatusBadRequest
	case rag.ErrCodeCapabilityUnavailable, rag.ErrCodeRetrievalEmpty:
		return http.StatusUnprocessableEntity
	case rag.ErrCodeRevisionUnavailable:
		return http.StatusConflict
	case rag.ErrCodeModelTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}

func (s *server) writeChatError(w http.ResponseWriter, err error) {
	var verrs *rag.ValidationErrors
	if errors.As(err, &verrs) {
		fields := make([]fieldErrorPayload, len(verrs.Fields))
		for i, fe := range verrs.Fields {
			fields[i] = fieldErrorPayload{Field: fe.Field, Message: fe.Message}
		}
		writeError(w, http.StatusBadRequest, rag.ErrCodeValidationFailed,
			"chat request failed validation", fields)
		return
	}

	var rerr *rag.Error
	if !errors.As(err, &rerr) {
		s.logger.Error("chat request failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected server error", nil)
		return
	}
	if rerr.Code == rag.ErrCodePersistenceFailed {
		// Correlated server-side log; the trace ID names what failed to store.
		s.logger.Error("chat trace persistence failed",
			slog.String("trace_id", rerr.TraceID),
			slog.String("error", rerr.Message))
		writeError(w, http.StatusInternalServerError, rag.ErrCodePersistenceFailed,
			"the answer was produced but its trace could not be persisted; the answer is not returned", nil)
		return
	}

	s.logger.Warn("chat request failed",
		slog.String("code", rerr.Code),
		slog.String("trace_id", rerr.TraceID),
		slog.String("error", rerr.Message))
	writeTraceError(w, chatErrorStatus(rerr.Code), rerr.Code, rerr.Message, nil, rerr.TraceID)
}
