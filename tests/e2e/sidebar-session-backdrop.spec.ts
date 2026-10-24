import { test, expect } from '@playwright/test';
import { backdropAgent, backdropTarget, backdropWorkspace, installSidebarBackdropEdges } from './fixtures/sidebar-session-backdrop';

// Founder Q81: either session-list entry point must dismiss the UNPINNED
// sidebar/backdrop, leave chat topmost, and deliver the first composer click.
// The expected outcomes are from that dispatch, never current implementation.
test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 900 } });
test.describe.configure({ retries: 0 });

for (const entry of ['Sessions', 'History'] as const) {
  test(`Q81 ${entry}: opening a session closes the unpinned sidebar and the first pointer click focuses the composer`, async ({ page }, info) => {
    const traffic = await installSidebarBackdropEdges(page);
    // The standalone inline chat has no screen header/hamburger. Start at a
    // real non-workspace header, as the existing sidebar navigation cases do.
    await page.goto('/#/skills', { waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { name: 'Skills & Tools', exact: true }), 'PREREQUISITE: real ScreenHeader renders').toBeVisible();
    await page.getByRole('button', { name: 'Show sidebar', exact: true }).click();
    const nav = page.locator('nav[aria-label="Main navigation"]');
    await expect(nav).toBeVisible();
    await expect(nav.getByRole('button', { name: 'Pin sidebar', exact: true }), 'PREREQUISITE: sidebar is genuinely unpinned').toHaveAttribute('aria-pressed', 'false');
    const backdrop = page.locator('div.absolute.inset-0.z-30[aria-hidden="true"]');
    await expect(backdrop, 'PREREQUISITE: instrument sees the real open-sidebar backdrop').toHaveCount(1);
    if (entry === 'Sessions') {
      await nav.getByRole('button', { name: 'Search sessions', exact: true }).click();
    } else {
      const expand = nav.getByRole('button', { name: `Show ${backdropWorkspace.name} agents`, exact: true });
      if (await expand.isVisible()) await expand.click();
      await nav.getByRole('button', { name: `More actions for ${backdropAgent.name}`, exact: true }).click();
      await page.getByRole('menuitem', { name: 'History', exact: true }).click();
    }
    const sessions = page.getByRole('dialog', { name: /^Sessions/ });
    await expect(sessions, 'PREREQUISITE: the requested session-list entry point opens').toBeVisible();
    if (entry === 'History') {
      await expect(sessions.getByRole('button', { name: 'Clear workspace filter', exact: true })).toBeVisible();
      await expect(sessions.getByRole('button', { name: 'Clear agent filter', exact: true })).toBeVisible();
    }
    await sessions.getByRole('button', { name: `Open ${backdropTarget.title}`, exact: true }).click();
    await expect(sessions).toHaveCount(0);
    const chat = page.locator(`[data-active-session-id="${backdropTarget.id}"]`);
    await expect(chat, 'PREREQUISITE: the selected real ChatScreen is active').toBeVisible();
    const composer = chat.getByTestId('chat-input');
    await expect(composer, 'PREREQUISITE: target attach completes before pointer judgment').toBeEnabled();
    expect(traffic.attaches, 'PREREQUISITE: real app sent the target attach over the controlled socket').toContain(backdropTarget.id);
    expect(traffic.unknown, 'PREREQUISITE: no missing API fixture is a backdrop failure').toEqual([]);
    expect(traffic.errors, 'PREREQUISITE: no page crash is a backdrop failure').toEqual([]);

    await expect.soft(page.locator('#sidebar-overlay-panel'), 'Q81: session selection must close the unpinned sidebar').toHaveCount(0);
    const hit = await chat.evaluate(root => {
      const box = root.getBoundingClientRect();
      const point = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
      const top = document.elementFromPoint(point.x, point.y);
      return { point, chat: { x: box.x, y: box.y, width: box.width, height: box.height },
        insideChat: top !== null && root.contains(top),
        isBackdrop: top !== null && top.closest('div.absolute.inset-0.z-30[aria-hidden="true"]') !== null,
        topElement: top?.outerHTML.slice(0, 800),
        drawnBackdropCount: document.querySelectorAll('div.absolute.inset-0.z-30[aria-hidden="true"]').length };
    });
    await info.attach('q81-before-pointer', { contentType: 'application/json', body: JSON.stringify({ entry, hit, traffic }, null, 2) });
    await info.attach('q81-before-pointer-screenshot', { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });
    expect.soft(hit.isBackdrop, 'Q81: elementFromPoint at chat center must NOT be the sidebar backdrop').toBe(false);
    expect.soft(hit.insideChat, 'Q81: elementFromPoint at chat center must be inside the chat').toBe(true);

    // Remove incidental autofocus as an alternate explanation. Do not focus,
    // force-click, click twice or dismiss the overlay before the actual click.
    await composer.evaluate(element => {
      element.blur();
      element.setAttribute('data-q81-pointer-hits', '0');
      element.addEventListener('pointerdown', () => element.setAttribute('data-q81-pointer-hits', '1'), { once: true });
    });
    expect(await composer.evaluate(element => document.activeElement === element), 'PREREQUISITE: autofocus cannot prove the pointer result').toBe(false);
    const box = await composer.boundingBox();
    expect(box, 'PREREQUISITE: drawn composer is required for a real pointer click').not.toBeNull();
    if (!box) throw new Error('PREREQUISITE: no drawn composer, not a backdrop RED');
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
    const pointer = await composer.evaluate(element => ({
      activeIsComposer: document.activeElement === element,
      activeElement: document.activeElement?.outerHTML.slice(0, 500),
      composerTag: element.tagName,
      pointerReached: element.getAttribute('data-q81-pointer-hits'),
    }));
    await info.attach('q81-pointer-result', { contentType: 'application/json', body: JSON.stringify({ entry, pointer }, null, 2) });
    await info.attach('q81-after-pointer-screenshot', { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });
    console.log('Q81_OBSERVATION ' + JSON.stringify({ entry, hit, pointer }));
    expect(pointer.composerTag, 'the focus oracle is the actual composer textarea').toBe('TEXTAREA');
    expect.soft(pointer.pointerReached, 'Q81: the FIRST real pointer click must reach the composer, not dismiss a stale backdrop').toBe('1');
    expect.soft(pointer.activeIsComposer, 'Q81: document.activeElement must be the composer textarea after the first pointer click').toBe(true);
  });
}
