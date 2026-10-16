// featureSwitches.ts — L1 stage-1 switches for UI whose server has not landed
// yet (founder decision 2026-10-14: land stage 1 with every entry point whose
// server isn't in it hidden; turn each back on when its server lands).
// Flip a switch only together with its server landing; the code behind it
// stays in place.

/** Group chats: sidebar section, squad dialog, Add participant, room route. Hidden until WU-15 (stage 3), #1314. */
export const ROOMS_ENABLED = false

/** Destination-bound secret cards. Enabled with the WU-4 request/submit server. */
export const SECRET_CARDS_ENABLED = true

/** An agent's private secrets panel (GET /secrets). Hidden until WU-5. */
export const AGENT_PRIVATE_SECRETS_ENABLED = false

/**
 * Inline tool approvals (#1263 unit 3: the card in the chat plus the "waiting in
 * another chat" banner). Hidden until WU-8 (stage 2): founder design = card in
 * parent chat, no banner, menu-bar attention animation. While off, the release
 * app-wide ToolApprovalModal shows every pending approval.
 */
export const INLINE_TOOL_APPROVALS_ENABLED = false
