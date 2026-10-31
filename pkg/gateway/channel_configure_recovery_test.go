package gateway

// T-02b / FR-050 / S-88 a–g: retry STUCK lifecycle state on an identical
// token-free save. Every recovery action uses {} so no changed public field
// can hide a missing predicate leg. Real reload/manager/stores/LINE HTTP run;
// only the adapter Stop boundary is faulted for case c. Case g's additional
// during-capture requirement is explicit, not equated with an after-capture probe.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

func t02bConfigure(t *testing.T, f *wu6Fixture, id, body string) (int, map[string]any) {
	t.Helper()
	rec := f.do(http.MethodPut, "/api/v1/channels/"+id+"/configure", body)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "Configure response must be JSON: %s", rec.Body.String())
	return rec.Code, out
}

func t02bRequireOutcome(t *testing.T, body map[string]any, want, where string) {
	t.Helper()
	require.Contains(t, body, "apply_outcome", "%s: FR-052 requires apply_outcome; absent in %v", where, body)
	require.Equal(t, want, body["apply_outcome"], "%s", where)
}

func TestT02b_CaseA_RestartFailedThenCauseRemoved_IdenticalReSaveRetries(t *testing.T) {
	f := t02Fixture(t)
	status, body := t02bConfigure(t, f, "line.healthy", `{"channel_access_token":"`+issue1277UnauthorizedAccess+`"}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied_not_running", "S-88 a, first save")
	ref := f.al.GetConfig().Channels["line.healthy"].ChannelAccessTokenRef
	require.NotEmpty(t, ref)
	require.NoError(t, f.store.Set(ref, issue1277HealthyAccess))
	status, body = t02bConfigure(t, f, "line.healthy", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied", "S-88 a, identical token-free re-save")
}

func TestT02b_CaseD_UnappliedSameReferenceCredential_IdenticalReSaveDeliversB(t *testing.T) {
	f := t02Fixture(t)
	status, first := t02bConfigure(t, f, "line.healthy", `{"channel_access_token":"`+issue1277HealthyAccess+`"}`)
	require.Equal(t, http.StatusOK, status, first)
	t02bRequireOutcome(t, first, "applied", "S-88 d, establish applied A under Configure's selected key")
	ref := f.al.GetConfig().Channels["line.healthy"].ChannelAccessTokenRef
	require.Equal(t, "channel_line.healthy_channel_access_token", ref, "instrument: the later B store replaces the selected reference")

	alt := credentials.NewStore(filepath.Join(t.TempDir(), "alt-store-case-d"))
	require.NoError(t, credentials.Unlock(alt))
	t.Cleanup(alt.Close)
	previous := f.api.credStore
	f.api.credStore = alt
	t.Cleanup(func() { f.api.credStore = previous })
	dir := filepath.Dir(f.f1277.configPath)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(dir, info.Mode().Perm())) })
	rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/configure", `{"channel_access_token":"`+issue1277ConnectAccess+`"}`)
	require.NoError(t, os.Chmod(dir, info.Mode().Perm()))
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	requireWireCode(t, t07ErrorBody(t, rec), "credential_saved_not_saved", "S-88 d, B's configuration write failed")
	// Both stores are real. Mirror the accepted B into the runtime store so
	// the next rebuild consumes the same selected-reference value as production.
	require.NoError(t, f.store.Set(ref, issue1277ConnectAccess))
	f.api.credStore = previous
	before := f.f1277.connectProbes.Load()
	status, body := t02bConfigure(t, f, "line.healthy", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied", "S-88 d: an uncleared earlier write forces identical re-save to apply B")
	require.Greater(t, f.f1277.connectProbes.Load(), before, "S-88 d: actual rebuilt adapter authenticates with B")
}

func TestT02b_CaseE_ConstructionFailureCausePresent_ReportsTrueOutcome(t *testing.T) {
	// Boot starts the supported #1277 state: line never constructs because
	// its referenced channel secret is absent; the healthy peer is running.
	f := t02BootFixture(t, func(*issue1277Fixture) {})
	_, installed := f.svc.ChannelManager.GetChannel("line")
	require.False(t, installed, "instrument: no installed adapter, not a stopped healthy adapter")
	status, body := t02bConfigure(t, f, "line", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied_not_running", "S-88 e, unchanged save while missing credential remains")
	require.Contains(t, body["apply_reason"], "line", "T-6: reason carries the failed-channel entry")
}

func TestT02b_CaseF_ConstructionCauseRemoved_IdenticalSaveConstructsAndStarts(t *testing.T) {
	f := t02BootFixture(t, func(*issue1277Fixture) {})
	_, installed := f.svc.ChannelManager.GetChannel("line")
	require.False(t, installed, "instrument: the adapter never constructed at boot")
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-restored-secret"))
	status, body := t02bConfigure(t, f, "line", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied", "S-88 f: restoring the credential alone allows unchanged save to construct")
	ch, exists := f.svc.ChannelManager.GetChannel("line")
	require.True(t, exists)
	require.True(t, ch.IsRunning(), "case f: a real new adapter is installed and started")
}

func TestT02b_PositiveControl_CurrentAdapterNeverRestarted(t *testing.T) {
	f := t02Fixture(t)
	status, first := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, first)
	t02bRequireOutcome(t, first, "applied", "current control, first save")
	probes := f.f1277.authorizedProbes.Load()
	status, second := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, second)
	t02bRequireOutcome(t, second, "no_reload_needed", "S-88 current control, identical second save")
	require.Equal(t, probes, f.f1277.authorizedProbes.Load(), "S-88: zero restarts of a current adapter")
}

func TestT02b_PositiveControl_HealthyPeerUnaffectedByDegradedInstance(t *testing.T) {
	f := t02BootFixture(t, func(*issue1277Fixture) {})
	peer, exists := f.svc.ChannelManager.GetChannel("line.healthy")
	require.True(t, exists)
	require.True(t, peer.IsRunning())
	probes := f.f1277.authorizedProbes.Load()
	status, body := t02bConfigure(t, f, "line.healthy", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "no_reload_needed", "S-88 degraded peer must not force this current connection to restart")
	after, exists := f.svc.ChannelManager.GetChannel("line.healthy")
	require.True(t, exists)
	require.Same(t, peer, after)
	require.Equal(t, probes, f.f1277.authorizedProbes.Load())
}

func TestT02b_CaseB_BootTimeStartFailure_NeedsFixtureSeam(t *testing.T) {
	f := t02BootFixture(t, func(base *issue1277Fixture) {
		require.NoError(t, base.store.Set(issue1277BrokenRef, "qa1277-restored-peer-secret"))
		require.NoError(t, base.store.Set("QA1277_HEALTHY_LINE_ACCESS", issue1277UnauthorizedAccess))
	})
	startErr := f.svc.ChannelManager.LastStartError("line.healthy")
	require.ErrorContains(t, startErr, "401", "instrument: failure was recorded by BOOT StartAll")
	peer, exists := f.svc.ChannelManager.GetChannel("line")
	require.True(t, exists)
	require.True(t, peer.IsRunning(), "the healthy peer keeps the gateway up")
	ref := f.al.GetConfig().Channels["line.healthy"].ChannelAccessTokenRef
	require.NoError(t, f.store.Set(ref, issue1277HealthyAccess))
	status, body := t02bConfigure(t, f, "line.healthy", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied", "S-88 b, identical save retries recorded boot failure")
	require.NoError(t, f.svc.ChannelManager.LastStartError("line.healthy"))
}

func TestT02b_CaseC_UnfinishedTeardown_NeedsSeam(t *testing.T) {
	f := t02Fixture(t)
	fault := t02FaultStop(t, f, "line.healthy")
	// Force disposal WITHOUT changing config: the retained applied hash is
	// still equal, so the later identical save must use the disposal leg.
	f.svc.ChannelManager.RequestRestart("line.healthy", "", f.al.GetConfig())
	_, err := f.api.triggerReloadAndWaitOutcome()
	require.Error(t, err, "instrument: actual reload records the Stop failure")
	require.Greater(t, fault.calls.Load(), int32(0))
	fault.fail.Store(false)
	status, body := t02bConfigure(t, f, "line.healthy", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied", "S-88 c, identical save retries unfinished disposal")
	require.Greater(t, fault.calls.Load(), int32(1), "the exact retained adapter was disposed again")
}

func TestT02b_CaseG_StaleBundleInterleaving_NeedsCapturePauseSeam(t *testing.T) {
	f := t02Fixture(t)
	mgr := f.svc.ChannelManager
	reporter, ok := any(mgr).(t02PendingReporter)
	if !ok {
		t.Fatal("BLOCKED: Manager.PendingApplication is not implemented on this pre-change base — required by S-88 g / FR-050 leg 5")
	}
	status, first := t02bConfigure(t, f, "line.healthy", `{"channel_access_token":"`+issue1277HealthyAccess+`"}`)
	require.Equal(t, http.StatusOK, status, first)
	t02bRequireOutcome(t, first, "applied", "S-88 g: first apply A under Configure's selected reference")
	ref := f.al.GetConfig().Channels["line.healthy"].ChannelAccessTokenRef
	require.Equal(t, "channel_line.healthy_channel_access_token", ref)
	_, pending := reporter.PendingApplication("line.healthy")
	require.False(t, pending, "instrument: the completed A rebuild acknowledged its own write")

	original, exists := mgr.GetChannel("line.healthy")
	require.True(t, exists)
	pause := &t02PauseStop{Channel: original, entered: make(chan struct{}), release: make(chan struct{})}
	mgr.RegisterChannel("line.healthy", pause)
	mgr.RequestRestart("line.healthy", "", f.al.GetConfig())
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(pause.release) }) }
	var reloadErr error
	reloadFinished := make(chan struct{})
	writeFinished := make(chan struct{})
	writeStarted := false
	var rec *httptest.ResponseRecorder
	var alt *credentials.Store
	previousStore := f.api.credStore
	var restorePermissions func() error
	t.Cleanup(func() {
		release()
		<-reloadFinished
		if writeStarted {
			<-writeFinished
		}
		// Join first, then restore/close dependencies the request could use.
		f.api.credStore = previousStore
		if restorePermissions != nil {
			require.NoError(t, restorePermissions())
		}
		if alt != nil {
			alt.Close()
		}
	})
	beforeA, beforeB := f.f1277.authorizedProbes.Load(), f.f1277.connectProbes.Load()
	go func() {
		_, reloadErr = f.api.triggerReloadAndWaitOutcome()
		close(reloadFinished)
	}()
	select {
	case <-pause.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("instrument: the requested rebuild never reached old-adapter disposal after capture")
	}

	alt = credentials.NewStore(filepath.Join(t.TempDir(), "alt-store-case-g"))
	require.NoError(t, credentials.Unlock(alt))
	f.api.credStore = alt
	dir := filepath.Dir(f.f1277.configPath)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	restorePermissions = func() error { return os.Chmod(dir, info.Mode().Perm()) }
	require.NoError(t, os.Chmod(dir, 0o500))
	writeStarted = true
	go func() {
		rec = f.do(http.MethodPut, "/api/v1/channels/line.healthy/configure", `{"channel_access_token":"`+issue1277ConnectAccess+`"}`)
		close(writeFinished)
	}()
	versionB := t02WaitPending(t, reporter, "line.healthy")
	// B's marker is observable BEFORE the A rebuild can finish: no race
	// between the store callback returning and the later marker advancement.
	release()
	<-reloadFinished
	<-writeFinished
	require.NoError(t, os.Chmod(dir, info.Mode().Perm()))
	f.api.credStore = previousStore
	require.NoError(t, reloadErr)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	requireWireCode(t, t07ErrorBody(t, rec), "credential_saved_not_saved", "S-88 g: B stored, configuration write failed")
	storedB, err := alt.Get(ref)
	require.NoError(t, err)
	require.Equal(t, issue1277ConnectAccess, storedB)
	require.Greater(t, f.f1277.authorizedProbes.Load(), beforeA, "the older rebuild really consumed A")
	require.Equal(t, beforeB, f.f1277.connectProbes.Load(), "the older rebuild did not consume B")
	retainedVersion, pending := reporter.PendingApplication("line.healthy")
	require.True(t, pending, "S-88 g: an A-based rebuild must not acknowledge B")
	require.Equal(t, versionB, retainedVersion, "the exact later B marker survives")

	// Split-vault write-failure staging: copy the actual accepted B value into
	// the real runtime vault before the token-free recovery save.
	require.NoError(t, f.store.Set(ref, storedB))
	status, body := t02bConfigure(t, f, "line.healthy", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied", "S-88 g: unchanged Save applies the unacknowledged B")
	require.Greater(t, f.f1277.connectProbes.Load(), beforeB, "the recovery rebuild actually receives B")
	_, pending = reporter.PendingApplication("line.healthy")
	require.False(t, pending, "a rebuild that consumed B may now acknowledge it")
	status, body = t02bConfigure(t, f, "line.healthy", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "no_reload_needed", "S-88 g: the adapter is current after applying B")
}

func TestT02b_DuringCaptureInterleaving_RequiresAffectedReferenceReadPause(t *testing.T) {
	t.Fatal("BLOCKED: a pause after the affected credential reference read but before remaining bundle reads finish is not implemented — required by R8-MIN-001 / FR-050 leg 5. S-88 g's adapter-Stop barrier and the implementation's rebuild hook are after-capture only")
}
