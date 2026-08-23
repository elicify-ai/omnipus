// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ADR-067 stage 1 — the Library preview-token route.
//
// TWO URLS, DELIBERATELY DIFFERENT (§10.5). The authenticated Library path
// (/api/v1/library/…?path=) keeps Content-Disposition: attachment exactly as
// today and carries no isolation policy. THIS route — a bare, path-token
// authenticated /library-preview/<token>/<relative-path> prefix on the MAIN
// listener (ADR-044 shape; there is no separate preview listener) — is the
// only one that serves bytes inline, and every single response it produces
// carries the §10.3 policy.
//
// WHY A TOKEN IN THE PATH AT ALL (FR-003a). A previewed document is bound to
// an OPAQUE origin — that is the whole of the P0 control — so when its own
// <link>, <script>, font or media request goes out it can send neither the
// SameSite=Strict session cookie nor an Authorization header. The
// authenticated Library route is behind withUploadAuth, so an HTML bundle
// served from there simply cannot load its own subresources. The credential
// therefore has to travel somewhere a sandboxed subresource request still
// carries it, and the URL path is the only such place. The accepted cost is
// that the credential appears in request logs and history — which is why
// FR-003e redacts it at every recording site and why the lifetime is fifteen
// minutes rather than a day.
//
// THE FOUR THINGS THAT MUST NOT BE "TIDIED" HERE:
//
//  1. The policy is built from §10.3's template exactly once and is asserted
//     directive by directive against the spec's own text. Its six source
//     directives name the gateway's canonical origin IN ADDITION TO `'self'`:
//     under WebKit, the FR-005b iframe sandbox ATTRIBUTE on top of the §10.3
//     sandbox DIRECTIVE makes the document's `self` an opaque origin matching
//     nothing, so the explicit host source is the one that matches — and
//     `'self'` stays beside it so a wrong or absent origin degrades on Safari
//     alone rather than on every engine (library_isolation_policy.go has the
//     measurement).
//     BOTH halves are load-bearing: measured on three engines,
//     the `sandbox` directive alone let five of seven egress vectors out, and
//     the source directives alone let window.open out because no CSP
//     directive covers popup navigation. Dropping either half satisfies
//     neither FR-005 nor FR-006 while still passing any requirement that
//     merely asks for "a policy".
//  2. The method gate runs FIRST, before the token is even looked at, and
//     never touches the request body (FR-003j). A handler registered on a bare
//     prefix accepts EVERY method by default. This matters more, not less, now
//     that the prefix is CSRF-exempt: the exemption exists so a cookie-less
//     POST reaches this 405 (and the §10.3 policy) instead of the CSRF gate's
//     policy-free 403 — which is only safe while the route really does answer
//     nothing but GET and HEAD. If that stops being true, the exemption turns
//     from a fix into a hole, with no other symptom.
//  3. Containment is resolved through the SAME chain as the authenticated
//     path — library.CleanRelPath for shape AND an os.Root-confined open at
//     the SYSCALL boundary — and the os.Root is opened at the TOKEN'S OWN
//     SCOPE ROOT, not merely at the workspace (FR-003i). A
//     filepath.Clean-and-Join implementation passes every string-level
//     traversal test and still follows a symlink out.
//  4. Expired, revoked and unknown tokens produce ONE indistinguishable
//     response (FR-003n). A 410-vs-404 split is a working oracle for whether
//     a token ever existed.

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// The §10.3 policy is BUILT in ONE place — library_isolation_policy.go's
// buildLibraryIsolationPolicy, reached through libraryIsolationPolicy() — and
// this route carries it via setLibraryPreviewSecurityHeaders and
// applyLibraryPreviewByteHeaders. A second copy here would be a defect, not a
// convenience: MV-13 asserts one byte-identical value, and the only thing
// keeping two literals in step is a human remembering both. The copy that
// drifts is the one nobody re-reads, and a dropped directive has no visible
// symptom — the page still renders, it is simply no longer contained.
// inline_serving_test.go's source gate fails the build if a second copy
// appears.

