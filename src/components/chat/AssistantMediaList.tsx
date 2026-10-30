// AssistantMediaList — the media of one assistant message: inline media
// (screenshots, files) exactly as before, plus office-preview thumbnail groups
// (office-preview spec §5.2/§5.3), one group per tool call. Shared by the
// live AssistantUI row (ChatScreen.tsx::InlineMedia) and the historical /
// plain row, which used to carry two copies of the inline markup.

import { useMemo } from 'react'
import { File } from '@phosphor-icons/react'
import type { MediaAttachment } from '@/store/chat'
import { ChatImage } from './ChatImage'
import { PreviewThumbnailGroup } from './PreviewThumbnailGroup'
import { partitionMedia } from './previewGroups'

function InlineMediaItem({ item }: { item: MediaAttachment }) {
  if (item.type === 'image') {
    return (
      <div className="max-w-2xl">
        <ChatImage src={item.url} alt={item.caption || item.filename} filename={item.filename} />
        {item.caption && (
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] px-[var(--space-2)] py-[var(--space-1)]">{item.caption}</p>
        )}
      </div>
    )
  }
  return (
    <a
      tabIndex={0}
      href={item.url}
      download={item.filename}
      className="inline-flex items-center gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-2)] rounded-lg border border-[var(--color-border)] text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
    >
      <File size={14} />
      {item.filename}
    </a>
  )
}

export interface AssistantMediaListProps {
  media: readonly MediaAttachment[]
}

export function AssistantMediaList({ media }: AssistantMediaListProps) {
  const blocks = useMemo(() => partitionMedia(media), [media])
  if (blocks.length === 0) return null
  return (
    <div className="flex flex-col gap-[var(--space-2)]">
      {blocks.map((block) =>
        block.kind === 'group' ? (
          <PreviewThumbnailGroup key={block.key} group={block.group} />
        ) : (
          <InlineMediaItem key={block.key} item={block.item} />
        ),
      )}
    </div>
  )
}
