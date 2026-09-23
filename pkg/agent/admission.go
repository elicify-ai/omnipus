// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ====================== FR-068: the live memory gate ======================
//
// Agent admission and the browser pool are ONE mechanism. They read the same
// accessor (config.MemoryPressureHigh), compare against the same threshold
// (config.memoryPressureRatioThreshold — there is exactly one, and nothing
// here defines a second), and carry the same reason code
// (config.ReasonMemoryPressure) when they refuse.
//
// What they do NOT share is the RESPONSE, and that difference is deliberate
// (FR-075). The browser pool refuses to GROW: it will not start a second
// Chrome. Agent admission also refuses to grow, but from a floor of two —
// because an agent turn is the product, and a host that cannot report its
// own memory must still be able to run one. Refusing to RUN on an
// unmeasurable host would make every Windows and gVisor deployment useless
// for the sake of a reading nobody can take.

// unmeasurableHostAgentFloor is how many concurrent agent turns are admitted
// on a host whose memory cannot be measured, or one already above the
// pressure threshold.
//
// TWO, not one and not zero. One would serialize the whole gateway on a
// Windows box; zero would make it refuse to work at all. Two lets a user's
// turn run while a background task or a delegated child runs alongside it,
// which is the smallest number at which the product is still recognisably
// itself. The third concurrent turn is refused, naming memory — refuse to
// GROW, never refuse to RUN.
//
// This is NOT a per-agent memory budget in disguise. It is a count, chosen
// for what it preserves, and nothing multiplies it by a byte figure.
const unmeasurableHostAgentFloor = 2

// memoryAdmissionCap reports the cap the live memory mechanism imposes on
// concurrent agent turns right now, and whether it imposes one at all.
//
// It is the ONLY memory-derived input to admission, and it reads
// config.MemoryPressureHigh — the same accessor and threshold the browser
// pool reads. There is no per-agent byte cost anywhere in this path; the old
// availableRAM/3.5-MB-per-agent formula is deleted, not relocated.
//
//   - measured, headroom available  -> (0, false): memory imposes no cap and
//     the operator's configured value (or the physical backstop) governs.
//   - measured, above the threshold -> (floor, true): refuse to grow.
//   - not measurable at all         -> (floor, true): refuse to grow.
//
// The last two collapse to the same answer on purpose. "I know this host is
// short of memory" and "I cannot tell whether this host is short of memory"
// are the same instruction to an admission gate: do not add load.
func memoryAdmissionCap() (int, bool) {
	high, ok := config.MemoryPressureHigh()
	if !ok || high {
		return unmeasurableHostAgentFloor, true
	}
	return 0, false
}

// applyMemoryCap folds the live memory cap into a configured cap, returning
// the cap to enforce and whether MEMORY is the binding constraint (which is
// what decides whether a refusal names memory).
//
// Memory can only ever LOWER the cap. An operator who configured 1 gets 1 on
// a healthy host and 1 on an unmeasurable one; the floor is a ceiling for
// the memory mechanism, never a floor that raises an operator's explicit
// choice above what they asked for.
func applyMemoryCap(configured int) (effective int, memoryBinding bool) {
	memCap, imposed := memoryAdmissionCap()
	if !imposed || memCap >= configured {
		return configured, false
	}
	return memCap, true
}

// AdmissionController is a soft-cap gate for concurrent session workers.
//
// Phase 1: gates inbound user-message dispatch only. The counter tracks unique
// active scopes (one per spawned session worker) — not per-turn, so a single
// chatty session cannot pin admission slots indefinitely. Subagent spawn and
// task-executor dispatch paths are gated separately by TaskExecutor's own
// dispatchSema (pkg/agent/dispatch_sema.go), the "single authority" for agent
// concurrency (concurrency-gate consolidation, 2026-08-04) — see resolveCap's
// doc comment below for how the two stay aligned.
type AdmissionController struct {
	// softCap is the fixed cap used when resolveCap is nil — the path taken
	// by direct-int test construction (newAdmissionController). Production
	// wiring never uses this field; see resolveCap.
	softCap int
	// resolveCap, when non-nil, is consulted FRESH on every effectiveCap()
	// call instead of softCap. Production wiring (NewAgentLoop) always sets
	// this to a closure reading al.GetConfig().Performance.
	// EffectiveMaxParallelAgents() live — the SAME central authority
	// TaskExecutor's dispatch semaphore uses (pkg/config's
	// PerformanceConfig.EffectiveMaxParallelAgents) — so this gate can never
	// silently impose an independent, smaller cap (the pre-fix defect: a
	// hardcoded runtime.NumCPU()*4 rejected sessions well under the
	// operator-advertised max_parallel_agents, with zero visibility — see
	// docs/internal/uat/parallelism-cost-browser-bash-2026-08-04.md finding
	// #1). Resolving live on every check (rather than caching a value
	// resolved once at construction) is also the fix for the auto-detected
	// default's own boot-time-read caveat: see
	// pkg/config's availableRAMBytes doc comment — a transient low reading
	// right after boot self-corrects the moment the host's real availability
	// changes, with no restart or explicit resize required.
	resolveCap   func() int
	mu           sync.Mutex
	activeScopes map[string]struct{}
}

