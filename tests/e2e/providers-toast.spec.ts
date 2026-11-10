/**
 * #1639, ruling B rule 6 / CHECK F1: a refusal must be painted ABOVE the
 * still-open provider editor, not merely present/"visible" in the DOM.
 * Real embedded SPA, AppShell, Sheet, error parser and toast. Only HTTP/WS
 * process edges are scripted; no live model, real account or founder data.
 */
import { once } from 'node:events'
import { readFileSync } from 'node:fs'
import { expect, test } from '@playwright/test'
import type { AboutResponse, AppState, ErrorResponse, GodModeStatus, NotificationList, Provider, ProvidersCatalog, VersionResponse } from '../../src/lib/api/generated/openapi-types'
import { getFreePort, startGateway, stopGateway, type GatewayHandle } from './setup'

// Same shared JSON as the Vitest fixture. Follow onboarding-stubs.ts's Node
// filesystem loading pattern: importing its Vite-only JSON module fails under
// Playwright's Node ESM loader (missing JSON import attribute).
const PROVIDERS_CATALOG = JSON.parse(readFileSync(new URL('../../src/test/fixtures/providers-catalog.json', import.meta.url), 'utf8')) as ProvidersCatalog

const CUSTOM_ID = 'localtest'
const MODEL_SLUG = 'test/model-a'
const SERVER_REFUSAL = 'unknown provider "localtest"'
const REFUSAL_BODY = { error: SERVER_REFUSAL, field: 'id' } satisfies ErrorResponse
const PROVIDER: Provider = {
  id: CUSTOM_ID, name: CUSTOM_ID, display_name: CUSTOM_ID,
  status: 'connected', auth_method: 'api_key', custom: true,
  protocol: 'openai-compatible', locality: 'cloud', has_api_key: true,
  has_models_endpoint: false, models: [], dependents: [], backs_default: false,
}

// Also runs in the default Chromium CI project; the local narrow config only
// avoids global-setup's unrelated live-key onboarding. Own empty cookie jar.
test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 900 } })
test.describe.configure({ retries: 0 })

