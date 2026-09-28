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
// Expand-drive ruling (batch-4 item 1, §2.2/§2.3): the pop-out is NO LONGER
// driven through LibraryExplorer's onPopOut button — the "Open in new tab"
// control MOVES TO THE SHELL HEADER ("panel keeps the behaviour, not the
// button", §2.2; §2.3: "content behaviours executed through the shell's
// single Expand action"). The pack drives Expand the way the shell does:
// the panel registers its pop-out behaviour through the shell-provided
// registerExpand callback (§8.1: "content: React component (receives
// close/expand callbacks via props)"; "Expand delegates to the panel's own
// pop-out behaviour"), and the test standing in for the shell invokes that
// registered action and closes the docked panel on success (US-6/SP-12:
// Expand closes the source panel, through the gate).
//
// Oracles (§6, MAJ-006/MAJ-208, FR-018):
//   - a DIFFERENT panel open -> re-dock is a no-op (never clobbers)
//   - a tab that does not hold the pop-out's window handle does not re-dock
//   - opener tab + SAME panel open + dirty -> the re-dock runs the leave
//     guard; cancel leaves the open Library where it was

import { useEffect, type ComponentType } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, waitFor } from '@testing-library/react'
import { useUiStore } from '@/store/ui'
import { announceLibraryPopoutClosed } from '@/lib/libraryHandoff'
import {
  setLibraryEditorDirty,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

// The mock explorer renders NO pop-out control: §2.2 moves "Open in new tab"
// to the shell header, and the panel passes its behaviour to the shell via
// registerExpand instead of rendering the button (GREEN's colocated
// LibraryPanel.test.tsx asserts Close/Expand are left out of LibraryExplorer).
vi.mock('./LibraryExplorer', () => ({
  LibraryExplorer: (props: { onWorkspaceChange?: (id: string | null) => void }) => {
    useEffect(() => {
      props.onWorkspaceChange?.('ws-current')
    }, [])
    return <div data-testid="mock-library-explorer" />
  },
}))

import { LibraryPanel } from './LibraryPanel'

/** §8.1: the shell hands panel content close/expand callbacks via props —
 * including registerExpand, through which the panel supplies the pop-out
 * behaviour the shell header's Expand action invokes (§2.2/§2.3). Wave-0
 * panel-shell/types.ts does not carry registerExpand yet, so the contract is
 * typed structurally here; the component cast keeps this file compiling on
 * the pre-GREEN tree (whose LibraryPanel predates shell hosting) without
 * loosening what is asserted. */
type ShellExpandProps = {
  context: Record<string, unknown>
  close: () => void
  expand: () => void
  registerExpand: (action: () => boolean) => void
  onWidthSettle: () => void
}
const ShellHostedLibraryPanel = LibraryPanel as unknown as ComponentType<{ shellProps?: ShellExpandProps }>

let registeredExpand: (() => boolean) | null = null

function makeShellProps(context: Record<string, unknown>): { shellProps: ShellExpandProps } {
  return {
    shellProps: {
      context,
      close: () => s81().closePanel(),
      expand: () => {},
      registerExpand: (action) => {
        registeredExpand = action
      },
      onWidthSettle: () => {},
    },
  }
}

/** The shell's Expand click (US-6/SP-12): invoke the panel's registered
 * pop-out behaviour; a successful open closes the docked panel — through
 * the gate. Returns whether the expand happened. */
function invokeShellExpand(): boolean {
  let opened = false
  act(() => {
    opened = registeredExpand?.() ?? false
    if (opened) s81().closePanel()
  })
  return opened
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
  registeredExpand = null
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
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
})

describe('pop-out re-dock no-clobber and opener-only (§12 #8c, #19)', () => {
  it('RED — a pop-out close does NOT re-dock while a DIFFERENT panel is open (MAJ-006)', async () => {
    // F-B1 (CHECK part B): the MAJ-208 ownership guard absorbs the broadcast
    // unless THIS TAB is the opener — without opener-ness the no-clobber
    // checks are unreachable and a mutant deleting both stayed green. This
    // test therefore establishes opener-ness FIRST (same flow as the
    // same-panel scenario below): only MAJ-006 can now keep the docked
    // DIFFERENT panel in place.
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({ closed: false, close: () => {} } as unknown as Window)
    act(() => {
      s81().openPanel('library', { workspaceId: 'ws-current' })
    })
    render(<ShellHostedLibraryPanel {...makeShellProps({ workspaceId: 'ws-current' })} />)
    await waitFor(() => expect(registeredExpand).not.toBeNull())

    // Establish THIS TAB as the opener: Expand through the shell's
    // registerExpand path; the docked panel closes (US-6/SP-12) and the
    // window handle exists HERE.
    const opened = invokeShellExpand()
    expect(opened).toBe(true)
    expect(openSpy).toHaveBeenCalledTimes(1)
    expect(s81().activePanel).toBeNull()

    // The operator docks a DIFFERENT panel while the pop-out lives.
    act(() => {
      s81().openPanel('browser', { sessionId: 'sess-1', agentId: 'mia' })
    })

    // The pop-out tab closes.
    act(() => {
      announceLibraryPopoutClosed('ws-other')
    })
    // BroadcastChannel delivery is async. Wait long enough for a would-be
    // re-dock to land, then assert the Browser was NOT clobbered.
    await new Promise((resolve) => setTimeout(resolve, 200))
    expect(s81().activePanel).toEqual({ id: 'browser', context: { sessionId: 'sess-1', agentId: 'mia' } })
    openSpy.mockRestore()
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
    act(() => {
      s81().openPanel('library', { workspaceId: 'ws-current' })
    })
    render(<ShellHostedLibraryPanel {...makeShellProps({ workspaceId: 'ws-current' })} />)
    // The panel registers its pop-out behaviour with the shell (§8.1).
    await waitFor(() => expect(registeredExpand).not.toBeNull())

    // Establish THIS TAB as the opener (MAJ-208): drive Expand the way the
    // shell does — invoke the registered action; a successful open closes
    // the docked panel (US-6/SP-12, through the gate). The spy proves the
    // opener handle was created HERE.
    const opened = invokeShellExpand()
    expect(opened).toBe(true)
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
