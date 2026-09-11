package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeQuotaClient is a deterministic QuotaClient for service tests.
type fakeQuotaClient struct {
	usage  *UsageResult
	models []ModelEntry
	err    error
	calls  atomic.Int64
}

func (f *fakeQuotaClient) UsageCalls() int { return int(f.calls.Load()) }

func (f *fakeQuotaClient) FetchUsage(context.Context, AccountConfig, string) (*UsageResult, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	if f.usage == nil {
		f.usage = &UsageResult{Level: "OpenCode Go"}
	}
	return f.usage, nil
}

func (f *fakeQuotaClient) FetchModels(context.Context, AccountConfig, string) ([]ModelEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.models, nil
}

// memoryAccountStore is an in-memory AccountStore for resolver tests.
type memoryAccountStore struct {
	accounts []StoredAccount
}

func (m *memoryAccountStore) List(context.Context) ([]StoredAccount, error) {
	return append([]StoredAccount(nil), m.accounts...), nil
}

func (m *memoryAccountStore) Get(_ context.Context, id string) (StoredAccount, error) {
	for _, account := range m.accounts {
		if account.ID == normalizeAccountID(id) {
			return account, nil
		}
	}
	return StoredAccount{}, errors.New("not found")
}

func (m *memoryAccountStore) Save(context.Context, string, []byte) (StoredAccount, error) {
	return StoredAccount{}, nil
}

func (m *memoryAccountStore) Delete(context.Context, string) error { return nil }

func TestNewServiceDefaults(t *testing.T) {
	service := NewService(Config{Timeout: time.Second}, nil)
	if service == nil || service.client == nil {
		t.Fatal("NewService() returned a service without a client")
	}
	if service.Catalog() == nil {
		t.Fatal("Catalog() = nil")
	}
}

func TestRefreshOneRejectsMissingCredentials(t *testing.T) {
	service := NewService(Config{Timeout: time.Second, Endpoint: defaultBaseURL}, &fakeQuotaClient{})

	disabled := service.RefreshOne(context.Background(), AccountConfig{ID: "a", Disabled: true})
	if disabled.Error == "" {
		t.Error("disabled account produced no error")
	}

	empty := service.RefreshOne(context.Background(), AccountConfig{ID: "b"})
	if empty.Error == "" {
		t.Error("account without a key produced no error")
	}
}

func TestRefreshOneSuccess(t *testing.T) {
	client := &fakeQuotaClient{usage: &UsageResult{Level: "OpenCode Go", Rolling: UsageWindow{Name: "five_hour", RemainingPercent: 90}}}
	service := NewService(Config{Timeout: time.Second, Endpoint: defaultBaseURL}, client)
	result := service.RefreshOne(context.Background(), AccountConfig{ID: "main", Label: "主账号", APIKey: "sk"})
	if result.Error != "" {
		t.Fatalf("RefreshOne() error = %q", result.Error)
	}
	if result.Provider != providerKey || result.Plan != "go" {
		t.Errorf("result = %#v", result)
	}
	if result.Usage == nil || result.Usage.Rolling.RemainingPercent != 90 {
		t.Errorf("usage = %#v", result.Usage)
	}
	if result.CredentialMask == "" || result.CredentialMask == "sk" {
		t.Errorf("credential mask leaked: %q", result.CredentialMask)
	}
	if result.FetchedAt.IsZero() {
		t.Error("FetchedAt is zero")
	}
}

func TestRefreshOneSurfacesClientError(t *testing.T) {
	client := &fakeQuotaClient{err: errors.New("upstream down")}
	service := NewService(Config{Timeout: time.Second, Endpoint: defaultBaseURL}, client)
	result := service.RefreshOne(context.Background(), AccountConfig{ID: "main", APIKey: "sk"})
	if result.Error != "upstream down" {
		t.Errorf("Error = %q, want upstream down", result.Error)
	}
}

func TestRefreshAllCoversEveryAccount(t *testing.T) {
	client := &fakeQuotaClient{usage: &UsageResult{Level: "OpenCode Go"}}
	service := NewService(Config{Timeout: time.Second, Endpoint: defaultBaseURL}, client)
	accounts := []AccountConfig{{ID: "a", APIKey: "sk"}, {ID: "b", APIKey: "sk"}, {ID: "c", APIKey: "sk"}}
	results := service.RefreshAll(context.Background(), accounts)
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for index, result := range results {
		if result.ID != accounts[index].ID {
			t.Errorf("results[%d].ID = %q, want %q", index, result.ID, accounts[index].ID)
		}
	}
	if got := client.UsageCalls(); got != 3 {
		t.Errorf("client calls = %d, want 3", got)
	}
}

