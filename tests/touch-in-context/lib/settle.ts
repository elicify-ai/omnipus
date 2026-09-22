import type { Page } from '@playwright/test';
import { INTERACTIVE_SELECTOR } from './measure';

/**
 * Route ids that render no content of their own BY DESIGN — a settle()
 * timeout (or a stable-at-zero settle) on one of these is the expected
 * shape, never a measurement gap to chase.
 *
 * `__root` (src/routes/__root.tsx): `Route.component` is `() => <Outlet />`
 * — a pure pass-through layout with no DOM of its own. Its actual visible
 * content is owned entirely by whichever child route mounts under it (in
 * practice `_app`, or `NotFoundPage` on a genuine 404); checking `__root`
 * for interactive controls is re-checking the child route's content a
 * second time under a different surface id, so real zero-of-its-own is the
 * CORRECT reading, not a measurement gap. (design-system/surfaces.json has
 * three entries that all resolve to path "/" — `__root`, `_app`, and
 * `_app/index`; only `__root` is a no-content-by-design shell in this
 * sense — `_app` renders the real `AppShell` sidebar/chrome, and
 * `_app/index` renders `DefaultWorkspaceRedirect`'s spinner/retry UI while
 * it resolves, both of which are real content this check should measure.)
 */
export const NO_CONTENT_BY_DESIGN_ROUTE_IDS: ReadonlySet<string> = new Set(['__root']);

/**
 * Elements this app uses, site-wide, to signal "an async fetch this region
 * depends on is still in flight" — settle() below treats any of these being
 * present as "not ready yet", the same way a real user reads a spinner or a
 * greyed-out row as "wait":
 *
 *   - `CollectionState` (src/components/ui/collection-state.tsx) sets
 *     `aria-busy={pending}` on its wrapper the instant its `state` prop is
 *     `'initial-loading'`/`'refreshing'` — NOT debounced, so it is the most
 *     immediate signal available. While fading in/out it also renders its
 *     `loading` slot inside `[data-collection-loading]` with
 *     `data-visible="true"`.
 *   - `Skeleton` (src/components/ui/skeleton.tsx) sets `aria-hidden="true"`
 *     together with `data-visible="true"` while its `pending` prop is true
 *     (debounced ~400ms in/300ms out via `useLoadingVisibility` — see that
 *     file — to avoid flicker on fast loads; `aria-busy` above is the
 *     signal for those). Confirmed by grep that these two components are
 *     the ONLY producers of the `data-visible` attribute in `src/`, so this
 *     combination is an unambiguous Skeleton fingerprint, not a guess.
 *   - Several routes predating `CollectionState` (e.g.
 *     `DefaultWorkspaceRedirect` — used by the folded-away `/automations`,
 *     `/tasks`, and the global "/" front door while it resolves the default
 *     workspace) use a bare Tailwind `animate-spin` spinner div with no
 *     `aria-busy` at all — the ad hoc but consistent "this is a spinner"
 *     convention elsewhere in the codebase.
 *
 * A page is "still loading" for settle() purposes if ANY element matching
 * this selector is present, scoped to the same content root settle() is
 * already counting controls in (see `contentRoot` below).
 */
export const BUSY_SELECTOR = [
  '[aria-busy="true"]',
  '[data-collection-loading][data-visible="true"]',
  '[aria-hidden="true"][data-visible="true"]',
  '.animate-spin',
].join(',');

/**
 * `<main id="main-content">` (src/components/layout/AppShell.tsx) wraps
 * `<ErrorBoundary><Outlet /></ErrorBoundary>` — it is the one DOM node that
 * holds ONLY the current route's own content for every `_app/*` route,
 * excluding `AppShell`'s persistent chrome (the `Sidebar`, the connection/
 * dev-mode/app-state banners, `MediaLightbox`, `SearchModal`, etc., which
 * all live outside it). Scoping both the control count AND the busy check
 * to this element — falling back to `document.body` for the shell-less
 * routes (`login`, `landing`, `onboarding`, and `__root`/`_app` themselves
 * before `AppShell` has mounted) — is what makes "ready" a ROUTE-SPECIFIC
 * claim instead of a document-wide one. The original settle() counted
 * `INTERACTIVE_SELECTOR` matches across the whole `document`, which is
 * exactly why "at least one control exists" (v2, see README) fired on the
 * persistent sidebar before any route-specific fetched content exists —
 * the sidebar's `a[href]` links live OUTSIDE this scope, so they can no
 * longer produce a false-ready read.
 */
