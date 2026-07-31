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
	"strings"

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
	// CloseToTray keeps the app running in the system tray when the
	// window is closed, instead of quitting. Default false — closing
	// the window quits the whole app (and tears down the backend), which
	// is what most users expect. Only enable this if your desktop's tray
	// reliably shows the icon, otherwise you'll lose the only way back to
	// the window.
	CloseToTray bool `toml:"close_to_tray"`

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
	// InaraAppName overrides the appName sent in the Inara API header.
	// Inara whitelists apps by name — until edcolreport is registered
	// there, uploads from the shipped default name will be rejected
	// with "this application has no access allowed". Users with their
	// own Inara dev relationship can set their registered app name here.
	// Empty defaults to the shipped value.
	InaraAppName string `toml:"inara_app_name"`

	// EdcolonizeEnabled turns on pushing commander-state snapshots to a
	// self-hosted edcolonize instance. Unlike every other destination this
	// reports INWARD — to the player's own box, not a community service — so
	// an AI companion can ground advice in current game state. Default off.
	EdcolonizeEnabled bool `toml:"edcolonize_enabled"`
	// EdcolonizeURL is the full snapshot endpoint, e.g.
	// http://172.16.3.208:3000/api/cmdr/snapshot
	EdcolonizeURL string `toml:"edcolonize_url"`
	// EdcolonizeToken is the shared secret; must match the receiving
	// instance's CMDR_INGEST_TOKEN.
	EdcolonizeToken string `toml:"edcolonize_token"`
}

// Environment variables that override the edcolonize push settings. The
// config file remains the primary mechanism — it's what the Settings GUI
// writes, and it keeps this destination consistent with the others — but the
// endpoint and token are deployment facts rather than user preferences, so
// they can also come from the environment. An env var wins when set and
// non-empty, and setting a URL is enough to enable the destination.
const (
	EnvEdcolonizeURL   = "EDCOLONIZE_URL"
	EnvEdcolonizeToken = "EDCOLONIZE_TOKEN"
)

// applyEdcolonizeEnv overlays the environment onto a loaded config.
func applyEdcolonizeEnv(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv(EnvEdcolonizeURL)); v != "" {
		cfg.EdcolonizeURL = v
		// Pointing the reporter at a URL is an unambiguous request to use it;
		// requiring a second opt-in flag in a different place would be a
		// papercut for a headless or scripted setup.
		cfg.EdcolonizeEnabled = true
	}
	if v := strings.TrimSpace(os.Getenv(EnvEdcolonizeToken)); v != "" {
		cfg.EdcolonizeToken = v
	}
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
		// No config file is a legitimate headless setup: the edcolonize push
		// target can be fully specified by environment alone.
		applyEdcolonizeEnv(&cfg)
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
	// Environment overlays the file. Applied here rather than in LoadFrom so
	// LoadFrom stays hermetic for tests.
	applyEdcolonizeEnv(&cfg)
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
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("encode toml: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
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
