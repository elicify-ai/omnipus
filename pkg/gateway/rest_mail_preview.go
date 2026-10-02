package gateway

// rest_mail_preview.go - the Mail HTML preview (spec 2.3a, MC-10; w5
// US-6/MC-11/MC-12: metadata-only grants). The mint
// (POST /api/v1/mail/html-preview-token) DIALS NOTHING: it authorizes the
// pair and issues the ref-bound token (US-6.3). Every serve request fetches
// and sanitizes within its own request through the shared pool and budget,
// and holds bytes only for that request's lifetime (US-6.2). Signature
// preview grants keep their operator-supplied sanitized HTML at mint —
// that is not mail content, and their controls are unchanged.

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/email/mailhtml"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/microcosm-cc/bluemonday"
)

const (
	// mailPreviewMintPath is the session-authenticated mint endpoint.
	mailPreviewMintPath = "/api/v1/mail/html-preview-token"
	// mailPreviewMaxHTMLBytes is the served-HTML cap (MC-10(7)): the same
	// 256 KB bound the inbound decode already applies.
	mailPreviewMaxHTMLBytes = 256 << 10
	// mailImageMaxBytes / mailImageMaxSeconds are the MC-41 proxy bounds.
	mailImageMaxBytes   = 5 << 20
	mailImageMaxSeconds = 10
	// mailImageLimiter is the mint limiter's rate (MC-44): 10/min per IP.
	mailMintRatePerMinute = 10
	// mailSignatureMintRatePerMinute is the signature mint limiter's rate
	// (MC-44, decision): 60/min per IP — typing cadence is the load.
	mailSignatureMintRatePerMinute = 60
	// mailSignatureMaxChars is the signature_html bound (MC-1, contract
	// MailSignaturePreviewTokenRequest): characters, not bytes.
	mailSignatureMaxChars = 16384
)

// mailSignaturePreviewMintPath is the session-authenticated signature preview
// mint endpoint (decision: POST /api/v1/mail/signature-preview-token — the
// draft signature's live preview; never dials IMAP, never persists).
const mailSignaturePreviewMintPath = "/api/v1/mail/signature-preview-token"

// mailPreviewRoutes binds the mint endpoint and the serve prefix to one
// token store, and publishes that store on the restAPI so logout (rest_auth)
// can reach it - the single wiring step a caller cannot skip (the Library
// constructor's own lesson).
type mailPreviewRoutes struct {
	api    *restAPI
	tokens *mailPreviewTokenStore
}

// newMailPreviewRoutes builds the routes over one store and publishes it on
// the restAPI (logout revocation) - the wiring step no caller may skip.
func newMailPreviewRoutes(a *restAPI) *mailPreviewRoutes {
	routes := &mailPreviewRoutes{api: a, tokens: newMailPreviewTokenStore()}
	a.mailPreviewTokens.Store(routes.tokens)
	return routes
}

// mailPreviewTokenStoreOf is the nil-safe store reader.
func (a *restAPI) mailPreviewTokenStoreOf() *mailPreviewTokenStore {
	return a.mailPreviewTokens.Load()
}

// registerMailPreviewRoutes registers the mint endpoint (session-auth +
// MC-44 limiter) and the token-only serve prefix with the MC-10 header set
// applied BEFORE the limiter so even the 429 carries the policy.
func (a *restAPI) registerMailPreviewRoutes(cm httpHandlerRegistrar) {
	routes := newMailPreviewRoutes(a)
	cm.RegisterHTTPHandler(mailPreviewMintPath,
		a.withAuth(withRateLimit(mailMintLimiter, routes.handleMint)))
	cm.RegisterHTTPHandler(mailSignaturePreviewMintPath,
		a.withAuth(withRateLimit(mailSignatureMintLimiter, routes.handleSignatureMint)))
	serve := routes.serveHandler()
	cm.RegisterHTTPHandler(mailPreviewPathPrefix, serve)
}

