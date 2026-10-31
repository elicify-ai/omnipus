/**
 * T-06 / S-57 / FR-055 / T-6b/T-6c: real Configure client -> panel.
 * The HTTP response is the process edge. configureChannel, request(), generated
 * validation and ApiError are real; no preconstructed Error stands in for a 500.
 * Expected text/codes are the spec's tables, not imported production constants.
 */
import React from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
import {
  ChannelConfigureError as ChannelConfigureErrorSchema,
  ChannelConfigureResponse as ChannelConfigureResponseSchema,
  ChannelOutboundRead as ChannelOutboundReadSchema,
} from '@/lib/api/generated/schemas'

const { addToast, fetchAvailability, enable } = vi.hoisted(() => ({
  addToast: vi.fn(),
  fetchAvailability: vi.fn(),
  enable: vi.fn(),
}))

vi.mock('@/store/ui', () => ({ useUiStore: () => ({ addToast }) }))
vi.mock('@/store/connection', () => {
  const state = { isConnected: false, connection: null }
  const hook = Object.assign(
    (selector: (s: typeof state) => unknown) => selector(state),
    { getState: () => state },
  )
  return { useConnectionStore: hook }
})
vi.mock('@/store/whatsappPairing', () => ({
  useWhatsAppPairingStore: (selector: (s: { byChannel: Record<string, unknown>; clear: () => void }) => unknown) =>
    selector({ byChannel: {}, clear: vi.fn() }),
}))
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    // configureChannel and isApiError deliberately remain real.
    fetchChannelConfig: vi.fn().mockResolvedValue({}),
    fetchChannelRouting: vi.fn().mockResolvedValue({ default_agent_id: '', workspace_id: '', bound: false }),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchWorkspace: vi.fn().mockResolvedValue({}),
    fetchChannelOutbound: fetchAvailability,
    enableChannel: enable,
  }
})
vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_: object, prop: string) =>
      ({ children, ...props }: Record<string, unknown>) =>
        React.createElement(prop as string, props, children as React.ReactNode),
  }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { ChannelConfigPanel } from './ChannelConfigPanel'

const FAILED_TEXT = 'Saved, but applying the change did not complete: scripted disposal failure. Check the connection status before retrying.'
const PARTIAL_ERRORS = [
  {
    code: 'saved_not_refreshed',
    error: 'settings saved, but the running configuration could not be refreshed; the connection was not restarted',
  },
  {
    code: 'credential_saved_not_saved',
    error: 'The credential was saved, but the configuration change was not: nothing else was applied. The connection keeps its previous token until it next restarts; whether that restart picks the new value up depends on the credential reference the saved configuration holds (see below). Save again to retry — a successful re-save is the general recovery.',
  },
  { code: 'could_not_save', error: 'could not save config' },
].map((body) => ChannelConfigureErrorSchema.parse(body))

let status: number
let body: unknown
const fetchMock = vi.fn<typeof fetch>()

beforeEach(() => {
  addToast.mockReset()
  fetchAvailability.mockReset()
  enable.mockReset().mockResolvedValue({ id: 'whatsapp', enabled: true })
  status = 200
  body = ChannelConfigureResponseSchema.parse({ apply_outcome: 'failed', apply_reason: 'scripted disposal failure' })
  document.cookie = 'csrf=t06-synthetic-csrf; path=/'
  fetchMock.mockReset().mockImplementation(async (input, init) => {
    const url = input instanceof Request ? input.url : String(input)
    expect(url).toMatch(/\/api\/v1\/channels\/whatsapp\/configure$/)
    expect(init?.method).toBe('PUT')
    expect(init?.credentials).toBe('include')
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
})

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  client.setQueryData(['channel-config', 'whatsapp'], {})
  client.setQueryData(['channel-routing', 'whatsapp'], { default_agent_id: '', workspace_id: '', bound: false })
  client.setQueryData(['agents'], [])
  client.setQueryData(['workspaces', { status: 'active' }], [])
  render(
    <QueryClientProvider client={client}>
      <ChannelConfigPanel channelId="whatsapp" channelName="WhatsApp" enabled={false} open={true} onOpenChange={vi.fn()} />
    </QueryClientProvider>,
  )
  return client
}

async function save(label: string) {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: label }))
  })
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
}

function expectNoEnableOrSuccess() {
  expect(enable).not.toHaveBeenCalled()
  for (const [toast] of addToast.mock.calls) {
    expect(toast.variant).not.toBe('success')
  }
}

describe.each(['Save', 'Save & Enable'])('T-06 real Configure wire path — %s', (label) => {
  it('failed adds the observed availability only after a successful re-read', async () => {
    fetchAvailability.mockResolvedValue(ChannelOutboundReadSchema.parse({
      outbound: { policy: 'reply_only', targets: [] },
      revision: 't06-synthetic-revision',
      availability: { state: 'running' },
      start_supported: false,
    }))
    renderPanel()
    await save(label)
    await waitFor(() => expect(addToast.mock.calls).toEqual([
      [{ message: FAILED_TEXT, variant: 'error' }],
      [{ message: 'The connection is currently: running.', variant: 'default' }],
    ]))
    expect(fetchAvailability.mock.calls).toEqual([['whatsapp']])
    expectNoEnableOrSuccess()
    expect(document.body.textContent).not.toMatch(/keeps running with its old settings|restarted with the new settings/)
  })

  it('failed availability re-read produces no extra toast or invented state', async () => {
    fetchAvailability.mockRejectedValue(new Error('synthetic read unavailable'))
    renderPanel()
    await save(label)
    await waitFor(() => expect(fetchAvailability.mock.calls).toEqual([['whatsapp']]))
    await act(async () => {})
    expect(addToast.mock.calls).toEqual([[{ message: FAILED_TEXT, variant: 'error' }]])
    expectNoEnableOrSuccess()
  })

  it.each(PARTIAL_ERRORS)('raw 500 $code retains the code and shows the exact server text', async (errorBody) => {
    status = 500
    body = errorBody
    const client = renderPanel()
    await save(label)
    await waitFor(() => expect(addToast.mock.calls).toEqual([[{ message: errorBody.error, variant: 'error' }]]))
    const error = client.getMutationCache().getAll().map((mutation) => mutation.state.error)
      .find((candidate) => candidate instanceof ApiError)
    expect(error).toBeInstanceOf(ApiError)
    if (!(error instanceof ApiError)) throw new Error('instrument: real mutation must retain its ApiError')
    // T-6c specifies human text in the toast, not a new machine-code UI label.
    expect(error.status).toBe(500)
    expect(error.code).toBe(errorBody.code)
    expect(error.userMessage).toBe(errorBody.error)
    expectNoEnableOrSuccess()
    expect(fetchAvailability).not.toHaveBeenCalled()
  })

  it('a raw 200 missing apply_outcome rejects rather than showing Saved or enabling', async () => {
    body = {}
    const client = renderPanel()
    await save(label)
    await waitFor(() => expect(client.getMutationCache().getAll().some((mutation) => mutation.state.status === 'error')).toBe(true))
    expect(addToast).toHaveBeenCalledTimes(1)
    expect(addToast.mock.calls[0][0].variant).toBe('error')
    expect(addToast.mock.calls[0][0].message).not.toMatch(/saved/i)
    expectNoEnableOrSuccess()
    expect(fetchAvailability).not.toHaveBeenCalled()
  })
})
