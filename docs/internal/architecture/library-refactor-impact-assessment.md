# Library Refactor — Impact Assessment

**Status:** Historical assessment for architectural review
**Base commit:** `da790c5` (origin/main at time of writing)
**Prompted by:** an April 2026 multi-edition strategy proposal (Community + Desktop + Enterprise). **That multi-edition framing is no longer the plan** — Omnipus today ships as a single open-source Go binary. The "Desktop" and "EE" naming below refers to hypothetical second consumers that motivated the original library-boundary analysis. The code-quality findings (CLA, `os.Exit` in logger, OpenAPI as source of truth, etc.) are independently valid and remain useful regardless of whether a second consumer ever appears.

**Purpose:** Ground-truth the strategy doc's claims against the current codebase and size the real work.

> This is **not** a decision record. It is a sizing document. Line references are against `origin/main@da790c5`.

---

## 1. Executive summary

Three things the reader needs up front:

1. **The library refactor is smaller than the strategy doc implies.** Current library-readiness is ~**6–7/10**, not the implied "0/10" of a greenfield rewrite. The channel gateway pattern is already interface-driven, DI-shaped, and genuinely pluggable. The HTTP API is already a uniform `/api/v1/...` surface with no UI-only privileges. Config is already passed by value, not sourced from globals inside `pkg/`.

2. **The real work is concentrated in three places, not spread across the codebase.** (a) `pkg/gateway/gateway.go` — a 1,186-line file that fuses library logic with application lifecycle (signal handling, banner printing, file-logger setup). (b) `pkg/logger/logger.go:274` — an unconditional `os.Exit(1)` on `FATAL` level that no library may contain. (c) The absence of swappable interfaces for `auth`, `policy`, `audit`, `credentials`, `storage` — these packages exist but expose concrete types, not contracts.

3. **Revised effort estimate: 4–6 weeks for the backend, not 7–10.** The strategy doc's estimate is defensible for a generic refactor; it overshoots for this specific codebase because the hard architectural decisions (channel plugin model, uniform API, DI of config/bus/provider) have already been made. See §6.

---

## 2. What the strategy doc gets right

These claims are accurate and should proceed to sign-off unchanged:

| Strategy claim | Verdict | Evidence |
|---|---|---|
| Community code cannot contain `if enterprise` / `if desktop` conditionals (§3.2) | **Correct and currently upheld** | Zero hits for edition conditionals in `pkg/`. |
| Extension interfaces must be versioned as first-class API (§4.4) | **Correct, and absent today** | No `pkg/ext` directory exists. No documented interface stability policy. |
| OpenAPI as source of truth (§4.5) | **Correct, and absent today** | No `openapi.yaml` / `swagger.json` anywhere in repo. Contract exists only implicitly, encoded in `src/lib/api.ts` (1,075 LOC) and handler-by-handler in `pkg/gateway/rest*.go`. |
| CLA on day one (§6.4) | **Correct and urgent** | No CLA mechanism currently in the repo. The moment any contribution lands in Community and is later relicensed or rebundled, this is a live legal exposure. Cheap to fix; blocks every downstream decision. |
| "No private repo until the library boundary is validated" (§7.2) | **Correct** | The single highest-leverage rule in the whole doc. Worth enforcing. |
| Contract tests in CI (§4.5) | **Correct, and absent today** | No contract-test job in CI; API/UI alignment is manual. |

## 3. What the strategy doc overstates

Claims that are directionally right but oversized for the actual code:

### 3.1 "Today's shape is application-shaped" (§4.1)

**Partially wrong.** The doc describes `main()` wiring hardcoded dependencies and the runtime "knowing it is OmniPus the app." Reality on `main@da790c5`:

- `cmd/omnipus/main.go` is **63 lines**, a thin Cobra entrypoint that delegates to `cmd/omnipus/internal/gateway/command.go`.
- `pkg/config/Config` is threaded through every subsystem via explicit arguments, not read from globals.
- `pkg/bus/MessageBus`, `pkg/providers`, `pkg/agent.AgentLoop` are all constructor-injected.
- The *actual* application-shaped code lives in `pkg/gateway/gateway.go` — not in `main.go` as the doc describes. The refactor target is different from what the doc names.

### 3.2 "Library-shaped requires creating pkg/core.Runtime from scratch" (§4.2)

**Wrong framing.** There is already a de-facto runtime entry point: `gateway.Run(debug, homePath, configPath, allowEmpty bool) error` at `pkg/gateway/gateway.go:231`. The work is not *creation*; it is **separation** — extracting application lifecycle out of this function so the residue is the library runtime.

