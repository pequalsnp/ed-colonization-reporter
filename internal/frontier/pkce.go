// Package frontier implements the Frontier (Elite Dangerous) OAuth 2.0
// authorization-code-with-PKCE flow and a thin client for Frontier's
// Companion API ("cAPI") at https://companion.orerve.net.
//
// Registration: developer access is granted via https://user.frontierstore.net/
// then an OAuth client is created at https://auth.frontierstore.net/client/signup.
// The client_id is per-user — we don't ship one. PKCE means the client_secret
// is not required for token exchange (RFC 7636), which is critical for a
// desktop app that can't keep secrets.
//
// The MIT-licensed reimplementation follows the protocol documented at
// https://github.com/EDCD/FDevIDs/blob/master/Frontier%20API/FrontierDevelopments-oAuth2-notes.md
// and RFCs 6749 / 7636 / 8252. EDMC's GPL code was consulted as a reference
// only; no code was copied.
package frontier

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCE holds the verifier/challenge pair for one OAuth flow.
type PKCE struct {
	Verifier  string // 43–128 chars base64url, used at token exchange
	Challenge string // SHA-256 of Verifier, base64url, sent at /auth
	Method    string // "S256"
}

// NewPKCE generates a fresh verifier/challenge pair per RFC 7636. The
// verifier is 32 random bytes encoded as base64url-without-padding (43
// chars), well within the spec's [43,128] range.
func NewPKCE() (*PKCE, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("pkce: random: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return &PKCE{Verifier: verifier, Challenge: challenge, Method: "S256"}, nil
}

// NewState generates a fresh random `state` parameter for the OAuth
// authorization request. The state is round-tripped through the browser
// and compared on the callback to defend against CSRF.
func NewState() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("state: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
