// RED — CRITICAL: switching workspace tabs with an unsaved Mail draft must
// not silently discard it.
//
// Spec source: side-panel-shell-spec.md's CRIT-001 leave gate, as actually
// wired for Mail — `mailPanelDefinition.beforeLeave = confirmDiscardMailEdits`
// / `beforeLeaveRequired = isMailEditorDirty` (mailPanelDefinition.tsx:158-159)
// — the SAME shared guard Escape and the header close button already route
// through. `PANEL_POLICIES.mail.switchFollows === 'always'` (types.ts:26)
// means every workspace-tab switch while Mail is open is a leave the panel
// must be asked about, exactly like those other paths.
//
// WorkspaceTabContainer's tab-switch effect (~line 86-135) calls
// `resolveWorkspaceSwitch` with `beforeLeave` HARDCODED to fire only for
// `activePanel.id === 'library'`:
//
//   beforeLeave:
//     activePanel.id === 'library' && openedWorkspaceId
//       ? confirmDiscardLibraryEdits
//       : undefined
//
// For Mail this is always `undefined`, so `resolveWorkspaceSwitch` (whose own
// contract is `if (input.beforeLeave && !(await input.beforeLeave())) return
// {action:'cancel'}`) skips straight to `{action:'follow'}` — the discard
// dialog (mailUnsavedGuard.ts's confirmDiscardMailEdits) is NEVER invoked, and
// the panel is re-opened against the new workspace immediately. The unsaved
// draft is gone with no prompt.
//
// Oracle: with a dirty Mail edit source registered (setMailEditorDirty), the
// SAME confirmDiscardMailEdits() guard other close paths trigger must open
// its dialog (getMailDiscardConfirmDialogOpen() === true) and the switch must
// not re-open the panel against the new workspace until answered. This is
// derived from mailUnsavedGuard.ts's own documented contract ("Every caller
// MUST await this") and mailPanelDefinition.tsx's beforeLeave wiring — never
// from what WorkspaceTabContainer.tsx currently does.

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import {
  setMailEditorDirty,
  isMailEditorDirty,
  getMailDiscardConfirmDialogOpen,
  resolveMailDiscardConfirmDialog,
} from '@/components/workspaces/mail/mailUnsavedGuard'

// ── Mocks (mirrors WorkspaceTabContainer.test.tsx's baseline setup) ────────

let mockPathname = '/workspaces/ws-1/chat'
vi.mock('@tanstack/react-router', () => ({
  Outlet: () => <div data-testid="outlet" />,
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: mockPathname }),
  Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
}))

vi.mock('framer-motion', () => ({
  motion: {
    div: ({ children, ...rest }: React.HTMLAttributes<HTMLDivElement>) => <div {...rest}>{children}</div>,
  },
}))

let mockWorkspaceId = 'ws-1'
let mockWorkspaceName = 'My Workspace'
vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: vi.fn().mockImplementation(({ queryKey }: { queryKey: unknown[] }) => {
      const key = JSON.stringify(queryKey)
      if (key.includes('app-state')) {
        return { data: { onboarding_complete: true, dev_mode_bypass: false }, isLoading: false, isError: false }
      }
      if (key.includes('archived')) {
        return { data: [], isLoading: false, isError: false }
      }
      return {
        data: [{ id: mockWorkspaceId, name: mockWorkspaceName, is_default: true, core_team: [] }],
        isLoading: false,
        isError: false,
        refetch: vi.fn(),
      }
    }),
  }
})

vi.mock('@/store/sidebar', () => ({
  useSidebarStore: (selector: ((s: { toggle: () => void; isOpen: boolean; isPinned: boolean }) => unknown) | undefined) => {
    const state = { toggle: vi.fn(), isOpen: false, isPinned: false }
    return selector ? selector(state) : state
  },
  SIDEBAR_PIN_BREAKPOINT: 1024,
}))

vi.mock('@/store/session', () => ({
  useSessionStore: (selector: ((s: { enterWorkspaceChat: (id: string) => void }) => unknown) | undefined) => {
    const state = { enterWorkspaceChat: vi.fn() }
    return selector ? selector(state) : state
  },
}))

const mockSetActiveWorkspaceId = vi.fn()
const mockSetActivePlanId = vi.fn()
vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: (selector?: ((s: {
    activeWorkspaceId: string | null
    setActiveWorkspaceId: (id: string | null) => void
    activePlanId: string | null
    setActivePlanId: (id: string | null) => void
    boardAltitude: string
    setBoardAltitude: (a: string) => void
  }) => unknown)) => {
    const state = {
      activeWorkspaceId: null,
      setActiveWorkspaceId: mockSetActiveWorkspaceId,
      activePlanId: null,
      setActivePlanId: mockSetActivePlanId,
      boardAltitude: 'top-level',
      setBoardAltitude: vi.fn(),
    }
    return selector ? selector(state) : state
  },
}))

