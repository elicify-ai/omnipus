# Feature Specification: Workspace side-panel shell (shared, resizable)

**Created**: 2026-09-26
**Status:** **Approved** (2026-09-26 — fix round 2 applied; the final round of the fixed two-round grill process)
**Input**: Founder interview output — `docs/internal/specs/spec-side-panel-shell.md` (request verbatim + Decisions Log SP-1..SP-31). Process order (founder, verbatim): "capture the requirements and update the design documents first, after another demo — let's do it properly." Requirements → this spec → clickable demo reviewed by the founder in a real browser (SP-10, split per SP-31) → spec review rounds → build.

**Review-fix round 1 (2026-09-26)**: grill-spec round 1 returned BLOCK
(`side-panel-shell-spec-spec-review.md`: 1 critical, 12 major, 9 minor, 3 observation).
The founder answered all eight grill questions in a post-grill interview — decisions
SP-16..SP-24 in the Decisions Log, applied throughout this revision. Q1/Q2 in the old
§14 (width storage, bare browser deep link) are answered (SP-20, SP-21). Review-finding
dispositions are in §19.

**Review-fix round 2 (2026-09-26, final)**: grill-spec round 2 returned REVISE
(`side-panel-shell-spec-spec-review-round2.md`: 0 critical, 13 major (MAJ-201..213),
11 minor, 3 observation). The founder answered the six product questions in a
post-grill interview — decisions SP-25..SP-31 — and every remaining finding carries a
stated default, applied as recommended. No blocking finding remains open; nothing is
escalated. Round-2 dispositions are in §19b.

**Post-approval units correction (2026-09-27, no behaviour change)**: building the wave-0
demo (commit `4861e3196`, fixed at `77d6c0aa6`) surfaced that the compact-dropdown
breakpoint was specified as a plain pixel number (1152px) but is implemented — correctly
— as a container query at `72rem` (Tailwind's `@6xl`), per this app's existing
container-query convention. This app clamps its root font-size to 14px, so `72rem`
resolves to **1008px**, not 1152px (`1152px` assumed the browser default of 16px/rem,
which this app does not use). The three spec mentions of "1152px" below (§4 US-5, §11's
compact-dropdown scenario, §15 reachability) are corrected to state the rule in rem with
the effective pixel value at this app's actual root, so the written number matches what a
user's browser does. The collapse behaviour itself is unchanged — this is a units/label
fix, not a new decision or a behaviour change, and needs no further grill round.

---

## 1. Context and scope

Every content surface that today lives beside the chat — Library, Live browser, and the
planned Mail panel — is hand-hosted: each is its own `<aside>` with its own hard-coded
width (`sm:w-[45%] sm:min-w-[320px] sm:max-w-[720px]`), its own header buttons, and its
own pop-out logic. The founder asked for one shared panel shell: every panel is a
slide-out beside the chat, resizable by dragging its border, with expand-to-new-tab, so
the conversation with the agent continues while any panel is open (SP-1, SP-2, SP-4).
Team, Tasks and Calendar join the same shell after Mail (SP-6, SP-8).

**Panel inventory (all six, SP-4/SP-6):**

| Panel id | Title | Content owner (today / planned) | Expand target (full page in a new tab) |
|---|---|---|---|
| `library` | Library | `LibraryExplorer` | `/library` (fullscreen pop-out route, exists) |
| `browser` | Live browser | `BrowserLiveView` | `/browser-live?session=…&agent=…` (exists) |
| `mail` | Mail | Mail panel (feature/email-mail, D11) | Mail full page (per email spec) |
| `tasks` | Tasks | `WorkspaceTasksTab` (narrow layout, SP-6) | `/workspaces/{id}/board` |
| `team` | Team | `WorkspaceTeamTab` (narrow layout, SP-6) | `/workspaces/{id}/team` |
| `calendar` | Calendar | `CalendarScreen` (narrow layout, SP-6) | `/workspaces/{id}/calendar` |

**Out of scope**: Settings stays a page, never a panel (SP-6). No redesign of any
panel's *content* (the Library explorer, the browser view, the tasks board are content
owners — only their hosting, width and header actions change). No new agent tools, no
gateway changes.

### Backend / contract impact — none

**This feature is frontend-only. No backend, gateway or wire-contract change is
required or permitted.** Every state this spec introduces lives in the SPA: the open
panel is a router search parameter under the existing hash route (SP-14), panel
identity and context are SPA store state (`src/store/ui.ts::UiStore`), and width memory
is browser-local storage — **decided SP-20** (localStorage, keyed per USER + panel +
workspace; no wire contract) — in the pattern of the existing persisted stores
(`src/store/sidebar.ts`, `src/store/chatPreferences.ts` — both use the Zustand persist
middleware). No new REST endpoint, no WebSocket frame, no `contracts/` edit, no
regeneration. A future server-side width sync WOULD be a contract-first change; it is
not part of this spec.

---

## 2. As-is: hosting and header inventory (no capability may be lost)

### 2.1 How panels are hosted today

| Aspect | Today | Evidence |
|---|---|---|
| Docking | Each panel is a plain `<aside>` flex sibling inside the root flex row of `AppShell` — a real side-by-side split with the chat column (`flex-1 min-w-0`), which shrinks automatically | `src/components/layout/AppShell.tsx::AppShell` (render comments at `<BrowserLivePanel />` / `<LibraryPanel />`) |
| Width | Hard-coded, not resizable: `w-full sm:w-[45%] sm:min-w-[320px] sm:max-w-[720px]` | `src/components/library/LibraryPanel.tsx::LibraryPanel` (aside className); `src/components/browser/BrowserLivePanel.tsx::BrowserLivePanel` (same classes) |
| State | Two independent store slices — `browserPanel: {sessionId, agentId} \| null` and `libraryPanel: {workspaceId?} \| null` — so Library and Browser can be open **simultaneously** today | `src/store/ui.ts::UiStore` |
| Phone (<640px) | Panel takes over full width; the chat region is `inert` (a JS matchMedia signal, since a CSS breakpoint cannot gate an HTML attribute) | `src/components/layout/AppShell.tsx::AppShell` (`isPhoneViewport`, `inert`) |
| Retired overlay variant | The slide-out Sheet overlay + pin toggle are retired (2026-07-16, operator direction): open = always docked. Do not reintroduce | `src/store/ui.ts::UiStore` (browserPanel comment); `LibraryPanel.tsx` module doc |
| Pop-out (Library) | `window.open('/#/library?workspace=…&path=…&folder=…', '_blank', 'noopener,noreferrer')`, carries the current selection, then CLOSES the docked panel; pop-out close re-docks the docked panel via BroadcastChannel | `src/components/library/LibraryPanel.tsx::LibraryPanel.handlePopOut`; `src/routes/_app/library.tsx::LibraryRoute` |
| Pop-out (Browser) | Opens `about:blank` in a named-free new tab, severs `opener`, owns the window handle, closes the docked panel synchronously (`flushSync`), re-docks on pop-out close | `src/components/browser/BrowserLivePanel.tsx::BrowserLivePanel.handlePopOut`, `watchPopoutClosed` via `src/lib/browserLiveHandoff.ts` |
| Workspace "Library" tab | The `media` route is a redirect stub: opens the Library panel scoped to the workspace and redirects to Chat (the URL never dead-ends) | `src/routes/_app/workspaces.$workspaceId.media.tsx::WorkspaceMediaRedirect` |
| Entry points | Sidebar "Library" (virtual root); top-bar "Open library" (scoped) and "Open browser"; chat transcript "Watch live" on browser tool-calls | `src/components/chat/ChatControls.tsx` (openers), `src/store/ui.ts` slices |

### 2.2 Header capability inventory — Library panel

Everything in the Library panel's header row today (`src/components/library/LibraryExplorer.tsx::LibraryExplorer` toolbar row). The shell migration must preserve every capability below; only the last two move to the shell header:

| Control | Kind | Behaviour today | Fate under the shell |
|---|---|---|---|
| Breadcrumb nav (root / workspace / path segments) | Nav | Clickable crumbs; `library-crumb-*` test ids | **Stays in panel content header** (content-specific) |
| Show hidden toggle | Switch | Per-workspace hidden-files toggle | **Stays in panel content** |
| Mounts count pill | Status | Passive readout of mounted folders + broad-grant colour | **Stays in panel content** |
| Hidden file input (upload) | Hidden | Feeds `LibraryCreateMenu` upload | **Stays in panel content** |
| Create menu (+) | Menu | New folder / New note / Upload / Add mount / Manage mounts / New vault / New workspace | **Stays in panel content** |
| Unsaved-edits guard | Gate | Pop-out AND close go through `confirmDiscardLibraryEdits()` | **Must wrap EVERY shell action that closes or replaces this panel** — Expand, Close, open-another-panel (any entry point), deep-link replace, workspace-switch re-target (CRIT-001 fix, FR-013) |
| "Open in new tab" (`library-popout-button`) | Action | Selection-carrying pop-out, closes docked panel (C4) | **Moves to the shell header** (Expand action) — panel keeps the behaviour, not the button |
| Close (`library-close-button`) | Action | Closes panel (through the guard) | **Moves to the shell header** (Close action) |

### 2.3 Header capability inventory — Browser panel

The Browser panel's chrome is two fixed-height rows plus overlays (`src/components/browser/BrowserLiveView.tsx::BrowserLiveView` renders `BrowserLiveTabStrip` row A + `BrowserLiveToolbar` row B):

| Control | Row | Behaviour today | Fate under the shell |
|---|---|---|---|
| Browser-session tab strip (switch / close / open-new browser tab) | A | Remote browser tabs, not app tabs (`browser-tab-*` test ids) | **Stays in panel content** (content-specific) |
| "Pop out" (`ArrowSquareOut`) | A | Owned pop-out handover (see §2.1) | **Moves to the shell header** (Expand action) — ownership handover behaviour preserved |
| Close | A | `onClose` → `closeBrowserPanel` | **Moves to the shell header** |
| Back / Refresh / Stop | B | Remote navigation; disabled while disconnected | **Stays in panel content** |
| Address bar (omnibox) | B | URL/search submit; min-width floor is load-bearing | **Stays in panel content** |
| Agent identity chip + drive-status chip | B | 7-state chip (working / you-driving / annotating / error / reconnecting / also-viewing / click-to-drive) | **Stays in panel content** |
| Annotate toggle | B | Only when `canAnnotate` (same-JS-realm requirement, ADR-039) | **Stays in panel content** |
| Mute toggle | B | Only when the stream has audio | **Stays in panel content** |
| Input-error + viewport-handoff overlays | Body | Visible WebRTC failure + Retry (ADR-061: never a silent degrade) | **Stays in panel content, untouched** |

**Kept per-panel differences (justified per SP-4):** the Browser's pop-out is an
*ownership handover* (the original tab closes its panel, keeps a window handle, and
re-docks on pop-out close — `BrowserLivePanel.tsx::BrowserLivePanel.handlePopOut`);
the Library's pop-out *carries the selection* and re-docks on close with the last-viewed
workspace (`LibraryPanel.tsx` C4). Both are content behaviours executed through the
shell's single Expand action — the shell does not need to know them.

---

## 3. Existing codebase context

GitNexus MCP tools are not connected in this session; impact rows are from manual
Read/Grep exploration and are labelled accordingly (permitted fallback).

### 3.1 Symbols involved

| Symbol | Role | Notes |
|---|---|---|
| `src/store/ui.ts::UiStore` (browserPanel, libraryPanel) | **modifies** | The two slices collapse into one at-most-one-open panel state |
| `src/components/layout/AppShell.tsx::AppShell` | **modifies** | Hosts the shared shell instead of two bespoke panels; `inert` logic generalizes to any open panel |
| `src/components/library/LibraryPanel.tsx::LibraryPanel` | **modifies** | Becomes shell content; keeps pop-out/re-dock behaviour behind the shell's Expand |
| `src/components/browser/BrowserLivePanel.tsx::BrowserLivePanel` | **modifies** | Becomes shell content; keeps owned-popout handover behind Expand |
| `src/components/library/LibraryExplorer.tsx::LibraryExplorer` (toolbar row) | **modifies** | Loses its own pop-out/close buttons (moved to shell header); keeps all content controls |
| `src/components/browser/BrowserLiveTabStrip.tsx::BrowserLiveTabStrip` | **modifies** | Loses Pop out / Close buttons; keeps tab strip |
| `src/components/workspaces/WorkspaceTabBar.tsx::WORKSPACE_TABS` / `resolveActiveSegment` | **modifies** | Tab entries become panel toggles (SP-11); selection state reads panel state, not route only |
| `src/routes/_app/workspaces.$workspaceId.media.tsx::WorkspaceMediaRedirect` | **modifies** | Redirect target becomes the deep-link chat URL with `?panel=library` instead of store-call + redirect |
| `src/routes/_app/library.tsx::LibraryRoute`, `src/routes/_app/browser-live.tsx::BrowserLiveRoute` | **extends** | Stay as expand targets; unchanged behaviour |
| `src/components/chat/ChatControls.tsx` (openers) | **extends** | Same store openers; now route through one-at-a-time shell state |
| `src/lib/libraryHandoff.ts`, `src/lib/browserLiveHandoff.ts` | **extends** | BroadcastChannel precedents the already-open-tab detection builds on (imported by both panels — verified via imports) |
| `src/components/library/preview/unsavedGuard.ts` (dirty flag + `confirmDiscardLibraryEdits`) | **extends** | The guard CRIT-001 requires on every close/replace path; today only LibraryPanel's own Close/Pop-out call it. Note its dialog is hosted inside the mounted `LibraryExplorer` and the dirty flag clears on the editor's unmount — so the guard MUST run BEFORE the store switches panels (the transition gate in FR-013) |
| `src/routes/_app/browser-live.tsx::BrowserLiveRoute` | **extends** | Already refuses a link with no `session`/`agent` (visible "Missing session or agent" message) — the grounding for SP-21's drop-the-param degrade |
| `src/routes/_app/workspaces.$workspaceId.chat.tsx` | **extends** | Declares NO `validateSearch` today — the `panel` search param must be added there (§8.2 names the routes) |

### 3.2 Impact assessment (manual exploration — Inferred, not GitNexus)

| Symbol | Risk | d=1 dependents | d=2 dependents |
|---|---|---|---|
| `src/store/ui.ts::UiStore` panel slices | **HIGH** | `AppShell`, `LibraryPanel`, `BrowserLivePanel`, `ChatControls`, `media` redirect route, pop-out routes' handoff reactions | Every test mounting `AppShell` or asserting two-panels-open behaviour; route tests (`-workspaces.$workspaceId.media.test.tsx`, `-library.deep-link.test.tsx`) |
| `WorkspaceTabBar` toggle semantics | MEDIUM | `WorkspaceTabContainer` (hosts the bar + `ChatControls`), compact dropdown | Playwright specs selecting `workspace-tab-*` |
| `LibraryExplorer` toolbar | MEDIUM | `LibraryPanel`, `/library` route (renders explorer without `onClose`/`onPopOut` today — unchanged) | Library component tests asserting `library-popout-button` |
| `BrowserLiveTabStrip` buttons | MEDIUM | `BrowserLiveView` | `BrowserLiveView.*.test.tsx` suites asserting pop-out/close |

The two-slices-to-one store change is the blast-radius centre: today Library and Browser
can be open at once (`src/store/ui.ts` has two independent slices); after SP-7 they
cannot — that is an intentional behaviour change, called out here so reviewers do not
flag it as a regression to "fix".

**OBS-003 disposition**: this assessment is still manual (GitNexus was unavailable in
both the review and this fix round). Before build starts, rerun GitNexus `impact` on
`UiStore`, `AppShell` and `WorkspaceTabBar` (repo rule) and paste the results here.

### 3.3 Cluster placement

Frontend shell/chrome cluster (`src/components/layout`, `src/store`), spanning the
library and browser component clusters and the workspaces cluster (tab bar). No backend
cluster touched (§1).

---

## 4. User stories & acceptance scenarios

Priorities: wave-1 shell behaviour is P0; cross-tab/deep-link conveniences P1; wave-3
panel adoption P1 (sequenced, SP-8); a11y P0 (non-negotiable, SP-3). **Every US and FR
carries a wave tag** (MAJ-012): `[wave 1]` ships with the shell+Library+Browser wave,
`[wave 2]` with Mail, `[wave 3]` with Team/Tasks/Calendar; behaviour tagged `[wave 1]`
holds for every later panel automatically.

### US-1 — One shared shell hosts every panel (SP-4) — P0 [wave 1]

An operator wants Mail, Library and Browser to feel like one mechanism — same docking,
same header, same actions — so that learning one panel teaches them all, and Mail can
be added as content without new hosting code. Each panel supplies only its content and
its expand target; the shell owns title, Expand (open in new tab), Close, the resize
border, docking beside the chat, phone takeover, and Escape-to-close. No capability
from today's Library/Browser headers may be lost (§2.2, §2.3 inventories).

**Why this priority**: it is the founder's core ask ("the underlying panel for all 3
must be the same shared component") and every other story composes on it.

**Independent test**: mount the shell with a sample panel; assert title, Expand and
Close render, the panel docks beside (not over) the chat, and Close removes it.

**Acceptance scenarios**:

1. **Given** the shell hosting a panel, **When** the panel is open, **Then** the shell
   header shows the panel title, an Expand ("Open in new tab") action and a Close
   action, and the panel content area renders the panel's own content with its content
   controls intact.
2. **Given** the Library panel under the shell, **When** the operator clicks Expand or
   Close **with unsaved Library edits**, **Then** the existing discard-confirmation
   prompt appears and a "cancel" answer leaves the panel open.
