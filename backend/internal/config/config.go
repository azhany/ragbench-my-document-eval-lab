package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"ragbench-my/backend/internal/providers"
)

type Config struct {
	Addr            string
	DatabaseURL     string
	UploadDir       string
	MaxUploadBytes  int64
	AirflowURL      string
	AirflowUsername string
	AirflowPassword string
	// Provider endpoints and credentials are resolved at startup. Keys stay in
	// process memory only; they are never logged or persisted.
	OpenAIBaseURL               string
	OpenAIEmbeddingAPIKey       string
	OpenAIGenerationAPIKey      string
	OpenAICompatibleBaseURL     string
	OpenAICompatibleAPIKey      string
	HuggingFaceEmbeddingBaseURL string
	HuggingFaceAPIKey           string
}

func Load() (Config, error) {
	cfg := Config{
		Addr:                        envOr("RAGBENCH_API_ADDR", ":8080"),
		DatabaseURL:                 strings.TrimSpace(os.Getenv("DATABASE_URL")),
		UploadDir:                   envOr("UPLOAD_DIR", "/data/uploads"),
		AirflowURL:                  envOr("AIRFLOW_API_URL", "http://airflow-apiserver:8080"),
		AirflowUsername:             envOr("AIRFLOW_API_USERNAME", "airflow"),
		AirflowPassword:             envOr("AIRFLOW_API_PASSWORD", "airflow"),
		OpenAIBaseURL:               envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIEmbeddingAPIKey:       providers.EmbeddingKey(),
		OpenAIGenerationAPIKey:      providers.GenerationKey(),
		OpenAICompatibleBaseURL:     envOr("OPENAI_COMPATIBLE_BASE_URL", "https://opencode.ai/zen/go/v1"),
		OpenAICompatibleAPIKey:      firstEnv("OPENCODE_API_KEY", "GENERATION_PROVIDER_API_KEY", "OPENAI_API_KEY"),
		HuggingFaceEmbeddingBaseURL: envOr("HUGGINGFACE_EMBEDDING_BASE_URL", "https://router.huggingface.co/hf-inference/models"),
		HuggingFaceAPIKey:           firstEnv("HF_TOKEN", "HUGGINGFACE_API_KEY", "EMBEDDING_PROVIDER_API_KEY"),
	}
	var err error
	cfg.MaxUploadBytes, err = strconv.ParseInt(envOr("MAX_UPLOAD_BYTES", "20971520"), 10, 64)
	if err != nil || cfg.MaxUploadBytes <= 0 || cfg.MaxUploadBytes > 1<<30 {
		return Config{}, errors.New("MAX_UPLOAD_BYTES must be between 1 and 1073741824")
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required, e.g. postgres://ragbench:ragbench@postgres:5432/ragbench?sslmode=disable")
	}
	if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		return Config{}, fmt.Errorf("invalid RAGBENCH_API_ADDR %q: %w", cfg.Addr, err)
	}

	return cfg, nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
