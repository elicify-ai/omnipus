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

## Open points — plan-spec must ask, not guess

| # | Question |
|---|---|
| O1 | Tab strip semantics once tabs become panels: does the Chat tab stay the page underneath, and do Tasks/Team/Calendar/Library/Mail entries become panel toggles (active state = panel open)? |
| O2 | "Open in new tab": is it a full-page route per panel (today's Library pop-out pattern) for all six panels? What happens to the panel in the original tab when the new tab opens (close it, keep it)? |
| O3 | Does a remembered width apply per panel only, or per panel × workspace? |
| O4 | Deep links: must every panel state be URL-addressable (e.g. `?panel=team`) so links and reloads restore it? |
