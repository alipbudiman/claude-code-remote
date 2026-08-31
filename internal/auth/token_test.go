package auth

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tokenPathFor returns an isolated token file path inside a temp dir so the
// tests never touch the real ~/.claude/claude-remote-token.
func tokenPathFor(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "claude-remote-token")
}

// assertValidToken fails the test if tok is not a 64-char hex string
// (i.e. 32 bytes hex-encoded).
func assertValidToken(t *testing.T, tok string) {
	t.Helper()
	if len(tok) != 64 {
		t.Fatalf("token length = %d, want 64 (got %q)", len(tok), tok)
	}
	raw, err := hex.DecodeString(tok)
	if err != nil {
		t.Fatalf("token %q is not valid hex: %v", tok, err)
	}
	if len(raw) != 32 {
		t.Fatalf("token decodes to %d bytes, want 32", len(raw))
	}
}

// (a) creates a 64-char hex file when absent
func TestLoadOrCreateTokenCreatesFileWhenAbsent(t *testing.T) {
	path := tokenPathFor(t)

	tok, err := loadOrCreateTokenAt(path)
	if err != nil {
		t.Fatalf("loadOrCreateTokenAt() error = %v", err)
	}
	assertValidToken(t, tok)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("token file was not created: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != tok {
		t.Fatalf("token file contains %q, want %q", got, tok)
	}
}

// (b) returns the same token on second call (idempotent)
func TestLoadOrCreateTokenIsIdempotent(t *testing.T) {
	path := tokenPathFor(t)

	first, err := loadOrCreateTokenAt(path)
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}

	second, err := loadOrCreateTokenAt(path)
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}

	if first != second {
		t.Fatalf("second call returned %q, want same token %q", second, first)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if strings.TrimSpace(string(data)) != first {
		t.Fatalf("token file was rewritten on second call")
	}
}

// (c) deterministic behavior for invalid file content: a new valid token is
// generated and the file is overwritten.
func TestLoadOrCreateTokenReplacesInvalidContent(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"not hex", "this-is-not-a-hex-token"},
		{"too short", "abc123"},
		{"wrong length hex", "0123456789abcdef0123456789abcdef"},
		{"empty", ""},
		{"whitespace only", "   \n\t  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tokenPathFor(t)
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatalf("seeding invalid token file: %v", err)
			}

			tok, err := loadOrCreateTokenAt(path)
			if err != nil {
				t.Fatalf("loadOrCreateTokenAt() error = %v", err)
			}
			assertValidToken(t, tok)

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading token file: %v", err)
			}
			if strings.TrimSpace(string(data)) != tok {
				t.Fatalf("token file not overwritten with new token %q", tok)
			}
		})
	}
}

// LoadOrCreateToken must resolve the default path under ~/.claude/ using the
// same home-dir pattern as internal/hooks (os.UserHomeDir + .claude).
func TestTokenPathDefaultsToClaudeDir(t *testing.T) {
	saved := tokenFilePath
	tokenFilePath = "" // force default resolution
	t.Cleanup(func() { tokenFilePath = saved })

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home directory on this machine: %v", err)
	}

	got, err := tokenPath()
	if err != nil {
		t.Fatalf("tokenPath() error = %v", err)
	}
	want := filepath.Join(home, ".claude", "claude-remote-token")
	if got != want {
		t.Fatalf("tokenPath() = %q, want %q", got, want)
	}
}
