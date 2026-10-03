package gateway

// rest_mail_attachment.go — the F1/F2 attachment endpoints (ADR-20261001;
// w4 spec §3.1/§5): the metadata-only preview mint and its preview-purpose
// byte/HTML resources (grill I-05's two distinct byte roles), and the
// save-to-library subresource with the save-operation-token reconciliation
// (correction M-02). Every wire type comes from pkg/api/generated; every
// byte read is a fresh request-scoped fetch — the grant store holds
// authorization/reference metadata only, never bytes (the retained-payload
// message-preview grant shape is retired for attachments).

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/mailattachment"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

const (
	// mailAttachmentPreviewMintPath is the session-authenticated attachment
	// preview mint.
	mailAttachmentPreviewMintPath = "/api/v1/mail/attachment-preview-token"
	// mailAttachmentPreviewRevokePrefix is the revoke subresource.
	mailAttachmentPreviewRevokePrefix = mailAttachmentPreviewMintPath + "/"
	// mailAttachmentPreviewPrefix is the token-only preview-purpose serve
	// prefix (the byte_url family) — the MORE SPECIFIC prefix wins over the
	// generic /mail-preview/ route.
	mailAttachmentPreviewPrefix = mailPreviewPathPrefix + "attachment/"
	// mailAttachmentMintRatePerMinute is the mint limiter's rate (MC-44
	// family: the message mint's 10/min per-IP discipline).
	mailAttachmentMintRatePerMinute = 10
	// mailAttachmentMaxLivePerSession caps live attachment grants per
	// session (the MC-43 cap-refuses-never-evicts family).
	mailAttachmentMaxLivePerSession = 8
	// mailAttachmentTokenBytes is the grant token entropy before encoding
	// (32 bytes = 43 base64url chars, the token family's bound).
	mailAttachmentTokenBytes = 32
)

// mailAttachmentMintLimiter guards the attachment mint (MC-44 family).
var mailAttachmentMintLimiter = newAPIRateLimiter(mailAttachmentMintRatePerMinute, 1*time.Minute)

// mailAttachmentGrant is a METADATA-ONLY grant: authorization/reference
// fields plus display metadata — no body bytes, no HTML payload, no spool.
// Every byte read re-fetches live through the reader under this binding.
type mailAttachmentGrant struct {
	WorkspaceID string
	AgentID     string
	Folder      string
	Ref         string
	PartIndex   int
	SessionKey  string
	ExpiresAt   time.Time
	Subject     string
	Filename    string
	ContentType string
	IsHTML      bool
}

// mailAttachmentGrantStore is the attachment-grant family of the MC-43
// hygiene: 256-bit token, named TTL (MailPreviewTokenTTL), per-session cap
// that REFUSES never evicts, revoke-on-demand, and unknown/expired/revoked
// all one answer. In memory only; a restart invalidates every live grant.
type mailAttachmentGrantStore struct {
	mu        sync.Mutex
	byToken   map[string]mailAttachmentGrant
	bySession map[string]map[string]struct{}
	now       func() time.Time
}

func newMailAttachmentGrantStore() *mailAttachmentGrantStore {
	return &mailAttachmentGrantStore{
		byToken:   make(map[string]mailAttachmentGrant),
		bySession: make(map[string]map[string]struct{}),
		now:       time.Now,
	}
}

