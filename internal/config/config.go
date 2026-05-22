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
	// ReplaySession causes the tailer to start from the beginning of the
	// most recent journal file on startup instead of seeking to end. Useful
	// after a restart mid-session so we re-report depot states the game
	// has already logged. Defaults to false.
	ReplaySession bool `toml:"replay_session"`
	// ProjectsPollSeconds is how often the GUI refreshes the active
	// project list from ravencolonial. Range [15, 600]; 0 means use
	// the default of 60s. Faster polling chews more network and
	// hammers the upstream; slower means the UI lags behind website
	// edits longer.
	ProjectsPollSeconds int `toml:"projects_poll_seconds"`
	// StartMinimized hides the main window on launch. The app still
	// runs in the system tray; click the tray icon to surface it.
	// Useful when the app is in autostart and you don't want it to
	// steal focus during login.
	StartMinimized bool `toml:"start_minimized"`

	// EDDNEnabled turns on uploads to the EDDN community data network
	// (https://eddn.edcd.io). Default off so users opt in explicitly.
	EDDNEnabled bool `toml:"eddn_enabled"`
	// EDDNTestMode points the EDDN uploader at the beta network and
	// appends `/test` to every schemaRef so messages are validated but
	// not broadcast. Developer/staging toggle.
	EDDNTestMode bool `toml:"eddn_test_mode"`

	// EDSMEnabled and EDSMAPIKey control journal uploads to EDSM
	// (https://www.edsm.net). API key from https://www.edsm.net/en/settings/api.
	EDSMEnabled bool   `toml:"edsm_enabled"`
	EDSMAPIKey  string `toml:"edsm_api_key"`

	// InaraEnabled and InaraAPIKey control uploads to Inara
	// (https://inara.cz). API key from https://inara.cz/settings-api/.
	InaraEnabled bool   `toml:"inara_enabled"`
	InaraAPIKey  string `toml:"inara_api_key"`

	// FrontierClientID overrides the shipped default OAuth client_id —
	// useful if Frontier ever revokes the default. Leave empty to use
	// the baked-in value.
	FrontierClientID string `toml:"frontier_client_id"`
	// FrontierCAPIEnabled controls whether we poll the cAPI /fleetcarrier
	// endpoint and use its data as the FC inventory ground-truth. Off by
	// default so users opt in to OAuth.
	FrontierCAPIEnabled bool `toml:"frontier_capi_enabled"`
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
