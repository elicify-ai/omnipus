// F24 / order 31: native gateway, real authenticated storage and real discovery.
// Only the external model and HTTP MCP server are scripted. No page.route,
// REST success substitute, fake approval, paid model or second browser login.
import http from 'node:http'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import { test as base, expect, type Page, type TestInfo, type Response } from '@playwright/test'
import type { ZodType } from 'zod'
import * as wire from '../../../src/lib/api/generated/schemas'
import type {
  AgentCreateRequestMain, AgentUpdateRequest, IntegrationProviderUpdateRequest,
  McpServerCreate, PermissionSettingsPatch, SkillCategory, SkillCategoryUpdateRequest, WorkspaceCreateRequest,
} from '../../../src/lib/api/generated/openapi-types'
import { GatewayProcess } from './gateway-process'
import { DEFAULT_OMNIPUS_BINARY } from '../setup'
import { E2E_FALLBACK } from './e2e-model'
import { e2eAgentModelFields } from './e2e-agent-model'
import { postReloadWithBearer } from './reload-bearer'
import { chatInput, waitForConnected } from './selectors'

export const GITHUB_TOOLS = ['list_issues', 'get_issue', 'create_issue', 'add_comment', 'list_repositories'] as const
export const GITHUB_KEYS = GITHUB_TOOLS.map((name) => `mcp_github_${name}`)
export const INSTALL_MARKDOWN = '---\nname: f24-upload\ndescription: Literal browser upload fixture.\nmetadata:\n  category: research\n---\n\nF24-UPLOAD-BYTES-7H\n'

// not-wire-format: external protocol observations and lifetime controls only.
async function startEdges() {
  const offered: string[][] = [], failures: string[] = []
  const mcpCalls: string[] = []
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (chunk: Buffer) => chunks.push(chunk))
    req.on('error', (error) => failures.push(error.message))
    req.on('end', () => {
      const json = (status: number, value: unknown) => {
        res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(value))
      }
      if (req.method === 'GET' && req.url === '/model/models') {
        json(200, { object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }); return
      }
      const isMcp = ['/github', '/github-edited', '/context7'].includes(req.url ?? '')
      // The actual StreamableClientTransport may open its optional notification
      // stream. Same protocol as pkg/mcp/ingest_bound_test.go: a JSON-only MCP
      // edge refuses GET with 405; it does not parse an empty GET body as JSON.
      if (isMcp && req.method === 'GET') { res.writeHead(405); res.end(); return }
      if (isMcp && req.method === 'DELETE') { res.writeHead(204); res.end(); return }
      if (req.method !== 'POST') {
        failures.push(`unexpected external method ${req.method} ${req.url}`)
        res.writeHead(405); res.end(); return
      }
      let body: Record<string, unknown>
      try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown> }
      catch { failures.push(`invalid external JSON at ${req.url}`); json(400, { error: 'invalid JSON' }); return }
      if (req.url === '/model/chat/completions') {
        const text = body.stream === true ? `F24 native turn ${offered.length + 1}.` : 'probe ok'
        if (body.stream !== true) {
          json(200, { id: 'f24-probe', object: 'chat.completion', model: E2E_FALLBACK.model, created: 1,
            choices: [{ index: 0, message: { role: 'assistant', content: text }, finish_reason: 'stop' }],
            usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }); return
        }
        // External OpenAI-compatible transport, not a hand-written gateway type.
        offered.push((Array.isArray(body.tools) ? body.tools : []).map((tool) => {
          const fn = (tool as Record<string, unknown>).function as Record<string, unknown>
          return String(fn.name)
        }))
        res.writeHead(200, { 'Content-Type': 'text/event-stream' })
        res.end('data: ' + JSON.stringify({ id: 'f24-stream', object: 'chat.completion.chunk',
          model: E2E_FALLBACK.model, created: 1, choices: [{ index: 0, delta: { role: 'assistant', content: text }, finish_reason: 'stop' }] }) + '\n\ndata: [DONE]\n\n')
        return
      }
      if (!isMcp) {
        failures.push(`unexpected external route ${req.method} ${req.url}`); json(404, { error: 'unexpected route' }); return
      }
      mcpCalls.push(`${req.url}:${String(body.method)}`)
      if (body.id === undefined || body.id === null) { res.writeHead(202); res.end(); return }
      const names = req.url === '/context7' ? ['lookup'] : [...GITHUB_TOOLS]
      const result = body.method === 'initialize'
        ? { protocolVersion: '2025-03-26', capabilities: { tools: {} }, serverInfo: { name: 'f24-native-edge', version: '1' } }
        : body.method === 'tools/list'
          ? { tools: names.map((name) => ({ name, description: `F24 ${name} description`, inputSchema: { type: 'object', properties: {} } })) }
          : undefined
      if (result === undefined) { failures.push(`unexpected MCP method ${String(body.method)}`); json(400, { error: 'unexpected method' }); return }
      res.setHeader('Mcp-Session-Id', 'f24-session')
      json(200, { jsonrpc: '2.0', id: body.id, result })
    })
  })
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address()
      if (!addr || typeof addr === 'string') { reject(new Error('F24 edge has no port')); return }
      resolve(`http://127.0.0.1:${addr.port}`)
    })
  })
  return { url, offered, failures, mcpCalls, close: async () => {
    await new Promise<void>((resolve, reject) => {
      server.close((error) => error ? reject(error) : resolve()); server.closeAllConnections()
    })
  } }
}

