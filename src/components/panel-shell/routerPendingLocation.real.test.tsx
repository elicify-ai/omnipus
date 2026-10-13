// Canary against a REAL router instance. usePanelDeepLink reads the router's
// private `_pendingLocation` (see usePanelDeepLink.leaveGuard.test.tsx, which
// can only MOCK that field by name). If a future router-core renames it, the
// mocked test stays green while isOwningChat silently degrades; this test is
// the one that must fail loudly then.
//
// Oracle: router-core's buildAndCommitLocation assigns `_pendingLocation` to
// the location being navigated to before the commit starts, and clears it in a
// microtask afterwards (nested router-core 1.171.34, router.js).
import { act, cleanup, render, waitFor } from '@testing-library/react'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { afterEach, describe, expect, it } from 'vitest'

afterEach(cleanup)

describe('real TanStack router exposes _pendingLocation during a pending navigation', () => {
  it('is the destination while navigate() is pending, and cleared once it settles', async () => {
    const root = createRootRoute({ component: Outlet })
    const chat = createRoute({ getParentRoute: () => root, path: '/workspaces/$workspaceId/chat', component: () => <div>chat</div> })
    const settings = createRoute({ getParentRoute: () => root, path: '/settings', component: () => <div>settings</div> })
    const router = createRouter({
      routeTree: root.addChildren([chat, settings]),
      history: createMemoryHistory({ initialEntries: ['/workspaces/ws-1/chat'] }),
    })
    render(<RouterProvider router={router} />)
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat'))

    // Private field: reached through a structural cast on purpose, because it
    // is not part of the public router type.
    const pending = () => (router as unknown as { _pendingLocation?: { pathname: string } })._pendingLocation
    expect(pending(), 'no navigation in flight: field must be unset').toBeUndefined()

    let navigation!: Promise<void>
    act(() => { navigation = router.navigate({ to: '/settings' }) })
    // Read synchronously: the field is only set between the call and the
    // microtask that clears it.
    expect(pending()?.pathname, '_pendingLocation missing: router-core renamed or removed it').toBe('/settings')

    await act(async () => { await navigation })
    expect(router.state.location.pathname).toBe('/settings')
    expect(pending()).toBeUndefined()
  })
})
