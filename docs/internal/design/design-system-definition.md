# Omnipus Design System — Definition

**Status:** Ratified by the founder 2026-09-17, eight decisions after a structured interview.
**Evidence base:** the four-lane maturity audit of 2026-09-17 — reports in `docs/internal/ui-audit-2026-09-17/` (`lane-a-tokens.md`, `lane-b-library.md`, `lane-c-implementation.md`, `lane-d-consistency.md`).
**Scope:** the SPA (`src/`) — every screen, component, and new piece of UI chrome. The Go backend is out of scope except where it serves assets.

This document is the constitution of the Omnipus design system. Where code or habit disagrees with it, the code or habit is wrong. Each decision names its enforcement mechanism; an unenforced decision is a wish, and this project has already measured what wishes are worth (a published `Card` with one consumer).

---

## Part 1 — Foundations

### D1. Root font size: 14px, declared the official dense baseline

**Decision.** The application root font size is **14px**. This is the deliberate "dense UI" posture the UX guidelines reserve for professional tools, not an accident. The brand guidelines (`docs/internal/brand/brand-guidelines.md`) must be amended: the 16px body figure applies to marketing surfaces (omnipus.ai), the product SPA runs at 14px.

**Evidence.** Lane A found the app serving a 14px root against a 16px brand spec, making every `rem` render 12.5% smaller than the brand sheet intends — and found this was load-bearing: 317 screens are implicitly designed around 14px.

**Enforcement.** `html` font-size set once in `src/styles/globals.css`; a comment cites this decision. The brand-sheet amendment lands in the same program.

### D2. Type floor: 11px as a named "label" token

**Decision.** The official type scale gains a bottom step: **11px "label"**, permitted for captions, badges, table metadata, and axis labels — never for body copy, never for anything a user must read to complete a task. **10px and below is banned** and all existing uses migrate up.

**Evidence.** Lane A counted 616 sub-12px `text-[Npx]` classes in production code (364× 10px, 211× 11px, 39× 9px and smaller). Lane D independently reframed them: this is not 616 acts of sloppiness, it is a missing scale step the UI already voted for. The definition legitimizes the vote at 11px and refuses it below.

**Enforcement.** The scale is tokenized in `@theme`; a lint rule bans `text-[Npx]` arbitrary values entirely (the scale has named steps; arbitrary sizes are how we got 666 of them) and CI fails on new ones.

### D3. Token architecture: full three layers, built now

**Decision.** The token system is rebuilt in three layers, in one program:

1. **Primitive ramps** — numbered shade scales per hue (e.g. `gold-100…900`, `silver-100…900`, space-black ramp), 5–10 shades each, fixed up front, never generated on the fly.
2. **Semantic aliases** — role tokens that point at ramp steps: `surface`, `surface-raised`, `text-primary`, `text-secondary`, `accent`, `border`, `status-*` (see D4). Components consume only these.
3. **Component tokens** — per-component tokens that point at semantics: `button-bg`, `button-text`, `card-border`, `input-bg`. Component CSS consumes only these.

Dead names (`font-inter`, `font-outfit`, `--color-text-secondary` — used but never defined) are resolved as part of the semantic layer, not patched one by one.

**Evidence.** Lane A: today's tokens are a flat named-hex list (`--color-accent: #d4af37` binds a name to a hex, not a role to a ramp), which is why every new surface (calendar chips, mermaid, file-type maps) invented its own hex — 125 hardcoded values across 12 files.

**Enforcement.** The three layers are the only definitions in `@theme`; a guard test asserts no component-level CSS references a primitive ramp step directly.

### D4. Status color: one brand-derived semantic palette

**Decision.** Success, warning, danger, info, and the workflow states (todo / in-progress / done / blocked) are **tokens derived from the Sovereign Deep world** — cool silvers and Forge Gold, contrast-checked against Deep Space Black — each with a full ramp. "In progress" is one color everywhere; the calendar repaints to the board's gold, not the reverse. **Tailwind's default `red-*` / `amber-*` / `sky-*` / `blue-*` utilities are banned in UI chrome.** Domain visualizations (mermaid diagrams, charts) may use a wider derived palette but must draw it from the token file, not inline hex.

**Evidence.** Lane A: the calendar paints "in progress" sky-blue while the board paints it Forge Gold for the same underlying task state; 32 files use Tailwind default-hue utilities next to the brand palette.

**Enforcement.** Lint rule banning default-hue utilities in `src/**/*.tsx` outside the token definitions; the workflow-state tokens are defined once and referenced by name.

---

## Part 2 — Component contract

### D5. Consumption: the library is the only legal path, migrated big-bang

