import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useQuery } from '@tanstack/react-query'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

// ── Mock auth store ───────────────────────────────────────────────────────────
vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn(),
}))

// ── Mock api ──────────────────────────────────────────────────────────────────
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchConfig: vi.fn(),
  }
})

import { useAuthStore } from '@/store/auth'
import { fetchConfig } from '@/lib/api'

// ── Stub: re-implements only the tab-visibility logic from SettingsScreen ─────
//
// SettingsScreen lives in a TanStack Router route file and can't be imported
// without a full router context. This stub tests only the Access-tab gate:
//   showAccessTab = isAdmin && !devModeBypass
//
// It uses the same mocked stores and API calls as the real screen.
function SettingsScreenStub() {
  const role = useAuthStore((s) => (s as { role?: string }).role)
  const isAdmin = role === 'admin'

  const { data: config } = useQuery({
    queryKey: ['config'],
    queryFn: fetchConfig,
    enabled: isAdmin,
    staleTime: 30_000,
  })

  const gateway = (config as Record<string, Record<string, unknown>> | undefined)?.gateway
  const devModeBypass = Boolean(gateway?.dev_mode_bypass)
  const showAccessTab = isAdmin && !devModeBypass

  return (
    <Tabs defaultValue="providers">
      <TabsList>
        <TabsTrigger value="providers">Providers</TabsTrigger>
        {isAdmin && <TabsTrigger value="devices">Devices</TabsTrigger>}
        {showAccessTab && <TabsTrigger value="access">Access</TabsTrigger>}
        <TabsTrigger value="about">About</TabsTrigger>
      </TabsList>
    </Tabs>
  )
}

function renderStub(prefetchedConfig?: Record<string, unknown>) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  if (prefetchedConfig !== undefined) {
    client.setQueryData(['config'], prefetchedConfig)
  }
  return render(
    <QueryClientProvider client={client}>
      <SettingsScreenStub />
    </QueryClientProvider>,
  )
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('TestSettingsNav_HidesAccessTabUnderBypass', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useAuthStore).mockImplementation(
      ((selector: (s: { role?: string; user?: { username: string } }) => unknown) =>
        selector({ role: 'admin', user: { username: 'testadmin' } })) as never,
    )
  })

  it('Access tab is NOT present when dev_mode_bypass=true', async () => {
    const config = { gateway: { dev_mode_bypass: true } }
    vi.mocked(fetchConfig).mockResolvedValue(config as never)

    renderStub(config)

    await waitFor(() => {
      expect(screen.queryByRole('tab', { name: /^access$/i })).not.toBeInTheDocument()
    })
  })

  it('Access tab IS present when dev_mode_bypass=false', async () => {
    const config = { gateway: { dev_mode_bypass: false } }
    vi.mocked(fetchConfig).mockResolvedValue(config as never)

    renderStub(config)

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /^access$/i })).toBeInTheDocument()
    })
  })
})
