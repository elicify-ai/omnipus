# Agent Types — Field Matrix

Reference for which fields belong to which agent type, side by side. Grounded in
the code as of `hotfix/v0.1.1` (2026-07-03): the wire contract
(`contracts/components/schemas/Agent*.yaml`), the gateway handlers
(`pkg/gateway/rest.go` — locked-agent rejection set at ~2358), the runtime
(`pkg/agent/instance.go`), and the create/edit UI (`CreateAgentWizard`,
`AgentProfile`). Companion to `docs/internal/specs/agent-form-requirements.md`
§2 (the 3-type wire taxonomy) and the planned discriminated-union contract
change (operator-approved 2026-07-03, pending two field decisions — see
"Open decisions" at the bottom).

## The four kinds at a glance

| | **Built-in (`core`)** | **Main** | **Subagent** (native worker) | **subagent_3p** (external worker) |
|---|---|---|---|---|
| What it is | Seeded roster: Mia · Jim · Ava · Ray | User-defined chat colleague | User-defined delegation-only worker | User-defined delegation-only worker on an external CLI |
| Runs on | Omnipus engine | Omnipus engine | Omnipus engine | claude-code / codex / opencode |
| Chat target | yes | yes | no (delegation only) | no (delegation only) |
| Created via | seeded (`coreagent.SeedConfig`) | wizard (3 steps) | wizard (3 steps) | wizard (**2 steps** — Identity+Runner → Personality) |
| Deletable | **no** (403) | yes | yes | yes |
| `locked` on the wire | `true` | `false` | `false` | `false` |
| Prompt (SOUL) | compiled-in, immutable | SOUL.md, editable | SOUL.md ("task prompt"), editable | SOUL.md (composed into the runner's system prompt) |

## Field-by-field matrix

Legend: **R** = required · **O** = optional · **RO** = visible read-only ·
**inherit** = may be inherited from the caller instead of set ·
**—** = not applicable / hidden (and, once the discriminated-union contract
lands, rejected by schema) · **defaults-only** = not settable per agent in the
UI; inherits `agents.defaults`.

| Field | Built-in (`core`) | Main | Subagent | subagent_3p |
|---|---|---|---|---|
| `type` | `core` (server-set) | **R** (discriminator) | **R** (discriminator) | **R** (discriminator) |
| `name` | RO — 403 on change | **R** | **R** | **R** |
| `description` | RO — 403 on change | O | **R** | **R** |
| `color` / `icon` | RO — 403 on change | O | O | O |
| `soul` (SOUL.md) | RO (compiled) — 403 on change | **R** at create (minLength 1) | **R** at create | **R** at create |
| `model` (+ `provider`) | O (editable) | **R** | **R** or **inherit** (`inherit_model`) | **R** — handed to the CLI (ADR-032) |
| `fallback_models` | O (editable) | O | O | — (runner manages retries) |
| `model_params` | O | O | O | — (runner-side concern) |
| `voice` | O (Main-surface concept) | O (**Main-only** among user types) | — (no chat/TTS surface) | — |
| `skills` | RO — 403 on change (compiled capability set, B-2) | O | O or **inherit** (`inherit_skills`) | — (external runner can't load Omnipus skills; UI hidden 2026-07-03) |
| `tools_cfg` (per-tool allow/ask/deny) | O (editable; e.g. mailbox grant fills entries) | O | O or **inherit** (`inherit_tools`) | — (runner has its own tools; per-tool CLI flags govern instead) |
| `sandbox_profile` + `shell_policy` | defaults-only in UI | O | O or **inherit** (`inherit_sandbox`) | — (runner manages its own isolation) |
| `executor` (cli, cli_path, args, env) | — | — | — (native) | **R** (`cli` **R**, `cli_path` **R**, validated on blur; `args`/`env` O) |
| `timeout_seconds` | O (editable; Execution knobs exposed for locked, decided 2026-07-03) | O | O | O — kept (operator-decided 2026-07-03; process-level kill for a hung CLI) |
| `max_tool_iterations` (per-turn cap) | O (editable; Execution knobs exposed for locked, decided 2026-07-03) | O (default 200/turn) | O (default 200/turn) | — excluded (operator-decided 2026-07-03; the external CLI runs its own loop; schema-rejected on create, 400 on update) |
| `steering_mode` | O (Main-surface concept) | O (**Main-only** among user types; workers forced `one-at-a-time` server-side) | — | — |
| `rate_limits` | O | O | O | O (calls still metered at the gateway) |
| `delegation_policy` / `can_delegate_to` | O (e.g. Jim's orchestration edges) | O | O (as delegation *target*) | O (as delegation *target*) |
| `default` (★ default agent) | O (Mia seeded default) | O | — (workers are never chat targets) | — |
| `heartbeat` | — **moved**: workspace-scoped (`member_configs`, ADR-027) — not an agent field for any type | — | — | — |
| `workspace` membership | via workspace `core_team` | via `core_team` | via `core_team` | via `core_team` |
| email mailbox | per (agent, workspace) pair — ADR-033; owner must be workspace `core_team`; **workers excluded** | same | — (worker) | — (worker) |

## Notes that keep biting

- **Built-in ≠ untouchable.** Locked core agents reject only the identity/
  capability set — `name`, `description`, `soul`, `color`, `icon`, `skills`
  (403 `cannot modify locked agent identity or prompt`). Model, fallbacks,
  timeout, max-tool-iterations, steering, rate limits ARE mutable via the API.
  Decided 2026-07-03: the profile UI exposes the Execution knobs (sampling,
  rate limits, execution) as EDITABLE for locked agents, and shows
  description/color/icon as visible read-only (like `name`).
- **The card for the built-in `worker` agent must open.** Its ID is literally
  `worker` — a route guard that treats `worker` as "not an agent ID" silently
  swallows the click (fixed `3bd7f355`).
- **subagent_3p is the type where "shared dynamic form" bugs concentrate.**
  Every field in its "—" rows has at some point leaked into its UI (skills
  mapping, Tools wizard step, duplicated runner form — all fixed 2026-07-03).
  The discriminated-union contract exists to make those leaks schema
  violations instead of silent junk.
- **Inheritance is a native-Subagent-only concept.** `inherit_model` /
  `inherit_tools` / `inherit_skills` / `inherit_sandbox` mean "resolve from the
  delegating caller at run time". Main has nothing to inherit from; an external
  runner resolves nothing from Omnipus.

## Decisions (resolved 2026-07-03, operator-approved)

1. `max_tool_iterations` on `subagent_3p`: **excluded** — schema-rejected on
   create (discriminated-union variant), 400 on update, hidden in UI.
2. `timeout_seconds` on `subagent_3p`: **kept** — process-level kill for a
   hung CLI; settable at create (slim Advanced) and edit.
3. `delegation_policy` on `subagent_3p` create: **allowed** (matrix +
   update-path consistency; previously 400).
4. The create contract is a discriminated union (`AgentCreateRequestMain` /
   `AgentCreateRequestSubagent` / `AgentCreateRequestSubagent3p`, hosted
   inline in `contracts/openapi.yaml`, `additionalProperties: false`,
   `type` required). Update stays flat with server-side per-type rejection
   (`pkg/gateway/agent_field_rules.go`).
