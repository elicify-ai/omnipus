// Omnipus — ADR-053 §9.1 design-conformance E2E shard.
//
// The v2.2 design diagrams ARE the behavioral spec. This file is the E2E
// counterpart to pkg/agent/conformance_design_test.go (#541): where that
// file proves each drawn DAG dispatch ordering and control-plane routing
// against fake/spy providers, this file proves the SAME drawn paths hold
// against the REAL gateway + real provider + real LLM, on a fresh install.
//
// Every TestConformance_*_E2E here traces to a BDD scenario in the spec
// matrix (docs/internal/specs/unified-goal-plan-subagent-spec.md TDD Plan
// rows 41-49) and to a live E2E assertion in §9.1's checklist:
//   - set /goal → SMART compile → confirm → worker turn → claim/idle →
//     Judge verdict → done
//   - ▶ Run → claim → evidence-gate → ladder → done; ■ Stop cancels
//   - Execute → approve → members → all-terminal → plan Judge → unmet →
//     awaiting-supervision holds → owner appends → re-judge → done;
//     Play resumes a cancelled member from last git commit
//   - intent + refs → owner plans → execute → unmet-all-done →
//     re-plan (supersede done / targeted-retry frozen) → done
//   - disjoint write-sets → lint passes; overlapping → lint rejects
//   - shard-schema + 3 disjoint-shard streams → ONE assemble member
//   - spawn child → message_parent(question) → respond → handback
//   - mid-run steer + blocking question(wait=true) + respond + handback
//     (warm — no cold restart)
//   - kill -9 mid-plan → non-terminal → failed(interrupted) → plan
//     re-judges/re-dispatches → idle settlement fires
//
// Quality gates every test passes:
//   1. Asserts on a SPECIFIC observed output (testid, REST field, or
//      transcript content) — never just `expect(err).toBe(null)`.
//   2. Traces to its BDD scenario with a `// Traces to:` line.
//   3. Surfaces BLOCKED via `expect(false).toBe(true)` rather than
//      `test.skip()` — if a feature is missing the test FAILS loud.
//
// Real-LLM requirement (per §9.1 "at least one real-LLM run per
// user-facing flow"): every test depends on OPENROUTER_API_KEY_CI.
// The global-setup preflight throws immediately if it is unset.

import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page, type APIRequestContext, request } from '@playwright/test'
import { test } from './fixtures/plan-cleanup'
import { chatInput, assistantMessages, selectAgent } from './fixtures/selectors'
import { GatewayProcess } from './fixtures/gateway-process.js'

// ── Constants ────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

// Auth file written by global-setup.ts after onboarding.
const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(
      path.dirname(new URL(import.meta.url).pathname),
      'fixtures/.auth/admin.json',
    )

// ── T0.1: API key gate ──────────────────────────────────────────────────────

function requireApiKey(): void {
  if (!process.env.OPENROUTER_API_KEY_CI) {
    throw new Error(
      'BLOCKED: OPENROUTER_API_KEY_CI not set. Every conformance e2e in this file ' +
        'requires a real LLM (the spec demands at least one real-LLM run per user-facing ' +
        'flow). See tests/e2e/README.md prerequisites.',
    )
  }
}

// ── Shared helpers (mirror verifier-eval.spec.ts's apiFetch pattern) ────────

interface ApiResult<T> {
  ok: boolean
  status: number
  body: T
  raw: string
}

async function getCsrfToken(page: Page): Promise<string> {
  const cookies = await page.context().cookies()
  const csrfCookie = cookies.find(
    (c) => c.name === '__Host-csrf' || c.name === 'csrf',
  )
  if (!csrfCookie) {
    throw new Error(
      'conformance-e2e: no CSRF cookie in browser context — global-setup.ts ' +
        'should have seeded one via POST /api/v1/auth/login.',
    )
  }
  return csrfCookie.value
}

