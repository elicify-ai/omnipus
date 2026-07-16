/**
 * WebServeUI tests — FR-008 / FR-008a / FR-013 / FR-015 / FR-019
 *
 * Coverage:
 *   - kind="static" result: Globe icon, path label, no warmup
 *   - kind="dev" result: Terminal icon, command, port chip, warmup state
 *   - back-compat aliases: ServeWorkspaceUI and RunInWorkspaceUI render correctly
 *   - module exports are defined (AssistantUI tool components)
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WebServeBlock } from './WebServeUI'

// ── Top-level mocks ───────────────────────────────────────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

vi.mock('@tanstack/react-query', () => ({
  useQuery: () => ({
    data: {
      preview_port: 5001,
      preview_listener_enabled: true,
      warmup_timeout_seconds: 60,
    },
    isLoading: false,
  }),
}))

vi.mock('@/lib/api', () => ({
  fetchAboutInfo: vi.fn().mockResolvedValue({}),
}))

vi.mock('@assistant-ui/react', () => ({
  makeAssistantToolUI: (config: Record<string, unknown>) => config,
}))

// ── Setup ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:' },
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
          url: '/preview/jim/tok/',
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

    // No warmup placeholder — static mode mounts immediately
    expect(screen.queryByText(/starting preview/i)).toBeNull()
    expect(screen.queryByText(/starting dev server/i)).toBeNull()
  })

  it('renders iframe using path field from result (back-compat /serve/ path)', () => {
    // Retains legacy /serve/ coverage — back-compat shim path must still work.
    render(
      <WebServeBlock
        args={{}}
        result={{
          kind: 'static',
          url: '/serve/jim/tok/',
          path: '/serve/jim/tok/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const src = iframes[0].getAttribute('src') ?? ''
    expect(src).toContain('/serve/jim/tok/')
  })

  it('renders iframe using path field from result (canonical /preview/ path)', () => {
    render(
      <WebServeBlock
        args={{}}
        result={{
          kind: 'static',
          url: '/preview/jim/tok/',
          path: '/preview/jim/tok/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const src = iframes[0].getAttribute('src') ?? ''
    expect(src).toContain('/preview/jim/tok/')
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
          url: '/preview/jim/tok/',
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
  })

  it('shows warmup state machine (aria-live region) while warming up (back-compat /dev/ path)', () => {
    // Retains legacy /dev/ coverage — back-compat shim path must still work.
    render(
      <WebServeBlock
        args={{ command: 'vite dev', port: 18000 }}
        result={{
          kind: 'dev',
          url: '/dev/jim/tok/',
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
  })

  it('infers dev mode from command + port when kind is absent (back-compat /preview/ path)', () => {
    render(
      <WebServeBlock
        args={{}}
        result={{
          // No `kind` field — legacy run_in_workspace transcript shape using new canonical path
          url: '/preview/agent-1/tok/',
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

  it('renders normally for a valid WebServeResult shape', () => {
    render(
      <WebServeBlock
        args={{}}
        result={{
          kind: 'static',
          url: '/preview/jim/tok/',
          path: '/preview/jim/tok/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
        isRunning={false}
        toolName="web_serve"
      />
    )

    // No malformed block
    expect(screen.queryByText(/web_serve tool returned a malformed result/i)).toBeNull()

    // Normal iframe present
    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
  })
})

// ── flat text-line status dot (ticket "Tool components in chat", P2) ────────
// WebServeUI itself consumes PreviewToolHeader (a sibling's file, left
// untouched) for the normal header — the only frame WebServeUI.tsx owns is
// the malformed-result block, which loses its rounded/bordered/backgrounded
// error card in favor of a border-l-2 left accent (same language as
// GenericToolCall.tsx's marshal-error/delegation-denied banners).

describe('WebServeUI — malformed result block flat styling', () => {
  it('uses a left accent line, not a rounded/bordered/backgrounded card', () => {
    render(
      <WebServeBlock
        args={{}}
        result={{ unexpected_field: 'some_value' }}
        isRunning={false}
        toolName="web_serve"
      />
    )
    const block = screen.getByTestId('webserve-malformed-block')
    expect(block.className).toContain('border-l-2')
    expect(block.className).not.toContain('rounded-md')
    expect(block.className).not.toContain('bg-[var(--color-error)]/5')
  })

  it('the raw-result <pre> has no rounded/backgrounded box', () => {
    render(
      <WebServeBlock
        args={{}}
        result={{ unexpected_field: 'some_value' }}
        isRunning={false}
        toolName="web_serve"
      />
    )
    const pre = document.querySelector('pre')
    expect(pre).toBeTruthy()
    expect(pre?.className).not.toContain('rounded')
    expect(pre?.className).not.toContain('bg-[var(--color-surface-2)]')
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
