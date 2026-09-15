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
	// Provider API keys resolved at startup. The embedding-specific key
	// wins over OPENAI_API_KEY, matching the Airflow pipeline precedence.
	// Keys stay in process memory only; they are never logged or persisted.
	EmbeddingAPIKey  string
	GenerationAPIKey string
}

func Load() (Config, error) {
	cfg := Config{
		Addr:             envOr("RAGBENCH_API_ADDR", ":8080"),
		DatabaseURL:      strings.TrimSpace(os.Getenv("DATABASE_URL")),
		UploadDir:        envOr("UPLOAD_DIR", "/data/uploads"),
		AirflowURL:       envOr("AIRFLOW_API_URL", "http://airflow-apiserver:8080"),
		AirflowUsername:  envOr("AIRFLOW_API_USERNAME", "airflow"),
		AirflowPassword:  envOr("AIRFLOW_API_PASSWORD", "airflow"),
		EmbeddingAPIKey:  providers.EmbeddingKey(),
		GenerationAPIKey: providers.GenerationKey(),
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

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
