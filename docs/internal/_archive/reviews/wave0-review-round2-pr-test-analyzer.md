# Wave 0 — Slice A (Contracts Foundation) — pr-test-analyzer review (round 2)

**Branch:** `sendfile-fix`
**HEAD at review time:** `d0e7374a` (docs(adr-051-rev4): Wave 0 / Slice A round-2 verification evidence)
**Round-2 test commit under review:** `48666ec5` (fix(gen-asyncapi): regenerate output + drift gate + branch coverage tests)
**Reviewer:** pr-test-analyzer (read-only)
**Scope:** Re-verify the 3 MAJORs (TA-2, TA-3, TA-4) and 1 MINOR (TA-5) flagged in round 1 by reading `scripts/gen-asyncapi-go/main_test.go` against `scripts/gen-asyncapi-go/main.go`. Then hunt for any remaining untested branches in `matchingNamedInlineGoType`.

---

## Verification of test execution

Run locally (single-scoped, Go test binary is cheap; full suite is not run in this devpod per CLAUDE.md):

```
$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -v -run '^TestGenerate' ./scripts/gen-asyncapi-go/
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
ok      github.com/elicify-ai/omnipus/scripts/gen-asyncapi-go      0.003s
```

All 5 Generate* tests pass; the 4 newly-added branch-coverage tests are exercised by CI (`go test -tags goolm,stdjson -count=1 ./...` per `.github/workflows/pr.yml:362-388`, `./...` includes `scripts/gen-asyncapi-go/`). ✅

---

## Re-verification of round-1 MAJORs

### TA-2 (ref-property short-circuit) — PARTIALLY RESOLVED → MINOR

**Code under test** (`scripts/gen-asyncapi-go/main.go:453`):

```go
if property.ref != "" || len(property.properties) == 0 {
    return "", false
}
```

**New test** (`main_test.go:132-166`): `TestGenerate_RefPropertyShortCircuits`. Sets `payload: {ref: "ErrorPayload"}`, asserts the generated field is `Payload *ErrorPayload `json:"payload,omitempty"``. The assertion is reached via the fall-through `resolveGoType` ref-resolve path.

**Branch-isolation analysis** — the early return is an OR. With the test's setup, both `property.ref != ""` AND `len(property.properties) == 0` are true (a `$ref`-only schema has no nested properties). The test cannot distinguish which sub-branch of the OR fired. The test's own comment acknowledges this honestly (lines 159-162):

> // shape stays the same here because the candidate name also resolves to ErrorPayload. The load-bearing assertion is that the generator does NOT crash and does NOT produce a `struct{...}` shape.

To isolate the ref sub-branch, the test would need a property that has BOTH `ref != ""` AND `len(properties) > 0` (a hybrid schema that real specs do not produce). So strict branch coverage of the ref-check is not achievable with a realistic schema.

What the test DOES catch:
- Generator panic / error on a ref-only property.
- Generator emitting anonymous struct instead of `*Name` for a ref.
- Generator emitting wrong JSON tag for a ref.

What it does NOT catch:
- Removal of the `property.ref != ""` sub-check (would still pass because the empty-properties sub-check also fires).
- Removal of `matchingNamedInlineGoType` entirely (resolveGoType handles refs directly).

**End-to-end regression protection: adequate.** The prior hand-fix protected `ErrorFrame.payload`'s `*ErrorPayload` shape; this test protects the equivalent regenerated output for any future schema with a ref-only payload. The TA-2 MAJOR is **downgraded to MINOR** on the basis that branch isolation is not achievable (the OR'd early-return cannot be independently tested without an unrealistic schema), but the functional coverage is real.

### TA-3 (required-field branch) — FULLY RESOLVED

**Code under test** (`scripts/gen-asyncapi-go/main.go:461-464`):

```go
if isRequired {
    return candidateName, true
}
return "*" + candidateName, true
```

**New test** (`main_test.go:93-124`): `TestGenerate_RequiredMatchingPropertyReturnsValueType`. Sets `required: {payload: true}`, asserts the generated field is `Payload ErrorPayload `json:"payload"`` (no pointer, no omitempty). This is a clean, isolated test:

- If the `isRequired` branch were removed (always returned `"*"+candidateName`), the test would FAIL (it expects no pointer, no omitempty).
- If the test setup were swapped to `required: {}`, the test would FAIL (the optional branch returns `"*ErrorPayload"` with omitempty).

**Branch isolation: confirmed.** ✅ TA-3 RESOLVED.

### TA-4 (sameSchemaShape false-return paths) — PARTIALLY RESOLVED → MAJOR

**Code under test** (`scripts/gen-asyncapi-go/main.go:477-514`):

```go
func sameSchemaShape(left, right *schema) bool {
    if left == nil || right == nil {
        return left == right
    }
    if left.schemaType != right.schemaType ||
        left.format != right.format ||
        left.ref != right.ref ||
        left.additionalProps != right.additionalProps ||
        left.constValue != right.constValue ||
        left.isEnum != right.isEnum ||
        !slices.Equal(left.enum, right.enum) ||
        !maps.Equal(left.required, right.required) ||
        len(left.properties) != len(right.properties) ||
        len(left.oneOf) != len(right.oneOf) ||
        len(left.anyOf) != len(right.anyOf) {
        return false
    }
    for name, leftProperty := range left.properties {
        rightProperty, ok := right.properties[name]
        if !ok || !sameSchemaShape(leftProperty, rightProperty) {
            return false
        }
    }
    if !sameSchemaShape(left.items, right.items) {
        return false
    }
    for index := range left.oneOf {
        if !sameSchemaShape(left.oneOf[index], right.oneOf[index]) {
            return false
        }
    }
    for index := range left.anyOf {
        if !sameSchemaShape(left.anyOf[index], right.anyOf[index]) {
            return false
        }
    }
    return true
}
```

**New test** (`main_test.go:174-219`): `TestGenerate_ShapeMismatchFallsBackToInline`. inlineShape has 2 props (`message`, `detail`), namedShape has 1 prop (`message`). Asserts NOT `Payload *ErrorPayload ` AND IS `Payload struct`.

**Trace**: `len(left.properties) == 2 != len(right.properties) == 1` triggers the `return false` at line 489. `matchingNamedInlineGoType` returns `("", false)`, falls through to `resolveGoType` which emits the anonymous `struct{...}`. ✅ Branch isolated for this specific mismatch type.

**What's still untested** in `sameSchemaShape` (15+ remaining false-return paths):

| # | Branch | Coverage |
|---|---|---|
| 1 | `left == nil && right != nil` (early nil) | ✗ |
| 2 | `left != nil && right == nil` (early nil) | ✗ |
| 3 | `left.schemaType != right.schemaType` | ✗ |
| 4 | `left.format != right.format` | ✗ |
| 5 | `left.ref != right.ref` | ✗ |
| 6 | `left.additionalProps != right.additionalProps` | ✗ |
| 7 | `left.constValue != right.constValue` | ✗ |
| 8 | `left.isEnum != right.isEnum` | ✗ |
| 9 | `!slices.Equal(left.enum, right.enum)` | ✗ |
| 10 | `!maps.Equal(left.required, right.required)` | ✗ |
| 11 | `len(left.oneOf) != len(right.oneOf)` | ✗ |
| 12 | `len(left.anyOf) != len(right.anyOf)` | ✗ |
| 13 | `rightProperty not ok` (property missing in right, lengths equal) | ✗ |
| 14 | `!sameSchemaShape(leftProperty, rightProperty)` (nested prop content mismatch) | ✗ |
| 15 | `!sameSchemaShape(left.items, right.items)` | ✗ |
| 16 | `!sameSchemaShape(oneOf[index], right.oneOf[index])` (length-equal but content differs) | ✗ |
| 17 | `!sameSchemaShape(anyOf[index], right.anyOf[index])` (length-equal but content differs) | ✗ |

