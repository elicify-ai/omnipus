// W2-1 contract §2.9 — R45 tooltip geometry acceptance.
//
// The kit Tooltip's bubble must fit the text (the old fixed w-64 is gone)
// and must never draw outside the viewport or over the pinned sidebar — at
// the default root size AND at a 20px root, where every rem-based size in
// the header grows. These are GEOMETRIC facts: jsdom has no layout engine,
// so they are measured here in Chromium against the real SPA, the real
// WorkspaceTabBar (its icons-only strip wraps every panel toggle in the kit
// Tooltip) and the real CSS. Only the API/WS edges are stubbed — the repo's
// own layout fixture — so no LLM key is needed.
//
// The readiness budget is 30s, the same budget chat-header-narrow.spec.ts
// runs under. A run that misses it is a FINDING to report with its log, not
// a budget to raise.
import { expect, test, type Page } from '@playwright/test';
import { backdropWorkspace, installSidebarBackdropEdges } from './fixtures/sidebar-session-backdrop';

// Long enough that the chat identity cannot keep the room the LABELLED
// strip would need: the header reaches its icons-only mode the same way it
// does for a real user with a long session title.
const LONG_AGENT_NAME = 'Q83 Reader With A Deliberately Long Display Name That Has To Be Truncated';

// How long the shell may take to render before the run is a finding.
const READINESS_TIMEOUT_MS = 30_000;

// The R45 acceptance: pinned sidebar, 1440px, default root and 20px root.
const VIEWPORT = { width: 1440, height: 800 };

// TooltipContent's collisionPadding is 8px (--space-2): the bubble must sit
// inside [8, innerWidth-8] x [8, innerHeight-8] in both axes.
const COLLISION_PADDING_PX = 8;

// The retired hand-built bubble was fixed w-64 (256px). §2.9: "A short text
// gives a bubble narrower than 256px" — the width now fits the text.
const RETIRED_FIXED_WIDTH_PX = 256;

// The strip's panel toggles (WorkspaceTabBar WORKSPACE_TABS): in icons-only
// mode each one is a kit Tooltip trigger with the label as its bubble text.
const TAB_SEGMENTS = ['board', 'calendar', 'media', 'mail', 'team'] as const;

// The API/WS edges are stubbed, so no auth state is needed.
test.use({ storageState: { cookies: [], origins: [] } });
test.describe.configure({ retries: 0 });

async function openIconsStrip(page: Page, options: { userFontSize?: number } = {}) {
  // Pin the sidebar the way the app persists the preference — the zustand
  // persist key "omnipus-sidebar" (src/store/sidebar.ts). A fresh visit is
  // NOT pinned by default, so the pin must be seeded before load; at 1440px
  // (≥ the 1024px pin breakpoint) the app renders the pinned <aside>.
  await page.addInitScript(([key, value]) => {
    window.localStorage.setItem(key as string, value as string);
  }, ['omnipus-sidebar', JSON.stringify({ state: { isPinned: true }, version: 0 })]);

  const traffic = await installSidebarBackdropEdges(page, { agentName: LONG_AGENT_NAME });
  await page.setViewportSize(VIEWPORT);
  await page.goto(`/#/workspaces/${backdropWorkspace.id}/chat`, { waitUntil: 'domcontentloaded' });

  // READINESS: the header line, with its real chat identity, is on screen.
  await expect(page.getByTestId('workspace-top-bar'), 'the workspace header never appeared').toBeVisible({ timeout: READINESS_TIMEOUT_MS });

  // The 20px root is applied the way the product applies it at runtime
  // (ProfileSection sets --user-font-size on documentElement; the strip
  // re-measures through its ResizeObserver and re-picks its mode).
  if (options.userFontSize) {
    await page.evaluate((size) => {
      document.documentElement.style.setProperty('--user-font-size', `${size}px`);
    }, options.userFontSize);
  }

  // INSTRUMENT: R45 measures the ICONS-ONLY strip. If the product never
  // reaches icons mode here, this fails honestly — the spec does not fall
  // back to whatever mode happened and measure tooltips that are not there.
  const strip = page.getByTestId('workspace-header-entries');
  await expect(
    strip,
    `the workspace strip never reached icons-only mode at 1440px${options.userFontSize ? ' at a 20px root' : ' at the default root'} with the sidebar pinned`,
  ).toHaveAttribute('data-mode', 'icons', { timeout: READINESS_TIMEOUT_MS });

  // INSTRUMENT: the sidebar is really PINNED (the docked <aside> that takes
  // layout space), not the overlay the same sidebar shows when unpinned.
  const sidebar = page.locator('aside').filter({ has: page.getByRole('navigation', { name: 'Main navigation' }) });
  await expect(sidebar, 'the pinned sidebar never rendered').toBeVisible();

  return { traffic, sidebar };
}

