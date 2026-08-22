// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Tests for the Library preview-token store (ADR-067 stage 1).
//
// Every expected value below is derived from the SPECIFICATION, not from what
// the implementation happens to do:
//
//	FR-003b  one workspace, one path, never wider; a token never widens access
//	FR-003d  15 minutes (MV-20), plus logout, mount revoke, and delete/move
//	FR-003h  32 bytes from crypto/rand, base64url, FAIL CLOSED on entropy error
//	FR-003k  at most 8 live tokens per session
//	FR-003m  a re-mint returns a NEW value and invalidates the previous one
//	§10.5    in-memory storage; a gateway restart invalidates every preview
//
// Where the spec names a constant (fifteen minutes, eight tokens, thirty-two
// bytes) the test asserts BOTH the constant's value and the behaviour driven
// by it, so a change to one without the other cannot pass.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// previewFixedClock returns a clock function reading a mutable instant, so TTL
// boundaries are asserted against the named constant instead of a sleep.
func previewFixedClock(at *time.Time) func() time.Time {
	return func() time.Time { return *at }
}

func mustMintPreview(t *testing.T, s *PreviewTokenStore, session, ws, path string, scope PreviewScope) string {
	t.Helper()
	tok, _, err := s.Mint(session, ws, path, scope)
	if err != nil {
		t.Fatalf("Mint(%q, %q, %q, %q): unexpected error: %v", session, ws, path, scope, err)
	}
	return tok
}

// --- FR-003h: entropy, encoding, and failing closed -------------------------

// TestPreviewToken_EntropyAndFailClosed is test 112 of the ADR-067 TDD plan.
//
// The spec's own note on what a distinctness check alone MISSES: an
// implementation that ignores the entropy error and issues a zero-filled
// token. Every token in a run of that implementation is identical, so
// distinctness catches it only by accident; the second half below drives the
// error deliberately.
func TestPreviewToken_EntropyAndFailClosed(t *testing.T) {
	// Spec §10.5 / FR-003h: 32 bytes, base64url, 43 characters on the wire.
	const specBytes = 32
	const specEncodedLen = 43
	if previewTokenBytes != specBytes {
		t.Fatalf("previewTokenBytes = %d, spec FR-003h requires %d", previewTokenBytes, specBytes)
	}
	if PreviewTokenEncodedLen != specEncodedLen {
		t.Fatalf("PreviewTokenEncodedLen = %d, the wire contract pins %d", PreviewTokenEncodedLen, specEncodedLen)
	}

	t.Run("thousand mints are 32 bytes each and all distinct", func(t *testing.T) {
		s := NewPreviewTokenStore()
		seen := make(map[string]struct{}, 1000)
		for i := 0; i < 1000; i++ {
			// A fresh session each time so the FR-003k cap is not the thing
			// under test here.
			session := fmt.Sprintf("session-%d", i)
			tok, _, err := s.Mint(session, "ws1", "reports/index.html", PreviewScopeFile)
			if err != nil {
				t.Fatalf("mint %d: %v", i, err)
			}
			if len(tok) != specEncodedLen {
				t.Fatalf("mint %d: token length %d, want %d", i, len(tok), specEncodedLen)
			}
			raw, decErr := base64.RawURLEncoding.DecodeString(tok)
			if decErr != nil {
				t.Fatalf("mint %d: token %q is not base64url-unpadded: %v", i, tok, decErr)
			}
			if len(raw) != specBytes {
				t.Fatalf("mint %d: decoded %d bytes, want %d", i, len(raw), specBytes)
			}
			if _, dup := seen[tok]; dup {
				t.Fatalf("mint %d: duplicate token issued", i)
			}
			seen[tok] = struct{}{}
		}
		if len(seen) != 1000 {
			t.Fatalf("distinct tokens = %d, want 1000", len(seen))
		}
	})

	t.Run("entropy error issues no token and mutates nothing", func(t *testing.T) {
		boom := errors.New("entropy device offline")
		s := NewPreviewTokenStore(WithPreviewTokenRand(func(b []byte) (int, error) {
			// Fill the buffer anyway: an implementation that ignores the error
			// would then issue a perfectly plausible-looking token, which is
			// precisely the failure this asserts against.
			for i := range b {
				b[i] = 0x41
			}
			return len(b), boom
		}))
		tok, grant, err := s.Mint("session-a", "ws1", "reports/index.html", PreviewScopeFile)
		if err == nil {
			t.Fatal("Mint succeeded despite an entropy failure — FR-003h requires it to fail closed")
		}
		if !errors.Is(err, ErrPreviewTokenEntropy) {
			t.Fatalf("error = %v, want it to wrap ErrPreviewTokenEntropy", err)
		}
		if !errors.Is(err, boom) {
			t.Fatalf("error = %v, want it to carry the underlying cause", err)
		}
		if tok != "" {
			t.Fatalf("token = %q, want empty — no token, no fallback, no shortened value", tok)
		}
		if grant != (PreviewGrant{}) {
			t.Fatalf("grant = %+v, want the zero grant", grant)
		}
		if n := s.LiveCount(); n != 0 {
			t.Fatalf("live tokens = %d after a failed mint, want 0", n)
		}
	})

	t.Run("short read issues no token", func(t *testing.T) {
		s := NewPreviewTokenStore(WithPreviewTokenRand(func(b []byte) (int, error) {
			// No error, but only half the entropy. The remainder is zero — the
			// "shortened value" FR-003h forbids by name.
			for i := 0; i < len(b)/2; i++ {
				b[i] = 0x42
			}
			return len(b) / 2, nil
		}))
		tok, _, err := s.Mint("session-a", "ws1", "reports/index.html", PreviewScopeFile)
		if err == nil {
			t.Fatal("Mint succeeded on a short entropy read — FR-003h requires fail closed")
		}
		if !errors.Is(err, ErrPreviewTokenEntropy) {
			t.Fatalf("error = %v, want ErrPreviewTokenEntropy", err)
		}
		if tok != "" {
			t.Fatalf("token = %q, want empty", tok)
		}
		if n := s.LiveCount(); n != 0 {
			t.Fatalf("live tokens = %d, want 0", n)
		}
	})

	t.Run("an entropy failure does not revoke the token it would have replaced", func(t *testing.T) {
		// A re-mint (FR-003m) invalidates the previous token. If the store
		// revoked first and drew entropy second, an entropy outage would leave
		// the operator with a dead preview AND no replacement.
		calls := 0
		s := NewPreviewTokenStore(WithPreviewTokenRand(func(b []byte) (int, error) {
			calls++
			if calls > 1 {
				return 0, errors.New("entropy device offline")
			}
			for i := range b {
				b[i] = byte(i)
			}
			return len(b), nil
		}))
		first := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		if _, _, err := s.Mint("session-a", "ws1", "reports/q3", PreviewScopeBundle); err == nil {
			t.Fatal("second Mint succeeded, want an entropy failure")
		}
		if _, ok := s.Lookup(first); !ok {
			t.Fatal("the previous token was revoked by a mint that issued nothing")
		}
	})
}