// libraryPreviewPathPrefix is the bare serving prefix on the MAIN listener
// (§10.5, ADR-044 shape). Deliberately NOT /preview/, which keeps its existing
// web_serve meaning.
//
// It is an ALIAS of the middleware constant, not a second literal, because the
// prefix is a security boundary in two packages at once: the router serves it
// and the CSRF middleware exempts it (middleware.defaultExemptPrefixes). Two
// literals would let the two drift, and the drift is silent — the middleware
// would keep exempting a path the router no longer serves.
//
// WHY IT IS CSRF-EXEMPT, since an earlier revision of this comment argued the
// opposite. FR-003f's argument — GET/HEAD-only, so the gate never has to BLOCK
// anything here — is about blocking and is correct. It is not an argument for
// letting the gate RUN: without the exemption a cookie-less POST is answered by
// CSRF with a policy-free 403 instead of FR-003j's 405 + `Allow: GET, HEAD`,
// and every subresource GET from the opaque-origin preview document trips the
// safe-method cookie re-mint and stamps a Set-Cookie onto a preview response.
// middleware.defaultExemptPrefixes carries the full reasoning.
//
// FR-003e is unaffected. Redaction is a property of each recording SITE, not of
// which routes reach it: pathredact still knows this prefix's token segment, the
// other token-bearing prefixes (/serve/, /dev/) still reach the CSRF gate, and
// the rate limiter on this very route still logs a redacted path on every 429.
const libraryPreviewPathPrefix = middleware.LibraryPreviewPathPrefix

// libraryPreviewMintPath is the authenticated minting endpoint (FR-003f).
// Registered as an EXACT pattern so it outranks the "/api/v1/library/"
// subtree registration, whose dispatcher would otherwise 404 it.
const libraryPreviewMintPath = "/api/v1/library/preview-token"

// libraryPreviewDefaultEntry is the document a bundle grant opens when the
// caller names none — the contract's stated default for entry_path.
const libraryPreviewDefaultEntry = "index.html"

// Rate limiters for the two halves of this feature (FR-003k).
//
// Two reasons the serving prefix is limited and not just the mint endpoint:
// minting creates an entry in an in-memory store, so an uncapped caller is a
// memory-growth path; and a forced 429 on the serving path IS the oracle for
// FR-003e's "no token in any log" test — without rate limiting that test
// captures nothing and passes having asserted nothing.
var (
	// libraryPreviewMintLimiter caps minting. Tighter than configLimiter:
	// a mint is a credential issue, and the per-session cap of 8 live tokens
	// means a legitimate SPA never needs a high rate here.
	libraryPreviewMintLimiter = newAPIRateLimiter(60, 1*time.Minute)
	// libraryPreviewServeLimiter caps the serving prefix. Generous, because
	// ONE bundle page legitimately fires a request per stylesheet, script,
	// font, image and media element, and a slow limit here would break the
	// flagship US-1 AS-4 scenario rather than protect anything.
	libraryPreviewServeLimiter = newAPIRateLimiter(600, 1*time.Minute)
)

// --- FR-003d revocation: the seam, and the three events that use it -------
//
// EXPIRY IS NOT REVOCATION. Until each of the three events below calls the
// matching Invalidate* method, an administrator's token stays a valid
// UNAUTHENTICATED read grant for up to fifteen minutes after they log out —
// the outcome FR-003b forbids, reached purely by omission.
//
// The store is reached through restAPI.previewTokens rather than a package
// global so that a test can drive the real logout / mount-delete / library-
// delete handlers against the same store the mint handler wrote to. A global
// would be shared by every parallel test in the package and could not be.
//
// Every helper below is nil-safe: a restAPI built by a test that never
// registered the preview routes has no store, and revocation on such an API is
// a no-op rather than a panic in an unrelated handler.

// previewTokenStore returns the live store, or nil when the preview routes
// were never registered on this restAPI.
func (a *restAPI) previewTokenStore() *PreviewTokenStore {
	if a == nil {
		return nil
	}
	return a.previewTokens.Load()
}

// revokePreviewTokensForSession invalidates every preview token minted by the
// session making r (FR-003d, logout).
//
// It is deliberately driven from the REQUEST rather than from a user name: the
// grant is keyed by the session credential the mint request carried, and the
// logout request still carries that same cookie or bearer token, so the key
// matches. A user-name key would revoke a colleague's live preview on another
// device, which SEC-1 explicitly refuses to do for bearer tokens.
func (a *restAPI) revokePreviewTokensForSession(r *http.Request) int {
	store := a.previewTokenStore()
	if store == nil {
		return 0
	}
	sessionKey, ok := PreviewSessionKey(r)
	if !ok {
		return 0
	}
	return store.InvalidateSession(sessionKey)
}

