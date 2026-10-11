// U8 participant labels — pure helper contract (session-core F15/F17).
//
// Design of record:
//   - coordination/squads/session-core-build-20261008/ARCHITECT-ANSWER-PARTICIPANT.md
//     section D4 (render table) — the label rules this module implements.
//   - Founder ruling F17 (coordination/FOUNDER-OPEN-ITEMS.md): own messages
//     carry NO label; every left-side message carries one ("Alice · Telegram");
//     a reply shows "Ava → Alice · Telegram"; two bots on one platform show
//     the platform only; display_name is PLAIN TEXT, ellipsis ~40 chars with
//     the full text in a tooltip.
//
// These are the oracle side of the render tests in
// participantLabel.render.test.tsx — the same expectations, asserted against
// the pure functions so a failure names the rule, not a render tree.

import { describe, expect, it } from 'vitest'
import type { ChatParticipant } from '@/lib/api/generated/asyncapi-types'
import {
  PARTICIPANT_LABEL_MAX_CHARS,
  collectPairedReplyToIds,
  isSendChipHiddenByReply,
  participantLabel,
  replyHeaderParts,
  sourceLabel,
  truncateParticipantLabel,
} from './participantLabel'

const AGENTS = [
  { id: 'agent-ava', name: 'Ava' },
  { id: 'agent-mia', name: 'Mia' },
]
const WORKSPACES = [
  { id: 'ws-main', name: 'Main' },
  { id: 'ws-atlas', name: 'Atlas' },
]

function human(name: string, source?: string): ChatParticipant {
  return { kind: 'human', display_name: name, ...(source ? { source } : {}) }
}

function agentParticipant(agentId: string, workspaceId: string, displayName: string): ChatParticipant {
  return { kind: 'agent', display_name: displayName, agent: { agent_id: agentId, workspace_id: workspaceId } }
}

describe('sourceLabel — the source map (D4 row 1)', () => {
  it('maps the known platforms', () => {
    expect(sourceLabel('telegram')).toBe('Telegram')
    expect(sourceLabel('slack')).toBe('Slack')
    expect(sourceLabel('web')).toBe('Web')
  })

  it('title-cases an unknown source and splits hyphenated platforms on word bounds', () => {
    expect(sourceLabel('foo')).toBe('Foo')
    expect(sourceLabel('google-chat')).toBe('Google Chat')
  })

  it('an absent source maps to nothing (no guessing)', () => {
    expect(sourceLabel(undefined)).toBe('')
    expect(sourceLabel('')).toBe('')
  })
})

describe('participantLabel — D4 rows 1-2 (human and agent participants)', () => {
  it('a human participant is "display_name · Source"', () => {
    expect(participantLabel(human('Alice', 'telegram'), AGENTS, 'ws-main', WORKSPACES)).toBe('Alice · Telegram')
    expect(participantLabel(human('Bob', 'slack'), AGENTS, 'ws-main', WORKSPACES)).toBe('Bob · Slack')
  })

  it('a web human reads "Dan · Web"', () => {
    expect(participantLabel(human('Dan', 'web'), AGENTS, 'ws-main', WORKSPACES)).toBe('Dan · Web')
  })

  it('an agent participant resolves the LIVE agent name by agent.agent_id', () => {
    expect(participantLabel(agentParticipant('agent-mia', 'ws-main', 'mia-fallback'), AGENTS, 'ws-main', WORKSPACES)).toBe('Mia')
  })

  it('an agent participant unknown to the catalog falls back to display_name', () => {
    expect(participantLabel(agentParticipant('agent-ghost', 'ws-main', 'Ghost'), AGENTS, 'ws-main', WORKSPACES)).toBe('Ghost')
  })

  it('a cross-workspace agent participant adds the workspace name', () => {
    expect(participantLabel(agentParticipant('agent-mia', 'ws-atlas', 'mia-fallback'), AGENTS, 'ws-main', WORKSPACES)).toBe('Mia · Atlas')
  })

  it('a same-workspace agent participant carries NO workspace suffix', () => {
    expect(participantLabel(agentParticipant('agent-mia', 'ws-main', 'mia-fallback'), AGENTS, 'ws-main', WORKSPACES)).toBe('Mia')
  })

  it('with no current workspace the difference cannot be established — no suffix, no guessing', () => {
    expect(participantLabel(agentParticipant('agent-mia', 'ws-atlas', 'mia-fallback'), AGENTS, null, WORKSPACES)).toBe('Mia')
  })

  it('an unknown workspace id renders as itself (never blank)', () => {
    expect(participantLabel(agentParticipant('agent-mia', 'ws-other', 'mia-fallback'), AGENTS, 'ws-main', WORKSPACES)).toBe('Mia · ws-other')
  })
})

