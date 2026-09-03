package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executionregistry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

const opencodeSessionTestModel = "grok-test"

type opencodeSessionCaptureExecutor struct {
	mu               sync.Mutex
	sessions         []string
	unauthorizedMode string
}

func (e *opencodeSessionCaptureExecutor) Identifier() string { return "xai" }

func (e *opencodeSessionCaptureExecutor) capture(auth *Auth, mode string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessions = append(e.sessions, auth.Attributes[openCodeSessionHeaderAttribute])
	if e.unauthorizedMode == mode {
		e.unauthorizedMode = ""
		return &Error{HTTPStatus: http.StatusUnauthorized, Message: "expired"}
	}
	return nil
}

func (e *opencodeSessionCaptureExecutor) Execute(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, e.capture(auth, "execute")
}

func (e *opencodeSessionCaptureExecutor) ExecuteStream(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	if err := e.capture(auth, "stream"); err != nil {
		return nil, err
	}
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("ok")}
	close(chunks)
	return &cliproxyexecutor.StreamResult{Chunks: chunks}, nil
}

func (e *opencodeSessionCaptureExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	updated := auth.Clone()
	updated.Metadata["access_token"] = "fresh-token"
	return updated, nil
}

func (e *opencodeSessionCaptureExecutor) CountTokens(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, e.capture(auth, "count")
}

func (e *opencodeSessionCaptureExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func (e *opencodeSessionCaptureExecutor) Sessions() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.sessions...)
}

func newOpenCodeSessionTestManager(t *testing.T, executor *opencodeSessionCaptureExecutor, auth *Auth) *Manager {
	t.Helper()
	manager := NewManager(nil, nil, nil)
	manager.SetRetryConfig(0, 0, 1)
	manager.RegisterExecutor(executor)
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: opencodeSessionTestModel}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return manager
}

func TestOpenCodeSessionRequestAuthStableAcrossTurnsAndExecutionModes(t *testing.T) {
	executor := &opencodeSessionCaptureExecutor{}
	auth := &Auth{
		ID:       "opencode-session-auth-" + uuid.NewString(),
		Provider: "xai",
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": "https://opencode.ai/zen/go/v1",
		},
	}
	original := auth.Clone()
	manager := newOpenCodeSessionTestManager(t, executor, auth)

	firstTurn := []byte(`{"model":"grok-test","messages":[{"role":"user","content":"stable root"}]}`)
	secondTurn := []byte(`{"model":"grok-test","messages":[{"role":"user","content":"stable root"},{"role":"assistant","content":"answer"},{"role":"user","content":"follow up"}]}`)
	differentRoot := []byte(`{"model":"grok-test","messages":[{"role":"user","content":"different root"}]}`)

	invoke := func(payload []byte, mode string) {
		t.Helper()
		req := cliproxyexecutor.Request{Model: opencodeSessionTestModel, Payload: payload}
		opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai"), OriginalRequest: payload}
		switch mode {
		case "execute":
			if _, err := manager.Execute(context.Background(), []string{"xai"}, req, opts); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
		case "stream":
			result, err := manager.ExecuteStream(context.Background(), []string{"xai"}, req, opts)
			if err != nil {
				t.Fatalf("ExecuteStream() error = %v", err)
			}
			for range result.Chunks {
			}
		case "count":
			if _, err := manager.ExecuteCount(context.Background(), []string{"xai"}, req, opts); err != nil {
				t.Fatalf("ExecuteCount() error = %v", err)
			}
		}
	}

	invoke(firstTurn, "execute")
	invoke(secondTurn, "execute")
	invoke(firstTurn, "stream")
	invoke(firstTurn, "count")
	invoke(differentRoot, "execute")

	sessions := executor.Sessions()
	if len(sessions) != 5 {
		t.Fatalf("captured sessions = %v, want 5", sessions)
	}
	for index := 0; index < 4; index++ {
		if sessions[index] == "" || sessions[index] != sessions[0] {
			t.Fatalf("session[%d] = %q, want stable non-empty %q", index, sessions[index], sessions[0])
		}
		if _, err := uuid.Parse(sessions[index]); err != nil {
			t.Fatalf("session[%d] = %q, want opaque UUID: %v", index, sessions[index], err)
		}
	}
	if sessions[4] == "" || sessions[4] == sessions[0] {
		t.Fatalf("different-root session = %q, want non-empty and different from %q", sessions[4], sessions[0])
	}
	stored, ok := manager.GetByID(auth.ID)
	if !ok {
		t.Fatal("registered auth disappeared")
	}
	if !reflect.DeepEqual(stored.Attributes, original.Attributes) {
		t.Fatalf("shared auth attributes mutated: got %#v want %#v", stored.Attributes, original.Attributes)
	}
}

