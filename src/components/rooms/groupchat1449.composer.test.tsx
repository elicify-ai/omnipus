// RED — #1449 US-2, US-11, US-13, D21.
// Oracles: docs/internal/specs/1449-group-chat-spec.md S-10, S-12, S-17, S-19, S-98, S-108.
// Real RoomScreen. Only the socket and fetch are doubled.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  Network, ROOM_ID, openRoom, resetRealStores, snapshot, stopHarness,
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

const GROUP_PLACEHOLDER = 'Message the group - type @ to address someone'

describe('the group message box is the normal message box (US-2)', () => {
  it('S-10 has the normal card, attach and send, and not the session-only controls', async () => {
    await open()
    expect(screen.getByRole('button', { name: /attach/i }), 'S-10: attach button').toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^send/i }), 'S-10: send button').toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /model/i }), 'S-10: no model picker').toBeNull()
    expect(screen.queryByRole('button', { name: /auto-approve/i }), 'S-10: no Auto-approve').toBeNull()
    expect(screen.queryByLabelText(/token/i), 'S-10: no token counter').toBeNull()
    expect(screen.queryByRole('button', { name: /skills/i }), 'S-10: no skills control').toBeNull()
    expect(screen.queryByRole('button', { name: /^stop$/i }), 'D21: no Stop button').toBeNull()
  })

  it('S-19 shows the group placeholder on an empty box', async () => {
    await open()
    expect(screen.getByRole('combobox', { name: 'Message the group' })).toHaveAttribute('placeholder', GROUP_PLACEHOLDER)
  })

  it('S-12 lists exactly the six group commands', async () => {
    await open()
    const user = userEvent.setup()
    await user.type(screen.getByRole('combobox', { name: 'Message the group' }), '/')
    // S-12: the menu lists exactly these six commands. A description beside
    // the command name is allowed; a seventh command is not.
    const commands = ['help', 'members', 'mute', 'pause', 'resume', 'unmute']
    const options = screen.getAllByRole('option')
    expect(options, 'S-12: six commands and no seventh').toHaveLength(commands.length)
    const names = options.map((option) => {
      const text = (option.textContent ?? '').toLowerCase()
      const hits = commands.filter((name) => new RegExp(`\\b${name}\\b`).test(text))
      expect(hits, `S-12: one command per row, got "${text}"`).toHaveLength(1)
      return hits[0]
    })
    expect([...names].sort()).toEqual([...commands].sort())
  })

  it('S-17 disables sending and shows waiting for the group before the first live view', async () => {
    const harness = await open()
    const store = harness.store as unknown as {
      getState: () => { rooms: Record<string, { snapshotReceived?: boolean }> }
      setState: (partial: { rooms: Record<string, object> }) => void
    }
    const current = store.getState().rooms[ROOM_ID]
    act(() => {
      store.setState({ rooms: { [ROOM_ID]: { ...current, snapshotReceived: false } } })
    })
    const send = screen.getByRole('button', { name: /^send/i })
    expect(send).toBeDisabled()
    expect(screen.getByText(/waiting for the group/i), 'S-17 / message-box loading row').toBeInTheDocument()
  })

  it('S-108 sends to the group and does not create a chat', async () => {
    await open()
    const user = userEvent.setup()
    await user.type(screen.getByRole('combobox', { name: 'Message the group' }), 'hello group')
    await user.click(screen.getByRole('button', { name: /^send/i }))
    const writes = net.writes()
    expect(writes.some((write) => write.path.includes('/sessions') && write.method === 'POST'), 'S-108: no chat is created').toBe(false)
    expect(writes.some((write) => write.path.endsWith('/messages') && write.method === 'POST')).toBe(true)
  })
})
