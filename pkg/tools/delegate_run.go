// delegate_run.go: Run a delegation, sync or async, and return its result — including ending one early by timeout or cancel.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

// ErrRequestedSkillDenied and ErrRequestedSkillNotFound are the two distinct
// dispatch-time failure sentinels a SubTurnSpawner implementation (in
// practice, pkg/agent's spawnSubTurn) returns for a `delegate.run` call's
// RequestedSkill (ADR-072 D9, spec FR-053/FR-054). Declared here — in
// pkg/tools, the lower package in the tools<->agent duplication this file's
// own ContextSnapshot/SubTurnConfig doc comments already describe — rather
// than in pkg/agent, so executeSync/executeAsync below can distinguish them
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
// transition or task bookkeeping of its own, so both executeSync (which
// returns the result directly, unwrapped) and executeAsync (which folds it
// into the same state/lifecycle bookkeeping every other dispatch outcome
// goes through) can call it identically.
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
	async             bool
	timeout           time.Duration
	requestedSkill    string
	snap              *ContextSnapshot
	resolvedMaxDepth  *int
	delegateSessionID string
}

func (t *DelegateTool) executeRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	dt := &delegateToolExecuteRun{t: t, ctx: ctx, args: args}

	if r0, stop := dt.validateRequest(); stop {
		return r0
	}

	// Delegation policy gate (FR-6.2): trust set + mode + depth, mode selected
	// by the async flag ("background" vs "await") — applied identically
	// regardless of async value (FR-D3). ADR-037: this is now the ONLY gate —
	// the legacy trust-only allowlistCheck/delegateChecker fallbacks (consulted
	// only when these were nil, which never happened in production) are
	// retired.
	//
	// FAIL CLOSED, not open, when no checker is wired: an unwired deny-checker
	// is a configuration error, never a permission grant. This is unreachable
	// in today's production wiring — pkg/agent/loop.go's registerSharedTools
	// unconditionally calls SetDelegationDenyCheckerBackground/Await for every
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

	// ADR-053 S2 — mint the child's own durable session_id (distinct from
	// the shared transcript session id, D1) and persist its initial
	// `queued` lifecycle record BEFORE dispatch, so a crash between here and
	// the goroutine/spawn call below still leaves a queryable record (the
	// boot sweep — another wave — will reconcile it to failed(interrupted)).
	if r0, stop := dt.persistLifecycle(); stop {
		return r0
	}

	if dt.async {
		// isResume: false — executeRun always mints a BRAND-NEW
		// delegateSessionID (generation 0) just above; this is a genuine
		// create, never a resume. Native `follow_up`'s warm resume goes
		// through spawnCorrectiveFollowUp's own executeAsync call instead.
		return dt.t.executeAsync(dt.ctx, dt.task, dt.label, dt.agentID, dt.resolvedMaxDepth, dt.delegateSessionID, dt.timeout, dt.snap, dt.requestedSkill, false, cb)
	}
	return dt.t.executeSync(dt.ctx, dt.task, dt.label, dt.agentID, dt.resolvedMaxDepth, dt.delegateSessionID, dt.timeout, dt.snap, dt.requestedSkill)
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

	dt.async = true
	if rawAsync, present := dt.args["async"]; present && rawAsync != nil {
		b, ok := rawAsync.(bool)
		if !ok {
			return ErrorResult("async must be a boolean"), true
		}
		dt.async = b
	}

	// timeout_seconds was documented in the schema but never actually read
	// anywhere — every delegated sub-turn silently used the hardcoded
	// defaultSubTurnTimeout (5 minutes, pkg/agent/subturn.go) regardless of
	// what the caller requested. 0/absent means "no override — use the
	// spawner's own default", matching the schema's "0 = default (5 min)"
	// wording; a nonzero value is bounds-checked and threaded into
	// SubTurnConfig.Timeout below.
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
	return nil, false
}

// authorizeDelegation applies the delegation policy and resolves the authorized depth.
func (dt *delegateToolExecuteRun) authorizeDelegation() (*ToolResult, bool) {
	if dt.async {
		if dt.t.delegationDenyBackground != nil {
			if denial := dt.t.delegationDenyBackground(dt.ctx, dt.agentID); denial != nil {
				return DelegationDeniedResult("delegate", denial), true
			}
		} else {
			slog.Error("delegate: no background delegation-deny checker installed — denying by default",
				"agent_id", dt.agentID)
			return DelegationDeniedResult("delegate", &DelegationDenial{
				Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
				Policy:        DenyTrustSet,
				TargetAgentID: dt.agentID,
			}), true
		}
	} else {
		if dt.t.delegationDenyAwait != nil {
			if denial := dt.t.delegationDenyAwait(dt.ctx, dt.agentID); denial != nil {
				return DelegationDeniedResult("delegate", denial), true
			}
		} else {
			slog.Error("delegate: no await delegation-deny checker installed — denying by default",
				"agent_id", dt.agentID)
			return DelegationDeniedResult("delegate", &DelegationDenial{
				Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
				Policy:        DenyTrustSet,
				TargetAgentID: dt.agentID,
			}), true
		}
	}

	// #477: resolve the effective depth cap the gate above just authorized
	// this call against, so the spawner's own depth check does not
	// independently re-derive a different (possibly stricter) default.

	if dt.t.delegationDepthResolver != nil {
		dt.resolvedMaxDepth = dt.t.delegationDepthResolver(dt.ctx, dt.agentID)
	}
	return nil, false
}