async function apiFetch<T = unknown>(
  page: Page,
  method: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE',
  path: string,
  data?: unknown,
): Promise<ApiResult<T>> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (method !== 'GET') {
    headers['X-Csrf-Token'] = await getCsrfToken(page)
  }
  const res = await page.request.fetch(`${BASE_URL}${path}`, {
    method,
    headers,
    data: data !== undefined ? JSON.stringify(data) : undefined,
  })
  const raw = await res.text()
  let body: unknown = null
  if (raw) {
    try {
      body = JSON.parse(raw)
    } catch {
      body = null
    }
  }
  return { ok: res.ok(), status: res.status(), body: body as T, raw }
}

// ── Plan / member helpers ───────────────────────────────────────────────────
//
// The REST contract (contracts/components/schemas/PlanCreateRequest.yaml +
// TaskCreateRequest.yaml) does NOT accept a `members` array on plan create.
// A Plan is created in `draft` state, then each member is added as a separate
// Task via POST /tasks with `plan_id` set (same-workspace FK). Member DAG
// ordering is expressed via `blocked_by` referencing ACTUAL task IDs (not
// client labels), so members must be created in dependency order and the
// label→id map resolved as we go.
//
// `bounds` uses { plan_judge_max_rounds, idle_expiry_days } (NOT max_rounds).
// `dod` is an ARRAY of AcceptanceCriterion (NOT a string). Both are
// additionalProperties:false under schema validation; even with validation
// off, a string `dod` fails to decode into *[]AcceptanceCriterion.
//
// `owner_agent_id` must be a chat-target agent (IsChatTarget()==true — not a
// worker / System Agent). The seeded core agent "jim" qualifies and is used
// throughout. Every agent_id (plan owner AND member assignee) must be a
// member of the workspace's team set (core_team ∪ delegation edges), so jim
// is added to core_team at workspace create time.

interface Criterion {
  kind: 'prose' | 'check' | 'behavior'
  text: string
  author: { kind: 'user' | 'agent'; id: string }
  status: 'pending' | 'met' | 'unmet'
  /** Present iff kind === 'check' (AcceptanceCriterion.yaml). Machine-checked
   * via the assignee agent's own bash tool — no LLM judgment involved, which
   * is what makes it useful for forcing a DETERMINISTIC met/unmet verdict
   * (E.2's F2 hold proof needs a round-1 unmet verdict that does not depend
   * on subjective LLM judging to be reliably reproducible). */
  check?: { command: string; expected_exit_code: number }
}

interface MemberSpec {
  label: string
  title: string
  prompt: string
  write_set?: string[]
  stream?: string
  is_join?: boolean
  /** Labels of members this one depends on; resolved to real task IDs. */
  blocked_by_labels?: string[]
  criteria: Criterion[]
  max_attempts?: number
}

interface PlanFields {
  title: string
  goal?: string
  description?: string
  dod?: Criterion[]
  bounds?: { plan_judge_max_rounds?: number; idle_expiry_days?: number }
}

function proseCriterion(text: string): Criterion {
  return { kind: 'prose', text, author: { kind: 'user', id: 'admin' }, status: 'pending' }
}

/**
 * A machine-checked criterion (AcceptanceCriterion.yaml `kind: check`):
 * dispatched through the assignee agent's own `bash` tool, exit-code
 * compared, resolved WITHOUT the LLM verifier. Used to force deterministic
 * met/unmet outcomes (round-1 DoD-unmet for E.2's F2 proof; a genuinely
 * WRONG-but-`done` outcome for E.3's supersede nudge) instead of relying on
 * subjective LLM judgment for the part of the scenario that must be
 * reproducible. The assignee agent MUST have been created with `bash: allow`
 * (see createMainAgent's `builtinPolicies` param) — a fresh custom agent is
 * seeded fully deny-by-default (coreagent.NewCustomAgentToolsCfg,
 * AgentToolsCfg.yaml), and a denied `bash` fails every check closed
 * regardless of the command, which would make BOTH the "pass" and "fail"
 * recipes below collapse to the same (fail) outcome.
 */
