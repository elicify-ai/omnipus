// -fullscreen.exit.escapeBypass.test.tsx — regression test for the real UAT
// finding against feat/resizable-side-panels@952de5216 (commit 002784be0,
// "add shared full-screen back to chat exit"):
//
//   1. Dirty Library editor, focus in the editor, Escape once: no-op
//      (correct — the editable-field exception in
//      src/components/panel-shell/panelEscape.ts::shouldClosePanelOnEscape).
//   2. Focus leaves the editor, Escape again: the discard-confirm dialog
//      opens (correct — beforeLeave -> confirmDiscardLibraryEdits(), dirty).
//   3. Escape a THIRD time, while the dialog is open. OBSERVED (real
//      browser, Playwright, UAT session): the tab closes immediately,
//      discarding the edit, with no confirmation.
//
// EXPECTED per the brief this branch shipped under: "Escape does nothing and
// the discard prompt/dialog keeps focus" — the edit must survive and the
// panel must not close. This test's oracle is that brief line, not any
// runtime output: the edit must still read "unsaved edit" and window.close
// must never have been called.
//
// STATUS (2026-09-29, qa-lead RED dispatch): this test currently PASSES
// against the unfixed pre-change code — it is NOT proven red. Reported to
// the squad lead as a finding, not silently shipped as the required RED.
// Traced with debug instrumentation (removed before commit; receipts under
// coordination/logs/side-panel-shell/receipts/fsx-red/bug1-debug*.log) that
// at the moment of the third Escape, BOTH of the app's own protections are
// already active: (a) event.defaultPrevented is already true, because
// @radix-ui/react-dismissable-layer's document-capture-phase Escape handler
// (node_modules/@radix-ui/react-dismissable-layer/dist/index.mjs
// ::handleKeyDown) calls event.preventDefault() synchronously before
// dismissing, and panelEscape.ts::shouldClosePanelOnEscape's own
// `event.defaultPrevented` check (line 11) catches that; (b) independently,
// the DOM still shows the dialog at `data-state="open"` (React's re-render
// from unsavedGuard.ts's `open=false` had not yet been committed), which
// shouldClosePanelOnEscape's own `[role="alertdialog"]:not(...
// [data-state="closed"])` querySelector fallback (line 14-17) also catches.
// This held even when the third Escape was fired in the SAME synchronous
// tick as the second (no `waitFor` in between) — Testing Library's
// `fireEvent` wraps every dispatch in `act()`, which flushes React's render
// AND effect phases synchronously, so this harness cannot expose any race
// that depends on an UNFLUSHED render — a structural limitation of
// Vitest+RTL for this class of bug, not proof the real-browser bug is
// impossible. The brief's leading hypothesis (a race on
// unsavedGuard.ts's `open` boolean, read by getDiscardConfirmDialogOpen(),
// between Radix's own dismiss and the route's bubble-phase check) is
// REFUTED as the sole cause: `getDiscardConfirmDialogOpen()` genuinely does
// go stale-false at that moment (confirmed in the debug trace), but the two
// OTHER checks in the same function's OR-chain (line 11's
// `defaultPrevented`, line 14-17's DOM query) still block the close, so the
// documented race is real but harmless as currently written.
//
// Kept as written (assertions unweakened) because it is still the correct
// oracle-independent test of the EXPECTED behavior from the brief; if GREEN
// deletes any of the three independent guards this test would go red for
// the right reason. Left for the squad lead to decide next steps (a
// real-browser Playwright repro against a running dev server, or a request
// for the UAT session's trace/video for the exact focus/timing sequence).
//
// Unlike src/routes/-fullscreen.exit.test.tsx (which mocks `beforeLeave`
// directly with a vi.fn and a plain manually-toggled mock `role="dialog"`
// element), this file wires the REAL production pieces the bug lives in:
// src/components/library/preview/unsavedGuard.ts (real
// confirmDiscardLibraryEdits/setLibraryEditorDirty/resolveDiscardConfirmDialog,
// unmocked) and src/components/ui/confirm-dialog.tsx's real
// <ConfirmDialog> (a real Radix AlertDialog, unmocked) wired exactly as
// src/components/library/LibraryExplorer.tsx wires it
// (open={useSyncExternalStore(subscribeDiscardConfirmDialog,
// getDiscardConfirmDialogOpen)}; onOpenChange resolves false; onConfirm
// resolves true) — because the bug is specifically in how Radix's own
// Escape handling on the real dialog interacts with the route's window-level
// bubble listener (src/routes/_fullscreen.panel.$panelId.tsx's `onKeyDown`
// calling src/components/panel-shell/panelEscape.ts::shouldClosePanelOnEscape),
// which a manually-toggled div and a mocked beforeLeave cannot exercise.
//
// Oracle: the branch's own shipped brief, quoted above (docs cited in the
// dispatch brief this test was written from) — not the implementation.

