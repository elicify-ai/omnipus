// Order 31 / CHECK F24. Spec-derived, real UI -> HTTP -> durable state -> UI.
// S-03, S-15, S-20, S-57, S-58, S-60–65, S-68–69 are independent cases.
// Also drive actual upload/review/install/grant and MCP edit/ACK/reopen.
// Private native gateway/account, hash route and exact binary-version receipt;
// only external model/MCP transports are scripted. No paid calls or REST stubs.
// CI EXECUTION REQUIRED: not run locally. Collection/setup is not RED or PASS.
// GREEN, mutations and proof-of-failability defer to independent CHECK.
//
// Fix round 4, Part H (findings F1, F5, F6, F9 of the frontend head e0f987fa0). Only LOCATORS were
// checked against the page source; every expected value is still the spec's own words
// (docs/internal/specs/skills-tools-redesign-spec.md at b27c64109) or the generated contract.
//   - A group is a section marked data-group-id with NO role and NO label, so it is found from its header
//     button (groupOf below), never as getByRole('group', { name }). The header words are matched ignoring
//     case: the page draws "Research" and styles it as capitals (architect ruling F5), where S-57 prints
//     "RESEARCH".
//   - A service header carries icon buttons named "Test <name>", "Edit <name>" and "Remove <name>"; the error
//     notice of a service that cannot connect carries buttons named exactly "Retry" and "Edit".
//   - S-58: spec US-7 AS8 adds "Approve this version" for an unverified skill under trust Ask, and a folder
//     put in by hand is unverified (C-5), so the Given fixes trust at Allow, where S-58's two-item menu is the
//     whole menu. The Ask menu is held by skills-tools-surfaces.spec.ts.
//   - S-61: the spec's Then is the notice with the reason, Retry and Edit. Retry is that service's Test and Edit
//     opens the header's Edit window (architect ruling F6). Whether the service reads connected after the
//     save is not in the spec and was not proven to connect in this fixture, so it is a hands-on row, not an
//     assertion here.
import fs from 'node:fs'
import path from 'node:path'
import { expect, type Locator, type Page } from '@playwright/test'
import * as wire from '../../src/lib/api/generated/schemas'
import type { McpServerUpdate, PermissionSettingsPatch, SessionCreateRequest, SkillInstallRequest } from '../../src/lib/api/generated/openapi-types'
import {
  test, GITHUB_TOOLS, GITHUB_KEYS, INSTALL_MARKDOWN, nativeGet, nativeWrite,
  openPage, skillsToolsTab, disclose, inventory, localSkill, github, missingContext7, grantMia,
  workspaceFor, editAgent, consent, responseFor, saved, nativeProbeAgent,
  modelTurn, seedSearchDefault, type SkillsToolsNative,
} from './fixtures/skills-tools-native'

test.use({ storageState: { cookies: [], origins: [] } })
test.setTimeout(240_000)
test.describe.configure({ retries: 0 }) // No paid-model variance to retry away.

