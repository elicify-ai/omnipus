/**
 * Delegation event derivation. Expected lines come from
 * docs/internal/specs/delegation-chat-surface-spec.md, not from reading the
 * implementation back. Polls are not events. A finish happens once. The
 * child's own words never appear on a line. A reload of the same records
 * yields the same lines.
 */

import { describe, expect, it } from 'vitest'
import type { ToolCall } from '@/lib/api'
import { deriveDelegationEvents } from './delegationEvents'
import type { DelegationEventSource, DelegationMessageView, DelegationSpanView } from './delegationEvents'

const SESSION = 'parent-1'
const AT = '2026-09-25T12:00:00.000Z'
const CHILD_SENTINEL = 'zz-child-authored-7f3c2a'
const NAMES = { ray: 'Ray' }

function tool(partial: Partial<ToolCall> & Pick<ToolCall, 'id' | 'tool'>): ToolCall {
  return { params: {}, status: 'success', ...partial }
}

function span(partial: Partial<DelegationSpanView> & Pick<DelegationSpanView, 'status'>): DelegationSpanView {
  return {
    spanId: 'span-1',
    parentCallId: 'run-1',
    taskLabel: 'Audit the logs',
    agentId: 'ray',
    childSessionId: 'child-1',
    ...partial,
  }
}

function message(partial: Partial<DelegationMessageView> & Pick<DelegationMessageView, 'id'>): DelegationMessageView {
  return { timestamp: AT, ...partial }
}

function source(partial: Partial<DelegationEventSource> = {}): DelegationEventSource {
  return { sessionId: SESSION, messages: [], agentNames: NAMES, ...partial }
}

function runResult(state: 'running' | 'queued', sessionId = 'child-1'): string {
  const payload = JSON.stringify({
    session_id: sessionId,
    generation: 1,
    is_3p: false,
    state,
    queue_position: state === 'queued' ? 1 : 0,
  })
  return state === 'queued' ? `${payload}\nQueued because the concurrency limit 1 is in use; queue position 1.` : payload
}

function runCall(state: 'running' | 'queued' = 'running', sessionId = 'child-1'): ToolCall {
  return tool({
    id: 'run-1',
    tool: 'delegate',
    params: { action: 'run', agent_id: 'ray', label: 'Audit the logs', task: 'Read every log' },
    result: runResult(state, sessionId),
  })
}

function statusCall(id: string, body: string): ToolCall {
  return tool({
    id,
    tool: 'delegate',
    params: { action: 'status', session_id: 'child-1' },
    result: body,
  })
}

function kinds(events: { kind: string }[]): string[] {
  return events.map((event) => event.kind)
}

describe('polls are not events', () => {
  it('AC-5: status, peek, and inbox produce no event, while a real finish still does', () => {
    const polls = [
      statusCall('status-1', 'still working'),
      tool({ id: 'peek-1', tool: 'delegate', params: { action: 'peek', session_id: 'child-1' }, result: 'peek text' }),
      tool({ id: 'inbox-1', tool: 'delegate', params: { action: 'inbox', session_id: 'child-1' }, result: '{"messages":[]}' }),
      tool({ id: 'ack-1', tool: 'delegate', params: { action: 'inbox_ack', session_id: 'child-1' }, result: 'acked' }),
    ]
    expect(polls.map((call) => call.params.action)).toEqual(['status', 'peek', 'inbox', 'inbox_ack'])

    const onlyPolls = deriveDelegationEvents(
      source({ messages: [message({ id: 'm1', toolCalls: polls })] }),
    )
    expect(onlyPolls).toEqual([])

    const withFinish = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [...polls, runCall()],
            spans: [span({ status: 'success' })],
          }),
        ],
      }),
    )
    expect(withFinish.filter((event) => event.kind === 'finished')).toHaveLength(1)
    expect(kinds(withFinish)).toEqual(['delegated', 'finished'])
  })

  it('AC-6: twelve status calls around one success produce exactly one finished line', () => {
    const polls = Array.from({ length: 12 }, (_, index) =>
      statusCall(
        `status-${index}`,
        JSON.stringify({ state: 'completed', summary: `poll ${index} saw the child finish` }),
      ),
    )
    expect(polls).toHaveLength(12)
    expect(polls.every((call) => call.params.action === 'status')).toBe(true)

    const events = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [runCall(), ...polls],
            spans: [span({ status: 'success', lifecycleState: 'completed' })],
          }),
        ],
      }),
    )

    expect(events.filter((event) => event.kind === 'finished')).toHaveLength(1)
    expect(events.filter((event) => event.id.startsWith('status'))).toHaveLength(0)
    expect(kinds(events)).toEqual(['delegated', 'finished'])
    expect(events[1]).toMatchObject({
      id: 'finished:span-1',
      kind: 'finished',
      sessionId: SESSION,
      anchorMessageId: 'm1',
      agentName: 'Ray',
      title: 'Audit the logs',
      childSessionId: 'child-1',
    })
  })

  it('AC-7: child-authored text is on the child record and on no event field', () => {
    const child = span({
      status: 'success',
      finalResult: `The answer is ${CHILD_SENTINEL}`,
      statusLine: `progress ${CHILD_SENTINEL}`,
    })
    const poll = statusCall('status-1', `result_so_far ${CHILD_SENTINEL}`)
    expect(child.finalResult).toContain(CHILD_SENTINEL)
    expect(child.statusLine).toContain(CHILD_SENTINEL)
    expect(poll.result).toContain(CHILD_SENTINEL)

    const events = deriveDelegationEvents(
      source({ messages: [message({ id: 'm1', toolCalls: [runCall(), poll], spans: [child] })] }),
    )
    expect(events.length).toBeGreaterThan(0)
    for (const event of events) {
      expect(JSON.stringify(event)).not.toContain(CHILD_SENTINEL)
    }
  })

})

