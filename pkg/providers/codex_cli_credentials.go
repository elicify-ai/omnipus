package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CodexHomeEnvVar is the environment variable that overrides the Codex CLI
// home directory when resolving the codex auth.json credentials file.
// Default: ~/.codex
const CodexHomeEnvVar = "CODEX_HOME"

// CodexCliAuth represents the subset of the ~/.codex/auth.json file structure
// that Omnipus reads. Only tokens.access_token and tokens.account_id are
// modelled (ADR-068 FR-007): refresh_token is deliberately absent — Omnipus
// never refreshes, writes or proxies the vendor credential file; a session
// ends at expiry and needs `codex login`.
type CodexCliAuth struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

// ReadCodexCliCredentials reads the access token and account id from the Codex
// CLI's auth.json file. The file is read-only to Omnipus (FR-007).
// Expiry is estimated as file modification time + 1 hour (same approach as moltbot).
func ReadCodexCliCredentials() (accessToken, accountID string, expiresAt time.Time, err error) {
	authPath, err := resolveCodexAuthPath()
	if err != nil {
		return "", "", time.Time{}, err
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("reading %s: %w", authPath, err)
	}

	var auth CodexCliAuth
	if err = json.Unmarshal(data, &auth); err != nil {
		return "", "", time.Time{}, fmt.Errorf("parsing %s: %w", authPath, err)
	}

	if auth.Tokens.AccessToken == "" {
		return "", "", time.Time{}, fmt.Errorf("no access_token in %s", authPath)
	}

	stat, err := os.Stat(authPath)
	if err != nil {
		expiresAt = time.Now().Add(time.Hour)
	} else {
		expiresAt = stat.ModTime().Add(time.Hour)
	}

	return auth.Tokens.AccessToken, auth.Tokens.AccountID, expiresAt, nil
}

// CreateCodexCliTokenSource creates a token source that reads from ~/.codex/auth.json.
// This allows the existing CodexProvider to reuse Codex CLI credentials.
func CreateCodexCliTokenSource() func() (string, string, error) {
	return func() (string, string, error) {
		token, accountID, expiresAt, err := ReadCodexCliCredentials()
		if err != nil {
			return "", "", fmt.Errorf("reading codex cli credentials: %w", err)
		}

		if time.Now().After(expiresAt) {
			return "", "", fmt.Errorf(
				"codex cli credentials expired (auth.json last modified > 1h ago). Run: codex login",
			)
		}

		return token, accountID, nil
	}
}

func resolveCodexAuthPath() (string, error) {
	codexHome := os.Getenv(CodexHomeEnvVar)
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting home dir: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	return filepath.Join(codexHome, "auth.json"), nil
}
