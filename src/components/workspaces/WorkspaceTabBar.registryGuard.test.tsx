import React from 'react'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'

const { beforeLeave } = vi.hoisted(() => ({
  beforeLeave: vi.fn<() => Promise<boolean>>(),
}))

vi.mock('@/components/panel-shell/registry', () => ({
  getPanelDefinition: (id: string) => id === 'tasks'
    ? {
        id: 'tasks',
        title: 'Tasks',
        content: () => null,
        fullScreen: { toSearch: () => ({}), fromSearch: () => ({}) },
        beforeLeave,
      }
    : undefined,
}))

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/workspaces/ws-1/chat' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to }: { children: React.ReactNode; to: string }) => (
    <a href={to}>{children}</a>
  ),
}))

vi.mock('framer-motion', () => ({
  motion: {
    div: ({ children, ...rest }: React.HTMLAttributes<HTMLDivElement>) => <div {...rest}>{children}</div>,
  },
}))

vi.mock('@/components/ui/dropdown-menu', async () => {
  const { Button } = await vi.importActual<typeof import('@/components/ui/button')>('@/components/ui/button')
  return {
    DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuItem: ({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) => (
      <Button variant="ghost" onClick={onClick}>{children}</Button>
    ),
  }
})

import { WorkspaceTabBar } from './WorkspaceTabBar'

beforeEach(() => {
  beforeLeave.mockReset().mockResolvedValue(false)
  useUiStore.setState({ activePanel: null })
})

describe('WorkspaceTabBar registry leave gate', () => {
  it('keeps a fake registered outgoing panel open when its beforeLeave declines', async () => {
    act(() => useUiStore.getState().openPanel('tasks', { workspaceId: 'ws-1' }))
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="Workspace" />)

    fireEvent.click(screen.getByTestId('workspace-tab-media'))

    await waitFor(() => expect(beforeLeave).toHaveBeenCalledOnce())
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'tasks',
      context: { workspaceId: 'ws-1' },
    })
  })
})
