// delegate_run.go: launch and manage durable delegated sessions.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// ErrRequestedSkillDenied and ErrRequestedSkillNotFound are the two distinct
// dispatch-time failure sentinels returned when a `delegate.run` call requests
// a skill (ADR-072 D9, spec FR-053/FR-054). Declared here in pkg/tools rather
// than in pkg/agent, so the corrective dispatch below can distinguish them
// with a plain errors.Is with no import cycle: pkg/agent already imports
// pkg/tools and MUST return these exact sentinel values (wrapped with %w),
// never a package-local duplicate, or the discrimination here silently
// degrades to the generic "Delegate execution failed" path.
//
// ErrRequestedSkillDenied: the resolved delegation target exists and the
// slug exists on some shelf visible to it, but the target is not granted
// it — reported to the caller via DelegationDeniedCode, reusing the
// existing discriminator per FR-053 rather than minting a new one.
//
// ErrRequestedSkillNotFound: the slug does not resolve to any installed
// skill on any shelf visible to the resolved delegation target at all —
// reported via SkillNotFoundCode (result.go), distinct from denial and
// never conflated with it (FR-054).
var (
	ErrRequestedSkillDenied   = errors.New("tools: requested_skill is not granted to the delegation target")
	ErrRequestedSkillNotFound = errors.New("tools: requested_skill does not resolve to any installed skill visible to the delegation target")
)

// requestedSkillDispatchFailureResult builds the structured, discriminated
// ToolResult for a `delegate.run` requested_skill dispatch failure (ADR-072
// D9, spec FR-053/FR-054): a pure function — it performs no lifecycle
// transition or task bookkeeping of its own, so the corrective dispatch can
// fold it into the same state/lifecycle bookkeeping as every other outcome.
//
// dispatchErr MUST be (or wrap) exactly ErrRequestedSkillDenied or
// ErrRequestedSkillNotFound — callers are expected to have already checked
// errors.Is before calling this; any other error falls through to the
// not-found branch defensively rather than panicking, since a wrongly
// classified error here is still strictly better than losing the
// distinction entirely.
func requestedSkillDispatchFailureResult(agentID, skillSlug string, dispatchErr error) *ToolResult {
	if errors.Is(dispatchErr, ErrRequestedSkillDenied) {
		return DelegationDeniedResult("delegate", &DelegationDenial{
			Reason: fmt.Sprintf(
				"delegation target %q is not granted the requested_skill %q — the receiver's own "+
					"grant is the only thing consulted (ADR-072 D9); the delegating agent's own skill "+
					"grants have no bearing on this outcome",
				agentID, skillSlug,
			),
			// No policy axis in the DelegationFailure contract (trust_set |
			// mode | depth) names a skill-grant refusal specifically — this
			// reuses the existing DelegationDeniedCode discriminator exactly
			// as FR-053 requires, without minting a new wire enum value
			// (Constraint #8: no new schema for this). DenyTrustSet is the
			// closest existing axis (a permission boundary the caller does
			// not control), and is also DelegationDeniedResult's own
			// fallback for a policy value outside the enum — so this is
			// documentary, not load-bearing.
			Policy:        DenyTrustSet,
			TargetAgentID: agentID,
		})
	}
	return requestedSkillNotFoundResult(agentID, skillSlug)
}

// requestedSkillNotFoundResult builds the structured not-found response for
// a `delegate.run` requested_skill slug that resolves to nothing on any
// shelf visible to the resolved delegation target (FR-054's
// SkillNotFoundCode — the SAME discriminator pkg/tools/skill.go's
// skillNotFoundResult mints for the `Skill` tool's own load path, minted
// once in result.go and reused here rather than duplicated). Deliberately a
// plain map[string]any marshaled with encoding/json, mirroring
// skillNotFoundResult's own shape exactly — see SkillNotFoundCode's doc
// comment (result.go) for why no generated wire shape exists or is needed.
func requestedSkillNotFoundResult(agentID, skillSlug string) *ToolResult {
	target := agentID
	if target == "" {
		target = "(self-delegation)"
	}
	message := fmt.Sprintf(
		"delegate: requested_skill %q does not resolve to any installed skill visible to delegation target %q.",
		skillSlug, target,
	)
	payload := map[string]any{
		"error":           SkillNotFoundCode,
		"tool":            "delegate",
		"skill":           skillSlug,
		"target_agent_id": agentID,
		"message":         message,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ErrorResult(message)
	}
	return &ToolResult{ForLLM: string(encoded), IsError: true}
}

