# Omnipus Design System — Definition

**Status:** Target-state constitution, amended 2026-09-17 after adversarial review, then after the founder ruling that the program is one big-bang migration and that small bounded visual changes are in scope.

**Scope:** The Omnipus SPA and reusable UI package. The Go backend is in scope only where it embeds SPA assets.

**Brand:** The Sovereign Deep — Deep Space Black `#0A0A0B`, Liquid Silver `#E2E8F0`, Forge Gold `#D4AF37`; Outfit, Inter, and JetBrains Mono.

**Companion:** Implementation and completion rules live in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/design/design-system-migration-plan.md`.

This constitution defines the finished system, not rollout order. Where product code and this document disagree, the code is non-conforming. Each decision separates measured facts from interpretation and names enforcement that can observe the rule it claims to enforce.

## The rules in plain language

1. **One system, one look.** There is not an old unofficial version and a new official version of spacing, type, colour, or buttons.
2. **No text smaller than 12px.** Dense labels use 12px, not 10px or 11px.
3. **Same meaning, same colour.** “In progress” on the board and on the calendar use the same colour. The map is in D4. Forge Gold is the live-work colour; other states use green, amber, red, blue, or grey — not silver and gold for everything.
4. **Gaps follow an 8px scale.** Today’s 7 / 14 / 21px steps become 8 / 16 / 24px. One scale, everywhere.
5. **Screens use the shared parts.** Buttons, confirms, switches, empty, error, and loading come from the named components. Those replacements keep today’s look.
6. **Inventing a one-off fails the build.** A new colour, a 10px label, a homemade button, or a one-off gap does not ship unless it is on the exception ledger.
7. **Touch follows the finger, not the device.** The page looks and behaves like desktop until someone actually touches it. An iPad used with a keyboard and trackpad stays desktop. Rule D17.
8. **Zooming works the same everywhere.** Graphs, diagrams and images open readable, a pinch zooms the picture and never the page, and one small zoom control sits in the same place. Rule D18.

Screen-by-screen first-run problems (empty Board, empty Library, the model list) are not these rules. They are product findings and are handled separately.

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
| D17 | Touch adaptation follows the input in use; in touch mode, text-entry controls render at `max(16px, current size)`, hover-only controls also appear, and hit regions enlarge invisibly (founder decision 2026-09-19) | Pointer mode is unchanged; touch-mode changes are bounded and verified in context | Resizing dropdown triggers or date pickers (open decision in D17), or any touch-mode reflow the in-context check does not require |
| D18 | One zoomable-content behaviour for graphs, diagrams and images: readable opening size, one compact zoom control, content-only pinch, a mini-map on large graphs, one zoom range (founder decision 2026-09-19) | Reuses existing canvas and viewer surfaces; adds only the compact control, a team-graph mini-map and corrected opening sizes | Replacing React Flow or Mermaid, or changing node, edge or diagram styling beyond D3/D4 tokens |

Anything not in this table is Invisible by default, or Redesign Risk. Two versions of a foundation (old spacing and new spacing, old type and new type) are not an accepted end state.

### E1. The build enforces this rulebook

**Decision.** These rules are not guidance. CI fails a change that introduces, in application or primitive code:

- a colour that is not a token or a registered exception (documents, QR, syntax, charts, user-authored);
- computed UI text below 12px, or an arbitrary text-size utility;
- a spacing value off the 4px / 8px scale, except hairlines and 1px borders;
- a raw `<button>`, `<dialog>`, `window.confirm`, or checkbox-as-switch in feature code;
- a second status palette (a hex for inbox / next / in progress / blocked / done / failed / cancelled that is not the D4 map);
- a public primitive without its coverage manifest, once Phase 2 has enabled that gate.

Temporary exceptions use the migration ledger and expire. Permanent exceptions use the constitutional registries. A green lint count that does not cover the rule does not count as enforcement.

**Visual delta: Invisible.** Locks do not change rendering. They stop new drift.

**Evidence (facts only).** Lanes A–D found raw colours, sub-12 text, homemade buttons, and two status palettes while the product still built.

**Enforcement.** Stylelint, AST-aware ESLint, token-graph parsing, and seeded self-tests. Each rule has a fixture that must fail when the violation is present and pass when it is not.

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

Primitive colour values for this program are today’s rendered hexes. Extra ramp steps may exist for future use; they must not be substituted into current surfaces without a declared delta.

| Primitive | Hex | Used as |
|---|---|---|
| surface-0 / primary | `#0A0A0B` | Page shell |
| surface-1 | `#111113` | Raised panel |
| surface-2 | `#141416` | Card / composer fill |
| surface-3 | `#222228` | Higher fill |
| secondary / text | `#E2E8F0` | Primary text (Liquid Silver) |
| muted | `#9CA3AF` | Secondary text |
| border | `#2D3748` | Default border |
| accent | `#D4AF37` | Forge Gold actions and live work |
| accent-hover | `#C49E2F` | Gold hover |
| success | `#10B981` | Done / success |
| warning | `#EAB308` | Cancelled (stopped by user) and warning |
| error | `#EF4444` | Failed / danger |
| error-hover | `#DC2626` | Danger hover |
| info | `#3B82F6` | Next / information |
| blocked / orange | `#F97316` | Blocked |
| mount | `#8EA3BD` | Library mount icon |