vi.mock('@/components/chat/ChatControls', () => ({
  ChatControls: () => <div data-testid="chat-controls-mock">ChatControls</div>,
}))

vi.mock('@/hooks/useWorkspaceSetupKickoff', () => ({
  useWorkspaceSetupKickoff: vi.fn(),
}))

// The one mock specific to this file: the shell's active-panel slice.
// WorkspaceTabContainer reads this ONLY via `useUiStore.getState()` (never as
// a hook) — see grep evidence: lines 99, 126, 130 of the component file.
let mockActivePanel: { id: string; context: Record<string, unknown> } | null = null
const mockOpenPanel = vi.fn()
vi.mock('@/store/ui', () => {
  const getUiState = () => ({
    activePanel: mockActivePanel,
    openPanel: mockOpenPanel,
  })
  // Mimics zustand's dual call shape: usable as a hook (WorkspaceTabBar calls
  // `useUiStore((s) => s.activePanel?.id ?? null)`) AND via the static
  // `.getState()` WorkspaceTabContainer itself uses (grep evidence: lines
  // 99, 126, 130 of the component file never call it as a hook).
  function useUiStore<T>(selector?: (s: ReturnType<typeof getUiState>) => T) {
    const state = getUiState()
    return selector ? selector(state) : state
  }
  useUiStore.getState = getUiState
  return { useUiStore }
})

// ── Component under test ────────────────────────────────────────────────────
import { WorkspaceTabContainer } from './WorkspaceTabContainer'

describe('WorkspaceTabContainer — Mail unsaved-draft discard guard on tab switch', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockPathname = '/workspaces/ws-1/chat'
    mockWorkspaceId = 'ws-1'
    mockWorkspaceName = 'My Workspace'
    mockActivePanel = {
      id: 'mail',
      context: { workspaceId: 'ws-1', mailboxId: null, folder: 'drafts', messageRef: null },
    }
    // Resolve any dialog left open by a previous test and clear dirtiness —
    // mailUnsavedGuard.ts is a module-level singleton store shared across the
    // whole vitest worker.
    if (getMailDiscardConfirmDialogOpen()) resolveMailDiscardConfirmDialog(false)
    setMailEditorDirty('draft', false)
    setMailEditorDirty('compose', false)
  })

  afterEach(() => {
    if (getMailDiscardConfirmDialogOpen()) resolveMailDiscardConfirmDialog(false)
    setMailEditorDirty('draft', false)
    setMailEditorDirty('compose', false)
  })

  it('opens the SAME discard-confirmation dialog Escape/header-close use, instead of silently switching', async () => {
    // Given: Mail is open for ws-1 with an unsaved draft edit in progress.
    setMailEditorDirty('draft', true)
    expect(isMailEditorDirty()).toBe(true)
    expect(getMailDiscardConfirmDialogOpen()).toBe(false)

    const { rerender } = render(<WorkspaceTabContainer workspaceId="ws-1" />)
    await act(async () => { /* flush mount effects */ })

    // When: the user clicks a different workspace tab (workspaceId prop
    // changes — this is exactly what the route does on a tab click).
    mockWorkspaceId = 'ws-2'
    mockWorkspaceName = 'Second Workspace'
    await act(async () => {
      rerender(<WorkspaceTabContainer workspaceId="ws-2" />)
    })
    // Flush the resolveWorkspaceSwitch promise chain's microtask.
    await act(async () => { await Promise.resolve() })

    // Then: the same guard other Mail close paths trigger must have fired —
    // its dialog is open, blocking the switch pending an answer. Silently
    // discarding the edit (dialog never opens, panel re-opens against ws-2
    // right away) is the CRITICAL data-loss bug this test pins.
    expect(getMailDiscardConfirmDialogOpen()).toBe(true)

    // And the panel must NOT have already been re-opened against the new
    // workspace before the user answered — that would mean the switch
    // proceeded regardless of what the dialog eventually decides.
    expect(mockOpenPanel).not.toHaveBeenCalledWith(
      'mail',
      expect.objectContaining({ workspaceId: 'ws-2' }),
    )

    // Clean up the now-open dialog so it doesn't leak into afterEach's own
    // resolve (harmless either way, but keeps intent explicit here).
    resolveMailDiscardConfirmDialog(false)
  })
})
