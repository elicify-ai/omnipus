// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_adjudication.go implements ADR-052's Judge/Verifier architecture:
// prose criteria are adjudicated by a REAL agent turn, in its OWN fresh
// session, under the seeded Judge System Agent's identity — replacing the
// former single-shot, no-tools raw Provider.Chat shortcut. This supersedes
// the old judgeProseCriteria (removed from judge.go); JudgeCriteria's public
// signature and all three of its callers (task_executor.go, plan_engine.go,
// goal_loop.go) are UNCHANGED — the conversion is entirely internal.
//
// A verifier differs from a normal agent ONLY by (FR-012): (a) memory OFF
// (context.go's ContextBuilder.WithMemoryEnabled, wired from the seeded
// Judge's config.AgentConfig.MemoryEnabled=false at AgentInstance
// construction — pkg/agent/instance.go, out of this file's scope); (b) its
// soul IS its rubric (FR-038, config.AgentConfig.Rubric was deleted); (c)
// read-only tools (seeded tool policy, out of this file's scope — this
// file makes no tool-registration or tool-policy decision at all); (d)
// engine-invoked with the work-under-review passed as untrusted DATA, never
// instructions (buildJudgeUserContent's framing, judge.go).
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// verifierWindowTokensDefault is ADR-052 FR-032's confirmed default transcript
// window size (operator interview 2026-07-21: "N=20000"). It is the fallback
// when no config is reachable; the operator-tunable source of truth is
// config.PlanningConfig.VerifierWindowTokens (resolved via
// EffectiveVerifierWindowTokens, zero-backfilled at boot).
const verifierWindowTokensDefault = 20000

// effectiveVerifierWindowTokens resolves the transcript-window token budget
// (FR-032) from PlanningConfig, falling back to the compiled default when no
// config is reachable (test scaffolding without a full config).
func (al *AgentLoop) effectiveVerifierWindowTokens() int {
	if cfg := al.GetConfig(); cfg != nil {
		return cfg.Planning.EffectiveVerifierWindowTokens()
	}
	return verifierWindowTokensDefault
}

// --- Verifier-session registry (ADR-052 FR-037) -----------------------------

// VerifierSessionPublisher is the minimal seam runVerifierAdjudication uses to
// publish which verifier session is currently live for a given adjudication
// unit (a task id, plan id, or — until goal_loop.go is updated to pass a
// session identifier through JudgeCriteriaInput — an interim per-agent key
// for a chat `/goal`), so a plan-Stop fan-out (owned by the plan-engine
// wave, ADR-052 FR-009) can look the session up and cancel it via
// RequestCancelForSession, exactly like any in-flight member session.
//
// This wave defines the interface's SHAPE and a safe, self-contained
// default implementation only. The engine agent (plan_engine.go, out of
// this file's ownership) wires its own real registry — or a richer type
// satisfying this same interface, e.g. one the Stop fan-out can also
// enumerate/query — via SetVerifierSessionRegistry at PlanEngine
// construction time; the lead reconciles the exact type at merge.
type VerifierSessionPublisher interface {
	// Register records that unitID currently has a live verifier session
	// sessionID in flight. MUST be called BEFORE the verifier turn is
	// dispatched (mirrors ADR-052's M1 synchronous-assignment rule for
	// member sessions) so a Stop landing in the creation window cannot miss
	// it.
	Register(unitID, sessionID string)
	// Unregister removes unitID's entry once its verifier turn has
	// completed, errored, or been abandoned (ctx canceled mid-backoff).
	Unregister(unitID string)
}

// The canonical registry implementation lives in verifier_registry.go
// (VerifierSessionRegistry + NewVerifierSessionRegistry): PlanEngine
// constructs and owns the single process-wide instance, and NewPlanEngine
// points this package-wide seam at it, so the Stop fan-out enumerates
// (SessionsFor) the very same entries runVerifierAdjudication publishes
// here. Both sides key their entries via verifier_registry.go's
// verifierUnitForPlan/verifierUnitForTask/verifierUnitForGoal (through
// verifierUnitID below) — never a raw id — so the two sides always agree
// (ADR-052 FR-037, F1). Tests may still override the seam with a spy.

