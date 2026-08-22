/**
 * preview-pdf-viewer-control.spec.ts — PLACEHOLDER (ADR-067, Stage 1)
 *
 * HEADED. The second of the two cases §0's derivation earns headed mode: the
 * browser-viewer negative control. D15 rev 3 removed the browser's own PDF viewer
 * from the Library path — PDF.js draws into a canvas — so this control exists to
 * prove the browser viewer is NOT what rendered the document. That claim is about
 * the browser's own PDF handling, which headless Chromium does not have, so it can
 * only be measured headed.
 *
 * The real test lands in Wave 3 (ADR-067 §13.1 test 57 / §13.4 item 4). Note §0's
 * correction: an earlier "PDF fails under sandbox everywhere" reading was partly a
 * headless artefact — headed, a TOP-LEVEL pdf renders even sandboxed, while a
 * FRAMED one is blocked. The Library case is framed, so the conclusion held but the
 * reasoning did not.
 *
 * Runs at `retries: 0`, for the same reason as the top-level case.
 */
import { test } from '@playwright/test';

test.describe('browser-viewer negative control — PLACEHOLDER, real tests land in ADR-067 Wave 3', () => {
  test.skip(true, 'ADR-067 Stage 1 not implemented yet; see this file header for test 57 / §13.4 item 4.');

  test('placeholder — keeps the headed project and the shard plan checkable', () => {
    // Intentionally empty; takes no `page` fixture, so it launches no headed browser.
  });
});
