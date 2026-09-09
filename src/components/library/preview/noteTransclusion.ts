// noteTransclusion.ts — slices a transcluded note's SOURCE text down to the
// requested heading section or anchored block (ADR-083 US-7, EMB-055,
// EMB-059: "heading and block slicing MUST run client-side").
//
// This is deliberately a plain string/line operation, not a markdown-AST
// transform: the sliced result is fed straight back into
// `KnowledgeBaseMarkdown` as ordinary markdown source, so the ONLY job here
// is finding the right substring — parsing stays the composition's job, done
// exactly once, on the slice.
//
// Heading matching is ATX only (`# Heading`) — the vaults this feature reads
// are Obsidian exports, which write ATX headings exclusively; setext
// (`Heading\n===`) is not attempted, matching remarkKbFrontmatter's own
// documented scope (this file's sibling, knowledgeMarkdown.tsx).

export interface SlicedTransclusion {
  /** The slice to render, or '' when nothing was found. */
  text: string
  /** False means the fragment was asked for and not found — render a stated
   *  reason, never blank space (the qualitative prohibition every other part
   *  of this spec repeats). True covers both "no fragment was given" and "the
   *  fragment WAS found". */
  found: boolean
}

const ATX_HEADING_RE = /^(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$/
/** Obsidian's block-reference token: a `^id` at the very end of a line, made
 *  of word characters and hyphens, preceded by whitespace or starting the
 *  line. */
const BLOCK_ANCHOR_RE = /(?:^|[ \t])\^([A-Za-z0-9-]+)[ \t]*$/

function headingAt(line: string): { depth: number; text: string } | null {
  const m = ATX_HEADING_RE.exec(line)
  if (!m) return null
  return { depth: (m[1] as string).length, text: (m[2] as string).trim() }
}

/** The Results section and its subsections: the heading line itself, plus
 *  every line up to (not including) the next heading whose depth is <= this
 *  one's — or the end of the document. Matches the heading TEXT, exact first
 *  then case-insensitively, mirroring EMB-041's own label ladder. */
function sliceHeadingSection(content: string, heading: string): SlicedTransclusion {
  const lines = content.split(/\r?\n/)
  const wanted = heading.trim()
  const wantedLower = wanted.toLowerCase()

  let startLine = -1
  let startDepth = 0
  for (let i = 0; i < lines.length; i++) {
    const h = headingAt(lines[i] as string)
    if (!h) continue
    if (h.text === wanted) {
      startLine = i
      startDepth = h.depth
      break
    }
  }
  if (startLine === -1) {
    for (let i = 0; i < lines.length; i++) {
      const h = headingAt(lines[i] as string)
      if (!h) continue
      if (h.text.toLowerCase() === wantedLower) {
        startLine = i
        startDepth = h.depth
        break
      }
    }
  }
  if (startLine === -1) return { text: '', found: false }

  let endLine = lines.length
  for (let i = startLine + 1; i < lines.length; i++) {
    const h = headingAt(lines[i] as string)
    if (h && h.depth <= startDepth) {
      endLine = i
      break
    }
  }

  return { text: lines.slice(startLine, endLine).join('\n'), found: true }
}

/** Only the block anchored `^blockId`: the contiguous run of non-blank lines
 *  ending at the anchored line (i.e. the paragraph or list item that carries
 *  it), with the trailing `^blockId` token itself stripped — Obsidian shows
 *  the block's content, not its own address. */
function sliceBlock(content: string, blockId: string): SlicedTransclusion {
  const lines = content.split(/\r?\n/)
  let anchorLine = -1
  for (let i = 0; i < lines.length; i++) {
    const m = BLOCK_ANCHOR_RE.exec(lines[i] as string)
    if (m && m[1] === blockId) {
      anchorLine = i
      break
    }
  }
  if (anchorLine === -1) return { text: '', found: false }

  let startLine = anchorLine
  while (startLine > 0 && (lines[startLine - 1] as string).trim() !== '') startLine--

  const strippedLast = (lines[anchorLine] as string).replace(BLOCK_ANCHOR_RE, '').replace(/[ \t]+$/, '')
  const blockLines = lines.slice(startLine, anchorLine)
  blockLines.push(strippedLast)

  return { text: blockLines.join('\n'), found: true }
}

/** Slices `content` to the requested fragment. Undefined heading AND block
 *  means the whole note — used both for a bare `![[Note]]` embed and as the
 *  fallback when a caller passes neither. `block` takes priority when both
 *  are somehow present (parseWikilink never produces both, but this stays
 *  honest about the tie-break rather than silently picking one). */
export function sliceTranscludedContent(
  content: string,
  heading?: string,
  block?: string,
): SlicedTransclusion {
  if (block !== undefined && block !== '') return sliceBlock(content, block)
  if (heading !== undefined && heading !== '') return sliceHeadingSection(content, heading)
  return { text: content, found: true }
}
