# Adversarial Review: Workspace side-panel shell (shared, resizable)

**Spec reviewed**: `docs/internal/specs/side-panel-shell-spec.md` (commit `fd9eb8735`, plus interview output `docs/internal/specs/spec-side-panel-shell.md` at `c484c4121`)
**Review date**: 2026-09-26
**Reviewer**: grill-spec, round 1, run by the architect role on Opus (read-only; code checked on `feat/resizable-side-panels`). GitNexus was not available in this session; code claims were checked with direct reads and searches, and the architect re-checked every Verified claim behind CRIT-001, MAJ-001, MAJ-002, MAJ-004 to MAJ-008 and MAJ-010 first-hand before this file was committed.
**Round detection**: no `side-panel-shell-spec-spec-review.md` or `-round2.md` existed before this file → round 1.
**Verdict**: **BLOCK**

## Executive Summary

The spec is well-grounded in today's code, but it misses one data-loss path: SP-7 ("one panel at a time")
turns every "open another panel" action into an unmount of the Library, and the unsaved-edits guard is only
wired to Expand and Close. It also has a set of MAJOR defects. The 70%-of-window ceiling does not keep the
chat usable when the sidebar is pinned. The named-window reuse mechanism would reload or blank the tab it
is meant to reuse. It is unclear whether the URL or the store holds the open-panel state. And SP-15
(Storybook stories) is missing from the spec entirely.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 12 |
| MINOR | 9 |
| OBSERVATION | 3 |
| **Total** | **25** |

