// loop_run_turn.go: Run one agent turn through provider, response, and tool stages

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// agentLoopRunTurnIterationFlow reports how a block stage of agentLoopRunTurnIteration wants the conductor to proceed.
type agentLoopRunTurnIterationFlow int

// agentLoopRunTurnRequestFlow reports how a block stage of agentLoopRunTurnRequest wants the conductor to proceed.
type agentLoopRunTurnRequestFlow int

// agentLoopRunTurnResponseFlow reports how a block stage of agentLoopRunTurnResponse wants the conductor to proceed.
type agentLoopRunTurnResponseFlow int

// agentLoopRunTurnToolsFlow reports how a block stage of agentLoopRunTurnTools wants the conductor to proceed.
type agentLoopRunTurnToolsFlow int

// finalizeTurn chooses final content, persists it, and completes the turn result.
func (rz *agentLoopRunTurnFinalize) finalizeTurn() (turnResult, error) {
	if rz.rc.rx.rr.rq.ri.rf.rt.ts.hardAbortRequested() {
		rz.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
		return rz.rc.rx.rr.rq.ri.rf.rt.al.abortTurn(rz.rc.rx.rr.rq.ri.rf.rt.ts, "turn_finalize", hardInterruptAbortReason)
	}

	if rz.rc.rx.finalContent == "" {
		if rz.rc.rx.rr.rq.ri.rf.rt.ts.getTruncationReason() != "" {
			// ADR-087 D4a: this IS the deliberate outcome, not a fallthrough
			// — a truncated turn that produced literally no content. No
			// retry, no fallback substitution, no markTurnFailed; finalContent
			// stays "" and the write choke points below persist a zero-content
			// entry for MarkLastEntryTruncated to find.
		} else if rz.rc.rx.rr.rq.ri.rf.rt.ts.currentIteration() >= rz.rc.rx.rr.rq.ri.rf.rt.ts.agent.MaxIterations && rz.rc.rx.rr.rq.ri.rf.rt.ts.agent.MaxIterations > 0 {
			// Genuine failure: tool-iteration ceiling hit without a final response.
			// markTurnFailed so DoneStats.TurnFailed=true reaches the done frame.
			rz.rc.rx.finalContent = toolLimitResponse
			rz.rc.rx.rr.rq.ri.rf.rt.ts.markTurnFailed()
		} else {
			// The engine fell through without an LLM response and uses the
			// caller-supplied DefaultResponse as the content.  Only mark as
			// failed when the caller passed the engine's own error sentinel
			// (defaultResponse) — a caller-supplied success string such as
			// "Background task completed." (heartbeat/system path) must NOT be
			// flagged as a failed turn.
			rz.rc.rx.finalContent = rz.rc.rx.rr.rq.ri.rf.rt.ts.opts.DefaultResponse
			if rz.rc.rx.rr.rq.ri.rf.rt.ts.opts.DefaultResponse == defaultResponse {
				rz.rc.rx.rr.rq.ri.rf.rt.ts.markTurnFailed()
			}
		}
	}

	rz.rc.rx.rr.rq.ri.rf.rt.ts.setPhase(TurnPhaseFinalizing)
	rz.rc.rx.rr.rq.ri.rf.rt.ts.setFinalContent(rz.rc.rx.finalContent)
	if !rz.rc.rx.rr.rq.ri.rf.rt.ts.opts.NoHistory {
		finalMsg := providers.Message{Role: "assistant", Content: rz.rc.rx.finalContent}
		rz.rc.rx.rr.rq.ri.rf.rt.ts.agent.Sessions.AddMessage(rz.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey, finalMsg.Role, finalMsg.Content)
		if err := rz.rc.rx.rr.rq.ri.rf.rt.ts.agent.Sessions.Save(rz.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey); err != nil {
			rz.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
			// Wave 1: never surface raw err.Error() (session-save is a
			// local I/O error, not a provider error, but the same
			// invariant holds — the classifier emits a generic copy).
			saveLLM := TranslateLLMError(nil, err.Error())
			rz.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindError,
				rz.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
				ErrorPayload{
					Stage: "session_save", ChatID: rz.rc.rx.rr.rq.ri.rf.rt.ts.opts.ChatID,
					Code: string(saveLLM.Code), Message: saveLLM.Message,
					SessionID: string(rz.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
				},
			)
			// US-1: persist the session-save failure to the JSONL
			// transcript so the replay path re-renders it after reload (see
			// appendErrorTranscript docstring).
			rz.rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "runTurn", saveLLM)
			return turnResult{}, err
		}
	}

	// Bug 3 fix: persist assistant text to transcript.jsonl when no wsStreamer
	// was active (WS disconnected, non-webchat channel, or headless run).
	// When ts.lastStreamer != nil, the deferred ts.finalizeStreamer will call
	// wsStreamer.Finalize which writes the accumulated streaming content to the
	// transcript — but ONLY if the streamer's token buffer has content. When
	// every Update() call silently failed (WS closed mid-stream) the buffer is
	// empty and Finalize would skip the write. We hand finalContent to the
	// streamer via SetFinalContent so it can fall back to that text.
	rz.rc.rx.rr.rq.ri.rf.rt.ts.SetFinalContent(rz.rc.rx.finalContent)
	rz.rc.rx.rr.rq.ri.rf.rt.ts.mu.RLock()
	hasActiveStreamer := rz.rc.rx.rr.rq.ri.rf.rt.ts.lastStreamer != nil
	rz.rc.rx.rr.rq.ri.rf.rt.ts.mu.RUnlock()
	truncReason := rz.rc.rx.rr.rq.ri.rf.rt.ts.getTruncationReason()
	if !hasActiveStreamer {
		// ADR-087 D4a/D4b: this is the non-streaming write choke point.
		// When the turn was truncated, stamp Truncated/TruncationReason in
		// the SAME write as the content — see
		// appendAssistantTranscriptTruncated's doc comment for why this
		// replaces the former append-then-MarkLastEntryTruncated two-step
		// (for the streaming case, finalizeStreamer's own deferred call
		// does the single-write equivalent after wsStreamer.Finalize
		// persists its entry).
		if truncReason != "" {
			rz.rc.rx.rr.rq.ri.rf.rt.ts.appendAssistantTranscriptTruncated(rz.rc.rx.finalContent, truncReason)
		} else if rz.rc.rx.finalContent != "" {
			rz.rc.rx.rr.rq.ri.rf.rt.ts.appendAssistantTranscript(rz.rc.rx.finalContent)
		}
	}

	rz.rc.rx.rr.rq.ri.rf.rt.ts.setPhase(TurnPhaseCompleted)
	return turnResult{
		finalContent: rz.rc.rx.finalContent,
		status:       rz.rc.rx.rr.rq.ri.turnStatus,
		followUps:    append([]bus.InboundMessage(nil), rz.rc.rx.rr.rq.ri.rf.rt.ts.followUps...),
		turnFailed:   rz.rc.rx.rr.rq.ri.rf.rt.ts.turnFailed,
	}, nil
}

// createTurnContext validates the caller context and creates the cancellable per-turn context.
func (rc *agentLoopRunTurnConductor) createTurnContext() agentLoopRunTurnConductorFlow {
	if rc.rx.ctx.Err() != nil {
		rc.ret0 = turnResult{}
		rc.ret1 = fmt.Errorf("turn not started: %w", rc.rx.ctx.Err())
		return agentLoopRunTurnConductorReturn
	}

	// Snapshot the media store once at turn start to avoid repeated lock
	// acquisitions on the hot path and to ensure consistency across the turn
	// even if restartServices swaps the store mid-turn (N2 data-race fix).
	rc.rx.rr.rq.ri.turnMediaStore = rc.rx.rr.rq.ri.rf.rt.al.GetMediaStore()
	// Snapshot the channel manager for the same reason.
	rc.rx.turnChannelManager = rc.rx.rr.rq.ri.rf.rt.al.getChannelManager()
	// ADR-051 Rev 4 (Wave 3 T9): snapshot the capability catalog and the
	// per-session manifest refcounter for the presentation chain. Both are
	// nil-safe (nil catalog → optimistic gate; nil refcounter → no tracking),
	// so legacy/no-workspace turns degrade gracefully.
	rc.rx.rr.rq.ri.turnCatalog = rc.rx.rr.rq.ri.rf.rt.al.getCapabilityCatalog()
	rc.rx.rr.rq.ri.turnRefcounter = rc.rx.rr.rq.ri.rf.rt.al.getTurnRefcounter(rc.rx.rr.rq.ri.rf.rt.ts.opts.WorkspaceID, rc.rx.rr.rq.ri.rf.rt.ts.transcriptSessionID)

	turnTimeout := time.Duration(rc.rx.rr.rq.ri.rf.rt.ts.agent.TimeoutSeconds) * time.Second
	if turnTimeout > 0 {
		rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.turnCancel = context.WithTimeout(rc.rx.ctx, turnTimeout)
	} else {
		rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.turnCancel = context.WithCancel(rc.rx.ctx)
	}
	return agentLoopRunTurnConductorNext
}