// mint issues one metadata-only attachment grant.
func (s *mailAttachmentGrantStore) mint(sessionKey string, g mailAttachmentGrant) (string, error) {
	if sessionKey == "" {
		return "", ErrMailPreviewSession
	}
	buf := make([]byte, mailAttachmentTokenBytes)
	if n, rerr := rand.Read(buf); rerr != nil || n != mailAttachmentTokenBytes {
		return "", ErrMailPreviewEntropy
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked()
	if s.countLiveLocked(sessionKey) >= mailAttachmentMaxLivePerSession {
		return "", ErrMailPreviewCap
	}
	g.SessionKey = sessionKey
	g.ExpiresAt = s.now().Add(MailPreviewTokenTTL)
	s.byToken[token] = g
	if s.bySession[sessionKey] == nil {
		s.bySession[sessionKey] = make(map[string]struct{})
	}
	s.bySession[sessionKey][token] = struct{}{}
	return token, nil
}

// lookup resolves a live grant; unknown/expired/revoked are one answer.
func (s *mailAttachmentGrantStore) lookup(token string) (mailAttachmentGrant, bool) {
	if token == "" {
		return mailAttachmentGrant{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.byToken[token]
	if !ok || !s.now().Before(g.ExpiresAt) {
		return mailAttachmentGrant{}, false
	}
	return g, true
}

// revoke kills one grant explicitly (Back/close/navigate). Unknown or
// already-expired is false — never an error the client must distinguish.
func (s *mailAttachmentGrantStore) revoke(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.byToken[token]
	if !ok {
		return false
	}
	delete(s.byToken, token)
	if set := s.bySession[g.SessionKey]; set != nil {
		delete(set, token)
		if len(set) == 0 {
			delete(s.bySession, g.SessionKey)
		}
	}
	return true
}

// revokeSession kills every grant of one session (logout revocation).
func (s *mailAttachmentGrantStore) revokeSession(sessionKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok := range s.bySession[sessionKey] {
		delete(s.byToken, tok)
	}
	delete(s.bySession, sessionKey)
}

// purgeLocked drops expired grants. Caller holds s.mu.
func (s *mailAttachmentGrantStore) purgeLocked() {
	now := s.now()
	for tok, g := range s.byToken {
		if !now.Before(g.ExpiresAt) {
			delete(s.byToken, tok)
			if set := s.bySession[g.SessionKey]; set != nil {
				delete(set, tok)
				if len(set) == 0 {
					delete(s.bySession, g.SessionKey)
				}
			}
		}
	}
}

// countLiveLocked counts one session's live grants. Caller holds s.mu.
func (s *mailAttachmentGrantStore) countLiveLocked(sessionKey string) int {
	n := 0
	for tok := range s.bySession[sessionKey] {
		if _, ok := s.byToken[tok]; ok {
			n++
		}
	}
	return n
}

// mailAttachmentRoutes binds the mint/revoke endpoints and the serve prefix
// to one store, published on the restAPI (logout revocation reaches it
// through mailAttachmentTokensOf).
type mailAttachmentRoutes struct {
	api    *restAPI
	tokens *mailAttachmentGrantStore
}

func newMailAttachmentRoutes(a *restAPI) *mailAttachmentRoutes {
	routes := &mailAttachmentRoutes{api: a, tokens: newMailAttachmentGrantStore()}
	a.mailAttachmentTokens.Store(routes.tokens)
	return routes
}

// registerMailAttachmentRoutes registers the mint (session-auth + MC-44
// limiter), the revoke (session-auth), and the token-only preview-purpose
// prefix with the MC-10 header set applied BEFORE the limiter.
func (a *restAPI) registerMailAttachmentRoutes(cm httpHandlerRegistrar) {
	routes := newMailAttachmentRoutes(a)
	cm.RegisterHTTPHandler(mailAttachmentPreviewMintPath,
		a.withAuth(withRateLimit(mailAttachmentMintLimiter, routes.handleMint)))
	cm.RegisterHTTPHandler(mailAttachmentPreviewRevokePrefix,
		a.withAuth(routes.handleRevoke))
	serve := routes.serveHandler()
	cm.RegisterHTTPHandler(mailAttachmentPreviewPrefix, serve)
}

// mailAttachmentSetSecurityHeaders applies the MC-10 header set to every
// response the preview-purpose prefix produces, refusals included (the
// message-preview prefix's discipline, mirrored — the CSP string stays
// built in its ONE place, mailIsolationPolicy).
func mailAttachmentSetSecurityHeaders(a *restAPI, w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", mailIsolationPolicy(mailPreviewCanonicalOrigin(a)))
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
}

// serveHandler wraps the serving handler in a limiter with the MC-10 header
// set applied BEFORE the limiter, so even the 429 carries the policy.
func (p *mailAttachmentRoutes) serveHandler() http.HandlerFunc {
	limited := withRateLimit(mailPreviewServeLimiter, p.handleServe)
	return func(w http.ResponseWriter, r *http.Request) {
		mailAttachmentSetSecurityHeaders(p.api, w)
		limited(w, r)
	}
}

// mailAttachmentJSONStaleReference writes the typed stale-reference 409
// (grill I-03): the ref failed its epoch/pair/generation validation on the
// lease that would have served it.
func mailAttachmentJSONStaleReference(w http.ResponseWriter) {
	jsonErrCode(w, http.StatusConflict, "This message changed or was deleted. Refresh the list.", "stale_reference")
}

// mailAttachmentPartFetch runs one capped part fetch through the shared
// budget with the ATTACHMENT error mapping (the generic mailBudgetWrap maps
// everything unknown to 502; the attachment contract carries typed 409/413
// refusals that must never masquerade as upstream failures).
func mailAttachmentPartFetch(a *restAPI, w http.ResponseWriter, r *http.Request, agentID, workspaceID string, client *email.Client, op string, params map[string]any, dial func(context.Context) (*email.AttachmentPart, error)) (*email.AttachmentPart, bool) {
	started := time.Now()
	req := email.MailBudgetRequest{
		Account:     client.AccountKey(),
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		Operation:   op,
		Params:      params,
		Retry:       mailRetryParam(r),
	}
	v, err := email.CallValue(a.mailBudgetFor(), r.Context(), req, dial)
	// w5 US-7.6/MC-18: the attachment part-fetch seam emits its own record.
	a.emitMailOperationTiming(op, agentID, workspaceID, started, err, "live", false)
	if a.mailBudgetErr(w, err) {
		return nil, true
	}
	if err != nil {
		switch {
		case errors.Is(err, email.ErrMailPartTooLarge):
			// The preview cap refusal (I-05): before any success state
			// exists; the larger-file path is browser Download.
			jsonErr(w, http.StatusRequestEntityTooLarge,
				"attachment exceeds the 25 MiB preview cap; use Download")
		case errors.Is(err, email.ErrMailPartNotFound):
			jsonErr(w, http.StatusNotFound, "no such attachment part")
		case errors.Is(err, email.ErrMailStaleReference):
			mailAttachmentJSONStaleReference(w)
		case errors.Is(err, email.ErrMailRefInvalid):
			jsonErr(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, email.ErrMessageNotFound):
			jsonErr(w, http.StatusNotFound, "message not found")
		default:
			mailErr502(w, err)
		}
		return nil, true
	}
	return v, false
}

// handleMint implements POST /api/v1/mail/attachment-preview-token (F1):
// decode, resolve the pair, one bounded metadata read (flags + subject — no
// body bytes), ONE capped part fetch (part-specific PEEK — the mint's single
// live body fetch), metadata grant, respond.
func (p *mailAttachmentRoutes) handleMint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req gen.MailAttachmentPreviewRequest
	if !decodeMailJSON(p.api, w, r, "MailAttachmentPreviewRequest", &req) {
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
	folder := string(req.Folder)

	// One bounded metadata read decides visibility AND names the context
	// bar — never a second body fetch (M10: byte counters stay part-only).
	meta, handled := mailBudgetWrap(p.api, w, r, req.AgentId, req.WorkspaceId, client, "mintMailAttachmentPreviewMeta",
		map[string]any{"folder": folder, "ref": req.MessageRef}, func(c context.Context) (*email.MessageMeta, error) {
			return client.ReadMessageMeta(c, folder, req.MessageRef)
		}, nil) // a metadata read has no row count
	if handled {
		return
	}
	if mailFlagHas(meta.Flags, "\\Deleted") {
		jsonErr(w, http.StatusNotFound, "message not found")
		return
	}

	part, handled := mailAttachmentPartFetch(p.api, w, r, req.AgentId, req.WorkspaceId, client, "mintMailAttachmentPreviewPart",
		map[string]any{"folder": folder, "ref": req.MessageRef, "idx": req.PartIndex},
		func(c context.Context) (*email.AttachmentPart, error) {
			return client.ReadAttachmentPart(c, folder, req.MessageRef, req.PartIndex)
		})
	if handled {
		return
	}
	if part.DataUnavailable || len(part.Data) == 0 {
		jsonErr(w, http.StatusRequestEntityTooLarge,
			"attachment part unavailable: over the 25 MiB per-part cap or failed to decode")
		return
	}
	name := part.Filename
	if name == "" {
		name = "attachment"
	}
	isHTML := strings.EqualFold(path.Ext(name), ".html") || strings.EqualFold(part.ContentType, "text/html")
	token, merr := p.tokens.mint(sessionKey, mailAttachmentGrant{
		WorkspaceID: req.WorkspaceId, AgentID: req.AgentId,
		Folder: folder, Ref: req.MessageRef, PartIndex: req.PartIndex,
		Subject: meta.Subject, Filename: name, ContentType: part.ContentType, IsHTML: isHTML,
	})
	if merr != nil {
		if errors.Is(merr, ErrMailPreviewCap) {
			jsonErr(w, http.StatusTooManyRequests, "too many concurrent previews: close older previews to mint a new one")
			return
		}
		slog.Error("rest: mail attachment preview mint failed", "error", merr)
		jsonErr(w, http.StatusInternalServerError, "could not mint preview token")
		return
	}
	byteURL := mailAttachmentPreviewPrefix + token
	resp := gen.MailAttachmentPreviewResponse{
		Kind:      gen.MailAttachmentPreviewResponseKindMailAttachment,
		PreviewId: token,
		Subject:   meta.Subject,
		Attachment: struct {
			ContentType       string `json:"content_type"`
			Filename          string `json:"filename"`
			PartIndex         int    `json:"part_index"`
			ReportedSizeBytes *int64 `json:"reported_size_bytes,omitempty"`
			SizeBytes         int    `json:"size_bytes"`
		}{
			ContentType: part.ContentType,
			Filename:    name,
			PartIndex:   part.PartIndex,
			SizeBytes:   len(part.Data),
		},
		TextReadable: strings.HasPrefix(strings.ToLower(part.ContentType), "text/"),
		ReadOnly:     gen.MailAttachmentPreviewResponseReadOnlyTrue,
		ContentSource: struct {
			ByteUrl          string  `json:"byte_url"`
			ExpiresInSeconds int     `json:"expires_in_seconds"`
			IsolatedHtmlUrl  *string `json:"isolated_html_url"`
			Token            string  `json:"token"`
		}{
			ByteUrl:          byteURL,
			ExpiresInSeconds: int(MailPreviewTokenTTL / time.Second),
			Token:            token,
		},
	}
	if part.ReportedSizeBytes >= 0 {
		sz := part.ReportedSizeBytes
		resp.Attachment.ReportedSizeBytes = &sz
	}
	if isHTML {
		htmlURL := byteURL + "/html"
		resp.ContentSource.IsolatedHtmlUrl = &htmlURL
	}
	jsonOK(w, resp)
}

// handleRevoke implements DELETE /api/v1/mail/attachment-preview-token/
// {previewId} (F1 ownership lifecycle): unknown/expired is 404, never an
// error the client must distinguish.
func (p *mailAttachmentRoutes) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	token := strings.TrimPrefix(r.URL.Path, mailAttachmentPreviewRevokePrefix)
	if token == "" || !p.tokens.revoke(token) {
		jsonErr(w, http.StatusNotFound, "not found")
		return
	}
	jsonOK(w, gen.OperationResult{Success: true})
}

// handleServe routes the preview-purpose prefix: attachment/{token} (the
// capped byte resource, inline disposition) and attachment/{token}/html
// (the scriptless rendering projection). Every failure is the same bare 404
// (MC-43/MC-45) — a live token is never distinguishable from a dead one.
func (p *mailAttachmentRoutes) handleServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		mailPreviewNotFound(w)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, mailAttachmentPreviewPrefix)
	token, suffix, hasSuffix := strings.Cut(rest, "/")
	g, ok := p.tokens.lookup(token)
	if !ok {
		mailPreviewNotFound(w)
		return
	}
	if !hasSuffix || suffix == "" {
		p.serveBytes(w, r, g)
		return
	}
	if suffix == "html" {
		p.serveHTML(w, r, g)
		return
	}
	mailPreviewNotFound(w)
}

