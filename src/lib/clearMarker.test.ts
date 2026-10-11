// The /clear divider is detected by role plus exact English text, and that
// text is written by the server: the literal lives in
// pkg/session/transcript_view.go::ClearMarkerText (moved there by 446452300,
// chat views start at the last /clear marker), and /clear's writer
// pkg/agent/clear_session.go::clearMarkerText must use that constant. Nothing
// else links the SPA to the server text, so this reads the Go source: if the
// server wording changes, or the writer stops using the shared constant, this
// fails here instead of the divider silently stopping.

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

  it('/clear writes the shared constant (pkg/agent/clear_session.go::clearMarkerText)', () => {
    const go = readFileSync(resolve(here, '../../pkg/agent/clear_session.go'), 'utf8')
    expect(go, 'the /clear writer uses session.ClearMarkerText, not its own literal').toMatch(
      /const clearMarkerText = session\.ClearMarkerText\b/,
    )
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
