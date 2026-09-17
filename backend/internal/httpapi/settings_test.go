package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ragbench-my/backend/internal/modelprofile"
)

type fakeProfileStore struct {
	profiles  []modelprofile.Profile
	created   modelprofile.CreateRequest
	createErr error
}

func (f *fakeProfileStore) List(context.Context) ([]modelprofile.Profile, error) {
	return f.profiles, nil
}

func (f *fakeProfileStore) Get(_ context.Context, id string) (modelprofile.Profile, error) {
	for _, profile := range f.profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return modelprofile.Profile{}, modelprofile.ErrNotFound
}

func (f *fakeProfileStore) Create(_ context.Context, req modelprofile.CreateRequest) (modelprofile.Profile, error) {
	f.created = req
	if f.createErr != nil {
		return modelprofile.Profile{}, f.createErr
	}
	return modelprofile.Profile{ID: "profile-1", Name: req.Name, Kind: req.Kind,
		Provider: req.Provider, Model: req.Model, Enabled: true}, nil
}

func settingsTestHandler(store ModelProfileStore) http.Handler {
	return NewFullAPI(testLogger(), nil, DocumentOptions{}, nil, nil,
		EvalOptions{Profiles: store})
}

func TestGetSettingsReturnsPersistedCatalogWithoutSecrets(t *testing.T) {
	store := &fakeProfileStore{profiles: []modelprofile.Profile{{
		ID: "p1", Name: "support-model-v1", Kind: modelprofile.KindGeneration,
		Provider: "opencode-go", Model: "glm-5.3-flash", Enabled: true,
	}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	rec := httptest.NewRecorder()
	settingsTestHandler(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "api_key") || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("settings response must not expose credentials: %s", rec.Body.String())
	}
	var body struct {
		Profiles []modelprofile.Profile `json:"model_profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Profiles) != 1 || body.Profiles[0].Name != "support-model-v1" {
		t.Fatalf("unexpected profile catalog: %+v", body.Profiles)
	}
}

func TestGetModelProfileReturnsPersistedProfile(t *testing.T) {
	store := &fakeProfileStore{profiles: []modelprofile.Profile{{
		ID: "p1", Name: "support-model-v1", Kind: modelprofile.KindGeneration,
		Provider: "opencode-go", Model: "glm-5.3-flash", Enabled: true,
	}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/model-profiles/p1", nil)
	rec := httptest.NewRecorder()
	settingsTestHandler(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "support-model-v1") {
		t.Fatalf("profile response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCreateModelProfileReturns201AndLocation(t *testing.T) {
	store := &fakeProfileStore{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/model-profiles", strings.NewReader(`{
		"name":"support-embedding-v2","kind":"embedding","provider":"openai","model":"text-embedding-3-large","dimensions":3072
	}`))
	rec := httptest.NewRecorder()
	settingsTestHandler(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "/api/v1/settings/model-profiles/profile-1" {
		t.Fatalf("Location = %q", rec.Header().Get("Location"))
	}
	if store.created.Name != "support-embedding-v2" || store.created.Dimensions != 3072 {
		t.Fatalf("store received wrong request: %+v", store.created)
	}
}

func TestCreateModelProfileMapsValidationAndConflict(t *testing.T) {
	validationStore := &fakeProfileStore{createErr: modelprofile.ValidationErrors{{Field: "dimensions", Message: "must be positive"}}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/model-profiles", strings.NewReader(`{"name":"x"}`))
	rec := httptest.NewRecorder()
	settingsTestHandler(validationStore).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "dimensions") {
		t.Fatalf("validation response = %d %s", rec.Code, rec.Body.String())
	}

	conflictStore := &fakeProfileStore{createErr: modelprofile.ErrNameConflict}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/settings/model-profiles", strings.NewReader(`{"name":"x"}`))
	rec = httptest.NewRecorder()
	settingsTestHandler(conflictStore).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", rec.Code)
	}
	if !errors.Is(conflictStore.createErr, modelprofile.ErrNameConflict) {
		t.Fatal("test setup lost conflict identity")
	}
}
