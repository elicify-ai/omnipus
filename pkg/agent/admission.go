// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/steer"
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
	activeScopes map[string]*ordinaryAdmissionOwner
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
		activeScopes: make(map[string]*ordinaryAdmissionOwner),
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
		activeScopes: make(map[string]*ordinaryAdmissionOwner),
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

	owner := a.activeScopes[scope]
	if owner != nil && owner.lease != nil {
		// Only an actual worker lease admits a follow-up without a new slot.
		return true, "", func() {}
	}

	limit, memoryBinding := a.admissionCapWithReason()
	if a.workerCountLocked() >= limit {
		if memoryBinding {
			logMemoryAdmissionRefusalOnce(limit)
			return false, config.ReasonMemoryPressure, nil
		}
		return false, "", nil
	}

	if owner == nil {
		owner = &ordinaryAdmissionOwner{}
		a.activeScopes[scope] = owner
	}
	lease := &ordinaryWorkerLease{held: true}
	owner.lease = lease
	release := func() {
		a.mu.Lock()
		if current := a.activeScopes[scope]; current != nil && current.lease == lease {
			current.lease = nil
			if current.execution == nil {
				delete(a.activeScopes, scope)
			}
		}
		a.mu.Unlock()
	}
	return true, "", release
}

// ActiveScopes returns the current count of active scopes (worker goroutines
// that hold an admission slot). Used in tests and observability.
func (a *AdmissionController) ActiveScopes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workerCountLocked()
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
// slot. runID is the admission's execution identity (execution_identity.go)
// minted and durably stamped BEFORE this entry was enqueued, copied here
// unchanged: the promotion dispatches under the SAME identity — a promoted
// admission never mints a second run_id for the turn it already owns.
// Entries without a genuine identity can be inspected by the queue primitive,
// but production promotion refuses them; it never synthesizes an identity.
type steerQueueEntry struct {
	sessionID   string
	generation  int
	runID       string
	bootSeq     uint64
	disposition *executionDisposition
	// Native pending wake identities belong to this full admission owner.
	wakeInputs []steeringQueueItem
}

func (entry steerQueueEntry) executionClaim() executionClaim {
	return executionClaim{SessionID: entry.sessionID, Generation: entry.generation, RunID: entry.runID, BootSeq: entry.bootSeq}
}

// steerAdmission is the turn-counting admission gate (I-3 "Admission"). cap
// is resolved LIVE on every tryAdmit via resolveCap (mirrors
// AdmissionController.resolveCap's live-resolution rationale — an operator's
// PUT /api/v1/performance write must reach this gate without a restart).
type steerAdmission struct {
	// entryMu covers preparation only: duplicate check, durable stamp and
	// registration/queue insertion. Never held while a turn runs or exits.
	entryMu    sync.Mutex
	mu         sync.Mutex
	active     map[string]steerQueueEntry
	queue      []steerQueueEntry
	resolveCap func() int
	// turns counts the DETACHED goroutines this gate's dispatch front has
	// in flight — the admitted turn itself (steer_launcher.go's
	// `go al.runDispatchedSteeredTurn`) and the promotion of the next
	// queued session (drainSteerQueue, below). Both write through the
	// session, lifecycle, inbox and transcript stores long after the call
	// that started them has returned, so AgentLoop.Close must be able to
	// JOIN them — see drainSteeredTurns. Not part of the admission
	// decision and deliberately outside mu: WaitGroup has its own
	// synchronisation and taking mu around a goroutine's whole lifetime
	// would serialise the gate on it.
	turns sync.WaitGroup
}

func newSteerAdmission(resolveCap func() int) *steerAdmission {
	return &steerAdmission{active: make(map[string]steerQueueEntry), resolveCap: resolveCap}
}

// tryAdmit atomically decides running vs queued for sessionID/gen (I-2
// "Dispatch — not Launch — decides atomically under the admission lock").
// Returns (true, 0) when admitted; (false, 1-based position) when queued —
// never blocks.
//
// The two-argument form is the pre-identity-seam signature the gate's unit
// tests call; production admissions call tryAdmitRun so the queue entry
// carries the stamped run identity.
func (g *steerAdmission) tryAdmit(sessionID string, gen int) (admitted bool, queuePosition, concurrencyLimit int) {
	return g.tryAdmitRun(sessionID, gen, "", 0)
}

