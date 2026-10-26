// Train 2 DESIGN §4 CR-T2-R6-01 / R6-D1, §5 T7.
// Real mounted screen/panel, queries, API validation and stores. Doubles are
// confined to HTTP and the outbound WebSocket transport; no success-only mocks.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ChannelEntry } from '@/lib/api/generated/openapi-types'
import type { WhatsAppPairingFrame } from '@/lib/api/generated/asyncapi-types'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { useUiStore } from '@/store/ui'
import { ChannelConfigPanel } from '@/components/skills/ChannelConfigPanel'
import { ConnectorsScreen } from './ConnectorsScreen'

const ID = 'whatsapp.ordering'
// Exact partial-error text is the existing server contract, DESIGN §6/§5 T7.
const UNCONFIRMED = 'The WhatsApp connection is enabled, but its restart could not be confirmed yet. The new code appears here when the pairing starts.'
const entry = (id: string, enabled: boolean): ChannelEntry => ({
  id, instance_id: id, name: 'WhatsApp', transport: 'native', enabled,
  description: 'WhatsApp (native, whatsmeow)', native_available: true,
})

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
}

type HttpCall = { path: string; method: string; body: BodyInit | null | undefined }

function transportFixture() {
  const enable = deferred<Response>()
  const enteredEnable = deferred<void>()
  const calls: HttpCall[] = []
  let enabled = false
  let readbackFails = false
  let configureStatus = 200
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = new URL(String(input), 'http://localhost').pathname
    const method = init?.method ?? 'GET'
    calls.push({ path, method, body: init?.body })
    if (path.endsWith('/configure')) return json(configureStatus === 200 ? {} : { error: 'controlled Configure save failure' }, configureStatus)
    if (path.endsWith('/enable')) {
      enteredEnable.resolve()
      return enable.promise
    }
    if (path.endsWith('/routing')) return json({ workspace_id: '', default_agent_id: '', bound: false })
    if (path === '/api/v1/channels') {
      return readbackFails ? json({ error: 'controlled readback failure' }, 503) : json([entry(ID, enabled)])
    }
    if (path.startsWith('/api/v1/channels/')) return json({ type: 'whatsapp', enabled: false })
    if (path === '/api/v1/mailboxes') return json({ mailboxes: [] })
    if (path === '/api/v1/agents' || path === '/api/v1/workspaces') return json([])
    throw new Error(`unexpected HTTP boundary: ${method} ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const client = new QueryClient({ defaultOptions: {
    queries: { retry: false, gcTime: Infinity }, mutations: { retry: false },
  } })
  const connection = new WsConnection({ onConnected() {}, onDisconnected() {}, onError() {} })
  // This is the external transport egress, not a mock of the interest hook.
  const send = vi.spyOn(connection, 'send').mockReturnValue(true)
  useConnectionStore.setState({ connection, isConnected: true })
  const view = render(<QueryClientProvider client={client}><ConnectorsScreen /></QueryClientProvider>)
  return {
    ...view, client, calls, send, enable, enteredEnable,
    setEnabled(value: boolean) { enabled = value },
    failReadback() { readbackFails = true },
    failConfigure() { configureStatus = 500 },
  }
}

type Fixture = ReturnType<typeof transportFixture>

async function openConfigure(f: Fixture) {
  fireEvent.click(await screen.findByRole('button', { name: `Configure ${ID}` }))
  await screen.findByRole('button', { name: /Save & Enable/i })
  expect(f.calls.filter((c) => c.path === `/api/v1/channels/${ID}`)).toHaveLength(1)
}

async function saveAndHoldEnable(f: Fixture) {
  await openConfigure(f)
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i }))
    await f.enteredEnable.promise
  })
  const writes = f.calls.filter((c) => c.method === 'PUT')
  expect(writes.map(({ path }) => path)).toEqual([
    `/api/v1/channels/${ID}/configure`, `/api/v1/channels/${ID}/enable`,
  ])
  expect(writes[1].body, 'Configure enable is unowned, never an implicit chat claim').toBeUndefined()
}

async function finishEnable(f: Fixture, response: Response) {
  await act(async () => { f.enable.resolve(response) })
  await waitFor(() => {
    expect(f.client.isMutating(), 'join the real mutation before checking recovery').toBe(0)
    expect(f.client.isFetching(), 'finish every resulting readback').toBe(0)
  })
}

function deliverCode(qr = 'QR-current') {
  const frame: WhatsAppPairingFrame = { type: 'whatsapp_pairing', channel_id: ID, status: 'code', qr }
  act(() => useWhatsAppPairingStore.getState().apply(frame))
}

function errorToasts() {
  return useUiStore.getState().toasts.filter((toast) => toast.variant === 'error').map((toast) => toast.message)
}

beforeEach(() => {
  document.cookie = 'csrf=ordering-test; path=/'
  useUiStore.setState({ toasts: [] })
  useWhatsAppPairingStore.setState({ byChannel: {} })
})

afterEach(() => {
  cleanup()
  useConnectionStore.setState({ connection: null, isConnected: false })
  useWhatsAppPairingStore.setState({ byChannel: {} })
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
})

describe('T7 mounted Configure ordering', () => {
  it('subscribes once Configure succeeds while Enable is still held', async () => {
    const f = transportFixture()
    try {
      await saveAndHoldEnable(f)
      expect.soft(f.send.mock.calls.map(([frame]) => frame)).toContainEqual({
        type: 'whatsapp_pairing_subscribe', channel_id: ID, active: true,
      })
      f.setEnabled(true)
    } finally {
      await finishEnable(f, json({ error: UNCONFIRMED }, 500))
    }
    expect(errorToasts()).toEqual([UNCONFIRMED])
  })

  it.each(['after error', 'before error'] as const)('retains the viewer after saved/unconfirmed 500, QR %s', async (order) => {
    const f = transportFixture()
    await saveAndHoldEnable(f)
    f.setEnabled(true)
    if (order === 'before error') deliverCode()
    await finishEnable(f, json({ error: UNCONFIRMED }, 500))
    if (order === 'after error') deliverCode()
    expect.soft(errorToasts()).toEqual([UNCONFIRMED])
    expect.soft(f.client.getQueryData<ChannelEntry[]>(['channels'])).toEqual([entry(ID, true)])
    expect.soft(screen.queryByTestId('whatsapp-qr')).toBeInTheDocument()
    expect.soft(screen.queryByTestId('whatsapp-enable-prompt')).not.toBeInTheDocument()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(useUiStore.getState().toasts.filter((toast) => toast.variant === 'success')).toEqual([])
  })

  it('an open panel follows current ChannelEntry, not its captured disabled props', async () => {
    const f = transportFixture()
    await openConfigure(f)
    f.setEnabled(true)
    await act(async () => { await f.client.refetchQueries({ queryKey: ['channels'] }) })
    deliverCode()
    expect(f.client.getQueryData<ChannelEntry[]>(['channels'])).toEqual([entry(ID, true)])
    expect(screen.queryByTestId('whatsapp-qr')).toBeInTheDocument()
  })

  it('does not enable or subscribe when Configure itself fails', async () => {
    const f = transportFixture()
    f.failConfigure()
    await openConfigure(f)
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i })) })
    expect(f.calls.filter((c) => c.path.endsWith('/enable'))).toEqual([])
    expect(f.send.mock.calls).toEqual([])
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(errorToasts()).toEqual(['controlled Configure save failure'])
  })

  it.each([
    { status: 404, code: undefined, message: 'This WhatsApp connection was deleted. Add it again from Connectors.' },
    { status: 409, code: 'pairing_in_progress', message: 'another pairing is already running' },
    { status: 500, code: undefined, message: UNCONFIRMED },
  ])('retains the error and respects disabled readback after $status', async ({ status, code, message }) => {
    const f = transportFixture()
    await saveAndHoldEnable(f)
    await finishEnable(f, json({ error: message, ...(code ? { code } : {}) }, status))
    expect(errorToasts()).toEqual([message])
    expect(f.client.getQueryData<ChannelEntry[]>(['channels'])).toEqual([entry(ID, false)])
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(useUiStore.getState().toasts.filter((toast) => toast.variant === 'success')).toEqual([])
  })

  it('failed state readback stays unknown, not assumed applied from a 500', async () => {
    const f = transportFixture()
    await saveAndHoldEnable(f)
    f.failReadback()
    await finishEnable(f, json({ error: UNCONFIRMED }, 500))
    expect.soft(errorToasts()).toEqual([UNCONFIRMED])
    expect.soft(f.client.getQueryState(['channels'])?.status).toBe('error')
    expect(useUiStore.getState().toasts.filter((toast) => toast.variant === 'success')).toEqual([])
  })
})

describe('T7 mounted row Enable ordering', () => {
  it('refreshes saved state after an unconfirmed 500 without promising an open panel', async () => {
    const f = transportFixture()
    const row = await screen.findByTestId(`channel-card-${ID}`)
    await act(async () => {
      fireEvent.click(within(row).getByRole('button', { name: `Enable ${ID}` }))
      await f.enteredEnable.promise
    })
    f.setEnabled(true)
    await finishEnable(f, json({ error: UNCONFIRMED }, 500))
    expect.soft(errorToasts()).toEqual([UNCONFIRMED])
    expect.soft(within(row).getByTestId(`channel-toggle-${ID}`)).toHaveTextContent('Disable')
    expect.soft(f.client.getQueryData<ChannelEntry[]>(['channels'])).toEqual([entry(ID, true)])
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(useUiStore.getState().toasts.filter((toast) => toast.variant === 'success')).toEqual([])
  })
})

it('T7 obsolete local Enable success cannot mount the next channel viewer', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  const held = deferred<Response>()
  const entered = deferred<void>()
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
    const path = new URL(String(input), 'http://localhost').pathname
    if (path.endsWith('/enable')) { entered.resolve(); return held.promise }
    if (path.endsWith('/routing')) return json({ workspace_id: '', default_agent_id: '', bound: false })
    if (path === '/api/v1/agents' || path === '/api/v1/workspaces') return json([])
    return json({})
  }))
  const panel = (id: string) => <QueryClientProvider client={client}>
    <ChannelConfigPanel channelId={id} channelName="WhatsApp" enabled={false} open onOpenChange={() => {}} />
  </QueryClientProvider>
  const view = render(panel(ID))
  await screen.findByRole('button', { name: /Save & Enable/i })
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i }))
    await entered.promise
  })
  view.rerender(panel('whatsapp.successor'))
  await act(async () => { held.resolve(json({ id: ID, enabled: true })) })
  expect(screen.queryByText(/Generating your QR code/i)).not.toBeInTheDocument()
  expect(screen.getByTestId('whatsapp-enable-prompt')).toBeInTheDocument()
  expect(useUiStore.getState().toasts).toEqual([])
})
