# Spec-6 — Skills + Self-Improvement, Plugin/Marketplace Shape, Integrations & Auth (v0.1.0 Foundation)

- **Spec:** 6 of 6 (v0.1.0 Foundation)
- **Source ADR:** [ADR-019](../architecture/ADR-019-v01-workspaces-foundation.md) — FR-9 (skills) + FR-10 (plugins/marketplaces shape) + FR-11 (protocols) + FR-12 (integrations + auth)
- **Status:** Draft → pending `/grill-spec` (GATE C)
- **Cross-spec (Phase 3.5):** ACP bidirectional-runner hook = **Spec-4**; A2A agent-reference + Card-projectable identity = **Spec-3/4**; skill tools use the renamed `system.workspace.*` namespace convention (Spec-1); the Workspace key (Spec-1) scopes skills.
- **Lessons pre-applied:** ground hard; contract-first; CI-authority; new deps = ADR; freeze persisted shapes; consent-gated writes (Spec-1); the skill tools are **stubs** (verified earlier this session).

## 1. Overview

**Skills + self-improvement:** wire the existing **stub** skill tools (`system.skill.{install,remove,search,list}`) to the real `pkg/skills` engine; **add `system.skill.create` + `system.skill.edit`** (the authoring/self-improvement verb — procedural memory); **`go:embed` + first-boot-seed** the default set (`summarize · skill-authoring · plan · daily-briefing`); per-agent **allowlist + progressive disclosure**; skill writes **consent-gated + versioned**. **Plugin/marketplace SHAPE (no installer):** the component-level-hybrid **bundle manifest shape**; the **marketplace-provider LIST** (`RegistryConfig` single ClawHub → list; ClawHub+GitHub first-class). **Protocols (hooks):** MCP is present; ACP/A2A hooks live in Specs 3/4 (cross-ref). **Integrations + auth:** the **Integrations provider-picker UI** (surface the existing `SearchProvider`/`Transcriber`) + a composer **mic**; **single-user/one-password**, sensitive settings = password re-type (`RequireNotBypass`); **Profile vs Settings**; **3-step onboarding** → auto-provision Mia·Assistant.

