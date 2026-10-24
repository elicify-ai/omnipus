import { vi } from 'vitest'
import type { Agent, Provider, Session, Workspace } from '@/lib/api/generated/openapi-types'

// Discriminating inputs, not product defaults. Restored expectations come from
// the user's actions in #1436, not from whatever the picker currently returns.
export const DEFAULT_MODEL = 'z-ai/glm-5.2'
export const FIRST_CHOICE = 'openai/gpt-4o'
export const LATEST_CHOICE = 'deepseek/deepseek-v4.1-flash'
export const SESSION_A = 'session-return-a'
export const SESSION_B = 'session-return-b'
export const MAIN_SESSION = 'session-return-main'
export const WORKSPACE_ID = 'session-return-workspace'
const FIXTURE_TIME = '2026-10-23T12:00:00Z'

export const agent: Agent = {
  revision: 'a'.repeat(64),
  id: 'mia', name: 'Mia', type: 'core', locked: true,
  figure: 'Omnipus', role: 'general', status: 'idle', soul: 'Test colleague',
  model: DEFAULT_MODEL, provider: 'openrouter', needs_model: false, memory_enabled: true,
  max_tool_iterations: 50, max_tool_iterations_source: 'global',
  max_tool_iterations_override_ignored: false,
}

export const providers: Provider[] = [
  {
    id: 'openrouter', name: 'openrouter', display_name: 'OpenRouter',
    status: 'connected', auth_method: 'api_key', dependents: [], backs_default: true,
    models: [DEFAULT_MODEL, FIRST_CHOICE, LATEST_CHOICE],
  },
  {
    id: 'backup', name: 'backup', display_name: 'Backup',
    status: 'connected', auth_method: 'api_key', dependents: [], backs_default: false,
    models: [DEFAULT_MODEL],
  },
]

export const workspace: Workspace = {
  revision: 'b'.repeat(64), id: WORKSPACE_ID, name: 'Linked workspace',
  status: 'active', pinned: false, pin_order: 0, task_count: 0,
  core_team: [agent.id], is_default: false,
  created_at: FIXTURE_TIME, updated_at: FIXTURE_TIME,
}

export function session(id: string, type: Session['type'] = 'chat'): Session {
  return {
    id, type, agent_id: agent.id, title: `Conversation ${id}`, status: 'active',
    created_at: FIXTURE_TIME, updated_at: FIXTURE_TIME,
    channel: 'webchat', partitions: [], workspace_id: WORKSPACE_ID,
    model: DEFAULT_MODEL, provider: 'openrouter',
    stats: { message_count: 0, tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0 },
  }
}

/** HTTP is the substitute boundary; real query functions and validators run. */
export function installSessionReturnNetwork() {
  const unexpectedRequests: string[] = []
  const sessions = [session(MAIN_SESSION, 'main'), session(SESSION_A), session(SESSION_B)]
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, 'http://localhost')
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    if (method === 'GET') {
      switch (url.pathname) {
        case '/api/v1/agents': return Response.json([agent])
        case '/api/v1/providers': return Response.json(providers)
        case '/api/v1/commands': return Response.json([])
        case '/api/v1/skills': return Response.json([])
        case `/api/v1/agents/${agent.id}/activity-runs`: return Response.json([])
        case '/api/v1/security/sandbox-status': return Response.json({
          backend: 'fallback', available: true, kernel_level: false,
          policy_applied: false, seccomp_enabled: false, bind_ports_count: 0,
          kernel_sandbox_active: false, auto_approve_effective: false,
        } satisfies import('@/lib/api/generated/openapi-types').SandboxStatus)
        case '/api/v1/workspaces': return Response.json([workspace])
        case `/api/v1/workspaces/${WORKSPACE_ID}`: return Response.json(workspace)
        case '/api/v1/sessions': return Response.json({ sessions })
      }
      const target = sessions.find((item) => url.pathname === `/api/v1/sessions/${item.id}`)
      if (target) return Response.json({ session: target, messages: [], agent_removed: false })
      if (sessions.some((item) => url.pathname === `/api/v1/sessions/${item.id}/messages`)) return Response.json([])
    }
    unexpectedRequests.push(`${method} ${url.pathname}${url.search}`)
    return Response.json({ message: 'Unexpected request in session-return test' }, { status: 500 })
  }))
  return unexpectedRequests
}

/** Browser facilities absent from jsdom, not application-behavior mocks. */
export function installSessionReturnBrowser() {
  vi.stubGlobal('scrollTo', vi.fn())
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
  vi.stubGlobal('matchMedia', (media: string): MediaQueryList => ({
    matches: false, media, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  }))
  const originalScroll = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollIntoView')
  Object.defineProperty(Element.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() })
  return () => {
    if (originalScroll) Object.defineProperty(Element.prototype, 'scrollIntoView', originalScroll)
    else Reflect.deleteProperty(Element.prototype, 'scrollIntoView')
  }
}
