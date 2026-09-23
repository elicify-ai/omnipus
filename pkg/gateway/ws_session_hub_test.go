// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeHubConn is a minimal hubConn for hub-level tests: it records every
// frame it is handed, in order, and can simulate a byte-budget overflow.
type fakeHubConn struct {
	mu        sync.Mutex
	received  [][]byte
	capBytes  int // 0 = unlimited
	usedBytes int
	name      string
}

func newFakeHubConn(name string) *fakeHubConn { return &fakeHubConn{name: name} }

func (c *fakeHubConn) enqueue(frame []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capBytes > 0 && c.usedBytes+len(frame) > c.capBytes {
		return false
	}
	c.usedBytes += len(frame)
	cp := append([]byte(nil), frame...)
	c.received = append(c.received, cp)
	return true
}

func (c *fakeHubConn) snapshot() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.received))
	copy(out, c.received)
	return out
}

func tokenFrame(t *testing.T, i int) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"type": "token", "content": fmt.Sprintf("tok-%d", i)})
	if err != nil {
		t.Fatalf("marshal token frame: %v", err)
	}
	return b
}

func decodeSeq(t *testing.T, frame []byte) (seq int64, hasSeq bool) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(frame, &m); err != nil {
		t.Fatalf("unmarshal frame: %v (%s)", err, frame)
	}
	raw, ok := m["seq"]
	if !ok {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatalf("unmarshal seq: %v (%s)", err, raw)
	}
	return n, true
}

// i64p is already declared in schedules_rest_test.go (identical shape) — reuse it.
func hubStrp(v string) *string { return &v }

// ---------------------------------------------------------------------
// H1: numbering does not depend on connections.
// ---------------------------------------------------------------------
func TestHub_H1_NumberingIndependentOfConnections(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-1")

	for i := 0; i < 50; i++ {
		hub.publish(tokenFrame(t, i))
	}
	if got := hub.snapshotHead(); got != 50 {
		t.Fatalf("head = %d, want 50", got)
	}

	conn := newFakeHubConn("late")
	res := hub.bind(conn, i64p(0), hubStrp("boot-1"), "boot-1")
	if !res.Servable {
		t.Fatalf("expected servable, got reason=%q", res.Reason)
	}
	if len(res.Tail) != 50 {
		t.Fatalf("tail length = %d, want 50", len(res.Tail))
	}
	for idx, b := range res.Tail {
		seq, ok := decodeSeq(t, b)
		if !ok || seq != int64(idx+1) {
			t.Fatalf("tail[%d] seq=%d ok=%v, want %d", idx, seq, ok, idx+1)
		}
	}
}

// ---------------------------------------------------------------------
// H2: gap-free and monotonic under concurrency.
// ---------------------------------------------------------------------
func TestHub_H2_GapFreeUnderConcurrency(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-2")

	const producers = 8
	const perProducer = 1000
	var wg sync.WaitGroup
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				hub.submit(tokenFrame(t, p*perProducer+i))
			}
		}(p)
	}

	// Attach/detach loop concurrently with publishing.
	stop := make(chan struct{})
	var attachWG sync.WaitGroup
	attachWG.Add(3)
	for c := 0; c < 3; c++ {
		go func(c int) {
			defer attachWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				conn := newFakeHubConn(fmt.Sprintf("c%d", c))
				hub.bind(conn, i64p(int64(hub.snapshotHead())), hubStrp("boot-1"), "boot-1")
				time.Sleep(time.Microsecond)
				hub.unbind(conn)
			}
		}(c)
	}

	wg.Wait()
	// Wait for the drainer to finish (submit runs the drainer async).
	deadline := time.Now().Add(5 * time.Second)
	for {
		hub.inboxMu.Lock()
		empty := len(hub.inbox) == 0 && !hub.draining
		hub.inboxMu.Unlock()
		if empty {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("drainer did not finish within deadline")
		}
		time.Sleep(time.Millisecond)
	}
	close(stop)
	attachWG.Wait()

	if got, want := hub.snapshotHead(), uint64(producers*perProducer); got != want {
		t.Fatalf("head = %d, want %d", got, want)
	}

	// journal is contiguous starting at lowSeq.
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for i, e := range hub.journal {
		want := hub.lowSeq + uint64(i)
		if e.seq != want {
			t.Fatalf("journal[%d].seq = %d, want %d (gap)", i, e.seq, want)
		}
	}
}

