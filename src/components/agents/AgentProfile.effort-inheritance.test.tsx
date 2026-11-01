/** CHECK-05 / S-86: real agent-primary model-switch transaction.
 * The agent has no own model/effort; default-model Off must block the switch.
 * Only HTTP API edges are mocked; AgentProfile, ModelSelector, query state,
 * blocked-step UI, focus, draft and autosave are production components.
 */
import { beforeEach, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import { mockCoreAgent } from './agentProfileTestFixtures'
import { fetchAgent, fetchConfig, fetchProviders, fetchProvidersCatalog, fetchSkills, fetchPerformanceSettings, updateAgent } from '@/lib/api'
import type { Agent, Config, Provider, ProvidersCatalog } from '@/lib/api'

class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
if (!globalThis.ResizeObserver) vi.stubGlobal('ResizeObserver', ResizeObserverStub)
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = function () {}
if (!Element.prototype.hasPointerCapture) Element.prototype.hasPointerCapture = () => false
vi.mock('@tanstack/react-router', async importOriginal => ({ ...await importOriginal<typeof import('@tanstack/react-router')>(), useNavigate: () => vi.fn(), useParams: () => ({}) }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), fetchAgent: vi.fn(), fetchConfig: vi.fn(), fetchProviders: vi.fn(), fetchProvidersCatalog: vi.fn(), fetchSkills: vi.fn(), fetchPerformanceSettings: vi.fn(), updateAgent: vi.fn() }))

const oldModel = 'deepseek/deepseek-v4.1-flash'
const target = 'openai/gpt-6.1-sol'
const muted = 'budget-only-model'
const catalog = {
  schema_version: '2.0.0', version: 'v2026.10.31', updated_at: '2026-10-31T00:00:00Z', source: 'test', served_from: 'embedded', stale: false,
  default_resize_limits: { long_edge_px: 2048, max_bytes: 10485760 },
  providers: [{ id: 'openrouter', name: 'OpenRouter', company: 'OpenRouter', api: 'https://openrouter.ai/api/v1', protocol: 'openai-compatible', protocols: [{ protocol: 'openai-compatible', api: 'https://openrouter.ai/api/v1' }], tier: 'popular', auth_methods: ['api_key'], aliases: [], locality: 'cloud', models: [
    [oldModel, ['none', 'low', 'high', 'max']], [target, ['low', 'medium', 'high']], [muted, []],
  ].map(([id, levels]) => ({ id, name: id, context_window: 128000, max_output_tokens: 8192, input_modalities: ['text'], tool_call: true, status: 'active', reasoning: { levels, switch: false } })) }],
} as unknown as ProvidersCatalog
const baselineUpdatedAt = '2026-11-01T06:00:00.000Z'
const riding = { ...mockCoreAgent, id: 'effort-inherited', name: 'Effort inherited fixture', model: '', provider: '', reasoning_effort: null,
  updated_at: baselineUpdatedAt,
  editable_fields: [...mockCoreAgent.editable_fields!, { name: 'reasoning_effort', editable: true }], fallback_models: [] } as unknown as Agent
let acknowledgedAgent: Agent

beforeEach(() => {
  // Model the API edge, not a GET that permanently returns the pre-save row.
  acknowledgedAgent = { ...riding }
  let acknowledgedAt = Date.parse(baselineUpdatedAt)
  vi.mocked(fetchAgent).mockReset().mockImplementation(async () => ({ ...acknowledgedAgent }))
  vi.mocked(fetchConfig).mockReset().mockResolvedValue({ agents: { defaults: { default_model: { provider: 'openrouter', model: oldModel, reasoning_effort: 'off' } } } } as unknown as Config)
  vi.mocked(fetchProvidersCatalog).mockReset().mockResolvedValue(catalog)
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([{ id: 'openrouter', name: 'openrouter', display_name: 'OpenRouter', status: 'connected', models: [oldModel, target, muted], auth_method: 'api_key', dependents: [], backs_default: true }] as Provider[])
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchPerformanceSettings).mockReset().mockResolvedValue({ max_tool_iterations: 20 } as never)
  vi.mocked(updateAgent).mockReset().mockImplementation(async (_id, body) => {
    acknowledgedAt += 1000
    acknowledgedAgent = { ...acknowledgedAgent, ...body, updated_at: new Date(acknowledgedAt).toISOString() } as Agent
    return { ...acknowledgedAgent }
  })
})

async function beginSwitch(to: string) {
  const user = userEvent.setup()
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><AgentProfile agentId={riding.id} /></QueryClientProvider>)
  await screen.findByText(riding.name)
  // Positive control: the inherited presentation exists before any switch.
  await screen.findByRole('button', { name: /^Effort.*(Default|Off)/i })
  const modelButton = screen.getByRole('combobox', { name: /^Model selector/ })
  await user.click(modelButton)
  await user.click(await screen.findByText(to, { exact: true }))
  expect(updateAgent).not.toHaveBeenCalled()
  return { user, modelButton }
}

it('S-86 agent primary: inherited Off requires explicit High, then stores model/provider/High together', async () => {
  const { user } = await beginSwitch(target)
  expect(screen.getByText(`${target} can't turn reasoning off. Pick a level to finish switching.`)).toBeInTheDocument()
  expect(screen.getByRole('radio', { name: 'Low' })).toHaveFocus()
  expect(screen.getByRole('radio', { name: 'Low' })).toHaveAttribute('aria-checked', 'false')
  await user.click(screen.getByRole('radio', { name: 'High' }))
  await waitFor(() => expect(updateAgent).toHaveBeenCalledTimes(1))
  const [id, body] = vi.mocked(updateAgent).mock.calls[0]
  expect(id).toBe(riding.id)
  expect(body).toMatchObject({ model: target, provider: 'openrouter', reasoning_effort: 'high' })
  expect(screen.queryAllByRole('radio')).toHaveLength(0)
  await waitFor(() => expect(screen.getByRole('button', { name: /^Effort.*High/i })).toHaveFocus())
})

it('S-86 agent primary: Escape preserves the default-model slot and Not set without an autosave', async () => {
  const { user, modelButton } = await beginSwitch(target)
  await user.keyboard('{Escape}')
  expect(updateAgent).not.toHaveBeenCalled()
  expect(modelButton).toHaveTextContent(/Provider default/i)
  expect(modelButton).toHaveFocus()
  expect(screen.getByText('Switch cancelled')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /^Effort/i }))
  expect(screen.getByRole('radio', { name: /^(Not set|Default's setting)/i })).toHaveAttribute('aria-checked', 'true')
})

it('S-86 agent primary: inherited Off to nothing sendable explicitly stores Provider default', async () => {
  const { user } = await beginSwitch(muted)
  await user.click(screen.getByRole('button', { name: 'Let the provider decide' }))
  await waitFor(() => expect(updateAgent).toHaveBeenCalledTimes(1))
  expect(vi.mocked(updateAgent).mock.calls[0][1]).toMatchObject({ model: muted, provider: 'openrouter', reasoning_effort: 'provider_default' })
})

it.each(['Cancel', 'Escape'])('S-86 agent primary: %s on nothing-sendable preserves the previous default model and Not set', async action => {
  const { user, modelButton } = await beginSwitch(muted)
  if (action === 'Escape') await user.keyboard('{Escape}')
  else await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(updateAgent).not.toHaveBeenCalled()
  expect(modelButton).toHaveTextContent(/Provider default/i)
  await user.click(screen.getByRole('button', { name: /^Effort/i }))
  expect(screen.getByRole('radio', { name: /^(Not set|Default's setting)/i })).toHaveAttribute('aria-checked', 'true')
})
