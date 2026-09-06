// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_compile.go implements the ADR-053 Phase-2 SMART goal compiler, the
// compile-time feasibility gate, the echo-&-confirm / amendment surface, and
// the non-verdict classifier (§1/§6, FR-107/FR-110/FR-111/FR-113/FR-114/
// FR-115/FR-116/FR-137/FR-138, acceptance G-7/G-8/G-14, decisions D9/D11/D12
// + folded findings N-6/N-12).
//
// The compiler is ENGINE-INVOKED, not a skill (ADR-053 §4.5/BOM): at /goal set
// it interprets user intent into a goal record (prompt + goal definition +
// acceptance criteria) via a schema-validated compilation turn. It produces the
// criteria ladder — behavior kind for countables ("5 searches" →
// tool:search_web min_count:5), the existing KindCheck for deterministic
// machine checks ("all tests pass" → command + expected exit code), and prose
// for the subjective remainder — reusing the SAME task.AcceptanceCriterion type
// tasks/plans use (DoD-11/FR-198: never a second criteria type).
//
// The co-located marker parser lives in THIS file (the B2/Gate-2 lesson: a
// parser separated from its teaching fragment drifts; co-location keeps the
// marker syntax and its docs one mutation). The compile-time feasibility gate
// (D9) is the ONLY net for unverifiable criteria — D9 deleted the runtime
// criterion_unverifiable verdict, so the gate must reject, fail-closed and
// immutable, anything the runtime cannot verify. The runtime false-accept
// fallback (R§8.1) is classifyNonVerdict + the escalate-once helpers below.
package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// UnableToVerifyMaxRerunsDefault is the K bound (R§8.1/m-4/FR-116): the max
// number of CONSECUTIVE unable_to_verify results on the SAME check before it
// escalates to the owner as a persistently-blocked check (so a permanently-
// blocked check cannot loop the adjudication forever). Config-keyed at the
// verifier/judge subsystem (FR-195 notes it is NOT part of session_messaging);
// this default applies when no override is configured.
const UnableToVerifyMaxRerunsDefault = 3

// FailedReasonJudgeRoundsExhausted is the honest terminal when a goal ends
// without a MET verdict after its round budget is spent (R§8.1/FR-138) —
// including the owner-inert case after a criterion_unjudgeable escalation.
const FailedReasonJudgeRoundsExhausted = "judge_rounds_exhausted"

// --- FeasibilityContext (the goal-bearing agent's own policy + sandbox) -----

// FeasibilityContext is the goal-bearing agent's own verification surface,
// consumed by the compile-time feasibility gate (FR-111/FR-112). It is an
// interface so the compiler stays decoupled from the tool/policy packages and
// is unit-testable with a fake. The concrete adapter (agentFeasibilityContext
// in goal_loop.go) reads the agent's OWN LoadToolPolicy — compiled checks run
// under the goal-bearing agent's OWN tool policy + kernel sandbox, NEVER a
// privileged bypass (MAJ-13/FR-112).
type FeasibilityContext interface {
	// EffectiveToolPolicy returns "allow"/"ask"/"deny" for toolName under the
	// goal-bearing agent's own policy + sandbox. A behavior criterion whose
	// tool resolves to "deny" is UNREACHABLE and rejected at compile (FR-111).
	EffectiveToolPolicy(toolName string) string
	// BashReachable reports whether the bash tool — which runs a KindCheck
	// criterion's command per CriterionCheck's contract — is policy-reachable.
	// A machine check whose runner is unreachable is rejected at compile.
	BashReachable() bool
}

// --- CompiledGoal (the S1 unified goal/criteria record) --------------------

// CompiledGoal is the engine-invoked compiler's output (FR-110): the worker
// prompt plus the criteria ladder. It IS the S1 unified goal/criteria record —
// the criteria slice reuses task.AcceptanceCriterion verbatim (DoD-11), never
// a parallel criteria type. Persisted as GoalCriteriaJSON on the session meta.
type CompiledGoal struct {
	// Intent is the raw user intent text (/goal <intent>), retained verbatim so
	// an amendment diff (N-6) and the echo (FR-113) can quote it.
	Intent string `json:"intent"`
	// Prompt is the steering prompt fed to the worker turn (the prose remainder
	// of the intent after marker extraction — the subjective goal statement).
	Prompt string `json:"prompt"`
	// Definition is ADR-080 D-STATEMENT's compiled SMART restatement of the
	// goal — one clear sentence, distinct from Prompt (the steering remainder)
	// — rendered before the criteria in every echo surface. Populated by the
	// compile-response parser (parseGoalCompileResponse); zero-value on the
	// deterministic/marker paths, where formatGoalEcho's Prompt/Intent
	// fallback applies.
	Definition string `json:"definition,omitempty"`
	// Criteria is the compiled criteria ladder: behavior (countables), check
	// (deterministic machine), prose (subjective). Schema-validated via
	// task.NormalizeCriteria before this struct is returned.
	Criteria []task.AcceptanceCriterion `json:"criteria"`
	// DoD is ADR-080 D-DOD's Definition of Done — generic standing quality
	// gates, DISTINCT from Criteria's outcome-specific checks, evaluated
	// together (Criteria UNION DoD, the judged-set union in
	// compiledGoalCriteriaFor). Every
	// loaded goal carries at least one item — see dodFloorConstructor /
	// loadCompiledGoal's legacy-goal backfill below, which guarantees this
	// in-memory invariant even for a pre-ADR-080 persisted goal with no DoD.
	DoD []task.AcceptanceCriterion `json:"dod,omitempty"`
}

// FeasibilityRejection is a compile-time gate failure (FR-111/D9): the
// criterion the runtime cannot verify, fail-closed and immutable. CriterionIndex
// is -1 for a whole-goal rejection (e.g. zero verifiable criteria produced).
type FeasibilityRejection struct {
	CriterionIndex int    `json:"criterion_index"`
	Reason         string `json:"reason"`
}

// CompileResult is what compileGoalIntent returns: either a compiled Goal (the
// gate passed) or a Rejection (the gate failed — no criterion persists). The
// two are mutually exclusive; exactly one is non-nil/non-empty.
type CompileResult struct {
	Goal      *CompiledGoal
	Rejection *FeasibilityRejection
}

// --- Co-located marker parser (B2/Gate-2: parser + teaching fragment) -------
//
// The compiler accepts free-form intent AND optional inline markers that pin a
// criterion to a concrete, deterministic shape. Markers are bracketed and
// intentionally terse so a user/agent can express the verifiable skeleton
// without abandoning natural language for the subjective remainder. Supported:
//
//	[behavior: <tool> min:N max:M]   → KindBehavior (max optional)
//	[search: N]                      → KindBehavior tool=search_web min:N
//	[check: <shell command> exit:N]  → KindCheck (exit defaults to 0)
//	[tests] / [tests pass]           → KindCheck `go test ./...` exit:0
//
// Anything outside a marker is the prose remainder (the subjective goal
// statement → a KindProse criterion, or folded into the worker prompt when it
// is pure steering). Markers are extracted left-to-right; the prose remainder
// keeps its original inter-marker text, trimmed and collapsed.

