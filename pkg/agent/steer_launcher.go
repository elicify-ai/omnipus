// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Owner: WP-A. Phase 2: the real steer.SessionLauncher body — I-1/I-2's
// generalised launcher, replacing the CP-0 "not wired" stub. Reuses the
// shape of task_executor.go::createTaskSessionSync +
// mintTaskLifecycleRecord, generalised with the steered-by edge (I-1).
//
// Dispatch admits a session and starts its first turn by constructing a
// turnState directly (newTurnState + registerTurnIfAbsent + al.runTurn in a
// goroutine). All delegate and task call sites use this launcher; the former
// in-chat subturn execution path has been deleted.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// maxLaunchAncestorWalk bounds Launch's root-verification walk — mirrors
// steer_classify.go's maxChainWalk (I-1: "RootSessionID ... verified by
// walking the chain at launch"). A separate constant (not shared with the
// classifier) because the two walks run in different files with different
// call shapes; both use a visited-id set as the real cycle detector and
// this only as a defensive backstop.
const maxLaunchAncestorWalk = 4096

// defaultSteeredSessionTimeout is the built-in lifetime for a steered session
// when neither a per-call timeout_seconds nor performance.delegation_timeout_minutes
// was configured (D9: 0 = default; the founder raised the default from 5 to
// 30 minutes, 2026-09-23). An explicit call-level timeout and a configured
// value still win over this backstop (see Launch's limits resolution above).
const defaultSteeredSessionTimeout = 30 * time.Minute

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
	// A task-origin session needs its task id to survive Dispatch, which
	// routes it into task orchestration (dispatchLaunchedTask). Refuse an
	// empty TaskID here — at launch, before any write — rather than letting
	// it fail later at dispatch (the "dispatched task session has no task
	// origin" error). No other Origin.Kind has this requirement: delegate's
	// CallID is only the I-4 span key, and the span-emission path
	// (publishSteeredLaunch, steer_frames.go) skips a missing CallID, so an
	// empty CallID is not a later failure.
	if req.Origin.Kind == steer.OriginKindTask && req.Origin.TaskID == "" {
		return steer.LaunchResult{}, steer.ErrTaskIDRequired
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
	if req.Limits.TimeoutSeconds < 0 {
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: timeout_seconds must be >= 0")
	}
	if req.Limits.TimeoutSeconds == 0 {
		minutes, timeoutErr := l.al.GetConfig().Performance.EffectiveDelegationTimeoutMinutes()
		if timeoutErr != nil {
			return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w", timeoutErr)
		}
		timeout := time.Duration(minutes) * time.Minute
		if timeout <= 0 {
			timeout = defaultSteeredSessionTimeout
		}
		req.Limits.TimeoutSeconds = int(timeout.Seconds())
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
	if lifecycle := l.al.GetSessionLifecycleStore(); lifecycle != nil {
		if rec, err := lifecycle.Load(result.SessionID); err == nil {
			l.al.deliverSubagentStart(req.SteeringSessionID, rec, title)
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
	goalID, goalErr := l.createLaunchGoal(req, childID, title, req.TargetAgentID)
	if goalErr != nil {
		rollback()
		return steer.LaunchResult{}, goalErr
	}
	rollbackGoal := func() {
		if goalID != "" {
			_ = resolveGoalRecordStore().Delete(goalID)
		}
	}

	origin := req.Origin
	ownerKind := session.OwnerScopeHuman
	ownerID := ""
	if req.PlanID != "" {
		ownerKind = session.OwnerScopePlan
		ownerID = req.PlanID
	}
	rec := &session.LifecycleRecord{
		SessionID:      childID,
		Generation:     1,
		State:          session.LifecycleQueued,
		OwnerScopeKind: ownerKind,
		OwnerScopeID:   ownerID,
		Title:          title,
		GoalRef:        goalID,
		WorkspaceID:    req.WorkspaceID,
		AgentID:        req.TargetAgentID,
		Origin:         &origin,
	}
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		rollbackGoal()
		rollback()
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: edge: %v", steer.ErrStoreWrite, persistErr)
	}
	return steer.LaunchResult{SessionID: childID, Generation: 1}, nil
}

// launchSteered is Launch's steered path (I-1): the child's record is
// published under the steering session's own record lock via
// PublishChildUnderParentLock (pkg/session/lifecycle.go) — the primitive
// that closes the atomicity gap the CP-0 report flagged. The depth decision,
// Stop-marker stamp, and mandatory child writes run inside that callback, so
// a concurrent Stop cascade against the same steering session cannot land
// between "read the parent's Stop status" and "the child exists." Ancestor
// records are loaded before the callback because lifecycle shard locks are
// not re-entrant; the callback revalidates the direct edge before publishing.
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

	// Resolve the ancestor chain before taking the direct parent's shard
	// lock. LifecycleStore uses striped, non-reentrant mutexes, so loading an
	// ancestor from inside PublishChildUnderParentLock can self-deadlock when
	// two distinct IDs share a shard. The callback below revalidates the
	// direct edge before publishing, closing the race between this snapshot
	// and lock acquisition without taking another shard lock.
	preParent, preParentErr := lifecycle.Load(req.SteeringSessionID)
	preParentExisted := preParentErr == nil
	if preParentErr != nil && !errors.Is(preParentErr, session.ErrLifecycleNotFound) {
		return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: resolve steering lifecycle: %v",
			steer.ErrInvalidEdge, preParentErr)
	}
	rootID := req.SteeringSessionID
	parentDepth := 0
	if preParentExisted {
		var walkErr error
		rootID, parentDepth, walkErr = l.walkVerifiedRoot(req.SteeringSessionID, preParent)
		if walkErr != nil {
			return steer.LaunchResult{}, fmt.Errorf("steer: launch: %w: %v", steer.ErrInvalidEdge, walkErr)
		}
	}

	var resultGen int
	var goalID string
	pubErr := lifecycle.PublishChildUnderParentLock(req.SteeringSessionID,
		func(parentRec *session.LifecycleRecord, existed bool) (*session.LifecycleRecord, error) {
			if existed != preParentExisted || (existed && !sameSteeringEdge(parentRec, preParent)) {
				return nil, fmt.Errorf("steering edge changed while launch was acquiring the parent lock")
			}
			steererMeta, metaErr := sessions.GetMeta(req.SteeringSessionID)
			if metaErr != nil {
				return nil, fmt.Errorf("steer: launch: %w: resolve steering session %q: %v",
					steer.ErrInvalidEdge, req.SteeringSessionID, metaErr)
			}
			parentAgentID := strings.TrimSpace(steererMeta.ActiveAgentID)
			if parentAgentID == "" && l.al.GetConfig().Tools.Delegate.EffectiveRequireParentAgentID() {
				return nil, fmt.Errorf("steer: launch: %w: delegating agent identity is empty", steer.ErrInvalidEdge)
			}
			if parentAgentID == "" {
				logger.WarnCF("agent", "steer: launch: accepting empty parent agent identity by operator configuration",
					map[string]any{"session_id": req.SteeringSessionID, "config": "tools.delegate.require_parent_agent_id"})
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

			remainingDepth := l.startingRemainingDepth(parentRec, req.TargetAgentID, parentDepth)
			if remainingDepth <= 0 {
				return nil, steer.ErrDepthExceeded
			}

			steeredBy := &session.SteeredBy{
				SteeringSessionID: req.SteeringSessionID,
				RootSessionID:     rootID,
				ReportingTarget: session.ReportingTarget{
					SessionID: req.SteeringSessionID,
					Channel:   steererMeta.Channel,
					ChatID:    steererMeta.PeerID,
				},
				Authorization: session.Authorization{
					Mode:           launchAuthorizationMode(req.Origin.Kind),
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
			var goalErr error
			goalID, goalErr = l.createLaunchGoal(req, childID, title, steererMeta.ActiveAgentID)
			if goalErr != nil {
				_ = sessions.DeleteSession(childID)
				return nil, goalErr
			}

			origin := req.Origin
			resultGen = 1
			return &session.LifecycleRecord{
				SessionID:      childID,
				Generation:     1,
				State:          session.LifecycleQueued,
				OwnerScopeKind: session.OwnerScopeParentSession,
				OwnerScopeID:   req.SteeringSessionID,
				Title:          title,
				GoalRef:        goalID,
				WorkspaceID:    workspaceID,
				AgentID:        req.TargetAgentID,
				ParentAgentID:  parentAgentID,
				Origin:         &origin,
				SteeredBy:      steeredBy,
				Stop:           stopStamp,
			}, nil
		},
	)
	if pubErr != nil {
		// The lifecycle transaction compensates its own parent/child JSONL
		// writes. The unified session was staged inside the callback, so it has
		// a separate compensating delete on every publication failure.
		_ = sessions.DeleteSession(childID)
		if goalID != "" {
			_ = resolveGoalRecordStore().Delete(goalID)
		}
		return steer.LaunchResult{}, pubErr
	}
	return steer.LaunchResult{SessionID: childID, Generation: resultGen}, nil
}

func launchAuthorizationMode(kind steer.OriginKind) session.AuthorizationMode {
	if kind == steer.OriginKindTask {
		return session.AuthorizationModeTask
	}
	return session.AuthorizationModeDirect
}

func (l *SteerLauncher) createLaunchGoal(
	req steer.LaunchRequest,
	childID string,
	title string,
	authorAgentID string,
) (string, error) {
	if req.Goal == nil {
		return "", nil
	}
	if authorAgentID == "" {
		return "", fmt.Errorf("steer: launch: %w: goal author agent is empty", steer.ErrStoreWrite)
	}
	criteria := launchGoalCriteria(req.Goal.Criteria, authorAgentID)
	dod := launchGoalCriteria(req.Goal.DoD, authorAgentID)
	source := generated.GoalSourceChatCompiled
	if req.Origin.Kind == steer.OriginKindTask {
		source = generated.GoalSourceTaskExplicit
	}
	now := time.Now().UTC()
	g, err := goal.New(
		generated.GoalOwnerKindSession,
		childID,
		source,
		title,
		"",
		criteria,
		dod,
		goalTryLimit(l.al),
		now,
	)
	if err != nil {
		return "", fmt.Errorf("steer: launch: %w: goal: %v", steer.ErrStoreWrite, err)
	}
	store := resolveGoalRecordStore()
	if err := store.Create(g); err != nil {
		return "", fmt.Errorf("steer: launch: %w: create goal: %v", steer.ErrStoreWrite, err)
	}
	if _, err := store.Update(g.GoalID, func(current *goal.Goal) error {
		return current.Activate(childID, now)
	}); err != nil {
		_ = store.Delete(g.GoalID)
		return "", fmt.Errorf("steer: launch: %w: activate goal: %v", steer.ErrStoreWrite, err)
	}
	return g.GoalID, nil
}

func launchGoalCriteria(in []steer.Criterion, authorAgentID string) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(in))
	for _, criterion := range in {
		mapped := task.AcceptanceCriterion{
			Kind:     task.CriterionKind(criterion.Kind),
			Judgment: task.JudgmentKind(criterion.Judgment),
			Text:     criterion.Text,
			Author: task.CriterionAuthor{
				Kind: task.AuthorKindAgent,
				ID:   authorAgentID,
			},
		}
		if criterion.Check != nil {
			mapped.Check = &task.CriterionCheck{
				Command:          criterion.Check.Command,
				ExpectedExitCode: criterion.Check.ExpectedExitCode,
			}
		}
		out = append(out, mapped)
	}
	return out
}

// sameSteeringEdge compares only the immutable hierarchy identity that was
// resolved before the parent lock. Mutable lifecycle fields (state, Stop,
// generation) are intentionally read fresh inside the locked callback.
func sameSteeringEdge(current, snapshot *session.LifecycleRecord) bool {
	if current == nil || snapshot == nil {
		return current == nil && snapshot == nil
	}
	if current.SteeredBy == nil || snapshot.SteeredBy == nil {
		return current.SteeredBy == nil && snapshot.SteeredBy == nil
	}
	return current.SteeredBy.SteeringSessionID == snapshot.SteeredBy.SteeringSessionID &&
		current.SteeredBy.RootSessionID == snapshot.SteeredBy.RootSessionID
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
	if req.Origin.TaskID != "" {
		taskID := req.Origin.TaskID
		patch.TaskID = &taskID
	}
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
func (l *SteerLauncher) walkVerifiedRoot(steeringSessionID string, steererRec *session.LifecycleRecord) (string, int, error) {
	if steererRec.SteeredBy == nil {
		return steeringSessionID, 0, nil
	}
	lifecycle := l.al.GetSessionLifecycleStore()
	visited := map[string]bool{steeringSessionID: true}
	cur := steererRec.SteeredBy.SteeringSessionID
	for depth := 1; depth <= maxLaunchAncestorWalk; depth++ {
		if cur == "" || visited[cur] {
			return "", 0, fmt.Errorf("cycle or invalid ancestor at %q", cur)
		}
		visited[cur] = true
		ancestorRec, err := lifecycle.Load(cur)
		if err != nil {
			return "", 0, fmt.Errorf("unknown ancestor %q: %w", cur, err)
		}
		if ancestorRec.SteeredBy == nil {
			return cur, depth, nil
		}
		cur = ancestorRec.SteeredBy.SteeringSessionID
	}
	return "", 0, fmt.Errorf("ancestor chain exceeded the walk bound (%d)", maxLaunchAncestorWalk)
}

// startingRemainingDepth resolves the budget available at the steering
// session before minting its child. The new edge and the performance ceiling
// use the shared precedence function; an inherited budget can only tighten
// that result. parentDepth is verified by the ancestor walk before the parent
// record lock is taken.
func (l *SteerLauncher) startingRemainingDepth(steererRec *session.LifecycleRecord, targetAgentID string, parentDepth int) int {
	globalMaxDepth := 0
	if cfg := l.al.GetConfig(); cfg != nil {
		if configured, err := cfg.Performance.EffectiveMaxDelegationDepth(); err == nil {
			globalMaxDepth = configured
		}
	}
	depthCap := resolveEffectiveDelegationDepth(nil, globalMaxDepth)
	if steererRec.WorkspaceID != "" && steererRec.AgentID != "" && targetAgentID != "" {
		if edges, err := workspace.ReadDelegation(omnipusHome(), steererRec.WorkspaceID); err == nil {
			for i := range edges {
				edge := &edges[i]
				if edge.FromAgent != steererRec.AgentID || edge.ToAgent != targetAgentID {
					continue
				}
				if edge.Depth != nil && *edge.Depth <= 0 {
					return 0
				}
				depthCap = resolveEffectiveDelegationDepth(edge.Depth, globalMaxDepth)
				break
			}
		}
	}
	available := depthCap - parentDepth
	if steererRec.SteeredBy != nil && steererRec.SteeredBy.Authorization.RemainingDepth < available {
		available = steererRec.SteeredBy.Authorization.RemainingDepth
	}
	return available
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

func (al *AgentLoop) dispatchSteeredSession(ctx context.Context, sessionID string, gen int) (steer.DispatchResult, error) {
	return al.dispatchSteeredSessionWithReservation(ctx, sessionID, gen, false)
}

// dispatchSteeredSessionReserved resumes the FIFO head whose slot was already
// reserved by steerAdmission.release. It deliberately skips tryAdmit; running
// the promoted entry through ordinary admission would see its own reservation
// at the cap and requeue forever.
func (al *AgentLoop) dispatchSteeredSessionReserved(ctx context.Context, sessionID string, gen int) (steer.DispatchResult, error) {
	return al.dispatchSteeredSessionWithReservation(ctx, sessionID, gen, true)
}

// dispatchSteeredSession is I-2/I-3's authoritative admission decision.
// Reserves via I-6's live reserveDispatch guard,
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
func (al *AgentLoop) dispatchSteeredSessionWithReservation(_ context.Context, sessionID string, gen int, reserved bool) (steer.DispatchResult, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: no lifecycle store wired", steer.ErrStoreWrite)
	}
	gate := al.steerAdmission()
	if reserved && !gate.hasReservation(sessionID, gen) {
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: promoted reservation is stale", steer.ErrStaleGeneration)
	}
	rollbackReservation := func() {
		if reserved {
			al.drainSteerQueue(sessionID, gen)
		}
	}

	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		rollbackReservation()
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: %v", steer.ErrStoreWrite, err)
	}
	if ok, reason := reserveDispatch(rec, gen); !ok {
		rollbackReservation()
		switch reason {
		case steer.ErrDispatchCancelled.Error():
			return steer.DispatchResult{}, steer.ErrDispatchCancelled
		case steer.ErrStaleGeneration.Error():
			return steer.DispatchResult{}, steer.ErrStaleGeneration
		case steer.ErrTerminal.Error():
			return steer.DispatchResult{}, steer.ErrTerminal
		}
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: refused: %s", reason)
	}

	if !reserved {
		admitted, position, concurrencyLimit := gate.tryAdmit(sessionID, gen)
		if !admitted {
			rec.State = session.LifecycleQueued
			if persistErr := lifecycle.Persist(rec); persistErr != nil {
				gate.removeQueued(sessionID, gen)
				return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: %v", steer.ErrStoreWrite, persistErr)
			}
			return steer.DispatchResult{State: steer.DispatchQueued, ConcurrencyLimit: concurrencyLimit, QueuePosition: position, Generation: gen}, nil
		}
	}

	if rec.Origin != nil && rec.Origin.Kind == session.OriginKindTask && al.taskExecutor != nil {
		if dispatchErr := al.taskExecutor.dispatchLaunchedTask(rec, func() {
			al.drainSteerQueue(sessionID, gen)
		}); dispatchErr != nil {
			al.drainSteerQueue(sessionID, gen)
			return steer.DispatchResult{}, dispatchErr
		}
		rec.State = session.LifecycleRunning
		if persistErr := lifecycle.Persist(rec); persistErr != nil {
			al.drainSteerQueue(sessionID, gen)
			return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: %v", steer.ErrStoreWrite, persistErr)
		}
		if rec.SteeredBy != nil {
			al.deliverSubagentState(rec.SteeringSessionID(), rec, string(session.LifecycleRunning))
		}
		return steer.DispatchResult{State: steer.DispatchRunning, Generation: gen}, nil
	}

	ts, buildErr := al.reconstructSteeredTurn(rec, nil)
	if buildErr != nil {
		al.drainSteerQueue(sessionID, gen)
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: reconstruct: %v", steer.ErrStoreWrite, buildErr)
	}
	if !al.registerTurnIfAbsent(ts) {
		// Another dispatch already won the race for this sessionKey; ours
		// takes no turn and releases the admission slot it just claimed.
		al.drainSteerQueue(sessionID, gen)
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: concurrent dispatch already registered a turn", steer.ErrStaleGeneration)
	}

	rec.State = session.LifecycleRunning
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		al.activeTurnStates.CompareAndDelete(sessionID, ts)
		al.drainSteerQueue(sessionID, gen)
		return steer.DispatchResult{}, fmt.Errorf("steer: dispatch: %w: %v", steer.ErrStoreWrite, persistErr)
	}
	if rec.SteeredBy != nil {
		al.deliverSubagentState(rec.SteeringSessionID(), rec, string(session.LifecycleRunning))
	}

	// Fire-and-forget (I-2 "Return timing", founder decision round 9): the
	// delegate tool returns as soon as Launch+Dispatch have returned; there
	// is no ordering hook between the parent's tool result and the child's
	// start.
	go al.runDispatchedSteeredTurn(rec, ts, gen)

	return steer.DispatchResult{State: steer.DispatchRunning, Generation: gen}, nil
}