// newAdmissionController returns a controller with a FIXED cap: softCap if
// positive, otherwise a defensive floor of 1 (never a hardcoded
// hardware-derived guess — see newAdmissionControllerWithResolver for the
// production, live-resolved path, which is what NewAgentLoop actually uses).
// This constructor exists for direct unit tests of TryAdmit's admission
// logic against a known, stable cap.
func newAdmissionController(softCap int) *AdmissionController {
	if softCap <= 0 {
		softCap = 1
	}
	return &AdmissionController{
		softCap:      softCap,
		activeScopes: make(map[string]struct{}),
	}
}

// newAdmissionControllerWithResolver returns a controller whose cap is
// resolved LIVE via resolveCap on every admission check, rather than fixed
// at construction. This is the production constructor (NewAgentLoop wires
// resolveCap to al.GetConfig().Performance.EffectiveMaxParallelAgents()) —
// see resolveCap's doc comment on the AdmissionController struct for why
// live resolution, rather than a cached value, is required.
func newAdmissionControllerWithResolver(resolveCap func() int) *AdmissionController {
	return &AdmissionController{
		resolveCap:   resolveCap,
		softCap:      1, // defensive floor, only reachable if resolveCap ever returns <= 0
		activeScopes: make(map[string]struct{}),
	}
}

// effectiveCap returns the CONFIGURED cap to enforce right now:
// resolveCap()'s current value when set and positive, otherwise the fixed
// softCap.
//
// It deliberately does NOT fold in the live memory gate. SoftCap() reports
// this value to tests and observability, and "the cap you configured" and
// "what memory will let you have this second" are two different facts: a
// panel or a log line that showed the second under the name of the first
// would make an operator think their setting had been silently lowered,
// which is the ADR-037 anti-pattern this project bans. The memory gate is
// applied where it is ACTED on — inside TryAdmitWithReason — and it names
// itself when it refuses.
func (a *AdmissionController) effectiveCap() int {
	if a.resolveCap != nil {
		if c := a.resolveCap(); c > 0 {
			return c
		}
	}
	return a.softCap
}

// admissionCapWithReason is the cap actually enforced on an admission
// decision: the configured cap, lowered by the live memory gate when memory
// is the tighter constraint, plus whether memory is what is binding.
func (a *AdmissionController) admissionCapWithReason() (int, bool) {
	return applyMemoryCap(a.effectiveCap())
}

// TryAdmit atomically claims a slot for scope. Returns (true, release) when
// the scope is admitted; release MUST be called (typically via defer) when
// the scope's worker exits.
//
// If scope is already active (follow-up turn in an existing session), the
// call always succeeds without consuming an additional slot — the slot was
// already claimed when the worker was first spawned.
//
// Returns (false, nil) when the effective cap (see effectiveCap) is reached
// and scope is a new scope.
func (a *AdmissionController) TryAdmit(scope string) (bool, func()) {
	ok, _, release := a.TryAdmitWithReason(scope)
	return ok, release
}

// TryAdmitWithReason is TryAdmit plus the reason a refusal happened.
//
// reason is "" on success and on a refusal caused by the operator's own
// configured cap; it is config.ReasonMemoryPressure when the live memory
// gate is what refused. Callers that surface a refusal to a model or an
// operator MUST use this form — "the cap is reached" and "this machine is
// out of memory" send a caller to two completely different remedies, and
// only one of them exists on an unmeasurable host (there is no cap to raise).
func (a *AdmissionController) TryAdmitWithReason(scope string) (bool, string, func()) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, alreadyActive := a.activeScopes[scope]; alreadyActive {
		// Existing scope — follow-up turn, always admitted, no new slot consumed.
		return true, "", func() {}
	}

	limit, memoryBinding := a.admissionCapWithReason()
	if len(a.activeScopes) >= limit {
		if memoryBinding {
			logMemoryAdmissionRefusalOnce(limit)
			return false, config.ReasonMemoryPressure, nil
		}
		return false, "", nil
	}

	a.activeScopes[scope] = struct{}{}
	release := func() {
		a.mu.Lock()
		delete(a.activeScopes, scope)
		a.mu.Unlock()
	}
	return true, "", release
}