// registerTurnContext injects turn metadata into context and registers active-turn lifecycle state.
func (rc *agentLoopRunTurnConductor) registerTurnContext() {
	rc.rx.rr.rq.ri.rf.rt.ts.setTurnCancel(rc.turnCancel)

	// NOTE: finalizeStreamer's defer is registered further below (after
	// ts.Finish/al.clearActiveTurn), not here — see that registration
	// site's comment for why the ORDER relative to Finish is load-bearing
	// (FIX 1/5c live-verification finding).

	// Inject turnState and AgentLoop into context so tools (e.g. delegate) can retrieve them.
	rc.rx.rr.rq.ri.rf.rt.turnCtx = withTurnState(rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.rx.rr.rq.ri.rf.rt.ts)
	rc.rx.rr.rq.ri.rf.rt.turnCtx = WithAgentLoop(rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.rx.rr.rq.ri.rf.rt.al)
	// SEC-15: Inject agent ID so audit entries carry the agent identity.
	rc.rx.rr.rq.ri.rf.rt.turnCtx = tools.WithAgentID(rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.rx.rr.rq.ri.rf.rt.ts.agent.ID)
	// Inject session key so switch_agent can address the session.
	if rc.rx.rr.rq.ri.rf.rt.ts.sessionKey == "" {
		logger.WarnCF("agent", "runTurn: sessionKey is empty — switch_agent tool will not work",
			map[string]any{"agent_id": rc.rx.rr.rq.ri.rf.rt.ts.agentID, "chat_id": rc.rx.rr.rq.ri.rf.rt.ts.chatID})
	}
	rc.rx.rr.rq.ri.rf.rt.turnCtx = tools.WithSessionKey(rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.rx.rr.rq.ri.rf.rt.ts.sessionKey)
	// Inject the actual session ID (directory name) for the switch_agent tool.
	// The session key is a routing key; the transcript session ID is the
	// real session directory (e.g., "session_01KP30THP63YFESKGECYYHYQWY").
	rc.rx.rr.rq.ri.rf.rt.turnCtx = tools.WithTranscriptSessionID(rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.rx.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID)
	// ADR-085 BROWSER-FR-021: stamp the ROOT chat session id (ADR-057
	// routingSessionID, inherited verbatim through a whole delegation
	// subtree) so pkg/tools/browser/tools.go::controlledResult can evaluate
	// its FR-020 second coverage check — the tab set the live panel would
	// hold the lock on for the chat this turn (or its delegated ancestor)
	// belongs to — even for a delegated child driving its OWN tab set
	// (FR-023). A turn with no root chat (cron/heartbeat/task) stamps "",
	// which controlledResult's own doc comment documents as "skip that
	// check entirely" (fails open, never closed).
	rc.rx.rr.rq.ri.rf.rt.turnCtx = withBrowserRootChatSessionID(rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.rx.rr.rq.ri.rf.rt.ts)
	// Inject the session owner so sysagent tools (system.workspace.create,
	// system.task.create) can stamp the owner on newly created entities
	// (Rule-2 of the sysagent ownership rule, SEC-2/#406).
	if rc.rx.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID != "" {
		if store := rc.rx.rr.rq.ri.rf.rt.al.ResolveSessionStore(rc.rx.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID); store != nil {
			if meta, err := store.GetMeta(rc.rx.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID); err == nil && meta.Owner != "" {
				rc.rx.rr.rq.ri.rf.rt.turnCtx = tools.WithSessionOwner(rc.rx.rr.rq.ri.rf.rt.turnCtx, meta.Owner)
			}
		}
	}
	// Inject the workspace ID so memory tools can route to the shared room (FR-7.1).
	// This stays driven EXCLUSIVELY by the turn's channel-bound WorkspaceID —
	// unchanged by the CoreTeam-based filesystem re-rooting below, which is a
	// deliberately separate, independent signal (see that block's comment).
	if rc.rx.rr.rq.ri.rf.rt.ts.opts.WorkspaceID != "" {
		rc.rx.rr.rq.ri.rf.rt.turnCtx = tools.WithWorkspaceID(rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.rx.rr.rq.ri.rf.rt.ts.opts.WorkspaceID)
	}

	// Filesystem re-rooting: every agent that belongs to a Workspace's CoreTeam
	// — native (Main/Subagent) or subagent_3p (external-CLI), no exceptions by
	// kind — works in that Workspace's dedicated project-work subdirectory
	// (workspaces/<id>/work/, materialized via workspace.EnsureWorkDir — the
	// sanctioned SafeWorkDir+MkdirAll+git-evidence replacement) instead of its
	// private per-agent one. Unconditional (no feature flag) and PRIMARILY driven by
	// AGENT IDENTITY (workspace.FindForAgent's CoreTeam-membership lookup),
	// NOT by ts.opts.WorkspaceID above: those are genuinely different signals
	// that can diverge in both directions — a CoreTeam member responding via
	// an unbound channel still has ts.opts.WorkspaceID == ""; a channel bound
	// to a workspace can route to an agent stale-removed from that
	// workspace's CoreTeam. Keying off agent identity instead of the
	// turn-carried value is also what makes this correctly cover DELEGATED
	// sub-agent turns regardless of whether ts.opts.WorkspaceID happens to be
	// set on the child: this identity-keyed lookup applies uniformly to
	// top-level turns and delegated children alike, since both resolve
	// ts.agent.ID the same way.
	//
	// STALE-COMMENT CORRECTION (FIX 1 re-review): this used to say
	// spawnSubTurn never threads WorkspaceID into a child's processOptions,
	// making ts.opts.WorkspaceID structurally always "" for a delegated
	// child. That is no longer true — spawnSubTurn (pkg/agent/subturn.go)
	// now inherits WorkspaceID from the PARENT turn (session/room context,
	// same as Channel/ChatID; see that struct literal's own comment for why
	// this is deliberately NOT covered by ADR-032's target-identity
	// inheritance rule). A delegated child's ts.opts.WorkspaceID can
	// therefore be non-empty today, which is exactly what lets it
	// participate in FindForAgentPreferring's tie-break below when the
	// child agent belongs to more than one workspace's CoreTeam — it does
	// NOT change the identity-primacy described above: a child with no
	// CoreTeam membership at all still gets no re-root regardless of
	// ts.opts.WorkspaceID, and a child whose membership is unambiguous
	// (one workspace) resolves the same way with or without it.
	//
	// FindForAgentPreferring (not FindForAgent) is used here so that when the
	// SAME agent belongs to MORE than one workspace's CoreTeam — a real,
	// reachable state FindForAgent's own doc comment describes — the CURRENT
	// turn's own ts.opts.WorkspaceID (when it is itself one of the agent's
	// memberships) breaks the tie, instead of FindForAgent's arbitrary
	// sorted-first pick. This narrows an already-ambiguous choice using a
	// signal that is trustworthy exactly when it is present; it never widens
	// or overrides the identity-based membership check, so an unbound-channel
	// turn (ts.opts.WorkspaceID == "") is unaffected — it falls straight
	// through to FindForAgent.
	//
	// Security: workspaces/<id>/ lives under $OMNIPUS_HOME, which the boot
	// Landlock policy already grants RWX — this changes only the working
	// directory and app-level path-validation root, never the kernel sandbox.
	// Sessions and private agent memory stay under agents/<id>/, never
	// re-rooted. The re-root target is deliberately workspaces/<id>/work/, NOT
	// workspaces/<id>/ itself: that directory also holds AGENT.md (the
	// workspace's Project Instructions, injected into every agent's prompt)
	// and the shared memory room (.omnipus/) — a generic write_file/edit_file
	// confined (via os.Root) to work/ cannot reach either, structurally, since
	// os.Root cannot open a path outside its own root. (An earlier version of
	// this re-rooted directly to workspaces/<id>/, leaving AGENT.md reachable —
	// pkg/tools/metadata_guard.go's app-level guard only recognizes the
	// agents/<id>/ layout and does not match workspaces/<id>/AGENT.md, so
	// nothing else was catching it.)
	// ADR-046 P1 (FR-007/008): execution is always workspace-scoped. An agent
	// that is not a member of ANY workspace's CoreTeam cannot execute at all —
	// no silent fallthrough to its own agent-home directory, no ambiguous
	// lexicographic guess. This refusal deliberately runs AFTER the
	// tools.WithWorkspaceID injection above (memory routing, FR-030) so that
	// separate signal is completely unaffected by this gate either way.
	//
	// resolveTurnWorkDirOrRefuse (workspace_reroot.go) is the SHARED gate —
	// external_dispatch.go's runExternalCLISubTurn calls the exact same
	// function so the membership refusal cannot diverge between the native
	// and external-cli dispatch paths again (a prior review found
	// runExternalCLISubTurn had its own, weaker copy that fell through to the
	// agent's private home directory instead of refusing).
	// The CALL itself is below, AFTER registerActiveTurn and the turn-end
	// defers. A prior version returned here, before the turn existed as a
	// turn: no EventKindError, no transcript, no done frame. The classifier
	// already knew the cause (TranslateTurnError → agent_not_configured) and
	// still had nowhere to put it. The user saw a turn that never started.
	// Moving the call below makes a refusal a real failed turn the SPA can
	// render. The gate function is unchanged — only when it runs changed.

	// FR-7.5 / NFR-1: install a per-turn citation tracker so recall_memory can
	// report surfaced memories and the loop can emit op:cited counter events
	// when the LLM references them by ID/title. Nil for the main gateway agent
	// (no memory store); WithCitationTracker is a no-op in that case.
	rc.rx.rr.citationTracker = newCitationTracker(rc.rx.rr.rq.ri.rf.rt.ts.agent.ContextBuilder.Memory())
	rc.rx.rr.rq.ri.rf.rt.turnCtx = tools.WithCitationTracker(rc.rx.rr.rq.ri.rf.rt.turnCtx, rc.rx.rr.citationTracker)

	rc.rx.rr.rq.ri.rf.rt.al.registerActiveTurn(rc.rx.rr.rq.ri.rf.rt.ts)
}

// agentLoopRunTurnPrepare carries the shared state of prepareTurn across its stages.
type agentLoopRunTurnPrepare struct {
	rc *agentLoopRunTurnConductor
}

// prepareTurn emits turn start, resolves workspace and model state, and assembles the initial context.
func (rc *agentLoopRunTurnConductor) prepareTurn() agentLoopRunTurnConductorFlow {
	rp := &agentLoopRunTurnPrepare{rc: rc}

	if r0, stop := rp.resolveWorkspaceAndModel(); stop {
		return r0
	}

	if r0, stop := rp.assembleInitialContext(); stop {
		return r0
	}

	// FR-005: an exempt provider (subprocess CLI) manages its own context —
	// the pre-turn trim and every budget check are skipped.
	return rp.selectTurnProvider()
}

