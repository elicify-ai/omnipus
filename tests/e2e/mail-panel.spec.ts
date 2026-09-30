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
import { getFreePort } from './setup.js'
import type { MailMessagePage, MailMessage } from '../../src/lib/api/generated/openapi-types'

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

  // f5f6-round2 Item 1 — CONFIRMED REGRESSION (squad-lead's own browser-check
  // run on this candidate before today's fixes had this step passing).
  // Opening Drafts a SECOND time, after a full compose/edit-draft/send cycle
  // followed by a round trip through a no-mailbox workspace and an
  // unreachable-mailbox workspace and back, must still show the real drafts
  // — never MailMessageList's "No messages" (messages.length === 0) branch,
  // which would make the Cc/Bcc/attachment controls (F5) and the docked
  // 20/80 split (F6) unreachable/unobservable.
  //
  // Own workspaces (never the shared `workspaceId`/seeded draft above) so
  // this assertion never depends on what the OTHER tests in this file have
  // already consumed from the one seeded draft.
  test('re-opening Drafts after a workspace round trip still shows the real drafts, not "No messages" (f5f6-round2 Item 1)', async ({ browser }) => {
    const wsA = await gw.apiFetch<{ id: string }>('POST', '/api/v1/workspaces', {
      name: `Mail regression A ${Date.now()}`,
      core_team: ['mia'],
    })
    if (!wsA.ok) throw new Error(`create workspace A ${wsA.status}: ${wsA.raw}`)
    const workspaceA = wsA.body.id
    const savedA = await gw.apiFetch('PUT', `/api/v1/agents/mia/mailboxes/${workspaceA}`, {
      enabled: true,
      imap_host: mail.imapHost,
      imap_port: mail.imapPort,
      smtp_host: mail.smtpHost,
      smtp_port: mail.smtpPort,
      username: mail.user,
      password: mail.password,
    })
    if (!savedA.ok) throw new Error(`configure mailbox A ${savedA.status}: ${savedA.raw}`)

    const wsB = await gw.apiFetch<{ id: string }>('POST', '/api/v1/workspaces', {
      name: `Mail regression B ${Date.now()}`,
      core_team: ['mia'],
    })
    if (!wsB.ok) throw new Error(`create workspace B ${wsB.status}: ${wsB.raw}`)
    const workspaceB = wsB.body.id
    // Deliberately no mailbox configured for B (US-3 AS-3's empty state).

    const wsC = await gw.apiFetch<{ id: string }>('POST', '/api/v1/workspaces', {
      name: `Mail regression C ${Date.now()}`,
      core_team: ['mia'],
    })
    if (!wsC.ok) throw new Error(`create workspace C ${wsC.status}: ${wsC.raw}`)
    const workspaceC = wsC.body.id
    const unreachablePort = await getFreePort()
    const savedC = await gw.apiFetch('PUT', `/api/v1/agents/mia/mailboxes/${workspaceC}`, {
      enabled: true,
      imap_host: '127.0.0.1',
      imap_port: unreachablePort,
      smtp_host: '127.0.0.1',
      smtp_port: unreachablePort,
      username: 'nobody@example.test',
      password: 'x',
    })
    if (!savedC.ok) throw new Error(`configure mailbox C ${savedC.status}: ${savedC.raw}`)

    // 3 of THIS test's own drafts (same physical fake mailbox as the shared
    // fixture, distinct Message-IDs/subjects) so the "still 3+" assertion
    // never depends on other tests' consumption of the shared seeded draft.
    const regressionDraft = (n: number) => [
      'From: mailbox@test.local',
      'To: ada@example.test',
      `Subject: Regression draft ${n}`,
      'Date: Mon, 02 Jan 2006 15:04:05 +0000',
      `Message-ID: <regression-draft-${n}@example.test>`,
      'X-Omnipus-Draft: 1',
      'MIME-Version: 1.0',
      'Content-Type: text/plain; charset=utf-8',
      '',
      'Please review.',
      '',
    ].join('\r\n')
    await mail.append('Drafts', regressionDraft(1), ['\\Draft'])
    await mail.append('Drafts', regressionDraft(2), ['\\Draft'])
    await mail.append('Drafts', regressionDraft(3), ['\\Draft'])

    const page = await mailPage(browser)

    // Log every drafts-folder list fetch's real response body, so the
    // eventual assertion is backed by evidence of whether the backend
    // itself ever returned zero messages (a genuine data bug) or always
    // returned the real list (a client-side display/cache bug) — brief's
    // explicit ask, item 1.
    const draftsListResponses: Array<{ status: number; body: string }> = []
    page.on('response', (response) => {
      const url = new URL(response.url())
      if (response.request().method() === 'GET' && /\/folders\/drafts\/messages(\?|$)/.test(url.pathname)) {
        response.text().then((body) => draftsListResponses.push({ status: response.status(), body })).catch(() => {})
      }
    })

    // 1. Compose a new message in workspace A, Send.
    await page.goto(`/#/workspaces/${workspaceA}/chat`)
    const mailToggle = page.getByTestId('workspace-tab-mail')
    await mailToggle.click()
    await expect(page.getByTestId('mail-panel')).toBeVisible()
    await page.getByRole('button', { name: /compose/i }).click()
    const compose1 = page.getByRole('dialog', { name: 'Compose message' })
    const recipient1 = compose1.getByRole('textbox', { name: 'To' })
    await recipient1.fill('ada@example.test')
    await recipient1.press('Enter')
    await compose1.getByRole('textbox', { name: 'Subject' }).fill('Round trip test')
    await compose1.getByRole('textbox', { name: 'Message' }).fill('Hello.')
    await compose1.getByRole('button', { name: 'Send' }).click()
    await expect(page.getByText('Message sent', { exact: true })).toBeVisible()

    // 2. Open a Drafts item, Edit it, Save, Send.
    const draftsTab = page.getByRole('tab', { name: 'Drafts' })
    await draftsTab.click()
    await expect(draftsTab).toHaveAttribute('aria-selected', 'true')
    await page.getByRole('button', { name: /Regression draft 1/ }).click()
    await page.getByRole('button', { name: 'Edit' }).click()
    await page.getByRole('textbox', { name: 'Message' }).fill('Please review. Updated.')
    await page.getByRole('button', { name: /^save$/i }).click()
    await expect(page.getByText('Draft saved', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: /^send$/i }).click()

    // 3. Navigate to workspace B (no mailbox) — the empty state.
    await page.goto(`/#/workspaces/${workspaceB}/chat`)
    await expect(page.getByTestId('mail-choose-mailbox')).toBeVisible()

    // 4. Navigate to workspace C (unreachable mailbox) — the connection-error state.
    await page.goto(`/#/workspaces/${workspaceC}/chat`)
    await expect(page.getByTestId('mail-folders-error')).toBeVisible({ timeout: 15_000 })

    // 5. Navigate back to A, open Mail again.
    await page.goto(`/#/workspaces/${workspaceA}/chat`)
    await expect(page.getByTestId('mail-panel')).toBeVisible()

    // 6. Open a compose dialog (unrelated check), Escape out.
    await page.getByRole('button', { name: /compose/i }).click()
    const compose2 = page.getByRole('dialog', { name: 'Compose message' })
    await expect(compose2).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(compose2).toHaveCount(0)

    // 7. Click Drafts — must show the real drafts, never "No messages".
    await page.getByRole('tab', { name: 'Drafts' }).click()
    await expect(page.getByText('No messages')).toHaveCount(0)
    await expect(page.getByText(/Regression draft 2/)).toBeVisible()

    // Evidence for the brief's "settle UI vs backend bug" ask: attach every
    // observed drafts-list response so a human/report can see whether the
    // backend itself ever served zero messages for this folder.
    await test.info().attach('drafts-list-responses.json', {
      body: JSON.stringify(draftsListResponses, null, 2),
      contentType: 'application/json',
    })
  })

  function outsideDraft(subject: string, messageId: string, body: string): string {
    return [
      'From: mailbox@test.local',
      'To: alice@test.local',
      `Subject: ${subject}`,
      'Date: Mon, 02 Jan 2006 15:04:05 +0000',
      `Message-ID: <${messageId}>`,
      'X-Omnipus-Draft: 1',
      'MIME-Version: 1.0',
      'Content-Type: text/plain; charset=utf-8',
      '', body, '',
    ].join('\r\n')
  }

  function isDraftDetail(response: { url(): string; request(): { method(): string } }): boolean {
    return response.request().method() === 'GET'
      && /\/folders\/drafts\/messages\/[^/]+$/.test(new URL(response.url()).pathname)
  }

  test('opens the updated draft from a stale list after another browser tab saves it', async ({ browser }) => {
    const subject = 'F5 cross-tab draft'
    const messageId = `f5-cross-tab-${Date.now()}@test.local`
    const original = await mail.append('Drafts', outsideDraft(subject, messageId, 'First version.'), ['\\Draft'])
    const tabA = await mailPage(browser)
    const tabB = await mailPage(browser)
    try {
      await openMail(tabA)
      const listResponse = tabA.waitForResponse((response) => response.request().method() === 'GET'
        && /\/folders\/drafts\/messages$/.test(new URL(response.url()).pathname))
      await tabA.getByRole('tab', { name: 'Drafts' }).click()
      const initialList = await (await listResponse).json() as MailMessagePage
      expect(initialList.messages.find((message) => message.subject === subject)?.uid).toBe(original.uid)
      await expect(tabA.getByRole('button', { name: new RegExp(`^${subject}`) })).toBeVisible()

      await openMail(tabB)
      await tabB.getByRole('tab', { name: 'Drafts' }).click()
      await tabB.getByRole('button', { name: new RegExp(`^${subject}`) }).click()
      await tabB.getByRole('button', { name: 'Edit' }).click()
      await tabB.getByRole('textbox', { name: 'Message' }).fill('Updated in browser tab B.')
      const saveResponse = tabB.waitForResponse((response) => response.request().method() === 'PUT'
        && /\/folders\/drafts\/messages\/[^/]+$/.test(new URL(response.url()).pathname))
      await tabB.getByRole('button', { name: /^save$/i }).click()
      const saved = await (await saveResponse).json() as MailMessage
      await expect(tabB.getByText('Draft saved', { exact: true })).toBeVisible()
      expect(saved.uid).not.toBe(original.uid)
      const fresh = await gw.apiFetch<MailMessagePage>('GET', `/api/v1/workspaces/${workspaceId}/mail/mia/folders/drafts/messages`)
      expect(fresh.ok).toBe(true)
      expect(fresh.body.messages.find((message) => message.subject === subject)?.uid).toBe(saved.uid)

      const detailResponse = tabA.waitForResponse(isDraftDetail)
      const refreshedList = tabA.waitForResponse((response) => response.request().method() === 'GET'
        && /\/folders\/drafts\/messages$/.test(new URL(response.url()).pathname) && response.status() === 200)
      await tabA.getByRole('button', { name: new RegExp(`^${subject}`) }).click()
      const detail = await detailResponse
      console.log('F5 cross-tab detail:', JSON.stringify({ oldUid: original.uid, currentUid: saved.uid, url: detail.url(), status: detail.status() }))
      await expect(tabA.getByText('Updated in browser tab B.')).toBeVisible()
      const refreshed = await (await refreshedList).json() as MailMessagePage
      expect(refreshed.messages.find((message) => message.subject === subject)?.uid).toBe(saved.uid)
      await expect(tabA.getByRole('button', { name: new RegExp(`^${subject}`) })).toHaveAttribute('aria-current', 'true')
      await expect(tabA.getByTestId('mail-reading-zone')).not.toContainText('404:')
    } finally {
      await tabA.context().close()
      await tabB.context().close()
    }
  })

  test('opens the updated draft after a background mailbox mutation renumbers its UID', async ({ browser }) => {
    const subject = 'F5 background draft'
    const messageId = `f5-background-${Date.now()}@test.local`
    const original = await mail.append('Drafts', outsideDraft(subject, messageId, 'First background version.'), ['\\Draft'])
    const page = await mailPage(browser)
    try {
      await openMail(page)
      await page.getByRole('tab', { name: 'Drafts' }).click()
      await expect(page.getByRole('button', { name: new RegExp(`^${subject}`) })).toBeVisible()
      const replacement = await mail.append('Drafts', outsideDraft(subject, messageId, 'Updated by the background actor.'), ['\\Draft'])
      await mail.storeFlags('Drafts', original.uid, ['\\Deleted'])
      const fresh = await gw.apiFetch<MailMessagePage>('GET', `/api/v1/workspaces/${workspaceId}/mail/mia/folders/drafts/messages`)
      expect(fresh.ok).toBe(true)
      expect(fresh.body.messages.find((message) => message.subject === subject)?.uid).toBe(replacement.uid)
      const detailResponse = page.waitForResponse(isDraftDetail)
      const refreshedList = page.waitForResponse((response) => response.request().method() === 'GET'
        && /\/folders\/drafts\/messages$/.test(new URL(response.url()).pathname) && response.status() === 200)
      await page.getByRole('button', { name: new RegExp(`^${subject}`) }).click()
      const detail = await detailResponse
      console.log('F5 background detail:', JSON.stringify({ oldUid: original.uid, currentUid: replacement.uid, url: detail.url(), status: detail.status() }))
      await expect(page.getByText('Updated by the background actor.')).toBeVisible()
      const refreshed = await (await refreshedList).json() as MailMessagePage
      expect(refreshed.messages.find((message) => message.subject === subject)?.uid).toBe(replacement.uid)
      await expect(page.getByRole('button', { name: new RegExp(`^${subject}`) })).toHaveAttribute('aria-current', 'true')
      await expect(page.getByTestId('mail-reading-zone')).not.toContainText('404:')
    } finally {
      await page.context().close()
    }
  })

  test('refreshes the list and explains when a draft was deleted elsewhere', async ({ browser }) => {
    const subject = 'F5 deleted draft'
    const original = await mail.append('Drafts', outsideDraft(subject, `f5-deleted-${Date.now()}@test.local`, 'Will be deleted.'), ['\\Draft'])
    const page = await mailPage(browser)
    try {
      await openMail(page)
      await page.getByRole('tab', { name: 'Drafts' }).click()
      await expect(page.getByRole('button', { name: new RegExp(`^${subject}`) })).toBeVisible()
      await mail.storeFlags('Drafts', original.uid, ['\\Deleted'])
      const detailResponse = page.waitForResponse(isDraftDetail)
      await page.getByRole('button', { name: new RegExp(`^${subject}`) }).click()
      const detail = await detailResponse
      console.log('F5 deleted detail:', JSON.stringify({ oldUid: original.uid, url: detail.url(), status: detail.status() }))
      await expect(page.getByTestId('mail-reading-zone')).toContainText('This draft was changed or deleted elsewhere. The list has been refreshed.')
      await expect(page.getByTestId('mail-reading-zone')).not.toContainText('404:')
      await expect(page.getByRole('button', { name: new RegExp(`^${subject}`) })).toHaveCount(0)
    } finally {
      await page.context().close()
    }
  })
})
