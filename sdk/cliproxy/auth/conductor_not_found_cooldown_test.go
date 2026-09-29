package auth

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	internallogging "github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestManagerMarkResultModelNotFoundCooldownSetting(t *testing.T) {
	previousDisabled := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	previousSeconds := modelNotFoundCooldownSeconds.Load()
	t.Cleanup(func() {
		quotaCooldownDisabled.Store(previousDisabled)
		modelNotFoundCooldownSeconds.Store(previousSeconds)
	})

	failureCases := []struct {
		name  string
		model string
		err   *Error
	}{
		{
			name:  "model scoped 404",
			model: "gpt-5.6-sol",
			err:   &Error{HTTPStatus: http.StatusNotFound, Message: "not found"},
		},
		{
			name:  "model not supported",
			model: "gpt-5.6-sol",
			err:   &Error{HTTPStatus: http.StatusBadRequest, Message: "requested model is not supported"},
		},
		{
			name: "auth scoped 404",
			err:  &Error{HTTPStatus: http.StatusNotFound, Message: "not found"},
		},
	}
	settings := []struct {
		name            string
		seconds         int
		wantDuration    time.Duration
		wantUnavailable bool
	}{
		{name: "legacy default", seconds: 0, wantDuration: 12 * time.Hour, wantUnavailable: true},
		{name: "one second", seconds: 1, wantDuration: time.Second, wantUnavailable: true},
		{name: "disabled", seconds: -1, wantUnavailable: false},
	}

	for _, failureCase := range failureCases {
		for _, setting := range settings {
			t.Run(failureCase.name+"/"+setting.name, func(t *testing.T) {
				SetModelNotFoundCooldownSeconds(setting.seconds)
				manager := NewManager(nil, nil, nil)
				auth := &Auth{ID: "auth-" + failureCase.name + setting.name, Provider: "codex"}
				if _, errRegister := manager.Register(WithSkipPersist(context.Background()), auth); errRegister != nil {
					t.Fatalf("register auth: %v", errRegister)
				}
				before := time.Now()
				manager.MarkResult(context.Background(), Result{
					AuthID:   auth.ID,
					Provider: auth.Provider,
					Model:    failureCase.model,
					Success:  false,
					Error:    failureCase.err,
				})
				updated, ok := manager.GetByID(auth.ID)
				if !ok || updated == nil {
					t.Fatal("updated auth missing")
				}

				unavailable := updated.Unavailable
				nextRetryAfter := updated.NextRetryAfter
				if failureCase.model != "" {
					state := updated.ModelStates[failureCase.model]
					if state == nil {
						t.Fatalf("model state %q missing", failureCase.model)
					}
					unavailable = state.Unavailable
					nextRetryAfter = state.NextRetryAfter
				}
				if unavailable != setting.wantUnavailable {
					t.Fatalf("unavailable = %t, want %t", unavailable, setting.wantUnavailable)
				}
				if !setting.wantUnavailable {
					if !nextRetryAfter.IsZero() {
						t.Fatalf("disabled cooldown deadline = %v, want zero", nextRetryAfter)
					}
					return
				}
				remaining := nextRetryAfter.Sub(before)
				if remaining < setting.wantDuration-200*time.Millisecond || remaining > setting.wantDuration+200*time.Millisecond {
					t.Fatalf("cooldown duration = %v, want about %v", remaining, setting.wantDuration)
				}
			})
		}
	}
}

