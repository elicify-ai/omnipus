// A replayed terminal outcome (replay_message.terminal_outcome === true) is its
// own message: reload must render it as a distinct bubble that keeps the
// frame's durable id, matching what the live path shows (live closes the
// narration bubble with `done` before the terminal notice streams).
//
// Oracle: contracts/components/schemas/ReplayMessageFrame.yaml, field
// `terminal_outcome`, and the e2e terminal-outcome spec ("one distinct durable
// terminal entry"; rendered data-message-id equals the durable entry id).
// Frames WITHOUT the field keep the same-turn merge (control test).

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'

const SID = 'sess-terminal-outcome'
const TURN = 'turn-1'
const AGENT = 'agent-ray'

function replay(id: string, content: string, extra: Record<string, unknown> = {}) {
  act(() => {
    useChatStore.getState().handleFrame({
      type: 'replay_message', role: 'assistant', content, id,
      agent_id: AGENT, turn_id: TURN, session_id: SID, ...extra,
    } as never)
  })
}

const assistants = () => useChatStore.getState().messages.filter((m) => m.role === 'assistant')

describe('replay_message terminal_outcome', () => {
  beforeEach(() => {
    useSessionStore.setState({ ...useSessionStore.getState(), activeSessionId: SID })
    act(() => { useChatStore.getState().resetSession() })
  })

  it('a terminal_outcome entry after same-turn narration stays a separate message with its own id', () => {
    replay('entry-narration', 'I will load the tool.')
    replay('entry-terminal', "I've reached this agent's limit.", { terminal_outcome: true })

    const list = assistants()
    expect(list.map((m) => m.id)).toEqual(['entry-narration', 'entry-terminal'])
    expect(list.map((m) => m.content)).toEqual(['I will load the tool.', "I've reached this agent's limit."])
  })

  it('control: the same pair without the field is merged into the first bubble, as before', () => {
    replay('entry-narration', 'I will load the tool.')
    replay('entry-terminal', "I've reached this agent's limit.")

    const list = assistants()
    expect(list.map((m) => m.id)).toEqual(['entry-narration'])
    expect(list[0].content).toBe("I will load the tool.\n\nI've reached this agent's limit.")
  })

  it('terminal_outcome: false behaves like an absent field', () => {
    replay('entry-narration', 'A')
    replay('entry-next', 'B', { terminal_outcome: false })
    expect(assistants().map((m) => m.id)).toEqual(['entry-narration'])
  })
})
