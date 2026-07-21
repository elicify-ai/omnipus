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
)

// verifierWindowTokensDefault is ADR-052 FR-032's confirmed default transcript
// window size (operator interview 2026-07-21: "N=20000"). // WAVE-MERGE:
// pkg/config.PlanningConfig.VerifierWindowTokens is being added by a
// parallel wave and does not exist in this worktree yet. Once it lands,
// effectiveVerifierWindowTokens should read cfg.Planning.VerifierWindowTokens
// (falling back to this const when <= 0) instead of returning this const
// unconditionally — see that function's own doc comment.
const verifierWindowTokensDefault = 20000

// effectiveVerifierWindowTokens resolves the transcript-window token budget
// (FR-032). Currently always the confirmed default; see the WAVE-MERGE note
// on verifierWindowTokensDefault for the pending per-install override.
func (al *AgentLoop) effectiveVerifierWindowTokens() int {
	return verifierWindowTokensDefault
}

// --- Verifier-session registry (ADR-052 FR-037) -----------------------------

// VerifierSessionRegistry is the minimal seam runVerifierAdjudication uses to
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
type VerifierSessionRegistry interface {
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

// defaultVerifierSessionRegistry is a minimal, safe, in-memory
// VerifierSessionRegistry used until the plan-engine wave wires a richer
// one. Safe for concurrent use.
type defaultVerifierSessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]string
}

func newDefaultVerifierSessionRegistry() *defaultVerifierSessionRegistry {
	return &defaultVerifierSessionRegistry{sessions: make(map[string]string)}
}

func (r *defaultVerifierSessionRegistry) Register(unitID, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[unitID] = sessionID
}

func (r *defaultVerifierSessionRegistry) Unregister(unitID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, unitID)
}

// Lookup returns the live verifier session id for unitID, if any. Exposed
// for tests and for a Stop fan-out that has not yet wired its own registry
// via SetVerifierSessionRegistry.
func (r *defaultVerifierSessionRegistry) Lookup(unitID string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[unitID]
	return s, ok
}

var (
	verifierSessionRegistryMu sync.RWMutex
	//nolint:gochecknoglobals // package-wide seam, see SetVerifierSessionRegistry's doc comment.
	verifierSessionRegistry VerifierSessionRegistry = newDefaultVerifierSessionRegistry()
)

// SetVerifierSessionRegistry overrides the package-wide verifier-session
// registry (ADR-052 FR-037). Intended to be called once at engine
// construction by the plan-engine wave to wire its real Stop-fan-out-
// queryable registry; tests may also call this to inject a spy. Passing nil
// restores a fresh safe default (never leaves the package with no
// registry).
func SetVerifierSessionRegistry(r VerifierSessionRegistry) {
	verifierSessionRegistryMu.Lock()
	defer verifierSessionRegistryMu.Unlock()
	if r == nil {
		r = newDefaultVerifierSessionRegistry()
	}
	verifierSessionRegistry = r
}

// currentVerifierSessionRegistry returns the active registry under its own
// lock so a concurrent SetVerifierSessionRegistry call never races a reader.
func currentVerifierSessionRegistry() VerifierSessionRegistry {
	verifierSessionRegistryMu.RLock()
	defer verifierSessionRegistryMu.RUnlock()
	return verifierSessionRegistry
}

// verifierUnitID resolves the registry key a Stop fan-out will look up a
// verifier session by, from whatever JudgeCriteriaInput already carries.
func verifierUnitID(in JudgeCriteriaInput) string {
	if in.TaskID != "" {
		return "task:" + in.TaskID
	}
	if in.PlanID != "" {
		return "plan:" + in.PlanID
	}
	// WAVE-MERGE: goal_loop.go:292 (task.VerdictScopeGoal caller) does not
	// yet pass a chat-session identifier through JudgeCriteriaInput — the
	// third FR-037 unitID kind — and that file is out of this wave's
	// ownership. Fall back to a per-agent key so a /goal verifier still gets
	// SOME registry entry; once goal_loop.go passes the session id, key off
	// that instead (it is the correct, collision-free unitID for /goal).
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
		// WAVE-MERGE: goal_loop.go:292 does not yet pass the chat session id
		// through JudgeCriteriaInput (out of this wave's ownership — see
		// verifierUnitID's identical note). Until it does, a /goal verifier
		// still judges correctly from criteria + claim alone; it simply
		// lacks the session-window evidence channel FR-032 describes for
		// /goal specifically.
		return ""
	default:
		return ""
	}
}

// taskSessionWindowText resolves and renders a task's own working-session
// tail. Returns "" (never an error) on any missing/unresolvable input —
// window evidence is a best-effort enrichment, never a hard requirement for
// adjudication to proceed.
func (al *AgentLoop) taskSessionWindowText(taskID, assigneeAgentID string) string {
	if taskID == "" || assigneeAgentID == "" {
		return ""
	}
	ts := GetTaskStore(al)
	if ts == nil {
		return ""
	}
	t, err := ts.Get(taskID)
	if err != nil || t == nil || t.SessionID == "" {
		return ""
	}
	store := al.GetAgentStore(assigneeAgentID)
	if store == nil {
		return ""
	}
	entries, err := store.ReadTranscript(t.SessionID)
	if err != nil {
		logger.WarnCF("agent", "verifier: could not read task session for window feed",
			map[string]any{"task_id": taskID, "session_id": t.SessionID, "error": err.Error()})
		return ""
	}
	return renderVerifierWindowText(entries, al.effectiveVerifierWindowTokens())
}

// renderTranscriptEntriesForWindow converts raw session.TranscriptEntry
// records into a flat, chronological []providers.Message the same shape
// every other in-package rendering/estimation helper already understands
// (context_budget.go's estimateMessageTokens). A delegated child sub-turn's
// own narration is skipped (IsDelegateChildEntry) — it is not part of the
// reviewed unit's own top-level thread, mirroring the same server-side
// suppression the live chat surfaces already apply. Tool calls are rendered
// as compact one-line summaries (tool name + status) so the deterministic
// "called X N times" style of evidence survives the window even before the
// dedicated `behavior` criteria kind (a different wave's work) exists.
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
	chatID := "verify:" + sessionKey

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

		byID := make(map[string]judgeCriterionResponse, len(parsed.Criteria))
		for _, c := range parsed.Criteria {
			byID[c.ID] = c
		}
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
