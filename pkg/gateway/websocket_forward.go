// websocket_forward.go: Forward agent events to the client as frames

package gateway

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// orphanWatchdogTimeout is the duration the forwarder waits after a parent turn ends
// before synthesizing a subagent_end{status:"interrupted"} for any still-open span.
// Configurable so tests can override to a short value (e.g., 200ms) without sleeping.
//
// Bumped 2026-05-11 from 5s → 60s. The old value killed legitimate subagents:
// a sub-turn that runs 3 shell calls back-to-back through a real LLM regularly
// takes 6–12s of wall-clock (1–4s per turn iteration × N tool calls), and Mia's
// root turn ends within ~2s of dispatching `spawn`. With a 5s watchdog the
// subagent was synthesizing `status:"interrupted"` after the second shell call
// even though the agent loop was still executing — closes the cascade of
// suite-load flakes in subagent.spec.ts (a)–(e) and handoff.spec.ts (b).
//
// 60s is a conservative upper bound for a single sub-turn; the parent-loop
// `subturn.default_timeout_minutes` config knob already enforces a hard
// runtime cap higher up the stack for legitimately stuck sub-turns.
var orphanWatchdogTimeout = 60 * time.Second

// orphanWatchdogMaxRechecks bounds how many times startOrphanWatchdog will
// reschedule after agent.AgentLoop.IsSubTurnActiveForSpawnCall reports "still
// active" before giving up and force-emitting the synthetic interrupted
// terminal frame regardless of what the liveness check reports (fail-closed).
//
// The re-check-and-reschedule loop is correctly bounded for the NORMAL case
// by the pre-existing sub-turn context timeout (pkg/agent/subturn.go's
// defaultSubTurnTimeout, 5 minutes by default, or subturn.default_timeout_minutes
// when configured) — that timeout cancels the child's context, runTurn
// returns, and IsSubTurnActiveForSpawnCall eventually reports false once
// spawnSubTurn's cleanup defer finishes persisting the real terminal status
// (see turnState.subTurnRecordPersisted's doc comment, pkg/agent/turn.go).
// But a genuinely wedged/deadlocked turn — a goroutine that neither returns
// nor panics, e.g. blocked on a tool call that does not honor context
// cancellation — has no ceiling of its own: IsSubTurnActiveForSpawnCall would
// report "active" forever (isFinished never flips), and without this bound
// the watchdog would reschedule indefinitely, logging only at slog.Debug
// (invisible at typical production log levels) and never emitting a terminal
// frame for that span.
//
// Default 15 reschedules x orphanWatchdogTimeout's default 60s = 15 minutes,
// comfortably (~3x) above pkg/agent/subturn.go's defaultSubTurnTimeout (5
// minutes) — a legitimately still-running sub-turn should never come close to
// exhausting this many reschedules; long before it would, its own context
// timeout has fired and IsSubTurnActiveForSpawnCall is already reporting
// false. Configurable so tests can override to a small value without
// sleeping for the real 15 minutes.
var orphanWatchdogMaxRechecks = 15

// openSpanEntry tracks an in-flight subagent span in the event forwarder.
type openSpanEntry struct {
	spanID          string
	parentCallID    string
	agentID         string
	sessionID       string        // session_id at spawn time; carried on synthesized frames
	parentTurnEnded bool          // set to true when EventKindTurnEnd fires for the parent turn
	closeCh         chan struct{} // closed when EventKindSubTurnEnd arrives (cancels watchdog)
}

