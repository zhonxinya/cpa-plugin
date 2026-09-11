package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	// streamChunkSize is the size of each chunk forwarded to the streaming bridge.
	streamChunkSize = 8 << 10
	// maxExecutorResponseBytes bounds buffered non-streaming executor replies.
	maxExecutorResponseBytes = 16 << 20
	// inferenceTimeout caps a single upstream inference call.
	inferenceTimeout = 30 * time.Minute
	// sessionHeader carries a stable conversation id for upstream routing.
	sessionHeader = "x-opencode-session"
)

// executorRequest mirrors the host executor request JSON contract.
type executorRequest struct {
	AuthID          string              `json:"AuthID"`
	AuthProvider    string              `json:"AuthProvider"`
	Model           string              `json:"Model"`
	Format          string              `json:"Format"`
	Stream          bool                `json:"Stream"`
	Alt             string              `json:"Alt"`
	Headers         map[string][]string `json:"Headers"`
	Query           map[string][]string `json:"Query"`
	OriginalRequest []byte              `json:"OriginalRequest"`
	SourceFormat    string              `json:"SourceFormat"`
	Payload         []byte              `json:"Payload"`
	Metadata        map[string]any      `json:"Metadata"`
	StorageJSON     []byte              `json:"StorageJSON"`
	AuthMetadata    map[string]any      `json:"AuthMetadata"`
	AuthAttributes  map[string]string   `json:"AuthAttributes"`
	StreamID        string              `json:"stream_id,omitempty"`
	HostCallbackID  string              `json:"host_callback_id,omitempty"`
}

// executorResponse mirrors the host non-streaming executor response contract.
type executorResponse struct {
	Payload  []byte              `json:"Payload,omitempty"`
	Headers  map[string][]string `json:"Headers,omitempty"`
	Metadata map[string]any      `json:"Metadata,omitempty"`
}

// executorStreamResponse mirrors the host streaming executor response contract.
type executorStreamResponse struct {
	Headers map[string][]string   `json:"headers,omitempty"`
	Chunks  []executorStreamChunk `json:"chunks,omitempty"`
}

type executorStreamChunk struct {
	Payload []byte `json:"Payload,omitempty"`
	Err     string `json:"Err,omitempty"`
}

// executorHTTPRequest mirrors the host executor HTTP request contract.
type executorHTTPRequest struct {
	AuthID       string              `json:"AuthID"`
	AuthProvider string              `json:"AuthProvider"`
	Method       string              `json:"Method"`
	URL          string              `json:"URL"`
	Headers      map[string][]string `json:"Headers"`
	Body         []byte              `json:"Body"`
	StorageJSON  []byte              `json:"StorageJSON"`
}

// executorHTTPResponse mirrors the host executor HTTP response contract.
type executorHTTPResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers,omitempty"`
	Body       []byte              `json:"Body,omitempty"`
}

type streamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

type streamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

// hopByHopHeaders are never forwarded to the upstream service.
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"host":                {},
	"content-length":      {},
	"authorization":       {},
	// Let the transport negotiate gzip so bodies are decompressed transparently.
	"accept-encoding": {},
}

func decodeExecutorRequest(request []byte) (executorRequest, error) {
	var req executorRequest
	if len(request) != 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return executorRequest{}, fmt.Errorf("decode executor request: %w", err)
		}
	}
	if len(req.Payload) == 0 && len(req.OriginalRequest) > 0 {
		req.Payload = req.OriginalRequest
	}
	if len(req.Payload) == 0 {
		return executorRequest{}, fmt.Errorf("executor request has no payload")
	}
	return req, nil
}

// accountFor resolves the credential used for one executor call. Host-supplied
// auth material wins; plugin-managed accounts are the fallback.
func (d *Dispatcher) accountFor(req executorRequest) (AccountConfig, error) {
	config, _, store := d.snapshot()
	if provider := strings.TrimSpace(req.AuthProvider); provider != "" && !strings.EqualFold(provider, providerKey) {
		return AccountConfig{}, fmt.Errorf("未配置 %s 的凭据", provider)
	}
	if len(req.StorageJSON) > 0 {
		if account, err := AccountFromAuthData(providerKey, req.StorageJSON, req.AuthID); err == nil {
			return account, nil
		}
	}
	if key := strings.TrimSpace(req.AuthAttributes["api_key"]); key != "" {
		return AccountConfig{
			ID:       firstNonEmpty(normalizeAccountID(req.AuthID), "cpa-auth"),
			Label:    firstNonEmpty(normalizeAccountID(req.AuthID), "CPA 认证"),
			Plan:     "go",
			Source:   SourceCPA,
			APIKey:   key,
			Endpoint: strings.TrimSpace(req.AuthAttributes["endpoint"]),
		}, nil
	}
	ctx, cancel := contextWithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	accounts := ResolveAccounts(ctx, config, store)
	if account, ok := FindAccount(accounts, req.AuthID); ok && strings.TrimSpace(account.APIKey) != "" {
		return account, nil
	}
	if account, ok := UsableAccount(accounts); ok {
		return account, nil
	}
	return AccountConfig{}, fmt.Errorf("未找到可用的 OpenCode Go API Key，请在面板或 CPA 中配置")
}

