// Package logging provides traffic debugging functionality for the CLI Proxy API server.
// This file implements JSON Lines logging for granular traffic debugging.
package logging

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
)

// globalTrafficLogger holds the singleton traffic logger instance.
var (
	globalTrafficLogger     *TrafficLogger
	globalTrafficLoggerOnce sync.Once
	globalTrafficLoggerMu   sync.RWMutex
)

// InitGlobalTrafficLogger initializes the global traffic logger instance.
// This should be called once during server startup.
func InitGlobalTrafficLogger(cfg *config.TrafficDebugConfig, configDir string) error {
	globalTrafficLoggerMu.Lock()
	defer globalTrafficLoggerMu.Unlock()

	logger, err := NewTrafficLogger(cfg, configDir)
	if err != nil {
		return err
	}
	globalTrafficLogger = logger
	return nil
}

// GetGlobalTrafficLogger returns the global traffic logger instance.
// Returns nil if not initialized.
func GetGlobalTrafficLogger() *TrafficLogger {
	globalTrafficLoggerMu.RLock()
	defer globalTrafficLoggerMu.RUnlock()
	return globalTrafficLogger
}

// UpdateGlobalTrafficLogger updates the global traffic logger configuration.
func UpdateGlobalTrafficLogger(cfg *config.TrafficDebugConfig, configDir string) error {
	globalTrafficLoggerMu.Lock()
	defer globalTrafficLoggerMu.Unlock()

	if globalTrafficLogger == nil {
		logger, err := NewTrafficLogger(cfg, configDir)
		if err != nil {
			return err
		}
		globalTrafficLogger = logger
		return nil
	}

	return globalTrafficLogger.UpdateConfig(cfg, configDir)
}

// CloseGlobalTrafficLogger closes the global traffic logger.
func CloseGlobalTrafficLogger() error {
	globalTrafficLoggerMu.Lock()
	defer globalTrafficLoggerMu.Unlock()

	if globalTrafficLogger != nil {
		err := globalTrafficLogger.Close()
		globalTrafficLogger = nil
		return err
	}
	return nil
}

// TrafficLogEntryType represents the type of traffic log entry.
type TrafficLogEntryType string

const (
	// TrafficLogTypeClientRequest represents a client request to the proxy.
	TrafficLogTypeClientRequest TrafficLogEntryType = "client_request"
	// TrafficLogTypeProviderRequest represents a request to an AI provider.
	TrafficLogTypeProviderRequest TrafficLogEntryType = "provider_request"
	// TrafficLogTypeProviderResponse represents a response from an AI provider.
	TrafficLogTypeProviderResponse TrafficLogEntryType = "provider_response"
)

// TrafficLogEntry represents a single JSON Lines entry for traffic debugging.
type TrafficLogEntry struct {
	Timestamp     string              `json:"timestamp"`
	RequestID     string              `json:"request_id"`
	Type          TrafficLogEntryType `json:"type"`
	Method        string              `json:"method,omitempty"`
	URL           string              `json:"url,omitempty"`
	StatusCode    int                 `json:"status_code,omitempty"`
	Headers       map[string][]string `json:"headers,omitempty"`
	Body          string              `json:"body,omitempty"`
	BodyTruncated bool                `json:"body_truncated,omitempty"`
	BodySize      int                 `json:"body_size,omitempty"`
	Provider      string              `json:"provider,omitempty"`
	AuthID        string              `json:"auth_id,omitempty"`
	AuthLabel     string              `json:"auth_label,omitempty"`
	AuthType      string              `json:"auth_type,omitempty"`
	Attempt       int                 `json:"attempt,omitempty"`
	IsStreaming   bool                `json:"is_streaming,omitempty"`
	ChunkCount    int                 `json:"chunk_count,omitempty"`
	TotalBytes    int                 `json:"total_bytes,omitempty"`
	DurationMs    int64               `json:"duration_ms,omitempty"`
	Error         string              `json:"error,omitempty"`
}

// ProviderRequestInfo holds information about a provider request for logging.
type ProviderRequestInfo struct {
	Provider  string
	AuthID    string
	AuthLabel string
	AuthType  string
	Method    string
	URL       string
	Headers   http.Header
	Body      []byte
	Attempt   int
}

// ProviderResponseInfo holds information about a provider response for logging.
type ProviderResponseInfo struct {
	Provider    string
	AuthID      string
	StatusCode  int
	Headers     http.Header
	Body        []byte
	IsStreaming bool
	ChunkCount  int
	TotalBytes  int
	DurationMs  int64
	Attempt     int
	Error       error
}

