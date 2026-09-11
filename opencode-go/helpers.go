package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	// maxQuotaResponseBytes bounds every upstream body the plugin buffers.
	maxQuotaResponseBytes = 1 << 20
)

func getEnvironment(name string) string { return os.Getenv(name) }

func contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return context.WithTimeout(parent, timeout)
}

func nowUTC(now func() time.Time) time.Time {
	if now == nil {
		now = time.Now
	}
	return now().UTC()
}

func okEnvelope(result any) []byte {
	raw, err := json.Marshal(envelope{OK: true, Result: mustJSON(result)})
	if err != nil {
		return errorEnvelope("encode_failed", "encode result failed")
	}
	return raw
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: firstNonEmpty(code, "plugin_error"), Message: message}})
	return raw
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func jsonManagementResponse(status int, value any) pluginapi.ManagementResponse {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json; charset=utf-8")
	return pluginapi.ManagementResponse{StatusCode: status, Headers: headers, Body: mustJSON(value)}
}

func htmlManagementResponse(body string) pluginapi.ManagementResponse {
	headers := make(http.Header)
	headers.Set("Content-Type", "text/html; charset=utf-8")
	headers.Set("Cache-Control", "no-store")
	return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: headers, Body: []byte(body)}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// maskCredential never reveals any part of a stored secret.
func maskCredential(string) string { return "••••••••" }

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func formatPercent(value float64) string {
	return fmt.Sprintf("%.1f%%", clamp(value))
}

func readLimitedResponse(body io.Reader, limit int) ([]byte, error) {
	if limit <= 0 {
		limit = maxQuotaResponseBytes
	}
	raw, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("upstream response exceeds %d bytes", limit)
	}
	return raw, nil
}

// trimToBytes cuts value to at most limit bytes without splitting a UTF-8 rune,
// so truncated Chinese error text never becomes invalid UTF-8.
func trimToBytes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func sanitizeMessage(message string) string {
	message = strings.Join(strings.Fields(strings.TrimSpace(message)), " ")
	return trimToBytes(message, 200)
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	return trimToBytes(value, limit)
}

// forwardHeaders converts upstream response headers into the host contract,
// dropping hop-by-hop headers.
func forwardHeaders(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for name, values := range headers {
		if _, skip := hopByHopHeaders[strings.ToLower(name)]; skip {
			continue
		}
		out[name] = append([]string(nil), values...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
