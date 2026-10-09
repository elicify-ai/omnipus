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
// it — reported to the caller via SkillNotGrantedCode (#895: not a
// delegation_denied permission refusal, because the delegation itself was
// permitted).
//
// ErrRequestedSkillNotFound: the slug does not resolve to any installed
// skill on any shelf visible to the resolved delegation target at all —
// reported via SkillNotFoundCode (result.go), distinct from denial and
// never conflated with it (FR-054).
var (
	ErrRequestedSkillDenied   = errors.New("tools: requested_skill is not granted to the delegation target")
	ErrRequestedSkillNotFound = errors.New("tools: requested_skill does not resolve to any installed skill visible to the delegation target")
)

// ErrDelegateTargetCannotReport is returned by the session launcher when a
// `delegate` target's effective message_parent tool policy is "deny" (#948).
// Such a worker can finish its task and then has no way to deliver the result
// upward, which is indistinguishable from a hang. The launcher wraps this
// sentinel (declared here for the same tools<->agent-cycle reason as the
// requested_skill sentinels above) and the refusal happens before any child
// session exists.
var ErrDelegateTargetCannotReport = errors.New("tools: delegation target cannot deliver a result upward (message_parent denied)")

// delegateTargetCannotReportResult is the visible refusal for
// ErrDelegateTargetCannotReport. It names the agent and the policy, and says
// plainly that this is a policy configuration problem, not a lifecycle
// failure of a running child — nothing was started.
func delegateTargetCannotReportResult(agentID string) *ToolResult {
	return ErrorResult(fmt.Sprintf(
		"delegate: refused — agent %q cannot be delegated to: its effective %q tool policy is %q, "+
			"so it could do the work but would have no way to report the result back to you. "+
			"No child session was started (this is a policy configuration problem, not a failed or hung child). "+
			"Set %q to allow for that agent, or delegate to a different agent.",
		agentID, "message_parent", "deny", "message_parent",
	))
}

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
		return requestedSkillNotGrantedResult(agentID, skillSlug)
	}
	return requestedSkillNotFoundResult(agentID, skillSlug)
}

// requestedSkillNotGrantedResult builds the response for a requested_skill the
// target exists but is not granted (#895). It is deliberately NOT a
// delegation_denied / trust_set refusal: the delegation itself was permitted
// (policy, edge and depth were all fine) and only the skill argument is
// unusable, so wording it as a permission denial sent an operator to grant
// delegation permissions that were never the blocker. It stays distinct from
// skill_not_found (ADR-072 FR-053/FR-054: the receiver's own grant is the
// gate, and "not granted" must remain distinguishable from "no such skill"),
// and like SkillNotFoundCode it is an LLM-facing plain payload, not a wire
// type (see SkillNotFoundCode's doc comment in result.go).
func requestedSkillNotGrantedResult(agentID, skillSlug string) *ToolResult {
	message := fmt.Sprintf(
		"delegate: the delegation to %q is permitted, but requested_skill %q is not available to it — "+
			"the target agent has not been granted that skill (the receiver's own skill grant is the only "+
			"one consulted; yours has no bearing). This is not a delegation-permission problem. "+
			"Nothing was started. Retry without requested_skill, or have the skill granted to %q.",
		agentID, skillSlug, agentID,
	)
	payload := map[string]any{
		"error":           SkillNotGrantedCode,
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
	t              *DelegateTool
	ctx            context.Context
	args           map[string]any
	task           string
	label          string
	agentID        string
	timeout        time.Duration
	requestedSkill string
	snap           *ContextSnapshot
	goal           *steer.GoalSpec
}

func (t *DelegateTool) executeRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	dt := &delegateToolExecuteRun{t: t, ctx: ctx, args: args}

	if r0, stop := dt.validateRequest(); stop {
		return r0
	}

	// FR-016 (session-core U5a): an external-CLI worker must never create an
	// Omnipus helper — refused here, before authorization and before any
	// launch/child-session write, so a graph-permitted edge can never let one
	// through. Applies to omitted, explicit-self and other-target requests
	// alike (a helper is any native child session).
	if r0, stop := dt.refuseExternalCLIHelperCreation(); stop {
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
	// wiring bug: a new agent-construction path, a future plugin-system entry
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
		ToolExclusions:    []string{string(ExcludedSwitchAgent)},
		RequestedSkill:    strings.TrimSpace(dt.requestedSkill),
		ContextReferences: dt.snapshotReferences(),
		ContextNotes:      dt.snapshotNotes(),
	})
	if err != nil {
		// ADR-072 D9/FR-053/FR-054: a requested_skill dispatch failure is a
		// distinct, structured outcome (denied vs. not-found), never the
		// generic launch-error text — Launch (pkg/agent/steer_launcher.go)
		// wraps exactly ErrRequestedSkillDenied/ErrRequestedSkillNotFound
		// (declared in this file) for this one reason: so the discrimination
		// below is a plain errors.Is with no import cycle.
		if errors.Is(err, ErrRequestedSkillDenied) || errors.Is(err, ErrRequestedSkillNotFound) {
			return requestedSkillDispatchFailureResult(targetAgentID, strings.TrimSpace(dt.requestedSkill), err)
		}
		if errors.Is(err, ErrDelegateTargetCannotReport) {
			return delegateTargetCannotReportResult(targetAgentID).WithError(err)
		}
		if result := steeringUnavailableResult(err); result != nil {
			return result
		}
		return ErrorResult(fmt.Sprintf("delegate: launch: %v", err)).WithError(err)
	}
	dispatch, err := dt.t.launcher.Dispatch(dt.ctx, launch.SessionID, launch.Generation)
	if err != nil {
		if result := steeringUnavailableResult(err); result != nil {
			return result
		}
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
			`Use delegate(action="stop_all") with session_id=%q to stop this queued session.`,
			dispatch.ConcurrencyLimit, dispatch.QueuePosition, launch.SessionID)
	}
	return NewToolResult(result)
}

