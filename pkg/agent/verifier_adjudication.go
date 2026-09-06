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

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
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
	// it. Returns ErrVerifierSessionHeld if a LIVE (non-empty) DIFFERENT
	// session already holds unitID (CAS guard, corr-MAJOR-3/G-1) — the caller
	// must back off rather than clobber it.
	Register(unitID, sessionID string) error
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
// This is no longer the ONLY seed path: pkg/gateway's boot sequence
// (gateway.go's seedSystemAgentEagerSouls, called right after
// coreagent.SeedConfig) now also backfills the same file EAGERLY on every
// boot, for EVERY System Agent that has a compiled default soul, so the
// Judge's profile shows its default standards immediately on a fresh install
// instead of staying blank until the first real judgment (operator-reported
// UX gap: now that the Judge's soul is operator-editable in the SPA, an
// empty soul hides the very standards the operator would be overriding).
// ensureVerifierSoul remains as the LAZY BACKSTOP for any path that
// constructs an AgentInstance without going through gateway boot — e.g.
// pkg/agent's own test harnesses (newGoalLoopTestLoop and friends), which
// build an AgentLoop directly and never call gateway.RunContextWithOptions.
//
// JUDGE-ONLY BY DESIGN, and deliberately not generalised to the other System
// Agents (plan-supervisor-spec FR-005 rev 2): this hook is reached only from
// runVerifierAdjudication's own dispatch, which is the Judge's path and
// nothing else's. PlanSupervisor is woken over the MessageBus into an
// ordinary agent turn that never passes through here, so widening the id
// check would not give it a backstop — it would need a NEW call site in the
// ordinary instance-construction path, which FR-005 rev 2 explicitly does
// not take. For PlanSupervisor the eager boot seed is therefore the ONLY
// path, which is exactly why seedSystemAgentEagerSouls is the thing under
// test rather than an optimisation.
//
// Deliberately NOT done in coreagent.SeedConfig: that function is
// documented, and relied on by its own test suite (none of which sets
// OMNIPUS_HOME), as a PURE config-struct mutation with zero filesystem side
// effects. Writing a file there would start silently touching the real
// machine's home directory on every `go test ./pkg/coreagent/...` run.
// pkg/coreagent also cannot cleanly resolve the Judge's REAL workspace path
// itself: that resolution (OMNIPUS_HOME lookup, ID sanitization/traversal
// guard) lives in resolveAgentHome/ResolveAgentHome
// below, and pkg/coreagent cannot import pkg/agent to reach it (pkg/agent
// already imports pkg/coreagent) — reimplementing that logic a second time
// in pkg/coreagent would be a second source of truth that could silently
// drift from the path AgentInstance.Home actually resolves to at runtime.
// gateway boot, which already imports both packages, is the cleanest place
// that can call the real ResolveAgentHome and get an identical path.
//
// The actual write — mkdir + backfill-only-when-missing/empty + atomic
// write — is centralized in SeedSystemAgentSoulFile below so neither call
// site duplicates it.
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
	if err := SeedSystemAgentSoulFile(workspace, coreagent.IDJudge); err != nil {
		logger.WarnCF("agent", "verifier: could not seed default SOUL.md",
			map[string]any{"agent_id": agentInst.ID, "error": err.Error()})
	}
}

