# Grill Review — ADR-051 Rev 4 Delivery Plan

**Review target:** `docs/internal/plans/ADR-051-rev4-delivery-plan.md` (v1, 202 lines, pending Stage 2)
**Governing spec:** `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md` (1253 lines, two grill rounds applied)
**Governing ADR:** `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md`
**Reviewer mode:** adversarial (plan-as-shipped — assume this plan, executed as written, fails)
**Read-only on plan:** confirmed.
**Date:** 2026-07-22
**Evidence base:** direct Read of plan + ADR + spec + both spec grill rounds; `git rev-list --count main..sendfile-fix`; subagent inventory (`ls /home/dev/.config/opencode/agent/`); ripgrep on `Resolve`/`ResolveWithMeta`/`isImageFormatUnsupportedByGo`/`CodeToolArgs`/`CodeSchema`/`media.delete`/`MessageItem.*tabindex`; Read of `pkg/media/store.go`, `pkg/agent/loop_media.go`, `pkg/agent/media_downgrade.go`, `pkg/agent/translate_error.go`, `pkg/workspace/instructions.go`, `pkg/audit/events.go`, `pkg/providers/catalog/data/providers_catalog.json`, `src/lib/llm-error.ts`.

---

## Executive Summary

The plan is structurally sound: the wave/slice decomposition tracks the ADR's "Affected Components" table, the wave ordering (Wave 0 foundation → Wave 1 parallel core backend → Wave 2 parallel cross-cutting → Wave 3 integration → Wave 4 UAT) is correct, the per-slice FR ownership is mostly right, and the reviewer-gate loop is a credible substitute for the "7-reviewer gate" the CLAUDE.md describes for code with no PR.

**However, executed as written the plan will fail in three concrete ways that are reachable from here and must be closed before Stage 3.**

1. **Wave 1 tests vs Wave 3 T8 contract rewrite — a coordination hazard the plan ignores.** Each Wave 1 subagent is told to "write failing tests first (TDD). Use the test names from the spec's TDD plan verbatim where given" (§3). Wave 3 T8 then "rewrites `resolveMediaRefs` to call the orchestrator" and "tests in `loop_media_test.go` are **UPDATED** to assert the new observable contract." I counted 16 production `TestResolveMediaRefs_*` tests across `loop_media_test.go` (10) and `loop_test.go` (6) plus 4 `TestEncodeImageToDataURL_*` tests in `loop_media_normalization_test.go` — every one of these touches the contract T8 rewrites. Wave 1's slice F (resize-to-fit + D2 deletion) will commit a slice-local version of those tests; T8 will then rewrite them. Tests written twice, Wave 1's commits churn in T8. The plan's "no test is silently dropped" promise does not address the double-write.

