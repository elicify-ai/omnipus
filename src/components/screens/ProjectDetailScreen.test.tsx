/**
 * ProjectDetailScreen.test.tsx
 *
 * Tests for the inbox-redirect useEffect inside ProjectDetailScreen.
 *
 * The component has a useEffect that redirects workspaceId === 'inbox' to the
 * real default workspace UUID:
 *
 *   useEffect(() => {
 *     if (workspaceId !== 'inbox') return
 *     if (projects.length === 0) return
 *     const defaultProject = projects.find((p) => p.is_default)
 *     if (defaultProject) {
 *       void navigate({ to: '/workspaces/$workspaceId', params: { workspaceId: defaultProject.id }, replace: true })
 *     }
 *   }, [workspaceId, projects, navigate])
 *
 * Scenarios tested:
 *   1. Redirect to default workspace — workspaceId='inbox', data has is_default=true workspace → navigate called
 *   2. No redirect when workspaces loading — workspaceId='inbox', empty data → navigate NOT called
 *   3. No redirect when no default workspace — workspaceId='inbox', list without is_default → navigate NOT called
 *
 * Traces to: ProjectDetailScreen.tsx lines 49–57 — inbox redirect useEffect.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Mocks declared before any SPA imports ─────────────────────────────────────

// navigate spy — captured by useNavigate() mock
const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

// Mock the full API layer — only fetchWorkspaces matters for redirect tests;
// all other fetches return never-resolving promises or empty arrays so they
// don't interfere with the assertions.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaces: vi.fn(),
    fetchBoardTasks: vi.fn(),
    fetchMilestones: vi.fn(),
    fetchAgents: vi.fn(),
    workspacesQueryKeys: actual.workspacesQueryKeys,
    boardTasksQueryKeys: actual.boardTasksQueryKeys,
    milestonesQueryKeys: actual.milestonesQueryKeys,
  }
})

// Mock projectsStore — the useEffect resets activeMilestoneId on project change;
// we stub it so we don't need a real Zustand store.
vi.mock('@/store/projectsStore', () => ({
  useProjectsStore: vi.fn(() => ({
    activeMilestoneId: null,
    setActiveMilestoneId: vi.fn(),
  })),
}))

// Stub heavy sub-components to keep this test fast and isolated.
vi.mock('@/components/projects/ProjectHeader', () => ({
  ProjectHeader: () => null,
}))

vi.mock('@/components/projects/MilestoneFilterPills', () => ({
  MilestoneFilterPills: () => null,
  MILESTONE_FILTER_UNSCHEDULED: '__unscheduled__',
}))

vi.mock('@/components/projects/BoardView', () => ({
  BoardView: () => null,
}))

vi.mock('@/components/projects/ListView', () => ({
  ListView: () => null,
}))

vi.mock('@/components/projects/ExecutionView', () => ({
  ExecutionView: () => null,
}))

vi.mock('@/components/projects/TaskDetailSlideOver', () => ({
  TaskDetailSlideOver: () => null,
}))

vi.mock('@/components/projects/CreateTaskSlideOver', () => ({
  CreateTaskSlideOver: () => null,
}))

vi.mock('@/components/projects/CreateMilestoneSlideOver', () => ({
  CreateMilestoneSlideOver: () => null,
}))

// Phosphor icons
vi.mock('@phosphor-icons/react', () => ({
  List: () => null,
  SquaresFour: () => null,
  Lightning: () => null,
  Plus: () => null,
}))

// ── Import mocked API functions ────────────────────────────────────────────────

import { fetchWorkspaces, fetchBoardTasks, fetchMilestones, fetchAgents } from '@/lib/api'
import type { Workspace } from '@/lib/api'

import { ProjectDetailScreen } from '@/components/screens/ProjectDetailScreen'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderScreen(workspaceId: string) {
  const client = makeQueryClient()
  return render(
    <QueryClientProvider client={client}>
      <ProjectDetailScreen workspaceId={workspaceId} />
    </QueryClientProvider>,
  )
}

/**
 * Build a minimal Workspace fixture from the generated OpenAPI type.
 */
function makeProject(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: 'proj-default-uuid',
    name: 'Inbox',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    is_default: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } as Workspace
}

// ── beforeEach: reset mocks ───────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()

  // Default: board tasks, milestones, agents return never-resolving promises so
  // they don't cause state changes that interfere with redirect assertions.
  vi.mocked(fetchBoardTasks).mockReturnValue(new Promise(() => {}) as never)
  vi.mocked(fetchMilestones).mockReturnValue(new Promise(() => {}) as never)
  vi.mocked(fetchAgents).mockReturnValue(new Promise(() => {}) as never)
})

// ── describe: redirect to default project ─────────────────────────────────────
// Traces to: ProjectDetailScreen.tsx lines 49–57 — inbox redirect useEffect.

