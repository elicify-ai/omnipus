# Wave 0 / Slice A — code-reviewer review (round 2)

**Reviewer:** `pr-review-toolkit:code-reviewer`  
**Scope:** corrections `08690ff9..d0e7374a`; new-risk review of generator repair (`da892f01`), drift gate (`48666ec5`), and schema hardening (`07497820`)  
**Exit proof:** re-run the strict validators, generator tests, standalone AsyncAPI drift gate, and full contract drift gate; verify each round-1 MAJOR against the corrected source.

## Findings

| ID | Severity | Section | One-line | Fix |
|---|---|---|---|---|
| CR2-01 | MAJOR | Round-1 CR-02 / contract semantics | The correction removes `position` and `content_injection_override`, but the remaining `POST /workspaces/{id}/media/attachments` still has no message/session target and returns no attachment/resource (`contracts/openapi.yaml:5475-5504`), so it still cannot implement its summary, cannot establish refcount ownership, and still conflicts with the specified `MessageFrame.media` ref-array flow. The claimed need to retain operations as schema anchors is false: both schemas are already referenced under `components.schemas` (`contracts/openapi.yaml:109-113`), and FR-031 asks only for generated component types (`docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1098`). | Delete the speculative POST and anchor/generate `MediaAttachmentRequest` from the component reference, or amend the spec first with target identity, response/lifecycle semantics, and tests. |
| CR2-02 | MAJOR | Round-1 CR-02 / explicit delete | The corrected contract still exposes only GET on `/workspaces/{id}/media/{media_id}` (`contracts/openapi.yaml:5506-5537`); FR-008's required explicit user delete remains absent (`docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1071`). Wave 1 cannot implement the required boundary contract-first without changing Slice A's wire contract. | Add DELETE with explicit success/error responses and UUID-constrained parameters before backend implementation, then regenerate. |
| CR2-03 | MAJOR | Round-1 CR-03 / upload boundary | `POST /upload` still accepts only `session_id`, promises session-directory storage, and returns the legacy `UploadedFile` shape (`contracts/openapi.yaml:4056-4095`; `contracts/components/schemas/UploadedFile.yaml:3-41`). Therefore generated clients still cannot select a workspace or observe the canonical `media://workspace/<ws>/<id>` result required by FR-001. Marking this “Wave 1 migration” does not satisfy the contract-first prerequisite. | Contract workspace targeting and canonical response/ref semantics now; Wave 1 should consume, not invent, this boundary. |
| CR2-04 | MAJOR | Round-1 CR-04 / runtime contract coverage | No `MediaLibraryEntry` or `MediaAttachmentRequest` fixture/test exists in `pkg/api/generated/contract_test.go` or `fixtures.go`; the corrections add no committed Zod unknown-key regression test either. `make verify-contracts` proves regenerated-file identity, not runtime behavior—the original strictness bug also passed it. | Add valid, boundary, invalid, and unknown-property coverage for both generated Go/schema and generated Zod validators. |
| CR2-05 | MAJOR | Commit A / schema-shape equivalence | `sameSchemaShape` calls its comparison “structural” but the parser/model omits wire-significant JSON-Schema constraints such as `minimum`, `maximum`, `minLength`, `maxLength`, `pattern`, and object closure (`additionalProperties: false` is indistinguishable from unspecified). Thus inline and named schemas can differ in accepted payloads yet be collapsed to one named Go type (`scripts/gen-asyncapi-go/main.go:93-111,135-229,467-513`). The drift gate only detects emitted-file changes; it cannot detect constraints the generator discarded. | Parse and compare every wire-significant constraint used by AsyncAPI, including an explicit tri-state/object-closure representation, and add mismatch tests for bounds/pattern/additionalProperties. Prefer explicit `$ref` in the source schema over heuristic equivalence where possible. |
| CR2-06 | MINOR | Commit E / workspace identity | `workspace_id` is described as authoritative ownership data but is not `readOnly`, unlike the other server-generated fields (`contracts/components/schemas/MediaLibraryEntry.yaml:23-27`). Request-side validation can therefore treat it as client-writable if this response schema is reused. | Mark `workspace_id` `readOnly: true` or split input/output schemas. |
| CR2-07 | MINOR | Commit E / path constraints | The corrected `media_id` body field is UUID-constrained, but both workspace `id` and path `media_id` remain unconstrained strings (`contracts/openapi.yaml:5455-5460,5484-5489,5515-5526`). | Apply the shared UUID format consistently if IDs are truly UUIDs; otherwise remove the UUID claim from component fields and document the actual ID grammar. |
| CR2-08 | MINOR | Source enum / two-mechanism invariant | `MediaLibraryEntry.source` permits `tool_output` while its description says agent-generated tool output is never migrated into the persistent library (`contracts/components/schemas/MediaLibraryEntry.yaml:59-70`). This encodes a state the governing non-behavior forbids. | Remove `tool_output`, or rename/document a permitted non-agent source with an explicit persistence rule. |

