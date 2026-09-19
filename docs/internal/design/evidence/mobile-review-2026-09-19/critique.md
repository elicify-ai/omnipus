# Critical review — the "16px input text on phones" recommendation and the mobile report

**Reviewer:** independent, read-only lane (`claude-fable-mobile-critique`), 2026-09-19.
**Under review:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/design/evidence/mobile-review-2026-09-19/report.md` and the lead's recommendation quoted in the brief.
**Method:** read the governing documents and the source; grep across `src/`; one live measurement of the built Storybook `Input` story at 375px width (served read-only on port 6177, stopped afterwards). Nothing was edited, built, or committed. Every finding below is marked **verified** (I saw it in a tool result) or **reasoned** (follows from verified facts or platform knowledge, but I did not observe it).

---

## 1. Verdict: AMEND

The lead is right about the problem and right that it needs the founder's sign-off, but the fix as worded would (a) **shrink** text for users who have set a large font size, (b) **miss** the chat composer, the search boxes, and every hand-written text field outside the two shared components, and (c) be written in a form the program's own typography lock is designed to reject. The amended recommendation:

> **On coarse-pointer devices (fingers, not mice), make the text inside every native text-entry control at least 16px, never smaller than it is today: `max(16px, <the control's current size>)`. Apply it as one registered design-system token and one CSS rule on the elements themselves (`input`, `textarea`, `select`) so it reaches every text field in the app, not only the shared `Input`/`Textarea` components. Desktop is unchanged. Record it as a founder-approved visual delta next to the existing 44px touch-height rule, and land it in C1 together with the 12px floor.**

Two follow-on decisions the founder must make, not the lead: whether the dropdown-style controls that are buttons (Select, date picker) also go to 16px on phones so form rows match, and whether the task board is on the "two-dimensional content" exemption list or owes a 320px single-column layout inside this program (see section 5).

---

## 2. Findings on the recommendation

| # | Finding | Severity | Evidence | Status |
|---|---|---|---|---|
| R1 | **A fixed 16px shrinks text for large-font users.** The root size is user-adjustable 12–20px. At root 20px the input text is already 17.5px; "16px on phones" would make it *smaller* for exactly the users who asked for bigger text. Must be `max(16px, current)`. | High | Live measurement, Storybook `design-system-input--default` at 375px: root 14px → input 12.25px; root 12px → 12px; root 20px → **17.5px**. Root clamp: `src/styles/globals.css` `html { font-size: clamp(12px, var(--user-font-size, 14px), 20px) }`. | Verified |
| R2 | **`max()` is already the house pattern, so this is not a new kind of rule.** The 12px floor is encoded as `--font-size-body-compact: max(12px, 0.875rem)` and the typography lock understands `max()` expressions. | Medium (it makes the fix cheaper, not riskier) | `src/styles/tokens.generated.css` lines 85–92; `scripts/design-system-locks/typography.mjs` (comment "max(a, b) >= a and >= b…" near the interval evaluator). | Verified |
| R3 | **Trigger must be `pointer: coarse`, not a width breakpoint.** The codebase already keys the 44px touch height to `[@media(pointer:coarse)]` in `Input`, `SelectTrigger` and `CommandInput`; the text rule should ride the same switch. A width rule fails both ways: an iPhone in landscape is 844–932px wide (wider than the 640px `sm` step) and would still zoom; a narrow desktop window would get 16px text, which contradicts "desktop unchanged". | High | `src/components/ui/input.tsx::Input` (`[@media(pointer:coarse)]:h-[44px]`), `select.tsx::SelectTrigger`, `command.tsx::CommandInput`; `design-system/tokens/foundations.json` `breakpoint.content.medium = 640px`. | Verified (convention) / Reasoned (landscape widths, iOS behaviour) |
| R4 | **"Input text" as scoped by the lead misses most phone text fields.** If the change is made inside the `Input`/`Textarea` React components it does not reach: the **chat composer** (assistant-ui `ComposerPrimitive.Input`, a textarea with `text-sm`, `src/components/chat/ChatScreen.tsx` ~line 2956 — the single most-used text field on a phone); cmdk's `CommandInput` (search boxes in pickers, a real `<input>`); **30 hand-written `<input>` elements** outside `components/ui/` (of which 9 untyped = text, 3 `type="text"`, 1 password, 1 number; the rest are checkbox/radio/range/file and do not matter); **2 hand-written `<textarea>`** (`BrowserLiveView.tsx:2895`, `McpServerModal.tsx:652` at `text-xs`); **3 native `<select>`** (`CustomEndpointPanel.tsx:102`, `AgentProfile.tsx:1682` at `text-[9px]`, `RecordFieldEditor.tsx:544`) — native selects also trigger the iOS zoom. One element-level CSS rule covers all of these; a component-level class covers two components. | High | grep counts above (`grep -rn '<input\b' src --include='*.tsx' | grep -v components/ui/`); file:line as listed. | Verified (inventory) / Reasoned (select triggers zoom) |
| R5 | **Radix-based Select, SmartSelect and the date pickers are `<button>`s.** They do not trigger the zoom, so they are not *required* to change — but if only inputs go to 16px, a phone form will show 16px text in a text box next to 12.25px text in the dropdown beside it. That is a look decision for the founder: raise all form-control text on coarse pointers (consistent, larger delta) or accept the mismatch. | Medium | `select.tsx::SelectTrigger` renders `SelectPrimitive.Trigger` (`text-sm`); `date-picker.tsx` line 8 comment "replaces native `<input type=date>`"; `smart-select.tsx` line 150 `text-sm`. | Verified (classes) / Reasoned (element type is a button — Radix default) |
| R6 | **An ad hoc `text-[16px]` would be rejected by the program's own lock.** E1: CI fails "an arbitrary text-size utility". D9: any size change "must identify affected surfaces and receive approval before implementation". The foundation policy's table lists "Any other type … change" as "Redesign Risk — Not authorized by this package". So the fix must be a **registered token** (e.g. a coarse-pointer control-text size) plus a recorded approval, not a class on a component. | High (process) | `design-system-definition.md` §E1, §D9 (quoted in section 4 below); `design-system-foundation-policy.md` "Scope and activation" table; `typography.mjs` line 18 comment and `matchArbitrary` (~line 623). | Verified |
| R7 | **16px fits the 44px coarse-pointer control.** Forced 16px text with the 44px height: line-height 22.86px, content box 35px, no overflow. Textarea (`min-h-[80px]`) and `CommandInput` (coarse `min-h-[44px]`) are also fine. | Low (no knock-on) | Live DOM simulation on the Input story (`scrollHeight 42 = clientHeight 42`). | Verified |
| R8 | **Visual hierarchy flips on phones.** Field labels/descriptions stay `text-sm` (12.25px) while the value inside the box becomes 16px. Not a defect; it is exactly the kind of thing the founder judges by eye and should see on a real phone before approving. | Low | `src/components/ui/field.tsx` lines 56–57, `label.tsx` line 12 (`text-sm`). | Verified |
| R9 | **The lead's "zero `touch-action` in src" is wrong.** One usage exists, in the inline-style form the grep missed. Harmless to the recommendation; it is an instrument-check failure (searching `touch-action` never finds `touchAction`). | Low | `src/components/library/preview/LibrarySignaturePad.tsx:180` `touchAction: 'none'`. | Verified |
| R10 | The other supporting facts check out: `text-sm` on Input/Textarea; root clamp; 12.25px measured; 9 files with `env(safe-area-inset-*)` (report body says 8 — 9 is right, `globals.css` included); viewport meta as quoted. | — | grep -rl safe-area-inset src → 9 files; `index.html` line 10. | Verified |

### Alternatives, honestly weighed

| Option | What it really does | Verdict |
|---|---|---|
| `maximum-scale=1` / `user-scalable=no` in the viewport meta | iOS Safari has ignored it for pinch-zoom since iOS 10 but still uses it to suppress the focus zoom, so it "works" on iPhone. Android Chrome honours it and **disables pinch zoom** for everyone unless they dig into accessibility settings — a WCAG 1.4.4 failure. axe-core ships a `meta-viewport` rule that flags exactly this. The definition requires WCAG 2.2 AA and 200% zoom (D16). | **Reject.** |
| `font-size: 16px` + `transform: scale(0.77)` + `width: 130%` | Defeats the zoom by lying about size, then breaks the 4px/8px geometry, hit regions and focus rings the program is built on. | **Reject.** |
| Tailwind's usual `text-base md:text-sm` | Fails here twice: `text-base` is `1rem` = **14px** at this product's root (still under 16px, still zooms — verified root 14px), and it is width-based (landscape phones missed, narrow desktop windows changed). | **Reject.** |
| Leave as is | Every phone text field — including the chat composer of a chat-first product — zooms the page on focus. | **Reject.** |
| Coarse-pointer `max(16px, current)` via a token and an element-level rule | Fixes the zoom on every text field, never shrinks text, desktop untouched, passes the lock, one line of CSS to enforce. | **Recommend.** |

---

## 3. Corrections to the report's gap classification

| Report item | Correction | Severity change | Bucket change | Status |
|---|---|---|---|---|
| "Login/onboarding password-visibility toggle sized to a 44px tap target" (listed under *What's already good*) | **Wrong — it is 38.5px on phones.** `min-h-11 min-w-11` is `2.75rem`; at the 14px root that is 38.5px (same arithmetic as the Input's `h-11`, which I measured at 38.5px). The toggle has no `pointer:coarse` override, so it sits under the D7 44px coarse-pointer minimum. Move from "good" to **Gaps**. | new: Medium | (a) — D7 hit-region normalization is already approved | Verified (arithmetic on verified source + measured sibling) |
| "Storybook has no mobile-viewport preset (no `@storybook/addon-viewport`)" | **Stale.** This is Storybook 10.6; the viewport tool has been part of core since v9 (`node_modules/storybook/dist/viewport/index.js` exists; `addon-viewport` no longer exists as a package). At most the gap is "no Omnipus-specific phone presets". | Low → drop or reword | (a) | Verified (module present) / not verified that the toolbar shows it |
| "The login page auto-opens the keyboard on load … zoom appears before the user has read the screen" | **Over-claimed.** iOS Safari does not raise the keyboard for programmatic focus outside a user gesture, so on the very platform the zoom applies to, `autoFocus` most likely does neither. Onboarding has the same `autoFocus` (lines 811, 937). | Medium → Low / unverified | (a) unchanged | Reasoned — not observed on a device |
| "Calendar: no switch to a list/agenda view" | **Partly wrong.** An Agenda (`listWeek`) view exists and is one tap away in the toolbar. What is missing is an automatic phone *default*. | High → Medium | (c) acceptable — the month grid is a timeline (see section 5) | Verified (`CalendarToolbar.tsx:117`, `FullCalendarView.tsx:342`) |
| "Board: you see well under two columns at once" | Over-stated: two 162px columns fit in 375px (324px + padding). The real problem is different (section 4, M1). | Medium (kept, different reason) | (c) — **needs a founder ruling**, see section 5 | Verified (`BoardView.tsx:385,646`) |
| Safe-area: "8 separate files" | 9 files (the table itself lists 9). | — | (a) unchanged | Verified |
| 16px input text | Keep (b): approval is required by D9. But the *form* must be the amended one in section 1, and it lands as a token + policy-table line, not a class. | High unchanged | (b) unchanged | Verified |
| Screenshot paths | Report cites `dist/design-system-baseline/…`, which is git-ignored (`git check-ignore dist` → ignored). The durable copies are beside the report in `docs/internal/design/evidence/mobile-review-2026-09-19/`; cite those. | — | — | Verified |

---

## 4. Governing text — is "Redesign Risk" the right label?

Yes, approval is required; no, nothing in the rulebook makes it *mandatory* work. Quoted:

- D9: "**Any change from current rendered typography—including family, size, weight, line height, tracking, or measure—must identify affected surfaces and receive approval before implementation, apart from D2's approved normalization.**"
- Migration plan §1: "The approved normalizations (12px floor, status-colour unification, 4px / 8px spacing snap) are required work. **Anything outside that table is Redesign Risk and stays out unless the founder approves it.**"
- Foundation policy table: "Any other type, density, geometry, timing or overlay-order change — **Redesign Risk — Not authorized by this package**."
- The reflow rule (D10 / foundation policy): "Layouts reflow at 320px without two-dimensional scrolling, apart from intrinsically two-dimensional tables, canvases and timelines." The iOS focus-zoom does not change the *layout* — the browser magnifies the page — so this rule is not breached on paper. D16 (WCAG 2.2 AA) is also not breached: a browser zooming is an accessibility feature, not a failure.

So the lead's (b) is correct, and the closest precedent is D7's 44px coarse-pointer hit region, which is *Normalization* precisely because it is scoped to coarse pointers and "uses the least disruptive treatment". The founder can reasonably approve the 16px rule on the same footing — a platform accommodation, scoped to fingers — and it should be recorded in the foundation-policy table as such. The E1 lock line "**computed UI text below 12px, or an arbitrary text-size utility**" is why it must be a token.

---

## 5. Is deferring calendar and board layouts to post-C6 acceptable?

**Calendar: yes.** The month grid is a timeline, which the reflow rule exempts by name. A phone default of Agenda view is cheap (the view exists) but is a behaviour change, so (c) or a founder call is fair.

**Board: not on the plan's own words.** Two problems:

1. The plan's post-C6 list is "Usability work (**empty Board**, Library, pickers, task panel, Knowledge)". That is the empty-state of the board, not board reflow. Nothing in the plan defers *board layout at 320px*.
2. The reflow exemption names "tables, canvases and timelines". A Kanban board is none of those by name; the report *asserted* it is "allowed as 2-D content". Today the board scrolls sideways (`BoardView.tsx:344 overflow-x-auto`) **and** each column scrolls vertically (`:646 overflow-y-auto`) — that is two-dimensional scrolling on a phone.

Either the founder adds "Kanban board" to the exemption list explicitly (then (c) stands), or a single-column 320px reflow is program work under D10, not optional usability work. This needs a ruling, not a bucket.

---

## 6. Mobile gaps the report missed (verified in code)

| # | Gap | Why it matters on a phone | Severity | Evidence | Bucket | Status |
|---|---|---|---|---|---|---|
| M1 | **Board drag-and-drop is not touch-ready.** Cards use dnd-kit's `PointerSensor` with a 6px activation distance and the drag listeners are on the whole card; there is no `touch-action` on cards (the only `touchAction` in `src` is the signature pad). dnd-kit sets `touch-action: none` only on its own drag overlay. On touch, the browser's scroll gesture and the drag compete: either a finger trying to scroll a column starts dragging a card after 6px, or the native scroll cancels the drag. | Medium | `BoardView.tsx:451` `useSensor(PointerSensor, { activationConstraint: { distance: 6 } })`; `TaskCard.tsx` lines 105–182 (listeners spread on the card); `node_modules/@dnd-kit/core/dist/core.esm.js:3632` (`touchAction: 'none'` on overlay only). | (a) for `touch-action` on the card; (c) for a long-press activation strategy | Verified (wiring) / Reasoned (behaviour, not observed) |
| M2 | **Four hover-only affordances have no touch fallback.** The codebase has the right pattern (`[@media(hover:none)]:opacity-100`, used in 10 places) but these four skip it, so on a phone they are invisible: the "edit" pencil on record fields (`RecordFieldEditor.tsx:645`, `:872`); **the view-tab strip of an embedded Base preview** (`BasePreview.tsx:685` — navigation hidden until hover); the selection check mark in the composer media library (`ComposerMediaLibrary.tsx:209`). | Medium (BasePreview tabs: High) | file:line as listed; pattern reference `TaskCard.tsx:249`. | (a) — same pattern, no visual change on desktop | Verified |
| M3 | **`100vh`/`h-screen` outside the app shell.** `onboarding.tsx:541` uses `h-screen` as its scroll container; `login.tsx:118` and `__root.tsx:8` use `min-h-screen`; `KnowledgeReader.tsx:257` uses `calc(100vh-6rem)`. The document is clipped at `html { height: 100%; overflow: hidden }` (`globals.css` 147–157). On iOS Safari `100vh` is the *larger* viewport (toolbar collapsed) while `100%` is the visible one, so the bottom strip of the onboarding scroll container — roughly the toolbar height — is clipped and cannot be scrolled to. The app shell itself is correct (`h-dvh` + visual-viewport tracking); these are the screens outside it. | Medium | file:line as listed; `dvh` is used 16 times elsewhere, so the fix is known. | (a) — invisible on desktop | Reasoned from verified CSS; not observed on a device |
| M4 | **Show-password toggle is 38.5px, not 44px, on phones** (see section 3). | Medium | `login.tsx` ~line 215 `min-h-11 min-w-11`, no coarse override. | (a) — D7 | Verified (arithmetic) |
| M5 | **Text fields outside the shared components** (30 raw inputs, 2 raw textareas, 3 native selects; several at sub-floor sizes: `text-[9px]`, `text-xs`, `text-[13px]`). The report only looked at `Input`/`Textarea`. These are also C1 12px-floor debt. | Medium | see R4. | (a)/(b) — covered by the element-level rule | Verified |
| M6 | **No installable/standalone (PWA) support at all**: no web manifest, no `theme-color`, no `apple-mobile-web-app-*` meta. Not a program defect; the report should state it as a known non-goal rather than leave it unmentioned. | Low | `index.html` head (lines 1–30), `ls public/` (no manifest). | out of scope / (c) | Verified |

Checked and **not** gaps: modal/sheet scroll containment is present (`dialog.tsx:69`, `sheet.tsx:110` `overscroll-contain`, plus Radix's scroll lock); no `onContextMenu` dependency anywhere (0 matches), so nothing relies on right-click; touch-target spacing is covered by the coarse-pointer harness the report describes.

---

## 7. What I could not verify

- Any real iOS or Android behaviour: the focus zoom itself, the `autoFocus` keyboard claim, the `100vh` clipping, drag-vs-scroll on the board, whether iPad Safari zooms. All device-level statements above are reasoned.
- Whether Storybook 10's viewport tool is actually enabled in this project's toolbar (the core module exists; I did not open the manager UI).
- That `SelectTrigger`/`DatePicker` render `<button>` (Radix default and the file comments say so; I did not inspect the DOM).
- The application screens live (chat, board, calendar) — per the ground rules I served only the built Storybook, and only the `Input` story was measured.
- Harness note: several compound grep commands returned exit 1 for a *non-matching* sub-pattern (e.g. no `type*.mjs` file, no `<Toaster` match); no data was lost and every count above came from a command that printed its result.

Screenshot/measurement evidence for this critique: the live numbers are quoted inline (12.25 / 12 / 17.5px text; 38.5 / 33 / 55px height at roots 14 / 12 / 20px; 16px-in-44px fits). No new image files were written.