var (
	// behaviorMarkerRe matches [behavior: tool min:N max:M] (max clause optional).
	behaviorMarkerRe = regexp.MustCompile(`(?i)\[behavior:\s*([a-zA-Z0-9_.-]+)\s+(?:min:(\d+))?\s*(?:max:(\d+))?\]`)
	// searchMarkerRe matches [search: N] — shorthand for search_web behavior.
	searchMarkerRe = regexp.MustCompile(`(?i)\[search:\s*(\d+)\]`)
	// checkMarkerRe matches [check: <cmd> exit:N] (exit clause optional, default 0).
	checkMarkerRe = regexp.MustCompile(`(?is)\[check:\s*(.+?)\s+(?:exit:(\d+))?\]`)
	// testsMarkerRe matches [tests] or [tests pass] — shorthand for go test.
	testsMarkerRe = regexp.MustCompile(`(?i)\[tests(?:\s+pass)?\]`)
)

// markerHit records the byte span of a matched marker so parseIntentMarkers can
// excise it from the prose remainder.
type markerHit struct {
	start int
	end   int
}

// parseIntentMarkers is the co-located marker parser (FR-110's "co-located
// parser"). It extracts the inline markers above into criteria and returns the
// prose remainder. authorID/authorKind tag every produced criterion (the
// compiler authors on the user's behalf at /goal set, so author=user). The
// returned criteria are NOT yet shape-validated — compileGoalIntent runs
// task.NormalizeCriteria over the full ladder.
func parseIntentMarkers(intent string, authorID string) ([]task.AcceptanceCriterion, string, []markerHit) {
	// Upper bound on marker matches: every marker has exactly one opening '['.
	hits := make([]markerHit, 0, strings.Count(intent, "["))
	var (
		criteria []task.AcceptanceCriterion
		author   = task.CriterionAuthor{Kind: task.AuthorKindUser, ID: authorID}
	)

	addBehavior := func(tool string, minVal, maxVal *int, text string) {
		criteria = append(criteria, task.AcceptanceCriterion{
			Kind: task.KindBehavior, Text: text,
			Behavior: &task.CriterionBehavior{Tool: tool, MinCount: minVal, MaxCount: maxVal},
			Author:   author,
		})
	}
	addCheck := func(cmd string, exit int, text string) {
		criteria = append(criteria, task.AcceptanceCriterion{
			Kind: task.KindCheck, Text: text,
			Check:  &task.CriterionCheck{Command: cmd, ExpectedExitCode: exit},
			Author: author,
		})
	}

	// [behavior: tool min:N max:M]
	for _, m := range behaviorMarkerRe.FindAllStringSubmatchIndex(intent, -1) {
		s, e := m[0], m[1]
		tool := intent[m[2]:m[3]]
		var minVal, maxVal *int
		if m[4] >= 0 {
			v, _ := strconv.Atoi(intent[m[4]:m[5]])
			minVal = &v
		}
		if m[6] >= 0 {
			v, _ := strconv.Atoi(intent[m[6]:m[7]])
			maxVal = &v
		}
		addBehavior(tool, minVal, maxVal, fmt.Sprintf("call %s (min %d)", tool, orDefault(minVal, 1)))
		hits = append(hits, markerHit{s, e})
	}
	// [search: N]
	for _, m := range searchMarkerRe.FindAllStringSubmatchIndex(intent, -1) {
		s, e := m[0], m[1]
		n, _ := strconv.Atoi(intent[m[2]:m[3]])
		nn := n
		addBehavior("search_web", &nn, nil, fmt.Sprintf("perform %d web search(es)", n))
		hits = append(hits, markerHit{s, e})
	}
	// [check: cmd exit:N]
	for _, m := range checkMarkerRe.FindAllStringSubmatchIndex(intent, -1) {
		s, e := m[0], m[1]
		cmd := strings.TrimSpace(intent[m[2]:m[3]])
		// strip a trailing exit: token if the lazy split swallowed it into cmd
		cmd = strings.TrimSuffix(strings.TrimSpace(cmd), "]")
		exit := 0
		if m[4] >= 0 {
			exit, _ = strconv.Atoi(intent[m[4]:m[5]])
		}
		addCheck(cmd, exit, fmt.Sprintf("command exits %d: %s", exit, cmd))
		hits = append(hits, markerHit{s, e})
	}
	// [tests] / [tests pass]
	for _, m := range testsMarkerRe.FindAllStringSubmatchIndex(intent, -1) {
		s, e := m[0], m[1]
		addCheck("go test ./...", 0, "all tests pass")
		hits = append(hits, markerHit{s, e})
	}

	prose := exciseMarkers(intent, hits)
	return criteria, prose, hits
}

// orDefault returns *p when non-nil, else d.
func orDefault(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}

// exciseMarkers removes the matched marker spans from intent and collapses
// residual whitespace, returning the prose remainder trimmed.
func exciseMarkers(intent string, hits []markerHit) string {
	if len(hits) == 0 {
		return strings.TrimSpace(intent)
	}
	var sb strings.Builder
	prev := 0
	for _, h := range hits {
		if h.start > prev {
			sb.WriteString(intent[prev:h.start])
		}
		prev = h.end
	}
	if prev < len(intent) {
		sb.WriteString(intent[prev:])
	}
	// Collapse runs of whitespace left by excision.
	out := regexp.MustCompile(`\s+`).ReplaceAllString(sb.String(), " ")
	return strings.TrimSpace(out)
}

// --- The compiler entry point (FR-110, G-7) --------------------------------

