package main

import (
	"os"
	"strings"
	"testing"
)

// TestPluginVersionMatchesVersionFile keeps the reported registration version in
// sync with the VERSION file used by the release workflow.
func TestPluginVersionMatchesVersionFile(t *testing.T) {
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	want := strings.TrimSpace(string(raw))
	if want == "" {
		t.Fatal("VERSION file is empty")
	}
	if pluginVersion != want {
		t.Fatalf("pluginVersion = %q, want VERSION file %q", pluginVersion, want)
	}
}
