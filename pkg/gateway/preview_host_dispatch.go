// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — the ADR-094 Mode 1 preview host-dispatch mux
// (FR-006/FR-007/FR-008, DS-1/DS-3, S-2.2, S-2.5–S-2.11).
//
// A request whose Host is <label>.localhost:<canonical-port> is dispatched to
// a preview-host mux mounting EXACTLY one handler — preview serving —
// structurally bypassing the CSRF gate, the planted-cookie guard and the
// navigation guard, which remain on the main branch only (FR-028, MIN-007:
// the exemption is scoped by dispatch, not by the /preview/ path prefix).
// Everything else falls through to the main chain untouched.
//
// Classification is by host SHAPE plus PORT AGREEMENT with the live canonical
// origin — deliberately NOT Mode-dependent (F-1: on an https Mode 2
// deployment the label registry is still consulted so an unknown label 404s
// via the preview mux instead of leaking the request into the main mux).
// Grammar-invalid label hosts and port-mismatched hosts never dispatch
// (DS-1 incoming rows, DS-3 rows 11–12): they fall through and stay fully
// CSRF-gated on state changes (S-2.11).
//
// The label registry IS the token registries: a label resolves through
// LookupByLabel on the two registration stores (dev first, then static, with
// the dev-preference for static entries — OneCodePath: a static registration
// whose agent has a live dev server serves the dev server, exactly like the
// Mode 2 path's dev-first lookup), so the FR-008 guarantee ("the label maps
// to the same registry entry as the Mode 2 token") is structural — there is
// no second store to drift.
package gateway

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// ---------------------------------------------------------------------------
// context plumbing
// ---------------------------------------------------------------------------

// previewHostContextKey carries the resolved label for a dispatched request
// downstream (HandlePreview's Mode 1 branch reads it to resolve the
// registration).
type previewHostContextKey struct{}

// WithPreviewHostLabel returns a context carrying the dispatched label.
func WithPreviewHostLabel(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, previewHostContextKey{}, label)
}

// previewHostLabelFromContext returns the dispatched label, or "" for a
// request that was not dispatched.
func previewHostLabelFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	label, _ := ctx.Value(previewHostContextKey{}).(string)
	return label
}

// ---------------------------------------------------------------------------
// dispatch classification
// ---------------------------------------------------------------------------

// classifyPreviewHostRequest reports whether the request's Host claims the
// preview-host mux: a grammar-valid <label>.localhost shape (F-2 — the Host
// is matched case-insensitively; the label is returned lower-cased) whose
// port agrees with the live canonical origin's port (DS-3 rows 11–12: a
// wrong-port or portless Host falls through to the main mux). The canonical
// origin is resolveMainOrigin's — the SAME origin vocabulary the mint consults
// (FR-019: one classification, one port). No registry consult here:
// classification is structural.
func classifyPreviewHostRequest(r *http.Request, cfg *config.Config) (string, bool) {
	label, hostPort, ok := middleware.ParsePreviewLabelHost(r.Host)
	if !ok {
		return "", false
	}
	if !previewPortAgrees(hostPort, resolveMainOrigin(cfg)) {
		return "", false
	}
	return label, true
}

// previewPortAgrees reports whether a label Host's port agrees with the
// canonical origin's port, compared on EFFECTIVE ports: a missing port on
// either side means the scheme default (80 for http, 443 for https) of the
// canonical origin's scheme. A browser omits the origin's default port from
// the Host header, so the portless form the mint emits on an implicit-80
// origin (DS-3 row 10) arrives as Host "<label>.localhost" with no port —
// the two portless sides agree (fix3, CR2). One-sided or mismatched ports
// never agree (DS-3 rows 11–12), and an unusable canonical origin ("",
// wildcard bind) agrees with nothing — the deployment has no
// browser-realistic Mode 1 origin, so nothing dispatches.
func previewPortAgrees(hostPort, canonicalOrigin string) bool {
	if canonicalOrigin == "" {
		return false
	}
	u, err := url.Parse(canonicalOrigin)
	if err != nil || u.Host == "" {
		return false
	}
	defaultPort := previewSchemeDefaultPort(u.Scheme)
	originPort := u.Port()
	if originPort == "" {
		originPort = defaultPort
	}
	if hostPort == "" {
		// A portless Host is the origin scheme's default port: the browser
		// strips it, and the mint emits the portless URL for exactly this
		// origin shape (DS-3 row 10, fix3 CR2).
		hostPort = defaultPort
	}
	return hostPort == originPort
}

