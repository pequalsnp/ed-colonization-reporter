// Package config persists user settings.
//
// Config is stored as TOML under the user's standard config directory:
//   - Linux/BSD: $XDG_CONFIG_HOME/ed-colonization-reporter/config.toml
//     (defaults to ~/.config/ed-colonization-reporter/config.toml)
//   - macOS:     ~/Library/Application Support/ed-colonization-reporter/config.toml
//   - Windows:   %AppData%\ed-colonization-reporter\config.toml
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
)

// AppName is the on-disk directory name for our config.
const AppName = "ed-colonization-reporter"

// Config holds user-tunable settings.
type Config struct {
	// JournalDir overrides the auto-detected Elite Dangerous journal directory.
	JournalDir string `toml:"journal_dir"`
	// APIBaseURL overrides the ravencolonial API base. Useful when the
	// upstream host changes or for pointing at a self-hosted mirror.
	APIBaseURL string `toml:"api_base_url"`
	// APIKey is the optional rcc-key used for write operations that require
	// auth (Fleet Carrier publishing, system site editing). Not needed for
	// the basic colonization-reporting flow.
	APIKey string `toml:"api_key"`
	// CommanderOverride forces the reporter to attribute contributions to a
	// specific commander name, ignoring the one parsed from the journal.
	// Empty (default) means "use the journal value".
	CommanderOverride string `toml:"commander_override"`
}

// Default returns a Config with the canonical defaults filled in. The defaults
// do not depend on the user's environment; auto-detected fields like the
// journal directory are resolved by the caller at runtime.
func Default() Config {
	return Config{APIBaseURL: ravencolonial.DefaultBaseURL}
}

// Path returns the absolute path to the config file for this OS/user.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, AppName, "config.toml"), nil
}

// Load reads the config from its standard location. If the file does not
// exist, returns the defaults and ok=false (so callers can show a first-run
// message).
func Load() (cfg Config, path string, existed bool, err error) {
	path, err = Path()
	if err != nil {
		return Config{}, "", false, err
	}
	cfg = Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, path, false, nil
	}
	if err != nil {
		return cfg, path, false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Default(), path, true, fmt.Errorf("parse %s: %w", path, err)
	}
	// Fill defaults for any fields the user left blank.
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = ravencolonial.DefaultBaseURL
	}
	return cfg, path, true, nil
}

// Save writes the config to its standard location, creating parent dirs as
// needed. Writes are atomic: we write to a temp file and rename, so a crash
// mid-write can't corrupt the user's config.
func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(cfg, path)
}

// SaveTo writes the config to a specific path. Useful for tests.
func SaveTo(cfg Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(cfg); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("encode toml: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}

// LoadFrom reads a config from a specific path. Used by tests.
func LoadFrom(path string) (Config, bool, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return cfg, false, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Default(), true, err
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = ravencolonial.DefaultBaseURL
	}
	return cfg, true, nil
}
