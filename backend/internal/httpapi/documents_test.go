package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"testing"

	"ragbench-my/backend/internal/documents"
)

type uploadStore struct {
	u           documents.Upload
	d           documents.Document
	creates     int
	dispatchErr error
}

func (s *uploadStore) Reprocess(context.Context, string, string) (documents.Document, error) {
	return s.d, nil
}
func (s *uploadStore) Delete(context.Context, string) error { return nil }

func (s *uploadStore) Create(_ context.Context, u documents.Upload) (documents.Document, error) {
	s.u = u
	s.creates++
	s.d = documents.Document{ID: u.ID, Status: "queued", Job: &documents.Job{ID: "job", RunID: "run", State: "dispatch_pending"}}
	return s.d, nil
}
func (s *uploadStore) List(context.Context) ([]documents.Document, error) {
	return []documents.Document{s.d}, nil
}
func (s *uploadStore) Get(context.Context, string) (documents.Document, error) { return s.d, nil }
func (s *uploadStore) RecordDispatch(_ context.Context, _ string, e error) error {
	s.dispatchErr = e
	return nil
}

type dispatchStub struct {
	calls int
	err   error
}

func (s *dispatchStub) Dispatch(context.Context, documents.Job) error { s.calls++; return s.err }

func TestDispatchFailureReturnsPersistedIdentityAndKeepsSource(t *testing.T) {
	dir := t.TempDir()
	st := &uploadStore{}
	dis := &dispatchStub{err: errors.New("Airflow unavailable; retry dispatch")}
	srv := &server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), documents: DocumentOptions{Store: st, Dispatcher: dis, UploadDir: dir, MaxBytes: 100}}
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	m.WriteField("config_id", "config")
	f, _ := m.CreateFormFile("file", "source.txt")
	f.Write([]byte("Synthetic evidence"))
	m.Close()
	r := httptest.NewRequest("POST", "/api/v1/documents", &body)
	r.Header.Set("Content-Type", m.FormDataContentType())
	w := httptest.NewRecorder()
	srv.uploadDocument(w, r)
	if w.Code != 503 || !bytes.Contains(w.Body.Bytes(), []byte(`"code":"dispatch_failed"`)) || !bytes.Contains(w.Body.Bytes(), []byte(st.d.ID)) {
		t.Fatalf("missing structured persisted failure: %d %s", w.Code, w.Body.String())
	}
	if st.dispatchErr == nil {
		t.Fatal("dispatch failure not recorded")
	}
	if _, err := os.Stat(st.u.Path); err != nil {
		t.Fatal("persisted source was removed")
	}
}

func TestStorageFailureDoesNotCreateDocument(t *testing.T) {
	st := &uploadStore{}
	srv := &server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), documents: DocumentOptions{Store: st, Dispatcher: &dispatchStub{}, UploadDir: t.TempDir() + "/missing", MaxBytes: 100}}
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	m.WriteField("config_id", "config")
	f, _ := m.CreateFormFile("file", "source.txt")
	f.Write([]byte("Synthetic evidence"))
	m.Close()
	r := httptest.NewRequest("POST", "/api/v1/documents", &body)
	r.Header.Set("Content-Type", m.FormDataContentType())
	w := httptest.NewRecorder()
	srv.uploadDocument(w, r)
	if w.Code != 500 || !bytes.Contains(w.Body.Bytes(), []byte(`"code":"storage_failed"`)) || st.creates != 0 {
		t.Fatalf("invalid storage failure behavior: %d %s", w.Code, w.Body.String())
	}
}

func TestMultipartUploadValidationAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name, filename, content string
		want                    int
	}{
		{"txt", "notes.txt", "source text", 202}, {"pdf", "notes.pdf", "%PDF test", 202}, {"docx", "notes.docx", "PK test", 202},
		{"jpg", "receipt.jpg", "image bytes", 202}, {"png", "receipt.png", "image bytes", 202},
		{"traversal", "../escape.txt", "text", 400}, {"windows", "C:\\escape.txt", "text", 400}, {"format", "notes.exe", "text", 415},
		{"oversized", "notes.txt", string(bytes.Repeat([]byte("x"), 33)), 413}, {"empty", "notes.txt", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			st := &uploadStore{}
			dis := &dispatchStub{}
			srv := &server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), documents: DocumentOptions{Store: st, Dispatcher: dis, UploadDir: dir, MaxBytes: 32}}
			var body bytes.Buffer
			m := multipart.NewWriter(&body)
			m.WriteField("config_id", "config")
			f, _ := m.CreateFormFile("file", tc.filename)
			f.Write([]byte(tc.content))
			m.Close()
			req := httptest.NewRequest("POST", "/api/v1/documents", &body)
			req.Header.Set("Content-Type", m.FormDataContentType())
			w := httptest.NewRecorder()
			srv.uploadDocument(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			files, _ := os.ReadDir(dir)
			if tc.want == 202 {
				if len(files) != 1 || st.creates != 1 || dis.calls != 1 {
					t.Fatalf("upload not persisted/dispatched: %d %d %d", len(files), st.creates, dis.calls)
				}
				data, _ := os.ReadFile(st.u.Path)
				if string(data) != tc.content {
					t.Fatal("stored bytes differ")
				}
			} else if len(files) != 0 || st.creates != 0 || dis.calls != 0 {
				t.Fatal("rejected upload left state")
			}
		})
	}
}
