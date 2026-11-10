/**
 * WebServeUI.isolated-url.test.tsx — RED test, ADR-094 TDD Plan order 26
 * (TestWebServeUIForwardsIsolatedUrl, round-2 MAJ-001).
 *
 * Traces to: docs/internal/specs/adr-094-preview-isolation-spec.md FR-022:
 * "The SPA forwarding hop MUST carry the field:
 * src/components/chat/tools/WebServeUI.tsx::WebServeResult gains
 * isolated_url? and its iframeResult construction forwards it into both
 * ServeWorkspaceIframeResult/RunInWorkspaceIframeResult prop variants ...
 * without this hop the card never receives the field."
 *
 * INSTRUMENT: IframePreview is mocked as a PROP-CAPTURE SEAM — the unit under
 * test is WebServeBlock's forwarding hop, not IframePreview itself (the
 * engine-gated selection behind that seam is orders 22/23's coverage).
 *
 * RED (current failure mode): the iframeResult construction in
 * WebServeUI.tsx builds its object literal WITHOUT isolated_url, so the field
 * is dropped at this hop and every forwarding assertion fails (captured
 * value undefined vs the planted string).
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { WebServeBlock } from './WebServeUI'

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

beforeEach(() => {
  iframePreviewSpy.mockClear()
})

const MODE2_URL = 'http://localhost:5000/preview/mia/tok-abc123/'

describe('WebServeBlock forwards isolated_url into IframePreview (order 26, FR-022)', () => {
  it('static variant: IframePreview receives isolated_url alongside path/url/expires_at', () => {
    render(
      <WebServeBlock
        args={{ path: 'elicify-hello' }}
        result={{
          kind: 'static',
          url: MODE2_URL,
          path: 'elicify-hello',
          expires_at: '2099-01-01T00:00:00Z',
          isolated_url: 'http://myapp.localhost:5000/',
        }}
        isRunning={false}
        toolName="web_serve"
        isError={undefined}
        isCancelled={undefined}
      />,
    )
    expect(iframePreviewSpy).toHaveBeenCalled()
    const captured = iframePreviewSpy.mock.calls[0][0] as {
      result: Record<string, unknown> | null
      kind: string
    }
    expect(captured.kind).toBe('serve_workspace')
    expect(captured.result, 'iframeResult must be built (hasPreviewShape must pass)').not.toBeNull()
    // The FR-022 forwarding assertion — whole shape, exact values:
    expect(captured.result).toMatchObject({
      path: 'elicify-hello',
      url: MODE2_URL,
      expires_at: '2099-01-01T00:00:00Z',
      isolated_url: 'http://myapp.localhost:5000/',
    })
  })

  it('dev variant: IframePreview receives isolated_url alongside command/port fields', () => {
    render(
      <WebServeBlock
        args={{ path: 'elicify-hello', command: 'vite dev', port: 5173 }}
        result={{
          kind: 'dev',
          url: MODE2_URL,
          path: 'elicify-hello',
          expires_at: '2099-01-01T00:00:00Z',
          command: 'vite dev',
          port: 5173,
          isolated_url: 'http://myapp.localhost:5173/',
        }}
        isRunning={false}
        toolName="web_serve"
        isError={undefined}
        isCancelled={undefined}
      />,
    )
    expect(iframePreviewSpy).toHaveBeenCalled()
    const captured = iframePreviewSpy.mock.calls[0][0] as {
      result: Record<string, unknown> | null
      kind: string
    }
    expect(captured.kind).toBe('run_in_workspace')
    expect(captured.result, 'iframeResult must be built (hasPreviewShape must pass)').not.toBeNull()
    expect(captured.result).toMatchObject({
      path: 'elicify-hello',
      url: MODE2_URL,
      expires_at: '2099-01-01T00:00:00Z',
      command: 'vite dev',
      port: 5173,
      isolated_url: 'http://myapp.localhost:5173/',
    })
  })

  it('control: absent isolated_url is forwarded as absent — never invented', () => {
    render(
      <WebServeBlock
        args={{ path: 'elicify-hello' }}
        result={{
          kind: 'static',
          url: MODE2_URL,
          path: 'elicify-hello',
          isolated_url: undefined,
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
        isError={undefined}
        isCancelled={undefined}
      />,
    )
    const captured = iframePreviewSpy.mock.calls[0][0] as {
      result: Record<string, unknown> | null
    }
    expect(captured.result).not.toBeNull()
    expect(
      Object.prototype.hasOwnProperty.call(captured.result, 'isolated_url'),
      'S-8.3: old transcripts must replay Mode 2-only — no invented field',
    ).toBe(false)
  })
})