2. **The 20M token budget is not credible.** The prior session exhausted 41M of 10M (per §6 risk #6); the new goal is 20M. Wave 1 alone is 4 parallel subagents × (full spec re-read ≈ 1.5M tokens + implementation ≈ 2M + tests ≈ 1M + 6-reviewer gate ≈ 6× ~500K = 3M per slice) — Wave 1 likely costs ~24M by itself, exhausting the budget before Wave 2 starts. The plan's mitigation "the work continues across sessions via the commit checkpoints on `sendfile-fix`" is honest about the restarts but ignores restart-overhead (each subagent restart re-reads spec + slice context, ~500K-1M of recurring spend).

3. **The "pre-existing branch debt" the plan commits Wave 4 T12 to fix is not actually failing on the branch.** The plan lists four items as "must close in Wave 4 per Constraint #7": (a) `pkg/providers/openai_compat` `TestProviderChat_HTMLResponsesReturnHelpfulError` (exists at `pkg/providers/openai_compat/provider_test.go:313`), (b) `pkg/tools` OOXML `TestReadFile_ExtractsDocx`/`Xlsx`/`DocumentPagination` (exist at `pkg/tools/filesystem_docextract_test.go:85,105,165`), (c) `src/lib/llm-error.ts` "hand-written wire types → migrate" (the file is **explicitly self-documented as display-only mirror types, NOT wire types — the file header reads "NOT a wire type (no `json:` tags, never crosses the gateway boundary) — display-only. Per CLAUDE.md hard-constraint #8 this is explicitly not a hand-written wire type"), (d) `MessageItem.tsx` tabindex test failure (verified the test files exist but the failure is not yet observed). **Items (a) and (b) may already pass on `main`** — their presence in the plan as "pre-existing debt" without a failing CI run URL is speculative; T12 may discover no failures and the budget is wasted, OR T12 finds real failures unrelated to these and the plan's framing is wrong. T12 must be gated on a real CI run, not a pre-planned list.

None of these are architecture problems — the spec/ADR survive intact, the wave ordering is correct, and the reviewer gate is a credible stand-in for the missing named lead agents. They are plan-level defects that are surgical to fix but will cost real cycles if executed unchanged.

**Verdict: REVISE.** Not BLOCK (the architecture is recoverable, no CRITICAL) — but the three load-bearing problems above, plus several smaller risks, must close before Stage 3.

**Counts:** 0 CRITICAL · 11 MAJOR · 7 MINOR · 5 OBSERVATION.

---

## Findings Table

| ID | Severity | Lens | Section / Anchor | One-line |
|---|---|---|---|---|
| **F-L1-1** | MAJOR | Sequencing | §2 Wave 3 T8 vs §3 prompt template | Wave 1 subagents commit slice-local tests against `resolveMediaRefs`/`encodeImageToDataURL`; Wave 3 T8 rewrites those tests against the orchestrator contract — tests written twice, Wave 1 churns in T8. |
| **F-L1-2** | MAJOR | Sequencing | §3 prompt template (Hard Constraints block) | Prompt template does not list Hard Constraints #1 (single Go binary), #3 (footprint <10MB), #4 (graceful degradation), #5 (ecosystem compatibility) — only #2 and #8; slice subagents may violate the others without knowing. |
| **F-L1-3** | MAJOR | Sequencing | §3 prompt template | Prompt does not require `go build ./cmd/omnipus` to enforce the single-binary invariant — implicit, missing, not enforced. |
| **F-L2-1** | MAJOR | Blast radius | §1 Slice B (FR-001/002/003/004/006/007/007a/008/009/033) | Slice B is listed as ONE slice but covers 4-5 distinct concerns (storage, lifecycle, explicit-delete, cascade-delete, audit); the slice table does not allocate Wave 1 T1 enough attention for a feature-area that maps to the whole media library. |
| **F-L2-2** | MAJOR | Blast radius | §1 Slice G (FR-028/028a/029/030) — 13+ call-sites, security MUST | Slice G's cross-workspace guard is a security MUST (STRIDE Spoofing) but the plan's security-lead pass is allocated only to Slice D (T6); Slice G gets no extra security review. |
| **F-L3-1** | MAJOR | Test coverage | §2 Wave 3 T8, §3 prompt | 16 production `TestResolveMediaRefs_*` + 4 `TestEncodeImageToDataURL_*` tests already exist; T8's contract rewrite touches every one, but no per-slice plan states which Wave 1 subagent updates which test — silent ownership gap. |
| **F-L3-2** | MAJOR | Test coverage | §2 Wave 3 T9 | "Add the spec's missed tests" is delegated to T9 but the spec's 45 named tests are spread across Wave 1 slices (each slice's TDD plan rows). Wave 1 subagents may commit tests in T9's scope; no ownership rule. |
| **F-L4-1** | MAJOR | Reviewer-gate | §0 (Available subagents), §4 (6-reviewer gate) | CLAUDE.md "7-reviewer gate" assumes dedicated architect/lead agents; the actual inventory has only 6 pr-review-toolkit agents + `general` (multi-purpose) substituting for all named lead roles — the holistic 7th is `general`, not a dedicated architect. Plan understates this is a downgrade. |
| **F-L5-1** | MAJOR | Risk realism | §6 Risk #6 (token budget) | 20M budget is not credible against Wave 1's parallel ×4 + per-commit 6-reviewer gate; prior session exhausted 41M of 10M. Restart overhead is not budgeted. |
| **F-L5-2** | MAJOR | Risk realism | §6 Risk #3 (pre-existing branch debt) + §1 (Pre-existing branch debt list) | The four listed items may not actually be failing on the branch: (a) OOXML tests exist in `pkg/tools/filesystem_docextract_test.go`; (b) `openai_compat` HTML tests exist at `pkg/providers/openai_compat/provider_test.go:313`; (c) `llm-error.ts` is self-documented as display-only, not a wire type. T12 must be gated on an observed CI failure, not the pre-planned list. |
| **F-L5-3** | MAJOR | Risk realism | §6 Risk #10 (stacked commits, no PR) | The plan's "open a single draft PR after each wave's commits land, OR rely on the 6-reviewer gate per-wave" is in tension with CLAUDE.md "Every PR MUST close its issues via keyword." The plan must commit to one approach: PR per wave OR explicit "no PR until Wave 4 acceptance" — currently neither. |
| **F-L6-1** | MAJOR | Acceptance gate | §5 (CI / verification), §2 Wave 4 T13 | Acceptance gate (a) "every green" requires a SPECIFIC GREEN CI run, not "we ran CI." Plan does not commit to a run URL or reproducible invocation. |
| **F-L7-1** | MINOR | Subagent prompt | §3 (template) | Template omits Hard Constraints #1/#3/#4/#5 — slice subagents may violate them silently. |
| **F-L7-2** | MINOR | Subagent prompt | §3 step 5 | "Run scoped Go tests" but does not say "gofmt" or "golangci-lint" must be green at the slice's package boundary; only "scoped lint" with no exit criterion. |
| **F-L7-3** | MINOR | Subagent prompt | §3 Wave 0 (Slice A) | Wave 0's Slice A GENERATES wire types — the "never edit generated files directly" rule applies, but the template's Wave 0 instructions are not differentiated from Wave 1+ "use generated types." |
| **F-L8-1** | MINOR | Decision logging | §7 (Open decisions) | The 5 listed open decisions omit (a) `pkg/providers/catalog` (existing, has `data/providers_catalog.json`) vs `pkg/providers/capabilities` (planned, NEW per ADR) — naming collision risk for Slice C; (b) step-5 offload writes up to 100 MB into `work/` per file (the per-workspace work/ quota is unspecified); (c) spec FR-007a's "REUSES pathStates.refCount" claim is factually false (spec grill round-2 R2-M1) — Slice B's design must declare a separate manifest refcount, not reuse. |
| **F-L8-2** | MINOR | Decision logging | §7 (Open decisions), Slice E | The plan does not commit to specific substrings for the new `CodeToolArgs`/`CodeSchema` classifier codes (FR-018), nor to a status-code-based backstop exclusion — a provider with a non-matching phrasing could re-enable strip-retry masking of non-media errors. |
| **F-L6-2** | MINOR | Acceptance gate | §2 Wave 4 T14 | "Per-SC observation log" format is unspecified — what fields, what tool-result type, what reproducibility contract. |
| **F-L2-3** | MINOR | Blast radius | §1 Slice C | Slice C "capability catalog transport" — the plan says it lives in a NEW package but does not name it explicitly; the existing `pkg/providers/catalog/data/providers_catalog.json` (provider metadata, not modalities) creates a naming collision risk for `pkg/providers/capabilities`. |
| **F-L4-2** | MINOR | Reviewer-gate | §4 step 2 | "ask the user after 3 rounds if not converged" — with 7 reviewers per round × 4 waves = 84 review rounds for the same code if convergence fails; the threshold should be per-wave, not global. |
| **F-L5-4** | OBSERVATION | Risk realism | §6 Risk #7 (retired surfaces) | Plan says "don't import from `src/components/command-center/`" — verified the directory is deleted; plan correctly notes this, but does not add a lint/test rule to enforce non-regression. |
| **F-L5-5** | OBSERVATION | Risk realism | §6 Risk #8 (LLM provider phrasings) | The "freeze gate: re-validate the seed commit message references the matrix-with-date artifact" is a process artifact, not a code/test artifact; re-validation is not scheduled in any wave. |
| **F-L4-3** | OBSERVATION | Reviewer-gate | §4 step 1 | `pr-test-analyzer` is the only test-coverage specialist in the gate but receives the same wall-time as the other 5; its job (sizing test coverage gaps) is the broadest. |
| **F-L3-3** | OBSERVATION | Test coverage | §2 Wave 3 T8 | The 6-reviewer gate is CODE review (correctness, security, simplicity) — it does not exercise behavioral correctness. T9's spec-named tests are the only behavioral surface; the plan's "no test is silently dropped" must be enforced at T9 commit time, not the gate. |
| **F-L1-4** | OBSERVATION | Sequencing | §2 Wave 2 T5 vs T6 | T5 (resolver signature) + T6 (security sanitization) — there's a hidden dependency: Slice D's FR-020a defines the copy-name sanitizer; Slice G's FR-028a adds the workspace guard. Both modify `pkg/media/library` (per the ADR Affected Components) — they should NOT race on the same package. Plan's parallel dispatch is safe IF the slices touch distinct files within the package (which they do: G touches `resolveMediaRefs` signature; D touches the new `pkg/agent/media_present.go`). Verified safe at code level; flagging as observation. |

