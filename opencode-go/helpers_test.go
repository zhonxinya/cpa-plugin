package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// capturedUsagePayload is a real response recorded from the OpenCode Go API.
const capturedUsagePayload = `{"usage":{"rolling":{"status":"ok","percent":2,"resetsAt":"2026-09-11T08:11:15.128Z"},"weekly":{"status":"ok","percent":6,"resetsAt":"2026-09-14T00:00:00.128Z"},"monthly":{"status":"ok","percent":3,"resetsAt":"2026-10-10T10:00:50.128Z"}}}`

const capturedModelsPayload = `{"object":"list","data":[{"id":"glm-5.3-flash","object":"model","created":1789105487,"owned_by":"opencode"},{"id":"minimax-m3","object":"model","created":1789105487,"owned_by":"opencode"}]}`

// newResponse builds an HTTP response for a fake transport.
func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// fakeHTTPDo returns a transport that always replies with the same response.
func fakeHTTPDo(status int, body string) HTTPDoFunc {
	return func(context.Context, *http.Request) (*http.Response, error) {
		return newResponse(status, body), nil
	}
}

// decodeOK asserts an OK RPC envelope and unmarshals its result.
func decodeOK(t *testing.T, raw []byte, out any) {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v (%s)", err, raw)
	}
	if !env.OK {
		t.Fatalf("envelope is not ok: %s", raw)
	}
	if out == nil {
		return
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		t.Fatalf("decode envelope result: %v (%s)", err, env.Result)
	}
}

// testDispatcher returns a dispatcher backed by a temp account directory.
func testDispatcher(t *testing.T, httpDo HTTPDoFunc) *Dispatcher {
	t.Helper()
	d := NewTestDispatcher(httpDo)
	d.accounts = NewFileAccountStore(t.TempDir())
	return d
}

func TestTrimToBytesKeepsValidUTF8(t *testing.T) {
	// Each Chinese rune is 3 bytes, so a byte limit inside a rune must back off.
	value := strings.Repeat("测", 100)
	got := trimToBytes(value, 200)
	if len(got) > 200 {
		t.Fatalf("trimToBytes() = %d bytes, want <= 200", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("trimToBytes() split a rune: %q", got)
	}
	if len(got) != 198 {
		t.Errorf("trimToBytes() = %d bytes, want the 198-byte rune boundary", len(got))
	}
	if got := trimToBytes("short", 200); got != "short" {
		t.Errorf("trimToBytes() shortened a value under the limit: %q", got)
	}
	if got := trimToBytes("abc", 0); got != "abc" {
		t.Errorf("trimToBytes() with a zero limit = %q", got)
	}
}

func TestSanitizeMessageKeepsValidUTF8(t *testing.T) {
	got := sanitizeMessage(strings.Repeat("错", 200))
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeMessage() produced invalid UTF-8: %q", got)
	}
	if len(got) > 200 {
		t.Errorf("sanitizeMessage() = %d bytes, want <= 200", len(got))
	}
}

func TestTruncateKeepsValidUTF8(t *testing.T) {
	got := truncate("  "+strings.Repeat("主", 40), 64)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate() produced invalid UTF-8: %q", got)
	}
	if len(got) > 64 {
		t.Errorf("truncate() = %d bytes, want <= 64", len(got))
	}
	if got := truncate("  值  ", 10); got != "值" {
		t.Errorf("truncate() = %q, want trimmed 值", got)
	}
}
