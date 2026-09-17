# Omnipus Design System — Definition

**Status:** Target-state constitution, amended 2026-09-17 after adversarial review, then after the founder ruling that the program is one big-bang migration and that small bounded visual changes are in scope.

**Scope:** The Omnipus SPA and reusable UI package. The Go backend is in scope only where it embeds SPA assets.

**Brand:** The Sovereign Deep — Deep Space Black `#0A0A0B`, Liquid Silver `#E2E8F0`, Forge Gold `#D4AF37`; Outfit, Inter, and JetBrains Mono.

**Companion:** Implementation and completion rules live in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/design/design-system-migration-plan.md`.

This constitution defines the finished system, not rollout order. Where product code and this document disagree, the code is non-conforming. Each decision separates measured facts from interpretation and names enforcement that can observe the rule it claims to enforce.

## Operating model

| Class | Owns | May depend on |
|---|---|---|
| Foundations | Tokens, typography, spacing, motion, icon and content rules | Nothing above foundations |
| Primitives | Generic controls such as `Button`, `IconButton`, `Field`, `Dialog`, `Sheet` | Foundations and approved low-level libraries |
| Composites | Reusable jobs such as `ConfirmDialog`, `EmptyState`, `QueryErrorState` | Foundations and primitives |
| Domain components | Product-specific UI such as `ModelSelector`, `AutoSaveIndicator` | Foundations, primitives, composites |

`@omnipus/ui` is a curated public API, not a directory mirror. Domain components remain outside the public primitive package unless a separate public contract is approved.

## Visual continuity — governing requirement

The current application appearance is the accepted visual baseline. This program matures the design system without substantially redesigning what users see. Architecture, token ownership, component consolidation, and enforcement must preserve the existing rendered appearance by default, except for the bounded normalizations listed below.

Every decision and migration work package must declare its visual delta as Invisible, Normalization, or Redesign Risk. Appearance-changing work must identify the affected surfaces, current and proposed values or presentations, rationale, expected noticeability, and before/after evidence. This includes typography, density, spacing, colors and opacity, component geometry, loading and motion behavior, and responsive or accessibility modes.

**Delivery.** One big-bang migration: foundations, contracts, and application conversion complete together. Internal batches are risk control, not a mixed long-term system.

**Small changes are in scope.** A Normalization is a small, bounded unification of something the product already does inconsistently, or a one-step snap onto a standard that is already close to what users see. Those changes land in this program. They still need a declared delta, a surface list, and before/after evidence. They do not need a separate redesign decision.

Substantial changes to the established appearance remain Redesign Risk and stay outside this migration's default scope. Accessibility corrections remain required, but their visible effects must be declared and use the least disruptive compliant treatment.

Existing rendered values take precedence over illustrative brand scales or newly selected defaults unless the change is in the approved-normalization list below, or a later explicit visual delta is approved. Tokenization, consolidation, and lint compliance are not sufficient justification for any other appearance change.

### Approved normalizations for this program

| ID | Change | Why it is small | Still out of bounds |
|---|---|---|---|
| D2 | Raise every UI text below 12px to 12px | Floor only; no new type scale | New heading/body sizes, families, or measures |
| D4 | Unify equivalent status colours across calendar and tasks | Same states, one hue family each | Recolouring the brand surfaces or Forge Gold usage |
| D10 | Map today's 14px-root rem spacing (7 / 14 / 21px…) onto one 4px / 8px scale | About one pixel per common step at the default root | A second live scale, or a default density reflow |
| D6 timing | 300–500ms loading delay, minimum dwell, 10s long-running escalation | Timing only | Replacing inline/card errors or page-shaped skeletons with a different layout |
| D7 | Enlarge hit regions, not chrome, to 24px / 44px | Usually invisible; spacing only where targets would overlap | Making dense checkboxes look like 44px controls |
| D12 | Shared reduced-motion; keep today's normal timings | Noticeable only with reduced motion on | Retiming ordinary open/close motion |
| D13 | Consistent punctuation, `…` loading copy, locale-aware numbers | Copy and format | A new stacked Field layout that moves labels or units |
| D14 | Add a second non-colour series cue on charts | Encoding, not a new palette | Recolouring charts, Mermaid, or syntax themes |
| D16 | Dark `color-scheme` and semantic heading/form wiring | Native controls and names | A new Settings information architecture |

Anything not in this table is Invisible by default, or Redesign Risk. Two versions of a foundation (old spacing and new spacing, old type and new type) are not an accepted end state.

## Part 1 — Core decisions

### D1. Product type density is user-adjustable, with a 14px default

**Decision.** The product defaults to a 14px root size within `clamp(12px, var(--user-font-size, 14px), 20px)`. Fourteen pixels is the default density, not a fixed root. Text roles that should follow the user setting use `rem`; fixed geometry, hairlines, and minimum hit regions do not. Marketing surfaces may retain the brand guideline's 16px body default.

**Visual delta: Invisible.** This codifies the current root-size behavior and does not change the rendered default.

**Evidence (facts only).** The audited stylesheet already uses the 12–20px clamp with a 14px default, and Profile settings use the same default. The audit counted 317 non-test TSX files potentially affected; it did not count 317 screens. Routes and rendered surfaces are inventoried separately.

**Enforcement.** The root clamp is defined once. Token metadata marks responsive `rem` roles. Browser tests cover the default, minimum, maximum, 200% zoom, and 320px reflow without loss of content or function.

### D2. Type has a hard 12px floor — amended 2026-09-17 after adversarial review

**Decision.** No Omnipus UI text is smaller than 12px. There is no 11px token and no compact exception below 12px. Dense metadata uses the 12px caption or label roles with appropriate weight and line height.

**Visual delta: Normalization — approved.** Approximately 616 audited text spots below 12px grow to the 12px floor across metadata, code, calendar, and other dense text. The change is noticeable and widespread; the migration must record the finalized computed-style scope and before/after evidence.

**Evidence (facts only).** Lane A counted 666 arbitrary `text-[Npx]` classes in 153 production files, including 616 below 12px. The brand guideline defines its caption/label at 12px. Frequency establishes current use, not usability.

**Enforcement.** The closed type scale has no sub-12 token. Stylelint and Tailwind-aware source rules reject sub-12 values and arbitrary text-size utilities. Browser tests exercise zoom and narrow reflow; computed-style tests prove the minimum rather than searching strings.

### D3. Tokens have one directed dependency graph

**Decision.** The only token graph is `primitive → semantic → component → CSS`.

- Primitive tokens are raw ramps and scales.
- Semantic tokens express purpose: surface, text, border, accent, status, spacing, motion, and elevation.
- Component tokens exist only when a component differs from the semantic default or needs stable public customization.
- Application and layout code may consume semantic tokens.
- Library components consume component tokens when their contract defines them; otherwise they consume semantic tokens directly.
- Visualization palettes and user-authored colors are explicit exceptions governed by D4 and D14.

Every component token has an owner, purpose, supported states, and consumers. Mechanical aliases such as `card-border → border` are forbidden.

**Visual delta: Invisible.** The graph must reproduce current computed values exactly. Repairing an undefined or invalid token that changes rendering is a separate declared delta, not part of token extraction.

**Evidence (facts only).** Lane A found one flat CSS token list, no primitive ramps, no component layer, duplicated hexadecimal values in TypeScript maps, 125 hard-coded color values across 12 audited files, and token names referenced without definitions. The audit did not establish that every component needs component tokens.

**Enforcement.** A PostCSS/Stylelint token-graph check parses custom-property declarations and references, validates namespace edges, detects cycles and undefined tokens, and covers CSS and third-party theme adapters. A generated typed token module is the only token source for TypeScript consumers. Tests seed bad edges, cycles, missing names, and allowed exceptions to prove detection.

### D4. Status is a complete, distinguishable presentation contract — amended 2026-09-17 after adversarial review

**Decision.** Success, warning, danger, information, and workflow states use recognizable semantic hue families—green, amber, red, blue, and others where needed—tuned to Sovereign Deep. They are not forced into silver and gold. Each status defines foreground, background, border, icon, label, hover/focus treatment, contrast ratios, and a non-color cue. Equivalent states look and read the same across surfaces. Status sets pass pairwise-distinction and common color-vision-deficiency checks.

**Visual delta: Normalization — approved.** Calendar and task status presentations are unified so equivalent states use the same approved semantic colors and non-color cues. The recoloring is noticeable; the final mapping and affected surfaces must be recorded before implementation.

**Evidence (facts only).** Lane A found calendar “in progress” rendered blue while the board rendered the same state in Forge Gold. It also found default Tailwind hue utilities in 32 files alongside semantic colors. The brand already defines green success and red error; it does not require every status to use silver or gold.

**Enforcement.** Stylelint covers CSS colors and utilities; AST-aware ESLint covers JSX attributes, style objects, SVG values, and class builders. An exception registry covers document/paper surfaces, QR codes, syntax highlighting, data visualization, and user-authored colors. Contract tests verify contrast, non-color cues, and pairwise distinction under color-vision simulations.

### D5. Named UI jobs have one legal component path

**Decision.** Feature code uses:

| Job | Required path |
|---|---|
| General action | `Button` or `IconButton` |
| Modal information or edit | `Dialog` |
| Confirmation, especially destructive | `ConfirmDialog` |
| Slide-over task | `Sheet` with named sizes |
| Label, control, help, validation | `Field` |
| Loading, empty, query error, long-running status | The owner named in D6 |
| Boolean preference | `Switch`; never a checkbox styled as a switch |

Raw elements remain legal inside named low-level primitive directories, documented wrappers, and approved third-party integration boundaries. They are not legal shortcuts in feature code. Each exception records exact surface, reason, owner, and expiry.

Destination components must preserve the current presentation of what they replace unless a visual delta is declared and approved. The default is “same look, one implementation,” not “new look, one implementation.” Required contracts may include presentation-preserving variants or wrappers for text actions, disclosures, inline controls, contextual sheet widths, and other established forms.

**Visual delta: Invisible by default; Redesign Risk if presentation changes.** Any changed control geometry, styling, confirmation chrome, or sheet sizing requires a separate approved delta before replacement.

**Evidence (facts only).** Lane D counted 98 production files with a raw `<button>` and no `Button` import, plus 38 mixed-use files. It found four confirmation mechanisms and one `window.confirm`. Lane B found a real primitive kit but inconsistent adoption and domain widgets mixed into `ui/`.

**Enforcement.** AST-aware ESLint bans raw `<button>`, `<dialog>`, `window.confirm`, and checkbox-as-switch syntax in feature code; import-boundary rules restrict low-level primitives and internal modules. Rules exempt only named directories and the reviewed exception file. Source-rule self-tests seed violations and legitimate wrappers.

### D6. State contracts are owned by component category

**Decision.** State is not a universal four-item list:

| Category | Required states | Reusable owner |
|---|---|---|
| Collections | initial-loading, refreshing, empty, partial, ready, error | `CollectionState` with `Skeleton`, `EmptyState`, `QueryErrorState` |
| Actions | idle, pending, success, error, disabled | The action primitive, normally `Button` or `IconButton` |
| Fields | default, focus, invalid, disabled, read-only | `Field` and its control |
| Long-running jobs | queued, running, progress, paused, failed, complete, cancelled | `JobStatus` and `Progress` |

Loading behavior is observable:

- Reserve layout immediately.
- Delay visible loading UI by 300–500ms to avoid flashes.
- Use skeletons for initial content structure and spinners for compact blocking actions.
- Once shown, retain the indicator for a short minimum dwell defined by motion tokens.
- Show determinate progress only when the operation reports real progress.
- After 10 seconds without progress data, show an indeterminate long-running state with applicable cancel, background, or retry actions.

Consolidation standardizes ownership without standardizing every presentation. Destination components must preserve the current inline, card, full-panel, page-shaped skeleton, and activity treatments they replace unless a visual delta is declared and approved.

**Visual delta: Redesign Risk.** The stated loading delay, minimum dwell, and long-running escalation change timing and must be approved as bounded normalizations. Replacing distinct error, empty, skeleton, or activity presentations is not approved by consolidation alone and must otherwise remain presentation-equivalent.

**Evidence (facts only).** Lane B found no shared loading state on `Button`, `Input`, or `Progress`; multiple empty/error implementations; two save indicators; and only three consumers of the field-error component. It did not establish that every component supports every state.

**Enforcement.** Narrow rules ban `animate-pulse` outside `Skeleton`, local declarations named `*EmptyState`, `*ErrorState`, or `*Skeleton` outside approved locations, and direct low-level imports where a composite exists. Source-search self-tests prove those rules. Review, interaction tests, and targeted visual regression cover semantic equivalence; lint is not claimed to understand meaning.

### D7. Primitives fail closed on accessibility and interaction

**Decision.** `IconButton` is distinct from `Button`. It requires an accessible name through a TypeScript union of `aria-label` or `aria-labelledby`; `Button` remains child-agnostic. `Button` defaults to `type="button"`. Decorative icons are `aria-hidden`; meaningful icons have an owned accessible name. `CommandInput` has a programmatic label.

Visual size and hit area are separate. Interactive hit regions are at least 24×24px and become at least 44×44px for coarse pointers. Wrappers or pseudo-elements may enlarge a hit region without enlarging dense chrome. Enlarged regions must not overlap. The WCAG spacing exception at 24px applies only when adjacent-target spacing satisfies the criterion; destructive, primary, and isolated touch controls use 44px.

**Visual delta: Normalization.** Accessible-name and button-type repairs are invisible. Effective hit regions are normally invisible; spacing may change subtly to noticeably where compliant, non-overlapping coarse-pointer targets cannot fit without reflow, using the least disruptive treatment.

**Evidence (facts only).** Lane C found central focus styling and Radix semantics, but no primitive guarantee for button type, icon names, decorative icons, command-input labeling, reduced motion, or minimum hit regions. It also found a hand-rolled Settings switch.

**Enforcement.** Type contracts and development assertions enforce accessible names and button type. Storybook interaction tests use axe and accessible-name assertions. Pointer-event tests verify effective hit regions and non-overlap at fine and coarse pointer settings. The primitive checklist blocks release when a required item is absent.

### D8. Storybook and `@omnipus/ui` are the verified front door

**Decision.** Components are classified before publication. `ModelSelector`, `RestartConfirmDialog`, `AutoSaveIndicator`, `BrandIcon`, and other domain widgets do not live in the primitive namespace. `@omnipus/ui` exports a curated foundations/primitives/composites API through an explicit export map.

Every public component has a coverage manifest naming variants, sizes, applicable states, themes, keyboard interactions, and accessibility assertions. Static components are not forced into irrelevant states. Storybook is development-only and never enters the production dependency graph or embedded SPA assets.

**Visual delta: Invisible.** Catalog, ownership, documentation, exports, and development-only verification do not change production rendering.

**Evidence (facts only).** Lane B inventoried 26 primitive families, 8 domain widgets plus 2 helpers inside `ui/`, 11 shared-composite files, 14 library test files, no Storybook, and an `@omnipus/ui` stub exporting only part of the catalog.

**Enforcement.** CI validates manifest-to-export and manifest-to-story coverage, builds Storybook, runs axe and interaction tests, and captures targeted visual snapshots. Export-map tests reject accidental public APIs. Storybook packages exist only in root `devDependencies`; output is separate from `dist/spa`; production source cannot import `.storybook` or `*.stories.*`; bundle inspection rejects Storybook modules; embedded asset size is compared with baseline.

## Part 2 — Complete foundations

### D9. Typography is a role system, not a bag of sizes

**Decision.** Outfit is for display/headings, Inter for interface/body, and JetBrains Mono for code, identifiers, and aligned technical data. The role system documents and tokenizes the current effective family, size, weight, line height, letter spacing, and maximum line length for display, page title, section title, body, compact body, label, caption, and code at the 14px default density. It does not introduce a new scale. The only approved size change is D2's 12px floor. Body copy targets 45–75 characters per line where that already reflects the current presentation; changing an established measure requires a declared delta.

**Visual delta: Invisible by default; Redesign Risk for any new value.** Any change from current rendered typography—including family, size, weight, line height, tracking, or measure—must identify affected surfaces and receive approval before implementation, apart from D2's approved normalization.

**Evidence (facts only).** The brand defines the three families and three example roles: 48px heading, 16px body, and 12px caption. Lane A found family tokens but no complete size, weight, or line-height system, and found `font-sans` not mapped to Inter.

**Enforcement.** Typography roles are generated typed tokens. Arbitrary family, size, weight, leading, and tracking values are rejected outside the exception registry. Computed-style and line-length stories cover every role.

### D10. Spacing, layout, breakpoints, and density adapt as one system

**Decision.** Spacing is one product-wide 4px / 8px token scale. Common steps are 4, 8, 16, 24, 32, 40, and 48. There is not a legacy scale and a new scale in production at the same time.

Today's default Tailwind rem steps at the 14px root compute to 7, 14, 21, 28px and similar. The big-bang maps those onto the 4px / 8px scale. Spacing and control geometry use this scale in pixels so they do not jump when the user changes font size (D1: `rem` is for type). Named tokens cover control gaps, content padding, sections, gutters, and page margins. Hairlines and 1px borders stay 1px.

Breakpoints express content behavior rather than device brands. Layouts reflow at 320px without two-dimensional scrolling except for intrinsically two-dimensional content such as data tables, canvases, and timelines.

Density has comfortable and compact modes. Those modes are additive and opt-in; they are not a second unnamed scale. The default density is this 4px / 8px system on every in-scope surface. Density changes spacing and control geometry, never the 12px floor or accessible hit region. Coarse pointers select touch-adapted spacing and 44px targets independently of visual density.

**Visual delta: Normalization — approved.** Mapping current 14px-root rem geometry onto the 4px / 8px scale shifts common padding and gaps by about one pixel per step (about 14% on those steps). Close comparison will notice it; it is not a redesign. A default switch to compact or comfortable density, or any larger reflow, remains Redesign Risk.

**Evidence (facts only).** Lane A found tokens for sidebar width, 44px tap target, and two chrome heights, but no general spacing scale. Lane D found 23 literal 44px dimensions despite an existing token.

**Enforcement.** Stylelint rejects unregistered spacing and breakpoint values in system-owned CSS. Responsive stories and browser tests cover 320px, intermediate widths, wide layouts, both density modes, and coarse/fine pointers.

### D11. Radius, borders, elevation, and overlays have finite scales

**Decision.** Radius, border width/style, shadow/elevation, and z-index are closed token scales that preserve current rendered values by default. Elevation communicates hierarchy sparingly on dark surfaces. Overlay order is explicit: base content, sticky chrome, menus/popovers, sheets/dialogs, alerts/toasts, and exceptional full-screen viewers. The order is relative to the active overlay: a dialog or sheet's owned menus and popovers render above that overlay, while unrelated background overlays remain below it. Components never invent numeric z-index values.

**Visual delta: Invisible by default; Redesign Risk for ordering changes.** Any global z-index or overlay-order change must be verified against nested-menu and nested-overlay cases before rollout. A change that hides or reorders active content fails the gate; approved changes require before/after evidence.

**Evidence (facts only).** Lane A found no radius or shadow tokens. Lane D found a hand-built lightbox at `z-[200]` while Dialog uses `z-50`; the audit did not visually verify the resulting stack behavior.

**Enforcement.** Stylelint rejects raw radius, shadow, and z-index values outside token definitions and registered third-party adapters. Overlay interaction tests verify stacking, focus containment, dismissal, and scroll locking.

### D12. Motion and iconography are governed foundations

**Decision.** Motion tokens define duration, easing, distance, and choreography for feedback, entrance, exit, expansion, and reordering. Motion explains causality and never blocks input. A shared reduced-motion utility removes non-essential travel and replaces essential motion with immediate or low-motion feedback. Framer Motion callers use the required reduced-motion hook or configuration.

Phosphor is the standard icon family. Named sizes, weights, and optical-alignment rules apply. Decorative icons are hidden from assistive technology; standalone meaningful icons use `IconButton` or have another explicit naming owner.

**Visual delta: Normalization.** Shared reduced-motion behavior is noticeable in that mode; normal-mode timings and existing icon metrics remain unchanged unless separately approved.

**Evidence (facts only).** Lane C found no `prefers-reduced-motion` coverage. Lane D found Phosphor used in 196 files and only two legitimate inline-SVG exceptions, so it found no competing icon set.

**Enforcement.** Lint rejects raw motion durations, unregistered keyframes, `transition-all`, and Framer Motion use without the shared policy. Browser tests under reduced motion cover Dialog, Sheet, Accordion, Toast, and custom motion. Icon stories test size, alignment, labels, and decorative treatment.

### D13. Forms and content have explicit composition rules

**Decision.** `Field` owns label, control identity, help text, required/optional indicator, validation message, `aria-invalid`, and `aria-describedby`. Validation explains the problem and next action without relying on color. Read-only and disabled are visually and semantically distinct.

Interface content uses consistent terminology, sentence case, and punctuation. Loading copy uses `…`. Truncation never hides information needed to decide or act; an accessible full value is provided. Dates, times, numbers, pluralization, and sorting use locale-aware APIs. User-visible strings support localization and expansion.

**Visual delta: Normalization.** Punctuation, localized formatting, and required indicators may change subtly; Field composition must preserve existing form geometry unless a separate delta is approved.

**Evidence (facts only).** Lane B found `FormError` used by three files and 42 files declaring their own `role="alert"`. Lane C found sparse explicit label wiring in audited settings fields and inconsistent live-region use. Lane B found both `Saving...` and `Saving…`.

**Enforcement.** Component types make Field wiring the default. Interaction tests assert labels, descriptions, validation announcements, required/optional copy, and keyboard behavior. ESLint rejects literal three-dot loading copy and non-localized UI date/number formatting. Content review owns terminology and usefulness.

### D14. Data visualization uses a separate accessible palette

**Decision.** Charts, graphs, diagrams, syntax highlighting, and file-type indicators may use more colors than application chrome. Their palette is separately tokenized, harmonious with Sovereign Deep surfaces, and not reused as status chrome. Series differ through at least two channels—hue plus shape, line style, label, or pattern. User-authored colors remain user data and receive contrast-aware surrounding treatment rather than silent replacement.

**Visual delta: Normalization.** Existing palette values remain; adding a second distinction channel such as dash, marker, pattern, or direct label is a subtle visible change on affected visualizations.

**Evidence (facts only).** Lane A found separate color maps for Mermaid, FullCalendar, file types, task/plan status, and user/agent colors. It identified dynamic entity colors and document-white surfaces as legitimate exceptions to a blanket inline-color ban.

**Enforcement.** Visualization tokens are in the typed output and exception registry. Automated contrast and color-vision simulations cover legends, labels, and adjacent series; stories verify monochrome comprehension and non-color cues.

### D15. Component anatomy, variants, composition, and support are contractual

**Decision.** Every public component documents anatomy, slots, variants, sizes, applicable states, composition, controlled/uncontrolled behavior, keyboard model, focus behavior, and accessible-name ownership. Variants represent stable product meaning, not one-screen styling. Public roots accept safe hooks such as `className` and refs where supported.

The supported browser matrix includes current Chromium, Firefox, and WebKit engines. Tests are proportional to risk: unit tests for logic/contracts, interaction tests for behavior, axe for detectable accessibility faults, browser tests for focus/reflow/motion/pointers, and targeted visual snapshots. No test type is treated as proof of the others.

**Visual delta: Invisible.** Documentation, API contracts, composition rules, and verification coverage do not require production appearance changes.

**Evidence (facts only).** Lane B found inconsistent variant, class merging, ref forwarding, disabled, and invalid-state APIs, and only 14 library test files for 36 production files in `ui/`.

**Enforcement.** The coverage manifest is machine-readable. CI validates public exports, required documentation, unit and interaction suites, browser coverage, axe results, and selected visual baselines.

### D16. Accessibility is a release requirement

**Decision.** WCAG 2.2 AA is the minimum. Each primitive checklist covers semantics, accessible-name ownership, keyboard operation, focus visibility/restoration, target size, error identification, status announcement, zoom/reflow, contrast, forced colors, reduced motion, and high-contrast behavior.

The system additionally requires:

- decorative icons marked `aria-hidden`;
- `CommandInput` programmatically labelled;
- one logical heading hierarchy per screen;
- labels, help, errors, and controls wired through `Field`;
- `color-scheme: dark` on the document;
- modal/sheet overscroll containment, background scroll lock, focus trap, focus restoration, and appropriate dismissal;
- navigational tabs and filters reflected in the URL when refresh, sharing, and Back/Forward should preserve them;
- representative screen-reader testing in addition to automation.

**Visual delta: Normalization.** Semantic repairs are invisible by default. Browser-owned dark controls and accessibility-mode treatments may change subtly or contextually and must use the least disruptive compliant presentation.

**Evidence (facts only).** Lane C found good Radix focus traps and roles in audited dialogs, a central focus ring, and strong keyboard behavior in several complex controls. It also found gaps in decorative-icon treatment, `CommandInput` naming, heading order, form wiring, dark color-scheme declaration, modal overscroll, and Settings tab URL state. Automated tests alone cannot establish screen-reader usability.

**Enforcement.** CI runs axe, keyboard interaction tests, forced-colors tests, 200% zoom and 320px reflow, and reduced-motion browser coverage. Release evidence includes representative screen-reader checks for navigation, a validating form, a dialog/sheet, a collection state, and a long-running job. Component owners own accessible names; callers supply domain wording where needed.

## Non-goals and governance

- The system remains dark-first; a light theme is not implied.
- This does not redesign product information architecture or workflows.
- No design-system tool becomes a shipped runtime dependency; the SPA remains embedded in the single Go binary.
- User colors, document rendering, syntax highlighting, and visualization are governed exceptions, not loopholes.
- Amendments require a recorded founder decision, date, evidence, and updated enforcement.
- Completion is measured only by `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/design/design-system-migration-plan.md`.

## Evidence sources

- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/ui-audit-2026-09-17/lane-a-tokens.md`
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/ui-audit-2026-09-17/lane-b-library.md`
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/ui-audit-2026-09-17/lane-c-implementation.md`
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/ui-audit-2026-09-17/lane-d-consistency.md`
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/brand/brand-guidelines.md`
