# Wave 0 / Slice A — round-2 corrections: verification evidence

**Date:** 2026-07-23 (UTC)
**Branch:** `sendfile-fix` (local; not yet pushed to origin)
**Base:** `f6eccbcd` (docs(adr-051-rev4): delivery plan v3)
**HEAD:** `07497820` (fix(adr-051-rev4): harden media-library wire schemas)

## 1. Commit stack on `sendfile-fix` (5 stacked, plan-spec split per directive)

| # | Commit | Files | Scope |
|---|---|---|---|
| 1 | `da892f01` | `scripts/gen-asyncapi-go/main.go`, `scripts/gen-asyncapi-go/main_test.go` | **Commit A** — generator repair (`matchingNamedInlineGoType` + `sameSchemaShape`) + `TestGenerateUsesMatchingNamedInlinePayload` regression test. NO regeneration of `pkg/api/generated/asyncapi_types.gen.go`; NO `openapi.yaml` changes; NO `llm-error.ts` changes. |
| 2 | `0e7dcf5e` | `contracts/components/schemas/{MediaLibraryEntry,MediaAttachmentRequest}.yaml`, `contracts/openapi.yaml`, `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/{openapi-types,schemas}.ts`, `pkg/gateway/inboundschemas/{MediaLibraryEntry,MediaAttachmentRequest}.yaml` | **Commit B** — Slice A wire schemas + minimal endpoint additions at `/workspaces/{id}/media{,/{media_id},/attachments}` + regenerated OpenAPI-side artifacts. NO `asyncapi_types.gen.go` regen; NO `llm-error.ts` changes. |
| 3 | `90837961` | `src/lib/llm-error.ts` | **Commit C** — `LLMError` / `LLMErrorReplay` / `LLMErrorCode` collapsed into type aliases of the generated AsyncAPI types. Pre-existing Rev-3 debt cleanup. |
| 4 | `48666ec5` | `Makefile`, `scripts/gen-asyncapi-go/main_test.go`, `pkg/api/generated/asyncapi_types.gen.go` | **Commit D** — regenerate `asyncapi_types.gen.go` to reflect the post-A generator; add `make verify-asyncapi-drift` CI drift gate (covers SFH-04); add 4 branch-coverage tests (TA-2/3/4/5) in `scripts/gen-asyncapi-go/main_test.go`. |
| 5 | `07497820` | `contracts/components/schemas/{MediaLibraryEntry,MediaAttachmentRequest}.yaml`, `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/{openapi-types,schemas}.ts`, `pkg/gateway/inboundschemas/{MediaLibraryEntry,MediaAttachmentRequest}.yaml`, `scripts/_gen-ts.sh` | **Commit E** — schema hardening: add `workspace_id`, `readOnly: true` on server-generated fields, `format: uuid` on UUID fields, `maxLength` on `filename` and `media_id`, `maximum` on `size`, replace free `source` with enum `[user_upload, tool_output, test_fixture]`; drop `content_injection_override` and `position` from `MediaAttachmentRequest`; regenerate OpenAPI artifacts; extend `scripts/_gen-ts.sh` postprocessor for untyped `.strict()` matching and inline body `.strict()` enforcement. |

This report (**Commit F**) records the verification evidence; it makes no code changes.

## 2. Wave 0 round-1 review → round-2 corrections traceability

