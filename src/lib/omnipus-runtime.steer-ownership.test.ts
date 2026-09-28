/**
 * omnipus-runtime.steer-ownership.test.ts — founder-reported hotfix
 * (2026-09-26): "tool rows vanish when a message is sent mid-turn".
 *
 * Evidence pack: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-evidence/steer-tools-2026-09-26/
 *
 * Covers the second half of the root cause: buildContentParts attached ALL
 * live (not-yet-baked) toolCallOrder entries to the NEWEST assistant bubble
 * with no ownership check. After a mid-turn steer, the pre-steer bubble's
 * still-running calls therefore re-appeared — duplicated and stuck — under
 * the post-steer bubble the moment a new tool_call_start arrived, until
 * turn end wiped them. The fix filters liveIds through
 * toolCallOwnerMessageId (stamped per call at tool_call_start): a live call
 * attaches to the last assistant only when the map has NO entry for it
 * (legacy calls that predate ownership tracking) or names THAT message.
 *
 * Approach mirrors omnipus-runtime.issue-617-isError.test.ts: drive
 * `convertMessage` directly (pure function), not the mounted hook.
 */

import { describe, it, expect } from 'vitest'
import { convertMessage } from './omnipus-runtime'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import type { ToolCall } from '@/lib/api'

type StoreToolCall = ToolCall & { call_id: string }

function makeAssistantMsg(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: 'a1',
    role: 'assistant',
    content: '',
    timestamp: '2026-09-26T10:00:01Z',
    status: 'done',
    ...overrides,
  } as ChatMessage
}

function liveTC(id: string, status: 'running' | 'success' = 'running'): StoreToolCall {
  return { id, call_id: id, tool: 'bash', params: {}, status }
}

/** All tool-call part ids in a convertMessage() output, in render order. */
function toolCallIds(content: unknown): string[] {
  const parts = content as Array<{ type: string; toolCallId?: string }>
  return parts.filter((p) => p.type === 'tool-call').map((p) => p.toolCallId!)
}

describe('convertMessage — live tool-call ownership after a mid-turn steer (founder-reported 2026-09-26)', () => {
  it('the NEW assistant bubble attaches ONLY its own live calls, not the previous bubble’s', () => {
    // Post-steer store state: m1 (steered-closed) still owns tc1/tc2/tc3 in
    // toolCallOrder (bake-in-place keeps the live entries); a new
    // tool_call_start minted m2 and stamped tc9 onto it. m2 is the last
    // assistant — this is the render that previously duplicated all four
    // calls onto it.
    const m2 = makeAssistantMsg({ id: 'm2', content: '', tool_calls: undefined })
    const toolCalls: Record<string, StoreToolCall> = {
      tc1: liveTC('tc1', 'success'),
      tc2: liveTC('tc2', 'success'),
      tc3: liveTC('tc3'),
      tc9: liveTC('tc9'),
    }
    const owners = { tc1: 'm1', tc2: 'm1', tc3: 'm1', tc9: 'm2' }
    const out = convertMessage(m2, toolCalls, ['tc1', 'tc2', 'tc3', 'tc9'], {}, true, owners)
    expect(toolCallIds(out.content)).toEqual(['tc9'])
  })

  it('the PREVIOUS bubble still renders its own baked rows via the history path (unchanged)', () => {
    const m1 = makeAssistantMsg({
      id: 'm1',
      content: 'Let me look.',
      tool_calls: [
        { id: 'tc1', tool: 'bash', params: {}, status: 'success' } as PositionedToolCall,
      ],
    })
    const owners = { tc1: 'm1', tc9: 'm2' }
    const toolCalls: Record<string, StoreToolCall> = { tc1: liveTC('tc1', 'success'), tc9: liveTC('tc9') }
    // isLastAssistant=false: history path — no live attach at all, baked row
    // resolves through the live map.
    const out = convertMessage(m1, toolCalls, ['tc9'], {}, false, owners)
    expect(toolCallIds(out.content)).toEqual(['tc1'])
  })

  it('an OWNERLESS live call still attaches to the last assistant (legacy fallback unchanged)', () => {
    const a1 = makeAssistantMsg({ id: 'a1', tool_calls: undefined })
    const toolCalls: Record<string, StoreToolCall> = { tc_old: liveTC('tc_old') }
    const out = convertMessage(a1, toolCalls, ['tc_old'], {}, true, {})
    expect(toolCallIds(out.content)).toEqual(['tc_old'])
  })

  it('a live call owned by THIS message still attaches (normal live turn unchanged)', () => {
    const a1 = makeAssistantMsg({ id: 'a1', tool_calls: undefined })
    const toolCalls: Record<string, StoreToolCall> = { tc_own: liveTC('tc_own') }
    const out = convertMessage(a1, toolCalls, ['tc_own'], {}, true, { tc_own: 'a1' })
    expect(toolCallIds(out.content)).toEqual(['tc_own'])
  })
})
