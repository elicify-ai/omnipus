package gateway

// RED B1 — the staging cancel route tests (spec TDD order 13, 13a, 13b, 13c,
// issue #1967). Spec: docs/internal/specs/export-import-b1-spec.md revision 5
// — the "Cancel lifecycle" table, FR-010, FR-024a, the BDD scenarios "Cancel
// frees the slot", "Cancel refusals", "Folder removal fails", "Folder left
// after a restart is removed by the next upload", "A restart alone removes
// nothing", "Cancel and apply at the same moment", "Two uploads are imported
// one at a time (server)", "Session refresh refusals happen before the import
// starts (server)".
//
// The cancel route is the RED stub (DELETE answers 501), so every cancel
// assertion fails naming the missing feature. Apply, upload and the
// middleware chain are real, so the rows that pin existing behaviour hold in
// RED and are marked as pins. All ordering is by channels — no sleeps.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// b1StageReady uploads a package through the registered upload route and
// returns the ready staging's id.
func b1StageReady(t *testing.T, api *restAPI) string {
	t.Helper()
	prev := stageUpload(t, api, validMainPackage(t))
	require.NotEmpty(t, prev.StagingId)
	return prev.StagingId
}

// b1StagingDir is the staging folder for id under the gateway's staging base.
func b1StagingDir(api *restAPI, id string) string {
	return filepath.Join(api.packageStagingBase(), id)
}

// b1RestartRegistry simulates a gateway restart: a fresh registry over the
// same disk base (the in-memory entries are gone; the folders stay).
func b1RestartRegistry(t *testing.T, api *restAPI) *omnpkg.StagingRegistry {
	t.Helper()
	api.packages.mu.Lock()
	defer api.packages.mu.Unlock()
	api.packages.registry = omnpkg.NewStagingRegistry(api.packageStagingBase(), packageNow)
	return api.packages.registry
}

// b1WriteJournal drops a byte-comparable journal file into a staging folder
// (the lifecycle's persistence point) and returns its bytes.
func b1WriteJournal(t *testing.T, api *restAPI, id string) []byte {
	t.Helper()
	require.NoError(t, os.MkdirAll(b1StagingDir(api, id), 0o755))
	journal := filepath.Join(b1StagingDir(api, id), "journal.json")
	require.NoError(t, os.WriteFile(journal, []byte(`{"state":"started"}`), 0o644))
	b, err := os.ReadFile(journal)
	require.NoError(t, err)
	return b
}

// ---------------------------------------------------------------------------
// Order 13 — TestStagingCancelHandler_ReadyBusyAndGone.
// ---------------------------------------------------------------------------

