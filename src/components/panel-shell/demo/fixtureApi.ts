// fixtureApi.ts — the demo's dev-only fetch interceptor (SP-31): serves the
// Library GET endpoints from fixtures.ts so the REAL LibraryExplorer runs
// against fixture data with no gateway. Installed once per document, only
// from the demo stories — never from app code — and it leaves every
// non-`/api/v1/` request (Storybook assets, fonts) untouched. Unknown API
// routes and all state-changing methods answer a visible 404/405: a demo
// save fails visibly instead of pretending to succeed.

import {
  DEMO_LIBRARY_ENTRIES,
  DEMO_WORKSPACES,
  fixtureContent,
  fixtureKnowledgeInfo,
} from './fixtures'

let installed = false

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function serveApi(url: URL, method: string): Response {
  const path = url.pathname
  if (method !== 'GET') {
    return jsonResponse({ error: 'fixture API serves GETs only in the wave-0 demo' }, 405)
  }
  if (path === '/api/v1/library/workspaces') {
    return jsonResponse(DEMO_WORKSPACES)
  }
  const m = path.match(/^\/api\/v1\/library\/([^/]+)(\/.*)?$/)
  if (m === null) {
    return jsonResponse({ error: `no fixture for ${path}` }, 404)
  }
  const workspaceId = decodeURIComponent(m[1] as string)
  const rest = m[2] ?? ''
  const qs = url.searchParams
  if (rest === '/entries') {
    const prefix = qs.get('path') ?? ''
    const all = DEMO_LIBRARY_ENTRIES[workspaceId] ?? []
    const level = all.filter((e) =>
      prefix === '' ? !e.path.includes('/') : e.path.startsWith(`${prefix}/`) && !e.path.slice(prefix.length + 1).includes('/'),
    )
    return jsonResponse(level)
  }
  if (rest === '/content') {
    return jsonResponse(fixtureContent(workspaceId, qs.get('path') ?? ''))
  }
  if (rest === '/knowledge') {
    return jsonResponse(fixtureKnowledgeInfo(workspaceId, qs.get('path') ?? ''))
  }
  return jsonResponse({ error: `no fixture for ${path}` }, 404)
}

/**
 * Install the interceptor (idempotent). Returns a no-op disposer — stories
 * never uninstall; the module only ever wraps `window.fetch` once.
 */
export function installFixtureApi(): () => void {
  if (installed) return () => undefined
  installed = true
  const realFetch = window.fetch.bind(window)
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url =
      typeof input === 'string'
        ? input
        : input instanceof URL
          ? input.toString()
          : input.url
    const parsed = new URL(url, window.location.origin)
    if (parsed.pathname.startsWith('/api/v1/')) {
      return serveApi(parsed, (init?.method ?? 'GET').toUpperCase())
    }
    return realFetch(input, init)
  }
  return () => undefined
}