// ---------------------------------------------------------------------
// H3: multi-tab byte identity.
// ---------------------------------------------------------------------
func TestHub_H3_MultiTabByteIdentity(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-3")

	connA := newFakeHubConn("A")
	connB := newFakeHubConn("B")
	hub.bind(connA, i64p(0), hubStrp("boot-1"), "boot-1")
	hub.bind(connB, i64p(0), hubStrp("boot-1"), "boot-1")

	for i := 0; i < 10; i++ {
		hub.publish(tokenFrame(t, i))
	}
	doneFrame, err := json.Marshal(map[string]any{"type": "done"})
	if err != nil {
		t.Fatalf("marshal done: %v", err)
	}
	hub.publish(doneFrame)

	a, b := connA.snapshot(), connB.snapshot()
	if len(a) != 11 || len(b) != 11 {
		t.Fatalf("lengths = %d, %d, want 11 each", len(a), len(b))
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			t.Fatalf("frame %d differs between tabs:\nA=%s\nB=%s", i, a[i], b[i])
		}
	}
}

// ---------------------------------------------------------------------
// H4: incremental attach while publishing — no gap, no duplicate across
// the tail/live boundary.
// ---------------------------------------------------------------------
func TestHub_H4_IncrementalAttachWhilePublishing(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-4")

	for i := 0; i < 20; i++ {
		hub.publish(tokenFrame(t, i))
	}

	conn := newFakeHubConn("hammered")
	var res attachResult
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		res = hub.bind(conn, i64p(20), hubStrp("boot-1"), "boot-1")
	}()
	// Hammer publishes concurrently; some land before the bind, some after —
	// the hub's job is to make sure every one of them ends up exactly once
	// either in the tail or delivered live, never both, never missing.
	for i := 20; i < 220; i++ {
		hub.publish(tokenFrame(t, i))
	}
	wg.Wait()

	if !res.Servable {
		t.Fatalf("expected servable, got reason=%q", res.Reason)
	}
	W := res.Head
	live := conn.snapshot()

	seen := map[int64]bool{}
	for _, b := range res.Tail {
		seq, _ := decodeSeq(t, b)
		if seen[seq] {
			t.Fatalf("duplicate seq %d in tail", seq)
		}
		seen[seq] = true
		if seq <= 20 || uint64(seq) > W {
			t.Fatalf("tail seq %d out of range (20, %d]", seq, W)
		}
	}
	for _, b := range live {
		seq, _ := decodeSeq(t, b)
		if seen[seq] {
			t.Fatalf("duplicate seq %d across tail+live", seq)
		}
		seen[seq] = true
		if uint64(seq) <= W {
			t.Fatalf("live seq %d should be > W=%d", seq, W)
		}
	}
	if uint64(len(seen)) != hub.snapshotHead()-20 {
		t.Fatalf("saw %d distinct seqs, want %d (final head=%d)", len(seen), hub.snapshotHead()-20, hub.snapshotHead())
	}
}

