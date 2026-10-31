// RED — #1449 US-1, US-6, US-12. Oracles: docs/internal/specs/1449-group-chat-spec.md
// S-01, S-02, S-05, S-07, S-08, S-09, S-41, S-104, S-105, S-106.
// Real RoomScreen through the l1-room-ui harness. Only the socket and fetch are doubled.
import { afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import {
  Network, ROOM_ID, entry, loadRoomScreen, openRoom, resetRealStores, snapshot, stopHarness, WS,
} from '../../../tests/l1-room-ui/harness'
import type { WsConnection } from '@/lib/ws'

let net: Network
let connection: WsConnection | undefined

// #616: transform the real room screen in setup using the existing hook budget.
beforeAll(async () => { await loadRoomScreen() })
beforeEach(() => {
  resetRealStores()
  net = new Network()
  connection = undefined
})
afterEach(() => stopHarness(connection))

async function open(state = snapshot()) {
  const harness = await openRoom(net, state)
  connection = harness.connection
  return harness
}

describe('group messages use the normal chat renderer (US-1, US-12)', () => {
  it('S-01 renders a markdown table and a code block, not the raw markers', async () => {
    await open(snapshot({
      seq: 2,
      entries: [entry({
        id: 'answer-1',
        author: { kind: 'agent', agent_id: 'mia' },
        text: '| Col | Val |\n| --- | --- |\n| a | b |\n\n```\necho hi\n```',
        client_message_id: undefined,
        addressees: [],
      })],
    }))
    expect(screen.getByRole('table'), 'S-01: a markdown table renders as a table').toBeInTheDocument()
    expect(screen.queryByText(/\| Col \|/), 'S-01: raw markdown markers stay hidden').toBeNull()
    expect(screen.getByText('echo hi')).toBeInTheDocument()
  })

  it('S-02 copies the code block text exactly', async () => {
    await open(snapshot({
      seq: 2,
      entries: [entry({
        id: 'answer-code',
        author: { kind: 'agent', agent_id: 'mia' },
        text: '```\necho hi\n```',
        client_message_id: undefined,
        addressees: [],
      })],
    }))
    const user = userEvent.setup()
    // S-02 names the code block's Copy control. A second Copy (the whole
    // message) must not make the query match two buttons.
    const copy = screen.getByRole('button', { name: 'Copy code to clipboard' })
    await user.click(copy)
    expect(await navigator.clipboard.readText()).toBe('echo hi')
    expect(copy, 'S-02: the control confirms the copy').toHaveTextContent(/copied/i)
  })

  it('S-07 shows each agent name and avatar, and the person message has no author label', async () => {
    await open(snapshot({
      seq: 3,
      entries: [
        entry({ id: 'user-1', seq: 1, text: 'status?', author: { kind: 'user' } }),
        entry({
          id: 'mia-1', seq: 2, text: 'Mia answer',
          author: { kind: 'agent', agent_id: 'mia' }, client_message_id: undefined, addressees: [],
        }),
        entry({
          id: 'jim-1', seq: 3, text: 'Jim answer',
          author: { kind: 'agent', agent_id: 'jim' }, client_message_id: undefined, addressees: [],
        }),
      ],
    }))
    const miaRow = messageRow('Mia answer')
    const jimRow = messageRow('Jim answer')
    const userRow = messageRow('status?')
    expect(within(miaRow).getByText('Mia')).toBeInTheDocument()
    expect(within(jimRow).getByText('Jim')).toBeInTheDocument()
    // S-07: the avatar sits beside the visible name. The shared mark is
    // decorative (not a named image) when the name is already shown.
    expect(within(miaRow).getByTestId('agent-icon'), 'S-07: Mia has an avatar').toBeInTheDocument()
    expect(within(jimRow).getByTestId('agent-icon'), 'S-07: Jim has an avatar').toBeInTheDocument()
    expect(within(userRow).queryByText(/^You$/), 'S-07: the person message has no author label').toBeNull()
    expect(userRow.className, 'S-07: the person message is right-aligned').toMatch(/flex-row-reverse|items-end|ml-auto|self-end/)
  })

  it('S-08 keeps the muted after-pause mark', async () => {
    await open(snapshot({
      seq: 2,
      entries: [entry({
        id: 'after',
        author: { kind: 'agent', agent_id: 'mia' },
        text: 'finished after the pause',
        after_pause: true,
        client_message_id: undefined,
        addressees: [],
      })],
    }))
    expect(screen.getByText(/after pause/i)).toBeInTheDocument()
  })

  it("S-09 does not show another chat's tool calls on a group row", async () => {
    useChatStore.setState({
      messages: {
        'foreign-session': [{
          id: 'foreign-1', role: 'assistant', content: 'FOREIGN_TOOL_alpha running',
        }],
      },
    } as never)
    await open(snapshot({
      seq: 2,
      entries: [entry({
        id: 'last',
        author: { kind: 'agent', agent_id: 'mia' },
        text: 'group answer',
        client_message_id: undefined,
        addressees: [],
      })],
    }))
    expect(screen.queryByText(/FOREIGN_TOOL_alpha/)).toBeNull()
  })

  it('S-104 shows an agent script tag as text and does not run it', async () => {
    const literal = '<script>alert(1)</script>'
    await open(snapshot({
      seq: 2,
      entries: [entry({
        id: 'html',
        author: { kind: 'agent', agent_id: 'mia' },
        text: literal,
        client_message_id: undefined,
        addressees: [],
      })],
    }))
    const row = messageRow(literal)
    expect(row.textContent).toContain(literal)
    expect(row.querySelector('script'), 'S-104: agent HTML must not become a script element').toBeNull()
  })

  it('S-105 strikes unsafe link schemes and keeps a normal link clickable', async () => {
    await open(snapshot({
      seq: 2,
      entries: [entry({
        id: 'link',
        author: { kind: 'agent', agent_id: 'mia' },
        text: '[run](javascript:alert(1))\n\n[payload](data:text/html,x)\n\n[site](https://example.com)',
        client_message_id: undefined,
        addressees: [],
      })],
    }))
    // jsdom does not compute Tailwind, so the strike is the shared renderer's
    // line-through class (DS-6 rows 6–7), not getComputedStyle.
    expectStruck('run')
    expectStruck('payload')
    expect(document.querySelector('a[href^="javascript:"]')).toBeNull()
    expect(document.querySelector('a[href^="data:"]')).toBeNull()
    const site = screen.getByRole('link', { name: 'site' })
    expect(site, 'S-105: a normal link stays clickable').toHaveAttribute('href', 'https://example.com')
    expect(site, 'S-105: a normal link opens in a new tab').toHaveAttribute('target', '_blank')
    expect(site.getAttribute('rel') ?? '', 'S-105: a normal link opens without referrer access').toMatch(/noreferrer/)
  })

  it('S-106 does not request a remote image address', async () => {
    const hits: string[] = []
    const original = window.fetch
    // S-106 watches the remote image only. The room record is also loaded
    // through fetch; answering every request with 404 never reaches the image.
    window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : String(input)
      if (url.includes('tracker.example')) {
        hits.push(url)
        return new Response('', { status: 404 })
      }
      return original(input, init)
    }) as typeof fetch
    try {
      await open(snapshot({
        seq: 2,
        entries: [entry({
          id: 'img',
          author: { kind: 'agent', agent_id: 'mia' },
          text: '![pixel](https://tracker.example/p.png)',
          client_message_id: undefined,
          addressees: [],
        })],
      }))
      expect(hits, 'S-106: no fetch request to the remote image').toEqual([])
      // FR-069 / accepted finding I8: jsdom does not load <img> resources or
      // enforce the page's policy, so DOM absence is not the security oracle.
      // T-56 is the proof: real browser + embedded SPA, an allowed-request
      // positive control and the policy img-src 'self' data: blob: blocking
      // network contact. This unit check covers explicit fetch calls only.
    } finally {
      window.fetch = original
    }
  })
})