for (const userFontSize of [undefined, 20] as const) {
  const label = userFontSize ? 'at a 20px root' : 'at the default root';
  test(`the icons strip tooltips stay inside the viewport, clear of the pinned sidebar, and narrower than the retired fixed bubble ${label}`, async ({ page }) => {
    const { traffic, sidebar } = await openIconsStrip(page, { userFontSize });

    const sidebarBox = await sidebar.boundingBox();
    expect(sidebarBox, 'the pinned sidebar has no box').not.toBeNull();
    const viewport = await page.evaluate(() => ({ width: window.innerWidth, height: window.innerHeight }));
    expect(viewport.width, 'the viewport is not the contracted 1440px').toBe(VIEWPORT.width);

    for (const segment of TAB_SEGMENTS) {
      const trigger = page.getByTestId(`workspace-tab-${segment}`);
      await expect(trigger, `the ${segment} toggle is not on the icons strip`).toBeVisible();
      await trigger.hover();
      const bubble = page.getByRole('tooltip');
      await expect(bubble, `the ${segment} tooltip never opened on hover`).toBeVisible();
      const box = await bubble.boundingBox();
      expect(box, `the ${segment} bubble has no box`).not.toBeNull();

      // The measured geometry, printed so the receipt carries the numbers
      // and not only the verdicts (chat-header-narrow house style).
      console.log(`TOOLTIP ${userFontSize ?? 'default'}px-root ${segment}: ${JSON.stringify({
        x: box!.x, y: box!.y, width: box!.width, height: box!.height,
        sidebarRight: sidebarBox!.x + sidebarBox!.width,
      })}`);

      // §2.9: the width fits the text — a short label gives a bubble
      // NARROWER than the retired fixed 256px bubble.
      expect.soft(box!.width, `the ${segment} bubble is as wide as the retired fixed w-64 bubble`).toBeLessThan(RETIRED_FIXED_WIDTH_PX);

      // collisionPadding 8: the bubble rect stays inside
      // [8, innerWidth-8] x [8, innerHeight-8].
      expect.soft(box!.x, `the ${segment} bubble crosses the left viewport edge`).toBeGreaterThanOrEqual(COLLISION_PADDING_PX);
      expect.soft(box!.x + box!.width, `the ${segment} bubble crosses the right viewport edge`).toBeLessThanOrEqual(viewport.width - COLLISION_PADDING_PX);
      expect.soft(box!.y, `the ${segment} bubble crosses the top viewport edge`).toBeGreaterThanOrEqual(COLLISION_PADDING_PX);
      expect.soft(box!.y + box!.height, `the ${segment} bubble crosses the bottom viewport edge`).toBeLessThanOrEqual(viewport.height - COLLISION_PADDING_PX);

      // R45: the bubble does not intersect the pinned sidebar's rect — it
      // sits entirely to the right of it (1px measuring tolerance).
      expect.soft(box!.x, `the ${segment} bubble overlaps the pinned sidebar`).toBeGreaterThanOrEqual(sidebarBox!.x + sidebarBox!.width - 1);

      // Leave the bubble before the next tab: move the pointer well away
      // from trigger and bubble (the content is hoverable, so only a move
      // outside that area closes it) and wait until it is gone, so each
      // measurement is of exactly one bubble.
      await page.mouse.move(VIEWPORT.width / 2, VIEWPORT.height - 40);
      await expect(bubble, `the ${segment} tooltip never closed before the next measurement`).toHaveCount(0);
    }

    expect(traffic.unknown, 'a missing API fixture is a backdrop failure').toEqual([]);
    expect(traffic.errors, 'a page crash is a backdrop failure').toEqual([]);
  });
}
