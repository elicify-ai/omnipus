// libraryLanguages.test.ts — smoke tests that the per-extension CodeMirror
// language loaders actually resolve to a usable Extension (not just that the
// map has an entry) and that Shiki alias mapping is correct. Every loader is
// backed by a REAL installed @codemirror/lang-* package (or
// @codemirror/legacy-modes), verified against each package's own shipped
// .d.ts before this map was written — see libraryLanguages.ts's doc comment.

import { describe, it, expect } from 'vitest'
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
})