// ADR-057 FR-089 — W5 audit classification artefact (U11's half).
//
// generated.SESSION_SCOPED_FRAME_TYPES has 19 members. 13 were classified by
// the spec itself (adr-057-session-unification-spec.md, BDD-16/BDD-98/BDD-99):
// class (a) both-ids — token, done, tool_call_start, tool_call_result,
// tool_approval_required, media; class (b) producing_session_id-absent —
// replay_message, session_started, session_close_ack, subagent_start,
// subagent_end; class (c) documented pre-existing gap — rate_limit,
// replay_done. The remaining 6 were left "class not yet assigned by the W5
// audit" on their generated types pending this classification, verified
// 2026-08 against this tree:
//
//   - agent_switched → class (a). Built at this file's ToolExecEnd case
//     (below, evtSID := p.SessionID from agent.ToolExecEndPayload) immediately
//     after a successful switch_agent tool_call_result (ADR-071 D4 merged
//     hand_off/return_to_default into this one tool) — the IDENTICAL payload
//     and session-id source as tool_call_result, which is already verified
//     class (a). A delegated child can invoke switch_agent on its own session
//     exactly as a root turn can, so evtSID is the child's own producing
//     session whenever that happens, distinct from the routing key. Stamped
//     alongside tool_call_result above.
//
//   - task_status_changed → class (b). Its only non-test construction site is
//     `TaskStatusChangedFrame{..., SessionId: p.SessionID, ...}` (this file's
//     EventKindTaskStatusChanged case), fed solely by
//     agent.TaskStatusChangedPayload, whose ONLY constructor is
//     pkg/agent/task_executor.go:1821-1830 (`sessionID := t.SessionID; ...
//     EmitTaskStatusChanged(TaskStatusChangedPayload{SessionID: sessionID,
//     ...})`) — the TaskExecutor (a system-level component) reporting a
//     scheduled task's OWN session lifecycle, not narration produced by a
//     delegated child turn. No stamping added; producing_session_id stays
//     absent.
//
//   - cancel_stage → class (b). Sole constructor is sendCancelStageFrame
//     (this file), called only with the id RequestCancel's CancelScope.SessionID
//     resolved to (pkg/agent/cancel.go:404-409,
//     `hooks.SendStageFrame(sessionID, "graceful")`) — the cancel machinery's
//     own target id, narrating the Stop's progress across the whole subtree it
//     cascades to (FR-032/W10c below), never a specific descendant's own
//     output. No stamping added.
//
//   - goal_status → class (b). Its only non-test construction site is this
//     file's EventKindGoalStatusChanged case (`SessionId: p.SessionID` from
//     agent.GoalStatusChangedPayload), whose constructors —
//     pkg/agent/goal_loop.go:502-514, several sites in
//     pkg/agent/goal_triggers.go, and pkg/agent/session_messaging_wire.go:602-606
//     /:626-630 — all pass the session that OWNS the /goal loop config being
//     reported, i.e. the session reporting on itself. No call site was found
//     where this payload's SessionID is a delegated child distinct from a
//     parent's routing id. No stamping added.
//
//   - loop_status → class (b). Its only non-test construction site is this
//     file's EventKindLoopStatusChanged case (`SessionId: p.SessionID` from
//     agent.LoopStatusChangedPayload), whose sole constructor
//     (pkg/agent/loop_command.go:301-309, `emitLoopStatusFrame(sessionID,
//     ...)`) reports on the session whose OWN /loop state changed — the
//     identical "reporting on itself" shape as goal_status. No stamping added.
//
//   - system_overload → COULD NOT DETERMINE; not guessed (spec line ~1309
//     forbids it). Verified: `rg -rl 'SystemOverloadFrame|system_overload'
//     pkg/` matches only the generated type
//     (pkg/api/generated/asyncapi_types.gen.go), its fixtures
//     (pkg/api/generated/fixtures.go), and the inbound-schema copy
//     (pkg/gateway/inboundschemas/SystemOverloadFrame.yaml) — ZERO
//     non-generated, non-fixture Go call site constructs or sends this frame
//     anywhere in pkg/gateway or pkg/agent. The type is fully specified on the
//     wire and consumed by the SPA (src/store/chat.ts, src/lib/ws.ts) but is
//     never produced by the backend, so there is no real emission site to
//     classify BY EVIDENCE. Do not assume a class for this type until a
//     producer exists and is audited.
//
// TokenFrame/DoneFrame (wsStreamer.Update/Finalize, below) need no stamping
// change: their shared "shadow stream" gate (isShadowStream forced true
// whenever parentSpawnCallID != "", in both Update and Finalize) means these
// two frames are constructed ONLY when parentSpawnCallID == "" — i.e. only
// for a turn that IS the routing session by definition (turn.go:406,
// session.RoutingSessionID's own contract: "for a root turn, RoutingSessionID
// MUST equal that turn's own SessionID"). SessionId already equals the
// routing key in every case either frame is actually emitted, so
// ProducingSessionId is correctly always absent.
//
// eventForwarder listens on the agent EventBus and forwards tool_call_start/result
// frames to the browser so tool call UIs render in real time.
// It also matches events from an attached task session (via taskChatIDs).
// Extended (FR-H-004, FR-H-005): emits subagent_start / subagent_end frames and
// propagates parent_call_id on tool_call_* frames fired inside sub-turns.
// Orphan watchdog (FR-H-004, Scenario 7): when the parent turn ends before all spans
// are closed, a timer fires after orphanWatchdogTimeout and synthesizes
// subagent_end{status:"interrupted"} for each still-open span.
// eventForwardState is the per-connection state one eventForwarder loop threads
// through its per-kind handlers: the connection, the chat id, the subscription, the
// open subagent spans, the root-turn latch, and the closures over the handler's maps.
// orphanFires carries a watchdog goroutine's "this span looks orphaned"
// verdict to THIS goroutine, which alone decides whether to synthesize the
// interrupted end.
//
// Root cause this closes (a delegation that completed normally reported as
// interrupted, before or just after its real success frame): the watchdog
// used to send the synthetic frame itself the moment
// agent.AgentLoop.IsSubTurnActiveForSpawnCall reported "not active". A
// sub-turn stops counting as active only after its EventKindSubTurnEnd is
// queued on sub.C (markSubTurnSpanOpen, pkg/agent/steering.go) — but this
// goroutine may not have consumed that event yet, so the watchdog's frame
// could still win. Deciding here, only once every event that was already
// queued when the verdict arrived has been handled, means a real end always
// closes its span first; a span still open after that is genuinely
// orphaned (or its end event was dropped, which EventBus counts and logs).
type orphanFire struct {
	entry  *openSpanEntry
	reason string
	// forced: the reschedule ceiling was exceeded (already logged at Error
	// level by the watchdog).
	forced bool
	// eventsAhead: events that were queued on sub.C when the verdict
	// arrived and have not been handled yet.
	eventsAhead int
}

type eventForwardState struct {
	h                 *WSHandler
	wc                *wsConn
	chatID            string
	sub               agent.EventSubscription
	openSpans         map[string]*openSpanEntry
	rootTurnEnded     bool
	rootTurnEndReason string
	orphanFires       chan orphanFire
	forwarderExited   chan struct{}
}

