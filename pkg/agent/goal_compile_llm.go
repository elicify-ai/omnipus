// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_compile_llm.go implements ADR-074 D4a's LLM compilation turn for prose
// and mixed `/goal` intents (judgment-first spec US-3, FR-006/FR-007):
//
//   - Marker-only goals never reach this file — applyGoalCommandPrompt keeps
//     today's deterministic, immediate, zero-LLM path for them (US-3 S3).
//   - A prose/mixed goal runs ONE bounded provider call under the goal-bearing
//     agent's own identity/model, in its own fresh context (never a
//     spawnSubTurn — no delegation semantics), with the `define-done` skill
//     content injected engine-side when the seeded file exists. Output is
//     schema-forced JSON: exactly one of {criteria[], clarifying_question}.
//   - INV-1 (MUST): the LLM authors PROSE criteria only. Markers inside a
//     mixed intent are compiled deterministically by the same parseIntentMarkers
//     call compileGoalIntent uses (byte-identical payloads); the response
//     parser hard-rejects any criterion entry carrying a technical key
//     (kind/check/behavior/...), so a misbehaving model cannot smuggle a
//     technical payload past the confirmation surface.
//   - The D9 feasibility gate is UNCHANGED and still last, still the only net:
//     a gate veto feeds the reason back for exactly ONE bounded repair attempt
//     (FR-007); a second veto falls back to the deterministic parser. Every
//     fallback still ends at the pending+confirm gate; the deterministic
//     fallback may itself reject — always with a plain-language message.
//   - LLM failure/timeout is observable, never silent (FR-014): a WARN log
//     with the reason plus the goal_compile_fallbacks_total structured field,
//     and one echo line noting no quality-bar rewrite happened.
//
// Every call is bounded (goalCompileCallTimeout) per the goalJudgeRoundTimeout
// precedent (goal_loop.go): a synchronous call inside the interactive turn
// must never hang live chat. Episode budget is ≤ 4 LLM calls: compile, repair,
// resumed compile, resumed repair (FR-007).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// goalCompileCallTimeout bounds ONE goal-compile provider call (ADR-074 D4a,
// FR-007 "every call bounded per the round-timeout precedent"). A var (not
// const) so tests can substitute a short bound without a real multi-minute
// wait — mirrors goalJudgeRoundTimeout's own rationale (goal_loop.go): this
// call runs SYNCHRONOUSLY inside the user's interactive turn, so an unbounded
// provider hang would hang live chat.
var goalCompileCallTimeout = 2 * time.Minute //nolint:gochecknoglobals

// goalCompileFallbacks counts deterministic-fallback compiles (FR-014, spec
// US-3 S4): incremented every time a prose/mixed `/goal` could not complete
// its LLM compile (provider error/timeout, unparseable output, second
// feasibility veto, question-round overflow, no agent instance) and fell back
// to the deterministic parser. Log-based observability: the WARN emitted at
// each increment carries the running total under the
// "goal_compile_fallbacks_total" structured field — no new metrics endpoint.
var goalCompileFallbacks atomic.Uint64 //nolint:gochecknoglobals

// goalCompileFallbacksTotal exposes the counter (tests, future status surfaces).
func goalCompileFallbacksTotal() uint64 { return goalCompileFallbacks.Load() }

// goalClarificationRecord is the pending-clarification record persisted as
// session.MetaPatch.GoalClarificationJSON (US-3 S7): the original intent plus
// the compiler's single clarifying question. The user's next ordinary chat
// message answers it, feeding ONE resumed compile; `/goal clear` or a fresh
// `/goal <intent>` discards it. Max one question round per episode.
type goalClarificationRecord struct {
	Intent   string `json:"intent"`
	Question string `json:"question"`
	AskedAt  string `json:"asked_at,omitempty"`
}

