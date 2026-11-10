// recipientAddress.test.ts — send-time @-recipient derivation (founder
// decision 2026-10-11 Q1 B: the @ agent menu is restored; selecting an agent
// addresses the MESSAGE, it never switches the chat — FR-029). The recipient
// is derived from the composer text AT SEND TIME and is not sticky: the
// leading "@<Agent name>" prefix must match a candidate agent EXACTLY, or no
// recipient goes on the wire.
//
// The module under test is pure: every input (text, agent list, active ids)
// is passed in, nothing is read from stores or the query cache here — the
// store-side wiring that feeds it is covered by src/store/chat.recipient-send.test.ts.

import { describe, it, expect } from 'vitest'
import { parseAddressedAgent, resolveSendRecipient } from './recipientAddress'
import { makeAgent } from '@/test/factories'

const candidates = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active' }),
  makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle' }),
  makeAgent({ id: 'mia-smith', name: 'Mia Smith', type: 'Main', status: 'active' }),
]

describe('parseAddressedAgent — leading-@ exact-name parse', () => {
  it('parses the leading "@<Name> " prefix to the matching candidate', () => {
    expect(parseAddressedAgent('@Mia review this', candidates)).toBe(candidates[0])
  })

  it('matches a prefix that is the whole content (trailing space deleted before send)', () => {
    expect(parseAddressedAgent('@Mia', candidates)).toBe(candidates[0])
  })

  it('accepts a newline right after the name (Shift+Enter multiline draft)', () => {
    expect(parseAddressedAgent('@Mia\nplease review', candidates)).toBe(candidates[0])
  })

  it('prefers the LONGEST matching name when one candidate name prefixes another', () => {
    expect(parseAddressedAgent('@Mia Smith hello', candidates)).toBe(candidates[2])
    expect(parseAddressedAgent('@Mia hello', candidates)).toBe(candidates[0])
  })

  it('returns null for a non-leading @ ("hello @x" is ordinary text)', () => {
    expect(parseAddressedAgent('hello @Mia', candidates)).toBeNull()
  })

  it('returns null for a PARTIAL name ("@M" matches nothing exactly)', () => {
    expect(parseAddressedAgent('@M review this', candidates)).toBeNull()
    expect(parseAddressedAgent('@Mi review this', candidates)).toBeNull()
  })

  it('returns null when the name was EDITED before sending (recipient is not sticky)', () => {
    expect(parseAddressedAgent('@Miax review this', candidates)).toBeNull()
    expect(parseAddressedAgent('@mia review this', candidates)).toBeNull() // case edit is an edit
    expect(parseAddressedAgent('@Mi a review this', candidates)).toBeNull()
  })

  it('returns null when the text does not start with @ at all', () => {
    expect(parseAddressedAgent('review this', candidates)).toBeNull()
    expect(parseAddressedAgent('', candidates)).toBeNull()
  })

  it('returns null with no candidates', () => {
    expect(parseAddressedAgent('@Mia hi', [])).toBeNull()
  })

  it('matches a name containing regex metacharacters (the name is matched literally)', () => {
    const tricky = [makeAgent({ id: 'c-plus', name: 'C++ (bot)', type: 'Main', status: 'active' })]
    expect(parseAddressedAgent('@C++ (bot) hello', tricky)).toBe(tricky[0])
    expect(parseAddressedAgent('@C++ (botx) hello', tricky)).toBeNull()
  })
})

describe('resolveSendRecipient — send-side eligibility + wire pair', () => {
  const workspaceId = 'ws-1'

  it('builds the generated MessageFrame.recipient pair for an eligible candidate', () => {
    expect(
      resolveSendRecipient({
        content: '@Jim do a review',
        agents: candidates,
        activeAgentId: 'mia',
        activeWorkspaceId: workspaceId,
      }),
    ).toEqual({ workspace_id: workspaceId, agent_id: 'jim' })
  })

  it('excludes the agent that owns the current chat (addressing yourself is meaningless)', () => {
    expect(
      resolveSendRecipient({
        content: '@Mia do a review',
        agents: candidates,
        activeAgentId: 'mia',
        activeWorkspaceId: workspaceId,
      }),
    ).toBeNull()
  })

  it('excludes workers and non-chat-eligible agents even when the name matches exactly', () => {
    const agents = [
      ...candidates,
      makeAgent({ id: 'max', name: 'Max Worker', type: 'Subagent', status: 'active' }),
      makeAgent({ id: 'judge', name: 'The Judge', type: 'system', status: 'active' }),
    ]
    expect(
      resolveSendRecipient({ content: '@Max Worker do it', agents, activeAgentId: 'mia', activeWorkspaceId: workspaceId }),
    ).toBeNull()
    expect(
      resolveSendRecipient({ content: '@The Judge rule', agents, activeAgentId: 'mia', activeWorkspaceId: workspaceId }),
    ).toBeNull()
  })

  it('returns null when there is no active workspace to address into', () => {
    expect(
      resolveSendRecipient({
        content: '@Jim do a review',
        agents: candidates,
        activeAgentId: 'mia',
        activeWorkspaceId: null,
      }),
    ).toBeNull()
  })

  it('returns null when the text carries no leading @-address', () => {
    expect(
      resolveSendRecipient({
        content: 'plain message about @Mia',
        agents: candidates,
        activeAgentId: 'mia',
        activeWorkspaceId: workspaceId,
      }),
    ).toBeNull()
  })
})
