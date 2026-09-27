/**
 * Delegation event derivation. Expected lines come from
 * docs/internal/specs/delegation-chat-surface-spec.md, not from reading the
 * implementation back. Polls are not events. A finish happens once. The
 * child's own words never appear on a line. A reload of the same records
 * yields the same lines.
 */

import { describe, expect, it, beforeEach } from 'vitest'
import { act } from 'react'
import type { ToolCall } from '@/lib/api'
import { getMessages, useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
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
                // Real follow_up result is prose, not JSON (pkg/tools/delegate_followup.go::spawnCorrectiveFollowUp).
                result: 'Follow-up dispatched for session child-1 at generation 2 (state: running)',
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

const LONG_TEXT = 'Check the remaining logs and write down every mismatch you find in the archive'

function launched(extra: ToolCall[]): ToolCall[] {
  return [
    tool({
      id: 'bash-run',
      tool: 'bash',
      params: { command: 'npm test', run_in_background: true },
      result: JSON.stringify({ sessionId: 'bash-1', status: 'running' }),
    }),
    ...extra,
  ]
}

describe('background command outcome', () => {
  it('H2: a read that says done without an exit code does not lock a later poll into a clean finish', () => {
    const read = tool({
      id: 'bash-read',
      tool: 'bash',
      params: { action: 'read', session_id: 'bash-1' },
      // executeRead returns sessionId, status, output — never exitCode (pkg/tools/shell_bg.go::executeRead).
      result: JSON.stringify({ sessionId: 'bash-1', status: 'done', output: 'FAIL src/app.test.ts' }),
    })
    const poll = tool({
      id: 'bash-poll',
      tool: 'bash',
      params: { action: 'poll', session_id: 'bash-1' },
      result: JSON.stringify({ sessionId: 'bash-1', status: 'done', exitCode: 1 }),
    })
    expect(read.result).not.toContain('exitCode')
    expect(poll.result).toContain('"exitCode":1')

    const events = deriveDelegationEvents(
      source({ messages: [message({ id: 'm1', toolCalls: launched([read, poll]) })] }),
    )
    expect(kinds(events)).toEqual(['bash_launched', 'bash_failed'])
    expect(events[1].exitCode).toBe(1)
    expect(JSON.stringify(events)).not.toContain('FAIL src/app.test.ts')
  })

  it('H2: ended with no reported exit code is not a clean success', () => {
    const read = tool({
      id: 'bash-read',
      tool: 'bash',
      params: { action: 'read', session_id: 'bash-1' },
      result: JSON.stringify({ sessionId: 'bash-1', status: 'done', output: 'still no exit' }),
    })
    const events = deriveDelegationEvents(
      source({ messages: [message({ id: 'm1', toolCalls: launched([read]) })] }),
    )
    expect(events.some((event) => event.kind === 'bash_launched')).toBe(true)
    expect(events.some((event) => event.kind === 'bash_finished')).toBe(false)
    expect(events.some((event) => event.kind === 'bash_failed')).toBe(false)
  })

  it('D7: killed and canceled are bash_stopped; timeout stays bash_failed', () => {
    const stopped = (status: 'killed' | 'canceled', id: string) =>
      deriveDelegationEvents(
        source({
          messages: [
            message({
              id: 'm1',
              toolCalls: launched([
                tool({
                  id,
                  tool: 'bash',
                  params: { action: status === 'killed' ? 'kill' : 'poll', session_id: 'bash-1' },
                  result: JSON.stringify({ sessionId: 'bash-1', status, exitCode: 137 }),
                }),
              ]),
            }),
          ],
        }),
      )
    expect(kinds(stopped('killed', 'bash-kill'))).toEqual(['bash_launched', 'bash_stopped'])
    expect(stopped('killed', 'bash-kill')[1].exitCode).toBeUndefined()
    expect(kinds(stopped('canceled', 'bash-cancel'))).toEqual(['bash_launched', 'bash_stopped'])

    const timedOut = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: launched([
              tool({
                id: 'bash-timeout',
                tool: 'bash',
                params: { action: 'poll', session_id: 'bash-1' },
                result: JSON.stringify({ sessionId: 'bash-1', status: 'timeout', exitCode: 124 }),
              }),
            ]),
          }),
        ],
      }),
    )
    expect(kinds(timedOut)).toEqual(['bash_launched', 'bash_failed'])
    expect(timedOut[1].exitCode).toBe(124)
  })

  it('M2: a truncated read still yields sessionId and status from its preview', () => {
    const preview = '{"sessionId":"bash-1","status":"killed","output":"' + 'x'.repeat(80)
    expect(() => JSON.parse(preview)).toThrow()
    const events = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: launched([
              tool({
                id: 'bash-read',
                tool: 'bash',
                params: { action: 'read', session_id: 'bash-1' },
                result: { _truncated_client: true, original_size_bytes: 80_000, preview },
              }),
            ]),
          }),
        ],
      }),
    )
    expect(kinds(events)).toEqual(['bash_launched', 'bash_stopped'])
    expect(JSON.stringify(events)).not.toContain('x'.repeat(40))
  })
})

