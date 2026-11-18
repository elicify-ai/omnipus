import type { ReactNode } from 'react'
import { act, render } from '@testing-library/react'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { beforeEach, vi } from 'vitest'

beforeEach(({ onTestFinished }) => {
  // Native window scrolling is absent in jsdom; link construction and the router stay real.
  const scroll = vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  onTestFinished(() => scroll.mockRestore())
})

/** Keep the card and Link real; use the repository's load-before-render router pattern. */
export async function renderWithTaskRouter(node: ReactNode) {
  const root = createRootRoute({ component: () => node })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
    defaultPendingMinMs: 0,
  })
  await act(async () => { await router.load() })
  return render(<RouterProvider router={router} />)
}
