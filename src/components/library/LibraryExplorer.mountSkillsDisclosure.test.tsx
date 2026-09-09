// LibraryExplorer.mountSkillsDisclosure.test.tsx — ADR-072 D1.2/FR-074/
// FR-074a coverage: the mount-creation-time disclosure of what a mount's
// recognised skills directory grants (always, even for a handful of
// skills), and the separate threshold warning (only past the configured
// count, spec default 500). Sibling test file convention mirrors
// SkillsScreenToolsTab.test.tsx / SkillCard.test.tsx — a focused test file
// exercising one feature area of an existing screen component rather than a
// standalone component (LibraryExplorer owns the mount-creation mutation;
// there is no separate "AddMount" component to import).
//
// createWorkspaceMount is mocked to resolve with the real, committed
// WorkspaceMountCreateResponse shape — including its
// `skills_count`/`skills_grants_message`/`skills_threshold_warning` fields
// (contracts/components/schemas/WorkspaceMountCreateResponse.yaml) — so this
// exercises `mountSkillsDisclosure()`'s real read path end to end, not a
// re-implementation of it.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryWorkspaceNode, WorkspaceMountCreateResponse } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryWorkspaces: vi.fn(),
    fetchLibraryEntries: vi.fn(),
    createWorkspaceMount: vi.fn(),
  }
})

import { fetchLibraryWorkspaces, fetchLibraryEntries, createWorkspaceMount } from '@/lib/api'
import { LibraryExplorer } from './LibraryExplorer'

const mockedFetchWorkspaces = vi.mocked(fetchLibraryWorkspaces)
const mockedFetchEntries = vi.mocked(fetchLibraryEntries)
const mockedCreateMount = vi.mocked(createWorkspaceMount)

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderExplorer(initialWorkspaceId = 'ws-1') {
  return render(
    <QueryClientProvider client={makeClient()}>
      <LibraryExplorer initialWorkspaceId={initialWorkspaceId} onClose={vi.fn()} onPopOut={vi.fn()} />
    </QueryClientProvider>,
  )
}

function makeWorkspaceNode(over: Partial<LibraryWorkspaceNode> = {}): LibraryWorkspaceNode {
  return { id: 'ws-1', name: 'Client Repo', entry_count: 0, ...over }
}

/**
 * A successful mount-creation response, as `createWorkspaceMount()` itself
 * returns it — i.e. the real, schema-validated `WorkspaceMountCreateResponse`
 * wire shape (contracts/components/schemas/WorkspaceMountCreateResponse.yaml),
 * carrying `skills_count`/`skills_grants_message`/`skills_threshold_warning`
 * directly as top-level fields now that ADR-072 D1.2's disclosure is wired
 * into `pkg/gateway/rest_workspace_mounts.go::mountToCreateResponse`. This
 * test mocks `createWorkspaceMount` itself (LibraryExplorer's own call
 * site), so it must match the real generated response shape —
 * `mountSkillsDisclosure()`'s own camelCase transform of these fields is
 * covered separately in api.workspaces.test.ts.
 */
function mountResponseWithDisclosure(
  over: { count?: number; grantsMessage?: string; thresholdWarning?: string } = {},
): WorkspaceMountCreateResponse {
  const disclosure =
    over.count !== undefined
      ? {
          skills_count: over.count,
          skills_grants_message: over.grantsMessage ?? '',
          ...(over.thresholdWarning !== undefined ? { skills_threshold_warning: over.thresholdWarning } : {}),
        }
      : {}
  return {
    name: 'client-repo',
    host_path: '/Users/operator/code/client-repo',
    status: 'ok',
    ...disclosure,
  }
}

async function openAddMountDialogAndConfirm(hostPath: string) {
  fireEvent.click(await screen.findByTestId('library-add-mount-button'))
  fireEvent.change(await screen.findByTestId('library-add-mount-path'), { target: { value: hostPath } })
  fireEvent.click(await screen.findByTestId('library-add-mount-confirm'))
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
  mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode()])
  mockedFetchEntries.mockResolvedValue([])
})

describe('LibraryExplorer — mount skills disclosure (ADR-072 D1.2, FR-074/074a)', () => {
  it('discloses what a mount with only a few skills grants, even far below the warning threshold', async () => {
    mockedCreateMount.mockResolvedValue(
      mountResponseWithDisclosure({
        count: 3,
        grantsMessage:
          "This mount's skills directory grants 3 skills to every agent working in this workspace as auto-loadable agent instructions.",
      }),
    )

    renderExplorer()
    await openAddMountDialogAndConfirm('/Users/operator/code/client-repo')

    const dialog = await screen.findByTestId('library-mount-skills-disclosure-dialog')
    expect(dialog).toHaveTextContent('grants 3')
    expect(dialog).toHaveTextContent('auto-loadable agent instructions')
    // No threshold warning for a 3-skill mount.
    expect(screen.queryByTestId('library-mount-skills-threshold-warning')).not.toBeInTheDocument()
  })

  it('warns at mount-creation time when the mount would contribute an implausible number of skills, but still creates it', async () => {
    mockedCreateMount.mockResolvedValue(
      mountResponseWithDisclosure({
        count: 5000,
        grantsMessage:
          "This mount's skills directory grants 5000 skills to every agent working in this workspace as auto-loadable agent instructions.",
        thresholdWarning:
          'This mount would contribute 5000 skills — well beyond a plausible hand-authored collection (threshold: 500).',
      }),
    )

    renderExplorer()
    await openAddMountDialogAndConfirm('/Users/operator/code/monorepo')

    const dialog = await screen.findByTestId('library-mount-skills-disclosure-dialog')
    expect(dialog).toHaveTextContent('grants 5000')
    const warning = screen.getByTestId('library-mount-skills-threshold-warning')
    expect(warning).toHaveTextContent('well beyond a plausible hand-authored collection')

    // FR-075: the mount is still created — createWorkspaceMount was called
    // and the mutation succeeded (the dialog only opens on success).
    expect(mockedCreateMount).toHaveBeenCalledTimes(1)
  })

  it('does not open the disclosure dialog for a mount with no recognised skills directory', async () => {
    mockedCreateMount.mockResolvedValue(mountResponseWithDisclosure())

    renderExplorer()
    await openAddMountDialogAndConfirm('/Users/operator/code/no-skills-here')

    // The ordinary success toast still fires…
    await waitFor(() => {
      expect(useUiStore.getState().toasts.some((t) => t.message.includes('Mounted'))).toBe(true)
    })
    // …but no skills disclosure dialog, since the mount carries none.
    expect(screen.queryByTestId('library-mount-skills-disclosure-dialog')).not.toBeInTheDocument()
  })
})
