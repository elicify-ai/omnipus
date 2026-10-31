// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_compile_llm.go assembles shared goal-authoring guidance for working
// agent turns and loads the seeded define-goal skill.
package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// defineGoalSkillPath returns the seeded define-goal skill file's path under
// the runtime skills root ($OMNIPUS_HOME/skills — the SeedDefaults
// destination, pkg/gateway boot). ADR-074 D4/D4a: the quality bar is injected
// ENGINE-SIDE into every goal compile, independent of the goal-bearing
// agent's own skill allowlist. ADR-080 D-SKILL renamed the skill
// define-done→define-goal; the path below reflects the rename.
func defineGoalSkillPath() string {
	home := config.OmnipusHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "skills", "define-goal", "SKILL.md")
}

// loadDefineGoalSkillContent reads the seeded define-goal skill content when
// the file exists; "" when it does not (the compile proceeds without the
// quality bar — the skill ships via ADR-074 D4's own rollout slot).
//
// Fix-wave finding #7: os.IsNotExist is the ONLY expected/silent case (a
// pre-rollout install, or an install where SeedDefaults simply hasn't run
// yet) — every other read error (permission denied, a corrupt/unreadable
// file, an I/O error) is WARN-logged once per call, mirroring
// goalCompileWindowText's own logging pattern above, so a mis-permissioned
// or corrupt SKILL.md is observable rather than silently indistinguishable
// from "not seeded yet". This is what makes finding #1's downstream
// (deleteOrphanedDefineDoneDir deleting define-done/ while define-goal/ is
// unreadable) visible in the logs rather than a silent quality-bar loss.
func loadDefineGoalSkillContent() string {
	p := defineGoalSkillPath()
	if p == "" {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.WarnCF("agent", "goal compile: could not read the seeded define-goal skill "+
				"(compiling without the quality-bar rewrite)",
				map[string]any{"path": p, "error": err.Error()})
		}
		return ""
	}
	return strings.TrimSpace(string(data))
}

