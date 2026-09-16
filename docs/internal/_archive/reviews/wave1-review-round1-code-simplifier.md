# Wave 1 Review — Round 1 — Code Simplifier

**Scope:** `d0e7374a..fba0acbf` (`sendfile-fix`, Slice B1 + C + F + E / Wave 1a+1b)
**Reviewer:** `pr-review-toolkit:code-simplifier` (round 1, READ-ONLY)
**Focus:** over-engineering — premature abstractions, speculative generality, unnecessary configurability, over-layered architecture, redundant package boundaries.

---

## Diff scope recap

```
24 files changed, 5134 insertions(+), 112 deletions(-)
```

New packages (production): `pkg/media/library/` (644 LOC), `pkg/media/resize/` (203 LOC), `pkg/providers/capabilities/` (catalog.go 468 + puller.go 314 + embed.go 23 + seed JSON 87 = 892 LOC). New/modified agent code: `pkg/agent/media_downgrade.go` (294 LOC, ext.), `pkg/agent/loop_media.go` (553 LOC, ext.), `pkg/agent/translate_error.go` (741 LOC, mostly existing classifier hoisted out), `pkg/agent/turn.go` (+34 LOC). New: `pkg/workspace/media_delete.go` (19 LOC). Tests: ~2,917 LOC across 5 files.

Production wiring status (verified by grep across `pkg/`, `cmd/`, `internal/`):

- **`pkg/media/library.Library`** — **not wired** anywhere in production. Only `pkg/media/library/library_test.go` calls `library.New`. Every `media.MediaStore` reference in `pkg/gateway`, `pkg/agent`, `pkg/tools` still uses the legacy `pkg/media.FileMediaStore` (`grep -rn "media.MediaStore" pkg cmd | wc -l` → 22 production sites).
- **`pkg/providers/capabilities`** — **not wired** anywhere in production. The only call sites are within the package itself. The doc comment's promise of a `pkg/agent/media_present.go` consumer names a file that does not exist (`grep -rn "media_present" pkg cmd` → 0 hits).
- **`pkg/media/resize`** — wired into `pkg/agent/loop_media.go::encodeImageToDataURL` (the only production caller). One import, one call.
- **`pkg/workspace/media_delete.go`** — `WorkspaceDeleteHook` is defined and **tested** (`pkg/media/library/library_test.go:419-435` imports `workspace.WorkspaceDeleteHook` and exercises the signature), but **never called** by `pkg/gateway/rest_workspaces.go::handleWorkspaceDelete` (`grep -n "WorkspaceDeleteHook" pkg/gateway/rest_workspaces.go` → 0 hits). The existing cascade at `pkg/gateway/rest_workspaces.go:1234` does `os.RemoveAll(wsDir)` which already removes the `media/` subdir as a side effect; the new hook is dead production code as written.

The new packages therefore ship as **storage scaffolding** — they compile, they test, they persist data structures, but they are not in the request path. This matters for over-engineering scoring: scaffolding without a consumer is where premature abstractions grow fastest, because no second caller exists to constrain them.

---

## Verification gates (round-1, no prior round)

| Gate | Command | Result |
|---|---|---|
| Wire-format drift | `make verify-contracts` | exit 0 (per the Wave 0 round-2 baseline; this commit only touches `MediaLibraryEntry.yaml` which is the existing schema). |
| Build tags | `make build` injects `-tags goolm,stdjson`; `grep -rn "providers/capabilities" pkg cmd` returns no production import outside the package itself | confirmed |
| Catalog wiring | `grep -rn "capabilities.New\|capabilities.Catalog\|GHReleasePuller" pkg cmd internal \| grep -v capabilities/` | 0 hits — package is unused in production |
| Library wiring | `grep -rn "library.New\|library.Library" pkg cmd internal \| grep -v library/` | 0 hits — package is unused in production |
| Hook wiring | `grep -rn "WorkspaceDeleteHook" pkg cmd internal` | 7 hits — 1 definition (`pkg/workspace/media_delete.go:9`) + 5 test references in `pkg/media/library/library_test.go:419-435` (a `TestWorkspaceLibrary_WorkspaceDeleteHookSignature` test that imports and exercises the function via `workspace.WorkspaceDeleteHook`) + 0 production callers. The function is wired to **test only**, not to `handleWorkspaceDelete` (`grep -n "WorkspaceDeleteHook" pkg/gateway/rest_workspaces.go` → 0 hits). |

The packages compile and the test files exist. The verification gap is **what is the second consumer** of the new abstractions — and the answer, in this commit, is **none**. Each MAJOR below scores against that gap.

---

## Findings

### MAJOR-01 — `pkg/providers/capabilities` defines two interfaces (`Puller`, `Store`) and one concrete `GHReleasePuller` with zero second implementations

`catalog.go:106-121` defines:

```go
type Puller interface { Pull(ctx context.Context) ([]byte, error) }
type Store interface  { Read(ctx context.Context) ([]byte, error); Write(ctx context.Context, data []byte) error }
```

`puller.go` is the **only** concrete implementation of `Puller` (`GHReleasePuller`). `Store` has **no production implementation at all** — `NewCatalog` accepts `Store` as a parameter and gracefully no-ops when it's `nil` (`catalog.go:241`). The doc comment for the `Puller` interface (`catalog.go:96-108`) lays out a multi-implementation design intent ("Implementations MUST be safe to call from multiple goroutines…"), and the package-claim "this package is the source of truth for 'which models accept which input modalities' used by the Layer-1 presentation orchestrator (pkg/agent/media_present.go, **Wave 3**)" (lines 6-8) names a file that does not exist.