// resolveWorkspaceAndModel emits turn start, resolves the workspace, and applies any requested model switch.
func (rp *agentLoopRunTurnPrepare) resolveWorkspaceAndModel() (agentLoopRunTurnConductorFlow, bool) {
	rp.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
		EventKindTurnStart,
		rp.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.start"),
		TurnStartPayload{
			Channel:     rp.rc.rx.rr.rq.ri.rf.rt.ts.channel,
			ChatID:      rp.rc.rx.rr.rq.ri.rf.rt.ts.chatID,
			UserMessage: rp.rc.rx.rr.rq.ri.rf.rt.ts.userMessage,
			MediaCount:  len(rp.rc.rx.rr.rq.ri.rf.rt.ts.media),
			IsRoot:      rp.rc.rx.rr.rq.ri.rf.rt.ts.parentTurnID == "",
			// #823 review item 8: the routing session id, exactly as
			// TurnEndPayload carries it, so the WS session hub can reset
			// the root-turn-ended latch for a background turn whose chat id
			// is bound to no browser connection.
			SessionID: string(rp.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
		},
	)

	// Shared workspace-membership gate — see the ADR-046 comment above for
	// WHY this refuses. It runs HERE so a refusal is a registered turn:
	// turn.start has already gone out, the LIFO defers (markTurnFailed →
	// turn.end → finalizeStreamer) will fire on return, and the typed
	// EventKindError below is what the SPA actually renders. Returning
	// before registerActiveTurn left the user with silence and a classifier
	// result nobody ever saw.
	var wsErr error
	rp.rc.rx.rr.rq.ri.wsDir, wsErr = resolveTurnWorkDirOrRefuse(rp.rc.rx.rr.rq.ri.rf.rt.turnCtx, rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID, rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.Home, rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.WorkspaceID)
	if wsErr != nil {
		rp.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
		llm := TranslateTurnError(wsErr)
		rp.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindError,
			rp.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{
				Stage:     "workspace",
				ChatID:    rp.rc.rx.rr.rq.ri.rf.rt.ts.chatID,
				SessionID: string(rp.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
				Code:      string(llm.Code),
				Message:   llm.Message,
			},
		)
		rp.rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "workspace", llm)
		rp.rc.ret0 = turnResult{}
		rp.rc.ret1 = wsErr
		return agentLoopRunTurnConductorReturn, true
	}
	rp.rc.rx.rr.rq.ri.rf.rt.turnCtx = tools.WithTurnWorkspaceDir(rp.rc.rx.rr.rq.ri.rf.rt.turnCtx, rp.rc.rx.rr.rq.ri.wsDir)

	// FR-011, FR-012: detect a per-thread model switch via the inbound message
	// metadata. When the user-selected model differs from the agent's currently
	// loaded model, run handleModelSwitch BEFORE the first LLM call so the next
	// request sees the compressed, annotated history. handleModelSwitch is a
	// no-op when no switch is requested.
	if requested := strings.TrimSpace(
		inboundMetadata(bus.InboundMessage{Metadata: rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.Metadata}, "model_name"),
	); requested != "" {
		// Skip when the requested model is the same as the agent's currently
		// loaded one — this is the no-op case in spec §11 Dataset 3 row 1.
		if requested != rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.Model {
			switchedAgent, switchErr := rp.rc.rx.rr.rq.ri.rf.rt.al.handleModelSwitch(
				rp.rc.rx.ctx,
				rp.rc.rx.rr.rq.ri.rf.rt.ts.agent,
				rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID,
				rp.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey,
				requested,
				bus.InboundMessage{Metadata: rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.Metadata},
			)
			if switchErr != nil {
				logger.WarnCF("agent", "switch-time compress failed; continuing with current model",
					map[string]any{
						"agent_id":   rp.rc.rx.rr.rq.ri.rf.rt.ts.agentID,
						"session_id": rp.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey,
						"new_model":  requested,
						"error":      switchErr.Error(),
					})
				// The caller explicitly asked for `requested` (typically a model
				// picked from the composer's live catalog) and the switch could not
				// be applied — the turn is about to proceed on the agent's current
				// model instead. A backend-only WARN log is not enough: nothing else
				// tells the caller their selection was ignored, which is exactly the
				// "picking a model has no effect" failure mode. Mirror the
				// FR-001/FR-002 pattern already used for rate-limit/provider errors
				// (docs/internal/specs/phase-1-chat-model-and-errors.md): emit
				// EventKindError + persist a transcript entry so replay shows it
				// (appendErrorTranscript), AND push a live notification so the
				// CURRENT session learns immediately — the transcript write alone
				// only becomes visible after a reload.
				//
				// The classifier has never recognized this sentence — the
				// comment that said it did was stale. Stamp the dedicated
				// code so live and replay both say the switch failed, not
				// "we can't tell why". Raw stays in the warn log.
				switchFailMsg := fmt.Sprintf(
					"Could not switch to model %q: %s. This reply used %q instead.",
					requested, switchErr.Error(), rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.Model,
				)
				switchLLM := LLMError{
					Code:      CodeModelUnavailable,
					Message:   defaultUserMessage(CodeModelUnavailable),
					Retryable: isRetryable(CodeModelUnavailable),
					Detail:    buildDetail(nil, switchFailMsg),
				}
				rp.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
					EventKindError,
					rp.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
					ErrorPayload{
						Stage:     "model_switch",
						Code:      string(switchLLM.Code),
						Message:   switchLLM.Message,
						ChatID:    rp.rc.rx.rr.rq.ri.rf.rt.ts.chatID,
						SessionID: string(rp.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
					},
				)
				rp.rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "model_switch", switchLLM)
				// NB: no notification frame here — `model_switch_failed` is not a
				// contract NotificationFrame.notification_type, so the SPA's
				// inbound Zod validation would drop it. The EventKindError +
				// error-transcript record above is the surfacing; the aggregator
				// resolver fix (ResolveModelCfg step 4) means a composer-catalog
				// pick now resolves, so this branch only fires for a genuinely
				// unroutable model (no passthrough provider configured).
			} else if switchedAgent != nil {
				// Re-point the turn at the (possibly mutated) agent so
				// subsequent reads of ts.agent.Model reflect the switch.
				rp.rc.rx.rr.rq.ri.rf.rt.ts.agent = switchedAgent
			}
		}
	}
	return 0, false
}

// ErrAgentNeedsProvider is returned by runTurn when the agent's PRIMARY
// provider id is UNKNOWN (ADR-067 FR-016/FR-038): neither a catalog id nor a
// constructible custom row — including an id that differs from a configured
// one only by case, which is exact-compared and therefore unknown (FR-036).
//
// The turn is refused with LLMError code needs_provider (attribution
// `config`), logged at WARN, and ZERO upstream requests are made. It is
// evaluated FIRST in the pre-turn gate, ahead of ADR-068's model_unassigned
// and ADR-066's ErrContextWindowUnknown: a provider must exist before a model
// can, and a model must exist before its window can be sized.
//
// The refusal clears the moment the operator re-points the agent at a real
// provider through the existing agent-update path — no restart beyond the
// reload that path already triggers (US-6.AC3).
var ErrAgentNeedsProvider = errors.New("agent's provider is not configured; turn refused")

// ErrAgentModelUnassigned is returned by runTurn when the agent has no model
// to send the request to (ADR-068 FR-014/FR-015, MAJ-008). Two shapes reach
// it, and they are exactly the two halves of the derived `needs_model` the
// gateway projects onto Agent.needs_model:
//
//   - the agent pins no primary model and `agents.defaults.default_model`
//     names none either, so there is literally nothing to call; or
//   - the model it does pin routes through a provider that is not configured,
//     which is ADR-067's `needs_provider` state — and that code WINS, because
//     the pre-turn gate evaluates it first (see below).
//
// The turn is refused with LLMError code model_unassigned (attribution
// `config`) and ZERO upstream requests are made. It is evaluated SECOND in
// the pre-turn gate: after ADR-067's ErrAgentNeedsProvider (a provider must
// exist before a model can) and before ADR-066's ErrContextWindowUnknown (a
// model must exist before its window can be sized). The overlap is not
// hypothetical — an agent bound to an unknown provider satisfies BOTH
// predicates, and SC-013 requires it to end with `needs_provider`.
//
// The refusal clears as soon as the operator assigns a model through the
// existing agent-update path, which triggers its own reload.
var ErrAgentModelUnassigned = errors.New("agent has no model assigned; turn refused")

// ErrContextWindowUnknown is returned by runTurn when the agent's provider is
// a `locality: local` endpoint that reported no context window and no
// operator override exists (ADR-066 D3, FR-008). The turn is refused — never
// run on a guessed window — with LLMError code context_window_unknown
// (attribution config). It is evaluated THIRD in the pre-turn gate, after
// ADR-067's needs_provider and ADR-068's model_unassigned. Setting
// ContextSettings.model_overrides[] for the (provider, model) triggers a
// reload and clears it without a restart.
var ErrContextWindowUnknown = errors.New("context window unknown for this model; turn refused")

