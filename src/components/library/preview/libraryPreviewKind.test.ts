// libraryPreviewKind.test.ts — classification (library-spec.md section 4's
// scope table, extended by ADR-067 D15 / spec FR-001, FR-018). Pure function,
// no rendering for the classifier itself — the actual per-kind rendering is
// covered end to end in ../LibraryPreviewPane.test.tsx.
//
// `classifyLibraryEntry` is rated HIGH risk by impact analysis (5 dependents,
// 2 direct: LibraryEntryRow and LibraryPreviewPane). It is the single decision
// point for how every Library file renders, so this file carries three guards
// beyond the ordinary unit tests, in the order they are most likely to save us:
//
//   1. TableDiffIsExactlyIntended (spec test 1, SC-013) — the whole
//      classification table, before vs after, diffed. Exactly three groups may
//      change; a fourth fails, and so does a missing one.
//   2. LibraryRow_MediaThumbnailKindsUnchanged (spec test 88, SC-016) — the
//      row's inline-thumbnail predicate still admits exactly image and video.
//   3. LibraryPreviewPane_NoUnhandledKind (spec test 89, SC-017) — every member
//      of the widened union is actually dispatched by the pane.
//
// Guards 2 and 3 exist because widening a union has consequences TypeScript
// cannot catch, and both fail SILENTLY. A predicate written as
// `kind === 'image' || kind === 'video'` keeps compiling while being wrong for
// the new members — nothing in the type system has an opinion about which kinds
// belong in it. A pane that dispatches with `{kind === '…' && …}` chains
// compiles clean and renders an empty pane for a kind nobody wired up; the pane
// has since moved to an exhaustive `switch`, which the compiler DOES check, so
// guard 3's job is now to notice if that ever regresses (see its own note).

import { describe, it, expect } from 'vitest'
import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { createElement } from 'react'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  classifyLibraryEntry,
  libraryEntryExt,
  LIBRARY_PREVIEW_KINDS,
} from './libraryPreviewKind'
import type { LibraryPreviewKind } from './libraryPreviewKind'
import { LibraryEntryRow } from '../LibraryEntryRow'
import type { LibraryEntry } from '@/lib/api'

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

// ---------------------------------------------------------------------------
// Spec test 2 — TestClassifyLibraryEntry_NewKinds (US-1 AS-1, AS-5, AS-6)
// ---------------------------------------------------------------------------

const AUDIO_EXTENSIONS = ['mp3', 'm4a', 'aac', 'ogg', 'opus', 'wav', 'flac'] as const

