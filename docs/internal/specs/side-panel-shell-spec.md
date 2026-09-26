# Feature Specification: Workspace side-panel shell (shared, resizable)

**Created**: 2026-09-26
**Status:** Draft
**Input**: Founder interview output — `docs/internal/specs/spec-side-panel-shell.md` (request verbatim + Decisions Log SP-1..SP-14). Process order (founder, verbatim): "capture the requirements and update the design documents first, after another demo — let's do it properly." Requirements → this spec → clickable demo reviewed by the founder in a real browser (SP-10) → spec review rounds → build.

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
is client-side persistence in the pattern of the existing persisted stores
(`src/store/sidebar.ts`, `src/store/chatPreferences.ts` — both use the Zustand persist
middleware). No new REST endpoint, no WebSocket frame, no `contracts/` edit, no
regeneration. If Q2 (Questions for the founder) is answered the other way and width
memory must sync across devices, that WOULD be a contract-first change — it is not part
of this spec unless the founder says so.

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
| Unsaved-edits guard | Gate | Pop-out AND close go through `confirmDiscardLibraryEdits()` | **Must wrap the shell's Expand and Close actions for this panel** |
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

### 3.3 Cluster placement

Frontend shell/chrome cluster (`src/components/layout`, `src/store`), spanning the
library and browser component clusters and the workspaces cluster (tab bar). No backend
cluster touched (§1).

---

## 4. User stories & acceptance scenarios

Priorities: wave-1 shell behaviour is P0; cross-tab/deep-link conveniences P1; wave-3
panel adoption P1 (sequenced, SP-8); a11y P0 (non-negotiable, SP-3).

### US-1 — One shared shell hosts every panel (SP-4) — P0

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
3. **Given** the shell hosting any panel, **When** the operator presses Escape and no
   modal layer is above the panel, **Then** the panel closes; **when a modal is above
   it, Escape closes the modal first** and the panel stays.
4. **Given** all six panels registered (waves 1–3 combined), **When** each opens in
   turn, **Then** each docks with the same border, header and actions — no
   panel-specific hosting code beyond content + expand target.

### US-2 — Resizable width with shared defaults (SP-2, SP-3) — P0

An operator wants to decide how much space a panel gets by dragging its border — the
same interaction for every panel. Widths: minimum 320px, maximum 70% of the window (the
chat stays usable), default = today's width (45% clamped to 320–720px). Double-click on
the border resets to the default. Keyboard accessible: focusable separator, arrow keys,
Home/End. No drag on phones (full-screen takeover, as today).

**Why this priority**: the feature's namesake ask; the demo gate (SP-10) shows it.

**Independent test**: render the shell, drag the border, assert width changes within
bounds and the chat column absorbs the remainder.

**Acceptance scenarios**:

1. **Given** an open panel, **When** the operator drags the border left/right, **Then**
   the panel width follows the pointer live and the chat column width changes by the
   same amount (a real side-by-side split, no overlay).
2. **Given** a drag that would exceed the bounds, **When** the pointer passes 320px or
   70% of the window width, **Then** the width stops at the bound (hard clamp, no
   rubber-banding, no negative chat column).
3. **Given** an open panel, **When** the operator double-clicks the border, **Then**
   width resets to the default (45% clamped 320–720px).
4. **Given** a window resize while a panel is open, **When** the window shrinks, **Then**
   the panel re-clamps to the new 70% bound and the chat column never collapses to zero
   on a ≥640px viewport.