Coverage went from 0/17 to 1/17. The TA-4 MAJOR remains MAJOR — only one specific mismatch type (property count) is covered. The other 16 branches are still load-bearing guard rails for future schema additions.

### TA-5 (Non-Frame owner) — NOT RESOLVED (claim vs reality mismatch)

**Round-1 finding** (`wave0-review-round1-pr-test-analyzer.md:19`): "Non-`Frame` owner (no suffix to trim) is untested. ... candidate-construction logic for e.g. `ToolCall` + property `tool_call` matching schema `ToolCallToolCall` is unverified."

**Claimed test** (`main_test.go:228-265`): `TestGenerate_OptionalInverseCase`. The function header (lines 222-227) labels it as "(TA-5)". The verification doc (`wave0-review-round2-verification.md:88`) also asserts "The 5 new tests cover the four pr-test-analyzer branches (TA-2 / TA-3 / TA-4 / TA-5)".

**Reality** — read the test setup (lines 229-251):

```go
schemas := map[string]*schema{
    "ErrorFrame": {                          // <-- FRAME-SUFFIX owner, not a Non-Frame owner
        schemaType: "object",
        properties: map[string]*schema{
            "payload": {
                schemaType:    "object",
                properties:    map[string]*schema{},    // <-- empty properties triggers a different branch
                propertyOrder: []string{},
                required:      map[string]bool{},
            },
        },
        ...
    },
    "ErrorPayload": {...},
}
```

The test exercises the **empty-properties short-circuit** branch (`main.go:453`: `len(property.properties) == 0`) with an `ErrorFrame` owner — not a non-Frame owner like `ToolCall`. The candidate-name construction for a non-Frame owner (where `strings.TrimSuffix(ownerGoName, "Frame")` is a no-op) is NOT tested.

The function header comment is internally inconsistent: it claims "TA-5: ... when the named schema exists with a matching shape and the field is OPTIONAL" but the test asserts the empty-properties branch (the inline has ZERO properties, so the named-schema matcher short-circuits). The test does correctly cover the empty-properties branch, which IS a real branch in the function — but it does NOT cover the non-Frame owner scenario that TA-5 originally raised.

**Branch coverage: the empty-properties short-circuit is now exercised** (which is a genuine gap-fill, NOT in the round-1 list — there was no TA-X for it explicitly). The non-Frame owner scenario from TA-5 remains untested. Verdict: TA-5's specific scenario is **NOT RESOLVED**; the new test is mislabeled.

---

## New finding: TA-11 — `candidate not in map` branch is untested

**Code under test** (`scripts/gen-asyncapi-go/main.go:457-459`):

```go
candidate, ok := allSchemas[candidateName]
if !ok || !sameSchemaShape(property, candidate) {
    return "", false
}
```

The OR'd condition means: when `!ok` (candidate name not present in the schema map), the function returns `("", false)`. This branch is currently untested by all 5 tests. None of them omit the candidate schema from the map.

**Regression scenario**: a future schema set renames `ErrorPayload` to something else (e.g., `ErrorBody`), or introduces a new owner (e.g., `ContextFrame`) whose property `payload` has no matching `ContextPayload` schema in the map. The generator must fall back to anonymous inline struct, not silently produce `*ContextPayload` against an undefined type. No test guards this.

**Suggested test** (`TestGenerate_NoMatchingSchemaFallsBackToInline`):

```go
inlineShape := &schema{
    schemaType: "object",
    properties: map[string]*schema{
        "message": {schemaType: "string"},
    },
    propertyOrder: []string{"message"},
    required:      map[string]bool{"message": true},
}
schemas := map[string]*schema{
    "SomeFrame": {  // no SomeFramePayload sibling exists in the map
        schemaType: "object",
        properties: map[string]*schema{"payload": inlineShape},
        propertyOrder: []string{"payload"},
        required:      map[string]bool{},
    },
    // deliberately NO "SomeFramePayload" in the map
}
src, err := generate(schemas)
if err != nil { t.Fatalf("generate: %v", err) }
if strings.Contains(string(src), "*SomeFramePayload ") {
    t.Fatalf("missing candidate must NOT coerce to *SomeFramePayload:\n%s", src)
}
if !strings.Contains(string(src), "Payload struct") {
    t.Fatalf("expected fallback to anonymous struct emit:\n%s", src)
}
```