// tryAdmitRun is tryAdmit with the admission's execution identity: the
// queue entry it appends copies runID unchanged (execution_identity.go).
func (g *steerAdmission) tryAdmitRun(sessionID string, gen int, runID string, bootSeq uint64) (admitted bool, queuePosition, concurrencyLimit int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	effectiveCap := 1
	if g.resolveCap != nil {
		if c := g.resolveCap(); c > 0 {
			effectiveCap = c
		}
	}
	entry := steerQueueEntry{sessionID: sessionID, generation: gen, runID: runID, bootSeq: bootSeq}
	_, sameSessionActive := g.active[sessionID]
	if len(g.active) >= effectiveCap || sameSessionActive {
		g.queue = append(g.queue, entry)
		return false, len(g.queue), effectiveCap
	}
	g.active[sessionID] = entry
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
	if active, ok := g.active[sessionID]; !ok || active.generation != generation {
		return steerQueueEntry{}, false
	}
	return g.releaseLocked(sessionID)
}

// releaseExecution is the production release boundary. An older same-
// generation run cannot release a replacement's reservation.
func (g *steerAdmission) releaseExecution(claim executionClaim) (steerQueueEntry, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	active, ok := g.active[claim.SessionID]
	if !ok || active.executionClaim() != claim {
		return steerQueueEntry{}, false
	}
	return g.releaseLocked(claim.SessionID)
}

func (g *steerAdmission) releaseLocked(sessionID string) (steerQueueEntry, bool) {
	delete(g.active, sessionID)
	for i, entry := range g.queue {
		if _, alreadyActive := g.active[entry.sessionID]; alreadyActive {
			continue
		}
		g.queue = append(g.queue[:i], g.queue[i+1:]...)
		g.active[entry.sessionID] = entry
		return entry, true
	}
	return steerQueueEntry{}, false
}

// hasReservation reports whether release promoted this exact generation into
// the active set. A promoted dispatch consumes that reservation by keeping it
// for the lifetime of the turn; it must not call tryAdmit and requeue itself.
func (g *steerAdmission) hasReservation(sessionID string, generation int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.active[sessionID]
	return ok && entry.generation == generation
}

func (g *steerAdmission) hasExecutionReservation(claim executionClaim) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.active[claim.SessionID]
	return ok && entry.executionClaim() == claim
}

// removeQueuedExecution rolls back only the admission whose state write failed.
func (g *steerAdmission) removeQueuedExecution(claim executionClaim) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, entry := range g.queue {
		if entry.executionClaim() == claim {
			g.queue = append(g.queue[:i], g.queue[i+1:]...)
			return
		}
	}
}

// removeQueuedSession drops EVERY queued entry for sessionID, whatever
// generation each carries, and reports how many it removed. It is not a
// rollback of one failed write: it is what the Stop cascade uses
// (steer_delegate_cancel.go::cancelDelegatedSubtree) to take a
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

// drainSteerQueue releases sessionID's slot and, if a session was waiting,
// dispatches it through the SAME admission path Dispatch itself uses
// (dispatchSteeredSessionReserved). It is called explicitly by whoever
// claimed the slot — steer_launcher.go::runDispatchedSteeredTurn's deferred
// release on the delegate front, loop_inbound.go::processSteeredSystemWake
// on the wake front — never from turn_exit.go::Finish, which has no
// back-reference to release through for a steered turn (see Finish's own
// comment on why the slot is not released there). The dispatch of the next
// queued session runs in a goroutine so the caller never blocks on it.
func (al *AgentLoop) drainSteerQueue(claim executionClaim) {
	next, hasNext := al.steerAdmission().releaseExecution(claim)
	if hasNext {
		al.promoteSteeredExecution(next)
	}
}

