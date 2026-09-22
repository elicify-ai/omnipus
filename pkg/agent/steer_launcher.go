// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Owner: WP-A. Phase 2: the real steer.SessionLauncher body — I-1/I-2's
// generalised launcher, replacing the CP-0 "not wired" stub. Reuses the
// shape of task_executor.go::createTaskSessionSync +
// mintTaskLifecycleRecord, generalised with the steered-by edge (I-1).
//
// Scope note (stated once here, restated in the phase-2 report): Dispatch
// admits a session and starts its first turn by constructing a turnState
// directly (newTurnState + registerTurnIfAbsent + al.runTurn in a
// goroutine) rather than by rewiring subturn.go::spawnSubTurn's live
// synchronous execution path. Landing order CP-5 assigns "WP-C's delegate
// uses the launcher" to WP-C, not WP-A — the actual `delegate` tool
// call-site rewiring (and the accompanying deletion of
// subturn.go::createChildSession, the ephemeralSessionStore ring, and
// SubTurnConfig.Async) is deferred to that checkpoint, coupled to WP-G's
// still-incomplete 49-file test classification (only 3/49 rows were filled
// in as of this session — see the phase-2 report).
package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// maxLaunchAncestorWalk bounds Launch's root-verification walk — mirrors
// steer_classify.go's maxChainWalk (I-1: "RootSessionID ... verified by
// walking the chain at launch"). A separate constant (not shared with the
// classifier) because the two walks run in different files with different
// call shapes; both use a visited-id set as the real cycle detector and
// this only as a defensive backstop.
const maxLaunchAncestorWalk = 4096

// SteerLauncher implements steer.SessionLauncher (I-2), owned by WP-A.
type SteerLauncher struct {
	al *AgentLoop
}

var _ steer.SessionLauncher = (*SteerLauncher)(nil)

// NewSteerLauncher returns the real SessionLauncher, wired to al's shared
// stores and registry.
func NewSteerLauncher(al *AgentLoop) *SteerLauncher {
	return &SteerLauncher{al: al}
}

// launchSessionType maps a LaunchRequest's Origin.Kind onto the session
// type its minted session carries — the two front doors this launcher
// serves (delegate, create_task) map onto session.SessionTypeDelegate and
// session.SessionTypeTask respectively (I-2 "Reuse": "Both fronts call the
// one function").
func launchSessionType(kind steer.OriginKind) session.UnifiedSessionType {
	switch kind {
	case steer.OriginKindTask:
		return session.SessionTypeTask
	case steer.OriginKindChat:
		return session.SessionTypeChat
	case steer.OriginKindChannel:
		return session.SessionTypeChannel
	case steer.OriginKindScheduled:
		return session.SessionTypeScheduled
	case steer.OriginKindHeartbeat:
		return session.SessionTypeHeartbeat
	case steer.OriginKindVerifier:
		return session.SessionTypeVerifier
	default:
		// steer.OriginKindDelegate, steer.OriginKindPlan, steer.OriginKindHuman,
		// and anything unrecognised: a steered child not covered by a more
		// specific UnifiedSessionType is a delegate-shaped session (I-1's
		// SessionTypeDelegate doc comment: "a subordinate session minted for
		// a delegated child sub-turn").
		return session.SessionTypeDelegate
	}
}

// Launch implements steer.SessionLauncher (I-2). Atomic for the mandatory
// writes: identity, owner, workspace, title, edge, and — when the steering
// session has no record of its own — that session's own ordinary_root
// record, published under the parent's record lock (I-1). A failure at any
// step leaves no session and no lifecycle record; the caller receives an
// error naming the failed write via errors.Is against the returned
// steer.Err* sentinel.
func (l *SteerLauncher) Launch(_ context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	if req.Label == "" && req.Task == "" {
		return steer.LaunchResult{}, steer.ErrTitleRequired
	}
	if req.Goal != nil && len(req.Goal.Criteria) == 0 {
		// Mirrors create_task's own refusal text exactly (US-1/AS-5:
		// "a LaunchRequest.Goal that create_task would reject is rejected
		// with an identical message" — pkg/tools/task.go::validateRequest).
		return steer.LaunchResult{}, errors.New(
			"Add at least one acceptance criterion: say what must be true for this task to be done.")
	}
	if l.al == nil {
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: no AgentLoop wired", steer.ErrStoreWrite)
	}
	if _, ok := l.al.GetRegistry().GetAgent(req.TargetAgentID); !ok {
		return steer.LaunchResult{}, steer.ErrAgentUnknown
	}
	lifecycle := l.al.GetSessionLifecycleStore()
	sessions := l.al.GetSessionStore()
	if lifecycle == nil || sessions == nil {
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: session stores not wired", steer.ErrStoreWrite)
	}

	title := req.Label
	if title == "" {
		title = req.Task
	}
	sessionType := launchSessionType(req.Origin.Kind)

	if req.SteeringSessionID == "" {
		return l.launchOrdinaryRoot(sessions, lifecycle, req, title, sessionType)
	}
	result, err := l.launchSteered(sessions, lifecycle, req, title, sessionType)
	if err == nil {
		l.publishSteeredLaunch(req, result, title)
	}
	return result, err
}

