// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge.go implements the evidence-ladder judge (ADR-049 D2, spec Part B
// §B): per-criterion adjudication of a worker's completion CLAIM against
// real evidence. Machine-checkable criteria dispatch EXCLUSIVELY through the
// assignee agent's existing `bash` tool machinery (same tool registry,
// policy resolution, sandbox enforcement, and audit trail as any other bash
// call — D2 rule 1, FR-049); a parallel judge-owned exec path is forbidden.
// Prose criteria are collected into ONE no-tools structured LLM call under
// the seeded Judge System Agent's identity (coreagent.IDJudge), whose
// rubric is its AgentConfig.Rubric field (pkg/config/config.go).
//
// JudgeCriteria is the single reusable entrypoint for BOTH the task
// goal-loop (task_executor.go, this wave) and the Wave 2-B plan engine's
// plan-level judge (SD-B8: same seeded Judge, no second seeded agent) — see
// its doc comment for the exact call shape.
//
// Known gap (documented, not fabricated): FR-053/OBS-003 call for "workspace
// file diffs" as part of the judge's evidence ordering. No existing API for
// collecting a workspace's file diff was found reachable from pkg/agent's
// scope in this wave; the prose judge call proceeds with machine-check
// evidence + criteria + the worker's claim only. Flagged for a follow-up
// wave (see this backend-lead's final report).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// judgeCallTimeout bounds a single Judge LLM call. Distinct from
// config.PlanningConfig.CheckTimeoutSeconds, which bounds a machine-check
// COMMAND, not the judge's own structured call.
const judgeCallTimeout = 120 * time.Second

// judgeRetryBackoff is the cron-style transient backoff schedule (ADR D7,
// mirroring pkg/cron/service.go's defaultRetryBackoffMs: 60/120/300s) applied
// when the Judge LLM call itself is UNAVAILABLE (SEC-26 throttled or
// cost-capped, a provider error, a timeout, or the Judge System Agent not
// being resolvable at all) — never when it ran and produced no/invalid
// verdict (that is fail-closed unmet, NFR-2, and DOES consume the attempt).
// Retries beyond the last entry repeat at the last (longest) interval — the
// "normal cadence" the spec's Judge-unavailability dataset describes for the
// 4th+ occurrence. Package vars (not consts) so tests can substitute a
// zero-duration schedule and a recording judgeSleepFn without any real sleep
// (CLAUDE.md build discipline: no wall-clock sleeps in tests).
var judgeRetryBackoff = []time.Duration{60 * time.Second, 120 * time.Second, 300 * time.Second} //nolint:gochecknoglobals

// judgeSleepFn performs the backoff wait between judge-unavailable retries.
// Production uses sleepWithContext (loop.go); tests substitute a
// non-blocking recorder.
var judgeSleepFn = sleepWithContext //nolint:gochecknoglobals

// machineCheckOutputCap bounds how much of a machine check's ToolResult text
// is captured as evidence Output before EvidenceStore's own redact+truncate
// pipeline runs — defense in depth against a pathological tool result.
const machineCheckOutputCap = 256 * 1024

// exitCodeSuffixRe extracts the real process exit code that ExecTool's own
// foregroundResultFromSandbox (pkg/tools/shell.go) appends to ForLLM on a
// non-zero exit ("[Command exited with code N]"). tools.ToolResult carries
// no structured exit-code field (pkg/tools/result.go) — this is the only way
// to recover the real code without a parallel exec path (forbidden by D2
// rule 1) or a pkg/tools change (out of this wave's file scope).
var exitCodeSuffixRe = regexp.MustCompile(`\[Command exited with code (-?\d+)\]`)

// judgeTimedOutMarker matches ExecTool's own timeout message text
// ("Command timed out after Ns") so a machine check that hit its OWN
// timeout_seconds argument is recognized as TimedOut, not a generic failure.
const judgeTimedOutMarker = "command timed out after"

// --- Public entrypoint (Wave 2-B's plan engine also calls this) -----------

