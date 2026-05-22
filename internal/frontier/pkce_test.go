package frontier

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewPKCE_Shape(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	if p.Method != "S256" {
		t.Errorf("Method = %q, want S256", p.Method)
	}
	// RFC 7636 §4.1: verifier is 43–128 chars, base64url unreserved set.
	if len(p.Verifier) < 43 || len(p.Verifier) > 128 {
		t.Errorf("Verifier length %d outside [43,128]", len(p.Verifier))
	}
	if strings.ContainsAny(p.Verifier, "+/=") {
		t.Errorf("Verifier %q contains non-url-safe chars", p.Verifier)
	}
	// Challenge must equal base64url(sha256(verifier)).
	sum := sha256.Sum256([]byte(p.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if p.Challenge != want {
		t.Errorf("Challenge mismatch: got %q, want %q", p.Challenge, want)
	}
}

func TestNewPKCE_FreshEachCall(t *testing.T) {
	a, _ := NewPKCE()
	b, _ := NewPKCE()
	if a.Verifier == b.Verifier {
		t.Error("two PKCE calls produced the same verifier — entropy broken")
	}
}

func TestNewState_Shape(t *testing.T) {
	s, err := NewState()
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if len(s) < 16 {
		t.Errorf("state too short: %d chars", len(s))
	}
	if strings.ContainsAny(s, "+/=") {
		t.Errorf("state %q contains non-url-safe chars", s)
	}
	t2, _ := NewState()
	if s == t2 {
		t.Error("two states identical — entropy broken")
	}
}
