// libraryPreviewKind.ts — classifies a LibraryEntry into which preview surface
// LibraryPreviewPane should mount (library-spec.md section 4's scope table).
//
// Deliberately extension/mime-driven for image/video/markdown/mermaid — those
// are always rendered as media or routed to the shared markdown/mermaid
// renderers regardless of what the server's directory-listing hint says.
// Everything else defers to `entry.is_text_editable` (the server's best-effort
// hint from LibraryEntry — see contracts/components/schemas/LibraryEntry.yaml)
// to decide whether it's worth even attempting the content fetch; the content
// endpoint's own `is_text`/`too_large` flags remain the AUTHORITATIVE check at
// read time (see LibraryPreviewPane, which honours both before rendering).

export type LibraryPreviewKind = 'image' | 'video' | 'markdown' | 'mermaid' | 'text' | 'other'

const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif', 'ico'])
const VIDEO_EXTS = new Set(['mp4', 'webm', 'mov', 'mkv', 'avi', 'm4v', 'ogv'])

/** Lowercased extension without the leading dot, or '' if there isn't one. */
export function libraryEntryExt(name: string): string {
  const i = name.lastIndexOf('.')
  return i > 0 ? name.slice(i + 1).toLowerCase() : ''
}

export interface ClassifiableEntry {
  name: string
  mime?: string
  is_text_editable: boolean
}

export function classifyLibraryEntry(entry: ClassifiableEntry): LibraryPreviewKind {
  const e = libraryEntryExt(entry.name)
  const mime = (entry.mime ?? '').toLowerCase()
  if (mime.startsWith('image/') || IMAGE_EXTS.has(e)) return 'image'
  if (mime.startsWith('video/') || VIDEO_EXTS.has(e)) return 'video'
  if (e === 'md' || e === 'markdown') return 'markdown'
  if (e === 'mmd' || e === 'mermaid') return 'mermaid'
  if (entry.is_text_editable) return 'text'
  return 'other'
}