var (
	verifierSessionRegistryMu sync.RWMutex
	//nolint:gochecknoglobals // package-wide seam, see SetVerifierSessionRegistry's doc comment.
	verifierPublisherSeam VerifierSessionPublisher = NewVerifierSessionRegistry()
)

// SetVerifierSessionRegistry overrides the package-wide verifier-session
// registry (ADR-052 FR-037). Intended to be called once at engine
// construction by the plan-engine wave to wire its real Stop-fan-out-
// queryable registry; tests may also call this to inject a spy. Passing nil
// restores a fresh safe default (never leaves the package with no
// registry).
func SetVerifierSessionRegistry(r VerifierSessionPublisher) {
	verifierSessionRegistryMu.Lock()
	defer verifierSessionRegistryMu.Unlock()
	if r == nil {
		r = NewVerifierSessionRegistry()
	}
	verifierPublisherSeam = r
}

// currentVerifierSessionRegistry returns the active registry under its own
// lock so a concurrent SetVerifierSessionRegistry call never races a reader.
func currentVerifierSessionRegistry() VerifierSessionPublisher {
	verifierSessionRegistryMu.RLock()
	defer verifierSessionRegistryMu.RUnlock()
	return verifierPublisherSeam
}

// verifierUnitID resolves the registry key a Stop fan-out (and /goal clear)
// will look up a verifier session by, from whatever JudgeCriteriaInput
// already carries. MUST use the shared verifierUnitForPlan/ForTask/ForGoal
// helpers (verifier_registry.go) — the single source of truth for this
// scheme (ADR-052 FR-037, F1: a prior version of this file constructed the
// prefix ad hoc here while plan_engine.go's Stop fan-out enumerated raw ids,
// so Stop never found a live verifier session at all).
func verifierUnitID(in JudgeCriteriaInput) string {
	if in.TaskID != "" {
		return verifierUnitForTask(in.TaskID)
	}
	if in.PlanID != "" {
		return verifierUnitForPlan(in.PlanID)
	}
	if in.GoalSessionID != "" {
		// The collision-free /goal unit key (FR-037): the chat session that
		// carries the goal condition. /goal clear looks this up to cancel an
		// in-flight goal verifier.
		return verifierUnitForGoal(in.GoalSessionID)
	}
	// Defensive fallback for a goal-scope caller that somehow passed no
	// session id — a per-agent key so the verifier still gets SOME registry
	// entry rather than none.
	return "goal-agent:" + in.AssigneeAgentID
}

// --- Verifier soul (ADR-052 FR-038, soul/rubric unification) ---------------

// ensureVerifierSoul lazily materializes the seeded Judge's default judging
// standards (coreagent.JudgeDefaultRubric) into its SOUL.md on first real
// verifier dispatch. AgentConfig.Rubric was deleted (R3-1 CLOSED) — a soul
// is file-based (SOUL.md under the agent's workspace, the SAME mechanism
// every custom agent already uses, see definition.go's LoadAgentDefinition)
// — so this is where the compiled default becomes the Judge's actual,
// operator-editable prompt.
//
// Deliberately NOT done in coreagent.SeedConfig: that function is
// documented, and relied on by its own test suite (none of which sets
// OMNIPUS_HOME), as a PURE config-struct mutation with zero filesystem side
// effects. Writing a file there would start silently touching the real
// machine's home directory on every `go test ./pkg/coreagent/...` run.
// Materializing here instead — at the moment the Judge is actually about to
// run a verifier turn — mirrors how NewAgentInstance itself lazily
// MkdirAlls an agent's workspace at construction time rather than at
// config-seed time, and is naturally sandboxed by every pkg/agent test's
// own isolated Home/workspace (see judge_seed_test.go's replacement,
// verifier_adjudication_test.go, for the pattern).
//
// Never overwrites existing non-empty content — an operator's own soul edit
// (or a custom verifier's own SOUL.md) is preserved exactly like the
// deleted Rubric field's old "backfill only when empty" rule. Only ever
// writes for the seeded Judge itself; a custom verifier's SOUL.md is
// entirely the operator's own and is never backfilled.
func ensureVerifierSoul(agentInst *AgentInstance) {
	if agentInst == nil || agentInst.ID != string(coreagent.IDJudge) {
		return
	}
	if strings.TrimSpace(judgeRubricFromConfig(agentInst)) != "" {
		return // operator (or a prior seed) already put real content here
	}
	workspace := strings.TrimSpace(agentInst.Home)
	if workspace == "" {
		return
	}
	if mkErr := os.MkdirAll(workspace, 0o755); mkErr != nil {
		logger.WarnCF("agent", "verifier: could not create workspace for default soul seed",
			map[string]any{"agent_id": agentInst.ID, "workspace": workspace, "error": mkErr.Error()})
		return
	}
	soulPath := filepath.Join(workspace, "SOUL.md")
	if err := fileutil.WriteFileAtomic(soulPath, []byte(coreagent.JudgeDefaultRubric), 0o644); err != nil {
		logger.WarnCF("agent", "verifier: could not seed default SOUL.md",
			map[string]any{"agent_id": agentInst.ID, "error": err.Error()})
	}
}

