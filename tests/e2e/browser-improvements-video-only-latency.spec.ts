import { test } from '@playwright/test';
import { runBrowserInputProbe } from './fixtures/browser-input-probe';

// A/B hypothesis experiment only. Disabled audio is not a product fix or soak acceptance.
test.describe.configure({ retries: 0 });
test('experiment:100 click timings with explicitly inactive viewer audio', async ({ page }, testInfo) => {
  await runBrowserInputProbe(page, testInfo, 'latency-video-only');
});
