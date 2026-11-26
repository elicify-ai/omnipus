// ARCHITECT round5 F1 and SFH-R5-2. Real reducer + ChatScreen; only the
// existing host/network-edge harness is doubled. No catalogue oracle for
// the uncoded task-chat sentence (approved TEXT.md section 3).
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import { reset, feed, mount, lines } from '../../../tests/helper-model-notices/harness'
import { emptyHistoryFixture } from '../../../tests/chat-history-paging/empty-history'
import { useChatStore, getMessages, makeBucketMessages, type ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { DoneFrame, ErrorFrame, MessageStatusFrame, ReplayErrorFrame, ReplayMessageFrame, SessionStateFrame, TokenFrame } from '@/lib/api/generated/asyncapi-types'
import { llmErrorUserMessages } from '@/lib/api/generated/llm-error-messages'

const SID = 'r5-external-task-or-helper'
const OTHER = 'r5-unrelated-view'
const ANSWER_ID = 'r5-healthy-answer'
const TURN = 'r5-still-finishing-turn'
const REFUSAL_ID = 'r5-saved-task-refusal'
const TASK_REFUSAL = "I couldn't deliver your message: this task runs in OpenCode and doesn't accept chat messages here. To change the work, open Tasks and stop the task or its plan before editing the task."
const ANSWER = 'The external helper gave a healthy answer. Keep every word.'
const STATES = ['empty', 'streaming', 'final'] as const
let stamp: string

type AnswerState = typeof STATES[number]

function emptyBucket() {
  return {
    ...emptyHistoryFixture(), ...makeBucketMessages([]),
    toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {}, isStreaming: false,
    isReplaying: false, replayCompletedForSession: SID, sessionTokens: 0, sessionCost: 0,
    rateLimitEvent: null, cancelStage: null, lastUserMessageAt: null, lastReceivedEventTime: null,
  }
}

function seed(state: AnswerState) {
  const original: ChatMessage = {
    id: ANSWER_ID, role: 'assistant', content: state === 'empty' ? '' : ANSWER,
    timestamp: stamp, agentId: 'researcher', turnId: TURN,
    status: state === 'final' ? 'done' : 'streaming', isStreaming: state !== 'final',
  }
  act(() => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'researcher', attachedSessionType: 'task' })
    useChatStore.setState({
      sessionsById: {
        [SID]: { ...emptyBucket(), ...makeBucketMessages([original]), isStreaming: true, activeTurnId: TURN, activeTurnAgentId: 'researcher' },
        [OTHER]: emptyBucket(),
      },
    })
    useSessionStore.getState().setActiveSession(SID, 'researcher')
  })
  return original
}

function savedTaskRefusal(): ReplayErrorFrame {
  return { type: 'replay_error', session_id: SID, entry_id: REFUSAL_ID, timestamp: stamp,
    kind: 'error', message: TASK_REFUSAL }
}

function liveTaskRefusal(): ErrorFrame {
  return { type: 'error', session_id: SID, client_message_id: 'r5-refused-send-from-view-a', message: TASK_REFUSAL }
}

function assertTaskNoticeAndAnswer(container: HTMLElement, original: ChatMessage) {
  const b = useChatStore.getState().sessionsById[SID]
  expect(b.messagesById[ANSWER_ID], 'F1: a refused task message must not restamp or consume the existing assistant bubble').toMatchObject(original)
  expect(getMessages(b).filter(m => m.content === TASK_REFUSAL), 'F1: one refused input has exactly one distinct visible history row').toHaveLength(1)
  expect(lines(container, TASK_REFUSAL), 'F1: one refusal in actual ChatScreen, not only a hidden announcement').toHaveLength(1)
  expect(lines(container, TASK_REFUSAL)[0]).toBeVisible()
  expect(b.activeTurnId, 'F1: history refusal is not completion of the live task').toBe(TURN)
  expect(b.isStreaming).toBe(true)
  expect(getMessages(useChatStore.getState().sessionsById[OTHER]), 'F1: unrelated view stays uncontaminated').toEqual([])
}

beforeEach(() => {
  reset()
  stamp = new Date().toISOString()
  useChatPreferencesStore.setState({ verboseChatEnabled: false })
})
afterEach(() => {
  reset()
  vi.unstubAllGlobals()
  useChatPreferencesStore.setState({ verboseChatEnabled: false })
})

it.each(STATES)('F1: replayed task refusal stays separate from an %s assistant and deduplicates', async state => {
  const original = seed(state)
  feed(savedTaskRefusal(), savedTaskRefusal())
  let container!: HTMLElement
  await act(async () => { container = mount().container })
  assertTaskNoticeAndAnswer(container, original)
})

