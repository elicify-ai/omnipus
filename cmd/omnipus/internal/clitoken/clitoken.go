// Package clitoken manages the dedicated `cli` principal and its token file.
//
// The `cli` user is a machine-only admin entry in Gateway.Users whose
// plaintext bearer token lives at $OMNIPUS_HOME/cli.token (mode 0600).
// The CLI reads the plaintext token from that file and sends it as a WS
// auth frame; the gateway verifies it via bcrypt just like any other user.
//
// The three lifecycle operations are:
//   - EnsureCLIToken — create-if-absent (called by start/onboard).
//   - ResetCLIToken  — always regenerate (called by start --new-cli-token).
//   - LoadCLIToken   — read plaintext from disk (called before each WS run).
package clitoken

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// ErrNoCLIToken is returned by LoadCLIToken when the token file does not
// exist. Callers should guide the user to run `omnipus start` or `omnipus
// onboard` to create it.
var ErrNoCLIToken = errors.New("cli.token not found — run: omnipus start")

// CLITokenPath returns the canonical path of the CLI token file.
func CLITokenPath(home string) string {
	return filepath.Join(home, "cli.token")
}

// EnsureCLIToken creates the `cli` principal and its token file if they do
// not already exist. It is idempotent: when both the file and the `cli`
// Gateway.Users entry are present it returns (false, nil) without mutating
// anything.
//
// On creation it:
//  1. Generates a fresh bearer token ("omnipus_" + 64 hex chars).
//  2. Upserts a `cli` user entry (role: "admin", token_hash: bcrypt(token))
//     into the gateway.users array in config.json via an atomic map-mutate +
//     WriteFileAtomic (0600).
//  3. Writes the plaintext token to $home/cli.token (mode 0600).
//
// The plaintext token is never written to stdout, stderr, or any log.
func EnsureCLIToken(home string) (created bool, err error) {
	tokenPath := CLITokenPath(home)
	configPath := filepath.Join(home, "config.json")

	// Check if both the file and the config entry exist (fully provisioned).
	if _, statErr := os.Stat(tokenPath); statErr == nil {
		// File exists — check config has a cli user with a token_hash.
		if hasCLIUser(configPath) {
			return false, nil
		}
		// File exists but config missing the cli user — fall through to (re)create.
	}

	return true, mintAndPersist(configPath, tokenPath)
}

// ResetCLIToken always generates a new token regardless of whether one
// already exists. The old token immediately stops authenticating because the
// bcrypt hash in config.json is replaced.
func ResetCLIToken(home string) error {
	configPath := filepath.Join(home, "config.json")
	tokenPath := CLITokenPath(home)
	return mintAndPersist(configPath, tokenPath)
}

// warnPermissiveTokenFile checks the file permissions of the token at path and
// writes a warning to w when the file is group- or world-readable (i.e. the
// permission bits beyond owner are non-zero). The warning is advisory — it
// does not prevent the token from being returned. Callers may pass os.Stderr
// for w. In tests, pass a *bytes.Buffer to capture the output.
func warnPermissiveTokenFile(path string, w io.Writer) {
	info, err := os.Stat(path)
	if err != nil {
		// Cannot stat — skip the warning; the caller handles the missing-file error.
		return
	}
	if info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(w,
			"warning: cli.token is group/world-readable (mode %o); run: chmod 600 %s\n",
			info.Mode().Perm(), path,
		)
	}
}

// LoadCLIToken reads the plaintext bearer token from $home/cli.token.
// Returns ErrNoCLIToken when the file is absent.
//
// If the token file has group- or world-readable permissions (mode bits beyond
// owner are non-zero), a warning is printed to os.Stderr before the token is
// returned (MIN-2). The warning is advisory — the token is still returned.
func LoadCLIToken(home string) (string, error) {
	tokenPath := CLITokenPath(home)
	warnPermissiveTokenFile(tokenPath, os.Stderr)
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoCLIToken
		}
		return "", fmt.Errorf("read cli.token: %w", err)
	}
	return strings.TrimRight(string(data), "\n\r"), nil
}

// mintAndPersist generates a fresh token and writes it to both config.json
// and cli.token. It is called by EnsureCLIToken and ResetCLIToken.
func mintAndPersist(configPath, tokenPath string) error {
	token, err := generateBearerToken()
	if err != nil {
		return fmt.Errorf("generate cli token: %w", err)
	}
	tokenHash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash cli token: %w", err)
	}

	if err := upsertCLIUser(configPath, string(tokenHash)); err != nil {
		return fmt.Errorf("update config.json: %w", err)
	}

	if err := fileutil.WriteFileAtomic(tokenPath, []byte(token), 0o600); err != nil {
		return fmt.Errorf("write cli.token: %w", err)
	}
	return nil
}

// hasCLIUser reports whether config.json contains a gateway.users entry with
// username=="cli" that has a non-empty token_hash.
func hasCLIUser(configPath string) bool {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	gatewayMap, _ := m["gateway"].(map[string]any)
	if gatewayMap == nil {
		return false
	}
	users, _ := gatewayMap["users"].([]any)
	for _, u := range users {
		um, ok := u.(map[string]any)
		if !ok {
			continue
		}
		if um["username"] == "cli" {
			th, _ := um["token_hash"].(string)
			return th != ""
		}
	}
	return false
}

// upsertCLIUser reads config.json, upserts the `cli` user entry with the
// given bcrypt tokenHash, and writes it back atomically (mode 0600).
// It mirrors the map-mutate pattern used in onboard.mutateConfigFile.
func upsertCLIUser(configPath, tokenHash string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config.json: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return fmt.Errorf("parse config.json: %w", unmarshalErr)
	}

	gatewayMap, _ := m["gateway"].(map[string]any)
	if gatewayMap == nil {
		gatewayMap = map[string]any{}
	}
	users, _ := gatewayMap["users"].([]any)
	if users == nil {
		users = []any{}
	}

	updated := false
	for i, u := range users {
		um, ok := u.(map[string]any)
		if !ok {
			continue
		}
		if um["username"] == "cli" {
			um["token_hash"] = tokenHash
			um["role"] = "admin"
			// Clear any legacy tokens set — the single token_hash is the
			// canonical field for the cli principal.
			delete(um, "tokens")
			users[i] = um
			updated = true
			break
		}
	}
	if !updated {
		users = append(users, map[string]any{
			"username":   "cli",
			"token_hash": tokenHash,
			"role":       "admin",
		})
	}

	gatewayMap["users"] = users
	m["gateway"] = gatewayMap

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize config.json: %w", err)
	}
	return fileutil.WriteFileAtomic(configPath, out, 0o600)
}

// GenerateBearerToken returns a new random bearer token of the form
// "omnipus_<64 hex chars>" (256 bits of entropy). It is exported so that
// onboard.go and any other CLI packages can share a single implementation
// rather than duplicating the logic.
func GenerateBearerToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "omnipus_" + hex.EncodeToString(b), nil
}

// generateBearerToken is the package-private alias used internally.
func generateBearerToken() (string, error) { return GenerateBearerToken() }
