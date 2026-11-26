// Shared fixtures for the Skills & Tools design-system surface tests (fix round 4,
// Part G): the five surfaces SkillsToolsPage.tsx#tab:skills, #tab:tools,
// #tab:external, #overlay:1 and SkillRowActions.tsx#overlay:1.
//
// This file is a helper, not a test file: it holds no test and no expectation
// about the page. Every oracle sits in the test file that uses it and comes from
// docs/internal/specs/skills-tools-redesign-spec.md (spec commit b27c64109), never
// from the page code. The helper only builds the inputs a test needs (a skill, a
// tool, a service), reaches the page through the same missing-file seam the other
// Skills & Tools tests use, and finds elements by the role and name a person sees.
//
// The one mock this file cannot declare is the module mock of '@/lib/api': a
// module mock is hoisted inside the file that states it, so each test file
// states its own and passes the mocked module to serveWorld below.
import { waitFor, within, screen, render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'
import { expect, vi } from 'vitest'
import type { McpServer, RegistryTool, Skill } from '@/lib/api'

// The BLOCKED seam: a tree that has no page yet still compiles this file, and the
// test fails loudly with the named missing element instead of an import error.
const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
export async function requireSkillsToolsPage(specRef: string): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    `BLOCKED: SkillsToolsPage is not implemented — required by ${specRef}`,
  ).toContain('/src/components/skills/SkillsToolsPage.tsx')
  const mod = (await pageModules['/src/components/skills/SkillsToolsPage.tsx']()) as Record<string, unknown>
  expect(typeof mod.SkillsToolsPage, 'BLOCKED: SkillsToolsPage public export').toBe('function')
  return mod.SkillsToolsPage as ComponentType
}

export function skill(id: string, overrides: Partial<Skill> = {}): Skill {
  return {
    has_local_changes: false,
    id,
    name: id,
    description: `Skill ${id}.`,
    source: 'global',
    status: 'active',
    verified: true,
    version: '1.0.0',
    revision: 'a'.repeat(64),
    category: 'tools',
    trust_approved: false,
    ...overrides,
  }
}

export function tool(name: string, overrides: Partial<RegistryTool> = {}): RegistryTool {
  return {
    name,
    description: `${name} description`,
    scope: 'general',
    category: 'communication',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
    ...overrides,
  }
}

// An external service's tools carry the id of their service (ToolRegistryEntry.server_id).
export function serviceTools(server: string, names: string[]): RegistryTool[] {
  return names.map((name) => tool(`mcp_${server}_${name}`, { category: 'search', source: 'mcp', server_id: server }))
}

export function service(id: string, tools: RegistryTool[], overrides: Partial<McpServer> = {}): McpServer {
  return {
    id,
    name: id,
    transport: 'sse',
    status: 'connected',
    enabled: true,
    tool_count: tools.length,
    tools: tools.map((entry) => entry.name),
    ...overrides,
  }
}

// What the server currently lists, as the list requests would answer.
export const world: { skills: Skill[]; tools: RegistryTool[]; servers: McpServer[] } = { skills: [], tools: [], servers: [] }
export function resetWorld() {
  world.skills = []
  world.tools = []
  world.servers = []
}

type Api = typeof import('@/lib/api')
// The list requests the page makes, taken from the module the test file mocked.
export type ListRequests = Pick<
  Api,
  'fetchSkills' | 'fetchRegistryTools' | 'fetchBuiltinTools' | 'fetchProviders' | 'fetchSkillMarketplaceStatus' | 'searchSkills' | 'fetchMcpServers' | 'fetchAgents'
>
export function serveWorld(api: ListRequests) {
  vi.mocked(api.fetchSkills).mockImplementation(async () => structuredClone(world.skills))
  vi.mocked(api.fetchRegistryTools).mockImplementation(async () => structuredClone(world.tools))
  vi.mocked(api.fetchMcpServers).mockImplementation(async () => structuredClone(world.servers))
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchAgents).mockResolvedValue([])
}

// A request that never answers: the state while a list is loading.
export const NEVER = () => new Promise<never>(() => {})

export function renderPage(Page: ComponentType) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  return render(
    <QueryClientProvider client={client}>
      <Page />
    </QueryClientProvider>,
  )
}

export async function openTab(name: RegExp) {
  await userEvent.setup().click(await screen.findByRole('tab', { name }))
}

// The disclosure button of a group on the visible tab: it is the button that has
// aria-expanded and is not a menu trigger (a menu trigger carries aria-haspopup).
export function groupDisclosures(): HTMLElement[] {
  return screen
    .queryAllByRole('button')
    .filter((button) => button.hasAttribute('aria-expanded') && !button.hasAttribute('aria-haspopup'))
}

// A header draws the group's name and its count as two spans with no space between them
// ("notion" + "4" reads "notion4" as DOM text), then a caret. A label is matched against the
// name alone, so /^notion\b/ finds that header and /^context7\b/ finds "context7" + "0".
export function groupName(button: HTMLElement): string {
  const name = (button.firstElementChild?.textContent ?? '').trim()
  return name || (button.textContent ?? '')
}

export async function groupDisclosure(label: RegExp): Promise<HTMLElement> {
  return waitFor(
    () => {
      const found = groupDisclosures().find((button) => label.test(groupName(button)))
      expect(found, `the group disclosure named ${label} is present`).toBeDefined()
      return found as HTMLElement
    },
    { timeout: 3000 },
  )
}

export async function openGroup(label: RegExp) {
  const trigger = await groupDisclosure(label)
  if (trigger.getAttribute('aria-expanded') === 'false') await userEvent.setup().click(trigger)
}

// What a person reads on a header or a tab: its words and its count, one space
// between them (the count may sit in its own element, so the DOM text can lack it).
export function reading(element: Element): string {
  return (element.textContent ?? '').replace(/\s+/g, ' ').trim().replace(/([^\s\d])(\d+)$/, '$1 $2')
}

// What a person reads on every group header of the visible tab, in lower case.
export function headerReadings(): string[] {
  return groupDisclosures().map((button) => reading(button).toLowerCase())
}

// The three Allow / Ask / Deny choices of one row, as the person sees them.
export const PERMISSION_WORDS = ['Allow', 'Ask', 'Deny'] as const
export function pressedChoices(row: HTMLElement): string[] {
  return PERMISSION_WORDS.filter(
    (word) => within(row).getByRole('button', { name: word }).getAttribute('aria-pressed') === 'true',
  )
}

// Radix Select, Dropdown and Sheet in jsdom need three browser functions jsdom lacks.
export function installOverlayPolyfills() {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.setPointerCapture ??= () => {}
  Element.prototype.releasePointerCapture ??= () => {}
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
}