---

## Phase 1 — Structural / Sequencing Assessment

### Wave ordering (verified)

- **Wave 0 → Wave 1** — Slice A's wire types are required by B/C/F/G/H. Correct.
- **Wave 1 → Wave 2** — Slice G/D/H depend on B's workspace library namespace. Correct.
- **Wave 2 → Wave 3** — T8's orchestrator depends on all upstream slices. Correct.
- **Wave 3 → Wave 4** — T10-T14 follow integration. Correct.

### Slice F coupling (verified at code level)

`pkg/agent/loop_media.go:464-478` — the D2 passthrough branch (`isImageFormatUnsupportedByGo` returning `data:image/avif;base64,…`) lives in the SAME function (`encodeImageToDataURL`) as the resize-to-fit path the plan's Slice F owns. The plan's co-travel ("FR-016 deletion + FR-011/013/014/015 resize add") is correct at the function/branch level. **Verified.**

### Slice G call-site count (verified)

`rg "Resolve\(|ResolveWithMeta\(" --type go -g "!*_test.go" pkg/channels/ pkg/gateway/ pkg/agent/` returns 16 production call-sites:

| Package | Call-site | Function |
|---|---|---|
| `pkg/channels/weixin/media.go:715` | `store.ResolveWithMeta(part.Ref)` | weixin outbound |
| `pkg/channels/qq/qq.go:417` | `store.ResolveWithMeta(part.Ref)` | qq outbound |
| `pkg/channels/matrix/matrix.go:460` | `store.ResolveWithMeta(ref)` | matrix outbound |
| `pkg/channels/wecom/media.go:497` | `store.ResolveWithMeta(ref)` | wecom outbound |
| `pkg/channels/discord/discord.go:187` | `store.Resolve(part.Ref)` | discord outbound |
| `pkg/channels/feishu/feishu_64.go:382` | `store.Resolve(part.Ref)` | feishu outbound |
| `pkg/channels/slack/slack.go:191` | `store.Resolve(part.Ref)` | slack outbound |
| `pkg/channels/telegram/telegram.go:459` | `store.Resolve(part.Ref)` | telegram outbound |
| `pkg/gateway/replay.go:677` | `mediaStore.Resolve(refStr)` | session replay |
| `pkg/gateway/rest.go:9020` | `store.ResolveWithMeta("media://" + refID)` | upload echo |
| `pkg/agent/loop_media.go:75` | `store.ResolveWithMeta(ref)` | `resolveMediaRefs` |
| `pkg/agent/loop_media.go:373` | `store.ResolveWithMeta(ref)` | `buildArtifactTags` |
| `pkg/agent/loop.go:4844` | `store.ResolveWithMeta(ref)` | `transcribeAudioInMessage` |
| `pkg/agent/loop.go:8112` | `turnMediaStore.ResolveWithMeta(ref)` | outbound media emit |
| `pkg/agent/loop.go:8223` | `turnMediaStore.ResolveWithMeta(ref)` | tool result inline image |
| `pkg/agent/loop.go:8295` | `turnMediaStore.ResolveWithMeta(ref)` | tool result descriptor persist |

