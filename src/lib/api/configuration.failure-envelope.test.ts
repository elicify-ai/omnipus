import { afterEach, describe, expect, it, vi } from 'vitest'
import { z } from 'zod'
import { requestConfiguration } from './configuration'

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

// Contract-derived HTTP failure envelopes: absent revision is meaningful;
// changed_fields remains an array even when storage failed before the first write.
describe('configuration storage failure envelopes', () => {
  it.each([
    { persistence_status: 'none', activation_status: 'not_attempted', changed_fields: [], error_stage: 'stage_entity', message: 'Storage failed.' },
    { persistence_status: 'none', activation_status: 'not_attempted', changed_fields: [], error_stage: 'stage_soul', message: 'Storage failed.' },
    { revision: 'a'.repeat(64), persistence_status: 'none', activation_status: 'not_attempted', changed_fields: [], error_stage: 'stage_entity', message: 'Storage failed.' },
    { revision: 'a'.repeat(64), persistence_status: 'partial', activation_status: 'not_attempted', changed_fields: [], error_stage: 'delegation', message: 'Storage failed.' },
  ])('preserves structured recovery state for $error_stage/$persistence_status', async (state) => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(state), {
      status: 500, headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)
    vi.spyOn(document, 'cookie', 'get').mockReturnValue('__Host-csrf=test')
    await expect(requestConfiguration('/api/v1/agents/new-agent', { method: 'POST' }, z.unknown())).rejects.toMatchObject({
      name: 'ConfigurationSaveError',
      state,
      message: state.persistence_status === 'none'
        ? 'Changes were not saved. Reload before trying again.'
        : 'Some changes were saved, but configuration is incomplete. Reload before making further changes.',
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