3. **Given** the shell hosting any panel OTHER than Browser, **When** the operator
   presses Escape while focus is inside the shell and no modal layer is above the
   panel, **Then** the panel closes; **when a modal is above it, Escape closes the
   modal first** and the panel stays. **Browser exception (SP-19)**: Escape inside the
   Browser panel NEVER closes the panel — Escape keeps its existing meaning ("stop
   driving the remote browser", `BrowserLiveView.tsx::handleKeyDown`, WCAG 2.1.2);
   the Browser panel closes only via its Close control.
4. **Given** all six panels registered (waves 1–3 combined), **When** each opens in
   turn, **Then** each docks with the same border, header and actions — no
   panel-specific hosting code beyond content + expand target.

### US-2 — Resizable width with shared defaults (SP-2, SP-3, SP-17) — P0 [wave 1]

An operator wants to decide how much space a panel gets by dragging its border — the
same interaction for every panel. **Width rules (SP-17, as amended by SP-25)**: the
panel docks side-by-side with the chat — the chat column stays ≥360px AND the panel
stays ≥320px. The panel's maximum width is
`min(70% of the row, row − sidebar width − 360px)` (all measured on the root
flex row — MIN-001's basis; the sidebar term is the pinned sidebar's current width
(256px), 0 when the sidebar is not pinned — a user preference enforced only at
≥1024px, `src/store/sidebar.ts::SIDEBAR_PIN_BREAKPOINT`; below 1024px the sidebar is
never in the row). **At every viewport ≥680px these floors always fit** (row − sidebar
≥ 680 − 0 = 680 = 360 + 320, since the sidebar term is 0 below 1024px and 256px at
≥1024px leaves row ≥ 768), so the panel ALWAYS docks above the breakpoint — there is
NO overlay state (SP-25 deleted it). **Below 680px the panel takes over the full
screen** (US-8, SP-25; was 640px before SP-25). Minimum 320px; default =
`clamp(0.45 × row, 320px, min(720px, ceiling))` — the default is capped by the same
ceiling (MAJ-203: an uncapped 45% broke the chat's 360px floor at 1024px with the
sidebar pinned); double-click on the border resets. Keyboard accessible: focusable
separator, arrow keys, Home/End.

**Why this priority**: the feature's namesake ask; the demo gate (SP-10/SP-31) shows it.

**Independent test**: render the shell, drag the border, assert width changes within
bounds and the chat column never drops below 360px; narrow the window past 680px and
assert the panel takes over the full screen.

**Acceptance scenarios**:

1. **Given** an open panel, **When** the operator drags the border left/right, **Then**
   the panel width follows the pointer live and the chat column width changes by the
   same amount (a real side-by-side split).
2. **Given** a drag that would exceed the bounds, **When** the pointer passes 320px or
   the SP-17 ceiling `min(70% of row, row − sidebar − 360px)`, **Then** the width stops
   at that bound (hard clamp, no rubber-banding, chat never below 360px).
3. **Given** an open panel, **When** the operator double-clicks the border, **Then**
   width resets to the default (`clamp(0.45 × row, 320px, min(720px, ceiling))`).
4. **Given** a window resize while a panel is open, **When** the window shrinks, **Then**
   the panel re-clamps to the new ceiling (transient — the stored width is untouched,
   MAJ-009); **when the viewport crosses below 680px, the panel takes over the full
   screen** (US-8, SP-25) — the chat column is never squeezed below 360px on a ≥680px
   viewport.
5. **Given** the Browser panel being resized, **When** keyboard/drag input SETTLES
   (300ms after the last input — MIN-205), **Then** the existing remote-viewport resize
   handover runs (its visible "Resizing browser… / input will resume" status shows; a
   failure is visible with Retry — never a silent stall — reusing
   `BrowserLiveView`'s existing handover UI). Handover runs once per settle, not once
   per keypress.

### US-3 — Width memory per user × panel × workspace (SP-13, SP-3, SP-20) — P0 [wave 1]

An operator who widens Tasks in one workspace and narrows Library at the virtual root
wants each choice remembered where they made it. The remembered width is keyed **per
USER + panel + workspace** (SP-20: two accounts on one browser never share widths —
MIN-004 resolved). Storage is browser-local localStorage (SP-20), in the pattern of the
SPA's existing persisted stores. Panels opened with no workspace context (sidebar
Library at the virtual root) use the `app` bucket; **the Browser panel ALWAYS uses the
`app` bucket** — its identity is a browser session, not a workspace (MAJ-201), so it
has ONE width per user regardless of which session/screen it was opened from.

**Write rules (MAJ-009 + MIN-205)**: the stored value is written ONLY on a settled user
choice — drag release, keyboard SETTLE (300ms after the last keypress), or reset. A
reset **deletes** the stored value (the default is re-derived at read time, never
stored — storing a default in px would freeze it to one window size). Storage is ONE
localStorage key per user × panel × workspace under a namespaced prefix,
`panel-width:<username>:<panelId>:<workspaceId|app>` (MIN-204: one key per combination;
the SPA's account record carries only a display `username`, `src/store/auth.ts`, so
that is the key's user term). Deleted-workspace entries are pruned on the next write —
ONLY the signed-in user's own entries (MIN-204: other accounts' entries are never
touched). A window-driven re-clamp (window shrink, zoom change) is **transient**: it
changes the applied width for as long as the window is that size and MUST NOT overwrite
the stored value; the clamp is re-applied at read time.

**Why this priority**: part of the resize story (SP-3); wrong granularity would force a
rebuild of the persistence key later.

**Independent test**: set different widths for two panels in one workspace and a third
width in another workspace; reopen each; assert each remembers its own. Shrink the
window (transient re-clamp), restore it, and assert the stored width comes back.

**Acceptance scenarios**:

1. **Given** the operator drags the Library border to a new width in workspace A,
   **When** they close and reopen the panel in workspace A, **Then** the
   remembered width applies (clamped per US-2 bounds).
2. **Given** remembered widths exist for workspace A, **When** the operator opens the
   same panel in workspace B, **Then** workspace B's own remembered width applies —
   never workspace A's — and the same workspace under a different user account shows
   that account's own widths.
3. **Given** no remembered width for a user × panel × workspace pair, **When** the
   panel opens, **Then** the default (45% clamped 320–720px) applies.
4. **Given** the operator resets via double-click, **When** the panel reopens later,
   **Then** the default applies again (the reset DELETED the stored value, not stored a
   default in px).
5. **Given** the operator switches workspace while a panel is open, **When** the new
   workspace's context loads, **Then** the per-panel workspace-switch rule applies
   (SP-29, FR-020): Browser stays anchored to its own session (never closed, never
   moved); Library follows ONLY if it was opened scoped to the workspace being left
   (a Library opened from the all-workspaces root stays as it is — today's behaviour);
   Tasks, Calendar and Mail follow the new workspace — **after passing the unsaved-edit
   guard when the outgoing panel is a following Library** (FR-013); the width bucket
   re-resolves with the panel's CURRENT context (a following panel re-reads the new
   workspace's bucket; a root-scoped Library stays in `app`).
6. **Given** a window shrink re-clamped an open panel from 950px to 640px (1400px →
   1000px viewport, sidebar unpinned — ceiling = 1000 − 360 = 640), **When** the
   window returns to 1400px, **Then** the panel returns to 950px and the stored value
   was never overwritten (transient re-clamp, MAJ-009).

### US-4 — One panel at a time, never silently discarding edits (SP-7, CRIT-001) — P0 [wave 1]

The chat always keeps enough room, and attention has one focus: opening a panel replaces
the open one. This intentionally changes today's behaviour, where Library and Browser
can be open simultaneously (§3.2) — approved by SP-7. **Because every open-other
transition unmounts the open panel, EVERY transition that closes or replaces the open
panel runs the outgoing panel's leave guard first** (FR-013; the CRIT-001 fix): no
path — tab toggle, sidebar, ChatControls, "Watch live", deep-link replace, workspace
switch, header Close, Expand — discards unsaved Library edits without the
"discard unsaved changes?" prompt.

**Why this priority**: founder decision SP-7; drives the store shape (§3.2) and the
CRIT-001 transition gate.

**Independent test**: open Library with an unsaved edit; trigger each close/replace path
in turn; assert the prompt fires on every one and cancelling leaves Library open and
intact; repeat with a clean Library and assert each path replaces silently.

**Acceptance scenarios**:

1. **Given** Library is open, **When** the operator opens Browser (any entry point:
   tab strip, "Watch live", "Open browser"), **Then** Library closes and Browser docks
   in its place.
2. **Given** a panel is open, **When** the operator clicks its own tab-strip entry
   again, **Then** the panel closes (a second click on a toggle closes — SP-11).
3. **Given** a panel is open, **When** a deep link with a different `panel` value is
   followed, **Then** the open panel is replaced by the linked one.
4. **Given** Library open WITH unsaved edits, **When** ANY close/replace path fires
   (open-another via tab toggle, "Watch live", "Open browser", deep-link replace,
   workspace switch, header Close, Expand), **Then** the discard-confirmation prompt
   appears on every path, and cancelling leaves the Library panel open with the edit
   intact.
5. **Given** Library open with a CLEAN editor state, **When** any of those paths fires,
   **Then** the replacement happens immediately with no prompt.

### US-5 — Tab-strip entries are panel toggles (SP-11) — P0 [wave 1 strip model; wave 3 entries]

Chat stays the page underneath. Panel entries are TOGGLES, not tabs — the accessible
model (MAJ-007): **the strip stops being a `role="tablist"` of all entries; Chat and the
workspace-name entry keep tab/link semantics (`aria-current="page"` on Chat while it is
the underlying page), and panel entries become toggle buttons with `aria-pressed`**
(the existing `role="tablist"` + `aria-selected` on `WorkspaceTabBar.tsx` cannot express
independent toggle states). The compact view-switcher dropdown (container query at
`72rem` — **1008px** at this app's clamped 14px root, corrected from the earlier
1152px/16px-root figure, 2026-09-27, units only — via `WorkspaceTabContainer.tsx`'s
`@container`) carries the same toggle semantics: panel
entries show a pressed/checked state while their panel is open, and the trigger keeps
its "Active ▾" label showing the underlying page's label ("Chat") — the trigger label
does NOT change to the panel's name, because Chat stays the page underneath.
**Mixed mode (waves 1–2, MAJ-012)**: entries for panels not yet registered
(Tasks/Team/Calendar in waves 1–2) remain ordinary navigation links to their full-page
routes, with no `aria-pressed`; they become toggles when their panel registers. Settings
remains a navigation entry to a page; workspace-name → settings unchanged.

**Why this priority**: the discovery surface for every panel; without it panels are
reachable only through side entry points.

**Independent test**: click the Library tab entry; assert the panel opens and the entry
shows `aria-pressed=true`; click again; assert closed. In mixed mode, assert Tasks is a
link without pressed state.

**Acceptance scenarios**:

1. **Given** no panel open, **When** the operator clicks the Tasks entry (wave 3), **Then**
   the Tasks panel opens over the chat page (the route stays on Chat), the entry shows
   pressed, and the URL gains `?panel=tasks` (US-7).
2. **Given** the Tasks panel open, **When** the operator clicks Tasks again, **Then**
   the panel closes and the entry clears its pressed state.
3. **Given** the Library panel open, **When** the operator clicks the Calendar entry,
   **Then** Calendar's panel replaces Library's (SP-7) and the pressed state moves.
4. **Given** the current route IS a panel's full-page route (deep link to
   `/workspaces/{id}/team`), **When** the operator looks at that panel's tab entry,
   **Then** it shows active without opening a panel (the page is the view; clicking the
   entry navigates back to Chat and toggles the panel on in that case).
5. **Given** a narrow container (compact dropdown), **When** the operator uses the
   dropdown entries, **Then** the same toggle semantics and pressed states apply as the
   full strip, the trigger label stays "Chat" (the page underneath), and mixed-mode
   links vs toggles render the same as on the strip.
6. **Given** the tab strip markup change (tablist → toggle group), **When** existing
   Playwright specs run, **Then** the `workspace-tab-*` test ids persist unchanged
   (semantics change; markup ids do not — §12 regression plan).

### US-6 — Expand closes the source; already-open tabs are switched to (SP-12, SP-18) — P1 [wave 1]

"Open in new tab" opens the panel's full-page view in a new browser tab AND closes the
panel in the original tab (chat regains full width) — today's Library (C4) and Browser
(flushSync close) already do this; the shell generalizes it. **One behaviour, stated
once (SP-18, extended by SP-30)**: invoking ANY entry point for a panel when that
panel's full-page tab is ALREADY open for the same scope means SWITCH to that tab —
never "open here too", never a duplicate. **Every entry point participates (SP-30)**:
the tab-strip toggle, Expand, the sidebar "Library" button, "Open library",
"Watch live", the chat draft link (Mail), and the retargeted `media` redirect — one
list, no implicit toggle/Expand-only rule. "Already open" is scoped by the panel's
IDENTITY (§8.1): a tab counts as already open only when it currently shows that panel
for the SAME workspace (workspace panels) or the SAME session + agent (Browser,
MAJ-201); the same panel for a DIFFERENT scope opens normally here. **Matching is by
CURRENT scope, not open-time scope (MAJ-213)**: a full-page tab that navigated to
another workspace is no longer "already open" for the workspace it left. The honest
browser constraint stands (§8.3): only tabs Omnipus opened itself (window handle in
the registry) can be reliably focused; a manually-opened tab gets the realistic
degrade — detected via the BroadcastChannel presence list, no duplicate opened, no
docked panel opened here either, and a visible "already open in another tab — switch"
affordance whose switch action is best-effort focus (`window.focus` is not guaranteed —
the affordance is the guarantee). The reuse mechanism is an **in-memory handle
registry** (§8.3, MAJ-002) — never a re-navigating `window.open(url, name)`.

**Why this priority**: cross-window behaviour, fully specified by founder choices
(SP-12 recommended option chosen; SP-18 answered the round-1 grill; SP-30 answered the
round-2 grill).

**Independent test**: open Library, Expand; assert new tab opens and the docked panel
closed. Re-invoke Library from the toggle, the sidebar button and "Open library" in
turn: with the app-opened tab present each is focused and no docked panel opens here;
with a manually-opened tab present the affordance shows; no path duplicates a tab.

**Acceptance scenarios**:

1. **Given** any panel open, **When** the operator clicks Expand, **Then** the panel's
   expand-target route opens in a new browser tab and the docked panel closes in the
   original tab (chat regains the width).
2. **Given** the app itself opened a panel's full-page tab earlier and still holds its
   window handle (§8.3), **When** the operator invokes ANY entry point (toggle,
   sidebar "Library", "Open library", "Watch live"), **Then** the existing tab is
   focused via the handle — no second tab, no navigation or reload of that tab, and no
   docked panel opens here.
3. **Given** a panel's full-page tab exists that the USER opened manually (no window
   handle; e.g. via browser chrome or a shared link), **When** the operator invokes any
   entry point, **Then** the presence list answers (§8.3), the docked panel
   does NOT open here, no duplicate opens, and a visible "already open in another tab —
   switch" affordance offers the best-effort switch.
4. **Given** the manually-opened tab is then closed, **When** the operator invokes the
   entry point again, **Then** the panel opens normally (presence detection cleared — no
   stale "already open" state).
5. **Given** a Library full-page tab open for workspace A, **When** the operator clicks
   the Library toggle in a tab showing workspace B, **Then** that is NOT "already open"
   (per-workspace scoping, SP-18): Library opens docked here for workspace B normally.
6. **Given** an app-opened Library full-page tab for workspace A that the operator
   navigated to workspace B inside the tab, **When** the Library toggle is clicked in
   a tab showing workspace A, **Then** it is NOT "already open" for A anymore
   (matching is by CURRENT scope, MAJ-213): Library opens docked here for A normally,
   and no context post is sent to the tab now showing B (never yanked back).

### US-7 — URL-addressable panel state (SP-14, SP-21, SP-22, SP-23, SP-27, SP-28) — P1 [wave 1]

The open panel is part of the page address (`…/chat?panel=team`); reload and shared
links restore it. Hash routing applies (the router's search lives in the `#/` fragment
— `src/components/library/LibraryPanel.tsx::LibraryPanel.handlePopOut` comment;
`src/routes/_app/-library.deep-link.test.tsx`). **Panel toggles are not pages
(SP-22, amended by MAJ-206)**: on desktop widths, opening, closing and switching a
panel uses history REPLACE, never push — and on Back/Forward the app's OWN state wins:
a restored history entry's stale `panel` value is ignored and immediately re-projected
from the store (replace), so Back behaves exactly as it does today (panels never
reopen from old history entries). **EXCEPTION — phone mode (SP-26)**: opening a panel
below 680px pushes ONE history step, and Back over that step closes the panel. Valid
`panel` values are the REGISTERED panel ids (MAJ-012): an unregistered-but-future id
(`tasks` in wave 1) is treated exactly like an unknown id — dropped with a URL replace.
**Browser exclusion (SP-28)**: `panel=browser` is NEVER restored from a reload or a
shared link — the param is always dropped; the Browser panel reopens only from its
entry points ("Watch live", "Open browser"), and no session id is ever placed in a
shareable chat-link. **Shared links through sign-in (SP-27)**: after signing in, the
user returns to the link they opened — workspace and panel both restored, not dropped
to `/`.

**Why this priority**: founder-chosen recommended option (SP-14); makes the one-panel
state shareable and reload-stable.

**Independent test**: open a panel, copy the URL, reload in a fresh tab; assert the
same panel restores; sign out, open the shared link, sign in, and assert the linked
workspace and panel come back (SP-27); press Back after toggling panels and assert
history is unchanged from today's behaviour.

**Acceptance scenarios**:

1. **Given** a panel open on a workspace Chat route (**any panel EXCEPT Browser**,
   SP-28), **When** the operator reloads, **Then** the same panel restores at its
   default view with its width.
2. **Given** a shared link carrying `?panel=calendar` (wave 3), **When** it is opened
   by a signed-out colleague, **Then** sign-in returns them to the link they opened
   (SP-27): the linked workspace's chat with the Calendar panel restored.
3. **Given** a link with no `panel` param, **When** opened, **Then** no panel opens
   (today's behaviour for links).
4. **Given** a link with an unknown or UNREGISTERED panel id, **When** opened, **Then**
   the param is dropped (URL replaced) and no panel opens — the chat still renders; the
   failure is never silent-invisible (the URL visibly changes).
5. **Given** ANY `?panel=browser` link (bare, or with session/agent context), **When**
   opened (fresh or reloaded), **Then** the param is dropped with a URL replace, no
   session is created, and just the chat renders (SP-21 + SP-28: the Browser panel is
   excluded from URL restore entirely; no session id is ever placed in a shareable
   chat-link; "Watch live" is the only way back into a live browser).
6. **Given** `?panel=mail` with no agent/mailbox selected, **When** opened, **Then** the
   Mail panel opens on its "choose a mailbox" state (SP-23 — Mail starts nothing
   costly; the link MAY carry `&agent=…` to land directly on that mailbox, SP-23).
7. **Given** the operator opens, switches and closes panels, then navigates to
   Settings and back (history now holds entries with stale `panel` values), **When**
   they press the browser Back button onto such an entry, **Then** the app's own state
   wins: the entry's stale `panel` value is ignored, the panel state shown is the
   store's current state, and the URL is re-projected (replace) — Back never opens or
   switches a panel (SP-22 as amended by MAJ-206).
8. **Given** ANY router navigation while the Library panel has unsaved edits
   (deep link pasted in the address bar, Back/Forward, workspace switch via the
   sidebar), **When** the navigation is intercepted by the router blocker (MAJ-205,
   FR-013), **Then** the discard-confirmation appears and a cancel leaves the route,
   URL and panel unchanged — the navigation never happens.

### US-8 — Phone behaviour: takeover + three ways back to the chat (SP-3, SP-4, SP-25, SP-26) — P0 [wave 1]

Below **680px** the panel takes over the full screen and the chat region is `inert` —
today's behaviour generalized to every panel, with the breakpoint raised from 640px to
680px (SP-25, which also deleted overlay mode entirely). In phone mode there are
**THREE ways back to the chat (SP-26)**:

1. **The panel header's ✕ (Close)** closes the panel.
2. **The phone's Back gesture / hardware button**: opening a panel in phone mode adds
   ONE history step, so Back closes the panel and reveals the chat underneath. Desktop
   is UNCHANGED — SP-22's history-REPLACE stays exactly as specified in US-7; this
   history-PUSH is phone-mode-only.
3. **Swipe-to-close**: a horizontal touch drag that begins within 24px of the screen's
   LEFT edge, moving rightward — the panel slides right, revealing the chat (the chat
   sits conceptually to the left, matching the docked geometry). It closes on release
   after ≥96px of rightward travel, OR on release with velocity ≥0.4 px/ms after ≥48px
   of rightward travel; below the thresholds it springs back and nothing closes. It
   MUST NOT conflict with horizontal scrolling inside the panel's own content (a
   Library file carousel, a Calendar week view): a horizontal drag that begins on an
   element (or ancestor up to the panel root) that can scroll horizontally in the
   drag's direction is delivered to that scroller — it scrolls, the panel does not
   close; the edge-swipe recognizer owns the gesture only when the touch starts in the
   edge zone on a target with no horizontally-scrollable ancestor in the drag
   direction, or on such a scroller already at its left end. Content can also opt out
   entirely by claiming the pan.

**Why this priority**: preserves the inert-chat accessibility behaviour; the three
affordances are the founder's answer to "how does a phone user get back to the chat"
(SP-26).

**Independent test**: at <680px open a panel; assert full-width takeover and inert
chat; close via ✕, via Back, and via the edge swipe in turn; assert each closes the
panel and restores the interactive chat; drag inside a horizontal scroller (carousel
fixture) and assert it scrolls without closing the panel.

**Acceptance scenarios**:

1. **Given** a <680px viewport, **When** a panel opens, **Then** it occupies the full
   content width, the chat region is `inert` (not focusable, not clickable), and no
   resize handle is offered.
2. **Given** the takeover state, **When** the operator taps the header's ✕, **Then**
   the panel closes and the chat region becomes interactive again (SP-26 affordance 1).
