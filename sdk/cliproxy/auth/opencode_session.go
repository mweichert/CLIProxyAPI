package auth

import (
	"net/url"
	"strings"

	"github.com/google/uuid"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	cliproxysession "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/session"
)

const openCodeSessionHeaderAttribute = "header:X-Opencode-Session"

var openCodeSessionNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://opencode.ai/x-opencode-session/v1"))

// withOpenCodeSessionHeader returns a request-scoped auth clone for opencode.ai
// upstreams. Other auths are returned unchanged so their execution is identical.
func withOpenCodeSessionHeader(auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) *Auth {
	if !isOpenCodeAuth(auth) {
		return auth
	}

	requestAuth := auth.Clone()
	if configuredOpenCodeSession(requestAuth.Attributes) != "" {
		return requestAuth
	}

	sessionID := sessionHeaderValue(opts.Headers, "X-Opencode-Session")
	if sessionID == "" {
		payload := opts.OriginalRequest
		if len(payload) == 0 {
			payload = req.Payload
		}
		identity := ExtractSessionID(opts.Headers, payload, opts.Metadata)
		if identity == "" {
			identity = ExtractSessionID(opts.Headers, payload, req.Metadata)
		}
		if identity != "" {
			sessionID = uuid.NewSHA1(openCodeSessionNamespace, []byte(identity)).String()
		}
	}
	if sessionID == "" {
		return requestAuth
	}
	if requestAuth.Attributes == nil {
		requestAuth.Attributes = make(map[string]string)
	}
	requestAuth.Attributes[openCodeSessionHeaderAttribute] = sessionID
	return requestAuth
}

func isOpenCodeAuth(auth *Auth) bool {
	if auth == nil || auth.Attributes == nil {
		return false
	}
	baseURL := strings.TrimSpace(auth.Attributes["base_url"])
	if baseURL == "" {
		return false
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), "opencode.ai")
}

func configuredOpenCodeSession(attributes map[string]string) string {
	for key, value := range attributes {
		if !strings.HasPrefix(key, "header:") || !strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(key, "header:")), "X-Opencode-Session") {
			continue
		}
		if normalized := cliproxysession.NormalizeExplicitID(value); normalized != "" {
			return normalized
		}
	}
	return ""
}
