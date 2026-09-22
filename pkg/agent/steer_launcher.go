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

	var (
		workspaceID string
		owner       string
		steeredBy   *session.SteeredBy
		stopStamp   *session.Stop
	)

	if req.SteeringSessionID != "" {
		if req.WorkspaceID != "" || req.Owner != "" {
			return steer.LaunchResult{}, fmt.Errorf(
				"steer: launch: %w: WorkspaceID/Owner must be empty for a steered launch (inherited from the steering session)",
				steer.ErrInvalidEdge)
		}

		// Deviation from I-1's literal text (stated in the phase-2 report):
		// "publishes the child under the parent's record lock" is NOT
		// implemented as one held lock spanning this read and the child's
		// write below. LifecycleStore.Lock(id) returns the exact
		// *sync.Mutex Load/Persist themselves acquire internally
		// (sync.Mutex is not reentrant, per that method's own doc comment)
		// — holding it here and then calling Load/Persist for the SAME
		// steering session id deadlocks every caller against itself. The
		// store's only externally-safe RMW primitive, Mutate, cannot
		// create a record where none exists (its callback receives a
		// plain *LifecycleRecord, nil when absent, with no way to hand a
		// new one back). Reads and writes below are therefore sequential,
		// each individually safe (Load/Persist/CreateSessionWithID/SetMeta
		// all take and release their own per-id lock), but NOT atomic as
		// one cross-call critical section — a concurrent Stop cascade
		// landing between this read and the child's Persist could in
		// principle race this launch. Unreachable today (WP-D's Canceller
		// is still the CP-0 "reaches nothing" stub), but a real gap once
		// I-6 lands; closing it needs a new store-level primitive (e.g.
		// LifecycleStore.MutateOrInsert), not a workaround here.
		steererMeta, metaErr := sessions.GetMeta(req.SteeringSessionID)
		if metaErr != nil {
			return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: resolve steering session %q: %v",
				steer.ErrInvalidEdge, req.SteeringSessionID, metaErr)
		}
		workspaceID = steererMeta.WorkspaceID
		owner = steererMeta.Owner

		steererRec, loadErr := lifecycle.Load(req.SteeringSessionID)
		if loadErr != nil {
			if !errors.Is(loadErr, session.ErrLifecycleNotFound) {
				return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: load steering session record: %v",
					steer.ErrStoreWrite, loadErr)
			}
			// I-1 round 9: this is the steering session's first delegation
			// — mint its own ordinary_root record now, under the same lock,
			// before publishing the child.
			steererRec = &session.LifecycleRecord{
				SessionID:      req.SteeringSessionID,
				Generation:     1,
				State:          session.LifecycleRunning,
				OwnerScopeKind: session.OwnerScopeHuman,
				WorkspaceID:    workspaceID,
				AgentID:        steererMeta.ActiveAgentID,
				Origin:         &session.Origin{Kind: session.OriginKind(steererMeta.Type)},
			}
			if persistErr := lifecycle.Persist(steererRec); persistErr != nil {
				return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: mint root record for steering session %q: %v",
					steer.ErrStoreWrite, req.SteeringSessionID, persistErr)
			}
		}

		rootID, walkErr := l.walkVerifiedRoot(req.SteeringSessionID, steererRec)
		if walkErr != nil {
			return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: %v", steer.ErrInvalidEdge, walkErr)
		}

		remainingDepth := l.startingRemainingDepth(steererRec)
		if remainingDepth <= 0 {
			return steer.LaunchResult{}, steer.ErrDepthExceeded
		}

		steeredBy = &session.SteeredBy{
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

		// I-1 US-4/AS-6: a launch under a parent carrying a Stop marker for
		// its CURRENT generation is stamped at launch and never starts.
		if steererRec.Stop != nil && steererRec.Stop.Generation == steererRec.Generation {
			stopStamp = &session.Stop{At: steererRec.Stop.At, Generation: 1, By: steererRec.Stop.By}
		}
	} else {
		// Ordinary-root launch (no steering session): inherit nothing,
		// invent nothing — the caller's own WorkspaceID/Owner apply
		// verbatim, including empty (US-1/AS-3: "a creator without a
		// workspace yields a child without one").
		workspaceID = req.WorkspaceID
		owner = req.Owner
	}

	title := req.Label
	if title == "" {
		title = req.Task
	}

	childID, idErr := session.NewSessionID()
	if idErr != nil {
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: mint session id: %v", steer.ErrStoreWrite, idErr)
	}
	sessionType := launchSessionType(req.Origin.Kind)

	var (
		meta        *session.UnifiedMeta
		identityErr error
	)
	if req.SteeringSessionID != "" {
		meta, identityErr = sessions.CreateSessionWithID(childID, req.SteeringSessionID, sessionType, "", req.TargetAgentID)
	} else {
		meta, identityErr = sessions.NewSession(sessionType, "", req.TargetAgentID)
		if meta != nil {
			childID = meta.ID
		}
	}
	if identityErr != nil {
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: identity: %v", steer.ErrStoreWrite, identityErr)
	}

	rollback := func() { _ = sessions.DeleteSession(childID) }

	patch := session.MetaPatch{Title: &title}
	if workspaceID != "" {
		patch.WorkspaceID = &workspaceID
	}
	if owner != "" {
		patch.Owner = &owner
	}
	if req.SteeringSessionID != "" {
		steeringID := req.SteeringSessionID
		patch.ParentSessionID = &steeringID
	}
	if setErr := sessions.SetMeta(childID, patch); setErr != nil {
		rollback()
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: owner/workspace/title: %v", steer.ErrStoreWrite, setErr)
	}

	// Record the task as the child's own first message — TWO writes, to
	// the two distinct logs a session keeps (unified.go's own doc comment:
	// "context.jsonl (agent loop), transcript.jsonl (UI)"). US-2 ("one
	// memory", the ring is gone) means reconstruction (I-3) builds the
	// child's first turn from its REAL history, not a special first-run
	// field, so both the model-visible history AND the UI transcript must
	// already be on disk before Dispatch ever reads them.
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
			rollback()
			return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: task transcript: %v", steer.ErrStoreWrite, appendErr)
		}
	}

	ownerScopeKind := session.OwnerScopeHuman
	ownerScopeID := ""
	if req.SteeringSessionID != "" {
		ownerScopeKind = session.OwnerScopeParentSession
		ownerScopeID = req.SteeringSessionID
	}

	origin := req.Origin
	rec := &session.LifecycleRecord{
		SessionID:      childID,
		Generation:     1,
		State:          session.LifecycleQueued,
		OwnerScopeKind: ownerScopeKind,
		OwnerScopeID:   ownerScopeID,
		WorkspaceID:    workspaceID,
		AgentID:        req.TargetAgentID,
		Origin:         &origin,
		SteeredBy:      steeredBy,
		Stop:           stopStamp,
	}
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		rollback()
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: edge: %v", steer.ErrStoreWrite, persistErr)
	}

	return steer.LaunchResult{SessionID: childID, Generation: 1}, nil
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