// --- Window feed (ADR-052 FR-032) -------------------------------------------

// resolveVerifierWindowText builds the transcript-window evidence text for
// one adjudication, per scope:
//
//   - Task/goal-loop scope: that unit's own working-session tail, read via
//     the assignee agent's session store (the "PartitionStore" read path —
//     UnifiedStore.ReadTranscript, backed by transcript.jsonl), rendered and
//     trimmed to the last effectiveVerifierWindowTokens() tokens using the
//     existing per-message token estimator (estimateMessageTokens,
//     context_budget.go).
//   - Plan scope: "" — GS-04: no single "plan session" exists. FR-032's
//     plan-scope "structured composition" (goal/DoD + each member's final
//     claim + evidence) is ALREADY exactly what plan_engine.go's
//     buildPlanJudgeExtraContext/buildPlanClaimText assemble into
//     JudgeCriteriaInput.ExtraContext/ClaimText before this ever runs — a
//     second raw session-window read would be the "raw multi-session token
//     concat" FR-032 explicitly rejects, not an addition to it.
func (al *AgentLoop) resolveVerifierWindowText(in JudgeCriteriaInput) string {
	switch in.Scope {
	case task.VerdictScopeTask:
		return al.taskSessionWindowText(in.TaskID, in.AssigneeAgentID)
	case task.VerdictScopePlan:
		return ""
	case task.VerdictScopeGoal:
		// FR-032 for /goal: the window source is the chat session carrying
		// the goal condition (GoalSessionID, passed by goal_loop.go), read
		// from the goal agent's own session store.
		return al.goalSessionWindowText(in.GoalSessionID, in.AssigneeAgentID)
	default:
		return ""
	}
}

// sessionWindowText is the shared read+render tail for both the task-scope
// and goal-scope FR-032 window feeds (Simplifier Q2: the two scopes'
// wrappers below differ only in HOW they resolve sessionID — task scope
// resolves it via the task store first, goal scope already has it in
// GoalSessionID — the read+trim+render body is identical, so it lives here
// once). extraLogFields are merged into the Warn log's structured fields on
// a ReadTranscript failure (session_id and error are always included) — the
// task-scope wrapper passes its task_id for correlation; the goal-scope
// wrapper passes nil. Returns "" (never an error) on a read failure — window
// evidence is a best-effort enrichment, never a hard requirement for
// adjudication to proceed.
func (al *AgentLoop) sessionWindowText(store *session.UnifiedStore, sessionID string, extraLogFields map[string]any) string {
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		fields := map[string]any{"session_id": sessionID, "error": err.Error()}
		for k, v := range extraLogFields {
			fields[k] = v
		}
		logger.WarnCF("agent", "verifier: could not read session for window feed", fields)
		return ""
	}
	return renderVerifierWindowText(entries, al.effectiveVerifierWindowTokens())
}

// goalSessionWindowText renders the transcript window for a chat /goal
// verification (FR-032): the last N tokens of the session carrying the goal
// condition, read from the goal agent's own session store. Returns "" (never
// an error) on any missing/unresolvable input.
func (al *AgentLoop) goalSessionWindowText(goalSessionID, agentID string) string {
	if goalSessionID == "" || agentID == "" {
		return ""
	}
	store := al.GetAgentStore(agentID)
	if store == nil {
		return ""
	}
	return al.sessionWindowText(store, goalSessionID, nil)
}

