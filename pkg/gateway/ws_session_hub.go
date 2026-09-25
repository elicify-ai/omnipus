// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// newHubBootID mints a fresh 128-bit, hex-encoded boot id (BE-DESIGN.md
// §3.4): one per WSHandler instance (i.e. one per gateway process run in
// practice), used by the cursor-servability rule (§3.3) to tell a stale
// cursor from a previous run apart from one that is merely behind. A
// crypto/rand failure here is treated as non-fatal (falls back to a
// time-based id) rather than panicking gateway boot — see the "unknown
// position" and "boot_mismatch" snapshot reasons downstream, both of which
// degrade a wrong/weak boot id to "always answer with a snapshot", never to
// a wrong catch-up.
func newHubBootID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		logsafeError("ws: crypto/rand failed for hub boot id, falling back to a time-derived id", "error", err)
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

// This file is the #823 catch-up-redesign chokepoint described in
// .squads/BE-DESIGN.md §1-§3: a single per-session event log ("hub") that
// every conversation frame for a session passes through exactly once, whether
// or not any browser tab is attached. Every bound connection then reads that
// log through a monotonic, gap-free, byte-identical sequence.
//
// Producers (wsStreamer, webchatChannel, the EventBus sync tap, message
// intake, cancel) publish here; publish numbers the frame, journals it,
// updates the active-turn projection (ws_hub_projection.go) and appends the
// bytes to every bound connection's own outbound queue (ws_conn_queue.go) —
// all in one critical section, with no network I/O. Attach/reconnect reads
// the journal (incremental) or the projection plus the transcript (snapshot)
// under the same lock that binds the connection (websocket_replay.go).

// hubConn is what the session hub needs from a bound connection: a
// non-blocking, never-dropping append to that connection's own ordered
// outbound queue (BE-DESIGN.md §2.1). *wsConn implements it
// (ws_conn_queue.go); hub-level tests use a fake. enqueue returning false
// means the connection is gone or too far behind and has been (or is being)
// closed with 4008 — the hub only unbinds it; it never touches the network.
type hubConn interface {
	enqueue(frame []byte) bool
}

// journalEntry is one retained, already-sequenced frame.
type journalEntry struct {
	seq   uint64
	bytes []byte
}

// hubIntent is one item submitted to a hub's inbox. frame is the frame's
// JSON bytes with no "seq" key yet (or an ignored placeholder) — publish
// splices the real, hub-assigned seq in before journaling or delivering it.
type hubIntent struct {
	frame []byte
}

// Retention bounds, BE-DESIGN.md §3.1.
const (
	hubJournalMaxFrames      = 2048
	hubJournalMaxBytes       = 1 << 20  // 1 MiB
	hubJournalTrimFraction   = 0.75     // batch-trim down to this fraction of the cap
	hubGlobalJournalMaxBytes = 32 << 20 // 32 MiB across all sessions
)

// sessionHub is one session's event log: the single chokepoint every
// conversation frame for that session passes through exactly once
// (BE-DESIGN.md §1.1).
type sessionHub struct {
	id string

	mu           sync.Mutex
	base         uint64 // first seq ever issued by this incarnation minus 1
	head         uint64 // last seq issued
	lowSeq       uint64 // oldest seq still in the journal (journal[0].seq, or head+1 if empty)
	journal      []journalEntry
	journalBytes int
	conns        map[hubConn]struct{}
	lastActive   time.Time
	proj         activeTurnProjection
	// evicted is set (under mu) when evictIdle removes this hub from the
	// registry. A producer or binder still holding the stale pointer re-routes
	// to the registry's current hub for the same session instead of writing
	// into a hub nobody can reach any more.
	evicted bool

	// onOverflow, if set, is called (outside no lock is held during the
	// call — see publish) for every connection whose enqueue returned false
	// during this publish. The caller owns closing the real socket with
	// 4008; the hub only unbinds its own bookkeeping.
	onOverflow func(hubConn)

	inboxMu  sync.Mutex
	inbox    []hubIntent
	draining bool

	registry *hubRegistry
}

