// WatchLive.dirtyGuard.test.tsx — gap pack from pr-test-analyzer F3: the
// three "Watch live" affordances (US-4 AS-1/AS-4, CRIT-001/FR-013) —
// BrowserTool.tsx (BrowserToolBlock), GenericToolCall.tsx (browser-tool rows
// on the replay path) and BrowserNavigate.tsx (BrowserNavigateBlock) — each
// REPLACES a possibly-dirty outgoing Library with the Browser panel, so each
// must run the leave gate FIRST.
//
// Oracle (side-panel-shell-spec.md, derived before re-reading the handlers):
//   US-4 AS-1: "Given Library is open, When the operator opens Browser (any
//   entry point: tab strip, 'Watch live', 'Open browser'), Then Library
//   closes and Browser docks in its place."
//   US-4 AS-4: "Given Library open WITH unsaved edits, When ANY close/replace
//   path fires (open-another via tab toggle, 'Watch live', 'Open browser',
//   ...), Then the discard-confirmation prompt appears on every path, and
//   cancelling leaves the Library panel open with the edit intact."
//   US-4 AS-5 (clean path): "the replacement happens immediately with no
//   prompt."
//   §5 error flows: "the 'Open browser' path creates NO paid session before
//   the guard passes (MIN-206)" — the guard precedes openPanel, not follows
//   it; pinned here by asserting the panel state stays Library-shaped while
//   the prompt is up.
//
// Characterisation pack: GREEN implements this. Unit boundary: REAL
// components → REAL leaveGate → REAL unsavedGuard; the dialog answer is
// delivered through the guard module's public API (the same function the
// mounted dialog host calls). The session store is seeded via its real
// setState (handleWatchLive reads it imperatively), not mocked.

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { GenericToolCall } from './GenericToolCall'
import { BrowserToolBlock } from './BrowserTool'
import { BrowserNavigateBlock } from './BrowserNavigate'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import {
  setLibraryEditorDirty,
  isLibraryEditorDirty,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

const COMPLETE = { type: 'complete' } as const

function seedLibraryOpen() {
  act(() => {
    useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
  })
}

function browserPanel() {
  return useUiStore.getState().activePanel
}

function resetStores() {
  act(() => {
    useUiStore.setState({ activePanel: null })
  })
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(true)
}

beforeEach(() => {
  act(() => {
    useSessionStore.setState({ activeSessionId: 'sess-1', activeAgentId: 'agent-1' })
  })
  resetStores()
})

afterEach(() => {
  resetStores()
})

/** The outgoing-panel invariant while the prompt is up: the Library panel is
 * still exactly what the click found — the gate runs BEFORE the store moves
 * (§5, MIN-206 ordering). */
function expectLibraryIntact() {
  expect(browserPanel()).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })
}

// ── BrowserTool.tsx (BrowserToolBlock) ───────────────────────────────────────

describe('BrowserToolBlock "Watch live" — leave gate (US-4 AS-1/AS-4)', () => {
  it('prompts instead of replacing a dirty Library, panel intact while the prompt is up', async () => {
    seedLibraryOpen()
    setLibraryEditorDirty(true)
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{ selector: '#submit' }}
        result={{ ok: true }}
        status={COMPLETE}
        isError={false}
        summary="#submit"
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))

    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))
    expectLibraryIntact()
  })

  it('cancel keeps the Library panel and the edit (US-4 AS-4)', async () => {
    seedLibraryOpen()
    setLibraryEditorDirty(true)
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{ selector: '#submit' }}
        result={{ ok: true }}
        status={COMPLETE}
        isError={false}
        summary="#submit"
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(false)
    })

    expect(getDiscardConfirmDialogOpen()).toBe(false)
    expectLibraryIntact()
    expect(isLibraryEditorDirty()).toBe(true)
  })

  it('confirm replaces Library with the Browser panel for the active session (US-4 AS-1)', async () => {
    seedLibraryOpen()
    setLibraryEditorDirty(true)
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{ selector: '#submit' }}
        result={{ ok: true }}
        status={COMPLETE}
        isError={false}
        summary="#submit"
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(true)
    })

    await waitFor(() =>
      expect(browserPanel()).toEqual({
        id: 'browser',
        context: { sessionId: 'sess-1', agentId: 'agent-1' },
      }),
    )
  })

  it('a CLEAN Library is replaced immediately, no prompt (US-4 AS-5)', () => {
    seedLibraryOpen()
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{ selector: '#submit' }}
        result={{ ok: true }}
        status={COMPLETE}
        isError={false}
        summary="#submit"
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))

    expect(browserPanel()).toEqual({
      id: 'browser',
      context: { sessionId: 'sess-1', agentId: 'agent-1' },
    })
    expect(getDiscardConfirmDialogOpen()).toBe(false)
  })
})

