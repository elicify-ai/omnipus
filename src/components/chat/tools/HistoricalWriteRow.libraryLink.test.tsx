/**
 * C2 / R2-D1: a finished reply keeps the write-row Library link.
 *
 * FR-011 / US-4 AS-4: the server `link:` line is the only Library address.
 * The completed row is VirtualAssistantMessageRow, including after reload
 * when the live tool-call store is empty. A result with no `link:` line
 * is not a link (R4). The click goes through the real leave gate (FR-008).
 */
import type { ReactNode } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ToolCall } from '@/lib/api'
import { useChatStore } from '@/store/chat'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { registerChatView } from '@/store/chatView'
import { useUiStore } from '@/store/ui'

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: ReactNode }) => children,
}))

vi.mock('../historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) => <div>{content}</div>,
}))

vi.mock('../markdown-text', () => ({
  MarkdownText: () => null,
}))

import { VirtualAssistantMessageRow } from '../ChatScreen'

const SID = 'sess-hist'
const HREF = '/#/library?workspace=W&path=notes%2Fplan.md'
const LINKED = 'Wrote it.\nlink: omnipus://file/W/notes/plan.md'

function message(tool: string, result: unknown): ChatMessage {
  const call = {
    id: `call-${tool}`,
    tool,
    params: { path: 'scratch/plan.md', content: 'plan' },
    result,
    status: 'success',
  } as PositionedToolCall
  return {
    id: `msg-${tool}`,
    role: 'assistant',
    content: 'Done.',
    timestamp: '2026-10-29T00:00:00.000Z',
    status: 'done',
    tool_calls: [call],
  }
}

function renderFinished(tool: string, result: unknown, live: boolean) {
  const msg = message(tool, result)
  const call = msg.tool_calls?.[0] as ToolCall
  useChatStore.setState({
    messages: [msg],
    toolCalls: live && call ? { [call.id]: { ...call, call_id: call.id } } : {},
    toolCallOrder: live && call ? [call.id] : [],
    textAtToolCallStart: {},
  })
  return render(
    <VirtualAssistantMessageRow
      message={msg}
      liteMode={false}
      delegationEvents={[]}
      pairedReplyToIds={new Set()}
    />,
  )
}

function expectPlanLink() {
  const link = screen.getByRole('link', { name: 'plan.md' })
  expect(link).toHaveAttribute('href', HREF)
  fireEvent.click(link)
  expect(useUiStore.getState().activePanel).toEqual({
    id: 'library',
    context: { workspaceId: 'W', path: 'notes/plan.md' },
  })
  return link
}

describe('finished write rows keep the server Library link (FR-011)', () => {
  beforeEach(() => {
    useSessionStore.getState().setActiveSession(SID, 'mia')
    useUiStore.getState().closePanel()
    registerChatView(SID, 'W')
    useChatStore.setState({ messages: [], toolCalls: {}, toolCallOrder: [] })
  })

  it.each(['write_file', 'edit_file', 'append_file'])(
    'keeps %s clickable from a string result, then from the persisted reload object',
    (tool) => {
      const live = renderFinished(tool, LINKED, true)
      expectPlanLink()
      live.unmount()
      useUiStore.getState().closePanel()
      useChatStore.setState({ toolCalls: {}, toolCallOrder: [] })

      // Reload replays the stored result map. truncateResult returns that map
      // unchanged when it fits inline (pkg/gateway/replay.go). The settled
      // shape is { text }, not the live string.
      renderFinished(tool, { text: LINKED }, false)
      expectPlanLink()
    },
  )

  it('does not invent a link when the finished result has no link line (R4)', () => {
    renderFinished('write_file', 'wrote scratch/plan.md', false)
    expect(screen.queryByRole('link', { name: 'plan.md' })).toBeNull()
    expect(document.querySelector('a[href*="/#/library"]')).toBeNull()
  })

  it('does not invent a link when the persisted object has no link line (R4)', () => {
    renderFinished('write_file', { text: 'wrote scratch/plan.md' }, false)
    expect(screen.queryByRole('link', { name: 'plan.md' })).toBeNull()
    expect(document.querySelector('a[href*="/#/library"]')).toBeNull()
  })
})
