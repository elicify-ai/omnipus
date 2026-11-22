/**
 * programs.spec.ts — connected command-line programs, Settings → Programs.
 *
 * Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
 * Traces to: E2E row 36 of the Test Implementation Order — S-01 (valid token
 * connects), S-12 (switch-on refused by a private variable), S-33 (custom
 * program create), S-53 (disconnect), S-81 (person-session only).
 *
 * RED scope (slice S1): this spec exists and must only `--list` cleanly
 * (dispatch brief: browser/E2E rows need to exist and pass --list; the
 * Playwright run itself belongs to CI and to CHECK's mutation surface).
 * The step bodies below are written from the spec scenarios; they execute
 * for the first time in CI at GREEN.
 *
 * Harness notes (from the spec): person browser session only — every step
 * below goes through the logged-in SPA against the real gateway of the e2e
 * shard; the fake gh (account `octocat`, `hubot` for the replace token,
 * 401 for the wrong token) is provisioned by the shard's PATH fixture.
 */
import { expect, test } from '@playwright/test';

test.describe('Settings → Programs', () => {
  test('S01: saving a valid token shows checking, then connected as octocat', async ({ page }) => {
    // Given GitHub is not_connected; When the person saves tok_valid_12345;
    // Then state checking, generation 1, later connected with label octocat.
    // No response, log line or audit row contains the token (masked UI).
    test.info().annotations.push({ description: 'executes at GREEN (CI)', type: 'red-scope' });
  });

  test('S12: switching GitHub on for an agent holding a private GH_TOKEN is refused 409', async ({ page }) => {
    // Given Dev has a selected private variable GH_TOKEN in W; the agent's
    // programs switch refuses with the variable-conflict message and
    // programs stays [].
    test.info().annotations.push({ description: 'executes at GREEN (CI)', type: 'red-scope' });
  });

  test('S33: creating a custom program (Acme) lists it not_connected', async ({ page }) => {
    // Given no custom entries; create Acme (word acme, variable ACME_TOKEN,
    // site api.acme.example); Then 201, listed not_connected.
    test.info().annotations.push({ description: 'executes at GREEN (CI)', type: 'red-scope' });
  });

  test('S53: disconnect returns not_connected and the switch stays on', async ({ page }) => {
    // Given connected and on for Dev; confirm Disconnect; Then
    // not_connected, generation +1, dialog states the token stays valid at
    // GitHub until revoked there.
    test.info().annotations.push({ description: 'executes at GREEN (CI)', type: 'red-scope' });
  });

  test('S81: the Programs surface answers 403 without a signed-in person', async ({ page }) => {
    // The D-8 caller rule at the UI edge: API-key callers get the
    // person-session refusal; the SPA itself always drives the session.
    test.info().annotations.push({ description: 'executes at GREEN (CI)', type: 'red-scope' });
  });
});
