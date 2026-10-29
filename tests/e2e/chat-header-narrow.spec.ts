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
//
// The readiness budget is 30s, the same budget this check always had. A run
// that misses it is a FINDING to report with its log, not a budget to raise:
// widening the wait would accept a slow start instead of measuring it.
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

// How long the shell may take to render before the run is a finding.
const READINESS_TIMEOUT_MS = 30_000;

// The touch minimum the design system reserves for a coarse pointer
// (--target-touch-minimum, 44px).
const TOUCH_MINIMUM_PX = 44;

// Where the browser remembers which chat a workspace last showed
// (src/store/session.ts::PERSISTED_SESSION_BY_WORKSPACE_KEY). The workspace
// entry restores the pointer from it — this is how "the chat this browser
// remembers" is seeded without a real first visit.
const REMEMBERED_SESSION_KEY = 'omnipus.sessionByWorkspace.v1';

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
    await page.goto(`/#/workspaces/${backdropWorkspace.id}/chat`, { waitUntil: 'domcontentloaded' });

    const header = page.getByTestId('workspace-top-bar');
    await expect(header).toBeVisible({ timeout: READINESS_TIMEOUT_MS });

    // READINESS: the header appears before the roster and the session do.
    // Measuring an empty slot would prove nothing, so wait for the slot's
    // REAL content — the full identity line and the participant control —
    // before every measurement.
    const line = page.locator('[data-testid="feed-kind-label"] span[title]');
    await expect(line, `the chat identity never appeared at ${width}px`).toBeVisible({ timeout: READINESS_TIMEOUT_MS });
    await expect(page.getByRole('button', { name: 'Add participant' }), `the participant control never appeared at ${width}px`).toBeVisible({ timeout: READINESS_TIMEOUT_MS });
    expect(await line.getAttribute('title'), `the identity is not the full one at ${width}px`).toContain(LONG_AGENT_NAME);

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

    // The long identity is TRUNCATED (clipped, not overflowing), and the full
    // text stays reachable through the title.
    const truncation = await line.evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
    expect(truncation.scroll, `the long identity is not truncated at ${width}px`).toBeGreaterThan(truncation.client);
  }

  expect(traffic.unknown, 'a missing API fixture is a backdrop failure').toEqual([]);
  expect(traffic.errors, 'a page crash is a backdrop failure').toEqual([]);
});

test('the failed-roster warning fits the capped slot and never overlaps the participant control', async ({ page }) => {
  const traffic = await installSidebarBackdropEdges(page, { agentName: LONG_AGENT_NAME, agentsError: true });
  await page.setViewportSize({ width: 420, height: 800 });
  // The deep link attaches the chat and writes its descriptor; the workspace
  // entry then cannot resolve the team (the roster read fails), which is the
  // state the honest "agent names unavailable" warning exists for.
  await page.goto(`/#/sessions/${backdropMain.id}`, { waitUntil: 'domcontentloaded' });

  // The roster failed, so the header shows the honest warning instead of the
  // agent's name. The warning must be on screen before anything is measured.
  const warning = page.getByTestId('feed-kind-warning');
  await expect(warning, 'the failed-roster warning never appeared').toBeVisible({ timeout: READINESS_TIMEOUT_MS });

  // The slot is capped, so its CONTENT must fit the cap: a warning that
  // overflowed would push the line sideways.
  const slot = page.getByTestId('chat-header-slot');
  const overflow = await slot.evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
  expect(overflow.scroll, 'the warning overflows the capped slot').toBeLessThanOrEqual(overflow.client + 1);

  // The warning stays INSIDE the header line.
  const headerBox = await page.getByTestId('workspace-top-bar').boundingBox();
  const warningBox = await warning.boundingBox();
  expect(warningBox, 'no warning box').not.toBeNull();
  expect(warningBox!.x + warningBox!.width, 'the warning reaches past the header line').toBeLessThanOrEqual(headerBox!.x + headerBox!.width + 1);

  // The identity's title carries the whole line, warning included.
  const identity = page.locator('[data-testid="feed-kind-label"] span[title]').first();
  expect(await identity.getAttribute('title')).toContain('agent names unavailable');

  // The participant control is NOT offered in this state: the workspace
  // entry failed, so there is no main chat of THIS workspace to start a group
  // from. That is why the warning and the control can never share the slot —
  // and why the slot's own cap must still hold the warning.
  await expect(page.getByRole('button', { name: 'Add participant' }), 'the control is offered while the entry failed').toHaveCount(0);

  // The explanation is reachable BY KEYBOARD and BY POINTER in a real
  // browser: the glyph is a named, focusable trigger, hovering or focusing it
  // opens the explanation, and Escape dismisses it again.
  expect(await warning.getAttribute('aria-label')).toBe('Agent names unavailable');
  await warning.hover();
  const bubble = page.getByRole('tooltip');
  await expect(bubble, 'the warning never explained itself on hover').toBeVisible();
  await expect(bubble, 'the explanation does not carry the recovery step').toContainText('Agent names could not be loaded');
  await expect(bubble, 'the explanation does not carry the recovery step').toContainText('Reload the page');
  await page.keyboard.press('Escape');
  await expect(bubble, 'Escape did not dismiss the explanation').toHaveCount(0);
  await warning.focus();
  await expect(bubble, 'the warning never explained itself on focus').toBeVisible();
  await page.keyboard.press('Escape');
  await expect(bubble, 'Escape did not dismiss the explanation').toHaveCount(0);

  expect(traffic.unknown, 'a missing API fixture is a backdrop failure').toEqual([]);
  expect(traffic.errors, 'a page crash is a backdrop failure').toEqual([]);
});