describe('follow-up generations', () => {
  it('M3: the open link uses the session id in the prose, and the title prefers label, then text, then task', () => {
    const prose = 'Follow-up for "Audit the logs" dispatched for session child-new at generation 2 (state: running)'
    expect(prose.startsWith('{')).toBe(false)
    const events = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              runCall(),
              tool({
                id: 'follow-1',
                tool: 'delegate',
                params: {
                  action: 'follow_up',
                  session_id: 'child-1',
                  label: 'Short label',
                  text: LONG_TEXT,
                  task: 'Deprecated alias',
                },
                result: prose,
              }),
            ],
            spans: [span({ status: 'running', lifecycleState: 'running' })],
          }),
        ],
      }),
    )
    const follow = events.find((event) => event.kind === 'follow_up')
    expect(follow).toMatchObject({ title: 'Short label', childSessionId: 'child-new', agentName: 'Ray' })
    expect(JSON.stringify(follow)).not.toContain(LONG_TEXT)

    const fromText = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              runCall(),
              tool({
                id: 'follow-text',
                tool: 'delegate',
                params: { action: 'follow_up', session_id: 'child-1', text: LONG_TEXT, task: 'Deprecated alias' },
                result: 'Follow-up dispatched for session child-1 at generation 2 (state: running)',
              }),
            ],
            spans: [span({ status: 'running' })],
          }),
        ],
      }),
    )
    const textTitle = fromText.find((event) => event.kind === 'follow_up')?.title ?? ''
    expect(textTitle.length).toBe(60)
    expect(LONG_TEXT.startsWith(textTitle)).toBe(true)
    expect(textTitle).not.toBe('Deprecated alias')
  })
})

describe('follow-up generation spans', () => {
  it('D9: run, finish, follow-up, finish is delegated, finished, follow_up, finished — no second delegated', () => {
    const run = runCall()
    const events = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              run,
              tool({
                id: 'follow-1',
                tool: 'delegate',
                params: { action: 'follow_up', session_id: 'child-1', text: 'Look again' },
                result: 'Follow-up dispatched for session child-1 at generation 2 (state: running)',
              }),
            ],
            spans: [
              span({ spanId: 'span_run-1', parentCallId: 'run-1', status: 'success', lifecycleState: 'completed' }),
              span({
                spanId: 'span_run-1_g2',
                parentCallId: 'run-1',
                status: 'success',
                lifecycleState: 'completed',
                taskLabel: 'Look again',
              }),
            ],
          }),
        ],
      }),
    )
    expect(kinds(events)).toEqual(['delegated', 'finished', 'follow_up', 'finished'])
    expect(events.filter((event) => event.kind === 'delegated')).toHaveLength(1)
    expect(events.map((event) => event.id)).toEqual([
      'delegated:span_run-1',
      'finished:span_run-1',
      'follow_up:follow-1',
      'finished:span_run-1_g2',
    ])
  })

  it('gap 2: a cancel suppresses only the generation it hit', () => {
    const events = deriveDelegationEvents(
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
              tool({
                id: 'follow-1',
                tool: 'delegate',
                params: { action: 'follow_up', session_id: 'child-1', text: 'Try once more' },
                result: 'Follow-up dispatched for session child-1 at generation 2 (state: running)',
              }),
            ],
            spans: [
              span({ spanId: 'span_run-1', parentCallId: 'run-1', status: 'cancelled', lifecycleState: 'cancelled' }),
              span({ spanId: 'span_run-1_g2', parentCallId: 'run-1', status: 'error', lifecycleState: 'failed' }),
            ],
          }),
        ],
      }),
    )
    expect(kinds(events)).toEqual(['delegated', 'cancelled', 'follow_up', 'stopped'])
    expect(events.filter((event) => event.kind === 'stopped')).toEqual([
      expect.objectContaining({ id: 'stopped:span_run-1_g2', childSessionId: 'child-1' }),
    ])
  })
})