describe('refusals', () => {
  it('AC-10: a depth refusal carries the reason and no child session', () => {
    const result = JSON.stringify({
      error: 'delegation_denied',
      reason: 'depth cap reached',
      policy: 'depth',
      tool: 'delegate',
      session_id: 'not-a-child',
      target_agent_id: 'mia',
    })
    const events = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [tool({ id: 'run-denied', tool: 'delegate', status: 'error', params: { action: 'run' }, result })],
          }),
        ],
      }),
    )
    expect(events).toHaveLength(1)
    expect(events[0]).toMatchObject({
      id: 'refused:run-denied',
      kind: 'refused',
      sessionId: SESSION,
      anchorMessageId: 'm1',
      reason: 'depth cap reached',
    })
    expect(events[0].childSessionId).toBeUndefined()
    expect(JSON.stringify(events[0])).not.toContain('not-a-child')
  })

  it('AC-10: an unknown skill uses the tool message as the reason and still has no child', () => {
    const result = JSON.stringify({
      error: 'skill_not_found',
      tool: 'delegate',
      skill: 'missing-skill',
      target_agent_id: 'ray',
      message: 'delegate: requested_skill "missing-skill" does not resolve to any installed skill.',
    })
    const events = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [tool({ id: 'run-skill', tool: 'delegate', status: 'error', params: { action: 'run' }, result })],
          }),
        ],
      }),
    )
    expect(events).toEqual([
      expect.objectContaining({
        kind: 'refused',
        reason: 'delegate: requested_skill "missing-skill" does not resolve to any installed skill.',
      }),
    ])
    expect(events[0].childSessionId).toBeUndefined()
  })

})

