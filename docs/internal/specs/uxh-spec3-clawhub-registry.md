# Spec-3 — ClawHub Multi-Provider Skill Registry (Browse / Install)

**Status:** Draft (ready for `/grill-spec`) · **Date:** 2026-06-03
**Source:** `ADR-018` (D-C1) · **Independent** of Spec-1/Spec-2.
**Surfaces:** Go gateway (REST endpoints — currently 501 stubs) + SPA (SkillBrowser + a registry-provider management surface) + **contract**.

> `[FACT, grill-verified]` The backend already exists: `pkg/skills` `SkillRegistry` interface + `RegistryManager.AddRegistry/SearchAll` + `ClawHubRegistry` → `clawhub.ai/api/v1/{search,skills,download}` (live, HTTP 200, shape matches `clawhubSearchResponse`; `version` is `null` in live data). Only the SPA-facing REST endpoints are stubbed (`rest.go:2386,2399` → 501) and the browse UI is a "not yet available" placeholder.

---

## 1. Scope
Wire the SPA to the existing multi-provider registry and **surface the multi-provider model in the API and UI** (owner: "must be represented in api and more important ui"). **In scope:** REST search/list/install bridging to `RegistryManager`; the SkillBrowser browse/search/install UI; a registry-provider list (ClawHub first, designed for adding providers); SkillTrust hash verification + record source+version (null-tolerant). **Out of scope:** redesigning the `SkillRegistry`/`RegistryManager` layer (it stays); the tools work (Spec-1/2). **Reuse, don't rebuild.**

## 2. Existing Codebase Context (delta)
| Symbol | Role | Context |
|---|---|---|
| `SkillRegistry` interface (`registry.go:51`: Name/Search/GetSkillMeta/DownloadAndInstall) | reuse | the multi-provider contract |
| `RegistryManager` (`AddRegistry`, `SearchAll`) | reuse + expose | already aggregates providers |
| `ClawHubRegistry` (`clawhub_registry.go` → clawhub.ai/api/v1) | reuse | concrete provider; tolerate null `version` |
| `installSkill` / search REST handlers (`rest.go:2386,2399`) | **implement** | currently 501 stubs |
| `config.Tools.Skills` + `ClawHubRegistryConfig{BaseURL}` (`defaults.go:442`) | extend | provider registry config (list, base URLs) |
| SkillTrust hash model (`SkillTrustSection`, installer hash verify) | reuse | verification + persist source+version |
| `SkillBrowser.tsx` ("ClawHub registry not yet available") | **replace** | real browse/search/install + provider awareness |
| contract: new `SkillRegistryEntry`, `SkillSearchResult`, `RegistryProvider` schemas | **add** | API representation of providers + results |

**Impact:** new REST endpoints = MEDIUM (network egress to clawhub.ai — must honor SSRF/proxy + timeouts + graceful-unreachable); SkillBrowser rewrite = MEDIUM; contract additions = MEDIUM (regen). **No change to the agent-facing `find_skills` tool** (already works).

## 3. User Stories

### US-1 — Browse & search skills from registries (P1) `[D-C1]`
- **AC1. Given** ClawHub is reachable, **When** the operator opens Browse Skills and searches, **Then** results from the configured registry providers render (slug, name, summary, source provider, version-or-"latest").
- **AC2. Given** ClawHub is unreachable, **Then** the UI shows a clear "registry unavailable, install from file still works" state — never a hang.

### US-2 — Install a skill with trust verification (P1) `[D-C1]`
- **AC1. Given** the operator installs a browsed skill, **Then** the backend downloads via `RegistryManager`, **verifies the SkillTrust hash**, and records the skill's **source registry + version** (tolerating null version → "unknown").
- **AC2. Given** the trust level is "Block unverified" and the hash can't be verified, **Then** install is blocked with a clear message (reuse the existing SkillTrust model).

### US-3 — Multi-provider visible & manageable (P1) `[owner: API + UI]`
- **AC1. Given** the registries config, **When** the operator opens registry management, **Then** the provider list (ClawHub + any added) is shown with name/base-URL/enabled, and the browse results indicate which provider each came from.
- **AC2.** The API exposes the provider list (`GET /api/v1/skills/registries`) so the model is represented, not backend-only.

