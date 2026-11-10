// secretRequests.test.ts — value-free request state (omnipus-system-agent
// K3, K4, FR-074, FR-079: "absence is never success").
import { describe, it, expect, beforeEach } from 'vitest'
import type { SessionStateSecretRequest } from '@/lib/api/generated/asyncapi-types'
import { cardsForSession, useSecretRequestStore } from './secretRequests'

function entry(id: string, state: SessionStateSecretRequest['state'], updated = '2026-10-14T00:00:00Z'): SessionStateSecretRequest {
  return {
    request_id: id, session_id: 'helper', root_session_id: 'main', state,
    acting_agent_id: 'omnipus', acting_agent_name: 'Omnipus', purpose: 'p',
    recipient: { kind: 'provider_key', lines: ['https://api.openai.com/v1'] },
    fields: [{ key: 'api_key', label: 'API key' }], updated_at: updated,
  }
}

describe('secret request store', () => {
  beforeEach(() => useSecretRequestStore.getState().reset())

  it('a pending request missing from a newer snapshot is checked, never assumed done', () => {
    const s = useSecretRequestStore.getState()
    s.reconcileSession('main', [entry('a', 'pending')])
    s.reconcileSession('main', [])
    const a = useSecretRequestStore.getState().byId.a
    expect(a.request.state).toBe('pending')
    expect(a.checkRequired).toBe(true)
  })

  it('a late duplicate request frame never reopens a terminal request', () => {
    const s = useSecretRequestStore.getState()
    s.reconcileSession('main', [entry('a', 'submitted')])
    s.upsertFromFrame({ type: 'secret_request', ...entry('a', 'pending') } as never)
    expect(useSecretRequestStore.getState().byId.a.request.state).toBe('submitted')
  })

  it('the chat shows its pending cards plus only the newest undismissed result', () => {
    const s = useSecretRequestStore.getState()
    s.reconcileSession('main', [entry('old', 'cancelled', '2026-10-14T00:00:01Z'), entry('new', 'submitted', '2026-10-14T00:00:02Z'), entry('p', 'pending')])
    const ids = cardsForSession(useSecretRequestStore.getState().byId, 'main').map((e) => e.request.request_id)
    expect(ids).toEqual(['new', 'p'])
    s.dismiss('new')
    expect(cardsForSession(useSecretRequestStore.getState().byId, 'main').map((e) => e.request.request_id)).toEqual(['p'])
    expect(cardsForSession(useSecretRequestStore.getState().byId, 'elsewhere')).toEqual([])
  })
})