// not-wire-format: fixture resources, not application request/response shapes.
export type SkillsToolsNative = { gateway: GatewayProcess; edge: Awaited<ReturnType<typeof startEdges>> }

async function versionReceipt(native: SkillsToolsNative, info: TestInfo, browserName: string) {
  const candidate = process.env.OMNIPUS_EXPECT_BUILD_SHA ?? execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: fileURLToPath(new URL('../../..', import.meta.url)), encoding: 'utf8',
  }).trim()
  expect(candidate, 'candidate must be an exact commit, not a branch or dev stamp').toMatch(/^[a-f0-9]{40}$/)
  const served = await nativeGet(native, '/api/v1/version', wire.VersionResponse)
  expect(served.build_sha, 'BLOCKED: served binary has no commit receipt').toMatch(/^[a-f0-9]{7,40}$/)
  expect(candidate.startsWith(served.build_sha), 'served native binary must match this cut').toBe(true)
  const binary = process.env.OMNIPUS_BINARY ?? DEFAULT_OMNIPUS_BINARY
  await info.attach('F24-exact-version-setup-only', { contentType: 'application/json', body: Buffer.from(JSON.stringify({
    candidate, served, browserName, binary_sha256: createHash('sha256').update(fs.readFileSync(binary)).digest('hex'),
    note: 'Setup/version identity only. Product PASS requires the actual row assertions.',
  })) })
}

export const test = base.extend<{ native: SkillsToolsNative }>({
  native: async ({ browserName }, use, info) => {
    const edge = await startEdges()
    const builtin = fs.mkdtempSync(path.join(os.tmpdir(), 'f24-empty-builtin-'))
    let gateway: GatewayProcess | undefined
    try {
      gateway = await GatewayProcess.start({ apiBase: edge.url + '/model', model: E2E_FALLBACK.model,
        adminUsername: 'f24-tester', env: { OMNIPUS_BUILTIN_SKILLS: builtin } })
      const native = { gateway, edge }
      await versionReceipt(native, info, browserName)
      await use(native)
      expect(edge.failures, 'external protocol instrument failures are not product RED').toEqual([])
    } finally {
      try { if (gateway) await gateway.stop() }
      finally { await edge.close(); fs.rmSync(builtin, { recursive: true, force: true }) }
    }
  },
  page: async ({ browser, native }, use, info) => {
    // Copy the SAME one-login cookie jar, never sign in again or borrow shared auth.
    // This manual context intentionally does not trace credential entry. Capture
    // an actual failure image explicitly; do not claim inherited trace/video.
    const context = await browser.newContext({ storageState: await native.gateway.browserStorageState(), viewport: { width: 1280, height: 844 } })
    const page = await context.newPage()
    try { await use(page) } finally {
      try {
        if (info.status !== info.expectedStatus) await info.attach('F24-native-browser-failure', {
          contentType: 'image/png', body: await page.screenshot({ fullPage: true }),
        })
      } finally { await context.close() }
    }
  },
})

