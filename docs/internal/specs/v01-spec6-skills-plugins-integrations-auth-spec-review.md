# Spec-6 Review — Skills + Plugin/Marketplace Shape + Integrations & Auth

**Reviewed:** `docs/internal/specs/v01-spec6-skills-plugins-integrations-auth-spec.md`
**Mode:** plan-spec (BDD + FR/SC + traceability present)
**Reviewer posture:** adversarial, read-only, grounded against the live tree.
**Date:** 2026-06-13

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
