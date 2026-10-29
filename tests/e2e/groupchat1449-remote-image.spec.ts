/**
 * T-56 / S-106 — a remote image in a group answer makes no request.
 * Oracle: docs/internal/specs/1449-group-chat-spec.md S-106 and T-56.
 * Real browser, real gateway. The positive control is a same-origin asset
 * request the test records before it judges the blocked image.
 */
import { expect, test, type Page } from '@playwright/test'
import type { components } from '../../src/lib/api/generated/openapi-types'

type Workspace = components['schemas']['Workspace']
type Room = components['schemas']['Room']

const REMOTE = 'https://tracker.example/p.png'
const MARKDOWN = `![pixel](${REMOTE})`

async function csrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies()
  const csrf = cookies.find((cookie) => cookie.name === 'csrf' || cookie.name === '__Host-csrf')
  return csrf ? { 'X-CSRF-Token': csrf.value } : {}
}

test('S-106 a group answer does not request a remote image, and the instrument sees an allowed request', async ({ page }, testInfo) => {
  const remoteHits: string[] = []
  const allowedHits: string[] = []
  page.on('request', (request) => {
    const url = request.url()
    if (url.includes('tracker.example')) remoteHits.push(url)
    else allowedHits.push(url)
  })
  await page.route('**/tracker.example/**', (route) => route.abort())

  const headers = await csrfHeaders(page)
  const workspaces = await page.request.get('/api/v1/workspaces', { headers })
  expect(workspaces.ok(), `list workspaces failed: ${workspaces.status()}`).toBe(true)
  const workspace = ((await workspaces.json()) as Workspace[]).find((item) => item.is_default)
  expect(workspace?.id, 'a default workspace must exist').toBeTruthy()

  const created = await page.request.post(`/api/v1/workspaces/${workspace!.id}/rooms`, {
    headers: { ...headers, 'content-type': 'application/json' },
    data: { kind: 'adhoc', name: `group-chat-1449-${testInfo.workerIndex}`, agent_ids: ['mia', 'jim'] },
  })
  expect(created.status(), `create group failed: ${created.status()} ${await created.text()}`).toBe(201)
  const room = (await created.json()) as Room

  const posted = await page.request.post(`/api/v1/rooms/${room.id}/messages`, {
    headers: { ...headers, 'content-type': 'application/json' },
    data: {
      client_message_id: `s106-${Date.now()}`,
      text: MARKDOWN,
      mentions: [],
    },
  })
  expect(posted.status(), `post image markdown failed: ${posted.status()} ${await posted.text()}`).toBe(202)

  await page.goto(`/workspaces/${workspace!.id}/rooms/${room.id}`)
  await page.waitForLoadState('networkidle')
  // Positive control: the page asked for an allowed same-origin asset, so a
  // missed remote request would have been visible too.
  expect(allowedHits.length, 'T-56 positive control: the browser issued an allowed request').toBeGreaterThan(0)
  expect(remoteHits, 'S-106: no request to the remote image').toEqual([])
  await expect(page.locator(`img[src="${REMOTE}"]`)).toHaveCount(0)
  // The shared pipeline still represents the image (blocked), it does not
  // leave the markdown source as the only visible text.
  await expect(page.getByRole('img', { name: 'pixel' })).toBeVisible()
})
