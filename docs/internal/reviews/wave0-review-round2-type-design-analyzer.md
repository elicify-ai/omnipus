# Wave 0 / Slice A — type-design-analyzer review (round 2)

**Reviewer:** `pr-review-toolkit:type-design-analyzer` (general subagent with inline role instructions)
**Scope:** re-verify the 10 MAJORs from round 1 against the corrected stack on `sendfile-fix` HEAD `d0e7374a` (5 stacked commits A→E per `docs/internal/reviews/wave0-review-round2-verification.md` §1), then hunt for remaining type-design gaps. Round-1 finding list lives at `docs/internal/reviews/wave0-review-round1-type-design-analyzer.md`.
**Method:** read the round-1 review, the round-2 verification report, the fix commit (`07497820`), and the canonical schemas; re-run the wire-types-lint, the OpenAPI redocly lint, the TypeScript typecheck, the SPA Zod test, and the generator unit test on the working tree; cross-check the Go, TS, and Zod artifacts against the YAML diff.
**Author identity check:** `git log -1 --format='%an <%ae>'` = `Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>`; `git log origin/main..HEAD --format='%(trailers:key=Co-authored-by)' | grep -i anthropic` empty. ✅.

---

## 1. Re-verification of the 10 round-1 MAJORs

Every claim below is grounded in tool results from this round (schema diff `git show 07497820`, regenerated `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/{openapi-types,schemas}.ts`, `pkg/gateway/inboundschemas/*.yaml`, the in-tree redocly + vitest + go-test runs, and the `_gen-ts.sh` body).

