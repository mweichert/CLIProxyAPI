package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestOpenAICompatExecutorOpenCodeSessionHeader(t *testing.T) {
	const sessionID = "8fe69268-0ac3-58cf-94a2-491fdb19e257"
	var mu sync.Mutex
	var sessions []string
	var customValues []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		sessions = append(sessions, request.Header.Get("X-Opencode-Session"))
		customValues = append(customValues, request.Header.Get("X-Test-Custom"))
		mu.Unlock()
		if request.Header.Get("Accept") == "text/event-stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url":                  server.URL + "/v1",
		"api_key":                   "fixture-key",
		"header:X-Opencode-Session": sessionID,
		"header:X-Test-Custom":      "still-present",
	}}
	payload := []byte(`{"model":"chat-model","messages":[{"role":"user","content":"hello"}]}`)
	req := cliproxyexecutor.Request{Model: "chat-model", Payload: payload}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai"), OriginalRequest: payload}

	if _, err := executor.Execute(context.Background(), auth, req, opts); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	stream, err := executor.ExecuteStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sessions) != 2 || sessions[0] != sessionID || sessions[1] != sessionID {
		t.Fatalf("upstream sessions = %v, want two %q values", sessions, sessionID)
	}
	if len(customValues) != 2 || customValues[0] != "still-present" || customValues[1] != "still-present" {
		t.Fatalf("custom headers = %v, want preserved values", customValues)
	}
}