// JudgeCriteriaInput bundles everything the evidence-ladder judge needs to
// adjudicate one set of acceptance criteria for one attempt (task scope) or
// round (plan scope, Wave 2-B).
type JudgeCriteriaInput struct {
	// Scope is task.VerdictScopeTask or task.VerdictScopePlan.
	Scope string
	// TaskID/PlanID correlate the verdict (NFR-5) and, for TaskID, the
	// on-disk EvidenceStore path; set the one matching Scope.
	TaskID string
	PlanID string
	// AssigneeAgentID is whose bash-tool machinery runs machine checks (D2
	// rule 1) — for a task this is the task's own AgentID.
	AssigneeAgentID string
	// Criteria is the set to adjudicate. Never mutated.
	Criteria []task.AcceptanceCriterion
	// Attempt is the attempt/round index -> JudgeVerdict.Round (D7: "a round
	// = one worker turn plus its judge evaluation").
	Attempt int
	// ClaimText is the worker's own completion summary — placed LAST in the
	// prose judge's input ordering (OBS-003/FR-053): a claim, never a
	// verdict.
	ClaimText string
	// ExtraContext is optional additional framing prepended to the judge's
	// user-message content ahead of the evidence (e.g. a plan's goal/DoD
	// framing for Wave 2-B's plan-level round). Empty for a plain task.
	ExtraContext string
}

// JudgeCriteriaResult is the outcome of one JudgeCriteria call.
type JudgeCriteriaResult struct {
	// Verdict is set iff !Unavailable. A non-nil Verdict is ALWAYS a real
	// verdict — never synthesized as "met" on absence of evidence (NFR-2).
	Verdict *task.JudgeVerdict
	// Unavailable means the Judge LLM call could not be completed AND the
	// caller's ctx was canceled while JudgeCriteria was retrying with
	// backoff (D7) — the caller MUST NOT consume an attempt/round or record
	// a verdict for this outcome. JudgeCriteria itself retries forever on
	// judge-unavailability (bounded only by ctx), so Unavailable is only
	// ever observed when ctx was already canceled.
	Unavailable bool
	// Reason is a short, human-readable cause (unavailability cause, or a
	// summary of the produced verdict).
	Reason string
}

// JudgeCriteria is the SINGLE reusable evidence-ladder judge entrypoint
// (ADR-049 D2/D5, spec Part B §B, FR-049..057). Machine-checkable criteria
// dispatch through in.AssigneeAgentID's OWN registered `bash` tool via
// AgentInstance.Tools.ExecuteWithContext — the exact same registry/policy/
// sandbox/audit path every other bash call in the system uses (see
// runMachineCheck's doc comment). Prose criteria (if any) are collected into
// ONE no-tools structured call to the seeded Judge System Agent
// (coreagent.IDJudge), whose AgentConfig.Rubric is its system prompt.
//
// Unavailability (D7): if the Judge LLM call cannot be completed — SEC-26
// rate-limited, daily-cost-capped, a provider error, a timeout, or the Judge
// agent is not registered at all (e.g. a raw pkg/agent harness that never
// ran coreagent.SeedConfig) — JudgeCriteria retries internally on the
// cron-style backoff schedule (judgeRetryBackoff) FOREVER, respecting ctx
// cancellation, and returns Unavailable=true ONLY if ctx is canceled
// mid-backoff. Callers therefore see AT MOST ONE JudgeCriteria call per
// attempt/round; internal judge-unavailability retries never surface as a
// second, attempt-consuming call.
//
// If Criteria contains no prose criterion, the Judge LLM is never called at
// all — machine-only criteria adjudicate purely from real exit codes, and
// Unavailable is impossible in that case (FR-052's all-machine scenario).
func (al *AgentLoop) JudgeCriteria(ctx context.Context, in JudgeCriteriaInput) JudgeCriteriaResult {
	var machineCriteria, proseCriteria []task.AcceptanceCriterion
	for _, c := range in.Criteria {
		switch c.Kind {
		case task.KindCheck:
			machineCriteria = append(machineCriteria, c)
		case task.KindProse:
			proseCriteria = append(proseCriteria, c)
		}
	}

	perCriterion := make([]task.CriterionVerdict, 0, len(in.Criteria))
	var evidence []task.EvidenceRecord
	for _, c := range machineCriteria {
		v, ev := al.runMachineCheck(ctx, in.AssigneeAgentID, c, in.Attempt, in.TaskID)
		perCriterion = append(perCriterion, v)
		if ev != nil {
			evidence = append(evidence, *ev)
		}
	}

	var judgeModel, judgeAgentID string
	if len(proseCriteria) > 0 {
		proseVerdicts, model, jaID, unavailable, reason := al.judgeProseCriteria(
			ctx, proseCriteria, evidence, in.ClaimText, in.ExtraContext,
		)
		if unavailable {
			return JudgeCriteriaResult{Unavailable: true, Reason: reason}
		}
		perCriterion = append(perCriterion, proseVerdicts...)
		judgeModel, judgeAgentID = model, jaID
	}

	return al.finalizeVerdict(in, perCriterion, judgeModel, judgeAgentID)
}