The Puller/Store interface pair is **speculative generality**: a single concrete `GHReleasePuller` plus a nilable `Store` parameter can satisfy every requirement without the interface declarations. The interface would earn its keep **if and when** a second Puller appears (S3 bucket, embedded local file, in-process registry) or a second Store appears (Postgres, encrypted vault). Neither exists; the interface is the cost of two extra `type … interface` declarations paid for a benefit that doesn't yet exist.

The same critique applies to `Store` more sharply: `Store` has **zero implementations**, even a test one (the catalog tests use `nil` for both Puller and Store at `catalog_test.go:186-187`, `208-209`, `237`, `283`, `314-316` and similar). A nilable `Store` parameter with a nil-check inside `NewCatalog` is more honest than an interface declared for "future use."

**Fix.** Either (a) drop both interfaces, accept `*GHReleasePuller` + `*FileStore` (where `FileStore` is the single concrete `Store`), and revisit when a second consumer arrives; or (b) keep the interfaces but delete the unused `Store` interface entirely and document why `Puller` is kept (multi-implementation intent is honest). The single-implementation + nilable-Stub pattern is the right shape for a Wave 1 commit.

**Evidence.** `git diff d0e7374a..HEAD -- pkg/providers/capabilities/*.go` confirms `Puller` and `Store` are the only interface types declared; `grep -rn "capabilities.Puller\b" pkg cmd internal` returns 1 hit (the interface declaration); `grep -rn "capabilities.Store\b" pkg cmd internal` returns 1 hit (the interface declaration). No production implementor.

---

### MAJOR-02 — `pkg/media/resize` is a 203-LOC package wrapping ~60 lines of pure-function code that has no shared mutable state

The resize package has no constructor, no struct, no field, no global, no interface. Every symbol is a package-level function (`ResizeToFit`, `encodePNG`, `encodeJPEG`, `scaleImage`, `maxInt`, `shrinkOrFloor`) or a constant (`DefaultLongEdge`, `DefaultMaxBytes`, `MinLongEdge`, `shrinkFactor`, `qualityLevels`) or a single sentinel error (`ErrLadderFloor`). Of the 203 lines, ~70 are package-level doc-comment (lines 1-15, 28-32, 42-44, 47-48, 50-53, 55-56, 58-60, 63-67, 70-81, 142-144, 155-156, 165-168, 186-188). Of the ~133 lines of code, ~40 are test fixture / encode helpers and ~25 are the `for { … }` ladder loop.

The only production caller is `pkg/agent/loop_media.go::encodeImageToDataURL` (`loop_media.go:495-498`), which calls one function. There is no second caller in production. The "package" is effectively a single file with one exported function.

**Why a separate package is not warranted yet.** The ADR-051 Rev 4 Affected Components table (`ADR-051-rev4-…md:138`) calls for `pkg/media/resize` as a separate package. The spec lists it independently. But the architectural justification for separating the resize pipeline from the library package is **none yet**: there is no second consumer, no shared state, no interface boundary, no mock-for-test benefit (the function is pure and tests with synthetic `image.Image` values). The split exists because the ADR drew it on the diagram, not because the code demands it.

A `pkg/media/library/resize.go` containing `ResizeToFit` + helpers would:
- Eliminate one import line in `loop_media.go`.
- Keep the resize constants next to the manifest constants that share the same conceptual owner (the 7680/10MB default budget — see MAJOR-06 for the duplicate-constants finding).
- Make the resize call site `library.ResizeToFit(...)` instead of `resize.ResizeToFit(...)` — same import surface to other potential callers.

**Fix.** Move `ResizeToFit`, `encodePNG`, `encodeJPEG`, `scaleImage`, `shrinkOrFloor`, `maxInt`, `Budget`, `Result`, `ErrLadderFloor`, `DefaultLongEdge`, `DefaultMaxBytes`, `MinLongEdge`, `shrinkFactor`, `qualityLevels` to `pkg/media/library/resize.go`. Delete `pkg/media/resize/`. The `resize_test.go` (479 LOC) merges to `pkg/media/library/resize_test.go`.

**Evidence.** `git diff d0e7374a..HEAD --stat | grep resize`: `pkg/media/resize/resize.go 203` + `resize_test.go 479`. `grep -rn "resize\\.\\|media/resize" pkg cmd internal | grep -v "_test.go" | grep -v "/resize/"` returns 4 hits, all in `pkg/agent/loop_media.go` (one import, three uses).

---

### MAJOR-03 — Two parallel media-store abstractions ship in the same tree, neither migrated to the other

After this commit, the codebase contains **two** media stores:

| | Legacy (`pkg/media/store.go`) | New (`pkg/media/library/library.go`) |
|---|---|---|
| State | In-memory `map[string]mediaEntry` + `pathStates` refcount | On-disk `manifest.json` with `entries` + `refcounts` |
| Scope | Global, scope-keyed (`upload:<sid>`, `tool:inline:session:<sid>`) | Workspace-scoped (one Library per workspace) |
| Cleanup | `CleanExpired` TTL with `deleteEligible` path-state | `OrphanGCConfig` 30d default; quarantine-then-remove |
| Refcount semantics | Path-keyed, **immediate** delete when refcount==0 | Manifest-keyed, **deferred** orphan delete (30d) |
| Integrity | None (trusts the filesystem) | sha256 verified on every read |
| Persisted to | `~/.omnipus/media/registry.json` (global) | `~/.omnipus/workspaces/<id>/media/manifest.json` (per-workspace) |
| API surface | `MediaStore` interface (4 methods: Store/Resolve/ResolveWithMeta/ReleaseAll/RefByPath) | `Library` concrete type (13 methods: Upload/Read/ResolveWithWorkspace/IncrementRefcount/DecrementRefcount/Refcount/OrphanGC/Store/Load/List/Path) |
| Production callers | 22 sites (gateway, agent, tools) | **0** (only `library_test.go`) |

