// featureSwitches.ts — L1 stage-1 switches for UI whose server has not landed
// yet (founder decision 2026-10-14: land stage 1 with every entry point whose
// server isn't in it hidden; turn each back on when its server lands).
// Flip a switch only together with its server landing; the code behind it
// stays in place.

/** Group chats: sidebar section, squad dialog, Add participant, room route. On since WU-15's server landed (stage 3, #1314). */
export const ROOMS_ENABLED = true

/** Destination-bound secret cards. Enabled with the WU-4 request/submit server. */
export const SECRET_CARDS_ENABLED = true

/** An agent's private secrets panel (GET /secrets). WU-5 landed in Train 2, so
 * it is on again (founder 2026-10-14: a switch hides its UI only until the work
 * unit it belongs to lands). Worker and system agents stay excluded at the
 * mount site. */
export const AGENT_PRIVATE_SECRETS_ENABLED = true

/**
 * Inline tool approvals (#1263 unit 3: the card in the chat plus the "waiting in
 * another chat" banner). On since WU-8 / bundle B3: the card shows in its chat
 * and a reload restores it complete from the session_state snapshot. While
 * off, the release app-wide ToolApprovalModal shows every pending approval.
 */
export const INLINE_TOOL_APPROVALS_ENABLED = true
