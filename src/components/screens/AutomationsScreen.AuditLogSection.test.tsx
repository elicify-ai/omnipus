/**
 * MonitorScreen.AuditLogSection.test.tsx
 *
 * Tests for the AuditLogSection nested component inside MonitorScreen.
 *
 * AuditLogSection behaviour:
 *   - role !== 'admin': renders "Audit log requires admin access." and does NOT call fetchAuditLog
 *   - role === 'admin': calls fetchAuditLog via useQuery (enabled: role === 'admin')
 *   - Loading state: shows "Loading audit log…"
 *   - Error state: shows "Audit log unavailable."
 *   - Empty data: shows "No audit entries."
 *   - With data: renders each entry's event field
 *
 * Traces to: MonitorScreen.tsx — AuditLogSection component (lines 93–147),
 *            FR-021 Audit Log section.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Mocks declared before any SPA imports ─────────────────────────────────────

vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAuditLog: vi.fn(),
    fetchTokenStats: vi.fn(),
    fetchSchedules: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    tokenStatsQueryKeys: actual.tokenStatsQueryKeys,
    auditLogQueryKeys: actual.auditLogQueryKeys,
  }
})

// Mock heavy sub-components that AutomationsScreen renders — we only care about
// AuditLogSection in this test file, so we stub the default component to null
// while preserving the named helper exports (triggerSummary / sessionModeLabel)
// that AutomationsScreen imports.
vi.mock('@/components/command-center/SchedulesList', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/components/command-center/SchedulesList')>()
  return { ...actual, SchedulesList: () => null }
})

vi.mock('@/components/command-center/ScheduleFormSheet', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/components/command-center/ScheduleFormSheet')>()
  return { ...actual, ScheduleFormSheet: () => null }
})

// Phosphor icons (used by MonitorScreen header)
vi.mock('@phosphor-icons/react', () => ({
  Plus: () => null,
}))

// ── Import mocked modules ─────────────────────────────────────────────────────

import { useAuthStore } from '@/store/auth'
import { fetchAuditLog, fetchTokenStats } from '@/lib/api'
import type { AuditEntry } from '@/lib/api'

// Import the real AutomationsScreen — it exports the parent; AuditLogSection is
// nested inside and exercised via AutomationsScreen.
import { AutomationsScreen } from '@/components/screens/AutomationsScreen'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

/**
 * Render AutomationsScreen (which contains AuditLogSection) inside a fresh
 * QueryClientProvider.  The caller sets up store/API mocks beforehand.
 */
function renderMonitor() {
  const client = makeQueryClient()
  return render(
    <QueryClientProvider client={client}>
      <AutomationsScreen />
    </QueryClientProvider>,
  )
}

/**
 * Build a minimal AuditEntry fixture from the generated OpenAPI type.
 * Only the required fields (timestamp, event) are set; optional fields default
 * to absent.
 */
function makeAuditEntry(overrides: Partial<AuditEntry> = {}): AuditEntry {
  return {
    timestamp: '2026-06-01T10:00:00Z',
    event: 'tool_call',
    ...overrides,
  }
}

// ── beforeEach: reset mocks ───────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()

  // Default: fetchTokenStats returns a never-resolving promise so it doesn't
  // interfere with AuditLogSection assertions.
  vi.mocked(fetchTokenStats).mockReturnValue(new Promise(() => {}))
})

// ── describe: non-admin gate ──────────────────────────────────────────────────
// Traces to: MonitorScreen.tsx line 104 — `if (role !== 'admin')` guard.

describe('AuditLogSection — non-admin access gate', () => {
  beforeEach(() => {
    // Mock useAuthStore selector to return role: 'user'
    vi.mocked(useAuthStore).mockImplementation(
      ((selector: (s: { role: string | null }) => unknown) =>
        selector({ role: 'user' })) as never,
    )
  })

  it('renders "Audit log requires admin access." for role=user', async () => {
    // BDD: Given the user has role 'user'
    // When MonitorScreen renders
    // Then "Audit log requires admin access." is visible
    // Traces to: MonitorScreen.tsx line 105

    vi.mocked(fetchAuditLog).mockResolvedValue([])
    renderMonitor()

    expect(screen.getByText('Audit log requires admin access.')).toBeInTheDocument()
  })

  it('does NOT call fetchAuditLog when role is not admin', async () => {
    // BDD: Given role='user'
    // When AuditLogSection renders
    // Then the query is disabled (enabled: false) so fetchAuditLog is never invoked
    // Traces to: MonitorScreen.tsx line 101 — `enabled: role === 'admin'`
    //
    // Differentiation test: if fetchAuditLog WERE called regardless of role,
    // this assertion would catch it.

    vi.mocked(fetchAuditLog).mockResolvedValue([])
    renderMonitor()

    // Allow any microtasks to settle before asserting.
    await waitFor(() => {
      expect(fetchAuditLog).not.toHaveBeenCalled()
    })
  })

  it('does not render "No audit entries." when role is not admin', () => {
    // Content test: the admin-only "No audit entries." message must not appear
    // for a non-admin user — only the access-denied message should show.
    vi.mocked(fetchAuditLog).mockResolvedValue([])
    renderMonitor()

    expect(screen.queryByText('No audit entries.')).not.toBeInTheDocument()
  })
})

