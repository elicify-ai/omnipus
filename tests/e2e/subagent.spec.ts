// Sprint H · Subagent Collapsed-Block UI — E2E Tests
// Traces to: sprint-h-subagent-block-spec.md TDD rows 21, 22, 23, 24
//
// ARCHITECTURE NOTE: The spec originally called for a "scenario-provider path" (deterministic
// scripted LLM) gated behind the `test_harness` build tag. That mechanism was removed
// 2026-05-10 — these tests use a real LLM (OPENROUTER_API_KEY_CI required) with temperature=0
// and seed=42 plumbed into OpenRouter requests for maximum determinism (Wave 2.1).
//
// data-testid cross-reference:
//   - [data-testid="subagent-collapsed"]    — SubagentBlock.tsx (collapsed header button)
//   - [data-testid="subagent-expanded"]     — SubagentBlock.tsx (expanded body)
//   - [data-testid="subagent-step-counter"] — SubagentBlock.tsx (step count span)
//   - [data-testid="subagent-live-step"]    — SubagentBlock.tsx (individual step wrapper)
//   - [data-testid="tool-call-badge"]       — ToolCallBadge.tsx

import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';
import { chatInput, assistantMessages, selectAgent, waitForConnected } from './fixtures/selectors';
import { enableVerboseChat } from './fixtures/verbose-chat';

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
  const newChat = page.getByRole('banner').getByRole('button', { name: 'New Chat' });
  if (await newChat.isVisible({ timeout: 5_000 })) {
    await newChat.click();
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });
  }
  // Route to Jim — Mia declines to delegate (see note above).
  await selectAgent(page, /Jim/i);
}

test.beforeEach(async ({ page }) => {
  // Delegation visuals (SubagentBlock cards) are verbose-only in the chat
  // thread since commit 8e1bf1b9 (shouldRenderSubagentSpan gates on
  // verboseChatEnabled, default false — src/store/chatPreferences.ts). This
  // whole file asserts the sub-turn MECHANICS (spawn, grandchild refusal,
  // sibling independence, live step counter, a11y) via
  // [data-testid="subagent-collapsed"] as its thread-based signal, so it
  // opts into verbose chat here to keep that signal working — independent
  // of the default (non-verbose) display policy, which is covered
  // separately by delegation-hidden.spec.ts.
  await enableVerboseChat(page);
  await page.goto('/');
});