// ---------------------------------------------------------------------
// H5: snapshot decisions — the cursor-servability table (§3.3).
// ---------------------------------------------------------------------
func TestHub_H5_SnapshotDecisions(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-5")
	for i := 0; i < 5; i++ {
		hub.publish(tokenFrame(t, i))
	}
	// Force retention: trim the journal down so lowSeq moves past 1.
	hub.mu.Lock()
	hub.journal = hub.journal[3:] // keep seq 4,5 only
	hub.lowSeq = hub.journal[0].seq
	hub.mu.Unlock()

	cases := []struct {
		name     string
		since    *int64
		boot     *string
		servable bool
		reason   string
	}{
		{"boot mismatch", i64p(5), hubStrp("other-boot"), false, reasonBootMismatch},
		{"below retention", i64p(1), hubStrp("boot-1"), false, reasonRetentionExceeded},
		{"above head", i64p(999), hubStrp("boot-1"), false, reasonCursorAhead},
		{"no cursor", nil, hubStrp("boot-1"), false, reasonUnknownPosition},
		{"equal to head, empty incremental", i64p(5), hubStrp("boot-1"), true, ""},
		{"at retention floor", i64p(3), hubStrp("boot-1"), true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := newFakeHubConn(tc.name)
			res := hub.bind(conn, tc.since, tc.boot, "boot-1")
			hub.unbind(conn)
			if res.Servable != tc.servable {
				t.Fatalf("servable = %v, want %v (reason=%q)", res.Servable, tc.servable, res.Reason)
			}
			if res.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", res.Reason, tc.reason)
			}
			if tc.name == "equal to head, empty incremental" && len(res.Tail) != 0 {
				t.Fatalf("expected empty tail, got %d entries", len(res.Tail))
			}
		})
	}
}

// ---------------------------------------------------------------------
// H8: the counter never goes backwards across idle eviction.
// ---------------------------------------------------------------------
func TestHub_H8_CounterNeverGoesBackwards(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-8")
	for i := 0; i < 10; i++ {
		hub.publish(tokenFrame(t, i))
	}
	oldHead := hub.snapshotHead()

	// A bound connection blocks eviction.
	conn := newFakeHubConn("keepalive")
	hub.bind(conn, i64p(int64(oldHead)), hubStrp("boot-1"), "boot-1")
	hub.mu.Lock()
	hub.lastActive = time.Now().Add(-time.Hour)
	hub.mu.Unlock()
	evicted := reg.evictIdle(time.Now())
	if len(evicted) != 0 {
		t.Fatalf("hub with a bound connection was evicted: %v", evicted)
	}
	hub.unbind(conn)

	// Now idle + unbound + old lastActive → eligible.
	hub.mu.Lock()
	hub.lastActive = time.Now().Add(-time.Hour)
	hub.mu.Unlock()
	evicted = reg.evictIdle(time.Now())
	if len(evicted) != 1 || evicted[0] != "sess-8" {
		t.Fatalf("evicted = %v, want [sess-8]", evicted)
	}

	newHub := reg.getOrCreate("sess-8")
	if newHub == hub {
		t.Fatal("expected a fresh hub instance after eviction")
	}
	if newHub.base < oldHead || newHub.head < oldHead {
		t.Fatalf("new hub base=%d head=%d, want >= old head %d", newHub.base, newHub.head, oldHead)
	}
	nextSeq := newHub.publish(tokenFrame(t, 0))
	if nextSeq <= oldHead {
		t.Fatalf("recreated hub issued seq %d, want > old head %d", nextSeq, oldHead)
	}

	// Belt and braces: if a stale connection somehow still had cursor
	// oldHead bound to the OLD hub object, publishing on the new hub does
	// not touch it, and its next expected seq (oldHead+1) will never equal
	// what the new hub would deliver starting at base+1 — i.e. the gap is
	// detectable, never silently wrong. We assert the concrete inequality
	// that makes that true: new hub's first seq != oldHead+1 whenever the
	// registry's global counter had already moved past oldHead+1 before the
	// eviction (which is the case that matters — same-run eviction with no
	// intervening publishes on OTHER sessions gives equality, which is also
	// safe, just not the illustrative case). Here we simply confirm strict
	// monotonicity, which is the guarantee that actually matters.
	if newHub.base != oldHead {
		t.Fatalf("newHub.base = %d, want %d (registry counter should resume exactly at old head since no other session published)", newHub.base, oldHead)
	}
}

