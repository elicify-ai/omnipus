# Agent Property Matrix — `Main` / `Subagent` / `Subagent (External)`

**Status:** Approved (review pending)
**Date:** 2026-06-18
**Author:** Daniel Piatkowski `<10800669+daniel-piatkowski-ai@users.noreply.github.com>`
**Branch target:** `hotfix/v0.1.1` (Wave 6 follow-up)
**Replaces:** the prior 2-type taxonomy (`custom` / `worker`)

> **2026-07-04 — `sandbox_profile` (row 17) removed entirely** (ADR-035): it
> never differentiated the actual kernel-enforced boundary per agent — that
> boundary is a single global Landlock/seccomp policy applied at gateway
> boot, identical for every agent. Row 18 (`shell_policy`) is unaffected and
> stays exactly as described below. References to `sandbox_profile` and
> `inherit_sandbox` throughout this document are historical.

This is the **canonical reference** for what fields exist on each agent type,
which are required vs optional vs hidden vs read-only, and why. Read this
before touching `AgentCreateRequest.yaml`, `AgentUpdateRequest.yaml`, or
the create/edit UI for agents.

---

## 1. The three user-creatable agent types

| Type | Lifecycle | Runtime | Audience |
|---|---|---|---|
| **Main** | Chat colleague (human ↔ agent) | Omnipus engine (native) | The user |
| **Subagent** | Delegation-only worker | Omnipus engine (native) | The Main agent (via delegation) |
| **Subagent (External)** | Delegation-only worker | External CLI (`claude-code` / `codex` / `opencode`) | The Main agent (via delegation) |

There is also the **locked core roster** (the "built-in Main" column in
this spec — currently `Mia`, `Jim`, `Ava`, `Ray`). Built-ins ship seeded
with sensible defaults and are not user-creatable. They share all wire
fields with user-creatable Main agents but their `ro`/seeded status is
determined by `Agent.locked: true`.

