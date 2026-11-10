// MailFolderRail — the folder column of the Mail panel (D5: Inbox, Sent,
// Drafts only), in the Library's visual language. Folders are tabs
// (role=tab inside a role=tablist) whose accessible name is pinned to the
// display name via aria-label, so the unread badge inside the tab never
// pollutes it. The inbox badge renders its unread count even when 0 (the
// count dropping to 0 must stay visible — opening a message clears it).
//
// W3 (US-2 AS-5 / FR-W3-5 / US-4): an UNKNOWN count never renders as a
// fabricated zero — a null `unread_count` (nullable on the wire) and a
// null `total` (post the scheduled MailFolder.total-nullability amendment)
// render the explicit "—" unknown marker; a genuine zero keeps rendering
// "0". A role whose availability is `unknown` shows "—" in its count slot
// regardless of any numeric value — an unconfirmed count is not a count.
// The role itself stays visible in every availability state (S-7/S-8);
// the list zone's absent/unknown explanations live in MailPanel.
import { NotePencil, PaperPlaneTilt, TrayArrowDown } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import type { MailFolder } from '@/lib/api'

export interface MailFolderRailProps {
  folders: MailFolder[]
  active: string
  onFolderChange: (slug: string) => void
}

/** The unknown-count marker: "—", never a fabricated zero (MC-W3-4). */
export const MAIL_UNKNOWN_COUNT = '—'

export function MailFolderRail({ folders, active, onFolderChange }: MailFolderRailProps) {
  return (
    <nav
      aria-label="Mail folders"
      role="tablist"
      className="hidden w-40 shrink-0 flex-col gap-[var(--space-1)] p-[var(--space-2)] sm:flex"
    >
      {folders.map((folder) => {
        const selected = folder.slug === active
        const unknownRole = folder.availability === 'unknown'
        const unread = folder.unread_count
        return (
          <Button
            key={folder.slug}
            variant="ghost"
            role="tab"
            aria-selected={selected}
            aria-label={folder.display_name}
            // The focus-return fallback target for the attachment-handoff
            // return path (US-7 AS-2's last step — see mailAttachmentHandoff).
            data-mail-folder-tab="true"
            onClick={() => onFolderChange(folder.slug)}
            className={cn(
              'h-auto w-full justify-start gap-[var(--space-2)] rounded-md px-[var(--space-2)] py-[var(--space-2)] font-[var(--font-weight-regular)]',
              selected
                ? 'bg-[var(--color-surface-2)] text-[var(--color-secondary)]'
                : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
            )}
          >
            <FolderIcon slug={folder.slug} />
            <span className="min-w-0 flex-1 truncate text-left text-[length:var(--type-body-compact-size)]">
              {folder.display_name}
            </span>
            {folder.slug === 'inbox' ? (
              // Inbox: the live unread count — rendered at 0 too, so the
              // drop to zero after reading a message stays visible. An
              // unknown (null) count renders the "—" marker, never 0.
              unread === null || unknownRole ? (
                <span
                  aria-label="Unread count unknown"
                  className="shrink-0 px-[var(--space-1)] text-[length:var(--type-utility-xs-size)] font-[var(--font-weight-medium)] text-[var(--color-muted)]"
                >
                  {MAIL_UNKNOWN_COUNT}
                </span>
              ) : unread > 0 ? (
                <Badge className="shrink-0 px-[var(--space-1)] py-0 font-[var(--font-weight-medium)]">
                  {unread}
                </Badge>
              ) : (
                <span className="shrink-0 px-[var(--space-1)] text-[length:var(--type-utility-xs-size)] font-[var(--font-weight-medium)] text-[var(--color-muted)]">
                  {unread}
                </span>
              )
            ) : (
              <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                {unknownRole || folder.total === null || folder.total === undefined
                  ? MAIL_UNKNOWN_COUNT
                  : folder.total}
              </span>
            )}
          </Button>
        )
      })}
    </nav>
  )
}

function FolderIcon({ slug }: { slug: string }) {
  if (slug === 'inbox') return <TrayArrowDown size={16} aria-hidden="true" />
  if (slug === 'sent') return <PaperPlaneTilt size={16} aria-hidden="true" />
  return <NotePencil size={16} aria-hidden="true" />
}
