import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import type { SandboxStatus } from '@/lib/api'
import { ChatModeBadge } from './ChatModeBadge'

/**
 * ChatModeBadge — chat-header ADR-092 permission-state indicator.
 *
 * Founder decision (2026-09-24): Auto-approve no longer requires an
 * enforcing kernel sandbox. These stories seed the shared zustand stores
 * (`useSessionStore`/`useChatStore`) and a react-query cache with a fixed
 * `SandboxStatus` (`staleTime: Infinity` — a static Storybook build has no
 * backend to refetch from) rather than mocking `fetch` directly, matching
 * this component's actual data flow through `useResolvedAutoApprove`.
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

function seed(status: SandboxStatus) {
  useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_1', activeAgentType: 'core' })
  useChatStore.setState({ autoApproveEffective: undefined, sessionsById: {}, pendingAutoApproveChoice: null })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  queryClient.setQueryData(['agents'], [{ id: 'mia', name: 'Mia', type: 'core', status: 'active' }])
  queryClient.setQueryData(['sandbox-status'], status)
  return queryClient
}

const meta = {
  title: 'Chat/Chat mode badge',
  component: ChatModeBadge,
  // 'centered' (matching tooltip.stories.tsx's own convention) gives the
  // AutoNoSandbox story's tooltip room to open ABOVE the badge without
  // going off-screen at the top of a 'padded' layout's small margin.
  parameters: { layout: 'centered' },
} satisfies Meta<typeof ChatModeBadge>
export default meta
type Story = StoryObj<typeof meta>

export const Ask: Story = {
  render: () => {
    const queryClient = seed(sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: true }))
    return (
      <QueryClientProvider client={queryClient}>
        <ChatModeBadge />
      </QueryClientProvider>
    )
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(await canvas.findByTestId('chat-mode-badge')).toHaveTextContent('Ask')
  },
}

export const Auto: Story = {
  render: () => {
    const queryClient = seed(sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: true }))
    return (
      <QueryClientProvider client={queryClient}>
        <ChatModeBadge />
      </QueryClientProvider>
    )
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(await canvas.findByTestId('chat-mode-badge')).toHaveTextContent('Auto')
  },
}

// The founder-decision state (2026-09-24): Auto is still fully active with
// no enforcing kernel sandbox — a caution, never a fallback to Ask. The
// play() function hovers the badge so the screenshot captures the tooltip
// bubble open, the same way tooltip.stories.tsx's Default story does.
export const AutoNoSandbox: Story = {
  render: () => {
    const queryClient = seed(sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: false }))
    return (
      <QueryClientProvider client={queryClient}>
        <ChatModeBadge />
      </QueryClientProvider>
    )
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const trigger = await canvas.findByTestId('chat-mode-badge-trigger')
    await expect(trigger).toHaveTextContent('Auto — no sandbox')
    await userEvent.hover(trigger)
    await expect(canvas.getByRole('tooltip')).toBeVisible()
    await expect(canvas.getByRole('tooltip')).toHaveTextContent(
      'No kernel sandbox is enforcing. Safe tool calls still run without asking, but shell commands are checked by reading the command text only.',
    )
  },
}

export const GodMode: Story = {
  render: () => {
    const queryClient = seed(
      sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: true, god_mode_active: true }),
    )
    return (
      <QueryClientProvider client={queryClient}>
        <ChatModeBadge />
      </QueryClientProvider>
    )
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(await canvas.findByTestId('chat-mode-badge')).toHaveTextContent('God Mode')
  },
}
