// RED — #1449 US-1, US-6, US-12. Oracles: docs/internal/specs/1449-group-chat-spec.md
// S-01, S-02, S-05, S-07, S-08, S-09, S-41, S-104, S-105, S-106.
// Real RoomScreen through the l1-room-ui harness. Only the socket and fetch are doubled.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import {
  Network, ROOM_ID, entry, openRoom, resetRealStores, snapshot, stopHarness, WS,
} from '../../../tests/l1-room-ui/harness'
import type { WsConnection } from '@/lib/ws'

let net: Network
let connection: WsConnection | undefined

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
    await user.click(screen.getByRole('button', { name: /copy/i }))
    expect(await navigator.clipboard.readText()).toBe('echo hi')
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
    const miaRow = screen.getByText('Mia answer').closest('article')
    const userRow = screen.getByText('status?').closest('article')
    expect(miaRow).not.toBeNull()
    expect(within(miaRow!).getByText('Mia')).toBeInTheDocument()
    expect(within(miaRow!).getByRole('img', { name: /mia/i }), 'S-07: the answer carries the author avatar').toBeInTheDocument()
    expect(userRow).not.toBeNull()
    expect(within(userRow!).queryByText(/^You$/), 'S-07: the person message has no author label').toBeNull()
    expect(userRow!.className).toMatch(/items-end|ml-auto|self-end/)
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

  it('S-09 does not show another chat's tool calls on a group row', async () => {
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
    const row = screen.getByText(literal).closest('article')
    expect(row?.textContent).toContain(literal)
    expect(row?.querySelector('script'), 'S-104: agent HTML must not become a script element').toBeNull()
  })

  it('S-105 strikes a script-scheme link and does not make it clickable', async () => {
    await open(snapshot({
      seq: 2,
      entries: [entry({
        id: 'link',
        author: { kind: 'agent', agent_id: 'mia' },
        text: '[click](javascript:alert(1))',
        client_message_id: undefined,
        addressees: [],
      })],
    }))
    expect(document.querySelector('a[href^="javascript:"]'), 'S-105: a script-scheme link is not clickable').toBeNull()
    const shown = screen.getByText(/javascript:alert\(1\)/)
    expect(window.getComputedStyle(shown).textDecorationLine).toContain('line-through')
  })

  it('S-106 does not request a remote image address', async () => {
    const hits: string[] = []
    const original = window.fetch
    window.fetch = (async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('tracker.example')) hits.push(url)
      return new Response('', { status: 404 })
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
      expect(hits, 'S-106: no request to the remote image').toEqual([])
      expect(document.querySelector('img[src*="tracker.example"]')).toBeNull()
    } finally {
      window.fetch = original
    }
  })
})

describe('group history uses the shared column behaviour (US-6)', () => {
  it('S-41 offers Return to latest once earlier messages exist', async () => {
    await open(snapshot({
      has_older: true,
      seq: 3,
      entries: [
        entry({ id: 'e1', seq: 1, text: 'older visible' }),
        entry({ id: 'e2', seq: 2, text: 'newest visible' }),
      ],
    }))
    expect(screen.getByRole('button', { name: /^Return to latest$/ })).toBeInTheDocument()
  })

  it('S-39 puts the message box in the same centred column as a normal chat', async () => {
    await open()
    const box = screen.getByRole('textbox', { name: /message the group/i })
    expect(
      box.closest('.max-w-3xl'),
      'S-39: the group message box uses the normal chat centred column (max-w-3xl)',
    ).not.toBeNull()
    expect(net.writes(), 'opening a group posts nothing').toEqual([])
    expect(WS).toBe('sales')
    expect(ROOM_ID).toBe('launch')
  })
})