describe('F17 (3)/(4) — two bots on one platform show the platform only', () => {
  it('two humans arriving via two telegram-bound bots both label with just Telegram — no instance disambiguator', () => {
    const labelA = participantLabel(human('Alice', 'telegram'), AGENTS, 'ws-main', WORKSPACES)
    const labelB = participantLabel(human('Bob', 'telegram'), AGENTS, 'ws-main', WORKSPACES)
    expect(labelA).toBe('Alice · Telegram')
    expect(labelB).toBe('Bob · Telegram')
  })
})

describe('truncateParticipantLabel — plain text, ellipsis ~40, full text for the tooltip', () => {
  it('a label within the cap is untouched', () => {
    const label = 'Alice · Telegram'
    expect(truncateParticipantLabel(label)).toEqual({ text: label, full: label, truncated: false })
  })

  it('a long label ellipsizes at the cap and keeps the full text', () => {
    const long = 'A'.repeat(60)
    const t = truncateParticipantLabel(long)
    expect(t.truncated).toBe(true)
    expect(t.full).toBe(long)
    expect(t.text.length).toBe(PARTICIPANT_LABEL_MAX_CHARS)
    expect(t.text.endsWith('…')).toBe(true)
    expect(t.text.startsWith('A'.repeat(PARTICIPANT_LABEL_MAX_CHARS - 1))).toBe(true)
  })
})

describe('replyHeaderParts — D4 rows 5-6 (the reply header)', () => {
  it('a reply to a participant renders "author → recipient label"', () => {
    const parts = replyHeaderParts(
      { agentId: 'agent-ava', replyToMessageId: 'req-1', replyToParticipant: human('Alice', 'telegram') },
      AGENTS, 'ws-main', WORKSPACES,
    )
    expect(parts).not.toBeNull()
    expect(parts?.author).toBe('Ava')
    expect(parts?.target).toBe('Alice · Telegram')
  })

  it('reply_to_message_id WITHOUT reply_to_participant falls back to "reply"', () => {
    const parts = replyHeaderParts(
      { agentId: 'agent-ava', replyToMessageId: 'req-1' },
      AGENTS, 'ws-main', WORKSPACES,
    )
    expect(parts).not.toBeNull()
    expect(parts?.author).toBe('Ava')
    expect(parts?.target).toBe('reply')
  })

  it('a message with no reply fields renders no header at all', () => {
    expect(replyHeaderParts({ agentId: 'agent-ava' }, AGENTS, 'ws-main', WORKSPACES)).toBeNull()
    expect(replyHeaderParts({}, AGENTS, 'ws-main', WORKSPACES)).toBeNull()
  })

  it('the header target truncates with the same rule (full text kept for the tooltip)', () => {
    const longName = 'N'.repeat(60)
    const parts = replyHeaderParts(
      { agentId: 'agent-ava', replyToMessageId: 'req-1', replyToParticipant: human(longName, 'telegram') },
      AGENTS, 'ws-main', WORKSPACES,
    )
    expect(parts?.targetTruncated).toBe(true)
    expect(parts?.targetFull).toContain(longName)
    expect(parts?.target.length).toBe(PARTICIPANT_LABEL_MAX_CHARS)
  })
})

describe('F17 (1) — the send_message chip paired with a reply is hidden', () => {
  it('collectPairedReplyToIds gathers the reply_to ids that have a reply bubble', () => {
    const ids = collectPairedReplyToIds([
      { replyToMessageId: 'req-1' },
      { replyToMessageId: 'req-2' },
      {},
      { replyToMessageId: undefined },
    ])
    expect(ids.has('req-1')).toBe(true)
    expect(ids.has('req-2')).toBe(true)
    expect(ids.size).toBe(2)
  })

  it('a send_message call whose reply_to matches a reply bubble is hidden', () => {
    const paired = collectPairedReplyToIds([{ replyToMessageId: 'req-1' }])
    expect(isSendChipHiddenByReply('send_message', { reply_to: 'req-1', content: 'hi' }, paired)).toBe(true)
  })

  it('an unpaired send_message, another tool, and a call without reply_to stay visible', () => {
    const paired = collectPairedReplyToIds([{ replyToMessageId: 'req-1' }])
    expect(isSendChipHiddenByReply('send_message', { reply_to: 'req-9' }, paired)).toBe(false)
    expect(isSendChipHiddenByReply('bash', { reply_to: 'req-1' }, paired)).toBe(false)
    expect(isSendChipHiddenByReply('send_message', {}, paired)).toBe(false)
    expect(isSendChipHiddenByReply('send_message', { reply_to: 42 } as unknown as Record<string, unknown>, paired)).toBe(false)
    expect(isSendChipHiddenByReply('send_message', undefined, paired)).toBe(false)
  })
})

describe('display_name is plain text — the label helpers never transform it', () => {
  it('markdown-ish display_name passes through verbatim (rendering is the row test below)', () => {
    expect(participantLabel(human('**Alice** <b>X</b>', 'slack'), AGENTS, 'ws-main', WORKSPACES)).toBe('**Alice** <b>X</b> · Slack')
  })
})
