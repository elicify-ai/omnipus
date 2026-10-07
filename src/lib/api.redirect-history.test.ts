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

async function load(wire: unknown[]) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(wire), {
    status: 200, headers: { 'Content-Type': 'application/json' },
  })))
  const { fetchSessionMessages } = await import('./api')
  return fetchSessionMessages('sid-s7')
}

describe('fetchSessionMessages — redirected vs plain-stopped turn', () => {
  beforeEach(() => { vi.resetModules() })
  afterEach(() => { vi.unstubAllGlobals(); vi.resetModules() })

  it('a cancelled turn followed by a redirect-… instruction has no marker and keeps its text', async () => {
    const msgs = await load([cancelledAssistant, userEntry('redirect-7f3a')])
    expect(msgs[0].content).toBe('partial text so far')
    expect(getMessageStatusSuffix(msgs[0] as never)).toBeNull()
  })

  it('control: followed by an ordinary user message it keeps "(interrupted)"', async () => {
    const msgs = await load([cancelledAssistant, userEntry('u-second')])
    expect(getMessageStatusSuffix(msgs[0] as never)).toBe('(interrupted)')
  })
})
