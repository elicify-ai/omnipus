# Wave 0 / Slice A — code-reviewer review (round 1)

**Reviewer:** `pr-review-toolkit:code-reviewer`  
**Scope:** commit `08690ff9` versus parent `f6eccbcd`  
**Governing requirement:** FR-031, with the Slice A touches to FR-001 and the attachment wire shape  
**Diff:** 12 files changed, 745 insertions, 82 deletions

## Findings

| ID | Severity | Section | One-line | Fix |
|---|---|---|---|---|
| CR-01 | MAJOR | Generated TS validation / schema fidelity | Both new source schemas are closed (`additionalProperties: false` at `contracts/components/schemas/MediaLibraryEntry.yaml:6` and `MediaAttachmentRequest.yaml:6`), but their generated Zod validators end without `.strict()` (`src/lib/api/generated/schemas.ts:2385-2400`); executed `safeParse` calls accepted and silently stripped an `unexpected` key, contrary to ADR-014 and the SPA-edge contract. | Extend the strict-schema postprocessor in `scripts/_gen-ts.sh:104-129` to support untyped declarations (`export const Name =`, not only `export const Name:`), include both new schemas or derive the list from OpenAPI automatically, regenerate, and add extra-key rejection tests. |
| CR-02 | MAJOR | OpenAPI operation minimality and consistency | The new `POST /workspaces/{id}/media/attachments` cannot attach anything to a chat because it has no session/message target and returns no resource (`contracts/openapi.yaml:5477-5506`); its unspecced `position` and `content_injection_override` fields (`MediaAttachmentRequest.yaml:15-25`) conflict with the specified `MessageFrame.media` ref-array flow (`MessageFrame.yaml:36-47`), while the same new surface omits FR-008's required explicit DELETE on `media/{media_id}`. | Keep the operation set spec-aligned: attach existing refs through `MessageFrame.media`, add the required DELETE contract, and remove the speculative POST/override/position; alternatively amend the ADR/spec first with target identity, refcount lifecycle, ordering, sanitization, response semantics, and tests, then regenerate. |
| CR-03 | MAJOR | Existing upload contract / FR-001 | Slice A leaves `POST /upload` documented and typed as session-directory storage with only `session_id` (`contracts/openapi.yaml:4058-4097`), while `UploadedFile` still promises `uploads/{session_id}/{filename}` and a legacy `media://<uuid>` ref (`contracts/components/schemas/UploadedFile.yaml:3-41`); generated clients therefore cannot express or observe the FR-001 workspace-library upload contract. | Contract workspace targeting before backend work: add a workspace-scoped path/field or explicitly specify derivation from a workspace-bound session, then update upload descriptions and `UploadedFile` path/ref semantics to the canonical workspace ref shape and regenerate. |
| CR-04 | MAJOR | Contract regression coverage | Neither `pkg/api/generated/contract_test.go` nor `fixtures.go` mentions either new type, so the accepted ADR-015 contract-test safety net is absent; this is why `make verify-contracts` passed despite CR-01's runtime schema mismatch. | Add valid, zero/invalid, boundary, and unknown-property fixtures/tests for `MediaLibraryEntry` and `MediaAttachmentRequest`, including Go-to-schema round trips and generated-Zod strictness. |
| CR-05 | MINOR | Identifier constraints | The contract describes both media IDs as UUIDs but validates only non-empty strings (`MediaLibraryEntry.yaml:16-20`, `MediaAttachmentRequest.yaml:10-14`), and the route parameter is an unconstrained string (`contracts/openapi.yaml:5523-5528`). | Use a shared UUID schema or `format: uuid` consistently for the entry ID, request ID, and path parameter, then regenerate. |
| CR-06 | MINOR | AsyncAPI generator documentation | The canonical AsyncAPI comments still say `ErrorFrame.Payload` and `ReplayErrorFrame.Payload` are hand-adjusted and must be re-applied (`contracts/asyncapi.yaml:1417-1423`, `1880-1887`), but this commit correctly moved the repair into `matchingNamedInlineGoType`; following the stale instruction would recreate generated-file drift. | Update both comments to identify the generator-owned named-inline pointer repair and its regression tests; remove all “hand-adjusted/re-apply” instructions. |
| CR-07 | MINOR | Text-file style | Both authored schema files and `src/lib/llm-error.ts` lack a final newline, producing `No newline at end of file` markers in the patch. | Add trailing newlines and regenerate the copied inbound schemas. |
| CR-08 | OBS | List scalability | `GET /workspaces/{id}/media` returns an unbounded raw array (`contracts/openapi.yaml:5448-5475`) even though the persistent library has no storage quota, so response memory and UI work grow without a contract bound. | Before the SPA consumes this shape, consider a bounded/cursor list response or explicitly document the accepted unbounded behavior. |

