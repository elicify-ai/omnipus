# Agent Create & Edit Forms — Requirements

**Status:** Approved (review pending)
**Date:** 2026-06-18
**Author:** Daniel Piatkowski `<10800669+daniel-piatkowski-ai@users.noreply.github.com>`
**Branch target:** `hotfix/v0.1.1` (Wave 6 follow-up)
**Companion spec:** [`agent-config-matrix-spec.md`](./agent-config-matrix-spec.md) — the source-of-truth property matrix this doc implements
**Replaces:** the 2-type wizard (`custom` / `worker`) and the 12-accordion edit slide-over

> **2026-07-04 — `sandbox_profile` removed entirely** (ADR-035): it never
> differentiated the actual kernel-enforced boundary per agent — that
> boundary is a single global Landlock/seccomp policy applied at gateway
> boot, identical for every agent. `shell_policy` is unaffected. References
> to `sandbox_profile` and `inherit_sandbox` throughout this document
> (including row 17, §4.14, and the Advanced-disclosure field lists) are
> historical.

This document specifies the user-facing create and edit flows for Omnipus
agents, including the 3-type taxonomy, the property matrix, the
wireframe-by-wireframe UI mapping, the wire schema diff, and the
acceptance criteria.

---

## 1. Goals & non-goals

### Goals
1. **Three distinct agent types** — `Main`, `Subagent`, `Subagent (External)` — with a clean, well-documented property matrix for each.
2. **Type choice at the roster level** — three distinct "+ Add" actions on the agents list, not a type-picker step inside the wizard.
3. **Conditional UI per type** — each field is shown, hidden, or read-only based on `type`; no irrelevant fields clutter the form.
4. **Auto-save on edit** — the edit slide-over commits every change with a 500ms debounce; no "Apply" button.
5. **Single source of truth for the wire contract** — `AgentCreateRequest.yaml`, `AgentUpdateRequest.yaml`, `Agent.yaml`, `ExecutorConfig.yaml` are updated in one PR.

### Non-goals (out of scope for this iteration)
- Bulk import of agents (e.g., from a YAML manifest).
- Templates / cloning existing agents. Future work.
- Per-channel routing UI for tool feedback. Already handled at runtime (webchat never, messaging always).
- ~~The full `delegation_policy` editor. That lives in the trust graph UI (`/agents/trust`); this spec only covers the *link* to it.~~ **Removed by [ADR-037](../architecture/ADR-037-remove-global-delegation-policy.md)** — the per-agent `delegation_policy` field and the `/agents/trust` editor are deleted; delegation is workspace-scoped (workspace Team tab). Treat every `delegation_policy` / `/agents/trust` reference in this spec as historical.

---

## 2. The three user-creatable types

| Type | Lifecycle | Runtime | Audience |
|---|---|---|---|
| **`type: Main`** | Chat colleague (human ↔ agent) | Omnipus engine (native) | The user |
| **`type: Subagent`** | Delegation-only worker | Omnipus engine (native) | The Main agent (via delegation) |
| **`type: subagent_3p`** | Delegation-only worker | External CLI (`claude-code` / `codex` / `opencode`) | The Main agent (via delegation) |

The wire values use **snake_case for the external-worker variant** and
**PascalCase for the user-creatable chat + native-worker variants** —
chosen for backward-compat with the codebase's identifier convention
(`one-at-a-time`, `queue-and-process`, `claude-code`, `subagent_3p`).
The display strings in the UI are `Main`, `Subagent`, and `Subagent
(External)`.

Plus the **locked core roster** (the "built-in Main" column — currently
`Mia`, `Jim`, `Ava`, `Ray`). Built-ins are not user-creatable.

**Important (resolves migration ambiguity):** Built-in agents keep
`type: core` in the wire. The new `Main` value is **only** for
user-created agents. `GET /api/v1/agents/{id}` on a built-in returns
`type: core, locked: true` — **not** `type: Main`. The `locked: true`
flag is the property-based signal that an agent is built-in; the
`type: core` value is the enum signal. The matrix column header is
called "built-in Main" for human readability, but the wire enum value
the server emits for those rows is `core`.

The "built-in Subagent" column is reserved for future shipped skill
agents.

---

## 3. Property matrix

Convention:
- **R** = Required (cannot save without a value)
- **O** = Optional (writable, may be omitted)
- **ro** = Read-only (visible, not editable)
- **derived** = Set automatically by the server/client based on `type`; not a form field
- **—** = Hidden / not applicable to this type
- **external editor** = Edited in a dedicated UI surface (not the agent form)

### 3.1 The full matrix

**Naming convention used in this matrix and throughout the doc:**
- `type: Main` (monospace) refers to the **wire enum value**.
- Bare "Main" in prose refers to the same type; for clarity at
  sentence starts, "the Main type" or "agents with type Main" is
  preferred. The same applies to `Subagent` and `subagent_3p`.
- `Main` is never used as a generic English word in this spec
  (resolves F-09 ambiguity).

| # | Property (exact wire name) | built-in Main | built-in Subagent | Main | Subagent | Subagent (External) |
|---|---|---|---|---|---|---|
| 1 | `name` | ro | ro | R | R | R |
| 2 | `type` (wire enum) | ro (`Main`) | ro (`Subagent`) | **derived** (`Main`) | **derived** (`Subagent`) | **derived** (`subagent_3p`) |
| 3 | `description` | ro | ro | O | **R** | **R** |
| 4 | `color` | R (seeded) | R (seeded) | R | R | R |
| 5 | `icon` | R (seeded) | R (seeded) | R | R | R |
| 6 | `model` | R (seeded) | R (seeded) | R (picker) | R (picker) | R (free text) |
| 7 | `model_params.{temperature, max_tokens, top_p}` | ro | ro | O | O | **—** |
| 8 | `soul` ⭐ (mandatory everywhere) | ro | ro | **R** ⭐ | **R** ⭐ | **R** ⭐ |
| 9 | `instructions` | ro | ro | O | O | O |
| 10 | `heartbeat` | ro | **—** | O | **—** | **—** |
| 11 | `heartbeat_enabled` | O | **—** | O | **—** | **—** |
| 12 | `heartbeat_interval` | O | **—** | O | **—** | **—** |
| 13 | `voice` | O | **—** | O | **—** | **—** |
| 14 | `tools_cfg` | ro (seeded) | ro (seeded) | O | O | **—** |
| 15 | `skills[]` | ro (seeded) | ro (seeded) | O | O | **—** |
| 16 | `fallback_models[]` (max 2) | ro (seeded) | ro (seeded) | O | O | **—** |
| 17 | `sandbox_profile` | O | O | O | O | **—** |
| 18 | `shell_policy.{enable_deny_patterns, custom_deny_patterns[]}` | O | O | O | O | **—** |
| 19 | `executor.kind` | derived (`native`) | derived (`native`) | derived (`native`) | derived (`native`) | derived (`external-cli`) |
| 20 | `executor.cli` | — | — | — | — | R |
| 21 | `executor.cli_path` | — | — | — | — | R |
| 22 | `executor.env_overrides` | — | — | — | — | O |
| 23 | `executor.cli_args` | — | — | — | — | O |
| 24 | `rate_limits.{use_global_defaults, max_llm_calls_per_hour, max_tool_calls_per_minute, max_cost_per_day}` | O | O | O | O | O |
| 25 | ~~`delegation_policy.{to[], accept_from, modes, depth, budget}`~~ **removed by ADR-037** (workspace-scoped; workspace Team tab) | — | — | — | — | — |
| 26 | `default` (G3 toggle) | ro (Mia seeded) | **—** | O | **—** | **—** |
| 27 | `timeout_seconds` | R (seeded) | R (seeded) | R (UI default 300) | R (UI default 300) | R (UI default 300) |
| 28 | `max_tool_iterations` | R (seeded) | R (seeded) | R (UI default 50) | R (UI default 50) | R (UI default 50) |
| 29 | `steering_mode` | R (seeded) | **—** | R (UI default `one-at-a-time`) | **—** (internal: `one-at-a-time`) | **—** (internal: `one-at-a-time`) |

### 3.2 Runtime / status fields (not in the form)

| Property | All types |
|---|---|
| `id` | ro (server-assigned UUID for custom; well-known string for built-ins) |
| `status` | ro (`idle` / `active` / `error` / `draft`) |
| `warning` | ro (advisory from save+reload outcome — empty when healthy) |
| `created_at`, `updated_at` | ro |
| `locked` | ro (server-set; `true` for built-ins only) |
| `stats` | ro (per-agent runtime metrics) |

### 3.3 Fields deliberately dropped from the wire

| Dropped field | Why |
|---|---|
| `tool_feedback` (per-agent bool) | This is **channel behavior**, not an agent property. The web chat surface already renders tool calls inline (`SessionPanel.tsx`, `ToolCallBadge`, `SubagentBlock`); publishing them again as outbound messages is duplication. Per-channel routing at runtime: `webchat` → never, messaging → always. |
| `executor.auth_method` | Each CLI handles its own auth (claude-code OAuth, etc.). Omnipus just spawns the CLI process. |
| `executor.instructions_file` | Omnipus passes `soul` + `instructions` as prompt content at runtime, not via a file the CLI reads. |
| `executor.model_override` | `model` IS the model name passed to the CLI. |
| `executor.workdir` | Set by the calling Main agent at runtime (invocation context), not by the Subagent itself. |