// runDispatchedSteeredTurn runs an admitted steered session's turn and
// disposes of its outcome. It owns the admission slot
// dispatchSteeredSessionWithReservation claimed for this turn and releases it
// explicitly on the way out — the same shape the task front uses (the release
// callback handed to task_executor.go::dispatchLaunchedTask, fired from
// runTask's deferred closure), and NOT turnState.al / turn_exit.go::Finish,
// which has no back-reference to release through for a steered turn.
//
// The release is deferred FIRST so it runs LAST: the turn's terminal write
// lands before the next queued session starts (D9: "a session whose turn has
// ended holds no slot"; I-3: "when a turn ends, the admission loop dispatches
// the oldest queued session").
func (al *AgentLoop) runDispatchedSteeredTurn(rec *session.LifecycleRecord, ts *turnState, gen int) {
	sessionID := rec.SessionID
	defer al.drainSteerQueue(sessionID, gen)

	runCtx := context.Background()
	cancel := func() {}
	if rec.SteeredBy != nil && rec.SteeredBy.Limits.TimeoutSeconds > 0 && !rec.CreatedAt.IsZero() {
		runCtx, cancel = context.WithDeadline(runCtx,
			rec.CreatedAt.Add(time.Duration(rec.SteeredBy.Limits.TimeoutSeconds)*time.Second))
	}
	defer cancel()
	result, runErr := al.runTurn(runCtx, ts)
	finalAudience := al.audienceFor(runCtx, steer.BoundaryFinalReply, sessionID)
	if runErr == nil && result.finalContent != "" && finalAudience == steer.AudienceUser {
		if publishErr := al.bus.PublishOutbound(runCtx, bus.OutboundMessage{
			Channel: ts.channel, ChatID: ts.chatID, Content: result.finalContent, SessionID: sessionID,
		}); publishErr != nil {
			logger.WarnCF("agent", "steer: publish dispatched final reply failed",
				map[string]any{"session_id": sessionID, "generation": gen, "error": publishErr.Error()})
		}
	}
	if rec.GoalRef != "" {
		al.finishSteeredGoalTurn(ts, &result, runErr)
		return
	}
	if finishErr := al.completeSteeredTurn(context.Background(), rec, result, runErr); finishErr != nil {
		logger.WarnCF("agent", "steer: complete dispatched turn failed",
			map[string]any{"session_id": sessionID, "generation": gen, "error": finishErr.Error()})
	}
}
