package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
)

func TestDefault_HasAPIBaseURL(t *testing.T) {
	c := Default()
	if c.APIBaseURL != ravencolonial.DefaultBaseURL {
		t.Errorf("Default APIBaseURL = %q, want %q", c.APIBaseURL, ravencolonial.DefaultBaseURL)
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdir", "config.toml")
	want := Config{
		JournalDir:        "/home/test/journal",
		APIBaseURL:        "https://example.com",
		APIKey:            "secret-key",
		CommanderOverride: "Alt-Cmdr",
	}
	if err := SaveTo(want, path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	got, existed, err := LoadFrom(path)
	if err != nil || !existed {
		t.Fatalf("LoadFrom: existed=%v, err=%v", existed, err)
	}
	if got != want {
		t.Errorf("roundtrip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadFrom_NonexistentReturnsDefault(t *testing.T) {
	got, existed, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if existed {
		t.Error("existed should be false for missing file")
	}
	if got.APIBaseURL != ravencolonial.DefaultBaseURL {
		t.Errorf("expected default API base URL, got %q", got.APIBaseURL)
	}
}

func TestLoadFrom_FillsAPIBaseDefaultIfBlank(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	// Save a config with the API URL field blanked.
	if err := os.WriteFile(path, []byte(`journal_dir = "/foo"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, existed, err := LoadFrom(path)
	if err != nil || !existed {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.APIBaseURL != ravencolonial.DefaultBaseURL {
		t.Errorf("expected default URL to be filled in, got %q", got.APIBaseURL)
	}
}

func TestLoadFrom_GarbageReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("this = is not valid TOML ===="), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadFrom(path); err == nil {
		t.Error("expected error from corrupt TOML")
	}
}

func TestPath_ContainsAppName(t *testing.T) {
	p, err := Path()
	if err != nil {
		t.Skipf("no user config dir on this platform: %v", err)
	}
	if !strings.Contains(p, AppName) {
		t.Errorf("path %q missing app name %q", p, AppName)
	}
	if !strings.HasSuffix(p, "config.toml") {
		t.Errorf("path %q should end in config.toml", p)
	}
}

func TestSaveTo_AtomicNoLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := SaveTo(Config{JournalDir: "/x"}, path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// We expect exactly one file: the final config. The temp file should
	// have been renamed away.
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only config.toml, got %v", names)
	}
}