describe('classifyLibraryEntry — the three new kinds (ADR-067 D15)', () => {
  it('classifies .html/.htm as html regardless of the is_text_editable hint', () => {
    // Dies on: dropping the HTML_EXTS branch, or placing it below the
    // `entry.is_text_editable` fallback (an editable .html would be 'text').
    expect(classifyLibraryEntry({ name: 'page.html', is_text_editable: false })).toBe('html')
    expect(classifyLibraryEntry({ name: 'page.html', is_text_editable: true })).toBe('html')
    expect(classifyLibraryEntry({ name: 'page.htm', is_text_editable: false })).toBe('html')
    expect(classifyLibraryEntry({ name: 'PAGE.HTM', is_text_editable: false })).toBe('html')
  })

  it('classifies .pdf as pdf, never as a download card', () => {
    // Dies on: removing the pdf branch (falls through to 'other', i.e. the
    // download card US-1 AS-5 exists to replace).
    expect(classifyLibraryEntry({ name: 'doc.pdf', is_text_editable: false })).toBe('pdf')
    expect(classifyLibraryEntry({ name: 'doc.PDF', is_text_editable: false })).toBe('pdf')
    expect(classifyLibraryEntry({ name: 'doc.pdf', is_text_editable: true })).toBe('pdf')
  })

  it('classifies each of the seven supported audio extensions as audio', () => {
    // Dies on: dropping any one extension from AUDIO_EXTS. Spec DS-5 case 8
    // requires all seven to play; a set missing one renders a download card for
    // a file the server is perfectly willing to stream.
    for (const ext of AUDIO_EXTENSIONS) {
      expect(classifyLibraryEntry({ name: `song.${ext}`, is_text_editable: false })).toBe('audio')
      expect(classifyLibraryEntry({ name: `SONG.${ext.toUpperCase()}`, is_text_editable: false })).toBe('audio')
    }
  })

  it('leaves .aiff a download card — it is not one of the seven (spec DS-5 case 9)', () => {
    // Dies on: widening AUDIO_EXTS beyond the Library type table. An <audio>
    // element for a type the gateway serves as application/octet-stream is a
    // silently broken player, which is worse than the download card.
    expect(classifyLibraryEntry({ name: 'audio.aiff', is_text_editable: false })).toBe('other')
  })

  it('keeps .svg an image — LOAD-BEARING, not incidental (spec §10.4, test 123)', () => {
    // Dies on: reclassifying .svg as 'html' on the reasoning that SVG is
    // scriptable. Inside the SPA an image is drawn in an <img>, which renders
    // SVG in secure static mode and never runs its scripts; routing it to the
    // active-document surface would make the reader a script host instead.
    expect(classifyLibraryEntry({ name: 'logo.svg', is_text_editable: false })).toBe('image')
    expect(classifyLibraryEntry({ name: 'logo.svg', is_text_editable: true })).toBe('image')
  })

  it('decides the new kinds on the extension alone, never on the mime hint', () => {
    // Two properties in one, both dying on a `mime.startsWith('audio/')`-style
    // branch or on moving the new checks above the image/video mime checks:
    //   - a mime hint that disagrees with the extension does not win (FR-015b:
    //     the Library type table and the §10.4 allow-list are extension-derived
    //     and the gateway sends nosniff, so the surface must agree with them);
    //   - the pre-existing mime-driven image/video checks still win, which is
    //     what keeps this change purely additive.
    expect(classifyLibraryEntry({ name: 'page.html', mime: 'image/png', is_text_editable: false })).toBe('image')
    expect(classifyLibraryEntry({ name: 'song.mp3', mime: 'video/mp4', is_text_editable: false })).toBe('video')
    expect(classifyLibraryEntry({ name: 'clip', mime: 'audio/mpeg', is_text_editable: false })).toBe('other')
    expect(classifyLibraryEntry({ name: 'page', mime: 'text/html', is_text_editable: false })).toBe('other')
  })

  it('classifies .base as base regardless of the is_text_editable hint (view-kinds §7)', () => {
    // A .base file opens as its views — tabs over evaluated view results —
    // never as raw YAML behind Edit and never as a download card.
    expect(classifyLibraryEntry({ name: 'Invoices.base', is_text_editable: true })).toBe('base')
    expect(classifyLibraryEntry({ name: 'Invoices.base', is_text_editable: false })).toBe('base')
    expect(classifyLibraryEntry({ name: 'INVOICES.BASE', is_text_editable: false })).toBe('base')
  })

  it('decides base on the extension alone, never on the mime hint', () => {
    // The mime-driven image/video checks still win (purely additive change),
    // and no mime hint can make a non-.base file a base surface.
    expect(classifyLibraryEntry({ name: 'Invoices.base', mime: 'image/png', is_text_editable: false })).toBe('image')
    expect(classifyLibraryEntry({ name: 'notes.txt', mime: 'application/x-base', is_text_editable: true })).toBe('text')
  })

  it('returns only kinds that are members of LIBRARY_PREVIEW_KINDS', () => {
    for (const c of CLASSIFICATION_CORPUS) {
      expect(LIBRARY_PREVIEW_KINDS as readonly string[]).toContain(
        classifyLibraryEntry({ name: c.name, mime: c.mime, is_text_editable: c.is_text_editable }),
      )
    }
  })
})

