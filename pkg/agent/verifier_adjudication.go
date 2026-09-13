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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/audit"
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
// SCOPE IS CONSULTED FIRST (review finding 3, 2026-09-11). This used to be a
// flat TaskID -> PlanID -> goal ladder with no reference to in.Scope at all,
// which was correct only while the three ids were mutually exclusive. ADR-086
// C-08 ended that: validate() now LEGALISES a goal-scope input carrying
// TaskID (a running task's own goal), so the flat ladder registered a
// task-owned goal's verifier under "task:<id>" while `/goal clear`
// (goal_loop.go's cancelGoalVerifierIfAny) and the duplicate-dispatch guard
// (goal_triggers.go's goalAdjudicationInFlight) both looked up
// verifierUnitForGoal(sessionID). Two consequences, both silent: `/goal
// clear` could never cancel that verifier, and the in-flight guard missed, so
// a second concurrent adjudication for the same goal was admitted.
//
// WHICH id the goal arm keys on also changed, and deliberately: it is the
// ACTIVE CHAT SESSION id (GoalSessionID), because that is the id BOTH
// lookups pass — they are reached from a chat session, not from a goal
// record, and neither is in a position to resolve a GoalID. Keying the
// registration on GoalID (C-08's original intent) while the lookups key on
// the session id is the same disagreement in a second place. GoalID remains
// the fallback for a goal-scope caller that has only that.
//
// The scope-less ladder is preserved verbatim as the default arm: several
// callers (and the pinned tests) construct a JudgeCriteriaInput with ids but
// no Scope, and their keys must not move.
func verifierUnitID(in JudgeCriteriaInput) string {
	switch in.Scope {
	case task.VerdictScopeTask:
		if in.TaskID != "" {
			return verifierUnitForTask(in.TaskID)
		}
	case task.VerdictScopePlan:
		if in.PlanID != "" {
			return verifierUnitForPlan(in.PlanID)
		}
	case task.VerdictScopeGoal:
		// The collision-free /goal unit key (FR-037): /goal clear and the
		// in-flight guard look exactly this up. TaskID is IGNORED here even
		// when set — a task-owned goal is still a goal (ADR-086: task goals
		// and chat goals behave identically), and its own task-scope
		// adjudication keys on "task:<id>" separately.
		if in.GoalSessionID != "" {
			return verifierUnitForGoal(in.GoalSessionID)
		}
		if in.GoalID != "" {
			return verifierUnitForGoal(in.GoalID)
		}
		return "goal-agent:" + in.AssigneeAgentID
	}

	// No (or unrecognised) Scope: the historical precedence ladder.
	if in.TaskID != "" {
		return verifierUnitForTask(in.TaskID)
	}
	if in.PlanID != "" {
		return verifierUnitForPlan(in.PlanID)
	}
	if id := in.goalScopeCorrelatingID(); id != "" {
		return verifierUnitForGoal(id)
	}
	// Defensive fallback for a goal-scope caller that somehow passed no
	// correlating id — a per-agent key so the verifier still gets SOME
	// registry entry rather than none.
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
	// FR-105: redact tier-1 tool-call parameters/errors through the SAME
	// RegisterSensitiveValues path every other tool-result-derived text the
	// Judge sees already goes through (judge.go's evidenceStore() redact
	// closure) — this feed puts recipients/tokens/paths in front of a model
	// that did not previously see them.
	return renderVerifierWindowText(entries, budgetTokens, withTierOneRedact(al.tierOneRedactFn()))
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
//
// FR-105/FR-106 (ADR-084 revision 9, D14, wave E10): tool calls now render
// as "{tool, parameters, status, error}" (judge_evidence_tiers.go's
// formatTierOneToolCall), durable fields only, bounded and optionally
// redacted per opts — replacing the old name+status-only summary (C26's
// defect: "the change is at renderTranscriptEntriesForWindow"). Signature
// stays backward-compatible (opts is a variadic tail) because
// verifier_adjudication_test.go — E9's write-set, not this wave's — calls
// this function directly with the old one-argument form; every such call
// keeps compiling and keeps its old behaviour (redact defaults to
// identity, below the 32 KiB drop threshold every existing test fixture
// is far under).
func renderTranscriptEntriesForWindow(
	entries []session.TranscriptEntry, opts ...tierOneRenderOption,
) []providers.Message {
	var ropts tierOneRenderOptions
	for _, opt := range opts {
		opt(&ropts)
	}
	redact := ropts.redact

	type item struct {
		msg      providers.Message
		toolCall bool
		bytes    int
	}
	items := make([]item, 0, len(entries))
	for _, e := range entries {
		if strings.TrimSpace(e.Content) != "" {
			role := e.Role
			if role == "" {
				role = "assistant"
			}
			items = append(items, item{msg: providers.Message{Role: role, Content: e.Content}})
		}
		for _, tc := range e.ToolCalls {
			content := formatTierOneToolCall(tc, redact)
			items = append(items, item{
				msg:      providers.Message{Role: "assistant", Content: content},
				toolCall: true,
				bytes:    len(content),
			})
		}
	}

	// FR-105's 32 KiB whole-tier-1-block bound: drop the OLDEST tool-call
	// summaries first — never a narration/content message — until the
	// surviving tool-call bytes fit, recording how many were dropped.
	total := 0
	for _, it := range items {
		if it.toolCall {
			total += it.bytes
		}
	}
	dropped := 0
	if total > tierOneBlockByteLimit {
		kept := make([]item, 0, len(items))
		for _, it := range items {
			if it.toolCall && total > tierOneBlockByteLimit {
				total -= it.bytes
				dropped++
				continue
			}
			kept = append(kept, it)
		}
		items = kept
	}

	out := make([]providers.Message, 0, len(items)+1)
	if dropped > 0 {
		out = append(out, providers.Message{
			Role: "assistant",
			Content: fmt.Sprintf(
				"[tier-1 evidence] %d earlier tool-call summaries dropped — the 32 KiB tier-1 "+
					"evidence block cap was reached (JUDGE-FR-105)", dropped),
		})
	}
	for _, it := range items {
		out = append(out, it.msg)
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
func renderVerifierWindowText(
	entries []session.TranscriptEntry, budgetTokens int, opts ...tierOneRenderOption,
) string {
	msgs := renderTranscriptEntriesForWindow(entries, opts...)
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
// -> that task's own session PLUS every descendant session at any depth
// (D1a, FR-010/FR-011); plan -> each member session PLUS its own
// descendants (GS-04: no single "plan session" exists; FR-012); goal -> the
// chat session carrying the goal condition PLUS its descendants (FR-010).
// Delegated work (a subagent turn a worker spawned) writes to its OWN child
// session (ADR-057 D1/W11), so without the descendant walk a criterion
// whose real evidence lives in delegated work could never be reached by
// inspect_session at all — see goalDescendantSessionIDs' own doc comment
// (C4/D1a) for the durable, transitive walker this reuses unmodified.
// Returns nil (authorizes nothing — VerifierSessionScopeAllows fails closed
// on an unset/empty scope) when the relevant root id(s) cannot be resolved.
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
		return al.scopeWithDescendants(in.AssigneeAgentID, []string{t.SessionID})
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
		var roots []string
		for i := range members {
			if members[i].SessionID != "" {
				roots = append(roots, members[i].SessionID)
			}
		}
		return al.scopeWithDescendants(in.AssigneeAgentID, roots)
	case task.VerdictScopeGoal:
		if in.GoalSessionID == "" {
			return nil
		}
		return al.scopeWithDescendants(in.AssigneeAgentID, []string{in.GoalSessionID})
	default:
		return nil
	}
}

// allSessionsForDescendantWalk enumerates every session this AgentLoop can
// see, from BOTH the shared store (where live chat/task/goal sessions are
// written today) AND the assignee agent's own legacy per-agent store — they
// are genuinely TWO DIFFERENT *session.UnifiedStore instances (GetAgentStore's
// own doc comment: "kept for legacy per-agent session access"; GetSessionStore's:
// "the shared UnifiedStore for new sessions"), not a fallback pair where one
// subsumes the other. A parent/child edge written under one store's session
// directory is invisible to the other's ListSessions, so the descendant
// walk (D1a) must see the UNION or it silently misses real delegated-work
// sessions — the same 2026-09-06 UAT defect class goalSessionWindowText's
// STORE RESOLUTION note already documents for a single-session read; this is
// its list-enumeration counterpart. A read error on either store degrades
// that store's contribution to empty (WARN-logged) rather than failing the
// whole walk.
func (al *AgentLoop) allSessionsForDescendantWalk(assigneeAgentID string) []*session.UnifiedMeta {
	var all []*session.UnifiedMeta
	seen := make(map[string]bool)
	add := func(store *session.UnifiedStore, label string) {
		if store == nil {
			return
		}
		metas, err := store.ListSessions()
		if err != nil {
			logger.WarnCF("agent",
				"verifier: could not list sessions for inspect_session descendant scope",
				map[string]any{"store": label, "error": err.Error()})
			return
		}
		for _, m := range metas {
			if m == nil || seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			all = append(all, m)
		}
	}
	add(al.GetSessionStore(), "shared")
	if assigneeAgentID != "" {
		add(al.GetAgentStore(assigneeAgentID), "per-agent")
	}
	return all
}

// scopeWithDescendants resolves rootIDs plus every descendant session at
// any depth (D1a, FR-010–FR-012), via the SAME durable, transitive walker
// goal_triggers.go's quiet-goal sweep already uses (goalDescendantSessionIDs,
// C4) — no new pkg/session surface, no second walker implementation.
//
// FR-013: neither store being resolvable, or both erroring, degrades the
// scope to rootIDs ALONE (never wider than what was asked for); it never
// fails the adjudication itself — inspect_session simply denies anything
// outside the narrower scope, which is the fail-closed direction (FR-014:
// a criterion whose evidence lies outside the resolved scope must resolve
// unmet/unable_to_verify, never met).
func (al *AgentLoop) scopeWithDescendants(assigneeAgentID string, rootIDs []string) []string {
	all := al.allSessionsForDescendantWalk(assigneeAgentID)
	seen := make(map[string]bool, len(rootIDs))
	var out []string
	for _, root := range rootIDs {
		if root == "" {
			continue
		}
		for _, id := range goalDescendantSessionIDs(all, root) {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
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

// --- Workspace diff evidence feed (G-3/G-15, FR-144; fix GX-E) -------------
//
// Operator correction (fix GX-E, 2026-09): THE WORKING TREE IS GROUND TRUTH.
// A commit is an audit/progress artifact, never a precondition for the Judge
// to see real evidence — the prior AttemptDiff(nil)-based design could
// report "(no workspace diff available)" purely because nothing had been
// committed yet, even with the worker's files sitting right there on disk
// (verified against a real operator run: the evidence repo's first commit
// landed 112s AFTER the verdict that complained no diff was available).
// resolveVerifierDiffText now reads gitevidence.Repo.DiffWorkingTree, which
// walks the LIVE filesystem directly and never depends on any commit having
// happened — correct from a completely unborn HEAD.

// verifierEvidenceScanner builds the MIN-5 secret guard EVERY gitevidence
// read on the Judge path must carry (review finding 6). Built per call rather
// than cached: audit.NewSecretScanner compiles a fixed pattern set (cheap,
// once per adjudication round) and reads the credential registry's replacer
// LIVE, so a credential registered or rotated since the last round is covered
// without a cache-invalidation path of our own.
//
// The judge path previously called gitevidence.Open(dir) with NO options at
// all, which left Repo.scanner and Repo.redact both nil — so a file whose
// content Commit had already refused to stage (its secret guard) was read
// straight off disk by DiffWorkingTree, classified as an "insert", rendered
// into the Judge's user message and sent to the external model on every
// round. That is the wider exposure of the two, and it was the unguarded one.
//
// Returns nil only when the scanner cannot be constructed at all (a corrupt
// compiled pattern set — logged at ERROR). WithSecretScanner(nil) is a
// documented no-op, so a nil here leaves the Repo unguarded and
// DiffWorkingTree's own fail-closed guard then refuses the read: the round
// degrades to "no workspace diff available", never to a leaked one.
func (al *AgentLoop) verifierEvidenceScanner() *audit.SecretScanner {
	var (
		scanner *audit.SecretScanner
		err     error
	)
	if cfg := al.GetConfig(); cfg != nil {
		// Passed as a concrete *strings.Replacer (never a nil one — see
		// config.Config.SensitiveDataReplacer, which always builds a real
		// replacer) so audit's registry layer is genuinely armed.
		scanner, err = audit.NewSecretScanner(cfg.SensitiveDataReplacer(), nil)
	} else {
		// No config reachable (a bare test harness): literal nil, NOT a
		// typed-nil *strings.Replacer — audit.SecretScanner.hasRegistry
		// treats a typed nil as "registry present" and would deref it. The
		// format-pattern layer (sk-…, ghp_…, JWTs, AWS keys) still applies.
		scanner, err = audit.NewSecretScanner(nil, nil)
	}
	if err != nil {
		logger.ErrorCF("agent",
			"verifier: could not build the secret scanner for workspace diff evidence — "+
				"the diff feed will fail closed (no diff evidence) rather than run unguarded",
			map[string]any{"error": err.Error()})
		return nil
	}
	return scanner
}

// verifierDiffBoundaryMu guards verifierDiffBoundaryHashMap.
var verifierDiffBoundaryMu sync.Mutex //nolint:gochecknoglobals // process-wide seam, see the doc comment below.

// verifierDiffBoundaryHashMap tracks, per adjudication unit (verifierUnitID),
// the commit hash as of the end of the last COMPLETED round — the "from"
// side of the NEXT round's cumulative working-tree diff (fix GX-E-3: a round
// with several intermediate commits — or none at all — must still show its
// FULL cumulative work, not just the single latest commit AttemptDiff(nil)
// used to report). Advanced only by advanceVerifierDiffBoundary (called from
// JudgeCriteria, judge.go, after a non-Unavailable runVerifierAdjudication),
// never by resolveVerifierDiffText itself — see that function's own doc
// comment for why a retried call must still see the same cumulative diff,
// not a spuriously-narrower one.
//
//nolint:gochecknoglobals // process-wide seam, see above.
var verifierDiffBoundaryHashMap = make(map[string]string)

// verifierDiffBoundary returns unitID's recorded boundary commit hash (""
// when never yet observed) and whether one has ever been recorded.
func verifierDiffBoundary(unitID string) (hash string, had bool) {
	verifierDiffBoundaryMu.Lock()
	defer verifierDiffBoundaryMu.Unlock()
	hash, had = verifierDiffBoundaryHashMap[unitID]
	return hash, had
}

// advanceVerifierDiffBoundary records headHash as unitID's new cumulative-
// diff start boundary, once a round has genuinely completed (fix GX-E-3). A
// no-op for an empty unitID. An empty headHash (workspace unreachable, or a
// real but still-unborn HEAD) is recorded too — "no commit yet" is itself a
// meaningful boundary state (distinct from "never observed", hadBoundary),
// and every SUBSEQUENT round's DiffWorkingTree call still walks the live
// working tree regardless of what this hash is, so recording "" here never
// hides uncommitted work.
func advanceVerifierDiffBoundary(unitID, headHash string) {
	if unitID == "" {
		return
	}
	verifierDiffBoundaryMu.Lock()
	defer verifierDiffBoundaryMu.Unlock()
	verifierDiffBoundaryHashMap[unitID] = headHash
}

// resolveVerifierDiffText returns the write-set-scoped, CUMULATIVE workspace
// diff text for the prose Judge's user message (the real file changes,
// committed or not — not a transcript window alone), and the commit hash
// HEAD resolved to at read time (headHash — "" when the repo is unreachable
// or HEAD is genuinely unborn). The caller (JudgeCriteria, judge.go) advances
// the per-unit diff boundary to headHash ONLY once the round genuinely
// completes; see advanceVerifierDiffBoundary's own doc comment for why this
// function itself must never do that (a retried call after an Unavailable
// judge round must still see the SAME cumulative diff).
//
// Best-effort like the pre-fix contract: an unbound goal (no WorkspaceID), no
// OMNIPUS_HOME, an invalid workspace id, or a nested user repo (MIN-6)
// returns diffText="" — the Judge degrades to machine evidence + transcript
// window + claim, never a hard failure. A gitevidence read error also
// degrades to "" (logged at WARN) — but, per the operator correction above,
// this is now the ONLY class of "" outcome: a workspace that is reachable
// but has NEVER been committed to no longer reads as unavailable —
// DiffWorkingTree("", nil) still walks the real working tree and reports
// whatever is actually on disk.
func (al *AgentLoop) resolveVerifierDiffText(in JudgeCriteriaInput) (diffText, headHash string) {
	wsID := strings.TrimSpace(in.WorkspaceID)
	if wsID == "" {
		return "", "" // unbound chat goal — no work-under-review workspace to diff
	}
	home := config.OmnipusHomeDir()
	if home == "" {
		return "", ""
	}
	dir, err := workspace.SafeWorkDir(home, wsID)
	if err != nil {
		return "", "" // invalid workspace id — not a diff-feed concern
	}
	// The MIN-5 secret guard is MANDATORY on this path (review finding 6):
	// DiffWorkingTree's output is rendered into the Judge's prompt and sent
	// to an external model. See verifierEvidenceScanner's doc comment.
	repo, err := gitevidence.Open(dir, gitevidence.WithSecretScanner(al.verifierEvidenceScanner()))
	if err != nil {
		// Nested user repo (ErrNestedRepo) or any other Open error: the git
		// layer degrades for this workspace (MIN-6). Logged at WARN inside
		// gitevidence.Open/EnsureWorkDir already; here it is a silent skip.
		return "", ""
	}
	head, err := repo.Head() // "" (nil error) is a legitimate unborn HEAD, not a failure
	if err != nil {
		logger.WarnCF("agent", "verifier: could not resolve workspace HEAD for diff evidence",
			map[string]any{"workspace_id": wsID, "error": err.Error()})
		return "", ""
	}

	boundary, hadBoundary := verifierDiffBoundary(verifierUnitID(in))
	fromHash := ""
	if hadBoundary {
		fromHash = boundary
	}
	// Deliberately NOT short-circuited when fromHash == head: HEAD not having
	// moved does not mean the working tree hasn't — uncommitted edits made
	// since the last round are exactly the case fix GX-E-3 exists to surface
	// (a commit is an audit artifact, never the evidence path).
	ev, err := repo.DiffWorkingTree(fromHash, nil)
	if err != nil {
		logger.WarnCF("agent", "verifier: could not read workspace diff evidence",
			map[string]any{"workspace_id": wsID, "error": err.Error()})
		return "", head
	}
	return renderDiffEvidence(ev), head
}

// resolveGoalScopedDiffEmpty is ADR-081 D6a/FR-014b's GOAL-SCOPED variant of
// resolveVerifierDiffText, used by the zero-adjudicable-output triple
// (goal_triggers.go's goalZeroOutputTripleHolds) to answer one question:
// "has THIS goal's own work changed anything in its workspace since we last
// looked?" — never resolveVerifierDiffText's own AttemptDiff(nil) ("the
// whole latest boundary commit"), which a co-tenant session sharing the SAME
// WorkspaceID can populate and so wrongly mask THIS goal's emptiness
// (round-2 B-4: two goals sharing one WorkspaceID, goal B produced nothing —
// B's diff term must still correctly read empty even though
// AttemptDiff(nil) at that moment would show goal A's commit).
//
// SCOPING MECHANISM (deviation from the ADR's literal "AttemptDiff takes a
// scope argument" phrasing, reported per the lane brief): AttemptDiff's own
// scope argument is a PATH write-set filter, and goal sessions — unlike
// plan-member tasks (task.AcceptanceCriterion has no path/write-set field of
// their own) — have no declared write-set to pass it. This function instead
// scopes by COMMIT RANGE: it tracks, per goal-id, the git HEAD hash observed
// the last time this function was called for that session
// (goalTriggerState.diffBoundaryHash, refreshed on every call), and diffs
// FROM that boundary TO the current HEAD via Repo.Diff(boundary, head, nil)
// — "this session's write set since this goal's own last look" read as a
// commit-range scope rather than a path scope. A co-tenant's commit that
// landed BEFORE this goal-id's own boundary was first captured is excluded
// by construction; a co-tenant's commit landing WITHIN the observation
// window is a residual, documented limitation (no per-path attribution
// exists for goal-scope commits today — see this function's own boundary
// field doc comment).
//
// Returns true ("diff term empty/zero") on every degenerate case: an unbound
// goal (workspaceID == "" — FR-014b: "the diff term degenerates away and the
// triple is deliberately a pair"), no repo, an unborn/inaccessible HEAD, the
// FIRST-EVER observation for this goal-id (nothing to compare against yet —
// the boundary is baselined to current HEAD and this call reports empty),
// or any gitevidence error (best-effort, mirroring resolveVerifierDiffText's
// own contract — never fail-closed into an unwarranted push denial).
func (al *AgentLoop) resolveGoalScopedDiffEmpty(sessionID, workspaceID string) bool {
	wsID := strings.TrimSpace(workspaceID)
	if wsID == "" {
		return true // unbound chat goal — diff term degenerates away (FR-014b)
	}
	home := config.OmnipusHomeDir()
	if home == "" {
		return true
	}
	dir, err := workspace.SafeWorkDir(home, wsID)
	if err != nil {
		return true // invalid workspace id — not a diff-feed concern
	}
	// Same mandatory guard as resolveVerifierDiffText above. This path only
	// COUNTS changed files (it never renders a patch), but the Repo it opens
	// must still be guarded: an unguarded Repo is one accidental
	// DiffWorkingTree call away from the leak finding 6 describes.
	repo, err := gitevidence.Open(dir, gitevidence.WithSecretScanner(al.verifierEvidenceScanner()))
	if err != nil {
		return true // nested user repo or any other Open error — degrade
	}
	head, err := repo.Head()
	if err != nil || head == "" {
		return true // unborn HEAD — nothing committed, trivially empty
	}

	gts := goalTriggers()
	gts.mu.Lock()
	boundary, hadBoundary := gts.diffBoundaryHash[sessionID]
	gts.diffBoundaryHash[sessionID] = head
	gts.mu.Unlock()

	if !hadBoundary {
		// First observation for this goal-id: baseline to current HEAD and
		// report empty — the OTHER two triple terms (evidence, transcript
		// output) still gate the free-push decision independently, so a
		// trivial baseline here cannot by itself trigger an unwarranted push.
		return true
	}
	if boundary == head {
		return true // nothing committed since the last look
	}
	ev, err := repo.Diff(boundary, head, nil)
	if err != nil {
		logger.WarnCF("agent", "goal trigger: could not read goal-scoped workspace diff",
			map[string]any{"session_id": sessionID, "workspace_id": wsID, "error": err.Error()})
		return true // best-effort degrade — never fail-closed
	}
	return ev == nil || len(ev.Files) == 0
}

// renderDiffEvidence formats a DiffEvidence as the concise text block the
// prose Judge consumes. ev == nil (the ONLY "" outcome, fix GX-E: the
// evidence layer itself could not be read — see resolveVerifierDiffText's own
// doc comment) → "" so buildJudgeUserContent's "(no workspace diff available
// for this adjudication)" sentinel fires. ev != nil but ev.Files empty (a
// real DiffWorkingTree call that found the working tree genuinely
// UNCHANGED against the boundary) renders an explicit "no changes found"
// sentence instead — never "", so it can never be confused with the
// evidence-layer-unavailable case above (operator correction point 4: "no
// changes were made" must not read as "evidence is unavailable").
//
// Two caps bound total prompt growth (fix GX-E-3: a CUMULATIVE working-tree
// diff can span far more files than the old single-commit AttemptDiff ever
// did): diffPatchCap (16 KiB) truncates any ONE file's patch text; once the
// running total across all files would exceed diffTotalCap (128 KiB),
// remaining files are still NAMED (so the Judge knows what else changed) but
// their patch bodies are omitted.
func renderDiffEvidence(ev *gitevidence.DiffEvidence) string {
	if ev == nil {
		return ""
	}
	var sb strings.Builder
	if len(ev.Files) == 0 {
		fmt.Fprintf(&sb,
			"(workspace diff evidence WAS collected — from %q to %q — and found NO file changes; "+
				"this means no work landed in the workspace since that boundary, not that evidence is unavailable)\n",
			ev.FromHash, ev.ToHash)
		return sb.String()
	}
	fmt.Fprintf(&sb, "(from %q to %q; %d of %d changed paths in scope)\n",
		ev.FromHash, ev.ToHash, ev.Matched, ev.Total)
	total := 0
	omitted := 0
	for _, f := range ev.Files {
		patch := f.Patch
		if len(patch) > diffPatchCap {
			patch = patch[:diffPatchCap] + "\n…[diff truncated]"
		}
		if total+len(patch) > diffTotalCap {
			omitted++
			fmt.Fprintf(&sb, "--- %s (%s) --- [patch omitted: cumulative diff exceeds %d bytes]\n", f.Path, f.Kind, diffTotalCap)
			continue
		}
		total += len(patch)
		fmt.Fprintf(&sb, "--- %s (%s) ---\n%s\n", f.Path, f.Kind, patch)
	}
	if omitted > 0 {
		fmt.Fprintf(&sb, "…[%d file(s) above had their patch body omitted for size]\n", omitted)
	}
	return sb.String()
}

// diffPatchCap bounds each file's diff text fed to the prose Judge, so one
// very large change cannot crowd out the rest of the evidence.
const diffPatchCap = 16 * 1024

// diffTotalCap bounds the TOTAL patch bytes fed to the prose Judge across
// every file in one adjudication (fix GX-E-3): a per-round CUMULATIVE diff
// can span far more files than the old single-commit AttemptDiff ever did,
// so the per-file cap alone no longer bounds total prompt growth.
const diffTotalCap = 128 * 1024

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
	// adjudicationID names THIS adjudication for the whole of its life: it is
	// stamped on the verifier turn's ctx (JUDGE-FR-084, so every audit entry
	// the Judge's tool calls produce carries the correlation) and reused as
	// the investigation-log id below, so "what did the verifier open" is
	// answerable by joining the audit log to the investigation log on one
	// value rather than on timestamps.
	adjudicationID := uuid.New().String()
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

	// Fix GX-E-4 (observability): the verifier attempt's wall-clock duration
	// and how many judge-unavailability retry iterations it consumed, in ONE
	// structured line — before this, the only way to infer duration was
	// subtracting the session's created_at from the judgeCallTimeout log
	// line, and iteration count was not logged at all. Runs as a defer (over
	// named returns, so it always observes the final `unavailable`/`reason`)
	// so it fires on EVERY exit path — success, fail-closed, or give-up
	// alike. No criterion/claim/user content, just timing and counters.
	startedAt := time.Now()
	iterations := 0
	defer func() {
		logger.InfoCF("agent", "verifier: adjudication attempt finished",
			map[string]any{
				"unit_id": unitID, "scope": in.Scope, "iterations": iterations,
				"duration_ms": time.Since(startedAt).Milliseconds(),
				"unavailable": unavailable,
			})
	}()

	// JUDGE-FR-057/FR-057a (review finding 5): god mode floors EVERY tool at
	// "allow" (Constraint #6's sandbox-off posture), which erases the
	// verifier's narrow read-only surface and makes FR-058's "mcp_*": deny
	// stamp resolve to nothing — resolveEffectivePolicyWith short-circuits on
	// cfg.GodMode before any per-agent map is consulted. An adjudication run
	// under that posture cannot be trusted, so it is refused BEFORE a verifier
	// session is created and BEFORE any Judge turn runs, exactly as FR-057
	// requires. Returned as unavailable (round NOT consumed) carrying the
	// machine-readable "god_mode: " reason, so the operator sees a distinct,
	// actionable state — not silence, and not a fail-closed unmet that would
	// burn the goal's rounds for a posture problem. Deliberately NOT retried
	// on the backoff schedule: god mode clears by operator action, not by
	// waiting, and a retry loop would only spin.
	if godModeReason, refuse := VerifierGodModeRefusalReason(GodModeActive(al.GetConfig())); refuse {
		logger.ErrorCF("agent",
			"verifier: adjudication refused — god mode is active (JUDGE-FR-057); "+
				"no verifier session was created and no Judge turn ran",
			map[string]any{"unit_id": unitID, "scope": in.Scope, "reason": godModeReason})
		return nil, "", "", true, godModeReason, nil
	}

	windowText := al.resolveVerifierWindowText(in)

	for attempt := 0; ; attempt++ {
		iterations = attempt + 1
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
		// JUDGE-FR-060/FR-060b (review finding 4): the two ctx seams whose own
		// doc comments say "the engine sets this where it sets
		// WithSystemAgentWorkspaceOverride" — and which nothing in production
		// set, so every Judge turn resolved ReadConfined=false and the
		// read-confinement ADR-084 exists to impose was never applied (a Judge
		// turn could read_file any other session's transcript.jsonl), while
		// the adjudication_id audit correlation was always empty. Set HERE,
		// at the dispatch, never by a tool.
		callCtx = tools.WithReadConfined(callCtx, true)
		callCtx = tools.WithVerifierAdjudicationID(callCtx, adjudicationID)
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
		content, flagged, callErr := al.dispatchVerifierTurn(callCtx, judgeInst, prompt, sessionKey, chatID)
		cancel()
		reportVerifierInjectionFlags(unitID, adjudicationID, flagged)

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
		// JUDGE-FR-067/FR-069a: one investigation-log id per adjudication —
		// shared by the structured log line below and every
		// noteAdjudicationReproducibility call this loop makes, so FR-069a's
		// "both investigation-log ids" names THIS adjudication and the
		// PREVIOUS one that produced the memoized outcome it is compared
		// against. It IS adjudicationID (minted once at the top of this
		// function, review finding 4) rather than a second uuid minted here,
		// so the audit entries the Judge's own tool calls carry
		// (WithVerifierAdjudicationID, JUDGE-FR-084) and this investigation
		// log join on one shared value.
		logID := adjudicationID
		for _, c := range proseCriteria {
			if pc, found := byID[c.ID]; found {
				v := verdictFromJudgeResponse(c.ID, pc)
				// E10: real grounding-based provenance refinement (JUDGE-
				// FR-065/FR-066) and the D-B-compliant grounding/attribution
				// REPORTS (FR-006, FR-007a, FR-014a, FR-028, FR-029,
				// FR-063a) — both layered around verdictFromJudgeResponse's
				// UNCHANGED body (verifier_provenance.go's own doc comment
				// explains why it could not be layered inside it), and both
				// strictly reporting: v.Met/v.Reason are never touched below
				// this point.
				v = refineVerdictProvenance(v, c.Text, diffText, evidence)
				reportGroundingIssues(c, pc, v)
				noteAdjudicationReproducibility(unitID, c.ID, v.Met, logID)
				out = append(out, v)
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
		// JUDGE-FR-067/FR-069 (D7): one structured investigation-log line
		// per adjudication — see this file's own doc comment and
		// verifier_provenance.go's for the reported FR-068 capture-source
		// scope boundary.
		emitInvestigationLog(
			unitID, logID, judgeInst.Model, judgeCallTimeout,
			al.buildInvestigationLogFromJudgeTranscript(judgeInst.ID, chatID),
		)
		return out, judgeInst.Model, judgeInst.ID, false, "", missing
	}
}

// --- the verifier turn's own dispatch (JUDGE-FR-030/FR-051/FR-052) ---------

// dispatchVerifierTurn runs ONE verifier turn under the Judge's identity and
// is the ONLY place JUDGE-FR-051's per-adjudication budget and JUDGE-FR-030's
// tool-result capture are bound to the turn that must honour them.
//
// WHY THIS EXISTS AT ALL, rather than the plain al.processTaskDirect call it
// replaces (review finding 5). Both registries are keyed by
// turnState.turnID — see verifier_budget.go's package doc comment for the
// (sound) reasons turnID rather than a context.Context value: the byte-cap
// consumer is admitToolResult, which is handed a *turnState and no ctx. But
// turnID is minted INSIDE runAgentLoop (al.newTurnEventScope, "<agentID>-turn
// -<seq>" off a process-wide atomic), so processTaskDirect's caller cannot
// know it, cannot predict it, and has nowhere to register anything. The
// result was that RegisterVerifierBudget and RegisterVerifierCapture had no
// production call site at all: verifierBudgetForTurn returned nil on every
// real adjudication, the tool-call/byte caps never fired, and the injection
// capture accumulated nothing. The existing tests passed because each one
// registered a budget itself.
//
// Owning the turnState here is what closes that: newTurnState mints the
// turnID, this function registers both handles against it, and only THEN is
// the turn run. No other mechanism inside this package's reach can bind them
// before the turn's first tool call.
//
// It is a FAITHFUL inlining of processTaskDirect + runAgentLoop for this one
// dispatch — the processOptions literal below is byte-for-byte the one
// processTaskDirect builds, so runTurn sees exactly the turn it saw before.
// The parts of runAgentLoop deliberately NOT reproduced, each because it is
// meaningless or actively wrong for a verifier turn:
//
//   - RecordLastChannel: would record the Judge's throwaway verifier session
//     as the agent's "last channel" for heartbeat notifications.
//   - checkGoalLoopAfterTurn: a fresh verifier session never carries a goal;
//     it was a no-op fast path here, and running the goal loop inside the
//     Judge's own turn is not a behaviour worth preserving by accident.
//   - follow-up publishing / PublishOutbound / the deferred goal-adjudication
//     dispatch: a verifier turn has SendResponse=false and must never enqueue
//     work or a second adjudication of its own.
//   - the lastTurnResult snapshot: test observability for ProcessMessage, and
//     runAgentLoop's own comment names itself the only writer.
//
// Returns the turn's final content, the adjudication-level injection flags
// captured during it (JUDGE-FR-009a — tool-call id -> matched pattern), and
// any turn error. A nil/empty flags map means nothing was flagged.
func (al *AgentLoop) dispatchVerifierTurn(
	ctx context.Context,
	judgeInst *AgentInstance,
	prompt, sessionKey, chatID string,
) (content string, flagged map[string]string, err error) {
	if hookErr := al.ensureHooksInitialized(ctx); hookErr != nil {
		return "", nil, fmt.Errorf("verifier turn: hooks: %w", hookErr)
	}
	if mcpErr := al.ensureMCPInitialized(ctx); mcpErr != nil {
		return "", nil, fmt.Errorf("verifier turn: mcp: %w", mcpErr)
	}

	// Tool context uses the "system" channel so the verifier's tools resolve
	// the same way processTaskDirect resolved them.
	turnCtx := tools.WithAgentID(ctx, judgeInst.ID)
	turnCtx = tools.WithToolContext(turnCtx, "system", "")
	delegationDepth := tools.ToolDelegationDepth(turnCtx)

	if chatID == "" {
		chatID = "task:" + sessionKey
	}

	// An operator who re-points the Judge at an external CLI runs no Omnipus
	// turn loop at all, so neither cap has a dispatch point to bind to. Fall
	// back to the shared external-CLI path rather than silently running that
	// agent on the native engine — and say so, so the missing caps are not
	// mistaken for caps that never fired.
	dispatchKind, dispatchErr := runner.ResolveDispatch(executorConfigOf(judgeInst))
	if dispatchErr != nil {
		return "", nil, fmt.Errorf("verifier turn: %w", dispatchErr)
	}
	if dispatchKind == runner.DispatchKindExternalCLI {
		logger.WarnCF("agent",
			"verifier: Judge is configured for an external CLI — the adjudication tool-call/byte caps "+
				"(JUDGE-FR-051) and the injection capture (JUDGE-FR-030) cannot be enforced for this turn",
			map[string]any{"judge_agent_id": judgeInst.ID})
		out, cliErr := al.processTaskDirect(ctx, judgeInst.ID, prompt, sessionKey, chatID)
		return out, nil, cliErr
	}

	opts := processOptions{
		SessionKey:             sessionKey,
		Channel:                "webchat",
		ChatID:                 chatID,
		SenderID:               "task-executor",
		UserMessage:            prompt,
		DefaultResponse:        defaultResponse,
		SendResponse:           false,
		TranscriptSessionID:    chatID,
		TranscriptStore:        al.GetAgentStore(judgeInst.ID),
		InitialDelegationDepth: delegationDepth,
		IsTaskRun:              true,
		WorkspaceID:            tools.ToolWorkspaceID(turnCtx),
	}

	ts := newTurnState(judgeInst, opts, al.newTurnEventScope(judgeInst.ID, sessionKey))
	if delegationDepth > 0 {
		ts.depth = delegationDepth
	}
	if opts.TranscriptSessionID != "" {
		resolverKey := "session:" + opts.TranscriptSessionID
		ts.activeAgentResolver = func() string {
			if v, ok := al.sessionActiveAgent.Load(resolverKey); ok {
				if id, ok := v.(string); ok && id != "" {
					return id
				}
			}
			return ""
		}
	}

	// JUDGE-FR-051/FR-052: the caps come from the OPERATOR's JudgeConfig via
	// its Effective* accessors — never a second copy of the default/clamp
	// logic — and are unregistered the moment this turn returns, whichever
	// way it returns.
	jcfg := al.judgeBudgetConfig()
	vb := NewVerifierBudget(
		jcfg.EffectiveToolCallCap(),
		jcfg.EffectiveByteCapBytes(),
		jcfg.EffectiveTokenCeiling(),
		jcfg.JudgeTokenCeilingWarnThreshold(),
	)
	RegisterVerifierBudget(ts.turnID, vb)
	defer UnregisterVerifierBudget(ts.turnID)

	// JUDGE-FR-030/FR-009a: the per-adjudication capture of every tool result
	// admitted to the model, carrying the injection-signature flags.
	vc := NewVerifierCapture()
	RegisterVerifierCapture(ts.turnID, vc)
	defer UnregisterVerifierCapture(ts.turnID)

	result, runErr := al.runTurn(turnCtx, ts)
	// Read the flags BEFORE the deferred Unregister — and on every exit path,
	// including a failed turn: an injection attempt that made the turn fail is
	// exactly the one worth reporting.
	flagged = vc.FlaggedToolCallIDs()
	// JUDGE-FR-082: a CANCELLED adjudication is discarded whole — it must
	// never be scored. A cancel (Stop, `/goal clear`, a plan Stop fan-out)
	// reaches this turn through RequestCancelForSession, which claims the
	// turnState and ends the turn with EMPTY content and, on most paths, NO
	// error — which the caller would otherwise read as "the verifier ran and
	// formed no judgment" and turn into a real criterion_unjudgeable verdict
	// against the work. Reporting it as a turn error instead routes it into
	// the caller's existing D7 unavailability branch: round not consumed, no
	// verdict, nothing recorded. ts.cancelFired is the authoritative flag
	// (turn.go's ClaimCancel sets it), not the end status, because a cancel
	// can land on several different terminal statuses.
	if ts.cancelFired.Load() {
		return "", flagged, fmt.Errorf(
			"verifier turn cancelled: the adjudication is discarded whole (JUDGE-FR-082)")
	}
	if runErr != nil {
		return "", flagged, runErr
	}
	if result.status == TurnEndStatusAborted {
		// Mirrors runAgentLoop: a user-initiated hard abort returns empty
		// content with no error (every system-initiated abort already
		// returned a non-nil error above). The caller treats empty content as
		// criterion_unjudgeable, which is the honest classification.
		return "", flagged, nil
	}
	return result.finalContent, flagged, nil
}

// judgeBudgetConfig resolves the operator's JudgeConfig, falling back to a
// zero JudgeConfig when no config is reachable (a bare test harness). The
// zero value is not a disabled budget: every Effective* accessor on it
// returns that field's SHIPPED default, so an unconfigured install still
// enforces FR-051's 25-call / 2 MiB caps.
func (al *AgentLoop) judgeBudgetConfig() config.JudgeConfig {
	if cfg := al.GetConfig(); cfg != nil {
		return cfg.Judge
	}
	return config.JudgeConfig{}
}

// reportVerifierInjectionFlags surfaces JUDGE-FR-009a's adjudication-level
// flag: the tool-call ids whose admitted result matched an injection
// signature, each with the pattern that matched. Reporting only (D-B): a
// flagged result is already banner-prefixed for the model by admitToolResult;
// this makes the adjudication-level fact visible to the operator, which it
// was not while the capture had no production registration at all.
func reportVerifierInjectionFlags(unitID, adjudicationID string, flagged map[string]string) {
	if len(flagged) == 0 {
		return
	}
	ids := make([]string, 0, len(flagged))
	for id := range flagged {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	patterns := make([]string, 0, len(ids))
	for _, id := range ids {
		patterns = append(patterns, flagged[id])
	}
	logger.WarnCF("agent",
		"verifier: injection signature(s) detected in tool results admitted during this adjudication (JUDGE-FR-009a)",
		map[string]any{
			"unit_id": unitID, "adjudication_id": adjudicationID,
			"flagged_tool_call_ids": ids, "patterns": patterns,
		})
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

// --- FR-070a: mapping a parsed judge response onto a persisted verdict -----

// maxCriterionEvidenceEntries mirrors CriterionVerdict.yaml's `evidence`
// array maxItems: 50 — enforced here (the same trust boundary
// maxEvidenceQuoteRunes enforces for a single quote, judge.go) so an
// over-long self-reported array from the model never reaches persistence.
const maxCriterionEvidenceEntries = 50

// deriveVerdictProvenance is E9's initial, schema-literal mapping from a
// verdict's VALIDATED EvidenceSource to its Provenance (JUDGE-FR-065 —
// CriterionVerdict.yaml's provenance description): deterministic_check
// when a check/veto decided it, judge_read/diff/transcript/session_read
// when the Judge's own reading decided it (source mapped 1:1, except
// file_read -> judge_read — there is no dedicated "file_read" Provenance
// value), none when neither applies (including an absent/invalid source).
//
// E9 wired this exact call site as the literal schema-only mapping
// TestDeriveVerdictProvenance_SchemaMapping (verifier_adjudication_test.go,
// E9's write-set — kept passing unmodified, hence the signature stays
// exactly one argument) pins forever: source alone, no additional context.
// "Real grounding-based derivation" (JUDGE-FR-065/FR-066/JUDGE-D7, E10) is
// therefore layered AROUND this call, not inside it —
// verifier_provenance.go's refineVerdictProvenance runs immediately after
// this literal mapping, in verdictFromJudgeResponse below, and MAY
// downgrade its result to task.ProvenanceNone when the reported target
// looks ungrounded. Still REPORTING-only (D-B): this changes what
// Provenance a verdict CARRIES, never whether it is Met.
func deriveVerdictProvenance(source task.VerdictEvidenceSource) task.VerdictProvenance {
	switch source {
	case task.EvidenceSourceMachineCheck:
		return task.ProvenanceDeterministic
	case task.EvidenceSourceFileRead:
		return task.ProvenanceJudgeRead
	case task.EvidenceSourceDiff:
		return task.ProvenanceDiffRead
	case task.EvidenceSourceTranscript:
		return task.ProvenanceTranscriptRead
	case task.EvidenceSourceSessionRead:
		return task.ProvenanceSessionRead
	default:
		return task.ProvenanceNone
	}
}

// verdictFromJudgeResponse maps one parsed judgeCriterionResponse onto its
// persisted task.CriterionVerdict (JUDGE-FR-070a, C-02's four new fields —
// EvidenceSource/EvidenceTarget/Provenance/Evidence, deliberately no
// Outcome field anywhere, D-H). ALL FOUR are OPTIONAL REPORTING fields
// (D-B): an absent, malformed, or non-verifying value here NEVER gates
// criterionID's met/unmet — that bool was already decided by pc.Met,
// straight off the Judge's own reasoning, before this function runs, and
// nothing below it ever re-decides it.
func verdictFromJudgeResponse(criterionID string, pc judgeCriterionResponse) task.CriterionVerdict {
	v := task.CriterionVerdict{
		CriterionID:   criterionID,
		Met:           pc.Met,
		Reason:        pc.Reason,
		EvidenceQuote: pc.EvidenceQuote,
	}
	// Self-reported by the model — validated against the closed enum
	// (never trusted blindly); an unrecognised value is treated as absent,
	// exactly like a legacy rubric that never emits one.
	if src := task.VerdictEvidenceSource(strings.TrimSpace(pc.EvidenceSource)); task.IsValidVerdictEvidenceSource(src) {
		v.EvidenceSource = src
	}
	v.EvidenceTarget = strings.TrimSpace(pc.EvidenceTarget)
	if len(pc.Evidence) > 0 {
		entries := pc.Evidence
		if len(entries) > maxCriterionEvidenceEntries {
			entries = entries[:maxCriterionEvidenceEntries]
		}
		v.Evidence = make([]task.CriterionEvidenceEntry, 0, len(entries))
		for _, e := range entries {
			v.Evidence = append(v.Evidence, task.CriterionEvidenceEntry{
				Part: e.Part, Source: e.Source, Target: e.Target, Quote: e.Quote,
			})
		}
		// FR-071: evidence_quote mirrors evidence[0].quote for readers that
		// don't know about the new array — keep the top-level field in sync
		// rather than trusting the model to have set both identically.
		v.EvidenceQuote = v.Evidence[0].Quote
	}
	v.Provenance = deriveVerdictProvenance(v.EvidenceSource)
	return v
}
