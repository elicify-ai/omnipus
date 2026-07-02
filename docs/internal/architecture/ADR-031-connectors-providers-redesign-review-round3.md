# Adversarial Review (Round 3): ADR-031 Connectors & Providers Redesign

**Spec reviewed**: `docs/internal/architecture/ADR-031-connectors-providers-redesign.md`
**Review date**: 2026-07-02
**Mode**: structured-spec (numbered FR/NFR/G requirements, decision criteria; no BDD/traceability matrix — this is an ADR, not a plan-spec)
**Verdict**: REVISE

## Executive Summary

The ADR is in strong shape after two prior grill rounds: the direction is operator-ratified, every open fork (G-1/G-2/G-3) is decided, and the R2 fixes I re-verified against source are accurate — `GetDefaultAPIBase` does return non-empty localhost URLs for `litellm`/`ollama`/`vllm` (`factory_provider.go:508/550/578`), so the pivot to reusing `endpointHint` is correct; `_app.tsx` beforeLoad ordering matches the FR-9 safety claim; there is genuinely no SPA feature-flag infrastructure; and `PLAN_LABELS` is exactly as quoted. No CRITICAL findings — the contract-derivation layer that R2 rebuilt holds up. However, one **MAJOR** slipped both prior rounds: the ADR mandates *deleting* `AVAILABLE_PROVIDERS` but never accounts for the live probe-enum ⊆ invariant test that guards it, which is a real cross-boundary safety property, not incidental test scaffolding. Four further MAJORs concern acceptance-criteria gaps that will re-open at plan-spec time. Verdict is REVISE, not BLOCK — nothing here is unshippable, but five items need closing before `/plan-spec` to avoid re-litigation.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 5 |
| MINOR | 5 |
| OBSERVATION | 3 |
| **Total** | **13** |

---

## Findings

### MAJOR Findings

#### [MAJ-001] Deleting `AVAILABLE_PROVIDERS` silently drops a live cross-boundary safety invariant

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: §6 G-2 point 1 ("`AVAILABLE_PROVIDERS` is **deleted**"), §9 ("then delete `AVAILABLE_PROVIDERS`")
- **Description**: The ADR treats `AVAILABLE_PROVIDERS` as pure duplication to be retired into a backend seed. But `src/routes/-onboarding.test.tsx:1029-1074` runs a **live invariant** over it that is a genuine safety property, not scaffolding: (a) *every* variant id ∈ the `ProbeProviderRequest` zod enum ("UI never offers an unsupported provider" — a cross-boundary contract guard, since a probe with an unknown id is a 4xx/no-op), and (b) exact per-company variant counts (Zhipu=6, Moonshot=4, MiniMax=4, DeepSeek=2, Qwen=4). When `AVAILABLE_PROVIDERS` is deleted, this test either deletes with it (losing the probe-enum guard entirely) or must be re-homed to assert *catalog ids ⊆ probe enum* — and the ADR's redefined drift-guard (§6 G-2 point 2) checks catalog↔`knownProtocols`, **not** catalog↔`ProbeProviderRequest` enum. Those are two different id-sets (`knownProtocols` has 61; the probe enum is a separate contract schema). A catalog id that is a valid `knownProtocol` but *missing from the probe enum* would pass the ADR's drift-guard and still break onboarding's model-list probe.
- **Impact**: The onboarding probe silently regresses for any catalog entry not also in `ProbeProviderRequest`, with no test catching it — exactly the class of bug the deleted invariant existed to prevent. At plan-spec this surfaces as "why did the probe stop working for provider X after the migration."
- **Recommendation**: Add to §6 G-2 / §9: the drift-guard must gain a **third property** — every catalog `id` (post-alias-normalization) is a member of the `ProbeProviderRequest` id enum — re-homing the invariant that `AVAILABLE_PROVIDERS ⊆ probe-enum` guaranteed. Explicitly state the retirement plan for `-onboarding.test.tsx:1029-1074`: which assertions move to the backend `contract_test.go`, and that the per-company count checks either move to a catalog-seed test or are dropped with justification.

#### [MAJ-002] FR-1 "configured-only list + empty-state roster" has no acceptance criterion for the partially-configured / disabled / broken-credential states

