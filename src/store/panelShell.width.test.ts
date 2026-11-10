// panelShell.width.test.ts — side-panel-shell-spec.md §12 tests #2 + #3:
// the SP-17 width geometry (width-bounds dataset rows 1–16) and the SP-20
// width memory (per USER × panel × workspace, reset deletes, transient
// re-clamp never writes).
//
// CHARACTERISATION — GREEN ON WAVE-0 CODE: the pure modules under test
// (src/components/panel-shell/panelWidth.ts, panelWidthMemory.ts) shipped in
// wave 0 and already implement this behaviour; this file pins it so wave 1's
// store/route wiring cannot regress the math. Failability is proven in RED by
// a one-mutation probe against panelWidth.ts (PANEL_MIN_PX 320 → 321): the
// run died on the floor rows, then the mutation was reverted and `git status`
// confirmed a clean tree (see the RED report's evidence table).
//
// Every expected value below derives from the SPEC, not from the code: the
// width-bounds dataset table in side-panel-shell-spec.md §12 ("Dataset:
// width bounds (drives tests 2, 10)") states row-by-row inputs and expected
// outputs; the persistence rules come from §7 (SP-20, MAJ-009, MIN-204,
// MIN-205) and §8.1's identity-key table (MAJ-201).
//
// PLACEMENT NOTE (coverage tripwire): the CI vitest matrix has no
// `src/components/panel-shell/` pattern, so this file lives under the
// covered `src/store/` group (the width memory is store-domain state in the
// spec's own framing: §1 "width memory is browser-local storage … in the
// pattern of the existing persisted stores"). The matrix gap is reported to
// team-lead as a finding.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  CHAT_FLOOR_PX,
  PANEL_MIN_PX,
  PANEL_TAKEOVER_PX,
  panelWidthCeiling,
  panelDefaultWidth,
  clampPanelWidth,
  isPhoneTakeover,
} from '@/components/panel-shell/panelWidth'
import {
  panelWidthKey,
  panelWidthScope,
  readPanelWidth,
  writePanelWidth,
  deletePanelWidth,
  prunePanelWidths,
} from '@/components/panel-shell/panelWidthMemory'

// --- spec-derived constants (side-panel-shell-spec.md §5/§7 Geometry) ------
// chat column floor 360px; panel floor 320px; takeover below 680px (SP-25).
const SPEC_CHAT_FLOOR = 360
const SPEC_PANEL_MIN = 320
const SPEC_TAKEOVER = 680