test('a refused provider save paints its toast above the open Sheet and preserves the draft on Dismiss', async ({ page }, info) => {
  let gateway: GatewayHandle | undefined
  const unexpectedEdges: string[] = []
  const pageErrors: string[] = []
  const sent: unknown[] = []
  page.on('pageerror', (error) => pageErrors.push(error.message))
  try {
    // Existing fixture: fresh home and ephemeral loopback port. Skip onboarding
    // because this is a UI/painting test with explicit network fixtures, not an
    // authentication or real-provider test. The Go process serves real assets.
    gateway = await startGateway({
      port: await getFreePort(), skipOnboarding: true,
      fixtureConfig: { gateway: { host: '127.0.0.1' } },
    })
    const origin = gateway.baseURL
    await page.context().addCookies([{ name: 'csrf', value: 'provider-toast-fixture', url: origin }])
    await page.routeWebSocket('**/api/v1/chat/ws', (socket) => {
      // Idle transport edge: never forward to a server or invent chat frames.
      socket.onMessage((message) => {
        if (typeof message !== 'string' || (JSON.parse(message) as { type?: string }).type !== 'ping') {
          unexpectedEdges.push(`unexpected WebSocket send: ${String(message)}`)
        }
      })
    })
    await page.route('**/*', async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      const path = url.pathname
      const method = request.method()
      if (url.origin !== origin) {
        unexpectedEdges.push(`${method} external origin ${url.origin}`)
        await route.abort('blockedbyclient')
        return
      }
      if (!path.startsWith('/api/')) {
        await route.continue() // Actual Go-embedded index, scripts, styles, fonts.
        return
      }
      let body: unknown
      let status = 200
      if (method === 'PUT' && path === `/api/v1/providers/${CUSTOM_ID}`) {
        sent.push(request.postDataJSON())
        body = REFUSAL_BODY
        status = 400
      } else if (method === 'GET') {
        switch (path) {
          case '/api/v1/state':
            body = { onboarding_complete: true, identity: { mode: 'platform', edition: 'hosted', signed_in: true } } satisfies AppState
            break
          case '/api/v1/providers': body = [PROVIDER]; break
          case '/api/v1/providers/catalog': body = PROVIDERS_CATALOG; break
          case '/api/v1/providers/default-model':
            body = { error: 'default model not set' } satisfies ErrorResponse
            status = 404
            break
          case '/api/v1/agents':
          case '/api/v1/tasks':
          case '/api/v1/workspaces':
          case '/api/v1/skills': body = []; break
          case '/api/v1/sessions': body = { sessions: [] }; break
          case '/api/v1/activity': body = { events: [] }; break
          case '/api/v1/performance': body = {}; break
          case '/api/v1/notifications': body = { notifications: [], unread_count: 0 } satisfies NotificationList; break
          case '/api/v1/version': body = { version: '0.1.1', build_sha: 'dev' } satisfies VersionResponse; break
          case '/api/v1/about':
            body = {
              version: '0.1.1', go_version: 'fixture', os: 'linux', arch: 'amd64',
              uptime: '0s', uptime_seconds: 0, pid: 1, preview_enabled: true,
              warmup_timeout_seconds: 60, frame_ancestors_fallback: false, device_pairing_enabled: false,
            } satisfies AboutResponse
            break
          case '/api/v1/gateway/god-mode':
            body = { enabled: false, persisted: false, supported: true, available: true } satisfies GodModeStatus
            break
          default: unexpectedEdges.push(`${method} ${path}`)
        }
      } else {
        unexpectedEdges.push(`${method} ${path}`)
      }
      if (body === undefined) {
        body = { error: `Unspecified fixture request: ${method} ${path}` } satisfies ErrorResponse
        status = 500
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    })

    await page.goto(`${origin}/#/settings`)
    await page.getByTestId(`configure-btn-${CUSTOM_ID}`).click()
    const sheet = page.getByTestId('provider-config-sheet')
    await expect(sheet).toHaveAttribute('data-state', 'open')
    await page.getByTestId(`add-model-input-${CUSTOM_ID}`).fill(MODEL_SLUG)
    await page.getByTestId(`add-model-${CUSTOM_ID}`).click()
    const draft = sheet.getByTestId(`model-list-${CUSTOM_ID}`)
    await expect(draft.getByText(MODEL_SLUG, { exact: true })).toBeVisible()
    await page.getByTestId(`save-provider-${CUSTOM_ID}`).click()
    const refused = page.waitForResponse((response) => response.url() === `${origin}/api/v1/providers/${CUSTOM_ID}` && response.request().method() === 'PUT')
    await page.getByRole('button', { name: 'Save API key', exact: true }).click()
    const response = await refused
    expect(response.status(), 'the real editor must receive the injected HTTP 400').toBe(400)
    expect(await response.json()).toEqual(REFUSAL_BODY)
    expect(sent, 'ruling B: the editor sends only the unsaved model list').toEqual([{ models: [MODEL_SLUG] }])
    await expect(page.getByRole('alertdialog')).toHaveCount(0)

    // CSS role selector deliberately includes aria-hidden descendants. Current
    // Radix hideOthers hides #root from assistive tech (developer finding F1).
    // This test proves PAINTING/hit-testing, not accessibility-tree exposure.
    const toast = page.locator('[role="alert"]').filter({ hasText: SERVER_REFUSAL })
    await expect(toast).toHaveCount(1)
    await expect(toast.getByText(SERVER_REFUSAL, { exact: true })).toBeVisible()
    await expect(sheet).toHaveAttribute('data-state', 'open')
    await expect(draft.getByText(MODEL_SLUG, { exact: true })).toBeVisible()

    // Instrument control: elementFromPoint must resolve into the open Sheet
    // at its own centre. No DOM style/z-index oracle can replace hit-testing.
    await expect.poll(() => sheet.evaluate((node) => {
      const rect = node.getBoundingClientRect()
      const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)
      return hit !== null && node.contains(hit)
    }), { message: 'control: the open Sheet must really paint and receive hits' }).toBe(true)

    const measureToast = () => toast.evaluate((node) => {
      const rect = node.getBoundingClientRect()
      const x = rect.x + rect.width / 2
      const y = rect.y + rect.height / 2
      const sheet = document.querySelector('[data-testid="provider-config-sheet"]')
      const sheetRect = sheet?.getBoundingClientRect()
      const hit = document.elementFromPoint(x, y)
      return {
        hasArea: rect.width > 0 && rect.height > 0,
        insideViewport: x >= 0 && x < innerWidth && y >= 0 && y < innerHeight,
        overlapsSheet: !!sheetRect && x > sheetRect.left && x < sheetRect.right && y > sheetRect.top && y < sheetRect.bottom,
        sheetOpen: sheet?.getAttribute('data-state') === 'open',
        toastOwnsCentre: hit !== null && node.contains(hit),
      }
    })
    // Oracle: error is on TOP while the editor stays open. With mutant f the
    // Sheet owns this point even though the toast has a visible bounding box.
    await expect.poll(measureToast, { message: '#1639: Sheet must not occlude the refusal toast at its centre' }).toEqual({
      hasArea: true, insideViewport: true, overlapsSheet: true, sheetOpen: true, toastOwnsCentre: true,
    })
    await info.attach('toast-hit-test', { contentType: 'application/json', body: JSON.stringify(await measureToast()) })
    await info.attach('toast-over-open-sheet', { contentType: 'image/png', body: await page.screenshot() })

    await toast.locator('[aria-label="Dismiss"]').click()
    await expect(toast).toHaveCount(0)
    await expect(sheet).toHaveAttribute('data-state', 'open')
    await expect(draft.getByText(MODEL_SLUG, { exact: true })).toBeVisible()
    expect(unexpectedEdges, 'no live model, external origin or unspecified API request').toEqual([])
    expect(pageErrors, 'the real SPA must not crash').toEqual([])
  } finally {
    await page.close()
    if (gateway) {
      if (gateway.process.exitCode === null && gateway.process.signalCode === null) {
        const exited = once(gateway.process, 'exit')
        gateway.process.kill('SIGTERM')
        await exited
      }
      await stopGateway(gateway)
    }
  }
})
