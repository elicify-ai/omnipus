# Workspace side-panel shell — interview output (requirements)

- **Status:** Interview output (input to `plan-spec`; not the final spec)
- **Date:** 2026-09-26 · **Interviewer:** team-lead (chief session `session_01T5RGTNve11wAMUcQa4aL41`) · **Founder:** Daniel Piatkowski
- **Integration branch:** `release/v0.1.1` · **Work branch:** `feat/resizable-side-panels`
- **Process (founder, verbatim):** "capture the requirements and update the design documents first, after another demo — let's do it properly." Order: requirements → spec → clickable demo reviewed by the founder in a real browser → spec review rounds → build.

## Request (founder, verbatim)

> "it should be also a slideout panel like the library and browser because it must be possible to continue the chat with the agent … the user must be able to adjust per mouse the width by dragging the border so he can decide how much space to give to the panel, that must be the same for email, library and browser"
>
> "also the email needs the expand in new tab functionality, so in a nutshell the underlying panel for all 3 must be the same shared component"
>
> "would it be too much effort to turn the other tabs of a workspace, like tasks and team, the same side panel?" → Team, Tasks, Calendar: yes; one panel at a time; after the shell lands.
>
> "if one of the panels is open in a separate tab, and someone clicks let's say team and it is open already, it needs to switch to that open tab and not reopen the panel"

## As-is (read 2026-09-26 on `release/v0.1.1`)

| Surface | Today |
|---|---|
| Library panel | `src/components/library/LibraryPanel.tsx` — docked `<aside>` next to chat, fixed width `sm:w-[45%] sm:min-w-[320px] sm:max-w-[720px]`, no resize; tab-strip entry `media` is a redirect stub that opens the panel; full-screen pop-out route exists |
| Browser live panel | `src/components/browser/BrowserLivePanel.tsx` — same hard-coded width, no resize |
| Hosting | `src/components/layout/AppShell.tsx` — panel state in `src/store/ui.ts` (`browserPanel`, `libraryPanel`); phone (<640px) full-screen takeover with `inert` on the chat region |
| Workspace tabs | `src/components/workspaces/WorkspaceTabBar.tsx::WORKSPACE_TABS` — Chat, Tasks (`board`), Calendar, Library (`media`), Team; Tasks/Calendar/Team are full pages today (`BoardView`, `WorkspaceTasksTab`, `WorkspaceTeamTab`, calendar route) |
| Mail (in progress) | Email spec (`feature/email-mail`, `docs/internal/specs/email-mail-view-spec.md`) plans a Library-style docked Mail panel (D11); prototype + clickable demo on `feature/email-mail-proto` / `feature/email-mail-demo` |