// ---------------------------------------------------------------------
// H9: overflow closes, never drops.
// ---------------------------------------------------------------------
func TestHub_H9_OverflowClosesNeverDrops(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-9")

	slow := newFakeHubConn("slow")
	slow.capBytes = 50 // small cap: will overflow quickly
	fast := newFakeHubConn("fast")
	// fast has no cap — effectively unlimited.

	var overflowed []string
	var omu sync.Mutex
	hub.mu.Lock()
	hub.onOverflow = func(c hubConn) {
		omu.Lock()
		defer omu.Unlock()
		if fc, ok := c.(*fakeHubConn); ok {
			overflowed = append(overflowed, fc.name)
		}
	}
	hub.mu.Unlock()

	hub.bind(slow, i64p(0), hubStrp("boot-1"), "boot-1")
	hub.bind(fast, i64p(0), hubStrp("boot-1"), "boot-1")

	for i := 0; i < 20; i++ {
		hub.publish(tokenFrame(t, i))
	}

	omu.Lock()
	gotOverflow := len(overflowed) > 0
	omu.Unlock()
	if !gotOverflow {
		t.Fatal("expected the slow connection to overflow and be reported")
	}

	// The journal is intact (nothing dropped from the log itself) and the
	// other connection is unaffected (got every frame).
	if hub.boundCount() != 1 {
		t.Fatalf("boundCount = %d, want 1 (only fast should remain bound)", hub.boundCount())
	}
	if got := len(fast.snapshot()); got != 20 {
		t.Fatalf("fast received %d frames, want 20 (unaffected by slow's overflow)", got)
	}
	hub.mu.Lock()
	journalLen := len(hub.journal)
	hub.mu.Unlock()
	if journalLen != 20 {
		t.Fatalf("journal has %d entries, want 20 (nothing dropped)", journalLen)
	}
}

// ---------------------------------------------------------------------
// H15 (partial): budgets — per-session journal trim by frame count and by
// byte count, plus the global cross-session byte budget.
// ---------------------------------------------------------------------
func TestHub_H15_JournalTrimByFrameCount(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-15a")
	for i := 0; i < hubJournalMaxFrames+100; i++ {
		hub.publish(tokenFrame(t, i))
	}
	hub.mu.Lock()
	n := len(hub.journal)
	low := hub.lowSeq
	hub.mu.Unlock()
	if n >= hubJournalMaxFrames {
		t.Fatalf("journal has %d entries after trim, want < %d", n, hubJournalMaxFrames)
	}
	if hub.journal[0].seq != low {
		t.Fatalf("lowSeq %d does not match journal[0].seq %d", low, hub.journal[0].seq)
	}
}

func TestHub_H15_GlobalBudgetDropsLRUJournal(t *testing.T) {
	reg := newHubRegistry("boot-1")
	// Session "old" is published to first, then goes idle. Each OTHER
	// session below stays within its OWN per-session 1 MiB cap (§3.1), so
	// this test must use many distinct sessions to cross the 32 MiB GLOBAL
	// cap — a single session can never do it, since its own journal is
	// capped well below the global budget by appendJournalLocked's
	// per-session trim. (An earlier version of this test tried a single big
	// session and never actually exercised enforceGlobalBudget at all —
	// caught by this test's own RED run.)
	oldHub := reg.getOrCreate("sess-15-old")
	for i := 0; i < 100; i++ {
		oldHub.publish(tokenFrame(t, i))
	}
	oldHub.mu.Lock()
	oldHub.lastActive = time.Now().Add(-time.Hour)
	oldHub.mu.Unlock()

	big := make([]byte, 200*1024) // 200 KiB payload per frame
	for i := range big {
		big[i] = 'x'
	}
	frame, err := json.Marshal(map[string]any{"type": "media", "content": string(big)})
	if err != nil {
		t.Fatalf("marshal big frame: %v", err)
	}
	frameBytesEach := len(frame) + 16 // + slack for the spliced ,"seq":N

	// 4 frames/session keeps each session's OWN journal comfortably under
	// its 1 MiB per-session cap (§3.1), so no per-session trim fires and the
	// per-session contribution is an exact, known quantity — unlike an
	// earlier version of this test, which tried to push a single session
	// over the cap and never actually exercised enforceGlobalBudget at all
	// (caught by that version's own unexpected-green run). 55 such sessions
	// is enough to put the registry's TOTAL past the 32 MiB global cap with
	// clear margin even accounting for JSON/seq overhead.
	const sessions = 55
	const framesPerSession = 4
	if perSession := frameBytesEach * framesPerSession; perSession >= hubJournalMaxBytes {
		t.Fatalf("test setup invariant broken: per-session bytes %d must stay under the %d per-session cap", perSession, hubJournalMaxBytes)
	}
	if total := frameBytesEach * framesPerSession * sessions; total <= hubGlobalJournalMaxBytes {
		t.Fatalf("test setup invariant broken: total bytes %d must exceed the %d global cap", total, hubGlobalJournalMaxBytes)
	}
	for s := 0; s < sessions; s++ {
		h := reg.getOrCreate(fmt.Sprintf("sess-15-new-%d", s))
		for i := 0; i < framesPerSession; i++ {
			h.publish(frame)
		}
	}

	oldHub.mu.Lock()
	oldJournalLen := len(oldHub.journal)
	oldLowSeq := oldHub.lowSeq
	oldHead := oldHub.head
	oldHub.mu.Unlock()

	if oldJournalLen != 0 {
		t.Fatalf("old (LRU) hub's journal was not dropped: %d entries remain", oldJournalLen)
	}
	if oldLowSeq != oldHead+1 {
		t.Fatalf("old hub lowSeq=%d, want head+1=%d after journal drop", oldLowSeq, oldHead+1)
	}
	// The counter itself is untouched by the journal drop.
	if oldHead != 100 {
		t.Fatalf("old hub head=%d, want 100 (counter must survive a journal drop)", oldHead)
	}
}

