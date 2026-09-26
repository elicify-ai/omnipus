// MailHtmlFrame — the sandboxed frame for untrusted mail HTML (D13, FR-019).
//
// PROTOTYPE POSTURE (D35): the sample HTML is passed via `srcDoc` with
// `sandbox=""` — no `allow-scripts`, no `allow-same-origin`, no
// `allow-forms`. The real feature serves the sanitized body from a
// token-scoped gateway route under `/mail-preview/` with the full MC-10
// header set; the prototype reproduces the visible behaviour (rendered
// HTML, no script surface, remote images blocked until "Load images",
// paper-toned reading surface) without any of that machinery.
import { cn } from '@/lib/utils'

export interface MailHtmlFrameProps {
  /** Pre-rendered, already-sanitized HTML (prototype: the sample string). */
  html: string
  /** Accessible frame title (axe: every frame needs a name). */
  title: string
  className?: string
}

export function MailHtmlFrame({ html, title, className }: MailHtmlFrameProps) {
  return (
    <iframe
      title={title}
      sandbox=""
      srcDoc={html}
      className={cn(
        'h-full w-full border-0 bg-[var(--color-surface-paper)]',
        className,
      )}
    />
  )
}
