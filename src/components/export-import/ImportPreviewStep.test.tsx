// Import Preview step — spec order 25 (issue #1967 B1).
// Oracles: spec scenarios "Preview after a valid bundle", "Preview of a name
// clash", "Preview warnings and secrets", and "Cancel from the wizard" (the
// exact answer→screen table, incl. one cancel only and the CSRF stay).
//
// Every row reaches the Preview through the wizard's REAL in-wizard
// transition — landing → file choice → upload answer → Preview (CHECK F06: an
// initial-URL mount at step=preview with a previewed record IS the reload
// state and belongs to the reload table, not to a fresh wizard). The per-tab
// record under test is the one the wizard itself writes at Preview time.
//
// Package API module mocked (upload + cancel — the network edge); the preview
// answers are valid against the UNCHANGED generated PackageImportPreview
// (CHECK F02: will_add carries proposed_name/renamed, skipped is
// skipped_elements with name, manifest is required) — the
// PackageImportPreviewSchema.parse call in the fixture builder fails loudly if
// a fixture drifts from the contract. Expiries derive from Date.now() (CHECK
// F07: no wall-clock-pinned fixture).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { ApiError } from '@/lib/api-error'
import {
  PackageImportPreviewSchema,
  type PackageImportPreview,
} from '@/lib/api/packages'

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

function readRecord(): Record<string, unknown> | null {
  const raw = sessionStorage.getItem(RECORD_KEY)
  return raw ? (JSON.parse(raw) as Record<string, unknown>) : null
}

function validPreview(overrides: Partial<PackageImportPreview> = {}): PackageImportPreview {
  const preview: PackageImportPreview = {
    staging_id: 'S1',
    expires_at: new Date(Date.now() + 30 * 60_000).toISOString(),
    manifest: {
      format: 'omnpkg',
      format_version: 1,
      scope: 'custom',
      elements: [{ type: 'agent', schema: 1, source_id: 'a-writer', name: 'Writer', files: ['agent.json'] }],
      counts: {},
      files: {},
      source_build: 'v0.1.1',
      created_at: new Date(Date.now() - 60_000).toISOString(),
      includes_sessions: false,
      needs_secrets: [],
    },
    will_add: [{ type: 'agent', source_id: 'a-writer', source_name: 'Writer', proposed_name: 'Writer', renamed: false }],
    skipped_elements: [],
    warnings: [],
    needs_secrets: [],
    ...overrides,
  }
  PackageImportPreviewSchema.parse(preview)
  return preview
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
  vi.restoreAllMocks()
  sessionStorage.clear()
})

function renderPage() {
  const Component = (Route as unknown as { component: React.ComponentType }).component
  return render(<Component />)
}

async function chooseBundle() {
  const input = document.querySelector('input[type="file"]') as HTMLInputElement | null
  expect(input, 'the File step renders a bundle-file input before the change event').not.toBeNull()
  const file = new File([new Uint8Array(64)], 'bundle.omnpkg', { type: 'application/octet-stream' })
  await act(async () => {
    fireEvent.change(input!, { target: { files: [file] } })
  })
}

// The REAL transition: landing → File → upload answer → Preview. The wizard
// writes its own previewed record on the way; the row may rely on it.
async function openPreview(p: PackageImportPreview = validPreview()) {
  mockUpload.mockResolvedValue(p)
  renderPage()
  await chooseBundle()
  await screen.findByText('Will be added')
  expect(readRecord()?.phase, 'the wizard wrote the previewed record at Preview time').toBe('previewed')
  expect(readRecord()?.staging_id).toBe('S1')
}

