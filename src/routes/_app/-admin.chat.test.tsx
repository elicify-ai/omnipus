import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'

const mockNavigate = vi.fn()
const mockCreateSession = vi.fn()
const mockSetActiveWorkspaceId = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: { component: React.ComponentType }) => options,
  useNavigate: () => mockNavigate,
}))

vi.mock('@/lib/api', () => ({
  createSession: mockCreateSession,
}))

vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: {
    getState: () => ({ setActiveWorkspaceId: mockSetActiveWorkspaceId }),
  },
}))

let AdminChatRoute: React.ComponentType

beforeEach(async () => {
  vi.clearAllMocks()
  mockCreateSession.mockResolvedValue({ id: 'admin-session-1' })
  const module = await import('./admin.chat')
  AdminChatRoute = (module.Route as unknown as { component: React.ComponentType }).component
})

describe('Admin chat entry route', () => {
  it('clears the workspace, creates an Admin session without a workspace, and enters the existing session route', async () => {
    render(<AdminChatRoute />)

    await waitFor(() => {
      expect(mockCreateSession).toHaveBeenCalledWith('admin')
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/sessions/$sessionId',
        params: { sessionId: 'admin-session-1' },
        replace: true,
      })
    })

    expect(mockSetActiveWorkspaceId).toHaveBeenCalledWith(null)
    expect(mockSetActiveWorkspaceId.mock.invocationCallOrder[0]).toBeLessThan(
      mockCreateSession.mock.invocationCallOrder[0],
    )
  })

  it('shows a retryable error when starting the standalone chat fails', async () => {
    mockCreateSession.mockRejectedValueOnce(new Error('gateway unavailable'))
    render(<AdminChatRoute />)

    expect(await screen.findByText('Could not start Admin chat.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()

    mockCreateSession.mockResolvedValueOnce({ id: 'admin-session-2' })
    await act(async () => {
      screen.getByRole('button', { name: 'Try again' }).click()
    })

    await waitFor(() => {
      expect(mockCreateSession).toHaveBeenCalledTimes(2)
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/sessions/$sessionId',
        params: { sessionId: 'admin-session-2' },
        replace: true,
      })
    })
  })
})
