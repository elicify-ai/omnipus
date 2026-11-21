// Import Preview step — spec order 25 (issue #1967 B1).
// Oracles: spec scenarios "Preview after a valid bundle", "Preview of a name
// clash", "Preview warnings and secrets", and "Cancel from the wizard" (the
// exact answer→screen table, incl. one cancel only and the CSRF stay).
// Package API module mocked (upload + cancel).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { ApiError } from '@/lib/api-error'
import type { PackageImportPreview } from '@/lib/api/packages'

let mockSearch: Record<string, string | undefined> = {}

const { navigateSpy, mockAgents, mockWorkspaces, mockUpload, mockCancel } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockAgents: vi.fn(),
  mockWorkspaces: vi.fn(),
  mockUpload: vi.fn(),
  mockCancel: vi.fn(),
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
  uploadPackageImport: mockUpload,
  cancelPackageImport: mockCancel,
}))

import { Route } from '@/routes/_app/export-import'
import { rememberIdentityMode } from '@/lib/api/identityMode'
import { useUiStore } from '@/store/ui'

const RECORD_KEY = 'omnipus.exportImport.import'

function seedRecord(record: Record<string, unknown>) {
  sessionStorage.setItem(RECORD_KEY, JSON.stringify(record))
}

function readRecord(): Record<string, unknown> | null {
  const raw = sessionStorage.getItem(RECORD_KEY)
  return raw ? (JSON.parse(raw) as Record<string, unknown>) : null
}

const preview: PackageImportPreview = {
  staging_id: 'S1',
  expires_at: '2026-11-21T09:30:00Z',
  will_add: [{ source_id: 'a-writer', source_name: 'Writer', final_name: 'Writer' }],
  skipped: [],
  warnings: [],
  needs_secrets: [],
}

