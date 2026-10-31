/**
 * effort.spec.ts — T-E1 for the effort-levels feature (train T7).
 *
 * Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
 * Covers: S-07 (chip present/offered levels), S-13 (pre-send choice rides the
 * first message and survives refresh), S-21 (history reload shows the saved
 * answer's effort note), S-36 (/effort exists as a command surface on web is
 * out of scope here — S-36's messenger contract is covered by the Go command
 * tests; here the web-composer local branch is S-41), S-41 (/effort max on web
 * sets the conversation chip locally), S-95 (an older answer fetched by
 * scrolling up shows its note and thinking figure).
 *
 * RED state (test author's note, do not weaken): the effort picker, the
 * served-catalog reasoning objects and the history effort fields do not exist
 * at this branch yet. The RED proof recorded for this file is Playwright
 * collection (--list, saved under logs/effort-red/); the runtime assertions
 * below are the GREEN acceptance runs (CI). They are written against the
 * spec's exact strings and testids so they fail, not skip, until the feature
 * lands.
 *
 * Approach: deterministic route stubs for the providers catalog (reasoning
 * objects per D3) and the session history endpoints, mirroring about.spec.ts.
 */

import { expect, test } from '@playwright/test';

const CATALOG_MODELS = [
  {
    id: 'deepseek/deepseek-v4.1-flash',
    reasoning: { levels: ['none', 'low', 'high', 'max'], switch: true },
  },
  {
    id: 'google/gemini-2.5-flash',
    reasoning: { levels: ['none', 'low', 'high', 'max'], switch: true },
  },
];

async function stubCatalog(page: import('@playwright/test').Page) {
  await page.route('**/api/v1/providers/catalog*', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ providers: [{ id: 'openrouter', models: CATALOG_MODELS }] }),
    });
  });
}

test('S-07: the model with offered levels shows the effort chip with Off default', async ({ page }) => {
  await stubCatalog(page);
  await page.goto('/#/chat');
  const chip = page.getByTestId('effort-chip');
  await expect(chip).toBeVisible();
  await expect(chip).toContainText(/off/i);
});

test('S-13: a choice made before the first send rides the first message and survives refresh', async ({ page }) => {
  await stubCatalog(page);
  let lastBody: Record<string, unknown> = {};
  await page.route('**/api/v1/chat', async (route) => {
    lastBody = JSON.parse(route.request().postData() ?? '{}');
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ id: 't1' }) });
  });
  await page.goto('/#/chat');
  await page.getByTestId('effort-chip').click();
  await page.getByRole('menuitem', { name: 'High' }).click();
  await page.reload();
  await page.getByTestId('effort-chip').click();
  await expect(page.getByRole('menuitem', { name: 'High' })).toBeChecked();
  await page.keyboard.type('hello');
  await page.keyboard.press('Enter');
  expect(lastBody).toHaveProperty('reasoning_effort', 'high');
});

test('S-21: reloading the chat shows the saved answer with its effort note', async ({ page }) => {
  await stubCatalog(page);
  await page.route('**/api/v1/sessions/*/messages*', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        messages: [
          { role: 'user', content: 'hello' },
          {
            role: 'assistant',
            content: 'Done.',
            effort: { chosen: 'medium', sent: 'low', reason: 'mapped', provider: 'openrouter', model: 'deepseek/deepseek-v4.1-flash' },
            turn_reasoning_tokens: 120,
          },
        ],
      }),
    });
  });
  await page.goto('/#/chat');
  await expect(page.getByText("Effort set to Low. deepseek/deepseek-v4.1-flash doesn't offer Medium.")).toBeVisible();
});

test('S-41: /effort max in the web composer sets the conversation chip locally', async ({ page }) => {
  await stubCatalog(page);
  let writes = 0;
  await page.route('**/api/v1/agents/**', async (route) => {
    if (route.request().method() !== 'GET') {
      writes += 1;
    }
    await route.continue();
  });
  await page.goto('/#/chat');
  await page.getByTestId('chat-composer').fill('/effort max');
  await page.keyboard.press('Enter');
  const chip = page.getByTestId('effort-chip');
  await expect(chip).toContainText(/max, set for this conversation/i);
  expect(writes).toBe(0);
});

test('S-95: an older answer fetched by scrolling up shows its note and thinking figure', async ({ page }) => {
  await stubCatalog(page);
  let olderFetched = false;
  await page.route('**/api/v1/sessions/*/messages*', async (route) => {
    const url = new URL(route.request().url());
    const older = url.searchParams.get('before') != null;
    if (older) {
      olderFetched = true;
    }
    const messages = older
      ? [
          {
            role: 'assistant',
            content: 'Old answer.',
            effort: { chosen: 'medium', sent: 'low', reason: 'mapped', provider: 'openrouter', model: 'deepseek/deepseek-v4.1-flash' },
            turn_reasoning_tokens: 120,
          },
        ]
      : [{ role: 'user', content: 'newer turn' }];
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ messages }),
    });
  });
  await page.goto('/#/chat');
  await page.getByTestId('history-scroll').evaluate((el) => {
    el.scrollTop = 0;
  });
  await expect.poll(() => olderFetched).toBe(true);
  await expect(page.getByText("Sent as Low: deepseek/deepseek-v4.1-flash doesn't offer Medium.")).toBeVisible();
  await expect(page.getByText('Thinking: 120 tokens (reported by the provider)')).toBeVisible();
});