**In scope:** wiring the 4 stub skill tools to `pkg/skills`; `system.skill.create`/`edit`; `go:embed` default-set + first-boot seed; per-agent skill allowlist; consent-gated+versioned skill writes; the bundle-manifest **shape** + the marketplace-provider **list** (`RegistryConfig` single→list); the Integrations provider-picker UI + composer mic; single-user/one-password + Profile/Settings split + onboarding.
**Out of scope:** the plugin **installer** + Marketplaces UI (later); the ACP/A2A **protocol drivers** (Spec-4/later); TTS/image-gen (v0.2.0); the Dreamcatcher proposing skill edits (v0.2.0 behaviour — Spec-5's logs feed it).

## 2. Existing Codebase Context (grounded)

### Symbols Involved
| Symbol | Role | Context |
|---|---|---|
| `pkg/sysagent/tools/skill.go` — `system.skill.{install,remove,search,list}` (**STUBS**) | **wire** to engine | each returns a placeholder "stub" today (verified) |
| `pkg/skills/` — `SkillInstaller` (GitHub install, SSRF, hash-verify), `RegistryManager` (multi-registry fan-out), `ClawHubRegistry`, loader | the real engine | the stubs were never wired to it |
| **`system.skill.create` / `system.skill.edit`** | **NEW** tools | absent today — the authoring/self-improvement verb |
| skill seeding (`OMNIPUS_BUILTIN_SKILLS`, `<wd>/skills`) | **`go:embed` + first-boot seed** | today filesystem-seeded only (single-binary shippability gap) |
| `RegistryConfig` (`registry.go:66`, single `ClawHub` field) | **single → list** | `[]Marketplace{name,type,baseURL,key,enabled}` (same single→list as channels/tool-providers) |
| `SearchProvider` (`pkg/tools/web.go:91`) + `Transcriber` (`pkg/voice/transcriber.go`) | surface in Integrations UI | exist; no provider-picker UI today (LLM-only `ProvidersSection`) |
| `RequireNotBypass` (`pkg/gateway` middleware) + auth (`auth.go`, register-admin) | password re-type + onboarding | the sensitive-settings gate |
| `coreagent.SeedConfig` (Spec-3 Mia·Assistant) | onboarding auto-provision | Mia is the auto-provisioned Assistant |

### Impact Assessment
| Modified | Risk | Direct (d=1) | Indirect (d=2) |
|---|---|---|---|
| skill stubs → real + create/edit | **HIGH** | the 4 tools, the tool catalog, the skills engine | per-agent allowlist |
| `go:embed` default skills | MEDIUM | the build (embed dir), first-boot seed | shippability |
| `RegistryConfig` single→list | MEDIUM | config + the registry manager | marketplaces |
| skill writes (create/edit) | **HIGH** (security) | consent layer (Spec-1), versioning | self-modifying agent |
| Integrations UI | LOW | a new Settings surface | provider config |
| auth/Profile/onboarding | MEDIUM | auth tree, the SPA | single-user posture |

## 3. User Stories

**US-1 — Skill tools wired (P0).** 1. **Given** the 4 stub tools, **When** an agent calls `system.skill.install`/`search`/`list`/`remove`, **Then** they hit the real `pkg/skills` engine (not a placeholder).

**US-2 — Skill authoring create/edit (P0, self-improvement).** 1. **Given** the NEW `system.skill.create`/`edit`, **When** an agent (or Ava) authors/refines a skill, **Then** the skill file is written/updated; **the write is consent-gated (password re-type) + versioned (rollback)**. 2. **Given** a built-in skill, **Then** edits create a user override (built-ins not mutated in place).

**US-3 — Default skills embedded + seeded (P0).** 1. **Given** a fresh install, **When** it boots, **Then** the default set (`summarize · skill-authoring · plan · daily-briefing`) is `go:embed`'d into the binary and seeded to the skills dir (no external files needed).

**US-4 — Per-agent allowlist + progressive disclosure (P0).** 1. **Given** the base agents, **When** skills load, **Then** each agent sees only its allowlisted skills, loaded on demand (anti-bloat); the matrix: summarize→Mia/Ray, plan→Jim, skill-authoring→Ava, daily-briefing→Mia.

**US-5 — Marketplace-provider list (P0).** 1. **Given** `RegistryConfig` as a **list**, **When** I configure ClawHub + GitHub, **Then** `recall`/search fans out across both (the manager already supports N registries); the installer UI is deferred (shape only).

**US-6 — Plugin bundle-manifest shape (P0, shape only).** 1. **Given** the component-level-hybrid manifest, **Then** the SHAPE is defined (reuse `SKILL.md` + `.mcp.json`; native agents/channels/providers) — the installer is deferred.

**US-7 — Integrations provider-picker + mic (P1).** 1. **Given** Settings → Integrations, **When** I view it, **Then** the existing multi-provider **search** (`SearchProvider`) + **voice-in** (`Transcriber`) are configurable (behind password re-type); **When** I use the composer mic, **Then** voice-in captures via the existing transcriber.

**US-8 — Single-user / one-password / Profile / onboarding (P0).** 1. **Given** single-user, **When** I touch a sensitive setting, **Then** I re-type the one password (`RequireNotBypass`); personal settings live under a Profile menu, app config under Settings. 2. **Given** a fresh install, **When** I onboard, **Then** 3 steps (name → password → model key) → Mia·Assistant auto-provisioned in My Workspace.

### Edge Cases
- `system.skill.edit` on a built-in → user override (not in-place). · skill write without a consent handler → denied. · `RegistryConfig` empty → no marketplaces (degraded). · go:embed default already present in the seed dir → not overwritten (idempotent). · password re-type failed → sensitive op rejected. · onboarding interrupted → resumable.

## 4. Behavioral Contract · Non-Behaviors · Integration Boundaries

**Contract:** the 4 skill tools are real; create/edit author skills (consent-gated+versioned); defaults embedded+seeded; per-agent allowlist; `RegistryConfig` is a list (fan-out); the bundle-manifest shape is defined; Integrations surfaces search+voice-in; single-user/one-password; Profile/Settings split; 3-step onboarding → Mia.

**Non-behaviors:** must **not** ship the plugin installer/Marketplaces UI (shape only); must **not** ship the ACP/A2A protocol drivers (Spec-4/later); must **not** mutate built-in skills in place (override); must **not** write a skill without consent; must **not** add multi-user/RBAC; must **not** leave skills filesystem-seeded only (embed); must **not** run the full Go suite locally (CI authority); greenfield.

**Integration boundaries:** ClawHub (existing registry) + GitHub (existing `InstallFromGitHub`) via the registry list; the consent layer (Spec-1 password re-type) for skill writes; the existing `SearchProvider`/`Transcriber` for Integrations.

## 5. BDD Scenarios

```gherkin
Scenario: Stub skill tools are wired to the real engine
  Traces to: US-1 / AC-1
  Category: Happy Path
  Given system.skill.search
  When called with a query
  Then it returns results from the real RegistryManager (not a placeholder stub)

Scenario: Authoring a skill is consent-gated and versioned
  Traces to: US-2 / AC-1
  Category: Happy Path
  Given system.skill.create
  When an agent authors a skill
  Then the write requires consent (password re-type)
  And the skill is versioned (a prior version is recoverable)

Scenario: Editing a built-in creates an override
  Traces to: US-2 / AC-2
  Category: Alternate Path
  Given a built-in skill
  When system.skill.edit modifies it
  Then a user override is written and the built-in is unchanged

Scenario: Default skills embedded and seeded on fresh install
  Traces to: US-3 / AC-1
  Category: Happy Path
  Given an empty skills dir
  When the binary boots
  Then summarize, skill-authoring, plan, daily-briefing are seeded from go:embed

Scenario: Marketplace search fans out across the list
  Traces to: US-5 / AC-1
  Category: Happy Path
  Given RegistryConfig lists ClawHub and GitHub
  When search runs
  Then it fans out across both registries

Scenario: Sensitive setting requires the one password
  Traces to: US-8 / AC-1
  Category: Error Path
  Given single-user
  When I change a model key without re-typing the password
  Then the operation is rejected (RequireNotBypass)

Scenario: Onboarding auto-provisions Mia
  Traces to: US-8 / AC-2
  Category: Happy Path
  Given a fresh install
  When I complete name → password → model key
  Then Mia·Assistant is provisioned in My Workspace
```

## 6. TDD Plan

| Order | Test | Level | Traces | Description |
|---|---|---|---|---|
| 1 | `TestSkillTools_WiredToEngine_NotStub` | Integration | "wired to engine" | install/search/list/remove real |
| 2 | `TestSkillCreate_ConsentGated_Versioned` | Integration | "consent-gated + versioned" | authoring |
| 3 | `TestSkillEdit_BuiltinOverride` | Unit | "built-in override" | no in-place mutation |
| 4 | `TestDefaultSkills_EmbeddedAndSeeded` | Integration | "embedded + seeded" | go:embed + first boot |
| 5 | `TestSkillAllowlist_PerAgent_OnDemand` | Unit | US-4 | allowlist matrix |
| 6 | `TestRegistryConfig_List_FanOut` | Integration | "fans out" | single→list |
| 7 | `TestSensitiveSetting_RequiresPassword` | Integration | "requires one password" | RequireNotBypass |
| 8 | `TestOnboarding_AutoProvisionsMia` | Integration | "auto-provisions Mia" | 3-step seed |
| 9 | `verify-contracts` (CI) | CI | (if skill/registry types cross boundary) | drift = fail |
| 10 | `e2e: Integrations picker + composer mic; Profile vs Settings` | E2E | US-7/US-8 | SPA |

**Test Datasets**: skill-tool {stub→real}; create {consent→write, no-consent→deny}; edit-builtin {→override}; embed {empty→seeded, present→idempotent}; registry {[ClawHub,GitHub]→fan-out}; sensitive {no-password→reject}.

**Regression:** wires existing stubs + extends config/auth. (1) The real `pkg/skills` engine (installer/registry/loader) is unchanged — only the tools now call it; (2) the LLM `ProvidersSection` still works (Integrations adds search/voice); (3) the auth tree (`RequireNotBypass`, register-admin) preserved + the Profile split added; (4) NEW: create/edit, embed/seed, allowlist, registry-list, Integrations UI. **CI authority; local scoped.**

## 7. Functional Requirements & Success Criteria

- **FR-9.1 (C-3):** MUST wire `system.skill.{install,remove,search,list}` (today stubs) to the real `pkg/skills` engine by **adding `SkillsLoader`/`RegistryManager`/`SkillInstaller` to the sysagent tool `Deps`** (`deps.go` has none today) so the tools reach the engine.
- **FR-9.2 (C-1, C-2, M-2):** MUST add `system.skill.create` + `system.skill.edit`. **Consent** for these **tool-layer** writes routes through the **existing `ws_approval`** (`ToolApprovalRequest`→`ApprovalDecision`) — NOT `RequireNotBypass` (a 503 dev-bypass guard, wrong layer; an HTTP middleware can't gate a tool `Execute()`). **Versioning is NEW** (`pkg/skills` has none) — define a `.versions/` snapshot scheme (rollback required). Editing a built-in writes a **user override** (no in-place mutation).
- **FR-9.3 (M-1):** MUST `go:embed` the default set + first-boot seed. **`go:embed` for skills is NEW infra** (only the SPA is embedded today). Of the 4 defaults only `summarize` exists on disk — **`skill-authoring`, `plan`, `daily-briefing` MUST be AUTHORED** as part of this spec.
- **FR-9.4:** MUST scope skills **per-agent (allowlist) + progressive disclosure** (the matrix); not all skills in every agent's context.
- **FR-10.1 (M-4):** MUST change `RegistryConfig` (`{ClawHub, MaxConcurrentSearches}`) to a **list** of marketplaces; `RegistryManager` already fans out over N, **but GitHub is NOT a `SkillRegistry` today** (separate `SkillInstaller.InstallFromGitHub`) — a **new GitHub `SkillRegistry` adapter** is required for it to participate in search fan-out.
- **FR-10.2:** MUST define the **component-level-hybrid bundle-manifest SHAPE** (reuse `SKILL.md` + `.mcp.json`; native agents/channels/providers) — installer/UI deferred.
- **FR-11.1:** MUST keep MCP present; the ACP bidirectional-runner hook is **Spec-4**, the A2A agent-reference + Card-identity hooks are **Spec-3/4** (cross-ref, no new work here).
- **FR-12.1:** MUST add the **Integrations provider-picker UI** surfacing the existing `SearchProvider` + `Transcriber` (behind password re-type) + a composer **mic**.
- **FR-12.2 (C-1):** MUST enforce **single-user/one-password**; sensitive **HTTP-layer** settings = a **NEW re-authentication check** (re-verify the one password) — `RequireNotBypass` is a 503 dev-bypass guard, NOT this (ADR FR-12); **Profile** (personal) vs **Settings** (app) split.
- **FR-12.3:** MUST ship the **3-step onboarding** (name → password → model key) → auto-provision **Mia·Assistant** in My Workspace.

**Success Criteria**
- **SC-1:** the 4 skill tools return real engine results (0 "stub" placeholders). · **SC-2:** `system.skill.create`/`edit` exist, consent-gated, versioned; built-in edits = override. · **SC-3:** fresh install seeds the 4 default skills from `go:embed`. · **SC-4:** each base agent sees only its allowlisted skills. · **SC-5:** `RegistryConfig` is a list; search fans out across ≥2 registries. · **SC-6:** the bundle-manifest shape is documented. · **SC-7:** a sensitive setting without password re-type is rejected. · **SC-8:** onboarding provisions Mia. · **SC-9:** build + typecheck + verify-contracts exit 0 (CI authority; local scoped).

## 8. Traceability Matrix

| Req | US | BDD | Test |
|---|---|---|---|
| FR-9.1 | US-1 | "wired to engine" | #1 |
| FR-9.2 | US-2 | "consent-gated…" / "built-in override" | #2,#3 |
| FR-9.3 | US-3 | "embedded + seeded" | #4 |
| FR-9.4 | US-4 | (allowlist) | #5 |
| FR-10.1 | US-5 | "fans out" | #6 |
| FR-10.2 | US-6 | (manifest shape) | — (doc) |
| FR-12.1 | US-7 | (e2e) | #10 |
| FR-12.2 | US-8 | "requires one password" | #7 |
| FR-12.3 | US-8 | "auto-provisions Mia" | #8 |

## 9. Ambiguity Warnings

| # | Ambiguous | Likely assumption | Resolution |
|---|---|---|---|
| 1 | skill tool types cross boundary? | catalog-registered; contract if the SPA reads them | confirm at impl |
| 2 | skill versioning mechanism | a `.versions/` dir or git-style snapshots | pin at impl; rollback required |
| 3 | RegistryConfig list back-compat | greenfield — list is the shape | RESOLVED — single→list, greenfield |
| 4 | bundle-manifest is doc-only in v0.1.0 | shape pinned, no installer | RESOLVED — shape only |
| 5 | Integrations UI vs existing ProvidersSection | extend Settings with an Integrations surface | additive |

## 10. Holdout Evaluation Scenarios *(post-impl; NOT in traceability)*
- H1: an agent installs a skill from ClawHub → it appears (not a stub).
- H2: Ava authors a skill → password prompt; a version is recoverable.
- H3: edit a built-in skill → an override is created; the built-in is intact.
- H4: fresh install → the 4 default skills present with no external files.
- H5: configure ClawHub + a GitHub repo → search returns from both.
- H6: change a model key without the password → rejected.
- H7: onboard a fresh install → Mia greets you in My Workspace.

## 11. Assumptions
- The 4 skill tools are stubs today; the real `pkg/skills` engine exists. `[FACT — verified]`
- `RegistryConfig` single ClawHub → list (greenfield). `[FACT: registry.go:66]`
- `SearchProvider`/`Transcriber` exist; Integrations surfaces them. `[FACT: web.go:91, voice/transcriber.go]`
- Consent is **NEW** (does not exist today): tool-layer skill writes ride the existing **`ws_approval`**; HTTP-layer sensitive settings ride a **new re-auth check**. `RequireNotBypass` is a 503 dev-bypass guard, unrelated. `[C-1, ADR FR-12]`
- ACP/A2A protocol drivers are Spec-4/later; this spec only confirms MCP + the marketplace list. `[ADR FR-11]`
- The Dreamcatcher proposing skill edits is v0.2.0 behaviour over Spec-5's logs. `[ADR NFR-6]`
