// The one definition of a user-authored message's delivery state in the SPA.
// REST rows (lib/api/sessions.ts UserMessage), store rows (store/chat/types.ts
// ChatMessage) and the delivery copy (components/chat/ConnectionStatus.tsx)
// all use this type, so a state added here is added everywhere.
//
// Wire states (message_status): received, working, failed, discarded. The rest
// are SPA-only: 'queued' / 'sending' (not acknowledged yet) and the two
// /clear-only settled states 'not-performed' (the server refused the clear)
// and 'unconfirmed' (the bounded wait ended with no answer).

export const USER_DELIVERY_STATES = [
  'queued', 'sending', 'received', 'working', 'failed', 'discarded', 'not-performed', 'unconfirmed',
] as const

export type UserDeliveryState = (typeof USER_DELIVERY_STATES)[number]
