// Export Download step — spec order 22 (issue #1967 B1).
// Oracles: spec scenarios "Download saves the bundle", "Agent deleted before
// download", "Audit failure stops the download", and the Expired-review
// download rows (re-read 404 before the export → Choose notice, no export
// request). Package API module mocked.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { ApiError } from '@/lib/api-error'

let mockSearch: Record<string, string | undefined> = {}

const { navigateSpy, mockAgents, mockWorkspaces, mockPlanRead, mockExport } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockAgents: vi.fn(),
  mockWorkspaces: vi.fn(),
  mockPlanRead: vi.fn(),
  mockExport: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: Record<string, unknown>) => ({ ...opts, useSearch: () => mockSearch }),
    useNavigate: () => navigateSpy,
  }
})

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents: mockAgents,
  fetchWorkspaces: mockWorkspaces,
}))

vi.mock('@/lib/api/packages', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/packages')>()),
  getPackageExportPlan: mockPlanRead,
  exportPackage: mockExport,
}))

import { Route } from '@/routes/_app/export-import'
import { rememberIdentityMode } from '@/lib/api/identityMode'
import { useUiStore } from '@/store/ui'

const plan = {
  plan_id: 'P1',
  created_at: '2026-11-21T09:00:00Z',
  expires_at: '2026-11-21T09:30:00Z',
  scope: 'agents',
  elements: [
    {
      type: 'agent',
      source_id: 'a-writer',
      name: 'Writer',
      kind: 'main',
      files: [
        { path: 'agent.json', size: 1200 },
        { path: 'SOUL.md', size: 2000 },
      ],
      bytes: 3200,
    },
  ],
  left_out: [],
  needs_secrets: [],
  disclosures: [{ code: 'free_text_copied', message: 'free text' }],
  size_bytes: 3200,
  file_name: 'Writer.omnpkg',
}

beforeEach(() => {
  mockSearch = { wizard: 'export', step: 'download', plan_id: 'P1' }
  navigateSpy.mockReset()
  mockAgents.mockResolvedValue([{ id: 'a-writer', name: 'Writer', type: 'Main', locked: false, description: 'writes' }])
  mockWorkspaces.mockResolvedValue([])
  mockPlanRead.mockReset().mockResolvedValue(plan)
  mockExport.mockReset()
  rememberIdentityMode('local')
  useUiStore.setState({ addToast: vi.fn() as unknown as () => void })
})

afterEach(() => {
  sessionStorage.clear()
})

function renderPage() {
  const Component = (Route as unknown as { component: React.ComponentType }).component
  return render(<Component />)
}

describe('Export Download (US-2.4)', () => {
  it('saves the bundle and shows "Saved <file> (<size>)."', async () => {
    mockExport.mockResolvedValue({
      blob: new Blob([new Uint8Array(3200)]),
      fileName: 'Writer.omnpkg',
    })
    renderPage()
    expect(await screen.findByText('Saved Writer.omnpkg (3.1 KB).')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Export another' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Done' })).toBeInTheDocument()
    expect(mockExport).toHaveBeenCalledWith(['a-writer'])
  })

  it('agent deleted before download: the missing-agent text with "Back to review", no saved file', async () => {
    mockExport.mockRejectedValue(new ApiError(404, 'gone', { code: 'agent_not_found' }))
    renderPage()
    expect(await screen.findByText('This agent no longer exists.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Back to review' })).toBeInTheDocument()
    expect(screen.queryByText(/Saved /)).toBeNull()
  })

  it('audit failure stops the download with the exact text', async () => {
    mockExport.mockRejectedValue(new ApiError(500, 'no', { code: 'audit_failed' }))
    renderPage()
    expect(
      await screen.findByText(
        'The export was stopped because the audit log could not be written. Nothing was downloaded.',
      ),
    ).toBeInTheDocument()
  })

  it('re-read just before the download answers 404: Choose with the expired notice and NO export request', async () => {
    mockPlanRead.mockRejectedValue(new ApiError(404, 'gone', { code: 'plan_not_found' }))
    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1' }
    renderPage()
    await screen.findByText('Settings')
    fireEvent.click(screen.getByRole('button', { name: 'Download' }))
    expect(await screen.findByText('The review expired. Check your choice and review again.')).toBeInTheDocument()
    await waitFor(() => expect(mockPlanRead).toHaveBeenCalled())
    expect(mockExport, 'the export never runs on an expired review').not.toHaveBeenCalled()
  })
})