// compileGoalIntent is the ADR-053 Phase-2 SMART compiler (FR-110): an
// engine-invoked, schema-validated turn that interprets user intent → a goal
// record (prompt + goal definition + acceptance criteria). It runs the
// co-located marker parser, lifts the prose remainder to a KindProse criterion
// when it carries a judgeable statement, schema-validates the ladder via
// task.NormalizeCriteria, then vets every criterion through the compile-time
// feasibility gate (FR-111/D9). On a gate failure the returned CompileResult
// carries a Rejection and a nil Goal — no rejected criterion persists (the
// caller surfaces the rejection in chat and asks for a re-statement).
//
// fc may be nil — a nil context skips the reachability vetoes (used by tests
// that exercise only the parser/prose path); production wiring always supplies
// one so the gate is exhaustive (D9 makes it the sole filter).
func compileGoalIntent(intent string, fc FeasibilityContext, authorID string) CompileResult {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return CompileResult{Rejection: &FeasibilityRejection{
			CriterionIndex: -1, Reason: "goal intent is empty",
		}}
	}

	parsed, prose, _ := parseIntentMarkers(intent, authorID)
	criteria := parsed

	// Lift the prose remainder to a KindProse criterion when it is non-trivial
	// (more than a steering fragment). The feasibility gate then vetoes it if
	// it is semantically unjudgeable.
	if prose != "" && !looksLikePureSteering(prose) {
		criteria = append(criteria, task.AcceptanceCriterion{
			Kind: task.KindProse, Text: prose,
			Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: authorID},
		})
	}

	// Schema-validate the full ladder (the "schema-validated compilation turn"
	// output — server-sets IDs, checks shape). A shape error here is a compiler
	// bug, not a user error; surface it as a whole-goal rejection.
	normalized, err := task.NormalizeCriteria(criteria)
	if err != nil {
		return CompileResult{Rejection: &FeasibilityRejection{
			CriterionIndex: -1, Reason: "compiled criterion failed shape validation: " + err.Error(),
		}}
	}

	// Feasibility gate (FR-111/D9 — the ONLY net for unverifiable criteria).
	if fc != nil {
		if rej := feasibilityGate(normalized, fc); rej != nil {
			return CompileResult{Rejection: rej}
		}
	}

	// A goal must carry at least one criterion to be verifiable at all; a
	// pure-steering intent with no markers and no judgeable prose is rejected
	// (D9: no criterion → nothing for the runtime to verify).
	if len(normalized) == 0 {
		return CompileResult{Rejection: &FeasibilityRejection{
			CriterionIndex: -1,
			Reason:         goalNoCriteriaRejectionReason,
		}}
	}

	return CompileResult{Goal: &CompiledGoal{
		Intent:   intent,
		Prompt:   prose,
		Criteria: normalized,
		// Fix-wave finding #2 (echo-vs-judged divergence): populate the
		// floor DoD (ADR-080 D-DOD layer 3) HERE, at compile time, not just
		// on load. compiledGoalCriteriaFor unions Criteria+DoD for
		// adjudication regardless of which path compiled the goal, so a
		// deterministic-fallback or marker-only goal that carried NO DoD at
		// compile time was still judged against the floor DoD once
		// loadCompiledGoal's own load-time backfill kicked in — but every
		// confirm echo (formatGoalEcho/formatGoalStatementAndCriteria,
		// buildGoalPendingNote, the queued goal_status frame) is built from
		// the freshly compiled CompiledGoal BEFORE it round-trips through
		// GoalCriteriaJSON, so the user confirmed against a bar they never
		// saw. Setting it here makes echoed == judged on every path; it also
		// makes loadCompiledGoal's own backfill a legacy-goal-only
		// fallback (a goal compiled after this fix never needs it).
		DoD: newFloorDoD(),
	}}
}

// goalNoCriteriaRejectionReason is the whole-goal "nothing verifiable"
// rejection text. Rewritten plain-language-FIRST per ADR-074 D4a (spec US-3
// S5): the old text led with marker syntax ("[tests pass], [search: 3]"),
// steering non-technical users toward technical markers; the plain-language
// description now leads and the marker syntax survives only as a trailing
// aside for users who want it.
const goalNoCriteriaRejectionReason = "I couldn't find anything checkable in that goal. " +
	"Describe what should be TRUE when the goal is done — an observable outcome, " +
	"specific enough that a reviewer could say it failed (for example: \"the summary " +
	"is written to notes.md\" or \"the flaky login test passes\"). Technical shorthand " +
	"like [tests pass] or [search: 3] also works if you prefer it"

// goalIntentNeedsLLMCompile reports whether intent takes the ADR-074 D4a LLM
// compilation path (US-3 S1/S2): true when the prose remainder after marker
// extraction is a real goal statement. Marker-only intents (every criterion
// from explicit markers; remainder empty or pure steering) return false and
// keep today's deterministic, immediate, zero-LLM path pinned (US-3 S3) — as
// does a fully-empty/steering-only intent, whose deterministic rejection is
// also unchanged.
func goalIntentNeedsLLMCompile(intent string) bool {
	_, prose, _ := parseIntentMarkers(strings.TrimSpace(intent), "")
	return prose != "" && !looksLikePureSteering(prose)
}

// looksLikePureSteering reports whether prose is only connective/steering text
// (e.g. "please continue", "and keep going") with no goal statement to lift to
// a prose criterion. Such text folds into the worker prompt instead.
func looksLikePureSteering(prose string) bool {
	low := strings.ToLower(strings.TrimSpace(prose))
	if low == "" {
		return true
	}
	steeringFragments := []string{
		"please continue", "keep going", "and continue", "go ahead", "proceed",
		"do it", "thanks", "ok", "okay", "sure",
	}
	for _, f := range steeringFragments {
		if low == f {
			return true
		}
	}
	return false
}

// --- The compile-time feasibility gate (FR-111/D9/R§8.1) -------------------

// feasibilityGate vets the compiled criteria (FR-111/D9): it is the ONLY net
// for unverifiable criteria (D9 deleted the runtime criterion_unverifiable
// verdict). It rejects, fail-closed and immutable, any criterion the runtime
// cannot verify, vetting BOTH:
//  1. REACHABILITY — a behavior criterion's tool must be policy-reachable
//     (EffectiveToolPolicy != deny); a check criterion's runner (bash) must
//     resolve;
//  2. SEMANTIC JUDGEABILITY — an LLM judgment can plausibly be formed (the
//     criterion is not empty/marker-only/pure-hedging with no observable
//     referent).
//
// Returns the FIRST rejection (indices preserve the input order), or nil if
// every criterion is feasible. No rejected criterion persists — the caller
// discards the whole compiled goal on a rejection and surfaces the reason.
func feasibilityGate(criteria []task.AcceptanceCriterion, fc FeasibilityContext) *FeasibilityRejection {
	for i, c := range criteria {
		switch c.Kind {
		case task.KindBehavior:
			if c.Behavior == nil || c.Behavior.Tool == "" {
				return &FeasibilityRejection{CriterionIndex: i,
					Reason: "behavior criterion is missing its tool — cannot be scanned"}
			}
			if fc.EffectiveToolPolicy(c.Behavior.Tool) == "deny" {
				return &FeasibilityRejection{CriterionIndex: i,
					Reason: fmt.Sprintf(
						"behavior criterion tool %q is out of the agent's tool policy (deny) — "+
							"the runtime could never satisfy this check (FR-111/D9)",
						c.Behavior.Tool)}
			}
		case task.KindCheck:
			if c.Check == nil || strings.TrimSpace(c.Check.Command) == "" {
				return &FeasibilityRejection{CriterionIndex: i,
					Reason: "machine check is missing its command — cannot be run"}
			}
			if !fc.BashReachable() {
				return &FeasibilityRejection{CriterionIndex: i,
					Reason: "machine check requires the bash tool, which is out of the agent's " +
						"policy — the runtime could never execute this check (FR-111/D9)"}
			}
		case task.KindProse:
			if looksSemanticallyUnjudgeable(c.Text) {
				// Plain-language-first (ADR-074 D4a / FR-007): lead with what
				// the user can do about it, not with internal vocabulary.
				return &FeasibilityRejection{CriterionIndex: i,
					Reason: fmt.Sprintf(
						"%q can't be verified as written — it doesn't name anything observable "+
							"a reviewer could check. Describe what should be TRUE when the goal is "+
							"done (a concrete outcome, specific enough to fail)",
						c.Text)}
			}
		default:
			return &FeasibilityRejection{CriterionIndex: i,
				Reason: fmt.Sprintf("unknown criterion kind %q", c.Kind)}
		}
	}
	return nil
}

