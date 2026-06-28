/**
 * SC-005 regression guard: ChatScreen.tsx must NOT contain hardcoded slash-command
 * constants (SLASH_COMMANDS or HELP_TEXT). The palette is driven exclusively by the
 * API (GET /commands?surface=web) per US-4 / FR-008.
 *
 * This test reads the compiled source file and asserts the absence of the
 * previously-hardcoded identifiers. It is intentionally a source-level check
 * (fs.readFileSync) so it catches accidental re-introduction without requiring
 * the component to render.
 */

import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect } from 'vitest'

const CHAT_SCREEN_PATH = join(__dirname, 'ChatScreen.tsx')

describe('SC-005 — no hardcoded slash-command constants in ChatScreen.tsx', () => {
  it('does not contain SLASH_COMMANDS identifier', () => {
    const source = readFileSync(CHAT_SCREEN_PATH, 'utf8')
    expect(source).not.toContain('SLASH_COMMANDS')
  })

  it('does not contain HELP_TEXT identifier', () => {
    const source = readFileSync(CHAT_SCREEN_PATH, 'utf8')
    expect(source).not.toContain('HELP_TEXT')
  })
})
