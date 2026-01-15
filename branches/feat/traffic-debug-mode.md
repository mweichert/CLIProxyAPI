# Feature: Traffic Debug Mode

## Summary

Adds a comprehensive traffic debug mode that logs all HTTP requests and responses to JSON Lines files for debugging and analysis purposes.

## Changes

### New Files
- `internal/logging/traffic_logger.go` - Core traffic logging implementation with JSON Lines output
- `internal/runtime/executor/logging_helpers.go` - Helper functions for logging in executors

### Modified Files
- `config.example.yaml` - Added traffic debug configuration section
- `internal/api/middleware/request_logging.go` - Integration with traffic logger
- `internal/api/server.go` - Server initialization with traffic logger
- `internal/config/config.go` - Traffic debug config struct
- `internal/config/sdk_config.go` - SDK config support for traffic debug
- `internal/watcher/diff/config_diff.go` - Hot-reload support for traffic debug config

## Configuration

Add to `config.yaml`:

```yaml
traffic-debug:
  enabled: true
  output-dir: "./logs/traffic"
  log-requests: true
  log-responses: true
  redact-headers:
    - Authorization
    - X-Api-Key
```

## Log Entry Types

The traffic logger produces three types of JSON Lines entries:

1. **client_request** - Incoming request from client to proxy
2. **provider_request** - Outgoing request from proxy to AI provider
3. **provider_response** - Response from AI provider back to proxy

## Features

- JSON Lines format for easy parsing and analysis
- Configurable header redaction for security
- Hot-reload configuration support
- Thread-safe singleton pattern
- Automatic log file rotation

## Dependencies

This branch includes the `fix/anthropic-models-endpoint-format` fix as it was merged during development.

## Testing

1. Enable traffic debug mode in config
2. Make API requests to the proxy
3. Check the output directory for `.jsonl` files
4. Parse and analyze with tools like `jq`:
   ```bash
   cat traffic.jsonl | jq 'select(.type == "provider_request")'
   ```
