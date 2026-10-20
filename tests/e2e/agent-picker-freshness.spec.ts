/**
 * agent-picker-freshness.spec.ts — row 55 (FAILURES.md) bug A, AND the
 * acceptance test for GitHub issue #1009, PORTED to the navigation redesign's
 * observation surface (2026-10-11).
 *
 * PORT NOTE — why the surface changed: this spec used to observe the fresh
 * agent in the composer AgentPicker dropdown. That picker is a RETIRED
 * surface — deleted outright by nav commit e84ac0532 "remove the composer
 * agent picker" (src/components/chat/ChatScreen.pickerRemoval.test.tsx pins
 * its absence; session-core-spec FR-029 removes in-chat agent switching).
 * The #1009 guarantee it accepted is NOT retired: an agent created by ANY
 * path (the `create_agent` tool, another tab, raw REST) must become visible
 * in the already-open UI without a reload. That guarantee now lives in the
 * SIDEBAR roster — the workspace's agent list the user actually reads
 * (src/components/layout/sidebar/WorkspaceAgentList.tsx) — so both tests
 * observe (and, via selectAgent, reach) the new agent there instead.
 *
 * ORIGIN (bug A, 2026-09-28): browser-live-video.spec.ts intermittently
 * timed out waiting for a freshly-created "Browser Probe <nonce>" agent to
 * appear in a cached roster immediately after a raw `POST /api/v1/agents`
 * call (fixtures/browser-input-probe.ts::createBrowserProbeAgent). The
 * underlying caching gap and its PRODUCT fix (#1009) are unchanged: a new
 * `agent_created` websocket frame (contracts/components/schemas/
 * AgentCreatedFrame.yaml) broadcast from BOTH agent-creation code paths
 * (pkg/gateway/rest_agents_create.go::createAgent and the create_agent tool),
 * handled in src/store/chat/slices/frames.ts `case 'agent_created':`
 * (invalidates `['agents']`), plus the defense-in-depth pull-half hook
 * (src/hooks/useAgentsCrossTabRefresh.ts).
 *
 * TIMEOUT RE-DERIVATION (honest, not a weakening): the old assertion was
 * `toBeVisible({ timeout: 5_000 })` because the picker read ONLY the
 * `['agents']` cache, which the WS frame invalidates within ms. The sidebar
 * roster needs TWO inputs (workspaceMains = memberRoster(workspace, agents),
 * src/components/layout/sidebar/workspaceRoster.ts): the agent object from
 * `['agents']` — still WS-invalidated, near-instant — AND the workspace's
 * core_team membership, which the raw REST PUT (below) writes with NO
 * websocket broadcast: nothing invalidates the SPA's `['workspaces']` query
 * on that PUT, so the row can only appear when the sidebar's workspaces poll
 * next fires (refetchInterval: 30_000, Sidebar.tsx:171-174). The no-reload
 * budget is therefore 45s — one poll interval plus load margin — and the
 * first test below remains the fastest no-reload acceptance in the suite for
 * the frame's OTHER half. (Coverage note for team-lead: a raw membership PUT
 * reaching the open sidebar within a bounded time is NOT currently
 * guaranteed by any invalidation — only by the poll. If that 30s bound ever
 * matters to a user, the fix is a `workspace_changed` frame or an
 * invalidation on the PUT path — production code, hence reported not
 * written.)
 *
 * Both tests create the probe the same way createBrowserProbeAgent always
 * has: POST /api/v1/agents, tool policies, then PUT the default workspace's
 * core_team — so the roster intersection (memberRoster) has both halves.
 */

import { expect } from "@playwright/test";
import { test } from "./fixtures/console-errors";
import {
  assistantMessages,
  selectAgent,
  agentRowGroup,
  DEFAULT_WORKSPACE_NAME,
} from "./fixtures/selectors";
import { createBrowserProbeAgent } from "./fixtures/browser-input-probe";
import { waitForWsConnected } from "./fixtures/session-setup";

