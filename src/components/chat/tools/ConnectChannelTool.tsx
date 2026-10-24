// ConnectChannelTool — the WhatsApp pairing QR under a connect_channel call in
// the requesting chat (omnipus-system-agent BDD-8.4, BDD-8.5, BDD-8.8, FR-081,
// frame K10).
//
// Reuse, not a second implementation (founder ruling): the chat mounts the SAME
// WhatsAppNativeNotice and the SAME whatsappPairing store the Connectors >
// WhatsApp panel uses. The connect_channel call's id is the pairing's
// request_id (pkg/sysagent/tools/channel_connect.go::NotePairingRequest), so
// the notice subscribes with it and shows only this request's pairing.
//
// Live vs replayed (team-lead Q6 = A): only a call this page saw live mounts
// the live QR — a finished call replayed from an older visit shows a static
// pointer to Connectors instead of subscribing (which would start the notice's
// timeout window for a pairing that may be long done). A call stays "live"
// for the rest of the page once its live render ran, so the QR survives the
// move from the streaming row into the completed transcript.

import { WhatsAppNativeNotice } from '@/components/skills/WhatsAppNativeNotice'

/** connect_channel calls this page rendered live (module-level: per page load). */
const liveCalls = new Set<string>()

/** Record that this page rendered the connect_channel call live (Q6 = A). */
export function markConnectChannelLive(toolCallId: string): void {
  liveCalls.add(toolCallId)
}

/** Whether this page rendered the connect_channel call live (Q6 = A). */
export function wasConnectChannelLive(toolCallId: string): boolean {
  return liveCalls.has(toolCallId)
}

/** A QR-paired instance id: the same rule as channel_connect.go::whatsappChannel, per instance. */
export function isWhatsAppChannelId(channelId: string): boolean {
  return channelId === 'whatsapp' || channelId === 'whatsapp_native'
    || channelId.startsWith('whatsapp.') || channelId.startsWith('whatsapp_native.')
}

export function ConnectChannelTool({ toolCallId, sessionId, channelId, live }: {
  toolCallId: string
  sessionId: string
  channelId: string
  live: boolean
}) {
  if (!isWhatsAppChannelId(channelId)) return null
  if (!live) {
    return (
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
        To pair this WhatsApp connection, open it on Connectors.
      </p>
    )
  }
  return <WhatsAppNativeNotice channelId={channelId} requestId={toolCallId} sessionId={sessionId || undefined} />
}
