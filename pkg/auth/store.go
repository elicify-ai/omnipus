package auth

import (
	"time"
)

// AuthCredential is the in-memory OAuth credential record produced by this
// package's device-code and refresh exchanges (oauth.go) and handed to the
// caller, which persists it in the ENCRYPTED credential store
// (pkg/credentials, AES-256-GCM + Argon2id) under
// credentials.OAuthEntryName(vendor).
//
// This type is deliberately transport-only. Until the OAuth-path hardening pass this file also held
// AuthStore / LoadStore / SaveStore / GetCredential / SetCredential /
// DeleteCredential, which serialized these same fields — AccessToken and
// RefreshToken included — as PLAINTEXT JSON to $OMNIPUS_HOME/auth.json. That
// writer had no caller left outside its own tests once the store-OAuth ladder
// was retired, but it stayed exported and compiled in, one call away from
// re-violating CLAUDE.md's "never a plaintext credential file". It is gone;
// there is no plaintext credential path in this package any more, and
// re-adding one needs a deliberate decision rather than an import.
//
// AccessToken/RefreshToken are annotated individually since gosec's G117
// fires per struct field; holding these fields IS this type's purpose.
type AuthCredential struct {
	AccessToken  string    `json:"access_token"`            // #nosec G117 -- designed credential field, see comment above
	RefreshToken string    `json:"refresh_token,omitempty"` // #nosec G117 -- designed credential field, see comment above
	AccountID    string    `json:"account_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Provider     string    `json:"provider"`
	AuthMethod   string    `json:"auth_method"`
	Email        string    `json:"email,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
}

// IsExpired reports whether the credential's own expiry has passed. A zero
// ExpiresAt means "unknown expiry" and is never treated as expired.
func (c *AuthCredential) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

// NeedsRefresh reports whether the credential is within five minutes of its
// own expiry. A zero ExpiresAt (unknown expiry) never triggers a refresh.
func (c *AuthCredential) NeedsRefresh() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(c.ExpiresAt)
}
