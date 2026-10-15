// AgentPrivateSecrets.test.tsx — the editor's private secrets list
// (omnipus-system-agent US-7, FR-241, spec §6.3 row "Agent editor private
// secrets list"; K6, K49). Boundary: listSecrets / updateSecretOptions doubled.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
import type { SecretNameEntry } from '@/lib/api/generated/openapi-types'
import { AgentPrivateSecrets } from './AgentPrivateSecrets'

const api = vi.hoisted(() => ({ list: vi.fn(), patch: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  listSecrets: api.list,
  updateSecretOptions: api.patch,
}))

function row(over: Partial<SecretNameEntry>): SecretNameEntry {
  return {
    reference: 'priv_sales_mia_WEATHER_TOKEN.v1', kind: 'agent_private', version: 1,
    recipient: { kind: 'agent_private', lines: ['Sales · Mia · WEATHER_TOKEN'] },
    owner: { workspace_id: 'ws-sales', agent_id: 'mia' }, consumers: [], state: 'selected',
    available_in_channel_turns: false, ...over,
  }
}

function mount(agentId = 'mia') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><AgentPrivateSecrets agentId={agentId} /></QueryClientProvider>)
}

describe('AgentPrivateSecrets', () => {
  beforeEach(() => { api.list.mockReset(); api.patch.mockReset() })

  it('lists only this agent\'s private versions, never another agent\'s or an integration key', async () => {
    api.list.mockResolvedValue([
      row({}),
      row({ reference: 'priv_sales_jim_X.v1', owner: { workspace_id: 'ws-sales', agent_id: 'jim' } }),
      row({ reference: 'openai_key.v1', kind: 'provider_key', owner: undefined }),
    ])
    mount()
    expect(await screen.findByTestId('private-secret-priv_sales_mia_WEATHER_TOKEN.v1')).toHaveTextContent('In use')
    expect(screen.queryByTestId('private-secret-priv_sales_jim_X.v1')).toBeNull()
    expect(screen.queryByTestId('private-secret-openai_key.v1')).toBeNull()
  })

  it('shows "No private secrets" when there are none, and the error with Retry when loading fails', async () => {
    api.list.mockResolvedValueOnce([])
    const first = mount()
    expect(await screen.findByTestId('agent-private-secrets-empty')).toHaveTextContent('No private secrets')
    first.unmount()
    api.list.mockRejectedValueOnce(new ApiError(500))
    mount()
    expect(await screen.findByRole('alert')).toHaveTextContent(/Could not load/)
  })

  it('the channel switch writes the option, and an unknown outcome says "not confirmed" with Retry', async () => {
    api.list.mockResolvedValue([row({})])
    api.patch.mockRejectedValueOnce(new ApiError(0)).mockResolvedValueOnce(row({ available_in_channel_turns: true }))
    mount()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('checkbox', { name: 'Available in channel conversations' }))
    expect(api.patch).toHaveBeenCalledWith('priv_sales_mia_WEATHER_TOKEN.v1', { available_in_channel_turns: true })
    expect(await screen.findByText('The change was not confirmed.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(api.patch).toHaveBeenCalledTimes(2))
    expect(api.patch).toHaveBeenLastCalledWith('priv_sales_mia_WEATHER_TOKEN.v1', { available_in_channel_turns: true })
  })
})
