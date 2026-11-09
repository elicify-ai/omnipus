// AssistantMediaList — the media of one assistant message: inline media
// (screenshots, files) exactly as before, plus office-preview thumbnail groups
// (office-preview spec §5.2/§5.3), one group per tool call. Shared by the
// live AssistantUI row (ChatScreen.tsx::InlineMedia) and the historical /
// plain row, which used to carry two copies of the inline markup.

import { useMemo } from 'react'
import type { MediaAttachment } from '@/store/chat'
import { ChatFileAttachment } from './ChatFileAttachment'
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
  // FR-002/FR-081: the same keyboard-operable file card on every row.
  return <ChatFileAttachment media={item} />
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