// ErrSnapshotOverCap is returned by ValidateContextSnapshot when the
// DISCRETIONARY portion (references + notes) exceeds its byte or count cap.
// The MANDATORY core (task prompt + criteria + identity) is NEVER subject
// to this check (m4) — only what this function is handed.
var ErrSnapshotOverCap = errors.New("tools: curated context snapshot exceeds the discretionary cap")

// defaultSnapshotMaxBytes/defaultSnapshotMaxRefs mirror
// agent.defaultSnapshotMaxBytes/defaultSnapshotMaxRefs (ADR §Contract
// Surface: 8 KiB / 50 refs) — duplicated here for the same
// tools<->agent-cycle reason as ContextSnapshot itself.
const (
	defaultSnapshotMaxBytes = 8 * 1024
	defaultSnapshotMaxRefs  = 50
)

// ValidateContextSnapshot enforces R§8.5's deny-by-default, hard-capped
// curated context snapshot on the DISCRETIONARY portion only. See
// agent.ValidateContextSnapshot (the byte-for-byte identical sibling
// function on the agent-package side) for the full contract.
func ValidateContextSnapshot(snap *ContextSnapshot, maxBytes, maxRefs int) error {
	if snap == nil {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = defaultSnapshotMaxBytes
	}
	if maxRefs <= 0 {
		maxRefs = defaultSnapshotMaxRefs
	}
	if len(snap.References) > maxRefs {
		return fmt.Errorf("%w: %d references exceeds snapshot_max_refs (%d) — narrow the snapshot",
			ErrSnapshotOverCap, len(snap.References), maxRefs)
	}
	total := len(snap.Notes)
	for _, ref := range snap.References {
		total += len(ref)
	}
	if total > maxBytes {
		return fmt.Errorf("%w: %d bytes exceeds snapshot_max_bytes (%d) — narrow the snapshot",
			ErrSnapshotOverCap, total, maxBytes)
	}
	return nil
}

// delegateToolExecuteRun carries the shared state of executeRun across its stages.
type delegateToolExecuteRun struct {
	t                 *DelegateTool
	ctx               context.Context
	args              map[string]any
	task              string
	label             string
	agentID           string
	timeout           time.Duration
	requestedSkill    string
	snap              *ContextSnapshot
	delegateSessionID string
	goal              *steer.GoalSpec
}

func (t *DelegateTool) executeRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	dt := &delegateToolExecuteRun{t: t, ctx: ctx, args: args}

	if r0, stop := dt.validateRequest(); stop {
		return r0
	}

	// Delegation policy gate (FR-6.2): trust set + background mode + depth.
	// ADR-037: this is now the ONLY gate —
	// the legacy trust-only allowlistCheck/delegateChecker fallbacks (consulted
	// only when these were nil, which never happened in production) are
	// retired.
	//
	// FAIL CLOSED, not open, when no checker is wired: an unwired deny-checker
	// is a configuration error, never a permission grant. This is unreachable
	// in today's production wiring — pkg/agent/loop.go's registerSharedTools
	// unconditionally calls SetDelegationDenyCheckerBackground for every
	// agent — but removing the legacy fallback (which was itself deny-by-
	// default: config.IsDelegationAllowed/CanSpawnSubagent both returned false
	// on an unset policy) must not also remove the safety net for the NEXT
	// wiring bug: a new agent-construction path, a v0.3 plugin-system entry
	// point, or a refactor slip that forgets to call the setter. Do NOT
	// "simplify" this back to fail-open — CLAUDE.md Hard Constraint #6 exists
	// precisely to forbid a silent runtime default here.
	if r0, stop := dt.authorizeDelegation(); stop {
		return r0
	}

	return dt.launchAndDispatch(cb)
}