describe('child lifecycle', () => {
  it('a queued child produces no line, and one that then starts produces started rather than delegated', () => {
    const queued = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [runCall('queued')],
            spans: [span({ status: 'running', lifecycleState: 'queued' })],
          }),
          message({
            id: 'm2',
            timestamp: '2026-09-25T12:05:00.000Z',
            toolCalls: [tool({ ...runCall(), id: 'run-done' })],
            spans: [span({ spanId: 'span-done', parentCallId: 'run-done', childSessionId: 'child-done', status: 'success' })],
          }),
        ],
      }),
    )
    expect(queued.some((event) => event.childSessionId === 'child-1')).toBe(false)
    expect(queued.some((event) => event.kind === 'finished' && event.childSessionId === 'child-done')).toBe(true)

    const started = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [runCall('queued')],
            spans: [span({ status: 'running', lifecycleState: 'running' })],
          }),
        ],
      }),
    )
    expect(kinds(started)).toEqual(['started'])
    expect(started[0].childSessionId).toBe('child-1')
  })

  it('a child that ends in error is stopped, once, and a parent cancel replaces that line', () => {
    const stopped = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [runCall()],
            spans: [span({ status: 'error', lifecycleState: 'failed' })],
          }),
        ],
      }),
    )
    expect(kinds(stopped)).toEqual(['delegated', 'stopped'])
    expect(stopped[1].title).toBeUndefined()

    const cancelled = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              runCall(),
              tool({
                id: 'cancel-1',
                tool: 'delegate',
                params: { action: 'cancel', session_id: 'child-1' },
                result: 'Session child-1 hard-cancelled immediately.',
              }),
            ],
            spans: [span({ status: 'cancelled', lifecycleState: 'running' })],
          }),
        ],
      }),
    )
    expect(kinds(cancelled)).toEqual(['delegated', 'cancelled'])
    expect(cancelled[1]).toMatchObject({ id: 'cancelled:cancel-1', childSessionId: 'child-1', agentName: 'Ray' })
    expect(cancelled[1].cascadeCount).toBeUndefined()

    const noop = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              runCall(),
              tool({
                id: 'cancel-noop',
                tool: 'delegate',
                params: { action: 'cancel', session_id: 'child-1' },
                result: 'Session child-1 is already terminal (completed) — no action needed.',
              }),
            ],
            spans: [span({ status: 'success' })],
          }),
        ],
      }),
    )
    expect(kinds(noop)).toEqual(['delegated', 'finished'])
  })

})

describe('landed cancel without a stored child session id', () => {
  it('a landed cancel suppresses stopped when the span has no childSessionId and the run result carries the session', () => {
    const legacy = span({ status: 'cancelled', lifecycleState: 'cancelled', childSessionId: undefined })
    expect(legacy.childSessionId).toBeUndefined()
    const run = runCall('running', 'child-legacy')
    expect(run.result).toContain('child-legacy')

    const withoutCancel = deriveDelegationEvents(
      source({ messages: [message({ id: 'm1', toolCalls: [run], spans: [legacy] })] }),
    )
    expect(kinds(withoutCancel)).toEqual(['delegated', 'stopped'])

    const withCancel = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              run,
              tool({
                id: 'cancel-legacy',
                tool: 'delegate',
                params: { action: 'cancel', session_id: 'child-legacy' },
                result: 'Session child-legacy hard-cancelled immediately.',
              }),
            ],
            spans: [legacy],
          }),
        ],
      }),
    )
    expect(kinds(withCancel)).toEqual(['delegated', 'cancelled'])
    expect(withCancel.some((event) => event.kind === 'stopped')).toBe(false)
    expect(withCancel[1]).toMatchObject({ id: 'cancelled:cancel-legacy', childSessionId: 'child-legacy' })
  })

})

describe('parent actions', () => {
  it('steer, respond, and follow-up are events; the steer instruction is not copied onto the line', () => {
    const steerText = `instruction ${CHILD_SENTINEL}`
    const events = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              runCall(),
              tool({
                id: 'steer-1',
                tool: 'delegate',
                params: { action: 'steer', session_id: 'child-1', text: steerText },
                result: 'Steering message queued for session child-1.',
              }),
              tool({
                id: 'respond-1',
                tool: 'delegate',
                params: { action: 'respond', session_id: 'child-1', text: 'yes' },
                result: 'Answer delivered.',
              }),
              tool({
                id: 'follow-1',
                tool: 'delegate',
                params: { action: 'follow_up', session_id: 'child-1', task: 'Check the rest' },
                result: runResult('running'),
              }),
            ],
            spans: [span({ status: 'running', lifecycleState: 'running' })],
          }),
        ],
      }),
    )
    expect(steerText).toContain(CHILD_SENTINEL)
    expect(kinds(events)).toEqual(['delegated', 'steered', 'answered', 'follow_up'])
    expect(JSON.stringify(events)).not.toContain(CHILD_SENTINEL)
    expect(events[3]).toMatchObject({ title: 'Check the rest', childSessionId: 'child-1', agentName: 'Ray' })
  })

})

