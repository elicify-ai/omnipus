package gateway

// T-02b — Two-save recovery over the stuck states (channels-spec-r8, T-02b;
// S-88 cases a–g; FR-050; US-6 AS7; SC-026; grill addendum R8-MIN-001).
//
// Oracles (S-88 table + FR-050's five-leg applied/current predicate):
// an enabled connection stuck in any of the five ways retries application on
// the next save — even a byte-identical one — and reports the true outcome;
// an already-current adapter is never restarted (positive controls).
//
// Cases implemented against real components (the wu6/issue1277 real stack):
//   a  reload-time start failure (the fixture's controlled 401 credential),
//      cause removed by fixing the stored credential, identical re-save → applied
//   d  same-reference credential replacement whose application failed
//      (credential_saved_not_saved via a second real store + a read-only
//      config dir), then an identical save WITHOUT re-entering the token →
//      the pending-application marker leg must force a restart that delivers
//      B (the fixture's connect-credential probe counts it)
//   e  the instance's referenced credential is missing when the rebuild runs
//      (the #1277 degradation shape: the rebuild fails to construct,
//      applied_not_running with the failed-channel reason) — never
//      no_reload_needed while the instance has no installed adapter
//   f  the same construction failure with the cause removed (the credential
//      restored in the store, no public configuration change) → the identical
//      save constructs and starts the adapter → applied (instrument: the
//      manager holds the channel and it is running)
//   positive controls: an identical save on an already-current adapter →
//   no_reload_needed with 0 restarts (authorizedProbes unchanged); the
//   healthy peer of a degraded instance saves identically → no_reload_needed
//   while the degraded instance's records are not consulted for the peer.
//
// Case staging notes (documented deviations, each keeping the predicate leg
// the spec's case exercises):
//   - Case e/f stage the missing-reference degradation after boot by deleting
//     the healthy instance's access reference from the store (the boot-time
//     form is the issue1277 fixture's own scenario and must keep passing).
//   - Case d mirrors B into the main store under the selected reference after
//     the failed save: the alt store only exists to let the credential store
//     succeed while the config write fails (both live in one directory); the
//     documented case-d state at the re-save is "B stored under the selected
//     reference, last application failed", which is what the test stages.
//
// RED expectation: configureChannel today has no restart logic and no
// apply_outcome field, so every outcome assertion fails on the missing field.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

func t02bConfigure(t *testing.T, f *wu6Fixture, id, body string) (int, map[string]any) {
	t.Helper()
	rec := f.do(http.MethodPut, "/api/v1/channels/"+id+"/configure", body)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out),
		"configure response must be JSON: %s", rec.Body.String())
	return rec.Code, out
}

func t02bRequireOutcome(t *testing.T, body map[string]any, want, where string) {
	t.Helper()
	require.Contains(t, body, "apply_outcome",
		"%s: the response must carry apply_outcome (FR-052); field absent: %v", where, body)
	require.Equal(t, want, body["apply_outcome"], "%s", where)
}