export async function nativeGet<T>(native: SkillsToolsNative, route: string, schema: ZodType<T>): Promise<T> {
  const response = await native.gateway.apiFetch('GET', route)
  expect(response.status, `native GET ${route}, not a stubbed success`).toBe(200)
  return schema.parse(response.body)
}
export async function nativeWrite<T>(native: SkillsToolsNative, method: 'POST' | 'PATCH' | 'PUT', route: string, input: unknown, schema: ZodType<T>): Promise<T> {
  const response = await native.gateway.apiFetch(method, route, input)
  expect(response.ok, `native ${method} ${route}, status ${response.status}`).toBe(true)
  return schema.parse(response.body)
}
export function skillsToolsTab(page: Page, tab: 'Skills' | 'Omnipus tools' | 'External tools') {
  // S-45 includes counts and the literal MCP qualifier in accessible tab names.
  const names = { Skills: /^Skills [0-9]+$/, 'Omnipus tools': /^Omnipus tools [0-9]+$/, 'External tools': /^External tools \(MCP\) [0-9]+$/ }
  return page.getByRole('tab', { name: names[tab] })
}
export async function openPage(page: Page, native: SkillsToolsNative, tab: 'Skills' | 'Omnipus tools' | 'External tools' = 'Skills') {
  await page.goto(`${native.gateway.baseURL}/#/skills`)
  await expect(page.getByRole('heading', { name: 'Skills & Tools', exact: true }), `BLOCKED: native page required by F24 ${tab} flows`).toBeVisible()
  await skillsToolsTab(page, tab).click()
  await expect(skillsToolsTab(page, tab)).toHaveAttribute('aria-selected', 'true')
}
export async function disclose(page: Page, name: RegExp) {
  const button = page.getByRole('button', { name }).and(page.locator('[aria-expanded]'))
  await expect(button).toHaveCount(1)
  if (await button.getAttribute('aria-expanded') === 'false') await button.click()
}

// Literal local files are Given inputs, not a fake publication result. C-5
// stores category OUTSIDE the folder: a file hint alone does not set that state.
export async function localSkill(native: SkillsToolsNative, slug: string, category: SkillCategory) {
  const dir = path.join(native.gateway.homeDir, 'skills', slug)
  fs.mkdirSync(dir, { recursive: true })
  const file = path.join(dir, 'SKILL.md')
  const body = `---\nname: ${slug}\ndescription: F24 ${slug} fixture.\n---\n\nF24-${slug}-INSTRUCTIONS\n`
  fs.writeFileSync(file, body)
  if (category !== 'uncategorised') {
    // C-21's real once-only writer, never a test-owned metadata store.
    const request: SkillCategoryUpdateRequest = { category }
    const stored = await nativeWrite(native, 'PUT', `/api/v1/skills/${slug}/category`, request, wire.Skill)
    expect(stored.id).toBe(slug)
    expect(stored.category).toBe(category)
  }
  expect(fs.readFileSync(file, 'utf8'), 'native category writes leave the complete input file unchanged').toBe(body)
}