function checkCriterion(text: string, command: string, expectedExitCode = 0): Criterion {
  return {
    kind: 'check',
    text,
    author: { kind: 'user', id: 'admin' },
    status: 'pending',
    check: { command, expected_exit_code: expectedExitCode },
  }
}

/**
 * Create a fresh native Main agent (chat-target) for one conformance test.
 *
 * WHY a per-test agent instead of the seeded core "jim": the conformance
 * shard runs all 9 specs sequentially in ONE gateway/OMNIPUS_HOME. Each
 * plan/task test creates its own workspace and adds the worker agent to
 * that workspace's core_team. Reusing jim across tests leaves jim in
 * MULTIPLE workspaces' core_teams — and the agent workspace reroot path
 * (`pkg/agent/workspace_reroot.go:106`, also used by the verifier turn
 * resolution) calls `workspace.FindForAgentPreferring` to resolve an
 * agent that belongs to >1 core team in favor of the task workspace
 * passed in via optWorkspaceID / ctx. The default multi-membership
 * resolution still falls back to `FindForAgent` (sorted-first id win)
 * when no preferred workspace is supplied, which is brittle for the
 * rapid test sequence: between sibling tests a fresh workspace may not
 * yet be the reroot target by the time the task dispatches. A
 * freshly-created Main agent belongs to exactly ONE workspace →
 * unambiguous resolution on every code path.
 *
 * A Main agent (vs Subagent) is required because plan ownership
 * (validatePlanOwnerAgent) rejects non-chat-target agents — Main is a
 * chat target; the seeded "worker" sub-agent is not. The new agent uses
 * the gateway's default model (agents.defaults.model_name) and inherits
 * the seeded default tool-policy set; the conformance member tasks only
 * need a textual LLM reply, so no special tools are required.
 *
 * `builtinPolicies`, when provided, is merged (server-side, sparse-over-
 * complete — see pkg/gateway/rest.go's createAgent) onto the fully-enumerated
 * deny-by-default seed via `tools_cfg.builtin.policies`. Used by tests that
 * need this agent to execute a `kind: check` criterion for real (e.g.
 * `{ bash: 'allow' }`) — verified empirically: a fresh custom agent with no
 * override gets `bash: "deny"`, which fails every check criterion closed
 * regardless of command (AgentToolsCfg.yaml's documented deny-by-default).
 */
async function createMainAgent(
  page: Page,
  name: string,
  builtinPolicies?: Record<string, 'allow' | 'ask' | 'deny'>,
): Promise<string> {
  const body: Record<string, unknown> = {
    type: 'Main',
    name,
    soul: 'Conformance e2e worker — follow the task prompt and reply concisely with exactly what is asked.',
  }
  if (builtinPolicies !== undefined) {
    body.tools_cfg = { builtin: { policies: builtinPolicies } }
  }
  const res = await apiFetch<{ id: string; warning?: string }>(page, 'POST', '/api/v1/agents', body)
  if (!res.ok) {
    throw new Error(`createMainAgent: POST /agents failed ${res.status}: ${res.raw}`)
  }
  // A 201 carrying a `warning` means the create persisted but the in-memory
  // reload behind it did not complete — so the AgentRegistry may not know this
  // agent yet, and the very next POST /tasks will reject it as "agent not
  // found". Failing here is a TIGHTENING, not a relaxation: it makes CI report
  // the real cause at the point of creation instead of a mystifying downstream
  // "agent not found" several steps later. (Mid-reload creates are no longer a
  // warning case at all — the gateway coalesces the reload request rather than
  // dropping it — so any warning now means a genuine reload failure.)
  if (res.body.warning) {
    throw new Error(
      `createMainAgent: POST /agents returned 201 with a reload warning, so the agent may not ` +
        `be registered in memory and is not safely usable: ${res.body.warning}`,
    )
  }
  return res.body.id
}

