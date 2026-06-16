import { create } from 'zustand'

import type { WhatsAppPairingFrame } from '@/lib/api/generated/asyncapi-types'

// Per-channel WhatsApp linked-device pairing state (#283). Fed by the
// whatsapp_pairing WS frame (see chatStore.handleFrame) and read by the
// Channels config panel to render the QR + live status in-browser, replacing the
// old "check the gateway terminal" notice.

export type WhatsAppPairingState = {
  status: WhatsAppPairingFrame['status']
  /** QR payload to render; present only when status === 'code'. */
  qr: string
  message: string
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