| ID | Round-1 finding | Round-2 disposition | Evidence |
|---|---|---|---|
| **M1** | `MediaLibraryEntry.id` required but not readOnly | **FIXED** | `contracts/components/schemas/MediaLibraryEntry.yaml:17-22` now `format: uuid` + `readOnly: true`; `pkg/api/generated/openapi_types.gen.go:5559` `Id *openapi_types.UUID` (pointer + omitempty); `src/lib/api/generated/openapi-types.ts:3049` `readonly id: string;`; Zod unchanged (`z.string().uuid()`). |
| **M2** | `MediaLibraryEntry.uploaded_at` required but not readOnly | **FIXED** | `MediaLibraryEntry.yaml:53-58` `format: date-time` + `readOnly: true`; Go `UploadedAt *time.Time` (pointer + omitempty); TS `readonly uploaded_at: string`; Zod `z.string().datetime({ offset: true })`. |
| **M3** | `MediaLibraryEntry.sha256` required but not readOnly | **FIXED** | `MediaLibraryEntry.yaml:47-52` `pattern: "^[a-f0-9]{64}$"` + `readOnly: true`; Go `Sha256 *string`; TS `readonly sha256`; Zod `z.string().regex(/^[a-f0-9]{64}$/)`. |
| **M4** | `MediaLibraryEntry.mime` required but not readOnly | **FIXED** | `MediaLibraryEntry.yaml:34-38` `readOnly: true`; Go `Mime *string`; TS `readonly mime`. (Side-effect: `minLength: 1` was silently dropped — see m-regression below.) |
| **M5** | `MediaLibraryEntry.size` required but not readOnly | **FIXED** | `MediaLibraryEntry.yaml:39-46` `format: int64`, `minimum: 0`, `maximum: 104857600`, `readOnly: true`; Go `Size *int64`; TS `readonly size: number`; Zod `z.number().int().gte(0).lte(104857600)`. |
| **M6** | `MediaLibraryEntry.size` missing `maximum` (100 MB cap) | **FIXED** | `MediaLibraryEntry.yaml:43` `maximum: 104857600`; description cross-references `maxUploadFileSize` per ADR-051 Rev 4. Zod upper bound `lte(100MB)` matches. Closes the invariant-expression gap. |
| **M7** | `MediaLibraryEntry.id` missing `format: uuid` | **FIXED** | `MediaLibraryEntry.yaml:19` `format: uuid`; Zod `z.string().uuid()`; Go runtime `openapi_types.UUID` is `github.com/google/uuid.UUID` (alias), so JSON unmarshal enforces UUID format via `uuid.UnmarshalJSON`. |
| **M8** | `MediaAttachmentRequest.media_id` missing `format: uuid` | **FIXED** | `MediaAttachmentRequest.yaml:12-13` `format: uuid` + `maxLength: 36`; Zod `z.string().max(36).uuid()`; Go `MediaId openapi_types.UUID` (required, not pointer). |
| **M9** | `MediaLibraryEntry.filename` missing `maxLength` | **FIXED** | `MediaLibraryEntry.yaml:31` `maxLength: 256` (POSIX-ish cap; the spec's `<= 128` is the *content-injection* cap per FR-023a, a separate downstream limit — see observation O-2). |
| **M10** | `MediaAttachmentRequest.content_injection_override` attacker-controlled LLM-bound text | **FIXED** (holistic m-1) | **Field dropped entirely** — `MediaAttachmentRequest.yaml:1-15` now carries only `media_id`. Per the holistic review's stronger signal, the override path is an internal orchestrator concept, not a SPA-API concept. `position` (also dropped) closes CR-02/CS-02. |

**Net:** M1–M10 all correctly applied. The cross-cutting structural change — adding `workspace_id` as required (resolves the holistic m-2 carry-forward), converting `source` from a free string to a closed enum `[user_upload, tool_output, test_fixture]` (resolves m-3), and threading the new `CR-01` strict-schema postprocessor plus `STRICT_BODY_ALIASES` inline-body `.strict()` fix into `scripts/_gen-ts.sh` — is well-grounded. The `_gen-ts.sh` postprocessor now matches both typed (`export const Name: z.ZodType<Name> = …`) and untyped (`export const Name = …`) declarations (the CR-01 gap), and the inline body schema for `createWorkspaceMediaAttachment` is now strict at the validation point (`src/lib/api/generated/schemas.ts:7239` `z.object({ media_id: z.string().max(36).uuid() }).strict()`).

## 2. Verification evidence (re-run on this pod)

```
$ bash scripts/check-no-handwritten-wire-types.sh
check-no-handwritten-wire-types: OK (0 findings)

$ npx --no-install @redocly/cli lint contracts/openapi.yaml --skip-rule no-server-example.com
contracts/openapi.yaml: validated in 721ms
Woohoo! Your API description is valid.

$ npm run typecheck
> tsc -b --noEmit
(exit 0)

$ npx vitest run src/lib/llm-error.test.ts
Test Files  1 passed (1)
     Tests  20 passed (20)

$ CGO_ENABLED=0 go test -count=1 -tags goolm,stdjson ./scripts/gen-asyncapi-go/
ok   github.com/elicify-ai/omnipus/scripts/gen-asyncapi-go   0.004s   (6/6 PASS)

$ CGO_ENABLED=0 go vet -tags goolm,stdjson ./pkg/api/generated/...   (clean)

$ bash scripts/gen-contracts.sh   (re-run idempotent, no git diff)
[gen-contracts] Done. All contract artifacts are up to date.
```

The `git diff --stat HEAD -- contracts/ pkg/api/generated/ src/lib/api/generated/ pkg/gateway/inboundschemas/` after re-running `gen-contracts.sh` is empty — the drift gate (`make verify-contracts`) passes. The `pkg/gateway/inboundschemas/*.yaml` mirrors are `diff -u`-identical to the canonicals (verified). The `media_id` path parameter in `contracts/openapi.yaml:5511-5528` is `type: string` (no `format: uuid` at the path parameter level — see O-3).

## 3. Findings table — round 2

| ID | Severity | schema:field (or scope) | One-line | Fix |
|---|---|---|---|---|
| **M11** | MAJOR | `MediaLibraryEntry.workspace_id` | `required` + `format: uuid` but **not** `readOnly: true`. The field is server-assigned at upload time (every entry is bound to exactly one workspace per the YAML description and FR-028a). The same consistency principle M1 applied to `id` applies here: the schema is now 6/8 required-fields readOnly (`id`, `sha256`, `mime`, `size`, `uploaded_at`, `workspace_id?-no`, `refcount`, `last_refcount_seen_at`) — and `workspace_id` is the only server-assigned *required* field missing `readOnly: true`. TS surface confirms: `openapi-types.ts:3055` `workspace_id: string;` whereas `openapi-types.ts:3049` is `readonly id: string;`. If a future write path lands, the schema gives a client the wire-impression that `workspace_id` is client-settable — and the FR-028a cross-workspace guard depends on the server trusting this field, so any spoof vector is worth pre-closing. | Add `readOnly: true` to `workspace_id` in `MediaLibraryEntry.yaml` and regenerate. (Note: `workspace_id` is path-parameter-equivalent — the URL `/workspaces/{id}/media/{media_id}` already carries the workspace ID — so this is purely a defense-in-depth wire invariant, not a behavioral change.) |
| m6 | MINOR | `MediaLibraryEntry.mime` | Commit E DROPPED `minLength: 1` from `mime` while only adding `readOnly: true`. The Zod artifact is now `mime: z.string()` (line 2389 of `schemas.ts`) — strict regression. The round-1 schema had `minLength: 1` as a defensive guard (and the round-1 m1 was *only* asking for a regex, not for the minLength to be removed). With `readOnly: true` the server is the only writer, but the Zod is also used to validate **response** payloads in the SPA edge: a server bug that emits `""` would silently pass validation rather than surface as a schema error. | Restore `minLength: 1` (cheap) AND add an RFC 6838 `pattern: "^[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+$"` (the actual round-1 m1 ask). |
| m7 | MINOR | `MediaLibraryEntry.source` enum values | The schema enum is `[user_upload, tool_output, test_fixture]`. The spec references agent-generated media with the literal scope `tool:inline:session:<id>` (US-3 AC1, FR-005 at `spec.md:1067`, BDD scenario at `:123,492`, test row at `:925`). The schema value `tool_output` is a generalization that **does not match the spec's literal terminology**. The description captures the intent ("session-scoped, never migrated") but the wire value differs. Wave 1+ B1 will write this field; the spec↔schema gap forces a translation layer or a spec amendment. | Either (a) align the spec to use `tool_output` (and document in the FR the scope mapping), or (b) qualify the enum values with the spec's existing scope vocabulary (e.g., add `agent_tool_inline_session`, deprecate `tool_output`). The `test_fixture` value is also spec-absent — its purpose is documented only in the schema description. |
| m8 | MINOR | `MediaLibraryEntry.filename` `maxLength` cap | The schema `maxLength: 256` (storage cap) and FR-023a `length-capped (≤ 128 chars)` (content-injection cap) are **two separate limits** (256 = storage, 128 = LLM-bound substring). The current description covers only the 256 storage cap. A future reader of the schema won't know about the 128 cap. Low-risk but the spec↔schema chain is incomplete. | Add to the filename description: "Server-side downstream sanitization caps *content-injected* filenames at 128 chars per FR-023a — this 256 cap is the storage cap." |
| m9 | MINOR | `path parameter media_id` in `contracts/openapi.yaml:5511-5528` | The path parameter is `type: string` with no `format: uuid` or pattern. The match body of `MediaAttachmentRequest.media_id` (now `format: uuid + maxLength: 36`) and the field `MediaLibraryEntry.id` (now `format: uuid`) are typed UUIDs, but the path parameter is open. Round-1 CA-3 noted the same gap and the round-2 fix closed it for the body fields but not the path parameter. | Add `schema: { type: string, format: uuid, maxLength: 36 }` to the `media_id` path parameter. |
| m10 | MINOR | `src/lib/llm-error.ts` | File ends with `}` at line 173 with no trailing newline (round-1 m6). Git still records `\ No newline at end of file` for this file. Carry-forward unchanged. | Add a trailing `\n` (one-byte fix). |
| O1 | OBS | `MediaLibraryEntry.last_refcount_seen_at` | Still undocumented in the spec (round-1 holistic o-4, CS-03). Round-2 disposition: DEFERRED to Wave 1 B1. The field is `readOnly: true` + optional, so the wire-shape is harmless — but the spec↔schema chain carries a known spec-omission. | Track in B1 PR description. |
| O2 | OBS | `MediaLibraryEntry.filename` 256 vs 128 | The description's "256-char cap mirrors POSIX filename limits" is correct but elides the spec's 128-char *content-injection* cap. Two separate limits; readers should not infer the 256 cap implies no further sanitization. See m8. | Already covered. |
| O3 | OBS | `path parameter id` in `contracts/openapi.yaml:5450-5468, 5510-5528` | The `/workspaces/{id}/media*` paths use `type: string` for the workspace ID path parameter. The new `MediaLibraryEntry.workspace_id` body field is `format: uuid` — the path parameter is the same logical identifier but is not UUID-constrained. Same shape-of-gap as m9. | Add `format: uuid` to the path parameter `id` on the three new operations. |
| O4 | OBS | `MediaLibraryEntry.size` `maximum` description | The description correctly cross-references `maxUploadFileSize` per ADR-051 Rev 4, but the spec carries the **config knob** (`pkg/gateway/rest.go:8699` per `spec.md:47`) — the schema description is the only place the constant 100 MB is named at the wire boundary. Acceptable but if `maxUploadFileSize` ever changes, the wire `maximum: 104857600` is fixed. The round-2 fix is correct (the hard constant matches the spec's named knob today), but the description should reference the operator-configurable knob too. | Add "(operator-configurable via `maxUploadFileSize`)" to the size description. |
| O5 | OBS | `MediaLibraryEntry.source` enum — `test_fixture` | `test_fixture` is a new value added in commit E with no spec entry. The description ("never emitted by the live upload path") is a sound server-side promise, but the wire contract is permissive (the test path can emit it). Anyone reading the spec future will not find `test_fixture` documented. | Either (a) document `test_fixture` in the spec as a testing-only emission, or (b) split into a separate `MediaLibraryEntryTestSource` extension type. |
| O6 | OBS | `OpenAPI 3.0.3` `format: uuid` semantics | The contract is OpenAPI 3.0.3 (not 3.1). In 3.0.3, `format: uuid` is a *documentation hint*, not a constraint. The actual UUID validation is enforced at three other layers — `z.string().uuid()` on the SPA edge, `github.com/google/uuid.UUID` JSON unmarshal on the Go side, and the path-parameter m9 gap noted above. The wire-types-lint (`scripts/check-no-handwritten-wire-types.sh`) still passes and the regenerate is byte-identical (+drift gate), but the spec's `format` keyword is weaker than the round-2 description claims. | Optional: tighten the round-2 description to read "format: uuid (codegen-enforced via Zod `z.string().uuid()` and Go `openapi_types.UUID`)" — make the secondary enforcement explicit. The wire-shape strictness is intact; this is a documentation alignment. |
| O7 | OBS | Operation surface in `openapi.yaml` (`/workspaces/{id}/media*`) | The three operations remain (per CR-02 / CS-02 disposition: keep the operations to anchor the `$ref` resolutions per `skip-prune: true`). Their `404` fallback in `HandleWorkspaces` (`pkg/gateway/rest_workspaces.go:494-544`) is the documented Wave 0 behavior. Wave 1+ B1/H owns the implementation. Type-design perspective: the operations are now spec-stable wire anchors — no type-design concern. | Track in Wave 1; no Slice A change. |
| O8 | OBS | `MediaAttachmentRequest` body schema strictness | `src/lib/api/generated/schemas.ts:7239` shows the inline body schema is rewritten `z.object({ media_id: z.string().max(36).uuid() }).strict()` — the new `STRICT_BODY_ALIASES` postprocessor (`scripts/_gen-ts.sh:143-188`) correctly added `.strict()` to the inlined single-field body. The pattern is narrow opt-in (one alias today) — adding a new strict-body operation requires extending `STRICT_BODY_ALIASES`. This is conservative behavior for a defense-in-depth postprocessor and is documented in the round-2 verification report §7. | None — noting the gate is intentionally narrow. |

## 4. Net verdict

**PASS-WITH-NITS** — 0 CRITICAL, **1 MAJOR** (M11), **5 MINOR** (m6, m7, m8, m9, m10), **8 OBSERVATION** (O1–O8).

The 10 round-1 MAJORs (M1–M10) are all correctly closed in commit E:
- M1–M5 (readOnly omissions) — `readOnly: true` correctly applied to `id`, `sha256`, `mime`, `size`, `uploaded_at`; the structural fix is consistent across YAML, Go (pointer + omitempty), TS (`readonly`), and Zod (unchanged behavior).
- M6 (size maximum) — `maximum: 104857600` exactly matches the spec's `maxUploadFileSize` 100 MB cap; Zod upper bound matches.
- M7, M8 (UUID format) — `format: uuid` on `id`, `workspace_id`, `media_id`; runtime validation enforced via `z.string().uuid()` (SPA) and `openapi_types.UUID` aliasing `github.com/google/uuid.UUID` (Go).
- M9 (filename maxLength) — `maxLength: 256` mirrors POSIX NAME_MAX; the 128-char content-injection cap from FR-023a is a separate downstream limit (m8 carries the description-alignment nit).
- M10 (content_injection_override) — field dropped, closing the holistic-m-1 + CR-02 + CS-02 trio together.

The remaining MAJOR-equivalent (M11) is a consistency-of-fix nit: `workspace_id` is the only newly-added or remaining server-assigned required field NOT marked `readOnly: true`. The same logic M1-M5 applied to the other ID/integrity fields applies to `workspace_id` (server-assigned, identity-discriminator, FR-028a trust-bearing). It's a one-line YAML change and a 2-line TS diff (add `readonly`), but the type-design invariant is the same as the M1 principle.

The minor cluster is a mix of (a) round-1 carry-forwards not addressed in commit E (m6 mime regression — `minLength: 1` dropped without replacement; m7 source enum terminology mismatch with spec; m10 trailing newline), (b) new gaps surfaced by the round-2 fix (m8 missing cross-reference to the 128-char content-injection cap; m9 path parameter still unconstrained), and (c) observations that are intentional design choices (O1, O4, O5) or wire-lint positives (O8).

**Recommendation for the operator:**
- **M11 should land in the same Wave 0 corrections commit** (or as a stacked fix on top of `07497820`). The change is mechanical: add `readOnly: true` to `workspace_id` in `MediaLibraryEntry.yaml` and regenerate. The TS surface gains `readonly workspace_id: string;` (consistent with `readonly id: string;` three lines above). The Go type is `WorkspaceId openapi_types.UUID` (required, no omitempty) — `readOnly: true` in YAML maps to pointer + omitempty in Go, so the Go type will become `WorkspaceId *openapi_types.UUID` — but readers should be aware this is a load-bearing schema invariant, not a behavioral change.
- **m6 is a regression** that should be reverted in the same fix (restore `minLength: 1` on `mime`). Cheap.
- **m7, m8, m9, m10** can either bundle with M11 or punt to Wave 1's round-1 reviewer; none block Wave 1.
- **Observations** are advisory.

The Slice A wire work is otherwise ready to merge. The `make verify-contracts` drift gate is clean (re-run idempotent, no diff). The Zod, TS, and Go artifacts are consistent with the canonical YAML. The `STRICT_BODY_ALIASES` postprocessor is a clean net-positive for Constraint #8 (closes the CR-01 gap at the inline-body-schema validation point without broadening the postprocessor's scope). The generator test additions in commit D cover the four pr-test-analyzer branches. The CLAUDE.md mandatory authorship rule is satisfied.

**End of round-2 review.** READ-ONLY — no files were modified by this reviewer.
