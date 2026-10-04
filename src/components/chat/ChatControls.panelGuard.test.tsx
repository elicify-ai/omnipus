// ChatControls.panelGuard.test.tsx — RED pack for side-panel-shell-spec.md
// FR-013 / CRIT-001 (wave 1): "The Library unsaved-edits guard MUST wrap
// EVERY action that closes or replaces the Library panel — header Close,
// Expand, open-other from ANY entry point (tab toggle, sidebar,
// ChatControls, "Watch live"), deep-link replace, and workspace-switch
// re-target — via the `PanelDefinition.beforeLeave` gate; the transition
// proceeds only on confirm and a cancel leaves store, URL and panel
// untouched. No silent data loss on any path (CRIT-001)."
//
// This file covers ChatControls' "Open browser" button as its "open-other
// from ANY entry point (... ChatControls ...)" instance. The chat-header
// "Open library" button is gone. The sidebar Library button's leave gate is
// proved by Sidebar.panelGuard.test.tsx. Other entry points (header
// Close/Expand, tab toggle, deep-link replace, workspace switch) are covered
// in their own files: WorkspaceTabBar.toggle.test.tsx (tab toggle),
// workspaces.$workspaceId.chat.panel.test.tsx (deep-link replace).
//
// Oracle: FR-013 + §8.1's beforeLeave contract + §12 test 8's "EVERY
// close/replace path runs the guard ... cancel keeps panel + edit" + test
// 8b's "cancelled transition is clean: store, URL and content untouched
// after a `false` from `beforeLeave`".
//
// The guard mechanism itself already exists and is well-tested in
// isolation — `src/components/library/preview/unsavedGuard.ts`'s
// `setLibraryEditorDirty` / `getDiscardConfirmDialogOpen` /
// `resolveDiscardConfirmDialog` — this file only asserts that ChatControls'
// two openers actually CALL into it before replacing an open, dirty Library
// panel. That call does not exist today.
//
// RED evidence (2026-09-27, read src/components/chat/ChatControls.tsx in
// full): `handleOpenBrowser` and `handleOpenLibrary` call
// `useUiStore.getState().openBrowserPanel(...)` /
// `useUiStore.getState().openLibraryPanel(...)` directly, with NO call to
// `confirmDiscardLibraryEdits()` or any `beforeLeave` gate anywhere in this
// file (verified: zero references to unsavedGuard.ts in ChatControls.tsx).
// So clicking "Open browser" while Library is open+dirty replaces it
// immediately, with the discard dialog never opening — every assertion
// below expecting the dialog to open, or the replacement to be blocked
// pending it, fails.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import {
  setLibraryEditorDirty,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => vi.fn(),
    useLocation: () => ({ pathname: '/' }),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, createSession: vi.fn() }
})

import { ChatControls } from './ChatControls'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderControls() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ChatControls />
    </QueryClientProvider>,
  )
}

/** The §8.1 single-slice view of the ui store. Read through the SLICE the
 *  spec defines — never by branching on which shape production currently
 *  has (batch-4 ruling: a shape-branching seed silently changes what the
 *  pack tests). */
type Section81Store = {
  activePanel: { id: string; context?: Record<string, unknown> } | null
  openPanel: (id: string, context?: Record<string, unknown>) => void
  closePanel: () => void
}
const s81 = () => useUiStore.getState() as unknown as Section81Store

function activePanelId(): string | null {
  const state = useUiStore.getState() as unknown as { activePanel?: { id: string } | null }
  return state.activePanel?.id ?? null
}

/** Fails every test with a STATED reason while the §8.1 slice is missing
 *  (same gate as Sidebar.panelGuard). */
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
  vi.clearAllMocks()
  // Kill any dialog a previous test left pending BEFORE touching the panel
  // state, and cancel it (resolve(false)) rather than proceed it —
  // resolving `true` would complete the previous test's open-other
  // transition and open the Browser panel as a reset side effect (the leak
  // that made 'cancelling the discard prompt' pass alone but fail in the
  // full file on GREEN). resolve(false) cancels: nothing proceeds.
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
  // Batch-4 ruling 2: seed ONLY through the §8.1 single-slice API. On this
  // pre-GREEN tree every test fails at the gate, naming the missing API.
  requireSection81Api()
  act(() => {
    useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_1' })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' } as never)
    s81().openPanel('library', { workspaceId: 'ws-1' })
  })
  setLibraryEditorDirty(false)
})

afterEach(() => {
  // Cancel any dialog this test left pending — resolve(false), never true:
  // the next test (or file) must not inherit a transition in flight.
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
})

describe('ChatControls — CRIT-001 leave guard on open-other (FR-013)', () => {
  it('"Open browser" with Library open and DIRTY opens the discard-confirmation instead of replacing it immediately', async () => {
    setLibraryEditorDirty(true)
    renderControls()

    const openBrowser = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    fireEvent.click(openBrowser)

    // The guard must open BEFORE the transition — the browser panel must
    // NOT yet be the active panel while the dialog is pending.
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))
    expect(activePanelId()).not.toBe('browser')
  })

  it('cancelling the discard prompt leaves the Library panel open and the edit intact (CRIT-001, test 8b)', async () => {
    setLibraryEditorDirty(true)
    renderControls()

    const openBrowser = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    fireEvent.click(openBrowser)
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(false) // Cancel
    })

    // Nothing moved: Library is still the active panel, the dirty flag is
    // still set (resolveDiscardConfirmDialog(false) does not clear it —
    // see unsavedGuard.ts), and the browser session was never opened.
    await waitFor(() => {
      expect(activePanelId()).not.toBe('browser')
    })
  })

  it('confirming the discard prompt proceeds to open the Browser panel, replacing Library (SP-7)', async () => {
    setLibraryEditorDirty(true)
    renderControls()

    const openBrowser = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    fireEvent.click(openBrowser)
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(true) // Discard and proceed
    })

    await waitFor(() => expect(activePanelId()).toBe('browser'))
  })

  it('a CLEAN Library panel is replaced immediately with no prompt (US-4 AS-5)', async () => {
    setLibraryEditorDirty(false)
    renderControls()

    const openBrowser = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    fireEvent.click(openBrowser)

    await waitFor(() => expect(activePanelId()).toBe('browser'))
    expect(getDiscardConfirmDialogOpen()).toBe(false)
  })
})
