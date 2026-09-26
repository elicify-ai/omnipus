# Feature Specification: Workspace side-panel shell (shared, resizable)

**Created**: 2026-09-26
**Status:** Draft — grill round 2 still to come (fix round 1 applied 2026-09-26)
**Input**: Founder interview output — `docs/internal/specs/spec-side-panel-shell.md` (request verbatim + Decisions Log SP-1..SP-24). Process order (founder, verbatim): "capture the requirements and update the design documents first, after another demo — let's do it properly." Requirements → this spec → clickable demo reviewed by the founder in a real browser (SP-10) → spec review rounds → build.

**Review-fix round 1 (2026-09-26)**: grill-spec round 1 returned BLOCK
(`side-panel-shell-spec-spec-review.md`: 1 critical, 12 major, 9 minor, 3 observation).
The founder answered all eight grill questions in a post-grill interview — decisions
SP-16..SP-24 in the Decisions Log, applied throughout this revision. Q1/Q2 in the old
§14 (width storage, bare browser deep link) are answered (SP-20, SP-21); **there are no
open founder questions**. Review-finding dispositions are in §19.

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
same interaction for every panel. **Width rules (SP-17, founder)**: the panel docks
side-by-side with the chat only while BOTH floors hold — the chat column stays
≥360px AND the panel stays ≥320px. The panel's maximum width is
`min(70% of the viewport, viewport − sidebar width − 360px)` (all measured on the root
flex row — MIN-001's basis; the sidebar term is the pinned sidebar's current width, 0
when it is not pinned, i.e. below its 1024px breakpoint,
`src/store/sidebar.ts::SIDEBAR_PIN_BREAKPOINT`). **When the window is too narrow to fit
both floors, the panel becomes an OVERLAY floating above the chat** (the chat keeps its
full row width and stays interactive underneath) instead of squeezing the chat below
360px. Minimum 320px; default = today's width (45% of the row clamped to 320–720px);
double-click on the border resets. Keyboard accessible: focusable separator, arrow keys,
Home/End. Below 640px there is no drag: full-screen takeover, as today (US-8).

**Why this priority**: the feature's namesake ask; the demo gate (SP-10/SP-16) shows it.

**Independent test**: render the shell, drag the border, assert width changes within
bounds and the chat column never drops below 360px; narrow the window until the floors
cannot both fit and assert the panel overlays instead of squeezing.

**Acceptance scenarios**:

1. **Given** an open panel, **When** the operator drags the border left/right, **Then**
   the panel width follows the pointer live and the chat column width changes by the
   same amount (a real side-by-side split).
2. **Given** a drag that would exceed the bounds, **When** the pointer passes 320px or
   the SP-17 ceiling `min(70% of row, row − sidebar − 360px)`, **Then** the width stops
   at that bound (hard clamp, no rubber-banding, chat never below 360px).
3. **Given** an open panel, **When** the operator double-clicks the border, **Then**
   width resets to the default (45% clamped 320–720px).
4. **Given** a window resize while a panel is open, **When** the window shrinks, **Then**
   the panel re-clamps to the new ceiling while both floors still fit; **when the floors
   no longer both fit (still ≥640px), the panel switches to overlay** — the chat column
   is never squeezed below 360px on a ≥640px viewport.
5. **Given** the Browser panel being resized, **When** the width settles, **Then** the
   existing remote-viewport resize handover runs (its visible "Resizing browser… / input
   will resume" status shows; a failure is visible with Retry — never a silent
   stall — reusing `BrowserLiveView`'s existing handover UI).

### US-3 — Width memory per user × panel × workspace (SP-13, SP-3, SP-20) — P0 [wave 1]

An operator who widens Tasks in one workspace and narrows Library in another wants each
choice remembered where they made it. The remembered width is keyed **per USER + panel
+ workspace** (SP-20: two accounts on one browser never share widths — MIN-004
resolved). Storage is browser-local localStorage (SP-20), in the pattern of the SPA's
existing persisted stores. Panels opened with no workspace context (sidebar Library at
the virtual root, Browser from a global screen) use a single `app` bucket.

**Write rules (MAJ-009)**: the stored value is written ONLY on a user choice — drag
release, keyboard adjustment, or reset. A reset **deletes** the stored value (the
default is re-derived at read time, never stored — storing a default in px would freeze
it to one window size). A window-driven re-clamp (window shrink, zoom change) is
**transient**: it changes the applied width for as long as the window is that size and
MUST NOT overwrite the stored value; the clamp is re-applied at read time.

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
   workspace's context loads, **Then** the panel stays open, re-targets to the new
   workspace's content, and re-clamps to the new workspace's remembered width — **after
   passing the unsaved-edit guard when the outgoing panel is the Library** (FR-013).
6. **Given** a window shrink re-clamped an open panel from 950px to 700px, **When** the
   window returns to its previous size, **Then** the panel returns to 950px and the
   stored value was never overwritten (transient re-clamp, MAJ-009).

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
independent toggle states). The compact view-switcher dropdown (container <1152px,
`WorkspaceTabContainer.tsx`'s `@container`) carries the same toggle semantics: panel
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
once (SP-18)**: clicking a panel's toggle when that panel's full-page tab is ALREADY
open means SWITCH to that tab — never "open here too", never a duplicate. "Already
open" is scoped PER WORKSPACE: a tab counts as already open only when it shows that
panel for the SAME workspace; the same panel for a DIFFERENT workspace opens normally
here. The honest browser constraint stands (§8.3): only tabs Omnipus opened itself can
be reliably re-focused; a manually-opened tab gets the realistic degrade — detected via
BroadcastChannel presence, no duplicate opened, no docked panel opened here either, and
a visible "already open in another tab — switch" affordance whose switch action is
best-effort focus (`window.focus` is not guaranteed — the affordance is the
guarantee). The reuse mechanism is an **in-memory handle registry** (§8.3, MAJ-002) —
never a re-navigating `window.open(url, name)`.

**Why this priority**: cross-window behaviour, fully specified by founder choices
(SP-12 recommended option chosen; SP-18 answered the round-1 grill).

**Independent test**: open Library, Expand; assert new tab opens and the docked panel
closed. Re-click Library's toggle: with the app-opened tab present it is focused and no
docked panel opens here; with a manually-opened tab present the affordance shows; no
path duplicates a tab.

**Acceptance scenarios**:

1. **Given** any panel open, **When** the operator clicks Expand, **Then** the panel's
   expand-target route opens in a new browser tab and the docked panel closes in the
   original tab (chat regains the width).
2. **Given** the app itself opened a panel's full-page tab earlier and still holds its
   window handle (§8.3), **When** the operator triggers Expand or clicks the panel's
   toggle again (same workspace), **Then** the existing tab is focused via the handle —
   no second tab, no navigation or reload of that tab, and no docked panel opens here.
3. **Given** a panel's full-page tab exists that the USER opened manually (no window
   handle; e.g. via browser chrome or a shared link), **When** the operator clicks that
   panel's toggle, **Then** BroadcastChannel presence answers (§8.3), the docked panel
   does NOT open here, no duplicate opens, and a visible "already open in another tab —
   switch" affordance offers the best-effort switch.
