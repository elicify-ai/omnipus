# Round-8 Verdict (2026-06-13) — PASS (GATE C GRANTED)

Re-review after the round-7 REVISE, scoped (per brief) to the sole remaining defect **R7-F1** —
the BDD scenario at L80/L84 still asserting the reverted "removed/excludes `config_old.go`"
disposition — and to confirming that **all `config_old.go` disposition locations now agree**
(migrate/rewrite, NOT delete/exclude). Grounded against the live tree:
`pkg/config/config_old.go:113,605,734,759`, `pkg/config/config.go:1784-1785`,
`pkg/config/migration.go:34,37,484,493`.

**Verdict: PASS. GATE C GRANTED.**

## R7-F1 — CLOSED (sound)

The BDD scenario is fixed exactly as the round-7 fix prescribed, on all three points:

1. **Retitled (L80):** `Scenario: Typed-field-access guard is clean (excludes only openclaw)` —
   the "+ config_old" clause is gone; the parenthetical "(excludes only openclaw)" is semantically
   identical to the recommended "and excludes only openclaw".
2. **Given now marks config_old as migrated (L83):** `Given the migrated code (typed fields
   deleted; config_old.go's ToChannelsConfig migrated to emit the map)` — config_old is explicitly
   migrated, not removed.
3. **When excludes ONLY openclaw (L84):** `When the stdlib go/types AST guard scans pkg/+cmd/ for
   selectors resolving to config.ChannelsConfig, excluding only pkg/migrate/sources/openclaw` —
   the "and the removed config_old.go" clause is dropped entirely; config_old is subject to the
   guard and passes by being migrated (0 selectors post-migration), like every other in-scope reader.

## All config_old.go disposition locations agree (7, not 6)

| # | Location | Disposition stated | Agrees? |
|---|----------|--------------------|---------|
| 1 | §1 In-scope (L13) | listed among "the extra readers …`config_old.go`" to migrate | ✓ migrate |
| 2 | §2 Symbols (L24) | "`config_old.go` \| **migrate** to the map (rewrite, NOT exclude)" | ✓ migrate |
| 3 | BDD (L80/L83/L84) | excludes ONLY openclaw; config_old "migrated to emit the map" | ✓ migrate |
| 4 | FR-2.2 (L154) | "LIVE v0→v1 migration module … **KEEP it; rewrite only `ToChannelsConfig`, do NOT delete**" | ✓ keep+rewrite |
| 5 | SC-4 (L164) | "`config_old.go`'s `ToChannelsConfig` is migrated to emit the map — counted as migrated, not excluded" | ✓ migrate |
| 6 | Assumption 5 (L203) | "(live v0→v1 migration) is **kept** — only its `ToChannelsConfig` is rewritten … Exclusion = guard-only" | ✓ keep+rewrite |
| 7 | H6 (L196) | "0 typed access outside `migrate/sources/openclaw`" — openclaw-only exclusion, no config_old | ✓ consistent |

A full-text sweep for any residual `config_old`-DELETE or `config_old`-EXCLUDE clause returns none:
every "delete" hit refers to the **typed fields** (the migration mechanism) or to the explicit
"do NOT delete" / "deleting it would break config.go/migration.go" guidance; every "exclude" hit
names **openclaw only**.

## Go-build oracle now internally consistent (live-tree confirmed)

The KEEP disposition is the only one under which SC-4's **primary** `go build` oracle is achievable,
and it is grounded:
- `config_old.go:113` `ToChannelsConfig()` (the converter to rewrite) is called at `:759` by the
  live `Migrate()`/`MigrateWithStore()` path.
- `config.go:1784-1785` live-consume `(*configV0).MigrateWithStore(store)` + `.hasLegacySecrets()`.
- `migration.go:37,484,493` live-consume `configV0` / `v0ConvertProvidersToModelList(*configV0)`.

