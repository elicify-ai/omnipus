// MailHtmlFrame — the ONE sanctioned HTML-mail surface (D13, D17, MC-10):
// a token-scoped, same-origin iframe served from /mail-preview/html/{token}
// with the normative attribute set — src (never srcDoc), sandbox exactly
// "allow-popups allow-popups-to-escape-sandbox" (no scripts, no same-origin,
// no forms), referrerPolicy no-referrer, allow="". Remote content is blocked
// until the human clicks "Load images"; that click does not flip a local
// boolean, it re-mints the frame's token with load_remote=true (the parent
// owns the re-mint via onLoadImages).
import { ShieldWarning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'

export interface MailHtmlFrameProps {
  /** The minted preview URL: /mail-preview/html/{token} (or absolute). */
  tokenUrl: string
  /** Fired once per click; the parent re-mints with load_remote: true. */
  onLoadImages: () => void
  /** Hide the D17 bar once images are loaded (the parent's token swap). */
  showLoadImages?: boolean
  /** Accessible frame name (axe: every frame needs one). */
  title?: string
}

/**
 * The normative sandbox attribute string — one literal, exported so the
 * signature preview mini-frame can mirror the exact posture.
 */
export const MAIL_HTML_FRAME_SANDBOX = 'allow-popups allow-popups-to-escape-sandbox'

export function MailHtmlFrame({ tokenUrl, onLoadImages, showLoadImages = true, title = 'Mail body' }: MailHtmlFrameProps) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      {showLoadImages && (
        <div className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-3)] py-[var(--space-2)]">
          <ShieldWarning size={16} aria-hidden="true" className="shrink-0 text-[var(--color-warning)]" />
          <p className="min-w-0 flex-1 text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
            Images from outside Omnipus are blocked.
          </p>
          <Button variant="outline" size="sm" onClick={onLoadImages} className="shrink-0">
            Load images
          </Button>
        </div>
      )}
      <iframe
        title={title}
        src={tokenUrl}
        sandbox={MAIL_HTML_FRAME_SANDBOX}
        referrerPolicy="no-referrer"
        allow=""
        className="h-full w-full flex-1 border-0 bg-[var(--color-surface-paper)]"
      />
    </div>
  )
}