describe('background commands', () => {
  it('background commands launch once and finish or fail once, and command output stays off the line', () => {
    const output = `stdout ${CHILD_SENTINEL}`
    const polls = Array.from({ length: 10 }, (_, index) =>
      tool({
        id: `poll-${index}`,
        tool: 'bash',
        params: { action: 'poll', session_id: 'bash-1' },
        result: JSON.stringify({ sessionId: 'bash-1', status: 'running', output }),
      }),
    )
    expect(polls).toHaveLength(10)
    expect(output).toContain(CHILD_SENTINEL)

    const finished = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              tool({
                id: 'bash-run',
                tool: 'bash',
                params: { command: 'npm test', run_in_background: true },
                result: JSON.stringify({ sessionId: 'bash-1', status: 'running' }),
              }),
              ...polls,
              tool({
                id: 'bash-done',
                tool: 'bash',
                params: { action: 'poll', session_id: 'bash-1' },
                result: JSON.stringify({ sessionId: 'bash-1', status: 'done', output }),
              }),
            ],
          }),
        ],
      }),
    )
    expect(kinds(finished)).toEqual(['bash_launched', 'bash_finished'])
    expect(finished[0].command).toBe('npm test')
    expect(finished[1].exitCode).toBeUndefined()
    expect(JSON.stringify(finished)).not.toContain(CHILD_SENTINEL)

    const failed = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              tool({
                id: 'bash-run',
                tool: 'bash',
                params: { command: 'npm test', run_in_background: true },
                result: JSON.stringify({ sessionId: 'bash-1', status: 'running' }),
              }),
              tool({
                id: 'bash-fail',
                tool: 'bash',
                params: { action: 'poll', session_id: 'bash-1' },
                result: JSON.stringify({ sessionId: 'bash-1', status: 'done', exitCode: 2, output }),
              }),
            ],
          }),
        ],
      }),
    )
    expect(kinds(failed)).toEqual(['bash_launched', 'bash_failed'])
    expect(failed[1].exitCode).toBe(2)
  })

})

describe('live and rebuilt snapshots', () => {
  it('a live turn and the same records baked after replay derive the same lines', () => {
    const polls = Array.from({ length: 12 }, (_, index) => statusCall(`status-${index}`, `saw ${CHILD_SENTINEL}`))
    const calls = [runCall(), ...polls]
    const liveSpan = span({
      status: 'success',
      lifecycleState: 'completed',
      finalResult: CHILD_SENTINEL,
      lastUpdateAt: '2026-09-25T12:00:01.000Z',
    })
    const rebuiltSpan = { ...liveSpan, lastUpdateAt: '2026-09-25T18:00:00.000Z' }
    expect(liveSpan.finalResult).toContain(CHILD_SENTINEL)
    expect(liveSpan.lastUpdateAt).not.toBe(rebuiltSpan.lastUpdateAt)

    const live = deriveDelegationEvents(
      source({
        messages: [message({ id: 'm1', spans: [liveSpan] })],
        liveToolCalls: Object.fromEntries(calls.map((call) => [call.id, call])),
        liveToolCallOrder: calls.map((call) => call.id),
        toolCallOwnerMessageId: Object.fromEntries(calls.map((call) => [call.id, 'm1'])),
      }),
    )
    const rebuilt = deriveDelegationEvents(
      source({ messages: [message({ id: 'm1', spans: [rebuiltSpan], toolCalls: calls })] }),
    )

    expect(live.length).toBeGreaterThan(0)
    expect(rebuilt).toEqual(live)
    expect(kinds(live)).toEqual(['delegated', 'finished'])
    expect(JSON.stringify(live)).not.toContain(CHILD_SENTINEL)
  })

  it('the same call baked and still live is one refusal, not two', () => {
    const denied = tool({
      id: 'run-denied',
      tool: 'delegate',
      status: 'error',
      params: { action: 'run' },
      result: JSON.stringify({ error: 'delegation_denied', reason: 'not in the trust set', policy: 'trust_set', tool: 'delegate' }),
    })
    const events = deriveDelegationEvents(
      source({
        messages: [message({ id: 'm1', toolCalls: [denied] })],
        liveToolCalls: { 'run-denied': denied },
        liveToolCallOrder: ['run-denied'],
        toolCallOwnerMessageId: { 'run-denied': 'm1' },
      }),
    )
    expect(events).toHaveLength(1)
    expect(events[0].kind).toBe('refused')
  })
})
