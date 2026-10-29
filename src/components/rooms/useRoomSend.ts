// useRoomSend — posting a user message into a group chat
// (omnipus-system-agent K26; FR-164, FR-205, FR-216; §6.3, §6.13).
//
// One request at a time. The client_message_id is minted once per message:
// Retry after an uncertain save re-posts the IDENTICAL request, so a message
// the server already recorded is never recorded twice (FR-216). A refusal is
// the server's own reason (FR-204/205, §6.13), shown as written.

import { useCallback, useState } from 'react'
import type { RoomMessageRequest } from '@/lib/api/generated/openapi-types'
import { postRoomMessage } from '@/lib/api/rooms'
import { generateId } from '@/lib/constants'
import { classifyMutationFailure, mutationFailureMessage } from '@/lib/mutationOutcome'

export type RoomSendState = // not-wire-format: in-browser outcome of one room message post
  | { status: 'idle' }
  | { status: 'sending'; request: RoomMessageRequest }
  | { status: 'uncertain'; request: RoomMessageRequest }
  | { status: 'refused'; request: RoomMessageRequest; message: string }

export interface RoomSend {
  state: RoomSendState
  /**
   * Resolves true when the text may leave the composer (accepted or not confirmed).
   * #1449 US-3: `media` carries the message's workspace media refs (at most 16).
   */
  send: (text: string, mentions: string[], media?: string[]) => Promise<boolean>
  retry: () => void
  dismiss: () => void
}

export function useRoomSend(roomId: string): RoomSend {
  const [state, setState] = useState<RoomSendState>({ status: 'idle' })

  const post = useCallback(async (request: RoomMessageRequest): Promise<boolean> => {
    setState({ status: 'sending', request })
    try {
      await postRoomMessage(roomId, request)
      setState({ status: 'idle' })
      return true
    } catch (error) {
      if (classifyMutationFailure(error) === 'uncertain') {
        setState({ status: 'uncertain', request })
        return true
      }
      setState({ status: 'refused', request, message: mutationFailureMessage(error) })
      return false
    }
  }, [roomId])

  const send = useCallback(
    (text: string, mentions: string[], media: string[] = []) =>
      post({ client_message_id: generateId(), text, mentions, ...(media.length > 0 ? { media } : {}) }),
    [post],
  )

  const retry = useCallback(() => {
    if (state.status === 'uncertain') void post(state.request)
  }, [post, state])

  const dismiss = useCallback(() => setState({ status: 'idle' }), [])

  return { state, send, retry, dismiss }
}