beforeEach(() => {
  mockSearch = { wizard: 'import' }
  navigateSpy.mockReset()
  mockAgents.mockResolvedValue([])
  mockWorkspaces.mockResolvedValue([])
  mockUpload.mockReset()
  mockCancel.mockReset()
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

async function chooseBundle() {
  const input = document.querySelector('input[type="file"]') as HTMLInputElement
  expect(input).not.toBeNull()
  const file = new File([new Uint8Array(64)], 'bundle.omnpkg', { type: 'application/octet-stream' })
  await act(async () => {
    fireEvent.change(input, { target: { files: [file] } })
  })
}

async function openPreview(p: PackageImportPreview = preview) {
  mockUpload.mockResolvedValue(p)
  renderPage()
  await chooseBundle()
  await screen.findByText('Will be added')
}

describe('Import Preview (US-4.2, D2-1/2/9/13)', () => {
  it('a clean install: "Writer" under Will be added with final name "Writer", the expiry, and the preview address', async () => {
    await openPreview()
    expect(screen.getByText('Writer')).toBeInTheDocument()
    expect(screen.getByText(/Can be imported until/)).toBeInTheDocument()
    const expiryText = screen.getByText(/Can be imported until/).textContent ?? ''
    expect(expiryText).toMatch(/2026/)
    expect(expiryText).not.toContain('T09:30:00Z')
    await waitFor(() => expect(navigateSpy).toHaveBeenCalled())
    const arg = JSON.stringify(navigateSpy.mock.calls)
    expect(arg).toContain('preview')
    expect(arg).toContain('S1')
  })

  it('a name clash: "Writer → Writer (imported)" with the rename note', async () => {
    await openPreview({
      ...preview,
      will_add: [{ source_id: 'a-writer', source_name: 'Writer', final_name: 'Writer (imported)' }],
    })
    expect(await screen.findByText('Writer (imported)')).toBeInTheDocument()
    expect(
      screen.getByText('renamed: an agent with this name exists; it is not changed'),
    ).toBeInTheDocument()
  })

  it('warnings show their messages and items; secrets list under "Enter again after import"', async () => {
    await openPreview({
      ...preview,
      warnings: [
        { code: 'skill_dropped', message: 'The bundle names skills this install does not have.', items: ['x'] },
        { code: 'mcp_binding_dropped', message: 'A connector binding was dropped.', items: ['github'] },
      ],
      needs_secrets: [{ name: 'API_TOKEN', used_by: 'external tool', kind: 'executor_env' }],
    })
    expect(await screen.findByText('The bundle names skills this install does not have.')).toBeInTheDocument()
    expect(screen.getByText('x')).toBeInTheDocument()
    expect(screen.getByText('A connector binding was dropped.')).toBeInTheDocument()
    expect(screen.getByText('github')).toBeInTheDocument()
    expect(screen.getByText('Enter again after import')).toBeInTheDocument()
    expect(screen.getByText('API_TOKEN')).toBeInTheDocument()
  })

  it('a skipped element (skill, v1) is listed under Skipped (D2-9)', async () => {
    await openPreview({
      ...preview,
      skipped: [{ source_id: 'sk-1', source_name: 'research', reason: 'element type not supported in this version' }],
    })
    expect(await screen.findByText('Skipped')).toBeInTheDocument()
    expect(screen.getByText('research')).toBeInTheDocument()
  })

  it('Continue moves to the Confirm step for the same staging', async () => {
    await openPreview()
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await waitFor(() => expect(navigateSpy).toHaveBeenCalled())
    const arg = JSON.stringify(navigateSpy.mock.calls)
    expect(arg).toContain('confirm')
    expect(arg).toContain('S1')
  })
})

describe('Cancel from the wizard (US-7, FR-026)', () => {
  function openCancellablePreview() {
    seedRecord({
      staging_id: 'S1',
      phase: 'previewed',
      workspace_id: 'ws-general',
      workspace_name: 'General',
      written_at: new Date().toISOString(),
    })
    mockSearch = { wizard: 'import', step: 'preview', staging_id: 'S1' }
    renderPage()
  }

  it('204: landing, no message, tab record cleared', async () => {
    openCancellablePreview()
    mockCancel.mockResolvedValue(undefined)
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(mockCancel).toHaveBeenCalledTimes(1))
    expect(mockCancel).toHaveBeenCalledWith('S1')
    await waitFor(() => expect(navigateSpy).toHaveBeenCalled())
    const arg = JSON.stringify(navigateSpy.mock.calls)
    expect(arg).toContain('export-import')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(readRecord(), 'a successful cancel clears the record').toBeNull()
  })

  it('500 staging_cleanup_failed: landing with the later-upload text, record cleared', async () => {
    openCancellablePreview()
    mockCancel.mockRejectedValue(new ApiError(500, 'cleanup failed', { code: 'staging_cleanup_failed' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByText(
        'The upload was cancelled. Omnipus will try to remove its temporary files at a later upload.',
      ),
    ).toBeInTheDocument()
    expect(readRecord()).toBeNull()
  })

  it('404 staging_not_found: the Uncertain screen with Open Team for the recorded workspace and Start over', async () => {
    openCancellablePreview()
    mockCancel.mockRejectedValue(new ApiError(404, 'gone', { code: 'staging_not_found' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByText(
        'Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again.',
      ),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Start over' })).toBeInTheDocument()
  })

  it('409 staging_busy: the same Uncertain screen', async () => {
    openCancellablePreview()
    mockCancel.mockRejectedValue(new ApiError(409, 'busy', { code: 'staging_busy' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByText(
        'Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again.',
      ),
    ).toBeInTheDocument()
  })

  it('a lost cancel answer: the Uncertain screen and no second cancel', async () => {
    openCancellablePreview()
    mockCancel.mockRejectedValue(new ApiError(0, 'Network unavailable. Check your connection.'))
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByText(
        'Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again.',
      ),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20))
    })
    expect(mockCancel, 'never resent').toHaveBeenCalledTimes(1)
  })

  it('a CSRF 403 stays on Preview with the session text; nothing re-sent', async () => {
    openCancellablePreview()
    mockCancel.mockRejectedValue(
      new ApiError(403, '403: csrf token mismatch', { body: '{"error":"csrf token mismatch"}' }),
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(await screen.findByText('Your session needed refreshing. Select Cancel again.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20))
    })
    expect(mockCancel, 'nothing re-sent').toHaveBeenCalledTimes(1)
    expect(readRecord(), 'the record is unchanged').not.toBeNull()
  })
})
