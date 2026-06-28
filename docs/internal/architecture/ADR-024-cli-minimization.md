# ADR-024 — CLI Minimization: a thin one-shot task-runner over the engine

- **Status:** Proposed (revised 2026-06-28 after grill-spec review)
- **Date:** 2026-06-28
- **Deciders:** operator, Albert (architect)
- **Evidence level (highest used):** 1 (user-provided decisions) grounded against 2/3 (codebase facts)

> Ratification ADR. The direction was decided with the operator in session on
> 2026-06-28; this record grounds each decision in code and assigns per-decision
> confidence. It does not re-open settled calls.
>
> **Review incorporated.** An adversarial grill-spec pass
> (`ADR-024-cli-minimization-review.md`, verdict PASS-WITH-CONDITIONS) found two
> blockers and five majors. All are folded into this revision: execute path is
> **WS-only** (MA-1); the one-shot **approval behavior** is specified (CR-2);
> local auth commits to a **CLI-owned 0600 token file**, loopback-trust rejected
> (CR-1); the **`gateway` alias is kept** and removed verbs are **hard-removed**
> with co-ordinated internal-caller updates + a CI grep-guard as a deliverable (MA-2/MA-3); the
> auto-start mechanism is **"invoke `start`" (persistent)**, which dissolves the
> ephemeral/channel-flapping problem (OB-1); and readiness/race/unlock preconditions
> are specified (MA-5). Hands to `/plan-spec` with security-lead owning the token
> handshake.

---

## 1. Problem Understanding

The `omnipus` CLI (`cmd/omnipus/`) was written for an early version of the app and
has drifted from the current architecture. The drift is not cosmetic — the primary
execution path is broken and several subcommands operate on dead data sources.

**Business objective.** Reduce the CLI to a small, correct, maintainable surface whose
single job is to *run an agent from the command line*, and stop it from being a
second, divergent copy of the engine that rots independently of the gateway.

**Stakeholders.** Operator (self-host/headless users, scripting/automation),
maintainers (drift surface), security (a second engine boot path that skips the
hardening the gateway applies).

**Blast radius.** Medium. The change deletes/deprecates user-facing subcommands
(breaking scripts that call them) and changes how `omnipus <agent>` reaches the
engine. It does **not** change the gateway, the SPA, or the execute wire contract.

**Grounded findings (the "why now"):**

- `[FACT]` The interactive `omnipus agent` REPL is broken out of the box. Its boot
  path does only `config.LoadConfig()` (`cmd/omnipus/internal/agent/helpers.go:38`)
  and never unlocks/injects credentials. `ModelConfig.APIKey()` returns
  `os.Getenv(c.APIKeyRef)` (`pkg/config/config.go:2478`) — the secret must be injected
  into the environment first. The gateway does this via `bootCredentials()`
  (`pkg/gateway/gateway.go:264`: NewStore→Unlock→LoadConfigWithStore→InjectFromConfig
  →ResolveBundle→RegisterSensitiveValues). The CLI skips it, so provider init fails
  with `"api_key or api_base is required"` before the REPL reads a keystroke.
- `[FACT]` Beyond that bug, the in-process REPL is a divergent engine: no streaming /
  tool-call output, no kernel sandbox, no audit chain, ask-policy tools hard-deny via
  the fallback `nopPolicyApprover`, no day-partitioned session persistence, single
  hardcoded agent.
- `[FACT]` Running an in-process engine while the gateway is up creates a
  **two-writers-one-datadir** hazard, and it is worse than a flock gap: the session
  *message* files have **no cross-process lock at all** — `context.jsonl` is appended
  via `JSONLStore.addMsg` with only an intra-process striped mutex + `O_APPEND`
  (`pkg/memory/jsonl.go:214-238`); `transcript.jsonl` via `fileutil.AppendJSONL` with no
  flock (`pkg/fileutil/file.go:151`). `WithFlock` covers only `meta.json`
  (`pkg/session/unified.go:412,826`). Two OS processes writing these can interleave/corrupt.
