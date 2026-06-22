# UAT Subagent Harness Guide (read first)

You are running an **exploratory UAT** against an isolated Omnipus gateway. You impersonate a
human user, drive the SPA through a headless browser via Playwright, screenshot every step, and
report **bugs**, **UX issues**, and **coverage gaps**. Be a curious, slightly impatient human —
click things, try the obvious path, note when something is confusing or broken.

## Your gateway is yours alone
- Each subagent has its **own** gateway instance + `OMNIPUS_HOME`. You cannot break other groups.
- Base URL and auth file are given in your task prompt. Seeded roster: Mia (Assistant), Jim
  (Orchestrator), Ava (Builder), Ray (Scout) — all `core`/locked — plus a `Worker` (Subagent/locked).
- Model: `openrouter/z-ai/glm-5.2` via OpenRouter (real LLM; chats cost real tokens — be economical).

## How to drive the browser
Write ONE self-contained ESM script **inside the repo** (e.g. `/tmp/uat/g<N>-journey.mjs` is fine,
but it must `import` the harness by absolute path) and run it with `node`. The script lives in one
node process, so the browser + login state persist across all steps. Import the harness:

```js
import { launch, shot, finish, tryClick } from '/home/dev/omnipus/docs/internal/uat/harness/lib.mjs';
const { page, errors } = await launch({
  baseURL: '<YOUR BASE URL>',
  statePath: '<YOUR auth.json>',   // OMIT this line if your prompt says "un-onboarded"
  shotDir: '<YOUR screenshot dir>',
});
// ... steps ...
await finish();   // prints captured console/pageerror/network errors as JSON, closes browser
```

`shot(page, 'NN-label')` writes `<shotDir>/NN-label.png`. Number them so the order is clear.
After running, **Read the PNGs** to see what a human would see, then iterate (edit + re-run) to
explore reactively. Run with a timeout: `timeout 180 node <script> 2>&1`.

## Navigation — CRITICAL
The SPA uses **HASH routing**. Direct `page.goto('/agents')` does NOT work (stays on chat).
Two reliable ways to navigate:
1. **Direct hash URL** (then `waitForTimeout(1500)`), e.g. `await page.goto('/#/workspaces/<id>/board', {waitUntil:'networkidle'})`.
2. **Like a human — the sidebar drawer:** click the top-left hamburger (first header button), then click.

**⚠️ NEW IA (workspace-as-project — the app was just redesigned; ignore older route lore):**
- The sidebar is reorganized: **WORKSPACES** (primary list — "My Workspace" ⭐ + any named ones) and a
  **Library** group: **Agents · Connectors · Skills & Tools**, then **Settings · Sign out**. There is NO
  top-level Chat/Tasks/Automations anymore (**Automations was removed**).
- **You're always inside a workspace.** Clicking a workspace enters it and lands on its **Chat** tab. A
  workspace is a container with a **7-tab bar**: **Chat · Board · List · Graph · Calendar · Team · Settings**.
- **Routes:** `/#/` redirects to the default workspace's Chat. Workspace tabs:
  `/#/workspaces/<id>/chat` (default) · `/board` · `/list` · `/graph` (Task DAG) · `/calendar` · `/team`
  (delegation graph editor) · `/settings`. The `<id>` is per-instance — get it from the sidebar (click "My
  Workspace") or from `/#/workspaces` (the index). `/#/tasks`, `/#/command-center`, `/#/automations` redirect
  into the default workspace's tabs.
- **Agents area** (`/#/agents`): two views — **Agents (library)** (filter All | by workspace; create
  Main/Subagent/subagent_3p; agent profile slide-over) and **Workspace Teams** (index → links to a workspace's
  `/team` delegation graph). The agent profile slide-over opens at `/#/agents/<id>`.
- **Settings** (`/#/settings`): tabs incl. **Gateway** (god-mode toggle + restart control). **Connectors**
  (`/#/connectors`): channels + the **email mailbox account**.
- Graph (Task DAG) and Team (delegation graph) render with **React Flow** — pannable canvases; screenshot them.

## Selectors
Prefer `getByRole('button'|'link'|'tab', { name })` and `getByText(...)`. For inputs,
`getByPlaceholder(/Message/i)`. Chat send = fill the message box then `page.keyboard.press('Enter')`.
Cribbing selectors: the existing Playwright specs in `tests/e2e/*.spec.ts` (named in your prompt)
show working selectors for your screens — read them.

## Waiting for LLM responses
After sending a chat, poll up to ~40s for the assistant text to appear:
```js
let got=''; for (let i=0;i<40;i++){ await page.waitForTimeout(1000);
  const t=await page.locator('body').innerText(); if (/<expected>/i.test(t)){got='ok';break;} }
```
Subagent/delegation runs (Jim → Ava) can take longer — allow 60–90s and screenshot the
`subagent_start`/`subagent_end` brackets if they appear.

## What to record per step
For each step: the action, the screenshot filename, and your **human observation** (what worked,
what was confusing, what looked broken). Capture `errors` from `finish()` — console errors and
HTTP 4xx/5xx are evidence. Note anything where the UI and reality disagree (e.g. a success toast
but the data didn't change, or a badge that says "UNRESOLVED" while the feature works).

## Output contract (your final message = JSON, nothing else after it)
Return a single JSON object:
```json
{
  "group": <N>,
  "persona": "<your name + who you are, e.g. 'Dana, non-technical first-timer'>",
  "journeys": ["<name>", ...],
  "steps": [{"journey":"...","action":"...","screenshot":"group-N/NN-label.png","observation":"...","feeling":"first-person reaction — what you expected, what delighted or frustrated you"}],
  "bugs": [{"severity":"Critical|Major|Minor","title":"...","repro":"...","screenshot":"...","evidence":"console/network/api"}],
  "ux_issues": [{"title":"...","detail":"...","screenshot":"...","recommendation":"..."}],
  "coverage_gaps": [{"title":"...","detail":"...","api_exists_but_ui":"..."}],
  "key_question_answers": {"<question>":"<your answer with evidence>"},
  "readiness": {"score_1to5": <int>, "would_i_ship_today": "yes|no|with-fixes", "what_felt_premium": "...", "what_felt_janky_or_unfinished": "...", "prose": "your overall felt impression of usability + readiness as a human user"},
  "console_network_errors": ["..."]
}
```
**You MUST fill `persona`, every step's `feeling`, and the `readiness` block** — sharing how it FEELS to use
(usability + ship-readiness) is a primary purpose of this UAT, not an afterthought.
Severity: **Critical** blocks a core function · **Major** feature broken but workaround exists ·
**Minor** cosmetic/edge · UX = usability concern (put in ux_issues).

Screenshots paths in JSON should be **relative to `docs/internal/uat/screenshots/`** (e.g. `group-3/05-handoff.png`).

## Rules
- Do NOT touch other groups' ports/homes. Do NOT git commit. Do NOT edit app source code.
- You may write/run throwaway `.mjs` scripts and read screenshots freely.
- If a screen is broken, screenshot it, record the bug, and MOVE ON — don't get stuck retrying forever.
- Be honest: report what you actually observed, including "this worked fine."
