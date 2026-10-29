// AssistantMediaItem — one file or image an agent sent into an assistant
// reply (send_file, screenshots). Extracted from ChatScreen.tsx by chat-links
// U8, where the live (InlineMedia) and finalized (VirtualAssistantMessageRow)
// paths each carried an identical inline copy of this markup.
//
//   image: ChatImage (lightbox + its own toolbar) with an optional caption.
//          A Library location, when present, is ignored here — images keep
//          their existing toolbar and gain no new action (spec A7, BS-5.6).
//   file:  the download link, plus "Open in Library" when the media part
//          carried a `library_ref` (FR-022, BS-5.1). The button opens the
//          docked Library through the leave gate (BS-5.5); its accessible
//          name names the file.

import { File, FolderOpen } from '@phosphor-icons/react'
import type { LibraryRef } from '@/lib/api/generated/asyncapi-types'
import { Button } from '@/components/ui/button'
import { openLibraryAt } from '@/components/library/openLibraryAt'
import { ChatImage } from './ChatImage'

export interface AssistantMediaItemProps {
  /** 'image' renders inline; every other media type is a downloadable file card. */
  kind: 'image' | 'file'
  url: string
  filename: string
  caption?: string
  libraryRef?: LibraryRef
}

export function AssistantMediaItem({ kind, url, filename, caption, libraryRef }: AssistantMediaItemProps) {
  if (kind === 'image') {
    return (
      <div className="max-w-2xl">
        <ChatImage
          src={url}
          alt={caption || filename}
          filename={filename}
        />
        {caption && (
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] px-[var(--space-2)] py-[var(--space-1)]">{caption}</p>
        )}
      </div>
    )
  }

  const download = (
    <a tabIndex={0}
      href={url}
      download={filename}
      className="inline-flex items-center gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-2)] rounded-lg border border-[var(--color-border)] text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
    >
      <File size={14} />
      {filename}
    </a>
  )
  if (!libraryRef) return download

  return (
    <div className="flex flex-wrap items-center gap-[var(--space-2)]">
      {download}
      <Button
        type="button"
        variant="outline"
        size="sm"
        aria-label={`Open ${filename} in Library`}
        data-testid="assistant-media-open-in-library"
        onClick={() => openLibraryAt({ workspaceId: libraryRef.workspace_id, path: libraryRef.path })}
      >
        <FolderOpen size={14} aria-hidden="true" />
        Open in Library
      </Button>
    </div>
  )
}
