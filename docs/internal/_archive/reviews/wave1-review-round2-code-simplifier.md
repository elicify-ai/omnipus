# Wave 1 Review — Round 2 — Code Simplifier

**Scope:** `sendfile-fix` HEAD `cd0616b0` against parent `d0e7374a` (15 commits: 5 Wave-1 functional slices + 10 r1-fix commits).
**Reviewer:** `pr-review-toolkit:code-simplifier` (round 2, READ-ONLY).
**Focus:** over-engineering introduced by the corrections (manifestEntry / refcount / Version / Modality / UploadFixture / ResizeBudget unification / typed MediaDowngradeResult / exhaustive-code contract test / strict CodeUnknown gate). Re-verify the r1 simplifications.

---

## Diff scope recap (incremental vs r1)

```
+13168 / -184 lines across 70 files (r1 baseline: 5134 / 112, 24 files)
```

The 10 follow-up commits (`d6827307` … `cd0616b0`) add ~8000 LOC of code+tests, of which ~1700 LOC are production code. The split:

| Concern | New LOC (production) | New LOC (tests) | New LOC (data) |
|---|---|---|---|
| TD-M1 + TD-M2 (manifestEntry + refcount) | ~250 | ~80 | — |
| TD-M3 + TD-M4 (private model + serialized Refresh) | ~140 | ~120 | — |
| TD-M5 (Version + Modality) | ~240 | ~440 | — |
| TD-M6 (ResizeBudget unification) | ~50 | ~200 | — |
| TD-M7 (strict CodeUnknown gate) | ~30 | ~260 | — |
| TD-M8 (typed MediaDowngradeResult) | ~120 | ~30 | — |
| TD-m1 + TD-m4 (Load/Store privacy + UploadFixture) | ~120 | ~10 | — |
| TD-C1 / SFH-W1-01 (LLMError enums + contract test) | ~10 | ~250 | — |
| B2 (audit + cascade wire-up, NOT a correction) | ~50 | ~50 | — |
| Spec / docs / research / contracts | — | — | ~3700 |

The corrections add **~960 LOC of production code** to address the type-design / contract / silent-failure MAJORs from the r1 specialist reports. The ratio is roughly 1:1 production:test for the corrections — heavier than r1's 4:1 ratio. That signals the corrections are carrying design overhead (typed domain models, exhaustive contracts) that wasn't present in the r1 first cut.

---

## Re-verification of the r1 simplifications

| R1 ID | Title | R2 status | Evidence |
|---|---|---|---|
| **MAJOR-04** | `clonePointer[T any]` generic + five trivial pointer helpers | **ADDRESSED** ✓ | `library.go` now has `manifestEntry.projection()` (lines 219-240) which uses `&value` literally — no `*Pointer` helpers, no `clonePointer` generic. The 30-LOC of helper machinery is gone. |
| **MAJOR-06** | `pkg/media/resize.DefaultLongEdge` / `DefaultMaxBytes` duplicate the catalog's default | **ADDRESSED** ✓ | `resize.Budget`, `DefaultLongEdge`, `DefaultMaxBytes` are deleted (`resize.go:34-55` history before TD-M6 → empty after). `ResizeToFit` now accepts `capabilities.ResizeBudget` only. The catalog is the single source of truth per the ADR. |
| **MAJOR-09** | `validatePersistedEntry` defensive layer | **PARTIALLY ADDRESSED** ⚠ | `validatePersistedEntry` is gone. The replacement is `manifestEntry.validate()` (library.go:246-257, 12 lines — load-bearing: workspace-match, filename-normalize, refcount-non-negative). BUT the **same defensive nil-check set is duplicated** in `manifestEntryFromProjection` (library.go:831-873, 43 lines) for the wire-shape boundary, AND in the `OrphanGC` rollback (library.go:731-743, 14 lines) for the post-quarantine restore. Three layers of "manifestEntry from projection", each with slightly different rules. See MINOR-r2-1. |
| **MAJOR-01** | `Puller`/`Store` interfaces with one implementation each | **NOT ADDRESSED** ❌ | `Puller` (catalog.go:188-190) and `Store` (catalog.go:196-199) interfaces remain. `Store` still has zero concrete implementations (the r1 noted this). The TD-M3+TD-M4 fix (`f7019e6c`) added `refreshMu` and made `Refresh` serialized, but did not collapse the interfaces. |
| **MAJOR-02** | `pkg/media/resize` is a 203-LOC package wrapping ~60 lines of pure-function code | **NOT ADDRESSED** ❌ | `pkg/media/resize/` is unchanged in shape. `resize.go` shrank slightly (203 → 203 LOC; the `Budget` deletion is offset by new comments). The "move into `pkg/media/library/`" recommendation stands. |
| **MAJOR-03** | Two parallel media-store abstractions | **NOT ADDRESSED** ❌ | `pkg/media/store.go::FileMediaStore` is unchanged. The new `pkg/media/library/library.go::Library` is now typed (TD-M1+TD-M2) but the two coexist exactly as in r1. `grep -rn "library.New\|library.Library" pkg cmd internal \| grep -v library/` → 0 hits. |
| **MAJOR-05** | Custom `logger` interface in `catalog.go` reimplements `*slog.Logger` | **NOT ADDRESSED** ❌ | `catalog.go:407-413` still defines the `logger interface { Warn(msg, args...); Info(msg, args...) }`; `noopLogger` (catalog.go:662-666) is still declared. The TD-M3+TD-M4 fix (`f7019e6c`) added the resolvedModel handle but did not collapse the logger. |
| **MAJOR-07** | `imageStripRange` variadic option on `stripRejectedImageMedia` | **PARTIALLY ADDRESSED** ⚠ | The variadic now has test callers in `runturn_redo_test.go:175-223` (added in the TD-M8 commit), so it's no longer completely unused. BUT the **single production caller** (`media_downgrade.go:205`) still passes no opts — `stripRejectedImageMedia(callMessages)`. The "Pass-2 fix" comment (line 293) refers to future-caller intent. The variadic machinery (`from`/`to` clamping, the `imageStripRange` struct) is exercised by tests but not by production code. |
| **MAJOR-08** | `startsWithCaseInsensitive` micro-optimization on a non-hot path | **NOT ADDRESSED** ❌ | `media_downgrade.go:350-368` still defines the helper. The "hot path" comment is still wrong — `TryMediaDowngrade` runs at most once per LLM 4xx response (per-class `atomic.Bool` guards). The same helper is called from `loop.go:6871` (per r1) and now from `callMessagesCarryMedia` at `media_downgrade.go:280` (the r2 corrections added this caller). Three call sites vs zero benefit. |

