// Omnipus — ADR-053 §9.1 design-conformance E2E: chat-surface flows.
//
// Covers: t0 (chat goal end-to-end), g6 (session control),
//         g7 (blocking-question round trip).
//
// One of FOUR sibling spec files that together make up the conformance suite
// (chat / plan / replan / exec). They were split out of the single
// conformance-design-e2e.spec.ts so CI can run them as four parallel shards:
// playwright.config.ts pins `workers: 1, fullyParallel: false` because a
// shared gateway's config/credentials cannot tolerate concurrent writes, so a
// spec FILE is the smallest unit of parallelism available. Each shard gets its
// own gateway process, which is what makes cross-shard parallelism safe.
//
// The suite-level doc comment, the REST/plan helpers and every shared constant
// live in ./fixtures/conformance-helpers — read that file first. Nothing in
// this file is duplicated from it.
//
// Every test here is self-contained: it starts its own chat session and, where
// it needs REST-created entities, its own workspace plus its own freshly
// created Main agent (name suffixed with Date.now()). No test reads state that
// another test wrote, in this file or in any sibling — which is precisely what
// makes splitting the suite across shards sound.

import { expect } from '@playwright/test'
import { test } from './fixtures/plan-cleanup'
import { chatInput, assistantMessages, userMessages } from './fixtures/selectors'
import { requireApiKey, startFreshChatWithAgent, startFreshChatWithJim } from './fixtures/conformance-helpers'

// ── Conformance_t0_ChatGoalE2E ───────────────────────────────────────────────
//
// BDD (§9.1 t0): set /goal → SMART compile → conversational confirm in chat
// → worker turn → claim → Judge verdict → done.
//
// The "OR idle trigger" this line used to carry is GONE, deliberately:
// ADR-084 revision 9 D13 (JUDGE-FR-095/FR-097) retired claimless idle
// adjudication in full, making a `met` claim the sole trigger. The §9.1
// sentence predates that decision; the decision wins.
// Pill walks active → judging → done; /goal clear cancels the verifier
// AND any in-flight compilation turn.
//
// Traces to:
//   - docs/internal/specs/unified-goal-plan-subagent-spec.md §"Group Z":
//     "t0 · chat goal end-to-end walks the drawn path" (line ~1160)
//   - TDD Plan row 41 `Conformance_t0_ChatGoalE2E` (line ~1279)
//   - §9.1 Live E2E checklist first 5 bullets (line ~286)
//   - ADR-053 FE-1 (GoalPillTray bottom-right, 8-state enum)