// serveBytes serves the preview-purpose byte resource: a FRESH capped fetch
// at serve time (the grant holds no bytes), inline rendering disposition,
// no-store (from the header set). The cap refusal fires before any byte is
// written — a truncated stream can never render as a completed preview
// (grill I-05 late-failure ordering). An HTML attachment has no byte render
// here: its projection is the /html route (MC-42 — an .html part is never
// inline).
func (p *mailAttachmentRoutes) serveBytes(w http.ResponseWriter, r *http.Request, g mailAttachmentGrant) {
	if g.IsHTML {
		mailPreviewNotFound(w)
		return
	}
	client := p.api.mailPairClient(w, g.AgentID, g.WorkspaceID)
	if client == nil {
		mailPreviewNotFound(w)
		return
	}
	part, err := client.ReadAttachmentPart(r.Context(), g.Folder, g.Ref, g.PartIndex)
	if err != nil || part.DataUnavailable || len(part.Data) == 0 {
		// Over-cap or a failed transfer: the typed 413 class, never a
		// 200-with-zero-bytes and never a partial success state.
		jsonErr(w, http.StatusRequestEntityTooLarge,
			"attachment preview unavailable: over the 25 MiB per-part cap or failed to transfer")
		return
	}
	name := g.Filename
	ctype := part.ContentType
	if ct, ok := libraryExtContentTypes[libraryExtOf(name)]; ok {
		ctype = ct
	} else if ctype == "" {
		ctype = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Length", strconv.Itoa(len(part.Data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, werr := w.Write(part.Data); werr != nil {
		slog.Error("rest: mail attachment preview write failed", "error", werr)
	}
}