func (h *WSHandler) eventForwarder(wc *wsConn, chatID string, sub agent.EventSubscription, done chan<- struct{}) {
	defer close(done)
	f := &eventForwardState{
		h: h, wc: wc, chatID: chatID, sub: sub,
		openSpans:       make(map[string]*openSpanEntry),
		orphanFires:     make(chan orphanFire),
		forwarderExited: make(chan struct{}),
	}

	// openSpans tracks in-flight subagent spans keyed by parentCallID.
	// Accessed only from the single eventForwarder goroutine — no mutex needed.

	// rootTurnEnded latches whether the root turn for this connection has
	// already ended, and with what watchdog reason (#605). The root TurnEnd
	// and a delegate's SubTurnSpawn are emitted from DIFFERENT goroutines
	// (the parent turn's vs the detached async-delegate's), so a spawn event
	// can legally reach this forwarder AFTER the root turn_end. The
	// EventKindTurnEnd case below only arms spans already registered in
	// openSpans — without this latch, such a late-registered span would
	// never be armed and would stay invisible to the orphan watchdog
	// forever. EventKindSubTurnSpawn consults the latch to arm late
	// registrations immediately; a NEW root turn's TurnStart resets it so
	// spans of a live root turn are not spuriously armed.
	// Single-goroutine state like openSpans — no mutex needed.

	// forwarderExited releases a watchdog blocked handing over its verdict
	// once this goroutine has stopped reading orphanFires.
	defer close(f.forwarderExited)
	var pendingOrphanFires []orphanFire

	for {
		// Settle every watchdog verdict whose already-queued events have all
		// been handled (see orphanFires).
		if len(pendingOrphanFires) > 0 {
			waiting := pendingOrphanFires[:0]
			for _, fire := range pendingOrphanFires {
				if fire.eventsAhead > 0 {
					waiting = append(waiting, fire)
					continue
				}
				f.synthesizeOrphanEnd(fire)
			}
			pendingOrphanFires = waiting
		}

		var evt agent.Event
		select {
		case received, ok := <-sub.C:
			if !ok {
				return
			}
			evt = received
			for i := range pendingOrphanFires {
				if pendingOrphanFires[i].eventsAhead > 0 {
					pendingOrphanFires[i].eventsAhead--
				}
			}
		case fire := <-f.orphanFires:
			fire.eventsAhead = len(sub.C)
			pendingOrphanFires = append(pendingOrphanFires, fire)
			continue
		}

		switch evt.Kind {
		case agent.EventKindRateLimit:
			// #823 catch-up redesign: session-scoped rate_limit now goes
			// through the hub sync tap (websocket_forward_hub.go's
			// hubRateLimit), exactly once per event regardless of tab
			// count. onRateLimit now handles ONLY global-scope events
			// (not tied to a session) — see its own doc comment.
			f.onRateLimit(evt)
		case agent.EventKindWhatsAppPairing:
			f.onWhatsAppPairing(evt)
		case agent.EventKindNotification:
			f.onNotification(evt)
		case agent.EventKindTaskStatusChanged:
			f.onTaskStatusChanged(evt)
		case agent.EventKindPlanStatusChanged:
			f.onPlanStatusChanged(evt)
		case agent.EventKindTaskRunStatus:
			f.onTaskRunStatus(evt)
		// #823 catch-up redesign (honest gap, see SQUAD-REPORT-BEA.md):
		// TurnStart/SubTurnSpawn/SubTurnEnd/TurnEnd stay on this
		// per-connection path FOR NOW — the orphan-watchdog test suite
		// (orphan_watchdog_*.go) is real-timer-driven and deadlock-prone
		// if its precondition (watchdog armed) silently stops holding, so
		// cutting these four over needs that whole suite migrated
		// together in one pass, not attempted piecemeal here. The
		// hub-side equivalents (hubTurnStart/hubSubTurnSpawn/
		// hubSubTurnEnd/hubTurnEnd, websocket_forward_hub.go) are
		// written and directly unit-tested, just not wired into
		// hubSyncTap's dispatch yet.
		case agent.EventKindTurnStart:
			f.onTurnStart(evt)
		case agent.EventKindSubTurnSpawn:
			f.onSubTurnSpawn(evt)
		case agent.EventKindSubTurnEnd:
			f.onSubTurnEnd(evt)
		case agent.EventKindTurnEnd:
			f.onTurnEnd(evt)
		case agent.EventKindToolExecStart, agent.EventKindToolExecEnd,
			agent.EventKindError, agent.EventKindGoalStatusChanged, agent.EventKindGoalOutcome,
			agent.EventKindJudgeVerdict, agent.EventKindLoopStatusChanged, agent.EventKindToolResultProjection:
			// #823 catch-up redesign (BE-DESIGN.md §1.2): these kinds are
			// now translated and delivered EXACTLY ONCE per event by the
			// EventBus sync tap (websocket_forward_hub.go's hubSyncTap
			// and its hubXxx handlers), not once per connected tab by
			// this per-connection forwarder — that per-connection
			// production was the root flaw the hub redesign closes (a
			// session with N tabs would otherwise translate, and once
			// hub-numbered, NUMBER, the same event N different ways).
			// The old f.onToolExecStart/onToolExecEnd/onError/
			// onGoalStatusChanged/onGoalOutcome/onJudgeVerdict/
			// onLoopStatusChanged/onToolResultProjection methods that used
			// to live in this file are DELETED, not just unwired —
			// golangci-lint's unused-code check flagged them as dead once
			// this case stopped calling them, and keeping unreachable code
			// around was worse than deleting it. Every test that used to
			// drive them through this per-connection path now calls
			// h.hubSyncTap directly instead (see the migrated test files
			// listed in SQUAD-REPORT-BEA.md). This case is explicitly
			// empty (not a silent unmatched-case fallthrough) so the
			// intent reads plainly at the call site.
		case agent.EventKindLLMRequest, agent.EventKindLLMDelta, agent.EventKindLLMResponse,
			agent.EventKindLLMRetry, agent.EventKindContextCompress,
			agent.EventKindToolExecSkipped, agent.EventKindSteeringInjected, agent.EventKindFollowUpQueued,
			agent.EventKindInterruptReceived, agent.EventKindSubTurnResultDelivered, agent.EventKindSubTurnOrphan,
			agent.EventKindTurnTimeout, agent.EventKindEmptyResponseRetry, agent.EventKindCompactionRetry,
			agent.EventKindBackgroundProcessKill:
			// Not part of the live WS wire protocol — this forwarder only
			// translates the kinds handled above into browser frames.
			// Behavior-preserving: previously these fell through the switch
			// unmatched (no default case existed), which is a silent no-op
			// identical to this explicit, empty case.
		}
	}
}