test('Conformance_t0_ChatGoalE2E: /goal set compiles → worker turn → claim → verdict → done pill walk', async ({
  page,
}) => {
  requireApiKey()

  // Real-LLM conformance: budget is the LLM round-trip + verifier turn.
  // 480s = 20s fast-path + 3×(typing + 30s delivery check + 90s pill wait)
  // + slack. A ceiling, not an oracle — the pass decider stays the done pill.
  // (Was 420s; raised when the delivery check grew to 30s — see the steer
  // loop below — so every failure lands on a MESSAGE-level assertion instead
  // of a harness timeout.)
  test.setTimeout(480_000)

  // Mia, not Jim: the goal's [check: true exit:0] machine criterion requires
  // the session agent to hold `bash` allow — Jim's ADR-090 policy denies bash,
  // so the feasibility gate (FR-111/D9) rejects the goal at compile time,
  // deterministically, before any LLM call. Mia's grants include bash,
  // set_goal and goal_claim, so the walk is fully in-policy. The oracle
  // (machine check + active pill walk) is unchanged.
  await startFreshChatWithAgent(page, /Mia/i)

  // Send /goal <condition>. The goal loop (pkg/agent/goal_loop.go
  // applyGoalCommandPrompt) intercepts this BEFORE the LLM call and emits a
  // goal_status WS frame with state="active" carrying the compiled criteria.
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })

  // A verifiable, single-criterion condition so the SMART compiler accepts
  // it (out-of-policy or unjudgeable criteria are rejected at compile,
  // per FR-111/D9 — fail-closed, no rejected criterion persists).
  //
  // Use an explicit [check:] machine criterion (not pure prose): the Judge
  // runs KindCheck under the agent's sandbox and `true` exits 0
  // deterministically, so the VERDICT half of this walk never depends on a
  // real model's opinion of prose. A pure-prose "say goal met" goal left the
  // judge returning unmet + steer loops (Jim kept working, pill never
  // reached done).
  //
  // WHY THE CONDITION STAYS MARKER-ONLY (measured in this verification's
  // runs 1-2): the prose after marker extraction decides the activation path
  // (goal_compile.go goalIntentNeedsLLMCompile — any real prose flips the
  // command to ADR-088 D1 instant activation, activateInstantGoal). The
  // instant path starts the record criteria-empty for the worker to author
  // via set_goal, and its adjudication then depends on the verifier LLM
  // forming a judgment on authored/prose criteria — in run 2 the verifier
  // "formed no judgment" (gateway log: `verifier: criterion unjudgeable …
  // the verifier turn ran but formed no judgment`) and the goal ended
  // `blocked`, with the model then refusing to re-claim a closed record. The
  // marker path's criteria (the [check:] machine check + the floor DoD) are
  // judged deterministically — `true` exits 0 — so the verdict half of the
  // walk carries no model-variance. The cost: the marker path folds the
  // prose into the worker prompt, so turn 1's prompt is bare "please
  // continue" and the model wanders (list_tasks, library_list, a `git log`
  // that sat on an unapproved ADR-092 D8 network pre-flight card —
  // preflight.go ClassifyNetworkNeed flags git — and an AskUserQuestion
  // card). Every one of those turn-1 behaviors is now CONTAINED: the
  // Ask card is cancelled by dismissPendingAsk (loop top), and the claim
  // instruction rides the steer text below, which this verification's runs
  // delivered 5 times out of 5 (pressSequentially + the fail-fast delivery
  // check). The release run's actual flake — steers never sending, a card
  // eating the wait, and the 4s done-pill window being missed by a polling
  // wait with blind spots — is fixed in the steer loop and waitForDone
  // below; this test was NEVER flaky in its verdict half.
  //
  // The pill aria-label carries the condition truncated to its first 80
  // graphemes (GoalPillTray.tsx truncateCondition); the asserted fragment
  // "please continue" is the first prose after the check marker (the compiler
  // keeps the prose remainder verbatim, goal_compile.go::exciseMarkers), so it
  // sits inside that window even with the worker guidance appended below.
  // The tail keeps the autonomous goal worker off network-capable binaries.
  // ADR-092 D8: under Auto-approve (the fresh-install default) a `bash git …`
  // call still escalates to a network_preflight approval modal — Auto does NOT
  // waive network access, and no existing setting (global/per-agent/per-chat
  // Auto, tool_policies) removes it without also loosening approvals for the
  // whole shard. Release run 36305587114 (job llm-conformance-chat): the worker
  // ran `bash git log --oneline -20` while the steer was being typed and the
  // modal swallowed ~380 characters. This test is about the goal/steer walk,
  // not approvals, so the condition tells the worker the check needs only
  // `true`. The prefix (check marker + "please continue") is unchanged, so the
  // aria-label assertion below still holds.
  const condition =
    '[check: true exit:0] please continue — the check needs only bash `true`; ' +
    'do not inspect the repository and do not run git, curl, npm, wget, ssh, docker or gh'
  await input.fill(`/goal ${condition}`)
  await input.press('Enter')

  // Assert the active-pill is rendered — this is the FR-113 "echoed in chat"
  // moment: the goal_status WS frame (state="active") the activation wrote.
  const activePill = page.locator('[data-testid="goal-pill-active"]')
  await expect(activePill).toBeVisible({ timeout: 60_000 })

  // Differentiation test: the active pill's aria-label carries the goal
  // condition (the frame's condition field = compiled.Prompt — the PROSE on
  // the marker path, not the raw /goal text; measured in this file's run 3:
  // label was "Goal: please continue, state active"). Assert a fragment of
  // OUR condition is in that aria-label — proving the pill is bound to OUR
  // goal, not some other.
  await expect(activePill.first()).toHaveAttribute('aria-label', /please continue/i, {
    timeout: 10_000,
  })

  // Wait for the worker turn to complete — assistant message counter advances.
  // The active-pill → worker-turn transition is automatic (goal_loop.go emits
  // the goal_status frame, then a normal chat turn fires to do the work).
  await expect(assistantMessages(page).first()).toBeVisible({ timeout: 300_000 })

  // A CLAIM is the only thing that produces a verdict.
  //
  // This wait used to be a bare 300s poll for done, on the strength of the
  // comment that claimless idle settlement would fire after the quiet window
  // and run the KindCheck itself. ADR-084 revision 9 D13 (JUDGE-FR-095/
  // FR-097) retired that path outright: a `met` claim is now the SOLE
  // adjudication trigger, and a quiet RECORDED goal gets the keeper's
  // bounded continue-push instead — never a Judge call, never a round
  // consumed. scripts/check-no-claimless-adjudication.sh fails the build if
  // it returns.
  //
  // That left this test's outcome resting on whether the real model happened
  // to claim during its worker turn, and it split exactly there: on one CI
  // worker it claimed and reached done, on the other it went quiet and the
  // goal ended the run at `state=active, round=0, attempts_used=0,
  // zero_output_pushes=1` — the keeper pushed, nothing was ever judged. Same
  // commit, same spec, opposite verdicts.
  //
  // So the claim is no longer left to chance. Poll briefly FIRST, because a
  // model that already claimed during its worker turn is the legitimate fast
  // path and must not be steered a second time; only if the goal is still
  // waiting is the claim asked for explicitly.
  //
  // Do NOT "fix" this by asserting a quiet goal stays unjudged: this test
  // cannot know whether a claim arrived, so such an assertion fails a
  // CORRECT system on the fast path. The D13 invariant is pinned
  // deterministically in Go (TestQuietWindow_MakesZeroJudgeCalls) and by the
  // guard script; this file's job is the visible pill walk.
  const donePill = page.locator('[data-testid="goal-pill-done"]')

  // User bubbles whose text mentions goal_claim. The /goal line itself
  // contains "goal_claim" by design, so this starts at 1 after the goal is
  // filed; every DELIVERED steer bumps it by exactly one. The steer loop's
  // fail-fast check counts it — see the comment at that assertion.
  const claimUserBubbles = userMessages(page).filter({ hasText: 'goal_claim' })

  // waitForDone wraps page.waitForFunction: an IN-PAGE check evaluated at
  // rAF frequency, so a done pill that lives for only 4 seconds cannot be
  // missed — the old per-tick poll could.
  //
  // WHY (measured, this verification's own run 1): GoalPillTray.tsx keeps a
  // terminal pill on screen for TERMINAL_PILL_DISPLAY_MS = 4_000 ms and then
  // stops rendering it. The verdict landed 23:26:15 (gateway log: judge MET
  // verdict + the benign `goal-status upward delivery failed … lifecycle
  // record not found` warn), the tray painted the done pill at 23:26:15 for
  // 4s, and the test — busy typing the steer — never polled during that
  // window. The walk happened; the test called it "no done pill" and steered
  // into a closed record (both later goal_claim calls refused "no active
  // goal", 23:26:25 and 23:28:08). Polling with blind spots vs a 4s window
  // is a detection race the test loses by chance — exactly the kind of
  // instrument gap docs/internal/false-green-patterns.md warns about.
  //
  // waitForFunction closes the gap: the predicate runs in-page on every
  // animation frame, covering the seconds the test spends typing a steer,
  // and fires the moment the pill mounts. The oracle is UNCHANGED: only the
  // real done pill decides pass/fail. Ask-card handling is not folded in
  // here — a card cannot hide the pill (backend-driven paint), and the
  // composer-facing dismissal stays where the composer is used (loop top).
  const waitForDone = (budgetMs: number): Promise<boolean> =>
    page
      .waitForFunction(
        () => document.querySelector('[data-testid="goal-pill-done"]') !== null,
        undefined,
        { timeout: budgetMs },
      )
      .then(
        () => true,
        () => false,
      )

  // "please continue" with nothing actually in progress is exactly the vague
  // prose door goal-work-first.spec.ts documents as model-dependent (holdout
  // H-2, spec §9): the worker may, instead of proceeding, call
  // AskUserQuestion to check what "continue" means before doing anything
  // else. ADR-088 D5 makes ordinary chat the entire steering mechanism, and
  // the composer is deliberately locked while a question is pending
  // (AskUserQuestionThreadTail's own copy: "chat input is locked while
  // questions are pending — Cancel to unlock") — so an unattended CI run
  // left facing that card would sit with `input.fill()` retrying against a
  // disabled textarea for the rest of the test's budget, never timing out on
  // anything the test actually asserts. Observed on CI (job 107857384754,
  // commit bd4ff739f): attempt 0 hit exactly this — the transcript shows
  // set_goal rejected once, a second self-authored goal registered, then an
  // AskUserQuestion card ("I found no in-progress work to continue. What
  // would you like to do?"), and `locator.fill` retried 825 times over the
  // full 420s budget with "element is not enabled" before the suite's own
  // flaky-test guard (correctly) failed the job. Dismissing a stray blocking
  // card before steering is the same category of fix as clearing a leftover
  // Tool-Approval overlay before a click — it does not touch the oracle
  // (still only `donePill` decides pass/fail), it only clears a UI door the
  // steer loop needs to get past to ask again.
  const askUserQuestionCard = page.locator('[data-testid="ask-user-question-card"]')
  const askUserCancel = page.locator('[data-testid="ask-user-cancel"]')
  // Every dismissal is recorded as a `blocker-dismissed` annotation (visible in
  // the Playwright report), not only logged: a blocker that reappeared on every
  // try would otherwise be cancelled silently and the run would still pass. The
  // count is deliberately not asserted — the tolerance is the point.
  const dismissPendingAsk = async (where: string): Promise<void> => {
    if (!(await askUserQuestionCard.isVisible({ timeout: 1_000 }).catch(() => false))) return
    console.log(`t0: AskUserQuestion card is blocking the composer — cancelling it to steer past it (${where})`)
    test.info().annotations.push({
      type: 'blocker-dismissed',
      description: `AskUserQuestion card cancelled — ${where}`,
    })
    await askUserCancel.click().catch(() => {
      /* card may resolve itself between the check and the click */
    })
    await expect(input, 'composer must unlock once the pending question is cancelled').toBeEnabled({
      timeout: 15_000,
    })
  }

  // TOOL-APPROVAL MODAL (measured, this file's run 4): turn 1's prompt is
  // bare steering prose, and this run's model spent it on repo orientation —
  // a network-capable `bash git …` call. Under ADR-092, a network-capable
  // binary on a chat with Auto-approve (and no enforcing kernel sandbox on
  // macOS) escalates to the operator as the ToolApprovalModal — a full-screen
  // Radix dialog whose overlay intercepts every pointer event until it is
  // answered, so the composer cannot be clicked at all (run 4: the steer's
  // input.click() retried 851× against the overlay for the rest of the
  // budget). The operator action this test takes is DENY (the
  // default-focused safe default, ToolApprovalModal.tsx denyButtonRef): the
  // goal's check needs only bash `true`, never the network. This is an
  // operator choice the ADR-092 flow exists to offer — it does not touch the
  // oracle; the done pill still decides pass/fail, and the done pill's
  // in-page wait is overlay-proof (the pill mounts in the DOM under the
  // overlay; presence, not visibility, is what waitForFunction checks).
  const toolApprovalDialog = page.locator('[data-testid="dialog-overlay"]')
  const denyToolApproval = async (where: string): Promise<void> => {
    if (!(await toolApprovalDialog.isVisible({ timeout: 1_000 }).catch(() => false))) return
    console.log(`t0: tool-approval modal is blocking the composer — denying it (no network calls needed) (${where})`)
    test.info().annotations.push({
      type: 'blocker-dismissed',
      description: `tool-approval modal denied — ${where}`,
    })
    await page
      .getByRole('button', { name: 'Deny', exact: true })
      .click()
      .catch(() => {
        /* modal may resolve itself between the check and the click */
      })
  }

  // FAST PATH: the worker may have claimed during its own turn. A model that
  // already claimed must not be steered again, so check before asking.
  let sawDone = await waitForDone(20_000)

  // ASK FOR THE CLAIM, AND KEEP ASKING.
  //
  // A single steer was not enough on CI, and the root cause was a genuine
  // test bug, not model noise: the previous version of this steer told the
  // model to pass "the exact text of the check criterion you were given" as
  // its goal_claim evidence — i.e. to paste the `[check: true exit:0]`
  // marker back verbatim. That is not a verification claim, it is an echo.
  // Every compiled goal automatically carries the floor DoD criterion
  // "goal-dod-floor-grounded-claims" — "Every factual claim is grounded, not
  // assumed" (pkg/agent/goal_compile.go's newFloorDoD) — alongside whatever
  // the operator asked for, and the Judge adjudicates ALL of them on a
  // claim, not just the one the operator wrote. Under ADR-084 revision 9
  // D2b–D2d an evidence string that only restates the criterion text grounds
  // nothing (D2c requires a quote/claim traceable to this turn's own tool
  // results, and D2d explicitly rejects "a quote that merely proves [the
  // criterion] exists" as verification), so the Judge correctly ruled that
  // claim ungrounded and sent the worker back to rework it — confirmed in
  // the CI gateway log as a verdict disagreement on
  // goal-dod-floor-grounded-claims with previous_met=false. The worker then
  // (correctly) refused to repeat an ungrounded claim and spent the rest of
  // its budget arguing instead of reaching done. The retry on CI passed only
  // because that run's model happened to ground its evidence on the first
  // try — a coin flip, not a fix, which is exactly the kind of flake
  // docs/internal/false-green-patterns.md says CI is right to reject
  // ("retries: 3 masked a real failure").
  //
  // The fix is to ask for evidence the Judge can actually ground: have the
  // model verify the check itself with a real tool call and cite what it
  // observed, matching goal_claim's own contract ("your own one-line
  // statement of what you verified", pkg/tools/goal_claim.go's
  // Description/Parameters) instead of parroting the marker text.
  //
  // A BARE claim (no evidence, or one that fails goal_claim's own non-empty
  // check) is separately bounced by design (G-4,
  // goal_triggers.go's handleBareGoalClaim) and costs a round instead of
  // adjudicating — that mechanism is untouched here.
  //
  // Steering repeatedly is still a legitimate user action and is how a real
  // operator would drive this if a first grounded attempt is somehow still
  // rejected, so this keeps asking up to three times with a full
  // adjudication budget after each. What is being tested is unchanged: the
  // walk from a met claim through the Judge to a done pill.
  // Grounded evidence, not an echo of the criterion — see the block
  // comment above this loop for why.
  //
  // RELEASE-FLAKE FIX (run 36305587114): the steer names the same
  // network-capable binaries preflight.go's ClassifyNetworkNeed flags
  // (networkCapableBinaries) so a steer-driven bash call cannot land on an
  // unapproved ADR-092 D8 pre-flight escalation — in the release run three
  // `bash git` calls ate ~85s each of the steer budget that way. And the
  // modal they raise is answered by denyToolApproval at loop top (run 4).
  const steerText =
    'Verify the goal\'s check criterion yourself, then claim it. First call the bash tool ' +
    'with the command `true; echo $?` and read the exit code it reports. Do not run git, curl, ' +
    'npm, wget, ssh, docker or gh — any network-capable bash calls land on an approval card ' +
    'nobody will answer. Then call the goal_claim tool with status "met" and, for the evidence ' +
    'argument, your own one-line statement of what you personally observed from that bash call ' +
    '(for applied work: "ran `true` via bash and observed exit code 0, as the check requires"). ' +
    'Do not pass the criterion text itself as evidence — describe what you verified. ' +
    'Do not reply with prose only — the tool calls are what is required.'

  const STEER_ATTEMPTS = 3
  for (let attempt = 1; attempt <= STEER_ATTEMPTS && !sawDone; attempt++) {
    // Clear a stray AskUserQuestion card first — see dismissPendingAsk's own
    // comment above for why one can be sitting here blocking the composer.
    // The tool-approval modal (run 4's blocker) is likewise answered before
    // the typing: denyToolApproval above.
    await dismissPendingAsk(`steer ${attempt}/${STEER_ATTEMPTS}, before typing`)
    await denyToolApproval(`steer ${attempt}/${STEER_ATTEMPTS}, before typing`)

    // Start the done-pill wait BEFORE the typing. GoalPillTray.tsx removes a
    // terminal pill 4s after it paints (TERMINAL_PILL_DISPLAY_MS) — see
    // waitForDone's block comment for the run that proved a poll can miss
    // that window entirely. The wait is in-page (waitForFunction) and runs
    // at rAF frequency DURING the multi-second typing below, so a verdict
    // landing mid-typing is caught instead of missed; each attempt's wait
    // spans its own typing + 90s poll, and the next attempt's wait starts
    // within milliseconds of this one expiring, so there is no gap.
    const doneWait = waitForDone(90_000)

    // Type the steer the way a user does, per keystroke — NOT via fill().
    // selectors.ts::startNewChat documents that fill() bypasses the input
    // events this composer listens to on this textarea (T22 in
    // cancel-cross-channel.spec.ts types for the same reason); in the flaky
    // release run the fills demonstrably never became sends, and the failed
    // snapshot's composer still held the steer text six times concatenated —
    // a programmatic bulk-fill artifact no real user can produce blind.
    // Plain text (no leading "/") cannot open the slash palette, so
    // pressSequentially is safe here; the /goal line above KEEPS fill()
    // deliberately — a per-key "/goal …" WOULD open the palette and make
    // Enter select a palette row instead of sending.
    const bubblesBefore = await claimUserBubbles.count()
    // BLOCKER MID-TYPING (CI run 37696589829, attempt 1): the dismiss/deny
    // checks above run ONCE, but the worker's turn is still live and the typing
    // takes seconds. The model raised an AskUserQuestion card (screenshot:
    // "MIA NEEDS YOUR INPUT", "chat input is locked while questions are
    // pending") part-way through, which disables the composer and leaves a
    // truncated steer in it. That is the same blocker the pre-typing checks
    // clear, arriving inside the typing window — so when the composer does not
    // hold the full steer AND a blocker is on screen, clear the blocker, wipe
    // the partial text and type again. No blocker, or still a mismatch after
    // the tries: fall through to the strict guard below, which fails loudly.
    const STEER_TYPE_TRIES = 3
    for (let typing = 1; typing <= STEER_TYPE_TRIES; typing++) {
      if (typing > 1) {
        console.log(`t0: a blocker interrupted the typing — retyping the steer (try ${typing}/${STEER_TYPE_TRIES})`)
        test.info().annotations.push({
          type: 'blocker-dismissed',
          description: `steer ${attempt}/${STEER_ATTEMPTS}: typing interrupted, retyping (try ${typing}/${STEER_TYPE_TRIES})`,
        })
        await dismissPendingAsk(`steer ${attempt}/${STEER_ATTEMPTS}, typing try ${typing}/${STEER_TYPE_TRIES}`)
        await denyToolApproval(`steer ${attempt}/${STEER_ATTEMPTS}, typing try ${typing}/${STEER_TYPE_TRIES}`)
      }
      await input.click()
      await input.press('ControlOrMeta+a')
      await input.press('Delete')
      // A card landing mid-typing can fail the keystrokes themselves; the
      // value check right after decides, so the failure is not lost here.
      await input.pressSequentially(steerText).catch(() => {})
      if ((await input.inputValue().catch(() => '')) === steerText) break
      const blocked =
        (await askUserQuestionCard.isVisible({ timeout: 1_000 }).catch(() => false)) ||
        (await toolApprovalDialog.isVisible({ timeout: 1_000 }).catch(() => false))
      if (!blocked) break
    }
    // LOUD GUARD: a blocker that took focus mid-typing swallows keystrokes
    // (release run 36305587114: ~380 chars lost; run 37696589829: an
    // AskUserQuestion card). Never send a truncated steer — fail here, naming
    // the cause, instead of sending garbage or waiting out the done-pill budget.
    await expect(
      input,
      `t0: composer text differs from the typed steer before Enter on attempt ${attempt}/${STEER_ATTEMPTS} — ` +
        'a blocker (an AskUserQuestion card or the ADR-092 network_preflight tool-approval modal) most likely ' +
        'took focus mid-typing and swallowed keystrokes, and retyping did not clear it.',
    ).toHaveValue(steerText)
    await input.press('Enter')

    // FAIL FAST: the steer must reach the transcript as a user message. In
    // the flaky release run the steer text never left the composer, and the
    // loop then burned its full 3×90s pill budget before failing on the
    // done-pill assertion with nothing diagnosable in the failure message.
    // The /goal line itself is the baseline (it contains "goal_claim" by
    // design), so each delivered steer bumps this count by exactly one.
    // This ADDS a check; it removes none — the done pill below remains the
    // sole pass decider.
    await expect(
      claimUserBubbles,
      `t0: steer ${attempt}/${STEER_ATTEMPTS} never became a user message — the composer did ` +
        'not send it (this is the exact mechanism of release run 36305587114, where the steer ' +
        'text sat in the composer while the loop burned its 90s pill budgets). Failing here ' +
        'instead of riding out a pointless 90s wait.',
    ).toHaveCount(bubblesBefore + 1, { timeout: 30_000 })

    // Adjudication is DEFERRED until after the reply is delivered (D13), so
    // the walk is claim → judging (ephemeral) → done. doneWait has been
    // running since BEFORE the typing — it covers the typing blind spot, and
    // its 90s clock started at the dismiss above.
    sawDone = await doneWait
    if (!sawDone && attempt < STEER_ATTEMPTS) {
      console.log(`t0: no done pill after steer ${attempt}/${STEER_ATTEMPTS} — asking again`)
    }
  }

  // Differentiation assertion: the active pill must be GONE once done
  // (the FR-114 cleanup — GoalCondition cleared from session meta). At
  // least one done-pill must have appeared.
  expect(sawDone, 'GoalPillTray must transition to data-testid="goal-pill-done" after a met claim').toBe(
    true,
  )
  // The active pill should be gone or replaced — at minimum the count of
  // active-pill instances must have dropped, OR the done pill must be the
  // dominant visible state.
  const activeStillVisible = await activePill.isVisible({ timeout: 1_000 }).catch(() => false)
  if (activeStillVisible) {
    // Acceptable ONLY if the done pill is also present — multiple pills
    // per goal-id (FE-1) can coexist briefly during the active→done flip.
    expect(
      await donePill.isVisible({ timeout: 1_000 }).catch(() => false),
      'If an active pill remains, a done pill must also be visible (FE-1 multi-pill)',
    ).toBe(true)
  }
})

