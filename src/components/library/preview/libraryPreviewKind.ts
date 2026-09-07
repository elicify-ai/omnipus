// libraryPreviewKind.ts — classifies a LibraryEntry into which preview surface
// LibraryPreviewPane should mount (library-spec.md section 4's scope table,
// extended by ADR-067 D15 / spec FR-001 and FR-018).
//
// Deliberately extension/mime-driven for image/video/markdown/mermaid — those
// are always rendered as media or routed to the shared markdown/mermaid
// renderers regardless of what the server's directory-listing hint says.
// Everything else defers to `entry.is_text_editable` (the server's best-effort
// hint from LibraryEntry — see contracts/components/schemas/LibraryEntry.yaml)
// to decide whether it's worth even attempting the content fetch; the content
// endpoint's own `is_text`/`too_large` flags remain the AUTHORITATIVE check at
// read time (see LibraryPreviewPane, which honours both before rendering).
//
// ADR-067 D15 adds three kinds — `html`, `pdf`, `audio` — so the pane shows the
// artifact rather than its source or a download card. Two properties of that
// change are load-bearing and easy to undo by accident:
//
//   1. `.svg` still classifies as `image`, NOT as an active document. Inside the
//      SPA an image is drawn in an `<img>`, which renders SVG in secure static
//      mode and never runs its scripts (spec §10.4, test 123). Reclassifying it
//      — or swapping the image surface to inline-SVG injection "so it scales" —
//      turns the reader into a script host and nothing else notices.
//   2. The three new checks sit AFTER the two mime-driven image/video checks and
//      are matched on EXTENSION ONLY. Both halves matter:
//        - After, because the change must be purely additive: an entry the
//          server hints is `image/*` classifies as `image` today and must keep
//          doing so (SC-013, TestClassifyLibraryEntry_TableDiffIsExactlyIntended).
//        - Extension only, because everything downstream of this decision is
//          extension-derived: the Library's own content-type table and the §10.4
//          inline allow-list both key off the extension and the gateway sends
//          `nosniff` (FR-015b — no content sniffing anywhere on this path). A
//          mime hint that disagrees with the extension is precisely D15.4's
//          type-confusion case, so trusting it here would classify a file into a
//          surface the server will not serve the bytes for.

export const LIBRARY_PREVIEW_KINDS = [
  'image',
  'video',
  'html',
  'pdf',
  'audio',
  'base',
  'markdown',
  'mermaid',
  'text',
  'other',
] as const

// Derived from the runtime array rather than declared separately, so a new kind
// cannot be added to the type without also appearing in a list the exhaustiveness
// guards can iterate. TypeScript erases types; those guards need a real value.
export type LibraryPreviewKind = (typeof LIBRARY_PREVIEW_KINDS)[number]

const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif', 'ico'])
const VIDEO_EXTS = new Set(['mp4', 'webm', 'mov', 'mkv', 'avi', 'm4v', 'ogv'])
const HTML_EXTS = new Set(['html', 'htm'])
// The seven of spec §10.4's Media group and DS-5 case 8. `.aiff` is deliberately
// absent: the Library type table has no entry for it, so it stays a download card
// (E-14) rather than an <audio> element the browser refuses to play.
const AUDIO_EXTS = new Set(['mp3', 'm4a', 'aac', 'ogg', 'opus', 'wav', 'flac'])

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
  // Extension only, and below the two checks above — see the header note.
  if (HTML_EXTS.has(e)) return 'html'
  if (e === 'pdf') return 'pdf'
  if (AUDIO_EXTS.has(e)) return 'audio'
  // view-kinds-design-2026-09-03 §7 — a .base file opens as its views (tabs
  // over evaluated view results), never as a download card and never as raw
  // YAML-behind-Edit. Extension only, below the mime-driven checks, for the
  // same two reasons the D15 kinds are (see the header note): the change must
  // stay purely additive, and everything downstream keys off the extension.
  if (e === 'base') return 'base'
  if (e === 'md' || e === 'markdown') return 'markdown'
  if (e === 'mmd' || e === 'mermaid') return 'mermaid'
  if (entry.is_text_editable) return 'text'
  return 'other'
}
