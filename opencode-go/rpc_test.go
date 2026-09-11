package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func mustCall(t *testing.T, d *Dispatcher, method string, request any) []byte {
	t.Helper()
	var raw []byte
	if request != nil {
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		raw = encoded
	}
	response, err := d.Call(method, raw)
	if err != nil {
		t.Fatalf("Call(%s) error = %v", method, err)
	}
	return response
}

func TestRegisterCapabilities(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusOK, capturedUsagePayload))
	raw := mustCall(t, d, pluginabi.MethodPluginRegister, lifecycleRequest{})
	var registration registration
	decodeOK(t, raw, &registration)
	if registration.SchemaVersion != pluginabi.SchemaVersion {
		t.Errorf("schema version = %d", registration.SchemaVersion)
	}
	capabilities := registration.Capabilities
	if !capabilities.ModelProvider || !capabilities.AuthProvider || !capabilities.Executor || !capabilities.ManagementAPI {
		t.Errorf("capabilities = %#v", capabilities)
	}
	if registration.Metadata.Name != pluginName || registration.Metadata.Version != pluginVersion {
		t.Errorf("metadata = %#v", registration.Metadata)
	}
}

func TestManagementRegistration(t *testing.T) {
	d := testDispatcher(t, nil)
	var response pluginapi.ManagementRegistrationResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodManagementRegister, nil), &response)
	if len(response.Resources) != 1 || response.Resources[0].Path != "/status" {
		t.Errorf("resources = %#v", response.Resources)
	}
	methods := map[string]bool{}
	for _, route := range response.Routes {
		methods[route.Method] = true
	}
	for _, want := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		if !methods[want] {
			t.Errorf("management routes missing method %s", want)
		}
	}
}

func TestManagementStatusDashboard(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusOK, capturedUsagePayload))
	if _, err := d.accounts.Save(nil, "main", []byte(`{"api_key":"sk","label":"主账号"}`)); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	var response pluginapi.ManagementResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodManagementHandle, pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   managementStatusPath,
		Query:  url.Values{"view": {"overview"}},
	}), &response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if !strings.Contains(string(response.Body), "主账号") {
		t.Error("dashboard missing the account label")
	}
	if strings.Contains(string(response.Body), "sk") && strings.Contains(string(response.Body), "Bearer") {
		t.Error("dashboard leaked credentials")
	}
}

func TestManagementResourceRouteHidesData(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusOK, capturedUsagePayload))
	if _, err := d.accounts.Save(nil, "main", []byte(`{"api_key":"sk","label":"secret-label"}`)); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	var response pluginapi.ManagementResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodManagementHandle, pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   resourceStatusPath,
	}), &response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if strings.Contains(string(response.Body), "secret-label") {
		t.Error("resource route leaked account data")
	}
}

func TestManagementUnknownRoute(t *testing.T) {
	d := testDispatcher(t, nil)
	var response pluginapi.ManagementResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodManagementHandle, pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/nope",
	}), &response)
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.StatusCode)
	}
}

func TestManagementSaveAccount(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusOK, capturedUsagePayload))
	body, _ := json.Marshal(saveAccountRequest{Label: "Main Account", Credential: "sk-valid"})
	var response pluginapi.ManagementResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodManagementHandle, pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   managementAccountsPath,
		Body:   body,
	}), &response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.StatusCode, response.Body)
	}
	var saved struct {
		ID   string `json:"id"`
		Mask string `json:"credential_mask"`
	}
	if err := json.Unmarshal(response.Body, &saved); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if saved.ID != "opencode-go-main-account" {
		t.Errorf("id = %q", saved.ID)
	}
	if strings.Contains(saved.Mask, "sk-valid") {
		t.Errorf("mask leaked credential: %q", saved.Mask)
	}
	if _, err := d.accounts.Get(nil, saved.ID); err != nil {
		t.Errorf("saved account not persisted: %v", err)
	}
}

func TestManagementSaveAccountRejectsUnreachable(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusUnauthorized, `{"error":{"message":"nope"}}`))
	body, _ := json.Marshal(saveAccountRequest{Label: "Bad", Credential: "sk-bad"})
	var response pluginapi.ManagementResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodManagementHandle, pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   managementAccountsPath,
		Body:   body,
	}), &response)
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.StatusCode)
	}
}

func TestManagementSaveAccountValidatesInput(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusOK, capturedUsagePayload))
	cases := map[string]saveAccountRequest{
		"missing label":      {Credential: "sk"},
		"missing credential": {Label: "x"},
	}
	for name, input := range cases {
		body, _ := json.Marshal(input)
		var response pluginapi.ManagementResponse
		decodeOK(t, mustCall(t, d, pluginabi.MethodManagementHandle, pluginapi.ManagementRequest{
			Method: http.MethodPost,
			Path:   managementAccountsPath,
			Body:   body,
		}), &response)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, response.StatusCode)
		}
	}
}

func TestManagementDeleteAccount(t *testing.T) {
	d := testDispatcher(t, nil)
	if _, err := d.accounts.Save(nil, "main", []byte(`{"api_key":"sk"}`)); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	var response pluginapi.ManagementResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodManagementHandle, pluginapi.ManagementRequest{
		Method: http.MethodDelete,
		Path:   managementAccountsPath,
		Query:  url.Values{"id": {"main"}},
	}), &response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if _, err := d.accounts.Get(nil, "main"); err == nil {
		t.Error("account still present after delete")
	}
}

