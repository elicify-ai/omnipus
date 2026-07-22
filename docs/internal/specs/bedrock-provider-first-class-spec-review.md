# Adversarial Review (revision 3): Bedrock as a first-class LLM provider

**Spec reviewed**: `docs/internal/specs/bedrock-provider-first-class-spec.md` (revision 3)
**Prior reviews**: `bedrock-provider-first-class-spec-review.md` (revision 1, verdict **BLOCK**; revision 2 in the same file, verdict **BLOCK**)
**Review date**: 2026-07-21
**Verdict**: **BLOCK** (revision 3 substantially resolves the three pass-2 CRITICALs the operator asked about — CRIT-N1/N2/N3 are addressed at the mechanism level — but introduces **one NEW CRITICAL** that defeats the central user story, plus three MAJORs and four MINORs)

## Executive summary

Revision 3 is the strongest revision so far. The operator's six verification questions get
these direct answers:

1. **CRIT-N1 (wrong SDK service)** — **RESOLVED.** FR-003 + FR-021 now correctly name the
   control-plane `bedrock` service's `ListFoundationModels` (verified: `go.mod` today has only
   `…/service/bedrockruntime`, the spec's new `…/service/bedrock` dep is a genuine addition).
   IAM `bedrock:ListFoundationModels` is named in FR-003 and in "Integration Boundaries". The
   prior "no new supply-chain surface" framing is explicitly corrected in FR-021 ("a **new**
   module in the same SDK family"). SC-009 + Test #36 verify the dep is present and at latest.
2. **CRIT-N2 (CODEOWNERS can't gate PR-body content)** — **PARTIALLY RESOLVED** (MAJ-N9). The
   CI-status-check-grepping-PR-body mechanism IS feasible (`github.event.pull_request.body` is
   readable in a `pull_request` workflow, and `pr.yml` already runs on every PR), so the
   infeasibility that made rev-2's SC-006 a CRITICAL is gone. But the spec stops at "a CI
   status check greps the PR body" — it names no workflow file, no branch-protection
   configuration step (a NEW required check needs admin UI/API work), and no exemption for
   non-functional changes (a one-line comment fix in `pkg/providers/bedrock/` would trigger
   the real-AWS recording requirement). Feasible, under-specified.
3. **CRIT-N3 (Go string switches aren't exhaustive)** — **LARGELY RESOLVED** (MIN-N4). Typed
   `Archetype` enum + `ResolveArchetype(string)(Archetype,error)` + exhaustive switch IS the
   right Go idiom and DOES deliver HC #6 at the dispatch site (unknown strings rejected before
   the switch; the switch on a typed enum is exhaustiveness-checkable). One residual: the repo's
   golangci config sets `exhaustive.default-signifies-exhaustive: true`, which means a `default:`
   case on the typed switch would also satisfy the linter — the spec forbids "raw-string switch"
   but doesn't explicitly forbid `default:` on the typed switch, so "MUST pass the exhaustive
   linter" is a floor not a ceiling. A reviewer catches it; the spec's enforcement claim is
   mildly overstated.
4. **FR-021/SC-009 (control-plane dep + SDK bump) vs lite decision** — **RESOLVED with a small
   gap.** Once the `bedrock` tag is removed (FR-011), `provider_bedrock.go` compiles
   unconditionally, and its new `…/service/bedrock` import rides into every build — lite
   included (Makefile line 22 confirms `lite` only excludes whatsmeow; it has no bedrock gate).
   So yes, lite pulls the control-plane module. FR-011's "Bedrock stays in lite" decision is
   internally consistent with this. SC-004 commits to re-measuring the delta "at implementation",
   but commits only to the full-build number — the **lite baseline** is not explicitly committed
   (residual from rev-2 MAJ-N2, downgraded to OBS-N4).
5. **MAJ-N5 (api_key endpoint-change SSRF hole)** — **RESOLVED** (MIN-N7). FR-016 now says
   "Covers `api_key` providers' endpoint changes too"; FR-017 says SSRF runs "regardless of
   archetype or api_key presence"; BDD "An api_key provider's endpoint change is SSRF-checked
   too" + Test #20 are added. The named hole is closed. One residual ambiguity: FR-016 says
   "MUST re-validate" for endpoint changes — for an `api_key` provider, does "re-validate" mean
   a billable `ValidateKey` re-probe using the persisted key, or just SSRF + reachability? The
   partial-update carve-out ("key omitted → unchanged, no re-probe") may collide with "endpoint
   changed → re-validate". Spec doesn't say which wins when endpoint changes but key is omitted.
6. **NEW holes from revision 3** — **One CRITICAL (CRIT-N4), one MAJOR cluster.** The live-catalogue
   flip (FR-004) is structurally broken: `providerCatalog.ts` honours the backend's
   `has_models_endpoint` boolean OVER the `LIVE_LISTING_PROVIDER_IDS` fallback, and the backend
   computes that boolean as `GetDefaultAPIBase(name) != ""` — which is `""` for Bedrock
   (verified: no `case "bedrock":` in `GetDefaultAPIBase`). The spec's "flip in
   `providerCatalog.ts`" is therefore a no-op; US-1 AS-1's "its catalogue mode is 'live'" cannot
   pass as specified. The operator flagged exactly this question ("does the spec wire
   has_models_endpoint=true on the Bedrock catalog entry?") — the answer is **no**.

| Severity | Count | Note |
|----------|-------|------|
| CRITICAL | 1 | NEW in this pass (CRIT-N4) |
| MAJOR | 3 | 2 NEW (N9, N10/N11), 1 carried-over inadequately-resolved cluster |
| MINOR | 4 | all NEW |
| OBSERVATION | 1 | residual from rev-2 MAJ-N2 |
| **Total** | **9** | down from 17 in rev 2 |

---

## Findings

### CRITICAL findings (NEW in this pass)

#### [CRIT-N4] FR-004's "flip Bedrock to 'live' in providerCatalog.ts" is a no-op — the backend emits `has_models_endpoint: false` for Bedrock and the frontend honours that signal first

- **Lens**: Infeasibility / Inconsistency / Incorrectness
- **Affected section**: FR-004 ("flip Bedrock's catalogue mode from `'manual'` to `'live'` in `src/lib/agents/providerCatalog.ts`"); US-1 AS-1; BDD "Bedrock appears in the picker with a live model list"; SC-001 ("the model picker shows a live Bedrock list"); Test #4
- **Description**: The spec's load-bearing claim — that flipping a constant in
  `src/lib/agents/providerCatalog.ts` makes Bedrock's model picker live — is **structurally
  false** against the actual code, verified four ways:

  1. **The frontend honours the backend signal first.** `providerCatalogMode()` at
     `src/lib/agents/providerCatalog.ts` reads:
     ```ts
     if (typeof provider.has_models_endpoint === 'boolean') {
       return provider.has_models_endpoint ? 'live' : 'manual'
     }
     return LIVE_LISTING_PROVIDER_IDS.has(provider.id) ? 'live' : 'manual'
     ```
     The `LIVE_LISTING_PROVIDER_IDS` set (the only thing the spec proposes to edit) is the
     **fallback for legacy gateways that don't send the field**. When the backend sends
     `has_models_endpoint` as a boolean — which it does today for every provider — the
     fallback is **never consulted**.
  2. **The backend sends `false` for Bedrock today, and would continue to after FR-004.**
     `rest.go:5347`, `:5656`, and `:5975` all compute the field identically:
     ```go
     hasEndpoint := providers_pkg.GetDefaultAPIBase(name) != ""
     ```
     `GetDefaultAPIBase` (verified at `factory_provider.go`) has **no `case "bedrock":`** and
     falls through to `return ""`. So for Bedrock the backend emits `has_models_endpoint: false`
     — and after FR-004 as written, it would STILL emit `false`, because FR-004 changes nothing
     in the Go code.
  3. **The existing frontend test confirms the override semantics.** `providerCatalog.test.ts`
     line 20-23 asserts: `has_models_endpoint: false → manual` (using `openrouter` with the
     signal forced to `false`). This is the exact posture Bedrock would be in post-FR-004.
  4. **Test #4 would not catch the hole.** Test #4 (`TestCatalog_BedrockPresent_LiveMode`) is a
     unit test on catalog DATA (`providers_catalog.json`), not on the runtime
     `has_models_endpoint` flow. It can pass while the live picker stays dark in the real UI.

  Net effect: an implementer follows FR-004 literally — adds `bedrock` to
  `LIVE_LISTING_PROVIDER_IDS`, or flips a constant — and **nothing changes at runtime**. The
  BDD "its catalogue mode is 'live' (models fetched from `ListFoundationModels`)" fails. SC-001
  ("the model picker shows a live Bedrock list") fails. US-1 AS-1 fails. The `ListFoundationModels`
  probe (FR-003) is wired but its output never reaches the picker, because the picker doesn't
  know to ask for it.

- **Impact**: The central user story's headline promise — "live model picker for Bedrock" — is
  undeliverable as specified. This is the feature the operator explicitly asked for ("*list
  models is the right way we need it anyway for the model selector*"). An implementer who
  follows the spec compiles clean, passes Test #4, and ships a Bedrock entry whose picker still
  demands hand-typed model slugs. The operator flagged this exact question in the task brief;
  the answer is "the spec does not wire it".
- **Recommendation**: Add a new clause to FR-004 (or a new FR-022): *"The backend's
  `has_models_endpoint` computation at `rest.go:5347`, `:5656`, and `:5975` MUST return `true`
  for `bedrock` after the `ListFoundationModels` probe is wired. The current computation
  `GetDefaultAPIBase(name) != ""` MUST be extended to consult the archetype or a
  live-list-allowlist: `hasEndpoint := GetDefaultAPIBase(name) != "" ||
  catalogEntryHasLiveModelList(name)`, where `catalogEntryHasLiveModelList` returns `true` for
  `bedrock` (and any future `aws_credential_chain` provider). The frontend
  `LIVE_LISTING_PROVIDER_IDS` edit is a legacy-gateway fallback ONLY and is insufficient on its
  own."* Add a row to the Symbols table: *"`rest.go:5347,5656,5975` hasEndpoint computation —
  **modifies** — extend to return true for Bedrock (FR-022)."* Replace Test #4's assertion to
  verify the END-TO-END signal: a new integration test `TestProviderGET_Bedrock_HasModelsEndpointTrue`
  asserts `GET /api/v1/providers` returns Bedrock with `has_models_endpoint: true` when the
  control-plane probe is available.

---

### MAJOR findings

#### [MAJ-N9] SC-006's CI gate mechanism is now feasible but materially under-specified — no workflow file, no branch-protection step, no exemption for non-functional changes

- **Lens**: Inoperability / Ambiguity
- **Affected section**: SC-006; operator focus question (2) (is the mechanism actually enforceable)
- **Description**: Rev-3 correctly replaces the infeasible CODEOWNERS mechanism with "a CI
  status check greps the PR body". The mechanism IS feasible — `github.event.pull_request.body`
  is available in any `pull_request`-triggered workflow, and `pr.yml` already runs on every PR
  (verified). So the CRITICAL-level infeasibility of rev 2 is discharged. But the spec stops at
  one sentence and leaves three operability gaps:

  1. **No workflow file named.** Rev-2's recommendation named
     `.github/workflows/bedrock-verification-gate.yml`; rev-3 drops the path. An implementer
     must invent the file name, the job name, and the status-check name — each of which must
     then match the branch-protection rule. Naming them in the spec makes the gate
     config-once.
  2. **Branch-protection configuration is unspecified.** A new status check does not become
     "required" by existing — an admin must add it to `main`'s branch protection rules via the
     GitHub UI or API. The spec says "The check is a required status check on
     `pkg/providers/bedrock/`-touching PRs" but doesn't say WHO configures this or WHEN. If the
     workflow ships without the branch-protection update, the gate is advisory — the exact
     "stated requirement without working enforcement" posture rev-1's CRIT-008 rejected.
  3. **No exemption for non-functional changes.** The trigger is "diff touches
     `pkg/providers/bedrock/**`". A doc-comment fix, a test-only refactor, or a `gofmt` sweep
     would trigger the real-AWS recording requirement, which is impossible for a drive-by
     contributor to satisfy. The spec needs either a path filter (`pkg/providers/bedrock/*.go`
     excluding `*_test.go` and doc comments) or an explicit "non-functional changes are exempt"
     clause with the exemption mechanism (e.g., a `[skip-verification]` commit-subject tag that
     the gate honours, with audit logging).

- **Impact**: Feasible-but-under-specified means the first implementer makes three judgement
  calls (file name, branch-protection step, exemption) that the spec doesn't anchor. The
  branch-protection step in particular is the difference between "required check" and
  "advisory check" — the operator's "real enforcement" requirement can silently degrade to the
  rev-1 posture if step 2 is skipped.
- **Recommendation**: Concretise SC-006: *"A workflow at `.github/workflows/bedrock-verification-gate.yml`
  runs on `pull_request` when the diff touches `pkg/providers/bedrock/*.go` (excluding
  `*_test.go`). It reads `github.event.pull_request.body`, asserts (a) a `## Real-AWS
  verification` heading exists and (b) the heading is followed by a link to
  `docs/internal/verification/bedrock-real-aws-*.md`, and (c) that file exists in the PR diff.
  The workflow registers a `bedrock-verification-gate` status check. Branch protection on
  `main` MUST be updated to require this check — this is a one-time admin step documented in
  the PR description and verified by the operator before merge."* Add a row to the TDD plan:
  `TestBedrockVerificationGate_ParsesPRBody` — a meta-test asserting the workflow logic.

---

#### [MAJ-N10] FR-018's choice to add `AuthKind` to `ValidateInput` reopens MAJ-N6 — "literally untouched" is true at the function-body level only, not the input-contract level

- **Lens**: Inconsistency / Ambiguity
- **Affected section**: FR-018 ("dispatching on a typed `AuthKind` field of `ValidateInput`"); FR-002 ("`ValidateKey` MUST remain literally untouched"); Test #6 (`TestValidateKey_UnchangedSignature`)
- **Description**: Rev-2's MAJ-N6 flagged that "ValidateKey stays literally untouched" is
  ambiguous at the `ValidateInput` boundary, and recommended Path B: a **separate**
  `ValidateProviderInput` struct that embeds `ValidateInput` and adds `AuthKind`, so
  `ValidateKey`'s input type is genuinely unchanged. Rev-3's FR-018 explicitly chooses Path A:
  *"dispatching on a typed `AuthKind` field of `ValidateInput`"* — i.e., the existing struct
  that `ValidateKey` receives (`validate.go:483`, `ValidateKey(ctx, ValidateInput, URLChecker)`)
  gains a new `AuthKind` field.

  The body-level claim is true: `ValidateKey`'s implementation doesn't read `AuthKind`, so its
  body is byte-identical. But the pass-2 critique stands at the input-contract level:
  `ValidateInput` is constructed at every call site (verified at `rest.go:5542`, onboarding
  paths, and throughout `validate_test.go` with struct literals). Every test that asserts the
  struct's shape, and every future caller that must decide what to put in `AuthKind`, now
  interacts with a changed surface. "Literally untouched" is defensible but narrower than the
  spec's wording suggests; the spec nowhere acknowledges that Path A was chosen over the
  rev-2-recommended Path B, nor why the trade-off is acceptable.

- **Impact**: Test #6's "literally untouched" claim is overstated for an implementer who reads
  it holistically (signature unchanged, body unchanged, BUT input struct shape changed). The
  residual ambiguity is small — Go struct literals with unset fields remain compilable — but
  the spec's framing doesn't match the mechanism it chose, and a reviewer checking "is
  `ValidateKey` provably untouched" gets a different answer depending on how they read
  "untouched".
- **Recommendation**: Either (a) commit to Path B in FR-018 ("`ValidateInput` is unchanged; the
  new `ValidateProvider(ctx, ValidateProviderInput, URLChecker)` takes a separate struct that
  embeds `ValidateInput` and adds `AuthKind`; `ValidateKey`'s signature, body, and input type
  are all literally unchanged") — this is the cleaner version of the claim; or (b) keep Path A
  but narrow the claim: *"FR-002's 'literally untouched' applies to `ValidateKey`'s function
  body and signature; the `ValidateInput` struct gains an `AuthKind` field that `ValidateKey`
  ignores. Call-site struct literals remain source-compatible (unset fields zero-value). The
  golden file (Test #5) proves observable behaviour is preserved."* Pick one and state it
  explicitly.

---

#### [MAJ-N11] FR-016 endpoint-change re-validation for `api_key` providers collides with the partial-update carve-out — billable re-probe semantics with persisted key are unspecified

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: FR-016 ("For **any** archetype, the system MUST re-validate when the endpoint or region changes"); Boundary ("partial update skips probe"); MAJ-N5 closure claim
- **Description**: FR-016's MAJ-N5 closure extends re-validation to `api_key` providers'
  endpoint changes — correct in intent (closes the unreachable-endpoint hole). But the spec's
  Boundary clause separately says: *"When an existing `api_key` provider is partially updated
  (model/label only, key omitted), behavior is unchanged: no re-probe."* The two collide for
  the case the operator's question (5) actually asked about: **endpoint changed, key omitted**.
  Does "re-validate" mean:
  - (a) A full `ValidateKey` re-probe using the **persisted** key against the new endpoint? This
    is a billable call the operator didn't explicitly authorise on a save where they omitted the
    key — and it requires the handler to re-read the persisted key ref, which the current
    `if keyChanged` block never does.
  - (b) SSRF-only on the new endpoint, no probe? Then "re-validate" is a misnomer and the
    unreachability hole (endpoint typo accepted silently) persists — the exact hole MAJ-N5 was
    supposed to close.
  - (c) Reject the save with "re-send the key to change the endpoint"? This is the safest but
    breaks the partial-update UX the carve-out was written to preserve.

  The spec doesn't pick. An implementer chooses one silently; the three produce materially
  different operator experiences and different golden-file outputs.

- **Impact**: The named MAJ-N5 hole is "closed" at the BDD level (the SSRF BDD passes), but the
  deeper question — what "re-validate" means for an api_key endpoint change — is unresolved.
  Option (a) surprises the operator with a billable call; (b) ships the unreachability hole
  anyway; (c) breaks partial-update. None is wrong, but the spec must pick.
- **Recommendation**: Add a clause to FR-016 naming the chosen behaviour: *"For an `api_key`
  provider whose `endpoint` changes on a PUT where `api_key` is omitted, the system MUST
  re-probe using the **persisted** key against the new endpoint (treating the endpoint change
  as configuration-equivalent to a region change on a keyless archetype). The partial-update
  skip applies ONLY to model/label changes — NOT to endpoint. The handler MUST re-resolve the
  persisted api_key_ref for this path. A new row in the api_key golden file (Test #25) asserts
  the billable re-probe fires and its outcome."* (This is option (a); (b) or (c) are also
  acceptable but must be named.)

---

### MINOR findings

#### [MIN-N4] `exhaustive` linter is configured with `default-signifies-exhaustive: true` — "MUST pass the exhaustive linter" is a floor, not a ceiling, for HC #6

- **Lens**: Insecurity (HC #6)
- **Affected section**: FR-001; Explicit Non-Behaviors ("Must **not** use a raw-string `switch`"); operator focus question (3)
- **Description**: Rev-3's typed-enum + `ResolveArchetype` + exhaustive-linter approach is the
  right idiom and DOES deliver HC #6 at the dispatch site — the core CRIT-N3 defect is closed.
  Residual: `.golangci.yaml:67-68` sets `exhaustive.default-signifies-exhaustive: true`, meaning
  a switch with a `default:` case is treated as exhaustive (no diagnostic). So an implementer
  who writes:
  ```go
  switch arch {
  case ArchAPIKey: return validateAPIKey(...)
  default: return validateAWSChain(...) // silently covers AWSChain + None + future values
  }
  ```
  passes the linter. The spec's Explicit Non-Behavior forbids "raw-string `switch`" but does
  not explicitly forbid a `default:` case on the typed-enum switch. A reviewer catches it, but
  the spec's enforcement claim ("MUST pass the `exhaustive` linter") overstates the guarantee
  the linter actually provides under this config.
- **Recommendation**: Tighten the Explicit Non-Behavior to: *"Must not use a raw-string
  `switch` for archetype dispatch, AND must not include a `default:` case in the typed-enum
  dispatch switch — every `Archetype` value MUST have an explicit `case`. The `exhaustive`
  linter (without relying on its `default-signifies-exhaustive` escape) MUST pass."* Test #2
  (`TestArchetype_Dispatch_ExhaustiveLinter`) should additionally assert no `default:` keyword
  appears in the dispatch switch's source span.

---

#### [MIN-N5] FR-020's repair wording is easy to misread — catalog-id→table consultation is implied but not explicit

- **Lens**: Ambiguity / HC #6
- **Affected section**: FR-020 ("backfills `auth_kind: api_key` **only** for persisted entries that have a stored api_key credential"); operator focus question (3) (is the config-load repair HC #6-safe)
- **Description**: Under adversarial reading, FR-020's "backfills `auth_kind: api_key` **only**
  for persisted entries that have a stored api_key credential" reads as if stored-key evidence
  is the SOLE backfill path. But FR-020 also says "entries with no stored key and no mapping are
  left unset" — the phrase "no mapping" implies a mapping IS consulted for some entries. The
  mapping is the id→archetype table from FR-001 (`api_key` for the 22 API-key providers, `none`
  for ollama, `aws_credential_chain` for bedrock). So the intended three-branch logic is:
  (1) catalog id → backfill from table; (2) non-catalog id with stored key → backfill `api_key`;
  (3) non-catalog id without stored key → WARN + unset + reject at save. This IS HC #6-safe —
  every branch is evidence-based (catalog mapping or stored credential), not a code default.
  But the wording buries branch (1); an implementer could skip the catalog-table consultation
  for catalog-keyless ids (Ollama) and leave them unset, forcing the operator to re-save every
  Ollama entry after upgrade.
- **Recommendation**: Restructure FR-020 into an explicit three-branch list: *"Config-load
  repair, one-shot and idempotent: (1) if the entry's id is in the FR-001 id→archetype table,
  backfill `auth_kind` from the table; (2) else if the entry has a stored api_key credential,
  backfill `auth_kind: api_key`; (3) else, leave `auth_kind` unset, log WARN with the id, and
  reject at next save. No branch defaults."* The current single-paragraph form is too dense to
  safely implement from.

---

#### [MIN-N6] Test #36 `TestGoMod_BedrockControlPlane_Latest` asserting "all `aws-sdk-go-v2` at latest" is fragile and infeasible as a stable test

- **Lens**: Infeasibility
- **Affected section**: Test #36; SC-009
- **Description**: Test #36 asserts "`service/bedrock` present; all `aws-sdk-go-v2` at latest".
  "At latest" is a moving target — aws-sdk-go-v2 releases weekly. A test that hardcodes a
  version goes stale within days; a test that queries the Go proxy at runtime is flaky and
  network-dependent. Neither is a stable CI gate.
- **Recommendation**: Rescope Test #36 to: *"`service/bedrock` is a direct require (not
  indirect); all `aws-sdk-go-v2/*` modules are at the same release vintage (no module is more
  than one minor behind the highest in the family)."* Drop "latest" from SC-009's wording;
  replace with "current" + a one-time PR-time verification recorded in the verification file.

---

#### [MIN-N7] Makefile:24's `bedrock` tag documentation is not updated by FR-011

- **Lens**: Inconsistency / Operability
- **Affected section**: FR-011; Symbols table (Makefile not listed)
- **Description**: `Makefile:21-24` documents three discretionary tags, including `bedrock`
  ("compiles in the real AWS Bedrock provider (stub without it)"). FR-011 removes the tag and
  makes the stub inert. The Makefile documentation line is now wrong and will mislead the next
  reader — the exact "looks like dead code" hazard ADR-053 was written to prevent. The spec's
  Symbols table doesn't list the Makefile; FR-011 doesn't mention updating it.
- **Recommendation**: Add to FR-011: *"Makefile:24's `bedrock` tag documentation line MUST be
  deleted (or replaced with `# bedrock — inert, kept for stale-CLI compatibility; the provider
  is always compiled in)."* Add a Symbols-table row: *"`Makefile:21-24` — **modifies** — drop
  the bedrock tag note."*

---

### Observations

#### [OBS-N4] SC-004's re-measurement commitment covers the full build but doesn't explicitly commit to the lite baseline — residual from rev-2 MAJ-N2

- **Lens**: Inoperability
- **Affected section**: SC-004; FR-011; operator focus question (4) (lite consistency)
- **Suggestion**: The operator asked whether lite pulls the control-plane module (answer: yes,
  unconditionally, once the tag is removed). SC-004 commits to re-measuring "the size increase
  (tag removal + control-plane module)" but the target "≤ ~6 MB" is framed against the full
  build baseline. The lite baseline is smaller (drops ~58 MB of whatsmeow), so the same delta
  is a larger percentage of lite — the exact concern rev-2's MAJ-N2 raised. Suggest adding to
  SC-004: *"…and the lite-build delta is measured against the lite baseline and documented
  separately. The lite decision (FR-011) is re-confirmed or flipped based on the lite-baseline
  number, not the full-build number."*

---

## Rev-1 + rev-2 resolution map — verification against actual code (revision 3)

The operator's task brief asked for each prior finding to be verified against the code. Results
for the items the operator specifically flagged this pass:

| Prior finding | Rev-3 claim | Verified against code | Result |
|---|---|---|---|
| P2 CRIT-N1 (wrong SDK service) | FR-003 + FR-021: control-plane `bedrock` `ListFoundationModels`, new dep, IAM scope | `go.mod` has only `bedrockruntime` (verified); FR-021's new `…/service/bedrock` is a real addition; FR-003 names the right service + IAM scope; "no new supply-chain surface" corrected in FR-021 | **RESOLVED** |
| P2 CRIT-N2 (CODEOWNERS can't enforce PR content) | SC-006: CI status check greps PR body | `github.event.pull_request.body` IS readable in `pull_request` workflows; `pr.yml` already runs on every PR — mechanism is feasible | **PARTIALLY RESOLVED** — see MAJ-N9 (under-specified) |
| P2 CRIT-N3 (Go string switches not exhaustive) | FR-001: typed `Archetype` enum + `ResolveArchetype` error-on-unknown + exhaustive linter | `.golangci.yaml:35` has `exhaustive`; typed-enum switch IS the right idiom; `ResolveArchetype` rejects unknown before the switch | **LARGELY RESOLVED** — see MIN-N4 (`default-signifies-exhaustive: true`) |
| P2 MAJ-N1 (custom-id repair) | FR-020: backfill `api_key` only with stored key; WARN + unset otherwise | Three-branch logic (catalog-table / stored-key / WARN) is HC #6-safe when read generously | **RESOLVED** — see MIN-N5 (wording) |
| P2 MAJ-N2 (lite baseline undercounts) | SC-004: re-measured at impl, target ≤ ~6 MB | Lite pulls the module (verified: Makefile `lite` has no bedrock gate); lite-baseline number not explicitly committed | **PARTIALLY RESOLVED** — see OBS-N4 |
| P2 MAJ-N4 (contract test ½ coverage) | Test #31 expanded to all 5 schemas + atomic-commit gate | Test #31 description now covers all 5 + Test #32 staleness gate added | **RESOLVED** |
| P2 MAJ-N5 (api_key endpoint SSRF) | FR-016/FR-017 cover all archetypes | FR-016 says "Covers `api_key` providers' endpoint changes too"; FR-017 archetype-agnostic; BDD + Test #20 added | **RESOLVED** — see MAJ-N11 (re-probe semantics) |
| P2 MAJ-N6 (ValidateInput boundary) | FR-018: `AuthKind` field on `ValidateInput` | Path A chosen (adds field to existing struct); Path B (separate struct) not adopted | **PARTIALLY RESOLVED** — see MAJ-N10 |
| P2 MAJ-N8 (golden-file breadth) | Test #5 still "representative api_key provider" (singular); Test #25 covers integration regression | Single-provider golden still narrow; anthropic-header / openrouter-extraBody variants not named | **PARTIALLY RESOLVED** (carry-over; not re-litigated at CRITICAL — the integration Test #25 covers more ground) |

**Score**: 5 resolved, 4 partially resolved. The partially-resolved items are all MAJOR-or-below
this pass — no CRITICAL carries over from rev 2.

---

## Operator focus questions — direct answers (revision 3)

1. **CRIT-N1 — probe names control-plane `ListFoundationModels`, go.mod change specified, IAM
   scope noted, "no new supply-chain surface" corrected?** — **Yes, all four.** FR-003 correctly
   names `bedrock ListFoundationModels` (NOT `bedrockruntime`); FR-021 + Symbols table specify
   the `go.mod` change; IAM `bedrock:ListFoundationModels` is in FR-003 + Integration Boundaries;
   FR-021 explicitly retracts the prior "no new surface" framing ("a **new** module in the same
   SDK family"). Verified `go.mod` today has no `…/service/bedrock` — the spec's addition is real.

2. **CRIT-N2 — SC-006 uses CI PR-body grep, not CODEOWNERS; is it actually enforceable?** —
   **Mechanism yes, specification no.** The CI-status-check-grepping-PR-body approach is
   feasible (`github.event.pull_request.body`, `pr.yml` trigger). But SC-006 names no workflow
   file, no branch-protection configuration step, and no non-functional-change exemption — see
   MAJ-N9. Feasible but under-specified; an implementer could ship it advisory-only.

3. **CRIT-N3 — typed Archetype enum + ResolveArchetype + exhaustive linter; does it deliver HC #6?**
   — **Yes, at the dispatch site, with one residual.** Typed-enum switch with `ResolveArchetype`
   erroring-on-unknown is the right Go idiom and structurally delivers HC #6 (unknown strings
   rejected before the switch; switch exhaustiveness is linter-checkable). Residual: the repo's
   `exhaustive.default-signifies-exhaustive: true` config means a `default:` case would also
   satisfy the linter — see MIN-N4. The spec should explicitly forbid `default:` on the typed
   switch.

4. **FR-021/SC-009 — control-plane dep + SDK bump complete and consistent with lite?** — **Yes,
   with one small gap.** Once the tag is removed (FR-011), Bedrock compiles unconditionally,
   pulling the control-plane module into every build — lite included (verified: `Makefile` `lite`
   has no bedrock gate; line 22 confirms lite excludes only whatsmeow). So lite DOES pull the
   control-plane module. FR-011's "Bedrock stays in lite" is consistent. Gap: SC-004 commits to
   re-measuring but doesn't explicitly commit to the **lite baseline** number — see OBS-N4.

5. **MAJ-N5 — do FR-016/FR-017 cover api_key providers' endpoint changes (the SSRF/endpoint
   hole)?** — **Yes at the SSRF level; ambiguous at the re-probe level.** FR-016 explicitly says
   "Covers `api_key` providers' endpoint changes too"; FR-017 is archetype-agnostic; BDD + Test #20
   are added. The named SSRF hole is closed. But "MUST re-validate" for an api_key endpoint
   change (key omitted) doesn't specify whether a billable re-probe fires using the persisted
   key — see MAJ-N11. Pick one behaviour and name it.

6. **NEW holes from revision 3?** — **One CRITICAL (CRIT-N4), plus the MAJ cluster.** CRIT-N4:
   the live-catalogue flip (FR-004) is structurally broken — `providerCatalog.ts` honours the
   backend's `has_models_endpoint` boolean over the `LIVE_LISTING_PROVIDER_IDS` fallback
   (verified in code + existing test), and the backend computes that boolean as
   `GetDefaultAPIBase(name) != ""` which returns `""` for Bedrock (verified: no `case "bedrock":`
   in `GetDefaultAPIBase`). The spec's "flip in `providerCatalog.ts`" is a no-op; US-1 AS-1's
   "live model list" cannot pass. The operator's task brief flagged this exact question
   ("does the spec wire has_models_endpoint=true on the Bedrock catalog entry?") — the answer
   is **no**. `ListFoundationModels` itself does NOT open a new SSRF/auth surface beyond what
   FR-017 already covers (SSRF check runs before the probe; SigV4-signed-request-to-attacker
   mitigated by FR-017). FR-020's config-load repair IS HC #6-safe under generous reading
   (three-branch evidence-based logic), with a wording-clarity gap (MIN-N5).

---

## Structural integrity (plan-spec mode)

| Check | Result | Notes |
|---|---|---|
| Every user story has acceptance scenarios | PASS | US-1..US-4 intact. |
| Every acceptance scenario has BDD scenarios | PASS | All AS trace to ≥1 BDD. |
| Every BDD scenario has `Traces to:` | PASS | Back-references intact. |
| Every BDD scenario has a test in TDD plan | **FAIL** | BDD "Bedrock appears in the picker with a live model list" traces to Test #4 only, which is a catalog-data unit test — it cannot detect the CRIT-N4 runtime `has_models_endpoint: false` hole. The BDD's "live model list" claim is untested at the integration level. |
| Every FR appears in traceability matrix | PASS | FR-001..FR-021 present (FR-014 deferral noted). |
| Every BDD scenario in traceability matrix | PASS | Matrix completeness check holds. |
| Test datasets cover boundaries/edges/errors | PASS | Region + auth_kind + api-key datasets are comprehensive. |
| Regression impact addressed | PASS | Regression table is strong. |
| Success criteria measurable | **FAIL** | SC-001 ("the model picker shows a live Bedrock list") is not deliverable as specified — CRIT-N4. SC-006's enforcement is under-specified — MAJ-N9. |

---

## Test coverage assessment

| Category | Gap | Affected |
|---|---|---|
| Backend `has_models_endpoint` population | No test asserts the backend returns `has_models_endpoint: true` for Bedrock post-probe-wiring (CRIT-N4). Test #4 is catalog-data only. | US-1 AS-1 / SC-001 |
| SC-006 enforcement workflow | No test or CI check for the PR-description gate's workflow logic (MAJ-N9). | SC-006 |
| api_key endpoint-change re-probe | No test specifies whether a billable re-probe fires on api_key endpoint change with key omitted (MAJ-N11). | FR-016 |
| Exhaustive-switch `default:` ban | No test asserts the dispatch switch has no `default:` keyword (MIN-N4). | FR-001 / HC #6 |
| FR-020 three-branch repair | Test #33 + #34 cover backfill + custom-rejection, but not the catalog-id→table branch for keyless catalog ids (Ollama) explicitly (MIN-N5). | FR-020 |

---

## STRIDE threat summary (revision-3 delta only)

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| `bedrock` control-plane client construction (NEW per FR-021) | ok | ok | ok | ok | ok | ok | FR-010's redacting sink + FR-017's SSRF-before-probe cover the new client; SigV4-to-attacker mitigated by FR-017 ordering. No new SSRF/auth surface (operator Q6). |
| Archetype dispatch (FR-001, typed enum) | ok | ok | ok | ok | ok | ok | Typed enum + `ResolveArchetype` error-on-unknown closes the rev-2 CRIT-N3 fail-open. Residual MIN-N4: linter config permits `default:`. |
| Config-load repair (FR-020, three-branch) | ok | ok | ok | ok | ok | ok | HC #6-safe under generous reading (evidence-based branches); MIN-N5 wording risk of skipping catalog-table consultation. |
| PR-description gate (SC-006, CI grep) | ok | ok | risk | ok | ok | ok | R (repudiation): feasible but under-specified; without the branch-protection config step the gate is advisory and a missing recording merges undetected (MAJ-N9). |
| Live-catalogue flip (FR-004, frontend-only) | ok | ok | ok | ok | ok | ok | Not a security threat — a functional defect (CRIT-N4). Picker stays manual; no data leaks. |

---

## Unasked questions (revision 3)

1. **(CRIT-N4)** The spec says "flip in `providerCatalog.ts`". But `providerCatalogMode()`
   honours the backend's `has_models_endpoint` boolean OVER the frontend fallback set. What
   changes the backend's `hasEndpoint` computation (currently `GetDefaultAPIBase(name) != ""`,
   which is false for Bedrock) so that Bedrock emits `has_models_endpoint: true`?
2. **(MAJ-N9)** Who configures the branch-protection rule that makes the new status check
   required on `main`, and when? Without that step the gate is advisory.
3. **(MAJ-N10)** Why was Path A (`AuthKind` on `ValidateInput`) chosen over Path B (separate
   `ValidateProviderInput`)? The choice is defensible but unacknowledged; FR-002's "literally
   untouched" is true only at body level.
4. **(MAJ-N11)** For an `api_key` provider whose endpoint changes on a PUT with the key omitted,
   does "re-validate" mean a billable re-probe with the persisted key, SSRF-only, or reject?
5. **(MIN-N4)** Should the spec explicitly forbid `default:` on the typed-enum dispatch switch,
   given the repo's `exhaustive.default-signifies-exhaustive: true` config weakens the linter
   guarantee?
6. **(OBS-N4)** Will the lite-baseline binary delta be measured and documented separately, or
   only the full-build delta?

---

## Verdict rationale

**Verdict: BLOCK.** Revision 3 made genuine, verifiable progress on the three pass-2 CRITICALs
the operator named. CRIT-N1 (wrong SDK service) is fully resolved — FR-003/FR-021 correctly name
the control-plane `bedrock` service, the new dep, the IAM scope, and explicitly retract the
prior "no new surface" framing (verified: `go.mod` today has no `…/service/bedrock`). CRIT-N2
(CODEOWNERS infeasibility) is resolved at the mechanism level — a CI PR-body grep IS feasible
(verified: `github.event.pull_request.body` + `pr.yml` trigger) — but materially under-specified
(MAJ-N9). CRIT-N3 (Go string-switch impossibility) is resolved by the typed-enum + `ResolveArchetype`
+ exhaustive-linter approach, which is the right Go idiom and DOES deliver HC #6 at the dispatch
site, with one residual (MIN-N4: linter config). The operator's six verification questions get
substantively correct answers on five of six.

But revision 3 introduces **one NEW CRITICAL** that defeats the central user story:

- **CRIT-N4** sits exactly on the feature the operator explicitly asked for ("*list models is the
  right way we need it anyway for the model selector*"). The live-catalogue flip in FR-004 is
  structurally a no-op: `providerCatalog.ts` honours the backend's `has_models_endpoint` boolean
  over the `LIVE_LISTING_PROVIDER_IDS` fallback (verified in code + existing test), and the
  backend computes that boolean as `GetDefaultAPIBase(name) != ""` — which returns `""` for
  Bedrock (verified: no `case "bedrock":` in `GetDefaultAPIBase`). The spec changes nothing in
  the Go computation. The BDD "Bedrock appears in the picker with a live model list", SC-001
  ("the model picker shows a live Bedrock list"), and US-1 AS-1 all fail as specified. Test #4
  (catalog-data unit test) gives false confidence. The operator's task brief flagged this exact
  question — the answer is no, the spec does not wire it.

This is fixable in a single paragraph (add a backend `hasEndpoint` computation change to FR-004
or a new FR-022). It does not require redesign. But it cannot ride to implementation — the
headline feature is undeliverable as written, and an implementer who starts work today will ship
a Bedrock entry whose picker still demands hand-typed model slugs, contradicting the operator's
stated reason for the whole endeavour.

The good news: this is the only blocker. The MAJ cluster (MAJ-N9/N10/N11) is closable in
single-clause additions; the MINORs are wording. Once CRIT-N4 is addressed (backend
`has_models_endpoint: true` for Bedrock + an integration test that asserts it end-to-end), this
spec should pass cleanly on the next revision.

### Recommended next actions

- [ ] **CRIT-N4**: Add FR-022 (or extend FR-004): backend `hasEndpoint` computation at
      `rest.go:5347/5656/5975` MUST return `true` for `bedrock` post-probe-wiring; add Symbols
      table row; replace Test #4 with an integration test asserting the END-TO-END signal
      (`TestProviderGET_Bedrock_HasModelsEndpointTrue`).
- [ ] **MAJ-N9**: Concretise SC-006 — name the workflow file, the branch-protection config step,
      and the non-functional-change exemption; add a TDD row for the workflow logic.
- [ ] **MAJ-N10**: Either commit to Path B (separate `ValidateProviderInput`) in FR-018, or
      narrow FR-002's "literally untouched" to "body and signature; `ValidateInput` gains an
      ignored `AuthKind` field".
- [ ] **MAJ-N11**: Pick one of (billable re-probe / SSRF-only / reject) for api_key endpoint
      change with key omitted; name it in FR-016; add a golden-file row.
- [ ] **MIN-N4**: Explicitly forbid `default:` on the typed-enum dispatch switch; Test #2 asserts
      no `default:` keyword in the dispatch span.
- [ ] **MIN-N5**: Restructure FR-020 into an explicit three-branch list.
- [ ] **MIN-N6**: Rescope Test #36 to "present + same-vintage", drop "latest".
- [ ] **MIN-N7**: Add Makefile:24 documentation update to FR-011.
- [ ] **OBS-N4**: Commit to measuring the lite-baseline delta separately in SC-004.
- [ ] After revision, re-run `/grill-spec docs/internal/specs/bedrock-provider-first-class-spec.md`
      to confirm BLOCK → REVISE → PASS.