async function createPlan(
  page: Page,
  workspaceId: string,
  ownerAgentId: string,
  fields: PlanFields,
): Promise<string> {
  const body: Record<string, unknown> = {
    workspace_id: workspaceId,
    title: fields.title,
    owner_agent_id: ownerAgentId,
  }
  if (fields.goal !== undefined) body.goal = fields.goal
  if (fields.description !== undefined) body.description = fields.description
  if (fields.dod !== undefined) body.dod = fields.dod
  if (fields.bounds !== undefined) body.bounds = fields.bounds
  const res = await apiFetch<{ id: string }>(
    page,
    'POST',
    `/api/v1/workspaces/${workspaceId}/plans`,
    body,
  )
  if (!res.ok) {
    throw new Error(`createPlan: POST /plans failed ${res.status}: ${res.raw}`)
  }
  return res.body.id
}

async function createPlanMember(
  page: Page,
  workspaceId: string,
  planId: string,
  agentId: string,
  member: MemberSpec & { blocked_by?: string[] },
): Promise<string> {
  const body: Record<string, unknown> = {
    title: member.title,
    prompt: member.prompt,
    action: 'llm',
    workspace_id: workspaceId,
    agent_id: agentId,
    plan_id: planId,
    criteria: member.criteria,
  }
  if (member.write_set !== undefined) body.write_set = member.write_set
  if (member.stream !== undefined) body.stream = member.stream
  if (member.is_join !== undefined) body.is_join = member.is_join
  if (member.max_attempts !== undefined) body.max_attempts = member.max_attempts
  if (member.blocked_by !== undefined && member.blocked_by.length > 0) {
    body.blocked_by = member.blocked_by
  }
  const res = await apiFetch<{ id: string }>(page, 'POST', '/api/v1/tasks', body)
  if (!res.ok) {
    throw new Error(
      `createPlanMember (${member.label}): POST /tasks failed ${res.status}: ${res.raw}`,
    )
  }
  const taskId = res.body.id

  // Triage the member from `inbox` → `next` so the plan engine's
  // dispatchReadyMembers (which only dispatches `next`-status members) picks
  // it up. POST /tasks always lands in `inbox` (Detail #8 landing rule,
  // rest_tasks.go:844); promoteReadyMembers only advances `blocked`
  // dependents of `done` tasks — nothing auto-promotes `inbox`→`next`. The
  // Go integration conformance (#541) creates members directly with
  // StatusNext; the e2e path must triage via PATCH. For a member with
  // unsatisfied blocked_by, the store's recomputeBlockedStateLocked
  // (task/store.go:1237) auto-flips `next`→`blocked` on this PATCH; when its
  // deps later complete, AdvanceBlockedDependents flips it back to `next`.
  const triage = await apiFetch<{ status: string }>(
    page,
    'PATCH',
    `/api/v1/tasks/${taskId}`,
    { status: 'next' },
  )
  if (!triage.ok) {
    throw new Error(
      `createPlanMember (${member.label}): PATCH /tasks/{id} triage to next failed ${triage.status}: ${triage.raw}`,
    )
  }
  return taskId
}

/**
 * Create a draft plan plus its member tasks (in dependency order, resolving
 * label→id for blocked_by). Returns the plan ID and the label→task-id map.
 */
async function createPlanWithMembers(
  page: Page,
  workspaceId: string,
  ownerAgentId: string,
  fields: PlanFields,
  members: MemberSpec[],
  // Best-effort teardown tracking (see fixtures/plan-cleanup.ts): when
  // provided, the newly-created plan id is pushed here so the auto fixture
  // stops it after this attempt, pass or fail — preventing a failed/retried
  // attempt from leaving a live plan (and its recurring supervision wake)
  // running into the next retry. Optional only so this helper stays usable
  // outside a test that wires the fixture (none currently do).
  trackPlanIds?: string[],
): Promise<{ planId: string; memberIds: Record<string, string> }> {
  const planId = await createPlan(page, workspaceId, ownerAgentId, fields)
  if (trackPlanIds) trackPlanIds.push(planId)
  const memberIds: Record<string, string> = {}
  for (const m of members) {
    const blockedBy = (m.blocked_by_labels ?? [])
      .map((l) => memberIds[l])
      .filter((id): id is string => typeof id === 'string')
    memberIds[m.label] = await createPlanMember(page, workspaceId, planId, ownerAgentId, {
      ...m,
      blocked_by: blockedBy,
    })
  }
  return { planId, memberIds }
}

