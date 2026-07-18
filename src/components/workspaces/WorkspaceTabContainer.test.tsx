// Unit tests for WorkspaceTabContainer layout and session integration.
//
// Verifies:
//   - Container renders the hamburger button in row 1
//   - WorkspaceTabBar tabs are rendered inside row 1
//   - ChatControls renders ONLY when the active tab is 'chat'
//   - Breadcrumb (row 2) is BELOW the top bar (row 1)
//   - enterWorkspaceChat is called with the workspace id on mount
//   - The enterWorkspaceChat effect fires on workspaceId change (workspace switch),
//     NOT on tab switch within the same workspace (Bug 1 regression)
//
// ChatControls is mocked — it's owned by another agent.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'

// ── Mocks ─────────────────────────────────────────────────────────────────────

// TanStack Router — provide location and navigation primitives
let mockPathname = '/workspaces/ws-1/chat'
vi.mock('@tanstack/react-router', () => ({
  Outlet: () => <div data-testid="outlet" />,
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: mockPathname }),
  Link: ({ children, to, params, role, 'aria-selected': ariaSelected, 'data-testid': testId }: {
    children: React.ReactNode
    to: string
    params?: Record<string, string>
    role?: string
    'aria-selected'?: boolean
    'data-testid'?: string
  }) => {
    const href = params ? to.replace('$workspaceId', params.workspaceId) : to
    return <a href={href} role={role} aria-selected={ariaSelected} data-testid={testId}>{children}</a>
  },
}))

// Framer Motion
vi.mock('framer-motion', () => ({
  motion: {
    div: ({ children, ...rest }: React.HTMLAttributes<HTMLDivElement>) => <div {...rest}>{children}</div>,
  },
}))

// TanStack Query — workspace queries return a resolved workspace
let mockWorkspaceId = 'ws-1'
let mockWorkspaceName = 'My Workspace'

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: vi.fn().mockImplementation(({ queryKey }: { queryKey: unknown[] }) => {
      const key = JSON.stringify(queryKey)
      if (key.includes('archived')) {
        return { data: [], isLoading: false, isError: false }
      }
      return {
        data: [{ id: mockWorkspaceId, name: mockWorkspaceName, is_default: true, core_team: [] }],
        isLoading: false,
        isError: false,
      }
    }),
  }
})

// Sidebar store — toggle action
const mockToggle = vi.fn()
vi.mock('@/store/sidebar', () => ({
  useSidebarStore: (selector: ((s: { toggle: () => void; isOpen: boolean; isPinned: boolean }) => unknown) | undefined) => {
    const state = { toggle: mockToggle, isOpen: false, isPinned: false }
    return selector ? selector(state) : state
  },
}))

// Session store — only enterWorkspaceChat matters for the container
const mockEnterWorkspaceChat = vi.fn()
vi.mock('@/store/session', () => ({
  useSessionStore: (selector: ((s: { enterWorkspaceChat: (id: string) => void }) => unknown) | undefined) => {
    const state = { enterWorkspaceChat: mockEnterWorkspaceChat }
    return selector ? selector(state) : state
  },
}))

// WorkspacesStore — the component calls it with NO selector, getting back the full state
const mockSetActiveWorkspaceId = vi.fn()
const mockSetActiveMilestoneId = vi.fn()
vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: (selector?: ((s: {
    activeWorkspaceId: string | null
    setActiveWorkspaceId: (id: string | null) => void
    activeMilestoneId: string | null
    setActiveMilestoneId: (id: string | null) => void
    boardAltitude: string
    setBoardAltitude: (a: string) => void
  }) => unknown)) => {
    const state = {
      activeWorkspaceId: null,
      setActiveWorkspaceId: mockSetActiveWorkspaceId,
      activeMilestoneId: null,
      setActiveMilestoneId: mockSetActiveMilestoneId,
      boardAltitude: 'top-level',
      setBoardAltitude: vi.fn(),
    }
    return selector ? selector(state) : state
  },
}))