// ---------------------------------------------------------------------------
// Spec test 1 — TestClassifyLibraryEntry_TableDiffIsExactlyIntended
// (SC-013, HIGH-risk release gate)
// ---------------------------------------------------------------------------
//
// The shape of this guard matters. "Zero diffs" was the original wording and it
// could only ever fail: FR-001 and FR-018 REQUIRE three groups to change kind.
// A guard written to that wording is deleted or weakened the moment the feature
// lands, which is how a HIGH-risk gate becomes decoration. So: the whole table
// is snapshotted, and the diff must be EXACTLY the intended set — an unintended
// fourth row fails, a missing intended row fails, and an intended row that lands
// on the wrong kind fails.
//
// PRE_CHANGE_CLASSIFICATION below was generated mechanically from the committed
// (pre-change) implementation — `git show HEAD:…/libraryPreviewKind.ts` piped
// through this exact corpus — before a line of the feature was written. It is a
// record of what the function used to do, not a description of what it does now;
// nothing in it may be "corrected" to make a test pass. If a row here looks
// wrong, the change is wrong.

interface CorpusCase {
  label: string
  name: string
  mime?: string
  is_text_editable: boolean
}

const CLASSIFICATION_CORPUS: CorpusCase[] = [
  // --- images (extension and mime driven) ---
  { label: 'photo.png', name: 'photo.png', is_text_editable: false },
  { label: 'photo.PNG (uppercase)', name: 'photo.PNG', is_text_editable: false },
  { label: 'photo.jpg', name: 'photo.jpg', is_text_editable: false },
  { label: 'photo.jpeg', name: 'photo.jpeg', is_text_editable: false },
  { label: 'photo.gif', name: 'photo.gif', is_text_editable: false },
  { label: 'photo.webp', name: 'photo.webp', is_text_editable: false },
  { label: 'photo.bmp', name: 'photo.bmp', is_text_editable: false },
  { label: 'photo.avif', name: 'photo.avif', is_text_editable: false },
  { label: 'favicon.ico', name: 'favicon.ico', is_text_editable: false },
  { label: 'logo.svg (LOAD-BEARING: must stay image)', name: 'logo.svg', is_text_editable: false },
  { label: 'logo.svg marked text-editable', name: 'logo.svg', is_text_editable: true },
  { label: 'extensionless, mime image/jpeg', name: 'scan', mime: 'image/jpeg', is_text_editable: false },
  // --- video (extension and mime driven) ---
  { label: 'clip.mp4', name: 'clip.mp4', is_text_editable: false },
  { label: 'clip.webm', name: 'clip.webm', is_text_editable: false },
  { label: 'clip.mov', name: 'clip.mov', is_text_editable: false },
  { label: 'clip.mkv', name: 'clip.mkv', is_text_editable: false },
  { label: 'clip.avi', name: 'clip.avi', is_text_editable: false },
  { label: 'clip.m4v', name: 'clip.m4v', is_text_editable: false },
  { label: 'clip.ogv (video, not audio)', name: 'clip.ogv', is_text_editable: false },
  { label: 'extensionless, mime video/webm', name: 'recording', mime: 'video/webm', is_text_editable: false },
  // --- markdown / mermaid ---
  { label: 'report.md', name: 'report.md', is_text_editable: false },
  { label: 'report.markdown', name: 'report.markdown', is_text_editable: true },
  { label: 'diagram.mmd', name: 'diagram.mmd', is_text_editable: true },
  { label: 'diagram.mermaid', name: 'diagram.mermaid', is_text_editable: false },
  // --- text via the server hint ---
  { label: 'main.ts', name: 'main.ts', is_text_editable: true },
  { label: 'notes.txt', name: 'notes.txt', is_text_editable: true },
  { label: 'style.css', name: 'style.css', is_text_editable: true },
  { label: 'script.js', name: 'script.js', is_text_editable: true },
  { label: 'data.json', name: 'data.json', is_text_editable: true },
  { label: 'Makefile (no extension, editable)', name: 'Makefile', is_text_editable: true },
  // --- other ---
  { label: 'archive.zip', name: 'archive.zip', is_text_editable: false },
  { label: 'archive.tar.gz', name: 'archive.tar.gz', is_text_editable: false },
  { label: 'audio.aiff (NOT one of the seven)', name: 'audio.aiff', is_text_editable: false },
  { label: 'blob.bin', name: 'blob.bin', is_text_editable: false },
  { label: '.library (dotfile)', name: '.library', is_text_editable: false },
  { label: 'font.woff2', name: 'font.woff2', is_text_editable: false },
  // --- HTML: the first intended diff group ---
  { label: 'page.html', name: 'page.html', is_text_editable: false },
  { label: 'page.html marked text-editable', name: 'page.html', is_text_editable: true },
  { label: 'page.htm', name: 'page.htm', is_text_editable: false },
  { label: 'page.HTM (uppercase)', name: 'page.HTM', is_text_editable: true },
  // --- PDF: the second intended diff group ---
  { label: 'doc.pdf', name: 'doc.pdf', is_text_editable: false },
  { label: 'doc.pdf marked text-editable', name: 'doc.pdf', is_text_editable: true },
  { label: 'doc.PDF (uppercase)', name: 'doc.PDF', is_text_editable: false },
  // --- audio: the third intended diff group (the seven of DS-5 case 8) ---
  { label: 'song.mp3', name: 'song.mp3', is_text_editable: false },
  { label: 'song.m4a', name: 'song.m4a', is_text_editable: false },
  { label: 'song.aac', name: 'song.aac', is_text_editable: false },
  { label: 'song.ogg', name: 'song.ogg', is_text_editable: false },
  { label: 'song.opus', name: 'song.opus', is_text_editable: false },
  { label: 'song.wav', name: 'song.wav', is_text_editable: false },
  { label: 'song.flac', name: 'song.flac', is_text_editable: false },
  { label: 'song.MP3 (uppercase)', name: 'song.MP3', is_text_editable: false },
  { label: 'song.wav marked text-editable', name: 'song.wav', is_text_editable: true },
  // --- precedence locks: the mime-driven image/video checks must keep winning ---
  { label: 'page.html with mime image/png', name: 'page.html', mime: 'image/png', is_text_editable: false },
  { label: 'doc.pdf with mime image/png', name: 'doc.pdf', mime: 'image/png', is_text_editable: false },
  { label: 'song.mp3 with mime video/mp4', name: 'song.mp3', mime: 'video/mp4', is_text_editable: false },
  // --- mime-widening tripwires: extensionless files the server hints at. The
  // three new kinds are extension-only, so every one of these must stay where
  // it is; a `mime.startsWith('audio/')`-style branch moves them and shows up
  // as an unintended fourth diff group.
  { label: 'extensionless, mime audio/mpeg', name: 'clip', mime: 'audio/mpeg', is_text_editable: false },
  { label: 'extensionless, mime audio/mpeg, editable', name: 'clip', mime: 'audio/mpeg', is_text_editable: true },
  { label: 'extensionless, mime application/pdf', name: 'doc', mime: 'application/pdf', is_text_editable: false },
  { label: 'extensionless, mime text/html', name: 'page', mime: 'text/html', is_text_editable: false },
  { label: 'extensionless, mime text/html, editable', name: 'page', mime: 'text/html', is_text_editable: true },
  // --- .base: the view-kinds diff group (view-kinds-design-2026-09-03 §7) ---
  { label: 'Invoices.base', name: 'Invoices.base', is_text_editable: false },
  { label: 'Invoices.base marked text-editable', name: 'Invoices.base', is_text_editable: true },
  { label: 'INVOICES.BASE (uppercase)', name: 'INVOICES.BASE', is_text_editable: false },
  { label: 'Invoices.base with mime image/png', name: 'Invoices.base', mime: 'image/png', is_text_editable: false },
]