func TestOpenCodeSessionRequestAuthExplicitConfiguredAndNonOpenCode(t *testing.T) {
	request := cliproxyexecutor.Request{
		Payload:  []byte(`{"messages":[{"role":"user","content":"hello"}]}`),
		Metadata: map[string]any{cliproxyexecutor.DerivedSessionIDMetadataKey: "ctx:v1:root"},
	}
	opts := cliproxyexecutor.Options{
		Headers:  http.Header{"X-Opencode-Session": {"caller-session"}},
		Metadata: map[string]any{cliproxyexecutor.DerivedSessionIDMetadataKey: "ctx:v1:root"},
	}

	opencodeAuth := &Auth{Attributes: map[string]string{"base_url": "https://opencode.ai/zen/v1", "keep": "yes"}}
	got := withOpenCodeSessionHeader(opencodeAuth, request, opts)
	if got == opencodeAuth {
		t.Fatal("OpenCode request auth was not cloned")
	}
	if value := got.Attributes[openCodeSessionHeaderAttribute]; value != "caller-session" {
		t.Fatalf("explicit session = %q, want caller-session", value)
	}
	if _, exists := opencodeAuth.Attributes[openCodeSessionHeaderAttribute]; exists {
		t.Fatal("shared OpenCode auth was mutated")
	}

	configured := &Auth{Attributes: map[string]string{
		"base_url":                     "https://opencode.ai/zen/v1",
		openCodeSessionHeaderAttribute: "configured-session",
	}}
	if value := withOpenCodeSessionHeader(configured, request, opts).Attributes[openCodeSessionHeaderAttribute]; value != "configured-session" {
		t.Fatalf("configured session = %q, want configured-session", value)
	}

	invalidOpts := opts
	invalidOpts.Headers = http.Header{"X-Opencode-Session": {"bad\nsession"}}
	invalid := withOpenCodeSessionHeader(opencodeAuth, request, invalidOpts)
	if value := invalid.Attributes[openCodeSessionHeaderAttribute]; value == "" || value == "bad\nsession" {
		t.Fatalf("invalid inbound session produced %q, want synthesized safe value", value)
	}

	nonOpenCode := &Auth{Attributes: map[string]string{"base_url": "https://example.com/opencode.ai/v1", "keep": "yes"}}
	if got := withOpenCodeSessionHeader(nonOpenCode, request, opts); got != nonOpenCode {
		t.Fatal("non-OpenCode auth was cloned or changed")
	}
}

