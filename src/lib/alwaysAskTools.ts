/**
 * Always-ask tools (channels-spec-r8 FR-036, FR-038; founder Q22 B). Mirrors
 * pkg/tools/always_ask.go's AlwaysAskTools: these tools ask for a fresh
 * approval on every call, also in God Mode, and an approval of one is never
 * remembered — the approval card offers Approve Once, Deny and Cancel, never
 * Always Allow (the server refuses that action for them).
 */
export const ALWAYS_ASK_TOOLS: readonly string[] = ['set_channel_outbound']

/** The card notice for an always-ask tool (FR-038, S-29). */
export const ONE_REQUEST_APPROVAL_NOTICE = 'This approval applies to this one request.'

/** Exact-name match, like the server's IsAlwaysAsk. */
export function isAlwaysAskTool(toolName: string): boolean {
  return ALWAYS_ASK_TOOLS.includes(toolName)
}