// serveHTML serves the scriptless rendering projection: a fresh capped
// fetch, the STYLE-AWARE sanitizer, the Mail isolation policy headers
// (already applied by serveHandler). Never a raw object URL, never srcdoc.
func (p *mailAttachmentRoutes) serveHTML(w http.ResponseWriter, r *http.Request, g mailAttachmentGrant) {
	client := p.api.mailPairClient(w, g.AgentID, g.WorkspaceID)
	if client == nil {
		mailPreviewNotFound(w)
		return
	}
	part, err := client.ReadAttachmentPart(r.Context(), g.Folder, g.Ref, g.PartIndex)
	if err != nil || part.DataUnavailable || len(part.Data) == 0 {
		jsonErr(w, http.StatusRequestEntityTooLarge,
			"attachment preview unavailable: over the 25 MiB per-part cap or failed to transfer")
		return
	}
	if len(part.Data) > mailPreviewMaxHTMLBytes {
		jsonErr(w, http.StatusBadRequest, "html attachment too large to render")
		return
	}
	html := mailSanitizePreviewHTML(string(part.Data), nil, nil)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(html)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(html))
}

// --- save-to-library ---

// mailAttachmentAuditSink adapts the gateway auditor to the shared service's
// AuditSink, returning the TRUTHFUL outcome (recorded/disabled/failed) the
// save receipt and response carry (US-2.AC-5).
type mailAttachmentAuditSink struct {
	a *restAPI
}

