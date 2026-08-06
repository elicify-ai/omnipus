// libraryPreviewKind.test.ts — classification (library-spec.md section 4's
// scope table). Pure function, no rendering — the actual per-kind rendering
// is covered end to end in ../LibraryPreviewPane.test.tsx.

import { describe, it, expect } from 'vitest'
import { classifyLibraryEntry, libraryEntryExt } from './libraryPreviewKind'

describe('libraryEntryExt', () => {
  it('lowercases and strips the leading dot', () => {
    expect(libraryEntryExt('Report.MD')).toBe('md')
    expect(libraryEntryExt('archive.tar.gz')).toBe('gz')
  })
  it('returns "" for a dotfile or an extensionless name', () => {
    expect(libraryEntryExt('.library')).toBe('')
    expect(libraryEntryExt('Makefile')).toBe('')
  })
})

describe('classifyLibraryEntry', () => {
  it('classifies images by extension and by mime', () => {
    expect(classifyLibraryEntry({ name: 'photo.png', is_text_editable: false })).toBe('image')
    expect(classifyLibraryEntry({ name: 'photo', mime: 'image/jpeg', is_text_editable: false })).toBe('image')
  })
  it('classifies video by extension and by mime', () => {
    expect(classifyLibraryEntry({ name: 'clip.mp4', is_text_editable: false })).toBe('video')
    expect(classifyLibraryEntry({ name: 'clip', mime: 'video/webm', is_text_editable: false })).toBe('video')
  })
  it('classifies .md/.markdown as markdown regardless of the is_text_editable hint', () => {
    expect(classifyLibraryEntry({ name: 'report.md', is_text_editable: false })).toBe('markdown')
    expect(classifyLibraryEntry({ name: 'report.markdown', is_text_editable: true })).toBe('markdown')
  })
  it('classifies .mmd/.mermaid as mermaid', () => {
    expect(classifyLibraryEntry({ name: 'diagram.mmd', is_text_editable: true })).toBe('mermaid')
    expect(classifyLibraryEntry({ name: 'diagram.mermaid', is_text_editable: true })).toBe('mermaid')
  })
  it('falls back to the server is_text_editable hint for anything else', () => {
    expect(classifyLibraryEntry({ name: 'main.ts', is_text_editable: true })).toBe('text')
    expect(classifyLibraryEntry({ name: 'archive.zip', is_text_editable: false })).toBe('other')
  })
})
