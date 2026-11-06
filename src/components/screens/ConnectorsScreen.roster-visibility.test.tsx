// ConnectorsScreen roster visibility — issue #1569.
//
// Oracles (issue #1569 "Expected", never current behaviour):
//   (a) "unconfigured types stay discoverable": with one connector
//       configured, the tiles of the other unconfigured connector types are
//       still shown (US-9 AS-3's own contract — the roster doc comment says
//       it renders in BOTH states; renderChannelsBody renders it in neither
//       populated state today → (a) and (b) are RED on today's code).
//   (b) "A disabled connector stays visible as configured-but-off, with
//       Configure, Enable and Delete": a bare-key connector whose token is
//       saved must not be misclassified as a template stub once disabled.
//   (c) Control: a TRUE template stub (never configured) still renders as a
//       roster Configure tile that opens the panel on the bare type key —
//       today's behaviour that must survive the fix.
//
// Known design note for the architect (contract gap, reported): ChannelEntry
// carries no credential-configured marker, so a token-saved bare-key stub is
// wire-indistinguishable from a never-configured stub. The fixtures serve the
// per-id config read (GET /channels/{id} → "[configured]" sentinel) that the
// frontend can classify from; if the fix instead lands a wire field, the
// list fixture is reconciled in the fix round — the asserted outcomes do not
// change.
//
// Real mounted screen/panel, queries, stores. Doubles are confined to HTTP
// and the outbound WebSocket transport, exactly like ConnectorsScreen
// .ordering.test.tsx; no success-only mocks.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ChannelEntry } from '@/lib/api/generated/openapi-types'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { useUiStore } from '@/store/ui'
import { ConnectorsScreen } from './ConnectorsScreen'

// The "[configured]" sentinel the backend's redacted config read returns for
// a stored credential (rest_channels.go::redactChannelConfig). Synthetic
// reference name, never a secret value.
const CONFIGURED_SENTINEL = '[configured]'

function instance(id: string, name: string, enabled: boolean): ChannelEntry {
  return {
    id, instance_id: id, name, transport: 'webhook', enabled,
    description: `${name} (${id})`,
  }
}

// A static "available but unconfigured" placeholder row: no instance_id.
function placeholder(id: string, name: string): ChannelEntry {
  return { id, name, transport: 'webhook', enabled: false, description: name }
}

const WEBCHAT: ChannelEntry = {
  id: 'webchat', name: 'Web Chat', transport: 'websocket', enabled: true,
  description: 'Built-in browser chat',
}

const ROUTING = { workspace_id: '', default_agent_id: '', bound: false }

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
}

type Fixture = ReturnType<typeof transportFixture>

