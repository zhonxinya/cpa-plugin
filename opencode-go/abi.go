package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; cliproxy_host_call_fn call; cliproxy_host_free_fn free_buffer; } cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
static cliproxy_host_api* stored_host_api;
static void store_host_api(cliproxy_host_api* host) { stored_host_api = host; }
static int call_stored_host(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host_api == NULL || stored_host_api->call == NULL || method == NULL) {
		return -1;
	}
	return stored_host_api->call(stored_host_api->host_ctx, method, request, request_len, response);
}
static void free_stored_host(void* ptr, size_t length) {
	if (ptr == NULL) {
		return;
	}
	// Host-allocated memory can only be released by the host allocator.
	if (stored_host_api != NULL && stored_host_api->free_buffer != NULL) {
		stored_host_api->free_buffer(ptr, length);
	}
}
*/
import "C"

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

var dispatcher = NewDispatcher(NewClient(realHTTPDo))

var (
	transportOnce sync.Once
	transport     *http.Transport
)

// upstreamTransport returns the shared transport used for OpenCode Go calls.
// TLS verification stays enabled and environment proxies are honoured.
func upstreamTransport() *http.Transport {
	transportOnce.Do(func() {
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		transport = &http.Transport{
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			ExpectContinueTimeout: 2 * time.Second,
		}
	})
	return transport
}

func realHTTPDo(ctx context.Context, req *http.Request) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return upstreamTransport().RoundTrip(req.WithContext(ctx))
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeBuffer(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var body []byte
	if request != nil && requestLen > 0 {
		body = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := dispatcher.Call(C.GoString(method), body)
	if err != nil {
		writeBuffer(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}
	writeBuffer(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func writeBuffer(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

// callHost invokes a host callback with no response size limit.
func callHost(method string, request []byte) ([]byte, error) {
	return callHostWithResponseLimit(method, request, 0)
}

// callHostWithResponseLimit invokes a host callback and copies the reply into
// Go memory before releasing the host-owned buffer.
func callHostWithResponseLimit(method string, request []byte, maxResponseBytes int) ([]byte, error) {
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var requestPtr *C.uint8_t
	if len(request) > 0 {
		requestPtr = (*C.uint8_t)(C.CBytes(request))
		defer C.free(unsafe.Pointer(requestPtr))
	}
	var response C.cliproxy_buffer
	status := C.call_stored_host(cMethod, requestPtr, C.size_t(len(request)), &response)
	if status != 0 {
		if response.ptr != nil {
			C.free_stored_host(response.ptr, response.len)
		}
		return nil, fmt.Errorf("host callback %q unavailable (status %d)", method, int(status))
	}
	if response.ptr == nil || response.len == 0 {
		return nil, nil
	}
	if maxResponseBytes > 0 && uint64(response.len) > uint64(maxResponseBytes) {
		C.free_stored_host(response.ptr, response.len)
		return nil, fmt.Errorf("host response %q exceeds %d bytes", method, maxResponseBytes)
	}
	raw := C.GoBytes(response.ptr, C.int(response.len))
	C.free_stored_host(response.ptr, response.len)
	return raw, nil
}