func TestManagerMarkResultNotFoundDiagnosticIsSafe(t *testing.T) {
	previousSeconds := modelNotFoundCooldownSeconds.Load()
	SetModelNotFoundCooldownSeconds(1)
	previousLevel := log.GetLevel()
	log.SetLevel(log.InfoLevel)
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() {
		modelNotFoundCooldownSeconds.Store(previousSeconds)
		log.SetLevel(previousLevel)
		hook.Reset()
	})

	manager := NewManager(nil, nil, nil)
	auth := &Auth{
		ID:       "secret-auth-id",
		Provider: "codex",
		Label:    "private@example.invalid",
		FileName: "/secret/private@example.invalid.json",
	}
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	manager.MarkResult(context.Background(), Result{
		AuthID:   auth.ID,
		Provider: auth.Provider,
		Model:    "gpt-5.6-sol",
		Success:  false,
		Error: &Error{
			HTTPStatus: http.StatusNotFound,
			Message:    "raw-upstream-secret",
		},
	})

	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Level != log.InfoLevel || entry.Message != "model eligibility cooldown scheduled" {
		t.Fatalf("entry = level %s message %q", entry.Level, entry.Message)
	}
	if entry.Data["provider"] != "codex" || entry.Data["model"] != "gpt-5.6-sol" || entry.Data["reason"] != "not_found" {
		t.Fatalf("diagnostic fields = %#v", entry.Data)
	}
	if authIndex, _ := entry.Data["auth_index"].(string); len(authIndex) != 16 {
		t.Fatalf("auth_index = %#v, want opaque 16-character index", entry.Data["auth_index"])
	}
	if retryAt, _ := entry.Data["retry_at"].(string); retryAt == "" {
		t.Fatal("retry_at missing")
	}
	serialized, errString := entry.String()
	if errString != nil {
		t.Fatalf("serialize log entry: %v", errString)
	}
	for _, secret := range []string{auth.ID, auth.Label, auth.FileName, "raw-upstream-secret"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("diagnostic leaked %q: %s", secret, serialized)
		}
	}
}

func TestManagerMarkResultNotFoundDiagnosticBoundsUntrustedFields(t *testing.T) {
	previousSeconds := modelNotFoundCooldownSeconds.Load()
	SetModelNotFoundCooldownSeconds(1)
	previousLevel := log.GetLevel()
	log.SetLevel(log.InfoLevel)
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() {
		modelNotFoundCooldownSeconds.Store(previousSeconds)
		log.SetLevel(previousLevel)
		hook.Reset()
	})

	testCases := []struct {
		name     string
		provider string
		model    string
		forbid   string
	}{
		{name: "newline", provider: "codex\nforged-provider", model: "gpt-5.6\r\nforged-model"},
		{name: "nul", provider: "codex\x00forged-provider", model: "gpt-5.6\x00forged-model"},
		{
			name:     "oversized utf8",
			provider: "provider-" + strings.Repeat("界", 100) + "PROVIDER_SECRET_TAIL",
			model:    "model-" + strings.Repeat("界", 100) + "MODEL_SECRET_TAIL",
			forbid:   "SECRET_TAIL",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			hook.Reset()
			manager := NewManager(nil, nil, nil)
			auth := &Auth{
				ID:       "secret-auth-id-" + testCase.name,
				Provider: "codex",
				Label:    "private@example.invalid",
				FileName: "/secret/private@example.invalid.json",
			}
			if _, errRegister := manager.Register(WithSkipPersist(context.Background()), auth); errRegister != nil {
				t.Fatalf("register auth: %v", errRegister)
			}
			manager.MarkResult(context.Background(), Result{
				AuthID:   auth.ID,
				Provider: testCase.provider,
				Model:    testCase.model,
				Success:  false,
				Error:    &Error{HTTPStatus: http.StatusNotFound, Message: "raw-upstream-secret"},
			})

			entries := hook.AllEntries()
			if len(entries) != 1 {
				t.Fatalf("log entries = %d, want 1", len(entries))
			}
			entry := entries[0]
			provider, _ := entry.Data["provider"].(string)
			model, _ := entry.Data["model"].(string)
			for fieldName, value := range map[string]string{"provider": provider, "model": model} {
				if !utf8.ValidString(value) {
					t.Fatalf("%s is not valid UTF-8: %q", fieldName, value)
				}
				if len(value) > modelEligibilityDiagnosticFieldMaxBytes {
					t.Fatalf("%s length = %d, max %d", fieldName, len(value), modelEligibilityDiagnosticFieldMaxBytes)
				}
				if strings.ContainsAny(value, "\r\n\x00") {
					t.Fatalf("%s contains a control character: %q", fieldName, value)
				}
			}

			formatted, errFormat := (&internallogging.LogFormatter{}).Format(entry)
			if errFormat != nil {
				t.Fatalf("format diagnostic: %v", errFormat)
			}
			if bytes.Count(formatted, []byte{'\n'}) != 1 {
				t.Fatalf("diagnostic physical lines = %d, want 1: %q", bytes.Count(formatted, []byte{'\n'}), formatted)
			}
			if len(formatted) > 512 {
				t.Fatalf("formatted diagnostic length = %d, want <= 512", len(formatted))
			}
			formattedText := string(formatted)
			for _, secret := range []string{auth.ID, auth.Label, auth.FileName, "raw-upstream-secret", testCase.forbid} {
				if secret != "" && strings.Contains(formattedText, secret) {
					t.Fatalf("diagnostic leaked %q: %s", secret, formattedText)
				}
			}
		})
	}
}