// persistLifecycle creates and persists the delegated session lifecycle record.
func (dt *delegateToolExecuteRun) persistLifecycle() (*ToolResult, bool) {
	dt.delegateSessionID = uuid.NewString()
	parentDurableKey := strings.TrimSpace(ToolTranscriptSessionID(dt.ctx))
	is3P := false
	if dt.agentID != "" && dt.t.getAgentRegistry != nil {
		if reg := dt.t.getAgentRegistry(); reg != nil {
			is3P = reg.IsExternalCLI(dt.agentID)
		}
	}
	ownerScopeKind := session.OwnerScopeHuman
	ownerScopeID := ""
	if parentDelegateID := strings.TrimSpace(ToolDelegateSessionID(dt.ctx)); parentDelegateID != "" {
		ownerScopeKind = session.OwnerScopeParentSession
		ownerScopeID = parentDelegateID
	}
	if dt.t.lifecycle != nil {
		// FR-015 — fail closed on an unresolvable parent. ToolAgentID returns
		// "" for BOTH a missing context key AND a wrong-typed value (it is a
		// comma-ok type assertion with the error discarded), so an empty
		// value here means the DELEGATING agent's identity could not be
		// resolved at all. A record minted without it is permanently
		// unattributable: ParentAgentID is the only parent linkage, and no
		// other field can stand in for it (ParentDurableKey post-ADR-057 (D1)
		// names only the DIRECT parent — one hop, never re-inherited down the
		// chain, see pkg/session/lifecycle.go's ParentDurableKey doc comment —
		// so it cannot stand in for ParentAgentID either; OwnerScopeID is ""
		// for a top-level delegation, and AgentID is the child's). Such a
		// session could never be returned to its parent by list_jobs. Refuse
		// the mint — and therefore the whole delegation — rather than persist
		// an orphan. Note the mint is deliberately still skipped entirely
		// when no lifecycle store is configured: with no store there is no
		// record to orphan (see the else branch below — FR-021/BDD-20 refuse
		// the delegation outright in that case instead).
		//
		// R2-MAJ-015 — tools.delegate.require_parent_agent_id is the operator
		// kill switch for exactly that refusal. It exists because the guard's
		// blast radius is the whole install: a wiring regression anywhere
		// upstream of ToolAgentID turns EVERY delegate call into this error,
		// and without a lever the only remedy is a code change. Resolving the
		// key to false downgrades the refusal to a log-at-Error and mints
		// with an empty ParentAgentID — knowingly degraded attribution, an
		// explicit operator choice, never the default (unset resolves to
		// true) and never silent: the Error line below fires on EVERY such
		// mint, not once, so a forgotten kill switch keeps announcing the
		// orphan records it is creating.
		parentAgentID := strings.TrimSpace(ToolAgentID(dt.ctx))
		if parentAgentID == "" {
			if dt.t.parentAgentIDRequired() {
				slog.Error("delegate: refusing to mint an unattributable lifecycle record — no parent agent id in context",
					"delegate_session_id", dt.delegateSessionID,
					"target_agent_id", dt.agentID,
					"parent_durable_key", parentDurableKey)
				return ErrorResult("delegate: cannot resolve the delegating agent's identity — " +
					"refusing to start a delegated session that could never be traced back to its parent"), true
			}
			slog.Error("delegate: minting an unattributable lifecycle record with an empty parent agent id — "+
				"the FR-015 guard is disabled by tools.delegate.require_parent_agent_id=false; "+
				"this session cannot be traced back to its parent and will never be returned to it by list_jobs",
				"delegate_session_id", dt.delegateSessionID,
				"target_agent_id", dt.agentID,
				"parent_durable_key", parentDurableKey)
		}
		rec := &session.LifecycleRecord{
			SessionID:        dt.delegateSessionID,
			Generation:       0,
			State:            session.LifecycleQueued,
			OwnerScopeKind:   ownerScopeKind,
			OwnerScopeID:     ownerScopeID,
			ParentAgentID:    parentAgentID,
			ParentDurableKey: parentDurableKey,
			OriginChannel:    ToolChannel(dt.ctx),
			OriginChatID:     ToolChatID(dt.ctx),
			WorkspaceID:      ToolWorkspaceID(dt.ctx),
			AgentID:          dt.agentID,
			Is3P:             is3P,
		}
		if err := dt.t.lifecycle.Persist(rec); err != nil {
			return ErrorResult(fmt.Sprintf("delegate: failed to persist durable session record: %v", err)).WithError(err), true
		}
	} else {
		// FR-021/BDD-20 (W7a) — fail CLOSED, not silently degraded, when no
		// durable lifecycle store is wired at all. Before this fix, the whole
		// `if t.lifecycle != nil { ... }` block above (mint + persist) was
		// simply skipped and execution fell straight through to
		// executeAsync/executeSync below — spawning a real child sub-turn
		// with NO durable record, no ParentDurableKey edge for the ancestor
		// walk (W12) or the boot sweep (W6/FR-078) to ever find, and a
		// success-shaped AsyncResult/inline result returned to the caller as
		// if nothing were wrong. That is precisely the silent-degradation
		// posture Hard Constraint #6 and this file's own FR-015 guard above
		// forbid, and BDD-20 pins the correct behavior: an operator-visible
		// refusal, no child session created, no success payload returned.
		// Mirrors the FR-015 refusal's shape (slog.Error + ErrorResult)
		// immediately above rather than introducing a second error style.
		slog.Error("delegate: refusing delegation — no durable lifecycle store configured",
			"delegate_session_id", dt.delegateSessionID,
			"target_agent_id", dt.agentID,
			"parent_durable_key", parentDurableKey)
		return ErrorResult("delegate: cannot start a delegated session — no durable lifecycle store is " +
			"configured (operator misconfiguration); refusing rather than spawning an untracked, " +
			"unrecoverable session"), true
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
// durable ParentDurableKey edge (pkg/session/lifecycle.go) starting at
// rootSessionID and returns every reachable descendant's own session id
// (rootSessionID itself is never included).
//
// This mirrors agent.CollectDescendantSessionIDs (pkg/agent/cancel.go)
// byte-for-byte in walk semantics and error contract, duplicated here rather
// than called directly because pkg/tools cannot import pkg/agent (pkg/agent
// already imports pkg/tools — see AgentLoopSpawner/SubTurnConfig — so the
// dependency can only run that direction). This is the same class of
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
		children, err := t.lifecycle.List(session.LifecycleFilter{ParentDurableKey: id})
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

// ErrDelegationTimedOut is wrapped into a SpawnSubTurn error when the
// delegated sub-turn reached its time limit (SubTurnConfig.Timeout) and was
// force-cancelled (pkg/agent/subturn.go's armSubTurnForceCancel). It lives here,
// not in pkg/agent, because this package cannot import pkg/agent — it is the
// one discriminator executeSync/executeAsync use to tell the delegator the
// truth about a timeout. A cooperative child is stopped; ErrDelegationDetached
// additionally identifies the exceptional case where the child turn's
// in-flight operation ignored cancellation and may still be unwinding.
var ErrDelegationTimedOut = errors.New("delegation timed out")

// ErrDelegationDetached additionally marks a timed-out native child whose
// in-flight operation ignored cancellation. The parent has stopped waiting
// and no new model/tool call can start, but that operation may still be
// unwinding. User-facing timeout copy must preserve that distinction.
var ErrDelegationDetached = fmt.Errorf("%w: detached after ignoring cancellation", ErrDelegationTimedOut)

// delegateTaskStatusTimedOut is the in-memory DelegateTaskState.Status for a
// delegation that reached its time limit and was force-cancelled. It mirrors
// the durable session.LifecycleTimedOut record written alongside it, so
// action:"status" and list_jobs agree on what happened.
const delegateTaskStatusTimedOut = "timed_out"

// delegateToolExecuteAsync carries the shared state of executeAsync across its stages.
type delegateToolExecuteAsync struct {
	t                 *DelegateTool
	ctx               context.Context
	task              string
	label             string
	agentID           string
	resolvedMaxDepth  *int
	delegateSessionID string
	timeout           time.Duration
	snap              *ContextSnapshot
	requestedSkill    string
	isResume          bool
	cb                AsyncCallback
	taskID            string
}

// executeAsync runs the background (async=true) delegation path. It records
// the task's state in t.tasks BEFORE launching the sub-turn goroutine and
// updates that SAME record on completion — the fix for FR-D2: action:"status"
// reads from this exact map, so a real, live status is always available.
func (t *DelegateTool) executeAsync(
	ctx context.Context,
	task, label, agentID string,
	resolvedMaxDepth *int,
	delegateSessionID string,
	timeout time.Duration,
	snap *ContextSnapshot,
	requestedSkill string,
	isResume bool,
	cb AsyncCallback,
) *ToolResult {
	dt := &delegateToolExecuteAsync{t: t, ctx: ctx, task: task, label: label, agentID: agentID, resolvedMaxDepth: resolvedMaxDepth, delegateSessionID: delegateSessionID, timeout: timeout, snap: snap, requestedSkill: requestedSkill, isResume: isResume, cb: cb}

	if dt.t.spawner == nil {
		return ErrorResult("delegate: no sub-turn spawner configured")
	}

	channel := ToolChannel(dt.ctx)
	chatID := ToolChatID(dt.ctx)
	// W2: capture, at task-creation time, the exact correlation anchors a
	// spawned child sub-turn's transcript entries will carry back —
	// SessionID mirrors TranscriptSessionID: parentTS.transcriptSessionID
	// (pkg/agent/subturn.go), and SpawnCallID mirrors the delegate tool
	// call's own ID (the value spawnToolCallIDFromContext captures on the
	// agent-package side to set childTS.parentSpawnCallID). Reading both
	// from THIS SAME ctx guarantees they match what the child will actually
	// use, without any agent-package coupling.
	sessionID := ToolTranscriptSessionID(dt.ctx)
	spawnCallID := ToolCallID(dt.ctx)
	is3P := false
	if dt.agentID != "" && dt.t.getAgentRegistry != nil {
		if reg := dt.t.getAgentRegistry(); reg != nil {
			is3P = reg.IsExternalCLI(dt.agentID)
		}
	}

	dt.t.mu.Lock()
	// FR-045: eviction runs as part of the tool's own bookkeeping — every
	// new task registration — never a separate goroutine/ticker.
	dt.t.evictStaleTasksLocked()
	dt.taskID = fmt.Sprintf("delegate-%d", dt.t.nextID)
	dt.t.nextID++
	dt.t.tasks[dt.taskID] = &DelegateTaskState{
		ID:                dt.taskID,
		Task:              dt.task,
		Label:             dt.label,
		AgentID:           dt.agentID,
		OriginChannel:     channel,
		OriginChatID:      chatID,
		Status:            "running",
		Created:           time.Now().UnixMilli(),
		SessionID:         sessionID,
		SpawnCallID:       spawnCallID,
		Is3P:              is3P,
		DelegateSessionID: dt.delegateSessionID,
		LastStatusRead:    dt.t.now().UnixMilli(),
	}
	if dt.delegateSessionID != "" {
		dt.t.sessionIndex[dt.delegateSessionID] = dt.taskID
	}
	dt.t.mu.Unlock()

	dt.t.transitionLifecycle(dt.delegateSessionID, session.LifecycleRunning, "")

	// The task is the first USER message; the delegate's soul (worker /
	// configured agent) is resolved inside spawnSubTurn and used as the
	// system role. delegate does not pre-inject any persona — a configured
	// delegate exposes its own soul and a soul-less worker runs with an empty
	// system role (worker souls are OPTIONAL by design). The label, when set,
	// is preserved as the task label for the WS subTurn_start frame.
	//
	// Critical: true is REQUIRED here, not optional. Background delegation's
	// entire premise is "the parent moves on; tell me later" — the parent
	// turn routinely finishes (its own follow-up LLM call after receiving
	// this async ack, then Finish(false)) in well under the time it takes
	// the delegate to run even one tool call. Without Critical:true, the
	// child sub-turn's own loop (pkg/agent/loop.go's "Parent turn ended"
	// check, evaluated early in each iteration, before the next LLM call)
	// treats !ts.critical && ts.IsParentEnded() as a signal to exit
	// gracefully — silently discarding the delegate's real answer for any
	// task needing more than a single LLM turn (i.e.
	// any task that calls a tool before its final answer). The delegate's
	// pre-tool-call narration survives (persisted per-iteration), but the
	// synthesized final answer is never produced at all: spawnSubTurn's
	// result comes back with ForLLM/ForUser == "", and asyncCallback's
	// `content == "" { return }` guard (pkg/agent/loop.go) then silently
	// drops it — no error, no notification, nothing delivered to the user,
	// live or on reload. Critical:true lets the child keep running past the
	// parent's own finish (it still delivers as an "orphan" on the
	// now-moot pendingResults channel — see deliverSubTurnResult — but its
	// REAL delivery path, this same cb -> AsyncNotifier.Notify chain, is
	// unaffected by parent lifecycle and fires correctly once the child
	// actually finishes). See SubTurnConfig.Critical's doc comment.
	//
	// Pending-spawn marker (delegate-spawn cancel race fix): recorded HERE,
	// synchronously on THIS (the delegating parent's own tool-execution)
	// goroutine, as the LAST thing that happens before the goroutine below
	// is dispatched — not inside the goroutine itself, and not any earlier
	// than this point. Marking any earlier (e.g. before the t.tasks
	// bookkeeping above) would risk recording a marker for a spawn that
	// this function then aborts before ever reaching the goroutine — there
	// is no such abort path between here and the `go func` below, so this
	// is also the LATEST point that still guarantees "marked implies a
	// spawn attempt is genuinely in flight," which is exactly the
	// invariant AgentLoopSpawner.MarkPendingDelegateSpawn's own doc comment
	// (pkg/agent/subturn.go) requires of every caller. sessionID/channel/
	// chatID are the delegating PARENT turn's own identity (captured above
	// from this same ctx) — the SAME identity a Stop click's
	// CancelScope carries (CancelScope.SessionID for the web SPA/CLI/Tier A
	// path, or (Channel, ChatID) for Tier B). channel/chatID are inherited
	// verbatim by the spawned child at any delegation depth; sessionID's
	// match instead relies on ROUTING identity being what's inherited
	// verbatim (turn.go's own doc comment: "routingSessionID is inherited
	// verbatim through the whole subtree"), NOT transcriptSessionID — post-
	// ADR-057 D1 each child gets its OWN distinct transcriptSessionID
	// rather than copying the parent's (this comment used to claim
	// "processOptions construction copies parentTS.transcriptSessionID …
	// onto the child," which was true pre-D1 and is false now). So
	// spawnSubTurn's own cleanup clears the exact keys marked here. A nil
	// t.spawnMarker (SetSpawner was never called with a marker-capable
	// spawner — e.g. this package's own unit tests) makes this call a
	// silent no-op, unchanged from before this fix.
	if dt.t.spawnMarker != nil {
		dt.t.spawnMarker.MarkPendingDelegateSpawn(sessionID, channel, chatID)
	}
	dt.t.asyncWG.Add(1)
	go func() {
		dt.runSubturn()
	}()

	// NOTE: "(task_id: %s)" must stay its own parenthesized clause, ending in
	// the FIRST ")" after the id — pkg/tools/delegate_test.go's extractTaskID
	// helper scans for "task_id: " and stops at the next ")"/"\n", so a
	// session_id appended INSIDE the same parens would corrupt every test
	// using that helper (regression: existing one-shot delegate.run compat).
	msg := fmt.Sprintf("Delegated task for: %s (task_id: %s)", dt.task, dt.taskID)
	if dt.label != "" {
		msg = fmt.Sprintf("Delegated task '%s' for: %s (task_id: %s)", dt.label, dt.task, dt.taskID)
	}
	msg += fmt.Sprintf(" (session_id: %s)", dt.delegateSessionID)
	msg += fmt.Sprintf(
		" — running in background; check progress with delegate(action=\"status\", session_id=%q), "+
			"or inbox/steer/respond/cancel/follow_up/peek using the same session_id.", dt.delegateSessionID,
	)
	return AsyncResult(msg)
}

// runSubturn runs the delegated sub-turn and records and reports its outcome.
func (dt *delegateToolExecuteAsync) runSubturn() {
	defer dt.t.asyncWG.Done()
	result, err := dt.t.spawner.SpawnSubTurn(dt.ctx, SubTurnConfig{
		Model:             dt.t.defaultModel,
		Tools:             nil, // Will inherit from parent via context
		SystemPrompt:      dt.task,
		TargetAgentID:     dt.agentID,
		MaxTokens:         dt.t.maxTokens,
		Temperature:       dt.t.temperature,
		Async:             true,
		Critical:          true,
		Timeout:           dt.timeout,
		TaskLabel:         dt.label,
		ResolvedMaxDepth:  dt.resolvedMaxDepth,
		ContextSnapshot:   dt.snap,
		DelegateSessionID: dt.delegateSessionID,
		IsResume:          dt.isResume,
		RequestedSkill:    dt.requestedSkill,
	})

	// ADR-072 D9 / FR-053/054: a requested_skill dispatch failure (the
	// receiver denied it, or the slug does not resolve at all) is a
	// distinct, structured outcome — never the generic "Delegate failed"
	// wrap below, which would flatten DelegationDeniedCode/SkillNotFoundCode
	// down to opaque prose. Built once, up front, so both the bookkeeping
	// switch (state.Result) and the result-rebuild switch below use the
	// SAME structured payload.
	requestedSkillFailure := errors.Is(err, ErrRequestedSkillDenied) || errors.Is(err, ErrRequestedSkillNotFound)
	var requestedSkillFailureResult *ToolResult
	if requestedSkillFailure {
		requestedSkillFailureResult = requestedSkillDispatchFailureResult(dt.agentID, dt.requestedSkill, err)
	}

	var lifecycleState session.LifecycleState
	var lifecycleFailedReason string
	// parked: the child called message_parent(wait=true) and is waiting on
	// the parent's respond(), NOT finished. Its turn stopped deliberately,
	// so err is nil and the result is neither Interrupted nor IsError —
	// exactly the shape that otherwise falls into `default` below and gets
	// stamped LifecycleCompleted, overwriting the needs_input state
	// parkNeedsInput just wrote. That overwrite is what made respond()
	// fail closed with "session is not parked" in the ADR-057 UAT even
	// once the turn loop itself was fixed to stop.
	parked := false

	// UAT A-17: a delegation that reached its time limit was
	// force-cancelled (pkg/agent/subturn.go). Built BEFORE t.mu is taken:
	// timedOutDelegationResult kills the child's background shells, which
	// walks the lifecycle store and must never run under this tool's
	// mutex. Its case below is checked ahead of `ctx.Err() != nil` on
	// purpose — ctx is the delegating PARENT's tool context, which is
	// routinely already cancelled by the time a background child times
	// out (the parent turn moved on), so that case would otherwise report
	// a timeout as "Task canceled during execution".
	timedOut := !requestedSkillFailure && errors.Is(err, ErrDelegationTimedOut)
	var timedOutResult *ToolResult
	if timedOut {
		timedOutResult = dt.t.timedOutDelegationResult(dt.delegateSessionID, dt.label, err)
	}

	dt.t.mu.Lock()
	if state, ok := dt.t.tasks[dt.taskID]; ok {
		switch {
		case requestedSkillFailure:
			state.Status = "failed"
			state.Result = requestedSkillFailureResult.ForLLM
			lifecycleState, lifecycleFailedReason = session.LifecycleFailed, "error"
		case timedOut:
			state.Status = delegateTaskStatusTimedOut
			state.Result = timedOutResult.ForLLM
			lifecycleState, lifecycleFailedReason = session.LifecycleTimedOut, ""
		case err != nil && dt.ctx.Err() != nil:
			state.Status = "canceled"
			state.Result = "Task canceled during execution"
			lifecycleState, lifecycleFailedReason = session.LifecycleCancelled, "stopped_by_user"
		case err != nil:
			state.Status = "failed"
			state.Result = fmt.Sprintf("Error: %v", err)
			lifecycleState, lifecycleFailedReason = session.LifecycleFailed, "error"
		case result != nil && result.ParksTurn:
			parked = true
			state.Status = "needs_input"
			state.Result = result.ForLLM
		default:
			state.Status = "completed"
			if result != nil {
				state.Result = result.ForLLM
			}
			lifecycleState = session.LifecycleCompleted
		}
	}
	dt.t.mu.Unlock()

	// Skip the transition entirely when parked — parkNeedsInput already
	// owns this record's state, and any write here would clobber it.
	if !parked {
		dt.t.transitionLifecycle(dt.delegateSessionID, lifecycleState, lifecycleFailedReason)
	}

	switch {
	case requestedSkillFailure:
		result = requestedSkillFailureResult
	case timedOut:
		// Not "spawn failed": the child ran and was force-cancelled at its
		// time limit. Warn (an expected, bounded outcome), not Error, and
		// with its own grep-able message.
		slog.Warn("delegate: async subagent reached its time limit and was force-cancelled",
			"session_id", dt.delegateSessionID,
			"task_id", dt.taskID,
			"agent_id", dt.agentID,
			"is_resume", dt.isResume,
			"error", err)
		result = timedOutResult
	case err != nil:
		// Kill the silent swallow: a spawn that dies before starting
		// (e.g. a `follow_up` resume whose target session vanished, or
		// any other SpawnSubTurn failure) must be operator-visible on
		// its OWN, unconditionally — never dependent on whatever `cb`
		// happens to do with the result downstream (a live channel that
		// may already be gone, an AsyncNotifier publish that lands
		// somewhere other than where an operator is watching, etc.).
		// This is the ONE line that fires every single time this
		// goroutine's spawn attempt fails, regardless of whether cb is
		// nil, so `grep -i "async subturn spawn failed" gateway.log`
		// always finds it.
		slog.Error("delegate: async subturn spawn failed",
			"session_id", dt.delegateSessionID,
			"task_id", dt.taskID,
			"agent_id", dt.agentID,
			"is_resume", dt.isResume,
			"error", err)
		result = ErrorResult(fmt.Sprintf("Delegate failed: %v", err)).WithError(err)
	case result != nil:
		// Finding B (A-I4 round 4, live-verified): mirror executeSync's
		// own wrapping below — the raw spawner result's ForUser field
		// (spawnSubTurn / pkg/agent/subturn.go sets it to
		// turnRes.finalContent, the CHILD's own unwrapped, first-person
		// final text) must never reach pkg/agent/loop.go's asyncCallback
		// unmodified. asyncCallback unconditionally does
		// `if !result.Silent && result.ForUser != "" { PublishOutbound(...
		// Content: result.ForUser ...) }` — a DIRECT, immediate publish
		// with no relation to the wsStreamer/shadow-stream machinery at
		// all (confirmed via a live background delegation: the leaked
		// bubble appeared even with no cancellation involved, the moment
		// the child's own final answer happened to require no wrapping —
		// e.g. a policy-denied task explaining itself in its own voice).
		// That silently turns the delegate's own raw narration into a
		// second, unattributed top-level chat bubble the instant the
		// parent's own turn has already ended — the common case for
		// background delegation (Critical:true's own doc comment above:
		// "the parent turn routinely finishes ... in well under the time
		// it takes the delegate to run even one tool call"). This is
		// exactly the content class the design intends to keep hidden,
		// matching the already-correct sync/await case (executeSync
		// below never independently publishes anything — its result only
		// ever becomes a normal tool_call_result) and
		// pkg/gateway/replay.go's ParentSpawnCallID skip. The LLM-facing
		// AsyncNotifier continuation turn (still fed the wrapped ForLLM
		// content below) already informs the user, in the DELEGATOR's
		// own voice, that the delegation finished and what it found —
		// clearing ForUser here removes the duplicate, unattributed raw
		// dump without losing any user-facing information.
		labelStr := dt.label
		if labelStr == "" {
			labelStr = "(unnamed)"
		}
		// A parked child is NOT finished — it is waiting on this
		// delegator's own respond(). Saying "completed" would tell the
		// delegator's next turn the opposite of what the lifecycle
		// record says (needs_input), which is how an orchestrator ends
		// up believing work is done and never answering the question.
		// ParksTurn must also survive this rebuild: dropping it here
		// would silently kill the signal for every downstream reader.
		headline := "Subagent task completed"
		if result.ParksTurn {
			headline = "Subagent task is PAUSED awaiting your answer (respond to it to continue)"
		}
		// ADR-072 D9 / FR-056: report the loaded skill in the delegation
		// result. Reaching this branch at all (requestedSkillFailure was
		// false above) means the receiver's own grant permitted it, so a
		// non-empty requestedSkill here was necessarily granted and
		// appended to the child's ForcedSkills by spawnSubTurn.
		if dt.requestedSkill != "" {
			headline += fmt.Sprintf(" (requested_skill %q was loaded into the child's first turn)", dt.requestedSkill)
		}
		result = &ToolResult{
			ForLLM:    fmt.Sprintf("%s:\nLabel: %s\nResult: %s", headline, labelStr, result.ForLLM),
			IsError:   result.IsError,
			ParksTurn: result.ParksTurn,
			Async:     true,
		}
	}

	// Call callback if provided
	if dt.cb != nil {
		dt.cb(dt.ctx, result)
	} else if err != nil {
		slog.Error("delegate: subturn failed with no callback", "error", err)
	}
}

// executeSync runs the await (async=false) delegation path: it blocks until
// the delegated turn completes and returns the result inline.
func (t *DelegateTool) executeSync(
	ctx context.Context,
	task, label, agentID string,
	resolvedMaxDepth *int,
	delegateSessionID string,
	timeout time.Duration,
	snap *ContextSnapshot,
	requestedSkill string,
) *ToolResult {
	if t.spawner == nil {
		return ErrorResult("delegate: no sub-turn spawner configured").WithError(fmt.Errorf("spawner not set"))
	}

	// ADR-057 FR-044/BDD-49: executeSync now registers a DelegateTaskState
	// too — previously only executeAsync did, so a completed/failed
	// synchronous (await) delegation left action:"status" with nothing to
	// find for it at all (not "empty", genuinely absent), and
	// recentActivityLines could never build a snapshot for a task that
	// action:"status" could not even resolve. Mirrors executeAsync's own
	// registration exactly (same fields, same eviction call).
	channel := ToolChannel(ctx)
	chatID := ToolChatID(ctx)
	sessionID := ToolTranscriptSessionID(ctx)
	spawnCallID := ToolCallID(ctx)
	is3P := false
	if agentID != "" && t.getAgentRegistry != nil {
		if reg := t.getAgentRegistry(); reg != nil {
			is3P = reg.IsExternalCLI(agentID)
		}
	}

	t.mu.Lock()
	t.evictStaleTasksLocked()
	taskID := fmt.Sprintf("delegate-%d", t.nextID)
	t.nextID++
	t.tasks[taskID] = &DelegateTaskState{
		ID:                taskID,
		Task:              task,
		Label:             label,
		AgentID:           agentID,
		OriginChannel:     channel,
		OriginChatID:      chatID,
		Status:            "running",
		Created:           time.Now().UnixMilli(),
		SessionID:         sessionID,
		SpawnCallID:       spawnCallID,
		Is3P:              is3P,
		DelegateSessionID: delegateSessionID,
		LastStatusRead:    t.now().UnixMilli(),
	}
	if delegateSessionID != "" {
		t.sessionIndex[delegateSessionID] = taskID
	}
	t.mu.Unlock()

	t.transitionLifecycle(delegateSessionID, session.LifecycleRunning, "")

	result, err := t.spawner.SpawnSubTurn(ctx, SubTurnConfig{
		Model:             t.defaultModel,
		Tools:             nil, // Will inherit from parent via context
		SystemPrompt:      task,
		TargetAgentID:     agentID, // "" → parent's own soul; non-empty → named agent's soul
		TaskLabel:         label,
		MaxTokens:         t.maxTokens,
		Temperature:       t.temperature,
		Async:             false,
		Timeout:           timeout,
		ResolvedMaxDepth:  resolvedMaxDepth,
		ContextSnapshot:   snap,
		DelegateSessionID: delegateSessionID,
		RequestedSkill:    requestedSkill,
	})
	// ADR-072 D9 / FR-053/054: a requested_skill dispatch failure gets its
	// OWN structured result — DelegationDeniedCode for a denial,
	// SkillNotFoundCode for an unresolvable slug — returned UNWRAPPED,
	// exactly like the pre-flight trust/mode/depth DelegationDeniedResult
	// this file's own executeRun already returns directly on denial. Checked
	// before the generic dispatch-failure shortcut below so neither
	// discriminator is ever flattened into "Delegate execution failed: %v".
	if errors.Is(err, ErrRequestedSkillDenied) || errors.Is(err, ErrRequestedSkillNotFound) {
		t.transitionLifecycle(delegateSessionID, session.LifecycleFailed, "error")
		failureResult := requestedSkillDispatchFailureResult(agentID, requestedSkill, err)
		t.finalizeSyncTask(taskID, "failed", failureResult.ForLLM)
		return failureResult
	}
	// Finding F (A-I4 round 5): only take the generic "Delegate execution
	// failed" shortcut for a genuine dispatch failure — result == nil (e.g.
	// a panic spawnSubTurn's own recover() deliberately nils result for) or
	// a real, non-interrupted error. A parent-cancellation interruption
	// (result.Interrupted, set by spawnSubTurn's cleanup defer using the
	// SAME classification the live subagent_end frame already reports —
	// see ToolResult.Interrupted's doc comment) still returns a non-nil err
	// here (the child's context WAS canceled), but must fall through to the
	// normal formatting below so result.Interrupted survives onto the
	// result this function returns — which pkg/agent/loop.go's tool-call-
	// transcript persistence reads to decide whether a session reload shows
	// "interrupted" (matching live) or "failed" (the bug this closes).
	// UAT A-17: a delegation that reached its time limit was force-cancelled
	// (pkg/agent/subturn.go) — report exactly that, never the generic
	// "Delegate execution failed" wording below, which told an orchestrator
	// nothing about whether the child was still running. Checked before that
	// shortcut so the timeout discriminator is never flattened into it.
	if errors.Is(err, ErrDelegationTimedOut) {
		timedOut := t.timedOutDelegationResult(delegateSessionID, label, err)
		t.transitionLifecycle(delegateSessionID, session.LifecycleTimedOut, "")
		t.finalizeSyncTask(taskID, delegateTaskStatusTimedOut, timedOut.ForLLM)
		return timedOut
	}
	if result == nil || (err != nil && !result.Interrupted) {
		t.transitionLifecycle(delegateSessionID, session.LifecycleFailed, "error")
		t.finalizeSyncTask(taskID, "failed", fmt.Sprintf("Error: %v", err))
		return ErrorResult(fmt.Sprintf("Delegate execution failed: %v", err)).WithError(err)
	}

	switch {
	case result.Interrupted:
		t.transitionLifecycle(delegateSessionID, session.LifecycleCancelled, "stopped_by_user")
		t.finalizeSyncTask(taskID, "canceled", "Task canceled during execution")
	case result.IsError:
		t.transitionLifecycle(delegateSessionID, session.LifecycleFailed, "error")
		t.finalizeSyncTask(taskID, "failed", result.ForLLM)
	case result.ParksTurn:
		// Parked on message_parent(wait=true): waiting for the parent's
		// respond(), not finished. Deliberately transitions nothing —
		// parkNeedsInput already wrote needs_input, and writing
		// LifecycleCompleted here would clobber it and make respond() fail
		// closed with "session is not parked" (the ADR-057 UAT symptom).
		// The in-memory task status IS updated (finalizeSyncTask is a plain
		// setter despite the name), so `delegate status` reports needs_input
		// rather than a stale "running" — matching executeAsync's parked case.
		t.finalizeSyncTask(taskID, "needs_input", result.ForLLM)
	default:
		t.transitionLifecycle(delegateSessionID, session.LifecycleCompleted, "")
		t.finalizeSyncTask(taskID, "completed", result.ForLLM)
	}

	// Format result for display
	userContent := result.ForLLM
	if result.ForUser != "" {
		userContent = result.ForUser
	}
	maxUserLen := 500
	if len(userContent) > maxUserLen {
		userContent = userContent[:maxUserLen] + "..."
	}

	labelStr := label
	if labelStr == "" {
		labelStr = "(unnamed)"
	}
	// Same truthfulness rule as executeAsync's rebuild: a parked child is
	// waiting on this delegator's respond(), not finished. Telling the
	// delegator's next turn "completed" contradicts the needs_input record
	// and is how an unanswered question turns into a permanently stuck child.
	llmHeadline := "Subagent task completed"
	if result.ParksTurn {
		llmHeadline = "Subagent task is PAUSED awaiting your answer (respond to it to continue)"
	}
	// ADR-072 D9 / FR-056: report the loaded skill in the delegation result.
	// Reaching here at all means the requestedSkill dispatch-failure check
	// above did not fire, so a non-empty requestedSkill was necessarily
	// granted and appended to the child's ForcedSkills by spawnSubTurn.
	if requestedSkill != "" {
		llmHeadline += fmt.Sprintf(" (requested_skill %q was loaded into the child's first turn)", requestedSkill)
	}
	llmContent := fmt.Sprintf("%s:\nLabel: %s\nResult: %s",
		llmHeadline, labelStr, result.ForLLM)

	return &ToolResult{
		ForLLM:      llmContent,
		ForUser:     userContent,
		Silent:      false,
		IsError:     result.IsError,
		Interrupted: result.Interrupted,
		// Carry the park signal across this rebuild. Dropping it would leave
		// the delegator's OWN turn loop unaware that its child parked — the
		// same class of silently-lost signal this defect started as.
		ParksTurn: result.ParksTurn,
		Async:     false,
	}
}

// finalizeSyncTask records executeSync's terminal outcome onto the
// DelegateTaskState registered at the top of executeSync (FR-044), mirroring
// the status/Result assignment executeAsync's own completion goroutine makes.
// A no-op if taskID was never registered (defensive; cannot happen given
// executeSync always registers before this is called).
func (t *DelegateTool) finalizeSyncTask(taskID, status, resultText string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if st, ok := t.tasks[taskID]; ok {
		st.Status = status
		st.Result = resultText
	}
}

// timedOutDelegationResult is the ONE place a delegation that reached its time
// limit is reported to the delegator — shared by executeSync and executeAsync
// so the two can never describe the same outcome differently.
//
// Documented intent (Description / the timeout_seconds schema): "A delegation
// is force-cancelled after timeout_seconds ... if it has not finished by then."
// pkg/agent/subturn.go's force-cancel has already hard-aborted the child's turn
// by the time this runs; this function completes the force-cancel for the
// child's OS-level work, exactly as executeCancel does for an explicit cancel:
// it kills the child's (and its descendants') background shells, so a backgrounded
// command cannot keep writing after the delegator was told the delegation ended.
//
// The ordinary force-cancel wording is deliberate (UAT A-17): an orchestrator that read the old
// "SubTurn failed: turn timed out: context deadline exceeded" concluded "a
// timed-out delegation is not necessarily dead", then fought a phantom writer.
// The message states plainly when the child is stopped and when partial work
// may remain. ErrDelegationDetached takes a separate truthful branch: no new
// work can start, but the cancellation-ignoring in-flight operation may still be
// unwinding.
func (t *DelegateTool) timedOutDelegationResult(delegateSessionID, label string, err error) *ToolResult {
	_, killFailed, walkIncomplete := t.killChildBackgroundShells(delegateSessionID)

	labelStr := label
	if labelStr == "" {
		labelStr = "(unnamed)"
	}
	shells := "Any background shells it started were killed."
	if t.sessionManager == nil {
		shells = "No background-shell manager is configured, so none could be checked."
	}
	var msg string
	if errors.Is(err, ErrDelegationDetached) {
		msg = fmt.Sprintf(
			"Subagent task TIMED OUT, ignored cancellation, and was detached:\nLabel: %s\nSession: %s\nDetail: %v\n"+
				"The parent stopped waiting and no new model or tool call will be dispatched, but the "+
				"already-running operation may still be unwinding. %s Work completed before the time limit "+
				"may still be on disk, possibly incomplete — inspect the current state before re-delegating.",
			labelStr, delegateSessionID, err, shells,
		)
	} else {
		msg = fmt.Sprintf(
			"Subagent task TIMED OUT and was force-cancelled:\nLabel: %s\nSession: %s\nDetail: %v\n"+
				"The subagent is STOPPED: it will make no further tool calls or file changes. %s "+
				"There is nothing left to cancel. Work it finished before the time limit (for example "+
				"files it had already written) may still be on disk, possibly incomplete — inspect the "+
				"current state before re-delegating, and raise timeout_seconds if the task needs longer.",
			labelStr, delegateSessionID, err, shells,
		)
	}
	msg += cancelBackgroundShellWarnings(killFailed, walkIncomplete)
	return ErrorResult(msg).WithError(err)
}

// cancelBackgroundShellWarnings renders the "could not be killed" /
// "descendant walk incomplete" warning sentences shared by every
// executeCancel outcome message — INCLUDING the TOCTOU "nothing to cancel"
// branch (MEDIUM-3, 14-reviewer sign-off): a background-shell kill failure or
// an incomplete descendant walk is real, caller-relevant information
// regardless of whether the turn-level cancel itself found anything left to
// cancel. Before this fix, killChildBackgroundShells' own warnings were
// computed unconditionally but appended ONLY to the two success-message
// branches below — the "terminated between the terminal check and the
// cancel hook" branch discarded them outright, so a caller could be told
// "nothing to cancel" while a background shell it just tried to kill was, in
// fact, left running with no warning at all.
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