5. **Given** the Browser panel being resized, **When** the width settles, **Then** the
   existing remote-viewport resize handover runs (its visible "Resizing browser… / input
   will resume" status shows; a failure is visible with Retry — never a silent
   stall — reusing `BrowserLiveView`'s existing handover UI).

### US-3 — Width memory per panel × workspace (SP-13, SP-3) — P0

An operator who widens Tasks in one workspace and narrows Library in another wants each
choice remembered where they made it. The remembered width is keyed **per panel AND per
workspace**; panels opened with no workspace context (sidebar Library at the virtual
root, Browser from a global screen) use a single `app` bucket. Storage location is
founder question Q1 (§14) — the behavioural contract below is independent of it.

**Why this priority**: part of the resize story (SP-3); wrong granularity would force a
rebuild of the persistence key later.

**Independent test**: set different widths for two panels in one workspace and a third
width in another workspace; reopen each; assert each remembers its own.

**Acceptance scenarios**:

1. **Given** the operator drags the Library border to a new width in workspace A,
   **When** they close and reopen the Library panel in workspace A, **Then** the
   remembered width applies (clamped per US-2 bounds).
2. **Given** remembered widths exist for workspace A, **When** the operator opens the
   same panel in workspace B, **Then** workspace B's own remembered width applies —
   never workspace A's.
3. **Given** no remembered width for a panel × workspace pair, **When** the panel opens,
   **Then** the default (45% clamped 320–720px) applies.
4. **Given** the operator resets via double-click, **When** the panel reopens later,
   **Then** the default applies again (the reset was persisted, not just applied).
5. **Given** the operator switches workspace while a panel is open, **When** the new
   workspace's context loads, **Then** the panel stays open, re-targets to the new
   workspace's content, and re-clamps to the new workspace's remembered width.

### US-4 — One panel at a time (SP-7) — P0

The chat always keeps enough room, and attention has one focus: opening a panel replaces
the open one. This intentionally changes today's behaviour, where Library and Browser
can be open simultaneously (§3.2) — approved by SP-7.

**Why this priority**: founder decision SP-7; drives the store shape (§3.2).

**Independent test**: open Library, open Browser; assert Library closed.

**Acceptance scenarios**:

1. **Given** Library is open, **When** the operator opens Browser (any entry point:
   tab strip, "Watch live", "Open browser"), **Then** Library closes and Browser docks
   in its place.
2. **Given** a panel is open, **When** the operator clicks its own tab-strip entry
   again, **Then** the panel closes (a second click on a toggle closes — SP-11).
3. **Given** a panel is open, **When** a deep link with a different `panel` value is
   followed, **Then** the open panel is replaced by the linked one.

### US-5 — Tab-strip entries are panel toggles (SP-11) — P0

Chat stays the page underneath. Tasks/Team/Calendar/Library (and Mail in wave 2) tab
entries are toggles: highlighted while their panel is open, a second click closes. The
compact view-switcher dropdown (container <1152px) carries the same toggle semantics.
Settings remains a navigation entry to a page. Workspace-name → settings unchanged.

**Why this priority**: the discovery surface for every panel; without it panels are
reachable only through side entry points.

**Independent test**: click the Library tab entry; assert the panel opens and the entry
shows active; click again; assert closed.

**Acceptance scenarios**:

1. **Given** no panel open, **When** the operator clicks the Tasks entry, **Then** the
   Tasks panel opens over the chat page (the route stays on Chat), the entry highlights,
   and the URL gains `?panel=tasks` (US-7).
2. **Given** the Tasks panel open, **When** the operator clicks Tasks again, **Then**
   the panel closes and the entry de-highlights.
3. **Given** the Library panel open, **When** the operator clicks the Calendar entry,
   **Then** Calendar's panel replaces Library's (SP-7) and the highlight moves.
4. **Given** the current route IS a panel's full-page route (deep link to
   `/workspaces/{id}/team`), **When** the operator looks at that panel's tab entry,
   **Then** it shows active without opening a panel (the page is the view; clicking the
   entry toggles the panel on top of the chat after navigating back to Chat — the
   entry's click navigates to Chat + opens the panel in that case).
5. **Given** a narrow viewport (compact dropdown), **When** the operator uses the
   dropdown entries, **Then** the same toggle semantics apply as the full strip.

### US-6 — Expand closes the source; already-open tabs are reused (SP-12, SP-9) — P1

"Open in new tab" opens the panel's full-page view in a new browser tab AND closes the
panel in the original tab (chat regains full width) — today's Library (C4) and Browser
(flushSync close) already do this; the shell generalizes it. If a panel is already open
in a separate browser tab and the user clicks its toggle again, the app switches to that
tab instead of reopening the panel — with the honest browser constraints below.

**Why this priority**: cross-window behaviour, fully specified by founder choices
(SP-12 recommended option chosen; SP-9 accepted the constraint framing).

**Independent test**: open Library, Expand; assert new tab opens and the docked panel
closed. With the tab still open, click Library's toggle; assert no duplicate tab.

**Acceptance scenarios**:

1. **Given** any panel open, **When** the operator clicks Expand, **Then** the panel's
   expand-target route opens in a new browser tab and the docked panel closes in the
   original tab (chat regains the width).
2. **Given** the app itself opened a panel's full-page tab earlier (window named per
   §8.3), **When** the operator triggers Expand (or the toggle → tab behaviour of SP-9)
   again, **Then** the existing named tab is reused and focused — no second tab.
3. **Given** a panel's full-page tab exists that the USER opened manually (no window
   handle; e.g. via browser chrome or a shared link), **When** the operator clicks that
   panel's toggle, **Then** the app detects the existing tab (BroadcastChannel presence,
   §8.3), does NOT open a duplicate, keeps/renders the docked panel here, and shows a
   visible "already open in another tab" affordance whose action best-effort focuses
   that tab (window.focus may be ignored — the affordance itself is the guarantee).
4. **Given** the manually-opened tab is then closed, **When** the operator clicks the
   toggle again, **Then** the panel opens normally (presence detection cleared — no
   stale "already open" state).

### US-7 — URL-addressable panel state (SP-14) — P1

The open panel is part of the page address (`…/chat?panel=team`); reload and shared
links restore it. Hash routing applies (the router's search lives in the `#/` fragment
— `src/components/library/LibraryPanel.tsx::LibraryPanel.handlePopOut` comment;
`src/routes/_app/-library.deep-link.test.tsx`).

**Why this priority**: founder-chosen recommended option (SP-14); makes the one-panel
state shareable and reload-stable.

**Independent test**: open a panel, copy the URL, reload in a fresh tab; assert the
same panel restores.

**Acceptance scenarios**:

1. **Given** any panel open on a workspace Chat route, **When** the operator reloads,
   **Then** the same panel restores with its content and width.
2. **Given** a shared link carrying `?panel=calendar`, **When** it is opened, **Then**
   the Calendar panel opens over Chat without any further click.
