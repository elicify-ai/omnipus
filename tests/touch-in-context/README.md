# D17 in-context touch check

This suite is the "in-context touch check" required by design-system definition
rule **D17** (`docs/internal/design/design-system-definition.md` — "Touch
adaptation follows the input in use") and built to be extended by **D18**
(zoomable content: pinch, pan, opening-scale). It runs the REAL application —
the same binary that ships, driven through a real gateway — not Storybook, and
it is **not a screenshot-comparison suite**. It measures the live DOM and
writes a JSON report of numbers and violations; screenshots are only captured
when a violation is found, as evidence for the one flagged item, never as a
baseline to diff against.

## What it checks

For every visible interactive control (`button`, `a[href]`, `input`,
`textarea`, `select`, `[role="button"|"link"|"menuitem"|"option"|"tab"|
"checkbox"|"radio"|"switch"]`, `[contenteditable="true"]`, and any element
with a positive `tabindex`) on a page, it asserts four things — see
`lib/measure.ts`:

1. **No clipping ancestor.** Walks up from the control; if an ancestor has
   `overflow: hidden` or `overflow: clip` (on either axis) AND the control's
   bounding box actually extends outside that ancestor's box, that's a
   violation. A rounded-corner container with `overflow:hidden` that doesn't
   truncate anything inside it is not flagged — only real clipping is.
2. **No clipped label/text.** `element.scrollWidth > element.clientWidth`
   (with a 1px rounding tolerance) means the control's own text content is
   wider than the box showing it.
3. **No overlapping hit regions.** Every pair of visible, non-nested
   interactive controls (excluding ancestor/descendant pairs — a button
   inside a bigger clickable row is one hit target, not two fighting for the
   same tap) must not share more than a 1px sliver of screen space.
4. **Key rows stay within their height budget.** Declared in
   `budgets/key-rows.json` (see below) — a data file, not a hardcoded number
   in test code. The check measures `getBoundingClientRect().height` on the
   row's root element and compares it to that environment's budget.

It also runs three task flows (`specs/task-flows.spec.ts`), each running the
same four checks against the page state they leave behind:

- **Model picker** — opens the composer's model selector
  (`[data-testid="composer-model-selector"]`), asserts at least 3 options are
  offered, and picks one.
- **Compose** — focuses the chat composer and types a message (does not send
  it — D17's enforcement text calls this "composing a chat message", not
  "sending" one, and this keeps the flow independent of a live LLM
  connection).
- **Sign in** — starts from a genuinely unauthenticated browser context and
  drives the real login form (reusing `tests/e2e/fixtures/login.ts`'s
  `loginAs`), then confirms the real, server-issued `omnipus-session` cookie
  landed.

## Environments

Five Playwright projects (`playwright.touch-in-context.config.ts`):

| Project | Device | Engine | Environment kind |
|---|---|---|---|
| `desktop-pointer-chromium` | 1280×800, no touch | Chromium | `desktopPointer` |
| `touch-phone-chromium` | iPhone 13 | Chromium | `touchPhone` |
| `touch-phone-webkit` | iPhone 13 | WebKit | `touchPhone` |
| `touch-tablet-chromium` | iPad Pro 11 landscape | Chromium | `touchTablet` |
| `touch-tablet-webkit` | iPad Pro 11 landscape | WebKit | `touchTablet` |

`lib/env.ts` derives the environment kind from the project name prefix — it
is what tells the key-row budget check which budget column applies. This is
Chromium+WebKit device *emulation*, not real hardware: D17's enforcement text
is explicit that "real iPhone and iPad Safari behaviour, including focus zoom
and mode switching, is verified once on real devices before C1 closes,
because browser emulation cannot reproduce it." This suite is the
before-that-step gate, not a replacement for it.

## Routes under test

Routes come from the checked-in inventory: `design-system/surfaces.json`
filtered to `kind: "route"` entries (28 as of this writing — see
`docs/internal/design/design-system-surface-inventory.md` for the full
catalog). `redirect` entries are intentionally excluded: they resolve to a
route already in this list, so checking them would just re-test the same
destination under a different URL. `screen`/`tab`/`modal` entries are reached
*by* navigating a route, not navigated to directly, so they're out of scope
for this route-level sweep (a future lane can extend `lib/routes.ts` to walk
into them).

Three routes are templated (`:workspaceId`, `:agentId`, `:sessionId`).
`lib/routes.ts`'s `discoverDynamicIds` resolves them at run time by calling
the same REST endpoints the SPA itself calls
(`GET /api/v1/workspaces`, `/api/v1/agents`, `/api/v1/sessions`) against
whatever gateway you point the suite at. A route whose id can't be discovered
(e.g. a completely empty home with zero workspaces) is recorded in the report
with `status: "skipped"` and a `skippedReason` — never silently dropped, and
never counted as a pass.

## Key-row budgets (`budgets/key-rows.json`)

Data-driven so a new key row is one JSON entry, not a code change. Each entry:

```json
{
  "id": "chat-composer",
  "description": "...",
  "selector": "[data-testid=\"composer-card\"]",
  "appliesToRouteIds": ["_app/workspaces.$workspaceId.chat"],
  "budgetPx": { "desktopPointer": 96, "touchPhone": 132, "touchTablet": 132 }
}
```

`appliesToRouteIds` scopes the row to the route surface(s) where its selector
is expected to exist — the composer card only renders on the chat route, so
its budget is only checked there.

**The starting entry's numbers are placeholders**, not a founder-approved
ceiling: D17 says to "start with the chat composer row, and make the budget
file data-driven" — it does not hand down a number. The values above are the
current measured single/short-multi-line composer height plus headroom, so
the check catches a real regression (e.g. the 2026-07-15 blanket 44px
coarse-pointer floor regression D17's evidence section describes) without
flagging normal text wrapping. Tighten or found-decision-approve them as this
program's C3 repair batch proceeds — the mechanism does not need to change to
add a second row (e.g. the workspace top-bar) later.

## Running it

This suite drives an **already-running** gateway — it has no `webServer`
block and never starts, stops, or rebuilds one itself. Point it at a gateway
you started yourself, on a home you don't mind mutating (it types into the
composer, opens menus, and logs in/out).

```bash
OMNIPUS_URL=http://127.0.0.1:<port> \
TOUCH_CHECK_USER=admin \
TOUCH_CHECK_PASS=<that home's admin password> \
  npx playwright test --config=playwright.touch-in-context.config.ts
```

`TOUCH_CHECK_USER`/`TOUCH_CHECK_PASS` are required — there is no built-in
default password (`lib/credentials.ts` fails fast with an actionable message
if either is unset). Reading a seeded dev home's password **without printing
it**:

```bash
# Bcrypt hashes in config.json can't be reversed — if the home was seeded by
# a script that POSTed to /api/v1/onboarding/complete or /api/v1/auth/login,
# the plaintext is usually still sitting in that seeding script. Extract it
# straight to a local file instead of echoing it to a terminal you or a
# transcript might capture:
node -e "
  const fs = require('fs');
  const src = fs.readFileSync('<path-to-seeding-script>', 'utf8');
  const m = src.match(/password:\s*'([^']+)'/);
  fs.writeFileSync('<a-local-gitignored-file>', m[1], { mode: 0o600 });
  console.log('wrote credential file, length=' + m[1].length); // never the value itself
"
TOUCH_CHECK_PASS="$(cat <a-local-gitignored-file>)" \
  OMNIPUS_URL=... TOUCH_CHECK_USER=admin \
  npx playwright test --config=playwright.touch-in-context.config.ts
```

### Dev loop — copying a seeded home and starting your own gateway

Never point this suite at another lane's or another developer's live
gateway. Copy a seeded `OMNIPUS_HOME`, pick a free port, and start your own:

```bash
cp -R <source-seeded-home> <your-evidence-dir>/home
# edit <your-evidence-dir>/home/config.json: set gateway.port to a free port
# (confirm with: lsof -iTCP:<port> -sTCP:LISTEN -P — empty output = free)

OMNIPUS_HOME=<your-evidence-dir>/home \
  <path-to-build>/omnipus start --allow-empty \
  > <your-evidence-dir>/gateway.log 2>&1 &
echo $! > <your-evidence-dir>/gateway.pid

# ... run the suite (see above) ...

kill "$(cat <your-evidence-dir>/gateway.pid)"
```

## The report

`globalTeardown` (`lib/global-setup.ts` logs in once via a real
`POST /api/v1/auth/login`; `lib/global-teardown.ts` runs after every
project/worker finishes) merges one JSON file per check
(`test-results/touch-in-context/raw/*.json` — one file per check, not a
shared appended log, so concurrent workers can never interleave-corrupt a
write) into a single aggregated report at
`test-results/touch-in-context/report.json`
(override with `TOUCH_CHECK_REPORT_PATH`). Shape:

```jsonc
{
  "generatedAt": "2026-09-19T...",
  "totals": {
    "checksRun": 25,
    "routeChecksOk": 0, "routeChecksWithViolations": 0,
    "routeChecksSkipped": 0, "routeChecksErrored": 0,
    "taskFlowsOk": 0, "taskFlowsWithViolations": 0, "taskFlowsErrored": 0,
    "totalViolations": 0,
    "violationsByType": { "ancestor-overflow-clip": 0, "text-clipped": 0, "hit-region-overlap": 0, "key-row-budget": 0 }
  },
  "results": [
    {
      "kind": "route-check", "id": "_app/workspaces.$workspaceId.chat",
      "templatePath": "/workspaces/:workspaceId/chat", "resolvedPath": "/#/workspaces/01M.../chat",
      "project": "touch-phone-chromium", "envKind": "touchPhone", "browserName": "chromium",
      "status": "violations", "controlsChecked": 42,
      "violations": [ { "type": "hit-region-overlap", "selector": "button ...", "otherSelector": "button ...", "detail": "overlap 6.0x18.0px" } ],
      "keyRows": [ { "id": "chat-composer", "found": true, "heightPx": 88.0, "budgetPx": 132, "withinBudget": true } ],
      "screenshotPath": "test-results/touch-in-context/screenshots/....png",
      "timestamp": "2026-09-19T..."
    }
  ]
}
```

`status` is one of `ok`, `violations`, `skipped` (route param couldn't be
resolved — see above), or `error` (navigation/measurement itself threw).
Screenshots exist only for `violations`/`error` entries with a
`screenshotPath`.

**A baseline run finding real violations is expected, not a failure of this
harness.** D17's own evidence section already documents known problems
(blanket coarse-pointer floors cutting off the agent picker on iPad, text
inputs computing to 12.25px — below the 16px iOS Safari zoom threshold). The
report is data for the repair batches (C3 etc.) to work from, not a gate this
suite itself claims to pass or fail on your behalf — wire that decision (fail
the run vs. just report) where the script gets invoked from CI.

## Known limitations

**Page-settle timing (lane T1, 2026-09-19) — fixed to a route-specific readiness signal; `controlsChecked: 0` is now a confirmed reading, not a measurement gap.** `settle()` (moved to its own module, `lib/settle.ts`, so both this file and any future caller can share it) no longer polls a document-wide control count. It now requires ALL of:
1. no busy indicator present — `[aria-busy="true"]` (`CollectionState`'s wrapper, set the instant its query is pending — not debounced), `[data-collection-loading][data-visible="true"]` / `[aria-hidden="true"][data-visible="true"]` (`CollectionState`'s loading slot / `Skeleton`, confirmed by grep to be the ONLY two producers of `data-visible` in `src/`, so that pairing is an unambiguous Skeleton fingerprint), or `.animate-spin` (the ad hoc spinner convention used by routes that predate `CollectionState`, e.g. `DefaultWorkspaceRedirect`);
2. the interactive-control count, scoped to `#main-content` (`AppShell`'s `<main>` — falls back to `document.body` for shell-less routes: login/landing/onboarding, and `__root`/`_app` before `AppShell` has mounted) rather than the whole `document`, is unchanged across two consecutive 250ms polls — INCLUDING a stable count of zero, now that condition 1 supplies the "not still loading" signal the old version was missing (it refused to ever settle at zero for exactly that reason);
3. capped at `TOUCH_CHECK_SETTLE_TIMEOUT_MS` (default 8s, up from 5s to give the extra network round trips these checks imply headroom on a loaded shared machine).

Scoping the control count to `#main-content` — the one DOM node that holds ONLY the current route's own content, excluding `AppShell`'s persistent sidebar/banners — is what makes "ready" a per-route claim: the old document-wide count was exactly why "at least one control exists" (the second iteration below) fired on the sidebar before any route-specific content existed. A route that never satisfies all three within the deadline is reported as its own status, `'settle-timeout'` (`report.ts`'s `ResultEntry.status`), with `settled: false` and a `settleReason` string — never silently folded into `'ok'`/zero-violations. `__root` (`src/routes/__root.tsx`, whose `Route.component` is `() => <Outlet />` — a pure pass-through with no DOM of its own) is the one route explicitly carved out (`NO_CONTENT_BY_DESIGN_ROUTE_IDS` in `lib/settle.ts`): a timeout there is expected, not a failure. In practice `__root` never timed out in either full run below — the surrounding redirect chain (`__root` → `_app` → `_app/index` → `DefaultWorkspaceRedirect` → the default workspace's chat tab, since all three of `__root`/`_app`/`_app/index` resolve to path `/`) always finished within budget and settled on the real chat page's content (14 controls on desktop, in both runs below).

**Measured, not inferred — two full 25-test runs (`dist/design-system-baseline/cli-lanes/fanout/T1/playwright-run-1.log`, `.../playwright-run-FINAL.log`; the latter's `report.json` is saved at `.../T1/final-report/report.json`), against this lane's own gateway (port 6420), all 25/25 passed both times:**

| | Zero-control route checks | % of 140 | `routeChecksSettleTimedOut` |
|---|---|---|---|
| **Before** (L10 baseline, `dist/design-system-baseline/cli-lanes/fanout/L10/baseline-report/report.json`) | 42/140 | 30.0% | n/a (field didn't exist yet) |
| **After — run 1** | 7/140 | 5.0% | 0 |
| **After — run FINAL** | 7/140 | 5.0% | 0 |

Per-environment zero-control counts:

| Project | Before (L10 baseline) | After (run FINAL) |
|---|---|---|
| desktop-pointer-chromium | 5 | 1 |
| touch-phone-chromium | 12 | 2 |
| touch-phone-webkit | 9 | 2 |
| touch-tablet-chromium | 13 | 1 |
| touch-tablet-webkit | 3 | 1 |

Of the 7 zero-control checks in each after-run, **5 are the same, fully-explained, deterministic reading every time**: `_app/browser-live`, in ALL FIVE environments. Confirmed by reading the route's own source (`src/routes/_app/browser-live.tsx`), not inferred: without a `session`/`agent` query param — which this route sweep never supplies, since it navigates the bare templated path — `BrowserLiveRoute` returns early with a plain, non-interactive `<div>` ("Missing session or agent — open this page via the…") and never mounts `BrowserLiveView` at all. Zero controls is the CORRECT reading for how the sweep drives this route, not a settle failure.

The other 2 of 7 moved between the two runs (run 1: `_app` on `touch-tablet-webkit` and `_app/index` on `touch-phone-webkit`; run FINAL: `_app/agents.$agentId` on `touch-phone-webkit` and `_app/tasks` on `touch-phone-chromium`) — each a single route/environment combination (three of the four were WebKit; one, `_app/tasks` in run FINAL, was Chromium), always `settled: true` (confirmed stable and busy-indicator-free at the moment of measurement, so this is NOT a case of the harness giving up early). Isolated re-run of the two run-1 outliers ONLY (`npx playwright test --config=playwright.touch-in-context.config.ts --project=touch-tablet-webkit --project=touch-phone-webkit -g "sweeps every inventoried route"`, log: `dist/design-system-baseline/cli-lanes/fanout/T1/playwright-run-2-isolated-webkit.log`) showed BOTH `_app` and `_app/index` rendering real content (14 and 9 controls respectively) with nothing else running against the gateway — meeting this repo's "fails twice under an isolated re-run is not a flake" bar (memory: `flakes-hide-defects-investigate`) on the "not a defect" side, the same pattern already documented below for the sign-in task flow. The run-FINAL pair (`_app/agents.$agentId`, `_app/tasks`) was observed but NOT separately isolated-retested — stated here as measured-once, not as independently confirmed non-reproducible. Read plainly: the settle mechanism itself is reliable (0 timeouts in either full run, every reported zero is a confirmed `settled: true`); a small number of non-reproducible-in-isolation (confirmed for one pair, plausible but unconfirmed for the other) zero readings remain under shared-machine contention, same character as the sign-in flake below.

**What was tried, in order, each backed by a real before/after measurement:**
1. Flat 800ms `page.waitForTimeout()` — 33/140 zero.
2. `waitForFunction` for "any element matching a selector broader than the four assertions actually check" (`[role]`, which also matches decorative roles like `role="presentation"`) — 83/140 zero, worse, because it matched the persistent sidebar nav (which exists before fetched content does) rather than anything route-specific.
3. Poll the SAME selector `measurePage()` itself uses (`INTERACTIVE_SELECTOR` from `lib/measure.ts`) until the count is stable for two consecutive 300ms polls, never accepting a stable-at-zero plateau, document-wide — 42/140 zero.
4. **(current, lane T1)** Scope the same stability poll to `#main-content` instead of `document`, gate it on an explicit busy-indicator check (`aria-busy`/`Skeleton`/`CollectionState`/`animate-spin` markers) so a stable-at-zero plateau can now be accepted as genuine, and report a real timeout as its own `'settle-timeout'` status instead of silently measuring whatever's on screen at the deadline — 7/140 zero, all confirmed `settled: true`, 0 timeouts across two full runs.

**The model-picker task flow needs the placeholder-clears wait documented in `specs/task-flows.spec.ts`** — `ModelPicker` (`src/components/chat/composer/ModelPicker.tsx`) does not forward its providers `useQuery`'s loading state into `ModelSelector`'s `catalogStatus` prop, so the trigger briefly renders a non-interactive "Connect a provider to pick a model" placeholder instead of the interactive "Loading models…" combobox the component supports for exactly that window. This is a real, observed product gap (measured: reproduced twice, isolated from a debug script, with the provider catalog confirmed connected and non-empty via a direct REST call at the same moment the picker showed the placeholder) — not a suite flake. The test waits for the placeholder to clear rather than papering over it.

**One flaky test, investigated and ruled a machine-contention artifact, not a defect.** The "sign in" task flow's `desktop-pointer-chromium` case failed twice across full 25-test serial runs on this shared, 15-concurrent-agent worktree — both times timing out waiting for the post-login banner (15s), both times as the very first test to touch a fresh browser context in that run. Two immediately-following ISOLATED re-runs (nothing else running against this gateway) passed in 7.7s and 5.5s respectively — well under the timeout. That does not meet this repo's "fails twice under an isolated re-run is not a flake" bar (memory: `flakes-hide-defects-investigate`) — neither failure was isolated. Measured, not inferred: the isolated timings are in `dist/design-system-baseline/cli-lanes/fanout/L10/` run logs from this session.

## Known gap for whoever wires this into CI/`package.json`

`playwright.touch-in-context.config.ts` is not yet in `tsconfig.tests.json`'s
`include` list (alongside `playwright.config.ts` and
`playwright.design-system.config.ts`) — this lane's ownership was scoped to
`tests/touch-in-context/` and the config file itself, not `tsconfig.tests.json`
or `package.json`. `npm run typecheck` type-checks everything under `tests/`
(which does cover this suite's own `.ts` files) but silently skips the root
config file until it's added there. `design-system/enforcement/contract.json`
also has no entry for this check yet.