// --- FR-003d / MV-20: the fifteen-minute lifetime ---------------------------

// TestPreviewToken_TtlBoundary is test 91. MV-20: "refused 15 minutes after
// minting and accepted at 14, against a named constant rather than a literal
// repeated in handler and test."
func TestPreviewToken_TtlBoundary(t *testing.T) {
	if PreviewTokenTTL != 15*time.Minute {
		t.Fatalf("PreviewTokenTTL = %v, FR-003d requires 15m", PreviewTokenTTL)
	}

	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	s := NewPreviewTokenStore(WithPreviewTokenClock(previewFixedClock(&now)))

	tok, grant, err := s.Mint("session-a", "ws1", "reports/index.html", PreviewScopeFile)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if want := now.Add(PreviewTokenTTL); !grant.ExpiresAt.Equal(want) {
		t.Fatalf("grant.ExpiresAt = %v, want %v (mint time + PreviewTokenTTL)", grant.ExpiresAt, want)
	}

	cases := []struct {
		name    string
		elapsed time.Duration
		wantOK  bool
	}{
		{"at mint", 0, true},
		{"one minute before expiry (MV-20's accepted-at-14 case)", PreviewTokenTTL - time.Minute, true},
		{"one nanosecond before expiry", PreviewTokenTTL - time.Nanosecond, true},
		{"exactly at expiry", PreviewTokenTTL, false},
		{"one nanosecond after expiry", PreviewTokenTTL + time.Nanosecond, false},
		{"one minute after expiry", PreviewTokenTTL + time.Minute, false},
	}
	minted := now
	for _, tc := range cases {
		now = minted.Add(tc.elapsed)
		_, ok := s.Lookup(tok)
		if ok != tc.wantOK {
			t.Errorf("%s: Lookup ok = %v, want %v", tc.name, ok, tc.wantOK)
		}
	}
}

// --- FR-003d: expiry is not revocation --------------------------------------

