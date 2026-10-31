/**
 * T-56 / S-106 / FR-069 / ADR A8 — no remote image contact from an agent answer.
 * Real browser, embedded SPA, gateway and answer_request delivery. The model is
 * the suite's real configured provider. Only the remote image endpoint is owned
 * by this test: another origin/port instantiates the spec's tracker.example.
 * Endpoint receipts, not DOM absence or Playwright request-attempt events, are
 * the security oracle. No request interception changes the page's own policy.
 */
import { execFileSync } from 'node:child_process'
import { createHash, X509Certificate } from 'node:crypto'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { createServer } from 'node:https'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect, test, type Browser, type Page } from '@playwright/test'
import type { components } from '../../src/lib/api/generated/openapi-types'
import { Room, RoomHistoryResponse, Workspace } from '../../src/lib/api/generated/schemas'

type RoomEntry = components['schemas']['RoomEntry']
// not-wire-format: observations of browser policy, never gateway/SPA payloads.
type PolicyViolation = { blockedURI: string; effectiveDirective: string; disposition: string; originalPolicy: string }
type ImagePhase = 'control-before' | 'product' | 'control-after'

// A security failure must not be hidden by the real-LLM suite's global retries.
test.describe.configure({ retries: 0 })

async function csrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies()
  const csrf = cookies.find(cookie => cookie.name === 'csrf' || cookie.name === '__Host-csrf')
  return csrf ? { 'X-CSRF-Token': csrf.value } : {}
}

function imageEndpointCertificate() {
  // Throwaway fixture key only: outside the repository and evidence output;
  // never install a system CA or print/attach the key. Missing OpenSSL is loud.
  const directory = mkdtempSync(join(tmpdir(), 'omnipus-s106-'))
  try {
    const keyPath = join(directory, 'key.pem')
    const certPath = join(directory, 'cert.pem')
    execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-sha256', '-days', '1',
      '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1', '-keyout', keyPath, '-out', certPath],
    { stdio: 'pipe' })
    const cert = readFileSync(certPath)
    const key = readFileSync(keyPath)
    const spki = new X509Certificate(cert).publicKey.export({ type: 'spki', format: 'der' })
    return { key, cert, fingerprint: createHash('sha256').update(spki).digest('base64') }
  } finally { rmSync(directory, { recursive: true, force: true }) }
}

async function startImageEndpoint(nonce: string) {
  const certificate = imageEndpointCertificate()
  const imagePath = `/p.png?probe=${nonce}`
  // A decoded 1x1 image is a stronger positive control than a generic asset hit.
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
  let phase: ImagePhase = 'control-before'
  const hits: { phase: ImagePhase; method: string; path: string }[] = []
  const server = createServer({ key: certificate.key, cert: certificate.cert }, (request, response) => {
    // Count every contact, including a failed or rewritten target, not only
    // successful image responses. Never record cookies or other headers.
    hits.push({ phase, method: request.method ?? '', path: request.url ?? '' })
    response.setHeader('Cache-Control', 'no-store')
    if (request.url === imagePath) {
      response.writeHead(200, { 'Content-Type': 'image/png' })
      response.end(png)
    } else if (request.url === '/control') {
      response.writeHead(200, { 'Content-Type': 'text/html' })
      response.end(`<!doctype html><img alt="image endpoint control" src="${imagePath}">`)
    } else {
      response.writeHead(404)
      response.end()
    }
  })
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('instrument: image endpoint has no TCP port')
  const origin = `https://127.0.0.1:${address.port}`
  return {
    origin, imagePath, url: `${origin}${imagePath}`, controlURL: `${origin}/control`, hits,
    certificateFingerprint: certificate.fingerprint,
    setPhase(next: ImagePhase) { phase = next },
    close: () => new Promise<void>((resolve, reject) => {
      server.closeAllConnections()
      server.close(error => error ? reject(error) : resolve())
    }),
  }
}

