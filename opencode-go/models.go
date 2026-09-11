package main

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// providerKey is the CPA provider key this plugin owns.
const providerKey = "opencode-go"

// TypeMarker classifies plugin-provided models in the host registry.
const modelType = "opencode-go"

// catalogTTL bounds how long a fetched model catalog stays fresh.
const catalogTTL = time.Hour

// documentedModelIDs is the published OpenCode Go model catalog, used when the
// live catalog cannot be fetched.
var documentedModelIDs = []string{
	"grok-4.6",
	"gpt-5.6-luna",
	"glm-5.3-flash",
	"glm-5.3",
	"glm-5.2",
	"glm-5.1",
	"kimi-k3",
	"kimi-k2.7-code",
	"kimi-k2.6",
	"longcat-2.0",
	"deepseek-v4.1-flash",
	"deepseek-v4-pro",
	"deepseek-v4-flash",
	"deepseek-v4-flash-vision-exp",
	"mimo-v2.5",
	"mimo-v2.5-pro",
	"minimax-m3",
	"minimax-m2.7",
	"minimax-m2.5",
	"muse-spark-1.3-contributor",
	"muse-spark-1.2-contributor",
	"qwen3.8-max",
	"qwen3.8-flash",
	"qwen3.7-max",
	"qwen3.7-plus",
	"qwen3.6-plus",
	"hy4-preview",
	"hy3",
}

// fallbackModelCatalog returns the documented catalog as host model metadata.
func fallbackModelCatalog() []pluginapi.ModelInfo {
	models := make([]pluginapi.ModelInfo, 0, len(documentedModelIDs))
	for _, id := range documentedModelIDs {
		models = append(models, ModelInfoFor(id))
	}
	return models
}

// ModelInfoFor builds host model metadata for one OpenCode Go model id.
func ModelInfoFor(id string) pluginapi.ModelInfo {
	id = strings.TrimSpace(id)
	return pluginapi.ModelInfo{
		ID:                         id,
		Object:                     "model",
		Type:                       modelType,
		DisplayName:                "OpenCode Go " + id,
		Name:                       id,
		OwnedBy:                    "opencode",
		SupportedGenerationMethods: []string{"chat.completions", "messages", "responses"},
		SupportedInputModalities:   []string{"text"},
		SupportedOutputModalities:  []string{"text"},
	}
}

// ModelInfosFromEntries converts a fetched catalog into host model metadata.
func ModelInfosFromEntries(entries []ModelEntry) []pluginapi.ModelInfo {
	models := make([]pluginapi.ModelInfo, 0, len(entries))
	for _, entry := range entries {
		model := ModelInfoFor(entry.ID)
		model.Created = entry.Created
		if strings.TrimSpace(entry.OwnedBy) != "" {
			model.OwnedBy = entry.OwnedBy
		}
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

// upstreamPathForFormat maps an executor request format to the OpenCode Go path.
// Requests are passed through unchanged, so the upstream path mirrors the
// client protocol the host selected.
func upstreamPathForFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "openai-response", "responses", "openai-responses":
		return "/responses"
	case "claude", "anthropic", "anthropic-messages":
		return "/messages"
	default:
		return "/chat/completions"
	}
}

// catalog caches the live OpenCode Go model catalog for host model callbacks.
type catalog struct {
	mu        sync.Mutex
	models    []pluginapi.ModelInfo
	fetchedAt time.Time
}

// Models returns the freshest catalog available, preferring live data.
func (c *catalog) Models() []pluginapi.ModelInfo {
	if c == nil {
		return fallbackModelCatalog()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.models) == 0 {
		return fallbackModelCatalog()
	}
	return append([]pluginapi.ModelInfo(nil), c.models...)
}

// Store records a freshly fetched catalog.
func (c *catalog) Store(models []pluginapi.ModelInfo) {
	if c == nil || len(models) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = append([]pluginapi.ModelInfo(nil), models...)
	c.fetchedAt = time.Now().UTC()
}

// Fresh reports whether the cached catalog is still within its TTL.
func (c *catalog) Fresh() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.models) > 0 && time.Since(c.fetchedAt) < catalogTTL
}

// ModelIDs lists the ids currently advertised, for diagnostics.
func (c *catalog) ModelIDs() []string {
	models := c.Models()
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}
