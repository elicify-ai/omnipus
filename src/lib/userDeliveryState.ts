// The one definition of a user-authored message's delivery state in the SPA.
// REST rows (lib/api/sessions.ts UserMessage), store rows (store/chat/types.ts
// ChatMessage) and the delivery copy (components/chat/ConnectionStatus.tsx)
// all use this type, so a state added here is added everywhere.
//
// Wire states (message_status): received, working, failed, discarded. The rest
// are SPA-only: 'queued' / 'sending' (not acknowledged yet).
//
// Ruling S3-02 (2026-10-12): /clear outcomes are wire states. The former
// /clear-only 'not-performed' and 'unconfirmed' states are deleted: a refusal
// arrives as message_status failed (the standard failed chip), and a held clear
// stays 'queued' with no deadline (founder ruling: the queued look is honest
// while the server holds the clear).

export const USER_DELIVERY_STATES = [
  'queued', 'sending', 'received', 'working', 'failed', 'discarded',
] as const

export type UserDeliveryState = (typeof USER_DELIVERY_STATES)[number]
