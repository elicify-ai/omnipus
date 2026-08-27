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

// OAuthCredential is the caller-facing shape of one stored device-code OAuth
// credential. It exists so the gateway's sign-in handlers persist the entry
// through this package rather than re-declaring the on-disk JSON shape at
// each call site: the poll handler (FR-044) and the codex-import handler
// (FR-047) each carried their own anonymous struct with hand-copied
// `json:"access_token"` tags, three independent copies of one format that
// nothing forced to agree. A tag typo in any one of them would have written
// an entry this package's own reader silently parses as an empty credential.
type OAuthCredential struct {
	AccessToken  string
	RefreshToken string
	AccountID    string
	ExpiresAt    time.Time
}

// WriteStoreOAuthCredential persists cred as providerID's stored
// "<vendor>_OAUTH" entry (ADR-068 FR-007/FR-046) — the single writer of that
// JSON shape outside this file's own refresh path.
//
// It deliberately does NOT take the per-vendor refresh lock. A sign-in write
// racing an in-flight refresh is already resolved correctly, and in the right
// direction, by the compare-and-swap in NewStoreOAuthTokenSource: the refresh
// sees a newer usable credential and returns it instead of overwriting it.
// Taking the lock here would add a bounded-but-real stall to an operator-
// facing sign-in for no correctness gain. Deletion is the case a
// compare-and-swap cannot resolve on its own — see DeleteStoreOAuthCred.
func WriteStoreOAuthCredential(providerID string, store *credentials.Store, cred OAuthCredential) error {
	entryName := credentials.OAuthEntryName(OAuthVendorID(providerID))
	return writeStoreOAuthCred(store, entryName, &storeOAuthCred{
		AccessToken:  cred.AccessToken,
		RefreshToken: cred.RefreshToken,
		AccountID:    cred.AccountID,
		ExpiresAt:    cred.ExpiresAt,
	})
}