// assembleInitialContext assembles message history and enforces provider, model, and context-window gates.
func (rp *agentLoopRunTurnPrepare) assembleInitialContext() (agentLoopRunTurnConductorFlow, bool) {
	var history []providers.Message
	if !rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.NoHistory {
		// FR-069 / FR-088: recover orphaned tool calls left by a SIGKILL or OOM
		// kill while the gateway was paused awaiting approval. The function is
		// idempotent — it no-ops on clean sessions and on sessions where the
		// synthetic turn_canceled_restart entry already exists. The on-disk
		// transcript is preserved; only the LLM-context slice (history) is
		// cleaned so the LLM does not see dangling unanswered tool_call entries.
		history = RecoverOrphanedToolCalls(rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.Sessions, rp.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey, rp.rc.rx.rr.rq.ri.rf.rt.al.auditLogger)
	}

	// Site-1: initial assembly (CRITICAL 2 — error handled inside assembleMessages).
	rp.rc.rx.rr.rq.ri.messages = rp.rc.rx.rr.rq.ri.rf.rt.al.assembleMessages(
		rp.rc.rx.rr.rq.ri.rf.rt.turnCtx,
		rp.rc.rx.rr.rq.ri.rf.rt.ts,
		history,
		rp.rc.rx.rr.rq.ri.rf.rt.ts.userMessage,
		rp.rc.rx.rr.rq.ri.rf.rt.ts.media,
		activeSkillNames(rp.rc.rx.rr.rq.ri.rf.rt.ts.agent, rp.rc.rx.rr.rq.ri.rf.rt.ts.opts),
	)

	rp.rc.rx.rr.rq.ri.cfg = rp.rc.rx.rr.rq.ri.rf.rt.al.GetConfig()
	rp.rc.rx.rr.rq.ri.maxMediaSize = rp.rc.rx.rr.rq.ri.cfg.Agents.Defaults.GetMaxMediaSize()
	// Step-5 offload target: the workspace work/ dir already resolved (and
	// MkdirAll'd) for this turn at resolveTurnWorkDirOrRefuse above. Passing it
	// as an offloadSink lets attachments no provider can present (e.g. AVIF/HEIC
	// with no decoder) be copied into work/ and surfaced as a filesystem path +
	// guidance instead of dying the turn (ADR-051 Rev 4 FR-020/020a/021).
	turnProvider, turnModel := rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.primaryModelPair()
	rp.rc.rx.rr.rq.ri.messages = resolveMediaRefsWithOffload(
		rp.rc.rx.rr.rq.ri.messages, rp.rc.rx.rr.rq.ri.turnMediaStore, rp.rc.rx.rr.rq.ri.maxMediaSize, turnProvider, turnModel,
		&offloadSink{workDir: rp.rc.rx.rr.rq.ri.wsDir}, rp.rc.rx.rr.rq.ri.turnCatalog, rp.rc.rx.rr.rq.ri.turnRefcounter,
		rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.WorkspaceID,
	)

	// ADR-067 FR-016/FR-038 pre-turn gate (FIRST of the three). An agent whose
	// PRIMARY provider id is unknown cannot reach any upstream at all, so it
	// is refused here — before the model gate can ask which model, and before
	// the window gate can ask how big that model's context is. Like the two
	// gates below it the refusal is a REAL failed turn: turn.start already
	// went out, the LIFO defers fire on return, and the typed EventKindError
	// is what the SPA renders. The WARN and the error name the operator's own
	// spelling of the id and nothing else — never a canonical alternative
	// (SC-010).
	if needsProvider, unknownProviderID := rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.needsProviderSnapshot(); needsProvider {
		rp.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
		providerErr := fmt.Errorf("%w: agent_id=%s provider=%s",
			ErrAgentNeedsProvider, rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID, unknownProviderID)
		llm := TranslateTurnError(providerErr)
		logger.WarnCF("agent", "Turn refused: the agent's provider is unknown",
			map[string]any{"agent_id": rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID, "provider": unknownProviderID})
		rp.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindError,
			rp.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{
				Stage:     "provider",
				ChatID:    rp.rc.rx.rr.rq.ri.rf.rt.ts.chatID,
				SessionID: string(rp.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
				Code:      string(llm.Code),
				Message:   llm.Message,
			},
		)
		rp.rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "provider", llm)
		rp.rc.ret0 = turnResult{}
		rp.rc.ret1 = providerErr
		return agentLoopRunTurnConductorReturn, true
	}

	// ADR-068 FR-014/FR-015 pre-turn gate (SECOND of the three). An agent
	// with no model to call is refused here — after the provider gate above
	// (a provider must exist before a model can, SC-013) and before the
	// window gate below (a model must exist before its window can be sized).
	// Same shape as its two siblings: a REAL failed turn with turn.start
	// already out, the LIFO defers firing on return, and a typed
	// EventKindError the SPA renders. The agent's stored model is NOT
	// touched — a refusal never re-points an agent at some other model
	// (US-3.AC4).
	if rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.needsModelSnapshot() {
		rp.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
		modelErr := fmt.Errorf("%w: agent_id=%s", ErrAgentModelUnassigned, rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID)
		llm := TranslateTurnError(modelErr)
		logger.WarnCF("agent", "Turn refused: the agent has no model assigned",
			map[string]any{"agent_id": rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID})
		rp.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindError,
			rp.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{
				Stage:     "model",
				ChatID:    rp.rc.rx.rr.rq.ri.rf.rt.ts.chatID,
				SessionID: string(rp.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
				Code:      string(llm.Code),
				Message:   llm.Message,
			},
		)
		rp.rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "model", llm)
		rp.rc.ret0 = turnResult{}
		rp.rc.ret1 = modelErr
		return agentLoopRunTurnConductorReturn, true
	}

	// ADR-066 D3 pre-turn gate (FR-008): a local endpoint that reported no
	// context window is refused, never run on a guessed number. Order:
	// needs_provider (ADR-067, above) → model_unassigned (ADR-068) →
	// context_window_unknown (here, third). It sits after the model switch
	// so a switch onto an unsized local model is refused too, and before
	// the first budget check so nothing ever computes a budget from W = 0.
	// Like the workspace gate above, the refusal is a REAL failed turn:
	// turn.start went out, the LIFO defers fire on return, and the typed
	// EventKindError is what the SPA renders.
	if _, windowExempt, windowUnknown := rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.windowSnapshot(); windowUnknown && !windowExempt {
		rp.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
		windowErr := fmt.Errorf("%w: agent_id=%s model=%s", ErrContextWindowUnknown, rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID, rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.Model)
		llm := TranslateTurnError(windowErr)
		rp.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindError,
			rp.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{
				Stage:     "context_window",
				ChatID:    rp.rc.rx.rr.rq.ri.rf.rt.ts.chatID,
				SessionID: string(rp.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
				Code:      string(llm.Code),
				Message:   llm.Message,
			},
		)
		rp.rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "context_window", llm)
		rp.rc.ret0 = turnResult{}
		rp.rc.ret1 = windowErr
		return agentLoopRunTurnConductorReturn, true
	}
	return 0, false
}

// selectTurnProvider trims the initial context, persists the user message, and selects provider candidates.
func (rp *agentLoopRunTurnPrepare) selectTurnProvider() agentLoopRunTurnConductorFlow {
	if !rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.NoHistory && !rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.budgetChecksExempt() {
		// FR-028: the pre-turn check reads the one budget B — the same value
		// windowTrim fits the suffix against — never the raw window — AND the
		// same tool surface windowTrim measures (sentToolSurfaceTokens, what
		// the turn actually sends). Charging the whole registry here, as this
		// site used to, fired the check on a conversation that fit and had
		// windowTrim evict one turn per turn.
		toolDefsTokens := rp.rc.rx.rr.rq.ri.rf.rt.al.sentToolSurfaceTokens(rp.rc.rx.rr.rq.ri.rf.rt.ts.agent, rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID, rp.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey)
		// C1: `messages` never carries the ephemeral system notes runTurn
		// injects into callMessages before the request that is actually
		// sent (scratchpad, workspace instructions — AGENT.md, up to
		// 262,144 bytes with no budget-aware cap — and the web-rendering
		// note); the compressed manifest note is already folded into
		// toolDefsTokens above. Without ephemeralSystemNoteTokens here, a
		// large AGENT.md alone can push the assembled request tens of
		// thousands of tokens past what this check saw, producing a
		// provider context_too_long on a window this check believed it was
		// protecting.
		nonMessageTokens := toolDefsTokens + rp.rc.rx.rr.rq.ri.rf.rt.al.ephemeralSystemNoteTokens(rp.rc.rx.rr.rq.ri.rf.rt.ts)
		if isOverContextBudgetTokens(agentContextBudget(rp.rc.rx.rr.rq.ri.rf.rt.ts.agent), rp.rc.rx.rr.rq.ri.messages, nonMessageTokens) {
			logger.WarnCF("agent", "Proactive window trim: context budget exceeded before LLM call",
				map[string]any{"session_key": rp.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey})
			if compression, ok := rp.rc.rx.rr.rq.ri.rf.rt.al.windowTrim(rp.rc.rx.rr.rq.ri.rf.rt.ts.agent, rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID, rp.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey); ok {
				rp.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
					EventKindContextCompress,
					rp.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.context.compress"),
					ContextCompressPayload{
						Reason:            ContextCompressReasonProactive,
						DroppedMessages:   compression.DroppedMessages,
						RemainingMessages: compression.RemainingMessages,
					},
				)
			}
			// Site-2: post-proactive-trim assembly.
			newHistory := rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.Sessions.GetHistory(rp.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey)
			rp.rc.rx.rr.rq.ri.messages = rp.rc.rx.rr.rq.ri.rf.rt.al.assembleMessages(
				rp.rc.rx.rr.rq.ri.rf.rt.turnCtx,
				rp.rc.rx.rr.rq.ri.rf.rt.ts,
				newHistory,
				rp.rc.rx.rr.rq.ri.rf.rt.ts.userMessage,
				rp.rc.rx.rr.rq.ri.rf.rt.ts.media,
				activeSkillNames(rp.rc.rx.rr.rq.ri.rf.rt.ts.agent, rp.rc.rx.rr.rq.ri.rf.rt.ts.opts),
			)
			trimProvider, trimModel := rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.primaryModelPair()
			rp.rc.rx.rr.rq.ri.messages = resolveMediaRefsWithOffload(
				rp.rc.rx.rr.rq.ri.messages, rp.rc.rx.rr.rq.ri.turnMediaStore, rp.rc.rx.rr.rq.ri.maxMediaSize, trimProvider, trimModel,
				&offloadSink{workDir: rp.rc.rx.rr.rq.ri.wsDir}, rp.rc.rx.rr.rq.ri.turnCatalog, rp.rc.rx.rr.rq.ri.turnRefcounter,
				rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.WorkspaceID,
			)
		}
	}

	// Save user message to session
	if !rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.NoHistory && (strings.TrimSpace(rp.rc.rx.rr.rq.ri.rf.rt.ts.userMessage) != "" || len(rp.rc.rx.rr.rq.ri.rf.rt.ts.media) > 0) {
		rootMsg := providers.Message{
			Role:    "user",
			Content: rp.rc.rx.rr.rq.ri.rf.rt.ts.userMessage,
			Media:   append([]string(nil), rp.rc.rx.rr.rq.ri.rf.rt.ts.media...),
		}
		if len(rootMsg.Media) > 0 {
			rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.Sessions.AddFullMessage(rp.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey, rootMsg)
		} else {
			rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.Sessions.AddMessage(rp.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey, rootMsg.Role, rootMsg.Content)
		}
	}

	rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.mu.RLock()

	var usedLight bool
	rp.rc.rx.rr.rq.ri.rf.rt.activeCandidates, rp.rc.rx.rr.rq.ri.activeModel, usedLight = rp.rc.rx.rr.rq.ri.rf.rt.al.selectCandidates(rp.rc.rx.rr.rq.ri.rf.rt.ts.agent, rp.rc.rx.rr.rq.ri.rf.rt.ts.userMessage, rp.rc.rx.rr.rq.ri.messages)
	rp.rc.rx.rr.rq.ri.rf.rt.activeProvider = rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.Provider
	if usedLight && rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.LightProvider != nil {
		rp.rc.rx.rr.rq.ri.rf.rt.activeProvider = rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.LightProvider
	}
	rp.rc.rx.rr.rq.ri.rf.rt.ts.agent.mu.RUnlock()
	rp.rc.rx.rr.rq.ri.pendingMessages = append([]providers.Message(nil), rp.rc.rx.rr.rq.ri.rf.rt.ts.opts.InitialSteeringMessages...)
	return agentLoopRunTurnConductorNext
}

const (
	agentLoopRunTurnIterationNext agentLoopRunTurnIterationFlow = iota
	agentLoopRunTurnIterationReturn
	agentLoopRunTurnIterationContinue
	agentLoopRunTurnIterationBreak
	agentLoopRunTurnIterationBreakL1
)

const (
	agentLoopRunTurnRequestNext agentLoopRunTurnRequestFlow = iota
	agentLoopRunTurnRequestReturn
	agentLoopRunTurnRequestContinue
	agentLoopRunTurnRequestBreak
)

const (
	agentLoopRunTurnResponseNext agentLoopRunTurnResponseFlow = iota
	agentLoopRunTurnResponseReturn
	agentLoopRunTurnResponseContinue
	agentLoopRunTurnResponseBreak
)

const (
	agentLoopRunTurnToolsNext agentLoopRunTurnToolsFlow = iota
	agentLoopRunTurnToolsReturn
	agentLoopRunTurnToolsContinue
	agentLoopRunTurnToolsBreak
	agentLoopRunTurnToolsBreakL1
)

