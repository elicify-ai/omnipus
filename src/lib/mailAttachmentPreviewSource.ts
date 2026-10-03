/**
 * mailAttachmentPreviewSource.ts — the temporary-source adapter and its
 * source-scoped resource policy (ADR-20261001 F1; w4 spec §3.1/§5.1, grill
 * I-04). Every reused Library renderer receives this policy; it is the ONLY
 * resolver a temporary mail source may hand them.
 *
 * The rule: a sender-authored URL is not trusted merely because it resolves
 * to this same gateway. Only resources MINTED FOR THIS PREVIEW may load —
 * the preview's own token-scoped byte/representation responses, CID inline
 * parts of the same message through the Mail preview prefix, and eligible
 * remote images ONLY through the consent-gated token-scoped proxy. Anything
 * else is refused STRUCTURALLY: the resolver returns null, so no element
 * ever mounts with an unauthorized URL — refusal is not a hidden-attribute
 * trick (w4 spec §5.1 counterexamples C-1..C-4).
 *
 * The policy scopes the TEMPORARY mail source only. An ordinary workspace
 * Library file keeps today's rendering byte-for-byte (founder Q-E=A: after
 * Save, a saved mail file is an ordinary workspace file).
 */

import {
  revokeMailAttachmentPreview,
} from '@/lib/api/mail'
import type { MailAttachmentPreviewResponse } from '@/lib/api/generated/openapi-types'

/** The paths a temporary mail source may reference, all server-minted. */
const MAIL_PREVIEW_PART_PREFIX = '/mail-preview/part/'
const MAIL_PREVIEW_IMG_PREFIX = '/mail-preview/img/'

/**
 * The exact mail context bar, from the founder's F1 decision: the Library
 * viewer renders "From mail: <subject> · Back to mail · Save to Library".
 * ONE constant so the Library-side integration (the panel wave's
 * LibraryPreviewPane edit) renders the specified text and no other
 * (US-1.AC-1: "the context bar reads exactly").
 */
export const MAIL_CONTEXT_BAR_PREFIX = 'From mail: '
export const MAIL_CONTEXT_BAR_FORMAT = `${MAIL_CONTEXT_BAR_PREFIX}<subject> · Back to mail · Save to Library`

/**
 * The disabled stored-file actions' explanation (US-1.AC-2): rendered text,
 * visible without hover, readable by screen readers — never a tooltip-only
 * hint.
 */
export const MAIL_SAVE_FIRST_EXPLANATION = 'Save to Library first'

/**
 * The resolved shape renderers consume: a URL the preview is authorized to
 * load, or null for a structural refusal (never a guessed fallback). Purely
 * in-process: the resolvers' return value is read by renderers to decide
 * which URL may mount — it is never serialized, sent to the gateway, or
 * persisted, so it carries no wire surface to contract for.
 */
export type ResolvedResource = { url: string } | null // not-wire-format: in-process resolver return value consumed only by preview renderers; never serialized or sent to the gateway

export class MailAttachmentPreviewSource {
  readonly kind = 'mail_attachment' as const
  readonly previewId: string
  readonly subject: string
  readonly filename: string
  readonly contentType: string
  readonly partIndex: number
  readonly textReadable: boolean
  readonly readOnly = true as const
  private readonly byteUrl: string
  private readonly isolatedHtmlUrl: string | null
  /** Load-images consent (off by default — the founder's no-tracking rule). */
  private remoteImagesAllowed = false
  private revoked = false

  constructor(resp: MailAttachmentPreviewResponse) {
    this.previewId = resp.preview_id
    this.subject = resp.subject
    this.filename = resp.attachment.filename
    this.contentType = resp.attachment.content_type
    this.partIndex = resp.attachment.part_index
    this.textReadable = resp.text_readable
    this.byteUrl = resp.content_source.byte_url
    this.isolatedHtmlUrl = resp.content_source.isolated_html_url
  }

