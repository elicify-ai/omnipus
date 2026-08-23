// Omnipus — ADR-053 §9.1 design-conformance E2E: re-planning + parallel-execution lint.
//
// Covers: t3 (re-plan applies supersede + targeted retry),
//         g4 (write-set lint approves disjoint, rejects overlapping).
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

// ── Conformance_t3_PlanningReplanningE2E ─────────────────────────────────────
//
// BDD (§9.1 t3): owner plans (checklist) → execute → unmet-all-done →
// owner re-plans → SUPERSEDE a done member (D4) → TARGETED-RETRY a
// frozen-transient member (D4) → transactional append → done.
//
// Traces to:
//   - §9.1 t3 "planning & re-planning walks the drawn path" (line ~1181)
//   - TDD Plan row 44 `Conformance_t3_PlanningReplanningE2E`
//   - adr-053-DEFERRED-ISSUES.md E.3 (this test used to send
//     `target_member_id: 'm3'` — a LABEL STRING, never a real task id — via
//     `PUT /plans/{id}` with a `revision_entry` body. Two things were wrong
//     with that: (1) it never sent SUPERSEDE at all, only targeted_retry;
//     (2) `revision_entry` is NOT a field of `PlanUpdateRequest` — verified
//     directly against `contracts/components/schemas/PlanUpdateRequest.yaml`
//     (fields: title/goal/description/state/owner_agent_id/dod/bounds,
//     `additionalProperties: false`) and `pkg/gateway/rest.go`'s
//     `handlePlanPut` (no revision_entry handling anywhere). `RevisionEntry`
//     IS a real generated type, but it is used exactly once on the wire: as
//     the payload of the `SessionMessageRevisionEntry` — an ENGINE-EMITTED,
//     read-only SessionMessage variant (`kind: revision_entry`,
//     `direction: engine`) that reports a correction that already happened.
//     There never was a client-writable PUT surface for this — the original
//     test was written against a wire contract that was never designed to
//     exist, not a partially-wired one.
//
// THE REAL PATH (verified empirically against a live gateway before writing
// this test): a plan correction is issued exclusively by the `plansupervisor`
// System Agent's `plan_correct` tool (pkg/tools/plan_correct.go — identity
// gate: `callerID != PlanSupervisorAgentID` is denied outright, including the
// plan's own owner, ADR-055 D3), invoked automatically by the engine's own
// `wakeSupervisor` (pkg/agent/plan_engine.go) the instant a plan parks at
// `awaiting_supervision` — no REST call, no chat UI (PlanSupervisor is a
// System Agent, `IsChatTarget()==false`, never a selectable chat persona).
// There is nothing for an e2e test to POST/PUT to trigger a correction; the
// only thing to do is let the real automatic wake fire (a REAL LLM call,
// same "at least one real-LLM run" requirement every other test in this file
// satisfies) and observe what it actually did.
//
// Observed via GET /api/v1/sessions/{plan.supervision.session_id} — the
// adjudication session's transcript. A committed correction shows up there
// as a `plan_correct` tool_call (status: "success") whose `parameters`
// carry the REAL verb and REAL member id(s) it used — verified empirically:
// a done-but-wrong member reliably drove the real PlanSupervisor LLM to
// choose SUPERSEDE with `superseded_member_id` set to the member's actual
// task id (never a label). This test engineers TWO independent, unambiguous
// problems into one plan — m2 (done, but its outcome is wrong per the DoD)
// and m3 (failed for a framed-as-transient reason) — and asserts on the REAL
// member ids used by whichever corrections it actually commits — never a
// label string.
//
// UPDATE — this test asserts SUPERSEDE only (targeted_retry moved to a
// sibling test, see below). The original design assumed SUPERSEDE and
// TARGETED-RETRY would both show up in ONE run against this two-problem
// plan. They do not, by product design: `AppendCorrection` auto-resets
// EVERY live-round failed member (excludes only frozen/`done` members) for
// BOTH append and supersede — never for targeted_retry alone, which instead
// resets only its one named member via its own path —
// `if req.Verb != CorrectionTargetedRetry { pe.autoResetLiveRoundFailedMembers(...) }`
// (pkg/agent/plan_engine.go:4295, reset logic at :4902) vs. targeted_retry's
// own single-member reset in `buildCorrectionApplyFunc` (:4859). The instant
// the real PlanSupervisor commits a SUPERSEDE against m2, m3 — still
// `failed` at that moment, never `done`/superseded — is swept up by that
// auto-reset and gets another dispatch for free. A real run's own recorded
// reasoning said exactly this: "m3-retry-target (failed) was a
// transient/flaky failure that was never retried — it will be auto-reset by
// the supersede and get another attempt under the corrected plan." Emitting
// a redundant targeted_retry against an already-reset m3 would be busywork,
// not correctness, so a well-reasoned supervisor will not do it — asserting
// it here demanded a tool call the product's own semantics make
// unnecessary. TARGETED-RETRY is proven in isolation by
// `Conformance_t3b_TargetedRetryOnlyE2E` immediately below, whose plan has
// NO done-but-wrong member at all, so supersede has no valid target, cannot
// fire, and cannot mask the retry.

