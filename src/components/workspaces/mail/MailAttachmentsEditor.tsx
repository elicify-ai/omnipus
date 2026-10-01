import { useRef } from 'react'
import { File, Plus, X } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import type { MailMessage } from '@/lib/api/generated/openapi-types'
import { formatMailBytes } from './mail-format'

const MAX_ATTACHMENTS = 10
const MAX_TOTAL_BYTES = 25 * 1024 * 1024

/** Mail-specific attachment editor: Compose starts with no MIME parts; draft
 * editing also lists carried parts by their stable part_index. Controls come
 * from the design-system catalog, and neither path uploads until approval. */
export function MailAttachmentsEditor({ files, existing = [], onFilesChange, onRemoveExisting, error, onErrorChange }: {
  files: File[]
  existing?: MailMessage['attachments']
  onFilesChange(files: File[]): void
  onRemoveExisting?(partIndex: number): void
  error: string | null
  onErrorChange(error: string | null): void
}) {
  const fileInputRef = useRef<HTMLInputElement>(null)

  const handleFiles = (selected: FileList | null) => {
    if (!selected || selected.length === 0) return
    const incoming = Array.from(selected)
    if (existing.length + files.length + incoming.length > MAX_ATTACHMENTS) {
      onErrorChange(`Up to ${MAX_ATTACHMENTS} attachments are allowed.`)
      return
    }
    const total = existing.reduce((sum, attachment) => sum + attachment.size_bytes, 0)
      + [...files, ...incoming].reduce((sum, file) => sum + file.size, 0)
    if (total > MAX_TOTAL_BYTES) {
      onErrorChange('Attachments are limited to 25 MB in total.')
      return
    }
    onErrorChange(null)
    onFilesChange([...files, ...incoming])
  }

  return (
    <div
      data-testid="compose-attachments-row"
      className="flex shrink-0 flex-wrap items-center gap-[var(--space-2)] border-t border-[var(--color-border)] pt-[var(--space-2)]"
    >
      <p className="shrink-0 text-[length:var(--type-caption-size)] font-[var(--font-weight-medium)] text-[var(--color-muted)]">
        Attachments (up to 10 files, 25 MB total)
      </p>
      <input
        ref={fileInputRef}
        type="file"
        tabIndex={0}
        multiple
        aria-label="Attach files"
        className="sr-only"
        onChange={(event) => {
          handleFiles(event.target.files)
          event.target.value = ''
        }}
      />
      <Button variant="outline" size="sm" className="shrink-0 gap-[var(--space-1)]" onClick={() => fileInputRef.current?.click()}>
        <Plus size={14} aria-hidden="true" />
        Attach files
      </Button>
      {(existing.length > 0 || files.length > 0) && (
        <ul className="flex min-w-0 flex-1 flex-wrap items-center gap-[var(--space-1)]">
          {existing.map((attachment) => (
            <li key={`part-${attachment.part_index}`} className="flex min-w-0 max-w-full items-center gap-[var(--space-1)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] pl-[var(--space-2)]">
              <File size={16} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
              <span className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">{attachment.filename}</span>
              <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">{formatMailBytes(attachment.size_bytes)}</span>
              {onRemoveExisting && <IconButton aria-label={`Remove ${attachment.filename}`} size="sm" variant="ghost" onClick={() => {
                onRemoveExisting(attachment.part_index)
                onErrorChange(null)
              }}><X size={12} aria-hidden="true" /></IconButton>}
            </li>
          ))}
          {files.map((file, index) => (
            <li key={`file-${index}`} className="flex min-w-0 max-w-full items-center gap-[var(--space-1)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] pl-[var(--space-2)]">
              <File size={16} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
              <span className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">{file.name}</span>
              <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">{formatMailBytes(file.size)}</span>
              <IconButton aria-label={`Remove ${file.name}`} size="sm" variant="ghost" onClick={() => {
                onFilesChange(files.filter((_, item) => item !== index))
                onErrorChange(null)
              }}><X size={12} aria-hidden="true" /></IconButton>
            </li>
          ))}
        </ul>
      )}
      {error && <p role="alert" className="w-full text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">{error}</p>}
    </div>
  )
}
