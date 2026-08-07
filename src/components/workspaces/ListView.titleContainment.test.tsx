/**
 * ListView.titleContainment.test.tsx
 *
 * UAT Finding 2 (List view half): a 200-char unbroken title (no spaces —
 * exactly the create-task form's own `maxLength`) widened the TITLE cell
 * enough to push STATUS/TAGS/AGENT/UPDATED/Actions off-screen, with no
 * horizontal scroll to reach them. Root cause: `table-layout: auto` (the
 * default) sizes every column from CONTENT's min-content width — for an
 * unbroken string, that IS the string's full rendered width — completely
 * ignoring the Title cell's own `truncate`/overflow-hidden styling, which
 * only ever applies to content AFTER the column width is already decided.
 *
 * Fix: `table-fixed` on the `<table>` sizes every column from the HEADER
 * row's own widths instead (the other columns' explicit `w-12`/`w-24`/
 * `w-28`/`w-10` classes, Title left unwidthed to absorb whatever's left) —
 * content can no longer drive column width, so the Title cell's own
 * `truncate` (real overflow:hidden + text-overflow:ellipsis + nowrap,
 * replacing the old `line-clamp-1`, which has no ellipsis) finally has
 * something fixed-width to truncate against.
 *
 * jsdom has no real layout/intrinsic-sizing engine, so this can't measure
 * actual on-screen column pixel widths. What IS verifiable: (a) the
 * structural precondition for `table-layout: fixed` to size columns off the
 * HEADER row is in place (narrow columns keep explicit widths, Title does
 * not), (b) the `<table>` and the title `<button>` carry the exact classes
 * the fix relies on, and (c) — via a real injected stylesheet +
 * `getComputedStyle`, not just a className string match — those classes
 * resolve through jsdom's real CSSOM to the EXACT declarations Tailwind
 * v4.3.3 (the version this repo has installed) actually compiles them to.
 * That mapping was confirmed by literally compiling these class names
 * with the installed `@tailwindcss/node` package's own `compile()` API
 * (not guessed, not copied from memory):
 *
 *   .table-fixed { table-layout: fixed; }
 *   .truncate    { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ListView } from './ListView'
import type { Task } from '@/lib/api'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Task',
    status: 'inbox',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:05:00Z',
    ...overrides,
  }
}

function renderList(tasks: Task[]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ListView tasks={tasks} agents={[]} onTaskClick={vi.fn()} />
    </QueryClientProvider>,
  )
}

/** Real, compiled Tailwind v4.3.3 output for the two utilities this fix
 * relies on — verified via `@tailwindcss/node`'s own `compile()` API
 * against this repo's installed tailwindcss version, not hand-guessed. */
function injectRealTailwindDeclarations() {
  const style = document.createElement('style')
  style.textContent =
    '.table-fixed{table-layout:fixed}.truncate{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}'
  document.head.appendChild(style)
  return () => style.remove()
}

describe('ListView — long unbroken title containment (UAT Finding 2)', () => {
  it('the table uses table-fixed (column widths come from the header row, not cell content)', () => {
    renderList([makeTask()])
    const table = screen.getByRole('table')
    expect(table).toHaveClass('table-fixed')
  })

  it('narrow columns keep explicit widths; Title has none — it absorbs the remaining space under table-fixed', () => {
    renderList([makeTask()])
    const headerRow = screen.getAllByRole('row')[0]
    const headers = headerRow.querySelectorAll('th')
    // Pri, Title, Status, Tags, Agent, Updated, Actions — 7 columns.
    expect(headers.length).toBe(7)
    const widthClasses = Array.from(headers).map((th) => Array.from(th.classList).find((c) => /^w-\d+$/.test(c)))
    expect(widthClasses[0]).toBe('w-12') // Pri
    expect(widthClasses[1]).toBeUndefined() // Title — absorbs remaining width
    expect(widthClasses[2]).toBe('w-24') // Status
    expect(widthClasses[3]).toBe('w-28') // Tags
    expect(widthClasses[4]).toBe('w-24') // Agent
    expect(widthClasses[5]).toBe('w-28') // Updated
    expect(widthClasses[6]).toBe('w-10') // Actions
  })

  it('renders a 200-char unbroken title truncated (ellipsis) with a tooltip carrying the full string', () => {
    const removeStyle = injectRealTailwindDeclarations()
    try {
      const longTitle = 'B'.repeat(200) // exactly the create-task form's maxLength, no spaces
      renderList([makeTask({ title: longTitle })])

      const titleButton = screen.getByRole('button', { name: new RegExp(`^${longTitle}, status`) })
      // Full text is preserved in the DOM — only CSS (truncate) visually clips it.
      expect(titleButton.textContent).toBe(longTitle)
      expect(titleButton).toHaveAttribute('title', longTitle)
      expect(titleButton).toHaveClass('truncate')
      expect(titleButton).not.toHaveClass('line-clamp-1') // replaced: line-clamp has no ellipsis

      // Real cascade resolution (not just a className string match): the
      // injected stylesheet mirrors Tailwind's ACTUAL compiled output
      // (verified above), proving the class takes effect through jsdom's
      // real CSSOM.
      const computed = getComputedStyle(titleButton)
      expect(computed.overflow).toBe('hidden')
      expect(computed.textOverflow).toBe('ellipsis')
      expect(computed.whiteSpace).toBe('nowrap')

      const table = screen.getByRole('table')
      expect(getComputedStyle(table).tableLayout).toBe('fixed')
    } finally {
      removeStyle()
    }
  })

  it('short titles are unaffected — no visible change for normal-length content', () => {
    renderList([makeTask({ title: 'Fix login bug' })])
    const titleButton = screen.getByRole('button', { name: /^Fix login bug, status/ })
    expect(titleButton.textContent).toBe('Fix login bug')
    expect(titleButton).toHaveAttribute('title', 'Fix login bug')
    expect(titleButton).toHaveClass('truncate')
  })
})
