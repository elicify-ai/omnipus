/**
 * U7 / channels-spec-r8 FR-054, US-6 AS1, S-50.
 * Oracles: the field's comma-separated help text and the chief's explicit
 * comma-or-whitespace ruling, with the WhatsApp comma-only exception (chief
 * option B, 2026-11-02 06:38Z). The brief supplies all exact phone/Discord IDs.
 * Panel, field catalog, controls, client and schemas stay real. Only HTTP is
 * replaced, and the raw request is captured without server-side normalization.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ChannelConfigPanel } from './ChannelConfigPanel'

const clients = new Set<QueryClient>()
const configureBodies: unknown[] = []
const unexpectedRequests: string[] = []
let channelId: string
let savedConfig: Record<string, unknown>
let configReadCount: number

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

beforeEach(() => {
  configureBodies.length = 0
  unexpectedRequests.length = 0
  configReadCount = 0
  savedConfig = { token: '[configured]', allow_from: [] }
  document.cookie = 'csrf=u7-synthetic-csrf; path=/'
  vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : String(input), 'http://localhost')
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    const path = `/api/v1/channels/${channelId}`
    if (method === 'PUT' && url.pathname === `${path}/configure`) {
      expect(init?.credentials).toBe('include')
      expect(typeof init?.body).toBe('string')
      configureBodies.push(JSON.parse(String(init?.body)))
      return jsonResponse({ apply_outcome: 'no_reload_needed' })
    }
    if (method === 'GET' && url.pathname === path) {
      configReadCount += 1
      return jsonResponse(savedConfig)
    }
    if (method === 'GET' && url.pathname === `${path}/routing`) {
      return jsonResponse({ default_agent_id: '', workspace_id: '', bound: false })
    }
    if (method === 'GET' && url.pathname === `${path}/outbound`) {
      return jsonResponse({
        outbound: { policy: 'reply_only', targets: [] },
        revision: 'u7-synthetic-revision',
        availability: { state: 'not_running', reason: 'not started' },
        start_supported: true,
      })
    }
    if (method === 'GET' && (url.pathname === '/api/v1/agents' || url.pathname === '/api/v1/workspaces')) {
      return jsonResponse([])
    }
    unexpectedRequests.push(`${method} ${url.pathname}${url.search}`)
    throw new Error(`U7 HTTP fixture has no response for ${method} ${url.pathname}`)
  }))
})

afterEach(async () => {
  cleanup()
  for (const client of clients) {
    await client.cancelQueries()
    client.clear()
  }
  clients.clear()
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
  expect(unexpectedRequests, 'instrument: no real-client dependency was silently left without an HTTP response').toEqual([])
})

function renderPanel(platform: string, initialAllowFrom: string[] = []) {
  channelId = platform
  // WhatsApp has no token descriptor; don't inject another connector's secret.
  savedConfig = platform === 'whatsapp'
    ? { allow_from: initialAllowFrom }
    : { token: '[configured]', allow_from: initialAllowFrom }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.add(client)
  const onOpenChange = vi.fn()
  const panel = (open: boolean) => (
    <QueryClientProvider client={client}>
      <ChannelConfigPanel channelId={platform} channelName={platform} enabled={false} open={open} onOpenChange={onOpenChange} />
    </QueryClientProvider>
  )
  const view = render(panel(true))
  return { setOpen: (open: boolean) => view.rerender(panel(open)) }
}

async function openAllowFrom(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: /^Advanced$/ }))
  return screen.getByRole<HTMLInputElement>('textbox', { name: /^Allow From$/ })
}

async function save(user: ReturnType<typeof userEvent.setup>) {
  const outbound = await screen.findByRole('region', { name: /^Starting messages$/ })
  const configureSaves = screen.getAllByRole('button', { name: /^Save$/ })
    .filter((button) => !outbound.contains(button))
  expect(configureSaves, 'instrument: select Configure Save, not the separate outbound Save').toHaveLength(1)
  await user.click(configureSaves[0])
  await waitFor(() => expect(configureBodies).toHaveLength(1))
}

describe('U7 Allow From — WhatsApp comma-only exception', () => {
  // The chief's literal examples fix the storage/request oracle. Preserve
  // internal phone spaces; this is not a claim of phone-to-native-JID matching.
  it.each([
    { text: '+1 234 567', want: ['+1 234 567'] },
    { text: '+1 234 567, +4 987 654', want: ['+1 234 567', '+4 987 654'] },
  ])('typing "$text" sends whole phone IDs, preserving internal spaces', async ({ text, want }) => {
    const user = userEvent.setup()
    renderPanel('whatsapp')
    const input = await openAllowFrom(user)
    await user.type(input, text)
    expect(input).toHaveValue(text)

    await save(user)

    expect(configureBodies, 'chief option B: split WhatsApp IDs only at commas, never at phone-number spaces').toEqual([
      { allow_from: want },
    ])
  })
})

describe.each(['discord', 'telegram'])('U7 Allow From — %s', (platform) => {
  it.each(['111, 222', '111 222'])('typing %j submits two separate IDs in the HTTP request body', async (text) => {
    const user = userEvent.setup()
    renderPanel(platform)
    const input = await openAllowFrom(user)
    await user.type(input, text)
    expect(input).toHaveValue(text)

    await save(user)

    expect(configureBodies, 'U7: send the exact ID list, never the unsplit typed text').toEqual([
      { allow_from: ['111', '222'] },
    ])
  })

  it('clearing the allowed IDs submits an empty list, not one empty ID', async () => {
    const user = userEvent.setup()
    renderPanel(platform, ['111', '222'])
    const input = await openAllowFrom(user)
    await user.clear(input)
    expect(input).toHaveValue('')

    await save(user)

    // Help text / US-7 AS2: an empty allow list admits everyone.
    expect(configureBodies).toEqual([{ allow_from: [] }])
  })

  it('reopening a saved list displays both IDs and preserves them on untouched save', async () => {
    const user = userEvent.setup()
    const panel = renderPanel(platform, ['111', '222'])
    const initial = await openAllowFrom(user)
    // The separator contract fixes the sequence, not optional display spacing.
    expect(initial.value.split(/[,\s]+/u).filter(Boolean)).toEqual(['111', '222'])
    const readsBeforeClose = configReadCount
    panel.setOpen(false)
    await waitFor(() => expect(screen.queryByRole('textbox', { name: /^Allow From$/ })).not.toBeInTheDocument())

    panel.setOpen(true)
    const reopened = await openAllowFrom(user)
    await waitFor(() => expect(configReadCount).toBeGreaterThan(readsBeforeClose))
    expect(reopened.value.split(/[,\s]+/u).filter(Boolean)).toEqual(['111', '222'])
    await save(user)
    expect(configureBodies).toEqual([{ allow_from: ['111', '222'] }])
  })
})
