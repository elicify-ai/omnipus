// The /clear divider is detected by role plus exact English text, and that
// text is written by the server: the literal lives in
// pkg/session/transcript_view.go::ClearMarkerText (moved there by 446452300,
// chat views start at the last /clear marker), and /clear's writer
// pkg/agent/clear_session.go::clearMarkerText must be exactly that constant.
// Nothing else links the SPA to the server text, so this reads the Go source
// with whole-line, active-code matchers: if the server wording changes, or the
// writer stops being exactly the shared constant, this fails here instead of
// the divider silently stopping.

import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'
import { CLEAR_MARKER_TEXT, isClearCommandText, isClearContextMarker } from './clearMarker'

const here = dirname(fileURLToPath(import.meta.url))

// Active-code matchers. Go comments (line AND block) are blanked first by a
// small lexer that respects string, raw-string and rune literals, so a
// commented-out declaration can never satisfy a matcher. Each matcher then
// needs a whole line: the declaration starts it and the initializer ends the
// statement (trailing spaces only), so `X + "."` or a call never counts. A
// grouped `const ( ... )` form is deliberately not accepted: it fails loudly
// here (a false red), never silently.
function stripGoComments(src: string): string {
  let out = ''
  let i = 0
  while (i < src.length) {
    const c = src[i]
    const next = src[i + 1]
    if (c === '/' && next === '/') {
      while (i < src.length && src[i] !== '\n') i++
    } else if (c === '/' && next === '*') {
      i += 2
      while (i < src.length && !(src[i] === '*' && src[i + 1] === '/')) {
        out += src[i] === '\n' ? '\n' : ' '
        i++
      }
      i += 2
    } else if (c === '"' || c === "'") {
      out += c
      i++
      while (i < src.length && src[i] !== c && src[i] !== '\n') {
        if (src[i] === '\\') { out += src[i]; i++ }
        out += src[i]
        i++
      }
      if (i < src.length) { out += src[i]; i++ }
    } else if (c === '`') {
      out += c
      i++
      while (i < src.length && src[i] !== '`') { out += src[i]; i++ }
      if (i < src.length) { out += src[i]; i++ }
    } else {
      out += c
      i++
    }
  }
  return out
}

const SHARED_LITERAL = /^const ClearMarkerText = "([^"]*)"[ \t]*$/m
const WRITER_ALIAS = /^const clearMarkerText = session\.ClearMarkerText[ \t]*$/m
const active = (go: string) => stripGoComments(go)

describe('clear marker', () => {
  it('matches the text the server writes (pkg/session/transcript_view.go::ClearMarkerText)', () => {
    const go = readFileSync(resolve(here, '../../pkg/session/transcript_view.go'), 'utf8')
    const match = active(go).match(SHARED_LITERAL)
    expect(match, 'the server constant exists under that name, as a plain literal').not.toBeNull()
    expect(match![1]).toBe(CLEAR_MARKER_TEXT)
  })

  it('/clear writes the shared constant (pkg/agent/clear_session.go::clearMarkerText)', () => {
    const go = readFileSync(resolve(here, '../../pkg/agent/clear_session.go'), 'utf8')
    expect(active(go), 'the /clear writer is exactly session.ClearMarkerText, not its own literal or an expression').toMatch(WRITER_ALIAS)
  })

  it('the source matchers reject the drift they exist to catch', () => {
    const writer = (go: string) => WRITER_ALIAS.test(active(go))
    const literal = (go: string) => SHARED_LITERAL.test(active(go))
    // Writer drift: suffix, call, own literal, line- and block-commented alias above a live literal.
    expect(writer('const clearMarkerText = session.ClearMarkerText + "."')).toBe(false)
    expect(writer('const clearMarkerText = session.ClearMarkerText()')).toBe(false)
    expect(writer('const clearMarkerText = "Conversation context cleared"')).toBe(false)
    expect(writer('// const clearMarkerText = session.ClearMarkerText\nconst clearMarkerText = "x"')).toBe(false)
    expect(writer('/*\nconst clearMarkerText = session.ClearMarkerText\n*/\nconst clearMarkerText = "Conversation context cleared."')).toBe(false)
    expect(writer('/* a */ const clearMarkerText = "x"')).toBe(false)
    // Accepted: the live alias, also with a trailing line or block comment, and a "/*" inside a string elsewhere.
    expect(writer('const clearMarkerText = session.ClearMarkerText // shared')).toBe(true)
    expect(writer('const clearMarkerText = session.ClearMarkerText /* shared */')).toBe(true)
    expect(writer('var s = "/* not a comment"\nconst clearMarkerText = session.ClearMarkerText')).toBe(true)
    // Shared-literal drift: trailing expression, line- and block-commented copy above a changed live literal.
    expect(literal('const ClearMarkerText = "Conversation context cleared" + "."')).toBe(false)
    expect(literal('// const ClearMarkerText = "Conversation context cleared"')).toBe(false)
    expect(active('/*\nconst ClearMarkerText = "Conversation context cleared"\n*/\nconst ClearMarkerText = "Context reset"').match(SHARED_LITERAL)?.[1]).toBe('Context reset')
    // A string containing comment markers is kept intact.
    expect(active('const ClearMarkerText = "a // b /* c */"').match(SHARED_LITERAL)?.[1]).toBe('a // b /* c */')
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
