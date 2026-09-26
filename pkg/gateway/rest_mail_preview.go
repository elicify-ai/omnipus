package gateway

// rest_mail_preview.go - the Mail HTML preview (spec 2.3a, MC-10). The mint
// (POST /api/v1/mail/html-preview-token) is the ONE live-IMAP fetch: it
// fetches the message, sanitizes the HTML, records the inline parts and the
// remote-image URL list in the token store, and mints the token. The
// /mail-preview/ serve routes never dial IMAP and answer 404-only.

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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
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
)

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
	serve := routes.serveHandler()
	cm.RegisterHTTPHandler(mailPreviewPathPrefix, serve)
}

// mailMintLimiter guards the mint endpoint (MC-44): dedicated 10/min per IP,
// separate from the panel mutation limiter.
var mailMintLimiter = newAPIRateLimiter(mailMintRatePerMinute, 1*time.Minute)

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

// handleMint implements POST /api/v1/mail/html-preview-token (D13/D17):
// decode, resolve the pair, ONE live fetch, sanitize, record, mint.
func (p *mailPreviewRoutes) handleMint(w http.ResponseWriter, r *http.Request) {
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
	client := p.api.mailPairClient(w, req.AgentId, req.WorkspaceId)
	if client == nil {
		return
	}
	v, err := client.ReadView(r.Context(), string(req.Folder), req.MessageRef)
	if err != nil {
		if errors.Is(err, email.ErrMailRefInvalid) {
			jsonErr(w, http.StatusBadRequest, err.Error())
		} else {
			mailErr502(w, err)
		}
		return
	}
	html := v.HTMLBody
	if len(html) > mailPreviewMaxHTMLBytes {
		jsonErr(w, http.StatusBadRequest, "html body too large")
		return
	}
	loadRemote := req.LoadRemote != nil && *req.LoadRemote
	inlineParts := make([]mailPreviewInline, 0, len(v.Inline))
	for _, part := range v.Inline {
		inlineParts = append(inlineParts, mailPreviewInline{ContentType: part.ContentType, Data: part.Data})
	}
	remoteURLs := []string(nil)
	if loadRemote {
		remoteURLs = mailExtractRemoteImageURLs(v.HTMLBody)
	}
	token, merr := p.tokens.mint(sessionKey, mailPreviewGrant{
		WorkspaceID: req.WorkspaceId, AgentID: req.AgentId,
		Folder: string(req.Folder), Ref: req.MessageRef, LoadRemote: loadRemote,
		HTML: mailSanitizePreviewHTML(v.HTMLBody, v.Inline, remoteURLs), Inline: inlineParts, RemoteURLs: remoteURLs,
	})
	if merr != nil {
		slog.Error("rest: mail preview mint failed", "error", merr)
		jsonErr(w, http.StatusInternalServerError, "could not mint preview token")
		return
	}
	resp := gen.MailHtmlPreviewTokenResponse{Token: token, ExpiresInSeconds: int(MailPreviewTokenTTL / time.Second)}
	jsonOK(w, resp)
}

// mailTokenPlaceholder stands in for the token inside sanitized HTML; it is
// substituted with the real token only AFTER a successful mint.
const mailTokenPlaceholder = "MAILPREVIEWTOKENPLACEHOLDER"

// mailSanitizePreviewHTML is the named inbound sanitizer (MC-10(5)/MC-40):
// strips meta/base/forms/scripts/handlers, rewrites cid: and remote image
// srcs onto the token-scoped mail-preview paths, hardens every surviving
// anchor. The result is what the token store holds.
func mailSanitizePreviewHTML(raw string, inlines []email.MailPart, remoteURLs []string) string {
	p := bluemonday.NewPolicy()
	for _, el := range []string{"p", "div", "span", "br", "b", "strong", "i", "em", "u", "s",
		"ul", "ol", "li", "table", "thead", "tbody", "tfoot", "tr", "td", "th",
		"h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "code", "hr", "a", "img"} {
		p.AllowElements(el)
	}
	p.AllowAttrs("alt", "width", "height", "align").OnElements("img")
	p.AllowAttrs("href").Matching(mailAnchorHrefRe).OnElements("a")
	p.AllowAttrs("src").Matching(mailImageSrcRe).OnElements("img")
	rewritten := mailRewritePreviewSources(raw, inlines, remoteURLs)
	html := p.Sanitize(rewritten)
	html = mailHardenAnchors(html)
	return html
}

