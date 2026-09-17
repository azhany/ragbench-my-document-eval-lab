package modelprofile

import "testing"

func TestValidateAcceptsGenerationAndEmbeddingAdapters(t *testing.T) {
	for _, req := range []CreateRequest{
		{Name: "support-model-v1", Kind: KindGeneration, Provider: "opencode-go", Model: "glm-5.3-flash"},
		{Name: "support-embedding-v1", Kind: KindEmbedding, Provider: "openai", Model: "text-embedding-3-small", Dimensions: 1536},
	} {
		if _, errs := Validate(req); len(errs) != 0 {
			t.Fatalf("Validate(%+v) = %v", req, errs)
		}
	}
}

func TestValidateRejectsCredentialLikeAndUnsupportedSettings(t *testing.T) {
	tests := []CreateRequest{
		{Name: "bad name", Kind: KindGeneration, Provider: "openai", Model: "gpt-4o-mini"},
		{Name: "custom", Kind: KindEmbedding, Provider: "opencode-go", Model: "x", Dimensions: 3},
		{Name: "custom", Kind: KindGeneration, Provider: "openai", Model: "x", Dimensions: 3},
		{Name: "custom", Kind: KindEmbedding, Provider: "openai", Model: "x", Dimensions: 0},
	}
	for _, req := range tests {
		if _, errs := Validate(req); len(errs) == 0 {
			t.Fatalf("Validate(%+v) accepted invalid settings", req)
		}
	}
}
