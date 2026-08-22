/**
 * preview-isolation.spec.ts — PLACEHOLDER (ADR-067, Stage 1)
 *
 * This file exists NOW so that playwright.config.ts's isolation matrix is
 * verifiable now: the three `isolation-*` projects scope their `testMatch` to
 * this file and `preview-bundle.spec.ts`, and `scripts/e2e-shards.sh check`
 * requires every spec on disk to be assigned to a shard. A config that points
 * at files which do not exist is a config nobody can check.
 *
 * The real tests land in Wave 3 (ADR-067 §13.1):
 *   10  E2E_PreviewIsolation_TopLevelNavigation — `document.cookie` THROWS
 *       (never "returns empty" — that also passes when the page failed to load)
 *       and `window.origin === "null"`. Positive control required in the same run.
 *   11  E2E_PreviewIsolation_NetworkBlocked — all seven egress vectors, asserted
 *       by SERVER-observed request arrival, never by console text.
 *   12  E2E_PreviewIsolation_BrowserMatrix — 10 and 11 and their positive
 *       controls, on Chromium, Firefox and WebKit at retries: 0.
 *   95  E2E_PreviewFrame_SandboxComposition
 *   110 E2E_PreviewSameOrigin_ReachableButUnauthenticated
 *   111 E2E_PreviewCannotFrameTheSpa
 *   122 an SVG subresource renders and stays inert
 *
 * Skipped rather than absent, and deliberately loud in its title: a skipped
 * placeholder is visible in every report, whereas a missing file is only visible
 * to whoever remembers it was supposed to exist.
 */
import { test } from '@playwright/test';

test.describe('preview isolation — PLACEHOLDER, real tests land in ADR-067 Wave 3', () => {
  test.skip(true, 'ADR-067 Stage 1 not implemented yet; see this file header for tests 10/11/12/95/110/111/122.');

  test('placeholder — keeps the isolation projects and the shard plan checkable', () => {
    // Intentionally empty. Takes no `page` fixture, so it cannot launch a browser
    // even if the skip modifier above were removed.
  });
});
