package gateway

// mail_preview_token.go - the Mail preview token store (MC-43). Same
// hygiene as the Library store (preview_token.go): 256-bit crypto/rand
// token, 43-char base64url, named TTL, per-session live-token cap that
// REFUSES never evicts, logout revocation, unknown/expired/revoked all one
// answer (404 on the wire).
//
// Since the w5-integration wave the store's MESSAGE grants carry
// authorization and reference metadata ONLY (US-6/MC-11: no fetched Mail
// HTML, no inline part bytes, no attachment data — the request-only body
// rule). Each preview serve fetches and sanitizes within its own request
// through the shared pool and budget. The remote-image URL list is
// source-URL METADATA the ADR explicitly permits; it is recorded by
// authorized server processing at the first live serve (§13 Q5's settled
// timing), never from client-trusted body bytes, and the serve-time proxy
// dials only entries of that recorded list (MC-41). SIGNATURE grants keep
// their already-sanitized payload: the draft signature is operator-
// supplied input, not mail content, and its controls are unchanged.

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// MailPreviewTokenTTL is the token lifetime (MC-43, Library 15-min
// precedent). Named so tests assert the constant, not a repeated literal.
const MailPreviewTokenTTL = 15 * time.Minute

// MailPreviewMaxLiveTokensPerSession caps live MESSAGE preview tokens per
// session (MC-43); the ninth message mint is refused, never an eviction.
// Signature tokens are purpose-differentiated out of this counter: they
// replace rather than accumulate.
const MailPreviewMaxLiveTokensPerSession = 8

// MailSignaturePreviewTokenTTL is the signature-preview token lifetime
// (decision: 2 minutes — bounds replay; the editor preview outlives
// debounce+render). Named so tests assert the constant, not a literal.
const MailSignaturePreviewTokenTTL = 2 * time.Minute

// Preview grant kinds. The zero value behaves as a message grant so grants
// constructed without a kind keep the cap-8-refuses / 15-minute hygiene.
const (
	// mailPreviewKindMessage marks a message-preview grant: per-session
	// cap-8-refuses, MailPreviewTokenTTL.
	mailPreviewKindMessage = "message"
	// mailPreviewKindSignature marks a draft-signature preview grant:
	// replace-on-mint (a session holds at most one live signature token),
	// never counted against the message cap, MailSignaturePreviewTokenTTL.
	mailPreviewKindSignature = "signature"
)

// grantKindOf normalizes a grant's zero-value kind to the message kind.
func grantKindOf(g mailPreviewGrant) string {
	if g.Kind == "" {
		return mailPreviewKindMessage
	}
	return g.Kind
}

// mailPreviewInline is one inline cid part ready to serve on
// /mail-preview/part/{token}/{index}.
type mailPreviewInline struct {
	ContentType string
	Data        []byte
}

// mailPreviewGrant is what a mail preview token buys: authorization and
// reference metadata bound to the (pair, folder, ref, load_remote) identity
// (MC-43) — and NOTHING else for a message grant (US-6.1/MC-11: zero HTML
// string, zero inline part bytes, zero attachment data). The remote-image
// URL list is metadata, recorded by server processing at the first live
// serve; the proxy dials recorded URLs only (MC-41). Signature-kind grants
// keep their operator-supplied sanitized HTML: that is not mail content,
// and their existing controls are unchanged.
type mailPreviewGrant struct {
	// Kind is the grant's purpose (mailPreviewKind*): it selects the TTL and
	// the per-session cap discipline (message: cap-8-refuses; signature:
	// replace-on-mint). Zero value behaves as message.
	Kind        string
	WorkspaceID string
	AgentID     string
	Folder      string
	Ref         string
	LoadRemote  bool
	SessionKey  string
	ExpiresAt   time.Time
	// HTML/Inline carry ONLY the signature kind's operator-supplied preview.
	// A message grant leaves both nil forever (MC-11's zero-payload rule).
	HTML       string
	Inline     []mailPreviewInline
	RemoteURLs []string
}

// ErrMailPreviewEntropy is the fail-closed mint refusal (MC-43): entropy
// failure issues no token and mutates nothing.
var ErrMailPreviewEntropy = errors.New("mail preview token: entropy source failed")

// ErrMailPreviewSession is the no-session mint refusal: an anonymous grant
// has nothing to revoke on logout.
var ErrMailPreviewSession = errors.New("mail preview token: no minting session")

// ErrMailPreviewCap is the per-session cap refusal: refuse, never evict.
var ErrMailPreviewCap = errors.New("mail preview token: too many live tokens for this session")

// mailPreviewTokenStore holds live mail preview grants. In memory only: a
// gateway restart invalidates every live token, accepted because a restart
// also drops the page holding them.
type mailPreviewTokenStore struct {
	mu        sync.Mutex
	byToken   map[string]mailPreviewGrant
	bySession map[string]map[string]struct{}
	randRead  func([]byte) (int, error)
	now       func() time.Time
}