describe('queued children that never ran', () => {
  it('D10 / gap 6: a parent cancel of a queued child is only cancelled; a system end is nothing', () => {
    const byParent = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              runCall('queued'),
              tool({
                id: 'cancel-q',
                tool: 'delegate',
                params: { action: 'cancel', session_id: 'child-1' },
                result: 'Session child-1 was still queued behind the concurrency limit and had not started; it has been dropped and will never run.',
              }),
            ],
            spans: [span({ status: 'cancelled', lifecycleState: 'cancelled' })],
          }),
        ],
      }),
    )
    expect(kinds(byParent)).toEqual(['cancelled'])

    const bySystem = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [runCall('queued')],
            spans: [span({ status: 'error', lifecycleState: 'failed' })],
          }),
        ],
      }),
    )
    expect(bySystem).toEqual([])

    const timedOut = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [runCall('queued')],
            spans: [span({ status: 'timeout', lifecycleState: 'timed_out' })],
          }),
        ],
      }),
    )
    expect(timedOut).toEqual([])
  })

  it('a queued child that did run still gets started and its own ending', () => {
    // The terminal state frame arrives before subagent_end (steer_audience.go).
    // Final shape is failed, not a leftover `running`. hasRun is the sticky
    // record that a runnable state was reduced earlier — the last state alone
    // is the same shape as a child dropped before it ran.
    const ranThenFailed = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [runCall('queued')],
            spans: [span({ status: 'error', lifecycleState: 'failed', hasRun: true })],
          }),
        ],
      }),
    )
    expect(kinds(ranThenFailed)).toEqual(['started', 'stopped'])

    const ranThenFinished = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [runCall('queued')],
            spans: [span({ status: 'success', lifecycleState: 'completed' })],
          }),
        ],
      }),
    )
    expect(kinds(ranThenFinished)).toEqual(['started', 'finished'])
  })
})

describe('terminal span statuses', () => {
  it('gap 7: parked has no terminal line; timeout and interrupted are stopped', () => {
    const parked = deriveDelegationEvents(
      source({
        messages: [message({ id: 'm1', toolCalls: [runCall()], spans: [span({ status: 'parked', lifecycleState: 'needs_input' })] })],
      }),
    )
    expect(kinds(parked)).toEqual(['delegated'])

    const timedOut = deriveDelegationEvents(
      source({
        messages: [message({ id: 'm1', toolCalls: [runCall()], spans: [span({ status: 'timeout', lifecycleState: 'timed_out' })] })],
      }),
    )
    expect(kinds(timedOut)).toEqual(['delegated', 'stopped'])

    const interrupted = deriveDelegationEvents(
      source({
        messages: [message({ id: 'm1', toolCalls: [runCall()], spans: [span({ status: 'interrupted' })] })],
      }),
    )
    expect(kinds(interrupted)).toEqual(['delegated', 'stopped'])
  })
})

