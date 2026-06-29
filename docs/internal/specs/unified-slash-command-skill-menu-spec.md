# Feature Specification: Unified Slash-Command + Skill Menu (skill-as-command)

**Created**: 2026-06-29
**Status**: Draft
**Branch**: `hotfix/v0.1.1`
**Input**: [ADR-026](../architecture/ADR-026-unified-slash-command-skill-menu.md) (Accepted, ratifying). All 10 decisions D1–D10 are LOCKED — this spec realizes them; it does not re-open them.

---

## Summary

Collapse the confusing `/skill` (activate) vs `/skills` (list) pair into one model: **a skill's slug *is* its command** (`/<skill-id> [message]`). Make skills discoverable and runnable inline in the web chat via a **partitioned `/` menu** (Commands · Skills), with an **inline ghost-text `<message>` placeholder** after a skill is picked. Bring agent-switching onto the slash surface (`/agents` opens the existing in-header agent selector, like `/model`). `/help` lists commands only. `/skill` and `/use` are hard-removed. The one-shot CLI gets no interactive menu (skills work by typing `/<skill-id> …`).

---

## Revision 1 — Claude-Code-aligned activation (2026-06-29)

> After researching Claude Code as the reference UX (see *Claude Code Alignment*), the operator refined three points. **This block is authoritative and supersedes any conflicting arm/force language below** (in D2, US1, US4.4, the resolution matrix, FRs, datasets — read "arm" there as "run-now / one-shot").

