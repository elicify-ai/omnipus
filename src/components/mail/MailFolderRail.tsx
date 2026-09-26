// MailFolderRail — the folder column of the Mail panel (D5: Inbox, Sent,
// Drafts only), in the Library's visual language. On narrow viewports this
// rail is replaced by the SegmentedControl in MailExplorer (see that file).
import {
  NotePencil,
  PaperPlaneTilt,
  TrayArrowDown,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import type { FolderKey, FolderSummary } from './sampleMail'

export interface MailFolderRailProps {
  folders: FolderSummary[]
  active: string
  onFolderChange: (key: FolderKey) => void
  className?: string
}

export function MailFolderRail({
  folders,
  active,
  onFolderChange,
  className,
}: MailFolderRailProps) {
  return (
    <nav
      aria-label="Mail folders"
      className={cn('flex w-40 shrink-0 flex-col gap-[var(--space-1)] p-[var(--space-2)]', className)}
    >
      {folders.map((folder) => {
        const selected = folder.key === active
        return (
          <Button
            key={folder.key}
            variant="ghost"
            onClick={() => onFolderChange(folder.key)}
            aria-current={selected ? 'true' : undefined}
            className={cn(
              'h-auto w-full justify-start gap-[var(--space-2)] rounded-md px-[var(--space-2)] py-[var(--space-2)] font-[var(--font-weight-regular)]',
              selected
                ? 'bg-[var(--color-surface-2)] text-[var(--color-secondary)]'
                : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
            )}
          >
            <FolderIcon folderKey={folder.key} />
            <span className="min-w-0 flex-1 truncate text-left text-[length:var(--type-body-compact-size)]">
              {folder.label}
            </span>
            {folder.unread > 0 ? (
              <span className="shrink-0 rounded-full bg-[var(--color-accent)] px-[var(--space-1)] text-[length:var(--type-utility-xs-size)] font-[var(--font-weight-medium)] text-[var(--color-primary)]">
                {folder.unread}
              </span>
            ) : (
              <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                {folder.total}
              </span>
            )}
          </Button>
        )
      })}
    </nav>
  )
}

function FolderIcon({ folderKey }: { folderKey: string }) {
  if (folderKey === 'inbox') return <TrayArrowDown size={16} aria-hidden="true" />
  if (folderKey === 'sent') return <PaperPlaneTilt size={16} aria-hidden="true" />
  return <NotePencil size={16} aria-hidden="true" />
}
