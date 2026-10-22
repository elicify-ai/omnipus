import type { Page, TestInfo } from '@playwright/test'
import { SessionDetail, Task } from '../../../src/lib/api/generated/schemas'
import type {
  SessionDetail as SessionDetailWire,
  Task as TaskWire,
  ToolCall,
} from '../../../src/lib/api/generated/openapi-types'

// The runtime schema defaults content_state but its generated output alias
// permits omission. Derive that read-side optionality from the real wire type.
type ParsedToolCall = Omit<ToolCall, 'content_state'> & Partial<Pick<ToolCall, 'content_state'>>

// not-wire-format: test-only, positively allowlisted failure attachment.
// Never a gateway request/response type. Raw data uses generated schemas only.
type Classification =
  | 'no_successful_goal_claim'
  | 'met_claim_empty_evidence'
  | 'successful_goal_claim'
  | 'refused_tool'
  | 'unknown'
  | 'unavailable'
type SafeCall = {
  tool: string
  status: ToolCall['status']
  callId: string | null
  turnId: string | null
  classification: Classification
}
type SafeDiagnostic = {
  classification: Classification
  taskId: string | null
  taskStatus: TaskWire['status'] | 'unknown'
  sessionId: string | null
  sessionStatus: SessionDetailWire['session']['status'] | 'unknown'
  lifecycleStatus: NonNullable<SessionDetailWire['session']['lifecycle_state']> | 'unknown'
  calls: SafeCall[]
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const SESSION_ID = /^session_[0-9A-HJKMNP-TV-Z]{26}$/
const CALL_ID = /^(?:call_|toolu_)[A-Za-z0-9_-]{1,96}$/
const TURN_ID = /^[A-Za-z0-9_-]{1,96}-turn-[0-9]{1,12}$/
const TOOL_NAME = /^[A-Za-z][A-Za-z0-9_.-]{0,95}$/
const MAX_CALLS = 256
// Go strings.TrimSpace uses unicode.White_Space (not JavaScript trim's set).
// In particular U+0085 is empty evidence, while U+FEFF is not.
const GO_EMPTY_EVIDENCE = /^\p{White_Space}*$/u

function unavailable(): SafeDiagnostic {
  return {
    classification: 'unavailable', taskId: null, taskStatus: 'unknown',
    sessionId: null, sessionStatus: 'unknown', lifecycleStatus: 'unknown', calls: [],
  }
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function validToken(value: string, pattern: RegExp): boolean {
  // JavaScript's $ may match before a final newline; demand the entire match.
  const match = pattern.exec(value)
  return match !== null && match[0] === value
}

function classifyCall(call: ParsedToolCall): Classification {
  if (call.status === 'denied') return 'refused_tool'
  if (call.tool !== 'goal_claim' || call.status !== 'success') return 'unknown'
  if (call.content_state !== 'full') return 'unknown'
  // Match resolveToolClaim's saved-result reader, NOT arguments or prose.
  // Inspect text/evidence only here in memory; neither crosses the projection.
  const text: unknown = call.result?.text
  if (typeof text !== 'string' || text.length > 65_536) return 'unknown'
  let saved: unknown
  try { saved = JSON.parse(text) } catch { return 'unknown' }
  if (!record(saved) || typeof saved.status !== 'string' || !['met', 'blocked', 'waiting_on_user'].includes(saved.status)) {
    return 'unknown'
  }
  if (saved.evidence !== undefined && typeof saved.evidence !== 'string') return 'unknown'
  if (saved.status === 'met' && (saved.evidence === undefined || validToken(saved.evidence, GO_EMPTY_EVIDENCE))) {
    return 'met_claim_empty_evidence'
  }
  return 'successful_goal_claim'
}

export function projectStandaloneTaskDiagnostic(taskRaw: unknown, sessionRaw: unknown): SafeDiagnostic {
  const task = Task.safeParse(taskRaw)
  const detail = SessionDetail.safeParse(sessionRaw)
  if (!task.success || !detail.success) return unavailable()
  const session = detail.data.session
  if (!validToken(task.data.id, UUID) || !validToken(session.id, SESSION_ID) ||
      task.data.session_id !== session.id || session.task_id !== task.data.id) return unavailable()

  const calls: SafeCall[] = []
  let incomplete = detail.data.has_earlier_messages === true
  let successfulClaim = false
  let latestClaim: Classification = 'no_successful_goal_claim'
  for (const message of detail.data.messages) {
    if (message.truncated || message.type === 'compaction' || message.type === 'context_window_notice') incomplete = true
    if (message.type !== 'tool_call') {
      if (message.tool_calls?.length) incomplete = true
      continue
    }
    for (const call of message.tool_calls ?? []) {
      if (!validToken(call.tool, TOOL_NAME)) { incomplete = true; continue }
      const callId = validToken(call.id, UUID) || validToken(call.id, CALL_ID) ? call.id : null
      const turnId = message.turn_id && validToken(message.turn_id, TURN_ID) ? message.turn_id : null
      if (callId === null || (message.turn_id !== undefined && turnId === null)) incomplete = true
      const classification = classifyCall(call)
      if (call.tool === 'goal_claim' && call.status === 'success') {
        successfulClaim = true
        latestClaim = classification
        if (classification === 'unknown') incomplete = true
      }
      // Construct a fresh tiny object: never spread a message, call or result.
      calls.push({ tool: call.tool, status: call.status, callId, turnId, classification })
    }
  }
  if (calls.length > MAX_CALLS) incomplete = true
  return {
    classification: incomplete ? 'unknown' : successfulClaim ? latestClaim : 'no_successful_goal_claim',
    taskId: task.data.id, taskStatus: task.data.status,
    sessionId: session.id, sessionStatus: session.status,
    lifecycleStatus: session.lifecycle_state ?? 'unknown',
    calls: calls.slice(-MAX_CALLS),
  }
}

// Failure-only reads use native fetch, NOT Playwright's APIRequestContext:
// adding the unprojected transcript to the API trace would defeat redaction.
// No filesystem/default-home fallback; malformed/unavailable stays unavailable.
export async function attachStandaloneTaskFailureDiagnostic(
  page: Page, info: TestInfo, taskId: string, baseURL: string,
): Promise<void> {
  let diagnostic: SafeDiagnostic
  try {
    const base = new URL(baseURL)
    if (!['http:', 'https:'].includes(base.protocol) || base.username || base.password || !validToken(taskId, UUID)) {
      throw new Error('diagnostic unavailable')
    }
    const cookies = await page.context().cookies(base.origin)
    const cookie = cookies.map((item) => `${item.name}=${item.value}`).join('; ')
    // Bounds only diagnostic I/O AFTER the original assertion has failed.
    // It changes none of the test's original polling/test/assertion budgets.
    const signal = AbortSignal.timeout(5_000)
    const read = async (path: string): Promise<unknown> => {
      const response = await fetch(new URL(path, base.origin), {
        headers: { Cookie: cookie }, redirect: 'error', signal,
      })
      if (!response.ok) throw new Error('diagnostic unavailable')
      return response.json()
    }
    const taskRaw = await read(`/api/v1/tasks/${encodeURIComponent(taskId)}`)
    const task = Task.safeParse(taskRaw)
    if (!task.success || task.data.id !== taskId || !task.data.session_id || !validToken(task.data.session_id, SESSION_ID)) {
      throw new Error('diagnostic unavailable')
    }
    const sessionRaw = await read(`/api/v1/sessions/${encodeURIComponent(task.data.session_id)}`)
    diagnostic = projectStandaloneTaskDiagnostic(taskRaw, sessionRaw)
  } catch {
    // Never serialize a fetch/Zod/JSON error, response body, URL or cookie.
    diagnostic = unavailable()
  }
  try {
    await info.attach('conformance-t1-redacted-diagnostic', {
      body: Buffer.from(JSON.stringify(diagnostic)), contentType: 'application/json',
    })
  } catch {
    // The caller still rethrows the ORIGINAL assertion; attachment failure
    // must neither replace it nor turn it into a pass. Only fixed copy here.
    info.annotations.push({ type: 'conformance-t1-diagnostic', description: 'unavailable' })
  }
}
