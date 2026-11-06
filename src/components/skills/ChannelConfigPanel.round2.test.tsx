/**
 * Chief's 2026-11-02 08:09Z rulings, CHECK F7/F8. Expected values come
 * from the dispatch, not from the current field catalog. The panel, catalog,
 * controls and API client stay real; only the HTTP process edge is replaced.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { getChannelFields } from '@/lib/channel-fields'
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
  document.cookie = 'csrf=round2-synthetic-csrf; path=/'
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
        revision: 'round2-synthetic-revision',
        availability: { state: 'not_running', reason: 'not started' },
        start_supported: true,
      })
    }
    if (method === 'GET' && (url.pathname === '/api/v1/agents' || url.pathname === '/api/v1/workspaces')) {
      return jsonResponse([])
    }
    unexpectedRequests.push(`${method} ${url.pathname}`)
    throw new Error(`Round 2 HTTP fixture has no response for ${method} ${url.pathname}`)
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
  expect(unexpectedRequests, 'instrument: no real-client dependency may disappear behind a missing HTTP response').toEqual([])
})

function renderPanel(platform: string, allowFrom: string | string[] = []) {
  channelId = platform
  savedConfig = platform === 'discord'
    ? { token: '[configured]', allow_from: allowFrom }
    : { allow_from: allowFrom }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.add(client)
  render(
    <QueryClientProvider client={client}>
      <ChannelConfigPanel
        channelId={platform}
        channelName={platform}
        enabled={false}
        nativeAvailable={false}
        open
        onOpenChange={vi.fn()}
        rejectedSenders={{ count: 1, recent: ['whatsapp:1234567890@s.whatsapp.net'] }}
      />
    </QueryClientProvider>,
  )
}

async function allowFromInput(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: /^Advanced$/ }))
  return screen.getByRole<HTMLInputElement>('textbox', { name: /^Allow From$/ })
}

// Stable facts only, never an exact prose sentence. Inspect the actual
// descriptor / input-associated help, not unrelated text elsewhere on the page.
function expectAccountIdGuidance(placeholder: string, help: string) {
  expect({
    placeholderHasAccountId: placeholder.includes('@s.whatsapp.net'),
    placeholderSuggestsBarePhone: placeholder.includes('+1234567890'),
    helpHasAccountId: help.includes('@s.whatsapp.net'),
    helpSuggestsBarePhone: help.includes('+1234567890'),
    helpMentionsIgnored: help.includes('Ignored'),
    helpMentionsLatest: help.includes('Latest'),
    helpMentionsCopy: /copy/iu.test(help),
  }, `chief Q1=A guidance: placeholder=${JSON.stringify(placeholder)}, help=${JSON.stringify(help)}`).toEqual({
    placeholderHasAccountId: true,
    placeholderSuggestsBarePhone: false,
    helpHasAccountId: true,
    helpSuggestsBarePhone: false,
    helpMentionsIgnored: true,
    helpMentionsLatest: true,
    helpMentionsCopy: true,
  })
}

it('F7: displays both legacy scalar IDs and omits untouched Allow From on Save', async () => {
  const user = userEvent.setup()
  renderPanel('discord', 'a b')
  const input = await allowFromInput(user)
  expect(configReadCount, 'instrument: the legacy scalar must come from the actual GET response').toBeGreaterThan(0)
  expect(input.value.split(/[,，\s]+/u).filter(Boolean)).toEqual(['a', 'b'])

  const outbound = await screen.findByRole('region', { name: /^Starting messages$/ })
  const saves = screen.getAllByRole('button', { name: /^Save$/ })
    .filter((button) => !outbound.contains(button))
  expect(saves, 'instrument: use Configure Save, not outbound Save').toHaveLength(1)
  await user.click(saves[0])
  await waitFor(() => expect(configureBodies).toHaveLength(1))
  expect(configureBodies, 'F7: an unedited legacy scalar must not be written back by Save').toEqual([{}])
})

describe.each(['whatsapp', 'whatsapp_native'])('F8/Q1=A — %s', (platform) => {
  it('catalog guidance uses account IDs and the Configure Ignored/Latest copy source', () => {
    const allowFrom = getChannelFields(platform).find((field) => field.key === 'allow_from')
    expect(allowFrom, 'instrument: the real catalog must resolve this platform').toBeDefined()
    expectAccountIdGuidance(allowFrom?.placeholder ?? '', allowFrom?.helpText ?? '')
  })

  it('renders account-ID guidance on Allow From, without suggesting a bare phone number', async () => {
    const user = userEvent.setup()
    renderPanel(platform)
    const input = await allowFromInput(user)
    const helpId = input.getAttribute('aria-describedby')
    expect(helpId, 'instrument: Allow From must identify its own rendered help').toBe('help-allow_from')
    const help = document.getElementById(helpId ?? '')
    expect(help, 'instrument: input-associated help must actually be rendered').not.toBeNull()
    expectAccountIdGuidance(input.getAttribute('placeholder') ?? '', help?.textContent ?? '')
  })
})
