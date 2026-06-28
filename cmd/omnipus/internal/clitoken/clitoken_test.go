package clitoken

import (
	"encoding/json"
	"os"
	"path/filepath"
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
