/// <reference types="vite/client" />
// Settings G1 transport fixture. Real sections, stores, router, QueryClient,
// API adapters and generated response validation. No component/hook spies.
import type { ReactElement } from 'react'
import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRootRoute, createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { vi } from 'vitest'
import { routeTree } from '@/routeTree.gen'
import { useUiStore } from '@/store/ui'
import type { SettingsSectionId } from '@/components/settings/settingsSections'
import type { IntegrationProvidersResponse, ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import { SettingsSectionBody } from '@/components/settings/SettingsSectionBody'

export function jsonG1(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}
export function heldG1() {
  let resolve!: (r: Response) => void
  const promise = new Promise<Response>((accept) => { resolve = accept })
  return { promise, resolve }
}
export const contextG1 = {
  mcp_result_cap: 20000, builtin_success_cap: 8000, builtin_failure_cap: 4000,
  tool_result_share_fraction: 0.4, ingest_bound_bytes: 1048576, default_context_window: null, model_overrides: [],
}
export const performanceG1 = {
  max_parallel_agents: 3, effective_max_parallel_agents: 3, max_parallel_agents_configured: true,
  tools_on_demand: true, goal_max_rounds: 25, goal_reminder_quiet_seconds: 300,
  goal_stalled_checkin_enabled: false, goal_stalled_checkin_minutes: 30, max_tool_iterations: 40,
  max_tool_iterations_saved_state: 'ok', max_tool_iterations_saved_raw: 40, max_tool_iterations_lowered_agents: [],
}
export const retentionG1 = { session_days: 90, memory_retros_days: 180, memory_retros_forever: false, keep_everything_forever: false }
export const thirdPartyG1 = {
  clis: {
    'claude-code': { path: '/opt/bin/claude', preset_args: '', detected: { installed: false } },
    codex: { path: '/opt/bin/Codex', preset_args: '--profile Work', detected: { installed: false } },
    opencode: { path: '', preset_args: '', detected: { installed: false } },
  },
}
const models = ['g1-model-a', 'g1-model-b', 'g1-model-c']
export const providersG1 = [{ id: 'openrouter', name: 'OpenRouter', display_name: 'OpenRouter', status: 'connected', auth_method: 'api_key', has_models_endpoint: true, models, dependents: [], backs_default: true }]
export const catalogG1: ProvidersCatalog = {
  schema_version: '2.0.0', version: 'v2026.11.22', updated_at: '2026-11-22T00:00:00Z',
  source: 'settings-g1-contract-fixture', served_from: 'embedded', stale: false,
  default_resize_limits: { long_edge_px: 2000, max_bytes: 5242880 },
  providers: [{ id: 'openrouter', name: 'OpenRouter', company: 'OpenRouter', api: 'https://openrouter.ai/api/v1', auth_methods: ['api_key'], tier: 'popular', aliases: [], locality: 'cloud', protocol: 'openai-compatible',
    models: models.map((id) => ({ id, name: id, context_window: 128000, max_output_tokens: 8192, input_modalities: ['text'], tool_call: true, status: 'active', reasoning: { levels: ['off', 'low', 'high'], switch: false } })),
  }],
}
export const integrationsG1: IntegrationProvidersResponse = {
  search: [], voice: (['elevenlabs', 'groq', 'audio-model'] as const).map((id) => ({ id, kind: 'voice', display_name: id, configured: true, requires_key: id !== 'audio-model', active: id === 'elevenlabs' })),
  active_voice: 'elevenlabs',
}
export const aboutG1 = { version: '1.2.3', go_version: 'go1.27.4', os: 'linux', arch: 'amd64', uptime_seconds: 3600 }

// not-wire-format: the transport recorder's internal test data, never a product carrier.
export interface GateCall { method: string; path: string; body: unknown; init?: RequestInit }
type Reply = (call: GateCall) => Response | Promise<Response>
export function transportG1() {
  const defaults: Record<string, unknown> = {
    '/state': { onboarding_complete: true, identity: { mode: 'local', edition: 'core', signed_in: true } },
    '/auth/validate': {}, '/auth/reauth': { verified: true, token: 'g1-single-use-consent', expires_in: 300 },
    '/config': { gateway: { host: '127.0.0.1', port: 5000, hot_reload: true, log_level: 'info', dev_mode_bypass: false }, agents: { defaults: { default_model: { provider: 'openrouter', model: 'g1-model-a' } } }, security: { exec_timeout_seconds: 0, max_background_seconds: 0, rate_limits: {} }, storage: { retention: { session_days: 90 } }, tools: { exec: { enable_proxy: false } } },
    '/config/pending-restart': [], '/about': aboutG1, '/version': { version: '1.2.3', build_sha: 'abc1234' },
    '/providers': providersG1, '/providers/catalog': catalogG1,
    '/providers/default-model': { provider: 'openrouter', model: 'g1-model-a', reasoning_effort: 'low', context_window: 128000, window_source: 'catalog' },
    '/integrations/providers': integrationsG1,
    '/settings/context': contextG1, '/performance': performanceG1, '/settings/retention': retentionG1,
    '/settings/third-party-agents': thirdPartyG1,
    '/settings/memory': { auto_recap_enabled: true, idle_timeout_minutes: 30, bootstrap_recap_enabled: true, bootstrap_recap_max_per_minute: 5, recap_model: 'g1-model-a', recap_provider: 'openrouter', recap_reasoning_effort: 'off', recap_fallback_models: [{ model: 'g1-model-b', provider: 'openrouter', reasoning_effort: 'off' }] },
    '/security/sandbox-config': { mode: 'enforce', applied_mode: 'enforce', auto_approve: false, allowed_paths: [], ssrf: { allow_internal: [] }, requires_restart: false },
    '/security/sandbox-status': { backend: 'landlock', available: true, kernel_level: true, policy_applied: true, seccomp_enabled: true, bind_ports_count: 0, kernel_sandbox_active: true },
    '/security/rate-limits': { enabled: true, max_agent_llm_calls_per_hour: 7, max_agent_tool_calls_per_minute: 9 },
    '/security/prompt-guard': { level: 'medium', requires_restart: false },
    '/gateway/god-mode': { enabled: false, persisted: false, available: true, supported: true },
    '/security/exec-proxy-status': { enabled: false, running: false },
    '/credentials': [], '/storage/stats': { workspace_size_bytes: 10, session_count: 3, memory_entry_count: 5, warnings: [] },
    '/status': { daily_cost: 0, uptime_seconds: 3600 }, '/doctor': null, '/tools': [],
    '/audit-log': { entries: [], chain_status: 'unknown' },
    '/notifications': { notifications: [], unread_count: 0 }, '/agents': [], '/tasks': [], '/sessions': [],
    '/workspaces': [{ id: 'ws-g1', name: 'G1 workspace', status: 'active', is_default: true }],
    '/gateway/restart': { status: 'restarting', restart_id: 'restart-g1-proof', drain_seconds: 0 },
  }
  const overrides = new Map<string, Reply>()
  const calls: GateCall[] = []
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), window.location.origin).pathname.replace(/^\/api\/v1/, '')
    const method = init?.method ?? 'GET'
    const call = { method, path, body: init?.body ? JSON.parse(String(init.body)) as unknown : undefined, init }
    calls.push(call)
    const reply = overrides.get(`${method} ${path}`)
    if (reply) return reply(call)
    if (method === 'GET' && path in defaults) return jsonG1(defaults[path])
    if (method === 'POST' && (path === '/auth/reauth' || path === '/gateway/restart')) return jsonG1(defaults[path])
    return jsonG1({ error: `Unexpected G1 transport: ${method} ${path}` }, 404)
  })
  vi.stubGlobal('fetch', fetch)
  document.cookie = 'csrf=settings-g1-proof; Path=/'
  useUiStore.setState({ toasts: [] })
  window.localStorage.setItem('omnipus_auth_username', 'g1-tester')
  vi.stubGlobal('matchMedia', (query: string) => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }))
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
  if (!globalThis.ResizeObserver) vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  return {
    calls, defaults,
    on(method: string, path: string, reply: Reply) { overrides.set(`${method} ${path}`, reply) },
    writes(path?: string) { return calls.filter((c) => c.method !== 'GET' && (!path || c.path === path)) },
  }
}
export function clientG1() {
  return new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })
}
export function renderG1(ui: ReactElement, client = clientG1()) {
  const root = createRootRoute({ component: () => ui })
  const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ['/'] }) })
  const view = render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  return { ...view, client, router }
}
export function sectionG1(section: SettingsSectionId, client = clientG1()) {
  return renderG1(<SettingsSectionBody section={section} />, client)
}
export async function appG1(path: string) {
  const client = clientG1()
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  const view = render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  return { ...view, client, router }
}