**Re-verification summary:** 2 fully addressed, 1 partial, 1 partial-but-better, 5 not addressed. The corrections did not adopt the r1 simplification set as a whole; they focused on the type-design / contract / silent-failure MAJORs and left the code-shape MAJORs for a later wave.

---

## New over-engineering introduced by the corrections

### MAJOR-r2-1 — `MediaDowngradeResult.MediaClass` is never populated — the typed result has a broken half

TD-M8's central claim is that `TryMediaDowngrade` now returns enough information for the loop to make a verdict without recomputing the classifier. The struct is `MediaDowngradeResult{Applied, Trigger, MediaClass}`. The `Trigger` field is set correctly:

```go
// media_downgrade.go:151-185
func TryMediaDowngrade(...) MediaDowngradeResult {
    ...
    if code == CodeMediaUnsupported {
        result, _ := ts.applyMediaDowngrade(callMessages)   // <-- second return discarded
        result.Trigger = TriggerClassifierPrimary
        return result
    }
    ...
    result, _ := ts.applyMediaDowngrade(callMessages)
    result.Trigger = TriggerOutcomeFallback
    return result
}
```

But `applyMediaDowngrade` returns `(MediaDowngradeResult, MediaClass)` — the second value carries the affected class (PDF or Image). The caller discards it via `result, _ :=`. The struct field `result.MediaClass` is therefore **always `MediaClassNone` (zero value)**, regardless of what `applyMediaDowngrade` actually stripped.

The loop then logs:

```go
// loop.go:6935
"media_class": string(downgradeResult.MediaClass),  // ALWAYS "none"
```

This means:
1. The `MediaClass` enum (`MediaClassPDF` / `MediaClassImage` / `MediaClassNone`) is dead at the call-site level — only the `applyMediaDowngrade` body's internal return value exercises it.
2. The warn-log field `media_class` is permanently `"none"` on every downgraded turn, including the Gemini SVG BDD row 1013 case where a real image strip happened.
3. The `MediaDowngradeResult.MediaClass` field exists but is write-only — the struct invariant `Applied=true → MediaClass≠None` is violated on every successful downgrade.

**Fix.** Either:
- (a) `applyMediaDowngrade` returns only `MediaDowngradeResult` and sets `result.MediaClass` before returning (`return MediaDowngradeResult{Applied: true, MediaClass: MediaClassPDF}`), OR
- (b) the caller reads the second value: `result, class := ts.applyMediaDowngrade(...); result.MediaClass = class; return result`.

Either fix is 1-2 lines. The current code introduces a typed result with three fields, but only two of them are populated — the third is a dead field masquerading as data.

**Evidence.**
- `media_downgrade.go:194-217` (the function that returns the tuple).
- `media_downgrade.go:162` and `:182` (the two discard sites).
- `media_downgrade.go:200` and `:209` (the two return statements that DO compute MediaClassPDF/Image — but the data is dropped before the typed result can carry it).
- `loop.go:6935` (the read site — always "none").
- No test asserts `result.MediaClass` (grep `pkg/agent/media_outcome_retry_test.go` for `MediaClass` → 0 hits). The TD-M8 regression tests assert `result.Trigger` and `ts.imageRetryDone.Load()` but never `result.MediaClass`.

### MAJOR-r2-2 — `UploadFixture` duplicates `Upload` (~100 LOC of functional identity, diverging only in 2 lines)

`pkg/media/library/library.go` ships **two ~95-LOC upload paths** that differ in only three things:

| Aspect | `Upload` (library.go:338-452) | `UploadFixture` (library.go:987-1088) |
|---|---|---|
| `source` validation | `source != gen.UserUpload → ErrSourceNotAllowed` (line 347-349) | none — `fixtureSource` is hardcoded |
| Source value passed | from caller | `fixtureSource` (line 1059) |
| Directory sync after rename | YES — `directory.Sync()` (library.go:401-409) | **NO** |
| Body | identical `os.CreateTemp` + chmod + ReadFull + sha256 + io.Copy + Sync/Close + Rename + newManifestEntry + persistLocked + rollback paths | identical |

