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
// Independent literal oracle from D3/W14, never obtained by running the app.
const TEMPLATE = "I couldn't continue this delegation in {cli}: its previous conversation can't be resumed. Start a new delegation."
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

it('R4-RED-10: generated catalogue and enum contain the exact W14 form and ambiguous attribution', () => {
  expect(llmErrorCodes, 'D3: BLOCKED until the contract and generated code enum include B16a').toContain(CODE)
  expect(Reflect.get(llmErrorUserMessages, CODE), 'D3: exact W14 template must be generated, not a test-authored replacement map').toBe(TEMPLATE)
  expect(Reflect.get(llmErrorUserAttributions, CODE)).toBe('ambiguous')
})

// The baseline generated union lacks the required code. Keep the production
// generated wire types and cast ONLY that future enum member at the injection
// edge; the catalogue/enum test above fails loudly until regeneration. No
// handwritten wire type or generated-file edit hides the missing implementation.
function payload(sentence: string): LLMError {
  return { code: CODE as LLMError['code'], message: sentence, retryable: false, provider_message: true }
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