// buildUpstreamRequest maps an executor request onto the OpenCode Go API.
// Payloads are forwarded unchanged so client and upstream protocols stay aligned.
func buildUpstreamRequest(ctx context.Context, req executorRequest, account AccountConfig, base string, stream bool) (*http.Request, error) {
	target, err := url.Parse(strings.TrimRight(account.EndpointOrDefault(base), "/") + upstreamPathForFormat(req.Format))
	if err != nil {
		return nil, fmt.Errorf("build upstream URL: %w", err)
	}
	if len(req.Query) > 0 {
		query := target.Query()
		for name, values := range req.Query {
			for _, value := range values {
				query.Add(name, value)
			}
		}
		target.RawQuery = query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), strings.NewReader(string(req.Payload)))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	for name, values := range req.Headers {
		if _, skip := hopByHopHeaders[strings.ToLower(name)]; skip {
			continue
		}
		for _, value := range values {
			httpReq.Header.Add(name, value)
		}
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.APIKey))
	httpReq.Header.Set("Content-Type", firstNonEmpty(httpReq.Header.Get("Content-Type"), "application/json"))
	httpReq.Header.Set("Accept", firstNonEmpty(httpReq.Header.Get("Accept"), "application/json"))
	httpReq.Header.Set("User-Agent", pluginUserAgent)
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	// A stable conversation id improves upstream routing and prompt caching.
	if httpReq.Header.Get(sessionHeader) == "" {
		httpReq.Header.Set(sessionHeader, sessionForRequest(req, account))
	}
	return httpReq, nil
}

// sessionForRequest derives a stable session id when the client sent none.
func sessionForRequest(req executorRequest, account AccountConfig) string {
	for _, name := range []string{"x-opencode-session", "X-Session-Affinity", "X-Session-Id"} {
		for header, values := range req.Headers {
			if !strings.EqualFold(header, name) {
				continue
			}
			for _, value := range values {
				if trimmed := strings.TrimSpace(value); trimmed != "" {
					return truncate(trimmed, 128)
				}
			}
		}
	}
	return truncate("cpa-"+account.ID+"-"+req.Model, 128)
}

func (d *Dispatcher) upstreamClient() HTTPDoFunc {
	if d != nil && d.httpDo != nil {
		return d.httpDo
	}
	return realHTTPDo
}

func (d *Dispatcher) execute(request []byte) (executorResponse, error) {
	req, err := decodeExecutorRequest(request)
	if err != nil {
		return executorResponse{}, err
	}
	account, err := d.accountFor(req)
	if err != nil {
		return executorResponse{}, err
	}
	config, _, _ := d.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), inferenceTimeout)
	defer cancel()
	httpReq, err := buildUpstreamRequest(ctx, req, account, config.Endpoint, false)
	if err != nil {
		return executorResponse{}, err
	}
	response, err := d.upstreamClient()(ctx, httpReq)
	if err != nil {
		return executorResponse{}, fmt.Errorf("OpenCode Go 请求失败: %w", err)
	}
	defer response.Body.Close()
	body, err := readLimitedResponse(response.Body, maxExecutorResponseBytes)
	if err != nil {
		return executorResponse{}, err
	}
	out := executorResponse{Payload: body, Headers: forwardHeaders(response.Header)}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Surface the upstream status so CPA can react to 429/401 correctly.
		out.Metadata = map[string]any{"status_code": response.StatusCode}
		if response.StatusCode == http.StatusTooManyRequests {
			return out, fmt.Errorf("OpenCode Go 用量已达上限或请求被限流（429）%s", apiErrorMessage(body))
		}
	}
	return out, nil
}