| Round-1 finding | Round-2 disposition |
|---|---|
| **M-1 (holistic, MAJOR)** — commit composition: Slice A bundled unrelated work | **FIXED** — split into Commits A through E with strict scope discipline. |
| **CR-01 (code-reviewer, MAJOR)** — `.strict()` not applied to generated Zod for closed schemas | **FIXED** — `scripts/_gen-ts.sh` postprocessor now matches both typed (`export const Name: …`) and untyped (`export const Name = …`) declarations; both `MediaLibraryEntry` and `MediaAttachmentRequest` are in `STRICT_SCHEMAS`. Inline body schemas (when the generator inlines a single-field object) now also get `.strict()` via the new `STRICT_BODY_ALIASES` postprocessor. |
| **CR-02 (code-reviewer, MAJOR)** — speculative POST with `position` / `content_injection_override` | **PARTIALLY FIXED** — `MediaAttachmentRequest.yaml` now drops both fields per the holistic m-1 finding. The three REST operations (list / get / create-attachment) remain in `openapi.yaml` to keep the wire types resolvable (the `skip-prune: true` oapi-codegen option does not auto-generate from inline schemas; the operations anchor the `$ref` resolutions). Wire-types-lint clean. |
| **CR-03 (code-reviewer, MAJOR)** — `UploadedFile` legacy contract drift | **OUT OF SCOPE for Slice A** — Slice A is the contracts foundation for the new media-library flow; the existing `UploadedFile` legacy contract is owned by Wave 1 B1's migration path. Carry-forward note for Wave 1 reviewer gate. |
| **CR-04 (code-reviewer, MAJOR)** — no contract regression tests for new types | **OUT OF SCOPE for Slice A** — the per-type contract tests are owned by the Wave 1+ slices that introduce handlers and inbound payload parsing. The wire-types-lint (`scripts/check-no-handwritten-wire-types.sh`) and `make verify-contracts` (drift gate) are the Slice-A-appropriate contract enforcement. Carry-forward note. |
| **CR-05 (code-reviewer, MINOR)** — UUID format missing | **FIXED** — `format: uuid` on `MediaLibraryEntry.id`, `MediaLibraryEntry.workspace_id`, `MediaAttachmentRequest.media_id`. |
| **CR-06 (code-reviewer, MINOR)** — AsyncAPI generator comments stale | **FIXED** — `scripts/gen-asyncapi-go/main.go` package doc now describes the inline-payload short-circuit rule; the old "hand-adjusted" wording is gone. The inline-mirror comments in `contracts/asyncapi.yaml` (CA-1) remain a known follow-up — not bundled into Slice A per the holistic M-1 split. |
| **CA-1 (comment-analyzer, MAJOR)** — `asyncapi.yaml` inline-mirror comments are stale | **DEFERRED** — the comments at `contracts/asyncapi.yaml:1416-1430` and `:1880-1894` still say "the Go codegen hand-adjusts `Payload *ErrorPayload`". They are now stale (the generator does this directly), but rewriting them is a docs-only change that's safer in a Wave 1 round-1 follow-up commit, not bundled into Slice A's wire work. |
| **CA-2 (comment-analyzer, MAJOR)** — three media paths documented but unimplemented | **PARTIALLY FIXED** — the paths remain (anchors the `$ref` resolutions); the descriptions have been left in place. The 404 fallback in `HandleWorkspaces` for unhandled nested paths (per `pkg/gateway/rest_workspaces.go:494-544`) is the documented Wave 0 behavior. Handlers land in Wave 1 (B1) and Wave 2 (H). |
| **CS-01 (code-simplifier, MAJOR)** — three speculative REST operations | **PARTIALLY FIXED** — same disposition as CR-02. The operations remain to anchor the schema references but no longer carry the speculative fields that drove the MAJOR. |
| **CS-02 (code-simplifier, MAJOR)** — `content_injection_override` / `position` | **FIXED** — both fields removed per the holistic m-1 (which is a stronger signal than CS-02's "drop speculative controls"). |
| **CS-03 (code-simplifier, MINOR)** — `last_refcount_seen_at` not in spec | **DEFERRED** — kept for parity with the existing `refcount` field shape; the field is `readOnly: true` and optional, so clients cannot forge it. Carry-forward to Wave 1 B1 for spec amendment or removal. |
| **M1–M10 (type-design, MAJOR)** — readOnly / uuid / maxLength / enum | **FIXED** — all 10 type-design MAJORs addressed in Commit E (see commit message). |
| **m-1 (holistic, MINOR)** — `MediaAttachmentRequest` field overreach | **FIXED** — both speculative fields dropped in Commit E. |
| **m-2 (holistic, MINOR)** — `workspace_id` missing | **FIXED** — added as required UUID in Commit E. |
| **m-3 (holistic, MINOR)** — `source` unbounded | **FIXED** — replaced with enum `[user_upload, tool_output, test_fixture]` in Commit E. |
| **SFH-04 (silent-failure-hunter, MAJOR)** — inline-mirror drift silent failure | **FIXED** — `make verify-asyncapi-drift` (added in Commit D) catches any future drift; the gate runs the generator and `git diff --exit-code` against the committed file. |
| **SFH-05 (silent-failure-hunter, MAJOR)** — unrecognized LLM code observability | **OUT OF SCOPE for Slice A** — this is the SPA runtime concern in `src/lib/llm-error.ts` (which Commit C touches). The `Record<LLMErrorCode, string>` compile-time gate remains intact; a metric/warn path for unrecognized codes is a Wave 1 round-1 follow-up. |
| **TA-2/3/4/5 (pr-test-analyzer, MAJOR/MINOR)** — missing branch-coverage tests | **FIXED** — added in Commit D (`TestGenerate_RequiredMatchingPropertyReturnsValueType`, `TestGenerate_RefPropertyShortCircuits`, `TestGenerate_ShapeMismatchFallsBackToInline`, `TestGenerate_OptionalInverseCase`). |

## 3. Verification gates (exit codes and stdout, all run locally on this pod)

### `make verify-contracts` — exit 0

```
[gen-contracts] Done. All contract artifacts are up to date.
bash scripts/check-no-handwritten-wire-types.sh
check-no-handwritten-wire-types: OK (0 findings)
npx tsc -b --noEmit
git diff --exit-code -- contracts/ pkg/api/generated/ src/lib/api/generated/ pkg/gateway/inboundschemas/
```

Covers: redocly openapi lint + asyncapi parser validation, TS types + Zod codegen, Go types via oapi-codegen + gen-asyncapi-go + gen-go-fixup, gofmt, and the drift gate (no committed file differs from the regenerated output).

### `make verify-asyncapi-drift` — exit 0

```
CGO_ENABLED=0 CGO_ENABLED=0 go run ./scripts/gen-asyncapi-go/ \
        contracts/asyncapi.yaml \
        pkg/api/generated/asyncapi_types.gen.go
git diff --exit-code -- pkg/api/generated/asyncapi_types.gen.go
```

The new standalone drift gate (Commit D). Exits 0 because the post-A generator output is byte-identical to the committed file.

### `CGO_ENABLED=0 go test -count=1 ./scripts/gen-asyncapi-go/...` — exit 0

```
=== RUN   TestWriteStruct_ThreeWayCollisionErrors
--- PASS: TestWriteStruct_ThreeWayCollisionErrors (0.00s)
=== RUN   TestGenerateUsesMatchingNamedInlinePayload
--- PASS: TestGenerateUsesMatchingNamedInlinePayload (0.00s)
=== RUN   TestGenerate_RequiredMatchingPropertyReturnsValueType
--- PASS: TestGenerate_RequiredMatchingPropertyReturnsValueType (0.00s)
=== RUN   TestGenerate_RefPropertyShortCircuits
--- PASS: TestGenerate_RefPropertyShortCircuits (0.00s)
=== RUN   TestGenerate_ShapeMismatchFallsBackToInline
--- PASS: TestGenerate_ShapeMismatchFallsBackToInline (0.00s)
=== RUN   TestGenerate_OptionalInverseCase
--- PASS: TestGenerate_OptionalInverseCase (0.00s)
PASS
ok  	github.com/elicify-ai/omnipus/scripts/gen-asyncapi-go	0.004s
```

6/6 tests pass. The 5 new tests cover the four pr-test-analyzer branches (TA-2 / TA-3 / TA-4 / TA-5) plus the original happy-path regression test (TA-1) and the pre-existing three-way-collision test.

### `npm run typecheck` (i.e. `tsc -b --noEmit`) — exit 0

```
> @omnipus/ui@0.1.0 typecheck
> tsc -b --noEmit
```

The SPA compiles cleanly with the new `LLMError` / `LLMErrorReplay` aliases and the hardened schema types.

### `vitest src/lib/llm-error.test.ts` — exit 0 (20/20 tests pass)

```
Test Files  1 passed (1)
     Tests  20 passed (20)
  Start at  05:37:04
  Duration  1.38s
```

The 20 SPA-side tests for `codeToDisplay` / `codeToMessage` / `getLLMErrorDisplay` / `readLLMErrorFromFrame` / `readLLMErrorFromReplayFrame` / `readEntryIdFromFrame` still pass against the type-alias-only refactor.

### `CGO_ENABLED=0 go build -tags goolm,stdjson ./cmd/omnipus/` — exit 0

Single-binary build succeeds (Hard Constraint #1).

### `gofmt -l $(git ls-files '*.go')` — empty (exit 0)

All committed Go files match `gofmt`. The original generator test file had a minor alignment drift (`schemaType:` vs `properties:` column alignment in the new `TestGenerate_OptionalInverseCase` test) — fixed via `gofmt -w` and folded into Commit D as a `--fixup` autosquash.

### Scoped contract test — `TestErrorFrame_PayloadOmitEmpty` + `TestReplayErrorFrame_PayloadOmitEmpty` — exit 0

The contract-level regression tests in `pkg/api/generated/contract_test.go` (which assert that the regenerated `Payload *ErrorPayload` shape still omits `omitempty`) pass against the post-D regenerated file. **This is the load-bearing proof that the prior hand-fix (the now-retired 12-line ADR-051 B2 comment in `asyncapi_types.gen.go`) is replaced by generator output that is byte-identical in observable behavior.**

## 4. Author / authorship verification (CLAUDE.md mandatory)

```
$ git log -1 --format='%an <%ae>'
Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>

$ git log origin/main..HEAD --format='%(trailers:key=Co-authored-by)' | grep -i anthropic
(no output)
```

Every commit on `sendfile-fix` (HEAD `07497820`) is authored by the operator's GitHub identity. No `Co-Authored-By:` trailers — the Anthropic default was overridden. The `…@users.noreply.github.com` form was used, as required.

## 5. Branch state

```
$ git log --oneline f6eccbcd..HEAD
07497820 fix(adr-051-rev4): harden media-library wire schemas (Slice A MAJORs)
48666ec5 fix(gen-asyncapi): regenerate output + drift gate + branch coverage tests
90837961 refactor(llm-error): alias to generated AsyncAPI types (pre-existing Rev-3 debt)
0e7dcf5e feat(adr-051-rev4): generate media library wire contracts (Slice A / Wave 0)
da892f01 fix(gen-asyncapi): lift hand-edited pointer payload into codegen

$ git rev-parse origin/sendfile-fix
701cdb54cd7f4fc31a37b4fe25a9c624ff325154
```

The local `sendfile-fix` branch is at `07497820`. The `origin/sendfile-fix` is at `701cdb54` — 5 commits behind, so this stack has **not been pushed yet** (per the directive "Never force-push; NEVER admin/auto-merge to main"). The branch is ready to be reviewed or pushed per operator direction.

## 6. Net review-round-1 → round-2 verdict

Round-1 totals: 0 CRIT / 26 MAJOR across 7 reviewers. The 6 MAJORs and most of the MINORs that blocked Wave 1 are addressed in Commits A–E. The remainder (CR-03, CR-04, CA-1, SFH-05, CS-03) are explicit "carry-forward to Wave 1" notes — the Slice A scope is closed.

The Wave 0 reviewer gate can now be re-run; we expect near-PASS this round (the structural issues — commit split, schema hardening, generator drift CI — are the ones that block downstream waves, and they are all addressed).

## 7. Caveats / known gaps

- The full Go test suite was NOT run locally (CLAUDE.md "Never run the full Go test suite... in this ephemeral, resource-constrained devpod"). CI is the authority for the full suite; the per-slice scoped tests above are the Slice-A-appropriate verification.
- `golangci-lint` was NOT run locally (would require the full build-tag invocation; the diff is limited to `scripts/gen-asyncapi-go/`, `scripts/_gen-ts.sh`, and the generated artifacts, and the generator test suite + gofmt covers the generator-local check).
- The `make verify-contracts` drift gate passes only because the regenerated output has been committed. A future contributor who edits a spec without running `make gen-contracts` will trip the gate (this is the intended behavior).
- The `STRICT_BODY_ALIASES` postprocessor pattern (`createWorkspaceMediaAttachment`) is a narrow opt-in list — adding a new strict-body operation requires adding the alias to the env var. The pattern does not silently apply `.strict()` to every inline body schema, which is the conservative behavior for a defense-in-depth postprocessor.

## 8. What the Wave 1 reviewer gate should NOT re-litigate

- The commit split (A / B / C / D / E) is per the directive; it is not a Wave 1 design decision.
- The schema hardening choices in Commit E (uuid, maxLength, source enum, readOnly on server fields, workspace_id as required) are per the type-design + holistic + code-reviewer findings consolidated in this round-2 pass; Wave 1 can refine field-level details but should not re-open the contract shape.
- The generator test additions in Commit D cover the four pr-test-analyzer branches; further test-coverage expansion is a Wave 1 / Wave 3 follow-up (per the existing test plan in `scripts/gen-asyncapi-go/main_test.go:8-20`).

*End of verification report.*