describe('Import Preview (US-4.2, D2-1/2/9/13)', () => {
  it('a clean install: "Writer" under Will be added with final name "Writer", the expiry, and the preview address', async () => {
    await openPreview()
    expect(screen.getByText('Writer')).toBeInTheDocument()
    expect(screen.getByText(/Can be imported until/)).toBeInTheDocument()
    const expiryYear = String(new Date(Date.now() + 30 * 60_000).getFullYear())
    const expiryText = screen.getByText(/Can be imported until/).textContent ?? ''
    expect(
      new RegExp(expiryYear).test(expiryText),
      'the expiry renders in local form with the fixture year, not the ISO wire string',
    ).toBe(true)
    expect(expiryText.includes('T'), 'the rendered expiry is not the ISO wire string').toBe(false)
    await waitFor(() => expect(navigateSpy).toHaveBeenCalled())
    const arg = JSON.stringify(navigateSpy.mock.calls)
    expect(arg).toContain('preview')
    expect(arg).toContain('S1')
  })

  it('a name clash: "Writer → Writer (imported)" with the rename note (proposed_name + renamed, F02)', async () => {
    await openPreview(
      validPreview({
        will_add: [
          { type: 'agent', source_id: 'a-writer', source_name: 'Writer', proposed_name: 'Writer (imported)', renamed: true },
        ],
      }),
    )
    expect(await screen.findByText('Writer (imported)')).toBeInTheDocument()
    expect(
      screen.getByText('renamed: an agent with this name exists; it is not changed'),
    ).toBeInTheDocument()
  })

  it('warnings show their messages and items; secrets list under "Enter again after import"', async () => {
    await openPreview(
      validPreview({
        warnings: [
          { code: 'skill_dropped', message: 'The bundle names skills this install does not have.', items: ['x'] },
          { code: 'mcp_binding_dropped', message: 'A connector binding was dropped.', items: ['github'] },
        ],
        needs_secrets: [{ name: 'API_TOKEN', used_by: 'external tool', kind: 'executor_env' }],
      }),
    )
    expect(await screen.findByText('The bundle names skills this install does not have.')).toBeInTheDocument()
    expect(screen.getByText('x')).toBeInTheDocument()
    expect(screen.getByText('A connector binding was dropped.')).toBeInTheDocument()
    expect(screen.getByText('github')).toBeInTheDocument()
    expect(screen.getByText('Enter again after import')).toBeInTheDocument()
    expect(screen.getByText('API_TOKEN')).toBeInTheDocument()
  })

  it('a skipped element (skill, v1) is listed under Skipped (D2-9, skipped_elements.name)', async () => {
    await openPreview(
      validPreview({
        skipped_elements: [{ type: 'skill', source_id: 'sk-1', name: 'research' }],
      }),
    )
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
  // Fresh-preview rows: the Cancel click happens on the live Preview the
  // transition produced; the record is the wizard's own previewed write.
  async function openCancellablePreview() {
    await openPreview()
    expect(readRecord()?.phase).toBe('previewed')
  }

  // A person can reach Cancel AFTER visiting Confirm (Preview → Confirm →
  // Back): on that real flow the wizard has written the chosen workspace into
  // the record. The test models that wizard-produced state through the spec's
  // documented record fields.
  function recordWorkspaceChosen() {
    const record = readRecord()
    expect(record).not.toBeNull()
    sessionStorage.setItem(
      RECORD_KEY,
      JSON.stringify({ ...record, workspace_id: 'ws-general', workspace_name: 'General' }),
    )
  }

  it('204: landing, no message, tab record cleared', async () => {
    await openCancellablePreview()
    recordWorkspaceChosen()
    mockCancel.mockResolvedValue(undefined)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(mockCancel).toHaveBeenCalledTimes(1))
    expect(mockCancel).toHaveBeenCalledWith('S1')
    await waitFor(() => expect(navigateSpy).toHaveBeenCalled())
    const arg = JSON.stringify(navigateSpy.mock.calls)
    expect(arg).toContain('export-import')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(readRecord(), 'a successful cancel clears the record the wizard wrote').toBeNull()
  })

  it('500 staging_cleanup_failed: landing with the later-upload text, record cleared', async () => {
    await openCancellablePreview()
    recordWorkspaceChosen()
    mockCancel.mockRejectedValue(new ApiError(500, 'cleanup failed', { code: 'staging_cleanup_failed' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByText(
        'The upload was cancelled. Omnipus will try to remove its temporary files at a later upload.',
      ),
    ).toBeInTheDocument()
    expect(readRecord()).toBeNull()
  })

  it('404 staging_not_found: the Uncertain screen with Open Team for the recorded workspace and Start over', async () => {
    await openCancellablePreview()
    mockCancel.mockRejectedValue(new ApiError(404, 'gone', { code: 'staging_not_found' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByText(
        'Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again.',
      ),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Start over' })).toBeInTheDocument()
  })

  it('409 staging_busy: the same Uncertain screen', async () => {
    await openCancellablePreview()
    mockCancel.mockRejectedValue(new ApiError(409, 'busy', { code: 'staging_busy' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByText(
        'Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again.',
      ),
    ).toBeInTheDocument()
  })

  it('a lost cancel answer: the Uncertain screen and no second cancel', async () => {
    await openCancellablePreview()
    mockCancel.mockRejectedValue(new ApiError(0, 'Network unavailable. Check your connection.'))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
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
    await openCancellablePreview()
    mockCancel.mockRejectedValue(
      new ApiError(403, '403: csrf token mismatch', { body: '{"error":"csrf token mismatch"}' }),
    )
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(await screen.findByText('Your session needed refreshing. Select Cancel again.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20))
    })
    expect(mockCancel, 'nothing re-sent').toHaveBeenCalledTimes(1)
    expect(readRecord(), 'the record is unchanged').not.toBeNull()
  })
})