// looksSemanticallyUnjudgeable is the narrow compile-time heuristic for D9's
// "an LLM judgment can be formed" test. It is intentionally conservative: the
// RUNTIME classifier (classifyNonVerdict → criterion_unjudgeable, R§8.1) is
// the real net for the subjective gray area, and D9 only requires the gate to
// be exhaustive against criteria the runtime OBVIOUSLY cannot verify. This
// catches: empty/whitespace text, marker-only residue, and prose that is solely
// hedging/opinion tokens with no content word that could anchor a judgment
// (e.g. "feels maintainable", "vibes good"). Anything with a substantive
// referent passes to the runtime Judge.
func looksSemanticallyUnjudgeable(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return true
	}
	// Marker-only residue (e.g. a stray "[]" or "min:3" left after a malformed
	// marker) has no judgeable statement.
	if isMarkerResidue(t) {
		return true
	}
	words := strings.Fields(t)
	hasContent := false
	for _, w := range words {
		clean := strings.Trim(strings.ToLower(w), ".,!?;:\"'()[]")
		if len(clean) < 3 {
			continue
		}
		if hedgingTokens[clean] {
			continue
		}
		hasContent = true
		break
	}
	return !hasContent
}

// isMarkerResidue reports whether text looks like a leftover marker fragment
// (brackets/colon-keyword with no natural-language statement).
func isMarkerResidue(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return true
	}
	// Pure bracketed token like "[...]" or "min:3 max:2".
	stripped := strings.Trim(t, "[]")
	if stripped == "" {
		return true
	}
	if regexp.MustCompile(`(?i)^(min|max|exit|behavior|check|search|tests|tool)\b`).MatchString(stripped) {
		return true
	}
	return false
}

// hedgingTokens are opinion/feeling words that, when they are the ONLY content
// of a prose criterion, leave no observable referent for a judgment.
var hedgingTokens = map[string]bool{
	"feels": true, "seems": true, "looks": true, "vibes": true, "maybe": true,
	"kinda": true, "sorta": true, "stuff": true, "things": true, "whatever": true,
	"good": true, "nice": true, "okay": true, "ok": true, "fine": true,
	"maintainable": true, "readable": true, "clean": true, "elegant": true,
	"pretty": true, "really": true, "very": true, "quite": true, "somewhat": true,
}

// --- Echo & confirm + amendment diff (FR-113/D11/N-6) ----------------------

// formatGoalStatementAndCriteria renders the shared CORE of every goal echo
// surface (ADR-080 D-STATEMENT/D-DOD, §122's "formatGoalEcho on EVERY
// surface"): the restated goal statement, the criteria ladder, and a
// DISTINCT "Definition of Done" block with `provenance == inferred` items
// flagged for approve/drop. formatGoalEcho (the web card AND the channel
// plain-text echo — one renderer, no separate channel path) and
// buildGoalPendingNote (goal_pending_note.go, ADR-078 D2) both build their
// surface-specific framing around this so a channel user who confirms by
// TYPING sees exactly what the web GoalEchoCard shows — the R-C2 fix: DoD
// and its inferred gates were previously web-card-only.
func formatGoalStatementAndCriteria(g *CompiledGoal) string {
	var sb strings.Builder
	sb.WriteString("Goal: ")
	switch {
	case g.Definition != "":
		// D-STATEMENT: the compiled SMART restatement leads every surface —
		// in place of the old Prompt/Intent fallback, which now only applies
		// to a goal compiled before this field existed (deterministic
		// fallback path, or a pre-ADR-080 persisted goal).
		sb.WriteString(g.Definition)
	case g.Prompt != "":
		sb.WriteString(g.Prompt)
	default:
		sb.WriteString(g.Intent)
	}
	sb.WriteString("\n\nDone when (a reviewer will verify each of these):\n")
	for i, c := range g.Criteria {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, criterionEchoLine(c))
	}
	if len(g.DoD) > 0 {
		// D-DOD: a DISTINCT block, never merged into the criteria numbering —
		// they are judged together (the verifier_adjudication.go union seam)
		// but shown separately so the setter can tell "what I asked for" from
		// "the standing quality bar every goal carries".
		sb.WriteString("\nDefinition of Done (standing quality gates, judged alongside the criteria above):\n")
		for i, c := range g.DoD {
			line := criterionEchoLine(c)
			if c.Provenance == task.ProvenanceInferred {
				line += " (inferred — confirm or drop)"
			}
			fmt.Fprintf(&sb, "  %d. %s\n", i+1, line)
		}
	}
	return sb.String()
}

// formatGoalEcho renders the compiled goal for the chat echo (FR-113/D11/G-8,
// delivered by ADR-074 D4a's pending step — this is the confirmation surface
// in chat history): the literal commands are included verbatim (never
// paraphrased away), and the whole echo is what the user confirms by a chat
// reply — no form/modal. The user's confirming reply (any of
// confirmGoalAliases, or `/goal confirm`) activates the goal. This IS the
// channel plain-text echo (no separate renderer) — see
// formatGoalStatementAndCriteria for the statement/criteria/DoD core it
// shares with buildGoalPendingNote.
//
// Plain-language-first (spec US-6 S4, FR-011): criteria are itemized as
// readable statements with their technical payloads verbatim per row —
// NEVER as `[kind]` classification tokens, which are not user-facing content.
func formatGoalEcho(g *CompiledGoal) string {
	if g == nil {
		return "(no goal compiled)"
	}
	var sb strings.Builder
	sb.WriteString("Here's the goal I've compiled for your confirmation.\n\n")
	sb.WriteString(formatGoalStatementAndCriteria(g))
	sb.WriteString("\nReply **" + ConfirmGoalWord + "** (or `/goal confirm`) to activate this goal, " +
		"`/goal <new intent>` to restate it, or `/goal clear` to discard.")
	return sb.String()
}

// goalEchoFallbackNote is the FR-014/US-3 S4 observability line appended to
// the pending echo whenever the deterministic parser produced the criteria
// because the LLM compile could not (failure/timeout/schema miss/second veto).
const goalEchoFallbackNote = "\n\nNote: the automatic quality-bar rewrite was unavailable, " +
	"so these criteria were compiled directly from your wording without it."

// judgmentEchoSuffix renders a short, plain-language tag for a criterion's
// ADR-080 D-TYPES judgment kind (the "what SHAPE of claim is this" axis,
// distinct from Kind's verification mechanism) — appended to every echo line
// so the setter reviews not just the criterion's text but what shape of
// verdict the Judge will form on it. Never a raw enum token (same
// plain-language-first bar formatGoalEcho's own doc comment states for Kind).
func judgmentEchoSuffix(j task.JudgmentKind) string {
	switch j {
	case task.JudgmentBoolean:
		return " (judged yes/no)"
	case task.JudgmentQuantitative:
		return " (judged against a measured value)"
	case task.JudgmentArtifact:
		return " (judged by checking for the named result)"
	default:
		return ""
	}
}