**Edge cases:** search timeout (graceful, partial results from healthy providers — `SearchAll` already degrades); a provider returning malformed JSON (skip + log, don't crash); duplicate slugs across providers (disambiguate by provider); a skill already installed (offer update/skip); SSRF — registry base URLs validated (https; internal-address caution consistent with the MCP-URL model).

## 4. Non-Behaviors
- MUST NOT redesign `SkillRegistry`/`RegistryManager` (reuse).
- MUST NOT bypass SkillTrust hash verification on install.
- MUST NOT block the UI on a slow/unreachable registry (timeouts + graceful degrade).
- MUST NOT send registry traffic outside the SSRF/proxy policy.

## 5. BDD (representative)
```
Scenario: Search returns provider-tagged results (Happy) — Traces to: US-1/AC1, US-3/AC1
  Given ClawHub is configured and reachable
  When the operator searches "research"
  Then results render with their source provider and version

Scenario: Registry unreachable degrades gracefully (Error) — Traces to: US-1/AC2
  Given ClawHub is unreachable
  When the operator opens Browse Skills
  Then a "registry unavailable" state shows and install-from-file remains available

Scenario: Install verifies hash + records source/version (Happy/Security) — Traces to: US-2/AC1
  Given the operator installs a browsed skill with a null upstream version
  When install completes
  Then the hash was verified and source=ClawHub, version="unknown" are recorded
```

## 6. TDD Plan
| Order | Test | Level | Traces |
|---|---|---|---|
| 1 | `clawhub_registry_test.go` (extend) — null-version tolerated; malformed-JSON skip | Unit (go) | US-2,edge |
| 2 | `skills_rest_test.go` — search/list/install endpoints bridge to a mock `RegistryManager`; 501 gone | Integration (go) | US-1,US-2 |
| 3 | `skills_install_trust_test.go` — hash verify + source/version recorded; Block-unverified blocks | Integration (go) | US-2 |
| 4 | `registries_endpoint_test.go` — `GET /skills/registries` returns provider list | Integration (go) | US-3 |
| 5 | `contract_test.go` — `SkillSearchResult`/`RegistryProvider` generate Go+TS; idempotent | Integration | US-1/US-3 |
| 6 | `SkillBrowser.test.tsx` — search renders results; unreachable→graceful; provider tag | Component | US-1,US-3 |
| 7 | `RegistryProviders.test.tsx` — provider list management UI | Component | US-3 |
| **E2E-1** | `tests/e2e/skills-browse.spec.ts` — open Browse, search (mocked registry route), install flow, trust dialog | E2E | US-1,US-2 |
| **E2E-2** | `tests/e2e/skills-registry-unavailable.spec.ts` — registry 5xx → graceful state, file-install still works | E2E | US-1/AC2 |

**Live verification (holdout, not CI):** a manual/integration check against the real `clawhub.ai/api/v1/search` confirming the shape still matches (network-dependent — not in the deterministic suite).

**Regression:** the agent-facing `find_skills` tool path unchanged (existing tests green); SkillTrust model reused unchanged.

## 7. FR / SC
- **FR-301** The REST search/list/install endpoints MUST bridge to `RegistryManager` (no 501).
- **FR-302** Install MUST verify the SkillTrust hash and record source registry + version (null→"unknown").
- **FR-303** The API MUST expose the registry-provider list; the SPA MUST surface browse + provider awareness + a provider-management view.
- **FR-304** An unreachable/slow registry MUST degrade gracefully (timeout, partial results, file-install fallback) and honor SSRF/proxy.
- **SC-301** Browse + install works end-to-end against a mocked registry (E2E-1) and the 501 stubs are gone.
- **SC-302** Unreachable registry shows the graceful state (E2E-2), never hangs.
- **SC-303** `GET /api/v1/skills/registries` returns ClawHub; results are provider-tagged.
- **SC-304** verify-contracts idempotent; all gates green; live clawhub.ai shape confirmed (holdout).

## 8. Traceability
| FR | US | Tests |
|---|---|---|
| FR-301 | US-1,US-2 | T2,E2E-1 |
| FR-302 | US-2 | T1,T3,E2E-1 |
| FR-303 | US-3 | T4,T6,T7,E2E-1 |
| FR-304 | US-1/AC2 | T2,E2E-2 |

## 9. Multi-agent parallel delivery
**Wave 0 — contract (blocks both sides):**
- `backend-lead`: add `SkillSearchResult`/`RegistryProvider` schemas to `contracts/`, regen (T5).

**Wave 1 — parallel:**
- **Team BE** `backend-lead` (+ `security-lead` for trust/SSRF): implement search/list/install + `/skills/registries` endpoints bridging to `RegistryManager`; null-version + graceful-degrade + SkillTrust (T1-T4). Files: `pkg/gateway/rest.go` (the 501 stubs), `pkg/skills/*`.
- **Team FE** `frontend-lead`: rewrite `SkillBrowser` (browse/search/install + provider tags) + a `RegistryProviders` management view (T6,T7). Files: `skills/SkillBrowser.tsx`, new `skills/RegistryProviders.tsx`, `SkillsScreen`.
- **Team QA** `qa-lead`: e2e (E2E-1,2) after integrate; the live-shape holdout check.

**7-reviewer gate** after each feature + on the whole Spec-3 diff (6 toolkit + `/grill-code`); `security-lead` reviews the install/trust/SSRF path.

**Parallelism:** BE and FE are file-disjoint and build against the Wave-0 contract concurrently; integrate via the contract.

## 10. Holdout
- H1: operator searches a real term, sees ClawHub results, installs one, and it appears in their skills.
- H2 (error): with the network blocked, Browse shows "unavailable" and file-install still works.
- H3 (security): installing a tampered/hash-mismatched skill under "Block unverified" is blocked.
- H4 (multi-provider): the registry-management view lists ClawHub and is structured to add another provider.
- H5 (live): a manual hit to clawhub.ai/api/v1/search still returns the expected shape.

## 11. Ambiguities (for `/grill-spec`)
| Item | Assumption | Resolve |
|---|---|---|
| Provider-management write ops (add/remove registries in UI) | read+enable/disable now; add-provider = config-driven first | confirm UX depth |
| Install endpoint request shape vs current stub (`{name}` vs `{slug,provider,version}`) | use slug+provider+version (matches RegistryManager) — contract-first | confirm |
| clawhub.ai auth/rate-limits | unauthenticated search per current client | verify against live API |
| Search result paging | first N (limit param exists in `SearchAll`) | confirm UX |

**Handoff:** `/grill-spec docs/internal/specs/uxh-spec3-clawhub-registry.md` → `/taskify`.
