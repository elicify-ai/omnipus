package gateway

// mail_preview_token.go - the Mail preview token store (MC-43). Same
// hygiene as the Library store (preview_token.go): 256-bit crypto/rand
// token, 43-char base64url, named TTL, per-session live-token cap that
// REFUSES never evicts, logout revocation, unknown/expired/revoked all one
// answer (404 on the wire).
//
// The mail store differs from the Library store in one structural way: a
// mail grant carries the ALREADY-FETCHED sanitized payload (HTML, inline
// cid parts, and the mint-time-recorded remote-image URL list), so the
// /mail-preview/ serve routes never dial IMAP (spec 2.3a).

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

// MailPreviewMaxLiveTokensPerSession caps live tokens per session (MC-43);
// the ninth mint is refused, never an eviction.
const MailPreviewMaxLiveTokensPerSession = 8

// mailPreviewInline is one inline cid part ready to serve on
// /mail-preview/part/{token}/{index}.
type mailPreviewInline struct {
	ContentType string
	Data        []byte
}

// mailPreviewGrant is what a mail preview token buys: the sanitized HTML,
// the inline parts and - only when load_remote was minted true - the
// remote-image URL list recorded AT MINT TIME (MC-41: the proxy serves
// store-recorded URLs only). Bound to the (pair, folder, ref, load_remote)
// identity (MC-43).
type mailPreviewGrant struct {
	WorkspaceID string
	AgentID     string
	Folder      string
	Ref         string
	LoadRemote  bool
	SessionKey  string
	ExpiresAt   time.Time
	HTML        string
	Inline      []mailPreviewInline
	RemoteURLs  []string
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
	if len(s.bySession[sessionKey]) >= MailPreviewMaxLiveTokensPerSession {
		return "", ErrMailPreviewCap
	}
	grant.SessionKey = sessionKey
	grant.ExpiresAt = s.now().Add(MailPreviewTokenTTL)
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
