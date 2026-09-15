package documents

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
)

type Dispatcher interface {
	Dispatch(context.Context, Job) error
}
type Airflow struct {
	BaseURL, Username, Password string
	Client                      *http.Client
}

func NewAirflow(base, user, password string) *Airflow {
	return &Airflow{strings.TrimRight(base, "/"), user, password, &http.Client{Timeout: 15 * time.Second}}
}

// Airflow 3's public API uses JWT authentication, not the removed v1 basic auth.
// The persisted run ID is reused after ambiguous timeouts and across API restarts.
func (a *Airflow) Dispatch(ctx context.Context, j Job) error {
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := a.request(ctx, "POST", "/auth/token", "", map[string]string{"username": a.Username, "password": a.Password}, &token); err != nil {
		return err
	}
	if token.AccessToken == "" {
		return fmt.Errorf("Airflow authentication returned no access token")
	}
	path := "/api/v2/dags/" + url.PathEscape(j.DAGID) + "/dagRuns"
	body := map[string]any{"dag_run_id": j.RunID, "logical_date": nil, "conf": map[string]string{"job_id": j.ID}}
	err := a.request(ctx, "POST", path, token.AccessToken, body, nil)
	if status, ok := err.(airflowStatus); ok && status == http.StatusConflict {
		var run struct {
			Conf struct {
				JobID string `json:"job_id"`
			} `json:"conf"`
		}
		if err = a.request(ctx, "GET", path+"/"+url.PathEscape(j.RunID), token.AccessToken, nil, &run); err != nil {
			return err
		}
		if run.Conf.JobID != j.ID {
			return fmt.Errorf("Airflow run ID conflict: existing run belongs to another job")
		}
		return nil
	}
	return err
}

type airflowStatus int

func (s airflowStatus) Error() string {
	return fmt.Sprintf("Airflow API returned HTTP %d; check Airflow health, DAG availability and credentials, then retry dispatch", s)
}
func (a *Airflow) request(ctx context.Context, method, path, token string, body, out any) error {
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
		return fmt.Errorf("Airflow request unavailable or timed out; retry dispatch with the existing job")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return airflowStatus(resp.StatusCode)
	}
	if out != nil {
		if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return fmt.Errorf("invalid Airflow response")
		}
	}
	return nil
}
