/**
 * connectors-programs.spec.ts — the program switch on the Connectors page.
 *
 * Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
 * Traces to: E2E row 37 of the Test Implementation Order — S-148 (program
 * switch on the Connectors page) and FR-009 (the per-connection
 * programs_allowed switch, default off).
 *
 * RED scope (slice S1): exists and must only `--list` cleanly (dispatch
 * brief: browser/E2E rows need to exist and pass --list). The body executes
 * for the first time in CI at GREEN.
 */
import { expect, test } from '@playwright/test';

test.describe('Connectors page — program switch', () => {
  test("S148: a connector's programs switch is off by default and turns on per connection", async ({ page }) => {
    // Given GitHub on for Dev and the telegram connection never changed,
    // the Connectors page shows the programs switch off; switching it on
    // persists programs_allowed: true for that connection only, and the
    // withheld-by-channel notice disappears for the next telegram turn.
    test.info().annotations.push({ description: 'executes at GREEN (CI)', type: 'red-scope' });
  });
});
