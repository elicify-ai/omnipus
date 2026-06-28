package clitoken

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// seedMinimalConfig writes a minimal config.json into home so that
// upsertCLIUser has a valid file to read and update.
func seedMinimalConfig(t *testing.T, home string) {
	t.Helper()
	cfg := map[string]any{
		"gateway": map[string]any{
			"port":  5000,
			"users": []any{},
		},
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("seed config marshal: %v", err)
	}
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("seed config write: %v", err)
	}
}

// readConfig reads and parses config.json from home.
func readConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}
	return m
}

// cliUserFromConfig extracts the `cli` user entry from a parsed config map.
// Returns nil if not found.
func cliUserFromConfig(m map[string]any) map[string]any {
	gw, _ := m["gateway"].(map[string]any)
	if gw == nil {
		return nil
	}
	users, _ := gw["users"].([]any)
	for _, u := range users {
		um, ok := u.(map[string]any)
		if !ok {
			continue
		}
		if um["username"] == "cli" {
			return um
		}
	}
	return nil
}

func TestEnsureCLIToken_FreshCreatesUserAndFile(t *testing.T) {
	home := t.TempDir()
	seedMinimalConfig(t, home)

	created, err := EnsureCLIToken(home)
	if err != nil {
		t.Fatalf("EnsureCLIToken: %v", err)
	}
	if !created {
		t.Errorf("expected created=true on fresh home, got false")
	}

	// Token file must exist and be non-empty.
	tokenPath := CLITokenPath(home)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("cli.token stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cli.token perm = %o, want 0600", info.Mode().Perm())
	}
	token, err := LoadCLIToken(home)
	if err != nil {
		t.Fatalf("LoadCLIToken: %v", err)
	}
	if token == "" {
		t.Error("cli.token is empty")
	}

	// Config must contain a `cli` user with a non-empty token_hash and role "admin".
	m := readConfig(t, home)
	user := cliUserFromConfig(m)
	if user == nil {
		t.Fatalf("no cli user found in config.json")
	}
	th, _ := user["token_hash"].(string)
	if th == "" {
		t.Errorf("cli user has empty token_hash")
	}
	role, _ := user["role"].(string)
	if role != "admin" {
		t.Errorf("cli user role = %q, want admin", role)
	}

	// The bcrypt hash must verify against the plaintext token.
	if err := bcrypt.CompareHashAndPassword([]byte(th), []byte(token)); err != nil {
		t.Errorf("token_hash does not match plaintext token: %v", err)
	}
}

func TestEnsureCLIToken_Idempotent(t *testing.T) {
	home := t.TempDir()
	seedMinimalConfig(t, home)

	// First call.
	if _, err := EnsureCLIToken(home); err != nil {
		t.Fatalf("first EnsureCLIToken: %v", err)
	}
	token1, err := LoadCLIToken(home)
	if err != nil {
		t.Fatalf("LoadCLIToken after first: %v", err)
	}
	m1 := readConfig(t, home)
	u1 := cliUserFromConfig(m1)
	if u1 == nil {
		t.Fatal("cli user missing after first call")
	}
	hash1, _ := u1["token_hash"].(string)

	// Second call — must be a no-op.
	created, err := EnsureCLIToken(home)
	if err != nil {
		t.Fatalf("second EnsureCLIToken: %v", err)
	}
	if created {
		t.Errorf("expected created=false on second call, got true")
	}
	token2, err := LoadCLIToken(home)
	if err != nil {
		t.Fatalf("LoadCLIToken after second: %v", err)
	}
	if token1 != token2 {
		t.Errorf("token changed on idempotent call: %q → %q", token1, token2)
	}
	m2 := readConfig(t, home)
	u2 := cliUserFromConfig(m2)
	if u2 == nil {
		t.Fatal("cli user missing after second call")
	}
	hash2, _ := u2["token_hash"].(string)
	if hash1 != hash2 {
		t.Errorf("token_hash changed on idempotent call")
	}
}

func TestResetCLIToken_ChangesTokenAndHash(t *testing.T) {
	home := t.TempDir()
	seedMinimalConfig(t, home)

	// Mint initial token.
	if _, err := EnsureCLIToken(home); err != nil {
		t.Fatalf("EnsureCLIToken: %v", err)
	}
	oldToken, err := LoadCLIToken(home)
	if err != nil {
		t.Fatalf("LoadCLIToken (before reset): %v", err)
	}
	m0 := readConfig(t, home)
	oldHash, _ := cliUserFromConfig(m0)["token_hash"].(string)

	// Reset.
	if resetErr := ResetCLIToken(home); resetErr != nil {
		t.Fatalf("ResetCLIToken: %v", resetErr)
	}
	newToken, err := LoadCLIToken(home)
	if err != nil {
		t.Fatalf("LoadCLIToken (after reset): %v", err)
	}
	m1 := readConfig(t, home)
	newHash, _ := cliUserFromConfig(m1)["token_hash"].(string)

	if newToken == oldToken {
		t.Errorf("token did not change after reset")
	}
	if newHash == oldHash {
		t.Errorf("token_hash did not change after reset")
	}

	// Old token must NOT match the new hash.
	if err := bcrypt.CompareHashAndPassword([]byte(newHash), []byte(oldToken)); err == nil {
		t.Errorf("old plaintext token still matches new hash — reset did not invalidate old token")
	}

	// New token must match the new hash.
	if err := bcrypt.CompareHashAndPassword([]byte(newHash), []byte(newToken)); err != nil {
		t.Errorf("new token_hash does not match new plaintext token: %v", err)
	}
}