= **8 channels + 1 replay + 1 upload-echo + 6 agent-loop** = 16. Plan claims "13+"; count is correct (channels are 8, not the 6 in CLAUDE.md — CLAUDE.md lists Telegram/Discord/Slack/Matrix/IRC/Google Chat/WhatsApp/… but the store-touching subset is the 8 above).

### Branch state (verified)

`git rev-list --count main..sendfile-fix` = 1122 commits. `sendfile-fix` is not on `main`'s tip — it branched from `release/v0.1.1` (per `git log --all --source --remotes --simplify-by-decoration`). The plan correctly notes "operator directive, 2026-07-22: no new branch. Implementation lands as a stacked commit set on the existing branch" — but does not flag that 1122 commits of unrelated history (calendar scheduler, channels redesign, providers UX, runs, contexts, memory, ADR-051 v3) travel with every pushed commit. **Observation:** a per-wave PR from `sendfile-fix` would carry that 1122-commit base; reviewers cannot isolate this feature's diff without `git diff release/v0.1.1..sendfile-fix`.

---

## Phase 2 — Eight Lenses

### 1. Sequencing & coupling — **F-L1-1, F-L1-2, F-L1-3, F-L1-4**

The wave ordering is correct (verified above). The slice-level dependencies are correct (B must precede G/H; A must precede everything). **The hidden sequencing problem is Wave 1 ↔ Wave 3 (T8)**:

Each Wave 1 subagent is told (§3 step 2) to "Write failing tests first (TDD). Use the test names from the spec's TDD plan verbatim where given." The spec's TDD plan rows 16-32 (TestPresentation_*) and 35-45 (TestWorkspaceLibrary_*, TestResolver_*) are SCOPED to slices B/D/F/G — but the tests at rows 1-15 (TestWorkspaceLibrary_*, TestCapabilityRegistry_*) are also slice-scoped. The total of 16+4=20 existing `TestResolveMediaRefs_*`/`TestEncodeImageToDataURL_*` tests will be touched by Wave 1's slice F subagent, who will commit slice-local versions. T8's "Tests in `loop_media_test.go` are **UPDATED** to assert the new observable contract" then rewrites them.

**Two readings of "no test is silently dropped":** (a) "no test is silently dropped from the test plan" (spec coverage intact) — true; (b) "no test is committed twice" — false. The plan's TDD rules need an ownership clause: **"Wave 1 subagents do not write or update tests that exercise `resolveMediaRefs` or `encodeImageToDataURL` — those tests belong to Wave 3 T8 and T9. Slice F subagents write only `TestResize_*` and the new `TestEncodeImageToDataURL_NoPassthroughForUnsupportedFormats` (the FR-016 regression). All other `loop_media_test.go`/`loop_media_normalization_test.go` tests are owned by T8."** Without this rule, Wave 1 commits and Wave 3 T8 will collide.

**Fix (F-L1-1):** Add an explicit ownership rule to §3 prompt template and §2 Wave 1 T3 / Wave 3 T8. The rule should read: *"If your slice modifies a function with existing tests in `loop_media_test.go` or `loop_media_normalization_test.go`, write only the new tests that name your slice's FRs (`TestResize_*`, `TestEncodeImageToDataURL_NoPassthroughForUnsupportedFormats`, `TestPresentation_Step5_*`). Do not modify existing `TestResolveMediaRefs_*` tests; Wave 3 T8 owns them."*

**Fix (F-L1-2, F-L1-3):** Add to §3 prompt template: *"Hard Constraints reminder: #1 single Go binary (one binary, no separate plugins, all features in `./cmd/omnipus`); #3 minimal footprint (security-feature RAM overhead < 10MB beyond baseline); #4 graceful degradation (Linux 5.13+ features fall back to app-level enforcement on older kernels / non-Linux); #5 ecosystem compatibility (follow SKILL.md/HEARTBEAT.md/SOUL.md/AGENTS.md/JSON config conventions). Constraint #2 (pure-Go, no CGo) and Constraint #8 (contract-first wire types) are listed; do not relax the others without an ADR. Always verify with `go build ./cmd/omnipus` after your changes — the single-binary invariant is checked at build time, not at review time."*

### 2. Slice ownership & blast radius — **F-L2-1, F-L2-2, F-L2-3**