// TestPreviewToken_InvalidatedOnLogoutMountRevokeAndDeletion is test 92.
// "Mint, log out, use — refused. Also mount revoked, and file deleted."
func TestPreviewToken_InvalidatedOnLogoutMountRevokeAndDeletion(t *testing.T) {
	t.Run("logout revokes every token of the minting session and no other", func(t *testing.T) {
		s := NewPreviewTokenStore()
		adminA := mustMintPreview(t, s, "session-admin", "ws1", "reports/q3", PreviewScopeBundle)
		adminB := mustMintPreview(t, s, "session-admin", "ws1", "notes/todo.md", PreviewScopeFile)
		other := mustMintPreview(t, s, "session-other", "ws1", "reports/q3", PreviewScopeBundle)

		if n := s.InvalidateSession("session-admin"); n != 2 {
			t.Fatalf("InvalidateSession revoked %d tokens, want 2", n)
		}
		for _, tok := range []string{adminA, adminB} {
			if _, ok := s.Lookup(tok); ok {
				t.Error("a token minted by the logged-out session is still valid — " +
					"it is now an unauthenticated read grant (FR-003d)")
			}
		}
		if _, ok := s.Lookup(other); !ok {
			t.Error("another session's token was revoked by this session's logout")
		}
	})

	t.Run("mount revoke kills the workspace's tokens only", func(t *testing.T) {
		s := NewPreviewTokenStore()
		inWS := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		otherWS := mustMintPreview(t, s, "session-a", "ws2", "reports/q3", PreviewScopeBundle)

		if n := s.InvalidateWorkspace("ws1"); n != 1 {
			t.Fatalf("InvalidateWorkspace revoked %d, want 1", n)
		}
		if _, ok := s.Lookup(inWS); ok {
			t.Error("token survived its workspace's mount being revoked (FR-003d)")
		}
		if _, ok := s.Lookup(otherWS); !ok {
			t.Error("revoking ws1's mount revoked a ws2 token")
		}
	})

	t.Run("deleting or moving the named path revokes its token", func(t *testing.T) {
		s := NewPreviewTokenStore()
		file := mustMintPreview(t, s, "session-a", "ws1", "notes/todo.md", PreviewScopeFile)
		bundle := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		unrelated := mustMintPreview(t, s, "session-a", "ws1", "reports/q4", PreviewScopeBundle)

		if n := s.InvalidatePath("ws1", "notes/todo.md"); n != 1 {
			t.Fatalf("InvalidatePath(file) revoked %d, want 1", n)
		}
		if _, ok := s.Lookup(file); ok {
			t.Error("token survived deletion of the file it names (FR-003d)")
		}
		if n := s.InvalidatePath("ws1", "reports/q3"); n != 1 {
			t.Fatalf("InvalidatePath(bundle root) revoked %d, want 1", n)
		}
		if _, ok := s.Lookup(bundle); ok {
			t.Error("token survived deletion of the bundle root it names (FR-003d)")
		}
		if _, ok := s.Lookup(unrelated); !ok {
			t.Error("deleting reports/q3 revoked the sibling reports/q4's token")
		}
	})

	t.Run("deleting an ancestor directory revokes tokens beneath it", func(t *testing.T) {
		// Nobody names "reports/q3" when they delete "reports", but the named
		// path is gone all the same.
		s := NewPreviewTokenStore()
		beneath := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		sibling := mustMintPreview(t, s, "session-a", "ws1", "reportsX/q3", PreviewScopeBundle)

		if n := s.InvalidatePath("ws1", "reports"); n != 1 {
			t.Fatalf("InvalidatePath(ancestor) revoked %d, want 1", n)
		}
		if _, ok := s.Lookup(beneath); ok {
			t.Error("a token beneath a deleted directory survived it")
		}
		if _, ok := s.Lookup(sibling); !ok {
			t.Error("deleting \"reports\" revoked a token under \"reportsX\" — prefix match without a separator")
		}
	})

	t.Run("deleting a file inside a bundle does not revoke the bundle", func(t *testing.T) {
		// FR-003d names "the file or bundle root". A bundle losing one image is
		// not a revocation event; revoking here would make every edit-and-save
		// inside an open preview kill the preview.
		s := NewPreviewTokenStore()
		bundle := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		if n := s.InvalidatePath("ws1", "reports/q3/logo.png"); n != 0 {
			t.Fatalf("InvalidatePath(descendant) revoked %d, want 0", n)
		}
		if _, ok := s.Lookup(bundle); !ok {
			t.Error("deleting one file inside a bundle revoked the whole bundle's token")
		}
	})

	t.Run("a malformed or empty path revokes nothing", func(t *testing.T) {
		s := NewPreviewTokenStore()
		tok := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		for _, bad := range []string{"", ".", "../escape", "/absolute"} {
			if n := s.InvalidatePath("ws1", bad); n != 0 {
				t.Errorf("InvalidatePath(%q) revoked %d tokens, want 0 — a bad argument must not "+
					"become a workspace-wide denial of service", bad, n)
			}
		}
		if _, ok := s.Lookup(tok); !ok {
			t.Error("a malformed path argument revoked a live token")
		}
	})

	t.Run("InvalidateToken revokes exactly one", func(t *testing.T) {
		s := NewPreviewTokenStore()
		a := mustMintPreview(t, s, "session-a", "ws1", "a.html", PreviewScopeFile)
		b := mustMintPreview(t, s, "session-a", "ws1", "b.html", PreviewScopeFile)
		if !s.InvalidateToken(a) {
			t.Fatal("InvalidateToken reported the live token as unknown")
		}
		if s.InvalidateToken(a) {
			t.Error("InvalidateToken reported a second revocation of the same token")
		}
		if _, ok := s.Lookup(a); ok {
			t.Error("revoked token still resolves")
		}
		if _, ok := s.Lookup(b); !ok {
			t.Error("revoking one token revoked another")
		}
	})
}