// TrafficLogger handles JSON Lines traffic logging for debugging.
type TrafficLogger struct {
	mu sync.Mutex

	file     *os.File
	filePath string

	clientRequests       bool
	providerRequests     bool
	providerResponses    bool
	maxBodySize          int
	includeHeaders       bool
	maskSensitiveHeaders bool
}

// NewTrafficLogger creates a new traffic logger from configuration.
// The configDir parameter is used to resolve relative log file paths.
func NewTrafficLogger(cfg *config.TrafficDebugConfig, configDir string) (*TrafficLogger, error) {
	if cfg == nil {
		return &TrafficLogger{}, nil
	}

	logger := &TrafficLogger{
		clientRequests:       cfg.ClientRequests,
		providerRequests:     cfg.ProviderRequests,
		providerResponses:    cfg.ProviderResponses,
		maxBodySize:          cfg.MaxBodySize,
		includeHeaders:       cfg.IncludeHeaders,
		maskSensitiveHeaders: cfg.MaskSensitiveHeaders,
	}

	// Only open the file if any logging is enabled
	if logger.isAnyEnabled() {
		if err := logger.openLogFile(cfg.LogFile, configDir); err != nil {
			return nil, err
		}
	}

	return logger, nil
}

// isAnyEnabled returns true if any traffic logging is enabled.
func (l *TrafficLogger) isAnyEnabled() bool {
	return l.clientRequests || l.providerRequests || l.providerResponses
}

// IsClientRequestsEnabled returns whether client request logging is enabled.
func (l *TrafficLogger) IsClientRequestsEnabled() bool {
	return l.clientRequests
}

// IsProviderRequestsEnabled returns whether provider request logging is enabled.
func (l *TrafficLogger) IsProviderRequestsEnabled() bool {
	return l.providerRequests
}

// IsProviderResponsesEnabled returns whether provider response logging is enabled.
func (l *TrafficLogger) IsProviderResponsesEnabled() bool {
	return l.providerResponses
}

// openLogFile opens the log file, creating directories as needed.
func (l *TrafficLogger) openLogFile(logFile, configDir string) error {
	if logFile == "" {
		logFile = "traffic-debug.jsonl"
	}

	// Expand ~ to home directory
	if strings.HasPrefix(logFile, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			logFile = filepath.Join(home, logFile[1:])
		}
	}

	// Resolve relative paths from config directory
	if !filepath.IsAbs(logFile) && configDir != "" {
		logFile = filepath.Join(configDir, logFile)
	}

	// Ensure directory exists
	dir := filepath.Dir(logFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Open file in append mode
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	l.file = file
	l.filePath = logFile
	return nil
}

// LogClientRequest logs an incoming client request to the proxy.
func (l *TrafficLogger) LogClientRequest(requestID, method, url string, headers map[string][]string, body []byte) {
	if !l.clientRequests {
		return
	}

	entry := TrafficLogEntry{
		Timestamp: time.Now().Format(time.RFC3339Nano),
		RequestID: requestID,
		Type:      TrafficLogTypeClientRequest,
		Method:    method,
		URL:       url,
		BodySize:  len(body),
	}

	if l.includeHeaders && headers != nil {
		entry.Headers = l.processHeaders(headers)
	}

	entry.Body, entry.BodyTruncated = l.truncateBody(body)

	l.writeEntry(entry)
}

// LogProviderRequest logs an outgoing request to an AI provider.
func (l *TrafficLogger) LogProviderRequest(requestID string, info ProviderRequestInfo) {
	if !l.providerRequests {
		return
	}

	entry := TrafficLogEntry{
		Timestamp: time.Now().Format(time.RFC3339Nano),
		RequestID: requestID,
		Type:      TrafficLogTypeProviderRequest,
		Method:    info.Method,
		URL:       info.URL,
		Provider:  info.Provider,
		AuthID:    info.AuthID,
		AuthLabel: info.AuthLabel,
		AuthType:  info.AuthType,
		Attempt:   info.Attempt,
		BodySize:  len(info.Body),
	}

	if l.includeHeaders && info.Headers != nil {
		entry.Headers = l.processHeaders(info.Headers)
	}

	entry.Body, entry.BodyTruncated = l.truncateBody(info.Body)

	l.writeEntry(entry)
}