// DeleteStoreOAuthCred removes providerID's stored "<vendor>_OAUTH" entry —
// the revocation half of sign-out (ADR-068 FR-048). It returns
// *credentials.NotFoundError verbatim when nothing was stored, so the caller
// can treat "already signed out" as success.
//
// THREAT NOTE — why this is not a bare store.Delete.
//
// The delete runs INSIDE the same process-wide per-vendor refresh lock the
// token source holds across its vendor exchange (oauthRefreshLock). Without
// that, sign-out was not a revocation at all: with the stored token minutes
// from expiry, an agent turn starts a refresh, the vendor takes a second or
// two, and in that window the operator — who believes the token is
// compromised — clicks Sign out. The delete succeeded, the UI showed "not
// signed in" and the audit log recorded provider.signed_out, and then the
// in-flight exchange completed and wrote a FRESH access+refresh pair straight
// back into the entry the operator had just destroyed. The grant was live
// again on the operator's real vendor account with nothing anywhere
// surfacing it.
//
// Serializing on the lock makes the two operations ordered rather than
// overlapping: the delete waits for the exchange, then removes what it wrote.
// The cost is that sign-out can block for as long as one vendor exchange —
// bounded by MaxOAuthRefreshLockHold, never unbounded — which is the right
// trade for a revocation.
//
// The lock alone is not sufficient, and is not the whole fix: it is
// process-wide, so a second Omnipus process sharing $OMNIPUS_HOME, or the
// provider-row delete in pkg/gateway/rest_providers_delete.go, can still
// remove the entry mid-exchange. NewStoreOAuthTokenSource's compare-and-swap
// covers that by re-reading the entry under the lock after the exchange and
// refusing to write when it has gone — see the "resurrection" branch there.
//
// The last-registered-token memo for this entry is dropped too, so a later
// sign-in re-registers with the scrubber from a clean slate rather than being
// suppressed by a stale memo for a credential that no longer exists.
func DeleteStoreOAuthCred(providerID string, store *credentials.Store) error {
	entryName := credentials.OAuthEntryName(OAuthVendorID(providerID))
	mu := oauthRefreshLock(entryName)
	mu.Lock()
	defer mu.Unlock()
	err := store.Delete(entryName)
	forgetOAuthTokenRegistration(entryName)
	return err
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

// MaxOAuthRefreshLockHold is the ceiling every caller that will enter
// NewStoreOAuthTokenSource must put on its auth.OAuthProviderConfig.Timeout.
//
// The lock above is held across the vendor exchange, so whoever holds it
// longest sets the stall everyone else on that vendor inherits: a live agent
// turn, a GET /providers/{id}/sign-in/status poll, and a DELETE
// /providers/{id}/sign-in sign-out all queue behind the same mutex. The
// agent path already bounded itself at 20s explicitly "so a hung vendor
// costs one turn"; the status poll did not, and silently ran on the auth
// package's 30s interactive default — so the tighter agent bound was not the
// ceiling it looked like, because the agent could be queued behind a status
// poll holding the lock for 30s. One constant, stated where the lock it
// governs is declared, is what keeps that from drifting apart again.
const MaxOAuthRefreshLockHold = 20 * time.Second

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

// --- Fast-path registration memo -------------------------------------------
//
// registeredOAuthTokens records, per store entry, the access token this
// process last handed to the scrubber. It exists to close the multi-process
// hole on the token source's FAST path — the branch that returns a stored,
// still-fresh token without refreshing.
//
// The gap: two Omnipus processes share $OMNIPUS_HOME. P2 refreshes and writes
// token B. P1's next call reads B, finds it fresh, and returns it straight to
// the LLM transport. P1 registered its sensitive values at boot and after
// every refresh IT performed, and B is neither — so if B is ever echoed back
// (a provider error quoting the Authorization header, a debug dump) it lands
// in P1's log verbatim instead of [FILTERED]. ADR-068 FR-046 does not care
// which process minted the token.
//
// Why a memo rather than registering unconditionally: the registrar the
// gateway installs is not cheap. It recomputes the COMPLETE sensitive set —
// credentials.ResolveBundle over the whole config plus a decrypting scan of
// every "<vendor>_OAUTH" entry — because RegisterSensitiveValues has
// replace-the-whole-set semantics. The fast path, by contrast, runs on every
// single LLM call. Paying a full config walk and store decrypt per call to
// re-register a value that is already registered would be a real cost for no
// change in protection. The memo makes the common case a map lookup and a
// string compare (nanoseconds, no I/O) and lets the expensive call through
// exactly when the token actually differs from the last one this process
// registered — which is precisely the case the hole describes.
//
// Correctness of suppressing the call: the gateway's registrar derives its
// set from the store, so any token still in the store stays registered no
// matter who else calls it. The only way a memoized token leaves the
// registered set is by leaving the store — sign-out or provider delete — and
// at that point it is not a secret this process needs to keep scrubbing.
// DeleteStoreOAuthCred drops the memo entry for exactly that reason.
//
// The map is bounded by the number of OAuth vendors used in this process — a
// handful — so entries are never evicted.
var (
	registeredOAuthTokensMu sync.Mutex
	registeredOAuthTokens   = make(map[string]string)
)

// ensureOAuthTokensRegistered registers cred's token pair as sensitive unless
// this process already registered that exact access token for this entry.
func ensureOAuthTokensRegistered(entryName string, cred *storeOAuthCred) {
	if cred == nil || cred.AccessToken == "" {
		return
	}
	registeredOAuthTokensMu.Lock()
	if registeredOAuthTokens[entryName] == cred.AccessToken {
		registeredOAuthTokensMu.Unlock()
		return
	}
	registeredOAuthTokens[entryName] = cred.AccessToken
	registeredOAuthTokensMu.Unlock()
	registerSensitiveValues(cred.AccessToken, cred.RefreshToken)
}

// forgetOAuthTokenRegistration drops the memo for one entry, so the next
// token stored under it is registered even in the (adversarial or test)
// case where it is byte-identical to the one that was just deleted.
func forgetOAuthTokenRegistration(entryName string) {
	registeredOAuthTokensMu.Lock()
	defer registeredOAuthTokensMu.Unlock()
	delete(registeredOAuthTokens, entryName)
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
//
// Residual race, stated rather than papered over: the post-exchange
// existence re-check and the write that follows it are two separate store
// operations, and credentials.Store offers no compare-and-swap. Within one
// process the refresh lock closes the gap completely — every writer that
// matters (this path and DeleteStoreOAuthCred) is serialized by it. ACROSS
// processes a delete that lands in the microseconds between our re-read and
// our Set is still lost. Closing that would need a CAS or a lock file in
// pkg/credentials, which is a change to a package this file does not own;
// the window shrank from "the whole vendor exchange", seconds wide and
// trivially hit by a human clicking Sign out, to two adjacent local file
// operations.
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
			// A stored token can have been minted by a DIFFERENT process
			// sharing this store, in which case nothing here has ever seen
			// it and it is about to be handed to the LLM transport
			// unprotected (ADR-068 FR-046). Memoized, so the overwhelmingly
			// common case — the same token this process already registered —
			// costs a map lookup rather than a full config walk and store
			// decrypt on every LLM call. See the memo's own note.
			ensureOAuthTokensRegistered(entryName, cred)
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

		// Compare-and-swap against a lost update AND against resurrecting a
		// revoked credential. The lock above excludes every other refresh IN
		// THIS PROCESS, but not the sign-in handlers (which write this same
		// entry without taking it), not the provider-row delete, and not a
		// second Omnipus process sharing the store. So the entry we read at
		// the top of this call may have been replaced — or REMOVED —
		// while our exchange was in flight, and the write below is the only
		// thing standing between that and an unconditional overwrite.
		latest, rerr := readStoreOAuthCred(store, entryName)
		switch {
		case rerr != nil:
			// We cannot establish what is in the store, so we cannot
			// establish that writing is safe. Fail closed: refusing costs
			// one turn and one "sign in again" prompt, and destroys nothing
			// — the stored credential is untouched, and the very next call
			// re-reads it. Writing blind is the branch that can resurrect a
			// credential the operator just revoked.
			return "", "", &providerNeedsSignInError{
				ProviderID: providerID,
				Cause:      fmt.Errorf("re-reading stored credential before save: %w", rerr),
			}

		case latest == nil:
			// THE REVOCATION CASE. readStoreOAuthCred reports a missing
			// entry as (nil, nil) — "not signed in" is not an error to it —
			// and the previous compare-and-swap only aborted when latest was
			// non-nil, so a DELETED entry read as "nothing newer here" and
			// fell straight through to the write. Sign-out therefore did not
			// revoke: the delete landed, the UI said "not signed in", the
			// audit log recorded provider.signed_out, and then this write put
			// a fresh access+refresh pair back on disk. Absence is not
			// "nothing newer" — it is the newest fact there is, and it means
			// the operator (or a provider-row delete, or another process)
			// destroyed this grant while we were talking to the vendor.
			//
			// Note what is NOT claimed here: the tokens the vendor just
			// minted exist in this process's memory and cannot be recalled;
			// what this guarantees is that they are never PERSISTED, so the
			// revocation holds for every later call rather than being undone
			// by this one. Nor is this a substitute for the vendor-side
			// revocation Omnipus does not perform — see ADR-068 FR-048.
			return "", "", &providerNeedsSignInError{
				ProviderID: providerID,
				Cause:      errors.New("stored credential was removed during the refresh exchange (signed out); refreshed tokens discarded"),
			}

		case latest.AccessToken != cred.AccessToken &&
			latest.AccessToken != refreshed.AccessToken &&
			!needsOAuthRefresh(latest.ExpiresAt):
			// A newer usable credential landed while we were in flight —
			// a just-completed sign-in, or another process's refresh.
			// Theirs wins: overwriting it with our older exchange's result
			// is the lost update that leaves a dead refresh token on disk.
			//
			// Register it too. When it came from THIS process's sign-in
			// handler it is already known to the scrubber, but when it came
			// from a second Omnipus process nothing here has ever seen it —
			// and we are about to hand it to the LLM transport. ADR-068
			// FR-046 does not care which process minted the token.
			ensureOAuthTokensRegistered(entryName, latest)
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
		ensureOAuthTokensRegistered(entryName, newCred)
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