func (s mailAttachmentAuditSink) AttachmentSaved(fields map[string]any) string {
	if s.a == nil || s.a.auditor == nil {
		return mailattachment.AuditDisabled
	}
	if err := s.a.auditor.Log(&audit.Entry{
		Event:   string(audit.EventMailAttachmentSaved),
		Details: fields,
	}); err != nil {
		logsafeWarn("rest: mail.attachment_saved audit write failed", "error", err)
		return mailattachment.AuditFailed
	}
	return mailattachment.AuditRecorded
}

// mailSaveReceiptStore returns the process-lifetime reconciliation store
// (one per process, lazily built — receipts live for the gateway process
// lifetime with no eviction; the restart is the only bound, grill-2 F-9).
func (a *restAPI) mailSaveReceiptStore() *mailattachment.SaveReceiptStore {
	if s := a.mailSaveReceipts.Load(); s != nil {
		return s
	}
	s := mailattachment.NewSaveReceiptStore()
	a.mailSaveReceipts.CompareAndSwap(nil, s)
	return a.mailSaveReceipts.Load()
}

// mailMarkerStore returns the workspace's mail-derived marker store: the
// per-workspace index under the workspace data directory — one level above
// the work root the Library serves, so a marker can never appear as a
// Library entry.
func (a *restAPI) mailMarkerStore(workspaceID string) *mailattachment.MarkerStore {
	workDir, err := workspace.SafeWorkDir(a.homePath, workspaceID)
	if err != nil || workDir == "" {
		return nil
	}
	return mailattachment.NewMarkerStore(filepath.Join(filepath.Dir(workDir), "mail-marker-index.json"))
}

