/**
 * Follow-up 2, pinned to the spec rather than to the tool's path argument.
 *
 * FR-011: write_file adds a `link:` line only when the server resolver says
 * the file is in a Library, and only on web chat. US-4 AS-4 / R4: a write
 * with no `link:` line is not a Library file, so it must not become a link.
 * BS-4.4: the turn's working directory is not a Library root, so a relative
 * tool path is not a workspace path. The server `link:` line is the only
 * authority for this row. BS-7.1's "links only from link: lines or relative
 * paths" is the agent's chat-text rule, not a second resolver for tool args.
 */
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ToolCall } from '@/lib/api'
import {
  getDiscardConfirmDialogOpen,
  isLibraryEditorDirty,
  resolveDiscardConfirmDialog,
  setLibraryEditorDirty,
} from '@/components/library/preview/unsavedGuard'
import { registerChatView, useChatViewStore } from '@/store/chatView'
import { useChatStore } from '@/store/chat'
import { useUiStore } from '@/store/ui'

type RenderFn = (props: {
  toolCallId?: string
  args: { path?: string; content?: string }
  result: unknown
  status: { type: string }
}) => React.ReactNode

const captured = vi.hoisted((): Record<string, RenderFn> => ({}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: { toolName?: string; render?: RenderFn }) => {
      if (config.toolName && config.render) captured[config.toolName] = config.render
      return config
    },
  }
})

import { FileWriteConfirmUI } from './FileWriteConfirm'

void FileWriteConfirmUI

const DIRTY = { id: 'library' as const, context: { workspaceId: 'W', path: 'notes/dirty.md' } }
const SERVER = { id: 'library' as const, context: { workspaceId: 'W', path: 'notes/plan.md' } }

function seedWrite(result: unknown, path = 'scratch/plan.md') {
  const id = 'call-write'
  useChatStore.setState({
    toolCalls: {
      [id]: {
        id,
        call_id: id,
        tool: 'write_file',
        params: { path },
        status: 'success',
      } as ToolCall & { call_id: string },
    },
  })
  const renderRow = captured.write_file
  expect(renderRow, 'write_file UI is registered').toBeTypeOf('function')
  render(renderRow({
    toolCallId: id,
    args: { path, content: 'plan' },
    result,
    status: { type: 'complete' },
  }))
}

describe('write confirmation file row (follow-up 2 / FR-011)', () => {
  beforeEach(() => {
    if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
    setLibraryEditorDirty(false)
    useChatStore.setState({ toolCalls: {} })
    useChatViewStore.setState({ views: {} })
    useUiStore.getState().closePanel()
    registerChatView('sess-write', 'W')
  })

  it('opens the path named by the server link line, not the tool path', () => {
    seedWrite('Wrote scratch/plan.md.\nlink: omnipus://file/W/notes/plan.md')
    const link = screen.getByRole('link', { name: 'plan.md' })
    expect(link).toHaveAttribute('href', '/#/library?workspace=W&path=notes%2Fplan.md')
    fireEvent.click(link)
    expect(useUiStore.getState().activePanel).toEqual(SERVER)
  })

  it('does not invent a Library link when the result has no link line (FR-011 / R4)', () => {
    seedWrite('wrote scratch/plan.md')
    expect(screen.queryByRole('link', { name: 'plan.md' })).toBeNull()
    expect(screen.getByText('plan.md')).toBeInTheDocument()
    expect(useUiStore.getState().activePanel).toBeNull()
  })

  it('asks before leaving a dirty Library, and Discard opens the server path (FR-008)', async () => {
    useUiStore.getState().openPanel('library', DIRTY.context)
    setLibraryEditorDirty(true)
    seedWrite('Wrote it.\nlink: omnipus://file/W/notes/plan.md')
    const link = screen.getByRole('link', { name: 'plan.md' })

    fireEvent.click(link)
    expect(getDiscardConfirmDialogOpen()).toBe(true)
    expect(useUiStore.getState().activePanel).toEqual(DIRTY)

    resolveDiscardConfirmDialog(false)
    expect(getDiscardConfirmDialogOpen()).toBe(false)
    expect(isLibraryEditorDirty()).toBe(true)
    expect(useUiStore.getState().activePanel).toEqual(DIRTY)

    fireEvent.click(link)
    expect(getDiscardConfirmDialogOpen()).toBe(true)
    resolveDiscardConfirmDialog(true)
    await waitFor(() => expect(useUiStore.getState().activePanel).toEqual(SERVER))
    expect(isLibraryEditorDirty()).toBe(false)
  })
})
