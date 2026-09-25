// Sprint H · Subagent Collapsed-Block UI — E2E Tests
// Traces to: sprint-h-subagent-block-spec.md TDD rows 21, 22, 23, 24
//
// ARCHITECTURE NOTE: The spec originally called for a "scenario-provider path" (deterministic
// scripted LLM) gated behind the `test_harness` build tag. That mechanism was removed
// 2026-05-10 — these tests use a real LLM (OPENROUTER_API_KEY_CI required) with temperature=0
// and seed=42 plumbed into OpenRouter requests for maximum determinism (Wave 2.1).
//
// REWRITTEN 2026-09-24 (lane sq-gwfix, coordinator-flagged loose end from CI run
// 35997069836's delegation-hidden.spec.ts fix). ADR-091 D7/D10 (commit 66362240d)
// deleted SubagentBlock.tsx and its gate `shouldRenderSubagentSpan` UNCONDITIONALLY —
// there is no producer of any `[data-testid="subagent-*"]` element left in `src/` at
// any verbosity. Every test in this file used to wait (test (a): 300s; (b)/(c)/(e):
// test.slow()'s 270s; each burning real, paid LLM time) for a box that can never
// appear, then fail. Re-pointed at the surfaces that replaced SubagentBlock (same
// pattern as replay-fidelity.spec.ts's test (b) and delegation-hidden.spec.ts):
//   - THREAD: the `delegate` tool-call chip (`[data-testid="tool-call-badge"]
//     [data-tool="delegate"]`) — the parent's now-only delegation surface,
//     visible unconditionally (shouldRenderToolCall, ADR-091 D7/AC-7).
//   - THREAD: zero `[data-testid="subagent-collapsed"]`, ever — the guard pinning
//     the deletion so the surface cannot quietly come back.
//   - PANEL: the Activity panel row (`[data-testid="activity-row"]`) and its open
//     control (`[data-testid="activity-row-open"]`) into the child's OWN session
//     (ActivityPanel.tsx, ADR-091 D7/FR-E-004) — this is where "click the
//     collapsed header to expand" moved to.
//   - CHILD SESSION: the child's own tool calls, visible ONLY there (a child's
//     frames carry the child's own session_id now, I-4 — nothing nests in the
//     parent's thread any more, regardless of how long a test waits).
//
// Test (c) ("live step counter") is DELETED, not re-pointed — see its own comment
// below for why no replacement preserves an equivalent guarantee.
//
// data-testid cross-reference (current, post-rewrite):
//   - [data-testid="tool-call-badge"][data-tool="delegate"] — the parent's delegation chip
//   - [data-testid="tool-call-toggle"]       — ToolCallBadge.tsx's own collapse/expand control
//   - [data-testid="activity-bar"]           — ActivityBar.tsx
//   - [data-testid="activity-row"]           — ActivityPanel.tsx (one per child)
//   - [data-testid="activity-row-open"]      — ActivityPanel.tsx (open control into the child's session)
//   - [data-testid="activity-row-toggle"]    — ActivityPanel.tsx's own per-row disclosure

import { expect, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';
import {
  chatInput,
  assistantMessages,
  dismissStaleDialogOverlay,
  selectAgent,
  waitForConnected,
} from './fixtures/selectors';
import type { DelegationFrame } from './fixtures/delegation-completion';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

// Helper: assert OPENROUTER_API_KEY_CI is present.
// T0.1: no longer soft-skips. The key is required in CI; its absence is a CI
// configuration failure. The function is kept to preserve call-site structure
// but now validates (throws) rather than skipping.
function requireApiKey(): void {
  if (!process.env.OPENROUTER_API_KEY_CI) {
    throw new Error(
      'BLOCKED: OPENROUTER_API_KEY_CI not set. ' +
      'This test requires a real LLM. ' +
      'The key must be present in CI — see tests/e2e/README.md prerequisites.',
    );
  }
}

// Helper: start a fresh chat session and route to a delegate-capable task agent.
//
// AGENT ROUTING: every test in this file expects the active agent to emit `delegate`.
// The default agent Mia's "guide" persona makes the model REFUSE to delegate ("My role
// is to explain… not to delegate to subagents"), captured in CI artifacts. Switch to Jim
// (the general-purpose task agent) so the delegate-dependent assertions are exercised.
async function startFreshChat(page: import('@playwright/test').Page): Promise<void> {
  // A stale tool-approval dialog rehydrates on every fresh page load while its
  // ask pends (reconcileWithSessionState, src/store/toolApproval.ts) and its
  // dialog-overlay intercepts the New Chat click — CI run 36123574726 attempt 1
  // burned the entire 300s budget in 579 blocked-click retries right here.
  // Escape maps to Deny while an approval is live, so the dismissal sticks.
  await dismissStaleDialogOverlay(page);
  const newChat = page.getByRole('banner').getByRole('button', { name: 'New Chat' });
  if (await newChat.isVisible({ timeout: 5_000 })) {
    await newChat.click();
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });
  }
  // Route to Jim — Mia declines to delegate (see note above).
  await selectAgent(page, /Jim/i);
}

// Open/close the Activity panel — mirrors replay-fidelity.spec.ts's /
// delegation-hidden.spec.ts's own helper of the same name (ADR-091
// D7/FR-E-004 surface). Idempotent open avoids steered-session-stop.spec.ts's
// documented trap: a second unconditional click while the panel is already
// open lands on the Radix Sheet's own overlay, not the bar.
async function openActivityPanel(page: Page): Promise<void> {
  const bar = page.locator('[data-testid="activity-bar"]');
  await expect(bar).toBeVisible({ timeout: 15_000 });
  if ((await bar.getAttribute('aria-expanded')) !== 'true') {
    await bar.click();
  }
  await expect(bar).toHaveAttribute('aria-expanded', 'true', { timeout: 15_000 });
}

