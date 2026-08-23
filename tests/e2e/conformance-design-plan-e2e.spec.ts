// Omnipus — ADR-053 §9.1 design-conformance E2E: plan lifecycle + judging.
//
// Covers: t2 (execute → approve → deterministic unmet → F2 hold),
//         t3b (targeted retry as the sole correct verb).
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
import {
  apiFetch,
  checkCriterion,
  createMainAgent,
  createPlanWithMembers,
  extractPlanCorrectCalls,
  getSessionMessages,
  listPlanMemberTasks,
  memberSignature,
  proseCriterion,
  requireApiKey,
  startFreshChatWithJim,
} from './fixtures/conformance-helpers'

// ── Conformance_t2_PlanLifecycleE2E ──────────────────────────────────────────
//
// BDD (§9.1 t2): plan lifecycle — Execute → gated approve → members per DAG
// → all-terminal → plan Judge → unmet → awaiting-supervision HOLDS
// (no round burned on unchanged state — F2 proof) → owner appends →
// re-judge → done; Play resumes a cancelled member from last git commit.
//
// Traces to:
//   - §9.1 t2 "plan lifecycle walks the drawn path" (line ~1174)
//   - TDD Plan row 43 `Conformance_t2_PlanLifecycleE2E`
//   - adr-053-DEFERRED-ISSUES.md E.2 (this test used to pass the instant the
//     plan reached done OR failed — it never forced a deterministic
//     round-1 unmet verdict, and the F2 "hold burns no JudgeRounds" claim
//     was asserted only conditionally, `if (sawHold)`, so a run that never
//     held at all silently passed without proving anything about F2.)
//
// FIX: the plan's DoD is a `kind: check` criterion whose command (`exit 1`)
// can NEVER return the expected exit code — a deterministic, LLM-judgment-
// free unmet verdict every single round, empirically verified against a
// live gateway before writing this test (see the qa-lead report for the
// trace). This makes "the plan reaches awaiting_supervision" a MANDATORY
// assertion, not a conditional one. The F2 property itself — "an idle tick
// with an UNCHANGED member terminal signature must not burn a JudgeRounds
// increment" — is then proven by SAMPLING (plan_phase, judge_rounds, member
// signature) at fine granularity across the hold and asserting the causal
// implication directly: judge_rounds may only advance between two samples
// whose member signature also changed. This is the actual F2 invariant
// (pkg/agent/plan_engine.go's `planTerminalSignature`/"a later unchanged
// idle tick's processPlan skips re-judging it") — proven directly rather
// than by hoping for a multi-tick silent gap from a live, actively-
// correcting adjudicator (which, empirically, keeps working the plan every
// ~30s once parked — see the report for why an artificially long idle
// window is not how this system actually behaves, and why sampling the
// causal condition is the honest way to test the guarantee).

