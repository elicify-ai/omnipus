// Fix 3 regression: the WS-(re)connect attach path must re-seed the
// header/composer agent from the LAST-ACTIVE agent (active_agent_id), not the
// session's creating agent (agent_id). After a handover (Mia → Jim) a full
// reload must keep the header on Jim.
//
// resolveLastActiveAgentId is the helper the onConnected path uses to pick the
// agent it re-asserts into the session store. This locks its resolution order.

import { describe, it, expect, beforeEach } from 'vitest'
import { resolveLastActiveAgentId } from './OmnipusRuntimeProvider'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import type { Session, SessionDetail } from '@/lib/api'

const SID = 'sess-handover'

function makeSession(over: Partial<Session>): Session {
  return {
    id: SID,
    title: 'T',
    type: 'chat',
    status: 'active',
    created_at: '2026-06-10T10:00:00Z',
    updated_at: '2026-06-10T10:05:00Z',
    ...over,
  } as Session
}

describe('resolveLastActiveAgentId', () => {
  beforeEach(() => {
    queryClient.clear()
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
  })

  it('prefers the live store value over a stale session-detail cache (no live-handover regression)', () => {
    // Live handover already set the store to Jim; a stale cache still shows Mia.
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: 'core' })
    queryClient.setQueryData(['session-detail', SID], {
      session: makeSession({ agent_id: 'mia', active_agent_id: 'mia' }),
      messages: [],
    } satisfies SessionDetail)
    expect(resolveLastActiveAgentId(SID)).toBe('jim')
  })

  it('prefers active_agent_id from the session-detail cache (post-handover) when no live value', () => {
    const detail: SessionDetail = {
      session: makeSession({ agent_id: 'mia', active_agent_id: 'jim' }),
      messages: [],
    }
    queryClient.setQueryData(['session-detail', SID], detail)
    expect(resolveLastActiveAgentId(SID)).toBe('jim')
  })

  it('falls back to agent_id when active_agent_id is absent in session-detail', () => {
    const detail: SessionDetail = {
      session: makeSession({ agent_id: 'mia' }),
      messages: [],
    }
    queryClient.setQueryData(['session-detail', SID], detail)
    expect(resolveLastActiveAgentId(SID)).toBe('mia')
  })

  it('uses the sessions-list cache when no session-detail is cached', () => {
    queryClient.setQueryData(['sessions'], [makeSession({ agent_id: 'mia', active_agent_id: 'jim' })])
    expect(resolveLastActiveAgentId(SID)).toBe('jim')
  })

  it('uses the live store value when set and nothing is cached', () => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: 'core' })
    expect(resolveLastActiveAgentId(SID)).toBe('jim')
  })

  it('returns null when no live value and nothing is cached (caller leaves store untouched)', () => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
    expect(resolveLastActiveAgentId(SID)).toBeNull()
  })
})