// previewSchemeDefaultPort is the portless effective port for a scheme: 443
// for https (any case), 80 for everything else. The canonical origin is the
// only scheme source — a Host header carries none.
func previewSchemeDefaultPort(scheme string) string {
	if strings.ToLower(scheme) == "https" {
		return "443"
	}
	return "80"
}

// ---------------------------------------------------------------------------
// per-label token bucket (FR-027 / S-7.2)
// ---------------------------------------------------------------------------

// previewLabelRateLimitBurst is the per-label token-bucket capacity: the
// number of requests a label may burst before its bucket is drained. The
// exact value is GREEN's choice (no preview limiter existed to copy); 60
// sits above any previewed app's legitimate initial page-load fan-out
// (HTML + assets in one burst) and far below the 100-request burst S-7.2
// drives.
const previewLabelRateLimitBurst = 60

// previewLabelRateLimitRefill is the steady-state refill: one request per
// second per label, enough for a human-paced preview session, far below an
// aggressive scraper.
const previewLabelRateLimitRefill = time.Second

// previewLabelLimiter is the per-label token-bucket rate limiter applied to
// dispatched Mode 1 requests (FR-027/S-7.2: one label throttled; other labels
// and the main host unaffected — the bucket key is the label, and the
// limiter runs only on the dispatch path).
//
// Admission is per label from a label's FIRST request (fix4): a label that
// resolves through the preview registries draws ONLY its own bucket — never
// the shared unknown-label budget — so a flood of junk labels cannot deny a
// legitimate label's first request (FR-027's "one label's throttle leaves
// others untouched"). Only labels that resolve to NO registration draw the
// shared unknown-label budget, and they allocate no per-label state.
//
// Its state is BOUNDED (fix3, A6/SL-F1/CR4): the preview Host is
// unauthenticated attacker input, so the bucket map is bounded twice over —
// buckets idle beyond previewLabelBucketIdleTTL are swept on every
// admission, and the map is hard-capped at previewLabelBucketCap with
// least-recently-used eviction at the cap. The unknown budget is one bucket:
// constant memory.
type previewLabelLimiter struct {
	mu      sync.Mutex
	buckets map[string]*previewLabelBucket
	// unknown is the shared budget every non-resolving label admits
	// through. Lazily initialised (tests construct the bare struct).
	unknown *previewLabelBucket
}

type previewLabelBucket struct {
	tokens float64
	last   time.Time
}

// previewLabelLimiters is the package-DEFAULT limiter, not the dispatcher's:
// the production restAPI is constructed with its own instance
// (restAPI.labelLimiters, set at boot), and every preview chain harness does
// the same, so two instances in one process can no longer throttle each
// other through shared state (fix4). The package default is the zero-value
// fallback for bare restAPI literals and the seam the round-3 RED pack
// drives directly.
var previewLabelLimiters = &previewLabelLimiter{
	buckets: make(map[string]*previewLabelBucket),
}

// previewLabelBucketIdleTTL is how long a per-label bucket may sit untouched
// before the sweep drops it: long enough to outlast a normal preview
// session's request gaps, short enough that abandoned labels release their
// memory.
const previewLabelBucketIdleTTL = 5 * time.Minute

// previewLabelBucketCap is the hard cap on the per-label bucket map. At the
// cap, allocateLocked evicts least-recently-used buckets for a new label —
// real deployments hold one bucket per live registration, so the cap binds
// only when registrations churn past it.
const previewLabelBucketCap = 256

// admit decides one dispatched request's admission. A label with its own
// bucket draws it. An unseen label routes by RESOLUTION (fix4): a label
// that resolves through the preview registries gets its own full bucket and
// admits — its first sight never draws the shared unknown-label budget
// (FR-027: one label's throttle leaves others untouched), while a label
// that resolves to no registration draws the shared unknown budget and
// allocates no per-label state: unauthenticated clients can spray unique
// grammar-valid labels, and none of them may grow process memory.
func (l *previewLabelLimiter) admit(label string, resolves bool) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)
	if b, ok := l.buckets[label]; ok {
		return takeLocked(b, now)
	}
	if !resolves {
		if l.unknown == nil {
			l.unknown = &previewLabelBucket{
				tokens: previewLabelRateLimitBurst,
				last:   now,
			}
		}
		// The shared budget carries the same burst/refill as a per-label
		// one, so a spray of distinct non-resolving labels shares ONE
		// label-sized budget.
		return takeLocked(l.unknown, now)
	}
	l.allocateLocked(label, now)
	return takeLocked(l.buckets[label], now)
}