// ---------------------------------------------------------------------
// spliceSeq unit coverage (not an H-numbered invariant, but the mechanism
// every H-test above relies on to read the assigned seq back out).
// ---------------------------------------------------------------------
func TestSpliceSeq(t *testing.T) {
	got := spliceSeq([]byte(`{"type":"token"}`), 7)
	want := `{"type":"token","seq":7}`
	if string(got) != want {
		t.Fatalf("spliceSeq = %s, want %s", got, want)
	}
	// Empty object.
	got = spliceSeq([]byte(`{}`), 1)
	want = `{"seq":1}`
	if string(got) != want {
		t.Fatalf("spliceSeq(empty) = %s, want %s", got, want)
	}
}

// TestHubRegistry_Lookup exercises lookup, the read-only counterpart to
// getOrCreate that a future integration pass needs (e.g. deciding whether a
// session already has a hub before doing anything that would create one —
// getOrCreate is not appropriate for a pure read like "does this session
// have any live activity").
func TestHubRegistry_Lookup(t *testing.T) {
	reg := newHubRegistry("boot-1")
	if got := reg.lookup("does-not-exist"); got != nil {
		t.Fatalf("lookup on unknown id = %v, want nil", got)
	}
	created := reg.getOrCreate("sess-lookup")
	if got := reg.lookup("sess-lookup"); got != created {
		t.Fatalf("lookup returned %v, want the same instance %v", got, created)
	}
}

// TestHub_PublishBytes_MatchesJournal exercises publishBytes, the variant
// used by the transitional wiring in websocket_streamer.go (Update/Finalize
// resolve their own delivery targets outside hub.conns while the full
// attach/bind cutover is still in progress): the bytes it returns must be
// byte-identical to what actually landed in the journal at that seq.
func TestHub_PublishBytes_MatchesJournal(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-publishbytes")

	seq, out := hub.publishBytes(tokenFrame(t, 0))
	if seq != 1 {
		t.Fatalf("seq = %d, want 1", seq)
	}
	hub.mu.Lock()
	journaled := hub.journal[0].bytes
	hub.mu.Unlock()
	if string(out) != string(journaled) {
		t.Fatalf("publishBytes returned %s, journal has %s", out, journaled)
	}
	gotSeq, ok := decodeSeq(t, out)
	if !ok || gotSeq != 1 {
		t.Fatalf("decoded seq = %d ok=%v, want 1", gotSeq, ok)
	}
}

