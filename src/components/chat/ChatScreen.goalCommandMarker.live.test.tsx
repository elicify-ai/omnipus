/**
 * ChatScreen.goalCommandMarker.live.test.tsx
 *
 * UAT defect B, rendering half — the LIVE path.
 *
 * The goal marker is rendered at two sites that must stay in step
 * (ChatScreen.tsx says so in both their comments): `UserMessage`, the live
 * AssistantUI path, and `VirtualUserMessageRow`, the historical/reload path.
 * ChatScreen.goalCommandMarker.test.tsx drives only the row — so the live
 * site, which extracts its text DIFFERENTLY (the first `text` part of
 * AssistantUI's `message.content` parts array, rather than a store
 * `ChatMessage.content` string), had no coverage at all: a change that broke
 * the marker there would have left the whole suite green.
 *
 * This file closes that hole and pins the parity itself: the same inputs are
 * put through BOTH components and their `data-goal-command` verdicts are
 * compared, so the two sites cannot silently diverge in either direction.
 *
 * '@assistant-ui/react' is mocked (the established pattern in this
 * directory — see ChatScreen.browser-handover-notice.test.tsx): the live
 * component needs a message context and primitives that exist only inside a
 * runtime provider. The mock is deliberately thin and pass-through —
 * `MessagePrimitive.Root` forwards every prop (so the real `data-testid` /
 * `data-goal-command` attributes the component sets are the ones asserted
 * on) and `MessagePrimitive.Parts` walks the same parts array the component
 * reads, so the extraction under test is the real one.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import * as React from 'react'
import { UserMessage, VirtualUserMessageRow } from './ChatScreen'

/** The message AssistantUI's `useMessage()` hands the live component. */
type LiveMessage = {
  id: string
  role: string
  content: { type: string; text?: string }[]
}

let liveMessage: LiveMessage = { id: 'msg-0', role: 'user', content: [] }

vi.mock('@assistant-ui/react', () => {
  const passthrough =
    (tag: string) =>
    ({ children, ...rest }: React.PropsWithChildren<Record<string, unknown>>) =>
      React.createElement(tag, rest as React.HTMLAttributes<HTMLElement>, children)

  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: passthrough('div'),
      Viewport: React.forwardRef((props: React.PropsWithChildren<Record<string, unknown>>, ref: React.Ref<HTMLDivElement>) => {
        const { children, ...rest } = props
        return React.createElement('div', { ...(rest as React.HTMLAttributes<HTMLDivElement>), ref }, children)
      }),
      Messages: () => null,
    },
    MessagePrimitive: {
      Root: passthrough('div'),
      // Walks the SAME parts array the component's own extraction reads, so
      // the bubble renders whatever a real text part would render.
      Parts: ({ children }: { children: (p: { part: { type: string; text?: string } }) => React.ReactNode }) =>
        React.createElement(
          React.Fragment,
          null,
          ...liveMessage.content.map((part, i) =>
            React.createElement(React.Fragment, { key: i }, children({ part })),
          ),
        ),
    },
    ComposerPrimitive: {
      Root: passthrough('div'),
      Input: passthrough('textarea'),
      Send: passthrough('button'),
      AddAttachment: passthrough('button'),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: passthrough('div'),
      Name: () => null,
      Remove: passthrough('button'),
      Thumb: () => null,
    },
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: { Root: passthrough('div'), Copy: passthrough('span') },
    AuiIf: () => null,
    useComposerRuntime: () => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    }),
    useMessage: () => liveMessage,
    useAttachment: vi.fn(() => ({
      id: 'att-default',
      name: 'file.txt',
      contentType: 'text/plain',
      file: undefined,
      status: { type: 'complete' },
      content: [],
    })),
    makeAssistantToolUI: () => () => null,
  }
})

// The live component fetches skills + slash commands; neither is under test
// here (an empty list is the shape that leaves the plain-text bubble alone).
vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

/** Renders the LIVE component for a message whose single text part is `text`. */
function renderLive(text: string) {
  liveMessage = {
    id: `msg-${Math.random().toString(36).slice(2)}`,
    role: 'user',
    content: [{ type: 'text', text }],
  }
  return render(<UserMessage />)
}

/** Renders the live component for a hand-built parts array. */
function renderLiveParts(content: LiveMessage['content']) {
  liveMessage = { id: 'msg-parts', role: 'user', content }
  return render(<UserMessage />)
}