test.describe("agent roster freshness after a raw-REST agent creation (row 55 bug A / issue #1009)", () => {
  test("a newly created agent becomes visible in the sidebar roster without a reload — the #1009 acceptance test", async ({
    page,
  }) => {
    // Outer budget = sum of the inner steps' own ceilings (goto + WS + raw
    // creation + the 45s roster poll + selectAgent's sidebar checks) with
    // cold-start headroom — the 90s config default would kill the test
    // mid-flight before the 45s oracle below could legitimately resolve.
    test.setTimeout(180_000);
    await page.goto("/");
    // The agent_created frame is fire-and-forget (nothing replays it on
    // connect), so the SPA socket must be open BEFORE the REST creation.
    await waitForWsConnected(page);

    const probeAgentName = await createBrowserProbeAgent(
      page,
      Date.now() % 65_536,
    );

    // NO reload anywhere in this test — that is the acceptance claim. The
    // row appears when the sidebar's workspaces poll (30s) lands the new
    // core_team member; see the TIMEOUT RE-DERIVATION note above for why
    // this cannot be the old 5s any more. If this fails, check both halves
    // of the roster: the agent_created frame's ['agents'] invalidation
    // (src/store/chat/slices/frames.ts) for the agent object, and the
    // workspaces query poll (Sidebar.tsx) for the core_team membership.
    const row = await agentRowGroup(page, probeAgentName, DEFAULT_WORKSPACE_NAME, {
      rowTimeoutMs: 45_000,
    });
    await expect(row).toBeVisible();

    // And the row is not just rendered but reachable: clicking it selects the
    // agent (the FR-029 row-click flow) — the reachability half of what the
    // old picker menu interaction proved.
    await selectAgent(page, probeAgentName, DEFAULT_WORKSPACE_NAME);
  });

  test("the SAME agent IS visible after a reload — proves the fix's shape", async ({
    page,
  }) => {
    test.setTimeout(180_000);
    await page.goto("/");
    // The agent_created frame is fire-and-forget (nothing replays it on
    // connect), so the SPA socket must be open BEFORE the REST creation.
    await waitForWsConnected(page);

    const probeAgentName = await createBrowserProbeAgent(
      page,
      Date.now() % 65_536,
    );

    // The fix for browser-live-video.spec.ts: reload before selecting, the
    // same order fixtures/conformance-helpers.ts's startFreshChatWithAgent
    // already uses safely. A fresh load refetches both roster inputs
    // directly, so the row is bounded by page load, not by the poll.
    await page.goto("/");

    await selectAgent(page, probeAgentName, DEFAULT_WORKSPACE_NAME);
  });
});