**F-L2-1 — Slice B is wider than the plan acknowledges.** The plan's slice table lists B as one slice owning FR-001/002/003/004/006/007/007a/008/009/033 — that's the **entire workspace library** including storage, lifecycle, explicit-delete, cascade-delete, and audit. The ADR's Affected Components table mirrors this (the workspace library is a single new package: `pkg/media/library`). Defensible IF the slice's TDD plan and prompt template specify the layering. The plan's §1 says "Slice B — workspace library core" with no internal decomposition. Compare to Slice C (one package, one role, two FRs) — Slice B is 5× the surface area with the same Wave 1 reviewer-gate slot.

**Fix:** Either (a) split Slice B into B1 (storage + manifest + sha256), B2 (lifecycle: GC + explicit-delete), B3 (cascade-delete hook + audit) — three slices, but the third depends on the second and the second on the first, so they cannot be Wave 1 parallel. OR (b) keep B as one slice but add a §3 sub-step "Slice B is large; implement in this order: storage (FR-001/002/003/004) → lifecycle (FR-006/007/007a/008) → cascade-delete + audit (FR-009/033). Commit one slice, three staged commits if needed; the 6-reviewer gate runs on the full slice diff, not the partials."

**F-L2-2 — Slice G's security MUST is under-resourced.** FR-028a is a security MUST (STRIDE Spoofing guard). The plan's §2 explicitly adds a `silent-failure-hunter` extra pass and STRIDE-focused prompt only to Slice D (T6). Slice G's reviewer gate is the standard 6 + holistic. **Why this matters:** Slice G touches 16 call-sites including 8 channel outbound paths — a missing `callerWorkspaceID` parameter that defaults to nil (the migration approach per FR-028a "nilable") could pass the standard review because nilable is the documented shape, but a channel that fails to thread the workspace ID through would not be caught by the standard gate.

**Fix:** Add to §2 Wave 2 T5: *"Slice G (resolver signature, FR-028a) is a security MUST (STRIDE Spoofing). Extra reviewer pass: `silent-failure-hunter` (default-nilable-context regression). The holistic 7th is asked to verify that the 8 channel outbound paths (`pkg/channels/{weixin,qq,wecom,matrix,discord,feishu,slack,telegram}/...`) thread a non-nil caller-workspace ID OR explicitly bypass the guard with documented justification. The T5 prompt carries the STRIDE Spoofing line from T6."*

**F-L2-3 — Slice C naming collision.** Plan §1 says Slice C writes "the compiled seed" but does not name the package. The ADR Affected Components table says: `pkg/providers/capabilities` — a NEW package. **However** `pkg/providers/catalog/` already exists with `data/providers_catalog.json` (provider metadata, not modalities). The plan should explicitly state "Slice C writes to `pkg/providers/capabilities` (NEW per ADR Affected Components); `pkg/providers/catalog/` is unchanged."

### 3. Test coverage sufficiency — **F-L3-1, F-L3-2, F-L3-3**

**F-L3-1 — silent ownership gap.** 16+4=20 existing tests will be touched by T8's rewrite. The plan's §2 Wave 3 T8 description: *"Tests in `loop_media_test.go` are **UPDATED** to assert the new observable contract — behavioral invariants preserved (SC-010 reworded per round-1 grill M4 — NO 'without modification' — but no test is silently dropped)."* This is the SC-010 reframe — T8 owns these. But the Wave 1 slice F subagent (T3) is told to "Write failing tests first (TDD). Use the test names from the spec's TDD plan verbatim where given." — which does NOT exclude `TestEncodeImageToDataURL_NoPassthroughForUnsupportedFormats` (a test the spec round-1 review added at row 38 — the FR-016 regression). T3 will commit it; that's correct ownership. But T3 may also commit `TestResize_PerProviderBudget_Default7680px` and other resize-specific tests, AND attempt to update the existing `TestEncodeImageToDataURL_PixelBomb_Rejected` to reflect the resize path's new pixel-budget semantics — which is now T8's contract territory.

**Fix:** Add ownership rule per F-L1-1 above.

**F-L3-2 — T9 vs Wave 1 scope overlap.** The plan's §2 Wave 3 T9: *"Add the spec's missed tests: all-formats-upload Scenario Outline (m1), resize ladder floor (m2), document-class guidance noun (m3), ref disambiguation (m4), single-file delete audit (m5); round-2 corrections: deferred GC (R2-M1), manifest refcount drives GC (R2-M2), filename sanitization in content (R2-M3), legacy nilable resolver (R2-M4), retry-fails-different (R2-m1), freeze-gate artifact (R2-m2), E2E env gating (R2-m3)."* These are the spec-grill-round-1/2 missed/coverage-gap tests. The spec's TDD plan already names tests 14a, 37-45 covering these — but T9 says "add the spec's MISSED tests." If Wave 1 subagents wrote tests 14a, 37, 43, 44, 45 per the spec TDD plan verbatim, T9 has nothing to add. The plan should clarify: T9's job is to add **only** the tests NOT named in the spec's TDD plan rows 1-45.

**Fix:** §2 Wave 3 T9: change to *"Add the spec's UNNUMBERED tests (the spec grill round-1/2 named tests as a verification audit; do not duplicate Wave 1 slice TDD tests)."*

**F-L3-3** (observation) — the 6-reviewer gate does not exercise behavioral correctness; T9's spec-named tests are the only behavioral surface. **Enforcement:** the per-SC observation log (Wave 4 T14) is the ground truth; the gate is code-style only.