3. **Given** a link with no `panel` param, **When** opened, **Then** no panel opens
   (today's behaviour for links).
4. **Given** a link with an unknown or stale panel id, **When** opened, **Then** the
   param is dropped (URL replaced) and no panel opens — the chat still renders; the
   failure is never silent-invisible (the URL visibly changes).
5. **Given** `?panel=browser` without session/agent context, **When** opened, **Then**
   the param degrades as §8.2 specifies (no surprise session creation — founder
   question Q2).

### US-8 — Phone behaviour (SP-3, SP-4) — P0

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

### US-9 — Keyboard and screen-reader access (SP-3, SP-4) — P0

The resize border is a focusable separator with complete assistive state; opening and
closing manages focus; panels are labelled landmarks.

**Why this priority**: SP-3 explicitly requires keyboard accessibility; non-negotiable.

**Independent test**: keyboard-only walkthrough — tab to separator, arrows adjust,
Home/End bound, Escape closes, focus lands predictably.

**Acceptance scenarios**:

1. **Given** an open panel, **When** the operator tabs to the resize separator, **Then**
   it is focusable and exposes role `separator`, vertical orientation, and current/min/max
   width values.
2. **Given** the separator focused, **When** the operator presses Left/Right arrows,
   **Then** width adjusts by a fixed step (16px) with clamping; **Home/End** set minimum
   and maximum.
3. **Given** a panel open via a toggle click, **When** the panel closes, **Then** focus
   returns to the control that opened it; **when it opens**, focus moves into the panel
   (its header) — and back out with Escape.
4. **Given** any panel open, **When** a screen-reader user lands on the panel, **Then**
   it is exposed as a complementary landmark labelled by its title (today's
   `aria-label` on the `<aside>` generalizes to the shell).

---

## 5. Behavioral contract (quick reference)

Primary flows:
- When a panel opens, it docks beside the chat in the shared shell with title, Expand,
  Close, and a resizable border; the chat keeps the remaining width.
- When another panel opens, the open one closes (one at a time, SP-7).
- When a tab-strip entry for a panel view is clicked, it toggles that panel (SP-11);
  Chat remains the page underneath.
- When Expand is clicked, the full-page route opens in a new tab and the docked panel
  closes (SP-12).
- When a panel's toggle is clicked and its full-page tab already exists, the app
  reuses/focuses that tab — or, for a manually-opened tab it cannot focus, shows the
  "already open" affordance instead of duplicating (SP-9).
- When the panel's width is dragged, changed by keyboard, or reset (double-click), the
  new width is remembered per panel × workspace (SP-13).
- When a URL carries `?panel=<id>`, that panel opens; reload and shared links restore
  it (SP-14).

Error flows:
- When a Library Expand/Close is attempted with unsaved edits, the discard-confirmation
  runs; cancelling leaves the panel open.
- When an unknown/stale panel id arrives via URL, the param is dropped and no panel
  opens.
- When the Browser panel cannot open its pop-out, today's visible toast + the panel
  staying open is preserved.
- When WebRTC video fails inside the Browser panel, the visible error + Retry
  (ADR-061) is untouched by the shell.

Boundary conditions:
- When width hits 320px or 70% of window, it clamps (drag, keyboard, and restore alike).
- When the window shrinks, the open panel re-clamps live.
- When the viewport is <640px, the panel takes over full screen, the chat is `inert`,
  and resizing is disabled.
- When the browser tab is reloaded, the URL restores the panel; the remembered width
  persists per panel × workspace.

---

## 6. Edge cases

- **Panel open + workspace switch** (US-3.5): the panel stays open, re-targets to the
  new workspace, and takes the new workspace's remembered width.
- **Browser panel deep link without session/agent** (US-7.5, Q2): degrades per §8.2.
- **Escape while a modal is above the panel**: modal closes first (US-1.3).
- **Escape during Library unsaved-edits**: the confirmation dialog takes Escape
  (topmost layer); a cancel keeps the panel open.
- **Double-click reset while below 640px**: no border exists in takeover — no-op.
- **Two entry points racing** (e.g. "Watch live" in chat while the Library toggle is
  clicked in the same interaction window): last open wins; one panel at a time holds.
- **Browser panel with an owned pop-out** (today's subscribe logic): clicking another
  panel's toggle must not strand the owned pop-out — the existing
  "close docked panel, focus owned window" reaction (`BrowserLivePanel.tsx`) is
  preserved for the Browser's own toggle; opening a DIFFERENT panel while an owned
  Browser pop-out exists closes the docked Browser panel state and leaves the pop-out
  owned and running (its re-dock reaction on pop-out close still fires).
- **Reload with `?panel=browser` and a valid session**: the Browser panel restores;
  a session that no longer exists shows the view's existing error state, not a silent
  blank (ADR-061 posture).
- **Window resized below 320px content width**: the clamp floors at 320px only while
  320px ≤ 70% of window; below that geometric impossibility (<457px window on
  desktop-width media queries — practically the phone takeover), the takeover rules
  apply.

---

## 7. Explicit non-behaviors & safeguards

### Qualitative prohibitions

- The system must not reintroduce a Sheet/overlay panel mode — docking beside the chat
  is the only layout (retired 2026-07-16 by operator direction; documented in
  `src/store/ui.ts::UiStore` and `LibraryPanel.tsx` module docs).
- The shell must not remove any capability inventoried in §2.2/§2.3; per-panel buttons
  may move to the shell header (capability preserved), never disappear.
- The shell must not know panel internals: it renders content supplied per panel and
  does not reach into the Library explorer or the browser view (each panel's pop-out
  handover semantics stay content-level).
- The system must not silently drop a failed Browser pop-out or a failed WebRTC stream
  (visible toast / visible error + Retry today; stays that way — ADR-061).
- The system must not open a second tab when one already exists for a panel (SP-9);
  worst case is a visible "already open" affordance, never a silent duplicate.
- The system must not let any panel width exceed 70% of the window — the chat stays
  usable (SP-3's rationale).
- The system must not persist the open/closed panel state server-side or reopen
  panels on reload except through the URL param (SP-14's mechanism is the URL; there is
  no panel session on the gateway).
- Settings must never become a panel (SP-6); the workspace-name entry keeps navigating
  to the settings page.
- The system must not gate panel open/close on any network call — all state is local
  (§1: no backend).

### Machine-verifiable constraints

**Geometry**:
- Panel width MUST clamp to [320px, 0.70 × window.innerWidth] on every path (drag,
  keyboard, restore, reset-clamp).
- Default width MUST be 45% of the content area clamped to [320px, 720px].
- Keyboard resize step MUST be 16px per keypress; Home = 320px; End = 70% bound.
- Viewport <640px MUST take over full width and MUST NOT render a resize handle.

**State machine**:
- At most one panel open at any instant (SP-7) — every open path (tab toggle, sidebar,
  ChatControls, "Watch live", deep link, email draft link) MUST route through the same
  single-panel state.
- Clicking the open panel's toggle MUST close it (SP-11).
- Expand MUST close the docked panel in the source tab (SP-12).

**URL**:
- The open panel MUST be encoded as search param `panel` with values exactly
  `library|browser|mail|tasks|team|calendar` (§8); any other value MUST be dropped with
  a URL replace.
- Under the app's hash routing the param lives under `#/` (e.g.
  `/#/workspaces/{id}/chat?panel=team`).

**Persistence**:
- Remembered width key MUST be panel id × workspace id (workspace-less contexts use the
  `app` bucket), written on every committed change (drag release, keyboard change,
  reset, re-clamp from a window resize may write the clamped value only if it differs).
- Storage location: per Q1 (§14) — default recommendation is browser-local
  (localStorage via the existing persisted-store pattern); if the founder chooses
  server-side, a contract-first change precedes implementation.

**Accessibility**:
- The separator MUST be focusable, role `separator`, `aria-orientation="vertical"`,
  with `aria-valuenow`/`aria-valuemin`/`aria-valuemax` in px.
- Escape MUST close the topmost layer (modal above panel, else panel).
- On open, focus MUST move into the panel; on close, focus MUST return to the invoking
  control.

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
  widthMemoryKey(workspaceId?): string          // 'panel-width.<id>.<workspaceId|app>'
}

Shell state (store, single slice replacing browserPanel/libraryPanel):
  activePanel: { id, context } | null           // at most one (SP-7)
  openPanel(id, context) / closePanel() / setPanelWidth(px)
```

- `context` carries what the panel needs (Library: `workspaceId?`; Browser:
  `sessionId, agentId`; workspace panels: `workspaceId`; Mail: mailbox context per the
  email spec).
- Panels register through a single registry; adding Mail (wave 2) and
  Tasks/Team/Calendar (wave 3) MUST require no shell change beyond a new
  `PanelDefinition` entry (SP-4's test: "each panel supplies only its content and its
  expand target").
- The shell header renders title + Expand + Close; Expand delegates to the panel's
  own pop-out behaviour (selection-carrying for Library, ownership handover for
  Browser — §2.3 kept differences).

### 8.2 URL and deep-link contract (SP-14)

- Param `panel` on the current route's search (under `#/`). Canonical example:
  `/#/workspaces/{workspaceId}/chat?panel=team`.
- Panel context that cannot live in the URL: the Browser panel's `panel=browser`
  restores ONLY if session/agent context is available in the link (extending today's
  `/browser-live?session=…&agent=…` params) or in session state; `panel=browser` with
  neither degrades to no panel (param dropped, URL replaced). Recommendation, pending
  Q2: a bare `panel=browser` never creates a new browser session — deep links must not
  start paid agent sessions implicitly.
- The `media` redirect stub
  (`src/routes/_app/workspaces.$workspaceId.media.tsx::WorkspaceMediaRedirect`)
  retargets to the deep-link form: navigate to
  `/workspaces/{id}/chat?panel=library` (replace) instead of store-call + bare redirect
  — bookmarked `media` links keep working.
- Full-page routes (`/library`, `/browser-live`, `/workspaces/{id}/{board,team,calendar}`)
  keep working exactly as today: a shared full-page link lands on the full page (US-5.4).

### 8.3 Already-open-tab contract (SP-9)

Verified web-platform facts this contract rests on (MDN Window/open, Window/focus):

1. `window.open(url, name)` reuses and focuses an existing window with the same name —
   this is the ONLY reliable programmatic re-focus, and it works only for windows the
   app itself opened. (MDN: "If the name doesn't identify an existing context, a new
   context is created…"; the reuse example calls `.focus()` on the returned handle.)
2. With `noopener`, a non-empty custom name is treated like `_blank` — no reuse, and
   `open()` returns null. The Library's current pop-out uses
   `'noopener,noreferrer'` (`LibraryPanel.tsx::handlePopOut`), so stable naming
   REQUIRES dropping `noopener` and severing `window.opener` after the open instead —
   exactly the Browser's existing pattern (`BrowserLivePanel.tsx::handlePopOut`:
   `popup.opener = null`).
3. `window.focus()` "may fail due to user settings and the window isn't guaranteed to
   be frontmost" (MDN Window/focus) — a background tab focusing itself is best-effort.

Therefore:

- **App-opened tabs**: each panel's expand route opens with a stable per-panel window
  name (e.g. `omnipus-panel-library`); re-invoking Expand or the SP-9 toggle behaviour
  reuses and focuses that window. No duplicate tab.
- **Manually-opened tabs** (user hit the URL directly): no window handle exists. The
  app detects presence over a BroadcastChannel (extending the
  `libraryHandoff`/`browserLiveHandoff` precedents): a panel toggle click pings the
  channel; an existing tab answers with presence (and may best-effort focus itself).
  The toggle then does NOT open a duplicate; the docked panel stays/becomes available
  and a visible "Library is already open in another tab" affordance offers the switch.
  The affordance is the guarantee — programmatic focus is not.
- Presence state MUST clear when the tab closes (channel disconnect / pagehide) so a
  later toggle reopens normally (US-6.4).

---

## 9. Clickable DEMO requirements (SP-10) — gate BEFORE any build

Per SP-10 and the founder process line: a clickable demo of the shell is reviewed by
the founder in a real browser BEFORE the build, and it must first pass team-lead's own
real-browser click test. House rule (founder memory, 2026-09-26): demos are interactive
and browser-verified — a static snapshot, never a live dev server handed over.

**Demo scope**: the shared shell with ALL SIX panel types present using sample content
(wave-1 panels can render their real content if convenient, but the demo MUST NOT
depend on the Mail or workspace-panel implementations):

| Panel in demo | Sample content |
|---|---|
| `library` | A lightweight file-list stand-in (or the real explorer if cheap) |
| `browser` | A static placeholder view (no live WebRTC required for the demo) |
| `mail` | Static sample message list + reader |
| `tasks` | A narrow list/kanban stand-in |
| `team` | A narrow agent-graph stand-in |
| `calendar` | A day/week stand-in |

**What the demo must demonstrate** (each maps to a US):

1. Resize by dragging, with the 320px floor and the 70%-of-window ceiling visibly
   enforced; chat column absorbing the remainder (US-2).
2. Double-click reset; width memory per panel × workspace across close/reopen (US-3).
3. One-at-a-time switching through the tab-strip toggles, highlight + second-click
   close (US-4, US-5).
4. Expand → new tab + source panel closes; re-click reuses/focuses the named tab;
   manually-opened tab detected with the "already open" affordance (US-6).
5. Deep link: copy URL with `?panel=…`, reopen in a fresh tab, panel restores; unknown
   id dropped (US-7).
6. Phone width: takeover + inert chat, no resize handle (US-8).
7. Keyboard walkthrough: Tab to separator, arrows, Home/End, Escape, focus return
   (US-9).

**Real-browser click-test list** (team-lead executes and records evidence before the
founder review; every step is a pass/fail with a screenshot):

| # | Action | Expected |
|---|---|---|
| 1 | Click Library tab entry | Panel docks beside chat; entry highlighted; URL has `?panel=library` |
| 2 | Drag border to far left | Stops at 320px; chat still visible and interactive |
| 3 | Drag border to far right | Stops at 70% of window |
| 4 | Double-click border | Width returns to default; persists after close/reopen |
| 5 | Set width in workspace A; switch to workspace B; open same panel | B's width (or default) applies, not A's |
| 6 | Click Tasks entry while Library open | Library closes; Tasks docks; highlight moves; URL `?panel=tasks` |
| 7 | Click Tasks entry again | Panel closes; entry de-highlights; `panel` param gone |
| 8 | Click Expand on Library | New tab opens full-page Library; original tab's panel closed; chat full width |
| 9 | Click Library toggle again with the expanded tab open | No duplicate tab; existing tab focused or affordance shown |
| 10 | Open full-page Library URL manually in a second tab; click Library toggle in tab 1 | No duplicate; "already open" affordance shown |
| 11 | Close the manual tab; click the toggle | Panel opens normally |
| 12 | Copy URL with `?panel=calendar`; paste into fresh tab | Calendar panel restores |
| 13 | URL with `?panel=bogus` | Param dropped; chat renders; no panel |
| 14 | Set viewport <640px; open Mail | Full-screen takeover; chat inert; no resize handle |
| 15 | Keyboard: Tab to border, arrows/Home/End/Escape | Steps work; focus returns to toggle on close |
| 16 | Open Browser panel, then click Calendar entry | Browser closes (one-at-a-time); no stranded state |

A demo failing any step is fixed and re-tested before the founder sees it (SP-10's
"team-lead's own browser check first").

---

## 10. Rollout sequence (SP-8) with narrow layouts (SP-6)

**Wave 0 — this spec + demo (SP-10).** Spec review rounds; clickable demo built and
founder-approved in a real browser. No production build starts before that approval.

**Wave 1 — Shell + Library + Browser (P0).** The shared shell lands hosting Library and
Browser: resize + width memory (SP-2/SP-3/SP-13), one-at-a-time (SP-7 — intentionally
removes today's simultaneous Library+Browser), Escape, header actions (§2.2/§2.3
buttons move to the shell header, capabilities preserved), tab-strip Library toggle
(SP-11), deep links (SP-14), already-open-tab behaviour (SP-9), phone takeover
generalized. Entry points (sidebar, ChatControls, "Watch live") keep working through
the new store slice.

**Wave 2 — Mail adopts the shell (on `feature/email-mail`).** The email spec's D11
(docked Mail panel, Library-style, plus fullscreen pop-out) is satisfied by registering
a Mail `PanelDefinition` — no shell change. Mail's expand target follows the email
spec. The chat draft-link opens the Mail panel through the same single-panel state.

**Wave 3 — Team, Tasks, Calendar become panels (SP-6).** Their tab entries become
toggles; their existing routes remain the full-page expand targets and deep links.
Narrow layouts are per-panel content work, explicitly in scope per SP-6:

| Panel | Narrow-panel layout (SP-6) |
|---|---|
| Tasks | List view fits; the board needs a narrow layout (single-column swim or stacked cards) |
| Team | Graph gets zoom/scroll inside the panel |
| Calendar | Day/week views fit the panel; month view routes to the full page (expand) |

Settings stays a page in every wave (SP-6).

**Sequencing rule**: each wave lands on `feat/resizable-side-panels` / the feature
branches in order; a wave starts only after the previous wave's gate (review gate; for
wave 0 additionally the founder demo approval). Founder questions Q1/Q2 (§14) must be
answered before wave 1 implementation (Q1 shapes the persistence key; Q2 shapes the
deep-link degrade).

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