// --- §10.5: in-memory storage -----------------------------------------------

// TestPreviewToken_GatewayRestartInvalidatesEverything asserts the property
// §10.5 records as accepted rather than leaving it implicit: the store is in
// memory, so a restart invalidates every live preview. A process restart is a
// fresh store, which is what this constructs.
func TestPreviewToken_GatewayRestartInvalidatesEverything(t *testing.T) {
	before := NewPreviewTokenStore()
	tok := mustMintPreview(t, before, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
	if _, ok := before.Lookup(tok); !ok {
		t.Fatal("token does not resolve in the store that minted it")
	}

	after := NewPreviewTokenStore() // the gateway restarted
	if _, ok := after.Lookup(tok); ok {
		t.Fatal("a token minted before the restart still resolves — the store is not in-memory-only")
	}
	if n := after.LiveCount(); n != 0 {
		t.Fatalf("a fresh store holds %d tokens, want 0", n)
	}
}

// --- FR-003b: scope, and never widening access ------------------------------

func TestPreviewToken_ScopeIsOneWorkspaceAndOnePath(t *testing.T) {
	t.Run("there is no whole-workspace scope", func(t *testing.T) {
		// CleanRelPath maps "", "." and "./" to the empty string, which means
		// the work-tree root. Accepting any of them would mint exactly the
		// whole-workspace grant FR-003b says does not exist.
		s := NewPreviewTokenStore()
		for _, root := range []string{"", ".", "./", "././."} {
			tok, _, err := s.Mint("session-a", "ws1", root, PreviewScopeBundle)
			if !errors.Is(err, ErrPreviewTokenPath) {
				t.Errorf("Mint(path=%q) err = %v, want ErrPreviewTokenPath — that is a whole-workspace grant", root, err)
			}
			if tok != "" {
				t.Errorf("Mint(path=%q) issued token %q", root, tok)
			}
		}
	})

	t.Run("a traversing or absolute path is refused", func(t *testing.T) {
		s := NewPreviewTokenStore()
		for _, root := range []string{
			"..", "../etc/passwd", "reports/../../etc/passwd",
			"/etc/passwd", "reports\\q3", "reports/\x00q3", "reports/\nq3",
		} {
			tok, _, err := s.Mint("session-a", "ws1", root, PreviewScopeBundle)
			if !errors.Is(err, ErrPreviewTokenPath) {
				t.Errorf("Mint(path=%q) err = %v, want ErrPreviewTokenPath", root, err)
			}
			if tok != "" {
				t.Errorf("Mint(path=%q) issued token %q", root, tok)
			}
		}
	})

	t.Run("the scope root is stored cleaned", func(t *testing.T) {
		s := NewPreviewTokenStore()
		_, grant, err := s.Mint("session-a", "ws1", "reports/./q3/", PreviewScopeBundle)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		if grant.ScopeRoot != "reports/q3" {
			t.Fatalf("ScopeRoot = %q, want the cleaned form %q", grant.ScopeRoot, "reports/q3")
		}
		if grant.WorkspaceID != "ws1" || grant.Scope != PreviewScopeBundle {
			t.Fatalf("grant = %+v, want workspace ws1 and scope bundle", grant)
		}
	})

	t.Run("required arguments are refused when missing", func(t *testing.T) {
		s := NewPreviewTokenStore()
		if _, _, err := s.Mint("", "ws1", "a.html", PreviewScopeFile); !errors.Is(err, ErrPreviewTokenSession) {
			t.Errorf("anonymous mint err = %v, want ErrPreviewTokenSession — a grant with no session "+
				"can never be revoked at logout", err)
		}
		if _, _, err := s.Mint("session-a", "", "a.html", PreviewScopeFile); !errors.Is(err, ErrPreviewTokenWorkspace) {
			t.Errorf("empty-workspace mint err = %v, want ErrPreviewTokenWorkspace", err)
		}
		if _, _, err := s.Mint("session-a", "ws1", "a.html", PreviewScope("workspace")); !errors.Is(err, ErrPreviewTokenScope) {
			t.Errorf("unknown-scope mint err = %v, want ErrPreviewTokenScope", err)
		}
		if n := s.LiveCount(); n != 0 {
			t.Fatalf("refused mints left %d tokens behind", n)
		}
	})

	t.Run("AllowsRelPath: a file grant is exactly one file, a bundle is a root and its descendants", func(t *testing.T) {
		fileGrant := PreviewGrant{Scope: PreviewScopeFile, ScopeRoot: "reports/q3/index.html"}
		bundleGrant := PreviewGrant{Scope: PreviewScopeBundle, ScopeRoot: "reports/q3"}

		cases := []struct {
			name  string
			grant PreviewGrant
			rel   string
			want  bool
		}{
			{"file grant admits its own file", fileGrant, "reports/q3/index.html", true},
			{"file grant refuses a sibling", fileGrant, "reports/q3/style.css", false},
			{"file grant refuses its own directory", fileGrant, "reports/q3", false},
			{"file grant refuses the empty path", fileGrant, "", false},
			{"bundle grant admits its root", bundleGrant, "reports/q3", true},
			{"bundle grant admits a descendant", bundleGrant, "reports/q3/style.css", true},
			{"bundle grant admits a deep descendant", bundleGrant, "reports/q3/assets/fonts/a.woff2", true},
			{"bundle grant refuses a prefix-sharing sibling", bundleGrant, "reports/q3x/style.css", false},
			{"bundle grant refuses its parent", bundleGrant, "reports", false},
			{"bundle grant refuses an unrelated path", bundleGrant, "notes/todo.md", false},
			{"bundle grant refuses the empty path", bundleGrant, "", false},
			{"a zero grant admits nothing", PreviewGrant{}, "anything", false},
		}
		for _, tc := range cases {
			if got := tc.grant.AllowsRelPath(tc.rel); got != tc.want {
				t.Errorf("%s: AllowsRelPath(%q) = %v, want %v", tc.name, tc.rel, got, tc.want)
			}
		}
	})
}

// --- FR-003k: at most eight live tokens per session -------------------------

func TestPreviewToken_LiveTokenCapPerSession(t *testing.T) {
	if MaxLivePreviewTokensPerSession != 8 {
		t.Fatalf("MaxLivePreviewTokensPerSession = %d, FR-003k requires 8", MaxLivePreviewTokensPerSession)
	}

	t.Run("the ninth live token is refused", func(t *testing.T) {
		s := NewPreviewTokenStore()
		var tokens []string
		for i := 0; i < MaxLivePreviewTokensPerSession; i++ {
			tokens = append(tokens, mustMintPreview(t, s, "session-a", "ws1", fmt.Sprintf("doc%d.html", i), PreviewScopeFile))
		}
		if n := s.LiveCountForSession("session-a"); n != MaxLivePreviewTokensPerSession {
			t.Fatalf("live tokens for the session = %d, want %d", n, MaxLivePreviewTokensPerSession)
		}
		ninth, _, err := s.Mint("session-a", "ws1", "doc8.html", PreviewScopeFile)
		if !errors.Is(err, ErrPreviewTokenCap) {
			t.Fatalf("ninth mint err = %v, want ErrPreviewTokenCap", err)
		}
		if ninth != "" {
			t.Fatalf("ninth mint issued token %q", ninth)
		}
		// Refusal, not eviction: the eight already handed out keep working.
		for i, tok := range tokens {
			if _, ok := s.Lookup(tok); !ok {
				t.Errorf("token %d was evicted to make room for a refused ninth", i)
			}
		}
	})

	t.Run("the cap is per session, not global", func(t *testing.T) {
		s := NewPreviewTokenStore()
		for i := 0; i < MaxLivePreviewTokensPerSession; i++ {
			mustMintPreview(t, s, "session-a", "ws1", fmt.Sprintf("doc%d.html", i), PreviewScopeFile)
		}
		if _, _, err := s.Mint("session-b", "ws1", "doc0.html", PreviewScopeFile); err != nil {
			t.Fatalf("a second session was refused because the first is at its cap: %v", err)
		}
	})

	t.Run("revoking a token frees a slot", func(t *testing.T) {
		s := NewPreviewTokenStore()
		var first string
		for i := 0; i < MaxLivePreviewTokensPerSession; i++ {
			tok := mustMintPreview(t, s, "session-a", "ws1", fmt.Sprintf("doc%d.html", i), PreviewScopeFile)
			if i == 0 {
				first = tok
			}
		}
		if !s.InvalidateToken(first) {
			t.Fatal("InvalidateToken failed")
		}
		if _, _, err := s.Mint("session-a", "ws1", "doc8.html", PreviewScopeFile); err != nil {
			t.Fatalf("mint after revocation: %v, want success — the revoked slot was not released", err)
		}
	})

	t.Run("expired tokens do not occupy slots", func(t *testing.T) {
		// The cap counts LIVE tokens (FR-003k). Counting dead ones would lock a
		// session out of previews for fifteen minutes after eight documents.
		now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
		s := NewPreviewTokenStore(WithPreviewTokenClock(previewFixedClock(&now)))
		for i := 0; i < MaxLivePreviewTokensPerSession; i++ {
			mustMintPreview(t, s, "session-a", "ws1", fmt.Sprintf("doc%d.html", i), PreviewScopeFile)
		}
		now = now.Add(PreviewTokenTTL)
		if _, _, err := s.Mint("session-a", "ws1", "doc8.html", PreviewScopeFile); err != nil {
			t.Fatalf("mint after every prior token expired: %v, want success", err)
		}
		if n := s.LiveCountForSession("session-a"); n != 1 {
			t.Fatalf("live tokens for the session = %d, want 1 — expired entries were not swept", n)
		}
	})
}

// --- FR-003m: a re-mint rotates the value -----------------------------------

// TestPreviewToken_ReMintRotatesValue guards against the precedent-copying
// mistake §10.5 names explicitly: pkg/agent/served_subdirs.go returns the SAME
// token string when the same directory is re-registered, so the credential
// lives as long as the tab does — the exact property a 15-minute lifetime
// exists to prevent.
func TestPreviewToken_ReMintRotatesValue(t *testing.T) {
	t.Run("the same scope re-minted returns a new value and kills the old", func(t *testing.T) {
		s := NewPreviewTokenStore()
		first := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		second := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)

		if second == first {
			t.Fatal("re-mint returned the SAME token string (FR-003m) — the lifetime is now meaningless")
		}
		if _, ok := s.Lookup(first); ok {
			t.Error("the previous token still resolves after a re-mint (FR-003m)")
		}
		if _, ok := s.Lookup(second); !ok {
			t.Error("the fresh token does not resolve")
		}
	})

	t.Run("re-minting extends the lifetime from the re-mint, not the original", func(t *testing.T) {
		now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
		s := NewPreviewTokenStore(WithPreviewTokenClock(previewFixedClock(&now)))
		mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		now = now.Add(10 * time.Minute)
		_, grant, err := s.Mint("session-a", "ws1", "reports/q3", PreviewScopeBundle)
		if err != nil {
			t.Fatalf("re-mint: %v", err)
		}
		if want := now.Add(PreviewTokenTTL); !grant.ExpiresAt.Equal(want) {
			t.Fatalf("re-minted ExpiresAt = %v, want %v", grant.ExpiresAt, want)
		}
	})

	t.Run("re-minting does not consume another slot of the cap", func(t *testing.T) {
		s := NewPreviewTokenStore()
		for i := 0; i < MaxLivePreviewTokensPerSession; i++ {
			mustMintPreview(t, s, "session-a", "ws1", fmt.Sprintf("doc%d.html", i), PreviewScopeFile)
		}
		// The SPA's Reload button re-mints an already-open document. If that
		// consumed a slot, the eighth document could never be reloaded.
		if _, _, err := s.Mint("session-a", "ws1", "doc0.html", PreviewScopeFile); err != nil {
			t.Fatalf("re-mint at the cap: %v, want success", err)
		}
		if n := s.LiveCountForSession("session-a"); n != MaxLivePreviewTokensPerSession {
			t.Fatalf("live tokens = %d, want %d", n, MaxLivePreviewTokensPerSession)
		}
	})

	t.Run("one session's re-mint does not revoke another session's token", func(t *testing.T) {
		s := NewPreviewTokenStore()
		theirs := mustMintPreview(t, s, "session-b", "ws1", "reports/q3", PreviewScopeBundle)
		mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		if _, ok := s.Lookup(theirs); !ok {
			t.Fatal("session A's re-mint of the same path revoked session B's live token")
		}
	})

	t.Run("a different scope of the same path is a different grant", func(t *testing.T) {
		s := NewPreviewTokenStore()
		asFile := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeFile)
		asBundle := mustMintPreview(t, s, "session-a", "ws1", "reports/q3", PreviewScopeBundle)
		if _, ok := s.Lookup(asFile); !ok {
			t.Error("minting a bundle grant revoked the file grant for the same path")
		}
		if _, ok := s.Lookup(asBundle); !ok {
			t.Error("the bundle grant does not resolve")
		}
	})
}

