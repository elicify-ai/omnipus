// Channels spec FR-026 / S-80, ChannelOutboundRead.revision, and chief Q8=A.
// Real panel, section, QueryClient, API clients, schemas, controls and stores;
// only HTTP is doubled. One initial read + one post-save read = exactly two.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ChannelConfigureResponse, ChannelOutboundRead } from '@/lib/api/generated/openapi-types'
import { ApiError } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'
import { ChannelConfigPanel } from './ChannelConfigPanel'

const CHANNEL_ID = 'discord'
const CHANNEL_PATH = `/api/v1/channels/${CHANNEL_ID}`
const FIRST_SAVE = 'Save the connection first'
const CONFIGURE_BODY = { token: 'synthetic-nc-refresh-token' }
const SAVED: ChannelConfigureResponse = { apply_outcome: 'no_reload_needed' }
const ABSENT: ChannelOutboundRead = {
  outbound: { policy: 'reply_only', targets: [] },
  revision: 'absent',
  availability: { state: 'not_loaded', reason: 'not configured' },
  start_supported: true,
}
const CONFIGURED: ChannelOutboundRead = {
  ...ABSENT,
  revision: 'configured-nc-refresh-revision',
  availability: { state: 'not_running', reason: 'disabled' },
}

let client: QueryClient
let configured: boolean
let failRefresh: boolean
let outboundReads: number
let configureBodies: unknown[]
let enableRequests: string[]
let unexpectedRequests: string[]
let finishConfigure: (response: Response) => void

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

beforeEach(() => {
  configured = false
  failRefresh = false
  outboundReads = 0
  configureBodies = []
  enableRequests = []
  unexpectedRequests = []
  client = new QueryClient({ defaultOptions: {
    // Only explicit invalidation should refresh this cached absent revision.
    queries: { retry: false, staleTime: Infinity },
    mutations: { retry: false },
  } })
  const configureReply = new Promise<Response>((resolve) => { finishConfigure = resolve })
  document.cookie = 'csrf=nc-refresh-synthetic; path=/'
  vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : String(input), 'http://localhost')
    const method = init?.method ?? 'GET'
    const request = `${method} ${url.pathname}${url.search}`
    switch (request) {
      case `GET ${CHANNEL_PATH}`:
        return json(configured ? { token: '[configured]' } : {})
      case `GET ${CHANNEL_PATH}/routing`:
        return json({ default_agent_id: '', workspace_id: '', bound: false })
      case 'GET /api/v1/agents':
      case 'GET /api/v1/workspaces?status=active':
        return json([])
      case `GET ${CHANNEL_PATH}/outbound`:
        outboundReads++
        if (configured && failRefresh) return json({ error: 'outbound refresh unavailable' }, 500)
        return json(configured ? CONFIGURED : ABSENT)
      case `PUT ${CHANNEL_PATH}/configure`:
        configureBodies.push(JSON.parse(String(init?.body)))
        return configureReply
      case `PUT ${CHANNEL_PATH}/enable`:
        enableRequests.push(request)
        return json({ id: CHANNEL_ID, enabled: true })
      default:
        unexpectedRequests.push(request)
        throw new Error(`Unexpected HTTP request: ${request}`)
    }
  }))
})

afterEach(() => {
  cleanup()
  client.clear()
  const ui = useUiStore.getState()
  for (const toast of ui.toasts) ui.removeToast(toast.id)
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
  // request() handles network failures, so check this outside the HTTP fake.
  expect(unexpectedRequests, 'no unhandled fixture route may masquerade as the intended read failure').toEqual([])
})

