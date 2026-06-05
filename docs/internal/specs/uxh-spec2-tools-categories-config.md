# Spec-2 — Tool Purpose-Categories, `dangerous` Flag, Graduated Defaults & Per-Tool Config

**Status:** Draft (ready for `/grill-spec`) · **Date:** 2026-06-03
**Source:** `ADR-018` (D-A3/A4/A5/A6) · **Depends on:** Spec-1 (the central metadata registry must exist first). **Independent of** Spec-1's ship date for everything except the registry it extends.
**Surfaces:** Go registry/tools + SPA + **contract** (`contracts/openapi.yaml`). Contract-first mandatory (new tool-metadata + config-schema fields).

> Requirements ratified in ADR-018 (owner-confirmed graduated defaults). This is the largest greenfield piece (the per-tool config datamodel) — flagged for a dedicated grill pass.

---

## 1. Scope
Make tool metadata richer and add per-tool configuration. **In scope:** purpose categories (D-A4) + the `scope`-field decision (F-05), the `dangerous` flag with a CI completeness gate + truth table (D-A5/F-02/F-09), graduated default policy (D-A3/F-07), and the per-tool config datamodel (D-A6/F-04). **Out of scope:** the registry-metadata exposure itself (Spec-1), ClawHub (Spec-3). **Invariant:** per-agent execution instancing unchanged.

## 2. Existing Codebase Context (delta)
| Symbol | Role | Context |
|---|---|---|
| `Tool` interface, `BaseTool` (`base.go:22-92`), `ToolCategory` enum (`:25-38`) | extend | add `Dangerous() bool`; assign real `Category()` to general tools (default `CategoryCore`); split `system` finely |
| `Tool.Scope()` + `ToolRegistryEntry.yaml` `scope` enum | decide | keep as non-rendered internal discriminator |
| `RequiresAdminAsk()` (`sysagent/tools/admin_ask.go`, uniformly true for 41) | reference | NOT a danger signal; produce truth table |
| `compositor.go FilterToolsByPolicy` (deny>ask>allow, wildcards) | unchanged | graduated defaults feed the *default policy*, not the engine |
| Seeded defaults (`coreagent.SeedConfig`, custom-agent default `system.*:deny`) | replace | retire deny-seed; graduated dangerous defaults instead |
| Scattered configs: `config.Tools.Web.{Brave,Tavily,…}`, `ExecConfig`, `BrowserToolConfig`, `VoiceConfig`, `SearchCacheConfig` | migrate | unify under per-tool config model (adapter, not rewrite) |
| `ToolRegistryEntry.yaml` (`contracts/components/schemas/`) | extend | add `category` (purpose enum), `dangerous` (bool), `config_schema` (`ToolConfigField[]`), `config` (values) |
| `ToolPolicyEditor.tsx` (flattened in Spec-1) | extend | category grouping, `dangerous` badge, per-tool config drawer |
| `AgentConfig` (`pkg/config`) | extend | per-agent `tool_config` override map |

**Impact:** `Tool` interface change = **MEDIUM-HIGH** (all tool implementors get a default `Dangerous()` on `BaseTool`, but the CI gate forbids the silent default — every concrete tool must opt in). Contract change = MEDIUM (regen Go+TS). Per-tool config persistence = MEDIUM (new global + per-agent layer).

## 3. User Stories

### US-1 — Tools grouped by purpose, dangerous ones badged (P1) `[D-A4/A5]`
- **AC1.** Tools render under purpose categories (File & Code, Web & Search, Browser, Memory, Tasks, Agent Management, Configuration, Introspection, …); no `core`/`system` user-facing category; `scope` not rendered.
- **AC2.** A `dangerous` tool shows a danger badge in both Security (global) and per-agent editors.

### US-2 — Graduated safe defaults (P0 security) `[D-A3]`
- **AC1.** On a fresh install, the **high-danger cluster** (`exec`, `system.agent.*` create/delete, `config.set`, `mcp.add`, `channel.enable`/`configure`, `skill.install`, `web_serve`) defaults to **global Deny** (invisible to agents until allowed).
- **AC2.** **Medium-danger** (browser, read-only introspection) defaults to **Ask**.
- **AC3.** A new custom agent CANNOT invoke a high-danger tool by default.