// matchesChatID returns true if evtChatID belongs to this connection's chat or
// to a task session the connection has attached to via handleAttachSession.
func (f *eventForwardState) matchesChatID(evtChatID string) bool {
	if evtChatID == f.chatID {
		return true
	}
	f.h.mu.Lock()
	tid := f.h.taskChatIDs[f.chatID]
	f.h.mu.Unlock()
	// Note: using exclusive lock for a read-only lookup. Acceptable for now;
	// migrate h.mu to sync.RWMutex if contention becomes measurable.
	return tid != "" && evtChatID == tid
}

// matchesEvent extends matchesChatID with a session-based fallback so a
// live event reaches a connection that reattached to the same PERSISTED
// session after a reload, even though the event's own ChatID still names
// a now-stale, pre-reload connection.
//
// Root cause this closes (reload/replay "never self-updates" bug, live
// UAT re-verification 2026-07): ServeHTTP mints a brand-new chatID
// ("webchat:" + uuid.New()) for EVERY WebSocket connection, including a
// browser reload — there is no client-supplied continuity. A turn's own
// ChatID (turnState.chatID, threaded onto every event payload below) is
// stamped ONCE at turn-dispatch time from whichever connection sent the
// message, and never changes even if that connection later closes. A
// background delegate (Critical:true, 7dd9e7a5) can legitimately keep
// running long after its ORIGINATING connection is gone — matchesChatID
// alone can then NEVER match its live completion event against a NEW
// connection that reattached via attach_session, because rule 1 compares
// against the stale chatID, and the taskChatIDs alias (rule 2) maps this
// connection's chatID to the session_id, not to that stale chatID. The
// browser was stuck showing whatever replay served at attach time until
// a SECOND reload happened to catch the by-then-corrected transcript.
//
// evtSessionID/currentSessionID compares by the durable session_id
// instead: h.sessionIDs[chatID] is the session THIS connection currently
// has open (set by handleAttachSession/handleChatMessage), and
// evtSessionID is threaded end-to-end on every relevant event payload
// (SubTurnSpawnPayload, SubTurnEndPayload, ToolExec*Payload,
// TurnEndPayload). session_id survives a reload; chatID does not.
func (f *eventForwardState) matchesEvent(evtChatID, evtSessionID string) bool {
	if f.matchesChatID(evtChatID) {
		return true
	}
	if evtSessionID == "" {
		return false
	}
	f.h.mu.Lock()
	currentSessionID := f.h.sessionIDs[f.chatID]
	f.h.mu.Unlock()
	return currentSessionID != "" && evtSessionID == currentSessionID
}

// sessionIDForChat looks up the active session_id for a given chatID so every
// event frame can carry it, enabling per-session routing in the SPA.
func (f *eventForwardState) sessionIDForChat(evtChatID string) string {
	f.h.mu.Lock()
	sid := f.h.sessionIDs[evtChatID]
	if sid == "" {
		// Also check the task alias.
		if tid := f.h.taskChatIDs[evtChatID]; tid != "" {
			sid = f.h.sessionIDs[tid]
		}
	}
	f.h.mu.Unlock()
	return sid
}

// closeSpan marks a span as resolved and signals its watchdog to stop.
func (f *eventForwardState) closeSpan(parentCallID string) {
	if entry, ok := f.openSpans[parentCallID]; ok {
		select {
		case <-entry.closeCh: // already closed
		default:
			close(entry.closeCh)
		}
		delete(f.openSpans, parentCallID)
	}
}