**Visual delta: Invisible.** The graph must reproduce these computed values exactly. Repairing an undefined or invalid token that changes rendering is a separate declared delta, not part of token extraction.

**Evidence (facts only).** Lane A found one flat CSS token list, no primitive ramps, no component layer, duplicated hexadecimal values in TypeScript maps, 125 hard-coded color values across 12 audited files, and token names referenced without definitions. The audit did not establish that every component needs component tokens.

**Enforcement.** A PostCSS/Stylelint token-graph check parses custom-property declarations and references, validates namespace edges, detects cycles and undefined tokens, and covers CSS and third-party theme adapters. A generated typed token module is the only token source for TypeScript consumers. Tests seed bad edges, cycles, missing names, and allowed exceptions to prove detection.

### D4. Status is a complete, distinguishable presentation contract — amended 2026-09-17 after adversarial review

**Decision.** Success, warning, danger, information, and workflow states use recognizable semantic hue families—green, amber, red, blue, and others where needed—tuned to Sovereign Deep. They are not forced into silver and gold. Each status defines foreground, background, border, icon, label, hover/focus treatment, contrast ratios, and a non-color cue. Equivalent states look and read the same across surfaces. Status sets pass pairwise-distinction and common color-vision-deficiency checks.

There is one map. The task palette is the winner. Calendar chips, list cells, graph nodes, and any other status chrome use these hexes. Filled calendar chips may stay filled, and tinted task pills may stay tinted; the hue and the label must match.

| State | Colour | Hex | Non-colour cue |
|---|---|---|---|
| Inbox | Grey | `#9CA3AF` | Quiet circle |
| Next | Blue | `#3B82F6` | Ready / info |
| In progress | Forge Gold | `#D4AF37` | Live work (the one gold status) |
| Blocked | Orange | `#F97316` | Prohibit |
| Done | Green | `#10B981` | Check |
| Failed | Red | `#EF4444` | X |
| Cancelled (stopped by user) | Amber | `#EAB308` | Distinct from Failed; not a separate board column |

Forge Gold is reserved for live work and primary actions. It is not the colour for Next, Blocked, Done, or Failed.

**Visual delta: Normalization — approved.** Calendar chips that today use different hexes (in progress blue `#60A5FA`, blocked yellow `#FBBF24`, and the other calendar-only values) move onto this map. The change is noticeable on the calendar; the board already matches.

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

A feature screen may not ship its own button, confirm box, or switch. Raw elements remain legal inside named low-level primitive directories, documented wrappers, and approved third-party integration boundaries. They are not legal shortcuts in feature code. Each exception records exact surface, reason, owner, and expiry.

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

**Enforcement.** Narrow rules ban `animate-pulse` outside `Skeleton`, local declarations named `*EmptyState`, `*ErrorState`, or `*Skeleton` outside approved locations, and direct low-level imports where a composite exists. Source-search self-tests prove those rules. Review and interaction tests cover semantic equivalence; lint is not claimed to understand meaning. The founder judges visual continuity on the running app. A one-off explanatory image is optional and never a gate.

### D7. Primitives fail closed on accessibility and interaction

**Decision.** `IconButton` is distinct from `Button`. It requires an accessible name through a TypeScript union of `aria-label` or `aria-labelledby`; `Button` remains child-agnostic. `Button` defaults to `type="button"`. Decorative icons are `aria-hidden`; meaningful icons have an owned accessible name. `CommandInput` has a programmatic label.

Visual size and hit area are separate. Interactive hit regions are at least 24×24px and become at least 44×44px in touch mode (D17). Wrappers or pseudo-elements may enlarge a hit region without enlarging dense chrome. Enlarged regions must not overlap. The WCAG spacing exception at 24px applies only when adjacent-target spacing satisfies the criterion; destructive, primary, and isolated touch controls use 44px.