/** Generated from HEAD's classifier before the ADR-067 change. Do not edit. */
const PRE_CHANGE_CLASSIFICATION: Record<string, LibraryPreviewKind> = {
  "photo.png": 'image',
  "photo.PNG (uppercase)": 'image',
  "photo.jpg": 'image',
  "photo.jpeg": 'image',
  "photo.gif": 'image',
  "photo.webp": 'image',
  "photo.bmp": 'image',
  "photo.avif": 'image',
  "favicon.ico": 'image',
  "logo.svg (LOAD-BEARING: must stay image)": 'image',
  "logo.svg marked text-editable": 'image',
  "extensionless, mime image/jpeg": 'image',
  "clip.mp4": 'video',
  "clip.webm": 'video',
  "clip.mov": 'video',
  "clip.mkv": 'video',
  "clip.avi": 'video',
  "clip.m4v": 'video',
  "clip.ogv (video, not audio)": 'video',
  "extensionless, mime video/webm": 'video',
  "report.md": 'markdown',
  "report.markdown": 'markdown',
  "diagram.mmd": 'mermaid',
  "diagram.mermaid": 'mermaid',
  "main.ts": 'text',
  "notes.txt": 'text',
  "style.css": 'text',
  "script.js": 'text',
  "data.json": 'text',
  "Makefile (no extension, editable)": 'text',
  "archive.zip": 'other',
  "archive.tar.gz": 'other',
  "audio.aiff (NOT one of the seven)": 'other',
  "blob.bin": 'other',
  ".library (dotfile)": 'other',
  "font.woff2": 'other',
  "page.html": 'other',
  "page.html marked text-editable": 'text',
  "page.htm": 'other',
  "page.HTM (uppercase)": 'text',
  "doc.pdf": 'other',
  "doc.pdf marked text-editable": 'text',
  "doc.PDF (uppercase)": 'other',
  "song.mp3": 'other',
  "song.m4a": 'other',
  "song.aac": 'other',
  "song.ogg": 'other',
  "song.opus": 'other',
  "song.wav": 'other',
  "song.flac": 'other',
  "song.MP3 (uppercase)": 'other',
  "song.wav marked text-editable": 'text',
  "page.html with mime image/png": 'image',
  "doc.pdf with mime image/png": 'image',
  "song.mp3 with mime video/mp4": 'video',
  "extensionless, mime audio/mpeg": 'other',
  "extensionless, mime audio/mpeg, editable": 'text',
  "extensionless, mime application/pdf": 'other',
  "extensionless, mime text/html": 'other',
  "extensionless, mime text/html, editable": 'text',
  // Generated the same way for the view-kinds change: what the classifier did
  // to .base files before the 'base' kind existed.
  "Invoices.base": 'other',
  "Invoices.base marked text-editable": 'text',
  "INVOICES.BASE (uppercase)": 'other',
  "Invoices.base with mime image/png": 'image',
}

