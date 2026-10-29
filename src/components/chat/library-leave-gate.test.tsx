/**
 * FR-008 / BS-5.5 / BS-6.4 / BS-6.6: chat link, file card, image card, and the
 * notice Open action go through the real leave gate. Cancel keeps the dirty
 * Library. Discard opens the named target. A live frame while the Library is
 * dirty does not open the dialog itself; it leaves one notice for that session.
 */
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { AssistantMediaItem } from '@/components/chat/AssistantMediaItem'
import { LibraryOpenNotice } from '@/components/chat/LibraryOpenNotice'
import { createLinkRenderer } from '@/components/chat/markdown-shared'
import {
  getDiscardConfirmDialogOpen,
  isLibraryEditorDirty,
  resolveDiscardConfirmDialog,
  setLibraryEditorDirty,
} from '@/components/library/preview/unsavedGuard'
import { useChatStore } from '@/store/chat'
import { resetLibraryOpenState, showLibraryOpenNotice, useLibraryOpenStore } from '@/store/chat/libraryOpen'
import { emptySessionState } from '@/store/chat/session'
import { registerChatView } from '@/store/chatView'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

const MarkdownLink = createLinkRenderer(null, { chatLinks: true })
const DIRTY = { id: 'library' as const, context: { workspaceId: 'W', path: 'notes/dirty.md' } }
const REQUEST = '0123456789abcdef0123456789abcdef'

function armDirtyLibrary() {
  useUiStore.getState().openPanel('library', DIRTY.context)
  setLibraryEditorDirty(true)
}

async function cancelKeepsDirtyThenDiscardOpens(
  activate: () => void,
  target: { workspaceId: string; path: string },
) {
  activate()
  expect(getDiscardConfirmDialogOpen()).toBe(true)
  expect(useUiStore.getState().activePanel).toEqual(DIRTY)

  resolveDiscardConfirmDialog(false)
  expect(getDiscardConfirmDialogOpen()).toBe(false)
  expect(isLibraryEditorDirty()).toBe(true)
  expect(useUiStore.getState().activePanel).toEqual(DIRTY)

  activate()
  expect(getDiscardConfirmDialogOpen()).toBe(true)
  resolveDiscardConfirmDialog(true)
  await waitFor(() => expect(useUiStore.getState().activePanel).toEqual({ id: 'library', context: target }))
  expect(isLibraryEditorDirty()).toBe(false)
}