async function saveConnection(action: 'Save' | 'Save & Enable') {
  render(<QueryClientProvider client={client}>
    {/* Keep the controlled panel mounted; never reopen or manually refetch it.
        Save & Enable may request its usual close through onOpenChange. */}
    <ChannelConfigPanel channelId={CHANNEL_ID} channelName="Discord" enabled={false}
      open={true} onOpenChange={vi.fn()} />
  </QueryClientProvider>)
  await screen.findByText(FIRST_SAVE, { exact: true })
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const panel = screen.getByRole('dialog', { name: 'Configure Discord' })
  const section = within(panel).getByRole('region', { name: 'Starting messages' })
  expect(outboundReads, 'mount must fetch exactly one absent-revision read').toBe(1)
  expect(within(section).queryAllByRole('radio')).toEqual([])
  expect(within(section).queryByRole('button', { name: 'Save' })).not.toBeInTheDocument()

  fireEvent.change(within(panel).getByLabelText(/Bot Token/), { target: { value: CONFIGURE_BODY.token } })
  await act(async () => { fireEvent.click(within(panel).getByRole('button', { name: action })) })
  await waitFor(() => expect(configureBodies).toEqual([CONFIGURE_BODY]))
  // The connection does not exist until Configure succeeds, not merely when clicked.
  expect(outboundReads, 'no outbound refresh before Configure succeeds').toBe(1)
  expect(within(section).getByText(FIRST_SAVE, { exact: true })).toBeVisible()
  await act(async () => {
    configured = true
    finishConfigure(json(SAVED))
  })
  await waitFor(() => {
    expect(client.isMutating()).toBe(0)
    expect(client.isFetching()).toBe(0)
  })
  return panel
}

describe('never-configured Starting messages refresh at the real Configure panel', () => {
  it.each(['Save', 'Save & Enable'] as const)('%s re-reads outbound exactly once and unlocks controls without reopening', async (action) => {
    const panel = await saveConnection(action)
    expect(outboundReads, `${action}: one initial read plus exactly one successful-save refresh`).toBe(2)
    expect(enableRequests).toEqual(action === 'Save & Enable' ? [`PUT ${CHANNEL_PATH}/enable`] : [])
    expect(screen.getByRole('dialog', { name: 'Configure Discord' })).toBe(panel)
    const section = within(panel).getByRole('region', { name: 'Starting messages' })
    await waitFor(() => expect(within(section).getByRole('radio', { name: 'Only answer' })).toBeChecked())
    expect(within(section).queryByText(FIRST_SAVE, { exact: true })).not.toBeInTheDocument()
    expect(within(section).getAllByRole('radio')).toHaveLength(2)
    const mayStart = within(section).getByRole('radio', { name: 'Answer and also start messages' })
    expect(mayStart).toBeEnabled()
    fireEvent.click(mayStart)
    expect(mayStart).toBeChecked()
    const targets = within(section).getByRole('textbox', { name: 'People and groups the agent may message' })
    expect(targets).toBeEnabled()
    expect(targets).toHaveValue('')
    expect(within(section).getByRole('button', { name: 'Save' })).toBeEnabled()
    expect(outboundReads, 'showing and selecting the normal controls must not trigger another read').toBe(2)
  })

  it('a failed post-Save read shows the section error, not stale Save the connection first', async () => {
    failRefresh = true
    const panel = await saveConnection('Save')
    expect(outboundReads, 'the failing refresh still requires exactly one post-save GET').toBe(2)
    expect(enableRequests).toEqual([])
    const error = client.getQueryState(['channel-outbound', CHANNEL_ID])?.error
    expect(error).toBeInstanceOf(ApiError)
    if (!(error instanceof ApiError)) throw new Error('Expected the real HTTP decoder to retain the refresh ApiError')
    expect(error.status).toBe(500)
    const section = within(panel).getByRole('region', { name: 'Starting messages' })
    const alerts = await within(section).findAllByRole('alert')
    expect(alerts).toHaveLength(1)
    expect(alerts[0]).toBeVisible()
    // The brief specifies the error state, not its exact recovery copy.
    expect(alerts[0]).toHaveTextContent(/\S/)
    expect(within(section).queryByText(FIRST_SAVE, { exact: true })).not.toBeInTheDocument()
    expect(within(section).queryAllByRole('radio')).toEqual([])
    expect(within(section).queryByRole('button', { name: 'Save' })).not.toBeInTheDocument()
    expect(screen.getByRole('dialog', { name: 'Configure Discord' })).toBe(panel)
    expect(outboundReads).toBe(2)
  })
})
