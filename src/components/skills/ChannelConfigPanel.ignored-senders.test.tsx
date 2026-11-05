/**
 * T-30 / FR-061 / S-53: real roster HTTP -> generated decoder -> real
 * ConnectorsScreen Configure action -> real ChannelConfigPanel diagnostic.
 * Exact copy/count/absence come from channels-spec-r8, not observed output.
 * No invented Panel prop, preloaded roster cache, or mocked diagnostic renderer.
 */
import React from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  ChannelEntry as EntrySchema,
  ChannelOutboundRead as OutboundReadSchema,
} from '@/lib/api/generated/schemas'
import type { ChannelEntry } from '@/lib/api/generated/openapi-types'

const { addToast } = vi.hoisted(() => ({ addToast: vi.fn() }))
vi.mock('@/store/ui', () => ({ useUiStore: () => ({ addToast }) }))
vi.mock('@/store/connection', () => {
  const state = { isConnected: false, connection: null }
  return { useConnectionStore: Object.assign((selector: (s: typeof state) => unknown) => selector(state), { getState: () => state }) }
})
vi.mock('@/store/whatsappPairing', () => ({
  useWhatsAppPairingStore: (selector: (s: { byChannel: Record<string, unknown>; clear: () => void }) => unknown) =>
    selector({ byChannel: {}, clear: vi.fn() }),
}))
vi.mock('@tanstack/react-router', () => ({
  createRootRoute: (opts: Record<string, unknown>) => ({ options: opts }),
  createFileRoute: () => (opts: Record<string, unknown>) => ({ options: opts }),
  Outlet: () => null,
  Link: ({ children, to, className }: { children: React.ReactNode; to: string; className?: string }) =>
    React.createElement('a', { href: to, className }, children),
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  useParams: () => ({}),
  useSearch: () => ({}),
}))
vi.mock('@/components/connectors/EmailMailboxPanel', () => ({ EmailMailboxPanel: () => null }))
vi.mock('framer-motion', () => ({
  motion: new Proxy({}, { get: (_: object, prop: string) =>
    ({ children, ...props }: Record<string, unknown>) => React.createElement(prop, props, children as React.ReactNode) }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual, // fetchChannels, its request and generated decoder remain real.
    fetchChannelConfig: vi.fn().mockResolvedValue({ token: '[configured]' }),
    fetchChannelRouting: vi.fn().mockResolvedValue({ default_agent_id: '', workspace_id: '', bound: false }),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchWorkspace: vi.fn().mockResolvedValue({}),
    fetchMailboxes: vi.fn().mockResolvedValue([]),
  }
})
import { ConnectorsScreen } from '@/components/screens/ConnectorsScreen'

const fetchMock = vi.fn<typeof fetch>()
let roster: ChannelEntry[]

function entry(id: string, rejected?: ChannelEntry['rejected_senders']): ChannelEntry {
  return EntrySchema.parse({
    id, instance_id: id, name: 'Discord', transport: 'websocket', enabled: true,
    description: 'Discord Gateway', ...(rejected === undefined ? {} : { rejected_senders: rejected }),
  })
}

beforeEach(() => {
  addToast.mockReset()
  roster = [entry('discord.qa')]
  fetchMock.mockReset().mockImplementation(async (input, init) => {
    const url = input instanceof Request ? input.url : String(input)
    expect(init?.method ?? 'GET').toBe('GET')
    expect(init?.credentials).toBe('include')
    let body: unknown
    if (url === '/api/v1/channels') {
      body = roster
    } else if (url === '/api/v1/channels/discord.qa/outbound') {
      body = OutboundReadSchema.parse({
        outbound: { policy: 'reply_only', targets: [] }, revision: 'fr061-read',
        availability: { state: 'running' }, start_supported: true,
      })
    } else {
      throw new Error(`FR-061 fixture received unexpected HTTP request: ${url}`)
    }
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  window.getSelection()?.removeAllRanges()
})

async function openSelectedPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><ConnectorsScreen /></QueryClientProvider>)
  fireEvent.click(await screen.findByLabelText('Configure discord.qa'))
  const panel = await screen.findByRole('dialog')
  // Wait for the actual open Panel, not just the background channel card.
  await within(panel).findByLabelText(/^Bot Token/)
  await within(panel).findByRole('radio', { name: 'Only answer' })
  expect(client.getQueryData(['channels'])).toEqual(roster)
  expect(fetchMock.mock.calls.some(([input]) => String(input) === '/api/v1/channels')).toBe(true)
  return panel
}

function normalizedText(node: Element) {
  return (node.textContent ?? '').replace(/\s+/g, ' ').trim()
}

async function exactDiagnostic(panel: HTMLElement, expected: string) {
  // Match the whole line even if copyable ids are wrapped in child spans;
  // avoid counting ancestor containers as duplicate copies of the same line.
  const lines = await within(panel).findAllByText((_text, node) =>
    node !== null && normalizedText(node) === expected &&
    !Array.from(node.children).some((child) => normalizedText(child) === expected))
  expect(lines).toHaveLength(1)
  expect(lines[0]).toBeVisible()
  return lines[0]
}

function expectSelectableDomID(line: HTMLElement, id: string) {
  // This exercises real DOM Range/Selection, not a mocked copy handler.
  // jsdom does not certify pointer/CSS selection; browser UAT still must.
  const walker = document.createTreeWalker(line, NodeFilter.SHOW_TEXT)
  let text = walker.nextNode()
  while (text && !(text.textContent ?? '').includes(id)) text = walker.nextNode()
  expect(text, 'FR-061: canonical id must be actual text, not only an attribute or inaccessible graphic').not.toBeNull()
  if (!text) throw new Error('FR-061: no selectable sender-id text node')
  const offset = (text.textContent ?? '').indexOf(id)
  const range = document.createRange()
  range.setStart(text, offset)
  range.setEnd(text, offset + id.length)
  const selection = window.getSelection()
  expect(selection).not.toBeNull()
  if (!selection) throw new Error('instrument: jsdom did not expose DOM Selection')
  selection.removeAllRanges()
  selection.addRange(range)
  expect(selection.toString()).toBe(id)
  selection.removeAllRanges()
}

describe('T-30 FR-061 ignored-senders diagnostic from the live roster', () => {
  it.each([
    { count: 1, copy: 'Ignored 1 messages from senders not on the allowed list. Latest: discord:9' },
    { count: 2, copy: 'Ignored 2 messages from senders not on the allowed list. Latest: discord:9' },
  ])('count $count shows its exact diagnostic once with a selectable canonical id', async ({ count, copy }) => {
    roster = [entry('discord.qa', { count, recent: ['discord:9'] })]
    const panel = await openSelectedPanel()
    const line = await exactDiagnostic(panel, copy)
    expectSelectableDomID(line, 'discord:9')
  })

  it('an entry without rejections shows no diagnostic or sender id from another connection', async () => {
    roster = [entry('discord.qa'), entry('discord.peer', { count: 2, recent: ['discord:77'] })]
    const panel = await openSelectedPanel()
    expect(within(panel).queryByText(/Ignored \d+ messages from senders not on the allowed list/)).not.toBeInTheDocument()
    expect(panel).not.toHaveTextContent('discord:77')
    expect(panel).not.toHaveTextContent('Latest:')
  })
})
