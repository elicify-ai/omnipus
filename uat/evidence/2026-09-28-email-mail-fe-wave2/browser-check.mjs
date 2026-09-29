import { chromium } from 'playwright'
import fs from 'node:fs'

const BASE = 'http://localhost:8891'
const EVIDENCE = 'uat/evidence/2026-09-28-email-mail-fe-wave2'
fs.mkdirSync(EVIDENCE, { recursive: true })

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()
const consoleErrors = []
page.on('pageerror', (e) => consoleErrors.push(`pageerror: ${e.message}`))
page.on('console', (m) => { if (m.type() === 'error') consoleErrors.push(m.text()) })
const shot = (name) => page.screenshot({ path: `${EVIDENCE}/${name}.png` })

try {
  await page.goto(`${BASE}/#/login`)
  await page.getByPlaceholder('admin').fill('admin')
  await page.locator('input[type="password"]').fill('admin123')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.waitForSelector('[data-testid="workspace-tab-strip"]', { timeout: 30000 })
  const ws = page.url().match(/workspaces\/([^/?#]+)/)?.[1]
  if (!ws) throw new Error(`no workspace id in URL: ${page.url()}`)
  console.log(`STEP1 OK signed in workspace=${ws}`)

  await page.click('[data-testid="workspace-tab-mail"]')
  await page.waitForSelector('[data-testid="side-panel"]', { timeout: 15000 })
  await page.waitForSelector('[data-testid="mail-panel"]', { timeout: 20000 })
  const headerText = await page.locator('[data-testid="side-panel-header"]').textContent()
  console.log(`STEP-A1 OK mail panel in shell, header=${JSON.stringify((headerText || '').slice(0, 60))}`)
  await shot('01-mail-tab-opens-mail-in-shell')

  const sep = page.locator('[data-testid="panel-resize-separator"]')
  await sep.waitFor({ state: 'visible', timeout: 10000 })
  const before = await page.locator('[data-testid="side-panel"]').boundingBox()
  const sb = await sep.boundingBox()
  if (!sb || !before) throw new Error('separator or panel not measurable')
  const cx = sb.x + sb.width / 2
  const cy = sb.y + sb.height / 2
  await page.mouse.move(cx, cy)
  await page.mouse.down()
  await page.mouse.move(cx - 100, cy, { steps: 6 })
  await page.mouse.move(cx - 220, cy, { steps: 6 })
  await page.mouse.up()
  await page.waitForTimeout(500)
  const after = await page.locator('[data-testid="side-panel"]').boundingBox()
  console.log(`STEP-A2 OK resize width ${Math.round(before.width)} -> ${Math.round(after.width)}`)
  if (Math.abs(after.width - before.width) < 40) throw new Error('resize did not move the panel width')
  await shot('02-mail-panel-resized')

  const popupPromise = ctx.waitForEvent('page', { timeout: 15000 })
  await page.click('[data-testid="panel-expand"]')
  const popup = await popupPromise
  await popup.waitForLoadState('load')
  await popup.waitForURL(/\/mail\b/, { timeout: 15000 })
  await popup.waitForSelector('[data-testid="mail-panel"]', { timeout: 20000 })
  console.log(`STEP-A3 OK expand opened new tab on ${popup.url()}`)
  await popup.screenshot({ path: `${EVIDENCE}/03-mail-expanded-fullpage.png` })
  await popup.close()
  await page.waitForTimeout(300)

  await page.goto(`${BASE}/#/workspaces/${ws}/chat?panel=mail`)
  await page.reload()
  await page.waitForSelector('[data-testid="mail-panel"]', { timeout: 20000 })
  await page.getByText('Choose a mailbox').first().waitFor({ timeout: 15000 })
  console.log('STEP-B OK hard-load ?panel=mail shows choose-a-mailbox')
  await shot('04-hardload-panel-mail-choose-mailbox')

  const libTab = page.locator('[data-testid="workspace-tab-media"]')
  if (await libTab.isVisible()) {
    await libTab.click()
  } else {
    // compact tab strip (narrow chat column): open the segment dropdown
    await page.getByRole('button', { name: /Chat/ }).first().click()
    await page.getByRole('menuitem', { name: /Library/ }).or(page.getByRole('option', { name: /Library/ })).first().click()
  }
  await page.waitForSelector('[data-testid="mail-panel"]', { state: 'detached', timeout: 15000 })
  await page.waitForSelector('[data-testid="side-panel"]', { timeout: 15000 })
  console.log('STEP-C OK Library replaced Mail (one panel at a time)')
  await shot('05-library-replaced-mail')

  console.log(`CONSOLE_ERRORS=${consoleErrors.length}`)
  consoleErrors.slice(0, 10).forEach((e) => console.log(`  ERR: ${e.slice(0, 160)}`))
} catch (err) {
  console.log(`FAILED url=${page.url()}: ${err.message}`)
  await shot('99-failure-state')
  process.exitCode = 1
} finally {
  await browser.close()
}
