package secret

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreReturnsErrorForCorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(`{"password":`), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewStore(path)
	if _, err := store.Get("password"); err == nil {
		t.Fatal("Get should return corrupt JSON error")
	}
	if err := store.Set("password", "secret"); err == nil {
		t.Fatal("Set should not overwrite a corrupt store")
	}
	if err := store.Update(map[string]string{"token": "secret"}); err == nil {
		t.Fatal("Update should not overwrite a corrupt store")
	}
}

func TestStoreMissingSecretReturnsEmptyValue(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "missing.json"))

	got, err := store.Get("password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("Get = %q, want empty value", got)
	}
}

func TestStoreUpdateSetsAndDeletesValuesInOneWrite(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI-backed secret writes are Windows-only")
	}
	store := NewStore(filepath.Join(t.TempDir(), "secrets.json"))
	if err := store.Update(map[string]string{
		"token":   "secret-token",
		"user_id": "42",
	}); err != nil {
		t.Fatal(err)
	}

	token, err := store.Get("token")
	if err != nil {
		t.Fatal(err)
	}
	if token != "secret-token" {
		t.Fatalf("token = %q", token)
	}
	userID, err := store.Get("user_id")
	if err != nil {
		t.Fatal(err)
	}
	if userID != "42" {
		t.Fatalf("user_id = %q", userID)
	}

	if err := store.Update(map[string]string{
		"token":   "",
		"user_id": "43",
	}); err != nil {
		t.Fatal(err)
	}
	token, err = store.Get("token")
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		t.Fatalf("deleted token = %q", token)
	}
	userID, err = store.Get("user_id")
	if err != nil {
		t.Fatal(err)
	}
	if userID != "43" {
		t.Fatalf("updated user_id = %q", userID)
	}
}

func TestStoreUpdateDoesNotWritePartialValuesWhenProtectFails(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI-backed secret writes are Windows-only")
	}
	path := filepath.Join(t.TempDir(), "secrets.json")
	store := NewStore(path)
	if err := store.Set("existing", "keep"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	oldProtect := protectSecret
	t.Cleanup(func() { protectSecret = oldProtect })
	calls := 0
	protectSecret = func(data []byte) ([]byte, error) {
		calls++
		if calls == 1 {
			return oldProtect(data)
		}
		return nil, errors.New("protect failed")
	}
	err = store.Update(map[string]string{
		"token":   "secret-token",
		"user_id": "42",
	})
	if err == nil {
		t.Fatal("Update should return protect error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed Update wrote partial secret data")
	}

	existing, err := store.Get("existing")
	if err != nil {
		t.Fatal(err)
	}
	if existing != "keep" {
		t.Fatalf("existing = %q", existing)
	}
}
