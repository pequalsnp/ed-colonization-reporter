package frontier

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFileTokenStore_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "tokens.json")
	store := NewFileTokenStore(path)
	if _, err := store.Load(); !errors.Is(err, ErrNoTokens) {
		t.Errorf("fresh Load = %v, want ErrNoTokens", err)
	}

	want := &Tokens{
		AccessToken:  "AT",
		RefreshToken: "RT",
		TokenType:    "Bearer",
		ExpiresIn:    14400,
		Scope:        "auth capi",
		ExpiresAt:    time.Date(2026, 5, 22, 16, 0, 0, 0, time.UTC),
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Errorf("roundtrip lost tokens: %+v", got)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
}

func TestFileTokenStore_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "tokens.json")
	store := NewFileTokenStore(path)
	if err := store.Save(&Tokens{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Refresh tokens are credentials — only the user should be able to read them.
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
}

func TestFileTokenStore_Clear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	store := NewFileTokenStore(path)
	if err := store.Save(&Tokens{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still exists after Clear: %v", err)
	}
	// Second Clear is a no-op.
	if err := store.Clear(); err != nil {
		t.Errorf("Clear on missing file: %v", err)
	}
}

func TestFileTokenStore_AtomicNoLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	store := NewFileTokenStore(path)
	if err := store.Save(&Tokens{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "tokens.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only tokens.json after Save; got %v", names)
	}
}

func TestMemoryTokenStore(t *testing.T) {
	store := &MemoryTokenStore{}
	if _, err := store.Load(); !errors.Is(err, ErrNoTokens) {
		t.Errorf("fresh Load = %v", err)
	}
	if err := store.Save(&Tokens{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "x" {
		t.Errorf("got %+v", got)
	}
	// Returned tokens must be a copy — mutating shouldn't leak.
	got.AccessToken = "mutated"
	got2, _ := store.Load()
	if got2.AccessToken != "x" {
		t.Errorf("MemoryTokenStore returned a shared reference; got %+v", got2)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNoTokens) {
		t.Errorf("after Clear: %v", err)
	}
}
