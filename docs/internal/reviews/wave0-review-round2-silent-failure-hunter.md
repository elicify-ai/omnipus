# Wave 0 Review — Round 2 — Silent Failure Hunter

**Scope:** `f6eccbcd..d0e7374a` (`sendfile-fix`, Slice A / FR-031 — Contracts foundation, round-2 corrections)
**Reviewer role:** `silent-failure-hunter` (pr-review-toolkit)
**Review focus:** silent failures in error handling, inadequate fallback behavior, catch blocks that swallow errors, fallback logic that could mask real problems. Re-verify the round-1 MAJORs against the corrected Wave 0 stack and hunt for any new silent-failure shapes introduced by the round-2 commits (Commits A through E + Commit F).

---

## Findings

| ID | Severity | File:line | One-line | Fix |
|---|---|---|---|---|
| **SFH-R2-01** | **MAJOR (residual)** | `scripts/gen-asyncapi-go/main.go:471-514` (`sameSchemaShape`) | **SFH-02 (round 1) NOT ADDRESSED.** The drift gate added in round 2 (`make verify-asyncapi-drift`) catches STRUCTURAL drift, but description / title / default are still ignored by `sameSchemaShape`. **Verified empirically**: changing `description` on the named `ErrorPayload` (line 2865 of `contracts/asyncapi.yaml`) produces a byte-identical regenerated file — `make verify-asyncapi-drift` exits 0. The "what did the named schema say?" information is still silently lost on the Go side; the SPA-side Zod codegen reads the inline mirror which has no description anyway. | Add `description` (and optionally `title`, `default`) to the `sameSchemaShape` comparison (option A in the round-1 review); OR add a CI lint step that asserts `description` text parity between each inline mirror and its named source. The round-1 reviewer's preferred option (B — CI-lint) is the conservative call because it keeps the generator's "structural-only" doctrine. |
| **SFH-R2-02** | **MAJOR (residual)** | `src/lib/llm-error.ts:68-73` (`codeToMessage`) + `pkg/agent/translate_error.go:99-100` (`defaultUserMessage`) | **SFH-05 (round 1) NOT ADDRESSED.** Both sides silently fall back to `unknown` / `CodeUnknown` for an unrecognized code without any telemetry, console.warn, or metric. The round-2 verification report marks this as "OUT OF SCOPE for Slice A — Wave 1 round-1 follow-up". The Go-side equivalent (`defaultUserMessage` in `pkg/agent/translate_error.go:99-100`) has the same shape — silent forward-compat fallback to a generic "the AI service encountered an error" message. | Add a single `if (code && !(code in codeToDisplay)) { console.warn(...) }` (SPA) and `if msg == userMessages[CodeUnknown] { log.Warn("unrecognized LLM error code", "code", code) }` (Go). The fallback-to-unknown is correct (never throw, never blank); the observability gap is the concern. Wave 1 round 1. |
| **SFH-R2-03** | **MAJOR (residual)** | `scripts/gen-asyncapi-go/main.go:419-465` (`matchingNamedInlineGoType`) | **SFH-01 (round 1) PARTIALLY ADDRESSED by the drift gate, NOT by the heuristic.** The function itself is unchanged — it still silently coerces any structurally-matching inline shape to `*Name`. The new `make verify-asyncapi-drift` is the safety net for STRUCTURAL drift on the Go side; the heuristic's own guard fence is still absent (no `x-mirror-of` opt-in, no warning log). The new doc comment (lines 425-441) explicitly documents the heuristic's "silent fallback to anonymous struct" semantics — but documentation is not enforcement. | The drift gate covers the structural drift case, but the heuristic itself should still log a one-time DEBUG/WARN when a matching inline is coerced (e.g., `fmt.Fprintf(os.Stderr, "matching inline to %s for %s.%s\n", ...)` behind a `-v` flag). Low priority — the gate is the load-bearing fix; this is belt-and-braces. |
| **SFH-R2-04** | **MAJOR (residual)** | `scripts/gen-asyncapi-go/main.go:106, 163-168, 484` (`schema.additionalProps` bool) | **SFH-03 (round 1) DORMANT in code, LIVE in drift-gate coverage.** The `bool` capture is still insufficient for `additionalProperties: { type: "..." }` (schema form). **Verified empirically**: `make verify-asyncapi-drift` DOES catch `additionalProperties: false` → `additionalProperties: true` on the inline mirror (output reverts from `*ErrorPayload` to `map[string]any`; gate exits 2). The failure mode is therefore covered by the gate for the surfaces used today (`additionalProperties: true | false`); the bool capture is sufficient because the asyncapi.yaml only uses these two forms. The schema-form (`additionalProperties: { type: "string", ... }`) is a hypothetical risk: if it lands, both `sameSchemaShape` and `resolveGoType` would silently treat it the same as `additionalProperties: true` (a `map[string]any`). | Add a regression test that introduces an `additionalProperties: { type: "string" }` schema (e.g., on a synthetic Frame property) and asserts that `sameSchemaShape` distinguishes it from `additionalProperties: true`. If the test passes, mark SFH-03 closed; if it fails, extend the `additionalProps` capture to a `*schema` field. |
| **SFH-R2-05** | **MAJOR (residual)** | `contracts/asyncapi.yaml:2863-2882` (inline `ErrorPayload` / `ReplayErrorPayload`) | **SFH-04 (round 1) ADDRESSED for structural drift, NOT addressed for the dead-file drift.** The `make verify-asyncapi-drift` gate catches structural drift on the inline mirror in `asyncapi.yaml`. **However**, `contracts/components/schemas/ErrorPayload.yaml` and `ReplayErrorPayload.yaml` are dead files from the generator's perspective — the generator reads ONLY `contracts/asyncapi.yaml` (verified: editing `ErrorPayload.yaml` to add `new_field` produced a byte-identical generator output; `make verify-asyncapi-drift` exits 0). A future spec author who updates the standalone `ErrorPayload.yaml` expecting it to propagate will be silently ignored. The same is true for `inboundschemas/ErrorPayload.yaml` which is a copy-on-write of the same dead schema. | Either (a) delete the dead `contracts/components/schemas/{ErrorPayload,ReplayErrorPayload}.yaml` and `pkg/gateway/inboundschemas/{ErrorPayload,ReplayErrorPayload}.yaml` files (they have no generator consumer and the inline definition in `asyncapi.yaml` is the actual source of truth), OR (b) replace the inline definitions in `asyncapi.yaml` with `$ref: '#/components/schemas/ErrorPayload'` where the file becomes the source of truth. The current state has THREE competing sources of truth (asyncapi.yaml inline, contracts/components/schemas/*.yaml, inboundschemas/*.yaml) — this is exactly the drift surface SFH-04 was warning about. |
| **SFH-R2-06** | **MAJOR (NEW)** | `scripts/_gen-ts.sh:113-135` (`STRICT_SCHEMAS` postprocessor) | **NEW silent-failure shape.** The `STRICT_SCHEMAS` env var is a hardcoded list (`FallbackModel AgentCreateRequestMain AgentCreateRequestSubagent AgentCreateRequestSubagent3p MediaLibraryEntry MediaAttachmentRequest`). The postprocessor's `if grep -qE "^export const ${name}(:| =)" "$STRICT_RAW"` has NO `else` clause — if a future maintainer renames a schema (e.g., `MediaLibraryEntry` → `MediaLibraryEntryV2`) and forgets to update `STRICT_SCHEMAS`, the postprocessor silently skips the rewrite and the schema is emitted WITHOUT `.strict()`. At runtime, the Zod validator would accept unknown fields silently (no error, no log). The drift gate (`make verify-contracts`) does NOT catch this — the generator output is byte-stable, and `tsc -b --noEmit` is happy because TypeScript interfaces don't enforce extra-field rejection. This is exactly the failure mode CR-01 (round-1 code-reviewer) was warning about, only now re-introducible via a rename rather than via a default-format change. | Add `else { echo "STRICT_SCHEMA '${name}' not found in generator output — was the schema renamed without updating STRICT_SCHEMAS?"; exit 1; }` after the `if grep -qE ...` check. Fail loud, not silent. Alternative: drive the `.strict()` rewrite from the YAML `additionalProperties: false` annotation directly via a sed pass on the YAML→TS codegen, eliminating the manual env-var list entirely. |
| **SFH-R2-07** | **MINOR** | `contracts/asyncapi.yaml:1416-1423, 1880-1886` (inline-mirror comments) | **Stale comments, deferred from round 2 (CA-1).** The comments say "The Go-side pointer fix lives in asyncapi_types.gen.go (hand-adjusted Payload → *ErrorPayload so omitempty works; search for the ADR-051 B2 comment there)." The hand-adjustment is gone since Commit A (`da892f01`); the generator now emits `Payload *ErrorPayload` directly via the new `matchingNamedInlineGoType` heuristic. A maintainer who reads the comment may try to hand-edit `asyncapi_types.gen.go` and have the generator overwrite it on the next regeneration — exactly the failure mode the comment itself was trying to warn about. This is a documentation drift, not a runtime silent failure, but it IS a silent-failure risk for the future maintainer. | Update the comments to: "Kept inline because the Zod codegen evaluates schemas eagerly at module load (Zod expressions, not lazy type aliases) — a `$ref` forward reference trips a TDZ error in generated schemas.ts. The Go-side pointer shape (`*ErrorPayload`) is now emitted directly by the generator's `matchingNamedInlineGoType` heuristic in `scripts/gen-asyncapi-go/main.go:419-465`; do not hand-edit `asyncapi_types.gen.go`." Tracked as CA-1 in the round-2 verification; safer to land as a docs-only Wave 1 round-1 commit than to bundle into Slice A. |
| **SFH-R2-08** | **MINOR** | `src/lib/llm-error.ts:6-27` (header JSDoc) | **No remaining JSDoc drift; the WHY is preserved.** Round 1 noted (SFH-07) that the removed header JSDoc documented the critical invariant "if the spec adds a new code, regenerate the contract and add it here in the same PR." Round 2's rewrite (lines 6-27) re-establishes the same invariant via the `Record<LLMErrorCode, string>` compile-time gate explanation. No drift; nothing to fix here. The hand-written enum was correctly retired (no in-source-of-truth duplication; the generated `LLMError.code` union is the single source). | None — closed. |

