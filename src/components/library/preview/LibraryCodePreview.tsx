// LibraryCodePreview — "any text file" view/edit (library-spec.md D-5 /
// section 4). View reuses ShikiCodeBlock VERBATIM — the same shared
// highlighted-code renderer historical-markdown.tsx's HistoricalCodeBlock and
// the live AssistantUI renderer both use (extracted to markdown-shared.tsx in
// ffa2bd27 precisely so this wouldn't need its own copy). Edit is CodeMirror
// with a best-effort language grammar (libraryLanguages.ts).
//
// Three UAT findings (2026-09-13) live in the VIEW side of this file:
//
//   D-38  A 1.6 MB single-line file rendered as a blank strip: the whole line
//         went into a non-wrapping <pre> eleven million pixels wide, which the
//         browser never painted, and nothing said why. Such a file is now
//         shown as PLAIN, WRAPPED text with a notice naming the reason, and
//         the highlighter is not asked to tokenise it at all.
//   D-118 Invalid JSON / YAML saved with no warning, then rendered as
//         confidently highlighted text under a `json` badge. The file is still
//         written byte-for-byte (no silent repair — that is correct); what was
//         missing is the sentence. The view now states that the file does not
//         parse, where, and offers Fix (→ Edit), the same shape the mermaid
//         preview already uses for a diagram that cannot be drawn.
//   D-103 The language badge react-shiki draws in the top-right corner sat at
//         3.81:1 against the pane — under the 4.5:1 AA floor. Overridden here
//         (this file owns the Library's use of the shared block; the chat
//         renderer is not touched) to the theme's muted text at 11 px.

import { useEffect, useState } from 'react'
import { ShikiCodeBlock } from '@/components/chat/markdown-shared'
import { LibraryTextPreview } from './LibraryTextPreview'
import { shikiLanguageFor } from './libraryLanguages'
import type { LibraryEntry } from '@/lib/api'

/** A line longer than this is never handed to the highlighter (D-38). */
export const LONG_LINE_CHARS = 10_000
/** A file larger than this (in UTF-16 units, ≈ bytes for ASCII) is shown
 *  plain: tokenising it would stall the tab for the sake of colour. */
export const HIGHLIGHT_MAX_CHARS = 512 * 1024

/**
 * Why a text is shown WITHOUT syntax highlighting, or null when it may be
 * highlighted. Pure; exported for the test.
 */
export function plainTextReason(text: string): string | null {
  if (text.length > HIGHLIGHT_MAX_CHARS) {
    return `this file is ${formatKiB(text.length)} — too large to highlight without stalling the page`
  }
  let lineStart = 0
  for (let i = 0; i <= text.length; i++) {
    if (i === text.length || text.charCodeAt(i) === 10) {
      if (i - lineStart > LONG_LINE_CHARS) {
        return `it has a line of ${(i - lineStart).toLocaleString('en-US')} characters, which the highlighter cannot lay out`
      }
      lineStart = i + 1
    }
  }
  return null
}

function formatKiB(chars: number): string {
  const kib = chars / 1024
  return kib >= 1024 ? `${(kib / 1024).toFixed(1)} MB` : `${Math.round(kib)} KB`
}

/** The JSON parse error for `text`, or null when it parses. Pure. */
export function jsonProblem(text: string): string | null {
  if (text.trim() === '') return null
  try {
    JSON.parse(text)
    return null
  } catch (err) {
    return err instanceof Error ? err.message : String(err)
  }
}

/**
 * The first YAML syntax error in `text` as "line N", or null when the
 * document parses. Uses the Lezer grammar `@codemirror/lang-yaml` already
 * ships for the editor — a real parser, loaded lazily (the same rule
 * LibraryCodeEditor.tsx follows: no top-level CodeMirror value import), not
 * a hand-rolled approximation of YAML. An error NODE in the tree is the
 * grammar's own "this could not be parsed" marker.
 */
