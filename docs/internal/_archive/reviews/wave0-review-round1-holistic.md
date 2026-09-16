# Wave 0 — Slice A (Contracts Foundation) — Holistic Review

**Review target:** commit `08690ff9` (`feat(adr-051-rev4): generate media library wire contracts (Slice A / Wave 0)`) on `sendfile-fix`, vs parent `f6eccbcd`.
**Reviewer:** 7th holistic reviewer (architect frame-of-reference: ADR-051 Rev 4 Decision section + spec Behavioral Contract / Explicit Non-Behaviors / Integration Boundaries).
**Mode:** READ-ONLY. No files modified. Plan v2 corrections applied.
**Scope per plan §2 Wave 0:** "Slice A — Generate `contracts/components/schemas/MediaLibraryEntry.yaml` + `MediaAttachmentRequest.yaml`. Reference from `contracts/openapi.yaml`. Run `scripts/gen-contracts.sh`. Verify with `make verify-contracts`."
**Actual diff:** 12 files, +745 / −82. Three of the 12 files are out-of-plan-scope for Slice A and are flagged in this review.

---

## Verdict

**REVISE** (with reservations). The slice is *substantively* correct — the in-scope wire work matches the ADR Decision, the schemas conform to Constraint #8 (contract-first), and the diff will pass `make verify-contracts` once gen-contracts is re-run. But three load-bearing concerns block the wave from advancing to Wave 1 as-is: (1) significant scope creep bundled into the Slice A commit (AsyncAPI generator fix + regeneration + SPA type refactor — the plan says one commit about wire types, not a generator rewrite); (2) `MediaAttachmentRequest` carries two fields (`content_injection_override`, `position`) that are NOT in the ADR Decision or the spec — locking in undocumented abstractions; (3) `MediaLibraryEntry` does NOT carry `workspace_id`, leaving Wave 1+ (B1, G, FR-028a) to construct the `media://workspace/<ws>/<id>` ref shape outside the wire schema, where the resolver shape is most load-bearing.

**Counts:** 1 MAJOR · 3 MINOR · 4 OBSERVATION.
**Recommend:** fix M-1 (split bundled scope into its own commit) before Wave 1a (B1) starts; the rest can be folded into the Wave 1 reviews as MINOR-driven corrections.

---

## Findings Table

