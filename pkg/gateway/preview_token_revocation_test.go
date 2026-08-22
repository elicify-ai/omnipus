// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 stage 1 — FR-003d, spec §13 test 92
// (TestPreviewToken_InvalidatedOnLogout: "Mint, log out, use — refused. Also
// mount revoked, and file deleted").
//
// WHY THIS FILE EXISTS SEPARATELY FROM preview_token_test.go. That file drives
// the store's Invalidate* methods directly and proves they work. It cannot
// prove they are ever CALLED — and for a while they were not: the store was
// implemented, unit-tested and never wired, so expiry was the only revocation
// the product had. FR-003d names that exact outcome: "an administrator's token
// stays a valid unauthenticated read grant after they log out — the outcome
// FR-003b forbids, reached by omission."
//
// So every test here drives the REAL event handler — HandleLogout,
// handleWorkspaceMountDelete, handleLibraryEntryDelete, handleLibraryRename,
// handleLibraryTransfer — and then asks the REAL serving handler for the bytes.
// Nothing here touches PreviewTokenStore directly; a wiring that is deleted
// makes these tests fail, which is the whole point.
//
// EVERY CASE CARRIES A POSITIVE CONTROL, in the same run, in this order:
//
//	mint → serve → 200 with the real bytes   (the grant genuinely worked)
//	event → assert the event's own status     (the event genuinely happened)
//	PUT THE BYTES BACK ON DISK                (see below — this is the load-bearing step)
//	serve → 404                               (the grant is genuinely dead)
//
// Without the first step "refused afterwards" is satisfied by a token that
// never worked at all — a mistyped path, a fixture that did not write the file,
// a mint that quietly failed. Without the second, it is satisfied by an event
// handler that 500'd and did nothing.
//
// THE THIRD STEP IS THE ONE THIS FILE GOT WRONG FIRST TIME, and it is worth
// spelling out because the mistake is invisible. For delete, rename, move and
// mount-revoke, the event REMOVES THE BYTES. So "serve → 404" afterwards is
// true whether or not anything was revoked — the file is simply not there. The
// first draft of these tests passed with every revocation call deleted; the
// mutation run is what exposed it. Restoring the file (or re-creating the
// mount) before the final serve makes the two outcomes distinguishable again:
// a LIVE token would now answer 200, so a 404 can only mean the grant was
// revoked. Logout is the one event that needs no restore, because it touches no
// file — which is exactly why it was the only case that failed under mutation.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// revocationFixture is a restAPI that can do all three things one test needs:
// authenticate a real user (so HandleLogout runs its real path rather than
// 500ing on a missing config row), hold a real Library workspace, and serve
// preview tokens from the store the revocation events reach.
type revocationFixture struct {
	api         *restAPI
	routes      *libraryPreviewRoutes
	workspaceID string
	workDir     string
	username    string
	sessionVal  string
}

const revocationSessionCookie = "session-value-for-revocation-fixture"

func newRevocationFixture(t *testing.T) *revocationFixture {
	t.Helper()
	const username = "revokeuser"
	api, _ := newTestRestAPIWithUser(t, username, "RevokePass1")

	wsID := seedLibraryWorkspace(t, api, "Revocation WS")
	work := workDir(api, wsID)
	require.NoError(t, os.MkdirAll(filepath.Join(work, "site"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(work, "site", "index.html"),
		[]byte("<!doctype html><p>revocation fixture</p>"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(work, "loose.txt"), []byte("a loose file"), 0o600))

	return &revocationFixture{
		api:         api,
		routes:      newLibraryPreviewRoutes(api),
		workspaceID: wsID,
		workDir:     work,
		username:    username,
		sessionVal:  revocationSessionCookie,
	}
}

// mint drives the real mint handler as the fixture's session.
func (f *revocationFixture) mint(t *testing.T, scope gen.LibraryPreviewTokenRequestScope, relPath string) gen.LibraryPreviewTokenResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"workspace_id": f.workspaceID,
		"path":         relPath,
		"scope":        string(scope),
	})
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPost, libraryPreviewMintPath, strings.NewReader(string(body)))
	r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: f.sessionVal})
	rec := httptest.NewRecorder()
	f.routes.handleMintPreviewToken(rec, r)
	require.Equal(t, http.StatusCreated, rec.Code, "mint failed: %s", rec.Body.String())

	var resp gen.LibraryPreviewTokenResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Token)
	return resp
}

