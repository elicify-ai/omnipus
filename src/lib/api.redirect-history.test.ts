import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getMessageStatusSuffix } from './truncation'

// Founder ruling 2026-10-07: cold-load (REST) history of a redirected turn
// shows its partial text with NO "(interrupted)" marker; a plain-stopped turn
// keeps it. The redirect instruction is the gateway's stored user entry with
// id "redirect-<uuid>" (pkg/agent/stop_redirect_root.go::continueOrdinaryAfterStop).
const cancelledAssistant = {
  id: 'a-1', agent_id: 'jim', role: 'assistant', content: 'partial text so far',
  timestamp: '2026-10-07T10:00:00Z', status: 'interrupted', truncated: true, truncation_reason: 'cancelled', turn_id: 't1',
}
const userEntry = (id: string) => ({
  id, agent_id: 'jim', role: 'user', content: 'now just say the word mango', timestamp: '2026-10-07T10:00:05Z', status: 'ok',
})

// The role-less turn_canceled entry exactly as pkg/agent/cancel.go::RequestCancel
// writes it (id <sid>_canceled, type turn_canceled, turn_id, no role), and the
// role-less tool_call rows pkg/agent/turn_transcript.go writes. The REST route
// (pkg/gateway/rest_sessions.go::getSessionMessages) returns them raw.
const turnCanceled = {
  id: 'sid-s7_canceled', type: 'turn_canceled', turn_id: 't1', timestamp: '2026-10-07T10:00:01Z',
  canceled_by_user: 'u1', canceled_by_channel: 'web', cancel_method: 'graceful',
}
const toolCall = (id: string) => ({
  id, type: 'tool_call', agent_id: 'jim', turn_id: 't1', timestamp: '2026-10-07T10:00:00Z',
  tool_calls: [{ id, tool: 'web_search', status: 'success', parameters: { q: 'x' } }],
})
const ordinaryUser = userEntry('u-second')

async function load(wire: unknown[]) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(wire), {
    status: 200, headers: { 'Content-Type': 'application/json' },
  })))
  const { fetchSessionMessages } = await import('./api')
  return fetchSessionMessages('sid-s7')
}

const suffix = (m: unknown) => getMessageStatusSuffix(m as never)
const statusOf = (m: unknown) => (m as { status?: string }).status

describe('fetchSessionMessages — redirected vs plain-stopped turn', () => {
  beforeEach(() => { vi.resetModules() })
  afterEach(() => { vi.unstubAllGlobals(); vi.resetModules() })

  it('exact stored order assistant(cancelled) → turn_canceled → redirect-…: no marker, text kept', async () => {
    const msgs = await load([cancelledAssistant, turnCanceled, userEntry('redirect-7f3a')])
    expect(msgs[0].content).toBe('partial text so far')
    expect(suffix(msgs[0]), 'the real partial is cleared').toBeNull()
    expect(statusOf(msgs[0]), 'finalised as an ordinary finished answer').toBe('done')
  })

  it('with tool_call rows between the partial and the redirect: no marker', async () => {
    const msgs = await load([cancelledAssistant, toolCall('tc-1'), turnCanceled, toolCall('tc-2'), userEntry('redirect-7f3a')])
    expect(suffix(msgs[0])).toBeNull()
    expect(statusOf(msgs[0])).toBe('done')
  })

  it('redirect instruction stored before the turn_canceled entry: no marker', async () => {
    const msgs = await load([cancelledAssistant, userEntry('redirect-7f3a'), turnCanceled])
    expect(suffix(msgs[0])).toBeNull()
    expect(statusOf(msgs[0])).toBe('done')
  })

  it('control: assistant → turn_canceled → ordinary user message keeps "(interrupted)"', async () => {
    const msgs = await load([cancelledAssistant, turnCanceled, ordinaryUser])
    expect(suffix(msgs[0])).toBe('(interrupted)')
  })

  it('Stop, then an unrelated user message and answer, then a redirect: the earlier Stop marker stays', async () => {
    const second = { ...cancelledAssistant, id: 'a-2', status: 'ok', truncated: undefined, truncation_reason: undefined, turn_id: 't2' }
    const msgs = await load([cancelledAssistant, turnCanceled, ordinaryUser, second, userEntry('redirect-7f3a')])
    expect(suffix(msgs[0]), 'genuine Stop marker survives').toBe('(interrupted)')
  })

  it('a user message with no reply between the Stop and the redirect stops the walk-back: marker stays', async () => {
    const msgs = await load([cancelledAssistant, turnCanceled, ordinaryUser, userEntry('redirect-7f3a')])
    expect(suffix(msgs[0]), 'the genuine Stop marker survives').toBe('(interrupted)')
    expect(statusOf(msgs[0])).toBe('interrupted')
  })

  it('an output-limit cutoff followed by a redirect keeps its own "(cut off at the output limit)" marker', async () => {
    const cutOff = { ...cancelledAssistant, status: 'ok', truncation_reason: 'max_output_tokens' }
    const msgs = await load([cutOff, userEntry('redirect-7f3a')])
    expect(suffix(msgs[0])).toBe('(cut off at the output limit)')
  })

  // KNOWN LIMIT (no wire field tells these apart): a plain Stop, then LATER a
  // redirect on the now-idle chat with no user message in between, is stored
  // exactly like a redirected turn (same turn_canceled entry, same
  // redirect-<uuid> user entry), so that earlier Stop marker is lost on reload.
  // Pinned so a change is noticed, not endorsed.
  it('KNOWN LIMIT: plain Stop then an idle-chat redirect loses the Stop marker on reload', async () => {
    const msgs = await load([cancelledAssistant, turnCanceled, userEntry('redirect-7f3a')])
    expect(suffix(msgs[0])).toBeNull()
  })

  // KNOWN LIMIT (helper chats): a /stop-redirect on a helper chat stores its
  // instruction as "<sid>-instruction-<uuid>" (pkg/agent/steering.go::
  // appendSteeredInstruction), the SAME id shape a follow-up or resume of a
  // stopped helper gets (ReviveStoppedSession callers: delegate_followup,
  // delegate_respond, steer_completion). Treating it as a redirect would hide a
  // genuine Stop marker, and nothing else in the stored entry tells them apart
  // without a new wire field — so the marker stays after a reload. Pinned.
  it('KNOWN LIMIT: a helper-chat redirect (<sid>-instruction-… id) keeps "(interrupted)" on reload', async () => {
    const msgs = await load([cancelledAssistant, turnCanceled, userEntry('sid-s7-instruction-0a1b')])
    expect(suffix(msgs[0])).toBe('(interrupted)')
  })
})