// revokePreviewTokensForWorkspace invalidates every preview token scoped to
// workspaceID (FR-003d, mount revoke).
//
// Workspace-wide rather than mount-wide on purpose: a grant records the
// workspace and the work-tree-relative scope root, not which mount (if any)
// backs it, so "was this path inside the mount that just went away" is not a
// question the store can answer. Revoking the workspace's tokens costs a
// re-mint the SPA performs transparently; leaving a token live over a folder
// the operator just detached is a read grant they believe they revoked.
func (a *restAPI) revokePreviewTokensForWorkspace(workspaceID string) int {
	store := a.previewTokenStore()
	if store == nil {
		return 0
	}
	return store.InvalidateWorkspace(workspaceID)
}

// revokePreviewTokensForPath invalidates every preview token whose scope root
// is relPath or lies beneath it (FR-003d, delete/move).
//
// Called with EVERY path a mutation destroys or vacates — for a rename that is
// both ends: the source no longer exists, and whatever used to be at the
// destination has been replaced by different bytes. A token still serving the
// destination would hand out content its holder was never granted.
func (a *restAPI) revokePreviewTokensForPath(workspaceID, relPath string) int {
	store := a.previewTokenStore()
	if store == nil {
		return 0
	}
	return store.InvalidatePath(workspaceID, relPath)
}

// libraryPreviewRoutes binds the mint endpoint and the serving prefix to one
// token store. Constructed once at registration; the store is process-wide
// for the life of the gateway (§10.5: in-memory, a restart invalidates every
// live preview, which is accepted because a restart also drops the page
// holding them).
type libraryPreviewRoutes struct {
	api    *restAPI
	tokens *PreviewTokenStore
}

// newLibraryPreviewRoutes builds the two halves over one token store and
// PUBLISHES that store on the restAPI (FR-003d).
//
// The publish is not bookkeeping. It is the only way logout, mount revoke and
// library delete/move — all of which live in other handlers that never see the
// registrar — can reach the store to revoke. An earlier revision returned the
// store from the registration function instead; the single call site dropped it
// on the floor and expiry silently became the only revocation the product had.
// Making the constructor perform the wiring removes the step a caller can skip.
func newLibraryPreviewRoutes(a *restAPI) *libraryPreviewRoutes {
	// Freeze the §10.3 policy against the gateway's canonical browser-facing
	// origin here, in the constructor, for the same reason the store is
	// published here: this is the one step no caller can skip. The registrar
	// below is not that step — the test fixtures build their routes through
	// this constructor and never register anything, so a freeze in the
	// registrar would leave every one of them serving the unfrozen policy while
	// the shipped binary served a different one. A test suite that measures a
	// header the product never sends is the false green this feature has
	// already been bitten by once.
	//
	// The origin comes from middleware.CanonicalGatewayOrigin — the resolver
	// §10.3 names, and the same one CORS, the WebSocket CheckOrigin fence and
	// web_serve's preview URLs use. It is deliberately NOT recomputed from
	// Host/Port here and deliberately NOT taken from a.allowedOrigin: a second
	// derivation of "the same" origin disagrees silently, and gateway.public_url
	// (restart-gated, ADR-044) is what makes a reverse-proxied deployment name
	// the origin the BROWSER reaches rather than the one we bound.
	//
	// When it resolves to "" — a wildcard bind with no gateway.public_url —
	// freezeLibraryIsolationPolicy collapses §10.3's template to its
	// `'self'`-only form, which is byte-identical to what this package shipped
	// before the amendment, and logs one WARN. Containment is unchanged; what
	// degrades is Safari's ability to load a bundle's external CSS and JS
	// inside the FR-005b sandbox attribute.
	freezeLibraryIsolationPolicy(previewCanonicalOrigin(a))

	routes := &libraryPreviewRoutes{api: a, tokens: NewPreviewTokenStore()}
	a.previewTokens.Store(routes.tokens)
	return routes
}

// previewCanonicalOrigin resolves the gateway's browser-facing origin through
// the one resolver §10.3 names, tolerating a restAPI with no agent loop.
//
// The nil case is not defensive padding: a restAPI without a loop cannot serve
// a preview at all, and returning "" there yields the collapsed `'self'` policy
// — the same answer an operator gets from a wildcard bind, arrived at by the
// same path, rather than a panic inside a constructor.
func previewCanonicalOrigin(a *restAPI) string {
	if a == nil || a.agentLoop == nil {
		return ""
	}
	return middleware.CanonicalGatewayOrigin(a.agentLoop.GetConfig())
}

