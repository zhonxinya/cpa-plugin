package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	pluginID      = "opencode-go"
	pluginName    = "OpenCode Go"
	pluginVersion = "0.1.0"
	pluginAuthor  = "zhonxinya"
	pluginRepo    = "https://github.com/zhonxinya/cpa-plugin"

	managementStatusPath   = "/plugins/" + pluginID + "/status"
	managementAccountsPath = "/plugins/" + pluginID + "/accounts"
	// CPA passes the full browser resource path to management.handle.
	resourceStatusPath     = "/v0/resource/plugins/" + pluginID + "/status"
	maxAddAccountBodyBytes = 64 << 10
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

// pluginCapabilities mirrors the host RPC capability contract.
type pluginCapabilities struct {
	ModelProvider         bool     `json:"model_provider"`
	AuthProvider          bool     `json:"auth_provider"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats"`
	ExecutorOutputFormats []string `json:"executor_output_formats"`
	ManagementAPI         bool     `json:"management_api"`
}

type registration struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  pluginCapabilities `json:"capabilities"`
}

// Dispatcher routes host RPC methods to plugin behaviour.
type Dispatcher struct {
	mu       sync.Mutex
	config   Config
	client   QuotaClient
	service  *Service
	accounts AccountStore
	httpDo   HTTPDoFunc
	now      func() time.Time

	cacheMu     sync.Mutex
	lastResults []AccountResult
}

// NewDispatcher builds the plugin dispatcher.
func NewDispatcher(client QuotaClient) *Dispatcher {
	if client == nil {
		client = NewClient(nil)
	}
	cfg := Config{Enabled: true, Priority: 1, Timeout: defaultTimeout, Endpoint: defaultBaseURL}
	return &Dispatcher{
		config:   cfg,
		client:   client,
		service:  NewService(cfg, client),
		accounts: NewFileAccountStore(""),
		httpDo:   realHTTPDo,
		now:      time.Now,
	}
}

// NewTestDispatcher builds a dispatcher whose HTTP transport is injected by tests.
func NewTestDispatcher(httpDo HTTPDoFunc) *Dispatcher {
	d := NewDispatcher(NewClient(httpDo))
	d.httpDo = httpDo
	return d
}

// Call dispatches one host RPC method.
func (d *Dispatcher) Call(method string, request []byte) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("dispatcher unavailable")
	}
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if err := d.configure(request); err != nil {
			return nil, err
		}
		return okEnvelope(d.registration()), nil
	case pluginabi.MethodPluginShutdown:
		return okEnvelope(map[string]any{"ok": true}), nil
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration()), nil
	case pluginabi.MethodManagementHandle:
		var req pluginapi.ManagementRequest
		if len(request) != 0 {
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, fmt.Errorf("decode management request: %w", err)
			}
		}
		return okEnvelope(d.handleManagement(req)), nil
	case pluginabi.MethodExecutorIdentifier, pluginabi.MethodAuthIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerKey}), nil
	case pluginabi.MethodExecutorExecute:
		result, err := d.execute(request)
		if err != nil {
			return nil, err
		}
		return okEnvelope(result), nil
	case pluginabi.MethodExecutorCountTokens:
		result, err := d.countTokens(request)
		if err != nil {
			return nil, err
		}
		return okEnvelope(result), nil
	case pluginabi.MethodExecutorHTTPRequest:
		result, err := d.httpRequest(request)
		if err != nil {
			return nil, err
		}
		return okEnvelope(result), nil
	case pluginabi.MethodExecutorExecuteStream:
		result, err := d.executeStream(request)
		if err != nil {
			return nil, err
		}
		return okEnvelope(result), nil
	case pluginabi.MethodModelStatic, pluginabi.MethodModelForAuth:
		return okEnvelope(d.modelResponse(request)), nil
	case pluginabi.MethodAuthParse:
		result, err := d.parseAuth(request)
		if err != nil {
			return nil, err
		}
		return okEnvelope(result), nil
	case pluginabi.MethodAuthRefresh:
		result, err := d.refreshAuth(request)
		if err != nil {
			return nil, err
		}
		return okEnvelope(result), nil
	case pluginabi.MethodAuthLoginStart, pluginabi.MethodAuthLoginPoll:
		return nil, fmt.Errorf("OpenCode Go 使用 API Key 认证，请直接保存 API Key，无需登录流程")
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (d *Dispatcher) configure(request []byte) error {
	var req lifecycleRequest
	if len(request) != 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return fmt.Errorf("decode lifecycle request: %w", err)
		}
	}
	cfg, err := DecodeConfig(req.ConfigYAML, getEnvironment)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.config = cfg
	d.service = NewService(cfg, d.client)
	if d.accounts == nil {
		d.accounts = NewFileAccountStore("")
	}
	service := d.service
	store := d.accounts
	d.mu.Unlock()

	// Warm the caches so the management panel and model callbacks are ready.
	ctx, cancel := contextWithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	accounts := ResolveAccounts(ctx, cfg, store)
	if len(accounts) > 0 {
		service.RefreshCatalog(ctx, accounts)
		d.rememberAll(service.RefreshAll(ctx, accounts))
	}
	return nil
}