#### Scenario: Escape closes the topmost layer only
**Traces to**: US-1, AS-3 · **Category**: Alternate Path
- **Given** a panel open with a modal above it
- **When** Escape is pressed
- **Then** the modal closes and the panel stays open
- **When** Escape is pressed again
- **Then** the panel closes

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

#### Scenario: App-opened tab is reused, not duplicated
**Traces to**: US-6, AS-2 · **Category**: Alternate Path
- **Given** the app previously opened the Library full-page tab under its stable window name
- **When** Expand (or the toggle's already-open behaviour) runs again
- **Then** the same tab is reused and focused — exactly one Library full-page tab exists

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
| 1 | Panel registry resolves every registered id to title + expand target | Unit | Header/docking scenario | Wave-1 registry (library, browser); wave-3 extends |
| 2 | Width clamp math: [320, 70%·window], default 45% clamped [320,720] | Unit | Drag clamp; double-click reset | Pure functions |
| 3 | Width memory key build + read/write per panel × workspace (`app` bucket for none) | Unit | Width-memory outline | Storage injected for testability |
| 4 | Single-panel reducer: open replaces, toggle closes, close clears URL param | Unit | One-at-a-time; toggle close | SP-7/SP-11 core |
| 5 | Panel-param validation: known ids kept, unknown dropped via URL replace | Unit | Unknown id dropped | SP-14 |
| 6 | Deep-link restore maps `panel=<id>` (+browser session/agent) to state | Unit | Deep link restores | |
| 7 | Shell renders header (title/Expand/Close) + content + separator | Component | Panel docks beside chat | Vitest, shell component |
| 8 | Library under shell: Expand/Close run the unsaved-edits guard | Component | Guard scenario | `confirmDiscardLibraryEdits` |
| 9 | Browser under shell: pop-out ownership handover preserved (re-dock on close) | Component | Expand scenarios | Existing handoff reactions |
| 10 | Separator a11y: role/orientation/values, 16px steps, Home/End | Component | Keyboard walkthrough | |
| 11 | Tab-strip toggle semantics incl. active-state + URL param | Component | Tab toggle scenarios | `WorkspaceTabBar` |
| 12 | Phone: takeover + inert chat + no handle at <640px; grows out cleanly | Component | Phone scenarios | `useMediaQuery` |
| 13 | Escape layers: modal above panel first; focus return on close | Component | Escape scenario | |
| 14 | Already-open tab: named-window reuse; BroadcastChannel presence + affordance; presence clears | Component | Reuse/detection scenarios | Channel mocked |
| 15 | Full-page routes still render (deep links to `/workspaces/{id}/team` etc.) | Integration | US-5.4 | Route tests |
| 16 | E2E click-test list (§9 table) automated where feasible (Playwright) | E2E | All | Owner: qa-lead, `tests/e2e` |

### Test datasets

#### Dataset: width bounds (drives tests 2, 10)

| # | window width | requested | expected | Traces to |
|---|---|---|---|---|
| 1 | 1400 | 980 | 980 (exactly 70%) | BDD drag clamp |
| 2 | 1400 | 1200 | 980 (clamped) | BDD drag clamp |
| 3 | 1400 | 100 | 320 (floored) | BDD drag clamp |
| 4 | 1000 | 900 | 700 (re-clamped) | BDD window shrink |
| 5 | 1400 | none (default) | 630 (45%) | BDD double-click reset |
| 6 | 900 | none (default) | 405 (45%) | default |
| 7 | 600 | none (default) | 320 (45%=270 → floored to 320) | default floor |

#### Dataset: panel param validation (drives test 5, 6)

| # | input value | outcome | Traces to |
|---|---|---|---|
| 1 | `library` | opens Library | deep link restores |
| 2 | `calendar` | opens Calendar (wave 3) | deep link restores |
| 3 | `bogus` | dropped, URL replaced, no panel | unknown id dropped |
| 4 | (absent) | no panel | US-7.3 |
| 5 | `browser` + session/agent | Browser panel with context | §8.2 |
| 6 | `browser` bare | dropped per Q2 recommendation | §8.2 |

#### Dataset: regression — preserved behaviours (drives test 15 + existing suites)

| # | preserved behaviour | existing evidence | Traces to |
|---|---|---|---|
| 1 | `/library?workspace=…&path=…` deep link renders fullscreen explorer | `src/routes/_app/library.tsx::LibraryRoute`; `-library.deep-link.test.tsx` | US-5.4 |
| 2 | `media` route opens Library scoped + redirects | `WorkspaceMediaRedirect`; `-workspaces.$workspaceId.media.test.tsx` | US-5.4 |
| 3 | Browser pop-out blocked → toast + panel stays | `BrowserLivePanel.handlePopOut` catch | Error flows |
| 4 | Library pop-out carries selection; docked re-docks on popout close | `LibraryPanel.handlePopOut`; `LibraryRoute` announcements | US-6.1 |
| 5 | Chat region inert on phone while panel open | `AppShell.tsx::AppShell` | US-8.1 |

### Regression test requirements

This feature MODIFIES existing functionality. Existing suites that MUST keep passing
(modulo the intentional SP-7 change): the library, browser, workspaces component
groups (their CLAUDE.md files name the CI groups `components-misc`,
`components-workspaces`), the routes' colocated tests, and the Playwright specs using
`workspace-tab-*` test ids (tab-strip markup persists; semantics change to toggles —
assertions on navigation to board/team/calendar from the STRIP change deliberately in
wave 3, and those specs are updated in that wave, not silently).

Two intentional behaviour changes to encode as NEW regression tests (so they can never
silently revert):
1. Simultaneous Library+Browser open is no longer possible (SP-7).
2. Library/Browser panels have no per-panel pop-out/close buttons; the shell header
   carries those actions (§2.2/§2.3).

No backend regression surface exists (§1).

---

## 13. Functional requirements & success criteria

### Functional requirements

- **FR-001**: The system MUST host every panel (library, browser, mail, tasks, team,
  calendar) in ONE shared shell providing title, Expand, Close, resize border, docking
  beside the chat, phone takeover and Escape-to-close; panels supply only content and
  expand target. [SP-4; US-1]
- **FR-002**: The shell migration MUST preserve every capability in the §2.2/§2.3
  inventories; per-panel Expand/Close buttons move to the shell header, content
  controls stay. [SP-4; US-1]
- **FR-003**: Every panel MUST be resizable by dragging its border, clamped to
  [320px, 70% of window width], default 45% clamped to [320, 720]px, double-click
  reset. [SP-2, SP-3; US-2]
- **FR-004**: The resize separator MUST be keyboard accessible (focusable, role
  separator, arrow keys 16px steps, Home/End) with full ARIA state. [SP-3; US-9]
- **FR-005**: Remembered width MUST be keyed per panel AND per workspace (app bucket
  when none), applied on open, updated on committed changes, reset persisted. [SP-13;
  US-3]
- **FR-006**: At most one panel MUST be open at any instant; opening one replaces the
  open one across ALL entry points. [SP-7; US-4]
- **FR-007**: Tab-strip entries for Tasks/Team/Calendar/Library (and Mail in wave 2)
  MUST toggle their panel — highlight while open, second click closes — on both the
  full strip and the compact dropdown; Chat stays the page underneath; Settings stays
  a page. [SP-11, SP-6; US-5]
- **FR-008**: Expand MUST open the panel's full-page route in a new browser tab and
  close the docked panel in the source tab. [SP-12; US-6]
- **FR-009**: If a panel's full-page tab is already open, re-invoking its toggle or
  Expand MUST reuse/focus it (stable window name for app-opened tabs; BroadcastChannel
  detection + visible affordance for manual tabs; never a duplicate). [SP-9; US-6]
- **FR-010**: The open panel MUST be URL-addressable as `panel=<id>` under the hash
  route, restored on reload and shared links; unknown ids dropped with URL replace.
  [SP-14; US-7]
- **FR-011**: Below 640px the panel MUST take over the full width, the chat region
  MUST be inert, and no resize handle MUST render; crossing the breakpoint MUST
  transition cleanly. [SP-3, SP-4; US-8]
- **FR-012**: Escape MUST close the topmost layer (modal above panel first, then
  panel), honouring the Library unsaved-edits guard; focus MUST move into the panel on
  open and return to the invoking control on close. [SP-4; US-1, US-9]
- **FR-013**: The Library unsaved-edits guard MUST wrap the shell's Expand and Close
  actions for the Library panel. [§2.2; US-1]
- **FR-014**: Adding Mail (wave 2) and Tasks/Team/Calendar (wave 3) MUST require only
  a new panel registration plus per-panel narrow layouts — no shell modification.
  [SP-4, SP-6, SP-8; US-1]
- **FR-015**: The Browser panel's resize MUST drive the existing remote-viewport
  handover with its visible status and Retry (ADR-061 posture), never a silent stall.
  [US-2]

### Success criteria

- **SC-001**: Every §9 click-test row passes in a real-browser run with recorded
  evidence BEFORE the founder demo and the build (SP-10 gate).
- **SC-002**: All six panels open through the same shell with no panel-specific
  hosting code beyond the panel definition (verified by registering a sample panel in
  the demo — wave-1 code contains exactly two real registrations: library, browser).
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

### Traceability matrix

| Requirement | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | Panel docks beside chat; Escape closes topmost layer | 1, 7, 13 |
| FR-002 | US-1 | Panel docks beside chat (content controls intact); guard scenario | 7, 8, 9 |
| FR-003 | US-2 | Drag clamp; double-click reset; window shrink | 2, 10, 12 |
| FR-004 | US-9 | Keyboard walkthrough | 10 |
| FR-005 | US-3 | Width-memory outline; workspace switch | 3 |
| FR-006 | US-4 | Second panel replaces; toggle closes | 4 |
| FR-007 | US-5 | Tab toggle without navigation; toggle close | 11 |
| FR-008 | US-6 | Expand opens new tab | 9, 14 |
| FR-009 | US-6 | Tab reused; manual tab detected; presence clears | 14 |
| FR-010 | US-7 | Deep link restores; unknown id dropped | 5, 6 |
| FR-011 | US-8 | Phone takeover; grows out of takeover | 12 |
| FR-012 | US-1, US-9 | Escape scenario; keyboard walkthrough | 13, 10 |
| FR-013 | US-1 | Guard scenario | 8 |
| FR-014 | US-1 | (registry design; demo SC-002) | 1 |
| FR-015 | US-2 | Browser viewport handover | 9, 15 |

**Completeness**: every FR appears; every BDD scenario traces; test numbers refer to
§12's order column.

---

## 14. Questions for the founder

Both blocks follow the house format: context, impact, options, recommendation.

**Q1 — Where is the remembered width stored? (panel × workspace memory, SP-13)**

- *Context*: SP-13 fixed the key granularity (per panel × per workspace) but not the
  storage location. Browser-local storage (the SPA's existing persisted-store pattern —
  `src/store/sidebar.ts`, `src/store/chatPreferences.ts`) needs no backend change and
  matches §1's "frontend-only" posture; server-side storage would sync widths across a
  user's devices but requires a new user-preferences wire contract (Hard Constraint #8
  5-step process) and gateway work.