describe('failed parent actions and refusals', () => {
  it('D6: a failed steer, respond, cancel, or follow_up produces no line', () => {
    const failed = (action: string, id: string) =>
      tool({
        id,
        tool: 'delegate',
        status: 'error',
        params: { action, session_id: 'child-1', text: 'nope' },
        result: 'delegate: no such session',
        error: 'delegate: no such session',
      })
    const events = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              runCall(),
              failed('steer', 'steer-bad'),
              failed('respond', 'respond-bad'),
              failed('cancel', 'cancel-bad'),
              failed('follow_up', 'follow-bad'),
            ],
            spans: [span({ status: 'running', lifecycleState: 'running' })],
          }),
        ],
      }),
    )
    expect(kinds(events)).toEqual(['delegated'])
  })

  it('L3 / gap 11: a delegation_denied frame with status error and no reason falls back to call.error', () => {
    // Live wire: IsError → status "error", result is the parsed object (pkg/gateway/websocket_forward_hub.go::hubToolExecEnd).
    const events = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [
              tool({
                id: 'run-denied',
                tool: 'delegate',
                status: 'error',
                params: { action: 'run', agent_id: 'ray' },
                result: { error: 'delegation_denied', policy: 'depth', tool: 'delegate' },
                error: 'delegation denied by policy',
              }),
            ],
          }),
        ],
      }),
    )
    expect(events).toHaveLength(1)
    expect(events[0]).toMatchObject({
      kind: 'refused',
      reason: 'delegation denied by policy',
    })
    expect(events[0].childSessionId).toBeUndefined()
  })
})

const LIVE_SESSION = 'dcs-f1-live'
const REPLAY_SESSION = 'dcs-f1-replay'

function resetDelegationStore(activeSessionId: string) {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useSessionStore.setState({ activeSessionId, activeAgentId: 'ray', activeAgentType: null })
  })
}

function feedDelegationFrames(sessionId: string, finish: boolean) {
  const frame = (body: Record<string, unknown>) => {
    act(() => {
      useChatStore.getState().handleFrame({ session_id: sessionId, ...body } as never)
    })
  }
  frame({ type: 'token', content: 'Delegating.', agent_id: 'ray' })
  frame({
    type: 'tool_call_start',
    call_id: 'run-1',
    tool: 'delegate',
    params: { action: 'run', agent_id: 'ray', label: 'Audit the logs' },
    agent_id: 'ray',
  })
  frame({
    type: 'tool_call_result',
    call_id: 'run-1',
    tool: 'delegate',
    status: 'success',
    result: runResult('running'),
  })
  frame({
    type: 'subagent_start',
    span_id: 'span_run-1',
    parent_call_id: 'run-1',
    task_label: 'Audit the logs',
    agent_id: 'ray',
    child_session_id: 'child-1',
  })
  for (let index = 0; index < 3; index += 1) {
    frame({
      type: 'tool_call_start',
      call_id: `status-${index}`,
      tool: 'delegate',
      params: { action: 'status', session_id: 'child-1' },
      agent_id: 'ray',
    })
    frame({
      type: 'tool_call_result',
      call_id: `status-${index}`,
      tool: 'delegate',
      status: 'success',
      result: `poll saw ${CHILD_SENTINEL}`,
    })
  }
  frame({
    type: 'subagent_state',
    span_id: 'span_run-1',
    state: 'completed',
    created_at: '2026-09-25T12:00:02.000Z',
  })
  frame({
    type: 'subagent_end',
    span_id: 'span_run-1',
    status: 'success',
    duration_ms: 10,
    final_result: CHILD_SENTINEL,
  })
  if (finish) frame({ type: 'done', stats: { tokens: 1, cost: 0, duration_ms: 10 } })
}