// Poll a chat surface's data-active-session-id until it is bound to a
// session NOT in excludeIDs (and not the '__pending' optimistic-send
// sentinel), then return that new id. Mirrors
// steered-session-reachability.spec.ts's own poll for the exact same seam:
// SessionRoute redirects a worker session's deep link straight to its
// workspace chat tab, so a URL check cannot tell "did we navigate" — this
// attribute is the one reliable signal, on either route. Accepts a list
// (not just one prior id) so a caller distinguishing two SIBLING children
// can exclude both the parent's id and the first sibling's id when opening
// the second.
async function waitForBoundSession(page: Page, excludeIDs: Array<string | null>): Promise<string> {
  const surface = page.locator('[data-active-session-id]').first();
  await expect
    .poll(
      async () => {
        const id = await surface.getAttribute('data-active-session-id');
        return id && id !== '__pending' && !excludeIDs.includes(id) ? id : null;
      },
      { timeout: 15_000 },
    )
    .not.toBeNull();
  const id = await surface.getAttribute('data-active-session-id');
  if (!id) throw new Error('waitForBoundSession: resolved to an empty session id');
  return id;
}

// Per-page WS-frame capture (registered in beforeEach BEFORE page.goto('/'),
// see below) — read by test (b) for its wire-level child-session checks.
const framesByPage = new WeakMap<Page, DelegationFrame[]>();

test.beforeEach(async ({ page }) => {
  // UPDATE 2026-09-24 (lane sq-gwfix): this file used to opt into verbose
  // chat here so its tests could use [data-testid="subagent-collapsed"] as
  // their thread-based signal. ADR-091 D7/D10 (66362240d) deleted that
  // component and its gate unconditionally — there is nothing left to opt
  // into verbose chat FOR. Every test below now asserts the `delegate`
  // tool-call chip and the Activity panel, neither of which
  // src/lib/toolVisibility.ts's shouldRenderToolCall or ActivityBar.tsx
  // gate on verboseChatEnabled — matching replay-fidelity.spec.ts's own
  // "no verbose-chat opt-in anywhere in this file" precedent.
  // Per-page WS-frames capture, for test (b)'s wire-level child-session
  // checks. Registered BEFORE page.goto('/') so it catches the chat
  // WebSocket the SPA opens on mount — a listener attached after the goto
  // would miss an already-open socket (page.on('websocket') only fires for
  // sockets opened after registration). Mirrors delegation-hidden.spec.ts's
  // per-page capture. Test (b) reads it via framesByPage.
  const frames: DelegationFrame[] = [];
  framesByPage.set(page, frames);
  page.on('websocket', socket => {
    if (!new URL(socket.url()).pathname.endsWith('/chat/ws')) return;
    socket.on('framereceived', ({ payload }) => {
      let frame: DelegationFrame;
      try { frame = JSON.parse(payload.toString()) as DelegationFrame; } catch { return; }
      if (frame['type'] === 'subagent_start' || frame['type'] === 'subagent_end') frames.push(frame);
    });
  });
  await page.goto('/');
});