// prepareToolSurface filters and validates the tool surface offered for this provider request.
func (rq *agentLoopRunTurnRequest) prepareToolSurface() agentLoopRunTurnRequestFlow {
	allAgentTools := rq.ri.rf.rt.ts.agent.Tools.GetAll()
	rq.policyFilteredTools, rq.filterTimePolicyMap = tools.FilterToolsByPolicy(allAgentTools, rq.ri.rf.rt.ts.agent.AgentType, rq.ri.rf.rt.ts.agent.LoadToolPolicy())

	// The unified `ToolSearch` infra tool is registration-gated, NOT policy-gated:
	// when compressed mode is on it must be callable by EVERY agent — including
	// deny-by-default agents (Ava/Mia/Ray) — or the model is shown `ToolSearch` in
	// its defs but its EXECUTION is denied, leaving every lazy tool permanently
	// unreachable. Force it into both the sent defs (policyFilteredTools) and
	// the execution-time policy snapshot (filterTimePolicyMap, consulted by
	// resolveToolPolicyAtExec) as "allow". This mirrors the defs force-include
	// in buildCompressedToolDefs at the authorization layer. (Found by live
	// validation: a deny-by-default agent called ToolSearch and the exec gate
	// denied it — reachability broke.)
	rq.policyFilteredTools = ensureInfraToolsExecutable(
		rq.ri.rf.rt.ts.agent.Tools, rq.policyFilteredTools, rq.filterTimePolicyMap)

	// ADR-088 D3/D4 (spec FR-007/009/010/011): evaluated ONCE per request,
	// right after the policy filter settles, so both the tool-surface
	// narrowing below and the rubric-note injection further down (and the
	// FR-010 question-budget bump after this iteration's tool-execution
	// loop) read the exact same verdict. See evaluateGoalForcing's own doc
	// comment for the full predicate.
	rq.goalForce = rq.ri.rf.rt.al.evaluateGoalForcing(rq.ri.rf.rt.ts, rq.ri.rf.rt.iteration, rq.policyFilteredTools)

	// FR-066: dedup invariant — tools[] must be name-unique after filter+assembly.
	// If a duplicate is detected, emit HIGH audit and return an error turn result
	// so the loop does not feed a malformed tool list to the LLM.
	if dedupErr := rq.ri.rf.rt.al.checkToolDedupInvariant(rq.ri.rf.rt.ts, rq.policyFilteredTools); dedupErr != nil {
		// issue #618 (fourth ungoverned member): this used to be built by
		// hand with fmt.Sprintf's %q verb — Go-string quoting, not JSON
		// quoting, unbounded, no contract schema, no allow-list entry, no
		// SPA detector. tools.ToolAssemblyDuplicatePayload (the generated
		// ToolAssemblyDuplicate schema) fixes all three: encoding/json
		// escaping (valid on invalid UTF-8 and C0/C1 control bytes alike),
		// a 1900-rune encoded budget via marshalWithinBudget, and a real
		// contract schema wired into the structured-failure allow-list.
		var denyMsg string
		if encoded, encErr := tools.ToolAssemblyDuplicatePayload(dedupErr.Error()); encErr == nil {
			denyMsg = string(encoded)
		} else {
			// Fully static fallback — no interpolated content — so a
			// marshal failure can never itself reintroduce the escaping
			// bug this fix exists to close. Reported rather than
			// swallowed: this branch discards dedupErr's real text.
			tools.ReportStructuredFailureMarshalError("pkg/agent.checkToolDedupInvariant", "", tools.ToolAssemblyDuplicateCode, encErr)
			denyMsg = `{"error":"tool_assembly_duplicate","message":"An internal error occurred while building the duplicate tool-assembly payload."}`
		}
		syntheticDenyMsg := providers.Message{Role: "system", Content: denyMsg}
		if !rq.ri.rf.rt.ts.opts.NoHistory {
			rq.ri.rf.rt.ts.agent.Sessions.AddFullMessage(rq.ri.rf.rt.ts.sessionKey, syntheticDenyMsg)
		}
		// ADR-058 §3.2/§10.A3: this branch used to also invoke FR-084's
		// per-turn synthetic-deny counter-and-abort helper before
		// returning below. FR-084 is deleted in full — this was the one
		// call site whose abort branch could NEVER fire
		// (every other call site's shouldAbort path was reachable; this
		// one always returns unconditionally on the very next line, so
		// its counter could reach at most 1 and a floor of 8 was
		// structurally unreachable, issue #595). Nothing behavioural is
		// lost: the turn already terminates unconditionally below.
		//
		// Fail the LLM call for this iteration by returning an error turn result.
		rq.ri.turnStatus = TurnEndStatusError
		rq.ret0 = turnResult{status: TurnEndStatusError, finalContent: denyMsg}
		rq.ret1 = dedupErr
		return agentLoopRunTurnRequestReturn
	}

	rq.ri.rf.providerToolDefs = nil
	switch {
	case rq.goalForce.layer1:
		// ADR-088 D3 Layer 1 (spec test 8's compressed-mode-suspension
		// row): "exactly the pair" is exact — bypass
		// buildCompressedToolDefs/stripInfraToolDefs entirely for this one
		// narrowed request, including the compressed-mode ToolSearch
		// force-through those helpers would otherwise apply.
		rq.ri.rf.providerToolDefs = tools.ToolsToProviderDefs(rq.goalForce.narrowed)
	case rq.ri.cfg.Tools.Manifest.Compressed:
		rq.ri.rf.providerToolDefs = rq.ri.rf.rt.al.buildCompressedToolDefs(rq.ri.rf.rt.ts, rq.policyFilteredTools)
	default:
		// Non-compressed defs path: strip manifest infra tools (ToolSearch)
		// before surfacing defs to the model. ToolSearch resolves through the
		// same global×agent merge as every other static builtin tool and is
		// seeded "allow" as real, explicit data for every agent
		// (pkg/coreagent/core.go), so it is typically present in
		// policyFilteredTools even when compression is off; but ToolSearch
		// exists only to drive the compressed manifest mechanism and has no
		// function when compression is off, so the model never sees it here
		// regardless of what the agent's tool-policy map resolves for it (see
		// stripInfraToolDefs for the mostly-deny vs. mostly-allow behavior
		// note) (#438).
		rq.ri.rf.providerToolDefs = tools.ToolsToProviderDefs(stripInfraToolDefs(rq.policyFilteredTools))
	}

	// Native web search support
	_, hasWebSearch := rq.ri.rf.rt.ts.agent.Tools.Get("search_web")
	rq.useNativeSearch = rq.ri.cfg.Tools.Web.PreferNative &&
		hasWebSearch &&
		func() bool {
			// Check if provider supports native search
			if ns, ok := rq.ri.rf.rt.activeProvider.(interface{ SupportsNativeSearch() bool }); ok {
				return ns.SupportsNativeSearch()
			}
			return false
		}()

	if rq.useNativeSearch {
		// Filter out client-side search_web tool
		filtered := make([]providers.ToolDefinition, 0, len(rq.ri.rf.providerToolDefs))
		for _, td := range rq.ri.rf.providerToolDefs {
			if td.Function.Name != "search_web" {
				filtered = append(filtered, td)
			}
		}
		rq.ri.rf.providerToolDefs = filtered
	}
	return agentLoopRunTurnRequestNext
}

// prepareCallMessages repairs history and injects the current ephemeral instruction messages.
func (rq *agentLoopRunTurnRequest) prepareCallMessages() {
	repairedHistory := rq.ri.messages
	if rq.ri.rf.rt.ts.opts.TranscriptStore != nil && rq.ri.rf.rt.ts.opts.TranscriptSessionID != "" && rq.ri.rf.rt.ts.agent != nil && rq.ri.rf.rt.ts.agent.Tools != nil {
		repaired, _ := repairHistory(rq.ri.rf.rt.turnCtx, rq.ri.messages, rq.ri.rf.rt.ts.opts.TranscriptStore, rq.ri.rf.rt.ts.opts.TranscriptSessionID, rq.ri.rf.rt.ts.agent.Tools, rq.ri.rf.rt.ts.agent.ID, rq.ri.rf.rt.ts.agent.LoadToolPolicy())
		repairedHistory = repaired
	}

	rq.ri.rf.callMessages = repairedHistory
	// Re-inject the acting agent's current scratchpad as an ephemeral system
	// message so the checklist survives context compression and the agent
	// always sees its plan at the top of the turn. The note is NOT persisted
	// to history — it is rebuilt fresh each turn from the task store.
	if rq.ri.rf.rt.ts.agent != nil {
		if note := rq.ri.rf.rt.al.buildScratchpadNote(rq.ri.rf.rt.ts.agent.ID, rq.ri.rf.rt.ts.opts.TranscriptSessionID); note != "" && len(rq.ri.rf.callMessages) > 0 {
			// Insert after callMessages[0] (the system prompt) so it immediately
			// follows the agent's identity, before the conversation history.
			injected := make([]providers.Message, 0, len(rq.ri.rf.callMessages)+1)
			injected = append(injected, rq.ri.rf.callMessages[0])
			injected = append(injected, providers.Message{Role: "system", Content: note})
			injected = append(injected, rq.ri.rf.callMessages[1:]...)
			rq.ri.rf.callMessages = injected
		}
		// Inject per-turn workspace instructions (AGENT.md) as an ephemeral
		// system message immediately after the system prompt. Empty/absent
		// instructions are a no-op — zero behavioral change.
		//
		// Ordering note (finding 10c, context-audit 2026-08 — ADR-088 D9
		// retires the ADR-078 D2 goal-pending note that used to sit between
		// this call and injectManifestNote below; buildGoalPendingNote/
		// injectGoalPendingNote, pkg/agent/goal_pending_note.go, are deleted
		// in full — instant activation leaves no pending state for a note to
		// describe): the remaining injectors (this one, injectWebRenderingNote,
		// injectManifestNote) insert at index 1 of the message array, so call
		// order alone determines final position — the LAST call ends up
		// CLOSEST to the system message. With every note present this turn,
		// final order is: [0] system prompt · [1] manifest note · [2]
		// web-rendering note · [3] workspace instructions · [4] scratchpad
		// (spliced above, before this call) · [5+] history. See
		// injectWorkspaceInstructions' own doc comment
		// (workspace_instructions.go) for the authoritative, single-sourced
		// version of this contract.
		rq.ri.rf.callMessages = injectWorkspaceInstructions(rq.ri.rf.callMessages, buildWorkspaceInstructionsNote(rq.ri.rf.rt.ts.opts.WorkspaceID))
		// Web-only: encourage Mermaid diagrams when the turn comes from the web
		// chat (the sole surface that renders them). Per-turn + surface-gated on
		// ts.channel — deliberately NOT in the cached system prompt, since one
		// agent serves multiple channels (see web_rendering_note.go).
		rq.ri.rf.callMessages = injectWebRenderingNote(rq.ri.rf.callMessages, buildWebRenderingNote(rq.ri.rf.rt.ts.channel))
		// ADR-088 D4 (spec FR-011, D3 amendment 2026-09-07): the goal
		// rubric + first-move instruction + define-goal skill quality
		// bar, injected exactly when the D3 base predicate holds
		// (goalForce.rubric — active goal AND an empty compiled record,
		// this turn's first LLM request) on EITHER origin — webchat gets
		// it alongside the narrowed two-tool surface below; a channel
		// origin gets the SAME note (with its conversational-ask
		// addendum) narrowed to {set_goal} alone (AskUserQuestion stays
		// permanently web-only). Neither origin forces a tool choice —
		// the note ASSISTS; the immediate post-turn correction
		// (checkGoalLoopAfterTurn, goal_loop.go) carries the guarantee.
		// buildGoalRubricInjectionNote returns "" when the predicate
		// does not hold, making this call a no-op on every non-goal turn.
		rq.ri.rf.callMessages = injectGoalRubricNote(rq.ri.rf.callMessages,
			buildGoalRubricInjectionNote(rq.goalForce.rubric, rq.goalForce.isWebchat))
		// Re-inject the compressed manifest of unloaded lazy tools as an ephemeral
		// system message. Like the scratchpad, it is rebuilt every turn (never
		// persisted) so it is never stale and not double-counted in the cached
		// system prompt. Injected only when Compressed is active and there are
		// unloaded lazy tools to list.
		//
		// Not on an ADR-088 D3 narrowed request (goalForce.layer1): that
		// request offers only {set_goal[, AskUserQuestion]}, and the block's
		// header tells the model to "call `ToolSearch`" to load a listed
		// tool — advertising a tool the request does not offer. UAT B-10
		// run 1 made exactly that un-offered ToolSearch call on the narrowed
		// request. The dispatch loop also refuses any call to a tool the
		// request did not offer (toolNotOfferedRefusal, tool_offer_gate.go).
		if rq.ri.cfg.Tools.Manifest.Compressed && !rq.goalForce.layer1 {
			rq.ri.rf.callMessages = injectManifestNote(rq.ri.rf.callMessages, rq.ri.rf.rt.al.buildToolManifestNote(rq.ri.rf.rt.ts, rq.policyFilteredTools))
		}
	}
	if rq.ri.gracefulTerminal {
		rq.ri.rf.callMessages = append(append([]providers.Message(nil), repairedHistory...), rq.ri.rf.rt.ts.interruptHintMessage())
		rq.ri.rf.providerToolDefs = nil
		rq.ri.rf.rt.ts.markGracefulTerminalUsed()
	}
}

