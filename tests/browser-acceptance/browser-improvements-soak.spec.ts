import { test } from '@playwright/test';
import { runBrowserInputProbe } from '../e2e/fixtures/browser-input-probe';

test.describe.configure({ retries: 0 });
test('isolated browser remains idle 10min then preserves exact mixed input for10min', async ({ page }, testInfo) => {
  await runBrowserInputProbe(page, testInfo, 'soak');
});
