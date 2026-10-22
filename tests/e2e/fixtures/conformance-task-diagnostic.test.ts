import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Page, TestInfo } from '@playwright/test'
import {
  attachStandaloneTaskFailureDiagnostic,
  projectStandaloneTaskDiagnostic,
} from './conformance-task-diagnostic'

const SECRET = 'BANNED_PROMPT_ARGUMENT_QUESTION_APPROVAL_RESULT_ERROR'
const TASK = '11111111-1111-4111-8111-111111111111'
const SESSION = 'session_01M5H3PTEC38S8Q1ZK91ETG4ZR'
const TIME = '2026-10-22T06:00:00Z'

function call(tool = 'goal_claim', status = 'success', result: unknown = { text: JSON.stringify({ status: 'met', evidence: SECRET }) }) {
  return {
    id: 'call_1', tool, status, content_state: 'full',
    parameters: { prompt: SECRET, question: SECRET, approval: SECRET, arguments: SECRET },
    result, error: SECRET,
  }
}

function fixture(calls: unknown[] = []) {
  return {
    task: {
      id: TASK, title: SECRET, prompt: SECRET, result: SECRET,
      action: 'llm', status: 'in_progress', workspace_id: 'test-ws',
      session_id: SESSION, owner: SECRET, created_by: SECRET,
      created_at: TIME, updated_at: TIME,
    },
    detail: {
      session: {
        id: SESSION, type: 'task', agent_id: 'worker', title: SECRET,
        task_id: TASK, status: 'active', lifecycle_state: 'working',
        created_at: TIME, updated_at: TIME, channel: 'webchat', partitions: [],
        stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
      },
      has_earlier_messages: false,
      messages: [{
        id: '22222222-2222-4222-8222-222222222222', type: 'tool_call',
        agent_id: 'worker', turn_id: 'worker-turn-1', timestamp: TIME,
        content: SECRET, summary: SECRET, tool_calls: calls,
      }],
    },
  }
}

afterEach(() => { vi.unstubAllGlobals() })

describe('standalone task diagnostic positive allowlist', () => {
  it('emits only the exact safe shape and never serializes forbidden input fields', () => {
    const input = fixture([call()])
    const safe = projectStandaloneTaskDiagnostic(input.task, input.detail)
    expect(safe).toEqual({
      classification: 'successful_goal_claim', taskId: TASK, taskStatus: 'in_progress',
      sessionId: SESSION, sessionStatus: 'active', lifecycleStatus: 'working',
      calls: [{ tool: 'goal_claim', status: 'success', callId: 'call_1', turnId: 'worker-turn-1', classification: 'successful_goal_claim' }],
    })
    expect(JSON.stringify(safe)).not.toContain(SECRET)
    expect(JSON.stringify(safe)).not.toMatch(/"(?:prompt|question|approval|arguments|parameters|result|evidence|content|summary|error|reason|title)"/)
  })

  it('classifies saved successful met claims with absent, empty or whitespace evidence', () => {
    for (const evidence of [undefined, '', ' \t\n']) {
      const input = fixture([call('goal_claim', 'success', { text: JSON.stringify({ status: 'met', evidence }) })])
      expect(projectStandaloneTaskDiagnostic(input.task, input.detail).classification).toBe('met_claim_empty_evidence')
    }
  })

  it('matches Go White_Space rather than JavaScript trim for empty evidence', () => {
    for (const code of [0x0085, 0x00a0, 0x1680, 0x2000, 0x2028, 0x202f, 0x205f, 0x3000]) {
      const input = fixture([call('goal_claim', 'success', { text: JSON.stringify({ status: 'met', evidence: String.fromCodePoint(code) }) })])
      expect(projectStandaloneTaskDiagnostic(input.task, input.detail).classification).toBe('met_claim_empty_evidence')
    }
    for (const code of [0xfeff, 0x200b]) {
      const input = fixture([call('goal_claim', 'success', { text: JSON.stringify({ status: 'met', evidence: String.fromCodePoint(code) }) })])
      expect(projectStandaloneTaskDiagnostic(input.task, input.detail).classification).toBe('successful_goal_claim')
    }
  })

  it('uses the saved successful result, never met/empty arguments or message prose', () => {
    const recorded = call('goal_claim', 'success', { text: JSON.stringify({ status: 'blocked', evidence: SECRET }) })
    const input = fixture([{ ...recorded, parameters: { status: 'met', evidence: '' } }])
    expect(projectStandaloneTaskDiagnostic(input.task, input.detail).classification).toBe('successful_goal_claim')
  })

  it('does not promote an error call to a successful empty-evidence claim', () => {
    const input = fixture([call('goal_claim', 'error', { text: JSON.stringify({ status: 'met', evidence: '' }) })])
    const safe = projectStandaloneTaskDiagnostic(input.task, input.detail)
    expect(safe.classification).toBe('no_successful_goal_claim')
    expect(safe.calls[0].classification).toBe('unknown')
    expect(JSON.stringify(safe)).not.toContain(SECRET)
  })

  it('classifies refusal only from typed denied status, not an error sentence', () => {
    const input = fixture([call('bash', 'denied'), call('read_file', 'error', { text: 'denied ' + SECRET })])
    const safe = projectStandaloneTaskDiagnostic(input.task, input.detail)
    expect(safe.calls.map((item) => item.classification)).toEqual(['refused_tool', 'unknown'])
    expect(safe.calls.map((item) => item.tool)).toEqual(['bash', 'read_file'])
    expect(JSON.stringify(safe)).not.toContain(SECRET)
  })

  it('keeps malformed, capped and invalid successful results unknown', () => {
    for (const bad of [
      call('goal_claim', 'success', { text: SECRET }),
      call('goal_claim', 'success', { text: JSON.stringify({ status: 'met', evidence: {} }) }),
      { ...call(), content_state: 'capped' },
      { ...call(), content_state: 'emptied' },
    ]) {
      const input = fixture([bad])
      expect(projectStandaloneTaskDiagnostic(input.task, input.detail).classification).toBe('unknown')
    }
  })

  it('does not infer no-claim from partial history or calls on a non-tool row', () => {
    const input = fixture()
    input.detail.has_earlier_messages = true
    expect(projectStandaloneTaskDiagnostic(input.task, input.detail).classification).toBe('unknown')
    const wrongRow = fixture([call()])
    wrongRow.detail.messages[0].type = 'message'
    expect(projectStandaloneTaskDiagnostic(wrongRow.task, wrongRow.detail).classification).toBe('unknown')
  })

  it('rejects invalid tool tokens, including a final newline and overlong name', () => {
    for (const tool of ['bash\n', '../' + SECRET, 'x'.repeat(97)]) {
      const input = fixture([call(tool)])
      const safe = projectStandaloneTaskDiagnostic(input.task, input.detail)
      expect(safe.classification).toBe('unknown')
      expect(safe.calls).toEqual([])
      expect(JSON.stringify(safe)).not.toContain(SECRET)
    }
  })

  it('omits invalid call/turn identifiers rather than serializing their content', () => {
    const input = fixture([{ ...call(), id: '../' + SECRET }])
    input.detail.messages[0].turn_id = SECRET + '\n'
    const safe = projectStandaloneTaskDiagnostic(input.task, input.detail)
    expect(safe.classification).toBe('unknown')
    expect(safe.calls[0].callId).toBeNull()
    expect(safe.calls[0].turnId).toBeNull()
    expect(JSON.stringify(safe)).not.toContain(SECRET)
  })

  it('keeps malformed or mismatched generated task/session records unavailable', () => {
    const input = fixture([call()])
    input.detail.session.task_id = '33333333-3333-4333-8333-333333333333'
    for (const safe of [
      projectStandaloneTaskDiagnostic(null, input.detail),
      projectStandaloneTaskDiagnostic(input.task, input.detail),
      projectStandaloneTaskDiagnostic(input.task, { messages: SECRET }),
    ]) {
      expect(safe.classification).toBe('unavailable')
      expect(safe.calls).toEqual([])
      expect(JSON.stringify(safe)).not.toContain(SECRET)
    }
  })

  it('bounds the safe call list without claiming complete history', () => {
    const input = fixture(Array.from({ length: 257 }, () => call('read_file')))
    const safe = projectStandaloneTaskDiagnostic(input.task, input.detail)
    expect(safe.classification).toBe('unknown')
    expect(safe.calls).toHaveLength(256)
    expect(JSON.stringify(safe)).not.toContain(SECRET)
  })
})

