// LibraryMarkdownPreview — markdown view/edit (library-spec.md D-5 / section
// 4; ADR-067 §STAGE 1, FR-011 / FR-013a–d).
//
// ── Why this file no longer just calls HistoricalMessageMarkdown ─────────────
// It used to render the view slot as `<HistoricalMessageMarkdown content={…} />`
// — chat's finalized-message renderer, VERBATIM — and the header comment said
// that was "deliberately NOT a second markdown pipeline". That decision STANDS,
// and this file still honours it. What it forbids is a second PARSER, a second
// PLUGIN STACK and a second set of ELEMENT RENDERERS: those were hand-copied
// once and drifted three times (mermaid lost on finalize, the languageless-fence
// collapse, block code losing Shiki highlighting). Every one of those is still
// imported from the single source of truth, `chat/markdown-shared.tsx`.
//
// What it never forbade is a second COMPOSITION over those same shared
// definitions, and the seam for one already existed: `createLinkRenderer` is a
// factory taking a parameter, and chat's own map is itself assembled from
// `commonMarkdownComponents`. This is that third thin composition (FR-013a).
//
// It may diverge from chat's in EXACTLY TWO places (FR-013b) — anything else is
// a defect:
//   1. the `a` slot        — `KbMarkdownLink`, below;
//   2. appended remark plugins — `remarkStripPrivateComments`, below.
// The parser, the rehype stack, and every element renderer are chat's own.
//
// ── Why the two divergences exist ────────────────────────────────────────────
// (1) `%%comment%%` is Obsidian's private-aside syntax and a knowledge-base
//     reader must not show it (FR-011, US-3 AS-1). The strip is scoped to the
//     KB reader ONLY and must never reach chat: chat renders untrusted model and
//     tool output, where silently deleting the text between two markers hides
//     content FROM the reader rather than protecting them. Chat shows `%%x%%`
//     literally today and must keep doing so.
// (2) A relative link (`notes/plan.md`, `./a.md`, `#heading`) renders STRUCK
//     THROUGH in chat as "Link removed: unsafe URL scheme", because the shared
//     `isSafeHref` calls `new URL(href)` with no base and a relative href throws.
//     That rejection is an artefact of the parsing mechanism, not an authored
//     policy — the Go sanitiser over the same class of markdown
//     (`pkg/utils/markdown.go::isSafeHref`) accepts `scheme == ""`. `isSafeHref`
//     is test-asserted and consumed by chat, so it is NOT changed (FR-013 /
//     ADR-067 §2.4 "record, do not fix"); this file supplies its own `a` slot
//     instead and leaves the helper alone.
//
// ── Perf, non-negotiable ─────────────────────────────────────────────────────
// `kbMarkdownComponents` and both plugin arrays are MODULE-SCOPE CONSTANTS
// (FR-013c). react-markdown keys each entry by object reference and treats it as
// that node type's component type, so a map rebuilt per render remounts every
// element on every keystroke — and the view slot re-renders on every keystroke,
// because LibraryTextPreview renders the LIVE DRAFT. `historical-markdown.tsx`
// records the same constraint; it is not rediscovered here.

import { LibraryTextPreview } from './LibraryTextPreview'
import { KnowledgeNoteView } from '../knowledge/KnowledgeNoteView'
import type { LibraryEntry } from '@/lib/api'

// The composition itself lives in `kbMarkdownBase.tsx` and is re-exported here
// unchanged, so every existing importer and test keeps working. It moved for
// one reason: this file now imports the stage-2 reading view, which imports the
// composition — sharing a module would be an import cycle read at module scope.
// See kbMarkdownBase.tsx's header.
export {
  remarkStripPrivateComments,
  kbMarkdownComponents,
  KB_REMARK_PLUGINS,
  KB_REHYPE_PLUGINS,
  KnowledgeMarkdown,
} from './kbMarkdownBase'


interface LibraryMarkdownPreviewProps {
  workspaceId: string
  entry: LibraryEntry
  content: string
  onSaved?: (entry: LibraryEntry) => void
  /** Open another note from a link inside this one, workspace-relative. */
  onOpenNote?: (workspacePath: string) => void
}

/**
 * The Library's markdown pane: the editor shell, with the STAGE 2 knowledge
 * reading view in its view slot.
 *
 * ── Why the view slot is KnowledgeNoteView and not KnowledgeMarkdown ─────────
 * It used to be `<KnowledgeMarkdown content={draft} />`, and that made the whole
 * of stage 2 unreachable: KnowledgeReader, KnowledgeOutline, KnowledgeBacklinks
 * and `preview/knowledgeMarkdown.tsx` had no importer outside their own tests,
 * so a reader who clicked a search hit got a view with `[[Wikilinks]]` as
 * literal text, `> [!note]` as raw markers, `==highlight==` unrendered and
 * frontmatter drawn as a horizontal rule and a heading — while 138 passing tests
 * asserted otherwise about code that was not in the binary.
 *
 * The outline is offered for ANY markdown file (FR-062); search and linked
 * mentions switch on only inside a detected collection, which KnowledgeNoteView
 * decides from the outline response's own `is_knowledge_base`.
 */
export function LibraryMarkdownPreview({
  workspaceId,
  entry,
  content,
  onSaved,
  onOpenNote,
}: LibraryMarkdownPreviewProps) {
  return (
    <LibraryTextPreview
      workspaceId={workspaceId}
      entry={entry}
      content={content}
      editorFilename={entry.name}
      onSaved={onSaved}
      renderView={(draft) => (
        <KnowledgeNoteView
          workspaceId={workspaceId}
          notePath={entry.path}
          content={draft}
          {...(onOpenNote ? { onOpenNote } : {})}
        />
      )}
    />
  )
}