// hubRegistry is one per WSHandler (BE-DESIGN.md §1.1's `h.hubs`). It owns
// hub lifecycle (create/evict), the process-wide published-frame counter
// that guarantees seq numbers never repeat across a hub's eviction and
// recreation (§3.2), and the boot id used for the cursor-servability rule
// (§3.3/§3.4).
type hubRegistry struct {
	mu             sync.RWMutex
	m              map[string]*sessionHub
	publishedTotal atomic.Uint64
	bootID         string

	// globalJournalBytes is the sum of journalBytes across every hub,
	// maintained incrementally so enforceGlobalBudget doesn't need to walk
	// every hub on every publish once nothing is over budget.
	globalJournalBytes atomic.Int64

	// idleEvictAfter is the idle duration before a hub with no bound
	// connections, an empty inbox and no unfinished turn/span in its
	// projection is eligible for eviction. FOUNDER DECISION Q6: a Go unit
	// test does not need to override this — evictIdle takes `now` as a
	// parameter, so a test can simulate any elapsed time without a real
	// wait AND without touching this field (see TestHub_H8_CounterNeverGoesBackwards).
	// This field exists for the ONE case that genuinely cannot fake time:
	// the real-browser e2e "idle 31+ min, then send" scenario, which needs
	// an actual running gateway process whose idle-eviction window is
	// shortened to something a real wall-clock wait can exercise in a
	// reasonable test runtime. See hubIdleEvictAfterEnvOverride for how a
	// real process opts in — off by default, never user-facing, never read
	// from config.json.
	idleEvictAfter time.Duration

	// streamTokenDelay is the e2e scenario-h test-only pause the web
	// streamer takes after publishing each token (see
	// streamTokenDelayEnvOverrideVar). Zero — no pause, no cost beyond one
	// field read per token — in every normal process.
	streamTokenDelay time.Duration

	// lastEvictSweepUnixNano rate-limits the piggybacked idle-eviction
	// sweep (BE-DESIGN.md §3.2: "a sweep piggybacked on submit, at most
	// once per minute") — submit is the hottest path in the whole hub, so
	// this check must be a single atomic load on every call, never a lock.
	lastEvictSweepUnixNano atomic.Int64

	// onEvict, when set, is told which sessions evictIdle just removed, on
	// its own goroutine (evictIdle can run under a publisher's locks), so
	// per-session state kept outside the hub can be released too.
	onEvict func(sessionIDs []string)

	// turnMu guards turnSessions: for each turn that published a tool call,
	// media or error item (the projection items keyed by turn id), the
	// sessions whose hubs hold them. hubTurnEnd pops the turn's entry so it
	// can forget a turn that will never publish a `done` (a steered child
	// or a non-web session has no web streamer) — TurnEndPayload.SessionID
	// is the ROUTING (root) session, not where a steered child's own items
	// live (merge-review F1).
	turnMu       sync.Mutex
	turnSessions map[string]map[string]struct{}
}

