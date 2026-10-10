// T11 supersedes SP-35's hidden Tags/Updated, narrow probe and reveal toggle.
// Retain the unrelated container-ownership and row-data checks. The new T11
// regression pins all seven columns, one scrolling table and aligned headers.
import { describe, it, expect, vi } from 'vitest'
import { render, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Task } from '@/lib/api'
import { ListView } from './ListView'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: `t-${Math.random().toString(36).slice(2)}`, title: 'A task', status: 'inbox', action: 'llm', priority: 3,
    workspace_id: 'ws-1', surface: 'user', owner: 'admin', created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z', updated_at: '2026-06-20T10:00:00Z', tags: ['alpha'], ...overrides,
  }
}
function makeClient() { return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }) }
const agents = [{ figure: 'Omnipus', role: 'general', id: 'jim', name: 'Jim' }]
function renderList(tasks: Task[]) {
  return render(<QueryClientProvider client={makeClient()}><ListView tasks={tasks} agents={agents} onTaskClick={vi.fn()} /></QueryClientProvider>)
}

describe('ListView narrow — retained container/data contracts', () => {
  it('the list declares itself a CSS container (@container)', () => {
    const mounted = renderList([makeTask({ title: 'Row task' })])
    const declaresContainer = Array.from(mounted.container.querySelectorAll('*')).some((el) => el.classList.contains('@container'))
    expect(declaresContainer).toBe(true)
  })
  it('columns keep their data: a tagged task shows its tag without a reveal control', () => {
    const mounted = renderList([makeTask({ id: 't-1', title: 'Tagged task', tags: ['alpha'] })])
    const row = mounted.getByText('Tagged task').closest('tr') as HTMLElement
    const tagCell = within(row).getByText('alpha').closest('td') as HTMLElement
    expect(tagCell).toBeDefined()
    expect(tagCell.className).not.toContain('hidden')
    expect(mounted.getByText('Tags').closest('th')?.className).not.toContain('hidden')
    expect(mounted.getByText(/^Updated/).closest('th')?.className).not.toContain('hidden')
  })
})
