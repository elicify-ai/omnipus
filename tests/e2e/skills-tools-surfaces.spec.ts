// Design-system BROWSER checks for the Skills & Tools page: one real-browser test for
// each of the five surfaces that scripts/design-system/catalog.mjs discovers in
//   src/components/skills/SkillsToolsPage.tsx   #tab:skills  #tab:tools  #tab:external  #overlay:1
//   src/components/skills/SkillRowActions.tsx   #overlay:1
// (the unit and interaction checks of the same surfaces are the vitest files under
// tests/design-system/surfaces/). Fix round 4, Part G.
//
// Same instrument as tests/e2e/skills-tools.spec.ts: a private native gateway with its
// real storage and real discovery, one browser login, an exact-version receipt. Only the
// external model and the HTTP MCP server are scripted. No page.route, no REST success
// substitute, no paid call. Every oracle is the spec's own words
// (docs/internal/specs/skills-tools-redesign-spec.md at b27c64109) or the generated
// contract; no expected value was read from the page code. Only the LOCATORS were checked
// against the page source at the frontend head 1a140710f: a group is a section marked
// data-group-id with no role (so it is found from its header button, groupOf below, not by
// a role named after the service), its header is a button with aria-expanded, the
// header words are matched ignoring case because the page draws "Research" where S-57
// prints "RESEARCH", rows carry data-testid tool-row-<key> / skill-row-<key>, the search
// cards carry data-testid default-search-card / fallback-search-card, and the service
// header buttons are named "Test <name>", "Edit <name>" and "Remove <name>".
//
// CI EXECUTION REQUIRED: not run locally. Collection/setup is not RED or PASS.
// GREEN, mutations and proof-of-failability defer to independent CHECK.
//
// Catalog nomination (the test title is the catalog check's `test`):
//   SkillsToolsPage.tsx#tab:skills      surface:src-components-skills-skillstoolspage-tsx-tab-skills:browser
//   SkillsToolsPage.tsx#tab:tools       surface:src-components-skills-skillstoolspage-tsx-tab-tools:browser
//   SkillsToolsPage.tsx#tab:external    surface:src-components-skills-skillstoolspage-tsx-tab-external:browser
//   SkillsToolsPage.tsx#overlay:1       surface:src-components-skills-skillstoolspage-tsx-overlay-1:browser
//   SkillRowActions.tsx#overlay:1       surface:src-components-skills-skillrowactions-tsx-overlay-1:browser
//
// Not asserted here, on purpose: the polite "Saved" status region (probed in the vitest
// file skills-tools-a11y-spec-gaps.test.tsx and by S-69 of skills-tools.spec.ts), and the
// sentence of a refused category write on screen (the dialog belongs to the row, and the
// row moves to another group when the list is read again, so a browser cannot hold it
// still without a request substitute; the jsdom interaction file holds the list read).
import { expect, type Locator, type Page } from '@playwright/test'
import type { PermissionSettingsPatch } from '../../src/lib/api/generated/openapi-types'
import * as wire from '../../src/lib/api/generated/schemas'
import {
  test, nativeGet, nativeWrite, openPage, skillsToolsTab, disclose, inventory, github, responseFor, seedSearchDefault,
} from './fixtures/skills-tools-native'

test.use({ storageState: { cookies: [], origins: [] } })
test.setTimeout(240_000)
test.describe.configure({ retries: 0 }) // No paid-model variance to retry away.