// taskSessionWindowText resolves and renders a task's own working-session
// tail (FR-032): the task's SessionID (via the task store), then the last N
// tokens of that session, read from the assignee agent's own session store.
// Returns "" (never an error) on any missing/unresolvable input — window
// evidence is a best-effort enrichment, never a hard requirement for
// adjudication to proceed — but every branch that returns "" because a
// collaborator is missing/unreachable (as opposed to the ordinary "task has
// no session yet" case) logs a Warn with task_id so a persistently-broken
// window feed is observable, not silent.
func (al *AgentLoop) taskSessionWindowText(taskID, assigneeAgentID string) string {
	if taskID == "" || assigneeAgentID == "" {
		return ""
	}
	ts := GetTaskStore(al)
	if ts == nil {
		logger.WarnCF("agent", "verifier: no task store available for task window feed",
			map[string]any{"task_id": taskID})
		return ""
	}
	t, err := ts.Get(taskID)
	if err != nil {
		logger.WarnCF("agent", "verifier: could not resolve task for window feed",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return ""
	}
	if t == nil || t.SessionID == "" {
		return "" // task has no session yet — not an error, just nothing to feed
	}
	store := al.GetAgentStore(assigneeAgentID)
	if store == nil {
		logger.WarnCF("agent", "verifier: no session store for assignee agent (task window feed)",
			map[string]any{"task_id": taskID, "agent_id": assigneeAgentID})
		return ""
	}
	return al.sessionWindowText(store, t.SessionID, map[string]any{"task_id": taskID})
}

// renderTranscriptEntriesForWindow converts raw session.TranscriptEntry
// records into a flat, chronological []providers.Message the same shape
// every other in-package rendering/estimation helper already understands
// (context_budget.go's estimateMessageTokens). A delegated child sub-turn's
// own narration is skipped (IsDelegateChildEntry) — it is not part of the
// reviewed unit's own top-level thread, mirroring the same server-side
// suppression the live chat surfaces already apply. Tool calls are rendered
// as compact one-line summaries (tool name + status) so the deterministic
// "called X N times" style of evidence still survives a subjective/prose
// verifier's transcript window even though the dedicated `behavior`
// criteria kind (behavior_scan.go) already resolves that same class of
// criterion deterministically, with no LLM verifier dispatch at all — this
// rendering exists for whatever a prose criterion's own window still needs.
func renderTranscriptEntriesForWindow(entries []session.TranscriptEntry) []providers.Message {
	out := make([]providers.Message, 0, len(entries))
	for _, e := range entries {
		if e.IsDelegateChildEntry() {
			continue
		}
		if strings.TrimSpace(e.Content) != "" {
			role := e.Role
			if role == "" {
				role = "assistant"
			}
			out = append(out, providers.Message{Role: role, Content: e.Content})
		}
		for _, tc := range e.ToolCalls {
			out = append(out, providers.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("[tool_call] %s -> %s", tc.Tool, tc.Status),
			})
		}
	}
	return out
}

// renderVerifierWindowText takes entries, converts them via
// renderTranscriptEntriesForWindow, and keeps the LAST budgetTokens worth
// (FR-032's "last N tokens"), walking backward from the newest message so
// the most recent evidence is always retained in full before anything
// older is dropped. Renders the kept tail as plain "role: content" lines —
// this is prompt TEXT for the verifier's user message, not a message list
// sent to a provider directly.
func renderVerifierWindowText(entries []session.TranscriptEntry, budgetTokens int) string {
	msgs := renderTranscriptEntriesForWindow(entries)
	if len(msgs) == 0 {
		return ""
	}
	if budgetTokens <= 0 {
		budgetTokens = verifierWindowTokensDefault
	}
	start := len(msgs)
	used := 0
	for start > 0 {
		cost := estimateMessageTokens(msgs[start-1])
		if used > 0 && used+cost > budgetTokens {
			break
		}
		used += cost
		start--
	}
	kept := msgs[start:]
	var sb strings.Builder
	for _, m := range kept {
		fmt.Fprintf(&sb, "%s: %s\n", m.Role, m.Content)
	}
	return sb.String()
}

