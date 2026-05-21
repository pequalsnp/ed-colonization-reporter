package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// redirectUserConfigDir points os.UserConfigDir at a temp directory by
// setting the platform-appropriate env var. Returns the resolved path that
// Path() will compute.
func redirectUserConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
	case "darwin":
		// On macOS, UserConfigDir uses HOME + Library/Application Support.
		// HOME redirect plus creating the parent works.
		t.Setenv("HOME", dir)
		appSupport := filepath.Join(dir, "Library", "Application Support")
		if err := os.MkdirAll(appSupport, 0o755); err != nil {
			t.Fatal(err)
		}
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	return dir
}

func TestPath_HoneresUserConfigDir(t *testing.T) {
	root := redirectUserConfigDir(t)
	p, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	// The path may include OS-specific intermediate dirs, but it must
	// live somewhere under our redirected root.
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "" || rel == ".." {
		t.Errorf("Path %q is not under redirected config root %q", p, root)
	}
	if filepath.Base(p) != "config.toml" {
		t.Errorf("filename = %q, want config.toml", filepath.Base(p))
	}
}

func TestSaveLoad_ViaCanonicalAPI(t *testing.T) {
	redirectUserConfigDir(t)

	want := Config{
		JournalDir:   "/home/x",
		APIBaseURL:   "https://example.com",
		APIKey:       "key",
		EDDNEnabled:  true,
		EDSMEnabled:  true,
		EDSMAPIKey:   "edsm",
		InaraEnabled: true,
		InaraAPIKey:  "inara",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, _, existed, err := Load()
	if err != nil || !existed {
		t.Fatalf("Load: existed=%v err=%v", existed, err)
	}
	if got != want {
		t.Errorf("roundtrip:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoad_NonexistentReturnsDefaults(t *testing.T) {
	redirectUserConfigDir(t)
	got, _, existed, err := Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if existed {
		t.Error("existed should be false on a fresh dir")
	}
	if got != Default() {
		t.Errorf("got %+v, want defaults %+v", got, Default())
	}
}
