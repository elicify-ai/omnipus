# UAT Matrix — Complete Browser Automation (+ text selectors) — end with edge cases

**Live target:** the preview URL + login in the task prompt. **Drive agent: "Ray — Scout"** (has the browser tools; not Mia/Ava). Backend model glm-5.2 (vision-capable behind the scenes). You are a **human UAT tester** driving the real SPA — open the browser panel, prompt Ray, watch the live view, and directly drive the panel UI. Be adversarial; report every real defect; no sugar-coating.

**What's new (the capability under test):** the browser tools now match elements **by visible text**, both ways:
- the Playwright-style **`:has-text("…")`** / **`:text-is("…")`** pseudo-selectors an agent naturally writes, and
- a **`text` param** on `browser_click`/`browser_get_text`/`browser_wait`.
Previously those pseudo-selectors errored (`DOM Error while querying`), which is why multi-step flows (Cal.com booking) got stuck. Everything else (navigate, CSS selectors, tabs/adoption, the live panel, take-the-wheel) also under test as a **complete** browser-automation pass.

**How to observe what the agent did:** watch the live browser panel (tab strip + screencast). Ask Ray to report the raw tool outputs when you need the technical truth ("report exactly what browser_click returned"). Tool results are visible in the chat's tool-call blocks (expand them).

---

## A. Navigation + basic interaction (regression — must still work)

| ID | Scenario | Expected |
|----|----------|----------|
| A1 | Ask Ray: "navigate to example.com and tell me the page's heading." | Navigates; reports "Example Domain". |
| A2 | Omnibox (you, the human): type `news.ycombinator.com` + submit. | Navigates there; screencast updates. |
| A3 | Omnibox: type `best coffee in berlin` (not a URL). | Google search results for that query. |
| A4 | Ask Ray: "on this page, click the first link and tell me where you ended up." (CSS `a`) | Plain CSS `a` click still works (unchanged fast path). |
| A5 | Ask Ray: "screenshot the current page." | Returns an inline image; the result also states the current URL/title. |
| A6 | SSRF: ask Ray to "navigate to http://169.254.169.254/". | Blocked with a clear SSRF/refused message (not a hang, not a crash). |

## B. Text selectors — the new capability (headline of this UAT)

| ID | Scenario | Expected |
|----|----------|----------|
| B1 | Ask Ray: "go to example.com, then click the link that says **Learn more** using a has-text selector." | `browser_click` with `a:has-text("Learn more")` (or `text:"Learn more"`) succeeds — NOT a `DOM Error while querying`. Ray lands on the IANA example-domains page (iana.org/domains/example). **Fixture note:** example.com's anchor text is **"Learn more"** (the old "More information..." text was retired in the IANA redesign — verify current text before running). |
| B2 | Ask Ray: "on this page, click the element whose visible text is exactly **Learn more**" (`:text-is`). | Exact-match click works; a substring-only element would NOT be hit. |
| B3 | Ask Ray to use the **`text` param**: "click, using the text parameter, the button/link labeled **Learn more**." | Works via the `text` param (no CSS needed). |
| B4 | Ask Ray: "read the text of the paragraph that contains the word **domain** (use has-text/text)." | `browser_get_text` by text returns the paragraph text. |
| B5 | **Specificity:** a page with a button inside a wrapper both containing "Continue" → ask Ray to "click **Continue**". | Clicks the actual button (its handler fires), NOT the wrapping container — the click does something, not a silent no-op. |

## C. Tabs + adoption (ADR-041 — must still work)

| ID | Scenario | Expected |
|----|----------|----------|
| C1 | Ask Ray: "go to elicify.ai and click **Book a call** (it opens a new tab)." | The click opens + **adopts** a new tab (cal.com), auto-switches to it; the tab strip shows 2 tabs with cal.com active; Ray says it's now on cal.com. |
| C2 | Ask Ray: "open example.org in a new tab, then list the open tabs." | `browser_open_tab` + `browser_list_tabs` — strip + list reflect both tabs. |
| C3 | Switch tabs from the strip (you click a tab chip). | Active tab changes; screencast follows; **no "session ended" banner**. |
| C4 | Close the active tab (✕). | A neighbour becomes active; screencast follows; never zero tabs; no false-death banner. |
| C5 | Open ~5 tabs, then have Ray open one more (MaxTabs). | Ray is told the cap was reached (not silently stuck). |