// allow is the per-label admission primitive: an unseen label is treated as
// resolving, so it gets its own bucket on first sight and admits. The
// dispatcher routes through admit with the live registry resolution
// instead; this primitive exists for the per-label property tests and any
// caller that wants pure FR-027 per-label semantics with no unknown-label
// concept.
func (l *previewLabelLimiter) allow(label string) bool {
	return l.admit(label, true)
}

// allowUnknown draws the shared unknown-label budget for one non-resolving
// label. The empty label never occupies the bucket map (classification
// never yields ""), so admit("", false) always reaches the unknown branch.
func (l *previewLabelLimiter) allowUnknown() bool {
	return l.admit("", false)
}

// allocateLocked inserts label's own full bucket, keeping the map bounded
// twice over: idle buckets beyond the TTL are swept first, then
// least-recently-used buckets are evicted while the map sits at the cap.
// Caller holds mu.
func (l *previewLabelLimiter) allocateLocked(label string, now time.Time) {
	l.sweepLocked(now)
	for len(l.buckets) >= previewLabelBucketCap {
		oldestKey, oldestAt := "", now
		for key, b := range l.buckets {
			if b.last.Before(oldestAt) {
				oldestKey, oldestAt = key, b.last
			}
		}
		if oldestKey == "" {
			break
		}
		delete(l.buckets, oldestKey)
	}
	l.buckets[label] = &previewLabelBucket{
		tokens: previewLabelRateLimitBurst,
		last:   now,
	}
}

// takeLocked refills b by elapsed time and consumes one token, reporting
// whether the consumption succeeded. Caller holds l.mu.
func takeLocked(b *previewLabelBucket, now time.Time) bool {
	elapsed := now.Sub(b.last)
	b.tokens += elapsed.Seconds() / previewLabelRateLimitRefill.Seconds()
	if b.tokens > previewLabelRateLimitBurst {
		b.tokens = previewLabelRateLimitBurst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweepLocked drops buckets idle beyond previewLabelBucketIdleTTL. Caller
// holds mu; deleting during range is safe in Go.
func (l *previewLabelLimiter) sweepLocked(now time.Time) {
	for key, b := range l.buckets {
		if now.Sub(b.last) >= previewLabelBucketIdleTTL {
			delete(l.buckets, key)
		}
	}
}

// promote refreshes label's per-label bucket on a successful registry
// resolution (the serve path calls it so an actively served label's bucket
// stays LRU-fresh and the map holds one bucket per live registration). The
// label's budget never draws on the shared unknown-label budget: one label
// being hammered cannot consume any other label's burst, which is the
// FR-027 per-label guarantee. Admission itself resolves first (admit), so
// promote only maintains the bucket the label already owns.
//
// Bounded like every insertion: idle buckets are swept and
// least-recently-used buckets are evicted while the map sits at the cap, so
// promoted labels never grow the map without bound either.
func (l *previewLabelLimiter) promote(label string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[label]; ok {
		b.last = now
		return
	}
	l.allocateLocked(label, now)
}

// ---------------------------------------------------------------------------
// the dispatcher middleware
// ---------------------------------------------------------------------------

// previewHostDispatchMW is the outermost middleware of the production chain:
// it claims <label>.localhost:<canonical-port> Hosts for the preview-host mux
// and passes everything else to the main chain unchanged.
//
// Dispatched requests are served through configSnapshotMiddleware (the live
// config snapshot — the hot-flip row reads preview_enabled per request) and
// HandlePreview's Mode 1 branch, and NOTHING else: the CSRF gate, the
// planted-cookie guard and the navigation guard sit on the main branch after
// this middleware and are structurally unreachable under a label Host.
func (a *restAPI) previewHostDispatchMW(next http.Handler) http.Handler {
	dispatched := a.configSnapshotMiddleware(http.HandlerFunc(a.HandlePreview))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := configFromContext(r.Context())
		if cfg == nil {
			cfg = a.agentLoop.GetConfig()
		}
		label, ok := classifyPreviewHostRequest(r, cfg)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		// Admission resolves FIRST for an unseen label (fix4): a label that
		// resolves gets its own per-label bucket and never draws the shared
		// unknown-label budget, so a junk-label flood cannot 429 a
		// legitimate label's first request (FR-027). Only labels resolving
		// to no registration consume the unknown budget. The resolution is
		// the same dev-first registry read servePreviewByLabel serves
		// with — an in-memory scan under the store locks, paid only until
		// the label owns a bucket.
		if !a.previewLabelLimiter().admit(label, a.previewLabelResolves(label)) {
			writeDevProxyError(w, http.StatusTooManyRequests, "rate limited")
			return
		}
		dispatched.ServeHTTP(w, r.WithContext(WithPreviewHostLabel(r.Context(), label)))
	})
}

