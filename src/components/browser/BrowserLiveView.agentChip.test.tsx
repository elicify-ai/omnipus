// BrowserLiveView.agentChip.test.tsx — ADR-043 D3 / US-6 coverage: the
// header's agent-identity chip must unambiguously label WHICH agent's browser
// context the panel is driving. With multiple agents browsing concurrently in
// isolated per-agent contexts, the chip is the persistent identity anchor
// (distinct from the drive-status chip, which shows who is currently driving).
//
// Mocks BrowserLiveWsConnection the same way the sibling BrowserLiveView test
// files do (no real WS); the agents query cache is seeded directly on the
// shared `queryClient` singleton the component reads from
// (`queryClient.getQueryData<Agent[]>(['agents'])`).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { queryClient } from '@/lib/queryClient'
import type { Agent } from '@/lib/api'

const { callbacksRef } = vi.hoisted(() => ({
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
}))

// D5: importOriginal so the real translateBrowserErrorMessage (now imported
// by BrowserLiveView for the D5 fix) stays live under this mock — only
// BrowserLiveWsConnection itself is replaced.
vi.mock('@/lib/browserLiveWs', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/browserLiveWs')>()
  return {
    ...actual,
    BrowserLiveWsConnection: vi.fn().mockImplementation(
      function (_sessionId: string, _agentId: string, callbacks: BrowserLiveWsCallbacks) {
        callbacksRef.current = callbacks
        return {
          connect: vi.fn(),
          detach: vi.fn(),
          close: vi.fn(),
          sendInput: vi.fn(),
          sendControl: vi.fn(() => true),
          sendTabAction: vi.fn(() => true),
          // Adaptive viewport (2026-07-31): BrowserLiveView's ResizeObserver
          // calls this on mount, so every connection double needs it.
          sendViewport: vi.fn(() => true),
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

/** Two agents seeded so the chip is exercised in the multi-agent case the ADR
 * is about: Jim has an icon, Ray has only a colour (exercises the initial
 * fallback). Both are valid concurrent browser-using agents per ADR-043. */
function seedAgents() {
  const agents = [
    { id: 'jim', name: 'Jim', color: '#D4AF37', icon: 'compass' },
    { id: 'ray', name: 'Ray', color: '#3B82F6', icon: undefined },
  ] as unknown as Agent[]
  queryClient.setQueryData(['agents'], agents)
}

beforeEach(() => {
  callbacksRef.current = null
  queryClient.clear()
})

afterEach(() => {
  queryClient.clear()
})

describe('BrowserLiveView — agent-identity chip (ADR-043 D3 / US-6)', () => {
  it('renders a chip labelled with the driven agent\'s name (Jim)', () => {
    seedAgents()
    render(<BrowserLiveView sessionId="s1" agentId="jim" />)

    const chip = screen.getByTestId('browser-live-agent-chip')
    expect(chip).toHaveTextContent('Jim')
    // The title attribute disambiguates whose context is driven.
    expect(chip).toHaveAttribute('title', expect.stringContaining('Jim'))
  })

  it('renders a DIFFERENT agent\'s name when the panel is opened for Ray', () => {
    seedAgents()
    render(<BrowserLiveView sessionId="s2" agentId="ray" />)

    const chip = screen.getByTestId('browser-live-agent-chip')
    expect(chip).toHaveTextContent('Ray')
    // The Jim chip is NOT shown on Ray's panel — the multi-agent clarity
    // guarantee: each open panel labels exactly its own agent.
    expect(screen.queryByText('Jim')).not.toBeInTheDocument()
  })

  it('falls back to a generic "Agent" label on a query-cache miss (never empty)', () => {
    // No agents seeded — simulates the pop-out route or a cold cache.
    render(<BrowserLiveView sessionId="s1" agentId="unknown-agent" />)

    const chip = screen.getByTestId('browser-live-agent-chip')
    expect(chip).toHaveTextContent('Agent')
  })

  it('coexists with the drive-status chip (identity ≠ driving state)', () => {
    seedAgents()
    render(<BrowserLiveView sessionId="s1" agentId="jim" />)

    // Both chips render: the agent-identity chip (persistent) AND the
    // drive-status chip (who's driving right now). They must not collapse
    // into one — identity and driving-state are orthogonal signals.
    expect(screen.getByTestId('browser-live-agent-chip')).toHaveTextContent('Jim')
    expect(screen.getByTestId('browser-live-status-chip')).toBeInTheDocument()
  })
})
