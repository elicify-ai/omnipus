// #1449 D13 — the workspace header line at narrow widths.
//
// The founder's rule (2026-10-28): the workspace strip ALWAYS keeps the
// header line; the chat slot shrinks and truncates first; the helper
// breadcrumb's caret stays on the SAME line as the breadcrumb text.
//
// These are GEOMETRIC facts — jsdom has no layout engine, so they are
// measured here in Chromium, against the real SPA, the real components and
// the real CSS. Only the API/WS edges are stubbed (the repo's own layout
// fixture), so the header under test is the production one.
import { expect, test } from '@playwright/test';
import { Session } from '../../src/lib/api/generated/schemas';
import { backdropMain, backdropWorkspace, installSidebarBackdropEdges } from './fixtures/sidebar-session-backdrop';

// Long enough that the identity line cannot fit a narrow header.
const LONG_AGENT_NAME = 'Q81 Reader With A Deliberately Long Display Name That Has To Be Truncated';
const HELPER_ID = 'q81-helper';
const helper = Session.parse({
  ...backdropMain,
  id: HELPER_ID,
  type: 'delegate',
  title: 'Q81 helper chat',
  parent_session_id: backdropMain.id,
  workspace_id: backdropWorkspace.id,
});

// The widths a real window reaches: a wide desktop, a laptop, a half-screen
// window and a narrow side-by-side window.
const WIDTHS = [1440, 900, 640, 420];

// The API/WS edges are stubbed, so no auth state is needed.
test.use({ storageState: { cookies: [], origins: [] } });
test.describe.configure({ retries: 0 });

test('the workspace strip keeps the header line at narrow widths; the chat slot truncates', async ({ page }) => {
  const traffic = await installSidebarBackdropEdges(page, { agentName: LONG_AGENT_NAME });

  for (const width of WIDTHS) {
    await page.setViewportSize({ width, height: 800 });
    await page.goto(`/#/workspaces/${backdropWorkspace.id}/chat`);

    const header = page.getByTestId('workspace-top-bar');
    await expect(header).toBeVisible();

    // The workspace navigation is still on the line: the strip may collapse
    // to icons or to its narrow menu, but the workspace stays reachable.
    await expect(page.getByTestId('workspace-name-button'), `the strip lost the workspace at ${width}px`).toBeVisible();

    // No horizontal overflow: the header line never scrolls sideways.
    const overflow = await header.evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
    expect(overflow.scroll, `the header line overflows sideways at ${width}px`).toBeLessThanOrEqual(overflow.client + 1);

    // The chat slot stays inside the line.
    const headerBox = await header.boundingBox();
    const slotBox = await page.getByTestId('chat-header-slot').boundingBox();
    expect(headerBox, `no header box at ${width}px`).not.toBeNull();
    expect(slotBox, `no slot box at ${width}px`).not.toBeNull();
    expect(slotBox!.x + slotBox!.width, `the slot reaches past the header line at ${width}px`).toBeLessThanOrEqual(headerBox!.x + headerBox!.width + 1);

    // THE RULE: the workspace strip wins the line. The strip's own space is
    // never smaller than the chat slot's — the slot shrinks and truncates
    // first, never the navigation.
    const stripBox = await page.getByTestId('workspace-header-entries').boundingBox();
    expect(stripBox, `no strip box at ${width}px`).not.toBeNull();
    expect(stripBox!.width, `the chat slot squeezed the workspace strip out at ${width}px`).toBeGreaterThanOrEqual(slotBox!.width);
  }

  // The long identity is TRUNCATED (clipped, not overflowing) at the narrowest
  // width, and the full text stays reachable through the title.
  const line = page.locator('[data-testid="feed-kind-label"] span[title]');
  await expect(line).toBeVisible();
  const truncation = await line.evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
  expect(truncation.scroll, 'the long identity is not truncated').toBeGreaterThan(truncation.client);
  expect(await line.getAttribute('title')).toContain(LONG_AGENT_NAME);

  expect(traffic.unknown, 'a missing API fixture is a backdrop failure').toEqual([]);
  expect(traffic.errors, 'a page crash is a backdrop failure').toEqual([]);
});

test('the helper breadcrumb stays on ONE line, caret included', async ({ page }) => {
  const traffic = await installSidebarBackdropEdges(page, { extraSessions: [helper] });
  await page.setViewportSize({ width: 900, height: 800 });
  await page.goto(`/#/workspaces/${backdropWorkspace.id}/chat?open=${HELPER_ID}`);

  const label = page.getByTestId('feed-kind-label');
  await expect(label).toBeVisible();
  await expect(label).toContainText('/');

  // The caret and the breadcrumb text share one line. The TEXT's own box is
  // measured through a Range over its text node — measuring the wrapper span
  // would pass even when the caret has broken the line onto its own row.
  const boxes = await label.evaluate((el) => {
    const caret = el.querySelector('svg');
    const span = el.querySelector('span');
    const textNode = span?.lastChild;
    if (!caret || !span || !textNode || textNode.nodeType !== Node.TEXT_NODE) return null;
    const range = document.createRange();
    range.selectNode(textNode);
    const caretBox = caret.getBoundingClientRect();
    const textBox = range.getBoundingClientRect();
    return {
      caretTop: caretBox.top, caretBottom: caretBox.bottom,
      textTop: textBox.top, textBottom: textBox.bottom,
    };
  });
  expect(boxes, 'the breadcrumb has no caret').not.toBeNull();
  // Same line: the caret's vertical band overlaps the text's band.
  expect(boxes!.caretTop, 'the caret sits above the breadcrumb text').toBeLessThan(boxes!.textBottom);
  expect(boxes!.caretBottom, 'the caret sits below the breadcrumb text').toBeGreaterThan(boxes!.textTop);

  expect(traffic.unknown, 'a missing API fixture is a backdrop failure').toEqual([]);
  expect(traffic.errors, 'a page crash is a backdrop failure').toEqual([]);
});