function eventsFromStore(sessionId: string) {
  const bucket = useChatStore.getState().sessionsById[sessionId]
  expect(bucket, 'handleFrame must have created the session bucket').toBeDefined()
  const messages = getMessages(bucket!)
  const stored = messages.flatMap((item) => item.spans ?? []).find((item) => item.spanId === 'span_run-1')
  const storedResult = stored && stored.status !== 'running' ? stored.finalResult : undefined
  expect(storedResult, 'the child result must actually be on the span the store kept').toContain(CHILD_SENTINEL)
  return deriveDelegationEvents({
    sessionId,
    messages: messages.map((item) => ({
      id: item.id,
      timestamp: item.timestamp,
      spans: item.spans?.map((itemSpan) => ({
        spanId: itemSpan.spanId,
        parentCallId: itemSpan.parentCallId,
        taskLabel: itemSpan.taskLabel,
        agentId: itemSpan.agentId,
        childSessionId: itemSpan.childSessionId,
        status: itemSpan.status,
        lifecycleState: itemSpan.lifecycleState,
        hasRun: itemSpan.hasRun,
        finalResult: itemSpan.status === 'running' ? undefined : itemSpan.finalResult,
        statusLine: itemSpan.statusLine,
        lastUpdateAt: itemSpan.lastUpdateAt,
      })),
      toolCalls: item.tool_calls as ToolCall[] | undefined,
    })),
    liveToolCalls: bucket!.toolCalls,
    liveToolCallOrder: bucket!.toolCallOrder,
    toolCallOwnerMessageId: bucket!.toolCallOwnerMessageId,
    agentNames: NAMES,
  })
}

function comparableLine(events: { id: string; kind: string; agentName?: string; title?: string; childSessionId?: string }[]) {
  return events.map(({ id, kind, agentName, title, childSessionId }) => ({ id, kind, agentName, title, childSessionId }))
}

describe('live and replay through the store', () => {
  beforeEach(() => resetDelegationStore(LIVE_SESSION))

  it('gap 9: a live turn and the same frames after done derive the same lines, with no child text', () => {
    feedDelegationFrames(LIVE_SESSION, false)
    const live = eventsFromStore(LIVE_SESSION)
    expect(useChatStore.getState().sessionsById[LIVE_SESSION].toolCallOrder.length).toBeGreaterThan(0)

    resetDelegationStore(REPLAY_SESSION)
    feedDelegationFrames(REPLAY_SESSION, true)
    const replayed = eventsFromStore(REPLAY_SESSION)
    expect(useChatStore.getState().sessionsById[REPLAY_SESSION].toolCallOrder).toEqual([])
    expect(getMessages(useChatStore.getState().sessionsById[REPLAY_SESSION]).some((item) => (item.tool_calls?.length ?? 0) > 0)).toBe(true)

    expect(live.length).toBeGreaterThan(0)
    expect(comparableLine(replayed)).toEqual(comparableLine(live))
    expect(kinds(live)).toEqual(['delegated', 'finished'])
    expect(JSON.stringify(live)).not.toContain(CHILD_SENTINEL)
    expect(JSON.stringify(replayed)).not.toContain(CHILD_SENTINEL)
  })
})

const RAN_SESSION = 'dcs-f6-ran'
const DROP_SESSION = 'dcs-f6-drop'

type QueuedChildState = 'queued' | 'running' | 'needs_input' | 'paused' | 'completed' | 'failed' | 'cancelled' | 'timed_out'

/**
 * Queued launch, then the lifecycle frames, then a terminal end.
 * `statesBeforeStart` is the replay-gap order: state frames parked before
 * subagent_start. Normal order matches the transcript (start, then states).
 */
