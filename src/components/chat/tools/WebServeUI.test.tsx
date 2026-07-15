/**
 * WebServeUI tests — FR-008 / FR-008a / FR-013 / FR-015 / FR-019
 * Also: docs/internal/specs/preview-on-main-listener-spec.md FR-016/FR-017 (US-9)
 *
 * Coverage:
 *   - kind="static" result: Globe icon, path label, renders a preview link (no iframe)
 *   - kind="dev" result: Terminal icon, command, port chip, warmup state, then a link
 *   - back-compat aliases: ServeWorkspaceUI and RunInWorkspaceUI render correctly
 *   - module exports are defined (AssistantUI tool components)
 *
 * IframePreview no longer reads /api/v1/about (ADR-044 — `/preview/` shares
 * the SPA's own origin), so there is no useQuery/fetchAboutInfo mock needed.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WebServeBlock } from './WebServeUI'

// ── Top-level mocks ───────────────────────────────────────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

vi.mock('@assistant-ui/react', () => ({
  makeAssistantToolUI: (config: Record<string, unknown>) => config,
}))

// ── Setup ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000', port: '5000' },
    writable: true,
  })
})

// ── kind="static" ─────────────────────────────────────────────────────────────

describe('WebServeUI — kind=static', () => {
  it('renders tool name chip and path label (canonical /preview/ path)', () => {
    render(
      <WebServeBlock
        args={{ path: 'elicify-hello' }}
        result={{
          kind: 'static',
          url: 'http://localhost:5000/preview/jim/tok/',
          path: 'elicify-hello',
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    // Tool name chip is present
    expect(screen.getByText('web_serve')).toBeInTheDocument()

    // Path label chip is present
    expect(screen.getByText('elicify-hello')).toBeInTheDocument()

    // No warmup placeholder — static mode renders the link immediately
    expect(screen.queryByText(/starting preview/i)).toBeNull()
    expect(screen.queryByText(/starting dev server/i)).toBeNull()

    // No iframe — link-only per US-9/FR-016
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('renders a preview link using the path field (back-compat /serve/ path)', () => {
    // Retains legacy /serve/ coverage — back-compat shim path must still work.
    render(
      <WebServeBlock
        args={{}}
        result={{
          kind: 'static',
          url: 'http://localhost:5000/serve/jim/tok/',
          path: '/serve/jim/tok/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    expect(document.querySelectorAll('iframe').length).toBe(0)
    const link = screen.getByTestId('preview-link')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/serve/jim/tok/')
  })

  it('renders a preview link using the path field (canonical /preview/ path)', () => {
    render(
      <WebServeBlock
        args={{}}
        result={{
          kind: 'static',
          url: 'http://localhost:5000/preview/jim/tok/',
          path: '/preview/jim/tok/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    expect(document.querySelectorAll('iframe').length).toBe(0)
    const link = screen.getByTestId('preview-link')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/preview/jim/tok/')
  })

  it('shows "Waiting for" when result is null (tool still running)', () => {
    render(
      <WebServeBlock
        args={{}}
        result={null}
        isRunning={true}
        toolName="web_serve"
      />
    )
    expect(screen.getByText(/waiting for/i)).toBeInTheDocument()
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })
})

// ── kind="dev" ────────────────────────────────────────────────────────────────

describe('WebServeUI — kind=dev', () => {
  it('renders Terminal icon, command label, and port chip (canonical /preview/ path)', () => {
    render(
      <WebServeBlock
        args={{ command: 'vite dev', port: 18000 }}
        result={{
          kind: 'dev',
          url: 'http://localhost:5000/preview/jim/tok/',
          path: '/preview/jim/tok/',
          command: 'vite dev',
          port: 18000,
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    // Tool name chip
    expect(screen.getByText('web_serve')).toBeInTheDocument()

    // Command label chip
    expect(screen.getByText('vite dev')).toBeInTheDocument()

    // Port chip
    expect(screen.getByText(':18000')).toBeInTheDocument()

    // No iframe — link-only per US-9/FR-016
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('shows warmup state machine (aria-live region) while warming up (back-compat /dev/ path)', () => {
    // Retains legacy /dev/ coverage — back-compat shim path must still work.
    // fetch is left unmocked here (jsdom has no real dev server) — the probe
    // simply fails/hangs, keeping the component in the warmup phase, which is
    // exactly what this test wants to observe.
    render(
      <WebServeBlock
        args={{ command: 'vite dev', port: 18000 }}
        result={{
          kind: 'dev',
          url: 'http://localhost:5000/dev/jim/tok/',
          path: '/dev/jim/tok/',
          command: 'vite dev',
          port: 18000,
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    // IframePreview renders for dev mode — warmup state machine starts ('starting' then 'probing')
    // The aria-live region is present in the DOM during warmup
    const liveRegion = document.querySelector('[aria-live="polite"]')
    expect(liveRegion).not.toBeNull()
    // Still no iframe even during warmup — HEAD-poll based, not iframe-probe based.
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('infers dev mode from command + port when kind is absent (back-compat /preview/ path)', () => {
    render(
      <WebServeBlock
        args={{}}
        result={{
          // No `kind` field — legacy run_in_workspace transcript shape using new canonical path
          url: 'http://localhost:5000/preview/agent-1/tok/',
          path: '/preview/agent-1/tok/',
          command: 'npm run dev',
          port: 3000,
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="run_in_workspace"
      />
    )

    expect(screen.getByText('npm run dev')).toBeInTheDocument()
    expect(screen.getByText(':3000')).toBeInTheDocument()
  })
})

// ── Back-compat aliases ───────────────────────────────────────────────────────

describe('WebServeUI — back-compat alias: ServeWorkspaceUI', () => {
  it('exports ServeWorkspaceUI as a defined AssistantUI tool component', async () => {
    const mod = await import('./ServeWorkspaceUI')
    expect(mod.ServeWorkspaceUI).toBeDefined()
  })
})

describe('WebServeUI — back-compat alias: RunInWorkspaceUI', () => {
  it('exports RunInWorkspaceUI as a defined AssistantUI tool component', async () => {
    const mod = await import('./RunInWorkspaceUI')
    expect(mod.RunInWorkspaceUI).toBeDefined()
  })
})

// ── B1.3(e) — Malformed result block ─────────────────────────────────────────

describe('WebServeUI — malformed result block (B1.3e)', () => {
  // Traces to: B1.3(e) security hardening
  // When isWebServeResult rejects the tool result (unexpected shape from replay
  // of old transcript or corrupted data), the component must render an inline
  // "tool returned malformed result" block with raw JSON, without crashing the
  // rest of the chat thread.

  it('renders malformed result block when result is not null/undefined and isRunning is false', () => {
    render(
      <WebServeBlock
        args={{}}
        result={{ unexpected_field: 'some_value', nested: { a: 1 } }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    // The malformed result notice must be present
    expect(screen.getByText(/web_serve tool returned a malformed result/i)).toBeInTheDocument()

    // The raw JSON must be in a details element
    expect(screen.getByText(/show raw result/i)).toBeInTheDocument()

    // No iframe rendered — the malformed block replaces it
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('renders malformed result block for a string result (not an object)', () => {
    render(
      <WebServeBlock
        args={{}}
        result="this is not an object"
        isRunning={false}
        toolName="web_serve"
      />
    )

    expect(screen.getByText(/web_serve tool returned a malformed result/i)).toBeInTheDocument()
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('does NOT render malformed block when result is null and isRunning is true', () => {
    // null result while running is normal (tool not done yet)
    render(
      <WebServeBlock
        args={{}}
        result={null}
        isRunning={true}
        toolName="web_serve"
      />
    )

    expect(screen.queryByText(/web_serve tool returned a malformed result/i)).toBeNull()
    // Shows waiting state instead
    expect(screen.getByText(/waiting for/i)).toBeInTheDocument()
  })

  it('does NOT render malformed block when result is null and isRunning is false', () => {
    // null + not running is the "failed / no result" path — normal handling
    render(
      <WebServeBlock
        args={{}}
        result={null}
        isRunning={false}
        toolName="web_serve"
      />
    )

    expect(screen.queryByText(/web_serve tool returned a malformed result/i)).toBeNull()
  })

  it('renders normally (a link, no malformed block) for a valid WebServeResult shape', () => {
    render(
      <WebServeBlock
        args={{}}
        result={{
          kind: 'static',
          url: 'http://localhost:5000/preview/jim/tok/',
          path: '/preview/jim/tok/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    // No malformed block
    expect(screen.queryByText(/web_serve tool returned a malformed result/i)).toBeNull()

    // A preview link is present, not an iframe.
    expect(document.querySelectorAll('iframe').length).toBe(0)
    expect(screen.getByTestId('preview-link')).toBeInTheDocument()
  })
})

// ── Module exports ─────────────────────────────────────────────────────────────

describe('WebServeUI — module exports', () => {
  it('exports WebServeUI, makeWebServeUI, and WebServeBlock', async () => {
    const mod = await import('./WebServeUI')
    expect(mod.WebServeUI).toBeDefined()
    expect(mod.makeWebServeUI).toBeDefined()
    expect(mod.WebServeBlock).toBeDefined()
  })
})