| ID | Severity | Scope | One-line | Fix |
|---|---|---|---|---|
| **M-1** | MAJOR | **Slice-A commit composition (Cross-cutting)** | The commit bundles 4 unrelated changes: (a) in-scope MediaLibraryEntry + MediaAttachmentRequest schema gen; (b) `scripts/gen-asyncapi-go/main.go` adds `matchingNamedInlineGoType` mechanism + `sameSchemaShape` recursive comparator; (c) `pkg/api/generated/asyncapi_types.gen.go` regenerated to drop the long "ADR-051 B2" hand-fix comments; (d) `src/lib/llm-error.ts` refactored from hand-written `LLMError`/`LLMErrorCode`/`LLMErrorReplay` types into aliases of the generated `asyncapi-types.ts` exports. Per plan §2 Wave 0 the slice produces "1 commit on `sendfile-fix` … wire types now exist" — not "fix the asyncapi generator + migrate `llm-error.ts`." The asyncapi work is **tech debt cleanup from a prior ADR-051 slice (Rev 3 B2)**, and bundling it into Slice A hides the prior-debt fix from the per-slice reviewer gate (CLAUDE.md Hard Constraint #7: every branch fully green, no "not mine" escapes — bundling makes review traceability muddier). | Split into 2 commits on `sendfile-fix`: (1) `feat(adr-051-rev4): generate media library wire contracts` (the 8 in-scope files: 2 schemas, 2 mirrored, `openapi.yaml`, generated TS/Go); (2) `chore(contracts): auto-detect matching named inline payloads in asyncapi generator + drop hand-fix in `asyncapi_types.gen.go` + migrate `llm-error.ts` to generated types`. The asyncapi scope needs its own reviewer-gate round; bundling makes its MAJOR/MINOR findings invisible. |
| **m-1** | MINOR | **`MediaAttachmentRequest` shape (Wave 1+ lock-in)** | The schema adds two fields that are NOT in the ADR Decision or the spec Behavioral Contract: `content_injection_override` (optional string, max 16384 chars) and `position` (optional int32 ≥0). Neither appears in the spec FR list, the ADR Decision, or the BDD scenarios. FR-031 ("System SHOULD define … `MediaAttachmentRequest.yaml`") names the file but does not enumerate fields. The slice invents these fields and the wire-types-lint will lock them in — Wave 1+ (B1 storage; H SPA composer; C capability registry) must honor the override path. | Either (a) drop both fields from this slice and let B1 / H add them when the implementation justifies them, or (b) document them in the spec via a revision (US-8 amendment: "attach with optional content override"). The conservative path is (a) — the slice's role is to define the manifest + minimal attachment identifier, not presentation-layer overrides. |
| **m-2** | MINOR | **`MediaLibraryEntry` shape (resolver coupling)** | The schema has `id` (UUID), `filename`, `mime`, `size`, `sha256`, `uploaded_at`, `source`, `refcount`, `last_refcount_seen_at`. It does NOT have `workspace_id`. Yet the spec US-11 BDD scenario and FR-028a establish the ref shape `media://workspace/<ws-id>/<id>` — the discriminator at FR-028 is `strings.HasPrefix(ref, "media://workspace/")`, and the cross-workspace guard (FR-028a) requires the resolver to know the `<ws-id>` segment to enforce membership. The wire schema hands Wave 1+ (B1 + G) the choice of where to carry workspace_id — likely a separate `<workspace_id>` column in the manifest, a separate `<media_workspaces>` join table, or implicit via the URL path. Each choice has a contract consequence (a later wire-shape extension is a breaking change once Wave 1+ ships). The slice is the right time to commit to one of these. | Add `workspace_id` as a required string (UUID, minLength 1) to `MediaLibraryEntry`. The path parameter `id` and the body's `workspace_id` will cross-validate naturally; B1 stores it; G's resolver reads it; ref shape `media://workspace/<ws-id>/<id>` is reconstructible from the wire shape without ambiguity. |
| **m-3** | MINOR | **`source` field — unbounded string (operability)** | `source: { type: string, minLength: 1 }` with example `"upload:webchat"` suggests a `<scope>:<channel>` convention but is not enforced (no enum, no pattern). The ADR mentions "agent-generated media (`tool:inline:session:<id>`) stays session-scoped, not migrated" and the spec US-3 AC1 names `tool:inline:session:<sess-1>` — but no operator-editable source taxonomy exists. Wave 1+ B1 will accept any string, leading to silent divergence between code-emitting sources ("upload:webchat", "upload:api", "agent:screenshot") and the spec's two-mechanism split (US-3 FR-005). | Add `pattern: "^(upload\|agent):[a-z0-9_-]{1,64}$"` (or an `enum` with the documented two channels for v0.1.1: `["upload:webchat","upload:api","agent:screenshot","agent:chart"]`). The ADR's two-mechanism split is a security-relevant boundary — encoding it in the wire shape forces B1 to declare which sources the library accepts, which is the spec US-3 invariant. |
| **o-1** | OBS | **AsyncAPI generator change is load-bearing for Slice A** | `matchingNamedInlineGoType` strips the `Frame` suffix from the owner name (e.g., `ErrorFrame` → `Error`) and concatenates with the pascal-cased property name (`payload` → `Payload`) to construct the candidate (`ErrorPayload`). The `sameSchemaShape` recursive comparator then does an EXACT structural match. **If the inline payload and the named schema EVER diverge** (e.g., a new required field added to `ErrorPayload.yaml` but not to `ErrorFrame.payload`'s inline mirror), the match silently fails and the generator falls back to the inline struct — silently restoring the hand-fix dependency the script is meant to eliminate. The new test (`TestGenerateUsesMatchingNamedInlinePayload`) covers only the success path. There is NO test for shape divergence, NO warning logged when a candidate is rejected, and the function does not return the rejection reason. | Either (a) log a debug/warn line when `sameSchemaShape` returns false on a candidate that exists (drift visibility), or (b) make the rejection FATAL when an inline payload has a matching candidate name (so any future drift becomes a CI failure rather than silent fallback). The "auto-detect + silent fallthrough" mode is exactly what creates the kind of tech debt this PR is trying to retire. |
| **o-2** | OBS | **Non-deterministic candidate resolution (only matters if multiple schemas match)** | `matchingNamedInlineGoType` does `allSchemas[candidateName]` — Go map lookup. If by future accident there are two named schemas with matching shape (e.g., `MyFramePayload` and `MyPayload` both structurally identical), only one will be returned. Map iteration in Go is randomised; if a future refactor uses `range allSchemas` to find candidates instead, output would differ between runs. Today there's only one collision (`ErrorPayload`/`ReplayErrorPayload` matching `*Frame.payload` each), so this is theoretical — but the function design is fragile. | Document the "one named schema per candidate" invariant in the function comment; add a test asserting that duplicate-shaped candidates cause `sameSchemaShape` to error rather than silently pick one. |
| **o-3** | OBS | **SPA embed directory is stale (out of Slice A's deliverable but in the wave's runtime acceptance)** | The `git grep` of `MediaLibraryEntry` in `pkg/gateway/spa/assets/*.js` returns 0 matches — the SPA embed directory was NOT rebuilt. Per CLAUDE.md Hard Constraint #1 ("SPA embedded via `go:embed`") and the build pipeline ("`cp -r dist/spa/* pkg/gateway/spa/`" then `go build`), the runtime SPA served by the gateway does NOT know about the new `/workspaces/{id}/media*` endpoints. The slice claim ("wire types now exist in `src/lib/api/generated/`") is literally true, but the runtime SPA is unaware. This is a Wave 4 acceptance concern, not Slice A's, but it is a verification gap the plan doesn't gate on. | The plan should add to T14 acceptance: verify `grep -c "MediaLibraryEntry" pkg/gateway/spa/assets/index-*.js` returns >0 (per CLAUDE.md "skip the sync → stale SPA served"). The slice's contract work is fine; the runtime smoke is Wave 4's responsibility, but it is explicitly noted here as a future-testable invariant. |
| **o-4** | OBS | **`last_refcount_seen_at` is undocumented in the spec** | The schema adds `last_refcount_seen_at` (RFC3339 timestamp, readOnly, optional). The spec FR-007a establishes refcount semantics (increment on session/turn ref, decrement on cleanup, deferred 30d GC) but does NOT mention a "last observation timestamp." This field locks in a tracking metric Wave 1+ (B1) must maintain and Wave 4 (T14 observability) will inspect. The field is plausible for orphan-GC age computation, but the spec's "31 days since upload" wording uses `uploaded_at`, not a refcount-observation timestamp. | Either (a) document `last_refcount_seen_at` in the spec as a future metric for "files still in active use past their upload age," or (b) drop the field from this slice. The schema's `readOnly: true` means clients can't set it; only B1's maintainer can — but the wire-shape presence is a public promise. |

---

## What the slice did RIGHT (carry-forward to Wave 1 reviewers)

- **Conformance to Constraint #8 (contract-first):** `MediaLibraryEntry` and `MediaAttachmentRequest` live ONLY in `contracts/components/schemas/`; mirrored byte-identical into `pkg/gateway/inboundschemas/` (per ADR-013/ADR-015 pattern, verified by `diff`); generated into `pkg/api/generated/openapi_types.gen.go` and `src/lib/api/generated/{openapi-types,schemas}.ts`. No parallel struct/interface anywhere — the wire-types-lint (`scripts/check-no-handwritten-wire-types.sh`) will stay clean.
- **ADR-051 §Affected Components compliance:** the ADR names "pre-name: `contracts/components/schemas/MediaLibraryEntry.yaml`, `MediaAttachmentRequest.yaml`; reference from `openapi.yaml`; regenerate via `scripts/gen-contracts.sh` (Constraint #8, 5-step process)" — the slice followed the 5-step recipe: schema → reference → `gen-contracts.sh` (presumably; verify by running it) → commit gen diff → handler/consumer next slice.
- **`refcount: readOnly`** is the right shape for a server-maintained metric; clients cannot forge it.
- **`sha256: pattern "^[a-f0-9]{64}$"`** is strict and matches the spec's "Lowercase hexadecimal SHA-256" intent (US-2 AC1).
- **`uploaded_at: format date-time`** is correct for RFC3339 UTC.
- **Auth on the new endpoints** is declared via `BearerAuth: []` on `listWorkspaceMedia`, `createWorkspaceMediaAttachment`, `getWorkspaceMedia` — consistent with the existing `/workspaces/{id}/*` family.
- **Author identity** verified: `Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>` per CLAUDE.md mandatory authorship rule. No `Co-Authored-By:` trailers (verified via `git log origin/main..HEAD --format='%(trailers:key=Co-authored-by)'` — empty).
- **One commit, stacked on `sendfile-fix`** — per plan §7 decision 7.
- **Endpoint REST surface is honest:** `GET /workspaces/{id}/media` (list), `GET /workspaces/{id}/media/{media_id}` (get), `POST /workspaces/{id}/media/attachments` (attach) — none of them overpromise. No DELETE, no PATCH, no upload endpoint (the upload goes through existing `/api/v1/upload` per Slice B1's scope; per ADR's `HandleUpload` ownership).

---

## What the slice did WRONG (correct before Wave 1)

### M-1 — Bundled scope (the headline concern)

The plan §2 Wave 0 enumerates 6 lines of work for Slice A. The diff does those 6, PLUS:

1. **`scripts/gen-asyncapi-go/main.go` rewrite** — `+73` lines adding a new mechanism (`matchingNamedInlineGoType`, `sameSchemaShape`). This is **a fix to a pre-existing hand-fix** in `pkg/api/generated/asyncapi_types.gen.go` (the `// ADR-051 B2: the AsyncAPI spec keeps the payload inline … hand-adjusted to *ErrorPayload` comments on `ErrorFrame.Payload` and `ReplayErrorFrame.Payload`). That hand-fix was added in a prior Rev-3 slice (commit `ae9271d0`, `feat(adr-051): implement media handling + provider-error translation (Wave 1+2)`); it predates ADR-051 Rev 4 entirely. The current slice is fixing it now.
2. **`pkg/api/generated/asyncapi_types.gen.go` regenerated** — `-17` lines of hand-fix comments removed. Output is correct (the `*ErrorPayload` / `*ReplayErrorPayload` pointer types are preserved by the new generator logic).
3. **`src/lib/llm-error.ts` refactor** — `-52` lines of hand-written `LLMError`, `LLMErrorCode`, `LLMErrorReplay` types replaced with aliases for the generated `asyncapi-types.ts` exports. This was round-1 review item F-L5-2(c) ("pre-existing branch debt"), correctly identified by round-1 as "self-documented as display-only, not a wire type" — meaning the round-1 reviewer's claim that this was a Constraint #8 violation was wrong. The current slice nonetheless refactors it to use generated types. The refactor itself is GOOD (eliminates the manual-sync burden the file's original header documented), but it is **NOT Slice A's work** and should travel separately.

The risk of bundling: Wave 1's 6 specialist reviewers + 7th holistic will see this single commit and try to triage the slice's "media library wire contracts" surface. The asyncapi generator rewrite introduces shape-matching logic that, if it later causes a regression (per o-1 above), will be hard to diagnose because it's interleaved with the new wire types. The m-1 finding (a `sameSchemaShape` regression could silently break ErrorFrame.Payload in the next regeneration) cannot be caught by the wave-1 reviewers because they'll be focused on MediaLibraryEntry. **Split the commit.**

### m-1 — `MediaAttachmentRequest` field overreach

The slice invents `content_injection_override` and `position`. Neither is in:
- The ADR Decision section (steps 1–7 of Layer 1, no override path; FR-021 names three fixed guidance strings, no override)
- The spec's 34 MUST/SHOULD FRs (FR-001…FR-033, no override or ordering)
- The spec's 40 BDD scenarios (none describe attachment override or ordering)

Yet the schema locks them in as part of the wire shape. The consequences:
- Wave 1 B1 (workspace library storage) and Wave 1 F (resize) don't touch this field, but Wave 2 H (SPA composer) MUST honor `content_injection_override` (the description says "instead of the automatic presentation-layer content"). H cannot ignore it; the SPA composer must surface an "override" UI affordance, even if minimal.
- Wave 1+ B1 / H / C MUST honor `position` (the description says "zero-based position among the message attachments"). Ordering across attachments is a NEW capability; it adds complexity to `resolveMediaRefs`'s output ordering.

The conservative fix is to drop both fields now and let later slices add them when their implementation justifies them. The plan's "Slice A — Generate `MediaAttachmentRequest.yaml`" is the file name only — field composition is the slice's choice.

### m-2 — `MediaLibraryEntry` doesn't carry `workspace_id`

The ref shape `media://workspace/<ws-id>/<id>` is established by spec FR-028, FR-028a, and US-11 BDD ("media ref `media://workspace/ws-A/<id>` whose library entry exists in `ws-A`"). The wire schema's `MediaLibraryEntry` has only `id` (UUID). Wave 1+ will need to:
- Store workspace_id somewhere (manifest table column, joined from a `<media_workspaces>` table, or implied from the file path)
- Read it during resolution (G's resolver with FR-028a's caller-workspace context)
- Reconstruct the `media://workspace/<ws-id>/<id>` ref (the `<ws-id>` segment is the discriminator)

The path parameter `id` on `GET /workspaces/{id}/media/{media_id}` is in the URL, not the body, so it doesn't help. Adding `workspace_id` as a required string field on `MediaLibraryEntry` costs nothing and locks in the discriminator.

### m-3 — `source` is unbounded

The example `"upload:webchat"` and the ADR's "two-mechanism split" (US-3, FR-005) imply a `<scope>:<channel>` taxonomy. Without a `pattern` or `enum`, any string is acceptable. B1's manifest writer will produce values like `"upload:webchat"`, `"upload:api"`, `"agent:screenshot"`, `"agent:chart"` — diverging from the spec's two-mechanism split. A pattern (`^(upload|agent):[a-z0-9_-]{1,64}$`) or enum encodes the security-relevant boundary (US-3 invariant: agent-generated media stays session-scoped) into the wire shape.

---

## Cross-cutting checks (Holistic reviewer concerns)

| Concern | Status | Note |
|---|---|---|
| **Conformance to ADR Decision** | ✅ + ⚠️ | Layer 0 manifest fields (`id, filename, mime (sniffed), size, sha256, uploaded_at, source`) all present and required (matches ADR "Manifest entry per file (new fields)"). ⚠️ `workspace_id` is NOT in the ADR's named field list either, but it's an OMISSION not a violation; the ADR doesn't preclude it. |
| **Conformance to spec Behavioral Contract** | ✅ | The schemas carry the contract: sha256 verified-on-read (sha256 + pattern), manifest required fields (required list), readOnly server fields (refcount, last_refcount_seen_at). |
| **Conformance to spec Explicit Non-Behaviors** | ✅ | No `media://` in manifest (good — manifest carries the entry id, the ref shape is reconstructed). No dedup-via-sha256 (good — sha256 is integrity only, not dedup key, per Ambiguity #4). |
| **Conformance to spec Integration Boundaries** | ✅ | LLM Providers boundary unchanged (slice A doesn't touch it). Omnipus Repo boundary unchanged (Slice C owns). Filesystem/Sandbox boundary unchanged. |
| **Wave 1+ lock-in safety** | ⚠️ | The slice locks in: `MediaLibraryEntry` field shape, `MediaAttachmentRequest` field shape, refcount semantics (via `readOnly` + `int ≥ 0`), and the 3 REST endpoints. It does NOT lock in: storage location, resolver behavior, capability catalog transport, audit event shape (FR-033). The lock-in is correct for what it sets; the overreach is m-1, m-2, m-3 above. |
| **Constraint #8 (contract-first)** | ✅ | All wire types generated. No parallel struct/interface. `inboundschemas/` mirror is in sync (verified via `diff` — no output). The `src/lib/llm-error.ts` refactor further REDUCES hand-written wire types (a net-positive). |
| **Constraint #1 (single Go binary)** | ✅ | No new build deps. The asyncapi script change is pure Go stdlib (`maps`, `slices` from 1.21+; existing `go.mod` requires 1.26.4 per CLAUDE.md, so fine). |
| **Constraint #2 (pure Go, no CGo)** | ✅ | No new CGo deps introduced. |
| **Constraint #3 (minimal footprint)** | ✅ | No runtime RAM delta. |
| **Constraint #4 (graceful degradation)** | ✅ | N/A for schema-only work. |
| **Constraint #5 (ecosystem compatibility)** | ✅ | Follows OpenClaw `AGENT.md` / `SOUL.md` / `HEARTBEAT.md` conventions for future Slice H (B1/H will write `AGENT.md`-like files). |
| **Hard Constraint #7 (release responsibility)** | ⚠️ | The bundled scope (M-1) makes prior-debt fixes harder to triage. Pre-existing failures listed in the plan (round-1 review F-L5-2) included `llm-error.ts` as item (c), which round-1 correctly identified as "self-documented display-only, NOT a wire type." The current slice migrates it ANYWAY — that's allowed (Constraint #7 forbids "not mine" escapes, but this is an IN-SCOPE fix, just bundled into the wrong commit). |
| **Hard Constraint #8 (contract-first, no hand-written wire types)** | ✅ | Net IMPROVED — the `src/lib/llm-error.ts` refactor reduces hand-written types. |
| **Author identity (CLAUDE.md mandate)** | ✅ | `Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>`. No `Co-Authored-By:` trailers. |
| **CLAUDE.md "Retired surfaces — do NOT reintroduce"** | ✅ | N/A — slice A doesn't touch the SPA. |
| **CLAUDE.md "Preview on the main listener (ADR-044)"** | ✅ | N/A — slice A doesn't add preview endpoints. |

---

## Decision

The slice is correct on its core wire-contract work and ships the right foundation for Wave 1a (B1). The blocking concern is **M-1 (bundled scope)**: the Wave 0 plan says Slice A produces one commit about wire types, not a bundled commit that includes asyncapi generator rewrite + SPA type refactor. Splitting M-1's commit boundary is a one-line cost (`git reset HEAD~1` + 2 new commits, or amend with multiple commits before push) and unblocks Wave 1a's review surface.

The m-1 / m-2 / m-3 findings are design choices the slice can roll back into a Wave 1 fix commit without delaying B1's start (they're schema additions or removals, contract-extension-safe if caught before B1's manifest persistence is wired).

**Recommend: do NOT start Wave 1a until M-1 is resolved (commit split). The m-1/m-2/m-3 findings can ride into the Wave 1 reviewer-gate round 1 as known MINOR items.**

---

## Carried-forward notes for Wave 1 reviewers (Slice B1 in particular)

1. **`MediaLibraryEntry.workspace_id` decision** — if m-2 isn't fixed in Slice A, B1 will define `workspace_id` storage ad-hoc and will need to file a contract-extension PR later. Wave 1's `type-design-analyzer` should flag the absence.
2. **`MediaAttachmentRequest.content_injection_override` decision** — if m-1 isn't fixed in Slice A, B1's `pkg/media/library/` will accept and persist this field, and Wave 2 H must surface it in the SPA composer. Wave 1's `pr-test-analyzer` should add a test asserting the field is honored on write/read.
3. **`source` taxonomy** — if m-3 isn't fixed, B1 will accept arbitrary source strings. Wave 1's `silent-failure-hunter` should add a test that rejects sources outside the documented taxonomy.
4. **AsyncAPI script drift visibility** — o-1's recommendation (warn on shape-mismatch rejection, or fail loudly) should be in Wave 1's review backlog even if not addressed now.
5. **SPA sync to `pkg/gateway/spa/`** — o-3 is a Wave 4 acceptance concern but Wave 1's `silent-failure-hunter` should flag any test that exercises the SPA-side stub before the gateway runtime smoke.

---

*End of holistic review. Slice A is well-scoped at the wire level and correctly conforms to Constraint #8; the bundled asyncapi generator fix is its only MAJOR concern. CRIT: 0. MAJOR: 1. MINOR: 3. OBS: 4.*