// finalizeVerdict computes the overall PASS/FAIL from perCriterion
// (fail-closed: an empty perCriterion — no criteria adjudicated at all —
// never defaults to met, NFR-2) and builds the persisted/transcript
// JudgeVerdict.
func (al *AgentLoop) finalizeVerdict(
	in JudgeCriteriaInput,
	perCriterion []task.CriterionVerdict,
	judgeModel, judgeAgentID string,
) JudgeCriteriaResult {
	met := len(perCriterion) > 0
	for _, v := range perCriterion {
		if !v.Met {
			met = false
			break
		}
	}
	v := &task.JudgeVerdict{
		ID:           uuid.New().String(),
		Scope:        in.Scope,
		TaskID:       in.TaskID,
		PlanID:       in.PlanID,
		Round:        in.Attempt,
		Met:          met,
		PerCriterion: perCriterion,
		Model:        judgeModel,
		JudgedAt:     time.Now().UTC().Format(time.RFC3339),
		JudgeAgentID: judgeAgentID,
	}
	return JudgeCriteriaResult{Verdict: v, Reason: summarizeVerdict(v)}
}

func summarizeVerdict(v *task.JudgeVerdict) string {
	if v.Met {
		return "all criteria met"
	}
	var unmet []string
	for _, c := range v.PerCriterion {
		if !c.Met {
			unmet = append(unmet, c.CriterionID)
		}
	}
	if len(unmet) == 0 {
		return "no criteria were adjudicated (fail-closed, NFR-2)"
	}
	return "unmet criteria: " + strings.Join(unmet, ", ")
}

// --- Machine checks (D2 rule 1: dispatched exclusively via the assignee's
// own bash tool) ------------------------------------------------------------

// runMachineCheck dispatches ONE kind:check criterion through the assignee
// agent's OWN registered `bash` tool (D2 rule 1 — same tool registry, policy
// resolution, sandbox enforcement, and audit trail as any other bash call;
// there is no parallel judge-owned exec path). Policy resolution mirrors
// D2 rule 2: allow -> runs for real; ask -> resolved to deny, unattended (no
// interactive approver mid-loop); deny -> denied. A timeout (default 60s,
// config.PlanningConfig.CheckTimeoutSeconds) kills the check and fails it
// closed WITHOUT holding the caller's clock open (D2 rule 4/D7).
func (al *AgentLoop) runMachineCheck(
	ctx context.Context,
	assigneeAgentID string,
	c task.AcceptanceCriterion,
	attempt int,
	taskID string,
) (task.CriterionVerdict, *task.EvidenceRecord) {
	verdict := task.CriterionVerdict{CriterionID: c.ID}

	if c.Check == nil {
		verdict.Reason = "check criterion has no command (malformed; fail-closed)"
		return verdict, nil
	}

	agentInst, ok := al.GetRegistry().GetAgent(assigneeAgentID)
	if !ok || agentInst == nil || agentInst.Tools == nil {
		reason := fmt.Sprintf(
			"assignee agent %q not resolvable — check not executed (fail-closed)", assigneeAgentID,
		)
		verdict.Reason = reason
		return verdict, al.persistEvidence(taskID, c.ID, attempt, c.Check.Command, reason, -1, false, false)
	}

	policy := tools.EffectiveToolPolicy(agentInst.LoadToolPolicy(), tools.ScopeCore, agentInst.AgentType, "bash")
	if policy != string(config.ToolPolicyAllow) {
		reason := fmt.Sprintf(
			"bash policy is %q for agent %q — check not executed (fail-closed, ADR-049 D2 rule 2)",
			policy, assigneeAgentID,
		)
		verdict.Reason = reason
		return verdict, al.persistEvidence(taskID, c.ID, attempt, c.Check.Command, reason, -1, false, true)
	}

	cfg := al.GetConfig()
	timeoutSecs := config.DefaultCheckTimeoutSeconds
	if cfg != nil && cfg.Planning.CheckTimeoutSeconds >= 1 {
		timeoutSecs = cfg.Planning.CheckTimeoutSeconds
	}

	// Defense-in-depth: ExecTool's own timeout_seconds argument (below) is
	// what actually kills the sandboxed process; this outer context timeout
	// (with a small grace margin) guarantees THIS call never blocks longer
	// than that even if the tool somehow failed to honor its own timeout.
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs+5)*time.Second)
	defer cancel()
	callCtx = tools.WithAgentID(callCtx, assigneeAgentID)

	args := map[string]any{
		"action":          "run",
		"command":         c.Check.Command,
		"timeout_seconds": timeoutSecs,
	}
	// D2 rule 1 / FR-049: dispatched EXCLUSIVELY through the assignee's own
	// registered `bash` tool via the SAME ToolRegistry.ExecuteWithContext
	// path every other bash call in the system uses (registry lookup,
	// argument validation, policy-aware audit logging, panic recovery) — no
	// parallel judge-owned exec path.
	result := agentInst.Tools.ExecuteWithContext(callCtx, "bash", args, "system", "", nil)

	timedOut, exitCode, output := interpretBashResult(result)
	if len(output) > machineCheckOutputCap {
		output = output[:machineCheckOutputCap]
	}
	ev := al.persistEvidence(taskID, c.ID, attempt, c.Check.Command, output, exitCode, timedOut, false)

	if timedOut {
		verdict.Reason = fmt.Sprintf("check timed out after %ds (fail-closed, D2 rule 4)", timeoutSecs)
		return verdict, ev
	}
	verdict.Met = exitCode == c.Check.ExpectedExitCode
	if verdict.Met {
		verdict.Reason = fmt.Sprintf("exit code %d matched expected %d", exitCode, c.Check.ExpectedExitCode)
	} else {
		verdict.Reason = fmt.Sprintf("exit code %d did not match expected %d", exitCode, c.Check.ExpectedExitCode)
	}
	return verdict, ev
}