func (d *Dispatcher) registration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             pluginName,
			Version:          pluginVersion,
			Author:           pluginAuthor,
			GitHubRepository: pluginRepo,
			ConfigFields: []pluginapi.ConfigField{
				{Name: "accounts", Type: pluginapi.ConfigFieldTypeArray, Description: "OpenCode Go 账号：id、label、api_key_env、可选 endpoint。"},
				{Name: "plans", Type: pluginapi.ConfigFieldTypeArray, Description: "accounts 的旧名称，语义相同。"},
				{Name: "timeout", Type: pluginapi.ConfigFieldTypeString, Description: "用量请求超时，例如 15s。"},
				{Name: "endpoint", Type: pluginapi.ConfigFieldTypeString, Description: "可选的 https://opencode.ai/zen/go/v1 端点覆盖。"},
			},
		},
		Capabilities: pluginCapabilities{
			ModelProvider:         true,
			AuthProvider:          true,
			Executor:              true,
			ExecutorModelScope:    "both",
			ExecutorInputFormats:  []string{"chat-completions", "responses", "anthropic"},
			ExecutorOutputFormats: []string{"chat-completions", "responses", "anthropic"},
			ManagementAPI:         true,
		},
	}
}

func managementRegistration() pluginapi.ManagementRegistrationResponse {
	return pluginapi.ManagementRegistrationResponse{
		Resources: []pluginapi.ResourceRoute{{
			Path: "/status", Menu: pluginName, Description: "OpenCode Go 订阅用量看板。",
		}},
		Routes: []pluginapi.ManagementRoute{
			{Method: http.MethodGet, Path: managementStatusPath, Description: "OpenCode Go usage dashboard."},
			{Method: http.MethodPost, Path: managementAccountsPath, Description: "Validate and save an OpenCode Go account."},
			{Method: http.MethodDelete, Path: managementAccountsPath, Description: "Delete a saved OpenCode Go account."},
		},
	}
}

