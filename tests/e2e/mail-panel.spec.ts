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
    const created = await gw.apiFetch<{ id: string }>('POST', '/api/v1/workspaces', { name: `Mail ${Date.now()}` })
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
    await page.goto(`/#/workspaces/${workspaceId}/mail`)
    await page.getByRole('tab', { name: /^mail$/i }).click()
  }

  test('reads the seeded inbox message (US-3)', async ({ browser }) => {
    const page = await mailPage(browser)
    await openMail(page)
    await expect(page.getByText('Quarterly')).toBeVisible()
    await page.getByText('Quarterly').click()
    await expect(page.getByText('The numbers are in.')).toBeVisible()
  })

  test('opening a message drops the Inbox unread count (US-6)', async ({ browser }) => {
    const page = await mailPage(browser)
    await openMail(page)
    const inbox = page.getByRole('tab', { name: /inbox/i })
    await expect(inbox).toContainText('1')
    await page.getByText('Quarterly').click()
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
    await page.getByRole('tab', { name: /drafts/i }).click()
    await page.getByText('Draft note').click()
    await page.getByRole('button', { name: /edit/i }).click()
    const body = page.getByRole('textbox', { name: /body|message/i })
    await body.fill('Please review. Updated.')
    await page.getByRole('button', { name: /save/i }).click()
    await page.getByRole('button', { name: /^send$/i }).click()
    const sent = await mail.smtpMessages()
    expect(sent.count).toBe(1)
    expect(sent.messages[0]).toContain('Updated')
  })

  test('compose sends an attachment and the signature, and keeps a Sent copy (US-1, US-5, US-8)', async ({ browser }) => {
    const page = await mailPage(browser)
    await openMail(page)
    await page.getByRole('button', { name: /compose/i }).click()
    await page.getByRole('textbox', { name: /^to$/i }).fill('ada@example.test')
    await page.getByRole('textbox', { name: /subject/i }).fill('With file')
    await page.getByRole('textbox', { name: /body|message/i }).fill('See attached.')
    await page.getByLabel(/attach/i).setInputFiles({
      name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('notes'),
    })
    await page.getByRole('button', { name: /^send$/i }).click()
    await expect(page.getByText(/sent/i)).toBeVisible()
    const sent = await mail.smtpMessages()
    const last = sent.messages[sent.messages.length - 1]
    expect(last).toContain('Kind regards')
    expect(last).toContain('notes.txt')
    await page.getByRole('tab', { name: /^sent$/i }).click()
    await expect(page.getByText('With file')).toBeVisible()
  })
})