// publishSteeredLaunch preserves the subagent span event while the old
// in-chat child executor is removed. The event is parent-scoped: its routing
// session is the steering session, while Label identifies the child.
func (l *SteerLauncher) publishSteeredLaunch(req steer.LaunchRequest, result steer.LaunchResult, title string) {
	if l == nil || l.al == nil || req.SteeringSessionID == "" || req.Origin.CallID == "" || result.SessionID == "" {
		return
	}
	l.al.emitEvent(EventKindSubTurnSpawn,
		EventMeta{Source: "steer", TracePath: "steer.launch", SessionKey: req.SteeringSessionID},
		SubTurnSpawnPayload{
			AgentID:           req.TargetAgentID,
			Label:             result.SessionID,
			SpanID:            subagentSpanID(req.Origin.CallID),
			ParentSpawnCallID: session.ToolCallID(req.Origin.CallID),
			TaskLabel:         title,
			SessionID:         req.SteeringSessionID,
		},
	)
	if lifecycle := l.al.GetSessionLifecycleStore(); lifecycle != nil {
		if rec, err := lifecycle.Load(result.SessionID); err == nil {
			l.al.deliverSubagentState(req.SteeringSessionID, rec, string(session.LifecycleQueued))
		}
	}
}

// launchOrdinaryRoot is Launch's no-steering-session path (US-1/AS-3: a
// creator with no workspace yields a child with none — never invented).
// No parent record to publish under, so this is a plain sequential mint —
// each store call individually lock-safe, no cross-record atomicity to
// provide.
func (l *SteerLauncher) launchOrdinaryRoot(
	sessions *session.UnifiedStore,
	lifecycle *session.LifecycleStore,
	req steer.LaunchRequest,
	title string,
	sessionType session.UnifiedSessionType,
) (steer.LaunchResult, error) {
	meta, identityErr := sessions.NewSession(sessionType, "", req.TargetAgentID)
	if identityErr != nil {
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: identity: %v", steer.ErrStoreWrite, identityErr)
	}
	childID := meta.ID
	rollback := func() { _ = sessions.DeleteSession(childID) }

	if err := l.writeChildMetaAndHistory(sessions, childID, title, req.WorkspaceID, req.Owner, "", req); err != nil {
		rollback()
		return steer.LaunchResult{}, err
	}

	origin := req.Origin
	rec := &session.LifecycleRecord{
		SessionID:      childID,
		Generation:     1,
		State:          session.LifecycleQueued,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    req.WorkspaceID,
		AgentID:        req.TargetAgentID,
		Origin:         &origin,
	}
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		rollback()
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: edge: %v", steer.ErrStoreWrite, persistErr)
	}
	return steer.LaunchResult{SessionID: childID, Generation: 1}, nil
}