The 100-LOC bodies are byte-for-byte the same except for:
1. The 3-line source check at the top (Upload only)
2. The `directory.Sync()` block at line 401-409 (Upload only — `UploadFixture` skips it, which is also a latent defect: a test-fixture upload that crashes mid-write leaves the dir in an inconsistent state on disk)
3. The `source` argument to `newManifestEntry` (line 423 vs line 1059)

The rationale for the split (per the TD-m1 commit message and the doc-comment at library.go:975-987) is that `test_fixture` must not be a public wire value. But this goal can be satisfied with one shared internal function:

```go
// internal, called by Upload + UploadFixture
func (l *Library) uploadWithSource(filename string, source gen.MediaLibraryEntrySource, reader io.Reader, syncDir bool) (string, gen.MediaLibraryEntry, error) {
    // ... the 95 lines of body ...
}

func (l *Library) Upload(filename string, source gen.MediaLibraryEntrySource, reader io.Reader) (string, gen.MediaLibraryEntry, error) {
    if source != gen.UserUpload {
        return "", gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrSourceNotAllowed, source)
    }
    return l.uploadWithSource(filename, source, reader, true)
}

func (l *Library) UploadFixture(filename string, reader io.Reader) (string, gen.MediaLibraryEntry, error) {
    return l.uploadWithSource(filename, fixtureSource, reader, true)
}
```

That collapses ~190 LOC to ~110 LOC, fixes the `UploadFixture`-no-sync defect by inheriting the same parameter, and keeps the public API stable.

**Evidence.**
- `library.go:338-452` (`Upload` body, ~115 LOC).
- `library.go:987-1088` (`UploadFixture` body, ~102 LOC).
- `git diff <(sed -n '338,452p' library.go) <(sed -n '987,1088p' library.go) | wc -l` — the diff is small (10-15 LOC); the rest is identical.
- The TD-m1 fix added the duplication where r1 had one `Upload` function. The split was avoidable by parameterizing `source` validation against a closed set or by lifting the source check to the wire boundary (`gen.MediaLibraryEntrySource.Valid()` covers `user_upload` and `tool_output`; `test_fixture` is intentionally not on the wire, so the public API can simply reject `source == fixtureSource` and a private wrapper can supply `fixtureSource`).

### MAJOR-r2-3 — `Version` type is 194 LOC of hand-rolled semver parsing when `golang.org/x/mod/semver` exists

`pkg/providers/capabilities/version.go` (194 LOC) defines a `Version` type with a `ParseVersion(s string) (Version, error)` parser and a `Compare(other Version) int` comparator. The parser handles three semver shapes (`v1`, `v1.2`, `v1.2.3-prerelease`) with hand-rolled digit scanning. The comparator has special cases for prerelease (`"" > any prerelease`), numeric `major`/`minor`/`patch` ordering, and a lexical fallback for non-semver strings.