func TestExecutorIdentifierAndUnknownMethod(t *testing.T) {
	d := testDispatcher(t, nil)
	var identifier identifierResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodExecutorIdentifier, nil), &identifier)
	if identifier.Identifier != providerKey {
		t.Errorf("identifier = %q", identifier.Identifier)
	}
	if _, err := d.Call("not.a.method", nil); err == nil {
		t.Error("Call() accepted an unknown method")
	}
}

func TestExecutorExecute(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusOK, `{"id":"chatcmpl-1"}`))
	request := executorRequest{
		AuthProvider:   providerKey,
		Model:          "glm-5.3",
		Format:         "chat-completions",
		AuthAttributes: map[string]string{"api_key": "sk-exec"},
		Payload:        []byte(`{"model":"glm-5.3","messages":[]}`),
	}
	var response executorResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodExecutorExecute, request), &response)
	if string(response.Payload) != `{"id":"chatcmpl-1"}` {
		t.Errorf("payload = %s", response.Payload)
	}
}

func TestExecutorExecuteWithoutCredential(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusOK, `{}`))
	request := executorRequest{Format: "chat-completions", Payload: []byte(`{}`)}
	if _, err := d.Call(pluginabi.MethodExecutorExecute, mustJSON(request)); err == nil {
		t.Error("execute without a credential succeeded")
	}
}

func TestExecutorExecuteStreamBuffersWithoutStreamID(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusOK, "data: chunk\n\n"))
	request := executorRequest{
		Format:         "chat-completions",
		AuthAttributes: map[string]string{"api_key": "sk-exec"},
		Payload:        []byte(`{}`),
	}
	var response executorStreamResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodExecutorExecuteStream, request), &response)
	if len(response.Chunks) != 1 || string(response.Chunks[0].Payload) != "data: chunk\n\n" {
		t.Errorf("chunks = %#v", response.Chunks)
	}
}

func TestExecutorExecuteStreamReportsUpstreamDetail(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusTooManyRequests, `{"error":{"message":"rate limit hit"}}`))
	request := executorRequest{
		Format:         "chat-completions",
		AuthAttributes: map[string]string{"api_key": "sk-exec"},
		Payload:        []byte(`{}`),
	}
	_, err := d.Call(pluginabi.MethodExecutorExecuteStream, mustJSON(request))
	if err == nil {
		t.Fatal("executeStream() succeeded on a 429")
	}
	// The upstream reason must survive instead of being reported as a bare status.
	if !strings.Contains(err.Error(), "rate limit hit") {
		t.Errorf("error = %v, want the upstream message", err)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %v, want the status code", err)
	}
}

func TestExecutorCountTokensEstimates(t *testing.T) {
	d := testDispatcher(t, nil)
	var response executorResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodExecutorCountTokens, executorRequest{Payload: []byte("abcdefgh")}), &response)
	var payload struct {
		Total int `json:"total_tokens"`
		Input int `json:"input_tokens"`
	}
	if err := json.Unmarshal(response.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Total != 2 || payload.Input != 2 {
		t.Errorf("token estimate = %#v", payload)
	}
	if estimated, _ := response.Metadata["estimated"].(bool); !estimated {
		t.Errorf("metadata = %#v, want estimated=true", response.Metadata)
	}
}

func TestExecutorHTTPRequestRestrictsHost(t *testing.T) {
	d := testDispatcher(t, fakeHTTPDo(http.StatusOK, `{}`))
	_, err := d.Call(pluginabi.MethodExecutorHTTPRequest, mustJSON(executorHTTPRequest{
		Method: http.MethodGet,
		URL:    "https://evil.example/usage",
	}))
	if err == nil || !strings.Contains(err.Error(), "opencode.ai") {
		t.Errorf("error = %v, want opencode.ai restriction", err)
	}
}

func TestModelStaticUsesFallbackCatalog(t *testing.T) {
	d := testDispatcher(t, nil)
	var response pluginapi.ModelResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodModelStatic, nil), &response)
	if response.Provider != providerKey {
		t.Errorf("provider = %q", response.Provider)
	}
	if len(response.Models) != len(documentedModelIDs) {
		t.Errorf("models = %d, want %d", len(response.Models), len(documentedModelIDs))
	}
}

func TestParseAuthHandlesCredential(t *testing.T) {
	d := testDispatcher(t, nil)
	var response pluginapi.AuthParseResponse
	decodeOK(t, mustCall(t, d, pluginabi.MethodAuthParse, map[string]any{
		"Provider": providerKey,
		"FileName": "main.json",
		"RawJSON":  json.RawMessage(`{"api_key":"sk-auth","label":"Main"}`),
	}), &response)
	if !response.Handled || response.Auth.Provider != providerKey {
		t.Fatalf("response = %#v", response)
	}
	if response.Auth.Attributes["api_key"] != "sk-auth" {
		t.Errorf("attributes = %#v", response.Auth.Attributes)
	}
}

func TestAuthLoginFlowRejected(t *testing.T) {
	d := testDispatcher(t, nil)
	if _, err := d.Call(pluginabi.MethodAuthLoginStart, nil); err == nil {
		t.Error("login start should be unsupported for API keys")
	}
	if _, err := d.Call(pluginabi.MethodAuthLoginPoll, nil); err == nil {
		t.Error("login poll should be unsupported for API keys")
	}
}
