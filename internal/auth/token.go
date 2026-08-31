// Package auth manages the shared-secret token used to authenticate every
// request to the server's /api/* endpoints and /ws WebSocket upgrade.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// tokenFileName is the file under ~/.claude/ holding the shared secret.
const tokenFileName = "claude-remote-token"

// validTokenRE matches exactly 64 hex characters (32 bytes hex-encoded).
var validTokenRE = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// tokenFilePath overrides the default token location. It is empty in
// production and only set by tests for isolation.
var tokenFilePath string

// tokenPath resolves the token file location. It mirrors the home-dir
// resolution used by internal/hooks (os.UserHomeDir() + .claude).
func tokenPath() (string, error) {
	if tokenFilePath != "" {
		return tokenFilePath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("auth: could not resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude", tokenFileName), nil
}

// LoadOrCreateToken returns the shared-secret API token stored in
// %USERPROFILE%\.claude\claude-remote-token. If the file is missing or does
// not contain a valid 64-char hex token, a new one is generated from 32
// crypto-random bytes and persisted with perm 0600.
func LoadOrCreateToken() (string, error) {
	path, err := tokenPath()
	if err != nil {
		return "", err
	}
	return loadOrCreateTokenAt(path)
}

// loadOrCreateTokenAt implements LoadOrCreateToken against an explicit path.
func loadOrCreateTokenAt(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(data)); validTokenRE.MatchString(tok) {
			return tok, nil
		}
	}
	// Missing or invalid file: generate a fresh token and overwrite it.

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: failed to generate token: %w", err)
	}
	tok := hex.EncodeToString(raw)

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("auth: failed to create token directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(tok), 0600); err != nil {
		return "", fmt.Errorf("auth: failed to write token file %s: %w", path, err)
	}
	return tok, nil
}
