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

// --- Process-wide single-flight for vendor refresh exchanges ---------------
//
// Threat note. This mutex map used to be a `var mu sync.Mutex` declared
// INSIDE NewStoreOAuthTokenSource, i.e. one mutex per constructed closure —
// and a fresh closure is constructed on every CreateProviderFromConfig (each
// agent instance, each loop rebuild, the voice transcriber) and on every
// GET /providers/{id}/sign-in/status poll from the SPA. Two of those hold two
// different mutexes, so they could run the refresh exchange concurrently with
// the SAME refresh token. OpenAI ROTATES refresh tokens: the first exchange
// consumes it, the second presents an already-consumed token, fails
// invalid_grant, and last-write-wins decided which credential survived. The
// realistic outcome was a stored credential whose refresh token was dead —
// a "needs sign-in" the operator could only clear by signing in again.
//
// Keying on the credential-store ENTRY NAME (not the provider id) is what
// makes the single-flight correct: openai-chatgpt and any future
// OpenAI-family row share one stored vendor identity (OAuthVendorID), so they
// must share one lock. The map is bounded by the number of OAuth vendors that
// have ever been used in this process — a handful — so entries are never
// evicted; a mutex is 8 bytes.
var (
	oauthRefreshLocksMu sync.Mutex
	oauthRefreshLocks   = make(map[string]*sync.Mutex)
)

// oauthRefreshLock returns the process-wide mutex for one credential-store
// entry, creating it on first use.
func oauthRefreshLock(entryName string) *sync.Mutex {
	oauthRefreshLocksMu.Lock()
	defer oauthRefreshLocksMu.Unlock()
	mu, ok := oauthRefreshLocks[entryName]
	if !ok {
		mu = &sync.Mutex{}
		oauthRefreshLocks[entryName] = mu
	}
	return mu
}

// --- Sensitive-value registration seam -------------------------------------
//
// ADR-068 FR-046 requires every stored OAuth token to be scrubbed from LLM
// output, logs and audit. The gateway registers them at boot
// (bootCredentials) and after each sign-in / status poll, but the AGENT-path
// refresh below mints a NEW access+refresh pair mid-turn: without this seam
// the scrubber kept protecting the OLD, superseded token while the live one
// travelled unprotected until the next boot, sign-in or status call.
//
// pkg/providers has no handle on *config.Config (that is the whole reason the
// gap existed), so the registration is a narrow hook the gateway installs
// once at boot — see SetSensitiveValueRegistrar. It stays a no-op in every
// other build and in tests that do not install one.

var (
	sensitiveRegistrarMu sync.RWMutex
	// sensitiveRegistrar defaults to a no-op so the refresh path never has to
	// nil-check a security control it cannot itself provide.
	sensitiveRegistrar = func(_ ...string) {}
)

// SetSensitiveValueRegistrar installs the callback invoked with every newly
// minted OAuth token after a successful agent-path refresh. Called once at
// gateway boot (pkg/gateway/gateway.go), where a *config.Config is in scope.
//
// The callback must treat its arguments as ADDITIONS: config's
// RegisterSensitiveValues has "replace with the complete current set"
// semantics, so the gateway's implementation recomputes the full set rather
// than registering these values alone. Passing nil restores the no-op.
func SetSensitiveValueRegistrar(fn func(values ...string)) {
	sensitiveRegistrarMu.Lock()
	defer sensitiveRegistrarMu.Unlock()
	if fn == nil {
		sensitiveRegistrar = func(_ ...string) {}
		return
	}
	sensitiveRegistrar = fn
}

// registerSensitiveValues hands freshly minted tokens to the installed
// registrar. Empty values are dropped so a vendor response missing a rotated
// refresh token cannot register "" as a secret.
func registerSensitiveValues(values ...string) {
	nonEmpty := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			nonEmpty = append(nonEmpty, v)
		}
	}
	if len(nonEmpty) == 0 {
		return
	}
	sensitiveRegistrarMu.RLock()
	fn := sensitiveRegistrar
	sensitiveRegistrarMu.RUnlock()
	fn(nonEmpty...)
}