// serve drives the real serving handler.
func (f *revocationFixture) serve(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.routes.handleServeLibraryPreview(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// requireGrantWorks is the positive control every case runs FIRST.
func (f *revocationFixture) requireGrantWorks(t *testing.T, url, wantBodySubstring string) {
	t.Helper()
	rec := f.serve(t, url)
	require.Equal(t, http.StatusOK, rec.Code,
		"positive control: the token must serve the file BEFORE the revocation event, "+
			"or \"refused afterwards\" proves nothing — a token that never worked also fails after")
	require.Contains(t, rec.Body.String(), wantBodySubstring,
		"positive control: the response must be the real file's bytes")
}

// requireGrantDead is the assertion FR-003d is about.
func (f *revocationFixture) requireGrantDead(t *testing.T, url, forbidden string) {
	t.Helper()
	rec := f.serve(t, url)
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"FR-003d: the token must be refused after the revocation event. Expiry alone is not "+
			"revocation — an unrevoked token is a live UNAUTHENTICATED read grant for up to 15 minutes")
	assert.NotContains(t, rec.Body.String(), forbidden,
		"FR-003d: the refused response must not still contain the granted file's bytes")
	assert.Equal(t, libraryIsolationPolicy, rec.Header().Get("Content-Security-Policy"),
		"FR-003n/§10.3: the refusal is still a response on the token path and carries the policy")
}

// TestPreviewToken_InvalidatedOnLogout is spec test 92.
//
// FR-003d: a token "MUST additionally be invalidated when the minting session
// logs out, when the workspace mount is revoked, and when the file or bundle
// root is deleted or moved."
func TestPreviewToken_InvalidatedOnLogout(t *testing.T) {
	f := newRevocationFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")
	f.requireGrantWorks(t, minted.Url, "revocation fixture")

	// The real logout handler, with the SAME session cookie the mint carried —
	// which is what makes the session key match. A logout that had to be told
	// which key to revoke would be testing the test.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: f.sessionVal})
	req = injectUser(req, f.username)
	rec := httptest.NewRecorder()
	f.api.HandleLogout(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code,
		"pre-condition: the logout must actually succeed, or nothing was revoked for a second reason")

	f.requireGrantDead(t, minted.Url, "revocation fixture")
}

// TestPreviewToken_InvalidatedOnMountRevoke drives DELETE
// /workspaces/{id}/mounts/{name} — FR-003d's second event.
func TestPreviewToken_InvalidatedOnMountRevoke(t *testing.T) {
	f := newRevocationFixture(t)

	// A real mount, created the way the mount tests create one.
	target := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(target, "notes.txt"), []byte("mounted bytes"), 0o600))
	_, _, err := workspace.CreateMount(f.api.homePath, f.workspaceID, "client-repo", target)
	require.NoError(t, err)

	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeFile, "client-repo/notes.txt")
	f.requireGrantWorks(t, minted.Url, "mounted bytes")

	rec := httptest.NewRecorder()
	f.api.handleWorkspaceMountDelete(rec,
		httptest.NewRequest(http.MethodDelete,
			"/api/v1/workspaces/"+f.workspaceID+"/mounts/client-repo", nil),
		f.workspaceID, "client-repo")
	require.Equal(t, http.StatusNoContent, rec.Code,
		"pre-condition: the mount delete must actually succeed")

	// Re-attach the SAME folder under the SAME name, so the bytes the token
	// names are readable again. Without this the 404 below is produced by the
	// missing mount, not by the revocation, and passes with the wiring deleted.
	_, _, err = workspace.CreateMount(f.api.homePath, f.workspaceID, "client-repo", target)
	require.NoError(t, err, "pre-condition: the folder must be reachable again, "+
		"or the assertion below cannot tell a revoked token from an unreachable file")

	f.requireGrantDead(t, minted.Url, "mounted bytes")
}