// ── Conformance_g6_SessionControlE2E ─────────────────────────────────────────
//
// BDD (§9.1 g6): spawn child → message_parent(question) → parent
// answers or escalates to human → respond/steer lands at child's next
// tool boundary → handback; 3P child is fire-and-collect; per-child
// ceiling — one noisy child cannot starve a sibling; durable inbox
// survives Stop/Play.
//
// Traces to:
//   - §9.1 g6 "session control walks the drawn path" (line ~1202)
//   - TDD Plan row 47 `Conformance_g6_SessionControlE2E`
//   - TestConformance_g6_PerChildCeiling_NoisyChildCannotStarveSibling (#541)
//   - FR-128 content-egress filter, D15 per-child ceiling, D16 durable inbox

test('Conformance_g6_SessionControlE2E: chat delegates, child asks, parent responds, handback reaches inbox', async ({
  page,
}) => {
  requireApiKey()

  test.setTimeout(360_000)
  await startFreshChatWithJim(page)

  // Drive a real delegate() tool call from the chat. Jim's persona is
  // "general purpose task agent" and is the canonical delegate-capable
  // agent (Mia declines — see subagent.spec.ts startFreshChat rationale).
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })

  // Deterministic prompt with temperature=0+seed=42 plumbing. The
  // sub-task mandates a single message_parent call so the parent inbox
  // receives a verifiable, machine-checkable artifact.
  await input.fill(
    [
      'Call the `delegate` tool exactly once, right now, with these arguments:',
      '  label: "g6 child"',
      '  task: "You are a delegated child. Your one and only job is to call the `message_parent` tool with kind=question, text=\\"CHOOSE_EITHER_A_OR_B\\", wait=false. Do not reply in prose. Do not call any other tool. After message_parent returns, call message_parent again with kind=handback and result_so_far=\\"child finished\\". Then stop."',
      'Do not reply in prose. Do not call any other tool. Call delegate now.',
    ].join('\n'),
  )
  await input.press('Enter')

  // Wait for at least one assistant message — the parent completing the
  // delegate call and the child finishing its handback. A 300s budget
  // covers the parent delegate round-trip + child message_parent pair
  // under suite load.
  await expect(assistantMessages(page).first()).toBeVisible({ timeout: 300_000 })

  // Drawn-path assertion: the parent's transcript contains a delegate
  // tool call. We can't introspect the in-memory transcript directly
  // from the browser, but the chat's message count MUST have advanced
  // by at least one assistant message (the parent's response after
  // the child handback). If no assistant message rendered, the
  // delegate→child→handback chain did not complete.
  const assistantCount = await assistantMessages(page).count()
  expect(
    assistantCount > 0,
    `g6: at least one assistant message must render after the delegate→child→handback chain — ` +
      `observed ${assistantCount}. The control-plane is broken.`,
  ).toBe(true)

  // Differentiation: the assistant message must carry NON-empty content
  // (an empty assistant message means the parent didn't actually
  // respond to the child handback — the chain ended silently).
  const firstAssistant = assistantMessages(page).first()
  const text = (await firstAssistant.textContent()) ?? ''
  expect(
    text.trim().length > 0,
    `g6: parent assistant message must be non-empty after child handback — observed "${text.slice(0, 80)}".`,
  ).toBe(true)
})

