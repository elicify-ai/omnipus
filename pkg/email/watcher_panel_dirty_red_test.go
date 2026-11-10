package email

// W1 §3.1 register row 13 / §4.10.3 / §8 rows 13 (W1-observable half of
// MC-W1-23) and 21; FR-W1-26, T21, DS-6 row 4. Gate finding (pr-test-analyzer
// #2): the dirty-mark signal had zero coverage — MarkPanelMetadataDirty,
// SetPanelDirtySink and markPanelDirtyIfNeeded were referenced by production
// only.
//
// Oracles are derived from the spec, never from the implementation:
//   - Register row 13: the signal is "folder version changed while closed";
//     it is set when the watcher OBSERVES a folder-version change, is
//     transport-agnostic, carries no mail data (pair + folder identity only —
//     FR-W1-26), and pending marks follow consumed-once semantics at the next
//     panel event.
//   - A first observation is the baseline, not a change (DS-6: "UIDVALIDITY
//     reset (baseline rule preserved)" — a baseline must exist before a
//     change can be observed), so it never marks.
//   - A UIDVALIDITY change is a folder-version change (W2 §3.6 trigger 4:
//     "an observed folder-version change (UIDVALIDITY)") — it marks.
//   - An unseen-count increase is the watcher's new-mail observation, which
//     "only marks panel metadata dirty for the next panel-open event"
//     (W1 §4.10.3 / US-8 scenario 4; ADR P1.3 watcher rows) — it marks.
//   - A no-change cycle marks nothing (nothing was observed).
//   - NOT asserted (spec gap, reported to team-lead): an unseen-count
//     DECREASE. No spec text settles whether a decrease is a folder-version
//     change; pinning either direction would read the implementation, not the
//     spec. The `unseen > prev.UnseenTotal` → `!=` mutation therefore
//     survives this pack by construction — it is a spec gap, not a missed
//     mutant.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// dirtyMarkTransport is the watcher-test transport double: cycleStubTransport's
// five Transport methods plus the one-STATUS probe the watcher prefers
// (MailboxStatuser) and the unexported markPanelMetadataDirty capability the
// register row 13 freeze publishes. Counters and marks are the test's
// observation instrument — nothing is mocked away: the trigger logic under
// test (Watcher.markPanelDirtyIfNeeded via the full Cycle path) is real.
type dirtyMarkTransport struct {
	cycleStubTransport
	probes      atomic.Int32
	unseen      int
	uidnext     uint32
	uidvalidity uint32

	mu    sync.Mutex
	marks [][2]string
}

func (d *dirtyMarkTransport) MailboxStatus(ctx context.Context) (int, uint32, uint32, error) {
	d.probes.Add(1)
	return d.unseen, d.uidnext, d.uidvalidity, nil
}

func (d *dirtyMarkTransport) markPanelMetadataDirty(pair, folder string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.marks = append(d.marks, [2]string{pair, folder})
}

func (d *dirtyMarkTransport) recordedMarks() [][2]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([][2]string(nil), d.marks...)
}

const (
	dirtyTestAgent  = "agent-dm"
	dirtyTestWS     = "ws-dm"
	dirtyTestPair   = dirtyTestAgent + "/" + dirtyTestWS // the pair identity the signal carries (register row 13: MarkPanelMetadataDirty(pair, folder); exact shape fixed at implementation per the row)
	dirtyTestFolder = "INBOX"                            // the folder the watcher probes (its one-STATUS unseen/uidnext/uidvalidity path)
)

func newDirtyMarkWatcher(t *testing.T) (*Watcher, *dirtyMarkTransport, string) {
	t.Helper()
	d := &dirtyMarkTransport{uidnext: 7, uidvalidity: 100}
	dir := t.TempDir()
	w, err := NewWatcher(WatcherConfig{
		AgentID: dirtyTestAgent, WorkspaceID: dirtyTestWS,
		Transport: d, StateDir: dir,
	})
	require.NoError(t, err)
	return w, d, dir
}