## Decisions Log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| SP-1 | Mail placement | Mail is a slide-out panel next to the chat (like Library and Browser), so the chat with the agent continues while mail is open | Founder verbatim above (D39 in the email decisions) | Founder | 2026-09-26 |
| SP-2 | Resizable width | Every side panel is resizable by dragging its border with the mouse; identical behaviour for all panels | Founder verbatim (D40) | Founder | 2026-09-26 |
| SP-3 | Resize defaults | Width remembered separately per panel; min 320 px; max 70% of the window so the chat stays usable; default = today's width (45%, 320–720 px); double-click on the border resets; keyboard accessible (focusable separator, arrow keys, Home/End); no drag on phones (full-screen takeover as today) | Team-lead defaults stated to the founder; not objected | Team-lead | 2026-09-26 |
| SP-4 | One shared shell | ONE shared panel component for Mail, Library, Browser: header (title, open in new tab / expand, close), resize border, docking next to the chat, phone takeover, Escape to close; each panel supplies only its content and its expand target. Existing Library/Browser header capabilities must not be lost (inventory first; any kept per-panel difference justified) | Founder verbatim (D41) | Founder | 2026-09-26 |
| SP-5 | Mail expand | Mail gets "open in new tab / expand" like the Library | Founder verbatim (D41) | Founder | 2026-09-26 |
| SP-6 | Workspace tabs as panels | Team, Tasks and Calendar become panels in the same shell; their current full page stays reachable via "open in new tab". Settings stays a page. Wide layouts need a narrow-panel layout (Tasks: list fits, board needs a narrow layout; Team graph zoom/scroll; Calendar day/week in panel, month via new tab) | Founder (D42) | Founder | 2026-09-26 |
| SP-7 | One panel at a time | Opening a panel replaces the open one; the chat always keeps enough room | Founder (D42) | Founder | 2026-09-26 |
| SP-8 | Sequencing | Shell with Library + Browser first → Mail adopts it → Team/Tasks/Calendar | Founder (D42) | Founder | 2026-09-26 |
| SP-9 | Already-open tab | If a panel is already open in a separate browser tab and the user clicks it again, switch to that tab instead of reopening the panel. Constraint to specify honestly: browsers only let a page re-focus a tab it opened itself under a stable window name; a manually opened tab can be detected (e.g. BroadcastChannel) and the user told it is already open | Founder (D43); feasibility note by team-lead (inferred) | Founder | 2026-09-26 |
| SP-10 | Demo before build | A clickable demo of the shell (all panels with sample content; resize, expand/new tab, close, one-at-a-time switching, already-open-tab behaviour, phone width) is reviewed by the founder in a real browser before the build; it must pass a real-browser click test and team-lead's own browser check first | Founder process instruction + team rule (demos are interactive, browser-verified, static snapshot) | Founder | 2026-09-26 |
| SP-11 | Tab strip (O1) | Chat stays the page underneath; Tasks/Team/Calendar/Library/Mail tab-strip entries are panel toggles — highlighted while their panel is open, a second click closes it | Founder chose recommended | Founder | 2026-09-26 |
| SP-12 | Open in new tab (O2) | "Open in new tab" opens the panel's full-page view in a new browser tab and CLOSES the panel in the original tab (chat regains full width) | Founder chose recommended | Founder | 2026-09-26 |
| SP-13 | Width memory (O3) | Remembered width is per panel AND per workspace | Founder (not the recommendation) | Founder | 2026-09-26 |
| SP-14 | Deep links (O4) | The open panel is part of the page address (e.g. `…/chat?panel=team`); reload and shared links restore it | Founder chose recommended | Founder | 2026-09-26 |
| SP-15 | Stories for touched panels | Every panel the shell work touches gets Storybook stories with sample data: Library, Browser live panel, Mail, Team, Tasks, Calendar, plus the shared shell itself; remaining surfaces tracked in issue #899 | Founder | 2026-09-26 |
| SP-16 | Demo IS the stories (grill round 1 Q7) | The SP-15 Storybook stories ARE the SP-10 clickable demo — one static Storybook build with all six panels + the shell on sample data, click-tested in a real browser before the founder sees it; no separate standalone demo build | Founder | 2026-09-26 |
| SP-17 | Chat/panel width formula (grill round 1 Q1, MAJ-001) | Docked side-by-side only while chat stays ≥360px AND the panel stays ≥320px; panel max width = min(70% of viewport, viewport − sidebar width − 360px); when the window is too narrow to fit both floors, the panel becomes an OVERLAY over the chat instead of squeezing it; phone (<640px) stays full-screen takeover as today | Team-lead recommendation, founder asked for best practice, not objected | Founder | 2026-09-26 |
| SP-18 | Already-open-elsewhere (grill round 1 Q2, MAJ-003) | ONLY SWITCH to the other tab (no "open here too", no duplicate) — scoped per workspace: a tab counts as "already open" only when it shows that panel for the SAME workspace; the same panel for a different workspace opens normally here. Honest browser constraint stays in the spec: only tabs Omnipus opened under a stable per-workspace+panel window name can be reliably re-focused; a manually-opened tab gets the realistic degrade, never an overpromise. *[2026-09-26, mechanism note (grill round 2, MIN-210) — the DECISION is unchanged; the re-focus mechanism in the spec is now an in-memory window-handle registry + continuous presence list (spec §8.3); the stable per-workspace+panel window name is no longer used — `window.open` with a name re-navigates an existing tab instead of focusing it]* | Founder | 2026-09-26 |
| SP-19 | Browser panel Escape (grill round 1 Q4, MAJ-005) | The Browser panel's Escape key NEVER closes the panel — Escape keeps its existing meaning ("stop driving the remote browser"); the panel closes only via its ✕. Every other panel keeps Escape-to-close | Founder | 2026-09-26 |
| SP-20 | Width storage key (spec Q1 A + grill round 1 Q6 A) | Remembered width is stored browser-locally (localStorage, no wire contract), keyed per USER + panel + workspace | Team-lead default, not objected | Founder | 2026-09-26 |
| SP-21 | Bare `?panel=browser` link (spec Q2 A) | A deep link to the Browser panel with no existing session never auto-starts a session (never trigger a paid agent session from a pasted link); it drops the parameter | Team-lead default, not objected | Founder | 2026-09-26 |
| SP-22 | Panel-toggle history (grill round 1 Q3 A, MAJ-004) | Opening/closing/switching a panel uses history REPLACE, not push — the browser Back button behaves exactly as it does today (panels are toggles, not pages) | Team-lead default, not objected | Founder | 2026-09-26 |
| SP-23 | `?panel=mail` with no mailbox (grill round 1 Q5 A, MAJ-010) | A Mail deep link with no agent/mailbox selected opens the Mail panel on its "choose a mailbox" state, rather than dropping the parameter | Team-lead default, not objected | Founder | 2026-09-26 |
| SP-24 | SP-15 stories rollout (grill round 1 Q8 A) | Storybook stories ship per wave, with the panel that moves into the shell — shell/Library/Browser with wave 0-1, Mail with wave 2, Team/Tasks/Calendar with wave 3 — not all seven story sets up front | Team-lead default, not objected | Founder | 2026-09-26 |
| SP-25 | Narrow-window handling (grill round 2 Q1, MAJ-204) | Overlay mode is DELETED; the phone full-screen takeover threshold moves up to cover every window below 680px (was <640px) | Founder | 2026-09-26 |
| SP-26 | Phone panel-close affordances (founder, alongside SP-25) | In phone mode there are THREE ways back to the chat: (1) the ✕ in the panel header closes the panel; (2) opening a panel in phone mode adds ONE history step, so the phone's Back gesture/button closes the panel and shows the chat (desktop is unaffected — SP-22's history-REPLACE stays as specified there); (3) swipe-to-close the panel. Direction and threshold must not conflict with horizontal scroll inside panel content; all three ways need their own click-test rows | Founder ("swipe is actually great") | Founder | 2026-09-26 |
| SP-27 | Shared links through sign-in (grill round 2 Q2, MAJ-211) | Wave 1 adds: after signing in, the user returns to the link they opened (workspace + panel restored), not to `/` | Founder | 2026-09-26 |
| SP-28 | Browser panel reload / shared links (grill round 2 Q3, MAJ-207) | The Browser panel is NOT restored from a reload or a shared link — it reopens only via "Watch live". No session id is ever placed in a shareable link | Founder | 2026-09-26 |
| SP-29 | Workspace switch and open panels (grill round 2 Q4, MAJ-212) | Browser stays anchored to its own session (a workspace switch does not close or move it); Library follows the new workspace only if it was opened scoped to the workspace being left (not from the all-workspaces root); Tasks, Calendar and Mail follow the new workspace | Founder | 2026-09-26 |
| SP-30 | Switch-to-already-open-tab entry points (grill round 2 Q5, MAJ-213) | EVERY entry point for the same panel + same workspace switches to the already-open full-page tab, not only the panel toggle and Expand — this includes the sidebar Library button, "Open library", and "Watch live" | Founder | 2026-09-26 |
| SP-31 | Demo gate split (grill round 2 Q6, MAJ-210) | The pre-build gate splits: the Storybook stories (SP-15/SP-16) are the demo for layout-only rows (resize, limits, reset, one-at-a-time, phone width, keyboard) — a small dev-only request-mocking add-on for Storybook is allowed for this; the address-bar, new-tab, deep-link and live-Browser-driving rows are click-tested in the real running app during wave 1, before wave 1's own gate | Founder | 2026-09-26 |

## Open points — resolved (SP-11..SP-14); plan-spec raises any NEW unclear point as a founder question

| # | Question |
|---|---|
| O1 | Tab strip semantics once tabs become panels: does the Chat tab stay the page underneath, and do Tasks/Team/Calendar/Library/Mail entries become panel toggles (active state = panel open)? |
| O2 | "Open in new tab": is it a full-page route per panel (today's Library pop-out pattern) for all six panels? What happens to the panel in the original tab when the new tab opens (close it, keep it)? |
| O3 | Does a remembered width apply per panel only, or per panel × workspace? |
| O4 | Deep links: must every panel state be URL-addressable (e.g. `?panel=team`) so links and reloads restore it? |