// TestNewHubRegistry_IdleEvictAfter_DefaultAndOverride proves FOUNDER
// DECISION Q6: idleEvictAfter defaults to 10 minutes (never overridden) in
// every normal process, and the ONLY way to shorten it is the explicit,
// undocumented, test-only env var — never a config file, never a REST
// call. It restores/unsets the env var itself so it cannot leak into any
// other test running in the same process (go test -p 1 in this repo's own
// convention runs the whole package's tests in one process).
func TestNewHubRegistry_IdleEvictAfter_DefaultAndOverride(t *testing.T) {
	t.Run("default is 10 minutes", func(t *testing.T) {
		os.Unsetenv(hubIdleEvictAfterEnvOverrideVar)
		reg := newHubRegistry("boot-1")
		if reg.idleEvictAfter != 10*time.Minute {
			t.Fatalf("idleEvictAfter = %s, want 10m (default, no env override set)", reg.idleEvictAfter)
		}
	})

	t.Run("env override shortens it", func(t *testing.T) {
		t.Setenv(hubIdleEvictAfterEnvOverrideVar, "5")
		reg := newHubRegistry("boot-1")
		if reg.idleEvictAfter != 5*time.Second {
			t.Fatalf("idleEvictAfter = %s, want 5s (env override)", reg.idleEvictAfter)
		}
	})

	t.Run("invalid value falls back to the default rather than panicking or zeroing", func(t *testing.T) {
		t.Setenv(hubIdleEvictAfterEnvOverrideVar, "not-a-number")
		reg := newHubRegistry("boot-1")
		if reg.idleEvictAfter != 10*time.Minute {
			t.Fatalf("idleEvictAfter = %s, want 10m (invalid override must fall back to the default)", reg.idleEvictAfter)
		}
	})
}

// TestHubRegistry_MaybeSweepIdle_RateLimitedAndActuallyEvicts proves the
// BE-DESIGN.md §3.2 piggybacked sweep: it evicts an eligible hub, and it
// does NOT re-run within hubIdleSweepInterval of its last run even if
// called again — the whole point of rate-limiting it is that it is called
// from the hot publish/submit path.
func TestHubRegistry_MaybeSweepIdle_RateLimitedAndActuallyEvicts(t *testing.T) {
	reg := newHubRegistry("boot-1")
	reg.idleEvictAfter = time.Millisecond // this test fakes elapsed time via `now`, not a real wait
	hub := reg.getOrCreate("sess-sweep")
	hub.publish(tokenFrame(t, 0))
	hub.mu.Lock()
	hub.lastActive = time.Now().Add(-time.Hour)
	hub.mu.Unlock()

	// publishBytes (called by publish, above) already ran its OWN internal
	// maybeSweepIdle(time.Now()) as part of publishing the setup token,
	// consuming the rate-limit slot moments ago — reset it so THIS test's
	// "first sweep" call below is actually testing a fresh interval, not
	// getting silently rate-limited by the setup call itself.
	reg.lastEvictSweepUnixNano.Store(0)

	base := time.Now()
	reg.maybeSweepIdle(base)
	if reg.lookup("sess-sweep") != nil {
		t.Fatal("first sweep should have evicted the idle hub")
	}

	// Re-create it, age it again, and call maybeSweepIdle again WITHOUT
	// advancing past hubIdleSweepInterval — it must be a no-op this time.
	hub2 := reg.getOrCreate("sess-sweep")
	hub2.mu.Lock()
	hub2.lastActive = time.Now().Add(-time.Hour)
	hub2.mu.Unlock()
	reg.maybeSweepIdle(base.Add(time.Second)) // well under hubIdleSweepInterval (1 minute)
	if reg.lookup("sess-sweep") == nil {
		t.Fatal("a second sweep within hubIdleSweepInterval must be a no-op (rate-limited)")
	}

	// Advancing PAST the interval must let it run again.
	reg.maybeSweepIdle(base.Add(hubIdleSweepInterval + time.Second))
	if reg.lookup("sess-sweep") != nil {
		t.Fatal("a sweep after hubIdleSweepInterval has elapsed must evict the (still idle) hub")
	}
}
