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

function transportFixture(
  list: ChannelEntry[],
  configs: Record<string, Record<string, unknown>> = {},
  behavior: Record<string, { sentinel?: boolean; fail?: boolean; hold?: boolean }> = {},
) {
  // Every GET /api/v1/channels/{id} (config shape — routing and channel
  // actions excluded) is recorded here in call order. These are the
  // saved-settings classification reads whose request boundary F6 pins.
  const configReads: string[] = []
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = new URL(String(input), 'http://localhost').pathname
    const method = init?.method ?? 'GET'
    if (path === '/api/v1/channels') return json(list)
    if (/^\/api\/v1\/channels\/[^/]+\/(configure|enable|disable|test|outbound)$/.test(path)) {
      throw new Error(`unexpected HTTP boundary: ${method} ${path}`)
    }
    if (/^\/api\/v1\/channels\/[^/]+\/routing$/.test(path)) return json(ROUTING)
    const configMatch = path.match(/^\/api\/v1\/channels\/([^/]+)$/)
    if (configMatch) {
      const id = decodeURIComponent(configMatch[1])
      configReads.push(id)
      const stubRead = behavior[id] ?? {}
      if (stubRead.hold) return new Promise<Response>(() => { /* held: never settles */ })
      if (stubRead.fail) return json({ error: 'controlled saved-settings read failure' }, 503)
      if (stubRead.sentinel) {
        // `sentinel: true` = the read SUCCEEDS and reports a STORED credential:
        // the redacted config read carries the "[configured]" marker on a
        // credential field (rest_channels.go::redactChannelConfig —
        // configHasSavedCredential checks for exactly that value), the same
        // body shape the D1 saved-credential test serves; the ref name follows
        // the channel_<type>_token convention and only the sentinel value is
        // load-bearing. An EMPTY body stays a never-configured body — pass
        // `configs[id]` (or nothing) for that, never `sentinel`.
        return json({ token: CONFIGURED_SENTINEL, token_ref: `channel_${id}_token` })
      }
      return json(configs[id] ?? {})
    }
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
  return { ...view, client, configReads }
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

// ── Round 3 — D1 (F3): a DISABLED real connector shows the muted "Off" badge ──
//
// Team-lead decision D1: a disabled connector shows the muted "Off" badge
// whenever it is a real connector — it has an instance_id, or an identity
// (bound), or a saved credential (bare stub with the "[configured]"
// sentinel). "Not configured" is only for a never-configured stub. The label
// AND the muted Badge variant are the oracle (the check's 1569-m3 and
// 1569-m4 mutants survived the round-2 suite).

// The muted Badge variant's own background token (src/components/ui/badge.tsx
// badgeVariants.variant.muted); the success variant carries
// bg-[var(--color-success)]/20 instead.
const MUTED_BADGE_CLASS = 'bg-[var(--color-surface-1)]'
const SUCCESS_BADGE_CLASS = 'bg-[var(--color-success)]/20'

async function requireOffBadge(card: HTMLElement, id: string) {
  const badge = within(card).getByText('Off')
  expect(badge, `${id}: the disabled connector's status badge must read exactly "Off"`).toBeInTheDocument()
  expect(badge, `${id}: Off must be the muted Badge variant`).toHaveClass(MUTED_BADGE_CLASS)
  expect(badge, `${id}: Off must not render as the success variant (mutant 1569-m4)`).not.toHaveClass(SUCCESS_BADGE_CLASS)
  expect(within(card).queryByText('Not configured'),
    `${id}: a real disabled connector must not be labelled "Not configured" (mutant 1569-m3)`).not.toBeInTheDocument()
}

describe('Connectors — a disabled real connector shows the muted Off badge (D1/F3)', () => {
  it('shows Off, muted, for a disabled bound connector with an identity', async () => {
    // A bound (identity-carrying) disabled connector is a real connector:
    // D1 demands Off, not "Not configured" — today it renders "Not
    // configured" because only credential-saved stubs reach the off status.
    transportFixture([
      WEBCHAT,
      {
        ...instance('telegram', 'Telegram', false),
        identity: { kind: 'agent' as const, id: 'mia' },
      },
    ])
    const card = await screen.findByTestId('channel-card-telegram')
    await requireOffBadge(card, 'telegram (bound)')
  })

  it('shows Off, muted, for a disabled operator-created namespaced instance', async () => {
    // A namespaced instance (ADR-029 <type>.<slug>) is never a template; its
    // disabled card must read Off — today "Not configured".
    transportFixture([
      WEBCHAT,
      instance('whatsapp.sales', 'WhatsApp', false),
    ])
    const card = await screen.findByTestId('channel-card-whatsapp.sales')
    await requireOffBadge(card, 'whatsapp.sales')
  })

  it('shows Off, muted, for a disabled bare stub whose saved-settings read found a credential', async () => {
    // The case the #1569 fix already covers: the "[configured]" sentinel
    // promotes the stub to a card and its badge reads Off. This test keeps
    // that behaviour pinned for the label and the variant.
    transportFixture(
      [WEBCHAT, instance('discord', 'Discord', false)],
      { discord: { token: CONFIGURED_SENTINEL, token_ref: 'channel_discord_token' } },
    )
    const card = await screen.findByTestId('channel-card-discord')
    await requireOffBadge(card, 'discord (saved credential)')
  })

  it('never shows Off or a card for a never-configured stub', async () => {
    // D1's boundary: "Not configured" territory is the never-configured
    // stub, which is a roster tile with no status badge at all.
    transportFixture([WEBCHAT, instance('matrix', 'Matrix', false)])
    const tile = await screen.findByTestId('channel-roster-connect-matrix')
    expect(tile).toBeInTheDocument()
    expect(screen.queryByTestId('channel-card-matrix')).not.toBeInTheDocument()
    expect(screen.queryByText('Off')).not.toBeInTheDocument()
  })
})

// ── Round 3 — D2 (F1): a failed or held saved-settings read strands nothing ──
//
// Team-lead decision D2: if the saved-settings read for a disabled bare stub
// fails, the connector is NOT downgraded to an unconfigured tile — its card
// stays with Configure, Enable and Delete, plus a visible inline notice that
// its saved settings could not be checked, with a Retry control. One slow or
// failed read must not hold back the other stubs (each stub settles on its
// own).

describe('Connectors — a failed saved-settings read strands nothing (D2/F1)', () => {
  it('keeps the failed stub\'s card with Configure, Enable, Delete, an inline notice and Retry, and never downgrades it to a tile', async () => {
    // Discord's config read fails; Matrix's succeeds. Today the failed stub
    // silently becomes an unconfigured tile (Enable/Delete lost, no
    // explanation); D2 demands the card stays and says honestly that its
    // saved settings could not be checked, with a Retry control.
    transportFixture(
      [WEBCHAT, instance('discord', 'Discord', false), instance('matrix', 'Matrix', false)],
      {},
      { discord: { fail: true }, matrix: { sentinel: true } },
    )

    const card = await screen.findByTestId('channel-card-discord')
    expect(within(card).getByRole('button', { name: 'Configure discord' })).toBeInTheDocument()
    expect(within(card).getByTestId('channel-toggle-discord')).toHaveTextContent('Enable')
    expect(within(card).getByTestId('channel-delete-btn-discord')).toBeInTheDocument()
    // The honest inline notice, worded after D2: the saved settings could
    // not be checked — plus the Retry control.
    expect(within(card).getByText(/saved settings could not be/i),
      'the failed stub must say its saved settings could not be checked, not silently pose as unconfigured').toBeInTheDocument()
    expect(within(card).getByRole('button', { name: /retry/i })).toBeInTheDocument()
    // The other stub's successful read must not be held back by the failure.
    const matrixCard = await screen.findByTestId('channel-card-matrix')
    expect(within(matrixCard).getByText('Off')).toBeInTheDocument()
  })

  it('a held (never-resolving) saved-settings read for one stub does not hold back another stub whose read succeeded', async () => {
    // Discord's config read never settles; Matrix's succeeds. Today the
    // aggregate Promise.all waits for every stub, so Matrix's confirmed
    // classification never publishes either — D2: each stub settles on its own.
    transportFixture(
      [WEBCHAT, instance('discord', 'Discord', false), instance('matrix', 'Matrix', false)],
      {},
      { discord: { hold: true }, matrix: { sentinel: true } },
    )

    const matrixCard = await screen.findByTestId('channel-card-matrix')
    expect(within(matrixCard).getByText('Off')).toBeInTheDocument()
  })
})

// ── Round 3 — F6: the request boundary of the saved-settings reads ───────────
//
// Classification config reads go ONLY to the disabled bare stubs (ids and
// count) — never to enabled instances, namespaced instances or placeholder
// rows. The check's 1569-m5 mutant (read every instance) survived round 2.

describe('Connectors — the saved-settings read boundary (F6)', () => {
  it('reads saved settings only for the disabled bare stubs — never for enabled, namespaced or placeholder rows', async () => {
    const f = transportFixture(
      [
        WEBCHAT,
        instance('telegram', 'Telegram', true),          // enabled instance — no read
        instance('whatsapp.sales', 'WhatsApp', false),   // disabled namespaced — no read
        instance('discord', 'Discord', false),           // disabled bare stub — read
        instance('matrix', 'Matrix', false),             // disabled bare stub — read
        placeholder('slack', 'Slack'),                   // placeholder row — no read
      ],
      { discord: { token: CONFIGURED_SENTINEL, token_ref: 'channel_discord_token' } },
    )

    // Wait until both stub classifications settled (discord's card exists)
    // and the rendered rows' routing reads are in flight or done.
    await screen.findByTestId('channel-card-discord')
    await waitFor(() => {
      expect(screen.getByTestId('channel-card-telegram')).toBeInTheDocument()
      expect(screen.getByTestId('channel-card-whatsapp.sales')).toBeInTheDocument()
    })

    // ORACLE: exactly the disabled bare stubs were read — the exact id set
    // pins both the ids and the count (2). Any read of an enabled instance,
    // a namespaced instance or a placeholder row fails this equality, and so
    // does a read of a stub that does not exist.
    const read = (ids: string[]) => [...new Set(ids)].sort()
    expect(read(f.configReads), `saved-settings reads must go to exactly the disabled bare stubs; got ${JSON.stringify([...new Set(f.configReads)].sort())}`)
      .toEqual(['discord', 'matrix'])
  })
})