// launchAndDispatch is the entire ADR-091 run front: creation belongs to the
// injected launcher, Dispatch owns admission, and this method waits for
// neither the child turn nor its completion.
func (dt *delegateToolExecuteRun) launchAndDispatch(_ AsyncCallback) *ToolResult {
	if dt.t.launcher == nil {
		return ErrorResult("delegate: no session launcher configured")
	}
	targetAgentID := strings.TrimSpace(dt.agentID)
	if targetAgentID == "" {
		targetAgentID = strings.TrimSpace(ToolAgentID(dt.ctx))
	}
	launch, err := dt.t.launcher.Launch(dt.ctx, steer.LaunchRequest{
		SteeringSessionID: strings.TrimSpace(ToolTranscriptSessionID(dt.ctx)),
		TargetAgentID:     targetAgentID,
		Label:             strings.TrimSpace(dt.label),
		Task:              strings.TrimSpace(dt.task),
		Origin: steer.Origin{
			Kind:   steer.OriginKindDelegate,
			CallID: strings.TrimSpace(ToolCallID(dt.ctx)),
		},
		Goal: dt.goal,
		Limits: steer.Limits{
			TimeoutSeconds: int(dt.timeout / time.Second),
		},
		ToolExclusions: []string{string(ExcludedSwitchAgent)},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: launch: %v", err)).WithError(err)
	}
	dispatch, err := dt.t.launcher.Dispatch(dt.ctx, launch.SessionID, launch.Generation)
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: dispatch: %v", err)).WithError(err)
	}

	state := generated.DelegateSessionResponseState(dispatch.State)
	response := generated.DelegateSessionResponse{
		Generation: dispatch.Generation,
		SessionId:  launch.SessionID,
		State:      state,
	}
	if dt.t.getAgentRegistry != nil {
		if registry := dt.t.getAgentRegistry(); registry != nil {
			response.Is3p = registry.IsExternalCLI(targetAgentID)
		}
	}
	if dispatch.State == steer.DispatchQueued {
		position := dispatch.QueuePosition
		response.QueuePosition = &position
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: encode launch response: %v", err)).WithError(err)
	}
	result := string(payload)
	if dispatch.State == steer.DispatchQueued {
		result += fmt.Sprintf("\nQueued because the concurrency limit %d is in use; queue position %d. "+
			`Use delegate(action="cancel") with session_id=%q to drop this queued session.`,
			dispatch.ConcurrencyLimit, dispatch.QueuePosition, launch.SessionID)
	}
	return NewToolResult(result)
}

// validateRequest validates and resolves the delegation request arguments.
func (dt *delegateToolExecuteRun) validateRequest() (*ToolResult, bool) {
	var ok bool
	dt.task, ok = dt.args["task"].(string)
	if !ok || strings.TrimSpace(dt.task) == "" {
		return ErrorResult("task is required and must be a non-empty string"), true
	}

	dt.label, _ = dt.args["label"].(string)

	// agent_id is OPTIONAL (omit it to run a generic subagent under the
	// caller's own agent), but when the caller DOES supply the key, it must
	// not be blank — an empty string used to be silently accepted and
	// treated identically to "omitted", spawning a generic/default subagent
	// instead of the (presumably named) target the caller intended. Mirrors
	// the "task is required and must be a non-empty string" / shell.go's
	// "command is required and must be a non-empty string" validation style,
	// adapted for an optional field: only PRESENT-but-blank is rejected.

	if rawAgentID, present := dt.args["agent_id"]; present && rawAgentID != nil {
		s, ok := rawAgentID.(string)
		if !ok {
			return ErrorResult("agent_id must be a string"), true
		}
		if strings.TrimSpace(s) == "" {
			return ErrorResult("agent_id must be a non-empty string when provided; omit it to run a generic subagent"), true
		}
		dt.agentID = s
	}

	for _, removed := range []string{"async", "allow_" + "blocking_question"} {
		if _, present := dt.args[removed]; present {
			return ErrorResult("invalid_argument: " + removed), true
		}
	}

	// 0/absent means "use the configured delegation timeout"; a nonzero
	// value is bounds-checked and passed to the launcher.
	var timeoutErr error
	dt.timeout, timeoutErr = resolveDelegateTimeoutSeconds(dt.args)
	if timeoutErr != nil {
		return ErrorResult(timeoutErr.Error()), true
	}

	// ADR-072 D9 / FR-050: requested_skill is OPTIONAL, but — mirroring
	// agent_id's own "present-but-blank is rejected, absent is fine" rule
	// just above — a caller that supplies the key must not supply an empty
	// string, which would silently mean the same thing as omitting it while
	// looking like a deliberate request. The actual grant/existence
	// resolution happens against the CHILD's own ContextBuilder inside
	// spawnSubTurn (pkg/agent/subturn.go) — this file never resolves or
	// gates the slug itself (D9: "the receiver's grant is the real gate,
	// structurally, not by convention").

	if raw, present := dt.args["requested_skill"]; present && raw != nil {
		s, ok := raw.(string)
		if !ok {
			return ErrorResult("requested_skill must be a string"), true
		}
		if strings.TrimSpace(s) == "" {
			return ErrorResult("requested_skill must be a non-empty string when provided; omit it to request no skill"), true
		}
		dt.requestedSkill = s
	}

	// R§8.5 curated context snapshot — deny-by-default, hard-capped
	// discretionary portion. Rejected here (never silently truncated) if
	// over cap.

	if raw, present := dt.args["snapshot"]; present && raw != nil {
		rawMap, ok := raw.(map[string]any)
		if !ok {
			return ErrorResult("snapshot must be an object"), true
		}
		dt.snap = &ContextSnapshot{}
		if refsRaw, present := rawMap["references"]; present && refsRaw != nil {
			refsAny, ok := refsRaw.([]any)
			if !ok {
				return ErrorResult("snapshot.references must be an array of strings"), true
			}
			for _, r := range refsAny {
				s, ok := r.(string)
				if !ok {
					return ErrorResult("snapshot.references must be an array of strings"), true
				}
				dt.snap.References = append(dt.snap.References, s)
			}
		}
		if notesRaw, present := rawMap["notes"]; present && notesRaw != nil {
			s, ok := notesRaw.(string)
			if !ok {
				return ErrorResult("snapshot.notes must be a string"), true
			}
			dt.snap.Notes = s
		}
	}
	if err := ValidateContextSnapshot(dt.snap, dt.t.snapshotMaxBytes, dt.t.snapshotMaxRefs); err != nil {
		return ErrorResult(err.Error()).WithError(err), true
	}
	goal, err := parseDelegateGoal(dt.args["goal"])
	if err != nil {
		return ErrorResult(fmt.Sprintf("goal: %v", err)).WithError(err), true
	}
	dt.goal = goal
	return nil, false
}

