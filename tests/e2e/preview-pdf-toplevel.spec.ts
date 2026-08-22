/**
 * preview-pdf-toplevel.spec.ts — PLACEHOLDER (ADR-067, Stage 1)
 *
 * HEADED. One of exactly two cases §0's derivation earns headed mode: headless
 * Chromium turns every top-level `.pdf` navigation into a DOWNLOAD, so "no script
 * ran" would be true for the wrong reason and the test would pass while proving
 * nothing.
 *
 * The real tests land in Wave 3 (ADR-067 §13.1 test 58,
 * TestTypeConfusion_HtmlNamedPdfDoesNotExecute) and need THREE controls in one run:
 *   - the HTML-payload-named-`.pdf` case: served `application/pdf`, `nosniff`
 *     present, no script runs, nothing reaches an external origin;
 *   - a POSITIVE control (same payload served `text/html`) proving the detection
 *     is not blind;
 *   - a genuine PDF served top-level that DOES render, or the result is
 *     inconclusive rather than a pass.
 *
 * Runs at `retries: 0` — "the script did not execute" is not a property a fourth
 * attempt establishes.
 */
import { test } from '@playwright/test';

test.describe('top-level .pdf type confusion — PLACEHOLDER, real tests land in ADR-067 Wave 3', () => {
  test.skip(true, 'ADR-067 Stage 1 not implemented yet; see this file header for test 58 and its three controls.');

  test('placeholder — keeps the headed project and the shard plan checkable', () => {
    // Intentionally empty; takes no `page` fixture, so it launches no headed browser.
  });
});