// --- Lookup is uniform for unknown, revoked and expired ---------------------

// FR-003n requires the SERVING handler to make expired, revoked and unknown
// indistinguishable on the wire. That is far easier to hold if the store never
// distinguishes them either, so it does not.
func TestPreviewToken_LookupIsUniformForUnknownRevokedAndExpired(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	s := NewPreviewTokenStore(WithPreviewTokenClock(previewFixedClock(&now)))

	revoked := mustMintPreview(t, s, "session-a", "ws1", "a.html", PreviewScopeFile)
	s.InvalidateToken(revoked)
	expired := mustMintPreview(t, s, "session-a", "ws1", "b.html", PreviewScopeFile)
	now = now.Add(PreviewTokenTTL)

	for _, tc := range []struct{ name, token string }{
		{"unknown", "MZ8vQ2mR7xT1yB4nW6cA9pL0sD3fG5hJ8kM2nP4qR6t"},
		{"revoked", revoked},
		{"expired", expired},
		{"empty", ""},
	} {
		grant, ok := s.Lookup(tc.token)
		if ok {
			t.Errorf("%s token resolved", tc.name)
		}
		if grant != (PreviewGrant{}) {
			t.Errorf("%s token returned grant %+v, want the zero grant — a non-zero grant beside "+
				"ok=false is an oracle for whether the token ever existed", tc.name, grant)
		}
	}
}