### US-3 — `dangerous` tagging is provably complete (P0 security) `[D-A5/F-02]`
- **AC1.** A CI test FAILS if any registered builtin lacks an explicit `Dangerous()` decision (no silent `false`).
- **AC2.** The spec ships a `(category, dangerous, RequiresAdminAsk)` truth table covering all ~65 builtins.

### US-4 — Per-tool configuration with global default + per-agent override (P1) `[D-A6]`
- **AC1. Given** the operator opens a tool's config in Security, **Then** a typed form renders its `config_schema` (e.g. web_search → provider-select [Brave/Tavily/Perplexity/DuckDuckGo] + credential-ref + max_results).
- **AC2. Given** an agent overrides a config field, **Then** that agent uses the override; others use the global default.
- **AC3.** Credential fields use `credential-ref` (vault) — never plaintext in config.
- **AC4.** The existing `Brave/Tavily/Exec/Browser/Voice/SearchCache` configs are surfaced via this model (no behavior change to the tools themselves).

**Edge cases:** a tool with no `config_schema` (no config drawer); an override referencing a deleted credential (graceful); a `dangerous=true` tool an operator globally allows (allowed, badge stays); migrating a config that was env-injected (`OMNIPUS_TOOLS_WEB_TAVILY_*`) — still honored.

