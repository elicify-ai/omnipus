// R2-S3-SFH-08: a main whose header cannot be named (failed roster AND no agent
// id on the descriptor) renders nothing - by design - but must leave a trace so
// the empty header is debuggable.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchAgents } from '@/lib/api'

const logDiagnostic = vi.hoisted(() => vi.fn())
vi.mock('@/lib/telemetry', async (orig) => ({ ...(await orig<typeof import('@/lib/telemetry')>()), logDiagnostic }))
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), fetchAgents: vi.fn() }))

import { FeedKindLabel } from './FeedKindLabel'

function setDescriptor(agentId: string | null, id: string): void {
  useWorkspacesStore.setState({ activeWorkspaceId: 'ws_diag' })
  useSessionStore.setState({
    activeSessionId: id,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: { ws_diag: { id, type: 'main', title: '', agentId } },
  })
}

function renderLabel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(<QueryClientProvider client={qc}><FeedKindLabel /></QueryClientProvider>)
  return { qc, view }
}

beforeEach(() => {
  logDiagnostic.mockClear()
  vi.mocked(fetchAgents).mockReset()
})

describe('FeedKindLabel empty-header diagnostic', () => {
  it('logs once when a failed roster and a missing agent id leave the header empty', async () => {
    setDescriptor(null, 'sid-diag-1')
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    const { qc, view } = renderLabel()
    await waitFor(() => expect(qc.getQueryState(['agents'])?.status).toBe('error'))
    view.rerender(<QueryClientProvider client={qc}><FeedKindLabel /></QueryClientProvider>)
    const calls = logDiagnostic.mock.calls.filter(([event]) => event === 'chatHeaderLabelUnavailable')
    expect(calls).toHaveLength(1)
    expect(calls[0][1]).toMatchObject({ sessionId: 'sid-diag-1' })
  })

  it('negative control: a failed roster WITH an agent id still names the header and logs nothing', async () => {
    setDescriptor('jim', 'sid-diag-2')
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    const { qc, view } = renderLabel()
    // R3-QA-03: strict equality — the component renders exactly one line,
    // the id-derived title plus the unavailability marker, nothing else.
    await waitFor(() => expect(view.container.textContent).toBe('jim - Main Session — agent names unavailable'))
    expect(qc.getQueryState(['agents'])?.status).toBe('error')
    expect(logDiagnostic.mock.calls.filter(([event]) => event === 'chatHeaderLabelUnavailable')).toHaveLength(0)
  })
})