**Visual delta: Normalization.** Accessible-name and button-type repairs are invisible. Effective hit regions are normally invisible; spacing may change subtly to noticeably where compliant, non-overlapping touch-mode targets cannot fit without reflow, using the least disruptive treatment.

**Evidence (facts only).** Lane C found central focus styling and Radix semantics, but no primitive guarantee for button type, icon names, decorative icons, command-input labeling, reduced motion, or minimum hit regions. It also found a hand-rolled Settings switch.

**Enforcement.** Type contracts and development assertions enforce accessible names and button type. Storybook interaction tests use axe and accessible-name assertions. Pointer-event tests verify effective hit regions and non-overlap in pointer mode and touch mode (D17). The primitive checklist blocks release when a required item is absent.

### D8. Storybook and `@omnipus/ui` are the verified front door

**Decision.** Components are classified before publication. `ModelSelector`, `RestartConfirmDialog`, `AutoSaveIndicator`, `BrandIcon`, and other domain widgets do not live in the primitive namespace. `@omnipus/ui` exports a curated foundations/primitives/composites API through an explicit export map.

Every public component has a coverage manifest naming variants, sizes, applicable states, themes, keyboard interactions, and accessibility assertions. Static components are not forced into irrelevant states. Storybook is development-only and never enters the production dependency graph or embedded SPA assets.

What already exists vs what this program must finish:

| Already in the product | Must be built or completed |
|---|---|
| `Button`, `Badge`, `Dialog`, `Sheet`, `Switch`, `Checkbox`, `Progress` | `IconButton` as a distinct contract; `Button` loading/success/error states |
| `SkeletonList`, `EmptyState`, `ErrorState`, `QueryErrorState` | Shared `Skeleton`, `CollectionState`, `JobStatus`, `ConfirmDialog`, `Field` |
| `@omnipus/ui` stub that re-exports part of the catalog | Curated export map; domain widgets moved out of `ui/` |
| No Storybook | Storybook as the verification front door |

Existing parts keep their current look. New parts are added to fill the holes; they are not a restyle of what is already on screen.

**Visual delta: Invisible.** Catalog, ownership, documentation, exports, and development-only verification do not change production rendering.

**Evidence (facts only).** Lane B inventoried 26 primitive families, 8 domain widgets plus 2 helpers inside `ui/`, 11 shared-composite files, 14 library test files, no Storybook, and an `@omnipus/ui` stub exporting only part of the catalog.

**Enforcement.** CI validates manifest-to-export and manifest-to-story coverage, builds Storybook, and runs axe and interaction tests. Export-map tests reject accidental public APIs. Storybook packages exist only in root `devDependencies`; output is separate from `dist/spa`; production source cannot import `.storybook` or `*.stories.*`; bundle inspection rejects Storybook modules; embedded asset size is measured and checked against its approved budget. Storybook is not a screenshot or snapshot baseline.

## Part 2 — Complete foundations

### D9. Typography is a role system, not a bag of sizes

**Decision.** Outfit is for display/headings, Inter for interface/body, and JetBrains Mono for code, identifiers, and aligned technical data. The role system documents and tokenizes the current effective family, size, weight, line height, letter spacing, and maximum line length for display, page title, section title, body, compact body, label, caption, and code at the 14px default density. It does not introduce a new scale. The only approved size changes are D2's 12px floor and D17's touch-mode text-entry floor. Body copy targets 45–75 characters per line where that already reflects the current presentation; changing an established measure requires a declared delta.

**Visual delta: Invisible by default; Redesign Risk for any new value.** Any change from current rendered typography—including family, size, weight, line height, tracking, or measure—must identify affected surfaces and receive approval before implementation, apart from D2's and D17's approved normalizations.

**Evidence (facts only).** The brand defines the three families and three example roles: 48px heading, 16px body, and 12px caption. Lane A found family tokens but no complete size, weight, or line-height system, and found `font-sans` not mapped to Inter.

**Enforcement.** Typography roles are generated typed tokens. Arbitrary family, size, weight, leading, and tracking values are rejected outside the exception registry. Computed-style and line-length stories cover every role.

### D10. Spacing, layout, breakpoints, and density adapt as one system

**Decision.** Spacing is one product-wide 4px / 8px token scale. Common steps are 4, 8, 16, 24, 32, 40, and 48. There is not a legacy scale and a new scale in production at the same time.

Today's default Tailwind rem steps at the 14px root compute to 7, 14, 21, 28px and similar. The big-bang maps those onto the 4px / 8px scale. Spacing and control geometry use this scale in pixels so they do not jump when the user changes font size (D1: `rem` is for type). Named tokens cover control gaps, content padding, sections, gutters, and page margins. Hairlines and 1px borders stay 1px.