// interpretBashResult recovers the real exit code and timeout status from a
// bash-tool ToolResult (see exitCodeSuffixRe's doc comment for why this
// regex-based recovery is necessary rather than a structured field). A
// result that IsError but carries neither the timeout marker nor a
// parseable exit-code suffix means the command was blocked before it ever
// ran (hardcoded deny-pattern guard, path-escape guard, or a sandbox setup
// failure) — this is reported as the -1 sentinel, which can never equal a
// criterion's declared 0..255 expected code, so it always fails closed.
func interpretBashResult(result *tools.ToolResult) (timedOut bool, exitCode int, output string) {
	if result == nil {
		return false, -1, "bash tool returned a nil result"
	}
	output = result.ForLLM
	if output == "" && result.Err != nil {
		output = result.Err.Error()
	}
	if strings.Contains(strings.ToLower(output), judgeTimedOutMarker) {
		return true, -1, output
	}
	if m := exitCodeSuffixRe.FindStringSubmatch(output); m != nil {
		if code, err := strconv.Atoi(m[1]); err == nil {
			return false, code, output
		}
	}
	if !result.IsError {
		return false, 0, output
	}
	return false, -1, output
}

// persistEvidence writes an EvidenceRecord via the redacting EvidenceStore.
// Returns nil (no on-disk record) when taskID is empty — a plan-scope round
// (Wave 2-B) has no task to correlate evidence under in this wave; the
// verdict itself is still computed correctly in-memory regardless.
func (al *AgentLoop) persistEvidence(
	taskID, criterionID string,
	attempt int,
	command, output string,
	exitCode int,
	timedOut, policyDenied bool,
) *task.EvidenceRecord {
	if taskID == "" {
		return nil
	}
	es := al.evidenceStore()
	rec, err := es.Record(taskID, criterionID, attempt, command, output, exitCode, timedOut, policyDenied)
	if err != nil {
		logger.WarnCF("agent", "judge: failed to persist machine-check evidence",
			map[string]any{"task_id": taskID, "criterion_id": criterionID, "attempt": attempt, "error": err.Error()})
		return nil
	}
	return rec
}

// evidenceStore builds the redacting EvidenceStore on demand. Constructing
// one is cheap (no I/O — only Record/List/DeleteTaskEvidence touch disk), so
// there is no need to cache it on AgentLoop; this keeps loop.go's
// constructor/struct untouched. redact is resolved lazily per call so a
// config reload's newly-registered sensitive values are always honored.
func (al *AgentLoop) evidenceStore() *task.EvidenceStore {
	redact := func(s string) string {
		if cfg := al.GetConfig(); cfg != nil {
			return cfg.FilterSensitiveData(s)
		}
		return s
	}
	return task.NewEvidenceStore(config.OmnipusHomeDir(), redact)
}

