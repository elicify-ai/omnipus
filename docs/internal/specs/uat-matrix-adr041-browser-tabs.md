# UAT Matrix — ADR-041 Multi-Tab Live Browser (+ ADR-040 re-run)

**Live target:** the preview/gateway URL in the task prompt. Login with the seeded creds in the task prompt.
**Drive agent:** **Ray — Scout** or **Jim — Orchestrator** (they have the browser tools; do NOT use Mia/Ava for browsing). Backend model glm-5.2 (vision-capable behind the scenes). Use the Playwright MCP browser tools **as a real human user** — click, type, scroll, screenshot. Be a skeptical tester; try to break it.

## What's new in ADR-041 (multi-tab)
The live browser is now a **real multi-tab browser**:
- **A tab strip** sits above the live frame: one chip per open tab (globe icon + title/hostname + close ✕), the **active tab** gold-underlined + bold, and a trailing **＋** to open a new blank tab.
- **The agent follows `target="_blank"` / `window.open` redirects.** A click that opens a new tab is **adopted** and becomes the active tab; the agent is told "opened + switched to tab N" so it keeps going. This fixes the appointment-booking dead-end (a Cal.com button that opens a new tab).
- **New agent tools:** `browser_list_tabs`, `browser_switch_tab {index}`, `browser_close_tab {index}`.
- **The screencast follows the active tab** — switching a tab re-binds the live view to it.
- **Tab actions honour the ADR-040 control model:** switching/closing/opening a tab from the strip **takes the wheel first** (pauses the agent if it was driving), exactly like the omnibox. A merely-watching second viewer can't yank a tab from whoever's driving.

---

## Headline acceptance test (MUST pass — the whole reason for ADR-041)

| ID | Scenario | Expected |
|----|----------|----------|
| **T0** | **Appointment booking across a new tab.** Ask Ray: *"Go to elicify.ai/contact, find the button to book a 30-minute appointment, click it, and pick the first available slot."* The booking button is `<a href="https://cal.com/daniel-piatkowski-ai" target="_blank">` — it opens a **new tab**. | Ray navigates to the contact page, clicks the booking button, a **new tab opens to cal.com and is adopted as the active tab** (tab strip shows 2 tabs, cal.com active), the screencast follows to the Cal.com page, and Ray continues **on the Cal.com tab** (reads it / picks a slot) instead of getting stranded on the contact page. Ray's narration should acknowledge it opened/switched to a new tab. |

---

## Multi-tab core scenarios

