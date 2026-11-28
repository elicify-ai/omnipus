import { act } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'
import { emptyHistoryFixture } from '../../tests/chat-history-paging/empty-history'
import type { LLMError } from '@/lib/api/generated/asyncapi-types'
import { codeToDisplay } from '@/lib/llm-error'
import type { ServerFrame } from '@/lib/ws'
import { getMessages, makeBucketMessages, useChatStore } from './chat'
import { useChatPreferencesStore } from './chatPreferences'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'

// #1968 M1, FR-3/S8 and §5.6 C1. This is the receiving-half GUARD of the
// existing curated-message contract. The paired Go regression drives a real
// refusal and WireLLMError, and requires this flag both live and persisted.
// Do not "fix" C1 by trusting arbitrary unflagged provider messages.
const SID = 'pdf-fix5-catalog-session'
const MODEL = 'fx/specific-pdf-reader'
const C1 = `PDF not sent: the model list is not loaded, so Omnipus cannot check whether ${MODEL} reads PDFs. Restart Omnipus; if this persists, check the "catalog" error in the log.`

beforeEach(() => {
  act(() => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
    useChatStore.setState({
      sessionsById: {
        [SID]: {
          ...emptyHistoryFixture(), ...makeBucketMessages([]),
          toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {},
          isStreaming: false, isReplaying: false, replayCompletedForSession: null,
          sessionTokens: 0, sessionCost: 0, rateLimitEvent: null,
          cancelStage: null, lastUserMessageAt: null, lastReceivedEventTime: null,
        },
      },
      messages: [], toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {},
      isStreaming: false, isReplaying: false, replayCompletedForSession: null,
      sessionTokens: 0, sessionCost: 0, rateLimitEvent: null, lastUserMessageAt: null,
    })
  })
})

function deliver(mode: 'live' | 'replay', le: LLMError): void {
  // Contract-generated payload; frame cast follows the existing reducer
  // fixtures for the live entry_id extension. No parallel wire interface.
  const frame = mode === 'live'
    ? {
        type: 'error', session_id: SID, entry_id: 'pdf-fix5-c1',
        message: le.message, payload: { llm_error: le },
      }
    : {
        type: 'replay_error', session_id: SID, entry_id: 'pdf-fix5-c1',
        timestamp: '2026-11-22T00:00:00Z', kind: 'error',
        message: le.message, payload: { llm_error: le },
      }
  act(() => { useChatStore.getState().handleFrame(frame as ServerFrame) })
}

describe('PDF C1 model-specific bubble and trust boundary', () => {
  for (const mode of ['live', 'replay'] as const) {
    it(`${mode}: controller-authored C1 displays the actual model without verbose mode`, () => {
      deliver(mode, {
        code: 'model_list_unavailable', message: C1, retryable: false, provider_message: true,
      })
      const messages = getMessages(useChatStore.getState().sessionsById[SID])
      expect(messages).toHaveLength(1)
      expect(messages[0].status).toBe('error')
      expect(messages[0].errorCode).toBe('model_list_unavailable')
      expect(messages[0].content).toBe(C1)
      expect(messages[0].content).toContain(MODEL)
      expect(messages[0].content).not.toContain('whether this model')
      expect(messages[0].errorDetail).toBeUndefined()
    })

    it(`${mode}: C1 code alone never grants trust to raw diagnostic text`, () => {
      const raw = 'catalog internal-path=/private/operator.pdf raw-provider-debug'
      deliver(mode, { code: 'model_list_unavailable', message: raw, retryable: false })
      const messages = getMessages(useChatStore.getState().sessionsById[SID])
      expect(messages).toHaveLength(1)
      expect(messages[0].content).toBe(codeToDisplay.model_list_unavailable)
      expect(messages[0].content).not.toContain(raw)
      expect(messages[0].errorDetail).toBeUndefined()
    })
  }
})