// TestStagingCancelHandler_ReadyBusyAndGone — spec order 13: the cancel
// lifecycle through the registered route: 204 / 404 / 405 / 409 / 500, the
// journal byte-identical for applying and needs-cleanup, a failed-removal
// cancel never applicable and not a slot, the restart rows, and the freed
// slot's sixth upload.
func TestStagingCancelHandler_ReadyBusyAndGone(t *testing.T) {
	t.Run("cancel frees the slot: 204, folder gone, the next upload succeeds", func(t *testing.T) {
		api := packageAPI(t)
		id := b1StageReady(t, api)
		_, statErr := os.Stat(b1StagingDir(api, id))
		require.NoError(t, statErr, "fixture: the staging folder exists")

		got := callPackageCancel(t, api, id)
		require.Equal(t, http.StatusNoContent, got.Code, "the cancel of a ready staging answers 204; body=%s", got.Body)
		_, statErr = os.Stat(b1StagingDir(api, id))
		require.ErrorIs(t, statErr, os.ErrNotExist, "the staging folder is gone")

		prev := stageUpload(t, api, validMainPackage(t))
		require.NotEmpty(t, prev.StagingId, "the freed slot admits the next upload")
	})

	t.Run("a ready staging past expiry, not yet swept, still cancels (204)", func(t *testing.T) {
		clk := newB1Clock()
		b1UseClock(t, clk)
		api := packageAPI(t)
		id := b1StageReady(t, api)
		clk.Set(clk.Now().Add(31 * time.Minute))
		got := callPackageCancel(t, api, id)
		require.Equal(t, http.StatusNoContent, got.Code, "expiry does not stop a cancel; body=%s", got.Body)
		_, statErr := os.Stat(b1StagingDir(api, id))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})

	t.Run("receiving refuses 409 staging_busy with nothing changed", func(t *testing.T) {
		api := packageAPI(t)
		reg := api.packageRegistry()
		id, err := reg.Reserve() // receiving: the upload is still streaming
		require.NoError(t, err)
		journal := b1WriteJournal(t, api, id)

		got := callPackageCancel(t, api, id)
		requirePackageStatus(t, got, http.StatusConflict, "staging_busy")
		st, err := reg.State(id)
		require.NoError(t, err)
		require.Equal(t, omnpkg.StagingReceiving, st, "the state is unchanged")
		after, err := os.ReadFile(filepath.Join(b1StagingDir(api, id), "journal.json"))
		require.NoError(t, err)
		require.Equal(t, journal, after, "the journal file is byte-identical")
	})

	t.Run("applying refuses 409 staging_busy with the journal byte-identical", func(t *testing.T) {
		api := packageAPI(t)
		id := b1StageReady(t, api)
		journal := b1WriteJournal(t, api, id)
		reg := api.packageRegistry()
		require.NoError(t, reg.BeginApply(id))

		got := callPackageCancel(t, api, id)
		requirePackageStatus(t, got, http.StatusConflict, "staging_busy")
		st, err := reg.State(id)
		require.NoError(t, err)
		require.Equal(t, omnpkg.StagingApplying, st)
		after, err := os.ReadFile(filepath.Join(b1StagingDir(api, id), "journal.json"))
		require.NoError(t, err)
		require.Equal(t, journal, after, "the journal file is byte-identical")
	})

	t.Run("needs-cleanup refuses 409 staging_busy with the journal byte-identical", func(t *testing.T) {
		api := packageAPI(t)
		id := b1StageReady(t, api)
		journal := b1WriteJournal(t, api, id)
		reg := api.packageRegistry()
		require.NoError(t, reg.BeginApply(id))
		require.NoError(t, reg.NeedsCleanup(id))

		got := callPackageCancel(t, api, id)
		requirePackageStatus(t, got, http.StatusConflict, "staging_busy")
		st, err := reg.State(id)
		require.NoError(t, err)
		require.Equal(t, omnpkg.StagingNeedsCleanup, st)
		after, err := os.ReadFile(filepath.Join(b1StagingDir(api, id), "journal.json"))
		require.NoError(t, err)
		require.Equal(t, journal, after, "the journal file is byte-identical at the handler checkpoint, like the unit table")
	})

	t.Run("done and unknown ids answer 404 staging_not_found", func(t *testing.T) {
		api := packageAPI(t)
		id := b1StageReady(t, api)
		b1WriteJournal(t, api, id)
		reg := api.packageRegistry()
		require.NoError(t, reg.BeginApply(id))
		require.NoError(t, reg.FinishApply(id)) // done; folder removed by FinishApply

		got := callPackageCancel(t, api, id)
		requirePackageStatus(t, got, http.StatusNotFound, "staging_not_found")
		gotUnknown := callPackageCancel(t, api, "00000000-0000-4000-8000-000000000009")
		requirePackageStatus(t, gotUnknown, http.StatusNotFound, "staging_not_found")
	})

	t.Run("a wrong method answers 405", func(t *testing.T) {
		api := packageAPI(t)
		id := b1StageReady(t, api)
		req := packageAuthRequest(t, http.MethodPut, "/api/v1/packages/import/"+id, nil)
		req.URL.Path = "/api/v1/packages/import/" + id
		got := serveRegisteredPackage(t, api, req)
		require.Equal(t, http.StatusMethodNotAllowed, got.Code, "PUT is not a cancel; body=%s", got.Body)
	})

	t.Run("a failed folder removal: 500 staging_cleanup_failed, never applicable, not a slot", func(t *testing.T) {
		api := packageAPI(t)
		reg := api.packageRegistry()
		calls := 0
		reg.RemoveDir = func(dir string) error {
			calls++
			if calls == 1 {
				return os.ErrPermission // the fault seam: the first removal fails
			}
			return os.RemoveAll(dir)
		}
		id := b1StageReady(t, api)

		got := callPackageCancel(t, api, id)
		requirePackageStatus(t, got, http.StatusInternalServerError, "staging_cleanup_failed")
		require.Equal(t, 1, calls, "the seam failed exactly the first removal")

		// The removal failed, so the folder is still on disk.
		_, statErr := os.Stat(b1StagingDir(api, id))
		require.NoError(t, statErr)
		// The staging can never be applied again.
		errBegin := reg.BeginApply(id)
		var pkgErr *omnpkg.PackageError
		require.ErrorAs(t, errBegin, &pkgErr)
		require.Equal(t, "staging_not_found", pkgErr.Code, "BeginApply after a failed-removal cancel is refused")

		// The cancelled staging does not count as a slot: five more uploads
		// are admitted while it sits cancelled (the first upload's sweep
		// retries and completes the removal — the unit suite owns that row).
		for i := 0; i < omnpkg.MaxStagedImports; i++ {
			prev := stageUpload(t, api, validMainPackage(t))
			require.NotEmpty(t, prev.StagingId, "the cancelled staging holds no slot (upload %d)", i+1)
		}
	})

	// CHECK W02 row 13: the removal must also be exercised CONTINUING to
	// fail across successive uploads — each upload's sweep retries the
	// removal while the fault lasts, and the first success ends it.
	t.Run("a removal that keeps failing: every upload's sweep retries; the first success ends it", func(t *testing.T) {
		api := packageAPI(t)
		reg := api.packageRegistry()
		calls := 0
		reg.RemoveDir = func(dir string) error {
			calls++
			if calls <= 2 {
				return os.ErrPermission // the cancel's removal and the first upload's sweep retry fail
			}
			return os.RemoveAll(dir)
		}
		id := b1StageReady(t, api)

		got := callPackageCancel(t, api, id)
		requirePackageStatus(t, got, http.StatusInternalServerError, "staging_cleanup_failed")
		require.Equal(t, 1, calls)
		dir := b1StagingDir(api, id)
		_, statErr := os.Stat(dir)
		require.NoError(t, statErr)

		// The first upload: its sweep retries and FAILS — the folder stays,
		// the entry stays cancelled and holds no slot.
		prev := stageUpload(t, api, validMainPackage(t))
		require.NotEmpty(t, prev.StagingId, "the cancelled staging holds no slot while its removal fails")
		require.Equal(t, 2, calls, "the first upload's sweep retried the removal")
		_, statErr = os.Stat(dir)
		require.NoError(t, statErr, "the folder survives the failed sweep retry")
		st, err := reg.State(id)
		require.NoError(t, err)
		require.Equal(t, omnpkg.StagingCancelled, st, "the entry stays cancelled while the removal fails")

		// The second upload: its sweep retries once more and SUCCEEDS.
		prev = stageUpload(t, api, validMainPackage(t))
		require.NotEmpty(t, prev.StagingId)
		require.Equal(t, 3, calls, "the second upload's sweep retried once more")
		_, statErr = os.Stat(dir)
		require.ErrorIs(t, statErr, os.ErrNotExist, "the first successful attempt ends the clean-up")
	})

	t.Run("a restart alone removes nothing; the next upload's sweep does", func(t *testing.T) {
		clk := newB1Clock()
		b1UseClock(t, clk)
		api := packageAPI(t)
		reg := api.packageRegistry()
		calls := 0
		reg.RemoveDir = func(dir string) error {
			calls++
			if calls == 1 {
				return os.ErrPermission
			}
			return os.RemoveAll(dir)
		}
		id := b1StageReady(t, api)
		got := callPackageCancel(t, api, id)
		requirePackageStatus(t, got, http.StatusInternalServerError, "staging_cleanup_failed")
		dir := b1StagingDir(api, id)
		_, statErr := os.Stat(dir)
		require.NoError(t, statErr)

		// Gateway restart: a fresh registry over the same base (the in-memory
		// entry is gone), and the clock moves 31 minutes with no upload.
		fresh := b1RestartRegistry(t, api)
		clk.Set(clk.Now().Add(31 * time.Minute))
		_, statErr = os.Stat(dir)
		require.NoError(t, statErr, "no timer and no start-up clean-up removes the folder in B1")

		// The next upload's sweep runs the orphan scan: the journal-less
		// folder is removed (spec "Folder left after a restart…").
		prev := stageUpload(t, api, validMainPackage(t))
		require.NotEmpty(t, prev.StagingId)
		_, statErr = os.Stat(dir)
		require.ErrorIs(t, statErr, os.ErrNotExist, "the next upload's sweep removes the journal-less folder")
		require.Empty(t, fresh.Sweep(), "a sweep after the folder is gone reports nothing")
	})

	t.Run("a cancelled slot is freed: the sixth upload succeeds", func(t *testing.T) {
		api := packageAPI(t)
		ids := make([]string, 0, omnpkg.MaxStagedImports)
		for i := 0; i < omnpkg.MaxStagedImports; i++ {
			ids = append(ids, b1StageReady(t, api))
		}
		got := callPackageCancel(t, api, ids[0])
		require.Equal(t, http.StatusNoContent, got.Code, "body=%s", got.Body)
		prev := stageUpload(t, api, validMainPackage(t))
		require.NotEmpty(t, prev.StagingId, "the sixth upload succeeds once a slot is freed")
	})
}