// TestWatcher_PanelDirtyMarkTriggers is the trigger matrix over the watcher's
// only panel-metadata interaction (FR-W1-26): baseline, no change, UIDVALIDITY
// change, unseen-count increase.
func TestWatcher_PanelDirtyMarkTriggers(t *testing.T) {
	ctx := context.Background()

	t.Run("baseline_first_observation_records_no_mark", func(t *testing.T) {
		w, d, dir := newDirtyMarkWatcher(t)
		d.unseen, d.uidvalidity = 3, 100

		require.NoError(t, w.Cycle(ctx))
		require.Equal(t, int32(1), d.probes.Load(), "premise: the cycle probed once")
		require.Empty(t, d.recordedMarks(),
			"register row 13 / DS-6 baseline rule: a first observation establishes the baseline — it is not a folder-version change and must not mark the panel dirty")
		st, err := LoadWatcherState(dir, dirtyTestAgent, dirtyTestWS)
		require.NoError(t, err)
		require.NotEmpty(t, st.LastSuccessAt,
			"premise: the baseline was genuinely recorded — the empty mark is not a skipped cycle")
	})

	t.Run("no_change_records_no_mark", func(t *testing.T) {
		w, d, _ := newDirtyMarkWatcher(t)
		d.unseen, d.uidnext, d.uidvalidity = 3, 7, 100

		require.NoError(t, w.Cycle(ctx)) // baseline
		require.NoError(t, w.Cycle(ctx)) // identical observation
		require.Equal(t, int32(2), d.probes.Load(),
			"premise: both cycles probed — the empty mark is a real comparison outcome, not a missing second cycle")
		require.Empty(t, d.recordedMarks(),
			"register row 13: with no folder-version change observed there is nothing to signal — every cycle marking dirty would invalidate the panel on every tick")
	})

	t.Run("uidvalidity_change_marks_pair_and_folder", func(t *testing.T) {
		w, d, _ := newDirtyMarkWatcher(t)
		d.unseen, d.uidnext, d.uidvalidity = 3, 7, 100

		require.NoError(t, w.Cycle(ctx)) // baseline at UIDVALIDITY 100
		d.uidvalidity = 200              // folder recreated / server-side rebuild (W2 §3.6 trigger 4)
		require.NoError(t, w.Cycle(ctx))
		require.Equal(t, [][2]string{{dirtyTestPair, dirtyTestFolder}}, d.recordedMarks(),
			"W2 §3.6 trigger 4: a UIDVALIDITY change is an observed folder-version change — exactly one mark, carrying the pair and folder identity only (FR-W1-26: no mail data)")
	})

	t.Run("unseen_increase_marks_pair_and_folder", func(t *testing.T) {
		w, d, _ := newDirtyMarkWatcher(t)
		d.unseen, d.uidnext, d.uidvalidity = 3, 7, 100

		require.NoError(t, w.Cycle(ctx)) // baseline at unseen 3
		d.unseen, d.uidnext = 5, 9       // new mail arrived while the panel is closed
		require.NoError(t, w.Cycle(ctx))
		require.Equal(t, [][2]string{{dirtyTestPair, dirtyTestFolder}}, d.recordedMarks(),
			"§4.10.3 / US-8 scenario 4: a folder change the watcher notices (new unseen mail) only marks panel metadata dirty for the next panel-open event — exactly one mark, identity only")
	})
}

// dirtySinkRecorder records everything a wired dirty-mark sink receives.
type dirtySinkRecorder struct {
	mu  sync.Mutex
	got [][2]string
}

func (r *dirtySinkRecorder) sink(pair, folder string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, [2]string{pair, folder})
}

func (r *dirtySinkRecorder) snap() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]string(nil), r.got...)
}

func newDirtyTestSessions(t *testing.T) *MailSessions {
	t.Helper()
	s := NewMailSessions(SessionsConfig{
		StateDir:    t.TempDir(),
		Credentials: func(string) (string, string, error) { return testIMAPUser, testIMAPPass, nil },
	})
	t.Cleanup(s.Close)
	return s
}

