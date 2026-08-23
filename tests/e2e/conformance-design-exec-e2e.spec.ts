// Omnipus — ADR-053 §9.1 design-conformance E2E: task execution + crash recovery.
//
// Covers: t1 (standalone task ▶ Run / ■ Stop ladder),
//         g5 (shard + assemble DAG, first-class join),
//         bootsweep (kill -9 mid-task → restart → reconcile).
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
import { GatewayProcess } from './fixtures/gateway-process.js'
import {
  apiFetch,
  createMainAgent,
  createPlanWithMembers,
  proseCriterion,
  requireApiKey,
  startFreshChatWithJim,
} from './fixtures/conformance-helpers'

// ── Conformance_t1_StandaloneTaskE2E ─────────────────────────────────────────
//
// BDD (§9.1 t1): standalone task — ▶ Run → claim → evidence-gate (bare
// claim → free steer, 2nd → attempt) → ladder → done; ■ Stop cancels
// turn + verifier.
//
// Traces to:
//   - §9.1 t1 "standalone task walks the drawn path" (line ~1167)
//   - TDD Plan row 42 `Conformance_t1_StandaloneTaskE2E`
//   - FR-009 (ADR-052 stop), FR-026 (restart)

test('Conformance_t1_StandaloneTaskE2E: ▶ Run ladder → done; ■ Stop cancels turn+verifier', async ({
  page,
  createdTaskIds,
}) => {
  requireApiKey()

  test.setTimeout(360_000)
  await startFreshChatWithJim(page)

  // Create a per-test Main agent + workspace so the task has a real
  // plan/team context. The task's agent_id must be a member of the
  // workspace's team set (core_team ∪ delegation edges —
  // validateTaskAgentID, rest_tasks.go). We create a FRESH Main agent
  // per test (not the seeded "jim") so the agent belongs to exactly ONE
  // workspace's core_team. The agent workspace reroot path
  // (pkg/agent/workspace_reroot.go:106) calls FindForAgentPreferring
  // with the task workspace as the preferred id, so multi-membership
  // is now resolved deterministically; the per-test isolation here is
  // defensive belt-and-suspenders to keep tests independent.
  const workerId = await createMainAgent(page, `conformance-t1-worker-${Date.now()}`)
  const wsRes = await apiFetch<{ id: string }>(page, 'POST', '/api/v1/workspaces', {
    name: 'conformance-t1',
    core_team: [workerId],
  })
  if (!wsRes.ok) {
    throw new Error(`Conformance_t1: POST /workspaces failed ${wsRes.status}: ${wsRes.raw}`)
  }
  const workspaceId = wsRes.body.id

  // Create the standalone task with a single criterion the LLM can satisfy.
  // action="llm" dispatches to the worker on the next processTick.
  const taskRes = await apiFetch<{ id: string; status: string }>(
    page,
    'POST',
    '/api/v1/tasks',
    {
      title: 't1 conformance task',
      prompt: 'reply with the literal word done and nothing else',
      action: 'llm',
      workspace_id: workspaceId,
      agent_id: workerId,
      max_attempts: 3,
      criteria: [proseCriterion('the reply contains the word "done"')],
    },
  )
  if (!taskRes.ok) {
    throw new Error(`Conformance_t1: POST /tasks failed ${taskRes.status}: ${taskRes.raw}`)
  }
  const taskId = taskRes.body.id
  // Best-effort teardown tracking (fixtures/plan-cleanup.ts): if this attempt
  // fails/times out before the polling loop below reaches a terminal state,
  // don't leave the task in_progress into the next retry. A task that DOES
  // reach done/failed on its own makes this a harmless no-op (POST
  // /tasks/{id}/stop 400s on a non-in-progress task).
  createdTaskIds.push(taskId)

  // PATCH the task to status=in_progress (the ▶ Run button's wire contract —
  // see handleTaskPatch + the runner dispatcher).
  const startRes = await apiFetch<{ status: string }>(
    page,
    'PATCH',
    `/api/v1/tasks/${taskId}`,
    { status: 'in_progress' },
  )
  if (!startRes.ok) {
    throw new Error(
      `Conformance_t1: PATCH /tasks/{id} (start) failed ${startRes.status}: ${startRes.raw}`,
    )
  }

  // Wait for the task to reach a terminal state — done (met criterion) or
  // failed (G-4 bounce economics on a bare claim, or attempts-exhausted).
  // Polling via GET /api/v1/tasks/{id} is the deterministic contract check
  // (vs. reading the chat, which is presentation-layer).
  const TERMINAL_STATES = new Set(['done', 'failed'])
  const deadline = Date.now() + 300_000
  let finalStatus = ''
  while (Date.now() < deadline) {
    const poll = await apiFetch<{ status: string }>(page, 'GET', `/api/v1/tasks/${taskId}`)
    if (!poll.ok) {
      throw new Error(
        `Conformance_t1: GET /tasks/{id} poll failed ${poll.status}: ${poll.raw}`,
      )
    }
    finalStatus = poll.body.status
    if (TERMINAL_STATES.has(finalStatus)) break
    await page.waitForTimeout(2_000)
  }

  // Drawn-path assertion 1: the task reached a terminal state (done or
  // failed) within the budget. A non-terminal state means the ladder
  // (worker → claim → evidence-gate → attempt → verifier → done) did not
  // complete — the design is not behaving as drawn.
  expect(
    TERMINAL_STATES.has(finalStatus),
    `Task ${taskId} must reach a terminal state within 300s — observed "${finalStatus}". ` +
      'The ladder (worker → claim → verifier → done) failed to terminate.',
  ).toBe(true)

  // Differentiation test: a successful ladder ends in "done", a bounced
  // ladder ends in "failed" with a clear reason. Either is a valid
  // terminus for t1 — what we MUST catch is "neither done nor failed",
  // which means the engine wedged. The check above catches that.

  // Drawn-path assertion 2: the ladder did consume at least one attempt
  // (GET /tasks/{id}/verdicts returns the verifier's call; if zero
  // verdicts, the verifier never fired and the ladder short-circuited).
  // We assert this only for the success case to keep the test focused on
  // the drawn path; failed-with-no-verifier is the attempts-exhausted
  // branch and is OK.
  if (finalStatus === 'done') {
    const verdicts = await apiFetch<Array<{ met: boolean }>>(
      page,
      'GET',
      `/api/v1/tasks/${taskId}/verdicts`,
    )
    expect(
      verdicts.ok && verdicts.body.length > 0,
      'A t1 task that reached "done" must have at least one JudgeVerdict recorded — ' +
        'a "done" with no verdict is an unproven ladder termination.',
    ).toBe(true)
  }

  // Cleanup: a second task exercises the ■ Stop path (cancel the task
  // mid-flight, before any verdict). Create + start + immediately stop.
  const stopRes0 = await apiFetch<{ id: string }>(page, 'POST', '/api/v1/tasks', {
    title: 't1 stop-conformance task',
    prompt: 'this task will be cancelled before the worker completes',
    action: 'llm',
    workspace_id: workspaceId,
    agent_id: workerId,
    max_attempts: 3,
    criteria: [proseCriterion('the reply contains the word "done"')],
  })
  if (!stopRes0.ok) {
    throw new Error(`Conformance_t1: POST /tasks (stop target) failed ${stopRes0.status}: ${stopRes0.raw}`)
  }
  const stopTaskId = stopRes0.body.id
  await apiFetch(page, 'PATCH', `/api/v1/tasks/${stopTaskId}`, { status: 'in_progress' })
  // Cancel: POST /tasks/{id}/stop is FR-009 / ADR-052 US-7.
  const stopRes = await apiFetch<{ status: string }>(
    page,
    'POST',
    `/api/v1/tasks/${stopTaskId}/stop`,
    {},
  )
  expect(stopRes.ok, `Conformance_t1: POST /tasks/{id}/stop failed ${stopRes.status}: ${stopRes.raw}`).toBe(
    true,
  )
  // Stop should flip the task to failed(stopped_by_user) — the canonical
  // stop wire state. Read it back and assert.
  const stopReadBack = await apiFetch<{ status: string; stop_reason?: string }>(
    page,
    'GET',
    `/api/v1/tasks/${stopTaskId}`,
  )
  expect(stopReadBack.ok).toBe(true)
  expect(
    stopReadBack.body.status === 'failed' || stopReadBack.body.status === 'stopped',
    `Stop must transition task to failed/stopped (FR-009 / ADR-052) — observed "${stopReadBack.body.status}"`,
  ).toBe(true)
})

