package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/auth"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// DefaultCredentialStore is the shared, unlocked encrypted credential store
// used to construct the openai-chatgpt (and, once configured, xai)
// store-backed OAuth token source in CreateProviderFromConfig's protocol
// switch. Wired once at gateway boot (pkg/gateway/gateway.go's
// bootCredentials, mirroring this package's own getCredential var for the
// legacy plaintext auth store in factory.go) via SetDefaultCredentialStore
// — CreateProviderFromConfig's many call sites (pkg/agent/instance.go,
// loop.go, pkg/voice/audio_model_transcriber.go) do not thread a
// *credentials.Store through, so this is the seam. Nil until boot wires it
// (e.g. in a Go test that never calls SetDefaultCredentialStore); the
// openai-chatgpt case fails closed with a clear error rather than a
// nil-pointer panic — see CreateProviderFromConfig.
var DefaultCredentialStore *credentials.Store

// SetDefaultCredentialStore wires the shared credential store used by
// device-code provider dispatch. Called once at gateway boot.
func SetDefaultCredentialStore(store *credentials.Store) {
	DefaultCredentialStore = store
}

// ErrProviderNeedsSignIn is the sentinel a store-OAuth token source returns
// when it cannot produce a usable access token — nothing is stored yet, or a
// refresh attempt failed and there is no way to recover without the operator
// signing in again. pkg/agent's error classifier recognizes it via
// errors.Is and maps it to LLMError code "needs_provider", attribution
// "user" (ADR-068 FR-046) — never a silent turn exit (ADR-066 D7).
var ErrProviderNeedsSignIn = errors.New("provider needs sign-in")

// providerNeedsSignInError carries the failing provider id and, when known,
// the underlying cause, while still satisfying errors.Is(err,
// ErrProviderNeedsSignIn) via Unwrap.
type providerNeedsSignInError struct {
	ProviderID string
	Cause      error
}

func (e *providerNeedsSignInError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("provider %s needs sign-in: %v", e.ProviderID, e.Cause)
	}
	return fmt.Sprintf("provider %s needs sign-in", e.ProviderID)
}

func (e *providerNeedsSignInError) Unwrap() error { return ErrProviderNeedsSignIn }

// OAuthVendorID maps a catalog/route provider id to the vendor identity its
// stored OAuth credential is keyed under (ADR-068 FR-007): openai-chatgpt's
// tokens belong to the operator's OpenAI account and are stored under
// "openai", not "openai-chatgpt" — any future OpenAI-family sign-in row
// would share the same stored identity. Every other id (e.g. "xai") is its
// own vendor id.
func OAuthVendorID(providerID string) string {
	if providerID == "openai-chatgpt" {
		return "openai"
	}
	return providerID
}

// storeOAuthCred is the JSON shape persisted under
// credentials.OAuthEntryName(vendorID) in the encrypted credential store —
// ADR-068 FR-007/FR-046. Never config.json, never the vendor's own
// credential file, never serialized back onto any REST/WS wire response.
type storeOAuthCred struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// readStoreOAuthCred returns (nil, nil) when the entry does not exist —
// "not signed in" is not an error to this function's caller, only to a
// token source with no other recovery.
func readStoreOAuthCred(store *credentials.Store, entryName string) (*storeOAuthCred, error) {
	raw, err := store.Get(entryName)
	if err != nil {
		var notFound *credentials.NotFoundError
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, err
	}
	var cred storeOAuthCred
	if err := json.Unmarshal([]byte(raw), &cred); err != nil {
		return nil, fmt.Errorf("parsing stored OAuth credential %q: %w", entryName, err)
	}
	return &cred, nil
}

func writeStoreOAuthCred(store *credentials.Store, entryName string, cred *storeOAuthCred) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("encoding OAuth credential %q: %w", entryName, err)
	}
	return store.Set(entryName, string(data))
}

// needsOAuthRefresh mirrors auth.AuthCredential.NeedsRefresh (5-minute
// lead), applied to a bare expiry rather than a full AuthCredential — a
// zero ExpiresAt (unknown expiry) never triggers a refresh on its own.
func needsOAuthRefresh(expiresAt time.Time) bool {
	if expiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(expiresAt)
}