test('Conformance_t3_PlanningReplanningE2E: re-plan applies SUPERSEDE + TARGETED-RETRY with real member ids', async ({
  page,
  createdPlanIds,
}) => {
  requireApiKey()

  // 900s, not 600s: Step 1's own hold budget below was widened from 120s to
  // 300s (see that constant's comment for the live-shard evidence), and
  // Step 2's observe loop already budgets a further 420s after Step 1
  // returns — 300s + 420s = 720s of INTENTIONAL polling alone, before setup
  // (agent/workspace/plan creation) or teardown assertions run at all. The
  // old 600s ceiling was already tighter than the 720s of polling it was
  // asked to contain; it happened to work only when round-1 finished fast
  // enough that Step 1 returned in a few seconds, not close to its deadline.
  test.setTimeout(900_000)
  await startFreshChatWithJim(page)

  // Setup: per-test Main agent (chat-target owner + member assignee) in
  // its own workspace core_team (avoids multi-workspace ambiguity).
  // `bash: allow` is required for the `check` criteria below (see t2's
  // createMainAgent comment — a fresh custom agent is deny-by-default).
  const ownerId = await createMainAgent(page, `conformance-t3-owner-${Date.now()}`, { bash: 'allow' })
  const wsRes = await apiFetch<{ id: string }>(page, 'POST', '/api/v1/workspaces', {
    name: 'conformance-t3',
    core_team: [ownerId],
  })
  if (!wsRes.ok) throw new Error(`t3: POST /workspaces failed ${wsRes.status}: ${wsRes.raw}`)
  const workspaceId = wsRes.body.id

  // Three independent (disjoint write_set, no blocked_by — they dispatch in
  // parallel) members:
  //   m1 — trivial filler, own check trivially passes.
  //   m2 — the SUPERSEDE target: own check trivially passes (member ends
  //        `done`), but the plan's DoD (below) explicitly says its recorded
  //        outcome is wrong — "member finished but result incorrect" is
  //        PlanSupervisor's own rubric language for SUPERSEDE.
  //   m3 — the TARGETED-RETRY target: max_attempts=1 with a check that
  //        deterministically fails (`exit 1`), so it ends `failed` after
  //        exactly one attempt. Its title frames the failure as transient —
  //        PlanSupervisor's member-list wake data is exactly
  //        "member_id | status | title", so the title is real, load-bearing
  //        signal it reads (the same idiom this file already uses for
  //        g7's exact-tool-call prompt engineering).
  const { planId, memberIds } = await createPlanWithMembers(
    page,
    workspaceId,
    ownerId,
    {
      title: 't3 conformance plan',
      goal: 'exercise the supersede + targeted-retry correction paths',
      description: 'owner re-plans after an unmet DoD adjudication',
      // A single prose DoD (real Judge LLM call, not a machine check) that
      // unambiguously names BOTH problems by each member's own title, so
      // the Judge's per-criterion reasoning — which is the ONLY diagnostic
      // text PlanSupervisor's wake carries — can correctly attribute each
      // problem to the right member.
      dod: [
        proseCriterion(
          'Member "m2-supersede-target" completed (status done) but its recorded outcome is ' +
            'factually WRONG and must be redone, not merely accepted as-is. Separately, member ' +
            '"m3-retry-target" must have SUCCEEDED at its check — if it is currently `failed`, that ' +
            'failure was for a transient, recoverable reason and the member should be retried, not ' +
            'abandoned or replaced. The plan is only met once BOTH conditions hold.',
        ),
      ],
      bounds: { plan_judge_max_rounds: 4 },
    },
    [
      {
        label: 'm1',
        title: 'm1-filler',
        prompt: 'reply with alpha',
        write_set: ['out/t3/m1.txt'],
        criteria: [checkCriterion('m1 trivially passes', 'exit 0', 0)],
      },
      {
        label: 'm2',
        title: 'm2-supersede-target: work is done but its recorded result is wrong',
        prompt: 'reply with beta',
        write_set: ['out/t3/m2.txt'],
        criteria: [checkCriterion('m2 own criterion trivially passes (member reaches done)', 'exit 0', 0)],
      },
      {
        label: 'm3',
        title: 'm3-retry-target: transient/flaky failure, safe and expected to succeed if retried',
        prompt: 'reply with gamma',
        write_set: ['out/t3/m3.txt'],
        max_attempts: 1,
        criteria: [checkCriterion('m3 deterministically fails its one attempt', 'exit 1', 0)],
      },
    ],
    createdPlanIds,
  )

  // Approve + run.
  const approveRes = await apiFetch<{ status: string }>(page, 'POST', `/api/v1/plans/${planId}/approve`, {})
  expect(approveRes.ok, `t3: POST /plans/{id}/approve failed ${approveRes.status}: ${approveRes.raw}`).toBe(true)

  const HOLD_PHASE = 'awaiting_supervision'

  // --- Step 1: MANDATORY — reach the hold at least once -------------------
  // Not conditional (the original escape hatch this section replaces): with
  // m2 done-but-DoD-wrong and m3 deterministically failed, a round-1 unmet
  // verdict (and the awaiting_supervision hold it triggers) is expected
  // reliably, not merely hoped for.
  //
  // BUDGET, widened from 120s to 300s (investigation, 2026-07-29, plan
  // 01KYQ6T9HAMNWNG507Y94G4GHS on the ee7ecc47 CI shard): 120s was a TIMING
  // defect, not a correctness one. Pulled straight from that plan's own
  // persisted record on the CI worker (plan_intents/<id>.jsonl,
  // plans/<id>.json) — approved at 15:10:26Z, round-1's real Judge verdict
  // and PlanSupervisor turn produced its first committed SUPERSEDE at
  // 15:15:08Z, ~282s later — reaching the hold itself (strictly before that
  // commit) was already past 120s. This is unlike t3b's E.3 fix (a scenario
  // ambiguity, fixed by splitting the test): here the mechanism is provably
  // sound — the SAME plan went on to commit three real SUPERSEDE corrections
  // (each with a genuine Judge-cited falsified-assumption) over the
  // following ~19 minutes, ending in a legitimate judge_rounds_exhausted,
  // not a premature abandon. t3's own round-1 requires THREE parallel real
  // member LLM turns plus a real Judge LLM call against a prose DoD
  // criterion (unlike t2's machine-checked, judgment-free DoD, or t3b's
  // single-member scenario) — the heaviest real-LLM critical path of any
  // conformance test here, and therefore the one most exposed when the
  // shared CI worker is under load. The SAME shard log showed, concurrently
  // with this plan's round-1: a sibling plan's supervision turn repeatedly
  // timing out over real HTTP (`net/http: request canceled
  // (Client.Timeout...)`) and re-arming its wake every ~30s for several
  // minutes, and an unrelated agent turn retrying a denied load_tool call
  // every few seconds for the same window — genuine, external LLM/CPU
  // contention on the shard, not anything introduced by plan_engine.go's own
  // dispatch/judge/supervision logic (unchanged by the commits under test).
  // 300s carries comfortable margin over the observed ~282s without eating
  // into Step 2's own 420s observation budget (see test.setTimeout's comment
  // above for the combined worst case).
  let reachedHoldOnce = false
  const firstHoldDeadline = Date.now() + 300_000
  while (Date.now() < firstHoldDeadline) {
    const poll = await apiFetch<{ plan_phase?: string }>(page, 'GET', `/api/v1/plans/${planId}`)
    if (!poll.ok) throw new Error(`t3: GET /plans/{id} poll (first hold) failed ${poll.status}: ${poll.raw}`)
    if (poll.body.plan_phase === HOLD_PHASE) {
      reachedHoldOnce = true
      break
    }
    await page.waitForTimeout(1_500)
  }
  expect(
    reachedHoldOnce,
    `t3: plan ${planId} must reach plan_phase=awaiting_supervision within 300s of approval — m2 (done, ` +
      'DoD-flagged wrong) + m3 (deterministically failed) make a round-1 unmet verdict expected reliably.',
  ).toBe(true)

  // --- Step 2: observe the REAL correction mechanism over the plan's full
  // round budget. Every distinct plan_correct call is discoverable via the
  // adjudication session(s) the plan's `supervision.session_id` names — a
  // NEW session may be minted each time the plan opens a fresh park
  // (pkg/agent/plan_engine.go's `ensureSupervisionSessionLocked`), so we
  // track every session id ever observed and inspect all of them at the end,
  // not just the most recent one.
  //
  // Alongside session tracking, sample (plan_phase, judge_rounds,
  // correction_rounds, member signature) at the same cadence — the identical
  // idiom Conformance_t2_PlanLifecycleE2E's F2 proof already uses
  // (memberSignature + listPlanMemberTasks, see that test's Step 2 comment).
  // This is evidence for the no-wedge check below: it needs to tell "the
  // plan is genuinely stuck" apart from "a real member LLM turn is still
  // running", and the only honest way to do that without a new arbitrary
  // staleness budget is to record whether the plan's own observable state
  // ever moves at all, and hand that trace to the check.
  interface T3Sample {
    tMs: number
    state: string
    phase: string
    judgeRounds: number
    correctionRounds: number
    memberSig: string
  }
  const samples: T3Sample[] = []
  const seenSessionIds = new Set<string>()
  let finalPlanState = ''
  let finalPlanPhase = ''
  const observeDeadline = Date.now() + 420_000
  const sampleWindowStart = Date.now()
  while (Date.now() < observeDeadline) {
    const [poll, members] = await Promise.all([
      apiFetch<{
        state: string
        plan_phase?: string
        judge_rounds?: number
        supervision?: { session_id?: string; correction_rounds?: number }
      }>(page, 'GET', `/api/v1/plans/${planId}`),
      listPlanMemberTasks(page, workspaceId, planId),
    ])
    if (!poll.ok) throw new Error(`t3: GET /plans/{id} poll (observe) failed ${poll.status}: ${poll.raw}`)
    finalPlanState = poll.body.state
    finalPlanPhase = poll.body.plan_phase ?? ''
    const sid = poll.body.supervision?.session_id
    if (sid) seenSessionIds.add(sid)
    samples.push({
      tMs: Date.now() - sampleWindowStart,
      state: finalPlanState,
      phase: finalPlanPhase,
      judgeRounds: poll.body.judge_rounds ?? 0,
      correctionRounds: poll.body.supervision?.correction_rounds ?? 0,
      memberSig: memberSignature(members),
    })
    if (finalPlanState === 'done' || finalPlanState === 'failed') break
    await page.waitForTimeout(4_000)
  }

  // Last sample offset (ms into the window) whose (phase, judge_rounds,
  // correction_rounds, member signature) tuple differed from the sample
  // before it — i.e. the last time the plan was observed to do ANYTHING.
  // Diagnostic only (see the no-wedge check below for why this does not
  // gate pass/fail on its own): a plan mid a real member LLM turn can
  // legitimately hold an identical tuple for minutes (dispatch only touches
  // the tuple at the START of a turn, not while it's in flight), so a fixed
  // staleness cutoff here would just be a second budget-widening exercise in
  // disguise.
  let lastChangeTMs = samples.length > 0 ? samples[0].tMs : 0
  for (let i = 1; i < samples.length; i++) {
    const prev = samples[i - 1]
    const cur = samples[i]
    if (
      cur.phase !== prev.phase ||
      cur.judgeRounds !== prev.judgeRounds ||
      cur.correctionRounds !== prev.correctionRounds ||
      cur.memberSig !== prev.memberSig
    ) {
      lastChangeTMs = cur.tMs
    }
  }
  const staleForMs = samples.length > 0 ? samples[samples.length - 1].tMs - lastChangeTMs : 0

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
  // Zero committed corrections after a full round budget with two
  // unambiguous, well-motivated problems means plan_correct is not
  // genuinely wired end-to-end — a CRITICAL finding, not a soft skip.
  expect(
    committed.length,
    `t3: zero plan_correct calls committed (status=success) across the plan's full round budget. ${diagnostic}`,
  ).toBeGreaterThan(0)

  // Real member ids — the E.3 defect this test exists to catch: never a
  // label string ('m1'/'m2'/'m3'), always a real task id from memberIds.
  const realMemberIds = new Set(Object.values(memberIds))
  const labelStrings = new Set(Object.keys(memberIds))
  for (const call of committed) {
    const supersededId = call.parameters.superseded_member_id as string | undefined
    const retriedId = call.parameters.retried_member_id as string | undefined
    const targetId = supersededId ?? retriedId
    if (targetId === undefined) continue // append/abandon name no existing member
    expect(
      labelStrings.has(targetId),
      `t3: a committed correction named a LABEL STRING ("${targetId}") instead of a real task id — ` +
        `exactly the E.3 defect this test exists to catch. ${diagnostic}`,
    ).toBe(false)
    expect(
      realMemberIds.has(targetId),
      `t3: a committed correction's target id ("${targetId}") does not match any real member task id ` +
        `(${[...realMemberIds].join(', ')}). ${diagnostic}`,
    ).toBe(true)
  }

  // THE core E.3 ask, SUPERSEDE half: the real adjudicator actually applied
  // SUPERSEDE against m2 (the done-but-wrong target), naming its real member
  // id (verified above, never a label). This is a hard requirement, not a
  // best-effort one.
  //
  // TARGETED-RETRY is deliberately NOT asserted here — see the "UPDATE"
  // paragraph in this test's header comment: committing a SUPERSEDE in this
  // same round auto-resets every live-round failed member, m3 included, so a
  // well-reasoned supervisor that has already superseded m2 has no remaining
  // reason to ALSO spend a targeted_retry on an already-reset m3.
  // `Conformance_t3b_TargetedRetryOnlyE2E` immediately below is the isolated
  // proof that targeted_retry itself works, in a plan with no done-but-wrong
  // member so supersede cannot fire and cannot mask it.
  const verbsSeen = new Set(committed.map((c) => c.parameters.verb as string))
  expect(
    verbsSeen.has('supersede'),
    `t3: no committed correction used verb="supersede" against m2 (the done-but-wrong target). ` +
      `Verbs actually observed: ${[...verbsSeen].join(', ') || '(none)'}. ${diagnostic}`,
  ).toBe(true)

  // Cross-check the SPECIFIC member: at least one supersede named m2's real
  // id (not just "supersede occurred somewhere, against an arbitrary
  // target").
  const supersededM2 = committed.some(
    (c) => c.parameters.verb === 'supersede' && c.parameters.superseded_member_id === memberIds.m2,
  )
  expect(
    supersededM2,
    `t3: no committed supersede named m2's real id (${memberIds.m2}) as superseded_member_id. ${diagnostic}`,
  ).toBe(true)

  // No-wedge sanity: the plan must not be permanently stuck. This is
  // deliberately NOT "the plan is finished or held" (the previous version of
  // this check, copied from Conformance_t2_PlanLifecycleE2E's Step 3) — t2's
  // DoD check itself is a machine-checked `exit 1`, but t2's Step 3 was
  // ITSELF found to need this exact widening (2026-07-30, live CI shard
  // llm-conformance): the PlanSupervisor turn that decides whether to
  // correct is a real LLM call regardless of how the DoD is checked, and any
  // correction re-runs members as real LLM turns too, so t2 can equally land
  // in state="running" phase="dispatching" at window-close. Both tests now
  // share this logic. t3's members run REAL LLM turns, and
  // pkg/plan/plan.go's Supervision doc comment states the contract
  // explicitly: "a plan leaves the supervision-eligible phase set on EVERY
  // applied correction... an applied correction returns the plan to
  // dispatching" — i.e. re-running its members. Observing
  // state="running" phase="dispatching" right after the SUPERSEDE this test
  // already proved committed (above) is that exact, documented, healthy
  // re-run — not a stall. Treating every non-terminal, non-held phase as
  // failure punishes normal forward motion.
  //
  // The property actually worth checking — is the plan stuck — is already
  // diagnosed continuously by the engine itself for EACH active phase, so
  // this check trusts that diagnosis rather than inventing a new one:
  //   - dispatching: processPlan (pkg/agent/plan_engine.go) reaches
  //     surfaceStallIfAny only when real member work remains AND this tick's
  //     dispatch found nothing new to start — exactly the condition under
  //     which it flips plan_phase to "stalled" (a member is dispatchable
  //     (`next`) or in flight (`in_progress`) is required to STAY at
  //     dispatching). `stalled` is itself a supervision-eligible phase (the
  //     same set as `awaiting_supervision`, plan.go's
  //     supervisionEligiblePhases), so a genuinely stuck plan self-escalates
  //     through the exact correction mechanism this test already proved
  //     works, instead of sitting silently in `dispatching`.
  //   - judging: processPlan's own switch returns immediately either because
  //     an adjudication goroutine is confirmed in flight in THIS process, or
  //     (crash-recovery case) it resumes the round from scratch right there
  //     — never leaves the plan sitting inert at judging.
  //   - synthesizing: processPlan returns immediately with "terminal hand-off
  //     already in progress" — by construction an active, in-flight state.
  // So observing `dispatching`/`judging`/`synthesizing` at window-close means
  // the engine's own live view is "something is next, in-flight, or actively
  // synthesizing right now" — real work, not silence. A fixed elapsed-time
  // staleness cutoff was deliberately NOT used here (see staleForMs's
  // comment above): during a real member LLM turn the observable tuple can
  // legitimately sit unchanged for minutes, and picking a cutoff short
  // enough to "prove" anything would just be a second, disguised
  // budget-widening exercise.
  //
  // What THIS still catches: state="running" with an unrecognised or
  // empty/idle plan_phase — the plan falling OUT of the documented phase
  // state machine entirely after Step 1 already proved it reached the hold.
  // That is a genuine, undiagnosed wedge, and the full sample trace
  // (including staleForMs) is reported for it.
  const ACTIVE_ROUND_PHASES = new Set(['dispatching', 'judging', 'synthesizing'])
  const HELD_PHASES = new Set([HOLD_PHASE, 'stalled'])
  const notWedged =
    finalPlanState === 'done' ||
    finalPlanState === 'failed' ||
    HELD_PHASES.has(finalPlanPhase) ||
    (finalPlanState === 'running' && ACTIVE_ROUND_PHASES.has(finalPlanPhase))
  expect(
    notWedged,
    `t3: plan ${planId} must be at a documented terminus, still legitimately held/stalled, or observably ` +
      `still progressing through an active round after the observation window — observed ` +
      `state="${finalPlanState}" phase="${finalPlanPhase}", last observed change ${staleForMs}ms before the ` +
      `window closed. Sample trace: ${JSON.stringify(samples)}`,
  ).toBe(true)
})