### 3.3 "7–10 weeks total" (§7.4)

**Overstated for this codebase.** That budget assumes the channel plugin pattern, the LLM provider abstraction, the message bus, the session/state separation, and the single-entrypoint HTTP surface all need to be built. They already exist. Revised sizing in §6.

### 3.4 "Three-layer UI monorepo requires extraction" (§5)

**Partially done.** `pnpm-workspace.yaml` + `packages/ui` already exist. The split between top-level `src/` (the Community web app) and `packages/ui` is the beginning of the shell-vs-package model. What is missing is the middle layer (`@omnipus/client`, `@omnipus/hooks`, `@omnipus/feature-*`) and a proper DI pattern for the API client. See §5.

---

## 4. Current-state ground truth

Verified on `main@da790c5`. Line numbers are stable unless noted.

### 4.1 Backend prohibited-list audit (strategy doc §4.3)

| Item | Present in `pkg/`? | Concrete evidence | Severity |
|---|---|---|---|
| `os.Exit` | **Yes, 2 production call sites** | `pkg/logger/logger.go:274` (FATAL level exits process); `pkg/logger/panic.go:33` (panic-trap exits after flush) | **Blocker.** Any subsystem logging at FATAL kills the host process. |
| `log.Fatal` | No | — | Clean. |
| `panic()` on recoverable errors | **Yes, 4 production call sites** of concern | `pkg/gateway/gateway.go:282` (file-logger setup); `pkg/policy/auditor.go:39` (nil Evaluator); `pkg/agent/audit_bridge.go:26` (nil Logger); `pkg/providers/fallback.go:43` (nil cooldown). Plus 6 "BUG: invalid hardcoded X" panics that are acceptable invariant checks. | **Medium.** Nil-constructor panics should become returned errors. |
| `init()` with side effects | **25 files** | 15 channel `init.go` files register factories; `pkg/channels/base.go:26` seeds a random prefix; `pkg/logger/logger.go` initializes the global logger; `pkg/coreagent/core.go` compiles agent prompts. | **Medium.** The channel registry is acceptable *if documented as an opt-in import boundary*; the logger singleton is not. |
| Global state / singletons | **Yes, confirmed** | `pkg/channels/registry.go:17–19` (global factories map); `pkg/logger/logger.go` (global `zerolog.Logger`, `fileLogger` under `sync.Once`); `pkg/channels/base.go:21–24` (global uniqueID counter). | **Medium.** Prevents two runtimes in one process (tests, multi-tenancy). |
| Hardcoded file paths | **No absolute hardcoded paths** | `pkg/gateway/gateway.go:70–72` defines relative names (`"logs"`, `"gateway_panic.log"`, `"gateway.log"`) joined to a `homePath` argument. No `~/.omnipus/` literals in `pkg/`. | **Clean.** |
| `os.Getenv` outside config helpers | **~30 occurrences, mostly OK** | Main concentration: `pkg/auth/oauth.go` reads OAuth client ID/secret from env. Should become `Config` fields. | **Low.** |
| Flag parsing in `pkg/` | No | All flag parsing is in `cmd/omnipus/`. | **Clean.** |
| Signal handling in `pkg/` | **Yes, 1 production site** | `pkg/gateway/gateway.go:245` (`signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)`) | **Blocker.** A library cannot own the host's signal strategy. |
| Banner printing | **Yes** | Embedded in `gateway.Run()` status prints. | **Low.** Cosmetic; fix alongside signal extraction. |

**Two hard blockers. Three medium-severity items. One low-severity.** The prohibited-list checklist is ~80% already satisfied.

### 4.2 Channel gateway — the founder's "already library-shaped" claim

**Substantively correct.** The channel subsystem is the strongest evidence that the architecture is already plugin-oriented:

- `pkg/channels/base.go:47–56` defines the `Channel` interface with 8 required methods and a further 7 *optional* capability interfaces in `pkg/channels/interfaces.go` (TypingCapable, MessageEditor, StreamingCapable, etc.). This is the same pattern `pkg/ext` proposes.
- `pkg/channels/registry.go` defines `ChannelFactory func(*config.Config, credentials.SecretBundle, *bus.MessageBus) (Channel, error)` — secrets arrive as a bundle, **not** from `os.Getenv`. This already solves the "no env var coupling" problem for the largest extension surface.
- 15 channels (`dingtalk`, `discord`, `feishu`, `googlechat`, `irc`, `line`, `maixcam`, `onebot`, `qq`, `slack`, `telegram`, `wecom`, `weixin`, `whatsapp`, `whatsapp_native`) are registered via blank-imports at `pkg/gateway/gateway.go:31–45`. Dropping one = remove its import line. Adding a private EE channel = import it from the EE `cmd/`.