/**
 * The only rows ADR-067 is allowed to move, and where each must land.
 * Three groups: `.html`/`.htm`, `.pdf`, and the seven audio extensions —
 * plus one later group, `.base` (view-kinds-design-2026-09-03 §7), added the
 * same way: fixture rows recorded from the pre-change classifier, intended
 * landing spot declared here. The mime-precedence row (`Invoices.base with
 * mime image/png`) is deliberately NOT in this set: it must stay `image`.
 */
const INTENDED_DIFF: Record<string, LibraryPreviewKind> = {
  'Invoices.base': 'base',
  'Invoices.base marked text-editable': 'base',
  'INVOICES.BASE (uppercase)': 'base',
  'page.html': 'html',
  'page.html marked text-editable': 'html',
  'page.htm': 'html',
  'page.HTM (uppercase)': 'html',
  'doc.pdf': 'pdf',
  'doc.pdf marked text-editable': 'pdf',
  'doc.PDF (uppercase)': 'pdf',
  'song.mp3': 'audio',
  'song.m4a': 'audio',
  'song.aac': 'audio',
  'song.ogg': 'audio',
  'song.opus': 'audio',
  'song.wav': 'audio',
  'song.flac': 'audio',
  'song.MP3 (uppercase)': 'audio',
  'song.wav marked text-editable': 'audio',
}