// launchSteered is Launch's steered path (I-1): the child's record is
// published under the steering session's own record lock via
// PublishChildUnderParentLock (pkg/session/lifecycle.go) — the primitive
// that closes the atomicity gap the CP-0 report flagged. Everything that
// decides the edge (root walk, depth, the Stop-marker stamp) AND every
// mandatory child write (session identity, meta, history, transcript) runs
// inside that one callback, so a concurrent Stop cascade against the SAME
// steering session cannot land between "read the parent's Stop status" and
// "the child exists" — see PublishChildUnderParentLock's own doc comment
// for why this is deadlock-safe across arbitrarily many concurrent
// launches under different parents.
func (l *SteerLauncher) launchSteered(
	sessions *session.UnifiedStore,
	lifecycle *session.LifecycleStore,
	req steer.LaunchRequest,
	title string,
	sessionType session.UnifiedSessionType,
) (steer.LaunchResult, error) {
	if req.WorkspaceID != "" || req.Owner != "" {
		return steer.LaunchResult{}, fmt.Errorf(
			"steer: launch: %w: WorkspaceID/Owner must be empty for a steered launch (inherited from the steering session)",
			steer.ErrInvalidEdge)
	}

	childID, idErr := session.NewSessionID()
	if idErr != nil {
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: mint session id: %v", steer.ErrStoreWrite, idErr)
	}

	var resultGen int
	pubErr := lifecycle.PublishChildUnderParentLock(req.SteeringSessionID,
		func(parentRec *session.LifecycleRecord, existed bool) (*session.LifecycleRecord, error) {
			steererMeta, metaErr := sessions.GetMeta(req.SteeringSessionID)
			if metaErr != nil {
				return nil, fmt.Errorf("steer: launch: %w: resolve steering session %q: %v",
					steer.ErrInvalidEdge, req.SteeringSessionID, metaErr)
			}
			workspaceID := steererMeta.WorkspaceID

			if !existed {
				// I-1 round 9: this is the steering session's first
				// delegation — mint its own ordinary_root record now, in
				// the SAME critical section as the child's publication.
				parentRec.Generation = 1
				parentRec.State = session.LifecycleRunning
				parentRec.OwnerScopeKind = session.OwnerScopeHuman
				parentRec.WorkspaceID = workspaceID
				parentRec.AgentID = steererMeta.ActiveAgentID
				parentRec.Origin = &session.Origin{Kind: session.OriginKind(steererMeta.Type)}
			}

			rootID, walkErr := l.walkVerifiedRoot(req.SteeringSessionID, parentRec)
			if walkErr != nil {
				return nil, fmt.Errorf("steer: launch: %w: %v", steer.ErrInvalidEdge, walkErr)
			}
			remainingDepth := l.startingRemainingDepth(parentRec)
			if remainingDepth <= 0 {
				return nil, steer.ErrDepthExceeded
			}

			steeredBy := &session.SteeredBy{
				SteeringSessionID: req.SteeringSessionID,
				RootSessionID:     rootID,
				ReportingTarget: session.ReportingTarget{
					SessionID: req.SteeringSessionID,
					Channel:   steererMeta.Channel,
				},
				Authorization: session.Authorization{
					Mode:           session.AuthorizationModeDirect,
					RemainingDepth: remainingDepth - 1,
				},
				Limits:         req.Limits,
				ToolExclusions: req.ToolExclusions,
			}
			// I-1 US-4/AS-6: a launch under a parent carrying a Stop
			// marker for its CURRENT generation is stamped at launch and
			// never starts.
			var stopStamp *session.Stop
			if parentRec.Stop != nil && parentRec.Stop.Generation == parentRec.Generation {
				stopStamp = &session.Stop{At: parentRec.Stop.At, Generation: 1, By: parentRec.Stop.By}
			}

			if _, err := sessions.CreateSessionWithID(childID, req.SteeringSessionID, sessionType, "", req.TargetAgentID); err != nil {
				return nil, fmt.Errorf("steer: launch: %w: identity: %v", steer.ErrStoreWrite, err)
			}
			if err := l.writeChildMetaAndHistory(sessions, childID, title, workspaceID, "", req.SteeringSessionID, req); err != nil {
				_ = sessions.DeleteSession(childID)
				return nil, err
			}

			origin := req.Origin
			resultGen = 1
			return &session.LifecycleRecord{
				SessionID:      childID,
				Generation:     1,
				State:          session.LifecycleQueued,
				OwnerScopeKind: session.OwnerScopeParentSession,
				OwnerScopeID:   req.SteeringSessionID,
				WorkspaceID:    workspaceID,
				AgentID:        req.TargetAgentID,
				Origin:         &origin,
				SteeredBy:      steeredBy,
				Stop:           stopStamp,
			}, nil
		},
	)
	if pubErr != nil {
		return steer.LaunchResult{}, pubErr
	}
	return steer.LaunchResult{SessionID: childID, Generation: resultGen}, nil
}

