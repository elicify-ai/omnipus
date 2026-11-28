// AssistantMediaList — the media of one assistant message: inline media
// (screenshots, files) through the shared AssistantMediaItem (chat-links U8,
// with "Open in Library" when a part carries library_ref), plus office-preview
// thumbnail groups (office-preview spec §5.2/§5.3), one group per tool call.
// Shared by the live AssistantUI row (ChatScreen.tsx::InlineMedia) and the
// historical / plain row, which used to carry two copies of the inline markup.

import { useMemo } from 'react'
import type { MediaAttachment } from '@/store/chat'
import { AssistantMediaItem } from './AssistantMediaItem'
import { PreviewThumbnailGroup } from './PreviewThumbnailGroup'
import { partitionMedia } from './previewGroups'

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
          <AssistantMediaItem
            key={block.key}
            kind={block.item.type === 'image' ? 'image' : 'file'}
            url={block.item.url}
            filename={block.item.filename}
            caption={block.item.caption}
            contentType={block.item.contentType}
            libraryRef={block.item.libraryRef}
          />
        ),
      )}
    </div>
  )
}
