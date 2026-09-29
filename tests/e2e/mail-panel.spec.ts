/**
 * mail-panel.spec.ts — D36 / T71. Every main mail flow in a real browser
 * against the built-in fake IMAP/SMTP server. Seeded with APPEND/STORE, never
 * a live model turn.
 *
 * Oracles: spec §7 row 71, US-3 (read), US-6 (open marks read, read-by-agent
 * tag), US-7 (edit and send a draft), US-5/US-8 (compose with an attachment),
 * US-1 (signature on the transmitted message), US-2 (Sent copy).
 *
 * The spec file is on the ui-4 shard. It uses its own gateway
 * (GatewayProcess) and a blank storage state so it does not touch the shared
 * session. It does not call the login route itself.
 */
import { test, expect, type Browser, type Page } from '@playwright/test'
import { GatewayProcess } from './fixtures/gateway-process'
import { startFakeMail, type FakeMail } from './fixtures/fake-mail-server'

test.use({ storageState: { cookies: [], origins: [] } })

const INBOX_RAW = [
  'From: Ada <ada@example.test>',
  'To: mailbox@test.local',
  'Subject: Quarterly',
  'Date: Mon, 02 Jan 2006 15:04:05 +0000',
  'Message-ID: <quarterly@example.test>',
  'MIME-Version: 1.0',
  'Content-Type: text/plain; charset=utf-8',
  '',
  'The numbers are in.',
  '',
].join('\r\n')

const DRAFT_RAW = [
  'From: mailbox@test.local',
  'To: ada@example.test',
  'Subject: Draft note',
  'Date: Mon, 02 Jan 2006 15:04:05 +0000',
  'Message-ID: <draft-note@example.test>',
  'X-Omnipus-Draft: 1',
  'MIME-Version: 1.0',
  'Content-Type: text/plain; charset=utf-8',
  '',
  'Please review.',
  '',
].join('\r\n')