// S-20 fixes the two granted names; the other eight are new fixture skills,
// never handwritten replacements for startup-owned built-ins.
const TEN_SKILLS = ['plan', 'deep-research', 'f24-one', 'f24-two', 'f24-three', 'f24-four', 'f24-five', 'f24-six', 'f24-seven', 'f24-eight'] as const
function toolRow(page: Page, key: string) { return page.getByTestId(`tool-row-${key}`) }
function skillRow(page: Page, key: string) { return page.getByTestId(`skill-row-${key}`) }
// A group header reads its name and then its count ("Tools 12"). The words are matched ignoring case, followed by a
// space, a digit or the end, so neither the capitals the page draws by style nor the way the count joins the name in
// the accessible name decides a result.
function header(name: string) {
  return new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?:\\s|\\d|$)`, 'i')
}
// A group is a section marked data-group-id with no role, found from its header button (a menu trigger also
// carries aria-expanded, so the name decides).
function groupOf(page: Page, name: RegExp) {
  return page.locator('section[data-group-id]').filter({ has: page.getByRole('button', { name }) })
}
function groupHeader(page: Page, name: RegExp) {
  return page.getByRole('button', { name }).and(page.locator('[aria-expanded]'))
}
function service(page: Page, id: string) { return groupOf(page, header(id)) }
// A More actions menu is read only once it is open: allTextContents does not wait for it. The order is not in the
// spec (S-58 and US-7 AS8 name which items, not their order), so the items are compared sorted.
async function menuItems(page: Page) {
  const menu = page.getByRole('menu')
  await expect(menu).toBeVisible()
  return (await menu.getByRole('menuitem').allTextContents()).map((text) => text.trim()).sort()
}
async function selected(row: Locator, value: 'Allow' | 'Ask' | 'Deny') {
  await expect(row.getByRole('button', { name: value, exact: true })).toHaveAttribute('aria-pressed', 'true')
}
async function tenInstalled(native: SkillsToolsNative) {
  await inventory(native, TEN_SKILLS.map((id) => [id, id === 'plan' ? 'business' : id === 'deep-research' ? 'research' : 'tools'] as const), ['plan', 'deep-research'])
}
async function external(page: Page, native: SkillsToolsNative, id = 'github') {
  await openPage(page, native, 'External tools')
  await disclose(page, header(id))
  await expect(service(page, id)).toBeVisible()
  return service(page, id)
}

// Each S-row has its own failing entry point; a heading alone can satisfy none.
test('S-03 github Set all Allow changes exactly five actual tools and survives reload', async ({ page, native }) => {
  await github(native)
  const group = await external(page, native)
  for (const key of GITHUB_KEYS) await selected(toolRow(page, key), 'Ask')
  const writes: unknown[] = []
  page.on('request', (request) => {
    if (request.method() === 'PATCH' && new URL(request.url()).pathname === '/api/v1/security/permissions') writes.push(request.postDataJSON())
  })
  const ack = responseFor(page, 'PATCH', '/api/v1/security/permissions')
  await group.getByRole('button', { name: /^Set all/i }).click()
  await page.getByRole('menuitem', { name: 'Allow', exact: true }).click()
  expect((await ack).status()).toBe(200)
  await saved(page)
  expect(writes).toEqual([{ policies: Object.fromEntries(GITHUB_KEYS.map((key) => [key, 'allow'])) }])
  for (const key of GITHUB_KEYS) await selected(toolRow(page, key), 'Allow')
  const readback = await nativeGet(native, '/api/v1/security/permissions', wire.PermissionSettings)
  expect(GITHUB_KEYS.map((key) => Reflect.get(readback.policies, key))).toEqual(['allow', 'allow', 'allow', 'allow', 'allow'])
  // The page starts on the Skills tab after a reload (the active tab is page state, not stored; the spec does not
  // say it survives a reload), so the External tools tab is opened again before the group is.
  await page.reload(); await openPage(page, native, 'External tools'); await disclose(page, header('github'))
  for (const key of GITHUB_KEYS) await selected(toolRow(page, key), 'Allow')
})

test('S-15 Skill infrastructure row has the exact help/link and no permission badges', async ({ page, native }) => {
  await openPage(page, native, 'Omnipus tools')
  await disclose(page, header('Skills'))
  const row = toolRow(page, 'Skill')
  await expect(row.getByText('Permissions are set per skill on the Skills tab.', { exact: true })).toBeVisible()
  await expect(row.getByRole('button', { name: /^(Allow|Ask|Deny)$/ })).toHaveCount(0)
  await row.getByRole('link', { name: /Skills tab/i }).click()
  await expect(skillsToolsTab(page, 'Skills')).toHaveAttribute('aria-selected', 'true')
})

test('S-20 Mia Tools shows exactly her two real grants out of ten installed', async ({ page, native }) => {
  await tenInstalled(native)
  const mia = await grantMia(native, ['plan', 'deep-research'])
  const ws = await workspaceFor(native, mia.id)
  await editAgent(page, native, ws.id, mia.id, mia.name)
  await page.getByTestId('tab-tools').click()
  await disclose(page, /^Skills(?:\s|$)/)
  const rows = page.getByTestId('agent-profile-sheet').getByTestId(/^skill-row-/)
  await expect(rows).toHaveCount(2)
  expect((await rows.evaluateAll((nodes) => nodes.map((n) => n.getAttribute('data-testid')))).sort()).toEqual(['skill-row-deep-research', 'skill-row-plan'])
  for (const key of ['plan', 'deep-research']) {
    const row = skillRow(page, key)
    for (const name of ['Allow', 'Ask', 'Deny']) await expect(row.getByRole('button', { name, exact: true })).toBeVisible()
  }
  const actual = await nativeGet(native, '/api/v1/agents/mia/tools', wire.AgentToolsResponse)
  expect(actual.skills.filter((s) => s.granted).map((s) => s.key).sort()).toEqual(['deep-research', 'plan'])
})

test('S-57 native inventory groups Research 1, Tools 12, Uncategorised 31 and no Blockchain', async ({ page, native }) => {
  await inventory(native, [
    ['research-one', 'research'],
    ...Array.from({ length: 12 }, (_, n) => [`tool-${n + 1}`, 'tools'] as const),
    ...Array.from({ length: 31 }, (_, n) => [`other-${n + 1}`, 'uncategorised'] as const),
  ])
  await openPage(page, native)
  // The spec prints the headers in capitals; the page draws the words as written and styles them as capitals, so
  // each header is matched ignoring case. The counts are the oracle: 1, 12 and 31, never 10 or 3.
  for (const name of [/^research\s*1$/i, /^tools\s*12$/i, /^uncategorised\s*31$/i]) await expect(groupHeader(page, name)).toBeVisible()
  await expect(page.getByRole('button', { name: header('BLOCKCHAIN') }), 'no empty or unlisted category draws a group').toHaveCount(0)
})

test('S-58 actual built-in plan can only be copied; installed weather can be copied or removed', async ({ page, native }) => {
  // Use the real startup-seeded plan, never fake its built-in stamp/content.
  const initial = await nativeGet(native, '/api/v1/skills', wire.Skill.array())
  expect(initial.find((s) => s.id === 'plan')?.source).toBe('builtin')
  await localSkill(native, 'weather', 'tools')
  // Given trust Allow. US-7 AS8 adds "Approve this version" for an unverified skill under trust Ask, and a
  // folder put in by hand is unverified (C-5), so under the default (Ask) weather's menu has a third item that
  // S-58 does not name. With trust at Allow that item is not offered and S-58's two items are the whole menu.
  const allow: PermissionSettingsPatch = { skill_trust: 'allow' }
  expect((await nativeWrite(native, 'PATCH', '/api/v1/security/permissions', allow, wire.PermissionSettings)).skill_trust).toBe('allow')
  await openPage(page, native)
  // The spec's built-in category decision puts plan in Business, not Tools.
  await disclose(page, header('BUSINESS'))
  await skillRow(page, 'plan').getByRole('button', { name: /More actions/i }).click()
  expect(await menuItems(page)).toEqual(['Make a copy'])
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu')).toBeHidden()
  await disclose(page, header('TOOLS'))
  await skillRow(page, 'weather').getByRole('button', { name: /More actions/i }).click()
  expect(await menuItems(page)).toEqual(['Make a copy', 'Remove'])
})

test('S-60 one switch click persists Off without confirmation and removes real model offers', async ({ page, native }) => {
  await github(native)
  const patch: PermissionSettingsPatch = { visibility: Object.fromEntries(GITHUB_KEYS.map((key) => [key, 'full'])) }
  await nativeWrite(native, 'PATCH', '/api/v1/security/permissions', patch, wire.PermissionSettings)
  const agent = await nativeProbeAgent(native), ws = await workspaceFor(native, agent.id)
  const input: SessionCreateRequest = { type: 'chat', agent_id: agent.id, workspace_id: ws.id }
  const first = await nativeWrite(native, 'POST', '/api/v1/sessions', input, wire.Session)
  const before = await modelTurn(page, native, first.id)
  expect(before.filter((key) => key.startsWith('mcp_github_')).sort(), 'positive actual model observer before Off').toEqual([...GITHUB_KEYS].sort())
  const group = await external(page, native)
  const calls: unknown[] = []
  page.on('request', (request) => {
    if (request.method() === 'PATCH' && new URL(request.url()).pathname === '/api/v1/mcp-servers/github') calls.push(request.postDataJSON())
  })
  const ack = responseFor(page, 'PATCH', '/api/v1/mcp-servers/github')
  await group.getByRole('switch').click()
  expect((await ack).status()).toBe(200)
  await expect(group.getByRole('switch')).toHaveAttribute('aria-checked', 'false')
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  expect(calls).toEqual([{ enabled: false }])
  expect((await nativeGet(native, '/api/v1/mcp-servers', wire.McpServer.array())).find((s) => s.id === 'github')?.enabled).toBe(false)
  const second = await nativeWrite(native, 'POST', '/api/v1/sessions', input, wire.Session)
  const after = await modelTurn(page, native, second.id)
  expect(after, 'model observer still sees ordinary native infrastructure').toContain('ToolSearch')
  expect(after.filter((key) => key.startsWith('mcp_github_'))).toEqual([])
})

test('S-61 real missing CONTEXT7_KEY shows cause with Retry and Edit; Retry attempts native test and Edit saves the header', async ({ page, native }) => {
  await missingContext7(native)
  const group = await external(page, native, 'context7')
  // US-8 AS3, S-61: an error notice shows the server's reason with Retry and Edit. The notice is the only alert in
  // the group; its two buttons are named exactly "Retry" and "Edit" (the header's icon buttons read "Edit context7").
  const notice = group.getByRole('alert')
  await expect(notice).toContainText('missing secret reference CONTEXT7_KEY')
  await expect(notice.getByRole('button', { name: 'Retry', exact: true })).toBeVisible()
  await expect(notice.getByRole('button', { name: 'Edit', exact: true })).toBeVisible()
  // Retry is that service's Test (architect ruling F6): the native gateway is asked again, and the service still
  // cannot connect, so the reason stays on screen.
  const testReply = responseFor(page, 'POST', '/api/v1/mcp-servers/context7/test')
  await notice.getByRole('button', { name: 'Retry', exact: true }).click()
  const retried = await testReply
  expect(retried.status()).toBe(200)
  const retry = wire.McpServerTestResponse.parse(await retried.json())
  expect(retry.success).toBe(false)
  expect(retry.message).toContain('CONTEXT7_KEY')
  await expect(notice).toContainText('missing secret reference CONTEXT7_KEY')
  // Edit opens the same Edit window as the header's Edit (architect ruling F6).
  await notice.getByRole('button', { name: 'Edit', exact: true }).click()
  const panel = page.getByTestId('mcp-sheet')
  await expect(panel).toBeVisible()
  const advanced = panel.getByTestId('advanced-disclosure-trigger')
  if (await advanced.getAttribute('aria-expanded') === 'false') await advanced.click()
  // The real modal starts with one blank row; secret VALUES are never read back.
  await panel.getByTestId('header-key-0').fill('Authorization')
  await panel.getByTestId('header-value-0').fill('f24-placeholder-not-a-real-secret')
  const ack = responseFor(page, 'PATCH', '/api/v1/mcp-servers/context7')
  await panel.getByRole('button', { name: 'Save changes', exact: true }).click()
  const response = await ack
  expect(response.status()).toBe(200)
  const patch: McpServerUpdate = response.request().postDataJSON()
  expect(wire.McpServerUpdate.parse(patch)).toEqual(patch)
  expect(patch.headers).toEqual({ Authorization: 'f24-placeholder-not-a-real-secret' })
  expect(patch.url ?? native.edge.url + '/context7').toBe(native.edge.url + '/context7')
  expect(Object.keys(patch).filter((key) => key !== 'url')).toEqual(['headers'])
  await expect(panel).toBeHidden()
  // Architect ruling F6: this test checks that the save is STORED. It does not assert that the notice clears or
  // that the service reads connected afterwards: the spec's S-61 does not say so, and nothing here proves the
  // corrected settings connect, so that outcome is a hands-on row of the Part H pack.
  const stored = (await nativeGet(native, '/api/v1/mcp-servers', wire.McpServer.array())).find((s) => s.id === 'context7')
  expect(stored?.url).toBe(native.edge.url + '/context7')
  expect(stored?.header_names).toEqual(['Authorization'])
  // The header's own Edit reopens the window whether or not the notice is still drawn.
  await group.getByRole('button', { name: 'Edit context7', exact: true }).click()
  await expect(panel.getByTestId('header-names-set')).toContainText('Authorization')
  await expect(panel.getByTestId('header-value-0')).toHaveValue('')
  await panel.getByRole('button', { name: /^Close/i }).click()
})

test('S-62 exact Remove confirmation then Cancel sends no DELETE and preserves native service', async ({ page, native }) => {
  await github(native)
  const group = await external(page, native)
  const deletes: string[] = []
  page.on('request', (r) => { if (r.method() === 'DELETE' && new URL(r.url()).pathname === '/api/v1/mcp-servers/github') deletes.push(r.url()) })
  await group.getByRole('button', { name: 'Remove github', exact: true }).click()
  const dialog = page.getByRole('alertdialog')
  await expect(dialog).toContainText('Remove this service? Agents that use it lose its tools.')
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(dialog).toBeHidden()
  // Native read completion is a causal observation after Cancel, not a sleep.
  expect((await nativeGet(native, '/api/v1/mcp-servers', wire.McpServer.array())).filter((s) => s.id === 'github')).toHaveLength(1)
  expect(deletes).toEqual([])
  await expect(group).toBeVisible()
})

test('S-63 local program warning focuses the safe main action and returns to network without spawning', async ({ page, native }) => {
  await openPage(page, native, 'External tools')
  await page.getByRole('button', { name: 'Add service', exact: true }).click()
  await page.getByRole('radio', { name: 'A local program', exact: true }).click()
  const dialog = page.getByRole('alertdialog')
  await expect(dialog).toContainText('This runs a program on your server')
  const safe = dialog.getByRole('button', { name: 'Use a network address instead', exact: true })
  await expect(safe).toBeFocused()
  await page.mouse.move(0, 0) // Preserve keyboard focus; do not sample a hover colour.
  // S-63's main-button oracle is rendered visual emphasis, not an invented
  // data-variant attribute. Resolve the catalog's primary accent token in this
  // browser/theme and require the safe action to be the ONLY accent-filled one.
  await expect.poll(async () => dialog.getByRole('button').evaluateAll((buttons) => {
    const probe = document.createElement('span')
    probe.hidden = true; probe.style.backgroundColor = 'var(--color-accent)'
    document.body.append(probe)
    const accent = getComputedStyle(probe).backgroundColor
    const token = getComputedStyle(probe).getPropertyValue('--color-accent').trim()
    probe.remove()
    if (!token || accent === 'rgba(0, 0, 0, 0)') throw new Error('fixture UNVERIFIED: primary accent token did not resolve to a visible fill')
    return buttons.filter((button) => getComputedStyle(button).backgroundColor === accent).map((button) => button.textContent?.trim())
  })).toEqual(['Use a network address instead'])
  await safe.click()
  await expect(dialog).toBeHidden()
  await expect(page.getByTestId('mode-network')).toHaveAttribute('aria-checked', 'true')
  expect(await nativeGet(native, '/api/v1/mcp-servers', wire.McpServer.array())).toEqual([])
})

test('S-64 real Web & Search side panel saves a new default with consent and reopens it', async ({ page, native }) => {
  await seedSearchDefault(native)
  await openPage(page, native, 'Omnipus tools')
  await disclose(page, header('Web & Search'))
  await groupOf(page, header('Web & Search')).getByRole('button', { name: 'Settings', exact: true }).click()
  const panel = page.getByRole('dialog', { name: /Web & Search/i })
  await expect(panel).toBeVisible()
  await expect(panel.getByTestId('default-search-card')).toContainText('Default search')
  await expect(panel.getByTestId('fallback-search-card')).toContainText('Fallback')
  await panel.getByTestId('default-search-card').getByRole('button', { name: 'Change', exact: true }).click()
  await panel.getByRole('combobox', { name: 'Default search', exact: true }).click()
  await page.getByRole('option', { name: 'DuckDuckGo', exact: true }).click()
  const ack = responseFor(page, 'PUT', '/api/v1/integrations/providers/duckduckgo')
  await consent(page, native)
  const response = await ack
  expect(response.status()).toBe(200)
  expect(response.request().postDataJSON()).toEqual({ kind: 'search', active: true })
  expect(wire.IntegrationProviderUpdateRequest.parse(response.request().postDataJSON())).toEqual({ kind: 'search', active: true })
  expect(wire.IntegrationProvidersResponse.parse(await response.json()).default_search).toBe('duckduckgo')
  expect((await nativeGet(native, '/api/v1/integrations/providers', wire.IntegrationProvidersResponse)).default_search).toBe('duckduckgo')
  await panel.getByRole('button', { name: /^Close/i }).click()
  await page.reload(); await openPage(page, native, 'Omnipus tools'); await disclose(page, header('Web & Search'))
  await groupOf(page, header('Web & Search')).getByRole('button', { name: 'Settings', exact: true }).click()
  await expect(panel.getByTestId('default-search-card')).toContainText('DuckDuckGo')
})

test('S-65 exact Protection and Shell links navigate to current Settings tabs, never tool permissions', async ({ page, native }) => {
  await openPage(page, native, 'Omnipus tools')
  await expect(page.getByText('Open Settings to change Auto-approve and God Mode.', { exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Open Settings', exact: true }).click()
  await expect(page).toHaveURL(/#\/settings\?tab=protection$/)
  await openPage(page, native, 'Omnipus tools'); await disclose(page, header('Shell'))
  await page.getByRole('link', { name: 'Shell safety rules (Settings)', exact: true }).click()
  await expect(page).toHaveURL(/#\/settings\?tab=sandbox$/)
  await openPage(page, native, 'Omnipus tools')
  const settings = await page.locator('main a[href*="settings"]').evaluateAll((links) => links.map((a) => a.getAttribute('href')))
  expect(settings.length, 'positive link scanner sees the existing Settings links').toBeGreaterThan(0)
  expect(settings.filter((href) => href && /tool.?permissions|tab=tools|tab=security/.test(href))).toEqual([])
})

test('S-68 390x844 every tab has card lines, all visible controls meet 44x44, no sideways scrolling', async ({ page, native }) => {
  await inventory(native, [['f24-phone', 'tools']])
  await github(native)
  await page.setViewportSize({ width: 390, height: 844 })
  for (const tab of ['Skills', 'Omnipus tools', 'External tools'] as const) {
    await openPage(page, native, tab)
    const group = tab === 'Skills' ? header('TOOLS') : tab === 'Omnipus tools' ? header('Shell') : header('github')
    await disclose(page, group)
    const rows = page.locator('[data-testid^="tool-row-"], [data-testid^="skill-row-"]').filter({ visible: true })
    expect(await rows.count(), `${tab} actually has visible cards`).toBeGreaterThan(0)
    for (const row of await rows.all()) {
      await expect(row.getByText('Permission', { exact: true })).toBeVisible()
      await expect(row.getByText('What the model sees', { exact: true })).toBeVisible()
    }
    const measurements = await page.locator('main').evaluate((main) => {
      const controls = Array.from(main.querySelectorAll<HTMLElement>('button, input:not([type=hidden]), select, textarea, a[href], [role=tab], [role=switch], [role=radio], [role=combobox]'))
        .filter((el) => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden')
      return controls.map((el) => ({ label: el.getAttribute('aria-label') ?? el.textContent, width: el.getBoundingClientRect().width, height: el.getBoundingClientRect().height }))
    })
    expect(measurements.length, 'touch-target scanner sees real controls').toBeGreaterThan(0)
    expect(measurements.filter((item) => item.width < 44 || item.height < 44), `${tab}: every target, not a selected sample`).toEqual([])
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  }
})

test('S-69 actual Tab then Enter chooses Ask on bash and the live status announces Saved', async ({ page, native }) => {
  const input: PermissionSettingsPatch = { policies: { bash: 'allow' } }
  await nativeWrite(native, 'PATCH', '/api/v1/security/permissions', input, wire.PermissionSettings)
  await openPage(page, native, 'Omnipus tools'); await disclose(page, header('Shell'))
  const row = toolRow(page, 'bash')
  await row.getByRole('button', { name: 'Allow', exact: true }).focus()
  await page.keyboard.press('Tab')
  await expect(row.getByRole('button', { name: 'Ask', exact: true })).toBeFocused()
  const ack = responseFor(page, 'PATCH', '/api/v1/security/permissions')
  await page.keyboard.press('Enter')
  expect((await ack).status()).toBe(200)
  await selected(row, 'Ask')
  await saved(page)
  const live = page.getByRole('status').filter({ hasText: /^Saved$/ })
  expect(await live.evaluate((el) => el.getAttribute('aria-live') ?? 'polite')).toBe('polite') // role=status has implicit polite semantics.
  expect(Reflect.get((await nativeGet(native, '/api/v1/security/permissions', wire.PermissionSettings)).policies, 'bash')).toBe('ask')
})

test('F24 integration: real upload, review, category, Install, exact bytes and person grant survive reopening', async ({ page, native }) => {
  await openPage(page, native)
  await page.getByRole('button', { name: 'New skill', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Upload a skill', exact: true }).click()
  const staged = responseFor(page, 'POST', '/api/v1/skills/install/review')
  await page.locator('input[type=file]').setInputFiles({ name: 'f24-upload.md', mimeType: 'text/markdown', buffer: Buffer.from(INSTALL_MARKDOWN) })
  const reviewed = await staged
  expect(reviewed.status()).toBe(200)
  const review = wire.SkillInstallReview.parse(await reviewed.json())
  expect(review.name).toBe('f24-upload')
  const stagedInput: SkillInstallRequest = reviewed.request().postDataJSON()
  expect(stagedInput.upload_id).toMatch(/^media:\/\//)
  expect(stagedInput.review_id).toBeUndefined()
  await page.getByRole('combobox', { name: 'Category', exact: true }).click()
  await page.getByRole('option', { name: 'Research', exact: true }).click()
  const installed = responseFor(page, 'POST', '/api/v1/skills/install')
  await page.getByRole('button', { name: 'Install skill', exact: true }).click()
  const response = await installed
  expect(response.status()).toBe(200)
  expect(response.request().postDataJSON()).toEqual({ upload_id: stagedInput.upload_id, review_id: review.review_id, category: 'research' })
  await expect(page.getByText('Installed f24-upload.', { exact: true })).toBeVisible()
  const row = (await nativeGet(native, '/api/v1/skills', wire.Skill.array())).find((s) => s.id === 'f24-upload')
  expect(row?.revision).toBe(review.revision)
  expect(row?.category).toBe('research')
  expect(fs.readFileSync(path.join(native.gateway.homeDir, 'skills', 'f24-upload', 'SKILL.md'), 'utf8')).toBe(INSTALL_MARKDOWN)
  const mia = await nativeGet(native, '/api/v1/agents/mia', wire.Agent), ws = await workspaceFor(native, 'mia')
  await editAgent(page, native, ws.id, mia.id, mia.name)
  await page.getByTestId('tab-skills').click()
  const grantAck = responseFor(page, 'PUT', '/api/v1/agents/mia')
  await page.getByTestId('skill-checkbox-f24-upload').check()
  expect((await grantAck).status()).toBe(200)
  const stored = await nativeGet(native, '/api/v1/agents/mia', wire.Agent)
  expect(stored.skills?.slice().sort()).toEqual([...new Set([...(mia.skills ?? []), 'f24-upload'])].sort())
  // Generated Agent's static map carrier is {}; its runtime schema validates
  // string values. Compare the whole map without indexing or inventing a type.
  expect(stored.skill_grants).toEqual({
    ...(mia.skill_grants ?? {}),
    'f24-upload': expect.stringMatching(/\S+/),
  })
  await page.getByTestId('agent-profile-sheet').getByRole('button', { name: /^Close/i }).click()
  await editAgent(page, native, ws.id, mia.id, mia.name); await page.getByTestId('tab-skills').click()
  await expect(page.getByTestId('skill-checkbox-f24-upload')).toBeChecked()
})

test('F24 integration: MCP Edit sends the exact native PATCH, waits for ACK and reopens stored URL/tools', async ({ page, native }) => {
  await github(native)
  const group = await external(page, native)
  await group.getByRole('button', { name: 'Edit github', exact: true }).click()
  const panel = page.getByTestId('mcp-sheet'), url = native.edge.url + '/github-edited'
  await panel.getByTestId('network-url').fill(url)
  const ack = responseFor(page, 'PATCH', '/api/v1/mcp-servers/github')
  await panel.getByRole('button', { name: 'Save changes', exact: true }).click()
  const response = await ack
  expect(response.status()).toBe(200)
  const expected: McpServerUpdate = { url }
  expect(response.request().postDataJSON()).toEqual(expected)
  expect(wire.McpServerUpdate.parse(response.request().postDataJSON())).toEqual(expected)
  expect(wire.McpServer.parse(await response.json()).url).toBe(url)
  await expect(panel).toBeHidden()
  expect((await nativeGet(native, '/api/v1/mcp-servers', wire.McpServer.array())).find((s) => s.id === 'github')?.url).toBe(url)
  await page.reload(); await openPage(page, native, 'External tools'); await disclose(page, header('github'))
  await service(page, 'github').getByRole('button', { name: 'Edit github', exact: true }).click()
  await expect(panel.getByTestId('network-url')).toHaveValue(url)
  await panel.getByRole('button', { name: /^Close/i }).click()
  expect((await nativeGet(native, '/api/v1/mcp-servers/github/tools', wire.McpServerToolsResponse)).tools.sort()).toEqual([...GITHUB_TOOLS].sort())
  expect(native.edge.mcpCalls).toContain('/github-edited:tools/list')
})