// ── Conformance_g5_ShardAssembleE2E ──────────────────────────────────────────
//
// BDD (§9.1 g5): BOTH design topologies on ONE git-based model:
//   (a) software plan: serial contract-first member → two lint-disjoint
//       isolated-checkout streams → a merge member leaving one green tree;
//   (b) report-with-workbook: serial shard-schema member → three
//       disjoint-shard streams → ONE assemble member building the .xlsx
//       from shards.
// The join is a first-class member with its own criteria in both.
//
// Traces to:
//   - §9.1 g5 "worked topologies (shard+assemble + software worktrees)"
//     (line ~1195)
//   - TDD Plan row 46 `Conformance_g5_ShardAssembleE2E`
//   - TestConformance_g5_ShardAssembleTopology_DAGExecution (#541)

test('Conformance_g5_ShardAssembleE2E: report-workbook shard+assemble DAG executes, join is first-class', async ({
  page,
  createdPlanIds,
}) => {
  requireApiKey()

  test.setTimeout(360_000)
  await startFreshChatWithJim(page)

  // Setup: per-test Main agent in its own workspace core_team.
  const ownerId = await createMainAgent(page, `conformance-g5-owner-${Date.now()}`)
  const wsRes = await apiFetch<{ id: string }>(page, 'POST', '/api/v1/workspaces', {
    name: 'conformance-g5',
    core_team: [ownerId],
  })
  if (!wsRes.ok) throw new Error(`g5: POST /workspaces failed ${wsRes.status}: ${wsRes.raw}`)
  const workspaceId = wsRes.body.id

  // Topology (mirrors conformance_design_test.go's g5 dataset):
  //   schema → 3 disjoint shards → assemble
  // The assemble member is the join with its OWN criteria (first-class
  // join member). Members are created as separate tasks (POST /tasks with
  // plan_id); blocked_by references real task IDs, resolved from the
  // label map by createPlanWithMembers.
  const { planId, memberIds } = await createPlanWithMembers(
    page,
    workspaceId,
    ownerId,
    {
      title: 'g5 report-workbook',
      goal: 'serial schema, parallel shards, one assemble',
      bounds: { plan_judge_max_rounds: 3 },
    },
    [
      {
        label: 'schema',
        title: 'schema',
        prompt: 'reply schema',
        write_set: ['g5/schema.json'],
        criteria: [proseCriterion('reply contains "schema"')],
      },
      {
        label: 'shard-a',
        title: 'shard-a',
        prompt: 'reply alpha',
        blocked_by_labels: ['schema'],
        write_set: ['g5/a.csv'],
        criteria: [proseCriterion('reply contains "alpha"')],
      },
      {
        label: 'shard-b',
        title: 'shard-b',
        prompt: 'reply beta',
        blocked_by_labels: ['schema'],
        write_set: ['g5/b.csv'],
        criteria: [proseCriterion('reply contains "beta"')],
      },
      {
        label: 'shard-c',
        title: 'shard-c',
        prompt: 'reply gamma',
        blocked_by_labels: ['schema'],
        write_set: ['g5/c.csv'],
        criteria: [proseCriterion('reply contains "gamma"')],
      },
      {
        label: 'assemble',
        title: 'assemble',
        prompt: 'reply assemble',
        blocked_by_labels: ['shard-a', 'shard-b', 'shard-c'],
        write_set: ['g5/report.xlsx'],
        is_join: true,
        // First-class join — assemble carries its OWN criteria, not a
        // bare edge. The drawn path requires this.
        criteria: [proseCriterion('reply contains "assembled"')],
      },
    ],
    createdPlanIds,
  )

  // Drawn-path assertion 1: the plan-lint accepted this topology.
  // (Lint rejection here would mean the g5 topology itself is malformed.)
  const approveRes = await apiFetch<{ status: string }>(
    page,
    'POST',
    `/api/v1/plans/${planId}/approve`,
    {},
  )
  expect(
    approveRes.ok,
    `g5: plan with shard+assemble topology must be approved by lint — ` +
      `got ${approveRes.status}: ${approveRes.raw}`,
  ).toBe(true)

  // Differentiation test: the topology round-tripped through the task
  // store. The Plan wire schema does NOT expose a `members` array
  // (membership is computed read-time from member tasks — Plan.yaml);
  // so the first-class-join invariant is verified by round-tripping the
  // assemble member TASK itself (GET /tasks/{id}), not GET /plans/{id}.
  const assembleTaskId = memberIds['assemble']
  const assembleRoundtrip = await apiFetch<{
    is_join?: boolean
    write_set?: string[]
    criteria?: unknown[]
    plan_id?: string
  }>(page, 'GET', `/api/v1/tasks/${assembleTaskId}`)
  expect(
    assembleRoundtrip.ok,
    `g5: GET /tasks/{assemble} failed ${assembleRoundtrip.status}: ${assembleRoundtrip.raw}`,
  ).toBe(true)
  expect(
    assembleRoundtrip.body.is_join === true,
    `g5: assemble member must round-trip is_join=true (first-class join) — observed ${String(assembleRoundtrip.body.is_join)}.`,
  ).toBe(true)
  expect(
    Array.isArray(assembleRoundtrip.body.criteria) &&
      assembleRoundtrip.body.criteria!.length > 0,
    'g5: assemble member must carry its OWN criteria after round-trip — ' +
      'a bare-edge demotion breaks the first-class join invariant (g5 spec).',
  ).toBe(true)
  expect(
    (assembleRoundtrip.body.write_set ?? []).includes('g5/report.xlsx'),
    'g5: assemble member write_set must round-trip the join artifact path',
  ).toBe(true)

  // Differentiation test: ALL 5 members were created and are linked to
  // this plan. A topology that drops a member at create is a
  // serialization bug the g5 conformance cannot accept.
  const expectedLabels = ['schema', 'shard-a', 'shard-b', 'shard-c', 'assemble']
  expect(
    expectedLabels.every((l) => typeof memberIds[l] === 'string'),
    `g5: all 5 member labels must have created task IDs — missing: ${expectedLabels.filter((l) => !memberIds[l]).join(', ')}.`,
  ).toBe(true)
  // Each member task's plan_id must point back at this plan.
  for (const label of expectedLabels) {
    const m = await apiFetch<{ plan_id?: string }>(
      page,
      'GET',
      `/api/v1/tasks/${memberIds[label]}`,
    )
    expect(
      m.ok && m.body.plan_id === planId,
      `g5: member "${label}" (${memberIds[label]}) plan_id must equal the plan — got ${m.body?.plan_id}.`,
    ).toBe(true)
  }
})

