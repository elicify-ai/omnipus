// libraryLanguages.ts — per-extension CodeMirror language-extension loaders
// (library-spec.md D-5's editor) AND the matching Shiki grammar name for the
// read-only view (ShikiCodeBlock, shared with chat — markdown-shared.tsx).
//
// Every loader is a SEPARATE dynamic import so Rollup/Vite gives each
// `@codemirror/lang-*` package its own chunk, fetched only the first time a
// file of that language is actually opened for editing — never eagerly
// bundled into the entry chunk (library-spec.md D-5 / this task's Vite-chunk
// requirement). Package exports were verified against each package's own
// shipped `.d.ts` (not assumed from memory) before wiring this map:
// `javascript()/cpp()/css()/go()/html()/java()/json()/markdown()/php()/
// python()/rust()/sql()/xml()/yaml()` are each that package's real named
// export, and `@codemirror/legacy-modes/mode/{shell,toml}` export a
// `StreamParser` (`shell`, `toml`) consumed via `StreamLanguage.define()` from
// `@codemirror/language` — there is no dedicated CodeMirror 6 package for
// either, unlike every other language below.
//
// Deliberately bounded to "markdown + the common code languages" (the spec's
// own phrasing) rather than a bundle-everything `@uiw/codemirror-extensions-
// langs`-style package (which imports its entire language set eagerly at
// module scope — exactly the "pull in every language pack eagerly" this task
// was told not to do).

import { bundledLanguages } from 'shiki'
import type { Extension } from '@codemirror/state'
import { libraryEntryExt } from './libraryPreviewKind'

type Loader = () => Promise<Extension>

const javascriptLoader = (opts?: { jsx?: boolean; typescript?: boolean }): Loader =>
  () => import('@codemirror/lang-javascript').then((m) => m.javascript(opts))

const streamLoader = (modeExport: 'shell' | 'toml'): Loader =>
  () =>
    Promise.all([import('@codemirror/language'), import(`@codemirror/legacy-modes/mode/${modeExport}`)]).then(
      ([{ StreamLanguage }, mod]) =>
        StreamLanguage.define((mod as Record<string, import('@codemirror/language').StreamParser<unknown>>)[modeExport]),
    )

/** extension (lowercase, no dot) -> CodeMirror language-extension loader. */
const LANGUAGE_LOADERS: Record<string, Loader> = {
  md: () => import('@codemirror/lang-markdown').then((m) => m.markdown()),
  markdown: () => import('@codemirror/lang-markdown').then((m) => m.markdown()),

  js: javascriptLoader(),
  mjs: javascriptLoader(),
  cjs: javascriptLoader(),
  jsx: javascriptLoader({ jsx: true }),
  ts: javascriptLoader({ typescript: true }),
  mts: javascriptLoader({ typescript: true }),
  cts: javascriptLoader({ typescript: true }),
  tsx: javascriptLoader({ typescript: true, jsx: true }),

  py: () => import('@codemirror/lang-python').then((m) => m.python()),
  json: () => import('@codemirror/lang-json').then((m) => m.json()),
  html: () => import('@codemirror/lang-html').then((m) => m.html()),
  htm: () => import('@codemirror/lang-html').then((m) => m.html()),
  css: () => import('@codemirror/lang-css').then((m) => m.css()),

  c: () => import('@codemirror/lang-cpp').then((m) => m.cpp()),
  h: () => import('@codemirror/lang-cpp').then((m) => m.cpp()),
  cpp: () => import('@codemirror/lang-cpp').then((m) => m.cpp()),
  cc: () => import('@codemirror/lang-cpp').then((m) => m.cpp()),
  cxx: () => import('@codemirror/lang-cpp').then((m) => m.cpp()),
  hpp: () => import('@codemirror/lang-cpp').then((m) => m.cpp()),

  go: () => import('@codemirror/lang-go').then((m) => m.go()),
  rs: () => import('@codemirror/lang-rust').then((m) => m.rust()),
  java: () => import('@codemirror/lang-java').then((m) => m.java()),
  php: () => import('@codemirror/lang-php').then((m) => m.php()),

  yaml: () => import('@codemirror/lang-yaml').then((m) => m.yaml()),
  yml: () => import('@codemirror/lang-yaml').then((m) => m.yaml()),
  sql: () => import('@codemirror/lang-sql').then((m) => m.sql()),
  xml: () => import('@codemirror/lang-xml').then((m) => m.xml()),

  sh: streamLoader('shell'),
  bash: streamLoader('shell'),
  zsh: streamLoader('shell'),
  toml: streamLoader('toml'),
}

/** Best-effort CodeMirror language extension for `filename`, or `[]` if none is
 * known — CodeMirror works fine with no language extension (plain text, still
 * gets line numbers / selection / editing from the shared basic setup). */
export async function loadLibraryLanguageExtension(filename: string): Promise<Extension[]> {
  const ext = libraryEntryExt(filename)
  const loader = LANGUAGE_LOADERS[ext]
  if (!loader) return []
  try {
    return [await loader()]
  } catch (err) {
    // A transient chunk-load failure degrades to plain text, not a crash —
    // mirrors mermaid-renderer.tsx's identical "don't cache a transient
    // import failure" reasoning.
    console.error('[library] language extension failed to load:', err)
    return []
  }
}

/** extension -> Shiki grammar name for the read-only view (ShikiCodeBlock).
 * Shiki already recognises most short names as-is; only the few real
 * mismatches are listed. An extension that resolves to no grammar Shiki
 * actually bundles yields `undefined`, which falls back to ShikiCodeBlock's
 * own 'text' default — see shikiLanguageFor below. */
const SHIKI_ALIASES: Record<string, string> = {
  mjs: 'js',
  cjs: 'js',
  mts: 'ts',
  cts: 'ts',
  h: 'c',
  hpp: 'cpp',
  cxx: 'cpp',
  cc: 'cpp',
  rs: 'rust',
  yml: 'yaml',
  htm: 'html',
  zsh: 'bash',
}

// WHAT THIS FUNCTION MAY RETURN, AND WHY IT IS CHECKED RATHER THAN ASSUMED
//
// It used to be `SHIKI_ALIASES[ext] ?? ext` — an unmapped extension returned
// ITSELF. The comment above already described the intended behaviour ("falls
// back to ShikiCodeBlock's own 'text' default") and the code did the opposite:
// `"base"` is a truthy string, so ShikiCodeBlock's `language || 'text'` guard
// never fired, Shiki was handed a grammar it does not have, and in a real
// browser it rendered NOTHING — an empty pane behind a `base` language chip.
// That is the 2026-09-05 view-kinds UAT's D2, and the pane it emptied is the
// LAST-RESORT one: "View raw" is reached precisely when everything else has
// already failed the reader.
//
// The blast radius was never just `.base`. EVERY extension absent from
// SHIKI_ALIASES and unknown to Shiki took the same path — `.conf`, `.env`, and
// anything else a vault happens to hold.
//
// So the grammar name is now checked against SHIKI'S OWN REGISTRY
// (`bundledLanguages`, the same map react-shiki's full bundle filters its
// `langs` through — react-shiki/dist/index.mjs imports it from 'shiki' at its
// top level, so this costs no extra bundle) instead of being guessed from the
// file name. Anything the registry does not hold returns `undefined`, and the
// `|| 'text'` fallback finally does its job.
export function shikiLanguageFor(filename: string): string | undefined {
  const ext = libraryEntryExt(filename)
  if (!ext) return undefined
  const grammar = SHIKI_ALIASES[ext] ?? ext
  return Object.prototype.hasOwnProperty.call(bundledLanguages, grammar) ? grammar : undefined
}