4. **Given** the manually-opened tab is then closed, **When** the operator clicks the
   toggle again, **Then** the panel opens normally (presence detection cleared — no
   stale "already open" state).
5. **Given** a Library full-page tab open for workspace A, **When** the operator clicks
   the Library toggle in a tab showing workspace B, **Then** that is NOT "already open"
   (per-workspace scoping, SP-18): Library opens docked here for workspace B normally.

### US-7 — URL-addressable panel state (SP-14, SP-21, SP-22, SP-23) — P1 [wave 1]

The open panel is part of the page address (`…/chat?panel=team`); reload and shared
links restore it. Hash routing applies (the router's search lives in the `#/` fragment
— `src/components/library/LibraryPanel.tsx::LibraryPanel.handlePopOut` comment;
`src/routes/_app/-library.deep-link.test.tsx`). **Panel toggles are not pages
(SP-22)**: opening, closing and switching a panel uses history REPLACE, never push —
the browser Back button behaves exactly as it does today. Valid `panel` values are the
REGISTERED panel ids (MAJ-012): an unregistered-but-future id (`tasks` in wave 1) is
treated exactly like an unknown id — dropped with a URL replace.

**Why this priority**: founder-chosen recommended option (SP-14); makes the one-panel
state shareable and reload-stable.

**Independent test**: open a panel, copy the URL, reload in a fresh tab; assert the
same panel restores; press Back after toggling panels and assert history is unchanged
from today's behaviour.

**Acceptance scenarios**:

1. **Given** any panel open on a workspace Chat route, **When** the operator reloads,
   **Then** the same panel restores at its default view with its width.
2. **Given** a shared link carrying `?panel=calendar` (wave 3), **When** it is opened,
   **Then** the Calendar panel opens over Chat without any further click.