// startOrphanWatchdog launches a goroutine that fires after orphanWatchdogTimeout
// if the span is not closed first. On timeout it hands its verdict to this
// goroutine (orphanFires), which synthesizes subagent_end and logs.
// W1-9: the goroutine also exits cleanly when wc.doneCh is closed (connection torn down).
func (f *eventForwardState) startOrphanWatchdog(entry *openSpanEntry, reason string) {
	// Snapshot BOTH test-shrinkable knobs ONCE, synchronously, before
	// spawning the goroutine below — never re-read the package-level vars
	// from inside it. This goroutine loops (reschedule on "still active")
	// for however long a genuine delegate keeps running, re-arming
	// time.After(orphanWatchdogTimeout) and re-checking the reschedule
	// count against orphanWatchdogMaxRechecks on every iteration —
	// potentially for the lifetime of a long test. A test that shrinks
	// these vars via
	// SetOrphanWatchdogTimeoutForTest/SetOrphanWatchdogMaxRechecksForTest
	// and restores them (defer/t.Cleanup) the moment its OWN foreground
	// assertions pass has no happens-before edge to this still-running
	// goroutine's later reads — a genuine data race (WARNING: DATA RACE,
	// websocket.go:3229 vs export_test.go:29, caught under
	// `go test -race`, TestOrphanWatchdog_GenuinelyActiveDelegate_
	// NeverSynthesizesInterrupted), not a flake. Capturing both up front
	// removes every later read of the package vars from this goroutine;
	// production behavior is unchanged since neither var is ever mutated
	// outside tests.
	watchdogTimeout := orphanWatchdogTimeout
	maxRechecks := orphanWatchdogMaxRechecks
	go func() {
		rechecks := 0
		for {
			select {
			case <-entry.closeCh:
				// Span resolved normally — nothing to do.
				return
			case <-f.wc.doneCh:
				// Connection closed while waiting — exit cleanly without emitting.
				return
			case <-time.After(watchdogTimeout):
				// Span is still open after timeout. Before declaring it
				// orphaned, confirm the real sub-turn genuinely isn't
				// still running.
				//
				// Root cause this closes (transient false "interrupted"
				// status flicker, live UAT re-verification 2026-07): this
				// watchdog arms the instant the PARENT turn ends
				// (EventKindTurnEnd, IsRoot), which — for a background
				// delegate — routinely happens within a second or two of
				// dispatch. Before 7dd9e7a5 ("background delegate's
				// final answer lost when parent finishes first"), a
				// delegate needing more than one LLM turn silently exited
				// its own loop early the instant its parent ended, so
				// the real EventKindSubTurnEnd almost always arrived
				// (closing this span via closeCh) well inside
				// orphanWatchdogTimeout. Critical:true now lets it run
				// for its full, genuine duration — so a normal,
				// still-working delegation can legitimately still be
				// open when this timer fires, and synthesizing
				// status:"interrupted" here fabricated a false terminal
				// state for a turn that was, in truth, still generating
				// (self-correcting only once the real EventKindSubTurnEnd
				// arrived later and overwrote it). Re-checking real
				// liveness via agent.AgentLoop.IsSubTurnActiveForSpawnCall
				// and rescheduling instead of firing turns this
				// heuristic, timeout-only guess into a confirm-or-wait
				// check — a genuinely orphaned span (the real check
				// below returns false) is still reported exactly as
				// before.
				stillActive := f.h.agentLoop != nil && f.h.agentLoop.IsSubTurnActiveForSpawnCall(entry.parentCallID)
				forceCeiling := false
				if stillActive {
					rechecks++
					if rechecks > maxRechecks {
						// Ceiling exceeded: a genuinely wedged/deadlocked
						// turn — a goroutine that neither returns nor
						// panics, e.g. blocked on a tool call not
						// honoring context cancellation — would
						// otherwise keep IsSubTurnActiveForSpawnCall
						// reporting "active" forever, and this loop
						// would reschedule indefinitely, never emitting
						// a terminal frame for the span. Fail closed:
						// force the synthetic interrupted frame below
						// regardless of what the liveness check
						// reports, matching this codebase's established
						// fail-closed posture elsewhere in delegation
						// gating.
						forceCeiling = true
						slog.Error("ws: subagent span still reports active past the watchdog's reschedule "+
							"ceiling — force-emitting interrupted (fail-closed)",
							"event", "span_orphan_ceiling_exceeded",
							"span_id", entry.spanID,
							"parent_call_id", entry.parentCallID,
							"reason", reason,
							"rechecks", rechecks,
							"max_rechecks", maxRechecks,
						)
					} else {
						// Escalate Debug -> Warn once the loop has
						// re-checked more than once or twice, so a
						// genuinely stuck span (heading toward the
						// ceiling above) is discoverable by an operator
						// without changing production log levels —
						// Debug alone is invisible at typical
						// production log levels.
						logFn := slog.Debug
						if rechecks > 2 {
							logFn = slog.Warn
						}
						logFn("ws: subagent span still genuinely active past watchdog timeout — rescheduling",
							"event", "span_orphan_recheck_still_alive",
							"span_id", entry.spanID,
							"parent_call_id", entry.parentCallID,
							"reason", reason,
							"rechecks", rechecks,
							"max_rechecks", maxRechecks,
						)
						continue
					}
				}
				// Span is still open after timeout AND either the real
				// sub-turn is confirmed no longer active, or the
				// reschedule ceiling was exceeded (forceCeiling, already
				// logged at Error level above). Hand the verdict to the
				// forwarder goroutine, which synthesizes the interrupted
				// end only if the span is STILL open once it has handled
				// every event already queued — see orphanFires.
				select {
				case f.orphanFires <- orphanFire{entry: entry, reason: reason, forced: forceCeiling}:
				case <-entry.closeCh:
				case <-f.wc.doneCh:
				case <-f.forwarderExited:
				}
				return
			}
		}
	}()
}