describe('diagnostic collection failure remains redacted', () => {
  const page = { context: () => ({ cookies: async () => [{ name: 'omnipus-session', value: SECRET }] }) } as unknown as Page

  it('attaches only the projected snapshot after two successful native reads', async () => {
    const input = fixture([call()])
    const reads: unknown[] = [input.task, input.detail]
    const native = vi.fn(async (_url: unknown, _init: unknown) => ({ ok: true, json: async () => reads.shift() }))
    vi.stubGlobal('fetch', native)
    const bodies: string[] = []
    const info = {
      annotations: [],
      attach: async (_name: string, options: { body: Buffer; contentType: string }) => { bodies.push(options.body.toString('utf8')) },
    } as unknown as TestInfo
    await attachStandaloneTaskFailureDiagnostic(page, info, TASK, 'http://127.0.0.1:6060')
    expect(native).toHaveBeenCalledTimes(2)
    expect(bodies).toHaveLength(1)
    expect(JSON.parse(bodies[0])).toEqual(projectStandaloneTaskDiagnostic(input.task, input.detail))
    expect(JSON.parse(bodies[0]).classification).toBe('successful_goal_claim')
    expect(bodies[0]).not.toContain(SECRET)
  })

  it('attaches only fixed unavailable data when native fetch throws a sensitive error', async () => {
    const native = vi.fn().mockRejectedValue(new Error(SECRET))
    vi.stubGlobal('fetch', native)
    const bodies: string[] = []
    const info = {
      annotations: [],
      attach: async (_name: string, options: { body: Buffer; contentType: string }) => { bodies.push(options.body.toString('utf8')) },
    } as unknown as TestInfo
    await attachStandaloneTaskFailureDiagnostic(page, info, TASK, 'http://127.0.0.1:6060')
    expect(native).toHaveBeenCalledTimes(1)
    expect(bodies).toHaveLength(1)
    expect(JSON.parse(bodies[0])).toEqual({
      classification: 'unavailable', taskId: null, taskStatus: 'unknown',
      sessionId: null, sessionStatus: 'unknown', lifecycleStatus: 'unknown', calls: [],
    })
    expect(bodies[0]).not.toContain(SECRET)
  })

  it('uses only a fixed annotation when attachment itself fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error(SECRET)))
    const annotations: TestInfo['annotations'] = []
    const info = { annotations, attach: async () => { throw new Error(SECRET) } } as unknown as TestInfo
    await attachStandaloneTaskFailureDiagnostic(page, info, TASK, 'http://127.0.0.1:6060')
    expect(annotations).toEqual([{ type: 'conformance-t1-diagnostic', description: 'unavailable' }])
    expect(JSON.stringify(annotations)).not.toContain(SECRET)
  })
})
