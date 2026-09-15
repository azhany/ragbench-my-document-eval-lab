package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"ragbench-my/backend/internal/documents"
)

type DocumentStore interface {
	Create(context.Context, documents.Upload) (documents.Document, error)
	List(context.Context) ([]documents.Document, error)
	Get(context.Context, string) (documents.Document, error)
	RecordDispatch(context.Context, string, error) error
	Reprocess(context.Context, string, string) (documents.Document, error)
	Delete(context.Context, string) error
}
type DocumentOptions struct {
	Store      DocumentStore
	Dispatcher documents.Dispatcher
	UploadDir  string
	MaxBytes   int64
}

func validFilename(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, "/\\:") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func uploadMIME(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".txt":
		return "text/plain"
	}
	return ""
}

func (s *server) uploadDocument(w http.ResponseWriter, r *http.Request) {
	opts := s.documents
	// Bound the complete request, including malformed multipart headers and fields.
	r.Body = http.MaxBytesReader(w, r.Body, opts.MaxBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, 400, "invalid_upload", "expected multipart fields file and config_id", nil)
		return
	}
	var file *os.File
	var path string
	persisted := false
	defer func() {
		if file != nil {
			file.Close()
		}
		if path != "" && !persisted {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				s.logger.Error("upload_cleanup_failed", "path", path, "error", err)
			}
		}
	}()
	u := documents.Upload{ID: uuid.NewString()}
	seen := map[string]bool{}
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			s.uploadReadError(w, e)
			return
		}
		name := part.FormName()
		if seen[name] || (name != "file" && name != "config_id") {
			writeError(w, 400, "invalid_upload", "exactly one file and config_id are required", nil)
			return
		}
		seen[name] = true
		if name == "config_id" {
			b, e := io.ReadAll(io.LimitReader(part, 129))
			if e != nil {
				s.uploadReadError(w, e)
				return
			}
			if len(b) > 128 {
				writeError(w, 400, "invalid_upload", "config_id is too long", nil)
				return
			}
			u.ConfigID = strings.TrimSpace(string(b))
			continue
		}
		// Part.FileName() strips directory components, so validate the raw header.
		_, params, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if e != nil || !validFilename(params["filename"]) {
			writeError(w, 400, "invalid_filename", "filename must be a plain name without paths or control characters", nil)
			return
		}
		u.Filename = params["filename"]
		u.MIMEType = uploadMIME(u.Filename)
		if u.MIMEType == "" {
			writeError(w, 415, "unsupported_format", "supported file formats are PDF, DOCX and UTF-8 TXT", nil)
			return
		}
		targetPath := filepath.Join(opts.UploadDir, u.ID+strings.ToLower(filepath.Ext(u.Filename)))
		file, e = os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
		if e != nil {
			s.logger.Error("upload_storage_failed", "error", e)
			writeError(w, 500, "storage_failed", "could not store uploaded bytes", nil)
			return
		}
		path = targetPath // cleanup is allowed only after this request created the file
		hash := sha256.New()
		u.Size, e = io.Copy(io.MultiWriter(file, hash), io.LimitReader(part, opts.MaxBytes+1))
		if e != nil {
			s.uploadReadError(w, e)
			return
		}
		if u.Size > opts.MaxBytes {
			writeError(w, 413, "upload_too_large", fmt.Sprintf("file exceeds %d bytes", opts.MaxBytes), nil)
			return
		}
		if u.Size == 0 {
			writeError(w, 400, "empty_upload", "file must contain bytes", nil)
			return
		}
		if e = file.Sync(); e != nil {
			writeError(w, 500, "storage_failed", "could not sync uploaded bytes", nil)
			return
		}
		if e = file.Close(); e != nil {
			writeError(w, 500, "storage_failed", "could not close uploaded bytes", nil)
			return
		}
		file = nil
		u.Path = path
		u.Checksum = hex.EncodeToString(hash.Sum(nil))
	}
	if !seen["file"] || !seen["config_id"] {
		writeError(w, 400, "invalid_upload", "exactly one file and config_id are required", nil)
		return
	}
	d, err := opts.Store.Create(r.Context(), u)
	if err != nil {
		// Commit outcomes can be ambiguous after a connection loss. Preserve bytes
		// on unknown DB errors; their generated UUID identifies reconciliation.
		if !errors.Is(err, documents.ErrConfig) && !errors.Is(err, documents.ErrDuplicate) {
			persisted = true
			s.logger.Error("upload_persistence_uncertain", "document_id", u.ID, "storage_path", path, "error", err)
		}
		s.writeDocumentError(w, err)
		return
	}
	persisted = true
	w.Header().Set("Location", "/api/v1/documents/"+d.ID)
	s.dispatchDocument(w, d)
}
func (s *server) uploadReadError(w http.ResponseWriter, err error) {
	var max *http.MaxBytesError
	if errors.As(err, &max) {
		writeError(w, 413, "upload_too_large", "multipart request exceeds the configured upload limit", nil)
		return
	}
	var path *os.PathError
	if errors.As(err, &path) {
		s.logger.Error("upload_storage_failed", "error", err)
		writeError(w, 500, "storage_failed", "could not write uploaded bytes", nil)
		return
	}
	writeError(w, 400, "invalid_upload", "could not read complete multipart upload", nil)
}
func (s *server) dispatchDocument(w http.ResponseWriter, d documents.Document) {
	// Finish durable dispatch bookkeeping even if the browser disconnects.
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	err := s.documents.Dispatcher.Dispatch(ctx, *d.Job)
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer saveCancel()
	if saveErr := s.documents.Store.RecordDispatch(saveCtx, d.Job.ID, err); saveErr != nil {
		s.logger.Error("dispatch_persistence_failed", "document_id", d.ID, "job_id", d.Job.ID, "error", saveErr)
		writeError(w, 503, "dispatch_state_unknown", "dispatch outcome could not be saved; inspect document and retry dispatch using its existing ID", nil)
		return
	}
	if err != nil {
		s.logger.Warn("document_dispatch_failed", "document_id", d.ID, "job_id", d.Job.ID, "run_id", d.Job.RunID, "error", err)
		latest, getErr := s.documents.Store.Get(saveCtx, d.ID)
		if getErr == nil {
			d = latest
		}
		writeJSON(w, 503, map[string]any{"error": errorPayload{Code: "dispatch_failed", Message: err.Error()}, "document": d})
		return
	}
	// Return the accepted queued snapshot; GET shows newer worker transitions.
	d.Status = "queued"
	d.Job.State = "queued"
	d.Job.ErrorCode = nil
	d.Job.ErrorMessage = nil
	d.Job.DispatchAttempts++
	s.logger.Info("document_dispatched", "document_id", d.ID, "job_id", d.Job.ID, "run_id", d.Job.RunID)
	writeJSON(w, 202, d)
}
func (s *server) listDocuments(w http.ResponseWriter, r *http.Request) {
	docs, err := s.documents.Store.List(r.Context())
	if err != nil {
		s.writeDocumentError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"documents": docs})
}
func (s *server) getDocument(w http.ResponseWriter, r *http.Request) {
	d, err := s.documents.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDocumentError(w, err)
		return
	}
	writeJSON(w, 200, d)
}
func (s *server) retryDispatch(w http.ResponseWriter, r *http.Request) {
	d, err := s.documents.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDocumentError(w, err)
		return
	}
	if d.Job == nil || (d.Job.State != "dispatch_pending" && d.Job.State != "dispatch_failed") {
		s.writeDocumentError(w, documents.ErrNotRetryable)
		return
	}
	s.dispatchDocument(w, d)
}
func (s *server) writeDocumentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, documents.ErrNotFound):
		writeError(w, 404, "not_found", err.Error(), nil)
	case errors.Is(err, documents.ErrConfig):
		writeError(w, 400, "invalid_config", err.Error(), nil)
	case errors.Is(err, documents.ErrDuplicate):
		writeError(w, 409, "duplicate_document", err.Error(), nil)
	case errors.Is(err, documents.ErrActive):
		writeError(w, 409, "ingestion_active", err.Error(), nil)
	case errors.Is(err, documents.ErrNotRetryable):
		writeError(w, 409, "dispatch_not_retryable", err.Error(), nil)
	default:
		s.logger.Error("document_request_failed", "error", err)
		writeError(w, 500, "persistence_failed", "document state could not be persisted or read; inspect server logs", nil)
	}
}

func (s *server) reprocessDocument(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConfigID string `json:"config_id"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, 400, "invalid_body", "expected JSON with saved config_id", nil)
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid_body", "expected exactly one JSON object", nil)
		return
	}
	d, err := s.documents.Store.Reprocess(r.Context(), r.PathValue("id"), body.ConfigID)
	if err != nil {
		s.writeDocumentError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/documents/"+d.ID)
	s.dispatchDocument(w, d)
}

func (s *server) deleteDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.documents.Store.Delete(r.Context(), id); err != nil {
		s.writeDocumentError(w, err)
		return
	}
	s.logger.Info("document_deleted", "document_id", id, "retention", "source and historical evidence retained")
	w.WriteHeader(http.StatusNoContent)
}
