# Design-system foundation policy

Status: A1 Encode contract, 2026-09-17; amended 2026-09-19 for founder decisions D17 (touch adaptation follows the input in use) and D18 (zoomable content). This policy implements D1, D2 and D9–D18 of the design-system definition. The machine-readable source is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/design-system/tokens/foundations.json`.

## Scope and activation

This package encodes the target non-colour foundations. It does not import generated CSS, change the document root, or convert components. Global adoption starts in C1, after the generator and validation package can prove the complete colour and non-colour graph. Until then, the current application remains the rendering source.

The visual-delta classification is:

| Area | Classification | A1 effect |
|---|---|---|
| Token extraction for current families, geometry and motion | Invisible | Definitions only |
| Text below 12px raised to the floor | Approved Normalization D2 | Encoded; applied in C1 |
| Current 14px-root rem gaps mapped to the 4px / 8px scale | Approved Normalization D10 | Encoded; applied in C1 |
| Reduced-motion behavior | Approved Normalization D12 | Policy encoded; applied and browser-tested later |
| Touch adaptation keyed to the input in use instead of the device's primary pointer | Approved Normalization D17 | Policy recorded; mechanism and tokens encoded and applied in C1 |
| Touch-mode text-entry size `max(16px, current size)` | Approved Normalization D17 | Policy recorded; token encoded and applied in C1 |
| Any other type, density, geometry, timing or overlay-order change | Redesign Risk | Not authorized by this package |

## Typography

The product root remains user-adjustable through `clamp(12px, var(--user-font-size, 14px), 20px)`. Text roles marked `responsive` follow that root. Fixed geometry, borders and hit regions use pixels and do not scale with the root.

Outfit owns display and headings. Inter owns interface and body text. JetBrains Mono owns code, identifiers and aligned technical data. Product roles are display, page title, section title, body, compact body, label, caption and code. Each role has a family, size, weight, line height, letter spacing and maximum measure token. Marketing may retain its established 16px body default.

Twelve pixels is a computed floor at every root setting. In touch mode (D17), native text-entry controls, search inputs and the chat composer additionally render at `max(16px, current size)` through one registered typography token and one element-level rule, so iOS Safari does not zoom on focus and text never shrinks at a larger root. Dropdown triggers and date pickers keep their current size until the founder decides the open D17 question. There is no 9px, 10px or 11px role and no compact-mode exemption. The caption and compact roles use a CSS `max()` expression so user scaling cannot push them below 12px. D2 authorizes only this floor correction; role migration must otherwise preserve the existing computed family, size, weight, line height and tracking of each surface.

Body copy may use the 65-character measure token where an existing presentation already fits the constitutional 45–75 character target. C1 must not impose that measure on an established surface whose layout differs without recording and approving the visual delta.

## Spacing, layout, breakpoints and density

The closed product spacing scale is 0, 4, 8, 16, 24, 32, 40, 48 and 64px. Named semantic tokens cover control gaps, content padding, section gaps, layout gutters and page margins. At C1, common 14px-root Tailwind geometry such as 7, 14, 21 and 28px maps to the nearest intended 4px / 8px step. Hairlines and 1px borders remain 1px.

The sidebar width, 44px chrome header, 34px live-browser tab row, 40px swatch geometry and minimum hit regions are explicit preserved geometry. They are not an invitation to add off-grid spacing. In particular, changing the 34px browser tab row changes the remote viewport and requires its own approved visual delta.

Breakpoints describe behavior: 320px is the minimum reflow viewport, 640px is the current intermediate content transition, and 1024px is the current wide-content transition. Application layouts must reflow at 320px without two-dimensional scrolling, apart from intrinsically two-dimensional tables, canvases and timelines. New device-branded or one-screen breakpoints are not allowed. Layout follows window width only: a full-screen iPad gets the desktop layout with or without a keyboard, and the phone layout starts below 640px. By founder ruling on 2026-09-19, the workspace task board and the calendar grid count as two-dimensional content for this program; their phone layouts are dedicated later work (#737, #738).

Comfortable and compact density are opt-in modes over the same scale. They may change control gaps and geometry. They never reduce the 12px type floor or an accessible hit region. Pointer adaptation is independent of density and follows the input in use (D17): pointer-mode targets are at least 24×24px only when the WCAG spacing exception is satisfied; touch-mode, destructive, primary and isolated controls use at least 44×44px. Touch mode enlarges invisible hit regions first, so switching modes does not move the page. Enlarged hit regions must not overlap.

## Radius, borders, elevation and overlays

Tailwind v4 currently defines the heavily used `rounded-sm`, `rounded-md`, `rounded-lg`, `rounded-xl` and `rounded-2xl` utilities as 0.25, 0.375, 0.5, 0.75 and 1rem. The radius tokens retain those root-relative values, preserving the existing computed corners at the 12, 14 and 20px user root settings. At the default 14px root they resolve to 3.5, 5.25, 7, 10.5 and 14px. The closed scale also records the audited fixed 3, 4, 6 and 10px literals used by the scrollbar, calendar and graph adapters, plus 0 and full. These adapter values may be consolidated only with a separately approved visual delta. Border widths are 0, 1 and 2px; border styles are solid and dashed. One pixel is the standard border and permitted hairline. Components do not invent raw radius or border values.

Elevation is sparse on dark surfaces: flat, raised, floating and overlay. The floating and overlay shadows preserve the audited `0 4px 16px rgba(0, 0, 0, 0.5)` and `0 8px 24px rgba(0, 0, 0, 0.5)` values. Shadow does not replace a visible boundary or focus treatment.

Overlay order is base content, sticky chrome, menus/popovers, sheets/dialogs, alerts/toasts, then exceptional full-screen viewers. The encoded values are 0, 10, 20, 50, 60 and 200. They establish relative categories; an active dialog or sheet owns a local stacking context in which its menu or popover must render above that overlay. Background overlays stay below the active overlay. C1 may not mechanically replace every historical `z-index` with these numbers: nested-menu, nested-overlay, focus containment, dismissal and scroll locking must be verified first. Any user-visible reordering is Redesign Risk.

## Motion and loading timing

Normal motion preserves the timings observed in current source: 150ms fast feedback, 200ms standard feedback and expansion, 300ms slow feedback or exit, and 500ms entrance. The standard, ease-in-out and current spring easing values are tokenized. Travel distances are 4, 16 and the currently used 36px onboarding step distance.

Motion explains feedback, entrance, exit, expansion and reordering and never blocks input. New callers use the smallest named duration and distance that explain causality. `transition-all`, raw durations, unregistered keyframes and arbitrary choreography are not valid application contracts.

Loading uses one timing contract:

- reserve layout immediately;
- wait 400ms before showing the loading UI;
- once shown, keep it visible for at least 300ms;
- at 10000ms without progress data, show the indeterminate long-running state and any applicable cancel, background or retry action;
- show determinate progress only when the operation reports real progress.

Reduced motion sets non-essential duration and travel to zero. Essential state change uses immediate or low-motion feedback. Framer Motion callers must use the shared reduced-motion policy. The normal-mode timing tokens do not change merely because reduced-motion support is added.

## Icons

Phosphor is the standard product icon family. The named metric set records the established sizes used across product surfaces: 12px small, 14px compact, 16px medium, 18px control, 20px large, 24px prominent, 28px feature, 32px display and 48px hero. The 14px size is currently the most prevalent explicit Phosphor size; 18px and 24px are established control and prominent-state sizes. `regular` is the default weight and `fill` is reserved for established emphasis and status treatment. A small optical baseline adjustment of at most 0.125em is available for inline alignment; arbitrary per-screen nudges are not.

Icon pixels and target pixels are separate. A 12px icon can sit within a 24px pointer-mode or 44px touch-mode hit region. Standalone interactive icons use `IconButton`. Decorative icons are hidden from assistive technology. Meaningful icons have an accessible-name owner; when visible text already supplies the name, the icon remains decorative. Product chrome does not introduce a competing icon family. The two audited inline-SVG integrations remain governed integration exceptions rather than a second icon system.

## Content, forms and localization

`Field` owns label and control identity, help, required or optional indication, validation, `aria-invalid` and `aria-describedby`. Disabled and read-only remain semantically and visually distinct. Validation states the problem and the next action without relying on colour.

Interface copy uses consistent terminology, sentence case and punctuation. Loading copy uses the ellipsis character `…`. Truncation cannot hide information required to decide or act; an accessible complete value must be available. Dates, times, numbers, plural rules and sorting use locale-aware platform APIs. Layout and component contracts must tolerate translated-text expansion.

These content rules do not authorize a new form layout. Field adoption preserves current geometry unless a separate visual delta is approved.

## Data visualization boundary

Charts, graphs, diagrams, syntax highlighting and file-type indicators use a separately governed colour palette from the colour package. That palette is not reused as status chrome. Each series needs a second channel in addition to hue, such as shape, line style, pattern or direct label. User-authored colour remains user data and receives contrast-aware surrounding treatment.

This non-colour package supplies the foundation rules that visualization consumers share: readable typography, named spacing, reduced motion and non-colour differentiation. Existing palette values remain unchanged in A1. Adding the required second channel is an approved D14 normalization during application conversion and must be checked for monochrome comprehension.

## Forced-colour system palette

In forced-colours mode, the standalone library uses the user's system palette: Canvas for page/surface backgrounds and CanvasText for ordinary secondary and muted text and boundaries. Muted text is ordinary readable content, not disabled content. Focus uses Highlight. Controls with selected, checked or progress states must retain a visible system-colour cue and readable text; the shared surface mapping alone does not prove that. These overrides apply only inside the forced-colours media query and leave normal D3/D4 values unchanged. Browser checks verify the rendered cue and contrast, not merely a state attribute. The repository-wide colour lock must register this narrow system-colour boundary when installed in B.

## Zoomable content

Definition D18 fixes the shared zoom values: one range of 25% to 400% on every zoomable surface, and an opening scale no smaller than the scale at which labels compute to 12px (the D2 floor applied to the fitted scale). A pinch changes the content's zoom, never the page's own zoom. These values live in `ZoomableView`, not in each surface.

## Enforcement and C1 evidence

The A1 token source must pass the shared JSON schema, uniqueness, defined-reference and layer-direction checks. Focused tests additionally lock the type floor, role completeness, closed spacing scale, density invariants, finite geometry scales, overlay order, motion/loading timings and icon metrics.

C1 and later packages add the enforcement that requires runtime context: computed type-floor tests at 12/14/20px roots, off-grid source locks, 320px and 200% reflow, pointer-mode and touch-mode checks, the D17 in-context touch check on real routes at phone and tablet sizes, a one-time real-device iPhone and iPad Safari check, reduced-motion browser tests, overlay interaction tests, locale expansion, visualization monochrome checks, and founder review of approved visual deltas. No A1 check claims that those user-visible gates have already run.