// ── GenericToolCall.tsx (replay-path browser rows) ──────────────────────────

describe('GenericToolCall "Watch live" — leave gate (US-4 AS-1/AS-4)', () => {
  it('prompts instead of replacing a dirty Library, panel intact while the prompt is up', async () => {
    seedLibraryOpen()
    setLibraryEditorDirty(true)
    render(
      <GenericToolCall toolName="browser.screenshot" args={{}} result={{}} status={COMPLETE} />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))

    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))
    expectLibraryIntact()
  })

  it('cancel keeps the Library panel and the edit (US-4 AS-4)', async () => {
    seedLibraryOpen()
    setLibraryEditorDirty(true)
    render(
      <GenericToolCall toolName="browser.screenshot" args={{}} result={{}} status={COMPLETE} />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(false)
    })

    expect(getDiscardConfirmDialogOpen()).toBe(false)
    expectLibraryIntact()
    expect(isLibraryEditorDirty()).toBe(true)
  })

  it('confirm replaces Library with the Browser panel for the active session (US-4 AS-1)', async () => {
    seedLibraryOpen()
    setLibraryEditorDirty(true)
    render(
      <GenericToolCall toolName="browser.screenshot" args={{}} result={{}} status={COMPLETE} />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(true)
    })

    await waitFor(() =>
      expect(browserPanel()).toEqual({
        id: 'browser',
        context: { sessionId: 'sess-1', agentId: 'agent-1' },
      }),
    )
  })
})

// ── BrowserNavigate.tsx (BrowserNavigateBlock) ───────────────────────────────

describe('BrowserNavigateBlock "Watch live" — leave gate (US-4 AS-1/AS-4)', () => {
  it('prompts instead of replacing a dirty Library, panel intact while the prompt is up', async () => {
    seedLibraryOpen()
    setLibraryEditorDirty(true)
    render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com/page' }}
        result={{ title: 'Example' }}
        isRunning={false}
        isError={false}
        isCancelled={false}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))

    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))
    expectLibraryIntact()
  })

  it('cancel keeps the Library panel and the edit (US-4 AS-4)', async () => {
    seedLibraryOpen()
    setLibraryEditorDirty(true)
    render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com/page' }}
        result={{ title: 'Example' }}
        isRunning={false}
        isError={false}
        isCancelled={false}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(false)
    })
    expect(getDiscardConfirmDialogOpen()).toBe(false)
    expectLibraryIntact()
    expect(isLibraryEditorDirty()).toBe(true)
  })

  it('confirm replaces Library with the Browser panel for the active session (US-4 AS-1)', async () => {
    seedLibraryOpen()
    setLibraryEditorDirty(true)
    render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com/page' }}
        result={{ title: 'Example' }}
        isRunning={false}
        isError={false}
        isCancelled={false}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(true)
    })

    await waitFor(() =>
      expect(browserPanel()).toEqual({
        id: 'browser',
        context: { sessionId: 'sess-1', agentId: 'agent-1' },
      }),
    )
  })
})