// ────────────────────────────────────────────────────────────────────────────────
// (a) grandchild refused — Scenario 10, US-3
// BDD: Given a subagent sub-turn is running
//      When the sub-turn's LLM attempts a tool call with name="delegate"
//      Then the tool dispatcher returns an unknown-tool error to the LLM
//      And no subagent_start frame with a grandchild parent_call_id is emitted
//      And the parent's transcript ToolCalls contains exactly one delegate entry
//
// Traces to: sprint-h-subagent-block-spec.md TDD row 21, BDD Scenario 10, lines 304-313
// ────────────────────────────────────────────────────────────────────────────────
test(
  '(a) grandchild refused: subagent attempting delegate gets unknown-tool error, no nested block',
  async ({ page }) => {
    requireApiKey();
    // 420s total: this test triggers TWO LLM round-trips (parent delegate +
    // subagent's failed grandchild-delegate attempt) before the collapsed block
    // settles, so the budget is wider than the sibling subagent tests.
    // test.slow()'s 270s was insufficient in CI under suite load — observed
    // 4×156s failures consistently exceeding the 150s collapsed budget.
    test.setTimeout(420_000);

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
    // its full timeout waiting on a subagent-collapsed block that will never
    // appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    // Deterministic prompt with temperature=0+seed=42 now plumbed into OpenRouter.
    // Commanding, specific: exact tool name, task, and behavior with no optional phrasing.
    await input.fill(
      [
        'Call the `delegate` tool exactly once, right now, with these arguments:',
        '  label: "grandchild test"',
        '  task: "You are the subagent. Your one and only job is to call the `delegate` tool yourself to attempt to delegate to a grandchild subagent with task \\"hello\\". If delegate is not in your available tools, report the exact error you receive. Do not do anything else."',
        'Do not reply in prose. Do not call any other tool. Call delegate now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // Structural assertion: wait for at least one subagent-collapsed to appear (the parent delegate).
    // With temperature=0+seed=42 the LLM must comply — if it doesn't, the test fails honestly.
    // 300s budget: this test needs the parent delegate AND the subagent's failed
    // grandchild-delegate round-trip to both complete; under CI load GLM-5v-turbo
    // can take 150-280s for that pair. 150s gave 4×156s timeouts in CI even
    // though local-isolated runs land in 20-40s.
    const collapsedBlocks = page.locator('[data-testid="subagent-collapsed"]');
    await expect(collapsedBlocks.first()).toBeVisible({ timeout: 300_000 });

    const blockCount = await collapsedBlocks.count();

    // Expand the parent block to inspect inner content.
    await collapsedBlocks.first().click();
    const expandedBlock = page.locator('[data-testid="subagent-expanded"]');
    await expect(expandedBlock).toBeVisible({ timeout: 10_000 });

    // Structural assertion: no nested [data-testid="subagent-collapsed"] inside the expanded region.
    // Traces to: BDD Scenario 10 — "no subagent_start frame with a grandchild parent_call_id"
    const nestedCollapsed = expandedBlock.locator('[data-testid="subagent-collapsed"]');
    const nestedCount = await nestedCollapsed.count();
    expect(nestedCount, 'expanded SubagentBlock must contain zero nested subagent-collapsed elements (grandchildren are forbidden — FR-H-006)').toBe(0);

    // Structural assertion: exactly one parent-level collapsed block.
    expect(blockCount, 'exactly one SubagentBlock at parent level — grandchild attempt must not create a second block').toBe(1);

    // Structural assertion: expanded block has child elements (steps or error message).
    const children = await expandedBlock.locator('> *').count();
    expect(children, 'expanded block must have content (steps or error message)').toBeGreaterThan(0);
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
  '(b) sibling delegate calls: two back-to-back delegate calls render as two independent SubagentBlocks',
  async ({ page }) => {
    requireApiKey();
    // test.slow() triples the global 90s test timeout to 270s. Subagent
    // delegation + execution can take 30-90s end-to-end under suite load even
    // though the same test passes in 5-15s alone.
    test.slow();

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
    // its full timeout waiting on a subagent-collapsed block that will never
    // appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    // Deterministic prompt: explicit, numbered, no prose.
    await input.fill(
      [
        'Call the `delegate` tool exactly TWO times, in sequence. No other tools. No prose answer until both delegations have been issued.',
        '',
        'First call (do this first):',
        '  delegate(label="task one", task="Reply with the word done-one. Use no tools.")',
        '',
        'Second call (do this immediately after the first returns):',
        '  delegate(label="task two", task="Reply with the word done-two. Use no tools.")',
        '',
        'Issue both delegate tool calls now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // Structural assertion: wait for the first collapsed block.
    const collapsedBlocks = page.locator('[data-testid="subagent-collapsed"]');
    await expect(collapsedBlocks.first()).toBeVisible({ timeout: 60_000 });

    // Structural assertion: wait for exactly 2 sibling blocks.
    // Traces to: BDD Scenario 13 — "two distinct SubagentBlock elements"
    await expect(collapsedBlocks).toHaveCount(2, { timeout: 60_000 });

    // Verify independent expansion: expand first — second should remain collapsed.
    await collapsedBlocks.nth(0).click();
    const expandedBlocks = page.locator('[data-testid="subagent-expanded"]');
    await expect(expandedBlocks).toHaveCount(1, { timeout: 10_000 });

    // Expand second — both should now be expanded independently.
    await collapsedBlocks.nth(1).click();
    await expect(expandedBlocks).toHaveCount(2, { timeout: 10_000 });

    // Collapse first — second should remain expanded.
    await collapsedBlocks.nth(0).click();
    await expect(expandedBlocks).toHaveCount(1, { timeout: 10_000 });

    // Differentiation test: two different blocks expanded/collapsed independently.
    const finalCount = await collapsedBlocks.count();
    expect(finalCount, 'exactly 2 sibling SubagentBlocks must be rendered for two delegate calls').toBe(2);
  },
);

// ────────────────────────────────────────────────────────────────────────────────
// (c) live step counter — US-4, Scenario 2
// BDD: Given a sub-turn that fires ≥3 tool_call_start frames with matching parent_call_id
//      When the run progresses
//      Then the collapsed header's step count text increments visibly (0→1→...→≥3)
//
// Traces to: sprint-h-subagent-block-spec.md TDD row 23, BDD Scenario 2, lines 221-229
// ────────────────────────────────────────────────────────────────────────────────
test(
  '(c) live step counter: collapsed header step count increments during multi-step sub-turn',
  async ({ page }) => {
    // T0.1 (re-investigated): the `test.skip(true, ...)` that used to sit here
    // ran BEFORE `test.slow()` below — since test.skip() throws/aborts test
    // execution immediately, test.slow() (which triples the global 90s
    // timeout to 270s) never actually executed. So the ">40s under load"
    // flake this skip cited was never actually covered by the wider budget;
    // the test was skipping itself with LESS headroom than it appeared to
    // have on paper. Removing the skip lets test.slow() apply for real.
    // See tests/e2e/README.md and the removed SKIP_ALLOWLIST entry (#155)
    // for the prior reasoning — validated below via repeated real runs
    // rather than assumed.
    requireApiKey();
    // test.slow() triples the global 90s test timeout to 270s. Subagent
    // delegation + execution can take 30-90s end-to-end under suite load even
    // though the same test passes in 5-15s alone.
    test.slow();

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
    // its full timeout waiting on a subagent-collapsed block that will never
    // appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    // Deterministic prompt: force a single delegate call with a subagent task that mandates ≥3 tool calls.
    // read_file is always registered and does not require special permissions.
    await input.fill(
      [
        'Call the `delegate` tool exactly once, now, with these arguments:',
        '  label: "multi step counter test"',
        '  task: "You are a subagent. You MUST call the read_file tool exactly THREE times in this exact order. Do not skip any call. Do not reply in prose between them. (1) read_file with path=\\"/etc/hostname\\"; (2) read_file with path=\\"/etc/os-release\\"; (3) read_file with path=\\"/proc/version\\". After all three read_file calls have completed, reply with the single word \\"finished\\"."',
        'Do not call any other tool. Do not reply in prose. Call delegate now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // Structural assertion: wait for the collapsed block to appear.
    const collapsedBlock = page.locator('[data-testid="subagent-collapsed"]').first();
    await expect(collapsedBlock).toBeVisible({ timeout: 60_000 });

    // Structural assertion: [data-testid="subagent-step-counter"] must be present.
    // This verifies the step counter element exists in the DOM (FR-H-010).
    const stepCounter = collapsedBlock.locator('[data-testid="subagent-step-counter"]');
    await expect(stepCounter).toBeVisible({ timeout: 5_000 });

    // Poll for ≥3 steps in the step counter text.
    // Traces to: sprint-h-subagent-block-spec.md BDD Scenario 2 — "step counter shows N steps"
    let reachedThreeSteps = false;
    const deadline = Date.now() + 60_000; // 60s budget for multi-step run

    while (Date.now() < deadline) {
      // Scoped catch — only swallow stale-locator errors, rethrow others.
      const counterText = await stepCounter.textContent().catch((err: unknown) => {
        if (err instanceof Error && (err.message.includes('Element is not attached') || err.message.includes('locator handle is stale'))) return null;
        throw err;
      });
      if (!counterText) break;

      const stepMatch = counterText.match(/(\d+)\s+steps?/);
      if (stepMatch) {
        const count = parseInt(stepMatch[1], 10);
        if (count >= 3) {
          reachedThreeSteps = true;
          break;
        }
      }

      // Check if the sub-turn has finished.
      const headerText = await collapsedBlock.textContent().catch((err: unknown) => {
        if (err instanceof Error && (err.message.includes('Element is not attached') || err.message.includes('locator handle is stale'))) return null;
        throw err;
      });
      if (!headerText) break;
      const isFinished = !headerText.includes('working') && !headerText.includes('Running');
      if (isFinished && !reachedThreeSteps) break;

      await page.waitForTimeout(500);
    }

    // Hard assertion: the step counter must have reached ≥3 steps.
    // With temperature=0 the subagent must execute all three read_file calls.
    // If reachedThreeSteps is false, the product did not produce the required steps.
    if (!reachedThreeSteps) {
      // Verify at least the step counter IS rendering (not a missing testid regression).
      const finalCounterText = await stepCounter.textContent().catch(() => '');
      const anySteps = /\d+\s+steps?/.test(finalCounterText ?? '');
      if (!anySteps) {
        throw new Error(
          'PRODUCT REGRESSION: SubagentBlock appeared but [data-testid="subagent-step-counter"] rendered no step count text. ' +
          'Expected text matching /\\d+ steps?/. ' +
          'Traces to: temporal-puzzling-melody.md W2-7, sprint-h-subagent-block-spec.md FR-H-010.',
        );
      }
      throw new Error(
        'ASSERTION FAILED: LLM subagent executed fewer than 3 tool calls. ' +
        'The subagent must follow the prompt and execute 3 read_file calls. ' +
        `Step counter text at timeout: "${finalCounterText}". ` +
        'Traces to: sprint-h-subagent-block-spec.md BDD Scenario 2.',
      );
    }

    expect(reachedThreeSteps).toBe(true);
  },
);

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
  '(d) real-LLM smoke: a live delegate turn renders a working SubagentBlock with no console errors',
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
    // Deliberately UNCHANGED by RC6: see the note above on why raising it would
    // be the wrong fix.
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
    // its full timeout waiting on a subagent-collapsed block that will never
    // appear.
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

    // The delegation card is the FIRST thing to appear — the collapsed block
    // renders as soon as the sub-turn starts, well before the parent's final
    // prose. Assert it before the completed-message count so a turn that never
    // delegates fails HERE, naming the missing block, instead of 300s later as
    // a bare "expected 1, received 0" on the assistant-message count (which was
    // how the RC6 failure presented and why it read as a timeout).
    //
    // Use .first(): glm-5.2 occasionally fans out to more than one subagent,
    // which would make a bare locator strict-mode-fail. We only need >=1.
    const collapsedBlock = page.locator('[data-testid="subagent-collapsed"]').first();
    await expect(
      collapsedBlock,
      'the prompt names `delegate` explicitly, so a sub-turn must start and ' +
        'render a SubagentBlock. No block means the model either took the ' +
        'create_task/run_task route instead (RC6) or delegation is broken — ' +
        'check the gateway log for the actual tool calls before touching this ' +
        'timeout.',
    ).toBeVisible({ timeout: 240_000 });

    // Now wait for the parent turn to actually finish. 300s total leaves ~60s
    // of the 360s test-level ceiling for the expansion + a11y checks below.
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 300_000 });

    // Click to expand — basic expansion must work.
    await collapsedBlock.click();
    const expandedBlock = page.locator('[data-testid="subagent-expanded"]');
    await expect(expandedBlock).toBeVisible({ timeout: 10_000 });

    // a11y check on subagent elements (BDD Scenario 11, US-5).
    // Traces to: sprint-h-subagent-block-spec.md Scenario 11, line 316
    await expectA11yClean(page, {
      include: ['[data-testid^="subagent-"]'],
    });

    // Zero unexpected JS console errors (captured by the consoleErrors fixture,
    // which asserts automatically at test end). Force-reference the binding so
    // the fixture is active.
    void consoleErrors;
  },
);

// ────────────────────────────────────────────────────────────────────────────────
// Axe integration: WCAG 2.1 AA against SubagentBlock elements
// Tests both collapsed and expanded states to satisfy US-5 / BDD Scenario 11.
// Traces to: sprint-h-subagent-block-spec.md TDD row 17 (component) + SC-H-006 (E2E layer)
// ────────────────────────────────────────────────────────────────────────────────
test(
  '(e) axe baseline: SubagentBlock elements are WCAG 2.1 AA clean',
  async ({ page }) => {
    requireApiKey();
    // test.slow() triples the global 90s test timeout to 270s. Subagent
    // delegation + execution can take 30-90s end-to-end under suite load even
    // though the same test passes in 5-15s alone.
    test.slow();

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
    // its full timeout waiting on a subagent-collapsed block that will never
    // appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    // The prompt gives the subagent a real reason to exist (running a shell
    // command in isolation) so the LLM doesn't shortcut and answer directly.
    // The previous prompt asked the subagent to "reply ok with no tools" —
    // a smarter LLM correctly skipped delegate because the task was trivial.
    await input.fill(
      [
        'Use the `delegate` tool right now to hand off work to a subagent.',
        'Set label to "axe test subagent".',
        'Set task to: "Use the bash tool to run `echo hello-from-subagent` and return the exact stdout."',
        'Do not run bash yourself — hand this off by calling delegate now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // Structural assertion: wait for a SubagentBlock to appear.
    // With temperature=0+seed=42 the LLM must comply — test fails honestly if it doesn't.
    const collapsedBlock = page.locator('[data-testid="subagent-collapsed"]');
    await expect(collapsedBlock.first()).toBeVisible({ timeout: 60_000 });

    // Test 1: axe against collapsed state.
    // Traces to: sprint-h-subagent-block-spec.md Scenario 11 — "collapsed SubagentBlock"
    await expectA11yClean(page, {
      include: ['[data-testid^="subagent-"]'],
    });

    // Test 2: expand the block and run axe again against expanded state.
    // Traces to: sprint-h-subagent-block-spec.md Scenario 11 — "expanded SubagentBlock"
    await collapsedBlock.first().click();
    const expandedBlock = page.locator('[data-testid="subagent-expanded"]');
    await expect(expandedBlock).toBeVisible({ timeout: 10_000 });

    await expectA11yClean(page, {
      include: ['[data-testid^="subagent-"]'],
    });
  },
);