describe('SP-17 width geometry — width-bounds dataset rows 1–16 (§12 #2) — characterisation, green on wave-0 code', () => {
  it('exposes the spec constants: chat floor 360, panel floor 320, takeover 680', () => {
    expect(CHAT_FLOOR_PX).toBe(SPEC_CHAT_FLOOR)
    expect(PANEL_MIN_PX).toBe(SPEC_PANEL_MIN)
    expect(PANEL_TAKEOVER_PX).toBe(SPEC_TAKEOVER)
  })

  // dataset rows 1–4, 7, 12–14: ceiling = min(70% × row, row − sidebar − 360)
  it.each([
    // [datasetRow, rowWidth, sidebar, requested, expected]
    [1, 1400, 256, 980, 784], // min(980, 1400−256−360=784) — not 70% alone
    [2, 1400, 0, 1200, 980], // 70% cap binds
    [3, 1400, 256, 100, 320], // panel floor binds
    [4, 1000, 0, 900, 640], // chat floor binds: min(700, 1000−360)
    [7, 680, 0, 500, 320], // ceiling min(476, 320)=320 — chat exactly at its floor
    [12, 1023, 0, 900, 663],
    [13, 1024, 0, 900, 664],
    [14, 1024, 256, 900, 408],
  ])(
    'dataset row %i: clamp(row=%ipx, sidebar=%ipx, requested=%ipx) → %ipx',
    (_d, rowWidth, sidebar, requested, expected) => {
      expect(clampPanelWidth(requested, rowWidth, sidebar)).toBe(expected)
    },
  )

  it('row 1 exact: the ceiling is min(70% row, row − sidebar − 360), not either term alone (BDD "Drag resize clamps at both bounds")', () => {
    expect(panelWidthCeiling(1400, 256)).toBe(784) // 70% term = 980 would be wrong
    expect(panelWidthCeiling(1400, 0)).toBe(980) // 70% term binds
    expect(panelWidthCeiling(1000, 0)).toBe(640) // chat-floor term binds
  })

  // dataset rows 5, 6, 8–11: default = clamp(0.45 × row, 320, min(720, ceiling)), floored
  it.each([
    // [datasetRow, rowWidth, sidebar, expected]
    [5, 1400, 256, 630], // 0.45×1400 = 630; ceiling 784 not binding
    [6, 900, 0, 405], // ceiling 540 not binding
    [8, 680, 0, 320], // raw 306 clamps UP to the 320 floor
    [9, 1024, 256, 408], // MAJ-203: raw 461 clamps DOWN to ceiling 408 (chat exactly 360)
    [10, 1119, 256, 503], // 503.55 → 503 floored (ceiling binds)
    [11, 1120, 256, 504], // ceiling 504
  ])('dataset row %i: default(row=%ipx, sidebar=%ipx) → %ipx', (_d, rowWidth, sidebar, expected) => {
    expect(panelDefaultWidth(rowWidth, sidebar)).toBe(expected)
  })

  it('rows 8 + boundary: the floors fit EXACTLY at 680px docked — 680 − 320 = 360 = the chat floor (SP-25 deletes overlay)', () => {
    expect(panelWidthCeiling(680, 0)).toBe(320)
    expect(680 - panelDefaultWidth(680, 0)).toBe(SPEC_CHAT_FLOOR)
  })

  it('row 16 + boundary: <680px is phone takeover, 680px is docked — min−1/min on the SP-25 threshold (no overlay band exists)', () => {
    expect(isPhoneTakeover(679)).toBe(true)
    expect(isPhoneTakeover(680)).toBe(false)
    expect(isPhoneTakeover(SPEC_TAKEOVER - 1)).toBe(true)
    expect(isPhoneTakeover(SPEC_TAKEOVER)).toBe(false)
  })

  it('row 15 (MAJ-009 transient re-clamp, geometry half): clamping never writes storage — the stored value re-applies when the window returns', () => {
    const setItemSpy = vi.spyOn(Storage.prototype, 'setItem')
    try {
      // stored 950 on a 1400px unpinned row; window shrinks to 1000px.
      const shrunk = clampPanelWidth(950, 1000, 0)
      expect(shrunk).toBe(640) // dataset row 4's ceiling: min(700, 1000−360)
      expect(setItemSpy).not.toHaveBeenCalled() // transient — never writes
      // window returns to 1400px: the stored width applies again, untouched.
      expect(clampPanelWidth(950, 1400, 0)).toBe(950)
      expect(setItemSpy).not.toHaveBeenCalled()
    } finally {
      setItemSpy.mockRestore()
    }
  })
})