function toolRow(page: Page, key: string) { return page.getByTestId(`tool-row-${key}`) }
function skillRow(page: Page, key: string) { return page.getByTestId(`skill-row-${key}`) }
// A header reads its name and then its count ("TOOLS 12"). The spec prints the names in capitals and
// the page may draw them in another case, with or without a space before the count in the accessible
// name, so a header is found by its words, ignoring case, followed by a space, a digit or the end.
function header(name: string) {
  return new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?:\\s|\\d|$)`, 'i')
}
// A group header is the disclosure button; a menu trigger also carries aria-expanded, so the name decides.
function groupHeader(page: Page, name: RegExp) {
  return page.getByRole('button', { name }).and(page.locator('[aria-expanded]'))
}
// The group that holds a header. The page draws a group as a section marked data-group-id and gives it
// no role, so the group is found from its header button and not by a role named after the service.
function groupOf(page: Page, name: RegExp) {
  return page.locator('section[data-group-id]').filter({ has: page.getByRole('button', { name }) })
}
function choiceButton(row: Locator, value: 'Allow' | 'Ask' | 'Deny') { return row.getByRole('button', { name: value, exact: true }) }
async function selected(row: Locator, value: 'Allow' | 'Ask' | 'Deny') {
  await expect(choiceButton(row, value)).toHaveAttribute('aria-pressed', 'true')
}
// Every permission write the browser itself makes (the native helpers use their own client).
function permissionWrites(page: Page): unknown[] {
  const writes: unknown[] = []
  page.on('request', (request) => {
    if (request.method() === 'PATCH' && new URL(request.url()).pathname === '/api/v1/security/permissions') writes.push(request.postDataJSON())
  })
  return writes
}

test('Skills tab surface: categories start collapsed with their counts and Deny on one skill is saved alone and survives a reload', async ({ page, native }) => {
  // S-57, US-6 AS2: grouped by category with counts, collapsed; S-45: the tab reads the number of skills.
  await inventory(native, [['research-one', 'research'], ['tool-one', 'tools'], ['tool-two', 'tools']])
  await openPage(page, native)
  await expect(skillsToolsTab(page, 'Skills')).toHaveAccessibleName('Skills 3')
  for (const name of [/^research\s*1$/i, /^tools\s*2$/i]) await expect(groupHeader(page, name)).toHaveAttribute('aria-expanded', 'false')
  await expect(skillRow(page, 'tool-one'), 'a collapsed group shows no row').toBeHidden()

  // US-2 AS1, C-2: one click on Deny changes that one skill, and only the key that changed is sent.
  await disclose(page, header('TOOLS'))
  const row = skillRow(page, 'tool-one')
  await expect(row).toBeVisible()
  const before = await nativeGet(native, '/api/v1/security/permissions', wire.PermissionSettings)
  const writes = permissionWrites(page)
  const ack = responseFor(page, 'PATCH', '/api/v1/security/permissions')
  await choiceButton(row, 'Deny').click()
  expect((await ack).status()).toBe(200)
  await selected(row, 'Deny')
  expect(writes).toEqual([{ skill_policies: { 'tool-one': 'deny' } }])
  const after = await nativeGet(native, '/api/v1/security/permissions', wire.PermissionSettings)
  expect(after.skill_policies, 'the stored skill permissions differ from before in that one key').toEqual({ ...before.skill_policies, 'tool-one': 'deny' })

  // The choice is stored, not only drawn: it is still there after a reload, and the other skill is not Deny.
  await page.reload()
  await openPage(page, native)
  await disclose(page, header('TOOLS'))
  await selected(skillRow(page, 'tool-one'), 'Deny')
  await expect(choiceButton(skillRow(page, 'tool-two'), 'Deny')).toHaveAttribute('aria-pressed', 'false')
})

test('Omnipus tools tab surface: groups start collapsed and Deny on bash is saved alone and survives a reload', async ({ page, native }) => {
  // US-6 AS2, S-46: groups start collapsed; S-45: the tab reads the number of tools (openPage matches "Omnipus tools <n>").
  await openPage(page, native, 'Omnipus tools')
  for (const name of [header('Shell'), header('Web & Search')]) await expect(groupHeader(page, name)).toHaveAttribute('aria-expanded', 'false')
  await expect(toolRow(page, 'bash'), 'a collapsed group shows no row').toBeHidden()

  // US-1 AS1, C-2: one click on Deny changes that one tool, and only the key that changed is sent.
  await disclose(page, header('Shell'))
  const row = toolRow(page, 'bash')
  await expect(row).toBeVisible()
  const before = await nativeGet(native, '/api/v1/security/permissions', wire.PermissionSettings)
  const writes = permissionWrites(page)
  const ack = responseFor(page, 'PATCH', '/api/v1/security/permissions')
  await choiceButton(row, 'Deny').click()
  expect((await ack).status()).toBe(200)
  await selected(row, 'Deny')
  expect(writes).toEqual([{ policies: { bash: 'deny' } }])
  const after = await nativeGet(native, '/api/v1/security/permissions', wire.PermissionSettings)
  expect(after.policies, 'the stored tool permissions differ from before in that one key').toEqual({ ...before.policies, bash: 'deny' })

  await page.reload()
  await openPage(page, native, 'Omnipus tools')
  await disclose(page, header('Shell'))
  await selected(toolRow(page, 'bash'), 'Deny')
})

test('External tools tab surface: the service header carries the switch Test Edit and Remove and Test reaches the service', async ({ page, native }) => {
  await github(native)
  await openPage(page, native, 'External tools')
  // S-45: the tab reads the number of external tools (the five tools of the one service).
  await expect(skillsToolsTab(page, 'External tools')).toHaveAccessibleName('External tools (MCP) 5')
  await disclose(page, header('github'))
  const group = groupOf(page, header('github'))
  await expect(group).toBeVisible()

  // US-8 AS2, FR-046: the header carries the switch (showing the stored state), Test, Edit and Remove.
  await expect(group.getByRole('switch')).toHaveAttribute('aria-checked', 'true')
  for (const word of ['Test', 'Edit', 'Remove']) {
    await expect(group.getByRole('button', { name: new RegExp(`^${word}\\b`) }), `one ${word} control in the header`).toHaveCount(1)
  }

  // Test is an on-demand connectivity probe (McpServerTestResponse): it connects and reports the tools it saw.
  const probe = responseFor(page, 'POST', '/api/v1/mcp-servers/github/test')
  await group.getByRole('button', { name: /^Test\b/ }).click()
  const reply = await probe
  expect(reply.status()).toBe(200)
  const result = wire.McpServerTestResponse.parse(await reply.json())
  expect(result.success, result.message).toBe(true)
  expect(result.tool_count, 'the five tools the service offers').toBe(5)

  // A test changes nothing: the service is still on and connected.
  const stored = (await nativeGet(native, '/api/v1/mcp-servers', wire.McpServer.array())).find((server) => server.id === 'github')
  expect(stored?.enabled).toBe(true)
  expect(stored?.status).toBe('connected')
})

test('Web & Search side panel surface: Settings opens the panel with both cards and Escape closes it without a save', async ({ page, native }) => {
  await seedSearchDefault(native) // a stored default, so the cards have something to show
  await openPage(page, native, 'Omnipus tools')
  await disclose(page, header('Web & Search'))
  const integrationWrites: string[] = []
  page.on('request', (request) => {
    const { pathname } = new URL(request.url())
    if (request.method() !== 'GET' && pathname.startsWith('/api/v1/integrations/')) integrationWrites.push(`${request.method()} ${pathname}`)
  })

  // US-9 AS1, S-64, FR-048: until Settings is pressed no panel is open; Settings opens a side panel (a dialog).
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const settings = groupOf(page, header('Web & Search')).getByRole('button', { name: 'Settings', exact: true })
  await settings.focus()
  await page.keyboard.press('Enter')
  const panel = page.getByRole('dialog', { name: /Web & Search/i })
  await expect(panel).toBeVisible()
  // Design system sheet contract (the SheetKeyboard story): Enter on the opener puts focus on a button inside.
  await expect.poll(() => panel.evaluate((el) => el.contains(document.activeElement) && document.activeElement?.tagName === 'BUTTON')).toBe(true)
  await expect(panel.getByTestId('default-search-card')).toContainText('Default search')
  await expect(panel.getByTestId('fallback-search-card')).toContainText('Fallback')

  // Escape closes it (WAI-ARIA dialog pattern), and the panel opens again with the cards.
  await page.keyboard.press('Escape')
  await expect(panel).toBeHidden()
  await settings.click()
  await expect(panel).toBeVisible()
  await expect(panel.getByTestId('default-search-card')).toContainText('Default search')
  await expect(panel.getByTestId('fallback-search-card')).toContainText('Fallback')
  expect(integrationWrites, 'opening, closing and reopening the panel saved nothing').toEqual([])
})

test('Choose a category dialog surface: choosing Lifestyle saves the category once and a later change is refused with the server sentence', async ({ page, native }) => {
  const REFUSAL = 'The category is set at install and cannot be changed.' // spec error table, S-104
  await inventory(native, [['trip-notes', 'uncategorised']]) // S-104's Given: stored as Uncategorised
  // US-7 AS8 ends "plus Approve this version for an unverified skill under trust Ask". A folder put in by hand has no
  // registry check, so it is unverified (spec C-5, Skill.verified), and trust is set to Ask here, not left to the default.
  const ask: PermissionSettingsPatch = { skill_trust: 'ask' }
  expect((await nativeWrite(native, 'PATCH', '/api/v1/security/permissions', ask, wire.PermissionSettings)).skill_trust).toBe('ask')
  const given = (await nativeGet(native, '/api/v1/skills', wire.Skill.array())).find((skill) => skill.id === 'trip-notes')
  expect(given?.verified, 'a folder put in by hand is unverified').toBe(false)
  await openPage(page, native)
  await disclose(page, header('UNCATEGORISED'))
  const writes: Array<{ path: string; body: unknown }> = []
  page.on('request', (request) => {
    const { pathname } = new URL(request.url())
    if (request.method() === 'PUT' && /^\/api\/v1\/skills\/[^/]+\/category$/.test(pathname)) writes.push({ path: pathname, body: request.postDataJSON() })
  })
  const menuItems = async () => {
    const menu = page.getByRole('menu')
    await expect(menu).toBeVisible() // allTextContents does not wait for the menu to open
    return (await menu.getByRole('menuitem').allTextContents()).map((text) => text.trim()).sort()
  }
  const openDialog = async () => {
    await skillRow(page, 'trip-notes').getByRole('button', { name: /More actions/i }).click()
    await page.getByRole('menuitem', { name: 'Choose category', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    return dialog
  }

  // US-7 AS8, FR-044: Make a copy, Remove (not built-in), Choose category (while Uncategorised) and Approve this version
  // (unverified, trust Ask).
  await skillRow(page, 'trip-notes').getByRole('button', { name: /More actions/i }).click()
  expect(await menuItems()).toEqual(['Approve this version', 'Choose category', 'Make a copy', 'Remove'])
  await page.keyboard.press('Escape')

  // Cancel changes nothing.
  const first = await openDialog()
  await first.getByRole('combobox', { name: 'Category', exact: true }).click()
  await page.getByRole('option', { name: 'Lifestyle', exact: true }).click()
  await first.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(first).toBeHidden()
  expect(writes, 'Cancel made no write').toEqual([])

  // S-104: choosing Lifestyle saves Lifestyle, once, for that skill.
  const dialog = await openDialog()
  await dialog.getByRole('combobox', { name: 'Category', exact: true }).click()
  await page.getByRole('option', { name: 'Lifestyle', exact: true }).click()
  const saved = responseFor(page, 'PUT', '/api/v1/skills/trip-notes/category')
  await dialog.getByRole('button', { name: /^Save/ }).click()
  const reply = await saved
  expect(reply.status()).toBe(200)
  expect(wire.Skill.parse(await reply.json())).toMatchObject({ id: 'trip-notes', category: 'lifestyle' })
  await expect(dialog).toBeHidden()
  expect(writes).toEqual([{ path: '/api/v1/skills/trip-notes/category', body: { category: 'lifestyle' } }])

  // The skill now sits under Lifestyle, and no empty Uncategorised group is drawn (US-7 AS7).
  await disclose(page, header('LIFESTYLE'))
  await expect(skillRow(page, 'trip-notes')).toBeVisible()
  await expect(page.getByRole('button', { name: header('UNCATEGORISED') })).toHaveCount(0)
  // FR-044: once it has a category, More actions no longer offers Choose category; the other three stay (a category
  // does not make a hand-made skill verified).
  await skillRow(page, 'trip-notes').getByRole('button', { name: /More actions/i }).click()
  expect(await menuItems()).toEqual(['Approve this version', 'Make a copy', 'Remove'])
  await page.keyboard.press('Escape')

  // S-104, second half: a later PUT with research is refused with 409 and the sentence; the stored category stays.
  const later = await native.gateway.apiFetch('PUT', '/api/v1/skills/trip-notes/category', { category: 'research' })
  expect(later.status).toBe(409)
  expect(later.raw).toContain(REFUSAL)
  const stored = (await nativeGet(native, '/api/v1/skills', wire.Skill.array())).find((skill) => skill.id === 'trip-notes')
  expect(stored?.category).toBe('lifestyle')
})