// --- Prose judge (single no-tools structured LLM call) ---------------------

// judgeProseCriteria collects ALL prose criteria into ONE no-tools
// structured call to the seeded Judge System Agent (FR-053). Input ordering
// is machine-check evidence + criteria FIRST, the worker's own claim LAST
// (OBS-003) — the rubric (AgentConfig.Rubric) instructs the model that
// unevidenced claims score unmet. Retries forever on unavailability
// (judgeBackoffWait), bounded only by ctx cancellation.
func (al *AgentLoop) judgeProseCriteria(
	ctx context.Context,
	proseCriteria []task.AcceptanceCriterion,
	evidence []task.EvidenceRecord,
	claimText, extraContext string,
) (verdicts []task.CriterionVerdict, model, judgeAgentID string, unavailable bool, reason string) {
	userContent, buildErr := buildJudgeUserContent(proseCriteria, evidence, claimText, extraContext)
	if buildErr != nil {
		return failClosedProseVerdicts(proseCriteria, "internal error building judge prompt: "+buildErr.Error()),
			"", "", false, "build_error"
	}
	rubric := judgeRubricFromConfig(al.GetConfig())
	messages := make([]providers.Message, 0, 2)
	if rubric != "" {
		messages = append(messages, providers.Message{Role: "system", Content: rubric})
	}
	messages = append(messages, providers.Message{Role: "user", Content: userContent})

	registry := al.GetRegistry()
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return nil, "", "", true, ctx.Err().Error()
		}

		judgeInst, ok := registry.GetAgent(string(coreagent.IDJudge))
		if !ok || judgeInst == nil || judgeInst.Provider == nil {
			const notConfiguredReason = "judge_not_configured: Judge System Agent is not registered"
			logger.WarnCF("agent", "judge: Judge System Agent not resolvable; pausing (D7 unavailability)", nil)
			if waitErr := al.judgeBackoffWait(ctx, attempt, notConfiguredReason); waitErr != nil {
				return nil, "", "", true, notConfiguredReason
			}
			continue
		}

		allowed, retryAfter, denyReason := al.checkJudgeSEC26(judgeInst.AgentType, judgeInst.ID)
		if !allowed {
			logger.WarnCF("agent", "judge: SEC-26 gate denied judge LLM call; pausing (D7 unavailability)",
				map[string]any{"reason": denyReason, "retry_after_s": retryAfter.Seconds()})
			if waitErr := al.judgeBackoffWait(ctx, attempt, denyReason); waitErr != nil {
				return nil, "", "", true, denyReason
			}
			continue
		}

		callCtx, cancel := context.WithTimeout(ctx, judgeCallTimeout)
		resp, callErr := judgeInst.Provider.Chat(callCtx, messages, nil, judgeInst.Model, map[string]any{
			"max_tokens":       2048,
			"temperature":      0.0,
			"prompt_cache_key": judgeInst.ID,
		})
		cancel()
		if callErr != nil {
			logger.WarnCF("agent", "judge: LLM call failed; pausing (D7 unavailability)",
				map[string]any{"error": callErr.Error()})
			if waitErr := al.judgeBackoffWait(ctx, attempt, callErr.Error()); waitErr != nil {
				return nil, "", "", true, callErr.Error()
			}
			continue
		}

		if al.rateLimiter != nil && resp != nil && resp.Usage != nil {
			al.rateLimiter.RecordSpend(estimateLLMCallCost(judgeInst.Model, resp.Usage), judgeInst.AgentType)
		}

		if resp == nil || strings.TrimSpace(resp.Content) == "" {
			// Ran but produced nothing -> fail-closed unmet (NFR-2), NOT
			// unavailable — the call itself succeeded.
			return failClosedProseVerdicts(proseCriteria, "judge returned an empty response"),
				judgeInst.Model, judgeInst.ID, false, ""
		}

		parsed, parseErr := parseJudgeResponse(resp.Content)
		if parseErr != nil {
			return failClosedProseVerdicts(
				proseCriteria, "judge response could not be parsed as valid JSON: "+parseErr.Error(),
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
					Reason: "judge did not return a verdict for this criterion (fail-closed, NFR-2)",
				})
			}
		}
		return out, judgeInst.Model, judgeInst.ID, false, ""
	}
}