// registerLibraryPreviewRoutes registers both halves and wires the store into
// the restAPI so the revocation events FR-003d requires (logout, mount revoke,
// path delete/move) can reach it.
func (a *restAPI) registerLibraryPreviewRoutes(cm httpHandlerRegistrar) *PreviewTokenStore {
	routes := newLibraryPreviewRoutes(a)
	cm.RegisterHTTPHandler(
		libraryPreviewMintPath,
		a.withAuth(withRateLimit(libraryPreviewMintLimiter, routes.handleMintPreviewToken)),
	)
	cm.RegisterHTTPHandler(libraryPreviewPathPrefix, routes.serveHandler())
	return routes.tokens
}

// serveHandler wraps the serving handler in its rate limiter, with the
// isolation headers set BEFORE the limiter runs.
//
// The ordering is the point. withRateLimit answers a rejected request itself,
// so a 429 produced there would otherwise be the one response on this prefix
// that carries no policy — and "every response, whatever the file's type"
// (§10.3) has to mean every response, including the ones this route produces
// by refusing. handleServeLibraryPreview sets the same headers again so a
// direct call (in a test, or from a future caller that skips the limiter) is
// never accidentally policy-free.
func (p *libraryPreviewRoutes) serveHandler() http.HandlerFunc {
	limited := withRateLimit(libraryPreviewServeLimiter, p.handleServeLibraryPreview)
	return func(w http.ResponseWriter, r *http.Request) {
		setLibraryPreviewSecurityHeaders(w)
		limited(w, r)
	}
}

// setLibraryPreviewSecurityHeaders applies the three response-borne controls
// this route owes every response it produces:
//
//   - Content-Security-Policy: the §10.3 literal (FR-005, FR-005a, FR-006).
//   - Referrer-Policy: no-referrer (FR-003c) — the token is IN the URL, so a
//     Referer header on any outbound navigation would hand the credential to
//     whatever the page links to.
//   - X-Content-Type-Options: nosniff (FR-015) — the Content-Type comes from
//     the compiled-in extension table and nothing else, so the browser must
//     not be allowed to second-guess it.
//
// Cache-Control: no-store is added for the same reason as Referrer-Policy:
// the URL is a credential, and a shared cache holding the response keyed by
// that URL outlives the fifteen-minute lifetime that makes the credential
// tolerable.
func setLibraryPreviewSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set(headerContentSecurityPolicy, libraryIsolationPolicy())
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
}

// --- POST /api/v1/library/preview-token (FR-003f) ---

