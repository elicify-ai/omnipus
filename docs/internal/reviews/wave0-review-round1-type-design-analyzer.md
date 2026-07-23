# Wave 0 / Slice A — type-design-analyzer review (round 1)

**Reviewer:** `pr-review-toolkit:type-design-analyzer` (general subagent with inline role instructions)
**Scope:** Commit `08690ff9` on `sendfile-fix` vs parent `f6eccbcd` (claims to be Slice A / Wave 0 = FR-031 contracts foundation).
**Diff stat:** `12 files changed, 745 insertions(+), 82 deletions(-)` — see `git diff f6eccbcd..08690ff9 --stat`.
**Spec reference:** FR-031 (`docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1098`); FRs -003, -006, -008, -023a, -028, -028a are *touched* by the types and need to cohere.
**ADR reference:** `docs/internal/plans/ADR-051-rev4-delivery-plan.md` §1 row for Slice A.

---

## Findings table

| ID | Severity | schema:field (or scope) | One-line | Fix |
|---|---|---|---|---|
| **M1** | MAJOR | `MediaLibraryEntry.id` | `required` but NOT `readOnly` — UUID media ID is server-assigned per spec Ambiguity #6 (UUID matched to legacy `media://<uuid>` shape), the schema exposes a "mutable" impression. With 2/9 fields flagged readOnly inconsistently, the contract is ambiguous about server-vs-client ownership of all fields. | Add `readOnly: true` to `id` (mirroring the existing pattern on `refcount` / `last_refcount_seen_at`). |
| **M2** | MAJOR | `MediaLibraryEntry.uploaded_at` | `required` (date-time), NOT `readOnly`. Description says "RFC3339 UTC upload timestamp" — i.e. server-stamped at upload; clients should not be able to set it. Slice A currently only exposes read endpoints so the issue is dormant, but the type is reused for any future write path. | Add `readOnly: true`. |
| **M3** | MAJOR | `MediaLibraryEntry.sha256` | `required`, pattern-constrained, NOT `readOnly`. Description: "Lowercase hexadecimal SHA-256 digest verified on every read" = server-computed. If this field is ever surfaced on a write path, the schema lets clients submit a forged digest. | Add `readOnly: true`. |
| **M4** | MAJOR | `MediaLibraryEntry.mime` | `required`, NOT `readOnly`. Description: "MIME type sniffed from the stored bytes" = server-derived (resniffable on every read). | Add `readOnly: true`. |
| **M5** | MAJOR | `MediaLibraryEntry.size` | `required` (`int64`, `minimum: 0`), NOT `readOnly`. Description: "Raw file size in bytes" — server-derived (from the stored bytes). | Add `readOnly: true`. |
| **M6** | MAJOR | `MediaLibraryEntry.size` | Has `minimum: 0` but no `maximum`. Spec FR-006 (plan §0) caps uploads at `maxUploadFileSize` (100 MB) server-side; the wire never expresses this invariant. Clients cannot form a well-formed request without consulting docs (Constraint #8 spirit: invariants the client needs to write a valid payload should be encoded in the schema). | Add `description: ... server-enforced 100 MB cap (maxUploadFileSize)` and either an explicit `maximum` (if exact) or cross-reference docs. |
| **M7** | MAJOR | `MediaLibraryEntry.id` | `minLength: 1` only — no `format: uuid` or regex. Per Ambiguity #6 the ID is UUID-shaped; the cross-workspace resolver (FR-028a) and the workspace ref parse rely on UUID shape. A 1-char string passes the wire but breaks downstream. | Add `format: uuid` (OpenAPI 3.1 vocabulary) or `pattern: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"`. |
| **M8** | MAJOR | `MediaAttachmentRequest.media_id` | Same as M7 — UUID field with only `minLength: 1`. Resolver (FR-028a) enforces membership against `MediaLibraryEntry.id`; if clients can send a non-UUID the resolver is forced to fail later. | Add `format: uuid`. |
| **M9** | MAJOR | `MediaLibraryEntry.filename` | `minLength: 1` only — no `maxLength`. Filenames are unbounded on the wire; a 1 MB string is "valid" per schema. Adjacent FR-023a enforces ≤128-char *injection* sanitization downstream — the schema and the spec are out of step. | Add `maxLength: 255` (POSIX-ish) or 128 (matching FR-023a's injection cap); document the source-of-truth in the spec. |
| **M10** | MAJOR | `MediaAttachmentRequest.content_injection_override` | The entire value is attacker-controlled LLM-bound text. Schema caps length at 16384 but has no `pattern`/description forbidding control chars / newlines. The whole point of FR-023a's sanitization is to prevent prompt injection via user data; the override field short-circuits the automatic presentation layer and injects arbitrary text verbatim. STRIDE: Tampering/Info-Disclosure vector if a multi-line string or ANSI escapes reach the LLM. | Add a `description` noting the server sanitizes the override per FR-023a, and document in the spec that this field is sanitization-bound. |
| m1 | MINOR | `MediaLibraryEntry.mime` | No `pattern`. RFC 6838 is `type/subtype`; `minLength: 1` permits `x`, `a`, ` `, … — accepts obvious garbage. | Add a regex like `^[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+$` with case-insensitive flag, or accept without the slash too. |
| m2 | MINOR | `MediaLibraryEntry.source` | Example is `upload:webchat` but no `pattern` / `enum`. The spec implicitly partitions by origin ("user uploads" vs "agent-generated" per FR-005); no closed vocabulary enforced. | Either add `pattern: "^(upload\|agent\|import):[a-z0-9_-]+$"` or `enum` — and align the example list in the spec. |
| m3 | MINOR | `MediaAttachmentRequest.content_injection_override` | No `minLength`. Empty string is silently accepted. Description says "Omit to use the default" — empty-vs-omit is ambiguous (both produce the same JSON null/missing). | Either add `minLength: 1` (treat empty as "use default") or update the description to clarify `""` semantics. |
| m4 | MINOR | `MediaAttachmentRequest.position` | `int32` `minimum: 0`, no `maximum`. Negative overflow wraps silently. A bounded attachment list won't hit INT32_MAX, but a malicious client could. | Add `maximum: <reasonable-bound>` (e.g. 1023) or document the server-side clamp. |
| m5 | MINOR | `MediaLibraryEntry.filename` | `minLength: 1`, no `pattern` for control-char rejection. FR-023a only sanitizes on injection — a control-char filename on the wire survives storage and round-trips through the manifest. | Add a `pattern` (e.g. `^[^\\x00-\\x1f\\\\/:*?"<>\|]+$`) — server-side trim+reject, or document the storage-side rejection. |
| m6 | MINOR | `src/lib/llm-error.ts` | File ends without trailing newline after the `readEntryIdFromFrame` close brace. Cosmetic / POSIX convention. | Add trailing newline. |
| O1 | OBSERVATION | `POST /workspaces/{id}/media/attachments` | Returns 204 No Content — no Location header, no correlation/audit ID echo. The audit-event shape per FR-033 (`media_id`, `bytes_freed`, `timestamp`) is not surfaced to the client. | Consider 200 with the resulting `MediaLibraryEntry` (or a minimal `{ attachment_id, audit_id }` body) — Slice H (SPA) and Wave-3 integration tests will struggle to assert success without a body. Optional, surface in spec. |
| O2 | OBSERVATION | `MediaLibraryEntry` (round-trip) | No `DELETE /workspaces/{id}/media/{media_id}` defined yet. FR-008 requires explicit delete. Expected for Slice A FOUNDATION; flagging for Wave 1 B2 to land coherently with the type. | Track in B2 PR description; no change to Slice A. |
| O3 | OBSERVATION | `MediaLibraryEntry` (write side) | No `MediaLibraryCreateRequest` body shape exists. Slice A FOUNDATION is read-only; the upload body lands in B1 (or uses a different wire like multipart). | Track in B1 PR; no change to Slice A. |
| O4 | OBSERVATION | Generator: `matchingNamedInlineGoType` (out-of-scope but high-quality) | The AsyncAPI Go generator now lifts the previously hand-applied pointer-vs-value distinction (`ErrorFrame.Payload *ErrorPayload`, `ReplayErrorFrame.Payload *ReplayErrorPayload`) into the codegen via a same-shape comparator. Strictly speaking this is **Wave-0 mechanics**, not Slice A scope, but it's a clean eliminate-the-hand-edit fix that the diff cites as `ADR-051 B2` rationale. Worth calling out as **net-positive** for type-design quality. | None — noting for the operator that the previously-documented manual step in `asyncapi_types.gen.go` is now generator-driven. |
| O5 | OBSERVATION | `src/lib/llm-error.ts` collapse (out-of-scope) | Replaces hand-maintained parallel `LLMError`/`LLMErrorCode` types with type aliases to generated types. **Net-positive** for Constraint #8 (eliminates one of the hand-written wire mirrors). Strictly Wave-0 hygiene, not Slice A scope. | None. |
| O6 | OBSERVATION | `additionalProperties: false` on both new schemas | Done. ✅. Preserves wire strictness; Constraint #8 clean. | None — positive note. |
| O7 | OBSERVATION | Zod generation | Generated Zod enforces `sha256` regex `/^[a-f0-9]{64}$/` (✅), `uploaded_at` datetime with offset (✅), `size` `int64` `gte(0)` (✅), and `mime`/`filename` only as `minLength(1)` (loose — see m1/m5). | None — partial positive. |

---

## Verdict

**REVISE** — 0 CRITICAL, 10 MAJOR (M1–M10), 5 MINOR (m1–m5), 7 OBSERVATION (O1–O7).

The MAJORs cluster around three themes:
1. **readOnly omissions (M1–M5)** — five server-generated fields are `required` but not `readOnly`. Two are correctly flagged (`refcount`, `last_refcount_seen_at`); the other five are inconsistently treated. This breaks the "mutability contract" (Constraint #8 invariant expression) the schema is supposed to encode.
2. **Identifier shape (M7, M8)** — both UUID fields use `minLength: 1` instead of `format: uuid` / regex, forcing the resolver (FR-028a) and ref parser to validate downstream.
3. **Content-injection schema (M9, M10, m3, m5)** — the new wire silently accepts overlong filenames, unbounded control characters, and the override-content field invites prompt injection not covered by FR-023a's filename-only sanitization.

The foundation is otherwise correct: `additionalProperties: false` is consistent, `sha256` regex is tight, `datetime({ offset: true })` is enforced in Zod, and the generator-side improvements are strong wins.

**Recommendation:** Slice A MERGE should be DEFERRED until M1–M10 are addressed in a stacked fix commit on the same branch (`fix(adr-051-rev4): review-round1-type-design corrections`). MINORs may be bundled with that commit or punted to Wave-1 review. OBSERVATIONS are advisory.

---

## Evidence / verification note

- Reviewed `git diff f6eccbcd..08690ff9 --stat` and the full per-file diff (`contracts/components/schemas/{MediaLibraryEntry,MediaAttachmentRequest}.yaml`, `contracts/openapi.yaml` paths block, `pkg/api/generated/openapi_types.gen.go` MediaLibraryEntry/MediaAttachmentRequest blocks, `src/lib/api/generated/{openapi-types,schemas}.ts`, `scripts/gen-asyncapi-go/main.go` + test, `pkg/api/generated/asyncapi_types.gen.go` ErrorFrame/ReplayErrorFrame blocks, `src/lib/llm-error.ts`) — all claims above rest on those tool results.
- Spec claims rest on `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1083-1102` (FR-003/006/020a/023a/028/028a/031/033) and `Ambiguity Warnings` rows 1–8.
- `pkg/gateway/inboundschemas/MediaLibraryEntry.yaml` and `…/MediaAttachmentRequest.yaml` are **not** hand-copies — they're auto-synced by `scripts/gen-contracts.sh:78-84` step 5 (`rm -f pkg/gateway/inboundschemas/*.yaml; cp contracts/components/schemas/*.yaml ...`), confirmed by `grep inboundschemas scripts/gen-contracts.sh`. No finding there.

**Nothing changed in this review** (read-only by instruction). No behavior to verify beyond the diff content already cited.