// ---------------------------------------------------------------------------
// Order 13a — TestStagingCancelHandler_InterleavedWithApply.
// ---------------------------------------------------------------------------

// TestStagingCancelHandler_InterleavedWithApply — spec order 13a: a test seam
// pauses the apply right after BeginApply (channel handshake, no sleeps); the
// cancel at that checkpoint answers 409 with folder, journal and state
// compared at the pause; after the release the apply's normal outcome holds
// and a later cancel answers 404. Cancel-first and the AbortApply row.
func TestStagingCancelHandler_InterleavedWithApply(t *testing.T) {
	t.Run("cancel at the apply pause is 409; the released apply ends normally; cancel then 404", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }

		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		id := b1StageReady(t, api)
		journal := b1WriteJournal(t, api, id)
		dir := b1StagingDir(api, id)
		reg := api.packageRegistry()

		// The apply pauses right after BeginApply. Release and join are
		// DEFERRED (CHECK W02 13a): every failure path — a fatal assertion, a
		// red cancel, a killed busy-code mutant — unblocks the paused apply
		// and waits for it, so no seam state outlives the subtest.
		paused := make(chan struct{})
		release := make(chan struct{})
		reg.AfterBeginApply = func(string) {
			close(paused)
			<-release
		}
		applyDone := make(chan packageHTTP, 1)
		go func() { applyDone <- callPackageApply(t, api, id, applyBody("new", ws)) }()
		closeRelease := sync.OnceFunc(func() { close(release) })
		joined := false
		t.Cleanup(func() {
			closeRelease()
			if !joined {
				<-applyDone
			}
		})
		<-paused

		// The cancel at that checkpoint: 409, nothing changed.
		got := callPackageCancel(t, api, id)
		requirePackageStatus(t, got, http.StatusConflict, "staging_busy")
		st, err := reg.State(id)
		require.NoError(t, err)
		require.Equal(t, omnpkg.StagingApplying, st, "at the checkpoint the staging is applying")
		_, statErr := os.Stat(dir)
		require.NoError(t, statErr, "at the checkpoint the folder exists")
		after, err := os.ReadFile(filepath.Join(dir, "journal.json"))
		require.NoError(t, err)
		require.Equal(t, journal, after, "at the checkpoint the journal is byte-identical")

		// Release: the apply ends in its normal way (200 + agent).
		closeRelease()
		gotApply := <-applyDone
		joined = true // consumed; the cleanup's join is a no-op now
		require.Equal(t, http.StatusOK, gotApply.Code, "the refused cancel had no effect on the apply; body=%s", gotApply.Body)
		res := decodeResult(t, gotApply.Body)
		require.Len(t, res.Created, 1)
		require.NotEqual(t, specSourceID, res.Created[0].NewId)
		_, statErr = os.Stat(dir)
		require.ErrorIs(t, statErr, os.ErrNotExist, "FinishApply removes the folder, as today")

		// A cancel after the apply is the unknown-staging outcome.
		gotLate := callPackageCancel(t, api, id)
		requirePackageStatus(t, gotLate, http.StatusNotFound, "staging_not_found")
	})

	t.Run("cancel first: 204, the apply then answers staging_not_found, no agent", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		id := b1StageReady(t, api)
		agentsBefore := len(api.agentLoop.GetConfig().Agents.List)

		got := callPackageCancel(t, api, id)
		require.Equal(t, http.StatusNoContent, got.Code, "body=%s", got.Body)

		gotApply := callPackageApply(t, api, id, applyBody("new", ws))
		requirePackageStatus(t, gotApply, http.StatusNotFound, "staging_not_found")
		require.Equal(t, agentsBefore, len(api.agentLoop.GetConfig().Agents.List), "no agent was created")
	})

	t.Run("an apply refused in preparation: 404 workspace_not_found, then the cancel succeeds (204)", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		id := b1StageReady(t, api)
		// A workspace id that does not exist: prepare fails before any write
		// and the staging returns to ready (AbortApply path).
		gotApply := callPackageApply(t, api, id, applyBody("new", "no-such-workspace"))
		requirePackageStatus(t, gotApply, http.StatusNotFound, "workspace_not_found")

		got := callPackageCancel(t, api, id)
		require.Equal(t, http.StatusNoContent, got.Code, "the cancel after a preparation refusal answers 204; body=%s", got.Body)
		_, statErr := os.Stat(b1StagingDir(api, id))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})
}

