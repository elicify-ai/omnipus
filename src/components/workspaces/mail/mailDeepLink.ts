// mailDeepLink.ts — the ONE helper for opening a Mail deep link from chat
// (email-mail-view-spec.md §17): the create_email_draft result's chat_link
// is an absolute http(s) URL whose hash carries the SPA path
// /#/workspaces/{ws}/mail?mailbox={agent}&folder={slug}&message={ref}.
// Because the SPA is hash-routed, "navigating in place" is one hash write:
// the /mail route stub consumes the params into the panel intent, opens the
// panel leave-gated, and lands on chat. Pattern-matched on the SPA path —
// origin equality is deliberately NOT required (public_url-derived links
// may name a different host than the one the browser is on; only the hash
// matters to a hash router). Safe-scheme gating rides isSafeHref.
import { isSafeHref } from '@/lib/url-safe'

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
  window.location.hash = target
  return true
}
