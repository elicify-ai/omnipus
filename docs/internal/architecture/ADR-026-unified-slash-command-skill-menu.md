# ADR-026: Unified Slash-Command + Skill Menu (skill-as-command, partitioned palette)

**Status:** Accepted (ratifying — decisions made in conversation 2026-06-29)
**Date:** 2026-06-29
**Branch:** `hotfix/v0.1.1`
**Author:** Daniel Piatkowski
**Supersedes in part:** the `/skill` autocomplete shipped in `a5142529` (its palette code is repurposed, not discarded)
**Decider:** operator (Daniel)
**Architect:** Albert (ratification mode)

---

## 1. Context

The slash-command system today has a discoverability and naming problem, verified in code:

- **`[FACT]`** Two near-identical commands exist: `/skills` (plural — *list* installed skills; `pkg/commands/cmd_skills.go`, surfaces CLI+Channel only) and `/skill` (singular, alias `/use` — *activate* a skill; `pkg/commands/cmd_use.go`, all surfaces). They differ by one trailing `s` and do different things — a fat-finger hazard, and `/skills` silently does nothing in the web chat (it's not web-surfaced).
- **`[FACT]`** Skill **activation** in the web chat is via `/skill <name> [message]`, parsed by the agent loop (`pkg/agent/loop.go::applyExplicitSkillCommand`, ~8377, which matches `cmdName == "use"`). Skills are **eager-injected** when active: a summary is always in the system prompt; the full `SKILL.md` body is injected on activation (`pkg/agent/context.go::buildActiveSkillsContext`, `pkg/skills/loader.go::LoadSkillsForContext`). Skill identity = the directory **slug** (`Skill.id`).
- **`[FACT]`** The web composer has an API-driven slash palette (`src/components/chat/ChatScreen.tsx`, fetched via `GET /api/v1/commands?surface=web`), recently extended with `/skill <name>` argument autocomplete (commit `a5142529`). There is **no skill discovery in chat** beyond that; the SPA's Skills *screen* is the only browse surface.
- **`[FACT]`** `/agents` exists but is **CLI/Channel-only** (`pkg/commands/cmd_agents.go`, "not web — the Agents screen covers this in the SPA"). `/help` exists on all surfaces and lists canonical commands (`pkg/commands/cmd_help.go::formatHelpMessage`).
- **`[FACT]`** `/model` is a web **client** command that opens the chat-header `ModelSelector` by flipping a UI-store flag (`useUiStore.setModelSelectorOpen(true)`, `ChatScreen.tsx:~1204`; `ChatControls.tsx`). The chat header also has an **agent** selector (`ChatControls.tsx::handleAgentSelect`), but no UI-store open-flag for it yet.
- **`[FACT]`** The command registry supports **hidden/deprecated** entries that still execute but are excluded from menus/help (`pkg/commands/definition.go::Hidden`; `pkg/commands/builtin.go` keeps `start/show/list/switch/check` hidden for one-release back-compat with a `TODO(v0.2)`).

**Problem to solve:** collapse the `/skill`/`/skills` confusion into one mental model, make skills **discoverable and runnable inline** from the chat, bring agent-switching into the slash surface, and make `/help` genuinely useful — without breaking the one-shot CLI (which is non-conversational and cannot host an interactive menu).

## 2. Decision

Adopt **skill-as-command** with a **partitioned slash menu**, web-focused. Ten ratified decisions:

| # | Decision |
|---|----------|
| **D1** | **Remove `/skill` and its `/use` alias — hard removal**, this change (no hidden-deprecation period). |
| **D2** | **A skill's name *is* its command:** `/<skill-id> [message]` activates that skill. `/<skill-id>` alone arms it for the next message; `/<skill-id> <message>` forces it for that turn (preserves the existing arm/force semantics, just keyed on the skill slug). |
| **D3** | **Built-ins win on name collision.** Command resolution checks built-in commands FIRST; a skill whose slug equals a built-in (`help`, `clear`, `model`, `agents`, `skills`, `cancel`, …) cannot shadow it. Skills resolve only for non-built-in `/<name>` tokens. |
| **D4** | **Unknown `/<x>`** (neither a built-in nor an installed skill) → **sent as a normal chat message** (no error, nothing blocked). `/` is not a reserved prefix. |
| **D5** | **Web `/` menu is partitioned:** a **Commands** section and a **Skills** section, each filtering live as the user types. (Extends the existing palette + the `a5142529` skill autocomplete.) |
| **D6** | **Selecting a skill** inserts `/<skill-id> ` as real text **plus an inline ghost-text `<message>` placeholder** — muted/lower-contrast, NOT part of the input value, cleared the instant the user types. Implemented as a positioned overlay (native textarea placeholders only show when empty). |
| **D7** | **`/agents` opens the existing chat-header agent selector** — the same way `/model` opens `ModelSelector` — via a new `agentSelectorOpen` UI-store flag mirroring `modelSelectorOpen`. It is a **web client command** (add `SurfaceWeb`); CLI/Channel keep the existing list-reply. |
| **D8** | **Switching agents matches the existing agent picker's behavior** (`ChatControls::handleAgentSelect`). `[INFERENCE]` that path goes through `startNewSession`, i.e. switching starts a new session for the chosen agent; `/agents` MUST reuse that exact flow, not invent a new one. |
| **D9** | **`/skills` on web filters the open `/` menu to the Skills section** (a "show me my skills" shortcut). It remains a text-list reply on CLI/Channel. |
| **D10** | **`/help` lists commands only** (not skills). Keep the existing `formatHelpMessage`; ensure it renders cleanly inline on web. |

**Out of scope (explicit):** the interactive numbered "type a number / Esc" CLI menu is **dropped**. The one-shot CLI (`omnipus <agent> "prompt"`) is non-conversational and cannot pause for input; skill activation there works by typing `/<skill-id> …` in the prompt (D2 makes this work on every surface), and `/skills` stays a text list. Channels are untouched this round (existing `/skills` text list; `/<skill-id>` activation works).

## 3. Options Considered (rejected alternatives, one line each)

- **D1 — keep `/skill`/`/use` as hidden deprecated aliases for one release** (the `builtin.go` pattern). *Rejected:* operator chose a clean break; D2 makes `/skill X` redundant and D4 means a stale `/skill X` simply sends as text (no crash).
- **D2 — keep `/skill <name>` internally, only *display* `/<skillname>`.** *Rejected:* two grammars for one action; skill-as-command is the simpler mental model and removes the singular/plural collision at the root.
- **D6 — muted hint below the input, or a skill "chip" + native placeholder.** *Rejected:* operator wants the hint inline after the command, in the field.
- **D7 — a third "Agents" partition in the `/` menu, or navigate to the full Agents screen.** *Rejected:* operator wants parity with the `/model` switcher (open the existing in-header selector), not a new menu section or a context switch.
- **D9 — drop `/skills` from web entirely, or post a plain text list inline.** *Rejected:* filtering the rich menu keeps the word meaningful and matches the inline experience.
- **D10 — `/help` lists commands *and* skills.** *Rejected:* operator scoped help to commands; skills are discoverable via the `/` menu's Skills section.
- **CLI — build an interactive prompt into the one-shot runner.** *Rejected:* contradicts the one-shot design; unnecessary given D2.

## 4. Consequences

**Positive**
- One mental model: every `/<token>` is "a thing you can run" — built-in command, or skill, or (unknown) literal text.
- Skills become discoverable + runnable inline in chat (the gap the SPA Skills screen didn't fill for the composer).
- `/agents` joins `/model` as a first-class in-chat switcher; consistent UX.
- Removes the `/skill` vs `/skills` footgun at the source.

**Negative / costs**
- **Backend parser change** (`applyExplicitSkillCommand`) from a fixed `/use` token to "resolve `/<name>` against built-ins then installed skills" — must preserve arm-vs-force semantics and be collision-safe (D3).
- **Hard removal (D1)** breaks any muscle-memory/docs/scripts using `/skill X` immediately (mitigated only by D4's no-error fallback).
- **Inline ghost-text (D6)** is the most finicky front-end work (overlay aligned to the textarea's font/padding/caret; must not interfere with IME, wrapping, or selection).
- **New `agentSelectorOpen` UI flag (D7)** + wiring the header agent selector to open programmatically.
- The `/skills` list handler message still says *"Use /skill <skill> …"* (`cmd_skills.go:35`) — **must be updated** to `/<skillname>`.
- Net contract surface: `/skills`, `/agents` gain `SurfaceWeb`; `/skill`/`/use` removed from `GET /commands`. SPA command-palette consumers already API-driven, so no hardcoded list to chase.

## 5. Per-Decision Confidence (Rule 5)

| Decision | Confidence | Basis / Missing evidence |
|----------|-----------|--------------------------|
| D2 skill-as-command | **High** | Grounded in the existing arm/force parser; slug is already the identity. Missing: none material. |
| D3 built-ins-win | **High** | Standard precedence rule; trivially testable. |
| D4 unknown→message | **High** | Matches existing "insert as text" fallback (`runClientCommand` returns false → composer keeps text). |
| D5 partitioned menu | **High** | Extends code shipped in `a5142529`; low risk. |
| D6 inline ghost-text | **Medium** | UX is clear; *implementation* risk is real (textarea overlay). Confidence improves once a spike confirms the overlay approach (mirror-div vs absolutely-positioned span). |
| D7 `/agents` = open header selector | **Medium-High** | `/model` precedent is exact; missing: the header agent selector currently has no open-flag — needs `agentSelectorOpen` added and the selector made controllable. |
| D8 switch = match existing | **Medium** | `[INFERENCE]` the existing path uses `startNewSession` (new session per agent). Confidence improves by reading `handleAgentSelect`'s full body before wiring `/agents`. |
| D1 hard-remove | **High** (operator-chosen) | Trade-off explicitly accepted. |
| D9 `/skills`→filter | **High** | Pure front-end menu filter. |
| D10 `/help` commands-only | **High** | `formatHelpMessage` already does this. |

## 6. Risks

- **R1 — ghost-text overlay correctness.** Misaligned/leaking placeholder is visually broken. *Mitigation:* spike the overlay first; fall back to D6-alt (hint below input) only with operator sign-off.
- **R2 — parser ambiguity / collision.** A skill named like a built-in, or a `/<name>` that's a *prefix* of a skill. *Mitigation:* D3 precedence + exact-slug match; unit-test the matrix (built-in / exact-skill / unknown / prefix).
- **R3 — hard removal surprises (D1).** `/skill foo` silently sends as a message. *Mitigation:* accepted; `/help` + the menu surface the new way; release note.
- **R4 — agent-switch session semantics.** If `/agents` starts a new session but the user expected continuity, that's data-loss-of-context surprise. *Mitigation:* D8 — reuse the exact existing flow; do not diverge.
- **R5 — regression in the heavily-tested composer.** *Mitigation:* the full `src/components/chat/` vitest dir must stay green (806 tests today); wave-pattern review gate.

## 7. Gaps & Ambiguities

- **G1** `[UNKNOWN]` the exact body of `ChatControls::handleAgentSelect` (new-session vs continue) — resolve by reading it during plan-spec; D8 binds the implementation to whatever it is.
- **G2** Ghost-text mechanism not yet chosen at the code level (absolutely-positioned span vs mirror-div measuring text width). A 30-minute spike resolves it; plan-spec should require it before estimating D6.
- **G3** `/skills`-filters-the-menu (D9): define precisely whether it pre-filters to Skills *and* hides Commands, or just scrolls/anchors. Plan-spec decision.
- **G4** Channels: D2 changes the activation token; the `cmd_skills.go` reply text and any channel docs referencing `/skill`/`/use` need updating even though channels are otherwise out of scope.
- **G5** Does selecting a skill then sending with an **empty** message (user deletes the ghost) send `/<skill-id>` alone (= arm for next message)? Expected yes per D2; confirm in plan-spec.

## 8. Non-Functional Requirements

- **Maintainability:** commands stay API-driven (no hardcoded SPA list — keep `ChatScreen.no-hardcoded-commands.test.ts` green). Contract-first for any new wire field (none anticipated; skills/commands/agents endpoints already exist).
- **Performance:** skills fetched lazily (only when the menu/skill-arg is active), matching the existing 60s-stale commands query.
- **Accessibility:** the partitioned menu + ghost-text must preserve keyboard nav (Arrow/Enter/Esc) and not break screen-reader semantics of the textarea.
- **Backward-compat:** none preserved for `/skill`/`/use` (D1, accepted).

## 9. Next Steps / Handoff

This is an architecture-level ratification. The decisions are settled; the open items (G1–G5, the ghost-text spike) are **implementation** questions for the spec.

**Handoff:** run

```
/plan-spec docs/internal/architecture/ADR-026-unified-slash-command-skill-menu.md
```

to produce the implementation-ready spec (BDD scenarios + TDD plan + traceability), covering: the backend parser change (built-in→skill→message resolution matrix), the four web behaviors (partitioned menu, ghost-text, `/agents` switcher, `/skills` filter, `/help`), the `cmd_skills.go` text fix, and the regression guard on the composer test suite. Optionally `/grill-spec` this ADR first if you want the hard-removal (D1) and ghost-text (D6) decisions red-teamed before speccing.

Then implement via the wave pattern (parallel agents → 7-reviewer gate → fix → test → redeploy to the preview).