// SeedSystemAgentSoulFile backfills the SOUL.md of the seeded System Agent id
// at workspace with that agent's compiled default soul
// (coreagent.SystemAgentDefaultSoul — JudgeDefaultRubric for the Judge,
// PlanSupervisorDefaultRubric for the PlanSupervisor) — but ONLY when the file
// is missing or contains no real content (absent, or present but
// empty/whitespace-only, e.g. a 0-byte file from an interrupted write). It
// NEVER overwrites existing non-empty content: an operator's own soul edit
// (or a custom verifier's own SOUL.md) is preserved exactly like the deleted
// Rubric field's old "backfill only when empty" rule. That is what makes the
// eager boot seed safe to re-run on EVERY boot.
//
// ID-GENERIC ON PURPOSE (plan-supervisor-spec FR-005). It used to be
// SeedJudgeSoulFile with coreagent.JudgeDefaultRubric hardcoded, which meant
// the PlanSupervisor's rubric existed only as a Go constant and never reached
// disk — the adjudicator would have woken with an EMPTY prompt. Sourcing the
// text from coreagent.SystemAgentDefaultSoul keeps one source of truth per
// agent and makes a future third System Agent a change in pkg/coreagent only.
//
// Exported so both ensureVerifierSoul (the Judge-only lazy backstop above,
// called from an *AgentInstance with a resolved Home) and pkg/gateway's eager
// boot-time seed (which has only a workspace path, no AgentInstance yet)
// share the exact same write semantics instead of each hand-rolling it.
//
// Returns an error — never a silent no-op — for an id with no compiled
// default soul, so a caller that seeds a System Agent whose default text was
// forgotten in pkg/coreagent finds out instead of writing an empty SOUL.md
// that would then look "already seeded" to the next boot.
func SeedSystemAgentSoulFile(workspace string, id coreagent.CoreAgentID) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return fmt.Errorf("verifier: empty workspace for %q soul seed", id)
	}
	soul := coreagent.SystemAgentDefaultSoul(id)
	if strings.TrimSpace(soul) == "" {
		return fmt.Errorf("verifier: no compiled default soul for System Agent %q", id)
	}
	soulPath := filepath.Join(workspace, "SOUL.md")
	if existing, err := os.ReadFile(soulPath); err == nil {
		if strings.TrimSpace(string(existing)) != "" {
			return nil // operator (or a prior seed) already put real content here
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("verifier: read existing %q soul %q: %w", id, soulPath, err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return fmt.Errorf("verifier: create %q workspace %q: %w", id, workspace, err)
	}
	if err := fileutil.WriteFileAtomic(soulPath, []byte(soul), 0o644); err != nil {
		return fmt.Errorf("verifier: write default %q SOUL.md %q: %w", id, soulPath, err)
	}
	return nil
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

// sessionWindowText is the shared read+render tail for the task-scope,
// goal-scope FR-032 window feeds AND the ADR-079 D1 /goal-compile window
// feed (Simplifier Q2: the callers below differ only in HOW they resolve
// sessionID and WHICH token budget applies — the read+trim+render body is
// identical, so it lives here once). budgetTokens is the caller's own
// resolved bound (the Judge passes effectiveVerifierWindowTokens(); the
// compile feed, goalCompileWindowText in goal_compile_llm.go, passes
// effectiveGoalCompileWindowTokens() — a mechanical parameterization, ADR-079
// D1, that changes no existing caller's behavior since every pre-existing
// call site still passes its own effectiveVerifierWindowTokens()).
// extraLogFields are merged into the Warn log's structured fields on a
// ReadTranscript failure (session_id and error are always included) — the
// task-scope wrapper passes its task_id for correlation; the goal-scope and
// compile wrappers pass nil. Returns "" (never an error) on a read failure —
// window evidence is a best-effort enrichment, never a hard requirement for
// adjudication (or compilation) to proceed.
func (al *AgentLoop) sessionWindowText(store *session.UnifiedStore, sessionID string, budgetTokens int, extraLogFields map[string]any) string {
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		fields := map[string]any{"session_id": sessionID, "error": err.Error()}
		for k, v := range extraLogFields {
			fields[k] = v
		}
		logger.WarnCF("agent", "verifier: could not read session for window feed", fields)
		return ""
	}
	return renderVerifierWindowText(entries, budgetTokens)
}

// goalSessionWindowText renders the transcript window for a chat /goal
// verification (FR-032): the last N tokens of the session carrying the goal
// condition. Returns "" (never an error) on any missing/unresolvable input.
//
// STORE RESOLUTION (2026-09-06 UAT defect, judgment-first H-7 class): live
// chat sessions are written to the SHARED store (al.GetSessionStore() —
// whose own doc marks GetAgentStore as "legacy per-agent session access"),
// but this feed used to read ONLY the per-agent legacy store. Because
// ReadTranscript returns empty-with-no-error for a session directory that
// does not exist in the store it's asked, the window came back silently
// empty and the goal Judge fail-closed every criterion with "no evidence"
// against a session whose transcript plainly held the reply — observed live
// on the first end-to-end /goal UAT. The shared store is now consulted
// first; the legacy per-agent store remains as a fallback for old installs
// whose sessions still live there.
func (al *AgentLoop) goalSessionWindowText(goalSessionID, agentID string) string {
	if goalSessionID == "" || agentID == "" {
		logger.WarnCF("agent", "verifier: goal window feed skipped — empty session or agent id",
			map[string]any{"goal_session_id": goalSessionID, "agent_id": agentID})
		return ""
	}
	if shared := al.GetSessionStore(); shared != nil {
		if text := al.sessionWindowText(shared, goalSessionID, al.effectiveVerifierWindowTokens(), nil); text != "" {
			return text
		}
		logger.WarnCF("agent", "verifier: goal window empty from the SHARED store — falling back to the legacy per-agent store",
			map[string]any{"goal_session_id": goalSessionID, "agent_id": agentID})
	} else {
		logger.WarnCF("agent", "verifier: no shared session store — goal window falling back to the legacy per-agent store",
			map[string]any{"goal_session_id": goalSessionID, "agent_id": agentID})
	}
	store := al.GetAgentStore(agentID)
	if store == nil {
		return ""
	}
	return al.sessionWindowText(store, goalSessionID, al.effectiveVerifierWindowTokens(), nil)
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
	// Shared store first (see goalSessionWindowText's STORE RESOLUTION note —
	// same 2026-09-06 UAT defect class; task sessions are written to the
	// shared store too), legacy per-agent store as the old-install fallback.
	if shared := al.GetSessionStore(); shared != nil {
		if text := al.sessionWindowText(shared, t.SessionID, al.effectiveVerifierWindowTokens(), map[string]any{"task_id": taskID}); text != "" {
			return text
		}
	}
	store := al.GetAgentStore(assigneeAgentID)
	if store == nil {
		logger.WarnCF("agent", "verifier: no session store for assignee agent (task window feed)",
			map[string]any{"task_id": taskID, "agent_id": assigneeAgentID})
		return ""
	}
	return al.sessionWindowText(store, t.SessionID, al.effectiveVerifierWindowTokens(), map[string]any{"task_id": taskID})
}

// renderTranscriptEntriesForWindow converts raw session.TranscriptEntry
// records into a flat, chronological []providers.Message the same shape
// every other in-package rendering/estimation helper already understands
// (context_budget.go's estimateMessageTokens). ADR-057 D1/W11 (FR-034/
// FR-038): a delegated child now owns its own real store-backed session
// (FR-005), so its narration lives in the CHILD's OWN transcript.jsonl and
// is never present in the entries this function is handed — the old
// retired child-entry visibility predicate's skip (mirroring the now-deleted
// server-side suppression, FR-034) is removed outright, not reapplied;
// BDD-39 requires the verifier window see the adjudicated session's own
// entries and nothing else, which this window's `entries` slice already
// scopes to by
// construction. Tool calls are rendered as compact one-line summaries (tool
// name + status) so the deterministic "called X N times" style of evidence
// still survives a subjective/prose verifier's transcript window even
// though the dedicated `behavior` criteria kind (behavior_scan.go) already
// resolves that same class of criterion deterministically, with no LLM
// verifier dispatch at all — this rendering exists for whatever a prose
// criterion's own window still needs.
func renderTranscriptEntriesForWindow(entries []session.TranscriptEntry) []providers.Message {
	out := make([]providers.Message, 0, len(entries))
	for _, e := range entries {
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
// sessionKey/unitID are used to build the fallback ad hoc chatID (matching
// Wave 1's exact original construction, byte for byte) and a debuggable
// session Title. processOptions.SessionKey — the activeTurnStates MAP
// STORAGE key (registerActiveTurn stores under ts.sessionKey, turn.go) — is
// unaffected by this function and still carries the caller's own sessionKey
// value verbatim.
//
// This function's RETURN VALUE, chatID, is the CANCEL-MATCH key: it becomes
// processOptions.TranscriptSessionID, which newTurnState stamps onto
// ts.transcriptSessionID (turn.go) — the field GetActiveTurnHookForSession/
// RequestCancelForSession actually range-match on, never the map storage
// key. The caller (runVerifierAdjudication) registers unitID -> chatID in
// the verifier-session registry for exactly this reason. A prior version of
// this code (and this comment) registered unitID -> sessionKey instead —
// a value RequestCancelForSession can never match, since it is never
// compared against ts.transcriptSessionID — silently defeating Stop/`/goal
// clear` cancellation of an in-flight verifier turn (7-reviewer gate
// BLOCKER, FR-037/G1/G8).
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

// --- Judge-unavailability escalation (sign-off finding 1) ------------------
//
// Every JudgeCriteria caller (task_executor.go, plan_engine.go, goal_loop.go)
// already handles a single Unavailable result the same way: a WARN log and a
// silent pause/retry-later — the right response to a one-off transient blip.
// But that same handling makes a PERSISTENTLY down Judge (a dead provider,
// SEC-26's daily-cost cap permanently exhausted) operator-invisible: every
// individual WARN reads exactly like an isolated hiccup, so nothing in the
// logs distinguishes "the Judge stumbled once" from "verification has been
// stalled for hours." Fixing this in each of the three callers would be
// three near-duplicate implementations (and two of them are owned by other
// waves) — this is the single, CENTRAL point every adjudication already
// passes through, so it is fixed here once.

// verifierUnavailabilityEscalateAt is the number of CONSECUTIVE Unavailable
// outcomes for the SAME adjudication unit that crosses the ERROR escalation
// threshold (sign-off finding 1's "N=3").
const verifierUnavailabilityEscalateAt = 3

var (
	verifierUnavailabilityMu sync.Mutex
	//nolint:gochecknoglobals // process-wide consecutive-Unavailable counter, see recordVerifierAvailabilityOutcome's doc comment.
	verifierUnavailabilityStreak = make(map[string]int)
)

// verifierUnavailabilityEscalateFn is the seam recordVerifierAvailabilityOutcome
// calls once a unit's streak reaches/exceeds verifierUnavailabilityEscalateAt
// (and again on every occurrence thereafter — this must stay loud for as
// long as the condition persists, not fire once and go quiet). Production
// default logs at ERROR: an unambiguous, operator-actionable message,
// distinct from the WARN every caller already emits for a single blip.
// Overridable in tests (mirrors this file's own judgeSleepFn/
// killProcessGroupFn-style seam pattern) so the escalation is verifiable
// deterministically, without scraping the process's real log output.
var verifierUnavailabilityEscalateFn = func(unitID string, streak int) {
	logger.ErrorCF("agent",
		fmt.Sprintf(
			"verifier unavailable %d consecutive times for %s — verification is stalled; "+
				"check the Judge's provider/model and SEC-26 budget",
			streak, unitID,
		),
		map[string]any{"unit_id": unitID, "consecutive_unavailable": streak},
	)
}

// recordVerifierAvailabilityOutcome updates unitID's consecutive-Unavailable
// streak with the outcome runVerifierAdjudication is about to return to its
// caller for THIS invocation, and escalates via verifierUnavailabilityEscalateFn
// once (and on every call thereafter) the streak crosses
// verifierUnavailabilityEscalateAt.
//
// A non-Unavailable outcome clears the streak to zero — including a
// fail-closed verdict from a parse/content error (build_error, empty
// content, malformed JSON): those mean the Judge's LLM call itself DID
// complete, which is exactly the thing this streak is tracking the absence
// of. Only a run of genuine provider/SEC-26/turn-failure Unavailable results
// in a row is the signal this function exists to surface.
func recordVerifierAvailabilityOutcome(unitID string, unavailable bool) {
	if unitID == "" {
		return
	}
	verifierUnavailabilityMu.Lock()
	defer verifierUnavailabilityMu.Unlock()
	if !unavailable {
		delete(verifierUnavailabilityStreak, unitID)
		return
	}
	verifierUnavailabilityStreak[unitID]++
	streak := verifierUnavailabilityStreak[unitID]
	if streak >= verifierUnavailabilityEscalateAt {
		verifierUnavailabilityEscalateFn(unitID, streak)
	}
}

// --- Blocked-check honesty seams (FR-116/FR-137/FR-138, R§8.1) --------------
//
// classifyNonVerdict (the named M1 predicate) + UnableToVerifyTracker (the
// re-run bound m-4) + UnjudgeableEscalationGate (the escalate-once FR-138) are
// ALL defined in goal_compile.go — this file WIRES them into the live Judge
// path via process-wide instances + setter seams (mirrors this file's own
// verifierUnavailabilityStreak pattern). DoD-11: extend/wire, never redefine.

var (
	verifierTrackerMu sync.RWMutex
	//nolint:gochecknoglobals // process-wide seams, see the setters' doc comments.
	verifierUnableToVerifyTracker = NewUnableToVerifyTracker(UnableToVerifyMaxRerunsDefault)
	verifierUnjudgeableGate       = NewUnjudgeableEscalationGate()
)

// currentUnableToVerifyTracker returns the active unable_to_verify tracker
// under its own lock so a concurrent setter never races a reader.
func currentUnableToVerifyTracker() *UnableToVerifyTracker {
	verifierTrackerMu.RLock()
	defer verifierTrackerMu.RUnlock()
	return verifierUnableToVerifyTracker
}

// currentUnjudgeableEscalationGate returns the active escalate-once gate.
func currentUnjudgeableEscalationGate() *UnjudgeableEscalationGate {
	verifierTrackerMu.RLock()
	defer verifierTrackerMu.RUnlock()
	return verifierUnjudgeableGate
}

// SetVerifierUnableToVerifyTracker overrides the process-wide unable_to_verify
// tracker. Intended for tests that need a deterministic K bound or a spy;
// passing nil restores the default (UnableToVerifyMaxRerunsDefault).
func SetVerifierUnableToVerifyTracker(t *UnableToVerifyTracker) {
	verifierTrackerMu.Lock()
	defer verifierTrackerMu.Unlock()
	if t == nil {
		t = NewUnableToVerifyTracker(UnableToVerifyMaxRerunsDefault)
	}
	verifierUnableToVerifyTracker = t
}

// SetVerifierUnjudgeableEscalationGate overrides the process-wide escalate-
// once gate. Intended for tests; nil restores the default.
func SetVerifierUnjudgeableEscalationGate(g *UnjudgeableEscalationGate) {
	verifierTrackerMu.Lock()
	defer verifierTrackerMu.Unlock()
	if g == nil {
		g = NewUnjudgeableEscalationGate()
	}
	verifierUnjudgeableGate = g
}

// unableToVerifyEscalateFn fires when a check crosses the K consecutive
// unable_to_verify bound (persistently-blocked, FR-116/m-4), and again on
// every subsequent occurrence — the signal must stay loud for as long as the
// block persists (mirrors verifierUnavailabilityEscalateFn). Overridable in
// tests so the escalation is verifiable without scraping process logs.
var unableToVerifyEscalateFn = func(unitKey, criterionID string, streak int) {
	logger.ErrorCF("agent",
		fmt.Sprintf(
			"verifier: check %q persistently blocked — verification mechanism could not run "+
				"(%d consecutive unable_to_verify) for %s; escalated to owner",
			criterionID, streak, unitKey,
		),
		map[string]any{"unit_key": unitKey, "criterion_id": criterionID, "consecutive_unable_to_verify": streak},
	)
}

// unjudgeableEscalateFn fires exactly once per adjudication-unit when a
// criterion resolves criterion_unjudgeable (the verifier turn RAN but formed
// no judgment, FR-115/FR-138). The escalation SURFACES the mis-compile — it
// does not itself halt round consumption (M2). Overridable in tests.
var unjudgeableEscalateFn = func(unitKey, criterionID string) {
	logger.ErrorCF("agent",
		fmt.Sprintf(
			"verifier: criterion %q unjudgeable for %s — the verifier turn ran but formed no judgment; "+
				"owner should re-state the goal (amendment) or /goal clear",
			criterionID, unitKey,
		),
		map[string]any{"unit_key": unitKey, "criterion_id": criterionID},
	)
}

// --- Workspace diff evidence feed (G-3/G-15, FR-144) ------------------------
//
// resolveVerifierDiffText returns the write-set-scoped workspace diff text for
// the prose Judge's user message (the real file changes, not a transcript
// window alone). It opens the Phase-1 git evidence repo at the work-under-
// review's work/ dir and reads AttemptDiff. Best-effort throughout: an unbound
// goal (no WorkspaceID), a nested user repo, an unborn HEAD, or any gitevidence
// error returns "" — the Judge degrades to machine evidence + transcript
// window + claim, never a hard failure (mirrors windowText's own contract).
func (al *AgentLoop) resolveVerifierDiffText(in JudgeCriteriaInput) string {
	wsID := strings.TrimSpace(in.WorkspaceID)
	if wsID == "" {
		return "" // unbound chat goal — no work-under-review workspace to diff
	}
	home := config.OmnipusHomeDir()
	if home == "" {
		return ""
	}
	dir, err := workspace.SafeWorkDir(home, wsID)
	if err != nil {
		return "" // invalid workspace id — not a diff-feed concern
	}
	repo, err := gitevidence.Open(dir)
	if err != nil {
		// Nested user repo (ErrNestedRepo) or any other Open error: the git
		// layer degrades for this workspace (MIN-6). Logged at WARN inside
		// gitevidence.Open/EnsureWorkDir already; here it is a silent skip.
		return ""
	}
	ev, err := repo.AttemptDiff(nil) // unscoped: the whole latest boundary commit
	if err != nil {
		logger.WarnCF("agent", "verifier: could not read workspace diff evidence",
			map[string]any{"workspace_id": wsID, "error": err.Error()})
		return ""
	}
	return renderDiffEvidence(ev)
}

// renderDiffEvidence formats a DiffEvidence as the concise text block the prose
// Judge consumes. Empty (no files / unborn HEAD) → "". The patch text is
// capped per-file so a single huge diff cannot blow the verifier's context.
func renderDiffEvidence(ev *gitevidence.DiffEvidence) string {
	if ev == nil || len(ev.Files) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "(from %q to %q; %d of %d changed paths in scope)\n",
		ev.FromHash, ev.ToHash, ev.Matched, ev.Total)
	for _, f := range ev.Files {
		patch := f.Patch
		if len(patch) > diffPatchCap {
			patch = patch[:diffPatchCap] + "\n…[diff truncated]"
		}
		fmt.Fprintf(&sb, "--- %s (%s) ---\n%s\n", f.Path, f.Kind, patch)
	}
	return sb.String()
}

// diffPatchCap bounds each file's unified-diff text fed to the prose Judge, so
// one very large change cannot crowd out the rest of the evidence.
const diffPatchCap = 16 * 1024

// --- The verifier turn itself (ADR-052 FR-011) ------------------------------

// runVerifierAdjudication adjudicates proseCriteria by running ONE real
// agent turn, synchronously, in a FRESH verifier session under the seeded
// Judge System Agent's identity — replacing the old judgeProseCriteria raw
// Provider.Chat shortcut. Called only from JudgeCriteria (judge.go), whose
// own public signature is unchanged; this function is the entire internal
// conversion.
//
// Per adjudication (ADR-052 FR-011 / GS-02):
//  1. Creates a FRESH, type-stamped verifier session id (fresh-eyes
//     impartiality — session reuse/resume is a noted future direction only)
//     — lazily, only once the ctx/Judge-registered/SEC-26 gates below have
//     all passed, NOT eagerly at function entry, so an early bail on a
//     retried attempt never orphans an empty on-disk verifier session
//     (7-reviewer gate item 3).
//  2. Registers THAT session id (chatID, not sessionKey — see the BLOCKER
//     fix noted on newVerifierSessionChatID's own doc comment) in the
//     verifier-session registry BEFORE dispatch (FR-037), so a Stop landing
//     in the creation window cannot miss it.
//  3. Runs ONE agent turn synchronously in that session via
//     al.processTaskDirect — the SAME synchronous turn primitive task runs
//     use. processTaskDirect wraps runAgentLoop/runTurn, which stores the
//     turnState in al.activeTurnStates keyed by sessionKey (map storage key
//     only) but stamps ts.transcriptSessionID = chatID — the field
//     GetActiveTurnHookForSession/RequestCancelForSession actually
//     range-match on — so it is RequestCancelForSession(chatID), never
//     RequestCancelForSession(sessionKey), that reaches this turn.
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
	diffText string,
) (verdicts []task.CriterionVerdict, model, judgeAgentID string, unavailable bool, reason string, unjudgeableIDs []string) {
	unitID := verifierUnitID(in)
	registry := currentVerifierSessionRegistry()
	sessionKey := fmt.Sprintf("agent:%s:verify:%s", string(coreagent.IDJudge), uuid.New().String())
	// chatID is created lazily, immediately before its first use below (item
	// 3, 7-reviewer gate) — NOT here — so a bail on ctx.Err()/Judge-not-
	// registered/SEC-26-denied never pre-creates an on-disk verifier session
	// that is then abandoned.
	var chatID string

	registered := false
	defer func() {
		if registered {
			registry.Unregister(unitID)
		}
	}()
	// Sign-off finding 1: record this call's outcome against unitID's
	// consecutive-Unavailable streak. Reads the named return `unavailable`
	// AFTER it has been set by whichever return statement below fires — a
	// defer over a named return always observes the final value.
	defer func() {
		recordVerifierAvailabilityOutcome(unitID, unavailable)
	}()

	windowText := al.resolveVerifierWindowText(in)

	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return nil, "", "", true, ctx.Err().Error(), nil
		}

		judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
		if !ok || judgeInst == nil || judgeInst.Provider == nil {
			const notConfiguredReason = "judge_not_configured: Judge System Agent is not registered"
			logger.WarnCF("agent", "verifier: Judge System Agent not resolvable; pausing (D7 unavailability)", nil)
			if waitErr := al.judgeBackoffWait(ctx, attempt, notConfiguredReason); waitErr != nil {
				return nil, "", "", true, notConfiguredReason, nil
			}
			continue
		}

		allowed, retryAfter, denyReason := al.checkJudgeSEC26(judgeInst.AgentType, judgeInst.ID)
		if !allowed {
			logger.WarnCF("agent", "verifier: SEC-26 gate denied verifier LLM call; pausing (D7 unavailability)",
				map[string]any{"reason": denyReason, "retry_after_s": retryAfter.Seconds()})
			if waitErr := al.judgeBackoffWait(ctx, attempt, denyReason); waitErr != nil {
				return nil, "", "", true, denyReason, nil
			}
			continue
		}

		ensureVerifierSoul(judgeInst)

		prompt, buildErr := buildJudgeUserContent(proseCriteria, evidence, in.ClaimText, in.ExtraContext, windowText, diffText)
		if buildErr != nil {
			return failClosedProseVerdicts(proseCriteria, "internal error building verifier prompt: "+buildErr.Error()),
				"", "", false, "build_error", nil
		}

		if !registered {
			// CAS pre-check (corr-MAJOR-3, G-1): if a LIVE verifier session
			// already holds this unit, a concurrent adjudication is in flight
			// (an idle-tick + claim-turn race). Back off as "unavailable" —
			// BEFORE creating a verifier session, so we don't pre-create +
			// abandon an on-disk session. The atomic Register CAS below is the
			// real guard against the residual check-then-act gap this Lookup
			// cannot close. The pre-check uses the richer VerifierSessionRegistry
			// interface via a type assertion: the production seam (the concrete
			// *verifierSessionRegistry) always satisfies it; a minimal spy that
			// does not skips the pre-check and relies on the CAS alone (the
			// spy's Register returns nil, so the CAS never rejects in tests).
			if richer, ok := registry.(VerifierSessionRegistry); ok {
				if existing, held := richer.Lookup(unitID); held && existing != "" {
					reason = "concurrent adjudication in flight for unit"
					unavailable = true
					return nil, "", "", true, reason, nil
				}
			}
			// Create the type-stamped verifier session now — right before it
			// is first needed — and register ITS id (chatID), not sessionKey
			// (BLOCKER fix, FR-037/G1/G8): sessionKey is only the
			// activeTurnStates map storage key; chatID becomes
			// ts.transcriptSessionID, the value Stop/`/goal clear`'s
			// RequestCancelForSession actually matches turns on.
			chatID = al.newVerifierSessionChatID(sessionKey, unitID)
			if regErr := registry.Register(unitID, chatID); regErr != nil {
				// CAS guard (corr-MAJOR-3, G-1): lost the race between the
				// Lookup pre-check above and this atomic Register — another
				// adjudication registered a live session in the gap. Back off
				// as "unavailable" (no-round retry); the in-flight adjudication
				// will resolve the goal. Do NOT set registered=true — this
				// call did not create the winning entry, so the deferred
				// Unregister must not evict the other adjudication's live
				// session. The chatID just minted is an abandoned shell, same
				// as any other pre-create failure path above.
				reason = "concurrent adjudication in flight for unit"
				unavailable = true
				return nil, "", "", true, reason, nil
			}
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
		// Product-blocker fix (ADR-052 FR-011/012 x ADR-046 P1, operator
		// decision "make the judge a member of every workspace"): the Judge
		// is an IMPLICIT member of every workspace (pkg/workspace's
		// isImplicitMember), so this is never needed to AVOID a refusal —
		// it is the "preferring" selector that picks WHICH of those implicit
		// memberships the verifier's turn roots in (the work-under-review's
		// own workspace), so its read-only escalation tools (FR-012(c)) can
		// actually reach the artifacts they exist to inspect, rather than
		// an arbitrary sorted-first workspace.
		// Sign-off 14 MINOR-1 / architect F4: an UNBOUND /goal (Scope==goal
		// with no WorkspaceID) has no work-under-review workspace to prefer
		// at all — there is nothing for optWorkspaceID to select AMONG, so
		// falling through to FindForAgentPreferring's ordinary sorted-first
		// pick would root the turn in an arbitrary one of the Judge's
		// (every) implicit memberships. That is benign today only because
		// real deployments happen to be single-tenant; it is still the
		// wrong THING to pick, not merely a low-risk one. Root at the
		// Judge's own agent home instead — exactly the rooting
		// resolveTurnWorkDirOrRefuse's pre-onboarding "no workspace exists
		// yet" branch already expresses for the "nothing to prefer" case,
		// requested here via WithSystemAgentAgentHomeOverride (one small,
		// explicit branch — never a bypass of the ordinary selector for any
		// other scope/WorkspaceID combination).
		if in.Scope == task.VerdictScopeGoal && strings.TrimSpace(in.WorkspaceID) == "" {
			callCtx = WithSystemAgentAgentHomeOverride(callCtx)
		} else {
			callCtx = WithSystemAgentWorkspaceOverride(callCtx, in.WorkspaceID)
		}
		content, callErr := al.processTaskDirect(callCtx, judgeInst.ID, prompt, sessionKey, chatID)
		cancel()

		if callErr != nil {
			logger.WarnCF("agent", "verifier: turn failed; pausing (D7 unavailability)",
				map[string]any{"error": callErr.Error()})
			if waitErr := al.judgeBackoffWait(ctx, attempt, callErr.Error()); waitErr != nil {
				return nil, "", "", true, callErr.Error(), nil
			}
			continue
		}

		if strings.TrimSpace(content) == "" {
			// R§8.1/FR-138: the verifier turn RAN to completion but formed no
			// judgment → criterion_unjudgeable (NOT unavailable — the turn did
			// complete; NOT a clean verdict either). The criteria resolve unmet
			// for this adjudication AND each is flagged unjudgeable so
			// JudgeCriteria emits the escalate-once (M1 predicate: mechanism
			// ran, no judgment). Old path fail-closed silently (the bug).
			return failClosedProseVerdicts(proseCriteria,
					"criterion_unjudgeable: verifier turn ran but produced no content"),
				judgeInst.Model, judgeInst.ID, false, "", allProseCriterionIDs(proseCriteria)
		}

		parsed, parseErr := parseJudgeResponse(content)
		if parseErr != nil {
			// R§8.1/FR-138: ran but returned no parseable judgment →
			// criterion_unjudgeable for every criterion (same M1 predicate).
			return failClosedProseVerdicts(
				proseCriteria, "criterion_unjudgeable: verifier response could not be parsed: "+parseErr.Error(),
			), judgeInst.Model, judgeInst.ID, false, "", allProseCriterionIDs(proseCriteria)
		}

		byID := dedupeJudgeCriteriaAnyUnmetWins(parsed.Criteria)
		out := make([]task.CriterionVerdict, 0, len(proseCriteria))
		var missing []string // criteria the verifier RAN on but omitted → unjudgeable
		for _, c := range proseCriteria {
			if pc, found := byID[c.ID]; found {
				out = append(out, task.CriterionVerdict{CriterionID: c.ID, Met: pc.Met, Reason: pc.Reason, EvidenceQuote: pc.EvidenceQuote})
			} else {
				// The verifier turn ran and judged OTHER criteria but returned
				// no verdict for THIS one → criterion_unjudgeable (ran, no
				// judgment for this criterion), FR-138.
				missing = append(missing, c.ID)
				out = append(out, task.CriterionVerdict{
					CriterionID: c.ID, Met: false,
					Reason: "criterion_unjudgeable: verifier did not return a verdict for this criterion",
				})
			}
		}
		return out, judgeInst.Model, judgeInst.ID, false, "", missing
	}
}

// allProseCriterionIDs returns the id of every criterion in cs — used when the
// verifier turn ran but formed no judgment AT ALL (empty / unparseable), so
// JudgeCriteria can flag every prose criterion criterion_unjudgeable.
func allProseCriterionIDs(cs []task.AcceptanceCriterion) []string {
	ids := make([]string, 0, len(cs))
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	return ids
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