The spec line 67 (Execution Flows / Manifest refcount) acknowledges this explicitly: *"The workspace library maintains a SEPARATE manifest-level refcount, distinct from the existing path-based pathStates.refCount … The two counters have different semantics and do not collide."* The justification is "no collision." That's true at the data level — but at the **API level**, every consumer of `media.MediaStore` now has to reason about which store it should be talking to. There is no plan, in the visible codebase, for when (or whether) `pkg/media/store.go::FileMediaStore` becomes the agent-media-only store (its shrinking scope after B1 lands) versus being deleted entirely.

This is **over-layered architecture**: a Layer 0 / Layer 1 distinction that exists at the ADR level but is not enforced at the package level. The two stores are independently developed, with **the new one having zero production consumers** and the legacy one still on the request path. The migration glue (which call site switches from `MediaStore` to `Library`, when, and what the shim looks like for legacy `media://<uuid>` refs that need to resolve through `Library`) is **not in this commit**. The sunset clause in the ADR (line 128: *"legacy global-registry resolution is removed in v0.1.2"*) is concrete but **invisible in code** — the resolver shim mentioned in line 127 is also not in this commit.

**Fix.** Either (a) explicitly mark `pkg/media/store.go` as the agent-media-only store with a `// scope: agent-generated media only; user uploads go through pkg/media/library` godoc, and the gateway's `HandleUpload` calls `library.New(...)` for user uploads in the same Wave 1 commit (no backward-compat complexity if it's a fresh v0.1.1 build); or (b) keep the legacy store but delete the new `pkg/media/library` package and extend the legacy one with workspace-namespace + sha256 + manifest — a single-store path. Option (a) is closer to the ADR's intent; option (b) is closer to Occam's razor.

The current commit ships option (c) — **both stores, neither migrated**. That's the worst of both worlds: a future maintainer reading `media.MediaStore` everywhere and `library.Library` nowhere will not know which one to extend when the bug report arrives.

**Evidence.** `grep -rn "media.MediaStore" pkg cmd internal | wc -l` → 22 production sites (5 in gateway, 3 in agent, 14 in tools, mostly via the `MediaStoreAwareTool` interface in `pkg/tools/registry.go`). `grep -rn "library.New\|library.Library" pkg cmd internal | grep -v library/` → 0 hits. `grep -n "MediaStore\|Library" pkg/agent/loop_media.go` → only the legacy `media.MediaStore` parameter; no Library import.

---

### MAJOR-04 — `clonePointer[T any]` generic + five trivial pointer helpers (~30 LOC of `&value`-equivalent code)

`library.go:575-612` defines:

- `clonePointer[T any](value *T) *T` — generic; 7 lines.
- `uuidPointer`, `stringPointer`, `intPointer`, `int64Pointer`, `timePointer` — five one-liners, each taking a value and returning `&value`.

Total: 30 lines of code that does what Go's `&x` syntax already does. The five `*Pointer` helpers exist **only** to wrap a literal at construction sites — `library.go:205-212` (Upload) and `library.go:444, 481-482` (refcount changes) are the only call sites.

The underlying problem the helpers "solve": `gen.MediaLibraryEntry` has all fields as pointers (because OpenAPI generates `*string` / `*time.Time` / `*int` for `readOnly + optional` fields). Constructing the struct in Go requires wrapping every literal in `&` or a helper. `&x` works directly: `entry := gen.MediaLibraryEntry{Id: &id, Mime: &mime, ...}`. The five helpers were created so the call site looks like `Mime: stringPointer(mime)` instead of `Mime: &mime` — purely cosmetic.

The `clonePointer[T]` generic is used inside `cloneEntry` (`library.go:576-582`) to deep-copy every pointer field of the struct before returning. The deep copy is necessary because the in-memory `manifest map[string]gen.MediaLibraryEntry` stores values that callers may mutate; without the deep copy, a caller mutating a returned entry would corrupt internal state. **The deep copy is correct and necessary.** But it doesn't need a generic helper — it needs a single function `cloneEntry(entry gen.MediaLibraryEntry) gen.MediaLibraryEntry` that calls `&` directly on each field copy:

```go
func cloneEntry(e gen.MediaLibraryEntry) gen.MediaLibraryEntry {
    if e.Id != nil { v := *e.Id; e.Id = &v }
    if e.Mime != nil { v := *e.Mime; e.Mime = &v }
    // ... 5 more
    return e
}
```

That's 14 lines and uses no generics. The 7-line generic + 6-line `uuidPointer` family is over-engineered abstraction for what is inlined into a single function.

**Fix.** Delete the `clonePointer` generic and the five `*Pointer` helpers. Inline `&x` at construction sites. Replace `clonePointer(entry.X)` in `cloneEntry` with `if entry.X != nil { v := *entry.X; entry.X = &v }`. Net: -28 LOC, no behavior change.

