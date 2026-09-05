// libraryLanguages.test.ts — smoke tests that the per-extension CodeMirror
// language loaders actually resolve to a usable Extension (not just that the
// map has an entry) and that Shiki alias mapping is correct. Every loader is
// backed by a REAL installed @codemirror/lang-* package (or
// @codemirror/legacy-modes), verified against each package's own shipped
// .d.ts before this map was written — see libraryLanguages.ts's doc comment.

import { describe, it, expect } from 'vitest'
import { bundledLanguages } from 'shiki'
import { loadLibraryLanguageExtension, shikiLanguageFor } from './libraryLanguages'

describe('loadLibraryLanguageExtension', () => {
  it('resolves a real extension for a known language', async () => {
    const md = await loadLibraryLanguageExtension('report.md')
    expect(md.length).toBe(1)

    const ts = await loadLibraryLanguageExtension('main.ts')
    expect(ts.length).toBe(1)

    const shell = await loadLibraryLanguageExtension('deploy.sh')
    expect(shell.length).toBe(1)
  })

  it('returns [] (plain text) for an unknown extension instead of throwing', async () => {
    await expect(loadLibraryLanguageExtension('README')).resolves.toEqual([])
    await expect(loadLibraryLanguageExtension('data.unknownext')).resolves.toEqual([])
  })
})

describe('shikiLanguageFor', () => {
  it('maps the known mismatches to their Shiki alias', () => {
    expect(shikiLanguageFor('main.mjs')).toBe('js')
    expect(shikiLanguageFor('app.mts')).toBe('ts')
    expect(shikiLanguageFor('lib.rs')).toBe('rust')
    expect(shikiLanguageFor('config.yml')).toBe('yaml')
  })
  it('passes an unmapped extension straight through', () => {
    expect(shikiLanguageFor('main.go')).toBe('go')
    expect(shikiLanguageFor('script.py')).toBe('py')
  })
  it('returns undefined for an extensionless name (ShikiCodeBlock defaults to "text")', () => {
    expect(shikiLanguageFor('Makefile')).toBeUndefined()
  })

  // ── The 2026-09-05 view-kinds UAT, D2 ────────────────────────────────────
  // The old implementation was `SHIKI_ALIASES[ext] ?? ext`, so an unmapped
  // extension returned ITSELF. `"base"` is truthy, so ShikiCodeBlock's
  // `language || 'text'` guard never fired, Shiki was handed a grammar it does
  // not have, and the browser rendered an empty pane — on the LAST-RESORT
  // "View raw" surface. The oracle below is Shiki's OWN registry, not a list
  // remembered here: whatever this function returns must be something Shiki
  // can be asked for.
  it('returns undefined for an extension Shiki has no grammar for', () => {
    expect(shikiLanguageFor('Damaged.base')).toBeUndefined()
    expect(shikiLanguageFor('app.conf')).toBeUndefined()
    expect(shikiLanguageFor('.env')).toBeUndefined()
    expect(shikiLanguageFor('notes.unknownext')).toBeUndefined()
  })

  it('never returns a grammar name Shiki does not bundle, for any extension', () => {
    const extensions = [
      // every key this module maps for the editor, plus the extensions the
      // UAT named as sharing the blast radius, plus a few invented ones.
      'md', 'markdown', 'js', 'mjs', 'cjs', 'jsx', 'ts', 'mts', 'cts', 'tsx',
      'py', 'json', 'html', 'htm', 'css', 'c', 'h', 'cpp', 'cc', 'cxx', 'hpp',
      'go', 'rs', 'java', 'php', 'yaml', 'yml', 'sql', 'xml', 'sh', 'bash',
      'zsh', 'toml',
      'base', 'conf', 'ini', 'env', 'canvas', 'excalidraw', 'zzz',
    ]
    for (const ext of extensions) {
      const grammar = shikiLanguageFor(`sample.${ext}`)
      if (grammar === undefined) continue
      expect(
        Object.prototype.hasOwnProperty.call(bundledLanguages, grammar),
        `.${ext} resolved to the grammar "${grammar}", which Shiki does not bundle`,
      ).toBe(true)
    }
  })

  it('still resolves the languages Shiki does bundle, so the guard is not a blanket give-up', () => {
    expect(shikiLanguageFor('main.go')).toBe('go')
    expect(shikiLanguageFor('config.yml')).toBe('yaml')
    expect(shikiLanguageFor('settings.ini')).toBe('ini')
    expect(shikiLanguageFor('index.htm')).toBe('html')
  })
})
