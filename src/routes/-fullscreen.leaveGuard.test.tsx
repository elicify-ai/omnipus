import { cleanup, render, waitFor } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PanelContentProps } from '@/components/panel-shell/types'

const mocks = vi.hoisted(() => ({
  beforeLeave: vi.fn(),
  beforeLeaveRequired: vi.fn(),
  contentProps: null as PanelContentProps | null,
}))

vi.mock('@/components/panel-shell/registry', () => {
  const definition = {
    id: 'library' as const,
    title: 'Library',
    content: (props: PanelContentProps) => {
      mocks.contentProps = props
      return null
    },
    fullScreen: {
      toSearch: () => ({}),
      fromSearch: () => ({ workspaceId: 'workspace-1' }),
    },
    beforeLeave: mocks.beforeLeave,
    beforeLeaveRequired: mocks.beforeLeaveRequired,
  }
  return {
    panels: [definition],
    getPanelDefinition: () => definition,
  }
})

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAppState: vi.fn(async () => ({ onboarding_complete: true, identity: { signed_in: true } })),
}))

vi.mock('@/lib/panelTabPresence', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/panelTabPresence')>()),
  announcePanelTabPresence: vi.fn(() => ({ update: vi.fn(), stop: vi.fn() })),
}))

vi.mock('@/lib/panelPopoutLifecycle', () => ({
  announcePanelPopoutClosed: vi.fn(),
  announcePanelPopoutContext: vi.fn(),
}))

import { routeTree } from '@/routeTree.gen'

async function renderFullScreen() {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/panel/library?popout=popout-1'] }),
  })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  await waitFor(() => expect(mocks.contentProps).not.toBeNull())
}

beforeEach(() => {
  mocks.contentProps = null
  mocks.beforeLeave.mockReset()
  mocks.beforeLeaveRequired.mockReset()
  mocks.beforeLeaveRequired.mockReturnValue(true)
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('full-screen panel leave guard', () => {
  it('closes only after the registered beforeLeave resolves true', async () => {
    mocks.beforeLeave.mockResolvedValueOnce(false).mockResolvedValueOnce(true)
    const close = vi.spyOn(window, 'close').mockImplementation(() => {})
    await renderFullScreen()

    mocks.contentProps?.close()
    await waitFor(() => expect(mocks.beforeLeave).toHaveBeenCalledTimes(1))
    expect(close).not.toHaveBeenCalled()

    mocks.contentProps?.close()
    await waitFor(() => expect(close).toHaveBeenCalledOnce())
  })

  it('arms beforeunload only while beforeLeaveRequired is true', async () => {
    await renderFullScreen()

    const guarded = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(guarded)
    expect(guarded.defaultPrevented).toBe(true)

    mocks.beforeLeaveRequired.mockReturnValue(false)
    const clean = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(clean)
    expect(clean.defaultPrevented).toBe(false)
  })
})
