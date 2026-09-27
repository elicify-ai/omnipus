// LibraryPanel.redock.test.tsx — side-panel-shell-spec.md §12 #8c + #19.
//
// RED against today's LibraryPanel (read before writing): onLibraryPopoutClosed
// calls openLibraryPanel UNCONDITIONALLY, and every mounted listener reacts to
// the broadcast — there is no window-handle check and no beforeLeave.
//
// Oracles (§6, MAJ-006 as corrected by MAJ-208, FR-018):
//   - a DIFFERENT panel open → re-dock is a no-op (never clobbers)
//   - a tab that does not hold the pop-out's window handle does not re-dock
//   - a same-panel re-dock follows the pop-out's last workspace THROUGH the
//     leave guard; cancel leaves the open Library where it was
// The empty-slot re-open and the Dana same-panel follow (when this tab is the
// opener and the editor is clean) stay in LibraryPanel.test.tsx.

import { useEffect } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import { useUiStore } from '@/store/ui'
import { announceLibraryPopoutClosed } from '@/lib/libraryHandoff'
import {
  setLibraryEditorDirty,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

vi.mock('./LibraryExplorer', () => ({
  LibraryExplorer: (props: { onWorkspaceChange?: (id: string | null) => void }) => {
    useEffect(() => {
      props.onWorkspaceChange?.('ws-current')
    }, [])
    return <div data-testid="mock-library-explorer" />
  },
}))

import { LibraryPanel } from './LibraryPanel'

beforeEach(() => {
  useUiStore.setState({ libraryPanel: null, browserPanel: null, toasts: [] })
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
      useUiStore.getState().openBrowserPanel('sess-1', 'mia')
    })

    act(() => {
      announceLibraryPopoutClosed('ws-99')
    })
    // BroadcastChannel delivery is async. Wait long enough for today's
    // unconditional re-dock to land, then assert it did not.
    await new Promise((resolve) => setTimeout(resolve, 200))
    expect(useUiStore.getState().libraryPanel).toBeNull()
    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'sess-1', agentId: 'mia' })
  })

  it('RED — a tab that never opened the pop-out (no window handle) does not re-dock (MAJ-208)', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
    render(<LibraryPanel />)
    expect(openSpy).not.toHaveBeenCalled()

    act(() => {
      announceLibraryPopoutClosed('ws-99')
    })
    await act(async () => {})

    expect(useUiStore.getState().libraryPanel).toBeNull()
    openSpy.mockRestore()
  })

  it('RED — a same-panel re-dock with unsaved edits runs the discard guard and a cancel leaves the workspace (FR-013, FR-018)', async () => {
    render(<LibraryPanel />)
    act(() => {
      useUiStore.getState().openLibraryPanel('ws-current')
    })
    setLibraryEditorDirty(true)

    act(() => {
      announceLibraryPopoutClosed('ws-other')
    })
    await act(async () => {})

    expect(getDiscardConfirmDialogOpen()).toBe(true)
    expect(useUiStore.getState().libraryPanel).toEqual({ workspaceId: 'ws-current' })

    act(() => {
      resolveDiscardConfirmDialog(false)
    })
    expect(useUiStore.getState().libraryPanel).toEqual({ workspaceId: 'ws-current' })
  })
})
