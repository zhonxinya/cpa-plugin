package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// pluginAccountsFileName is the private file holding panel-managed accounts.
	pluginAccountsFileName = ".opencode-go-accounts"
	// defaultAuthDirEnv lets deployments point the plugin at a shared auth dir.
	defaultAuthDirEnv = "CLIPROXY_AUTH_DIR"
)

// AccountStore persists panel-managed OpenCode Go credentials.
type AccountStore interface {
	List(context.Context) ([]StoredAccount, error)
	Get(context.Context, string) (StoredAccount, error)
	Save(context.Context, string, []byte) (StoredAccount, error)
	Delete(context.Context, string) error
}

// StoredAccount is one persisted credential record.
type StoredAccount struct {
	ID        string
	Label     string
	APIKey    string
	Endpoint  string
	Source    AuthSource
	Disabled  bool
	AuthIndex string
	Path      string
	Raw       json.RawMessage
}

type pluginAccountFile struct {
	Accounts []json.RawMessage `json:"accounts"`
}

type fileAccountStore struct {
	mu  sync.Mutex
	dir string
}

// NewFileAccountStore creates a store rooted at dir, or an auto-discovered directory.
func NewFileAccountStore(dir string) *fileAccountStore {
	return &fileAccountStore{dir: strings.TrimSpace(dir)}
}

func (s *fileAccountStore) List(context.Context) ([]StoredAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, records, err := s.load()
	if err != nil {
		return nil, err
	}
	accounts := make([]StoredAccount, 0, len(records))
	for _, record := range records {
		account, err := storedAccountFromRecord(record)
		if err != nil {
			continue
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func (s *fileAccountStore) Get(_ context.Context, id string) (StoredAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := normalizeAccountID(id)
	_, records, err := s.load()
	if err != nil {
		return StoredAccount{}, err
	}
	for _, record := range records {
		account, err := storedAccountFromRecord(record)
		if err != nil {
			continue
		}
		if account.ID == want {
			return account, nil
		}
	}
	return StoredAccount{}, fmt.Errorf("account %q not found", id)
}

func (s *fileAccountStore) Save(_ context.Context, name string, credential []byte) (StoredAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := normalizeAccountID(name)
	if !accountIDPattern.MatchString(id) {
		return StoredAccount{}, fmt.Errorf("account id is invalid")
	}
	var payload map[string]any
	if err := json.Unmarshal(credential, &payload); err != nil {
		return StoredAccount{}, fmt.Errorf("invalid account payload")
	}
	payload["id"] = id
	normalized, err := json.Marshal(payload)
	if err != nil {
		return StoredAccount{}, err
	}
	path, records, err := s.load()
	if err != nil {
		return StoredAccount{}, err
	}
	next := make([]json.RawMessage, 0, len(records)+1)
	replaced := false
	for _, record := range records {
		existing, err := storedAccountFromRecord(record)
		if err != nil {
			continue
		}
		if existing.ID == id {
			if !replaced {
				next = append(next, json.RawMessage(append([]byte(nil), normalized...)))
				replaced = true
			}
			continue
		}
		next = append(next, record)
	}
	if !replaced {
		next = append(next, json.RawMessage(append([]byte(nil), normalized...)))
	}
	if err := s.write(path, next); err != nil {
		return StoredAccount{}, err
	}
	return storedAccountFromRecord(normalized)
}

func (s *fileAccountStore) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := normalizeAccountID(name)
	if id == "" {
		return fmt.Errorf("account id is required")
	}
	path, records, err := s.load()
	if err != nil {
		return err
	}
	next := make([]json.RawMessage, 0, len(records))
	for _, record := range records {
		existing, err := storedAccountFromRecord(record)
		if err != nil {
			continue
		}
		if existing.ID == id {
			continue
		}
		next = append(next, record)
	}
	return s.write(path, next)
}

func (s *fileAccountStore) load() (string, []json.RawMessage, error) {
	path, err := s.filePath()
	if err != nil {
		return "", nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, nil, nil
		}
		return "", nil, fmt.Errorf("read plugin accounts: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return path, nil, nil
	}
	var file pluginAccountFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return "", nil, fmt.Errorf("decode plugin accounts: %w", err)
	}
	return path, file.Accounts, nil
}

func (s *fileAccountStore) write(path string, records []json.RawMessage) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create plugin account dir: %w", err)
	}
	raw, err := json.Marshal(pluginAccountFile{Accounts: records})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create plugin account temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set plugin account permissions: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write plugin accounts: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close plugin account temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace plugin accounts: %w", err)
	}
	return nil
}

func (s *fileAccountStore) filePath() (string, error) {
	if s == nil {
		return "", fmt.Errorf("account storage unavailable")
	}
	// Callers must hold s.mu; the resolved directory is cached on first use.
	if dir := strings.TrimSpace(s.dir); dir != "" {
		return filepath.Join(dir, pluginAccountsFileName), nil
	}
	dir := discoverAccountDir()
	if dir == "" {
		return "", fmt.Errorf("plugin account directory is unavailable")
	}
	s.dir = dir
	return filepath.Join(dir, pluginAccountsFileName), nil
}

// discoverAccountDir resolves the directory that holds plugin account state.
func discoverAccountDir() string {
	if dir := strings.TrimSpace(os.Getenv(defaultAuthDirEnv)); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return ""
}

func storedAccountFromRecord(raw json.RawMessage) (StoredAccount, error) {
	var record credentialRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return StoredAccount{}, err
	}
	id := firstNonEmpty(record.ID, record.Name)
	if id == "" {
		return StoredAccount{}, fmt.Errorf("account id is required")
	}
	apiKey := firstNonEmpty(record.APIKey, record.AccessToken, record.Token.AccessToken)
	endpoint := ""
	if strings.TrimSpace(record.Endpoint) != "" {
		normalized, err := normalizeEndpoint(record.Endpoint)
		if err != nil {
			return StoredAccount{}, fmt.Errorf("saved endpoint: %w", err)
		}
		endpoint = normalized
	}
	return StoredAccount{
		ID:       id,
		Label:    firstNonEmpty(record.Label, id),
		APIKey:   apiKey,
		Endpoint: endpoint,
		Source:   SourcePanel,
		Disabled: record.Disabled,
		Path:     record.Path,
		Raw:      append(json.RawMessage(nil), raw...),
	}, nil
}

type credentialRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Type        string `json:"type"`
	Plan        string `json:"plan"`
	Label       string `json:"label"`
	APIKey      string `json:"api_key"`
	AccessToken string `json:"access_token"`
	Endpoint    string `json:"endpoint"`
	Path        string `json:"path"`
	Disabled    bool   `json:"disabled"`
	Token       struct {
		AccessToken string `json:"access_token"`
	} `json:"token"`
}

func normalizeAccountID(value string) string {
	return strings.TrimSuffix(strings.TrimSpace(value), ".json")
}

// MakeAccountID derives a stable, URL-safe account id from a label.
func MakeAccountID(label string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(providerKey + "-" + strings.TrimSpace(label)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.':
			builder.WriteRune(r)
		default:
			if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "-") {
				builder.WriteByte('-')
			}
		}
	}
	value := strings.Trim(builder.String(), "-._")
	if value == "" {
		return providerKey + "-account"
	}
	if len(value) > 64 {
		value = strings.Trim(value[:64], "-._")
	}
	if !accountIDPattern.MatchString(value) {
		return providerKey + "-account"
	}
	return value
}