3. **Given** the takeover state, **When** the operator uses the phone's Back
   gesture/button, **Then** the panel closes and the chat is revealed (SP-26
   affordance 2 — SP-26's history push); the chat page underneath was never navigated
   away from.
4. **Given** the takeover state, **When** the operator swipes from the left edge
   rightward past the thresholds, **Then** the panel closes (SP-26 affordance 3); a
   swipe below the thresholds springs back and the panel stays.
5. **Given** panel content with a horizontal scroller (Library carousel, Calendar week
   view), **When** the operator drags horizontally INSIDE it, **Then** the scroller
   scrolls and the panel does NOT close — the close-swipe and in-content horizontal
   scrolling are distinguishable (SP-26's non-conflict rule).
6. **Given** a viewport crossing 680px upward while a panel is open, **When** the
   layout re-evaluates, **Then** the panel docks at its remembered width with a resize
   handle (no stuck takeover state); the phone history step (SP-26) is collapsed
   (replaced away) when leaving phone mode.

### US-9 — Keyboard and screen-reader access (SP-3, SP-4) — P0 [wave 1]

The resize border is a focusable separator with complete assistive state; opening and
closing manages focus; panels are labelled landmarks. **Focus-return rules, per case
(MIN-002, extended MIN-208)**: toggling a panel closed returns focus to the toggle that
invoked it; Close/Expand in the shell header returns focus to the chat input (matching the
Browser's existing behaviour — `BrowserLivePanel.tsx::handlePopOut` focuses the chat
input today); an Escape close follows the same per-case rules (the panel's invoking
toggle, else the chat input); when the invoking control no longer exists (a collapsed
dropdown item, a relaid-out strip), focus falls back to the chat input; a deep-link
panel restored on page load moves NO focus (never steal focus on load).

**Why this priority**: SP-3 explicitly requires keyboard accessibility; non-negotiable.

**Independent test**: keyboard-only walkthrough — tab to separator, arrows adjust,
Home/End bound, Escape closes (except Browser, SP-19), focus lands predictably per the
per-case rules above.

**Acceptance scenarios**:

1. **Given** an open panel, **When** the operator tabs to the resize separator, **Then**
   it is focusable, exposes role `separator`, vertical orientation, an accessible name
   ("Resize <Panel title> panel"), `aria-controls` pointing at the panel element,
   `aria-valuetext` ("640 pixels wide"), and current/min/max width values
   (`aria-valuemax` updates on window resize — MIN-003).
2. **Given** the separator focused, **When** the operator presses Left/Right arrows,
   **Then** width adjusts by a fixed step (16px) with clamping; **Home/End** set minimum
   and maximum.
3. **Given** a panel open via a toggle click, **When** the panel closes, **Then** focus
   returns per the per-case focus rules (invoking control, else chat input); **when it
   opens by user action**, focus moves into the panel (its header) — and back out with
   Escape (except Browser per SP-19).
4. **Given** any panel open, **When** a screen-reader user lands on the panel, **Then**
   it is exposed as a complementary landmark labelled by its title (today's
   `aria-label` on the `<aside>` generalizes to the shell).

---

## 5. Behavioral contract (quick reference)

Primary flows:
- When a panel opens, it docks beside the chat in the shared shell with title, Expand,
  Close, and a resizable border; the chat keeps the remaining width.
- When another panel opens, the open one closes — through the leave guard (SP-7,
  FR-013); the chat never drops below its 360px floor (SP-17).
- When a tab-strip entry for a panel view is clicked, it toggles that panel (SP-11);
  Chat remains the page underneath.
- When Expand is clicked, the full-page route opens in a new tab and the docked panel
  closes (SP-12).
- When ANY entry point for a panel is invoked (tab toggle, Expand, sidebar "Library",
  "Open library", "Watch live", chat draft link, `media` redirect) and that panel's
  full-page tab is already open for the same identity scope, the app SWITCHES to that
  tab (handle-focus when it holds the handle; affordance + best-effort switch for a
  manual tab) — it never opens a duplicate and never also opens the docked panel here
  (SP-18 + SP-30, one list of entry points, stated once).
- When the panel's width is dragged, adjusted by keyboard (written on settle, 300ms
  after the last keypress), or reset (double-click), the new width is remembered per
  user + panel + workspace (SP-13, SP-20) — written only on a settled user choice,
  never on a window-driven re-clamp (MAJ-009, MIN-205).
- When a URL carries `?panel=<registered id>`, that panel opens; reload and shared
  links restore it; on desktop toggling panels never adds history entries and Back
  never re-opens a panel from a stale history entry (SP-14, SP-22, MAJ-206); in phone
  mode opening a panel pushes ONE history step that Back consumes (SP-26).
- When a shared link is opened signed-out, sign-in returns the user to the link they
  opened (workspace + panel), never dropped to `/` (SP-27).

Error flows:
- When ANY path closes or replaces the Library panel with unsaved edits, the
  discard-confirmation runs; cancelling leaves the panel open (FR-013, CRIT-001) —
  on app-initiated paths via the shell's `beforeLeave` gate, on URL-initiated paths
  (address-bar deep links, Back/Forward, workspace switch) via the router blocker
  (MAJ-205), and the "Open browser" path creates NO paid session before the guard
  passes (MIN-206).
- When an unknown or unregistered panel id arrives via URL, the param is dropped and no
  panel opens.
- When ANY `?panel=browser` link arrives (bare or with context), the param is dropped
  with a URL replace and no session is created — the Browser panel is never restored
  from reload or a shared link (SP-21 + SP-28); "Watch live" is the only way back in.
- When `?panel=mail` arrives with no mailbox selected, the Mail panel opens on its
  "choose a mailbox" state (SP-23).
- When the Browser panel cannot open its pop-out, today's visible toast + the panel
  staying open is preserved.
- When ANY panel's Expand cannot open its new tab (`window.open` returns null or
  throws — popup blocker), a visible error toast shows and the docked panel STAYS open
  (MAJ-209 — for every panel, never a silently vanished panel).
- When WebRTC video fails inside the Browser panel, the visible error + Retry
  (ADR-061) is untouched by the shell.

Boundary conditions:
- When width hits 320px or the SP-17 ceiling `min(70% row, row − sidebar − 360px)`, it
  clamps (drag, keyboard, and restore alike).
- At every viewport ≥680px the floors always fit (row − sidebar ≥ 680 = 360 + 320), so
  the panel ALWAYS docks — there is no overlay state (SP-25 deleted it).
- When the window shrinks or zooms, the open panel re-clamps TRANSIENTLY — the stored
  width is untouched and returns when the window does (MAJ-009).
- When the viewport is <680px, the panel takes over full screen, the chat is `inert`,
  and resizing is disabled (SP-25; was 640px).
- When Escape is pressed inside the Browser panel, it keeps its "stop driving" meaning
  and the panel stays open (SP-19); every other panel closes on Escape.
- When the browser tab is reloaded, the URL restores the panel; the remembered width
  persists per user + panel + workspace.

---

## 6. Edge cases

- **Panel open + workspace switch** (US-3.5, SP-29): the per-panel rule applies —
  Browser anchored; Library conditional; Tasks/Calendar/Mail follow. (A following
  Library passes the leave guard, FR-013.)
- **Browser deep link, ANY form** (US-7.5, SP-21 + SP-28): `panel=browser` is always
  dropped with a URL replace — bare or carrying session/agent context, fresh or
  reloaded — no session is created, just the chat renders; the Browser panel is never
  restored from reload or a shared link, and no session id is ever placed in a
  shareable chat-link.
- **Mail deep link with no mailbox** (US-7.6, SP-23): the Mail panel opens on its
  "choose a mailbox" state.
- **Escape while a modal is above the panel**: modal closes first (US-1.3).
- **Escape inside the Browser panel** (SP-19): never closes the panel — it releases
  the wheel (stops driving the remote browser); the panel closes only via its Close
  control.
- **Escape during Library unsaved-edits**: the confirmation dialog takes Escape
  (topmost layer); a cancel keeps the panel open.
- **Double-click reset while below 680px**: no border exists in takeover — no-op
  (SP-25).
- **Two entry points racing** (e.g. "Watch live" in chat while the Library toggle is
  clicked in the same interaction window): last open wins; one panel at a time holds;
  the leave guard still runs (a dirty Library cancels the race loser — the losing
  transition is the one whose `beforeLeave` resolves false; the winner re-runs its own
  gate once the dialog closes).
