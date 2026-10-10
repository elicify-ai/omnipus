// Browser/network-edge fixtures shared by the T1–T10 regression tests.
import type { Agent, Plan, Task } from '@/lib/api'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'

export function layoutTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-ray', title: 'Ray report', status: 'inbox', action: 'llm', priority: 3,
    workspace_id: 'ws-layout', surface: 'user', owner: 'admin', created_by: 'admin',
    created_at: '2026-10-07T10:00:00Z', updated_at: '2026-10-07T10:00:00Z',
    agent_id: 'ray', agent_name: 'Ray', tags: ['docs'], ...overrides,
  }
}

export function layoutPlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-layout', title: 'Launch plan', state: 'draft', plan_phase: 'idle',
    workspace_id: 'ws-layout', owner_agent_id: 'ray', owner: 'admin', created_by: 'admin',
    created_at: '2026-10-07T10:00:00Z', updated_at: '2026-10-07T10:00:00Z', ...overrides,
  }
}

export function layoutAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'ray', name: 'Ray', type: 'core', locked: true, needs_model: false, status: 'active', soul: '',
    timeout_seconds: 300, max_tool_iterations: 50, max_tool_iterations_source: 'global',
    max_tool_iterations_override_ignored: false, memory_enabled: true, revision: '0'.repeat(64),
    figure: 'Omnipus', role: 'general',
    ...overrides,
  }
}

/** T26 retains diagnostic/tag assertions through the real info affordance. */
export function showTaskInfo(title: string) {
  fireEvent.pointerEnter(screen.getByRole('button', { name: `Task details: ${title}` }), { pointerType: 'mouse' })
  return screen.getByRole('dialog', { name: 'Task details' })
}

export function renderTaskInfo(children: ReactNode) {
  const mounted = render(children)
  const info = screen.getByRole('button', { name: /^Task details:/ })
  fireEvent.pointerEnter(info, { pointerType: 'mouse' })
  return mounted
}

export function renderLayout(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return { ...render(<QueryClientProvider client={client}>{children}</QueryClientProvider>), client }
}