So `config_old.go` is genuinely live; deleting it would break `CGO_ENABLED=0 go build -tags
goolm,stdjson ./...`. KEEP-and-rewrite-`ToChannelsConfig` keeps every producer compiling while
carrying 0 typed `config.ChannelsConfig` selectors post-migration, satisfying both the primary
build gate (SC-4) and the secondary stdlib AST guard (test #8) with the openclaw-only exclusion.

## Decision

**GATE C GRANTED — PASS.** R7-F1 is closed: the BDD scenario no longer asserts the reverted
removed/excluded disposition; it is retitled to "excludes only openclaw", its Given marks
config_old's `ToChannelsConfig` as migrated, and its When excludes only `pkg/migrate/sources/openclaw`.
All seven disposition locations (the six the brief named plus H6) agree on migrate/keep-and-rewrite,
and the go-build oracle is internally consistent and live-tree-achievable. No CRITICAL or MAJOR
findings remain. The spec is ready for task decomposition.

To proceed, run:

```
/taskify docs/internal/specs/v01-spec2-connections-instance-email-spec.md
```

---

# Round-7 Verdict (2026-06-13) — REVISE (GATE C NOT granted)

Re-review after the round-6 REVISE, scoped (per brief) to confirming R6-F1 — `config_old.go`'s
contradictory disposition — is genuinely closed: i.e. the file is now consistently "KEEP + rewrite
only `ToChannelsConfig:113` to emit the map" *everywhere*, and the `go build` oracle is achievable
(every producer compiles against the map). Grounded against the live tree: `pkg/config/config_old.go:64,97,113,759`,
`pkg/config/config.go:1784`, `pkg/config/migration.go:37,484`.

**Verdict: REVISE. GATE C NOT granted.** The round-6 fix landed in **five** of the six locations —
§1 In-scope (L13), §2 Symbols (L24, "migrate … NOT exclude"), FR-2.2 (L154, "KEEP it; rewrite only
`ToChannelsConfig`, do NOT delete"), SC-4 (L164, "counted as migrated, not excluded"), and Assumption 5
(L203, "kept … only its `ToChannelsConfig` is rewritten … Exclusion = guard-only"). The codebase facts
that justify KEEP are confirmed: `config_old.go` is the live v0→v1 migration module —
`channelsConfigV0` (L97) → `ToChannelsConfig` (L113) feeds `cfg.Channels` (L759), reached via
`configV0.Migrate*` (config.go:1784); deleting it would break `go build`. So far so consistent.

**But the BDD scenario at L80/L84 was missed by the round-6 sweep and still states the *deleted*
disposition** — making the spec internally contradictory exactly where the brief asked me to certify
consistency. The go-build oracle is *individually* achievable for `config_old.go` under the KEEP
disposition, but the spec does not yet *say so consistently*, so GATE C cannot be granted.

## R7-F1 — `config_old.go` disposition STILL contradictory in the BDD scenario (L80, L84)

| ID | Sev | Lens | Finding | Recommended fix |
|---|---|---|---|---|
| **R7-F1** | MAJOR | Inconsistency | The BDD scenario "Typed-field-access guard is clean…" (L80) is titled "**excludes openclaw + config_old**", and its `When` step (L84) reads "excluding `pkg/migrate/sources/openclaw` and **the removed `config_old.go`**". Both clauses assert the round-5 (now-reverted) disposition: that `config_old.go` is (a) **removed** and (b) **excluded from the guard**. This directly contradicts FR-2.2/SC-4/Assumption 5, which now say `config_old.go` is **KEPT** and its `ToChannelsConfig` **migrated to emit the map**, therefore "**counted as migrated, not excluded**" (SC-4, verbatim) and "Exclusion = guard-only" applies to **openclaw only** (Assumption 5). Under the corrected disposition, `config_old.go` has **zero** typed `config.ChannelsConfig` selectors after migration, so it must **not** be excluded — it is subject to the guard and passes by being migrated, identical to every other in-scope reader. | Rewrite L80 to: `Scenario: Typed-field-access guard is clean and excludes only openclaw`. Rewrite L84 to: `When the guard … excluding only pkg/migrate/sources/openclaw (its own OpenClawChannels type)` — drop "and the removed config_old.go" entirely. `config_old.go` is migrated, not excluded; it carries 0 selectors post-migration and so satisfies the guard like any migrated reader. |

After this single edit, all six locations agree on KEEP + rewrite-`ToChannelsConfig` + subject-to-guard,
and the go-build oracle is internally consistent. This is a one-line-pair reword (no codebase risk),
hence MAJOR/REVISE — not BLOCK.

**Next action:**
```
/plan-spec --revise docs/internal/specs/v01-spec2-connections-instance-email-spec.md docs/internal/specs/v01-spec2-connections-instance-email-spec-review.md
```

---

# Round-6 Verdict (2026-06-13) — REVISE (GATE C NOT granted)

Re-review after the round-5 REVISE, scoped (per brief) to confirming the two round-5 findings are
genuinely closed against the live tree, with special attention to (a) whether the named producers
are *all* of them, and (b) whether the `go build`-after-deletion oracle is now internally consistent
with the exclusion list. Grounded against the live tree: `pkg/config/config.go:774,1784`,
`pkg/config/config_old.go`, `pkg/config/migration.go`, `pkg/config/defaults.go:78`,
`pkg/channels/manager_channel.go:108`, `pkg/migrate/sources/openclaw/openclaw_config.go:757,965,990`,
`go.mod`/`go.sum`.

**Verdict: REVISE. GATE C NOT granted.** R5-F2 is genuinely closed. R5-F1's *producer enumeration*
is correct as far as it goes — but the round-5 fix introduced a **new internal contradiction** for
`config_old.go`, and its chosen disposition (DELETE) makes SC-4's primary `go build` oracle
**unachievable**. This is precisely the "go-build oracle internally consistent with the exclusions?"
question the brief asked me to verify — and for `config_old.go` the answer is no. Concrete and
mechanically fixable (one disposition decision + reword), hence REVISE, not BLOCK.

## R5-F2 — CLOSED (sound)

SC-4 secondary (L164) and test #8 (L143) now specify the guard uses **stdlib
`go/parser`+`go/types`+`go/importer` (NOT `golang.org/x/tools`)**. Verified against `go.mod`
(0 `golang.org/x/tools` require lines) and `go.sum` (only a built-module hash line, no direct/indirect
require). The stdlib trio is sufficient for a single-package type-resolving AST pass, so no new dep is
pulled and Constraint #1 holds. **Closed.**

## R5-F1 — producer set CORRECT, but disposition for `config_old.go` is self-contradictory and infeasible (NEW: R6-F1)

The composite-literal PRODUCER census is now confirmed complete and correctly named. A module-wide
grep for `config.ChannelsConfig{` / `&config.ChannelsConfig{` / bare `ChannelsConfig{` in
`pkg/`+`cmd/` (minus `_test.go`) returns exactly the four real-`config.ChannelsConfig` producers the
spec scopes — `defaults.go:78`, `manager_channel.go:108`, `openclaw_config.go:990`,
`config_old.go:114` — plus `openclaw_config.go:757` (`channels := ChannelsConfig{}`), which is the
openclaw package's **own local** `ChannelsConfig` type (defined `openclaw_config.go:620`), correctly
out of scope. `ToStandardChannels` is live (called at `openclaw_config.go:965`); the "rewrite to emit
the map" disposition is feasible. **The enumeration is sound.**

But the spec now says two contradictory things about `config_old.go`, and the one the round-5 review
endorsed (DELETE) breaks the very `go build` oracle this round must certify:

| ID | Sev | Lens | Finding | Recommended fix |
|----|-----|------|---------|-----------------|
| **R6-F1** | **MAJOR** | Inconsistency / Infeasibility / Incompleteness | **The spec gives `config_old.go` two mutually-exclusive dispositions, and the DELETE one makes SC-4's primary `go build` gate unachievable.** §1 In-scope (L13) and §2 Symbols (L24) list `config_old.go` under **"migrate to the map (rewrite, NOT explude)"**. FR-2.2 (L154) and Assumption 5 (L203) instead say **"`pkg/config/config_old.go` is DELETED (legacy/greenfield)."** These contradict. Worse, DELETE is **infeasible**: `config_old.go` is **not** dead legacy — it is the live v0→v1 config-migration module. `configV0` and its methods are consumed by non-test live code: `pkg/config/config.go:1784-1785` (`v.(*configV0).MigrateWithStore(store)`, `.hasLegacySecrets()`) and `pkg/config/migration.go:37,484` (`v0ConvertProvidersToModelList(cfg *configV0)`, `var v0 configV0`). `MigrateWithStore` (`config_old.go:605`, the live dispatch target) calls `cfg.Channels = c.Channels.ToChannelsConfig()` at `config_old.go:759`. Deleting the file removes `configV0`, `MigrateWithStore`, `hasLegacySecrets`, the parameter type of `v0ConvertProvidersToModelList`, and ~15 V0 sub-types — leaving `config.go` and `migration.go` referencing undefined symbols, so `CGO_ENABLED=0 go build -tags goolm,stdjson ./...` (SC-4 **primary** gate, L164) **fails to compile**. So the go-build oracle is *still* internally inconsistent with the spec's exclusion/deletion list — the exact failure mode this round was asked to confirm is closed. (It is closed for the four producers; it is **re-opened** by the `config_old.go` DELETE.) | Pick ONE disposition for `config_old.go` and make every section agree: **(a) Recommended — REWRITE, not delete.** Keep `configV0`/`MigrateWithStore`/`v0ConvertProvidersToModelList` (they are the live v0→v1 path) and change only `channelsConfigV0.ToChannelsConfig()` (`:113`) to **emit `map[string]ChannelInstanceConfig`** (matching the new `Channels` field type), exactly as the openclaw `ToStandardChannels` converter is being rewritten. This is what §1/§2 already say; delete the "DELETED" language from FR-2.2 + Assumption 5. The file stays in the **guard** scope too (it produces the real `config.ChannelsConfig`, so its reads/writes must resolve), so drop it from the guard-exclusion phrasing in the BDD (L84) and SC-4 (L164) — `config_old.go` is migrated, not excluded. **(b) If genuinely DELETE:** then `config.go:1784-1785` + `migration.go:37,484` must *also* be rewritten to drop the entire v0 migration path (an explicit, separate scope item with its own regression note — the v0→v1 migration tests `config_old_test.go`/`migration_test.go` would be deleted too), and the spec must say so. Leaving it as bare "DELETED" with those live callers untouched does not build. Reconcile L13/L24 ("rewrite") with L154/L203 ("DELETED") either way — they cannot both stand. |

## Briefed-item closure (round-6)

| Item | Live-tree check | Status |
|------|-----------------|--------|
| R5-F1 named producers are *all* of them | grep confirms exactly the 4 real-`config.ChannelsConfig` producers (`defaults.go:78`, `manager_channel.go:108`, `openclaw:990`, `config_old.go:114`); `openclaw:757` is the local type (out of scope) ✓ | **Producer set complete** |
| `go build` oracle now internally consistent with exclusions | **NO** — `config_old.go` "DELETED" disposition (FR-2.2/Assumption 5) breaks `config.go:1784`+`migration.go:37,484` → build fails; and it contradicts §1/§2 "rewrite" (R6-F1) | **NOT closed** |
| R5-F2 guard tooling = stdlib, no x/tools | SC-4 + test #8 say stdlib `go/parser`+`go/types`+`go/importer`; `go.mod` has 0 x/tools requires ✓ | **CLOSED** |

## Decision

**GATE C NOT granted — REVISE.** R5-F2 is closed (stdlib guard, no new dep). R5-F1's producer
enumeration is correct and complete. But the round-5 fix assigned `config_old.go` a **DELETE**
disposition that (1) contradicts the spec's own §1/§2 "rewrite, NOT exclude" rows, and (2) is
infeasible — `config_old.go` is the **live** v0→v1 migration module (consumed by `config.go:1784`
and `migration.go:37,484`), so deleting it breaks `go build ./...`, the very SC-4 primary oracle this
round certifies. The go-build oracle is therefore **not yet** internally consistent with the
exclusion/deletion list. Fix R6-F1: change `config_old.go`'s disposition to **rewrite
`ToChannelsConfig` to emit the instance map** (keeping the rest of the live v0 migration path),
delete the "DELETED" wording from FR-2.2 + Assumption 5, and align the guard phrasing (it's migrated,
not excluded). A targeted re-confirmation (config_old.go is REWRITE-not-DELETE everywhere; `go build`
after field deletion compiles `config.go`/`migration.go`) suffices — no full re-grill.

To address, run:

```
/plan-spec --revise docs/internal/specs/v01-spec2-connections-instance-email-spec.md docs/internal/specs/v01-spec2-connections-instance-email-spec-review.md
```

---

# Round-5 Verdict (2026-06-13) — REVISE (GATE C NOT granted)

Re-review after the round-4 REVISE. The round-5 fix abandons hand-enumeration for
**completeness-by-construction**: the typed fields are **deleted** so `go build ./...` is the
exhaustive oracle (FR-2.2, SC-4 primary), and test #8 becomes an **AST / `go/analysis`** guard
flagging selectors that resolve to `config.ChannelsConfig` (SC-4 secondary), catching the
`ch := cfg.Channels` alias the round-4 regex missed.

**Verdict: REVISE. GATE C NOT granted.** Grounded against the live tree
(`pkg/config/config.go:774`, `config_old.go`, `defaults.go`, `pkg/credentials/inject.go`,
`pkg/gateway/{gateway,rest}.go`, `pkg/channels/manager_channel.go`,
`pkg/migrate/sources/openclaw/openclaw_config.go`, `cmd/omnipus/internal/doctor/command.go`,
`go.mod`/`go.sum`).

## What round 5 genuinely closes

- **R4-F1 — CLOSED (sound).** Making `go build ./...`-after-deletion the primary oracle is a
  correct, complete oracle **for readers**: every consuming site (`cfg.Channels.<F>`,
  `ch.<Type>.<F>` alias, whole-value reads) becomes a compile error until migrated. All the
  newly-named example readers are confirmed live and correctly named: `ResolveAll`
  (`inject.go:68`, alias read at `:98`), `buildEnabledRefMap` (`gateway.go:203`),
  `HandleChannels` (`rest.go:4274`, alias at `:4343`), `emitGHSARemovalWarn` (`gateway.go:2081`),
  `countEnabledChannels` (`rest.go:4923`, alias at `:4924`), `toChannelHashes`/`toChannelConfig`
  (`manager_channel.go:44/107`, aliases at `:46/:109`), both doctor blocks
  (`doctor/command.go:118` + alias at `:176`), `config.go:1916` Discord normalizer, the 13
  factory constructors. The hand-list is now explicitly non-exhaustive examples; the compiler is
  the authority. The "a fifth hand-count would miss a fifth reader" failure mode is genuinely
  retired for the **reader** surface.
- **R4-F2 — CLOSED in principle (sound).** An AST/`go/types` guard that flags any selector whose
  resolved receiver type is `config.ChannelsConfig` is alias-safe by construction: the type
  resolver reports `ch`'s type as `config.ChannelsConfig` regardless of whether the access is
  `cfg.Channels.X` or `ch := cfg.Channels; ch.X`. Confirmed the alias form is the dominant access
  shape in the tree (gateway.go:205, rest.go:4343, rest.go:4924, inject.go:98,
  manager_channel.go:46/109, doctor:176), so the regex was indeed toothless and the AST approach
  is the right fix.

## New findings (block GATE C)

| ID | Sev | Lens | Finding | Recommended fix |
|----|-----|------|---------|-----------------|
| **R5-F1** | **MAJOR** | Inconsistency / Incompleteness / Infeasibility | **The `go build` primary oracle contradicts the spec's own openclaw/config_old exclusions on the PRODUCER side, and omits the seed producer.** Rounds 1–4 treated `openclaw` + `config_old.go` purely as *reader-side* false positives to exclude from the **grep**. Round 5 newly makes **`go build ./...` after field deletion** the primary gate — which surfaces a class the grep never could: **typed-field composite-literal _producers_**. Deleting the 13 typed fields turns every keyed `ChannelsConfig{WhatsApp:…,Telegram:…}` literal into an "unknown field" compile error. The live tree has **four** such producers the spec does not scope: (1) **`pkg/config/defaults.go:78`** — the seed/default `Channels: ChannelsConfig{WhatsApp:WhatsAppConfig{…},Telegram:TelegramConfig{…},…}` (in-scope, **not named anywhere** in FR-2.2/§2); (2) **`pkg/config/config_old.go:113`** `channelsConfigV0.ToChannelsConfig()` → keyed `ChannelsConfig{WhatsApp:…,Telegram:…}` (spec marks config_old **EXCLUDED**); (3) **`pkg/migrate/sources/openclaw/openclaw_config.go:990`** `ToStandardChannels()` → `config.ChannelsConfig{WhatsApp:config.WhatsAppConfig{Enabled:…},…}` (spec marks openclaw **EXCLUDED**); (4) **`pkg/channels/manager_channel.go:108`** `&config.ChannelsConfig{}` (named as a reader, but it is a producer). **None has a build tag — all four compile under `go build ./...`.** Therefore SC-4's primary gate ("`go build` succeeds after the typed fields are deleted") is **unachievable while the spec keeps openclaw + config_old unmodified**, and the two oracles **disagree**: the AST guard (with its `migrate/sources/openclaw` + `config_old.go` path-exclusions) stays green while `go build` is red. "Completeness by construction" is internally inconsistent as written. | Resolve the reader-vs-producer conflation. The path-exclusions for openclaw/config_old are correct for the **AST guard** (those reads are on the *foreign* `channelsConfigV0`/`OpenClawChannels` types and won't resolve to `config.ChannelsConfig` anyway), but those files **also produce the real `config.ChannelsConfig` via keyed literals**, and field deletion breaks those literals. So: (a) add `pkg/config/defaults.go` (seed literal — **rewrite**: seed an empty `map[string]ChannelInstanceConfig{}`, or the v0.1.0 default instances), and explicitly state that the two migration converters (`config_old.go::ToChannelsConfig`, `openclaw::ToStandardChannels`) **must be rewritten to emit the instance map** as part of the deletion — they are excluded from the *guard*, **not** from *compilation*. (b) Reword Assumption 5 / Out-of-scope so "excluded" means "excluded from the typed-field guard," not "left untouched" — because untouched, they don't build. (c) If the intent really is to leave openclaw/config_old producers alone, then the typed fields **cannot be deleted** and SC-4's primary oracle collapses back to the (now-retired) hand-enumeration — so this must be decided, not left implicit. |
| **R5-F2** | **MINOR** | Infeasibility / Constraints | **The AST guard's `go/analysis` tooling is an unaddressed new (test) dependency.** SC-4 secondary + test #8 mandate "AST / `go/analysis`". `go/analysis` and `go/packages` live in **`golang.org/x/tools`**, which is **not** a direct dependency (`go.mod`: 0 occurrences; `go.sum`: transitive-`/go.mod`-hash lines only — not pulled as a built module). Stdlib has `go/ast`/`go/parser`/`go/types`/`go/importer` but **not** `go/packages`/`go/analysis`. The spec never notes that "AST / `go/analysis`" pulls `golang.org/x/tools` into the build graph, nor reconciles it with Constraint #1 ("No new runtime deps") — even as a test-only dep this should be a conscious, stated choice. | Pick and state the mechanism: either (a) implement the guard with **stdlib only** (`go/parser` + `go/types` + `go/importer.ForCompiler`, single-package or `golang.org/x/tools`-free load), or (b) accept `golang.org/x/tools` as a **test-only** dependency and say so explicitly (test deps don't violate Constraint #1's runtime-binary intent, but the spec must make the call rather than hand-wave "AST / `go/analysis`"). Update test #8 + SC-4 with the concrete choice. |

## Decision

**GATE C NOT granted — REVISE.** The round-5 redesign correctly closes R4-F1 (compiler is a
complete *reader* oracle) and R4-F2 (AST guard is alias-safe). But the new "delete the fields →
`go build` is the oracle" gate exposes a **producer**-side gap the previous rounds' grep framing
hid (R5-F1, MAJOR): four typed-field composite-literal producers — including the in-scope seed
`defaults.go:78` and the two *excluded* migration converters (`config_old.go:113`,
`openclaw:990`) — will fail to compile on field deletion, so SC-4's primary gate is unachievable
while the spec's exclusion list keeps them untouched, and the AST guard (path-excluding them)
green-lights a state where `go build` is red. Plus a minor tooling-dependency gap (R5-F2). Both
are concrete and mechanically fixable, hence REVISE not BLOCK. Fix: bring the four producers into
explicit scope (seed → rewrite; the two converters → rewrite to emit the map, excluded from the
*guard* only), reword Assumption 5 / Out-of-scope so "excluded" ≠ "left uncompiled," and pin the
guard's tooling (stdlib vs `x/tools` test dep). A targeted re-confirmation (the four producers are
in FR-2.2/§2 scope with a rewrite disposition; Assumption 5 distinguishes guard-exclusion from
compile-exclusion; SC-4 names the AST tooling) suffices — no full re-grill.

To address, run:

```
/plan-spec --revise docs/internal/specs/v01-spec2-connections-instance-email-spec.md docs/internal/specs/v01-spec2-connections-instance-email-spec-review.md
```

---

# Round-4 Verdict (2026-06-13) — REVISE (GATE C NOT granted)

Re-review after the round-3 REVISE (R3-F1: `cmd/` typed readers). Briefed fixes:
FR-2.2 now says *"every factory constructor that reads typed fields (grep, don't count to 13)"*,
adds the three `cmd/` sites (`auth/weixin.go:117`, `auth/wecom.go:206`, `doctor/command.go:118`)
as rewrites, states *"no typed-field access remains anywhere in the module (`pkg/`+`cmd/`)"*; §2
adds the `cmd/` readers row; SC-4 + test #8 + US-1 AC-2 widen the guard root to the whole module
(`pkg/`+`cmd/`), excluding `migrate/sources/openclaw` + `config_old.go`. The brief's thesis: *"the
completeness is now defined by a module-wide grep-to-zero guard rather than an enumerated list, so
it catches any remaining reader at implementation time."*

**Verdict: REVISE. GATE C NOT granted.** I grounded the **whole** typed-reader surface against the
live tree (enumerating the enclosing function of every `cfg.Channels.<Type>.*` **and** every
`ch := cfg.Channels; … ch.<Type>.*` alias read across `pkg/`+`cmd/`, minus the two exclusions).
Two findings, both MAJOR:

1. **R4-F1 — the named R3-F1 sites are scoped, but they were again not the last ones.** The
   enumerated inventory (§2 Symbols + FR-2.2) still omits **at least five live typed-reader
   functions**, none of which are factory constructors / `emitGHSARemovalWarn` /
   `countEnabledChannels` / `config_old.go` / `config.go:1916` / the three now-named `cmd/` sites.
   Same self-inflicted SC-4/test-#8 failure mode as R2-F1 and R3-F1, a third time.
2. **R4-F2 — the guard the brief leans on is UNSOUND module-wide, so its "grep-to-zero" thesis is
   false.** The guard greps for `cfg.Channels.<TypedField>` (US-1 AC-2 line 41, BDD line 84, SC-4
   line 164, FR text line 11/23). But the **dominant access form in the actual tree is the
   `ch := cfg.Channels` alias**, where reads compile as `ch.Telegram.Enabled` — a
   `cfg.Channels.<Field>` regex matches **none** of them. The guard would report **0 while ~60+
   typed reads remain**, i.e. it green-lights a migration that did not happen. The round-4 fix's
   whole premise — "a grep-to-zero guard catches any remaining reader" — does **not** hold for the
   guard as specified.

Because R4-F2 means the guard can falsely PASS, "module-wide grep-to-zero" is **not** a safe
substitute for the inventory until the guard's match pattern is fixed. These are MAJOR (concrete,
self-inflicted, but mechanically fixable), so REVISE, not BLOCK.

## Live-tree typed-reader census (enclosing function of every typed read, `pkg/`+`cmd/`, minus openclaw + config_old.go)

| Function (enclosing) | Site | Read form | In §2/FR-2.2 scope? |
|---|---|---|---|
| 13 factory constructors (`telegram.go` etc., each `init.go`) | `pkg/channels/*` | mixed | **YES** (F-1) |
| `Manager.initChannels` | `manager.go:645` | `*config.ChannelsConfig` | **YES** |
| `emitGHSARemovalWarn` | `gateway.go:2084-2102` | `ch.<T>.*` alias | **YES** |
| `countEnabledChannels` | `rest.go:4927-4939` | `ch.<T>.Enabled` alias | **YES** |
| Discord `MentionOnly` (`migrateChannelConfigs`) | `config.go:1916-1917` | `c.Channels.Discord.*` | **YES** (R2-F1) |
| `saveWeixinConfig` | `cmd/.../auth/weixin.go:117-124` | `cfg.Channels.Weixin.*` | **YES** (R3-F1) |
| `applyWeComAuthResult` | `cmd/.../auth/wecom.go:206-210` | `cfg.Channels.WeCom.*` | **YES** (R3-F1) |
| `checkPreviewPort` | `cmd/.../doctor/command.go:118` | `cfg.Channels.LINE.WebhookPort` | **YES** (R3-F1) |
| **`ResolveAll`** (credential injection, boot path) | **`pkg/credentials/inject.go:100-119`** (~19 `ch.<T>.*Ref` reads) | `ch := cfg.Channels` alias | **NO — R4-F1** |
| **`buildEnabledRefMap`** | **`pkg/gateway/gateway.go:212-222`** (~11 reads) | `ch := cfg.Channels` alias | **NO — R4-F1** (spec names `gateway.go` only for `emitGHSARemovalWarn`) |
| **`HandleChannels`** (GET `/api/v1/channels` list → emits `ChannelEntry`) | **`pkg/gateway/rest.go:4350-4413`** (~13 reads) | `ch := cfg.Channels` alias | **NO — R4-F1** (spec names `rest.go` only for `countEnabledChannels`) |
| **`checkDMPolicies`** (doctor DM-allowlist audit) | **`cmd/.../doctor/command.go:179-185+`** | `ch := cfg.Channels` alias | **NO — R4-F1** (spec names doctor only at `:118`/`checkPreviewPort`) |
| **`toChannelHashes` / `toChannelConfig`** | **`pkg/channels/manager_channel.go:46,109`** | `ch := cfg.Channels` whole-value marshal + `&config.ChannelsConfig{}` literal | **NO — R4-F1** (spec names `manager.go`, not `manager_channel.go`) |

## Round-4 findings (must fix before GATE C)

| ID | Sev | Lens | Finding | Recommended fix |
|----|-----|------|---------|-----------------|
| **R4-F1** | **MAJOR** | Incompleteness / Incorrectness | The enumerated inventory still misses ≥5 live typed-reader functions: **`ResolveAll`** (`pkg/credentials/inject.go:100-119`, ~19 reads — a credential **boot-path** reader, not a foreign source), **`buildEnabledRefMap`** (`pkg/gateway/gateway.go:212`), **`HandleChannels`** (`pkg/gateway/rest.go:4350` — the channel-**list** handler that *emits `ChannelEntry`*, directly coupled to the FR-2.5 contract change), **`checkDMPolicies`** (`cmd/.../doctor/command.go:179` — a **second** doctor block beyond the named `:118`), and **`toChannelHashes`/`toChannelConfig`** (`pkg/channels/manager_channel.go:46,109` — whole-value marshal + `&config.ChannelsConfig{}` literal). All are live omnipus `config.Config` typed access, in-scope for the guard, **not** legitimate exclusions. SC-4 / test #8 / US-1 AC-2 will fail on them exactly as on R3-F1's `cmd/` sites. | Add all five to FR-2.2 + §2 Symbols (disposition **rewrite** to read the instance map). Add `pkg/credentials/` and `pkg/channels/manager_channel.go` to the §2/Impact direct-blast-radius. Note `HandleChannels` must also be rewritten to emit the new `instance_id`/`identity` `ChannelEntry` fields (ties to FR-2.5). **Stop enumerating by hand**: make the guard (R4-F2) the source of truth and reduce §2/FR-2.2 to "whatever the guard flags," because a fourth hand-count will miss a fourth site. |
| **R4-F2** | **MAJOR** | Infeasibility / Incorrectness | **The guard as specified is unsound and can falsely PASS.** US-1 AC-2 (L41), BDD (L84), SC-4 (L164), and the §1/§2 prose (L11/L23) all define the guard as grepping for **`cfg.Channels.<TypedField>`**. But the predominant read form in the tree is the local alias **`ch := cfg.Channels`** followed by **`ch.<Type>.<Field>`** (confirmed in `ResolveAll`, `buildEnabledRefMap`, `HandleChannels`, `countEnabledChannels`, `checkDMPolicies`, `emitGHSARemovalWarn`, `manager_channel.go`, `inject.go`, `doctor`). A `cfg.Channels.<Field>` regex matches **zero** of those, so the guard returns 0 while the migration is incomplete — a green light for a half-done change. The brief's claim that "a module-wide grep-to-zero guard catches any remaining reader" is therefore **false for this guard**. | Make the guard catch typed-field access **regardless of the receiver expression**. Either (a) **AST/`go/analysis`-based**: flag any selector whose resolved type is `config.ChannelsConfig` (or a field thereof) outside the excluded paths — robust against aliases, range vars, and `&config.ChannelsConfig{}` literals; or (b) if grep-only, the guard MUST be a **two-clause** match: the direct `\.Channels\.<Field>` form **and** the alias form (detect `\w+ := \w+\.Channels` bindings, then flag `<alias>\.<Field>` within that function) — plus reject any `config.ChannelsConfig` **type** reference outside the migration package. Specify this in test #8, SC-4, US-1 AC-2, and BDD line 84 so the implementer cannot ship the toothless regex. Until the guard provably catches the alias form, SC-4's "0 typed access" is not actually verified. |

## Briefed-item closure (round-4)

| Item | Live-tree check | Status |
|------|-----------------|--------|
| FR-2.2 "(a) every factory constructor that reads typed fields (grep, don't count to 13)" | L154 wording present ✓ | Text present |
| FR-2.2 "(b) three `cmd/` sites as rewrites" | `weixin.go:117` / `wecom.go:206` / `doctor/command.go:118` all named in FR-2.2 (L154) + §2 (L25) ✓; all three confirmed live typed readers ✓ | **The named sites CLOSED** |
| FR-2.2 "(c) no typed-field access remains anywhere in the module (`pkg/`+`cmd/`)" | The *assertion* is in the text (L154) but is **factually false against the tree** — ≥5 functions still read typed fields (R4-F1) | **NOT closed** |
| §2 adds the `cmd/` readers row | L25 present ✓ | Present |
| SC-4 / test #8 / US-1 AC-2 widen guard root to whole module | Root widened to `pkg/`+`cmd/` ✓ (L41, L143, L164) — **but the match *pattern* is still the alias-blind `cfg.Channels.<Field>` form (R4-F2)** | **Root widened; pattern unsound** |

The narrow R3-F1 patch (scope the three named `cmd/` sites) is correctly applied. But the brief's
broader thesis — that a module-wide grep-to-zero guard now makes the inventory self-completing — is
**not** established: (1) the inventory still misses five functions (R4-F1), and (2) the guard's
match form is alias-blind, so "grep-to-zero" can be reached with the migration incomplete (R4-F2).

## Decision

**GATE C NOT granted — REVISE.** The three round-3 `cmd/` sites are now in scope; the guard's
*search root* is correctly the whole module. But (R4-F1) ≥5 typed-reader functions remain unscoped,
and (R4-F2) the guard's match *pattern* is alias-blind and can falsely report PASS. Fix both —
add the five functions to FR-2.2/§2 **and** redefine the guard to be receiver-agnostic
(AST-based, or a two-clause grep that also catches `ch := cfg.Channels` aliases and
`config.ChannelsConfig` type references) — and replace the hand-enumeration with "whatever the
(now-sound) guard flags." After that, SC-4 is genuinely achievable and this can PASS. A targeted
re-confirmation (guard catches alias reads; the five functions are in FR-2.2 scope) suffices — no
full re-grill needed.

To address, run:

```
/plan-spec --revise docs/internal/specs/v01-spec2-connections-instance-email-spec.md docs/internal/specs/v01-spec2-connections-instance-email-spec-review.md
```

---

# Round-3 Verdict (2026-06-13) — REVISE

Re-review of the round-2 REVISE. The single residual (R2-F1) and the three carried MAJORs
(F-8, F-11, F-12) were re-checked against the **live tree** (`pkg/config/config.go`,
`pkg/channels/**/init.go`, `pkg/gateway/gateway.go`, `pkg/gateway/rest.go`, `cmd/omnipus/**`,
`pkg/routing/route.go`, `contracts/components/schemas/ChannelId.yaml`).

**Verdict: REVISE.** Three of the four briefed items are genuinely fixed in the spec text. But
the central premise of this round — *"confirm `config.go:1916` is the last typed reader"* — is
**false against the live tree**. The grep that closed R2-F1 was run too narrowly: there are
**three more live typed-reader sites** the spec's inventory (§1 In-scope, §2 Symbols, FR-2.2)
still does not scope. They will trip the spec's own **SC-4** ("0 typed `ChannelsConfig.<X>`
access") and **test #8** (the typed-field grep-guard) exactly as R2-F1 did. This is the same
class of self-inflicted failure (MAJOR), so **GATE C is NOT granted** — but it is a one-shot
scope addition, not a structural defect, so this is REVISE, not BLOCK.

## Briefed-item closure (round-3)

| # | Claim | Live-tree check | Status |
|---|-------|-----------------|--------|
| **R2-F1** | `config.go:1916` (Discord `MentionOnly` normalizer) now a REWRITE in FR-2.2 + §2 | FR-2.2 (line 153) and §2 Symbols (line 24) now name `pkg/config/config.go:1916` as a rewrite, **not** an exclusion ✓. The specific site is correctly characterized. | **Text CLOSED — but premise wrong (see R3-F1)** |
| **F-8** | Cap enforced at config LOAD as well as the API | FR-2.3 (line 154): *"enforced both at the **API** (422 on a 2nd) **and at config LOAD** (a hand-edited `config.json` with 2 of a type is rejected/logged, not silently run)"* ✓. SC-3 + datasets present. | **CLOSED** |
| **F-11** | IMAP mandates TLS; auth-failure (permanent) vs unreachable (transient) | FR-2.7 (line 158): *"over TLS (IMAPS/SMTPS or STARTTLS — no plaintext auth)…distinguish auth-failure (permanent — surface in Connectors, stop retrying) from unreachable/timeout (transient — retry with backoff)"* ✓. Addresses round-1 F-11 + F-19 (wire-plaintext). | **CLOSED** |
| **F-12** | US-5 specifies agent-bound vs user-attributed routing; wires field into existing `ResolveRoute` without changing the algorithm | US-5 (line 48) now distinguishes `kind:agent` (acts AS agent X) vs `kind:user` (attributed to user, routed via default agent / `ResolveRoute`), and states it *"persists the field and wires it into the existing `ResolveRoute` input; it does not change the routing algorithm"* ✓. `pkg/routing/route.go` confirms `ResolveRoute` + `IdentityLinks` exist as the wiring surface. | **CLOSED** |

## Residual finding (must fix before GATE C)

| ID | Sev | Finding | Recommended fix |
|----|-----|---------|-----------------|
| **R3-F1** | **MAJOR** | **`config.go:1916` is NOT the last typed reader.** A live-tree grep (`cfg.Channels.<Field>` / `c.Channels.<Field>`, excluding `pkg/migrate/sources/openclaw` and `config_old.go`) returns **three reader sites outside the spec's entire inventory**, none of which are factory constructors, `emitGHSARemovalWarn`, `countEnabledChannels`, `config_old.go`, or `config.go:1916`: (1) **`cmd/omnipus/internal/auth/weixin.go:117-124`** — the live `omnipus weixin` onboarding command's `saveWeixinConfig` writes `cfg.Channels.Weixin.{Enabled,TokenRef,BaseURL,Proxy}`; (2) **`cmd/omnipus/internal/auth/wecom.go:206-210`** — the WeCom onboarding command writes `cfg.Channels.WeCom.{Enabled,BotID,SecretRef,WebSocketURL}`; (3) **`cmd/omnipus/internal/doctor/command.go:118`** — `omnipus doctor` reads `cfg.Channels.LINE.WebhookPort`. All three are live omnipus `config.Config` access (not a foreign source type), so they are in-scope for the grep-guard and **not** legitimate exclusions. Because the spec scopes none of them, **SC-4** and **test #8** will fail on these ~10 sites just as they would have on `config.go:1916`. The round-2 closure grep evidently scoped only `pkg/`, missing `cmd/`. | Add to FR-2.2 + §2 Symbols (disposition: **rewrite** to read the instance map): the `weixin`/`wecom` CLI onboarding writers (`cmd/omnipus/internal/auth/weixin.go`, `…/wecom.go`) and the `doctor` reader (`cmd/omnipus/internal/doctor/command.go`). Do **not** add them to the guard exclusion list. Also widen the guard's stated search root in SC-4 / test #8 / H6 from `pkg/`-implied to the **whole module** (`cmd/` + `pkg/`, excluding only `pkg/migrate/sources/openclaw` + removed `config_old.go`), so the guard actually covers the code it must. After this, SC-4's "0 typed access" becomes achievable. |

## Secondary note (not blocking)

- **Count framing.** The spec says "13 factory constructors" throughout; the live tree has **14** factory-registering channel packages (`telegram, discord, slack, matrix, irc, googlechat, whatsapp_native, feishu, dingtalk, line, qq, wecom, weixin` + `registry.go`), matching the **14-value `ChannelId` enum** (webchat + 13 external). "13 external (webchat is the internal WS channel)" — the round-2 framing — is defensible, so this is not a finding; but FR-2.2's "all 13 factory constructors" must be read as **all factory constructors that read typed fields**, and the implementer should grep, not count to 13. Worth a one-word clarification ("all factory constructors") to avoid an off-by-one where a 14th package's reader is skipped.

## Decision

F-8, F-11, F-12 are closed against the live tree. R2-F1's *named* site is now scoped — but the
brief's premise that it was the **last** typed reader is wrong: the `cmd/` onboarding + doctor
sites (R3-F1, MAJOR) remain unscoped and re-break SC-4 / test #8. **GATE C NOT granted.** Fix
R3-F1 (add the three `cmd/` sites to FR-2.2 + §2 and widen the guard's search root to the whole
module), then this can PASS. No full re-grill is required for that edit — a targeted confirmation
that `cmd/omnipus/internal/auth/{weixin,wecom}.go` and `cmd/omnipus/internal/doctor/command.go`
are in the FR-2.2 rewrite scope, and that the guard covers `cmd/`, is sufficient.

To address, run:

```
/plan-spec --revise docs/internal/specs/v01-spec2-connections-instance-email-spec.md docs/internal/specs/v01-spec2-connections-instance-email-spec-review.md
```

---

# Round-2 Verdict (2026-06-13) — REVISE

Re-review of **Rev 2** of the spec after the round-1 BLOCK (5 CRITICAL / 7 MAJOR). The ADR
(`ADR-019`) was genuinely amended (FR-2 scope corrections + `emersion/go-imap`+`net/smtp`
dependency decision — both verified present at `ADR-019` lines 31-33). Grounded against the
**live tree** (`registry.go`, `manager.go`, `config.go`, `gateway.go`, `rest.go`,
`go.mod`/`go.sum`, `contracts/components/schemas/ChannelId.yaml` + `ChannelEntry.yaml`,
`pkg/migrate/sources/openclaw/`).

**Verdict: REVISE.** Four of the five round-1 CRITICALs are fully closed; the fifth (F-3) is
**partially closed with one residual reader that breaks the spec's own SC-4 / guard #8.** No
new CRITICAL remains, so this is not a BLOCK — but the F-3 gap is a concrete, self-inflicted
test failure that must be fixed before `/taskify`. GATE C is **not** granted.

## Closure verification (round-1 CRITICALs F-1..F-5 + the two re-review MAJORs F-6/F-7)

| # | Claim | Live-tree check | Status |
|---|---|---|---|
| **F-1** | All 13 factory constructors now in scope; reads instance config | `ChannelFactory = func(cfg *config.Config, …)` at `registry.go:15` ✓; `telegram.go:66` reads `cfg.Channels.Telegram` ✓; 14 channel packages register factories (webchat is the internal WS channel — "13 external" framing is correct) | **CLOSED** |
| **F-2** | `emersion/go-imap` (pure-Go) + stdlib `net/smtp` approved in ADR FR-2 | `go.mod`/`go.sum` have **0** imap deps and `net/smtp` is imported nowhere (matches "must be added"); ADR-019:33 records the deliberate Constraint-#1 exception scoped to the email channel ✓; spec ambiguity #3 + Assumption 2 cite `[ADR FR-2]` ✓ | **CLOSED** |
| **F-3** | Reader inventory adds `emitGHSARemovalWarn`, `countEnabledChannels`, `config_old.go` | Both funcs exist (`gateway.go:2081`, `rest.go:4923`) and `config_old.go` exists ✓ — **BUT** the live tree still has `pkg/config/config.go:1916-1917` (`c.Channels.Discord.MentionOnly` normalizer), which round-1 F-3 **explicitly named** and which Rev 2 does **not** list in scope **nor** in the guard exclusion list. | **PARTIAL — residual gap (R2-F1)** |
| **F-4** | Fields TYPE-keyed (validate type vs `ChannelId`); storage INSTANCE-keyed | `channelSensitiveFields`/`channelRequiredFields` keyed by `gen.ChannelId` (`rest.go:4698,4718`); `validChannelIDs` gate at `rest.go:4282,4646`; `channelCredKey(channelID,field)` at `rest.go:2090` — all match the specified split ✓; test #5 covers it | **CLOSED** |
| **F-5** | `ChannelId += email`; `ChannelEntry += instance_id+identity`; `additionalProperties:false` preserved | `ChannelId.yaml` enum has **no `email`** (confirmed — the delta is real) ✓; `ChannelEntry.yaml` has `additionalProperties:false` + `required:[id,name,transport,enabled,description]` ✓; `transport` enum already includes `tcp`/`native` (so email's transport value is available) ✓ | **CLOSED** |
| **F-6** | Loop has type→factory map (`whatsapp`→`whatsapp_native`) | Factory registered as `whatsapp_native` (`manager.go:598`, `nonFatalChannels["whatsapp_native"]` at :719) while the config/`ChannelId` type is `whatsapp` ✓; US-3 AC-2 + FR-2.4 specify the map | **CLOSED** |
| **F-7** | Guard excludes `openclaw` (`OpenClawChannels`) + removed `config_old.go`; targets the 13 typed names | `pkg/migrate/sources/openclaw/openclaw_config.go` defines its own `OpenClawChannels` with legitimate `c.Channels.Telegram` reads ✓; exclusions stated in US-1 AC-2, SC-4, Assumption 5 | **CLOSED (see R2-F1 caveat)** |

## Residual finding (must fix before GATE C)

| ID | Sev | Finding | Recommended fix |
|----|-----|---------|-----------------|
| **R2-F1** | **MAJOR** | The F-3 closure is incomplete. The spec's reader inventory (§1 In-scope, §2 Symbols, FR-2.2) lists `emitGHSARemovalWarn`, `countEnabledChannels`, `config_old.go` but **omits `pkg/config/config.go:1916-1917`** — a live typed-field reader (`c.Channels.Discord.MentionOnly` / `.GroupTrigger.MentionOnly` Discord normalizer). Round-1 F-3 named this exact site (`config.go:1916`). Because Rev 2 neither scopes it for migration nor lists it in the guard exclusion set (which is only `openclaw` + `config_old.go`), the spec's own **SC-4** ("0 typed `ChannelsConfig.<X>` access") and **test #8** (the typed-field grep-guard) will **fail** on this line. The grep I ran confirms 22 live reader sites across `pkg/config/config.go`, `pkg/channels/manager.go`, and the 13 channel packages — the spec's inventory is otherwise complete but for the `config.go` normalizer. | Add `pkg/config/config.go` (the Discord `MentionOnly` normalization at :1916-1917) to the FR-2.2 migration scope and the §2 Symbols table (disposition: rewrite to read the instance map), so the guard can pass. Do **not** add it to the exclusion list — it is omnipus's own `config.Config`, not a foreign source type. After this, SC-4's claim of "0 typed access" becomes achievable. |

## Phase 1 re-check (deltas from round 1)
- **Regression scope (was FAIL):** now lists the 13 rewritten constructors + the 3 extra readers — **near-PASS**, but the `config.go` normalizer (R2-F1) keeps it one site short of complete.
- **Datasets (was PARTIAL):** Rev 2 adds non-enum-instance-id→422, whatsapp→whatsapp_native, imap-down→degraded — improved. (Note: the round-1 MAJORs F-8 cap-at-load, F-11 IMAP auth-failure-vs-unreachable + TLS, and F-12 identity-routing semantics were **not** in this round's closure brief and are not re-verified as resolved here; they remain open as previously filed unless the author addressed them out-of-band.)
- **Contract delta (was a gap):** §2 + FR-2.5 now name the `ChannelId` enum add, the required-vs-optional `instance_id`/`identity`, and `additionalProperties:false` preservation — **PASS**.

## Decision
Four CRITICALs (F-1/F-2/F-4/F-5) and both re-review MAJORs (F-6/F-7) are closed against the
live tree. F-3 is one reader short (R2-F1, MAJOR). **GATE C NOT granted.** Fix R2-F1 (a
one-line scope addition), then this can PASS. No re-grill of the whole spec is required for
that single edit — a targeted confirmation that `pkg/config/config.go` is in the FR-2.2 scope
is sufficient.

To address, run:

```
/plan-spec --revise docs/internal/specs/v01-spec2-connections-instance-email-spec.md docs/internal/specs/v01-spec2-connections-instance-email-spec-review.md
```

---

# Spec Review — Spec-2: Connection-as-Instance Migration, Connectors UI & Basic Email (round 1, BLOCK)

- **Spec under review:** `docs/internal/specs/v01-spec2-connections-instance-email-spec.md`
- **Mode:** `plan-spec` (full structural + 8-lens review)
- **Reviewer stance:** adversarial / read-only
- **Grounded against:** `pkg/config/config.go`, `pkg/channels/manager.go`, `pkg/channels/registry.go`, `pkg/channels/telegram/*`, `pkg/gateway/rest.go`, `pkg/gateway/gateway.go`, `contracts/components/schemas/Channel*.yaml`, `go.mod`/`go.sum`, `docs/internal/architecture/ADR-019-*.md`

---

## Executive Summary

This spec proposes the one deliberate breaking migration of v0.1.0 (channels typed-singleton → `map[string]ChannelInstanceConfig`, cap 1) plus a Connectors UI and a new IMAP/SMTP email channel. The intent is sound and ADR-aligned, but the spec **materially under-scopes the blast radius** and rests on **two factual assumptions that the live tree contradicts**.

**Total findings: 19** — 5 CRITICAL, 7 MAJOR, 4 MINOR, 3 OBSERVATION.

The two showstoppers:
1. **The `ChannelFactory` signature reads typed fields, so "13 factories unchanged" is false.** Every one of the 13 channel constructors reaches into `cfg.Channels.<TypedField>` directly (e.g. `telegram.go` has 6+ `cfg.Channels.Telegram.X` sites). Removing the typed fields breaks all 13 packages at compile time. The spec scopes only `initChannels` for rewrite and explicitly declares the factories "unchanged in behaviour" — they cannot be left unchanged; they will not compile.
2. **There is no pure-Go IMAP client in the tree, and `net/smtp` is not even imported.** FR-2.6's "pure-Go, an IMAP client lib already in go.mod or added" collides head-on with **Hard Constraint #1 ("No new runtime deps")**. The spec never resolves this; ambiguity-warning #3 punts it as a "flag for operator," which is not a decision a spec may defer past the gate.

**Verdict: BLOCK.**

---

## Findings Table

| ID | Sev | Lens | Section | Finding | Recommended fix |
|----|-----|------|---------|---------|-----------------|
| F-1 | CRITICAL | Incorrectness / Incompleteness | §2 Symbols ("13 factories … unchanged"), FR-2.1, SC-7 | The `ChannelFactory` type is `func(cfg *config.Config, secrets, bus)` (`pkg/channels/registry.go:15`); each constructor reads `cfg.Channels.<Typed>` itself (`telegram.go:66,189,370,415,907,916`). Deleting the typed fields breaks all 13 packages. "Activation moves to the loop, internals unchanged" is impossible. | Decide and specify the factory contract change: either (a) change `ChannelFactory` to receive the per-instance `ChannelInstanceConfig` (+ instance id) and rewrite each constructor's reads, or (b) keep `*config.Config` but have factories read `cfg.Channels[instanceID]`. Either way it is **13 packages rewritten**, not "unchanged." Update §2, FR-2.1, SC-7, the Impact table, and the Regression section accordingly. |
| F-2 | CRITICAL | Infeasibility / Constraints | FR-2.6, §2, §4 boundaries, Assumption 2, Ambiguity #3 | No pure-Go IMAP client is in `go.mod`/`go.sum` (no `emersion/go-imap`, `go-message`, `go-smtp`); `net/smtp` is imported nowhere in `pkg/`. "already in go.mod or added — pure-Go only" + **Constraint #1 "No new runtime deps"** is an unresolved contradiction. | Resolve before the gate: name the exact lib + version, confirm it is pure-Go (no CGo, no `replace` to a C shim), and get an explicit Constraint-#1 exception in the ADR (a new dep is an architectural decision, not a spec footnote). If no acceptable lib exists, either cut email from v0.1.0 or write a minimal pure-Go IMAP reader in-tree and scope that work. Do not leave it as "flag for operator." |
| F-3 | CRITICAL | Incompleteness | §2 ("every reader of `cfg.Channels.<X>`"), FR-2.8, Test #7 | The typed-field readers are **far more than the 13 factories**. Live omnipus readers also include: `manager.go::initChannels`; `gateway.go::emitGHSARemovalWarn` (2078-2104, 7 sites); `rest.go::countEnabledChannels` (4923-4945, 13 sites); `config.go:1916` Discord normalization; `config_old.go` secret-migration (which the greenfield non-behavior says must be removed/dead anyway). The spec's Impact table lists only "`initChannels`, every reader" generically and SC-7 frames it as "the 13 channels." | Produce the mechanical reader inventory the ADR demands ("grep … across `pkg/`" — ADR-019 §Experiments), enumerate every site, and give each a disposition (rewrite / delete / N/A). The Impact "Direct (d=1)" cell must list `emitGHSARemovalWarn`, `countEnabledChannels`, the config normalizer, and `config_old.go`, not just `initChannels`. |
| F-4 | CRITICAL | Incompleteness / Insecurity | FR-2.5, US-6, §2 (`configureChannel`/`channelCredKey`), Test #4 | `configureChannel` keys config and credentials by the **type** (`m["channels"][channelID]`, `channelCredKey(channelID, field)`), and `channelSensitiveFields`/`channelRequiredFields` are maps keyed by `gen.ChannelId` (the **closed type enum**, `rest.go:4698,4718`). In the instance-keyed model the URL `{id}` becomes an arbitrary instance id (`tg-1`), which is **not** a valid `ChannelId`, so `channelSensitiveFields[gen.ChannelId("tg-1")]` returns nil → secrets silently pass through inline (a SEC-23 violation), and `testChannel` reports "no required fields." | Specify the instance→type resolution: the handler must look up the instance's `type` from the config map and key `channelSensitiveFields`/`channelRequiredFields`/`channelCredKey` by **type**, while keying the config write and credential ref by **instance id** (so two instances of a type don't collide on the same cred key — relevant now for forward-compat even at cap 1). Add a test that a non-enum instance id still routes secrets to refs. |
| F-5 | CRITICAL | Inconsistency / Contracts | FR-2.4, US-4, §2, Test #8 | `ChannelEntry` has `additionalProperties: false` and `required: [id, name, transport, enabled, description]`, with `id` `$ref`-ing the **closed** `ChannelId` enum (`webchat,telegram,…google-chat` — **no `email`**). Adding `email` as a channel type and adding `instance_id`/`identity` as **required** will (a) fail validation for the new email channel unless `ChannelId` gains `email`, and (b) break every existing fixture/handler that emits a `ChannelEntry` without the new required fields. The spec says "modify … carry instance_id + identity" but never mentions the `ChannelId` enum, the `transport` field, `additionalProperties: false`, or required-vs-optional. | Spell out the exact schema delta: add `email` to `ChannelId.yaml`; add `instance_id` (string) and `identity` (object `{kind: enum[agent,user], id: string}`) as **optional** (or migrate all emitters in the same PR if required); state the `transport` value for email (`tcp`? `native`?). Run the ADR-mandated **dry-run contract regen** and paste the diff into the spec. |
| F-6 | MAJOR | Incompleteness | FR-2.3, §2, BDD "activates without touching initChannels" | The registry name ≠ config type for WhatsApp: the factory is registered as `whatsapp_native` but the config type/`ChannelId` is `whatsapp` (`manager.go:598`, `nonFatalChannels["whatsapp_native"]`). A loop that does `getFactory(cfg.Type)` will fail to find a factory for `type:"whatsapp"`. Telegram-token/Matrix-multi-field gating (`warnMisconfigured`) is also per-type branch logic the if-ladder performs today. | Specify the type→factory-name mapping (and where it lives) and how per-type required-field gating (`warnMisconfigured`) survives the loop — today it's hardcoded per branch; a generic loop needs `channelRequiredFields` (already exists) wired in. Note the `whatsapp`→`whatsapp_native` and `nonFatalChannels` interaction. |
| F-7 | MAJOR | Infeasibility / Insecurity | Test #7, US-1 AC-2, FR-2.8, SC-4 | The grep-guard `TestRenameGuard_NoTypedChannelFieldAccess` will **false-positive** on `pkg/migrate/sources/openclaw/openclaw_config.go` (its own `OpenClawChannels` type, ~60 legitimate `c.Channels.Telegram` sites) and on `config_old.go` migration code. This is the exact Spec-1 grep-guard lesson. As written ("0 `cfg.Channels.<TypedField>` access") the guard is either un-passable or, if loosened naively, toothless. | Define the guard precisely: scope to the omnipus `config.Config` type only (e.g. AST-based, or path-scoped to `pkg/` excluding `pkg/migrate/sources/**` and any retired `config_old.go`), match the specific field selectors, and assert against that set. State the exact exclude list in the spec so the implementer doesn't reinvent it. |
| F-8 | MAJOR | Incompleteness | §3 edge cases, FR-2.2, BDD "second instance rejected" | Cap enforcement is specified at the **API** (422 on a 2nd POST) but not at **config load**. What happens if a hand-edited `config.json` contains two enabled instances of one type? The loop would activate both (or nondeterministically one). No load-time validation/repair is specified. | Specify load-time behavior: either reject the config, or activate the first and record a non-fatal failure for the rest, mirroring the existing degraded pattern. Add a test. |
| F-9 | MAJOR | Ambiguity / Incompleteness | FR-2.2, US-2 AC-1 | "cap at 1 **enabled** instance per type" — but US-2 AC-1 rejects "a second telegram instance is added," not "a second *enabled* one." Can you have one enabled + one disabled instance of a type at cap 1? Is the cap on total instances or enabled instances? These give different APIs and migration shapes. | Pin the cap semantics: total instances per type vs. enabled per type. Given the v0.1.0 goal is "behaviour unchanged," recommend **total = 1 per type** (simplest; matches the typed singleton). State it in FR-2.2 and align US-2/BDD. |
| F-10 | MAJOR | Ambiguity | Ambiguity #1, FR-2.1, US-1 | "instance id source = server-generated; assume … document" is left unresolved (no format, no collision handling, no who-generates). Edge case "Duplicate instance id → rejected" implies client-supplied ids, contradicting "server-generated." | Decide: server-generates a stable id (e.g. `type` itself at cap 1, or a uuid) vs. client-supplies. Specify format, uniqueness scope, and the 422/409 on duplicate. At cap 1 the simplest answer is **instance id == type**, which also keeps `channelCredKey`/URL routing unchanged — call that out as the recommended path. |
| F-11 | MAJOR | Incompleteness / Operability | US-7, FR-2.6, §4 boundaries | Email inbound is "IMAP poll on an interval" but the spec gives no interval value, no dedup/seen-state (so the agent doesn't reprocess the whole mailbox each poll), no outbound recipient/threading semantics, no TLS/STARTTLS requirement, no auth-failure vs. host-unreachable distinction. "IMAP unreachable degrades" is tested but auth-failure (wrong password) is not. | Specify: poll interval (config key + default), seen/last-uid persistence (where stored — per-instance file?), required TLS (IMAPS/SMTPS or STARTTLS — plaintext creds over cleartext is a SEC issue), and the inbound→bus mapping (sender = From, chat id = ?). Add an auth-failure test distinct from unreachable-host. |
| F-12 | MAJOR | Incompleteness | US-5, FR-2.5(identity), Traceability | Identity (`agent`|`user`) is "persisted only; routing via existing `ResolveRoute`." But `ResolveRoute` resolves from `cfg.Bindings[]`, not from a per-channel `identity` field — the spec never shows how a persisted `identity` reaches routing, or what `identity.kind:user` even means for routing (route as which user? the single greenfield user?). US-5 AC-1 asserts behavior ("inbound messages route as that agent") that this spec disclaims implementing. | Either (a) drop the behavioral assertion from US-5 (persist-only, no routing claim, and remove "route as that agent" from the AC), or (b) specify the wiring from `identity` → routing. Also reconcile with Spec-3/4's agent-reference shape (cross-spec coupling, see F-18). |
| F-13 | MINOR | Inconsistency | Traceability Matrix | Row "FR-2.5 (identity) / US-5 / 'round-trips' / #1" maps identity to the config round-trip test, but identity persistence is not exercised by `TestChannelsConfig_InstanceMap_RoundTrip` as described (that test asserts `Type`, not `identity`). US-5 has no dedicated test; FR-2.7 traces only to an e2e. | Add an explicit identity-persistence assertion to test #1 (or a new unit test) and a non-e2e test for FR-2.7's cap-1 UI guard, so P0/P1 requirements aren't e2e-only. |
| F-14 | MINOR | Incompleteness | §6 TDD, Regression | "port their enable tests to the loop" — the 13 channels' existing enable tests likely assert on the typed config shape and the if-ladder branches; porting is non-trivial and unscoped. No estimate of which tests break. | Inventory the existing channel enable/init tests that reference typed fields and list them as regression work, mirroring F-3's reader inventory. |
| F-15 | MINOR | Inoperability | §10 Holdout, FR-2.6 | No observability specified for the email poll loop (metrics/logs for poll cycles, messages fetched, auth failures) and no runbook note for "email stopped receiving." Degraded-boot is surfaced in Connectors, but a *running* poll that silently stops (token expired mid-run) has no signal. | Specify structured logs for poll cycles + a degraded transition when N consecutive polls fail, surfaced in Connectors (reuse the existing `degraded`/`degraded_reason` `ChannelEntry` fields). |
| F-16 | MINOR | Overcomplexity | FR-2.4, identity object | `identity: {kind: agent|user, id}` introduces a 2-field object now, but the spec admits routing isn't wired and v0.1.0 is single-user. At cap 1 + single user, `identity.kind:user` always means the one user. Is the `id` sub-field load-bearing in v0.1.0, or speculative shape for v0.3? | Confirm whether `identity.id` is used in v0.1.0. If not, either justify it as a deliberate ADR-blessed forward shape (NFR-1 "shape now, behaviour later") and say so explicitly, or simplify to `identity.kind` only and add `id` when routing lands. (Note: the ADR's NFR-1 may legitimately justify keeping it — but the spec must state that, not leave it implicit.) |
| F-17 | OBSERVATION | Overcomplexity | overall | Bundling the breaking config migration + the 13-factory rewrite + a brand-new network channel (email) + a UI in one spec/PR maximizes blast radius for the single most dangerous change in v0.1.0. | Consider sequencing: land the typed→map migration + loop + contract delta + Connectors UI first (behaviour-preserving, fully testable), then email as a follow-up spec/PR. Email's unresolved dep question (F-2) is reason enough not to gate the migration on it. |
| F-18 | OBSERVATION | Cross-spec | §Depends-on, US-5, F-12 | The `identity` shape here (`{kind:agent|user, id}`) must match the agent-reference shape Spec-3/4 defines, and binds to the Spec-1 `Workspace` key — but the spec only references these in prose, with no concrete shared schema. | Phase 3.5 cross-spec check must verify `identity.kind:agent` + `id` is byte-identical to Spec-3/4's agent-reference object, and that the Connection→Workspace binding key matches Spec-1's renamed `Workspace` field. Flag if Spec-3/4 isn't drafted yet (this spec can't finalize `identity` before then). |
| F-19 | OBSERVATION | Insecurity (STRIDE) | email, configureChannel | SMTP/IMAP plaintext-auth over cleartext (no enforced TLS) = credential disclosure on the wire (Info Disclosure). No rate-limit on the Connectors test endpoint (DoS via repeated IMAP connects). | Require IMAPS/SMTPS or STARTTLS in FR-2.6; reuse the existing auth-endpoint rate-limiting (`withRateLimit`) on the test endpoint. |

---

## Phase 1 — Structural Integrity (plan-spec mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | PASS | US-1..US-8 all have ACs |
| Every AC has ≥1 BDD scenario | **FAIL** | US-8 (Connectors UI) has only an e2e line, no Gherkin; US-5 identity has no dedicated BDD |
| Every BDD has a `Traces to:` | PASS | all 8 scenarios traced |
| Every BDD has a corresponding TDD test | PARTIAL | "IMAP unreachable" → #6 ok; no BDD for cap-at-load (F-8) |
| Every FR in traceability matrix | PASS | FR-2.1..2.9 present (2.9 greenfield only in prose, not matrix — MINOR) |
| Every BDD in traceability matrix | PARTIAL | identity scenario folded into #1 questionably (F-13) |
| Datasets cover boundary/edge/error | PARTIAL | missing: auth-failure (vs unreachable), duplicate-id, two-enabled-at-load, non-enum instance id |
| Regression impact explicitly addressed | **FAIL (under-scoped)** | names "the 13 channels" but omits `emitGHSARemovalWarn`, `countEnabledChannels`, config normalizer, factory-signature change (F-1, F-3) |
| Success criteria measurable, no subjective language | PASS | SC-1..SC-7 are measurable |

---

## Phase 3 — Test Coverage Assessment

- **Missing negative tests:** auth-failure (wrong IMAP/SMTP password) distinct from host-unreachable (F-11); non-enum instance id secret routing (F-4); duplicate instance id (F-10); two enabled instances of a type at config load (F-8).
- **Grep-guard soundness (Test #7):** not false-positive-free as written — will trip on `pkg/migrate/sources/openclaw` and `config_old.go` (F-7). This is the specific item the brief asked to verify, and it fails.
- **Factory-rewrite regression (Test #5/SC-7):** "13 channels unchanged" is the wrong claim; the regression set must cover 13 rewritten constructors, not 13 untouched ones (F-1, F-14).
- **Contract dry-run (Test #8):** the ADR explicitly lists a "dry-run contract regen for the Connection-instance delta" as a Spec-2 experiment; the spec asserts `verify-contracts` exits 0 but contains no regen diff and ignores the `ChannelId` enum / `additionalProperties:false` / required-field interactions (F-5).

---

## STRIDE Threat Summary

| Component | Threats |
|---|---|
| Email IMAP/SMTP transport | **Info Disclosure** — plaintext creds without enforced TLS (F-19); **DoS** — unbounded poll/connect, no test-endpoint rate-limit |
| `configureChannel` (instance-keyed) | **Tampering/Info Disclosure** — instance id not a valid `ChannelId` → `channelSensitiveFields` miss → plaintext secret persisted inline (F-4, SEC-23 regression) |
| Connectors UI cap enforcement | **EoP/bypass** — API-only cap; hand-edited config bypasses (F-8) |
| Credential refs per instance | **Collision** — `channelCredKey(type,…)` shared across instances of a type if id≠type (forward-compat hazard, F-4) |

---

## Unasked Questions

1. Does `ChannelFactory` change signature, or do constructors read `cfg.Channels[id]`? (Unanswerable from the spec; it's the central design decision — F-1.)
2. Which exact pure-Go IMAP lib + version, and is the Constraint-#1 exception ADR-approved? (F-2.)
3. Is instance id == type at cap 1 (keeping cred keys/URLs stable), or a separate generated id? (F-10.)
4. What is the email poll interval, seen-state store, and TLS requirement? (F-11, F-19.)
5. What does `identity.kind:user` resolve to for routing in a single-user greenfield install, and does this spec wire it or not? (F-12.)
6. Where is the reader inventory + contract-regen dry-run the ADR mandated as Spec-2 experiments? (F-3, F-5.)

---

## Verdict

**BLOCK** — 5 CRITICAL findings. The spec cannot proceed to `/taskify` until at least F-1 (factory contract), F-2 (IMAP dep vs Constraint #1), F-3 (full reader inventory), F-4 (instance-keyed secret routing), and F-5 (concrete contract delta incl. `ChannelId` enum) are resolved.

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/v01-spec2-connections-instance-email-spec.md docs/internal/specs/v01-spec2-connections-instance-email-spec-review.md
```