describe('TestClassifyLibraryEntry_TableDiffIsExactlyIntended (SC-013)', () => {
  it('the fixture and the corpus describe the same set of cases', () => {
    // A missing fixture row would silently drop a case out of the diff, so the
    // guard would stop covering it without failing. Dies on: adding a corpus
    // case without regenerating the fixture, or vice versa.
    const corpusLabels = CLASSIFICATION_CORPUS.map((c) => c.label).sort()
    const fixtureLabels = Object.keys(PRE_CHANGE_CLASSIFICATION).sort()
    expect(new Set(corpusLabels).size).toBe(corpusLabels.length) // labels unique
    expect(fixtureLabels).toEqual(corpusLabels)
  })

  it('differs from the pre-change table in exactly the three intended groups', () => {
    // Dies on: any classification change outside the three groups (e.g. adding
    // a mime-driven audio branch, which also moves `clip` with mime audio/mpeg;
    // or reclassifying .svg, which moves two image rows), AND on any intended
    // row failing to move (e.g. dropping `.htm` from HTML_EXTS).
    const actualDiff: Record<string, LibraryPreviewKind> = {}
    for (const c of CLASSIFICATION_CORPUS) {
      const now = classifyLibraryEntry({ name: c.name, mime: c.mime, is_text_editable: c.is_text_editable })
      if (now !== PRE_CHANGE_CLASSIFICATION[c.label]) actualDiff[c.label] = now
    }
    expect(actualDiff).toEqual(INTENDED_DIFF)
  })

  it('leaves every row outside those three groups byte-identical to the fixture', () => {
    // The same property stated from the other side, so a failure names the row
    // that moved rather than dumping two maps.
    for (const c of CLASSIFICATION_CORPUS) {
      if (c.label in INTENDED_DIFF) continue
      const now = classifyLibraryEntry({ name: c.name, mime: c.mime, is_text_editable: c.is_text_editable })
      expect(`${c.label} -> ${now}`).toBe(`${c.label} -> ${PRE_CHANGE_CLASSIFICATION[c.label]}`)
    }
  })
})

// ---------------------------------------------------------------------------
// Spec test 88 — TestLibraryRow_MediaThumbnailKindsUnchanged (SC-016)
// ---------------------------------------------------------------------------
//
// LibraryEntryRow uses the kind for exactly one thing: `isMedia`, which decides
// whether the row loads the file itself as an inline thumbnail. It must stay
// exactly `image` and `video`. Someone reasonably reading "audio is media"
// would make every audio and PDF row download the whole file into an <img> that
// cannot render it — and the row would still look correct, because the <img>
// error handler falls back to the type glyph. The cost is invisible: a folder of
// podcasts becomes a folder of full downloads just by being listed.
//
// LibraryEntryRow is NOT owned by this change; this guard is here so the
// property is asserted somewhere rather than nowhere.

/** One sample filename per kind. Keyed so a new kind cannot skip the guard. */
const SAMPLE_PER_KIND: Record<LibraryPreviewKind, { name: string; is_text_editable: boolean }> = {
  image: { name: 'photo.png', is_text_editable: false },
  video: { name: 'clip.mp4', is_text_editable: false },
  html: { name: 'page.html', is_text_editable: false },
  pdf: { name: 'doc.pdf', is_text_editable: false },
  audio: { name: 'song.mp3', is_text_editable: false },
  base: { name: 'Invoices.base', is_text_editable: true },
  markdown: { name: 'report.md', is_text_editable: true },
  mermaid: { name: 'diagram.mmd', is_text_editable: true },
  text: { name: 'main.ts', is_text_editable: true },
  other: { name: 'archive.zip', is_text_editable: false },
}

const KINDS_WITH_INLINE_THUMBNAILS: readonly LibraryPreviewKind[] = ['image', 'video']

// No `as LibraryEntry` cast: the cast is what would let a wire-type field go
// missing without the compiler noticing, and this fixture stands in for a real
// directory-listing entry.
function makeEntry(over: Partial<LibraryEntry> & { name: string; is_text_editable: boolean }): LibraryEntry {
  return {
    path: over.name,
    is_dir: false,
    is_hidden: false,
    size: 1024,
    modified_at: '2026-08-22T10:15:00Z',
    ...over,
  }
}

