// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Library preview-token store (ADR-067 stage 1: FR-003b, FR-003d, FR-003h,
// FR-003k).
//
// WHAT THIS IS. A previewed document is served with an OPAQUE origin — that is
// the whole of the P0 isolation control (§10.3) — so it can send neither the
// SameSite=Strict session cookie nor an Authorization header when it loads its
// own stylesheet, script, font or media. The bytes therefore cannot come from
// the authenticated Library route (FR-003a). They come from a bare
// /library-preview/<token>/<relative-path> prefix instead, and this store is
// the only thing standing between that prefix and the operator's files.
//
// SO READ THE FAIL-CLOSED RULES BEFORE CHANGING ANYTHING HERE:
//
//  1. Minting FAILS CLOSED on an entropy error (FR-003h). No token, no
//     fallback, no shortened value. The entropy source is injectable
//     (WithPreviewTokenRand) precisely so that path has a test; an untested
//     failure path is not a failure path.
//  2. A token names ONE workspace and ONE scope root — a single file, or one
//     bundle root and its descendants. There is no whole-workspace scope, so
//     an empty or "." path is REFUSED rather than treated as the work-tree
//     root (FR-003b).
//  3. EXPIRY IS NOT REVOCATION (FR-003d). A token dies 15 minutes after
//     minting AND when the minting session logs out, when the workspace mount
//     is revoked, and when the named file or bundle root is deleted or moved.
//     Drop the logout half and an administrator's token stays a valid
//     UNAUTHENTICATED read grant after they log out — the outcome FR-003b
//     forbids, reached purely by omission.
//  4. At most 8 live tokens per session (FR-003k). Minting creates an entry in
//     an in-memory map, so an uncapped caller is a memory-growth path.
//
// WHAT THIS STORE DOES NOT DO, so nobody mistakes it for the control it is not:
//
//   - It does not check that the caller may read the path. "A token never
//     widens access" (FR-003b) is enforced by the MINT HANDLER, which must
//     resolve the path through the authenticated Library chain BEFORE calling
//     Mint. The store records the decision; it does not make it.
//   - AllowsRelPath is a string-level SCOPE check, layered before — never
//     instead of — the os.Root-confined open at the syscall boundary that
//     FR-003i requires of the serving handler. A string check passes every
//     traversal test and still follows a symlink out.
//
// Shape is borrowed from pkg/agent/served_subdirs.go for BYTE COUNT AND
// ENCODING ONLY (§10.5). Its renewal branch is deliberately NOT copied:
// re-registering the same directory there returns the SAME token string, so
// the credential survives as long as the tab is open — exactly the property a
// 15-minute lifetime exists to prevent. Here a re-mint for the same scope
// returns a NEW value and invalidates the previous one (FR-003m).
//
// Storage is in memory, keyed by token. A gateway restart invalidates every
// live preview, which is accepted because a restart also drops the page
// holding them.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/library"
)

// PreviewTokenTTL is the lifetime of a Library preview token (FR-003d, MV-20).
//
// Fifteen minutes: long enough to load and read a bundle, short enough that a
// token found later in a log is already dead. MV-20 requires this to be a
// NAMED constant rather than a literal repeated in handler and test, so that
// changing the lifetime cannot leave a test asserting the old one.
const PreviewTokenTTL = 15 * time.Minute

// MaxLivePreviewTokensPerSession caps how many unexpired tokens one session may
// hold at once (FR-003k). The ninth is refused.
const MaxLivePreviewTokensPerSession = 8

// previewTokenBytes is the entropy of a preview token before encoding: 32
// bytes from a cryptographic source (FR-003h, §10.5).
const previewTokenBytes = 32

// PreviewTokenEncodedLen is the length of a minted token string — 32 bytes in
// base64url with no padding is exactly 43 ASCII characters. The wire contract
// (LibraryPreviewTokenResponse.token) pins minLength and maxLength to 43, so a
// change here is a contract change.
const PreviewTokenEncodedLen = 43

// PreviewScope names the two grant shapes, and there is no third (FR-003b).
type PreviewScope string

const (
	// PreviewScopeFile grants exactly one file and nothing else.
	PreviewScopeFile PreviewScope = "file"
	// PreviewScopeBundle grants one directory and its descendants — what an
	// HTML page with its own stylesheets, scripts, fonts and media needs.
	PreviewScopeBundle PreviewScope = "bundle"
)