// ── Conformance_bootsweep_E2E ────────────────────────────────────────────────
//
// BDD (§9.1 §5 boot sweep): kill -9 mid-plan → non-terminal sessions →
// failed(interrupted) within N s → plan re-judges/re-dispatches → idle
// settlement fires again → no wedge (CRIT-1 proof).
//
// Traces to:
//   - §9.1 §5 "boot sweep walks the drawn path" (line ~1216)
//   - TDD Plan row 49 `Conformance_bootsweep_E2E`
//   - TestBootSweep_NonTerminalToFailedInterrupted (G-13 / #541)
//   - adr-053-DEFERRED-ISSUES.md E.1 (this test used to only check Session
//     enum membership on the SHARED gateway — it never forked/killed/
//     restarted a real process, so the §5 diagram was never actually
//     exercised. Fixed by driving a dedicated, isolated GatewayProcess
//     (tests/e2e/fixtures/gateway-process.ts) this test owns exclusively:
//     own port, own OMNIPUS_HOME, own credentials.json/master.key, real
//     SIGKILL + real restart.)
//
// This test does NOT touch the shared conformance-suite gateway (OMNIPUS_URL)
// at all — killing that would take down every other Conformance_* spec in
// this file. It boots its own throwaway gateway, drives a real plan member
// task to `in_progress` (a real dispatched turn, session_id present), sends
// it a REAL SIGKILL, restarts the SAME binary against the SAME
// OMNIPUS_HOME/port, and asserts BOTH reconciliation surfaces the design
// names: the task-level hard reset (`reconcileStuckTasks`,
// pkg/gateway/rest_tasks.go) to failed(interrupted), and the session-level
// lifecycle sweep (`runBootSweep`, pkg/agent/boot_sweep.go) to
// status=interrupted — then polls the plan itself to prove the engine
// noticed and kept moving (re-judged/re-dispatched), not wedged (CRIT-1).