function renderRow(entry: LibraryEntry) {
  // LibraryEntryRow reads the knowledge-base-info query cache (C3, a passive
  // read for Vault-icon detection — see LibraryEntryRow.tsx), so it now
  // needs a QueryClientProvider ancestor even where no query actually runs.
  // Not this file's own concern (SC-016 is about thumbnail-kind coverage),
  // but required scaffolding for any render of the row.
  return render(
    createElement(
      QueryClientProvider,
      { client: new QueryClient() },
      createElement(LibraryEntryRow, {
        workspaceId: 'ws-1',
        entry,
        selected: false,
        onOpenDirectory: () => {},
        onSelectFile: () => {},
        onDownload: () => {},
        onRename: () => {},
        onTransfer: () => {},
        onDelete: () => {},
      }),
    ),
  )
}

describe('TestLibraryRow_MediaThumbnailKindsUnchanged (SC-016)', () => {
  it('covers every kind in the union, so a new kind cannot slip past this guard', () => {
    expect(Object.keys(SAMPLE_PER_KIND).sort()).toEqual([...LIBRARY_PREVIEW_KINDS].sort())
  })

  it('samples classify to the kind they claim', () => {
    // Without this the guard could pass while testing nine files that all
    // classify as 'other'.
    for (const kind of LIBRARY_PREVIEW_KINDS) {
      const s = SAMPLE_PER_KIND[kind]
      expect(classifyLibraryEntry({ name: s.name, is_text_editable: s.is_text_editable })).toBe(kind)
    }
  })

  it('loads an inline thumbnail for exactly image and video, after the union widened', () => {
    // Dies on: LibraryEntryRow.tsx changing `isMedia` to
    // `kind === 'image' || kind === 'video' || kind === 'audio'` — verified by
    // making that edit and watching this assertion fail on song.mp3.
    const withThumbnail: LibraryPreviewKind[] = []
    for (const kind of LIBRARY_PREVIEW_KINDS) {
      const s = SAMPLE_PER_KIND[kind]
      const entry = makeEntry({ name: s.name, is_text_editable: s.is_text_editable })
      const { unmount } = renderRow(entry)
      if (screen.queryByTestId(`library-thumb-${entry.path}`) !== null) withThumbnail.push(kind)
      unmount()
    }
    expect(withThumbnail).toEqual([...KINDS_WITH_INLINE_THUMBNAILS])
  })
})

// ---------------------------------------------------------------------------
// Spec test 89 — TestLibraryPreviewPane_NoUnhandledKind (SC-017)
// ---------------------------------------------------------------------------
//
// LibraryPreviewPane USED to dispatch with `{kind === '…' && …}` chains, where a
// kind nobody wired up compiled clean and rendered an EMPTY PANE — no error, no
// type failure, nothing in the console. As of this change it dispatches with an
// exhaustive `switch (kind)` closed by `const unhandled: never = kind`, so the
// compiler now catches the gap. This guard survives that improvement because the
// improvement is exactly what a later refactor can quietly undo: revert to a
// chain, or drop the `never` line, and the silent empty pane is back with no
// signal anywhere. The detector therefore accepts BOTH idioms.
//
// WHAT THIS GUARD DOES AND DOES NOT PROVE. It reads the pane's source and asks
// whether each kind appears in a dispatch position. That catches the real
// failure — a kind added here and never wired there — but it does not prove the
// mounted surface renders anything useful; only LibraryPreviewPane.test.tsx,
// which renders the pane, can do that, and it is not this change's file. The
// weaker oracle is stated here rather than hidden behind a confident name.
//
// The detector carries its own positive control, so "found nothing, therefore
// fine" is not expressible: a source that omits a kind must be reported.

