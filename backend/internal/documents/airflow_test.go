package documents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAirflowUnavailableReturnsSafeRecoveryError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte("private upstream response"))
	}))
	defer ts.Close()
	err := NewAirflow(ts.URL, "user", "secret").Dispatch(context.Background(), Job{ID: "job", DAGID: "document_ingestion", RunID: "rb_test"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "retry dispatch") {
		t.Fatalf("missing recovery detail: %v", err)
	}
	if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
		t.Fatal("upstream body/credentials leaked")
	}
}

func TestDispatchRetryUsesPersistedRunAndChecksOwnership(t *testing.T) {
	calls := 0
	owner := "job-1"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/token" {
			w.Write([]byte(`{"access_token":"test-token"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer token")
		}
		if r.Method == "POST" {
			calls++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["dag_run_id"] != "rb_revision-1" {
				t.Errorf("run ID changed: %v", body)
			}
			if _, ok := body["logical_date"]; !ok {
				t.Error("Airflow 3 requires logical_date")
			}
			w.WriteHeader(409)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"conf": map[string]string{"job_id": owner}})
	}))
	defer ts.Close()
	a := NewAirflow(ts.URL, "user", "password")
	job := Job{ID: "job-1", DAGID: "document_ingestion", RunID: "rb_revision-1"}
	for i := 0; i < 2; i++ {
		if err := a.Dispatch(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	owner = "another-job"
	if err := a.Dispatch(context.Background(), job); err == nil {
		t.Fatal("accepted unrelated conflicting run")
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}