describe('ProjectDetailScreen — inbox redirect', () => {
  it('navigates to default workspace UUID when workspaceId is "inbox" and is_default workspace exists', async () => {
    // BDD: Given workspaceId='inbox' and fetchWorkspaces returns a workspace with is_default=true
    // When the component mounts and the workspaces query resolves
    // Then navigate is called with the default workspace's id and replace: true
    //
    // Traces to: ProjectDetailScreen.tsx line 54

    const defaultProject = makeProject({ id: 'abc-123', name: 'Inbox', is_default: true })
    vi.mocked(fetchWorkspaces).mockResolvedValue([defaultProject])

    renderScreen('inbox')

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId',
        params: { workspaceId: 'abc-123' },
        replace: true,
      })
    })
  })

  it('navigates to the correct UUID (not a hardcoded value) when default workspace has different id', async () => {
    // Differentiation test: two different default workspace IDs → two different
    // navigate calls.  A hardcoded response would return the same id regardless
    // of what fetchWorkspaces returns.
    //
    // Traces to: ProjectDetailScreen.tsx line 54

    const defaultProject = makeProject({ id: 'xyz-999', name: 'Inbox', is_default: true })
    vi.mocked(fetchWorkspaces).mockResolvedValue([defaultProject])

    renderScreen('inbox')

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith(
        expect.objectContaining({ params: { workspaceId: 'xyz-999' } }),
      )
    })

    // Must NOT have been called with the id from the previous test scenario.
    expect(mockNavigate).not.toHaveBeenCalledWith(
      expect.objectContaining({ params: { workspaceId: 'abc-123' } }),
    )
  })

  it('does NOT call navigate when workspaceId is NOT "inbox"', async () => {
    // Content test: the guard `if (workspaceId !== 'inbox') return` means navigate
    // must never be called for a real UUID.
    // Traces to: ProjectDetailScreen.tsx line 50

    const realProject = makeProject({ id: 'real-uuid-001', name: 'My Workspace', is_default: false })
    vi.mocked(fetchWorkspaces).mockResolvedValue([realProject])

    renderScreen('real-uuid-001')

    // Allow effects to settle.
    await waitFor(() => {
      expect(fetchWorkspaces).toHaveBeenCalled()
    })

    expect(mockNavigate).not.toHaveBeenCalled()
  })
})

// ── describe: no redirect while projects loading ───────────────────────────────
// Traces to: ProjectDetailScreen.tsx line 51 — `if (projects.length === 0) return`

describe('ProjectDetailScreen — no redirect while projects still loading', () => {
  it('does NOT call navigate when workspaces query has not resolved yet (empty default)', async () => {
    // BDD: Given workspaceId='inbox' and fetchWorkspaces is pending (data defaults to [])
    // When the component mounts
    // Then navigate is NOT called because projects.length === 0
    //
    // The guard `if (projects.length === 0) return` prevents a premature
    // redirect before data is available.
    //
    // Traces to: ProjectDetailScreen.tsx line 51

    // Never-resolving promise → query stays in loading state → data = [] (default)
    vi.mocked(fetchWorkspaces).mockReturnValue(new Promise(() => {}) as never)

    renderScreen('inbox')

    // Give React time to flush all effects — navigate must remain uncalled.
    await new Promise((r) => setTimeout(r, 50))

    expect(mockNavigate).not.toHaveBeenCalled()
  })
})

// ── describe: no redirect when no default project ─────────────────────────────
// Traces to: ProjectDetailScreen.tsx lines 52–55 — defaultProject guard

describe('ProjectDetailScreen — no redirect when no default workspace in list', () => {
  it('does NOT call navigate when workspaces list has entries but none is is_default', async () => {
    // BDD: Given workspaceId='inbox' and fetchWorkspaces returns workspaces without is_default=true
    // When the component mounts
    // Then navigate is NOT called (falls through to "not found" state)
    //
    // Traces to: ProjectDetailScreen.tsx lines 52–55 — `if (defaultProject)` guard.
    //
    // Rejection test: a workspace list without is_default set must not trigger a
    // redirect to a wrong ID.

    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeProject({ id: 'proj-a', name: 'Alpha', is_default: false }),
      makeProject({ id: 'proj-b', name: 'Beta', is_default: false }),
    ])

    renderScreen('inbox')

    // Allow query to resolve and effects to flush.
    await waitFor(() => {
      expect(fetchWorkspaces).toHaveBeenCalled()
    })

    // Give a small window for any erroneous navigate call to appear.
    await new Promise((r) => setTimeout(r, 50))

    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('does NOT call navigate when is_default is explicitly false on all workspaces', async () => {
    // Persistence test: even with multiple workspaces all having is_default=false,
    // navigate stays silent.
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeProject({ id: 'proj-x', name: 'X', is_default: false }),
    ])

    renderScreen('inbox')

    await waitFor(() => {
      expect(fetchWorkspaces).toHaveBeenCalled()
    })
    await new Promise((r) => setTimeout(r, 50))

    expect(mockNavigate).not.toHaveBeenCalled()
  })
})