const CONTENT_ROOT_SELECTOR = '#main-content';

export interface SettleResult {
  /** True once busy indicators cleared AND the control count held steady. */
  settled: boolean;
  /** Interactive-control count within the content root at the moment settle() returned. */
  controlsAtSettle: number;
  /** Human-readable explanation, always present — never silently blank on a timeout. */
  reason: string;
}

const DEFAULT_DEADLINE_MS = Number(process.env.TOUCH_CHECK_SETTLE_TIMEOUT_MS ?? 8_000);
const POLL_INTERVAL_MS = 250;
/** Two consecutive agreeing polls = 500ms with no new controls and no busy indicator. */
const STABLE_STREAK_REQUIRED = 2;

/**
 * Waits for the route's OWN content to finish rendering before a caller
 * measures the page, rather than a flat timeout or a document-wide control
 * count (see this file's and README's history of what was tried before).
 *
 * Three conditions, ALL required, polled every 250ms up to a deadline
 * (`TOUCH_CHECK_SETTLE_TIMEOUT_MS`, default 8s — up from the previous 5s to
 * give the extra network round trips this version's readiness checks imply
 * (e.g. `DefaultWorkspaceRedirect`'s own workspace-list fetch before it
 * navigates again) headroom on a loaded shared machine):
 *   1. no busy indicator (`BUSY_SELECTOR`) is present in the content root;
 *   2. the interactive-control count in the content root is unchanged
 *      across two consecutive polls (previously this NEVER accepted a
 *      stable-at-zero plateau, because zero-with-no-other-signal was
 *      indistinguishable from "React hasn't mounted yet" — condition 1 now
 *      supplies that missing signal, so a stable, non-busy zero is treated
 *      as a genuine, reportable empty page, not a measurement gap);
 *   3. (implicit) the content root itself has appeared — before `AppShell`
 *      mounts, `document.querySelector('#main-content')` is null, so the
 *      count/busy check falls back to `document.body`, which is also still
 *      near-empty at that point, so it naturally fails condition 2 until
 *      the shell (or the shell-less route) actually paints.
 *
 * A route that never satisfies all three within the deadline returns
 * `settled: false` with a stated `reason` — callers MUST surface that as a
 * settle failure, never silently fold it into "0 violations found" (see
 * `NO_CONTENT_BY_DESIGN_ROUTE_IDS` for the one documented exception).
 */
export async function settle(page: Page, routeId?: string): Promise<SettleResult> {
  const deadline = Date.now() + DEFAULT_DEADLINE_MS;
  let previousCount = -1;
  let stableStreak = 0;

  while (Date.now() < deadline) {
    const { count, busy } = await page
      .evaluate(
        ({ interactiveSelector, busySelector, contentRootSelector }) => {
          const root = document.querySelector(contentRootSelector) ?? document.body;
          const busyEl = root.querySelector(busySelector);
          return {
            count: root.querySelectorAll(interactiveSelector).length,
            busy: busyEl !== null,
          };
        },
        { interactiveSelector: INTERACTIVE_SELECTOR, busySelector: BUSY_SELECTOR, contentRootSelector: CONTENT_ROOT_SELECTOR },
      )
      .catch(() => ({ count: 0, busy: true }));

    if (!busy && count === previousCount) {
      stableStreak++;
      if (stableStreak >= STABLE_STREAK_REQUIRED) {
        return { settled: true, controlsAtSettle: count, reason: 'no busy indicator, control count stable' };
      }
    } else {
      stableStreak = 0;
    }
    previousCount = count;
    await page.waitForTimeout(POLL_INTERVAL_MS);
  }

  const isShell = routeId !== undefined && NO_CONTENT_BY_DESIGN_ROUTE_IDS.has(routeId);
  return {
    settled: isShell,
    controlsAtSettle: Math.max(previousCount, 0),
    reason: isShell
      ? 'no-content-by-design route (see NO_CONTENT_BY_DESIGN_ROUTE_IDS) — timeout expected, not a failure'
      : `timed out after ${DEFAULT_DEADLINE_MS}ms without a busy-indicator-free, stable control count`,
  };
}
