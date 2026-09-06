package cliproxy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestApplyOAuthModelAlias_Rename(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"codex": {
				{Name: "gpt-5", Alias: "g5", DisplayName: "Configured GPT Five"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "gpt-5", Name: "models/gpt-5", DisplayName: "Upstream GPT Five"},
	}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 1 {
		t.Fatalf("expected 1 model, got %d", len(out))
	}
	if out[0].ID != "g5" {
		t.Fatalf("expected model id %q, got %q", "g5", out[0].ID)
	}
	if out[0].Name != "models/g5" {
		t.Fatalf("expected model name %q, got %q", "models/g5", out[0].Name)
	}
	if out[0].DisplayName != "Configured GPT Five" {
		t.Fatalf("expected display name %q, got %q", "Configured GPT Five", out[0].DisplayName)
	}
}

func TestApplyOAuthModelAlias_ForkAddsAlias(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"codex": {
				{Name: "gpt-5", Alias: "g5", Fork: true, DisplayName: "Configured GPT Five"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "gpt-5", Name: "models/gpt-5", DisplayName: "Upstream GPT Five"},
	}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 2 {
		t.Fatalf("expected 2 models, got %d", len(out))
	}
	if out[0].ID != "gpt-5" {
		t.Fatalf("expected first model id %q, got %q", "gpt-5", out[0].ID)
	}
	if out[1].ID != "g5" {
		t.Fatalf("expected second model id %q, got %q", "g5", out[1].ID)
	}
	if out[1].Name != "models/g5" {
		t.Fatalf("expected forked model name %q, got %q", "models/g5", out[1].Name)
	}
	if out[0].DisplayName != "Upstream GPT Five" {
		t.Fatalf("expected original display name %q, got %q", "Upstream GPT Five", out[0].DisplayName)
	}
	if out[1].DisplayName != "Configured GPT Five" {
		t.Fatalf("expected alias display name %q, got %q", "Configured GPT Five", out[1].DisplayName)
	}
}

func TestApplyOAuthModelAlias_PreservesUpstreamDisplayNameByDefault(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"codex": {
				{Name: "gpt-5", Alias: "g5"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "gpt-5", DisplayName: "Upstream GPT Five"},
	}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 1 {
		t.Fatalf("expected 1 model, got %d", len(out))
	}
	if out[0].DisplayName != "Upstream GPT Five" {
		t.Fatalf("expected upstream display name %q, got %q", "Upstream GPT Five", out[0].DisplayName)
	}
}

func TestApplyOAuthModelAlias_ForkAddsMultipleAliases(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"codex": {
				{Name: "gpt-5", Alias: "g5", Fork: true},
				{Name: "gpt-5", Alias: "g5-2", Fork: true},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "gpt-5", Name: "models/gpt-5"},
	}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 3 {
		t.Fatalf("expected 3 models, got %d", len(out))
	}
	if out[0].ID != "gpt-5" {
		t.Fatalf("expected first model id %q, got %q", "gpt-5", out[0].ID)
	}
	if out[1].ID != "g5" {
		t.Fatalf("expected second model id %q, got %q", "g5", out[1].ID)
	}
	if out[1].Name != "models/g5" {
		t.Fatalf("expected forked model name %q, got %q", "models/g5", out[1].Name)
	}
	if out[2].ID != "g5-2" {
		t.Fatalf("expected third model id %q, got %q", "g5-2", out[2].ID)
	}
	if out[2].Name != "models/g5-2" {
		t.Fatalf("expected forked model name %q, got %q", "models/g5-2", out[2].Name)
	}
}

func TestApplyOAuthModelAlias_PluginProvider(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"sample-provider": {
				{Name: "sample-model-latest", Alias: "sample-latest"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "sample-model-latest", Name: "models/sample-model-latest"},
	}

	out := applyOAuthModelAlias(cfg, "sample-provider", "oauth", models)
	if len(out) != 1 {
		t.Fatalf("expected 1 model, got %d", len(out))
	}
	if out[0].ID != "sample-latest" {
		t.Fatalf("expected plugin alias id %q, got %q", "sample-latest", out[0].ID)
	}
	if out[0].Name != "models/sample-latest" {
		t.Fatalf("expected plugin alias name %q, got %q", "models/sample-latest", out[0].Name)
	}
}

func TestApplyOAuthModelAlias_PluginProviderSkipsAPIKey(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"sample-provider": {
				{Name: "sample-model-latest", Alias: "sample-latest"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "sample-model-latest", Name: "models/sample-model-latest"},
	}

	out := applyOAuthModelAlias(cfg, "sample-provider", "api_key", models)
	if len(out) != 1 || out[0].ID != "sample-model-latest" {
		t.Fatalf("expected API key plugin model to remain unchanged, got %#v", out)
	}
}