// ChatControls — self-contained stub (owned by another agent)
vi.mock('@/components/chat/ChatControls', () => ({
  ChatControls: () => <div data-testid="chat-controls-mock">ChatControls</div>,
}))

// Workspace-setup kickoff — mocked to a no-op. This suite covers
// the container's OWN layout/session-lifecycle concerns, not the kickoff
// mechanics (those have dedicated coverage in
// src/hooks/useWorkspaceSetupKickoff.test.ts and
// src/store/chat.workspace-setup-kickoff.test.ts). Mocking it here (rather
// than letting the real hook import '@/store/chat') avoids dragging chat.ts's
// module-load-time registerChatSetReplaying/registerChatResetForReplay/
// registerSyncChatForeground calls into this file's already-partial
// '@/store/session' mock above, which doesn't export those.
vi.mock('@/hooks/useWorkspaceSetupKickoff', () => ({
  useWorkspaceSetupKickoff: vi.fn(),
}))

// ── Component under test ───────────────────────────────────────────────────────
import { WorkspaceTabContainer } from './WorkspaceTabContainer'
import { useWorkspaceSetupKickoff } from '@/hooks/useWorkspaceSetupKickoff'

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('WorkspaceTabContainer — layout', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockPathname = '/workspaces/ws-1/chat'
    mockWorkspaceId = 'ws-1'
    mockWorkspaceName = 'My Workspace'
  })

  it('renders the hamburger button in the top bar row', async () => {
    await act(async () => {
      render(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    const hamburger = screen.getByTestId('workspace-hamburger')
    expect(hamburger).toBeTruthy()
    // Hamburger is inside the top-bar row
    const topBar = screen.getByTestId('workspace-top-bar')
    expect(topBar.contains(hamburger)).toBe(true)
  })

  it('renders all six workspace tabs inside the top bar row (settings via the name button)', async () => {
    await act(async () => {
      render(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    const topBar = screen.getByTestId('workspace-top-bar')
    const segments = ['chat', 'board', 'list', 'graph', 'calendar', 'team']
    for (const seg of segments) {
      // WorkspaceTabBar renders each segment twice (full strip + sr-only strip).
      const tabs = screen.getAllByTestId(`workspace-tab-${seg}`)
      expect(tabs.length).toBeGreaterThan(0)
      // At least one instance must be inside the top-bar row.
      const inTopBar = tabs.some((tab) => topBar.contains(tab))
      expect(inTopBar).toBe(true)
    }
  })

  it('renders ChatControls in the top bar when the active tab is chat', async () => {
    mockPathname = '/workspaces/ws-1/chat'
    await act(async () => {
      render(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    const controls = screen.getByTestId('chat-controls-mock')
    expect(controls).toBeTruthy()
    const topBar = screen.getByTestId('workspace-top-bar')
    expect(topBar.contains(controls)).toBe(true)
  })

  it('does NOT render ChatControls when the active tab is board', async () => {
    mockPathname = '/workspaces/ws-1/board'
    await act(async () => {
      render(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    expect(screen.queryByTestId('chat-controls-mock')).toBeNull()
  })

  it('does NOT render a secondary breadcrumb row below the top bar', async () => {
    await act(async () => {
      render(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    // The breadcrumb row was removed for flat 44px shell alignment — the tab
    // strip already names the workspace + active view, so the second chrome
    // line was redundant. Guard against reintroduction.
    expect(screen.queryByTestId('workspace-breadcrumb')).toBeNull()
  })

  it('calls sidebar toggle when the hamburger is clicked', async () => {
    await act(async () => {
      render(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    const hamburger = screen.getByTestId('workspace-hamburger')
    fireEvent.click(hamburger)

    expect(mockToggle).toHaveBeenCalledOnce()
  })
})

describe('WorkspaceTabContainer — session lifecycle (Bug 1 regression)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockPathname = '/workspaces/ws-1/chat'
    mockWorkspaceId = 'ws-1'
    mockWorkspaceName = 'My Workspace'
  })

  it('calls enterWorkspaceChat with the workspace id on mount', async () => {
    await act(async () => {
      render(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    expect(mockEnterWorkspaceChat).toHaveBeenCalledWith('ws-1')
  })

  it('calls enterWorkspaceChat again when the workspace id changes (workspace switch)', async () => {
    const { rerender } = render(<WorkspaceTabContainer workspaceId="ws-1" />)
    await act(async () => { /* flush effects */ })

    expect(mockEnterWorkspaceChat).toHaveBeenCalledTimes(1)
    expect(mockEnterWorkspaceChat).toHaveBeenLastCalledWith('ws-1')

    // Simulate navigating to a different workspace
    mockWorkspaceId = 'ws-2'
    mockWorkspaceName = 'Second Workspace'
    await act(async () => {
      rerender(<WorkspaceTabContainer workspaceId="ws-2" />)
    })

    expect(mockEnterWorkspaceChat).toHaveBeenCalledTimes(2)
    expect(mockEnterWorkspaceChat).toHaveBeenLastCalledWith('ws-2')
  })

  it('does NOT call enterWorkspaceChat again when only the tab changes within the same workspace', async () => {
    // BDD: Given user is on ws-1/chat, When they navigate to ws-1/board and back to ws-1/chat,
    // Then enterWorkspaceChat fires only ONCE (on mount with ws-1), not on tab switches.
    // This simulates the bug fix: the container stays mounted and workspaceId doesn't change.
    const { rerender } = render(<WorkspaceTabContainer workspaceId="ws-1" />)
    await act(async () => { /* flush effects */ })

    expect(mockEnterWorkspaceChat).toHaveBeenCalledTimes(1)

    // Tab switch: update pathname to board (same workspace)
    mockPathname = '/workspaces/ws-1/board'
    await act(async () => {
      rerender(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    // workspaceId prop didn't change, so the effect didn't re-fire
    expect(mockEnterWorkspaceChat).toHaveBeenCalledTimes(1)

    // Switch back to chat
    mockPathname = '/workspaces/ws-1/chat'
    await act(async () => {
      rerender(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    // Still only once
    expect(mockEnterWorkspaceChat).toHaveBeenCalledTimes(1)
  })
})

describe('WorkspaceTabContainer — useWorkspaceSetupKickoff wiring', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockPathname = '/workspaces/ws-1/chat'
    mockWorkspaceId = 'ws-1'
    mockWorkspaceName = 'My Workspace'
  })

  it('calls useWorkspaceSetupKickoff with the RESOLVED workspace object once the workspace query has data', async () => {
    await act(async () => {
      render(<WorkspaceTabContainer workspaceId="ws-1" />)
    })

    // The container must hand the hook the actual resolved Workspace object
    // (not the bare workspaceId string, and not undefined) once the
    // workspaces query has data matching the route param — the hook's own
    // guards (setup_pending, status, core_team) all read fields off this
    // object.
    expect(useWorkspaceSetupKickoff).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'ws-1', name: 'My Workspace' }),
    )
  })

  it('re-calls useWorkspaceSetupKickoff with the newly-resolved workspace when the workspace id changes', async () => {
    const { rerender } = render(<WorkspaceTabContainer workspaceId="ws-1" />)
    await act(async () => { /* flush effects */ })

    expect(useWorkspaceSetupKickoff).toHaveBeenLastCalledWith(
      expect.objectContaining({ id: 'ws-1' }),
    )

    mockWorkspaceId = 'ws-2'
    mockWorkspaceName = 'Second Workspace'
    await act(async () => {
      rerender(<WorkspaceTabContainer workspaceId="ws-2" />)
    })

    expect(useWorkspaceSetupKickoff).toHaveBeenLastCalledWith(
      expect.objectContaining({ id: 'ws-2', name: 'Second Workspace' }),
    )
  })
})