// synthesizeOrphanEnd emits the synthetic interrupted subagent_end for a
// span a watchdog found orphaned — unless the span's real end closed it
// while the verdict waited (see orphanFires). Runs only on this goroutine.
func (f *eventForwardState) synthesizeOrphanEnd(fire orphanFire) {
	entry := fire.entry
	if f.openSpans[entry.parentCallID] != entry {
		// Closed by its real EventKindSubTurnEnd (or replaced by a newer
		// span under the same call ID) while the verdict waited.
		return
	}
	reason := fire.reason
	switch {
	case fire.forced:
		// Already logged by the watchdog; avoid a second, redundant log line.
	case reason == "unknown":
		slog.Error("ws: subagent span orphaned with unknown reason — synthesizing interrupted end",
			"event", "span_orphan_interrupted",
			"span_id", entry.spanID,
			"parent_call_id", entry.parentCallID,
			"reason", reason,
		)
	default:
		slog.Warn("ws: subagent span orphaned — synthesizing interrupted end",
			"event", "span_orphan_interrupted",
			"span_id", entry.spanID,
			"parent_call_id", entry.parentCallID,
			"reason", reason,
		)
	}
	// Use generated.SubagentEndFrame (contract-first migration).
	endFrame := generated.SubagentEndFrame{
		Type:      string(generated.WsFrameTypeSubagentEnd),
		SessionId: entry.sessionID,
		SpanId:    entry.spanID,
		Status:    "interrupted",
		Message:   &reason,
	}
	if entry.agentID != "" {
		agentID := entry.agentID
		endFrame.AgentId = &agentID
	}
	if entry.parentCallID != "" {
		pc := entry.parentCallID
		endFrame.ParentCallId = &pc
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeSubagentEnd), endFrame)
	// The span is resolved: a later real end still sends its own frame
	// (the EventKindSubTurnEnd case never needs the entry), and a
	// resolved span is never re-armed or synthesized twice.
	f.closeSpan(entry.parentCallID)
}

// onTurnStart forwards agent.EventKindTurnStart to this connection.
func (f *eventForwardState) onTurnStart(evt agent.Event) {
	// #605: a NEW root turn began on this chat — reset the
	// root-turn-ended latch so spans it spawns are registered
	// unarmed (their root is alive; the TurnEnd case will arm them).
	// Only a ROOT turn's start may reset: a child's own turn-start
	// arrives between its SubTurnSpawn and the next root turn, and
	// resetting on it would reopen the arming hole for a sibling
	// delegate's later-arriving spawn event.
	p, ok := evt.Payload.(agent.TurnStartPayload)
	if !ok || !p.IsRoot || !f.matchesEvent(p.ChatID, "") {
		return
	}
	f.rootTurnEnded = false
	f.rootTurnEndReason = ""
}

// onSubTurnSpawn forwards agent.EventKindSubTurnSpawn to this connection.
func (f *eventForwardState) onSubTurnSpawn(evt agent.Event) {
	// FR-H-004: emit subagent_start when a sub-turn is spawned.
	p, ok := evt.Payload.(agent.SubTurnSpawnPayload)
	if !ok || !f.matchesEvent(p.ChatID, p.SessionID) {
		return
	}
	slog.Debug("ws: subagent_start",
		"span_id", p.SpanID,
		"parent_call_id", p.ParentSpawnCallID,
		"agent_id", p.AgentID,
	)
	// Prefer SessionID from payload; fall back to map lookup for legacy events.
	spawnSID := p.SessionID
	if spawnSID == "" {
		spawnSID = f.sessionIDForChat(p.ChatID)
	}
	// Use generated.SubagentStartFrame (contract-first migration).
	spawnFrame := generated.SubagentStartFrame{
		Type:         string(generated.WsFrameTypeSubagentStart),
		SessionId:    spawnSID,
		SpanId:       p.SpanID,
		ParentCallId: string(p.ParentSpawnCallID),
		TaskLabel:    p.TaskLabel,
	}
	if p.AgentID != "" {
		aid := p.AgentID
		spawnFrame.AgentId = &aid
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeSubagentStart), spawnFrame)
	// Register the span in openSpans for orphan watchdog tracking.
	entry := &openSpanEntry{
		spanID:       p.SpanID,
		parentCallID: string(p.ParentSpawnCallID),
		agentID:      p.AgentID,
		sessionID:    spawnSID,
		closeCh:      make(chan struct{}),
	}
	f.openSpans[string(p.ParentSpawnCallID)] = entry
	// #605: if the root turn already ended, the EventKindTurnEnd case
	// has already run its arming loop and will never see this entry —
	// arm it now, or the span stays invisible to the orphan watchdog
	// forever (no reschedule ceiling, no forced interrupted frame).
	if f.rootTurnEnded {
		entry.parentTurnEnded = true
		f.startOrphanWatchdog(entry, f.rootTurnEndReason)
	}
}

// onSubTurnEnd forwards agent.EventKindSubTurnEnd to this connection.
func (f *eventForwardState) onSubTurnEnd(evt agent.Event) {
	// FR-H-004: emit subagent_end when a sub-turn finishes.
	p, ok := evt.Payload.(agent.SubTurnEndPayload)
	if !ok || !f.matchesEvent(p.ChatID, p.SessionID) {
		return
	}
	slog.Debug("ws: subagent_end",
		"span_id", p.SpanID,
		"parent_call_id", p.ParentSpawnCallID,
		"agent_id", p.AgentID,
	)
	// Prefer SessionID from payload; fall back to map lookup for legacy events.
	endSID := p.SessionID
	if endSID == "" {
		endSID = f.sessionIDForChat(p.ChatID)
	}
	// Use generated.SubagentEndFrame (contract-first migration).
	endFrameEnd := generated.SubagentEndFrame{
		Type:      string(generated.WsFrameTypeSubagentEnd),
		SessionId: endSID,
		SpanId:    p.SpanID,
		Status:    string(p.Status),
	}
	if p.DurationMS != 0 {
		dm := int(p.DurationMS)
		endFrameEnd.DurationMs = &dm
	}
	if p.AgentID != "" {
		aid := p.AgentID
		endFrameEnd.AgentId = &aid
	}
	if p.ParentSpawnCallID != "" {
		pc := string(p.ParentSpawnCallID)
		endFrameEnd.ParentCallId = &pc
	}
	// FIX 4 (7-reviewer-gate follow-up): surface SubTurnEndPayload.Reason
	// (populated by spawnSubTurn's cleanup defer, pkg/agent/subturn.go,
	// only when Status == "interrupted") as the wire contract's
	// SubagentEndFrame.reason. The frontend (SubagentBlock.tsx) already
	// renders this — it just never received a value before this fix.
	if p.Reason != "" {
		reason := p.Reason
		endFrameEnd.Reason = &reason
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeSubagentEnd), endFrameEnd)
	// Signal the watchdog that the span closed normally.
	f.closeSpan(string(p.ParentSpawnCallID))
}

