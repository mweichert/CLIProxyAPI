package diff

import (
	"slices"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestBuildConfigChangeDetailsModelNotFoundCooldown(t *testing.T) {
	changes := BuildConfigChangeDetails(
		&config.Config{ModelNotFoundCooldownSeconds: 0},
		&config.Config{ModelNotFoundCooldownSeconds: 1},
	)
	if !slices.Contains(changes, "model-not-found-cooldown-seconds: 0 -> 1") {
		t.Fatalf("changes = %v", changes)
	}
}
