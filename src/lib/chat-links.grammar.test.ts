/**
 * Shared Go/SPA grammar (TDD rows 1–2, SEC-1, SEC-5, SEC-13).
 * The fixture is tests/fixtures/chat-links/grammar.json. Expected links live
 * there, not in a run of the encoder.
 */
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { parseChatLink } from './chatLinks'

interface GrammarFixture {
  roundTrip: { workspace: string; path: string; link: string }[]
  rejectHref: string[]
  rejectPath: { workspace: string; path: string }[]
}

const fixture = JSON.parse(
  readFileSync(resolve(__dirname, '../../tests/fixtures/chat-links/grammar.json'), 'utf8'),
) as GrammarFixture

describe('parseChatLink shared grammar (SEC-1 / SEC-13)', () => {
  it('round-trips the shared awkward names to the fixture workspace and path', () => {
    expect(fixture.roundTrip.length).toBeGreaterThan(0)
    for (const row of fixture.roundTrip) {
      expect(parseChatLink(row.link, 'other'), row.link).toEqual({
        workspaceId: row.workspace,
        path: row.path,
      })
    }
  })

  it('rejects traversal, dot segments, encoded separators and non-file forms', () => {
    expect(fixture.rejectHref.length).toBeGreaterThan(0)
    for (const href of fixture.rejectHref) {
      expect(parseChatLink(href, 'W'), href).toBeNull()
    }
    for (const row of fixture.rejectPath) {
      const href = `omnipus://file/${row.workspace}/${row.path}`
      expect(parseChatLink(href, row.workspace), href).toBeNull()
    }
  })

  it('accepts a 255-character segment and rejects 256 (BS-1.8)', () => {
    const accepted = 'a'.repeat(255)
    const rejected = 'a'.repeat(256)
    expect(parseChatLink(`omnipus://file/W/${accepted}`, 'W')).toEqual({
      workspaceId: 'W',
      path: accepted,
    })
    expect(parseChatLink(`omnipus://file/W/${rejected}`, 'W')).toBeNull()
  })

  it('resolves a bare file only when the chat has a workspace (BS-1.2 / BS-1.11)', () => {
    expect(parseChatLink('notes/plan.md', 'W')).toEqual({ workspaceId: 'W', path: 'notes/plan.md' })
    expect(parseChatLink('notes/plan.md', null)).toBeNull()
    expect(parseChatLink('notes/plan.md', '')).toBeNull()
    expect(parseChatLink('README', 'W')).toBeNull()
    expect(parseChatLink('./a.md', 'W')).toBeNull()
  })
})