// buildGoalRubricNote returns the compile contract's rubric text: the
// clarity gate, the checklist-authoring guidance (judgment types, the
// 4-layer DoD derivation ladder), and the seeded define-goal skill content
// when present — WITHOUT ADR-079 D1's session-transcript window and WITHOUT
// ADR-080 D-CONTEXT2's workspace/project instructions (ADR-088 D4, spec
// FR-011). This is a single reusable builder so buildGoalCompileMessages
// (below, D7's fallback-only compile) and loop.go's D3/D4 turn-scoped
// rubric injection (the WORKING agent's own first-move guidance) can never
// drift apart on what the quality bar says. The window and
// workspace-instructions feeds are deliberately excluded: loop.go's
// injection call site already carries the session's own native message
// history and already injects the turn's own workspace instructions
// (injectWorkspaceInstructions) on every turn — re-including either here
// would double-inject.
//
// forTool selects the delivery-mechanism framing that opens the note — the
// two callers describe the SAME fields through two different surfaces, and
// conflating them would leave a tool-calling turn holding an instruction to
// emit raw JSON instead of calling a tool (D3 AMENDMENT, 2026-09-07: the
// tool-calling path used to inherit the raw-JSON framing verbatim, since
// provider tool-choice forcing carried the actual guarantee and the prompt
// text's exact wording didn't matter as much; now that the immediate
// post-turn correction is the enforcement point, the request itself must
// say the right thing):
//   - false — D7's compileGoalIntentLLM/goalCompileLLMCall, a raw no-tools
//     LLM call with no set_goal/AskUserQuestion available at all. Keeps the
//     original "respond with ONLY a JSON object" contract unchanged.
//   - true — loop.go's D3/D4 turn-scoped injection, where set_goal and
//     (webchat origin, budget permitting) AskUserQuestion are REAL callable
//     tools. Opens with an explicit first-move instruction instead: call
//     set_goal now, or ask once when genuinely unclear, then keep working in
//     the same turn.
//
// The shared substance below (criterion/DoD authoring guidance, the skill
// content) never differs — only how the model is told to deliver it.
func buildGoalRubricNote(forTool bool) string {
	var sys strings.Builder
	if forTool {
		sys.WriteString(
			"Your FIRST action this turn must be a tool call, not prose. When you are confident, against " +
				"the quality bar below, about the goal's restated statement, acceptance criteria, and " +
				"Definition of Done, call set_goal now (mode: \"register\") with those fields, then continue " +
				"straight into the work in this SAME turn — there is no confirmation step to wait for. When " +
				"the goal is genuinely unclear, ask once instead: call AskUserQuestion where it is offered, " +
				"or ask conversationally in your own reply where it is not, then call set_goal immediately " +
				"once the answer arrives, before doing anything else.\n\n" +
				goalOneDoorRule + "\n\n")
	} else {
		sys.WriteString(
			"You compile a user's goal into a restated statement, judgment-typed acceptance criteria, and a\n" +
				"Definition of Done (DoD) that a reviewer (the Judge) will later evaluate the work against.\n" +
				"Respond with ONLY a JSON object, no prose around it, in exactly one of these two shapes:\n\n" +
				"Clear:\n" +
				"  {\"assessment\":{\"clarity\":\"clear\"},\n" +
				"   \"definition\":\"<one clear sentence restating the goal>\",\n" +
				"   \"criteria\":[{\"text\":\"...\",\"judgment\":\"boolean\"|\"quantitative\"|\"artifact\"}, ...],\n" +
				"   \"dod\":[{\"text\":\"...\",\"judgment\":\"boolean\"|\"quantitative\"|\"artifact\"," +
				"\"provenance\":\"stated\"|\"workspace\"|\"floor\"|\"inferred\"}, ...]}\n\n" +
				"Ambiguous:\n" +
				"  {\"assessment\":{\"clarity\":\"ambiguous\"},\n" +
				"   \"clarifying_questions\":[\n" +
				"     {\"header\":\"<short unique tab label>\",\"question\":\"<the question text>\",\n" +
				"      \"options\":[{\"label\":\"...\",\"description\":\"...\"}, ... concrete answer options],\n" +
				"      \"multi_select\":true|false (optional, default false),\n" +
				"      \"recommended\":\"<one option's exact label, optional>\"},\n" +
				"     ... up to 10 questions in one round\n" +
				"   ]}\n\n" +
				fmt.Sprintf(
					"Every clarifying question needs a short unique header (max %d chars) and %d-%d concrete "+
						"answer options — real, specific candidate answers, never filler — even when the true "+
						"answer is open-ended: the user can always answer in free text instead, so the options are "+
						"a helpful starting menu, not an exhaustive list. Never set a \"default_safe\" field on a "+
						"clarifying question — these must always wait for the user's own answer.\n\n",
					askuser.MaxHeaderChars, askuser.MinOptions, askuser.MaxOptions,
				))
	}
	sys.WriteString(
		"Choose \"clear\" ONLY when you are confident, against the quality bar below, that every " +
			"criterion is unambiguous and no reasonable reader would disagree about what \"done\" means. " +
			"If scope, acceptance, or the user's meaning is genuinely ambiguous — including a goal that " +
			"only makes sense against earlier conversation you were not given enough of — treat it as " +
			"ambiguous and ask, instead of guessing.\n\n" +
			goalReferentRule + "\n\n" +
			"Definition: one clear sentence restating the goal, staying close to the setter's own words. " +
			"Shape: \"Produce <outcome> for <who/what it serves>, so that <the one observable end-state> " +
			"— <optional: by when / within a budget or attempt limit>.\" One primary outcome only (extra " +
			"outcomes become criteria); describe an observable end-state, not an activity; add a " +
			"time/effort bound ONLY if the request implies one; never assert the goal is achievable — " +
			"that is judged separately.\n\n" +
			"Each criterion is plain language: an observable outcome specific enough to fail, tagged with " +
			"EXACTLY one judgment kind:\n" +
			"  - \"boolean\" — a yes/no fact the reader can rule true or false. This is the default for any " +
			"honestly-subjective or yes/no outcome — never fabricate a number for a matter of taste.\n" +
			"  - \"quantitative\" — a value against a threshold or comparator.\n" +
			"  - \"artifact\" — a named produced/changed/sent thing whose existence is checkable.\n" +
			"Tag every criterion with exactly one judgment; if you cannot tag it, rewrite it until you can. " +
			"One thing per criterion — never a compound \"X and Y\" line.\n\n" +
			"Never invent shell commands, tool names, counts, or any other technical payload for a " +
			"criterion or DoD item — those are added only by the user's own explicit markers, handled " +
			"outside this call.\n\n" +
			"The Definition of Done (dod) is REQUIRED and must be non-empty: generic standing quality " +
			"gates distinct from the outcome-specific criteria above, each tagged with a judgment AND a " +
			"provenance layer. Derive it from these layers, highest authority first:\n" +
			"  1. \"stated\" — a quality gate the goal setter named explicitly in the goal statement.\n" +
			"  2. \"workspace\" — derived from the workspace/project instructions below, when they apply " +
			"to this goal's kind.\n" +
			"  3. \"floor\" — universal gates that always apply: no secrets or credentials in the output; " +
			"every factual claim is grounded, not assumed. Always include at least these when no higher " +
			"layer supplies enough — the DoD must never be empty.\n" +
			"  4. \"inferred\" — bounded, type-appropriate gates you infer beyond the above, for the " +
			"setter's approval — never silently invent a gate; use sparingly, only when clearly defensible.\n" +
			"Every dod item's text must be SELF-CONTAINED: restate any workspace convention detail it " +
			"relies on, since the reviewer will not see the workspace instructions again.\n")
	if bar := loadDefineGoalSkillContent(); bar != "" {
		sys.WriteString("\nQuality bar for the statement, criteria, and DoD you author (define-goal):\n")
		sys.WriteString(bar)
		sys.WriteString("\n")
	}
	return sys.String()
}

