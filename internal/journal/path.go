// Package journal locates, parses, and tails Elite Dangerous journal files.
package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// EliteAppID is the Steam app ID for Elite Dangerous, used to locate the
// Proton compatdata prefix on Linux.
const EliteAppID = "359320"

// ErrJournalDirNotFound is returned when no journal directory can be located
// in any of the default locations.
var ErrJournalDirNotFound = errors.New("journal directory not found in any default location")

// DefaultJournalDirs returns the list of directories where the Elite Dangerous
// journal might live for the current OS, in priority order. They are not
// checked for existence; use FindJournalDir for that.
func DefaultJournalDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return defaultJournalDirsForHome(home, runtime.GOOS)
}

func defaultJournalDirsForHome(home, goos string) []string {
	const sub = "Saved Games/Frontier Developments/Elite Dangerous"
	switch goos {
	case "windows":
		return []string{filepath.Join(home, "Saved Games", "Frontier Developments", "Elite Dangerous")}
	case "linux":
		protonSub := filepath.Join("steamapps", "compatdata", EliteAppID, "pfx",
			"drive_c", "users", "steamuser", filepath.FromSlash(sub))
		return []string{
			filepath.Join(home, ".steam", "steam", protonSub),
			filepath.Join(home, ".local", "share", "Steam", protonSub),
			filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", "data", "Steam", protonSub), // Flatpak
		}
	case "darwin":
		// Elite Dangerous is not natively supported on macOS, but a user may
		// run it via CrossOver/Whisky. Don't guess paths — require config.
		return nil
	default:
		return nil
	}
}

// FindJournalDir returns the first existing journal directory from the
// default candidates, or ErrJournalDirNotFound if none exist.
func FindJournalDir() (string, error) {
	for _, dir := range DefaultJournalDirs() {
		if isDir(dir) {
			return dir, nil
		}
	}
	return "", ErrJournalDirNotFound
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// LatestJournalFile returns the path of the most recently modified journal
// file (Journal.*.log) in dir. Returns an error if dir has no journal files.
func LatestJournalFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read journal dir: %w", err)
	}
	var (
		bestPath string
		bestTime int64
	)
	for _, e := range entries {
		if e.IsDir() || !isJournalFilename(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if mt := info.ModTime().UnixNano(); mt > bestTime {
			bestTime = mt
			bestPath = filepath.Join(dir, e.Name())
		}
	}
	if bestPath == "" {
		return "", fmt.Errorf("no Journal.*.log files in %s", dir)
	}
	return bestPath, nil
}

// isJournalFilename reports whether name matches the Journal.*.log pattern
// Frontier uses for journal files.
func isJournalFilename(name string) bool {
	const prefix = "Journal."
	const suffix = ".log"
	if len(name) < len(prefix)+len(suffix) {
		return false
	}
	return name[:len(prefix)] == prefix && name[len(name)-len(suffix):] == suffix
}
