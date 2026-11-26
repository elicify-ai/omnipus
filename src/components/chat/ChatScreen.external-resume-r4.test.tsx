// R4-RED-10 / D2-D4, ARCHITECT-RULING-R4-goals.md §2.
// Real frame reducer and ChatScreen (historical/error row included). Reuse the
// existing host/network-edge harness; do not mock the reducer or error renderer.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import { reset, feed, mount, lines } from '../../../tests/helper-model-notices/harness'
import { emptyHistoryFixture } from '../../../tests/chat-history-paging/empty-history'
import { useChatStore, getMessages, makeBucketMessages } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { ErrorFrame, ReplayErrorFrame, LLMError } from '@/lib/api/generated/asyncapi-types'
import { llmErrorCodes, llmErrorUserMessages, llmErrorUserAttributions } from '@/lib/api/generated/llm-error-messages'

const SID = 'r4-external-helper-session'
const PARENT = 'r4-parent-session'
const ENTRY = 'r4-saved-classified-refusal'
const AT = '2026-11-22T12:00:00.000Z'
const CODE = 'delegation_not_resumable'
// The generated W14 catalogue is the contract (team-lead ruling 2026-11-23).
// Code, CLI names, attribution and routing/cardinality oracles stay independent.
const TEMPLATE = llmErrorUserMessages[CODE]
const CLIS = ['Claude Code', 'Codex', 'OpenCode'] as const

function emptyBucket() {
  return {
    ...emptyHistoryFixture(), ...makeBucketMessages([]),
    toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {}, isStreaming: false,
    isReplaying: false, replayCompletedForSession: SID, sessionTokens: 0,
    sessionCost: 0, rateLimitEvent: null, cancelStage: null,
    lastUserMessageAt: null, lastReceivedEventTime: null,
  }
}

function seedHelper() {
  act(() => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'researcher' })
    useChatStore.setState({
      sessionsById: { [SID]: emptyBucket(), [PARENT]: emptyBucket() },
      messages: [], toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {},
      isStreaming: false, isReplaying: false, replayCompletedForSession: SID,
    })
  })
}

beforeEach(() => {
  reset()
  useChatPreferencesStore.setState({ verboseChatEnabled: false })
  seedHelper()
})
afterEach(() => {
  reset()
  vi.unstubAllGlobals()
  useChatPreferencesStore.setState({ verboseChatEnabled: false })
})

it('R4-RED-10: generated contract declares the refusal code, CLI slot and ambiguous attribution', () => {
  expect(llmErrorCodes, 'D3: the generated enum must include B16a').toContain(CODE)
  expect(TEMPLATE.trim(), 'D3: the contract sentence must not be empty').not.toBe('')
  expect([...TEMPLATE.matchAll(/\{([^{}]+)\}/g)].map(match => match[1]), 'D3: W14 has exactly the CLI slot').toEqual(['cli'])
  expect(llmErrorUserAttributions[CODE]).toBe('ambiguous')
})

// Inject through the generated wire type; regeneration now supplies the code.
// Do not cast away a missing enum member or introduce a handwritten wire type.
function payload(sentence: string): LLMError {
  return { code: CODE, message: sentence, retryable: false, provider_message: true }
}

function assertOwnRefusal(container: HTMLElement, sentence: string) {
  const bucket = useChatStore.getState().sessionsById[SID]
  const messages = getMessages(bucket)
  expect(messages).toHaveLength(1)
  expect(messages[0]).toMatchObject({ role: 'assistant', status: 'error', content: sentence, errorCode: CODE })
  expect(getMessages(useChatStore.getState().sessionsById[PARENT]), 'D2: the helper-addressed refusal must not leak into the parent bucket').toEqual([])
  const visible = lines(container, sentence)
  expect(visible, 'D2: exactly one visible refusal in the actual helper ChatScreen').toHaveLength(1)
  expect(visible[0]).toBeVisible()
  expect(container.textContent).not.toContain('{cli}')
  expect(container.textContent).not.toContain('Technical details')
}

it.each(CLIS)('R4-RED-10: live helper-addressed %s refusal renders once, not in the parent', async cli => {
  const sentence = TEMPLATE.replace('{cli}', cli)
  const frame: ErrorFrame = { type: 'error', session_id: SID, message: sentence, payload: { llm_error: payload(sentence) } }
  feed(frame)
  let container!: HTMLElement
  await act(async () => { container = mount().container })
  assertOwnRefusal(container, sentence)
})

it.each(CLIS)('R4-RED-10: saved %s refusal survives a fresh store/reload and replay deduplicates it', async cli => {
  const sentence = TEMPLATE.replace('{cli}', cli)
  const live: ErrorFrame = { type: 'error', session_id: SID, message: sentence, payload: { llm_error: payload(sentence) } }
  feed(live)
  let rendered!: ReturnType<typeof mount>
  await act(async () => { rendered = mount() })
  assertOwnRefusal(rendered.container, sentence)
  rendered.unmount()
  // Model a browser reload, not just delivering replay into an existing bubble.
  reset()
  seedHelper()
  expect(getMessages(useChatStore.getState().sessionsById[SID]), 'instrument: old live messages really were discarded').toEqual([])
  const saved: ReplayErrorFrame = {
    type: 'replay_error', session_id: SID, entry_id: ENTRY, timestamp: AT,
    kind: 'error', message: sentence, payload: { llm_error: payload(sentence) },
  }
  feed(saved, saved)
  await act(async () => { rendered = mount() })
  assertOwnRefusal(rendered.container, sentence)
  expect(getMessages(useChatStore.getState().sessionsById[SID])[0].errorEntryId).toBe(ENTRY)
})
