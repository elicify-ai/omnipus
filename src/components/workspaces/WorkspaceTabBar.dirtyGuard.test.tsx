// WorkspaceTabBar.dirtyGuard.test.tsx — gap pack from pr-test-analyzer F1,
// the CLOSE half of the tab-strip toggle (US-4 AS-2 through the CRIT-001
// gate; the OPEN half and the ARIA model are pinned by
// WorkspaceTabBar.toggle.test.tsx, which this file deliberately does not
// touch).
//
// Oracle (side-panel-shell-spec.md, derived BEFORE re-reading the toggle):
//   US-4 AS-4: "Given Library open WITH unsaved edits, When ANY close/replace
//   path fires (open-another via tab toggle, ...), Then the discard-
//   confirmation prompt appears on every path, and cancelling leaves the
//   Library panel open with the edit intact."
//   US-4 AS-2 / §7 state machine: "Clicking the open panel's toggle MUST
//   close it (SP-11) — through the gate."
//   US-4 AS-5: "Given Library open with a CLEAN editor state, When any of
//   those paths fires, Then the replacement happens immediately with no
//   prompt."
//   §7: "No path may silently discard unsaved Library edits (CRIT-001):
//   every close/replace path is gated (FR-013)."
//
// Characterisation pack: GREEN already implements this; every expectation
// here is derived from the spec text above, not observed off the component.
// Unit boundary: the REAL WorkspaceTabBar → REAL leaveGate → REAL
// unsavedGuard module store. The discard dialog itself is not mocked: its
// open state is read from the guard module (getDiscardConfirmDialogOpen) and
// the user's answer is delivered through the same public function the real
// dialog host calls (resolveDiscardConfirmDialog), so the seam under test —
// "the guard runs before the store moves" — is real end to end.

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { useUiStore } from '@/store/ui'
import {
  setLibraryEditorDirty,
  isLibraryEditorDirty,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

let mockPathname = '/workspaces/ws-1/chat'
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: mockPathname }),
  useNavigate: () => mockNavigate,
  Link: ({
    children,
    to,
    params,
    'data-testid': testId,
    'aria-current': ariaCurrent,
    'aria-label': ariaLabel,
  }: {
    children: React.ReactNode
    to: string
    params?: Record<string, string>
    'data-testid'?: string
    'aria-current'?: React.HTMLAttributes<HTMLAnchorElement>['aria-current']
    'aria-label'?: string
  }) => {
    const href = params ? to.replace('$workspaceId', params.workspaceId) : to
    return (
      <a href={href} data-testid={testId} aria-current={ariaCurrent} aria-label={ariaLabel}>
        {children}
      </a>
    )
  },
}))

vi.mock('framer-motion', () => ({
  motion: {
    div: ({ children, ...rest }: React.HTMLAttributes<HTMLDivElement>) => <div {...rest}>{children}</div>,
  },
}))

vi.mock('@/components/ui/dropdown-menu', async () => {
  const { Button } = await vi.importActual<typeof import('@/components/ui/button')>('@/components/ui/button')
  return {
    DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
      asChild ? <>{children}</> : <div>{children}</div>,
    DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuItem: ({ children, onClick, className, ...rest }: {
      children: React.ReactNode
      onClick?: () => void
      className?: string
    } & Record<string, unknown>) => {
      void className
      return <Button variant="ghost" onClick={onClick} {...rest}>{children}</Button>
    },
  }
})

import { WorkspaceTabBar } from './WorkspaceTabBar'

function activePanel() {
  return useUiStore.getState().activePanel ?? null
}

beforeEach(() => {
  mockNavigate.mockClear()
  mockPathname = '/workspaces/ws-1/chat'
  act(() => {
    useUiStore.setState({ activePanel: null })
  })
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(true)
})

afterEach(() => {
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(true)
})

/** Render the strip with the Library panel ALREADY open, scoped to ws-1 —
 * the state from which a second click on the entry is the CLOSE path. */
function renderStripWithLibraryOpen() {
  act(() => {
    useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
  })
  render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
  return screen.getByTestId('workspace-tab-media')
}

describe('WorkspaceTabBar toggle CLOSE path — CRIT-001 leave gate (US-4 AS-4/AS-5, FR-013)', () => {
  it('a second click on the Library entry with UNSAVED edits opens the discard prompt and keeps the panel (US-4 AS-4)', async () => {
    const libraryEntry = renderStripWithLibraryOpen()
    setLibraryEditorDirty(true)

    fireEvent.click(libraryEntry)

    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))
    // The guard runs BEFORE the store moves: the panel is still open while
    // the question is up — nothing was silently discarded.
    expect(activePanel()).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })
  })

  it('cancelling the prompt keeps the Library panel open AND the edit intact (US-4 AS-4)', async () => {
    const libraryEntry = renderStripWithLibraryOpen()
    setLibraryEditorDirty(true)

    fireEvent.click(libraryEntry)
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(false)
    })

    expect(getDiscardConfirmDialogOpen()).toBe(false)
    expect(activePanel()).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })
    // "the edit intact": the dirty flag survives a cancel — only a Discard
    // answer may clear it (unsavedGuard's contract).
    expect(isLibraryEditorDirty()).toBe(true)
  })

  it('confirming the discard closes the panel through the gate and clears the edit (US-4 AS-4, SP-11)', async () => {
    const libraryEntry = renderStripWithLibraryOpen()
    setLibraryEditorDirty(true)

    fireEvent.click(libraryEntry)
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(true)
    })

    await waitFor(() => expect(activePanel()).toBeNull())
    // The operator chose to discard, so the edit is gone with the panel.
    expect(isLibraryEditorDirty()).toBe(false)
    expect(getDiscardConfirmDialogOpen()).toBe(false)
  })

  it('a CLEAN Library closes IMMEDIATELY on the second click with no prompt (US-4 AS-5)', () => {
    const libraryEntry = renderStripWithLibraryOpen()
    expect(isLibraryEditorDirty()).toBe(false)

    fireEvent.click(libraryEntry)

    // Synchronous: no dialog round-trip, no waitFor — the store has already
    // moved by the time the click handler returns (leaveGate's clean path).
    expect(activePanel()).toBeNull()
    expect(getDiscardConfirmDialogOpen()).toBe(false)
  })
})
