// Shared attachment visuals (ChatGPT-style): image thumbnails + color-coded,
// type-specific file cards. AssistantUI is headless here — AttachmentPrimitive.Thumb
// only renders ".ext" text — so the presentation is ours, but it sits on top of
// AssistantUI's structural primitives (ComposerPrimitive.Attachments /
// AttachmentPrimitive.*) and the public useAttachment() hook. Used in both the
// composer (pre-send chips) and the sent user-message bubble so they're consistent.

import { useEffect, useState } from 'react'
import {
  File,
  FilePdf,
  FileDoc,
  FileXls,
  FileCsv,
  FilePpt,
  FileText,
  FileCode,
  FileZip,
  FileImage,
  FileAudio,
  FileVideo,
  X,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

type IconComp = typeof File

interface FileTypeMeta {
  Icon: IconComp
  /** Accent colour for the icon + badge tint. */
  color: string
  /** Short type label shown under the filename. */
  label: string
}

function ext(filename: string): string {
  const parts = filename.toLowerCase().split('.')
  return parts.length > 1 ? (parts.pop() as string) : ''
}

/** Maps a file's extension / MIME to a Phosphor icon, accent colour, and label. */
export function fileTypeMeta(filename: string, contentType?: string): FileTypeMeta {
  const e = ext(filename)
  const mime = (contentType ?? '').toLowerCase()

  const is = (...exts: string[]) => exts.includes(e)

  if (e === 'pdf' || mime === 'application/pdf') return { Icon: FilePdf, color: '#E5484D', label: 'PDF' }
  if (is('doc', 'docx') || mime.includes('wordprocessingml')) return { Icon: FileDoc, color: '#2563EB', label: 'Word' }
  if (is('xls', 'xlsx') || mime.includes('spreadsheetml')) return { Icon: FileXls, color: '#16A34A', label: 'Excel' }
  if (is('csv')) return { Icon: FileCsv, color: '#16A34A', label: 'CSV' }
  if (is('ppt', 'pptx') || mime.includes('presentationml')) return { Icon: FilePpt, color: '#EA580C', label: 'Slides' }
  if (is('zip', 'tar', 'gz', 'tgz', 'rar', '7z')) return { Icon: FileZip, color: '#B45309', label: 'Archive' }
  if (is('json', 'xml', 'yaml', 'yml', 'html', 'htm', 'css', 'js', 'jsx', 'ts', 'tsx', 'py', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'sh', 'sql'))
    return { Icon: FileCode, color: '#7C3AED', label: e.toUpperCase() }
  if (is('mp3', 'wav', 'ogg', 'flac', 'm4a', 'aac') || mime.startsWith('audio/')) return { Icon: FileAudio, color: '#DB2777', label: 'Audio' }
  if (is('mp4', 'mov', 'avi', 'webm', 'mkv') || mime.startsWith('video/')) return { Icon: FileVideo, color: '#4F46E5', label: 'Video' }
  if (is('png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif') || mime.startsWith('image/'))
    return { Icon: FileImage, color: '#0EA5E9', label: 'Image' }
  if (is('txt', 'md', 'markdown', 'rtf', 'log')) return { Icon: FileText, color: '#64748B', label: e ? e.toUpperCase() : 'Text' }

  return { Icon: File, color: '#64748B', label: e ? e.toUpperCase() : 'File' }
}

export function isImageAttachment(filename: string, contentType?: string): boolean {
  return (contentType ?? '').toLowerCase().startsWith('image/') || /\.(png|jpe?g|gif|webp|svg|bmp|avif)$/i.test(filename)
}

/** Creates (and revokes) an object URL for previewing a local image File. */
export function useFilePreview(file: File | undefined, contentType?: string): string | undefined {
  const [url, setUrl] = useState<string>()
  useEffect(() => {
    if (!file || !isImageAttachment(file.name, contentType ?? file.type)) {
      setUrl(undefined)
      return
    }
    const objectUrl = URL.createObjectURL(file)
    setUrl(objectUrl)
    return () => URL.revokeObjectURL(objectUrl)
  }, [file, contentType])
  return url
}

interface AttachmentCardProps {
  filename: string
  contentType?: string
  /** Preview URL for images (object URL pre-send, or server URL once uploaded). */
  imageUrl?: string
  /** Optional remove control rendered in the top-right corner (e.g. composer). */
  removeButton?: React.ReactNode
  /** Extra classes for the outer container. */
  className?: string
}

/**
 * One attachment rendered ChatGPT-style: an image becomes a rounded thumbnail,
 * any other file becomes a compact card with a colour-coded, type-specific icon
 * plus filename + type label. Styling uses the chat's CSS variables so it stays
 * consistent across the composer and the message bubble.
 */
export function AttachmentCard({ filename, contentType, imageUrl, removeButton, className }: AttachmentCardProps) {
  if (isImageAttachment(filename, contentType) && imageUrl) {
    return (
      <div
        className={cn(
          'relative shrink-0 rounded-lg overflow-hidden border border-[var(--color-border)] bg-[var(--color-surface-2)]',
          className,
        )}
        title={filename}
      >
        <img src={imageUrl} alt={filename} className="block w-full h-full object-cover" loading="lazy" />
        {removeButton}
      </div>
    )
  }

  const { Icon, color, label } = fileTypeMeta(filename, contentType)
  return (
    <div
      className={cn(
        'relative shrink-0 flex items-center gap-2.5 pl-2 pr-3 py-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] max-w-[220px]',
        className,
      )}
      title={filename}
    >
      <div
        className="shrink-0 w-9 h-9 rounded-md flex items-center justify-center"
        style={{ backgroundColor: `${color}22`, color }}
      >
        <Icon size={20} weight="fill" />
      </div>
      <div className="min-w-0">
        <p className="truncate text-xs font-medium text-[var(--color-secondary)]">{filename}</p>
        <p className="text-[10px] uppercase tracking-wide text-[var(--color-muted)]">{label}</p>
      </div>
      {removeButton}
    </div>
  )
}

/** A small corner "×" remove control, styled for overlaying an AttachmentCard. */
export function AttachmentRemoveX() {
  return (
    <span className="flex items-center justify-center w-4 h-4 rounded-full bg-[var(--color-surface-3)] text-[var(--color-muted)] hover:bg-[var(--color-error)] hover:text-white transition-colors">
      <X size={10} weight="bold" />
    </span>
  )
}
