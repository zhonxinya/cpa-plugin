package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileAccountStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewFileAccountStore(dir)
	ctx := context.Background()

	saved, err := store.Save(ctx, "main.json", []byte(`{"api_key":"sk-main","label":"主账号"}`))
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if saved.ID != "main" || saved.Label != "主账号" || saved.APIKey != "sk-main" {
		t.Fatalf("saved = %#v", saved)
	}
	if saved.Source != SourcePanel {
		t.Errorf("Source = %q, want %q", saved.Source, SourcePanel)
	}

	records, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].ID != "main" {
		t.Fatalf("List() = %#v", records)
	}

	got, err := store.Get(ctx, "main.json")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.APIKey != "sk-main" {
		t.Errorf("Get() APIKey = %q", got.APIKey)
	}
}

func TestFileAccountStoreSaveReplacesExisting(t *testing.T) {
	store := NewFileAccountStore(t.TempDir())
	ctx := context.Background()
	if _, err := store.Save(ctx, "a", []byte(`{"api_key":"first"}`)); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}
	if _, err := store.Save(ctx, "a", []byte(`{"api_key":"second"}`)); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	records, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() = %#v, want a single replaced record", records)
	}
	if records[0].APIKey != "second" {
		t.Errorf("APIKey = %q, want second", records[0].APIKey)
	}
}

func TestFileAccountStoreDelete(t *testing.T) {
	store := NewFileAccountStore(t.TempDir())
	ctx := context.Background()
	if _, err := store.Save(ctx, "a", []byte(`{"api_key":"x"}`)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := store.Save(ctx, "b", []byte(`{"api_key":"y"}`)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Delete(ctx, "a"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	records, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].ID != "b" {
		t.Fatalf("List() = %#v, want only b", records)
	}
	if _, err := store.Get(ctx, "a"); err == nil {
		t.Error("Get() found a deleted account")
	}
	if err := store.Delete(ctx, ""); err == nil {
		t.Error("Delete() accepted an empty id")
	}
}

func TestFileAccountStoreRejectsInvalidInput(t *testing.T) {
	store := NewFileAccountStore(t.TempDir())
	ctx := context.Background()
	if _, err := store.Save(ctx, "bad id!", []byte(`{"api_key":"x"}`)); err == nil {
		t.Error("Save() accepted an invalid id")
	}
	if _, err := store.Save(ctx, "a", []byte(`not-json`)); err == nil {
		t.Error("Save() accepted invalid JSON")
	}
}

func TestFileAccountStoreFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions are not enforced on Windows")
	}
	dir := t.TempDir()
	store := NewFileAccountStore(dir)
	if _, err := store.Save(context.Background(), "a", []byte(`{"api_key":"x"}`)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, pluginAccountsFileName))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %o, want 600", mode)
	}
}

func TestStoredAccountFromRecord(t *testing.T) {
	account, err := storedAccountFromRecord([]byte(`{"name":"legacy","token":{"access_token":"sk-token"}}`))
	if err != nil {
		t.Fatalf("storedAccountFromRecord() error = %v", err)
	}
	if account.ID != "legacy" || account.APIKey != "sk-token" {
		t.Errorf("account = %#v", account)
	}
	if _, err := storedAccountFromRecord([]byte(`{}`)); err == nil {
		t.Error("storedAccountFromRecord() accepted a record without an id")
	}
}

func TestMakeAccountID(t *testing.T) {
	if got := MakeAccountID("主账号"); got != "opencode-go" {
		t.Errorf("MakeAccountID(non-ascii) = %q, want opencode-go", got)
	}
	got := MakeAccountID("My Main Account")
	if got != "opencode-go-my-main-account" {
		t.Errorf("MakeAccountID = %q", got)
	}
	if !accountIDPattern.MatchString(got) {
		t.Errorf("MakeAccountID produced an id the pattern rejects: %q", got)
	}
	long := MakeAccountID(strings.Repeat("a", 200))
	if len(long) > 64 || !accountIDPattern.MatchString(long) {
		t.Errorf("MakeAccountID(long) = %q", long)
	}
}

func TestNormalizeAccountID(t *testing.T) {
	if got := normalizeAccountID("  main.json "); got != "main" {
		t.Errorf("normalizeAccountID = %q", got)
	}
	if got := normalizeAccountID(""); got != "" {
		t.Errorf("normalizeAccountID(empty) = %q", got)
	}
}