// ActiveScopes returns the current count of active scopes (worker goroutines
// that hold an admission slot). Used in tests and observability.
func (a *AdmissionController) ActiveScopes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.activeScopes)
}

// SoftCap returns the cap currently being enforced — the live-resolved value
// when a resolver is configured (production wiring), otherwise the fixed
// softCap. Safe to call without holding a.mu (effectiveCap only reads
// resolveCap/softCap, never activeScopes).
func (a *AdmissionController) SoftCap() int {
	return a.effectiveCap()
}

var lastMemoryAdmissionRefusalLogged atomic.Bool

func logMemoryAdmissionRefusalOnce(effectiveCap int) {
	if lastMemoryAdmissionRefusalLogged.Swap(true) {
		return
	}
	slog.Warn("agent admission is bound by memory, not by configuration — concurrent agent turns are held at a floor while this host is under memory pressure or its memory cannot be measured",
		"reason", config.ReasonMemoryPressure,
		"effective_concurrent_cap", effectiveCap)
}

func resetMemoryAdmissionRefusalLogForTest() {
	lastMemoryAdmissionRefusalLogged.Store(false)
}

// ====================== ADR-091 I-3/D9: the steered-turn admission loop ======================
//
// A SEPARATE gate from AdmissionController/RootDelegationAdmission above: those
// two govern the delegate() tool's SPAWN attempt (refuse immediately, no
// queue). steerAdmission governs steer.SessionLauncher.Dispatch's admission
// decision (I-2/I-3): the counter tracks TURNS EXECUTING right now — not
// sessions in a "running" lifecycle state (D9) — and a session that cannot
// be admitted is QUEUED, never refused, started in launch order (FIFO) as
// slots free (turn end -> steerAdmission.release -> drainSteerQueue).

// steerQueueEntry is one FIFO-queued dispatch awaiting a free admission
// slot.
type steerQueueEntry struct {
	sessionID  string
	generation int
}

// steerAdmission is the turn-counting admission gate (I-3 "Admission"). cap
// is resolved LIVE on every tryAdmit via resolveCap (mirrors
// AdmissionController.resolveCap's live-resolution rationale — an operator's
// PUT /api/v1/performance write must reach this gate without a restart).
type steerAdmission struct {
	mu         sync.Mutex
	active     map[string]int // sessionKey -> generation, only while THIS gate admitted the turn
	queue      []steerQueueEntry
	resolveCap func() int
}

func newSteerAdmission(resolveCap func() int) *steerAdmission {
	return &steerAdmission{active: make(map[string]int), resolveCap: resolveCap}
}

// tryAdmit atomically decides running vs queued for sessionID/gen (I-2
// "Dispatch — not Launch — decides atomically under the admission lock").
// Returns (true, 0) when admitted; (false, 1-based position) when queued —
// never blocks.
func (g *steerAdmission) tryAdmit(sessionID string, gen int) (admitted bool, queuePosition, concurrencyLimit int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	effectiveCap := 1
	if g.resolveCap != nil {
		if c := g.resolveCap(); c > 0 {
			effectiveCap = c
		}
	}
	if len(g.active) >= effectiveCap {
		g.queue = append(g.queue, steerQueueEntry{sessionID: sessionID, generation: gen})
		return false, len(g.queue), effectiveCap
	}
	g.active[sessionID] = gen
	return true, 0, effectiveCap
}

// release frees sessionID's admission slot (a turn ended — D9: "a session
// whose turn has ended holds no slot") and pops the oldest queued entry, if
// any, for the caller to dispatch next (FIFO). A release for a sessionID
// this gate never admitted (e.g. a non-steered turn's ordinary Finish, or a
// session that was queued rather than admitted) is a harmless no-op.
func (g *steerAdmission) release(sessionID string, generation int) (next steerQueueEntry, hasNext bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if activeGeneration, ok := g.active[sessionID]; !ok || activeGeneration != generation {
		return steerQueueEntry{}, false
	}
	delete(g.active, sessionID)
	if len(g.queue) == 0 {
		return steerQueueEntry{}, false
	}
	next = g.queue[0]
	g.queue = g.queue[1:]
	g.active[next.sessionID] = next.generation
	return next, true
}