function transportFixture(list: ChannelEntry[], configs: Record<string, Record<string, unknown>> = {}) {
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = new URL(String(input), 'http://localhost').pathname
    const method = init?.method ?? 'GET'
    if (path === '/api/v1/channels') return json(list)
    if (/^\/api\/v1\/channels\/[^/]+\/(configure|enable|disable|test|outbound)$/.test(path)) {
      throw new Error(`unexpected HTTP boundary: ${method} ${path}`)
    }
    if (/^\/api\/v1\/channels\/[^/]+\/routing$/.test(path)) return json(ROUTING)
    const configMatch = path.match(/^\/api\/v1\/channels\/([^/]+)$/)
    if (configMatch) return json(configs[decodeURIComponent(configMatch[1])] ?? {})
    if (path === '/api/v1/mailboxes') return json({ mailboxes: [] })
    if (path === '/api/v1/agents' || path === '/api/v1/workspaces') return json([])
    throw new Error(`unexpected HTTP boundary: ${method} ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const client = new QueryClient({ defaultOptions: {
    queries: { retry: false, gcTime: Infinity }, mutations: { retry: false },
  } })
  const connection = new WsConnection({ onConnected() {}, onDisconnected() {}, onError() {} })
  vi.spyOn(connection, 'send').mockReturnValue(true)
  useConnectionStore.setState({ connection, isConnected: true })
  const view = render(<QueryClientProvider client={client}><ConnectorsScreen /></QueryClientProvider>)
  return { ...view, client }
}

beforeEach(() => {
  document.cookie = 'csrf=roster-visibility-test; path=/'
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

describe('Connectors — unconfigured types stay discoverable with one configured (#1569 a)', () => {
  it('shows the other unconfigured types\' Configure tiles alongside the configured connector', async () => {
    // Telegram is configured (bare key, enabled). Discord and Slack exist
    // only as static placeholder rows — the GET /channels list the issue's
    // UAT recorded once one connector exists.
    transportFixture([
      WEBCHAT,
      instance('telegram', 'Telegram', true),
      placeholder('discord', 'Discord'),
      placeholder('slack', 'Slack'),
    ])

    // The configured connector's own group still renders…
    await waitFor(() => {
      expect(screen.getByTestId('channel-card-telegram')).toBeInTheDocument()
    })
    // …and ORACLE (a): the unconfigured types stay discoverable as roster
    // Configure tiles — today the whole roster disappears once anything is
    // configured, so these tiles exist nowhere.
    expect(screen.getByTestId('channel-roster-connect-discord')).toBeInTheDocument()
    expect(screen.getByTestId('channel-roster-connect-slack')).toBeInTheDocument()
  })
})

describe('Connectors — a disabled configured connector stays visible (#1569 b)', () => {
  it('shows a disabled bare-key connector with a saved token as configured-but-off with Configure, Enable and Delete', async () => {
    // The issue's stranded state: telegram is configured and running; the
    // discord bare-key connector has a saved token and is disabled — on the
    // list it looks like the DefaultConfig template stub (bare key, disabled,
    // no identity), which today misclassifies it and strands it.
    transportFixture(
      [
        WEBCHAT,
        instance('telegram', 'Telegram', true),
        instance('discord', 'Discord', false),
        placeholder('slack', 'Slack'),
      ],
      {
        // The token IS saved: the redacted per-id config read says so.
        discord: {
          type: 'discord', enabled: false,
          token: CONFIGURED_SENTINEL, token_ref: 'channel_discord_token',
        },
        telegram: { type: 'telegram', enabled: true },
      },
    )

    // ORACLE (b): the disabled connector renders as its own configured row
    // with all three affordances reachable — today it is shown nowhere.
    await waitFor(() => {
      expect(screen.getByTestId('channel-card-telegram')).toBeInTheDocument()
    })
    const card = await screen.findByTestId('channel-card-discord')
    expect(within(card).getByRole('button', { name: 'Configure discord' })).toBeInTheDocument()
    const toggle = within(card).getByTestId('channel-toggle-discord')
    expect(toggle).toHaveTextContent('Enable')
    expect(within(card).getByTestId('channel-delete-btn-discord')).toBeInTheDocument()
  })
})

describe('Connectors — a true template stub still behaves as today (#1569 c, control)', () => {
  it('renders a never-configured stub as a roster Configure tile that opens the panel on the bare type', async () => {
    // Nothing configured anywhere: the discord DefaultConfig stub (bare key,
    // disabled, NO token) is a genuine template. The roster grid is the
    // empty-state surface it must keep rendering in.
    transportFixture(
      [
        WEBCHAT,
        instance('discord', 'Discord', false),
        placeholder('telegram', 'Telegram'),
        placeholder('slack', 'Slack'),
      ],
      { discord: { type: 'discord', enabled: false } },
    )

    const tile = await screen.findByTestId('channel-roster-connect-discord')
    expect(tile).toBeInTheDocument()
    // The one-click flow: Configure opens ChannelConfigPanel directly on the
    // bare type key.
    await act(async () => { fireEvent.click(tile) })
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Save & Enable/i })).toBeInTheDocument()
    })
  })
})