// CHECK round6 F1/F2: keep the live refusal in the bucket when its saved copy
// arrives. Navigation/reload reconstruction below does not exercise adoption.
it('F1: saved replay adopts the retained live refusal without navigation or a second row', async () => {
  const original = seed('streaming')
  feed(liveTaskRefusal())
  let container!: HTMLElement
  await act(async () => { container = mount().container })
  assertTaskNoticeAndAnswer(container, original)
  const refusalRows = () => getMessages(useChatStore.getState().sessionsById[SID]).filter(m => m.content === TASK_REFUSAL)
  const liveRefusalIds = refusalRows().map(m => m.id)
  expect(refusalRows().map(m => m.errorEntryId), 'instrument: the retained live row has not received a saved entry id').toEqual([undefined])

  feed(savedTaskRefusal(), savedTaskRefusal())
  assertTaskNoticeAndAnswer(container, original)
  expect(refusalRows().map(m => m.id), 'F1: saved replay adopts the actual live row rather than replacing it').toEqual(liveRefusalIds)
  expect(refusalRows().map(m => m.errorEntryId), 'F2: the adopted row carries its durable saved identity after repeated replay').toEqual([REFUSAL_ID])
})

it.each(['empty', 'streaming'] as const)('F1: after switching away and back, replay shows one refusal and preserves the retained %s answer', async state => {
  const original = seed(state)
  feed(liveTaskRefusal())
  let container!: HTMLElement
  await act(async () => { container = mount().container })
  assertTaskNoticeAndAnswer(container, original) // Instrument: live refusal alone is correctly separate.
  act(() => { useSessionStore.getState().setActiveSession(OTHER, 'writer') })
  expect(useSessionStore.getState().activeSessionId).toBe(OTHER)
  expect(useChatStore.getState().sessionsById[SID].messagesById[ANSWER_ID], 'instrument: live answer is retained across real navigation').toMatchObject(original)
  feed(savedTaskRefusal(), savedTaskRefusal())
  act(() => { useSessionStore.getState().setActiveSession(SID, 'researcher') })
  assertTaskNoticeAndAnswer(container, original)
})

it('F1: reload discards the live store and reconstructs one uncoded refusal beside the saved healthy answer', async () => {
  seed('streaming')
  feed(liveTaskRefusal())
  let rendered!: ReturnType<typeof mount>
  await act(async () => { rendered = mount() })
  expect(lines(rendered.container, TASK_REFUSAL)).toHaveLength(1)
  rendered.unmount()
  reset()
  // A reload has no old messages. The task is still live, and actual history
  // carriers supply the saved answer and the separate refusal (no saved user
  // input intervenes for Q13 B).
  act(() => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'researcher' })
    useChatStore.setState({ sessionsById: { [SID]: emptyBucket(), [OTHER]: emptyBucket() } })
    useSessionStore.getState().setActiveSession(SID, 'researcher')
  })
  expect(getMessages(useChatStore.getState().sessionsById[SID])).toEqual([])
  const active: SessionStateFrame = { type: 'session_state', session_id: SID, user_id: 'r5-person', pending_approvals: [], emitted_at: stamp,
    active_turn: { turn_id: TURN, agent_id: 'researcher', started_at: stamp }, history_state: { status: 'available' } }
  const answer: ReplayMessageFrame = { type: 'replay_message', session_id: SID, id: ANSWER_ID,
    role: 'assistant', content: ANSWER, agent_id: 'researcher', turn_id: TURN, timestamp: stamp }
  feed(active, answer)
  const original = { ...useChatStore.getState().sessionsById[SID].messagesById[ANSWER_ID] }
  expect(original.content, 'instrument: healthy saved answer is actually reconstructed').toBe(ANSWER)
  feed(savedTaskRefusal(), savedTaskRefusal())
  await act(async () => { rendered = mount() })
  assertTaskNoticeAndAnswer(rendered.container, original)
})