// writeChildMetaAndHistory applies the child's meta patch (title,
// workspace, owner, parent edge) and seeds its first message into both the
// model-visible history and the durable transcript (US-2: "one memory" —
// reconstruction, I-3, builds the child's first turn from real, persisted
// history, not a special first-run field).
func (l *SteerLauncher) writeChildMetaAndHistory(
	sessions *session.UnifiedStore,
	childID, title, workspaceID, owner, steeringSessionID string,
	req steer.LaunchRequest,
) error {
	patch := session.MetaPatch{Title: &title}
	if workspaceID != "" {
		patch.WorkspaceID = &workspaceID
	}
	if owner != "" {
		patch.Owner = &owner
	}
	if steeringSessionID != "" {
		patch.ParentSessionID = &steeringSessionID
	}
	if setErr := sessions.SetMeta(childID, patch); setErr != nil {
		return fmt.Errorf("steer: launch: %w: owner/workspace/title: %v", steer.ErrStoreWrite, setErr)
	}

	if req.Task != "" {
		sessions.AddMessage(childID, "user", req.Task)
		taskEntry := session.TranscriptEntry{
			ID:        childID + "-task",
			Role:      "user",
			AgentID:   req.TargetAgentID,
			Content:   req.Task,
			Timestamp: time.Now().UTC(),
		}
		if appendErr := sessions.AppendTranscriptStrict(childID, taskEntry); appendErr != nil {
			return fmt.Errorf("steer: launch: %w: task transcript: %v", steer.ErrStoreWrite, appendErr)
		}
	}
	return nil
}

// walkVerifiedRoot resolves the cascade root for a new child steered by
// steeringSessionID (I-1: "RootSessionID ... verified by walking the chain
// at launch"). When the steering session is itself a root (SteeredBy ==
// nil), it IS the root. Otherwise walks its own ancestor chain — bounded by
// a visited-id set (the real cycle detector) plus maxLaunchAncestorWalk (a
// backstop) — refusing a cycle or an ancestor whose record cannot be read.
func (l *SteerLauncher) walkVerifiedRoot(steeringSessionID string, steererRec *session.LifecycleRecord) (string, error) {
	if steererRec.SteeredBy == nil {
		return steeringSessionID, nil
	}
	lifecycle := l.al.GetSessionLifecycleStore()
	visited := map[string]bool{steeringSessionID: true}
	cur := steererRec.SteeredBy.SteeringSessionID
	for i := 0; i < maxLaunchAncestorWalk; i++ {
		if cur == "" || visited[cur] {
			return "", fmt.Errorf("cycle or invalid ancestor at %q", cur)
		}
		visited[cur] = true
		ancestorRec, err := lifecycle.Load(cur)
		if err != nil {
			return "", fmt.Errorf("unknown ancestor %q: %w", cur, err)
		}
		if ancestorRec.SteeredBy == nil {
			return cur, nil
		}
		cur = ancestorRec.SteeredBy.SteeringSessionID
	}
	return "", fmt.Errorf("ancestor chain exceeded the walk bound (%d)", maxLaunchAncestorWalk)
}

// startingRemainingDepth is the RemainingDepth a new child inherits: the
// steering session's own remaining budget (already inductively verified at
// ITS launch) when it is itself steered, else the effective global
// delegation-depth ceiling for a first hop off a root.
//
// Reads the SAME config key (Agents.Defaults.SubTurn.MaxDepth) subturn.go's
// getSubTurnConfig still reads today — the D9 config-fold ("MaxDepth" moves
// to performance.max_delegation_depth) is deferred to land together with
// that file's own ring/spawnSubTurn deletion (see this file's package
// doc), so as not to split the single source of truth
// resolveEffectiveDelegationDepth's own doc comment requires.
func (l *SteerLauncher) startingRemainingDepth(steererRec *session.LifecycleRecord) int {
	if steererRec.SteeredBy != nil {
		return steererRec.SteeredBy.Authorization.RemainingDepth
	}
	globalMaxDepth := 0
	if cfg := l.al.GetConfig(); cfg != nil {
		globalMaxDepth = cfg.Agents.Defaults.SubTurn.MaxDepth
	}
	return resolveEffectiveDelegationDepth(nil, globalMaxDepth)
}