import { useSyncExternalStore } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryHistory, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import type { PanelContentProps, PanelContext, PanelDefinition, PanelId } from '@/components/panel-shell/types'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import {
  confirmDiscardLibraryEdits,
  discardConfirmDialogHostUnmounted,
  getDiscardConfirmDialogOpen,
  isLibraryEditorDirty,
  resolveDiscardConfirmDialog,
  setLibraryEditorDirty,
  subscribeDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

const mocks = vi.hoisted(() => ({
  announceClosed: vi.fn(),
  fetchWorkspaces: vi.fn(),
}))

/** Mirrors LibraryExplorer.tsx's own dialog-hosting wiring (lines ~1548-1558
 * as read for this task), minus everything unrelated to the Escape bug. */
function LibraryTestContent(props: PanelContentProps) {
  const dialogOpen = useSyncExternalStore(subscribeDiscardConfirmDialog, getDiscardConfirmDialogOpen)
  return (
    <div data-testid="panel-content">
      <textarea
        data-testid="panel-editor"
        defaultValue="original draft"
        onChange={(event) => {
          setLibraryEditorDirty(event.target.value !== 'original draft')
        }}
      />
      <div data-testid="outside-panel-editor" tabIndex={-1}>outside the editor</div>
      <ConfirmDialog
        open={dialogOpen}
        onOpenChange={(next) => {
          if (!next) resolveDiscardConfirmDialog(false)
        }}
        title="Discard unsaved changes?"
        description="You have unsaved changes in the Library editor. Leaving now will discard them. Continue?"
        confirmLabel="Discard"
        destructive
        onConfirm={() => resolveDiscardConfirmDialog(true)}
      />
      <span data-testid="panel-context">{JSON.stringify(props.context)}</span>
    </div>
  )
}

vi.mock('@/components/panel-shell/registry', () => {
  const definitions: Record<'library', PanelDefinition> = {
    library: {
      id: 'library',
      title: 'Library',
      content: LibraryTestContent,
      fullScreen: {
        toSearch: (context: PanelContext) => ({
          ...(context.workspaceId ? { workspace: context.workspaceId } : {}),
          ...(context.path ? { path: context.path } : {}),
        }),
        fromSearch: (search: Record<string, unknown>) => ({
          ...(typeof search.workspace === 'string' ? { workspaceId: search.workspace } : {}),
          ...(typeof search.path === 'string' ? { path: search.path } : {}),
        }),
      },
      // The REAL registry.tsx wiring (registry.tsx lines ~49-50, read for
      // this task) — not a vi.fn(). This is the actual production seam the
      // bug lives behind.
      beforeLeave: () => confirmDiscardLibraryEdits(),
      beforeLeaveRequired: () => isLibraryEditorDirty(),
    },
  }
  return {
    getPanelDefinition: (id: PanelId) => definitions[id as keyof typeof definitions],
  }
})

vi.mock('./-authenticatedBeforeLoad', () => ({ authenticatedBeforeLoad: vi.fn(async () => {}) }))
vi.mock('@/components/layout/AppShell', () => ({ AppShell: () => <Outlet /> }))
vi.mock('@/components/workspaces/WorkspaceTabContainer', () => ({ WorkspaceTabContainer: () => <Outlet /> }))
vi.mock('@/components/workspaces/WorkspaceChatTab', () => ({
  WorkspaceChatTab: () => {
    const [ready] = useState(true)
    return ready ? <textarea data-testid="chat-input" /> : <div data-testid="workspace-chat-restoring" />
  },
}))
vi.mock('@/lib/panelTabPresence', () => ({
  announcePanelTabPresence: vi.fn(() => ({ update: vi.fn(), stop: vi.fn() })),
  panelIdentityFromContext: (panelId: PanelId, context: PanelContext) => ({ panelId, ...context }),
}))
vi.mock('@/lib/panelPopoutLifecycle', () => ({
  announcePanelPopoutClosed: mocks.announceClosed,
  announcePanelPopoutContext: vi.fn(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchWorkspaces: mocks.fetchWorkspaces,
}))

import { routeTree } from '@/routeTree.gen'

const originalClosed = Object.getOwnPropertyDescriptor(window, 'closed')

async function renderPanel(path: string) {
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  await screen.findByTestId('fullscreen-panel')
  return { router, client }
}

beforeEach(() => {
  mocks.announceClosed.mockReset()
  mocks.fetchWorkspaces.mockReset().mockResolvedValue([
    { id: 'ws-home', name: 'Home', status: 'active', is_default: true },
  ])
  useUiStore.getState().closePanel()
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  vi.spyOn(window, 'close').mockImplementation(() => {})
  Object.defineProperty(window, 'closed', { configurable: true, value: false })
  // unsavedGuard.ts is a module-level singleton (by design — see its own
  // doc comment) shared across every test file that imports it; never
  // bare-assume the previous test left it clean.
  setLibraryEditorDirty(false)
  discardConfirmDialogHostUnmounted()
})

afterEach(() => {
  cleanup()
  useUiStore.getState().closePanel()
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  vi.restoreAllMocks()
  if (originalClosed) Object.defineProperty(window, 'closed', originalClosed)
  else Reflect.deleteProperty(window, 'closed')
  setLibraryEditorDirty(false)
  discardConfirmDialogHostUnmounted()
})

describe('full-screen exit — Escape must never bypass the discard-confirm dialog', () => {
  it('BUG: a third Escape while the discard dialog is open must NOT close the panel or discard the edit', async () => {
    const { router, client } = await renderPanel('/panel/library?workspace=ws-a&popout=popout-a')
    const editor = screen.getByTestId('panel-editor')

    // Make the editor dirty, exactly as a real edit would.
    fireEvent.change(editor, { target: { value: 'unsaved edit' } })
    expect(editor).toHaveValue('unsaved edit')
    expect(isLibraryEditorDirty()).toBe(true)

    // Escape #1 — focus is IN the editable field: no-op (the editable-field
    // exception in panelEscape.ts::shouldClosePanelOnEscape).
    editor.focus()
    fireEvent.keyDown(editor, { key: 'Escape' })
    await act(async () => {
      await Promise.resolve()
    })
    expect(window.close).not.toHaveBeenCalled()
    expect(mocks.announceClosed).not.toHaveBeenCalled()
    expect(getDiscardConfirmDialogOpen()).toBe(false)

    // Focus leaves the editor.
    screen.getByTestId('outside-panel-editor').focus()

    // Escape #2 — focus outside the field, dirty: opens the discard-confirm
    // dialog (beforeLeave -> confirmDiscardLibraryEdits(), since dirty).
    fireEvent.keyDown(screen.getByTestId('fullscreen-panel'), { key: 'Escape' })
    await waitFor(() => expect(screen.getByRole('alertdialog')).toBeInTheDocument())
    expect(getDiscardConfirmDialogOpen()).toBe(true)
    expect(window.close).not.toHaveBeenCalled()
    expect(editor).toHaveValue('unsaved edit')

    // Escape #3 — fired while the REAL discard-confirm dialog is open.
    // Dispatched on the dialog's own content, matching a real user keypress
    // (Radix auto-focuses the dialog content on open): the native event then
    // propagates through the real capture-then-bubble chain — Radix's own
    // document-capture-phase Escape listener first
    // (@radix-ui/react-dismissable-layer), then the route's window
    // bubble-phase `keydown` listener
    // (src/routes/_fullscreen.panel.$panelId.tsx) — exactly the path the
    // real browser bug travels.
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape' })
    await act(async () => {
      await Promise.resolve()
    })

    // EXPECTED (per the branch's own shipped brief, quoted in this file's
    // header comment): nothing should happen — the edit must survive and
    // the panel must not close.
    expect(window.close).not.toHaveBeenCalled()
    expect(mocks.announceClosed).not.toHaveBeenCalled()
    expect(editor).toHaveValue('unsaved edit')
    expect(isLibraryEditorDirty()).toBe(true)
    expect(router.state.location.pathname).toBe('/panel/library')

    client.clear()
  })
})