A future **built-in Subagent** is reserved (for "Shipped Omnipus skill
agents"). No wire field work is needed today; the column in the matrix
is kept for symmetry.

---

## 2. The matrix

Convention:
- **R** = Required (cannot save without a value)
- **O** = Optional (writable, may be omitted)
- **ro** = Read-only (visible, not editable)
- **derived** = Set automatically by the server/client based on `type`; not a form field
- **—** = Hidden / not applicable to this type
- **external editor** = Edited in a dedicated UI surface (not the agent form)

`built-in Main` is the locked core chat agent (`Mia` / `Jim` / `Ava` / `Ray`).
`built-in Subagent` is reserved for future shipped skill agents.

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
| 25 | ~~`delegation_policy.{to[], accept_from, modes, depth, budget}`~~ **removed by ADR-037** — delegation is workspace-scoped (workspace Team tab); the global `/agents/trust` editor is deleted | — | — | — | — | — |
| 26 | `default` (the "is this the global default agent?" toggle) | ro (Mia seeded) | **—** | O | **—** | **—** |
| 27 | `timeout_seconds` | R (seeded) | R (seeded) | R (UI default 300) | R (UI default 300) | R (UI default 300) |
| 28 | `max_tool_iterations` | R (seeded) | R (seeded) | R (UI default 50) | R (UI default 50) | R (UI default 50) |
| 29 | `steering_mode` | R (seeded) | **—** | R (UI default `one-at-a-time`) | **—** (internal: `one-at-a-time`) | **—** (internal: `one-at-a-time`) |

Runtime / status fields (not in the form):

| Property | All types |
|---|---|
| `id` | ro (server-assigned UUID for custom; well-known string for built-ins) |
| `status` | ro (`idle` / `active` / `error` / `draft`) |
| `warning` | ro (advisory from save+reload outcome — empty when healthy) |
| `created_at`, `updated_at` | ro |
| `locked` | ro (server-set; `true` for built-ins only) |
| `stats` | ro (per-agent runtime metrics) |

---

## 3. Per-row rationale

The "why" for every cell that is not obvious:

### 3.1 `name` (row 1)
Identifies the agent in the agent list and command-center pickers. Required
on every user-creatable type — even an empty Agents page needs labels.
Built-ins carry the seeded names (`Mia`, `Jim`, `Ava`, `Ray`).

### 3.2 `type` (row 2)
This is a **lifecycle choice**, not a runtime config. The user makes it once
at create time. After create, the runtime (`executor.kind`) is implied:
- `type: Main` → `executor.kind: native`
- `type: Subagent` → `executor.kind: native`
- `type: subagent_3p` → `executor.kind: external-cli`

So `executor.kind` is **derived**, never a form field, and never exposed
in the UI. The wire field still exists (the server needs it), but the
client computes it from `type` and never asks the user.

### 3.3 `description` (row 3)
For **Main**, the description is a human-readable subtitle on the agent
card. Optional — the user can leave it empty if the name alone is enough.
For **Subagent / Subagent (External)**, the description is the **basis on
which an orchestrator decides which agent to delegate to**. Without it,
the worker is unroutable. Hence required for workers.

### 3.4 `color` (row 4) and `icon` (row 5)
**Visual identity is mandatory.** Every agent needs a swatch + icon in the
Agents list and command-center pickers. Both are R at the wire layer,
with the UI **pre-selecting a sensible default** so users can "skip" the
field without it being empty:
- `color` default = first entry in `AVATAR_COLORS` (`Verdant`)
- `icon` default = `"Robot"`

This matches the pattern of `name` and `model_params`-with-defaults — R at
the wire, with the UI never letting the user see an empty value.

### 3.5 `model` (row 6)
Mandatory everywhere, but the **input widget differs by type**:
- **Main / Subagent**: the model picker (filtered by connected providers
  per Wave C4 G12's "unresolved" indicator).
- **Subagent (External)**: free-text slug input. The value goes verbatim
  into the CLI invocation (`claude --model <slug>`). The picker wouldn't
  work because Omnipus doesn't know which models the external CLI supports
  (each CLI uses its own model naming).

The free-text-for-3P requirement is what changed the wire schema from
`O` (in the old `custom/worker` taxonomy) to `R` (here).

### 3.6 `model_params.{temperature, max_tokens, top_p}` (row 7)
Sampling parameters. **Hidden for Subagent (External)** because Omnipus
can't guarantee every CLI supports these flags (`claude-code` may support
`--temperature`, `opencode` may not). Sending them to a CLI that ignores
them is silent dead behavior; better to hide the field.

### 3.7 `soul` (row 8) — ⭐ MANDATORY EVERYWHERE
The SOUL.md content. **Required for every user-creatable type**, including
Subagent (External). For Main and Subagent, Omnipus injects it into every
LLM call. For Subagent (External), Omnipus **passes it as part of the CLI
prompt at runtime** — the CLI never reads a file from disk; it gets the
agent identity inline.

This corrected an earlier draft where Subagent (External) had `—` for
`soul`. Soul is universal.

### 3.8 `instructions` (row 9)
The AGENT.md body. Same scoping as `soul` (everywhere), but **optional**
(empty is valid — agents start with just `soul.md` and add `instructions`
later when needed).

### 3.9 `heartbeat*` (rows 10–12)
The HEARTBEAT.md periodic poll. **Only meaningful for chat agents**
(built-in Main, Main). Workers don't poll — they're invoked by their
parent. Hidden for all worker types.

`heartbeat_enabled` and `heartbeat_interval` are conditional on
`heartbeat` being set at all — if `heartbeat` is empty, both are hidden.

### 3.10 `voice` (row 13)
TTS voice identifier. **Only meaningful for Main** (chat agents produce
audio output to humans). Workers don't produce TTS — they produce text
or structured output for their parent. Hidden for all worker types.

For the dynamic widget behavior (dropdown when provider has a voices enum,
free text when not, disabled when no voice provider configured), see the
`voice` spec at the bottom of this doc.

### 3.11 `tools_cfg` (row 14)
**Hidden for Subagent (External)**. The CLI bypasses Omnipus entirely for
execution; Omnipus tools (`web_search`, `file_read`, `shell`, `code_exec`)
are not accessible to the CLI process. Showing the field would be a
dead-end setting. The frontend hides it; the backend silently drops the
field on Subagent (External) PUTs anyway.

### 3.12 `skills[]` (row 15)
**Hidden for Subagent (External)** — same reason as `tools_cfg`. Skills
are Omnipus-invoked; the CLI process doesn't see them.

Note: **Subagent (native) DOES have skills**, even though it might seem
analogous to Subagent (External). The native worker is invoked BY Omnipus,
so Omnipus can pre-resolve a skill result and pass it as context to the
worker. The CLI worker has no such integration point.

### 3.13 `fallback_models[]` (row 16)
Ordered chain of `{model, provider}`. **max 2** everywhere (was 10 — the
schema change is part of this spec).

**Hidden for Subagent (External)** because the CLI handles its own
retries. Omnipus doesn't pre-empt with fallback chains when the worker
is a CLI subprocess.

### 3.14 `sandbox_profile` (row 17) and `shell_policy.*` (row 18)
Both are **operator-tunable** for all types where the runtime is Omnipus
(because Omnipus enforces Landlock + shell deny patterns via the agent
loop). **Hidden for Subagent (External)** because the CLI manages its
own isolation (Omnipus can't enforce Landlock on a CLI subprocess).

For **built-in Main / built-in Subagent**: not seeded with values; the
operator chooses them. The "locked" lock applies to **identity** fields,
not operational ones.

### 3.15 `executor.kind` (row 19) — derived, never a form field
This was the biggest simplification. There is no form field for
`executor.kind` — the user picks `type` at create time, and the kind
follows. Removing the kind picker eliminates a redundant question
(the user already chose `Subagent (External)` at the type picker, so
they don't need to also choose `external-cli` in the executor panel).

### 3.16 `executor.cli` (row 20)
The CLI tool choice. **Required for Subagent (External)** only. Other
types run on Omnipus engine — no CLI to pick.

### 3.17 `executor.cli_path` (row 21)
The absolute path to the CLI binary. **Required for Subagent (External)**
because the user explicitly told us they need the path passed to the
invocation command — relying on `$PATH` resolution at runtime is fragile
when multiple CLI versions may be installed.

### 3.18 `executor.env_overrides` (row 22)
Per-CLI env vars. Optional. When set, merged into the spawned process's
env alongside Omnipus's own (`OMNIPUS_AGENT_NAME`, `OMNIPUS_AGENT_TYPE`).

### 3.19 `executor.cli_args` (row 23)
Free-form additional CLI arguments. Optional. Joined to the CLI invocation
as trailing args. Light validation (warns on shell-injection chars like
`;`, `|`, `` ` ``, `$()`) but does not reject — power users may need them.

### 3.20 `rate_limits.*` (row 24)
Optional everywhere. When `use_global_defaults: true` (default), the
global `agents.defaults.rate_limits` applies. For Subagent (External),
these gate the Omnipus-mediated retries and callbacks, NOT the CLI's
internal rate (the CLI manages that).

### 3.21 `delegation_policy.*` (row 25) — REMOVED by ADR-037
The per-agent `delegation_policy` field and the global `/agents/trust`
delegation-graph editor were **deleted entirely by
[ADR-037](../architecture/ADR-037-remove-global-delegation-policy.md)** (no
back-compat, matching the ADR-035 `sandbox_profile` precedent). Delegation
trust is **workspace-scoped**: edit it in a workspace's **Team tab**. The
per-workspace `Delegation[]` edge list (`pkg/workspace/delegation.go`) has been
the sole runtime authority since commit `822202ad` (2026-06-27); the per-agent
field was dead in enforcement and only seeded new workspaces. `accept_from`,
`modes`, `depth`, `budget` no longer exist even in the retained seed DTO. The
struck prose below is kept only as a record of the removed surface.

> ~~Edited in the delegation graph UI (`/agents/trust`), never in the agent
> form. Main agents only — workers don't delegate. `accept_from`, `modes`,
> `depth`, `budget` were schema fields but NOT enforced in v0.1.0 (a startup
> WARN was emitted if either was non-empty); they showed up in the graph UI as
> advisory.~~

### 3.22 `default` (row 26) — the G3 toggle
Whether this agent is the global default that handles unrouted inbound.
- **built-in Main**: Mia ships as default.
- **Main**: optional toggle.
- **all worker types**: never default — workers don't handle unrouted
  inbound.

### 3.23 `timeout_seconds` (row 27) and `max_tool_iterations` (row 28)
Both required at the wire layer with UI default pre-selected (300s / 50).
Same pattern as `color` and `icon` — never saveable empty.

For Subagent (External), `timeout_seconds` bounds the Omnipus wait for
the CLI process — if the CLI takes longer, Omnipus hard-kills it.

### 3.24 `steering_mode` (row 29)

> **2026-07-17 — per-agent `steering_mode` removed entirely (dead config):**
> create silently dropped it, PUT persisted it to a config location
> `config.AgentConfig` never loads, and GET always echoed the global default
> regardless of any per-agent value — the field never actually worked.
> Steering is now global-only and always-on via `agents.defaults.steering_mode`
> (`pkg/agent/steering.go` / `pkg/config/config.go`), unaffected by this
> removal. The description below is historical.

Controls how a Main agent handles a new human message arriving while a
previous turn is still running. **Only Main agents** see this option in
the UI. Workers never show the field; the server always sets
`one-at-a-time` for them.

Default is `one-at-a-time` (strict serial — agent finishes current turn
before starting next, no interleaving). `queue-and-process` is the
advisory alternative.

---

## 4. Fields deliberately dropped from the wire

These were considered and **removed** from the agent form (and from the
wire schema where they were never load-bearing):

| Dropped field | Why |
|---|---|
| `tool_feedback` (per-agent bool) | This is **channel behavior**, not an agent property. The web chat surface already renders tool calls inline (`SessionPanel.tsx`, `ToolCallBadge`, `SubagentBlock`); publishing them again as outbound messages is duplication. The right behavior is per-channel routing at runtime: `webchat` → never, messaging → always, `system`/`cli`/`cron` → never. |
| `executor.auth_method` | Each CLI handles its own auth (claude-code OAuth, etc.). Omnipus just spawns the CLI process — the CLI runs `login` on first use and stores credentials locally. Trying to surface auth in Omnipus duplicates CLI UX. |
| `executor.instructions_file` | Omnipus passes `soul` + `instructions` as prompt content at invocation time, not via a file the CLI reads. There is no file path to configure. |
| `executor.model_override` | `model` IS the model name passed to the CLI. The reason `model` is free text for Subagent (External) is exactly so it matches the CLI's model naming. No separate override field needed. |
| `executor.workdir` | Set by the calling Main agent at runtime (the invocation context). It's not part of the Subagent's static definition. |

---

## 5. The wire schema diff (to ship in Wave 6 follow-up)

```diff
# contracts/components/schemas/AgentCreateRequest.yaml

 required:
   - name
+  - soul
 properties:
   name:
     type: string
     minLength: 1
   type:
     type: string
     enum:
-      - custom
-      - worker
+      - Main
+      - Subagent
+      - subagent_3p
     default: Main
     description: >
-      Agent tier to create. "custom" = a user-defined chat colleague
-      (default for the existing POST /agents flow). "worker" = a sub-agent
-      worker: a delegation-only labour agent that is NOT a chat target, has
-      no heartbeat, is never the default, and must carry an executor
-      (see Agent.executor). When omitted, the server creates a "custom"
-      agent. The "core" and "system" types are reserved and cannot be
-      created via this endpoint.
+      Agent tier. "Main" = chat colleague. "Subagent" = delegation-only
+      worker on the Omnipus engine (native executor). "subagent_3p" =
+      delegation-only worker on an external CLI
+      (claude-code / codex / opencode; `executor.kind: external-cli`).
+      When omitted, the server creates a "Main" agent.
   description:
     type: string
   model:
     type: string
-    description: >
-      Model name for LLM calls. When omitted, the global agents.defaults.model_name is used.
+    description: >
+      Model name. For Main / Subagent: rendered as a picker filtered by
+      connected providers. For Subagent (External): free-text slug passed
+      verbatim to the CLI invocation. Required.
   fallback_models:
     type: array
-    maxItems: 10
+    maxItems: 2
   rate_limits:
     # unchanged
+  soul:
+    type: string
+    minLength: 1
+    description: >
+      Required SOUL.md content (markdown). Mandatory for all agent types —
+      even Subagent (External) where it is passed as prompt content at
+      invocation time, not via a file.

# contracts/components/schemas/AgentUpdateRequest.yaml
- REMOVE tool_feedback (channel behavior, not agent config)

# contracts/components/schemas/Agent.yaml (read-only on GET)
 type:
   enum:
-    - core
-    - custom
-    - system
-    - worker
+    - core       # locked core Main (Mia / Jim / Ava / Ray)
+    - system     # reserved; not user-creatable
+    - Main       # user-creatable chat colleague
+    - Subagent   # user-creatable worker on native
+    - subagent_3p  # user-creatable worker on external CLI

# contracts/components/schemas/ExecutorConfig.yaml
 properties:
   kind:
-    description: Executor kind. Selected by the user.
+    description: >
+      Executor kind. Derived from `type` — `Main` and `Subagent` set this
+      to `native`; `subagent_3p` sets this to `external-cli`. Not editable.
   cli:
     enum: [claude-code, codex, opencode]
+    description: >
+      CLI tool. Required when kind=external-cli. Ignored otherwise.
+  cli_path:
+    type: string
+    description: |
+      Absolute path to the CLI binary on disk.
+      Required when kind=external-cli. The CLI is invoked as
+      `<cli_path> [cli_args...] --prompt <prompt>`.
+  env_overrides:
+    type: object
+    additionalProperties: { type: string }
+    description: >
+      Additional environment variables injected into the spawned CLI process.
+      Merged with Omnipus's own (OMNIPUS_AGENT_NAME, OMNIPUS_AGENT_TYPE).
+  cli_args:
+    type: string
+    description: >
+      Free-form additional CLI arguments. Joined to the invocation
+      command after the binary. Validated lightly (warns on
+      shell-injection chars; does not reject).
```

---

## 6. UI mapping (how this lands in the create/edit wizards)

### 6.1 Create wizard — step 0 (type picker)

| Type | Subsequent steps |
|---|---|
| **Main** | ① Identity → ② Personality (soul ⭐ R + heartbeat + voice + instructions) → ③ Tools (tools_cfg + skills + fallback_models) → Advanced disclosure (sandbox_profile + shell_policy + rate_limits + timeout + max_iter + steering + tool_feedback REMOVED + **delegation_policy REMOVED — ADR-037**) |
| **Subagent** | ① Identity → ② Personality (soul ⭐ R + instructions only — no heartbeat/voice) → ③ Tools (tools_cfg + skills + fallback_models) → Advanced disclosure (sandbox_profile + shell_policy + rate_limits + timeout + max_iter) |
| **Subagent (External)** | ① CLI choice (claude-code / codex / opencode) → ② Identity → ③ Runtime config (cli_path R + env_overrides + cli_args) → ④ Tools (skills + fallback_models; tools_cfg hidden) → Advanced disclosure (model, rate_limits, timeout, max_iter) |

### 6.2 Edit slide-over (the layout for each type)

| Tab | Main | Subagent | Subagent (External) |
|---|---|---|---|
| **Basics** | name, description, color, icon, default, model, model_params, voice, timeout, max_iter | name, description, color, icon, model, model_params, timeout, max_iter | name, color, icon, model, timeout, max_iter |
| **Personality** | soul ⭐, heartbeat, heartbeat_enabled, heartbeat_interval, instructions | soul ⭐, instructions | soul ⭐, instructions |
| **Tools** | tools_cfg, skills, fallback_models | tools_cfg, skills, fallback_models | skills (tools_cfg and fallback_models hidden) |
| **Advanced** | sandbox_profile, shell_policy, rate_limits, timeout, max_iter, steering_mode, cli (hidden) | sandbox_profile, shell_policy, rate_limits, timeout, max_iter, cli (hidden) | executor.{cli, cli_path, env_overrides, cli_args}, rate_limits, timeout, max_iter |

For **built-in Main / built-in Subagent**: same layout, but editable cells
are ro (the form shows them disabled, with a tooltip: *"Locked agent —
seeded at gateway boot."*).

### 6.3 `voice` widget (Main / built-in Main only)

| Condition | UI |
|---|---|
| No voice provider configured globally | Field **disabled** with tooltip: *"Configure a voice provider in Settings → Voice to enable per-agent voice."* |
| Voice provider configured **and** exposes a standardized voices enum (OpenAI TTS, ElevenLabs, Google, Azure, Piper) | **Dropdown** populated from the provider's voices API |
| Voice provider configured **but** exposes no enum | **Free-text input** with placeholder: `e.g. alloy` |

---

## 7. Open questions for follow-up

1. **`subagent_3p` slug vs display** — wire enum uses `subagent_3p` (snake-case per contract convention); UI displays `Subagent (External)`. Confirmed in this doc. **No follow-up needed unless the spec needs revisiting.**
2. **`core` enum retention** — `core` and `system` stay in `Agent.yaml` enum for the locked core roster (`Mia`/`Jim`/`Ava`/`Ray`) and the reserved `system` type. They are NEVER in `AgentCreateRequest.yaml`. Confirmed.
3. **Worker description copy** — the Create wizard should label the `description` field differently for workers (e.g., *"What does this worker do? (used by the orchestrator to decide when to delegate)"*) vs Main (*"Short description (shown as subtitle)"*). UI copy change, not a schema change.
4. **`voice` provider discovery** — the dynamic widget logic (dropdown vs free text vs disabled) needs a runtime check against the configured voice provider's API. The current contract has `voice: string | nullable`; the UI decision is based on the gateway's voice-provider config. **Owner: frontend-lead + new audit issue for runtime detection.**

---

## 8. Files this spec changes

- `contracts/components/schemas/AgentCreateRequest.yaml` — `type` enum + `soul: required` + `fallback_models.maxItems: 2`
- `contracts/components/schemas/AgentUpdateRequest.yaml` — remove `tool_feedback`
- `contracts/components/schemas/Agent.yaml` — `type` enum
- `contracts/components/schemas/ExecutorConfig.yaml` — add `cli_path`, `env_overrides`, `cli_args`; mark `kind` as derived (doc only)
- `src/components/agents/CreateAgentModal.tsx` — type picker at step 0; wizard branches by type
- `src/components/agents/AgentProfile.tsx` — tab contents vary by `type` per the table above
- `src/components/ui/FormError.tsx` — unchanged (already correct)
- `src/lib/agents/voice-provider-detect.ts` — NEW: runtime check for `voice` widget behavior
- `pkg/gateway/rest.go` — handle the renamed `type` enum + new required `soul` validation

---

## 9. Test coverage requirements

For each row of the matrix, there must be at least one test that
asserts:

| Test type | Coverage |
|---|---|
| **wire schema** | `make verify-contracts` exits 0; `lint-wire-types` reports 0 hand-written wire-format structs |
| **create payload** | A test that POSTs each type and asserts the server accepts + persists the required fields (soul, color, icon, model, timeout, max_iter, default fields per type) |
| **hidden fields** | A test that POSTs a `Subagent` with `tools_cfg` set and asserts the server drops it (or rejects with 400); same for `Subagent (External)` with `sandbox_profile` |
| **derived `executor.kind`** | A test that POSTs `Main` with `executor.kind: external-cli` and asserts the server overrides to `native` |
| **steering_mode for workers** | A test that POSTs a `Subagent` with `steering_mode: queue-and-process` and asserts the server overrides to `one-at-a-time` |
| **voice widget logic** | A frontend test that asserts the field is hidden for non-Main types, dropdown when provider has enum, free text otherwise |