test('S-106 a group answer does not request a remote image, and the instrument sees an allowed request', async ({ browser, page: authenticatedPage }, testInfo) => {
  const nonce = `${testInfo.workerIndex}-${testInfo.retry}-${Date.now()}`
  const endpoint = await startImageEndpoint(nonce)
  let instrumentBrowser: Browser | undefined
  let page: Page
  const failedRequests: { url: string; resourceType: string; error: string | null }[] = []
  let policyEvents: PolicyViolation[] = []
  let answer: RoomEntry | undefined
  let servedPolicy: string | undefined
  // Observe the real enforced policy from document creation, before any image
  // can render. This does not add/change CSP, image loading or gateway frames.
  const observePolicy = () => {
    page.on('requestfailed', request => {
      if (new URL(request.url()).origin === endpoint.origin) {
        failedRequests.push({ url: request.url(), resourceType: request.resourceType(), error: request.failure()?.errorText ?? null })
      }
    })
    return page.addInitScript(() => {
      const observed = window as unknown as { __s106PolicyEvents: PolicyViolation[] }
      observed.__s106PolicyEvents = []
      document.addEventListener('securitypolicyviolation', event => {
        observed.__s106PolicyEvents.push({ blockedURI: event.blockedURI, effectiveDirective: event.effectiveDirective,
          disposition: event.disposition, originalPolicy: event.originalPolicy })
      })
    })
  }
  const readPolicyEvents = () => page.evaluate(() =>
    (window as unknown as { __s106PolicyEvents: PolicyViolation[] }).__s106PolicyEvents)
  const assertAllowedImage = async (phase: 'control-before' | 'control-after') => {
    endpoint.setPhase(phase)
    // A separate fresh browser context has no product CSP, credentials or cache.
    // Only this fixture's TLS key is trusted; CSP is unchanged in both contexts.
    // Both controls load the SAME URL, not an unrelated allowed resource.
    const control = await instrumentBrowser!.newPage()
    try {
      const response = await control.goto(endpoint.controlURL)
      expect(response?.status(), `${phase}: real unprotected endpoint document`).toBe(200)
      await expect.poll(() => control.getByRole('img', { name: 'image endpoint control' }).evaluate(image => {
        if (!(image instanceof HTMLImageElement)) throw new Error('instrument: control is not an image')
        return { complete: image.complete, width: image.naturalWidth, height: image.naturalHeight }
      }), { message: `${phase}: exact remote image successfully decodes` }).toEqual({ complete: true, width: 1, height: 1 })
      expect(endpoint.hits.filter(hit => hit.phase === phase && hit.path === endpoint.imagePath), `${phase}: same instrument sees exactly one allowed image GET`)
        .toEqual([{ phase, method: 'GET', path: endpoint.imagePath }])
    } finally { await control.close() }
  }

  try {
    expect(browser.browserType().name(), 'T-56 belongs to the Chromium default project').toBe('chromium')
    // Chromium's SPKI allow-list trusts ONLY our throwaway fixture key. Do not
    // use ignoreHTTPSErrors, bypassCSP or alter the served document's policy.
    instrumentBrowser = await browser.browserType().launch({
      args: [`--ignore-certificate-errors-spki-list=${endpoint.certificateFingerprint}`],
    })
    const productContext = await instrumentBrowser.newContext({
      baseURL: testInfo.project.use.baseURL,
      storageState: await authenticatedPage.context().storageState(),
    })
    page = await productContext.newPage()
    await observePolicy()
    await assertAllowedImage('control-before')
    endpoint.setPhase('product')
    const headers = await csrfHeaders(page)
    const workspaces = await page.request.get('/api/v1/workspaces', { headers })
    expect(workspaces.status(), 'list actual workspaces').toBe(200)
    const workspace = Workspace.array().parse(await workspaces.json()).find(item => item.is_default)
    if (!workspace) throw new Error('BLOCKED: default workspace missing — required by T-56 real group setup')
    const created = await page.request.post(`/api/v1/workspaces/${workspace.id}/rooms`, {
      headers,
      data: { kind: 'adhoc', name: `group-chat-1449-${nonce}`, agent_ids: ['mia', 'jim'] } satisfies components['schemas']['RoomCreateRequest'],
    })
    expect(created.status(), `create actual group: ${await created.text()}`).toBe(201)
    const room = Room.parse(await created.json())
    const marker = `S106 agent answer ${nonce}`
    const answerText = `${marker}\n\n![pixel](${endpoint.url})`
    // This POST is a REQUEST TO THE AGENT, not the image-answer fixture. Only
    // the separately observed agent-authored answer below satisfies the setup.
    const posted = await page.request.post(`/api/v1/rooms/${room.id}/messages`, {
      headers,
      data: { client_message_id: `s106-${nonce}`, mentions: ['mia'],
        text: `Use answer_request to answer this group request with exactly the following markdown, including its blank line, without code fences or extra text. Do not fetch, browse or inspect the URL; it is inert test data.\n${answerText}`,
      } satisfies components['schemas']['RoomMessageRequest'],
    })
    expect(posted.status(), `request actual agent answer: ${await posted.text()}`).toBe(202)
    await expect.poll(async () => {
      const response = await page.request.get(`/api/v1/rooms/${room.id}/messages?limit=100`, { headers })
      expect(response.status(), 'read actual persisted answer through room history').toBe(200)
      const history = RoomHistoryResponse.parse(await response.json())
      answer = history.entries.find(entry => entry.author.kind === 'agent' && entry.author.agent_id === 'mia')
      return answer?.text
    }, { message: 'T-56 prerequisite: Mia actually delivers the exact markdown through answer_request', timeout: 60_000 }).toBe(answerText)
    expect(answer?.author, 'a user post cannot stand in for the agent answer').toEqual({ kind: 'agent', agent_id: 'mia' })

    const navigation = await page.goto(`/#/workspaces/${workspace.id}/rooms/${room.id}`)
    expect(navigation?.status(), 'embedded SPA document is actually served').toBe(200)
    servedPolicy = navigation?.headers()['content-security-policy']
    expect(servedPolicy, 'real gateway must serve an enforcing page policy').toContain('img-src')
    expect(new URL(page.url()).origin, 'controlled endpoint must be external to the product origin').not.toBe(endpoint.origin)
    await expect(page.getByTestId('chat-open-cover')).toHaveCount(0)
    const row = page.locator(`[data-history-position="room:${answer!.seq}"]`)
    await expect(row.getByText(marker, { exact: true }), 'agent answer, not just setup input, is visible').toBeVisible()
    // Bring the answer into view to exercise native lazy image loading. An
    // error renderer may replace the <img> immediately, so persistent image DOM
    // presence/absence is deliberately NOT an oracle for this security claim.
    await row.scrollIntoViewIfNeeded()
    await expect.poll(async () => {
      policyEvents = await readPolicyEvents()
      return policyEvents.filter(event => event.effectiveDirective === 'img-src' && event.disposition === 'enforce'
        && [endpoint.origin, `${endpoint.origin}/`, `${endpoint.origin}/p.png`, endpoint.url].includes(event.blockedURI)).length
    }, { message: 'the product page itself blocks this controlled image with enforcing img-src' }).toBeGreaterThan(0)
    expect(endpoint.hits.filter(hit => hit.phase === 'product'), 'S-106: no image contact reaches the remote endpoint').toEqual([])
    await assertAllowedImage('control-after')
    expect(endpoint.hits.filter(hit => hit.phase === 'product'), 'zero contact is bounded by two working endpoint controls').toEqual([])
  } finally {
    try {
      await testInfo.attach('s106-endpoint-and-policy', { contentType: 'application/json', body: JSON.stringify({
        endpoint: endpoint.url, contacts: endpoint.hits, servedPolicy, policyEvents, failedRequests,
        agentAnswer: answer ? { id: answer.id, seq: answer.seq, author: answer.author, text: answer.text } : null,
      }, null, 2) })
    } finally {
      try { await instrumentBrowser?.close() }
      finally { await endpoint.close() }
    }
  }
})