func TestLoadCLIToken_ReturnsToken(t *testing.T) {
	home := t.TempDir()
	seedMinimalConfig(t, home)

	if _, err := EnsureCLIToken(home); err != nil {
		t.Fatalf("EnsureCLIToken: %v", err)
	}
	tok, err := LoadCLIToken(home)
	if err != nil {
		t.Fatalf("LoadCLIToken: %v", err)
	}
	if tok == "" {
		t.Error("LoadCLIToken returned empty token")
	}
	// Tokens have the "omnipus_" prefix.
	if len(tok) < 9 || tok[:8] != "omnipus_" {
		t.Errorf("unexpected token format: %q", tok)
	}
}

func TestLoadCLIToken_MissingFile_ReturnsErrNoCLIToken(t *testing.T) {
	home := t.TempDir()
	_, err := LoadCLIToken(home)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrNoCLIToken {
		t.Errorf("expected ErrNoCLIToken, got: %v", err)
	}
}

// TestWarnPermissiveTokenFile_0644EmitsWarning verifies that
// warnPermissiveTokenFile writes a warning when the file mode has group/world
// read bits set (MIN-2). The warning must mention "group/world-readable" and
// "chmod 600". The file is still readable afterwards — the helper does not
// refuse access.
func TestWarnPermissiveTokenFile_0644EmitsWarning(t *testing.T) {
	home := t.TempDir()
	seedMinimalConfig(t, home)
	if _, err := EnsureCLIToken(home); err != nil {
		t.Fatalf("EnsureCLIToken: %v", err)
	}
	tokenPath := CLITokenPath(home)

	// Widen permissions so group/world can read.
	if err := os.Chmod(tokenPath, 0o644); err != nil {
		t.Fatalf("chmod 0644: %v", err)
	}

	var buf bytes.Buffer
	warnPermissiveTokenFile(tokenPath, &buf)
	warning := buf.String()

	if warning == "" {
		t.Error("expected a warning for a 0644 token file, got empty output")
	}
	if !strings.Contains(warning, "group/world-readable") {
		t.Errorf("warning does not mention group/world-readable: %q", warning)
	}
	if !strings.Contains(warning, "chmod 600") {
		t.Errorf("warning does not mention chmod 600: %q", warning)
	}
}

// TestWarnPermissiveTokenFile_0600NoWarning verifies that a correctly-moded
// 0600 file produces no output (the common case must be silent).
func TestWarnPermissiveTokenFile_0600NoWarning(t *testing.T) {
	home := t.TempDir()
	seedMinimalConfig(t, home)
	if _, err := EnsureCLIToken(home); err != nil {
		t.Fatalf("EnsureCLIToken: %v", err)
	}

	var buf bytes.Buffer
	warnPermissiveTokenFile(CLITokenPath(home), &buf)
	if buf.Len() != 0 {
		t.Errorf("unexpected warning for a 0600 file: %q", buf.String())
	}
}

// TestWarnPermissiveTokenFile_MissingFile_NoOutput verifies that
// warnPermissiveTokenFile silently does nothing when the file does not exist
// (the caller handles the missing-file error path in LoadCLIToken).
func TestWarnPermissiveTokenFile_MissingFile_NoOutput(t *testing.T) {
	var buf bytes.Buffer
	warnPermissiveTokenFile("/nonexistent/path/cli.token", &buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output for a missing file, got: %q", buf.String())
	}
}

// TestLoadCLIToken_EmptyFile_ReturnsEmptyString verifies that an empty
// cli.token file does not panic and returns an empty string without error
// (the caller will later fail auth on an empty token, which is the correct
// handling path).
func TestLoadCLIToken_EmptyFile_ReturnsEmptyString(t *testing.T) {
	home := t.TempDir()
	tokenPath := CLITokenPath(home)
	// Write an empty file at the token path.
	if err := os.WriteFile(tokenPath, []byte{}, 0o600); err != nil {
		t.Fatalf("write empty token file: %v", err)
	}
	tok, err := LoadCLIToken(home)
	if err != nil {
		t.Fatalf("LoadCLIToken returned unexpected error for empty file: %v", err)
	}
	if tok != "" {
		t.Errorf("expected empty string from empty token file, got %q", tok)
	}
}

// TestLoadCLIToken_TruncatedFile_ReturnsPartialToken verifies that a token
// file containing only a partial token (no newline, truncated) does not panic;
// the partial content is returned and the caller will receive an auth rejection
// from the gateway (the correct error path, distinct from ErrNoCLIToken).
func TestLoadCLIToken_TruncatedFile_ReturnsPartialToken(t *testing.T) {
	home := t.TempDir()
	tokenPath := CLITokenPath(home)
	partial := []byte("omnipus_abc")
	if err := os.WriteFile(tokenPath, partial, 0o600); err != nil {
		t.Fatalf("write truncated token file: %v", err)
	}
	tok, err := LoadCLIToken(home)
	if err != nil {
		t.Fatalf("LoadCLIToken returned unexpected error for truncated file: %v", err)
	}
	if tok != "omnipus_abc" {
		t.Errorf("expected %q, got %q", "omnipus_abc", tok)
	}
}
