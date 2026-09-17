# Omnipus Design System — Migration Plan

**Status:** Delivery plan for the target-state constitution, 2026-09-17.

**Target state:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/design/design-system-definition.md`

**Program model:** One big-bang migration program, with controlled internal work packages and one final conformance cutover.

## 1. Outcome and governing rule

The SPA moves to the complete design system in one dedicated program. Product-wide adoption is not left to “touch it when nearby” work. Internal work packages reduce risk and expose defects early, but do not redefine the decision as gradual adoption: completion occurs only after the final application package lands, every gate passes, and the temporary exception ledger is empty.

Green lint alone is insufficient. The program must preserve intended behavior, reach the actual route inventory, and pass visual, interaction, accessibility, and bundle checks.

## 2. Baseline before work starts

Capture a reproducible baseline and distinguish measured facts from review hypotheses.

| Baseline | Required record |
|---|---|
| Route inventory | Every route, redirect, modal-only entry, and major tab; never substitute 317 non-test TSX files for routes |
| Source inventory | Counts by foundations, primitives, composites, domain components, layouts, routes, and tests |
| Token debt | Undefined tokens, forbidden edges, raw colors, arbitrary type/spacing/motion values, approved exceptions |
| Pattern debt | Raw buttons/dialogs/confirms/switches; duplicate empty/error/skeleton/save/confirm/sheet patterns; low-level imports |
| Accessibility | Axe, keyboard flows, 200% zoom, 320px reflow, forced colors, reduced motion, representative screen-reader findings |
| Visual evidence | Stable screenshots for representative routes, states, overlays, dense tables, forms, long-running work |
| Delivery footprint | Production and embedded SPA asset sizes, dependency graph, build time, install/CI impact |

Known audit facts include 317 non-test TSX files potentially affected, 666 arbitrary text-size classes with 616 below 12px, 98 raw-button files without a `Button` import plus 38 mixed-use files, 352 inline-style occurrences across 77 files including legitimate dynamic values, and four confirmation mechanisms. Recount with finalized exclusions before using any number as a completion baseline.

## 3. Program order

The order is mandatory: converting screens before destination contracts stabilize creates rework.

### Phase 1 — Foundations and tokens

Deliver:

- the primitive → semantic → conditional component → CSS graph;
- status contracts and explicit color exception registry;
- complete typography with 12px floor and adjustable 12–20px root;
- spacing/grid, breakpoints, density and touch adaptation;
- radius, border, elevation, shadow, z-index and overlay order;
- motion and reduced-motion policy;
- icon, content, localization, and data-visualization rules;
- generated typed tokens for TypeScript;
- PostCSS/Stylelint graph parsing, AST-aware color checks, and seeded self-tests.

**Exit gate.** No token cycles, undefined references, or forbidden edges; typed output matches CSS; status contracts pass contrast/distinction checks; browser tests pass at root minimum/default/maximum, 200% zoom, and 320px.

### Phase 2 — Primitive and composite contracts

Classify the catalog and move domain widgets out of the primitive namespace. Complete:

- `Button`, `IconButton`, `Field`, `Dialog`, `ConfirmDialog`, `Sheet`, `Switch`, `Skeleton`, `EmptyState`, `QueryErrorState`, `CollectionState`, `Progress`, and `JobStatus`;
- accessible names, button types, hit regions, focus, scroll/focus restoration, form wiring, applicable states, and reduced motion;
- named Sheet sizes and one confirmation dismissal contract;
- curated `@omnipus/ui` export map;
- AST-aware syntax and import-boundary rules with explicit low-level directories;
- characterization tests for behaviors at risk during replacement.

**Exit gate.** Each destination component passes its manifest, unit, axe, keyboard, interaction, pointer, reduced-motion, and applicable browser tests. Export tests keep domain widgets private. No source pattern is replaced before its destination contract is green.

### Phase 3 — Storybook as verification front door

Build:

- one coverage manifest per public component;
- stories for every variant, size, applicable state, density, and relevant viewport;
- keyboard and accessible-name interaction tests;
- axe checks and targeted visual snapshots;
- manifest-to-export and manifest-to-story validation;
- separate `dist/storybook` output.

**Exit gate.** Storybook builds from root `devDependencies`; production source has no `.storybook` or story imports; the production bundle contains no Storybook module; embedded assets contain no Storybook payload.

### Phase 4 — Big-bang application migration, last

Convert the complete application onto the finished foundations and contracts as one program. Work may use reviewable internal batches by pattern and route family, but partial batches are not an accepted final state and do not establish an indefinite mixed system.

Internal order:

1. Mechanical token, typography, motion, spacing, and status replacements.
2. Raw controls and low-level imports onto primitives.
3. Confirmations, sheets, forms, and overlay behavior onto composites.
4. Collection, action, field, save, and long-running state presentations.
5. Domain-component relocation and public-import cleanup.
6. Route-by-route behavioral, visual, keyboard, and accessibility verification.
7. Remove every temporary exception and enable all final gates repository-wide.

**Final cutover gate.** The route inventory is verified, all completion metrics pass, the temporary ledger has zero open entries, and production build/bundle checks preserve the single-binary and no-runtime-dependency constraints.

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

Each batch passes its narrow checks before another depends on it. Syntax gates never stand in for visual or behavioral evidence. Applicable checks include unit/type tests, Storybook interaction and axe tests, route browser tests, keyboard-only flows, representative screen-reader checks, forced colors, reduced motion, 200% zoom, 320px reflow, visual snapshots, pointer hit-region tests, and production asset comparison.

### Merge-conflict control

Freeze or coordinate high-churn UI work during final conversion. Assign ownership by route family and shared component. Shared contracts land before consumers, and consumer batches do not independently alter them. Rebase and rerun affected routes after conflict resolution.

### Rollback and diagnosis

Align commits or pull-request units to observable patterns so a regression can be isolated without restoring the old architecture. Preserve baseline screenshots and test artifacts. A rollback that restores an old pattern reopens its ledger entry; it cannot silently become permanent.

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
| Owner | Named person or workstream |
| Characterization | Test/evidence protecting current behavior |
| Replacement | Destination token/component/contract |
| Expiry | Date or program checkpoint |
| Status | Open, blocked, resolved, or approved permanent transfer |
| Proof | Test, screenshot, or review evidence closing it |

Every lint suppression and allow-list entry maps to a ledger ID. Expired entries fail CI. Permanent transfer requires explicit review and registry addition. The program cannot complete with any open or blocked temporary entry.

## 6. Verification gates

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
| Visual regression | Selected appearance is reviewed | Interaction correctness |
| Production bundle audit | Storybook is absent and size delta known | Runtime behavior alone |

## 7. Completion metrics

The baseline records numerator, denominator, exclusions, command, and artifact for every metric. Counts are exception-adjusted: registered legitimate cases remain reported separately, not silently deleted.

| Measure | Completion rule |
|---|---|
| Route coverage | 100% of inventoried routes and major modal/tab states receive their verification matrix |
| Adoption by category | 100% of in-scope foundations, primitives, composites, domain components, layouts, and feature surfaces conform |
| Duplicate patterns | Zero unregistered duplicate confirm, empty, error, skeleton, save, switch, sheet-width, or state implementations |
| Token integrity | Zero undefined tokens, cycles, forbidden edges, or unregistered raw system colors; typed output matches CSS |
| Typography | Zero computed UI text below 12px and zero unregistered arbitrary type values |
| Library boundary | Zero unregistered raw named-job elements or forbidden low-level imports in feature code |
| Contrast/status | 100% of status combinations pass required contrast and distinction checks |
| Axe | Zero serious/critical violations; lesser findings resolved or explicitly accepted through permanent governance |
| Keyboard flows | 100% pass for the representative route/component matrix |
| Zoom/reflow | 100% pass at 200% zoom and 320px without loss of content/function |
| Forced colors/reduced motion | 100% of the specified matrix passes |
| Visual regression | Zero unexplained diffs; every accepted change has reviewed evidence |
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