function feedQueuedOutcome(
  sessionId: string,
  states: QueuedChildState[],
  endStatus: 'error' | 'cancelled' | 'timeout',
  finish: boolean,
  statesBeforeStart = false,
) {
  const frame = (body: Record<string, unknown>) => {
    act(() => {
      useChatStore.getState().handleFrame({ session_id: sessionId, ...body } as never)
    })
  }
  const stateFrames = () => {
    for (const state of states) {
      frame({
        type: 'subagent_state',
        span_id: 'span_run-1',
        state,
        created_at: '2026-09-25T12:00:02.000Z',
      })
    }
  }
  frame({ type: 'token', content: 'Delegating.', agent_id: 'ray' })
  frame({
    type: 'tool_call_start',
    call_id: 'run-1',
    tool: 'delegate',
    params: { action: 'run', agent_id: 'ray', label: 'Audit the logs' },
    agent_id: 'ray',
  })
  frame({
    type: 'tool_call_result',
    call_id: 'run-1',
    tool: 'delegate',
    status: 'success',
    result: runResult('queued'),
  })
  if (statesBeforeStart) stateFrames()
  frame({
    type: 'subagent_start',
    span_id: 'span_run-1',
    parent_call_id: 'run-1',
    task_label: 'Audit the logs',
    agent_id: 'ray',
    child_session_id: 'child-1',
  })
  if (!statesBeforeStart) stateFrames()
  frame({
    type: 'subagent_end',
    span_id: 'span_run-1',
    status: endStatus,
    duration_ms: 180000,
    final_result: CHILD_SENTINEL,
  })
  if (finish) frame({ type: 'done', stats: { tokens: 1, cost: 0, duration_ms: 10 } })
}

function linesForQueued(sessionId: string) {
  const bucket = useChatStore.getState().sessionsById[sessionId]
  expect(bucket, 'handleFrame must have created the session bucket').toBeDefined()
  const messages = getMessages(bucket!)
  const stored = messages.flatMap((item) => item.spans ?? []).find((item) => item.spanId === 'span_run-1')
  expect(stored, 'the child span must be on the message, or an empty line list proves nothing').toBeDefined()
  const calls = [
    ...messages.flatMap((item) => item.tool_calls ?? []),
    ...bucket!.toolCallOrder.map((id) => bucket!.toolCalls[id]),
  ]
  const run = calls.find((call) => call?.id === 'run-1')
  expect(typeof run?.result === 'string' ? run.result : '', 'the queued launch must be present').toContain('Queued')
  const events = deriveDelegationEvents({
    sessionId,
    messages: messages.map((item) => ({
      id: item.id,
      timestamp: item.timestamp,
      spans: item.spans?.map((itemSpan) => ({
        spanId: itemSpan.spanId,
        parentCallId: itemSpan.parentCallId,
        taskLabel: itemSpan.taskLabel,
        agentId: itemSpan.agentId,
        childSessionId: itemSpan.childSessionId,
        status: itemSpan.status,
        lifecycleState: itemSpan.lifecycleState,
        hasRun: itemSpan.hasRun,
        finalResult: itemSpan.status === 'running' ? undefined : itemSpan.finalResult,
        statusLine: itemSpan.statusLine,
        lastUpdateAt: itemSpan.lastUpdateAt,
      })),
      toolCalls: item.tool_calls as ToolCall[] | undefined,
    })),
    liveToolCalls: bucket!.toolCalls,
    liveToolCallOrder: bucket!.toolCallOrder,
    toolCallOwnerMessageId: bucket!.toolCallOwnerMessageId,
    agentNames: NAMES,
  })
  return { stored: stored!, events }
}