// mailAttachmentSave runs the shared save through the standard mail budget
// with the SAVE error mapping. The request's save_operation_token is part of
// the flight params, so two saves can never coalesce into one flight (the
// contract's no-coalesce/no-replay rule), and a same-token retry never
// reaches the fetch at all (the receipt store answers first). Returns
// handled=true when the response was already written.
func (a *restAPI) mailAttachmentSave(w http.ResponseWriter, r *http.Request, agentID, workspaceID string, client *email.Client, service *mailattachment.Service, folder, ref string, partIndex int, token string) (*mailattachment.SaveReceipt, bool) {
	started := time.Now()
	req := email.MailBudgetRequest{
		Account:     client.AccountKey(),
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		Operation:   "saveMailAttachmentToLibrary",
		Params:      map[string]any{"folder": folder, "ref": ref, "idx": partIndex, "token_len": len(token)},
		Retry:       false, // a save is never an automatic retry (M-02)
	}
	receipt, err := email.CallValue(a.mailBudgetFor(), r.Context(), req, func(c context.Context) (*mailattachment.SaveReceipt, error) {
		return service.Save(c, mailattachment.SaveRequest{
			Slug: folder, Ref: ref, PartIndex: partIndex, Token: token,
		})
	})
	// w5 US-7.6/MC-18: the save seam emits its own record.
	a.emitMailOperationTiming("saveMailAttachmentToLibrary", agentID, workspaceID, started, err, "live", false)
	if a.mailBudgetErr(w, err) {
		return nil, true
	}
	if err != nil {
		switch {
		case errors.Is(err, mailattachment.ErrInvalidToken):
			jsonErr(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, mailattachment.ErrUnsafeName):
			jsonErr(w, http.StatusBadRequest, "the attachment name cannot be saved safely: "+err.Error())
		case errors.Is(err, mailattachment.ErrOverCap):
			jsonErr(w, http.StatusRequestEntityTooLarge,
				"attachment exceeds the 25 MiB save cap; larger files are browser Download only")
		case errors.Is(err, mailattachment.ErrPartMissing):
			jsonErr(w, http.StatusNotFound, "no such attachment part")
		case errors.Is(err, mailattachment.ErrStaleRef):
			mailAttachmentJSONStaleReference(w)
		case errors.Is(err, mailattachment.ErrDestinationWrite):
			jsonErr(w, http.StatusInternalServerError, "could not write the file into the Library")
		default:
			mailErr502(w, err)
		}
		return nil, true
	}
	if receipt == nil {
		// service.Save never returns (nil, nil) by construction; refuse
		// rather than write a success shape without an outcome.
		jsonErr(w, http.StatusInternalServerError, "save completed without an outcome")
		return nil, true
	}
	return receipt, false
}