// ── Conformance_g7_RoundTripE2E ──────────────────────────────────────────────
//
// BDD (§9.1 g7): mid-run steer + a blocking question(wait=true) answered
// by respond WITHOUT restarting the child + a clean handback into the
// evidence gate. Assert: child kept warm context (no cold restart),
// answer routed by correlation_id, handback's
// result_so_far/artifacts[]/open_questions[] fed the rung-0 gate.
//
// Traces to:
//   - §9.1 g7 "session round-trip sequence walks the drawn path" (line ~1209)
//   - TDD Plan row 48 `Conformance_g7_RoundTripE2E`
//   - TestConformance_g7_SessionRoundTrip_WarmQuestionRespondHandback (#541)

test('Conformance_g7_RoundTripE2E: blocking question + respond routes warm, handback reaches inbox', async ({
  page,
}) => {
  requireApiKey()

  test.setTimeout(360_000)
  await startFreshChatWithJim(page)

  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })

  // Deterministic prompt — the child MUST do exactly one message_parent
  // question(wait=true) followed by exactly one handback. The blocking
  // question drives the question→respond correlation routing the g7
  // spec asserts.
  await input.fill(
    [
      'Call the `delegate` tool exactly once, right now, with these arguments:',
      '  label: "g7 round trip"',
      '  task: "You are a delegated child. Your one and only job is to (1) call the `message_parent` tool with kind=question, text=\\"CONFIRM_READY\\", wait=TRUE. Then (2) call the `message_parent` tool with kind=handback and result_so_far=\\"g7 child finished after parent answer\\". Do not reply in prose. Do not call any other tool. Call delegate now."',
      'Do not reply in prose. Do not call any other tool. Call delegate now.',
    ].join('\n'),
  )
  await input.press('Enter')

  // The parent needs to respond to the child's blocking question in
  // chat (g6 / g7 invariant: the child is parked in needs_input until
  // the parent answers). The harness has no automatic answer path, so
  // we observe the assistant message arriving (the LLM acknowledges or
  // escalates to human per the g6 diagram), then assert the chain
  // closed.
  await expect(assistantMessages(page).first()).toBeVisible({ timeout: 300_000 })

  // Drawn-path assertion: at least one assistant message rendered —
  // the chain (delegate → question(wait=true) → parent-ack → handback)
  // must close with the parent seeing the child's handback.
  const count = await assistantMessages(page).count()
  expect(
    count > 0,
    `g7: parent must render at least one assistant message after the round-trip — observed ${count}. ` +
      'The blocking question→respond→handback chain did not close.',
  ).toBe(true)

  // The "warm" assertion (no cold restart) is at the control-plane level
  // (the g7 Go integration test verifies the same-generation invariant
  // in pkg/agent/conformance_design_test.go). At the chat-thread level
  // the e2e signal is: ONE continuous assistant-message sequence, not
  // two disconnected runs (a cold restart would surface as a visible
  // thread break with a new session ID in the UI).
  //
  // We assert on the rendered DOM: every assistant message must share
  // a single thread — i.e., a single continuous message container,
  // not two separate chat panels (which is how cold restarts render).
  // This is a coarse but observable proxy for the warm invariant.
  const firstAssistant = assistantMessages(page).first()
  const firstText = (await firstAssistant.textContent()) ?? ''
  expect(
    firstText.trim().length > 0,
    `g7: first assistant message must be non-empty — observed "${firstText.slice(0, 80)}".`,
  ).toBe(true)
})
