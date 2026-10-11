import { type Locator, type Page, errors, expect } from '@playwright/test';

/**
 * Chat composer input — AssistantUI renders ComposerPrimitive.Input as a
 * <textarea> with aria-label="Message input" (ChatScreen.tsx:631).
 */
export const chatInput = (page: Page) =>
  page.locator('textarea[aria-label="Message input"]');

/**
 * Wait for a GENUINE, wire-ready WebSocket connection — not merely for the
 * composer's `disabled` attribute to clear.
 *
 * Regression history (2fa26e6a, issue #105 fix): ChatScreen's `inputEnabled`
 * used to require strict `isConnected`, so `chatInput(page)` reporting
 * `toBeEnabled()` was a reliable (if accidental) proxy for "the socket is
 * open" — every readiness gate in this suite leaned on that side effect.
 * The #105 fix correctly ALSO enables the composer while
 * `reconnectPhase` is `'reconnecting'`/`'slow'`, so a user can type during a
 * transient outage — the message is buffered in the outbound queue and
 * drained automatically once the connection recovers (this is the CORRECT,
 * intentional product behavior — see ws-reconnect.spec.ts's
 * `online_event_triggers_reconnect`, which deliberately asserts the
 * composer STAYS enabled during that window; do not "fix" that test).
 *
 * `toBeEnabled()` alone therefore no longer implies "connected": a message
 * filled and sent while only enabled-but-reconnecting lands in the outbound
 * queue, not the wire, and a test awaiting a real LLM reply then hangs to
 * its full timeout instead of failing fast. Call this ALONGSIDE (not
 * instead of) a `toBeEnabled()` check wherever a test previously used
 * `toBeEnabled()` alone as its "safe to type and send" gate.
 */
