// The workspace chevron names agents, not sessions. Show when folded, Hide when open.

import type { ReactNode } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { Workspace } from '@/lib/api'
import { WorkspaceAgentList } from './WorkspaceAgentList'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/' }),
  Link: ({ children }: { children: ReactNode }) => children,
}))

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  })
}

const project = {
  id: 'product-launch',
  name: 'Product launch',
  is_default: false,
} as Workspace

function renderList(expanded: boolean) {
  return render(
    <WorkspaceAgentList
      projects={[project]}
      expandedIds={expanded ? new Set([project.id]) : new Set()}
      activeId={null}
      agents={[]}
      rosterState="fresh"
      sessions={[]}
      onToggle={() => {}}
      onOpen={() => {}}
      onOverlayClose={() => {}}
    />,
  )
}

describe('workspace chevron name', () => {
  it('names a folded workspace as show agents, not sessions', () => {
    renderList(false)
    expect(screen.getByRole('button', { name: 'Show Product launch agents' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('button', { name: /sessions/i })).toBeNull()
  })

  it('names an open workspace as hide agents', () => {
    renderList(true)
    expect(screen.getByRole('button', { name: 'Hide Product launch agents' })).toHaveAttribute('aria-expanded', 'true')
  })
})