describe('Library opens honour the leave gate (FR-008)', () => {
  afterEach(() => {
    if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
    setLibraryEditorDirty(false)
    useUiStore.getState().closePanel()
    resetLibraryOpenState()
  })

  it('a chat link Cancel stays, Discard opens the link target', async () => {
    armDirtyLibrary()
    render(<MarkdownLink href="omnipus://file/W/secrets/payroll.xlsx">payroll</MarkdownLink>)
    const link = screen.getByTestId('markdown-link')
    await cancelKeepsDirtyThenDiscardOpens(
      () => fireEvent.click(link),
      { workspaceId: 'W', path: 'secrets/payroll.xlsx' },
    )
  })

  it('a file card Cancel stays, Discard opens the card path (BS-5.5)', async () => {
    armDirtyLibrary()
    render(
      <AssistantMediaItem
        kind="file"
        url="/api/v1/media/plan.md"
        filename="plan.md"
        libraryRef={{ workspace_id: 'W', path: 'notes/plan.md' }}
      />,
    )
    const button = screen.getByRole('button', { name: 'Open plan.md in Library' })
    await cancelKeepsDirtyThenDiscardOpens(
      () => fireEvent.click(button),
      { workspaceId: 'W', path: 'notes/plan.md' },
    )
  })

  it('an image card Cancel stays, Discard opens the image path (follow-up 3)', async () => {
    armDirtyLibrary()
    render(
      <AssistantMediaItem
        kind="image"
        url="/api/v1/media/pic.png"
        filename="pic.png"
        libraryRef={{ workspace_id: 'W', path: 'pics/pic.png' }}
      />,
    )
    const button = screen.getByRole('button', { name: 'Open pic.png in Library' })
    await cancelKeepsDirtyThenDiscardOpens(
      () => fireEvent.click(button),
      { workspaceId: 'W', path: 'pics/pic.png' },
    )
  })

  it('keeps one notice per session, and Dismiss or Escape removes only that one', async () => {
    showLibraryOpenNotice('sess-a', { workspaceId: 'W', path: 'notes/a.md', requestId: 'a'.repeat(32) })
    showLibraryOpenNotice('sess-b', { workspaceId: 'W', path: 'notes/b.md', requestId: 'b'.repeat(32) })
    render(
      <>
        <LibraryOpenNotice sessionId="sess-a" />
        <LibraryOpenNotice sessionId="sess-b" />
      </>,
    )
    expect(screen.getByText('a.md')).toBeInTheDocument()
    expect(screen.getByText('b.md')).toBeInTheDocument()

    await act(async () => {
      showLibraryOpenNotice('sess-a', { workspaceId: 'W', path: 'notes/newer.md', requestId: 'c'.repeat(32) })
    })
    expect(screen.getByText('newer.md')).toBeInTheDocument()
    expect(screen.queryByText('a.md')).toBeNull()
    expect(screen.getByText('b.md')).toBeInTheDocument()
    expect(Object.keys(useLibraryOpenStore.getState().notices)).toEqual(['sess-a', 'sess-b'])

    fireEvent.click(screen.getAllByRole('button', { name: 'Dismiss' })[1])
    expect(useLibraryOpenStore.getState().notices['sess-b']).toBeUndefined()
    expect(useLibraryOpenStore.getState().notices['sess-a']?.path).toBe('notes/newer.md')

    fireEvent.keyDown(screen.getByTestId('library-open-notice'), { key: 'Escape' })
    expect(useLibraryOpenStore.getState().notices['sess-a']).toBeUndefined()
    expect(useUiStore.getState().activePanel).toBeNull()
  })

  it('a dirty Library stores a notice instead of prompting, and Open then runs the gate (BS-6.4)', async () => {
    armDirtyLibrary()
    useSessionStore.getState().setActiveSession('sess-1', 'mia')
    useChatStore.setState({ sessionsById: { 'sess-1': emptySessionState() } })
    const release = registerChatView('sess-1', 'W')
    try {
      useChatStore.getState().handleFrame({
        type: 'open_in_library',
        session_id: 'sess-1',
        call_id: 'call-1',
        request_id: REQUEST,
        workspace_id: 'W',
        path: 'notes/plan.md',
      } as never)
      expect(getDiscardConfirmDialogOpen()).toBe(false)
      expect(useUiStore.getState().activePanel).toEqual(DIRTY)
      expect(useLibraryOpenStore.getState().notices['sess-1']).toEqual({
        workspaceId: 'W',
        path: 'notes/plan.md',
        requestId: REQUEST,
      })

      render(<LibraryOpenNotice sessionId="sess-1" />)
      const openNotice = () => fireEvent.click(screen.getByRole('button', { name: 'Open plan.md in Library' }))
      openNotice()
      expect(getDiscardConfirmDialogOpen()).toBe(true)
      expect(useUiStore.getState().activePanel).toEqual(DIRTY)
      expect(useLibraryOpenStore.getState().notices['sess-1']).toBeUndefined()

      resolveDiscardConfirmDialog(false)
      expect(getDiscardConfirmDialogOpen()).toBe(false)
      expect(isLibraryEditorDirty()).toBe(true)
      expect(useUiStore.getState().activePanel).toEqual(DIRTY)

      await act(async () => {
        showLibraryOpenNotice('sess-1', {
          workspaceId: 'W',
          path: 'notes/plan.md',
          requestId: REQUEST,
        })
      })
      openNotice()
      expect(getDiscardConfirmDialogOpen()).toBe(true)
      resolveDiscardConfirmDialog(true)
      await waitFor(() => expect(useUiStore.getState().activePanel).toEqual({
        id: 'library',
        context: { workspaceId: 'W', path: 'notes/plan.md' },
      }))
      expect(isLibraryEditorDirty()).toBe(false)
      expect(useLibraryOpenStore.getState().notices['sess-1']).toBeUndefined()
    } finally {
      release()
    }
  })
})