// criterionEchoLine renders one criterion's literal verification shape for the
// echo (commands included verbatim — FR-113 "including literal commands"),
// followed by its ADR-080 D-TYPES judgment tag (judgmentEchoSuffix).
func criterionEchoLine(c task.AcceptanceCriterion) string {
	base := c.Text
	switch c.Kind {
	case task.KindBehavior:
		if c.Behavior != nil {
			minCount := c.Behavior.EffectiveMinCount()
			if c.Behavior.MaxCount != nil {
				base = fmt.Sprintf("%s: call %s between %d and %d times", c.Text, c.Behavior.Tool, minCount, *c.Behavior.MaxCount)
			} else {
				base = fmt.Sprintf("%s: call %s at least %d times", c.Text, c.Behavior.Tool, minCount)
			}
		}
	case task.KindCheck:
		if c.Check != nil {
			base = fmt.Sprintf("%s: `%s` (expected exit %d)", c.Text, c.Check.Command, c.Check.ExpectedExitCode)
		}
	}
	return base + judgmentEchoSuffix(c.Judgment)
}

// ConfirmGoalWord is the chat reply that activates an echoed goal (FR-113/D11).
// A bare "confirm"/"yes"/"ok"/"activate" (case-insensitive) confirms; anything
// else is treated as a re-statement → an amendment diff (N-6).
const ConfirmGoalWord = "confirm"

// confirmGoalAliases are the accepted chat-confirming replies.
var confirmGoalAliases = map[string]bool{
	"confirm": true, "yes": true, "ok": true, "okay": true, "activate": true, "y": true,
}

// IsGoalConfirm reports whether a chat reply confirms the echoed goal.
func IsGoalConfirm(reply string) bool {
	return confirmGoalAliases[strings.ToLower(strings.TrimSpace(reply))]
}

// GoalAmendment is the diffed, confirmed amendment surface (N-6/D11): a
// re-statement is NEVER a silent recompile. It shows the user exactly what was
// added, changed, and dropped relative to the currently-active goal, and the
// Proposed goal is what they confirm to amend to (minting a new goal generation
// — criteria stay immutable per D9; re-statement AMENDS via a fresh record).
type GoalAmendment struct {
	Added   []task.AcceptanceCriterion `json:"added"`
	Changed []task.AcceptanceCriterion `json:"changed"`
	Dropped []task.AcceptanceCriterion `json:"dropped"`
	// DoDAdded/DoDChanged/DoDDropped are fix-wave finding #4's DoD diff
	// (ADR-080 D-DOD): a `/goal <new intent>` amendment recompiles a new DoD
	// alongside Criteria — both are unioned into the judged set on confirm
	// (compiledGoalCriteriaFor) — so the amendment echo must show DoD
	// deltas too, not just Criteria's. Kept as separate fields (not merged
	// into Added/Changed/Dropped) so formatAmendmentEcho can render them in
	// their own "Definition of Done changes" block, mirroring
	// formatGoalStatementAndCriteria's distinct DoD block on the plain
	// compile echo.
	DoDAdded   []task.AcceptanceCriterion `json:"dod_added,omitempty"`
	DoDChanged []task.AcceptanceCriterion `json:"dod_changed,omitempty"`
	DoDDropped []task.AcceptanceCriterion `json:"dod_dropped,omitempty"`
	Proposed   *CompiledGoal              `json:"proposed"`
}

// HasChanges reports whether the amendment actually differs from current
// (Criteria OR DoD).
func (a *GoalAmendment) HasChanges() bool {
	if a == nil {
		return false
	}
	return len(a.Added) > 0 || len(a.Changed) > 0 || len(a.Dropped) > 0 ||
		len(a.DoDAdded) > 0 || len(a.DoDChanged) > 0 || len(a.DoDDropped) > 0
}

// diffGoalAmendment computes the added/changed/dropped diff between the
// current active goal and a proposed re-statement (N-6), for BOTH Criteria
// and DoD (fix-wave finding #4 — a re-statement recompiles a new DoD too,
// and it must not be diffed silently). A criterion is "changed" when its
// Text matches an existing one but its verification shape (Check/Behavior)
// or judgment differs; "dropped" when present in current but not proposed;
// "added" when present in proposed but not current. The proposed goal's
// criteria (and DoD) drive the new generation on confirm.
func diffGoalAmendment(current, proposed *CompiledGoal) *GoalAmendment {
	amd := &GoalAmendment{Proposed: proposed}
	if proposed == nil {
		return amd
	}
	var curCriteria, curDoD []task.AcceptanceCriterion
	if current != nil {
		curCriteria = current.Criteria
		curDoD = current.DoD
	}
	amd.Added, amd.Changed, amd.Dropped = diffCriteriaSet(curCriteria, proposed.Criteria)
	amd.DoDAdded, amd.DoDChanged, amd.DoDDropped = diffCriteriaSet(curDoD, proposed.DoD)
	return amd
}

// diffCriteriaSet is the shared added/changed/dropped set-diff (by
// normalized text + sameShape) that diffGoalAmendment runs once for
// Criteria and once for DoD — a single implementation so the two ladders
// diff identically (fix-wave finding #4).
func diffCriteriaSet(current, proposed []task.AcceptanceCriterion) (added, changed, dropped []task.AcceptanceCriterion) {
	curByText := map[string]task.AcceptanceCriterion{}
	for _, c := range current {
		curByText[normalizeCritText(c.Text)] = c
	}
	propSeen := map[string]bool{}
	for _, c := range proposed {
		key := normalizeCritText(c.Text)
		propSeen[key] = true
		if existing, ok := curByText[key]; ok {
			if !sameShape(existing, c) {
				changed = append(changed, c)
			}
			// identical text + shape → unchanged (not listed)
		} else {
			added = append(added, c)
		}
	}
	for _, c := range current {
		if !propSeen[normalizeCritText(c.Text)] {
			dropped = append(dropped, c)
		}
	}
	return added, changed, dropped
}

// formatAmendmentEcho renders the amendment for chat confirmation (N-6),
// including the DoD delta block (fix-wave finding #4) whenever the
// amendment touches DoD, with inferred items flagged for approve/drop
// exactly like formatGoalStatementAndCriteria's own DoD block.
func formatAmendmentEcho(a *GoalAmendment) string {
	if a == nil || a.Proposed == nil {
		return "No amendment to apply."
	}
	if !a.HasChanges() {
		return "The re-statement matches the current goal — no changes to amend."
	}
	var sb strings.Builder
	sb.WriteString("Proposed amendment to your active goal:\n\n")
	for _, c := range a.Added {
		sb.WriteString("  + [added]   " + criterionEchoLine(c) + "\n")
	}
	for _, c := range a.Changed {
		sb.WriteString("  ~ [changed] " + criterionEchoLine(c) + "\n")
	}
	for _, c := range rangeCriteria(a.Dropped) {
		sb.WriteString("  - [dropped] " + criterionEchoLine(c) + "\n")
	}
	if len(a.DoDAdded) > 0 || len(a.DoDChanged) > 0 || len(a.DoDDropped) > 0 {
		sb.WriteString("\nDefinition of Done changes (standing quality gates, judged alongside the criteria above):\n")
		for _, c := range a.DoDAdded {
			sb.WriteString("  + [added]   " + dodAmendmentEchoLine(c) + "\n")
		}
		for _, c := range a.DoDChanged {
			sb.WriteString("  ~ [changed] " + dodAmendmentEchoLine(c) + "\n")
		}
		for _, c := range rangeCriteria(a.DoDDropped) {
			sb.WriteString("  - [dropped] " + dodAmendmentEchoLine(c) + "\n")
		}
	}
	sb.WriteString("\nReply **" + ConfirmGoalWord + "** to apply this amendment, or restate again.")
	return sb.String()
}

