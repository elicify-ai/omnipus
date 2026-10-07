// Per-writer guard for usePanelDeepLink: while the router's PENDING/LATEST
// location is another route (the chat is still mounted, the committed state
// has not moved), NONE of the three search writers may navigate — each ends in
// cleanedSearch, which would erase the destination's own `?tab=chat`.
//
// Oracle: the hook owns the Chat route's search only (usePanelDeepLink.ts
// header); a navigation builds from router.pendingBuiltLocation ??
// router.latestLocation (router-core buildLocation), so that is the address
// the writers must respect. Positive controls prove each writer DOES write
// while the chat is the addressed route, so a silent hook cannot pass.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, act } from '@testing-library/react'

let mockSearch: Record<string, unknown> = {}
const mockRouter: { latestLocation: { pathname: string }; pendingBuiltLocation?: { pathname: string } } = {
  latestLocation: { pathname: '/workspaces/ws-1/chat' },
}
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => mockRouter,
  useNavigate: () => mockNavigate,
  useRouterState: <T,>({ select }: { select: (s: { location: { search: Record<string, unknown> } }) => T }) =>
    select({ location: { search: mockSearch } }),
  useBlocker: () => undefined,
}))

let mockActivePanel: { id: string; context: Record<string, unknown> } | null = null
vi.mock('@/store/ui', () => {
  const getUiState = () => ({
    activePanel: mockActivePanel,
    openPanel: vi.fn(),
    closePanel: vi.fn(),
  })
  function useUiStore<T>(selector?: (s: ReturnType<typeof getUiState>) => T) {
    const state = getUiState()
    return selector ? selector(state) : state
  }
  useUiStore.getState = getUiState
  return { useUiStore }
})
vi.mock('@/components/panel-shell/leaveGate', () => ({
  leaveGateThen: (_outgoing: unknown, go: () => void) => go(),
}))
vi.mock('@/components/library/preview/unsavedGuard', () => ({
  confirmDiscardLibraryEdits: vi.fn(async () => true),
  isLibraryEditorDirty: () => false,
}))

import { usePanelDeepLink } from './usePanelDeepLink'

function Harness({ panel }: { panel: string | undefined }) {
  usePanelDeepLink('ws-1', panel)
  return null
}

const toSettings = () => {
  mockRouter.latestLocation = { pathname: '/settings' }
  mockRouter.pendingBuiltLocation = undefined
}

beforeEach(() => {
  vi.clearAllMocks()
  mockSearch = {}
  mockActivePanel = null
  mockRouter.latestLocation = { pathname: '/workspaces/ws-1/chat' }
  mockRouter.pendingBuiltLocation = undefined
})

describe('usePanelDeepLink search writers respect the addressed route', () => {
  it('popstate writer: Back/forward onto another route does not rewrite search', async () => {
    mockSearch = { tab: 'chat' }
    render(<Harness panel={undefined} />)
    await act(async () => {})
    toSettings()
    mockNavigate.mockClear()
    await act(async () => { window.dispatchEvent(new PopStateEvent('popstate')) })
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('popstate writer positive control: on the chat route it re-projects', async () => {
    render(<Harness panel={undefined} />)
    await act(async () => {})
    mockNavigate.mockClear()
    await act(async () => { window.dispatchEvent(new PopStateEvent('popstate')) })
    expect(mockNavigate).toHaveBeenCalledTimes(1)
  })

  it('cleanup-effect writer: a foreign key on another route is not stripped', async () => {
    const { rerender } = render(<Harness panel={undefined} />)
    await act(async () => {})
    toSettings()
    mockSearch = { tab: 'chat' }
    await act(async () => { rerender(<Harness panel={undefined} />) })
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('cleanup-effect writer positive control: a foreign key on the chat route is stripped', async () => {
    const { rerender } = render(<Harness panel={undefined} />)
    await act(async () => {})
    mockSearch = { session: 'x' }
    await act(async () => { rerender(<Harness panel={undefined} />) })
    expect(mockNavigate).toHaveBeenCalledTimes(1)
  })

  it('projection writer: a panel-store change while another route is addressed does not write', async () => {
    const { rerender } = render(<Harness panel={undefined} />)
    await act(async () => {})
    toSettings()
    mockActivePanel = { id: 'library', context: { workspaceId: 'ws-1' } }
    await act(async () => { rerender(<Harness panel={undefined} />) })
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('projection writer positive control: on the chat route the store change is projected', async () => {
    const { rerender } = render(<Harness panel={undefined} />)
    await act(async () => {})
    mockActivePanel = { id: 'library', context: { workspaceId: 'ws-1' } }
    await act(async () => { rerender(<Harness panel={undefined} />) })
    expect(mockNavigate).toHaveBeenCalledTimes(1)
  })

  it('a pending navigation target counts as the addressed route (builds from pendingBuiltLocation)', async () => {
    render(<Harness panel={undefined} />)
    await act(async () => {})
    mockRouter.pendingBuiltLocation = { pathname: '/settings' }
    mockNavigate.mockClear()
    await act(async () => { window.dispatchEvent(new PopStateEvent('popstate')) })
    expect(mockNavigate).not.toHaveBeenCalled()
  })
})