// --- PurgeExpired -----------------------------------------------------------

func TestPreviewToken_PurgeExpired(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	s := NewPreviewTokenStore(WithPreviewTokenClock(previewFixedClock(&now)))

	old := mustMintPreview(t, s, "session-a", "ws1", "a.html", PreviewScopeFile)
	now = now.Add(PreviewTokenTTL - time.Minute)
	fresh := mustMintPreview(t, s, "session-a", "ws1", "b.html", PreviewScopeFile)
	now = now.Add(time.Minute) // old is now exactly at expiry; fresh has 14m left

	if n := s.PurgeExpired(); n != 1 {
		t.Fatalf("PurgeExpired dropped %d, want 1", n)
	}
	if _, ok := s.Lookup(old); ok {
		t.Error("the expired token still resolves")
	}
	if _, ok := s.Lookup(fresh); !ok {
		t.Error("PurgeExpired dropped a live token")
	}
	if n := s.PurgeExpired(); n != 0 {
		t.Fatalf("a second PurgeExpired dropped %d, want 0", n)
	}
}

// --- Session identity -------------------------------------------------------

func TestPreviewSessionKey(t *testing.T) {
	const cookieValue = "PLAINTEXT-SESSION-COOKIE-VALUE"
	const bearerValue = "omnipus_abcd_PLAINTEXT-BEARER-TOKEN"

	withCookie := func() *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "/api/v1/library/preview-token", nil)
		r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookieValue})
		return r
	}
	withBearer := func() *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "/api/v1/library/preview-token", nil)
		r.Header.Set("Authorization", "Bearer "+bearerValue)
		return r
	}

	t.Run("a cookie request yields a stable key", func(t *testing.T) {
		k1, ok1 := PreviewSessionKey(withCookie())
		k2, ok2 := PreviewSessionKey(withCookie())
		if !ok1 || !ok2 {
			t.Fatal("a cookie-bearing request produced no session key")
		}
		if k1 != k2 {
			t.Fatal("the same cookie produced two different keys — logout would revoke nothing")
		}
	})

	t.Run("the key is not the credential", func(t *testing.T) {
		// The key is a map key and may end up in a heap dump or a debug print.
		// If it contained the cookie value, that print would be a replayable
		// credential (FR-003e's concern, one layer down).
		k, _ := PreviewSessionKey(withCookie())
		if strings.Contains(k, cookieValue) {
			t.Fatalf("session key %q contains the plaintext cookie", k)
		}
		kb, _ := PreviewSessionKey(withBearer())
		if strings.Contains(kb, bearerValue) {
			t.Fatalf("session key %q contains the plaintext bearer token", kb)
		}
	})

	t.Run("different credentials yield different keys", func(t *testing.T) {
		a, _ := PreviewSessionKey(withCookie())
		b, _ := PreviewSessionKey(withBearer())
		if a == b {
			t.Fatal("a cookie session and a bearer session share a key")
		}

		other, _ := http.NewRequest(http.MethodPost, "/x", nil)
		other.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "A-DIFFERENT-VALUE"})
		c, _ := PreviewSessionKey(other)
		if c == a {
			t.Fatal("two different cookies share a key — one user's logout would revoke another's tokens")
		}
	})

	t.Run("a cookie and a bearer token of identical bytes do not collide", func(t *testing.T) {
		const shared = "IDENTICAL-BYTES"
		rc, _ := http.NewRequest(http.MethodPost, "/x", nil)
		rc.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: shared})
		rb, _ := http.NewRequest(http.MethodPost, "/x", nil)
		rb.Header.Set("Authorization", "Bearer "+shared)
		kc, _ := PreviewSessionKey(rc)
		kb, _ := PreviewSessionKey(rb)
		if kc == kb {
			t.Fatal("a cookie and a bearer token of the same bytes produced the same key")
		}
	})

	t.Run("the cookie wins when both are present", func(t *testing.T) {
		// Matches withOptionalAuth's documented precedence: the cookie is the
		// SPA's primary path and wins on mismatch.
		r := withCookie()
		r.Header.Set("Authorization", "Bearer "+bearerValue)
		k, ok := PreviewSessionKey(r)
		if !ok {
			t.Fatal("no key produced")
		}
		want, _ := PreviewSessionKey(withCookie())
		if k != want {
			t.Fatal("the bearer token took precedence over the session cookie")
		}
	})

	t.Run("no credential yields no key", func(t *testing.T) {
		anon, _ := http.NewRequest(http.MethodPost, "/x", nil)
		if _, ok := PreviewSessionKey(anon); ok {
			t.Fatal("an anonymous request produced a session key — its grant could never be revoked at logout")
		}
		empty, _ := http.NewRequest(http.MethodPost, "/x", nil)
		empty.Header.Set("Authorization", "Bearer ")
		if _, ok := PreviewSessionKey(empty); ok {
			t.Fatal("an empty bearer token produced a session key")
		}
		if _, ok := PreviewSessionKey(nil); ok {
			t.Fatal("a nil request produced a session key")
		}
	})
}