---

## 4. Per-row rationale (the "why")

### 4.1 `name` (row 1)
Identifies the agent in the list and command-center pickers. Required
on every user-creatable type. Built-ins carry seeded names
(`Mia`, `Jim`, `Ava`, `Ray`).

### 4.2 `type` (row 2)
A **lifecycle choice**, not a runtime config. Made once at create
(router-level), immutable after. The runtime (`executor.kind`) is implied:
- `type: Main` → `executor.kind: native`
- `type: Subagent` → `executor.kind: native`
- `type: subagent_3p` → `executor.kind: external-cli`

So `executor.kind` is **derived**, never a form field.

### 4.3 `description` (row 3)
For `type: Main`, the description is a human-readable subtitle on the
agent card (optional). For `type: Subagent` and `type: subagent_3p`,
the description is the **basis on which an orchestrator decides which
agent to delegate to**. Without it, the worker is unroutable. Hence
**required for workers** — and "required" means **non-empty after
trim** (the wire schema enforces `minLength: 1`):

- **CREATE** (`POST /api/v1/agents`):
  - `type: Main` — `description` is optional (may be absent, may be empty string).
  - `type: Subagent` — `description` MUST be present and non-empty after trim. **400** otherwise.
  - `type: subagent_3p` — same as Subagent.
- **UPDATE** (`PUT /api/v1/agents/{id}`):
  - Omitting `description` in the body **leaves the existing value unchanged** (no error).
  - Setting `description` to an empty string after trim is a **400** for Subagent / subagent_3p; a no-op for Main (empty string is allowed; backend stores it as the new value).

**Wire schema:** `description: { type: string, minLength: 1 }` on the
create request. The backend trims before length-validation, so
`"   "` (whitespace-only) is treated as empty and rejected.

### 4.4 `color` (row 4) and `icon` (row 5)
**Visual identity is mandatory.** Every agent needs a swatch + icon in the
Agents list and command-center pickers.

- **R at the wire layer** — POST without `color`/`icon` returns 400.
- **UI pre-selects a default before submission**, so the user never sees an empty field:
  - `color` default = the first entry of `AVATAR_COLORS_BY_NAME` (resolved through the `avatarColorName()` helper in `src/lib/constants.ts`; current first entry is `Verdant`)
  - `icon` default = `"Robot"`
- The `avatarColorName()` helper is the **single source of truth** for the color list and its semantic labels (`Verdant, Azure, Amethyst, Saffron, Ember, Crimson, Slate, Forge Gold`) — the form must read from it, never hard-code values.