// The two controls that CAN share the header line: the failed-roster warning
// and "Add participant". The exact-restore path makes them coexist — this
// browser remembers the workspace's main chat, the session listing still
// contains it, and the roster read fails.
// workspaceEntryFlow.ts::loadInputs keeps the exact restore alive when the
// TEAM could not be loaded (transportFailed = sessionsFailed || (teamFailed
// && !exactAvailable)), so the entry is the exact main chat with sending
// enabled: the participant control is offered while the header still warns
// that the names are unavailable.
test.describe('at a coarse pointer', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 420, height: 800 } });

  test('the failed-roster warning and "Add participant" are separate touch targets', async ({ page }) => {
    // This browser remembers the workspace's main chat: the pointer the
    // workspace entry restores from.
    await page.addInitScript(([key, value]) => {
      window.localStorage.setItem(key as string, value as string);
    }, [REMEMBERED_SESSION_KEY, JSON.stringify({
      [backdropWorkspace.id]: {
        id: backdropMain.id,
        type: 'main',
        title: backdropMain.title,
        agentId: backdropMain.agent_id,
      },
    })]);

    const traffic = await installSidebarBackdropEdges(page, { agentsError: true });
    await page.goto(`/#/workspaces/${backdropWorkspace.id}/chat`, { waitUntil: 'domcontentloaded' });

    const warning = page.getByTestId('feed-kind-warning');
    const participant = page.getByRole('button', { name: 'Add participant' });
    await expect(warning, 'the failed-roster warning never appeared').toBeVisible({ timeout: READINESS_TIMEOUT_MS });
    await expect(participant, 'the participant control never appeared').toBeVisible({ timeout: READINESS_TIMEOUT_MS });

    // THE INSTRUMENT: the touch sizing only applies to a coarse pointer. If
    // the emulation did not take effect, this case would measure a mouse
    // pointer and pass for the wrong reason.
    expect(
      await page.evaluate(() => matchMedia('(pointer: coarse)').matches),
      'the coarse-pointer emulation did not take effect',
    ).toBe(true);

    // The warning is a touch-sized target — the token minimum, not the 14px
    // glyph it draws.
    const warningBox = await warning.boundingBox();
    expect(warningBox, 'the warning has no box').not.toBeNull();
    expect(warningBox!.width, 'the warning is not a touch-sized target').toBeGreaterThanOrEqual(TOUCH_MINIMUM_PX);
    expect(warningBox!.height, 'the warning is not a touch-sized target').toBeGreaterThanOrEqual(TOUCH_MINIMUM_PX);

    // ... and the two targets never overlap: a tap meant for one control
    // cannot reach the other.
    const participantBox = await participant.boundingBox();
    expect(participantBox, 'the participant control has no box').not.toBeNull();
    expect(
      warningBox!.x + warningBox!.width,
      'the warning reaches into the participant control',
    ).toBeLessThanOrEqual(participantBox!.x);

    // The slot's cap still holds with the bigger touch box: the controls fit
    // inside it and the identity truncates instead of pushing the line
    // sideways.
    const slotOverflow = await page.getByTestId('chat-header-slot').evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
    expect(slotOverflow.scroll, 'the touch-sized warning overflows the capped slot').toBeLessThanOrEqual(slotOverflow.client + 1);

    // A tap on the warning opens ONLY the explanation — never the picker.
    await warning.tap({ position: { x: warningBox!.width - 2, y: warningBox!.height / 2 } });
    const explanation = page.getByRole('tooltip').filter({ hasText: 'Agent names could not be loaded' });
    await expect(explanation, 'the warning never explained itself on a tap').toBeVisible();
    await expect(page.getByTestId('add-participant-dialog'), 'the warning tap opened the participant picker').toHaveCount(0);
    await page.keyboard.press('Escape');
    await expect(explanation, 'Escape did not dismiss the explanation').toHaveCount(0);

    // A tap on the participant control opens ONLY the picker — never the
    // explanation.
    await participant.tap({ position: { x: 2, y: participantBox!.height / 2 } });
    await expect(page.getByTestId('add-participant-dialog'), 'the participant picker never opened').toBeVisible();
    await expect(explanation, 'the participant tap opened the warning explanation').toHaveCount(0);

    expect(traffic.unknown, 'a missing API fixture is a backdrop failure').toEqual([]);
    expect(traffic.errors, 'a page crash is a backdrop failure').toEqual([]);
  });
});

test('the helper breadcrumb stays on ONE line, caret included', async ({ page }) => {
  const traffic = await installSidebarBackdropEdges(page, { extraSessions: [helper] });
  await page.setViewportSize({ width: 900, height: 800 });
  await page.goto(`/#/workspaces/${backdropWorkspace.id}/chat?open=${HELPER_ID}`, { waitUntil: 'domcontentloaded' });

  const label = page.getByTestId('feed-kind-label');
  await expect(label).toBeVisible({ timeout: READINESS_TIMEOUT_MS });
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