// Resolved against the vitest root (the repo root), not against import.meta.url:
// under the jsdom environment import.meta.url is an http: URL, not a file: one.
// The existsSync assertion below is what stops a moved file from reading as
// "nothing to check, therefore fine".
const PANE_SOURCE_PATH = resolve(process.cwd(), 'src/components/library/LibraryPreviewPane.tsx')

/**
 * The body of `switch (kind) { … }`, brace-balanced, or '' if there is none.
 * Scoped deliberately: a bare `case '…'` scan over the whole file would count
 * cases from unrelated switches (`reason`, `status`, …) and could report a kind
 * as handled because some other switch happens to use the same string.
 */
function switchOnKindBody(source: string): string {
  const open = source.search(/switch\s*\(\s*kind\s*\)\s*\{/)
  if (open < 0) return ''
  const start = source.indexOf('{', open)
  let depth = 0
  for (let i = start; i < source.length; i++) {
    if (source[i] === '{') depth++
    else if (source[i] === '}') {
      depth--
      if (depth === 0) return source.slice(start, i + 1)
    }
  }
  return ''
}

/** Kinds named in a dispatch position — either idiom the pane has used. */
function kindsDispatchedIn(source: string): Set<string> {
  const found = new Set<string>()
  for (const m of source.matchAll(/kind\s*===\s*'([a-z0-9_]+)'/g)) found.add(m[1])
  for (const m of switchOnKindBody(source).matchAll(/case\s+'([a-z0-9_]+)'\s*:/g)) found.add(m[1])
  return found
}

function unhandledKindsIn(source: string): string[] {
  const dispatched = kindsDispatchedIn(source)
  return LIBRARY_PREVIEW_KINDS.filter((k) => !dispatched.has(k))
}

describe('TestLibraryPreviewPane_NoUnhandledKind (SC-017)', () => {
  const asChain = (kinds: readonly string[]) => kinds.map((k) => `{kind === '${k}' && <X />}`).join('\n')
  const asSwitch = (kinds: readonly string[]) =>
    `switch (kind) {\n${kinds.map((k) => `  case '${k}': return <X />`).join('\n')}\n}`

  it('the detector reports a missing kind, in both dispatch idioms (positive control)', () => {
    // Dies on: a detector that returns [] unconditionally — e.g. a regex that
    // stops matching after a refactor, which would otherwise turn this whole
    // guard green and blind on the same day. Both halves are asserted for both
    // idioms, so "complete source ⇒ []" alone cannot carry the test.
    expect(unhandledKindsIn(asChain(LIBRARY_PREVIEW_KINDS))).toEqual([])
    expect(unhandledKindsIn(asSwitch(LIBRARY_PREVIEW_KINDS))).toEqual([])

    const withoutAudio = LIBRARY_PREVIEW_KINDS.filter((k) => k !== 'audio')
    expect(unhandledKindsIn(asChain(withoutAudio))).toEqual(['audio'])
    expect(unhandledKindsIn(asSwitch(withoutAudio))).toEqual(['audio'])
  })

  it('does not count a case from an unrelated switch as a handled kind', () => {
    // Dies on: widening the scan to every `case '…'` in the file. `reason` here
    // has an 'other' case; that must not make the pdf/audio/other kinds look
    // dispatched when `switch (kind)` never mentions them.
    const decoy = `switch (reason) { case 'other': case 'audio': break }`
    expect(unhandledKindsIn(decoy)).toEqual([...LIBRARY_PREVIEW_KINDS])
  })

  it('every kind in the union is dispatched by LibraryPreviewPane', () => {
    // Dies on: adding a member to LIBRARY_PREVIEW_KINDS with no matching branch
    // in the pane — verified by adding a throwaway 'zzz' kind and watching this
    // fail, then removing it. Also dies if the pane's `switch (kind)` loses a
    // case, whichever idiom replaces it.
    expect(existsSync(PANE_SOURCE_PATH), `pane source not found at ${PANE_SOURCE_PATH}`).toBe(true)
    const source = readFileSync(PANE_SOURCE_PATH, 'utf8')
    expect(source.length).toBeGreaterThan(0)
    expect(unhandledKindsIn(source)).toEqual([])
  })
})
