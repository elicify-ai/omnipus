/**
 * preview-pdf.spec.ts — PLACEHOLDER (ADR-067, Stage 1)
 *
 * HEADLESS, and that is the point. PDF.js draws into a canvas, and a canvas renders
 * identically headless, so §0's narrowed rule leaves this file on the DEFAULT
 * project — it is matched by no isolation or headed project.
 *
 * It therefore doubles as the live proof that the default project still picks up a
 * newly added spec: `playwright test --list` must show it under `default`. If a
 * future edit turns the default project's `testIgnore` into an allow-list, this
 * file is the first thing that stops running.
 *
 * The real tests land in Wave 3 (ADR-067 §13.1):
 *   61 PDF.js loads only when a PDF is opened — two ORDERED phases in one session.
 *      Phase 1 (open a `.md`) asserts zero requests match the PDF.js chunk; phase 2
 *      (open a `.pdf`) asserts the chunk IS requested and the canvas renders, which
 *      is what stops phase 1 passing merely because the app never loaded.
 *   75 form fields are inert — nothing appears, the file is BYTE-IDENTICAL on disk,
 *      and no write request reached the gateway. The disk-hash and server-side
 *      halves are what stop a UI-only fix passing.
 */
import { test } from '@playwright/test';

test.describe('PDF.js preview — PLACEHOLDER, real tests land in ADR-067 Wave 3', () => {
  test.skip(true, 'ADR-067 Stage 1 not implemented yet; see this file header for tests 61/75.');

  test('placeholder — proves the default project still matches newly added specs', () => {
    // Intentionally empty; takes no `page` fixture.
  });
});
