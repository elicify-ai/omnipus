// Omnipus — per-request "consider your skills" reminder (ADR-072 D3)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// skill_reminder.go implements ADR-072 D3 part 2: a one-line reminder
// emitted per request, in the dynamic context block
// (pkg/agent/context.go::buildDynamicContext), which is already rebuilt on
// every turn and already sits AFTER the cached prefix — closer to the user's
// message than the "# Skills" menu, which stays in the cached static prompt
// (BuildSystemPrompt) per ADR-071 D5's reasoning about what may sit inside a
// cached prefix. A single reminder at session start decays across a long
// conversation; this placement is what buys recency instead.
//
// See docs/internal/architecture/ADR-072-skill-activation-and-loading.md D3
// and docs/internal/specs/skill-activation-and-loading-spec.md FR-013,
// FR-014, FR-015.
package agent

// skillReminderNote is the ADR-072 D3 per-request reminder. Wording states
// the EFFECT, not the mechanism (D3 part 2): check the "# Skills" list for a
// matching description, then call Skill — and do it silently (D3 part 3 /
// FR-015: the agent must never narrate "let me consider available
// skills…", this is a silent habit, not a step to report to the user).
//
// Budget: <=240 bytes, HARD (FR-013, MIN-001 corrected in ADR r4). The unit
// is bytes, not tokens: this text lands OUTSIDE the prompt-cache breakpoint
// on every single turn (see buildDynamicContext below), the position the
// ADR's §1.2 cost discussion is about — so "one line" alone is not a
// checkable budget. No tokenizer exists anywhere in this codebase
// (confirmed during the spec's review round: no TokenCount/EstimateTokens/
// CountTokens/tiktoken), and the only measurement infrastructure this ADR
// itself cites (static_chars/total_chars in context.go's BuildMessages) is
// len() over a string, i.e. a byte count — so bytes is the normative,
// testable unit. ~30 tokens remains the *design intent* behind the number,
// a review-time judgement rather than an assertion any test makes.
// TestReminder_WithinByteBudget pins this at build time.
const skillReminderNote = `When starting a new task or acting on a new request, check the "# Skills" list for one whose description matches, call Skill before proceeding, and do this silently -- never narrate it.`

// maxSkillReminderBytes is FR-013's hard byte ceiling.
const maxSkillReminderBytes = 240
