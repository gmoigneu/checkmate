package config

import (
	"testing"
	"time"
)

func TestLoadReportGenerationConfig(t *testing.T) {
	t.Setenv("CHECKMATE_ENV", "development")
	t.Setenv("CHECKMATE_BASE_URL", "http://localhost:8080")
	t.Setenv("CHECKMATE_OPENROUTER_API_KEY", "  secret  ")
	t.Setenv("CHECKMATE_OPENROUTER_BASE_URL", "https://openrouter.example/v1/")
	t.Setenv("CHECKMATE_REPORT_GENERATION_TIMEOUT", "2m3s")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenRouterAPIKey != "secret" {
		t.Fatalf("OpenRouterAPIKey = %q", cfg.OpenRouterAPIKey)
	}
	if cfg.OpenRouterBaseURL != "https://openrouter.example/v1" {
		t.Fatalf("OpenRouterBaseURL = %q", cfg.OpenRouterBaseURL)
	}
	if cfg.ReportGenerationTimeout != 2*time.Minute+3*time.Second {
		t.Fatalf("ReportGenerationTimeout = %s", cfg.ReportGenerationTimeout)
	}
}

func TestLoadRejectsInvalidOpenRouterBaseURL(t *testing.T) {
	t.Setenv("CHECKMATE_ENV", "development")
	t.Setenv("CHECKMATE_BASE_URL", "http://localhost:8080")
	t.Setenv("CHECKMATE_OPENROUTER_BASE_URL", "file:///tmp/openrouter")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a non-HTTP OpenRouter URL")
	}
}
