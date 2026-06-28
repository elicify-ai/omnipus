# Spec — Slash-Command Harmonization & Surface-Aware Single Source of Truth

| | |
|---|---|
| **Status** | Draft — ready for `/grill-spec` |
| **Author** | Daniel Piatkowski |
| **Date** | 2026-06-28 |
| **Branch** | `feat/0.1.0-uat-fixes` (UAT) |
| **Grounding** | Code read this session: `pkg/commands/*`, `pkg/agent/loop.go`, `pkg/bus/types.go`, `pkg/gateway/websocket.go`, `pkg/channels/manager.go`, `src/components/chat/{ChatScreen,MessageInput}.tsx`, `src/store/chat.ts`, the `/skills` GET contract path |

---

## 1. Overview

### Problem
Slash commands are inconsistent and duplicated:
- **Names are verb-based** (`/show model`, `/list agents`, `/switch model to X`), not Claude-Code-style noun commands.
- **The web palette is a hand-maintained duplicate** — `ChatScreen.tsx::SLASH_COMMANDS` (5) **and** a second `MessageInput.tsx` suggestion list — neither derived from the backend registry, so they drift from the 11 canonical commands.
- **No surface awareness** — every command is "globally visible"; the web UI can't hide commands that already have a dedicated screen, and there's no way to say "this command isn't for the web chat."

### Goal
1. **Rename** to a harmonized Claude-Code-aligned set, keeping old names as **hidden back-compat aliases** for one release.
2. Make the **central registry (`pkg/commands/`) the single source of truth** for names, aliases, descriptions, **and surfaces**.
3. Add **surface-aware gating** to the executor: a non-web command typed in the web chat **passes through to the model** (option B), but still runs in CLI/channels.
4. Expose the surface-applicable set via a new **`GET /api/v1/commands?surface=` contract endpoint**.
5. **Rebuild the web palette to render from that endpoint** and **delete both hardcoded lists**; dispatch each command by a registry-provided `delivery` hint (client vs agent).

### Non-goal
No change to *what* any command does (behavior preserved), no brand-new commands, no per-agent dynamic commands beyond skills, and the `omnipus <subcommand>` CLI binary commands are untouched.

---

## 2. Actors
| Actor | Role |
|---|---|
| **Web chat user** | Types `/` → sees the palette (5), runs commands |
| **Channel user** (Telegram/Discord/…) | Types commands; sees the platform menu (channel-surfaced) |
| **CLI user** (`omnipus agent` REPL) | Types commands |
| **Agent loop / executor** | Parses inbound text, gates by surface, dispatches |
| **Channel adapters** | Sync the channel-surfaced commands to the platform menu |
| **SPA composer** | Fetches `/commands?surface=web`, renders palette, dispatches by `delivery` |

---

## 3. Available Reference Patterns
| Source | Pattern | Use here |
|---|---|---|
| `GET /api/v1/skills` (contract + `fetchSkills` + `Skill.yaml`) | Read-only list endpoint, 5 contract touch-points | Mirror the **shape** for `GET /api/v1/commands` + `SlashCommand.yaml` + `fetchCommands`. NOTE: `/skills` has **no query params** — `/commands` adds a `?surface=` query + handler-side filtering that `/skills` lacks. |
| `pkg/commands/` central registry + `executor.go` | Single dispatch path through the agent loop | Extend with `Surfaces` + surface gating; no per-channel command logic |
| `CommandRegistrarCapable` (`channels/interfaces.go`) | Channels sync the registry to their platform menu | Filter to channel-surfaced defs |
| Constraint #8 "add a wire type" 5-step process | Contract-first generated types | The `SlashCommand` schema + `/commands` path |

---

## 4. Existing Codebase Context

