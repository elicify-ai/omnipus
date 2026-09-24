import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, within } from 'storybook/test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { SandboxStatus } from '@/lib/api'
import { AutoApproveControl } from './SecuritySection'

/**
 * AutoApproveControl — Security tab's Auto-approve card
 * (SecuritySection.tsx).
 *
 * Founder decision (2026-09-24): the old "Needs the sandbox — not available
 * on Windows" line (implying Auto is switched OFF without one) is replaced
 * by a caveat that Auto still works, shown as a caution (warning-colored)
 * when the server reports no enforcing kernel sandbox, muted otherwise.
 *
 * Seeds ['sandbox-config'], ['sandbox-status'] and ['app-state'] directly
 * into a react-query cache (`staleTime: Infinity` — a static Storybook
 * build has no backend to refetch from), the same approach
 * ChatModeBadge.stories.tsx uses, rather than mocking `fetch`.
 */

function sandboxStatus(overrides: Partial<SandboxStatus> = {}): SandboxStatus {
  return {
    backend: 'landlock',
    available: true,
    kernel_level: true,
    policy_applied: true,
    seccomp_enabled: true,
    bind_ports_count: 0,
    ...overrides,
  }
}

function seed(status: SandboxStatus, autoApprove: boolean) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  queryClient.setQueryData(['sandbox-config'], { auto_approve: autoApprove })
  queryClient.setQueryData(['sandbox-status'], status)
  queryClient.setQueryData(['app-state'], {
    onboarding_complete: true,
    identity: { mode: 'platform', edition: 'hosted', signed_in: true },
  })
  return queryClient
}

const meta = {
  title: 'Settings/Auto-approve control',
  component: AutoApproveControl,
} satisfies Meta<typeof AutoApproveControl>
export default meta
type Story = StoryObj<typeof meta>

export const EnforcingSandbox: Story = {
  render: () => {
    const queryClient = seed(sandboxStatus({ kernel_sandbox_active: true }), true)
    return (
      <QueryClientProvider client={queryClient}>
        <AutoApproveControl />
      </QueryClientProvider>
    )
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const caveat = await canvas.findByText(
      /without a kernel sandbox \(for example on windows\), shell commands are checked by reading the command text only/i,
    )
    await expect(caveat.className).toContain('text-[var(--color-muted)]')
  },
}

// The founder-decision caution state (2026-09-24): the server reports no
// enforcing kernel sandbox, so the caveat line renders warning-colored.
export const NoSandboxCaution: Story = {
  render: () => {
    const queryClient = seed(sandboxStatus({ kernel_sandbox_active: false }), true)
    return (
      <QueryClientProvider client={queryClient}>
        <AutoApproveControl />
      </QueryClientProvider>
    )
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const caveat = await canvas.findByText(
      /without a kernel sandbox \(for example on windows\), shell commands are checked by reading the command text only/i,
    )
    await expect(caveat.className).toContain('text-[var(--color-warning)]')
  },
}