// ────────────────────────────────────────────────────────────────────────────────
// (a) grandchild refused — Scenario 10, US-3
// BDD: Given a subagent sub-turn is running with a LEAF-ROLE target (Researcher:
//      no delegate grant, no onward delegation edges — Jim → Researcher is legal)
//      When the sub-turn's LLM attempts a tool call with name="delegate"
//      Then the tool dispatcher refuses it by policy for that leaf role
//      And no subagent_start frame with a grandchild parent_call_id is emitted
//      And the parent's transcript ToolCalls contains exactly one delegate entry
//
// Traces to: sprint-h-subagent-block-spec.md TDD row 21, BDD Scenario 10, lines 304-313
// ────────────────────────────────────────────────────────────────────────────────
test(
  '(a) grandchild refused: leaf-role subagent (Researcher) attempting delegate is refused, no nested row',
  async ({ page }) => {
    requireApiKey();
    // 300s: one parent delegate call (fast) plus Researcher's own refused
    // grandchild-delegate attempt, observed via Researcher's OWN session.
    // The refusal itself is fast (a policy check, not a second real LLM
    // round-trip past the tool call) — this is narrower than the old 420s
    // because that budget was sized for "wait for a box that needed both
    // round-trips AND client-side rendering to settle"; here the parent
    // delegate badge and the panel row resolve independently and early.
    test.setTimeout(300_000);

    await startFreshChat(page);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    // Confirm the composer is genuinely usable — enabled AND the socket is
    // actually open, not merely queueing. toBeEnabled() alone no longer
    // implies "connected" since the #105 offline-queue fix (2fa26e6a): see
    // waitForConnected's doc comment in fixtures/selectors.ts. Without this,
    // a page-load-time reconnect blip can leave the composer looking usable
    // while the very first message (the one that triggers `delegate`) lands
    // in the outbound queue instead of the wire, and this test then hangs to
    // its full timeout waiting on a chip that will never appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    const label = `grandchild-test-${Date.now()}`;

    // Deterministic prompt: commanding, specific — exact tool name, exact target.
    // Target pinned to Researcher: a LEAF in the delegation graph — Jim → Researcher
    // is a legal edge (seed.go delegation graph), but the Researcher role holds NO
    // delegate grant and no onward edges, so its delegate attempt is refused by
    // policy. Grandchildren are not universally forbidden (Worker holds delegate
    // with a Worker→Worker same-type helper edge); the refusal under test here is
    // the LEAF-ROLE one. Keeping the target fixed removes the parent model's
    // target choice as a variance source.
    await input.fill(
      [
        'Call the `delegate` tool exactly once, right now, with these arguments:',
        '  agent_id: "researcher"',
        `  label: "${label}"`,
        '  task: "You are the subagent. Your one and only job is to call the `delegate` tool yourself to attempt to delegate to a grandchild subagent with task \\"hello\\". If delegate is not available to you, report the exact error you receive. Do not do anything else."',
        'Do not reply in prose. Do not call any other tool. Call delegate now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // (1) THREAD — the parent's delegate chip, and the guard: zero
    // subagent-collapsed elements ever, at any verbosity.
    const delegateBadges = page.locator('[data-testid="tool-call-badge"][data-tool="delegate"]');
    await expect(delegateBadges.first()).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('[data-testid="subagent-collapsed"]')).toHaveCount(0);

    // (2) PANEL — exactly one row at the PARENT level (the direct analog of
    // "exactly one parent-level collapsed block": the leaf subagent's
    // refused delegate attempt must not create a second span up here, since
    // a refused/never-dispatched grandchild never gets its own
    // subagent_start in the parent's bucket either).
    await openActivityPanel(page);
    const parentRow = page.locator('[data-testid="activity-row"]', { hasText: label });
    await expect(parentRow).toBeVisible({ timeout: 60_000 });
    await expect(
      page.locator('[data-testid="activity-row"]'),
      'exactly one Activity-panel row at the parent level — the leaf subagent\'s refused delegate attempt must not create a second row',
    ).toHaveCount(1);
    const openControl = parentRow.locator('[data-testid="activity-row-open"]');
    await expect(openControl).toBeVisible({ timeout: 15_000 });

    // (3) CHILD SESSION — open Researcher's own session (where its refused
    // delegate attempt actually lives now — I-4, a child's frames carry the
    // child's own session_id, never the parent's).
    const parentSurface = page.locator('[data-active-session-id]').first();
    const parentSessionID = await parentSurface.getAttribute('data-active-session-id');
    await openControl.click();
    await waitForBoundSession(page, [parentSessionID]);

    // Researcher's OWN refused delegate attempt is visible in ITS OWN thread.
    // CI run 36026415761 (gateway log, all four attempts of this test —
    // 16:46:49 / 16:48:27 / 16:50:07): the leaf Researcher's policy denies
    // `delegate` at the ToolSearch LOAD step — "ToolSearch(load): delegate —
    // denied by this agent's policy" — so the delegate call itself NEVER
    // happens (a denied load means the tool never becomes callable), and the
    // bare `delegate` tool-call badge this test used to wait 90s for CANNOT
    // exist. The refusal DOES have a visible surface, one layer up: the
    // denied ToolSearch call renders as an errored tool-call badge
    // (toolVisibility.ts's ToolSearch case forces visibility on error — a
    // denied load has no other narrator), and GenericToolCall marks it
    // "Failed" in the collapsed header (toolStatusConfig's error label).
    // The BDD guarantee ("the tool dispatcher refuses a leaf's delegate
    // attempt by policy") is intact — it is enforced one step EARLIER than
    // the old assertion assumed (at load, not at dispatch) — and the
    // zero-rows guard at the end of this test still proves no grandchild
    // subagent_start ever fires. The assertion accepts BOTH refusal surfaces
    // so the spec stays true if the backend ever moves the denial to
    // dispatch time: a `delegate` badge (denied at dispatch — the old
    // assertion's exact shape) OR the denied ToolSearch badge (denied at
    // load — the gateway-log-verified shape today).
    const refusedDelegateAttempt = page
      .locator('[data-testid="tool-call-badge"][data-tool="delegate"]')
      .or(page.locator('[data-testid="tool-call-badge"][data-tool="ToolSearch"]'))
      .first()
    await expect(
      refusedDelegateAttempt,
      'Researcher must show its own (refused) delegate attempt in its own session — ' +
        'as a denied `delegate` badge if policy denies at dispatch, or as the denied ' +
        'ToolSearch(load) badge if policy denies at load (the gateway-log-verified shape today)',
    ).toBeVisible({ timeout: 90_000 });
    await expect(refusedDelegateAttempt).toContainText(/denied|failed/i, { timeout: 30_000 });

    // (4) CHILD SESSION — no grandchild ever spawned: Traces to BDD Scenario
    // 10's actual invariant, "no subagent_start frame with a grandchild
    // parent_call_id is emitted". Direct replacement for "no nested
    // subagent-collapsed inside the expanded region" (FR-H-006): checked
    // from INSIDE the child's own session (where a grandchild's row would
    // live, if one existed) rather than as a nested element of a card that
    // no longer exists.
    //
    // ActivityBar.tsx mounts NOTHING when there is no open/failed agent
    // child (its own shouldMount gate) — a policy-refused delegate call
    // never reaches Launch, so the bar most likely never mounts here at
    // all. But Radix's Sheet unmounts its CONTENT while closed, so if the
    // bar DID mount for some other reason, an unopened panel would hide a
    // real row rather than proving its absence — open it first so a count
    // of 0 is never a false negative from an unopened Sheet.
    const childActivityBar = page.locator('[data-testid="activity-bar"]');
    if ((await childActivityBar.count()) > 0) {
      await openActivityPanel(page);
    }
    await expect(
      page.locator('[data-testid="activity-row"]'),
      'Researcher must show zero Activity-panel rows of its own — the refused delegate call never produced a grandchild subagent_start (FR-H-006)',
    ).toHaveCount(0);
  },
);

// ────────────────────────────────────────────────────────────────────────────────
// (b) sibling delegate calls — Scenario 13
// BDD: Given the assistant emits two delegate frames with call_ids c1 then c2
//      When the chat renders the message
//      Then two distinct SubagentBlock elements appear, in the order (c1, c2)
//      And each expands independently without affecting the other
//
// Traces to: sprint-h-subagent-block-spec.md TDD row 22, BDD Scenario 13, lines 334-342
// ────────────────────────────────────────────────────────────────────────────────
test(
  '(b) sibling delegate calls: two back-to-back delegate calls produce two independent rows and two distinct gateway-registered child sessions (first open control clicked through and cross-checked against subagent_start frames)',
  async ({ page }) => {
    requireApiKey();
    // 240s: two independent-but-trivial child turns ("reply with one word,
    // use no tools") plus opening each child's own session in turn. Not
    // test.slow()'s inherited 270s — re-derived because the new assertions
    // (delegate chips + panel rows) resolve as soon as the PARENT's own
    // turn emits both tool calls, without needing to wait for a nested,
    // client-rendered expand/collapse sequence the old test also budgeted
    // for.
    test.setTimeout(240_000);

    await startFreshChat(page);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    // Confirm the composer is genuinely usable — enabled AND the socket is
    // actually open, not merely queueing. toBeEnabled() alone no longer
    // implies "connected" since the #105 offline-queue fix (2fa26e6a): see
    // waitForConnected's doc comment in fixtures/selectors.ts. Without this,
    // a page-load-time reconnect blip can leave the composer looking usable
    // while the very first message (the one that triggers `delegate`) lands
    // in the outbound queue instead of the wire, and this test then hangs to
    // its full timeout waiting on a chip that will never appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    const labelOne = `sibling-one-${Date.now()}`;
    const labelTwo = `sibling-two-${Date.now()}`;

    // Deterministic prompt: explicit, numbered, no prose.
    await input.fill(
      [
        'Call the `delegate` tool exactly TWO times, in sequence. No other tools. No prose answer until both delegations have been issued.',
        '',
        'First call (do this first):',
        `  delegate(label="${labelOne}", task="Reply with the word done-one. Use no tools.")`,
        '',
        'Second call (do this immediately after the first returns):',
        `  delegate(label="${labelTwo}", task="Reply with the word done-two. Use no tools.")`,
        '',
        'Issue both delegate tool calls now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // (1) WIRE — child identity from the gateway's own frames, not only
    // from the UI. pkg/agent/steer_frames.go::deliverSubagentStart puts
    // task_label and child_session_id on every subagent_start frame
    // (session_id there is the PARENT's session). Captured per page in
    // beforeEach (before page.goto('/'), see it) and read via framesByPage.
    const frames = framesByPage.get(page)!;
    const childStart = (label: string): string | null => {
      const start = frames.find(
        (f) => f['type'] === 'subagent_start' && f['task_label'] === label,
      );
      if (!start) return null;
      const child = start['child_session_id'];
      return typeof child === 'string' ? child : null;
    };

    // (2) THREAD — the guard: zero subagent-collapsed elements, ever.
    await expect(page.locator('[data-testid="subagent-collapsed"]')).toHaveCount(0);

    // (3) THREAD — at least 2 sibling delegate chips.
    // Traces to: BDD Scenario 13 — "two distinct SubagentBlock elements".
    const delegateBadges = page.locator('[data-testid="tool-call-badge"][data-tool="delegate"]');
    await expect(delegateBadges.first()).toBeVisible({ timeout: 60_000 });

    // (4) PANEL — open the panel at the FIRST in-flight moment: the first
    // chip renders the instant the parent emits the call; the child's
    // subagent_start arrives ~immediately after, putting an agent item in
    // `running` — ActivityBar.tsx's shouldMount (= hasOpenAgentChildren ||
    // panelOpen || hasFailedRecent, ActivityBar.tsx:94) is satisfied and the
    // bar is mounted. Once panelOpen is set it keeps the bar mounted through
    // every later gap in child activity on this same ChatScreen mount (the
    // panel is component-local React state; nothing navigates before the
    // openOne click below). The children here are trivial ("reply with one
    // word", no tools) and CI run 36026415761 shows them completing within
    // ~20s — faster than the old open-after-settle-loop position could
    // reliably catch (its 4/4 failures at ~22s were exactly this race lost).
    await openActivityPanel(page);

    // (5) THREAD — with the panel open (which does not touch the thread),
    // require both sibling chips. toHaveCount polls until the count EQUALS 2
    // and returns the moment it does — it does not promise the model is
    // finished. The prompt asks for exactly two delegate calls, and a real
    // model usually complies, but "usually" is the whole problem: a third
    // call landing during the assertions below would make the row-count
    // assertion fail on a run where nothing about the PRODUCT was wrong
    // (this settle pattern is preserved from before this rewrite — it is
    // not part of what ADR-091 changed). How many times the model chooses to
    // call `delegate` is not a product invariant this test can enforce. What
    // IS the invariant — and what BDD Scenario 13 is actually about — is
    // that sibling children are independent: each has its own row and its
    // own distinct session. So: settle, snapshot the count, and hold that
    // snapshot as the INVARIANT.
    await expect(delegateBadges).toHaveCount(2, { timeout: 60_000 });
    let stableCount = await delegateBadges.count();
    for (let i = 0; i < 6; i++) {
      await page.waitForTimeout(500);
      const now = await delegateBadges.count();
      if (now === stableCount) break;
      stableCount = now;
    }
    expect(
      stableCount,
      'at least 2 sibling delegate chips are required to test independent children',
    ).toBeGreaterThanOrEqual(2);

    // (6) PANEL — one row per sibling, matching the settled thread count.
    // The direct replacement for "two distinct SubagentBlock elements":
    // each child has its own row (ActivityPanel.tsx). A row enters at its
    // child's subagent_start (running) and is RETAINED after completion —
    // useRunningActivity's recentlyFinished keeps finished items (successes
    // included, cap 8), so an early-finishing child still has its row here.
    const rowOne = page.locator('[data-testid="activity-row"]', { hasText: labelOne });
    const rowTwo = page.locator('[data-testid="activity-row"]', { hasText: labelTwo });
    await expect(rowOne).toBeVisible({ timeout: 30_000 });
    await expect(rowTwo).toBeVisible({ timeout: 30_000 });
    await expect(
      page.locator('[data-testid="activity-row"]'),
      'the Activity panel must carry exactly one row per settled sibling delegate chip',
    ).toHaveCount(stableCount);

    // (7) DIFFERENTIATION — each row's open control targets a genuinely
    // DIFFERENT child session; independence is a property of the children,
    // not just of the UI state (BDD Scenario 13's underlying invariant).
    const parentSurface = page.locator('[data-active-session-id]').first();
    const parentSessionID = await parentSurface.getAttribute('data-active-session-id');

    const openOne = rowOne.locator('[data-testid="activity-row-open"]');
    const openTwo = rowTwo.locator('[data-testid="activity-row-open"]');
    await expect(openOne).toBeVisible({ timeout: 15_000 });
    await expect(openTwo).toBeVisible({ timeout: 15_000 });

    // (8) CHILD SESSION — click the FIRST sibling's open control live: the
    // chat surface must bind to a session that is neither the parent's nor
    // the '__pending' sentinel, and the bound id must equal the
    // child_session_id the gateway itself put on the subagent_start frame
    // for the first sibling — the open control's navigation cross-checked
    // against wire identity (steer_frames.go).
    await openOne.click();
    const childOneSessionID = await waitForBoundSession(page, [parentSessionID]);
    await expect
      .poll(() => childStart(labelOne), { timeout: 30_000 })
      .not.toBeNull();
    const wireChildOne = childStart(labelOne);
    expect(
      childOneSessionID,
      'the open control must bind the chat surface to the gateway-registered child session for the first sibling',
    ).toBe(wireChildOne);

    // (9) WIRE DISTINCTNESS — the second sibling must be a genuinely
    // different session. The old test proved distinctness by navigating back
    // with page.goto(parent) and issuing a SECOND openActivityPanel — a
    // bar-dependent re-open that cannot run at idle (shouldMount false: both
    // children done, panelOpen reset by the navigation remount, nothing
    // failed) and that failed 4/4 at ~22s in CI run 36026415761. The
    // distinctness invariant itself moves to the wire: two subagent_start
    // frames with different child_session_id values (registered under
    // different task_labels) ARE two independent children — the underlying
    // fact the old double click-through indirectly established. openTwo is
    // asserted rendered above; its click exercises the same handler as
    // openOne (same ActivityPanel row component), so the second live
    // navigation would only re-prove an already-proven mechanism while
    // reintroducing the bar-dependency that broke this test.
    await expect
      .poll(() => childStart(labelTwo), { timeout: 60_000 })
      .not.toBeNull();
    const wireChildTwo = childStart(labelTwo);
    expect(
      wireChildTwo,
      'the second sibling must have its own child session (gateway-registered)',
    ).not.toBe(parentSessionID);
    expect(
      wireChildTwo,
      'sibling delegate calls must produce two DIFFERENT child sessions — independence is a property of the children, not just of the UI state',
    ).not.toBe(wireChildOne);
  },
);

// (c) live step counter — DELETED 2026-09-24 (lane sq-gwfix), not re-pointed.
//
// Was: US-4/Scenario 2 — the collapsed header's step-count text incrementing
// visibly (0→1→...→≥3) as a sub-turn fired ≥3 tool_call_start frames, polled
// via [data-testid="subagent-step-counter"] INSIDE the parent's own card.
// Traced to sprint-h-subagent-block-spec.md TDD row 23, BDD Scenario 2.
//
// This is the one test in this file NOT re-pointed at the surface that
// replaced SubagentBlock, because there is no surface that carries an
// equivalent guarantee — ADR-091 D10 deleted per-step detail from the
// parent's view ON PURPOSE, not just relocated it: "a span carries no step
// detail any more" (ActivityPanel.tsx's own doc comment). The nearest thing
// left, ActivityRow's `statusLine`, is qualitatively different, not a
// weaker version of the same signal:
//   - It is fed by `subagent_message` frames (steer_frames.go's
//     deliverSubagentMessage), themselves gated behind audience-scoped
//     upward delivery (steer_audience.go::SteerUpwardDeliverer.Deliver) —
//     an opportunistic, LLM-authored progress note the child chooses to
//     send, not a deterministic per-tool-call counter driven by
//     tool_call_start frames.
//   - It is free text, not a number — "the step counter shows N steps,
//     N >= 3" has no equivalent assertion once the underlying signal is a
//     sentence a child may or may not have said yet.
// A rewrite that polled statusLine for "some text eventually appears" would
// not be a weaker version of Scenario 2's oracle, it would be a DIFFERENT,
// much looser claim ("the child said something at some point") dressed up
// to look like a re-point. Per this lane's brief — "I would rather lose a
// test deliberately than keep a vacuous one" — deleting it is the honest
// choice; keeping a test that polls prose for the WORD "steps" would have
// been vacuous by construction (nothing in the new architecture ever emits
// that word). The Go-level, deterministic half of this guarantee (a
// multi-tool-call sub-turn genuinely executes every call in order) is
// unaffected by any of this and is not this file's job to re-cover — it
// lives at the tool-dispatch layer, not the UI.
//
// ────────────────────────────────────────────────────────────────────────────────
// (d) real-LLM smoke — US-1
// BDD: Uses OpenRouter CI (OPENROUTER_API_KEY_CI env). Drives a real delegate turn
//      end-to-end and asserts BOTH that a SubagentBlock renders and behaves, and
//      that no JS console errors fire during the turn.
//
// Traces to: sprint-h-subagent-block-spec.md TDD row 24, SC-H-003, US-1
//
// ─── 2026-07-28 (RC6): this test was a coin flip, and its two defects were the
// prompt, not the product. It used to ask, in deliberately vague natural
// language, "Please have one of your subagents check what files are in the /tmp
// directory", and then tolerate a non-delegating answer via an `else` branch.
// Both halves of that were wrong:
//
// 1. TWO TOOLS ANSWER THAT SENTENCE, AND THEY DIFFER ~10x IN RUNTIME. Since
//    ADR-052 wave1 (f9bcfae7) the catalog also contains `create_task` +
//    `run_task`. `create_task` is a FULL-manifest tool (pkg/tools/manifest.go —
//    always sent as a callable def) while `delegate` is LAZY (needs a
//    `ToolSearch` round-trip first), so the model is structurally biased toward
//    the task route. In the failing run Jim said "I'll delegate this to Worker"
//    and then reached for create_task + run_task; `run_task` blocked the parent
//    turn for 304 SECONDS, busting the 300s budget by 4s. The passing retry used
//    `delegate` and finished in 78s. Every delegate-route turn in that shard was
//    20-80s. So the budget was never the problem and MUST NOT be raised —
//    bumping 300s → 400s would have made this green while blessing a five-minute
//    `run_task` as normal. (That runtime is a real product bug, tracked
//    separately; it is not this test's job to absorb it.)
// 2. `/tmp` IS OUTSIDE THE SANDBOX. Every route was guaranteed to flail on
//    blocked tools — `bash` "path outside working dir", `list_directory`
//    "outside the effective filesystem scope" — which maximised retries and
//    burned budget. Even the "passing" retry never listed /tmp; it passed only
//    because the assertion tolerated a failed delegation.
//
// The fix names the tool (exactly as this file's passing siblings (a)/(b)/(e)
// already do) and retargets the work INSIDE the workspace so it can actually
// succeed. This STRENGTHENS the test: the SubagentBlock branch used to execute
// roughly half the time and be silently skipped otherwise, so the test
// advertised coverage it did not provide. It now runs every time, which is why
// the `else` branch is gone — a turn that renders no block is a real failure.
// The test is renamed accordingly: what it guards is a clean real-LLM
// delegation turn, not a wager on the model's tool choice.
// ────────────────────────────────────────────────────────────────────────────────
test(
  '(d) real-LLM smoke: a live delegate turn renders a delegate chip and Activity panel row with no console errors',
  async ({ page, consoleErrors }) => {
    // T0.1: OPENROUTER_API_KEY_CI soft-skip removed. The key is required in CI.
    requireApiKey();
    // 360s budget, matching cancel-cross-channel.spec.ts's T24a/T24b precedent:
    // glm-5.2 (the standard e2e model, swapped in for the old gemini-2.5-flash
    // pick — see tests/e2e/fixtures/onboard-via-api.ts) can genuinely take a
    // couple of minutes for a delegate round-trip under suite load. A too-tight
    // budget here doesn't just fail this assertion — this repo's
    // cancelOnTeardown fixture (tests/e2e/fixtures/console-errors.ts) then
    // clicks Stop on teardown, which cancels the still-in-flight delegate turn
    // too, producing a confusing "context canceled" server-side error that
    // looks unrelated to the actual root cause (a plain timeout). Root-caused
    // via direct gateway-log instrumentation on 2026-07-07 — see PR history.
    // Deliberately UNCHANGED by RC6, and unaffected by this rewrite: the
    // delegate chip and Activity row resolve no slower than the deleted card
    // did (same underlying frames), so the same budget still applies.
    test.setTimeout(360_000);

    await startFreshChat(page);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    // Confirm the composer is genuinely usable — enabled AND the socket is
    // actually open, not merely queueing. toBeEnabled() alone no longer
    // implies "connected" since the #105 offline-queue fix (2fa26e6a): see
    // waitForConnected's doc comment in fixtures/selectors.ts. Without this,
    // a page-load-time reconnect blip can leave the composer looking usable
    // while the very first message (the one that triggers `delegate`) lands
    // in the outbound queue instead of the wire, and this test then hangs to
    // its full timeout waiting on a chip that will never appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    // Name the tool, and keep the subagent's work inside the sandbox so it can
    // actually complete (see the RC6 note above). `list_directory` on "." is
    // the agent's own workspace root and is known-permitted; `/tmp` is not.
    // No "do not call any other tool" guardrail here: `delegate` is a LAZY
    // manifest tool, so the model legitimately calls `ToolSearch` first.
    await input.fill(
      [
        'Use the `delegate` tool exactly once to hand this to a subagent:',
        '  task: "List the files in your current working directory (path `.`) and report back what you find."',
        'Then summarise what the subagent reported.',
      ].join('\n'),
    );
    await input.press('Enter');

    // The delegate chip is the FIRST thing to appear — it renders as soon as
    // the parent's own turn emits the tool call, well before the parent's
    // final prose. Assert it before the completed-message count so a turn
    // that never delegates fails HERE, naming the missing chip, instead of
    // 300s later as a bare "expected 1, received 0" on the assistant-message
    // count (which was how the RC6 failure presented and why it read as a
    // timeout).
    //
    // Use .first(): glm-5.2 occasionally fans out to more than one subagent,
    // which would make a bare locator strict-mode-fail. We only need >=1.
    const delegateBadge = page.locator('[data-testid="tool-call-badge"][data-tool="delegate"]').first();
    await expect(
      delegateBadge,
      'the prompt names `delegate` explicitly, so a sub-turn must start and ' +
        'render its delegate chip. No chip means the model either took the ' +
        'create_task/run_task route instead (RC6) or delegation is broken — ' +
        'check the gateway log for the actual tool calls before touching this ' +
        'timeout.',
    ).toBeVisible({ timeout: 240_000 });

    // Guard: zero subagent-collapsed elements, ever — ADR-091 D7/D10
    // deleted the card unconditionally.
    await expect(page.locator('[data-testid="subagent-collapsed"]')).toHaveCount(0);

    // Open the Activity panel NOW, while the delegation is in flight — the
    // moment the delegate chip is visible the child's span is open (or its
    // subagent_start is within a second of arriving), which satisfies
    // ActivityBar.tsx's shouldMount (= hasOpenAgentChildren || panelOpen ||
    // hasFailedRecent, ActivityBar.tsx:94), and panelOpen then keeps the bar
    // mounted on this same ChatScreen mount for the rest of the test. The OLD
    // position — after the assistantMessages(1) completion wait — is why this
    // test failed 4/4 at ~25s in CI run 36026415761: by the time the parent
    // turn finished, the child was done too, nothing had failed, and the
    // panel was still closed — shouldMount false, the bar mounts NOTHING, and
    // the helper's 15s bar wait burns out. (The ~25s failure time is a FAST
    // model completing, not a slow one.)
    await openActivityPanel(page);

    // Now wait for the parent turn to actually finish. 300s total leaves ~60s
    // of the 360s test-level ceiling for the row + a11y checks below. The
    // panel stays open across this wait (panelOpen is React state on the
    // UNCHANGED ChatScreen mount — nothing between the open above and the row
    // checks below navigates), and the child's row is retained through
    // completion by useRunningActivity's recentlyFinished (successes
    // included, cap 8).
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 300_000 });
    const row = page.locator('[data-testid="activity-row"]').first();
    await expect(
      row,
      'a completed delegation must leave a row in the Activity panel',
    ).toBeVisible({ timeout: 10_000 });
    await expect(row.locator('[data-testid="activity-row-open"]')).toBeVisible({ timeout: 10_000 });

    // a11y check covering both surfaces that replaced SubagentBlock's
    // collapsed/expanded states: the thread's delegate chip and the open
    // Activity panel's row.
    // Traces to: sprint-h-subagent-block-spec.md Scenario 11, line 316
    await expectA11yClean(page, {
      include: ['[data-testid="tool-call-badge"]', '[data-testid="activity-row"]'],
    });

    // Zero unexpected JS console errors (captured by the consoleErrors fixture,
    // which asserts automatically at test end). Force-reference the binding so
    // the fixture is active.
    void consoleErrors;
  },
);

// ────────────────────────────────────────────────────────────────────────────────
// Axe integration: WCAG 2.1 AA against the surfaces that replaced SubagentBlock.
// Tests both collapsed and expanded states to satisfy US-5 / BDD Scenario 11.
// Traces to: sprint-h-subagent-block-spec.md TDD row 17 (component) + SC-H-006 (E2E layer)
//
// REWRITTEN 2026-09-24 (lane sq-gwfix): SubagentBlock had ONE collapse/expand
// affordance carrying both states. ADR-091 D7/D10 split that into TWO real
// surfaces — the delegate chip's own toggle (ToolCallBadge.tsx) and the
// Activity panel row (ActivityPanel.tsx) — so this test now covers three
// checks instead of two: the delegate chip collapsed, the delegate chip
// expanded (its own params/result disclosure — the same KIND of affordance
// SubagentBlock used to have, just on a different component), and the
// Activity panel row (the surface that replaced "expand to see the child's
// status"). Strictly more coverage than the two states this test used to
// check, not less.
// ────────────────────────────────────────────────────────────────────────────────
test(
  '(e) axe baseline: the delegate chip and Activity panel row are WCAG 2.1 AA clean',
  async ({ page }) => {
    requireApiKey();
    // 300s, replacing the inherited test.slow() (270s): unlike the old
    // two-state check, expanding the delegate chip's OWN toggle needs the
    // delegate call to reach a TERMINAL status first (ToolCallBadge.tsx
    // disables tool-call-toggle while `isRunning` — "while running, there
    // is nothing to expand"), i.e. the child must actually finish its bash
    // echo, not just start. Budgeted like the other child-completion waits
    // in this shard (subagent.spec.ts test (a), handoff.spec.ts test (b)).
    test.setTimeout(300_000);

    await startFreshChat(page);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    // Confirm the composer is genuinely usable — enabled AND the socket is
    // actually open, not merely queueing. toBeEnabled() alone no longer
    // implies "connected" since the #105 offline-queue fix (2fa26e6a): see
    // waitForConnected's doc comment in fixtures/selectors.ts. Without this,
    // a page-load-time reconnect blip can leave the composer looking usable
    // while the very first message (the one that triggers `delegate`) lands
    // in the outbound queue instead of the wire, and this test then hangs to
    // its full timeout waiting on a chip that will never appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    const label = `axe-test-subagent-${Date.now()}`;

    // The prompt gives the subagent a real reason to exist (running a shell
    // command in isolation) so the LLM doesn't shortcut and answer directly.
    // The previous prompt asked the subagent to "reply ok with no tools" —
    // a smarter LLM correctly skipped delegate because the task was trivial.
    await input.fill(
      [
        'Use the `delegate` tool right now to hand off work to a subagent.',
        `Set label to "${label}".`,
        'Set task to: "Use the bash tool to run `echo hello-from-subagent` and return the exact stdout."',
        'Do not run bash yourself — hand this off by calling delegate now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // Structural assertion: wait for the delegate chip to appear.
    // With temperature=0+seed=42 the LLM must comply — test fails honestly if it doesn't.
    const delegateBadge = page.locator('[data-testid="tool-call-badge"][data-tool="delegate"]').first();
    await expect(delegateBadge).toBeVisible({ timeout: 60_000 });

    // Guard: zero subagent-collapsed elements, ever.
    await expect(page.locator('[data-testid="subagent-collapsed"]')).toHaveCount(0);

    // Test 3 FIRST, moved up (was last) — IN-FLIGHT ANCHOR, same fix as
    // delegation-hidden.spec.ts and this file's tests (b)/(d) (commit
    // 8c4892c29 — this test (e) was missed by that pass). ActivityBar.tsx
    // only mounts the bar while shouldMount(=hasOpenAgentChildren||
    // panelOpen||hasFailedRecent) is true; a purely-successful completed
    // delegation satisfies none of the three once its subagent_end has
    // landed (recentlyFinished's cap only keeps the bar reachable for
    // FAILED items — ActivityBar.tsx's isFailedStatus). The old ordering
    // opened the panel only after both axe passes and the delegate toggle
    // reaching a terminal state; CI job 107857384500's failure trace shows
    // that was already too late — the child had finished (delegateToggle
    // went enabled a mere 10ms after the wait for it started in that same
    // run, proving the delegate TOOL CALL's own status is an async launch
    // ack, NOT a "child is done" signal, so it bought no real safety
    // margin) and the bar never appeared even once across the full 15s
    // timeout; the failure's own ARIA snapshot already showed the complete
    // "delegation is done" transcript. Opening right after the in-flight
    // anchor, before anything else runs, is the only reliably-mountable
    // moment — see the panel-close comment below for why this can't simply
    // stay open through Tests 1/2 instead.
    await openActivityPanel(page);
    const row = page.locator('[data-testid="activity-row"]', { hasText: label });
    await expect(row).toBeVisible({ timeout: 30_000 });
    await expectA11yClean(page, {
      include: ['[data-testid="activity-row"]'],
    });

    // Close the panel before Tests 1/2 (moved below, was first): ActivityPanel.tsx's
    // <Sheet> is a modal Radix Dialog (`modal` defaults true, never
    // overridden here) whose Portal mounts at document.body — Radix's
    // hideOthers applies aria-hidden to everything outside that portal
    // while open, INCLUDING the delegate chip in the main thread. Scanning
    // the chip with the panel still open would not fail Tests 1/2 — it
    // would silently scan nothing (axe skips aria-hidden subtrees),
    // turning both into a vacuous pass instead of a real check. Escape is
    // Radix's own built-in close path (same pattern as
    // accessibility.spec.ts's sign-in-dialog test) and flips panelOpen
    // back to false via ActivityBar.tsx's onOpenChange={setPanelOpen}.
    await page.keyboard.press('Escape');
    await expect(row).not.toBeVisible();

    // Test 1: axe against the delegate chip's COLLAPSED state.
    // Traces to: sprint-h-subagent-block-spec.md Scenario 11 — "collapsed SubagentBlock"
    await expectA11yClean(page, {
      include: ['[data-testid="tool-call-badge"]'],
    });

    // Test 2: expand the delegate chip's own disclosure (ToolCallBadge.tsx's
    // tool-call-toggle) and run axe again — this is the delegate chip's own
    // params/result panel, the same KIND of collapse/expand SubagentBlock
    // used to carry, now on the surface that actually renders it.
    // Traces to: sprint-h-subagent-block-spec.md Scenario 11 — "expanded SubagentBlock"
    const delegateToggle = delegateBadge.locator('[data-testid="tool-call-toggle"]');
    await expect(delegateToggle).toBeEnabled({ timeout: 150_000 });
    await delegateToggle.click();
    await expectA11yClean(page, {
      include: ['[data-testid="tool-call-badge"]'],
    });
  },
);