// dodAmendmentEchoLine renders one DoD delta line, flagging
// provenance==inferred items exactly like formatGoalStatementAndCriteria's
// own DoD block (fix-wave finding #4).
func dodAmendmentEchoLine(c task.AcceptanceCriterion) string {
	line := criterionEchoLine(c)
	if c.Provenance == task.ProvenanceInferred {
		line += " (inferred — confirm or drop)"
	}
	return line
}

// rangeCriteria is a trivial identity-iterator kept so the dropped-loop reads
// uniformly with the append loops above (and a single place to guard nil).
func rangeCriteria(cs []task.AcceptanceCriterion) []task.AcceptanceCriterion {
	if cs == nil {
		return nil
	}
	return cs
}

// normalizeCritText is the text key used for amendment matching (case/whitespace).
func normalizeCritText(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// sameShape reports whether two criteria with the same Text have the same
// verification shape (Check or Behavior payload) AND the same judgment
// (ADR-080 D-TYPES: two criteria are "the same shape" only if their judgment
// matches too — a re-statement that keeps the same text and Check/Behavior
// payload but retags the judgment, e.g. boolean -> quantitative, is still a
// real change the amendment diff must surface as "changed", not silently
// treat as identical).
func sameShape(a, b task.AcceptanceCriterion) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Judgment != b.Judgment {
		return false
	}
	switch a.Kind {
	case task.KindCheck:
		if a.Check == nil || b.Check == nil {
			return a.Check == b.Check
		}
		return a.Check.Command == b.Check.Command && a.Check.ExpectedExitCode == b.Check.ExpectedExitCode
	case task.KindBehavior:
		if a.Behavior == nil || b.Behavior == nil {
			return a.Behavior == b.Behavior
		}
		return a.Behavior.Tool == b.Behavior.Tool &&
			a.Behavior.EffectiveMinCount() == b.Behavior.EffectiveMinCount() &&
			intPtrEq(a.Behavior.MaxCount, b.Behavior.MaxCount)
	}
	return true
}

func intPtrEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// --- Non-verdict classifier + escalate-once (FR-115/FR-116/FR-137/FR-138) ---

// NonVerdictClass is the M1 classification of a verifier turn that produced no
// met/unmet ruling (FR-137). It keys on whether the verification MECHANISM ran
// to completion — never on prose.
type NonVerdictClass string

const (
	// NonVerdictNone means the mechanism ran AND formed a judgment (a normal
	// met/unmet verdict — not a non-verdict at all).
	NonVerdictNone NonVerdictClass = ""
	// NonVerdictUnableToVerify means the verification MECHANISM could NOT
	// execute (sandbox-denied / tool unavailable / policy-blocked / exit-code
	// unreadable). The check is re-run safely and NEVER scored (FR-116/G-3),
	// bounded by UnableToVerifyTracker (m-4) before a persistently-blocked
	// owner escalation.
	NonVerdictUnableToVerify NonVerdictClass = "unable_to_verify"
	// NonVerdictCriterionUnjudgeable means the verifier turn RAN to completion
	// but formed no judgment (genuinely subjective). The criterion resolves
	// unmet for that adjudication (AND-combine) and emits exactly ONE
	// owner-escalation per goal-id (FR-115/FR-138); the owner re-states (a
	// diffed amendment) or /goal clears, else the goal honestly terminates
	// failed(judge_rounds_exhausted).
	NonVerdictCriterionUnjudgeable NonVerdictClass = "criterion_unjudgeable"
)

// VerifierTurnOutcome is the machine-observable outcome of one verifier turn,
// fed to classifyNonVerdict. MechanismRan captures "did the verification
// MECHANISM execute (not blocked/unavailable/unreadable)?"; FormedJudgment
// captures "did it produce a met/unmet ruling?".
type VerifierTurnOutcome struct {
	// MechanismRan is false when the sandbox denied the tool, the tool was
	// unavailable, policy blocked it, or an exit code was unreadable.
	MechanismRan bool
	// FormedJudgment is true when the verifier returned a met/unmet ruling for
	// this criterion (a real judgment, not a non-verdict).
	FormedJudgment bool
}

// classifyNonVerdict is the named M1 predicate (FR-137): it keys on whether the
// verification mechanism ran to completion — never on prose.
//   - mechanism could NOT run → NonVerdictUnableToVerify (re-run, never scored,
//     bounded by UnableToVerifyTracker);
//   - ran but no judgment → NonVerdictCriterionUnjudgeable (unmet + escalate-
//     once);
//   - ran and judged → NonVerdictNone (a normal verdict, not a non-verdict).
func classifyNonVerdict(o VerifierTurnOutcome) NonVerdictClass {
	if !o.MechanismRan {
		return NonVerdictUnableToVerify
	}
	if !o.FormedJudgment {
		return NonVerdictCriterionUnjudgeable
	}
	return NonVerdictNone
}

// UnableToVerifyTracker bounds the unable_to_verify re-run loop (R§8.1/m-4/
// FR-116): after maxReruns CONSECUTIVE unable_to_verify results on the SAME
// check, the check escalates to the owner as persistently-blocked (a distinct
// owner escalation from criterion_unjudgeable) so a permanently-blocked check
// cannot loop the adjudication forever. Safe for concurrent use.
type UnableToVerifyTracker struct {
	mu        sync.Mutex
	reruns    map[string]int
	maxReruns int
}

// NewUnableToVerifyTracker constructs a tracker; maxReruns<=0 falls back to
// UnableToVerifyMaxRerunsDefault.
func NewUnableToVerifyTracker(maxReruns int) *UnableToVerifyTracker {
	if maxReruns <= 0 {
		maxReruns = UnableToVerifyMaxRerunsDefault
	}
	return &UnableToVerifyTracker{reruns: make(map[string]int), maxReruns: maxReruns}
}

// NoteUnableToVerify records one consecutive unable_to_verify result for
// criterionID and reports whether the check is now persistently-blocked (the
// re-run bound reached → owner escalation). Once persistently-blocked, further
// notes for that criterion stay escalated until Reset.
func (t *UnableToVerifyTracker) NoteUnableToVerify(criterionID string) (persistentlyBlocked bool) {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Fix-wave finding 6(g): lazily init a nil map so a zero-value
	// &UnableToVerifyTracker{} (constructed without NewUnableToVerifyTracker)
	// is safe to use rather than panicking on this write ("assignment to
	// entry in nil map").
	if t.reruns == nil {
		t.reruns = make(map[string]int)
	}
	t.reruns[criterionID]++
	return t.reruns[criterionID] > t.maxReruns
}