func TestOpenCodeSessionRequestAuthSurvivesUnauthorizedRefresh(t *testing.T) {
	for _, mode := range []string{"execute", "stream", "count"} {
		t.Run(mode, func(t *testing.T) {
			executor := &opencodeSessionCaptureExecutor{unauthorizedMode: mode}
			auth := &Auth{
				ID:         "opencode-refresh-auth-" + uuid.NewString(),
				Provider:   "xai",
				Status:     StatusActive,
				Attributes: map[string]string{"auth_kind": "oauth", "base_url": "https://opencode.ai/zen/go/v1"},
				Metadata:   map[string]any{"access_token": "stale-token", "refresh_token": "refresh-token"},
			}
			manager := newOpenCodeSessionTestManager(t, executor, auth)
			payload := []byte(`{"model":"grok-test","messages":[{"role":"user","content":"refresh root"}]}`)
			req := cliproxyexecutor.Request{Model: opencodeSessionTestModel, Payload: payload}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai"), OriginalRequest: payload}
			var err error
			switch mode {
			case "execute":
				_, err = manager.Execute(context.Background(), []string{"xai"}, req, opts)
			case "stream":
				var stream *cliproxyexecutor.StreamResult
				stream, err = manager.ExecuteStream(context.Background(), []string{"xai"}, req, opts)
				if err == nil {
					for range stream.Chunks {
					}
				}
			case "count":
				_, err = manager.ExecuteCount(context.Background(), []string{"xai"}, req, opts)
			}
			if err != nil {
				t.Fatalf("%s error = %v", mode, err)
			}
			sessions := executor.Sessions()
			if len(sessions) != 2 || sessions[0] == "" || sessions[0] != sessions[1] {
				t.Fatalf("%s refresh sessions = %v, want two equal non-empty values", mode, sessions)
			}
			stored, ok := manager.GetByID(auth.ID)
			if !ok {
				t.Fatal("registered auth disappeared")
			}
			if _, exists := stored.Attributes[openCodeSessionHeaderAttribute]; exists {
				t.Fatal("request-scoped header persisted after refresh")
			}
		})
	}
}

type openCodeSessionHomeDispatcher struct{}

func (openCodeSessionHomeDispatcher) HeartbeatOK() bool       { return true }
func (openCodeSessionHomeDispatcher) AbortAmbiguousDispatch() {}
func (openCodeSessionHomeDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	return json.Marshal(homeAuthDispatchResponse{Auth: Auth{
		ID: "home-opencode-auth", Provider: "xai", Status: StatusActive,
		Attributes: map[string]string{"base_url": "https://opencode.ai/zen/go/v1"},
	}})
}

func TestOpenCodeSessionHomeExecuteAndCountTokens(t *testing.T) {
	for _, mode := range []string{"execute", "count"} {
		t.Run(mode, func(t *testing.T) {
			manager := NewManager(nil, nil, nil)
			manager.SetConfig(&internalconfig.Config{Home: internalconfig.HomeConfig{Enabled: true}})
			manager.PublishHomeDispatch(openCodeSessionHomeDispatcher{}, executionregistry.New(), 1)
			executor := &opencodeSessionCaptureExecutor{}
			manager.RegisterExecutor(executor)
			payload := []byte(`{"model":"grok-test","messages":[{"role":"user","content":"home root"}]}`)
			req := cliproxyexecutor.Request{Model: opencodeSessionTestModel, Payload: payload}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai"), OriginalRequest: payload}
			var err error
			if mode == "execute" {
				_, err = manager.Execute(context.Background(), []string{"xai"}, req, opts)
			} else {
				_, err = manager.ExecuteCount(context.Background(), []string{"xai"}, req, opts)
			}
			if err != nil {
				t.Fatalf("Home %s error = %v", mode, err)
			}
			sessions := executor.Sessions()
			if len(sessions) != 1 || sessions[0] == "" {
				t.Fatalf("Home %s sessions = %v, want one non-empty value", mode, sessions)
			}
		})
	}
}

func TestExtractSessionIDOpenCodeHeaderPriority(t *testing.T) {
	headers := http.Header{
		"X-Opencode-Session": {"opencode-session"},
		"X-Session-ID":       {"weaker-session"},
	}
	if got := ExtractSessionID(headers, nil, nil); got != "opencode:opencode-session" {
		t.Fatalf("ExtractSessionID() = %q, want opencode header identity", got)
	}
}