// authorizeDelegation applies the delegation policy and resolves the authorized depth.
func (dt *delegateToolExecuteRun) authorizeDelegation() (*ToolResult, bool) {
	if dt.t.delegationDenyBackground != nil {
		if denial := dt.t.delegationDenyBackground(dt.ctx, dt.agentID); denial != nil {
			return DelegationDeniedResult("delegate", denial), true
		}
	} else {
		slog.Error("delegate: no delegation-deny checker installed — denying by default", "agent_id", dt.agentID)
		return DelegationDeniedResult("delegate", &DelegationDenial{
			Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
			Policy:        DenyTrustSet,
			TargetAgentID: dt.agentID,
		}), true
	}

	return nil, false
}

// minDelegateTimeoutSeconds/maxDelegateTimeoutSeconds bound a caller-supplied
// timeout_seconds override. Mirrors pkg/tools/shell.go's
// minTimeoutSeconds/maxTimeoutSeconds bounds-checking style/values for
// consistency across the tool surface.
const (
	minDelegateTimeoutSeconds = 1
	maxDelegateTimeoutSeconds = 3600
)

// resolveDelegateTimeoutSeconds parses args["timeout_seconds"] for
// action="run". Absent/nil/explicit 0 all resolve to 0 (time.Duration zero
// value), meaning "no override — use the spawner's own default
// (defaultSubTurnTimeout)", matching the schema's documented "0 = default
// (5 min)". A nonzero value is bounds-checked against
// [minDelegateTimeoutSeconds, maxDelegateTimeoutSeconds] and REJECTED —
// never silently clamped or ignored — when out of range, mirroring
// shell.go's resolveTimeoutSeconds.
func resolveDelegateTimeoutSeconds(args map[string]any) (time.Duration, error) {
	raw, present := args["timeout_seconds"]
	if !present || raw == nil {
		return 0, nil
	}
	var v int64
	switch n := raw.(type) {
	case float64:
		v = int64(n)
	case int:
		v = int64(n)
	case int32:
		v = int64(n)
	case int64:
		v = n
	default:
		return 0, fmt.Errorf("timeout_seconds must be a number")
	}
	if v == 0 {
		return 0, nil
	}
	if v < minDelegateTimeoutSeconds || v > maxDelegateTimeoutSeconds {
		return 0, fmt.Errorf(
			"timeout_seconds must be between %d and %d when non-zero (got %d); 0 means use the default",
			minDelegateTimeoutSeconds, maxDelegateTimeoutSeconds, v,
		)
	}
	return time.Duration(v) * time.Second, nil
}

