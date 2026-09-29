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
// therefore establishes opener-ness FIRST by driving the REAL pop-out flow,
// proven by the window.open spy, before the pop-out-close broadcast.
//
// Expand-drive ruling (SP-38/R11): full-screen Expand belongs to the shell.
// The panel reports only its current selection through registerExpandContext;
// this pack clicks the header button in a real SidePanelShell, which owns the
// shared #/panel/library route, opener handle, close and re-dock lifecycle.
//
// Oracles (§6, MAJ-006/MAJ-208, FR-018):
//   - an opener with an EMPTY panel slot re-docks the Library
//   - a DIFFERENT panel open -> re-dock is a no-op (never clobbers)
//   - a tab that does not hold the pop-out's window handle does not re-dock
//   - opener tab + SAME panel open + dirty -> the re-dock runs the leave
//     guard; cancel leaves the open Library where it was

import { useEffect } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, waitFor, fireEvent, screen, cleanup } from '@testing-library/react'
import { useUiStore } from '@/store/ui'
import { announcePanelPopoutClosed } from '@/lib/panelPopoutLifecycle'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { panels } from '@/components/panel-shell/registry'
import {
  setLibraryEditorDirty,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

// The mock explorer renders NO pop-out control: SP-38 moves Expand to the
// shell header, and the panel reports only its current selection through
// registerExpandContext (GREEN's colocated LibraryPanel.test.tsx asserts
// Close/Expand are left out of LibraryExplorer).
vi.mock('./LibraryExplorer', () => ({
  LibraryExplorer: (props: {
    onWorkspaceChange?: (id: string | null) => void
    onSelectionChange?: (selection: { path: string | null; folder: string }) => void
  }) => {
    useEffect(() => {
      props.onWorkspaceChange?.('ws-current')
      props.onSelectionChange?.({ path: 'Notes/Current.md', folder: 'Notes' })
    }, [])
    return <div data-testid="mock-library-explorer" />
  },
}))

import { LibraryPanel } from './LibraryPanel'

class RowResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe() {
    this.callback(
      [{ contentRect: { width: 1280 } as DOMRectReadOnly } as ResizeObserverEntry],
      this as unknown as ResizeObserver,
    )
  }
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', RowResizeObserver)

function popup() {
  return {
    closed: false,
    opener: {} as Window | null,
    close: vi.fn(),
    focus: vi.fn(),
    location: { replace: vi.fn() },
  }
}

function renderShell() {
  return render(
    <SidePanelShell
      panels={panels}
      username="dana"
      chat={<div data-testid="chat-probe">chat</div>}
    />,
  )
}

async function expandLibrary(child: ReturnType<typeof popup>) {
  const openSpy = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
  act(() => {
    s81().openPanel('library', { workspaceId: 'ws-current' })
  })
  renderShell()
  await screen.findByTestId('mock-library-explorer')
  fireEvent.click(screen.getByRole('button', { name: 'Expand Library panel' }))
  await waitFor(() => expect(s81().activePanel).toBeNull())

  expect(openSpy).toHaveBeenCalledTimes(1)
  expect(openSpy).toHaveBeenCalledWith('about:blank', '_blank')
  const href = child.location.replace.mock.calls[0]?.[0] as string
  expect(href).toMatch(/^\/#\/panel\/library\?/)
  const search = new URLSearchParams(href.split('?')[1])
  expect(search.get('workspace')).toBe('ws-current')
  const popoutId = search.get('popout')
  expect(popoutId).not.toBeNull()
  return popoutId!
}

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
  // Cancel (never proceed) any dialog a previous test left pending — the
  // batch-3 rationale: resolving `true` would complete the previous test's
  // transition as a reset side effect.
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
})

afterEach(() => {
  cleanup()
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
  vi.restoreAllMocks()
})

describe('pop-out re-dock no-clobber and opener-only (§12 #8c, #19)', () => {
  it('RED — an opener re-docks the Library into an EMPTY panel slot (MAJ-006)', async () => {
    const popoutId = await expandLibrary(popup())

    act(() => {
      announcePanelPopoutClosed('library', popoutId, {
        workspaceId: 'ws-other',
        path: 'Notes/Other.md',
      })
    })

    await waitFor(() => {
      expect(s81().activePanel).toEqual({
        id: 'library',
        context: { workspaceId: 'ws-other', path: 'Notes/Other.md' },
      })
    })
  })

  it('RED — a pop-out close does NOT re-dock while a DIFFERENT panel is open (MAJ-006)', async () => {
    // F-B1 (CHECK part B): the MAJ-208 ownership guard absorbs the broadcast
    // unless THIS TAB is the opener — without opener-ness the no-clobber
    // checks are unreachable and a mutant deleting both stayed green. This
    // test therefore establishes opener-ness FIRST (same flow as the
    // same-panel scenario below): only MAJ-006 can now keep the docked
    // DIFFERENT panel in place.
    const popoutId = await expandLibrary(popup())

    // The operator docks a DIFFERENT panel while the pop-out lives.
    act(() => {
      s81().openPanel('browser', { sessionId: 'sess-1', agentId: 'mia' })
    })

    // The pop-out tab closes.
    act(() => {
      announcePanelPopoutClosed('library', popoutId, { workspaceId: 'ws-other' })
    })
    // BroadcastChannel delivery is async. Wait long enough for a would-be
    // re-dock to land, then assert the Browser was NOT clobbered.
    await new Promise((resolve) => setTimeout(resolve, 200))
    expect(s81().activePanel).toEqual({ id: 'browser', context: { sessionId: 'sess-1', agentId: 'mia' } })
  })

  it('RED — a tab that never opened the pop-out (no window handle) does not re-dock (MAJ-208)', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
    render(<LibraryPanel />)
    // Prove this tab holds NO opener handle: it never called window.open.
    expect(openSpy).not.toHaveBeenCalled()

    act(() => {
      announcePanelPopoutClosed('library', 'not-opened-here', { workspaceId: 'ws-99' })
    })
    await act(async () => {})

    expect(s81().activePanel).toBeNull()
  })

  it('RED — opener tab, SAME panel open with unsaved edits: the re-dock runs the discard guard; cancel leaves the workspace (MAJ-208, FR-013, FR-018)', async () => {
    const popoutId = await expandLibrary(popup())

    // The operator re-docks the Library while the pop-out lives (the two
    // surfaces may legitimately coexist — libraryHandoff.ts), then edits.
    act(() => {
      s81().openPanel('library', { workspaceId: 'ws-current' })
    })
    setLibraryEditorDirty(true)

    // The pop-out tab closes.
    act(() => {
      announcePanelPopoutClosed('library', popoutId, { workspaceId: 'ws-other' })
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
  })
})
