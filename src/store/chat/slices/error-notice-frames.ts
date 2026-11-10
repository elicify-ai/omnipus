import { generateId } from '@/lib/constants'
import {
  getLLMErrorDisplay,
  readEntryIdFromFrame,
  readLLMErrorFromFrame,
  readLLMErrorFromReplayFrame,
  type LLMError,
} from '@/lib/llm-error'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useConnectionStore } from '@/store/connection'
import { getMessages } from '../messages'
import { applyMessageArray } from '../session'
import type { ChatMessage, ChatStore, SessionChatState } from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]
type WithBucket = (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void

/** Append a non-terminal error notice without changing the reply or turn state. */
export function appendErrorNotice(
  bucket: SessionChatState,
  message: string,
  llmError: LLMError | undefined,
  entryId: string | undefined,
  detail: string | undefined,
): Partial<SessionChatState> {
  const messages = getMessages(bucket)
  if (entryId && messages.some((m) => m.errorEntryId === entryId)) return {}
  const notice: ChatMessage = {
    id: generateId(),
    role: 'assistant',
    content: message,
    timestamp: new Date().toISOString(),
    status: 'error',
    isStreaming: false,
    refusalNotice: true,
    ...(llmError
      ? {
          errorCode: llmError.code,
          ...(detail !== undefined ? { errorDetail: detail } : {}),
          ...(entryId ? { errorEntryId: entryId } : {}),
          ...(llmError.facts ? { errorFacts: llmError.facts } : {}),
        }
      : {}),
  }
  return applyMessageArray([...messages, notice], bucket)
}

/** Conclusion handling is an operational notice, never evidence that a turn ended. */
export function handleJudgeConclusionErrorFrame(frame: Frame, withBucket: WithBucket): boolean {
  if (frame.type !== 'error' && frame.type !== 'replay_error') return false
  const llmError = frame.type === 'error'
    ? readLLMErrorFromFrame(frame)
    : readLLMErrorFromReplayFrame(frame)
  if (llmError?.code !== 'judge_conclusion_failed') return false
  const { message, detail } = getLLMErrorDisplay(
    llmError,
    useChatPreferencesStore.getState().verboseChatEnabled,
  )
  // Never guess another session or invoke the global terminal-error sweep.
  if (!frame.session_id) {
    useConnectionStore.getState().setConnectionError(message)
    return true
  }
  const entryId = readEntryIdFromFrame(frame)
  withBucket(frame.session_id, (bucket) => appendErrorNotice(bucket, message, llmError, entryId, detail))
  return true
}