// noteTurnSession records that turnID left projection items in sessionID's
// hub (see hubRegistry.turnSessions).
func (r *hubRegistry) noteTurnSession(turnID, sessionID string) {
	if turnID == "" || sessionID == "" {
		return
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	if r.turnSessions == nil {
		r.turnSessions = make(map[string]map[string]struct{})
	}
	set := r.turnSessions[turnID]
	if set == nil {
		set = make(map[string]struct{}, 1)
		r.turnSessions[turnID] = set
	}
	set[sessionID] = struct{}{}
}

// takeTurnSessions returns and forgets the sessions noteTurnSession recorded
// for turnID.
func (r *hubRegistry) takeTurnSessions(turnID string) []string {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	set := r.turnSessions[turnID]
	delete(r.turnSessions, turnID)
	out := make([]string, 0, len(set))
	for sid := range set {
		out = append(out, sid)
	}
	return out
}

// hubIdleSweepInterval bounds how often submit's piggybacked sweep actually
// calls evictIdle (BE-DESIGN.md §3.2). Not the eviction THRESHOLD
// (idleEvictAfter) — this is how often the registry CHECKS for hubs that
// have crossed that threshold.
const hubIdleSweepInterval = time.Minute

// hubIdleEvictAfterEnvOverrideVar is the FOUNDER DECISION Q6 test-only
// shortcut: unset in every normal install, read once at hubRegistry
// construction, and only ever set by an e2e test's own process launch — see
// newHubRegistry's doc comment. Deliberately not a config.json key, not
// exposed via any REST endpoint or the SPA, and not documented in the
// operator-facing docs — a real operator has no way to discover or set it.
const hubIdleEvictAfterEnvOverrideVar = "OMNIPUS_TEST_ONLY_HUB_IDLE_EVICT_SECONDS"

// streamTokenDelayEnvOverrideVar is a test-only shortcut in the same style
// as hubIdleEvictAfterEnvOverrideVar: unset in every normal install, read
// once at hubRegistry construction, and only ever set by an e2e test's own
// gateway process launch. When set to a positive number of milliseconds, the
// web streamer pauses that long after publishing each token frame, so a
// turn stays mid-answer long enough for the #823 e2e scenario h to kill the
// gateway between the first token and done (a fast model otherwise finishes
// before the kill lands). Deliberately not a config.json key, not exposed via
// any REST endpoint or the SPA, and not documented in the operator-facing
// docs — a real operator has no way to discover or set it.
const streamTokenDelayEnvOverrideVar = "OMNIPUS_TEST_ONLY_STREAM_TOKEN_DELAY_MS"

// streamTokenDelayFromEnv reads streamTokenDelayEnvOverrideVar once.
func streamTokenDelayFromEnv() time.Duration {
	raw := os.Getenv(streamTokenDelayEnvOverrideVar)
	if raw == "" {
		return 0
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 {
		logsafeError("ws: invalid "+streamTokenDelayEnvOverrideVar+", ignoring", "value", raw)
		return 0
	}
	if ms == 0 {
		return 0
	}
	logsafeWarn("ws: web streamer token pause enabled — this must NEVER be set in a production install",
		"env_var", streamTokenDelayEnvOverrideVar, "ms", ms)
	return time.Duration(ms) * time.Millisecond
}

func newHubRegistry(bootID string) *hubRegistry {
	idleEvictAfter := 10 * time.Minute
	if raw := os.Getenv(hubIdleEvictAfterEnvOverrideVar); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			idleEvictAfter = time.Duration(secs) * time.Second
			logsafeWarn("ws: hub idle-eviction window overridden — this must NEVER be set in a production install",
				"env_var", hubIdleEvictAfterEnvOverrideVar, "seconds", secs)
		} else {
			logsafeError("ws: invalid "+hubIdleEvictAfterEnvOverrideVar+", ignoring", "value", raw)
		}
	}
	r := &hubRegistry{
		m:              make(map[string]*sessionHub),
		bootID:         bootID,
		idleEvictAfter: idleEvictAfter,

		streamTokenDelay: streamTokenDelayFromEnv(),
	}
	// Start the process-wide counter at 1, not 0, so every hub's head — and
	// therefore every seq a session_snapshot / catch_up_complete /
	// session_started frame reports — is >= 1, as the contract requires
	// (seq minimum: 1). The first frame any session ever publishes is seq 2.
	// The §3.2 guarantee is unaffected: publishedTotal stays >= every head
	// ever issued, because each hub's head is its base plus its own publishes.
	r.publishedTotal.Store(1)
	return r
}

// maybeSweepIdle runs evictIdle at most once per hubIdleSweepInterval,
// piggybacked on submit (BE-DESIGN.md §3.2) so idle sessions actually get
// cleaned up in a running gateway without a dedicated background goroutine.
// The rate-limit check itself is a single atomic load on the common
// (not-yet-time) path; the CompareAndSwap ensures that if multiple
// goroutines cross the interval at once, only one of them actually runs the
// (registry-locking) sweep.
func (r *hubRegistry) maybeSweepIdle(now time.Time) {
	last := r.lastEvictSweepUnixNano.Load()
	if now.Sub(time.Unix(0, last)) < hubIdleSweepInterval {
		return
	}
	if !r.lastEvictSweepUnixNano.CompareAndSwap(last, now.UnixNano()) {
		return // a concurrent caller already claimed this sweep
	}
	r.evictIdle(now)
}