// Dispatch implements steer.SessionLauncher (I-2/I-3): a thin delegate onto
// AgentLoop.dispatchSteeredSession, which owns the real body (so the
// turn-end drain — triggered from turn_exit.go, which has no *SteerLauncher
// to call — can call the identical admission path).
func (l *SteerLauncher) Dispatch(ctx context.Context, sessionID string, gen int) (steer.DispatchResult, error) {
	if l.al == nil {
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: no AgentLoop wired", steer.ErrStoreWrite)
	}
	return l.al.dispatchSteeredSession(ctx, sessionID, gen)
}

// dispatchSteeredSession is I-2/I-3's authoritative admission decision.
// Reserves via I-6's reserveDispatch (WP-D's, admits everything until CP-3),
// then decides running/queued atomically against the turn-counting
// admission gate (steerAdmission), then registers the turn via
// registerTurnIfAbsent — in that order — so two concurrent dispatches for
// one session resolve to exactly one `running`. At the effective cap it
// queues instead of blocking or refusing.
//
// Deviation from "under the admission lock" read as one held
// LifecycleStore record lock (stated in the phase-2 report, mirroring
// Launch's own deviation note above): LifecycleStore.Lock(id) is the exact
// *sync.Mutex Load/Persist take internally, so holding it across a Load
// call for the SAME id deadlocks (sync.Mutex is not reentrant) — an
// earlier version of this function did exactly that and hung a test for
// its full 10-minute timeout. The guarantee I-2 actually requires —
// "two concurrent launches that both see one free slot cannot both be
// told running" — does not depend on the lifecycle record's own lock at
// all: it is enforced by steerAdmission.tryAdmit's own mutex-guarded
// counter and registerTurnIfAbsent's sync.Map.LoadOrStore compare-and-set,
// both independently atomic. The record read/write below is sequential
// (Load, decide, Persist — each individually lock-safe) and is a state
// MIRROR for observability, not the concurrency gate itself.
func (al *AgentLoop) dispatchSteeredSession(ctx context.Context, sessionID string, gen int) (steer.DispatchResult, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: no lifecycle store wired", steer.ErrStoreWrite)
	}

	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: %v", steer.ErrStoreWrite, err)
	}
	if rec.Terminal() {
		return steer.DispatchResult{}, steer.ErrTerminal
	}
	// Checked directly, NOT gated behind reserveDispatch's return value:
	// reserveDispatch (steer_cancel.go) is still WP-D's CP-0 stub, which
	// admits everything unconditionally until CP-3. The Stop marker and
	// Generation fields are I-1 (this lane's own row); "no dispatch starts
	// a turn on a stamped session" is the ADR's one-paragraph model, not a
	// property that may wait for another lane's checkpoint to hold.
	if rec.Stop != nil && rec.Stop.Generation == rec.Generation {
		return steer.DispatchResult{}, steer.ErrDispatchCancelled
	}
	if gen < rec.Generation {
		return steer.DispatchResult{}, steer.ErrStaleGeneration
	}
	if ok, reason := reserveDispatch(rec, gen); !ok {
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: refused: %s", reason)
	}

	admitted, position := al.steerAdmission().tryAdmit(sessionID, gen)
	if !admitted {
		rec.State = session.LifecycleQueued
		if persistErr := lifecycle.Persist(rec); persistErr != nil {
			return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: %v", steer.ErrStoreWrite, persistErr)
		}
		return steer.DispatchResult{State: steer.DispatchQueued, QueuePosition: position, Generation: gen}, nil
	}

	ts, buildErr := al.reconstructSteeredTurn(rec, nil)
	if buildErr != nil {
		al.steerAdmission().release(sessionID)
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: reconstruct: %v", steer.ErrStoreWrite, buildErr)
	}
	if !al.registerTurnIfAbsent(ts) {
		// Another dispatch already won the race for this sessionKey; ours
		// takes no turn and releases the admission slot it just claimed.
		al.steerAdmission().release(sessionID)
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: concurrent dispatch already registered a turn", steer.ErrStaleGeneration)
	}

	rec.State = session.LifecycleRunning
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: %v", steer.ErrStoreWrite, persistErr)
	}

	// Fire-and-forget (I-2 "Return timing", founder decision round 9): the
	// delegate tool returns as soon as Launch+Dispatch have returned; there
	// is no ordering hook between the parent's tool result and the child's
	// start.
	go func() { _, _ = al.runTurn(ctx, ts) }()

	return steer.DispatchResult{State: steer.DispatchRunning, Generation: gen}, nil
}