---

## Summary table — branch coverage of `matchingNamedInlineGoType`

| Branch | Round-1 status | Round-2 status | Test |
|---|---|---|---|
| `property.ref != ""` → `("", false)` | UNTESTED | PARTIALLY TESTED (OR'd w/ empty-properties, can't isolate) | `TestGenerate_RefPropertyShortCircuits` |
| `len(property.properties) == 0` → `("", false)` | UNTESTED | TESTED (but mislabeled as TA-5) | `TestGenerate_OptionalInverseCase` |
| `!ok` (candidate not in map) → `("", false)` | UNTESTED | UNTESTED | (none) — **TA-11 NEW** |
| `!sameSchemaShape(property, candidate)` → `("", false)` | UNTESTED | TESTED (1 of 17 mismatch types) | `TestGenerate_ShapeMismatchFallsBackToInline` |
| `isRequired` → `candidateName` | UNTESTED | TESTED | `TestGenerate_RequiredMatchingPropertyReturnsValueType` |
| not required → `"*" + candidateName` | TESTED | TESTED | `TestGenerateUsesMatchingNamedInlinePayload` |

`matchingNamedInlineGoType` branch coverage: 3.5/6 distinct branches (the ref branch counts as 0.5 because it shares an OR with the empty-properties branch).

## Summary table — `sameSchemaShape` false-return paths

| Branch | Round-1 status | Round-2 status |
|---|---|---|
| Nil-handling (left/right combinations) | UNTESTED | UNTESTED |
| `schemaType` mismatch | UNTESTED | UNTESTED |
| `format` mismatch | UNTESTED | UNTESTED |
| `ref` mismatch | UNTESTED | UNTESTED |
| `additionalProps` mismatch | UNTESTED | UNTESTED |
| `constValue` mismatch | UNTESTED | UNTESTED |
| `isEnum` mismatch | UNTESTED | UNTESTED |
| `enum` slice differ | UNTESTED | UNTESTED |
| `required` map differ | UNTESTED | UNTESTED |
| `properties` length differ | UNTESTED | **TESTED** |
| `oneOf` length differ | UNTESTED | UNTESTED |
| `anyOf` length differ | UNTESTED | UNTESTED |
| Property missing in right | UNTESTED | UNTESTED |
| Nested property content mismatch | UNTESTED | UNTESTED |
| `items` mismatch | UNTESTED | UNTESTED |
| `oneOf` content mismatch (len equal) | UNTESTED | UNTESTED |
| `anyOf` content mismatch (len equal) | UNTESTED | UNTESTED |

`sameSchemaShape` false-return-path coverage: 1/17.

---

## Findings

