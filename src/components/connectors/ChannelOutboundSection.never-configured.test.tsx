// UAT finding at cut 3fbc5f5e3, chief ruling 2026-11-01 21:17Z.
// Real section, API client, generated validation and error decoder; fetch only
// is doubled. The 404 server contract is deliberately unchanged.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ChannelOutboundRead, ChannelOutboundUpdateRequest } from '@/lib/api/generated/openapi-types'
import { ChannelOutboundSection } from './ChannelOutboundSection'

const PATH = '/api/v1/channels/discord/outbound'
const FIRST_SAVE = 'Save the connection first'
const CONNECT_FIRST = 'Connect the channel first to add Discord ids.'
let read: ChannelOutboundRead
let reads: number
let writes: ChannelOutboundUpdateRequest[]
let client: QueryClient

beforeEach(() => {
  reads = 0
  writes = []
  read = {
    outbound: { policy: 'reply_only', targets: [] }, revision: 'absent',
    availability: { state: 'not_loaded', reason: 'not loaded' }, start_supported: true,
  }
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  document.cookie = 'csrf=never-configured-test; path=/'
  vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input, init) => {
    const url = input instanceof Request ? input.url : String(input)
    expect(new URL(url, 'http://localhost').pathname).toBe(PATH)
    expect(init?.credentials).toBe('include')
    if ((init?.method ?? 'GET') === 'GET') {
      reads++
      return new Response(JSON.stringify(read), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }
    expect(init?.method).toBe('PUT')
    writes.push(JSON.parse(String(init?.body)) as ChannelOutboundUpdateRequest)
    const response = read.revision === 'absent'
      ? { status: 404, body: { error: 'channel "discord" is not configured' } }
      : { status: 409, body: { code: 'connect_first', error: CONNECT_FIRST } }
    return new Response(JSON.stringify(response.body), { status: response.status, headers: { 'Content-Type': 'application/json' } })
  }))
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
})

async function mountSection() {
  render(<QueryClientProvider client={client}><ChannelOutboundSection channelId="discord" channelName="Discord" /></QueryClientProvider>)
  await waitFor(() => expect(reads).toBe(1))
  await waitFor(() => expect(client.getQueryState(['channel-outbound', 'discord'])?.status).toBe('success'))
  await waitFor(() => expect(document.querySelector('[aria-busy="true"]')).not.toBeInTheDocument())
}

async function attemptAnIdWhereControlsRemainAvailable() {
  // Hidden or disabled editing is valid; do not require a specific layout.
  // The configured control below proves this driver can actually submit.
  const radio = screen.queryByRole('radio', { name: 'Answer and also start messages' })
  if (radio && !radio.hasAttribute('disabled')) fireEvent.click(radio)
  const field = screen.queryByRole('textbox', { name: 'People and groups the agent may message' })
  if (field && !field.hasAttribute('disabled')) fireEvent.change(field, { target: { value: '222' } })
  const save = screen.queryByRole('button', { name: 'Save' })
  if (save && !save.hasAttribute('disabled')) await act(async () => { fireEvent.click(save) })
}

describe('Starting messages on a never-configured connection — separate UAT row', () => {
  it('revision absent prevents every id Save and explains Save the connection first, never a raw 404', async () => {
    await mountSection()
    await attemptAnIdWhereControlsRemainAvailable()
    expect.soft(writes, 'chief ruling: a never-configured connection must not send an outbound PUT').toEqual([])
    expect.soft(screen.queryByText(FIRST_SAVE, { exact: true }), 'chief ruling: exact actionable copy').toBeInTheDocument()
    expect.soft(document.body.textContent).not.toContain('The requested resource was not found.')
    expect.soft(document.body.textContent).not.toContain('channel "discord" is not configured')
  })

  it('positive control: configured but stopped Discord keeps its Connect the channel first refusal', async () => {
    read = { ...read, revision: 'configured-live-revision', availability: { state: 'not_running', reason: 'stopped' } }
    await mountSection()
    expect(screen.getByRole('radio', { name: 'Answer and also start messages' })).toBeEnabled()
    await attemptAnIdWhereControlsRemainAvailable()
    await waitFor(() => expect(writes).toEqual([{ policy: 'agent_may_start', targets: ['222'], expected_revision: 'configured-live-revision' }]))
    expect(await screen.findByText(CONNECT_FIRST, { exact: true })).toBeInTheDocument()
    expect(screen.queryByText(FIRST_SAVE, { exact: true })).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain('The requested resource was not found.')
  })
})