func TestRefreshCatalogStoresFetchedModels(t *testing.T) {
	client := &fakeQuotaClient{models: []ModelEntry{{ID: "glm-5.3", OwnedBy: "opencode"}, {ID: "kimi-k3"}}}
	service := NewService(Config{Timeout: time.Second, Endpoint: defaultBaseURL}, client)
	models := service.RefreshCatalog(context.Background(), []AccountConfig{{ID: "a", APIKey: "sk"}})
	if len(models) != 2 {
		t.Fatalf("RefreshCatalog() = %#v", models)
	}
	if !service.Catalog().Fresh() {
		t.Error("catalog is not fresh after refresh")
	}
	if got := service.Catalog().ModelIDs(); len(got) != 2 {
		t.Errorf("catalog ids = %#v", got)
	}
}

func TestRefreshCatalogSkipsUnusableAccounts(t *testing.T) {
	client := &fakeQuotaClient{models: []ModelEntry{{ID: "glm-5.3"}}}
	service := NewService(Config{Timeout: time.Second, Endpoint: defaultBaseURL}, client)
	if models := service.RefreshCatalog(context.Background(), []AccountConfig{{ID: "a", Disabled: true}, {ID: "b"}}); models != nil {
		t.Errorf("RefreshCatalog() = %#v, want nil", models)
	}
}

func TestCatalogFallsBackToDocumentedModels(t *testing.T) {
	var empty catalog
	if got := len(empty.Models()); got != len(documentedModelIDs) {
		t.Errorf("fallback models = %d, want %d", got, len(documentedModelIDs))
	}
	if empty.Fresh() {
		t.Error("empty catalog reported fresh")
	}
}

func TestResolveAccountsMergesAndSorts(t *testing.T) {
	cfg := Config{Accounts: []AccountConfig{{ID: "zeta", APIKey: "sk-config", Source: SourceConfig}}}
	store := &memoryAccountStore{accounts: []StoredAccount{
		{ID: "alpha", Label: "Alpha", APIKey: "sk-panel", Source: SourcePanel},
		{ID: "zeta", APIKey: "sk-duplicate"},
	}}
	accounts := ResolveAccounts(context.Background(), cfg, store)
	if len(accounts) != 2 {
		t.Fatalf("accounts = %#v, want 2", accounts)
	}
	if accounts[0].ID != "alpha" || accounts[1].ID != "zeta" {
		t.Errorf("accounts not sorted by id: %#v", accounts)
	}
	if accounts[1].Source != SourceConfig {
		t.Errorf("duplicate id should keep the configured account: %#v", accounts[1])
	}
}

func TestFindAndUsableAccount(t *testing.T) {
	accounts := []AccountConfig{{ID: "alpha.json", Disabled: true, APIKey: "sk"}, {ID: "beta", APIKey: "sk"}}
	if _, ok := FindAccount(accounts, "beta.json"); !ok {
		t.Error("FindAccount() missed a normalized match")
	}
	if account, ok := UsableAccount(accounts); !ok || account.ID != "beta" {
		t.Errorf("UsableAccount() = %#v, %v", account, ok)
	}
	if account, ok := FindAccount(accounts, "missing"); ok {
		t.Errorf("FindAccount(missing) = %#v", account)
	}
}

func TestAccountFromAuthData(t *testing.T) {
	account, err := AccountFromAuthData(providerKey, []byte(`{"id":"main","label":"主账号","api_key":"sk-auth"}`), "main")
	if err != nil {
		t.Fatalf("AccountFromAuthData() error = %v", err)
	}
	if account.Source != SourceCPA || account.APIKey != "sk-auth" || account.Plan != "go" {
		t.Errorf("account = %#v", account)
	}

	if _, err := AccountFromAuthData("zhipu", []byte(`{"api_key":"sk"}`), "x"); !errors.Is(err, errUnsupportedProvider) {
		t.Errorf("unsupported provider error = %v", err)
	}
	if _, err := AccountFromAuthData(providerKey, []byte(`{"id":"x"}`), "x"); !errors.Is(err, errMissingCredential) {
		t.Errorf("missing credential error = %v", err)
	}
}
