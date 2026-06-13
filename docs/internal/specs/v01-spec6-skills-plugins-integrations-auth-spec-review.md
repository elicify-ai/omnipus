# Spec-6 Review — Skills + Plugin/Marketplace Shape + Integrations & Auth

**Reviewed:** `docs/internal/specs/v01-spec6-skills-plugins-integrations-auth-spec.md`
**Mode:** plan-spec (BDD + FR/SC + traceability present)
**Reviewer posture:** adversarial, read-only, grounded against the live tree.
**Date:** 2026-06-13

---

## ROUND-4 VERDICT (2026-06-13) — PASS ✅ (GATE C OPEN)

Re-review after the round-3 REVISE. Round 3 withheld GATE C **solely** on three
FR↔TDD-plan inconsistencies (one MAJOR-class M-3 contradiction, two MINOR M-5/M-6
missing-negative-test residues) — explicitly "a documentation-consistency pass over the
TDD plan, not a redesign". All three are now closed. No new grounding needed; the
engine/auth/audit/loader claims were verified sound in rounds 2–3 and the spec body of
FRs is unchanged.

### The three required edits — ALL CONFIRMED CLOSED

| # | Round-3 requirement | Round-4 state | Evidence |
|---|---------------------|---------------|----------|
| 1 | **TDD #9 unconditional** — drop "(if skill/registry types cross boundary)"; `verify-contracts` must be unconditional (FR-12.1 declares the boundary crossed). | **CLOSED** | l135 now reads `verify-contracts (CI) … "fans out" + Integrations provider config (contract-first, M-3)` — the conditional is gone and it affirmatively names the contract-first Integrations provider config. Consistent with FR-12.1 (l152) "contract-first (Constraint #8 — not conditional)". |
| 2 | **TDD #5 load-denied negative** — add an explicit denial test + a `denied` dataset row. | **CLOSED** | l131 renamed `TestSkillAllowlist_PerAgent_DefaultDeny`, traced US-4, asserting "**negative: a non-allowlisted skill is DENIED at tool-resolution** (M-5)". Test Datasets (l139) adds `allowlist {allowed→invoke, not-allowed→DENY (M-5)}`. Matches FR-9.4 (l148) "default-DENY … cannot invoke it … a negative test asserts denial". |
| 3 | **Oversize + traversal skill-write tests** — restore oversize alongside traversal; add `traversal→reject`/`oversize→reject` to Test Datasets and a row to the TDD table. | **CLOSED** | NEW TDD **#11** (l137) `TestSkillWrite_Confinement_TraversalAndOversize_Rejected` — `..`/traversal + oversize SKILL.md + invalid frontmatter rejected + audit-logged. Test Datasets (l139) adds `skill-write {../traversal→reject, oversize SKILL.md→reject, invalid frontmatter→reject (M-6)}`. Backs FR-9.2 (l146) "path-confined … validate the SKILL.md … audit-log every create/edit". |

### Consistency sweep

- **RequireNotBypass-as-consent:** none survives. All 8 mentions (l11, 27, 56, 133, 141,
  146, 153, 196) describe it correctly *as* the 503 dev-bypass guard or explicitly disclaim
  it as consent. C-1r stays closed.
- **FR↔TDD alignment:** #5↔FR-9.4 (tool-resolution default-DENY + negative), #9↔FR-12.1
  (unconditional contract-first), #11↔FR-9.2/M-6 (traversal/oversize/invalid + audit) all
  agree. TDD #11 is broader than FR-9.2's "a traversal-attempt test" (adds oversize +
  invalid-frontmatter) — strengthening, not contradicting; FR-9.2's "validate the SKILL.md
  (frontmatter + structure)" backs the extra cases.
