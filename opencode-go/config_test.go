package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDecodeConfigDefaults(t *testing.T) {
	cfg, err := DecodeConfig(nil, nil)
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if !cfg.Enabled {
		t.Error("Enabled = false, want true")
	}
	if cfg.Timeout != defaultTimeout {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, defaultTimeout)
	}
	if cfg.Endpoint != defaultBaseURL {
		t.Errorf("Endpoint = %q, want %q", cfg.Endpoint, defaultBaseURL)
	}
	if len(cfg.Accounts) != 0 {
		t.Errorf("Accounts = %d, want 0", len(cfg.Accounts))
	}
}

func TestDecodeConfigResolvesCredentialEnv(t *testing.T) {
	raw := []byte("timeout: 20s\naccounts:\n  - id: main\n    label: 主账号\n    api_key_env: OC_GO_KEY\n")
	cfg, err := DecodeConfig(raw, func(name string) string {
		if name == "OC_GO_KEY" {
			return "  sk-secret  "
		}
		return ""
	})
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if cfg.Timeout != 20*time.Second {
		t.Errorf("Timeout = %v, want 20s", cfg.Timeout)
	}
	if len(cfg.Accounts) != 1 {
		t.Fatalf("Accounts = %d, want 1", len(cfg.Accounts))
	}
	account := cfg.Accounts[0]
	if account.APIKey != "sk-secret" {
		t.Errorf("APIKey = %q, want trimmed sk-secret", account.APIKey)
	}
	if account.Label != "主账号" {
		t.Errorf("Label = %q, want 主账号", account.Label)
	}
	if account.Source != SourceConfig {
		t.Errorf("Source = %q, want %q", account.Source, SourceConfig)
	}
	if account.Plan != "go" {
		t.Errorf("Plan = %q, want go", account.Plan)
	}
}

func TestDecodeConfigLegacyPlansAlias(t *testing.T) {
	raw := []byte("plans:\n  - id: legacy\n    api_key_env: OC_GO_KEY\n")
	cfg, err := DecodeConfig(raw, func(string) string { return "sk-legacy" })
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].ID != "legacy" {
		t.Fatalf("Accounts = %#v, want legacy entry", cfg.Accounts)
	}
	if cfg.Accounts[0].Label != "legacy" {
		t.Errorf("Label = %q, want id fallback", cfg.Accounts[0].Label)
	}
}

func TestDecodeConfigRejectsInvalidInput(t *testing.T) {
	cases := map[string]struct {
		raw    string
		getenv func(string) string
		want   string
	}{
		"missing id":            {raw: "accounts:\n  - api_key_env: K\n", want: "id is required"},
		"invalid id":            {raw: "accounts:\n  - id: 'bad id'\n    api_key_env: K\n", want: "only ASCII letters"},
		"duplicate id":          {raw: "accounts:\n  - id: a\n    api_key_env: K\n  - id: a\n    api_key_env: K\n", want: "duplicate"},
		"missing env name":      {raw: "accounts:\n  - id: a\n", want: "api_key_env is required"},
		"empty env value":       {raw: "accounts:\n  - id: a\n    api_key_env: K\n", getenv: func(string) string { return "" }, want: "is empty"},
		"invalid timeout":       {raw: "timeout: nope\n", want: "positive duration"},
		"negative timeout":      {raw: "timeout: -1s\n", want: "positive duration"},
		"insecure endpoint":     {raw: "endpoint: http://opencode.ai/zen/go/v1\n", want: "must use HTTPS"},
		"foreign endpoint host": {raw: "endpoint: https://evil.example/zen/go/v1\n", want: "host must be opencode.ai"},
		"wrong endpoint path":   {raw: "endpoint: https://opencode.ai/zen/v1\n", want: "path must be /zen/go/v1"},
		"uppercase host":        {raw: "endpoint: https://OpenCode.ai/zen/go/v1\n", want: "lowercase"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			getenv := testCase.getenv
			if getenv == nil {
				getenv = func(string) string { return "sk-x" }
			}
			_, err := DecodeConfig([]byte(testCase.raw), getenv)
			if err == nil {
				t.Fatalf("DecodeConfig() error = nil, want %q", testCase.want)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("DecodeConfig() error = %q, want contains %q", err.Error(), testCase.want)
			}
		})
	}
}

func TestDecodeConfigAcceptsEndpointOverride(t *testing.T) {
	raw := []byte("endpoint: https://opencode.ai/zen/go/v1/\naccounts:\n  - id: a\n    api_key_env: K\n    endpoint: https://opencode.ai/zen/go/v1\n")
	cfg, err := DecodeConfig(raw, func(string) string { return "sk-x" })
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if cfg.Endpoint != defaultBaseURL {
		t.Errorf("Endpoint = %q, want %q", cfg.Endpoint, defaultBaseURL)
	}
	if cfg.Accounts[0].Endpoint != defaultBaseURL {
		t.Errorf("account Endpoint = %q, want %q", cfg.Accounts[0].Endpoint, defaultBaseURL)
	}
}

func TestNormalizeEndpointRejectsCredentialsAndQuery(t *testing.T) {
	if _, err := normalizeEndpoint("https://user:pass@opencode.ai/zen/go/v1"); err == nil {
		t.Error("normalizeEndpoint() accepted embedded credentials")
	}
	if _, err := normalizeEndpoint("https://opencode.ai/zen/go/v1?x=1"); err == nil {
		t.Error("normalizeEndpoint() accepted a query string")
	}
}

func TestAccountEndpointOrDefault(t *testing.T) {
	if got := (AccountConfig{}).EndpointOrDefault(""); got != defaultBaseURL {
		t.Errorf("EndpointOrDefault() = %q, want default", got)
	}
	if got := (AccountConfig{}).EndpointOrDefault("https://opencode.ai/zen/go/v1"); got != defaultBaseURL {
		t.Errorf("EndpointOrDefault() = %q, want fallback", got)
	}
	if got := (AccountConfig{Endpoint: "https://opencode.ai/zen/go/v1"}).EndpointOrDefault(""); got != defaultBaseURL {
		t.Errorf("EndpointOrDefault() = %q, want account endpoint", got)
	}
}

func TestUpstreamPathForFormat(t *testing.T) {
	cases := map[string]string{
		"chat-completions": "/chat/completions",
		"openai":           "/chat/completions",
		"":                 "/chat/completions",
		"responses":        "/responses",
		"openai-response":  "/responses",
		"anthropic":        "/messages",
		"claude":           "/messages",
	}
	for format, want := range cases {
		if got := upstreamPathForFormat(format); got != want {
			t.Errorf("upstreamPathForFormat(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestMaskCredentialNeverRevealsValue(t *testing.T) {
	if got := maskCredential("sk-supersecret"); strings.Contains(got, "supersecret") || strings.Contains(got, "sk-") {
		t.Errorf("maskCredential() = %q, leaks the secret", got)
	}
}

func TestUsageStatusErrorMessages(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        "401",
		http.StatusForbidden:           "403",
		http.StatusTooManyRequests:     "429",
		http.StatusNotFound:            "404",
		http.StatusInternalServerError: "500",
	}
	for status, want := range cases {
		err := usageStatusError(status, []byte(`{"type":"error","error":{"type":"AuthError","message":"Unauthorized"}}`))
		if err == nil {
			t.Fatalf("usageStatusError(%d) = nil", status)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("usageStatusError(%d) = %q, want contains %q", status, err.Error(), want)
		}
		if strings.Contains(err.Error(), "sk-") {
			t.Errorf("usageStatusError(%d) leaked a credential: %q", status, err.Error())
		}
	}
}
