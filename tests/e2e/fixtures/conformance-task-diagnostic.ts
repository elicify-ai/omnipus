import { SessionHistoryResponse as PageSchema, Message as MessageSchema, HistoryEntryOrigin } from '../../../src/lib/api/generated/schemas'
import type { Message as WireMessage, SessionHistoryResponse } from '../../../src/lib/api/generated/openapi-types'
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

// Playwright-safe test-local mirror of api/history-validation.ts. All protocol
// predicates, generated schema checks and error shapes below are kept exact.
// Only browser HTTP telemetry is omitted: readableHistory already catches the
// failure and exposes the same fixed unavailable diagnostic, never the error.
class ApiSchemaError extends Error {
  readonly endpoint: string
  readonly zodIssues: Array<{ path: (string | number)[]; message: string }>
  readonly rawBody: unknown

  constructor(endpoint: string, zodIssues: Array<{ path: (string | number)[]; message: string }>, rawBody: unknown) {
    super(`API response schema mismatch for ${endpoint}: ${zodIssues[0]?.message ?? 'unknown'}`)
    this.name = 'ApiSchemaError'
    this.endpoint = endpoint
    this.zodIssues = zodIssues
    this.rawBody = rawBody
  }
}

function historyProtocolError(endpoint: string, path: (string | number)[], raw: unknown): never {
  throw new ApiSchemaError(endpoint, [{ path, message: 'Inconsistent session history response.' }], raw)
}

function historyPosition(raw: unknown, endpoint: string, path: (string | number)[]): string {
  const result = HistoryEntryOrigin.safeParse({ position: raw })
  // A 32-byte base64url digest has two unused low bits in its final character.
  if (!result.success || !/[AEIMQUYcgkosw048]$/.test(result.data.position)) historyProtocolError(endpoint, path, raw)
  return result.data.position
}

/** Public logical rows, not transport frames or an outer wrapper plus children. */
function historyRowWeight(entry: Pick<WireMessage, 'type' | 'role' | 'content' | 'summary' | 'tool_calls'>): number {
  // The replay reducer applies this marker to its existing turn; it never
  // draws an extra bubble for the cancellation metadata source itself.
  if (entry.type === 'turn_canceled') return 0
  const tools = new Set(entry.tool_calls?.map(call => call.id) ?? []).size
  const narration = !!(entry.content || entry.summary) || tools === 0
  return Number(narration) + tools
}

/** Validate metadata atomically while leaving per-item public corruption to the adapter. */
function validateHistoryPage(raw: unknown, sessionId: string, endpoint: string): SessionHistoryResponse {
  if (!raw || typeof raw !== 'object' || !('entries' in raw) || !Array.isArray(raw.entries)) historyProtocolError(endpoint, ['entries'], raw)
  const source = raw as Record<string, unknown> & { entries: unknown[] }
  const parsed = PageSchema.safeParse({ ...source, entries: [] })
  if (!parsed.success) {
    throw new ApiSchemaError(endpoint, parsed.error.issues, raw)
  }
  const page = { ...parsed.data, entries: source.entries as WireMessage[] }
  if (page.session_id !== sessionId) historyProtocolError(endpoint, ['session_id'], raw)
  const positions = source.entries.map((entry, index) => {
    if (!entry || typeof entry !== 'object') historyProtocolError(endpoint, ['entries', index], raw)
    return historyPosition(Reflect.get(entry, 'history_position'), endpoint, ['entries', index, 'history_position'])
  })
  validateHistoryMetadata(page, positions, endpoint, raw)
  // Invalid public items still have positions, but their row weight cannot be
  // reconstructed reliably. Do not turn item degradation into page rejection.
  const countable = page.entries.every(entry => MessageSchema.safeParse(entry).success)
  if (countable && page.entries.reduce((sum, entry) => sum + historyRowWeight(entry), 0) !== page.page_info.row_count) historyProtocolError(endpoint, ['page_info', 'row_count'], raw)
  return page
}

function validateHistoryMetadata(page: Omit<SessionHistoryResponse, 'entries' | 'session_id'>, positions: readonly string[], endpoint: string, raw: unknown = page): void {
  const info = page.page_info
  const fail = (field: string): never => historyProtocolError(endpoint, ['page_info', field], raw)
  if (positions.length > 100 || new Set(positions).size !== positions.length || info.source_group_count !== positions.length) fail('source_group_count')
  if (info.first_position !== positions[0]) fail('first_position')
  if (info.last_position !== positions.at(-1)) fail('last_position')
  if (page.next_before !== (page.has_earlier ? positions[0] : undefined) || (page.has_earlier && !positions.length)) historyProtocolError(endpoint, ['next_before'], raw)
  if (page.next_after !== (page.has_later ? positions.at(-1) : undefined) || (page.has_later && !positions.length)) historyProtocolError(endpoint, ['next_after'], raw)
  if (info.min_rows > info.limit) fail('min_rows')
  if (!positions.length && info.row_count !== 0) fail('row_count')
  if (info.byte_budget_exceeded !== (info.projected_bytes > info.soft_byte_budget)) fail('byte_budget_exceeded')
  if (info.byte_budget_exceeded !== (info.budget_override !== undefined)) fail('budget_override')
  if (info.stop_reason === 'byte_budget' && info.row_count < info.min_rows) fail('stop_reason')
  if (info.stop_reason === 'row_target' && info.row_count < info.limit) fail('stop_reason')
  if (info.stop_reason === 'source_cap' && info.source_group_count !== 100) fail('stop_reason')
  if (info.stop_reason === 'clear' && !info.stopped_at_clear) fail('stop_reason')
}

// Structural parsing and the canonical semantic validator are complementary.
// Missing/unavailable history is never successful empty history.
function readableHistory(detail: ReturnType<typeof SessionDetail.parse>): boolean {
  if (detail.history_state.status !== 'available' ||
      detail.history_state.reason !== undefined || detail.history_state.message !== undefined ||
      detail.messages === undefined || detail.page_info === undefined ||
      typeof detail.has_earlier !== 'boolean' || typeof detail.has_later !== 'boolean') return false
  try {
    validateHistoryPage({
      session_id: detail.session.id, entries: detail.messages,
      page_info: detail.page_info, has_earlier: detail.has_earlier, has_later: detail.has_later,
      ...(detail.next_before !== undefined ? { next_before: detail.next_before } : {}),
      ...(detail.next_after !== undefined ? { next_after: detail.next_after } : {}),
    }, detail.session.id, 'standalone-task failure diagnostic')
    return true
  } catch {
    // The existing safe unavailable projection carries this failure; no raw
    // response, position, query or exception is copied into diagnostics.
    return false
  }
}

export function projectStandaloneTaskDiagnostic(taskRaw: unknown, sessionRaw: unknown): SafeDiagnostic {
  const task = Task.safeParse(taskRaw)
  const detail = SessionDetail.safeParse(sessionRaw)
  if (!task.success || !detail.success) return unavailable()
  const session = detail.data.session
  if (!validToken(task.data.id, UUID) || !validToken(session.id, SESSION_ID) ||
      task.data.session_id !== session.id || session.task_id !== task.data.id) return unavailable()

  const messages = detail.data.messages
  if (!readableHistory(detail.data) || messages === undefined) return unavailable()

  const calls: SafeCall[] = []
  let incomplete = detail.data.has_earlier === true || detail.data.has_later === true
  let successfulClaim = false
  let latestClaim: Classification = 'no_successful_goal_claim'
  for (const message of messages) {
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