## Round-1 MAJOR re-verification

| Finding | Result | Evidence |
|---|---|---|
| CR-01 — generated Zod accepted unknown keys | **Fixed** | Executed both corrected validators with valid payloads plus `unexpected`; both returned `success: false`. Generated component and inline request-body schemas include `.strict()` (`src/lib/api/generated/schemas.ts:2385-2399,7233-7239`). |
| CR-02 — speculative attachment operation / missing DELETE | **Not fixed** | Speculative controls were removed, but the targetless POST remains and DELETE is still absent. See CR2-01/02. |
| CR-03 — legacy upload contract | **Not fixed** | `/upload` and `UploadedFile` remain session-scoped/legacy. See CR2-03. |
| CR-04 — contract regression coverage | **Not fixed** | Searches found no mentions in generated contract fixtures/tests; no committed Zod regression test was added. See CR2-04. |

## Causal chains

- **CR2-01/02/03:** contract foundation defers required boundary decisions → Wave 1 must alter/add wire formats while implementing handlers → contract-first sequencing is inverted → clients and backend can diverge or speculative dead endpoints ship.
- **CR2-04:** drift-only gate regenerates identical code → runtime strictness/validation behavior is never asserted → generator/postprocessor regressions can pass the gate, as round 1 already demonstrated.
- **CR2-05:** parser drops validation keywords → equivalence sees schemas as equal → generator aliases an inline field to a named type despite validation-shape divergence → Go wire representation no longer faithfully signals source-schema differences, while the drift gate stays green because the loss is deterministic.

## Hypothesis disposition

| Hypothesis | Result | Evidence |
|---|---|---|
| H1 — all four round-1 MAJORs were corrected | **Rejected** | Only CR-01 is fully fixed; CR-02–04 remain blockers. |
| H2 — Commit A's equivalence test is complete for wire-significant shape | **Rejected** | The schema AST/comparator omits bounds, patterns, lengths, and explicit closure; tests cover property/required/ref branches only. |
| H3 — Commit D's drift gate is ineffective or non-idempotent | **Rejected** | `make verify-asyncapi-drift` exited 0 and regenerated output was byte-identical. Its scope is correctly drift, but it cannot replace behavioral/semantic tests. |
| H4 — Commit E's strict postprocessing still silently strips unknown keys | **Rejected** | Executed validators rejected extra properties for both schemas. |
| H5 — schema hardening introduced no remaining type inconsistencies | **Rejected** | `workspace_id` mutability, path-ID inconsistency, and forbidden `tool_output` state remain (CR2-06–08). |

## Verification evidence

- `CGO_ENABLED=0 go test -count=1 ./scripts/gen-asyncapi-go/...` — exit 0.
- `make verify-asyncapi-drift` — exit 0; generated AsyncAPI Go output byte-identical.
- `make verify-contracts` — exit 0; OpenAPI/AsyncAPI validation, generation, wire lint, TypeScript check, and generated drift all clean.
- `npx tsx` execution of `MediaLibraryEntry.safeParse` and `MediaAttachmentRequest.safeParse` with extra keys — both `success: false`.
- Initial plain `node` attempt could not import `.ts` (`ERR_UNKNOWN_FILE_EXTENSION`); isolated as runner mismatch and rerun successfully with `tsx`.
- `git diff --check` — exit 0.
- Gap: full Go/CI suites were not run locally, per repository policy; review is read-only and no production behavior was changed by this reviewer.

## Verdict

**REVISE — 0 CRIT / 5 MAJOR / 3 MINOR.**

Do not advance Wave 1 until CR2-01 through CR2-05 are resolved and re-reviewed.
