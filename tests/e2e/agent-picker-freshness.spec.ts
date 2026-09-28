/**
 * agent-picker-freshness.spec.ts — row 55 (FAILURES.md) bug A, AND the
 * acceptance test for GitHub issue #1009.
 *
 * ORIGIN (bug A, 2026-09-28): browser-live-video.spec.ts intermittently
 * timed out (up to its full 720_000ms budget) waiting for a freshly-created
 * "Browser Probe <nonce>" agent to appear in the AgentPicker dropdown,
 * immediately after creating it via a raw `POST /api/v1/agents` call
 * (fixtures/browser-input-probe.ts::createBrowserProbeAgent). Confirmed on
 * PR #940 (run 36341847219, 2026-09-27) and on release/v0.1.1 itself (run
 * 36396042860 @ 8402634e7, 2026-09-28) — the exact same `selectAgent` call
 * resolved in ~14s on a healthy run and never resolved in 12 minutes on
 * two others. That symptom was fixed test-side (browser-live-video.spec.ts
 * now reloads before selecting), and THIS file was added as an isolated,
 * fast-failing regression test for the underlying caching gap — at the
 * time believed test-only.
 *
 * UPDATE (issue #1009, 2026-09-28): the "not a real-user-reachable bug"
 * verdict below was WRONG — corrected here rather than left standing. Two
 * confirmed real-user paths hit the exact same gap: (1) the `create_agent`
 * tool (pkg/sysagent/tools/agent.go::AgentCreateTool) lets an agent create
 * another agent mid-conversation, entirely bypassing CreateAgentModal.tsx,
 * so the very tab the user is watching never saw its own new agent; (2) any
 * other open tab/window, regardless of which path created the agent. The
 * PRODUCT fix (not just a test fixture fix) landed for #1009: a new
 * `agent_created` websocket frame (contracts/components/schemas/
 * AgentCreatedFrame.yaml), broadcast from BOTH agent-creation code paths
 * (pkg/gateway/rest_agents_create.go::createAgent and the
 * create_agent tool above), handled in src/store/chat/slices/frames.ts's
 * `case 'agent_created':` (invalidates `['agents']`), plus a defense-in-
 * depth pull-half hook (src/hooks/useAgentsCrossTabRefresh.ts, mirroring
 * D-107's useLibraryCrossTabRefresh.ts) for a dropped/reconnecting WS
 * connection. The test below is now that fix's real, end-to-end acceptance
 * test — its `toBeVisible({ timeout: 5_000 })` assertion is the ONE piece
 * of coverage in this fix that exercises the real WS wire format, the real
 * React Query cache, and the real timing together; everything else (unit
 * tests on both backend call sites, the frontend switch case, the hook)
 * only proves its own unit is wired correctly in isolation.
 *
 * ROOT CAUSE (still accurate, now closed by the #1009 fix above): the
 * AgentPicker's `['agents']` react-query cache (src/hooks/useChatAgents.ts)
 * is prefetched once per page load by AppShell.tsx and, before the #1009
 * fix, had no websocket-driven invalidation for agent creation. The ONLY
 * real-product path that creates an agent — src/components/agents/
 * CreateAgentModal.tsx — calls `queryClient.invalidateQueries({ queryKey:
 * ['agents'] })` right after a successful create, so a real user's own tab
 * always sees their own new agent immediately THROUGH THAT PATH; every
 * other path relied on the 30s global staleTime (src/lib/queryClient.ts)
 * elapsing, or a focus/visibility event, before #1009's push-half frame
 * closed the gap directly. browser-live-video.spec.ts's `beforeEach`
 * navigates to "/" ONCE (populating the cache with the agents that existed
 * at that moment), then creates the probe agent via raw REST — bypassing
 * the modal and its invalidation — and immediately tries to select it from
 * what would, pre-#1009, have been an already-stale cache. Compare
 * fixtures/conformance-helpers.ts's `startFreshChatWithAgent`, which
 * navigates AFTER creating its agent (via `createMainAgent`) — the safe,
 * already-used pattern this spec did not follow (and, post-#1009, no
 * longer strictly needs to for freshness, though the reload-based second
 * test below still independently proves that shape works too).
 *
 * This test isolates the gap with a short timeout so it fails in seconds,
 * not minutes, and does none of browser-live-video.spec.ts's expensive
 * WebRTC/video work.
 */

import { expect } from "@playwright/test";
import { test } from "./fixtures/console-errors";
import { agentPicker } from "./fixtures/selectors";
import { createBrowserProbeAgent } from "./fixtures/browser-input-probe";

test.describe("agent picker freshness after a raw-REST agent creation (row 55 bug A / issue #1009)", () => {
  test("a newly created agent becomes visible in the picker without a reload — the #1009 acceptance test", async ({
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

    // Short timeout deliberately: proves the #1009 fix (or the gap, if it
    // regresses) in seconds. Production code (selectAgent) waits far longer
    // and eats the whole test budget.
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
