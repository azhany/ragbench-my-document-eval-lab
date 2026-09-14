// Package providers defines the small integration surfaces for embedding and
// generation providers plus the immutable registry of prompt versions and
// model profiles. Profiles are identifiers only — credentials never live here.
//
// The registry is the explicit source of truth: configurations referencing a
// name that is not registered fail validation instead of being silently
// accepted. Adding a profile or prompt version is a deliberate code change,
// so every stored configuration keeps a resolvable identity.
package providers

import (
	"context"
	"fmt"
)

// EmbeddingProfile identifies one embedding provider/model/dimensions
// combination. Dimensions must match the vector column pinned in
// db/migrations/0003_doc_chunks.sql.
type EmbeddingProfile struct {
	Name       string
	Provider   string
	Model      string
	Dimensions int
}

// GenerationProfile identifies one generation provider/model combination.
type GenerationProfile struct {
	Name     string
	Provider string
	Model    string
}

// Prompt is an immutable prompt template reference. The version is the
// identity stored in configurations and runs; the identifier names the
// template that version renders.
type Prompt struct {
	Version    string
	Identifier string
}

var embeddingProfiles = map[string]EmbeddingProfile{
	"openai-text-embedding-3-small": {
		Name:       "openai-text-embedding-3-small",
		Provider:   "openai",
		Model:      "text-embedding-3-small",
		Dimensions: 1536,
	},
}

var generationProfiles = map[string]GenerationProfile{
	"openai-gpt-4o-mini": {
		Name:     "openai-gpt-4o-mini",
		Provider: "openai",
		Model:    "gpt-4o-mini",
	},
}

var prompts = map[string]Prompt{
	"v1": {Version: "v1", Identifier: "grounded-answer"},
}

// EmbeddingProfileByName returns the registered embedding profile with the
// given name, or an error naming the unknown profile.
func EmbeddingProfileByName(name string) (EmbeddingProfile, error) {
	profile, ok := embeddingProfiles[name]
	if !ok {
		return EmbeddingProfile{}, fmt.Errorf("unknown embedding profile %q", name)
	}
	return profile, nil
}

// GenerationProfileByName returns the registered generation profile with the
// given name, or an error naming the unknown profile.
func GenerationProfileByName(name string) (GenerationProfile, error) {
	profile, ok := generationProfiles[name]
	if !ok {
		return GenerationProfile{}, fmt.Errorf("unknown model profile %q", name)
	}
	return profile, nil
}

// PromptByVersion returns the registered prompt with the given version, or an
// error naming the unknown version.
func PromptByVersion(version string) (Prompt, error) {
	prompt, ok := prompts[version]
	if !ok {
		return Prompt{}, fmt.Errorf("unknown prompt version %q", version)
	}
	return prompt, nil
}

// Embedder produces embeddings for a batch of texts under one embedding
// profile. Implementations arrive with RB-07 (embedding pipeline).
type Embedder interface {
	Embed(ctx context.Context, profile EmbeddingProfile, texts []string) ([][]float32, error)
}

// Generator produces one completion for a prompt under one generation
// profile. Implementations arrive with RB-10 (grounded answers).
type Generator interface {
	Generate(ctx context.Context, profile GenerationProfile, prompt string) (string, error)
}
