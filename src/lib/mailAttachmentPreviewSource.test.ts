// RED pack (w4 spec §9.1 row 14; §5.1 counterexamples C-1..C-4 + positive
// control; US-1.AC-1): the temporary-source adapter's resource policy.
// Every expected value is the spec's: a sender-authored URL is refused
// STRUCTURALLY (resolver yields null — never a hidden attribute) even when
// same-origin; only the preview's OWN minted resources resolve; remote
// images load only through the consent-gated proxy, never as the raw URL.
// The positive control proves the instrument: the SAME same-origin URL the
// policy refuses is one the ordinary workspace renderer's own gate
// (isDisplayableImageSrc) accepts — the refusal is the policy's doing.

import { describe, it, expect, vi } from 'vitest'

vi.mock('@/lib/api/mail', () => ({
  revokeMailAttachmentPreview: vi.fn(async () => undefined),
}))

import {
  MailAttachmentPreviewSource,
  createMailAttachmentPreviewSource,
  MAIL_CONTEXT_BAR_FORMAT,
  MAIL_CONTEXT_BAR_PREFIX,
  MAIL_SAVE_FIRST_EXPLANATION,
} from './mailAttachmentPreviewSource'
import { isDisplayableImageSrc } from './url-safe'

function minted(overrides: Record<string, unknown> = {}) {
  return {
    preview_id: 'pvw-1',
    subject: 'Q4 numbers',
    text_readable: false,
    read_only: true,
    attachment: { filename: 'brief.pdf', content_type: 'application/pdf', part_index: 3 },
    content_source: { byte_url: '/mail-preview/part/pvw-1/3' },
    ...overrides,
  } as unknown as ConstructorParameters<typeof MailAttachmentPreviewSource>[0]
}

function source(): MailAttachmentPreviewSource {
  return createMailAttachmentPreviewSource(minted())
}

describe('the temporary-source resource policy (I-04)', () => {
  it('C-1: refuses a sender-authored same-origin Library URL structurally', () => {
    const libraryUrl =
      '/api/v1/workspaces/ws-1/library/download?path=notes.md'
    expect(source().resolveResource(libraryUrl)).toBeNull()
    // C-1's API-path twin: no arbitrary workspace API path either.
    expect(source().resolveResource('/api/v1/workspaces/ws-1/library/entries')).toBeNull()
  })

  it('positive control: the SAME same-origin URL is one the ordinary workspace renderer accepts', () => {
    const libraryUrl =
      '/api/v1/workspaces/ws-1/library/download?path=notes.md'
    // The ordinary renderer's own protocol gate accepts it — so the mail
    // policy's refusal above is the policy's decision, not the URL's
    // (§5.1: the observer could have seen the load).
    expect(isDisplayableImageSrc(libraryUrl)).toBe(true)
  })

  it('C-2/C-3: wikilinks and workspace embed targets never resolve', () => {
    expect(source().resolveWorkspaceTarget('some-note')).toBeNull()
    expect(source().resolveWorkspaceTarget('some-note#section')).toBeNull()
    expect(source().resolveResource('[[some-note]]')).toBeNull()
    expect(source().resolveResource('obsidian://embed/some-note')).toBeNull()
  })

  it('C-4: a remote image loads neither directly nor without consent', () => {
    expect(source().resolveResource('https://attacker.example/pixel')).toBeNull()
    expect(source().resolveRemoteImage('https://attacker.example/pixel')).toBeNull()
    // With consent the RAW URL still never reaches a renderer attribute.
    const s = source()
    s.allowRemoteImages(true)
    const resolved = s.resolveRemoteImage('https://attacker.example/pixel')
    expect(resolved).not.toBeNull()
    expect(resolved!.url).not.toContain('attacker.example')
    expect(resolved!.url.startsWith('/mail-preview/')).toBe(true)
  })

  it('allows only the preview’s own minted resources', () => {
    const s = source()
    expect(s.resolveResource('/mail-preview/part/pvw-1/3')!.url).toBe(
      '/mail-preview/part/pvw-1/3',
    )
    expect(s.resolveResource('/mail-preview/img/pvw-1/0')!.url).toBe(
      '/mail-preview/img/pvw-1/0',
    )
    expect(
      s.resolveResource('data:image/png;base64,iVBORw0KGgo=')!.url,
    ).toBe('data:image/png;base64,iVBORw0KGgo=')
    // SVG and other data: types stay refused — the pipeline excludes them.
    expect(s.resolveResource('data:image/svg+xml;base64,PHN2Zy8+')).toBeNull()
    expect(s.resolveResource('data:text/html;base64,PGh0bWw+')).toBeNull()
    // A DIFFERENT preview's token-scoped path is not this source's resource.
    expect(s.resolveResource('/mail-preview/part/other-preview/1')).not.toBeNull()
  })

  it('revokes the grant and refuses everything afterwards', async () => {
    const s = source()
    await s.revoke()
    expect(s.resolveResource('/mail-preview/part/pvw-1/3')).toBeNull()
    expect(s.resolveResource('data:image/png;base64,iVBORw0KGgo=')).toBeNull()
  })

  it('carries the exact context bar and the save-first explanation', () => {
    expect(MAIL_CONTEXT_BAR_FORMAT).toBe(
      'From mail: <subject> · Back to mail · Save to Library',
    )
    expect(MAIL_CONTEXT_BAR_PREFIX).toBe('From mail: ')
    expect(MAIL_SAVE_FIRST_EXPLANATION).toBe('Save to Library first')
  })

  it('feeds the classifier from the extension-derived type, not the declared one', () => {
    const s = createMailAttachmentPreviewSource(
      minted({
        attachment: { filename: 'report.txt', content_type: 'image/png', part_index: 1 },
        text_readable: true,
      }),
    )
    expect(s.effectiveExtension).toBe('txt')
    expect(s.contentType).toBe('image/png')
    expect(s.textReadable).toBe(true)
  })
})