test.describe('Mail panel on the built-in fake server (D36)', () => {
  let mail: FakeMail
  let gw: GatewayProcess
  let workspaceId: string

  test.beforeAll(async () => {
    mail = await startFakeMail()
    gw = await GatewayProcess.start()
    const created = await gw.apiFetch<{ id: string }>('POST', '/api/v1/workspaces', {
      name: `Mail ${Date.now()}`,
      core_team: ['mia'],
    })
    if (!created.ok) throw new Error(`create workspace ${created.status}: ${created.raw}`)
    workspaceId = created.body.id
    const saved = await gw.apiFetch('PUT', `/api/v1/agents/mia/mailboxes/${workspaceId}`, {
      enabled: true,
      imap_host: mail.imapHost,
      imap_port: mail.imapPort,
      smtp_host: mail.smtpHost,
      smtp_port: mail.smtpPort,
      username: mail.user,
      password: mail.password,
      signature_html: '<p>Kind regards</p>',
    })
    if (!saved.ok) throw new Error(`configure mailbox ${saved.status}: ${saved.raw}`)
    await mail.append('INBOX', INBOX_RAW, [])
    await mail.append('Drafts', DRAFT_RAW, ['\\Draft'])
    const flagged = await mail.append('INBOX', INBOX_RAW.replace('Quarterly', 'Agent read'), ['\\Seen'])
    await mail.storeFlags('INBOX', flagged.uid, ['$OmnipusAgentRead'])
  })

  test.afterAll(async () => {
    await gw?.stop()
    await mail?.stop()
  })

  async function mailPage(browser: Browser): Promise<Page> {
    const context = await browser.newContext({
      storageState: await gw.browserStorageState(),
      baseURL: gw.baseURL,
    })
    return context.newPage()
  }

  async function openMail(page: Page) {
    await page.goto(`/#/workspaces/${workspaceId}/chat`)
    const mailToggle = page.getByTestId('workspace-tab-mail')
    await expect(mailToggle).toHaveAttribute('aria-pressed', 'false')
    await mailToggle.click()
    await expect(mailToggle).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('mail-panel')).toBeVisible()
  }

  test('reads the seeded inbox message and marks it seen (US-3, US-6)', async ({ browser }) => {
    const page = await mailPage(browser)
    await openMail(page)
    await expect(page.getByText('Quarterly')).toBeVisible()
    const inbox = page.getByRole('tab', { name: /inbox/i })
    await expect(inbox).toContainText('1')
    const [detailResponse] = await Promise.all([
      page.waitForResponse((response) => {
        const url = new URL(response.url())
        return response.request().method() === 'GET'
          && /\/folders\/inbox\/messages\/[^/]+$/.test(url.pathname)
      }),
      page.getByText('Quarterly').click(),
    ])
    const messageRef = decodeURIComponent(new URL(detailResponse.url()).pathname.split('/').at(-1) ?? '')
    expect(messageRef).toMatch(/^uid:\d+:\d+$/)
    expect(detailResponse.status()).toBe(200)
    await expect(page.getByText('The numbers are in.')).toBeVisible()
    await expect(inbox).toContainText('0')
  })

  test('shows the read-by-agent tag once (US-6, D38)', async ({ browser }) => {
    const page = await mailPage(browser)
    await openMail(page)
    await expect(page.getByText('Agent read')).toBeVisible()
    await expect(page.getByText(/read by agent/i)).toHaveCount(1)
  })

  test('edits an agent draft and sends it (US-7)', async ({ browser }) => {
    const page = await mailPage(browser)
    await openMail(page)
    const drafts = page.getByRole('tab', { name: 'Drafts' })
    await drafts.click()
    await expect(drafts).toHaveAttribute('aria-selected', 'true')
    const [detailResponse] = await Promise.all([
      page.waitForResponse((response) => {
        const url = new URL(response.url())
        return response.request().method() === 'GET'
          && /\/folders\/drafts\/messages\/[^/]+$/.test(url.pathname)
      }),
      page.getByRole('button', { name: /^Draft note\b/ }).click(),
    ])
    expect(detailResponse.status()).toBe(200)
    await page.getByRole('button', { name: 'Edit' }).click()
    const body = page.getByRole('textbox', { name: 'Message' })
    await body.fill('Please review. Updated.')
    await page.getByRole('button', { name: /save/i }).click()
    await expect(page.getByText('Draft saved', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: /^send$/i }).click()
    const sent = await mail.smtpMessages()
    expect(sent.count).toBe(1)
    expect(sent.messages[0]).toContain('Updated')
  })

  test('compose sends an attachment and the signature, and keeps a Sent copy (US-1, US-5, US-8)', async ({ browser }) => {
    const page = await mailPage(browser)
    await openMail(page)
    await page.getByRole('button', { name: /compose/i }).click()
    const compose = page.getByRole('dialog', { name: 'Compose message' })
    const recipient = compose.getByRole('textbox', { name: 'To' })
    await recipient.fill('ada@example.test')
    await recipient.press('Enter')
    await expect(compose.getByTestId('recipient-chip')).toHaveText('ada@example.test')
    await compose.getByRole('textbox', { name: 'Subject' }).fill('With file')
    await compose.getByRole('textbox', { name: 'Message' }).fill('See attached.')
    await compose.getByLabel('Attach files').setInputFiles({
      name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('notes'),
    })
    await compose.getByRole('button', { name: 'Send' }).click()
    await expect(page.getByText('Message sent', { exact: true })).toBeVisible()
    const sent = await mail.smtpMessages()
    const last = sent.messages[sent.messages.length - 1]
    expect(last).toContain('Kind regards')
    expect(last).toContain('notes.txt')
    await page.getByRole('tab', { name: /^sent$/i }).click()
    await expect(page.getByText('With file')).toBeVisible()
  })

  // pr-test-analyzer finding (feature-gate round 1): Mail had no real-browser
  // coverage for the CRIT-001/FR-013 "Escape race" class that
  // panelEscape.ts / _fullscreen.panel.$panelId.tsx already handle for other
  // panels (side-panel-expand-multitab.spec.ts W4 is the sibling case for
  // Library). Expected behaviour, from the shell contract, not from running
  // the app: Expand opens Mail full-screen in a NEW tab and closes the
  // docked panel in the original one (W4); Escape in the full-screen tab
  // closes that tab and RE-DOCKS the panel in the original tab with the
  // SAME folder/message context it was expanded with (usePanelShell.ts::
  // expandActivePanel's registerPanelPopout/onClosed re-open, mirrored by
  // MailPanel's own onLocationChange -> registerExpandContext reporting) —
  // never a bare close with the docked panel gone, and never a navigation
  // away from the workspace chat route.
  test('expand to fullscreen, then Escape, returns to the docked panel with the open message intact (CRIT-001/FR-013)', async ({ browser }) => {
    const page = await mailPage(browser)
    await openMail(page)
    const inboxTab = page.getByRole('tab', { name: /inbox/i })
    await expect(inboxTab).toHaveAttribute('aria-selected', 'true')

    const [detailResponse] = await Promise.all([
      page.waitForResponse((response) => {
        const url = new URL(response.url())
        return response.request().method() === 'GET'
          && /\/folders\/inbox\/messages\/[^/]+$/.test(url.pathname)
      }),
      page.getByText('Quarterly').click(),
    ])
    expect(detailResponse.status()).toBe(200)
    await expect(page.getByText('The numbers are in.')).toBeVisible()

    // Expand: a new tab opens with the full-screen route, and the docked
    // panel in the original tab disappears (W4's own assertion for Library).
    const opened = page.context().waitForEvent('page')
    await page.getByTestId('panel-expand').click()
    const pop = await opened
    await expect(pop).toHaveURL(/\/panel\/mail/)
    await expect(page.getByTestId('side-panel')).toHaveCount(0)

    // The full-screen tab carries the SAME message context over (the
    // context this test exercises on the way back).
    await expect(pop.getByTestId('fullscreen-panel')).toBeVisible()
    await expect(pop.getByText('The numbers are in.')).toBeVisible()

    // Escape in the full-screen tab must close it and re-dock Mail in the
    // ORIGINAL tab — not just close the tab and leave nothing docked, and
    // not navigate the original tab away from workspace chat.
    await pop.keyboard.press('Escape')
    await pop.waitForEvent('close')

    await expect(page).toHaveURL(/\/workspaces\/[^/]+\/chat/)
    await expect(page.getByTestId('mail-panel')).toBeVisible()
    await expect(inboxTab).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByText('The numbers are in.')).toBeVisible()
  })
})
