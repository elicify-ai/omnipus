// mailDeepLink.ts — the ONE helper for opening a Mail deep link from chat
// (email-mail-view-spec.md §17): the create_email_draft result's chat_link
// is an absolute http(s) URL whose hash carries the SPA path
// /#/workspaces/{ws}/mail?mailbox={agent}&folder={slug}&message={ref}.
// Because the SPA's /mail route is now the full-page surface, chat links are
// handled here: carry their address into the panel intent, replace the active
// panel through the shared leave gate, and keep the conversation on screen.
// Pattern-matched on the SPA path —
// origin equality is deliberately NOT required (public_url-derived links
// may name a different host than the one the browser is on; only the hash
// matters to a hash router). Safe-scheme gating rides isSafeHref.
import { isSafeHref } from '@/lib/url-safe'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import { writeMailPanelIntent } from './mailPanelIntent'

/** The SPA path a chat_link's hash must carry for in-place navigation. */
const MAIL_PATH_RE = /^\/workspaces\/[^/]+\/mail(\?|$)/

/**
 * Return the hash target to set for an in-place Mail deep link, or null
 * when `href` is not one (not safe, not http(s), hash does not carry the
 * /workspaces/{ws}/mail path).
 */
export function mailDeepLinkTarget(href: string): string | null {
  try {
    if (!isSafeHref(href)) return null
    const url = new URL(href, window.location.origin)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return null
    const hashPath = url.hash.startsWith('#') ? url.hash.slice(1) : url.hash
    const candidate = hashPath.startsWith('/') ? hashPath : url.pathname + url.search
    if (!MAIL_PATH_RE.test(candidate)) return null
    return candidate
  } catch {
    return null
  }
}

/**
 * Navigate in place to a Mail deep link (open the panel on that
 * mailbox/folder/message). Returns false when `href` is not a Mail deep
 * link — callers fall back to their default link behavior.
 */
export function openMailDeepLink(href: string): boolean {
  const target = mailDeepLinkTarget(href)
  if (target === null) return false

  const url = new URL(target, window.location.origin)
  const segments = url.pathname.split('/').filter(Boolean)
  const workspaceId = segments[1]
  if (segments.length !== 3 || segments[0] !== 'workspaces' || segments[2] !== 'mail' || !workspaceId) {
    return false
  }

  const mailboxId = url.searchParams.get('mailbox')
  const folder = url.searchParams.get('folder') ?? 'inbox'
  const messageRef = url.searchParams.get('message')
  const outgoing = useUiStore.getState().activePanel?.id ?? null
  leaveGateThen(outgoing, () => {
    writeMailPanelIntent(workspaceId, { agentId: mailboxId, folder, messageRef })
    useUiStore.getState().openPanel('mail', { workspaceId })
    window.location.hash = `/workspaces/${encodeURIComponent(workspaceId)}/chat?panel=mail`
  })
  return true
}