// handleMintPreviewToken mints a preview token for one workspace and one path.
//
// "A token never widens access" (FR-003b) is enforced HERE and nowhere else:
// the store records the decision, it does not make it. So this handler
// resolves the requested path through the authenticated Library chain —
// workspace.Exists, library.CleanRelPath, then an os.Root-confined Stat via
// library.Root — BEFORE calling Mint. A path the caller could not read on the
// authenticated route never becomes a token.
func (p *libraryPreviewRoutes) handleMintPreviewToken(w http.ResponseWriter, r *http.Request) {
	a := p.api
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Minting is authenticated (FR-003b). Route registration puts withAuth in
	// front of this handler, but the session IDENTITY is a separate
	// requirement: a grant with no session behind it has nothing to revoke
	// when someone logs out, which is exactly the omission FR-003d warns
	// turns a token into a standing unauthenticated read grant.
	sessionKey, ok := PreviewSessionKey(r)
	if !ok {
		jsonErr(w, http.StatusUnauthorized,
			"preview tokens can only be minted by an authenticated session")
		return
	}

	var req gen.LibraryPreviewTokenRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LibraryPreviewTokenRequest", &req, validateEnabled) {
		return
	}

	if err := validateEntityID(req.WorkspaceId); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	if !workspace.Exists(a.homePath, req.WorkspaceId) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	scope, scopeOK := previewScopeFromWire(req.Scope)
	if !scopeOK {
		jsonErr(w, http.StatusBadRequest, "scope must be \"file\" or \"bundle\"")
		return
	}

	scopeRoot, err := library.CleanRelPath(req.Path)
	if err != nil || scopeRoot == "" {
		// An empty path is refused rather than treated as the work-tree root:
		// there is no whole-workspace scope (FR-003b).
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, rootOK := a.openLibraryRoot(w, req.WorkspaceId, "root")
	if !rootOK {
		return
	}
	defer root.Close()

	// The read check. Stat goes through library.Root's os.Root, so an
	// out-of-root symlink surfaces here as ErrOutsideRoot (403) rather than
	// becoming a token that would have to be caught later.
	switch scope {
	case PreviewScopeFile:
		if _, statErr := root.StatFile(scopeRoot); statErr != nil {
			mapLibraryErr(w, "mint preview token", req.WorkspaceId, statErr)
			return
		}
	case PreviewScopeBundle:
		if _, statErr := root.StatDir(scopeRoot); statErr != nil {
			mapLibraryErr(w, "mint preview token", req.WorkspaceId, statErr)
			return
		}
	}

	// The document the returned url points at. For a file grant that is the
	// file; for a bundle grant it is entry_path inside the bundle, defaulting
	// to index.html.
	entryRel := scopeRoot
	if scope == PreviewScopeBundle {
		entry := libraryPreviewDefaultEntry
		if req.EntryPath != nil && strings.TrimSpace(*req.EntryPath) != "" {
			entry = *req.EntryPath
		}
		cleanEntry, entryErr := library.CleanRelPath(entry)
		if entryErr != nil || cleanEntry == "" {
			jsonErr(w, http.StatusBadRequest, "invalid entry_path")
			return
		}
		entryRel = scopeRoot + "/" + cleanEntry
	}

	token, grant, mintErr := p.tokens.Mint(sessionKey, req.WorkspaceId, scopeRoot, scope)
	if mintErr != nil {
		writeMintError(w, req.WorkspaceId, mintErr)
		return
	}

	resp := gen.LibraryPreviewTokenResponse{
		Token: token,
		Url:   libraryPreviewURL(token, entryRel),
		// UTC, because the contract says RFC3339 UTC and a local-zone
		// timestamp would still parse — silently, and with the wrong offset.
		ExpiresAt: grant.ExpiresAt.UTC(),
		// Derived from the named constant, never a literal 900 repeated here:
		// a lifetime change must not leave this field asserting the old one.
		ExpiresInSeconds: int(PreviewTokenTTL / time.Second),
		Scope:            gen.LibraryPreviewTokenResponseScope(scope),
		ScopeRoot:        grant.ScopeRoot,
		WorkspaceId:      &req.WorkspaceId,
	}
	jsonCreated(w, resp)
}

// previewScopeFromWire maps the wire scope enum onto the store's scope type.
// There is no third shape and no default: an unrecognised value is a 400, not
// a silent narrowing to "file".
func previewScopeFromWire(s gen.LibraryPreviewTokenRequestScope) (PreviewScope, bool) {
	switch s {
	case gen.LibraryPreviewTokenRequestScopeFile:
		return PreviewScopeFile, true
	case gen.LibraryPreviewTokenRequestScopeBundle:
		return PreviewScopeBundle, true
	default:
		return "", false
	}
}