// mailAnchorHrefRe allows only http/https/mailto anchors after rewrite.
var mailAnchorHrefRe = regexp.MustCompile(`^(?:https?|mailto):`)

// mailImageSrcRe allows data: images and the token-scoped preview paths.
var mailImageSrcRe = regexp.MustCompile(`^(?:/mail-preview/(?:part|img)/|data:image/(?:png|gif|jpe?g|webp);base64,)`)

// mailRewritePreviewSources rewrites img srcs onto the token-scoped paths
// before sanitization; bluemonday then re-checks each surviving value.
func mailRewritePreviewSources(raw string, inlines []email.MailPart, remoteURLs []string) string {
	out := raw
	for i, part := range inlines {
		cid := part.ContentID
		if cid == "" {
			continue
		}
		bare := strings.Trim(cid, "<>")
		path := mailPreviewPartPrefix + mailTokenPlaceholder + "/" + strconv.Itoa(i)
		out = strings.ReplaceAll(out, `src="cid:`+bare+`"`, `src="`+path+`"`)
		out = strings.ReplaceAll(out, `src='cid:`+bare+`'`, `src='`+path+`'`)
	}
	for i, u := range remoteURLs {
		if u == "" {
			continue
		}
		path := mailPreviewImgPrefix + mailTokenPlaceholder + "/" + strconv.Itoa(i)
		quoted := []string{`src="` + u + `"`, `src='` + u + `'`}
		for _, q := range quoted {
			out = strings.ReplaceAll(out, q, `src="`+path+`"`)
		}
	}
	return out
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
	for _, tag := range mailImgTagRe.FindAllString(raw, 64) {
		for _, m := range mailSrcAttrRe.FindAllStringSubmatch(tag, 1) {
			u := m[2]
			if m[3] != "" {
				u = m[3]
			}
			if !strings.HasPrefix(strings.ToLower(u), "https://") || seen[u] || len(out) >= 25 {
				continue
			}
			seen[u] = true
			out = append(out, u)
		}
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

// serveHTML serves the sanitized HTML with the real token substituted for
// the placeholder in the token-scoped image paths.
func (p *mailPreviewRoutes) serveHTML(w http.ResponseWriter, r *http.Request, token string) {
	g, ok := p.tokens.lookup(token)
	if !ok {
		mailPreviewNotFound(w)
		return
	}
	html := strings.ReplaceAll(g.HTML, mailTokenPlaceholder, token)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(html))
}

// servePart serves one mint-time inline part by index. Only image parts are
// served: the sandboxed iframe can reference them solely through img-src
// (the CSP names the part path under img-src only), so anything else has no
// rendering path and is refused as a bare 404.
func (p *mailPreviewRoutes) servePart(w http.ResponseWriter, r *http.Request, token string, idx int) {
	g, ok := p.tokens.lookup(token)
	if !ok || idx >= len(g.Inline) {
		mailPreviewNotFound(w)
		return
	}
	part := g.Inline[idx]
	if mt, _, perr := mime.ParseMediaType(part.ContentType); perr != nil || !strings.HasPrefix(mt, "image/") {
		mailPreviewNotFound(w)
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

// mailAddrForbidden reports whether an IP may never be dialed (MC-41):
// loopback, private ranges, link-local unicast/multicast (link-local also
// covers the cloud metadata address 169.254.169.254), multicast, and
// unspecified.
func mailAddrForbidden(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
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
		if !mailAddrForbidden(ia.IP) {
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
