/**
 * WebServeUI.persisted-envelope.test.tsx — ADR-094 S-8.1 with the REAL result
 * shapes. serve_web returns its payload as a JSON string, so WebServeBlock
 * never receives the flat object from a real gateway:
 *   - live path: the `tool_call_result` frame carries the raw JSON STRING
 *     (pkg/gateway/websocket_forward_hub.go::hubToolExecEnd, `Result:
 *     p.Result` where p.Result is the tool's model-visible text);
 *   - reload path: the transcript records a string-returning tool's result as
 *     `{ text: "<json>" }` (pkg/agent/tool_result_transcript.go::
 *     projectRecordedResult), observed on a real gateway.
 *
 * Oracle: S-8.1 / FR-024 — a serve_web result carrying isolated_url renders
 * exactly one preview link whose href is the isolated_url on a
 * Mode-1-capable engine. The string / envelope is transport, not a malformed
 * result. Negative oracle (B1.3e): a result that is NOT a decodable payload —
 * a non-JSON `text`, or an envelope with any key besides `text` — still
 * renders the malformed block and no preview link.
 */

import type { ReactElement } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WebServeBlock, ServeWebUI } from './WebServeUI'

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

// makeAssistantToolUI returns its config, so ServeWebUI.render is the exact
// render function the live assistant-ui runtime calls for a serve_web part.
vi.mock('@assistant-ui/react', () => ({
  makeAssistantToolUI: (config: Record<string, unknown>) => config,
}))

const MODE2_URL = 'http://localhost:5000/preview/mia/tok-abc123/'
const ISOLATED_URL = 'http://myapp.localhost:5000/'

const ORIGINAL_UAD = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(navigator), 'userAgentData')

beforeEach(() => {
  Object.defineProperty(navigator, 'userAgentData', {
    configurable: true,
    value: { brands: [{ brand: 'Chromium', version: '130' }], mobile: false, platform: 'Linux' },
  })
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000', port: '5000' },
    writable: true,
  })
})

afterEach(() => {
  if (ORIGINAL_UAD) Object.defineProperty(Object.getPrototypeOf(navigator), 'userAgentData', ORIGINAL_UAD)
  else Reflect.deleteProperty(navigator, 'userAgentData')
})

function payload() {
  return {
    kind: 'static',
    path: '/preview/mia/tok-abc123/',
    url: MODE2_URL,
    expires_at: new Date(Date.now() + 3600_000).toISOString(),
    isolated_url: ISOLATED_URL,
  }
}

/** Reload path: HistoricalToolCallBlock renders WebServeBlock directly. */
function renderBlock(result: unknown) {
  render(
    <WebServeBlock
      args={{ path: 'e2e' }}
      result={result}
      isRunning={false}
      toolName="serve_web"
      isError={false}
      isCancelled={false}
    />,
  )
}

/** Live path: the registered serve_web tool UI's render, as the runtime calls it. */
function renderLive(result: unknown) {
  const liveRender = (ServeWebUI as unknown as {
    render: (props: { args: unknown; result: unknown; status: { type: string }; isError: boolean }) => ReactElement
  }).render
  render(liveRender({ args: { path: 'e2e' }, result, status: { type: 'complete' }, isError: false }))
}

function expectOneIsolatedLink() {
  expect(screen.queryByTestId('webserve-malformed-block')).toBeNull()
  const links = screen.getAllByTestId('preview-link')
  expect(links).toHaveLength(1)
  expect(links[0]).toHaveAttribute('href', ISOLATED_URL)
}

function expectMalformed() {
  expect(screen.getByTestId('webserve-malformed-block')).toBeInTheDocument()
  expect(screen.queryByTestId('preview-link')).toBeNull()
}

describe('WebServeBlock — real serve_web result shapes (S-8.1)', () => {
  it('control: the flat payload renders the Mode 1 link', () => {
    renderBlock(payload())
    expectOneIsolatedLink()
  })

  it('reload: the recorded {text: JSON} envelope renders the single Mode 1 link, not the malformed block', () => {
    renderBlock({ text: JSON.stringify(payload()) })
    expectOneIsolatedLink()
  })

  it('live: the raw JSON string from the tool_call_result frame renders the single Mode 1 link', () => {
    renderLive(JSON.stringify(payload()))
    expectOneIsolatedLink()
  })

  it('live: the registered serve_web render also unwraps the {text: JSON} envelope', () => {
    renderLive({ text: JSON.stringify(payload()) })
    expectOneIsolatedLink()
  })

  it('negative: {text: non-JSON} still renders the malformed block', () => {
    renderBlock({ text: 'not json' })
    expectMalformed()
  })

  it('negative: {text: JSON, other key} still renders the malformed block', () => {
    renderBlock({ text: JSON.stringify(payload()), extra: 1 })
    expectMalformed()
  })

  it('negative (live): a non-JSON string still renders the malformed block', () => {
    renderLive('not json')
    expectMalformed()
  })
})
