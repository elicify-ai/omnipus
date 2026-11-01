import type { ComponentType } from 'react'
import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const routeBoundary = vi.hoisted(() => ({ navigate: vi.fn(), paths: [] as string[] }))
// Only router history is replaced. The actual /automations route component,
// DefaultWorkspaceRedirect, workspace query, typed client and schema run.
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => routeBoundary.navigate,
  createFileRoute: (path: string) => (options: object) => { routeBoundary.paths.push(path); return { options } },
}))
import { Route } from '@/routes/_app/automations'

const workspace = (id: string, isDefault = false) => ({ revision: '0'.repeat(64), id, name: id, status: 'active', pinned: false, pin_order: 0, task_count: 0, is_default: isDefault, created_at: '2026-11-01T00:00:00Z', updated_at: '2026-11-01T00:00:00Z' })
beforeEach(() => { routeBoundary.navigate.mockReset(); window.location.hash = '#/automations' })
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.location.hash = '' })

describe('T10 Automations retirement preserves the SPA redirect (S-G.6, D-5.9)', () => {
  it.each([true, false])('actual old route replaces history with the workspace Calendar (default=%s)', async hasDefault => {
    const fetch = vi.fn(async () => new Response(JSON.stringify([workspace('first'), workspace('preferred', hasDefault)]), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetch)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const Component = Route.options.component as ComponentType
    render(<QueryClientProvider client={client}><Component /></QueryClientProvider>)
    await waitFor(() => expect(routeBoundary.navigate).toHaveBeenCalledWith({ to: '/workspaces/$workspaceId/calendar', params: { workspaceId: hasDefault ? 'preferred' : 'first' }, replace: true }))
    expect(routeBoundary.paths).toContain('/_app/automations')
    expect(fetch.mock.calls.length).toBeGreaterThan(0)
    expect(screen.queryByText(/404|not found|automations/i)).not.toBeInTheDocument()
  })
  it('sidebar has no retired link and the standalone screens stay deleted', () => {
    const path = (relative: string) => fileURLToPath(new URL(relative, import.meta.url))
    const sidebar = readFileSync(path('../layout/Sidebar.tsx'), 'utf8')
    expect(sidebar).toContain('to="/settings"') // Positive instrument: real navigation source was read.
    expect(sidebar).toContain('/workspaces/$workspaceId/chat')
    expect(sidebar).not.toMatch(/["'`]\/automations(?:["'`/?])/)
    expect(existsSync(path('../screens/CalendarScreen.tsx'))).toBe(true)
    for (const retired of ['AutomationsScreen.tsx', 'SchedulesScreen.tsx']) expect(existsSync(path(`../screens/${retired}`))).toBe(false)
    const route = readFileSync(path('../../routes/_app/automations.tsx'), 'utf8')
    expect(route).toContain('DefaultWorkspaceRedirect tab="calendar"')
    expect(route).not.toMatch(/AutomationsScreen|SchedulesScreen/)
  })
})