| ID | Severity | File:Line | One-line | Fix |
|---|---|---|---|---|
| TA-2 | (was MAJOR → now MINOR) | `scripts/gen-asyncapi-go/main_test.go:132-166` | Ref short-circuit test exists but the OR'd early-return (`property.ref != "" \|\| len(property.properties) == 0`) makes strict branch isolation impossible without an unrealistic hybrid schema (ref+properties). The test does verify the end-to-end regression scenario; the load-bearing assertion is "generator does not crash on a ref-only property and emits `*Name` with omitempty". | Document the branch-isolation limitation in the test comment (the test already does this — accept as-is); or strengthen with a hybrid-schema test if you want absolute isolation. |
| TA-3 | (was MAJOR → RESOLVED) | `scripts/gen-asyncapi-go/main_test.go:93-124` | `TestGenerate_RequiredMatchingPropertyReturnsValueType` cleanly isolates the required-field branch. No pointer, no omitempty. ✅ | None. |
| TA-4 | (MAJOR, partial resolution) | `scripts/gen-asyncapi-go/main_test.go:174-219` | `TestGenerate_ShapeMismatchFallsBackToInline` covers ONE of 17 false-return paths in `sameSchemaShape` (specifically the `len(properties)` count mismatch). 16 other branches — schemaType/format/ref/additionalProps/constValue/isEnum/enum/required/oneOf/anyOf comparisons plus property missing-in-right, items, nested property content, and oneOf/anyOf content — remain untested. A regression in any of these could silently flip an inline-payload Go emit from anonymous struct to `*Name`. | Add at least one test per mismatch axis. Highest-leverage next additions: (a) `schemaType` mismatch (e.g., inline is `"object"`, named is `"string"`), (b) `required` map differ (e.g., inline requires `message`, named does not), (c) `additionalProps` differ (e.g., inline has open map, named is closed). |
| TA-5 | (was MINOR → still MINOR, **NOT RESOLVED**) | `scripts/gen-asyncapi-go/main_test.go:228-265` | The verification report and test header both claim `TestGenerate_OptionalInverseCase` covers TA-5 (Non-Frame owner), but the test uses an `ErrorFrame` owner — it actually covers the empty-properties short-circuit branch, not the non-Frame owner scenario that TA-5 raised. The candidate-name construction for non-Frame owners (where `strings.TrimSuffix(ownerGoName, "Frame")` is a no-op) is still untested. The non-Frame owner case (`ToolCall` + `tool_call` → `ToolCallToolCall`) is a real schema shape that doesn't appear in the live AsyncAPI spec but is a latent failure mode for future schema additions. | Either: (a) fix the test comment to honestly claim TA-2's empty-properties branch (drop the TA-5 reference), AND add a separate `TestGenerate_NonFrameOwnerMatches` for the non-Frame owner scenario; or (b) accept the mislabel and add only the missing non-Frame test. **Option (a) preferred** — the comment is currently internally inconsistent (claims TA-5 in the header but the body correctly describes empty-properties short-circuit). |
| TA-11 | NEW MAJOR | `scripts/gen-asyncapi-go/main.go:457-459` | `matchingNamedInlineGoType`'s `!ok` branch (candidate name not in `allSchemas`) is untested. None of the 5 Generate tests omit the candidate schema from the map. Regression scenario: a future schema set renames the sibling named schema, or introduces a new owner whose property has no matching `<Owner><Prop>` sibling. The generator must fall back to anonymous inline struct — no test guards this. | Add `TestGenerate_NoMatchingSchemaFallsBackToInline` per the suggested test in the body above. |
| TA-12 | OBSERVATION | `scripts/gen-asyncapi-go/main_test.go:222-227` | The function header for `TestGenerate_OptionalInverseCase` says "(TA-5)" but the test body actually covers a different branch (empty-properties short-circuit) than the original TA-5 finding (non-Frame owner). The body comment on line 257-261 correctly describes the empty-properties branch; only the header is wrong. Cosmetic but worth fixing because future maintainers grepping for `TA-5` will be misled. | Update the header to drop the "(TA-5)" tag and instead reference the empty-properties branch (which has no round-1 TA-X of its own — name it TA-X or leave it uncited). |
| TA-13 | OBSERVATION | `docs/internal/reviews/wave0-review-round2-verification.md:88` | The verification report claims "The 5 new tests cover the four pr-test-analyzer branches (TA-2 / TA-3 / TA-4 / TA-5)". This claim is accurate for TA-2/TA-3 (with TA-2 caveats), accurate for TA-4 (1 of 17 paths), and inaccurate for TA-5 (the test does not cover the non-Frame owner scenario). The doc also does not flag the empty-properties branch as a new gap-fill (which is what Test 5 actually covers). | Update the disposition line for TA-5 to note: "PARTIALLY FIXED — empty-properties branch covered; non-Frame owner scenario from TA-5 NOT covered (test mislabeled); 16 of 17 sameSchemaShape branches still untested; 1 new gap found (TA-11, candidate-not-in-map)." |

---

## Drift-gate (CI) coverage