**Evidence.** `library.go:586-612` (the helper block); call sites `library.go:205-212`, `:444`, `:481-482` (construction); `library.go:576-582` (cloneEntry's six generic calls).

---

### MAJOR-05 — Custom `logger` interface in `catalog.go` reimplements `*slog.Logger`

`catalog.go:154-160`:

```go
type logger interface {
    Warn(msg string, args ...any)
    Info(msg string, args ...any)
}
```

The signature exactly matches `*slog.Logger.Warn` / `Info`. The package then defines a `noopLogger{}` (lines 462-468) to satisfy the interface when `nil` is passed, and the test file uses `slog.New(slog.DiscardHandler)` (catalog_test.go:69) as the test logger — which **does** satisfy the interface (because the signatures match) but is the project-standard `*slog.Logger`.

The comment on line 155 says *"Defined as an interface so tests can capture log output without dragging in slog."* This is **factually wrong**: the test file already imports `log/slog` (catalog_test.go:38) and constructs an `*slog.Logger`. The interface definition is *the reason tests have to provide an `*slog.Logger`-shaped value* — without the interface, tests would pass an `*slog.Logger` directly.

The interface is also non-extensible: `logger.Warn(msg string, args ...any)` is a subset of `slog.Logger.Warn`. If the catalog ever needs to log structured context (`slog.String("agent_id", id)`), the interface signature accepts `...any`, so it would work — but **only because slog's signature happens to match**. A consumer using `slog.With(...)` gets back an `*slog.Logger` that satisfies the interface trivially.

The entire custom-interface machinery could be replaced with `log *slog.Logger` and a nilable check at each call site (5 sites), or with a single 5-line helper `c.logf("warn", "msg", args...)` that no-ops on `c.log == nil`. Total: ~13 LOC saved, no interface declaration, no `noopLogger` type.

**Fix.** Replace the `logger` interface with `log *slog.Logger`. Delete `noopLogger`. Wrap the 5 `c.logger.Warn(...)` calls in a method `c.warn(msg string, args ...any)` that no-ops on nil. The field is still optional — same API contract, less machinery.

**Evidence.** `catalog.go:151-160` (interface declaration); `catalog.go:462-468` (noopLogger); `catalog.go:419, 425, 432-433, 439-442` (the 5 call sites); `catalog_test.go:38-39, 69` (test imports slog).

---

### MAJOR-06 — `pkg/media/resize.DefaultLongEdge` / `DefaultMaxBytes` duplicate the catalog's default budget

`pkg/media/resize/resize.go:45-48`:

```go
const DefaultLongEdge = 7680
const DefaultMaxBytes = 10 * 1024 * 1024
```

`pkg/providers/capabilities/catalog.go:200-205` and `:254`:

```go
if s.DefaultResizeBudget.LongEdgePx <= 0 { … }
if s.DefaultResizeBudget.MaxBytes <= 0 { … }
// and in NewCatalog:
defaultBudget: ResizeBudget{LongEdgePx: 7680, MaxBytes: 10 * 1024 * 1024}
```

The same magic numbers appear **three** times:

1. `pkg/providers/capabilities/catalog.go:254` — `NewCatalog`'s initializer.
2. `pkg/providers/capabilities/data/providers_capabilities_seed.json:86` — `"default_resize_budget": {"long_edge_px": 7680, "max_bytes": 10485760}`.
3. `pkg/media/resize/resize.go:45, 48` — `DefaultLongEdge = 7680`, `DefaultMaxBytes = 10 * 1024 * 1024`.

The ADR's "Capability source" section says *"the catalog is the source of truth for 'which models accept which input modalities' used by the Layer-1 presentation orchestrator."* The catalog's `DefaultResizeBudget` is documented (catalog.go:55-67) as the per-model budget **with the catalog as the fallback**. The resize-package constants are a **third copy** of the same source of truth, and the only production call site (`loop_media.go:495-498`) hardcodes the resize-package constants rather than reading from the catalog.

Worse: the resize-package constants define a `Budget{LongEdge int, MaxBytes int}` shape with **`int`**, and the catalog defines `ResizeBudget{LongEdgePx int, MaxBytes int64}` with **different field names** (`LongEdge` vs `LongEdgePx`) and **different byte-size type** (`int` vs `int64`). The two shapes are semantically identical but textually incompatible — a future caller cannot pass a `capabilities.ResizeBudget` to `resize.ResizeToFit` without copying fields and converting types.

This is the inverse of MAJOR-02: it's not the package boundary that's wrong, it's the **constants boundary**. The resize-package constants should be **deleted** (the catalog is the source of truth), and `resize.ResizeToFit` should accept only a `resize.Budget` populated by the caller. The catalog's `Budget{LongEdgePx, MaxBytes}` and the resize package's `Budget{LongEdge, MaxBytes}` should converge on one shape.

**Fix.** Either (a) delete `DefaultLongEdge` and `DefaultMaxBytes` from the resize package; have the only call site pass a literal `Budget{LongEdge: 7680, MaxBytes: 10 << 20}` until the catalog wiring lands; or (b) converge on one struct shape (`Budget{LongEdgePx int, MaxBytes int64}` is the catalog's shape; use that and rename `LongEdge` → `LongEdgePx`). The catalog is the source of truth per the ADR; the resize package should not carry its own copy.

**Evidence.** `resize.go:42-48`, `catalog.go:200-205, 254`, `data/providers_capabilities_seed.json:86`, `loop_media.go:495-498`. Confirmed at all four sites via `grep -n "7680\|10485760\|10 \* 1024 \* 1024\|10 << 20"`.

---

### MAJOR-07 — `imageStripRange` variadic option on `stripRejectedImageMedia` has zero callers

`media_downgrade.go:227-274`:

```go
func stripRejectedImageMedia(messages []providers.Message, opts ...imageStripRange) bool {
    from, to := 0, len(messages)
    if len(opts) > 0 {
        from, to = opts[0].from, opts[0].to
    }
    from = max(from, 0)
    to = min(to, len(messages))
    if from >= to { return false }
    // … strip loop over [from, to) …
}

type imageStripRange struct{ from, to int }
```

The only caller is `media_downgrade.go:130` (`stripRejectedImageMedia(callMessages)` — **no opts**). The variadic range is dead API surface. The "Pass-2 fix" comment on line 219 references a future caller that doesn't exist; the spec (US-7 / AC1) describes a single-pass all-messages behavior.

This is **17 lines of unused machinery** (the struct type + the variadic unpack + the `max(from, 0)` / `min(to, len(messages))` clamping + the `from >= to` early-return + the doc comment). The function would be 7 lines shorter as `func stripRejectedImageMedia(messages []providers.Message) bool` with the loop iterating the full slice.

**Fix.** Delete the `opts ...imageStripRange` parameter, delete the `imageStripRange` struct, delete the `from/to` clamping, restore the loop to iterate `range messages`. If a future caller needs range scoping, add the parameter back then.

**Evidence.** `grep -rn "imageStripRange\|stripRejectedImageMedia(" pkg cmd internal` returns 2 hits — the struct definition and the only call site (which passes nothing). `grep -n "from, to :=" pkg/agent/media_downgrade.go` confirms the unused unpack.

---

### MAJOR-08 — `startsWithCaseInsensitive` micro-optimization on a non-hot path

`media_downgrade.go:277-294` (18 lines) implements a manual case-insensitive prefix match. The comment (lines 276-278) justifies it as avoiding `strings.ToLower` allocation on a "hot path." The path is **not hot** — `TryMediaDowngrade` runs **at most once per LLM 4xx response**, gated by per-class `atomic.Bool` guards (`mediaRetryDone` / `imageRetryDone`). A turn that hits 3 LLM 4xx responses in a row fires `stripRejectedImageMedia` once per class guard; that's one to two calls per turn maximum.

The "hot path" framing is wrong; the optimization saves an `O(11)` allocation of `strings.ToLower("data:image/")` on a code path that runs at most 2 times per turn. The standard library's `strings.HasPrefix(strings.ToLower(ref), imgPrefix)` is 1 line and the cost is unmeasurable.

The same `startsWithCaseInsensitive` helper is also called from `loop.go:6871` — the helper was added to `media_downgrade.go` but its function shape duplicates what `strings.HasPrefix` provides.

**Fix.** Delete `startsWithCaseInsensitive`. Replace `if !startsWithCaseInsensitive(ref, imgPrefix)` with `if !strings.HasPrefix(ref, imgPrefix)` (the prefix `"data:image/"` is already lowercase ASCII; the case-insensitivity claim was unjustified). The `loop.go:6871` site already uses `strings.HasPrefix` correctly; align the other site.

**Evidence.** `media_downgrade.go:277-294` (definition); `media_downgrade.go:251` (call site); `loop.go:6871` (existing `strings.HasPrefix` call, the canonical pattern).

---

### MAJOR-09 — `pkg/media/library/library.go::validatePersistedEntry` defensive layer duplicates type-system invariants

`library.go:517-547` (31 lines) re-implements invariants that the type system and `json.Decoder.DisallowUnknownFields` already enforce:

- `entry.Id` non-nil and equals parsed UUID — enforced by the `uuid.Pointer(*Id)` generation contract + the `Validate:` line in the OpenAPI schema (`uuid` + `format: uuid`).
- `entry.WorkspaceId` matches `l.workspaceID` — enforced by the resolver's `callerWorkspaceID == workspaceID` check at `ResolveWithWorkspace:294-301`.
- `entry.Mime` non-empty — enforced by `normalizeFilename` semantics + the `description:` line in the OpenAPI schema.
- `entry.Size` non-negative — enforced by Go's `int64` type (no negatives in JSON without custom UnmarshalJSON).
- `entry.Sha256` valid hex digest — partially type-system-enforced by `string`, partially a runtime invariant that **does** need the explicit `validDigest` check (sha256 hex format is not type-encoded). This check is genuinely load-bearing.
- `entry.UploadedAt` non-zero — enforced by the upload path (`uploadedAt := l.now().UTC()`, never zero).
- `entry.Source` is `gen.UserUpload` or `gen.TestFixture` — enforced by the `Upload` function's source check at `library.go:133`.
- `entry.Refcount` non-negative — partially type-system-enforced, partially worth checking at the JSON-decoder boundary.

Of the 31 lines of `validatePersistedEntry`, **at least 12 lines** (the UUID parsing, workspace match, Mime non-empty, Size range, Source enum) re-check what the type system + `Upload()` already guarantee. The sha256 check and the Refcount non-negative check are load-bearing (the JSON-decoder boundary does not enforce them). The other checks are defensive layering.

This is **defensive layering that costs maintenance**: any future change to `gen.MediaLibraryEntry` (e.g. adding a new required field) requires updating both `Upload()` and `validatePersistedEntry()`. The Upload function is the single source of truth for valid state; validatePersistedEntry is a paranoid mirror.

**Fix.** Keep `validatePersistedEntry` but reduce it to the **load-bearing checks** only:
- `entry.Sha256` is valid hex (`validDigest(*entry.Sha256)`) — runtime invariant.
- `count >= 0` — runtime invariant.
- `*entry.Refcount == count` (manifest/Refcounts dual-write consistency) — runtime invariant.

Delete the UUID/workspace/Mime/Size/Source/UploadedAt checks: those are invariants of the upload path, and a persisted manifest was produced by an upload. Re-validating them on read is duplication.

**Evidence.** `library.go:517-547`. The OpenAPI schema `contracts/components/schemas/MediaLibraryEntry.yaml` (modified in this commit per the diff stat) is the source of truth for the type system contract.

---

### MINOR-01 — `maxInt` duplicated in both `pkg/media/resize` and `pkg/agent/loop_media`

`resize.go:179-184` and `loop_media.go:518-523` both define:

```go
func maxInt(a, b int) int {
    if a > b { return a }
    return b
}
```

The project's go.mod requires Go 1.26.4 (per `CLAUDE.md`). Go 1.21+ ships the `max()` builtin. Both definitions can be deleted in favor of the builtin.

**Fix.** Replace all 4 call sites (`resize.go:97, 198`, `loop_media.go:503`) with `max(a, b)`. Delete both `maxInt` definitions.

---

### MINOR-02 — `pdfCapableModel` substring allow-list duplicates the capability catalog's role

`loop_media.go:181-222` maintains a hardcoded list of `claude-3.5-sonnet`, `gpt-4o`, `gemini-1.5`, `claude-3-opus` etc. with a deny-list for "haiku". The capability catalog seed (`providers_capabilities_seed.json`) lists the **same models** with `input_modalities: ["text", "image", "pdf"]` (the `claude-*`, `gpt-4*`, `gemini-*`, `mistral-*` rows). When the catalog is wired in (Wave 3), one of these lists must die — and per the ADR (line 165 "operator decision: Capability overrides per agent/workspace — global seed only") and the spec (US-7 / FR-010 "System MUST gate media send on the capability registry at step 1 — if the model lacks the image/pdf modality, skip native send"), the catalog wins.

**Fix.** Add a `// TODO(wave-3): delete when catalog wiring lands; replaced by capabilities.Catalog.HasModal(model, "pdf").` to `pdfCapableModel`. Better: extract the deny-list ("haiku") and the catalog-seeded allow-list into a comment that explains the contract.

---

### MINOR-03 — `defaultUserMessage` and `UserMessageForCode` are the same function under two names

`translate_error.go:116-129` defines:

```go
func defaultUserMessage(code LLMErrorCode) string { … }  // line 116, package-private
func UserMessageForCode(code LLMErrorCode) string {       // line 127, exported
    return defaultUserMessage(code)
}
```

The exported function exists "for tests (and any external caller that needs to look up the generic copy without running the full classifier)" (line 125). This is the textbook "export for testability" anti-pattern — the same function with two names because one happens to be capitalized. The doc comment justifying the duplication is the exact pattern Constraint #6 (no default-policy fallback) and standard Go style would collapse.

**Fix.** Rename `defaultUserMessage` → `UserMessageForCode` (it's already the canonical name) and delete the lowercase wrapper. Internal callers change to use the exported name (no behavior change).

---

### MINOR-04 — `errorToProviderError` / `providerErrorFromChain` / `ProviderErrorFromFailover` — three conversion functions with overlapping responsibility

`translate_error.go:600-740` defines three functions that all return `*ProviderError`:

- `ProviderErrorFromFailover(fe *providers.FailoverError) *ProviderError` (line 600) — walks `fe.Wrapped`, returns a pe with Status+Body+Err.
- `providerErrorFromChain(err error) *ProviderError` (line 714) — walks an `err` chain for `*providers.ProviderError` (preferred) then `*providers.FailoverError`, returns pe or nil.
- `errorToProviderError(err error) *ProviderError` (line 661) — multi-unwrap + single-error dispatch; calls `providerErrorFromChain` per attempt.

Three functions, each with its own doc comment explaining the priority order, and each constructing `&ProviderError{Status: ..., Body: ..., Err: ...}` with subtly different field sources. The split exists because each function's doc comment is long enough to make a single function unwieldy — but the three-function chain is reading-cost overhead for the maintainer.

**Fix.** Merge into one function `extractProviderError(err error) *ProviderError` with the multi-unwrap / single-error / FailoverError fallback as branches. Delete `providerErrorFromChain` and `ProviderErrorFromFailover`. ~80 LOC → ~40 LOC. The behavior is unchanged; only the API surface shrinks.

---

### MINOR-05 — `MediaCleanerConfig` and `OrphanGCConfig` are the same shape under two names

`pkg/media/store.go:85` defines `MediaCleanerConfig{Enabled bool, MaxAge time.Duration, Interval time.Duration}` (3 fields). `pkg/media/library/library.go:63` defines `OrphanGCConfig{Enabled bool, MaxAge time.Duration}` (2 fields, no Interval). Both exist in the `pkg/media` parent.

The shape similarity (Enabled + MaxAge, both about media lifecycle cleanup) is real but not identical (legacy is interval-driven for periodic TTL, new is on-demand orphan-GC). A maintainer seeing both will assume they're related (and that the new one replaces the old) when they're actually different lifecycle strategies.

**Fix.** Either (a) accept the duplication as a deliberate marker that legacy and new stores are independent (add a `// standalone; not related to MediaCleanerConfig` comment to `OrphanGCConfig`); or (b) rename `OrphanGCConfig` → `LibraryGCConfig` to make the scope difference explicit. (a) is the lighter fix.

---

### MINOR-06 — Doc-comment preambles narrate the Wave 1 fix-pass history

`translate_error.go:1-13`, `media_downgrade.go:1-9`, `loop_media.go:393-417` open with multi-line "Wave 1 fix pass" or "FR-016 (ADR-051 Rev 4): the prior D2 passthrough was …" comments that document what changed in this commit. These are valuable during review but become stale once Wave 1 ships. `git blame` and ADR-051-rev4 are the durable sources of "why was this written."

The project convention (per `CLAUDE.md` "DO NOT ADD ***ANY*** COMMENTS unless asked") is to minimize narrative comments. The Wave 1 fix-pass comments violate this for new code.

**Fix.** Trim each comment to 2-3 lines of load-bearing WHY (the spec FR/ADR reference) and drop the historical narrative. The deleted lines are recoverable from `git log`.

---

### MINOR-07 — `providerErrorFromChain` and `ProviderErrorFromFailover` are both reachable via `errorToProviderError`, but only the exported one is used externally

After MINOR-04 fix, `providerErrorFromChain` and `ProviderErrorFromFailover` become unexported helpers inside one function — fine. But as-shipped, `ProviderErrorFromFailover` is exported (line 600) with no external caller. `grep -rn "ProviderErrorFromFailover" pkg cmd internal` returns 2 hits: the definition and the internal call from `providerErrorFromChain:738`. The exported name signals "external caller exists" when none does.

**Fix.** Lowercase `ProviderErrorFromFailover` → `providerErrorFromFailover` once it has no external caller.

---

### MINOR-08 — `image/png` import dropped from `pkg/agent/loop_media.go` correctly, but `encodePNG` in `pkg/media/resize` does the same job

The diff removes `image/png` from `loop_media.go`'s imports because the PNG-encoding logic moved to `pkg/media/resize/encodePNG`. This is correct (no dead imports), but worth noting: `resize.encodePNG` is functionally identical to the prior inline `png.Encode` call in `loop_media.go` (which previously did `png.Encode(buf, img)` directly). The split is clean but the only consumer of `png.Encode` is now the resize package.

This is a **MINOR because the split is correct**, but the comment "encodePNG scales src to width×height using the Catmull-Rom kernel and encodes the result as PNG" (`resize.go:142-144`) is doing more than `png.Encode` did in the prior version (the prior version didn't scale — it just encoded). The scale step was new in this commit and is the actual load-bearing logic. The doc comment is fine.

---

### OBS-01 — `pkg/providers/capabilities/catalog.go` package doc references a file that doesn't exist

`catalog.go:6-8`: *"`pkg/agent/media_present.go`, Wave 3"* — the Wave 3 orchestrator. The file does not exist in the tree. This is the same speculative-framing pattern as the `Puller` / `Store` interfaces (MAJOR-01): the doc comment describes a future consumer that constrains the package's API, but the consumer isn't in this commit.

Not a finding per se; an observation that the doc comment is forward-referenced to a not-yet-written file.

---

### OBS-02 — Test-file preamble inventories are over-specified

`catalog_test.go:1-32` (32 lines, 21 numbered tests) and `puller_test.go:1-20` (20 lines, 10 numbered tests) follow the project convention of a numbered test inventory as the file header. The convention is acknowledged (round-2 code-simplifier) but the inventories are heavy: 30-line preamble indexing 580 LOC of tests.

A 5-line preamble (the package name, the FR/ADR reference, the data flow overview) would carry the load-bearing signal. The full inventory can live in a `// Test inventory:` comment near the top of each `func Test…` block, or in the godoc of the package being tested.

---

### OBS-03 — `noopLogger{}` empty methods vs `slog.DiscardHandler`

`catalog.go:462-468` defines a local `noopLogger{}` with empty `Warn` and `Info` methods. Go 1.24+ ships `slog.DiscardHandler` (which the test file already uses at `catalog_test.go:69`). The catalog could accept `log *slog.Logger` and use `slog.New(slog.DiscardHandler)` as the nil-default. This is the same fix as MAJOR-05.

---

### OBS-04 — `validatePersistedEntry` checks `entry.Source != gen.UserUpload && entry.Source != gen.TestFixture`

`library.go:537-539` — the check enforces that **only** the two enum values `user_upload` and `test_fixture` are valid in storage. This is **correct** (the wire schema restricts `source` to those two values, per the round-1 CS-08 carry-forward observation that `test_fixture` is a deliberate testability provision). Just confirming the validation matches the wire schema; no fix.

---

### OBS-05 — `imageStripRange` carries the same naming pattern as `MediaCleanerConfig` / `OrphanGCConfig`

Two cleanups (MAJOR-07 + MINOR-05) both delete "config-shaped but unused" helpers. There's a pattern here: the Wave 1 commit is permissive about adding small structs/options "just in case." Worth flagging as a project-wide code-simplifier concern, not just a Wave 1 finding.

---

## Rejected candidates (not findings)

- **Splitting `pkg/media/library` and `pkg/media/resize` into two packages** — partially raised in MAJOR-02. The argument for the split (resize has zero state, could live anywhere) is genuine, but if the catalog's wiring is going to grow to call `ResizeToFit` directly (Wave 3 orchestrator), the resize package may earn its keep as a stable import surface for the orchestrator. **The split is a soft no, the constant duplication (MAJOR-06) is a hard yes.**
- **`Capability source — global seed only` operator decision** — locked in the ADR. The package enforcing it (no per-agent/per-workspace override) is correct. Not an over-engineering concern.
- **7-step presentation chain** — locked in the ADR. The 7-step chain is what the catalog's per-model modality gates; the chain is the spec, not over-engineering.
- **Two retries per turn (`mediaRetryDone` / `imageRetryDone`)** — the per-class independence (FR-019) is the load-bearing design choice. Not over-engineering; it's the correct granularity for a PDF-only-rejection-then-image-rejection scenario.
- **`outcomeRelabel` field on `turnState`** — the FR-017a success-edge relabel is a one-line field with two getter/setter methods. Small, load-bearing, not over-engineered.
- **Multi-language test preambles** (`// ── Test #1 ────…`) — project convention. Carries grep affordance.
- **`Puller.Pull(ctx)` returning `([]byte, error)`** vs `(io.ReadCloser, error)` — `[]byte` is bounded (2 MB cap from puller.go:188, `2<<20` cap on the asset body). Returning `[]byte` is correct because the entire catalog is <50KB in practice; streaming would be over-engineered.
- **`-tags goolm,stdjson` build constraints** — already covered by `CLAUDE.md` and out of scope for this review.
- **`spec.go` and `seed-validate` split** — capability catalog uses `ParseSeed()` + `Seed.validate()` as two-phase (parse → validate). The split is a deliberate "validate uses the parsed structure" pattern; merging would not save meaningful LOC.

---

## Pre-existing conditions noted (not introduced by Wave 1)

- **`pkg/providers/catalog` (legacy) and `pkg/providers/capabilities` (new) coexist** in the same parent (`pkg/providers/`). The legacy package is the company × plan × region × wire catalog (per `catalog.go:2-8`'s own doc comment); the new one is the model-modality catalog. The two are intentionally orthogonal (per the new doc comment). The naming overlap (`catalog` vs `capabilities`) is the only friction. **Not a Wave 1 finding** — the legacy package was already there.
- **`media.MediaStore` interface in `pkg/media/store.go:47-70`** has 4 methods (Store, Resolve, ResolveWithMeta, ReleaseAll, RefByPath). The new `Library` type has 13 methods. The API asymmetry is real but **not a Wave 1 finding** — the Library is a concrete type, not an interface, and adding an interface would be premature abstraction on top of MAJOR-03.
- **`providerErrorFromChain` constructs a synthetic pe with `Status: 0, Body: "", Err: err`** when no structured error is reachable (`translate_error.go:697-701`). The synthetic pe carries only the message; the classifier substring-matches on `Err.Error()`. This is correct fallback behavior; not over-engineering.

---

## What the Wave 1 reviewer gate should NOT re-litigate

- The Slice A/B1/C/E/F commit split (per the round-1 holistic directive) is correct. Wave 1 reviewer can refactor within a slice but should not re-merge slices.
- The schema hardening choices (uuid, maxLength, source enum, readOnly) are the round-1 M1–M10 carry-forward. Wave 1 does not re-open the wire shape.
- The presentation orchestrator is **Wave 3** per the ADR. The catalog package is storage scaffolding; the orchestrator's call sites will land later. Wave 1 should not pre-implement the orchestrator.
- The classifier substring lists (`pdfRejectionSubstrings`, `imageRejectionSubstrings`, `capabilityAbsenceSubstrings`, `toolArgsSubstrings`, `schemaSubstrings`, etc. in `translate_error.go:131-318`) are the canonical pinned-substring set per the ADR's dataset row #3 (xAI/Grok incident) and FR-018 (tool-args / schema backstop). They are **data**, not over-engineering. The number of substrings (~30 total across all categories) is appropriate for the canonical-event coverage the spec requires.
- The `Loop(turn.go)` test fixtures and the round-1 F-L8-2 backstop (the outcome-based retry must NOT mis-label tool-args/schema rejections) are correct and well-tested.

---

## Verdict

**REVISE — 0 CRIT / 9 MAJOR / 8 MINOR / 5 OBS.**

The four Slice commits deliver real value (sha256-on-read, manifest persistence, resize-to-fit, outcome-based retry). The complexity that creeps in is not in the load-bearing logic — it's in the **peripheral machinery**: defensive validation layers, dual media-store abstractions, duplicated default constants, custom logger interfaces, unused variadic options, micro-optimized helpers on non-hot paths, multi-function error-chain converters.

**None of the MAJORs is a correctness bug**; all are over-engineering, premature abstraction, or speculative generality. Every MAJOR has a concrete simplification that preserves behavior:

| ID | Scope | Fix size |
|---|---|---|
| MAJOR-01 | `pkg/providers/capabilities` interfaces | Drop `Store` interface; keep `Puller` for honest intent |
| MAJOR-02 | `pkg/media/resize` package boundary | Move to `pkg/media/library/resize.go` |
| MAJOR-03 | Dual media stores | Pick (a) migrate `HandleUpload` to `Library` or (b) delete `Library` and extend legacy; current "both, neither migrated" is worst |
| MAJOR-04 | `clonePointer` + 5 trivial `*Pointer` helpers | Delete generics; inline `&x` |
| MAJOR-05 | Custom `logger` interface | Replace with `*slog.Logger` + nilable warn helper |
| MAJOR-06 | Duplicated default constants | Delete `resize.DefaultLongEdge`/`DefaultMaxBytes`; catalog is source of truth |
| MAJOR-07 | `imageStripRange` unused variadic | Delete; restore full-slice signature |
| MAJOR-08 | `startsWithCaseInsensitive` micro-opt | Replace with `strings.HasPrefix` |
| MAJOR-09 | Defensive `validatePersistedEntry` layer | Trim to load-bearing checks (sha256, refcount) |

**Recommend:** Apply MAJOR-01 through MAJOR-06 in a single follow-up commit (`chore(adr-051-rev4): wave-1 simplification pass`) before pushing to `main`. MAJOR-07, MAJOR-08, MAJOR-09 can ride as a second commit or fold into the same one. The 8 MINORs are mostly cosmetic and can ride into Wave 2/Wave 3 or be filed as Wave 1 follow-ups. The 5 OBSs are noted but do not block.

The slice's correctness posture (per the pr-test-analyzer, silent-failure-hunter, and type-design reports) is **not** in scope for this review and is presumed clean. This review is solely on **shape**: whether the abstractions, layers, and configuration surfaces are proportionate to the load-bearing logic.

*End of review.*