---

## Re-verification of round-1 MAJORs (one-line each)

| ID | Round 1 status | Round 2 status | Evidence |
|---|---|---|---|
| SFH-01 (greedy heuristic) | MAJOR | MAJOR (residual) — drift gate covers the structural-drift blast radius; heuristic itself unchanged | `matchingNamedInlineGoType` body in `scripts/gen-asyncapi-go/main.go:446-465` is byte-identical to round 1. New doc comment (lines 425-441) acknowledges the cost; no guard added. |
| SFH-02 (description/title/default ignored) | MAJOR | MAJOR (residual) — empirically CONFIRMED that drift gate does NOT catch description drift | Changed `ErrorPayload.description` to "...CHANGED." in `contracts/asyncapi.yaml:2865`; ran `make verify-asyncapi-drift`; exit code 0, byte-identical `asyncapi_types.gen.go`. Description drift is still silent on the Go side. |
| SFH-03 (additionalProps bool) | MAJOR | MAJOR (residual — DORMANT for current usage, LIVE in drift-gate coverage) — empirically confirmed the gate catches `additionalProperties: false → true` drift | Changed inline mirror `additionalProperties: false` → `additionalProperties: true` in `contracts/asyncapi.yaml:1427`; `make verify-asyncapi-drift` exits 2 (output reverts to `Payload map[string]any`). The bool capture is sufficient because no `additionalProperties: { schema }` forms exist in the spec today. |
| SFH-04 (asyncapi SPA-side drift gate) | MAJOR | MAJOR → MAJOR (residual) — gate catches STRUCTURAL drift on the Go side; DEAD-FILE drift on `contracts/components/schemas/*.yaml` is not caught | Confirmed via 4 drift scenarios: (1) add `new_field` to named `ErrorPayload` → gate exits 2; (2) add field to inline mirror only → gate exits 2; (3) change `additionalProperties: false → true` on inline mirror → gate exits 2; (4) description-only change → gate exits 0 (SFH-02 residual); (5) edit `contracts/components/schemas/ErrorPayload.yaml` → gate exits 0 (dead file). |
| SFH-05 (llm-error.ts observability) | MAJOR | MAJOR (residual, deferred to Wave 1) | `codeToMessage` (line 68-73) unchanged. `defaultUserMessage` Go equivalent (`pkg/agent/translate_error.go:99-100`) carries the same shape. |