  /** The viewer's byte source — the preview-purpose resource (never the
   *  authenticated download endpoint; grill I-05's two distinct roles). */
  get byteResource(): string {
    return this.byteUrl
  }

  /** The scriptless HTML projection, when the attachment renders as HTML. */
  get htmlResource(): string | null {
    return this.isolatedHtmlUrl
  }

  /**
   * The extension-derived classification hint: the classifier feeds from
   * the effective extension-derived type, NOT the sender's self-declared
   * content_type (the existing type-confusion rule, w4 spec §2.2).
   */
  get effectiveExtension(): string {
    const dot = this.filename.lastIndexOf('.')
    return dot >= 0 ? this.filename.slice(dot + 1).toLowerCase() : ''
  }

  /**
   * The source-scoped resource policy (I-04). Returns the authorized URL
   * for a sender-authored reference, or null when the reference is NOT one
   * of this preview's minted resources — including same-origin Library/API
   * paths (C-1), which the ordinary renderer would display but this source
   * was never granted.
   */
  resolveResource(rawUrl: string): ResolvedResource {
    if (this.revoked || !rawUrl) return null
    const url = rawUrl.trim()
    // The preview's own minted resources.
    if (url === this.byteUrl || url === this.isolatedHtmlUrl) {
      return { url }
    }
    // CID inline parts of the same message, through the Mail preview
    // prefix (server-routed, token-scoped by the minted message preview).
    if (url.startsWith(MAIL_PREVIEW_PART_PREFIX) || url.startsWith(MAIL_PREVIEW_IMG_PREFIX)) {
      return { url }
    }
    // Inline raster data: the same shape the sanitizer's CSS/img policy
    // admits (data:image/(png|gif|jpe?g|webp);base64). SVG and every other
    // data: type are refused — the pipeline deliberately excludes them.
    if (/^data:image\/(?:png|gif|jpe?g|webp);base64,/i.test(url)) {
      return { url }
    }
    // Eligible remote images ONLY after explicit Load-images consent — and
    // then never the raw remote URL: the renderer must call
    // resolveRemoteImage, which the consent gate guards, so the request can
    // only ever ride the token-scoped proxy minted for this preview.
    return null
  }

  /** Set the Load-images consent. Returns the previous value. */
  allowRemoteImages(allowed: boolean): boolean {
    const prev = this.remoteImagesAllowed
    this.remoteImagesAllowed = allowed
    return prev
  }

  get remoteImagesConsented(): boolean {
    return this.remoteImagesAllowed
  }

  /**
   * The consent-gated remote-image resolution: without explicit consent
   * this returns null (zero direct remote requests — C-4); with consent it
   * still does NOT return the raw https URL — the caller receives the
   * authorization to fetch through the preview's pinned proxy path, and the
   * raw URL never reaches a renderer attribute.
   */
  resolveRemoteImage(_rawUrl: string): ResolvedResource {
    if (this.revoked || !this.remoteImagesAllowed) return null
    return { url: `${this.byteUrl}/img-consented` }
  }

  /**
   * Wikilinks and embeds never resolve against the workspace or Library
   * index from a temporary mail source (C-2/C-3): the resolver exists so
   * the ordinary renderer's workspace lookup is never reached.
   */
  resolveWorkspaceTarget(_target: string): ResolvedResource {
    return null
  }

  /** Revoke the minted grant (Back/close/navigate/logout lifecycle). */
  async revoke(): Promise<void> {
    if (this.revoked) return
    this.revoked = true
    try {
      await revokeMailAttachmentPreview(this.previewId)
    } catch {
      // A revoke racing the grant's own expiry is 404 by design (unknown/
      // expired are one answer); a network failure falls back to the TTL —
      // never surfaced as a user-facing error on an exit path.
    }
  }
}

/** Build the temporary source from a minted preview response. */
export function createMailAttachmentPreviewSource(
  resp: MailAttachmentPreviewResponse,
): MailAttachmentPreviewSource {
  return new MailAttachmentPreviewSource(resp)
}