### 4. Reviewer-gate assignment — **F-L4-1, F-L4-2, F-L4-3**

**F-L4-1 — `general` as architect is a downgrade.** Verified inventory: 6 pr-review-toolkit agents (`PR-Reviewer-{code-reviewer,code-simplifier,comment-analyzer,pr-test-analyzer,silent-failure-hunter,type-design-analyzer}.md`) plus `prometheus.md` + `translation-reviewer{,-worker}.md`. **No** named lead agents (backend-lead, frontend-lead, security-lead, qa-lead, architect). The plan §0 says "Substituted by `general` with a fresh-context holistic-review prompt." This is honest but understates that the 7th reviewer cannot do the depth a dedicated architect would — `general` reads context fresh and gives a holistic review, but cannot reason against the full architecture map the way a project-specific architect prompt would.

**Fix:** Plan §0 footnote: *"Note: the 7th reviewer is `general` with a holistic prompt, not a dedicated `architect` agent. The holistic review is a fresh-context pass that catches cross-file issues the 6 specialists miss; it does not replace architect-level system reasoning. For high-risk waves (Wave 2 T6 security, Wave 3 T8 orchestrator), the holistic prompt is seeded with the slice's spec sections + the spec-grill reports so the reviewer has the same context an architect would have."*

**F-L4-2** (minor) — 3-round threshold is per-wave, but the plan's wording "ask the user after 3 rounds if not converged" is global. With 4 waves × up to 3 rounds × 7 reviewers = 84 review rounds if convergence fails on every wave. Per-wave threshold is reasonable; the wording should make it per-wave.

### 5. Risk realism — **F-L5-1, F-L5-2, F-L5-3, F-L5-4, F-L5-5**

**F-L5-1 — 20M budget is not credible.** §6 risk #6: "high (we nearly hit it earlier at 41M of 10M)... This new goal has 20M." Wave 1 cost estimate (subagent parallel ×4):

