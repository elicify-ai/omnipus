// mail.retry.test.ts — the A8 contract's human-initiated `retry` marker
// (D29/R2-9, MC-33) on the four mail dialing GETs (contracts/openapi.yaml:
// folders / messages list / one message / attachment): opts.retry === true
// appends `retry=true` to the outgoing URL; absent/false leaves the marker
// off — the automatic-poll posture, which the gateway refuses with
// 503 code=backoff while the watcher is backing off. The oracle is the
// CONTRACT — the URL the gateway parses — never the implementation's
// internals.
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  fetchMailAttachment,
  fetchMailFolders,
  fetchMailMessage,
  fetchMailMessages,
} from './mail'

afterEach(() => {
  vi.restoreAllMocks()
})

function stubFetchJson(body: unknown): ReturnType<typeof vi.fn> {
  // A fresh Response per call — a Response body is consumable ONCE, so a
  // shared instance turns every call after the first into "not valid JSON".
  return vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  }))
}

function lastUrl(): string {
  const call = vi.mocked(globalThis.fetch).mock.calls.at(-1)
  if (call === undefined) throw new Error('fetch was never called')
  return String(call[0])
}

const FOLDER_LIST = { folders: [{ slug: 'inbox', display_name: 'INBOX', total: 0, unread_count: null }] }
const MESSAGE_SUMMARY = {
  message_id: '<m1@t.test>',
  uid: 1,
  uidvalidity: 1,
  folder: 'inbox',
  subject: 'S',
  from: 'a@b.test',
  from_name: null,
  to: ['c@d.test'],
  cc: [],
  date: '2026-09-26T10:00:00Z',
  seen: true,
  is_draft: false,
  is_omnipus_draft: false,
  read_by_agent: false,
}
const MESSAGE_PAGE = { messages: [MESSAGE_SUMMARY], truncated: false, next_before_uid: null }
const MESSAGE = {
  ...MESSAGE_SUMMARY,
  reply_to: null,
  in_reply_to: null,
  references: null,
  body_text: 'hi',
  has_html: false,
  bcc: null,
  attachments: [],
  body_markdown: null,
  markdown_lossy: false,
}

describe('mail dialing GETs — A8 retry=true marker (D29/R2-9, MC-33)', () => {
  it('folders: retry=true appends the marker; the default omits it', async () => {
    stubFetchJson(FOLDER_LIST)
    await fetchMailFolders('ws-1', 'mia', { retry: true })
    expect(lastUrl()).toContain('/folders?retry=true')
    await fetchMailFolders('ws-1', 'mia', {})
    expect(lastUrl()).toContain('/folders')
    expect(lastUrl()).not.toContain('?')
  })

  it('message list: retry=true appends the marker after any pagination params', async () => {
    stubFetchJson(MESSAGE_PAGE)
    await fetchMailMessages('ws-1', 'mia', 'inbox', { limit: 20, retry: true })
    const url = lastUrl()
    expect(url).toContain('/messages?')
    expect(url).toContain('limit=20')
    expect(url).toContain('retry=true')
    await fetchMailMessages('ws-1', 'mia', 'inbox')
    expect(lastUrl()).not.toContain('retry=true')
  })

  it('one message: retry=true appends the marker; the mid: form keeps it on the encoded ref', async () => {
    stubFetchJson(MESSAGE)
    await fetchMailMessage('ws-1', 'mia', 'inbox', 'uid:12:34', { retry: true })
    expect(lastUrl()).toContain('/messages/uid%3A12%3A34?retry=true')
    await fetchMailMessage('ws-1', 'mia', 'inbox', 'uid:12:34')
    expect(lastUrl()).not.toContain('retry=true')
  })

  it('attachment: retry=true appends the marker to the raw fetch URL', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(new Blob(['x']), { status: 200 }))
    await fetchMailAttachment('ws-1', 'mia', 'inbox', 'uid:12:34', 1, { retry: true })
    expect(lastUrl()).toContain('/attachments/1?retry=true')
    await fetchMailAttachment('ws-1', 'mia', 'inbox', 'uid:12:34', 1)
    expect(lastUrl()).not.toContain('retry=true')
  })
})
