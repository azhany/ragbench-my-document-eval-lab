package documentintelligence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	BaseURL  string
	Username string
	Password string
	Client   *http.Client
}

func NewAirflow(baseURL, username, password string) *Airflow {
	return &Airflow{BaseURL: strings.TrimRight(baseURL, "/"), Username: username, Password: password,
		Client: &http.Client{Timeout: 15 * time.Second}}
}

type airflowStatus int

func (s airflowStatus) Error() string {
	return fmt.Sprintf("Airflow API returned HTTP %d; inspect the persisted analysis job and retry dispatch", s)
}

func (a *Airflow) Dispatch(ctx context.Context, job Job) error {
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := a.request(ctx, http.MethodPost, "/auth/token", "", map[string]string{
		"username": a.Username, "password": a.Password,
	}, &token); err != nil {
		return err
	}
	if token.AccessToken == "" {
		return errors.New("Airflow authentication returned no access token")
	}
	path := "/api/v2/dags/" + url.PathEscape(job.DAGID) + "/dagRuns"
	body := map[string]any{"dag_run_id": job.RunID, "logical_date": nil,
		"conf": map[string]string{"analysis_id": job.AnalysisID, "job_id": job.ID}}
	err := a.request(ctx, http.MethodPost, path, token.AccessToken, body, nil)
	if status, ok := err.(airflowStatus); ok && status == http.StatusConflict {
		var run struct {
			Conf struct {
				AnalysisID string `json:"analysis_id"`
				JobID      string `json:"job_id"`
			} `json:"conf"`
		}
		if err := a.request(ctx, http.MethodGet, path+"/"+url.PathEscape(job.RunID), token.AccessToken, nil, &run); err != nil {
			return err
		}
		if run.Conf.AnalysisID != job.AnalysisID || run.Conf.JobID != job.ID {
			return errors.New("Airflow run ID conflict: existing run belongs to another analysis job")
		}
		return nil
	}
	return err
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
		return errors.New("invalid Airflow endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Airflow request unavailable or timed out; retry dispatch with the existing analysis job")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return airflowStatus(resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return errors.New("invalid Airflow response")
		}
	}
	return nil
}