### Symbols Involved
| Symbol | Role | Context |
|---|---|---|
| `pkg/commands/definition.go` `Definition` | **modify** | Add `Surfaces []Surface` + `Delivery DeliveryMode`. `Surface` + `DeliveryMode` new enums (`surface.go`). Empty `Surfaces` = all (back-compat). |
| `pkg/commands/registry.go` `Registry` | calls (unchanged API) | `Definitions()`, `Lookup(name)` keep working; surface filtering is in the executor/handler, not the registry. |
| `pkg/commands/executor.go` `Execute` | **modify** | After `Lookup`, map `req.Channel`→`Surface`; if `def.Surfaces` non-empty and excludes it → `OutcomePassthrough` (no execute). |
| `pkg/commands/request.go` `Request.Channel` | calls (unchanged) | Already carries origin ("webchat"/"cli"/channel). No bus/loop change. |
| `pkg/commands/builtin.go` `BuiltinDefinitions()` + `cmd_*.go` | **modify** | Rename `Name`, add old name to `Aliases`, set `Surfaces`/`Delivery`. New `cmd_*.go` for `/agents`, `/tasks`, `/channels`, `/skills`, `/model`, `/config`, `/status` (most are renames of existing handlers). |
| `pkg/commands/cmd_help.go` `formatHelpMessage` | **modify** | Accept the caller's surface; show only surface-applicable, non-alias commands. |
| `pkg/commands/cmd_cancel.go` + `_test.go` | unchanged | `/cancel` keeps zero aliases (legacy FR-5). |
| `pkg/channels/manager.go` `StartAll` (~943-967) | **modify** | Pass only channel-surfaced defs to each `RegisterCommands`. |
| `pkg/gateway/` (new handler) | **new** | `handleListCommands` mirrors the `/skills` GET handler; maps registry→`SlashCommand` filtered by `?surface=`. |
| `contracts/openapi.yaml` + `components/schemas/SlashCommand.yaml` | **new** | `GET /commands` path + schema; regen `pkg/api/generated/` + `src/lib/api/generated/`. |
| `src/lib/api.ts` `fetchCommands` | **new** | Mirror `fetchSkills`; generated `SlashCommand[]` + zod. |
| `src/components/chat/ChatScreen.tsx` `SLASH_COMMANDS` + `executeSlashCommand` | **modify (delete list)** | Render palette from `useQuery(fetchCommands)`; dispatch by `delivery`; keep client handlers keyed by `name`. |
| `src/components/chat/MessageInput.tsx` suggestion list | **modify (delete built-in list)** | Reconcile to the same `fetchCommands` source; dynamic `/skill <id>` entries still from `fetchSkills`. |

### Impact Assessment
| Symbol modified | Risk | Direct dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `Definition` (+2 fields) | **LOW** | every `cmd_*.go`, `BuiltinDefinitions`, registry, channel registration | help/list formatters |
| `Executor.Execute` (gating) | **MEDIUM** | `agent/loop.go::handleCommand` (only caller) | every inbound message that *could* be a command (pass-through path must stay correct for normal text) |
| `BuiltinDefinitions` renames | **MEDIUM** | channel menus, help, the new `/commands` handler, existing command tests | docs, any test asserting old names |
| `ChatScreen`/`MessageInput` palette | **MEDIUM** | the chat composer | existing chat tests asserting the hardcoded palette |
| new `/commands` contract | **LOW** | generated types (Go+TS) | `make verify-contracts` |

### Relevant Execution Flows
| Flow | Relevance |
|---|---|
| **Inbound command**: channel/web/cli → `bus.InboundMessage{Channel}` → `loop.handleCommand` → `executor.Execute(Request{Channel})` → Handled\|Passthrough | Where surface gating inserts; pass-through must route normal text to the LLM unchanged. |
| **Channel menu sync**: `manager.StartAll` → `RegisterCommands(channel-surfaced defs)` → platform menu | Renames + surface filter propagate here. |
| **Web palette**: composer `useQuery(fetchCommands('web'))` → render → user selects → dispatch by `delivery` (client handler **or** insert-as-text → `store/chat.sendMessage` WS frame) | The consolidated single source. |

---

## 5. Scope

### Ground truth — backend commands TODAY (verified `BuiltinDefinitions()`)
`/start`, `/help`, `/show`*, `/list`*, `/use`, `/switch`*, `/check`*, `/clear`, `/subagents`, `/reload`, `/cancel` — where `*` = **multiplexer** (routes sub-commands: `/show model|channel|agents`, `/list models|channels|agents|skills`, `/switch model|channel`, `/check channel`). **`/new`, `/session new`, `/model`, `/agents`, `/tasks`, `/skills`, `/channels`, `/status`, `/config` do NOT exist as backend commands** — the harmonization *creates* the noun commands by restructuring the multiplexers (each new noun command reuses the existing sub-handler logic — behaviour preserved, not new functionality). `/new` + `/session new` are **SPA-only labels** (client `/clear`), not backend commands.