// TestPreviewToken_InvalidatedOnDeleteAndMove covers FR-003d's third event in
// all four shapes the Library offers: delete, rename, move and copy.
//
// The rename and copy cases assert the half that is easy to miss — the
// DESTINATION. A token minted over a path that is then overwritten by different
// bytes would otherwise keep serving content its holder was never granted.
func TestPreviewToken_InvalidatedOnDeleteAndMove(t *testing.T) {
	t.Run("delete_of_the_granted_file", func(t *testing.T) {
		f := newRevocationFixture(t)
		minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeFile, "loose.txt")
		f.requireGrantWorks(t, minted.Url, "a loose file")

		rec := libDelete(t, f.api,
			"/api/v1/library/"+f.workspaceID+"/entries?path=loose.txt")
		require.Equal(t, http.StatusNoContent, rec.Code,
			"pre-condition: the delete must actually succeed; body: %s", rec.Body.String())

		// Put the bytes back. A deleted file 404s on its own; only a restored
		// one can tell a revoked grant from a missing file.
		require.NoError(t, os.WriteFile(
			filepath.Join(f.workDir, "loose.txt"), []byte("a loose file"), 0o600))

		f.requireGrantDead(t, minted.Url, "a loose file")
	})

	t.Run("delete_of_a_parent_directory_kills_a_bundle_token_beneath_it", func(t *testing.T) {
		// FR-003d's beneath-it half. Nobody named "site" when minting a token
		// over "site" — but deleting "site" is exactly what makes it dead.
		f := newRevocationFixture(t)
		minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")
		f.requireGrantWorks(t, minted.Url, "revocation fixture")

		rec := libDelete(t, f.api,
			"/api/v1/library/"+f.workspaceID+"/entries?path=site")
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

		require.NoError(t, os.MkdirAll(filepath.Join(f.workDir, "site"), 0o700))
		require.NoError(t, os.WriteFile(
			filepath.Join(f.workDir, "site", "index.html"),
			[]byte("<!doctype html><p>revocation fixture</p>"), 0o600))

		f.requireGrantDead(t, minted.Url, "revocation fixture")
	})

	t.Run("rename_kills_the_source_token", func(t *testing.T) {
		f := newRevocationFixture(t)
		minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeFile, "loose.txt")
		f.requireGrantWorks(t, minted.Url, "a loose file")

		rec := libPostJSON(t, f.api,
			"/api/v1/library/"+f.workspaceID+"/rename",
			`{"from":"loose.txt","to":"renamed.txt"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		require.NoError(t, os.WriteFile(
			filepath.Join(f.workDir, "loose.txt"), []byte("a loose file"), 0o600))

		f.requireGrantDead(t, minted.Url, "a loose file")
	})

	t.Run("move_kills_the_source_token", func(t *testing.T) {
		f := newRevocationFixture(t)
		source := f.mint(t, gen.LibraryPreviewTokenRequestScopeFile, "loose.txt")
		f.requireGrantWorks(t, source.Url, "a loose file")

		rec := libPostJSON(t, f.api, "/api/v1/library/move",
			`{"from_workspace_id":"`+f.workspaceID+`","from_path":"loose.txt",`+
				`"to_workspace_id":"`+f.workspaceID+`","to_path":"moved.txt"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		require.NoError(t, os.WriteFile(
			filepath.Join(f.workDir, "loose.txt"), []byte("a loose file"), 0o600))

		f.requireGrantDead(t, source.Url, "a loose file")
	})

	t.Run("copy_leaves_the_source_token_alive", func(t *testing.T) {
		// The negative half, and it is a real requirement rather than an
		// omission. FR-003d's third event is "deleted or moved". A copy destroys
		// nothing and moves nothing, so revoking on it would break a preview the
		// operator is reading for no reason they could observe — the kind of
		// over-revocation a "revoke on any library mutation" shortcut produces.
		f := newRevocationFixture(t)
		source := f.mint(t, gen.LibraryPreviewTokenRequestScopeFile, "loose.txt")
		f.requireGrantWorks(t, source.Url, "a loose file")

		rec := libPostJSON(t, f.api, "/api/v1/library/copy",
			`{"from_workspace_id":"`+f.workspaceID+`","from_path":"loose.txt",`+
				`"to_workspace_id":"`+f.workspaceID+`","to_path":"copied.txt"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

		still := f.serve(t, source.Url)
		assert.Equal(t, http.StatusOK, still.Code,
			"FR-003d names deletion and move. A copy is neither, so the source grant survives")
	})

	t.Run("an_existing_destination_is_refused_so_no_destination_grant_can_exist", func(t *testing.T) {
		// The premise the two handlers' comments rest on, asserted rather than
		// assumed. Both revoke the SOURCE only, on the grounds that a token over
		// the destination is unreachable: minting requires the path to exist, and
		// a rename or move onto an existing path is refused. If that refusal ever
		// relaxes, this fails and the revocation has to grow a second call.
		f := newRevocationFixture(t)
		require.NoError(t, os.WriteFile(
			filepath.Join(f.workDir, "occupied.txt"), []byte("occupied bytes"), 0o600))

		rename := libPostJSON(t, f.api,
			"/api/v1/library/"+f.workspaceID+"/rename",
			`{"from":"loose.txt","to":"occupied.txt"}`)
		assert.Equal(t, http.StatusConflict, rename.Code,
			"a rename onto an existing entry must be refused; body: %s", rename.Body.String())

		move := libPostJSON(t, f.api, "/api/v1/library/move",
			`{"from_workspace_id":"`+f.workspaceID+`","from_path":"loose.txt",`+
				`"to_workspace_id":"`+f.workspaceID+`","to_path":"occupied.txt"}`)
		assert.Equal(t, http.StatusConflict, move.Code,
			"a move onto an existing entry must be refused; body: %s", move.Body.String())
	})
}

// TestPreviewToken_LogoutRevokesOnlyTheLoggedOutSession pins the boundary the
// simple implementation gets wrong in the OTHER direction.
//
// SEC-1 established that logging out of one browser must not end a colleague's
// session on another device. A revocation that walked the whole store instead
// of the logging-out session's own bucket would satisfy FR-003d and break that
// — a preview open on a second machine would go dead for no visible reason.
func TestPreviewToken_LogoutRevokesOnlyTheLoggedOutSession(t *testing.T) {
	f := newRevocationFixture(t)

	mine := f.mint(t, gen.LibraryPreviewTokenRequestScopeFile, "loose.txt")

	// A second session, same user, different device.
	otherSession := "a-second-device-session-value"
	body, err := json.Marshal(map[string]any{
		"workspace_id": f.workspaceID, "path": "loose.txt", "scope": "file",
	})
	require.NoError(t, err)
	otherReq := httptest.NewRequest(http.MethodPost, libraryPreviewMintPath, strings.NewReader(string(body)))
	otherReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: otherSession})
	otherRec := httptest.NewRecorder()
	f.routes.handleMintPreviewToken(otherRec, otherReq)
	require.Equal(t, http.StatusCreated, otherRec.Code, otherRec.Body.String())
	var theirs gen.LibraryPreviewTokenResponse
	require.NoError(t, json.Unmarshal(otherRec.Body.Bytes(), &theirs))

	require.NotEqual(t, mine.Token, theirs.Token,
		"pre-condition: two sessions over the same path must hold two distinct tokens, "+
			"or this test cannot tell them apart")
	f.requireGrantWorks(t, mine.Url, "a loose file")
	f.requireGrantWorks(t, theirs.Url, "a loose file")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: f.sessionVal})
	req = injectUser(req, f.username)
	rec := httptest.NewRecorder()
	f.api.HandleLogout(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	f.requireGrantDead(t, mine.Url, "a loose file")

	other := f.serve(t, theirs.Url)
	assert.Equal(t, http.StatusOK, other.Code,
		"SEC-1: logging out of one session must not kill another session's live preview")
}
