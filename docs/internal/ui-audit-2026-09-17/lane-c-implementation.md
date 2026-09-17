# Lane C — Component Implementation Quality

Audit date: 2026-09-17. Criteria: Vercel Web Interface Guidelines (fetched live from `command.md`) and the repo `react-best-practices` `rerender-*` / `rendering-*` rules. Read-only except this file.

## Maturity verdict: 3 (defined)

The SPA has a real component library, not a pile of one-off controls. Interactive primitives are Radix (or cmdk) wrappers, there is one global keyboard focus ring, FormError and toasts have a live-region contract, SmartSelect *requires* an accessible name, and several screens treat icon-only buttons, `htmlFor`, and `type="button"` as normal work. That is a defined system.

It is not managed, and it is not best-in-class. Accessibility is a convention consumers must remember, not a guarantee the primitive enforces: `Button` does not default `type="button"`, `size="icon"` does not require `aria-label`, Checkbox/Switch hit targets are 16–20px, decorative Phosphor icons in the library are usually not `aria-hidden`, and `prefers-reduced-motion` is almost absent outside the calendar theme. Settings still ships a hand-rolled switch next to the real `Switch`. React quality is “avoid the worst mistakes” (virtualized chat/search/model lists, module-level row components) rather than compiler- or memo-level discipline. A Radix + shadcn shop that fully applied the Vercel rule set would score 4–5; this is a solid 3.

## Coverage statement (what you sampled, what you did not)

**Library — `src/components/ui/` (full production set, 28 TSX files).** Read: `button`, `input`, `label`, `textarea`, `checkbox`, `switch`, `select`, `smart-select`, `command`, `dialog`, `sheet`, `alert-dialog`, `dropdown-menu`, `popover`, `tabs`, `accordion`, `slider`, `progress`, `table`, `avatar`, `calendar`, `date-picker`, `date-time-picker`, `toast-container`, `FormError`, `AutoSaveIndicator`, `RestartConfirmDialog`, `error-boundary`. Skimmed: `model-selector` (combobox + virtualizer + `data-no-focus-ring`), `brand-icon` (memo + decorative flag). Tests in that folder were used only as evidence of intent, not as a quality score.

**Ten screen-level surfaces (read, not grepped only):**