// Reset clears the consecutive count for a criterion (called when the
// mechanism succeeds or forms a real judgment — the blocker cleared).
func (t *UnableToVerifyTracker) Reset(criterionID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.reruns, criterionID)
}

// Consecutive returns the current consecutive unable_to_verify count.
func (t *UnableToVerifyTracker) Consecutive(criterionID string) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reruns[criterionID]
}

// UnjudgeableEscalationGate enforces the "exactly one criterion_unjudgeable
// owner-escalation per goal-id" rule (FR-115/FR-138). The escalation SURFACES
// the mis-compile — it does NOT itself halt round consumption (R§8.1/M2); the
// owner re-states (amendment) or /goal clears, else the goal honestly
// terminates failed(judge_rounds_exhausted).
type UnjudgeableEscalationGate struct {
	mu        sync.Mutex
	escalated map[string]bool // goalID → escalated once
}

// NewUnjudgeableEscalationGate constructs the per-install escalation gate.
func NewUnjudgeableEscalationGate() *UnjudgeableEscalationGate {
	return &UnjudgeableEscalationGate{escalated: make(map[string]bool)}
}

// ShouldEscalate reports whether a criterion_unjudgeable result for goalID
// should emit its (one) owner escalation. The first call for a goal-id returns
// true and marks it escalated; subsequent calls return false (the escalation
// already surfaced — surfacing again would not help and R§8.1 bounds it to one).
func (g *UnjudgeableEscalationGate) ShouldEscalate(goalID string) bool {
	if g == nil {
		return true // fail-open to surface (safer for the owner) when unconfigured
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// Fix-wave finding 6(g): lazily init a nil map so a zero-value
	// &UnjudgeableEscalationGate{} (constructed without
	// NewUnjudgeableEscalationGate) is safe to use rather than panicking on
	// the write below ("assignment to entry in nil map").
	if g.escalated == nil {
		g.escalated = make(map[string]bool)
	}
	if g.escalated[goalID] {
		return false
	}
	g.escalated[goalID] = true
	return true
}

// Reset clears the escalation marker for the given key.
//
// Fix-wave finding 3 (14-reviewer sign-off): this was documented as "used on
// /goal clear or a confirmed amendment that mints a new generation" but had
// ZERO production callers — the gate is process-wide and was keyed purely by
// chat session (verifierUnitID/verifierUnitForGoal), so a session that
// escalated once for one goal could never escalate again for ANY later goal
// in that same session, including a totally unrelated one started after
// `/goal clear`.
//
// The fix does NOT wire an explicit Reset call here — clearGoal and
// confirmPendingGoal (the two call sites the old doc comment named) live in
// goal_loop.go, outside this package area's fix-wave scope. It also would
// not have been sufficient on its own: confirmPendingGoal's own contract
// deliberately keeps the SAME GoalID across a confirmed amendment ("the same
// goal being refined, not a new one"), so re-keying — or resetting — by
// GoalID alone silently fails to free a fresh escalation slot for an
// amendment, only for a `/goal clear` + fresh goal.
//
// Instead, judge.go folds a fingerprint of the ACTUAL criteria ladder being
// judged into the goal-scope escalation key (see
// goalCriteriaLadderFingerprint / judge.go's escalationKey). Criterion IDs
// are server-set random UUIDs minted fresh by every compileGoalIntent call
// and persisted unchanged for the rest of that generation's rounds, so the
// fingerprint is stable across repeated rounds of one still-active goal
// (preserving "escalate at most once per generation") and changes on every
// fresh compile — a clear+restate AND a confirmed amendment both recompile,
// so both get a fresh, unescalated key with no explicit Reset call needed.
//
// This method is retained as a general utility (direct unit testing, a
// caller with a raw key already in hand, or a future scope that isn't
// self-fingerprinting) — it is deliberately still safe to call, just no
// longer required for the goal path's correctness.
func (g *UnjudgeableEscalationGate) Reset(goalID string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.escalated, goalID)
}