- Per-slice spec re-read: ~1.5M tokens (the slice's spec sections + impact table + relevant test names from TDD plan)
- Per-slice implementation: ~2M tokens (Go code generation, package wiring)
- Per-slice tests: ~1M tokens (TDD failing-tests-first)
- Per-slice 6-reviewer gate: 6 × ~500K = ~3M tokens
- Per-slice review-round fixes: ~500K-1M tokens (one fix round expected)
- **Per-slice total: ~8M tokens. Wave 1 × 4 parallel slices: 32M tokens** (assuming parallel reduces context re-read; sequential: 32M+).

Wave 1 alone likely exceeds 20M. **Mitigation per the plan** ("continue across sessions via commit checkpoints") is honest about cross-session continuation but ignores restart overhead (each subagent restart re-reads spec context, ~500K-1M of recurring spend per restart).

**Fix:** Plan §6 risk #6 mitigation: *"If exhausted mid-pipeline, the work continues across sessions via the commit checkpoints on `sendfile-fix`. **Each session restart costs an estimated 500K-1M tokens of context re-load per active slice.** The 20M budget is a per-session allocation, not a per-feature budget; the feature spans as many sessions as it needs."*

**F-L5-2 — pre-existing branch debt is partially unverifiable.** Verified:
- `pkg/providers/openai_compat/provider_test.go:313` `TestProviderChat_HTMLResponsesReturnHelpfulError` EXISTS on the branch.
- `pkg/tools/filesystem_docextract_test.go:85,105,165` `TestReadFile_ExtractsDocx`/`ExtractsXlsx`/`DocumentPagination` EXIST on the branch.
- `src/lib/llm-error.ts` exists; the file's header comment **explicitly states** *"NOT a wire type (no `json:` tags, never crosses the gateway boundary) — display-only. Per CLAUDE.md hard-constraint #8 this is explicitly not a hand-written wire type."* The plan's claim that `llm-error.ts` is "hand-written wire types → migrate to `contracts/components/schemas/`" is **factually incorrect** against the file's own self-documentation. This is a MINOR misleading claim in the plan, but it affects the Wave 4 T12 scope.

The plan must verify these failures with a CI run before scheduling T12. **Fix:** Plan §1 pre-existing branch debt: *"Verify each item against a CI run on `sendfile-fix` before scheduling T12. If a listed item passes, drop it; if an unlisted failure exists, add it. The `llm-error.ts` migration is NOT a Constraint #8 violation (the file is display-only per its own self-documentation); remove from the list."*

**F-L5-3 — stacked commits vs CLAUDE.md PR conventions.** §6 risk #10 mitigation: *"Open a single draft PR after each wave's commits land, OR rely on the 6-reviewer gate per-wave (the gate IS the PR review); document the decision so nothing slips through."* This is in tension with CLAUDE.md: *"Every PR MUST close its issues via keyword."* If a draft PR is opened per wave, it must follow the keyword convention; if no PR is opened until Wave 4, the stacked commits on `sendfile-fix` will not close any issue. The plan should commit to ONE approach.

**Fix:** Plan §6 risk #10 mitigation: *"Open ONE draft PR from `sendfile-fix` after Wave 1 lands (closes via Wave 4 acceptance); the 6-reviewer gate substitutes for PR review on each subsequent wave commit. The PR body accumulates `Closes #XXX` keywords as fixes land. Stacked commits on `sendfile-fix` do NOT close issues until the PR merges to `main`."*

### 6. Acceptance gate verifiability — **F-L6-1, F-L6-2**

**F-L6-1 — acceptance gate (a) "every green" needs a SPECIFIC CI run.** §5 acceptance: *"trigger one final CI run on the entire sendfile-fix branch and observe green; cross-check with live-gateway Playwright runs for the matrix §5 stress tests + every SC's behavior."* The plan does not commit to:
- A specific CI workflow invocation pattern (the plan uses `gh workflow run pr.yml --ref sendfile-fix` but does not commit to capturing the run URL)
- A "final" criterion that distinguishes a green run from a previous green run (re-trigger each time)
- A branch protection check ("all required checks green on HEAD")

**Fix:** Plan §5 acceptance (a): *"Trigger `gh workflow run pr.yml --ref sendfile-fix` and capture the run URL. Acceptance gate (a) passes only when (i) the run URL is recorded in the per-SC observation log, (ii) all required checks on the run are green, AND (iii) the commit SHA at run-start matches `git rev-parse HEAD` at acceptance time."*

**F-L6-2** (minor) — Wave 4 T14 "per-SC observation log" has no format spec. The plan should specify: each observation is `{SC_id, tool_call_summary, tool_result_excerpt, file_path_evidence, timestamp, operator_or_agent_id}` recorded as YAML or JSON in `docs/internal/uat/ADR-051-rev4-sc-observations.md`.

### 7. Subagent prompt template (section 3) — **F-L1-2, F-L1-3, F-L7-1, F-L7-2, F-L7-3**

**F-L7-1** (minor) — Hard Constraints #1/#3/#4/#5 not in prompt template (covered under F-L1-2 above).

**F-L7-2** (minor) — Template step 5: *"Run scoped Go tests with `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/<your-package>/`."* — does not include gofmt (`gofmt -l pkg/<your-package> | wc -l` must be 0 per CLAUDE.md) or the lint exit criterion. The plan mentions lint in step 6 but only for scoped files; the exit criterion (0 issues) is missing.

**Fix:** Add to template step 5: *"Step 5 exit: `gofmt -l pkg/<your-package>` is empty AND `golangci-lint run --build-tags=goolm,stdjson --new-from-rev=HEAD pkg/<your-package>` exits 0."*

**F-L7-3** (minor) — Wave 0's Slice A GENERATES wire types. The template's Wave 0 instruction (§2 Wave 0) says *"Slice A — Generate `contracts/components/schemas/MediaLibraryEntry.yaml` + `MediaAttachmentRequest.yaml`. Reference from `contracts/openapi.yaml`. Run `scripts/gen-contracts.sh`. Hand the new generated types back to the controller."* — fine. But the template's per-slice prompt block (§3, listed after Wave 0 in the doc) says *"Hard Constraint #8 (contract-first): use the generated wire types from pkg/api/generated/ — never a parallel struct/interface."* Wave 0 has nothing to use — it IS the generator. The prompt should differentiate.

### 8. Decision logging — **F-L8-1, F-L8-2**

**F-L8-1 — 5 open decisions omit higher-risk questions.** Plan §7 lists 5 open decisions for the reviewer to evaluate. Each is real but the list omits higher-risk questions:

- **(omitted)** Slice C package naming collision: `pkg/providers/catalog` (existing, has `data/providers_catalog.json`) vs `pkg/providers/capabilities` (planned, NEW per ADR Affected Components). Plan should explicitly state "Slice C writes to `pkg/providers/capabilities` (NEW per ADR); `pkg/providers/catalog/` is unchanged. The `data/providers_catalog.json` (provider metadata) is not the capability catalog — those are different concerns."
- **(omitted)** Step-5 offload copy in `work/` is the Landlock root for agent file tools. A 100 MB offloaded file (the per-file cap) into `work/` per workspace per turn is unbounded — does `work/` have a per-workspace size limit? The plan/spec doesn't say. If `work/` is on a small-volume Fly devpod (~96% full mentioned in CLAUDE.md), a few offloads can fill the disc.
- **(omitted)** Spec FR-007a's "REUSES pathStates.refCount" claim is factually false per spec-grill-round-2 R2-M1 (`pkg/media/store.go:79-99, 168-177, 354-380` shows the existing counter is path-keyed, Store/release-triggered, immediate-delete-at-zero — incompatible with manifest-keyed, session-attach-triggered, 30d-deferred). Slice B's design MUST declare a separate manifest refcount, not reuse.
- **(omitted)** Slice C's spec FR-024 "re-validated against fresh 2026 provider data before freeze" has no defined gate/artifact/test (spec grill round-2 R2-m2). Slice C's TDD plan should commit to a concrete artifact (e.g. "a dated re-validation commit on `provider-media-format-support.md` is recorded in the release notes; the seed commit message references the report").
- **(omitted)** Slice G's "nilable caller-workspace context parameter" approach: how do we verify channels that resolve a `media://workspace/<id>` ref for OUTBOUND delivery actually thread the caller's workspace? Per spec-grill-round-2 R2-M4, the 8 channels each have their own resolver call-site — the plan should commit to a per-channel test or lint rule.

**F-L8-2** (minor) — Slice E classifier codes (FR-018). The plan says Slice E adds `CodeToolArgs` and `CodeSchema` "detected via body-substring patterns (e.g. 'invalid tool arguments', 'schema validation')." What if a provider uses different phrasing (e.g. "tool call schema mismatch")? The plan should commit to specific substrings AND a status-code-based backstop exclusion (e.g. "any 400 with status-only content-policy/bad-tool-args/schema codes does NOT fire step 4, regardless of body phrasing").

---

## Phase 3 — Verifiability of Acceptance

| Acceptance artifact | Tool-result evidence | Reproducible? | Plan-claimed? |
|---|---|---|---|
| Acceptance (a): CI green | `gh workflow run pr.yml --ref sendfile-fix` URL + run green check | Plan triggers CI but does not commit to URL capture → F-L6-1 | Partial |
| Acceptance (b) per-SC: SC-001 100% uploads | 11-format upload test output | Plan names E2E tests 33-34 but does not gate on env-var skip pattern (spec grill round-2 R2-m3) → F-L3-3 | Partial |
| SC-002 100% sha256 mismatch detected | TestWorkspaceLibrary_Read_VerifiesSHA256_TamperDetected output | Yes (test exists in spec TDD plan row 3) | Yes |
| SC-003 0 dead turns | TestE2E_AnyFileAnyModel_UsefulTurn | Plan names but no env-var gate → F-L3-3 | Partial |
| SC-004 100% pixel-bomb caught | TestResize_DecodeConfigGuard_PixelBomb_RoutesToStep7 | Yes (spec row 12) | Yes |
| SC-005 exclusion-set preserves | TestTryMediaDowngrade_ExclusionSet_ContentPolicy_DoesNotFire + 4 analogs | Yes (spec rows 27, FR-018 expansion) | Yes |
| SC-006 step-5 path injected | TestPresentation_Step5Offload_CopiesToWorkDir_InjectsPath | Yes (spec row 20) | Yes |
| SC-007 agent media not in workspace | TestAgentMedia_StaysSessionScoped_NotInWorkspaceLibrary | Yes (spec row 5) | Yes |
| SC-008 legacy fallback | TestResolver_LegacyCallSites_UnaffectedByNilableContextParam | Yes (spec row 45) | Yes |
| SC-009 catalog pull non-fatal | TestCapabilityRegistry_PullFailure_NonFatalRetainsSeed | Yes (spec row 9) | Yes |
| SC-010 behavioral invariants | 16+4 existing tests UPDATED to assert new contract via orchestrator | **Wave 3 T8 ownership rule missing** → F-L1-1, F-L3-1 | Partial |

---

## Unasked Questions

1. **(F-L5-2)** Are the four "pre-existing branch debt" items actually failing on the branch? T12 must verify with a CI run before scheduling fixes.
2. **(F-L2-1)** Should Slice B be split into B1/B2/B3 (storage/lifecycle/cascade) or kept as one slice with a staged commit rule?
3. **(F-L1-1)** Who owns the 16 `TestResolveMediaRefs_*` + 4 `TestEncodeImageToDataURL_*` test updates — T3 (Wave 1 slice F) or T8 (Wave 3)? Current plan leaves ownership ambiguous.
4. **(F-L8-1)** Where does step-5 offload's 100 MB-per-file cap interact with the workspace's `work/` dir size limit (if any)?
5. **(F-L8-1)** Is the manifest refcount a separate counter from `pathStates.refCount`, given the spec grill round-2 R2-M1 finding that the reuse claim is false?
6. **(F-L5-1)** What is the per-session token budget allocation if 20M is exhausted mid-Wave-1, and how is restart-overhead accounted for?
7. **(F-L4-1)** Is the `general`-as-holistic-reviewer an acceptable downgrade from CLAUDE.md's named-architect reviewer for high-risk waves (Wave 2 T6 security, Wave 3 T8 orchestrator)?

---

## Verdict

**REVISE.**

The plan is structurally sound and the wave/slice decomposition tracks the ADR Affected Components correctly. The 6-reviewer-gate loop is a credible substitute for the missing named lead agents, the test plan exists, and the acceptance criteria are testable. None of the findings are architecture problems — they are surgical plan-level fixes.

But the plan cannot govern execution as written. **At minimum, F-L1-1 (Wave 1 ↔ Wave 3 ownership), F-L1-2/3 (prompt template Hard Constraints), F-L2-1 (Slice B staged commit rule), F-L2-2 (Slice G security pass), F-L3-1 (test ownership), F-L4-1 (general-as-architect disclosure), F-L5-1 (token budget honesty), F-L5-2 (pre-existing debt verification), F-L5-3 (PR/commit convention), F-L6-1 (acceptance CI URL), and F-L8-1 (omitted high-risk decisions) must close before Stage 3.**

Once those 11 MAJORs are addressed, the plan can move to PASS. The MINORs (F-L7-1/2/3, F-L6-2, F-L2-3, F-L4-2, F-L8-2) and OBSERVATIONS (F-L5-4/5, F-L4-3, F-L3-3, F-L1-4) are required polish but do not individually block.

---

*End of review. Plan was not modified. Per operator directive: do NOT begin Stage 3.*