// LibraryPanel.redock.test.tsx — side-panel-shell-spec.md §12 #8c + #19.
//
// Written against the §8.1 single-slice store (batch-2 squad-lead ruling):
// `activePanel: { id, context } | null` with `openPanel(id, context)` /
// `closePanel()` — "ONE slice replacing browserPanel/libraryPanel" (§8.1,
// SP-7). The pack previously seeded the RETIRED libraryPanel/browserPanel
// slices and drove openLibraryPanel/openBrowserPanel.
//
// RED on this pre-GREEN tree: the §8.1 API does not exist yet, so the
// beforeEach gate fails every test with an explicit BLOCKED naming it.
// On GREEN the behavioural REDs are wave-0's re-dock wiring: unconditional
// re-dock (no different-panel no-clobber), no opener-handle check, and no
// guard on the same-panel follow.
//
// Opener-handle ruling (batch-2 item 2, MAJ-208/FR-018): the same-panel
// guard scenario only exists for the tab that OPENED the pop-out — "only
// the tab that OPENED the pop-out (holds its window handle) reacts to a
// pop-out close" (§6, MAJ-006 as corrected by MAJ-208; §12 dataset row 7
// cites BrowserLivePanel.tsx::watchPopoutClosed as the pattern). The test
// therefore establishes opener-ness FIRST by driving the REAL pop-out flow
// (handlePopOut via the mock explorer's onPopOut button — the same mock
// boundary as LibraryPanel.test.tsx), proven by the window.open spy, before
// the pop-out-close broadcast.
//
// Oracles (§6, MAJ-006/MAJ-208, FR-018):
//   - a DIFFERENT panel open -> re-dock is a no-op (never clobbers)
//   - a tab that does not hold the pop-out's window handle does not re-dock
//   - opener tab + SAME panel open + dirty -> the re-dock runs the leave
//     guard; cancel leaves the open Library where it was

import { useEffect } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, screen, fireEvent } from '@testing-library/react'
import { useUiStore } from '@/store/ui'
import { announceLibraryPopoutClosed } from '@/lib/libraryHandoff'
import {
  setLibraryEditorDirty,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

vi.mock('./LibraryExplorer', () => ({
  LibraryExplorer: (props: { onWorkspaceChange?: (id: string | null) => void; onPopOut?: () => void }) => {
    useEffect(() => {
      props.onWorkspaceChange?.('ws-current')
    }, [])
    return (
      <div data-testid="mock-library-explorer">
        {props.onPopOut && (
          <button type="button" tabIndex={0} onClick={props.onPopOut}>
            mock-pop-out
          </button>
        )}
      </div>
    )
  },
}))

import { LibraryPanel } from './LibraryPanel'

/** §8.1 single-slice view of the ui store (defensive: absent pre-GREEN). */
type Section81Store = {
  activePanel: { id: string; context?: Record<string, unknown> } | null
  openPanel: (id: string, context?: Record<string, unknown>) => void
  closePanel: () => void
}
const s81 = () => useUiStore.getState() as unknown as Section81Store

/** Fails every test with a STATED reason while the §8.1 slice is missing. */
function requireSection81Api() {
  const s = useUiStore.getState() as unknown as Record<string, unknown>
  if (typeof s.openPanel !== 'function' || typeof s.closePanel !== 'function' || !('activePanel' in s)) {
    throw new Error(
      'BLOCKED: ui store has no §8.1 single-slice API (activePanel / openPanel(id, context) / closePanel()) — ' +
        'required by side-panel-shell-spec.md §8.1/SP-7 (ONE slice replacing the retired libraryPanel/browserPanel)',
    )
  }
}

beforeEach(() => {
  requireSection81Api()
  act(() => {
    s81().closePanel()
  })
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(true)
})

afterEach(() => {
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(true)
})

describe('pop-out re-dock no-clobber and opener-only (§12 #8c, #19)', () => {
  it('RED — a pop-out close does NOT re-dock while a DIFFERENT panel is open (MAJ-006)', async () => {
    render(<LibraryPanel />)
    act(() => {
      s81().openPanel('browser', { sessionId: 'sess-1', agentId: 'mia' })
    })

    act(() => {
      announceLibraryPopoutClosed('ws-99')
    })
    // BroadcastChannel delivery is async. Wait long enough for today's
    // unconditional re-dock to land, then assert it did not.
    await new Promise((resolve) => setTimeout(resolve, 200))
    expect(s81().activePanel).toEqual({ id: 'browser', context: { sessionId: 'sess-1', agentId: 'mia' } })
  })

  it('RED — a tab that never opened the pop-out (no window handle) does not re-dock (MAJ-208)', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
    render(<LibraryPanel />)
    // Prove this tab holds NO opener handle: it never called window.open.
    expect(openSpy).not.toHaveBeenCalled()

    act(() => {
      announceLibraryPopoutClosed('ws-99')
    })
    await act(async () => {})

    expect(s81().activePanel).toBeNull()
    openSpy.mockRestore()
  })

  it('RED — opener tab, SAME panel open with unsaved edits: the re-dock runs the discard guard; cancel leaves the workspace (MAJ-208, FR-013, FR-018)', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({ closed: false, close: () => {} } as unknown as Window)
    render(<LibraryPanel />)
    act(() => {
      s81().openPanel('library', { workspaceId: 'ws-current' })
    })

    // Establish THIS TAB as the opener (MAJ-208): drive the REAL pop-out
    // flow — handlePopOut opens the pop-out window and closes the docked
    // panel (C4). The spy proves the opener event happened here.
    fireEvent.click(screen.getByText('mock-pop-out'))
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(openSpy).toHaveBeenCalledTimes(1)
    expect(s81().activePanel).toBeNull()

    // The operator re-docks the Library while the pop-out lives (the two
    // surfaces may legitimately coexist — libraryHandoff.ts), then edits.
    act(() => {
      s81().openPanel('library', { workspaceId: 'ws-current' })
    })
    setLibraryEditorDirty(true)

    // The pop-out tab closes.
    act(() => {
      announceLibraryPopoutClosed('ws-other')
    })
    await new Promise((resolve) => setTimeout(resolve, 200))

    // The re-dock must run the leave guard (FR-013/CRIT-001): the discard
    // prompt opens and the open Library stays put until it resolves.
    expect(getDiscardConfirmDialogOpen()).toBe(true)
    expect(s81().activePanel).toEqual({ id: 'library', context: { workspaceId: 'ws-current' } })

    act(() => {
      resolveDiscardConfirmDialog(false)
    })
    expect(s81().activePanel).toEqual({ id: 'library', context: { workspaceId: 'ws-current' } })
    openSpy.mockRestore()
  })
})