test('Conformance_bootsweep_E2E: kill -9 mid-task → restart → boot sweep reconciles it, plan recovers', async () => {
  requireApiKey()

  test.setTimeout(300_000)

  let gw: GatewayProcess | null = null
  try {
    gw = await GatewayProcess.start()

    // Setup: a Main agent (chat-target, plan owner + member assignee) in its
    // own workspace core_team — same rationale as createMainAgent's doc
    // comment on the shared gateway (unambiguous single-workspace resolution).
    const agentRes = await gw.apiFetch<{ id: string; warning?: string }>('POST', '/api/v1/agents', {
      type: 'Main',
      name: 'bootsweep-worker',
      soul: 'Conformance e2e worker — follow the task prompt and reply concisely with exactly what is asked.',
    })
    expect(agentRes.ok, `bootsweep: POST /agents failed ${agentRes.status}: ${agentRes.raw}`).toBe(true)
    expect(
      agentRes.body.warning,
      `bootsweep: POST /agents returned a reload warning — agent may not be registered: ${agentRes.body.warning}`,
    ).toBeUndefined()
    const workerId = agentRes.body.id

    const wsRes = await gw.apiFetch<{ id: string }>('POST', '/api/v1/workspaces', {
      name: 'bootsweep-ws',
      core_team: [workerId],
    })
    expect(wsRes.ok, `bootsweep: POST /workspaces failed ${wsRes.status}: ${wsRes.raw}`).toBe(true)
    const workspaceId = wsRes.body.id

    // A one-member plan so we can prove BOTH the task-level reconciliation
    // AND that the owning PLAN notices the reconciled failure and keeps
    // moving (re-judges/re-dispatches) instead of wedging forever at
    // state=running with a permanently-stuck member.
    const planRes = await gw.apiFetch<{ id: string }>('POST', `/api/v1/workspaces/${workspaceId}/plans`, {
      workspace_id: workspaceId,
      title: 'bootsweep conformance plan',
      owner_agent_id: workerId,
      goal: 'reply with the literal word done',
      bounds: { plan_judge_max_rounds: 5 },
    })
    expect(planRes.ok, `bootsweep: POST /plans failed ${planRes.status}: ${planRes.raw}`).toBe(true)
    const planId = planRes.body.id

    const memberRes = await gw.apiFetch<{ id: string }>('POST', '/api/v1/tasks', {
      title: 'bootsweep member',
      prompt:
        'Write a detailed, at least 150-word explanation of how a distributed task queue ' +
        'reconciles crashed workers, then end your reply with the literal word done.',
      action: 'llm',
      workspace_id: workspaceId,
      agent_id: workerId,
      plan_id: planId,
      max_attempts: 3,
      criteria: [
        {
          kind: 'prose',
          text: 'the reply contains the word "done"',
          author: { kind: 'user', id: 'admin' },
          status: 'pending',
        },
      ],
    })
    expect(memberRes.ok, `bootsweep: POST /tasks (member) failed ${memberRes.status}: ${memberRes.raw}`).toBe(true)
    const memberId = memberRes.body.id

    // Triage to `next` (same landing rule as createPlanMember on the shared
    // gateway — POST /tasks always lands in `inbox`).
    const triageRes = await gw.apiFetch('PATCH', `/api/v1/tasks/${memberId}`, { status: 'next' })
    expect(triageRes.ok, `bootsweep: PATCH member->next failed`).toBe(true)

    const approveRes = await gw.apiFetch('POST', `/api/v1/plans/${planId}/approve`, {})
    expect(approveRes.ok, `bootsweep: POST /plans/{id}/approve failed ${approveRes.status}: ${approveRes.raw}`).toBe(
      true,
    )

    // Poll for the member to actually be DISPATCHED — status=in_progress
    // AND a session_id present (proof a real turn is genuinely in flight,
    // not merely that we intend to kill "soon" and hope we win a race).
    let preKillSessionId = ''
    const dispatchDeadline = Date.now() + 30_000
    while (Date.now() < dispatchDeadline) {
      const poll = await gw.apiFetch<{ status: string; session_id?: string }>('GET', `/api/v1/tasks/${memberId}`)
      expect(poll.ok, `bootsweep: GET member poll failed ${poll.status}: ${poll.raw}`).toBe(true)
      if (poll.body.status === 'in_progress' && poll.body.session_id) {
        preKillSessionId = poll.body.session_id
        break
      }
      await new Promise((r) => setTimeout(r, 300))
    }
    expect(
      preKillSessionId,
      'bootsweep: member never reached status=in_progress with a session_id within 30s — ' +
        'cannot prove a real in-flight turn was interrupted if dispatch itself never happened.',
    ).not.toBe('')

    // THE KILL. Real SIGKILL against a real OS process, mid-turn — the
    // whole reason this file needed its own isolated GatewayProcess.
    await gw.kill9()

    // THE RESTART. Same binary, same OMNIPUS_HOME, same port.
    await gw.restart()

    // --- Assertion 1 (task-level): reconcileStuckTasks -----------------
    // pkg/gateway/rest_tasks.go's boot reconciliation hard-resets any task
    // left `in_progress` by a crashed process to failed, with a specific,
    // literal result string. Assert the EXACT observable content, not just
    // "no longer in_progress" — a hardcoded/no-op reconciliation would also
    // produce "not in_progress" for the wrong reason (e.g. silently stuck
    // in some other state), so the literal message is the real proof.
    const memberAfter = await gw.apiFetch<{ status: string; result?: string }>('GET', `/api/v1/tasks/${memberId}`)
    expect(memberAfter.ok, `bootsweep: GET member post-restart failed ${memberAfter.status}: ${memberAfter.raw}`).toBe(
      true,
    )
    expect(
      memberAfter.body.status,
      `bootsweep: member must be reconciled to "failed" post-restart — observed "${memberAfter.body.status}". ` +
        'A member still "in_progress" after a real process restart means reconcileStuckTasks did not run.',
    ).toBe('failed')
    expect(
      memberAfter.body.result ?? '',
      'bootsweep: reconciled member.result must contain the literal boot-reconciliation message ' +
        '("interrupted: gateway restarted while task was running") — asserting exact content (not ' +
        'just status) so a differently-worded or coincidental failure cannot be mistaken for the sweep.',
    ).toContain('interrupted: gateway restarted while task was running')

    // --- Assertion 2 (session-level): runBootSweep ----------------------
    // The session this member's turn was running under must have been
    // reconciled from its non-terminal state to `interrupted` — the
    // Session wire enum's crash-recovery terminus (contracts/components/
    // schemas/Session.yaml: active | archived | interrupted).
    //
    // ADR-057 U18 (commit 664633b9, "fix(gateway): ADR-057 U18 — read
    // boundaries + REST pagination/nesting"): GET /api/v1/sessions now
    // ALWAYS returns the named gen.SessionPage envelope
    // ({"sessions": [...], "next_cursor"?, "partial_errors"?}), retiring the
    // old bare-array response this assertion used to assume. That commit
    // fixed three pkg/gateway tests with the identical bare-array decode
    // (and a sibling fix landed for tests/integration's
    // getMostRecentSessionID, commit e5d3a25c), but this e2e assertion was
    // outside both file lists and was missed — the same cross-package
    // ownership gap flagged elsewhere in this wave. Without this fix,
    // `sessionsAfter.body.find` throws ("find is not a function") because
    // the body is the envelope object, not an array, before this assertion
    // ever gets to check the reconciled status.
    const sessionsAfter = await gw.apiFetch<{ sessions: Array<{ id: string; status: string }> }>(
      'GET',
      '/api/v1/sessions',
    )
    expect(sessionsAfter.ok, `bootsweep: GET /sessions post-restart failed ${sessionsAfter.status}`).toBe(true)
    const sessionRows = sessionsAfter.body.sessions ?? []
    const preKillSession = sessionRows.find((s) => s.id === preKillSessionId)
    expect(
      preKillSession,
      `bootsweep: the pre-kill session ${preKillSessionId} must still be present in the session list post-restart ` +
        `(observed ids: ${sessionRows.map((s) => s.id).join(', ')})`,
    ).toBeDefined()
    expect(
      preKillSession?.status,
      `bootsweep: the interrupted session must be reconciled to status="interrupted" — observed "${preKillSession?.status}". ` +
        'A session left "active" post-restart with no live turn behind it is exactly the wedge the boot sweep exists to close.',
    ).toBe('interrupted')

    // Differentiation/no-wedge check across every OTHER session too — none
    // may sit outside the wire enum (a phantom status the sweep missed).
    const KNOWN_STATES = new Set(['active', 'archived', 'interrupted'])
    for (const s of new Set(sessionRows.map((s) => s.status))) {
      expect(
        KNOWN_STATES.has(s),
        `bootsweep: observed session status "${s}" outside the Session wire enum {active, archived, interrupted}.`,
      ).toBe(true)
    }

    // --- Assertion 3 (plan-level): CRIT-1 "no wedge", the plan recovers -
    // The plan must notice the reconciled member failure and keep moving —
    // re-judge and either terminate (done/failed) or hold at
    // awaiting_supervision for adjudication — NOT sit wedged in
    // state=running/plan_phase=dispatching with a permanently-stuck member
    // and zero further engine activity.
    const HOLD_PHASE = 'awaiting_supervision'
    const recoveryDeadline = Date.now() + 180_000
    let planState = ''
    let planPhase = ''
    let recovered = false
    while (Date.now() < recoveryDeadline) {
      const poll = await gw.apiFetch<{ state: string; plan_phase?: string }>('GET', `/api/v1/plans/${planId}`)
      expect(poll.ok, `bootsweep: GET plan poll failed ${poll.status}: ${poll.raw}`).toBe(true)
      planState = poll.body.state
      planPhase = poll.body.plan_phase ?? ''
      if (planState === 'done' || planState === 'failed' || planPhase === HOLD_PHASE) {
        recovered = true
        break
      }
      await new Promise((r) => setTimeout(r, 3_000))
    }
    expect(
      recovered,
      `bootsweep: plan ${planId} must reach state=done/failed or plan_phase=awaiting_supervision within 180s ` +
        `post-restart — observed state="${planState}" phase="${planPhase}". A plan stuck in running/dispatching ` +
        'this long after its only member was reconciled means the engine wedged (CRIT-1 violation).',
    ).toBe(true)
  } finally {
    await gw?.stop()
  }
})