// snapshotReferences and snapshotNotes expose the validated curated context
// snapshot to the launch request; both are zero when no snapshot was given.
func (dt *delegateToolExecuteRun) snapshotReferences() []string {
	if dt.snap == nil {
		return nil
	}
	return dt.snap.References
}

func (dt *delegateToolExecuteRun) snapshotNotes() string {
	if dt.snap == nil {
		return ""
	}
	return dt.snap.Notes
}

// steeringUnavailableResult is ADR-093 D5. An inactive conversation is a
// normal, recoverable outcome: the model is told that a new message in this
// conversation resumes the request, and is never shown store machinery text.
// A nil result means err is some other failure and the caller formats it.
func steeringUnavailableResult(err error) *ToolResult {
	if !steer.IsSteeringUnavailable(err) {
		return nil
	}
	// Gate SFH#6: a revival-failed refusal gets the truthful variant sentence —
	// the standard sentence points at "send a new message", the exact action
	// that just failed. Same no-raw-text surface: the cause stays on the
	// result's WithError side (machine-readable), not in the user-visible
	// string.
	if errors.Is(err, steer.ErrSteeringRevivalFailed) {
		return ErrorResult(steer.SteeringRevivalFailedMessage).WithError(err)
	}
	return ErrorResult(steer.SteeringUnavailableMessage).WithError(err)
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

	for _, removed := range []string{"async", "allow_blocking_question"} {
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
	// resolution happens against the CHILD's own ContextBuilder, built in the
	// steered-session reconstruction path (pkg/agent/steer_reconstruct.go) —
	// this file never resolves or gates the slug itself (D9: "the receiver's
	// grant is the real gate, structurally, not by convention").

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
	if err := ValidateContextSnapshot(dt.snap, defaultSnapshotMaxBytes, defaultSnapshotMaxRefs); err != nil {
		return ErrorResult(err.Error()).WithError(err), true
	}
	goal, err := parseDelegateGoal(dt.args["criteria"], dt.args["dod"])
	if err != nil {
		return ErrorResult(fmt.Sprintf("goal: %v", err)).WithError(err), true
	}
	dt.goal = goal
	return nil, false
}

// refuseExternalCLIHelperCreation enforces FR-016 (session-core U5a): an
// external-CLI worker must NEVER create an Omnipus helper. A helper is any
// Omnipus-native child session, so the prohibition is BLANKET — it applies
// whether the worker targets itself, omits the target entirely, or names
// another agent with a graph-permitted edge.
//
// It is a property of the CALLER, resolved from the live registry's strict
// executor resolver (registry.IsExternalCLI → runner.ResolveDispatch), never of
// the target, and it runs BEFORE authorization and BEFORE any launch or
// child-session write (ADR-D4 / architect Q2): registration or a graph edge
// must never be the only thing standing between an external-CLI worker and a
// native helper.
func (dt *delegateToolExecuteRun) refuseExternalCLIHelperCreation() (*ToolResult, bool) {
	if dt.t.getAgentRegistry == nil {
		return nil, false
	}
	registry := dt.t.getAgentRegistry()
	if registry == nil {
		return nil, false
	}
	callerID := strings.TrimSpace(ToolAgentID(dt.ctx))
	if callerID == "" || !registry.IsExternalCLI(callerID) {
		return nil, false
	}
	targetAgentID := strings.TrimSpace(dt.agentID)
	slog.Warn("delegate: external-CLI worker refused helper creation (FR-016)",
		"caller_id", callerID, "target_agent_id", targetAgentID)
	return DelegationDeniedResult("delegate", &DelegationDenial{
		Reason: fmt.Sprintf(
			"an external-CLI worker (%q) cannot create an Omnipus helper: external-CLI agents run on a separate "+
				"engine and never delegate to native helper sessions (FR-016)",
			callerID,
		),
		Policy:        DenyTrustSet,
		TargetAgentID: targetAgentID,
	}), true
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
// (30 min)". A nonzero value is bounds-checked against
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
// to session.TransitionSession (the single dual-store mediator, Defect #28).
// The UnifiedStore half is t.unified, installed by SetUnifiedStore from the
// same shared store SteerLauncher.Launch mints every child's meta.json into
// (issue #947: passing nil here left sessions/<id>/meta.json at status=active
// after the lifecycle record had gone terminal). A nil t.unified — an unwired
// tool, or a harness that never constructed the shared store — keeps the
// mediator's "no chat-transcript meta" skip; it must not be the production
// shape. The mediator's atomic LifecycleStore.Mutate (the RMW primitive that holds the
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
//
// Used only by the failed-launch paths. It never lands a stop: only the owning execution lands
// `stopped` (ADR-20260928 D2), so no StopNote is passed here.
func (t *DelegateTool) transitionLifecycle(sessionID string, state session.LifecycleState, failedReason string) {
	if t.lifecycle == nil || sessionID == "" {
		return
	}
	// t.lifecycle (MessageParentLifecycleStore) satisfies
	// session.LifecycleMutator, so no type assertion is needed.
	// t.unified may be nil; TransitionSession then skips the mirror.
	if err := session.TransitionSession(t.lifecycle, t.unified, sessionID, state, failedReason, nil); err != nil {
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
// the only return values; executeStopAll folds failed into what it tells the
// user. walkIncomplete is true when collectCancelDescendantSessionIDs hit a
// t.lifecycle.List failure partway through the walk — in that case
// killed/failed reflect only the PARTIAL subtree actually reached, and
// executeStopAll MUST surface that rather than reporting a clean success (a
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
		slog.Warn("delegate: stop_all: descendant walk failed partway through — the background-shell kill "+
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
		// signal executeStopAll's own result message is now built from.
		slog.Warn("delegate: stop_all: failed to kill some background shells for stopped child or its descendants",
			"session_id", sessionID, "descendant_count", len(descendants), "killed", killed, "failed", failed)
	case killed > 0:
		slog.Info("delegate: stop_all: killed background shells for stopped child and/or its descendants",
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

// executeStopAll implements action="stop_all" (ADR-20261004, locked decision
// 2; renamed from cancel with no alias path). Stops that helper and every
// helper under it through the ONE session Stop people use too (founder
// one-stop decision, 2026-10-05): polite at once, forced 3 s later, landed
// only by each selected execution's owner.
func (t *DelegateTool) executeStopAll(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	// FAIL-CLOSED (MAJOR-1): caller-ownership verification is MANDATORY —
	// a Load error (not-found, corrupt tail, I/O) MUST NOT let the cancel
	// fall through against whatever session_id the caller named (a cross-
	// tenant DoS gated on an induced read error). Previously the ownership
	// check only ran when Load SUCCEEDED, so any Load error skipped it and
	// the stop proceeded regardless. Now deny the cancel when
	// the lifecycle store is unconfigured OR when Load errors, mirroring
	// executeSteer/executeRespond/executeResume's posture (the rest of
	// ADR-053's fail-closed contract).
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: stop_all: %v", lerr))
	}
	// The principal is not just the authorisation answer: it is stamped on
	// the durable Stop marker (session.Stop.By, ADR-091 I-1) and is what the
	// UI and the audit trail show as who stopped this session. This used to
	// call verifyCallerOwnsSession, which computes the same identity and
	// throws it away.
	by, verr := t.verifyCallerPrincipal(ctx, rec)
	if verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: stop_all: %v", verr))
	}

	// An already-terminal session answers a SUCCESS with wording distinct
	// from a stop request, so it can never be mistaken for "I just stopped
	// something" (#588 N9). It is deliberately not an error (RC-3, UAT
	// amplification loop: orchestrators re-issued stops and re-spawned
	// workers when routine "already done" read as breakage) — do not turn
	// it back into ErrorResult. A session can still terminate between this
	// check and the stop hook; that TOCTOU miss (reached nothing) answers the
	// same idempotent no-op shape below, unless a background-shell kill
	// failure or an incomplete descendant walk was detected (signoff14
	// MEDIUM-3: that partial failure stays an actionable error).
	if rec.Terminal() {
		return NewToolResult(fmt.Sprintf(
			"Session %s is already terminal (%s) — no action needed.", sessionID, rec.State,
		))
	}

	if t.stop == nil {
		return ErrorResult("delegate: stop_all: no stop capability is configured; nothing was stopped")
	}
	// ADR-057 FR-028/BDD-29/D8/R-13: stop_all also kills that child's OWN
	// background shells AND every durable descendant's (D8-CASCADE), before
	// the turn-level stop: a background shell is an OS process, not a turn,
	// so it does not wait for the polite stop. killFailed and walkIncomplete
	// are carried into the reply — a partial cascade must not read as a clean
	// success; walkIncomplete forces the WARNING even when killFailed is 0.
	_, killFailed, walkIncomplete := t.killChildBackgroundShells(sessionID)

	reached, cerr := t.stop(sessionID, by, "delegate stop_all")
	if cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: stop_all: %v", cerr) + cancelBackgroundShellWarnings(killFailed, walkIncomplete)).WithError(cerr)
	}
	if len(reached) == 0 {
		// RC-3: a clean TOCTOU miss (no real kill failure, no incomplete
		// walk) is an idempotent no-op, same as the pre-dispatch
		// rec.Terminal() branch above. A real background-shell kill failure
		// or an incomplete descendant walk is NOT a clean no-op (signoff14
		// MEDIUM-3), so it keeps the error shape.
		if killFailed > 0 || walkIncomplete {
			return ErrorResult(fmt.Sprintf(
				"delegate: stop_all: session %s terminated between the terminal check and the stop hook — nothing to stop",
				sessionID,
			) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
		}
		return NewToolResult(fmt.Sprintf(
			"Session %s terminated between the terminal check and the stop hook — no action needed.",
			sessionID,
		) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
	}
	// One Stop, one reply (founder one-stop decision, 2026-10-05). Nothing is
	// landed here: each selected execution's owner lands `stopped` after its
	// running work has shut down (ADR-20260928 D2/D4); the one Stop forces a
	// turn that ignores the polite request 3 s later.
	msg := fmt.Sprintf("Stop requested for session %s and its helpers; "+
		"they will show as stopped once their running work has shut down.", sessionID)
	msg += cancelBackgroundShellWarnings(killFailed, walkIncomplete)
	return NewToolResult(msg)
}