// transitionLifecycle is a small helper that atomically transitions
// sessionID's durable record to the given terminal/non-terminal state +
// optional failedReason, preserving every other field. Errors are logged,
// not propagated — a durable-record write failure must never fail (or mask
// the outcome of) the underlying delegation itself.
//
// NOTE ON LOCKING (Correctness-MAJOR-3, honesty template): this delegates
// to session.TransitionSession (the single dual-store mediator, Defect #28)
// with a nil UnifiedStore — a delegate/subturn session has no chat-transcript
// meta.json at all (UnifiedStore.NewSession is never called for a child turn
// — see pkg/agent/subturn.go), so there is nothing to mirror onto. The
// mediator's atomic LifecycleStore.Mutate (the RMW primitive that holds the
// per-session striped lock across tail→fn→write) replaces the hand-rolled
// Mutate call this helper used to make directly. The prior Load+Persist pair
// was a non-atomic RMW: two concurrent transitions on the same session_id
// (the cancel-vs-complete race, S4 INV-3) raced — the loser either overwrote
// the winner's terminal record or was rejected by the immutable-terminal
// guard non-atomically. Under the mediator's Mutate the two serialize: the
// first writer lands its terminal state, the second sees that terminal tail
// under the lock and persistLocked rejects its same-generation write with
// ErrLifecycleTerminalImmutable (logged here, harmless — the record is already
// terminally correct). Callers MUST NOT already hold Lock(sessionID):
// sync.Mutex is not reentrant, and Mutate takes the lock ONCE internally.
// The sibling comment in message_parent.go parkNeedsInput mirrors this one.
func (t *DelegateTool) transitionLifecycle(sessionID string, state session.LifecycleState, failedReason string) {
	if t.lifecycle == nil || sessionID == "" {
		return
	}
	// nil UnifiedStore: delegate/subturn sessions have no chat-transcript meta
	// (see the doc comment above) — the mediator skips the mirror. t.lifecycle
	// (MessageParentLifecycleStore) satisfies session.LifecycleMutator, so no
	// type assertion is needed.
	if err := session.TransitionSession(t.lifecycle, nil, sessionID, state, failedReason); err != nil {
		slog.Warn("delegate: transitionLifecycle: dual-store transition failed", "session_id", sessionID, "state", state, "error", err)
	}
}

// killChildBackgroundShells kills sessionID's own background bash/exec
// shells AND every one of its durable descendants' (ADR-057 D8/R-13,
// FR-028/BDD-29 — see the sessionManager field doc for the full rationale)
// via the same KillAllForSessions primitive U16 exposes.
//
// [D8-CASCADE, 2026-08-04] Before this fix, this call reached ONLY sessionID's
// own shells — never a descendant's — because KillAllForSessions was called
// with the single-element []string{sessionID} regardless of whether
// sessionID had any children. The UAT gap-closure report (2026-08-03) proved
// this live against a real jim->ray->worker chain: hard-cancelling ray left
// worker's detached background HTTP server (a genuine descendant, owned by
// worker's own distinct session id per ProcessSession.OwnerSessionID's doc
// comment) serving for minutes afterward. This mirrors the identical fix
// pkg/agent/cancel.go's RequestCancel already applies to the chat-wide Stop
// path (resolveBackgroundKillSessionIDs) — a per-delegation cancel must
// reach the same descendant set a chat-wide Stop does.
//
// Returns (killed, failed, walkIncomplete). killed/failed were previously
// the only return values; executeCancel folds failed into what it tells the
// user. walkIncomplete is true when collectCancelDescendantSessionIDs hit a
// t.lifecycle.List failure partway through the walk — in that case
// killed/failed reflect only the PARTIAL subtree actually reached, and
// executeCancel MUST surface that rather than reporting a clean success (a
// caller reading "0 failed" must not conclude "the whole subtree was swept
// clean" when this is true). A nil sessionManager (SetSessionManager never
// called) is a silent no-op ((0, 0, false)), matching every other optional
// capability this tool accepts via a setter.
func (t *DelegateTool) killChildBackgroundShells(sessionID string) (killed, failed int, walkIncomplete bool) {
	if t.sessionManager == nil || sessionID == "" {
		return 0, 0, false
	}
	descendants, walkErr := t.collectCancelDescendantSessionIDs(sessionID)
	if walkErr != nil {
		walkIncomplete = true
		slog.Warn("delegate: cancel: descendant walk failed partway through — the background-shell kill "+
			"cascade below is INCOMPLETE; some descendants' background bash/exec work may be left running undetected",
			"session_id", sessionID, "error", walkErr)
	}
	ids := append([]string{sessionID}, descendants...)
	killed, failed = t.sessionManager.KillAllForSessions(ids)
	switch {
	case failed > 0:
		// A REAL kill failure (KillAllForSessions already excludes the
		// benign lost-the-race case from this count) deserves Warn, not
		// the same Info level a clean kill gets — this is the actionable
		// signal executeCancel's own result message is now built from.
		slog.Warn("delegate: cancel: failed to kill some background shells for cancelled child or its descendants",
			"session_id", sessionID, "descendant_count", len(descendants), "killed", killed, "failed", failed)
	case killed > 0:
		slog.Info("delegate: cancel: killed background shells for cancelled child and/or its descendants",
			"session_id", sessionID, "descendant_count", len(descendants), "killed", killed)
	}
	return killed, failed, walkIncomplete
}

