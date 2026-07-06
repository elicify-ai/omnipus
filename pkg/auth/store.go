package auth

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

type AuthCredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Provider     string    `json:"provider"`
	AuthMethod   string    `json:"auth_method"`
	Email        string    `json:"email,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
}

type AuthStore struct {
	Credentials map[string]*AuthCredential `json:"credentials"`
}

func (c *AuthCredential) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

func (c *AuthCredential) NeedsRefresh() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(c.ExpiresAt)
}

func authFilePath() string {
	if home := os.Getenv(config.EnvHome); home != "" {
		return filepath.Join(home, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Log at Warn but continue with empty home; the caller will get a bad path
		// rather than a panic. This matches the graceful-degradation policy.
		slog.Warn("auth: UserHomeDir failed; auth file path may be incorrect", "error", err)
	}
	return filepath.Join(home, pkg.DefaultOmnipusHome, "auth.json")
}

func LoadStore() (*AuthStore, error) {
	path := authFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &AuthStore{Credentials: make(map[string]*AuthCredential)}, nil
		}
		return nil, err
	}

	// Treat an empty file (e.g. newly created by WithFlock) as an empty store.
	if len(data) == 0 {
		return &AuthStore{Credentials: make(map[string]*AuthCredential)}, nil
	}

	var store AuthStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	if store.Credentials == nil {
		store.Credentials = make(map[string]*AuthCredential)
	}
	return &store, nil
}

func SaveStore(store *AuthStore) error {
	path := authFilePath()
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

func GetCredential(provider string) (*AuthCredential, error) {
	store, err := LoadStore()
	if err != nil {
		return nil, err
	}
	cred, ok := store.Credentials[provider]
	if !ok {
		return nil, nil
	}
	return cred, nil
}

func SetCredential(provider string, cred *AuthCredential) error {
	path := authFilePath()
	// Ensure the directory exists before trying to open the file for flock.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fileutil.WithFlock(path, func() error {
		store, err := LoadStore()
		if err != nil {
			return err
		}
		store.Credentials[provider] = cred
		return SaveStore(store)
	})
}

func DeleteCredential(provider string) error {
	store, err := LoadStore()
	if err != nil {
		return err
	}
	delete(store.Credentials, provider)
	return SaveStore(store)
}

func DeleteAllCredentials() error {
	path := authFilePath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