// TestSessions_PanelDirtyMarkDeliveredOnce asserts register row 13's
// consumed-once semantics at the MailSessions seam: a mark raised while a sink
// is wired is delivered immediately exactly once; a mark raised with no sink
// (the panel-closed case) stays pending and is delivered exactly once when a
// sink is wired; a consumed pending mark is never re-delivered on re-wiring;
// distinct pending marks are each delivered once.
func TestSessions_PanelDirtyMarkDeliveredOnce(t *testing.T) {
	t.Run("wired_sink_delivers_immediately_exactly_once", func(t *testing.T) {
		sessions := newDirtyTestSessions(t)
		var rec dirtySinkRecorder
		sessions.SetPanelDirtySink(rec.sink)

		sessions.MarkPanelMetadataDirty("pair-a", "INBOX")
		require.Equal(t, [][2]string{{"pair-a", "INBOX"}}, rec.snap(),
			"register row 13: with the consumer wired, the mark is delivered to it — exactly once")
	})

	t.Run("pending_mark_delivered_exactly_once_when_sink_wired", func(t *testing.T) {
		sessions := newDirtyTestSessions(t)
		// No sink yet — the panel-closed case: the change is observed while no
		// eligible panel event consumes it.
		sessions.MarkPanelMetadataDirty("pair-b", "INBOX")

		var rec dirtySinkRecorder
		sessions.SetPanelDirtySink(rec.sink)
		require.Equal(t, [][2]string{{"pair-b", "INBOX"}}, rec.snap(),
			"register row 13: a mark recorded while the panel is closed is held pending and delivered once at the next panel event (the sink wiring)")
	})

	t.Run("consumed_pending_never_redispatched_on_rewire", func(t *testing.T) {
		sessions := newDirtyTestSessions(t)
		sessions.MarkPanelMetadataDirty("pair-c", "INBOX")

		var first, second dirtySinkRecorder
		sessions.SetPanelDirtySink(first.sink)
		require.Equal(t, [][2]string{{"pair-c", "INBOX"}}, first.snap(), "premise: the pending mark was delivered to the first consumer")

		sessions.SetPanelDirtySink(second.sink)
		require.Empty(t, second.snap(),
			"register row 13 consumed-once: a consumed mark is never re-delivered to a re-wired consumer — the panel would refresh twice for one change")
	})

	t.Run("distinct_pending_marks_each_delivered_once", func(t *testing.T) {
		sessions := newDirtyTestSessions(t)
		sessions.MarkPanelMetadataDirty("pair-c", "INBOX")
		sessions.MarkPanelMetadataDirty("pair-d", "INBOX")

		var rec dirtySinkRecorder
		sessions.SetPanelDirtySink(rec.sink)
		got := rec.snap()
		require.Len(t, got, 2, "each pending pair's mark is delivered — no pending mark may be lost on wiring")
		require.Contains(t, got, [2]string{"pair-c", "INBOX"}, "pair-c's pending mark delivered")
		require.Contains(t, got, [2]string{"pair-d", "INBOX"}, "pair-d's pending mark delivered")
		require.NotEqual(t, got[0], got[1], "each distinct mark delivered exactly once (no duplicated delivery)")
	})
}

// TestWatcher_DirtyMarkReachesSessionSink asserts the T21/MC-W1-23 seam end to
// end at W1's boundary: a watcher cycle whose real pooled client observes an
// unseen-count increase delivers the dirty mark through the client to the
// wired session sink — exactly once, pair and folder only — and a subsequent
// cycle that observes no change does not re-mark. (The zero-retained-sockets
// half of MC-W1-23 is TestWatcher_CycleOwnWorkspacePanelOpen_NeverRetains'; no
// cache call can exist on this path because no cache is wired on it — W2's
// wiring is a later wave, so there is nothing to observe here.)
func TestWatcher_DirtyMarkReachesSessionSink(t *testing.T) {
	stub := newStubIMAPServer(t) // its STATUS answer: UNSEEN 1, UIDNEXT 3, UIDVALIDITY 4242
	clock := newPoolTestClock()
	sessions := newSessionsForStub(t, stub, clock)
	client := facadeWithScope(t, stub, sessions, "agent-fi/ws-fi", "gen-1")
	dir := t.TempDir()
	w, err := NewWatcher(WatcherConfig{
		AgentID: "agent-fi", WorkspaceID: "ws-fi",
		Transport: client, StateDir: dir, Now: clock.Now,
	})
	require.NoError(t, err)

	// Prior state: a recorded baseline with zero unseen on the same folder
	// version (UIDVALIDITY 4242 — the value this server reports). The cycle
	// below therefore observes an unseen-count INCREASE, the §4.10.3 case.
	writeWatcherState(t, dir, WatcherState{
		AgentID: "agent-fi", WorkspaceID: "ws-fi",
		UIDValidity: 4242, UnseenTotal: 0,
		State:         "ok",
		LastSuccessAt: clock.Now().UTC().Format(time.RFC3339),
	})

	var rec dirtySinkRecorder
	sessions.SetPanelDirtySink(rec.sink)

	require.NoError(t, w.cycleIfDue(testCtx(t, 3*time.Second), clock.Now()))
	require.Equal(t, [][2]string{{"agent-fi/ws-fi", "INBOX"}}, rec.snap(),
		"register row 13 / FR-W1-26: the watcher's new-mail observation reaches the wired sink through the real client path — exactly one mark, pair and folder identity only")

	// The same observation again: no change, no further mark.
	clock.advance(time.Minute)
	require.NoError(t, w.cycleIfDue(testCtx(t, 3*time.Second), clock.Now()))
	require.Len(t, rec.snap(), 1,
		"a cycle that observes no change never re-marks — the panel must not be invalidated on every tick")
}
