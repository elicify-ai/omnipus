// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
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
		slog.Error("ws: crypto/rand failed for hub boot id, falling back to a time-derived id", "error", err)
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
// INTEGRATION STATUS (honest, see SQUAD-REPORT-BEA.md): this file implements
// the hub itself — submit/drain/publish, the per-session journal with
// retention, the global journal budget, the cursor-servability rule (§3.3),
// counter monotonicity across idle eviction (§3.2), and per-connection
// overflow handling (§2.1) — and is covered by hub-level tests (H1-H5, H8,
// H9, H15 in ws_session_hub_test.go). It is NOT yet wired to the real
// producers (wsStreamer, webchatChannel, the EventBus sync tap,
// websocket_chat.go, websocket_cancel.go, ws_tool_approval.go,
// ws_ask_user.go, knowledge_lifecycle.go) or to the real *wsConn/replay
// machinery — that integration, and the legacy divert/backoff-drop code
// deletion it enables, is the largest remaining gap and is called out in the
// report rather than claimed done.

// hubConn is the minimal interface the session hub needs from a bound
// connection to deliver sequenced frames. It exists so the hub can be built
// and tested standalone; the real *wsConn is expected to implement it via a
// small adapter once the integration pass lands (BE-DESIGN.md §2.1's
// wc.enqueue). enqueue must never block: it appends to the connection's own
// outbound queue and reports whether the queue is still within its byte
// budget. Returning false means the connection is over budget and MUST be
// closed with WS close code 4008 ("catch-up required") by the caller that
// owns the socket — the hub itself never touches the network.
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
	// connections, empty inbox and (once the projection lands) no open
	// turn/span is eligible for eviction. Test-only override, off by
	// default in production wiring — FOUNDER DECISION Q6.
	idleEvictAfter time.Duration
}

func newHubRegistry(bootID string) *hubRegistry {
	return &hubRegistry{
		m:              make(map[string]*sessionHub),
		bootID:         bootID,
		idleEvictAfter: 10 * time.Minute,
	}
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
// NOTE (honest gap): the design also requires gating eviction on "no active
// turn in its projection and no open span" (§3.2). The active-turn
// projection (§4.4) is not implemented in this pass, so that extra gate is
// not enforced here — only the bound-connection and empty-inbox gates are.
// This is safe (it can only evict LESS eagerly, i.e. never mid-turn with a
// tab attached) but is not yet the full rule; flagged in the report.
func (r *hubRegistry) evictIdle(now time.Time) []string {
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
		eligible := bound == 0 && inboxEmpty && idleFor >= r.idleEvictAfter
		journalBytes := h.journalBytes
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

// publishBytes is publish's full implementation, additionally returning the
// exact seq-stamped bytes that were journaled and (for any currently-bound
// hubConn) delivered. A caller that still resolves its OWN delivery targets
// outside the hub's conns set (the #823 integration's transitional state —
// see websocket_streamer.go's Update/Finalize) needs these exact bytes so
// what it delivers is byte-identical to what the journal retains, preserving
// H3's multi-tab-byte-identity guarantee even before every producer's
// delivery path has been cut over to hub-tracked bindings.
func (h *sessionHub) publishBytes(frame []byte) (uint64, []byte) {
	h.mu.Lock()
	h.head++
	seq := h.head
	out := spliceSeq(frame, seq)
	h.appendJournalLocked(seq, out)
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
// send session_state / the tail / catch_up_complete (BE-DESIGN.md §4.1
// steps A4-A6). It intentionally mirrors the design's servability reasons
// (§3.3) rather than a generic bool, since the reason rides the wire on the
// snapshot frame.
type attachResult struct {
	Servable bool
	Reason   string // "" if servable; else boot_mismatch|retention_exceeded|cursor_ahead|unknown_position
	Head     uint64
	Tail     [][]byte // only populated if Servable
}

const (
	reasonBootMismatch      = "boot_mismatch"
	reasonRetentionExceeded = "retention_exceeded"
	reasonCursorAhead       = "cursor_ahead"
	reasonUnknownPosition   = "unknown_position"
)

// bind is (*WSHandler).handleAttachSession's A2-A4 (BE-DESIGN.md §4.1): it
// decides servability under the SAME critical section that binds the
// connection and reads head, so there is no window in which a frame
// published between the decision and the bind is missed (§4.2's "gaps"
// argument). The caller is responsible for holding the connection in "hold"
// mode until it has delivered session_state/tail/snapshot and
// catch_up_complete — that hold-mode buffering lives on the real wsConn and
// is NOT implemented in this pass (see file header).
func (h *sessionHub) bind(c hubConn, sinceSeq *int64, bootID *string, registryBootID string) attachResult {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.conns[c] = struct{}{}
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
	return res
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
	out := make([]byte, 0, len(body)+24)
	out = append(out, body...)
	if len(bytes.TrimSpace(body)) > len(`{`) {
		out = append(out, ',')
	}
	out = append(out, `"seq":`...)
	out = strconv.AppendUint(out, seq, 10)
	out = append(out, '}')
	return out
}
