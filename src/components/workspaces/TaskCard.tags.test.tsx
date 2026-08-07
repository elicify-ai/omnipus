/**
 * TaskCard.tags.test.tsx
 *
 * ADR-049 (US-11 AS-6/AS-7): tag chips replace the removed milestone chip
 * (`TaskCard.tsx:191-195` pre-removal). Migrated `milestone:<name>` tags
 * render as ordinary chips, verbatim — no special-casing, no client-side
 * re-prefixing/uniquifying.
 *
 * `showActions={false}` on every render here (ADR-052 §6.8): this file only
 * exercises tag-chip rendering, unrelated to the ▶/■ action button — turning
 * it off avoids needing a QueryClientProvider (TaskActionButton's
 * useMutation/useQueryClient would otherwise throw without one).
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TaskCard } from './TaskCard'
import type { Task } from '@/lib/api'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Sample task',
    status: 'inbox',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

describe('TaskCard — tag chips (replaces the milestone chip)', () => {
  it('renders no tag chips and no milestone UI when the task has no tags', () => {
    render(<TaskCard task={makeTask()} onClick={() => {}} showActions={false} />)
    expect(screen.queryByText(/milestone/i)).toBeNull()
  })

  it('renders a chip per tag', () => {
    render(<TaskCard task={makeTask({ tags: ['release', 'urgent'] })} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('release')).toBeInTheDocument()
    expect(screen.getByText('urgent')).toBeInTheDocument()
  })

  it('caps visible chips at 3 with a "+N" overflow indicator', () => {
    render(
      <TaskCard
        task={makeTask({ tags: ['a', 'b', 'c', 'd', 'e'] })}
        onClick={() => {}}
        showActions={false}
      />,
    )
    expect(screen.getByText('a')).toBeInTheDocument()
    expect(screen.getByText('b')).toBeInTheDocument()
    expect(screen.getByText('c')).toBeInTheDocument()
    expect(screen.queryByText('d')).toBeNull()
    expect(screen.queryByText('e')).toBeNull()
    expect(screen.getByText('+2')).toBeInTheDocument()
  })

  it('renders a migrated milestone:<name> tag as an ordinary chip, verbatim', () => {
    render(<TaskCard task={makeTask({ tags: ['milestone:q3'] })} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('milestone:q3')).toBeInTheDocument()
    // No dedicated "Milestone" label/dropdown/select anywhere on the card.
    expect(screen.queryByText(/^milestone$/i)).toBeNull()
  })

  it('a chip carries the full tag as its title (tooltip) for truncation', () => {
    const longTag = 'a'.repeat(64)
    render(<TaskCard task={makeTask({ tags: [longTag] })} onClick={() => {}} showActions={false} />)
    expect(screen.getByTitle(longTag)).toBeInTheDocument()
  })
})