// hasReservation reports whether release promoted this exact generation into
// the active set. A promoted dispatch consumes that reservation by keeping it
// for the lifetime of the turn; it must not call tryAdmit and requeue itself.
func (g *steerAdmission) hasReservation(sessionID string, generation int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active[sessionID] == generation
}

// removeQueued rolls back one exact queue entry after its queued-state write
// fails. It never changes active reservations and therefore cannot release a
// different turn's slot.
func (g *steerAdmission) removeQueued(sessionID string, generation int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, entry := range g.queue {
		if entry.sessionID == sessionID && entry.generation == generation {
			g.queue = append(g.queue[:i], g.queue[i+1:]...)
			return
		}
	}
}

// removeQueuedSession drops EVERY queued entry for sessionID, whatever
// generation each carries, and reports how many it removed. Unlike
// removeQueued it is not a rollback of one failed write: it is what the Stop
// cascade uses (steer_delegate_cancel.go::cancelDelegatedSubtree) to take a
// cancelled session out of the start queue.
//
// reserveDispatch would refuse the promotion anyway, so this is not what
// makes a stopped session safe. It is what makes the queue HONEST: a
// cancelled worker left sitting in it still counts toward every queue
// position the tool reports to a model, and still shows in the side panel as
// a session about to start.
//
// Active reservations are never touched, so this can never release a running
// turn's slot.
func (g *steerAdmission) removeQueuedSession(sessionID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	kept := g.queue[:0]
	removed := 0
	for _, entry := range g.queue {
		if entry.sessionID == sessionID {
			removed++
			continue
		}
		kept = append(kept, entry)
	}
	g.queue = kept
	return removed
}

// activeCount reports the number of turns this gate currently holds a slot
// for — test/observability seam.
func (g *steerAdmission) activeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.active)
}

// queueLen reports the number of sessions currently queued — test/
// observability seam.
func (g *steerAdmission) queueLen() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.queue)
}

// steerAdmissionRegistry maps *AgentLoop -> its one steerAdmission gate.
// A package-level registry, not an AgentLoop struct field, because loop.go
// is line-count-pinned (scripts/budgets/files.txt) and cannot grow by even
// one field declaration; every other ADR-091 per-AgentLoop steering state
// (this gate) is threaded through this side table instead. Entries are
// never removed — AgentLoop instances are process-lifetime singletons in
// production, and the handful a test suite constructs and discards is a
// bounded, acceptable leak (mirrors how Go program-lifetime caches are
// commonly implemented; there is no AgentLoop.Close hook this could
// unregister from today).
var (
	steerAdmissionRegistry   = map[*AgentLoop]*steerAdmission{}
	steerAdmissionRegistryMu sync.Mutex
)

// steerAdmission returns al's steered-turn admission gate, constructing it
// on first use with a resolver reading the SAME central authority
// (Performance.EffectiveMaxParallelAgents) AdmissionController and
// RootDelegationAdmission already read, so all three concurrency gates
// stay aligned to one operator-configured number.
func (al *AgentLoop) steerAdmission() *steerAdmission {
	steerAdmissionRegistryMu.Lock()
	defer steerAdmissionRegistryMu.Unlock()
	if g, ok := steerAdmissionRegistry[al]; ok {
		return g
	}
	g := newSteerAdmission(func() int {
		n, _ := al.GetConfig().Performance.EffectiveMaxParallelAgents()
		return n
	})
	steerAdmissionRegistry[al] = g
	return g
}

// drainSteerQueue is called when a turn ends (turn_exit.go::Finish) —
// releases sessionID's slot and, if a session was waiting, dispatches it
// through the SAME admission path Dispatch itself uses
// (dispatchSteeredSession), started in a goroutine so Finish (which may be
// running inside another turn's own goroutine, e.g. a hard-abort cascade)
// never blocks on the next session's turn.
func (al *AgentLoop) drainSteerQueue(sessionID string, generation int) {
	next, hasNext := al.steerAdmission().release(sessionID, generation)
	if !hasNext {
		return
	}
	go func() {
		if _, err := al.dispatchSteeredSessionReserved(context.Background(), next.sessionID, next.generation); err != nil {
			logger.WarnCF("agent", "steer: drain queue: dispatch of the next queued session failed",
				map[string]any{"session_id": next.sessionID, "generation": next.generation, "error": err.Error()})
		}
	}()
}
