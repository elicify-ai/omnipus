// Tests for the composer media-library picker + chips (ADR-051 Rev 4 / Slice H).
//
// Covers: the picker opens the library dialog, selecting an entry calls
// attachWorkspaceMedia (FR-022) + stages the ref in the library-attachment
// store, and LibraryAttachmentChips renders/removes staged attachments.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  ComposerMediaLibraryButton,
  LibraryAttachmentChips,
} from './ComposerMediaLibrary'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { clearLibraryAttachments, getLibraryAttachments } from '@/lib/library-attachment'
import type { MediaLibraryEntry } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaceMedia: vi.fn(),
    attachWorkspaceMedia: vi.fn(),
  }
})

import { fetchWorkspaceMedia, attachWorkspaceMedia } from '@/lib/api'

const mockedFetch = vi.mocked(fetchWorkspaceMedia)
const mockedAttach = vi.mocked(attachWorkspaceMedia)

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function makeEntry(over: Partial<MediaLibraryEntry> = {}): MediaLibraryEntry {
  return {
    id: 'm-1',
    workspace_id: 'ws-1',
    filename: 'diagram.png',
    mime: 'image/png',
    size: 204800,
    sha256: 'a'.repeat(64),
    uploaded_at: '2026-07-22T14:22:00Z',
    source: 'user_upload',
    ...over,
  } as MediaLibraryEntry
}

function setActiveWorkspace(id: string | null) {
  act(() => useWorkspacesStore.setState({ activeWorkspaceId: id }))
}

function renderPicker() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ComposerMediaLibraryButton />
    </QueryClientProvider>,
  )
}

function renderChips() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <LibraryAttachmentChips />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  clearLibraryAttachments()
  setActiveWorkspace('ws-1')
})

describe('ComposerMediaLibraryButton', () => {
  it('opens the dialog and lists library entries', async () => {
    mockedFetch.mockResolvedValue([makeEntry({ id: 'm-1', filename: 'diagram.png' })])
    renderPicker()
    fireEvent.click(screen.getByTestId('composer-library-attach'))
    await waitFor(() => expect(mockedFetch).toHaveBeenCalledWith('ws-1'))
    await waitFor(() => {
      expect(screen.getByTestId('library-pick-m-1')).toBeInTheDocument()
    })
    expect(screen.getByText('diagram.png')).toBeInTheDocument()
  })

  it('attaches on select (FR-022): calls attachWorkspaceMedia + stages the ref', async () => {
    mockedFetch.mockResolvedValue([makeEntry({ id: 'm-1', filename: 'diagram.png' })])
    mockedAttach.mockResolvedValue(undefined)
    renderPicker()
    fireEvent.click(screen.getByTestId('composer-library-attach'))
    await waitFor(() => expect(screen.getByTestId('library-pick-m-1')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('library-pick-m-1'))
    await waitFor(() => expect(mockedAttach).toHaveBeenCalledWith('ws-1', 'm-1'))

    // The ref was staged in the library-attachment store.
    expect(getLibraryAttachments()).toHaveLength(1)
    expect(getLibraryAttachments()[0].ref).toBe('media://workspace/ws-1/m-1')
  })

  it('is disabled when there is no active workspace', () => {
    setActiveWorkspace(null)
    renderPicker()
    expect(screen.getByTestId('composer-library-attach')).toBeDisabled()
  })

  it('shows the empty state when the library has no entries', async () => {
    mockedFetch.mockResolvedValue([])
    renderPicker()
    fireEvent.click(screen.getByTestId('composer-library-attach'))
    await waitFor(() => {
      expect(screen.getByText(/No files in this workspace yet/i)).toBeInTheDocument()
    })
  })
})

describe('LibraryAttachmentChips', () => {
  it('renders nothing when no attachments are staged', () => {
    renderChips()
    expect(screen.queryByTestId('library-attachment-chips')).not.toBeInTheDocument()
  })

  it('renders + removes a staged attachment chip', async () => {
    // Stage one directly through the public store API.
    const { addLibraryAttachment, buildWorkspaceMediaRef } = await import('@/lib/library-attachment')
    addLibraryAttachment({
      mediaId: 'm-1',
      ref: buildWorkspaceMediaRef('ws-1', 'm-1'),
      filename: 'diagram.png',
      contentType: 'image/png',
    })
    renderChips()
    expect(screen.getByTestId('library-chip-m-1')).toBeInTheDocument()
    expect(screen.getByText('diagram.png')).toBeInTheDocument()

    fireEvent.click(screen.getByLabelText(/Remove diagram.png/i))
    expect(screen.queryByTestId('library-chip-m-1')).not.toBeInTheDocument()
  })
})