- `[FACT]` `auth` and `status` read the **deprecated OAuth store**
  (`cmd/omnipus/internal/status/helpers.go:45` `auth.LoadStore()`;
  `cmd/omnipus/internal/auth/helpers.go:53` `auth.SetCredential("openai", …)`), not the
  encrypted `credentials.json` the gateway uses — they misreport state after a real
  onboard.
- `[FACT]` The `cron` CLI manages `workspace/cron/jobs.json`, but the cron *tool* was
  retired in the v0.1.0 tool refactor — it manages a store agents can no longer use.
  `migrate` is OpenClaw-only.
- `[FACT]` What works and stays: `onboard` writes `master.key` + AES-256-GCM
  `credentials.json` + `api_key_ref` correctly; `credentials` uses the same Unlock
  contract as the gateway; `start` is already the canonical verb
  (`cmd/omnipus/internal/gateway/command.go:32` `Use:"start"`, `gateway`/`g` aliases).

## 2. Extracted Requirements

### Functional
- FR-1: `omnipus <agent> "<prompt>"` MUST run a single task on a named agent, print the
  result, and exit (no interactive REPL). `[FACT: operator decision]`
- FR-2: `<agent>` MUST be mandatory (a positional) and resolve to a **chat-target**
  agent in the roster. `[FACT: operator decision]`
- FR-3: `--model <slug>` MUST override the model for that run only, via the WS chat
  frame's `metadata.model_name` (`contracts/asyncapi.yaml:922-928`). `[FACT]`
- FR-4: Execution MUST go over the same engine the SPA uses (the **WS chat path**), not
  an embedded engine. `[FACT]` (The `/chat` SSE endpoint is **not** viable — see D2/MA-1.)
- FR-5: The CLI MUST require a reachable gateway; when none is reachable it MUST bring
  one up by **invoking the `start` path (persistent)**, poll `/health` until ready
  (bounded timeout), then run the task. Auto-start MUST require a non-interactive unlock
  mode (`master.key` / `OMNIPUS_KEY_FILE` / `OMNIPUS_MASTER_KEY`) and fail with guidance
  if none is available. `[FACT: operator decision + MA-5]`
- FR-6: `omnipus` with no args MUST list the available agents + command usage, with the
  roster read from local `config.json` (works with no gateway running). It MUST NOT
  promise token-usage here (that lives in the session store, not config). `[FACT + MI-2]`
- FR-7: `omnipus --help`/`help` MUST show a curated command reference. `[FACT]`
- FR-8: `onboard` MUST present an interactive numbered provider menu, keep its headless
  `--non-interactive` mode, print the access URL on completion, **and mint the CLI
  token (FR-13)**. `[FACT]`
- FR-9: `start` MUST print bind-aware access URL(s) with guidance on opening the UI, and
  ensure the CLI token (FR-13) exists. `[FACT]`
- FR-10: `credentials set/list/delete/rotate` MUST remain (rotation kept). `[FACT]`
- FR-11: Result MUST go to stdout, progress/tool activity to stderr (scriptable).
  `[FACT: operator decision]`
- FR-12: On a `*_approval_request` / `tool_approval_required` frame, a one-shot run MUST
  **default-deny and continue**, printing the blocked tool to stderr; an opt-in
  `--yes`/`--auto-approve` flag MAY auto-allow for that run, still gated by the agent's
  own policy. The run MUST NOT silently hang on the 90 s approver timeout. `[FACT: CR-2]`
- FR-13: The CLI MUST authenticate to its gateway with a **CLI-owned token persisted
  `0600` at `$OMNIPUS_HOME/cli.token`**, minted by `onboard`/`start` (plaintext to the
  file, bcrypt hash into `Gateway.Users` as a named principal). Loopback-trust is
  forbidden. For a remote gateway, `--url` + an explicit token over TLS only. `[FACT: CR-1]`

### Non-Functional
- NFR-1 (Drift-resistance): the CLI MUST NOT embed the engine boot/agent-loop. A
  regression test MUST assert the CLI never opens an in-process engine when a gateway is
  reachable. `[FACT]`
- NFR-2 (Auth scoping): the local token MUST NOT be sent to a `--url` remote, and a
  remote token MUST NOT be sent to localhost; non-TLS `http://` remotes MUST be rejected
  for any token send. `[INFERENCE + OB-2]`
