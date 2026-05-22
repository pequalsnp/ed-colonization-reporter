package frontier

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// TokenStore persists Tokens to disk between runs. Implementations must
// store atomically and with restrictive permissions — the refresh token is
// equivalent to the user's account password for cAPI purposes.
type TokenStore interface {
	Load() (*Tokens, error)
	Save(*Tokens) error
	Clear() error
}

// ErrNoTokens is returned by Load when there's nothing on disk yet.
var ErrNoTokens = errors.New("frontier: no tokens persisted")

// FileTokenStore writes tokens as JSON to a file with mode 0600. Suitable
// for any user-only-readable directory (XDG_CONFIG_HOME on Linux,
// %AppData% on Windows).
type FileTokenStore struct {
	Path string
	mu   sync.Mutex
}

// NewFileTokenStore builds a store that uses the given file path. The
// parent directory is created (mode 0700) on first Save.
func NewFileTokenStore(path string) *FileTokenStore {
	return &FileTokenStore{Path: path}
}

// Load reads tokens from disk. Returns ErrNoTokens if the file doesn't
// exist (so callers can show a "sign in" prompt rather than treating it
// as an error).
func (s *FileTokenStore) Load() (*Tokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoTokens
	}
	if err != nil {
		return nil, fmt.Errorf("frontier tokens: read: %w", err)
	}
	var tok Tokens
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("frontier tokens: decode: %w", err)
	}
	return &tok, nil
}

// Save atomically writes tokens with mode 0600. Parent dirs are created
// with 0700 if needed.
func (s *FileTokenStore) Save(tok *Tokens) error {
	if tok == nil {
		return errors.New("frontier tokens: nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("frontier tokens: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".tokens-*.json")
	if err != nil {
		return fmt.Errorf("frontier tokens: tempfile: %w", err)
	}
	tmpName := tmp.Name()
	// Tighten permissions immediately — refresh tokens are sensitive.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("frontier tokens: chmod: %w", err)
	}
	if err := json.NewEncoder(tmp).Encode(tok); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("frontier tokens: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("frontier tokens: close: %w", err)
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("frontier tokens: rename: %w", err)
	}
	return nil
}

// Clear removes the token file. No error if it doesn't exist.
func (s *FileTokenStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// MemoryTokenStore keeps tokens in RAM only — used in tests.
type MemoryTokenStore struct {
	mu     sync.Mutex
	tokens *Tokens
}

// Load returns the in-memory tokens or ErrNoTokens.
func (m *MemoryTokenStore) Load() (*Tokens, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens == nil {
		return nil, ErrNoTokens
	}
	cp := *m.tokens
	return &cp, nil
}

// Save overwrites the in-memory tokens.
func (m *MemoryTokenStore) Save(t *Tokens) error {
	if t == nil {
		return errors.New("nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *t
	m.tokens = &cp
	return nil
}

// Clear empties the in-memory tokens.
func (m *MemoryTokenStore) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = nil
	return nil
}