// onTurnEnd forwards agent.EventKindTurnEnd to this connection.
func (f *eventForwardState) onTurnEnd(evt agent.Event) {
	// W1-2: only arm the orphan watchdog when the root turn for this
	// connection ends (IsRoot == true) and the event belongs to our chat
	// (ChatID matches). Sub-turn ends from sibling sub-turns would otherwise
	// spuriously interrupt still-running spans on this connection.
	p, ok := evt.Payload.(agent.TurnEndPayload)
	if !ok || !p.IsRoot || !f.matchesEvent(p.ChatID, p.SessionID) {
		return
	}
	// Determine watchdog reason from the terminal status of the parent turn.
	var watchdogReason string
	switch p.Status {
	case agent.TurnEndStatusAborted:
		watchdogReason = "parent_cancelled"
	case agent.TurnEndStatusError:
		watchdogReason = "parent_timeout"
	case agent.TurnEndStatusCompleted:
		watchdogReason = "parent_done_early"
	case agent.TurnEndStatusParked:
		// Behavior-preserving: this previously fell through the
		// `default` branch below to "unknown" (Parked was not a
		// distinct case). Kept identical here rather than guessing
		// a more specific wire value without frontend confirmation
		// of what consumes it.
		watchdogReason = "unknown"
	default:
		watchdogReason = "unknown"
	}
	// #605: latch the root-turn-ended state for spans whose
	// SubTurnSpawn arrives after this event (see rootTurnEnded decl).
	f.rootTurnEnded = true
	f.rootTurnEndReason = watchdogReason
	for _, entry := range f.openSpans {
		if !entry.parentTurnEnded {
			entry.parentTurnEnded = true
			f.startOrphanWatchdog(entry, watchdogReason)
		}
	}
}

// onToolExecStart forwards agent.EventKindToolExecStart to this connection.
// onToolExecEnd forwards agent.EventKindToolExecEnd to this connection.
// onRateLimit forwards agent.EventKindRateLimit to this connection.
func (f *eventForwardState) onRateLimit(evt agent.Event) {
	// SEC-26: forward rate-limit denials to the browser so the chat UI
	// can display an inline indicator. Global-scope events (daily cost
	// cap) are broadcast to every connection since they are not tied
	// to a specific chatID.
	//
	// #823 catch-up redesign: SESSION-scoped rate_limit denials now go
	// through the hub sync tap (websocket_forward_hub.go's hubRateLimit)
	// exactly once per event instead of once per connected tab — this
	// method now handles ONLY the global-scope branch, unchanged from
	// before, per-connection (a global event is not tied to any one
	// session, so it has no session hub to number it through).
	p, ok := evt.Payload.(agent.RateLimitPayload)
	if !ok || p.Scope != "global" {
		return
	}
	rateSID := p.SessionID
	if rateSID == "" {
		rateSID = f.sessionIDForChat(p.ChatID)
	}
	// Use generated.RateLimitFrame (contract-first migration).
	rateF := generated.RateLimitFrame{
		Type:              string(generated.WsFrameTypeRateLimit),
		SessionId:         rateSID,
		Scope:             p.Scope,
		Resource:          p.Resource,
		PolicyRule:        p.PolicyRule,
		RetryAfterSeconds: p.RetryAfterSeconds,
	}
	if p.AgentID != "" {
		aid := p.AgentID
		rateF.AgentId = &aid
	}
	if p.Tool != "" {
		tool := p.Tool
		rateF.Tool = &tool
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeRateLimit), rateF)
}

// onError forwards agent.EventKindError to this connection.
// onWhatsAppPairing forwards agent.EventKindWhatsAppPairing to this connection.
func (f *eventForwardState) onWhatsAppPairing(evt agent.Event) {
	// #283: WhatsApp linked-device pairing (QR + status). Not tied to a
	// chatID. Delivered only to connections that subscribed to this
	// channel's pairing UI (Option B), so the QR pairing secret isn't
	// broadcast to every connected tab.
	p, ok := evt.Payload.(agent.WhatsAppPairingPayload)
	if !ok {
		return
	}
	pairF := generated.WhatsAppPairingFrame{
		Type:      string(generated.WsFrameTypeWhatsappPairing),
		ChannelId: p.ChannelID,
		Status:    string(p.Status),
	}
	if p.QR != "" {
		qr := p.QR
		pairF.Qr = &qr
	}
	if p.Message != "" {
		msg := p.Message
		pairF.Message = &msg
	}
	// #368: maintain the per-channel QR cache so late subscribers (e.g.
	// a tab that opens the pairing UI after the first QR fires) receive
	// the last-seen code immediately on subscribe rather than waiting for
	// the next QR rotation.  Only "code" (QR available) is cached;
	// terminal states are evicted so stale QRs are not re-emitted.
	switch p.Status {
	case channels.PairingStatusCode:
		if frameBytes, merr := json.Marshal(pairF); merr == nil {
			f.h.lastPairingState.Store(p.ChannelID, frameBytes)
		} else {
			slog.Error("ws: failed to marshal whatsapp_pairing frame for cache",
				"channel_id", p.ChannelID, "error", merr)
		}
	case channels.PairingStatusLinked, channels.PairingStatusTimeout, channels.PairingStatusError,
		channels.PairingStatusWaiting:
		// PairingStatusWaiting and any other status that is not
		// "code" must not leave a stale QR in the cache — evict so a
		// late subscriber is not shown an outdated code.
		f.h.lastPairingState.Delete(p.ChannelID)
	default:
		// Any future status not yet in this switch: same fail-safe
		// eviction as above, so an unrecognized status never leaves
		// a stale QR behind.
		f.h.lastPairingState.Delete(p.ChannelID)
	}
	if !f.wc.wantsPairing(p.ChannelID) {
		return
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeWhatsappPairing), pairF)
}