// Mint refusals. Each is a distinct sentinel so the handler can map it to the
// right status without string matching.
var (
	// ErrPreviewTokenEntropy is returned when the random source fails. No
	// token is issued and the store is not mutated (FR-003h).
	ErrPreviewTokenEntropy = errors.New("preview token: entropy source failed")
	// ErrPreviewTokenSession is returned when no minting session can be
	// identified. Minting is authenticated (FR-003b); an anonymous mint has
	// nothing to revoke on logout.
	ErrPreviewTokenSession = errors.New("preview token: no minting session")
	// ErrPreviewTokenWorkspace is returned for an empty workspace id.
	ErrPreviewTokenWorkspace = errors.New("preview token: workspace required")
	// ErrPreviewTokenScope is returned for a scope other than file or bundle.
	ErrPreviewTokenScope = errors.New("preview token: unknown scope")
	// ErrPreviewTokenPath is returned when the path is unusable as a scope
	// root — malformed, traversing, or empty. An empty path is refused
	// deliberately: it would be a whole-workspace grant, which FR-003b says
	// does not exist.
	ErrPreviewTokenPath = errors.New("preview token: invalid scope root")
	// ErrPreviewTokenCap is returned when the session already holds
	// MaxLivePreviewTokensPerSession live tokens (FR-003k).
	ErrPreviewTokenCap = errors.New("preview token: too many live tokens for this session")
)

// PreviewGrant is what a token buys: one workspace, one scope root, until one
// instant. It carries no secret — the token string itself is the credential
// and is never stored on the grant, so a grant may be logged where a token may
// not (FR-003e).
type PreviewGrant struct {
	// WorkspaceID is the workspace whose work tree contains ScopeRoot.
	WorkspaceID string
	// ScopeRoot is the cleaned workspace-relative path this token is confined
	// to: the file itself for PreviewScopeFile, the bundle root for
	// PreviewScopeBundle. Never empty.
	ScopeRoot string
	// Scope is the grant shape.
	Scope PreviewScope
	// SessionKey identifies the minting session so logout can revoke it. It is
	// a one-way digest of the presented credential, never the credential
	// itself — see PreviewSessionKey.
	SessionKey string
	// ExpiresAt is the instant the token stops working. A lookup at exactly
	// this instant is REFUSED.
	ExpiresAt time.Time
}

// AllowsRelPath reports whether relPath is inside this grant's scope.
//
// relPath MUST already have been through library.CleanRelPath. This is a
// string-level scope check and NOTHING MORE: it is layered before, never
// instead of, the os.Root-confined open the serving handler owes FR-003i. A
// filepath.Clean-and-compare implementation passes every string traversal test
// and still follows a symlink out of the scope.
func (g PreviewGrant) AllowsRelPath(relPath string) bool {
	if g.ScopeRoot == "" || relPath == "" {
		return false
	}
	if relPath == g.ScopeRoot {
		// A file grant is exactly its own file. A bundle grant trivially
		// contains its own root.
		return true
	}
	if g.Scope != PreviewScopeBundle {
		return false
	}
	// The separator is part of the prefix on purpose: bundle root "a/b" must
	// contain "a/b/c" and must NOT contain the sibling "a/bc".
	return strings.HasPrefix(relPath, g.ScopeRoot+"/")
}

// PreviewTokenStore holds live preview grants. Zero value is unusable — call
// NewPreviewTokenStore.
//
// Every exported method is safe for concurrent use; the store is reached from
// HTTP handlers on the main listener.
type PreviewTokenStore struct {
	mu sync.Mutex
	// byToken maps token string → grant. The token is the map key and is the
	// credential, so this map must never be range-printed into a log.
	byToken map[string]PreviewGrant
	// bySession maps session key → its live token set, so logout revocation
	// and the per-session cap are both O(live tokens for that session).
	bySession map[string]map[string]struct{}
	// byScope maps a per-session scope identity → the token currently granted
	// for it, so a re-mint of the SAME document rotates in place (FR-003m)
	// instead of consuming another slot of the cap. Keyed by session as well
	// as scope so one session's re-mint can never revoke another session's
	// token for the same file.
	byScope map[string]string

	// randRead is the entropy source, injectable so the fail-closed path in
	// Mint is testable (FR-003h). Defaults to crypto/rand.Read.
	randRead func([]byte) (int, error)
	// now is the clock, injectable so TTL boundaries are asserted against the
	// named constant rather than a sleep.
	now func() time.Time
}