func (al *AgentLoop) promoteSteeredExecution(next steerQueueEntry) {
	al.goSteeredTurn(func() {
		if _, err := al.dispatchSteeredSessionReserved(context.Background(), next.sessionID, next.generation, next.runID, next.bootSeq); err != nil {
			if classifyDrainDispatchError(err) {
				logger.InfoCF("agent", "steer: drain queue: promoted session was no longer dispatchable (legitimate)",
					map[string]any{"session_id": next.sessionID, "generation": next.generation, "error": err.Error()})
				return
			}
			// Finding 6 (ADR-091 fix lane 2): the promoted entry is already
			// out of the queue and its slot went to someone else — on any
			// OTHER dispatch error (I/O failure, a lost race) the record is
			// left neither queued, running, terminal nor failed unless we
			// land it here, so its own parent's hasRunningOrQueuedDescendant
			// check is not blocked forever on a worker that will never run.
			logger.WarnCF("agent", "steer: drain queue: dispatch of the next queued session failed — landing it terminal so its parent is not blocked forever",
				map[string]any{"session_id": next.sessionID, "generation": next.generation, "error": err.Error()})
			// Failed promotion is an admission-identity failure, not a stop
			// landing. The claim-bound writer keeps the queued run's identity;
			// a generation-only terminal report could land the failure on a
			// later same-generation owner. A failed owning commit reports a
			// nonfatal persistence error through the existing parent inbox; its
			// returned error still records that the outcome is not committed.
			if reportErr := al.reportSteeredExecutionFailure(context.Background(), next.executionClaim(), fmt.Sprintf("dispatch_failed: %v", err)); reportErr != nil {
				logger.ErrorCF("agent", "steer: promoted admission failure could not be fully reported",
					map[string]any{"session_id": next.sessionID, "run_id": next.runID, "error": reportErr.Error()})
			}
		}
	})
}

// goSteeredTurn starts one detached goroutine on the steered-dispatch front
// and registers it with the gate's WaitGroup so AgentLoop.Close can join it.
//
// [ADR-091 fix lane FX-GOTEST] Close() already drains every OTHER dispatch
// front it owns before tearing down the stores those fronts write through —
// recaps (waitRecapDrain), tasks (TaskExecutor.Drain) and session workers
// (stopSessionWorkers) — and its own comments say why: "nothing writes after
// Close() returns to race temp-dir cleanup". ADR-091 added a THIRD front and
// registered it with none of that, so an admitted steered turn (and the
// promotion it triggers on the way out) kept writing lifecycle, inbox and
// transcript files after Close() had returned. In production that is a
// shutdown that reports done while work is still landing on disk; under
// `go test` it surfaces as "TempDir RemoveAll cleanup: directory not empty",
// because t.Cleanup runs BEFORE t.TempDir's own removal.
//
// Add() happens on the CALLER's goroutine, before the new one starts, so a
// Close racing a dispatch either sees the turn or happens strictly before
// it was ever admitted. A turn calling drainSteerQueue from its own defer
// adds the promotion's count while still holding its own, so the counter
// cannot dip to zero between the two.
func (al *AgentLoop) goSteeredTurn(run func()) {
	gate := al.steerAdmission()
	gate.turns.Add(1)
	go func() {
		defer gate.turns.Done()
		run()
	}()
}

// drainSteeredTurns waits for every in-flight steered turn and queue
// promotion, bounded by budget. Bounded for the same reason waitRecapDrain
// and TaskExecutor.Drain are: a provider that never returns must not be able
// to freeze shutdown for ever. After the budget it logs and proceeds — an
// unfinished child turn is strictly better than a wedged process.
func (al *AgentLoop) drainSteeredTurns(budget time.Duration) {
	done := make(chan struct{})
	go func() {
		al.steerAdmission().turns.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(budget):
		logger.WarnCF("agent", "Close: steered-turn drain budget exceeded; proceeding with teardown",
			map[string]any{"budget": budget.String()})
	}
}

// classifyDrainDispatchError reports whether err is one of the three
// legitimate "not dispatchable right now" outcomes I-6's reserveDispatch
// produces (ErrDispatchCancelled/ErrTerminal/ErrStaleGeneration) — expected,
// logged at Info — versus anything else, which drainSteerQueue must not let
// silently strand the record (Finding 6).
func classifyDrainDispatchError(err error) bool {
	return errors.Is(err, steer.ErrDispatchCancelled) ||
		errors.Is(err, steer.ErrTerminal) ||
		errors.Is(err, steer.ErrStaleGeneration)
}