func TestSchedulerReportsMixedQuotaAndNotFoundFiniteBlocksAsUnavailable(t *testing.T) {
	previousSeconds := modelNotFoundCooldownSeconds.Load()
	SetModelNotFoundCooldownSeconds(1)
	t.Cleanup(func() { modelNotFoundCooldownSeconds.Store(previousSeconds) })

	const (
		provider       = "codex"
		model          = "gpt-5.6-sol"
		quotaAuthID    = "mixed-block-quota-auth"
		notFoundAuthID = "mixed-block-not-found-auth"
	)
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(quotaAuthID, provider, []*registry.ModelInfo{{ID: model}})
	modelRegistry.RegisterClient(notFoundAuthID, provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(quotaAuthID)
		modelRegistry.UnregisterClient(notFoundAuthID)
	})

	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	for _, authID := range []string{quotaAuthID, notFoundAuthID} {
		if _, errRegister := manager.Register(WithSkipPersist(context.Background()), &Auth{ID: authID, Provider: provider}); errRegister != nil {
			t.Fatalf("register auth %q: %v", authID, errRegister)
		}
	}
	quotaRetry := 5 * time.Second
	manager.MarkResult(context.Background(), Result{
		AuthID:     quotaAuthID,
		Provider:   provider,
		Model:      model,
		Success:    false,
		RetryAfter: &quotaRetry,
		Error:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota"},
	})
	manager.MarkResult(context.Background(), Result{
		AuthID:   notFoundAuthID,
		Provider: provider,
		Model:    model,
		Success:  false,
		Error:    &Error{HTTPStatus: http.StatusNotFound, Message: "not found"},
	})

	_, errPick := manager.scheduler.pickSingle(context.Background(), provider, model, cliproxyexecutor.Options{}, nil)
	if errPick == nil {
		t.Fatal("expected mixed finite blocked error")
	}
	authErr, ok := errPick.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", errPick)
	}
	if authErr.Code != "auth_unavailable" || authErr.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("error = %#v, want auth_unavailable/503", authErr)
	}
	if !strings.Contains(authErr.Message, "blocked=2/2") || !strings.Contains(authErr.Message, "retry_after_seconds=1") {
		t.Fatalf("message = %q", authErr.Message)
	}
}

func TestSchedulerReportsFiniteNotFoundBlockWithoutChangingQuotaSemantics(t *testing.T) {
	previousSeconds := modelNotFoundCooldownSeconds.Load()
	SetModelNotFoundCooldownSeconds(1)
	t.Cleanup(func() { modelNotFoundCooldownSeconds.Store(previousSeconds) })

	const (
		provider = "codex"
		model    = "gpt-5.6-sol"
		authID   = "not-found-scheduler-auth"
	)
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(authID, provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { modelRegistry.UnregisterClient(authID) })

	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), &Auth{ID: authID, Provider: provider}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	manager.MarkResult(context.Background(), Result{
		AuthID:   authID,
		Provider: provider,
		Model:    model,
		Success:  false,
		Error:    &Error{HTTPStatus: http.StatusNotFound, Message: "not found"},
	})

	_, errPick := manager.scheduler.pickSingle(context.Background(), provider, model, cliproxyexecutor.Options{}, nil)
	if errPick == nil {
		t.Fatal("expected finite blocked error")
	}
	authErr, ok := errPick.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", errPick)
	}
	if authErr.Code != "auth_unavailable" || authErr.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("error = %#v, want auth_unavailable/503", authErr)
	}
	if !strings.Contains(authErr.Message, "blocked=1/1") || !strings.Contains(authErr.Message, "retry_after_seconds=1") {
		t.Fatalf("message = %q", authErr.Message)
	}
}
