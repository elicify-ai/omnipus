import React from 'react'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/workspaces/ws-1/chat' }),
  useNavigate: () => navigate,
  Link: ({ children, ...props }: React.AnchorHTMLAttributes<HTMLAnchorElement>) => <a {...props}>{children}</a>,
}))
vi.mock('framer-motion', () => ({
  motion: { div: ({ children, ...props }: React.HTMLAttributes<HTMLDivElement>) => <div {...props}>{children}</div> },
}))
vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({ children, onClick, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement>) => (
    <button type="button" onClick={onClick} {...props}>{children}</button>
  ),
}))

import { WorkspaceTabBar } from './WorkspaceTabBar'

beforeEach(() => {
  navigate.mockClear()
  act(() => useUiStore.getState().closePanel())
})

describe('WorkspaceTabBar compact dropdown parity', () => {
  it('uses the Library toggle model, including aria-pressed and second-click close', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="Workspace One" />)

    const library = screen.getByTestId('workspace-view-switcher-media')
    expect(library).toHaveAttribute('aria-pressed', 'false')
    expect(library).toHaveAttribute('data-panel-trigger', 'library')
    fireEvent.click(library)
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'ws-1' },
    })
    expect(library).toHaveAttribute('aria-pressed', 'true')
    expect(navigate).not.toHaveBeenCalled()

    fireEvent.click(library)
    expect(useUiStore.getState().activePanel).toBeNull()
    expect(library).toHaveAttribute('aria-pressed', 'false')
  })
})