## 4. Non-Behaviors
- MUST NOT derive `dangerous` from `RequiresAdminAsk` (orthogonal).
- MUST NOT default-allow any tool lacking an explicit danger decision (the rail).
- MUST NOT use a free-form JSON-Schema passthrough for `config_schema` — only the closed `ToolConfigField` union (HC#8).
- MUST NOT change tool execution semantics — config is read by the existing tools via an adapter.

## 5. BDD (representative)
```
Scenario: High-danger tool denied by default (Happy/Security) — Traces to: US-2/AC1,AC3
  Given a fresh install and a newly created custom agent
  When the agent attempts to use exec
  Then exec is not present in the agent's LLM tools[] (global default Deny)

Scenario: dangerous-completeness CI gate (Error) — Traces to: US-3/AC1
  Given a new builtin tool registered without an explicit Dangerous() value
  When the registry completeness test runs
  Then the test FAILS

Scenario: web_search provider config (Happy) — Traces to: US-4/AC1,AC2
  Given web_search has a config_schema with provider-select + credential-ref + max_results
  When the operator sets the global provider to Tavily and agent A overrides to Brave
  Then agent A uses Brave and all other agents use Tavily
```

## 6. TDD Plan
| Order | Test | Level | Traces |
|---|---|---|---|
| 1 | `tool_metadata_test.go` — every builtin has explicit category + `Dangerous()`; **completeness gate fails on a silent default** | Unit (go) | US-3 |
| 2 | `dangerous_truth_table_test.go` — asserts the (category,dangerous,RequiresAdminAsk) table | Unit (go) | US-3/AC2 |
| 3 | `default_policy_test.go` — graduated defaults: high→deny, medium→ask, fresh custom agent can't exec | Unit (go) | US-2 |
| 4 | `tool_config_schema_test.go` — closed `ToolConfigField` union validates; global+override resolution | Unit (go) | US-4 |
| 5 | `tool_config_adapter_test.go` — Brave/Tavily/Exec/… read via the new model unchanged | Unit (go) | US-4/AC4 |
| 6 | `contract_test.go` — `ToolRegistryEntry` + `ToolConfigField` generate valid Go/TS; verify-contracts idempotent | Integration | US-1/US-4 |
| 7 | `ToolPolicyEditor.test.tsx` — purpose categories, dangerous badge, scope not rendered | Component | US-1 |
| 8 | `ToolConfigForm.test.tsx` — renders each field kind; credential-ref picks vault; override path | Component | US-4 |
| **E2E-1** | `tests/e2e/tool-categories.spec.ts` — real `/tools`: tools grouped by purpose, dangerous badge visible, no "system" category | E2E | US-1 |
| **E2E-2** | `tests/e2e/tool-config.spec.ts` — set web_search provider globally + override per-agent via the UI | E2E | US-4 |
| **E2E-3** | `tests/e2e/dangerous-default.spec.ts` — a fresh custom agent shows high-danger tools as Deny in its editor | E2E | US-2 |

**Regression:** `FilterToolsByPolicy` behavior unchanged (existing compositor tests stay green); the deny-seed removal must be covered by US-2 tests proving the graduated rail replaces it equivalently for the high cluster.

## 7. FR / SC
- **FR-201** Every builtin MUST declare an explicit `Dangerous()` value; CI MUST fail otherwise.
- **FR-202** High-danger tools MUST default to global Deny; medium-danger to Ask; on a fresh install a new custom agent MUST NOT see high-danger tools.
- **FR-203** Every builtin MUST carry a purpose `category`; `core`/`system` MUST NOT be user-facing; `scope` MUST NOT be rendered.
- **FR-204** A tool MAY declare a `config_schema` (closed `ToolConfigField` union); the registry MUST expose it; Security MUST render a global config form; agents MAY override per field; credentials MUST use `credential-ref`.
- **FR-205** The scattered tool configs MUST be served via the per-tool model without changing tool behavior.
- **SC-201** `dangerous_truth_table_test` + completeness gate pass; removing a tool's `Dangerous()` value fails CI.
- **SC-202** E2E-3: a fresh custom agent's editor shows `exec`=Deny.
- **SC-203** E2E-2: per-agent provider override takes effect (observable in the agent's tool config).
- **SC-204** verify-contracts idempotent with the new fields; all gates green.

## 8. Traceability
| FR | US | Tests |
|---|---|---|
| FR-201 | US-3 | T1,T2 |
| FR-202 | US-2 | T3,E2E-3 |
| FR-203 | US-1 | T7,E2E-1 |
| FR-204 | US-4 | T4,T6,T8,E2E-2 |
| FR-205 | US-4 | T5 |

## 9. Multi-agent parallel delivery
**Wave 0 — contract + metadata foundation (blocks the rest):**
- `backend-lead` + `security-lead`: extend `contracts/` (`ToolRegistryEntry`+`ToolConfigField`), regen; add `Dangerous()`/category to the `Tool` interface + the completeness gate (T1,T6). `security-lead` owns the dangerous truth table + graduated defaults (T2,T3) — this is the security-critical core.

**Wave 1 — parallel (after Wave 0 contract lands):**
- **Team BE-config** `backend-lead`: per-tool config datamodel + adapters for the 6 scattered configs (T4,T5). Files: `pkg/tools/*config*`, `pkg/config`.
- **Team FE-tools** `frontend-lead`: category grouping + dangerous badge + `ToolConfigForm` drawer in `ToolPolicyEditor`/SecuritySection/agent UI (T7,T8). Files: `shared/ToolPolicyEditor.tsx`, new `ToolConfigForm.tsx`.
- **Team QA** `qa-lead`: e2e suite (E2E-1..3) after the features integrate.

**7-reviewer gate** after each feature + on the whole Spec-2 diff before its PR (6 toolkit + `/grill-code`). **`security-lead` + `/grill-code` MUST sign off on the dangerous-rail completeness** as a release-blocking criterion.

## 10. Holdout
- H1: a fresh custom agent genuinely cannot run `exec` until an operator allows it.
- H2: operator sees a danger badge on `system.agent.delete` and `exec`.
- H3: operator sets the search provider to Tavily globally; one agent to Brave; both behave accordingly.
- H4 (error): removing a tool's danger tag breaks CI (developer-facing).
- H5 (edge): a tool with no config shows no config drawer.

## 11. Ambiguities (for `/grill-spec` — dedicate focus to D-A6)
| Item | Assumption | Resolve |
|---|---|---|
| Per-field global-only vs overridable matrix | exec timeout overridable; search provider overridable; voice global | enumerate per config in grill |
| Collapse `dangerous`/`RequiresAdminAsk`? | keep separate; forbid `dangerous=true,adminAsk=false` | grill decides |
| `ToolConfigField` exact union | the 8 kinds in §scope | confirm; add only if needed |
| Exact high-vs-medium cluster membership | per ADR D-A3 | owner/grill confirm |

**Handoff:** `/grill-spec docs/internal/specs/uxh-spec2-tools-categories-config.md` (focus the per-tool config datamodel + dangerous completeness) → `/taskify`.
