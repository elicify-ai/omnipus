// The /clear divider is detected by role plus exact English text, and that
// text is written by the server. Nothing else links the two, so this reads
// the Go source: if the server wording changes, this fails here instead of
// the divider silently stopping.
//
// Oracle location (nav-clearview lane, 2026-10-11): the constant moved in
// server commit 446452300 — pkg/agent/clear_session.go::clearMarkerText is
// now an alias for pkg/session/transcript_view.go::ClearMarkerText — so the
// drift check reads the constant's new home. Same check, same strength.

import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'
import { CLEAR_MARKER_TEXT, isClearCommandText, isClearContextMarker } from './clearMarker'

const here = dirname(fileURLToPath(import.meta.url))

describe('clear marker', () => {
  it('matches the text the server writes (pkg/session/transcript_view.go::ClearMarkerText)', () => {
    const go = readFileSync(resolve(here, '../../pkg/session/transcript_view.go'), 'utf8')
    const match = go.match(/const ClearMarkerText = "([^"]*)"/)
    expect(match, 'the server constant exists under that name').not.toBeNull()
    expect(match![1]).toBe(CLEAR_MARKER_TEXT)
  })

  it('recognises only a system row with exactly that text', () => {
    expect(isClearContextMarker({ role: 'system', content: CLEAR_MARKER_TEXT })).toBe(true)
    expect(isClearContextMarker({ role: 'assistant', content: CLEAR_MARKER_TEXT })).toBe(false)
    expect(isClearContextMarker({ role: 'user', content: CLEAR_MARKER_TEXT })).toBe(false)
    expect(isClearContextMarker({ role: 'system', content: `${CLEAR_MARKER_TEXT}.` })).toBe(false)
    expect(isClearContextMarker({ role: 'system', content: null })).toBe(false)
  })

  it('recognises /clear as a command, with or without trailing words, in any case', () => {
    expect(isClearCommandText('/clear')).toBe(true)
    expect(isClearCommandText('  /CLEAR now ')).toBe(true)
    expect(isClearCommandText('/clearly')).toBe(false)
    expect(isClearCommandText('please /clear')).toBe(false)
  })
})