export async function waitForConnected(page: Page, opts?: { timeout?: number }): Promise<void> {
  await expect.poll(async () => page.evaluate(() =>
    ((window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [])
      .some((ws) => ws.readyState === WebSocket.OPEN),
  ), { timeout: opts?.timeout ?? 15_000 }).toBe(true);
}

/**
 * Send button — ComposerPrimitive.Send rendered with aria-label="Send message"
 * (ChatScreen.tsx:698). Only visible when not streaming.
 */
export const sendButton = (page: Page) =>
  page.locator('button[aria-label="Send message"]').first();

// ── Sidebar agent rows (navigation redesign; session-core-spec FR-029) ─────
//
// Ground truth (src/components/layout/sidebar/WorkspaceAgentList.tsx):
//   - Workspace disclosure IconButton — aria-label `Show <name> agents` when
//     folded / `Hide <name> agents` when expanded, with aria-expanded
//     (WorkspaceAgentList.tsx:213). Agent rows render ONLY while expanded
//     (isExpanded && rows.map, WorkspaceAgentList.tsx:254).
//   - Agent row — div role="group" aria-label="<Agent name>" with
//     data-selected (WorkspaceAgentList.tsx:347); its main button carries
//     aria-current="true" exactly when the row is the selected chat
//     (WorkspaceAgentList.tsx:355). Clicking it opens that agent's MAIN chat
//     in that workspace (openMain → selectSession, FR-029 "Row click /
//     switch-agent MUST navigate eligible main"). There is no in-chat agent
//     switching: the composer AgentPicker was deleted outright (nav commit
//     e84ac0532 "remove the composer agent picker";
//     src/components/chat/ChatScreen.pickerRemoval.test.tsx pins its
//     absence), so `agentPicker` is gone from this file.
//   - "New chat with <Agent name>" icon button on the row
//     (WorkspaceAgentList.tsx:299,379) — the ONLY new-chat path (FR-029
//     "only agent-row New chat creates extra MAIN chat"; FR-031 deleted
//     "/new" outright and forbids the SPA from sending it).
//
// The sidebar is CLOSED by default (src/store/sidebar.ts isOpen:false,
// isPinned:false) and renders nav[aria-label="Main navigation"] only while
// open (Sidebar.tsx:375), so every helper below first opens it the way a
// user does: the header hamburger, aria-label "Show sidebar"
// (SidebarShowButton.tsx:32).

/** escapeRegExp — embedded in locator name patterns below. */
const escapeRegExp = (s: string): string => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * The seeded default workspace's display name — ensureDefaultWorkspace seeds
 * exactly one workspace named "My Workspace" (pkg/gateway/rest_workspaces.go:584).
 * E2E gateways are fresh-seeded, so helpers that take no workspace argument
 * resolve rows against this one. Pass the name explicitly for any other
 * workspace (its disclosure button is labelled from the workspace's own name).
 */
export const DEFAULT_WORKSPACE_NAME = 'My Workspace';

/**
 * The workspace disclosure button (chevron) for one workspace — the only
 * control that folds/unfolds its agent roster
 * (WorkspaceAgentList.tsx:207-217). The name alternates between
 * "Show <name> agents" (folded) and "Hide <name> agents" (expanded), so the
 * pattern matches either state and `aria-expanded` carries the truth.
 */
export const workspaceDisclosureButton = (page: Page, workspace: string) =>
  page
    .getByRole('button', {
      name: new RegExp(`^(?:Show|Hide) ${escapeRegExp(workspace)} agents$`),
    });

/**
 * The currently-selected agent row — the one group in the sidebar whose
 * data-selected is "true" (WorkspaceAgentList.tsx:347; only agent rows carry
 * data-selected). Selected means: this workspace is active AND this agent owns
 * the selected chat (WorkspaceAgentList.tsx:296-297) — the user-visible
 * replacement for reading the deleted composer picker's label.
 */
export const selectedAgentRow = (page: Page) =>
  page.locator('nav[aria-label="Main navigation"] [role="group"][data-selected="true"]');

/**
 * Open the sidebar if it is closed. No-op when it is already open (the nav is
 * only mounted while open — Sidebar.tsx:375), so this is cheap to call
 * unconditionally. Returns once the nav is visible.
 */
export async function openSidebar(page: Page): Promise<void> {
  const nav = page.locator('nav[aria-label="Main navigation"]');
  if (!(await nav.isVisible())) {
    // Bounded so a missing/covered hamburger fails in seconds with a message
    // naming the target, instead of riding the locator's unbounded default
    // (config sets no actionTimeout) out to the whole test timeout.
    await page
      .getByRole('button', { name: 'Show sidebar', exact: true })
      .click({ timeout: 15_000 });
  }
  await nav.waitFor({ state: 'visible', timeout: 15_000 });
}

/**
 * Locate an agent row's group (role="group" aria-label="<Agent name>"),
 * expanding the workspace's roster first when it is folded. The workspace
 * defaults to the seeded default workspace (DEFAULT_WORKSPACE_NAME).
 *
 * The row group is the same locator the component's own vitest tests use
 * (WorkspaceAgentList.newChat.test.tsx:235), and name matching follows
 * getByRole's case-insensitive rules — pass a workspace when two workspaces
 * hold same-named agents.
 */
export async function agentRowGroup(
  page: Page,
  name: string | RegExp,
  workspace: string = DEFAULT_WORKSPACE_NAME,
  opts: { rowTimeoutMs?: number } = {},
) {
  await openSidebar(page);
  const disclosure = workspaceDisclosureButton(page, workspace);
  await disclosure.waitFor({ state: 'visible', timeout: 15_000 });
  if ((await disclosure.getAttribute('aria-expanded')) === 'false') {
    // Bounded: a disclosure that never becomes clickable (overlay, rerender
    // churn) must fail here, named, in seconds — not at the test timeout.
    await disclosure.click({ timeout: 15_000 });
  }
  const row = page
    .getByRole('group', { name: workspace, exact: true })
    .getByRole('group', { name });
  await row.waitFor({ state: 'visible', timeout: opts.rowTimeoutMs ?? 15_000 });
  return row;
}

/**
 * Switch the active chat to an agent by clicking its sidebar row — the
 * current user flow for "choose an agent" (FR-029 row click navigates the
 * eligible main chat; the composer AgentPicker and routing-agent-select are
 * RETIRED surfaces, removed by nav commit e84ac0532, and there is no in-chat
 * handover).
 *
 * Asserts the switch took effect through the row's own user-visible state:
 * its main button carries aria-current="true" exactly when the row is the
 * selected chat (WorkspaceAgentList.tsx:355) — never through a deleted
 * picker label.
 *
 * Does NOT clear dialog overlays. Calling dismissStaleDialogOverlay from here
 * put an Escape keypress in the path of all 11 specs that select an agent, and
 * turned E2E — ui-browser flaky on release 585b70b1b (green at d81bcb1ec) — a
 * shard whose specs open legitimate dialogs. A spec that needs the overlay
 * cleared calls dismissStaleDialogOverlay itself, before selecting.
 */
export const selectAgent = async (
  page: Page,
  name: string | RegExp = /Jim/i,
  workspace: string = DEFAULT_WORKSPACE_NAME,
) => {
  const row = await agentRowGroup(page, name, workspace);
  // The row strip's first button is the row's MAIN action (WorkspaceAgentList
  // .tsx:348-387: main button, then "Past sessions with X", then "New chat
  // with X" — an order the component's own vitest tests pin).
  const mainButton = row.locator('.sidebar-agent-row button').first();
  // Bounded: a click that never lands (row detached mid-attach, overlay)
  // fails here, named, in seconds — the unbounded default burned whole test
  // timeouts on CI (llm-agents, run 38112714098).
  await mainButton.click({ timeout: 15_000 });
  // Assert the switch took effect: the row's main button carries aria-current
  // exactly while this agent owns the selected chat (WorkspaceAgentList
  // .tsx:355). RACE: clicking the row also CLOSES an unpinned sidebar
  // (useSelectSession calls onClose before navigate — Sidebar.tsx wires
  // onOverlayClose to close()), so the row unmounts after its exit animation
  // and the aria-current poll can legitimately lose to the unmount ("element(s)
  // not found" — reproduced locally and in CI llm-chat run 38115921651, and
  // passing on retry). The check is therefore TWO-SURFACED, same oracle, same
  // strength: (1) the row's aria-current before any unmount, then (2) if the
  // sidebar closed, REOPEN it the way a user does and read the row's
  // data-selected instead — the same store-driven "this agent owns the
  // selected chat" truth, independent of the close timing.
  try {
    await expect(mainButton).toHaveAttribute('aria-current', 'true', { timeout: 5_000 });
  } catch (err) {
    if (!(err instanceof errors.TimeoutError)) throw err;
    // Fall through to the second surface whether the sidebar closed OR the
    // row was transiently replaced while it stayed open — both lose the
    // aria-current poll without the switch having failed.
    const nav = page.locator('nav[aria-label="Main navigation"]');
    if (!(await nav.isVisible())) await openSidebar(page);
    const stillSelected = selectedAgentRow(page);
    await expect(
      stillSelected,
      `selectAgent: clicked ${String(name)}'s row but neither its aria-current nor the ` +
        'selected row confirmed the switch — the active chat did not move',
    ).toHaveAttribute('aria-label', name, { timeout: 10_000 });
  }
};

/**
 * Start a new chat via the agent row's "New chat with <Agent name>" icon
 * button — the ONLY new-chat path left (FR-029; FR-031 deleted "/new" from
 * the command table and the SPA must not send it: typing it now shows a
 * refusal and sends nothing, src/hooks/useSlashMenu.ts RETIRED_COMMAND_NAMES).
 * The old header "New Chat" button and the /new palette route this helper
 * used to drive are both gone.
 *
 * Which row: `name`/`workspace` pick one explicitly; with no name, the
 * CURRENTLY SELECTED row is used when one exists (the old /new kept the
 * active agent — this preserves that contract), falling back to the first
 * row of the workspace roster (deterministic core_team order).
 *
 * When the current first message has no known chat id, the SPA raises the
 * "Start a new chat?" confirm dialog (extraChatGuard.decideNewChat — an
 * unconfirmed send must not be silently abandoned; src/components/layout/
 * sidebar/WorkspaceAgentList.tsx:394-403). A user who wants a fresh chat
 * confirms it, so this helper clicks "Start a new chat" whenever the dialog
 * appears and is a no-op when it does not.
 *
 * The new chat binds lazily: like the /new it replaces, the session id is
 * minted on the first sent message (beginPairExtra → startNewSession —
 * src/components/layout/sidebar/pairExtra.ts), so specs that identify the
 * turn's session by diffing OMNIPUS_HOME/sessions keep working unchanged.
 * Returns once the dialog question is settled; callers assert the empty
 * thread themselves (assistantMessages toHaveCount(0)).
 */
export const startNewChat = async (
  page: Page,
  name?: string | RegExp,
  workspace: string = DEFAULT_WORKSPACE_NAME,
) => {
  let row: Locator;
  if (name !== undefined) {
    row = await agentRowGroup(page, name, workspace);
  } else {
    await openSidebar(page);
    row = selectedAgentRow(page);
    if ((await row.count()) === 0) {
      row = page
        .getByRole('group', { name: workspace, exact: true })
        .getByRole('group')
        .first();
    }
  }
  const agentName = await row.getAttribute('aria-label', { timeout: 15_000 });
  if (!agentName) {
    throw new Error(
      'startNewChat: could not resolve an agent row to click — no "New chat with …" ' +
        `label could be derived (name=${String(name)})`,
    );
  }
  // Click the row's "New chat with <Agent>" button DIRECTLY — never a strip
  // hover first. The hover this used to do is the fail-slow defect behind the
  // llm-agents shard burning whole spec timeouts (CI run 38112714098: every
  // cancel-cross-channel T2x attempt died at exactly its test timeout — 4.7m
  // for the slow() tests, 6.2m/7.3m for T24a/b; reproduced locally on T21 as
  // "locator.hover: Test timeout of 270000ms exceeded" looping "element is
  // not stable" / "element was detached"): startNewChat runs right after the
  // row was clicked, while the sidebar is animating (exit/re-enter slide) and
  // the row's attention halo (attention.css sidebar-attention-halo-loop, a
  // 1.6s infinite scale pulse) keeps boxes moving; Playwright's hover demands
  // a stable box across two frames and retries FOREVER by default (config
  // sets no actionTimeout). The click needs no hover: agent-row.css reveals
  // the actions when the row is hovered, focus-within, OR SELECTED
  // ([data-selected='true'] .sidebar-agent-actions { opacity: 1 }), and the
  // click itself moves the pointer onto the row, :hover-revealing them.
  //
  // The click is OUTCOME-VERIFIED rather than trusted blind, because a
  // LANDING click closes the sidebar (beginPairExtra → closeOverlay →
  // Sidebar's onOverlayClose) and detaches the button mid-action — Playwright
  // reports that as a click timeout even though the fresh chat started
  // (observed in the same local T21 run: the failure screenshot showed the
  // fresh, empty Jim chat while the click "timed out"). Bounded retries; the
  // OUTCOME between attempts decides: the confirm dialog up, or the sidebar
  // closed (closeOverlay only runs on the 'started' outcome — pairExtra.ts),
  // means the new-chat flow is under way. Overall budget 20s: bounded, named,
  // and far below every caller's test timeout.
  const newChatButton = row.getByRole('button', {
    name: new RegExp(`^New chat with ${escapeRegExp(agentName)}$`),
  });
  const nav = page.locator('nav[aria-label="Main navigation"]');
  const dialog = page.getByRole('alertdialog', { name: 'Start a new chat?' });
  const clickBudget = Date.now() + 20_000;
  for (;;) {
    try {
      await newChatButton.click({ timeout: 5_000 });
      break;
    } catch (err) {
      // Outcome first: a landed click either raised the dialog ('prompt') or
      // closed the sidebar ('started' — closeOverlay only runs there).
      const dialogUp = await dialog.isVisible().catch(() => false);
      const navVisible = await nav.isVisible().catch(() => true);
      if (dialogUp || !navVisible) break;
      if (!(err instanceof errors.TimeoutError) || Date.now() >= clickBudget) {
        throw new Error(
          `startNewChat: the "New chat with ${agentName}" button never produced a landing ` +
            `click within its 20s budget (sidebar ${navVisible ? 'still open' : 'closed'}, ` +
            `dialog ${dialogUp ? 'up' : 'not up'}) — the roster row kept animating/detaching ` +
            'so Playwright could not complete the action. Original error: ' +
            (err instanceof Error ? err.message : String(err)),
        );
      }
      // Otherwise the click genuinely has not landed yet — retry.
    }
  }

  // Confirm dialog — only when an unconfirmed first send is pending; confirm
  // it (the caller asked for a FRESH chat) and wait for the question to
  // settle. When no dialog ever appears this resolves immediately. (`dialog`
  // is declared above, next to the click whose outcome it verifies.)
  try {
    await dialog.waitFor({ state: 'visible', timeout: 3_000 });
  } catch {
    return; // no unconfirmed send — the extra chat started directly
  }
  await dialog
    .getByRole('button', { name: 'Start a new chat', exact: true })
    .click({ timeout: 10_000 });
  await expect(dialog).toHaveCount(0, { timeout: 10_000 });
};

/**
 * Completed assistant messages — only counts messages whose data-status is not
 * "running". AssistantUI creates a placeholder element with data-message-id as
 * soon as the user sends a message (before the LLM responds). Excluding
 * data-status="running" ensures tests wait for the LLM to actually complete
 * its response rather than matching the in-progress placeholder.
 *
 * Ground truth: ChatScreen sets data-status={message.status?.type ?? 'complete'}
 * on AssistantMessage's MessagePrimitive.Root, and data-message-id on all
 * message roots. User messages have flex-row-reverse (right-aligned bubbles);
 * assistant messages do not.
 */
export const assistantMessages = (page: Page) =>
  page.locator('[data-message-id]:not(.flex-row-reverse):not([data-status="running"])');

/**
 * User messages — complement of assistantMessages; row uses `flex-row-reverse`.
 */
export const userMessages = (page: Page) =>
  page.locator('[data-message-id].flex-row-reverse');

/**
 * Nav link helper — sidebar must be open before calling this.
 * Returns the anchor inside the nav for a given href.
 * The sidebar renders nav[aria-label="Main navigation"] ONLY while open.
 *
 * HashRouter: TanStack Router generates href="/#/<path>" links.
 * Call with the full hash-prefixed href, e.g. navLink(page, '/#/agents').
 */
export const navLink = (page: Page, href: string) =>
  page.locator(`nav[aria-label="Main navigation"] a[href="${href}"]`);

/**
 * Agent cards on the roster page — AgentCard renders a <button> with
 * aria-label="View agent {name}" (AgentCard.tsx:29).
 * Ground truth: "View agent Mia — Omnipus Guide" (em-dash, not regular dash).
 */
export const agentCards = (page: Page) =>
  page.locator('button[aria-label^="View agent "]');

// The retired `newChatButton` (header banner "New Chat") and the former
// /new-palette `startNewChat` were deleted here — both drove surfaces the
// navigation redesign removed. Citations: header button removed (ChatControls
// .tsx), "/new" deleted outright by session-core-spec FR-031 (founder WC-1,
// 2026-10-09; the SPA must not send it), extra-chat creation is the agent-row
// "New chat" action per FR-029. The current `startNewChat` above is the
// replacement.

/**
 * Session token/cost counter — moved out of the header banner into the
 * composer's context row (src/components/chat/composer/TokenCounter.tsx),
 * per ChatControls.tsx: "The Agent picker, Model selector, and Token counter
 * used to live here but moved into the composer's context row... so they sit
 * next to the input they scope." Rendered unconditionally but hidden below
 * the composer's own `@2xl` container-query breakpoint (~42rem/672px) — the
 * default Playwright viewport (1280×720) is wide enough for it to show.
 */
export const tokenCounter = (page: Page) =>
  page.locator('[data-testid="session-token-counter"]');

/**
 * Budget for dismissStaleDialogOverlay: generous enough to drain a short
 * queue of stacked approvals, far below any test budget, and free (instant)
 * when there is nothing to clear.
 */
const DIALOG_DRAIN_BUDGET_MS = 15_000;

/**
 * Collapse whitespace and cap length — dismissal texts surface in test
 * annotations, so keep them honest (the real dialog text) and bounded.
 */
const oneLineDialogText = (text: string): string => {
  const collapsed = text.replace(/\s+/g, ' ').trim();
  return collapsed.length > 200 ? `${collapsed.slice(0, 200)}…` : collapsed;
};

/**
 * Dismiss any modal dialog-overlay left over from earlier state, and keep
 * dismissing until none remains. No-op (and instantly so) when no dialog is
 * open — the poll's first probe already reads 0.
 *
 * Why this exists (CI run 36123574726, job "E2E — llm-agents"): a delegated
 * child's `bash` call can raise an ADR-092 Auto filesystem/network pre-flight
 * escalation ask (pkg/tools/shell_permission_mode.go::requestPreflightApproval).
 * Unattended CI never answers it, so it sits pending for the full
 * defaultToolApprovalTimeout — 600s (pkg/gateway/gateway.go:88). While it is
 * pending, ToolApprovalModal rehydrates it on EVERY fresh page load —
 * reconcileWithSessionState (src/store/toolApproval.ts) rebuilds the queue
 * entry from the session_state snapshot (as a reconnect stub when the ask
 * predates the page) — and that Radix dialog-overlay intercepts every pointer
 * event on the page. Observed cost: 579 blocked click retries across a full
 * 300s budget, then `Test timeout of 300000ms exceeded` at selectAgent
 * (subagent.spec.ts test (a), attempt 1 of the same run).
 *
 * Escape is the dismissal that STICKS: while an approval is live the modal
 * maps Escape to Deny (the safe default — ToolApprovalModal.tsx
 * ::handleDismissRequest), which resolves the approval server-side so the
 * next session_state snapshot cannot put the card back. Tab-local dismissal
 * (dequeue) does not have that property — the next snapshot resurrects the
 * card as a stub.
 *
 * The check is quiet-window-shaped, not one-shot, for two reasons:
 *   1. The rehydrating session_state frame can land after goto('/') returns;
 *      a single count() check races it and misses. CI run 36123574726
 *      attempt 3 shows a pending ask expiring at 11:27:30 with NO deny ever
 *      recorded — no page ever dismissed it, despite the steered-session
 *      spec's one-shot guard having run at that attempt's start. Worse, the
 *      original `expect.poll(...).toBe(0)` form returned on its FIRST zero
 *      reading: GitHub release run 36337231197 (steered-session
 *      reachability, retry #1) failed exactly that way — the poll read 0,
 *      returned, and the PREVIOUS attempt's approval rehydrated moments
 *      later, blocking selectAgent for 357s of a 420s budget. So a zero is
 *      only accepted once it has HELD for a post-hydration quiet window:
 *      the helper keeps polling after the first zero and only returns when
 *      no overlay has been seen for the whole window.
 *   2. Approvals QUEUE behind the visible card ("+N more"). One Escape may
 *      reveal the next card; the loop presses again until the overlay is
 *      gone, bounded by the timeout.
 *
 * Escape is pressed ONLY while an overlay is actually present: a bare Escape
 * with no dialog open cancels a streaming chat turn (the behaviour
 * cancel-cross-channel.spec.ts T23 pins), which must never happen here.
 *
 * RETURNS the dialogs it actually dismissed, as single-line truncated texts.
 * A dismissal IS a server-side Deny (Escape on a live approval maps to Deny —
 * see above), so a caller can make that Deny visible by annotating the test
 * report per entry (bug-regression.spec.ts::dismissAndAnnotate pushes an
 * `approval-drained` annotation for each). An empty array = nothing was
 * dismissed. Callers that ignore the return value behave exactly as before.
 *
 * THROWS a genuine `errors.TimeoutError` (from @playwright/test) when an
 * overlay outstays the 15s budget. expect.poll in the installed Playwright
 * reports its budget expiry as a plain matcher-failure error, not a
 * TimeoutError, so the poll is rewrapped below — giving callers a stable,
 * class-checkable timeout. Behaviour is unchanged for callers that just let
 * it propagate: still "throws when a dialog outstays 15s". The original
 * error text is preserved in full inside the new message, so nothing is
 * laundered away.
 */
// Post-hydration quiet window (see the doc comment, reason 1): how long a
// zero-overlay reading must HOLD before it is trusted. Chosen to cover the
// session_state snapshot landing just after goto('/') on a cold start; it
// spends ~2s when the page is genuinely clean and the same ~2s inside the
// existing DIALOG_DRAIN_BUDGET_MS bound when it is not — no caller's budget
// is lengthened.
const DIALOG_QUIET_WINDOW_MS = 2_000;

export const dismissStaleDialogOverlay = async (page: Page): Promise<string[]> => {
  const overlay = page.locator('[data-testid="dialog-overlay"]');
  const dismissedDialogTexts: string[] = [];
  // First moment of the CURRENT unbroken run of zero-overlay readings; -1
  // while an overlay is present. A rehydrating overlay that lands mid-window
  // resets it, so a late approval card restarts the window instead of racing
  // it (release run 36337231197, retry #1: a first-zero return let the
  // previous attempt's card rehydrate and block selectAgent for 357s).
  let zeroSince = -1;
  try {
    await expect
      .poll(
        async () => {
          // Re-read immediately before pressing. `count() > 0` then press is
          // check-then-act: if the overlay closes in that window the Escape lands
          // on the page instead, and a bare Escape with no dialog CANCELS A
          // STREAMING TURN (the behaviour cancel-cross-channel.spec.ts T23 pins).
          // Pressing on the overlay locator itself cannot hit the page: if the
          // element is gone the press throws and the poll simply re-reads 0.
          if ((await overlay.count()) > 0) {
            zeroSince = -1;
            let dialogText: string | null = null;
            let denied = false;
            try {
              // Read the dialog text BEFORE pressing so a dismissal can be
              // reported honestly: only an Escape that actually landed is
              // recorded — a failed read or a vanished overlay records nothing.
              dialogText = await overlay
                .first()
                .textContent({ timeout: 1_000 })
                .catch(() => null);
              await overlay.first().press('Escape', { timeout: 1_000 });
              denied = true;
            } catch {
              // Overlay vanished between the count and the press — nothing to
              // dismiss, and crucially no stray Escape reached the page.
            }
            if (denied && dialogText !== null) {
              dismissedDialogTexts.push(oneLineDialogText(dialogText));
            }
          }
          const remaining = await overlay.count();
          if (remaining > 0) {
            zeroSince = -1;
            return remaining;
          }
          if (zeroSince < 0) zeroSince = Date.now();
          // Zero is only accepted once it has HELD for the quiet window; until
          // then report "not yet settled" so the poll keeps sampling.
          return Date.now() - zeroSince >= DIALOG_QUIET_WINDOW_MS ? 0 : -1;
        },
        // Budget rationale: see DIALOG_DRAIN_BUDGET_MS above. Intervals stay at
        // or below the quiet window so a clean page settles in ~2s.
        { timeout: DIALOG_DRAIN_BUDGET_MS, intervals: [250, 500, 1_000] },
      )
      // A leftover modal overlay must be dismissable — it blocks every click
      // beneath it, so leaving one up converts every later click assertion
      // into a slow timeout instead of a clear failure.
      .toBe(0);
  } catch (err) {
    // Two failure modes reach here. (1) The poll budget expires: Playwright
    // 1.61 reports that as a plain matcher-failure ExpectError, not a
    // TimeoutError, so it is rewrapped below as a REAL errors.TimeoutError
    // (class-checkable by the drain helpers in bug-regression.spec.ts), with
    // the original message embedded in full. (2) A poll callback error such
    // as a closed page propagates immediately and is NOT a timeout: it is
    // rethrown unchanged so the drain helpers fail loudly with the real cause.
    // Playwright does not export TargetClosedError, so it is matched by name.
    if (err instanceof Error && err.name === 'TargetClosedError') throw err;
    throw new errors.TimeoutError(
      `dismissStaleDialogOverlay: dialog overlay still present (or kept rehydrating) after ` +
        `${DIALOG_DRAIN_BUDGET_MS}ms of Escape-on-overlay retries. ` +
        `Original error: ${err instanceof Error ? err.message : String(err)}`,
    );
  }
  return dismissedDialogTexts;
};

// The former picker-based `selectAgent` was deleted with the composer
// AgentPicker it drove (nav commit e84ac0532; ChatScreen.pickerRemoval
// .test.tsx pins the picker's absence; FR-029 "No in-chat handover"). The
// current sidebar-row `selectAgent` above is the replacement; its
// "Delegate-dependent E2E specs must run against a general-purpose task agent
// (default: Jim) rather than the default agent Mia" rationale carries over
// unchanged — Mia's "guide" persona refuses to emit `delegate`, so
// delegate-expecting specs still select Jim, now by clicking his row.

/**
 * Mid-run safety net for ADR-092 approval cards (PR #965's denyToolApproval,
 * widened to "any card, any time" so a card raised by a nested delegation
 * level cannot block the run for the production approval timeout).
 *
 * Polls the page for the ToolApprovalModal overlay; when one is visible it
 * clicks Deny — the safe operator default (the modal's default-focused
 * denyButtonRef, src/components/agents/ToolApprovalModal.tsx) — and RECORDS
 * the deny twice, so it is visible in the report either way:
 *   - console.warn at deny time (shows in the playwright list reporter), and
 *   - a `tool-approval-denied` test annotation (test.info().annotations).
 *
 * Deny — never Allow — because unattended runs must never grant an ask-gated
 * capability; a denied child tool call fails that one call and the child
 * continues on its allowed tools. The watcher NEVER presses a bare Escape:
 * with no dialog open that cancels a streaming chat turn
 * (cancel-cross-channel.spec.ts T23), and the watcher runs DURING the chain.
 *
 * onDeny, when given, is called after each successful deny (the caller uses
 * it to attach the tool-approval-denied test annotation — selectors.ts stays
 * free of the test object).
 *
 * Returns a stop function and the live deny log (one entry per deny this
 * watcher performed); call stop() before the test returns so the poll loop
 * cannot outlive the page.
 */
export function watchAndDenyToolApprovals(
  page: Page,
  onDeny?: (deniedAt: string) => void,
): { stop: () => void; denies: string[] } {
  let stopped = false
  const denies: string[] = []
  const overlay = page.locator('[data-testid="dialog-overlay"]')
  const denyButton = page.getByRole('button', { name: 'Deny', exact: true }).first()
  const run = async (): Promise<void> => {
    while (!stopped && !page.isClosed()) {
      try {
        if (await overlay.isVisible()) {
          // Re-check the button right before clicking: the modal can resolve
          // itself (production approval timeout) between the two reads.
          if (await denyButton.isVisible()) {
            const clicked = await denyButton
              .click({ timeout: 2_000 })
              .then(
                () => true,
                () => false,
              )
            if (clicked) {
              const at = new Date().toISOString()
              denies.push(at)
              console.warn(
                `[watchAndDenyToolApprovals] tool-approval modal appeared mid-run — DENIED it at ${at} ` +
                  '(safe operator default) so the run could continue',
              )
              onDeny?.(at)
            }
          }
        }
      } catch {
        // Page tearing down (test end/failure) — the stop flag and isClosed
        // check end the loop; nothing to record on a dead page.
      }
      await page.waitForTimeout(250).catch(() => undefined)
    }
  }
  void run()
  return {
    stop: () => {
      stopped = true
    },
    denies,
  }
}

// ── Live browser panel (ADR-038/039/040/041/047) ──────────────────────────
//
// Ground truth (src/components/browser/BrowserLiveView.tsx):
//   - "Watch live" launcher — aria-label="Watch live" (BrowserNavigateBlock /
//     BrowserToolBlock, src/components/chat/tools/), opens the app-root
//     BrowserLivePanel onto the globally-active session/agent.
//   - data-testid="browser-live-panel-docked" — the docked panel's <aside>
//     root (BrowserLivePanel.tsx).
//   - data-testid="browser-live-frame" — the pointer/keyboard capture
//     surface (role="application"), present once the first frame (JPEG or
//     WebRTC) has arrived.
//   - data-testid="browser-live-video" — the WebRTC <video> sink. ONLY
//     rendered when a live MediaStream is actually attached (mediaStream
//     truthy) — its presence is the one honest signal that this session is
//     really running the WebRTC video/audio path, not the JPEG fallback.
//   - data-testid="browser-live-img" — the JPEG-screencast <img> sink. ADR-061
//     (JPEG-fallback removal, "Retired surfaces" in the root CLAUDE.md)
//     deleted this OUTRIGHT from BrowserLiveView.tsx — it no longer exists
//     anywhere in `src/`, and reintroducing it is a regression, not a
//     legitimate merge-conflict resolution. WebRTC is the only live-video
//     path now: a failure surfaces as a persistent, honest error + Retry
//     button (data-testid="browser-live-retry" / "browser-live-retry-overlay"),
//     never a silent degrade to a second sink. `waitForLiveSink` below still
//     polls for this selector as a CANARY for that exact regression (it
//     should never match), not because the element is expected to appear.

/** The "Watch live" launcher rendered on a browser tool-call row in chat. */
export const watchLiveButton = (page: Page) =>
  page.getByRole('button', { name: 'Watch live' }).first();

/** The docked live-browser panel's root element. */
export const browserLivePanel = (page: Page) =>
  page.locator('[data-testid="browser-live-panel-docked"]');

/** The pointer/keyboard capture surface inside the live panel (present once a frame has arrived). */
export const browserLiveFrame = (page: Page) =>
  page.locator('[data-testid="browser-live-frame"]');

/** The WebRTC `<video>` sink — presence proves a real live MediaStream is attached (not the JPEG fallback). */
export const browserLiveVideo = (page: Page) =>
  page.locator('[data-testid="browser-live-video"]');

/**
 * The JPEG-screencast `<img>` fallback sink locator. ADR-061 deleted the
 * element this targets outright (see the ground-truth comment above) — this
 * locator can never match anything in the current codebase. Kept ONLY as the
 * canary `waitForLiveSink` polls for below; do not use it to assert "video is
 * live" (browserLiveVideo is the honest signal for that).
 */
export const browserLiveImgFallback = (page: Page) =>
  page.locator('[data-testid="browser-live-img"]');

/**
 * The "Retry" affordance BrowserLiveView.tsx renders the instant it has a
 * real, honest WebRTC failure to report — either data-testid="browser-live-retry"
 * (shown before the stream ever attaches) or "browser-live-retry-overlay"
 * (shown after attach, while still waiting for the first decoded frame).
 * Both only render when `displayError` is non-null (ADR-061 — every WebRTC
 * failure surfaces with a real reason, never silently) — so either becoming
 * visible is proof the outcome for THIS attempt is already known.
 */
const browserLiveRetryAny = (page: Page) =>
  page.locator('[data-testid="browser-live-retry"], [data-testid="browser-live-retry-overlay"]');

/**
 * Poll until the live view settles on the WebRTC `<video>` sink, or the
 * deadline expires — then report what it actually settled on: `"video"`,
 * `"img"` (JPEG fallback — see below, effectively unreachable now), or
 * `null` (neither ever appeared, i.e. a capture-path failure rather than a
 * WebRTC-specific one).
 *
 * FIX (SMALL-2, external review 2026-08-13): ADR-061 deleted the JPEG `<img>`
 * fallback sink outright (see `browserLiveImgFallback`'s own doc comment) —
 * `data-testid="browser-live-img"` no longer exists anywhere in `src/`, so
 * the `'img'` verdict below was unreachable dead code, and every WebRTC
 * failure fell through to the bare `null` verdict ("capture-path failure")
 * ONLY after riding out the FULL `timeoutMs` (default 90s) budget — even
 * though BrowserLiveView.tsx had already reported the real, specific reason
 * (`disabled` / `lite_build` / `ice-failed` / `answer-timeout` / ...) within
 * seconds, via its Retry-button error state. That misdiagnosed a WebRTC
 * failure as an unrelated capture-path stall, and did so as slowly as
 * possible. Polling for that same Retry-button error state now lets this
 * FAIL FAST — in seconds, not 90s — with the real reason in the thrown
 * error, the moment BrowserLiveView itself has already concluded this
 * attempt is dead. Retained the `img`-polling below (rather than deleting
 * it) as a CANARY: reintroducing the JPEG sink is a documented regression
 * (see CLAUDE.md's "Retired surfaces"), not a legitimate merge resolution,
 * and this is what would catch it coming back.
 *
 * CRITICAL — it must NOT return on the first `img` sighting (were it ever to
 * reappear). JPEG used to be architecturally guaranteed to paint FIRST on
 * every healthy cold start, so returning early on it would false-red
 * essentially every run:
 *
 *   - `BrowserLiveView.tsx` renders `mediaStream ? <video> : <img>` in the
 *     pre-ADR-061 world, and `mediaStream` is non-null only once WebRTC
 *     negotiation COMPLETES.
 *   - WebRTC negotiation legitimately takes seconds to tens of seconds:
 *     `waitForTracksTimeout` is 15s and the SPA's cold-start
 *     `firstAnswerTimeoutMs` is 30s.
 *
 * Deliberately NOT a `Promise.race` of separate `waitFor` calls: both
 * timeouts would still be in flight when the first resolves, and racing two
 * "resolve to null after timeoutMs" promises cannot tell you which signal was
 * actually visible. A manual poll gives an unambiguous answer.
 */
export async function waitForLiveSink(
  page: Page,
  timeoutMs = 90_000,
): Promise<'video' | 'img' | null> {
  const video = browserLiveVideo(page);
  const img = browserLiveImgFallback(page);
  const retry = browserLiveRetryAny(page);
  const deadline = Date.now() + timeoutMs;
  let sawImg = false;
  while (Date.now() < deadline) {
    // Video wins the moment it appears, whenever that happens — including
    // long after any interim state has been showing.
    if (await video.isVisible().catch(() => false)) return 'video';
    // FAIL FAST: BrowserLiveView has already concluded this attempt failed
    // (its Retry button + `displayError` text are up) — no point riding out
    // the rest of the budget for an outcome that's already known. Throwing
    // (rather than returning a value) lets the real, specific reason reach
    // the test failure message directly, instead of being flattened into
    // the generic `null` "capture-path failure" verdict below.
    if (await retry.first().isVisible().catch(() => false)) {
      const reported = await retry
        .first()
        .locator('..')
        .textContent()
        .catch(() => null);
      throw new Error(
        'WebRTC live view reported a real, specific failure before the video sink ever ' +
          `appeared: "${reported?.trim() ?? '(error text unavailable)'}" — see ` +
          'translateWebRTCFallbackReason (src/lib/browserWebRTC.ts) for the reason catalogue. ' +
          'This is a WebRTC-specific failure, not a capture-path stall — do not treat it as ' +
          'the generic "neither sink appeared" case.',
      );
    }
    if (!sawImg && (await img.isVisible().catch(() => false))) {
      sawImg = true;
    }
    await page.waitForTimeout(500);
  }
  // Budget exhausted with NEITHER a video sink NOR a reported error ever
  // appearing — a genuine capture-path stall (browser_attach/browser_status
  // round trip never completed), or the ADR-061 regression the `img` canary
  // above exists to catch.
  return sawImg ? 'img' : null;
}

// ── Native <select> helpers ───────────────────────────────────────────────

/**
 * Select an option from a NATIVE `<select>` by a label PATTERN.
 *
 * WHY THIS EXISTS: `Locator.selectOption({ label })` takes a **string**, not a
 * RegExp — the label is compared with `===` inside Playwright's injected page
 * script. A RegExp neither survives the wire serialization nor that comparison,
 * so `selectOption({ label: /Sales/i })` matches nothing and dies on the
 * "did not find some options" timeout. Seven call sites across
 * channel-routing.spec.ts and channels-routing.spec.ts were written exactly
 * that way. Nobody noticed because these are the `<select>` branch of a
 * SmartSelect fork, and SmartSelect renders a Radix combobox (not a native
 * `<select>`) for every roster size these specs actually build — so the broken
 * branch has never executed. It was also invisible to `npm run typecheck`,
 * which did not look at tests/ at all until tsconfig.tests.json existed.
 *
 * Resolving the pattern against the real option list here keeps the intended
 * case-insensitive/partial matching, and selecting by INDEX sidesteps the
 * `option.label`-vs-textContent ambiguity entirely. A non-match throws with the
 * full option list, so an activated-but-wrong branch fails loudly and legibly
 * instead of timing out anonymously.
 */
export async function selectNativeOptionByLabel(
  select: Locator,
  label: RegExp,
): Promise<void> {
  const texts = await select.locator('option').allTextContents();
  const index = texts.findIndex((t) => label.test(t.trim()));
  if (index === -1) {
    throw new Error(
      `selectNativeOptionByLabel: no <option> matches ${String(label)} — ` +
        `options present: ${JSON.stringify(texts.map((t) => t.trim()))}`,
    );
  }
  await select.selectOption({ index });
}
