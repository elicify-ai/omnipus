// held-turn-harness.ts — shared setup and settlement evidence for the
// deterministic held-stream cancellations (T21/T22/T23/T26, issue #1661).
// Extracted from cancel-cross-channel.spec.ts so each describe stays within
// the 240-line function budget (scripts/check-function-budget.sh); the
// helpers' behavior is unchanged.

import { expect, type Browser } from '@playwright/test';
import { GatewayProcess } from './gateway-process';
import { E2E_FALLBACK } from './e2e-model';
import type { HeldStreamProvider } from './held-stream-provider';

/** The isolated gateway on the scripted-provider seam + an authenticated
 * browser context aimed at it. Fails safe: a context failure stops the
 * already-started gateway instead of leaking the process. */
export async function startHeldTurnHarness(browser: Browser, provider: HeldStreamProvider) {
  const gateway = await GatewayProcess.start({ apiBase: provider.url, model: E2E_FALLBACK.model })
  try {
    const context = await browser.newContext({
      baseURL: gateway.baseURL, storageState: await gateway.browserStorageState(),
    })
    const page = await context.newPage()
    return { gateway, context, page }
  } catch (error) {
    await gateway.stop()
    throw error
  }
}

/** Post-cancel settlement: the REAL cancel closed the in-flight provider
 * stream. "Cancel does nothing" cannot pass this: a no-op cancel leaves the
 * stream open forever, the stop button never clears (original assertions)
 * and this evidence never reaches 1. */
export async function expectCancelClosedStream(provider: HeldStreamProvider, label: string): Promise<void> {
  await expect.poll(() => provider.evidence.streamsClosed, {
    timeout: 10_000, message: `${label}: the real cancel must close the in-flight provider stream`,
  }).toBe(1)
  expect(provider.evidence.released, `${label}: release() was never called — the cancel did this`).toBe(0)
}