### Harmonized canonical set (11) — origin, surfaces, web delivery
| Canonical | How produced (from today's backend) | Surfaces | Web delivery |
|---|---|---|---|
| `/clear` | **kept**. Surface-appropriate effect: web = new local chat; CLI/channel = `rt.ClearHistory()` (clear server session). | Web, CLI, Channel | **client** |
| `/help` | **kept**. Web renders the fetched command list; CLI/channel = backend help text. | Web, CLI, Channel | **client** |
| `/model [name]` | **new noun cmd** — reuses `/switch model` + `/show model` + `/list models` logic. Web = set the chat model selector. | Web, CLI, Channel | **client** |
| `/skill <name>` | **1:1 rename of `/use`** (alias). Web = insert as text → forward to agent. | Web, CLI, Channel | **agent** |
| `/cancel` | **kept**, NO aliases (legacy rule). | Web, CLI, Channel | **client** |
| `/agents` | **new noun cmd** — reuses `/show agents` + `/list agents`. | CLI, Channel | — |
| `/tasks` | **1:1 rename of `/subagents`** (alias). | CLI, Channel | — |
| `/skills` | **new noun cmd** — reuses `/list skills`. | CLI, Channel | — |
| `/channels` | **new noun cmd** — reuses `/show channel` + `/list channels` + `/check channel`. | CLI, Channel | — |
| `/status` | **new noun cmd** — reuses bare `/show`. | CLI, Channel | — |
| `/config` | **1:1 rename of `/reload`** (alias). | CLI, Channel | — |
| ~~`/start`~~ | **dropped** as a user command (optional channel greeting only). | — | — |

### Migration mechanics (three distinct groups)
1. **1:1 renames → add old name as a hidden `Alias` on the new canonical:** `/use`→`/skill`, `/subagents`→`/tasks`, `/reload`→`/config`.
2. **Multiplexers → retained as hidden deprecated commands** for one release (their existing sub-handlers keep working), while their sub-behaviours are *also* exposed as the new noun commands: `/show`→`/status`(+`/model`/`/agents`/`/channels`), `/list`→`/skills`(+`/model`/`/agents`/`/channels`), `/switch`→`/model`(+`/channels`), `/check`→`/channels`. `/help` nudges to the noun commands.
3. **SPA-only labels (not backend):** `/new`, `/session new` are removed from the SPA and folded into the API-driven `/clear` entry (client delivery). They are **not** registry aliases.

All aliases + deprecated multiplexers are excluded from `/help`, the channel menu, and `GET /commands`.

### In scope
Registry `Surfaces`+`Delivery`; executor surface gating; the rename+alias map; channel-menu surface filtering; surface-filtered `/help`; `GET /api/v1/commands?surface=`; SPA palette from API + deletion of both hardcoded lists + delivery dispatch; tests every layer; docs (`using-omnipus-ui.md`, `channels.md`, `configuration.md` command tables).

### Out of scope / Non-behaviors
- **Must not** change what any command *does* (behavior preserved; only name/surface/source-of-truth change).
- **Must not** add new *functionality*: the noun commands (`/model`,`/agents`,`/tasks`,`/skills`,`/channels`,`/status`,`/config`) **restructure** existing verb-multiplexers/renames and **reuse their handlers** (behaviour preserved). No brand-new capabilities; no per-agent dynamic commands (skills unchanged).
- **Must not** add a hand-written wire type (Constraint #8 — `SlashCommand` is generated).
- **Must not** give `/cancel` any alias.
- **Must not** hard-error when a non-web command is typed in web — it **passes through to the model** (option B).
- **Must not** touch `omnipus <subcommand>` CLI binary commands.

---

## 6. User Stories & Acceptance Criteria

### US-1 — Harmonized, Claude-Code-style names with back-compat — **P0**
*As any user, I want consistent noun-based command names, while my old commands keep working for a release.*
**Why P0:** the naming is the headline ask; breaking old names would regress every channel user.
**Independent test:** `Lookup("subagents")` and `Lookup("tasks")` resolve to the same definition; `/help` shows `/tasks`, not `/subagents`.
1. **Given** the registry, **When** I look up a canonical name (`tasks`, `model`, `skill`, `config`, `clear`), **Then** it resolves to its definition.
2. **Given** an old name (`subagents`, `switch`, `use`, `reload`, `new`, `session new`), **When** I look it up, **Then** it resolves to the **same** canonical definition (alias) — for one release.
3. **Given** `/help` on any surface, **When** it renders, **Then** only **canonical** names appear (no aliases/deprecated).
4. **Given** `/cancel`, **When** I inspect its aliases, **Then** there are **none** (legacy rule preserved).

### US-2 — Surface-aware execution (web pass-through) — **P0**
*As a web user, typing a screen-backed command (e.g. `/agents`) should NOT run in chat — it should pass through to the model — but it must still run in CLI/channels.*
**Why P0:** this is option B and the core of "surface awareness."
**Independent test:** call `Execute` with `Request{Channel:"webchat", Text:"/agents"}` → `OutcomePassthrough`; with `Channel:"cli"` → `OutcomeHandled`.
1. **Given** a command whose `Surfaces` excludes Web, **When** it's executed with `Channel:"webchat"`, **Then** the result is `OutcomePassthrough` (the text reaches the model, no handler runs).
2. **Given** the same command, **When** executed with `Channel:"cli"` or any channel name, **Then** it runs (`OutcomeHandled`).
3. **Given** a command with empty `Surfaces`, **When** executed from any origin, **Then** it runs everywhere (back-compat default).
4. **Given** a web-surfaced command (`/clear`), **When** executed with `Channel:"webchat"`, **Then** it runs (or is client-handled — see US-4).
5. **Given** normal text (not a command) from any origin, **When** executed, **Then** `OutcomePassthrough` (unchanged).

### US-3 — `GET /api/v1/commands?surface=` endpoint — **P0**
*As the SPA, I fetch the surface-applicable commands so the palette is never hand-maintained.*
**Why P0:** the single source of truth for the UI.
**Independent test:** `GET /commands?surface=web` → exactly the 5; `?surface=cli` → all 11; each item is a valid `SlashCommand`.
1. **Given** the endpoint, **When** I `GET /commands?surface=web` (authenticated), **Then** I get exactly `clear, help, model, skill, cancel` as `SlashCommand[]`.
2. **Given** `?surface=cli` or `?surface=channel`, **Then** I get all 11 canonical commands.
3. **Given** no `surface` param, **Then** it defaults to `web`.
4. **Given** the response, **Then** each item carries `name, label, description, delivery` (+ optional `usage`, `aliases`, `available_while_streaming`); **aliases/deprecated names are NOT returned as their own entries**.
5. **Given** `delivery`, **Then** `clear/help/model/cancel = client` and `skill = agent`.
6. **Given** an unauthenticated request, **Then** 401.

### US-4 — Web palette derived from the API; both hardcoded lists deleted — **P0**
*As a web user, the `/` palette shows exactly what the backend says, and each command dispatches correctly.*
**Why P0:** kills the divergence permanently.
**Independent test:** with `fetchCommands` mocked to the 5, the palette renders 5; selecting `/clear` resets the session (client); selecting `/skill` inserts text to forward.
1. **Given** the composer, **When** I type `/`, **Then** the palette renders the commands from `GET /commands?surface=web` (no hardcoded array).
2. **Given** a `delivery:client` command (`/clear`,`/help`,`/model`,`/cancel`), **When** I select it, **Then** the SPA runs the client handler keyed by `name` and does **not** send it to the backend.
3. **Given** a `delivery:agent` command (`/skill <name>`), **When** I select it, **Then** the SPA inserts `/skill ` as text and (on send) forwards it via the message frame.
4. **Given** the code after this change, **Then** `SLASH_COMMANDS` (ChatScreen) and the `MessageInput` built-in suggestion array are **removed**; dynamic `/skill <id>` entries still come from `fetchSkills`.
5. **Given** streaming, **Then** only `available_while_streaming` commands (`/cancel`) show — driven by the field, not a hardcoded flag.

### US-5 — Channel menu + `/help` filtered by surface — **P1**
*As a channel/CLI user, the platform menu and `/help` show only what applies to my surface.*
**Independent test:** `RegisterCommands` receives only channel-surfaced defs; `/help` from `Channel:"webchat"` lists the 5, from `cli` lists 11.
1. **Given** `manager.StartAll`, **When** it registers a channel's menu, **Then** only commands whose `Surfaces` include Channel are sent.
2. **Given** `/help` executed with a given origin, **Then** the output lists only commands available on that surface, canonical names only.

### Edge Cases
- **Alias on a non-web surface in web:** typing `/subagents` (alias of `/tasks`, which is non-web) in web → resolves to `/tasks`, whose Surfaces exclude Web → **Passthrough** (consistent with the canonical).
- **Empty `Surfaces` (legacy/forgotten):** treated as all-surfaces (runs everywhere) — safe default, never accidentally hidden.
- **Unknown `?surface=` value:** 400 (or default to web — pick one; spec says **default to web** and ignore unknown, to be lenient).
- **`/skill` with no name:** palette inserts `/skill ` (trailing space); user completes; if sent bare, the agent handles "no skill specified."
- **Sub-commands of canonical (`/model gpt-x`, `/list`→deprecated):** `/model <arg>` still parses args; deprecated `/list models` still works (hidden) for one release.
- **`/commands` while skills are loading:** the static 5 come from the registry; dynamic `/skill <id>` entries merge in once `fetchSkills` resolves (don't block the palette).
- **Cross-surface delivery mismatch:** `delivery` is the **web** delivery; the field is only meaningful for web-surfaced commands; non-web commands MAY omit it (default `agent`).

---

## 7. Behavioral Contract / Non-Behaviors / Integration Boundaries

### Behavioral Contract
- When a command is executed, the system **maps the origin to a surface and runs the command only if its `Surfaces` allow that surface**, else passes the text through to the model.
- When the SPA needs the palette, it **fetches the web-surfaced commands** and renders them; it never hardcodes the list.
- When a palette command is `client`, the SPA **handles it locally**; when `agent`, it **forwards it as text**.
- When a channel registers its menu, it **registers only channel-surfaced commands**.
- When `/help` runs, it **lists only the caller-surface canonical commands**.
- When an old name is used, it **resolves to the canonical command** (for one release) and is **absent from help/menu/`/commands`**.

### Explicit Non-Behaviors
(see §5 Out of scope) — plus: the system **must not** return alias names as standalone entries from `/commands`; **must not** execute a non-web command typed in web; **must not** change the existing `/cancel` semantics or add its aliases.

### Integration Boundaries
| System | In/Out | Contract | Failure | Dev |
|---|---|---|---|---|
| `GET /api/v1/commands?surface=` | out `SlashCommand[]` | new OpenAPI schema | SPA: on fetch error, palette shows nothing (no crash) + counter; non-blocking | real handler; mocked in SPA tests |
| Channel platform menus | out: channel-surfaced defs | `CommandRegistrarCapable` | per-channel failures logged WARN, boot continues (existing FR-28) | existing |
| Agent loop executor | in: `Request{Channel,Text}` | `ExecuteResult{Outcome}` | pass-through is the safe default | unit-tested pure |

---

## 8. BDD Scenarios

```gherkin
Feature: Slash-command harmonization & surface-aware single source of truth

  # US-1
  Scenario: Old name resolves to canonical (alias)                       # Happy
    Given the command registry
    When I look up "subagents"
    Then it resolves to the same definition as "tasks"
    Traces to: US-1/AS-2

  Scenario: Help shows canonical names only                              # Happy
    Given /help on the cli surface
    When it renders
    Then "/tasks" appears and "/subagents" does not
    Traces to: US-1/AS-3

  Scenario: /cancel has no aliases                                       # Edge
    Given the /cancel definition
    Then its aliases list is empty
    Traces to: US-1/AS-4

  # US-2
  Scenario Outline: Surface gating per origin                            # Happy/Edge
    Given a command "<cmd>" whose Surfaces are <surfaces>
    When Execute runs with Channel "<origin>" and text "/<cmd>"
    Then the outcome is "<outcome>"
    Traces to: US-2/AS-1, US-2/AS-2
    Examples:
      | cmd     | surfaces            | origin   | outcome     |
      | agents  | CLI,Channel         | webchat  | Passthrough |
      | agents  | CLI,Channel         | cli      | Handled     |
      | agents  | CLI,Channel         | telegram | Handled     |
      | clear   | Web,CLI,Channel     | webchat  | Handled     |
      | config  | CLI,Channel         | webchat  | Passthrough |

  Scenario: Empty Surfaces runs everywhere                               # Edge
    Given a command with empty Surfaces
    When Execute runs with Channel "webchat"
    Then the outcome is Handled
    Traces to: US-2/AS-3

  Scenario: Normal text passes through                                   # Happy
    Given the text "hello there" from Channel "webchat"
    When Execute runs
    Then the outcome is Passthrough and no handler ran
    Traces to: US-2/AS-5

  Scenario: Web alias of a non-web command passes through                # Edge
    Given "/subagents" (alias of non-web "/tasks") from Channel "webchat"
    When Execute runs
    Then the outcome is Passthrough
    Traces to: US-2/AS-1 (edge: alias)

  # US-3
  Scenario: /commands?surface=web returns the five                       # Happy
    Given an authenticated GET /api/v1/commands?surface=web
    Then the body is exactly [clear, help, model, skill, cancel] as SlashCommand[]
    And no alias names appear as entries
    Traces to: US-3/AS-1, US-3/AS-4

  Scenario: /commands?surface=cli returns all eleven                     # Happy
    Given GET /api/v1/commands?surface=cli
    Then the body contains all 11 canonical commands
    Traces to: US-3/AS-2

  Scenario: default surface is web                                       # Alternate
    Given GET /api/v1/commands (no surface param)
    Then the body equals the surface=web result
    Traces to: US-3/AS-3

  Scenario: delivery field is correct                                    # Happy
    Given GET /api/v1/commands?surface=web
    Then clear/help/model/cancel have delivery "client" and skill has "agent"
    Traces to: US-3/AS-5

  Scenario: unauthenticated /commands is 401                             # Error
    Given an unauthenticated GET /api/v1/commands
    Then the response is 401
    Traces to: US-3/AS-6

  # US-4
  Scenario: Palette renders from the API                                 # Happy
    Given fetchCommands returns the five
    When I type "/" in the composer
    Then the palette renders those five (no hardcoded array)
    Traces to: US-4/AS-1

  Scenario: client-delivery command is handled locally                   # Happy
    Given the palette
    When I select "/clear" (delivery client)
    Then the session resets locally and no message is sent to the backend
    Traces to: US-4/AS-2

  Scenario: agent-delivery command is forwarded as text                  # Happy
    Given the palette
    When I select "/skill" (delivery agent)
    Then "/skill " is inserted as text and forwarded on send via the message frame
    Traces to: US-4/AS-3

  Scenario: hardcoded lists are gone                                     # Edge
    Given the codebase after this change
    Then SLASH_COMMANDS in ChatScreen.tsx and the MessageInput built-in suggestion array are removed
    Traces to: US-4/AS-4

  Scenario: streaming filter is field-driven                             # Edge
    Given a streaming turn
    When I open the palette
    Then only commands with available_while_streaming=true (cancel) are shown
    Traces to: US-4/AS-5

  # US-5
  Scenario: channel menu gets only channel-surfaced commands             # Happy
    Given manager.StartAll registers a channel's menu
    Then only commands whose Surfaces include Channel are sent to RegisterCommands
    Traces to: US-5/AS-1

  Scenario: /help is surface-filtered                                    # Happy
    Given /help executed from Channel "webchat"
    Then it lists the 5 web commands; from "cli" it lists all 11
    Traces to: US-5/AS-2
```

---

## 9. TDD Plan

| # | Test | Level | Traces | Notes |
|---|---|---|---|---|
| 1 | `surfaceForChannel`: "webchat"→Web, "cli"→CLI, others→Channel | Unit (Go) | US-2 | pure map |
| 2 | `Definition.Surfaces` empty → allowed on all surfaces | Unit | US-2/AS-3 | |
| 3 | `Execute` gating: non-web cmd + webchat → Passthrough; +cli → Handled | Unit | US-2/AS-1,2 | table-driven (DS-1) |
| 4 | `Execute`: normal text → Passthrough (regression) | Unit | US-2/AS-5 | |
| 5 | registry alias: `Lookup("subagents")` == `Lookup("tasks")` | Unit | US-1/AS-2 | all rename pairs (DS-2) |
| 6 | `/cancel` has zero aliases (keep existing) | Unit | US-1/AS-4 | cmd_cancel_test.go |
| 7 | `defToSlashCommand` mapper: fields + delivery + alias-exclusion | Unit | US-3/AS-4,5 | the registry→wire mapper |
| 8 | help formatter: surface filter + canonical-only | Unit | US-1/AS-3, US-5/AS-2 | |
| 9 | channel registration filter: only Channel-surfaced defs | Unit/Integration | US-5/AS-1 | |
| 10 | `GET /commands?surface=web` → exactly 5; `cli` → 11; default web; 401 | Integration (Go gateway) | US-3/AS-1,2,3,6 | mirror /skills handler test |
| 11 | SPA: palette renders from mocked fetchCommands | Integration (vitest) | US-4/AS-1 | |
| 12 | SPA: delivery dispatch — client handled locally, agent forwarded | Integration | US-4/AS-2,3 | spy on send + client handlers |
| 13 | SPA: streaming shows only available_while_streaming | Integration | US-4/AS-5 | |
| 14 | grep guard: no `SLASH_COMMANDS` array remains in chat | Unit (lint/test) | US-4/AS-4 | repo-grep assertion |
| 15 | contract: `make verify-contracts` no diff | Gate | US-3 | committed generated artifacts |
| 16 | e2e: type `/agents` in web → passes through to model (LLM sees it); `/clear` resets; palette shows 5 | E2E (Playwright) | US-2/AS-1, US-4 | LLM-light |

### Datasets
**DS-1 — surface gating** (cmd × origin → outcome): rows from the §8 Scenario Outline (agents×{webchat,cli,telegram}, clear×webchat, config×webchat, empty-surfaces×webchat, normal-text×webchat).
**DS-2 — alias resolution**: each pair (`subagents`→`tasks`, `switch`→`model`, `use`→`skill`, `reload`→`config`, `new`→`clear`, `session new`→`clear`) + a non-alias canonical control.
**DS-3 — `/commands` per surface**: web→{clear,help,model,skill,cancel}; cli→all 11; channel→all 11; `?surface=bogus`→web; unauth→401.

### Regression
- **Modifies existing:** the executor pass-through path for normal text MUST be unchanged (test #4). Existing per-command behavior tests must pass after rename (update names; behavior identical). `cmd_cancel_test.go` unchanged.
- **Existing chat tests WILL break on deletion and must be rewritten to the API-driven palette:** `src/components/chat/ChatScreen.test.tsx` (the slash-palette assertions) and `src/components/chat/MessageInput.slash.test.tsx` (the built-in `/clear` + skill suggestions). Tests #11/#12 replace their palette assertions; the dynamic-skill suggestion path (`fetchSkills`) is preserved.
- **Channel menu**: existing registration tests updated to expect channel-surfaced subset.

---

## 10. Functional Requirements
- **FR-001 (MUST):** `Definition` gains `Surfaces []Surface` (enum Web/CLI/Channel) and `Delivery DeliveryMode` (Client/Agent, default Agent). Empty `Surfaces` = all surfaces.
- **FR-002 (MUST):** `Execute` maps `Request.Channel` → Surface (`webchat`→Web, `cli`→CLI, else→Channel) and returns `OutcomePassthrough` when the matched command's non-empty `Surfaces` excludes that surface (no handler runs).
- **FR-003 (MUST):** Commands are renamed per §5; old names resolve to the canonical definition as **hidden aliases / deprecated commands** for one release; aliases never appear in `/help`, the channel menu, or `GET /commands`.
- **FR-004 (MUST):** `/cancel` has **no** aliases (existing rule preserved + test).
- **FR-005 (MUST):** `clear, help, model, skill, cancel` include Web in `Surfaces`; `agents, tasks, skills, channels, status, config` do **not** include Web.
- **FR-006 (MUST):** `GET /api/v1/commands?surface=web|cli|channel` (BearerAuth, default `web`) returns the canonical, surface-applicable commands as generated `SlashCommand[]` — no alias entries.
- **FR-007 (MUST):** `SlashCommand.delivery` = `client` for clear/help/model/cancel, `agent` for skill.
- **FR-008 (MUST):** The web palette renders from `fetchCommands('web')`; the hardcoded `SLASH_COMMANDS` (ChatScreen), the `MessageInput` built-in suggestion array, **and the `HELP_TEXT` constant** are **deleted** — `/help` (client delivery) renders from the fetched command list; dynamic `/skill <id>` entries still derive from `fetchSkills`.
- **FR-009 (MUST):** The SPA dispatches by `delivery`: `client` → run the local handler keyed by `name`; `agent` → insert as text and forward via the message frame.
- **FR-010 (MUST):** Channel menu registration passes only Channel-surfaced defs.
- **FR-011 (MUST):** `/help` lists only the caller-surface canonical commands.
- **FR-012 (MUST):** `SlashCommand` is contract-generated (Constraint #8); `make verify-contracts` is clean; no hand-written wire type.
- **FR-013 (MUST):** Each command's effect is preserved **per surface** by reusing the existing handlers — e.g. `/clear` = new local chat in web, `rt.ClearHistory()` (clear server session) in CLI/channel (existing surface-appropriate behaviours, unchanged). FR-013 is per-surface, not a single global effect.
- **FR-014 (MUST):** Typing a non-web command in the web chat passes the raw text through to the model (no execute, no error).
- **FR-015 (SHOULD):** Unknown `?surface=` defaults to `web` (lenient).

## 11. Success Criteria
- **SC-001:** `GET /commands?surface=web` returns exactly 5 (`clear,help,model,skill,cancel`).
- **SC-002:** `?surface=cli` and `?surface=channel` each return all 11 canonical commands; aliases absent.
- **SC-003:** `Execute(Request{"webchat","/agents"})` → Passthrough; `Execute(Request{"cli","/agents"})` → Handled (and similarly for all 6 non-web commands).
- **SC-004:** `Lookup` resolves every old name to its canonical; none of the old names appear in `/help` or `/commands`.
- **SC-005:** `rg "SLASH_COMMANDS|HELP_TEXT" src/components/chat/` returns 0; the palette **and** `/help` derive from the API/registry.
- **SC-006:** `make verify-contracts` no diff; gofmt/golangci-lint/go test/vitest/typecheck pass.
- **SC-007:** `/cancel` aliases == `[]` (test asserts).

## 12. Traceability Matrix
| FR | US | BDD | Tests |
|---|---|---|---|
| FR-001 | US-2 | Empty Surfaces runs everywhere | #2 |
| FR-002 | US-2 | Surface gating outline; normal text | #1,#3,#4 |
| FR-003 | US-1 | Old name resolves; help canonical-only | #5,#8 |
| FR-004 | US-1 | /cancel no aliases | #6 |
| FR-005 | US-3 | /commands web vs cli | #7,#10 |
| FR-006 | US-3 | /commands?surface; default web; 401 | #10, DS-3 |
| FR-007 | US-3 | delivery field correct | #7,#10 |
| FR-008 | US-4 | palette from API; hardcoded gone | #11,#14 |
| FR-009 | US-4 | client handled / agent forwarded | #12 |
| FR-010 | US-5 | channel menu filtered | #9 |
| FR-011 | US-5 | /help surface-filtered | #8 |
| FR-012 | US-3 | (contract gate) | #15 |
| FR-013 | US-1 | (behavior-preserving — existing per-cmd tests) | renamed existing |
| FR-014 | US-2 | web alias/non-web passes through | #3,#16 |
| FR-015 | US-3 | default surface is web | #10 |

> Every BDD scenario traces to a US and ≥1 FR; every FR appears here.

---

## 13. Ambiguity Self-Audit
| # | Item | Resolution |
|---|---|---|
| A1 | Endpoint shape | **`?surface=web|cli|channel`**, default web (operator). |
| A2 | `delivery` location | **In the contract** (registry-driven) (operator). |
| A3 | `/help` web delivery | Client renders from the fetched list (no backend round-trip needed in web). Accepted. |
| A4 | `/model` web behavior | Client-handled: sets the chat model selector (mirrors the header dropdown); does not change server default. Accepted. |
| A5 | `/show`,`/list`,`/check` migration | Retained as **hidden deprecated commands** (existing handlers) for one release, not pure aliases (they had subcommands). Accepted. |
| A6 | Unknown `?surface=` | Default to `web` (lenient), not 400. Accepted (FR-015). |
| A7 | Alias-removal timeline | "One release" — track via a follow-up issue; aliases hidden meanwhile. Accepted. |
| A8 | `delivery` for non-web commands | Field optional / default `agent`; only meaningful for web-surfaced. Accepted. |

All resolved/accepted.

## 14. Holdout Evaluation Scenarios (post-impl; NOT in traceability)
- **H1:** In the web chat, type `/` → you see exactly 5 entries (clear, help, model, skill, cancel) with descriptions.
- **H2:** Type `/agents` in web and send → the assistant replies as if you asked about agents in prose (it passed through), and the Agents *screen* did not open.
- **H3:** In a Telegram chat, the bot's command menu lists the channel set (incl. `/tasks`, `/channels`) and `/subagents` still works but isn't in the menu.
- **H4 (error):** Kill the `/commands` endpoint (404) → the web palette simply shows nothing; the composer still sends normal messages.
- **H5 (error):** `/cancel` mid-stream still stops the turn from the palette.
- **H6 (edge):** `/clear` in web starts a fresh chat (local), and in the `omnipus agent` CLI it clears the server session — same name, surface-appropriate effect.
- **H7 (edge):** `/model opus` from the palette switches the chat's model selector; `/model` with no arg shows the current model.

## 15. Assumptions & Future Work
- **Assumes** `Request.Channel` continues to carry the origin ("webchat"/"cli"/channel name) — verified.
- **Future:** per-agent dynamic commands via `?agent_id=` on `/commands` (schema already allows extension); removing the deprecated aliases after one release; a CLI help UI consuming `?surface=cli`.