// Do this AFTER startup SyncBuiltins, not a restart that would restore the set.
// Only explicitly named real built-ins survive; never replace their bytes/stamp.
export async function inventory(native: SkillsToolsNative, entries: ReadonlyArray<readonly [string, SkillCategory]>, preserveBuiltins: readonly string[] = []) {
  const initial = await nativeGet(native, '/api/v1/skills', wire.Skill.array())
  for (const slug of preserveBuiltins) {
    expect(entries.some(([name]) => name === slug), 'preserved built-in is an explicit requested input').toBe(true)
    expect(initial.find((skill) => skill.id === slug)?.source, `real startup-seeded ${slug}, never a fabricated built-in`).toBe('builtin')
  }
  const root = path.join(native.gateway.homeDir, 'skills')
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    if (entry.isDirectory() && !entry.name.startsWith('.') && !preserveBuiltins.includes(entry.name)) fs.rmSync(path.join(root, entry.name), { recursive: true })
  }
  for (const [slug, category] of entries) if (!preserveBuiltins.includes(slug)) await localSkill(native, slug, category)
  const actual = await nativeGet(native, '/api/v1/skills', wire.Skill.array())
  expect(actual.map((skill) => skill.id).sort(), 'positive native inventory receipt, no hidden extra shelf').toEqual(entries.map(([slug]) => slug).sort())
  for (const [slug, category] of entries) expect(actual.find((skill) => skill.id === slug)?.category).toBe(category)
}

export async function github(native: SkillsToolsNative) {
  const body: McpServerCreate = { name: 'github', transport: 'http', url: native.edge.url + '/github' }
  await nativeWrite(native, 'POST', '/api/v1/mcp-servers', body, wire.McpServer)
  await expect.poll(async () => (await nativeGet(native, '/api/v1/mcp-servers', wire.McpServer.array())).find((s) => s.id === 'github')?.tool_count).toBe(5)
  const tools = await nativeGet(native, '/api/v1/mcp-servers/github/tools', wire.McpServerToolsResponse)
  expect([...tools.tools].sort()).toEqual([...GITHUB_TOOLS].sort())
  const patch: PermissionSettingsPatch = { policies: Object.fromEntries(GITHUB_KEYS.map((name) => [name, 'ask'])) }
  await nativeWrite(native, 'PATCH', '/api/v1/security/permissions', patch, wire.PermissionSettings)
}

export async function missingContext7(native: SkillsToolsNative) {
  const configFile = path.join(native.gateway.homeDir, 'config.json')
  const cfg = JSON.parse(fs.readFileSync(configFile, 'utf8'))
  cfg.tools.mcp ??= {}; cfg.tools.mcp.enabled = true; cfg.tools.mcp.servers ??= {}
  cfg.tools.mcp.servers.context7 = { enabled: true, type: 'http', url: native.edge.url + '/context7', header_refs: { Authorization: 'CONTEXT7_KEY' } }
  fs.writeFileSync(configFile, JSON.stringify(cfg), { mode: 0o600 })
  const reloaded = await postReloadWithBearer(native.gateway.baseURL, native.gateway.reloadBearer())
  expect(reloaded.status, 'native reload preserves degraded missing-ref service').toBe(200)
  const servers = await nativeGet(native, '/api/v1/mcp-servers', wire.McpServer.array())
  expect(servers.find((s) => s.id === 'context7')?.status_reason).toBe('missing secret reference CONTEXT7_KEY')
}

