package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// usagePath is the documented OpenCode Go subscription usage endpoint.
	usagePath = "/usage"
	// modelsPath lists the models available to Go subscribers.
	modelsPath = "/models"
	// pluginUserAgent identifies this plugin to the upstream service.
	pluginUserAgent = "opencode-go-cpa-plugin/0.1"
)

// UsageWindow is one subscription window reported by OpenCode Go.
type UsageWindow struct {
	Name             string     `json:"name,omitempty"`
	Group            string     `json:"group,omitempty"`
	Status           string     `json:"status,omitempty"`
	UsedPercent      float64    `json:"used_percent"`
	RemainingPercent float64    `json:"remaining_percent"`
	ResetAt          *time.Time `json:"reset_at,omitempty"`
}

// UsageResult is the subscription usage snapshot for one account.
type UsageResult struct {
	Level   string        `json:"level,omitempty"`
	Rolling UsageWindow   `json:"rolling"`
	Weekly  UsageWindow   `json:"weekly"`
	Monthly UsageWindow   `json:"monthly"`
	Windows []UsageWindow `json:"windows,omitempty"`
}

// ModelEntry is one OpenCode Go model.
type ModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type usageEntry struct {
	Status   string   `json:"status"`
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resetsAt"`
}

type usagePayload struct {
	Usage struct {
		Rolling *usageEntry `json:"rolling"`
		Weekly  *usageEntry `json:"weekly"`
		Monthly *usageEntry `json:"monthly"`
	} `json:"usage"`
}

type apiErrorPayload struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ParseUsageResponse decodes the /zen/go/v1/usage payload.
func ParseUsageResponse(body []byte) (*UsageResult, error) {
	var payload usagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Usage.Rolling == nil && payload.Usage.Weekly == nil && payload.Usage.Monthly == nil {
		return nil, fmt.Errorf("usage payload contains no windows")
	}
	result := &UsageResult{Level: "OpenCode Go"}
	if window, ok := usageWindow("five_hour", payload.Usage.Rolling); ok {
		result.Rolling = window
		result.Windows = append(result.Windows, window)
	}
	if window, ok := usageWindow("weekly", payload.Usage.Weekly); ok {
		result.Weekly = window
		result.Windows = append(result.Windows, window)
	}
	if window, ok := usageWindow("monthly", payload.Usage.Monthly); ok {
		result.Monthly = window
		result.Windows = append(result.Windows, window)
	}
	return result, nil
}

func usageWindow(name string, entry *usageEntry) (UsageWindow, bool) {
	if entry == nil {
		return UsageWindow{}, false
	}
	used := 0.0
	if entry.Percent != nil {
		used = clamp(*entry.Percent)
	}
	status := strings.ToLower(strings.TrimSpace(entry.Status))
	if status == "" {
		status = "ok"
	}
	window := UsageWindow{
		Name:             name,
		Group:            "OpenCode Go",
		Status:           status,
		UsedPercent:      used,
		RemainingPercent: clamp(100 - used),
	}
	if reset, ok := parseResetTime(entry.ResetsAt); ok {
		window.ResetAt = &reset
	}
	return window, true
}

func parseResetTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

// ParseModelsResponse decodes the OpenAI-compatible /zen/go/v1/models payload.
func ParseModelsResponse(body []byte) ([]ModelEntry, error) {
	var payload struct {
		Data []ModelEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	models := make([]ModelEntry, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, model := range payload.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" || strings.ContainsAny(id, " \t\n") {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		model.ID = id
		models = append(models, model)
	}
	return models, nil
}

// HTTPDoFunc performs one HTTP round trip using the plugin transport.
type HTTPDoFunc func(context.Context, *http.Request) (*http.Response, error)

// QuotaClient fetches OpenCode Go subscription data.
type QuotaClient interface {
	FetchUsage(context.Context, AccountConfig, string) (*UsageResult, error)
	FetchModels(context.Context, AccountConfig, string) ([]ModelEntry, error)
}

// Client is the OpenCode Go HTTP client.
type Client struct {
	httpDo HTTPDoFunc
}

// NewClient wraps an HTTP transport in an OpenCode Go client.
func NewClient(httpDo HTTPDoFunc) *Client {
	if httpDo == nil {
		httpDo = func(context.Context, *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("http transport unavailable")
		}
	}
	return &Client{httpDo: httpDo}
}

// FetchUsage reads the subscription usage windows for one account.
func (c *Client) FetchUsage(ctx context.Context, account AccountConfig, base string) (*UsageResult, error) {
	raw, status, err := c.get(ctx, account, account.EndpointOrDefault(base)+usagePath)
	if err != nil {
		return nil, fmt.Errorf("OpenCode Go 用量请求失败: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, usageStatusError(status, raw)
	}
	result, err := ParseUsageResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 OpenCode Go 用量响应失败: %w", err)
	}
	return result, nil
}

// FetchModels reads the model catalog available to OpenCode Go subscribers.
func (c *Client) FetchModels(ctx context.Context, account AccountConfig, base string) ([]ModelEntry, error) {
	raw, status, err := c.get(ctx, account, account.EndpointOrDefault(base)+modelsPath)
	if err != nil {
		return nil, fmt.Errorf("OpenCode Go 模型列表请求失败: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, usageStatusError(status, raw)
	}
	models, err := ParseModelsResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 OpenCode Go 模型列表失败: %w", err)
	}
	return models, nil
}

func (c *Client) get(ctx context.Context, account AccountConfig, endpoint string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.APIKey))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", pluginUserAgent)
	response, err := c.httpDo(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	raw, err := readLimitedResponse(response.Body, maxQuotaResponseBytes)
	if err != nil {
		return nil, response.StatusCode, err
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return raw, response.StatusCode, fmt.Errorf("接口返回了重定向")
	}
	return raw, response.StatusCode, nil
}

func usageStatusError(status int, body []byte) error {
	detail := apiErrorMessage(body)
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("OpenCode Go 拒绝了该 API Key（401）%s", detail)
	case http.StatusForbidden:
		return fmt.Errorf("该 API Key 未订阅 OpenCode Go（403）%s", detail)
	case http.StatusTooManyRequests:
		return fmt.Errorf("OpenCode Go 用量接口限流（429）%s", detail)
	case http.StatusNotFound:
		return fmt.Errorf("OpenCode Go 用量接口不存在（404）%s", detail)
	default:
		return fmt.Errorf("OpenCode Go API %d%s", status, detail)
	}
}

func apiErrorMessage(body []byte) string {
	var payload apiErrorPayload
	if err := json.Unmarshal(body, &payload); err == nil {
		message := firstNonEmpty(payload.Error.Message, payload.Error.Type, payload.Type)
		if message != "" {
			return ": " + sanitizeMessage(message)
		}
	}
	message := sanitizeMessage(string(body))
	if message == "" {
		return ""
	}
	return ": " + message
}