// mailMintLimiter guards the mint endpoint (MC-44): dedicated 10/min per IP,
// separate from the panel mutation limiter.
var mailMintLimiter = newAPIRateLimiter(mailMintRatePerMinute, 1*time.Minute)

// mailSignatureMintLimiter guards the signature preview mint (MC-44, decision):
// its OWN dedicated 60/min per-IP instance — typing cadence is the load.
// Never the shared API limiter, never the message mint limiter.
var mailSignatureMintLimiter = newAPIRateLimiter(mailSignatureMintRatePerMinute, 1*time.Minute)

// serveHandler wraps the serving handler in a limiter with the MC-10 header
// set applied BEFORE the limiter, so even the 429 carries the policy.
func (p *mailPreviewRoutes) serveHandler() http.HandlerFunc {
	limited := withRateLimit(mailPreviewServeLimiter, p.handleServe)
	return func(w http.ResponseWriter, r *http.Request) {
		p.setMailPreviewSecurityHeaders(w)
		limited(w, r)
	}
}

// mailPreviewCanonicalOrigin resolves the browser-facing origin through the
// one resolver the spec names, tolerating a nil api.
func mailPreviewCanonicalOrigin(a *restAPI) string {
	if a == nil || a.agentLoop == nil {
		return ""
	}
	return middleware.CanonicalGatewayOrigin(a.agentLoop.GetConfig())
}

// mailPreviewServeLimiter bounds the unauthenticated serve prefix.
var mailPreviewServeLimiter = newAPIRateLimiter(120, 1*time.Minute)

// setMailPreviewSecurityHeaders applies the MC-10 header set to every
// response the prefix produces, refusals included.
func (p *mailPreviewRoutes) setMailPreviewSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", mailIsolationPolicy(mailPreviewCanonicalOrigin(p.api)))
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
}

