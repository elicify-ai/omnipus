import { act, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'
import type { PanelContentProps, PanelContext } from '@/components/panel-shell/types'

const browserLiveViewProps = vi.fn()
vi.mock('./BrowserLiveView', () => ({
  BrowserLiveView: (props: Record<string, unknown>) => {
    browserLiveViewProps(props)
    return <div data-testid="mock-browser-live-view" />
  },
}))

import { BrowserLivePanel } from './BrowserLivePanel'

let currentContext: (() => PanelContext) | null = null

function shellProps(
  presentation: PanelContentProps['presentation'],
  context = { sessionId: 'session-1', agentId: 'agent-1' },
): PanelContentProps {
  return {
    context,
    presentation,
    close: vi.fn(),
    expand: vi.fn(),
    registerExpandContext: (getter) => {
      currentContext = getter
    },
    onWidthSettle: vi.fn(),
  }
}

beforeEach(() => {
  currentContext = null
  browserLiveViewProps.mockClear()
  useUiStore.setState({ activePanel: null, toasts: [] })
})

describe('BrowserLivePanel shared presentation', () => {
  it('renders nothing without a Browser context', () => {
    render(<BrowserLivePanel />)
    expect(screen.queryByTestId('mock-browser-live-view')).not.toBeInTheDocument()
  })

  it('renders the store-owned dock and keeps annotation in chat', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openPanel('browser', { sessionId: 'session-1', agentId: 'agent-1' })
    })

    expect(screen.getByTestId('browser-live-panel-docked').tagName).toBe('ASIDE')
    expect(browserLiveViewProps).toHaveBeenLastCalledWith(expect.objectContaining({
      sessionId: 'session-1',
      agentId: 'agent-1',
      canAnnotate: true,
      fillContainer: true,
    }))
  })

  it('reports its owner context and renders the same viewer full screen without chat annotation', () => {
    const context = { sessionId: 'session-1', agentId: 'agent-1' }
    render(<BrowserLivePanel shellProps={shellProps('fullscreen', context)} />)

    expect(screen.getByTestId('browser-live-panel-fullscreen')).toBeInTheDocument()
    expect(currentContext?.()).toEqual(context)
    expect(browserLiveViewProps).toHaveBeenLastCalledWith(expect.objectContaining({
      sessionId: 'session-1',
      agentId: 'agent-1',
      canAnnotate: false,
    }))
  })

  it('does not pass retired ownership or per-panel pop-out actions to the viewer', () => {
    render(<BrowserLivePanel shellProps={shellProps('fullscreen')} />)
    const props = browserLiveViewProps.mock.calls.at(-1)?.[0] as Record<string, unknown>
    expect(props).not.toHaveProperty('onPopOut')
    expect(props).not.toHaveProperty('onHandToAgent')
    expect(props).not.toHaveProperty('isPinned')
    expect(props).not.toHaveProperty('onTogglePin')
  })
})
