package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultJournalDirsForHome_Linux(t *testing.T) {
	dirs := defaultJournalDirsForHome("/home/test", "linux")
	if len(dirs) == 0 {
		t.Fatal("expected at least one default dir on linux")
	}
	want := "Saved Games/Frontier Developments/Elite Dangerous"
	for _, d := range dirs {
		if !strings.Contains(d, "Elite Dangerous") {
			t.Errorf("dir %q does not contain Elite Dangerous", d)
		}
		if !strings.Contains(d, EliteAppID) {
			t.Errorf("dir %q does not contain Steam app ID %s", d, EliteAppID)
		}
		if !strings.Contains(filepath.ToSlash(d), want) {
			t.Errorf("dir %q does not contain expected subpath %q", d, want)
		}
	}
}

func TestDefaultJournalDirsForHome_Windows(t *testing.T) {
	dirs := defaultJournalDirsForHome(`C:\Users\test`, "windows")
	if len(dirs) != 1 {
		t.Fatalf("expected 1 windows dir, got %d", len(dirs))
	}
	if !strings.Contains(dirs[0], "Elite Dangerous") {
		t.Errorf("windows dir %q missing Elite Dangerous", dirs[0])
	}
	// Windows path should NOT contain the Proton-prefix steamapps subpath.
	if strings.Contains(dirs[0], "steamapps") {
		t.Errorf("windows dir %q should not reference steamapps", dirs[0])
	}
}

func TestDefaultJournalDirsForHome_UnknownOS(t *testing.T) {
	if got := defaultJournalDirsForHome("/home/test", "plan9"); got != nil {
		t.Errorf("expected nil for unknown OS, got %v", got)
	}
}

func TestIsJournalFilename(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Journal.2026-05-21T123045.01.log", true},
		{"Journal.something.log", true},
		{"Journal.log", false}, // no middle segment
		{"journal.2026.log", false},
		{"Journal.2026.txt", false},
		{"Status.json", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isJournalFilename(tc.name); got != tc.want {
				t.Errorf("isJournalFilename(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestLatestJournalFile(t *testing.T) {
	dir := t.TempDir()

	// Create three journal files with distinct mtimes, plus distractors.
	mustTouch(t, filepath.Join(dir, "Journal.001.log"), time.Now().Add(-3*time.Hour))
	mustTouch(t, filepath.Join(dir, "Journal.002.log"), time.Now().Add(-1*time.Hour))
	mustTouch(t, filepath.Join(dir, "Journal.003.log"), time.Now().Add(-2*time.Hour))
	mustTouch(t, filepath.Join(dir, "Status.json"), time.Now())
	mustTouch(t, filepath.Join(dir, "Cargo.json"), time.Now())

	got, err := LatestJournalFile(dir)
	if err != nil {
		t.Fatalf("LatestJournalFile: %v", err)
	}
	if filepath.Base(got) != "Journal.002.log" {
		t.Errorf("got %q, want Journal.002.log (the newest)", filepath.Base(got))
	}
}

func TestLatestJournalFile_Empty(t *testing.T) {
	dir := t.TempDir()
	if _, err := LatestJournalFile(dir); err == nil {
		t.Error("expected error for empty journal dir")
	}
}

func TestFindJournalDir_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // a clean home with no journal subdirs
	if _, err := FindJournalDir(); err == nil {
		t.Error("expected ErrJournalDirNotFound when no candidate dirs exist")
	}
}

func mustTouch(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}