3. **Given** a link with no `panel` param, **When** opened, **Then** no panel opens
   (today's behaviour for links).
4. **Given** a link with an unknown or UNREGISTERED panel id, **When** opened, **Then**
   the param is dropped (URL replaced) and no panel opens — the chat still renders; the
   failure is never silent-invisible (the URL visibly changes).
5. **Given** `?panel=browser` without session/agent context, **When** opened, **Then**
   the param is dropped and just the chat renders (SP-21, founder: never trigger a paid
   agent session from a pasted link).
6. **Given** `?panel=mail` with no agent/mailbox selected, **When** opened, **Then** the
   Mail panel opens on its "choose a mailbox" state (SP-23 — Mail starts nothing
   costly, unlike Browser).
7. **Given** the operator opens, switches and closes panels, **When** they press the
   browser Back button afterwards, **Then** navigation history is exactly as if the
   panels had never been toggled (SP-22 — replace, never push).

### US-8 — Phone behaviour (SP-3, SP-4) — P0 [wave 1]

Below 640px the panel takes over the full screen and the chat region is `inert` —
exactly today's behaviour, generalized to every panel.

**Why this priority**: preserves an existing accessibility behaviour; the inert
treatment must not be lost for new panels.

**Independent test**: at <640px open a panel; assert full-width takeover and inert chat.

**Acceptance scenarios**:

1. **Given** a <640px viewport, **When** a panel opens, **Then** it occupies the full
   content width, the chat region is `inert` (not focusable, not clickable), and no
   resize handle is offered.
2. **Given** the takeover state, **When** the operator closes the panel, **Then** the
   chat region becomes interactive again and the panel is gone.
3. **Given** a viewport crossing 640px while a panel is open, **When** the layout
   re-evaluates, **Then** the panel docks at its remembered width with a resize
   handle (no stuck takeover state).

### US-9 — Keyboard and screen-reader access (SP-3, SP-4) — P0 [wave 1]

The resize border is a focusable separator with complete assistive state; opening and
closing manages focus; panels are labelled landmarks. **Focus-return rules, per case
(MIN-002)**: toggling a panel closed returns focus to the toggle that invoked it;
Close/Expand in the shell header returns focus to the chat input (matching the
Browser's existing behaviour — `BrowserLivePanel.tsx::handlePopOut` focuses the chat
input today); when the invoking control no longer exists (a collapsed dropdown item, a
relaid-out strip), focus falls back to the chat input; a deep-link panel restored on
page load moves NO focus (never steal focus on load).

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
- When a panel's toggle is clicked and its full-page tab for the SAME workspace is
  already open, the app SWITCHES to that tab (handle-focus when it holds the handle;
  affordance + best-effort switch for a manual tab) — it never opens a duplicate and
  never also opens the docked panel here (SP-18, stated once).
- When the panel's width is dragged, changed by keyboard, or reset (double-click), the
  new width is remembered per user + panel + workspace (SP-13, SP-20) — written only on
  user choice, never on a window-driven re-clamp (MAJ-009).
- When a URL carries `?panel=<registered id>`, that panel opens; reload and shared
  links restore it; toggling panels never adds history entries (SP-14, SP-22).

Error flows:
- When ANY path closes or replaces the Library panel with unsaved edits, the
  discard-confirmation runs; cancelling leaves the panel open (FR-013, CRIT-001).
- When an unknown or unregistered panel id arrives via URL, the param is dropped and no
  panel opens.
- When a bare `?panel=browser` link arrives with no session/agent, the param is dropped
  and no session is created (SP-21).
- When `?panel=mail` arrives with no mailbox selected, the Mail panel opens on its
  "choose a mailbox" state (SP-23).
- When the Browser panel cannot open its pop-out, today's visible toast + the panel
  staying open is preserved.
- When WebRTC video fails inside the Browser panel, the visible error + Retry
  (ADR-061) is untouched by the shell.

Boundary conditions:
- When width hits 320px or the SP-17 ceiling `min(70% row, row − sidebar − 360px)`, it
  clamps (drag, keyboard, and restore alike).
- When the floors (chat ≥360px, panel ≥320px) no longer both fit, the panel overlays
  the chat instead of squeezing it (SP-17).
- When the window shrinks or zooms, the open panel re-clamps TRANSIENTLY — the stored
  width is untouched and returns when the window does (MAJ-009).
- When the viewport is <640px, the panel takes over full screen, the chat is `inert`,
  and resizing is disabled.
- When Escape is pressed inside the Browser panel, it keeps its "stop driving" meaning
  and the panel stays open (SP-19); every other panel closes on Escape.
- When the browser tab is reloaded, the URL restores the panel; the remembered width
  persists per user + panel + workspace.

---

## 6. Edge cases

- **Panel open + workspace switch** (US-3.5): the panel stays open, re-targets to the
  new workspace, and takes the new workspace's remembered width — after the leave guard
  when the outgoing panel is the Library (FR-013).
- **Browser panel deep link without session/agent** (US-7.5, SP-21): the param drops,
  just the chat renders, no session is created.
- **Mail deep link with no mailbox** (US-7.6, SP-23): the Mail panel opens on its
  "choose a mailbox" state.
- **Escape while a modal is above the panel**: modal closes first (US-1.3).
- **Escape inside the Browser panel** (SP-19): never closes the panel — it releases
  the wheel (stops driving the remote browser); the panel closes only via its Close
  control.
- **Escape during Library unsaved-edits**: the confirmation dialog takes Escape
  (topmost layer); a cancel keeps the panel open.
- **Double-click reset while below 640px**: no border exists in takeover — no-op.
- **Two entry points racing** (e.g. "Watch live" in chat while the Library toggle is
  clicked in the same interaction window): last open wins; one panel at a time holds;
  the leave guard still runs (a dirty Library cancels the race loser).
- **Browser pop-out close re-docks only into an EMPTY panel slot** (MAJ-006): a pop-out
  close re-docks its panel only if NO panel is open in the source tab; if a different
  panel has since been opened there, the re-dock is a NO-OP — it never clobbers or
  replaces the panel the operator is using (CRIT-001 safe: no unmount, no guard needed
  on this path).
- **Browser panel with an owned pop-out** (today's subscribe logic): clicking another
  panel's toggle must not strand the owned pop-out — the existing
  "close docked panel, focus owned window" reaction (`BrowserLivePanel.tsx`) is
  preserved for the Browser's own toggle; opening a DIFFERENT panel while an owned
  Browser pop-out exists closes the docked Browser panel state and leaves the pop-out
  owned and running (its re-dock reaction on pop-out close fires per the no-clobber
  rule above).
- **Reload with `?panel=browser` and a valid session**: the Browser panel restores;
  a session that no longer exists shows the view's existing error state, not a silent
  blank (ADR-061 posture).
- **Window too narrow for both floors** (SP-17): at ≥640px with chat floor 360px and
  panel floor 320px, a window narrower than `sidebar + 360 + 320` puts the panel in
  overlay mode over the interactive chat — the old "70% ≥ 448px always fits" reasoning
  is gone (MIN-005 replaced by the SP-17 floors).

---

## 7. Explicit non-behaviors & safeguards

### Qualitative prohibitions

- The system must not reintroduce the retired Sheet/overlay panel MODE — the retired
  surface is the slide-out Sheet with its pin toggle as the panel's hosting mode
  (retired 2026-07-16 by operator direction; `src/store/ui.ts::UiStore`,
  `LibraryPanel.tsx` module docs). SP-17's narrow-window OVERLAY degradation is a
  DIFFERENT thing: a docking fallback for windows too narrow for both floors, with no
  Sheet component, no pin toggle, and docking restored as soon as the floors fit again.
  It is in scope, and it is not the retired mode.
- The shell must not remove any capability inventoried in §2.2/§2.3; per-panel buttons
  may move to the shell header (capability preserved), never disappear.
- The shell must not know panel internals: it renders content supplied per panel and
  does not reach into the Library explorer or the browser view (each panel's pop-out
  handover semantics stay content-level).
- The system must not silently drop a failed Browser pop-out or a failed WebRTC stream
  (visible toast / visible error + Retry today; stays that way — ADR-061).
- The system must not open a second tab when one already exists for a panel (SP-18);
  worst case is a visible "already open" affordance, never a silent duplicate.
- The system must not let the panel exceed the SP-17 ceiling
  `min(70% of row, row − sidebar − 360px)` — the chat stays usable (SP-3's purpose,
  SP-17's formula).
- The system must not persist the open/closed panel state server-side or reopen
  panels on reload except through the URL param (SP-14's mechanism is the URL; there is
  no panel session on the gateway).
- Settings must never become a panel (SP-6); the workspace-name entry keeps navigating
  to the settings page.
- The system must not gate panel open/close on any network call — all state is local
  (§1: no backend).
- No path may silently discard unsaved Library edits (CRIT-001): every close/replace
  path is gated (FR-013).

### Machine-verifiable constraints

**Geometry (SP-17)**:
- Docked layout requires BOTH floors: chat column ≥360px AND panel ≥320px. All widths
  are measured on the root flex row; percentages use the row width as their single
  basis (MIN-001's basis — today's `sm:w-[45%]` is 45% of the row).
- Panel maximum width MUST equal `min(0.70 × row, row − sidebar − 360px)` (sidebar term
  = the pinned sidebar's current width; 0 when unpinned, below its 1024px breakpoint).
- When `row − sidebar < 360 + 320` (floors cannot both fit) at ≥640px, the panel MUST
  switch to OVERLAY: it floats above the chat at its clamped width, the chat keeps the
  full row and stays interactive (no inert), and the resize handle keeps working.
- Default width MUST be 45% of the row clamped to [320px, 720px].
- Keyboard resize step MUST be 16px per keypress; Home = 320px; End = the SP-17
  ceiling.
- Viewport <640px MUST take over full width and MUST NOT render a resize handle.

**Performance (MIN-006)**:
- Live drag MUST update the width via a CSS variable driven by requestAnimationFrame;
  layout-commit happens on drag release. No full React re-render per pointer move.

**State machine**:
- At most one panel open at any instant (SP-7) — every open path (tab toggle, sidebar,
  ChatControls, "Watch live", deep link, email draft link) MUST route through the same
  single-panel state.
- **Every transition that closes or replaces the open panel is gated by the outgoing
  panel's `beforeLeave()` (CRIT-001 fix, FR-013)**: tab toggle close, open-other (any
  entry point), deep-link replace, workspace-switch re-target, header Close, Expand.
  The transition proceeds only when `beforeLeave()` resolves true; a false cancels it
  (no state change, URL unchanged, panel stays). Pop-out re-dock fires only when no
  panel is open (MAJ-006), so it never needs the gate.
- Clicking the open panel's toggle MUST close it (SP-11) — through the gate.
- Expand MUST close the docked panel in the source tab (SP-12) — through the gate.

**URL**:
- The open panel MUST be encoded as search param `panel` on the workspace Chat route,
  whose value MUST be a REGISTERED panel id (MAJ-012); any other value — unknown or
  unregistered-but-future — MUST be dropped with a URL replace.
- Under the app's hash routing the param lives under `#/` (e.g.
  `/#/workspaces/{id}/chat?panel=team`).
- Panel toggles use history REPLACE, never push (SP-22): open, switch and close are
  `router.replace`-class operations; Back/Forward behave exactly as today.

**Persistence (SP-20, MAJ-009)**:
- Remembered width key MUST be **user id × panel id × workspace id** (workspace-less
  contexts use the `app` bucket), in browser-local storage (SP-20 — one namespaced
  JSON entry in the existing persisted-store pattern; deleted workspaces' entries are
  ignored on read and pruned on the next write, so the key space cannot grow without
  bound — MIN-004).
- Stored value is px, written ONLY on a user choice: drag release, keyboard adjustment,
  or reset. Reset DELETES the key (default re-derived at read time; a default is never
  stored in px). A window-driven re-clamp is transient and MUST NOT write.
- Storage location: browser-local localStorage (SP-20, decided). A future server-side
  home would be a contract-first change — not in this spec.

**Accessibility**:
- The separator MUST be focusable, role `separator`, `aria-orientation="vertical"`,
  with an accessible name ("Resize <Panel> panel"), `aria-controls` referencing the
  panel element, `aria-valuetext` ("<n> pixels wide"), and
  `aria-valuenow`/`aria-valuemin`/`aria-valuemax` in px (`aria-valuemax` updates on
  window resize — MIN-003).
- Escape MUST close the topmost layer (modal above panel, else panel) — EXCEPT the
  Browser panel, where Escape NEVER closes the panel (SP-19: Escape releases the
  wheel, per `BrowserLiveView.tsx::handleKeyDown`, WCAG 2.1.2; only Close closes it).
  The shell's Escape listener is bubble-phase on the shell root, fires only when focus
  is inside the shell, and only when `event.defaultPrevented` is false — content that
  consumes Escape (Radix menus, IME, the chat composer) calls `preventDefault()`.
- On open (by user action), focus MUST move into the panel; on close, focus MUST follow
  the per-case rules in US-9 (invoking toggle, else chat input; never steals focus on a
  load-time deep-link restore).

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
- `beforeLeave` is supplied ONLY by panels with an unsaved-edit risk (Library today:
  its existing `confirmDiscardLibraryEdits`); panels without such risk omit it, and
  transitions replace them freely. The shell awaits it BEFORE touching the store, URL
  or content (the guard must run while the outgoing panel is still mounted — its dialog
  is hosted inside the mounted content, `unsavedGuard.ts`).
- **The shell derives the width-memory key itself** — `panel-width.<user>.<id>.<ws|app>`
  — from `id × context.workspaceId × signed-in user` (SP-20); panels do not supply it
  (OBS-001: the per-panel `widthMemoryKey` was unnecessary indirection, removed).
- Panels register through a single registry; adding Mail (wave 2) and
  Tasks/Team/Calendar (wave 3) MUST require no shell change beyond a new
  `PanelDefinition` entry (SP-4's test: "each panel supplies only its content and its
  expand target").
- The shell header renders title + Expand + Close; Expand delegates to the panel's
  own pop-out behaviour (selection-carrying for Library, ownership handover for
  Browser — §2.3 kept differences).

### 8.2 URL and deep-link contract (SP-14, SP-21, SP-22, SP-23)

- Param `panel` on the workspace Chat route's search (under `#/`). Canonical example:
  `/#/workspaces/{workspaceId}/chat?panel=team`. **The workspace Chat route is the only
  route whose search schema declares `panel`** (today it declares none — verified);
  the Chat route's own `{workspaceId}` param scopes every panel's workspace context,
  which is why no extra workspace param is needed. A `panel` param arriving on any
  other route is dropped (URL replace).
- **Source of truth (MAJ-004)**: the store slice (`activePanel`) is the runtime source
  of truth; the URL `panel` param is its addressable projection — written with history
  REPLACE on every change and read on load (reload/deep link). One model, no dual
  authority. Toggles are replace, never push (SP-22) — Back behaves exactly as today.
- **Cross-route navigation (MAJ-004)**: panels are app-global (§17) — navigating to a
  non-chat route (Agents, Settings) keeps the open panel open in the store but the URL
  stops carrying `panel` (only the Chat route declares it); navigating back to a
  workspace Chat route re-projects the state into the URL (replace). A reload on a
  non-chat route restores no panel (the URL is the restore mechanism and it does not
  carry `panel` there).
- Valid `panel` values are the REGISTERED panel ids (MAJ-012); `tasks` in wave 1 is
  treated exactly like `bogus` — dropped with a URL replace, no panel.
- Panel context that cannot live in the URL:
  - **Browser (SP-21, decided)**: a bare `panel=browser` (no `session`/`agent` in the
    link and none in session state) NEVER auto-starts a session — never trigger a paid
    agent session from a pasted link. The param is dropped (URL replaced) and just the
    chat renders. `panel=browser` WITH session/agent context restores normally. The
    `/browser-live` route's existing visible "Missing session or agent" refusal
    (`BrowserLiveRoute`) is unchanged.
    **Shared-link authorization (MIN-009 disposition)**: a shared
    `panel=browser&session=…&agent=…` link relies on the gateway's existing per-user
    authorization of the live stream (the ADR-044 session cookie) — stated here as an
    ASSUMPTION, not a verified property. Required wave-1 regression test (test-16
    scope): a second account following the link sees the panel's visible denial
    surface, never the other user's stream.
  - **Mail (SP-23, decided)**: `panel=mail` with no agent/mailbox selected opens the
    Mail panel on its "choose a mailbox" state (Mail starts nothing costly, unlike
    Browser); a link MAY carry `&agent=…` to land directly on that mailbox.
  - **Library (MAJ-010 honesty)**: `panel=library` restores the Library scoped to the
    Chat route's `{workspaceId}` at its DEFAULT view (the workspace's root). It does
    NOT restore a path/folder selection — selection-carrying links remain the existing
    full-page `/library?workspace=…&path=…&folder=…` form, whose `folder` is a one-time
    initial seed (`src/routes/_app/library.tsx::librarySearchSchema`). No new context
    params are introduced.
- The `media` redirect stub
  (`src/routes/_app/workspaces.$workspaceId.media.tsx::WorkspaceMediaRedirect`)
  retargets to the deep-link form: navigate to
  `/workspaces/{id}/chat?panel=library` (replace) instead of store-call + bare redirect
  — bookmarked `media` links keep working.
- Full-page routes (`/library`, `/browser-live`, `/workspaces/{id}/{board,team,calendar}`)
  keep working exactly as today: a shared full-page link lands on the full page (US-5.4).

### 8.3 Already-open-tab contract (SP-18)

Verified web-platform facts this contract rests on (MDN Window/open, Window/focus):

1. `window.open(url, name)` on an existing named window **navigates** that window to
   `url` — it does not merely focus it. A second Expand under the old "stable window
   name reuse" design would have navigated the existing Browser tab to `about:blank`
   and torn down a live viewer (MAJ-002). **The named-window reuse mechanism is
   therefore REPLACED by an in-memory handle registry** (below). A stable per-
   workspace+panel window name is still used for NEW opens —
   `omnipus-panel-<panelId>-<workspaceId|app>` — as an identity label and collision
   guard, never as a reuse mechanism.
2. With `noopener`, a non-empty custom name is treated like `_blank` — no reuse, and
   `open()` returns null. The Library's current pop-out uses
   `'noopener,noreferrer'` (`LibraryPanel.tsx::handlePopOut`), so the named open
   REQUIRES dropping `noopener` and severing `window.opener` after the open instead —
   exactly the Browser's existing pattern (`BrowserLivePanel.tsx::handlePopOut`:
   `popup.opener = null`).
3. `window.focus()` "may fail due to user settings and the window isn't guaranteed to
   be frontmost" (MDN Window/focus) — a background tab focusing itself is best-effort.

**The handle registry (MAJ-002 fix)**:

- On each Expand (new tab), the app stores the returned `Window` handle in a
  module-level registry keyed `panelId × workspaceId|app` — the Browser's existing
  `ownedPopout` is the precedent (`BrowserLivePanel.tsx`).
- On a subsequent Expand or toggle click for the same `panelId × workspace` (SP-18's
  per-workspace scoping), the app checks the registry: **if the handle exists and
  `!handle.closed`, it calls `handle.focus()` and posts the current context (e.g. a new
  Library selection) over the existing handoff BroadcastChannel — it NEVER re-calls
  `window.open` for that tab** (no re-navigation, no blanking). A stale handle
  (`handle.closed`) is dropped.
- The docked panel does NOT open here in the reuse path (SP-18: only switch).
- A handle is lost when the source tab reloads; in that case the flow falls back to
  BroadcastChannel presence detection (below), exactly as for a manually-opened tab.

**Manually-opened tabs** (user hit the URL directly): no window handle exists. The app
detects presence over a BroadcastChannel (extending the
`libraryHandoff`/`browserLiveHandoff` precedents), **keyed `panelId × workspaceId|app`**
— a Team full page for workspace A never blocks Team for workspace B. A toggle click
pings the channel and waits a bounded 150ms for a presence reply:

- **Reply received (same workspace)** → SP-18 applies: no duplicate, no docked panel
  opens here; a visible "already open in another tab — switch" affordance offers the
  best-effort switch (the affordance is the guarantee — programmatic focus is not).
  Multiple replies (two manual tabs) → the affordance targets the most recent replier;
  nothing duplicates.
- **No reply within 150ms** → the panel opens docked here normally (and stores its
  handle if Expand was the trigger).

Presence state MUST clear when the tab closes (channel disconnect / pagehide) so a
later toggle reopens normally (US-6.4).

---

## 9. Clickable DEMO requirements (SP-10, SP-16) — gate BEFORE any build

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

**Stories inventory (the demo scope — SP-15/SP-16)**:

| Story set | Wave | Sample content |
|---|---|---|
| Shell (empty, docking, overlay, phone, guard) | 0–1 | Generic sample panel + Library/Browser content |
| `library` | 0–1 | Real explorer on fixture data (no gateway) |
| `browser` | 0–1 | Static placeholder view (no live WebRTC required) |
| `mail` | 2 (stand-in 0) | Real content wave 2; wave-0 stand-in: static message list + reader |
| `tasks` | 3 (stand-in 0) | Real content wave 3; stand-in: narrow list/kanban |
| `team` | 3 (stand-in 0) | Real content wave 3; stand-in: narrow agent-graph |
| `calendar` | 3 (stand-in 0) | Real content wave 3; stand-in: day/week |

**What the demo must demonstrate** (each maps to a US):

1. Resize by dragging, with the 320px floor and the SP-17 ceiling visibly enforced;
   chat column absorbing the remainder; overlay kicks in when the floors stop fitting
   (US-2).
2. Double-click reset; width memory per user + panel + workspace across close/reopen
   (US-3).
3. One-at-a-time switching through the tab-strip toggles, pressed state + second-click
   close (US-4, US-5).
4. Expand → new tab + source panel closes; re-click switches to the open tab (handle
   focus, or the manual-tab affordance) — never a duplicate (US-6, SP-18).
5. Deep link: copy URL with `?panel=…`, reopen in a fresh tab, panel restores; unknown
   id dropped; bare `?panel=browser` drops without a session (US-7, SP-21).
6. Phone width: takeover + inert chat, no resize handle (US-8).
7. Keyboard walkthrough: Tab to separator, arrows, Home/End, Escape (except Browser,
   SP-19), focus return (US-9).

**Real-browser click-test list** (team-lead executes against the static Storybook
build and records evidence before the founder review; every step is a pass/fail with a
screenshot):

| # | Action | Expected |
|---|---|---|
| 1 | Click Library tab entry | Panel docks beside chat; entry pressed; URL has `?panel=library` |
| 2 | Drag border to far left | Stops at 320px; chat still visible and interactive |
| 3 | Drag border to far right, then narrow the window until the floors stop fitting | Stops at the SP-17 ceiling `min(70% row, row − sidebar − 360px)`; below that, panel OVERLAYS the interactive chat (chat ≥360px) |
| 4 | Double-click border | Width returns to default; persists after close/reopen |
| 5 | Set width in workspace A; switch to workspace B; open same panel | B's width (or default) applies, not A's |
| 6 | Click Tasks entry while Library open | Library closes (through the leave guard); Tasks docks; pressed state moves; URL `?panel=tasks` |
| 7 | Click Tasks entry again | Panel closes; pressed state clears; `panel` param gone |
| 8 | Click Expand on Library | New tab opens full-page Library; original tab's panel closed; chat full width |
| 9 | Click Library toggle again with the app-opened tab still open | Existing tab is focused (handle registry); no duplicate tab; no docked panel opens here |
| 10 | Open full-page Library URL manually in a second tab; click Library toggle in tab 1 | No duplicate; no docked panel opens here; "already open — switch" affordance shown |
| 11 | Close the manual tab; click the toggle | Panel opens normally |
| 12 | Copy URL with `?panel=calendar`; paste into fresh tab | Calendar panel restores |
| 13 | URL with `?panel=bogus` (and, in wave 1, `?panel=tasks` unregistered) | Param dropped; chat renders; no panel |
| 14 | Set viewport <640px; open Mail | Full-screen takeover; chat inert; no resize handle |
| 15 | Keyboard: Tab to border, arrows/Home/End/Escape | Steps work; focus returns per US-9's per-case rules; Escape on the BROWSER panel releases driving and does NOT close it (SP-19) |
| 16 | Open Browser panel, then click Calendar entry | Browser closes (one-at-a-time); no stranded state |

A demo failing any step is fixed and re-tested before the founder sees it (SP-10's
"team-lead's own browser check first"). Drag smoothness (MIN-006) is observed during
rows 2–3: live width updates ride a CSS variable via requestAnimationFrame — no visible
jank on a long chat transcript.

---

## 10. Rollout sequence (SP-8) with narrow layouts (SP-6)

**Wave 0 — this spec + the Storybook demo (SP-10, SP-16).** Spec review rounds; the
static Storybook demo build (shell + Library/Browser stories + sample stand-ins for the
later-wave panels) click-tested and founder-approved in a real browser. No production
build starts before that approval.

**Wave 1 — Shell + Library + Browser (P0) [wave 1 FRs].** The shared shell lands
hosting Library and Browser: resize + width memory (SP-2/SP-3/SP-13/SP-17/SP-20),
one-at-a-time WITH the every-path leave guard (SP-7, FR-013/CRIT-001), Escape with the
Browser exception (SP-19), header actions (§2.2/§2.3 buttons move to the shell header,
capabilities preserved), tab-strip Library toggle with the new ARIA model (SP-11,
MAJ-007), deep links with replace history (SP-14, SP-22), already-open-tab behaviour
via the handle registry + presence (SP-18, §8.3), phone takeover generalized. Entry
points (sidebar, ChatControls, "Watch live") keep working through the new store slice.
Stories: shell + Library + Browser sets (SP-24).

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
- **When** the Library panel is open on a ≥640px viewport
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
- **Given** an open panel on a 1400px-wide window
- **When** the border is dragged beyond 70% of the window (980px)
- **Then** the width stops at 980px
- **When** the border is dragged below 320px
- **Then** the width stops at 320px

#### Scenario: Double-click resets to default
**Traces to**: US-2, AS-3 · **Category**: Happy Path
- **Given** a panel widened to 900px
- **When** the border is double-clicked
- **Then** width becomes 45% of the content area clamped to [320, 720] px

#### Scenario: Window shrink re-clamps the open panel
**Traces to**: US-2, AS-4 · **Category**: Edge Case
- **Given** a panel at 900px on a 1400px window
- **When** the window shrinks to 1000px
- **Then** the panel re-clamps to 700px (70%)

#### Scenario Outline: Width memory per panel × workspace
**Traces to**: US-3, AS-1/2 · **Category**: Happy Path
- **Given** a remembered width for <panel> in <workspace>
- **When** <panel> opens in <workspace>
- **Then** the remembered width applies clamped to bounds

**Examples**:

| panel | workspace | remembered | expected |
|---|---|---|---|
| library | A | 640px | 640px |
| tasks | A | 950px | 950px (≤70%) |
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
**Traces to**: US-4, AS-2 · **Category**: Alternate Path
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
**Traces to**: US-8, AS-3 · **Category**: Edge Case
- **Given** a panel open in phone takeover
- **When** the viewport grows past 640px
- **Then** the panel docks at its remembered width with a resize handle

#### Scenario: Keyboard resize full walkthrough
**Traces to**: US-9, AS-1/2/3 · **Category**: Happy Path
- **Given** an open panel and a keyboard-only operator
- **When** the separator is focused via Tab
- **Then** it exposes role separator with vertical orientation and min/current/max values
- **When** Right-arrow is pressed four times
- **Then** width increases by 64px total (16px steps, clamped)
- **When** Home then End are pressed
- **Then** width is 320px then the 70% bound
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

#### Scenario: Narrow window puts the panel in overlay mode
**Traces to**: US-2, AS-4 · **Category**: Edge Case
- **Given** a window where `row − sidebar < 360 + 320` (floors cannot both fit), viewport ≥640px
- **When** a panel opens
- **Then** the panel overlays the interactive chat (chat keeps the full row, ≥360px, not inert)
- **And** the resize handle still works within the overlay ceiling

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
- **When** the window shrinks (panel re-clamps to 700px) and then returns
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
- **Given** a container <1152px (compact dropdown)
- **When** the operator toggles the Library entry in the dropdown
- **Then** the panel opens/closes exactly as on the full strip, the entry shows the same pressed state, and the trigger label stays "Chat"

#### Scenario: Mixed-mode strip renders links and toggles together
**Traces to**: US-5 (mixed mode, MAJ-012) · **Category**: Alternate Path
- **Given** wave 1 (only library + browser registered)
- **When** the strip renders
- **Then** Library shows as a toggle with `aria-pressed` while Tasks/Team/Calendar remain navigation links with no pressed state

#### Scenario: Reload restores the open panel
**Traces to**: US-7, AS-1 · **Category**: Happy Path
- **Given** any panel open on a workspace Chat route
- **When** the operator reloads
- **Then** the same panel restores at its default view with its width

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
- **Given** a panel open at <640px (chat inert)
- **When** the panel is closed
- **Then** the chat region becomes interactive again and the panel is gone

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

#### Scenario: Presence is scoped per panel × workspace
**Traces to**: US-6, AS-5 · **Category**: Alternate Path
- **Given** a Library full-page tab open for workspace A (manual)
- **When** the Library toggle is clicked in a tab showing workspace B
- **Then** workspace B does NOT treat it as "already open" — the Library docks here for workspace B (SP-18)

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
| 2 | Width clamp math: [320, min(70%·row, row − sidebar − 360)], default 45% clamped [320,720]; overlay when the floors don't fit | Unit | Drag clamp; narrow-window overlay | Pure functions |
| 3 | Width memory: per-user key build + read/write/delete; reset deletes; transient re-clamp never writes (MAJ-009) | Unit | Width-memory outline; shrink-restore | Storage injected |
| 4 | Single-panel reducer: open replaces, toggle closes, close clears URL param; EVERY path awaits `beforeLeave`, a `false` cancels store+URL (CRIT-001) | Unit | One-at-a-time; guard scenarios | SP-7/SP-11/FR-013 |
| 5 | Panel-param validation: registered ids kept; unknown AND unregistered-but-future ids dropped via URL replace | Unit | Unknown id dropped | SP-14/MAJ-012 |
| 6 | Deep-link restore maps `panel=<id>` (+browser session/agent; mail agent) to state; bare browser drops (SP-21); mail no-mailbox → choose state (SP-23) | Unit | Deep link restores; bare browser; mail choose-state | SP-21/SP-23 |
| 7 | Shell renders header (title/Expand/Close) + content + separator | Component | Panel docks beside chat | Vitest, shell component |
| 8 | Library under shell: EVERY close/replace path runs the guard — header Close, Expand, open-other (Watch live, tab toggle, sidebar), deep-link replace, workspace switch; cancel keeps panel + edit | Component | Guard scenarios (Close, Watch live, deep-link replace, workspace switch) | `confirmDiscardLibraryEdits` via `beforeLeave`, CRIT-001 |
| 8b | Cancelled transition is clean: store, URL and content untouched after a `false` from `beforeLeave` | Component | Guard scenarios (cancel clean) | CRIT-001's loss mechanism was store-switch-first — this pins the order |
| 8c | Re-dock no-clobber: pop-out close re-docks ONLY into an empty slot | Component | Re-dock no-clobber | MAJ-006 |
| 9 | Browser under shell: pop-out ownership handover preserved (re-dock on close) | Component | Expand scenarios | Existing handoff reactions |
| 9b | Browser under shell: width change triggers the existing settle/handover with visible status | Component | Browser handover | FR-015's ONLY mapping (MAJ-011 fix) |
| 10 | Separator a11y: role/orientation/name/controls/valuetext/values, 16px steps, Home/End | Component | Keyboard walkthrough; landmark | MIN-003 |
| 11 | Tab-strip toggle semantics: `aria-pressed` model, dropdown parity, mixed mode (links vs toggles) | Component | Tab toggles; dropdown parity; mixed mode | MAJ-007/MAJ-012 |
| 12 | Phone: takeover + inert chat + no handle at <640px; grows out cleanly | Component | Phone scenarios | `useMediaQuery` |
| 13 | Escape layers: modal above panel first; BROWSER Escape never closes (SP-19); focus follows the per-case rules | Component | Escape scenarios | SP-19/MIN-002 |
| 14 | Already-open tab: handle focus WITHOUT re-navigating; presence keyed panel × workspace; 150ms timeout; affordance; presence clears; stale handle dropped | Component | Reuse/detection scenarios | MAJ-002/SP-18 |
| 15 | Full-page routes still render (deep links to `/workspaces/{id}/team` etc.); Back after toggles equals today's history | Integration | US-5.4; back-button scenario | SP-22 |
| 16 | E2E: the §9 click-test list — AUTOMATED rows 1–7 and 12–16 (Playwright); MANUAL rows 8–11 (cross-tab window-handle rows — also executed by team-lead per §9/SP-10) — spec files assigned to a `ui-*` group in `tests/e2e/shards.json` (`scripts/e2e-shards.sh check` enforces assignment; MIN-008) | E2E | All | Owner: qa-lead, `tests/e2e` |

### Test datasets

#### Dataset: width bounds (drives tests 2, 10)

Basis: the root flex row width; the sidebar term is 256px when pinned (≥1024px) and 0
when not. Expected = requested clamped to [320, min(70%·row, row − sidebar − 360)].
Below 640px there is no resize (takeover). In the overlay band (floors cannot both
fit) the width clamps to [320, 70% of row] — the SP-17 chat floor governs the DOCKED
layout, where the panel shares the row; in overlay the chat column keeps the row.

| # | row / sidebar | requested | expected | Traces to |
|---|---|---|---|---|
| 1 | 1400 / 256 pinned | 980 | 784 (= min(980, 1400−256−360)) | BDD drag clamp (SP-17 ceiling) |
| 2 | 1400 / unpinned | 1200 | 980 (70% cap) | BDD drag clamp |
| 3 | 1400 / 256 pinned | 100 | 320 (floored) | BDD drag clamp |
| 4 | 1000 / unpinned | 900 | 640 (= min(700, 1000−360) — the chat floor binds) | BDD drag clamp / window shrink |
| 5 | 1400 / 256 pinned | none | 630 (45% default; ceiling 784 not binding) | BDD double-click reset |
| 6 | 900 / unpinned | none | 405 (45% default) | default |
| 7 | 640 / unpinned | 500 | 448 (OVERLAY mode — floors don't fit at 640; overlay clamps [320, 70%]) | narrow-window overlay |
| 8 | 600 / unpinned | none | <640 → phone takeover, no resize | takeover |
| 9 | 1400→1000→1400 / unpinned | stored 950 | shows 700 at 1000 (transient re-clamp, NOT written), 950 again at 1400; stored stays 950 | shrink-restore (MAJ-009) |

#### Dataset: panel param validation (drives test 5, 6)

| # | input value | outcome | Traces to |
|---|---|---|---|
| 1 | `library` | opens Library | deep link restores |
| 2 | `calendar` | opens Calendar (wave 3) | deep link restores |
| 3 | `bogus` | dropped, URL replaced, no panel | unknown id dropped |
| 4 | (absent) | no panel | US-7.3 |
| 5 | `browser` + session/agent | Browser panel with context | §8.2 |
| 6 | `browser` bare | dropped, URL replaced, NO session created (SP-21, decided) | §8.2 |
| 7 | `tasks` in wave 1 (unregistered) | dropped, URL replaced, no panel (treated exactly like `bogus` — MAJ-012) | unknown id dropped |
| 8 | `mail`, no agent selected | Mail panel opens on "choose a mailbox" (SP-23, wave 2) | §8.2 |

#### Dataset: regression — preserved behaviours (drives test 15 + existing suites)

| # | preserved behaviour | existing evidence | Traces to |
|---|---|---|---|
| 1 | `/library?workspace=…&path=…` deep link renders fullscreen explorer | `src/routes/_app/library.tsx::LibraryRoute`; `-library.deep-link.test.tsx` | US-5.4 |
| 2 | `media` route opens Library scoped + redirects | `WorkspaceMediaRedirect`; `-workspaces.$workspaceId.media.test.tsx` | US-5.4 |
| 3 | Browser pop-out blocked → toast + panel stays | `BrowserLivePanel.handlePopOut` catch | Error flows |
| 4 | Library pop-out carries selection; docked re-docks on popout close | `LibraryPanel.handlePopOut`; `LibraryRoute` announcements | US-6.1 |
| 5 | Chat region inert on phone while panel open | `AppShell.tsx::AppShell` | US-8.1 |
| 6 | Browser pop-out close re-docks ONLY into an empty slot — it never replaces a panel opened since (semantic change from today's unconditional re-dock, encoded as a regression test so it cannot silently revert) | `LibraryPanel.tsx::onLibraryPopoutClosed`; `BrowserLivePanel.tsx::watchPopoutClosed` — both re-open unconditionally today | §6 re-dock rule (MAJ-006) |

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

No backend regression surface exists (§1).

---

## 13. Functional requirements & success criteria

### Functional requirements

Wave tags (MAJ-012): `[wave 1]` ships with shell+Library+Browser; `[wave 2]` with Mail;
`[wave 3]` with Team/Tasks/Calendar. A `[wave 1]` behaviour applies to every later
panel automatically.

- **FR-001** [wave 1]: The system MUST host every panel (library, browser, mail, tasks, team,
  calendar) in ONE shared shell providing title, Expand, Close, resize border, docking
  beside the chat, phone takeover and Escape-to-close (Browser per FR-012); panels
  supply only content and expand target. [SP-4; US-1]
- **FR-002** [wave 1]: The shell migration MUST preserve every capability in the §2.2/§2.3
  inventories; per-panel Expand/Close buttons move to the shell header, content
  controls stay. [SP-4; US-1]
- **FR-003** [wave 1]: Every panel MUST be resizable by dragging its border, clamped to
  [320px, min(70% of row, row − sidebar − 360px)] (SP-17); when the floors (chat ≥360,
  panel ≥320) cannot both fit, the panel switches to overlay over the interactive chat;
  default 45% clamped to [320, 720]px; double-click reset. [SP-2, SP-3, SP-17; US-2]
- **FR-004** [wave 1]: The resize separator MUST be keyboard accessible (focusable, role
  separator, arrow keys 16px steps, Home/End) with full ARIA state: accessible name,
  `aria-controls`, `aria-valuetext`, dynamic `aria-valuemax` (MIN-003). [SP-3; US-9]
- **FR-005** [wave 1]: Remembered width MUST be keyed per USER + panel + workspace
  (SP-20, `app` bucket when none) in browser-local storage; written ONLY on a user
  choice (drag release, keyboard change, reset — reset DELETES the key); a
  window-driven re-clamp is transient and never writes (MAJ-009); clamp re-applied at
  read time. [SP-13, SP-20; US-3]
- **FR-006** [wave 1]: At most one panel MUST be open at any instant; opening one replaces the
  open one across ALL entry points, every path through the `beforeLeave` gate
  (FR-013). [SP-7, CRIT-001; US-4]
- **FR-007** [wave 1 model / wave 3 entries]: Tab-strip entries for registered panels
  MUST toggle their panel — `aria-pressed` while open, second click closes — on both the
  full strip and the compact dropdown (MAJ-007's ARIA model); unregistered entries stay
  navigation links until their wave; Chat stays the page underneath; Settings stays
  a page. [SP-11, SP-6; US-5]
- **FR-008** [wave 1]: Expand MUST open the panel's full-page route in a new browser tab and
  close the docked panel in the source tab. [SP-12; US-6]
- **FR-009** [wave 1]: If a panel's full-page tab for the SAME workspace is already open,
  re-invoking its toggle or Expand MUST SWITCH to it — via the in-memory handle
  registry (focus + context post, never re-navigation) when the app holds the handle;
  via BroadcastChannel presence (150ms bound), no docked open, and the visible
  "already open — switch" affordance for manual tabs; never a duplicate; per-workspace
  scoping throughout. [SP-9, SP-18, MAJ-002, MAJ-003; US-6]
- **FR-010** [wave 1]: The open panel MUST be URL-addressable as `panel=<registered id>` under the
  workspace Chat route's hash search, restored on reload and shared links; unknown or
  unregistered ids dropped with URL replace; open/switch/close use history REPLACE,
  never push (SP-22). [SP-14, SP-22, MAJ-012; US-7]
- **FR-011** [wave 1]: Below 640px the panel MUST take over the full width, the chat region
  MUST be inert, and no resize handle MUST render; crossing the breakpoint MUST
  transition cleanly. [SP-3, SP-4; US-8]
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
  handover with its visible status and Retry (ADR-061 posture), never a silent stall.
  [US-2]
- **FR-016** [wave 0–3, per SP-24]: Every panel the shell touches MUST have Storybook
  stories with sample data (no live gateway) — shell/Library/Browser in wave 0–1, Mail
  in wave 2, Team/Tasks/Calendar in wave 3, each replacing its wave-0 demo stand-in;
  the wave-0 static Storybook build IS the SP-10 demo (SP-16). [SP-15, SP-16, SP-24; §9, §10]
- **FR-017** [wave 1]: The shell MUST satisfy the design-system publication contract (public
  export → catalog → manifest → story → executed checks, per the design-system skill;
  touch-target and focus-visible on the separator included). [MAJ-008; US-1]

### Success criteria

- **SC-001**: Every §9 click-test row passes in a real-browser run against the
  static Storybook build (SP-16 — the stories ARE the demo) with recorded evidence
  BEFORE the founder demo and the build (SP-10 gate).
- **SC-002**: The shell hosts a panel with no panel-specific hosting code — proven by
  a TEST-ONLY third registration that mounts through the shell with ZERO shell edits
  (MAJ-012's acceptance proof); production wave-1 code contains exactly two real
  registrations (library, browser).
- **SC-003**: A keyboard-only operator can open, resize (within 16px granularity),
  reset, switch, and close every panel without a mouse.
- **SC-004**: A shared link `…chat?panel=<id>` restores the named panel on a machine
  that has never visited the app before this call (width defaults, panel restores).
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
| FR-003 | US-2 | Drag clamp; narrow-window overlay; double-click reset; window shrink | 2, 10, 12 |
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
| FR-016 | — (§9/§10 demo + stories) | §9 click-test list runs on the static build | 16 |
| FR-017 | US-1 | Design-system publication checks | 7, 16 |

**Completeness**: every FR appears; every BDD scenario traces; test numbers refer to
§12's order column (including 8b, 8c, 9b).

### Decision coverage (SP-16..SP-24)

| Decision | Decision (one line) | Lands in | Verified by |
|---|---|---|---|
| SP-16 | Stories ARE the demo — one static build, click-tested before the founder sees it | §9, §10, FR-016, SC-001 | Test 16; team-lead manual rows 8–11 |
| SP-17 | Floors chat ≥360 / panel ≥320; ceiling min(70%, row−sidebar−360); overlay when floors don't fit | §4 US-2, §6, §7 Geometry, FR-003 | Test 2; width-bounds dataset; click-test row 3 |
| SP-18 | Already-open elsewhere: ONLY switch, per workspace; honest browser constraint | §5, §8.3, FR-009 | Test 14; click-test rows 9–10 |
| SP-19 | Browser panel Escape NEVER closes the panel | §4 US-1 AS-3, §6, §7 Accessibility, FR-012 | Test 13; click-test row 15; §11 scenario |
| SP-20 | Width stored browser-locally, keyed per USER + panel + workspace | §1, §7 Persistence, FR-005 | Test 3 |
| SP-21 | Bare `?panel=browser` never auto-starts a session — drops the param | §3.1, §6, §8.2, FR-010 | Test 6; dataset row 6 |
| SP-22 | Panel open/close/switch = history REPLACE; Back behaves as today | §4 US-7, §7 URL, FR-010 | Test 15; §11 back-button scenario |
| SP-23 | `?panel=mail` with no mailbox → "choose a mailbox" state | §6, §8.2, FR-010 | Test 6; dataset row 8 |
| SP-24 | Stories ship per wave with each panel | §9 Stories inventory, §10, FR-016, SC-007 | §10 wave gate |

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

---

## 15. Reachability (Definition of Done)

Code-correct-and-tested and reachable-by-a-user are stated separately, per the
mandatory two-line delivery rule.

**How a real user reaches this feature** (all paths verified against the entry-point
inventory in §2.1):

1. Workspace tab strip (≥1152px) and the compact view-switcher dropdown (<1152px):
   Tasks / Calendar / Library / Team entries toggle panels (Mail joins in wave 2).
2. Sidebar "Library" (virtual root), top-bar "Open library" / "Open browser", chat
   "Watch live" on browser tool-calls — all keep working through the new single-panel
   state.
3. Deep links: `?panel=<id>` on any workspace chat URL; bookmarked `media` URLs keep
   working via the retargeted redirect stub; full-page routes remain directly
   reachable and are the expand targets.
4. Mail (wave 2): the chat draft link opens the Mail panel (email spec's link).

**Reachability gates for this feature**:

- The §9 click-test list executed in a real browser against the static Storybook
  build (SP-16) with evidence, THEN the founder demo (SP-10) — before build.
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
   opens it logged-in. Expected: Calendar panel visible immediately, correct workspace.
   Category: Happy Path.
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
   (min(70% of row, row − sidebar − 360px); overlay if the floors stop fitting);
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
- The SP-17 overlay degradation is NOT the retired overlay hosting mode: the retired
  surface is the Sheet-component panel hosting mode with a pin toggle (root CLAUDE.md,
  "Retired surfaces"); SP-17's overlay is a narrow-window layout state of the same
  docked shell — same panel, same header, chat stays interactive, no Sheet component,
  no pin toggle.

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
  stable window name for app-opened tabs, detection + affordance for manual tabs
  (SP-9, §8.3).
- Q: Demo before build? → A: Yes — clickable, real-browser-verified, founder-approved
  (SP-10, §9).
- Q: Sequencing? → A: Shell + Library + Browser → Mail → Team/Tasks/Calendar (SP-8, §10).
- Q: Separate demo build or Storybook? → A: The stories ARE the demo — one static
  build, click-tested before the founder sees it (SP-16, §9).
- Q: Window too narrow for chat + panel? → A: Floors chat ≥360 / panel ≥320, ceiling
  min(70%, row − sidebar − 360); below that the panel overlays the interactive chat
  (SP-17, §4 US-2).
- Q: Panel already open in another tab? → A: ONLY switch — per workspace; app-opened
  tabs re-focused via the handle, manual tabs detected with an affordance (SP-18, §8.3).
- Q: Do panel toggles push history? → A: No — history REPLACE; Back as today (SP-22, §8.2).
- Q: Does Browser Escape close the panel? → A: Never — Escape stops driving; close
  only via ✕ (SP-19, §7).
- Q: `?panel=browser` with no session? → A: Drops the param, starts nothing (SP-21, §8.2).
- Q: `?panel=mail` with no mailbox? → A: Opens on "choose a mailbox" (SP-23, §8.2).
- Q: Where is width stored, and when is it written? → A: Browser-local, per user +
  panel + workspace; written only on a user choice (SP-20 + MAJ-009 rules, §7).
- Q: All story sets up front? → A: No — per wave, with each panel (SP-24, §10).

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
| MAJ-007 (tab ARIA + dropdown) | **FIXED** — strip is not a tablist; Chat keeps tab semantics; panel entries `aria-pressed` toggle buttons; dropdown mirrors semantics; mixed mode until wave 3 | §4 US-5, §7 Accessibility, FR-007, test 11 |
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
round 2 re-grills the corrected spec. Status stays Draft.
