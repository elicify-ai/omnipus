/**
 * T7 / BS-5.1: library_ref travels frame → store → the real assistant row,
 * live (InlineMedia) and after the same frame is applied as history.
 * Mounting AssistantMediaItem directly is not this test.
 */
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from '@/store/chat'
import { emptySessionState } from '@/store/chat/session'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

const live = vi.hoisted(() => ({ id: '' }))

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  useMessage: () => ({ id: live.id, role: 'assistant', status: { type: 'running' }, content: [] }),
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: () => null,
}))

vi.mock('./markdown-text', () => ({
  MarkdownText: () => null,
}))

import { AssistantMessage, VirtualAssistantMessageRow } from './ChatScreen'

const SID = 'sess-media'

function deliver(kind: 'file' | 'image', url: string, filename: string, path: string) {
  useSessionStore.getState().setActiveSession(SID, 'mia')
  useChatStore.setState({
    sessionsById: { [SID]: emptySessionState() },
    messages: [],
    toolCalls: {},
  })
  useChatStore.getState().handleFrame({
    type: 'media',
    session_id: SID,
    parts: [{
      type: kind,
      url,
      filename,
      content_type: kind === 'image' ? 'image/png' : 'text/markdown',
      library_ref: { workspace_id: 'W', path },
    }],
  } as never)
  const message = useChatStore.getState().messages.find((item) => item.media?.length)
  expect(message?.media?.[0]?.libraryRef, 'reducer copy').toEqual({ workspace_id: 'W', path })
  expect(message?.media?.[0]?.url).toBe(url)
  return message!
}

describe('library_ref reaches the real assistant row (BS-5.1)', () => {
  beforeEach(() => {
    useUiStore.getState().closePanel()
    live.id = ''
  })

  it.each([
    ['file', '/api/v1/media/plan.md', 'plan.md', 'notes/plan.md'],
    ['image', '/api/v1/media/pic.png', 'pic.png', 'pics/pic.png'],
  ] as const)('shows the %s download address and Library target live and from history', (kind, url, filename, path) => {
    // jsdom fires error for every image src and ChatImage then replaces the
    // element. Keep the address on the img so the assertion can see it.
    const src = vi.spyOn(HTMLImageElement.prototype, 'src', 'set').mockImplementation(function (this: HTMLImageElement, value: string) {
      this.setAttribute('src', value)
    })
    const message = deliver(kind, url, filename, path)
    live.id = message.id
    const { unmount } = render(<AssistantMessage />)
    expectDownload(kind, url, filename)
    unmount()

    render(
      <VirtualAssistantMessageRow
        message={{ ...message, isStreaming: false }}
        liteMode={false}
        delegationEvents={[]}
        pairedReplyToIds={new Set()}
      />,
    )
    expectDownload(kind, url, filename)
    src.mockRestore()
  })
})

function expectDownload(kind: 'file' | 'image', url: string, filename: string) {
  expect(screen.getByRole('button', { name: `Open ${filename} in Library` })).toBeInTheDocument()
  if (kind === 'file') {
    expect(screen.getByRole('link', { name: filename })).toHaveAttribute('href', url)
    return
  }
  const image = document.querySelector(`img[src="${url}"]`)
  expect(image, 'image address').not.toBeNull()
  expect(image).toHaveAttribute('aria-label', `View: ${filename}`)
}