## Causal chains

- **CR-01:** closed YAML schema → omitted strict-codegen mapping → Zod returns success and strips unknown keys → SPA emits no schema-error counter/toast → server/client drift becomes silent.
- **CR-02:** composer sends an ordered WS `media` array → REST POST has no message/session identity → `position` and refcount ownership have no target → attachment lifecycle and GC decrement semantics cannot be implemented coherently.
- **CR-03:** upload client can send only `session_id` → contract still promises session storage/legacy refs → workspace destination and canonical ref are undefined at the boundary → later backend/SPA slices must either violate contract-first or preserve the wrong behavior.

## Hypothesis disposition

| Hypothesis | Result | Evidence |
|---|---|---|
| H1 — schemas/operations diverge from FR-031 and touched flows | **Confirmed** | CR-02 and CR-03. |
| H2 — generated artifacts drift from the source schemas | **Partially confirmed** | Byte/codegen drift is absent, but runtime `additionalProperties: false` fidelity is broken (CR-01). Required/optional Go fields, int64/date-time mapping, SHA-256 regex, and generated TS aliases otherwise match. |
| H3 — the AsyncAPI generator repair regresses payload omission | **Rejected** | Generator unit test and both targeted Go payload-omission contract tests passed; generated fields are pointers. |
| H4 — the `llm-error.ts` refactor changes runtime or TS behavior | **Rejected** | The generated code union is the same seven literals, imports are type-only, `tsc -b --noEmit` passed, and all 20 focused Vitest tests passed. |
| H5 — generator/LLM-error changes are unrelated churn | **Rejected** | On the parent, `make verify-contracts` failed with two hand-written-wire findings and regeneration replaced pointer payloads with anonymous value structs; on `08690ff9`, the same gate passed with no drift. |

## Verification evidence

- Parent `f6eccbcd` in an isolated clone: `make verify-contracts` failed on exactly two `src/lib/llm-error.ts` handwritten-wire findings; regeneration also produced a 28-line `asyncapi_types.gen.go` drift replacing pointer payloads with anonymous value structs.
- Commit `08690ff9` in an isolated clone: `make verify-contracts` passed (OpenAPI lint, AsyncAPI validation, regeneration, wire-type lint, `tsc -b --noEmit`, zero generated drift).
- Executed generated validators with extra keys: both `MediaLibraryEntry.safeParse(...)` and `MediaAttachmentRequest.safeParse(...)` returned `success: true` and stripped `unexpected`, confirming CR-01.
- `go test -count=1 ./scripts/gen-asyncapi-go` passed; targeted `TestErrorFrame_PayloadOmitEmpty` and `TestReplayErrorFrame_PayloadOmitEmpty` passed.
- `npx vitest run src/lib/llm-error.test.ts`: 1 file, 20 tests passed.
- `gofmt -l` returned no files; scoped `golangci-lint` returned `0 issues`; `git diff --check` passed.
- Gap: full Go/CI suites were not run locally, per the repository's devpod resource policy.

## Verdict

**REVISE — 0 CRIT / 4 MAJOR / 3 MINOR / 1 OBS.**

Do not begin Wave 1 until CR-01 through CR-04 are resolved and re-reviewed.