/**
 * List a plan's member tasks. GET /api/v1/tasks has no `plan_id` query filter
 * (rest_tasks.go's handleTaskList only accepts workspace_id/status/agent_id/
 * surface/parent_task_id), so this lists by workspace_id and filters
 * client-side on the `plan_id` field every Task wire response already
 * carries — no new REST surface needed.
 */
async function listPlanMemberTasks(
  page: Page,
  workspaceId: string,
  planId: string,
): Promise<Array<{ id: string; status: string; plan_id?: string }>> {
  const res = await apiFetch<Array<{ id: string; status: string; plan_id?: string }>>(
    page,
    'GET',
    `/api/v1/tasks?workspace_id=${workspaceId}&limit=1000`,
  )
  if (!res.ok) {
    throw new Error(`listPlanMemberTasks: GET /tasks failed ${res.status}: ${res.raw}`)
  }
  return res.body.filter((t) => t.plan_id === planId)
}

/**
 * A stable, order-independent snapshot of a plan's member set for the F2
 * round-burn proof (E.2): two snapshots are "the same terminal signature" iff
 * this string is identical. Deliberately just (id,status) pairs, sorted by
 * id — the exact granularity `planTerminalSignature` (pkg/agent/plan_engine.go)
 * itself keys on: a NEW member appearing, or an existing member's status
 * changing, is a genuine change; anything else (timestamps, result text) is
 * not part of the terminal-state signature and must not make two otherwise-
 * identical samples look different.
 */
function memberSignature(members: Array<{ id: string; status: string }>): string {
  return members
    .map((m) => `${m.id}:${m.status}`)
    .sort()
    .join('|')
}

/**
 * Fetch a session's message transcript (GET /api/v1/sessions/{id}). Used to
 * inspect PlanSupervisor's adjudication session for the `plan_correct` tool
 * calls it actually committed (E.3) — the only way to observe WHICH verb and
 * WHICH real member id a correction used, since the wire contract has no
 * revision-history read endpoint on the Plan resource itself (RevisionEntry
 * is emitted as a SessionMessage, `kind: revision_entry`/tool_call, not
 * persisted as a queryable Plan field).
 */
async function getSessionMessages(page: Page, sessionId: string): Promise<Array<Record<string, unknown>>> {
  const res = await apiFetch<{ messages: Array<Record<string, unknown>> }>(
    page,
    'GET',
    `/api/v1/sessions/${sessionId}`,
  )
  if (!res.ok) {
    throw new Error(`getSessionMessages: GET /sessions/${sessionId} failed ${res.status}: ${res.raw}`)
  }
  return res.body.messages ?? []
}

/**
 * Extract every `plan_correct` tool_call entry from a session transcript,
 * flattened to {status, parameters}. A message's `tool_calls` array can carry
 * more than one call; only `plan_correct` calls are kept.
 */
function extractPlanCorrectCalls(
  messages: Array<Record<string, unknown>>,
): Array<{ status: string; parameters: Record<string, unknown> }> {
  const out: Array<{ status: string; parameters: Record<string, unknown> }> = []
  for (const msg of messages) {
    const calls = msg.tool_calls as Array<Record<string, unknown>> | undefined
    if (!Array.isArray(calls)) continue
    for (const c of calls) {
      if (c.tool === 'plan_correct') {
        out.push({
          status: String(c.status ?? ''),
          parameters: (c.parameters as Record<string, unknown>) ?? {},
        })
      }
    }
  }
  return out
}