// marshalGoalClarification serializes a clarification record for session meta.
func marshalGoalClarification(r *goalClarificationRecord) (string, error) {
	if r == nil {
		return "", nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// loadGoalClarification deserializes GoalClarificationJSON. Returns nil for
// an empty value; logs loud (mirroring loadCompiledGoal's corrupt-vs-absent
// distinction) for a non-empty value that fails to parse.
func loadGoalClarification(raw string) *goalClarificationRecord {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var r goalClarificationRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		logger.WarnCF("agent", "goal: GoalClarificationJSON is non-empty but failed to parse (discarding)",
			map[string]any{"error": err.Error()})
		return nil
	}
	if strings.TrimSpace(r.Intent) == "" {
		return nil
	}
	return &r
}

// goalCompileResponse is the schema-forced oneOf the compile call must return
// (US-3 S7): EXACTLY ONE of Criteria / ClarifyingQuestion. Criteria carry
// prose text only (INV-1 — parseGoalCompileResponse enforces it).
type goalCompileResponse struct {
	Criteria           []string
	ClarifyingQuestion string
}

// errGoalCompileSchema wraps every INV-1/oneOf violation so callers can treat
// schema misses uniformly as "the LLM did not produce a usable compile".
var errGoalCompileSchema = errors.New("goal compile response violates the compile schema")

// parseGoalCompileResponse parses + schema-enforces one compile call's output
// (INV-1, spec test 11c). Rejects: no JSON object; both or neither of
// criteria/clarifying_question; any criteria entry that is not an object with
// EXACTLY a non-empty "text" string — in particular any technical key (kind,
// check, behavior, command, tool, min_count, ...) is a hard schema error, so
// LLM output is constrained to prose criteria ONLY.
func parseGoalCompileResponse(raw string) (*goalCompileResponse, error) {
	jsonStr := extractJSONFromText(raw)
	if jsonStr == "" {
		return nil, fmt.Errorf("%w: no JSON object found in compile output", errGoalCompileSchema)
	}
	var probe struct {
		Criteria           []map[string]json.RawMessage `json:"criteria"`
		ClarifyingQuestion string                       `json:"clarifying_question"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &probe); err != nil {
		return nil, fmt.Errorf("%w: %w", errGoalCompileSchema, err)
	}
	hasCriteria := len(probe.Criteria) > 0
	hasQuestion := strings.TrimSpace(probe.ClarifyingQuestion) != ""
	if hasCriteria == hasQuestion { // both set, or neither
		return nil, fmt.Errorf("%w: exactly one of criteria / clarifying_question is required", errGoalCompileSchema)
	}
	if hasQuestion {
		return &goalCompileResponse{ClarifyingQuestion: strings.TrimSpace(probe.ClarifyingQuestion)}, nil
	}
	out := &goalCompileResponse{Criteria: make([]string, 0, len(probe.Criteria))}
	for i, entry := range probe.Criteria {
		for key := range entry {
			if key != "text" {
				return nil, fmt.Errorf(
					"%w: criterion %d carries key %q — the compiler may author prose text only (INV-1); "+
						"technical criteria come exclusively from the deterministic marker parser",
					errGoalCompileSchema, i+1, key)
			}
		}
		var text string
		if rawText, ok := entry["text"]; ok {
			if err := json.Unmarshal(rawText, &text); err != nil {
				return nil, fmt.Errorf("%w: criterion %d text is not a string", errGoalCompileSchema, i+1)
			}
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, fmt.Errorf("%w: criterion %d has empty text", errGoalCompileSchema, i+1)
		}
		out.Criteria = append(out.Criteria, text)
	}
	return out, nil
}

// defineDoneSkillPath returns the seeded define-done skill file's path under
// the runtime skills root ($OMNIPUS_HOME/skills — the SeedDefaults
// destination, pkg/gateway boot). ADR-074 D4/D4a: the quality bar is injected
// ENGINE-SIDE into every goal compile, independent of the goal-bearing
// agent's own skill allowlist.
func defineDoneSkillPath() string {
	home := config.OmnipusHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "skills", "define-done", "SKILL.md")
}

// loadDefineDoneSkillContent reads the seeded define-done skill content when
// the file exists; "" when it does not (the compile proceeds without the
// quality bar — the skill ships via ADR-074 D4's own rollout slot).
func loadDefineDoneSkillContent() string {
	p := defineDoneSkillPath()
	if p == "" {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// buildGoalCompileMessages assembles the compile call's fresh context: a
// system message carrying the compile contract (+ the define-done quality bar
// when seeded), and one user message carrying the intent's prose remainder
// (plus, on a resumed compile, the clarifying question and the user's answer;
// plus, on a repair call, the feasibility-gate rejection to repair around).
func buildGoalCompileMessages(prose, question, answer, repairReason string) []providers.Message {
	var sys strings.Builder
	sys.WriteString(
		"You compile a user's goal statement into acceptance criteria a reviewer will later judge.\n" +
			"Respond with ONLY a JSON object, no prose around it, in exactly one of these two shapes:\n" +
			"  {\"criteria\": [{\"text\": \"...\"}, ...]}   — the compiled criteria, or\n" +
			"  {\"clarifying_question\": \"...\"}          — ONE question, only when success is genuinely ambiguous.\n" +
			"Each criterion is plain language: an observable outcome that is specific enough to fail.\n" +
			"Never invent shell commands, tool names, counts, or any technical payload — those are added\n" +
			"only by the user's own explicit markers, which are handled outside this call.\n")
	if bar := loadDefineDoneSkillContent(); bar != "" {
		sys.WriteString("\nQuality bar for the criteria you author (define-done):\n")
		sys.WriteString(bar)
		sys.WriteString("\n")
	}

	var usr strings.Builder
	usr.WriteString("Goal statement to compile:\n")
	usr.WriteString(prose)
	usr.WriteString("\n")
	if question != "" {
		fmt.Fprintf(&usr, "\nYou previously asked: %s\nThe user answered: %s\n"+
			"Compile the criteria now — do not ask another question.\n", question, answer)
	}
	if repairReason != "" {
		fmt.Fprintf(&usr, "\nYour previous criteria were rejected by the feasibility gate:\n%s\n"+
			"Produce a corrected set of criteria that avoids this problem.\n", repairReason)
	}
	return []providers.Message{
		{Role: "system", Content: sys.String()},
		{Role: "user", Content: usr.String()},
	}
}

// goalCompileLLMCall runs one bounded compile call on the goal-bearing
// agent's OWN provider/model (read together under the instance mutex so a
// concurrent model switch is never observed torn — the ADR-032 model-quad
// rule). Cost lands on the agent's own provider credentials; cancellation
// follows the turn ctx.
func goalCompileLLMCall(ctx context.Context, agentInst *AgentInstance, messages []providers.Message) (string, error) {
	if agentInst == nil {
		return "", errors.New("no agent instance for goal compile")
	}
	agentInst.mu.RLock()
	provider := agentInst.Provider
	model := agentInst.Model
	agentInst.mu.RUnlock()
	if provider == nil {
		return "", errors.New("goal-bearing agent has no provider")
	}
	callCtx, cancel := context.WithTimeout(ctx, goalCompileCallTimeout)
	defer cancel()
	resp, err := provider.Chat(callCtx, messages, nil, model, nil)
	if err != nil {
		return "", err
	}
	if resp == nil || strings.TrimSpace(resp.Content) == "" {
		return "", errors.New("goal compile returned no content")
	}
	return resp.Content, nil
}

// llmGoalCompileOutcome is what compileGoalIntentLLM hands back to
// applyGoalCommandPrompt / the clarification-resume path.
type llmGoalCompileOutcome struct {
	// Result carries the compiled goal or a (plain-language) rejection —
	// mutually exclusive with ClarifyingQuestion.
	Result CompileResult
	// ClarifyingQuestion is non-empty when the compile paused on its single
	// question round (US-3 S7). No criteria exist yet in that case.
	ClarifyingQuestion string
	// UsedFallback reports the deterministic parser produced Result (LLM
	// failure/timeout/schema miss/second veto/question overflow) — the echo
	// gains the FR-014 "no quality-bar rewrite" note.
	UsedFallback bool
	// FallbackReason is the WARN-logged reason when UsedFallback.
	FallbackReason string
}

// noteGoalCompileFallback increments the FR-014 counter and emits the
// structured WARN (log-based observability — the on-call reader greps
// "goal_compile_fallbacks_total").
func noteGoalCompileFallback(sessionID, reason string) {
	total := goalCompileFallbacks.Add(1)
	logger.WarnCF("agent", "goal: LLM compile fell back to the deterministic parser",
		map[string]any{
			"session_id":                   sessionID,
			"reason":                       reason,
			"goal_compile_fallbacks_total": total,
		})
}

// compileGoalIntentLLM is the D4a two-phase compiler for prose/mixed intents.
// question/answer are empty for an initial compile; a resumed compile (the
// clarification answer arrived) passes both and gets its OWN single repair
// attempt but may NOT ask again (max one question round — a second question
// falls back). Callers have already verified the intent is not marker-only
// and that admission passed (US-3 S1/S6).
func (al *AgentLoop) compileGoalIntentLLM(
	ctx context.Context,
	agentInst *AgentInstance,
	fc FeasibilityContext,
	intent, sessionID, question, answer string,
) llmGoalCompileOutcome {
	fallback := func(reason string) llmGoalCompileOutcome {
		noteGoalCompileFallback(sessionID, reason)
		return llmGoalCompileOutcome{
			Result:         compileGoalIntent(intent, fc, sessionID),
			UsedFallback:   true,
			FallbackReason: reason,
		}
	}

	// EC-4: no agent instance (or no provider) → deterministic fallback, zero
	// LLM calls. Still ends at the pending+confirm gate like every prose path.
	if agentInst == nil {
		return fallback("no agent instance available for the compile call")
	}

	// Markers compile deterministically and byte-identically (INV-1): the SAME
	// parseIntentMarkers call compileGoalIntent itself uses.
	markerCriteria, prose, _ := parseIntentMarkers(intent, sessionID)
	if prose == "" {
		prose = intent // defensive: callers gate on a non-trivial prose remainder
	}

	assemble := func(proseTexts []string) CompileResult {
		criteria := make([]task.AcceptanceCriterion, 0, len(markerCriteria)+len(proseTexts))
		criteria = append(criteria, markerCriteria...)
		author := task.CriterionAuthor{Kind: task.AuthorKindUser, ID: sessionID}
		for _, text := range proseTexts {
			criteria = append(criteria, task.AcceptanceCriterion{
				Kind: task.KindProse, Text: text, Author: author,
			})
		}
		normalized, err := task.NormalizeCriteria(criteria)
		if err != nil {
			return CompileResult{Rejection: &FeasibilityRejection{
				CriterionIndex: -1, Reason: "compiled criterion failed shape validation: " + err.Error(),
			}}
		}
		// Feasibility gate: unchanged, still last, still the only net (FR-007).
		if fc != nil {
			if rej := feasibilityGate(normalized, fc); rej != nil {
				return CompileResult{Rejection: rej}
			}
		}
		if len(normalized) == 0 {
			return CompileResult{Rejection: &FeasibilityRejection{
				CriterionIndex: -1, Reason: goalNoCriteriaRejectionReason,
			}}
		}
		return CompileResult{Goal: &CompiledGoal{Intent: intent, Prompt: prose, Criteria: normalized}}
	}

	// One compile call, then (on a feasibility veto) exactly one repair call.
	repairReason := ""
	for call := 0; call < 2; call++ {
		content, err := goalCompileLLMCall(ctx, agentInst, buildGoalCompileMessages(prose, question, answer, repairReason))
		if err != nil {
			return fallback("compile call failed: " + err.Error())
		}
		parsed, perr := parseGoalCompileResponse(content)
		if perr != nil {
			return fallback("compile output rejected: " + perr.Error())
		}
		if parsed.ClarifyingQuestion != "" {
			if answer != "" || repairReason != "" {
				// Max ONE question round per episode (US-3 S7): a question from
				// the resumed compile — or from a repair call — is out of budget.
				return fallback("compiler asked a question outside its single question round")
			}
			return llmGoalCompileOutcome{ClarifyingQuestion: parsed.ClarifyingQuestion}
		}
		res := assemble(parsed.Criteria)
		if res.Rejection == nil {
			return llmGoalCompileOutcome{Result: res}
		}
		if repairReason != "" {
			// Second veto → deterministic fallback (US-3 S5). The fallback may
			// itself reject — always plain-language, never silent.
			return fallback("feasibility gate rejected the repaired criteria: " + res.Rejection.Reason)
		}
		repairReason = res.Rejection.Reason
	}
	// Unreachable: the loop always returns on the second pass. Kept for the
	// compiler's exhaustiveness.
	return fallback("compile loop exhausted")
}
