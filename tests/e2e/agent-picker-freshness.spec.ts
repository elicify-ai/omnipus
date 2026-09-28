/**
 * agent-picker-freshness.spec.ts — row 55 (FAILURES.md) bug A.
 *
 * browser-live-video.spec.ts intermittently timed out (up to its full
 * 720_000ms budget) waiting for a freshly-created "Browser Probe <nonce>"
 * agent to appear in the AgentPicker dropdown, immediately after creating
 * it via a raw `POST /api/v1/agents` call
 * (fixtures/browser-input-probe.ts::createBrowserProbeAgent). Confirmed on
 * PR #940 (run 36341847219, 2026-09-27) and on release/v0.1.1 itself (run
 * 36396042860 @ 8402634e7, 2026-09-28) — the exact same `selectAgent` call
 * resolved in ~14s on a healthy run and never resolved in 12 minutes on
 * two others.
 *
 * ROOT CAUSE (not a real-user-reachable bug): the AgentPicker's `['agents']`
 * react-query cache (src/hooks/useChatAgents.ts) is prefetched once per page
 * load by AppShell.tsx and has no websocket-driven invalidation for agent
 * creation (grep confirms no `agent_created`-shaped frame exists anywhere in
 * pkg/gateway or contracts/). The ONLY real-product path that creates an
 * agent — src/components/agents/CreateAgentModal.tsx — calls
 * `queryClient.invalidateQueries({ queryKey: ['agents'] })` right after a
 * successful create, so a real user's own tab always sees their own new
 * agent immediately. browser-live-video.spec.ts's `beforeEach` navigates to
 * "/" ONCE (populating the cache with the agents that existed at that
 * moment), then creates the probe agent via raw REST — bypassing the modal
 * and its invalidation — and immediately tries to select it from the
 * ALREADY-STALE cache. Compare fixtures/conformance-helpers.ts's
 * `startFreshChatWithAgent`, which navigates AFTER creating its agent
 * (via `createMainAgent`) — the safe, already-used pattern this spec did
 * not follow.
 *
 * This test isolates the gap with a short timeout so it fails in seconds,
 * not minutes, and does none of browser-live-video.spec.ts's expensive
 * WebRTC/video work.
 */

import { expect } from "@playwright/test";
import { test } from "./fixtures/console-errors";
import { agentPicker } from "./fixtures/selectors";
import { createBrowserProbeAgent } from "./fixtures/browser-input-probe";

test.describe("agent picker freshness after a raw-REST agent creation (row 55 bug A)", () => {
  test("a newly created agent is NOT visible in the picker without a reload — the caching gap", async ({
    page,
  }) => {
    await page.goto("/");

    const probeAgentName = await createBrowserProbeAgent(
      page,
      Date.now() % 65_536,
    );
    const menuitemPattern = new RegExp(
      probeAgentName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"),
      "i",
    );

    const picker = agentPicker(page);
    await picker.waitFor({ state: "visible", timeout: 15_000 });
    await picker.click();

    // Short timeout deliberately: this proves the gap in seconds. Production
    // code (selectAgent) waits far longer and eats the whole test budget.
    const menuitem = page.getByRole("menuitem", { name: menuitemPattern });
    await expect(
      menuitem,
      `"${probeAgentName}" should be visible in the agent picker within 5s of creation ` +
        "without a page reload; if this times out, the ['agents'] react-query cache " +
        "populated by the pre-creation navigation is stale and nothing invalidated it " +
        "(see this file's header comment for the root cause).",
    ).toBeVisible({ timeout: 5_000 });
  });

  test("the SAME agent IS visible after a reload — proves the fix's shape", async ({
    page,
  }) => {
    await page.goto("/");

    const probeAgentName = await createBrowserProbeAgent(
      page,
      Date.now() % 65_536,
    );

    // The fix for browser-live-video.spec.ts: reload before selecting, the
    // same order fixtures/conformance-helpers.ts's startFreshChatWithAgent
    // already uses safely.
    await page.goto("/");

    const menuitemPattern = new RegExp(
      probeAgentName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"),
      "i",
    );
    const picker = agentPicker(page);
    await picker.waitFor({ state: "visible", timeout: 15_000 });
    await picker.click();
    await expect(
      page.getByRole("menuitem", { name: menuitemPattern }),
    ).toBeVisible({ timeout: 5_000 });
  });
});