### 4.5 `model` (row 6)
Mandatory everywhere. **Input widget differs by type**:
- **Main / Subagent**: model picker (filtered by connected providers per
  Wave C4 G12's "unresolved" indicator)
- **Subagent (External)**: free-text slug input — passed verbatim to
  the CLI invocation (`claude --model <slug>`)

### 4.6 `model_params.{temperature, max_tokens, top_p}` (row 7)
**Hidden for Subagent (External)** — Omnipus can't guarantee every CLI
supports these flags. Hidden beats silent dead behavior.

### 4.7 `soul` (row 8) — ⭐ MANDATORY EVERYWHERE
The SOUL.md content. **Required for every user-creatable type**, including
Subagent (External). For Main and Subagent, Omnipus injects it into every
LLM call. For Subagent (External), Omnipus **passes it as part of the CLI
prompt at runtime** — the CLI never reads a file from disk.

### 4.8 `instructions` (row 9)
AGENT.md body. Same scoping as `soul` (everywhere), but **optional**
(empty is valid).

### 4.9 `heartbeat*` (rows 10–12)
**Main only** — chat agents have a periodic poll. Workers don't poll
(they're invoked by their parent).

### 4.10 `voice` (row 13)
**Main only** — TTS for user-facing audio. Workers produce text or
structured output for their parent.

Widget behavior (Main / built-in Main only):
| Condition | UI |
|---|---|
| No voice provider configured globally | Field **disabled** with tooltip |
| Provider has a standardized voices enum (OpenAI TTS, ElevenLabs, Google, Azure, Piper) | **Dropdown** populated from the provider's voices API |
| Provider has no enum | **Free-text input** |

For **built-in Main** agents (Mia / Jim / Ava / Ray), the Voice field
is shown but **disabled** — see §6.5 and §11 #2. Built-in voice is
locked because the seeded voice is part of the persona's identity; the
operator can change the global voice provider's default but not Mia's
individual setting. (F-14 resolution: voice is *not* in the
operator-tunable subset for built-ins; voice IS in the
operator-tunable subset for user-created Main agents.)

### 4.10.1 `voice-provider-detect.ts` contract (F-07)

The runtime check that powers the widget behaviour lives in
`src/lib/agents/voice-provider-detect.ts` (NEW, per §8.3).

**Interface (TypeScript, proposed):**

```ts
// src/lib/agents/voice-provider-detect.ts

export type VoiceProviderMode = 'enum' | 'free-text' | 'disabled'

export interface VoiceProviderDetectResult {
  /** Which widget variant to render. */
  mode: VoiceProviderMode
  /** Populated when mode === 'enum'; the provider's voices array. */
  voices?: string[]
  /** Populated when mode === 'disabled'; surfaced as the tooltip text. */
  reason?: string
  /** ISO timestamp of when the result was fetched (for staleness checks). */
  fetchedAt: string
}

/**
 * Detect the current voice provider's capability and return the
 * widget mode + (optional) voices list.
 *
 * Behaviour:
 * - Fetches GET /api/v1/voice/provider (gateway source-of-truth for the
 *   active voice provider config).
 * - Result is cached with a 10-second SWR (stale-while-revalidate) in
 *   module scope; the agent-edit mount path triggers a fresh fetch.
 * - On 5xx, network failure, or non-array response: returns
 *   { mode: 'disabled', reason: 'Voice provider unavailable' }.
 *   This is the safe default — a broken provider must not break the
 *   agent edit slide-over.
 * - On 'enum' mode where the provider returned 0 voices, falls back
 *   to 'free-text' (empty enum is the same as no enum).
 */
export async function detectVoiceProvider(): Promise<VoiceProviderDetectResult>
```

**Provider-change subscription:** the agent-edit slide-over subscribes
to a `voice-provider-change` event (emitted by the Settings → Voice
section when the operator changes the provider) and re-calls
`detectVoiceProvider()` on receipt. The widget re-renders within the
10 s SWR window. While re-fetching, the existing field value is
preserved; only the **wrapper** (dropdown vs free-text) re-renders.

**Provider enum support matrix** (verified in §11 #2):

| Provider | Has voices enum | Endpoint |
|---|---|---|
| OpenAI TTS | yes (fixed 6 voices) | `GET /v1/voices` |
| ElevenLabs | yes (paginated) | `GET /v1/voices` |
| Google TTS | yes (WaveNet list) | `GET /v1/voices` |
| Azure Speech | yes (region-scoped) | `GET /voices/list` |
| Piper / Coqui (local) | yes (filesystem) | reads `~/.local/share/piper/voices/` |
| Anything else | no | — |

### 4.11 `tools_cfg` (row 14)

### 4.11 `tools_cfg` (row 14)
**Hidden for Subagent (External)**. The CLI bypasses Omnipus entirely;
Omnipus tools (`web_search`, `file_read`, `shell`, `code_exec`) are not
accessible to the CLI process. The frontend hides the field; the backend
**rejects with 400** if the field is present in the PUT body for a
`subagent_3p` agent (see §4.20 for the rejected-fields list).

### 4.12 `skills[]` (row 15)
**Hidden for Subagent (External)** — same reason as `tools_cfg`. Skills
are Omnipus-invoked.

Note: **Subagent (native) DOES have skills** — Omnipus pre-resolves a
skill result and passes it as context to the worker.

### 4.13 `fallback_models[]` (row 16)
**max 2** everywhere (was 10 — the schema change is part of this spec).
**Hidden for Subagent (External)** — the CLI handles its own retries.

### 4.14 `sandbox_profile` (row 17) and `shell_policy.*` (row 18)
Operator-tunable for all types where the runtime is Omnipus (Omnipus
enforces Landlock + shell deny patterns). **Hidden for Subagent
(External)** — the CLI manages its own isolation. The backend rejects
both fields with **400** if present in a PUT body for a `subagent_3p`
agent (see §4.20).

### 4.15 `executor.kind` (row 19) — derived
Never a form field. The user picks `type` at create time and the kind
follows. Removing the kind picker eliminates a redundant question.

### 4.16 `executor.cli` (row 20)
Required for Subagent (External) only. Other types run on Omnipus
engine — no CLI to pick. **Locked after create** (to switch CLIs, the
user must create a new agent).

### 4.17 `executor.cli_path` (row 21)
**Required** for Subagent (External) — passed to the CLI invocation.
Relying on `$PATH` is fragile when multiple CLI versions are installed.

### 4.18 `executor.env_overrides` (row 22)
Optional. Merged into the spawned process's env alongside Omnipus's
own (`OMNIPUS_AGENT_NAME`, `OMNIPUS_AGENT_TYPE`).

### 4.19 `executor.cli_args` (row 23)
Free-form additional CLI arguments. Light validation (warns on
shell-injection chars `;|``$()`) but does not reject.

### 4.19.1 Rejected fields on `subagent_3p` PUT (400)

For `type: subagent_3p` agents, the following fields are **never
valid** on `PUT /api/v1/agents/{id}`. The backend rejects them with
**400** and a `code: "field_not_applicable_to_type"` error:

- `tools_cfg` — CLI doesn't see Omnipus tools.
- `skills` — CLI doesn't see Omnipus skills.
- `fallback_models` — CLI handles its own retries.
- `model_params` — CLI may not support these flags.
- `sandbox_profile` — CLI manages its own isolation.
- `shell_policy` — same reason.
- ~~`delegation_policy` — workers don't delegate.~~ (field removed entirely by ADR-037; delegation is workspace-scoped)

This is a deliberate choice over silent-drop: silent-drop is the worst
of both worlds (200 OK + no UI feedback = "I set this and it doesn't
work" bug reports). **400-reject forces the UI to keep the field
hidden on External edit**, surfacing any UI bugs early.

The frontend hides all of these fields on External edit (per the
matrix §3.1 and §6.4), so a 400 from this rule is a UI bug — the form
should never let the user submit one of these fields.

### 4.20 `rate_limits.*` (row 24)
Optional everywhere. When `use_global_defaults: true` (default), the
global `agents.defaults.rate_limits` applies.

### 4.21 `delegation_policy.*` (row 25) — REMOVED by ADR-037
The per-agent `delegation_policy` field and the `/agents/trust` delegation-graph
editor were **deleted entirely by [ADR-037](../architecture/ADR-037-remove-global-delegation-policy.md)**.
Delegation trust is **workspace-scoped** — edit it in a workspace's **Team tab**
(the per-workspace `Delegation[]` edge list, `pkg/workspace/delegation.go`, has
been the sole runtime authority since commit `822202ad`). The `to[]` / `modes` /
`depth` enforcement described below now lives entirely on the workspace edge; the
`accept_from` / `budget` advisory fields no longer exist even in the retained
seed DTO. The struck text is kept only as a record of the removed surface.

> ~~Edited in the delegation graph UI (`/agents/trust`), never in the agent form.
> Main agents only. **v0.1.0 enforcement scope:** `to[]` ENFORCED (target must
> appear here; `*` allowed), `modes` ENFORCED (`await`/`background`/`task`),
> `depth` ENFORCED as a safety cap; `accept_from` / `budget` schema-present but
> NOT enforced (advisory-only, startup WARN if non-empty).~~

### 4.22 `default` (row 26)
**Main only** — the G3 toggle. Workers never default.

### 4.23 `timeout_seconds` (row 27) and `max_tool_iterations` (row 28)
R at the wire layer with UI default pre-selected (300s / 50).

For Subagent (External), `timeout_seconds` bounds the Omnipus wait for
the CLI process.

### 4.24 `steering_mode` (row 29)
**Main only** — controls how a Main agent handles a new human message
arriving while a previous turn is still running.

Workers don't show the field; the server always sets
`one-at-a-time` for them (default for Main, same internal value for
workers).

---

## 5. Create flow — wireframes

### 5.1 Agent roster — three distinct "+ Add" actions

The type choice happens **at the roster level**, not inside the wizard.
Three distinct "+ Add" buttons, one per type.

```
┌─ Agents ───────────────────────────────────────────[ × ]──┐
│                                                         │
│  ⌕ Search agents                                          │
│                                                         │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ + Add Main                                         │ │ ← Main
│  │   Chat colleague you talk to directly.              │ │
│  └─────────────────────────────────────────────────────┘ │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ + Add Subagent                                     │ │ ← Subagent
│  │   Delegation-only worker on Omnipus engine.        │ │
│  └─────────────────────────────────────────────────────┘ │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ + Add Subagent (External) ▾                        │ │ ← Subagent (External) — expands on hover/click
│  │   Delegation-only worker on an external CLI.       │ │
│  │   ┌─────────────────────────────────────────────┐   │ │
│  │   │ + Add (External) claude-code                │   │ │ ← each is its own button
│  │   │   → wizard opens with cli=claude-code locked │   │ │
│  │   │ + Add (External) codex                      │   │ │
│  │   │   → wizard opens with cli=codex locked      │   │ │
│  │   │ + Add (External) opencode                   │   │ │
│  │   │   → wizard opens with cli=opencode locked   │   │ │
│  │   └─────────────────────────────────────────────┘   │ │
│  └─────────────────────────────────────────────────────┘ │
│                                                         │
│  ─────────────────────────────────────────────────── │
│                                                         │
│  Your agents                                             │
│  ⭐ Research Assistant       Main · claude-sonnet-4-6 │
│  ...                                                    │
│                                                         │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ ⚠ Built-in roster (locked)                       ▾ │ │
│  │ ⭐ Mia   Main · claude-sonnet-4-6 · ● active     │ │
│  │ ⭐ Jim   Main · claude-sonnet-4-6 · ● active     │ │
│  │    Ava   Main · claude-sonnet-4-6 · ○ idle       │ │
│  │    Ray   Main · claude-sonnet-4-6 · ○ idle       │ │
│  └─────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

**Built-in roster default disclosure** (resolves F-18): expanded by
default on desktop (≥ `md` breakpoint), collapsed by default on phone
(`< sm`). The wireframe above shows the desktop-expanded state; on
phone, the user taps the `▾` disclosure to expand. Single rule,
applied consistently across §5.1 and §13.2.

### 5.2 Wizard structure — 3 numbered steps + Advanced

When the user clicks any of the 3 buttons, the wizard opens at **step ①**.
The `type` (and `cli`, for External) is shown as a locked chip at the top.

```
┌─ + New agent ──────────────────────────────────[ × ]──┐
│                                                       │
│  ┌──────────────────────────────────────────────────┐ │
│  │  Type:  Main                       (locked)  [×] │ │
│  │  CLI:   claude-code                (locked)      │ │  ← only on External wizard
│  └──────────────────────────────────────────────────┘ │
│                                                       │
│  *(The `CLI:` line is hidden on Main / Subagent wizards   │
│   and only rendered when the wizard is for an External   │
│   agent. The Type chip is the single source of truth for *
│   /  the wizard's branch.)                              *
│                                                       │
│  ① Identity   ② Personality   ③ Tools                  │
│  ●─────────────○──────────────○                          │
│                                                       │
│  (fields per the matrix for this type)                 │
│                                                       │
│  ▼ Advanced settings (8 sections)         [tap ▾]    │
│                                                       │
├───────────────────────────────────────────────────────┤
│            [← Back]   [ Skip ]   [ Next → ]            │
└───────────────────────────────────────────────────────┘
```

### 5.3 Wizard — `Main` (3 steps + Advanced)

| Step | Visible fields |
|---|---|
| **① Identity** | `color` (default: `Verdant`), `icon` (default: `Robot`), `name` *, `description`, `model` * (picker) |
| **② Personality** | `soul` * ⭐, `instructions`, `heartbeat`, `heartbeat_enabled`, `heartbeat_interval`, `voice` (dynamic widget) |
| **③ Tools** | `tools_cfg` (ToolPolicyEditor, default `deny`), `skills[]`, `fallback_models[]` (max 2) |
| **Advanced** | `model_params`, `sandbox_profile`, `shell_policy`, `rate_limits`, ~~`delegation_policy` (link to `/agents/trust`)~~ **[removed — ADR-037]**, `timeout_seconds` (default 300), `max_tool_iterations` (default 50), `steering_mode` (default `one-at-a-time`) |

### 5.4 Wizard — `Subagent` (3 steps + Advanced)

| Step | Visible fields |
|---|---|
| **① Identity** | `color`, `icon`, `name` *, **`description` * (required for routing)**, `model` * (picker) |
| **② Personality** | `soul` * ⭐ (label: *"Soul / task prompt"*), `instructions` (no Heartbeat, no Voice) |
| **③ Tools** | `tools_cfg` (default `deny`), `skills[]`, `fallback_models[]` (max 2) |
| **Advanced** | `model_params`, `sandbox_profile`, `shell_policy`, `rate_limits`, `timeout_seconds`, `max_tool_iterations` (no steering_mode, no delegation_policy [field removed entirely — ADR-037], no Heartbeat, no Voice) |

### 5.5 Wizard — `Subagent (External)` (3 steps + Advanced)

| Step | Visible fields |
|---|---|
| **① Identity** | `color`, `icon`, `name` *, `description` * (required for routing), `model` * (free-text slug input — NOT picker) |
| **② Personality** | `soul` * ⭐, `instructions` (no Heartbeat, no Voice) |
| **③ Tools** | `skills[]` (no tools_cfg — CLI doesn't see Omnipus tools, no fallback_models — CLI handles retries) |
| **Advanced** | `rate_limits`, `timeout_seconds`, `max_tool_iterations`, **`executor.cli_path` *, `executor.env_overrides`, `executor.cli_args`** (no `sandbox_profile` / `shell_policy` — CLI manages its own isolation, per matrix rows 17-18) |

The CLI choice is **fixed at the roster level** (not visible in the
wizard). The `executor.cli` field is shown in the Edit slide-over (in
the Runtime tab) but is **locked** after create.

---

## 6. Edit flow — wireframes

### 6.1 Footer (shared across all edit slide-overs)

```
├──────────────────────────────────────────────────────┤
│  Last saved 3s ago ●           [ 🗑 Delete agent ]  │
└──────────────────────────────────────────────────────┘
```

- **No Apply button** — every change auto-commits via the `useAutoSave`
  hook (500ms debounce).
- **Delete** is a destructive discrete action — opens a confirmation
  modal: *"Delete `<name>`? This cannot be undone."*
- The `Last saved Xs ago ●` indicator reflects the most recent successful
  autosave. Pulses on save, turns red on error.

### 6.1.1 Autosave semantics (F-12)

The `useAutoSave` hook flushes the field state to `PUT /api/v1/agents/{id}`
after **500 ms** of input inactivity. Behaviour by response class:

| Outcome | Indicator | Recovery |
|---|---|---|
| `200 OK` | Green ●, `Last saved Xs ago` | — |
| `4xx` (validation) | Red ●, error message inline on the offending field | Autosave paused for the offending field until the input is corrected (other fields continue autosaving). After correction, the next flush re-validates the full payload. |
| `5xx` or network error | Red ● | Autosave continues — the next keystroke triggers another flush; no manual retry. The indicator clears to green on the next successful PUT. |
| Tab close mid-debounce | (n/a) | The in-flight change is flushed via `navigator.sendBeacon()` (or `fetch(..., { keepalive: true })` fallback) on the `visibilitychange` event. The PUT is best-effort; no retries. |
| Two browser tabs edit the same agent | (n/a) | The second tab's PUT will be rejected with `409 Conflict` if the `updated_at` doesn't match; the indicator turns red and the tab must reload. |

**Rationale:** silent-drop is unacceptable for an autosave UX — every
failure must be visible. But manual-retry-on-error is also a foot-gun
(it makes the user the retry loop). The pattern above keeps the
indicator honest while letting the user keep typing.

### 6.2 Edit — `Main` (5 tabs: Basics, Personality, Tools, Advanced + Identity strip)

### 6.2 Edit — `Main` (5 tabs: Basics, Personality, Tools, Advanced + Identity strip)

```
┌─ Edit agent: Research Assistant ───────────[ × ]──┐
│                                                      │
│  Identity (above tabs)                               │
│   Name · Description · Color · Icon · Default toggle  │
│                                                      │
│  ┌────────┬──────────┬───────┬───────────┐           │
│  │Basics ●│Persona-  │Tools  │Advanced ▾ │           │
│  │        │lity     │       │           │           │
│  └────────┴──────────┴───────┴───────────┘           │
│                                                      │
│  ▼ Basics (visible)                                  │
│   Model · ▼ Sampling params · ▼ Voice               │
│  ▼ Personality (visible)                              │
│   Soul ⭐ · Instructions · ▼ Heartbeat               │
│  ▼ Tools (visible)                                   │
│   ToolPolicyEditor · Skills · Fallback models         │
│  ▼ Advanced                                          │
│   Sandbox · Shell · Rate limits · Delegation → /trust │
│   Timeout · Max iter · Steering                      │
│                                                      │
├──────────────────────────────────────────────────────┤
│  Last saved 3s ago ●      [ 🗑 Delete agent ]      │
└──────────────────────────────────────────────────────┘
```

### 6.3 Edit — `Subagent` (4 tabs: same shape, fewer fields)

Identical to Main, except:
- No `Default` toggle (workers never default)
- No `Voice` in Basics
- No `Heartbeat` in Personality
- No `Steering mode` in Advanced
- No `Delegation policy` link in Advanced (the link and the `delegation_policy` field were removed entirely — ADR-037; delegation is workspace-scoped)

### 6.4 Edit — `Subagent (External)` (5 tabs: Basics, Personality, Tools, Runtime, Advanced)

```
┌─ Edit agent: External Researcher ──────────[ × ]──┐
│  Identity (above tabs)                               │
│   Name · Description · Color · Icon                  │
│   (no Default toggle — workers never default)         │
│                                                      │
│  ┌────────┬──────────┬───────┬────────┬──────────┐   │
│  │Basics ●│Persona-  │Tools  │Runtime │Advanced ▾│   │
│  │        │lity     │       │  (CLI) │          │   │
│  └────────┴──────────┴───────┴────────┴──────────┘   │
│                                                      │
│  ▼ Basics                                            │
│   Model *  (free-text slug)                          │
│   (no model_params — CLI may not support)             │
│                                                      │
│  ▼ Personality                                       │
│   Soul ⭐ · Instructions                             │
│  ▼ Tools                                             │
│   Skills                                             │
│   (no tools_cfg, no fallback_models)                  │
│                                                      │
│  ▼ Runtime (CLI-specific, NEW)                       │
│   CLI             claude-code (locked)               │
│   ↑ CLI is locked after create — to switch CLIs,      │
│     create a new agent. The CLI choice is part of     │
│     the agent's identity.                             │
│   CLI path *      /usr/local/bin/claude              │
│   Env overrides   [+ Add var]                        │
│   CLI args        --max-turns 5                      │
│                                                      │
│  ▼ Advanced                                          │
│   Rate limits · Timeout · Max iter                  │
│   (no Sandbox, no Shell, no Steering, no Delegation, │
│    no Heartbeat, no Voice)                           │
│                                                      │
├──────────────────────────────────────────────────────┤
│  Last saved 3s ago ●      [ 🗑 Delete agent ]      │
└──────────────────────────────────────────────────────┘
```

### 6.5 Edit — `built-in Main` (Mia, Jim, Ava, Ray)

Same shape as Main, but every field that would be R/O is rendered `ro`
with a tooltip: *"Locked agent — seeded at gateway boot."*

```
┌─ Edit agent: Mia (locked) ──────────────────[ × ]──┐
│  ⚠ This is a built-in core agent. Most fields are    │
│  read-only. To create your own chat colleague, use   │
│  the + Add Main button.                             │
│                                                      │
│  Identity (above tabs, all read-only)                │
│   Name [Mia] · Description [General-purpose ...]     │
│   Color [Verdant] · Icon [⚙]                        │
│   ⭐ Default agent ●━━○  (ro — Mia IS default)       │
│                                                      │
│  [Basics | Personality | Tools | Advanced ▾]         │
│                                                      │
│  ▼ Basics                                           │
│   Model [claude-sonnet-4-6]   (editable — operator)  │
│   Sampling [T=1.0 · top_p=1.0 · max_tokens=4096]     │
│      (ro — seeded)                                  │
│   Voice [alloy ▾]            (editable — operator)   │
│  ▼ Personality (ro)                                  │
│   Soul [sealed markdown]                             │
│   Instructions · Heartbeat, etc.                    │
│                                                      │
│  ▼ Tools (ro)                                       │
│   (locked tool policy — system.*=deny baked in)      │
│                                                      │
│  ▼ Advanced (operator-tunable subset)                │
│   Sandbox · Shell policy · Rate limits ·             │
│   Steering_mode ·                                    │
│   Timeout_seconds · Max_tool_iterations ·            │
│   Heartbeat_enabled · Heartbeat_interval             │
│                                                      │
├──────────────────────────────────────────────────────┤
│  Last saved 3s ago ●      [ 🗑 Delete agent ]      │
└──────────────────────────────────────────────────────┘
```

Per `AgentUpdateRequest.yaml`, the **operator-tunable subset** for
locked (core) agents is exactly:
- `model`
- `voice` (F-14 resolution: voice is operator-tunable on built-ins because it's a runtime TTS config, not identity; the operator can switch Mia to a different voice without touching her persona prompt)
- `timeout_seconds`
- `max_tool_iterations`
- `steering_mode`
- `heartbeat_enabled`
- `heartbeat_interval`

Everything else is `ro` for built-ins. The footer is **autosave only** —
no `[ Apply ]` button (matches the design language of §6.1; applies
uniformly to all edit slide-overs). The Voice field follows the dynamic
widget from §4.10 / §4.10.1 and **is editable on built-ins** (F-14
resolution). For non-Main types (Subagent / Subagent (External)) the
field is hidden, not disabled — workers never have voice.

---

## 7. Wire schema changes

The wire schema diffs are documented in detail in
[`agent-config-matrix-spec.md §5`](./agent-config-matrix-spec.md#5-the-wire-schema-diff-to-ship-in-wave-6-follow-up).
The summary:

```diff
# contracts/components/schemas/AgentCreateRequest.yaml

 required:
   - name
+  - soul
 properties:
   type:
     enum:
-      - custom
-      - worker
+      - Main
+      - Subagent
+      - subagent_3p
     default: Main
   fallback_models.maxItems: 10 → 2
+  soul: { type: string, minLength: 1 }  # mandatory

# AgentUpdateRequest.yaml
- REMOVE tool_feedback  # now per-channel runtime behavior

# Agent.yaml type enum
   enum: [core, system, Main, Subagent, subagent_3p]

# ExecutorConfig.yaml
 properties:
   kind: { enum: [native, external-cli, remote-a2a] }   # derived, no UI
+  cli:     { enum: [claude-code, codex, opencode] }   # R for subagent_3p
+  cli_path: { type: string }                          # R for subagent_3p
+  env_overrides: { type: object, additionalProperties: { type: string } }  # O
+  cli_args: { type: string }                          # O
```

---

## 8. Files this spec changes

### 8.1 Wire contracts
- `contracts/components/schemas/AgentCreateRequest.yaml`
- `contracts/components/schemas/AgentUpdateRequest.yaml`
- `contracts/components/schemas/Agent.yaml`
- `contracts/components/schemas/ExecutorConfig.yaml`
- `contracts/openapi.yaml` — `Agent` schema + the `/agents/{id}` PUT description text referencing `tool_feedback`

**`tool_feedback` removal — completeness checklist** (the field moves to
per-channel runtime behaviour; this is a wire + generated-types change):

1. Remove from `contracts/components/schemas/AgentUpdateRequest.yaml` (the property definition and the description-text reference).
2. Remove from `contracts/components/schemas/Agent.yaml` (lines 19 and 131 — the `required:` entry and the property definition).
3. Remove the reference from `contracts/openapi.yaml` line 1444 (description text of the PUT endpoint).
4. The **global** `ToolFeedbackConfig` on `pkg/config/config.go:1251-1287` (`AgentDefaults.ToolFeedback`) **stays** — it is the per-channel runtime replacement and is unrelated to the per-agent field being removed.
5. Regenerate `pkg/api/generated/openapi_types.gen.go` (`make gen-contracts`) and commit the diff atomically with the contract change (CLAUDE.md Constraint #8, 5-step process).

### 8.2 Backend
- `pkg/gateway/rest.go` — handle renamed `type` enum, validate `soul` is required on create, validate `cli` + `cli_path` required when `type: subagent_3p`, override derived fields (`executor.kind` from `type`, `steering_mode: one-at-a-time` for workers)
- `pkg/agent/loop.go` — replace `tool_feedback` checks with per-channel routing (`if msg.Channel == "webchat" { skip }`)

### 8.3 Frontend

**Existing files modified** (resolved per F-11):

- `src/routes/_app/agents.tsx` — add the 3 distinct "+ Add" buttons + Subagent (External) CLI sub-picker
- `src/components/agents/AgentProfile.tsx` — tab contents conditional on `type` per the matrix; add `Runtime` tab for Subagent (External); update voice field to dynamic widget behavior per §4.10.1
- `src/components/shared/IconRenderer.tsx` — already case-insensitive (Wave B fix); no change

**Create wizard file structure** (proposed; implementer may adjust):

The current `CreateAgentModal.tsx` (573 LOC) handles a 2-tab wizard and
imports `getCreateAgentFormCopy` from `AgentFormFields.tsx`. The new
spec calls for a 3-step + Advanced wizard that branches by type — a
near-rewrite. Recommended file split (per F-11):

- `src/components/agents/CreateAgentWizard.tsx` — top-level container, the `Dialog`/`Sheet` wrapper, the `type` + `cli` chip, the numbered stepper
- `src/components/agents/CreateAgentStep1Identity.tsx` — step ① (color, icon, name, description, model)
- `src/components/agents/CreateAgentStep2Personality.tsx` — step ② (soul ⭐, instructions, heartbeat, voice — fields conditional on `type`)
- `src/components/agents/CreateAgentStep3Tools.tsx` — step ③ (tools_cfg, skills, fallback_models — fields conditional on `type`)
- `src/components/agents/CreateAgentAdvanced.tsx` — Advanced disclosure (sandbox, shell, rate_limits, executor CLI fields for External, etc.)
- `src/components/agents/AgentFormFields.tsx` — keep for shared input primitives (AvatarColorPicker, IconPicker, etc.)

The implementer may keep the wizard as a single file at their
discretion — the split is a recommendation for readability, not a hard
requirement.

**New files:**

- `src/lib/agents/voice-provider-detect.ts` — NEW (per F-07; see §4.10.1 for the contract)
- `src/components/agents/voice-provider-sub.ts` — NEW: small event bus the Settings → Voice section publishes to when the operator changes the provider; the agent-edit slide-over subscribes via `detectVoiceProvider()` (see §4.10.1)

### 8.4 Generated artifacts

- `make gen-contracts` — regenerates `pkg/api/generated/openapi_types.gen.go` and `src/lib/api/generated/openapi-types.ts`
- `make verify-contracts` — must pass after the change
- **Atomic commit rule** (F-08): the PR MUST commit the regenerated
  `pkg/api/generated/` and `src/lib/api/generated/` artifacts in the
  same commit as the schema change (CLAUDE.md Constraint #8, 5-step
  process). The acceptance criteria in §9.1 include grep checks against
  the generated artifacts to prove the regeneration took effect.

---

## 9. Acceptance criteria

### 9.1 Wire contract

| Check | Expected outcome |
|---|---|
| `make verify-contracts` exits 0 | pass |
| `lint-wire-types` reports 0 hand-written wire-format structs | pass |
| The `type` enum in `AgentCreateRequest.yaml` has exactly 3 values: `Main`, `Subagent`, `subagent_3p` | pass |
| `soul` is in the `required` list of `AgentCreateRequest.yaml` | pass |
| `description` has `minLength: 1` in `AgentCreateRequest.yaml` | pass |
| `fallback_models` has `maxItems: 2` everywhere | pass |
| `tool_feedback` is removed from `AgentUpdateRequest.yaml` AND `Agent.yaml` AND `openapi.yaml` (per §8.1 checklist) | pass |
| `ExecutorConfig` has the 4 fields: `cli`, `cli_path`, `env_overrides`, `cli_args` | pass |
| `grep -r 'subagent_3p' pkg/api/generated/` returns ≥1 hit (proves the new enum value is in the generated Go types) | pass |
| `grep -rn 'tool_feedback' pkg/api/generated/` returns 0 hits (proves the field is removed from the generated Go types) | pass |
| `grep -rn 'tool_feedback' src/lib/api/generated/` returns 0 hits (proves the field is removed from the generated TS types) | pass |
| The PR commits `pkg/api/generated/` and `src/lib/api/generated/` artifacts atomically with the schema changes (CLAUDE.md Constraint #8, 5-step process) | pass |

### 9.2 Backend behavior

| Test | Expected outcome |
|---|---|
| `POST /api/v1/agents` with `type: Main`, no `soul` | **400** — `soul` is required |
| `POST /api/v1/agents` with `type: Main`, `soul: "..."`, `name: "..."` | **201** — agent created |
| `POST /api/v1/agents` with `type: Subagent`, no `description` | **400** — description is required for workers (F-02) |
| `POST /api/v1/agents` with `type: Subagent`, `description: "   "` (whitespace only) | **400** — backend trims and rejects as `minLength: 1` violation (F-02) |
| `POST /api/v1/agents` with `type: Subagent`, `description: "..."`, no `cli` | **201** — `executor.kind: native` inferred |
| `POST /api/v1/agents` with `type: subagent_3p`, no `cli` | **400** — `cli` is required for External |
| `POST /api/v1/agents` with `type: subagent_3p`, `cli: claude-code`, no `cli_path` | **400** — `cli_path` is required |
| `POST /api/v1/agents` with `type: Main`, `executor.kind: external-cli` (in body) | **201** but server overrides `executor.kind: native` |
| `POST /api/v1/agents` with `type: Main`, `fallback_models` containing 3 entries | **400** — `fallback_models exceeds maxItems: 2` (F-13) |
| `POST /api/v1/agents` with `type: Main`, `color: "not-a-hex"` | **400** — invalid `color` regex |
| `POST /api/v1/agents` with `type: Main`, `icon: "<string-of-1000-chars>"` | **400** — `icon` exceeds `maxLength: 50` |
| `PUT /api/v1/agents/{id}` on `Subagent`, `steering_mode: queue-and-process` | **200** but server overrides to `one-at-a-time` |
| `PUT /api/v1/agents/{id}` on `Subagent`, adding a 3rd `fallback_models` entry | **400** — exceeds `maxItems: 2` (F-13) |
| `PUT /api/v1/agents/{id}` on `Subagent (External)`, `tools_cfg: {...}` | **400** — `field_not_applicable_to_type` (was: silent-drop; see §4.19.1) |
| `PUT /api/v1/agents/{id}` on `Subagent (External)`, `skills: [...]` | **400** — same code |
| `PUT /api/v1/agents/{id}` on `Subagent (External)`, `fallback_models: [...]` | **400** — same code |
| `PUT /api/v1/agents/{id}` on `Subagent (External)`, `model_params: {...}` | **400** — same code |
| `PUT /api/v1/agents/{id}` on `Subagent (External)`, `sandbox_profile: "workspace"` | **400** — same code |
| `PUT /api/v1/agents/{id}` on `Subagent (External)`, `shell_policy: {...}` | **400** — same code |
| `PUT /api/v1/agents/{id}` on `Subagent (External)`, `delegation_policy: {...}` | **400** — post-ADR-037: `delegation_policy` is a retired field for ALL agent types; the handler raw-body-sniffs it and 400s (mirrors the ADR-035 `sandbox_profile` precedent), not the former subagent_3p-specific rejection |
| `PUT /api/v1/agents/{id}` on `Subagent (External)` created with `cli: claude-code`, body `{executor: {cli: codex}}` | **400** — `executor.cli is immutable after create; create a new agent to use a different CLI` (F-10) |
| `PUT /api/v1/agents/{id}` on `Subagent (External)`, `executor.cli_path: "/new/path"` | **200** — `cli_path` IS mutable (unlike `cli`); allows binary upgrades without re-creating the agent |
| `PUT /api/v1/agents/{id}` on `Main`, `description: ""` | **200** — empty string is valid for Main |
| `PUT /api/v1/agents/{id}` on `Subagent`, `description: ""` | **400** — description required for workers |
| `GET /api/v1/agents/{id}` on a built-in Main | returns `type: core, locked: true` (built-ins keep `core`; the `Main` enum value is only for user-created agents, per §2 and F-04) |
| `GET /api/v1/agents/{id}` on a user-created Main | returns `type: Main, locked: false` |

### 9.3 Frontend behavior (BDD — Given / When / Then)

```gherkin
Scenario: Agents roster shows three distinct "+ Add" buttons
  Given I am signed in as a user on /agents
  When the page renders
  Then I see [data-testid="add-main-button"], [data-testid="add-subagent-button"], and [data-testid="add-subagent-external-button"]
  And the "Subagent (External)" button expands to show 3 CLI options on hover/click
  And the CLI options are claude-code, codex, opencode (data-testid="add-external-{cli}" each)

Scenario: Tapping "+ Add (External) claude-code" opens a wizard with both type and CLI locked
  Given I am on /agents
  When I click [data-testid="add-external-claude-code"]
  Then the create wizard modal opens
  And the locked chip shows "Type: Subagent (External)" AND "CLI: claude-code"
  And the wizard opens at step ① (Identity)

Scenario: Wizard step ① shows a Type locked chip on every type
  Given a wizard of any type is open
  When step ① renders
  Then [data-testid="type-chip"] is visible and reads "Type: <Main|Subagent|Subagent (External)> (locked)"
  And the [×] on the chip behaves as Cancel (closes wizard, no confirmation even if dirty — by design; see §11 #3)

Scenario: Step ① Subagent wizard shows Description with a required asterisk
  Given a Subagent wizard is open at step ①
  When the description input renders
  Then [data-testid="wizard-description"] is visible
  And the field label includes "*" (required indicator)
  And the field is non-empty after the wizard's "Next" button is enabled

Scenario: Step ① Subagent (External) wizard shows Model as free-text input (not picker)
  Given a Subagent (External) wizard is open at step ①
  When the model input renders
  Then [data-testid="wizard-model"] is an <input type="text"> (NOT a <select> or picker)
  And no model-picker dropdown is rendered

Scenario: Step ② Subagent wizard has no Heartbeat field
  Given a Subagent wizard is open at step ②
  When the form renders
  Then [data-testid="wizard-heartbeat"] is NOT in the DOM
  And the Personalities section contains only `soul` and `instructions`

Scenario: Step ② wizard of any worker type has no Voice field
  Given a Subagent or Subagent (External) wizard is open at step ②
  When the form renders
  Then [data-testid="wizard-voice"] is NOT in the DOM

Scenario: Step ② wizard of any type shows Soul with a required asterisk
  Given a wizard of any type is open at step ②
  When the soul textarea renders
  Then [data-testid="wizard-soul"] is visible
  And the field label includes "*"
  And the textarea is non-empty before "Next" is enabled (per the §9.2 missing-soul 400 contract)

Scenario: Step ③ Subagent (External) wizard has no tools_cfg editor
  Given a Subagent (External) wizard is open at step ③
  When the Tools step renders
  Then [data-testid="wizard-tools-cfg"] is NOT in the DOM
  And the only field visible is `skills` (multi-select chips)

Scenario: Step ③ wizard of any type caps Fallback models at 2
  Given a wizard of any type is open at step ③
  When I have already added 2 fallback models
  Then the "+ Add fallback" button is disabled
  And the tooltip reads "Maximum 2 fallback models"

Scenario: Edit slide-over (Main) shows 4 tabs
  Given I click on a Main agent in the roster
  When the edit slide-over opens
  Then the tab bar contains [data-testid="tab-basics"], [data-testid="tab-personality"], [data-testid="tab-tools"], [data-testid="tab-advanced"]
  And no Runtime tab is present

Scenario: Edit slide-over (Subagent External) shows 5 tabs including Runtime
  Given I click on a Subagent (External) agent
  When the edit slide-over opens
  Then the tab bar contains Basics, Personality, Tools, Runtime, Advanced
  And the Runtime tab shows CLI (locked), CLI path, Env overrides, CLI args

Scenario: Edit slide-over footer shows Last saved indicator and Delete (no Apply)
  Given an edit slide-over is open for any type
  When the footer renders
  Then [data-testid="last-saved-indicator"] is visible with the format "Last saved Xs ago ●"
  And [data-testid="delete-agent-button"] is visible
  And there is NO Apply / Save button (autosave-only per §6.1)

Scenario: Tapping Delete opens a confirmation modal
  Given an edit slide-over is open
  When I click [data-testid="delete-agent-button"]
  Then an AlertDialog renders with text "Delete <name>? This cannot be undone."
  And the dialog has a Cancel button and a destructive Delete button

Scenario: Built-in agents show a ⚠ Locked banner at the top of edit
  Given I click on Mia (or Jim / Ava / Ray)
  When the edit slide-over opens
  Then [data-testid="locked-banner"] is visible at the top of the body
  And the banner reads "This is a built-in core agent. Most fields are read-only..."

Scenario: Built-in agent Voice field is shown and editable (operator-tunable per F-14)
  Given I am editing Mia with a voice provider configured globally
  When the Voice widget renders under Basics
  Then [data-testid="voice-field"] is visible with Mia's seeded value
  And the field is NOT disabled — the operator can change Mia's voice
  And selecting a different voice autosaves within 500 ms

Scenario: Voice field renders a dropdown when the provider exposes a voices enum
  Given a voice provider is configured globally with a voices enum (e.g. OpenAI TTS)
  When the Voice widget renders on a user-created Main agent
  Then [data-testid="voice-field"] is a <select> populated from the provider's voices API
  And selecting a voice autosaves within 500 ms

Scenario: Voice field renders a text input when the provider has no enum
  Given a voice provider is configured but does not expose a voices enum
  When the Voice widget renders on a user-created Main agent
  Then [data-testid="voice-field"] is an <input type="text">
  And the placeholder reads "e.g. alloy"

Scenario: Voice field is disabled when no voice provider is configured
  Given no voice provider is configured globally
  When the Voice widget renders on any agent (including built-ins)
  Then [data-testid="voice-field"] is disabled
  And the tooltip reads "Configure a voice provider in Settings → Voice to enable per-agent voice"

Scenario: Autosave indicator turns red on PUT error and clears on next success (F-12)
  Given an edit slide-over is open and I have just typed in a field
  When the PUT returns 5xx or the network fails
  Then [data-testid="last-saved-indicator"] turns red
  And [data-testid="last-saved-indicator"] clears to green on the next successful PUT (no manual retry)
  And the field that errored is highlighted with the error message inline

Scenario: Tapping the [×] on the type chip cancels the wizard (no confirm dialog by design)
  Given a wizard of any type is open with dirty input
  When I click the [×] on [data-testid="type-chip"]
  Then the wizard closes immediately
  And the roster is shown
  (By design: dirty input is discarded; see §11 #3.)

Scenario: The 3 "+ Add (External)" sub-pickers are disabled when their CLI is not installed
  Given the host system has only claude-code installed (codex and opencode missing)
  When I expand the Subagent (External) button
  Then [data-testid="add-external-codex"] and [data-testid="add-external-opencode"] are disabled
  And each disabled button's tooltip reads "This CLI is not installed on the host"
```

### 9.4 Visual regression (BDD — Given / When / Then + Playwright snapshot)

```gherkin
Scenario: /agents page renders the 3 "+ Add" buttons + roster + collapsible built-ins
  Given I am on /agents with at least one custom agent and 4 built-ins
  When the page first paints
  Then the top of the page shows the 3 add-buttons (Main, Subagent, Subagent (External))
  And below that is the "Base agents" section with the user's custom agents
  And below that is the "Built-in roster (locked)" section, expanded by default on desktop / collapsed on phone (per F-18)
  And the visual regression snapshot matches the §5.1 wireframe

Scenario: Wizard stepper renders correctly with step content matching the matrix
  Given any wizard is open at step ②
  When the stepper renders
  Then it shows ●○○ (filled for current step, hollow for upcoming)
  And the current step name "Personality" is shown next to the dots on desktop; just dots + current name on phone (per §13.3)
  And the form fields visible match the matrix row for this type (e.g. no Heartbeat for Subagent)

Scenario: Edit slide-over tabs render with autosave + Delete in the footer
  Given any edit slide-over is open
  When the slide-over renders
  Then the tab bar matches §6.2 / §6.3 / §6.4 per the type
  And the footer shows [data-testid="last-saved-indicator"] (left) and [data-testid="delete-agent-button"] (right)
  And there is no Apply / Save button anywhere

Scenario: Built-in edit shows ⚠ Locked banner with appropriate fields ro
  Given I edit a built-in (Mia)
  When the slide-over renders
  Then [data-testid="locked-banner"] is pinned at the top with the warning text
  And the operator-tunable subset (model, voice, timeout, max_iter, steering, heartbeat_enabled, heartbeat_interval — per F-14) is editable
  And all other fields are `disabled`

Scenario: Subagent (External) edit shows the Runtime tab and hides Tools / Sandbox / Voice
  Given I edit a Subagent (External)
  When the slide-over renders
  Then the Runtime tab is visible with CLI (locked), CLI path, Env overrides, CLI args
  And the Tools tab has only the Skills field (no tools_cfg, no fallback_models)
  And the Advanced tab has no Sandbox, no Shell policy, no Steering, no Delegation link
  And no Voice field is rendered anywhere (workers never have voice)
```

---

## 10. Migration from current state (the 2-type system)

The current code uses:
- `type: "custom" | "worker"` (2 values) on `AgentCreateRequest`
- `type: "core" | "custom" | "system" | "worker"` (4 values) on `Agent.yaml`
- 12-accordion Edit slide-over (one Accordion per section)
- 2-tab Create modal (General / Tools)
- `tool_feedback` per-agent bool

### 10.1 Migration plan (single PR, semver-major on the gateway)

1. **Wire contracts** — apply the diff in §7, including the **`tool_feedback` completeness checklist in §8.1** (Agent.yaml removal, openapi.yaml description-text removal, generated-type regeneration). Bump gateway version.
2. **Backend** — handle the renamed `type` enum. Migration rule:
   - Old `type: "custom"` → new `type: "Main"`
   - Old `type: "worker"` (executor.kind=native) → new `type: "Subagent"`
   - Old `type: "worker"` (executor.kind=external-cli) → new `type: "subagent_3p"`
   - The `core` / `system` enum values stay for the locked roster
3. **Database migration** — if the DB stores `type` as a string, run a one-time
   `UPDATE agents SET type = 'Main' WHERE type = 'custom'` and
   `UPDATE agents SET type = 'Subagent' WHERE type = 'worker' AND executor_kind = 'native'`,
   etc.
4. **Frontend** — replace the 12-accordion Edit with the 4 (or 5) tab layout.
   Replace the 2-tab Create modal with the type-aware wizard. The 3 "+ Add"
   buttons replace the existing single "+ New agent" button.
5. **`tool_feedback`** — remove from the wire. The runtime per-channel
   routing (webchat never, messaging always) takes its place.

### 10.2 Rollback plan
The semver-major bump + DB migration is one-way. If the rollout fails:
- Revert the gateway binary; the DB migration is non-destructive (only
  rewrites `type` strings, no schema change).
- Re-deploy the previous binary; the new `type` strings (`Main` / `Subagent`
  / `subagent_3p`) are unknown to the old binary and would surface as
  `type: "Unknown"` in the UI. Fix: also restore the DB rows from a backup,
  or write a one-shot downgrade migration `Main → custom`, etc.

---

## 11. Open questions for follow-up

### Resolved during this design pass
1. **CLI choice position** — 3 separate buttons on the roster (`+ Add (External) claude-code / codex / opencode`) for discoverability. The wizard opens with `Type: Subagent (External)` AND `CLI: <choice>` both locked in the chip.
2. **Built-in Main Voice field** — **Shown and editable** for built-ins (F-14 resolution: voice is a runtime TTS config, not identity, so it joins the operator-tunable subset alongside `model`). The widget follows the global voice-provider detection from §4.10.1: dropdown if the provider exposes an enum, free-text if not. Disabled only when no voice provider is configured globally — that case applies to user-created Main agents too. Workers (Subagent / Subagent (External)) never show the field (hidden, not disabled).
3. **Wizard step ① "Type" chip dismissibility** — The `[×]` on the chip = same as Cancel (closes wizard, returns to roster). Single behaviour across all three types.

### Open for follow-up
4. **"Defaults" tab** — For built-in agents, the operator-tunable subset (Sandbox, Shell, Rate limits, Timeouts, Heartbeat) takes up most of the Advanced tab. Recommendation: keep it in Advanced for now; split into its own tab if usage data shows operators revisit these often.
5. ✅ **`built-in Subagent` column rename** — Confirmed by the user: `Worker` → `Subagent` everywhere, including the built-in matrix column. No revert needed.
6. **`soul` + `instructions` delivery to External CLIs** — Currently passed as prompt content (`<cli_path> --prompt <prompt> --model <model>`). Confirm the long-string-prompt path works against all three CLIs (claude-code, codex, opencode) — particularly Codex's input handling. If a CLI rejects long prompts, fall back to writing a tmp file and pointing at it (which would re-introduce `executor.instructions_file` — currently dropped).
7. **Voice widget edge case** — What if the user installs a voice provider that *partially* exposes a voices enum (returns some voices but errors on others)? Decision tree currently picks "free-text" — confirm with frontend-lead whether a partial-enum provider should render as dropdown (filtering known voices) or free-text.

---

## 12. References

- [`agent-config-matrix-spec.md`](./agent-config-matrix-spec.md) — the canonical property matrix
- `docs/internal/specs/chat-served-iframe-preview-spec.md` — related: iframe preview
- `docs/internal/architecture/ADR-009-per-agent-sandbox-as-security-boundary.md` — why sandbox is per-agent
- `docs/internal/specs/phase-1-chat-model-and-errors.md` — Q1/Q7 reconciliation (object-form `[{model, provider}]`)

---

## 13. Mobile layout (responsive)

The wireframes in §5–§6 are the **desktop** layouts. This section covers
how the same flows adapt to phone and tablet breakpoints. **Nothing
about the data model changes on mobile** — the 3-type taxonomy, the
property matrix, and the autosave semantics are identical; only the
visual treatment changes.

This section is grounded in the **actual app conventions** verified in
the SPA code:

- **Breakpoint convention**: mobile-first Tailwind. Default styles apply
  on phone; `sm:` (≥640 px) and up are desktop. Inverted from a typical
  "desktop-first" mental model.
- **Slide-over widths** (from `src/components/ui/sheet.tsx`):
  - Right (edit slide-over) default: `w-[90vw] sm:max-w-2xl` — i.e.
    **90 vw on phone, 672 px on desktop**.
  - CreateAgentModal overrides with `w-full sm:max-w-3xl` — **100 vw
    on phone, 768 px on desktop**.
  - Left slide-over default: `w-[90vw] sm:max-w-md` (448 px on desktop).
- **SheetFooter button stack**: `flex-col-reverse sm:flex-row
  sm:justify-end sm:space-x-2` (from `sheet.tsx:101`, `dialog.tsx:68`,
  `alert-dialog.tsx:81`). **Buttons stack vertically (primary on top)
  on phone; side-by-side on `sm+`.**
- **Existing roster grid** (from `AgentListScreen.tsx:61,133,182`):
  `grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4` — 1 column on
  phone, 2 on tablet, 3 on desktop.
- **No bottom-sheet component exists** — pickers/modals use the existing
  `Sheet` (slide-over) or `Dialog` (centered modal). No `Drawer` /
  `BottomSheet` in the codebase.
- **No mobile-detection hook** — the app uses pure Tailwind responsive
  classes (`sm:`, `md:`, `lg:`); no `useIsMobile` or `useMediaQuery`.
- **Section header pattern** (`AgentListScreen.tsx:96-114`): a `flex
  items-start justify-between` row with the section heading on the left
  and a `ghost` variant `New agent` button on the right. No FAB.
- **Design tokens** referenced throughout: `--color-surface-1`
  (sheet body), `--color-primary` at 80% + `backdrop-blur-sm` (modal
  backdrop), `--color-border`, `--color-secondary` (primary text —
  Liquid Silver), `--color-muted` (secondary text), `--color-accent`
  (Forge Gold — CTAs and accents), `--color-error` (Ruby). Type:
  `font-headline` (Outfit — section titles), `font-mono` (JetBrains
  Mono — IDs, code).
- **Tap target**: close `[×]` button is `h-11 w-11` (44 × 44 px), matching
  Apple HIG. Interactive rows/buttons should match this minimum.
- **Sheet header padding**: `px-8 pt-7 pb-5` (32 / 28 / 20 px) —
  applied identically on phone and desktop unless overridden.

### 13.1 What stays IDENTICAL across breakpoints

- The **3-type taxonomy** (Main / Subagent / Subagent External)
- The **property matrix** (§3 — which fields apply to which type)
- The **autosave semantics** (500 ms debounce, no Apply button)
- The **wire schema** (the contract is breakpoint-agnostic)
- The **server-side validation** (400 on missing `soul`, derived
  `executor.kind`, dropped `tools_cfg` on External, etc.)
- The **footer copy** (`Last saved Xs ago ●` + Delete)
- The **delete confirmation copy**

Mobile is purely a **layout concern**; the data model and validation
rules are fixed.

### 13.2 Roster (`/agents`) on phone (`< sm`)

**Roster grid**: keeps the existing `grid grid-cols-1 sm:grid-cols-2
lg:grid-cols-3 gap-4` from `AgentListScreen.tsx`. Phone = 1 column,
tablet portrait = 2, desktop = 3. **No new grid class needed**; the
existing responsive grid already collapses correctly.

**Section headers** ("Base agents", "Sub-agents", "Built-in roster"):
unchanged. The `New agent` button stays in the section header
(`ghost` variant, right-aligned). **There is no FAB pattern in the
app** — do not invent one. The button hides only if its label doesn't
fit, per the `hidden sm:inline` convention (label on desktop, icon-only
on phone).

**Search input**: full-width, pinned to the top, same as desktop.

**Default agent (Mia)**: `⭐` glyph next to name — unchanged.

**Built-in roster section**: collapsed by default on phone (default
disclosure); expanded on desktop. The ⚠ banner is always rendered when
the section is expanded.

### 13.3 Create wizard on phone (`< sm`)

The Create modal **already opens full-width on phone** via the existing
`widthClass="w-full sm:max-w-3xl"` on `CreateAgentModal.tsx:247`. **No
new pattern required** — the wizard IS a full-screen modal on phone
because of that widthClass.

**Layout inside the wizard on phone:**

- **Type / CLI chip**: stays pinned at the top of the modal body
  (above the stepper). Same as desktop.
- **Stepper**: numbered (●○○). Step names ("Identity / Personality /
  Tools") collapse on phone to **dots + current step name only**, per
  the `hidden sm:inline` convention (e.g. `Identity` becomes icon + dot
  on phone, full label on desktop).
- **Form fields**: stack vertically, full-width. The Identity strip
  (color swatch + icon picker + name + description + model) is already
  a vertical stack — keep it.
- **Advanced disclosure**: expands inline as a full-width accordion
  (matches the existing `▼ Advanced settings [tap ▾]` pattern in §5.2).
- **Footer buttons** (`SheetFooter`): follow the existing
  `flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2` — on phone
  they STACK VERTICALLY (primary `[Next →]` on TOP, secondary `[← Back]`
  below it); on `sm+` they sit side-by-side (Back on left, Next on
  right).

```
┌─ + New agent ──────────────────────────────[ × ]──┐
│                                                  │
│  Type: Subagent (External)         (locked)  [×] │
│  CLI:  claude-code                 (locked)      │
│                                                  │
│   ●─────○─────○                                  │
│   Identity                                       │
│                                                  │
│   [ color swatch ]                               │
│   [ icon picker ]                                │
│   Name * [_____________________________]        │
│   Description * [_______________________]        │
│   Model * [_____________________________]        │
│                                                  │
│   ▼ Advanced settings                  [tap ▾]   │
│                                                  │
├──────────────────────────────────────────────────┤
│  [ Next → ]                          (full-     │
│  [ ← Back ]                          width on   │
│                                       phone)     │
└──────────────────────────────────────────────────┘
```

### 13.4 Edit slide-over on phone (`< sm`)

The Edit slide-over **already opens at 90 vw on phone** via the
default `w-[90vw] sm:max-w-2xl` on `SheetContent` (`sheet.tsx:59`).
**No new pattern required** — it's effectively full-width on phone.

**Layout inside the edit on phone:**

- **Header**: `SheetHeader` with `px-8 pt-7 pb-5 border-b
  border-[var(--color-border)]`. Same padding as desktop (the app does
  NOT currently use `px-4 sm:px-8`); if a smaller padding is preferred,
  add it as a one-line Tailwind override on the specific components.
- **Identity strip**: stacks vertically — Name, Description, Color +
  Icon (side-by-side via existing `flex`), Default toggle (if Main).
  This is already the current desktop layout.
- **Tabs** (`Basics | Personality | Tools | Advanced ▾`):
  - 4-tab Main / Subagent edit: tabs render as a horizontal row; on
    `sm` and below, the tab bar **wraps or scrolls** per the existing
    `<Tabs>` component behaviour. Tab labels stay visible (no overflow
    menu in the app).
  - 5-tab Subagent (External) edit: same — horizontal row that may
    scroll on phone. The 5th tab (`Runtime`) is reachable by horizontal
    scroll on phone.
  - **Single-open accordion on `sm`**: each tab's content sections
    (`▼ Basics`, `▼ Personality`, etc.) collapse to a single-open
    accordion on phone (one section visible at a time) to avoid a
    wall-of-text. On `sm+`, all sections can be expanded
    independently, matching the desktop wireframes.
- **Footer** (sticky at the bottom): `Last saved Xs ago ●` on the
  left, `[ 🗑 Delete agent ]` on the right — same as desktop. **No
  Apply button** (autosave-only design holds).
- **Delete confirmation**: opens the same `AlertDialog` as desktop
  (`alert-dialog.tsx`) — centered modal with the
  `bg-[var(--color-primary)]/80 backdrop-blur-sm` overlay. Buttons
  follow the same `flex-col-reverse gap-2 sm:flex-row sm:justify-end
  sm:space-x-2 sm:gap-0` stack convention.

### 13.5 Pickers and editors on phone (`< sm`)

There is **no bottom-sheet pattern in the app** — do not invent one.
All pickers and editors reuse the existing primitives:

| Component | Mobile behaviour | Implementation hint |
|---|---|---|
| Color picker | Opens in the existing `Sheet` (right slide-over at 90 vw on phone) — same as desktop | Render the swatch grid inside a Sheet; pass `widthClass="w-full sm:max-w-md"` |
| Icon picker | Same — Sheet at full-width on phone | `widthClass="w-full sm:max-w-md"` |
| Model picker | Same — Sheet (full-width on phone); the existing `<SmartSelect>` / model-selector handles narrow viewports | No change |
| Voice picker | Same — Sheet or inline text input on a single-line form if the provider has no enum | No change |
| ToolPolicyEditor | Vertical list with a per-tool 3-way toggle (allow/ask/deny); the existing component already wraps on narrow viewports | No change |
| ~~Delegation policy link~~ **[removed — ADR-037]** | ~~Tap pushes within the SPA router to `/agents/trust`~~ — the `/agents/trust` screen and the link are deleted; delegation is workspace-scoped (workspace Team tab) | Removed |
| Skill picker | Multi-select chips, wrapping naturally on narrow screens via flex-wrap | No change |
| Fallback models | Drag handles (`⋮⋮`) hidden on phone via `hidden sm:inline`; "+ Add fallback" expands a full-width row | One-line class addition |

**Pattern rule**: any new picker MUST live inside a `Sheet` (right
slide-over). Do not introduce a `Drawer` / `BottomSheet` component.

### 13.6 Built-in edit on phone (`< sm`)

Same as the Main edit (§13.4), plus:

- The ⚠ **Locked banner** is pinned at the top of the body (renders
  before the Identity strip; `border-b border-[var(--color-error)]/20`
  accent for visibility — matches the existing
  `connection-error-banner` pattern at `AppShell.tsx:128`).
- All `ro` fields render as **disabled inputs** (not hidden) for
  transparency — uses the same `disabled` attribute pattern as the
  desktop wireframe (§6.5).
- The **Voice field** is shown-but-disabled per §6.5 and §11 #2. The
  dynamic widget logic (dropdown / free-text / disabled) follows the
  global voice-provider detection (`voice-provider-detect.ts`), but
  on built-ins the widget is locked to disabled regardless of the
  provider's enum.

### 13.7 Acceptance criteria (mobile-specific)

| Test | Expected |
|---|---|
| Open `/agents` on a 390 px viewport | Roster renders as 1-column grid; existing `grid-cols-1` from `AgentListScreen.tsx` is already correct |
| Open `/agents` on a 768 px viewport (tablet) | Roster renders as 2-column grid; existing `sm:grid-cols-2` |
| Open `/agents` on a 1280 px viewport | Roster renders as 3-column grid; existing `lg:grid-cols-3` |
| Tap a "+ New agent" button on phone | Wizard opens as a full-width Sheet (existing `widthClass="w-full sm:max-w-3xl"`) |
| Wizard footer on phone | Back / Next buttons stack vertically (primary on top), per the existing `flex-col-reverse sm:flex-row` |
| Wizard footer on tablet (`sm:`) | Back / Next buttons sit side-by-side |
| Open edit slide-over on phone | Sheet renders at `90vw` (existing `w-[90vw] sm:max-w-2xl`); effectively full-width |
| Tab bar inside edit on phone | Tabs row wraps or scrolls horizontally; all labels visible |
| Section accordions inside tabs on phone | Single-open accordion (one section visible at a time) |
| Tap Delete on phone | `AlertDialog` opens centered; primary action on top per `flex-col-reverse` |
| Tap Voice picker on built-in Mia (phone) | Field visible but disabled; tooltip explains why |
| Submit a wizard on phone | Identical 400 / 201 / override behaviour as desktop (no mobile-specific validation gaps) |
| No horizontal scroll on `/agents` at 360 px | Pass; the only horizontal scroll allowed is inside the tab bar of an open edit (already a documented exception) |

### 13.8 What was REMOVED in this rewrite

Earlier drafts of this section proposed several patterns that **do not
exist in the app** and were removed to stay consistent:

- ❌ A sticky bottom bar with 3 icon `+Add` buttons — not in the app;
  the existing `New agent` button in the section header is the
  established pattern.
- ❌ A "bottom sheet" pattern for color / icon / model pickers — no
  `Drawer` / `BottomSheet` component exists; all pickers use the
  existing `Sheet` (slide-over).
- ❌ A "full-screen modal" as a distinct component — the slide-over
  already becomes effectively full-width on phone via the existing
  `w-[90vw]` / `w-full` widthClass pattern. There is no separate
  full-screen surface.
- ❌ An overflow menu for tabs on phone — the existing `<Tabs>`
  component handles narrow viewports by wrapping or scrolling; an
  overflow menu is not part of the app vocabulary.

If any of these patterns are wanted, they require new components and
should be raised as separate design tasks — out of scope for this
agent-form spec.

---

**Status:** Approved (review pending). Implementation PR target: hotfix/v0.1.1 Wave 6 follow-up. Estimated implementation effort: 1 frontend PR (~600 LOC) + 1 backend PR (~200 LOC) + 1 contract PR (~100 LOC) + test coverage.