// checkJudgeSEC26 applies the SAME SEC-26 per-agent LLM rate limit + daily
// cost cap gates the normal turn loop applies (loop.go's turnLoop, lines
// ~6289/~6321), for the Judge's own out-of-turn call. Privileged agents
// (core-only, ADR-049 D3) are exempt, but the Judge is type "system" — never
// privileged — so this always applies in a real install.
func (al *AgentLoop) checkJudgeSEC26(agentType, agentID string) (allowed bool, retryAfter time.Duration, reason string) {
	cfg := al.GetConfig()
	if al.rateLimiter == nil || cfg == nil || security.IsPrivilegedAgent(agentType) {
		return true, 0, ""
	}
	if cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour > 0 {
		window := al.rateLimiter.GetOrCreate(
			"agent:"+agentID+":llm_call",
			cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour,
			time.Hour,
			security.ScopeAgent,
			agentID,
			"llm_call",
		)
		if result := window.Allow(); !result.Allowed {
			return false, time.Duration(result.RetryAfterSeconds * float64(time.Second)),
				"sec26_rate_limited: " + result.PolicyRule
		}
	}
	if cfg.Sandbox.RateLimits.DailyCostCapUSD > 0 {
		if al.rateLimiter.GetDailyCost() >= cfg.Sandbox.RateLimits.DailyCostCapUSD {
			return false, 0, fmt.Sprintf(
				"sec26_cost_capped: daily cost cap $%.2f reached", cfg.Sandbox.RateLimits.DailyCostCapUSD,
			)
		}
	}
	return true, 0, ""
}

// judgeBackoffWait sleeps on the cron-style backoff schedule (judgeRetryBackoff),
// clamping to the last (longest) interval for any attemptIdx beyond the
// table — the "normal cadence" the spec's Judge-unavailability dataset
// describes for the 4th+ occurrence. Returns a non-nil error (ctx canceled)
// when the caller should give up.
func (al *AgentLoop) judgeBackoffWait(ctx context.Context, attemptIdx int, reason string) error {
	idx := attemptIdx
	if idx >= len(judgeRetryBackoff) {
		idx = len(judgeRetryBackoff) - 1
	}
	d := judgeRetryBackoff[idx]
	logger.WarnCF("agent", "judge: unavailable, backing off before retry",
		map[string]any{"reason": reason, "backoff_ms": d.Milliseconds()})
	return judgeSleepFn(ctx, d)
}

// judgeRubricFromConfig reads the Judge System Agent's editable Rubric field
// (its system prompt) from cfg.Agents.List. Returns "" when the Judge is not
// present in the config at all (AgentInstance construction already handles
// the AgentConfig.Model/Provider side generically — see NewAgentInstance;
// the rubric-as-system-prompt wiring is this wave's own addition since no
// prior wave read AgentConfig.Rubric anywhere).
func judgeRubricFromConfig(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(coreagent.IDJudge) {
			return cfg.Agents.List[i].Rubric
		}
	}
	return ""
}

