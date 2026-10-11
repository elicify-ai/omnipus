// sessionDisplayTitle — the one title derivation for main sessions
// (founder UI feedback 2026-10-11, point 4: a main session must never read
// "Untitled"). Order: server title when present; otherwise, for a main, the
// fixed "<Agent name> - Main Session" string derived from the agent's CURRENT
// display name. Non-main sessions keep their caller's existing fallback
// (core owns auto-titling of user sessions — this module must not invent one).

import { describe, it, expect } from 'vitest'
import type { Session } from '@/lib/api'
import { sessionDisplayTitle } from './sessionDisplayTitle'

function session(overrides: Partial<Pick<Session, 'type' | 'title'>>): Pick<Session, 'type' | 'title'> {
  return { type: 'chat', title: '', ...overrides }
}

describe('sessionDisplayTitle', () => {
  it('returns the server title verbatim when present — on a main', () => {
    expect(sessionDisplayTitle(session({ type: 'main', title: 'Weekly planning' }), 'Jim', 'Untitled session')).toBe(
      'Weekly planning',
    )
  })

  it('returns the server title verbatim when present — on a non-main (unchanged behaviour)', () => {
    expect(sessionDisplayTitle(session({ type: 'chat', title: 'My extra chat' }), 'Jim', 'Untitled session')).toBe(
      'My extra chat',
    )
  })

  it('treats a whitespace-only server title as absent and derives for a main', () => {
    expect(sessionDisplayTitle(session({ type: 'main', title: '   ' }), 'Jim', 'Untitled session')).toBe(
      'Jim - Main Session',
    )
  })

  it('derives "<Agent> - Main Session" for an untitled main (ASCII hyphen, single spaces)', () => {
    const derived = sessionDisplayTitle(session({ type: 'main', title: '' }), 'Jim', 'Untitled session')
    expect(derived).toBe('Jim - Main Session')
    // Exactly the fixed format: ASCII hyphen (U+002D) with one space each side.
    expect(derived).toMatch(/^[^—–]+ - Main Session$/)
    expect(derived).not.toContain('—')
    expect(derived).not.toContain('–')
  })

  it('never returns the fallback for an untitled main, even with no agent name', () => {
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), undefined, 'Untitled session')).toBe(
      'Unknown agent - Main Session',
    )
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), '', 'Untitled session')).toBe(
      'Unknown agent - Main Session',
    )
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), '   ', 'Untitled session')).toBe(
      'Unknown agent - Main Session',
    )
  })

  it('keeps the caller fallback for an untitled non-main session (core owns that titling)', () => {
    expect(sessionDisplayTitle(session({ type: 'chat', title: '' }), 'Jim', 'Untitled session')).toBe('Untitled session')
    expect(sessionDisplayTitle(session({ type: 'task', title: '' }), undefined, 'Untitled')).toBe('Untitled')
    expect(sessionDisplayTitle(session({ type: 'delegate', title: '' }), undefined, 'Untitled session')).toBe(
      'Untitled session',
    )
  })

  it('follows an agent rename immediately: same session, new name, new title (no caching)', () => {
    const untitledMain = session({ type: 'main', title: '' })
    expect(sessionDisplayTitle(untitledMain, 'Jim', 'Untitled session')).toBe('Jim - Main Session')
    expect(sessionDisplayTitle(untitledMain, 'Jim Prime', 'Untitled session')).toBe('Jim Prime - Main Session')
  })

  it('trims surrounding whitespace off the agent name', () => {
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), '  Jim  ', 'Untitled session')).toBe(
      'Jim - Main Session',
    )
  })
})
