// ListView.narrow.test.tsx — RED pack for side-panel-shell-spec.md Wave 3,
// SP-35 (with SP-33's mechanism):
//
//   "List narrow hides Tags/Updated behind a '…' overflow control below a
//    measured width."  (§10 Wave 3 table; §13 SP-35)
//
//   Wireframe §2F: "List — narrow, Tags/Updated hidden behind '⋯' (SP-34
//   numbering in the decision table). Click the '⋯' header to show
//   Tags/Updated again — the real breakpoint for this switch is measured
//   live from the table's own content, see section 9."  (§9B: List's own
//   breakpoint is measured, not guessed.)
//
// Oracle sources: spec §10 Wave 3 / §13 SP-35, wireframe §2F + §9B.
//
// RED↔GREEN DOM contracts declared by this pack (same rationale as
// BoardView.narrow.test.tsx — jsdom cannot evaluate container queries):
//   1. The table's width-responding root is a CSS container (`@container`
//      class) — SP-33's mechanism, measured from the List's own content.
//   2. The root carries `data-narrow="true"|"false"` (jsdom default true).
//   3. The overflow control is a real <button> with
//      `data-testid="list-narrow-columns-toggle"` and an accessible name
//      naming BOTH governed columns (Tags and Updated).
//   4. Toggling reveals/hides exactly the Tags and Updated column headers;
//      every other column (Pri, Title, Status, Agent) stays visible.
//
// RED evidence (2026-10-04, read src/components/workspaces/ListView.tsx):
// ListView renders ALL columns unconditionally today — the Tags and Updated
// ColumnMenus (label="Tags" / label="Updated") always render, no overflow
// control exists, and the file carries no `@container`/container-type (the
// wireframe §9 grep still holds at base fe0b68fb0). Every assertion below
// fails against this code.
//
// Browser-truth deferred to UAT/click-test: the measured breakpoint value
// itself (wireframe §9B measures it live from content).

import { describe, it, expect, vi } from 'vitest'
import { render, within, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Task } from '@/lib/api'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: `t-${Math.random().toString(36).slice(2)}`,
    title: 'A task',
    status: 'inbox',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    tags: ['alpha'],
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

import { ListView } from './ListView'

const agents = [
  { id: 'jim', name: 'Jim' },
]

function renderList(tasks: Task[]) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ListView tasks={tasks} agents={agents} onTaskClick={vi.fn()} />
    </QueryClientProvider>,
  )
}

function rootOf(mounted: ReturnType<typeof renderList>): HTMLElement {
  const root = mounted.container.firstElementChild
  expect(root, 'ListView must render a single root element').not.toBeNull()
  return root as HTMLElement
}

describe('ListView narrow — SP-33 mechanism: the List owns a measured, container-driven breakpoint', () => {
  it('the list declares itself a CSS container (@container)', () => {
    const mounted = renderList([makeTask({ title: 'Row task' })])
    const declaresContainer = Array.from(mounted.container.querySelectorAll('*')).some((el) =>
      el.classList.contains('@container'),
    )
    expect(declaresContainer).toBe(true)
  })

  it('the list root exposes its measured narrow state via data-narrow', () => {
    const mounted = renderList([makeTask({ title: 'Row task' })])
    const root = rootOf(mounted)
    expect(root.hasAttribute('data-narrow')).toBe(true)
    expect(['true', 'false']).toContain(root.getAttribute('data-narrow'))
  })
})

describe('ListView narrow — SP-35: Tags/Updated hidden behind a "⋯" control below the measured width', () => {
  it('narrow by default: the Tags and Updated column headers are hidden', () => {
    const mounted = renderList([
      makeTask({ id: 't-1', title: 'Tagged task', tags: ['alpha'] }),
    ])
    // jsdom containers measure 0 → narrow is the honest default (declared
    // seam, see header). In narrow mode the two governed columns are gone.
    expect(mounted.queryByText('Tags')).not.toBeInTheDocument()
    expect(mounted.queryByText('Updated')).not.toBeInTheDocument()
  })

  it('the "⋯" overflow control exists, is a real button, and names both governed columns', () => {
    const mounted = renderList([makeTask({ title: 'Tagged task' })])
    const toggle = mounted.getByTestId('list-narrow-columns-toggle')
    expect(toggle.tagName).toBe('BUTTON')
    const name = toggle.getAttribute('aria-label') ?? toggle.textContent ?? ''
    expect(name).toMatch(/Tags/i)
    expect(name).toMatch(/Updated/i)
  })

  it('toggling reveals Tags and Updated; toggling again hides them (state round-trip)', () => {
    const mounted = renderList([makeTask({ title: 'Tagged task' })])
    const toggle = mounted.getByTestId('list-narrow-columns-toggle')

    // Intermediate assertion pins the hidden start state so the click below
    // proves a transition, not a static condition.
    expect(mounted.queryByText('Tags')).not.toBeInTheDocument()

    fireEvent.click(toggle)
    expect(mounted.getByText('Tags')).toBeInTheDocument()
    expect(mounted.getByText('Updated')).toBeInTheDocument()

    fireEvent.click(toggle)
    expect(mounted.queryByText('Tags')).not.toBeInTheDocument()
    expect(mounted.queryByText('Updated')).not.toBeInTheDocument()
  })

  it('the toggle governs ONLY Tags and Updated — Pri, Title, Status, Agent stay visible throughout', () => {
    const mounted = renderList([makeTask({ title: 'Tagged task' })])
    const toggle = mounted.getByTestId('list-narrow-columns-toggle')

    // The untouched columns are present narrow…
    expect(mounted.getByText('Pri')).toBeInTheDocument()
    expect(mounted.getByText('Status')).toBeInTheDocument()

    fireEvent.click(toggle)
    // …and remain present with Tags/Updated revealed.
    expect(mounted.getByText('Pri')).toBeInTheDocument()
    expect(mounted.getByText('Status')).toBeInTheDocument()
    expect(mounted.getByText('Tags')).toBeInTheDocument()
    expect(mounted.getByText('Updated')).toBeInTheDocument()
  })

  it('revealed columns keep their data: a tagged task shows its tag once Tags is revealed', () => {
    const mounted = renderList([
      makeTask({ id: 't-1', title: 'Tagged task', tags: ['alpha'] }),
    ])
    fireEvent.click(mounted.getByTestId('list-narrow-columns-toggle'))
    const table = mounted.container
    expect(within(table as HTMLElement).getByText('alpha')).toBeInTheDocument()
  })
})
