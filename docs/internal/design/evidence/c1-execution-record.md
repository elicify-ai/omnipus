# C1 execution record

Live record of the C1 repair batch (foundations: tokens, 12px floor, 4/8px spacing, status colours, touch foundations). One row per applied script or change, in the order applied. Every row states the gate results that were actually observed.

**Start state:** HEAD `040caf34d`, Stage B installed, checkpoint-B audit PASS (0 errors), 3,451 recorded breaches (C1 3,269 · C2 133 · C5 49), 0 unsupported.

## Order of application

Scripts are applied ONE AT A TIME by the lead, because 255 of the 302 affected files (84%) carry breaches from more than one rule family. Authoring and verification run in parallel; application does not.

| # | Script or change | Items targeted | Applied | Typecheck | Tests | Audit diff | Commit |
|---|---|---|---|---|---|---|---|
| 1 | codemod-status-source --literal | 13 ledger items (status hexes -> the governed source) | 2 files, 15 edits, 0 refusals; re-run plans 0 | exit 0 | 707/707 related | PASS, 0 errors; debt 3451 -> 3380 (71 removed, 0 added), 0 unsupported | see below |
| 2 | codemod-type-properties (main pass) | weight, line height, letter spacing, family, plus the inert line-height in 12 logo files | 34 files, 51 edits, 16 refusals; re-run plans 0 | exit 0 | 3068/3068 related | PASS, 0 errors; debt 3341 (39 removed, 0 added), 0 unsupported | this commit |
| 3 | codemod-type-scale-css | text sizes and families in CSS files and inline styles | 9 files, 36 edits, 10 refusals; re-run plans 0 | exit 0 | 2332/2332 related | PASS, 0 errors; debt 3321 (20 removed, 0 added), 0 unsupported | this commit |
| 4 | codemod-type-scale-tailwind | Tailwind text sizes, incl. the 12px floor | 246 files, 2033 edits, 20 refusals; re-run plans 0 | exit 0 | full suite 9979/9982; 1 load-induced timeout, 16/16 twice isolated | PASS, 0 errors; debt 3451 -> 2786 after the ledger rebuild | this commit |
| 4b | ledger + baseline rebuilt | stale fingerprints from rows 1-4 cleared | tracked sequence: verify -> merge -> build -> assemble | — | — | PASS, 0 errors; 2786 entries, 0 unresolved, 0 blocking | this commit |

**Row 1 note — applied twice.** The first attempt was reverted by this gate: it produced 17 `unsupported` findings, because neither scanner could read `statusContract.<status>.resolvedColor`, the governed source the repair moves code onto. Fixed in commit fc33bb643 (both scanners now resolve that read structurally, and a second dispatcher handling the `${color}1a` tint idiom was wired in too), then re-applied byte-identically. Three tests compared hex strings exactly and failed on letter case alone (`#9CA3AF` vs `#9ca3af`, the same colour); they now compare case-insensitively.

**Ledger refresh policy during C1.** Each applied repair removes debt, so the installed baseline lists fingerprints that no longer exist. The audit still passes (a resolved fingerprint is not an error), but a stale baseline could silently re-accept a regression of the same value. The ledger and baseline are therefore rebuilt through the tracked install sequence at C1 close, and that rebuild is itself a row in this record.

## Gate for every row

1. Preview run: planned edits and refusals recorded.
2. Apply, then re-run: must plan zero further edits (safe to re-run).
3. `npm run typecheck` exit 0.
4. Related tests green; any failure root-caused before the next row.
5. Audit with committed scanners: exactly the targeted findings gone, no new finding, no debt fingerprint removed without a probe.
6. Commit, authored by the human, referencing this record.

## Batch close

C1 closes when: the four C1 families report zero debt, the four locks are switched to blocking, the touch check passes before and after, `/code-review high` is clean, and the founder has looked at the running app (this batch changes text sizes and spacing visibly, by prior approval).

## Findings that change later batches

Recorded during C1 preparation; each was proven, not assumed.

| Finding | Consequence |
|---|---|
| The 53 `controls/shadcn-low-level-import` items are NOT a catalog data fix. `catalog.json`'s public export list is validated as a mirror of `src/index.ts`, so any entry not re-exported there fails as "stale"; value exports also need a manifest; and `domain`/`application`-classified modules are blocked from the public list by design (D8), which `library-package.check.mjs` enforces by name. Proven by adding `BrandIcon`, watching `npm run test:design-system:unit` fail, and reverting. | Real C2/C5 work: route the 14 files importing `AlertDialog` directly onto the public `ConfirmDialog`; promote `DateTriggerButton` (already in its manifest, two lines from ready); decide the 28 domain/application items with the architect. Not a C1 script. |
| The 15 `controls/radix-import` items are all inside our own `src/components/ui/` wrapper layer, where importing Radix is the point. The lock has no way to accept them: only `*/extension-boundary` rule ids may be ledger exceptions, and reviewed boundaries cover only test, story and generated-token paths. | Needs a scope decision before C6 can empty the exception list: either the lock stops firing inside the sanctioned wrapper layer, or a reviewed-boundary kind is added for it. |
| Colour is not a mechanical family. Tailwind v4 regenerated its palette in OKLCH, so `text-red-400` and friends no longer equal our brand hexes — every such swap is a visible change, not a no-op. Most remaining colour debt is governed data (avatar colours, file-type icons, diagram themes) rather than chrome. | The colour script must be split: exact-match token swaps only; everything else is a decision batch. |
| `--color-cancelled` in `globals.css` is `#F97316`, which is Blocked-orange, not the registered cancelled colour `#EAB308`. | A real rendered-colour bug, to fix deliberately rather than inside a bulk swap. |