// LogProviderResponse logs a response from an AI provider.
func (l *TrafficLogger) LogProviderResponse(requestID string, info ProviderResponseInfo) {
	if !l.providerResponses {
		return
	}

	entry := TrafficLogEntry{
		Timestamp:   time.Now().Format(time.RFC3339Nano),
		RequestID:   requestID,
		Type:        TrafficLogTypeProviderResponse,
		StatusCode:  info.StatusCode,
		Provider:    info.Provider,
		AuthID:      info.AuthID,
		IsStreaming: info.IsStreaming,
		ChunkCount:  info.ChunkCount,
		TotalBytes:  info.TotalBytes,
		DurationMs:  info.DurationMs,
		Attempt:     info.Attempt,
	}

	if info.Error != nil {
		entry.Error = info.Error.Error()
	}

	if l.includeHeaders && info.Headers != nil {
		entry.Headers = l.processHeaders(info.Headers)
	}

	if !info.IsStreaming && info.Body != nil {
		entry.BodySize = len(info.Body)
		entry.Body, entry.BodyTruncated = l.truncateBody(info.Body)
	} else if info.IsStreaming {
		entry.BodySize = info.TotalBytes
	}

	l.writeEntry(entry)
}

// processHeaders processes headers, masking sensitive values if configured.
func (l *TrafficLogger) processHeaders(headers map[string][]string) map[string][]string {
	if !l.maskSensitiveHeaders {
		return headers
	}

	masked := make(map[string][]string, len(headers))
	for key, values := range headers {
		maskedValues := make([]string, len(values))
		for i, value := range values {
			maskedValues[i] = util.MaskSensitiveHeaderValue(key, value)
		}
		masked[key] = maskedValues
	}
	return masked
}

// truncateBody truncates body if it exceeds the configured max size.
func (l *TrafficLogger) truncateBody(body []byte) (string, bool) {
	if len(body) == 0 {
		return "", false
	}

	if l.maxBodySize <= 0 || len(body) <= l.maxBodySize {
		return string(body), false
	}

	return string(body[:l.maxBodySize]) + "...[TRUNCATED]", true
}

// writeEntry writes a log entry to the file.
func (l *TrafficLogger) writeEntry(entry TrafficLogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return
	}

	data, err := json.Marshal(entry)
	if err != nil {
		log.WithError(err).Warn("failed to marshal traffic log entry")
		return
	}

	// Append newline for JSON Lines format
	data = append(data, '\n')

	if _, err := l.file.Write(data); err != nil {
		log.WithError(err).Warn("failed to write traffic log entry")
	}
}

// UpdateConfig updates the logger configuration for hot-reload support.
func (l *TrafficLogger) UpdateConfig(cfg *config.TrafficDebugConfig, configDir string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if cfg == nil {
		l.clientRequests = false
		l.providerRequests = false
		l.providerResponses = false
		if l.file != nil {
			_ = l.file.Close()
			l.file = nil
		}
		return nil
	}

	l.clientRequests = cfg.ClientRequests
	l.providerRequests = cfg.ProviderRequests
	l.providerResponses = cfg.ProviderResponses
	l.maxBodySize = cfg.MaxBodySize
	l.includeHeaders = cfg.IncludeHeaders
	l.maskSensitiveHeaders = cfg.MaskSensitiveHeaders

	// Resolve the new file path
	newFilePath := cfg.LogFile
	if newFilePath == "" {
		newFilePath = "traffic-debug.jsonl"
	}
	if strings.HasPrefix(newFilePath, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			newFilePath = filepath.Join(home, newFilePath[1:])
		}
	}
	if !filepath.IsAbs(newFilePath) && configDir != "" {
		newFilePath = filepath.Join(configDir, newFilePath)
	}

	// Check if we need to reopen the file
	needsReopen := l.filePath != newFilePath || (l.file == nil && l.isAnyEnabledUnlocked())

	if !l.isAnyEnabledUnlocked() {
		// Close file if logging is disabled
		if l.file != nil {
			_ = l.file.Close()
			l.file = nil
		}
		return nil
	}

	if needsReopen {
		if l.file != nil {
			_ = l.file.Close()
			l.file = nil
		}

		// Ensure directory exists
		dir := filepath.Dir(newFilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}

		file, err := os.OpenFile(newFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}

		l.file = file
		l.filePath = newFilePath
	}

	return nil
}

// isAnyEnabledUnlocked returns true if any logging is enabled (must hold lock).
func (l *TrafficLogger) isAnyEnabledUnlocked() bool {
	return l.clientRequests || l.providerRequests || l.providerResponses
}

// Close closes the log file.
func (l *TrafficLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}
