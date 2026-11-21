// Import File step — spec order 24 (issue #1967 B1).
// Oracles: spec scenarios "Upload refusals" (exact mapped texts, stays on the
// File step) and "Size check before upload" (67,108,864 sent; 67,108,865 not
// sent, with the too-large text). Package API module mocked.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApiError } from '@/lib/api-error'
import type { PackageImportPreview } from '@/lib/api/packages'

let mockSearch: Record<string, string | undefined> = {}

const { navigateSpy, mockAgents, mockWorkspaces, mockUpload } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockAgents: vi.fn(),
  mockWorkspaces: vi.fn(),
  mockUpload: vi.fn(),
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
}))

import { Route } from '@/routes/_app/export-import'
import { rememberIdentityMode } from '@/lib/api/identityMode'
import { useUiStore } from '@/store/ui'
import { PackageImportPreviewSchema } from '@/lib/api/packages'

// Generated wire shape (CHECK F02); the expiry derives from Date.now() with a
// 30-minute window (CHECK F07 — no wall-clock-pinned fixture). A fixture that
// fails its own schema is a harness bug, so it is parsed here.
const preview: PackageImportPreview = PackageImportPreviewSchema.parse({
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
})

beforeEach(() => {
  mockSearch = { wizard: 'import' }
  navigateSpy.mockReset()
  mockAgents.mockResolvedValue([])
  mockWorkspaces.mockResolvedValue([])
  mockUpload.mockReset()
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

async function chooseFile(bytes = 128, name = 'bundle.omnpkg') {
  const input = document.querySelector('input[type="file"]') as HTMLInputElement | null
  expect(input, 'the File step renders a bundle-file input before the change event fires').not.toBeNull()
  const file = new File([new Uint8Array(bytes)], name, { type: 'application/octet-stream' })
  await act(async () => {
    fireEvent.change(input!, { target: { files: [file] } })
  })
  return file
}

describe('Import File step (US-4.1)', () => {
  it('shows the file button with its hint', async () => {
    renderPage()
    expect(await screen.findByRole('button', { name: 'Choose a bundle file' })).toBeInTheDocument()
    expect(screen.getByText('Bundle files end in .omnpkg')).toBeInTheDocument()
  })

  it('announces "Checking the bundle…" while the upload runs', async () => {
    let release: (p: PackageImportPreview) => void = () => {}
    mockUpload.mockReturnValue(
      new Promise<PackageImportPreview>((resolve) => {
        release = resolve
      }),
    )
    renderPage()
    await chooseFile()
    expect(screen.getByText('Checking the bundle…')).toBeInTheDocument()
    act(() => release(preview))
  })

  it('a valid bundle moves to the Preview step', async () => {
    mockUpload.mockResolvedValue(preview)
    renderPage()
    await chooseFile()
    expect(await screen.findByText('Will be added')).toBeInTheDocument()
    expect(mockUpload).toHaveBeenCalledTimes(1)
  })

  describe('Upload refusals (US-5.1, D2)', () => {
    const rows: Array<[string, string, string]> = [
      ['notes.txt (text)', 'not_a_package', 'This file is not an Omnipus bundle.'],
      ['empty file', 'not_a_package', 'This file is not an Omnipus bundle.'],
      ['format_version 2', 'package_too_new', 'This bundle was made by a newer Omnipus. Update this install to import it.'],
      ['element schema 2', 'element_schema_too_new', 'Part of this bundle was made by a newer Omnipus. Update this install to import it.'],
      ['one changed byte in SOUL.md', 'checksum_mismatch', 'This bundle is damaged: a file does not match its record. Export it again.'],
      ['two agents', 'too_many_agents', 'This bundle holds more than one agent. This version imports one agent per bundle.'],
      [
        'sixth upload while five are kept',
        'too_many_staged_imports',
        'Too many imports are waiting. Each one expires 30 minutes after upload; finish or cancel one, then try again.',
      ],
    ]

    for (const [fileDesc, code, text] of rows) {
      it(`${fileDesc}: the refusal text shows in an alert and the step stays`, async () => {
        mockUpload.mockRejectedValue(new ApiError(422, 'refused', { code }))
        renderPage()
        await chooseFile(64, fileDesc.endsWith('.txt') ? 'notes.txt' : 'bundle.omnpkg')
        const alert = await screen.findByRole('alert')
        expect(alert).toHaveTextContent(text)
        // Still on the File step.
        expect(screen.getByRole('button', { name: 'Choose a bundle file' })).toBeInTheDocument()
        expect(screen.queryByText('Will be added')).toBeNull()
      })
    }
  })

  describe('Size check before upload (US-5.3, D2-10/11)', () => {
    it('a file of exactly 67,108,864 bytes is sent', async () => {
      mockUpload.mockResolvedValue(preview)
      renderPage()
      await chooseFile(67108864)
      await screen.findByText('Will be added')
      expect(mockUpload).toHaveBeenCalledTimes(1)
    })

    it('a file of 67,108,865 bytes is not sent and shows the too-large text', async () => {
      renderPage()
      await chooseFile(67108865)
      expect(
        screen.getByText('This bundle is too large for this version (most is 64 MB, 256 MB unpacked).'),
      ).toBeInTheDocument()
      expect(mockUpload, 'nothing above the limit reaches the server').not.toHaveBeenCalled()
    })
  })
})