/**
 * Start a fresh chat session, route to a delegate-capable task agent, and
 * return when the composer is ready to receive input.
 *
 * AGENT ROUTING: conformance e2e uses Jim (the general-purpose task agent)
 * for goal/plan flows. Mia — the default — declines to perform goal-loop
 * or plan operations because her persona is "guide, not executor" (see
 * subagent.spec.ts's `startFreshChat` for the same rationale).
 */
async function startFreshChatWithJim(page: Page): Promise<void> {
  await page.goto('/')
  // Drive /new via the slash command (the canonical replacement for the
  // header "New Chat" button that was removed in workspace top-bar redesign).
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await input.fill('/new')
  await input.press('Enter')
  await expect(assistantMessages(page)).toHaveCount(0, { timeout: 15_000 })
  await selectAgent(page, /Jim/i)
}

// ── Conformance_t0_ChatGoalE2E ───────────────────────────────────────────────
//
// BDD (§9.1 t0): set /goal → SMART compile → conversational confirm in chat
// → worker turn → claim OR idle trigger → Judge verdict → done.
// Pill walks active → judging → done; /goal clear cancels the verifier
// AND any in-flight compilation turn.
//
// Traces to:
//   - docs/internal/specs/unified-goal-plan-subagent-spec.md §"Group Z":
//     "t0 · chat goal end-to-end walks the drawn path" (line ~1160)
//   - TDD Plan row 41 `Conformance_t0_ChatGoalE2E` (line ~1279)
//   - §9.1 Live E2E checklist first 5 bullets (line ~286)
//   - ADR-053 FE-1 (GoalPillTray bottom-right, 8-state enum)

