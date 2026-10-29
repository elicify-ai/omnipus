/**
 * Follow-up 2: a successful write of a workspace file is a Library link,
 * not only the file name. This is red until FileOpBlock renders that link.
 */
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ToolCall } from '@/lib/api'
import { registerChatView } from '@/store/chatView'
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

describe('write confirmation file row (follow-up 2)', () => {
  beforeEach(() => {
    useChatStore.setState({ toolCalls: {} })
    useUiStore.getState().closePanel()
    registerChatView('sess-write', 'W')
  })

  it('makes a workspace file name open that Library path', () => {
    const renderRow = captured.write_file
    expect(renderRow, 'write_file UI is registered').toBeTypeOf('function')
    const id = 'call-write'
    useChatStore.setState({
      toolCalls: {
        [id]: {
          id,
          call_id: id,
          tool: 'write_file',
          params: { path: 'notes/plan.md' },
          status: 'success',
        } as ToolCall & { call_id: string },
      },
    })
    render(renderRow({
      toolCallId: id,
      args: { path: 'notes/plan.md', content: 'plan' },
      result: 'wrote',
      status: { type: 'complete' },
    }))
    const link = screen.getByRole('link', { name: 'plan.md' })
    expect(link).toHaveAttribute('href', '/#/library?workspace=W&path=notes%2Fplan.md')
    fireEvent.click(link)
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'W', path: 'notes/plan.md' },
    })
  })
})