func TestApplyOAuthModelAlias_PerAuthAlias(t *testing.T) {
	models := []*ModelInfo{
		{ID: "gpt-5.3-codex-spark", Name: "models/gpt-5.3-codex-spark"},
	}
	attributes := map[string]string{
		"model_aliases": `[{"name":"gpt-5.3-codex-spark","alias":"gpt-5.5","display-name":"Configured GPT Five"}]`,
	}

	out := applyOAuthModelAliasForAuth(nil, "codex", "oauth", attributes, models)
	if len(out) != 1 {
		t.Fatalf("expected 1 model, got %d", len(out))
	}
	if out[0].ID != "gpt-5.5" {
		t.Fatalf("expected per-auth alias id %q, got %q", "gpt-5.5", out[0].ID)
	}
	if out[0].Name != "models/gpt-5.5" {
		t.Fatalf("expected per-auth alias name %q, got %q", "models/gpt-5.5", out[0].Name)
	}
	if out[0].DisplayName != "Configured GPT Five" {
		t.Fatalf("expected per-auth display name %q, got %q", "Configured GPT Five", out[0].DisplayName)
	}
}

func TestApplyOAuthModelAlias_DisplayOnlyKeepsModelID(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"codex": {
				{Name: "gpt-5.5", Alias: "gpt-5.5", DisplayName: "Daily Driver"},
			},
		},
	}
	models := []*ModelInfo{{ID: "gpt-5.5", Name: "models/gpt-5.5", DisplayName: "GPT-5.5"}}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 1 || out[0].ID != "gpt-5.5" {
		t.Fatalf("display-only override changed model identity: %#v", out)
	}
	if out[0].DisplayName != "Daily Driver" {
		t.Fatalf("display-only override = %q, want Daily Driver", out[0].DisplayName)
	}
}

func TestApplyOAuthModelAlias_DisplayOnlySurvivesYAMLLoad(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("oauth-model-alias:\n  codex:\n    - name: gpt-5.5\n      alias: gpt-5.5\n      display-name: Daily Driver\n")
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	models := []*ModelInfo{{ID: "gpt-5.5", Name: "models/gpt-5.5", DisplayName: "GPT-5.5"}}
	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 1 || out[0].ID != "gpt-5.5" || out[0].DisplayName != "Daily Driver" {
		t.Fatalf("loaded display-only override = %#v", out)
	}
}

func TestApplyOAuthModelAlias_DisplayOnlyAppliesAlongsideForkAlias(t *testing.T) {
	cfg := &config.Config{OAuthModelAlias: map[string][]config.OAuthModelAlias{
		"codex": {
			{Name: "gpt-5.6-sol", Alias: "gpt-5.6-sol-1m", DisplayName: "GPT-5.6 Sol (1M)", Fork: true},
			{Name: "gpt-5.6-sol", Alias: "gpt-5.6-sol", DisplayName: "Daily Sol"},
		},
	}}
	models := []*ModelInfo{{ID: "gpt-5.6-sol", Name: "models/gpt-5.6-sol", DisplayName: "GPT 5.6 Sol"}}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 2 {
		t.Fatalf("display-plus-fork output = %#v, want original and fork", out)
	}
	if out[0].ID != "gpt-5.6-sol" || out[0].DisplayName != "Daily Sol" {
		t.Fatalf("display-only original = %#v, want Daily Sol with unchanged ID", out[0])
	}
	if out[1].ID != "gpt-5.6-sol-1m" || out[1].DisplayName != "GPT-5.6 Sol (1M)" {
		t.Fatalf("fork alias = %#v, want labelled 1M alias", out[1])
	}
}

func TestApplyOAuthModelAlias_DisplayOnlyAddsMissingConfiguredModel(t *testing.T) {
	cfg := &config.Config{OAuthModelAlias: map[string][]config.OAuthModelAlias{
		"codex": {{Name: "gpt-5.4-mini", Alias: "gpt-5.4-mini", DisplayName: "GPT-5.4-Mini"}},
	}}
	out := applyOAuthModelAlias(cfg, "codex", "oauth", nil)
	if len(out) != 1 || out[0].ID != "gpt-5.4-mini" || out[0].Name != "models/gpt-5.4-mini" || out[0].DisplayName != "GPT-5.4-Mini" {
		t.Fatalf("missing display-only model = %#v", out)
	}
}
