// LibraryCodeEditor.tsx — the D-5 editor: CodeMirror 6 via @uiw/react-codemirror,
// lazy-loaded (React.lazy) so it and every @codemirror/lang-* grammar are
// fetched only once a user actually opens a file for editing, never bundled
// into the entry chunk (verify with `npx vite build` per this task's gate).
//
// IMPORTANT: this file must NOT have a top-level VALUE import of any
// `@codemirror/*` / `@uiw/*` package — a single static `import { EditorView }
// from '@codemirror/view'` (even just to build a theme object) pulls that
// code into whatever chunk contains THIS file, which is imported eagerly by
// every Library text preview (markdown/mermaid/code), not only when a user
// actually opens Edit mode. `import type` is fine (erased entirely at
// compile time — no runtime module resolution, no chunk-graph edge); only
// VALUE imports have this cost. `EditorView` is instead obtained from the
// same dynamic `import('@codemirror/view')` used to build the chrome theme,
// resolved once and cached at module scope.
//
// @uiw/react-codemirror's props (value/onChange/extensions/theme/height,
// default export) were verified against the package's own shipped
// `esm/useCodeMirror.d.ts` before writing this — not assumed from memory.

import { Suspense, lazy, useEffect, useState } from 'react'
import type { Extension } from '@codemirror/state'
import { loadLibraryLanguageExtension } from './libraryLanguages'

const ReactCodeMirror = lazy(() => import('@uiw/react-codemirror'))

// Lazily built once (module-level cache) and reused across every editor
// instance thereafter — avoids re-importing `@codemirror/view` per file open.
let chromeThemePromise: Promise<Extension[]> | null = null

function loadChromeExtensions(): Promise<Extension[]> {
  if (!chromeThemePromise) {
    chromeThemePromise = import('@codemirror/view').then(({ EditorView }) => {
      // Chrome-only theme layered on top of the built-in `theme="dark"`
      // (oneDark, via @codemirror/theme-one-dark) so the EDITOR'S OWN token
      // colors come from a proven CM6 dark theme, while the surrounding
      // chrome (background/gutter/selection/cursor) matches Sovereign Deep's
      // CSS variables like every sibling component (house rule: no default
      // Tailwind/theme colours). `var(--color-*)` resolves at paint time
      // exactly like any other CSS declaration — CodeMirror's
      // `EditorView.theme()` just generates a real stylesheet, unlike
      // Mermaid's inline-SVG renderer (why mermaid-renderer.tsx hardcodes
      // hex instead).
      const sovereignDeepChrome = EditorView.theme(
        {
          '&': {
            backgroundColor: 'var(--color-surface-2)',
            color: 'var(--color-secondary)',
            height: '100%',
            fontSize: '12.5px',
          },
          '.cm-scroller': {
            fontFamily: '"JetBrains Mono", "Fira Code", monospace',
          },
          '.cm-content': {
            caretColor: 'var(--color-accent)',
          },
          '.cm-cursor, .cm-dropCursor': {
            borderLeftColor: 'var(--color-accent)',
          },
          '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
            backgroundColor: 'rgba(212, 175, 55, 0.25)',
          },
          '.cm-gutters': {
            backgroundColor: 'var(--color-surface-1)',
            color: 'var(--color-muted)',
            border: 'none',
            borderRight: '1px solid var(--color-border)',
          },
          '.cm-activeLine': { backgroundColor: 'rgba(212, 175, 55, 0.06)' },
          '.cm-activeLineGutter': {
            backgroundColor: 'rgba(212, 175, 55, 0.08)',
            color: 'var(--color-secondary)',
          },
          '.cm-matchingBracket, .cm-nonmatchingBracket': {
            backgroundColor: 'rgba(212, 175, 55, 0.3)',
            outline: 'none',
          },
        },
        { dark: true },
      )
      return [sovereignDeepChrome, EditorView.lineWrapping]
    })
  }
  return chromeThemePromise
}

interface LibraryCodeEditorProps {
  value: string
  onChange: (value: string) => void
  /** Filename (or path) used to pick a language grammar — see libraryLanguages.ts. */
  filename: string
  readOnly?: boolean
}

function EditorFallback() {
  return (
    <div
      className="flex h-full min-h-[200px] items-center justify-center text-xs text-[var(--color-muted)]"
      data-testid="library-editor-loading"
    >
      Loading editor…
    </div>
  )
}

function LibraryCodeEditorImpl({ value, onChange, filename, readOnly = false }: LibraryCodeEditorProps) {
  const [extensions, setExtensions] = useState<Extension[]>([])

  useEffect(() => {
    let cancelled = false
    setExtensions([])
    void Promise.all([loadChromeExtensions(), loadLibraryLanguageExtension(filename)]).then(
      ([chrome, lang]) => {
        if (!cancelled) setExtensions([...chrome, ...lang])
      },
    )
    return () => {
      cancelled = true
    }
  }, [filename])

  return (
    <Suspense fallback={<EditorFallback />}>
      <div className="h-full min-h-[200px]" data-testid="library-code-editor">
        <ReactCodeMirror
          value={value}
          onChange={onChange}
          theme="dark"
          extensions={extensions}
          height="100%"
          readOnly={readOnly}
          basicSetup={{ foldGutter: true, highlightActiveLine: true }}
        />
      </div>
    </Suspense>
  )
}

export const LibraryCodeEditor = LibraryCodeEditorImpl