// ---------------------------------------------------------------------------
// Order 13b — TestPackageApply_TwoStagingsSerialised.
// ---------------------------------------------------------------------------

// TestPackageApply_TwoStagingsSerialised — spec order 13b: two uploads are
// imported one at a time (server): S1's apply paused after BeginApply; S2's
// apply answers 409 import_in_progress and S2 stays ready; after the release
// S1 answers 200 and S2's second apply answers 200. Regression pin for the
// cancel feature: it rides the existing apply serialization.
func TestPackageApply_TwoStagingsSerialised(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }

	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	s1 := b1StageReady(t, api)
	s2 := b1StageReady(t, api)
	reg := api.packageRegistry()

	paused := make(chan struct{})
	release := make(chan struct{})
	var pauseOnce sync.Once
	reg.AfterBeginApply = func(string) {
		// Pause only the FIRST apply (S1); S2's second, legitimate apply
		// later in this test reaches BeginApply too and must not re-pause.
		pauseOnce.Do(func() {
			close(paused)
			<-release
		})
	}
	applyDone := make(chan packageHTTP, 1)
	go func() { applyDone <- callPackageApply(t, api, s1, applyBody("new", ws)) }()
	// Deferred release and join (CHECK W02 13b): a failure below must not
	// strand the paused S1 apply holding the serialization lock.
	closeRelease := sync.OnceFunc(func() { close(release) })
	joined := false
	t.Cleanup(func() {
		closeRelease()
		if !joined {
			<-applyDone
		}
	})
	<-paused

	// S2 while S1 holds the apply lock: 409 import_in_progress, S2 still ready.
	gotS2 := callPackageApply(t, api, s2, applyBody("new", ws))
	requirePackageStatus(t, gotS2, http.StatusConflict, "import_in_progress")
	st2, err := reg.State(s2)
	require.NoError(t, err)
	require.Equal(t, omnpkg.StagingReady, st2, "S2 stays ready")

	closeRelease()
	gotS1 := <-applyDone
	joined = true // consumed; the cleanup's join is a no-op now
	require.Equal(t, http.StatusOK, gotS1.Code, "S1 answers 200 after the release; body=%s", gotS1.Body)
	res1 := decodeResult(t, gotS1.Body)
	require.Len(t, res1.Created, 1)

	// S2's second apply now runs (one at a time).
	gotS2b := callPackageApply(t, api, s2, applyBody("new", ws))
	require.Equal(t, http.StatusOK, gotS2b.Code, "S2's second apply answers 200; body=%s", gotS2b.Body)
	res2 := decodeResult(t, gotS2b.Body)
	require.Len(t, res2.Created, 1)
	require.NotEqual(t, res1.Created[0].NewId, res2.Created[0].NewId, "each apply creates its own agent")
}

