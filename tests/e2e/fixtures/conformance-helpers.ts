// Omnipus — shared fixtures/helpers for the ADR-053 §9.1 design-conformance
// E2E suite.
//
// WHY THIS MODULE EXISTS: the suite used to be a single 2,406-line spec file
// (conformance-design-e2e.spec.ts) holding all ten Conformance_* tests plus
// ~495 lines of shared setup. Because playwright.config.ts pins
// `workers: 1, fullyParallel: false` (shared gateway config/credentials cannot
// tolerate concurrent writes), a spec FILE is the smallest unit of CI
// parallelism — so one file meant one 22-minute CI shard that set the whole
// pipeline's floor while eleven other runners idled. The tests were split
// across four sibling spec files (conformance-design-{chat,plan,replan,exec}
// -e2e.spec.ts), one per CI shard, each with its own gateway. Everything they
// share lives HERE, once — copies drift, and the comments below encode real
// debugging history (agent multi-workspace reroot resolution, deny-by-default
// bash policy, the inbox→next triage rule, the terminal-signature granularity).
//
// DO NOT inline any of this back into a spec file, and do not raise `workers`
// in playwright.config.ts — concurrent writes to one gateway's config and
// credentials is a real corruption risk, not a tuning knob. Parallelism across
// shards is safe only because each CI shard runs its own gateway process.
//
// ─────────────────────────────────────────────────────────────────────────────
// The original suite header, verbatim — it describes all four spec files:
// ─────────────────────────────────────────────────────────────────────────────
//
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

import { expect, type Page } from '@playwright/test'
import { chatInput, assistantMessages, selectAgent } from './selectors'

// ── Constants ────────────────────────────────────────────────────────────────

export const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── T0.1: API key gate ──────────────────────────────────────────────────────

export function requireApiKey(): void {
  if (!process.env.OPENROUTER_API_KEY_CI) {
    throw new Error(
      'BLOCKED: OPENROUTER_API_KEY_CI not set. Every conformance e2e in this file ' +
        'requires a real LLM (the spec demands at least one real-LLM run per user-facing ' +
        'flow). See tests/e2e/README.md prerequisites.',
    )
  }
}

// ── Shared helpers (mirror verifier-eval.spec.ts's apiFetch pattern) ────────

export interface ApiResult<T> {
  ok: boolean
  status: number
  body: T
  raw: string
}

export async function getCsrfToken(page: Page): Promise<string> {
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

export async function apiFetch<T = unknown>(
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

export interface Criterion {
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

export interface MemberSpec {
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

export interface PlanFields {
  title: string
  goal?: string
  description?: string
  dod?: Criterion[]
  bounds?: { plan_judge_max_rounds?: number; idle_expiry_days?: number }
}

export function proseCriterion(text: string): Criterion {
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
export function checkCriterion(text: string, command: string, expectedExitCode = 0): Criterion {
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
export async function createMainAgent(
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

export async function createPlan(
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

export async function createPlanMember(
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
export async function createPlanWithMembers(
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
export async function listPlanMemberTasks(
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
export function memberSignature(members: Array<{ id: string; status: string }>): string {
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
export async function getSessionMessages(page: Page, sessionId: string): Promise<Array<Record<string, unknown>>> {
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
export function extractPlanCorrectCalls(
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
export async function startFreshChatWithJim(page: Page): Promise<void> {
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