// --- Concurrency ------------------------------------------------------------

// TestPreviewToken_ConcurrentUse exercises the store the way the gateway does —
// from many HTTP handlers at once. Run under -race, this fails on an
// unsynchronised map access; without -race it still catches a lost token or a
// leaked cap slot.
func TestPreviewToken_ConcurrentUse(t *testing.T) {
	s := NewPreviewTokenStore()

	const sessions = 8
	const perSession = 40
	var wg sync.WaitGroup

	for i := 0; i < sessions; i++ {
		session := fmt.Sprintf("session-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perSession; j++ {
				// Re-mint the same few scopes so the byScope rotation path and
				// the cap path are both exercised concurrently.
				path := fmt.Sprintf("docs/doc%d.html", j%4)
				tok, _, err := s.Mint(session, "ws1", path, PreviewScopeFile)
				if err != nil {
					continue
				}
				s.Lookup(tok)
				if j%7 == 0 {
					s.InvalidateToken(tok)
				}
			}
		}()
	}
	// Concurrent readers and revokers, including the three revocation events.
	for i := 0; i < sessions; i++ {
		session := fmt.Sprintf("session-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perSession; j++ {
				s.LiveCount()
				s.LiveCountForSession(session)
				s.PurgeExpired()
				switch j % 3 {
				case 0:
					s.InvalidatePath("ws1", "docs/doc1.html")
				case 1:
					s.InvalidateWorkspace("ws-absent")
				case 2:
					s.InvalidateSession(session + "-absent")
				}
			}
		}()
	}
	wg.Wait()

	// Whatever the interleaving, no session may end above its cap, and the
	// bookkeeping must agree with itself.
	total := 0
	for i := 0; i < sessions; i++ {
		n := s.LiveCountForSession(fmt.Sprintf("session-%d", i))
		if n > MaxLivePreviewTokensPerSession {
			t.Fatalf("session-%d holds %d live tokens, above the cap of %d", i, n, MaxLivePreviewTokensPerSession)
		}
		total += n
	}
	if got := s.LiveCount(); got != total {
		t.Fatalf("LiveCount() = %d but the per-session counts sum to %d — an index is out of step", got, total)
	}
}