// --- inspect_session target scope (ADR-052 FR-033, R3-10/R3-11) -----------

// resolveVerifierSessionScope resolves the set of session IDs the verifier
// turn dispatched for in is authorized to read via the inspect_session tool
// (tools.WithVerifierSessionScope/VerifierSessionScopeAllows,
// pkg/tools/base.go) — engine-set, never client-suppliable. Per scope: task
// -> that task's own session only; plan -> that plan's member sessions only
// (GS-04: no single "plan session" exists); goal -> the chat session
// carrying the goal condition only. Returns nil (authorizes nothing —
// VerifierSessionScopeAllows fails closed on an unset/empty scope) when the
// relevant id(s) cannot be resolved.
func (al *AgentLoop) resolveVerifierSessionScope(in JudgeCriteriaInput) []string {
	switch in.Scope {
	case task.VerdictScopeTask:
		ts := GetTaskStore(al)
		if ts == nil || in.TaskID == "" {
			return nil
		}
		t, err := ts.Get(in.TaskID)
		if err != nil || t == nil || t.SessionID == "" {
			return nil
		}
		return []string{t.SessionID}
	case task.VerdictScopePlan:
		ts := GetTaskStore(al)
		if ts == nil || in.PlanID == "" {
			return nil
		}
		members, err := ts.List(task.Filter{PlanID: in.PlanID})
		if err != nil {
			logger.WarnCF("agent", "verifier: could not list plan member sessions for inspect_session scope",
				map[string]any{"plan_id": in.PlanID, "error": err.Error()})
			return nil
		}
		var sessions []string
		for i := range members {
			if members[i].SessionID != "" {
				sessions = append(sessions, members[i].SessionID)
			}
		}
		return sessions
	case task.VerdictScopeGoal:
		if in.GoalSessionID == "" {
			return nil
		}
		return []string{in.GoalSessionID}
	default:
		return nil
	}
}

// --- Verifier session type stamp (ADR-052 FR-036 — this wave's narrow
// slice only) -----------------------------------------------------------

// newVerifierSessionChatID resolves the chatID/TranscriptSessionID
// runVerifierAdjudication dispatches into. Wherever possible it PRE-CREATES
// the verifier's UnifiedStore session explicitly (mirroring
// createTaskSessionSync's exact pattern, task_executor.go) so its meta.json
// is written with Type="verifier" from the moment it exists — UnifiedStore
// has no post-creation "change type" seam (MetaPatch carries no Type field,
// and writeMetaLocked/createSessionLocked are package-private to
// pkg/session), so the type MUST be stamped at creation, not patched in
// afterward.
//
// Creation goes through session.NewVerifierSession — the ONLY sanctioned
// way to mint a verifier-typed session (its doc comment carries the
// spoof-prevention rationale: REST createSession deliberately cannot
// request type="verifier").
//
// sessionKey/unitID are used only for the fallback ad hoc chatID (matching
// Wave 1's exact original construction, byte for byte) and for a
// debuggable session Title — they do NOT change the registry/cancel key
// Wave 1 already wired (registry.Register(unitID, sessionKey) and
// processOptions.SessionKey both still use the caller's own sessionKey
// value, entirely untouched by this function).
//
// Falls back to the original ad hoc "verify:"+sessionKey string (Wave 1's
// exact prior, unstamped behavior) when the Judge's session store isn't
// resolvable yet (Judge not registered — the retry loop's own
// judgeInst-not-configured check independently handles that as a D7 pause
// regardless of what this returns) or session creation itself fails — a
// missing type stamp must never block adjudication.
func (al *AgentLoop) newVerifierSessionChatID(sessionKey, unitID string) string {
	fallback := "verify:" + sessionKey

	sessStore := al.GetAgentStore(string(coreagent.IDJudge))
	if sessStore == nil {
		return fallback
	}
	meta, err := sessStore.NewVerifierSession(string(coreagent.IDJudge))
	if err != nil {
		logger.WarnCF("agent",
			"verifier: could not pre-create type-stamped session; falling back to an unstamped ad hoc session id",
			map[string]any{"unit_id": unitID, "error": err.Error()})
		return fallback
	}
	title := "Verifier: " + unitID
	if setErr := sessStore.SetMeta(meta.ID, session.MetaPatch{Title: &title}); setErr != nil {
		logger.WarnCF("agent", "verifier: could not set verifier session title",
			map[string]any{"session_id": meta.ID, "error": setErr.Error()})
	}
	return meta.ID
}