- **Lens**: Incompleteness
- **Affected section**: FR-1, FR-3, FR-5
- **Description**: FR-1 splits the world into "configured" (shown in list) vs "connectable" (shown in empty-state roster + behind "Connect"). Reality has more states: a provider row whose API key was stored but now fails validation (ProvidersSection today renders "Connected"/"Not configured" status badges at 193-204 — a real tri-state exists); a channel *configured but disabled*; a provider *configured with zero model slugs* (the "No models added yet" state at `ProvidersSection.tsx:351-354` the ADR itself calls out as distinct). The ADR defines the binary but never says which bucket a half-configured or invalid entry lands in, nor whether "configured" means "has a stored key" or "has a *validated* key."
- **Impact**: Two engineers implement different definitions of "configured." A provider with an expired key either vanishes from the list (operator can't find it to fix it) or shows as configured-but-broken with no affordance — both are UX failures the redesign is meant to cure.
- **Recommendation**: Define "configured" precisely (recommend: "has a persisted config entry", independent of key validity), and specify that invalid-key / disabled / zero-model entries stay in the configured list with a distinct status indicator (reuse the existing status badge). Add this as an explicit plan-spec BDD row.

#### [MAJ-003] No acceptance criterion ties "onboarding ≡ Settings terminology" (NFR-1) to a failing-if-drifted check across the G-3=C duplicated pickers

- **Lens**: Infeasibility (untestable as written) / Inconsistency
- **Affected section**: NFR-1, §6 G-3, §9 ("Add a **consistency test** proving both render identical terminology + logos")
- **Description**: The ADR's central de-risking argument for G-3=C is "words are wire data, so a duplicated picker can't drift the words." §9 asks plan-spec to "add a consistency test," but the mechanism that *makes* the words identical is only guaranteed if both pickers read the label from the catalog rather than re-deriving it. Recall the F-07 refinement (§6 G-2 caveat) explicitly proposes **deriving `label`/`subtitle` frontend-side from a "shared label map"** rather than carrying them on the wire. If that refinement is taken, the "words are wire data" guarantee evaporates — the label map is now frontend code that each picker could import differently or inline. The ADR's own preferred refinement partially undermines its own G-3=C safety argument, and neither §6 nor §9 resolves the tension.
- **Impact**: If plan-spec adopts the label-derivation split (which §6 recommends "on NFR-2 alone") *and* duplicates the picker (G-3=C), the terminology-drift risk the ADR claims is eliminated is quietly reintroduced. The "consistency test" becomes the *only* guard, and the ADR doesn't specify it as mandatory-blocking.
- **Recommendation**: Resolve the interaction explicitly: if `label`/`subtitle` are derived frontend-side (F-07 refinement), state that the derivation MUST live in a single shared module imported by both pickers (not inlined), and make the §9 consistency test a **blocking** acceptance criterion, not a nicety. Alternatively, note that keeping `label`/`subtitle` on the wire (the operator's plain baseline) is what actually preserves the G-3=C safety claim, so the two decisions interlock and should be decided together.

#### [MAJ-004] Migration reverse-lookup (G-4) has no acceptance criterion for the region-suffixed / dropped-variant ids that exist in real configs

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: G-4 (§3 table + §7 risk), §6 G-2 point 2
- **Description**: G-4 says existing configs store ids like `z-ai-coding` and the reverse-lookup normalizes aliases then falls back to a generic group. But the ADR's own R1 correction established the catalog is the **curated ~30 subset**, while real persisted configs may hold ids that are valid `knownProtocols` yet **deliberately excluded from the catalog** (the ~31 alias/CLI/infra ids — e.g. an operator who configured `litellm` or `ollama` or `qwen-coding`). Those are not "unknown/unrecognized" (the ADR's only fallback trigger) — they're *known but catalog-excluded*. The reverse-lookup logic as described has no branch for "valid protocol, intentionally not in catalog": does that provider vanish from Settings entirely? Show under a generic group? The migration spec covers alias→canonical and unknown→generic, but not known-excluded.
- **Impact**: An operator who configured a self-hosted `vllm`/`ollama`/`litellm` provider (a documented, supported path) finds it missing or mis-grouped after the redesign, because the catalog — the new display SoT — has no entry for it. This is a real regression for exactly the self-hosted users the excluded-but-instantiable design was meant to preserve.
- **Recommendation**: Add a G-4 branch: a configured provider whose id is a valid `knownProtocol` but *not* in the catalog MUST still render (recommend: a "Custom / self-hosted" or "Other" group using the raw id + lettermark), since these remain instantiable by design. Add a migration test fixture with a `litellm`/`ollama` config asserting it survives the redesign visible-and-editable.

#### [MAJ-005] "endpointHint" reuse (R2-01 fix) inherits the field's placeholder/non-URL values without specifying display handling

- **Lens**: Incorrectness / Ambiguity
- **Affected section**: §6 G-2 point 3, §9 ("`endpointHint` correctness table-test")
- **Description**: The R2 fix correctly reuses the curated `endpointHint`, but that field's real values include **non-resolvable placeholders** — verified: `azure` → `<resource>.openai.azure.com` (`onboarding.tsx:137`), and localhost hosts (`ollama` → `localhost:11434`, line 130). The ADR calls `endpointHint` "the literal host" (FR-6: "subtitle naming billing model + literal host") but `<resource>.openai.azure.com` is a *template*, not a literal host, and a localhost value is meaningless as a "the endpoint you connect to" cue in a hosted-provider list. §9's correctness test asserts the catalog value *matches the curated source* — which just pins the template string, not that it's a sensible display value. FR-6's "literal host" language and the Azure template value are in direct tension.
- **Impact**: Settings and onboarding render `<resource>.openai.azure.com` as if it were an endpoint the operator connects to, which is confusing (it's a fill-in-the-blank), and the "literal host" acceptance language is unsatisfiable for Azure/self-hosted rows.
- **Recommendation**: Either (a) soften FR-6 from "literal host" to "endpoint hint (may be a template for deployment-configured providers)" and state that template/localhost hints render verbatim as a recognition aid, not a live URL claim; or (b) specify per-row handling for the placeholder cases. The §9 correctness test should additionally assert that every catalog `endpointHint` is non-empty (catching a silent omission) and flag which ids are templates.

---

### MINOR Findings

#### [MIN-001] FR-12 mandates a build-time SVG sanitizer without noting DOMPurify is already in the tree

- **Lens**: Incompleteness (feasibility grounding)
- **Affected section**: FR-12, NFR-3
- **Description**: FR-12 requires an allow-list SVG sanitizer and NFR-3 allows "a build-time dev-dep to extract SVGs." The tree already ships **DOMPurify** (used for Mermaid SVG — `mermaid-renderer.test.tsx:101,349`, `src/store/ui.ts:115`), which does allow-list SVG sanitization at runtime. The ADR doesn't say whether FR-12 reuses DOMPurify (already present, no new dep) or adds a separate build-time tool, leaving the "no new runtime dep" (NFR-3) question of whether runtime-sanitized-once-at-build counts as clean.
- **Recommendation**: Note that DOMPurify (already a dependency) can perform the allow-list sanitization at build time, satisfying FR-12 with zero new runtime deps. Confirm the sanitizer runs at build (vendored SVGs are static), so no per-render cost and no runtime dep concern.

#### [MIN-002] `login.tsx` has a *second* onboarding entry point the FR-9 analysis doesn't mention

- **Lens**: Incompleteness
- **Affected section**: FR-9, §1 (login re-onboard button)
- **Description**: FR-9 removes the "Set up Omnipus for the first time" button (`login.tsx:182-188`). But `login.tsx:32-35` *also* navigates to `/onboarding` after a successful login when `!state.onboarding_complete`. The ADR's safety analysis only discusses the button and the `_app.tsx` guard; it doesn't confirm the post-login redirect is retained (it should be — it's the real path a logged-in-but-not-onboarded admin reaches onboarding). Removing the button but not mentioning this sibling path risks a plan-spec agent over-removing.
- **Recommendation**: Add a one-line note to §1/FR-9: only the standalone button + `Rocket` import are removed; the post-successful-login `!onboarding_complete → /onboarding` redirect (`login.tsx:32-35`) is retained. The regression test should assert this path still works.

#### [MIN-003] "Adaptive grouping shown when ≥1 entry" (FR-4) contradicts the configured-only empty state (FR-1)

- **Lens**: Inconsistency
- **Affected section**: FR-4 ("Grouping is adaptive (shown when ≥1 entry)"), FR-1
- **Description**: FR-4 says grouping shows "when ≥1 entry," but FR-1 says the list shows *only configured* entries and the catalog roster appears in the *empty state*. With exactly 1 configured entry, is a group header (with logo + "Add another…") shown for a single row? That's arguably heavier chrome than the single row warrants. The threshold "≥1" seems to mean "always group once anything is configured," but "adaptive" implies it varies — the two words fight.
- **Recommendation**: Clarify: does a single configured instance render inside a full group header, or as a bare row until a second instance appears? State the exact threshold and what "adaptive" modifies.

#### [MIN-004] No success criterion / definition-of-done for the ADR as a whole

- **Lens**: Infeasibility (measurability)
- **Affected section**: §4 Decision Criteria, §9
- **Description**: §4 lists weighted decision *criteria* (used to pick options) but there is no measurable "this redesign is done when…" statement. The plan-spec handoff (§9) is a task list, not exit criteria. For a two-plan-spec split (Providers / Channels), each needs its own done-definition. "Fixes the mental-model defect" (criterion, weight 0.20) is subjective as written.
- **Recommendation**: Add a short "Definition of Done" per shippable half (Providers, Channels): e.g. "Providers: Settings + onboarding both render from the shared catalog; a consistency test passes; configured-only list + roster empty state ships; disclaimer renders on every mark-bearing screen." Keeps §9 honest.

#### [MIN-005] `catalogVisible` predicate vs `catalogExcluded` list — the ADR uses both terms for the same partition

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: §6 G-2 point 1 ("an explicit `catalogVisible` predicate (or allow-list)"), point 2 & §9 ("`catalogExcluded` list")
- **Description**: §6 G-2 point 1 describes the mechanism as a `catalogVisible` allow-list; points 2 and §9 describe the drift-guard against a `catalogExcluded` list. Allow-list (name what's in) and exclude-list (name what's out) are opposite postures with different failure modes (a new user-facing protocol defaults *out* under allow-list, *in* under exclude-list). The drift-guard's "a new protocol fails CI until triaged" behavior depends on which one is authoritative.
- **Recommendation**: Pick one authoritative structure. Recommend allow-list (`catalogVisible`): a new protocol is invisible until explicitly added, which is the safer default and matches "curated ~30 subset." Then the drift-guard's property (b) becomes "every non-alias/CLI/infra protocol is either allow-listed or on an explicit `knownNonCatalog` list" — reconcile the naming.

---

### Observations

#### [OBS-001] The R2-10 build-time `go:embed` catalog is strictly better than the live endpoint and should be the recommendation, not a co-equal option

- **Lens**: Overcomplexity / Inoperability
- **Affected section**: §6 G-2 R2-10, §9 ("live `GET /providers/catalog` … OR the R2-10 build-time `go:embed` artifact — plan-spec picks")
- **Suggestion**: The catalog is static (changes only on rebuild), so a live `GET /api/v1/providers/catalog` endpoint adds: a pre-auth fingerprinting surface (F-08), a network round-trip in onboarding's critical first-run path, an endpoint to auth/rate-limit/test, and a failure mode (endpoint down → onboarding can't render providers). The `go:embed` artifact has none of these and delivers the identical single-SoT. The ADR frames these as equal plan-spec options; the operator chose "B" (backend-owned SoT), which the embed satisfies *better*. Recommend the ADR lean toward the embed and require plan-spec to justify a live endpoint if it wants one — G-2=B does not require a runtime endpoint.

#### [OBS-002] `contracts/` surface for a display-only catalog is worth a second look against Constraint #8's own carve-out

- **Lens**: Overcomplexity
- **Affected section**: §6 G-2 (F-07 caveat), NFR-2
- **Suggestion**: The ADR already documents the tension: much of `ProviderCatalogEntry` (label, subtitle, logoSlug) is pure presentation that NFR-2 would *exempt* from the contract. Combined with OBS-001 (no runtime endpoint needed), the "backend-owned catalog" could be a Go source-of-truth compiled into an embedded JSON/TS artifact **without** a hand-authored `contracts/` schema at all — the generated TS type is the frontend surface, satisfying "generated types only" without inventing a wire schema for data that never crosses the wire at runtime. Flag for plan-spec: does G-2=B *require* an OpenAPI schema, or just a single backend SoT + generated types? The operator chose "backend-owned + on the contract"; if the embed path (OBS-001) is taken, "on the contract" may be vestigial.

#### [OBS-003] Monochromization (FR-8 `currentColor`) undercuts the G-1=B "real marks everywhere" recognition rationale

- **Lens**: Incorrectness (assumption tension)
- **Affected section**: FR-8, §7 (F-18), G-1=B rationale
- **Suggestion**: The ADR already flags (F-18) that `currentColor` collapses multi-hue marks to one color. Worth stating more sharply: the operator chose G-1=B ("real marks everywhere") *for maximum recognition*, but FR-8's `currentColor` theming **removes the color that carries recognition** for brands like Google, Slack, Discord, WhatsApp. A monochrome LINE logo is both (a) the recoloring its guidelines forbid *and* (b) less recognizable than the operator's stated goal wants. This means G-1=B's recognition benefit and FR-8's theming requirement are partly self-cancelling. Plan-spec's "which marks survive monochromization" note (F-18) should be elevated to decide whether the highest-recognition brands warrant a 2-tone exception — otherwise the legal risk of G-1=B is incurred without the full recognition payoff that justified it.

---

## Structural Integrity (Variant B: Structured Spec)

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | PARTIAL | FR-10/FR-11/FR-12 have explicit acceptance criteria (good, added R1/R2). FR-1/FR-3/FR-4/FR-5 lack them (MAJ-002, MIN-003). No overall Definition of Done (MIN-004). |
| Cross-references are consistent | PARTIAL | `catalogVisible` vs `catalogExcluded` naming split (MIN-005); FR-6 "literal host" vs Azure template value (MAJ-005). Otherwise §1↔§6↔§9 cite-chain is tight and re-verified. |
| Scope boundaries are explicit | PASS | §1 blast radius, §7 "Scope creep" guard (no routing/probe changes), Providers/Channels split all well-bounded. |
| Success criteria are measurable | PARTIAL | Per-FR acceptance for the security/legal FRs is measurable; the UX FRs and overall done-state are not (MIN-004). |
| Error/failure scenarios addressed | PARTIAL | Login `/state`-throw (R2-06), unknown logoSlug (FR-11), unknown provider id (G-4) all covered. Missing: known-but-excluded id migration (MAJ-004), invalid-key/disabled state (MAJ-002). |
| Dependencies between requirements identified | PASS | ADR-029 hard dependency (F-10) explicitly gates the Channels half; G-2↔G-3 interlock documented. The one missed dependency is the probe-enum invariant (MAJ-001). |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Requirement |
|----------|----------------|---------------------|
| Contract invariant re-homing | Deleting `AVAILABLE_PROVIDERS` orphans the probe-enum ⊆ guard; the new drift-guard checks `knownProtocols`, not the `ProbeProviderRequest` enum | MAJ-001 / §6 G-2 |
| Migration fixtures | No test that a configured known-but-catalog-excluded provider (`litellm`/`ollama`/`vllm`) survives the redesign | MAJ-004 / G-4 |
| Consistency (blocking) | §9 "consistency test" is not marked blocking; under the F-07 label-split it's the *only* drift guard | MAJ-003 / NFR-1 |
| `endpointHint` completeness | §9 correctness test pins values against source but doesn't assert non-empty or flag templates | MAJ-005 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Provider config ids (migration) | Known-but-excluded (`litellm`, `ollama`), region-suffixed, raw alias (`z.ai`) | Fixture per class asserting visible + editable + correct group |
| `endpointHint` values | Template (`<resource>.openai.azure.com`), localhost (`localhost:11434`), empty | Assert render handling per class; assert none silently empty |
| logoSlug | `__removed__` / unknown (already covered by FR-11), strict-TM brand present | FR-11 test exists; add a render test that the disclaimer string is in the DOM on each mark-bearing screen (FR-10 acceptance) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `GET /providers/catalog` (if live) | ok | ok | ok | risk | ok | ok | Pre-auth fingerprinting (F-08, accepted low). OBS-001: the `go:embed` variant removes this surface entirely — prefer it. |
| `<BrandIcon>` inline SVG | ok | risk | ok | ok | ok | ok | Inline-SVG XSS sink; FR-12 allow-list sanitizer mitigates. MIN-001: DOMPurify (already in tree) can do this at build time. |
| Catalog payload (secrets) | ok | ok | ok | ok | ok | ok | ADR requires secret-free payload + contract-test assertion (F-08). Adequately specified. |
| Migration reverse-lookup | ok | ok | ok | ok | risk | ok | A crash on an unrecognized id would DoS the Settings screen; ADR requires "never crash" fallback but MAJ-004's known-excluded branch is unspecified. |

**Legend**: risk = identified threat not fully closed in spec, ok = adequately addressed or not applicable.

---

## Unasked Questions

1. When `AVAILABLE_PROVIDERS` is deleted, what happens to the probe-enum ⊆ invariant and the per-company count tests in `-onboarding.test.tsx:1029-1074`? Which assertions move to the backend, which are dropped? (MAJ-001)
2. Is "configured" defined as "has a persisted config entry" or "has a *validated* key"? Where does a configured-but-invalid-key provider render? (MAJ-002)
3. If `label`/`subtitle` are derived frontend-side (the F-07 refinement), what stops the two G-3=C pickers from importing different derivations — i.e., does the F-07 split reopen the drift G-3=C claims to close? (MAJ-003)
4. Where does a configured self-hosted provider (`litellm`/`ollama`/`vllm`) — a valid `knownProtocol` deliberately absent from the curated catalog — appear in the redesigned Settings? (MAJ-004)
5. Does FR-6's "literal host" language survive the Azure `<resource>.openai.azure.com` template and localhost hints, or does the copy need softening? (MAJ-005)
6. Does G-2=B actually require an OpenAPI `contracts/` schema, or does "backend-owned SoT + generated types" suffice via a `go:embed` artifact (no runtime endpoint, no wire schema for display-only data)? (OBS-001, OBS-002)
7. With FR-8 `currentColor` monochromization, do the highest-recognition color-carrying brands (Google/Slack/Discord/WhatsApp) still deliver the recognition that justified paying the G-1=B legal risk? (OBS-003)

---

## Verdict Rationale

**REVISE.** No CRITICAL findings — the ADR's decided forks are sound, the R2 derivation fixes are code-accurate (I re-verified `factory_provider.go:508/550/578`, `_app.tsx` ordering, the absent feature-flag infra, and `PLAN_LABELS`), and the security/legal FRs (10/11/12) now carry real acceptance criteria. The verdict is REVISE because five MAJORs would each re-open at plan-spec: the deleted-`AVAILABLE_PROVIDERS` invariant (MAJ-001) is a genuine cross-boundary safety guard both prior rounds missed; MAJ-002/MAJ-004 are missing acceptance criteria for states real operators will hit (invalid keys, self-hosted providers); MAJ-003 exposes that the ADR's own preferred F-07 label-split partially undermines its G-3=C drift-safety argument; and MAJ-005 is a live contradiction between FR-6's "literal host" and the Azure template value. None block the direction — they block a clean plan-spec.

The Providers/Channels split (R2-11) is the right call and lets the clean Providers half proceed once these are closed; the Channels half remains correctly gated on ADR-029 (still `Proposed`, 6 open MAJORs). Address MAJ-001 through MAJ-005 in a §11-style R3 revision, then this is PASS-able.

### Recommended Next Actions

- [ ] MAJ-001: Add the third drift-guard property (catalog id ⊆ `ProbeProviderRequest` enum) and state the retirement plan for `-onboarding.test.tsx:1029-1074`.
- [ ] MAJ-002: Define "configured" precisely; specify where invalid-key/disabled/zero-model entries render.
- [ ] MAJ-003: Resolve the F-07-split ↔ G-3=C interaction; make the NFR-1 consistency test blocking and single-sourced.
- [ ] MAJ-004: Add a G-4 branch for known-but-catalog-excluded ids (self-hosted providers) + a migration fixture.
- [ ] MAJ-005: Soften FR-6 "literal host" for template/localhost hints; assert `endpointHint` non-empty in the §9 test.
- [ ] MIN-001..005 / OBS-001..003: fold into the R3 revision or defer explicitly.
