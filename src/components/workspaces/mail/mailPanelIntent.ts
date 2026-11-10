// mailPanelIntent.ts — the Mail panel's per-workspace intent (FR-010,
// MC-31a): which mailbox and folder the panel reopens on. The side-panel
// shell's PanelContext (src/components/panel-shell/types.ts) carries
// workspace/agent/session/mailbox ids but NO folder/message fields, so the
// folder/message focus travels through this sessionStorage-backed intent
// (per workspace), not through panel context. Session-only by design — a new
// browser session starts at Inbox/unselected, per US-3's default view.

export interface MailPanelIntent {
  /** The selected mailbox's agent id (FR-010) — null until a mailbox is chosen. */
  agentId: string | null
  /** The open folder slug (inbox | sent | drafts) — null until a folder is opened. */
  folder: string | null
  /**
   * The message to open (uid:<uidvalidity>:<uid> or mid:<Message-ID>) —
   * CONSUME-ONCE: the deep link (§17 chat_link) writes it, the panel reads
   * it once at mount and the panel's persist effect immediately rewrites
   * the intent without it, so a later panel re-open starts at the list.
   */
  messageRef: string | null
}

const KEY_PREFIX = 'omnipus.mail-panel.intent.'

function key(workspaceId: string): string {
  return KEY_PREFIX + workspaceId
}

export function readMailPanelIntent(workspaceId: string): MailPanelIntent {
  try {
    const raw = sessionStorage.getItem(key(workspaceId))
    if (raw === null) return { agentId: null, folder: null, messageRef: null }
    const parsed = JSON.parse(raw) as Partial<MailPanelIntent> | null
    if (parsed === null || typeof parsed !== 'object') return { agentId: null, folder: null, messageRef: null }
    return {
      agentId: typeof parsed.agentId === 'string' ? parsed.agentId : null,
      folder: typeof parsed.folder === 'string' ? parsed.folder : null,
      messageRef: typeof parsed.messageRef === 'string' ? parsed.messageRef : null,
    }
  } catch {
    // A malformed stored intent must never break the panel — start clean.
    return { agentId: null, folder: null, messageRef: null }
  }
}

export function writeMailPanelIntent(workspaceId: string, intent: MailPanelIntent): void {
  try {
    sessionStorage.setItem(key(workspaceId), JSON.stringify(intent))
  } catch {
    // sessionStorage can throw (private mode, quota) — a persistence failure
    // must never break the panel.
  }
}

export function clearMailPanelIntent(workspaceId: string): void {
  try {
    sessionStorage.removeItem(key(workspaceId))
  } catch {
    // Same posture as write: persistence failures stay non-fatal.
  }
}