// --- The verifier turn itself (ADR-052 FR-011) ------------------------------

// runVerifierAdjudication adjudicates proseCriteria by running ONE real
// agent turn, synchronously, in a FRESH verifier session under the seeded
// Judge System Agent's identity — replacing the old judgeProseCriteria raw
// Provider.Chat shortcut. Called only from JudgeCriteria (judge.go), whose
// own public signature is unchanged; this function is the entire internal
// conversion.
//
// Per adjudication (ADR-052 FR-011 / GS-02):
//  1. Creates a FRESH verifier session id (fresh-eyes impartiality —
//     session reuse/resume is a noted future direction only).
//  2. Registers it in the verifier-session registry BEFORE dispatch
//     (FR-037), so a Stop landing in the creation window cannot miss it.
//  3. Runs ONE agent turn synchronously in that session via
//     al.processTaskDirect — the SAME synchronous turn primitive task runs
//     use (processTaskDirect wraps runAgentLoop/runTurn, which registers in
//     al.activeTurnStates under the session key, so
//     RequestCancelForSession(sessionKey) reaches it exactly like any other
//     session).
//  4. Extracts the verdict from the turn's final message via the REQUIRED
//     structured verdict block (parseJudgeResponse, judge.go — the same
//     per-criterion JSON contract the old shortcut used), parsed
//     FAIL-CLOSED: missing/malformed -> every criterion unmet. D7-pause
//     semantics are preserved and are UNCHANGED in shape from the old
//     judgeProseCriteria loop — only the "make one call" step changed (a
//     full agent turn instead of a raw judgeInst.Provider.Chat call); a
//     provider-unavailable/SEC-26-denied/turn-error outcome is
//     Unavailable=true (attempt NOT consumed), never fail-closed-unmet.
//  5. Unregisters the session from the registry on return (success, fail-
//     closed, or ctx-canceled give-up alike).
func (al *AgentLoop) runVerifierAdjudication(
	ctx context.Context,
	in JudgeCriteriaInput,
	proseCriteria []task.AcceptanceCriterion,
	evidence []task.EvidenceRecord,
) (verdicts []task.CriterionVerdict, model, judgeAgentID string, unavailable bool, reason string) {
	unitID := verifierUnitID(in)
	registry := currentVerifierSessionRegistry()
	sessionKey := fmt.Sprintf("agent:%s:verify:%s", string(coreagent.IDJudge), uuid.New().String())
	chatID := al.newVerifierSessionChatID(sessionKey, unitID)

	registered := false
	defer func() {
		if registered {
			registry.Unregister(unitID)
		}
	}()

	windowText := al.resolveVerifierWindowText(in)

	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return nil, "", "", true, ctx.Err().Error()
		}

		judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
		if !ok || judgeInst == nil || judgeInst.Provider == nil {
			const notConfiguredReason = "judge_not_configured: Judge System Agent is not registered"
			logger.WarnCF("agent", "verifier: Judge System Agent not resolvable; pausing (D7 unavailability)", nil)
			if waitErr := al.judgeBackoffWait(ctx, attempt, notConfiguredReason); waitErr != nil {
				return nil, "", "", true, notConfiguredReason
			}
			continue
		}

		allowed, retryAfter, denyReason := al.checkJudgeSEC26(judgeInst.AgentType, judgeInst.ID)
		if !allowed {
			logger.WarnCF("agent", "verifier: SEC-26 gate denied verifier LLM call; pausing (D7 unavailability)",
				map[string]any{"reason": denyReason, "retry_after_s": retryAfter.Seconds()})
			if waitErr := al.judgeBackoffWait(ctx, attempt, denyReason); waitErr != nil {
				return nil, "", "", true, denyReason
			}
			continue
		}

		ensureVerifierSoul(judgeInst)

		prompt, buildErr := buildJudgeUserContent(proseCriteria, evidence, in.ClaimText, in.ExtraContext, windowText)
		if buildErr != nil {
			return failClosedProseVerdicts(proseCriteria, "internal error building verifier prompt: "+buildErr.Error()),
				"", "", false, "build_error"
		}

		if !registered {
			registry.Register(unitID, sessionKey)
			registered = true
		}

		callCtx, cancel := context.WithTimeout(ctx, judgeCallTimeout)
		// FR-033/R3-10/R3-11 (F2 half): plumb the engine-set inspect_session
		// target scope onto the verifier's own turn ctx BEFORE dispatch — the
		// ctx propagates through processTaskDirect's own derivation
		// (tools.WithAgentID(ctx, ...) etc.) into every tool call the
		// verifier's turn makes, so inspect_session's
		// VerifierSessionScopeAllows check (pkg/tools/inspect_session.go)
		// sees it. A scope that resolves to nil leaves ctx untouched
		// (WithVerifierSessionScope's own "empty is unset" contract), which
		// correctly fails inspect_session closed for every session id.
		callCtx = tools.WithVerifierSessionScope(callCtx, al.resolveVerifierSessionScope(in))
		content, callErr := al.processTaskDirect(callCtx, judgeInst.ID, prompt, sessionKey, chatID)
		cancel()

		if callErr != nil {
			logger.WarnCF("agent", "verifier: turn failed; pausing (D7 unavailability)",
				map[string]any{"error": callErr.Error()})
			if waitErr := al.judgeBackoffWait(ctx, attempt, callErr.Error()); waitErr != nil {
				return nil, "", "", true, callErr.Error()
			}
			continue
		}

		if strings.TrimSpace(content) == "" {
			// Ran but produced nothing -> fail-closed unmet (NFR-2), NOT
			// unavailable — the turn itself completed.
			return failClosedProseVerdicts(proseCriteria, "verifier turn produced no final content"),
				judgeInst.Model, judgeInst.ID, false, ""
		}

		parsed, parseErr := parseJudgeResponse(content)
		if parseErr != nil {
			return failClosedProseVerdicts(
				proseCriteria, "verifier response could not be parsed as valid JSON: "+parseErr.Error(),
			), judgeInst.Model, judgeInst.ID, false, ""
		}

		byID := dedupeJudgeCriteriaAnyUnmetWins(parsed.Criteria)
		out := make([]task.CriterionVerdict, 0, len(proseCriteria))
		for _, c := range proseCriteria {
			if pc, found := byID[c.ID]; found {
				out = append(out, task.CriterionVerdict{CriterionID: c.ID, Met: pc.Met, Reason: pc.Reason})
			} else {
				out = append(out, task.CriterionVerdict{
					CriterionID: c.ID, Met: false,
					Reason: "verifier did not return a verdict for this criterion (fail-closed, NFR-2)",
				})
			}
		}
		return out, judgeInst.Model, judgeInst.ID, false, ""
	}
}

// dedupeJudgeCriteriaAnyUnmetWins collapses a verifier's raw per-criterion
// responses into one response per criterion id, FAIL-CLOSED on a duplicate:
// a real LLM occasionally repeats an id across multiple JSON array entries,
// and a naive last-write-wins map assignment would let a later met:true
// duplicate silently override an earlier, correct met:false — laundering a
// real failure into a pass purely by array ordering. Here, if ANY duplicate
// entry for an id reports met:false, the collapsed result for that id stays
// unmet regardless of where in the array that entry appears.
func dedupeJudgeCriteriaAnyUnmetWins(responses []judgeCriterionResponse) map[string]judgeCriterionResponse {
	byID := make(map[string]judgeCriterionResponse, len(responses))
	for _, c := range responses {
		if existing, seen := byID[c.ID]; seen && !existing.Met {
			continue // an earlier unmet duplicate for this id is never overridden
		}
		byID[c.ID] = c
	}
	return byID
}