// SFH-R5-2 consumer CONTROLS, not a false claim that the old producer emits
// either safe carrier. Agent's producer test supplies RED; gateway's bridge
// test drives the actual translator. No sender optimistic row exists here:
// this is the second view, whose answer is still streaming.
it.each(['system_notice', 'correlated_error'] as const)('SFH-R5-2: %s preserves another view\'s live answer through later completion and fresh history', async carrier => {
  seed('empty')
  // A genuine token event, not only a pre-built finished message, reaches the
  // real consumer before the refused input notice.
  const token: TokenFrame = { type: 'token', session_id: SID, message_id: ANSWER_ID,
    turn_id: TURN, agent_id: 'researcher', content: ANSWER }
  feed(token)
  const sentence = llmErrorUserMessages.delegation_not_resumable.replace('{cli}', 'OpenCode')
  const notice: ReplayMessageFrame = { type: 'replay_message', session_id: SID, role: 'system',
    id: 'r5-helper-refusal', content: sentence, timestamp: stamp }
  const correlated: ErrorFrame = { type: 'error', session_id: SID, client_message_id: 'real-send-in-other-view',
    message: sentence, payload: { llm_error: { code: 'delegation_not_resumable', message: sentence, retryable: false, provider_message: true } } }
  expect(getMessages(useChatStore.getState().sessionsById[SID]).filter(m => m.role === 'user')).toEqual([])
  feed(carrier === 'system_notice' ? notice : correlated)
  const b = useChatStore.getState().sessionsById[SID]
  expect(b.messagesById[ANSWER_ID]).toMatchObject({ content: ANSWER, status: 'streaming', isStreaming: true })
  expect(b.activeTurnId).toBe(TURN)
  expect(b.isStreaming).toBe(true)
  let rendered!: ReturnType<typeof mount>
  await act(async () => { rendered = mount() })
  expect(lines(rendered.container, sentence)).toHaveLength(1)
  const done: DoneFrame = { type: 'done', session_id: SID, message_id: ANSWER_ID, turn_id: TURN }
  feed(done)
  expect(useChatStore.getState().sessionsById[SID].messagesById[ANSWER_ID]).toMatchObject({ content: ANSWER, status: 'done', isStreaming: false })
  expect(lines(rendered.container, ANSWER)).toHaveLength(1)
  expect(lines(rendered.container, sentence)).toHaveLength(1)
  rendered.unmount()
  reset()
  act(() => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'researcher' })
    useChatStore.setState({ sessionsById: { [SID]: emptyBucket() } })
    useSessionStore.getState().setActiveSession(SID, 'researcher')
  })
  const savedAnswer: ReplayMessageFrame = { type: 'replay_message', session_id: SID, id: ANSWER_ID, role: 'assistant',
    content: ANSWER, timestamp: stamp, agent_id: 'researcher', turn_id: TURN }
  const savedRefusal: ReplayErrorFrame = { type: 'replay_error', session_id: SID, entry_id: 'r5-helper-refusal', timestamp: stamp,
    kind: 'error', message: sentence, payload: { llm_error: { code: 'delegation_not_resumable', message: sentence, retryable: false, provider_message: true } } }
  feed(savedAnswer, savedRefusal, savedRefusal)
  await act(async () => { rendered = mount() })
  expect(lines(rendered.container, ANSWER)).toHaveLength(1)
  expect(lines(rendered.container, sentence)).toHaveLength(1)
  expect(useChatStore.getState().sessionsById[SID].messagesById[ANSWER_ID]).toMatchObject({ content: ANSWER, status: 'done' })
})

// C2 Part 1, Server/SPA behaviour: the displayed sender row is failed by
// message_status; the refusal does not turn the concurrent answer into error.
it('C2: refused helper follow-up marks only its sender row failed and preserves the live answer', async () => {
  const original = seed('streaming')
  const clientId = 'r5-helper-sender-row'
  const human: ChatMessage = { id: clientId, role: 'user', content: 'This follow-up is refused.', timestamp: stamp, status: 'done', deliveryStatus: 'received' }
  act(() => {
    const bucket = useChatStore.getState().sessionsById[SID]
    useChatStore.setState({ sessionsById: { ...useChatStore.getState().sessionsById, [SID]: { ...bucket, ...makeBucketMessages([...getMessages(bucket), human]) } } })
  })
  const sentence = llmErrorUserMessages.delegation_not_resumable.replace('{cli}', 'OpenCode')
  const refusal: ErrorFrame = { type: 'error', session_id: SID, client_message_id: clientId, message: sentence,
    payload: { llm_error: { code: 'delegation_not_resumable', message: sentence, retryable: false, provider_message: true } } }
  const failed: MessageStatusFrame = { type: 'message_status', session_id: SID, client_message_id: clientId, state: 'failed' }
  feed(refusal, failed)
  let rendered!: ReturnType<typeof mount>
  await act(async () => { rendered = mount() })
  const bucket = useChatStore.getState().sessionsById[SID]
  expect(bucket.messagesById[clientId]).toMatchObject({ ...human, deliveryStatus: 'failed', status: 'error' })
  expect(bucket.messagesById[ANSWER_ID]).toMatchObject(original)
  expect(bucket.activeTurnId).toBe(TURN)
  expect(bucket.isStreaming).toBe(true)
  expect(lines(rendered.container, sentence)).toHaveLength(1)
  const done: DoneFrame = { type: 'done', session_id: SID, message_id: ANSWER_ID, turn_id: TURN }
  feed(done)
  expect(useChatStore.getState().sessionsById[SID].messagesById[clientId]).toMatchObject({ content: human.content, deliveryStatus: 'failed', status: 'error' })
  expect(useChatStore.getState().sessionsById[SID].messagesById[ANSWER_ID]).toMatchObject({ content: ANSWER, status: 'done' })
})
