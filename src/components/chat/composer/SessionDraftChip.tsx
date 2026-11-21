// #1428 step 2 (spec 4.5 rule 3): a staged file of the open chat's draft,
// rendered from the draft store with the same visuals as the runtime chip
// (OmnipusComposer.tsx::ComposerAttachmentChip): AttachmentCard plus the
// hover/focus remove control. Removal deletes the file from the store only.

import { AttachmentCard, AttachmentRemoveX, useFilePreview } from '../AttachmentCard'
import { IconButton } from '@/components/ui/icon-button'
import { cn } from '@/lib/utils'
import type { DraftFile } from '@/store/chat/drafts'

export function SessionDraftChip({ file, onRemove }: { file: DraftFile; onRemove: () => void }) {
  const local = file.kind === 'local' ? file.file : undefined
  const name = file.kind === 'local' ? file.file.name : file.name
  const contentType = file.kind === 'local' ? file.file.type : file.mime
  const localPreview = useFilePreview(local, contentType)
  const imageUrl = file.kind === 'uploaded' && contentType.startsWith('image/') ? file.previewUrl : localPreview
  return (
    <AttachmentCard
      filename={name}
      contentType={contentType}
      imageUrl={imageUrl}
      className={cn('group', !!imageUrl && 'w-16 h-16')}
      removeButton={
        <IconButton
          size="sm"
          variant="ghost"
          onClick={onRemove}
          className="absolute -top-1.5 -right-1.5 h-auto w-auto p-0 opacity-0 group-hover:opacity-100 focus:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity"
          aria-label={`Remove ${name}`}
        >
          <AttachmentRemoveX />
        </IconButton>
      }
    />
  )
}