// handleMint implements POST /api/v1/mail/html-preview-token (D13/D17; w5
// US-6.3/MC-12: mint dials nothing). It authorizes the pair, validates the
// folder slug, and issues the ref-bound metadata token. Every failure the
// old eager mint produced at mint time (missing message, oversized body,
// backoff, upstream error) now surfaces at the FIRST SERVE with the same
// safe classes — the failure moved, it did not disappear.
func (p *mailPreviewRoutes) handleMint(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req gen.MailHtmlPreviewTokenRequest
	if !decodeMailJSON(p.api, w, r, "MailHtmlPreviewTokenRequest", &req) {
		return
	}
	switch req.Folder {
	case "inbox", "sent", "drafts":
	default:
		jsonErr(w, http.StatusBadRequest, "unknown folder slug")
		return
	}
	sessionKey, ok := PreviewSessionKey(r)
	if !ok {
		jsonErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	// Authorization only: the pair must resolve to an authenticated client.
	// NewClient dials nothing (US-6.3) — the counter stays at zero here.
	if p.api.mailPairClient(w, req.AgentId, req.WorkspaceId) == nil {
		return
	}
	loadRemote := req.LoadRemote != nil && *req.LoadRemote
	token, merr := p.tokens.mint(sessionKey, mailPreviewGrant{
		WorkspaceID: req.WorkspaceId, AgentID: req.AgentId,
		Folder: string(req.Folder), Ref: req.MessageRef, LoadRemote: loadRemote,
	})
	// w5 US-7.6/MC-18, per the spec's frozen mint line: the metadata mint
	// emits its record under the frozen "open" category with ZERO mail
	// acquisition (source "none", socket_count 0 — the mint dialed nothing).
	p.api.emitMailOperationTiming("open", req.AgentId, req.WorkspaceId, started, merr, "none", false)
	if merr != nil {
		// A full per-session token table is the caller's doing, not a server
		// fault: refuse 429 with the actionable text and no ERROR log — the
		// 500 path stays for genuine mint failures.
		if errors.Is(merr, ErrMailPreviewCap) {
			jsonErr(w, http.StatusTooManyRequests, "too many concurrent previews: close older previews to mint a new one")
			return
		}
		slog.Error("rest: mail preview mint failed", "error", merr)
		jsonErr(w, http.StatusInternalServerError, "could not mint preview token")
		return
	}
	resp := gen.MailHtmlPreviewTokenResponse{Token: token, ExpiresInSeconds: int(MailPreviewTokenTTL / time.Second)}
	jsonOK(w, resp)
}

// handleSignatureMint implements POST /api/v1/mail/signature-preview-token
// (decision memo email-arch-sigcsp, "Decision — exact contract shape"): the
// draft signature's live preview. It NEVER dials IMAP — the request carries
// no workspace/agent/folder/ref — and never persists: the sanitized HTML
// lives only in the in-memory token store and dies with the token (logout
// revocation or the 2-minute TTL).
func (p *mailPreviewRoutes) handleSignatureMint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req gen.MailSignaturePreviewTokenRequest
	if !decodeMailJSON(p.api, w, r, "MailSignaturePreviewTokenRequest", &req) {
		return
	}
	sessionKey, ok := PreviewSessionKey(r)
	if !ok {
		jsonErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	// MC-1: 1..16384 characters (chars, not bytes).
	if n := utf8.RuneCountInString(req.SignatureHtml); n < 1 || n > mailSignatureMaxChars {
		jsonErr(w, http.StatusBadRequest, "signature_html must be 1..16384 characters")
		return
	}
	sanitized, serr := email.SanitizeSignatureHTML(req.SignatureHtml)
	if serr != nil {
		jsonErr(w, http.StatusBadRequest, serr.Error())
		return
	}
	// T38 (MIN-001 resolution): the signature's https images ride the
	// existing MC-41-pinned proxy — extract from the FR-003-sanitized HTML,
	// rewrite onto token-scoped paths, and bind LoadRemote so no load-images
	// step is needed.
	remoteURLs := mailExtractRemoteImageURLs(sanitized)
	previewHTML := mailRewritePreviewSources(sanitized, nil, remoteURLs)
	previewHTML = mailHardenAnchors(previewHTML)
	token, merr := p.tokens.mint(sessionKey, mailPreviewGrant{
		Kind:       mailPreviewKindSignature,
		LoadRemote: true,
		HTML:       previewHTML,
		RemoteURLs: remoteURLs,
	})
	if merr != nil {
		// No cap can refuse a signature mint (replace-on-mint); a failure
		// here is entropy, a genuine server fault.
		slog.Error("rest: mail signature preview mint failed", "error", merr)
		jsonErr(w, http.StatusInternalServerError, "could not mint preview token")
		return
	}
	jsonOK(w, gen.MailSignaturePreviewTokenResponse{Token: token, ExpiresInSeconds: int(MailSignaturePreviewTokenTTL / time.Second)})
}

// mailTokenPlaceholder stands in for the token inside sanitized HTML; it is
// substituted with the real token only AFTER a successful mint — and ONLY
// inside the path shapes the pipeline itself minted (mailSubstitutePreviewToken):
// the placeholder is a fixed literal, so a bare whole-document substitution
// would also fire inside a sender-typed occurrence — an href carrying it to
// a foreign host delivered the live bearer token to that host (the F2
// finding).
const mailTokenPlaceholder = "MAILPREVIEWTOKENPLACEHOLDER"

// mailTokenPathRe matches exactly the two path forms mailRewritePreviewSources
// mints around the placeholder — the token-scoped /mail-preview/part|img
// index paths ("/PH/<i>" from the src rewrite, "PH<i>" from the CSS url()
// rewrite). An href is never such a form, so the token substitution below
// cannot fire inside any attribute a sender controls.
var mailTokenPathRe = regexp.MustCompile(`(/mail-preview/(?:part|img)/)` + regexp.QuoteMeta(mailTokenPlaceholder) + `((?:/\d+|\d+))`)

// mailSubstitutePreviewToken writes the live serve token into the preview
// paths, and only there:
//   - the shape-scoped replacement fires solely inside the minted
//     /mail-preview/part|img path forms (their values are wholly
//     pipeline-generated; the /mail-preview/img form without a slash before
//     the index is the CSS url() rewrite's own emission);
//   - every surviving literal is scrubbed, so a sender-typed placeholder —
//     in an href, a class, a text node — never leaves the frame carrying
//     the constant, with or without the token.
func mailSubstitutePreviewToken(html, token string) string {
	html = mailTokenPathRe.ReplaceAllStringFunc(html, func(m string) string {
		i := strings.Index(m, mailTokenPlaceholder)
		return m[:i] + token + m[i+len(mailTokenPlaceholder):]
	})
	return strings.ReplaceAll(html, mailTokenPlaceholder, "")
}

// mailSanitizePreviewHTML is the named inbound sanitizer (MC-10(5)/MC-40):
// strips meta/base/forms/scripts/handlers, rewrites cid: and remote image
// srcs onto the token-scoped mail-preview paths, hardens every surviving
// anchor — and, since the styling wave (F4/#1172), preserves SAFE styling
// through the parsed CSS policy (pkg/email/mailhtml): style attributes via
// bluemonday's own decode-then-check layer, <style> blocks via the
// douceur-based stylesheet pass, the 73-property allow table, and the two
// sanitizer-only gates (data:-font @font-face and data:image/svg+xml in CSS
// url() stay stripped). The result is what the token store holds.
func mailSanitizePreviewHTML(raw string, inlines []email.MailPart, remoteURLs []string) string {
	// Pin-then-rewrite FIRST, while the original https url() values are
	// still in the text — the CSS rewrite replaces them inside style
	// attributes AND <style> blocks alike.
	rewritten := mailRewritePreviewSources(raw, inlines, remoteURLs)
	// Lift the <style> blocks for the stylesheet sanitizer (bluemonday
	// cannot sanitize element content without AllowUnsafe), then drop
	// over-cap style attributes (bounded work, P8).
	prepared, blocks := mailhtml.ExtractStyleBlocks(rewritten, mailhtml.StyleMaxBlockBytes)
	prepared = mailhtml.StripOversizedStyleAttrs(prepared)
	// P4 decode-before-check on the attribute path: identifier escapes are
	// decoded through the declaration parser, so the browser's reading of
	// the property name is what the policy judges.
	prepared = mailhtml.DecodeStyleAttrProperties(prepared)
	sanitizedBlocks := make([]string, len(blocks))
	for i, b := range blocks {
		sanitizedBlocks[i] = mailhtml.SanitizeStylesheet(b, mailhtml.StyleMaxBlockBytes)
	}
	p := bluemonday.NewPolicy()
	for _, el := range []string{"p", "div", "span", "br", "b", "strong", "i", "em", "u", "s",
		"ul", "ol", "li", "table", "thead", "tbody", "tfoot", "tr", "td", "th",
		"h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "code", "hr", "a", "img"} {
		p.AllowElements(el)
	}
	// The style ELEMENT carries only the placeholder text here; its content
	// is re-inserted post-sanitize from the sanitized blocks. The font
	// element and the presentation attribute tables come from the artefact's
	// markup table.
	p.AllowElements("style")
	mailhtml.AllowPresentationMarkup(p)
	// The parsed CSS policy: the ONE 73-property table, applied to style
	// attributes (decode-then-check, fail-closed on parse error).
	mailhtml.ApplyStylePolicy(p)
	p.AllowAttrs("alt", "width", "height", "align").OnElements("img")
	p.AllowAttrs("href").Matching(mailAnchorHrefRe).OnElements("a")
	p.AllowAttrs("src").Matching(mailImageSrcRe).OnElements("img")
	html := p.Sanitize(prepared)
	html = mailhtml.ReplaceStyleBlocks(html, sanitizedBlocks)
	html = mailHardenAnchors(html)
	return html
}

// mailAnchorHrefRe allows only http/https/mailto anchors after rewrite, and
// anchors the END too: a value that merely BEGINS with an allowed scheme used
// to survive with arbitrary trailing content — the shape that carried the
// token placeholder (and now meets the scrub instead) into a foreign host
// (the F2 finding). A URL is scheme + whitespace-free remainder; malformed
// whitespace-bearing values drop (fail-safe: the link vanishes).
var mailAnchorHrefRe = regexp.MustCompile(`^(?:https?|mailto):\S*$`)

// mailImageSrcRe allows data: images and the token-scoped preview paths.
var mailImageSrcRe = regexp.MustCompile(`^(?:/mail-preview/(?:part|img)/|data:image/(?:png|gif|jpe?g|webp);base64,)`)

// mailRewritePreviewSources rewrites img srcs onto the token-scoped paths
// before sanitization; bluemonday then re-checks each surviving value. The
// attribute name folds case (SRC/src — Outlook normalizes attribute names to
// upper case) and the value quoting is tolerant (double, single, unquoted —
// round-8 F6): real-world mail HTML must not fail the rewrite and then the
// post-sanitize src allowlist, which silently vanishes the image. The VALUE
// still matches case-sensitively (two cids differing only by case address
// different parts).
func mailRewritePreviewSources(src string, inlines []email.MailPart, remoteURLs []string) string {
	out := src
	for i, part := range inlines {
		cid := part.ContentID
		if cid == "" {
			continue
		}
		bare := strings.Trim(cid, "<>")
		if bare == "" {
			continue
		}
		path := mailPreviewPartPrefix + mailTokenPlaceholder + "/" + strconv.Itoa(i)
		out = mailRewriteSrcAttr(out, "cid:"+bare, path)
	}
	for i, u := range remoteURLs {
		if u == "" {
			continue
		}
		path := mailPreviewImgPrefix + mailTokenPlaceholder + "/" + strconv.Itoa(i)
		out = mailRewriteSrcAttr(out, u, path)
	}
	// CSS url() values ride the same grant/index space: every pinned URL —
	// whether referenced from an img src or a CSS background — rewrites
	// onto its own /mail-preview/img/<token>/<i> path, so backgrounds
	// actually render under Load-images consent (grill I-05).
	out = mailhtml.RewriteCSSImageURLs(out, remoteURLs, mailPreviewImgPrefix+mailTokenPlaceholder)
	return out
}

// mailRewriteSrcAttr rewrites every src attribute whose value is exactly want
// to src="path": the attribute NAME folds case, the quoting is tolerant
// (double-quoted, single-quoted, or unquoted). RE2 has no lookahead, so the
// unquoted form captures its terminator ([\s>/]) and the replacement re-emits
// it ($3) — the terminator is never consumed.
func mailRewriteSrcAttr(out, want, path string) string {
	q := regexp.QuoteMeta(want)
	re := regexp.MustCompile(`(?i:src)(\s*=\s*)(?:"` + q + `"|'` + q + `'|(` + q + `)([\s>/]))`)
	return re.ReplaceAllString(out, `src$1"`+path+`"$3`)
}

// mailAnchorTagRe matches an opening anchor tag (MC-40 hardening pass).
var mailAnchorTagRe = regexp.MustCompile(`(?i)<a\b[^>]*>`)

// mailHardenAnchors adds target="_blank" and rel="noopener noreferrer" to
// every surviving anchor tag. It runs AFTER sanitization: bluemonday's
// allowlist permits neither attribute, so mailer-supplied values cannot
// survive to override the hardened ones.
func mailHardenAnchors(html string) string {
	return mailAnchorTagRe.ReplaceAllStringFunc(html, func(tag string) string {
		insert := ` target="_blank" rel="noopener noreferrer"`
		if strings.Contains(strings.ToLower(tag), "target=") {
			insert = ` rel="noopener noreferrer"`
		}
		return tag[:len(tag)-1] + insert + ">"
	})
}

// mailImgTagRe matches an opening img tag; mailSrcAttrRe matches a quoted
// src attribute value inside one.
var mailImgTagRe = regexp.MustCompile(`(?i)<img\b[^>]*>`)
var mailSrcAttrRe = regexp.MustCompile(`(?i)\bsrc\s*=\s*("([^"]*)"|'([^']*)')`)

// mailExtractRemoteImageURLs collects the https image URLs the raw HTML
// references, first-seen order, deduplicated, capped at 25. The serve-time
// proxy fetches ONLY entries of this mint-time list, by index (MC-41):
// caller-supplied URLs never reach the dialer.
func mailExtractRemoteImageURLs(raw string) []string {
	seen := map[string]bool{}
	var out []string
	collect := func(u string) {
		u = strings.TrimSpace(u)
		if !strings.HasPrefix(strings.ToLower(u), "https://") || seen[u] || len(out) >= 25 {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	for _, tag := range mailImgTagRe.FindAllString(raw, 64) {
		for _, m := range mailSrcAttrRe.FindAllStringSubmatch(tag, 1) {
			u := m[2]
			if m[3] != "" {
				u = m[3]
			}
			collect(u)
		}
	}
	// The CSS half of the pin (grill I-05 / artefact §5): background
	// url() values join the SAME mint-time grant so the token-scoped proxy
	// is the only dialer for them too.
	for _, u := range mailhtml.ExtractCSSImageURLs(raw, 25) {
		collect(u)
	}
	return out
}

// mailPreviewNotFound is the ONE refusal body for the whole serve prefix
// (MC-43/MC-45): unknown path, wrong method, bad index, unknown/expired
// token and the cookie-variant all write byte-identical JSON 404s - never
// the stock library body, never a distinguishable difference.
func mailPreviewNotFound(w http.ResponseWriter) {
	jsonErr(w, http.StatusNotFound, "not found")
}

// handleServe routes the token-only preview prefix: html/{token},
// part/{token}/{index}, img/{token}/{index}. Every failure - unknown path,
// unknown token, expired token, bad index - is the same bare 404 (MC-43):
// no body distinguishes a live token from a dead one.
func (p *mailPreviewRoutes) handleServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		mailPreviewNotFound(w)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, mailPreviewPathPrefix)
	kind, tail, _ := strings.Cut(rest, "/")
	switch kind {
	case "html":
		p.serveHTML(w, r, tail)
	case "part", "img":
		token, idxStr, ok := strings.Cut(tail, "/")
		idx, perr := strconv.Atoi(idxStr)
		if !ok || perr != nil || idx < 0 {
			mailPreviewNotFound(w)
			return
		}
		if kind == "part" {
			p.servePart(w, r, token, idx)
		} else {
			p.serveImage(w, r, token, idx)
		}
	default:
		mailPreviewNotFound(w)
	}
}

// serveHTML serves the preview HTML. A signature grant serves its
// operator-supplied sanitized HTML exactly as before (no dial). A message
// grant performs its OWN budget-gated live fetch and sanitizes within this
// request (US-6.2/MC-12) — the grant never carried the payload — records
// the freshly extracted remote-image URL list (serve-time recording, §13
// Q5), and serves with the real token substituted for the placeholder.
func (p *mailPreviewRoutes) serveHTML(w http.ResponseWriter, r *http.Request, token string) {
	g, ok := p.tokens.lookup(token)
	if !ok {
		mailPreviewNotFound(w)
		return
	}
	if grantKindOf(g) == mailPreviewKindSignature {
		html := mailSubstitutePreviewToken(g.HTML, token)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte(html))
		return
	}
	html, ok := p.fetchAndSanitizeMessageHTML(w, r, token, g)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(html))
}