Breakpoints express content behavior rather than device brands. Layouts reflow at 320px without two-dimensional scrolling except for intrinsically two-dimensional content such as data tables, canvases, and timelines. By founder ruling on 2026-09-19, the workspace task board and the calendar grid count as two-dimensional content for this program; their phone-adapted layouts are dedicated later work (issues #737 and #738). Layout follows window width only, never device type (D17).

Density has comfortable and compact modes. Those modes are additive and opt-in; they are not a second unnamed scale. The default density is this 4px / 8px system on every in-scope surface. Density changes spacing and control geometry, never the 12px floor or accessible hit region. Touch mode (D17) selects touch-adapted hit regions and 44px targets independently of visual density.

**Visual delta: Normalization — approved.** Mapping current 14px-root rem geometry onto the 4px / 8px scale shifts common padding and gaps by about one pixel per step (about 14% on those steps). Close comparison will notice it; it is not a redesign. A default switch to compact or comfortable density, or any larger reflow, remains Redesign Risk.

**Evidence (facts only).** Lane A found tokens for sidebar width, 44px tap target, and two chrome heights, but no general spacing scale. Lane D found 23 literal 44px dimensions despite an existing token.

**Enforcement.** Stylelint rejects unregistered spacing and breakpoint values in system-owned CSS. Responsive stories and browser tests cover 320px, intermediate widths, wide layouts, both density modes, and pointer and touch modes (D17).

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

The supported browser matrix includes current Chromium, Firefox, and WebKit engines. Tests are proportional to risk: unit tests for logic/contracts, interaction tests for behavior, axe for detectable accessibility faults, and browser tests for focus/reflow/motion/pointers. No test type is treated as proof of the others.

**Visual delta: Invisible.** Documentation, API contracts, composition rules, and verification coverage do not require production appearance changes.

**Evidence (facts only).** Lane B found inconsistent variant, class merging, ref forwarding, disabled, and invalid-state APIs, and only 14 library test files for 36 production files in `ui/`.

**Enforcement.** The coverage manifest is machine-readable. CI validates public exports, required documentation, unit and interaction suites, browser coverage, and axe results. Visual continuity is founder-judged on the running app, not through a screenshot or snapshot suite.

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

### D17. Touch adaptation follows the input in use — added 2026-09-19 by founder decision

**Decision.** The page decides touch behaviour from the input the person is actually using, not from the type of device. It is in *touch mode* after a finger interaction and in *pointer mode* after a mouse, trackpad or pen interaction. Keyboard input never changes the mode. A device that reports no fine pointer at all starts in touch mode; every other device starts in pointer mode. One shared foundation mechanism records the mode on the document root, and tokens and components read that single signal. No component detects input on its own, and no new `pointer: coarse` or `hover: none` media query is added for touch adaptation. Existing coarse-pointer rules migrate to the mode signal in C1.

Layout follows window width only (D10). An iPad at full screen gets the desktop layout, whether or not a keyboard is attached; a narrow split-screen window follows its width. The phone layout starts below the 640px transition.

Touch mode enlarges invisible hit regions first (D7), so switching modes does not move the page. Visible touch-mode changes are limited to:

- **Text entry.** Native inputs, textareas, search inputs and the chat composer render at `max(16px, current size)` through one registered typography token and one element-level rule, so iOS Safari does not zoom on focus and no user's text gets smaller at a larger root setting.
- **Hover-only controls.** A control that is revealed only on hover is also shown in touch mode.
- **Unavoidable geometry.** Where the in-context touch check shows an invisible hit region cannot fit without overlap, the least disruptive visible adjustment is used.

**Open decision.** Whether dropdown triggers and date pickers, which do not cause focus zoom, match the 16px text-entry size in touch mode. Until the founder decides, they keep their current size.

**Visual delta: Normalization — approved 2026-09-19.** Pointer mode is unchanged apart from the other approved normalizations. The touch-mode changes above are bounded and each is verified in context.

**Evidence (facts only).** Eight application source files, plus five test and story files, decide touch adaptation from the device's primary pointer, which an iPad reports as touch even with a keyboard attached. On 2026-07-15, blanket 44px coarse-pointer floors inflated the chat composer controls and cut off the agent picker on iPad; they were removed in commit `80afc1329`. Text inputs compute to 12.25px at the default 14px root, below the 16px threshold at which iOS Safari zooms on focus. No production code observes the input type actually used. The mobile assessment and its independent critique are in `docs/internal/design/evidence/mobile-review-2026-09-19/`.

**Enforcement.** The in-context touch check runs the real application routes from the checked-in inventory as a touch phone and a touch tablet, in both modes, before and after each repair batch. For every visible interactive control it asserts four things: no ancestor that hides overflow clips it; no label or entered text is clipped; hit regions do not overlap; and declared key rows stay within their height budget, measured as numbers. It also completes task flows: choosing a model in the model picker, composing a chat message, and signing in. A failure blocks the batch. The founder reviews only flagged items, each with one explanatory image; this is not a screenshot suite. The typography lock rejects an ad hoc 16px value; only the registered token is allowed. Real iPhone and iPad Safari behaviour, including focus zoom and mode switching, is verified once on real devices before C1 closes, because browser emulation cannot reproduce it.

### D18. Zoomable content behaves one way — added 2026-09-19 by founder decision

**Decision.** Zooming and panning content that is larger than its frame is one named UI job (D5), with one shared component, `ZoomableView`. It covers the workspace task graph, the workspace team graph, Mermaid diagrams, chat images and every future zoomable surface. Gestures are the primary input. The compact zoom control is the required single-pointer alternative (WCAG 2.5.1), not decoration.

- **Opening size.** Fit all content if every label stays at or above the 12px floor (D2) at the fitted scale. Otherwise open at the smallest scale that keeps labels at 12px, anchored at the start of the content: the first node, the top-left of a diagram, or the selected item. Opening and "Fit" frame identically.
- **Control.** One compact zoom pill: zoom out, the current percentage, zoom in. The percentage opens a menu with Fit, 100% and, on graphs, Zoom to selection. The pill sits in the same corner on every canvas; in the media viewer it lives in the viewer toolbar. Keyboard shortcuts are +, −, 0 for fit and 1 for 100%, shown in tooltips. In touch mode its hit regions are 44px (D17) without enlarging its chrome.
- **Gestures.** A touch pinch and a trackpad pinch zoom the content only, never the page. Drag pans. The mouse wheel zooms in full-frame canvases and in the media viewer; inside the chat stream it always scrolls the conversation. Double-click or double-tap zooms in at that point; in the media viewer it toggles between fitted and zoomed.
- **Mini-map.** Graph canvases show a mini-map whenever content exceeds the frame. Clicking it navigates, which is the single-pointer alternative to dragging (WCAG 2.5.7).
- **One zoom range:** 25% to 400% on every surface.
- **Inline previews.** Chat diagrams and images stay previews sized to the column. The enlarge action is always visible in touch mode (D17) and opens the media viewer, where every diagram, wide or tall, opens fitted to the screen.

**Defects fixed under this rule.**

- **Wide diagrams in the viewer.** Enlarging a wide Mermaid diagram collapses it to an unreadable strip about 300px wide at every viewport.
- **The Graph tab at phone width.** At 390px, a tap on the Tasks "Graph" view tab was intercepted by the overlapping "Filter by agent" control in automated emulation. It is confirmed on a real device or by human emulation, then fixed.

Both are in scope for the C3 repair batch. They are fixed, not deferred.

**Visual delta: founder-approved 2026-09-19.** This adds the zoom pill to the media viewer and the team graph, and a mini-map to the team graph. It changes the opening size and anchor of both graphs and the media viewer. Node, edge and diagram styling are otherwise preserved.

**Evidence (facts only).** Live measurement on 2026-09-19, in desktop Chromium and in iPad and iPhone emulation in Chromium and WebKit:

- **Task graph.** It opens at 80%, with 34 of 40 tasks off-screen, centred mid-chain.
- **Team graph.** It opens at 50%, with labels about 7px effective and agents cut off at top and bottom.
- **Chat previews.** Inline chat Mermaid renders wide diagrams at 15–32% of natural width.
- **Media viewer.** It has no zoom control. A touch pinch zooms the media and the page together. Tall diagrams open at 100% cut off, and wide diagrams collapse as described above.
- **Zoom ranges.** They differ: task graph 20–175%, team graph 50–200%, viewer 50–800%.

Evidence: `docs/internal/design/evidence/zoom-live-2026-09-19/`.

**Enforcement.** `ZoomableView` owns zoom and pan. Surfaces do not configure them directly: a raw React Flow zoom or fit property outside the shared preset, or a bespoke wheel or transform handler, is rejected in review, and by lint once the raw-control lock is extended to it.

- **Storybook** interaction tests cover the pill, the keyboard shortcuts, reset and the menu.
- **Browser tests** measure the opening scale against the 12px label floor.
- **The in-context touch check (D17)** runs pinch, pan and opening scale on the real routes at phone and tablet sizes. It asserts that the page's own zoom stays at 1 during a pinch.

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