// PreviewTokenStoreOption configures a store at construction.
type PreviewTokenStoreOption func(*PreviewTokenStore)

// WithPreviewTokenRand replaces the entropy source. Tests use it to force the
// FR-003h fail-closed path; production never calls it.
func WithPreviewTokenRand(read func([]byte) (int, error)) PreviewTokenStoreOption {
	return func(s *PreviewTokenStore) {
		if read != nil {
			s.randRead = read
		}
	}
}

// WithPreviewTokenClock replaces the clock, so TTL boundaries can be asserted
// without sleeping fifteen minutes.
func WithPreviewTokenClock(now func() time.Time) PreviewTokenStoreOption {
	return func(s *PreviewTokenStore) {
		if now != nil {
			s.now = now
		}
	}
}

// NewPreviewTokenStore returns an empty store.
//
// There is no janitor goroutine and no Close: expired entries are swept on
// every Mint, and an expired entry is refused by Lookup regardless of whether
// it has been swept yet, so a lingering entry is inert. PurgeExpired is
// exported for a caller that wants to schedule a sweep anyway.
func NewPreviewTokenStore(opts ...PreviewTokenStoreOption) *PreviewTokenStore {
	s := &PreviewTokenStore{
		byToken:   make(map[string]PreviewGrant),
		bySession: make(map[string]map[string]struct{}),
		byScope:   make(map[string]string),
		randRead:  rand.Read,
		now:       time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// PreviewSessionKey derives the minting session's identity from a request.
//
// The gateway has no session-id primitive: a browser caller is identified by
// the omnipus-session cookie's plaintext value, an API caller by its bearer
// token (pkg/gateway/rest_auth.go). Either is a live credential, so neither is
// used as a map key directly — the key is a SHA-256 digest, tagged by source
// so a cookie value can never collide with a bearer token of the same bytes.
// Storing the digest means a heap dump or a debug print of the store yields
// nothing an attacker can replay.
//
// Returns ok=false when the request carries neither, which Mint refuses: an
// anonymous grant has no session to revoke when someone logs out.
//
// HandleLogout MUST call this on the logout request and pass the result to
// InvalidateSession. The logout request still carries the cookie being
// revoked, so the key matches the one used at mint time.
func PreviewSessionKey(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	if c, err := r.Cookie(middleware.SessionCookieName); err == nil && c != nil && c.Value != "" {
		return "c:" + digestCredential(c.Value), true
	}
	const bearerPrefix = "Bearer "
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, bearerPrefix) {
		if raw := strings.TrimPrefix(auth, bearerPrefix); raw != "" {
			return "b:" + digestCredential(raw), true
		}
	}
	return "", false
}

// digestCredential one-way hashes a credential for use as a map key.
func digestCredential(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// scopeIdentity is the per-session re-mint key (FR-003m).
func scopeIdentity(sessionKey, workspaceID string, scope PreviewScope, scopeRoot string) string {
	// NUL joins the parts so no combination of ids can be spelled two ways —
	// CleanRelPath already rejects control characters in a path, so a NUL can
	// never appear inside a component.
	return sessionKey + "\x00" + workspaceID + "\x00" + string(scope) + "\x00" + scopeRoot
}

// Mint issues a token for one workspace and one scope root, valid for
// PreviewTokenTTL.
//
// The caller MUST already have verified that this session may read this path;
// Mint does not and cannot check that (FR-003b — "a token never widens
// access" is a property of the mint handler, not of this store).
//
// Failure modes, all of them closed:
//   - entropy error          → ErrPreviewTokenEntropy, no token, no mutation
//   - no session             → ErrPreviewTokenSession
//   - empty workspace        → ErrPreviewTokenWorkspace
//   - scope not file/bundle  → ErrPreviewTokenScope
//   - path empty/traversing  → ErrPreviewTokenPath
//   - 8 live already         → ErrPreviewTokenCap
//
// Re-minting for the same session, workspace, scope and path returns a NEW
// token and invalidates the previous one (FR-003m), and does not consume an
// extra slot of the cap.
func (s *PreviewTokenStore) Mint(
	sessionKey, workspaceID, rawPath string,
	scope PreviewScope,
) (token string, grant PreviewGrant, err error) {
	if sessionKey == "" {
		return "", PreviewGrant{}, ErrPreviewTokenSession
	}
	if workspaceID == "" {
		return "", PreviewGrant{}, ErrPreviewTokenWorkspace
	}
	if scope != PreviewScopeFile && scope != PreviewScopeBundle {
		return "", PreviewGrant{}, ErrPreviewTokenScope
	}
	scopeRoot, cleanErr := library.CleanRelPath(rawPath)
	if cleanErr != nil {
		return "", PreviewGrant{}, ErrPreviewTokenPath
	}
	if scopeRoot == "" {
		// CleanRelPath maps "", "." and "./" to the empty string, meaning the
		// work-tree root. There is no whole-workspace scope (FR-003b), so this
		// is a refusal and not a root grant.
		return "", PreviewGrant{}, ErrPreviewTokenPath
	}

	// Draw the entropy BEFORE touching the store, so an entropy failure cannot
	// leave a previous token revoked with no replacement issued.
	buf := make([]byte, previewTokenBytes)
	n, randErr := s.randRead(buf)
	if randErr != nil {
		return "", PreviewGrant{}, errors.Join(ErrPreviewTokenEntropy, randErr)
	}
	if n != previewTokenBytes {
		// A short read is the quiet half of the same failure: io.ReadFull
		// semantics are not guaranteed by an arbitrary source, and a token
		// built from a partly-zero buffer is exactly the "shortened value"
		// FR-003h forbids.
		return "", PreviewGrant{}, ErrPreviewTokenEntropy
	}
	token = base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.purgeExpiredLocked()

	ident := scopeIdentity(sessionKey, workspaceID, scope, scopeRoot)
	prev, hasPrev := s.byScope[ident]
	if !hasPrev && len(s.bySession[sessionKey]) >= MaxLivePreviewTokensPerSession {
		// Refuse rather than evict: FR-003k's cap exists to bound memory, and
		// evicting someone's oldest live preview to serve a ninth would make
		// the failure invisible instead of explicit.
		return "", PreviewGrant{}, ErrPreviewTokenCap
	}
	if hasPrev {
		s.deleteTokenLocked(prev)
	}

	grant = PreviewGrant{
		WorkspaceID: workspaceID,
		ScopeRoot:   scopeRoot,
		Scope:       scope,
		SessionKey:  sessionKey,
		ExpiresAt:   s.now().Add(PreviewTokenTTL),
	}
	s.byToken[token] = grant
	if s.bySession[sessionKey] == nil {
		s.bySession[sessionKey] = make(map[string]struct{})
	}
	s.bySession[sessionKey][token] = struct{}{}
	s.byScope[ident] = token
	return token, grant, nil
}

// Lookup returns the grant for token, or ok=false if the token is unknown,
// revoked or expired.
//
// Unknown and expired are DELIBERATELY the same answer. FR-003n requires the
// serving handler to make expired, revoked and unknown indistinguishable on
// the wire, and that is far easier to hold if the store never distinguishes
// them either. A lookup at exactly ExpiresAt is refused.
func (s *PreviewTokenStore) Lookup(token string) (PreviewGrant, bool) {
	if token == "" {
		return PreviewGrant{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.byToken[token]
	if !ok {
		return PreviewGrant{}, false
	}
	if !s.now().Before(g.ExpiresAt) {
		return PreviewGrant{}, false
	}
	return g, true
}

// InvalidateToken revokes one token. Reports whether it was live.
func (s *PreviewTokenStore) InvalidateToken(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byToken[token]; !ok {
		return false
	}
	s.deleteTokenLocked(token)
	return true
}

// InvalidateSession revokes every token minted by one session and returns how
// many were revoked.
//
// THIS IS THE HALF THAT GETS FORGOTTEN (FR-003d). Expiry alone is not
// revocation: without this call wired into logout, an administrator's token
// remains a valid UNAUTHENTICATED read grant for up to fifteen minutes after
// they log out, on a path that requires no credential but the token itself.
func (s *PreviewTokenStore) InvalidateSession(sessionKey string) int {
	if sessionKey == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tokens := s.bySession[sessionKey]
	revoked := 0
	for tok := range tokens {
		s.deleteTokenLocked(tok)
		revoked++
	}
	return revoked
}

// InvalidateWorkspace revokes every token scoped to one workspace and returns
// how many were revoked. Called when a workspace mount is revoked (FR-003d).
func (s *PreviewTokenStore) InvalidateWorkspace(workspaceID string) int {
	if workspaceID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	revoked := 0
	for tok, g := range s.byToken {
		if g.WorkspaceID == workspaceID {
			s.deleteTokenLocked(tok)
			revoked++
		}
	}
	return revoked
}

// InvalidatePath revokes every token whose scope root is rawPath or lies
// beneath it, within one workspace, and returns how many were revoked. Called
// when a file or bundle root is deleted or moved (FR-003d).
//
// The beneath-it half matters: deleting the directory "reports" must also kill
// a token scoped to "reports/q3", whose named path is now gone even though
// nobody named it. Deleting a file INSIDE a bundle does not revoke the
// bundle's token — FR-003d names the file or the bundle root, and a bundle
// losing one of its images is not a revocation event.
func (s *PreviewTokenStore) InvalidatePath(workspaceID, rawPath string) int {
	if workspaceID == "" {
		return 0
	}
	cleaned, err := library.CleanRelPath(rawPath)
	if err != nil || cleaned == "" {
		// A malformed or root path names nothing revocable. Revoking the whole
		// workspace here would turn a bad argument into a denial of service;
		// callers with a workspace-wide event call InvalidateWorkspace.
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	revoked := 0
	for tok, g := range s.byToken {
		if g.WorkspaceID != workspaceID {
			continue
		}
		if g.ScopeRoot == cleaned || strings.HasPrefix(g.ScopeRoot, cleaned+"/") {
			s.deleteTokenLocked(tok)
			revoked++
		}
	}
	return revoked
}

// PurgeExpired drops expired entries and returns how many were dropped. Mint
// calls it on every mint; it is exported for a caller that wants to schedule a
// sweep. An unswept expired entry is already refused by Lookup, so this is
// housekeeping, not a control.
func (s *PreviewTokenStore) PurgeExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.purgeExpiredLocked()
}

// LiveCount returns the number of unexpired tokens across all sessions.
func (s *PreviewTokenStore) LiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	live := 0
	for _, g := range s.byToken {
		if now.Before(g.ExpiresAt) {
			live++
		}
	}
	return live
}

// LiveCountForSession returns the number of unexpired tokens held by one
// session — the quantity MaxLivePreviewTokensPerSession bounds.
func (s *PreviewTokenStore) LiveCountForSession(sessionKey string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	live := 0
	for tok := range s.bySession[sessionKey] {
		if g, ok := s.byToken[tok]; ok && now.Before(g.ExpiresAt) {
			live++
		}
	}
	return live
}

// purgeExpiredLocked drops expired entries. Caller holds s.mu.
func (s *PreviewTokenStore) purgeExpiredLocked() int {
	now := s.now()
	dropped := 0
	for tok, g := range s.byToken {
		if !now.Before(g.ExpiresAt) {
			s.deleteTokenLocked(tok)
			dropped++
		}
	}
	return dropped
}

// deleteTokenLocked removes a token from all three indexes. Caller holds s.mu.
//
// All three, every time: leaving a stale bySession entry silently consumes a
// slot of the FR-003k cap forever, and leaving a stale byScope entry makes the
// next re-mint delete a token that no longer exists while failing to rotate
// the one that does.
func (s *PreviewTokenStore) deleteTokenLocked(token string) {
	g, ok := s.byToken[token]
	if !ok {
		return
	}
	delete(s.byToken, token)
	if set := s.bySession[g.SessionKey]; set != nil {
		delete(set, token)
		if len(set) == 0 {
			delete(s.bySession, g.SessionKey)
		}
	}
	ident := scopeIdentity(g.SessionKey, g.WorkspaceID, g.Scope, g.ScopeRoot)
	if s.byScope[ident] == token {
		delete(s.byScope, ident)
	}
}