- **Traceability matrix (§8):** unchanged and not made inconsistent. #11 is an additional
  negative for already-traced FR-9.2 (matrix maps FR-9.2→#2,#3); #5/#9 are renumber/reword
  in place. The long-standing MINOR (FR-11.1 absent from §8; FR-10.2 "doc"; CI/E2E rows
  unnumbered) persists but was never gating and is unaffected by these edits.

### Verdict

No CRITICAL, no MAJOR remain. The one MAJOR-class item that held round 3 at REVISE (M-3:
FR-12.1 vs conditional TDD #9) is resolved — the test gate is now unconditional and matches
the FR. The two MINOR residues (M-5 negative test, M-6 oversize/traversal datasets) are
present in the TDD table and Test Datasets. The TDD plan is internally consistent with the
FRs and the security primitive is described correctly throughout.

**Verdict: PASS. GATE C is OPEN.** Spec-6 is ready for task decomposition.

Residual non-blocking items for the implementer (OBSERVATION, do not gate): O-1 (the
bundle-manifest shape ships with no consumer — guard against drift before the installer
arrives), m-1 (§8 matrix lacks FR-11.1/FR-10.2 rows — add explicit "doc-only" markers),
STRIDE note (registry tokens must route via the credential store like channels, SEC-23 —
confirm at impl). None of these invalidate any FR/SC.

```
/taskify docs/internal/specs/v01-spec6-skills-plugins-integrations-auth-spec.md
```

---

## ROUND-3 VERDICT (2026-06-13) — REVISE (GATE C still withheld)

Re-grounded against the live tree: `bypass_gate.go:35-45` (`RequireNotBypass` returns
**503** on `cfg.Gateway.DevModeBypass`), `ws_approval.go:84/132` (`ApproveTool` +
`ApprovalDecision`/`ToolApprovalRequest` — the real tool-layer consent), `pkg/skills/loader.go:28-48`
(`SkillMetadata` frontmatter + `validate()` — the SKILL.md validation surface exists),
`pkg/audit/` (audit infrastructure exists), `pkg/agent/loop.go` allowlist/prompt-build surfaces.

### C-1r — CLOSED. All five flagged surfaces purged of RequireNotBypass-as-consent.

| Surface | Round-2 state | Round-3 state |
|---|---|---|
| **line 11** (Overview) | "password re-type (`RequireNotBypass`)" | "a **NEW** consent primitive — `RequireNotBypass` is a 503 dev-bypass guard, unrelated" ✓ |
| **line 27** (Symbols) | "`RequireNotBypass` … the sensitive-settings gate" | row is now "NEW consent primitive (tool-layer `ws_approval`; HTTP-layer re-auth)"; `RequireNotBypass` named "a 503 dev-bypass guard, **NOT** consent" ✓ |
| **line 56** (US-8) | "I re-type the one password (`RequireNotBypass`)" | "the **NEW** re-auth check — `RequireNotBypass` is a 503 dev-bypass guard, unrelated" ✓ |
| **line 113** (BDD `Then`) | "rejected (RequireNotBypass)" | "rejected (the new re-auth check fails)" ✓ |
| **line 133** (TDD #7) | `TestSensitiveSetting_RequiresPassword` → "RequireNotBypass" | `TestSensitiveSetting_RequiresReAuth` → "the new re-auth check (NOT RequireNotBypass)" ✓ |

Every other `RequireNotBypass` mention (140, 145, 152, 195) describes it correctly *as*
the 503 dev-bypass guard — permitted by the round-2 closure rule. **No surviving
RequireNotBypass-as-consent reference anywhere.** C-1r is closed; grounding verified
(bypass_gate.go:35-45, ws_approval.go:84/132).

### The three majors — FR prose closed and grounded; but each left a TDD-plan residue

The round-3 fixes corrected the **FR prose** for M-3/M-5/M-6 correctly and with sound
grounding. But each repaired only the FR and left the **TDD plan table / Test Datasets**
carrying the exact conditional/positive-only language round-2 told the author to remove —
the same FR-fixed-but-test-not failure mode that produced C-1r in round 2. TDD-driven
implementers execute the test plan first; a negative test mandated in FR prose but absent
from the test table will be skipped.

| ID | FR prose (load-bearing) | Residue | Sev |
|----|-------------------------|---------|-----|
| **M-3** | **CLOSED** — FR-12.1 (l151) "contract-first (Constraint #8 — **not conditional**)"; §9 amb.1 (l176) "RESOLVED — provider config contract-first". | **OPEN (residue):** TDD **#9 (l135)** still reads `verify-contracts (CI) … (if skill/registry types cross boundary)` — the exact conditional round-2 required made unconditional. The FR now says it IS boundary-crossing, so the test gate must be unconditional. Self-contradiction between FR-12.1 and TDD #9. | **MAJOR→MINOR** (FR resolves the contract question; the test row is stale) |
| **M-5** | **CLOSED** — FR-9.4 (l147): enforcement at "**prompt-build + tool-resolution**", "**default-DENY**", "a negative test asserts denial, not just the positive matrix". | **OPEN (residue):** TDD **#5 (l131)** is unchanged — `TestSkillAllowlist_PerAgent_OnDemand` traced to "allowlist matrix", **still positive-only**, the precise gap round-2 named ("TDD #5 is positive-only"). The mandated load-denied negative test has no table row and no dataset. | **MINOR** |
| **M-6** | **CLOSED** (mostly) — FR-9.2 (l145): "path-confined … (no `..`/traversal), validate the `SKILL.md` (frontmatter + structure), and audit-log every create/edit". | **OPEN (residue):** (a) the **oversize** negative test round-2 required is **dropped** — only traversal survives in the FR; (b) neither the traversal-rejection test nor an oversize test appears in the **TDD table** or the **Test Datasets** line (l138 still `create {consent→write, no-consent→deny}` — no `traversal→reject`/`oversize→reject`). | **MINOR** |

Grounding for the majors is sound: `loader.go:48 validate()` + `SkillMetadata`
frontmatter back the SKILL.md-validation requirement; `pkg/audit/` backs the audit-log
requirement; `pkg/agent/loop.go` allowlist surfaces back the enforcement point.

### Why REVISE, not PASS

No CRITICAL remains — C-1r is fully closed and the three majors' load-bearing FR prose is
corrected and grounded. But the spec **self-contradicts** between FR-12.1 (contract-first,
unconditional) and TDD #9 (conditional), and the negative tests that M-5/M-6 FRs now
*require* are **absent from the test plan an implementer executes**. These are exactly the
"FR fixed, test not" residues that round-2 escalated to CRITICAL for C-1; here they sit a
notch lower because the load-bearing requirement is in the FR, but they still mean the spec
ships an internally inconsistent test plan. One MAJOR-class contradiction (M-3 FR vs TDD #9)
holds the verdict at REVISE.

### Required to reach GATE C (PASS) — test-plan alignment only

1. **TDD #9 (l135):** drop "(if skill/registry types cross boundary)" → make `verify-contracts`
   **unconditional** (FR-12.1 already declares the boundary crossed).
2. **TDD #5 (l131):** add an explicit load-denied negative test (e.g.
   `TestSkillAllowlist_DeniesUnlisted` — agent without the skill on its allowlist cannot
   invoke/load it), and a `denied` dataset row.
3. **FR-9.2 / TDD / Test Datasets:** restore the **oversize** SKILL.md negative test alongside
   traversal; add `traversal→reject` and `oversize→reject` to the Test Datasets line (l138)
   and a traversal/oversize test row to the TDD table.

No re-grounding needed — the engine/auth/audit/loader claims all check out against the live
tree. This is a documentation-consistency pass over the TDD plan, not a redesign.

**Verdict: REVISE.** C-1r closed and verified; M-3/M-5/M-6 FR prose closed and grounded;
GATE C withheld solely on the FR↔TDD inconsistencies (M-3 FR vs conditional TDD #9 is the
MAJOR; M-5/M-6 missing negative-test rows are MINOR but were explicitly named in round 2).

```
/plan-spec --revise docs/internal/specs/v01-spec6-skills-plugins-integrations-auth-spec.md docs/internal/specs/v01-spec6-skills-plugins-integrations-auth-spec-review.md
```

---

## ROUND-2 VERDICT (2026-06-13) — REVISE (not yet GATE C)

Re-grounded against the live tree: `ws_approval.go` (`ApproveTool(ctx, *ToolApprovalRequest)
→ ApprovalDecision`, policy `allow`/`ask`/`deny`, interactive frame on `ask`),
`bypass_gate.go:35` (503 dev-bypass guard), `pkg/sysagent/tools/deps.go:51` (`Deps` has
no skills handle), `pkg/skills/registry.go:51-117` (one `SkillRegistry` impl: ClawHub;
GitHub is a `SkillInstaller` method, not a registry; no version/snapshot type), on-disk
`workspace/skills/` (only `summarize` of the 4 defaults exists), and `ADR-019` line 46
(consent correction recorded).

### The six listed closures — ALL CONFIRMED CLOSED

| ID | Was | Round-2 state | Grounding |
|----|-----|---------------|-----------|
| **C-1** | `RequireNotBypass` = consent (fabricated) | **Closed** *in FR-9.2/FR-12.2/§11* — consent is NEW; tool-layer = `ws_approval`, HTTP-layer = new re-auth; `RequireNotBypass` correctly named a 503 dev-bypass guard. ADR FR-12 records it. | `bypass_gate.go:35` returns 503; `ws_approval.go:132` is the real tool-consent layer; ADR-019:46 |
| **C-2** | Layer mismatch (HTTP mw gating a tool) | **Closed** — FR-9.2 routes skill-write consent through tool-layer `ws_approval`, not HTTP middleware. | `ws_approval.go:155-167` policy `ask` → interactive |
| **C-3** | No wiring path | **Closed** — FR-9.1 adds `SkillsLoader`/`RegistryManager`/`SkillInstaller` to the sysagent tool `Deps`. | `deps.go:51` confirmed has none today |
| **M-1** | Default skills don't exist / no embed | **Closed** — FR-9.3 states `go:embed` is NEW infra and `skill-authoring`/`plan`/`daily-briefing` MUST be authored (only `summarize` on disk). | `workspace/skills/` = {agent-browser,github,skill-creator,summarize,tmux,weather} |
| **M-2** | Versioning ungrounded | **Closed** — FR-9.2 defines a NEW `.versions/` snapshot scheme. | `pkg/skills` has no snapshot/rollback type (only registry-metadata `Version` strings) |
| **M-4** | GitHub fan-out misstated | **Closed** — FR-10.1 requires a NEW GitHub `SkillRegistry` adapter for fan-out. | `registry.go` one impl (ClawHub); `installer.go:135 InstallFromGitHub` is not a registry |

The amended FR-9.1/FR-9.2/FR-9.3/FR-10.1/FR-12.2 and the §11 assumptions are now
correctly grounded. The architectural core of the spec is sound.

### Why this is REVISE and not PASS — three round-1 MAJORs were NOT in the closure list, and C-1's fabrication survives outside the corrected FRs

The author's closure list covered C-1/C-2/C-3/M-1/M-2/M-4. It silently omitted **M-3,
M-5, M-6** (all still open) and left the **original C-1 fabrication intact in five
surfaces an implementer actually reads** (Overview, Symbols, US-8, the BDD scenario,
the TDD test name). A spec that corrects a security primitive in the FR section but
leaves the wrong primitive in the user story, the executable BDD scenario, and the test
name will be implemented wrong — the implementer follows the scenario, not the FR prose.

| ID | Sev | Status | Evidence in current spec |
|----|-----|--------|--------------------------|
| **C-1r** (residue) | **CRITICAL** | **OPEN** | The corrected FRs coexist with the un-corrected claim in: **line 11** ("sensitive settings = password re-type (`RequireNotBypass`)"), **line 27** (Symbols: "`RequireNotBypass` … password re-type … the sensitive-settings gate"), **line 56** (US-8: "I re-type the one password (`RequireNotBypass`)"), **line 113** (BDD: "rejected (RequireNotBypass)"), **line 133** (TDD `TestSensitiveSetting_RequiresPassword` traces to "RequireNotBypass"). The spec now contradicts itself on its load-bearing security control. |
| **M-3** | MAJOR | **OPEN** | Contract boundary still conditional: **line 135** TDD #9 "(if skill/registry types cross boundary)"; **§9 amb.1 (line 176)** "confirm at impl". The Integrations picker (FR-12.1) and onboarding state cross the SPA boundary → Constraint #8 requires schemas *before* code; `verify-contracts` must be unconditional. Unchanged from round 1. |
| **M-5** | MAJOR | **OPEN** | FR-9.4 (line 147) still only "scope skills per-agent (allowlist) + progressive disclosure". No enforcement point (loader filter vs tool policy vs agent config), no default-deny rule, no custom/new-agent behavior, no load-denied negative test (TDD #5 is positive-only). Unchanged from round 1. |
| **M-6** | MAJOR | **OPEN** | Zero requirements for the self-modifying-write surface: no path-confinement to the user-skills dir, no `SKILL.md` schema validation before persist, no audit-log entry per create/edit, no traversal/oversize negative test. The (now-real) `ws_approval` consent helps, but the write-path hardening C-1/C-2 was paired with is still absent. Unchanged from round 1. |

Round-1 MINORs (m-1 FR-11.1 still absent from §8 matrix; m-2 sensitive-set unenumerated;
m-3 onboarding replace-vs-extend + resume; m-4 transcriber default/limits) and the STRIDE
note (registry tokens must route via the credential store like channels, SEC-23 — no
mention in the spec) also remain open but do not by themselves change the verdict.

### Required to reach GATE C (PASS)

1. **Close C-1r:** purge `RequireNotBypass` as the consent/re-type primitive from **lines 11, 27, 56, 113, 133**. Overview/Symbols/US-8 must say "re-authentication check (re-verify the one password)"; the BDD `Then` and the TDD test must assert the new re-auth check, not `RequireNotBypass`. `RequireNotBypass` may remain only where the spec describes it *as* the 503 dev-bypass guard (it is fine in FR-9.2/FR-12.2/§11).
2. **Close M-3:** make TDD #9 unconditional; resolve §9 amb.1 by enumerating SPA-read payloads (registry list, provider config, skill list, onboarding state) and requiring OpenAPI/AsyncAPI schemas + generated types per the 5-step process before handler code.
3. **Close M-5:** pin the allowlist enforcement point and a default-deny rule (agent sees only explicitly-allowlisted skills; custom agents → none by default); add a load-denied negative test.
4. **Close M-6:** require write confinement to the user-skills dir (no traversal), `SKILL.md` schema validation before persist, an audit entry per create/edit, and traversal/oversize negative tests.

**Verdict: REVISE.** Six listed items genuinely closed and well-grounded; GATE C withheld
because the spec now self-contradicts on its security primitive (C-1 fabrication left in
the story/BDD/test it must drive) and three round-1 MAJORs (M-3/M-5/M-6) were not addressed.

```
/plan-spec --revise docs/internal/specs/v01-spec6-skills-plugins-integrations-auth-spec.md docs/internal/specs/v01-spec6-skills-plugins-integrations-auth-spec-review.md
```

---

## Round 1 (2026-06-13) — BLOCK (superseded; kept for history)

---

## Executive Summary

This omnibus spec wires four stub skill tools to the real `pkg/skills` engine, adds
authoring verbs (`create`/`edit`), embeds a default skill set, lists marketplaces,
surfaces an Integrations UI, and ships single-user auth/onboarding. The intent is
sound and the engine-side grounding (stubs, `RegistryManager`, `SkillInstaller`,
`SearchProvider`, `Transcriber`) is largely accurate.

**However, the spec's central security mechanism is fabricated.** The repeated claim
that skill writes are "consent-gated (password re-type) via `RequireNotBypass`
(Spec-1)" does not survive grounding: `RequireNotBypass` is a *dev-mode-bypass guard*
that returns **503**, not a password re-type / re-authentication gate; Spec-1 contains
**no** consent mechanism at all; and skill tools execute inside the agent loop, where
an HTTP middleware cannot reach them. The "versioned (rollback)" guarantee is likewise
ungrounded — `pkg/skills` has no versioning. And the **wiring path itself is undefined**:
the `Deps` struct that skill tools receive carries no skills-engine reference today.

**Findings:** 3 CRITICAL · 6 MAJOR · 4 MINOR · 3 OBSERVATION
**Verdict: BLOCK.**

---

## Findings Table

| ID | Sev | Lens | Section | Finding | Fix |
|----|-----|------|---------|---------|-----|
| C-1 | CRITICAL | Incorrectness / Insecurity | FR-9.2, US-2, §4, §11, BDD "consent-gated" | `RequireNotBypass` is **not** a password re-type / consent gate. `pkg/gateway/middleware/bypass_gate.go:35` returns **503** when `cfg.Gateway.DevModeBypass` is true (or config is missing) — it gates admin routes when dev-mode-bypass is on. It performs **no** re-authentication and accepts **no** password. The entire "consent = password re-type (`RequireNotBypass`)" premise is fabricated. | Either (a) specify a *real* consent primitive (a new re-auth endpoint / step-up that verifies the single password against the credential store), grounded and contract-defined, or (b) drop the password-re-type claim and state the actual gate (e.g., per-agent tool policy `ask`). Do not cite `RequireNotBypass`. |
| C-2 | CRITICAL | Infeasibility / Architecture | FR-9.2, FR-9.1, US-1/US-2 | **Layer mismatch makes the consent gate impossible as written.** Skill tools are agent-loop tools (`Execute(ctx, args)` in `pkg/sysagent/tools/skill.go`), invoked by the turn engine — **not** HTTP routes. `RequireNotBypass` is an `http.HandlerFunc` middleware. It architecturally cannot wrap a tool `Execute`. A tool cannot trigger an interactive browser password prompt mid-turn. | Specify how a *tool* obtains consent: either route skill-write *through* a gateway endpoint the SPA calls (and the agent only proposes), or define a tool-policy `ask`/confirmation channel. Pin the exact mechanism; it is the load-bearing security control of FR-9.2. |
| C-3 | CRITICAL | Incompleteness | FR-9.1, US-1, TDD #1, "Symbols Involved" | **No wiring path is specified.** The `Deps` struct skill tools receive (`pkg/sysagent/tools/deps.go:51`) has **no** `SkillsLoader`, `RegistryManager`, or `SkillInstaller` field. FR-9.1 says "wire the stubs to the real engine" but never says how the engine reaches the tool layer (add to `Deps`? construct inline? inject via gateway?). This is the spec's #1 deliverable and it is hand-waved. | Add an explicit requirement: extend `Deps` with the skills-engine handle(s), wire them where `Deps` is constructed in the gateway boot, and state nil-handling (tools must error cleanly when the engine is absent, e.g. in tests). Reference `deps.go` line. |
| M-1 | MAJOR | Incorrectness | FR-9.3, US-3, SC-3, BDD "embedded + seeded" | **The named default set does not match the tree, and `go:embed` for skills does not exist.** On disk: `workspace/skills/{agent-browser, github, skill-creator, summarize, tmux, weather}`. The spec's "default set" is `summarize · skill-authoring · plan · daily-briefing` — only `summarize` exists; `skill-authoring`, `plan`, `daily-briefing` are **not present as files**. Nothing in `pkg/skills` or `cmd/` uses `go:embed` for skills (only `pkg/gateway/embed.go` embeds the SPA). So FR-9.3 must (a) author 3 new skill files and (b) build the embed+seed machinery from scratch — both larger than "embed the existing default set" implies. | State that 3 of 4 default skills must be **authored** as part of this spec (or revise the set to existing skills), and that the `embed.FS` + first-boot seed is **new** infrastructure, not a wiring of existing seeding. List the exact `SKILL.md` files to embed and the seed-on-empty logic location. |
| M-2 | MAJOR | Infeasibility | FR-9.2, US-2, BDD "versioned", §9 amb. 2 | **"Versioned (rollback)" is ungrounded.** `pkg/skills` has no version/snapshot/rollback mechanism (loader + installer carry none). Ambiguity #9.2 admits the mechanism is unknown ("`.versions/` dir or git-style — pin at impl"). A P0 success criterion (SC-2: "versioned") rests on undecided design. | Pin the versioning mechanism in the spec (storage layout, what a "version" is, how rollback is invoked and by whom). If it cannot be pinned now, descope rollback from v0.1.0 and remove it from SC-2 / BDD, rather than leaving a P0 on "decide at impl". |
| M-3 | MAJOR | Incompleteness / Contract | FR-10.1, US-5, §9 amb. 1, TDD #9 | **Wire-format boundary is left "confirm at impl".** Constraint #8 (contract-first) requires every byte crossing the SPA boundary to be schema-defined *before* code. The Integrations UI (FR-12.1) and any SPA-visible skill/registry data (skill list, marketplace list) cross that boundary, yet §9 amb.1 defers this ("contract if the SPA reads them — confirm at impl") and TDD #9 is conditional ("if skill/registry types cross boundary"). Given an Integrations picker UI reads provider/registry config, they **do** cross. | Resolve the boundary now: enumerate which payloads the SPA reads (registry list, provider config, skill list, onboarding state), and require the OpenAPI/AsyncAPI schemas + generated types per the 5-step process **before** handler code. Make `verify-contracts` non-conditional. |
| M-4 | MAJOR | Incorrectness | FR-10.1, US-5, §2, §11 | **`RegistryConfig` "single→list" misstates the shape.** `registry.go:66` defines `RegistryConfig{ ClawHub ClawHubConfig; MaxConcurrentSearches int }` — it is not literally "a single ClawHub field". `RegistryManager` already fans out over N registries (`AddRegistry`/`SearchAll`), but `NewRegistryManagerFromConfig` only instantiates ClawHub when enabled — GitHub is **not** currently a registry behind the `SkillRegistry` interface (GitHub installs go through `SkillInstaller.InstallFromGitHub`, a different path). So "search fans out across ClawHub + GitHub" (BDD "fans out") requires a **new** `SkillRegistry` adapter for GitHub, not just a config list change. | Correct §2/§11 to describe the real struct. Add an explicit requirement: implement a GitHub `SkillRegistry` (search-capable) so fan-out is meaningful; or scope FR-10.1 to "config becomes a list of `ClawHubConfig`-shaped entries" and drop the GitHub-fan-out claim from BDD/SC-5. |
| M-5 | MAJOR | Ambiguity / Insecurity | FR-9.4, US-4 | **Allowlist enforcement layer + default-deny posture undefined.** The matrix (summarize→Mia/Ray, plan→Jim, …) names assignments but not (a) where enforcement lives (loader filter? tool policy? agent config?), (b) the default for skills *not* in any allowlist (denied? visible to all?), (c) what a custom/new agent sees. Without default-deny stated, "progressive disclosure" can silently become "all skills to all agents". | Specify the enforcement point and a default-deny rule (an agent sees only explicitly-allowlisted skills; unassigned skills are hidden). Define behaviour for custom agents. Add a negative test: agent X cannot load skill not in its allowlist. |
| M-6 | MAJOR | Insecurity (STRIDE: EoP/Tampering) | FR-9.2, US-2 | **Self-modifying-agent threat under-specified.** `system.skill.create/edit` lets an agent write files that become *its own future instructions* (procedural memory). With the consent gate fabricated (C-1/C-2), an agent can author/alter skills with no real human checkpoint. No path-confinement, no SKILL.md schema validation, no size/quota, no audit-log requirement is specified for the write. | Add requirements: writes confined to the user-skills dir (no traversal), SKILL.md validated against a schema before persist, an audit-log entry per create/edit, and the (real) consent gate from C-1. Add tamper/traversal negative tests. |
| m-1 | MINOR | Inconsistency | §3 US-list vs §8 matrix | US-6 (bundle-manifest shape) and US-7's mic and the Profile/Settings split appear in stories but the traceability matrix has no row for FR-10.2 test coverage beyond "— (doc)" and no FR for the Profile/Settings split as distinct from FR-12.2. Orphan-ish coverage. | Add matrix rows (or explicit "doc-only, no test" markers) for every FR including FR-11.1; make the doc-only items explicit rather than blank. |
| m-2 | MINOR | Ambiguity | FR-12.2, US-8 | "Sensitive settings = password re-type" — the *set* of sensitive settings is never enumerated (model key is the only example). Two engineers will gate different surfaces. | Enumerate the sensitive-setting set (credential/provider keys, registry tokens, skill writes, user/password change) or define the rule that classifies one. |
| m-3 | MINOR | Incompleteness | FR-12.3, US-8, BDD "onboarding" | Onboarding is "3-step (name → password → model key)" but the existing flow has `/api/v1/onboarding/complete` with rate limiting; the spec doesn't say whether this *replaces* or *reshapes* the current onboarding, nor the resume mechanism it claims in Edge Cases ("interrupted → resumable"). | State whether onboarding is rebuilt or extended; specify the resume/state mechanism, or drop the "resumable" edge case if unsupported. |
| m-4 | MINOR | Ambiguity | FR-12.1, US-7 | "Composer mic … captures via the existing transcriber" — but `pkg/voice` has 4 transcriber backends (audio_model, elevenlabs, groq, plus the interface). Which is the default? How is selection surfaced? Capture format/length limits? | Name the default transcriber, the selection mechanism in the picker, and audio constraints (format, max duration). |
| O-1 | OBSERVATION | Overcomplexity | FR-10.2, US-6 | The "component-level-hybrid bundle-manifest SHAPE" (native agents/channels/providers) with no installer is pure speculative generality for v0.1.0 — it ships a documented shape nobody consumes. Per issue #151 the unified plugin system is unbuilt. | Consider deferring FR-10.2 entirely to the plugin epic. A manifest shape with no reader cannot be validated and tends to drift before the installer arrives. |
| O-2 | OBSERVATION | Overcomplexity | FR-9.4 | A static per-agent allowlist matrix for 4 base agents × 4 skills may be simpler as agent-config seed values than a new enforcement subsystem. | Prefer expressing the allowlist in existing agent config (`SeedConfig`) over new machinery if a config field suffices. |
| O-3 | OBSERVATION | Inoperability | whole spec | No observability requirements: no audit-log/metric for skill installs/writes, no health surface for registry reachability, no structured-log requirement on the consent decision. | Add: audit entry per skill install/create/edit/remove; a degraded-marketplace signal when a registry is unreachable. |

---

## Structural Integrity (plan-spec checks)

| Check | Result |
|---|---|
| Every US has ≥1 acceptance scenario | PASS |
| Every US has a BDD scenario | **FAIL** — US-4 (allowlist) and US-6 (manifest) have no Gherkin; US-7 covered only by an E2E line |
| Every BDD has a `Traces to:` | PASS |
| Every BDD has a TDD test | PASS (US-6 doc-only) |
| Every FR in traceability matrix | **FAIL** — FR-11.1 absent from matrix; FR-10.2 test = "doc" only |
| Every BDD in matrix | PASS |
| Boundary/edge/error datasets | PARTIAL — happy-path heavy; no traversal/oversize/concurrent-write datasets for skill writes |
| Regression addressed | PASS (§6 Regression) |
| Success criteria measurable | **FAIL** — SC-2 "versioned" and SC-7 "rejected" rest on the fabricated/undefined consent + versioning (C-1, M-2) |

---

## Test Coverage Assessment

- **Negative tests thin.** Only `no-consent→deny` and `no-password→reject` are listed, both of which test a mechanism (C-1) that doesn't exist as described. No path-traversal, oversize-SKILL.md, malformed-frontmatter, or concurrent-write tests for the authoring verbs (M-6).
- **No engine-absent / nil-Deps test** for the wiring (C-3) — yet tests routinely construct `Deps` without an agent loop.
- **Fan-out test (#6)** will be vacuous until a GitHub `SkillRegistry` exists (M-4); a "fan-out across ClawHub+GitHub" test cannot pass against the current single-registry config.
- **Allowlist test (#5)** lacks the load-denied negative case (M-5).
- **Contract test (#9) is conditional** — must be unconditional given the Integrations UI reads config (M-3).

---

## STRIDE Threat Summary

| Component | Threat | Status in spec |
|---|---|---|
| `system.skill.create/edit` (file write) | **Tampering / EoP** — agent writes its own future instructions; path traversal; built-in poisoning | Under-specified; "consent" gate fabricated (C-1, C-2, M-6) |
| `system.skill.install` (network → disk) | **Tampering / DoS** — malicious archive, zip-bomb | Engine has SSRF + hash-verify + zip limits (good); spec must require the wired path preserves them |
| Marketplace list (registry tokens) | **Info Disclosure** — tokens in config.json | Spec says "behind password re-type" (fabricated); must route tokens via credential store like channels (SEC-23) |
| Integrations / transcriber (audio upload) | **DoS / Info Disclosure** — unbounded audio, PII in transcript logs | Not addressed (m-4) |
| Onboarding (set admin password) | **Spoofing** — first-writer-wins admin | Rate-limited today; spec must preserve register-admin single-shot semantics |

---

## Unasked Questions

1. What *exactly* is the consent primitive, at which layer, verifying what against the credential store? (C-1/C-2 — load-bearing.)
2. How does the skills engine reach a tool's `Execute`? Through `Deps`? (C-3.)
3. Are the 3 missing default skills (`skill-authoring`, `plan`, `daily-briefing`) to be authored here, and where are their `SKILL.md` bodies specified? (M-1.)
4. Is GitHub a `SkillRegistry` (searchable) or only an installer source? Fan-out depends on the answer. (M-4.)
5. What does a custom agent see by default — all skills or none? (M-5.)
6. Which transcriber backend is the composer mic default, and what are the audio limits? (m-4.)
7. Which config keys count as "sensitive"? (m-2.)
8. Does FR-10.2's manifest shape have any consumer in v0.1.0, or is it documentation that will drift? (O-1.)

---

## Verdict

**BLOCK.** The spec's primary security control (consent-gated skill writes) is grounded
in a misread of `RequireNotBypass` and a non-existent Spec-1 mechanism, sits at the wrong
architectural layer for a tool, and pairs with an ungrounded versioning guarantee — while
the headline deliverable (engine wiring) omits the wiring path entirely, and the default
skill set doesn't exist on disk. These are not polish items; they invalidate FR-9.1,
FR-9.2, FR-9.3, FR-10.1, and SC-2/SC-3/SC-7 as written.

Address C-1, C-2, C-3 and M-1..M-6, then re-run:

```
/plan-spec --revise docs/internal/specs/v01-spec6-skills-plugins-integrations-auth-spec.md docs/internal/specs/v01-spec6-skills-plugins-integrations-auth-spec-review.md
```