## D. Live panel UX (ADR-040 — regression)

| ID | Scenario | Expected |
|----|----------|----------|
| D1 | Open the browser panel from the chat launcher. | Panel opens: tab strip + always-visible omnibox + header (✕ Close / 📌 Pin / ✎ Pen); a glow border + "who's driving" chip. |
| D2 | Click-to-drive (agent idle): click the live frame. | You drive; chip → "You're driving" + gold glow. |
| D3 | While Ray is browsing (streaming), try to click the frame; then click **Take over** once. | Watch-only blocks your input; a single "Take over" click → "You're driving" + gold glow (one click, not two). |
| D4 | 📌 Pin. | Panel docks beside the chat; both usable; toggle back → overlay. |
| D5 | ✎ Pen: drag a box over a region, comment, send. | The cropped image + comment reaches Ray; Ray responds about that region. |

## E. Edge cases (each maps to a fix — verify it holds)

| ID | Scenario | Expected (the fix) |
|----|----------|--------------------|
| E1 | **Async element (poll):** ask Ray to "wait for the element that says **Loaded** to appear" on a page that shows it after a delay. | `browser_wait` by text POLLS and succeeds when it appears — it must NOT fail instantly with "no match". |
| E2 | **No match:** ask Ray to "click the button that says **Nonexistent XYZ**." | A clear "no visible element matching text …" message (NOT a cryptic `DOM Error`), and it does not falsely report success. |
| E3 | **Ambiguous text:** on a page with two identical **Delete** buttons (or the cal.com calendar showing "14" twice), ask Ray to "click **Delete** / the day **14**." | Ray gets a clear "N elements match … narrow it" message (or scopes with a selector) — NOT a silent wrong click. Ray should then narrow it and succeed. |
| E4 | **Invisible element not matched:** a page with a hidden (sr-only / display:none / opacity:0) element containing the needle. | The text selector does NOT match the invisible element (matches the visible one, or clear no-match). |
| E5 | **Scope safety:** ask Ray to click "**Yes**" but scoped to a specific container that does NOT contain a decoy "Yes". | It clicks within the intended scope (or errors clearly) — it must NOT silently hit a "Yes" elsewhere on the page. |
| E6 | **Marker not leaked:** trigger a text-selected action that fails (element removed) and read the error. | The error names the **text you asked for** (e.g. "Confirm") — it must NOT show an internal `data-omnipus-tsel` attribute. |
| E7 | **Dynamic SPA selectors:** on cal.com, ask Ray to pick a time using the visible time label (e.g. "click **9:00am**"). | Text selector clicks the time slot by its visible label (where CSS `data-*` selectors previously errored). |

## Z. HEADLINE ACCEPTANCE — the full appointment-booking flow

**Z1.** Ask Ray, in ONE natural request: *"Go to elicify.ai, book a 30-minute appointment for tomorrow — click the Book-a-call button, and on the scheduling page pick tomorrow's date and an available morning time, then fill name/email and confirm. Tell me each step and the final result."*

**Expected — the whole chain works with minimal flailing:**
1. Navigate elicify.ai → click **Book a call** (by text/has-text) → new tab to **cal.com** adopted + active.
2. On cal.com: select the **30-min** meeting (by visible label), pick **tomorrow's date** (calendar day by text), pick a **morning time slot** (by visible label).
3. Fill name/email (form fields) and click **Confirm** (by text).
4. Ray reports success (or a clear, specific blocker — e.g. "no morning slots tomorrow").

This is the acceptance test the whole text-selector work exists for. Watch for: `DOM Error while querying` (must NOT appear), the agent getting stuck on new-tab handling (must adopt+switch), and the number of failed clicks (should be far lower than before — the agent should drive Cal.com by visible text). Record the tool-call count and any errors.

## Usability (narrate as a first-time user)
- U1 Did Ray complete the booking without you having to intervene? How many nudges?
- U2 Were the text-based clicks reliable, or did they hit wrong/invisible elements?
- U3 When something was ambiguous/missing, was the error clear and actionable?
- U4 Any jank in the panel (flicker, stuck, wrong-tab, false disconnect)?

## Reporting
Per row: **PASS / FAIL / PARTIAL** + what you actually saw (screenshots welcome) + repro for any FAIL. **Z1 (full booking) and B1 (has-text click works) are the two highest-priority checks.** Usability findings separately with severity. Be a skeptical human tester — try to break it. No sugar-coating.
