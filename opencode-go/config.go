package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// defaultBaseURL is the documented OpenCode Go API prefix.
	defaultBaseURL = "https://opencode.ai/zen/go/v1"
	// defaultTimeout bounds a single upstream quota request.
	defaultTimeout = 15 * time.Second
	// allowedEndpointHost is the only host endpoint overrides may target.
	allowedEndpointHost = "opencode.ai"
)

var accountIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// AuthSource describes where an account credential came from.
type AuthSource string

const (
	// SourceConfig is an account declared in the plugin configuration file.
	SourceConfig AuthSource = "config"
	// SourcePanel is an account saved through the management panel.
	SourcePanel AuthSource = "panel"
	// SourceCPA is a credential already stored in CPA and reused here.
	SourceCPA AuthSource = "cpa"
)

// AccountConfig is one normalized OpenCode Go credential.
type AccountConfig struct {
	ID        string
	Label     string
	Plan      string
	Source    AuthSource
	APIKey    string
	APIKeyEnv string
	Endpoint  string
	Disabled  bool
	AuthIndex string
	Raw       json.RawMessage
}

// EndpointOrDefault resolves the per-account base URL.
func (a AccountConfig) EndpointOrDefault(fallback string) string {
	if strings.TrimSpace(a.Endpoint) != "" {
		return a.Endpoint
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return defaultBaseURL
}

// Config is the decoded plugin configuration.
type Config struct {
	Enabled  bool
	Priority int
	Timeout  time.Duration
	Endpoint string
	Accounts []AccountConfig
}

type rawAccount struct {
	ID        string `yaml:"id"`
	Label     string `yaml:"label"`
	Plan      string `yaml:"plan"`
	APIKeyEnv string `yaml:"api_key_env"`
	Endpoint  string `yaml:"endpoint"`
	Disabled  bool   `yaml:"disabled"`
}

type rawConfig struct {
	Enabled  *bool        `yaml:"enabled"`
	Priority *int         `yaml:"priority"`
	Timeout  string       `yaml:"timeout"`
	Endpoint string       `yaml:"endpoint"`
	Accounts []rawAccount `yaml:"accounts"`
	Plans    []rawAccount `yaml:"plans"`
}

// DecodeConfig parses the plugin configuration subtree. getenv resolves
// credential environment variables and may be nil in tests.
func DecodeConfig(raw []byte, getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	decoded := rawConfig{Enabled: new(bool), Priority: new(int), Timeout: "15s", Endpoint: defaultBaseURL}
	*decoded.Enabled = true
	*decoded.Priority = 1
	if len(raw) != 0 {
		if err := yaml.Unmarshal(raw, &decoded); err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
	}

	cfg := Config{
		Enabled:  *decoded.Enabled,
		Priority: *decoded.Priority,
		Timeout:  defaultTimeout,
		Endpoint: strings.TrimSpace(decoded.Endpoint),
	}
	if decoded.Timeout != "" {
		timeout, err := time.ParseDuration(decoded.Timeout)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("timeout must be a positive duration")
		}
		cfg.Timeout = timeout
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultBaseURL
	}
	normalized, err := normalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return Config{}, fmt.Errorf("endpoint: %w", err)
	}
	cfg.Endpoint = normalized

	type entry struct {
		source string
		index  int
		value  rawAccount
	}
	entries := make([]entry, 0, len(decoded.Accounts)+len(decoded.Plans))
	for index, account := range decoded.Accounts {
		entries = append(entries, entry{source: "accounts", index: index, value: account})
	}
	for index, plan := range decoded.Plans {
		entries = append(entries, entry{source: "plans", index: index, value: plan})
	}

	seen := make(map[string]struct{}, len(entries))
	for _, item := range entries {
		index, account := item.index, item.value
		id := strings.TrimSpace(account.ID)
		if id == "" {
			return Config{}, fmt.Errorf("%s[%d].id is required", item.source, index)
		}
		if !accountIDPattern.MatchString(id) {
			return Config{}, fmt.Errorf("%s[%d].id must contain only ASCII letters, digits, dot, underscore, or dash", item.source, index)
		}
		if _, exists := seen[id]; exists {
			return Config{}, fmt.Errorf("duplicate account id %q", id)
		}
		seen[id] = struct{}{}

		apiKeyEnv := strings.TrimSpace(account.APIKeyEnv)
		if apiKeyEnv == "" {
			return Config{}, fmt.Errorf("%s[%d].api_key_env is required", item.source, index)
		}
		apiKey := strings.TrimSpace(getenv(apiKeyEnv))
		if apiKey == "" {
			return Config{}, fmt.Errorf("environment variable %q is empty", apiKeyEnv)
		}
		label := strings.TrimSpace(account.Label)
		if label == "" {
			label = id
		}
		endpoint := ""
		if strings.TrimSpace(account.Endpoint) != "" {
			endpoint, err = normalizeEndpoint(account.Endpoint)
			if err != nil {
				return Config{}, fmt.Errorf("%s[%d].endpoint: %w", item.source, index, err)
			}
		}
		cfg.Accounts = append(cfg.Accounts, AccountConfig{
			ID:        id,
			Label:     label,
			Plan:      firstNonEmpty(account.Plan, "go"),
			Source:    SourceConfig,
			APIKey:    apiKey,
			APIKeyEnv: apiKeyEnv,
			Endpoint:  endpoint,
			Disabled:  account.Disabled,
		})
	}
	return cfg, nil
}

// normalizeEndpoint validates an OpenCode Go base URL and strips a trailing slash.
func normalizeEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("must not be empty")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("must be a valid URL")
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("must use HTTPS")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("must not embed credentials")
	}
	if parsed.Host != strings.ToLower(parsed.Host) {
		return "", fmt.Errorf("host must be lowercase")
	}
	if parsed.Hostname() != allowedEndpointHost {
		return "", fmt.Errorf("host must be %s", allowedEndpointHost)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("must not contain a query or fragment")
	}
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if path != "/zen/go/v1" {
		return "", fmt.Errorf("path must be /zen/go/v1")
	}
	return "https://" + allowedEndpointHost + path, nil
}