describe('UserMessage (live path) — a /goal message is recognisable as a goal', () => {
  it('marks a goal-setting message, so the live thread shows the same trace as a reloaded one', () => {
    renderLive('/goal ship the release with a written changelog')

    expect(screen.getByTestId('goal-command-marker')).toBeInTheDocument()
    expect(screen.getByTestId('user-message')).toHaveAttribute('data-goal-command', 'true')
  })

  it('still shows the intent the user typed — the marker does not replace the message', () => {
    const { container } = renderLive('/goal ship the release')

    expect(container.textContent).toContain('ship the release')
  })

  it('marks the "!" prefix the server also accepts', () => {
    renderLive('!goal ship the release')

    expect(screen.getByTestId('goal-command-marker')).toBeInTheDocument()
    expect(screen.getByTestId('user-message')).toHaveAttribute('data-goal-command', 'true')
  })

  it('claims nothing about goal STATE — the marker is not a record card or a pill', () => {
    renderLive('/goal ship the release')

    expect(screen.queryByTestId('goal-echo-card')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-pill-tray')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-ack-line')).not.toBeInTheDocument()
  })

  it('leaves an ordinary chat message unmarked', () => {
    renderLive('ship the release')

    expect(screen.queryByTestId('goal-command-marker')).not.toBeInTheDocument()
    expect(screen.getByTestId('user-message')).not.toHaveAttribute('data-goal-command')
  })

  it('leaves a goal CLEAR command and a bare /goal unmarked', () => {
    const clear = renderLive('/goal clear')
    expect(screen.queryByTestId('goal-command-marker')).not.toBeInTheDocument()
    clear.unmount()

    renderLive('/goal')
    expect(screen.queryByTestId('goal-command-marker')).not.toBeInTheDocument()
  })

  // The live extraction is `parts.find(p => p.type === 'text')` — NOT
  // `parts[0]`. A message that leads with a non-text part (an image the user
  // attached) must still be marked from its text part.
  it('reads the first TEXT part, not the first part, when the message leads with an attachment', () => {
    renderLiveParts([
      { type: 'image' },
      { type: 'text', text: '/goal ship the release' },
    ])

    expect(screen.getByTestId('goal-command-marker')).toBeInTheDocument()
    expect(screen.getByTestId('user-message')).toHaveAttribute('data-goal-command', 'true')
  })

  it('is unmarked when the message carries no text part at all', () => {
    renderLiveParts([{ type: 'image' }])

    expect(screen.queryByTestId('goal-command-marker')).not.toBeInTheDocument()
    expect(screen.getByTestId('user-message')).not.toHaveAttribute('data-goal-command')
  })
})

// ── The two sites must agree ─────────────────────────────────────────────────
// ChatScreen.tsx's own comments say the live component and the virtualized
// row are "the other half of the same rendering". This pins that: one input
// set, both components, identical verdicts — so neither site can drift
// without a failure here, whichever one is changed.

describe('the live path and the reload path mark exactly the same messages', () => {
  const CASES: [string, boolean][] = [
    ['/goal ship the release', true],
    ['!goal ship the release', true],
    ['/goal@omnipus ship the release', true],
    ['/GOAL ship the release', true],
    ['/goal stop the build', true],
    ['/goal', false],
    ['/goal clear', false],
    ['/goal confirm', false],
    ['ship the release', false],
    ['/plan ship the release', false],
  ]

  it.each(CASES)('agrees on %j', (content, expected) => {
    const live = renderLive(content)
    const liveMarked = screen.getByTestId('user-message').getAttribute('data-goal-command')
    const liveHasMarker = screen.queryByTestId('goal-command-marker') !== null
    live.unmount()

    render(
      <VirtualUserMessageRow
        message={
          {
            id: 'row-1',
            role: 'user',
            content,
            timestamp: new Date().toISOString(),
            status: 'done',
          } as never
        }
        skills={[]}
        commandLabels={['/goal']}
      />,
    )
    const rowMarked = screen.getByTestId('user-message').getAttribute('data-goal-command')
    const rowHasMarker = screen.queryByTestId('goal-command-marker') !== null

    expect(liveMarked).toBe(expected ? 'true' : null)
    expect(rowMarked).toBe(liveMarked)
    expect(liveHasMarker).toBe(expected)
    expect(rowHasMarker).toBe(liveHasMarker)
  })
})