Certainty legend used below: **Verified** = checked in code on this branch (evidence named); **Inferred** =
logical from the code, not executed; **Unknown** = could not establish.

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Replacing a panel silently discards unsaved Library edits

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: §2.2 row "Unsaved-edits guard"; FR-013; US-4; §6 "Panel open + workspace switch"; §6 "Two entry points racing"
- **Description**: The spec wraps the guard around the shell's **Expand and Close** only (FR-013: "MUST wrap
  the shell's Expand and Close actions"). Under SP-7, however, the Library panel is also unmounted by:
  (a) clicking another tab-strip toggle, (b) "Watch live" / "Open browser" in chat, (c) following a deep link
  with a different `panel` value (US-4.3), (d) closing a Browser pop-out whose re-dock reaction reopens Browser
  (MAJ-006), and (e) a workspace switch that "re-targets" Library to another workspace (US-3.5). None of these
  run `confirmDiscardLibraryEdits()`. This is new exposure: today Library and Browser live in separate slices,
  so opening Browser never unmounted Library. Worse, the discard dialog is **hosted inside `LibraryExplorer`**
  (`src/components/library/preview/unsavedGuard.ts` module doc: "let whichever `LibraryExplorer` instance is
  mounted render the dialog"). If the store switches first, the dialog's host is already gone, and the
  editor's unmount effect clears the dirty flag (`setLibraryEditorDirty` doc: "its own unmount effect always
  clears the flag"). **Verified** (code read); the loss scenario is **Inferred (high confidence)**.
- **Impact**: An operator types a long note in the Library editor, then clicks "Watch live" on a browser tool
  call. The Browser panel docks and the note is lost with no prompt.
- **Recommendation**: Add an FR: "Every transition that unmounts or re-targets an open panel (open-other,
  toggle-close, deep-link replace, workspace switch, pop-out re-dock) MUST first call the outgoing panel's
  `beforeLeave()` hook. The transition is cancelled if it resolves `false`." Add `beforeLeave?: () =>
  Promise<boolean>` to `PanelDefinition` (§8.1), and make `openPanel` async-gated through it. The Library
  supplies `confirmDiscardLibraryEdits`. Add BDD scenarios "Watch live with unsaved Library edit → prompt →
  cancel keeps Library open with edit intact" and "workspace switch with unsaved edit → prompt". Add them to
  test 8.

---

### MAJOR Findings

#### [MAJ-001] The 70%-of-window ceiling does not keep the chat usable when the sidebar is pinned

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: SP-3 rationale; US-2 AS-2/AS-4; §7 Geometry; dataset "width bounds"
- **Description**: The pinned sidebar is a flex sibling in the same root row as the chat column and the panel
  (`src/components/layout/AppShell.tsx::AppShell` renders `<Sidebar />` in the row).
  `--spacing-sidebar: min(256px, 80vw)` (`src/styles/globals.css`), and it pins at ≥1024px
  (`src/store/sidebar.ts::SIDEBAR_PIN_BREAKPOINT = 1024`). With the panel at the spec's 70% ceiling:
  a 1024px window leaves the chat 1024 − 256 − 717 ≈ **51px**; a 1400px window leaves ≈ **164px**. SP-3's stated
  purpose ("max 70% of the window so the chat stays usable") is not met. US-2.4's weaker guarantee ("never
  collapses to zero") passes while the chat is unusable. **Verified** (widths from code; the arithmetic is exact
  apart from borders).
- **Impact**: The demo's step 3 ("Drag border to far right → stops at 70%") passes, and the founder sees a
  chat 51–164px wide.
- **Recommendation**: Define a chat minimum (for example 360px) and make the ceiling
  `min(0.70 × rowWidth, rowWidth − sidebarWidth − chatMin)`, measured on the flex row, not `window.innerWidth`.
  Put this to the founder as a one-line confirmation, because it changes SP-3's literal number. Add dataset
  rows with a pinned sidebar (1024, 1280, 1400).

#### [MAJ-002] Named-window reuse would reload or blank the tab it is meant to reuse

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: §8.3 "App-opened tabs"; US-6.2; FR-009; BDD "App-opened tab is reused"
- **Description**:
  1. `window.open(url, name)` on an existing named window **navigates** that window to `url`; it does not just
     focus it. The Browser's pop-out opens `about:blank` first and then calls `popup.location.replace(...)`
     (`src/components/browser/BrowserLivePanel.tsx::BrowserLivePanel.handlePopOut`). Given a stable name, a
     second Expand would navigate the existing live-browser tab to `about:blank` and tear down its live viewer.
  2. For the Library, a hash-only change keeps the page, but `/library`'s `folder` param is a "one-time
     initial seed" (`src/routes/_app/library.tsx::librarySearchSchema` comment), so the reused tab would not
     show the new selection. The spec promises both "carries the selection" (§2.3 kept differences) and "reuse".
  3. A toggle click cannot find out whether the named window still exists without calling `window.open`. If it
     does not exist, that call opens a new blank tab, which is the opposite of what the toggle wants.
  4. The name `omnipus-panel-library` is not keyed by workspace or session. Expanding Team for workspace B
     would hijack workspace A's Team tab.

  **Inferred (high confidence)**, from web-platform semantics and the code cited.
- **Impact**: Expanding Browser twice kills a live session viewer. Toggle clicks spawn blank tabs.
- **Recommendation**: Replace the "stable window name" mechanism with an **in-memory handle registry**
  (the Browser already keeps `ownedPopout`). On Expand or toggle: if `handle && !handle.closed`, call
  `handle.focus()` and post the new selection over the existing handoff BroadcastChannel; never re-navigate.
  Otherwise open a new tab. Key handles by `panelId × context` (workspace, or session+agent). A handle is lost
  when the source tab reloads; state that in that case the flow falls back to presence detection (§8.3,
  manual tabs).

#### [MAJ-003] Three different "already open" behaviours: SP-9, US-6.3 and §8.3 contradict each other

- **Lens**: Inconsistency / Ambiguity
- **Affected section**: SP-9; §5 bullet 5; US-6 AS-3; §8.3 "Manually-opened tabs"; click-test rows 9–10
- **Description**:
  - SP-9 (founder): "switch to that tab **instead of reopening the panel**".
  - US-6.3: "does NOT open a duplicate, **keeps/renders the docked panel here**, and shows ... affordance".
  - §8.3: "the docked panel **stays/becomes available**".
  - Click-test row 9: "existing tab focused **or** affordance shown".

  It is undefined whether the docked panel opens. Also unspecified:
  - how long the toggle waits for a BroadcastChannel presence reply (the click becomes asynchronous);
  - what presence is keyed on: a Team full page for workspace A must not block Team for workspace B; a
    `/browser-live` tab is per session;
  - what happens when two presence replies arrive (two manual tabs).
- **Impact**: Two engineers build opposite behaviours, and the demo's pass/fail rows cannot fail.
- **Recommendation**: Pick one rule and state it once:
  1. Presence found → do not open the docked panel.
  2. Show the affordance with "Switch to tab" (best-effort focus) and "Open here anyway".
  3. Presence is keyed `panelId × context`.
  4. Reply timeout is 150ms; after it, open normally.

  Make row 9 deterministic. If "Open here anyway" departs from SP-9's wording, take it to the founder.

#### [MAJ-004] Open-panel source of truth (URL vs store), navigation and Back button are unspecified

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: §8.1 "single source of truth"; §8.2; §17 assumption "Panels remain app-global"; US-7
- **Description**: §8.1 calls the store slice `activePanel` the single source of truth, while SP-14 and §7 make
  the URL `panel` param authoritative. §17 keeps panels app-global (openable from any screen). TanStack Router
  `<Link>` navigation does not carry search params unless told to, and
  `src/routes/_app/workspaces.$workspaceId.chat.tsx` declares no `validateSearch`. So:
  - Does navigating from Chat to Agents close the panel, keep it with a stale URL, or copy `panel` onto
    `/agents`?
  - What do `?panel=tasks`, `team` or `calendar` mean on a route with no workspace?
  - Does opening a panel **push** a history entry (so Back closes it) or **replace**? Holdout scenario 6
    expects "no duplicated URL history entries", which implies a rule that is never stated.

  **Verified** (route file read); behaviour **Inferred**.
- **Impact**: Panels flicker closed on navigation, or the URL and the visible state drift apart; Back either
  closes panels or skips them, whichever the implementer picks.
- **Recommendation**: State that the URL is the source of truth and the store mirrors it. Name the routes
  that accept `panel` (workspace routes only? all `_app` routes via a root `validateSearch`?). Specify that
  open, toggle and replace use `replace: true` (or `push`, if the founder prefers Back-to-close; that makes a
  **Q3**). Specify what happens to the panel on cross-route navigation. Add BDD for each.

#### [MAJ-005] Escape-to-close collides with Escape handling inside panel content

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: US-1 AS-3; FR-012; §7 Accessibility "Escape MUST close the topmost layer"
- **Description**: The spec models only "modal above panel". Other layers already use Escape:
  - `src/components/browser/BrowserLiveView.tsx::handleKeyDown` uses Escape as the WCAG 2.1.2 "release the
    wheel" exit while the operator drives the remote browser. Its comment records that a document-level
    Escape listener above it previously pre-empted this exit.
  - IME composition, the Library editor, Radix menus inside panel content (the Library create menu) and the
    browser address bar all use Escape.
  - Escape in the chat composer must not close a panel either.

  **Verified** (code read).
- **Impact**: A keyboard user presses Escape to stop driving the remote browser, and the whole Browser panel
  closes.
- **Recommendation**: "The shell closes on Escape only when focus is inside the shell, only on a bubble-phase
  listener on the shell root, and only if `event.defaultPrevented` is false. Content that consumes Escape
  calls `preventDefault()`." Add a BDD scenario "Escape while driving the Browser releases the wheel; the
  panel stays; a second Escape (focus on the address bar) closes the panel." Decide whether the second
  Escape should close; this can go to the founder.

#### [MAJ-006] Pop-out re-dock reactions will hijack whatever panel is open

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: §6 "Browser panel with an owned pop-out" ("its re-dock reaction on pop-out close still fires"); §2.1 pop-out rows
- **Description**:
  - `src/components/library/LibraryPanel.tsx`: `onLibraryPopoutClosed` **unconditionally** calls
    `openLibraryPanel(...)` (its comment: "now unconditional").
  - `BrowserLivePanel.tsx::watchPopoutClosed` re-opens when `browserPanel === null`. With a single slice, that
    check means "no Browser", not "no panel".

  Under SP-7, closing an expanded Library tab therefore replaces the Tasks or Mail panel the operator is
  using in the original tab. Combined with CRIT-001, this can also drop edits. **Verified** (code read).
- **Impact**: The operator closes a background Library tab, and their open Tasks panel in the main tab is
  swapped for Library without any action on their part.
- **Recommendation**: "A pop-out close re-docks its panel only if **no panel** is open in the source tab.
  Otherwise it is a no-op." Add BDD and a regression test.

#### [MAJ-007] Tab-strip toggle semantics conflict with the tablist markup, and the highlight is rarely visible

- **Lens**: Ambiguity / Infeasibility
- **Affected section**: US-5; FR-007; click-test rows 1, 6, 7
- **Description**:
  - `WorkspaceTabBar` is a `role="tablist"` with a single `aria-selected` tab and a sliding underline
    (`src/components/workspaces/WorkspaceTabBar.tsx`). The spec never says whether Chat stays selected while
    Tasks is "highlighted". Toggle buttons need `aria-pressed`, not a second selected tab.
  - The compact dropdown's trigger shows the active label ("Active ▾"), and the spec does not say which label
    it shows when Chat plus a panel are active.
  - The strip collapses to the dropdown when the **chat column's** top bar (`@container` in
    `WorkspaceTabContainer.tsx`) is below 1152px. With the pinned sidebar, a 1400px window already gives
    ≈1144px **without** any panel. So on typical laptops the strip entries the demo rows click do not exist,
    and they never exist while a panel is open unless the window is very wide.

  **Verified** (markup and breakpoints); exact widths **Inferred**.
- **Recommendation**: Specify the ARIA model: Chat as the only tab, the panel entries as toggle buttons with
  `aria-pressed`, or a separate group. Specify the dropdown trigger label and the pressed-state rendering.
  Rewrite click-test rows 1/6/7 to name the viewport width, and add dropdown variants.

#### [MAJ-008] SP-15 (Storybook stories) and the design-system publication contract are missing from the spec

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: Header "Input ... SP-1..SP-14"; §10; §12; §13
- **Description**: SP-15 was decided after the spec was written (commit `c484c4121` after `fd9eb8735`). It
  requires stories with sample data for Library, Browser, Mail, Team, Tasks, Calendar and the shell. No FR,
  test, wave or success criterion carries it. Separately, a new shared shell component under `src/components/`
  must satisfy the blocking design-system gate's publication chain (public export → catalog → manifest →
  story → executed checks; `.claude/skills/omnipus-design-system/SKILL.md` §9, enforced by `coverage.mjs`).
  Skill rule 14 prefers a ported shadcn/ui component for a recurring job such as a resizable split, while
  §17 commits to "native pointer events, no new deps". That trade-off is undecided. **Verified**.
- **Impact**: Wave 1 goes red on the design-system gate, or SP-15 is silently dropped.
- **Recommendation**: Add FR-016 (stories per SP-15, with the wave each story lands in), FR-017 (the shell's
  manifest and catalog entry, including the separator's touch-target and focus-visible checks), and SC-007.
  Record the build-vs-port decision for the resize primitive with its reason.
- **SP-15 specifically against §9 (SP-10 demo) and §10 (SP-8 rollout)** — dispatch check, verified by the
  architect directly: `grep -n -i -E 'SP-15|storybook|stor(y|ies)'` over the spec finds no reference (the
  only hits are "User stories" headings). §9 builds the demo as a separate static snapshot with sample
  content for all six panels, and never mentions Storybook. §10 has no story deliverable in any wave. The
  header still reads "Decisions Log SP-1..SP-14". So SP-15 is a **gap, not already accounted for**. Two
  further points the revision must handle:
  1. **No product panel story exists today.** `find src -name '*.stories.tsx'` gives 36 files, and the only
     name matching library/browser/mail/task/team/calendar/workspace is `src/components/ui/calendar.stories.tsx`
     (the date-picker primitive, not the Calendar panel). Panel stories need sample data, a store and router
     stubs; the spec must say where those fixtures come from (SP-15 says "sample data", so no live gateway).
  2. **SP-10's demo and SP-15's stories overlap.** Both require every panel rendered with sample content;
     a static Storybook build is itself a static snapshot that can be click-tested in a real browser. Building
     both means two sample-data sets that can drift. Whether the stories may *be* the demo is a founder call
     (Questions for the founder, Q7).
  Issue #899 ("Storybook covers only the design system: add stories for the product's main screens and
  panels", OPEN) should be cited as the home for everything outside the six panels plus the shell.

#### [MAJ-009] Width memory write rules are ambiguous and can destroy the user's preference

- **Lens**: Ambiguity / Incorrectness
- **Affected section**: §7 Persistence "re-clamp from a window resize **may** write the clamped value only if it differs"; US-3 AS-4; FR-005
- **Description**: "May" leaves both behaviours allowed. If the implementer writes on re-clamp, one temporary
  window shrink (or docking the laptop to a smaller screen, or the zoom in holdout scenario 7) permanently
  overwrites 950px with 700px. The storage unit is not stated either (px or fraction), and "reset persisted"
  does not say whether it stores the default or deletes the key. Storing the default in px freezes the
  default for one window size.
- **Recommendation**: "Persist the user-chosen px only on drag release, keyboard change, or reset (reset
  **deletes** the key). Never persist a window-driven re-clamp; clamp at read time." Add a BDD scenario:
  shrink, then restore the window, and the original width returns.

#### [MAJ-010] Deep links cannot restore panel context for Library or Mail, not just Browser

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: US-7 AS-1 ("restores with its content"); §8.2; Q2 ("every other panel restores from just the id")
- **Description**:
  - `?panel=library` on a workspace chat URL cannot tell a sidebar-opened virtual-root Library (§2.1: no
    workspace) from a workspace-scoped one. It carries no path or folder, while today's pop-out carries both.
  - Mail's API is per workspace **and agent** (email spec, `feature/email-mail`,
    `GET /workspaces/{id}/mail/{agentId}/...`), so a bare `?panel=mail` has the same missing-context problem
    Q2 raises for Browser.
  - "Every other panel restores from just the id" is false.

  **Verified** (email spec read on `feature/email-mail`).
- **Recommendation**: Define context params per panel (for example `panel=library&lib_ws=…&lib_path=…`,
  `panel=mail&agent=…`), or narrow US-7.1 to "restores the panel at its default view". Extend Q2 to Mail.

#### [MAJ-011] Traceability gaps: acceptance scenarios without BDD, and one requirement traced to the wrong tests

- **Lens**: Inconsistency (structural)
- **Affected section**: §11, §12, §13 traceability matrix
- **Description**:
  - Acceptance scenarios with **no** BDD scenario: US-2.1 (live follow), US-3.3 (no memory → default),
    US-3.4 (reset persisted), US-4.3 (deep link replaces), US-5.4 (full-page route active state),
    US-5.5 (dropdown), US-7.1 (reload), US-7.3 (no param), US-7.5 (bare browser), US-8.2 (close restores
    chat), US-9.3 (focus into panel on open), US-9.4 (landmark).
  - FR-015 (Browser viewport handover) is traced to tests 9 and 15, but test 9 is the pop-out ownership test
    and test 15 is full-page routes. **No test covers FR-015.**
  - Test 16 is "automated where feasible", which cannot fail.
  - The §13 claim "every BDD scenario traces" is true, but the reverse direction is not checked.
- **Recommendation**: Add the missing BDD scenarios. Add a component test "Browser under shell: width change
  triggers existing settle/handover" and map FR-015 to it. Replace "where feasible" with a named list of
  automated rows and a named list of manual rows.

#### [MAJ-012] Wave boundaries are not reflected in the FRs, so wave-1 acceptance is undefined

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: FR-001, FR-007, FR-010, §7 URL constraint, SC-002, §10 Wave 1
- **Description**:
  - FR-001 says "MUST host every panel (library ... calendar)", and §7 fixes the six valid `panel` values. Wave 1
    ships two panels, so it is undefined whether `?panel=tasks` in wave 1 is dropped as unknown or kept.
  - During waves 1–2 the strip mixes toggles (Library) with navigating links (Tasks/Team/Calendar), and the
    spec never specifies the mixed state or its active indicator.
  - SC-002 "verified by registering a sample panel in the demo": the demo is a separate static snapshot, so
    this verifies nothing about production code.
- **Recommendation**: Tag each FR with its wave. State that valid `panel` values are the **registered** ids,
  with unregistered ids treated as unknown. Specify the strip's mixed-mode rendering for waves 1–2. Make SC-002
  a production test (a test-only third registration mounts with zero shell edits).

---

### MINOR Findings

#### [MIN-001] "45% of the content area" is undefined
- **Lens**: Ambiguity · **Affected**: §7 Geometry, US-2, dataset rows 5–7
- **Description**: Today's `sm:w-[45%]` is 45% of the root flex row, which is the full window. The dataset uses
  the window (1400 → 630), while §7 says "content area".
- **Recommendation**: Name the basis (the root row width) and use it for both 45% and the ceiling (see MAJ-001).

#### [MIN-002] Focus-return rules conflict with existing behaviour
- **Lens**: Inconsistency · **Affected**: FR-012, US-9.3
- **Description**: Browser Expand focuses the chat input today (`BrowserLivePanel.handlePopOut`:
  `chat-input.focus()`). The spec says focus returns to the invoking control. The invoker may also no longer
  exist (a collapsed dropdown item, or a toggle in a strip that changed layout), and a deep-link restore on
  load should not steal focus.
- **Recommendation**: Specify one rule per case: toggle close, Expand, deep-link open, and missing invoker
  (fall back to the chat input).

#### [MIN-003] Separator ARIA is incomplete
- **Lens**: Incompleteness · **Affected**: §7 Accessibility
- **Description**: The spec does not require `aria-controls` (the panel), an accessible name ("Resize Library
  panel"), or `aria-valuetext`. `aria-valuemax` is dynamic and must update on window resize.
- **Recommendation**: Add all three and the update rule.

#### [MIN-004] Width-memory keys are not scoped per user and grow without bound
- **Lens**: Incompleteness · **Affected**: §8.1 `widthMemoryKey`
- **Description**: Keys are `panel-width.<id>.<ws>`. Two accounts on one browser share widths. Deleted
  workspaces leave orphan keys.
- **Recommendation**: Decide whether keys are per user (for example prefix with the user id), and cap or prune
  orphan keys.

#### [MIN-005] The "<457px window" edge case is wrong
- **Lens**: Incorrectness · **Affected**: §6 last bullet
- **Description**: At ≥640px, 70% is ≥448px, which is always above 320px, so the stated impossibility never
  occurs. The real edge is the chat minimum (MAJ-001).
- **Recommendation**: Replace it with the chat-minimum rule.

#### [MIN-006] Live-drag performance is not budgeted
- **Lens**: Incompleteness · **Affected**: US-2.1
- **Description**: Relaying out a long chat transcript on every pointer move can stutter visibly.
- **Recommendation**: Update the width with requestAnimationFrame and a CSS variable during the drag, commit
  on release, and add a smoothness check to the demo list.

#### [MIN-007] The spec does not list its own blocking open questions
- **Lens**: Inoperability · **Affected**: Header, §14
- **Description**: Q1 and Q2 are unanswered, and §10 says both must be answered before wave 1. The status line
  does not say so.
- **Recommendation**: Add "Blocking questions: Q1, Q2 (and Q3 from MAJ-004)" to the header.

#### [MIN-008] E2E placement is not planned
- **Lens**: Inoperability · **Affected**: §12 test 16
- **Description**: New Playwright specs must be placed in `tests/e2e/shards.json`, or they run in no shard.
- **Recommendation**: Name the shard in the spec.

#### [MIN-009] Shared browser deep links embed session IDs
- **Lens**: Insecurity · **Affected**: §8.2, US-7
- **Description**: The spec encourages shared links carrying `session` and `agent`. It relies on the gateway's
  existing per-user authorization of the live stream (ADR-044 cookie) without stating or testing it.
  Whether a different user opening the link is refused per user is **Unknown** (not checked).
- **Recommendation**: State the assumption. Add a test that a second account following the link sees a
  visible denial and not the stream.

---

### Observations

#### [OBS-001] `widthMemoryKey` per panel is unnecessary indirection
- **Lens**: Overcomplexity · **Affected**: §8.1
- **Suggestion**: Every panel would implement the same function. The shell should derive the key from
  `id × context.workspaceId`, and panels should not supply it.

#### [OBS-002] Manual-tab presence detection is heavy machinery for a rare case
- **Lens**: Overcomplexity · **Affected**: SP-9, §8.3
- **Suggestion**: The founder chose it, so it is not a defect. Still, the BroadcastChannel presence protocol,
  its affordance, its clear-on-close handling and the extra tests all serve the case "user pasted a full-page
  URL by hand". Consider offering the founder the option of covering app-opened tabs only in wave 1, with the
  manual-tab handling deferred.

#### [OBS-003] Impact analysis was manual
- **Lens**: Inoperability · **Affected**: §3.2
- **Suggestion**: Rerun GitNexus `impact` on `UiStore`, `AppShell` and `WorkspaceTabBar` before the build
  (repo rule), and paste the results into §3.2.

---

## Structural Integrity (plan-spec mode)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1..US-9 |
| Every acceptance scenario has BDD scenarios | FAIL | 12 gaps, listed in MAJ-011 |
| Every BDD scenario has a `Traces to:` reference | PASS | |
| Every BDD scenario has a test in the TDD plan | PASS | Mapped coarsely by category |
| Every FR appears in the traceability matrix | PASS | FR-015 mapped to the wrong tests (MAJ-011) |
| Every BDD scenario is in the traceability matrix | PASS | |
| Test datasets cover boundaries, edge cases and errors | FAIL | No pinned-sidebar rows (MAJ-001); no persisted-width-after-shrink rows (MAJ-009) |
| Regression impact addressed | PASS | §12 regression section; misses the re-dock regression (MAJ-006) |
| Success criteria are measurable | FAIL | SC-002 is verified by the demo, not production (MAJ-012); test 16 "where feasible" |

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap | Affected |
|---|---|---|
| Data loss / guard | Panel replacement with unsaved edits | CRIT-001 |
| Cross-window | Expanding twice does not re-navigate the existing tab; toggle does not spawn a blank tab | MAJ-002 |
| Keyboard conflict | Escape while driving the Browser | MAJ-005 |
| Re-dock | Pop-out close while another panel is open | MAJ-006 |
| Layout | Pinned sidebar plus panel at the ceiling keeps the chat minimum | MAJ-001 |
| History | Back and forward after opening, toggling or replacing a panel | MAJ-004 |
| Browser handover | Width change triggers settle/handover (FR-015) | MAJ-011 |
| Design system | Shell manifest, story, touch target, focus-visible | MAJ-008 |

### Dataset Gaps

| Dataset | Missing | Add |
|---|---|---|
| Width bounds | Pinned sidebar | window 1024 / 1280 / 1400 with sidebar 256 → expected ceiling respects the chat minimum |
| Width bounds | Transient shrink | stored 950 → window 1000 (700 shown) → window 1400 → 950 shown, 950 still stored |
| Panel param | Unregistered but valid id in wave 1 | `tasks` in wave 1 → dropped |
| Panel param | Mail without an agent | `mail` bare → behaviour per the extended Q2 |

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| `panel` URL param | ok | ok | ok | risk | ok | ok | Session IDs in shared links (MIN-009) |
| BroadcastChannel presence | ok | risk | ok | ok | ok | ok | Same-origin only; any same-origin tab can fake presence, which blocks opening the panel. Low risk; the "Open here anyway" escape hatch in MAJ-003 mitigates it |
| Width memory (localStorage) | ok | ok | ok | ok | ok | ok | Cosmetic; shared across accounts (MIN-004) |
| Pop-out handles | ok | ok | ok | ok | ok | ok | `opener` severed; keep it that way |

## Unasked Questions

1. When a panel opens, is a history entry pushed (Back closes it) or replaced? (MAJ-004)
2. What is the minimum chat width the ceiling must protect when the sidebar is pinned? (MAJ-001)
3. When a manual full-page tab is detected, may the operator still open the panel here? (MAJ-003)
4. Does the second Escape, after releasing the Browser wheel, close the panel? (MAJ-005)
5. What does `?panel=mail` without an agent do? (MAJ-010)
6. Are widths per user or per browser profile? (MIN-004)
7. Which story files and manifests land in which wave for SP-15? (MAJ-008)

## Questions for the founder

Genuinely undecided points only. The spec's own §14 Q1 (where the width is stored) and Q2 (what a bare
`?panel=browser` link does) are still open and are not repeated here. Answer in one line, e.g. "Q1 A, Q2 A".

**Q1 — How wide must the chat stay when a panel is dragged wide? (MAJ-001)**
Context: the founder set "max 70% of the window so the chat stays usable" (SP-3). With the sidebar pinned
(256px, from 1024px windows up), 70% leaves the chat about 51px on a 1024px window and about 164px on a
1400px window — not usable. Impact: the ceiling formula and the demo's row 3.
- **A** — keep 70% but also never let the chat go below a minimum width (for example 360px); the panel stops at whichever limit comes first. **Recommended.**
- **B** — keep 70% literally; accept a very narrow chat on smaller windows.
- **C** — a lower percentage instead (for example 60%), no minimum.

**Q2 — A panel is already open as a full page in another tab. What happens when I click its toggle here? (MAJ-003)**
Context: SP-9 says "switch to that tab instead of reopening the panel", but the spec in three places says
different things (switch, open here too, "stays/becomes available"). A browser can't always bring another
tab to the front reliably. Impact: one behaviour for engineers and a pass/fail demo row.
- **A** — don't open the panel here; show a note "Already open in another tab" with two buttons: "Switch to tab" (best effort) and "Open here anyway". **Recommended.**
- **B** — strict SP-9: only try to switch to the other tab; never open here.
- **C** — open the panel here as well and just show the note.

**Q3 — Should the browser's Back button close an open panel? (MAJ-004)**
Context: the open panel is part of the web address (SP-14). Each open or switch either adds a history step
(Back closes the panel) or replaces the current one (Back leaves the page). Impact: history behaviour and tests.
- **A** — replace: opening and switching panels adds no history steps; Back behaves as today. **Recommended** (panels are toggles, not pages).
- **B** — push: each open or switch is a step; Back closes or steps back through panels.

**Q4 — In the Browser panel, Escape first stops "driving" the remote browser. Should a second Escape close the panel? (MAJ-005)**
Context: Escape is today the accessibility exit from controlling the remote browser (`BrowserLiveView.tsx::handleKeyDown`).
The spec says Escape closes the panel. Impact: keyboard users, WCAG "no keyboard trap".
- **A** — yes: the first Escape stops driving; a second Escape (focus now outside the remote view) closes the panel, same as every other panel. **Recommended.**
- **B** — no: the Browser panel never closes on Escape; only its Close button closes it.

**Q5 — What does a link with `?panel=mail` but no mailbox agent do? (MAJ-010)**
Context: Mail is per workspace **and** per agent (email spec on `feature/email-mail`). Unlike the Browser, opening Mail starts nothing costly.
- **A** — links carry the agent (`panel=mail&agent=…`); a link without it opens the Mail panel on its "choose a mailbox" state. **Recommended.**
- **B** — treat it like the Browser (spec Q2 option A): drop the parameter and show just the chat.

**Q6 — Are remembered panel widths per user or per browser? (MIN-004)**
Context: two accounts signed in on one browser would share widths today's way.
- **A** — per user (the stored key includes the user). **Recommended.**
- **B** — per browser; accounts on one browser share widths.

**Q7 — Can the Storybook stories (SP-15) be the clickable demo (SP-10)? (MAJ-008)**
Context: SP-10 asks for a clickable demo with all six panels on sample content; SP-15 asks for Storybook
stories with sample data for the same six panels plus the shell. A static Storybook build is a static
snapshot that can be click-tested in a real browser. Building both means two sample-data sets that drift.
- **A** — yes: build the shell and panel stories first; the static Storybook build is the SP-10 demo, click-tested before the founder sees it. **Recommended.**
- **B** — no: a separate standalone demo for SP-10; stories come later.

**Q8 — When do the SP-15 stories land? (MAJ-008)**
Context: SP-15 fixes *which* stories, not *when*. Impact: the size of wave 0 versus later waves.
- **A** — shell, Library and Browser stories with wave 0/1; Mail with wave 2; Team, Tasks and Calendar with wave 3 — each panel's stories ship with the wave that moves it into the shell. **Recommended** (with Q7 A, the demo stand-ins for later panels become their first stories).
- **B** — all seven story sets up front, in wave 0.

## Verdict Rationale

**BLOCK** because of CRIT-001: SP-7 creates new paths that unmount the Library panel, and the spec guards
only two of them, so unsaved user edits are lost without a prompt. MAJ-001 to MAJ-006 are design-level
defects that a build would hard-code: the chat width guarantee, a tab-reuse mechanism that would blank or
reload the reused tab, a contradictory SP-9 behaviour, an unspecified URL/store/history model, Escape
collisions, and re-dock hijacking. MAJ-007 to MAJ-012 are specification gaps that make the demo's click-test
rows and the wave-1 acceptance ambiguous.

### Recommended Next Actions

- [ ] CRIT-001: add a `beforeLeave` hook and an FR covering every unmount path; add BDD and tests
- [ ] MAJ-001: define a chat minimum and a new ceiling formula; confirm with the founder
- [ ] MAJ-002 / MAJ-003: replace named-window reuse with a handle registry; settle one SP-9 behaviour (founder)
- [ ] MAJ-004: URL as source of truth, the routes that accept `panel`, push vs replace (new founder Q3)
- [ ] MAJ-005 / MAJ-006: scoped Escape rule; re-dock only when no panel is open
- [ ] MAJ-007 to MAJ-012: ARIA model for the strip, SP-15 plus design-system FRs, width write rules, per-panel deep-link context, traceability fixes, wave tags on FRs
