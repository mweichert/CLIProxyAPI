# Fix: Anthropic Models Endpoint Format

## Summary

Fixes the `/v1/models` endpoint to return the correct Anthropic API format when using the Anthropic SDK, rather than the OpenAI format.

## Problem

When using the Anthropic SDK (e.g., Claude CLI), the `/v1/models` endpoint was returning models in OpenAI format:

```json
{
  "object": "list",
  "data": [
    {"id": "claude-3-opus", "object": "model", ...}
  ]
}
```

The Anthropic SDK expects a different format:

```json
{
  "data": [
    {"id": "claude-3-opus", "type": "model", ...}
  ]
}
```

## Solution

Added handler-specific model formatting in the Claude code handler that returns models in the correct Anthropic format by leveraging the model registry.

## Changes

### New Files
- `sdk/api/handlers/claude/code_handlers.go` - Claude-specific API handler with correct models format

### Modified Files
- `internal/api/server.go` - Route registration for Claude-specific endpoints
- `internal/registry/model_registry.go` - Support for handler-specific model queries

### Removed Files
- `internal/api/handlers/management/api_tools.go` - Removed legacy management handlers (cleanup)

## Files Modified

| File | Change |
|------|--------|
| `internal/api/server.go` | Added Claude handler route registration |
| `internal/registry/model_registry.go` | Added `GetAvailableModels(provider string)` method |
| `sdk/api/handlers/claude/code_handlers.go` | New file with `Models()` returning Anthropic format |

## Testing

1. Start the proxy server
2. Query models using the Anthropic SDK:
   ```bash
   curl http://localhost:8080/v1/models \
     -H "Authorization: Bearer your-key" \
     -H "anthropic-version: 2023-06-01"
   ```
3. Verify response is in Anthropic format with `type: "model"` instead of `object: "model"`