test('Conformance_t2_PlanLifecycleE2E: Execute → approve → members → deterministic unmet → F2 hold proven → no wedge', async ({
  page,
  createdPlanIds,
}) => {
  requireApiKey()

  test.setTimeout(600_000)
  await startFreshChatWithJim(page)

  // Setup: per-test Main agent (chat-target plan owner + member assignee)
  // in its own workspace core_team. A fresh agent per test avoids the
  // multi-workspace find_for_agent ambiguity that cancels member turns.
  // `bash: allow` is required for the `check` criteria below — a fresh
  // custom agent is seeded fully deny-by-default (verified empirically:
  // omitting this override makes every check fail closed regardless of
  // command, which would make the "definitely met" and "definitely unmet"
  // recipes indistinguishable).
  const ownerId = await createMainAgent(page, `conformance-t2-owner-${Date.now()}`, { bash: 'allow' })
  const wsRes = await apiFetch<{ id: string }>(page, 'POST', '/api/v1/workspaces', {
    name: 'conformance-t2',
    core_team: [ownerId],
  })
  if (!wsRes.ok) throw new Error(`t2: POST /workspaces failed ${wsRes.status}: ${wsRes.raw}`)
  const workspaceId = wsRes.body.id

  // Create a plan with TWO members (each a deterministic, machine-checked
  // `exit 0` — fast and judgment-free, so "all members terminal" is reached
  // quickly and reliably) and a plan-level DoD that can NEVER be satisfied
  // (`exit 1`, expected 0) — forcing a deterministic round-1 UNMET verdict.
  // The plan-lint gate (G-16) requires disjoint write_sets per parallel
  // member; we comply here.
  const { planId } = await createPlanWithMembers(
    page,
    workspaceId,
    ownerId,
    {
      title: 't2 conformance plan',
      goal: 'produce a verdict of met or unmet for both members',
      description: 'two serial members, each with a machine-checked criterion',
      dod: [checkCriterion('deterministic unmet DoD — forces round-1 unmet for the F2 proof', 'exit 1', 0)],
      bounds: { plan_judge_max_rounds: 5 },
    },
    [
      {
        label: 'm1',
        title: 'member one',
        prompt: 'reply with the literal word alpha',
        write_set: ['out/m1.txt'],
        criteria: [checkCriterion('m1 trivially passes', 'exit 0', 0)],
      },
      {
        label: 'm2',
        title: 'member two',
        prompt: 'reply with the literal word beta',
        blocked_by_labels: ['m1'],
        write_set: ['out/m2.txt'],
        criteria: [checkCriterion('m2 trivially passes', 'exit 0', 0)],
      },
    ],
    createdPlanIds,
  )

  // Approve (tiered-DoD + unconditional member-creation; ADR-049). This
  // is the "gated approve" node in the t2 diagram.
  const approveRes = await apiFetch<{ status: string }>(
    page,
    'POST',
    `/api/v1/plans/${planId}/approve`,
    {},
  )
  expect(
    approveRes.ok,
    `t2: POST /plans/{id}/approve failed ${approveRes.status}: ${approveRes.raw}`,
  ).toBe(true)

  // IMPORTANT: the plan's hold ("awaiting_supervision") is a SUB-PHASE
  // of State=running, NOT a top-level state. The 5-value state machine is
  // draft/approved/running/done/failed; an unmet plan stays State=running
  // with plan_phase="awaiting_supervision" (Plan.yaml:80, plan.go:190).
  const HOLD_PHASE = 'awaiting_supervision'

  // --- Step 1: MANDATORY — the plan MUST reach the hold ------------------
  // Not conditional. With an immutable, deterministically-unmet DoD this is
  // guaranteed once both members are terminal (empirically verified) — if it
  // is not observed, the F2 hold itself is broken, not merely "not proven".
  const holdDeadline = Date.now() + 120_000
  let reachedHold = false
  while (Date.now() < holdDeadline) {
    const poll = await apiFetch<{ plan_phase?: string; judge_rounds?: number }>(page, 'GET', `/api/v1/plans/${planId}`)
    if (!poll.ok) throw new Error(`t2: GET /plans/{id} poll (hold wait) failed ${poll.status}: ${poll.raw}`)
    if (poll.body.plan_phase === HOLD_PHASE) {
      reachedHold = true
      break
    }
    await page.waitForTimeout(1_500)
  }
  expect(
    reachedHold,
    `t2: plan ${planId} must reach plan_phase=awaiting_supervision within 120s of approval — the DoD check ` +
      '("exit 1", expected 0) can never be satisfied, so a round-1 unmet verdict (and the F2 hold it triggers) ' +
      'is deterministic. Not observing it means either the DoD check never ran, or the F2 hold path is broken.',
  ).toBe(true)

  // --- Step 2: the F2 round-burn proof itself -----------------------------
  // Sample (judge_rounds, member terminal signature, plan_phase) at fine
  // granularity while the plan sits at the hold. The F2 invariant, stated
  // causally: judge_rounds may only advance between two samples whose
  // member signature ALSO changed (a real correction landed and changed
  // the DAG) — an increment between two samples with an IDENTICAL member
  // signature is exactly the bug F2 exists to prevent (re-judging an
  // unchanged terminal state on every idle tick). The sampling window ends
  // either at its own deadline or as soon as the plan leaves the hold
  // (whichever first — sampling past that point is not testing F2 anymore).
  interface Sample {
    tMs: number
    phase: string
    judgeRounds: number
    signature: string
  }
  const samples: Sample[] = []
  const sampleWindowDeadline = Date.now() + 150_000
  const sampleIntervalMs = 4_000
  const t0 = Date.now()
  while (Date.now() < sampleWindowDeadline) {
    const [planPoll, members] = await Promise.all([
      apiFetch<{ plan_phase?: string; judge_rounds?: number }>(page, 'GET', `/api/v1/plans/${planId}`),
      listPlanMemberTasks(page, workspaceId, planId),
    ])
    if (!planPoll.ok) throw new Error(`t2: GET /plans/{id} poll (F2 sampling) failed ${planPoll.status}: ${planPoll.raw}`)
    samples.push({
      tMs: Date.now() - t0,
      phase: planPoll.body.plan_phase ?? '',
      judgeRounds: planPoll.body.judge_rounds ?? 0,
      signature: memberSignature(members),
    })
    if (planPoll.body.plan_phase !== HOLD_PHASE) break
    await page.waitForTimeout(sampleIntervalMs)
  }

  // Causal invariant: for every consecutive pair, judge_rounds may only
  // increase alongside a member-signature change.
  const violations: string[] = []
  let sawHeldUnchangedPair = false
  for (let i = 1; i < samples.length; i++) {
    const prev = samples[i - 1]
    const cur = samples[i]
    const roundsAdvanced = cur.judgeRounds > prev.judgeRounds
    const signatureChanged = cur.signature !== prev.signature
    if (prev.phase === HOLD_PHASE && !signatureChanged) {
      sawHeldUnchangedPair = true
      if (roundsAdvanced) {
        violations.push(
          `t=${prev.tMs}ms→${cur.tMs}ms: judge_rounds ${prev.judgeRounds}→${cur.judgeRounds} advanced while the ` +
            `member terminal signature stayed IDENTICAL ("${prev.signature}") and phase stayed ` +
            'awaiting_supervision — this is exactly the F2 round-burn bug (re-judging an unchanged idle hold).',
        )
      }
    }
  }
  expect(
    violations,
    `F2 violation(s) detected — an unchanged idle hold burned a JudgeRounds increment:\n${violations.join('\n')}\n` +
      `Full sample trace: ${JSON.stringify(samples)}`,
  ).toEqual([])
  // The invariant above is only a meaningful proof if we actually observed
  // at least one held, signature-unchanged consecutive pair to test it
  // against — otherwise it holds vacuously (every sample happened to land
  // on a round transition). Require genuine coverage of the idle case.
  expect(
    sawHeldUnchangedPair,
    'F2 sampling window never observed two consecutive samples that were BOTH held at awaiting_supervision ' +
      `with an unchanged member signature — the invariant above was not genuinely exercised. Sample trace: ${JSON.stringify(samples)}`,
  ).toBe(true)
  // judge_rounds must never have been observed to REGRESS either (a
  // completely different failure mode from round-burn, but still a broken
  // invariant worth catching for free from the same trace).
  for (let i = 1; i < samples.length; i++) {
    expect(
      samples[i].judgeRounds,
      `t2: judge_rounds must be monotonically non-decreasing — observed a regression at sample ${i} ` +
        `(${samples[i - 1].judgeRounds} → ${samples[i].judgeRounds}). Trace: ${JSON.stringify(samples)}`,
    ).toBeGreaterThanOrEqual(samples[i - 1].judgeRounds)
  }

  // --- Step 3: no wedge — the plan keeps moving after the sampling window -
  // Whatever the sampling window observed, the plan must eventually reach a
  // documented terminus: done (full correction-append→re-judge→done cycle
  // completed — the drawn t2 happy path), failed (e.g. round-budget
  // exhausted honestly), or still legitimately parked at awaiting_supervision
  // (a fresh, real correction landed since the sampling window ended and the
  // engine is actively re-judging it).
  //
  // ALSO acceptable, same rationale as Conformance_t3_PlanningReplanningE2E's
  // Step 3 (see that check's comment): state="running" with plan_phase in
  // {dispatching, judging, synthesizing} or "stalled". t2's DoD is
  // immutably unmet, but the PlanSupervisor turn that decides whether to
  // correct is itself a real LLM call, and any correction it applies re-runs
  // m1/m2 as real member turns (pkg/plan/plan.go's Supervision doc comment:
  // "an applied correction returns the plan to dispatching") — real LLM
  // latency this check must not misclassify as a wedge. Verified live,
  // 2026-07-30, CI shard llm-conformance: this plan was observed at
  // state="running" phase="dispatching" at window-close — exactly the
  // documented post-correction re-run, not a stall.
  const ACTIVE_ROUND_PHASES = new Set(['dispatching', 'judging', 'synthesizing'])
  const HELD_PHASES = new Set([HOLD_PHASE, 'stalled'])
  const finalDeadline = Date.now() + 180_000
  let planState = ''
  let planPhase = ''
  let reachedTerminalOrHold = false
  while (Date.now() < finalDeadline) {
    const poll = await apiFetch<{ state: string; plan_phase?: string }>(page, 'GET', `/api/v1/plans/${planId}`)
    if (!poll.ok) throw new Error(`t2: GET /plans/{id} poll (final) failed ${poll.status}: ${poll.raw}`)
    planState = poll.body.state
    planPhase = poll.body.plan_phase ?? ''
    if (
      planState === 'done' ||
      planState === 'failed' ||
      HELD_PHASES.has(planPhase) ||
      (planState === 'running' && ACTIVE_ROUND_PHASES.has(planPhase))
    ) {
      reachedTerminalOrHold = true
      break
    }
    await page.waitForTimeout(3_000)
  }
  expect(
    reachedTerminalOrHold,
    `t2: plan ${planId} must be at a documented terminus (done/failed), still legitimately held/stalled, or ` +
      `observably still progressing through an active round — observed state="${planState}" phase="${planPhase}". ` +
      'A wedge here means the correction-append → re-judge cycle stalled after the sampling window.',
  ).toBe(true)
})