func newMailPreviewTokenStore() *mailPreviewTokenStore {
	return &mailPreviewTokenStore{
		byToken:   make(map[string]mailPreviewGrant),
		bySession: make(map[string]map[string]struct{}),
		randRead:  rand.Read,
		now:       time.Now,
	}
}

// mint issues one token. The caller must have already fetched the message
// over the authenticated chain and sanitized the HTML; the store records
// the payload, it does not authorize it.
func (s *mailPreviewTokenStore) mint(sessionKey string, grant mailPreviewGrant) (string, error) {
	if sessionKey == "" {
		return "", ErrMailPreviewSession
	}
	buf := make([]byte, mailPreviewTokenBytes)
	n, rerr := s.randRead(buf)
	if rerr != nil || n != mailPreviewTokenBytes {
		return "", ErrMailPreviewEntropy
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked()
	ttl := MailPreviewTokenTTL
	if grant.Kind == mailPreviewKindSignature {
		// Replace-on-mint (decision): a session holds at most one live
		// signature token — the new mint revokes the session's previous
		// one, and signature tokens never count against the message cap.
		s.revokeKindLocked(sessionKey, mailPreviewKindSignature)
		ttl = MailSignaturePreviewTokenTTL
	} else if s.countKindLocked(sessionKey, mailPreviewKindMessage) >= MailPreviewMaxLiveTokensPerSession {
		return "", ErrMailPreviewCap
	}
	grant.SessionKey = sessionKey
	grant.ExpiresAt = s.now().Add(ttl)
	s.byToken[token] = grant
	if s.bySession[sessionKey] == nil {
		s.bySession[sessionKey] = make(map[string]struct{})
	}
	s.bySession[sessionKey][token] = struct{}{}
	return token, nil
}

// mailPreviewTokenBytes is the token entropy before encoding: 32 bytes =
// 43 base64url chars, the MailPreviewTokenResponse.token wire bound.
const mailPreviewTokenBytes = 32

// lookup returns the grant for token; unknown, revoked and expired are the
// same answer (MC-43). A lookup at exactly ExpiresAt is refused.
func (s *mailPreviewTokenStore) lookup(token string) (mailPreviewGrant, bool) {
	if token == "" {
		return mailPreviewGrant{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.byToken[token]
	if !ok {
		return mailPreviewGrant{}, false
	}
	if !s.now().Before(g.ExpiresAt) {
		return mailPreviewGrant{}, false
	}
	return g, true
}

// invalidateSession revokes every token minted by one session (MC-43
// logout revocation).
func (s *mailPreviewTokenStore) invalidateSession(sessionKey string) int {
	if sessionKey == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tokens := s.bySession[sessionKey]
	revoked := 0
	for tok := range tokens {
		delete(s.byToken, tok)
		revoked++
	}
	delete(s.bySession, sessionKey)
	return revoked
}

// purgeLocked drops expired entries. Caller holds s.mu.
func (s *mailPreviewTokenStore) purgeLocked() {
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

// countKindLocked counts the session's live tokens of one kind. Caller
// holds s.mu.
func (s *mailPreviewTokenStore) countKindLocked(sessionKey, kind string) int {
	n := 0
	for tok := range s.bySession[sessionKey] {
		if g, ok := s.byToken[tok]; ok && grantKindOf(g) == kind {
			n++
		}
	}
	return n
}

// revokeKindLocked revokes the session's live tokens of one kind. Caller
// holds s.mu.
func (s *mailPreviewTokenStore) revokeKindLocked(sessionKey, kind string) {
	for tok := range s.bySession[sessionKey] {
		if g, ok := s.byToken[tok]; ok && grantKindOf(g) == kind {
			delete(s.byToken, tok)
			delete(s.bySession[sessionKey], tok)
		}
	}
	if set := s.bySession[sessionKey]; len(set) == 0 {
		delete(s.bySession, sessionKey)
	}
}

// recordRemoteURLs overwrites the grant's recorded remote-image URL list
// with the list the CURRENT serve's authorized fetch produced (serve-time
// recording, §13 Q5). A dead token records nothing and reports false — the
// caller still finishes its own response; the list only matters for later
// proxy requests, which a dead token no longer serves.
func (s *mailPreviewTokenStore) recordRemoteURLs(token string, urls []string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.byToken[token]
	if !ok || !s.now().Before(g.ExpiresAt) {
		return false
	}
	g.RemoteURLs = urls
	s.byToken[token] = g
	return true
}

// invalidatePair kills every MESSAGE grant of one (agent, workspace) pair —
// the removal/disable cascade's grant revocation (w5 US-5.1). Signature
// grants carry no pair identity and are untouched.
func (s *mailPreviewTokenStore) invalidatePair(agentID, workspaceID string) {
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