The lex-fallback handles ISO-date strings like `"2026-07-23"` (the embedded seed's version). `golang.org/x/mod/semver` does not support date strings, but the simpler split is:
- "is this string semver-shaped?" → `semver.IsValid(s)` (yes for `v10.0.0`, no for `2026-07-23`).
- If yes: compare numerically via `semver.Compare(s, other)`.
- If no: compare lexically via `strings.Compare(s, other)`.

That is ~15 LOC including the date-fallback branch, vs the current 194. The r2 correction reimplemented semver parsing instead of using the stdlib-adjacent package. The package isn't currently in go.mod (confirmed: `grep "golang.org/x/mod" go.mod` → 0 hits), but it's a single-line `require golang.org/x/mod v0.x.0` addition with no transitive deps beyond what's already there (golang.org/x/* is already a heavy indirect set per go.mod:152-184).

A smaller concern: `ParseVersion` declares an `error` return but never produces one (`return v, nil` on every path). The doc-comment reserves the error for "future strict-mode validation" — speculative generality that costs the caller a `require.NoError(t, err)` line in 5 test rows (catalog_test.go:368, :370, :416) without paying for it.

**Fix.** Drop the `Version` type and the comparator file entirely. Replace `ParseVersion(s).Compare(otherVersion)` with `(semver.Compare(s, other) when semver.IsValid both sides else strings.Compare(s, other))` inside `refreshLocked` (catalog.go:643). The catalog state can hold `version string` (raw) and the compare is inlined at the one call site. Saves ~190 LOC.

**Evidence.**
- `version.go` is 194 LOC; the public API surface is `ParseVersion(string) (Version, error)` and `Version.Compare(other) int` — 2 functions.
- The compare site is one: `catalog.go:643` (`if currentVersion.raw != "" && s.Version.raw != "" && s.Version.Compare(currentVersion) < 0`).
- 194 LOC for one inline-conditional is not proportionate. The file also has a `parseSemverPrefix` + `parseSemverRest` private pair (54 LOC, hand-rolled digit scanning) that re-implements what `strconv.ParseUint` + `strings.IndexByte` could do in 5 lines.

### MAJOR-r2-4 — `KnownModality` map is dead at runtime; consulted only by tests

`pkg/providers/capabilities/modality.go:39-45` declares:

```go
var KnownModality = map[Modality]bool{
    ModalityText:  true,
    ModalityImage: true,
    ModalityPDF:   true,
    ModalityAudio: true,
    ModalityVideo: true,
}
```

with the doc-comment claim "the runtime's recognition boundary". The actual recognition logic in `resolvedModel.Supports` iterates the slice (catalog.go:125-131) — the map is not consulted anywhere in production code. The only references to `KnownModality` outside its definition are in tests (`catalog_test.go:199-201`).

The map is therefore a **publicly exported symbol that does nothing**. The forward-compat semantics ("unknown modalities are recorded as-is, runtime gate iterates the slice") are preserved by `Supports` without the map. The map adds:
- 1 public global var
- 1 doc-comment that overstates the role
- 1 indirection in test files (`assert.False(t, KnownModality[got[1]])`) that the tests could replace with `assert.NotContains(t, []Modality{ModalityText, ModalityImage, ...}, got[1])` — explicit and test-only.

**Fix.** Delete `KnownModality`. Replace test references with literal slice assertions.

**Evidence.**
- `modality.go:39-45` (definition + comment).
- `grep -rn "KnownModality" pkg/` → 2 hits (definition + 3 lines in catalog_test.go). No production reference.

### MAJOR-r2-5 — `pkg/api/generated/llm_error_codes_test.go` (238 LOC) uses Go AST parsing + text-searching a TS file — the test infrastructure outweighs the property it asserts

`d6827307` adds `pkg/api/generated/llm_error_codes_test.go` (238 LOC) as the "exhaustive-code regression guard" for SFH-W1-01 / TD-C1. It:
1. Parses `pkg/agent/translate_error.go` with `go/ast` (lines 53-105) to enumerate every `LLMErrorCode` constant value.
2. Text-matches the generated `_asyncapi-zod-schemas.generated.ts` file (lines 111-165) for `z.enum([` substrings and parses strings by hand from the TS source (lines 135-155 — manual character-by-character quote skipping with escape handling).
3. Marshals each code as an `LLMError` + `LLMErrorReplay`, validates against the canonical JSON schema via the existing `validateAgainstComponentSchemaRawJSON` helper.
4. Cross-references the AST-derived codes against the text-derived codes via a `map[string]struct{}` set membership check.

The simpler check that catches the same regression class (adding a new `LLMErrorCode` constant without updating the 4 schema layers):
- Read `contracts/components/schemas/LLMError.yaml` and `LLMErrorReplay.yaml` as text.
- For each `LLMErrorCode` constant declared in `pkg/agent/translate_error.go`, assert the string appears in both YAML files (the enums are `- <code>` lines).

That is ~30 LOC, no AST, no TS-text-parsing, no escape handling. The current test does ~238 LOC for what is essentially "the codes in the source appear in the schemas".

The contract-test machinery that does **matter** (round-tripping wire-shape JSON against canonical schemas) is already in the existing `contract_test.go` (362 test functions, 4243 LOC, with `validateAgainstComponentSchemaRawJSON` shared at line 205). The new file adds an additional enumeration layer that overlaps with what those tests already cover for known codes.

The TS-text-parsing piece (`liveCodesFromZodSchemas`, lines 111-165) is the most concerning — the function reads a `.generated.ts` file via `strings.Index` and hand-parses quoted strings, including escape handling (`if enumBody[j] == '\\' && j+1 < len(enumBody) { j += 2 }`). A generator-output format change silently breaks the parser without surfacing in any schema-validation path. A robust version would call `node -e` on the generated TS or compile the generated Zod enum as a fixture, but neither is justified by the test's purpose.

**Fix.** Replace `llm_error_codes_test.go` with a 30-LOC test:
- `for _, code := range knownCodes { assert.Contains(t, llmErrorSchemaYAML, "- " + code); assert.Contains(t, llmErrorReplaySchemaYAML, "- " + code); assert.Contains(t, asyncapiYAML, "- " + code) }`.

The contract test machinery that already round-trips wire shapes (the 362 functions in `contract_test.go`) catches any schema-shape regression. The new file's additional layer is over-engineered for its purpose.

**Evidence.**
- `pkg/api/generated/llm_error_codes_test.go` (238 LOC; 53-105 for AST, 111-165 for TS text-parsing, 200-238 for the round-trip + Zod assertion).
- `pkg/api/generated/contract_test.go` (362 test functions, 4243 LOC, with `validateAgainstComponentSchemaRawJSON` already used at 6+ sites).
- The 4-layer-code presence is verifiable with `strings.Contains` against the 3 schema files in ~10 lines.

---

### MINOR-r2-1 — `manifestEntryFromProjection` (43 LOC) duplicates `OrphanGC`'s post-quarantine rollback (14 LOC); both rebuild projection → entry

Three sites in `library.go` convert `gen.MediaLibraryEntry` → `manifestEntry`:

| Site | LOC | Validation strategy |
|---|---|---|
| `manifestEntryFromProjection` (library.go:831-873) | 43 | defensive nil-check on every wire field, then `newManifestEntry` for invariant re-validation |
| `OrphanGC` rollback (library.go:731-743) | 14 | assumes `Id != nil` (early continue), uses `deref*` helpers, no validation |
| (projection() is the inverse — value-typed → pointer-typed — but operates at the API edge, not from disk.) | 22 | n/a |

The two forward-projection paths use slightly different rules. The r1 MAJOR-09 fix to delete `validatePersistedEntry` reduced 31 LOC to 12 (the new `manifestEntry.validate()` at library.go:246-257), but the disk-load path now re-implements the validation as defensive nil-checks before calling `newManifestEntry` — and `OrphanGC` rollback skips even that. Three layers of "from projection to entry", each with subtly different rules.

**Fix.** Have `OrphanGC`'s rollback call `manifestEntryFromProjection` (it already has the parsed `gen.MediaLibraryEntry` projection at hand at library.go:718). Removes 14 LOC of inline rebuild, unifies the validation rules, and means future schema additions don't need to be tracked in three places.

**Evidence.**
- `library.go:731-743` (OrphanGC rollback — inline rebuild).
- `library.go:831-873` (manifestEntryFromProjection — disk-load rebuild).
- The `derefString` / `derefInt` / `derefInt64` / `derefTime` helpers (library.go:1122-1148) are only used by the inline rollback, not by `manifestEntryFromProjection`. Deleting the inline rollback deletes 4 helpers (27 LOC).

### MINOR-r2-2 — `r1 MAJOR-01 / MAJOR-02 / MAJOR-03 / MAJOR-05` not addressed by any correction

Five r1 code-simplifier MAJORs are unchanged in the r2 stack:
- `Puller` / `Store` interfaces (r1 MAJOR-01): still both interfaces declared, `Store` still has zero implementations.
- `pkg/media/resize` package boundary (r1 MAJOR-02): still a separate 203-LOC package.
- Two parallel media stores (r1 MAJOR-03): `FileMediaStore` + `Library` still coexist, neither migrated.
- Custom `logger` interface (r1 MAJOR-05): still `catalog.go:407-413`, `noopLogger` still at `catalog.go:662-666`.

These were the structural findings. The corrections focused on the type-design / contract / silent-failure MAJORs from the other reviewers (TD-M1 through TD-M8, TD-C1). The code-simplifier MAJORs are still in the tree.

The holistic r2 round (`wave1-review-round2-holistic.md:73-87`) marks these as "STILL OPEN — explicitly downstream-owned" but they are NOT downstream-owned; they are code-shape findings that would have been a 1-commit refactor. They ride into Wave 2 as inherited debt.

**Evidence.** grep against the r1 + r2 `library.go` / `catalog.go` shows the 5 MAJOR sites are byte-identical to the r1 snapshot except for the TD-M1+TD-M2 (manifestEntry) and TD-M3+TD-M4 (private model + serialized Refresh) patches.

### MINOR-r2-3 — `outcomeRelabelCode()` accessor is still write-only dead state

The r1 type-design-analyzer (TD-M8, line 27) and code-reviewer (W1-CR-02, line 19) explicitly flagged that `turnState.outcomeRelabel` has no production read site. The r2 TD-M8 correction (`4f70672d`) adds the typed `MediaDowngradeResult` but does NOT add a production read site for `outcomeRelabelCode()`. `grep -rn "outcomeRelabelCode" pkg/ cmd/ internal/` returns exactly 1 hit — the definition at `turn.go:322`. The setter `setOutcomeRelabel` is called at `loop.go:6950`, but the getter has no production caller.

The r2 holistic (`wave1-review-round2-holistic.md:13`) claims "the outcome-relabel is consumed in production" — that is incorrect. The "consumption" is the warn-log emitting the original `helperCode` (line 6933), NOT the relabeled verdict. The relabel is written into turnState but never surfaces to a transcript, audit event, telemetry, or UI.

**Fix.** Either:
- Add a production read site — e.g., the emit site at the turn-end emit (line ~7000) consults `ts.outcomeRelabelCode()` and uses it as the recorded classifier verdict; OR
- Delete `outcomeRelabel` + `setOutcomeRelabel` + `outcomeRelabelCode` (turn.go:261-271, 307-326, ~25 LOC) — the field is write-only; without a consumer, it is dead state.

Either fix closes the W1-CR-02 / TD-M8 finding that the r2 corrections did not address.

**Evidence.**
- `turn.go:307-327` (setter + getter).
- `loop.go:6950` (setter call site).
- `grep -rn "outcomeRelabelCode" pkg/ cmd/ internal/` → 1 hit (definition only).

### MINOR-r2-4 — `clone()` on `manifestEntry` is a thin value-copy wrapper

`library.go:210-212`:

```go
func (m manifestEntry) clone() manifestEntry {
    return m
}
```

This is a 3-line method on a value-typed struct with no pointer fields — a Go value copy does exactly the same thing. The method exists for "type-domain symmetry" per the doc comment. Call sites (`library.go:574` Delete, `:632` CascadeDelete, `:743` OrphanGC rollback — wait, that one is the inline rebuild, not clone; `:896` changeRefcount) use `entry.clone()` for "consistency with future callers" — there are no future callers.

**Fix.** Inline `previousEntry := entry` at the call sites. Saves the method, no behavior change.

**Evidence.** `library.go:210-212` (the method). 4 call sites at `:574`, `:632`, `:896`, and `:643` (the inline rollback path uses `previousManifest := make(map[string]manifestEntry, len(l.manifest))` directly without calling clone — interesting inconsistency).

### MINOR-r2-5 — Wave-1 fix-pass and TD-history comments narrate r1 review process (project convention violation)

The project convention (`CLAUDE.md` "DO NOT ADD ***ANY*** COMMENTS unless asked") is to minimize narrative comments. The corrections added multiple multi-line comments documenting the r1 review findings and TD numbers:
- `library.go:1-40` (40-line package doc with TD-M1, TD-M2, TD-m1, TD-m4 references).
- `media_downgrade.go:1-9` (Wave 1 fix pass narrative).
- `media_downgrade.go:71-79` (TD-M8 reference inside TryMediaDowngrade comment).
- `media_downgrade.go:170-176` (TD-M7 reference inside Path 2 comment).
- `library.go:128-141` (TD-M2 reference in manifestEntry doc).
- `version.go:1-19` (TD-M5 reference in version.go header).
- `modality.go:1-12` (TD-M5 reference in modality.go header).
- `catalog.go:30-53` (TD-M5 + TD-M3 + TD-M4 references in design section).

The TD-IDs are an artifact of the review pipeline. Future maintainers reading `library.go:10` and seeing `// Internal invariant (Wave 1 TD-M1 / TD-M2):` will need to grep the repo for "TD-M1" to understand what the comment means. The substantive invariants are valid; the TD-IDs are not. A trimmed comment ("Refcount is non-negative; the constructor `newRefcount` rejects negative values") carries the load-bearing signal without the review-process breadcrumbs.

**Fix.** Trim each multi-line TD-narrative to 2-3 lines of load-bearing WHY. Drop the TD-IDs. The historical chain is recoverable from `git log -p --grep TD-M1` for any future reader who needs it.

**Evidence.** grep for `// Wave 1 TD-` across `pkg/` → 14 hits in 7 files.

### OBS-r2-1 — `manifestEntry.validate()` re-validates filename via `normalizeFilename` even though the constructor already did

`library.go:246-257`:

```go
func (m manifestEntry) validate(workspaceID string) error {
    if m.workspaceID != workspaceID { ... }
    if _, err := normalizeFilename(m.filename); err != nil { ... }
    if m.refcount < 0 { ... }
    return nil
}
```

`normalizeFilename` was already called inside `newManifestEntry` (library.go:166-169) at construction. The `validate()` method's re-normalize is a "defense-in-depth" check that re-does work the constructor already did. It's load-bearing for the Load path (which receives already-persisted entries), but the constructor has already been called in `manifestEntryFromProjection` (library.go:861) before `validate()` runs (library.go:815) — so the normalizeFilename call has already happened twice.

**Fix.** Drop the filename re-check from `validate()`. The check is a redundant second pass; the constructor path is `manifestEntryFromProjection → newManifestEntry → validate`, and `newManifestEntry` is the authoritative validator. The 3-line normalization re-check in `validate()` is dead.

This is similar to the r1 MAJOR-09 finding (defensive layering) — the correction reduced the function from 31 LOC to 12 but kept the "validate everything again" pattern. One more pass is sufficient.

### OBS-r2-2 — `cloned := false` pattern in `OrphanGC` rollback via projection list

`library.go:721-746`: after a `persistLocked` failure in `OrphanGC`, the rollback rebuilds manifest entries from the persisted projection list. The rebuild uses `derefString`/`derefInt`/`derefInt64`/`derefTime` helpers (library.go:1122-1148, 27 LOC of helpers). These helpers exist only for this one call site.

`manifestEntryFromProjection` (library.go:831-873) does the same conversion without the helpers, instead using defensive `if p.Foo == nil { ... }` blocks. **Two functions perform the same projection → entry conversion; they use different helper strategies.** See MINOR-r2-1 for the recommended fix (have the rollback call `manifestEntryFromProjection`).

### OBS-r2-3 — `noopLogger` reimplementation of `slog.DiscardHandler`

`catalog.go:662-666` defines a local `noopLogger{}` with empty `Warn` and `Info` methods. Go 1.24+ ships `slog.DiscardHandler` (catalog_test.go:86 uses it: `slog.New(slog.DiscardHandler)`). The catalog could accept `log *slog.Logger` and use `slog.New(slog.DiscardHandler)` as the nil-default (replacing `log = noopLogger{}` at catalog.go:442 with `log = slog.New(slog.DiscardHandler)`).

This is the same finding as r1 MAJOR-05 / OBS-03. The corrections did not address it.

### OBS-r2-4 — `catalog_test.go` test preamble inventories now span ~440 LOC of preamble + test rows

The TD-M5 correction added `TestVersion_*` (7 tests, catalog_test.go:358-462) and the validation tests (~190 LOC). Combined with the r1 inventory of catalog_test.go (preamble 1-100 + 41 test funcs), the test file is now 988 LOC with ~80 LOC of preamble. The preamble inventories are useful but heavy.

A 5-line preamble (package name, FR/ADR reference, data-flow overview) per file is the project convention; the full inventory is recoverable from `grep -c "^func Test"` if needed.

---

## What the corrections got right (positive observations)

The 10 follow-up commits delivered substantive value on the type-design / contract / silent-failure axes that the r1 type-design-analyzer flagged. Specifically:

| TD | Closed | Evidence |
|---|---|---|
| **TD-M1 + TD-M2** | ✓ | `manifestEntry` private + required values; `refcount` newtype with `newRefcount` constructor; single refcount source of truth (no `entry.Refcount` vs `refcounts[id]` disagreement possible); `gen.MediaLibraryEntry` is wire projection only. |
| **TD-M3 + TD-M4** | ✓ | `model` private; `resolvedModel` deep-owned handle with accessor methods; `refreshMu` separate from `stateMu`; the 4-step Refresh transaction (pull → parse → version-check → apply → store) is serialized. |
| **TD-M5** | ✓ (seed validation) | Schema-version/updated-at/source non-empty; default budget positive; per-model ID unique + provider non-empty + modalities non-empty/dedup/include-text; budget positive when set; default applied when omitted. The bug class the r1 flagged (residual 4xx classifying as CodeProviderRejected) is closed. |
| **TD-M6** | ✓ | `resize.Budget`, `DefaultLongEdge`, `DefaultMaxBytes` deleted; `capabilities.ResizeBudget` is canonical; int64 bytes end-to-end. The duplicated-constant finding is closed. |
| **TD-M7** | ✓ | `outcomeFallbackEligible` accepts `CodeUnknown` only; classifier returns `CodeUnknown` for residual 4xx with non-pinned body; Gemini SVG BDD row 1013 regression-locked at media_outcome_retry_test.go:580 + 767. |
| **TD-M8 (partial)** | ⚠ | `MediaDowngradeResult{Applied, Trigger, MediaClass}` typed; `Trigger` correctly set; `loop.go` reads `downgradeResult.Trigger == TriggerOutcomeFallback` (no classifier recomputation); `setOutcomeRelabel(CodeMediaUnsupported)` fires on the success edge. But `MediaClass` is never populated (see MAJOR-r2-1) and the FR-017a relabel has no production read site (see MINOR-r2-3). |
| **TD-m1 + TD-m4** | ✓ | `Load`/`Store` are package-private; `UploadFixture` is the test-only entry; `test_fixture` removed from the production source enum. |
| **TD-C1 + SFH-W1-01** | ✓ | `tool_args` + `schema` added to all 4 schema layers (LLMError.yaml, LLMErrorReplay.yaml, asyncapi.yaml); contracts regenerated; runtime tests pass. The exhaustive contract test (llm_error_codes_test.go) is the over-engineered part (MAJOR-r2-5); the actual schema update is correct. |
| **B2** | ✓ | `media.delete` + `media.cascade_delete` audit events declared + emitted; cascade-delete hook wired from `handleWorkspaceDelete` at rest_workspaces.go:1233-1244; new M-r2-1 / M-r2-2 gaps surfaced (race + single-delete emitter missing) per the r2 holistic. |

The corrections are **disciplined on the load-bearing fixes** (domain types, contract drift, classifier narrowing, type-invariants on the wire shape). The over-engineering findings above are concentrated in the **secondary helpers** (UploadFixture duplication, Version type reinvention, dead KnownModality map, AST-parsing contract test, broken MediaClass field) — these don't undermine the load-bearing fixes but they carry their own maintenance cost.

---

## What the corrections did NOT do (carry-forward)

The r1 code-simplifier MAJORs that ride into Wave 2 / Wave 3 as inherited debt:

| R1 ID | Title | Status after r2 corrections |
|---|---|---|
| MAJOR-01 | `Puller`/`Store` interfaces, `Store` unimplemented | unchanged — interfaces remain |
| MAJOR-02 | `pkg/media/resize` as a separate package | unchanged — package boundary stands |
| MAJOR-03 | Two parallel media stores (FileMediaStore + Library) | unchanged — both still in tree |
| MAJOR-05 | Custom `logger` interface vs `*slog.Logger` | unchanged — interface + noopLogger remain |
| MAJOR-07 (partial) | `imageStripRange` variadic has only test callers | partial — test-only callers; production still no-opts |
| MAJOR-08 | `startsWithCaseInsensitive` micro-optimization | unchanged — helper remains |
| (NEW) | MAJOR-r2-1: `MediaClass` field never populated | NEW — introduced by TD-M8 |
| (NEW) | MAJOR-r2-2: `UploadFixture` duplicates `Upload` | NEW — introduced by TD-m1 |
| (NEW) | MAJOR-r2-3: `Version` reinvents semver | NEW — introduced by TD-M5 |
| (NEW) | MAJOR-r2-4: `KnownModality` is dead at runtime | NEW — introduced by TD-M5 |
| (NEW) | MAJOR-r2-5: `llm_error_codes_test.go` uses AST + TS text-searching | NEW — introduced by TD-C1/SFH-W1-01 |
| (r1 carry-forward) | MINOR-r2-3: `outcomeRelabelCode` accessor is still write-only | unchanged |

The corrections did not regress any of the r1 simplifications, but they did introduce **5 new MAJORs + 5 new MINORs** in the secondary helpers around the load-bearing fixes. Net effect: the r2 stack is **more typed but also more verbose**; the corrections carry ~960 production LOC vs the r1 baseline of ~700 production LOC for the same slice coverage.

---

## Verification (read-only)

| Gate | Command | Result |
|---|---|---|
| Wave-1 prompt rule honored | `git diff d0e7374a..HEAD -- 'pkg/agent/loop_media_test.go' 'pkg/agent/loop_test.go' 'pkg/agent/loop_media_normalization_test.go'` | 0 lines (no mutation) ✓ |
| Author identity | `git log --format='%an <%ae>' -15` | All 15 commits authored by Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com> ✓ |
| Co-authored-by trailer filter | `git log origin/main..HEAD --format='%(trailers:key=Co-authored-by)' \| grep -i anthropic` | empty ✓ |
| `Upload` vs `UploadFixture` near-duplication | `diff <(sed -n '338,452p' pkg/media/library/library.go) <(sed -n '987,1088p' pkg/media/library/library.go) \| wc -l` | small diff (~15 lines of source variation) — bodies are 95% identical |
| `outcomeRelabelCode` write-only | `grep -rn "outcomeRelabelCode" pkg/ cmd/ internal/` | 1 hit — definition only. No production caller. |
| `MediaClass` write-only | `grep -rn "result.MediaClass\|\.MediaClass =\|MediaClassPDF\|MediaClassImage" pkg/agent/` | constants used only inside `applyMediaDowngrade`'s return tuple, discarded by the caller |
| `KnownModality` runtime use | `grep -rn "KnownModality" pkg/` | 4 hits — 1 declaration + 3 test-only references. No production caller. |
| `Version` use | `grep -rn "ParseVersion\|capabilities.Version\b" pkg/ cmd/ internal/` | 6 hits — all in catalog.go/catalog_test.go (within the package); zero outside the package. |
| `noopLogger` vs `slog.DiscardHandler` | `grep -n "noopLogger\|slog.DiscardHandler" pkg/providers/capabilities/catalog.go pkg/providers/capabilities/catalog_test.go` | noopLogger declared + used in catalog.go; slog.DiscardHandler used in test only. |
| `logger` interface vs `*slog.Logger` | `grep -n "logger interface\|log \*slog.Logger" pkg/providers/capabilities/catalog.go` | interface still declared (catalog.go:407-413). |
| `imageStripRange` production callers | `grep -rn "stripRejectedImageMedia(" pkg/ cmd/ internal/ \| grep -v _test.go` | 1 hit — `media_downgrade.go:205` passes NO opts. |
| `startsWithCaseInsensitive` production callers | `grep -rn "startsWithCaseInsensitive" pkg/ cmd/ internal/ \| grep -v _test.go` | 1 hit — `media_downgrade.go:325` (plus `callMessagesCarryMedia`'s `strings.HasPrefix` at line 280, which is the correct pattern — proving the helper is unnecessary even on the same file). |

No Go test suite was run locally; per `CLAUDE.md`, CI is the authority. The full Go suite (especially `pkg/gateway` with `goolm`) is OOM-prone in this devpod.

---

## Verdict

**REVISE — 0 CRIT / 5 MAJOR / 5 MINOR / 4 OBS.**

The corrections deliver strong load-bearing fixes on the type-design / contract / silent-failure axes (TD-M1 through TD-M8, TD-C1/SFH-W1-01). The 5 new MAJORs introduced are concentrated in **secondary helpers** around the load-bearing fixes — UploadFixture duplication, Version type reinvention, dead KnownModality map, AST-parsing contract test, and the broken `MediaClass` field on `MediaDowngradeResult`. None of the MAJORs is a correctness regression in the load-bearing logic; all are over-engineering, premature abstraction, or surface-layer breakage in the helpers that wrap the fix.

The r1 simplifications were **2 fully addressed (MAJOR-04, MAJOR-06), 1 partially addressed (MAJOR-09), 6 NOT addressed (MAJOR-01, MAJOR-02, MAJOR-03, MAJOR-05, MAJOR-07, MAJOR-08)**. The r2 corrections focused on the type-design findings and left the code-shape findings for a later wave. The r2 stack is **more typed but also more verbose** — ~960 production LOC for the corrections vs ~700 production LOC for the r1 baseline.

**Recommend:** Apply MAJOR-r2-1 (populate MediaClass) and MAJOR-r2-2 (collapse Upload/UploadFixture) as a single follow-up commit (`fix(adr-051-rev4): Wave 1 r2 corrections A+B`); MAJOR-r2-3 (use `golang.org/x/mod/semver`) and MAJOR-r2-4 (delete `KnownModality`) as a second commit; MAJOR-r2-5 (simplify `llm_error_codes_test.go`) as a third. MINOR-r2-1 (unify projection → entry rebuilds) can ride with the second commit. MINOR-r2-3 (delete write-only `outcomeRelabel`) closes a r1 carry-forward and is non-controversial. The remaining MINORs and OBSs are cosmetic and can ride into Wave 2.

| ID | Scope | Fix size |
|---|---|---|
| MAJOR-r2-1 | `pkg/agent/media_downgrade.go` — populate `MediaClass` | 1-2 lines |
| MAJOR-r2-2 | `pkg/media/library/library.go` — collapse `Upload`/`UploadFixture` | ~80 LOC saved |
| MAJOR-r2-3 | `pkg/providers/capabilities/version.go` — use `golang.org/x/mod/semver` | ~190 LOC saved |
| MAJOR-r2-4 | `pkg/providers/capabilities/modality.go` — delete `KnownModality` | ~10 LOC |
| MAJOR-r2-5 | `pkg/api/generated/llm_error_codes_test.go` — replace AST + TS-text with `strings.Contains` | ~200 LOC saved |

The slice's correctness posture (per the r2 holistic, silent-failure-hunter, and pr-test-analyzer reports) is **not** in scope for this review and is presumed clean. This review is solely on **shape**: whether the abstractions, layers, and helper surfaces added by the corrections are proportionate to the load-bearing logic they wrap.

*End of review.*
