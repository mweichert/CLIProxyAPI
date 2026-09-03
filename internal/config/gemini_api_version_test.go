package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeGeminiKeysNormalizesAndKeepsDistinctAPIVersions(t *testing.T) {
	cfg := &Config{GeminiKey: []GeminiKey{
		{APIKey: " key ", BaseURL: " https://example.test ", APIVersion: " V1 "},
		{APIKey: "key", BaseURL: "https://example.test", APIVersion: "v1beta"},
		{APIKey: "key", BaseURL: "https://example.test", APIVersion: "v1"},
	}}

	cfg.SanitizeGeminiKeys()

	if len(cfg.GeminiKey) != 2 {
		t.Fatalf("GeminiKey count = %d, want 2 distinct versions", len(cfg.GeminiKey))
	}
	if cfg.GeminiKey[0].APIVersion != "v1" || cfg.GeminiKey[1].APIVersion != "v1beta" {
		t.Fatalf("api versions = %#v, want v1 and v1beta", []string{cfg.GeminiKey[0].APIVersion, cfg.GeminiKey[1].APIVersion})
	}
}

func TestLoadConfigRejectsUnsupportedGeminiAPIVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("gemini-api-key:\n  - api-key: test\n    api-version: v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "api-version must be v1 or v1beta") {
		t.Fatalf("LoadConfig() error = %v, want api-version validation", err)
	}
}