// onNotification forwards agent.EventKindNotification to this connection.
func (f *eventForwardState) onNotification(evt agent.Event) {
	// #264: a user-facing notification (e.g. a scheduled run failed).
	// Delivered ONLY to the recipient user's connections (filtered by
	// wc.userID) so it never leaks to other tabs/sessions. The
	// NotificationAdminBroadcast sentinel fans out to every connected
	// client unconditionally when no specific recipient could be
	// resolved — under the single-user model, "broadcast to admins" and
	// "broadcast to the one account's connections" are the same thing.
	p, ok := evt.Payload.(agent.NotificationPayload)
	if !ok {
		return
	}
	if p.Recipient != agent.NotificationAdminBroadcast && f.wc.userID != p.Recipient {
		return
	}
	notifF := generated.NotificationFrame{
		Type:             string(generated.WsFrameTypeNotification),
		Id:               p.ID,
		NotificationType: p.NotificationType,
		Title:            p.Title,
		Severity:         p.Severity,
		Read:             p.Read,
		CreatedAtMs:      p.CreatedAtMs,
	}
	if p.Body != "" {
		body := p.Body
		notifF.Body = &body
	}
	if p.ScheduleID != "" {
		sid := p.ScheduleID
		notifF.ScheduleId = &sid
	}
	if p.SessionID != "" {
		ses := p.SessionID
		notifF.SessionId = &ses
	}
	if p.AgentID != "" {
		aid := p.AgentID
		notifF.AgentId = &aid
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeNotification), notifF)
}

// onTaskStatusChanged forwards agent.EventKindTaskStatusChanged to this connection.
func (f *eventForwardState) onTaskStatusChanged(evt agent.Event) {
	// A workflow task's status changed (queued→running→completed/failed).
	// Not tied to a specific chatID — broadcast to every connection so
	// anyone viewing the tasks board sees live updates. The SPA
	// invalidates its tasks TanStack Query cache on receipt.
	p, ok := evt.Payload.(agent.TaskStatusChangedPayload)
	if !ok {
		return
	}
	taskF := generated.TaskStatusChangedFrame{
		Type:      string(generated.WsFrameTypeTaskStatusChanged),
		SessionId: p.SessionID,
		TaskId:    p.TaskID,
		Status:    p.Status,
	}
	if p.AgentID != "" {
		aid := p.AgentID
		taskF.AgentId = &aid
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeTaskStatusChanged), taskF)
}

// onPlanStatusChanged forwards agent.EventKindPlanStatusChanged to this connection.
func (f *eventForwardState) onPlanStatusChanged(evt agent.Event) {
	// ADR-049 D4/D7: a Plan's state/phase/progress/paused_reason changed.
	// Not tied to a specific chatID (a Plan is workspace-scoped, not
	// session-scoped) — broadcast to every connection, mirroring
	// EventKindTaskStatusChanged above. The SPA invalidates its plans
	// query cache / updates the plan card on receipt.
	p, ok := evt.Payload.(agent.PlanStatusChangedPayload)
	if !ok {
		return
	}
	planF := generated.PlanStatusFrame{
		Type:      string(generated.WsFrameTypePlanStatus),
		PlanId:    p.PlanID,
		State:     p.State,
		PlanPhase: p.PlanPhase,
		Progress:  p.Progress,
	}
	if p.PausedReason != "" {
		pr := p.PausedReason
		planF.PausedReason = &pr
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypePlanStatus), planF)
}

// onGoalStatusChanged forwards agent.EventKindGoalStatusChanged to this connection.
// onGoalOutcome forwards agent.EventKindGoalOutcome to this connection.
// onJudgeVerdict forwards agent.EventKindJudgeVerdict to this connection.
// onLoopStatusChanged forwards agent.EventKindLoopStatusChanged to this connection.
// onTaskRunStatus forwards agent.EventKindTaskRunStatus to this connection.
func (f *eventForwardState) onTaskRunStatus(evt agent.Event) {
	// A per-execution run opened or closed (ADR-050). Broadcast so the
	// calendar's per-occurrence chip updates live without a full refetch.
	// occurrence_ms is nil for an ad-hoc/once/manual run.
	p, ok := evt.Payload.(agent.TaskRunStatusPayload)
	if !ok {
		return
	}
	runF := generated.TaskRunStatusFrame{
		Type:   string(generated.WsFrameTypeTaskRunStatus),
		TaskId: p.TaskID,
		RunId:  p.RunID,
		Status: p.Status,
	}
	if p.OccurrenceMs != nil {
		ms := *p.OccurrenceMs
		runF.OccurrenceMs = &ms
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeTaskRunStatus), runF)
}

// onToolResultProjection forwards agent.EventKindToolResultProjection to this connection.