**The one coupling issue:** because blank-imports live inside `pkg/gateway/gateway.go`, a third-party channel cannot be registered without the consumer modifying the library or manually calling `channels.RegisterFactory()` before `gateway.Run()`. This is fixable by moving the blank-imports out of `pkg/` and into `cmd/omnipus/`.

### 4.3 UI as "just another client" — the founder's second claim

**Correct.** Verified at `src/lib/api.ts:1–28`: the UI authenticates via Bearer token (same token path as any other HTTP client), calls `/api/v1/...` via `fetch`, and has no privileged "UI-only" endpoints. The same auth middleware that protects external calls protects UI calls. The UI is `go:embed`-ded (`pkg/gateway/embed.go`) as a convenience — the Go server does not *require* it; it is served alongside the API.

**One nuance worth flagging:** `src/lib/api.ts` is 1,075 LOC of hand-maintained fetch wrappers. This is the single biggest *latent* risk if a second UI consumer ever appears — any drift between Go handlers and this file is silent. Generating it from OpenAPI (§4.5 of the strategy doc) is not optional once a second consumer exists.

### 4.4 Gate-level coupling hotspots

In order of extraction difficulty:

1. **`pkg/gateway/gateway.go` (1,186 LOC).** The monolith. `Run()` (line 231) bootstraps credentials, config, provider, agent loop, HTTP server, reload loop, signal handling, banner, and file-logger. This is the *actual* refactor target — not `main.go`, not `pkg/core` (which does not exist).
2. **`pkg/agent.AgentLoop`.** A large struct with many injected dependencies. Structurally DI-correct, but the number of fields means any change to a dependency's interface ripples through construction sites. Risk is refactor friction, not design failure.
3. **`pkg/logger`.** Global singleton with `os.Exit` on FATAL. Must be either (a) made injectable as an `ext.Logger` interface, or (b) kept global but stripped of `os.Exit`. Option (b) is cheaper.
4. **`pkg/auth`, `pkg/policy`, `pkg/audit`, `pkg/credentials`.** Each is a concrete package today. Extracting an interface is mechanical; the risk is that EE's needs (OIDC/SAML/SCIM, OPA, Splunk/SIEM, Vault/KMS) expose interface-design errors we cannot see yet. Defer full interface design until a paying EE partner's requirements are concrete (strategy doc §7.3 mitigation — correct).
5. **Channel registry globalness.** Moving `factories` from a package-level `map` to a `*Registry` owned by the runtime is a 1-day refactor but touches 15 channel packages. Low risk, high surface area.

---

## 5. Gap analysis vs. the proposed `pkg/ext` interfaces

Strategy doc §4.4 proposes nine interfaces. Current state, per interface:

| Interface | Today | Extraction effort | Notes |
|---|---|---|---|
| `ConnectorRegistry` | **Done.** `pkg/channels` already matches this shape. | 2 days | Rename + document + move blank-imports to `cmd/`. |
| `CredentialBroker` | **Partial.** `pkg/credentials.SecretBundle` already decouples channels from env. No broker interface for rotation/external vaults. | 3 days | Interface exists implicitly; formalize and add KMS/Vault seam. |
| `Telemetry` | **Absent.** `pkg/health` provides a health server, not a metrics pipeline. | 2 days | Minimal surface (Counter/Gauge/Histogram/TraceSpan). Community defaults to stdout/Prometheus. |
| `AuditSink` | **Concrete.** `pkg/audit` writes JSONL. No interface. | 3 days | Interface is small; `audit.Logger` already looks sink-shaped. Straightforward. |
| `Storage` | **Absent.** Session/task/state persistence goes directly to filesystem via `pkg/session`, `pkg/taskstore`, `pkg/state`. | 5–8 days | Biggest hidden effort. Interface must cover multiple entity kinds without becoming leaky. Design carefully. |
| `PolicyEngine` | **Concrete.** `pkg/policy` is a full implementation. | 3 days | Interface design is the hard part — OPA-style policy is much richer than allowlist. Defer rich design until EE is real. |
| `AuthProvider` | **Concrete.** `pkg/auth` handles local + OAuth today. | 3 days | SSO/SAML/SCIM is EE-only work; Community interface is small. |
| `WorkflowEngine` | **Absent.** No workflow abstraction today. | Defer | Strategy doc flags this as EE-durable-workflow territory. Don't build until Temporal/Inngest-equivalent is actually required by EE buyer. |
| `TenantResolver` | **Absent.** Single-tenant everywhere. | Defer | Same logic. |