// collectCancelDescendantSessionIDs performs a breadth-first walk of the
// durable SteeringSessionID edge (pkg/session/lifecycle.go) starting at
// rootSessionID and returns every reachable descendant's own session id
// (rootSessionID itself is never included).
//
// This mirrors agent.CollectDescendantSessionIDs (pkg/agent/cancel.go)
// byte-for-byte in walk semantics and error contract, duplicated here rather
// than called directly because pkg/tools cannot import pkg/agent (pkg/agent
// already imports pkg/tools, so the dependency can only run that direction).
// This is the same class of
// cross-package duplication cancel.go's own CollectDescendantSessionIDs doc
// comment describes for the (now-hoisted) pkg/gateway/websocket.go copy.
//
// Returns (nil, nil) when t.lifecycle is nil or rootSessionID is empty — not
// an error, the documented degrade-gracefully path for a DelegateTool that
// never had SetLifecycleStore called (killChildBackgroundShells's caller
// already checks t.sessionManager != nil before reaching here, but this
// function is defensive on its own terms too).
//
// Returns a non-nil error when ANY t.lifecycle.List call in the walk fails
// partway through — the returned slice is still the PARTIAL set discovered
// before the failure, never silently reported as "this node has no
// children." Callers MUST treat a non-nil error as a truncated view of the
// true descendant set, never as clean success with fewer descendants than
// expected (D8-CASCADE, mirroring FIX-5's identical Defect-2 fix in
// agent.CollectDescendantSessionIDs).
func (t *DelegateTool) collectCancelDescendantSessionIDs(rootSessionID string) ([]string, error) {
	if t.lifecycle == nil || rootSessionID == "" {
		return nil, nil
	}
	visited := map[string]struct{}{rootSessionID: {}}
	queue := []string{rootSessionID}
	var descendants []string
	var walkErrs []error
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		children, err := t.lifecycle.List(session.LifecycleFilter{SteeringSessionID: id})
		if err != nil {
			// This branch of the tree is now UNREACHABLE for this walk —
			// recorded (not just logged) so the caller can distinguish this
			// from "id has no children".
			walkErrs = append(walkErrs, fmt.Errorf("list children of %q: %w", id, err))
			continue
		}
		for _, rec := range children {
			if _, seen := visited[rec.SessionID]; seen {
				continue
			}
			visited[rec.SessionID] = struct{}{}
			descendants = append(descendants, rec.SessionID)
			queue = append(queue, rec.SessionID)
		}
	}
	if len(walkErrs) > 0 {
		return descendants, fmt.Errorf("descendant walk incomplete for root %q: %d branch(es) failed to list children: %w",
			rootSessionID, len(walkErrs), errors.Join(walkErrs...))
	}
	return descendants, nil
}

func cancelBackgroundShellWarnings(killFailed int, walkIncomplete bool) string {
	var warnings string
	if killFailed > 0 {
		warnings += fmt.Sprintf(
			" WARNING: %d of that session's background shell(s) could not be killed and may still be running.",
			killFailed,
		)
	}
	if walkIncomplete {
		warnings += " WARNING: the descendant walk for this session's background shells failed partway through — " +
			"some of its descendants may not have been reached at all and their background shells could still be running."
	}
	return warnings
}

