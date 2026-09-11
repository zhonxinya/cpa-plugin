package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseUsageResponse(t *testing.T) {
	usage, err := ParseUsageResponse([]byte(capturedUsagePayload))
	if err != nil {
		t.Fatalf("ParseUsageResponse() error = %v", err)
	}
	if usage.Level != "OpenCode Go" {
		t.Errorf("Level = %q, want OpenCode Go", usage.Level)
	}
	if len(usage.Windows) != 3 {
		t.Fatalf("Windows = %d, want 3", len(usage.Windows))
	}
	if usage.Rolling.Name != "five_hour" || usage.Rolling.UsedPercent != 2 {
		t.Errorf("Rolling = %#v", usage.Rolling)
	}
	if usage.Rolling.RemainingPercent != 98 {
		t.Errorf("Rolling remaining = %v, want 98", usage.Rolling.RemainingPercent)
	}
	if usage.Rolling.ResetAt == nil || !usage.Rolling.ResetAt.Equal(time.Date(2026, 9, 11, 8, 11, 15, 128000000, time.UTC)) {
		t.Errorf("Rolling reset = %v", usage.Rolling.ResetAt)
	}
	if usage.Weekly.UsedPercent != 6 || usage.Monthly.UsedPercent != 3 {
		t.Errorf("weekly=%v monthly=%v", usage.Weekly.UsedPercent, usage.Monthly.UsedPercent)
	}
}

func TestParseUsageResponseClampsPercent(t *testing.T) {
	body := `{"usage":{"rolling":{"status":"rate-limited","percent":140},"weekly":{"status":"","percent":-5}}}`
	usage, err := ParseUsageResponse([]byte(body))
	if err != nil {
		t.Fatalf("ParseUsageResponse() error = %v", err)
	}
	if usage.Rolling.UsedPercent != 100 || usage.Rolling.RemainingPercent != 0 {
		t.Errorf("rolling over-limit = %#v", usage.Rolling)
	}
	if usage.Rolling.Status != "rate-limited" {
		t.Errorf("rolling status = %q", usage.Rolling.Status)
	}
	if usage.Weekly.UsedPercent != 0 || usage.Weekly.Status != "ok" {
		t.Errorf("weekly clamped = %#v", usage.Weekly)
	}
	if len(usage.Windows) != 2 {
		t.Errorf("Windows = %d, want 2", len(usage.Windows))
	}
}

func TestParseUsageResponseRejectsEmptyAndInvalid(t *testing.T) {
	if _, err := ParseUsageResponse([]byte(`{"usage":{}}`)); err == nil {
		t.Error("ParseUsageResponse() accepted a payload without windows")
	}
	if _, err := ParseUsageResponse([]byte(`not-json`)); err == nil {
		t.Error("ParseUsageResponse() accepted invalid JSON")
	}
}

func TestParseModelsResponseDeduplicatesInvalidIDs(t *testing.T) {
	body := `{"data":[{"id":"glm-5.3"},{"id":"glm-5.3"},{"id":"  "},{"id":"bad id"},{"id":"minimax-m3"}]}`
	models, err := ParseModelsResponse([]byte(body))
	if err != nil {
		t.Fatalf("ParseModelsResponse() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %#v, want 2 unique valid ids", models)
	}
	if models[0].ID != "glm-5.3" || models[1].ID != "minimax-m3" {
		t.Errorf("models = %#v", models)
	}
}

func TestClientFetchUsageSuccess(t *testing.T) {
	var gotAuth, gotPath string
	client := NewClient(func(_ context.Context, req *http.Request) (*http.Response, error) {
		gotAuth = req.Header.Get("Authorization")
		gotPath = req.URL.Path
		return newResponse(http.StatusOK, capturedUsagePayload), nil
	})
	usage, err := client.FetchUsage(context.Background(), AccountConfig{APIKey: "sk-test"}, defaultBaseURL)
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}
	if usage.Rolling.UsedPercent != 2 {
		t.Errorf("usage = %#v", usage)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotPath != "/zen/go/v1/usage" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestClientFetchUsageStatusErrors(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        "401",
		http.StatusForbidden:           "403",
		http.StatusTooManyRequests:     "429",
		http.StatusNotFound:            "404",
		http.StatusInternalServerError: "500",
	}
	for status, want := range cases {
		client := NewClient(fakeHTTPDo(status, `{"error":{"message":"denied"}}`))
		_, err := client.FetchUsage(context.Background(), AccountConfig{APIKey: "sk"}, defaultBaseURL)
		if err == nil {
			t.Fatalf("status %d: expected error", status)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("status %d: error = %q, want contains %q", status, err.Error(), want)
		}
	}
}

func TestClientFetchUsageTransportError(t *testing.T) {
	client := NewClient(func(context.Context, *http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})
	_, err := client.FetchUsage(context.Background(), AccountConfig{APIKey: "sk"}, defaultBaseURL)
	if err == nil || !strings.Contains(err.Error(), "用量请求失败") {
		t.Errorf("error = %v, want request failure message", err)
	}
}

func TestClientFetchModels(t *testing.T) {
	client := NewClient(fakeHTTPDo(http.StatusOK, capturedModelsPayload))
	models, err := client.FetchModels(context.Background(), AccountConfig{APIKey: "sk"}, defaultBaseURL)
	if err != nil {
		t.Fatalf("FetchModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %#v", models)
	}
}

func TestClientHonorsAccountEndpointOverride(t *testing.T) {
	var gotHost string
	client := NewClient(func(_ context.Context, req *http.Request) (*http.Response, error) {
		gotHost = req.URL.Host
		return newResponse(http.StatusOK, capturedUsagePayload), nil
	})
	_, err := client.FetchUsage(context.Background(), AccountConfig{APIKey: "sk", Endpoint: defaultBaseURL}, "")
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}
	if gotHost != "opencode.ai" {
		t.Errorf("host = %q", gotHost)
	}
}

func TestParseResetTime(t *testing.T) {
	if _, ok := parseResetTime(""); ok {
		t.Error("parseResetTime(\"\") reported ok")
	}
	if _, ok := parseResetTime("not-a-time"); ok {
		t.Error("parseResetTime(invalid) reported ok")
	}
	parsed, ok := parseResetTime("2026-09-11T08:11:15Z")
	if !ok || !parsed.Equal(time.Date(2026, 9, 11, 8, 11, 15, 0, time.UTC)) {
		t.Errorf("parseResetTime = %v, %v", parsed, ok)
	}
}

func TestSanitizeAndMask(t *testing.T) {
	if got := sanitizeMessage("  line\n  break  "); got != "line break" {
		t.Errorf("sanitizeMessage = %q", got)
	}
	if got := maskCredential("sk-secret-value"); strings.Contains(got, "secret") {
		t.Errorf("maskCredential leaked the secret: %q", got)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	if got := apiErrorMessage([]byte(`{"error":{"message":"too many"}}`)); got != ": too many" {
		t.Errorf("apiErrorMessage = %q", got)
	}
	if got := apiErrorMessage([]byte("plain")); got != ": plain" {
		t.Errorf("apiErrorMessage = %q", got)
	}
	if got := apiErrorMessage(nil); got != "" {
		t.Errorf("apiErrorMessage(nil) = %q", got)
	}
}
