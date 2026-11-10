/**
 * HistoricalToolCallBlock.serve-web.test.tsx — replay routing for the
 * backend-canonical `serve_web` tool name (#950 review F3).
 *
 * Spec: docs/internal/specs/adr-094-preview-isolation-spec.md US-8 / S-8.x —
 * a saved serve_web call reopened from history renders the same preview card
 * (and forwards isolated_url to it) as the live call, never the generic badge.
 *
 * The merge of feat/798 into the release kept the release's extracted replay
 * renderer (renderHistoricalToolCall), which recognised only the legacy names
 * serve_workspace / run_in_workspace / web_serve. A canonical serve_web call
 * fell through to GenericToolCall.
 *
 * INSTRUMENT: IframePreview is a prop-capture seam — the unit under test is the
 * replay routing plus WebServeBlock's forwarding hop, not the Mode 1/Mode 2
 * link selection (covered by WebServeUI.serve-web.test.tsx and the
 * IframePreview tests).
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { ToolCall } from '@/lib/api'
import { renderHistoricalToolCall } from './HistoricalToolCallBlock'

const iframePreviewSpy = vi.hoisted(() => vi.fn())

vi.mock('../IframePreview', () => ({
  IframePreview: (props: unknown) => {
    iframePreviewSpy(props)
    return null
  },
}))

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

vi.mock('@assistant-ui/react', () => ({
  makeAssistantToolUI: (config: Record<string, unknown>) => config,
}))

const MODE2_URL = 'http://localhost:5000/preview/mia/tok-abc123/'
const ISOLATED_URL = 'http://myapp.localhost:5000/'

const SERVE_RESULT = {
  kind: 'static',
  url: MODE2_URL,
  path: 'elicify-hello',
  expires_at: '2099-01-01T00:00:00Z',
  isolated_url: ISOLATED_URL,
}

function savedCall(tool: string, status: ToolCall['status'] = 'success'): ToolCall {
  return {
    id: `tc_${tool}`,
    tool,
    params: { path: 'elicify-hello' },
    result: SERVE_RESULT,
    status,
  } as ToolCall
}

function renderReplay(tc: ToolCall): void {
  render(
    <>
      {renderHistoricalToolCall({
        tc,
        callId: tc.id,
        inlineEvents: undefined,
        activeSessionId: 'sess-replay',
        verboseChatEnabled: false,
        goalRecordEmpty: false,
        liteMode: false,
      })}
    </>,
  )
}

beforeEach(() => {
  iframePreviewSpy.mockClear()
})

function expectPreviewCard(toolName: string): void {
  expect(screen.getByTestId('webserve-tool-header')).toBeInTheDocument()
  expect(screen.getByText(toolName)).toBeInTheDocument()
  expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  expect(iframePreviewSpy).toHaveBeenCalled()
  const props = iframePreviewSpy.mock.calls[0][0] as { result: Record<string, unknown> | null }
  expect(props.result).toMatchObject({
    path: 'elicify-hello',
    url: MODE2_URL,
    expires_at: '2099-01-01T00:00:00Z',
    isolated_url: ISOLATED_URL,
  })
}

describe('renderHistoricalToolCall — serve_web replay (F3)', () => {
  it('a saved canonical serve_web call renders the WebServeBlock preview card with isolated_url, not the generic badge', () => {
    renderReplay(savedCall('serve_web'))
    expectPreviewCard('serve_web')
  })

  it('a saved serve_web call that failed renders the preview block in its error state, not the generic badge', () => {
    renderReplay({ ...savedCall('serve_web', 'error'), result: undefined, error: 'port in use' } as ToolCall)
    expect(screen.getByTestId('webserve-tool-header')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it.each(['web_serve', 'serve_workspace', 'run_in_workspace'])(
    'pins: a saved legacy %s call still renders the preview card',
    (tool) => {
      renderReplay(savedCall(tool))
      expectPreviewCard(tool)
    },
  )
})
