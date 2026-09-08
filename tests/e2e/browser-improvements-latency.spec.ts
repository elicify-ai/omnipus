import { test } from '@playwright/test';
import { runBrowserInputProbe } from './fixtures/browser-input-probe';

// Diagnostic only: no ten-minute idle/mixed acceptance claim.
test.describe.configure({ retries: 0 });
test('diagnostic:100 clicks with outbound and receiver video timing', async ({ page }, testInfo) => {
  await runBrowserInputProbe(page, testInfo, 'latency');
});
