# Wave 0 / Slice A — comment-analyzer review (round 1)

**Scope:** `08690ff9` vs `f6eccbcd` on `sendfile-fix`  
**Review focus:** accuracy, rot, completeness, and maintenance burden of comments, docstrings, and OpenAPI descriptions.

## Findings

| ID | Severity | File:line | One-line | Fix |
|---|---|---|---|---|
| CA-1 | MAJOR | `contracts/asyncapi.yaml:1421-1423`, `contracts/asyncapi.yaml:1884-1887` | Both inline-payload comments are now stale: they say the pointer fix is a hand-adjustment in `asyncapi_types.gen.go` and direct maintainers to a deleted ADR-051 comment, while this commit moved the behavior into `matchingNamedInlineGoType`. | Replace the hand-edit instructions with a concise statement that `scripts/gen-asyncapi-go` reuses a shape-matching named payload type and emits a pointer for optional fields; keep the Zod TDZ rationale. |
| CA-2 | MAJOR | `contracts/openapi.yaml:5451`, `contracts/openapi.yaml:5480`, `contracts/openapi.yaml:5511` | The new summaries and response descriptions advertise list/get/create media operations as supported, but `HandleWorkspaces` has no media dispatch and sends these subpaths to 404; Wave 0 only required component-schema references. | Remove the three path operations until their handlers land, leaving the component schemas referenced under `components`, or implement/register the operations in the owning backend slice before publishing them. |
| CA-3 | MINOR | `contracts/components/schemas/MediaLibraryEntry.yaml:19`, `contracts/components/schemas/MediaAttachmentRequest.yaml:13` | The descriptions promise UUID identifiers, but both schemas accept any non-empty string, so generated Go/TS/Zod API documentation and validation disagree. | Add `format: uuid` to both fields, or describe them as opaque non-empty IDs if UUID is not contractual. |
| CA-4 | MINOR | `contracts/components/schemas/MediaLibraryEntry.yaml:45`, `contracts/components/schemas/MediaLibraryEntry.yaml:61` | Both timestamp descriptions promise UTC, while OpenAPI `date-time` and generated `z.string().datetime({ offset: true })` accept non-UTC RFC3339 offsets. | Either remove “UTC” from the descriptions or constrain values to a trailing `Z` and normalize server output accordingly. |
| CA-5 | MINOR | `scripts/gen-asyncapi-go/main.go:7-20`, `scripts/gen-asyncapi-go/main.go:419` | The generator’s package documentation omits the new, non-obvious inline-object naming/shape-reuse rule, including the `Frame` suffix convention that now prevents anonymous payload structs. | Add one mapping-rule bullet and a short function comment describing candidate naming, shape equality, and optional-pointer behavior. |
| CA-6 | MINOR | `src/lib/llm-error.ts:6-8` | Replacing hand-maintained wire mirrors with generated aliases is correct, but it also removed all documentation from three exported public types. | Add concise alias-level docs that point to the generated AsyncAPI types; do not repeat the code union or field list. |

## Hypothesis disposition

- Rejected: the media operations are implemented outside the diff — `pkg/gateway/rest_workspaces.go:494-544` dispatches only milestones, delegation, instructions, and base workspace routes; unknown nested paths return 404.
- Rejected: the generated payload pointers still require a manual patch — `scripts/gen-asyncapi-go/main.go:395-437` now emits the named optional pointer and `pkg/api/generated/asyncapi_types.gen.go` no longer contains the referenced hand-edit comment.
- Confirmed: UUID and UTC wording is stricter than the generated validators (`minLength: 1`; date-time offsets accepted).

## Verdict

**REVISE — 0 CRIT / 2 MAJOR / 4 MINOR / 0 OBS.**