// handleMailAttachmentSave implements POST .../attachments/{partIndex}/
// save-to-library (F2): the explicit user-directed export through the ONE
// shared service — same cap, naming, audit and refusal semantics as the
// agent's download tool.
func (a *restAPI) handleMailAttachmentSave(w http.ResponseWriter, r *http.Request, workspaceID, agentID, folder, ref string, partIndex int) {
	var req gen.MailAttachmentSaveRequest
	if !decodeMailJSON(a, w, r, "MailAttachmentSaveRequest", &req) {
		return
	}
	if req.SaveOperationToken == "" {
		jsonErr(w, http.StatusBadRequest, "save_operation_token is required")
		return
	}
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	root, err := library.OpenRoot(a.homePath, workspaceID)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "workspace library is unavailable")
		return
	}
	defer root.Close()
	service := mailattachment.NewService(
		client,
		mailattachment.RootWriter{Root: root, Marker: a.mailMarkerStore(workspaceID)},
		mailAttachmentAuditSink{a: a},
		a.mailSaveReceiptStore(),
		func() string { return client.Address() },
	)
	receipt, handled := a.mailAttachmentSave(w, r, agentID, workspaceID, client, service, folder, ref, partIndex, req.SaveOperationToken)
	if handled {
		return
	}
	marker := a.mailMarkerStore(workspaceID)
	out := gen.MailAttachmentSaveResponse{
		Saved:        gen.MailAttachmentSaveResponseSavedTrue,
		WorkspaceId:  workspaceID,
		Path:         receipt.Path,
		AbsolutePath: receipt.AbsolutePath,
		SizeBytes:    receipt.SizeBytes,
	}
	switch receipt.AuditStatus {
	case mailattachment.AuditRecorded:
		out.AuditStatus = gen.MailAttachmentSaveResponseAuditStatusRecorded
	case mailattachment.AuditFailed:
		out.AuditStatus = gen.MailAttachmentSaveResponseAuditStatusFailed
		wc := gen.MailAttachmentSaveResponseWarningCodeAuditWriteFailed
		out.WarningCode = &wc
	default:
		out.AuditStatus = gen.MailAttachmentSaveResponseAuditStatusDisabled
	}
	// The Entry field is the generated anonymous struct; fields are set
	// directly on the zero value.
	out.Entry.IsHidden = strings.HasPrefix(path.Base(receipt.Path), ".")
	out.Entry.Name = path.Base(receipt.Path)
	out.Entry.Path = receipt.Path
	out.Entry.Size = receipt.SizeBytes
	out.Entry.ModifiedAt = time.UnixMilli(receipt.SavedAtUnixMS).UTC()
	// Marker presence decides the served profile (Q5=A); the response
	// states it so the SPA needs no second lookup. A store read failure is
	// Unknown — the entry carries no profile and serving fails safe to the
	// stricter one.
	if marker != nil {
		if status, merr := marker.Status(receipt.Path); merr == nil && status == mailattachment.StatusMailDerived {
			pp := gen.MailAttachmentSaveResponseEntryPreviewProfileMailRestricted
			out.Entry.PreviewProfile = &pp
		}
	}
	if mime, ok := libraryExtContentTypes[libraryExtOf(out.Entry.Name)]; ok {
		m := mime
		out.Entry.Mime = &m
	}
	jsonOK(w, out)
}

// invalidatePair kills every grant of one (agent, workspace) pair — the
// removal/disable cascade's grant revocation (w5 US-5.1).
func (s *mailAttachmentGrantStore) invalidatePair(agentID, workspaceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, g := range s.byToken {
		if g.AgentID == agentID && g.WorkspaceID == workspaceID {
			delete(s.byToken, tok)
			if set := s.bySession[g.SessionKey]; set != nil {
				delete(set, tok)
				if len(set) == 0 {
					delete(s.bySession, g.SessionKey)
				}
			}
		}
	}
}