**Decision.** Using `src/components/ui/` primitives is **mandatory**, and the existing violations are converted in **one dedicated migration program**, not gradually on-touch. Raw `<button>` in screen code, hand-rolled dialogs, and one-off panels where a library component exists are defects after the migration lands.

**Evidence.** Lane D: 98–141 files ship raw `<button>` (count depends on whether `ui/` itself is excluded); `Card` is published with ~1 consumer outside the library. Lane B judged the library 2/5 — "emerging" — precisely because consumption is optional. The founder chose big-bang over migrate-on-touch: the end state is reached once, and the definition starts its life true rather than becoming true over a year.

**Enforcement.** A lint rule bans new raw `<button>` / `<dialog>` / one-off confirm patterns in screen code (the `ui/` primitives themselves are exempt — they are the implementation). The migration program is tracked as its own workstream with a completion criterion of zero lint exemptions.

### D6. The four-state contract

**Decision.** Every data-driven component receives its states from **library APIs**, not local invention:

- **Loading** → one `Skeleton` system (with the timing rules: nothing < 1 s, skeleton for content loads 2–10 s, determinate progress > 10 s).
- **Empty** → one `EmptyState` with title / description / action slots.
- **Error** → one query-error pattern with retry, and inline field errors on forms.
- **Disabled / pending** → one `Button` loading state and shared disabled styling.

Per-screen copies of any of these are deleted during the D5 migration.

**Evidence.** Lane B: loading/empty/error are "invented per screen" and named this the inconsistency users actually see. Lane D found the same forks independently (empty states and confirm dialogs differing per module) and one surviving `window.confirm` (`src/components/library/preview/unsavedGuard.ts:42`).

**Enforcement.** The state components exist in `ui/` with the specified slots; the D5 lint rule flags local reimplementations of their patterns.

### D7. Fail-closed primitives

**Decision.** The primitives enforce correctness at build time instead of relying on convention:

- Icon-only buttons **require** an `aria-label` prop — the component refuses to render without it (type error / runtime throw in dev).
- `Button` defaults to `type="button"` (explicit opt-in for `submit`).
- Interactive hit targets are **≥ 24px** inside primitives (Checkbox, Switch, Slider, icon buttons), with 44px on coarse-pointer layouts.
- One global `prefers-reduced-motion` gate in `globals.css` covers every `animate-in`, accordion, and toast — motion is gated once, centrally, not per component.
- The Settings hand-rolled Switch fork is deleted; the library Switch is the only switch.

**Evidence.** Lane C: "accessibility is a convention, not a guarantee the primitive enforces" — a central focus ring exists, Radix provides traps and roles, but `type="button"`, hit targets, reduced-motion, and remembered `aria-label`s are exactly the four holes the primitives leave open.

**Enforcement.** The primitives themselves (type-level and dev-runtime checks); the reduced-motion gate is a single CSS block with a guard test asserting its presence.

### D8. Storybook is the library's front door

**Decision.** The project adopts **Storybook** as the interactive component catalog: every primitive gets stories covering its variants and its four states (D6), and `@omnipus/ui` exports what `ui/` actually contains (today it exports 4 of 46 primitive files). Storybook is **dev-only tooling**: nothing it produces ships in the Go binary or the embedded SPA; hard constraint #1 (single binary) is unaffected.

**Evidence.** Lane B: no Storybook, no `components.json`, no catalog of any kind — and named invisibility the direct cause of Lane D's re-invention findings.

**Enforcement.** A story file per primitive is part of the definition of done for the D5 migration; CI builds Storybook so broken stories fail the gate.

---

## Consequences and non-goals

- **Brand guidelines get one amendment** (14px product root, D1). Everything else in "The Sovereign Deep" stands.
- **This is a work program, not a doc.** The natural phasing: (1) token rebuild D3+D4 and type scale D1+D2, (2) primitive contract D6+D7, (3) Storybook D8 alongside, (4) big-bang migration D5 last, onto the finished foundation — the founder's logic: migrate once, onto the final architecture.
- **Non-goals:** redesigning screens, changing the Sovereign Deep palette or fonts, a light theme (dark-first stands), and any runtime dependency in the shipped binary.
- **Changing this document** requires the same process that created it: a founder decision, recorded here with its date and evidence. Drive-by edits are not amendments.

## References

- Audit reports: `docs/internal/ui-audit-2026-09-17/` (four lanes, file:line evidence)
- Brand: `docs/internal/brand/brand-guidelines.md`
- Rubric: `elicify-ui-ux-design` skill (visual-system, checklist, accessibility knowledge files)
- Rule set: Vercel Web Interface Guidelines (via the repo-level `web-design-guidelines` skill)
