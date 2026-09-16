import type { StoreApi } from 'zustand'
import { useConnectionStore } from '@/store/connection'
import type { ChatStore } from '../types'

type OutboundResponseSlice = Pick<ChatStore, 'sendAskUserAnswer' | 'respondToPairing'>

export function createOutboundResponseSlice(
  _set: StoreApi<ChatStore>['setState'],
  _get: StoreApi<ChatStore>['getState'],
): OutboundResponseSlice {
  return {
    sendAskUserAnswer: (answer) => {
      const { connection } = useConnectionStore.getState()
      if (!connection) {
        useConnectionStore.getState().setConnectionError('Cannot send your answers — not connected. Reconnect and try again.')
        return
      }
      const sent = connection.send({ type: 'ask_user_answer', ...answer })
      if (!sent) {
        useConnectionStore.getState().setConnectionError('Failed to send your answers — connection dropped. Reconnect and try again.')
      }
    },

    respondToPairing: (deviceId, decision) => {
      const { connection } = useConnectionStore.getState()
      if (!connection) {
        useConnectionStore.getState().setConnectionError('Cannot respond to pairing — not connected. Reconnect and try again.')
        return
      }

      const sent = connection.send({ type: 'device_pairing_response', device_id: deviceId, decision })
      if (!sent) {
        useConnectionStore.getState().setConnectionError('Failed to send pairing response — connection dropped. Reconnect and try again.')
      }
    },
  }
}
