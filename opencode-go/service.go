package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sentinel errors shared by credential conversion paths.
var (
	errUnsupportedProvider = fmt.Errorf("credential provider is not opencode-go")
	errMissingCredential   = fmt.Errorf("credential has no OpenCode Go API key")
)

// maxConcurrentQuotaRequests bounds simultaneous upstream quota calls.
const maxConcurrentQuotaRequests = 4

// AccountResult is the rendered state of one OpenCode Go account.
type AccountResult struct {
	ID             string       `json:"id"`
	Provider       string       `json:"provider"`
	Plan           string       `json:"plan,omitempty"`
	Label          string       `json:"label"`
	Source         AuthSource   `json:"source,omitempty"`
	CredentialMask string       `json:"credential_mask,omitempty"`
	Endpoint       string       `json:"-"`
	Usage          *UsageResult `json:"usage,omitempty"`
	Error          string       `json:"error,omitempty"`
	FetchedAt      time.Time    `json:"fetched_at"`
}

// Service refreshes account usage and owns the cached model catalog.
type Service struct {
	cfg     Config
	client  QuotaClient
	catalog *catalog
	now     func() time.Time
}

// NewService builds a refresh service for the given configuration.
func NewService(cfg Config, client QuotaClient) *Service {
	if client == nil {
		client = NewClient(nil)
	}
	return &Service{cfg: cfg, client: client, catalog: &catalog{}, now: time.Now}
}

// Catalog exposes the model catalog for provider model callbacks.
func (s *Service) Catalog() *catalog {
	if s == nil {
		return nil
	}
	return s.catalog
}

func (s *Service) timestamp() time.Time {
	if s == nil || s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

// RefreshOne fetches usage for a single account.
func (s *Service) RefreshOne(ctx context.Context, account AccountConfig) AccountResult {
	result := AccountResult{
		ID:             account.ID,
		Provider:       providerKey,
		Plan:           firstNonEmpty(account.Plan, "go"),
		Label:          firstNonEmpty(account.Label, account.ID),
		Source:         account.Source,
		CredentialMask: maskCredential(account.APIKey),
		Endpoint:       account.Endpoint,
		FetchedAt:      s.timestamp(),
	}
	if account.Disabled {
		result.Error = "账号已禁用，请在配置或面板中重新启用"
		return result
	}
	if strings.TrimSpace(account.APIKey) == "" {
		result.Error = "缺少 API Key，请在配置或面板中补充"
		return result
	}
	requestCtx, cancel := contextWithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	usage, err := s.client.FetchUsage(requestCtx, account, s.cfg.Endpoint)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Usage = usage
	return result
}

// RefreshAll fetches usage for every account with bounded concurrency.
func (s *Service) RefreshAll(ctx context.Context, accounts []AccountConfig) []AccountResult {
	results := make([]AccountResult, len(accounts))
	slots := make(chan struct{}, maxConcurrentQuotaRequests)
	var wait sync.WaitGroup
	for index, account := range accounts {
		wait.Add(1)
		go func(index int, account AccountConfig) {
			defer wait.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[index] = s.RefreshOne(ctx, account)
		}(index, account)
	}
	wait.Wait()
	return results
}

// RefreshCatalog refreshes the cached model catalog using the first usable account.
func (s *Service) RefreshCatalog(ctx context.Context, accounts []AccountConfig) []ModelEntry {
	if s == nil || s.client == nil {
		return nil
	}
	for _, account := range accounts {
		if account.Disabled || strings.TrimSpace(account.APIKey) == "" {
			continue
		}
		requestCtx, cancel := contextWithTimeout(ctx, s.cfg.Timeout)
		models, err := s.client.FetchModels(requestCtx, account, s.cfg.Endpoint)
		cancel()
		if err != nil || len(models) == 0 {
			continue
		}
		s.catalog.Store(ModelInfosFromEntries(models))
		return models
	}
	return nil
}

// ResolveAccounts merges configured accounts with panel-saved accounts.
func ResolveAccounts(ctx context.Context, cfg Config, store AccountStore) []AccountConfig {
	accounts := make([]AccountConfig, 0, len(cfg.Accounts))
	seen := make(map[string]struct{}, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		if account.ID == "" {
			continue
		}
		seen[account.ID] = struct{}{}
		accounts = append(accounts, account)
	}
	if store != nil {
		entries, err := store.List(ctx)
		if err == nil {
			for _, entry := range entries {
				if entry.ID == "" {
					continue
				}
				if _, exists := seen[entry.ID]; exists {
					continue
				}
				seen[entry.ID] = struct{}{}
				account, err := accountFromStored(entry)
				if err != nil {
					continue
				}
				accounts = append(accounts, account)
			}
		}
	}
	sort.SliceStable(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	return accounts
}

func accountFromStored(entry StoredAccount) (AccountConfig, error) {
	return AccountConfig{
		ID:       entry.ID,
		Label:    firstNonEmpty(entry.Label, entry.ID),
		Plan:     "go",
		Source:   SourcePanel,
		APIKey:   entry.APIKey,
		Endpoint: entry.Endpoint,
		Disabled: entry.Disabled,
	}, nil
}

// FindAccount returns the account matching id, if any.
func FindAccount(accounts []AccountConfig, id string) (AccountConfig, bool) {
	want := normalizeAccountID(id)
	for _, account := range accounts {
		if account.ID == want {
			return account, true
		}
	}
	return AccountConfig{}, false
}

// UsableAccount returns the first enabled account with a credential.
func UsableAccount(accounts []AccountConfig) (AccountConfig, bool) {
	for _, account := range accounts {
		if !account.Disabled && strings.TrimSpace(account.APIKey) != "" {
			return account, true
		}
	}
	return AccountConfig{}, false
}

// AccountFromAuthData converts a host credential blob into an account config.
func AccountFromAuthData(provider string, raw []byte, authID string) (AccountConfig, error) {
	if strings.TrimSpace(provider) != "" && !strings.EqualFold(strings.TrimSpace(provider), providerKey) {
		return AccountConfig{}, errUnsupportedProvider
	}
	var record credentialRecord
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &record); err != nil {
			return AccountConfig{}, fmt.Errorf("decode credential: %w", err)
		}
	}
	apiKey := firstNonEmpty(record.APIKey, record.AccessToken, record.Token.AccessToken)
	if strings.TrimSpace(apiKey) == "" {
		return AccountConfig{}, errMissingCredential
	}
	endpoint := ""
	if strings.TrimSpace(record.Endpoint) != "" {
		normalized, err := normalizeEndpoint(record.Endpoint)
		if err != nil {
			return AccountConfig{}, err
		}
		endpoint = normalized
	}
	return AccountConfig{
		ID:        firstNonEmpty(record.ID, normalizeAccountID(authID)),
		Label:     firstNonEmpty(record.Label, record.ID, normalizeAccountID(authID)),
		Plan:      firstNonEmpty(record.Plan, "go"),
		Source:    SourceCPA,
		APIKey:    apiKey,
		Endpoint:  endpoint,
		AuthIndex: authID,
	}, nil
}