// getOrCreate returns the hub for id, creating one if none exists. A newly
// created hub's first seq is registry.publishedTotal+1, which — because
// publishedTotal only ever increases and is never reset by eviction — is
// guaranteed strictly greater than any seq that session ever issued in this
// process's lifetime (§3.2).
func (r *hubRegistry) getOrCreate(id string) *sessionHub {
	r.mu.RLock()
	h := r.m[id]
	r.mu.RUnlock()
	if h != nil {
		return h
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.m[id]; existing != nil {
		return existing
	}
	start := r.publishedTotal.Load()
	h = &sessionHub{
		id:         id,
		base:       start,
		head:       start,
		lowSeq:     start + 1,
		conns:      make(map[hubConn]struct{}),
		lastActive: time.Now(),
		registry:   r,
	}
	r.m[id] = h
	return h
}

// lookup returns the hub for id without creating one.
func (r *hubRegistry) lookup(id string) *sessionHub {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.m[id]
}

// evictIdle deletes every hub that has no bound connection, an empty inbox,
// and has been idle (by lastActive) for at least idleEvictAfter as of now.
// It is the only path that removes a hub from the registry; the counter
// (publishedTotal) is untouched by eviction, which is what keeps §3.2's
// monotonicity guarantee intact across re-creation.
//
// It is also gated on the hub's active-turn projection being empty (no
// unfinished turn, no open delegate span — §3.2).
func (r *hubRegistry) evictIdle(now time.Time) []string {
	evicted := r.evictIdleLocked(now)
	if len(evicted) > 0 && r.onEvict != nil {
		go r.onEvict(evicted)
	}
	return evicted
}

// evictIdleLocked is evictIdle's sweep, under the registry lock.
func (r *hubRegistry) evictIdleLocked(now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var evicted []string
	for id, h := range r.m {
		h.mu.Lock()
		bound := len(h.conns)
		idleFor := now.Sub(h.lastActive)
		h.inboxMu.Lock()
		inboxEmpty := len(h.inbox) == 0 && !h.draining
		h.inboxMu.Unlock()
		// BE-DESIGN.md §3.2: never evict a hub whose turn or delegate span is
		// still unfinished — its projection is what a later snapshot needs.
		eligible := bound == 0 && inboxEmpty && idleFor >= r.idleEvictAfter && !h.proj.active()
		journalBytes := h.journalBytes
		if eligible {
			h.evicted = true
		}
		h.mu.Unlock()
		if eligible {
			delete(r.m, id)
			r.globalJournalBytes.Add(-int64(journalBytes))
			evicted = append(evicted, id)
		}
	}
	return evicted
}

// enforceGlobalBudget drops whole journals (never the hub itself, never its
// counters) of the least-recently-active hubs first until the global journal
// byte budget is satisfied (§3.1 "Journal, all sessions"). Dropping a
// journal sets lowSeq = head+1, which is exactly the "stale cursor ⇒
// snapshot" outcome for any tab that later reattaches to that session.
func (r *hubRegistry) enforceGlobalBudget() {
	if r.globalJournalBytes.Load() <= hubGlobalJournalMaxBytes {
		return
	}
	r.mu.RLock()
	hubs := make([]*sessionHub, 0, len(r.m))
	for _, h := range r.m {
		hubs = append(hubs, h)
	}
	r.mu.RUnlock()

	sort.Slice(hubs, func(i, j int) bool {
		hubs[i].mu.Lock()
		ti := hubs[i].lastActive
		hubs[i].mu.Unlock()
		hubs[j].mu.Lock()
		tj := hubs[j].lastActive
		hubs[j].mu.Unlock()
		return ti.Before(tj)
	})

	for _, h := range hubs {
		if r.globalJournalBytes.Load() <= hubGlobalJournalMaxBytes {
			return
		}
		h.mu.Lock()
		freed := h.journalBytes
		h.journal = nil
		h.journalBytes = 0
		h.lowSeq = h.head + 1
		h.mu.Unlock()
		r.globalJournalBytes.Add(-int64(freed))
	}
}

// submit is the only entry point for session-scoped conversation frames
// (BE-DESIGN.md §1.1's (*WSHandler).submit). It appends to the hub's inbox
// under inboxMu and starts a drainer goroutine if one is not already
// running. It never blocks on the network and never drops.
func (h *sessionHub) submit(frame []byte) {
	h.inboxMu.Lock()
	h.inbox = append(h.inbox, hubIntent{frame: frame})
	start := !h.draining
	if start {
		h.draining = true
	}
	h.inboxMu.Unlock()
	if start {
		go h.drain()
	}
	if h.registry != nil {
		h.registry.maybeSweepIdle(time.Now())
	}
}

// drain runs only while the inbox is non-empty (BE-DESIGN.md §1.1). It pops
// intents in FIFO order and publishes each one. Because a new submit() while
// drain is running simply appends to h.inbox and does NOT start a second
// goroutine (draining stays true), there is always at most one drainer per
// hub, and delivery order equals submit order.
func (h *sessionHub) drain() {
	for {
		h.inboxMu.Lock()
		if len(h.inbox) == 0 {
			h.draining = false
			h.inboxMu.Unlock()
			return
		}
		batch := h.inbox
		h.inbox = nil
		h.inboxMu.Unlock()

		for _, it := range batch {
			h.publish(it.frame)
		}
	}
}

// publish is (*sessionHub).publishLocked in the design doc's naming — here
// split into the public "take the lock" entry point (publish) so tests can
// call it directly without going through submit/drain when they want
// synchronous, ordered publication.
//
// Under h.mu: increment head, splice the new seq into the frame JSON, append
// to the journal (trimming under retention if needed), update lastActive,
// bump the registry's global published-frame counter, and enqueue the bytes
// to every currently-bound connection. No network I/O happens here — enqueue
// only appends to the connection's own queue (BE-DESIGN.md §2, §2.1).
func (h *sessionHub) publish(frame []byte) uint64 {
	seq, _ := h.publishBytes(frame)
	return seq
}

// publishBytes publishes a frame that does not affect the active-turn
// projection and returns its seq and the exact seq-stamped bytes that were
// journaled and delivered.
func (h *sessionHub) publishBytes(frame []byte) (uint64, []byte) {
	return h.publishMeta(hubFrameMeta{}, frame)
}

// publishMeta is the one publish implementation: under h.mu it numbers the
// frame, journals it, updates the active-turn projection from meta, and
// appends the seq-stamped bytes to every bound connection's own queue
// (never blocking, never dropping — a connection too far behind is closed
// with 4008 by its own queue and simply unbound here).
func (h *sessionHub) publishMeta(meta hubFrameMeta, frame []byte) (uint64, []byte) {
	h.mu.Lock()
	if h.evicted && h.registry != nil {
		h.mu.Unlock()
		return h.registry.getOrCreate(h.id).publishMeta(meta, frame)
	}
	h.head++
	seq := h.head
	out := spliceSeq(frame, seq)
	h.appendJournalLocked(seq, out)
	h.proj.update(meta, frame, h.id)
	h.lastActive = time.Now()

	var overflowed []hubConn
	for c := range h.conns {
		if !c.enqueue(out) {
			overflowed = append(overflowed, c)
		}
	}
	for _, c := range overflowed {
		delete(h.conns, c)
	}
	onOverflow := h.onOverflow
	h.mu.Unlock()

	if h.registry != nil {
		h.registry.publishedTotal.Add(1)
		// enforceGlobalBudget locks hubs (including, potentially, this same
		// hub) to sort them by lastActive — it MUST run after h.mu is
		// released above, or a hub whose own journal just pushed the global
		// total over budget would try to re-lock its own already-held mutex
		// and deadlock permanently. Found by TestHub_H15_GlobalBudgetDropsLRUJournal
		// during development; see that test's history for the RED receipt.
		h.registry.enforceGlobalBudget()
		// BE-DESIGN.md §3.2: piggyback the rate-limited idle-eviction sweep
		// on the publish path every producer shares.
		h.registry.maybeSweepIdle(time.Now())
	}
	if onOverflow != nil {
		for _, c := range overflowed {
			onOverflow(c)
		}
	}
	return seq, out
}

// appendJournalLocked appends entry (seq, bytes) to the journal and applies
// the per-session retention bound (§3.1): a batch trim (down to
// hubJournalTrimFraction of the cap) when either the frame count or the byte
// count exceeds its cap. Caller holds h.mu.
func (h *sessionHub) appendJournalLocked(seq uint64, out []byte) {
	entry := journalEntry{seq: seq, bytes: out}
	h.journal = append(h.journal, entry)
	h.journalBytes += len(out)
	if h.registry != nil {
		h.registry.globalJournalBytes.Add(int64(len(out)))
	}

	if len(h.journal) > hubJournalMaxFrames || h.journalBytes > hubJournalMaxBytes {
		targetFrames := int(float64(hubJournalMaxFrames) * hubJournalTrimFraction)
		targetBytes := int(float64(hubJournalMaxBytes) * hubJournalTrimFraction)
		freed := 0
		for (len(h.journal) > targetFrames || h.journalBytes > targetBytes) && len(h.journal) > 0 {
			freed += len(h.journal[0].bytes)
			h.journalBytes -= len(h.journal[0].bytes)
			h.journal = h.journal[1:]
		}
		if h.registry != nil && freed > 0 {
			h.registry.globalJournalBytes.Add(-int64(freed))
		}
	}
	if len(h.journal) > 0 {
		h.lowSeq = h.journal[0].seq
	} else {
		h.lowSeq = h.head + 1
	}
	// NOTE: enforceGlobalBudget is deliberately NOT called from here. This
	// method runs under h.mu (caller-held); enforceGlobalBudget locks every
	// hub in the registry (by lastActive) to decide what to trim, which can
	// include THIS hub. Calling it while h.mu is already held would
	// self-deadlock the moment this hub's own growth is what pushed the
	// global total over budget. publish() calls it after releasing h.mu.
}

// attachResult is what bind returns: enough for the caller to build and
// send session_state / the tail or snapshot / catch_up_complete
// (BE-DESIGN.md §4.1 steps A4-A6). It mirrors the design's servability
// reasons (§3.3) rather than a bare bool, since the reason rides the wire on
// the snapshot frame.
type attachResult struct {
	Servable bool
	Reason   string // "" if servable; else boot_mismatch|retention_exceeded|cursor_ahead|unknown_position
	Head     uint64
	Tail     [][]byte           // only populated if Servable
	Proj     []projSnapshotItem // only populated if not Servable
	// evicted: the hub was evicted before the bind; nothing was bound and
	// the caller must retry against the registry's live hub.
	evicted bool
}

const (
	reasonBootMismatch      = "boot_mismatch"
	reasonRetentionExceeded = "retention_exceeded"
	reasonCursorAhead       = "cursor_ahead"
	reasonUnknownPosition   = "unknown_position"
)

// holdable is implemented by a connection that can buffer live frames while
// its attach is being answered (*wsConn — BE-DESIGN.md §4.1 A4 "hold mode").
type holdable interface {
	startHold()
}

// bind is handleAttachSession's A2-A4 (BE-DESIGN.md §4.1): it decides
// servability, binds the connection, switches it into hold mode (live frames
// published from now on queue behind the catch-up), and reads the head and
// either the journal tail or the projection — all under the SAME critical
// section every publish takes. That is what makes the catch-up gap-free and
// duplicate-free (§4.2): every frame with seq <= Head is in the tail or
// reflected in the snapshot, and every frame with seq > Head reaches the
// connection's held queue.
func (h *sessionHub) bind(c hubConn, sinceSeq *int64, bootID *string, registryBootID string) attachResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.evicted {
		return attachResult{evicted: true}
	}

	h.conns[c] = struct{}{}
	if hc, ok := c.(holdable); ok {
		hc.startHold()
	}
	h.lastActive = time.Now()
	res := attachResult{Head: h.head}

	switch {
	case sinceSeq == nil || bootID == nil:
		res.Servable = false
		res.Reason = reasonUnknownPosition
	case *bootID != registryBootID:
		res.Servable = false
		res.Reason = reasonBootMismatch
	case *sinceSeq < int64(h.lowSeq)-1:
		res.Servable = false
		res.Reason = reasonRetentionExceeded
	case *sinceSeq > int64(h.head):
		res.Servable = false
		res.Reason = reasonCursorAhead
	default:
		res.Servable = true
		tail := make([][]byte, 0, len(h.journal))
		for _, e := range h.journal {
			if e.seq > uint64(*sinceSeq) {
				tail = append(tail, e.bytes)
			}
		}
		res.Tail = tail
	}
	if !res.Servable {
		res.Proj = h.proj.snapshot()
	}
	return res
}

