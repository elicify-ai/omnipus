# Wave 0 / Slice A — Holistic Review (Round 2)

**Review target:** `sendfile-fix` HEAD `d0e7374a` against base `f6eccbcd`.
**Mode:** holistic reviewer, round 2; implementation review was read-only. This report is the requested output artifact.
**Primary re-verification:** round-1 holistic **M-1**, commit split A–F.

## Verdict

**REVISE — M-1 is only partially resolved.** The original mixed commit is now split into six coherent, reviewable commits, and the final stack at Commit E/HEAD is contract-clean. However, the requested stronger property — **each commit independently mergeable** — does not hold for Commit B. At `0e7dcf5e`, `make verify-contracts` fails the repository wire-type lint on the pre-existing hand-written `LLMError` and `LLMErrorReplay`; running generation also changes `pkg/api/generated/asyncapi_types.gen.go` because Commit A changed the generator but Commit D does not commit its regenerated output until later. Commit B is therefore intentionally dependent on both Commit C and Commit D and cannot pass the repository's own contract gate by itself.

**Counts: CRITICAL 0 · MAJOR 1 · MINOR 1 · OBSERVATION 3.**

## M-1 commit-split re-verification

| Commit | Scope audit | Verification observed | Independently mergeable? |
|---|---|---|---|
| **A** `da892f01` | Generator implementation + its initial regression test only. | `CGO_ENABLED=0 go test -count=1 ./scripts/gen-asyncapi-go/...` passed. | **Yes**, within its scoped generator contract. It deliberately does not regenerate committed output. |
| **B** `0e7dcf5e` | Media-library schemas, OpenAPI operations, generated OpenAPI Go/TS/Zod, inbound mirrors only. | `make verify-contracts` failed with 2 wire-lint findings in `src/lib/llm-error.ts`. A generation-only check also produced a diff in `pkg/api/generated/asyncapi_types.gen.go`, because A's generator output had not yet landed. | **No.** Clean scope, but not independently green or mergeable. |
| **C** `90837961` | `src/lib/llm-error.ts` generated-type alias migration only. | `npm run typecheck` passed. | **Yes** as a focused debt-cleanup commit; it also removes B's wire-lint blocker. |
| **D** `48666ec5` | AsyncAPI regeneration, standalone drift target, and branch tests only. | `make verify-asyncapi-drift` passed; generator tests passed. | **Yes** when stacked after A; semantically dependent on A by design but internally green. |
| **E** `07497820` | Media schema hardening + generated outputs + narrowly scoped TS generator postprocessing. | `make verify-contracts` passed with wire lint `OK (0 findings)`, TypeScript compilation, and zero generated drift. | **Yes** when stacked after B–D. This is the first fully green contracts checkpoint. |
| **F** `d0e7374a` | Verification evidence document only. | `git diff --check HEAD^..HEAD` passed. | **Yes** as documentation, though it necessarily describes A–E and is not useful alone. |

All six commits have the required operator author and committer identity and no `Co-authored-by` trailers. `git diff --check` passed for every individual commit and for `f6eccbcd..d0e7374a`.

### MAJOR H-1 — Commit B is not independently mergeable

Round-1 M-1 required disentangling the media contracts, AsyncAPI generator repair, generated-output update, and `llm-error.ts` migration. The corrected stack achieves **scope separation**, but not **independent mergeability**:

1. At B, the contract gate reaches `scripts/check-no-handwritten-wire-types.sh` and fails on `LLMError` and `LLMErrorReplay`; C is required to restore the gate.
2. At B, rerunning contract generation changes `asyncapi_types.gen.go`; D is required to restore generated-output drift cleanliness after A.
3. Therefore B cannot be cherry-picked or merged as a standalone Slice-A contract commit while satisfying Hard Constraints #7 and #8.

**Required correction:** reorder/squash dependencies so every advertised independently mergeable unit is green. The minimal clean topology is either:

- **A+D** as one atomic generator-fix commit (implementation, tests, regenerated output, drift gate), then **C**, then **B+E** as one atomic media-contract commit; or
- keep A/D separate but place D immediately after A, C before B, and fold E into B so the media-contract commit is born hardened and `make verify-contracts` passes at that commit.

Commit F should then record the corrected hashes and rerun evidence. This does not require changing runtime behavior.

## Structural / architectural assessment

The final schema stack substantially matches the ADR Decision and the spec's compensating-control envelope:

- `MediaLibraryEntry` carries workspace ownership, immutable server-derived integrity metadata, bounded filename/size, and strict object validation.
- `MediaAttachmentRequest` is reduced to the minimal existing-entry identifier; the undocumented override and ordering abstractions are gone.
- `workspace_id` provides the data needed for the future `media://workspace/<workspace>/<media>` membership check.
- Strict Zod generation is enforced both for named schemas and the inlined attachment body.
- No Layer-1 behavior is implemented in Wave 0, so the ADR's presentation-chain compensating controls, Explicit Non-Behaviors, and provider/filesystem integration boundaries are not prematurely encoded as runtime behavior.

### MINOR H-2 — `source` weakens the ADR's two-mechanism split

`MediaLibraryEntry.source` allows `tool_output`, while its own description says agent-generated tool output is session-scoped and **never migrated into the persistent workspace library**. The ADR Decision and spec Explicit Non-Behaviors make that split a compensating control against agent-driven persistent-disc flooding. Encoding `tool_output` as a valid persistent-library entry source creates a contract-level path for exactly the prohibited state, even if the future live handler promises not to emit it.

**Fix:** remove `tool_output` from this persistent-library schema, or rename it to a narrowly documented user-mediated provenance that cannot represent session-inline agent output. Keep test-only provenance out of the public enum where practical; fixtures can use the same production-valid source they are exercising.

### OBS H-1 — Wave 0 cannot close the spec's unresolved round-2 MAJORs

The latest spec grill remains **REVISE (0 CRIT / 4 MAJOR)**: the manifest refcount architecture is factually unresolved, its BDD trace is missing, step-7 filename sanitization is incomplete, and workspace-aware resolution has an unenumerated 13+ call-site blast radius. The current schemas do not worsen these issues, but `refcount` and `last_refcount_seen_at` should not be treated as settled implementation semantics. Wave 1 must resolve the spec before implementing persistence/GC or resolver authorization.

### OBS H-2 — Three REST operations remain future-facing

The contracts advertise list/get/attach endpoints before handlers exist. This is acceptable only as a Wave-0 contract declaration if the actual router continues returning 404 for those nested paths and no release is cut between Wave 0 and handler delivery. It is not evidence that the Behavioral Contract is implemented.

### OBS H-3 — Generator drift control is final-stack sound

The AsyncAPI generator's silent shape-divergence fallback remains intentional, but Commit D adds branch coverage and a generated-output drift target. At the final stack, `make verify-contracts` passed and therefore regenerated artifacts, mirrored inbound schemas, wire lint, and TypeScript compilation were observed clean. This addresses the architectural risk of carrying an undocumented hand-edited generated type, though stale prose in `contracts/asyncapi.yaml` remains a documentation follow-up.

## Compensating-control carry-forward

Before Wave 1 implementation advances, retain these governing boundaries from the ADR/spec/grills:

1. Persistent workspace media is **user-upload-only**; agent-generated media stays session-scoped.
2. Workspace membership must be checked at every workspace-ref resolution surface, not only the agent-loop path.
3. Manifest refcount/GC must be specified as a separate lifecycle from the legacy path refcount unless the spec proves compatible semantics.
4. Every provider-bound interpolation of a user filename, including honest markers, must use a safe derived/display value.
5. Layer 1 must preserve classifier-primary retry exclusions, sandbox-safe work-dir offload, decode guards, strict no-passthrough behavior, and honest-marker survival.
6. The unresolved spec round-2 MAJORs remain blocking design debt; passing Wave-0 contract generation does not override the spec's REVISE verdict.

## Rejected hypotheses

- **Hypothesis: the split is still scope-mixed. Rejected.** File-level and patch-level inspection shows A–F have coherent responsibilities.
- **Hypothesis: final generated artifacts drift. Rejected.** Commit E's `make verify-contracts` passed with zero drift and zero wire-lint findings.
- **Hypothesis: every commit is independently mergeable. Rejected.** Commit B fails the contract gate and generation changes an uncommitted AsyncAPI artifact.
- **Hypothesis: Wave 0 introduces Layer-1 behavioral contradiction. Rejected.** It declares wire shapes only; presentation/runtime boundaries remain for later waves.

## Final decision

**REVISE. CRIT 0 / MAJOR 1.** Round-1 M-1 is fixed as a separation-of-concerns problem, but not as the requested independently mergeable A–F stack. Repair the commit topology, remove or justify the persistent-library `tool_output` source, then rerun the holistic gate. No runtime code change is required for this verdict.