Confirmed observed in `Makefile:384` (Commit D, 48666ec5) — `verify-asyncapi-drift` target runs the AsyncAPI Go generator and `git diff --exit-code pkg/api/generated/asyncapi_types.gen.go`. ✅ Coverage confirmed in CI.

The drift gate catches the **specific** regression that this branch coverage is meant to prevent: a generator change that breaks the wire shape. It does NOT catch a generator change that adds a new branch with no test (e.g., a future change that adds a third short-circuit condition) — that requires test coverage to catch.

## Go-test (CI) coverage

Confirmed: `go test -tags goolm,stdjson -count=1 ./...` per `.github/workflows/pr.yml:362-388` includes `scripts/gen-asyncapi-go/`. All 5 Generate tests + the pre-existing 3-way-collision test run in CI. ✅

## Test count delta

- Round 1 (commit `f6eccbcd`): 1 test (`TestWriteStruct_ThreeWayCollisionErrors`) + 1 test added in `0e7dcf5e` (`TestGenerateUsesMatchingNamedInlinePayload`) = 2 tests.
- Round 2 (commit `48666ec5`): 5 Generate tests + 1 pre-existing = 6 tests.
- Net delta: +4 tests, +220 LoC test code (lines 47-265 of `main_test.go`).

## Pre-existing test preservation

Confirmed observed: `TestWriteStruct_ThreeWayCollisionErrors` (the prior test at `scripts/gen-asyncapi-go/main_test.go:21-45`) is preserved untouched. ✅

---

## Verdict

**Verdict:** ACCEPT-WITH-FOLLOWUP (3 of 4 round-1 pr-test-analyzer findings addressed; TA-5's specific scenario remains uncovered despite the verification report's claim; one new untested branch (TA-11) discovered; TA-4 partially resolved).

**Counts:** **0** CRITICAL, **2** MAJOR (TA-4 partial; TA-11 new), **2** MINOR (TA-2 downgraded from MAJOR; TA-5 mislabeled), **2** OBSERVATION (TA-12 header comment; TA-13 verification report).

**Round-1 → round-2 delta:**

| Round 1 | Round 2 | Δ |
|---|---|---|
| 0 CRIT | 0 CRIT | — |
| 3 MAJOR | 2 MAJOR | -1 (TA-3 fully resolved; TA-2 downgraded; TA-4 partially resolved → still MAJOR) |
| 1 MINOR | 2 MINOR | +1 (TA-5 newly resolved as uncovered) |
| 6 OBSERVATION | 2 OBSERVATION | -4 (TA-6/7/8/10/... mostly outside this reviewer's re-scope) |

**Recommended next action** (in priority order, all non-blocking for Wave 1 since the live AsyncAPI spec only exercises the optional-pointer path):

1. **Add `TestGenerate_NoMatchingSchemaFallsBackToInline`** (TA-11, NEW MAJOR) — covers the `!ok` branch with a 15-line test.
2. **Add `TestGenerate_NonFrameOwnerMatches`** (TA-5 actual scenario) — non-Frame owner where `strings.TrimSuffix` is a no-op; e.g., `ToolCall` + `tool_call` → `ToolCallToolCall`.
3. **Fix the `TestGenerate_OptionalInverseCase` header comment** (TA-12) — drop the "(TA-5)" tag, keep the empty-properties branch description.
4. **Add at least one test per `sameSchemaShape` mismatch axis** (TA-4 closure) — 3-5 tests covering `schemaType`, `required` map, `additionalProps` mismatch. The `oneOf`/`anyOf` content branches can be deferred to when the live spec actually exercises them.
5. **Update the verification report** (TA-13) — TA-5 is NOT fully fixed; TA-11 is a new gap.

None of these is a blocker for Wave 1 (B1, B2, C, F, E). The live `contracts/asyncapi.yaml` currently exercises only the TA-1 path (optional-pointer match against `ErrorFrame.payload` / `ReplayErrorFrame.payload`). The uncovered branches are guard rails for future schema additions and for the round-1 follow-up in `pkg/llm-error.ts` / similar future inline-mirror owners.
