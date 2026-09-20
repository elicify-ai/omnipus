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
| 5 | codemod-spacing-css + codemod-spacing-arbitrary + codemod-spacing-tailwind-class | the whole spacing family | 258 files, 4101 edits, 291 refusals; all three re-run to 0 | exit 0 | full suite 10013/10013 | PASS, 0 errors; debt 2786 -> 666 after rebuild, 0 unsupported | this commit |
| 5b | ledger + baseline rebuilt | stale fingerprints from row 5 and the wrapper-layer scope fix | tracked sequence | — | — | PASS; 662 entries, 0 unresolved, 0 blocking | this commit |
| 6 | codemod-colour-exact | literals byte-identical to exactly one registered token | 5 files, 23 edits, 625 refusals; re-run plans 0 | exit 0 | theme + 7 related suites green; codemod test 19/19, 0 skipped | 22 debt fingerprints removed, 0 unsupported | 3ba92bfce |
| 6b | ledger + baseline rebuilt | row 6, the role-button lock, and the two published components | tracked sequence | — | locks 1779/1779; unit 514/514 | PASS, 0 errors; 666 -> 651 entries, 0 unresolved, 0 blocking | 7149e1775 |
| 7 | codemod-safe-area-fallback | safe-area env() missing its required closed-scale fallback | 9 files, 17 edits, 2 refusals (both comments); re-run plans 0 | exit 0 | 184 tests across 11 suites; codemod test 11/11, 0 skipped | PASS, 0 errors; 651 -> 639 entries, 454 exceptions | af9315e39 |

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

## Decisions waiting on the founder

These are the remaining C1 items that a script refused rather than guessed. Each one changes what a user sees, so none of them is a mechanical fix.

| # | Decision | What changes on screen | Why it is not mechanical |
|---|---|---|---|
| D-A | Task status colours (`design-system/status-mismatch`, 18 findings) | "Next" gold `#D4AF37` -> blue `#3B82F6`; "In progress" amber `#EAB308` -> gold `#D4AF37`; "Blocked" amber `#EAB308` -> orange `#F97316` | The code and the registered status contract disagree. Applying the contract is a visible recolouring of the task board, not a token swap. `codemod-status-source --mismatch` is written and previews 12 edits across 3 files; it is deliberately NOT applied. |
| D-B | `--color-cancelled` in `globals.css` | Cancelled chips orange `#F97316` -> amber `#EAB308` | `globals.css` declares Blocked-orange where the registered cancelled token is amber. A real rendered-colour bug; fixing it is a visible change and interacts with D-A. |
| D-C | Agent `draft` badge | Unchanged until decided | Agent status is a third status domain outside the seven-state task/plan table. Needs a ruling on whether it joins the contract. |
| D-D | Dismissed question threads (`AskUserQuestionCard`) | Unchanged until decided | A cancelled thread renders muted rather than Cancelled-amber. May be a deliberate inert/historical treatment; product decision. |
| D-E | Root-font-dependent spacing (`spacing/root-dependent`, 36 findings) | Nothing today; changes behaviour when a user enlarges their browser font | `rem` follows a user-adjusted root font; a px token does not. Converting is an accessibility trade-off, not a cleanup. |
| D-F | Remaining raw colours (`ts-colors/raw-color`, 403 findings) | Varies | Mostly governed data rather than chrome — avatar colours, file-type icons, diagram themes. Tailwind v4 regenerated its palette in OKLCH, so its named colours no longer equal our brand hexes; every swap is a visible change. Needs splitting into clusters and deciding per cluster. |

| D-G | The 3px indents (`spacing/off-scale`, 14 sites) | `ml-[3px]` becomes either 2px or 4px | 3px is exactly equidistant between the 2px and 12px rungs added on 2026-09-20, so the mapping tool refused the tie. D10's own wording ("next grid multiple", which gave 1.75->2 and 10.5->12) reads as 4px, but the tool tie-broke downward to 2px. A 1px call either way, in chat metadata indents. |
| D-H | `fontSize: 9` in `ChartPart.tsx` | A chart axis label grows from 9px to 12px | The 12px floor is ratified and has been applied to 324 other sites, but a chart label is data visualisation rather than UI chrome, and +3px may reflow the chart. One site. |

## Ready to implement — analysis done, no ruling needed

| Item | Finding | Resolved mapping |
|---|---|---|
| Tree indent custom properties | `spacing/invalid-var`, 4 sites (`Sidebar`, `FileTreeView`, `KnowledgeOutline`, `SearchModal`) | Each sets a pixel number in JS and multiplies by `1px`: `calc(var(--x-indent-depth-px) * 1px)`. The step is 14px and the base 12px -- the legacy 14px-root values D10 already maps to 16px and 12px. The fix is to set a unitless depth COUNT and multiply by tokens: `calc(var(--space-2-5) + var(--depth) * var(--space-4))`. That is the same normalisation already applied to `px-4`/`p-4` in 87 places, so it needs no new decision -- but it is a structural change to nested tree indentation in four files, and there is no visual baseline to catch a regression (issue #753), so it wants care rather than a bulk edit. |

## Deferred, tracked elsewhere

- **Appearance gate.** No visual-regression coverage exists: `toHaveScreenshot` appears zero times, there are no snapshot baselines, and the design-system Playwright config sets `screenshot: 'off'`. The only look check is a human. Issue [#753](https://github.com/elicify-ai/omnipus/issues/753), explicitly out of scope for this delivery.
- **Design-system skill.** The rules are enforced mechanically but discoverable only by failing CI. Issue [#754](https://github.com/elicify-ai/omnipus/issues/754) covers an `omnipus-design-system` skill plus the `CLAUDE.md` wiring that requires loading it before frontend work.