func TestT02b_CaseA_RestartFailedThenCauseRemoved_IdenticalReSaveRetries(t *testing.T) {
	// S-88 case a: a save whose restart failed transiently (applied_not_running),
	// the cause then removed (the stored credential fixed in place — no public
	// configuration change), then the identical re-save → applied.
	f, _, _ := orderingWriterFixture(t)

	// First save: the rebuild starts with the fixture's controlled 401
	// credential → Start fails → applied_not_running.
	status, body := t02bConfigure(t, f, "line.healthy",
		`{"channel_access_token":"`+issue1277UnauthorizedAccess+`"}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied_not_running", "S-88 case a, first save")

	// Cause removed: the stored credential is fixed under the same reference
	// the saved configuration selects.
	ref := f.al.GetConfig().Channels["line.healthy"].ChannelAccessTokenRef
	require.NotEmpty(t, ref, "instrument: the saved instance must select an access reference")
	require.NoError(t, f.store.Set(ref, issue1277HealthyAccess))

	// The identical re-save (no credential re-entered) must retry the
	// application — never answer no_reload_needed while the connection is down.
	status, body = t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied", "S-88 case a, identical re-save")
}

func TestT02b_CaseD_UnappliedSameReferenceCredential_IdenticalReSaveDeliversB(t *testing.T) {
	// S-88 case d: token B stored under the selected reference, the
	// application failed (credential_saved_not_saved), then the operator
	// re-saves the displayed settings without re-entering the token → the
	// pending-application marker leg forces a restart that delivers B.
	f, _, _ := orderingWriterFixture(t)

	// Stage the failed application: the credential store succeeds (a second
	// real unlocked store outside the config dir) while the config write
	// cannot land (read-only config dir) — the T-07 shape-1 injection.
	alt := credentials.NewStore(filepath.Join(f.f1277.home, "alt-store-case-d"))
	require.NoError(t, credentials.Unlock(alt))
	previous := f.api.credStore
	f.api.credStore = alt
	t.Cleanup(func() { f.api.credStore = previous })

	dir := filepath.Dir(f.f1277.configPath)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))
	rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/configure",
		`{"channel_access_token":"`+issue1277ConnectAccess+`"}`)
	require.NoError(t, os.Chmod(dir, info.Mode().Perm()))
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	// Stage the documented case-d state: B stored under the reference the
	// saved configuration selects (the failed save leaves the config
	// unchanged, still selecting the same reference).
	ref := f.al.GetConfig().Channels["line.healthy"].ChannelAccessTokenRef
	require.NotEmpty(t, ref)
	require.NoError(t, f.store.Set(ref, issue1277ConnectAccess))

	// The identical re-save, no token re-entered (the form omits untouched
	// credentials): must restart and deliver B — never no_reload_needed.
	before := f.f1277.connectProbes.Load()
	status, body := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "apply_outcome",
		"S-88 case d: the response must carry apply_outcome (FR-052); field absent")
	require.NotEqual(t, "no_reload_needed", body["apply_outcome"],
		"S-88 case d: the marker leg must force a restart on the token-free identical save")
	require.Greater(t, f.f1277.connectProbes.Load(), before,
		"S-88 case d: the rebuilt adapter must come up with B (the fixture probe carries it)")
}

func TestT02b_CaseE_ConstructionFailureCausePresent_ReportsTrueOutcome(t *testing.T) {
	// S-88 case e: the referenced credential is missing when the rebuild runs
	// (the #1277 degradation shape); the identical save without a token must
	// attempt application and report the actual outcome — never
	// no_reload_needed while the instance has no installed adapter.
	f, _, _ := orderingWriterFixture(t)
	ref := f.al.GetConfig().Channels["line.healthy"].ChannelAccessTokenRef
	require.NoError(t, f.store.Delete(ref), "instrument: drop the reference the instance selects")

	status, body := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied_not_running", "S-88 case e")
	require.Contains(t, body, "apply_reason",
		"S-88 case e: applied_not_running carries the construction failure reason")
	require.Contains(t, body["apply_reason"], "line.healthy",
		"S-88 case e: the reason carries the failed-channel entry")
}

func TestT02b_CaseF_ConstructionCauseRemoved_IdenticalSaveConstructsAndStarts(t *testing.T) {
	// S-88 case f: the same construction failure with the cause then removed
	// — the credential restored in the store, no public configuration change —
	// then the identical re-save constructs and starts the adapter → applied.
	f, _, _ := orderingWriterFixture(t)
	ref := f.al.GetConfig().Channels["line.healthy"].ChannelAccessTokenRef
	require.NoError(t, f.store.Delete(ref))
	_, body := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	t02bRequireOutcome(t, body, "applied_not_running", "S-88 case f setup (cause present)")

	// Cause removed: the reference resolves again; no config edit, no token.
	require.NoError(t, f.store.Set(ref, issue1277HealthyAccess))
	status, body := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, body)
	t02bRequireOutcome(t, body, "applied", "S-88 case f, identical re-save")

	// Instrument: the adapter is installed and running now.
	ch, exists := f.svc.ChannelManager.GetChannel("line.healthy")
	require.True(t, exists, "case f: the adapter must be constructed and registered")
	require.True(t, ch.IsRunning(), "case f: the adapter must be running")
}

func TestT02b_PositiveControl_CurrentAdapterNeverRestarted(t *testing.T) {
	// S-88 positive control (healthy current): an identical second save on an
	// already-current adapter returns no_reload_needed with 0 restarts —
	// observed through the fixture's accepted-credential probe counter (a
	// rebuild of the healthy instance would re-probe with the healthy
	// access). The predicate reads lifecycle records, not connectivity (m-06).
	f, _, _ := orderingWriterFixture(t)

	status, first := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, first)
	t02bRequireOutcome(t, first, "applied", "positive control, first save")

	probes := f.f1277.authorizedProbes.Load()
	status, second := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, second)
	t02bRequireOutcome(t, second, "no_reload_needed", "positive control, identical second save")
	require.Equal(t, probes, f.f1277.authorizedProbes.Load(),
		"positive control: 0 restarts — the adapter must not have re-probed")
}

func TestT02b_PositiveControl_HealthyPeerUnaffectedByDegradedInstance(t *testing.T) {
	// S-88 positive control (degraded peer): a degraded instance's records
	// are not consulted for its peer — the healthy peer's identical second
	// save stays no_reload_needed while `line` holds a fresh failed-channel
	// record.
	f, _, _ := orderingWriterFixture(t)

	// Stage the degraded peer: drop line's reference, its save's rebuild
	// fails to construct, the manager holds the failed-channel record.
	require.NoError(t, f.f1277.store.Delete(issue1277BrokenAccessRef))
	status, _ := t02bConfigure(t, f, "line", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status)

	status, first := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, first)
	t02bRequireOutcome(t, first, "applied", "peer control, first save")

	status, second := t02bConfigure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, second)
	t02bRequireOutcome(t, second, "no_reload_needed",
		"peer control: the degraded instance's records must not force the peer to restart")
}

func TestT02b_CaseB_BootTimeStartFailure_NeedsFixtureSeam(t *testing.T) {
	// S-88 case b: a BOOT-time start failure (scripted at gateway start, a
	// second healthy connection keeping the gateway up — the gate_r2 state),
	// cause removed, then the identical save retries via the start-failure
	// leg. The wu6 fixture boots both instances healthy and cannot stage a
	// boot-time start failure (the boot credential set is fixed inside
	// newWU6Fixture). Fixture seam: a boot variant that stages the
	// unauthorized credential BEFORE StartAll. The reload-time leg (case a)
	// and the existing gate_r2 boot-failure tests cover the surrounding
	// behaviour until then.
	t.Fatalf("BLOCKED: no fixture seam to stage a boot-time Start failure before StartAll — required by S-88 case b / R6-MAJ-001 leg 3; named in the RED report for the WP developers")
}

func TestT02b_CaseC_UnfinishedTeardown_NeedsSeam(t *testing.T) {
	// S-88 case c: an unfinished teardown (a failed Stop retained as a
	// pending disposal — the r9 fixture state) must fail the predicate on the
	// disposal leg and retry on the identical save. Staging a failed Stop
	// through the real REST stack needs an injectable channel Stop failure;
	// the pkg/channels r9 fixture does it with mocks inside the manager
	// package, not through this gateway stack.
	t.Fatalf("BLOCKED: no seam to stage a failed channel Stop (pending disposal) through the real gateway stack — required by S-88 case c / R6-MAJ-001 leg 4; named in the RED report")
}

func TestT02b_CaseG_StaleBundleInterleaving_NeedsCapturePauseSeam(t *testing.T) {
	// S-88 case g (r8, R7-MAJ-002 + grill R8-MIN-001): the deterministic
	// stale-bundle interleaving —
	//   (1) a reload that must rebuild the instance pauses AFTER its final
	//       credential-bundle capture (it has resolved A; the capture is the
	//       snapshot FR-050 leg 5 stamps),
	//   (2) Configure stores B under the selected reference (marker version
	//       advances) and its configuration write fails →
	//       credential_saved_not_saved, no new reload requested,
	//   (3) the paused older rebuild resumes and completes, having consumed
	//       only A → the marker STAYS pending (the rebuild's stamped capture
	//       version predates B's store; FR-050's clearance rule refuses to
	//       acknowledge it — the completion-only r7 rule is the mutation),
	//   (4) a follow-up identical save applies B (the rebuilt adapter
	//       receives B; the fixture's connect-credential probe counts it).
	// R8-MIN-001 sharpens (1): the pause must sit after the AFFECTED
	// REFERENCE has been read but BEFORE bundle capture finishes, so a write
	// interleaved WITH the resolver's reads (not only after capture) is also
	// refused acknowledgment.
	//
	// No seam exists: the reload path exposes neither a bundle-capture pause
	// hook nor the stamped capture version a test could compare. WP-2 must
	// provide them (named in the RED report); the normative invariant is the
	// FR-050 leg-5 snapshot-aware acknowledgment.
	t.Fatalf("BLOCKED: no test hooks for the reload bundle-capture pause, the marker version stamp, or the capture-version comparison — required by S-88 case g / R7-MAJ-002 / R8-MIN-001 / FR-050 leg 5; WP-2 must expose the capture-stamp seam named in the RED report")
}