- **Pop-out close re-docks: ONLY the opener tab, only the same panel** (MAJ-006 as
  corrected by MAJ-208): only the tab that OPENED the pop-out (holds its window
  handle — the Browser's existing `watchPopoutClosed` pattern) reacts to a pop-out
  close; a manually-opened full-page tab's close triggers NO re-dock anywhere. In the
  opener tab: if the SAME panel is open, it re-docks/follows the pop-out's last
  workspace (the Dana UAT fix, preserved) — through the leave guard when that panel is
  a dirty Library; if a DIFFERENT panel is open, the re-dock is a NO-OP (never
  clobbers what the operator is using); if no panel is open, it re-docks normally.
- **Browser panel with an owned pop-out** (today's subscribe logic): clicking another
  panel's toggle must not strand the owned pop-out — the existing
  "close docked panel, focus owned window" reaction (`BrowserLivePanel.tsx`) is
  preserved for the Browser's own toggle; opening a DIFFERENT panel while an owned
  Browser pop-out exists closes the docked Browser panel state and leaves the pop-out
  owned and running (its re-dock reaction on pop-out close fires per the opener-only
  rule above).
- **Reload with `?panel=browser`** (SP-28): the param is dropped and just the chat
  renders — the Browser panel never restores from a reload; "Watch live" reopens it.
- **Phone-mode panel switch**: opening a second panel while one is open in phone mode
  replaces it WITHOUT adding another history step — exactly one pushed step exists for
  "a panel is open", so Back still closes straight to the chat (SP-26).
- **Boundary note (SP-25)**: below 680px is takeover; at ≥680px the floors always fit
  (row − sidebar ≥ 680 = 360 + 320) so the panel always docks — the old
  "floors stop fitting / overlay" case no longer exists anywhere.

---

## 7. Explicit non-behaviors & safeguards

### Qualitative prohibitions

- The system must not reintroduce the retired Sheet/overlay panel MODE — the retired
  surface is the slide-out Sheet with its pin toggle as the panel's hosting mode
  (retired 2026-07-16 by operator direction; `src/store/ui.ts::UiStore`,
  `LibraryPanel.tsx` module docs). SP-25 DELETED the SP-17 narrow-window overlay
  fallback entirely (MAJ-204's founder answer): there is NO overlay state anywhere —
  below 680px is full-screen takeover, at ≥680px the panel always docks. The only
  surviving prohibition is the retired hosting mode itself.
- The shell must not remove any capability inventoried in §2.2/§2.3; per-panel buttons
  may move to the shell header (capability preserved), never disappear.
- The shell must not know panel internals: it renders content supplied per panel and
  does not reach into the Library explorer or the browser view (each panel's pop-out
  handover semantics stay content-level).
- The system must not silently drop a failed Browser pop-out or a failed WebRTC stream
  (visible toast / visible error + Retry today; stays that way — ADR-061).
- The system must not open a second tab when one already exists for a panel's identity
  scope (SP-18 + SP-30: any entry point); worst case is a visible "already open"
  affordance, never a silent duplicate.
- The system must not close the docked panel when its Expand fails to open the new tab
  (MAJ-209): a blocked/failed `window.open` shows a visible error toast and the docked
  panel stays open — for every panel.
- The shell's own open/close path must not require any network call (§1: no backend) —
  the one exception is content-level: with no active session, "Open browser" creates a
  session before opening (MIN-206), and on that path the leave guard MUST run BEFORE
  the session creation, so a cancelled guard never creates a paid session.
- The system must not let the panel exceed the SP-17 ceiling
  `min(70% of row, row − sidebar − 360px)` — the chat stays usable (SP-3's purpose,
  SP-17's formula).
- The system must not persist the open/closed panel state server-side or reopen
  panels on reload except through the URL param (SP-14's mechanism is the URL; there is
  no panel session on the gateway).
- Settings must never become a panel (SP-6); the workspace-name entry keeps navigating
  to the settings page.
- The shell's own open/close decisions must not require any network call — all panel
  state is local (§1: no backend); the only network call on an open path is the
  content-level session creation named in the exception above (MIN-206).
- No path may silently discard unsaved Library edits (CRIT-001): every close/replace
  path is gated (FR-013).

### Machine-verifiable constraints

**Geometry (SP-17 as amended by SP-25, MAJ-203)**:
- Docked layout requires BOTH floors: chat column ≥360px AND panel ≥320px. All widths
  are measured on the root flex row; percentages use the row width as their single
  basis (MIN-001's basis — today's `sm:w-[45%]` is 45% of the row).
- Panel maximum width MUST equal `min(0.70 × row, row − sidebar − 360px)` (sidebar term
  = 256px when the sidebar is PINNED — a user preference honoured only at ≥1024px,
  `src/store/sidebar.ts::SIDEBAR_PIN_BREAKPOINT`; 0 when not pinned, which is always
  the case below 1024px).
- At every viewport ≥680px the floors always fit (row − sidebar ≥ 680 = 360 + 320):
  the panel ALWAYS docks side-by-side. **There is NO overlay state** — SP-25 deleted
  it; below 680px the panel takes over the full screen (US-8).
- Default width MUST equal `clamp(0.45 × row, 320px, min(720px, ceiling))` — the
  default is capped by the same ceiling (MAJ-203: an uncapped 45% gave a 1024px/pinned
  window a 461px default against a 408px ceiling, squeezing the chat to 307px, below
  its 360px floor). Computed px values are floored to whole pixels.
- Keyboard resize step MUST be 16px per keypress; Home = 320px; End = the SP-17
  ceiling.
- Viewport <680px MUST take over full width and MUST NOT render a resize handle.

**Performance (MIN-006 + MIN-205)**:
- Live drag MUST update the width via a CSS variable driven by requestAnimationFrame;
  layout-commit happens on drag release. No full React re-render per pointer move.
- Keyboard resize rides the same rAF path; the storage write and (for the Browser) the
  remote-viewport handover run when keyboard input SETTLES — 300ms after the last
  keypress — matching drag release, never once per keypress.

**State machine (two transition classes — MAJ-205)**:
- At most one panel open at any instant (SP-7) — every open path (tab toggle, sidebar,
  ChatControls, "Watch live", deep link, email draft link) MUST route through the same
  single-panel state.
- **App-initiated transitions** (tab toggle close, open-other from any entry point,
  header Close, Expand) are gated by the outgoing panel's `beforeLeave()`
  (CRIT-001 fix, FR-013): the shell awaits it BEFORE touching the store, URL or
  content; a `false` cancels (no state change, URL unchanged, panel stays).
- **URL-initiated transitions** (address-bar deep links, Back/Forward, workspace
  switch via a sidebar click, any router navigation) change the URL first, so they are
  gated by a ROUTER BLOCKER — the existing `useBlocker` pattern
  (`src/routes/_app/library.tsx::LibraryRoute`, already wired to
  `confirmDiscardLibraryEdits`): while the open panel has unsaved edits, ANY router
  navigation runs the outgoing panel's `beforeLeave`; on cancel the navigation does
  not happen — route, address and panel stay unchanged. Replaces the old
  "one model, no dual authority" claim: the true rule is store→URL projection for
  app-initiated changes, URL→store adoption for URL-initiated ones, each gated by its
  own mechanism.
- Pop-out re-dock is OPENER-ONLY (MAJ-208, FR-018): only the tab holding the pop-out's
  window handle reacts to its close; a same-panel re-dock follows the pop-out's last
  workspace (the Dana UAT fix) THROUGH the `beforeLeave` gate; a different-panel-open
  re-dock is a no-op; a manual full-page tab's close triggers no re-dock anywhere.
- Clicking the open panel's toggle MUST close it (SP-11) — through the gate.
- Expand MUST close the docked panel in the source tab (SP-12) — through the gate; a
  failed/blocked new tab leaves the docked panel open with a visible toast (MAJ-209).

**URL (SP-14, SP-22, SP-26, SP-28, MAJ-205, MAJ-206)**:
- The open panel MUST be encoded as search param `panel` on the workspace Chat route,
  whose value MUST be a REGISTERED panel id (MAJ-012); any other value — unknown,
  unregistered-but-future, or `browser` (SP-28: Browser is excluded from URL restore
  entirely) — MUST be dropped with a URL replace.
- Under the app's hash routing the param lives under `#/` (e.g.
  `/#/workspaces/{id}/chat?panel=team`).
- Desktop: panel open/switch/close are history REPLACE, never push (SP-22); on
  Back/Forward the store wins — a restored entry's stale `panel` value is ignored and
  immediately re-projected from the store (replace), so Back behaves exactly as today
  (MAJ-206).
- Phone (<680px): opening a panel pushes ONE history step carrying a phone-push
  marker; Back over that step closes the panel (SP-26); desktop entries never carry
  the marker.
- Workspace navigation is NOT a panel change: a workspace switch under an open panel
  follows SP-29's per-panel rules (FR-020), never a URL panel-param edit.

**Persistence (SP-20, MAJ-009, MIN-204, MIN-205)**:
- Remembered width key MUST be **`panel-width:<username>:<panelId>:<workspaceId|app>`**
  — ONE localStorage key per combination (MIN-204; no single JSON blob), in
  browser-local storage (SP-20). `username` is the signed-in account's display name —
  the only account identifier the SPA holds (`src/store/auth.ts`); the Browser panel
  always uses the `app` workspace term (MAJ-201).
- Stored value is px, written ONLY on a settled user choice: drag release, keyboard
  settle (300ms after the last keypress), or reset. Reset DELETES the key (default
  re-derived at read time; a default is never stored in px). A window-driven re-clamp
  is transient and MUST NOT write.
- Pruning: on each write, the signed-in user's OWN entries for workspaces no longer in
  their workspace list are removed; other accounts' entries are never touched
  (MIN-204 — the SPA cannot see another account's entries' meaning, only its own
  list).
- Storage failure (quota exceeded, private mode): the width works in memory for the
  session, nothing is persisted, NO error is surfaced — an explicit, accepted,
  documented degrade for a preference, not a silent failure (MIN-204).
- Storage location: browser-local localStorage (SP-20, decided). A future server-side
  home would be a contract-first change — not in this spec.

**Accessibility (MIN-002, MIN-003, MIN-208)**:
- The separator MUST be focusable, role `separator`, `aria-orientation="vertical"`,
  with an accessible name ("Resize <Panel> panel"), `aria-controls` referencing the
  panel element, `aria-valuetext` ("<n> pixels wide"), and
  `aria-valuenow`/`aria-valuemin`/`aria-valuemax` in px (`aria-valuemax` updates on
  window resize — MIN-003).
- Escape MUST close the topmost layer (modal above panel, else panel) — EXCEPT the
  Browser panel, where Escape NEVER closes the panel (SP-19: Escape releases the
  wheel, per `BrowserLiveView.tsx::handleKeyDown`, WCAG 2.1.2; only Close closes it).
  The shell's Escape listener is bubble-phase on the shell root, fires only when focus
  is inside the shell, and only when `event.defaultPrevented` is false AND
  `event.isComposing` is false (MIN-208: IME composition must be detected with
  `isComposing`, not assumed to call `preventDefault()`; Radix layers DO call
  `preventDefault` when they dismiss). "Focus inside the shell" means the active
  element's composed DOM path contains the shell root or an element the shell has
  registered as one of its portalled layers (MIN-208 — DOM tree, not React tree, so
  portalled content counts).
- On open (by user action), focus MUST move into the panel; on close — including an
  Escape close — focus MUST follow the per-case rules in US-9 (invoking toggle, else
  chat input; never steals focus on a load-time deep-link restore).

**Broadcast channels and the same-origin preview surface (MIN-207, STRIDE note)**:
- Presence/context messages are validated on receipt: malformed or wrong-version
  messages are ignored, never applied. File paths and Library selections are NEVER
  posted over BroadcastChannel (any same-origin page can read them — agent-built
  dev apps are served same-origin at `/preview/` per ADR-044 "Serve /preview/ on the
  main gateway listener"); per-target context (e.g. a new Library selection) travels
  to a specific registry handle via targeted `postMessage`, not the broadcast
  channel. Recorded assumption (§17): same-origin preview apps can join the SPA's
  channels; validation + the no-paths rule bound the exposure to channel noise (a
  spoofed "already open" answer or a fake close signal), never to file-path
  disclosure.

---

## 8. Contracts introduced by this spec (SPA-local)

### 8.1 Shell component contract (shape — backend-lead-equivalent edits happen only in `src/`)

One panel-definition type feeds the shell; one store slice is the single source of
truth. Shape-level contract (not implementation):

```
PanelDefinition {
  id:            'library' | 'browser' | 'mail' | 'tasks' | 'team' | 'calendar'
  title:         string                          // shell header title
  content:       React component (receives close/expand callbacks via props)
  expandTarget:  (context) => route location    // full-page route + params
  beforeLeave?:  () => Promise<boolean>         // CRIT-001: false cancels the transition
}

Shell state (store, single slice replacing browserPanel/libraryPanel):
  activePanel: { id, context } | null           // at most one (SP-7)
  openPanel(id, context) / closePanel() / setPanelWidth(px)
```

- `context` carries what the panel needs (Library: `workspaceId?`; Browser:
  `sessionId, agentId`; workspace panels: `workspaceId`; Mail: mailbox context per the
  email spec).
- **Each panel's identity key (MAJ-201)** — what "already open", presence, the handle
  registry and the entry-point switch all match on (§8.3), and what scopes the width
  bucket:
  | Panel | Identity key | Width bucket |
  |---|---|---|
  | Library / Tasks / Team / Calendar | `panelId × workspaceId` (`app` when opened at the virtual root) | that workspace, or `app` at the root |
  | Browser | `panelId × sessionId × agentId` (MAJ-201: a session, NOT a workspace — today's `browserPanel` is `{sessionId, agentId}`, `src/store/ui.ts`) | **`app` always** (one width per user for the Browser panel) |
  | Mail | `panelId × workspaceId` + mailbox context per the email spec | that workspace |
  A Library full-page tab that navigates to another workspace no longer matches its
  old workspace's identity (matching is by CURRENT scope, MAJ-213).
- `beforeLeave` is supplied ONLY by panels with an unsaved-edit risk (Library today:
  its existing `confirmDiscardLibraryEdits`); panels without such risk omit it, and
  transitions replace them freely. The shell awaits it BEFORE touching the store, URL
  or content (the guard must run while the outgoing panel is still mounted — its dialog
  is hosted inside the mounted content, `unsavedGuard.ts`).
- **The shell derives the width-memory key itself** —
  `panel-width:<username>:<panelId>:<workspaceId|app>` — from `id × identity scope ×
  signed-in user` (SP-20, MIN-204; Browser = `app` per MAJ-201); panels do not supply
  it (OBS-001: the per-panel `widthMemoryKey` was unnecessary indirection, removed).
- Panels register through a single registry; adding Mail (wave 2) and
  Tasks/Team/Calendar (wave 3) MUST require no shell change beyond a new
  `PanelDefinition` entry (SP-4's test: "each panel supplies only its content and its
  expand target").
- The shell header renders title + Expand + Close; Expand delegates to the panel's
  own pop-out behaviour (selection-carrying for Library, ownership handover for
  Browser — §2.3 kept differences).

### 8.2 URL and deep-link contract (SP-14, SP-21..SP-23, SP-27, SP-28, MAJ-205, MAJ-206)

- Param `panel` on the workspace Chat route's search (under `#/`). Canonical example:
  `/#/workspaces/{workspaceId}/chat?panel=team`. **The workspace Chat route is the only
  route whose search schema declares `panel` (plus `agent`, meaningful only with
  `panel=mail`, SP-23)** (today it declares none — verified);
  the Chat route's own `{workspaceId}` param scopes every panel's workspace context,
  which is why no extra workspace param is needed. A `panel` param arriving on any
  other route is dropped (URL replace).
- **Two transition directions (MAJ-004 as corrected by MAJ-205)**: the store slice
  (`activePanel`) is the runtime source of truth and the URL `panel` param is its
  addressable projection — but the old "one model, no dual authority" claim was wrong
  for navigations that start at the URL. The actual rule: **store→URL projection**
  (history REPLACE) for app-initiated changes (toggle, open-other, Close, Expand,
  workspace-switch re-target), and **URL→store adoption** for URL-initiated ones
  (deep link, Back/Forward, workspace switch via a sidebar click) — the latter gated
  by the router blocker (below). Desktop toggles are replace, never push (SP-22); on
  Back/Forward the store wins (MAJ-206): a restored entry's stale `panel` value is
  ignored and re-projected from the store, so Back behaves exactly as today. Phone
  mode is the SP-26 exception (one pushed step; Back closes the panel).
- **URL-initiated transitions are gated by a router blocker (MAJ-205)** — the
  existing `useBlocker` pattern already in this codebase
  (`src/routes/_app/library.tsx::LibraryRoute`, wired to
  `confirmDiscardLibraryEdits`): while the open panel has unsaved edits, ANY router
  navigation (deep link pasted in the address bar, Back/Forward, a sidebar click to
  another workspace or route) runs the outgoing panel's `beforeLeave`; **on cancel the
  navigation does not happen — route, address and panel stay unchanged**. For a
  workspace-switch cancel this means the route stays on the outgoing workspace's chat,
  with the Library left exactly as it was (US-3.5, FR-020).
- **Cross-route navigation (MAJ-004)**: panels are app-global (§17) — navigating to a
  non-chat route (Agents, Settings) keeps the open panel open in the store but the URL
  stops carrying `panel` (only the Chat route declares it); navigating back to a
  workspace Chat route re-projects the state into the URL (replace). A reload on a
  non-chat route restores no panel (the URL is the restore mechanism and it does not
  carry `panel` there).
- Valid `panel` values are the REGISTERED panel ids (MAJ-012); `tasks` in wave 1 is
  treated exactly like `bogus` — dropped with a URL replace, no panel.
- **Sign-in return (SP-27, wave 1)**: the auth gate preserves the original hash URL
  (path + search) across the login redirect (today `_app.tsx` redirects to `/login`
  with no return address, and `login.tsx` navigates to `/` after sign-in — verified);
  after signing in, the user lands back on the link they opened — workspace and panel
  both restored, never dropped to `/`. This closes SC-004 (shared links work for
  signed-out colleagues too).
- Panel context that cannot live in the URL:
  - **Browser — EXCLUDED from URL restore (SP-21 + SP-28, decided)**: a `panel=browser`
    param is ALWAYS dropped with a URL replace — bare or carrying context, fresh or
    reloaded. The Browser panel is NEVER restored from a reload or a shared link; it
    reopens only via its entry points ("Watch live", "Open browser"). **No session id
    is ever placed in a shareable chat-link** — the chat route's `panel` projection
    never carries `session`/`agent` for the Browser; "session state" as a restore
    source does not exist. The `/browser-live` route's existing visible
    "Missing session or agent" refusal (`BrowserLiveRoute`) is unchanged — that
    full-page route keeps its existing params (today's expand target, not a panel
    deep link).
    **Shared-link authorization (MIN-009 disposition)**: the one URL that still
    carries a session id is the full-page `/browser-live?session=…&agent=…` expand
    target (today's route). It relies on the gateway's existing per-user
    authorization of the live stream (ADR-044 "Serve /preview/ on the main gateway
    listener", session-cookie decision) — stated here as an ASSUMPTION, not a
    verified property. Required wave-1 regression test (test 17): a second account
    following the owner's `/browser-live` link sees the panel's visible denial
    surface, never the other user's stream.
  - **Mail (SP-23, decided)**: `panel=mail` with no agent/mailbox selected opens the
    Mail panel on its "choose a mailbox" state (Mail starts nothing costly); a link
    MAY carry `&agent=…` to land directly on that mailbox — declared in the chat
    route's search schema, meaningful only with `panel=mail`.
  - **Library (MAJ-010 honesty)**: `panel=library` restores the Library scoped to the
    Chat route's `{workspaceId}` at its DEFAULT view (the workspace's root). It does
    NOT restore a path/folder selection — selection-carrying links remain the existing
    full-page `/library?workspace=…&path=…&folder=…` form, whose `folder` is a one-time
    initial seed (`src/routes/_app/library.tsx::librarySearchSchema`). No new context
    params are introduced.
    **Virtual-root reload difference (MIN-211, accepted)**: a Library opened from the
    sidebar sits at the all-workspaces root (`openLibraryPanel()` with no workspace,
    `Sidebar.tsx`), but the URL cannot express root scope (`?panel=library` on the
    chat route) — on reload it restores scoped to the route's workspace, in that
    workspace's width bucket. Accepted, stated difference; the width bucket moves
    accordingly on that restore (root `app` → workspace bucket).
- The `media` redirect stub
  (`src/routes/_app/workspaces.$workspaceId.media.tsx::WorkspaceMediaRedirect`)
  retargets to the deep-link form: navigate to
  `/workspaces/{id}/chat?panel=library` (replace) instead of store-call + bare redirect
  — bookmarked `media` links keep working.
- Full-page routes (`/library`, `/browser-live`, `/workspaces/{id}/{board,team,calendar}`)
  keep working exactly as today: a shared full-page link lands on the full page (US-5.4).

### 8.3 Already-open-tab contract (SP-18, SP-30, MAJ-201, MAJ-202, MAJ-209, MAJ-213, MIN-207)

Verified web-platform facts this contract rests on (MDN Window/open, Window/focus):

1. `window.open(url, name)` on an existing named window **navigates** that window to
   `url` — it does not merely focus it. A second Expand under the old "stable window
   name reuse" design would have navigated the existing Browser tab to `about:blank`
   and torn down a live viewer (MAJ-002). **MAJ-202 drops the stable window name
   entirely** — no `omnipus-panel-*` name is used for any open; every new tab is a
   plain `_blank` open. Identity lives in the handle registry and the presence list
   (below), not in window names.
2. Opening with a custom name would require dropping `noopener` for nothing — with
   `_blank` the open works with `noopener` set, but then `open()` returns null and no
   handle can be held. So new opens use `_blank` WITHOUT `noopener` (as the Browser
   already does: `BrowserLivePanel.tsx::handlePopOut`, which then severs
   `popup.opener = null`), purely to hold the handle; `window.opener` is severed
   immediately after the open. The Library's current `'noopener,noreferrer'`
   (`LibraryPanel.tsx::handlePopOut`) is replaced by this pattern.
3. `window.focus()` "may fail due to user settings and the window isn't guaranteed to
   be frontmost" (MDN Window/focus) — a background tab focusing itself is best-effort.

**Identity keys (MAJ-201, §8.1's table)**: every already-open check matches by the
panel's identity key — `panelId × workspaceId` for Library/Tasks/Team/Calendar
(`app` at the root), `panelId × sessionId × agentId` for Browser. **MAJ-213: matching
runs against the panel's CURRENT scope** — a full-page tab whose panel navigated from
workspace A to B is "already open" for B only; it no longer matches A (clicking A's
toggle opens docked at A; it does not yank the other tab's context back).

**The handle registry (MAJ-002/MAJ-202)**:

- On each Expand (new tab), the app stores the returned `Window` handle in a
  module-level registry keyed by the identity key — the Browser's existing
  `ownedPopout` is the precedent (`BrowserLivePanel.tsx`).
- On a subsequent click for the SAME identity key (SP-30: EVERY entry point —
  toggle, Expand, sidebar Library, "Open library", "Watch live"), the app checks the
  registry: **if the handle exists and `!handle.closed`, it calls `handle.focus()` and
  posts the current context (e.g. a new Library selection) over the existing handoff
  BroadcastChannel — it NEVER re-calls `window.open` for that tab** (no re-navigation,
  no blanking). A stale handle (`handle.closed`) is dropped.
- The docked panel does NOT open here in the reuse path (SP-18: only switch).
- A handle is lost when the source tab reloads; in that case the flow falls back to
  presence detection (below), exactly as for a manually-opened tab.

**Presence list (OBS-202 — replaces the old 150ms ping-and-wait)**: every Omnipus tab
continuously announces itself on the handoff BroadcastChannel (extending the
`libraryHandoff`/`browserLiveHandoff` precedents): on load, on scope change, and on
`pagehide` (exit). Each announcement carries the identity keys of the full-page panels
that tab currently shows — a small `{panelId, workspaceId|app}` set; **MIN-207: never
file paths, selections or session contents ride the channel**, and incoming messages
are validated before use. Because the list is continuously present, an entry-point
click resolves synchronously — no wait window:

- **Present (same identity key)** → SP-18/SP-30 apply: no duplicate, no docked panel
  opens here; a visible "already open in another tab — switch" affordance offers the
  best-effort switch (the affordance is the guarantee — programmatic focus is not).
  Multiple entries (two manual tabs) → the affordance targets the most recent joiner;
  nothing duplicates.
- **Absent** → the panel opens docked here normally (and stores its handle if Expand
  was the trigger).

**Open ordering (MAJ-209)**: the presence check and any `window.open` MUST run
synchronously inside the user-gesture handler — the only way a popup is not
block-headed. So: (1) resolve against registry + presence list first; (2) only if
nothing is already open, call `window.open(url, '_blank', features)` synchronously in
the click handler, then sever `opener`, store the handle, close the docked panel
(SP-12) and project the URL (REPLACE) — in that order. Deferred async work
(presence refreshes, context posts) happens after, never between the click and the
open. If the popup IS blocked (rare, but possible under strict browser settings), the
docked panel stays open and an error toast tells the user — no silent no-op, no
docked-panel loss (MAJ-209's fail-visible rule; US-6 AS-5).

Presence state MUST clear when the tab closes (channel disconnect / pagehide) so a
later entry point reopens normally (US-6.4).

---

## 9. Clickable DEMO requirements (SP-10, SP-16, SP-31) — split gates BEFORE build

Per SP-10 and the founder process line: a clickable demo of the shell is reviewed by
the founder in a real browser BEFORE the build, and it must first pass team-lead's own
real-browser click test. House rule (founder memory, 2026-09-26): demos are interactive
and browser-verified — a static snapshot, never a live dev server handed over.

**SP-16 (founder, post-grill): the SP-15 Storybook stories ARE the SP-10 demo.** One
static Storybook build — all six panels + the shared shell on sample data — is
click-tested in a real browser (team-lead, evidence recorded) and then handed to the
founder. There is NO separate standalone demo build; two sample-data sets would drift
(grill MAJ-008). Wave 0 builds: the shell stories, the real Library and Browser stories,
and sample-content STAND-IN stories for Mail/Team/Tasks/Calendar (needed so the demo
shows one-at-a-time across all six); the stand-ins live in the demo build only and are
replaced by each panel's real story set when its wave lands (SP-24, §10). Sample data
comes from story fixtures — no live gateway (SP-15).

**SP-31 (founder, grill round 2): the gate SPLITS in two.**

- **Wave-0 gate — Storybook stories** cover every LAYOUT-ONLY row: resize, floors and
  ceiling, double-click reset, per-panel/workspace width memory, one-at-a-time
  switching, phone takeover, the three SP-26 close affordances, keyboard walkthrough.
  A small **dev-only Storybook request-mocking add-on** (e.g. msw-style story-level
  request interception) is ALLOWED for panel content that would otherwise need the
  gateway — a dev dependency only, never shipped in the app bundle.
- **Wave-1 exit criterion — the REAL running app** covers every row that needs real
  browser chrome: address-bar deep links, new-tab opens and already-open switching,
  live Browser driving, sign-in return, Back/Forward behaviour. These rows are
  click-tested against a running dev build during wave 1, before wave 1's own gate —
  they are NOT waived because the Storybook demo passed.

**Stories inventory (the demo scope — SP-15/SP-16)**:

| Story set | Wave | Sample content |
|---|---|---|
| Shell (empty, docking, phone, guard) | 0–1 | Generic sample panel + Library/Browser content |
| `library` | 0–1 | Real explorer on fixture data (no gateway) |
| `browser` | 0–1 | Static placeholder view (no live WebRTC required) |
| `mail` | 2 (stand-in 0) | Real content wave 2; wave-0 stand-in: static message list + reader |
| `tasks` | 3 (stand-in 0) | Real content wave 3; stand-in: narrow list/kanban |
| `team` | 3 (stand-in 0) | Real content wave 3; stand-in: narrow agent-graph |
| `calendar` | 3 (stand-in 0) | Real content wave 3; stand-in: day/week |

**Viewport coverage (MIN-201)**: every shell story renders at BOTH `680×900` (takeover
boundary — one pixel decides the mode) and `1280×800` (docked), via Storybook viewport
dropdown variants; layout rows are checked at both, not just the default canvas width.

**What the wave-0 demo must demonstrate** (each maps to a US):

1. Resize by dragging, with the 320px floor and the SP-17 ceiling visibly enforced;
   chat column absorbing the remainder; at no width does the panel overlay the chat —
   below 680px total the takeover takes over instead (US-2, SP-25).
2. Double-click reset; width memory per user + panel + workspace across close/reopen
   (US-3).
3. One-at-a-time switching through the tab-strip toggles, pressed state + second-click
   close (US-4, US-5).
4. Phone width: takeover + inert chat, no resize handle; the THREE SP-26 close
   affordances — ✕, Back (one pushed history step), swipe-to-close with its
   direction/threshold rules (US-8, SP-26).
5. Keyboard walkthrough: Tab to separator, arrows, Home/End, Escape (except Browser,
   SP-19), focus return (US-9).

**Wave-0 real-browser click-test list** (team-lead executes against the static
Storybook build and records evidence before the founder review; every step is a
pass/fail with a screenshot):

| # | Action | Expected | Viewport |
|---|---|---|---|
| 1 | Click Library tab entry | Panel docks beside chat; entry pressed | 1280×800 |
| 2 | Drag border to far left | Stops at 320px; chat still visible and interactive | 1280×800 |
| 3 | Drag border toward far right | Stops at the SP-17 ceiling `min(70% row, row − sidebar − 360px)`; chat never below 360px | 1280×800 |
| 4 | Double-click border | Width returns to default; persists after close/reopen | 1280×800 |
| 5 | Set width in workspace A; switch to workspace B; open same panel | B's width (or default) applies, not A's | 1280×800 |
| 6 | Click Tasks entry while Library open (unsaved Library edits → CANCEL the guard) | Navigation/switch does not happen; Library stays exactly as it was | 1280×800 |
| 7 | Repeat row 6, choose CONTINUE | Library closes (through the leave guard); Tasks docks; pressed state moves | 1280×800 |
| 8 | Click Tasks entry again | Panel closes; pressed state clears | 1280×800 |
| 9 | Open Browser panel, then click Calendar entry | Browser closes (one-at-a-time); no stranded state | 1280×800 |
| 10 | Set viewport 679px; open Mail | Full-screen takeover; chat inert; no resize handle | 679×900 |
| 11 | Set viewport 680px with the panel open | Docked layout renders — floors 360+320 fit exactly | 680×900 |
| 12 | Phone ✕ | Panel closes; chat visible again | 679×900 |
| 13 | Phone Back (memory-router story) | Panel closes; chat shows; history returns to the pre-open entry | 679×900 |
| 14 | Phone swipe right from the left edge, ≥96px | Panel closes; chat visible again | 679×900 |
| 15 | Phone swipe INSIDE a horizontal scroller (carousel/week view) | Panel does NOT close; content scrolls | 679×900 |
| 16 | Keyboard: Tab to border, arrows/Home/End/Escape | Steps work; focus returns per US-9's per-case rules; Escape on the BROWSER panel releases driving and does NOT close it (SP-19) | 1280×800 |

**Wave-1 real-app click-test list** (executed against a RUNNING dev build during
wave 1, before wave 1's gate — SP-31's second half; rows NOT covered by the Storybook
demo):

| # | Action | Expected |
|---|---|---|
| W1 | Copy URL with `?panel=calendar`; paste into fresh tab | Calendar panel restores |
| W2 | URL with `?panel=bogus` (and, in wave 1, `?panel=tasks` unregistered) | Param dropped; chat renders; no panel |
| W3 | URL with `?panel=browser` — bare, or with `session`/`agent` | Param ALWAYS dropped (SP-28); chat renders; no panel; no session id ever in the link |
| W4 | Click Expand on Library | New tab opens full-page Library; original tab's panel closed; chat full width |
| W5 | Click Library toggle again with the app-opened tab still open | Existing tab is focused (handle registry); no duplicate tab; no docked panel opens here |
| W6 | Click the sidebar LIBRARY button while a Library full-page tab is open (SP-30) | The open TAB is focused — not a docked panel, not a second tab |
| W7 | Open full-page Library URL manually in a second tab; click Library toggle in tab 1 | No duplicate; no docked panel opens here; "already open — switch" affordance shown |
| W8 | Close the manual tab; click the toggle | Panel opens normally |
| W9 | Open a Browser live session; reload the page | Browser panel NOT restored (SP-28); chat renders; reopen via "Watch live" |
| W10 | Browser panel open, navigate Back then Forward (MAJ-206) | Panel state follows the store (store wins over the stale URL value), no duplicate history steps |
| W11 | Sign out (or use a signed-out tab), open `?panel=calendar` link, sign in | Land back on the opened link — workspace + Calendar restored (SP-27), not `/` |
| W12 | Blocked-popup browser setting; click "Watch live" | Error toast; docked panel stays open; no silent failure (MAJ-209) |

A demo failing any step is fixed and re-tested before the gate it feeds (SP-10's
"team-lead's own browser check first"; OBS-201: rows W4/W5/W7 need real multi-tab
behaviour — they are scripted as Playwright multi-tab tests where feasible and
click-tested otherwise). Drag smoothness (MIN-006) is observed during wave-0 rows
2–3: live width updates ride a CSS variable via requestAnimationFrame — no visible
jank on a long chat transcript.

---

## 10. Rollout sequence (SP-8) with narrow layouts (SP-6)

**Wave 0 — this spec + the Storybook demo (SP-10, SP-16, SP-31).** Spec review rounds;
the static Storybook demo build (shell + Library/Browser stories + sample stand-ins for
the later-wave panels) click-tested against the **wave-0 gate rows** (§9's wave-0
list — layout only: resize, limits, reset, one-at-a-time, phone takeover + the three
SP-26 affordances, keyboard) and founder-approved in a real browser. No production
build starts before that approval. The address-bar / new-tab / deep-link /
live-Browser rows are NOT part of this gate — they moved to wave 1 (SP-31).

**Wave 1 — Shell + Library + Browser (P0) [wave 1 FRs].** The shared shell lands
hosting Library and Browser: resize + width memory (SP-2/SP-3/SP-13/SP-17/SP-20),
one-at-a-time WITH the every-path leave guard (SP-7, FR-013/CRIT-001), Escape with the
Browser exception (SP-19), header actions (§2.2/§2.3 buttons move to the shell header,
capabilities preserved), tab-strip Library toggle with the new ARIA model (SP-11,
MAJ-007), deep links with replace history (SP-14, SP-22), already-open-tab behaviour
via the handle registry + presence list (SP-18, SP-30, §8.3), phone takeover at 680px
with the three close affordances (SP-25, SP-26), sign-in return (SP-27), Browser
excluded from URL restore (SP-28), per-panel workspace-switch rules (SP-29), the
router leave-blocker (MAJ-205). **Wave 1's own gate additionally requires the §9
wave-1 real-app rows to pass** — that is SP-31's exit criterion for the
address-bar/new-tab/deep-link/live-Browser behaviours. Entry points (sidebar,
ChatControls, "Watch live") keep working through the new store slice. Stories: shell +
Library + Browser sets (SP-24).

**Wave 2 — Mail adopts the shell (on `feature/email-mail`).** The email spec's D11
(docked Mail panel, Library-style, plus fullscreen pop-out) is satisfied by registering
a Mail `PanelDefinition` — no shell change. Mail's expand target follows the email
spec; `?panel=mail` without a mailbox opens the "choose a mailbox" state (SP-23). The
chat draft-link opens the Mail panel through the same single-panel state. Stories:
Mail set replaces its wave-0 stand-in (SP-24).

**Wave 3 — Team, Tasks, Calendar become panels (SP-6).** Their tab entries become
toggles (ending mixed mode — until then they are navigation links, US-5); their
existing routes remain the full-page expand targets and deep links. Stories: the three
sets replace their stand-ins (SP-24). Narrow layouts are per-panel content work,
explicitly in scope per SP-6:

| Panel | Narrow-panel layout (SP-6) |
|---|---|
| Tasks | List view fits; the board needs a narrow layout (single-column swim or stacked cards) |
| Team | Graph gets zoom/scroll inside the panel |
| Calendar | Day/week views fit the panel; month view routes to the full page (expand) |

Settings stays a page in every wave (SP-6).

**Sequencing rule**: each wave lands on `feat/resizable-side-panels` / the feature
branches in order; a wave starts only after the previous wave's gate (review gate; for
wave 0 additionally the founder demo approval). Former founder questions Q1/Q2 are
ANSWERED (SP-20, SP-21) — nothing blocks wave 1 pending §14.

---

## 11. BDD scenarios

### Feature: Workspace side-panel shell

#### Scenario: Panel docks beside chat with shell header
**Traces to**: US-1, AS-1 · **Category**: Happy Path
- **Given** the shell hosting the Library panel
- **When** the Library panel is open on a ≥680px viewport
- **Then** the shell header shows the title "Library", an Expand action and a Close action
- **And** the Library content renders with breadcrumb, Show-hidden, mounts pill and create menu intact
- **And** the chat column shares the remaining width (both visible, no overlay)

#### Scenario: Shell Close honours the Library unsaved-edits guard
**Traces to**: US-1, AS-2 · **Category**: Error Path
- **Given** an unsaved Library edit
- **When** the operator clicks the shell header's Close
- **Then** the discard-confirmation appears
- **And** cancelling leaves the panel open with the edit intact

#### Scenario: Escape closes the topmost layer only (except Browser — SP-19)
**Traces to**: US-1, AS-3 · **Category**: Alternate Path
- **Given** a panel open with a modal above it
- **When** Escape is pressed
- **Then** the modal closes and the panel stays open
- **When** Escape is pressed again
- **Then** the panel closes

#### Scenario: Browser panel Escape never closes the panel
**Traces to**: US-1, AS-3 · **Category**: Error Path
- **Given** the Browser panel open with the operator driving the remote browser
- **When** Escape is pressed
- **Then** driving stops (the wheel is released, `BrowserLiveView.tsx::handleKeyDown`)
- **And** the Browser panel remains open
- **And** the panel closes only when its Close control is used

#### Scenario: Drag resize clamps at both bounds
**Traces to**: US-2, AS-2 · **Category**: Edge Case
- **Given** an open panel on a 1400px-wide window with the sidebar PINNED (256px)
- **When** the border is dragged toward the right
- **Then** the width stops at the SP-17 ceiling `min(70% row, row − sidebar − 360px)` = min(980, 784) = **784px** — not 70% alone
- **When** the border is dragged below 320px
- **Then** the width stops at 320px

#### Scenario: Double-click resets to default
**Traces to**: US-2, AS-3 · **Category**: Happy Path
- **Given** a panel widened to 900px on a 1400px UNPINNED row
- **When** the border is double-clicked
- **Then** width becomes `clamp(0.45 × row, 320px, min(720px, ceiling))` — 630px here (0.45 × 1400)

#### Scenario: Window shrink re-clamps the open panel
**Traces to**: US-2, AS-4 · **Category**: Edge Case
- **Given** a panel at 900px on a 1400px unpinned window
- **When** the window shrinks to 1000px
- **Then** the panel re-clamps to 640px (the SP-17 ceiling `min(700, 1000 − 360)`)

#### Scenario Outline: Width memory per panel × workspace
**Traces to**: US-3, AS-1/2 · **Category**: Happy Path
- **Given** a remembered width for <panel> in <workspace>
- **When** <panel> opens in <workspace>
- **Then** the remembered width applies clamped to bounds

**Examples**:

| panel | workspace | remembered | expected |
|---|---|---|---|
| library | A | 640px | 640px |
| tasks | A | 950px | 950px (within the SP-17 ceiling at a 1400px unpinned row) |
| library | B | none recorded | default (45% clamped) |

#### Scenario: Workspace switch re-targets and re-widths the open panel
**Traces to**: US-3, AS-5 · **Category**: Alternate Path
- **Given** the Library panel open scoped to workspace A at 800px
- **When** the operator switches to workspace B (remembered 500px)
- **Then** the panel stays open, shows B's library, at 500px

#### Scenario: Opening a second panel replaces the first
**Traces to**: US-4, AS-1 · **Category**: Happy Path
- **Given** Library open
- **When** the operator triggers "Watch live" in chat
- **Then** the Browser panel docks and Library is closed

#### Scenario: Second click on the toggle closes the panel
**Traces to**: US-4, AS-2 + US-5, AS-2 · **Category**: Alternate Path
- **Given** the Tasks panel open via its tab entry
- **When** the entry is clicked again
- **Then** the panel closes and the entry de-highlights

#### Scenario: Tab entry toggles without route navigation
**Traces to**: US-5, AS-1 · **Category**: Happy Path
- **Given** the workspace Chat route with no panel
- **When** the Calendar tab entry is clicked
- **Then** the route stays on Chat, the Calendar panel docks, the entry highlights, and the URL gains `?panel=calendar`

#### Scenario: Expand opens a new tab and closes the source panel
**Traces to**: US-6, AS-1 · **Category**: Happy Path
- **Given** any panel open
- **When** Expand is clicked
- **Then** the panel's full-page route opens in a new browser tab
- **And** the docked panel closes in the original tab with the chat at full width

#### Scenario: App-opened tab is focused via the handle, never re-navigated
**Traces to**: US-6, AS-2 · **Category**: Alternate Path
- **Given** the app previously opened the Library full-page tab and holds its window handle
- **When** Expand (or the toggle's already-open behaviour) runs again
- **Then** the same tab is focused via the stored handle — exactly one Library full-page tab exists
- **And** the tab is NOT navigated, reloaded or blanked (no second `window.open` — MAJ-002)
- **And** no docked panel opens in the source tab (SP-18: only switch)

#### Scenario: Manually-opened tab is detected, never duplicated
**Traces to**: US-6, AS-3 · **Category**: Error Path
- **Given** the operator opened `/#/library` manually in another tab (no app window handle)
- **When** the Library toggle is clicked in the app tab
- **Then** no duplicate tab opens
- **And** a visible "already open in another tab" affordance appears

#### Scenario: Presence clears when the manual tab closes
**Traces to**: US-6, AS-4 · **Category**: Edge Case
- **Given** the "already open" state from a manual tab
- **When** that tab is closed and the toggle is clicked again
- **Then** the Library panel opens normally

#### Scenario: Deep link restores the panel
**Traces to**: US-7, AS-2 · **Category**: Happy Path
- **Given** a link to `/#/workspaces/{id}/chat?panel=calendar`
- **When** it is opened in a fresh tab
- **Then** the Calendar panel is open over Chat without further clicks

#### Scenario: Unknown panel id is dropped visibly
**Traces to**: US-7, AS-4 · **Category**: Error Path
- **Given** a link with `?panel=bogus`
- **When** it is opened
- **Then** no panel opens and the URL is replaced without the `panel` param

#### Scenario: Phone takeover with inert chat
**Traces to**: US-8, AS-1 · **Category**: Happy Path
- **Given** a 480px-wide viewport
- **When** the Mail panel opens
- **Then** it fills the content width, the chat region is inert, and no resize handle renders

#### Scenario: Viewport grows out of takeover
**Traces to**: US-8, AS-6 · **Category**: Edge Case
- **Given** a panel open in phone takeover
- **When** the viewport reaches 680px
- **Then** the panel docks at its remembered width with a resize handle
- **And** the phone-mode history step collapses (Back no longer closes the panel)

#### Scenario: Keyboard resize full walkthrough
**Traces to**: US-9, AS-1/2/3 · **Category**: Happy Path
- **Given** an open panel and a keyboard-only operator
- **When** the separator is focused via Tab
- **Then** it exposes role separator with vertical orientation and min/current/max values
- **When** Right-arrow is pressed four times
- **Then** width increases by 64px total (16px steps, clamped)
- **When** Home then End are pressed
- **Then** width is 320px then the SP-17 ceiling — 980px on a 1400px unpinned row
- **When** Escape closes the panel
- **Then** focus returns to the toggle that opened it

#### Scenario: Browser panel resize shows the viewport handover
**Traces to**: US-2, AS-5 · **Category**: Alternate Path
- **Given** the Browser panel connected to a live session
- **When** the operator drags its border
- **Then** the existing "Resizing browser… input will resume" status appears during the handover
- **And** a failed handover is visible with Retry (never a silent stall)

#### Scenario: Drag resize follows the pointer live
**Traces to**: US-2, AS-1 · **Category**: Happy Path
- **Given** an open panel beside the chat
- **When** the border is dragged left/right
- **Then** the panel width follows the pointer and the chat column width changes by the same amount

#### Scenario: Narrow window crosses into phone takeover at 680px (SP-25)
**Traces to**: US-2, AS-4 + US-8, AS-1 · **Category**: Edge Case
- **Given** a viewport narrowed to exactly 679px
- **When** a panel opens
- **Then** it takes over the full content width (phone mode) — there is NO overlay mode (SP-25 deleted it)
- **When** the viewport is widened to exactly 680px
- **Then** the panel docks beside the chat — the 360px + 320px floors fit exactly at 680px

#### Scenario: No remembered width falls back to the default
**Traces to**: US-3, AS-3 · **Category**: Happy Path
- **Given** a fresh browser profile (no stored width for this user + panel + workspace)
- **When** the panel opens
- **Then** the width is 45% of the row clamped to [320, 720]

#### Scenario: Reset deletes the stored width
**Traces to**: US-3, AS-4 · **Category**: Alternate Path
- **Given** a stored width of 900px
- **When** the border is double-clicked (reset) and the panel reopens
- **Then** the default applies again — the stored value was DELETED, not stored as a px default

#### Scenario: Transient shrink never overwrites the stored width
**Traces to**: US-3, AS-6 · **Category**: Edge Case
- **Given** a stored width of 950px
- **When** the window shrinks (panel re-clamps to 640px) and then returns
- **Then** the panel shows 950px again and the stored value is still 950px (MAJ-009)

#### Scenario: "Watch live" with an unsaved Library edit prompts before replacing
**Traces to**: US-4, AS-1 + AS-4 · **Category**: Error Path
- **Given** the Library panel open with an unsaved edit
- **When** "Watch live" is triggered on a browser tool call
- **Then** the discard-confirmation appears
- **And** cancelling leaves the Library panel open with the edit intact and the Browser closed

#### Scenario: Deep-link replace with an unsaved Library edit prompts
**Traces to**: US-4, AS-3 + AS-4 · **Category**: Error Path
- **Given** the Library panel open with an unsaved edit
- **When** a link with a different `panel` value is followed
- **Then** the discard-confirmation appears and a cancel leaves Library open, URL unchanged

#### Scenario: Workspace switch with an unsaved Library edit prompts
**Traces to**: US-3, AS-5 · **Category**: Error Path
- **Given** the Library panel open on workspace A with an unsaved edit
- **When** the operator switches to workspace B
- **Then** the discard-confirmation appears and a cancel keeps the Library on workspace A

#### Scenario: A clean panel is replaced without any prompt
**Traces to**: US-4, AS-5 · **Category**: Happy Path
- **Given** the Library panel open with a CLEAN editor state
- **When** any replace path fires (Watch live, deep link, workspace switch, toggle)
- **Then** the replacement happens immediately — no dialog

#### Scenario: Deep link replaces the open panel
**Traces to**: US-4, AS-3 · **Category**: Alternate Path
- **Given** the Tasks panel open (clean), **When** a link with `?panel=library` is followed
- **Then** Library docks in Tasks' place and the URL shows `?panel=library`

#### Scenario: Full-page route shows its entry active without a panel
**Traces to**: US-5, AS-4 · **Category**: Alternate Path
- **Given** a deep link to `/workspaces/{id}/team` (wave 3)
- **When** the page renders
- **Then** the Team entry shows active (current page), no panel is open
- **And** clicking the entry navigates to Chat and opens the Team panel

#### Scenario: Compact dropdown mirrors the strip's toggle semantics
**Traces to**: US-5, AS-5 · **Category**: Alternate Path
- **Given** a container narrower than `72rem` (**1008px** at this app's 14px root — units
  corrected 2026-09-27 from 1152px; the rule and its behaviour are unchanged, compact
  dropdown)
- **When** the operator toggles the Library entry in the dropdown
- **Then** the panel opens/closes exactly as on the full strip, the entry shows the same pressed state, and the trigger label stays "Chat"

#### Scenario: Mixed-mode strip renders links and toggles together
**Traces to**: US-5 (mixed mode, MAJ-012) · **Category**: Alternate Path
- **Given** wave 1 (only library + browser registered)
- **When** the strip renders
- **Then** Library shows as a toggle with `aria-pressed` while Tasks/Team/Calendar remain navigation links with no pressed state

#### Scenario: Reload restores the open panel — EXCEPT Browser
**Traces to**: US-7, AS-1 · **Category**: Happy Path
- **Given** any panel except Browser open on a workspace Chat route
- **When** the operator reloads
- **Then** the same panel restores at its default view with its width

#### Scenario: Reload does NOT restore the Browser panel (SP-28)
**Traces to**: US-7, AS-1 · **Category**: Error Path
- **Given** the Browser panel open on a live session
- **When** the operator reloads
- **Then** no Browser panel restores — the chat renders without a panel
- **And** the session id appears NOWHERE in the URL (never in a shareable link)
- **And** the panel comes back only via its entry point ("Watch live")

#### Scenario: Sign-in returns to the opened link (SP-27)
**Traces to**: US-7, AS-2 · **Category**: Alternate Path
- **Given** a signed-out tab opening `…chat?panel=calendar` (the auth gate intercepts)
- **When** the operator signs in
- **Then** they land on the link they opened — workspace restored AND the Calendar panel restored — never on bare `/`

#### Scenario: Address-bar navigation with unsaved edits is blocker-gated (MAJ-205)
**Traces to**: US-7, AS-8 · **Category**: Error Path
- **Given** the Library panel open with an unsaved edit
- **When** a navigation that starts at the URL fires (pasted deep link, Back/Forward, sidebar click to another workspace)
- **Then** the router blocker runs the Library's `beforeLeave` discard confirmation
- **And** cancelling leaves the route, the address and the panel EXACTLY unchanged (a workspace-switch cancel stays on the outgoing workspace's chat with the Library as it was)

#### Scenario: Back/Forward lets the store win over a stale panel value (MAJ-206)
**Traces to**: US-7, AS-7 · **Category**: Edge Case
- **Given** history entries carrying different `panel` values from before (REPLACE kept them stale)
- **When** the operator presses Back or Forward
- **Then** the panel state follows the STORE — the stale URL value is ignored and re-projected
- **And** the phone pushed entry is the one exception: Back over it closes the panel (SP-26)

#### Scenario: A link with no panel param opens nothing
**Traces to**: US-7, AS-3 · **Category**: Happy Path
- **Given** a workspace Chat URL with no `panel` param
- **When** it is opened
- **Then** no panel opens

#### Scenario: Bare browser deep link drops the param without creating a session
**Traces to**: US-7, AS-5 · **Category**: Error Path
- **Given** a link `…chat?panel=browser` with no session/agent context
- **When** it is opened
- **Then** no browser session is created (SP-21), the param is dropped with a URL replace, and just the chat renders

#### Scenario: Mail deep link without a mailbox opens the choose state
**Traces to**: US-7, AS-6 · **Category**: Alternate Path
- **Given** a link `…chat?panel=mail` with no agent/mailbox selected (wave 2)
- **When** it is opened
- **Then** the Mail panel opens on its "choose a mailbox" state (SP-23)

#### Scenario: Panel toggles leave browser history untouched
**Traces to**: US-7, AS-7 · **Category**: Alternate Path
- **Given** any sequence of panel open/switch/close actions
- **When** the operator presses Back
- **Then** the previous PAGE (not a previous panel state) appears — history identical to today's (SP-22)

#### Scenario: Closing the phone takeover restores the chat
**Traces to**: US-8, AS-2 · **Category**: Happy Path
- **Given** a panel open at <680px (chat inert)
- **When** the panel is closed
- **Then** the chat region becomes interactive again and the panel is gone

#### Scenario: Phone Back closes the panel (SP-26 affordance 2)
**Traces to**: US-8, AS-3 · **Category**: Alternate Path
- **Given** a panel open in phone takeover (one history step was PUSHED on open)
- **When** the phone's Back gesture/button is used
- **Then** the panel closes, the chat shows, and history is back at the pre-open entry
- **And** on desktop no such step was pushed — Back still behaves exactly as today (SP-22 REPLACE)

#### Scenario: Phone swipe-to-close honours its thresholds (SP-26 affordance 3)
**Traces to**: US-8, AS-4 · **Category**: Alternate Path
- **Given** a panel open in phone takeover
- **When** a touch drag starts in the 24px left-edge zone and moves rightward
- **Then** the panel tracks the finger, and closes on ≥96px travel OR velocity ≥0.4px/ms after ≥48px
- **When** the drag ends short of both thresholds
- **Then** the panel springs back open

#### Scenario: In-content horizontal scroll never closes the panel (SP-26 conflict rule)
**Traces to**: US-8, AS-5 · **Category**: Error Path
- **Given** panel content with its own horizontal scroller (Library carousel, Calendar week view)
- **When** the operator swipes horizontally INSIDE that scroller
- **Then** the content scrolls and the panel does NOT close (scroller-ancestor direction check)
- **And** the panel only closes from an edge-zone swipe, or when content opts out by claiming the pan

#### Scenario: Focus moves into the panel on open
**Traces to**: US-9, AS-3 · **Category**: Happy Path
- **Given** no panel open
- **When** the operator opens a panel by clicking its toggle
- **Then** focus moves into the panel's header

#### Scenario: Panel is a labelled complementary landmark
**Traces to**: US-9, AS-4 · **Category**: Happy Path
- **Given** any panel open
- **When** a screen-reader user lists landmarks
- **Then** the panel appears as a complementary landmark labelled by its title

#### Scenario: Pop-out close never clobbers a panel opened since
**Traces to**: US-4 / §6 (MAJ-006) · **Category**: Error Path
- **Given** a Library full-page pop-out tab open and, in the source tab, the Tasks panel now open
- **When** the pop-out tab is closed
- **Then** the re-dock is a NO-OP — the Tasks panel stays open and untouched

#### Scenario: Pop-out close re-docks ONLY in the opener tab (MAJ-208)
**Traces to**: US-6 / §6 (MAJ-208) · **Category**: Edge Case
- **Given** a Library pop-out opened from tab 1, and a THIRD tab independently showing the same workspace's chat with no panel
- **When** the pop-out tab is closed
- **Then** only tab 1 — the tab holding the window handle — re-docks the Library
- **And** the third tab stays as it was (no panel appears there)

#### Scenario: Pop-out re-dock follows the pop-out's last workspace
**Traces to**: US-6 / §6 (MAJ-208, Dana fix) · **Category**: Alternate Path
- **Given** a Library pop-out for workspace A, whose tab later navigated to workspace B's Library
- **When** the pop-out tab is closed
- **Then** the opener re-docks the Library for workspace B (where the pop-out actually was), through the same leave guard as any open

#### Scenario: Workspace switch moves each panel per SP-29
**Traces to**: US-3, AS-5 (SP-29) · **Category**: Alternate Path
- **Given** the Browser panel anchored to its session AND the Tasks panel open, workspace A
- **When** the operator switches to workspace B
- **Then** Browser stays exactly as it is (anchored to its session — not closed, not moved)
- **And** Tasks re-targets to workspace B; Calendar and Mail would too
- **When** instead a Library opened from the sidebar (all-workspaces root) is open
- **Then** it does NOT follow — it stays at the root scope

#### Scenario: Every entry point honours the already-open tab (SP-30)
**Traces to**: US-6, AS-2 · **Category**: Alternate Path
- **Given** a Library full-page tab open (app-opened or manual)
- **When** ANY Library entry point fires — the sidebar Library button, "Open library", "Watch live" for Browser, the tab-strip toggle, Expand
- **Then** each switches to the already-open tab per the single §8.3 list — none docks a second copy

#### Scenario: Presence is scoped per panel × workspace
**Traces to**: US-6, AS-5 · **Category**: Alternate Path
- **Given** a Library full-page tab open for workspace A (manual)
- **When** the Library toggle is clicked in a tab showing workspace B
- **Then** workspace B does NOT treat it as "already open" — the Library docks here for workspace B (SP-18)

#### Scenario: All six panels dock through the same shell (MIN-202 gap: US-1 AS-4)
**Traces to**: US-1, AS-4 · **Category**: Happy Path
- **Given** all six panels registered (waves 1–3 combined)
- **When** each opens in turn
- **Then** each docks with the same border, header and actions — no panel-specific hosting code beyond content + expand target

#### Scenario: Width memory is per user as well as per workspace (MIN-202 gap: US-3 AS-2)
**Traces to**: US-3, AS-2 · **Category**: Edge Case
- **Given** user 1 has a remembered Library width for workspace A
- **When** user 2 signs in on the SAME browser profile and opens the Library in workspace A
- **Then** user 2 gets the default width — never user 1's value (the storage key carries the username)

#### Scenario: Tab-strip markup change preserves test ids (MIN-202 gap: US-5 AS-6)
**Traces to**: US-5, AS-6 · **Category**: Edge Case
- **Given** the tab strip rewritten from tablist to toggle group
- **When** the existing Playwright specs run
- **Then** they pass unchanged — the `workspace-tab-*` test ids persist (semantics changed; markup ids did not)

---

## 12. Test-driven development plan

Tests are written before implementation, in this order. All frontend; per repo rules
the TypeScript gate is `npm run typecheck`, vitest runs one file at a time locally, and
full suites/CI are the authority. Every test file must match a CI vitest group pattern
(`scripts/check-vitest-coverage.mjs` is the tripwire) — colocate under the component
dirs already covered (library, browser, workspaces) or the routes' colocated tests.

### Test implementation order

| Order | Test name (behaviour) | Level | Traces to BDD | Level notes |
|---|---|---|---|---|
| 1 | Panel registry resolves every registered id to title + expand target; an UNREGISTERED id resolves to nothing | Unit | Header/docking scenario | Wave-1 registry (library, browser); MAJ-012 |
| 2 | Width clamp math: [320, min(70%·row, row − sidebar − 360)], default `clamp(0.45×row, 320, min(720, ceiling))`; below 680px → takeover (no overlay exists) | Unit | Drag clamp; 680 boundary | Pure functions |
| 3 | Width memory: per-user key build + read/write/delete; reset deletes; transient re-clamp never writes (MAJ-009) | Unit | Width-memory outline; shrink-restore | Storage injected |
| 4 | Single-panel reducer: open replaces, toggle closes, close clears URL param; EVERY path awaits `beforeLeave`, a `false` cancels store+URL (CRIT-001) | Unit | One-at-a-time; guard scenarios | SP-7/SP-11/FR-013 |
| 5 | Panel-param validation: registered ids kept; unknown AND unregistered-but-future ids dropped via URL replace | Unit | Unknown id dropped | SP-14/MAJ-012 |
| 6 | Deep-link restore maps `panel=<id>` to state; ANY `panel=browser` value drops (SP-28 — bare or with context, never restored); mail no-mailbox → choose state (SP-23) | Unit | Deep link restores; browser never restores; mail choose-state | SP-21/SP-23/SP-28 |
| 7 | Shell renders header (title/Expand/Close) + content + separator | Component | Panel docks beside chat | Vitest, shell component |
| 8 | Library under shell: EVERY close/replace path runs the guard — header Close, Expand, open-other (Watch live, tab toggle, sidebar), deep-link replace, workspace switch; cancel keeps panel + edit | Component | Guard scenarios (Close, Watch live, deep-link replace, workspace switch) | `confirmDiscardLibraryEdits` via `beforeLeave`, CRIT-001 |
| 8b | Cancelled transition is clean: store, URL and content untouched after a `false` from `beforeLeave` | Component | Guard scenarios (cancel clean) | CRIT-001's loss mechanism was store-switch-first — this pins the order |
| 8c | Re-dock no-clobber: pop-out close re-docks ONLY into an empty slot | Component | Re-dock no-clobber | MAJ-006 |
| 9 | Browser under shell: pop-out ownership handover preserved (re-dock on close) | Component | Expand scenarios | Existing handoff reactions |
| 9b | Browser under shell: width change triggers the existing settle/handover with visible status | Component | Browser handover | FR-015's ONLY mapping (MAJ-011 fix) |
| 10 | Separator a11y: role/orientation/name/controls/valuetext/values, 16px steps, Home/End | Component | Keyboard walkthrough; landmark | MIN-003 |
| 11 | Tab-strip toggle semantics: `aria-pressed` model, dropdown parity, mixed mode (links vs toggles) | Component | Tab toggles; dropdown parity; mixed mode | MAJ-007/MAJ-012 |
| 12 | Phone: takeover + inert chat + no handle at <680px; grows out cleanly at 680; the three SP-26 affordances — ✕, Back (one pushed step, desktop REPLACE unchanged), swipe (edge zone, thresholds, scroller non-conflict) | Component | Phone scenarios; SP-26 affordance scenarios | `useMediaQuery`/touch emulation |
| 13 | Escape layers: modal above panel first; BROWSER Escape never closes (SP-19); focus follows the per-case rules | Component | Escape scenarios | SP-19/MIN-002 |
| 14 | Already-open tab: handle focus WITHOUT re-navigating; presence list keyed by identity key (MAJ-201); synchronous resolution in the gesture (MAJ-209, no wait window); affordance; presence clears; stale handle dropped; blocked popup → toast + panel stays | Component | Reuse/detection scenarios; popup-blocked toast | MAJ-002/SP-18/SP-30/MAJ-209 |
| 15 | Full-page routes still render (deep links to `/workspaces/{id}/team` etc.); Back after toggles equals today's history | Integration | US-5.4; back-button scenario | SP-22 |
| 16 | E2E: the §9 click-test rows, SPLIT BY GATE (SP-31): wave-0 rows (1–16) run against the static Storybook build; the wave-1 rows (W1–W12) are the real-app rows — AUTOMATED where Playwright can reach them (W1–W3, W9–W11; OBS-201: W4/W5/W7 as Playwright multi-tab where feasible) and click-tested by team-lead otherwise — spec files assigned to a `ui-*` group in `tests/e2e/shards.json` (`scripts/e2e-shards.sh check` enforces assignment; MIN-008) | E2E | All | Owner: qa-lead, `tests/e2e` |
| 17 | Cross-account denial on `/browser-live`: a second account opening the owner's full-page link sees the visible denial surface, never the stream (MIN-009, re-scoped to the full-page route — the panel form never carries a session id, SP-28) | Integration | Browser-never-restores scenario | Owner: qa-lead |
| 18 | Sign-in return: signed-out deep link → sign-in → land back on the linked workspace + panel (SP-27, SC-004) | Integration | Sign-in returns scenario | Router redirect capture |
| 19 | Pop-out close re-dock: ONLY the opener tab re-docks (handle-keyed); a third tab shows nothing; the re-dock follows the pop-out's last workspace through the leave guard | Component | Opener-only re-dock; Dana-fix scenarios | MAJ-208 |
| 20 | Workspace switch with panels open: Browser anchored (untouched), Library root scope does not follow, Library workspace-scoped follows, Tasks/Calendar/Mail follow (SP-29) | Component | SP-29 scenario | Per-panel `beforeLeave`/re-target |
| 21 | Phone affordances + boundary: ✕ closes; Back (pushed step) closes and restores history; swipe thresholds close; scroller-internal swipe scrolls instead; 679px takeover vs 680px docked | Component | SP-26 scenarios; 680 boundary | Touch emulation + resize |

### Test datasets

#### Dataset: width bounds (drives tests 2, 10)

Basis: the root flex row width; the sidebar term is 256px when pinned (≥1024px, per
the pinned preference) and 0 when not. Expected = requested clamped to
[320, min(70%·row, row − sidebar − 360)]; no request → default
`clamp(0.45×row, 320, min(720, ceiling))`, floored. **There is no overlay band** —
below 680px total the takeover takes over (SP-25); the floors 360+320 fit at exactly
680px, so the docked layout is reachable at every width ≥680px (sidebar term 0 below
1024px; at ≥1024px a pinned row is ≥768px) — the invariant that let SP-25 delete
overlay.

| # | row / sidebar | requested | expected | Traces to |
|---|---|---|---|---|
| 1 | 1400 / 256 pinned | 980 | 784 (= min(980, 1400−256−360)) | BDD drag clamp |
| 2 | 1400 / unpinned | 1200 | 980 (70% cap) | BDD drag clamp |
| 3 | 1400 / pinned | 100 | 320 (floor) | BDD drag clamp |
| 4 | 1000 / unpinned | 900 | 640 (= min(700, 1000−360) — chat floor binds) | BDD drag clamp / window shrink |
| 5 | 1400 / pinned | none | 630 (default; 0.45×1400=630, ceiling not binding) | BDD double-click reset |
| 6 | 900 / unpinned | none | 405 (default; ceiling 540 not binding) | default |
| 7 | 680 / unpinned | 500 | 320 (ceiling min(476, 320)=320 — chat exactly at its floor) | 680 boundary |
| 8 | 680 / unpinned | none | 320 (raw default 306 clamps up to the 320 floor) | 680 boundary |
| 9 | 1024 / pinned | none | 408 (raw 45% default 461 clamps to ceiling 1024−256−360=408 — chat exactly 360) | default/ceiling |
| 10 | 1119 / pinned | none | 503 (raw 503.55 floored; ceiling 503 binds) | default/ceiling |
| 11 | 1120 / pinned | none | 504 (ceiling 504) | default/ceiling |
| 12 | 1023 / unpinned | 900 | 663 (= min(716, 1023−360)) | ceiling |
| 13 | 1024 / unpinned | 900 | 664 (= min(716, 1024−360)) | ceiling |
| 14 | 1024 / pinned | 900 | 408 (ceiling) | ceiling |
| 15 | 1400→1000→1400 / unpinned | stored 950 | 950→640→950: transient re-clamp at 1000 is NOT written; stored stays 950 (MAJ-009) | shrink-restore |
| 16 | 679 / unpinned | any | NO resize — <680 is phone takeover (SP-25); no docked width applies | takeover |

#### Dataset: panel param validation (drives test 5, 6)

| # | input value | outcome | Traces to |
|---|---|---|---|
| 1 | `library` | opens Library | deep link restores |
| 2 | `calendar` | opens Calendar (wave 3) | deep link restores |
| 3 | `bogus` | dropped, URL replaced, no panel | unknown id dropped |
| 4 | (absent) | no panel | US-7.3 |
| 5 | `browser` — bare | dropped, URL replaced, NO session created (SP-21 + SP-28) | §8.2 |
| 6 | `browser&session=…&agent=…` | ALSO dropped — SP-28 excludes Browser from URL restore entirely; no session id is ever placed in a shareable chat-link | §8.2 |
| 7 | `tasks` in wave 1 (unregistered) | dropped, URL replaced, no panel (treated exactly like `bogus` — MAJ-012) | unknown id dropped |
| 8 | `mail`, no agent selected | Mail panel opens on "choose a mailbox" (SP-23, wave 2) | §8.2 |
| 9 | `browser&session=…` pasted while that session is LIVE in another tab | STILL dropped — restore never happens even when the session exists; reopening goes via "Watch live" (SP-28) | §8.2 |

#### Dataset: regression — preserved behaviours (drives test 15 + existing suites)

| # | preserved behaviour | existing evidence | Traces to |
|---|---|---|---|
| 1 | `/library?workspace=…&path=…` deep link renders fullscreen explorer | `src/routes/_app/library.tsx::LibraryRoute`; `-library.deep-link.test.tsx` | US-5.4 |
| 2 | `media` route opens Library scoped + redirects | `WorkspaceMediaRedirect`; `-workspaces.$workspaceId.media.test.tsx` | US-5.4 |
| 3 | Browser pop-out blocked → toast + panel stays | `BrowserLivePanel.handlePopOut` catch | Error flows |
| 4 | Library pop-out carries selection; docked re-docks on popout close | `LibraryPanel.handlePopOut`; `LibraryRoute` announcements | US-6.1 |
| 5 | Chat region inert on phone while panel open | `AppShell.tsx::AppShell` | US-8.1 |
| 6 | Browser pop-out close re-docks ONLY into an empty slot — it never replaces a panel opened since (semantic change from today's unconditional re-dock, encoded as a regression test so it cannot silently revert) | `LibraryPanel.tsx::onLibraryPopoutClosed`; `BrowserLivePanel.tsx::watchPopoutClosed` — both re-open unconditionally today | §6 re-dock rule (MAJ-006) |
| 7 | Pop-out close re-docks ONLY in the opener tab (the one holding the window handle); a third tab showing the same workspace never re-docks (semantic change, MAJ-208) | `BrowserLivePanel.tsx::watchPopoutClosed` (opener-owned `ownedPopout`) | §6/§8.3 (MAJ-208) |
| 8 | Sidebar-rooted Library does NOT follow a workspace switch (stays at root scope); workspace-scoped Library still follows (SP-29 change) | `Sidebar.tsx` `openLibraryPanel()` (no workspace today) | §6/US-3 AS-5 (SP-29) |

### Regression test requirements

This feature MODIFIES existing functionality. Existing suites that MUST keep passing
(modulo the intentional SP-7 change): the library, browser, workspaces component
groups (their CLAUDE.md files name the CI groups `components-misc`,
`components-workspaces`), the routes' colocated tests, and the Playwright specs using
`workspace-tab-*` test ids (tab-strip markup persists; semantics change to toggles —
assertions on navigation to board/team/calendar from the STRIP change deliberately in
wave 3, and those specs are updated in that wave, not silently).

Three intentional behaviour changes to encode as NEW regression tests (so they can never
silently revert):
1. Simultaneous Library+Browser open is no longer possible (SP-7).
2. Library/Browser panels have no per-panel pop-out/close buttons; the shell header
   carries those actions (§2.2/§2.3).
3. A pop-out close re-docks only into an empty slot (MAJ-006) — never over a panel the
   operator opened since.
4. A Library opened from the sidebar (all-workspaces root) NO LONGER follows a
   workspace switch — it stays at the root scope; a workspace-SCOPED Library still
   follows (SP-29 changes today's follow-always behaviour).
5. A pop-out close re-docks ONLY in the tab holding the window handle — other tabs
   showing the same workspace never re-dock (MAJ-208 narrows today's
   popout-closed → re-open path to the opener).

No backend regression surface exists (§1).

---

## 13. Functional requirements & success criteria

### Functional requirements

Wave tags (MAJ-012): `[wave 1]` ships with shell+Library+Browser; `[wave 2]` with Mail;
`[wave 3]` with Team/Tasks/Calendar. A `[wave 1]` behaviour applies to every later
panel automatically.

- **FR-001** [wave 1 shell; panels per wave]: The system MUST host every panel (library,
  browser, mail, tasks, team, calendar) in ONE shared shell providing title, Expand,
  Close, resize border, docking beside the chat, phone takeover and Escape-to-close
  (Browser per FR-012); panels supply only content and expand target. [SP-4; US-1]
- **FR-002** [wave 1]: The shell migration MUST preserve every capability in the §2.2/§2.3
  inventories; per-panel Expand/Close buttons move to the shell header, content
  controls stay. [SP-4; US-1]
- **FR-003** [wave 1]: Every panel MUST be resizable by dragging its border, clamped to
  [320px, min(70% of row, row − sidebar − 360px)] (SP-17); there is NO overlay mode —
  below 680px total the phone takeover applies (SP-25), and the floors are guaranteed
  to fit at every docked width; default `clamp(0.45 × row, 320px, min(720px, ceiling))`;
  double-click reset. [SP-2, SP-3, SP-17, SP-25; US-2]
- **FR-004** [wave 1]: The resize separator MUST be keyboard accessible (focusable, role
  separator, arrow keys 16px steps, Home/End) with full ARIA state: accessible name,
  `aria-controls`, `aria-valuetext`, dynamic `aria-valuemax` (MIN-003). [SP-3; US-9]
- **FR-005** [wave 1]: Remembered width MUST be keyed per USER + panel + workspace
  (SP-20, `app` bucket when none) in browser-local storage — key
  `panel-width:<username>:<panelId>:<workspaceId|app>` (MIN-204; the username is the
  SPA's only account identifier); written ONLY on a user choice settling — drag
  release, keyboard change, or 300ms after the last resize keystroke (MIN-205; the
  settle write doubles as the pop-out handover trigger); reset DELETES the key; a
  window-driven re-clamp is transient and never writes (MAJ-009); clamp re-applied at
  read time; a storage failure degrades to memory-only for the session — never an
  error surfaced to the user. [SP-13, SP-20; US-3]
- **FR-006** [wave 1]: At most one panel MUST be open at any instant; opening one replaces the
  open one across ALL entry points, every path through the `beforeLeave` gate
  (FR-013). [SP-7, CRIT-001; US-4]
- **FR-007** [wave 1 model / wave 3 entries]: Tab-strip entries for registered panels
  MUST toggle their panel — `aria-pressed` while open, second click closes — on both the
  full strip and the compact dropdown (MAJ-007's ARIA model); unregistered entries stay
  navigation links until their wave; Chat stays the page underneath; Settings stays
  a page. [SP-11, SP-6; US-5]
- **FR-008** [wave 1]: Expand MUST open the panel's full-page route in a NEW browser tab
  (plain `_blank`; no stable window name — MAJ-202) and close the docked panel in the
  source tab; the open runs synchronously in the user gesture (MAJ-209), the handle is
  kept by severing `window.opener` after the open, and a blocked popup is fail-visible
  (toast, docked panel stays). [SP-12, MAJ-202, MAJ-209; US-6]
- **FR-009** [wave 1]: If a panel's full-page tab with the SAME identity key is already
  open, re-invoking ANY of its entry points MUST SWITCH to it — toggle, Expand, sidebar
  Library, "Open library", "Watch live" (SP-30) — via the in-memory handle registry
  (focus + context post, never re-navigation) when the app holds the handle, or via the
  continuous BroadcastChannel presence list (no wait window — OBS-202) with no docked
  open and the visible "already open — switch" affordance for manual tabs; never a
  duplicate; identity keys per §8.1 (MAJ-201) matched by the panel's CURRENT scope
  (MAJ-213). [SP-9, SP-18, SP-30, MAJ-002, MAJ-201, MAJ-213, OBS-202; US-6]
- **FR-010** [wave 1]: The open panel MUST be URL-addressable as `panel=<registered id>`
  under the workspace Chat route's hash search, restored on reload and shared links
  (Browser excluded per FR-019's SP-28 rule); unknown or unregistered ids dropped with
  URL replace; open/switch/close use history REPLACE, never push (SP-22), with the
  store winning over stale URL values on Back/Forward (MAJ-206) and the one SP-26
  phone-mode exception; a `beforeLeave`-gated router blocker (MAJ-205) gates every
  URL-initiated navigation over unsaved edits — cancel leaves route, URL and panel
  unchanged. [SP-14, SP-22, MAJ-012, MAJ-205, MAJ-206; US-7]
- **FR-011** [wave 1]: Below 680px the panel MUST take over the full content width, the chat
  region MUST be inert, and no resize handle MUST render; crossing 680px MUST
  transition cleanly (with the phone history step collapsing on the way up). [SP-3,
  SP-4, SP-25, SP-26; US-8]
- **FR-012** [wave 1]: Escape MUST close the topmost layer (modal above panel first, then
  panel) — EXCEPT the Browser panel, where Escape NEVER closes the panel and keeps its
  "stop driving" meaning (SP-19); honouring the Library unsaved-edits guard; focus
  MUST move into the panel on open and follow US-9's per-case rules on close.
  [SP-4, SP-19; US-1, US-9]
- **FR-013** [wave 1]: The Library unsaved-edits guard MUST wrap EVERY action that closes or
  replaces the Library panel — header Close, Expand, open-other from ANY entry point
  (tab toggle, sidebar, ChatControls, "Watch live"), deep-link replace, and
  workspace-switch re-target — via the `PanelDefinition.beforeLeave` gate; the
  transition proceeds only on confirm and a cancel leaves store, URL and panel
  untouched. No silent data loss on any path (CRIT-001). [§2.2; US-1, US-4]
- **FR-014** [wave 2/3]: Adding Mail (wave 2) and Tasks/Team/Calendar (wave 3) MUST require only
  a new panel registration plus per-panel narrow layouts — no shell modification.
  [SP-4, SP-6, SP-8; US-1]
- **FR-015** [wave 1]: The Browser panel's resize MUST drive the existing remote-viewport
  handover triggered on SETTLE (300ms after the last resize keystroke — MIN-205, so a
  fast drag never floods the remote viewport), with its visible status and Retry
  (ADR-061 "Remove JPEG screencast fallback" posture), never a silent stall. [US-2]
- **FR-016** [wave 0–3, per SP-24]: Every panel the shell touches MUST have Storybook
  stories with sample data (no live gateway) — shell/Library/Browser in wave 0–1, Mail
  in wave 2, Team/Tasks/Calendar in wave 3, each replacing its wave-0 demo stand-in;
  the wave-0 static Storybook build IS the SP-10 demo (SP-16). [SP-15, SP-16, SP-24; §9, §10]
- **FR-017** [wave 1]: The shell MUST satisfy the design-system publication contract (public
  export → catalog → manifest → story → executed checks, per the design-system skill;
  touch-target and focus-visible on the separator included). [MAJ-008; US-1]
- **FR-018** [wave 1]: A full-page pop-out's close MUST re-dock the panel ONLY in the tab
  that opened it (the window-handle holder) — a third tab showing the same workspace
  never re-docks — following the pop-out's last workspace, through the same
  `beforeLeave` guard as any open (MAJ-208; keeps the round-1 Dana re-dock fix).
  [MAJ-208; §6, §8.3]
- **FR-019** [wave 1]: The Browser panel MUST NOT be restorable from a reload or a shared
  link — ANY `panel=browser` URL value is dropped with a URL replace, no session id is
  ever placed in a shareable chat-link, and the panel reopens only via its entry points
  ("Watch live"); the auth gate MUST preserve the original hash URL so signing in
  returns to the opened link — workspace + panel restored (SP-27, closes SC-004).
  [SP-27, SP-28; US-7]
- **FR-020** [wave 1]: A workspace switch MUST move each open panel per its own scope:
  Browser anchored to its session (not closed, not moved), Library following only when
  it was opened scoped to the workspace being left (a sidebar-rooted Library stays at
  the all-workspaces root), Tasks/Calendar/Mail following — each through its own
  `beforeLeave` gate. [SP-29; US-3 AS-5, §6]
- **FR-021** [wave 1]: In phone takeover there MUST be THREE ways back to the chat: the
  header ✕; Back (opening a panel in phone mode adds ONE pushed history step — desktop
  keeps SP-22's REPLACE and Back behaviour); swipe-to-close (24px edge zone, rightward,
  close at ≥96px travel OR velocity ≥0.4px/ms after ≥48px; a panel-content
  scroller claiming the pan wins, so in-content horizontal scroll never closes the
  panel). [SP-26; US-8]

### Success criteria

- **SC-001** [wave-0 gate, SP-31]: Every §9 WAVE-0 click-test row passes in a
  real-browser run against the static Storybook build (SP-16 — the stories ARE the
  demo) with recorded evidence BEFORE the founder demo and the build (SP-10 gate).
- **SC-009** [wave-1 exit criterion, SP-31]: Every §9 WAVE-1 real-app row (address bar,
  new tab, deep links, live Browser, sign-in return, Back/Forward) passes against a
  running dev build during wave 1, before wave 1's own gate — the Storybook demo
  passing does NOT waive these rows.
- **SC-002**: The shell hosts a panel with no panel-specific hosting code — proven by
  a TEST-ONLY third registration that mounts through the shell with ZERO shell edits
  (MAJ-012's acceptance proof); production wave-1 code contains exactly two real
  registrations (library, browser).
- **SC-003**: A keyboard-only operator can open, resize (within 16px granularity),
  reset, switch, and close every panel without a mouse.
- **SC-004**: A shared link `…chat?panel=<id>` restores the named panel on a machine
  that has never visited the app before this call (width defaults, panel restores);
  a signed-out colleague returns to that link after signing in (SP-27) — the Browser
  is the exception (SP-28: never restored; the panel form of the link never carries a
  session id).
- **SC-005**: No simultaneous Library+Browser open exists anywhere in the app after
  wave 1 (grep + store shape prove one open path); the change is covered by a
  regression test per §12.
- **SC-006**: Existing Library/Browser capabilities from §2.2/§2.3 are each reachable
  in wave 1 (capability walk-through of both tables; reachability per Definition of
  Done).
- **SC-007**: Stories ship per wave (SP-24) — wave 0–1: shell + Library + Browser
  (+ demo-only stand-ins for the four later panels, clearly labelled); wave 2: Mail's
  real set replaces its stand-in; wave 3: Team/Tasks/Calendar's real sets replace
  theirs. No panel reaches its wave without stories.
- **SC-008**: No close/replace path can bypass the unsaved-edit guard — the §7
  `beforeLeave` state machine enumerates every path, and tests 8 + 8b cover each
  path and the cancelled-transition invariant (CRIT-001's failure mode was a
  store-switch-first order; the test pins the order).

### Traceability matrix

| Requirement | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | Panel docks beside chat; Escape closes topmost layer | 1, 7, 13 |
| FR-002 | US-1 | Panel docks beside chat (content controls intact); guard scenario | 7, 8, 9 |
| FR-003 | US-2 | Drag clamp; 680 boundary (takeover); double-click reset; window shrink | 2, 10, 12, 21 |
| FR-004 | US-9 | Keyboard walkthrough; landmark preserved | 10 |
| FR-005 | US-3 | Width-memory outline; shrink-restore; workspace switch | 3 |
| FR-006 | US-4 | Second panel replaces; toggle closes | 4 |
| FR-007 | US-5 | Tab toggle without navigation; dropdown parity; mixed mode | 11, 15 |
| FR-008 | US-6 | Expand opens new tab | 9, 14 |
| FR-009 | US-6 | Tab reused via handle (never re-navigated); manual tab detected; per-workspace presence | 14 |
| FR-010 | US-7 | Deep link restores; unknown AND unregistered ids dropped; back button = today | 5, 6, 15 |
| FR-011 | US-8 | Phone takeover; grows out of takeover | 12 |
| FR-012 | US-1, US-9 | Escape scenario (incl. Browser never-close); keyboard walkthrough | 13, 10 |
| FR-013 | US-1, US-4 | Guard scenarios (every path); cancelled transition clean | 8, 8b |
| FR-014 | US-1 | (registry design; SC-002 test-only registration) | 1 |
| FR-015 | US-2 | Browser handover on width change | 9b |
| FR-016 | — (§9/§10 demo + stories) | §9 wave-0 rows on the static build; W-rows on the real app | 16 |
| FR-017 | US-1 | Design-system publication checks | 7, 16 |
| FR-018 | US-6/§6 | Pop-out re-dock: opener-only; follows pop-out's last workspace; no-clobber | 8c, 19 |
| FR-019 | US-7 | Browser never restores (reload + ANY link); sign-in return | 6, 17, 18 |
| FR-020 | US-3 | Workspace switch moves panels per SP-29 | 20 |
| FR-021 | US-8 | Phone ✕ / Back / swipe; scroller non-conflict; 680 boundary | 12, 21 |

**Completeness**: every FR appears; every BDD scenario traces; test numbers refer to
§12's order column (including 8b, 8c, 9b).

### Decision coverage (SP-16..SP-31 + round-2 defaults)

| Decision | Decision (one line) | Lands in | Verified by |
|---|---|---|---|
| SP-16 | Stories ARE the demo — one static build, click-tested before the founder sees it | §9, §10, FR-016, SC-001 | Test 16; §9 wave-0 rows |
| SP-17 | Floors chat ≥360 / panel ≥320; ceiling min(70%, row−sidebar−360) — as amended by SP-25: no overlay, takeover below 680px | §4 US-2, §6, §7 Geometry, FR-003 | Test 2; width-bounds dataset; click-test rows 3, 10, 11 |
| SP-18 | Already-open elsewhere: ONLY switch, per identity key; honest browser constraint | §5, §8.3, FR-009 | Test 14; W4–W8 |
| SP-19 | Browser panel Escape NEVER closes the panel | §4 US-1 AS-3, §6, §7 Accessibility, FR-012 | Test 13; click-test row 16; §11 scenario |
| SP-20 | Width stored browser-locally, keyed per USER + panel + workspace | §1, §7 Persistence, FR-005 | Test 3 |
| SP-21 | Bare `?panel=browser` never auto-starts a session — drops the param | §3.1, §6, §8.2, FR-010 | Test 6; dataset rows 5–6 |
| SP-22 | Panel open/close/switch = history REPLACE; Back behaves as today | §4 US-7, §7 URL, FR-010 | Test 15; §11 back-button scenario |
| SP-23 | `?panel=mail` with no mailbox → "choose a mailbox" state | §6, §8.2, FR-010 | Test 6; dataset row 8 |
| SP-24 | Stories ship per wave with each panel | §9 Stories inventory, §10, FR-016, SC-007 | §10 wave gate |
| SP-25 | Overlay deleted; takeover threshold 680px | §4 US-2/US-8, §6, §7 Geometry, FR-003, FR-011 | Test 2; width-bounds rows 7, 8, 16; 680-boundary BDD row |
| SP-26 | Phone close: ✕ + Back (one pushed step) + swipe (edge zone, thresholds, scroller rule) | §4 US-8, §6, §7, FR-021 | Test 12/21; §11 SP-26 scenarios; wave-0 click rows 12–15 |
| SP-27 | Sign-in returns to the opened link (workspace + panel) | §8.2, FR-019, SC-004 | Test 18; W11 |
| SP-28 | Browser NEVER restored from reload/share; no session id in shareable links | §8.2, FR-019, SC-004 | Test 6/17; dataset rows 5/6/9; W3/W9 |
| SP-29 | Workspace switch per panel: Browser anchored; Library only if workspace-scoped; Tasks/Calendar/Mail follow | §6, FR-020 | Test 20; §11 SP-29 scenario |
| SP-30 | EVERY entry point switches to the already-open tab | §5, §8.3, FR-009 | Test 14; W5/W6 |
| SP-31 | Demo gate split: wave-0 Storybook rows; wave-1 real-app exit criterion | §9, §10, SC-001/SC-009 | Test 16; §9 W-rows |
| MAJ-201 | Identity keys: panelId × workspaceId (Browser + session + agent) | §8.1, §8.3, FR-009 | Test 14 |
| MAJ-202 | No stable window names; `_blank` opens; opener severed after | §8.3, FR-008 | Test 14; W4 |
| MAJ-203 | Default = clamp(0.45·row, 320, min(720, ceiling)) | §4 US-2, FR-003 | Test 2; dataset rows 5/6/8–11 |
| MAJ-205 | Router blocker gates URL-initiated transitions; cancel = unchanged | §4 US-7, §8.2, FR-010 | §11 blocker scenario; test 8 |
| MAJ-206 | Store wins on Back/Forward over stale panel values | §4 US-7, §8.2, FR-010 | §11 stale-Back scenario; test 15 |
| MAJ-208 | Pop-out re-dock: opener-only, follows last workspace | §6, §8.3, FR-018 | Tests 8c/19; regression rows 7 |
| MAJ-209 | Sync `window.open` in the gesture; blocked popup → toast + panel stays | §8.3, FR-008 | Test 14; W12 |
| MIN-204 | Width key `panel-width:<username>:<panelId>:<workspaceId\|app>`; prune own keys only; degrade memory-only | §7 Persistence, §8.1, FR-005 | Test 3 |
| MIN-205 | Width write + Browser handover on settle (300ms) | §4 US-3, FR-005, FR-015 | Test 3/9b |
| MIN-207 | Channel messages validated; no paths/selections on BroadcastChannel | §7 STRIDE note, §8.3 | Test 14 |
| MIN-208 | Escape ignored while composing; focus-inside via composed DOM path incl. portals | §4 US-9, §7 Accessibility | Test 13 |
| OBS-202 | Continuous presence list replaces the 150ms wait | §8.3, FR-009 | Test 14 |

*(Round-2 defaults applied without a new founder question: MAJ-201, MAJ-202,
MAJ-203, MAJ-205, MAJ-206, MAJ-208, MAJ-209 — per the grill round-2 defaults
table, §19b carries each finding's disposition.)*

---

## 14. Questions for the founder — all ANSWERED (2026-09-26)

This spec raised no open founder questions. Both questions the first draft carried
were answered with the fix round (SP-20, SP-21), and every question the grill round 1
review posed to the founder was answered in the same decision batch (SP-16..SP-24).
Recorded here so the answers are auditable; nothing is pending.

| Question | Answer | Decision |
|---|---|---|
| Spec Q1 — where is remembered width stored? | Browser-local localStorage, keyed per user + panel + workspace | SP-20 |
| Spec Q2 — bare `?panel=browser` deep link? | Drops the parameter; never auto-starts a (paid) session | SP-21 |
| Grill Q1 — what happens when the window is too narrow for chat + panel? | Floors (chat ≥360, panel ≥320); ceiling formula; overlay degradation | SP-17 (MAJ-001) |
| Grill Q2 — panel already open in another tab? | ONLY switch (no "open here too"), scoped per workspace; honest manual-tab degrade | SP-18 (MAJ-003) |
| Grill Q3 — do panel toggles push browser history? | History REPLACE; Back behaves exactly as today | SP-22 (MAJ-004) |
| Grill Q4 — should Browser Escape close the panel? | Never — Escape keeps "stop driving"; close only via ✕ | SP-19 (MAJ-005) |
| Grill Q5 — `?panel=mail` with no mailbox? | Mail opens on its "choose a mailbox" state | SP-23 (MAJ-010) |
| Grill Q6 — width-storage location? | Browser-local (same answer as spec Q1) | SP-20 |
| Grill Q7 — separate demo build or Storybook? | The SP-15 stories ARE the demo — one static build, click-tested first | SP-16 (MAJ-008) |
| Grill Q8 — do all seven story sets land up front? | No — per wave, with each panel | SP-24 |

**Grill round 2 (2026-09-26) — founder answers (SP-25..SP-31); no question left open:**

| Question | Answer | Decision |
|---|---|---|
| R2-Q1 — what happens when the window is too narrow for chat + panel? | Overlay is DELETED; the phone takeover threshold covers every window below 680px | SP-25 (MAJ-204) |
| R2-Q2 — shared links through sign-in? | After signing in the user returns to the opened link (workspace + panel restored) | SP-27 (MAJ-211) |
| R2-Q3 — Browser panel reload / shared links? | NEVER restored — reopen only via "Watch live"; no session id ever in a shareable link | SP-28 (MAJ-207) |
| R2-Q4 — workspace switch with panels open? | Per panel: Browser anchored to its session; Library follows only if opened scoped to the workspace being left; Tasks/Calendar/Mail follow | SP-29 (MAJ-212) |
| R2-Q5 — which entry points switch to the already-open tab? | EVERY entry point for same panel + same workspace — sidebar Library, "Open library", "Watch live" included | SP-30 (MAJ-213) |
| R2-Q6 — demo gate on real-app behaviours? | Split: Storybook stories gate the layout rows; address-bar/new-tab/deep-link/live-Browser rows are a wave-1 real-app exit criterion | SP-31 (MAJ-210) |

Defaults applied without a new founder question (grill round-2 stated defaults, each
disposition in §19b): MAJ-201, MAJ-202, MAJ-203, MAJ-205, MAJ-206, MAJ-208, MAJ-209,
plus the 11 MIN and 3 OBS findings at their own recommended defaults.

---

## 15. Reachability (Definition of Done)

Code-correct-and-tested and reachable-by-a-user are stated separately, per the
mandatory two-line delivery rule.

**How a real user reaches this feature** (all paths verified against the entry-point
inventory in §2.1):

1. Workspace tab strip (container ≥ `72rem`, **1008px** at this app's 14px root) and the
   compact view-switcher dropdown (below that — units corrected 2026-09-27 from the
   earlier 1152px/16px-root figure, no behaviour change):
   Tasks / Calendar / Library / Team entries toggle panels (Mail joins in wave 2).
2. Sidebar "Library" (virtual root), top-bar "Open library" / "Open browser", chat
   "Watch live" on browser tool-calls — all keep working through the new single-panel
   state.
3. Deep links: `?panel=<id>` on any workspace chat URL; bookmarked `media` URLs keep
   working via the retargeted redirect stub; full-page routes remain directly
   reachable and are the expand targets.
4. Mail (wave 2): the chat draft link opens the Mail panel (email spec's link).

**Reachability gates for this feature**:

- The §9 wave-0 click-test rows executed in a real browser against the static
  Storybook build (SP-16) with evidence, THEN the founder demo (SP-10) — before
  build; the §9 wave-1 real-app rows pass against a running dev build before wave
  1's own gate (SP-31, SC-009).
- Wave-1 delivery claims a user can: open Library and Browser from at least two entry
  points each, resize both, expand both to tabs, deep-link both, on desktop and phone
  widths — evidenced by executed tests, not written ones.
- CI vitest coverage tripwire (`scripts/check-vitest-coverage.mjs`) proves the new
  test files actually run in a CI group (a test file matching no group runs nowhere
  while CI stays green).

---

## 16. Evaluation scenarios (holdout — post-implementation only; excluded from the TDD plan and traceability)

1. **Happy**: fresh browser profile → workspace → drag Library to 60% → close → reload
   the app (no URL param) → reopen Library via tab entry. Expected: panel at 60%
   (remembered), chat at the remainder. Category: Happy Path.
2. **Happy**: three panels opened in sequence via three different entry points
   (tab strip, sidebar, Watch live). Expected: only the last is open at every step;
   no stranded highlight in the strip. Category: Happy Path.
3. **Happy**: founder shares `…chat?panel=calendar` to a colleague (wave 3) — colleague
   opens it logged-out, signs in. Expected: Calendar panel visible immediately on the
   linked workspace (SP-27 return). Category: Happy Path.
4. **Error**: kill the network after the Browser panel is open. Expected: existing
   WebRTC/connection error surfaces visibly with Retry inside the panel; the shell
   header keeps working (Close still closes). Category: Error.
5. **Error**: unsaved Library edit → press Escape → cancel. Expected: panel open, edit
   intact, focus back in the editor. Category: Error.
6. **Edge**: rapid double-toggle (two tab entries within ~200ms). Expected: exactly one
   panel open at the end; no animation tearing or duplicated URL history entries.
   Category: Edge Case.
7. **Edge**: resize panel to 70% cap, then zoom browser to 200% (window.innerWidth
   shrinks in CSS px). Expected: panel re-clamps to the new SP-17 ceiling
   (min(70% of row, row − sidebar − 360px); takeover below 680px, no overlay);
   nothing overflows the viewport. Category: Edge Case.

---

## 17. Assumptions

- The existing docked-flex-row hosting geometry stays (panels render as flex siblings
  in the root row — `AppShell.tsx`); the shell keeps that geometry, so chat absorption
  remains automatic. Moving the panel INTO the workspace screen (below the top bar) is
  explicitly not part of this spec.
- Panels remain app-global (openable from any screen, as today) — the workspace
  context, when present, comes from the route.
- Hash routing stays; the `panel` param is a router search param under `#/`.
- The demo is the static Storybook build (SP-16 — the stories ARE the demo), served
  as a static snapshot for the founder, not a dev server (founder rule 2026-09-26);
  stand-in stories for not-yet-migrated panels are demo-only and labelled (SP-24).
- Wave-2/3 details (Mail context shape, the three narrow layouts) are specified to
  contract level here and detailed in their own specs/branches (`feature/email-mail`
  for Mail; wave-3 spec for the narrow layouts).
- No telemetry, no new deps: resize uses native pointer events; BroadcastChannel is
  already in use (library/browser handoff), no polyfill (Safari 15.4+ supports it;
  presence detection degrades to "no manual-tab detection" on older browsers —
  the docked panel still opens, worst case a duplicate full-page tab, never a crash;
  founder browsers today all support it).
- **Resize primitive is BUILT, not ported** (checked 2026-09-26): the design-system
  catalog has zero resizable/split entries (`design-system/catalog.json`) and
  `package.json` has no resizable dependency; the docking/overlay/takeover interaction
  is bespoke to this shell. The separator is a shell-owned component published through
  the design-system chain (FR-017, MAJ-008) rather than a ported shadcn/ui primitive.
- **Overlay mode is DELETED (SP-25, round 2)** — the earlier assumption that SP-17's
  overlay degradation was not the retired Sheet-hosting mode is moot: nothing
  overlays the chat anymore. Below 680px total width the phone takeover applies; the
  docked layout always keeps chat ≥360px and panel ≥320px. The retired Sheet-hosting
  surface (root CLAUDE.md, "Retired surfaces") stays retired.
- **`/preview/` is same-origin (MIN-207)**: the shell's cross-tab messaging assumes
  preview apps are served on the main gateway listener — ADR-044 "Serve /preview/ on
  the main gateway listener". BroadcastChannel never carries file paths, selections or
  session contents; targeted `postMessage` to known handles only, messages validated
  on receipt.
- **Dev-only dependencies for the demo gate (SP-31)**: the no-new-deps stance is
  AMENDED for dev dependencies only — a Storybook request-mocking add-on (e.g.
  msw-storybook-decorator style) is allowed for wave-0 stories, and the phone-Back
  story needs a memory-router decorator; neither ships in the app bundle, and no
  runtime dependency is added.

## 18. Clarifications (from the founder interview — `spec-side-panel-shell.md`)

- Q: Panel or page for Mail? → A: Slide-out panel beside chat, resizable, expandable
  (SP-1, SP-2, SP-5).
- Q: One shared component? → A: Yes — one shell, panels supply content + expand target;
  no capability lost (SP-4, §2 inventories).
- Q: Workspace tabs as panels? → A: Team/Tasks/Calendar yes, after Mail; Settings
  stays a page; narrow layouts per SP-6 (§10).
- Q: Tab-strip semantics? → A: Chat stays the page; panel entries toggle; highlight +
  second-click close (SP-11).
- Q: Open in new tab — keep or close the source panel? → A: Close it (SP-12).
- Q: Width memory granularity? → A: Per panel AND per workspace (SP-13). Storage
  location → decided browser-local (SP-20, §14).
- Q: Deep links? → A: Yes — `?panel=<id>`, reload + shared links restore (SP-14).
- Q: Already-open tab? → A: Switch to it; browser constraints stated honestly —
  window-handle registry for app-opened tabs, presence list + affordance for manual
  tabs (SP-9, §8.3; mechanism as amended by MAJ-202).
- Q: Demo before build? → A: Yes — clickable, real-browser-verified, founder-approved
  (SP-10, §9).
- Q: Sequencing? → A: Shell + Library + Browser → Mail → Team/Tasks/Calendar (SP-8, §10).
- Q: Separate demo build or Storybook? → A: The stories ARE the demo — one static
  build, click-tested before the founder sees it (SP-16, §9).
- Q: Window too narrow for chat + panel? → A: Floors chat ≥360 / panel ≥320, ceiling
  min(70%, row − sidebar − 360); below 680px total the phone takeover applies — there
  is no overlay (SP-17 as amended by SP-25, §4 US-2).
- Q: Panel already open in another tab? → A: ONLY switch — per identity key (§8.1);
  app-opened tabs re-focused via the window-handle registry (no stable window name —
  MAJ-202), manual tabs detected via the continuous presence list with an affordance
  (SP-18, §8.3).
- Q: Do panel toggles push history? → A: No — history REPLACE; Back as today (SP-22, §8.2).
- Q: Does Browser Escape close the panel? → A: Never — Escape stops driving; close
  only via ✕ (SP-19, §7).
- Q: `?panel=browser` with no session? → A: Drops the param, starts nothing (SP-21, §8.2).
- Q: `?panel=mail` with no mailbox? → A: Opens on "choose a mailbox" (SP-23, §8.2).
- Q: Where is width stored, and when is it written? → A: Browser-local, per user +
  panel + workspace; written only on a user choice (SP-20 + MAJ-009 rules, §7).
- Q: All story sets up front? → A: No — per wave, with each panel (SP-24, §10).

**Grill round 2 (2026-09-26):**

- Q: Window too narrow for chat + panel (re-asked)? → A: Overlay DELETED; takeover
  below 680px (SP-25, §4 US-2/US-8).
- Q: Phone panel-close affordances? → A: THREE — ✕, Back (one pushed history step,
  phone only), swipe-to-close with thresholds that never conflict with in-content
  horizontal scroll (SP-26, §4 US-8).
- Q: Shared links through sign-in? → A: Return to the opened link — workspace + panel
  restored (SP-27, §8.2).
- Q: Browser panel on reload/shared links? → A: NEVER restored; reopen via "Watch
  live"; no session id in any shareable link (SP-28, §8.2).
- Q: Workspace switch with panels open? → A: Browser anchored to its session; Library
  follows only if opened scoped to the workspace being left; Tasks/Calendar/Mail
  follow (SP-29, §6).
- Q: Which entry points switch to the already-open tab? → A: EVERY entry point for
  the same panel + same workspace (SP-30, §8.3).
- Q: Demo gate on real-app behaviours? → A: Split — wave-0 Storybook rows gate the
  build; wave-1 real-app rows are the exit criterion (SP-31, §9).

---

## 19. Review-finding dispositions (grill round 1 — `side-panel-shell-spec-spec-review.md`)

Every finding from the round-1 grill has a visible disposition below; none is
silently dropped. CRIT and MAJ findings are resolved in the body (§/US column);
MIN findings are either fixed or dispositioned; OBS are noted.

| Finding | Disposition | Where |
|---|---|---|
| CRIT-001 (unsaved-edit guard bypass) | **FIXED** — `PanelDefinition.beforeLeave` gate wraps EVERY close/replace path (header Close, Expand, open-other from any entry point, deep-link replace, workspace switch); the shell awaits it BEFORE touching store/URL/content; cancel leaves everything untouched | §2.2, §4 US-4 AS-4/AS-5, §7 State machine, §8.1, FR-013, tests 8/8b, SC-008 |
| MAJ-001 (chat minimum width) | **FIXED** — SP-17: floors chat ≥360 / panel ≥320; ceiling min(70%, row − sidebar − 360); overlay degradation | §4 US-2, §6, §7 Geometry, FR-003, test 2, width-bounds dataset |
| MAJ-002 (window.open re-navigates) | **FIXED** — in-memory handle registry; reuse = focus + context post, never a second `window.open`; stable window name only for NEW opens; presence fallback for lost handles | §8.3, §4 US-6, FR-009, test 14 |
| MAJ-003 (already-open ambiguity) | **FIXED** — SP-18: ONLY switch, scoped per workspace; manual tabs get the realistic degrade | §5, §8.3, US-6, FR-009, click-test rows 9–10 |
| MAJ-004 (history push/replace) | **FIXED** — SP-22: REPLACE everywhere; Back as today; store is source of truth, URL the projection | §4 US-7, §7 URL, §8.2, FR-010, test 15 |
| MAJ-005 (Browser Escape) | **FIXED** — SP-19: Browser Escape never closes; others keep Escape-to-close; bubble-phase/defaultPrevented rule stated | §4 US-1 AS-3, §6, §7 Accessibility, FR-012, test 13 |
| MAJ-006 (pop-out close clobbers) | **FIXED** — re-dock only into an EMPTY slot; never replaces a panel opened since; encoded as a new regression test | §6 re-dock rule, §7 State machine, test 8c, regression row 6 |
| MAJ-007 (tab ARIA + dropdown) | **FIXED** — strip is not a tablist; Chat keeps tab semantics; panel entries `aria-pressed` toggle buttons; dropdown mirrors semantics; mixed mode until wave 3. Round 2 (MIN-201) completed the visibility half: the pressed state is exercised at BOTH demo viewports via §9's viewport column | §4 US-5, §7 Accessibility, FR-007, test 11, §9 viewport column |
| MAJ-008 (demo = stories) | **FIXED** — SP-16: stories ARE the demo; design-system publication contract added | §9, §10, FR-016/FR-017, SC-001 |
| MAJ-009 (width write rules) | **FIXED** — persist only on drag release / keyboard change / reset; reset DELETES the key; window re-clamp is transient, never written | §4 US-3, §7 Persistence, FR-005, test 3, dataset row 9 |
| MAJ-010 (mail deep link no mailbox) | **FIXED** — SP-23: opens on "choose a mailbox" | §6, §8.2, FR-010, test 6, dataset row 8 |
| MAJ-011 (BDD/test traceability gaps) | **FIXED** — 12 missing scenarios added to §11; FR-015 remapped to its ONLY correct test (9b); matrix rebuilt with 8b/8c/9b | §11, §12, §13 matrix |
| MAJ-012 (extensibility testability) | **FIXED** — registered-ids-only validation (`tasks` in wave 1 behaves like `bogus`); SC-002 proof is a TEST-ONLY third registration mounting with zero shell edits; wave tags on FRs/USs | §4 preamble, §8.2, FR-010/FR-014, test 5, SC-002 |
| MIN-001 (sidebar term in ceiling) | **FIXED** — ceiling formula carries the pinned-sidebar term (256px ≥1024px breakpoint, 0 below) | §4 US-2, §7 Geometry, dataset basis |
| MIN-002 (focus-return rules) | **FIXED** — per-case rules: invoking toggle → header actions → chat input; missing invoker → chat input; load-time restore moves nothing | §4 US-9, §7 Accessibility, test 13 |
| MIN-003 (separator ARIA completeness) | **FIXED** — accessible name, `aria-controls`, `aria-valuetext`, dynamic `aria-valuemax` | §4 US-9, §7 Accessibility, FR-004, test 10 |
| MIN-004 (localStorage shared across accounts) | **FIXED** — SP-20 keys per USER + panel + workspace; stale other-user keys pruned on next write | §1, §7 Persistence, FR-005 |
| MIN-005 (stale width-vs-viewport rule) | **FIXED** — replaced by SP-17 floors/ceiling/overlay behaviour, one authoritative statement | §6, §7 Geometry |
| MIN-006 (drag perf/tearing) | **FIXED** — rAF-driven CSS variable during drag; layout commit on release; smoothness row in §9 | §7 Performance, §9 note |
| MIN-007 (stale section refs) | **FIXED** — header/Input line and cross-references updated to this file's § numbering | Header, §19 |
| MIN-008 (test 16 unmaintainable) | **FIXED** — automated/manual split; spec files assigned to a `ui-*` CI group; `scripts/e2e-shards.sh check` enforces | §12 test 16 |
| MIN-009 (shared-link session authorization unstated) | **FIXED** — assumption stated (per-user stream authorization via the ADR-044 cookie); named wave-1 regression test: a second account sees the visible denial, never the stream | §8.2 Browser bullet |
| OBS-001 (widthMemoryKey on PanelDefinition) | **FIXED** — removed; the shell derives the key from SP-20's rule; registration stays minimal | §8.1 |
| OBS-002 (contradicting already-open descriptions) | **FIXED** — SP-18 decided; exactly ONE behaviour description survives (§5), others cite it | §5, §8.3 |
| OBS-003 (no GitNexus impact run) | **NOTED** — GitNexus unavailable in this checkout; citations are file::symbol reads; the implementing lead reruns `impact` on every shell-touched symbol before build (§3.2) | §3.2 |

**Verdict context**: the round-1 verdict was BLOCK pending exactly this fix round;
round 2 re-grilled the corrected spec; round 2's fix round (final) is dispositioned
below. Status: **Approved** (2026-09-26).

---

## 19b. Review-finding dispositions (grill round 2 — `side-panel-shell-spec-spec-review-round2.md`)

All 27 round-2 findings applied; nothing deferred, nothing escalated to the founder —
every founder question became SP-25..SP-31, every other finding landed at the
review's own recommended default. Round-1 disposition text above reflects that
round's state and is superseded where round 2 changed it (notably SP-25's overlay
deletion). Status: **Approved** (2026-09-26).

### Founder decisions

| Finding | Disposition | Where |
|---|---|---|
| MAJ-204 (overlay serves a 40px band, hides the chat it claims to keep working) | **APPLIED (founder SP-25)** — overlay DELETED; phone takeover threshold covers every window below 680px (was <640px); width dataset recomputed | §4 US-2/US-8, §5, §6, §7 Geometry, §11, §12 dataset, FR-003/FR-011 |
| MAJ-207 (Browser reload/shared-link restore undefined) | **APPLIED (founder SP-28)** — Browser NEVER restored from reload or shared link; ALWAYS reopens via "Watch live"; no session id ever in a shareable link; excluded from `?panel=` restore | §4 US-7, §8.2, §11, §12 dataset, FR-019, SC-004 |
| MAJ-211 (sign-in drops the shared link — SC-004 unreachable) | **APPLIED (founder SP-27)** — auth gate preserves the original hash URL; sign-in returns to the opened link, workspace + panel restored; closes SC-004 | §4 US-7 AS-2, §8.2, FR-019, §14, SC-004 |
| MAJ-212 (workspace-switch re-target undefined for Browser; undeclared Library change) | **APPLIED (founder SP-29)** — per-panel rules: Browser anchored to its session; Library follows ONLY if opened scoped to the workspace being left; Tasks/Calendar/Mail follow | §4 US-3 AS-5, §6, §11, FR-020, test 20 |
| MAJ-213 (which entry points switch; stale scope matching) | **APPLIED (founder SP-30 + MAJ-213 current-scope default)** — EVERY entry point switches (sidebar Library, "Open library", "Watch live" included); matching by CURRENT scope; no context yank | §4 US-6, §5, §8.1, §8.3, §11, FR-009 |
| MAJ-210 (Storybook demo cannot execute half the click-test list) | **APPLIED (founder SP-31)** — gate split: wave-0 Storybook rows (layout + phone + keyboard, with a dev-only request-mocking add-on allowed) vs wave-1 real-app rows as exit criterion | §9, §10, §12 test 16, §15, SC-001/SC-009 |

### Stated defaults (review's own recommendation)

| Finding | Disposition | Where |
|---|---|---|
| MAJ-201 (Browser identity = session, not workspace) | **APPLIED** — identity keys per §8.1: Library/Tasks/Team/Calendar = `panelId × workspaceId`; Browser = `panelId × sessionId × agentId` (width bucket `app` always) | §8.1, §8.3, FR-009, test 14 |
| MAJ-202 (fixed window name still navigates the tab) | **APPLIED** — stable window names DROPPED; every open is a plain `_blank`; handle held by opening without `noopener` and severing `window.opener` immediately (Browser's existing pattern; Library drops `'noopener,noreferrer'`) | §8.3, FR-008 |
| MAJ-203 (geometry numbers contradict; default breaks the chat floor) | **APPLIED** — default = `clamp(0.45×row, 320px, min(720px, ceiling))`; every width number recomputed from SP-17-as-amended (16-row dataset) | §4 US-2, FR-003, §11, §12 dataset |
| MAJ-205 (URL-initiated transitions ungated) | **APPLIED** — router blocker (existing `useBlocker` pattern, `library.tsx::LibraryRoute`) gates URL-initiated navigations through `beforeLeave`; cancel leaves route/URL/panel unchanged | §4 US-7 AS-8, §8.2, §11, FR-010 |
| MAJ-206 (stale panel values on Back/Forward) | **APPLIED** — store wins: stale `panel` values in old history entries ignored and re-projected; the one exception is the SP-26 phone pushed step (Back closes the panel) | §4 US-7 AS-7, §8.2, §11, FR-010 |
| MAJ-208 (re-dock regresses the Dana fix; fires in every tab) | **APPLIED** — opener-only re-dock via the window handle; follows the pop-out's last workspace through the guard; third tab never re-docks; manual tabs trigger nothing | §6, §8.3, §11, FR-018, tests 8c/19, regression rows 7 |
| MAJ-209 (Expand failure path + gesture timing unspecified) | **APPLIED** — `window.open` runs synchronously in the user gesture (enabled by OBS-202's presence list); failure → error toast + docked panel stays open, EVERY panel | §8.3, §4 US-6 AS-5, FR-008, W12 |
| MIN-201 (round-1 MAJ-007 visibility half still open) | **APPLIED** — §9 gained a viewport column; shell stories render at 680×900 and 1280×800 variants | §9, §19 MAJ-007 note |
| MIN-202 (traceability holes: US-1 AS-4, US-3 AS-2, US-5 AS-2/3/6) | **APPLIED** — scenarios added for each; US-5 AS-2 also traced from the second-click scenario | §11 |
| MIN-203 (boundary rows missing; two geometry statements wrong) | **APPLIED** — 679/680 boundary rows added; drag-clamp (784 pinned), shrink (640), keyboard End (980) recomputed | §9, §11, §12 dataset |
| MIN-204 (width-storage details inconsistent/impossible) | **APPLIED** — key `panel-width:<username>:<panelId>:<workspaceId\|app>`; prune only own keys; storage failure = memory-only for the session, never an error | §7 Persistence, §8.1, FR-005 |
| MIN-205 (keyboard resize writes every keypress) | **APPLIED** — write + Browser handover fire on SETTLE (300ms after the last keystroke), not per keypress | §4 US-3 AS-5 note, FR-005, FR-015, test 9b |
| MIN-206 ("Open browser" needs a network call, runs before the guard) | **APPLIED** — guard runs BEFORE any `createSession`; no session created before the leave gate passes; §5 error flow states it | §5 error flows, §7 State machine |
| MIN-207 (presence/context messages trust any same-origin page) | **APPLIED** — incoming channel messages validated; no file paths/selections/session contents on BroadcastChannel; targeted postMessage to handles | §7 STRIDE note, §8.3, test 14 |
| MIN-208 (Escape during composition; focus-inside undefined) | **APPLIED** — Escape ignored while `event.isComposing`; "focus inside shell" = composed DOM path contains shell root or a registered portalled layer | §4 US-9, §7 Accessibility |
| MIN-209 (ADRs cited by number only, one ambiguously) | **APPLIED** — ADRs cited by title ("ADR-044 — Serve /preview/ on the main gateway listener", ADR-039 "User-initiated browsing and annotate", ADR-061 "Remove JPEG screencast fallback") | §8.2, FR-015 |
| MIN-210 (Decisions Log still describes window names) | **APPLIED** — dated mechanism-only amendment to SP-18's rationale in `spec-side-panel-shell.md` (handle registry replaced stable-window-name re-focus) | `spec-side-panel-shell.md` SP-18 rationale |
| MIN-211 (virtual-root Library reload changes scope + bucket) | **APPLIED** — stated as an accepted difference: reload restores workspace-scoped, in that workspace's width bucket; `&scope=root` rejected as URL surface growth | §8.2 Library bullet |
| OBS-201 (cross-tab rows CAN be automated) | **APPLIED** — W4/W5/W7 scripted as Playwright multi-tab where feasible, click-tested otherwise | §9 note, §12 test 16 |
| OBS-202 (150ms wait slows every open) | **APPLIED** — continuous presence list (announce on load/scope-change/pagehide); click resolves synchronously; also unblocks MAJ-209's sync open | §8.3, FR-009 |
| OBS-203 (GitNexus impact still not run) | **CARRIED FORWARD** — unchanged from §3.2/OBS-003: the implementing lead runs `impact` on `UiStore`, `AppShell`, `WorkspaceTabBar`, `LibraryPanel`, `BrowserLivePanel` before wave 1 starts (a spec-round obligation, not a spec defect) | §3.2 |

Nothing in §19b is "Deferred" — every finding landed now.