export async function grantMia(native: SkillsToolsNative, skills: string[]) {
  const mia = await nativeGet(native, '/api/v1/agents/mia', wire.Agent)
  const body: AgentUpdateRequest = { revision: mia.revision, skills }
  const stored = await nativeWrite(native, 'PUT', '/api/v1/agents/mia', body, wire.Agent)
  expect(stored.skills?.slice().sort()).toEqual([...skills].sort())
  return stored
}
export async function workspaceFor(native: SkillsToolsNative, agentId: string) {
  const input: WorkspaceCreateRequest = { name: 'F24 private workspace', core_team: [agentId] }
  return nativeWrite(native, 'POST', '/api/v1/workspaces', input, wire.Workspace)
}
export async function editAgent(page: Page, native: SkillsToolsNative, workspaceId: string, agentId: string, name: string) {
  await page.goto(`${native.gateway.baseURL}/#/workspaces/${workspaceId}/chat?panel=team`)
  await page.getByTestId(`team-card-actions-${agentId}`).click()
  await page.getByRole('button', { name: `Edit ${name}`, exact: true }).click()
  await expect(page.getByTestId('agent-profile-sheet')).toBeVisible()
}
export async function consent(page: Page, native: SkillsToolsNative) {
  await expect(page.getByTestId('reauth-password-input')).toBeVisible()
  await page.getByTestId('reauth-password-input').fill(native.gateway.adminPassword)
  await page.getByRole('dialog', { name: 'Confirm your password', exact: true }).getByRole('button', { name: 'Confirm', exact: true }).click()
}
export function responseFor(page: Page, method: string, route: string): Promise<Response> {
  return page.waitForResponse((res) => res.request().method() === method && new URL(res.url()).pathname === route)
}
export async function saved(page: Page) {
  await expect(page.getByRole('status').filter({ hasText: /^Saved$/ })).toHaveText('Saved')
}

export async function nativeProbeAgent(native: SkillsToolsNative) {
  const input: AgentCreateRequestMain = { type: 'Main', name: 'F24 offered-tools probe', soul: 'Reply without tools.',
    ...e2eAgentModelFields('mock'), mcp_servers: [{ id: 'github' }] }
  return nativeWrite(native, 'POST', '/api/v1/agents', input, wire.Agent)
}
export async function modelTurn(page: Page, native: SkillsToolsNative, sessionId: string) {
  const before = native.edge.offered.length
  await page.goto(`${native.gateway.baseURL}/#/sessions/${sessionId}`)
  await waitForConnected(page)
  await expect(chatInput(page)).toBeEnabled()
  await chatInput(page).fill('F24: reply without calling tools.')
  await chatInput(page).press('Enter')
  await expect(page.getByText(`F24 native turn ${before + 1}.`, { exact: true })).toBeVisible()
  expect(native.edge.offered).toHaveLength(before + 1)
  return native.edge.offered[before]
}

// Native consent-only setup for S-64. Node fetch keeps cookies and the single-use
// re-auth token out of Playwright traces; no token/password appears in receipts.
export async function seedSearchDefault(native: SkillsToolsNative) {
  const gw = native.gateway, state = await gw.browserStorageState()
  const csrf = state.cookies.find((cookie) => cookie.name === 'csrf' || cookie.name === '__Host-csrf')
  expect(csrf, 'real CSRF fixture cookie').toBeDefined()
  if (!csrf) throw new Error('F24 missing native CSRF cookie')
  const headers = { 'Content-Type': 'application/json', Cookie: state.cookies.map((cookie) => `${cookie.name}=${cookie.value}`).join('; '), 'X-CSRF-Token': csrf.value }
  const auth = await fetch(gw.baseURL + '/api/v1/auth/reauth', { method: 'POST', headers, body: JSON.stringify({ password: gw.adminPassword }) })
  expect(auth.status, 'native consent setup status, never output the credential').toBe(200)
  const token = wire.ReAuthResponse.parse(await auth.json()).token
  const input: IntegrationProviderUpdateRequest = { kind: 'search', api_key: 'f24-placeholder-not-a-real-search-key', active: true }
  const update = await fetch(gw.baseURL + '/api/v1/integrations/providers/brave', { method: 'PUT',
    headers: { ...headers, 'X-Reauth-Token': token }, body: JSON.stringify(input) })
  expect(update.status, 'native initial default write').toBe(200)
  const result = wire.IntegrationProvidersResponse.parse(await update.json())
  expect(result.default_search).toBe('brave')
  expect(result.search.find((p) => p.id === 'duckduckgo')?.usable).toBe(true)
}
