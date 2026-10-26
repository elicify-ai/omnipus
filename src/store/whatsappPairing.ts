import { create } from 'zustand'

import type { WhatsAppPairingFrame } from '@/lib/api/generated/asyncapi-types'

// Per-channel WhatsApp linked-device pairing state (#283). Fed by the
// whatsapp_pairing WS frame (see chatStore.handleFrame) and read by the
// Channels config panel to render the QR + live status in-browser, replacing the
// old "check the gateway terminal" notice. The same state also feeds the QR in
// the requesting chat (K10, FR-081): a chat-started pairing's frames carry the
// request_id/session_id of the connect_channel call, kept here so a chat card
// shows only its own pairing.

export type WhatsAppPairingState = {
  status: WhatsAppPairingFrame['status']
  /** QR payload to render; present only when status === 'code'. */
  qr: string
  message: string
  /** K10: the chat request that started this pairing; absent for a Connectors-started one. */
  requestId?: string
  /** K10: the chat session of that request. */
  sessionId?: string
}

type WhatsAppPairingStore = {
  /** Keyed by channel_id (e.g. "whatsapp_native"). */
  byChannel: Record<string, WhatsAppPairingState>
  /** Apply an incoming whatsapp_pairing frame. */
  apply: (frame: WhatsAppPairingFrame) => void
  /** Clear a channel's pairing state (e.g. when the config panel closes). */
  clear: (channelId: string) => void
}

export const useWhatsAppPairingStore = create<WhatsAppPairingStore>((set) => ({
  byChannel: {},
  apply: (frame) =>
    set((s) => ({
      byChannel: {
        ...s.byChannel,
        [frame.channel_id]: {
          status: frame.status,
          qr: frame.qr ?? '',
          message: frame.message ?? '',
          ...(frame.request_id ? { requestId: frame.request_id } : {}),
          ...(frame.session_id ? { sessionId: frame.session_id } : {}),
        },
      },
    })),
  clear: (channelId) =>
    set((s) => {
      if (!(channelId in s.byChannel)) return s
      const next = { ...s.byChannel }
      delete next[channelId]
      return { byChannel: next }
    }),
}))