// ── Conformance_t3b_TargetedRetryOnlyE2E ─────────────────────────────────────
//
// BDD (§9.1 t3, same underlying scenario as Conformance_t3_PlanningReplanningE2E
// above — see that test's header comment for the full "REAL PATH" mechanism
// writeup: how a correction is issued exclusively by PlanSupervisor's
// `plan_correct` tool on an automatic wake, and how it is observed via the
// adjudication session transcript's committed `plan_correct` tool_calls).
//
// Traces to:
//   - §9.1 t3 "planning & re-planning walks the drawn path" (line ~1181)
//   - TDD Plan row 44 `Conformance_t3_PlanningReplanningE2E` — this test
//     proves the TARGETED-RETRY half of that same row. G-11/FR-143 requires
//     append + SUPERSEDE + TARGETED-RETRY to each WORK and each record a
//     revision entry — it does not require one scenario to exercise all
//     three; splitting the proof across two deterministic scenarios still
//     satisfies it, and satisfies it more honestly than one scenario whose
//     two halves are not actually independent (see below).
//
// WHY THIS IS A SEPARATE TEST, NOT AN ASSERTION ADDED TO t3: t3's plan
// engineers TWO problems at once (m2 done-but-wrong, m3 failed-transient) so
// it could assert both SUPERSEDE and TARGETED-RETRY were committed in one
// run. They are not independent outcomes: `AppendCorrection`
// (pkg/agent/plan_engine.go:4295) auto-resets EVERY live-round failed member
// (excludes only frozen/`done` members — `autoResetLiveRoundFailedMembers`,
// :4902) for BOTH append and supersede; targeted_retry alone resets only its
// one named member, via its own path inside `buildCorrectionApplyFunc`
// (:4859). The instant a real PlanSupervisor commits a SUPERSEDE against a
// done-but-wrong member, every OTHER still-failed member in that plan —
// however it got there — is swept back to `next` for free. A real run's own
// recorded reasoning said exactly this about m3: "m3-retry-target (failed)
// was a transient/flaky failure that was never retried — it will be
// auto-reset by the supersede and get another attempt under the corrected
// plan." A well-reasoned supervisor has no motivation to ALSO spend a
// targeted_retry on a member the engine already reset, so a plan that offers
// BOTH a supersede target AND a failed-transient member cannot reliably
// prove targeted_retry fires at all — it proves, at most, that the plan
// eventually clears.
//
// This test removes that confound: the plan below has exactly ONE problem
// (a deterministically-failed, framed-as-transient member) and structurally
// NO candidate for supersede — supersede requires an existing `done` member
// whose outcome is in question
// (`resolvePlanMember(..., task.StatusDone, ...)`, pkg/tools/plan_correct.go),
// and the DoD here explicitly states no such member exists and no new work
// is needed, so append has no motivated use either. TARGETED-RETRY — "reset
// one failed member," per the tool's own description to the LLM
// (pkg/tools/plan_correct.go:141/165) — is the one verb whose stated purpose
// matches the diagnosis, and the only one discoverable here without
// inventing unmotivated new work. The filler member (m1) reaches `done` on
// its own trivial check but is never named as a problem by the DoD, so its
// existence gives the plan no supersede target either.
test("Conformance_t3b_TargetedRetryOnlyE2E: re-plan applies TARGETED-RETRY as the sole correct verb (no done-but-wrong member exists to trigger supersede's auto-reset masking)", async ({
  page,
  createdPlanIds,
}) => {
  requireApiKey()

  test.setTimeout(600_000)
  await startFreshChatWithJim(page)

  // Setup: per-test Main agent (chat-target owner + member assignee) in its
  // own workspace core_team — same rationale as t3's createMainAgent comment.
  const ownerId = await createMainAgent(page, `conformance-t3b-owner-${Date.now()}`, { bash: 'allow' })
  const wsRes = await apiFetch<{ id: string }>(page, 'POST', '/api/v1/workspaces', {
    name: 'conformance-t3b',
    core_team: [ownerId],
  })
  if (!wsRes.ok) throw new Error(`t3b: POST /workspaces failed ${wsRes.status}: ${wsRes.raw}`)
  const workspaceId = wsRes.body.id

  // Two independent (disjoint write_set, no blocked_by — they dispatch in
  // parallel) members:
  //   m1 — trivial filler, own check trivially passes, reaches `done`, and
  //        is never named as a problem by the DoD — a `done` member that
  //        exists in the plan but is not a valid/motivated supersede target.
  //   m2 — the SOLE TARGETED-RETRY target: max_attempts=1 with a check that
  //        deterministically fails (`exit 1`), so it ends `failed` after
  //        exactly one attempt. Its title frames the failure as transient —
  //        the identical idiom Conformance_t3_PlanningReplanningE2E already
  //        used for its own m3, which is exactly the wake data PlanSupervisor
  //        reads ("member_id | status | title").
  const { planId, memberIds } = await createPlanWithMembers(
    page,
    workspaceId,
    ownerId,
    {
      title: 't3b conformance plan (targeted-retry isolation)',
      goal: 'exercise the targeted-retry correction path in isolation, with no supersede candidate',
      description:
        'owner re-plans after an unmet DoD adjudication whose ONLY problem is one failed-transient member',
      // A single prose DoD (real Judge LLM call) that names the ONE problem
      // and explicitly forecloses the other verbs: no done member's outcome
      // is in question (no supersede target), and no new work is needed (no
      // motivated append) — so TARGETED-RETRY is the only verb whose stated
      // purpose matches the diagnosis.
      dod: [
        proseCriterion(
          'Member "m2-retry-target" must have SUCCEEDED at its check. If it is currently `failed`, that ' +
            'failure was for a transient, recoverable reason — the member itself is sound and does not ' +
            'need to be replaced or abandoned; simply retry it. Member "m1-filler" already completed ' +
            'correctly and needs no correction of any kind — its outcome is NOT in question, so there is ' +
            'no done member here whose result needs discounting. There is no other problem with this plan ' +
            'and no new work is required. The plan is only met once "m2-retry-target" succeeds.',
        ),
      ],
      bounds: { plan_judge_max_rounds: 4 },
    },
    [
      {
        label: 'm1',
        title: 'm1-filler',
        prompt: 'reply with alpha',
        write_set: ['out/t3b/m1.txt'],
        criteria: [checkCriterion('m1 trivially passes', 'exit 0', 0)],
      },
      {
        label: 'm2',
        title: 'm2-retry-target: transient/flaky failure, safe and expected to succeed if retried',
        prompt: 'reply with beta',
        write_set: ['out/t3b/m2.txt'],
        max_attempts: 1,
        criteria: [checkCriterion('m2 deterministically fails its one attempt', 'exit 1', 0)],
      },
    ],
    createdPlanIds,
  )

  // Approve + run.
  const approveRes = await apiFetch<{ status: string }>(page, 'POST', `/api/v1/plans/${planId}/approve`, {})
  expect(approveRes.ok, `t3b: POST /plans/{id}/approve failed ${approveRes.status}: ${approveRes.raw}`).toBe(true)

  const HOLD_PHASE = 'awaiting_supervision'

  // --- Step 1: MANDATORY — reach the hold at least once -------------------
  // With m2 deterministically failed, a round-1 unmet verdict (and the
  // awaiting_supervision hold it triggers) is expected reliably.
  let reachedHoldOnce = false
  const firstHoldDeadline = Date.now() + 120_000
  while (Date.now() < firstHoldDeadline) {
    const poll = await apiFetch<{ plan_phase?: string }>(page, 'GET', `/api/v1/plans/${planId}`)
    if (!poll.ok) throw new Error(`t3b: GET /plans/{id} poll (first hold) failed ${poll.status}: ${poll.raw}`)
    if (poll.body.plan_phase === HOLD_PHASE) {
      reachedHoldOnce = true
      break
    }
    await page.waitForTimeout(1_500)
  }
  expect(
    reachedHoldOnce,
    `t3b: plan ${planId} must reach plan_phase=awaiting_supervision within 120s of approval — m2 ` +
      '(deterministically failed) makes a round-1 unmet verdict expected reliably.',
  ).toBe(true)

  // --- Step 2: observe the REAL correction mechanism over the plan's full
  // round budget. Same session/transcript plumbing as
  // Conformance_t3_PlanningReplanningE2E — a NEW session may be minted each
  // time the plan opens a fresh park, so every session id ever observed is
  // tracked and all of them are inspected at the end.
  const seenSessionIds = new Set<string>()
  let finalPlanState = ''
  let finalPlanPhase = ''
  const observeDeadline = Date.now() + 420_000
  while (Date.now() < observeDeadline) {
    const poll = await apiFetch<{ state: string; plan_phase?: string; supervision?: { session_id?: string } }>(
      page,
      'GET',
      `/api/v1/plans/${planId}`,
    )
    if (!poll.ok) throw new Error(`t3b: GET /plans/{id} poll (observe) failed ${poll.status}: ${poll.raw}`)
    finalPlanState = poll.body.state
    finalPlanPhase = poll.body.plan_phase ?? ''
    const sid = poll.body.supervision?.session_id
    if (sid) seenSessionIds.add(sid)
    if (finalPlanState === 'done' || finalPlanState === 'failed') break
    await page.waitForTimeout(4_000)
  }

  // Collect every plan_correct call (any status) across every adjudication
  // session this plan ever used.
  const allCalls: Array<{ status: string; parameters: Record<string, unknown> }> = []
  for (const sid of seenSessionIds) {
    const messages = await getSessionMessages(page, sid)
    allCalls.push(...extractPlanCorrectCalls(messages))
  }
  const committed = allCalls.filter((c) => c.status === 'success')

  const diagnostic =
    `plan state="${finalPlanState}" phase="${finalPlanPhase}"; sessions=${[...seenSessionIds].join(',')}; ` +
    `all plan_correct calls: ${JSON.stringify(allCalls)}`

  // MANDATORY: the mechanism actually committed at least one correction.
  // Zero committed corrections after a full round budget with one
  // unambiguous, well-motivated problem means plan_correct is not genuinely
  // wired end-to-end — a CRITICAL finding, not a soft skip.
  expect(
    committed.length,
    `t3b: zero plan_correct calls committed (status=success) across the plan's full round budget. ${diagnostic}`,
  ).toBeGreaterThan(0)

  // Real member ids — never a label string ('m1'/'m2'), always a real task
  // id from memberIds (the same E.3 defect Conformance_t3_PlanningReplanningE2E
  // exists to catch, checked here too since any committed correction — of
  // any verb — could in principle repeat it).
  const realMemberIds = new Set(Object.values(memberIds))
  const labelStrings = new Set(Object.keys(memberIds))
  for (const call of committed) {
    const supersededId = call.parameters.superseded_member_id as string | undefined
    const retriedId = call.parameters.retried_member_id as string | undefined
    const targetId = supersededId ?? retriedId
    if (targetId === undefined) continue // append/abandon name no existing member
    expect(
      labelStrings.has(targetId),
      `t3b: a committed correction named a LABEL STRING ("${targetId}") instead of a real task id. ${diagnostic}`,
    ).toBe(false)
    expect(
      realMemberIds.has(targetId),
      `t3b: a committed correction's target id ("${targetId}") does not match any real member task id ` +
        `(${[...realMemberIds].join(', ')}). ${diagnostic}`,
    ).toBe(true)
  }

  // THE core ask: TARGETED-RETRY was actually applied against m2's real id.
  // This scenario has no done-but-wrong member (supersede has no valid
  // target) and the DoD forecloses new work (no motivated append), so
  // TARGETED-RETRY is the one verb whose stated purpose matches the
  // diagnosis. This is a hard requirement, not a best-effort one — if the
  // real adjudicator never reaches for it here, that is real, reportable
  // information about the correction path, not something to downgrade to a
  // soft pass.
  const verbsSeen = new Set(committed.map((c) => c.parameters.verb as string))
  expect(
    verbsSeen.has('targeted_retry'),
    `t3b: no committed correction used verb="targeted_retry" against m2 (the sole failed-transient target). ` +
      `Verbs actually observed: ${[...verbsSeen].join(', ') || '(none)'}. ${diagnostic}`,
  ).toBe(true)

  // Cross-check the SPECIFIC member: at least one targeted_retry named m2's
  // real id (not just "targeted_retry occurred somewhere, against an
  // arbitrary target").
  const retriedM2 = committed.some(
    (c) => c.parameters.verb === 'targeted_retry' && c.parameters.retried_member_id === memberIds.m2,
  )
  expect(
    retriedM2,
    `t3b: no committed targeted_retry named m2's real id (${memberIds.m2}) as retried_member_id. ${diagnostic}`,
  ).toBe(true)

  // No-wedge sanity: the plan must not still be stuck neither terminal nor
  // held after the full observation window.
  expect(
    finalPlanState === 'done' || finalPlanState === 'failed' || finalPlanPhase === HOLD_PHASE,
    `t3b: plan ${planId} must be at a documented terminus or still legitimately held after the observation ` +
      `window — observed state="${finalPlanState}" phase="${finalPlanPhase}".`,
  ).toBe(true)
})
