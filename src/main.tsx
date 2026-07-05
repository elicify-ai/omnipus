import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createRouter, RouterProvider, createHashHistory } from '@tanstack/react-router'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from './lib/queryClient'
import { routeTree } from './routeTree.gen'
import { RouteFallback } from './components/shared/RouteFallback'
import { RouteErrorFallback } from './components/shared/RouteErrorFallback'
import './styles/globals.css'

// Hash routing — required for go:embed static file serving.
// go:embed serves a single index.html; history mode would 404 on deep links.
const hashHistory = createHashHistory()

const router = createRouter({
  routeTree,
  history: hashHistory,
  defaultPreload: 'intent',
  scrollRestoration: true,
  // With autoCodeSplitting (vite.config.ts) every route component is a lazy
  // chunk, so route loading now happens at the ROUTER level. Show the shared
  // skeleton during a cold navigation instead of a blank frame. defaultPreload
  // 'intent' preloads on hover so most clicks are instant (no pending shown);
  // the 150 ms delay avoids flashing the skeleton on those fast/preloaded loads
  // while still covering genuinely on-demand fetches. Replaces the per-route
  // <Suspense fallback={RouteFallback}> that the (now-converted) hand-lazy
  // routes used to carry.
  defaultPendingComponent: RouteFallback,
  defaultPendingMs: 150,
  // Symmetric with defaultPendingComponent above: a route chunk fetch can
  // fail (e.g. a stale tab referencing a chunk hash an old deployment no
  // longer serves), not just take time. Without this, TanStack Router's
  // built-in ErrorComponent renders a bare "Something went wrong!" with no
  // reload affordance and no diagnostic signal.
  defaultErrorComponent: RouteErrorFallback,
  defaultOnCatch: (error) => {
    console.error('[router] route load/render failed:', error)
  },
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

const rootElement = document.getElementById('root')
if (!rootElement) {
  throw new Error('Root element #root not found in DOM. Ensure index.html contains <div id="root"></div>.')
}
createRoot(rootElement).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>
)