**R1 — Activation is ONE-SHOT (drop arming).** Like a Claude Code slash command, activating a skill runs it **on the current turn** — the command and the prompt go together. There is **no "arm for the next message" and no pending/armed state**:
- `/<skill-id> <message>` → inject the skill + run the turn now with `<message>` as the prompt.
- `/<skill-id>` **alone** (no message) → inject the skill + run the turn now with the **skill itself as the effective prompt** (the agent acts on the skill's instructions). `[Loop note]` the agent loop must support starting a turn from a skill activation with an empty user message; the skill body (already model-only) drives it.
- Consequence: the ambiguity **A1 (cancel an armed skill) is fully moot** — there is nothing to cancel; "if you don't want it, clear the input and don't send" (CC behavior). `setPendingSkills`/the arm branch in `applyExplicitSkillCommand` is **removed**, not rekeyed.

**R2 — Compact execution indicator (don't show the expanded prompt).** In Claude Code the expanded command prompt is not shown — only a one-liner that the skill ran. Mirror this: when a skill is activated, the transcript renders the user's **actual message** with the `/<skill-id>` token shown as a **compact "skill: <name>" chip/indicator**, NOT the raw `/<skill-id> …` text and NOT the skill's SKILL.md body (which stays model-only). For `/<skill-id>` alone, show just the "⚡ <name>" indicator (skill executed). `[Impl note]` likely client-side: the SPA recognizes a user message whose leading token is a known skill slug and renders it compactly; no skill body is ever surfaced.

**R3 — Argument hint in menu help AND ghost text.** Skills carry an optional `ArgumentHint` (`pkg/skills/loader.go::SkillMetadata.ArgumentHint`). Surface it in **both** places:
- In the **menu**: show the skill's `ArgumentHint` as muted help text next to the skill entry (Claude-Code-style).
- As **inline ghost text** after selection (D6): if the skill declares an `ArgumentHint`, the ghost shows it (e.g. `[topic]`); otherwise the generic `<message>`. The ghost still clears on type/blur and is never submitted.

**Net simplification:** R1 removes the arm path, the cancel affordance, and all pending-state edge cases; the resolution matrix collapses to **builtin → run-skill-now → normal-message** (no arm row).

---

## Available Reference Patterns

> In-repo patterns this feature extends. No `docs/reference/` library applies.

| Reference | Pattern | Relevance |
|-----------|---------|-----------|
| `src/components/chat/ChatScreen.tsx` (commit `a5142529`) | `slashItems` unified palette list + `visibleSkillItems` + `completeSkillName` + lazy `fetchSkills` query + keyboard nav | The `/skill` autocomplete shipped here is **repurposed** into the partitioned menu (D5). Most of it survives; the trigger/partition/ghost change. |
| `ChatScreen.tsx::runClientCommand('model')` → `useUiStore.setModelSelectorOpen(true)` | Client command opens an in-header selector via a UI-store flag | The exact template for D7 `/agents` → `setAgentSelectorOpen(true)`. |
| `src/store/ui.ts` `modelSelectorOpen` / `setModelSelectorOpen` | UI-store open-flag for a header popover | Mirror as `agentSelectorOpen` / `setAgentSelectorOpen`. |
| `pkg/agent/loop.go::applyExplicitSkillCommand` | Arm (`setPendingSkills`) vs force (`ForcedSkills`+`UserMessage`) skill activation | The arm/force semantics are preserved; only the *trigger* changes from the `use` token to "any non-built-in `/<skill-id>`". |
| `pkg/commands/definition.go::Hidden` + `builtin.go` | Hidden/deprecated command pattern | NOT used here (D1 is a hard removal) — noted so the implementer doesn't reach for it. |
| `pkg/commands/registry.go::Lookup` + `commands.CommandName` | Resolve a `/<name>` to a command definition | The built-in-precedence gate (D3). |

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/commands/cmd_use.go::skillCommand` | **delete** | D1 — remove `/skill` + `/use` from the registry. |
| `pkg/commands/builtin.go` (registry list) | modify | Drop `skillCommand()` from the registered set. |
| `pkg/commands/cmd_skills.go::skillsCommand` | modify | Add `SurfaceWeb` (D9). Fix the reply text (`:35` says "Use /skill <skill> …" → "/<skillname>"). |
| `pkg/commands/cmd_agents.go::agentsCommand` | modify | Add `SurfaceWeb` + `DeliveryClient` for web (D7); keep CLI/Channel `DeliveryAgent` list reply. |
| `pkg/agent/loop.go::applyExplicitSkillCommand` | **rewrite trigger** | D2/D3/D4 — match any `/<name>` where name is NOT a built-in AND IS an installed skill; arm/force preserved; else `matched=false`. |
| `agent.ContextBuilder.ResolveSkillName` / `ListSkillNames` | call | Resolve `<name>` → canonical skill; unchanged. |
| `commands.CommandName` / `registry.Lookup` | call | Detect built-in names for the D3 precedence gate. |
| `src/components/chat/ChatScreen.tsx` (palette: `slashItems`, `visibleSkillItems`, `visibleSlashCommands`, `completeSkillName`, `executeSlashCommand`, `handleKeyDown`, dropdown JSX, `runClientCommand`) | modify | D5 partitioned menu, D6 ghost-text, D7 `/agents` client handler, D9 `/skills` filter, D4 unknown-as-text. |
| `src/components/chat/ChatControls.tsx::handleAgentSelect` + the agent selector | modify | D7/D8 — make the selector openable via the new `agentSelectorOpen` flag; `/agents` reuses `handleAgentSelect` (which keeps the current session, swapping the agent). |
| `src/store/ui.ts` | modify | Add `agentSelectorOpen` + `setAgentSelectorOpen` (mirror `modelSelectorOpen`). |
| `pkg/commands/cmd_help.go::formatHelpMessage` | verify | D10 — lists commands only; ensure clean web render. |

### Impact Assessment

| Symbol Modified | Risk | d=1 (WILL break / must update) | d=2 (SHOULD test) |
|-----------------|------|-------------------------------|-------------------|
| `applyExplicitSkillCommand` (rewrite) | **HIGH** | the loop's command/skill dispatch path; `/use`-token tests | every skill-activation flow (web/CLI/channel); arm/force; `/<builtin>` not mis-activated |
| `cmd_use.go` delete | **MEDIUM** | `builtin.go` registry; `GET /commands` consumers; any test asserting `/skill`/`/use` present | SPA palette (must no longer show them); CLI/channel help |
| `ChatScreen.tsx` palette | **HIGH** | the full `src/components/chat/` vitest dir (806 tests, 41 files); `ChatScreen.no-hardcoded-commands.test.ts` | composer keyboard nav, send-path interception, replay/virtualization/harmful-upload tests (the 3 that broke last time via mocks) |
| `ui.ts` (+`agentSelectorOpen`) | LOW | `ChatControls` agent selector | any store consumer test |
| `cmd_agents.go` / `cmd_skills.go` surfaces | LOW | `GET /commands?surface=web`; surface tests | web palette Commands section |

### Relevant Execution Flows

| Flow | Relevance |
|------|-----------|
| Web compose → `/` palette → select → composer text → send → WS frame | D5/D6/D9; the partitioned menu + ghost-text live here. |
| Agent loop message processing → command dispatch → `applyExplicitSkillCommand` | D2/D3/D4 — built-in dispatch must run BEFORE the skill parser; unknown falls through to normal message. |
| `/model` client command → `setModelSelectorOpen(true)` → header popover | D7 template for `/agents`. |
| `handleAgentSelect(agentId)` → `setActiveSession(activeSessionId, agentId, type)` | D8 — keeps current session; `/agents` binds to this. |

---

## User Stories & Acceptance Criteria

### User Story 1 — Skill name is its command (Priority: P0)

A chat user wants to run an installed skill by typing its name as a command — `/<skill-id> do the thing` — instead of the awkward `/skill <name>`. This is the backend foundation: the agent loop must recognize `/<skill-id>` as activation, with built-ins taking precedence and unknown slashes passing through as normal text.

**Why this priority**: P0 — every web behavior (menu, ghost-text) produces `/<skill-id> …` text that this parser must honor. Without it, the whole feature is inert.

**Independent Test**: Drive the agent loop's command-resolution with a fixed skill set and assert the resolution matrix (built-in / exact-skill / unknown / prefix / collision / empty / case) routes to command-dispatch / arm / force / normal-message correctly.

**Acceptance Scenarios**:
1. **Given** an installed skill `web-research`, **When** the user sends `/web-research summarize this`, **Then** the skill is **forced** for that turn and the message is `summarize this` (force path).
2. **Given** `web-research`, **When** the user sends `/web-research` alone, **Then** it is **armed** for the next message (arm path) with a confirmation reply.
3. **Given** a `/<name>` whose name is a built-in (`/help`, `/clear`, `/model`, `/agents`, `/skills`, `/cancel`, …), **When** sent, **Then** the built-in command runs — the skill parser does NOT treat it as a skill (built-ins win, D3) — even if a skill with that slug exists.
4. **Given** `/<name>` that is neither a built-in nor an installed skill, **When** sent, **Then** it is delivered as a **normal chat message** (no error, no block) (D4).
5. **Given** a skill `web-research` and input `/web` (a prefix, not an exact slug), **When** sent, **Then** it is NOT activated as `web-research` (exact-slug match only) — it's a normal message (D4).

---

### User Story 2 — `/skill` and `/use` are gone (Priority: P0)

A user (and the API) should no longer see `/skill` or `/use`. They are hard-removed (D1). Typing `/skill foo` now just sends as a message (per D4), with no skill side-effect.

**Why this priority**: P0 — the consolidation's whole point; leaving them creates two grammars.

**Independent Test**: `GET /api/v1/commands?surface=web` (and cli/channel) does not include `skill` or `use`; the registry has no `skillCommand`.

**Acceptance Scenarios**:
1. **Given** the running gateway, **When** `GET /commands?surface=web|cli|channel`, **Then** neither `skill` nor `use` appears (hidden or visible).
2. **Given** the chat, **When** the user types `/skill web-research go`, **Then** it is sent as a normal message (no activation), per D4.
3. **Given** the codebase, **When** built, **Then** `pkg/commands/cmd_use.go` is removed and nothing references `skillCommand()`.

---

### User Story 3 — Partitioned `/` menu (Commands · Skills) (Priority: P0)

A chat user pressing `/` wants one menu that shows **both** the commands they can run and the skills they can activate, in **two labeled sections**, filtering live as they type — so discovery is one keystroke away.

**Why this priority**: P0 — the primary discovery surface; repurposes the `a5142529` palette.

**Independent Test**: Render the composer, type `/`, assert a **Commands** section and a **Skills** section render; type `/web`, assert both filter (commands starting `web…`, skills whose id/name prefix-matches `web`).

**Acceptance Scenarios**:
1. **Given** the composer with skills installed, **When** the user types `/`, **Then** the menu shows a **Commands** section (the web commands) and a **Skills** section (installed skills), each with a section header.
2. **Given** the open menu, **When** the user types more (`/web`), **Then** both sections filter live (case-insensitive prefix); empty sections are hidden.
3. **Given** the menu, **When** the user arrows down/up, **Then** the highlight moves across both sections (one continuous selection); Enter selects the highlighted item; Esc closes.
4. **Given** a selected **command** entry, **When** chosen, **Then** the existing command behavior runs (client command executes, or agent command inserts its text) — unchanged from today.

---

### User Story 4 — Inline ghost-text `<message>` after picking a skill (Priority: P1)

After selecting a skill, the composer shows `/<skill-id> ` as real text followed by a **muted, lower-contrast `<message>` ghost placeholder** that is *not* part of the input and disappears the instant the user types — guiding them to add their prompt.

**Why this priority**: P1 — high-value UX, but gated behind a spike (the overlay mechanism) and degradable to a fallback; the feature is usable (just less polished) without it.

**Independent Test**: After `completeSkillName('web-research')`, assert the input value is exactly `/web-research ` AND a ghost element showing `<message>` is present; simulate a keystroke, assert the ghost is gone; blur, assert the ghost is gone.

**Acceptance Scenarios**:
1. **Given** a skill picked from the menu, **When** selection completes, **Then** the input **value** is exactly `/<skill-id> ` (trailing space) and a muted `<message>` ghost is shown after it (the ghost is NOT in the textarea value).
2. **Given** the ghost is showing, **When** the user types any character, **Then** the ghost disappears immediately and their text renders in normal contrast.
3. **Given** the ghost is showing, **When** the input loses focus (blur) or the value no longer matches `/<skill-id> ` exactly (e.g. edited the command), **Then** the ghost is removed.
4. **Given** the ghost is showing, **When** the user sends without typing a message (value still `/<skill-id> `), **Then** the message submitted is `/<skill-id>` (the skill is **armed** for the next message per D2/G5) — the ghost text is never submitted.

---

### User Story 5 — `/agents` opens the agent selector (Priority: P1)

A user wants to switch the active agent from the chat without leaving it. `/agents` opens the existing in-header agent selector (exactly like `/model` opens the model selector); picking an agent switches it, keeping the current session.

**Why this priority**: P1 — parity with `/model`; reuses existing components and the existing switch flow.

**Independent Test**: `runClientCommand('agents')` sets `agentSelectorOpen=true`; the `ChatControls` agent selector is controlled by that flag; selecting an agent calls `handleAgentSelect` (→ `setActiveSession(currentSession, agentId)`).

**Acceptance Scenarios**:
1. **Given** the chat, **When** the user runs `/agents` (typed+Enter or picked from the menu), **Then** the in-header agent selector opens (no navigation away).
2. **Given** the open selector, **When** the user picks an agent, **Then** the active agent switches via the existing `handleAgentSelect`, **keeping the current session** (no new session created).
3. **Given** CLI/Channel, **When** `/agents` is invoked, **Then** the existing text list of agents is returned (web-only gets the selector).

---

### User Story 6 — `/skills` filters the menu to Skills (Priority: P2)

On web, typing `/skills` is a shortcut for "show me my skills": it narrows the open menu to just the Skills section. On CLI/Channel, `/skills` keeps replying with the text list.

**Why this priority**: P2 — convenience shortcut; the `/` menu already exposes skills.

**Independent Test**: Type `/skills`, assert the menu shows only the Skills section (Commands hidden) listing all installed skills.

**Acceptance Scenarios**:
1. **Given** the composer, **When** the user types `/skills`, **Then** the menu shows **only** the Skills section (Commands section hidden), listing all installed skills, first one highlighted.
2. **Given** that filtered menu, **When** the user picks a skill, **Then** the D6 ghost-text flow runs (insert `/<skill-id> ` + ghost).
3. **Given** CLI/Channel, **When** `/skills` is sent, **Then** the text list replies (with the corrected "Use /<skillname> …" wording).

---

### User Story 7 — `/help` lists commands cleanly (Priority: P2)

A user running `/help` sees a clear inline list of the available **commands** (not skills) with descriptions.

**Why this priority**: P2 — `formatHelpMessage` already does this; this is a verify/polish, not net-new.

**Independent Test**: Run `/help` on web; assert an inline message lists the canonical commands with descriptions and does NOT list skills.

**Acceptance Scenarios**:
1. **Given** the chat, **When** `/help` is run, **Then** an inline help message lists the canonical non-hidden commands (incl. `/skills`, `/agents`) with descriptions; `/skill`/`/use` are absent.
2. **Given** `/help`, **When** rendered, **Then** no skills are listed (commands only, D10).

---

### User Story 8 — Skills usable from CLI/channels by name (Priority: P2)

A CLI/channel user activates a skill by typing `/<skill-id> …` (D2 works on every surface); `/skills` lists them. No interactive menu (D10).

**Why this priority**: P2 — falls out of US1 (the backend parser is surface-agnostic); only the `/skills` reply text needs fixing.

**Independent Test**: In a channel/CLI context, `/<skill-id> msg` activates the skill (force); `/skills` returns the list with corrected wording.

**Acceptance Scenarios**:
1. **Given** a channel, **When** `/<skill-id> do x` is sent, **Then** the skill is forced for that turn (same parser as web, US1).
2. **Given** `/skills`, **When** sent on CLI/Channel, **Then** the reply lists skills and instructs `Use /<skillname> <message> …` (not `/skill <skill> …`).

---

### Edge Cases

- **Collision: skill slug == built-in name** (e.g. a skill literally named `help`) → the built-in wins (D3); the skill is unreachable by `/help` (acceptable; document). The skill is still reachable... it isn't, by command — note this limitation.
- **Prefix**: `/web` when `web-research` exists → not activated (exact slug only) → normal message (D4).
- **Case**: `/Web-Research` vs slug `web-research` → `ResolveSkillName` case handling defines it; spec: case-insensitive match for activation (mirrors `EqualFold` usage already in the parser), but the menu inserts the canonical slug.
- **Empty / `/` alone**: typing just `/` opens the menu; sending `/` alone → normal message.
- **Cancel an armed skill** (the `/use clear` affordance is removed with D1): **[AMBIGUITY A1 — see audit]**. Default: arming is consumed by the next message; to cancel before sending, re-arm a different skill, or (web) a "Clear armed skill" entry appears at the top of the Skills section while a skill is armed; (CLI/channel) `/skills clear`.
- **Ghost-text with wrapping / multi-line**: the ghost shows ONLY when the value is exactly `/<skill-id> ` (single short line, no wrap) — so caret/wrap tracking is unnecessary (see G2 design). Any edit that breaks the exact-prefix match removes the ghost.
- **Two installed skills, one a prefix of another** (`web`, `web-research`): exact-slug match → `/web` activates `web`, `/web-research` activates `web-research`; the menu lists both.
- **No skills installed**: the `/` menu shows only the Commands section (Skills section hidden); `/skills` shows an empty Skills section / "No installed skills".
- **Send-path interception** (existing `interceptClientCommand`): `/clear`/`/cancel` etc. still intercept; `/<skill-id> msg` must NOT be intercepted (it's an agent message); `/agents`/`/skills`/`/help` client behaviors fire correctly.

---

## Behavioral Contract

- When the user sends `/<name> <message>` and `<name>` is an installed skill (not a built-in), the system forces that skill for the turn with `<message>`.
- When the user sends `/<name>` alone and it's a skill, the system arms it for the next message.
- When `<name>` is a built-in command, the built-in runs (skills never shadow built-ins).
- When `<name>` is neither, the literal text is sent as a normal message.
- When the user types `/`, the menu shows partitioned Commands and Skills sections, filtering live.
- When the user picks a skill, the input becomes `/<skill-id> ` plus a muted ghost `<message>` that clears on type/blur and is never submitted.
- When the user runs `/agents`, the in-header agent selector opens; picking an agent switches it in the current session.
- When the user types `/skills` on web, the menu narrows to the Skills section.
- When the user runs `/help`, an inline list of commands (not skills) is shown.
- `/skill` and `/use` are absent from every command surface.

## Explicit Non-Behaviors

- The system must **not** let a skill shadow a built-in command (D3) — a skill named `clear` must never hijack `/clear`.
- The system must **not** error or block on an unknown `/<x>` — it sends as text (D4), because `/` isn't reserved.
- The system must **not** keep `/skill` or `/use` as hidden aliases (D1) — hard removal.
- The system must **not** submit the ghost `<message>` text as part of the message (D6) — it's display-only.
- The system must **not** invent new agent-switch session semantics (D8) — it reuses `handleAgentSelect` verbatim (current session preserved).
- The system must **not** add an interactive numbered menu to the one-shot CLI (D10).
- The system must **not** list skills in `/help` (D10) — commands only.
- The system must **not** hardcode the command or skill lists in the SPA (keep `ChatScreen.no-hardcoded-commands.test.ts` green) — both come from the API.

## Integration Boundaries

### `GET /api/v1/commands?surface=…` (existing)
- **In/out**: surface → list of `SlashCommand` (label, description, delivery, available_while_streaming). After D1/D7/D9: no `skill`/`use`; `skills`+`agents` now on web.
- **Failure**: on error the palette shows nothing (existing behavior) — Commands section empty, Skills section still works (independent `fetchSkills`).

### `GET /api/v1/skills` (existing)
- **In/out**: → `Skill[]` (id, name, description). Drives the Skills section + the `/<skill-id>` completion. Lazy-fetched (60s stale), as today.
- **Failure**: error → Skills section empty; Commands section unaffected.

### Agent loop command dispatch (in-process)
- Built-in dispatch runs first; `applyExplicitSkillCommand` (rekeyed) runs for non-built-in `/<name>`; unknown → normal message. Dev approach: unit-test the parser against an in-memory skill set + the built-in registry (no network).

---

## BDD Scenarios

> Format per `bdd-template.md`. Every scenario carries `Traces to:` (US.AC).

### Backend resolution (US1/US2)

```gherkin
Scenario Outline: /<name> resolution matrix
  Given an agent with installed skills ["web-research", "web", "summarize"]
  And the built-in commands include ["help","clear","model","agents","skills","cancel"]
  When the user sends "<input>"
  Then the routing is "<routing>"
  And the forced/armed skill is "<skill>"

  Examples:
    | input                       | routing          | skill        |
    | /web-research find X        | force-skill      | web-research |
    | /web-research               | arm-skill        | web-research |
    | /web do Y                   | force-skill      | web          |
    | /help                       | builtin-command  | -            |
    | /clear                      | builtin-command  | -            |
    | /skills                     | builtin-command  | -            |
    | /agents                     | builtin-command  | -            |
    | /nonesuch hello             | normal-message   | -            |
    | /sum                        | normal-message   | -            |   # prefix of "summarize", not exact
    | /WEB-RESEARCH go            | force-skill      | web-research |   # case-insensitive activation
    | /skill web-research go      | normal-message   | -            |   # /skill removed (D1/D4)
    | /use web-research           | normal-message   | -            |   # /use removed
    | /                           | normal-message   | -            |
```
**Category**: Happy/Alt/Error mix. **Traces to**: US1.1–1.5, US2.2.

```gherkin
Scenario: a skill named like a built-in cannot shadow it
  Given an installed skill whose slug is "clear"
  When the user sends "/clear"
  Then the built-in /clear (new conversation) runs
  And the "clear" skill is NOT activated
```
**Category**: Edge Case. **Traces to**: US1.3.

```gherkin
Scenario: /skill and /use are absent from the command API
  When GET /api/v1/commands is requested for surface web, cli, and channel
  Then the response contains neither "skill" nor "use"
```
**Category**: Happy Path. **Traces to**: US2.1.

### Partitioned menu (US3)

```gherkin
Scenario: typing / shows Commands and Skills sections
  Given the composer with skills ["web-research","summarize"] installed
  When the user types "/"
  Then the menu shows a "Commands" section and a "Skills" section
  And each section has a header
```
**Category**: Happy Path. **Traces to**: US3.1.

```gherkin
Scenario: live filtering across both sections
  Given the open menu
  When the user types "/web"
  Then the Commands section shows only commands whose label starts with "/web"
  And the Skills section shows only skills whose id or name prefix-matches "web"
  And an empty section is hidden
```
**Category**: Happy Path. **Traces to**: US3.2.

```gherkin
Scenario: continuous keyboard navigation across sections
  Given the open menu with both sections populated
  When the user presses ArrowDown past the last Command
  Then the highlight moves into the Skills section
  And Enter selects the highlighted item
```
**Category**: Alternate Path. **Traces to**: US3.3.

### Ghost-text (US4)

```gherkin
Scenario: picking a skill inserts the command + ghost message
  Given the open menu
  When the user selects the skill "web-research"
  Then the composer value is exactly "/web-research " (trailing space)
  And a muted "<message>" ghost is shown after it
  And the ghost text is NOT part of the textarea value
```
**Category**: Happy Path. **Traces to**: US4.1.

```gherkin
Scenario Outline: ghost clears on type or blur
  Given the ghost is showing after picking "web-research"
  When "<event>" occurs
  Then the ghost is no longer shown

  Examples:
    | event                         |
    | the user types a character    |
    | the input loses focus (blur)  |
    | the user edits the command    |
```
**Category**: Alternate Path. **Traces to**: US4.2, US4.3.

```gherkin
Scenario: sending with the ghost (no typed message) arms the skill
  Given the ghost is showing and the value is "/web-research "
  When the user presses Enter without typing
  Then the submitted message is "/web-research"
  And the skill is armed for the next message
  And the ghost text is never submitted
```
**Category**: Edge Case. **Traces to**: US4.4, US1.2.

### /agents (US5)

```gherkin
Scenario: /agents opens the in-header selector and switches in-session
  Given the chat with an active session and agent A
  When the user runs "/agents" and selects agent B
  Then the in-header agent selector opened (no navigation)
  And handleAgentSelect switched to agent B keeping the current session
```
**Category**: Happy Path. **Traces to**: US5.1, US5.2.

### /skills filter (US6) + /help (US7) + CLI (US8)

```gherkin
Scenario: /skills narrows the web menu to skills
  Given installed skills
  When the user types "/skills"
  Then the menu shows only the Skills section (Commands hidden)
  And the first skill is highlighted
```
**Category**: Alternate Path. **Traces to**: US6.1.

```gherkin
Scenario: /help lists commands only
  When the user runs "/help"
  Then an inline message lists the canonical commands with descriptions
  And no skills are listed
  And neither /skill nor /use appears
```
**Category**: Happy Path. **Traces to**: US7.1, US7.2.

```gherkin
Scenario: /skills reply text references the new activation form
  When "/skills" is sent on a CLI/Channel surface
  Then the reply lists installed skills
  And instructs the user to "Use /<skillname> <message> …"
  And does not mention "/skill" or "/use"
```
**Category**: Happy Path. **Traces to**: US8.2.

---

## Test-Driven Development Plan

> **Task 0 (spike, before US4): ghost-text mechanism.** Time-box ~30 min. Decide between (a) an absolutely-positioned overlay `<span>` showing the ghost after the known `/<skill-id> ` prefix (viable because the ghost only shows when the value is exactly that single short line — no caret/wrap tracking), vs (b) a mirror-`div` measuring text width. **Recommended: (a)** — simplest, since D6's exact-prefix gate removes the hard caret problem. **Fallback (operator sign-off):** a muted hint line *below* the composer. Output: a 1-paragraph decision note appended to this spec + a throwaway branch demo.

| Order | Test Name | Level | Traces to | Description |
|-------|-----------|-------|-----------|-------------|
| 1 | `TestResolveSlash_Matrix` | Unit (Go) | Resolution Outline | The full `/<name>` matrix (force/arm/builtin/normal/prefix/case). |
| 2 | `TestResolveSlash_SkillCannotShadowBuiltin` | Unit (Go) | Builtin-shadow | Skill slug == builtin → builtin runs. |
| 3 | `TestApplyExplicitSkill_ArmThenForce` | Unit (Go) | US1.1/1.2 | Arm sets pending; force sets ForcedSkills+UserMessage. |
| 4 | `TestCommands_NoSkillOrUse` | Unit (Go) | US2.1 | Registry/`Definitions()` omit `skill`+`use` on all surfaces. |
| 5 | `TestCmdSkills_WebSurface_AndReplyText` | Unit (Go) | US6.3/US8.2 | `/skills` has SurfaceWeb; reply text uses `/<skillname>`. |
| 6 | `TestCmdAgents_WebSurface` | Unit (Go) | US5.3 | `/agents` web-surfaced; CLI/Channel list preserved. |
| 7 | `TestHelp_CommandsOnly` | Unit (Go) | US7 | `formatHelpMessage` lists commands, not skills, no skill/use. |
| 8 | `ChatScreen.partitioned-menu.test.tsx` | Unit (vitest) | US3 | `/` shows Commands+Skills headers; `/web` filters both; empty section hidden. |
| 9 | `ChatScreen.menu-keyboard.test.tsx` | Unit (vitest) | US3.3 | Arrow nav crosses sections; Enter selects; Esc closes. |
| 10 | `ChatScreen.ghost-text.test.tsx` | Unit (vitest) | US4 | value==`/<id> `; ghost present; clears on type/blur/edit; not submitted. |
| 11 | `ChatScreen.skill-as-command.test.tsx` | Unit (vitest) | US1/US4.4 | Selecting → `/<id> `; send with ghost → submits `/<id>`. |
| 12 | `ChatScreen.agents-command.test.tsx` | Unit (vitest) | US5 | `/agents` sets `agentSelectorOpen`; `runClientCommand('agents')` returns true. |
| 13 | `ChatControls.agent-selector-open.test.tsx` | Unit (vitest) | US5.1/5.2 | Selector controlled by `agentSelectorOpen`; select → `handleAgentSelect` (current session). |
| 14 | `ChatScreen.skills-filter.test.tsx` | Unit (vitest) | US6.1 | `/skills` → only Skills section, first highlighted. |
| 15 | `ChatScreen.unknown-slash.test.tsx` | Unit (vitest) | US1.4/US2.2 | `/nonesuch`, `/skill x` → sent as normal message (not intercepted). |
| 16 | `ui.store.agentSelector.test.ts` | Unit (vitest) | US5 | `agentSelectorOpen` default false; setter toggles. |
| 17 | `commands.surface.integration_test.go` | Integration (Go) | US2.1 | `GET /commands` per surface excludes skill/use, includes skills+agents(web). |
| 18 | `slash-command-skill-menu.spec.ts` | E2E (Playwright, stub LLM) | US1/US3/US4/US5 | Type `/`, see sections; pick skill → `/id ` + ghost; type → ghost gone; `/agents` opens selector. |

### Test Datasets

#### Dataset A — `/<name>` resolution (drives Test #1/#2/#3)

| ID | Installed skills | Input | Expected routing | Skill | Traces |
|----|------------------|-------|------------------|-------|--------|
| A1 | web-research | `/web-research find X` | force-skill | web-research | A.Outline |
| A2 | web-research | `/web-research` | arm-skill | web-research | A.Outline |
| A3 | web-research,web | `/web do Y` | force-skill | web | A.Outline |
| A4 | * | `/help` | builtin | - | Shadow |
| A5 | clear (skill) | `/clear` | builtin | - | Shadow |
| A6 | web-research | `/nonesuch hi` | normal-message | - | A.Outline |
| A7 | summarize | `/sum` | normal-message | - | prefix |
| A8 | web-research | `/WEB-RESEARCH go` | force-skill | web-research | case |
| A9 | web-research | `/skill web-research go` | normal-message | - | D1/D4 |
| A10 | web-research | `/use web-research` | normal-message | - | D1 |
| A11 | * | `/` | normal-message | - | empty |
| A12 | (none) | `/web-research` | normal-message | - | no skills |

#### Dataset B — Web menu / ghost (drives Tests #8–#15)

| ID | Input/action | Expectation | Traces |
|----|--------------|-------------|--------|
| B1 | type `/` | Commands + Skills sections render | US3.1 |
| B2 | type `/web` | both sections prefix-filter; empty hidden | US3.2 |
| B3 | type `/skills` | only Skills section, first highlighted | US6.1 |
| B4 | type `/help` (no skills) | only Commands section | US3.2 |
| B5 | select skill `web-research` | value `/web-research `; ghost shown | US4.1 |
| B6 | then type `a` | ghost gone; value `/web-research a` | US4.2 |
| B7 | then blur | ghost gone | US4.3 |
| B8 | select skill, Enter (no type) | submit `/web-research` | US4.4 |
| B9 | `/agents` Enter | `agentSelectorOpen=true` | US5.1 |
| B10 | `/nonesuch` Enter | sent as message (not intercepted/cleared) | US1.4 |
| B11 | `/skill x` Enter | sent as message | US2.2 |

### Regression Test Requirements

This **modifies existing functionality** (the composer palette + the skill parser + the command registry).

**Behaviours that MUST be preserved**:
1. The full `src/components/chat/` vitest dir — **806 tests / 41 files** — stays green (incl. replay, virtualization, harmful-upload, which mock `@/lib/api` and broke once before — any new import must follow the deferred-read pattern or update those mocks).
2. `ChatScreen.no-hardcoded-commands.test.ts` — commands/skills remain API-driven, not hardcoded.
3. Existing command behaviors (`/clear`, `/model`, `/cancel`, `/help`) unchanged.
4. `interceptClientCommand` still intercepts client commands; agent commands still send.
5. Arm/force skill activation outcomes (the agent loop's pending/forced behavior) unchanged — only the trigger token changes.

**DELIBERATE changes (re-assert, call out in the PR)**:
- `/skill`/`/use` removed from `GET /commands` and from activation — existing tests asserting their presence must be deleted/updated.
- The `applyExplicitSkillCommand` `/use`-token tests → rewritten to the `/<skill-id>` resolution matrix.
- The `/skills` reply text changes (`/skill <skill>` → `/<skillname>`).

**Regression dataset**: Dataset A rows A1–A3 (arm/force preserved) + the command-behavior smoke (A4) double as the preservation check; A9/A10 are the removal change.

---

## Functional Requirements

- **FR-001**: The agent loop MUST resolve a leading `/<name>` as: built-in command (if `name` is a non-hidden registered command) → that command; else installed skill (exact, case-insensitive slug) → **activate and run on the current turn** (one-shot, R1: with the message as the prompt, or — if no message — the skill itself as the prompt); else → normal message. **No arm/pending state.** (D2-as-revised/D3/D4/R1)
- **FR-002**: `/skill` and `/use` MUST be removed from the registry and absent from `GET /commands` on every surface. (D1)
- **FR-003**: A skill MUST NOT be activatable by a name equal to a built-in command (built-ins win). (D3)
- **FR-004**: An unknown `/<x>` MUST be delivered as a normal message with no error. (D4)
- **FR-005**: The web composer MUST render a `/` menu partitioned into a **Commands** section and a **Skills** section, each filtering live (case-insensitive prefix); empty sections hidden; one continuous keyboard selection. (D5)
- **FR-006**: Selecting a skill MUST set the composer value to exactly `/<skill-id> ` and display a muted, lower-contrast ghost that shows the skill's `ArgumentHint` if declared, else `<message>`; the ghost is not part of the value, removed on the first keystroke, on blur, or when the value no longer equals `/<skill-id> `, and never submitted. (D6/R3)
- **FR-013**: When a user message activates a skill, the transcript MUST render it **compactly** — the user's actual message (or "⚡ <name>" when there is no message) with the skill shown as a "skill: <name>" indicator — and MUST NOT show the raw `/<skill-id>` token or the skill's SKILL.md body. (R2)
- **FR-014**: The web `/` menu MUST display each skill's `ArgumentHint` (when declared) as muted help text alongside the skill entry. (R3)
- **FR-007**: `/agents` MUST be a web client command that opens the in-header agent selector via a new `agentSelectorOpen` UI-store flag (mirroring `modelSelectorOpen`); selecting an agent MUST use the existing `handleAgentSelect` (current session preserved). CLI/Channel keep the list reply. (D7/D8)
- **FR-008**: `/skills` MUST gain `SurfaceWeb` and, on web, narrow the open menu to the Skills section; CLI/Channel keep the text-list reply with corrected wording (`/<skillname>`). (D9)
- **FR-009**: `/help` MUST list commands only (no skills) and render cleanly inline on web. (D10)
- **FR-010**: The SPA MUST keep commands and skills API-driven (no hardcoded lists). (regression)
- **FR-011**: The one-shot CLI MUST NOT gain an interactive menu; `/<skill-id> …` activation works there via FR-001. (D10)
- **FR-012**: The `cmd_skills.go` reply text MUST reference `/<skillname>`, not `/skill <skill>`. (D9)

## Success Criteria

- **SC-001**: 100% of Dataset-A rows (A1–A12) pass `TestResolveSlash_Matrix`/related (0 misroutes).
- **SC-002**: `GET /commands?surface={web,cli,channel}` returns 0 entries named `skill` or `use`; `grep -rc "skillCommand\|cmd_use" pkg/commands` shows the command deleted.
- **SC-003**: A skill slug equal to a built-in never activates the skill (Test #2).
- **SC-004**: Selecting a skill yields composer value exactly `/<skill-id> ` and a ghost element; one keystroke removes the ghost; the ghost is absent from any submitted frame (Tests #10/#11).
- **SC-005**: `/agents` sets `agentSelectorOpen=true` and agent selection preserves `activeSessionId` (Tests #12/#13).
- **SC-006**: `/skills` on web shows only the Skills section (Test #14).
- **SC-007**: The full `src/components/chat/` vitest dir stays green (≥806 tests, 41 files) and `ChatScreen.no-hardcoded-commands.test.ts` passes.
- **SC-008**: All gates green: `npm run typecheck` 0, `npx vitest run` 0, `CGO_ENABLED=0 go test -tags goolm,stdjson` (via CI worker) 0, `golangci-lint` 0, `make verify-contracts` 0.
- **SC-009**: Manual: typing `/skill web-research go` sends as a normal chat message (no activation) — the deliberate D1 change.

## Traceability Matrix

| FR | User Story | BDD Scenario(s) | Test(s) |
|----|-----------|------------------|---------|
| FR-001 | US1 | Resolution Outline | #1, #3 |
| FR-002 | US2 | /skill /use absent | #4, #17 |
| FR-003 | US1 | builtin-shadow | #2 |
| FR-004 | US1 | Resolution (normal-message rows) | #1, #15 |
| FR-005 | US3 | partitioned menu; live filter; keyboard | #8, #9 |
| FR-006 | US4 | ghost insert; clears; arms-on-empty | #10, #11 |
| FR-007 | US5 | /agents opens selector | #12, #13, #16 |
| FR-008 | US6 | /skills narrows | #5, #14 |
| FR-009 | US7 | /help commands only | #7 |
| FR-010 | regression | (no-hardcoded) | #8 + existing |
| FR-011 | US8 | CLI by-name | #1 (surface-agnostic) |
| FR-012 | US6/US8 | /skills reply text | #5 |

Every FR is covered; every BDD scenario traces to ≥1 FR.

---

## Ambiguity Self-Audit

| # | What's ambiguous | Likely agent assumption | Resolution / Question |
|---|------------------|-------------------------|------------------------|
| **A1** | **Cancel an armed skill** — `/use clear` is removed (D1). | — | **RESOLVED (operator + Claude Code research): no explicit cancel.** Claude Code has no armed/pending state at all (commands/skills are one-shot, sent with the prompt) — so there is nothing to cancel; if you don't want it, you clear the input and don't send. Omnipus keeps `/<skill>`-alone arming (D2) only because Omnipus skills are context-injections that need a *follow-up* user message (unlike Claude Code, where a command IS a prompt). So: an armed skill is simply **consumed by the next message**; no cancel command, no menu entry. To not use it, send a different `/<skill>` or just proceed. |
| A2 | Ghost-text mechanism (overlay span vs mirror-div). | Agent picks one arbitrarily. | Resolved by **Task 0 spike**; recommended = positioned span (exact-prefix gate removes caret tracking). Fallback = hint below input (operator sign-off). |
| A3 | `/skills` filter precision (hide Commands entirely vs just anchor). | Show only Skills. | **Spec'd**: hide Commands, show only Skills, highlight first (US6.1). Confirm acceptable. |
| A4 | Case-folding for activation (`/WEB-RESEARCH`). | Case-insensitive (matches existing `EqualFold`). | **Spec'd** case-insensitive activation; menu inserts canonical slug. |
| A5 | Skill slug with characters invalid as a command token (spaces/dots). | Slugs are dir names (hyphen/alnum); fine. | **Accepted** — `Skill.id` is a slug; no spaces. |
| A6 | Does the `/` menu also include `/agents` discovery (since agents aren't a partition)? | `/agents` is a Command in the Commands section (it's a command). | **Spec'd**: `/agents` appears in the Commands section like any command; selecting it runs the client handler (opens selector). No separate Agents partition. |

**GATE**: A1 needs an operator answer; A2 is a spike; A3/A4/A6 are spec'd defaults to confirm.

---

## Holdout Evaluation Scenarios

> **HOLDOUT — post-implementation only; NOT in the TDD plan/matrix. Evaluate manually.**

**Happy path**
- H1: Type `/`, see Commands and Skills sections; type a few letters of a real skill, pick it, type a prompt, send → the skill runs on that turn.
- H2: `/agents`, pick a different agent → header shows the new agent, same conversation continues.
- H3: `/help` → a readable list of commands (no skills, no `/skill`/`/use`).

**Error path**
- H4: `/skill web-research go` → appears as a normal chat message; the agent treats it as text (no skill side-effect).
- H5: `/<a-skill-named-clear>` → starts a new conversation (the built-in), does not run the skill.

**Edge case**
- H6: Pick a skill, delete the ghost-implied message, send with just `/<skill-id> ` → the skill is armed; the next plain message uses it.
- H7: Pick a skill, then edit the command itself (backspace into `/web-researc`) → ghost disappears; sending it is a normal message (no longer an exact slug).

---

## Claude Code Alignment (reference UX) — deliberate divergences

Claude Code was researched as the reference. Where this spec diverges, it is a **conscious operator choice**, recorded here so implementers and reviewers don't "fix" it toward Claude Code:

| Aspect | Claude Code behavior | This spec (Omnipus) | Why we diverge |
|--------|----------------------|---------------------|----------------|
| `/` menu | **Flat** single list (commands + skills + bundled, no sections) | **Partitioned** Commands · Skills (D5) | Operator wants the two kinds visually separated. |
| Activation timing | **One-shot** — command/skill sent *with* the prompt; no pending state | **One-shot** (R1, arming dropped) | ✅ **aligned** (Revision 1). |
| Prompt display | Expanded prompt **not shown**; a one-liner indicates the skill ran | **Compact "skill: X" indicator**, skill body model-only (R2) | ✅ **aligned** (Revision 1). |
| Cancel pending | None needed (no pending state) | None — moot (R1/A1) | ✅ aligned. |
| Argument hint | Shown as **menu help text** (`argument-hint`) | **Menu help text AND inline ghost text** (R3) | Superset of CC — both surfaces. |
| `/` menu | **Flat** single list | **Partitioned** Commands · Skills (D5) | Intentional divergence (operator). |
| Agent switching | **No** `/`-style agent switcher | `/agents` opens the in-header selector (D7) | Intentional divergence — Omnipus is multi-agent. |

**Alignment verdict (post-Revision 1):** activation, prompt-display, no-cancel, and the argument hint now match (or superset) Claude Code. The only deliberate divergences are the **partitioned menu (D5)** and **`/agents` (D7)** — both justified Omnipus-specific UX, neither blocks the build.

## Assumptions

- `ResolveSkillName` performs case-insensitive resolution and returns the canonical slug (matches the existing `EqualFold` handling in the parser); the menu always inserts the canonical `Skill.id`.
- The agent loop's command dispatch already runs built-in commands before `applyExplicitSkillCommand`; the rewrite preserves that ordering (built-ins first).
- The header agent selector in `ChatControls` is a controllable popover (or can be made one) the way `ModelSelector` is — wiring an `agentSelectorOpen` flag is sufficient.
- A2/A3/A4/A6 defaults accepted unless the operator changes them; A1 pending operator answer.

## Dependencies

- Existing endpoints `GET /commands`, `GET /skills`, `GET /agents` (no new wire types anticipated — verify no contract change needed; if a new field is added, follow the 5-step contract process).
- CI worker (`ci-omnipus`) for the Go suite; never run the full gateway suite locally.

## Out of Scope

- Interactive numbered CLI menu (D10).
- Channels UX beyond the `/skills` text fix + `/<skill-id>` activation.
- Any change to skill *authoring*, injection mechanics, or the Skills screen.
- A new "Agents" partition in the `/` menu (agents are reached via the `/agents` command).