| # | Surface | Path |
|---|---------|------|
| 1 | Agent list | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/src/components/screens/AgentListScreen.tsx` |
| 2 | Settings | `.../src/components/screens/SettingsScreen.tsx` |
| 3 | Connectors | `.../src/components/screens/ConnectorsScreen.tsx` |
| 4 | Skills | `.../src/components/screens/SkillsScreen.tsx` |
| 5 | Usage | `.../src/components/screens/UsageScreen.tsx` |
| 6 | Calendar | `.../src/components/screens/CalendarScreen.tsx` |
| 7 | Profile | `.../src/components/screens/ProfileScreen.tsx` |
| 8 | Chat (composer + virtualized list) | `.../src/components/chat/ChatScreen.tsx` |
| 9 | Login | `.../src/routes/login.tsx` |
| 10 | Workspace tab bar | `.../src/components/workspaces/WorkspaceTabBar.tsx` |

**Also sampled as chrome (not counted in the ten):** `AppShell` skip-link + `<main>`, `ScreenHeader`, `Sidebar` icon buttons, `MemorySection` (settings body), `ChatImage`, `AgentCard`, `ListStates`.

**Repo-wide greps (not full reads):** `outline-none` / `focus:outline-none`, `aria-live` / `role="alert"|"status"`, `htmlFor` / `autoComplete`, `size="icon"`, `<img`, `transition-all`, `prefers-reduced-motion`, `useEffect` in screens, `React.memo` / compiler flags, `div onClick`, native `<button`.

**Not sampled in depth:** `BrowserLiveView` (3k+ lines; grepped only), Library preview/markdown, Board/Graph/Team, onboarding wizard body, SearchModal internals, CreateAgentWizard beyond the one `size="icon"` button, `packages/ui/`.

**Honesty note:** `outline-none` on Radix items is **not** a missing focus ring in this codebase. `src/styles/globals.css:168` paints `:focus-visible` with `!important` unless `data-no-focus-ring` is set. Findings below only treat outline-none as a defect when the global ring is opted out without a replacement, or when a local `focus:outline-none` is cargo-culted onto a control that already has the global ring.

## Accessibility findings (severity → what → file:line → rule violated → fix)

### High

| Severity | What | Where | Rule | Fix |
|----------|------|-------|------|-----|
| High | Primitive animations ignore `prefers-reduced-motion`. Dialog/Sheet/Select/Dropdown/Popover/Toast/Accordion all use `animate-in` / accordion keyframes. The only reduce-motion block in `src/` is the FullCalendar theme. | `src/components/ui/dialog.tsx:72`, `sheet.tsx:90`, `select.tsx:70`, `dropdown-menu.tsx:60`, `popover.tsx:17`, `toast-container.tsx:26`, `accordion.tsx:53`; contrast `src/styles/fullcalendar-theme.css:523` | Vercel Animation: honor `prefers-reduced-motion`; muted decorative loops must stop | One `@media (prefers-reduced-motion: reduce)` in `globals.css` that disables `animate-in` / accordion / toast slide. Do not leave this to each primitive. |
| High | `Button` never defaults `type="button"`. A native `<button>` inside a form is `submit`. Callers who remember (`login.tsx:242`, `ConnectorsScreen.tsx:657`) are safe; callers who forget (`RestartConfirmDialog.tsx:22`, `error-boundary.tsx:43`, many Settings `Button`s) are one wrapping `<form>` away from a double-submit. | `src/components/ui/button.tsx:46–58` | Vercel: `<button>` for actions; implicit submit is an anti-pattern | `type={props.type ?? 'button'}` on the host `<button>`, still overridable for real submits. Add a unit test. |
| High | Checkbox / Switch / Slider thumb are below WCAG 2.5.8’s 24×24 CSS-pixel minimum (AA). Checkbox is `h-4 w-4` (16px). Switch track is `h-5` (20px). Slider thumb is `h-4 w-4`. Input already does 44px on mobile (`input.tsx:12`) — the library knows the rule and does not apply it to toggles. | `src/components/ui/checkbox.tsx:17`, `switch.tsx:14`, `slider.tsx:17` | WCAG 2.5.8 Target Size; Vercel Touch | Pad the hit target to ≥24px (or 44px on coarse pointers) without growing the visual glyph. `min-h-11 min-w-11 sm:min-h-6 sm:min-w-6` on the root, as login’s show-password control already does (`login.tsx:220`). |
| High | Settings ships a second switch instead of `ui/Switch`. Same 20px track, `focus:outline-none`, no `aria-hidden` on the thumb beyond the span — and it will drift from any future Switch fix. | `src/components/settings/MemorySection.tsx:47–66` | Vercel: semantic HTML before ARIA; composition over forks | Delete `ToggleRow`’s custom button. Use `<Switch id={id} />` + the existing `<label htmlFor>`. |

### Medium

| Severity | What | Where | Rule | Fix |
|----------|------|-------|------|-----|
| Medium | Screen chrome heading is `h2`, then the page paints an `h1`. Document order on Settings/Profile/Connectors/Skills/Usage is skip-link → sidebar → `ScreenHeader` `h2` → content `h1`. Hierarchical headings are inverted. | `src/components/layout/ScreenHeader.tsx:45`; `src/components/screens/SettingsScreen.tsx:59` (same pattern on Profile/Connectors/Skills/Usage) | Vercel Accessibility: headings hierarchical `h1`–`h6` | Make `ScreenHeader` a `<p>` or `<div role="presentation">`, **or** make it the sole `h1` and drop the duplicate page title. |
| Medium | Accordion trigger wraps an `h2`. A heading inside a button breaks heading navigation (the outline either skips it or announces a button as a heading). | `src/components/screens/AgentListScreen.tsx:320–328` | Vercel: semantic HTML before ARIA | Put the heading *next to* the trigger, or style a `<span>` inside the trigger and keep a visually-hidden `h2` outside. |
| Medium | `CommandInput` has no accessible name. Placeholder `"Search..."` is not a label. Used by SmartSelect (`smart-select.tsx:144`) and the model picker (`model-selector.tsx:878`). | `src/components/ui/command.tsx:22–24` | Vercel: form controls need `<label>` or `aria-label` | Default `aria-label="Search"` on `CommandInput`; allow override. Ellipsis: `"Search…"`. |
| Medium | Decorative icons in primitives are not `aria-hidden`. Close `X`, select caret, accordion caret, checkbox check, toast status icons, SmartSelect caret. `brand-icon` and the date pickers already do this correctly — the rest of the library does not. | `dialog.tsx:85`, `sheet.tsx:105`, `select.tsx:27`, `accordion.tsx:39`, `checkbox.tsx:26`, `toast-container.tsx:37–43`, `smart-select.tsx:135` | Vercel: decorative icons need `aria-hidden="true"` | Add `aria-hidden` on every Phosphor node that sits next to visible/sr-only text. |
| Medium | `size="icon"` is untyped for a11y. The only production `size="icon"` *does* pass `aria-label` (`CreateAgentWizard.tsx:411–416`). The primitive cannot enforce that. | `src/components/ui/button.tsx:30` | Vercel: icon-only buttons need `aria-label` | When `size === 'icon'`, require `aria-label` (TypeScript: `ButtonProps` discriminated union). |
| Medium | Settings tabs are URL-initialized but not URL-synced. `?tab=` opens a tab (`settings.tsx` route), then `<Tabs defaultValue={initialTab}>` is uncontrolled — changing tabs does not update the query string; Back does not restore the tab. | `src/components/screens/SettingsScreen.tsx:75` | Vercel Navigation: URL reflects state (tabs in query params) | Controlled `value` + `onValueChange` that `navigate`s `?tab=`. |
| Medium | Settings “could not fetch gateway build info” is a silent `<p>` — no `role="alert"` / `aria-live`. FormError and toasts already define the pattern. | `src/components/screens/SettingsScreen.tsx:64–71` | Vercel: async updates need `aria-live="polite"` | Use `FormError` or `role="alert"`. |
| Medium | Dialog/Sheet/AlertDialog scrollers omit `overscroll-behavior: contain`. Body is `overscroll-behavior: none` (`globals.css:151`), which hides most chaining, but a tall dialog still rubber-bands the page on some browsers. | `dialog.tsx:69`, `sheet.tsx:85`, `alert-dialog.tsx:58` | Vercel Touch: `overscroll-behavior: contain` in modals/drawers | Add `overscroll-contain` to the content class lists. |
| Medium | `ChatImage` turns the `<img>` into a button (`role="button"` + `onClick` + Enter/Space). Works, but it is not a `<button>`, has no `width`/`height` (CLS), and decorative-vs-action is mixed. | `src/components/chat/ChatImage.tsx:155–172` | Vercel: `<button>` for actions; images need `width`/`height` | Wrap the image in `<button type="button">`. Set width/height or `aspect-ratio`. |
| Medium | `AvatarImage` forwards no `alt` and no dimensions. Parent `Avatar` sets CSS size, so CLS is small, but a missing `alt` is an empty accessible name. | `src/components/ui/avatar.tsx:33–36` | Vercel Images: `alt`; explicit width/height | Default `alt=""` (decorative) and document that callers must pass `alt` when the photo is meaningful. |
| Medium | Ellipsis / loading copy uses `...` not `…`. The library’s own AutoSaveIndicator is the source of truth for “Saving...” and tests lock the three dots in. | `src/components/ui/AutoSaveIndicator.tsx:57`; `RestartConfirmDialog.tsx:24`; `smart-select.tsx:48,145`; `login.tsx:249` | Vercel Typography: `…` not `...`; loading states end with `…` | Change the strings and the tests together. |
| Medium | `transition-all` on Tabs, Accordion, Progress, and AgentCard. Animates every property, including layout. | `tabs.tsx:29`, `accordion.tsx:33`, `progress.tsx:18`; `src/components/agents/AgentCard.tsx:48` | Vercel Animation: never `transition: all` | List properties (`transition-colors`, `transition-transform`, `transition-[transform,background-color]`). Progress already animates `transform` via `translateX` — drop `transition-all`. |
| Medium | Document does not declare `color-scheme: dark`. Native form controls, scrollbars, and date pickers can render light-on-dark. A Library audio test even comments that the document has no `color-scheme`. | `index.html:2`; `src/components/library/preview/LibraryAudioPreview.tsx:70` | Vercel Dark Mode: `color-scheme: dark` on `<html>` | `<html lang="en" class="..." style>` or CSS `:root { color-scheme: dark }`. Add `<meta name="theme-color" content="#0a0a0b">`. |
| Medium | `touch-action: manipulation` is unused anywhere in `src/`. | (absent) | Vercel Touch: `touch-action: manipulation` | Set it on `button, a, [role="button"]` in `globals.css`. |

### Low (still real)

| Severity | What | Where | Rule | Fix |
|----------|------|-------|------|-----|
| Low | Login username uses `autoFocus`. Allowed for a single desktop primary field; fires the virtual keyboard on mobile. | `src/routes/login.tsx:195` | Vercel: `autoFocus` sparingly — desktop only | Gate with a coarse-pointer / `matchMedia` check, or drop it. |
| Low | Date pickers `autoFocus` the calendar when the popover opens. Good for keyboard users; can surprise VoiceOver. | `date-picker.tsx:112`, `date-time-picker.tsx:120` | same | Keep for the grid, but don’t also move focus on mobile. |
| Low | ListStates retry uses `focus:outline-none` even though the global ring still wins (`!important`). Cargo-cult that will confuse the next editor. | `src/components/shared/ListStates.tsx:33` | Vercel Focus: never `outline-none` without replacement | Delete the class. |
| Low | Agent list CLI menu items use `focus:outline-none` for the same reason. | `AgentListScreen.tsx:447` | same | Delete. The global ring is the replacement. |
| Low | Memory/Context number inputs add `focus:outline-none` on top of `Input`. | `MemorySection.tsx:108`, `ContextSection.tsx:388` | same | Delete. |
| Low | `htmlFor` is sparse outside Connectors + a handful of settings files (18 hits in settings, 3 in screens). Many settings inputs are labelled by proximity or `aria-label` only. | grep `htmlFor` vs `<Input` in `src/components/settings/` (18 vs 28) | Vercel Forms: labels clickable (`htmlFor` or wrapping) | Pair every `Input` with `Label htmlFor` or wrap the control. |
| Low | No skip-link on login/onboarding (those routes are outside `AppShell`). Acceptable for a single-form page; still a gap if chrome grows. | `src/routes/login.tsx` | Vercel: skip link for main content | Add when those pages grow a second landmark. |

### Passes worth recording (so the score is not only gaps)

- Central `:focus-visible` ring with an explicit, tested opt-out (`globals.css:154–184`, `input.test.tsx:8–16`). Chat composer documents the replacement (`ChatScreen.tsx:2886–2890`).
- Skip link is the first Tab stop (`AppShell.tsx:167–173`), tested.
- Toasts: `role="alert"` vs `role="status"`, dismiss `aria-label`, `type="button"` (`toast-container.tsx:13–64`).
- FormError: `role="alert"`, no empty live region (`FormError.tsx:51–61`).
- AutoSaveIndicator keeps the live region mounted, including idle (`AutoSaveIndicator.tsx:26–52`). Copy still uses `...`.
- SmartSelect **requires** `ariaLabel` (`smart-select.tsx:35–40`).
- Date/DateTime pickers forward `aria-label`; hour/minute selects are labelled (`date-time-picker.tsx:125,138`).
- Skills icon-only buttons all have specific `aria-label`s (`SkillsScreen.tsx:277–373`). Connectors instance actions too (`ConnectorsScreen.tsx:233–251`).
- Login: `htmlFor`, `autoComplete="username"|"current-password"`, show-password `aria-label`, submit `type="submit"`, error `role="alert"` (`login.tsx:179–239`).
- Dialog/Sheet close: `sr-only` “Close” + 44px hit target (`dialog.tsx:84–86`).
- AlertDialog: `role="alertdialog"`, no overlay-click dismiss (`alert-dialog.tsx:52–55`).
- DialogFooter DOM order = visual + Tab order, documented (`dialog.tsx:98–103`).
- Usage table: `scope="col"`, `aria-sort`, real `<button type="button">`, `tabular-nums`, `<Link>` not `div onClick` (`UsageScreen.tsx:166–210`).
- Chat composer: combobox APG (`role="combobox"`, `aria-autocomplete="list"`, `aria-activedescendant`) (`ChatScreen.tsx:2998–3025`).
- Almost no production `div onClick`. TaskCard / graph nodes that use `role="button"` have Enter/Space tests.

## React quality findings (same format)

### High

| Severity | What | Where | Rule | Fix |
|----------|------|-------|------|-----|
| High | React Compiler is not on. `babel-plugin-react-compiler` exists only as a lockfile transitive. `memo(` in `src/components/ui/` is **one** component (`BrandIcon`). Zero `React.memo` under `screens/`, `agents/`, `layout/`. Without a compiler, every Zustand/query tick re-renders whole screens (AgentCard grid, Settings tab bodies). | `package-lock.json` (plugin present); `brand-icon.tsx:118`; grep `React.memo` in screens = 0 | `rerender-memo`; skill note: compiler makes manual memo unnecessary **if enabled** | Enable the compiler in Vite, **or** memo `AgentCard`, virtualizer rows (already extracted — wrap them), and Settings section roots. |
| High | `ChatScreen.tsx` is a mega-module (virtualizer, composer, slash menu, attachments). Rows *were* extracted to module scope (`ChatScreen.tsx:1024–1212`) — that is the `rerender-no-inline-components` fix done right. The parent still owns too much state, so composer keystrokes can still invalidate the list unless subscriptions are split. | `src/components/chat/ChatScreen.tsx` | `rerender-split-combined-hooks`, `rerender-defer-reads` | Split composer state from the list (already partially done via stores). Keep going: list must not subscribe to slash-menu text. |

### Medium

| Severity | What | Where | Rule | Fix |
|----------|------|-------|------|-----|
| Medium | Built-in roster open state is derived from `agents` via `useEffect` + a ref. Extra render on first load; can drift if the list changes after the ref latches. | `AgentListScreen.tsx:578–585` | `rerender-derived-state-no-effect` | Compute `defaultValue` from `agents` during render, or key the Accordion on `hasCustom`. |
| Medium | Create-channel sheet resets fields in `useEffect` when `open` flips. Valid “reset on open”, but it is the effect-as-event pattern. | `ConnectorsScreen.tsx:431–438` | `rerender-move-effect-to-event` | Reset in `onOpenChange(true)` / when the opener calls `openCreateFor`. |
| Medium | `SessionsTable` copies and sorts on every render (`[...rows].sort`). Fine for tens of rows; not for hundreds. | `UsageScreen.tsx:152–155` | `rerender-memo` (extract expensive work); `js-tosorted-immutable` | `useMemo(() => rows.toSorted(...), [rows, sortKey, sortDir])`. |
| Medium | Settings tab panels all mount behind Radix Tabs (inactive content typically unmounts — Radix default is to unmount). If that ever changes to `forceMount`, every section’s queries would run at once. Today this is OK. No `<Activity>` for show/hide of heavy panels (Models/Context). | `SettingsScreen.tsx:91–120` | `rendering-activity` | Optional: wrap heavy tabs in `Activity` when React 19 Activity is adopted, so state survives tab switches without remount. |
| Medium | Default non-primitive props: SmartSelect / DatePicker are not memoized, so this is latent. If they get `memo()`, `placeholder = 'Select...'` and inline `onSelect` lambdas will break memo. | `smart-select.tsx:48`; date pickers’ `onSelect={() => { ... }}` | `rerender-memo-with-default-value` | Hoist default strings; don’t memo until the compiler is on. |
| Medium | `renderChannelsBody()` is a nested function returning JSX (not a nested *component*). Harmless, but it is the same instinct that produces remount bugs when someone later writes `<Body />`. | `ConnectorsScreen.tsx:1025` | `rerender-no-inline-components` (adjacent) | Leave as a function call, or extract `ChannelsBody` to module scope. |

### Low

| Severity | What | Where | Rule | Fix |
|----------|------|-------|------|-----|
| Low | Calendar `nowTick` interval is a real subscription (not derived state). Correct use of an effect. | `CalendarScreen.tsx:192–197` | — | Keep. Prefer `useSyncExternalStore` only if you want to avoid the extra render on view switch. |
| Low | Error toasts from `useEffect` on `tasksError` / `occurrencesError`. Side effect of a query flag — acceptable; could be `meta.onError` on the query. | `CalendarScreen.tsx:99–103,160–162` | `rerender-move-effect-to-event` (weak fit) | Optional query `onError`. |
| Low | No `content-visibility: auto` on non-virtualized lists (agent cards, skills, connectors). Chat/search/model-selector **are** virtualized (`useVirtualizer`) — the expensive lists were handled. | agent/skills/connectors grids | `rendering-content-visibility` | Add only if a roster regularly exceeds ~50 cards. |
| Low | `&&` conditional rendering: screens generally use ternaries for counts (`UsageScreen` empty states). No `count && <Badge>` trap found in the ten screens. | — | `rendering-conditional-render` | Pass. |
| Low | Prop drilling is not the problem. Screens use Zustand + TanStack Query. Composition of Radix parts is correct. | — | composition vs prop-drilling | Pass for the library. |

## Gaps vs best-in-class (benchmark: Radix primitives’ a11y guarantees, Vercel rule set full compliance)

**What Radix already gives you (and you mostly keep):** focus trap, Escape, `aria-expanded`, listbox/option roles, dialog `aria-modal`, tab list keyboard, slider keyboard. Wrappers do not strip those. Sheet even sets `aria-modal="true"` explicitly (`sheet.tsx:84`). AlertDialog adds `role="alertdialog"` and blocks overlay dismiss — that is *better* than a stock shadcn port.

**What a best-in-class library adds on top of Radix — and this one does not yet:**

1. **Primitive-enforced a11y, not screen-enforced.** Radix will not set `type="button"`, will not require `aria-label` on icon buttons, will not grow a 16px checkbox to 24px, will not `aria-hidden` your icons, will not label `CommandInput`. shadcn’s better forks (Vercel’s own `ai-elements`, Adobe Spectrum, Base UI examples) bake those in. Omnipus relies on reviewer memory. Skills/Connectors/login show the memory is often good. MemorySection and Button’s missing `type` show it is not a guarantee.
2. **One motion policy.** Radix animations plus `animate-in` with zero `prefers-reduced-motion` is a WCAG 2.3.3 miss. Best-in-class is a single CSS gate.
3. **No forks.** A second switch in Settings means the design system is a suggestion. Best-in-class deletes the duplicate.
4. **URL is the source of truth for tabs/filters.** Workspace tabs are Links (good). Settings tabs are not (gap).
5. **Vercel content/typography rules** (`…`, `color-scheme`, `touch-action`, `overscroll-contain` on the overlay itself) are not part of the primitive checklist.
6. **React 19 performance baseline.** Virtualization where lists explode (chat, search, model catalog, provider picker) is genuinely best-in-class. The rest of the tree has no compiler and no memo, so a typing keystroke in the wrong store still costs a full screen. That is “we fixed the known disaster,” not “the default path is cheap.”
7. **Form primitive.** There is `Label` + `Input` + `FormError` as three parts and a comment showing how to wire `aria-describedby`. There is no `<FormField>` that does it for you. Best-in-class ships the compound so `aria-invalid` cannot be forgotten.

## Top 3 fixes by impact

1. **Gate motion once, in `globals.css`.** `@media (prefers-reduced-motion: reduce)` that kills `animate-in`, accordion height animation, and toast slide. Highest a11y return per line; unblocks WCAG 2.3.3 for every Radix overlay you already ship.

2. **Make the primitives fail-closed for a11y.** Default `Button` `type="button"`; 24px minimum hit target on Checkbox/Switch/Slider; `aria-hidden` on decorative icons; `CommandInput` default `aria-label`; TypeScript-require `aria-label` when `size="icon"`. Then delete `MemorySection`’s hand-rolled switch. After this, a new screen inherits the quality instead of re-deriving it.

3. **Fix chrome semantics + Settings URL.** `ScreenHeader` must not emit an `h2` before the page `h1`. Make Settings tabs a controlled `?tab=` value. Optional but related: enable the React Compiler so AgentCard grids and Settings sections stop rerendering as a unit.

---

Evidence for this report is grep hits plus the file reads listed in Coverage. No browser run; claims about the global focus ring winning over `outline-none` rest on the `!important` rule at `src/styles/globals.css:168–176`, which `input.test.tsx` asserts components must not duplicate.
