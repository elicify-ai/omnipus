/**
 * preview-bundle.spec.ts — PLACEHOLDER (ADR-067, Stage 1)
 *
 * Second of the two isolation spec files (ADR-067 §13.4 item 1). See
 * preview-isolation.spec.ts's header for why these placeholders exist.
 *
 * The real tests land in Wave 3 (ADR-067 §13.1):
 *   9  E2E_PreviewBundle_AllAssetsLoad — a real browser loads css + js + font +
 *      audio from a bundle through the token path.
 *   60 E2E_FontAppliesWithCorsHeader — asserted by RENDERED WIDTH.
 *      `document.fonts.status` is NOT the oracle: it reports "loaded" on failure.
 *   64 E2E_BundleLoadsViaTokenPath — against the real AUTHENTICATED gateway, not
 *      a static server; that gap is what hid FR-003a.
 *
 * Cross-engine because the CORS-webfont result was measured on Chromium ONLY
 * (spec §0) — Firefox and WebKit are unmeasured and this is where that gets fixed.
 */
import { test } from '@playwright/test';

test.describe('preview bundle subresources — PLACEHOLDER, real tests land in ADR-067 Wave 3', () => {
  test.skip(true, 'ADR-067 Stage 1 not implemented yet; see this file header for tests 9/60/64.');

  test('placeholder — keeps the isolation projects and the shard plan checkable', () => {
    // Intentionally empty; takes no `page` fixture.
  });
});
