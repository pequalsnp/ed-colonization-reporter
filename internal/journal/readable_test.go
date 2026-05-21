package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsJournalDirReadable_Existing(t *testing.T) {
	dir := t.TempDir()
	if err := IsJournalDirReadable(dir); err != nil {
		t.Errorf("empty existing dir should be readable; got %v", err)
	}
}

func TestIsJournalDirReadable_NotExisting(t *testing.T) {
	if err := IsJournalDirReadable(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestIsJournalDirReadable_NotADirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := IsJournalDirReadable(path); err == nil {
		t.Error("expected error when path is a file")
	}
}