- *Impact*: browser-local means widths differ per browser/device (the same user on a
  second machine re-drags once); server-side means a contract change ahead of wave 1
  and a longer critical path — for a cosmetic preference.
- *Options*: (A) browser-local localStorage, no contract change — **recommended**;
  (B) server-side preference, contract-first change before wave 1.
- *Recommendation*: **A.** A width is a per-device ergonomic choice; the fastest path
  to the founder's demo-and-build order. A later server-side home can migrate the
  store without changing behaviour (FR-005's key is storage-agnostic).

**Q2 — What does a bare `?panel=browser` deep link do? (no session/agent context)**

- *Context*: every other panel restores from just the id; the Browser panel needs a
  live session + agent (`/browser-live` refuses without them —
  `src/routes/_app/browser-live.tsx::BrowserLiveRoute`). A deep link could (a) degrade
  to no panel, or (b) implicitly create a new browser session via the "Open browser"
  flow.
- *Impact*: (b) means a pasted link silently starts an agent browser session — a paid,
  resource-consuming side effect a link recipient did not ask for; (a) means the link
  recipient reopens the browser from the UI deliberately.
- *Options*: (A) drop the param, show chat, no session created — **recommended**;
  (B) create a session through the existing "Open browser" flow.
- *Recommendation*: **A.** Deep links must never implicitly start agent sessions; the
  explicit entry points ("Open browser", "Watch live") remain the only session
  creators.

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

- The §9 demo click-test list executed in a real browser with evidence, THEN the
  founder demo (SP-10) — before build.
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
   shrinks in CSS px). Expected: panel re-clamps to the new 70%; nothing overflows the
   viewport. Category: Edge Case.