describe('group history uses the shared column behaviour (US-6)', () => {
  it('S-41 shows Return to latest only while earlier messages are being read', async () => {
    // S-41 Given is "scrolled far up", not "older entries exist". Opening on
    // the newest message must not show the control (US-6 AS 3, UAT-18).
    const harness = await open(snapshot({
      has_older: true,
      seq: 3,
      entries: [
        entry({ id: 'e1', seq: 1, text: 'older visible' }),
        entry({ id: 'e2', seq: 2, text: 'newest visible' }),
      ],
    }))
    expect(screen.queryByRole('button', { name: /^Return to latest$/ })).toBeNull()
    enterEarlierReading(harness.store)
    const control = await screen.findByRole('button', { name: /^Return to latest$/ })
    await userEvent.setup().click(control)
    expect(screen.getByText('newest visible')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /^Return to latest$/ })).toBeNull()
    })
  })

  it('S-39 puts the message box in the same centred column as a normal chat', async () => {
    await open()
    const box = screen.getByRole('combobox', { name: 'Message the group' })
    expect(
      box.closest('.max-w-3xl'),
      'S-39: the group message box uses the normal chat centred column (max-w-3xl)',
    ).not.toBeNull()
    expect(net.writes(), 'opening a group posts nothing').toEqual([])
    expect(WS).toBe('sales')
    expect(ROOM_ID).toBe('launch')
  })
})

/** The message row. The shared list uses data-message-id; the old list uses article. */
function messageRow(text: string): HTMLElement {
  const node = screen.getByText(text)
  const row = node.closest('article, [data-message-id]')
  expect(row, `the message "${text}" is shown in a row`).not.toBeNull()
  return row as HTMLElement
}

/** S-105: struck through, and not an anchor. */
function expectStruck(label: string): void {
  const node = screen.getByText(label)
  const marked = node.className.includes('line-through') ? node : node.closest('.line-through')
  expect(marked, `S-105: "${label}" is struck through`).not.toBeNull()
  expect(marked!.closest('a'), `S-105: "${label}" is not clickable`).toBeNull()
}

/**
 * The shared reader mode that scrolling far up sets (S-41 Given). The room
 * store's setViewMode is that signal once the group uses the shared list.
 */
function enterEarlierReading(store: { getState(): unknown; setState(state: object, replace: true): void }): void {
  const api = store as unknown as {
    getState: () => {
      rooms: Record<string, object>
      setViewMode?: (roomId: string, mode: 'historical' | 'latest') => void
    }
    setState: (partial: object) => void
  }
  const state = api.getState()
  act(() => {
    if (state.setViewMode) {
      state.setViewMode(ROOM_ID, 'historical')
      return
    }
    const current = state.rooms[ROOM_ID] ?? {}
    api.setState({ rooms: { ...state.rooms, [ROOM_ID]: { ...current, viewMode: 'historical' } } })
  })
}