// ── describe: admin sees entries ──────────────────────────────────────────────
// Traces to: MonitorScreen.tsx lines 126–146 — entry list rendering.

describe('AuditLogSection — admin sees audit entries', () => {
  beforeEach(() => {
    vi.mocked(useAuthStore).mockImplementation(
      ((selector: (s: { role: string | null }) => unknown) =>
        selector({ role: 'admin' })) as never,
    )
  })

  it('renders two entries when fetchAuditLog returns two AuditEntry objects', async () => {
    // BDD: Given role='admin' and fetchAuditLog returns 2 entries
    // When AuditLogSection renders
    // Then both entry event fields are visible
    //
    // Differentiation test: two different event values → two different rendered
    // strings.  A hardcoded/stub response would fail this.
    // Traces to: MonitorScreen.tsx line 133 — `{entry.event}`

    vi.mocked(fetchAuditLog).mockResolvedValue([
      makeAuditEntry({ event: 'tool_call', timestamp: '2026-06-01T10:00:00Z' }),
      makeAuditEntry({ event: 'exec', timestamp: '2026-06-01T11:00:00Z' }),
    ])

    renderMonitor()

    await waitFor(() => {
      expect(screen.getByText('tool_call')).toBeInTheDocument()
      expect(screen.getByText('exec')).toBeInTheDocument()
    })
  })

  it('renders the decision badge when entry has a decision field', async () => {
    // Content test: entry.decision ('allow' / 'deny') is rendered as a coloured
    // span.  Asserts real content, not just "no crash".
    // Traces to: MonitorScreen.tsx lines 138–142

    vi.mocked(fetchAuditLog).mockResolvedValue([
      makeAuditEntry({ event: 'policy_eval', decision: 'deny' }),
    ])

    renderMonitor()

    await waitFor(() => {
      expect(screen.getByText('deny')).toBeInTheDocument()
    })
  })

  it('renders the tool field when entry.tool is set', async () => {
    // Content test: entry.tool is appended with " — " separator.
    // Traces to: MonitorScreen.tsx line 135

    vi.mocked(fetchAuditLog).mockResolvedValue([
      makeAuditEntry({ event: 'tool_call', tool: 'workspace.shell' }),
    ])

    renderMonitor()

    await waitFor(() => {
      expect(screen.getByText(/workspace\.shell/)).toBeInTheDocument()
    })
  })
})

// ── describe: admin with empty result ─────────────────────────────────────────
// Traces to: MonitorScreen.tsx lines 120–123 — empty state.

describe('AuditLogSection — admin empty result', () => {
  beforeEach(() => {
    vi.mocked(useAuthStore).mockImplementation(
      ((selector: (s: { role: string | null }) => unknown) =>
        selector({ role: 'admin' })) as never,
    )
  })

  it('renders "No audit entries." when fetchAuditLog returns empty array', async () => {
    // BDD: Given role='admin' and fetchAuditLog returns []
    // When AuditLogSection renders
    // Then "No audit entries." is displayed
    // Traces to: MonitorScreen.tsx line 122

    vi.mocked(fetchAuditLog).mockResolvedValue([])

    renderMonitor()

    await waitFor(() => {
      expect(screen.getByText('No audit entries.')).toBeInTheDocument()
    })
  })

  it('does NOT render "Audit log requires admin access." for admin with empty data', async () => {
    // Differentiation test: admin + empty data → "No audit entries.", not the
    // access-denied message.  Two different roles → two different messages.
    vi.mocked(fetchAuditLog).mockResolvedValue([])

    renderMonitor()

    await waitFor(() => {
      expect(screen.getByText('No audit entries.')).toBeInTheDocument()
    })
    expect(screen.queryByText('Audit log requires admin access.')).not.toBeInTheDocument()
  })
})

// ── describe: admin error state ───────────────────────────────────────────────
// Traces to: MonitorScreen.tsx lines 113–117 — error state.

describe('AuditLogSection — admin error state', () => {
  beforeEach(() => {
    vi.mocked(useAuthStore).mockImplementation(
      ((selector: (s: { role: string | null }) => unknown) =>
        selector({ role: 'admin' })) as never,
    )
  })

  it('renders "Audit log unavailable." when fetchAuditLog rejects', async () => {
    // BDD: Given role='admin' and fetchAuditLog rejects with an error
    // When AuditLogSection renders and the query fails
    // Then "Audit log unavailable." is shown
    // Traces to: MonitorScreen.tsx line 116

    vi.mocked(fetchAuditLog).mockRejectedValue(new Error('network error'))

    renderMonitor()

    await waitFor(() => {
      expect(screen.getByText('Audit log unavailable.')).toBeInTheDocument()
    })
  })

  it('does NOT render any entry text when fetchAuditLog rejects', async () => {
    // Content test: on error no entry events must bleed through.
    vi.mocked(fetchAuditLog).mockRejectedValue(new Error('500 Internal Server Error'))

    renderMonitor()

    await waitFor(() => {
      expect(screen.getByText('Audit log unavailable.')).toBeInTheDocument()
    })
    expect(screen.queryByText('tool_call')).not.toBeInTheDocument()
  })
})