**Realistic initial `pkg/ext` scope:** `ConnectorRegistry`, `CredentialBroker`, `Telemetry`, `AuditSink`, `AuthProvider`, `PolicyEngine`, `Storage`. Ship stubs/no-ops for `WorkflowEngine` and `TenantResolver`; implement them only when a paying partner's requirements exist. This matches strategy doc §7.3.

---

## 6. Revised effort estimate

The strategy doc's Phase 2+3+4 estimate is **7–9 weeks**. Based on §4, the revised estimate:

| Phase | Strategy doc | This assessment | Delta rationale |
|---|---|---|---|
| 1. API contract (OpenAPI + contract tests + CLA + generated TS client) | 1–2 wk | **1.5–2 wk** | No delta. Genuinely absent work. |
| 2a. Extract `pkg/gateway/gateway.go` → `pkg/core.Runtime` + thin cmd | — | **1.5 wk** | The real Phase 2 entry. Split signal handling, banner, file-logger setup out. |
| 2b. Remove `os.Exit` from logger; convert nil-constructor panics to errors | — | **2–3 days** | Mechanical. |
| 2c. Move channel blank-imports from `pkg/gateway` to `cmd/omnipus` | — | **1 day** | Mechanical but touches 15 packages. |
| 2d. Formalize 5 in-scope `pkg/ext` interfaces with defaults wired into the new `core.Runtime` (Connector, Credential, Telemetry, Audit, Auth — defer Policy/Storage until needed) | — | **1–1.5 wk** | Less than the doc implies because four of these already exist as concrete packages to wrap. |
| 3. UI library refactor | 3–4 wk | **2–3 wk** | Less than the doc implies because `packages/ui` + pnpm workspace + `vite.lib.config.ts` already exist. Remaining: `@omnipus/client` (generated from OpenAPI), `@omnipus/hooks`, peel 1–2 features out as proof. |
| 4. Validate boundary with `cmd/omnipus-minimal` | 1 wk | **3–5 days** | Same. |
| 5. Private repos scaffolded | 1 wk | **3–5 days** | Same. |
| **Total** | **7–10 wk** | **~5–6 wk** | Concentrated in items that are genuinely absent; existing structure saves ~2–4 weeks. |

**One senior engineer, full-time, familiar with the codebase.** Add ~30% for code review cycles if the refactor lands via PRs with mandatory review (realistic).

---

## 7. Recommended sequencing (revised)

Strategy doc Phase 1–5 remain correct in shape. Two adjustments:

1. **Phase 1 should include the CLA and the "kill `os.Exit` in logger" chore.** Both are cheap, blocking, and do not require the big refactor. Do them first.

2. **Phase 2 should explicitly target `pkg/gateway/gateway.go` as the lift site**, not create `pkg/core` from scratch. The work is to extract the library runtime *out of* the existing gateway file, not build a new one.

Suggested branch/PR decomposition:

| PR | Scope | Risk |
|---|---|---|
| 1 | Add CLA bot, add OpenAPI skeleton + CI contract test stub, remove `os.Exit(1)` from `pkg/logger` | Low |
| 2 | Move channel blank-imports from `pkg/gateway/gateway.go` to `cmd/omnipus/main.go` | Low |
| 3 | Extract `pkg/core.Runtime` from `gateway.Run()`; leave signal handling, banner, file-logger setup in `cmd/omnipus` | Medium — touches every caller of `gateway.Run` |
| 4 | Introduce `pkg/ext` with `ConnectorRegistry`, `CredentialBroker`, `Telemetry`, `AuditSink`, `AuthProvider` interfaces + `pkg/ext/defaults` reference impls | Medium |
| 5 | Generate `src/lib/api.ts` from OpenAPI; delete handwritten version | Medium — regression surface |
| 6 | `cmd/omnipus-minimal` — 20-line proof-point consuming `pkg/core` | Low |
| 7 | UI: extract `@omnipus/client` + `@omnipus/hooks`; keep existing SPA working | Medium |
| 8 | UI: peel 1 feature (recommend `sessions`) into `@omnipus/feature-sessions` | Low — proves the pattern |