// executeStream starts the upstream stream and forwards chunks through the host
// streaming bridge. It returns before the stream ends, so the host can consume
// chunks while they are produced.
func (d *Dispatcher) executeStream(request []byte) (executorStreamResponse, error) {
	req, err := decodeExecutorRequest(request)
	if err != nil {
		return executorStreamResponse{}, err
	}
	account, err := d.accountFor(req)
	if err != nil {
		return executorStreamResponse{}, err
	}
	config, _, _ := d.snapshot()
	// Streaming runs until the client or upstream ends it, so it has no deadline.
	ctx, cancel := context.WithCancel(context.Background())
	httpReq, err := buildUpstreamRequest(ctx, req, account, config.Endpoint, true)
	if err != nil {
		cancel()
		return executorStreamResponse{}, err
	}
	response, err := d.upstreamClient()(ctx, httpReq)
	if err != nil {
		cancel()
		return executorStreamResponse{}, fmt.Errorf("OpenCode Go 流式请求失败: %w", err)
	}
	headers := forwardHeaders(response.Header)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer cancel()
		// Read the error body before closing so the upstream reason survives.
		body, _ := readLimitedResponse(response.Body, maxExecutorResponseBytes)
		response.Body.Close()
		return executorStreamResponse{}, fmt.Errorf("OpenCode Go 流式请求失败（%d）%s", response.StatusCode, apiErrorMessage(body))
	}
	if strings.TrimSpace(req.StreamID) == "" {
		// Older hosts expect the chunks inline; buffer the whole stream.
		defer cancel()
		defer response.Body.Close()
		body, err := readLimitedResponse(response.Body, maxExecutorResponseBytes)
		if err != nil {
			return executorStreamResponse{}, err
		}
		return executorStreamResponse{Headers: headers, Chunks: []executorStreamChunk{{Payload: body}}}, nil
	}
	go streamToHost(ctx, cancel, response.Body, req.StreamID)
	return executorStreamResponse{Headers: headers}, nil
}

// streamToHost forwards upstream bytes to the host bridge and always closes it.
func streamToHost(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser, streamID string) {
	defer cancel()
	defer body.Close()
	buffer := make([]byte, streamChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			_ = emitStreamError(streamID, err.Error())
			return
		}
		read, errRead := body.Read(buffer)
		if read > 0 {
			payload := append([]byte(nil), buffer[:read]...)
			if errEmit := callStreamEmit(streamEmitRequest{StreamID: streamID, Payload: payload}); errEmit != nil {
				// The host closed the stream (client gone); stop forwarding.
				return
			}
		}
		if errRead != nil {
			if errRead == io.EOF {
				_ = callStreamClose(streamCloseRequest{StreamID: streamID})
				return
			}
			_ = emitStreamError(streamID, errRead.Error())
			return
		}
	}
}

func emitStreamError(streamID, message string) error {
	_ = callStreamEmit(streamEmitRequest{StreamID: streamID, Error: sanitizeMessage(message)})
	return callStreamClose(streamCloseRequest{StreamID: streamID, Error: sanitizeMessage(message)})
}

func callStreamEmit(request streamEmitRequest) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = callHost(pluginabi.MethodHostStreamEmit, raw)
	return err
}

func callStreamClose(request streamCloseRequest) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = callHost(pluginabi.MethodHostStreamClose, raw)
	return err
}

// countTokens returns a byte-based estimate because OpenCode Go exposes no
// tokenizer. Values are intentionally approximate.
func (d *Dispatcher) countTokens(request []byte) (executorResponse, error) {
	req, err := decodeExecutorRequest(request)
	if err != nil {
		return executorResponse{}, err
	}
	estimate := (len(req.Payload) + 3) / 4
	payload, err := json.Marshal(map[string]any{"total_tokens": estimate, "input_tokens": estimate})
	if err != nil {
		return executorResponse{}, err
	}
	return executorResponse{
		Payload:  payload,
		Headers:  map[string][]string{"Content-Type": {"application/json"}},
		Metadata: map[string]any{"estimated": true},
	}, nil
}

// httpRequest performs an executor-owned HTTP call, restricted to OpenCode Go.
func (d *Dispatcher) httpRequest(request []byte) (executorHTTPResponse, error) {
	var req executorHTTPRequest
	if len(request) != 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return executorHTTPResponse{}, fmt.Errorf("decode executor http request: %w", err)
		}
	}
	target, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || target.Scheme != "https" || target.Hostname() != allowedEndpointHost {
		return executorHTTPResponse{}, fmt.Errorf("executor http request must target https://%s", allowedEndpointHost)
	}
	account, err := d.accountFor(executorRequest{AuthID: req.AuthID, AuthProvider: req.AuthProvider, StorageJSON: req.StorageJSON})
	if err != nil {
		return executorHTTPResponse{}, err
	}
	method := strings.ToUpper(firstNonEmpty(req.Method, http.MethodGet))
	ctx, cancel := context.WithTimeout(context.Background(), inferenceTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, method, target.String(), strings.NewReader(string(req.Body)))
	if err != nil {
		return executorHTTPResponse{}, err
	}
	for name, values := range req.Headers {
		if _, skip := hopByHopHeaders[strings.ToLower(name)]; skip {
			continue
		}
		for _, value := range values {
			httpReq.Header.Add(name, value)
		}
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.APIKey))
	httpReq.Header.Set("User-Agent", pluginUserAgent)
	response, err := d.upstreamClient()(ctx, httpReq)
	if err != nil {
		return executorHTTPResponse{}, fmt.Errorf("OpenCode Go 请求失败: %w", err)
	}
	defer response.Body.Close()
	body, err := readLimitedResponse(response.Body, maxExecutorResponseBytes)
	if err != nil {
		return executorHTTPResponse{}, err
	}
	return executorHTTPResponse{StatusCode: response.StatusCode, Headers: forwardHeaders(response.Header), Body: body}, nil
}