// goalCriteriaLadderFingerprint returns a stable fingerprint for a compiled
// goal's criteria ladder, built from its criterion IDs (sorted for order-
// independence). See UnjudgeableEscalationGate.Reset's doc comment
// (fix-wave finding 3) for why this — not an explicit Reset call, and not a
// meta.GoalID rekey — is what scopes "escalate once" to one generation of a
// /goal loop rather than to the chat session forever. Returns "" for an
// empty ladder (defensive; a criterion just triggered classifyNonVerdict, so
// callers always have at least one).
func goalCriteriaLadderFingerprint(criteria []task.AcceptanceCriterion) string {
	if len(criteria) == 0 {
		return ""
	}
	ids := make([]string, len(criteria))
	for i, c := range criteria {
		ids[i] = c.ID
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// --- Feasibility adapter + compiled-goal persistence -----------------------

// agentFeasibilityContext adapts an AgentInstance to FeasibilityContext,
// reading the goal-bearing agent's OWN tool policy (FR-112 — compiled checks
// run under the agent's own policy + sandbox, never a privileged bypass).
type agentFeasibilityContext struct{ agentInst *AgentInstance }

// EffectiveToolPolicy resolves toolName under the agent's own policy. Behavior
// tools (search_web, browse, …) are general-scope by convention; the verdict
// is conservative (deny ⇒ the tool is unreachable ⇒ the criterion is rejected
// at compile, FR-111). A nil agent fails closed to deny.
func (a agentFeasibilityContext) EffectiveToolPolicy(toolName string) string {
	if a.agentInst == nil {
		return "deny"
	}
	return tools.EffectiveToolPolicy(
		a.agentInst.LoadToolPolicy(), tools.ScopeGeneral, a.agentInst.AgentType, toolName)
}

// BashReachable reports whether bash (the KindCheck runner per
// CriterionCheck) is policy-reachable, mirroring judge.go's own check.
func (a agentFeasibilityContext) BashReachable() bool {
	if a.agentInst == nil {
		return false
	}
	return tools.EffectiveToolPolicy(
		a.agentInst.LoadToolPolicy(), tools.ScopeCore, a.agentInst.AgentType, "bash") != "deny"
}

// goalDoDFloorAuthorID tags the built-in floor DoD's author identity (no
// human/agent authored these — they are the ADR-080 D-DOD layer-3
// guarantee). Mirrors judge.go's softTierCriterionID pattern: a fixed,
// recognizable sentinel rather than a fresh UUID, so re-derivations are
// byte-stable and a floor item is identifiable at a glance in logs/echoes.
const goalDoDFloorAuthorID = "system"

// newFloorDoD constructs ADR-080 D-DOD's built-in floor Definition of Done
// (layer 3): a few universal quality gates that GUARANTEE a goal always
// carries at least one DoD item (>= 1), even when nothing was stated, no
// workspace convention applied, and no bounded inference ran. Defined once
// as a single reusable constructor (per the ADR) so both the legacy-goal
// load-time backfill below and the compiler's own layer-3 fallback (a later
// wave) stay identical. IDs are fixed sentinels, not fresh UUIDs, so the
// floor DoD is byte-stable across repeated loads of the same legacy goal.
func newFloorDoD() []task.AcceptanceCriterion {
	author := task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: goalDoDFloorAuthorID}
	return []task.AcceptanceCriterion{
		{
			ID: "goal-dod-floor-no-secrets", Kind: task.KindProse,
			Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
			Text:   "No secrets or credentials appear in the output.",
			Author: author, Status: task.CritPending,
		},
		{
			ID: "goal-dod-floor-grounded-claims", Kind: task.KindProse,
			Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
			Text:   "Every factual claim is grounded, not assumed.",
			Author: author, Status: task.CritPending,
		},
	}
}

// marshalCompiledGoal serializes a CompiledGoal for GoalCriteriaJSON persistence.
func marshalCompiledGoal(g *CompiledGoal) (string, error) {
	if g == nil {
		return "", nil
	}
	data, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// loadCompiledGoal deserializes a CompiledGoal from GoalCriteriaJSON. Returns
// nil for an empty/unparseable value (callers fall back to a single prose
// criterion from GoalCondition for back-compat with pre-Phase-2 goals).
//
// Fix-wave finding 6(h): an EMPTY raw value is the legitimately absent case
// (silent nil — a pre-Phase-2 goal with no compiled ladder yet, or none at
// all) and stays silent. A NON-EMPTY raw value that fails to parse, or
// parses but carries zero criteria, is CORRUPT — logged loud (still
// returning nil, so the caller's fallback behavior is unchanged) so a real
// persisted-record bug is never silently indistinguishable from "nothing was
// ever compiled here".
func loadCompiledGoal(raw string) *CompiledGoal {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var g CompiledGoal
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		logger.WarnCF("agent", "goal: GoalCriteriaJSON is non-empty but failed to parse as a CompiledGoal "+
			"(falling back to a single prose criterion from GoalCondition)",
			map[string]any{"error": err.Error()})
		return nil
	}
	if len(g.Criteria) == 0 {
		logger.WarnCF("agent", "goal: GoalCriteriaJSON parsed but carries zero criteria "+
			"(falling back to a single prose criterion from GoalCondition)", nil)
		return nil
	}
	// ADR-080 D-DOD legacy-goal backfill: a pre-ADR-080 persisted goal has no
	// DoD at all (the field did not exist yet). Inject the built-in floor DoD
	// here, in the goal-LOAD path, BEFORE any schema validation of the wire
	// Goal record — mirrors normalizeCriteria's own load-time judgment
	// backfill — so a legacy goal always satisfies the wire schema's
	// `dod: minItems: 1` instead of failing the read.
	if len(g.DoD) == 0 {
		g.DoD = newFloorDoD()
	}

	// Fix-wave finding #8: run BOTH ladders through the real
	// task.NormalizeCriteria — until this fix, criterion.go's own doc
	// comment claimed a "load-time backfill path for legacy persisted
	// criteria carrying no judgment", but loadCompiledGoal bare-unmarshaled
	// straight from JSON with no schema pass at all, so a goal compiled
	// before ADR-080 D-TYPES existed (every criterion's Judgment field
	// empty) stayed with an empty Judgment forever — the claimed invariant
	// was never actually enforced for goals (only for task/plan store
	// writes, which route through normalizeCriteria on every Create/Update).
	// This is genuinely load-BEARING now, not just a compile-time nicety:
	// buildJudgeUserContent and the DoD-provenance echo both read Judgment
	// off these structs. A NormalizeCriteria failure is treated exactly
	// like the parse/zero-criteria corruption cases above — logged loud,
	// falls back (nil) — rather than handing the caller a goal that fails
	// the wire schema's own judgment/kind invariants downstream.
	normCriteria, cErr := task.NormalizeCriteria(g.Criteria)
	if cErr != nil {
		logger.WarnCF("agent", "goal: GoalCriteriaJSON's criteria failed NormalizeCriteria "+
			"(falling back to a single prose criterion from GoalCondition)",
			map[string]any{"error": cErr.Error()})
		return nil
	}
	g.Criteria = normCriteria
	normDoD, dErr := task.NormalizeCriteria(g.DoD)
	if dErr != nil {
		logger.WarnCF("agent", "goal: GoalCriteriaJSON's dod failed NormalizeCriteria "+
			"(falling back to a single prose criterion from GoalCondition)",
			map[string]any{"error": dErr.Error()})
		return nil
	}
	g.DoD = normDoD

	return &g
}

// compiledGoalCriteriaFor adjudication returns the criteria ladder a goal round
// should be judged against: the compiled ladder when GoalCriteriaJSON is
// present (Phase 2), else a single prose criterion synthesized from condition
// (back-compat with pre-Phase-2 /goal sessions). This is the single point at
// which the goal loop reads its criteria — DoD-11 (one implementation).
//
// ADR-080 D-DOD's "judged-set union seam" (§120, the load-bearing part):
// when a compiled ladder exists, the returned slice is Criteria UNION DoD —
// every Definition-of-Done item rides alongside the acceptance criteria into
// JudgeCriteria (both callers, goal_triggers.go's runGoalAdjudication and
// goal_loop.go's own pre-Phase-2 amendment-diff seed, feed this straight into
// adjudication), so each DoD item gets its own per-criterion verdict exactly
// like an acceptance criterion and an unmet DoD item fails the round via the
// SAME dedupeJudgeCriteriaAnyUnmetWins path (verifier_adjudication.go) —
// there is no separate DoD adjudication call, no separate Judge feed, and no
// change to JudgeCriteria/runVerifierAdjudication's own shape: DoD items are
// AcceptanceCriterion-shaped (KindProse) and simply widen in.Criteria. IDs
// never collide (normalizeCriteria mints a fresh UUID per item; the fixed
// floor-DoD IDs — newFloorDoD — are namespaced "goal-dod-floor-*", disjoint
// from any criterion ID). Without this union the DoD would be defined,
// confirmed, and never scored (ADR-080 R-C1).
func compiledGoalCriteriaFor(rawJSON, condition, sessionID string) []task.AcceptanceCriterion {
	if g := loadCompiledGoal(rawJSON); g != nil {
		if len(g.DoD) == 0 {
			return g.Criteria
		}
		union := make([]task.AcceptanceCriterion, 0, len(g.Criteria)+len(g.DoD))
		union = append(union, g.Criteria...)
		union = append(union, g.DoD...)
		return union
	}
	if strings.TrimSpace(condition) == "" {
		return nil
	}
	return []task.AcceptanceCriterion{{
		ID: "goal-condition", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: condition,
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: sessionID},
		Status: task.CritPending,
	}}
}

// isGoalConfirmVerb reports whether args (text after "/goal ") is a confirmation
// alias (FR-113/D11 — the chat reply that activates an echoed pending goal or
// applies a pending amendment). Distinct from the clear aliases.
func isGoalConfirmVerb(args string) bool {
	return IsGoalConfirm(args)
}