Each PR is independently mergeable to `main` without Desktop or EE existing. The validation gate in strategy doc §7.2 (no private repo until boundary green) is satisfied after PR 6.

---

## 8. Risks

### 8.1 Interface design errors hidden until EE is real (medium)

`PolicyEngine`, `Storage`, `WorkflowEngine`, `TenantResolver` are all interfaces we cannot design correctly without a concrete EE requirement. If we design them speculatively, we will either (a) over-build and pay interface-maintenance cost for years, or (b) guess wrong and break every consumer when EE arrives. **Mitigation:** keep these out of v1 of `pkg/ext`. Ship only the five we already have concrete shape for.

### 8.2 Hand-maintained `src/lib/api.ts` (medium, pre-existing)

1,075 LOC of handwritten fetch wrappers. Already a drift risk today; becomes acute the moment a second consumer (Desktop's Tauri shell, EE's hosted UI) depends on it. **Mitigation:** PR 5 generates this file from OpenAPI. Non-optional if three editions exist.

### 8.3 Logger globalness (low)

Making the logger injectable is architecturally clean but would touch every log call site. **Mitigation:** keep the global, remove only `os.Exit(1)`. Acceptable compromise; revisit if multi-runtime-per-process becomes a real need.

### 8.4 Channel blank-import coupling (low)

Moving imports out of `pkg/gateway` into `cmd/omnipus` is mechanical but will surface any channel that reaches back into gateway internals. Expect 1–2 such discoveries. **Mitigation:** fix as found; none should be structural.

### 8.5 "Refactor in flight, features keep shipping" (high, non-technical)

The strategy doc assumes a clean Phase 1→5 sequence. In practice, feature work on channels, security, and agents is active (visible in concurrent feature branches: `channel-signal`, `channel-google-chat`, `channel-teams`, `sprint-c-test-first`). A 5-week refactor that freezes these branches will be rejected; one that merges incrementally without freezing will take longer in wall-clock time.

**Mitigation:** design the PR sequence above to be independently mergeable and non-breaking. No single PR should block active feature work for more than ~2 days of rebase.

---

## 9. Recommendation

### 9.1 To the architect

Sign off on strategy doc §9 items **1, 2, 3, 4, 5, 6** — all six. They are correct in direction. However, adjust:

- **§9.1 (three repos, dep-not-fork):** Sign off, but defer creation of the two private repos until after PR 6 lands. Strategy doc §7.2 already says this — enforce it.
- **§9.2 (`pkg/core` + `pkg/ext`):** Sign off on the pattern, but scope initial `pkg/ext` to five interfaces, not nine. Defer `PolicyEngine` rich features, `Storage`, `WorkflowEngine`, `TenantResolver` until EE requirements are concrete.
- **§9.4 (OpenAPI first-class):** Sign off, and **prioritize**. The handwritten `src/lib/api.ts` is the largest hidden debt in the current codebase relative to the three-edition plan.
- **§9.5 (CLA day one):** Sign off, and do it **this week**. It is the cheapest, highest-leverage item in the entire strategy.
- **§9.6 (no private repo until boundary validated):** Sign off. Single most important rule in the document.

### 9.2 To Daniel

The strategy doc is architecturally sound but oversized for the codebase you actually have. Your instinct that "much of this is already done" is correct:

- The channel gateway is already `pkg/ext`-shaped in everything but name.
- The HTTP API is already uniform and unprivileged.
- `packages/ui` + pnpm workspace already exists.
- Config is already DI'd through the stack.

**What is genuinely missing** and worth starting now, regardless of whether Desktop/EE ever ship:

1. A CLA (§9.5). 30 minutes of work. Unblocks every future licensing question.
2. An OpenAPI spec, even imperfect. Even if the backend stays the sole consumer, it ends the drift risk on `src/lib/api.ts`.
3. Removal of `os.Exit(1)` from `pkg/logger/logger.go:274` and the nil-constructor panics. These are library-hygiene improvements that are cheap and valuable regardless of edition plans.

**What to defer** until a paying EE design partner commits: everything in §4 of the strategy doc beyond the five in-scope `pkg/ext` interfaces. Don't build `TenantResolver` for a product you haven't sold.

**The actual go/no-go question** is not "library refactor yes or no." It is: **do we have signal that Desktop or EE will produce revenue within ~2 quarters of the refactor landing?** If yes, the 5–6 week investment pays back. If no, do only the three no-regret items above and revisit when signal changes.

---

*End of assessment.*