// goalReferentRule is the part of buildGoalRubricNote's shared substance that
// says WHERE a goal's referent may come from. UAT 2026-09-14 (B-1, five fresh
// sessions of "/goal improve it" on one agent): every run's context carried
// an agent-wide "# Current scratchpad" note (loop.go's buildScratchpadNote
// selects the agent's most recent open set_todos card regardless of which
// session or workspace wrote it). Runs 3 and 5 resolved "it" from that
// leftover checklist — run 5's own set_goal assumptions say the record was
// "reconstructed from the session scratchpad" — registered with clarity
// "clear", and did unrequested work, where the ambiguity instruction above
// should have produced a question. The ambiguity sentence alone did not stop
// this because the model treated the scratchpad as conversation it HAD been
// given. This rule names those surfaces as non-evidence explicitly.
const goalReferentRule = "Work out what the goal refers to (\"it\", \"that\", \"the report\") ONLY from what the " +
	"user has said in this conversation. A scratchpad checklist, board tasks, workspace files, or another " +
	"goal's record are NOT evidence of what the user means right now — they can belong to unrelated work in " +
	"another session. If the user's own words in this conversation do not identify the target, the goal is " +
	"ambiguous: ask before registering a record or doing any work."

// goalOneDoorRule sharpens the tool-calling first-move instruction: the two
// doors are alternatives, never a pair. UAT 2026-09-14 (W4 B-9 run 4,
// session_01M2FKG8R1Z2Z2XRDVXZ4RRYFT, 09:22:19Z): on a clear goal the model
// called set_goal AND AskUserQuestion in the SAME response, the question
// reading "Placeholder question - not used." with options "Proceed" /
// "Clarify". set_goal succeeded, but the card parked the turn until a human
// answered it 18 minutes later. The instruction "Your FIRST action this turn
// must be a tool call" plus a surface of exactly those two tools read, to
// that model, as "call the tools you were given".
const goalOneDoorRule = "Call exactly ONE of the two, never both in the same response. A question card pauses " +
	"the whole turn until it is answered, so only send one you genuinely need answered — never a " +
	"placeholder, a test question, or a request for permission to proceed on a goal that is already clear."

// goalRubricChannelAddendum extends buildGoalRubricNote's text on a
// non-webchat origin (ADR-088 D3 [G-B2] / D4): AskUserQuestion is
// permanently web-only (pkg/tools/ask_user_question.go), so a channel-origin
// goal turn gets the SAME rubric plus this one line telling it to ask
// conversationally instead of reaching for a card that would refuse.
const goalRubricChannelAddendum = "\n\nAskUserQuestion is unavailable on this channel (it is permanently " +
	"web-app only) — if you are not confident enough to register the record now, ask your clarifying " +
	"question conversationally, in plain language, as your normal reply, and register the record once " +
	"the operator answers.\n"

// buildGoalRubricInjectionNote returns the ADR-088 D4 turn-scoped system
// note loop.go injects on a goal turn: "" when holds is false (the D3 base
// predicate — active goal AND an empty compiled record — does not hold this
// request), buildGoalRubricNote(true) otherwise (the tool-calling framing —
// this is always the WORKING agent's own turn, never the D7 raw-JSON
// fallback compile), with goalRubricChannelAddendum appended when the turn
// did not originate on the web (isWebchat false).
func buildGoalRubricInjectionNote(holds, isWebchat bool) string {
	if !holds {
		return ""
	}
	note := buildGoalRubricNote(true)
	if !isWebchat {
		note += goalRubricChannelAddendum
	}
	return note
}

// injectGoalRubricNote appends request-only user text for the volatile tail.
// The note's wording is unchanged; only its request placement changes.
func injectGoalRubricNote(msgs []providers.Message, note string) []providers.Message {
	if note == "" || len(msgs) == 0 {
		return msgs
	}
	return appendVolatileNote(msgs, note)
}