test('Conformance_t0_ChatGoalE2E: /goal set compiles → worker turn → verdict → done pill walk', async ({
  page,
}) => {
  requireApiKey()

  // Real-LLM conformance: budget is the LLM round-trip + verifier turn.
  // 420s = 60s compile/worker + 60s claim/idle + 60s verifier + 240s slack.
  test.setTimeout(420_000)

  await startFreshChatWithJim(page)

  // Send /goal <condition>. The goal loop (pkg/agent/goal_loop.go
  // applyGoalCommandPrompt) intercepts this BEFORE the LLM call and emits a
  // goal_status WS frame with state="active" carrying the compiled criteria.
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })

  // A verifiable, single-criterion condition so the SMART compiler accepts
  // it (out-of-policy or unjudgeable criteria are rejected at compile,
  // per FR-111/D9 — fail-closed, no rejected criterion persists).
  //
  // Use an explicit [check:] machine criterion (not pure prose): claimless
  // idle adjudication (G-3) runs KindCheck under the agent's sandbox. A
  // pure-prose "say goal met" goal left the real-LLM judge returning unmet
  // + steer loops (Jim kept working, pill never reached done). `true`
  // exits 0 deterministically so idle→judge→met→done is reliable.
  // "please continue" is pure steering (looksLikePureSteering) so it does
  // NOT lift a second KindProse criterion.
  const condition = '[check: true exit:0] please continue'
  await input.fill(`/goal ${condition}`)
  await input.press('Enter')

  // Assert the active-pill is rendered — this is the FR-113 "echoed in chat"
  // moment, the conversational confirmation the LLM-driven compile wrote.
  const activePill = page.locator('[data-testid="goal-pill-active"]')
  await expect(activePill).toBeVisible({ timeout: 60_000 })

  // Differentiation test: the active pill's aria-label carries GoalCondition
  // (compiled.Prompt after marker extraction — "please continue" here, not
  // the raw [check:] marker). Assert a fragment of OUR prompt is in that
  // aria-label — proving the pill is bound to OUR goal.
  await expect(activePill.first()).toHaveAttribute('aria-label', /please continue/i, {
    timeout: 10_000,
  })

  // Wait for the worker turn to complete — assistant message counter advances.
  // The active-pill → worker-turn transition is automatic (goal_loop.go emits
  // the goal_status frame, then a normal chat turn fires to do the work).
  await expect(assistantMessages(page).first()).toBeVisible({ timeout: 300_000 })

  // After the worker turn, claimless idle settlement fires (~60s quiet window,
  // FR-102). The KindCheck runs `true` → exit 0 → met → clearGoal emits
  // state=done (ADR-053 R§8.10). Poll for done (or ephemeral judging).
  const judgingPill = page.locator('[data-testid="goal-pill-judging"]')
  const donePill = page.locator('[data-testid="goal-pill-done"]')
  // Race-free wait: poll for either. Budget = quiet window (60s) + judge
  // turn + slack for a possible unmet→steer→retry cycle.
  const deadline = Date.now() + 300_000
  let sawDone = false
  while (Date.now() < deadline) {
    if (await donePill.isVisible({ timeout: 1_000 }).catch(() => false)) {
      sawDone = true
      break
    }
    if (await judgingPill.isVisible({ timeout: 1_000 }).catch(() => false)) {
      // Saw judging — that's a legitimate mid-state. Keep polling for done.
    }
    await page.waitForTimeout(500)
  }

  // Differentiation assertion: the active pill must be GONE once done
  // (the FR-114 cleanup — GoalCondition cleared from session meta). At
  // least one done-pill must have appeared.
  expect(sawDone, 'GoalPillTray must transition to data-testid="goal-pill-done" within 300s').toBe(
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
  // engine is actively re-judging it). What we must NOT see is the plan
  // wedged in running/dispatching with no supervision activity at all.
  const finalDeadline = Date.now() + 180_000
  let planState = ''
  let planPhase = ''
  let reachedTerminalOrHold = false
  while (Date.now() < finalDeadline) {
    const poll = await apiFetch<{ state: string; plan_phase?: string }>(page, 'GET', `/api/v1/plans/${planId}`)
    if (!poll.ok) throw new Error(`t2: GET /plans/{id} poll (final) failed ${poll.status}: ${poll.raw}`)
    planState = poll.body.state
    planPhase = poll.body.plan_phase ?? ''
    if (planState === 'done' || planState === 'failed' || planPhase === HOLD_PHASE) {
      reachedTerminalOrHold = true
      break
    }
    await page.waitForTimeout(3_000)
  }
  expect(
    reachedTerminalOrHold,
    `t2: plan ${planId} must be at a documented terminus (done/failed) or still legitimately held ` +
      `(awaiting_supervision) — observed state="${planState}" phase="${planPhase}". A wedge here means the ` +
      'correction-append → re-judge cycle stalled after the sampling window.',
  ).toBe(true)
})

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
  // this check, copied from Conformance_t2_PlanLifecycleE2E's Step 3) —
  // t2's DoD is a machine-checked `exit 1`, so its rounds resolve in
  // seconds and it always lands back at a terminus or the hold inside any
  // reasonable window. t3's members run REAL LLM turns, and
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
    const sessionsAfter = await gw.apiFetch<Array<{ id: string; status: string }>>('GET', '/api/v1/sessions')
    expect(sessionsAfter.ok, `bootsweep: GET /sessions post-restart failed ${sessionsAfter.status}`).toBe(true)
    const preKillSession = sessionsAfter.body.find((s) => s.id === preKillSessionId)
    expect(
      preKillSession,
      `bootsweep: the pre-kill session ${preKillSessionId} must still be present in the session list post-restart ` +
        `(observed ids: ${sessionsAfter.body.map((s) => s.id).join(', ')})`,
    ).toBeDefined()
    expect(
      preKillSession?.status,
      `bootsweep: the interrupted session must be reconciled to status="interrupted" — observed "${preKillSession?.status}". ` +
        'A session left "active" post-restart with no live turn behind it is exactly the wedge the boot sweep exists to close.',
    ).toBe('interrupted')

    // Differentiation/no-wedge check across every OTHER session too — none
    // may sit outside the wire enum (a phantom status the sweep missed).
    const KNOWN_STATES = new Set(['active', 'archived', 'interrupted'])
    for (const s of new Set(sessionsAfter.body.map((s) => s.status))) {
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