export async function yamlProblem(text: string): Promise<string | null> {
  if (text.trim() === '') return null
  const { yamlLanguage } = await import('@codemirror/lang-yaml')
  const tree = yamlLanguage.parser.parse(text)
  let firstError = -1
  tree.iterate({
    enter(node) {
      if (firstError >= 0) return false
      if (node.type.isError) {
        firstError = node.from
        return false
      }
      return undefined
    },
  })
  if (firstError < 0) return null
  // The grammar recovers as far as it can, so an unterminated bracket or
  // quote is reported where the parser GAVE UP (often the end of the file),
  // not where the construct opened — hence "near", never "at".
  const line = text.slice(0, firstError).split('\n').length
  return `syntax error near line ${line}`
}

/** Languages this view VALIDATES (D-118). Everything else is shown as-is. */
type ValidatedLanguage = 'json' | 'yaml'

function validatedLanguage(language: string | undefined): ValidatedLanguage | null {
  if (language === 'json') return 'json'
  if (language === 'yaml') return 'yaml'
  return null
}

/**
 * The D-118 notice: "this file does not parse" + Fix. Rendered ABOVE the
 * highlighted source, never instead of it — the bytes are the reader's and
 * are always shown. Nothing is rendered while a parse is still pending or
 * when the file parses, so the common case costs no chrome.
 */
function CodeValidityNotice({
  language,
  text,
  onFix,
}: {
  language: ValidatedLanguage
  text: string
  onFix: () => void
}) {
  const [problem, setProblem] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    if (language === 'json') {
      setProblem(jsonProblem(text))
      return
    }
    setProblem(null)
    void yamlProblem(text).then((p) => {
      if (!cancelled) setProblem(p)
    })
    return () => {
      cancelled = true
    }
  }, [language, text])

  if (problem === null) return null
  const name = language === 'json' ? 'JSON' : 'YAML'
  return (
    <div
      role="status"
      data-testid="library-code-invalid-notice"
      className="mb-2 flex flex-wrap items-center gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 px-3 py-2 text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]"
    >
      <span className="min-w-0 flex-1">
        This file is not valid {name} — {problem}. It was saved exactly as written; nothing that
        reads it as {name} will accept it until this is fixed.
      </span>
      <button
        type="button"
        tabIndex={0}
        onClick={onFix}
        data-testid="library-code-invalid-fix"
        className="rounded border border-[var(--color-warning)]/60 px-2 py-0.5 text-[length:var(--type-caption-size)] font-medium hover:bg-[var(--color-warning)]/10"
      >
        Fix
      </button>
    </div>
  )
}

interface LibraryCodePreviewProps {
  workspaceId: string
  entry: LibraryEntry
  content: string
  onSaved?: (entry: LibraryEntry) => void
}

export function LibraryCodePreview({ workspaceId, entry, content, onSaved }: LibraryCodePreviewProps) {
  const language = shikiLanguageFor(entry.name)
  const validated = validatedLanguage(language)
  return (
    <LibraryTextPreview
      workspaceId={workspaceId}
      entry={entry}
      content={content}
      editorFilename={entry.name}
      onSaved={onSaved}
      renderView={(draft, { switchToEdit }) => {
        const plainReason = plainTextReason(draft)
        return (
          // D-103: the `[data-slot=language-label]` selectors reach the badge
          // react-shiki renders inside ShikiCodeBlock and lift it to the
          // theme's muted text (#9ca3af on the pane ≈ 8:1) at 11 px.
          <div
            data-testid="library-code-view"
            className="[&_[data-slot=language-label]]:!text-[length:var(--type-caption-size)] [&_[data-slot=language-label]]:!text-[var(--color-muted)]"
          >
            {validated !== null && plainReason === null && (
              <CodeValidityNotice language={validated} text={draft} onFix={switchToEdit} />
            )}
            {plainReason !== null ? (
              <>
                <p
                  role="status"
                  data-testid="library-code-plain-notice"
                  className="mb-2 text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
                >
                  Shown as plain text without highlighting: {plainReason}.
                </p>
                <pre
                  data-testid="library-code-plain"
                  className="whitespace-pre-wrap break-all rounded-md bg-[var(--color-surface-2)] px-4 py-3 font-mono text-[length:var(--type-caption-size)] leading-[1.65] text-[var(--color-secondary)]"
                >
                  {draft}
                </pre>
              </>
            ) : (
              <ShikiCodeBlock language={language} code={draft} />
            )}
          </div>
        )
      }}
    />
  )
}