---

## 17. Assumptions

- The existing docked-flex-row hosting geometry stays (panels render as flex siblings
  in the root row — `AppShell.tsx`); the shell keeps that geometry, so chat absorption
  remains automatic. Moving the panel INTO the workspace screen (below the top bar) is
  explicitly not part of this spec.
- Panels remain app-global (openable from any screen, as today) — the workspace
  context, when present, comes from the route.
- Hash routing stays; the `panel` param is a router search param under `#/`.
- The demo is a static build snapshot served for the founder, not a dev server
  (founder rule 2026-09-26).
- Wave-2/3 details (Mail context shape, the three narrow layouts) are specified to
  contract level here and detailed in their own specs/branches (`feature/email-mail`
  for Mail; wave-3 spec for the narrow layouts).
- No telemetry, no new deps: resize uses native pointer events; BroadcastChannel is
  already in use (library/browser handoff), no polyfill (Safari 15.4+ supports it;
  presence detection degrades to "no manual-tab detection" on older browsers —
  the docked panel still opens, worst case a duplicate full-page tab, never a crash;
  founder browsers today all support it).

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
  location → Q1 (§14).
- Q: Deep links? → A: Yes — `?panel=<id>`, reload + shared links restore (SP-14).
- Q: Already-open tab? → A: Switch to it; browser constraints stated honestly —
  stable window name for app-opened tabs, detection + affordance for manual tabs
  (SP-9, §8.3).
- Q: Demo before build? → A: Yes — clickable, real-browser-verified, founder-approved
  (SP-10, §9).
- Q: Sequencing? → A: Shell + Library + Browser → Mail → Team/Tasks/Calendar (SP-8, §10).