// fetchAndSanitizeMessageHTML is the message-preview serve: one live,
// budget-gated ReadView through the SAME shared pool and budget the panel
// GETs use, the same 256 KB served-HTML cap (moved here from the mint —
// US-6.3), sanitize within the request, and the remote-image URL list
// recorded for the proxy. The bytes live only for this request.
func (p *mailPreviewRoutes) fetchAndSanitizeMessageHTML(w http.ResponseWriter, r *http.Request, token string, g mailPreviewGrant) (string, bool) {
	client := p.api.mailPairClient(w, g.AgentID, g.WorkspaceID)
	if client == nil {
		return "", false
	}
	v, handled := mailBudgetWrap(p.api, w, r, g.AgentID, g.WorkspaceID, client, "open",
		map[string]any{"folder": g.Folder, "ref": g.Ref}, func(c context.Context) (*email.MailView, error) {
			return client.ReadView(c, g.Folder, g.Ref)
		})
	if handled {
		return "", false
	}
	if mailViewHidden(v) {
		jsonErr(w, http.StatusNotFound, "message not found")
		return "", false
	}
	if len(v.HTMLBody) > mailPreviewMaxHTMLBytes {
		jsonErr(w, http.StatusBadRequest, "html body too large")
		return "", false
	}
	remoteURLs := []string(nil)
	if g.LoadRemote {
		remoteURLs = mailExtractRemoteImageURLs(v.HTMLBody)
	}
	// Serve-time recording (§13 Q5): the proxy's later requests dial only
	// entries of THIS fetch's list. Best-effort — an expired token still
	// finishes the response it already authorized.
	_ = p.tokens.recordRemoteURLs(token, remoteURLs)
	html := mailSanitizePreviewHTML(v.HTMLBody, v.Inline, remoteURLs)
	return mailSubstitutePreviewToken(html, token), true
}