---

## Rejected candidates

- **The new `STRICT_BODY_ALIASES` postprocessor** (`scripts/_gen-ts.sh:148-184`) — explicitly fails loud (exits 1) if the named body schema is not found in the inline rewrite. Not silent.
- **The new `UNION_SATISFIES_SCHEMAS` postprocessor** (`scripts/_gen-ts.sh:194-217`) — fails loud (exits 1) if any of the named schemas is not found. Not silent.
- **The new comment-injection step** (`scripts/_gen-ts.sh:239-280`) — uses `console.warn` (not exit 1) for unknown schemas, but the injection is OPTIONAL (no-op if `POLICY_COMMENTS` doesn't match). Only `MessageFrame` is annotated today; adding another would not silently fail.
- **The `package.json` `STRICT_SCHEMAS` env override** — the env var is opt-in for downstream consumers; defaults are committed in the script. If a future consumer overrides it with a stale list, the same silent-skip failure mode applies, but the blame lives with the consumer's override.
- **The contract tests `TestContract_ErrorFrame_LLMErrorInvalidCode` / `TestContract_ErrorFrame_LLMErrorMissingRetryable` / `TestContract_ErrorFrame_LLMErrorUnknownProperty`** (`pkg/api/generated/contract_test.go:484-541`) — all three assert the JSON-Schema validator rejects malformed payloads. Loud (assertion failure), not silent.
- **The `description: Structured payload for a live error frame.` on the named `ErrorPayload` is NOT propagated to the inline mirror.** This is by design — the SPA-side Zod codegen reads the inline mirror's structure (which has no description), and `sameSchemaShape` ignores description. The information loss is real but is consistent with the generator's "structural-only" doctrine and is caught by the `make verify-contracts` drift gate only if the structural fields change. Not a new silent-failure shape — it's the same SFH-02 residual.
- **The `pkg/workspace/media_delete.go` and `pkg/media/library/`, `pkg/media/resize/`, `pkg/providers/capabilities/` directories** — these are untracked files in the working tree (per `git status`) and are NOT part of the `sendfile-fix` round-2 stack (`f6eccbcd..d0e7374a`). They belong to a different wave; not in scope for this review.
- **`fmt.Fprintf(buf, "\t// %s\n", desc)` in `main.go:416`** — description IS emitted as a Go struct field comment. But the Go struct field comment is the resolved property's description, not the named schema's description. So the generator emits a description-based drift into the generated Go comments only if the INLINE shape's property has a description — the named schema's description is ignored. Same SFH-02 residual.

---

## New silent-failure shapes introduced by round 2

- **SFH-R2-06** (`STRICT_SCHEMAS` silent skip) — new shape, MAJOR. Documented above.
- **SFH-R2-05** (`contracts/components/schemas/{ErrorPayload,ReplayErrorPayload}.yaml` are dead from the generator's perspective) — new shape surfaced by round 2's drift-gate addition (the gate's existence highlighted that the gate only covers `asyncapi.yaml`). Documented above.
- **The `_gen-ts.sh` postprocessors** are loud (exits 1 on miss), not silent. Documented above.
- **The regenerated artifacts** (`pkg/api/generated/asyncapi_types.gen.go`, `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/{asyncapi-types,openapi-types,_asyncapi-zod-schemas.generated,schemas}.ts`) are byte-identical to round 1 except where round-2 edits added new schemas (MediaLibraryEntry, MediaAttachmentRequest) and the `matchingNamedInlineGoType` heuristic fixed the inline-mirror pointer shape. No new silent shapes from the regeneration itself.

---

## Verification observed

- `make verify-asyncapi-drift` — exit 0 (clean baseline; matches committed `pkg/api/generated/asyncapi_types.gen.go`).
- `CGO_ENABLED=0 go test -count=1 ./scripts/gen-asyncapi-go/...` — 6/6 PASS (the round-2 added branch-coverage tests TA-2 / TA-3 / TA-4 / TA-5 + the round-1 happy-path test + the pre-existing three-way-collision test).
- `npm run typecheck` (`tsc -b --noEmit`) — exit 0.
- `npx vitest run src/lib/llm-error.test.ts` — 20/20 PASS.
- `git diff --exit-code contracts/asyncapi.yaml contracts/components/schemas/*.yaml pkg/api/generated/asyncapi_types.gen.go` — empty (state restored after drift experiments; no leftover edits in the working tree).
- Empirical drift-gate coverage (4 scenarios):
  - Description-only change to named `ErrorPayload` → drift gate exit 0 (NOT caught; SFH-02 residual).
  - Add `new_field` to named `ErrorPayload` → drift gate exit 2 (caught).
  - Add field to inline `ErrorFrame.payload` mirror only → drift gate exit 2 (caught).
  - Add required field to inline mirror only → drift gate exit 2 (caught).
  - Change `additionalProperties: false → true` on inline mirror only → drift gate exit 2 (caught).
  - Edit `contracts/components/schemas/ErrorPayload.yaml` (the dead file) → drift gate exit 0 (NOT caught; SFH-R2-05).
  - All drift experiments restored via `git checkout contracts/asyncapi.yaml contracts/components/schemas/ErrorPayload.yaml`; final `git status --short` shows the working tree clean of spec changes.

## Gaps in verification

- I did NOT run the full Go test suite (per CLAUDE.md resource constraint on the devpod).
- I did NOT run `golangci-lint` (would require the full build-tag invocation; the diff is limited to `scripts/gen-asyncapi-go/`, `scripts/_gen-ts.sh`, and the regenerated artifacts; the generator's own test suite + gofmt covers the generator-local check).
- I did NOT exercise the runtime behavior of an ErrorFrame round-trip (Go marshal → SPA Zod parse → render) — the fixes are at the generator level; behavioral verification is the Wave 4 T15 SC-observation pass.
- I did NOT test the `STRICT_SCHEMAS` rename-silent-skip failure mode empirically — that requires modifying the YAML in a way that's outside the current diff. The shape is reasoned from the script's `if grep -qE ...; then ... fi` structure with no `else` branch.

---

## Verdict

**PASS WITH FOLLOW-UPS — 0 CRIT / 3 MAJOR (residual) / 2 MAJOR (new) / 2 MINOR / 0 OBS.**

(Total: **5 MAJOR** + 2 MINOR, of which **3 are round-1 residuals** that the round-2 commits explicitly addressed in some form (drift gate for SFH-01/03/04; deferred for SFH-05) and **2 are new findings** introduced or surfaced by the round-2 stack.)

The corrected Wave 0 stack is materially better than round 1:
- The `matchingNamedInlineGoType` heuristic now produces the right `*ErrorPayload` pointer for the current spec (verified byte-identical to the previously hand-adjusted file).
- The `make verify-asyncapi-drift` gate catches STRUCTURAL drift between the inline mirror and the named schema on the Go side — verified via 4 empirical scenarios.
- The `llm-error.ts` refactor correctly aliases the generated types and preserves the `Record<LLMErrorCode, string>` compile-time gate (no behavioral change, no drift).
- The 4 new branch-coverage tests (`TestGenerate_RequiredMatchingPropertyReturnsValueType`, `TestGenerate_RefPropertyShortCircuits`, `TestGenerate_ShapeMismatchFallsBackToInline`, `TestGenerate_OptionalInverseCase`) cover the four pr-test-analyzer branches.
- The `MediaLibraryEntry` and `MediaAttachmentRequest` schemas are correctly hardened (uuid, maxLength, readOnly, source enum, workspace_id required) and `.strict()` is applied via the `STRICT_SCHEMAS` postprocessor on both the named schemas AND the inline body of `createWorkspaceMediaAttachment`.

The 5 MAJOR follow-ups are NOT blockers for Slice A to merge — they are carry-forwards to Wave 1 round 1:
- **SFH-R2-01** (description drift), **SFH-R2-02** (unrecognized-code observability) — independent of Slice A; pre-existing Wave 1 debt.
- **SFH-R2-03** (greedy heuristic guard) — the drift gate is the load-bearing fix; the heuristic's own observability is belt-and-braces.
- **SFH-R2-04** (`additionalProps` schema form) — dormant; no regression test needed until the schema form lands.
- **SFH-R2-05** (dead `ErrorPayload.yaml` / `ReplayErrorPayload.yaml` files) — should be cleaned up as a docs-only commit before Wave 1 round 1 review.
- **SFH-R2-06** (`STRICT_SCHEMAS` silent skip) — should be patched in the same Wave 1 round 1 commit as SFH-R2-05 since both are about postprocessor robustness.

The 2 MINORs (SFH-R2-07 stale comments, SFH-R2-08 closed) are documentation touch-ups.

Slice A / Wave 0 is ready to merge pending the Wave 0 reviewer gate verdict (other reviewers' round-2 reports should agree before sign-off).

*End of review.*