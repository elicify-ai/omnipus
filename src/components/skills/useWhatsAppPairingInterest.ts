// useWhatsAppPairingInterest — this connection's interest in one channel's
// whatsapp_pairing (QR/status) frames, shared by every place that shows the
// pairing: Connectors > WhatsApp (ChannelConfigPanel) and the requesting chat
// (ConnectChannelTool, K10 / FR-081).
//
// The gateway keeps ONE subscription per connection and channel (a set, not a
// counter: pkg/gateway/websocket.go::setPairingInterest). So the browser
// counts the mounted viewers per channel: every viewer (re)sends the
// subscribe while connected — with the chat request's ids as observation-only
// correlation hints, never an ownership claim — and only the LAST viewer to
// close sends `active: false` and drops the QR
// secret from the store (#283).

import { useEffect, useRef } from 'react'
import { useConnectionStore } from '@/store/connection'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'

/** Mounted viewers per channel id on this page. */
const viewers = new Map<string, number>()

export function useWhatsAppPairingInterest(channelId: string, requestId?: string, sessionId?: string): void {
  const isConnected = useConnectionStore((s) => s.isConnected)
  // The store's clear, read through the hook (not getState) and kept in a ref
  // so the viewer-lifetime effect below never re-runs on a new identity.
  const clear = useWhatsAppPairingStore((s) => s.clear)
  const clearRef = useRef(clear)
  useEffect(() => {
    clearRef.current = clear
  }, [clear])

  // Viewer lifetime: the last close unsubscribes and clears.
  useEffect(() => {
    viewers.set(channelId, (viewers.get(channelId) ?? 0) + 1)
    return () => {
      const left = (viewers.get(channelId) ?? 1) - 1
      if (left > 0) {
        viewers.set(channelId, left)
        return
      }
      viewers.delete(channelId)
      useConnectionStore.getState().connection?.send({
        type: 'whatsapp_pairing_subscribe',
        channel_id: channelId,
        active: false,
      })
      clearRef.current(channelId)
    }
  }, [channelId])

  // Reconnect only restores observation interest, with the same correlation
  // hints (BDD-8.8). It never claims ownership or enables/restarts the channel;
  // only an explicit Retry's REST enable request may claim the next attempt.
  useEffect(() => {
    if (!isConnected) return
    useConnectionStore.getState().connection?.send({
      type: 'whatsapp_pairing_subscribe',
      channel_id: channelId,
      active: true,
      ...(requestId ? { request_id: requestId } : {}),
      ...(sessionId ? { session_id: sessionId } : {}),
    })
  }, [isConnected, channelId, requestId, sessionId])
}