// NewStoreOAuthTokenSource returns a token-source closure — the shape
// CodexProvider.tokenSource expects, func() (token, accountID string, err
// error) — backed by the encrypted credential store's
// "<OAuthVendorID(providerID)>_OAUTH" entry. It refreshes the stored token
// when within 5 minutes of expiry, single-flighted per token-source
// instance (one mutex per constructed closure) so concurrent turns sharing
// this provider share one vendor refresh call rather than racing it, and
// persists the refreshed result before returning it (ADR-068 FR-046).
//
// Scope note: refresh-on-401 (a live API 401 despite a not-yet-expired
// stored token) is not actively retried here — CodexProvider's Chat() has
// no hook to re-invoke the token source mid-call. The next call to this
// closure re-evaluates NeedsRefresh from the stored expiry as usual; a
// token revoked server-side before its own exp is only caught once its
// natural expiry passes, or the operator signs out/in again.
func NewStoreOAuthTokenSource(
	providerID string, store *credentials.Store, oauthCfg auth.OAuthProviderConfig,
) func() (string, string, error) {
	vendorID := OAuthVendorID(providerID)
	entryName := credentials.OAuthEntryName(vendorID)
	var mu sync.Mutex

	return func() (string, string, error) {
		mu.Lock()
		defer mu.Unlock()

		cred, err := readStoreOAuthCred(store, entryName)
		if err != nil {
			return "", "", fmt.Errorf("loading stored credential for %s: %w", providerID, err)
		}
		if cred == nil {
			return "", "", &providerNeedsSignInError{ProviderID: providerID}
		}

		if !needsOAuthRefresh(cred.ExpiresAt) {
			return cred.AccessToken, cred.AccountID, nil
		}

		if cred.RefreshToken == "" {
			return "", "", &providerNeedsSignInError{ProviderID: providerID}
		}

		refreshed, err := auth.RefreshAccessToken(&auth.AuthCredential{
			AccessToken:  cred.AccessToken,
			RefreshToken: cred.RefreshToken,
			AccountID:    cred.AccountID,
			Provider:     vendorID,
			AuthMethod:   "oauth",
		}, oauthCfg)
		if err != nil {
			return "", "", &providerNeedsSignInError{ProviderID: providerID, Cause: err}
		}

		newCred := &storeOAuthCred{
			AccessToken:  refreshed.AccessToken,
			RefreshToken: refreshed.RefreshToken,
			AccountID:    refreshed.AccountID,
			ExpiresAt:    refreshed.ExpiresAt,
		}
		if err := writeStoreOAuthCred(store, entryName, newCred); err != nil {
			return "", "", fmt.Errorf("saving refreshed credential for %s: %w", providerID, err)
		}
		return newCred.AccessToken, newCred.AccountID, nil
	}
}

// oauthEntrySuffix matches the "_OAUTH" suffix credentials.OAuthEntryName
// appends. Duplicated as a literal (rather than importing credentials just
// for the constant) because both packages already share the format via that
// function; this is a read-side scan, not a producer.
const oauthEntrySuffix = "_OAUTH"

// CollectOAuthSensitiveValues scans the credential store for every stored
// device-code OAuth entry (any "<vendor>_OAUTH" name — CollectOAuthSensitiveValues
// does not need to know which vendors exist) and returns every access and
// refresh token found, so a caller can fold them into
// config.Config.RegisterSensitiveValues's COMPLETE replacement set (ADR-068
// FR-046/security paragraph: these tokens must be scrubbed from LLM output,
// logs and audit exactly like any other credential). Best-effort: a store
// list/read/parse failure on one entry is skipped, not fatal — this must
// never block boot or a sign-in response.
func CollectOAuthSensitiveValues(store *credentials.Store) []string {
	if store == nil {
		return nil
	}
	names, err := store.List()
	if err != nil {
		return nil
	}
	var values []string
	for _, name := range names {
		if !strings.HasSuffix(name, oauthEntrySuffix) {
			continue
		}
		raw, err := store.Get(name)
		if err != nil {
			continue
		}
		var cred storeOAuthCred
		if err := json.Unmarshal([]byte(raw), &cred); err != nil {
			continue
		}
		if cred.AccessToken != "" {
			values = append(values, cred.AccessToken)
		}
		if cred.RefreshToken != "" {
			values = append(values, cred.RefreshToken)
		}
	}
	return values
}