// NewStoreOAuthTokenSource returns a token-source closure — the shape
// CodexProvider.tokenSource expects, func() (token, accountID string, err
// error) — backed by the encrypted credential store's
// "<OAuthVendorID(providerID)>_OAUTH" entry. It refreshes the stored token
// when within 5 minutes of expiry, single-flighted PROCESS-WIDE per stored
// vendor entry (oauthRefreshLock, not a per-closure mutex — see the threat
// note there) so that however many token sources exist, one vendor refresh
// call happens rather than N racing ones, and persists the refreshed result
// before returning it (ADR-068 FR-046).
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

	return func() (string, string, error) {
		// Held across the vendor exchange, which is why that exchange must be
		// bounded: auth.RefreshAccessToken carries an explicit HTTP timeout
		// (auth.OAuthProviderConfig.Timeout, defaulted by the auth package),
		// so the worst case is one bounded stall, never a permanent wedge.
		mu := oauthRefreshLock(entryName)
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

		// Compare-and-swap against a lost update. The lock above excludes
		// every other refresh IN THIS PROCESS, but not the sign-in handlers
		// (which write this same entry without taking it) nor a second
		// Omnipus process sharing the store. If either installed a newer
		// usable credential while our exchange was in flight, theirs wins:
		// overwriting a just-completed sign-in with our older exchange's
		// result is exactly the lost update that leaves a dead refresh token
		// on disk.
		if latest, rerr := readStoreOAuthCred(store, entryName); rerr == nil &&
			latest != nil &&
			latest.AccessToken != cred.AccessToken &&
			latest.AccessToken != refreshed.AccessToken &&
			!needsOAuthRefresh(latest.ExpiresAt) {
			// Register it too. When the newer credential came from THIS
			// process's sign-in handler it is already known to the scrubber,
			// but when it came from a second Omnipus process sharing the
			// store nothing here has ever seen it — and we are about to hand
			// it to the LLM transport. ADR-068 FR-046 does not care which
			// process minted the token.
			registerSensitiveValues(latest.AccessToken, latest.RefreshToken)
			return latest.AccessToken, latest.AccountID, nil
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
		// ADR-068 FR-046: the pair that just replaced the stored one must be
		// scrubbed from LLM output, logs and audit from here on. Registered
		// AFTER the write so the gateway's registrar, which recomputes the
		// complete set from the store, sees the same credential we return.
		registerSensitiveValues(newCred.AccessToken, newCred.RefreshToken)
		return newCred.AccessToken, newCred.AccountID, nil
	}
}

// PeekedOAuthCred is the subset of a stored device-code OAuth credential a
// cheap, local status check needs — never the tokens themselves (those never
// leave NewStoreOAuthTokenSource's closure).
type PeekedOAuthCred struct {
	AccountID string
	ExpiresAt time.Time
}

// PeekStoreOAuthCred reads providerID's stored "<vendor>_OAUTH" credential
// with NO refresh attempt and NO network call — unlike
// NewStoreOAuthTokenSource's closure, which refreshes an expiring token
// against the vendor. It exists for callers that need to answer "is this row
// signed in" as cheaply as possible, e.g. GET /providers' list render
// (ADR-068 T068-14 gap fix), which must never fan out to a vendor for every
// configured row. Returns (nil, nil) when nothing is stored for this
// provider — "not signed in" is not an error here, mirroring
// readStoreOAuthCred's own convention.
func PeekStoreOAuthCred(providerID string, store *credentials.Store) (*PeekedOAuthCred, error) {
	entryName := credentials.OAuthEntryName(OAuthVendorID(providerID))
	cred, err := readStoreOAuthCred(store, entryName)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, nil
	}
	return &PeekedOAuthCred{AccountID: cred.AccountID, ExpiresAt: cred.ExpiresAt}, nil
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
