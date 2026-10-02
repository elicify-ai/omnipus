/**
 * mailAttachmentPreviewSource.test.ts — the temporary source's resource
 * policy (w4 spec §5.1, counterexamples C-1..C-4): refusals are STRUCTURAL
 * (the resolver returns null — no authorized URL is ever produced), the
 * preview's own minted resources load, and remote images load only through
 * the consent gate.
 */

import { describe, it, expect, vi } from 'vitest'

vi.mock('@/lib/api/mail', () => ({
  revokeMailAttachmentPreview: vi.fn(async () => undefined),
}))

import {
  MailAttachmentPreviewSource,
  createMailAttachmentPreviewSource,
} from './mailAttachmentPreviewSource'
import type { MailAttachmentPreviewResponse } from '@/lib/api/generated/openapi-types'

function mintResponse(overrides?: Partial<MailAttachmentPreviewResponse>): MailAttachmentPreviewResponse {
  return {
    kind: 'mail_attachment',
    preview_id: 'at_test_token',
    subject: 'Quarterly report',
    attachment: {
      content_type: 'text/markdown',
      filename: 'notes.md',
      part_index: 2,
      size_bytes: 1024,
    },
    text_readable: true,
    content_source: {
      byte_url: '/mail-preview/attachment/at_test_token',
      isolated_html_url: null,
      token: 'at_test_token',
      expires_in_seconds: 900,
    },
    read_only: true,
    ...overrides,
  } as MailAttachmentPreviewResponse
}

describe('MailAttachmentPreviewSource resource policy (w4 §5.1)', () => {
  it('C-1: refuses a same-origin Library URL structurally', () => {
    const src = createMailAttachmentPreviewSource(mintResponse())
    expect(
      src.resolveResource(
        '/api/v1/workspaces/ws_1/library/download?path=secret-marker.txt',
      ),
    ).toBeNull()
  })

  it('C-2/C-3: never resolves a workspace target (wikilink/embed)', () => {
    const src = createMailAttachmentPreviewSource(mintResponse())
    expect(src.resolveWorkspaceTarget('some-note')).toBeNull()
    expect(src.resolveResource('[[some-note]]')).toBeNull()
  })

  it('C-4: a remote image without consent resolves to nothing', () => {
    const src = createMailAttachmentPreviewSource(mintResponse())
    expect(src.resolveResource('https://attacker.example/pixel')).toBeNull()
    expect(src.resolveRemoteImage('https://attacker.example/pixel')).toBeNull()
  })

  it('C-4 positive gate: consented remote images never yield the raw URL', () => {
    const src = createMailAttachmentPreviewSource(mintResponse())
    src.allowRemoteImages(true)
    const resolved = src.resolveRemoteImage('https://attacker.example/pixel')
    expect(resolved).not.toBeNull()
    expect(resolved?.url).not.toContain('attacker.example')
    expect(resolved?.url.startsWith('/mail-preview/attachment/')).toBe(true)
  })

  it('the preview\'s own minted resources are allowed', () => {
    const src = createMailAttachmentPreviewSource(mintResponse())
    expect(src.resolveResource('/mail-preview/attachment/at_test_token')).toEqual({
      url: '/mail-preview/attachment/at_test_token',
    })
    expect(src.resolveResource('/mail-preview/part/at_msg_token/0')).toEqual({
      url: '/mail-preview/part/at_msg_token/0',
    })
    expect(
      src.resolveResource('data:image/png;base64,aGVsbG8='),
    ).toEqual({ url: 'data:image/png;base64,aGVsbG8=' })
  })

  it('data:image/svg+xml and non-raster data: URLs are refused (the sanitizer-only gate)', () => {
    const src = createMailAttachmentPreviewSource(mintResponse())
    expect(src.resolveResource('data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=')).toBeNull()
    expect(src.resolveResource('data:text/html;base64,PHNjcmlwdD4=')).toBeNull()
  })

  it('a revoked source refuses everything', async () => {
    const src = createMailAttachmentPreviewSource(mintResponse())
    await src.revoke()
    expect(src.resolveResource(src.byteResource)).toBeNull()
    expect(src.resolveRemoteImage('https://x.example/i.png')).toBeNull()
  })

  it('feeds the classifier from the extension-derived type, not the declared one', () => {
    const src = createMailAttachmentPreviewSource(
      mintResponse({
        attachment: {
          content_type: 'application/octet-stream',
          filename: 'photo.PNG',
          part_index: 0,
          size_bytes: 12,
        },
      }),
    )
    expect(src.effectiveExtension).toBe('png')
  })
})