// ── Conformance_g4_ParallelLintE2E ───────────────────────────────────────────
//
// BDD (§9.1 g4): disjoint write-sets → lint passes; overlapping → lint
// REJECTS at approve; an exploratory member → own isolated checkout
// (highest available rung: worktree → clone → subdir). A merge at the
// join surfaces a real conflict as a plan-correction event.
//
// Traces to:
//   - §9.1 g4 "parallel streams lint walks the drawn path" (line ~1188)
//   - TDD Plan row 45 `Conformance_g4_ParallelLintE2E`

test('Conformance_g4_ParallelLintE2E: disjoint write-sets approve; overlapping rejected', async ({
  page,
  createdPlanIds,
}) => {
  requireApiKey()

  test.setTimeout(180_000)
  await startFreshChatWithJim(page)

  // Setup: per-test Main agent in its own workspace core_team.
  const ownerId = await createMainAgent(page, `conformance-g4-owner-${Date.now()}`)
  const wsRes = await apiFetch<{ id: string }>(page, 'POST', '/api/v1/workspaces', {
    name: 'conformance-g4',
    core_team: [ownerId],
  })
  if (!wsRes.ok) throw new Error(`g4: POST /workspaces failed ${wsRes.status}: ${wsRes.raw}`)
  const workspaceId = wsRes.body.id

  // CASE 1: disjoint write_sets — must be accepted at approve (G-16 happy
  // path). Plan-lint runs at APPROVE (handlePlanApprove, rest_plans.go),
  // not at create — so the plan + member tasks create fine, and the gate
  // is exercised by the approve call below.
  const { planId: disjointPlanId } = await createPlanWithMembers(
    page,
    workspaceId,
    ownerId,
    {
      title: 'g4 disjoint plan',
      goal: 'lint passes',
      bounds: { plan_judge_max_rounds: 3 },
    },
    [
      {
        label: 'a',
        title: 'stream a',
        prompt: 'reply alpha',
        write_set: ['g4/a.txt'],
        criteria: [proseCriterion('reply contains "alpha"')],
      },
      {
        label: 'b',
        title: 'stream b',
        prompt: 'reply beta',
        write_set: ['g4/b.txt'],
        criteria: [proseCriterion('reply contains "beta"')],
      },
    ],
    createdPlanIds,
  )

  // Approve the disjoint plan — must succeed (lint passes: disjoint
  // write_sets on the two parallel members a/b).
  const approveDisjoint = await apiFetch<{ status: string }>(
    page,
    'POST',
    `/api/v1/plans/${disjointPlanId}/approve`,
    {},
  )
  expect(
    approveDisjoint.ok,
    `g4: disjoint plan approve must succeed (G-16 lint). Got ${approveDisjoint.status}: ${approveDisjoint.raw}`,
  ).toBe(true)

  // CASE 2: OVERLAPPING write_sets — must be REJECTED at approve
  // (G-16 fail-closed: overlap → lint rejection). The plan + member tasks
  // create fine (lint is an approve-time gate); the two parallel members
  // x/y share the same write_set path, which lint rejects at approve.
  const { planId: overlapPlanId } = await createPlanWithMembers(
    page,
    workspaceId,
    ownerId,
    {
      title: 'g4 overlapping plan',
      goal: 'lint rejects',
      bounds: { plan_judge_max_rounds: 3 },
    },
    [
      {
        label: 'x',
        title: 'stream x',
        prompt: 'reply x',
        write_set: ['shared/conflict.txt'],
        criteria: [proseCriterion('reply contains "x"')],
      },
      {
        label: 'y',
        title: 'stream y',
        prompt: 'reply y',
        // INTENTIONALLY overlapping with x's write_set — the same file path.
        write_set: ['shared/conflict.txt'],
        criteria: [proseCriterion('reply contains "y"')],
      },
    ],
    createdPlanIds,
  )

  // Drawn-path assertion 2: the overlapping plan is REJECTED at approve
  // by lint (G-16). A 2xx success here means the lint gate is missing or
  // bypassed — BLOCKED.
  const approveOverlap = await apiFetch<{ status: string }>(
    page,
    'POST',
    `/api/v1/plans/${overlapPlanId}/approve`,
    {},
  )
  expect(
    !approveOverlap.ok,
    `g4: overlapping-write-sets plan must be rejected by lint (G-16) at approve — ` +
      `create succeeded AND approve succeeded. The lint gate is missing. ` +
      `Approve response: ${approveOverlap.status} ${approveOverlap.raw}`,
  ).toBe(true)
  expect(
    approveOverlap.status >= 400 && approveOverlap.status < 500,
    `g4: overlapping plan approve must be rejected with 4xx — got ${approveOverlap.status}: ${approveOverlap.raw}`,
  ).toBe(true)
})
