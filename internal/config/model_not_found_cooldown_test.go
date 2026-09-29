package config

import "testing"

func TestParseConfigBytesModelNotFoundCooldownSeconds(t *testing.T) {
	cfg, errParse := ParseConfigBytes([]byte("model-not-found-cooldown-seconds: 7\n"))
	if errParse != nil {
		t.Fatalf("ParseConfigBytes() error = %v", errParse)
	}
	if cfg.ModelNotFoundCooldownSeconds != 7 {
		t.Fatalf("ModelNotFoundCooldownSeconds = %d, want 7", cfg.ModelNotFoundCooldownSeconds)
	}

	defaultCfg, errDefault := ParseConfigBytes([]byte("host: 127.0.0.1\n"))
	if errDefault != nil {
		t.Fatalf("ParseConfigBytes(default) error = %v", errDefault)
	}
	if defaultCfg.ModelNotFoundCooldownSeconds != 0 {
		t.Fatalf("default ModelNotFoundCooldownSeconds = %d, want 0", defaultCfg.ModelNotFoundCooldownSeconds)
	}
}