// modelResponse advertises the OpenCode Go catalog for both static and per-auth
// discovery, refreshing it in the background when it is stale.
func (d *Dispatcher) modelResponse(request []byte) pluginapi.ModelResponse {
	var req struct {
		AuthProvider string `json:"AuthProvider"`
		AuthID       string `json:"AuthID"`
		StorageJSON  []byte `json:"StorageJSON"`
	}
	if len(request) != 0 {
		_ = json.Unmarshal(request, &req)
	}
	if provider := strings.TrimSpace(req.AuthProvider); provider != "" && !strings.EqualFold(provider, providerKey) {
		return pluginapi.ModelResponse{}
	}
	config, service, store := d.snapshot()
	models := service.Catalog().Models()
	if !service.Catalog().Fresh() {
		go func() {
			ctx, cancel := contextWithTimeout(context.Background(), config.Timeout)
			defer cancel()
			service.RefreshCatalog(ctx, ResolveAccounts(ctx, config, store))
		}()
	}
	return pluginapi.ModelResponse{Provider: providerKey, Models: models}
}

// parseAuth accepts an OpenCode Go credential file so the provider can be
// managed as a CPA auth as well as through the plugin panel.
func (d *Dispatcher) parseAuth(request []byte) (pluginapi.AuthParseResponse, error) {
	var req struct {
		Provider string          `json:"Provider"`
		FileName string          `json:"FileName"`
		RawJSON  json.RawMessage `json:"RawJSON"`
	}
	if len(request) != 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return pluginapi.AuthParseResponse{}, fmt.Errorf("decode auth parse request: %w", err)
		}
	}
	if provider := strings.TrimSpace(req.Provider); provider != "" && !strings.EqualFold(provider, providerKey) {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	account, err := AccountFromAuthData(providerKey, req.RawJSON, strings.TrimSuffix(strings.TrimSpace(req.FileName), ".json"))
	if err != nil {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: authDataFor(account, req.FileName)}, nil
}

// refreshAuth keeps the stored API key unchanged but reports the refresh result,
// because OpenCode Go API keys do not expire.
func (d *Dispatcher) refreshAuth(request []byte) (pluginapi.AuthRefreshResponse, error) {
	var req struct {
		AuthID       string          `json:"AuthID"`
		AuthProvider string          `json:"AuthProvider"`
		StorageJSON  json.RawMessage `json:"StorageJSON"`
	}
	if len(request) != 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return pluginapi.AuthRefreshResponse{}, fmt.Errorf("decode auth refresh request: %w", err)
		}
	}
	if provider := strings.TrimSpace(req.AuthProvider); provider != "" && !strings.EqualFold(provider, providerKey) {
		return pluginapi.AuthRefreshResponse{}, fmt.Errorf("unsupported auth provider %q", provider)
	}
	account, err := AccountFromAuthData(providerKey, req.StorageJSON, req.AuthID)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	return pluginapi.AuthRefreshResponse{Auth: authDataFor(account, "")}, nil
}

func authDataFor(account AccountConfig, fileName string) pluginapi.AuthData {
	storage, _ := json.Marshal(map[string]any{
		"id":       account.ID,
		"provider": providerKey,
		"type":     providerKey,
		"label":    account.Label,
		"api_key":  account.APIKey,
	})
	attributes := map[string]string{"api_key": account.APIKey}
	if account.Endpoint != "" {
		attributes["endpoint"] = account.Endpoint
	}
	return pluginapi.AuthData{
		Provider:    providerKey,
		ID:          account.ID,
		FileName:    firstNonEmpty(fileName, account.ID+".json"),
		Label:       account.Label,
		StorageJSON: storage,
		Attributes:  attributes,
	}
}
