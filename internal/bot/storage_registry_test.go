package bot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadUserStorageRegistryMissingFileStartsEmpty(t *testing.T) {
	registry, err := LoadUserStorageRegistry(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("LoadUserStorageRegistry() error = %v", err)
	}
	if _, ok := registry.Get(777); ok {
		t.Fatal("unexpected storage for unknown user")
	}
}

func TestUserStorageRegistryRegisterPersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "users.json")
	registry, err := LoadUserStorageRegistry(path)
	if err != nil {
		t.Fatalf("LoadUserStorageRegistry() error = %v", err)
	}
	registry.now = func() time.Time {
		return time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	}

	if err := registry.Register(777, 348313485); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	reloaded, err := LoadUserStorageRegistry(path)
	if err != nil {
		t.Fatalf("reload error = %v", err)
	}
	storage, ok := reloaded.Get(777)
	if !ok {
		t.Fatal("reloaded storage not found")
	}
	if reloaded.Count() != 1 {
		t.Fatalf("registered users = %d, want 1", reloaded.Count())
	}
	if storage.UserID != 777 || storage.ChatID != 348313485 {
		t.Fatalf("storage = %#v", storage)
	}
	if !storage.RegisteredAt.Equal(time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("registered_at = %s", storage.RegisteredAt)
	}
}

func TestLoadUserStorageRegistryRejectsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := LoadUserStorageRegistry(path); err == nil {
		t.Fatal("LoadUserStorageRegistry() error = nil, want decode error")
	}
}