func (d *Dispatcher) handleManagement(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	path := strings.TrimSpace(req.Path)
	if strings.HasPrefix(path, "/v0/management") {
		path = strings.TrimPrefix(path, "/v0/management")
	}
	switch {
	case path == managementAccountsPath && req.Method == http.MethodPost:
		return d.handleSaveAccount(req)
	case path == managementAccountsPath && req.Method == http.MethodDelete:
		return d.handleDeleteAccount(req)
	}
	if req.Method != http.MethodGet || (path != managementStatusPath && path != resourceStatusPath) {
		return jsonManagementResponse(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	if path == resourceStatusPath {
		// Browser resources are not management-authenticated; never expose account data here.
		return htmlManagementResponse(RenderResourcePanel(nowUTC(d.now)))
	}
	config, service, store := d.snapshot()
	ctx, cancel := contextWithTimeout(context.Background(), config.Timeout)
	defer cancel()
	accounts := ResolveAccounts(ctx, config, store)
	results := d.refresh(ctx, service, accounts, req.Query.Get("refresh"))
	return htmlManagementResponse(RenderPanelView(results, nowUTC(d.now), req.Query.Get("view")))
}

type saveAccountRequest struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Credential string `json:"credential"`
	Endpoint   string `json:"endpoint"`
}

func (d *Dispatcher) handleSaveAccount(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	if len(req.Body) > maxAddAccountBodyBytes {
		return jsonManagementResponse(http.StatusRequestEntityTooLarge, map[string]string{"error": "request body is too large"})
	}
	var input saveAccountRequest
	if err := json.Unmarshal(req.Body, &input); err != nil {
		return jsonManagementResponse(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	label := truncate(input.Label, 64)
	if label == "" {
		return jsonManagementResponse(http.StatusBadRequest, map[string]string{"error": "label is required"})
	}
	apiKey := strings.TrimSpace(input.Credential)
	if apiKey == "" {
		return jsonManagementResponse(http.StatusBadRequest, map[string]string{"error": "credential is required"})
	}
	endpoint := ""
	if strings.TrimSpace(input.Endpoint) != "" {
		normalized, err := normalizeEndpoint(input.Endpoint)
		if err != nil {
			return jsonManagementResponse(http.StatusBadRequest, map[string]string{"error": "endpoint is invalid"})
		}
		endpoint = normalized
	}
	id := normalizeAccountID(input.ID)
	if id == "" {
		id = MakeAccountID(label)
	}
	if !accountIDPattern.MatchString(id) {
		return jsonManagementResponse(http.StatusBadRequest, map[string]string{"error": "account id is invalid"})
	}

	config, service, store := d.snapshot()
	if store == nil {
		return jsonManagementResponse(http.StatusServiceUnavailable, map[string]string{"error": "account storage unavailable"})
	}
	account := AccountConfig{ID: id, Label: label, Plan: "go", Source: SourcePanel, APIKey: apiKey, Endpoint: endpoint}
	// Only reachable accounts may be persisted.
	ctx, cancel := contextWithTimeout(context.Background(), config.Timeout)
	defer cancel()
	result := service.RefreshOne(ctx, account)
	if result.Error != "" {
		return jsonManagementResponse(http.StatusBadRequest, map[string]string{"error": result.Error})
	}
	credential := map[string]any{
		"id":       id,
		"provider": providerKey,
		"type":     providerKey,
		"plan":     "go",
		"label":    label,
		"api_key":  apiKey,
	}
	if endpoint != "" {
		credential["endpoint"] = endpoint
	}
	raw, _ := json.Marshal(credential)
	if _, err := store.Save(ctx, id+".json", raw); err != nil {
		return jsonManagementResponse(http.StatusServiceUnavailable, map[string]string{"error": "account storage unavailable"})
	}
	d.remember(result)
	return jsonManagementResponse(http.StatusCreated, map[string]any{
		"ok":              true,
		"id":              id,
		"label":           label,
		"provider":        providerKey,
		"credential_mask": maskCredential(apiKey),
	})
}

func (d *Dispatcher) handleDeleteAccount(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	id := normalizeAccountID(req.Query.Get("id"))
	if id == "" && len(req.Body) > 0 {
		var input struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(req.Body, &input); err == nil {
			id = normalizeAccountID(input.ID)
		}
	}
	if id == "" || !accountIDPattern.MatchString(id) {
		return jsonManagementResponse(http.StatusBadRequest, map[string]string{"error": "account id is required"})
	}
	_, _, store := d.snapshot()
	if store == nil {
		return jsonManagementResponse(http.StatusServiceUnavailable, map[string]string{"error": "account storage unavailable"})
	}
	ctx, cancel := contextWithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := store.Delete(ctx, id); err != nil {
		return jsonManagementResponse(http.StatusServiceUnavailable, map[string]string{"error": "account delete unavailable"})
	}
	d.forget(id)
	return jsonManagementResponse(http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (d *Dispatcher) snapshot() (Config, *Service, AccountStore) {
	d.mu.Lock()
	defer d.mu.Unlock()
	service := d.service
	if service == nil {
		service = NewService(d.config, d.client)
		d.service = service
	}
	return d.config, service, d.accounts
}

func (d *Dispatcher) refresh(ctx context.Context, service *Service, accounts []AccountConfig, refreshID string) []AccountResult {
	if service == nil {
		_, service, _ = d.snapshot()
	}
	if refreshID != "" {
		if account, ok := FindAccount(accounts, refreshID); ok {
			result := service.RefreshOne(ctx, account)
			d.remember(result)
			d.cacheMu.Lock()
			defer d.cacheMu.Unlock()
			merged := make([]AccountResult, 0, len(accounts))
			for _, account := range accounts {
				if account.ID == result.ID {
					merged = append(merged, result)
					continue
				}
				merged = append(merged, d.cached(account.ID))
			}
			d.lastResults = merged
			return append([]AccountResult(nil), merged...)
		}
	}
	results := service.RefreshAll(ctx, accounts)
	d.rememberAll(results)
	return results
}

func (d *Dispatcher) cached(id string) AccountResult {
	for _, result := range d.lastResults {
		if result.ID == id {
			return result
		}
	}
	return AccountResult{ID: id, Provider: providerKey, Label: id, FetchedAt: nowUTC(d.now)}
}

func (d *Dispatcher) rememberAll(results []AccountResult) {
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()
	d.lastResults = append([]AccountResult(nil), results...)
}

func (d *Dispatcher) remember(result AccountResult) {
	if result.ID == "" {
		return
	}
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()
	for index, existing := range d.lastResults {
		if existing.ID == result.ID {
			d.lastResults[index] = result
			return
		}
	}
	d.lastResults = append(d.lastResults, result)
}

func (d *Dispatcher) forget(id string) {
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()
	kept := d.lastResults[:0]
	for _, result := range d.lastResults {
		if result.ID == id {
			continue
		}
		kept = append(kept, result)
	}
	d.lastResults = kept
}