func (t *DelegateTool) executeCancel(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	hard := false
	if raw, present := args["hard"]; present && raw != nil {
		b, ok := raw.(bool)
		if !ok {
			return ErrorResult("hard must be a boolean")
		}
		hard = b
	}
	// FAIL-CLOSED (MAJOR-1): caller-ownership verification is MANDATORY —
	// a Load error (not-found, corrupt tail, I/O) MUST NOT let the cancel
	// fall through against whatever session_id the caller named (a cross-
	// tenant DoS gated on an induced read error). Previously the ownership
	// check only ran when Load SUCCEEDED, so any Load error skipped it and
	// cancelHard/cancelSoft proceeded regardless. Now deny the cancel when
	// the lifecycle store is unconfigured OR when Load errors, mirroring
	// executeSteer/executeRespond/executeFollowUp's posture (the rest of
	// ADR-053's fail-closed contract).
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: cancel: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: cancel: %v", verr))
	}

	// Cancelling an already-terminal session doesn't corrupt any state
	// (cancelSoft/cancelHard against a session with no live turn are
	// harmless no-ops). #588 (N9) required this to NOT reuse the
	// success-shaped "cooperatively cancelled" / "hard-cancelled
	// immediately" message — that wording would misleadingly claim an
	// action was taken when nothing happened. The rec.Terminal() check
	// below is a plain check-then-return, not a check-then-act race: a
	// terminal session never leaves that state (L-3 immutable-terminal
	// invariant), so there's nothing for a concurrent writer to race here.
	//
	// RC-3 (UAT amplification-loop fix, 2026-08): #588's OWN requirement
	// was narrower than the original implementation of it — it bars reusing
	// the success-cancel WORDING, it does not require this to be a tool-call
	// FAILURE. Reporting IsError:true here made an orchestrating agent read
	// routine cleanup (a worker session finishing before the parent's
	// cancel call landed) as breakage: in one real UAT session 20 of 28
	// cancel calls hit this branch, and the caller re-issued cancels and
	// re-spawned workers in a loop instead of treating "already done" as
	// success. SessionManager.KillAll (pkg/tools/session.go:635-637)
	// already treats an already-terminal candidate as a silent no-op —
	// this brings cancel in line with that precedent. The response is
	// still a SUCCESS with wording distinct from "cooperatively
	// cancelled"/"hard-cancelled" so it can never be mistaken for "I just
	// cancelled something" — do not re-fix this back to ErrorResult; that
	// would resurrect the RC-3 amplification loop while only restoring a
	// stricter reading of #588 than #588 itself required.
	//
	// There is, however, a TOCTOU window BETWEEN this check and the
	// cancelSoft/cancelHard call below: a non-terminal session can terminate
	// in that gap, in which case Interrupt/InterruptSessionHard (ScopeSelfOnly)
	// finds no live turnState and returns (nil descendants, nil error) — a documented
	// no-op. The pre-fix code discarded the descendants return and STILL
	// reported the success-shaped message, so a cancel that landed nothing
	// looked identical to one that actually interrupted. The cancelSoft/
	// cancelHard calls below now capture descendants and, when
	// len(descendants)==0 and cerr==nil, return the same idempotent-no-op
	// shape as this pre-dispatch check UNLESS a real background-shell kill
	// failure or an incomplete descendant walk was also detected — that
	// failure signal is a genuine partial failure, not a clean no-op, and
	// signoff14's MEDIUM-3 fix (delegate_signoff14_test.go) requires it to
	// still reach the caller as an actionable error rather than being
	// silently downgraded to success by this fix. See the len(descendants)==0
	// branches below for that split.
	if rec.Terminal() {
		return NewToolResult(fmt.Sprintf(
			"Session %s is already terminal (%s) — no action needed.", sessionID, rec.State,
		))
	}

	// ADR-057 FR-028/BDD-29/D8/R-13: delegate action="cancel" now also kills
	// that child's OWN background shells AND every durable descendant's
	// (D8-CASCADE) — before this fix, it reached only the single named
	// session, silently leaking a grandchild's background work (see
	// killChildBackgroundShells's own doc comment). Fired unconditionally of
	// hard/soft and BEFORE the turn-level escalation below, mirroring
	// RequestCancel's own "decoupled from the active-turn gate" design
	// (pkg/agent/cancel.go): a background shell is an OS process, not a
	// steerable LLM turn, so there is no reason to make it wait through the
	// cooperative grace window that exists for the turn.
	//
	// killFailed and walkIncomplete are carried into both success messages
	// below: this call previously reported the same unconditional
	// "cancelled"/"hard-cancelled" success text regardless of whether a
	// background shell for this session (or a descendant) actually died, or
	// whether the descendant walk itself broke down partway through — a real
	// kill failure or an incomplete walk was visible only in a log line,
	// never to the caller. A partial cascade must not read as a clean
	// success (binding requirement): walkIncomplete forces that WARNING into
	// the response even when killFailed is 0, since a walk that broke down
	// may have left descendants entirely unvisited (0 attempted, not 0
	// failed). The turn-level cancel outcome (hard/soft, checked separately
	// just below) and the shell-kill outcome are distinct facts; a caller
	// needs both to know what actually happened.
	_, killFailed, walkIncomplete := t.killChildBackgroundShells(sessionID)

	if hard {
		if t.cancelHard == nil {
			return ErrorResult("delegate: no hard-cancel hook configured")
		}
		descendants, cerr := t.cancelHard(sessionID, "delegate cancel(hard=true)")
		if cerr != nil {
			return ErrorResult(fmt.Sprintf("delegate: cancel: %v", cerr)).WithError(cerr)
		}
		if len(descendants) == 0 {
			// RC-3: a clean TOCTOU miss (no real kill failure, no incomplete
			// walk) is an idempotent no-op, same as the pre-dispatch
			// rec.Terminal() branch above — see that branch's comment for
			// the full rationale. A real background-shell kill failure or
			// an incomplete descendant walk is NOT a clean no-op though:
			// signoff14's MEDIUM-3 fix requires that failure signal to
			// still surface as an actionable error, so this branch keeps
			// the original error shape whenever killFailed>0 or
			// walkIncomplete (see delegate_signoff14_test.go's
			// TestDelegateTool_Cancel_NothingToCancel_StillSurfacesShellKillWarnings).
			if killFailed > 0 || walkIncomplete {
				return ErrorResult(fmt.Sprintf(
					"delegate: cancel: session %s terminated between the terminal check and the cancel hook — nothing to cancel",
					sessionID,
				) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
			}
			return NewToolResult(fmt.Sprintf(
				"Session %s terminated between the terminal check and the cancel hook — no action needed.",
				sessionID,
			) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
		}
		t.transitionLifecycle(sessionID, session.LifecycleCancelled, "stopped_by_user")
		msg := fmt.Sprintf("Session %s hard-cancelled immediately.", sessionID)
		msg += cancelBackgroundShellWarnings(killFailed, walkIncomplete)
		return NewToolResult(msg)
	}

	if t.cancelSoft == nil {
		return ErrorResult("delegate: no soft-cancel hook configured")
	}
	softDescendants, cerr := t.cancelSoft(sessionID, "delegate cancel(hard=false)")
	if cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: cancel: %v", cerr)).WithError(cerr)
	}
	if len(softDescendants) == 0 {
		// RC-3: mirrors the hard-path branch above — a clean TOCTOU miss is
		// an idempotent no-op; a real kill failure or incomplete walk keeps
		// the original error shape (signoff14's MEDIUM-3 requirement).
		if killFailed > 0 || walkIncomplete {
			return ErrorResult(fmt.Sprintf(
				"delegate: cancel: session %s terminated between the terminal check and the cancel hook — nothing to cancel",
				sessionID,
			) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
		}
		return NewToolResult(fmt.Sprintf(
			"Session %s terminated between the terminal check and the cancel hook — no action needed.",
			sessionID,
		) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
	}

	// cancel(soft) = soft cooperative stop + a hard RequestCancel backstop
	// after the grace window, mirroring Interrupt/InterruptSessionHard's
	// existing two-phase escalation (steering.go, ScopeSelfOnly). The backstop only fires
	// if the session has NOT already reached a terminal state within grace.
	// (Comments-MINOR-3: the prior `// FR-...` prefix was a placeholder —
	// cancel is not a numbered FR; see ADR-053 R§Cancel/restart for the
	// two-phase prose this implements.)
	if t.cancelHard != nil {
		grace := t.cancelGrace
		go func() {
			time.Sleep(grace)
			if t.lifecycle != nil {
				if rec, lerr := t.lifecycle.Load(sessionID); lerr == nil && rec.Terminal() {
					return // cooperative stop already landed — no backstop needed
				}
			}
			// The descendants return closes the same TOCTOU window the
			// synchronous path above guards: if the session terminated
			// between the terminal check and this hard-cancel call,
			// cancelHard returns (nil, nil) and there is nothing left to
			// transition — skip transitionLifecycle rather than stamping a
			// redundant LifecycleCancelled onto an already-terminal record.
			backstopDescendants, cerr := t.cancelHard(sessionID, "delegate cancel(hard=false): grace elapsed")
			if cerr != nil {
				slog.Warn("delegate: cancel: hard-cancel backstop failed", "session_id", sessionID, "error", cerr)
				return
			}
			if len(backstopDescendants) == 0 {
				return
			}
			t.transitionLifecycle(sessionID, session.LifecycleCancelled, "stopped_by_user")
		}()
	}

	msg := fmt.Sprintf(
		"Session %s cooperatively cancelled; a checkpoint flush is expected within %s, "+
			"after which a hard cancel backstop fires if it has not stopped on its own.",
		sessionID, t.cancelGrace,
	)
	msg += cancelBackgroundShellWarnings(killFailed, walkIncomplete)
	return NewToolResult(msg)
}