// bindLive binds c for live delivery only — no catch-up (a connection that
// just minted this session, or sent a message on it without attaching
// first). It returns the head at bind time: the connection's cursor, since
// every later publish reaches it.
func (h *sessionHub) bindLive(c hubConn) (head uint64, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.evicted {
		return 0, false
	}
	h.conns[c] = struct{}{}
	h.lastActive = time.Now()
	return h.head, true
}

// forgetTurn removes an abandoned turn's items from the active-turn
// projection (see activeTurnProjection.forgetTurn).
func (h *sessionHub) forgetTurn(turnID string) {
	h.mu.Lock()
	h.proj.forgetTurn(turnID)
	h.mu.Unlock()
}

// isEvicted reports whether evictIdle removed this hub from its registry.
func (h *sessionHub) isEvicted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.evicted
}

// unbind removes c from the hub's bound-connection set (BE-DESIGN.md §4.1
// A2, "unbind wc from its previous hub").
func (h *sessionHub) unbind(c hubConn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

// boundCount reports how many connections are currently bound — used by
// tests and by eviction bookkeeping.
func (h *sessionHub) boundCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// snapshotHead returns the current head (last issued seq) without binding
// anything.
func (h *sessionHub) snapshotHead() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.head
}

// spliceSeq inserts `"seq":<n>` as an additional top-level key into a
// compact JSON object's bytes, immediately before the closing brace
// (BE-DESIGN.md §1.1's publishLocked: "splice \"seq\":head into the JSON").
// It assumes frame is a compact (no trailing whitespace) JSON object, which
// holds for every generated frame type's json.Marshal output. A frame that
// already carries a "seq" key (there should never be one before publish)
// gets a second, later "seq" key — later keys win under encoding/json's own
// unmarshal semantics, so this stays correct even in that defensive case.
func spliceSeq(frame []byte, seq uint64) []byte {
	trimmed := bytes.TrimRight(frame, " \t\r\n")
	if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' {
		// Not a JSON object we can splice into — return unchanged rather
		// than corrupt it. Callers only ever pass json.Marshal output of a
		// generated frame struct, so this path is defensive only.
		return frame
	}
	body := trimmed[:len(trimmed)-1]
	// Capacity from the body length alone (append grows it once for the
	// short `,"seq":N}` suffix) — no length arithmetic that could overflow.
	out := make([]byte, 0, len(body))
	out = append(out, body...)
	if len(bytes.TrimSpace(body)) > len(`{`) {
		out = append(out, ',')
	}
	out = append(out, `"seq":`...)
	out = strconv.AppendUint(out, seq, 10)
	out = append(out, '}')
	return out
}