| ID | Scenario | Expected |
|----|----------|----------|
| T1 | **Tab strip appears.** Open the browser panel, have Ray navigate somewhere (or navigate via omnibox). | A tab strip is visible above the frame with one chip for the current page (globe + title/hostname), gold-underlined as active. |
| T2 | **Open a new tab (＋).** Click the ＋ in the tab strip. | A new blank tab opens and becomes active; strip shows 2 tabs; the new one is highlighted; screencast shows the blank/new tab. |
| T3 | **Switch tabs.** With ≥2 tabs open, click a non-active tab chip. | That tab becomes active (gold underline moves); the **screencast re-binds to it promptly** (you see that tab's page, not the old one); no "session ended" error, no blank freeze. |
| T4 | **Close a non-active tab (✕).** Click ✕ on a tab that isn't active. | That tab disappears from the strip; the active tab is unchanged; screencast unaffected. |
| T5 | **Close the ACTIVE tab (✕).** Click ✕ on the active tab (with ≥2 open). | The tab closes; a **neighbour becomes active**; the screencast re-binds to the neighbour; strip highlight is correct; never zero tabs. |
| T6 | **Close the last remaining tab.** Close tabs until one remains, then close it. | You never end up with zero tabs — a fresh blank tab is kept/opened; no crash, no dead panel. |
| T7 | **Agent opens a tab via a tool / window.open.** Ask Ray: *"open example.com, then open example.org in a new tab, and tell me which tabs are open."* | Ray uses `browser_list_tabs` / new-tab handling; the strip reflects both tabs; Ray correctly lists them with the active one marked. |
| T8 | **Agent switches tabs.** After T7, ask Ray: *"switch to the example.com tab and read its heading."* | Ray calls `browser_switch_tab`; the active tab + screencast + strip all move to example.com; Ray reads the right page. |

---

## Edge cases (each maps to a review fix — verify it holds)

| ID | Scenario | Expected (the fix) |
|----|----------|--------------------|
| E-T1 | **Screencast rebind doesn't false-death (BLOCKER fix).** With a viewer attached and watching, trigger several tab switches in a row (agent-driven in T0/T7, or manual T3). | **No "browser session ended unexpectedly / re-attach" banner ever appears on a switch.** The live view keeps working and follows each new active tab. (This was the top blocker — a switch used to kill the view.) |
| E-T2 | **MaxTabs cap is reported, not silently swallowed (CRITICAL fix).** Open 5 tabs (the default cap) — e.g. ＋ several times or have the agent open several — then have the agent click a link/button that opens **another** new tab. | The agent is **told** the tab couldn't be opened (cap reached) — e.g. it says something like "a new tab tried to open but the max-tabs limit was reached; close a tab first" — rather than silently reporting success and getting stuck. It does NOT strand silently. |
| E-T3 | **Tab actions require control (MAJOR fix).** Open the panel for Ray and **Pin** it. Ask Ray to browse (agent driving / streaming). While Ray is mid-task, click a tab chip (switch) / ＋ / ✕. | Clicking a tab **takes the wheel first** (Ray pauses, chip flips to "You're driving") **then** performs the tab action — you don't silently fight the agent, and the action isn't dropped. |
| E-T4 | **Tab chip disabled while disconnected (fix).** Force a brief WS reconnect (e.g. toggle Pin, or briefly kill network if testable) and, while disconnected, click a tab chip. | The tab chip is **non-interactive while disconnected** (not-allowed cursor, no silent no-op); if you do trigger a send that fails, a **toast** explains it (no silent nothing-happens). |
| E-T5 | **Take-over never wedges (fix).** Repeatedly click "Take over" / click-to-drive under flaky conditions (e.g. right around a reconnect). | The "you're driving" state never gets **stuck** without actually giving you control; if a take fails you get a toast and can retry — it never locks you out. |
| E-T6 | **Switch to the tab the user opened (shared-tab handoff).** You (manually) open a new tab (＋), navigate it somewhere, then tell Ray *"look at the tab I just opened and summarise it."* | Ray can see/switch to that tab (shared browsing context) and summarises the right page. |
| E-T7 | **Stale tab strip after reconnect.** After a reconnect, confirm the strip reflects the **current** tabs (a fresh `browser_tabs` arrives on re-attach), not stale/ghost tabs. | Strip re-syncs to the real current tab set within a moment of reconnecting; no ghost tabs linger. |
| E-T8 | **Rapid switching.** Click back and forth between two tabs quickly several times. | The screencast keeps up (follows the last-clicked tab), no permanent freeze, no error banner, no wrong-tab display that persists. |

---

## ADR-040 re-run (regression — these must STILL pass with tabs added)

| ID | Scenario | Expected |
|----|----------|----------|
| R1 (was A2) | **Click-to-drive** with the agent idle. | Clicking the page drives immediately; chip → "You're driving", gold glow. |
| R2 (was A4/E7) | **Omnibox search vs URL.** Type `best pizza in tokyo` (→ Google search) and `example.org` (→ navigates). | Search text → Google results; a URL/bare domain → navigates. |
| R3 (was A6/A7) | **Watch-only while agent works.** While Ray browses, try to click/scroll/type on the page. | Input does NOT go through (watch-only); "Take over" available; cursor shows watching. |
| R4 (was A8) | **Take over** while Ray works. | Ray pauses; you hold the wheel; you can act on the page. |
| R5 (was A9) | **Hand back by message.** After taking over, send a chat message. | Ray resumes on the current page/tab (shared). |
| R6 (was A10) | **Pin side-by-side.** Toggle 📌 Pin. | Panel docks beside chat; both usable; toggle back → overlay. (Brief reconnect flicker OK.) |
| R7 (was E1) | **Take-over pauses the RIGHT session.** Pin Ray's panel, switch to a DIFFERENT chat in the sidebar, come back and "Take over" while Ray's session is working. | Pauses **Ray's** turn only — does NOT cancel the other chat; does NOT get stuck watch-only. |
| R8 (was A5) | **Pen / annotate.** ✎ Pen, drag a box over a region, comment, send. | Cropped image + comment reaches Ray; Ray responds about that region. |

---

## Usability pass (both testers — narrate as a first-time user)
- U1 Is the tab strip discoverable and obvious? Could you tell how to open/switch/close a tab with no instructions?
- U2 When the agent opened a new tab (booking flow), was it clear what happened — did you understand a new tab opened and the agent moved to it?
- U3 Is it always clear which tab is active and who's driving?
- U4 Any jank: flicker on switch, wrong-tab flashes, stuck states, ignored clicks, confusing labels?
- U5 Did tab actions while watching (take-the-wheel-first) feel natural or surprising?

## Reporting
Per row: **PASS / FAIL / PARTIAL** + what you actually saw (screenshots welcome) + a repro for any FAIL. Usability findings separately with severity (blocker/major/minor/nit). **T0 (booking across a new tab) and E-T1 (no false-death on switch) are the two highest-priority correctness checks.** No sugar-coating — every real defect reported.