// ---------------------------------------------------------------------------
// Order 13c — TestPackageApply_SessionRefusalsBeforeHandler.
// ---------------------------------------------------------------------------

// TestPackageApply_SessionRefusalsBeforeHandler — spec order 13c
// (FR-024a): through the full production middleware chain of the registered
// routes (planted-cookie guard → CSRF → config snapshot → mux, the
// buildProductionMiddlewareChain order), a session refusal happens before the
// handler: a CSRF-mismatch apply answers 403 with the CSRF-failure body and a
// planted reserved-cookie apply answers 403 planted_cookie_cleared — both
// leaving the staging ready and no agent created — and then a valid apply
// answers 200. The same two refusals on the B1 cancel route leave the staging
// ready, and a valid cancel then answers 204.
func TestPackageApply_SessionRefusalsBeforeHandler(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }

	// The CSRF gate exists for browser sessions: a Bearer caller is exempt
	// (middleware/csrf.go). These requests authenticate the way the SPA does
	// — an omnipus-session cookie — so a wired session user is needed. The
	// user also carries the harness bearer as its TokenHash so the upload
	// fixtures keep authenticating.
	plaintext, hash, err := middleware.MintSessionToken()
	require.NoError(t, err)
	cfg := api.agentLoop.GetConfig()
	cfg.Gateway.Users = []config.UserConfig{{
		Username:         "b1-session-user",
		TokenHash:        hashBearer(t, packageTestBearer),
		SessionTokenHash: config.BcryptHash(hash),
	}}

	chain := buildProductionMiddlewareChain(api, packageRouteMux(t, api))
	send := func(t *testing.T, method, path string, body string, hdr map[string]string) packageHTTP {
		t.Helper()
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		req = req.WithContext(packageCtx(t))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		chain.ServeHTTP(w, req)
		return packageHTTP{Code: w.Code, Body: w.Body.Bytes(), Hdr: w.Header()}
	}
	// The genuine session cookie plus the CSRF double-submit pair. A cookie
	// and header mismatch is the CSRF-failure refusal; a match passes.
	session := "omnipus-session=" + plaintext
	csrfMismatch := map[string]string{"Cookie": session + "; csrf=tok-a", "X-CSRF-Token": "tok-b"}
	csrfValid := map[string]string{"Cookie": session + "; csrf=tok-a", "X-CSRF-Token": "tok-a"}
	planted := func(base map[string]string) map[string]string {
		out := map[string]string{"Cookie": "omnipus-session=planted-a; omnipus-session=planted-b"}
		for k, v := range base {
			if k != "Cookie" {
				out[k] = v
			}
		}
		return out
	}

	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	id := b1StageReady(t, api)
	applyPath := "/api/v1/packages/import/" + id + "/apply"
	cancelPath := "/api/v1/packages/import/" + id
	applyJSON := applyBody("new", ws)
	agentsBefore := len(api.agentLoop.GetConfig().Agents.List)
	stillReady := func(when string) {
		t.Helper()
		st, err := api.packageRegistry().State(id)
		require.NoError(t, err)
		require.Equal(t, omnpkg.StagingReady, st, "%s: the staging is still ready after the refusal", when)
		require.Equal(t, agentsBefore, len(api.agentLoop.GetConfig().Agents.List),
			"%s: no agent was created", when)
	}

	// CSRF mismatch on the apply: 403 with the CSRF-failure body.
	gotCSRF := send(t, http.MethodPost, applyPath, applyJSON, csrfMismatch)
	require.Equal(t, http.StatusForbidden, gotCSRF.Code, "body=%s", gotCSRF.Body)
	var csrfBody map[string]any
	require.NoError(t, jsonUnmarshal(gotCSRF.Body, &csrfBody), "body=%s", gotCSRF.Body)
	require.Equal(t, "csrf token mismatch", csrfBody["error"], "the CSRF-failure body")
	stillReady("CSRF-mismatch apply")

	// Planted reserved cookie on the apply: 403 planted_cookie_cleared.
	gotPlanted := send(t, http.MethodPost, applyPath, applyJSON, planted(csrfMismatch))
	erPlanted := requirePackageStatus(t, gotPlanted, http.StatusForbidden, "planted_cookie_cleared")
	require.NotEmpty(t, erPlanted.Error)
	stillReady("planted-cookie apply")

	// The same two refusals on the B1 cancel route.
	gotCancelCSRF := send(t, http.MethodDelete, cancelPath, "", csrfMismatch)
	require.Equal(t, http.StatusForbidden, gotCancelCSRF.Code, "body=%s", gotCancelCSRF.Body)
	var cancelCSRFBody map[string]any
	require.NoError(t, jsonUnmarshal(gotCancelCSRF.Body, &cancelCSRFBody), "body=%s", gotCancelCSRF.Body)
	require.Equal(t, "csrf token mismatch", cancelCSRFBody["error"])
	stillReady("CSRF-mismatch cancel")

	gotCancelPlanted := send(t, http.MethodDelete, cancelPath, "", planted(nil))
	requirePackageStatus(t, gotCancelPlanted, http.StatusForbidden, "planted_cookie_cleared")
	stillReady("planted-cookie cancel")

	// A valid apply through the same chain answers 200.
	gotOK := send(t, http.MethodPost, applyPath, applyJSON, csrfValid)
	require.Equal(t, http.StatusOK, gotOK.Code, "the valid apply answers 200; body=%s", gotOK.Body)
	res := decodeResult(t, gotOK.Body)
	require.Len(t, res.Created, 1)

	// A valid cancel of the NEXT upload through the same chain answers 204
	// (RED: the cancel stub answers 501 — the missing feature).
	next := b1StageReady(t, api)
	gotCancel := send(t, http.MethodDelete, "/api/v1/packages/import/"+next, "", csrfValid)
	require.Equal(t, http.StatusNoContent, gotCancel.Code, "the valid cancel answers 204; body=%s", gotCancel.Body)
}
