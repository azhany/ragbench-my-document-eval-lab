package ragconfig

import (
	"errors"
	"strings"
	"testing"
)

func validRequest() CreateRequest {
	return CreateRequest{
		Name:             "baseline-v1",
		ChunkSize:        500,
		ChunkOverlap:     80,
		RetrievalMode:    RetrievalModeVector,
		TopK:             5,
		RerankEnabled:    false,
		PromptVersion:    "v1",
		ModelProfile:     "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small",
	}
}

func fieldMessages(errs ValidationErrors, field string) []string {
	var out []string
	for _, fe := range errs {
		if fe.Field == field {
			out = append(out, fe.Message)
		}
	}
	return out
}

func TestResolveAcceptsValidRequest(t *testing.T) {
	resolved, errs := Resolve(validRequest())
	if len(errs) != 0 {
		t.Fatalf("Resolve() returned validation errors: %v", errs)
	}
	if resolved.EmbeddingProvider != "openai" ||
		resolved.EmbeddingModel != "text-embedding-3-small" ||
		resolved.EmbeddingDimensions != 1536 {
		t.Fatalf("embedding identity not resolved from registry: %+v", resolved)
	}
	if resolved.Name != "baseline-v1" {
		t.Fatalf("name not trimmed/kept: %q", resolved.Name)
	}
}

func TestResolveAcceptsHuggingFaceEmbeddingAndOpenAICompatibleGeneration(t *testing.T) {
	req := validRequest()
	req.ModelProfile = "opencode-go-glm-5.3-flash"
	req.EmbeddingProfile = "huggingface-bge-small-en-v1.5"
	resolved, errs := Resolve(req)
	if len(errs) != 0 {
		t.Fatalf("Resolve() returned validation errors: %v", errs)
	}
	if resolved.EmbeddingProvider != "huggingface" ||
		resolved.EmbeddingModel != "BAAI/bge-small-en-v1.5" ||
		resolved.EmbeddingDimensions != 384 {
		t.Fatalf("Hugging Face identity not resolved: %+v", resolved)
	}
}

func TestResolveRejectsInvalidChunkBounds(t *testing.T) {
	tests := []struct {
		name         string
		chunkSize    int
		chunkOverlap int
	}{
		{"chunk size zero", 0, 0},
		{"chunk size negative", -1, 0},
		{"chunk size above max", MaxChunkSize + 1, 0},
		{"overlap negative", 500, -1},
		{"overlap equals size", 500, 500},
		{"overlap above size", 500, 501},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			req.ChunkSize = tt.chunkSize
			req.ChunkOverlap = tt.chunkOverlap
			_, errs := Resolve(req)
			if len(errs) == 0 {
				t.Fatal("Resolve() = no errors, want validation errors")
			}
			if len(fieldMessages(errs, "chunk_size")) == 0 &&
				len(fieldMessages(errs, "chunk_overlap")) == 0 {
				t.Fatalf("errors do not name chunk fields: %v", errs)
			}
		})
	}
}

func TestResolveRejectsInvalidTopK(t *testing.T) {
	for _, topK := range []int{0, -3, MaxTopK + 1} {
		req := validRequest()
		req.TopK = topK
		_, errs := Resolve(req)
		if len(fieldMessages(errs, "top_k")) == 0 {
			t.Fatalf("top_k=%d: want top_k validation error, got %v", topK, errs)
		}
	}
}

func TestResolveRejectsUnknownRegistryEntries(t *testing.T) {
	tests := []struct {
		field  string
		mutate func(*CreateRequest)
	}{
		{"prompt_version", func(r *CreateRequest) { r.PromptVersion = "v999" }},
		{"model_profile", func(r *CreateRequest) { r.ModelProfile = "unknown-profile" }},
		{"embedding_profile", func(r *CreateRequest) { r.EmbeddingProfile = "unknown-embedding" }},
		{"retrieval_mode", func(r *CreateRequest) { r.RetrievalMode = "keyword" }},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			req := validRequest()
			tt.mutate(&req)
			_, errs := Resolve(req)
			if len(fieldMessages(errs, tt.field)) == 0 {
				t.Fatalf("want %s validation error, got %v", tt.field, errs)
			}
		})
	}
}

func TestResolveRejectsInvalidName(t *testing.T) {
	req := validRequest()
	req.Name = "   "
	_, errs := Resolve(req)
	if len(fieldMessages(errs, "name")) == 0 {
		t.Fatalf("want name validation error, got %v", errs)
	}

	req.Name = strings.Repeat("a", MaxNameLen+1)
	_, errs = Resolve(req)
	if len(fieldMessages(errs, "name")) == 0 {
		t.Fatalf("want name length validation error, got %v", errs)
	}
}

func TestResolveRejectsUnknownRerankerProfile(t *testing.T) {
	req := validRequest()
	req.RerankerProfile = "remote-v9"
	_, errs := Resolve(req)
	if len(fieldMessages(errs, "reranker_profile")) == 0 {
		t.Fatalf("expected reranker profile validation error, got %v", errs)
	}
}

func TestResolveReportsAllViolationsAtOnce(t *testing.T) {
	req := CreateRequest{}
	_, errs := Resolve(req)
	found := map[string]bool{}
	for _, fe := range errs {
		found[fe.Field] = true
	}
	for _, field := range []string{"name", "chunk_size", "retrieval_mode", "top_k",
		"prompt_version", "model_profile", "embedding_profile"} {
		if !found[field] {
			t.Errorf("expected violation for field %q, got %v", field, errs)
		}
	}
}

func TestUnavailableCapabilitiesAndExecutionBlocker(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		rerank    bool
		wantCaps  []string
		wantBlock bool
	}{
		{"vector no rerank is executable", RetrievalModeVector, false, nil, false},
		{"hybrid is executable since RB-17", RetrievalModeHybrid, false, nil, false},
		{"rerank is executable", RetrievalModeVector, true, nil, false},
		{"hybrid plus rerank is executable", RetrievalModeHybrid, true,
			nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{RetrievalMode: tt.mode, RerankEnabled: tt.rerank}
			got := unavailableCapabilitiesFor(tt.mode, tt.rerank)
			if len(got) != len(tt.wantCaps) {
				t.Fatalf("capabilities = %v, want %v", got, tt.wantCaps)
			}
			for i := range got {
				if got[i] != tt.wantCaps[i] {
					t.Fatalf("capabilities = %v, want %v", got, tt.wantCaps)
				}
			}
			err := cfg.ExecutionBlocker()
			if tt.wantBlock {
				var capErr *CapabilityUnavailableError
				if !errors.As(err, &capErr) {
					t.Fatalf("ExecutionBlocker() = %v, want *CapabilityUnavailableError", err)
				}
			} else if err != nil {
				t.Fatalf("ExecutionBlocker() = %v, want nil", err)
			}
		})
	}
}

func TestValidationErrorsMessageListsFields(t *testing.T) {
	_, errs := Resolve(CreateRequest{})
	if len(errs) == 0 {
		t.Fatal("expected validation errors")
	}
	msg := errs.Error()
	if !strings.Contains(msg, "chunk_size") || !strings.Contains(msg, "name") {
		t.Fatalf("error message should list offending fields, got %q", msg)
	}
}