// writeMintError maps a store refusal onto a status. Every branch is an
// explicit sentinel match — never a string comparison on the message.
func writeMintError(w http.ResponseWriter, workspaceID string, err error) {
	switch {
	case errors.Is(err, ErrPreviewTokenCap):
		// 429, matching the contract: the caller is not wrong, they are
		// holding too many live previews at once (FR-003k).
		jsonErr(w, http.StatusTooManyRequests,
			"too many live previews for this session — close one and try again")
	case errors.Is(err, ErrPreviewTokenSession):
		jsonErr(w, http.StatusUnauthorized,
			"preview tokens can only be minted by an authenticated session")
	case errors.Is(err, ErrPreviewTokenWorkspace),
		errors.Is(err, ErrPreviewTokenScope),
		errors.Is(err, ErrPreviewTokenPath):
		jsonErr(w, http.StatusBadRequest, "invalid preview token request")
	case errors.Is(err, ErrPreviewTokenEntropy):
		// FR-003h fail-closed: no token was issued. This is a 500 because the
		// machine's random source failed, which is not something the caller
		// can fix by changing the request.
		logger.ErrorCF("rest", "library preview: token entropy source failed",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
	default:
		logger.ErrorCF("rest", "library preview: mint failed",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
	}
}

// libraryPreviewURL builds the gateway-relative URL for a token and a
// workspace-relative document path.
//
// Each path segment is percent-encoded via net/url so a file name containing
// a space, a '#' or a '?' produces a URL that still resolves to that file.
// Building the string by concatenation instead truncates at the first '#',
// which shows up as a missing subresource and nothing else.
func libraryPreviewURL(token, relPath string) string {
	u := url.URL{Path: libraryPreviewPathPrefix + token + "/" + relPath}
	return u.EscapedPath()
}

// --- GET|HEAD /library-preview/<token>/<relative-path> (FR-003a, FR-003i, FR-003j) ---

// handleServeLibraryPreview serves one file from a token's scope.
//
// Order of operations is a requirement, not a style choice:
//
//	headers → method gate → token → path shape → scope → syscall-confined open
//
// The method gate is second because FR-003j says 405 "without reading a
// body", and because FR-003f's CSRF argument needs the 405 to happen for
// every non-GET/HEAD method regardless of whether the token is any good.
func (p *libraryPreviewRoutes) handleServeLibraryPreview(w http.ResponseWriter, r *http.Request) {
	setLibraryPreviewSecurityHeaders(w)

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Allow lists exactly the two verbs this route answers. The request
		// body is never touched — not parsed, not read, not drained.
		w.Header().Set("Allow", "GET, HEAD")
		writeLibraryPreviewErrorPage(w, r, http.StatusMethodNotAllowed,
			"Method not allowed",
			"This preview address answers GET and HEAD requests only.")
		return
	}

	rest, hasPrefix := strings.CutPrefix(r.URL.Path, libraryPreviewPathPrefix)
	if !hasPrefix {
		writeLibraryPreviewTokenFailure(w, r)
		return
	}
	token, relRaw, _ := strings.Cut(rest, "/")
	if token == "" {
		writeLibraryPreviewTokenFailure(w, r)
		return
	}

	grant, live := p.tokens.Lookup(token)
	if !live {
		// Expired, revoked and unknown all land here, because the store
		// deliberately does not distinguish them either (FR-003n).
		writeLibraryPreviewTokenFailure(w, r)
		return
	}

	rel, cleanErr := library.CleanRelPath(relRaw)
	if cleanErr != nil || rel == "" {
		writeLibraryPreviewNotFound(w, r)
		return
	}
	// String-level scope check, layered BEFORE — never instead of — the
	// syscall-confined open below.
	if !grant.AllowsRelPath(rel) {
		writeLibraryPreviewNotFound(w, r)
		return
	}

	f, fi, openErr := openWithinPreviewScope(p.api.homePath, grant, rel)
	if openErr != nil {
		if !errors.Is(openErr, os.ErrNotExist) {
			// A containment refusal (os.Root returning "path escapes from
			// parent") is logged, because it is either a bug or an attack and
			// both are worth seeing. The response stays an ordinary 404: the
			// caller learns nothing about which it was.
			logger.WarnCF("rest", "library preview: refused to serve path",
				map[string]any{
					"workspace_id": grant.WorkspaceID,
					"scope_root":   grant.ScopeRoot,
					"error":        openErr.Error(),
				})
		}
		writeLibraryPreviewNotFound(w, r)
		return
	}
	defer f.Close()

	if fi.IsDir() {
		// A directory is not a document. There is no listing on this route —
		// a token grants files inside a bundle, never an index of them.
		writeLibraryPreviewNotFound(w, r)
		return
	}

	// ONE helper decides Content-Type, the §10.4 inline/attachment split and
	// the §10.3 policy, for this route and every other route in this package
	// that carries workspace bytes (FR-008b, FR-008c, FR-015a, FR-015b). The
	// preview variant differs from the plain one in exactly one way, and it is
	// the way §10.3 requires: an attachment response HERE still carries the
	// policy ("every response on the preview-token path ... whatever the
	// file's type"), where the authenticated path's attachment carries none.
	//
	// Setting the type BEFORE any byte is written is a correctness requirement,
	// not an ordering preference: it is the one and only condition under which
	// http.ServeContent's sniffing branch is unreachable.
	applyLibraryPreviewByteHeaders(w, path.Base(rel))

	ext := libraryExtOf(rel)
	if libraryPreviewNeedsCORS(ext) {
		// FR-019. A webfont inside a sandboxed bundle is fetched by a document
		// whose origin is OPAQUE, so the request is cross-origin and arrives
		// with "Origin: null" — without this header the browser discards the
		// response and the page renders in a fallback face. "*" is the correct
		// value and is not a widening: the request carries no credentials (it
		// cannot — the origin is opaque), and the bytes are already gated by
		// the token in the path.
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	// The name argument is deliberately empty. It exists only so ServeContent
	// can guess a type from the extension — the guess FR-015b forbids — and
	// passing "" removes the branch rather than stepping around it. What is
	// kept is what ServeContent is genuinely good for: Range requests, so a
	// bundle's audio and video can seek, conditional GETs, and correct HEAD
	// semantics.
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// libraryPreviewNeedsCORS reports whether an extension is a webfont, the one
// group FR-019 requires Access-Control-Allow-Origin for. Kept as its own
// predicate rather than inlined so the §10.4 font row and this list can be
// compared by test instead of by eye.
func libraryPreviewNeedsCORS(ext string) bool {
	switch strings.ToLower(ext) {
	case ".woff2", ".woff", ".ttf", ".otf":
		return true
	default:
		return false
	}
}

// openWithinPreviewScope opens rel for reading, confined at the SYSCALL
// boundary to the grant's own scope root (FR-003i).
//
// THIS IS THE FUNCTION THE SPEC CALLS THE MOST SERIOUS GAP IN STAGE 1, so the
// mechanism is spelled out. Two roots are opened, in order:
//
//  1. A base os.Root at the workspace work tree — or at a mount's real target
//     directory when the scope lies inside a mounted folder, which is what
//     makes previews work for the operator's own folders at all.
//  2. From that base, an os.Root at the scope's DIRECTORY, reached with
//     (*os.Root).OpenRoot so the walk to it is itself confined.
//
// The file is then opened relative to root (2). os.Root follows symlinks but
// refuses any that resolve outside it, at path-walk time rather than by a
// prior lexical check — so a symlink planted INSIDE a bundle pointing at
// /etc/passwd, or at a sibling directory elsewhere in the same workspace, is
// refused here even though it passes every string-level traversal test and
// even though it never leaves the workspace.
//
// For a file grant the confining root is the file's own directory and the
// only name openable within it is the file's own base name, which the
// caller's AllowsRelPath check has already pinned.
func openWithinPreviewScope(home string, g PreviewGrant, rel string) (*os.File, os.FileInfo, error) {
	scopeDir, within, splitErr := previewScopeSplit(g, rel)
	if splitErr != nil {
		return nil, nil, splitErr
	}

	base, baseSub, baseErr := openPreviewScopeBase(home, g.WorkspaceID, scopeDir)
	if baseErr != nil {
		return nil, nil, baseErr
	}
	defer base.Close()

	scopeRoot, scopeErr := base.OpenRoot(baseSub)
	if scopeErr != nil {
		return nil, nil, scopeErr
	}
	// Closing the root does not invalidate a file already opened through it.
	defer scopeRoot.Close()

	f, openErr := scopeRoot.Open(within)
	if openErr != nil {
		return nil, nil, openErr
	}
	fi, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return nil, nil, statErr
	}
	return f, fi, nil
}

// previewScopeSplit resolves a granted relative path into the directory that
// will confine the open and the name to open inside it.
//
// Bundle: the scope root IS the confining directory. File: the confining
// directory is the file's parent, and the only openable name is its base —
// which is stricter than the grant needs to be and deliberately so.
func previewScopeSplit(g PreviewGrant, rel string) (scopeDir, within string, err error) {
	switch g.Scope {
	case PreviewScopeBundle:
		if rel == g.ScopeRoot {
			// The bundle root itself. Resolved as "." so the caller's IsDir
			// check produces the not-found answer rather than a listing.
			return g.ScopeRoot, ".", nil
		}
		sub, ok := strings.CutPrefix(rel, g.ScopeRoot+"/")
		if !ok || sub == "" {
			return "", "", os.ErrNotExist
		}
		return g.ScopeRoot, sub, nil
	case PreviewScopeFile:
		if rel != g.ScopeRoot {
			return "", "", os.ErrNotExist
		}
		return path.Dir(g.ScopeRoot), path.Base(g.ScopeRoot), nil
	default:
		return "", "", os.ErrNotExist
	}
}

// openPreviewScopeBase opens the os.Root that scopeDir is reached from, and
// returns scopeDir expressed relative to it.
//
// Mounts are the reason this is not a one-liner. A mounted folder appears in
// the work tree as a symlink to somewhere else on the operator's disk, and an
// os.Root at the work tree correctly REFUSES to follow it. The authenticated
// Library path handles this by opening one os.Root per mount
// (library.Root.openMountRoots); this mirrors that, using library.Root's own
// MountAt so the two cannot disagree about which paths are inside a mount.
func openPreviewScopeBase(home, workspaceID, scopeDir string) (*os.Root, string, error) {
	lib, libErr := library.OpenRoot(home, workspaceID)
	if libErr != nil {
		return nil, "", libErr
	}
	defer lib.Close()

	if _, target, _, isMount := lib.MountAt(scopeDir); isMount && target != "" {
		_, rest, _ := strings.Cut(scopeDir, "/")
		if rest == "" {
			rest = "."
		}
		root, err := os.OpenRoot(target)
		if err != nil {
			return nil, "", err
		}
		return root, rest, nil
	}

	workDir, wdErr := workspace.SafeWorkDir(home, workspaceID)
	if wdErr != nil {
		return nil, "", wdErr
	}
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return nil, "", err
	}
	sub := scopeDir
	if sub == "" {
		sub = "."
	}
	return root, sub, nil
}

// --- Error pages ---

// writeLibraryPreviewTokenFailure answers an expired, revoked or unknown
// token (FR-003n).
//
// ONE response for all three, byte for byte. A 410-for-expired /
// 404-for-unknown split would be a working oracle for whether a token ever
// existed, which is the same as confirming a guess. The body is
// human-readable HTML rather than an empty 404 because the frame is
// cross-origin and opaque: the SPA cannot read the status, so whatever this
// writes IS the only thing the reader sees (FR-003c — "a visible error rather
// than a blank frame").
func writeLibraryPreviewTokenFailure(w http.ResponseWriter, r *http.Request) {
	writeLibraryPreviewErrorPage(w, r, http.StatusNotFound,
		"This preview link is no longer valid",
		"Preview links last 15 minutes and stop working when you sign out. "+
			"Close this frame and open the file again from the Library to get a fresh one.")
}

// writeLibraryPreviewNotFound answers a live token whose request names
// something that is not there, or not inside the token's scope.
//
// Deliberately a DIFFERENT body from the token failure: the two say different
// true things to the reader, and FR-003n's indistinguishability requirement is
// specifically about expired-vs-revoked-vs-unknown, not about every 404 this
// route can produce. Anyone who reaches this branch already holds a valid
// token, so nothing is disclosed by telling them their file is missing.
func writeLibraryPreviewNotFound(w http.ResponseWriter, r *http.Request) {
	writeLibraryPreviewErrorPage(w, r, http.StatusNotFound,
		"File not found in this preview",
		"The preview is still valid, but it does not contain the file that was requested.")
}

// writeLibraryPreviewErrorPage writes a self-contained HTML error page.
//
// Self-contained matters: default-src 'none' means this page may load no
// external stylesheet, script, image or font, so everything it needs is
// inline (which style-src's 'unsafe-inline' permits). The security headers
// are re-applied here so that a caller which reached this function on a path
// that skipped setLibraryPreviewSecurityHeaders still cannot emit a
// policy-free response.
func writeLibraryPreviewErrorPage(w http.ResponseWriter, r *http.Request, status int, heading, detail string) {
	setLibraryPreviewSecurityHeaders(w)
	body := libraryPreviewErrorHTML(heading, detail)
	h := w.Header()
	h.Set(headerContentType, "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r != nil && r.Method == http.MethodHead {
		return
	}
	if _, err := io.WriteString(w, body); err != nil {
		logger.WarnCF("rest", "library preview: error page write failed",
			map[string]any{"error": err.Error()})
	}
}

// libraryPreviewErrorHTML renders the error page.
//
// heading and detail are compiled-in constants at every call site, never
// caller-supplied — no request-derived text reaches this template, so there is
// nothing here to escape. If that ever changes, escape it: this page is served
// as real HTML on a path anyone holding a URL can reach.
func libraryPreviewErrorHTML(heading, detail string) string {
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + heading + `</title>
<style>
  :root { color-scheme: dark light; }
  body {
    margin: 0; min-height: 100vh;
    display: flex; align-items: center; justify-content: center;
    background: #0A0A0B; color: #E2E8F0;
    font-family: system-ui, -apple-system, "Segoe UI", sans-serif;
    line-height: 1.5;
  }
  main { max-width: 32rem; padding: 2rem; text-align: center; }
  h1 { font-size: 1.125rem; font-weight: 600; margin: 0 0 0.5rem; }
  p { margin: 0; font-size: 0.9375rem; opacity: 0.75; }
</style>
</head>
<body>
<main>
<h1>` + heading + `</h1>
<p>` + detail + `</p>
</main>
</body>
</html>
`
}