describe('SP-20 width memory — per USER × panel × workspace (§12 #3) — characterisation, green on wave-0 code', () => {
  beforeEach(() => {
    localStorage.clear()
  })
  afterEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('the storage key is `panel-width:<username>:<panelId>:<workspaceId|app>` — ONE key per combination (MIN-204, §7 Persistence)', () => {
    expect(panelWidthKey('dana', 'library', 'ws-1')).toBe('panel-width:dana:library:ws-1')
  })

  it('an empty scope falls back to the `app` bucket (sidebar Library at the all-workspaces root)', () => {
    expect(panelWidthKey('dana', 'library', '')).toBe('panel-width:dana:library:app')
    expect(panelWidthKey('dana', 'library', 'app')).toBe('panel-width:dana:library:app')
  })

  it('the Browser panel ALWAYS uses the `app` bucket — its identity is a session, not a workspace (MAJ-201)', () => {
    expect(panelWidthScope('browser', { sessionId: 's1', agentId: 'a1' })).toBe('app')
    // even a stray workspaceId in the context must not move the Browser's bucket
    expect(panelWidthScope('browser', { sessionId: 's1', agentId: 'a1', workspaceId: 'ws-1' })).toBe(
      'app',
    )
  })

  it('workspace panels use the workspace, `app` when none (§8.1 identity-key table)', () => {
    expect(panelWidthScope('library', { workspaceId: 'ws-1' })).toBe('ws-1')
    expect(panelWidthScope('library', {})).toBe('app')
    expect(panelWidthScope('tasks', { workspaceId: 'ws-9' })).toBe('ws-9')
  })

  it('read/write roundtrip stores px under the user × panel × workspace key', () => {
    writePanelWidth('dana', 'library', { workspaceId: 'ws-1' }, 950)
    expect(readPanelWidth('dana', 'library', { workspaceId: 'ws-1' })).toBe(950)
    expect(localStorage.getItem('panel-width:dana:library:ws-1')).toBe('950')
  })

  it('two accounts on one browser NEVER share widths — the key carries the username (SP-20, US-3 AS-2)', () => {
    writePanelWidth('dana', 'library', { workspaceId: 'ws-1' }, 950)
    expect(readPanelWidth('mia', 'library', { workspaceId: 'ws-1' })).toBeNull()
  })

  it('per-panel and per-workspace isolation: workspace B never sees workspace A\'s width (US-3 AS-2)', () => {
    writePanelWidth('dana', 'library', { workspaceId: 'ws-1' }, 950)
    expect(readPanelWidth('dana', 'library', { workspaceId: 'ws-2' })).toBeNull()
    expect(readPanelWidth('dana', 'browser', { sessionId: 's1', agentId: 'a1' })).toBeNull()
  })

  it('a double-click reset DELETES the stored value — the default is re-derived, never stored as px (US-3 AS-4)', () => {
    writePanelWidth('dana', 'library', { workspaceId: 'ws-1' }, 950)
    deletePanelWidth('dana', 'library', { workspaceId: 'ws-1' })
    expect(readPanelWidth('dana', 'library', { workspaceId: 'ws-1' })).toBeNull()
    expect(localStorage.getItem('panel-width:dana:library:ws-1')).toBeNull()
  })

  it('dataset row 15 end-to-end: a window-driven re-clamp never writes — the stored width survives shrink AND restore (MAJ-009)', () => {
    writePanelWidth('dana', 'library', {}, 950) // stored 950, unpinned row
    const setItemSpy = vi.spyOn(Storage.prototype, 'setItem')
    try {
      // shrink 1400 → 1000: applied width re-clamps to 640…
      expect(clampPanelWidth(readPanelWidth('dana', 'library', {}) ?? 0, 1000, 0)).toBe(640)
      // …without a single write…
      expect(setItemSpy).not.toHaveBeenCalled()
      expect(readPanelWidth('dana', 'library', {})).toBe(950) // …stored stays 950
      // window returns to 1400px: 950 comes back.
      expect(clampPanelWidth(readPanelWidth('dana', 'library', {}) ?? 0, 1400, 0)).toBe(950)
    } finally {
      setItemSpy.mockRestore()
    }
  })

  it('a storage failure degrades to memory-only for the session — nothing throws, the caller gets false (FR-005, MIN-204)', () => {
    const failing = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('quota exceeded', 'QuotaExceededError')
    })
    expect(writePanelWidth('dana', 'library', { workspaceId: 'ws-1' }, 950)).toBe(false)
    expect(failing).toHaveBeenCalledTimes(1)
    // and the in-session value still reads back from this session's write path
    // via the store, not storage — the memory-only degrade is the store's job
    // (shell-level file); here we only pin that no error surfaced.
  })

  it('pruning removes ONLY the signed-in user\'s own dead-workspace entries (MIN-204) — other accounts and `app` keys survive', () => {
    writePanelWidth('dana', 'library', { workspaceId: 'ws-live' }, 500)
    writePanelWidth('dana', 'library', { workspaceId: 'ws-dead' }, 600)
    writePanelWidth('dana', 'library', {}, 700) // app bucket — never pruned
    writePanelWidth('mia', 'library', { workspaceId: 'ws-dead' }, 800) // another account

    const removed = prunePanelWidths('dana', (ws) => ws === 'ws-live')

    expect(removed).toBe(1)
    expect(localStorage.getItem('panel-width:dana:library:ws-live')).toBe('500')
    expect(localStorage.getItem('panel-width:dana:library:app')).toBe('700')
    expect(localStorage.getItem('panel-width:mia:library:ws-dead')).toBe('800') // untouched
    expect(localStorage.getItem('panel-width:dana:library:ws-dead')).toBeNull()
  })

  it('a stored value below the 320px floor is rejected at read time (the clamp re-applies at read — §7 Persistence)', () => {
    localStorage.setItem('panel-width:dana:library:ws-1', '100')
    expect(readPanelWidth('dana', 'library', { workspaceId: 'ws-1' })).toBeNull()
  })
})