describe('a queued child that ran is not the same as one that never left the queue', () => {
  it('a: queued, then running, then failed, then an error end is started plus stopped', () => {
    resetDelegationStore(RAN_SESSION)
    feedQueuedOutcome(RAN_SESSION, ['running', 'failed'], 'error', false)
    const { stored, events } = linesForQueued(RAN_SESSION)
    expect(stored.status).toBe('error')
    expect(stored.lifecycleState).toBe('failed')
    expect(stored.hasRun).toBe(true)
    expect(kinds(events)).toEqual(['started', 'stopped'])
    expect(events.map((event) => event.id)).toEqual(['started:span_run-1', 'stopped:span_run-1'])
    expect(JSON.stringify(events)).not.toContain(CHILD_SENTINEL)
  })

  it('needs_input, paused, and completed also stick after a later failure', () => {
    for (const state of ['needs_input', 'paused', 'completed'] as const) {
      resetDelegationStore(RAN_SESSION)
      feedQueuedOutcome(RAN_SESSION, [state, 'failed'], 'error', false)
      const { stored, events } = linesForQueued(RAN_SESSION)
      expect(stored.hasRun, state).toBe(true)
      expect(stored.lifecycleState, state).toBe('failed')
      expect(kinds(events), state).toEqual(['started', 'stopped'])
    }
  })

  it('a runnable state parked before subagent_start is not cleared by the later failure', () => {
    resetDelegationStore(RAN_SESSION)
    feedQueuedOutcome(RAN_SESSION, ['running', 'failed'], 'error', false, true)
    const { stored, events } = linesForQueued(RAN_SESSION)
    expect(stored.lifecycleState).toBe('failed')
    expect(stored.hasRun).toBe(true)
    expect(kinds(events)).toEqual(['started', 'stopped'])
  })

  it('b: queued then failed or cancelled, with no runnable state, is no line', () => {
    resetDelegationStore(DROP_SESSION)
    feedQueuedOutcome(DROP_SESSION, ['queued', 'failed'], 'error', false)
    const failed = linesForQueued(DROP_SESSION)
    expect(failed.stored.status).toBe('error')
    expect(failed.stored.lifecycleState).toBe('failed')
    expect(failed.stored.hasRun).not.toBe(true)
    expect(failed.events).toEqual([])
    // Same records, flag flipped: the empty list is because it never ran,
    // not because the span or the queued launch was missing.
    const couldHave = deriveDelegationEvents(
      source({
        messages: [
          message({
            id: 'm1',
            toolCalls: [runCall('queued')],
            spans: [span({ status: 'error', lifecycleState: 'failed', hasRun: true })],
          }),
        ],
      }),
    )
    expect(kinds(couldHave)).toEqual(['started', 'stopped'])

    resetDelegationStore(DROP_SESSION)
    feedQueuedOutcome(DROP_SESSION, ['cancelled'], 'cancelled', false)
    const cancelled = linesForQueued(DROP_SESSION)
    expect(cancelled.stored.status).toBe('cancelled')
    expect(cancelled.stored.lifecycleState).toBe('cancelled')
    expect(cancelled.stored.hasRun).not.toBe(true)
    expect(cancelled.events).toEqual([])
  })

  it('c: replaying the same frames through the reducer keeps both outcomes', () => {
    resetDelegationStore(RAN_SESSION)
    feedQueuedOutcome(RAN_SESSION, ['running', 'failed'], 'error', false)
    const liveRan = linesForQueued(RAN_SESSION)

    resetDelegationStore(REPLAY_SESSION)
    feedQueuedOutcome(REPLAY_SESSION, ['running', 'failed'], 'error', true)
    const replayRan = linesForQueued(REPLAY_SESSION)
    expect(replayRan.stored.hasRun).toBe(true)
    expect(replayRan.stored.lifecycleState).toBe('failed')
    expect(kinds(replayRan.events)).toEqual(['started', 'stopped'])
    expect(comparableLine(replayRan.events)).toEqual(comparableLine(liveRan.events))
    expect(JSON.stringify(replayRan.events)).not.toContain(CHILD_SENTINEL)

    resetDelegationStore(DROP_SESSION)
    feedQueuedOutcome(DROP_SESSION, ['failed'], 'error', false)
    const liveDrop = linesForQueued(DROP_SESSION)
    expect(liveDrop.stored.hasRun).not.toBe(true)
    expect(liveDrop.events).toEqual([])

    resetDelegationStore(REPLAY_SESSION)
    feedQueuedOutcome(REPLAY_SESSION, ['failed'], 'error', true)
    const replayDrop = linesForQueued(REPLAY_SESSION)
    expect(replayDrop.stored.hasRun).not.toBe(true)
    expect(replayDrop.stored.lifecycleState).toBe('failed')
    expect(replayDrop.events).toEqual([])
  })
})
