package evalrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EvaluationDAGID is the Airflow DAG that executes one eval run.
const EvaluationDAGID = "rag_evaluation"

// Dispatcher dispatches one run to the evaluation DAG. Kept as a small
// explicit interface: Airflow just triggers; all evaluation logic lives in
// Go, and Python duplicates none of it.
type Dispatcher interface {
	DispatchRun(ctx context.Context, runID string) error
}

// AirflowDispatcher POSTs a durable run ID to the rag_evaluation DAG.
type AirflowDispatcher struct {
	BaseURL, Username, Password string
	Client                      *http.Client
}

func NewAirflowDispatcher(base, user, password string) *AirflowDispatcher {
	return &AirflowDispatcher{strings.TrimRight(base, "/"), user, password, &http.Client{Timeout: 15 * time.Second}}
}

// DispatchRun triggers rag_evaluation with the run's durable dag_run_id
// (reused across ambiguous timeouts, mirroring ingestion dispatch).
func (a *AirflowDispatcher) DispatchRun(ctx context.Context, runID string) error {
	dagRun := EvalDagRunID(runID)
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := a.request(ctx, "POST", "/auth/token", "", map[string]string{"username": a.Username, "password": a.Password}, &token); err != nil {
		return err
	}
	if token.AccessToken == "" {
		return fmt.Errorf("Airflow authentication returned no access token")
	}
	path := "/api/v2/dags/" + url.PathEscape(EvaluationDAGID) + "/dagRuns"
	body := map[string]any{"dag_run_id": dagRun, "logical_date": nil, "conf": map[string]string{"run_id": runID}}
	if err := a.request(ctx, "POST", path, token.AccessToken, body, nil); err != nil {
		return err
	}
	return nil
}

// EvalDagRunID derives the durable, idempotent Airflow run ID for one eval
// run so retries never spawn a second logical run.
func EvalDagRunID(runID string) string {
	if _, err := uuid.Parse(runID); err != nil {
		return "eval_" + strings.ReplaceAll(runID, "-", "")
	}
	return "eval_" + runID
}

func (a *AirflowDispatcher) request(ctx context.Context, method, path, token string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, a.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid Airflow endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return fmt.Errorf("Airflow request unavailable or timed out; re-dispatch the run")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Airflow API returned HTTP %d; check Airflow health, DAG availability and credentials, then re-dispatch", resp.StatusCode)
	}
	if out != nil {
		if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return fmt.Errorf("invalid Airflow response")
		}
	}
	return nil
}
