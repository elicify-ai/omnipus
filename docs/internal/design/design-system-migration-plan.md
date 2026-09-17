# Omnipus Design System — Migration Plan

**Status:** Delivery plan for the target-state constitution, amended 2026-09-17. Small bounded visual changes are in scope. **The founder judges “no redesign” by eye — there is no screenshot suite and no photo baseline.**

**Target state:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/design/design-system-definition.md`

**Program model:** One big-bang migration program, with controlled internal work packages and one final conformance cutover. Small approved normalizations land in that same cutover. Two live versions of a foundation are not an accepted end state.

## 1. Outcome and governing rule

The SPA moves to the complete design system in one dedicated program. Product-wide adoption is not left to “touch it when nearby” work. Internal work packages reduce risk and expose defects early, but do not redefine the decision as gradual adoption: completion occurs only after the final application package lands, every gate passes, and the temporary exception ledger is empty.

Green lint alone is insufficient. The program must preserve intended behavior, reach the actual route inventory, and pass visual, interaction, accessibility, and bundle checks.

The current rendered application is the accepted visual baseline. Every work package declares its visual delta as Invisible, Normalization, or Redesign Risk. The approved normalizations (12px floor, status-colour unification, 4px / 8px spacing snap) are required work. Anything outside that table is Redesign Risk and stays out unless the founder approves it.

**Visual continuity is founder-judged.** Do not photograph the app as a start gate, and do not run a screenshot regression suite as a completion gate. Encode the rules, enforce them in CI, then repair what does not comply. The founder looks at the running app and says whether it still looks like Omnipus.

The definition's **E1** locks are part of the deliverable, not a later cleanup. A change that invents a colour, a sub-12px label, a homemade button, a one-off gap, or a second status palette must fail CI once that lock is enabled. Each lock ships with a seeded fixture that proves it can fail.

## 1a. Sequence (how we actually work)

The rulebook is already written. This program is: encode it, lock it, repair what does not follow.

| Order | Work | Done when |
|---|---|---|
| Already done | Rulebook (definition) | Colours, 12px floor, 8px spacing, status map, homemade-button rule |
| **A. Encode** | Put the rulebook into tokens, shared parts, and development verification | Typed tokens use the D3 hex table. The full D8 catalog is classified. Existing `Button`, `Badge`, `Dialog`, `Sheet`, `Switch`, `Checkbox`, `Progress`, `SkeletonList`, `EmptyState`, `ErrorState`, and `QueryErrorState` contracts are recorded. `IconButton`, `ConfirmDialog`, `Field`, `CollectionState`, `JobStatus`, and shared `Skeleton` are built or completed. The curated public export map and Storybook manifests exist. |
| **B. Enforce** | Implement every E1 lock without blocking existing debt | Seeded failing and passing fixtures prove the colour, sub-12px, off-grid spacing, second-status-palette, raw button/dialog/confirm/switch, and public-component-manifest locks. All begin in audit/new-violation mode. Storybook interaction and axe checks pass for public parts. |
| **C. Repair** | Change existing application code until it complies, activating locks batch by batch | Each matching lock becomes repository-blocking when its C batch reaches zero debt. The exception ledger is empty at C6. |
| **D. You look** | Running app | You say it still looks like Omnipus. No photo suite |

Storybook is required development verification before screen conversion. It is not a screenshot baseline and it is not included in the production binary.

### Repair batches (C), in this order

| Batch | Fix | Why this order |
|---|---|---|
| C1 | Tokens, 12px floor, 4px / 8px spacing, status colours; then activate those four locks | Everything else sits on this |
| C2 | Raw buttons, dialogs, confirms, switches; then activate the raw-control lock | Destination parts must exist first |
| C3 | Sheets, forms, overlays | Needs Field / Dialog / Confirm |
| C4 | Empty, error, loading, save, long-running | Needs the composites |
| C5 | Domain widgets out of `ui/`; public-import cleanup | After callers use the official parts |
| C6 | Verify every lock is repository-blocking; empty the exception list | Cutover |

Usability work (empty Board, Library, pickers, task panel, Knowledge) is **not** this program. It starts after C6.

## 2. Cutover inventory before repair

Encode starts immediately. It does not wait for an exhaustive audit.

Before application repair, create a lightweight, checked-in route inventory covering routes, redirects, modal-only entries, and major tabs. This inventory is the cutover denominator. CI must fail when a listed surface has no required verification mapping. The inventory file itself is created during Encode; this plan does not invent its contents.

Each lock or repair batch produces its own debt count, exclusions, accessibility evidence, and delivery measurements when that batch starts. Known audit counts are planning evidence only, not an up-front completion baseline.

## 3. Program order

The order is mandatory: converting screens before destination contracts stabilize creates rework.

### Phase 1 — Foundations and tokens

Deliver:

- the primitive → semantic → conditional component → CSS graph, seeded with the hex table in D3 (today’s rendered values);
- status contracts using the D4 map only (task palette wins; calendar and graph consume the same hexes); colour exception registry;
- complete typography with 12px floor and adjustable 12–20px root;
- one 4px / 8px spacing scale (mapping today's 14px-root rem steps), breakpoints, optional density modes, and touch adaptation;
- radius, border, elevation, shadow, z-index and overlay order;
- motion and reduced-motion policy;
- icon, content, localization, and data-visualization rules;
- generated typed tokens for TypeScript;
- PostCSS/Stylelint graph parsing, AST-aware color checks, and seeded self-tests.

**Exit gate.** No token cycles, undefined references, or forbidden edges; typed output matches CSS; the D4 status map and tokens exist; contract checks pass. The colour, type-floor, spacing, and second-status-palette locks have failing and passing fixtures and run in audit/new-violation mode. Calendar, board, list, and graph screens are not converted here; that work is C1.

### Phase 2 — Primitive and composite contracts

Classify the catalog and prepare the public boundary. Do not move application domain widgets yet; that work is C5. Complete:

- `Button`, `IconButton`, `Field`, `Dialog`, `ConfirmDialog`, `Sheet`, `Switch`, `Skeleton`, `EmptyState`, `QueryErrorState`, `CollectionState`, `Progress`, and `JobStatus`;
- accessible names, button types, hit regions, focus, scroll/focus restoration, form wiring, applicable states, and reduced motion;
- named Sheet sizes and one confirmation dismissal contract;
- curated `@omnipus/ui` export map that excludes domain widgets without relocating them yet;
- AST-aware syntax and import-boundary rules with explicit low-level directories (E1 raw-control lock);
- characterization tests for behaviors at risk during replacement;
- the exists-vs-build catalog in D8 completed: new contracts (`IconButton`, `ConfirmDialog`, `Field`, `CollectionState`, `JobStatus`, shared `Skeleton`) green before any screen conversion.

**Exit gate.** Each destination component passes its manifest, unit, axe, keyboard, interaction, pointer, reduced-motion, and applicable browser tests. Export tests keep domain widgets private. No source pattern is replaced before its destination contract is green. The raw button/dialog/confirm/switch lock and public-component-manifest lock have failing and passing fixtures and run in audit/new-violation mode.

### Phase 3 — Storybook as verification front door

Build:

- one coverage manifest per public component;
- stories for every variant, size, applicable state, density, and relevant viewport;
- keyboard and accessible-name interaction tests;
- axe checks and interaction checks;
- manifest-to-export and manifest-to-story validation;
- separate `dist/storybook` output.

**Exit gate.** Every public part has its required manifest, stories, interaction checks, and axe checks before screen conversion. Storybook builds from root `devDependencies`; production source has no `.storybook` or story imports; the production bundle contains no Storybook module; embedded assets contain no Storybook payload. This is development verification, not a screenshot baseline.

### Phase 4 — Big-bang application migration, last

Convert the complete application onto the finished foundations and contracts as one program. Work may use reviewable internal batches by pattern and route family, but partial batches are not an accepted final state and do not establish an indefinite mixed system.

Internal order:

1. Mechanical token, typography, motion, spacing, and status replacements.
2. Raw controls and low-level imports onto primitives.
3. Confirmations, sheets, forms, and overlay behavior onto composites.
4. Collection, action, field, save, and long-running state presentations.
5. Domain-component relocation and public-import cleanup.
6. Route-by-route behavioral, visual, keyboard, and accessibility verification.
7. Remove every temporary exception and verify all final gates are repository-blocking.

**Final cutover gate.** The route inventory is verified, all completion metrics pass, the temporary ledger has zero open entries, and production build/bundle checks preserve the single-binary and no-runtime-dependency constraints. The founder signs off that the app still looks like Omnipus. No screenshot suite.

## 4. Big-bang risk controls

### Characterization before replacement

Record current user-observable behavior for:

- form submission and implicit Enter behavior;
- focus entry, trap, return, and restoration;
- Escape, overlay-click, and destructive-confirm dismissal;
- preserved local state and unsaved changes;
- keyboard ordering and roving focus;
- background scroll and overscroll containment;
- calendars, graphs, editors, live-browser, and other third-party controls;
- URL-backed navigation state;
- asynchronous announcements and retry/cancel/background actions.

Characterization does not canonize a defect. The ledger marks each behavior as preserved, intentionally corrected by the constitution, or separately approved.

### Verification after every internal batch

Each batch passes its narrow checks before another depends on it. Syntax gates never stand in for visual or behavioral evidence. Applicable checks include unit/type tests, Storybook interaction and axe tests, route browser tests, keyboard-only flows, representative screen-reader checks, forced colors, reduced motion, 200% zoom, 320px reflow, pointer hit-region tests, and production asset comparison. A one-off image may explain a visual delta, but is optional and never a gate.

### Merge-conflict control

Freeze or coordinate high-churn UI work during final conversion. Assign ownership by route family and shared component. Shared contracts land before consumers, and consumer batches do not independently alter them. Rebase and rerun affected routes after conflict resolution.

### Rollback and diagnosis

Align commits or pull-request units to observable patterns so a regression can be isolated without restoring the old architecture. Preserve behavioral and test artifacts. A rollback that restores an old pattern reopens its ledger entry; it cannot silently become permanent.

## 5. Exception ledger

There are two kinds:

1. **Permanent governed exceptions** in constitutional registries: user-authored colors, QR/document surfaces, syntax highlighting, and data visualization.
2. **Temporary migration exceptions**, which must reach zero before completion.

Temporary entries use:

| Field | Requirement |
|---|---|
| ID | Stable identifier |
| Surface | Absolute file path plus route/component |
| Rule | Exact violated decision or lint rule |
| Reason | Concrete blocker; “legacy” is insufficient |
| Risk | User-visible, accessibility, behavior, security, or delivery impact |
| Visual delta class | Invisible, Normalization, or Redesign Risk, plus the approval reference for every non-Invisible delta |
| Owner | Named person or workstream |
| Characterization | Test/evidence protecting current behavior |
| Replacement | Destination token/component/contract |
| Expiry | Date or program checkpoint |
| Status | Open, blocked, resolved, or approved permanent transfer |
| Proof | Test or review evidence closing it; an explanatory image is optional and never a gate |

Every lint suppression and allow-list entry maps to a ledger ID. Expired entries fail CI. Permanent transfer requires explicit review and registry addition. The program cannot complete with any open or blocked temporary entry.

## 6. Verification gates

### Per-lock activation matrix

Commands and CI jobs below are names to wire during Encode. Every violating fixture must exit non-zero; every permitted fixture must exit zero. The program and CI own all rows. Temporary exceptions require a live ledger ID; permanent exceptions require the matching constitutional registry entry.

| Lock | Owner | What it forbids and covers | Fixture that must fail | Fixture that must pass | Command / CI job | Repository-blocking |
|---|---|---|---|---|---|---|
| Colour | This program / CI | Unregistered colours in CSS, utilities, JSX attributes, style objects, SVG values, and class builders | Raw system hex in each covered syntax | Token and registered colour exception | Stylelint + AST-aware ESLint / design-system locks | After C1 |
| Sub-12px type | This program / CI | Computed UI text below 12px and arbitrary text-size utilities | 10px text and an arbitrary size utility | Named 12px role | Stylelint + ESLint + computed-style check / design-system locks | After C1 |
| Off-grid spacing | This program / CI | Any unregistered spacing outside the closed 4px / 8px scale | 6px, 10px, and a current 7px rem-derived gap | Registered scale token, hairline, and 1px border | Stylelint + token-graph / design-system locks | After C1 |
| Second status palette | This program / CI | Status hexes outside D4 across CSS and TypeScript presentation paths | Alternate `in progress` or `blocked` hex | D4 status token | Stylelint + AST-aware ESLint + token-graph / design-system locks | After C1 |
| Raw controls | This program / CI | Raw `<button>`, `<dialog>`, `window.confirm`, or checkbox-as-switch in feature code | One fixture for each forbidden control | Named primitive and approved low-level wrapper | AST-aware ESLint / design-system locks | After C2 |
| Public-component manifest | This program / CI | A curated public export without its coverage manifest, stories, interaction checks, and axe checks | Export missing one required mapping | Fully mapped public export | Manifest validator + Storybook checks / design-system verification | Enabled after Phase 2; blocking before C1 |

| Gate | What it proves | What it does not prove |
|---|---|---|
| Token graph parser | Defined references, legal edges, no cycles | Visual quality or contrast alone |
| Stylelint/ESLint/import rules | Concrete syntax and dependency boundaries | Semantic equivalence of arbitrary markup |
| Source-search self-tests | Guards detect seeded examples and known names | Every possible reimplementation |
| Type/unit tests | API and logic contracts | Browser rendering and assistive behavior |
| Storybook build | Catalog compiles | Coverage, accessibility, or visual correctness alone |
| Manifest validation | Public exports have declared stories/tests | Quality of assertions |
| Axe | Detectable automated accessibility faults | Full WCAG or screen-reader usability |
| Keyboard/browser tests | Focus, input, reflow, motion, pointer behavior | Every assistive technology |
| Screen-reader checks | Representative spoken flow and naming | Exhaustive platform coverage |
| Founder look | Still Omnipus, including approved small deltas | Mechanical token correctness |
| Production bundle audit | Storybook is absent and size delta known | Runtime behavior alone |

## 7. Completion metrics

Each batch records numerator, denominator, exclusions, command, and artifact for its metrics. Counts are exception-adjusted: registered legitimate cases remain reported separately, not silently deleted.

| Measure | Completion rule |
|---|---|
| Route coverage | The checked-in inventory is the denominator. Every listed route, modal-only entry, and major tab has the checks required for its surface type; CI fails any missing inventory-to-check mapping. |
| Adoption by category | 100% of in-scope foundations, primitives, composites, domain components, layouts, and feature surfaces conform |
| Duplicate patterns | Zero unregistered duplicate confirm, empty, error, skeleton, save, switch, sheet-width, or state implementations |
| Token integrity | Zero undefined tokens, cycles, forbidden edges, or unregistered raw system colors; typed output matches CSS; D3 hex table is the primitive source |
| Typography | Zero computed UI text below 12px and zero unregistered arbitrary type values |
| Spacing | Zero unregistered spacing outside the closed 4px / 8px scale. Hairlines and 1px borders are the only built-in exceptions. Current 7 / 14 / 21px rem-derived gaps are examples of debt, not the whole forbidden set. |
| Status | Zero second palette; calendar/board/list/graph match the D4 map |
| Library boundary | Zero unregistered raw named-job elements or forbidden low-level imports in feature code |
| E1 locks | Every lock has failing and passing fixtures in audit/new-violation mode before Phase 4. Each becomes repository-blocking at the batch named in the activation matrix; C6 verifies all remain active. |
| Contrast/status | 100% of status combinations pass required contrast and distinction checks |
| Axe | Zero serious/critical violations; lesser findings resolved or explicitly accepted through permanent governance |
| Keyboard flows | Every keyboard check required by the checked-in inventory and public-component manifests passes |
| Zoom/reflow | Every applicable inventory mapping passes at 200% zoom and 320px without loss of content/function |
| Forced colors/reduced motion | Every applicable inventory and public-component mapping passes |
| Visual continuity | Founder sign-off on the running app. No screenshot suite |
| Storybook coverage | 100% of curated public exports have complete applicable manifests, stories, assertions |
| Bundle delta | Zero Storybook modules in production; embedded delta measured, explained, within approved budget |
| Temporary exceptions | Exactly zero open or blocked entries |
| Permanent exceptions | 100% documented with owner, rationale, scope, and automated boundary where feasible |

## 8. Completion report

The final report states two conclusions separately:

1. **Code correct and tested:** all gates passed, with commands, exit codes, and artifacts.
2. **Reachable by users:** every inventoried route and named job uses the system, and representative user and assistive-technology flows were executed.

It also records before/after counts, permanent exceptions, production bundle delta, Storybook install/CI cost, browser matrix, and any founder-approved deferral. Without both conclusions, the program is not complete.

## 9. Source evidence

- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/ui-audit-2026-09-17/lane-a-tokens.md`
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/ui-audit-2026-09-17/lane-b-library.md`
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/ui-audit-2026-09-17/lane-c-implementation.md`
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/ui-audit-2026-09-17/lane-d-consistency.md`
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/brand/brand-guidelines.md`