// Harness-only regression for selectors.ts::selectAgent (static DOM, no
// gateway). A real row click closes the unpinned sidebar, so the row can
// unmount before its aria-current is read; selectAgent then reopens the
// sidebar and reads the selected row instead. CI run 39143775263 (job
// 117725852235) failed THIS spec's acceptance test exactly there: the chat
// had switched ("Browser Probe 41894 - Main Session" in the header, sidebar
// closed), but the fallback never ran, because an expect() matcher that runs
// out of time throws a plain matcher error, not errors.TimeoutError.
test.describe("selectAgent when the row click closes the sidebar (static harness)", () => {
  test("confirms the switch through the reopened sidebar's selected row", async ({ page }) => {
    await page.setContent(`
      <button id="show" aria-label="Show sidebar" style="display:none">menu</button>
      <div id="host"></div>
      <script>
        const nav = (selected) => \`
          <nav aria-label="Main navigation">
            <button aria-label="Hide My Workspace agents" aria-expanded="true">My Workspace</button>
            <div role="group" aria-label="My Workspace">
              <div role="group" aria-label="Probe Agent" data-selected="\${selected}">
                <div class="sidebar-agent-row"><button id="main">Probe Agent</button></div>
              </div>
            </div>
          </nav>\`;
        const host = document.getElementById('host');
        const show = document.getElementById('show');
        host.innerHTML = nav('false');
        // The row click selects the agent and closes the sidebar at once: the
        // row (and its aria-current) is gone before anyone can read it.
        host.addEventListener('click', (e) => {
          if (e.target.id !== 'main') return;
          host.innerHTML = '';
          show.style.display = '';
        });
        show.addEventListener('click', () => {
          host.innerHTML = nav('true');
          show.style.display = 'none';
        });
      </script>`);
    await selectAgent(page, "Probe Agent", "My Workspace");
  });

  test("still fails when the click did not move the selection", async ({ page }) => {
    await page.setContent(`
      <nav aria-label="Main navigation">
        <button aria-label="Hide My Workspace agents" aria-expanded="true">My Workspace</button>
        <div role="group" aria-label="My Workspace">
          <div role="group" aria-label="Probe Agent" data-selected="false">
            <div class="sidebar-agent-row"><button>Probe Agent</button></div>
          </div>
        </div>
      </nav>`);
    await expect(selectAgent(page, "Probe Agent", "My Workspace")).rejects.toThrow(
      /neither its aria-current nor the selected row confirmed the switch/,
    );
  });

  // F1 (code-reviewer, PR #1366): the fallback's completion check used the
  // GLOBAL selected row, so a same-named agent still selected in ANOTHER
  // workspace satisfied it after a failed attach rolled the app back —
  // selectAgent blessed a switch that never happened. The completion check
  // must be the TARGET workspace's own row. This harness pins the exact
  // supported failure state useSelectSession produces: Jim exists in two
  // EXPANDED workspaces, My Workspace's Jim is selected, clicking Other
  // Workspace's Jim fails to attach (the sidebar closes as a real row click
  // does; the app stays on My Workspace), and the reopened sidebar shows the
  // stale selection. selectAgent must REJECT — never accept the other
  // workspace's selected same-named row as proof.
  test("does not accept another workspace's selected same-named agent when the target row's click does nothing", async ({
    page,
  }) => {
    await page.setContent(`
      <button id="show" aria-label="Show sidebar" style="display:none">menu</button>
      <div id="host"></div>
      <script>
        const nav = () => \`
          <nav aria-label="Main navigation">
            <button aria-label="Hide My Workspace agents" aria-expanded="true">My Workspace</button>
            <div role="group" aria-label="My Workspace">
              <div role="group" aria-label="Jim" data-selected="true">
                <div class="sidebar-agent-row"><button>Jim</button></div>
              </div>
            </div>
            <button aria-label="Hide Other Workspace agents" aria-expanded="true">Other Workspace</button>
            <div role="group" aria-label="Other Workspace">
              <div role="group" aria-label="Jim" data-selected="false">
                <div class="sidebar-agent-row"><button id="target">Jim</button></div>
              </div>
            </div>
          </nav>\`;
        const host = document.getElementById('host');
        const show = document.getElementById('show');
        host.innerHTML = nav();
        host.addEventListener('click', (e) => {
          if (e.target.id !== 'target') return;
          host.innerHTML = '';
          show.style.display = '';
        });
        show.addEventListener('click', () => {
          host.innerHTML = nav();
          show.style.display = 'none';
        });
      </script>`);
    await expect(selectAgent(page, "Jim", "Other Workspace")).rejects.toThrow(
      /neither its aria-current nor the selected row confirmed the switch/,
    );
  });
});

// Harness-only regression for selectors.ts::assistantMessages (static DOM, no
// gateway). Since the parent-wake landing a helper's hand-back renders in the
// parent chat as a system row that carries data-message-id
// (SystemMessageRows.tsx::VirtualSystemMessageRow, data-message-role="system").
// Before ea78b8704 the selector counted it as an assistant reply:
// subagent.spec.ts (d) saw 2 replies where the parent gave 1 (release run
// 39427198408). This pins ea78b8704's exclusion. System rows
// - helper hand-backs, goal outcomes, the clear-context divider - are never
// assistant replies.
test.describe("assistantMessages counts assistant replies only (static harness)", () => {
  test("a system row with data-message-id is not an assistant reply", async ({ page }) => {
    await page.setContent(`
      <div data-message-role="user" data-message-id="u1" class="flex-row-reverse">delegate this</div>
      <div data-testid="assistant-message" data-message-role="assistant" data-message-id="a1" data-status="complete">summary</div>
      <div data-message-role="system" data-message-id="helper-event-finished-s1-s2:1:final">A delegated session handed back (final): done</div>
      <div data-message-role="system" data-message-id="clear-1">Context cleared</div>
      <div data-message-id="a2">a reply rendered without a role attribute</div>
      <div data-message-id="a3" data-status="running">still streaming</div>`);
    await expect(assistantMessages(page)).toHaveCount(2);
    expect(await assistantMessages(page).evaluateAll((els) => els.map((el) => el.getAttribute("data-message-id"))))
      .toEqual(["a1", "a2"]);
  });
});