// servePart serves one inline part by index. For a message grant the part
// bytes are fetched LIVE within this request (US-6.2 — the grant never
// carried them; the added live fetches are the ADR's recorded measurement
// item). Only image parts are served: the sandboxed iframe can reference
// them solely through img-src (the CSP names the part path under img-src
// only), so anything else has no rendering path and is refused as a bare
// 404. Signature grants have no inline parts and stay 404 here.
func (p *mailPreviewRoutes) servePart(w http.ResponseWriter, r *http.Request, token string, idx int) {
	g, ok := p.tokens.lookup(token)
	if !ok {
		mailPreviewNotFound(w)
		return
	}
	if grantKindOf(g) == mailPreviewKindSignature {
		mailPreviewNotFound(w)
		return
	}
	client := p.api.mailPairClient(w, g.AgentID, g.WorkspaceID)
	if client == nil {
		return
	}
	v, handled := mailBudgetWrap(p.api, w, r, g.AgentID, g.WorkspaceID, client, "open",
		map[string]any{"folder": g.Folder, "ref": g.Ref, "part": idx}, func(c context.Context) (*email.MailView, error) {
			return client.ReadView(c, g.Folder, g.Ref)
		})
	if handled {
		return
	}
	if mailViewHidden(v) || idx >= len(v.Inline) {
		mailPreviewNotFound(w)
		return
	}
	part := v.Inline[idx]
	if mt, _, perr := mime.ParseMediaType(part.ContentType); perr != nil || !strings.HasPrefix(mt, "image/") {
		mailPreviewNotFound(w)
		return
	}
	if len(part.Data) == 0 {
		// Round-8 F7: an inline part whose bytes are absent at fetch (over
		// the 25 MiB per-part fetch cap, or a decode failure at view time —
		// the transport leaves Data nil and the serve boundary carries that
		// as empty Data) must refuse like the attachment download
		// (rest_mail_read.go's 413), never serve 200 with zero bytes — a
		// broken image indistinguishable from a corrupt one.
		jsonErr(w, http.StatusRequestEntityTooLarge,
			"inline part unavailable: over the 25 MiB per-part fetch cap or failed to decode")
		return
	}
	h := w.Header()
	h.Set("Content-Type", part.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(part.Data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(part.Data)
}

// serveImage is the remote-image proxy entry (MC-41): it fetches ONLY the
// mint-time-recorded URL at the requested index, and only when the grant
// was minted with load_remote. Any refusal is the same bare 404.
func (p *mailPreviewRoutes) serveImage(w http.ResponseWriter, r *http.Request, token string, idx int) {
	g, ok := p.tokens.lookup(token)
	if !ok || !g.LoadRemote || idx >= len(g.RemoteURLs) {
		mailPreviewNotFound(w)
		return
	}
	data, ctype, ok := p.fetchRemoteImage(r.Context(), g.RemoteURLs[idx])
	if !ok {
		mailPreviewNotFound(w)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

// mailImageSSRFChecker enforces MC-41's pinned remote-image fetch IP screen
// using the canonical SSRF checker (pkg/security/ssrf.go::SSRFChecker)
// instead of a hand-rolled duplicate range list (the delta-review round-2
// finding: the prior round's hand-rolled mailAddrForbidden added the RFC
// 6598 CGNAT range directly rather than delegating, and — being a
// duplicate, not a delegation — missed every other range the canonical
// checker already covers: TEST-NET-1/2/3, the benchmarking range, the IETF
// protocol-assignments range, and IPv6-embedded-IPv4 unwrapping).
//
// A zero-value security.SSRFChecker is used here deliberately, NOT
// restAPI.ssrfChecker (which is nil when SSRF protection is globally
// disabled by the operator): MC-41's pinned image-proxy screen is an
// always-on security boundary that must never depend on the general SSRF
// toggle. SSRFChecker's zero value is documented (pkg/security/ssrf.go,
// SSRFChecker's doc comment) as safe to use directly — it lazily populates
// the full built-in private/reserved-range block list on first use and
// fails CLOSED, exactly matching NewSSRFChecker(nil)'s behavior, with no
// allowlist.
var mailImageSSRFChecker security.SSRFChecker

// mailImageAddrForbidden reports whether ip may never be dialed for the
// MC-41 pinned remote-image fetch, delegating to the canonical SSRF
// checker's CheckIP rather than re-implementing an IP-range block list.
func mailImageAddrForbidden(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return mailImageSSRFChecker.CheckIP(ip) != nil
}

// fetchRemoteImage performs the MC-41 pinned fetch: https only; DNS resolved
// once up front with every returned address screened and the surviving
// address PINNED into the dial (no rebinding window); zero redirects; at
// most mailImageMaxBytes over at most mailImageMaxSeconds; only image/*
// responses are re-emitted. The URL always comes from the mint-time grant,
// never from the request.
func (p *mailPreviewRoutes) fetchRemoteImage(ctx context.Context, rawURL string) ([]byte, string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, "", false
	}
	addrs, aerr := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if aerr != nil {
		return nil, "", false
	}
	pinned := ""
	for _, ia := range addrs {
		if !mailImageAddrForbidden(ia.IP) {
			pinned = ia.IP.String()
			break
		}
	}
	if pinned == "" {
		return nil, "", false
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	dialAddr := net.JoinHostPort(pinned, port)
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(dctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(dctx, network, dialAddr)
		},
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        1,
		IdleConnTimeout:     time.Second,
	}
	client := &http.Client{
		Timeout:       mailImageMaxSeconds * time.Second,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if rerr != nil {
		return nil, "", false
	}
	req.Header.Set("User-Agent", "Omnipus-MailPreview/0.1")
	req.Header.Set("Accept", "image/*")
	resp, derr := client.Do(req)
	if derr != nil {
		return nil, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", false
	}
	mt, _, perr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if perr != nil || !strings.HasPrefix(mt, "image/") {
		return nil, "", false
	}
	body, rerr2 := io.ReadAll(io.LimitReader(resp.Body, mailImageMaxBytes+1))
	if rerr2 != nil || len(body) > mailImageMaxBytes {
		return nil, "", false
	}
	safe := mime.FormatMediaType(mt, map[string]string{})
	if safe == "" {
		return nil, "", false
	}
	return body, safe, true
}