- NFR-3 (Footprint): no new runtime dependency; single Go binary (Constraint #1/#2).
- NFR-4 (Offline bootstrap): `onboard`/`credentials` MUST function before any gateway
  exists. `[FACT]`
- NFR-5 (Performance): first call after a cold machine pays one gateway boot; subsequent
  calls reuse it. The health-poll budget MUST be a measured value (spike), not a guess.
  `[UNKNOWN: exact boot time]`

### Constraints
- Pure-Go single binary; build tags `goolm,stdjson`; contract-first (Constraint #8) —
  satisfied because the execute path is WS-only and adds no wire type. `[FACT]`
- Cross-platform daemon control (Constraint #4): the spawn/stop story MUST work on
  Windows (no `setsid`/`nohup`). `[FACT]`
- Constraint #7 (green): removals MUST update every internal caller in the same change.

## 3. Gaps and Ambiguities

| # | Item | Status after revision | Resolution / owner |
|---|---|---|---|
| G1 | Local-auth mechanism | **Resolved (direction)** — CLI-owned 0600 token file (FR-13); loopback-trust rejected. Exact principal/role is security-lead-gated. | `/plan-spec` + security-lead |
| G2 | Daemon lifecycle | **Resolved (direction)** — auto-start = invoke `start` (persistent); add `omnipus stop` (+ PID file) reconciled with the launcher's existing PID handling (`launcher-tui/ui/gateway.go:90`); cross-platform detach. | `/plan-spec` |
| G3 | Execute transport | **Resolved** — WS-only (MA-1). `/chat` SSE cannot carry `agent_id`/`--model` and is legacy/no-stats. | closed |
| G4 | Port contention / spawn race | **Open (bounded)** — only one auto-start may win: a `gateway.lock` flock guards spawn; health-check before spawn; clear error if a foreign process owns the port. | `/plan-spec` |
| G5 | Pre-onboard / headless-unlock execute | **Open (bounded)** — `omnipus <agent>` before onboarding, or with no non-interactive unlock available, MUST fail with guidance (run `onboard`; set a key mode). | `/plan-spec` |

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Drift-resistance | 30% | Does the CLI stay correct as the engine evolves, without parallel maintenance? |
| Data-safety | 20% | Avoids the two-writers-one-datadir corruption hazard. |
| Simplicity / footprint | 20% | Lines of code, surface area, single-binary fit. |
| Ergonomics | 15% | "Just works", scriptability, discoverability. |
| Offline capability | 15% | Works without a server for bootstrap; ideally for execution. |

## 5. Option Analysis

The architectural fork is **how `omnipus <agent> "<prompt>"` reaches the engine.**

### Option A — Pure thin client (require `omnipus start` first, error if down)
| Dimension | Assessment |
|---|---|
| Strengths | Simplest and safest: one engine ever; zero drift; no daemon lifecycle. |
| Weaknesses | Errors when nothing is running — extra manual step. |
| Risks | Poor first-run UX. |
| Complexity | Low. |

### Option B — In-process per call (no server)
| Dimension | Assessment |
|---|---|
| Strengths | Fully offline; preserves "single binary, no daemon". |
| Weaknesses | Re-embeds the engine — the drift source this ADR removes; must replicate the whole boot contract. |
| Risks | **Two-writers-one-datadir corruption** (§1, message files have no cross-process lock); security regressions. |
| Complexity | High. |

### Option C — Require a gateway, but auto-bring-up via `start` (persistent)  *(chosen)*
| Dimension | Assessment |
|---|---|
| Strengths | "Just works": connect if up, else invoke `start` (persistent) and reuse it. CLI is a *client* → cannot drift (NFR-1); no second writer. **Persistent (not ephemeral)** → channels connect once, no flapping; **one** start mechanism, no parallel auto-start path. |
| Weaknesses | Needs the token handshake (FR-13), a health-poll, a spawn lock, an `omnipus stop`, and a non-interactive unlock precondition. |
| Risks | Token-file at rest (mitigated 0600); orphaned daemon (mitigated by reuse + `omnipus stop`); spawn race (mitigated by lock); headless deadlock if no unlock mode (mitigated by precondition check). |
| Complexity | Medium — but lower than a bespoke auto-start because it reuses `start`. |

**Ephemeral per-task gateway (sub-variant, rejected):** spinning a gateway up and down
per task would flap every configured channel and pay full boot cost each call. Persistent
auto-start avoids both. `[INFERENCE]`

## 6. Recommended Architecture

Adopt **Option C** (persistent auto-bring-up via `start`) and the command decisions
below.

**Final command surface:**

```
omnipus <agent> "<prompt>"            one-shot task → result(stdout) → exit; progress→stderr
omnipus <agent> --model X "<prompt>"  per-run model override (metadata.model_name, WS)
omnipus <agent> --yes "<prompt>"      auto-approve ask-policy tools for this run
omnipus                               list agents + usage (roster from local config.json)
omnipus --help | help                 curated command reference
omnipus onboard                       first-run setup (numbered menu); prints URL; mints cli.token
omnipus start                         serve SPA+API; prints bind-aware URL(s); ensures cli.token
omnipus stop                          stop a CLI-started gateway (PID file)
omnipus credentials set|list|delete|rotate   headless secret management
```

Removed (hard cut — all internal callers updated in the same PR + a CI grep-guard):
`agent`, `auth`, `status`, `cron`, `migrate`, standalone `model`/`skills`. **Kept:** the
`gateway` alias for `start`. (Operator chose a hard cut over deprecation stubs, 2026-06-28.)

### D1 — CLI role = thin one-shot task-runner, not an embedded engine
The CLI's only execution job is to hand a named agent a prompt and stream back the
result. Justification: NFR-1 (drift) and data-safety — a client cannot drift from or
corrupt the engine it calls. `[FACT]`

```
CONFIDENCE: High
  Basis         : Direct code evidence of the broken/divergent in-process path; operator decision.
  Evidence      : helpers.go:38, gateway.go:264, the parity gaps and unlocked message files in §1.
  Missing       : Nothing material.
  Would improve : n/a.
```

### D2 — Execution = require a gateway; auto-bring-up via `start` (persistent), WS-only
`omnipus <agent>` connects to a reachable gateway over the **WS chat path**
(`asyncapi.yaml` MessageFrame carries `agent_id` + `metadata.model_name`). If none is
reachable, the CLI invokes the same `start` path to bring up a **persistent** gateway,
polls `/health` until ready, then runs the task. The `/chat` SSE endpoint is rejected:
`SseChatRequest` carries only `message` (`contracts/components/schemas/SseChatRequest.yaml:7-14`)
— no `agent_id` (FR-2), no model override (FR-3) — and is legacy/no-stats
(`pkg/gateway/sse.go`). No new wire type is required. `[FACT: MA-1]`

```
CONFIDENCE: Medium-High
  Basis         : Transport and mechanism now decided and contract-verified; only lifecycle/auth details remain.
  Evidence      : asyncapi.yaml:922-928; SseChatRequest.yaml:7-14; sse.go (legacy).
  Missing       : Health-poll budget (spike), spawn-lock + stop semantics (G2/G4).
  Would improve : A latency spike; the security-lead token handshake review.
```

### D3 — Command grammar: mandatory positional `<agent>` + prompt; `--model`; no REPL
The agent is a required first positional; the prompt is the second positional; `--model`
and `--yes` are flags. No REPL. Bare `omnipus` prints roster + usage; `--help` prints the
reference. `onboard`/`start`/`stop`/`credentials` are reserved names — agent creation MUST
reject/warn on a name that collides with a reserved verb (cobra resolves subcommands
first, so a colliding agent would be unreachable). `[FACT: operator decision + MI-3]`

```
CONFIDENCE: High
  Basis         : Operator decision; cobra precedence well understood.
  Evidence      : main.go command tree; session decisions.
  Missing       : Nothing material.
  Would improve : n/a.
```

### D4 — Agent targeting: chat-target agents only; worker/specialists out of scope
`<agent>` resolves to a chat-target agent (Mia/Jim/Ava/Ray/custom) reached via the chat
API. The worker is **not** addressable: the gateway hard-rejects a worker as a session
target (`pkg/gateway/rest.go:893`), and the general worker is the least-capable agent.
**Rejected option — "default a bare prompt to the least-privilege worker":** the chat API
rejects workers, the worker is the weakest agent, and it would force a separate
task/delegation endpoint. Mandatory explicit agent makes the question moot. An
isolated/memory-free worker-execute path is a **deliberate v-next item** (needs a
non-session delegation endpoint), to be confirmed acceptable for v1. `[FACT + MI-1]`

```
CONFIDENCE: High
  Basis         : Hard gateway invariant + capability evidence; operator chose mandatory agent.
  Evidence      : rest.go:893/2203/6219; coreagent/core.go:971; routing/route.go:296.
  Missing       : Operator confirmation that chat-target-only is acceptable for v1 (low risk).
  Would improve : One-line forward note (added to D4).
```

### D5 — Keep bootstrap commands in-process (`onboard`, `start`, `credentials`)
These create/manage on-disk state before any gateway exists, so they cannot be API
clients. `onboard` gains a numbered provider menu + end-of-run URL block **and mints
`cli.token`**; `start` gains a bind-aware URL block (honor `gateway.public_url`; always
show `localhost`; show the LAN IP only when `Gateway.Host` is `0.0.0.0`/empty; otherwise
hint to set `gateway.host=0.0.0.0`) **and ensures `cli.token` exists**. `credentials`
keeps `rotate`; the SPA Credential Vault remains the primary ongoing surface. `[FACT]`

```
CONFIDENCE: High
  Basis         : These commands already work against the current contract; additions are bounded.
  Evidence      : onboard.go (applyInput), command.go:32 (start), credentials/command.go.
  Missing       : LAN-IP enumeration + token-mint detail (implementation-level).
  Would improve : n/a.
```

### D6 — Removals (hard cut) and the kept alias
Operator decision (2026-06-28): **hard-remove** the dead verbs rather than ship
deprecation stubs. The break is mitigated not by stubs but by updating every internal
caller in the same PR and a CI grep-guard (below).
| Command | Disposition |
|---|---|
| `agent` (in-process REPL) | **Removed.** cobra "unknown command". Broken + divergent engine. `[FACT]` |
| `auth` | **Removed** → Settings → Security. Reads the dead OAuth store. `[FACT]` |
| `status` | **Removed** → SPA dashboard. Reads the dead OAuth store. `[FACT]` |
| `cron` | **Removed.** Cron tool retired; orphaned `jobs.json`. `[FACT]` |
| `migrate` | **Removed.** OpenClaw-only. `[FACT]` |
| standalone `model`, `skills` | **Removed** → onboarding / SPA. `[INFERENCE]` |
| `gateway` alias | **KEPT.** One line; back-stops Docker entrypoints, CI, the CI worker, the desktop launcher, and `install.sh`. `[FACT: MA-2]` |

**Constraint #7 deliverable:** the change MUST update every internal caller in the same
PR — `docker/Dockerfile:83`, `Dockerfile.heavy:94`, `Dockerfile.full:44`,
`docker/entrypoint.sh:11`, `.github/workflows/cross-platform.yml:83`, `pr.yml:616`,
`deploy/ci-worker/runci.sh:189`, `cmd/omnipus-launcher-tui/ui/gateway.go:88-90`,
`scripts/install.sh:180`, and `docs/using-omnipus-cli.md` — plus a CI grep-guard that the
removed verbs no longer appear in our own infra. `[FACT: MA-2]`

```
CONFIDENCE: High
  Basis         : Each disposition cites a dead source, retired feature, or duplication; callers enumerated.
  Evidence      : status/helpers.go:45, auth/helpers.go:53; the caller list above.
  Missing       : Nothing material.
  Would improve : n/a.
```

### D7 — Local-auth (token file) + daemon lifecycle
The CLI authenticates to its gateway with a **CLI-owned `0600` token file**
(`$OMNIPUS_HOME/cli.token`), minted by `onboard`/`start` as a named RBAC principal
(plaintext to the file, bcrypt hash into `Gateway.Users`), read by the CLI and sent as
the bearer. This reuses the existing `VerifyToken` path, needs no `RequireNotBypass`
exception, and never trusts "localhost = admin." **Loopback-trust is explicitly
rejected.** The CLI-started gateway is persistent; `omnipus stop` (PID file, reconciled
with the launcher's existing PID handling) stops it; cross-platform detach is required
(Constraint #4). `[FACT: CR-1]`

```
CONFIDENCE: Medium
  Basis         : Direction committed (token file) and reuses an existing auth path; details remain.
  Evidence      : checkBearerAuth tree (auth.go:117), VerifyToken, onboard token_hash, RequireNotBypass guard.
  Missing       : Principal/role design; cross-platform detach + stop; token-scoping tests (NFR-2).
  Would improve : Security-lead review of the handshake in /plan-spec (gated).
```

## 7. Risks and Caveats

- **R1 — Local-auth one-way door.** Mitigated by committing to the 0600 token file and
  rejecting loopback-trust (FR-13/D7). Residual: token at rest (same class as
  `master.key`; 0600-enforced, never logged) and token scoping (NFR-2). Security-lead gate.
- **R2 — Back-compat break.** Hard cut (operator decision). Mitigated: `gateway` alias
  kept; all internal callers (Docker/CI/worker/launcher/`install.sh`/docs) updated in the
  same PR; a CI grep-guard blocks reintroduction. Residual: external user scripts calling
  a removed verb get cobra "unknown command" with no migration hint — accepted.
- **R3 — Orphaned daemon.** Mitigated by persistent reuse + `omnipus stop`; residual
  cross-platform detach/stale-PID handling (G2).
- **R4 — Product-story tension.** The execute path now depends on a gateway being up
  (auto-brought-up). Accepted for drift-immunity; bootstrap stays fully offline.
- **R5 — First-call latency / spawn race / headless unlock.** Mitigated by a measured
  health-poll budget, a `gateway.lock` spawn lock, and a non-interactive-unlock
  precondition with a guided failure (G4/G5/MA-5).

## 8. Confidence Assessment

| Decision | Confidence |
|---|---|
| D1 — thin one-shot runner | High |
| D2 — require + auto-bring-up via `start`, WS-only | Medium-High |
| D3 — mandatory positional grammar, no REPL | High |
| D4 — chat-target only; worker out of scope (v-next noted) | High |
| D5 — keep bootstrap commands + UX + token mint | High |
| D6 — removals/deprecation, keep `gateway` alias, caller updates | High |
| D7 — token-file auth + daemon lifecycle | Medium |

Overall direction: **High**. The remaining soft spots — D7 mechanism, and the bounded
G2/G4/G5 items — are mechanism, not direction, and are resolved in `/plan-spec` with a
security-lead gate on the token handshake.

### Out of scope (explicit)
- A per-agent worker/specialist task-execution endpoint (isolated/memory-free run) — v-next (D4).
- Remote / multi-tenant CLI beyond a single `--url` + token over TLS.
- Session continuity for one-shots (`-s <name>`).
- Ephemeral per-task gateway (rejected — channel flapping + boot cost; §5).

## 9. Validation / Next Steps

- **Spec the chosen option:** `/plan-spec docs/internal/architecture/ADR-024-cli-minimization.md`
  — produce the implementation-ready spec (BDD/TDD), resolving G2/G4/G5 and the FR-12/FR-13
  behaviors, with **security-lead owning the FR-13 token handshake** (gated).
- **Spikes to set real numbers/behaviors before committing:** (a) token handshake design +
  security review; (b) `omnipus stop`/detach portability incl. Windows; (c) auto-start health-poll
  latency + `gateway.lock` race behavior; (d) the FR-12 approval-frame flow against a live `ask`-policy agent.
- **Regression guards:** NFR-1 (CLI never embeds the engine when a gateway is reachable);
  the D6 CI grep-guard for removed verbs in our own infra.
- **Then:** `/taskify` → wave implementation → `/grill-code`.

> The grill-spec verdict (PASS-WITH-CONDITIONS) is satisfied by this revision: MA-1 (WS-only),
> CR-2 (FR-12), CR-1 (FR-13 token file), MA-2/MA-3 (alias kept + stubs + caller updates), and
> MA-4/MA-5 (lifecycle/preconditions) are folded in. Review: `ADR-024-cli-minimization-review.md`.