// buildJudgeUserContent assembles the judge's user-message content: optional
// extra framing, THEN machine-check evidence (real, unfakeable), THEN the
// prose criteria to judge, THEN the worker's own claim LAST (OBS-003/FR-053
// input ordering).
func buildJudgeUserContent(
	criteria []task.AcceptanceCriterion,
	evidence []task.EvidenceRecord,
	claimText, extraContext string,
) (string, error) {
	var sb strings.Builder
	if extraContext != "" {
		sb.WriteString(extraContext)
		sb.WriteString("\n\n")
	}
	sb.WriteString("## Machine-check evidence (real, unfakeable — ordered first)\n")
	if len(evidence) == 0 {
		sb.WriteString("(no machine-check evidence on this attempt)\n\n")
	} else {
		evJSON, err := json.MarshalIndent(evidence, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal evidence: %w", err)
		}
		sb.Write(evJSON)
		sb.WriteString("\n\n")
	}
	sb.WriteString(
		"NOTE: workspace file diffs are not collected by this runtime (documented gap) — " +
			"judge only against the evidence above and the criteria text.\n\n",
	)
	sb.WriteString("## Prose criteria to judge (return exactly one entry per id)\n")
	type criterionForPrompt struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	forPrompt := make([]criterionForPrompt, 0, len(criteria))
	for _, c := range criteria {
		forPrompt = append(forPrompt, criterionForPrompt{ID: c.ID, Text: c.Text})
	}
	critJSON, err := json.MarshalIndent(forPrompt, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal criteria: %w", err)
	}
	sb.Write(critJSON)
	sb.WriteString("\n\n")
	sb.WriteString(
		"## Worker's own completion claim (LAST — a CLAIM, not a verdict; " +
			"verify it against the evidence above, never the other way around)\n",
	)
	if strings.TrimSpace(claimText) == "" {
		sb.WriteString("(the worker reported no summary text)\n")
	} else {
		sb.WriteString(claimText)
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func failClosedProseVerdicts(criteria []task.AcceptanceCriterion, reason string) []task.CriterionVerdict {
	out := make([]task.CriterionVerdict, 0, len(criteria))
	for _, c := range criteria {
		out = append(out, task.CriterionVerdict{CriterionID: c.ID, Met: false, Reason: reason})
	}
	return out
}

// judgeCriterionResponse is one entry of the judge's declared JSON contract
// (judgeDefaultRubric, pkg/coreagent/core.go): {"id","met","reason"}.
type judgeCriterionResponse struct {
	ID     string `json:"id"`
	Met    bool   `json:"met"`
	Reason string `json:"reason"`
}

// judgeLLMResponse is the judge's full declared JSON contract:
// {"met":bool,"criteria":[...],"summary":"..."}.
type judgeLLMResponse struct {
	Met      bool                     `json:"met"`
	Criteria []judgeCriterionResponse `json:"criteria"`
	Summary  string                   `json:"summary"`
}

// judgeCodeFenceRe strips an optional Markdown code-fence wrapper some LLMs
// add around JSON output (mirrors evals/judge/scorer.go's identical helper —
// duplicated rather than imported since evals/ is an offline eval harness,
// not a runtime dependency of pkg/agent).
var judgeCodeFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// extractJudgeJSON returns the first balanced JSON object in s, preferring a
// fenced code block if present.
func extractJudgeJSON(s string) (string, error) {
	if m := judgeCodeFenceRe.FindStringSubmatch(s); len(m) == 2 {
		return strings.TrimSpace(m[1]), nil
	}
	start := strings.Index(s, "{")
	if start == -1 {
		return "", fmt.Errorf("judge response contains no JSON object")
	}
	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			if inStr {
				escaped = true
			}
		case '"':
			inStr = !inStr
		case '{':
			if !inStr {
				depth++
			}
		case '}':
			if !inStr {
				depth--
				if depth == 0 {
					return s[start : i+1], nil
				}
			}
		}
	}
	return "", fmt.Errorf("judge response contains an unclosed JSON object")
}

func parseJudgeResponse(raw string) (judgeLLMResponse, error) {
	jsonStr, err := extractJudgeJSON(raw)
	if err != nil {
		return judgeLLMResponse{}, err
	}
	var out judgeLLMResponse
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return judgeLLMResponse{}, fmt.Errorf("unmarshal judge JSON: %w", err)
	}
	return out, nil
}

// --- Soft-tier criterion fallback (ADR-049 D5) ------------------------------

// softTierCriterionID is the deterministic (non-UUID) ID used for the
// synthesized soft-tier criterion. Stable across attempts/retries so the
// judge's echoed "id" in its JSON response always maps back correctly.
const softTierCriterionID = "soft-tier-implicit"

// SoftTierCriterion synthesizes the implicit prose criterion used when a
// task/plan has no explicit acceptance criteria (ADR-049 D5 soft tier):
// judge against the Prompt when present, else title+description. Returns
// nil when title, description, AND prompt are all empty — a structurally
// empty unit with nothing to judge at all.
func SoftTierCriterion(title, description, prompt string) *task.AcceptanceCriterion {
	text := strings.TrimSpace(prompt)
	if text == "" {
		text = strings.TrimSpace(title)
		description = strings.TrimSpace(description)
		if description != "" {
			if text != "" {
				text += ": "
			}
			text += description
		}
	}
	if text == "" {
		return nil
	}
	const maxCriterionTextRunes = 1000
	runes := []rune(text)
	if len(runes) > maxCriterionTextRunes {
		text = string(runes[:maxCriterionTextRunes])
	}
	return &task.AcceptanceCriterion{
		ID:     softTierCriterionID,
		Kind:   task.KindProse,
		Text:   text,
		Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "system"},
		Status: task.CritPending,
	}
}
