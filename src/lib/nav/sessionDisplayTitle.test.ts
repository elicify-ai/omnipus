// sessionDisplayTitle — the one title derivation for main sessions
// (founder UI feedback 2026-10-11, point 4; honest-unavailable amendment +
// team-lead steer 2026-10-12, combined-review S4-04/S2-05). For a MAIN the
// session's stored title is IGNORED ENTIRELY — architect ruling: the SPA
// always derives "<Agent> - Main Session" from the LIVE agent list, so no
// title copied at attach time (or persisted in the browser) can ever survive
// an agent rename, and the derivation is byte-identical to what the server
// shows on a fresh read. When no name can be resolved, the AGENT ID —
// exactly the server's own empty-name fallback
// (pkg/gateway/session_title.go::withDisplayTitle falls back to m.AgentID;
// pkg/session/auto_title.go::MainSessionTitle appends " - Main Session") —
// and when there is neither a name nor an id, NULL: the caller renders its
// honest loading/unavailable presentation. Never a fabricated placeholder
// ("Unknown agent" is gone), and never "Untitled" on a main. Non-main
// sessions keep the stored title and their caller's existing fallback (core
// owns auto-titling of user sessions — this module must not invent one).

import { describe, it, expect } from 'vitest'
import type { Session } from '@/lib/api'
import { sessionDisplayTitle } from './sessionDisplayTitle'

function session(overrides: Partial<Pick<Session, 'type' | 'title'>>): Pick<Session, 'type' | 'title'> {
  return { type: 'chat', title: '', ...overrides }
}

describe('sessionDisplayTitle', () => {
  it('IGNORES the stored title on a main and always derives from the live agent name (steer 2026-10-12: no title survives a rename)', () => {
    expect(sessionDisplayTitle(session({ type: 'main', title: 'Weekly planning' }), 'Jim', 'Untitled session')).toBe(
      'Jim - Main Session',
    )
    // Even a stored copy that matches the OLD name is never shown.
    expect(sessionDisplayTitle(session({ type: 'main', title: 'Ava - Main Session' }), 'Eve', 'Untitled session')).toBe(
      'Eve - Main Session',
    )
  })

  it('returns the server title verbatim when present — on a non-main (unchanged behaviour)', () => {
    expect(sessionDisplayTitle(session({ type: 'chat', title: 'My extra chat' }), 'Jim', 'Untitled session')).toBe(
      'My extra chat',
    )
  })

  it('derives for a main regardless of the stored title — the title is not read at all (S2-05 ruling)', () => {
    expect(sessionDisplayTitle(session({ type: 'main', title: '   ' }), 'Jim', 'Untitled session')).toBe(
      'Jim - Main Session',
    )
  })

  it('derives "<Agent> - Main Session" for an untitled main (ASCII hyphen, single spaces)', () => {
    const derived = sessionDisplayTitle(session({ type: 'main', title: '' }), 'Jim', 'Untitled session', 'jim')
    expect(derived).toBe('Jim - Main Session')
    // Exactly the fixed format: ASCII hyphen (U+002D) with one space each side.
    expect(derived).toMatch(/^[^—–]+ - Main Session$/)
    expect(derived).not.toContain('—')
    expect(derived).not.toContain('–')
  })

  it('falls back to the AGENT ID for an untitled main whose name cannot be resolved — the server\'s own empty-name fallback', () => {
    // withDisplayTitle: name == "" -> m.AgentID. The id is honest and stable;
    // "Unknown agent" was a fabricated name.
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), undefined, 'Untitled session', 'jim')).toBe(
      'jim - Main Session',
    )
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), '', 'Untitled session', 'agt_8f3k')).toBe(
      'agt_8f3k - Main Session',
    )
  })

  it('trims surrounding whitespace off the fallback agent id too (TrimSpace, like the server)', () => {
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), '   ', 'Untitled session', '  jim  ')).toBe(
      'jim - Main Session',
    )
  })

  it('returns null for an untitled main with NEITHER a name nor an id — the caller\'s honest loading/unavailable presentation', () => {
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), undefined, 'Untitled session', null)).toBeNull()
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), undefined, 'Untitled session')).toBeNull()
    expect(sessionDisplayTitle(session({ type: 'main', title: '' }), '   ', 'Untitled session', '   ')).toBeNull()
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