// previewLabelLimiter returns THIS gateway instance's preview per-label
// limiter (fix4): admission state belongs to the restAPI/dispatcher, not to
// package state, so two instances in one process — boot plus tests, or
// parallel harnesses — never throttle each other through a shared map. Nil
// (bare test literals) falls back to the package default
// previewLabelLimiters, which stays the RED pack's direct seam.
func (a *restAPI) previewLabelLimiter() *previewLabelLimiter {
	if a.labelLimiters != nil {
		return a.labelLimiters
	}
	return previewLabelLimiters
}

// previewLabelResolves reports whether the label currently resolves through
// the SAME registries, in the SAME dev-first order, servePreviewByLabel
// serves with (OneCodePath): admission routing must not disagree with the
// serving lookup, or a resolving label could be unknown-capped (a 429 the
// registry would have served) and a non-resolving one promoted. Both
// lookups are in-memory scans under their registry locks.
func (a *restAPI) previewLabelResolves(label string) bool {
	if a.devServers != nil && a.devServers.LookupByLabel(label) != nil {
		return true
	}
	return a.servedSubdirs != nil && a.servedSubdirs.LookupByLabel(label) != nil
}

// ---------------------------------------------------------------------------
// Mode 1 serving (HandlePreview's label branch)
// ---------------------------------------------------------------------------

// servePreviewByLabel resolves a dispatched label to its registration and
// serves it — the Mode 1 half of HandlePreview. Resolution order mirrors the
// Mode 2 path (OneCodePath): dev registry first, then the static registry
// with the dev-preference for its entries (a static registration whose agent
// has a live dev server proxies to the dev server), else the static file
// server, else 404.
//
// FR-026 (redaction): the label NEVER enters logs or audit details — the
// static branch audits with an empty token (token_prefix "<invalid>") and
// deduplicates serve.served under a "label:" key; the proxied branch audits
// with the registration's real token, which is not the label.
func (a *restAPI) servePreviewByLabel(w http.ResponseWriter, r *http.Request, label string, startedAt time.Time) {
	// Dev registry first — a label may resolve directly to a dev server.
	if a.devServers != nil {
		if reg := a.devServers.LookupByLabel(label); reg != nil {
			a.previewLabelLimiter().promote(label)
			remaining := strings.TrimPrefix(r.URL.Path, "/")
			a.proxyDevRequest(w, r, reg, remaining, reg.AgentID, reg.Token, startedAt)
			return
		}
	}

	// Static registry lookups that resolve promote the label to its own
	// bucket (fix3 A6: only labels that resolve allocate limiter state).
	if a.servedSubdirs != nil {
		if entry := a.servedSubdirs.LookupByLabel(label); entry != nil {
			a.previewLabelLimiter().promote(label)
			if a.devServers != nil {
				if reg := a.devServers.LookupByAgent(entry.AgentID); reg != nil {
					remaining := strings.TrimPrefix(r.URL.Path, "/")
					a.proxyDevRequest(w, r, reg, remaining, reg.AgentID, reg.Token, startedAt)
					return
				}
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				writeDevProxyError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.serveStaticFile(w, r, entry.AbsDir, strings.TrimPrefix(r.URL.Path, "/"),
				entry.AgentID, "label:"+label, "", startedAt)
			return
		}
	}

	// fix3 (SL-F1): at most one preview.label_unknown audit entry per
	// suppression window per remote IP — see maybeAuditLabelUnknown.
	a.maybeAuditLabelUnknown(r, startedAt)
	writeDevProxyError(w, http.StatusNotFound, "preview registration not found or expired")
}