// prepareLLMRequest applies request options and hooks, then records the outgoing LLM request.
func (rq *agentLoopRunTurnRequest) prepareLLMRequest() agentLoopRunTurnRequestFlow {
	narrowingActive := rq.goalForce.layer1 && !rq.ri.gracefulTerminal
	rq.ri.rf.rt.llmOpts = map[string]any{
		"max_tokens":       rq.ri.rf.rt.ts.agent.MaxTokens,
		"temperature":      rq.ri.rf.rt.ts.agent.Temperature,
		"prompt_cache_key": rq.ri.rf.rt.ts.agent.ID,
	}
	if rq.useNativeSearch && !narrowingActive {
		rq.ri.rf.rt.llmOpts["native_search"] = true
	}
	rq.ri.rf.rt.ts.agent.mu.RLock()
	agentThinkingLevel := rq.ri.rf.rt.ts.agent.ThinkingLevel
	rq.ri.rf.rt.ts.agent.mu.RUnlock()
	if agentThinkingLevel != ThinkingOff {
		if tc, ok := rq.ri.rf.rt.activeProvider.(providers.ThinkingCapable); ok && tc.SupportsThinking() {
			rq.ri.rf.rt.llmOpts["thinking_level"] = string(agentThinkingLevel)
		} else {
			logger.WarnCF("agent", "thinking_level is set but current provider does not support it, ignoring",
				map[string]any{"agent_id": rq.ri.rf.rt.ts.agent.ID, "thinking_level": string(agentThinkingLevel)})
		}
	}

	rq.ri.rf.rt.llmModel = rq.ri.activeModel
	if rq.ri.rf.rt.al.hooks != nil {
		llmReq, decision := rq.ri.rf.rt.al.hooks.BeforeLLM(rq.ri.rf.rt.turnCtx, &LLMHookRequest{
			Meta:             rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.request"),
			Model:            rq.ri.rf.rt.llmModel,
			Messages:         rq.ri.rf.callMessages,
			Tools:            rq.ri.rf.providerToolDefs,
			Options:          rq.ri.rf.rt.llmOpts,
			Channel:          rq.ri.rf.rt.ts.channel,
			ChatID:           rq.ri.rf.rt.ts.chatID,
			GracefulTerminal: rq.ri.gracefulTerminal,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if llmReq != nil {
				rq.ri.rf.rt.llmModel = llmReq.Model
				rq.ri.rf.callMessages = llmReq.Messages
				rq.ri.rf.providerToolDefs = llmReq.Tools
				rq.ri.rf.rt.llmOpts = llmReq.Options
			}
		case HookActionAbortTurn:
			rq.ri.turnStatus = TurnEndStatusError
			rq.ret0 = turnResult{}
			rq.ret1 = rq.ri.rf.rt.al.hookAbortError(rq.ri.rf.rt.ts, "before_llm", decision)
			return agentLoopRunTurnRequestReturn
		case HookActionHardAbort:
			_ = rq.ri.rf.rt.ts.requestHardAbort()
			rq.ri.turnStatus = TurnEndStatusAborted
			rq.ret0, rq.ret1 = rq.ri.rf.rt.al.abortTurn(rq.ri.rf.rt.ts, "before_llm", decision.Reason)
			return agentLoopRunTurnRequestReturn
		}
	}

	// The exact tool set this request offers, captured AFTER every step that
	// shapes providerToolDefs (goal-door narrowing, compressed manifest,
	// native-search strip, graceful-terminal clearing, BeforeLLM hook). The
	// tool loop below refuses any call to a tool outside it — see
	// toolNotOfferedRefusal (tool_offer_gate.go).
	rq.offeredTools = newOfferedToolSet(rq.ri.rf.providerToolDefs)

	// G1 fix: a cheap, non-blocking tool-call-argument progress callback,
	// so a `delegate action=status` poll on a running child can tell
	// "still generating a large tool-call argument" apart from "hung".
	// The incident this closes: an orchestrator polled a delegated
	// worker 75 times over 46s, saw no activity because the only output
	// was landing inside a streaming tool-call argument, and killed it
	// mid-write.
	//
	// This is passed to ChatStream as an explicit ARGUMENT, not smuggled
	// through the llmOpts map (ADR-059 W1/D1). The map route was fragile
	// in a way that had already produced one near-miss: a BeforeLLM hook
	// returning HookActionModify replaces llmOpts wholesale — possibly
	// with a nil map — so the key had to be re-injected after the hook
	// block, in a specific position, with a comment explaining why. A
	// parameter cannot be dropped by a hook.
	//
	// It is NOT, however, compile-enforced on implementers, and an earlier
	// version of this comment claimed it was. StreamingProvider is
	// consulted by runtime type assertion a few hundred lines below
	// (`activeProvider.(providers.StreamingProvider)`), so a provider that
	// keeps the old signature simply stops satisfying the interface: the
	// build succeeds and the turn silently drops to the non-streaming
	// path. That is exactly how ClaudeProvider shipped with no ChatStream
	// at all. The real enforcement is pkg/providers/compliance.go's
	// `var _` assertions plus streaming_forwarding_test.go, and any new
	// implementer has to be added to them by hand.
	//
	// The callback only does an atomic store per delta (see
	// turnState.recordToolCallProgress) — cheap and non-blocking, safe
	// to call synchronously from the provider's SSE read loop. ts is
	// this turn's own turnState, captured by the closure: concurrent
	// turns each get their own callback writing into their own state,
	// never into another turn's. That per-turn binding is the reason the
	// handler travels with the call rather than being set on the
	// provider, which is shared across every concurrent turn.
	rq.ri.rf.rt.onToolCallProgress = protocoltypes.OnToolCallProgress(func(p protocoltypes.ToolCallProgress) {
		rq.ri.rf.rt.ts.recordToolCallProgress(p)
	})

	rq.ri.rf.rt.al.emitEvent(
		EventKindLLMRequest,
		rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.request"),
		LLMRequestPayload{
			Model:         rq.ri.rf.rt.llmModel,
			MessagesCount: len(rq.ri.rf.callMessages),
			ToolsCount:    len(rq.ri.rf.providerToolDefs),
			MaxTokens:     rq.ri.rf.rt.ts.agent.MaxTokens,
			Temperature:   rq.ri.rf.rt.ts.agent.Temperature,
		},
	)

	systemPromptLen := 0
	if len(rq.ri.rf.callMessages) > 0 {
		systemPromptLen = len(rq.ri.rf.callMessages[0].Content)
	}
	logger.DebugCF("agent", "LLM request",
		map[string]any{
			"agent_id":          rq.ri.rf.rt.ts.agent.ID,
			"iteration":         rq.ri.rf.rt.iteration,
			"model":             rq.ri.rf.rt.llmModel,
			"messages_count":    len(rq.ri.rf.callMessages),
			"tools_count":       len(rq.ri.rf.providerToolDefs),
			"max_tokens":        rq.ri.rf.rt.ts.agent.MaxTokens,
			"temperature":       rq.ri.rf.rt.ts.agent.Temperature,
			"system_prompt_len": systemPromptLen,
		})
	logger.DebugCF("agent", "Full LLM request",
		map[string]any{
			"iteration":     rq.ri.rf.rt.iteration,
			"messages_json": formatMessagesForLog(rq.ri.rf.callMessages),
			"tools_json":    formatToolsForLog(rq.ri.rf.providerToolDefs),
		})
	return agentLoopRunTurnRequestNext
}

// beginIteration starts one turn iteration, applies limits, and injects pending steering or subturn messages.
func (ri *agentLoopRunTurnIteration) beginIteration() agentLoopRunTurnIterationFlow {
	if ri.rf.rt.ts.hardAbortRequested() {
		ri.turnStatus = TurnEndStatusAborted
		ri.ret0, ri.ret1 = ri.rf.rt.al.abortTurn(ri.rf.rt.ts, "turn_loop", hardInterruptAbortReason)
		return agentLoopRunTurnIterationReturn
	}

	ri.rf.rt.iteration = ri.rf.rt.ts.currentIteration() + 1
	ri.rf.rt.ts.setIteration(ri.rf.rt.iteration)

	// toolCallTruncationRepairUsed (ADR-087 D3.4) bounds a truncated
	// tool call to exactly one repair per turnLoop round — reset every
	// round (this `:=` runs again on every loop-body execution,
	// including via `continue turnLoop`), shared across this round's
	// three possible provider-call sites (D9).
	ri.toolCallTruncationRepairUsed = false

	// Hard ceiling: never exceed 2x MaxIterations regardless of pending messages or
	// graceful-interrupt state. This prevents an unbounded loop when the agent keeps
	// producing follow-up messages or the interrupt flag is never cleared.
	if hardCeiling := 2 * ri.rf.rt.ts.agent.MaxIterations; ri.rf.rt.iteration > hardCeiling {
		logger.WarnCF("agent", "Turn exceeded hard iteration ceiling, breaking unconditionally",
			map[string]any{
				"agent_id":     ri.rf.rt.ts.agentID,
				"turn_id":      ri.rf.rt.ts.turnID,
				"iteration":    ri.rf.rt.iteration,
				"max_iter":     ri.rf.rt.ts.agent.MaxIterations,
				"hard_ceiling": hardCeiling,
			})
		return agentLoopRunTurnIterationBreakL1
	}

	ri.rf.rt.ts.setPhase(TurnPhaseRunning)

	// SEC-26: Per-agent LLM call rate limit check. Runs once per turn
	// iteration, before the actual LLM call. The system agent is exempt.
	if ri.rf.rt.al.rateLimiter != nil && ri.cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour > 0 &&
		!security.IsPrivilegedAgent(ri.rf.rt.ts.agent.AgentType) {
		window := ri.rf.rt.al.rateLimiter.GetOrCreate(
			"agent:"+ri.rf.rt.ts.agent.ID+":llm_call",
			ri.cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour,
			time.Hour,
			security.ScopeAgent,
			ri.rf.rt.ts.agent.ID,
			"llm_call",
		)
		if result := window.Allow(); !result.Allowed {
			ri.rf.rt.al.recordRateLimitDenial(
				ri.rf.rt.ts,
				"agent_llm_calls_per_hour",
				RateLimitPayload{
					Scope:             string(security.ScopeAgent),
					Resource:          "llm_call",
					PolicyRule:        result.PolicyRule,
					RetryAfterSeconds: result.RetryAfterSeconds,
					AgentID:           ri.rf.rt.ts.agent.ID,
					ChatID:            ri.rf.rt.ts.chatID,
					SessionID:         string(ri.rf.rt.ts.routingSessionID),
				},
				map[string]any{"retry_after_seconds": result.RetryAfterSeconds},
			)
			ri.turnStatus = TurnEndStatusError
			// ADR-087 D6.8: a rate-limit denial makes no provider call —
			// a D6 continuation left unresolved by a prior round is
			// preserved by runTurn's deferred preserveTruncatedAccumulator
			// choke point, which covers this return like every other.
			ri.ret0 = turnResult{}
			ri.ret1 = fmt.Errorf("rate limit: %s (retry after %.0fs)",
				result.PolicyRule, result.RetryAfterSeconds)
			return agentLoopRunTurnIterationReturn
		}
	}

	if ri.rf.rt.iteration > 1 {
		if steerMsgs := ri.rf.rt.al.dequeueSteeringMessagesForScope(ri.rf.rt.ts.sessionKey); len(steerMsgs) > 0 {
			ri.pendingMessages = append(ri.pendingMessages, steerMsgs...)
		}
	} else if !ri.rf.rt.ts.opts.SkipInitialSteeringPoll {
		if steerMsgs := ri.rf.rt.al.dequeueSteeringMessagesForScopeWithFallback(ri.rf.rt.ts.sessionKey); len(steerMsgs) > 0 {
			ri.pendingMessages = append(ri.pendingMessages, steerMsgs...)
		}
	}

	// Check if parent turn has ended (SubTurn support)
	if ri.rf.rt.ts.parentTurnState != nil && ri.rf.rt.ts.IsParentEnded() {
		if !ri.rf.rt.ts.critical {
			logger.InfoCF("agent", "Parent turn ended, non-critical SubTurn exiting gracefully", map[string]any{
				"agent_id":  ri.rf.rt.ts.agentID,
				"iteration": ri.rf.rt.iteration,
				"turn_id":   ri.rf.rt.ts.turnID,
			})
			return agentLoopRunTurnIterationBreak
		}
		logger.InfoCF("agent", "Parent turn ended, critical SubTurn continues running", map[string]any{
			"agent_id":  ri.rf.rt.ts.agentID,
			"iteration": ri.rf.rt.iteration,
			"turn_id":   ri.rf.rt.ts.turnID,
		})
	}

	// Poll for pending SubTurn results
	if ri.rf.rt.ts.pendingResults != nil {
		select {
		case result, ok := <-ri.rf.rt.ts.pendingResults:
			if ok && result != nil && result.ForLLM != "" {
				content := ri.cfg.FilterSensitiveData(result.ForLLM)
				msg := providers.Message{Role: "user", Content: fmt.Sprintf("[SubTurn Result] %s", content)}
				ri.pendingMessages = append(ri.pendingMessages, msg)
			}
		default:
			// No results available
		}
	}

	// Inject pending steering messages
	if len(ri.pendingMessages) > 0 {
		resolvedPending := resolveMediaRefsWithOffload(
			ri.pendingMessages, ri.turnMediaStore, ri.maxMediaSize,
			candidateProvider(ri.rf.rt.activeCandidates), ri.activeModel,
			&offloadSink{workDir: ri.wsDir}, ri.turnCatalog, ri.turnRefcounter,
			ri.rf.rt.ts.opts.WorkspaceID,
		)
		totalContentLen := 0
		for i, pm := range ri.pendingMessages {
			ri.messages = append(ri.messages, resolvedPending[i])
			totalContentLen += len(pm.Content)
			if !ri.rf.rt.ts.opts.NoHistory {
				// Persist the original (unresolved) message to session history to preserve
				// compact media refs; resolved (base64) form is only used for the LLM request.
				ri.rf.rt.ts.agent.Sessions.AddFullMessage(ri.rf.rt.ts.sessionKey, pm)
			}
			logger.InfoCF("agent", "Injected steering message into context",
				map[string]any{
					"agent_id":    ri.rf.rt.ts.agent.ID,
					"iteration":   ri.rf.rt.iteration,
					"content_len": len(pm.Content),
					"media_count": len(pm.Media),
				})
		}
		ri.rf.rt.al.emitEvent(
			EventKindSteeringInjected,
			ri.rf.rt.ts.eventMeta("runTurn", "turn.steering.injected"),
			SteeringInjectedPayload{
				Count:           len(ri.pendingMessages),
				TotalContentLen: totalContentLen,
			},
		)
		ri.pendingMessages = nil
	}

	logger.DebugCF("agent", "LLM iteration",
		map[string]any{
			"agent_id":  ri.rf.rt.ts.agent.ID,
			"iteration": ri.rf.rt.iteration,
			"max":       ri.rf.rt.ts.agent.MaxIterations,
		})

	ri.gracefulTerminal, _ = ri.rf.rt.ts.gracefulInterruptRequested()
	return agentLoopRunTurnIterationNext
}

// tryPDFTextFallback retries a rejected native PDF request after converting its media to text.
func (rf *agentLoopRunTurnFallbacks) tryPDFTextFallback() bool {
	if !downgradePDFMediaToText(rf.callMessages) {
		return false
	}
	logger.WarnCF(
		"agent",
		"provider rejected request carrying a native PDF block — retrying with extracted text",
		map[string]any{
			"agent_id": rf.rt.ts.agent.ID,
			"model":    rf.rt.llmModel,
			"error":    rf.err.Error(),
		},
	)
	rf.response, rf.err = rf.callLLM(rf.callMessages, rf.providerToolDefs)
	return rf.err == nil
}

// ModelSyntheticImageRejection is the transcript-stamp value the loop sets on
// `ts.lastProducedModel` when the LLM provider refuses an image-bearing input
// and we synthesize a guidance reply in place of the raw error. The provider
// did NOT actually emit this turn — the stamp exists so the transcript
// `model` field does not mis-attribute the synthesized guidance to the
// refusing model. Downstream consumers (replay, future filters) can detect
// this sentinel by typed string-comparison; the "synthetic:" prefix is
// reserved for non-LLM-produced transcripts.
const ModelSyntheticImageRejection = "synthetic:image-rejection"

// synthesizeImageRejection turns an image-only capability rejection into actionable model guidance.
func (rf *agentLoopRunTurnFallbacks) synthesizeImageRejection(pe *ProviderError, rejectionErr error) bool {
	// FIX 5: this is the IMAGE-only friendly-rejection path — it must
	// consult ts.imageRetryDone, the image-class guard, NOT
	// ts.mediaRetryDone (the PDF-class guard). turn.go's design
	// comment on the two fields is explicit that the guard was split
	// per-class precisely so a PDF-class downgrade earlier in the
	// SAME turn can never consume the image-class budget (or vice
	// versa). Reading the wrong guard here meant a PDF downgrade
	// earlier in the turn silently blocked this friendly synthesis
	// for a LATER, unrelated image rejection — it fell through to
	// the generic classifier-driven strip-retry instead.
	if rf.rt.ts.imageRetryDone.Load() || rejectionErr == nil {
		return false
	}

	rejectionText := rejectionErr.Error()
	if pe != nil && pe.Body != "" {
		rejectionText = pe.Body
	}
	if !isImageRejectionMessage(rejectionText) || isPDFRejectionMessage(rejectionText) {
		return false
	}

	// No media is allowed here for compatibility with capability errors
	// returned after compaction. When media is present, every block must be
	// an image; a PDF or any other block makes this a mixed-media request.
	for _, message := range rf.callMessages {
		for _, mediaRef := range message.Media {
			if !startsWithCaseInsensitive(mediaRef, "data:image/") {
				return false
			}
		}
	}

	logger.WarnCF("agent", "model rejected image input — returning guidance instead of retrying without it",
		map[string]any{"agent_id": rf.rt.ts.agent.ID, "model": rf.rt.llmModel, "error": rejectionErr.Error()})
	rf.response = &providers.LLMResponse{
		Content: fmt.Sprintf(
			"I can't view images with the current model (%s). To work with images, switch this "+
				"agent to a model that supports image input, then try again.",
			rf.rt.llmModel,
		),
	}
	rf.err = nil
	rf.rt.ts.imageRetryDone.Store(true)
	rf.rt.ts.setLastProducedModel(ModelSyntheticImageRejection)
	logger.DebugCF("agent", "image-rejection synthesis stamped; llmModel retained in error log only",
		map[string]any{"agent_id": rf.rt.ts.agent.ID, "model": rf.rt.llmModel})
	return true
}

// callProviderOnce calls the configured provider or fallback chain once and records streaming progress.
// Delegated-turn rate-limit retries wrap this method in loop_provider_retry.go.
func (rt *agentLoopRunTurn) callProviderOnce(messagesForCall []providers.Message, toolDefsForCall []providers.ToolDefinition) (*providers.LLMResponse, error) {
	// Clear tool-argument progress when the round ends, on EVERY exit
	// path (success, error, retry, recovery). Placed here rather than
	// at the four call sites so no future path can forget it.
	//
	// Without this the signal keeps asserting "generating" for the rest
	// of the turn — including while the tool it just finished streaming
	// is actually EXECUTING. A worker blocked twenty minutes inside a
	// bash command would still report as generating, and an
	// orchestrator taught by this very feature to read that as "leave
	// it alone" would leave a genuinely hung child alone. That is the
	// original defect inverted: it killed healthy workers; this would
	// suppress the kill a hung one needs.
	defer rt.ts.clearToolCallProgress()

	// Normalize at the top of every provider call — initial and every
	// retry/recovery — so that timeout-recovery and context-overflow-recovery
	// paths (which rebuild callMessages via BuildMessages and continue back
	// here) are always normalized. The fast path is allocation-free on valid
	// histories, so per-call cost is negligible.
	messagesForCall = normalizeMessagesForProvider(messagesForCall)

	providerCtx, providerCancel := context.WithCancel(rt.turnCtx)
	rt.ts.setProviderCancel(providerCancel)
	defer func() {
		providerCancel()
		rt.ts.clearProviderCancel(providerCancel)
	}()

	rt.al.activeRequests.Add(1)
	defer rt.al.activeRequests.Done()

	if len(rt.activeCandidates) > 1 && rt.al.fallback != nil {
		fbResult, fbErr := rt.al.fallback.Execute(
			providerCtx,
			rt.activeCandidates,
			func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
				cat := rt.al.getCapabilityCatalog()
				budget := resizeBudgetForModel(cat, provider, model, int(catalog.DefaultResizeLimits.MaxBytes))
				candidateMessages, imageErr := attachTurnInspectionImagesWithBudget(ctx, messagesForCall, rt.inspectionImages, modelSupportsImage(cat, provider, model), budget)
				if imageErr != nil {
					return nil, imageErr
				}
				// FR-007: look up the provider instance that matches
				// this candidate's pinned Provider. Without this,
				// every fallback routes through activeProvider (the
				// primary's instance) — defeating the point of
				// provider-aware fallbacks. Falls back to
				// activeProvider when the candidate has no Provider
				// pinned (legacy wire shape) or no pool entry.
				p := rt.ts.agent.GetProviderForCandidate(providers.FallbackCandidate{Provider: provider, Model: model})
				if p == nil {
					p = rt.activeProvider
				}
				return p.Chat(ctx, candidateMessages, toolDefsForCall, model, rt.llmOpts)
			},
		)
		if fbErr != nil {
			return nil, fbErr
		}
		if fbResult.Provider != "" && len(fbResult.Attempts) > 0 {
			logger.InfoCF(
				"agent",
				fmt.Sprintf("Fallback: succeeded with %s/%s after %d attempts",
					fbResult.Provider, fbResult.Model, len(fbResult.Attempts)+1),
				map[string]any{"agent_id": rt.ts.agent.ID, "iteration": rt.iteration},
			)
		}
		// Phase 1B FR-013: record the model that actually produced
		// the response (may differ from the agent's primary model
		// when a fallback candidate was used).
		rt.ts.setLastProducedModel(fbResult.Model)
		rt.ts.markLastStreamerProducedModel(fbResult.Model)
		return fbResult.Response, nil
	}
	providerName := ""
	if len(rt.activeCandidates) > 0 {
		providerName = rt.activeCandidates[0].Provider
	}
	var imageErr error
	cat := rt.al.getCapabilityCatalog()
	budget := resizeBudgetForModel(cat, providerName, rt.llmModel, int(catalog.DefaultResizeLimits.MaxBytes))
	messagesForCall, imageErr = attachTurnInspectionImagesWithBudget(providerCtx, messagesForCall, rt.inspectionImages, modelSupportsImage(cat, providerName, rt.llmModel), budget)
	if imageErr != nil {
		return nil, imageErr
	}
	// Use streaming if the provider supports it and we have a streamer for this channel.
	if sp, ok := rt.activeProvider.(providers.StreamingProvider); ok && rt.al.bus != nil {
		logger.DebugCF("agent", "Provider supports streaming, checking for streamer", map[string]any{"channel": rt.ts.channel, "chat_id": rt.ts.chatID})
		if streamer, hasStreamer := rt.al.bus.GetStreamer(providerCtx, rt.ts.channel, rt.ts.chatID, rt.ts.transcriptSessionID); hasStreamer {
			logger.InfoCF("agent", "Using streaming for response", map[string]any{"channel": rt.ts.channel, "chat_id": rt.ts.chatID})
			// FIX 5a/5c: stamp the TRUE per-turn producer and this turn's own
			// ID before any token can flow — see stampStreamerProducerAgentID
			// and stampStreamerTurnID's doc comments. stampStreamerParentSpawnCallID
			// additionally stamps this turn's delegation-nesting correlation
			// (empty for a root turn) so a delegate's own streamed final
			// response round-trips through Finalize with the same
			// ParentSpawnCallID its non-streaming siblings carry — see its
			// own doc comment.
			rt.ts.stampStreamerProducerAgentID(streamer)
			rt.ts.stampStreamerTurnID(streamer)
			rt.ts.stampStreamerParentSpawnCallID(streamer)
			// #823: mint (or, for an ADR-087 D6 auto-continue round, reuse)
			// this round's message id BEFORE any token can flow — mirrors the
			// three stamps immediately above. nextRoundMessageID must run
			// before the stamp so the freshly-obtained streamer and this
			// round's later appendIntermediateAssistantTranscript call (if
			// this round ends in tool calls) agree on the SAME id.
			rt.ts.nextRoundMessageID()
			rt.ts.stampStreamerMessageID(streamer)
			var lastChunk string
			// Residual native tool-call markup must never reach the
			// live view. This is not only a rendering concern: the
			// gateway streamer PERSISTS what it accumulated from these
			// Update calls (wsStreamer.Finalize prefers its own buffer
			// over the turn's final content), so anything forwarded
			// here also lands in transcript.jsonl. Filtering at this
			// seam is what keeps the live bubble and the persisted
			// entry identical — and both clean. See
			// providers.StreamTextFilter.
			var streamFilter providers.StreamTextFilter
			resp, streamErr := sp.ChatStream(providerCtx, messagesForCall, toolDefsForCall, rt.llmModel, rt.llmOpts, func(accumulated string) {
				// B4: if the turn has been abandoned (stuck-goroutine detach),
				// suppress further frame emits so a zombie goroutine cannot
				// push frames to disconnected clients.
				if rt.ts.abandoned.Load() {
					abandonedWritesSuppressed.Add(1)
					return
				}
				visible := streamFilter.Visible(accumulated)
				// Send only the new delta (visible minus what we already sent).
				//
				// Defensive: this slice panics with index-out-of-range if a
				// provider ever emits an accumulated string SHORTER than its
				// predecessor. The contract is monotonic growth, but a provider
				// bug, a block reorder, or an SDK revision changing accumulation
				// semantics would otherwise take down the whole turn. Treat a
				// non-growing value as "nothing new" and skip it. The filter
				// upholds the same non-shrinking contract on its own output.
				if len(visible) < len(lastChunk) {
					logger.DebugCF("agent", "Streaming callback emitted a shorter accumulated string; ignoring", map[string]any{
						"previous_len": len(lastChunk),
						"new_len":      len(visible),
					})
					return
				}
				delta := visible[len(lastChunk):]
				lastChunk = visible
				if delta != "" {
					// This attempt-local counter is independent of the concrete
					// streamer. Some channels expose no buffer-length method, and
					// WebSocket creates a fresh streamer per round. Count before
					// Update so an attempted partial emit fails closed even if the
					// client disconnects during the write.
					rt.providerCallStreamedBytes.Add(int64(len(delta)))
					if err := streamer.Update(providerCtx, delta); err != nil {
						logger.DebugCF("agent", "Streaming update error (client may have disconnected)", map[string]any{"error": err.Error()})
					}
				}
			}, rt.onToolCallProgress)
			// Reconcile against the provider's final text. Two things
			// need this: the few bytes the filter holds back mid-stream
			// in case they start a marker split across SSE chunks, and
			// a provider that returned content without ever invoking
			// the callback. Without it those bytes would be dropped
			// silently — the streamer's buffer is what gets persisted.
			if streamErr == nil && resp != nil && !rt.ts.abandoned.Load() {
				finalVisible := resp.Content
				if om, isOrphan := providers.DetectOrphanToolCallMarkup(finalVisible); isOrphan {
					finalVisible = om.Prose
				}
				if len(finalVisible) > len(lastChunk) && strings.HasPrefix(finalVisible, lastChunk) {
					if err := streamer.Update(providerCtx, finalVisible[len(lastChunk):]); err != nil {
						logger.DebugCF("agent", "Streaming tail flush error (client may have disconnected)", map[string]any{"error": err.Error()})
					}
				}
			}
			// Do NOT finalize here — the turn may continue with tool calls.
			// Store the streamer so the turn-level code can finalize once,
			// after the last LLM call, preventing premature "done" frames
			// that tell the frontend the response is complete mid-turn.
			rt.ts.setLastStreamer(streamer)
			rt.ts.setLastProducedModel(rt.llmModel)
			// FR-013: also push to the streamer so Finalize stamps the
			// per-turn Model field on the streamed assistant entry.
			rt.ts.markLastStreamerProducedModel(rt.llmModel)
			return resp, streamErr
		}
	}
	rt.ts.setLastProducedModel(rt.llmModel)
	return rt.activeProvider.Chat(providerCtx, messagesForCall, toolDefsForCall, rt.llmModel, rt.llmOpts)
}
