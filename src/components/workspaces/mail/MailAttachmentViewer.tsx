/**
 * MailAttachmentViewer.tsx — the Mail-panel host of the W3 temporary
 * attachment viewer (spec §4 US-6 AS-3/AS-5, US-7; §11 S-20; §7 scenarios
 * 7.1–7.5). W3 publishes the mount seam (`mailAttachmentHandoff.ts`); this
 * component is the fallback CONSUMER of that seam so Open is reachable in
 * the built app before the Library-side integration lands:
 *
 *   - It registers the ONE mount handler only when no host is already
 *     registered (`hasMailAttachmentMountHandler`): an earlier-registered
 *     host — the Library viewer's own registration, or a test stand-in —
 *     keeps the seam; this component yields. When nothing else hosts, the
 *     panel does, and Open hands off here instead of falling into the
 *     mount-less "preview_unavailable" branch.
 *   - The surface renders the context bar reading exactly
 *     "From mail: <subject> · Back to mail · Save to Library" (US-6 AS-3,
 *     built from `@/lib/mailAttachmentPreviewSource`'s pinned constants),
 *     moves focus to the trusted heading — never into rendered content
 *     (US-7 AS-1) — and routes Back/Escape through the seam's `onBack`, so
 *     disposal, revocation and the focus-return rules stay in ONE place
 *     (the handoff module's — never a second implementation).
 *   - The five stored-file actions render disabled with the pinned
 *     "Save to Library first." explanation in place — visible without
 *     hover, screen-reader exposed, tab-discoverable (US-7 AS-4, S-20,
 *     scenario 7.5) — because nothing here may treat a temporary preview
 *     as a saved file (spec §6).
 *   - Save uses the seam's own save controller (M-02 token semantics,
 *     S-15/S-16/S-17 announcements without focus movement); success enables
 *     "Open in Library" (US-6 AS-5). The save route's identity (pair,
 *     folder, message ref, part index — the generated request keeps its
 *     body to observer + token) comes from the open panel's scope via
 *     props, the same source the attachment rows use — never fabricated
 *     from the descriptor, whose frozen payload (spec §3.1) carries no
 *     route identity.
 *   - Content renders through the preview source's resource policy — the
 *     minted byte resource only (US-8); the full renderer matrix stays the
 *     ADR-W8 renderer work's to replace.
 */
import React, { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/store/ui'
import {
  createMailAttachmentSaveController,
  hasMailAttachmentMountHandler,
  setMailAttachmentMountHandler,
  type MailAttachmentHandoff,
  type MailHandoffControls,
} from './mailAttachmentHandoff'
import {
  MAIL_CONTEXT_BAR_PREFIX,
  MAIL_SAVE_FIRST_EXPLANATION,
  createMailAttachmentPreviewSource,
  type MailAttachmentPreviewSource,
} from '@/lib/mailAttachmentPreviewSource'

const SAVED_FIRST_NOTE_ID = 'mail-attachment-save-first-note'

/** The stored-file actions a saved Library file offers and a temporary
 * preview must not (US-7 AS-4's list). */
const STORED_FILE_ACTIONS = ['Edit', 'Rename', 'Move', 'Download', 'Fill & sign'] as const

export interface MailAttachmentViewerProps {
  workspaceId: string
  /** The open panel's mailbox/folder/message — the save route's identity
   * (the generated request body carries only observer + token). */
  agentId: string
  folder: 'inbox' | 'sent' | 'drafts'
  messageRef: string
}

export function MailAttachmentViewer({ workspaceId, agentId, folder, messageRef }: MailAttachmentViewerProps): React.JSX.Element | null {
  const [handoff, setHandoff] = useState<MailAttachmentHandoff | null>(null)
  const controlsRef = useRef<MailHandoffControls | null>(null)
  const sourceRef = useRef<MailAttachmentPreviewSource | null>(null)
  const registeredRef = useRef(false)
  const headingRef = useRef<HTMLHeadingElement | null>(null)

  // The seam: defer to an already-registered host; otherwise this panel is
  // the host. Only the registration THIS component made is ever cleared.
  useEffect(() => {
    if (hasMailAttachmentMountHandler()) return undefined
    registeredRef.current = true
    setMailAttachmentMountHandler((next, controls) => {
      controlsRef.current = controls
      sourceRef.current = createMailAttachmentPreviewSource(next.source)
      setHandoff(next)
    })
    return () => {
      if (!registeredRef.current) return
      registeredRef.current = false
      setMailAttachmentMountHandler(null)
      // The panel went away mid-view: revoke the grant best-effort (the
      // handoff module's TTL is the backstop). Focus return belongs to
      // onBack — there is no Mail surface to return to.
      void sourceRef.current?.revoke()
    }
  }, [])

  const saveController = useMemo(() => createMailAttachmentSaveController(), [])
  const saveStatus = useSyncExternalStore(saveController.subscribe, saveController.getStatus)

  // A new handoff is a new save target: the prior receipt must not leak in.
  useEffect(() => {
    saveController.reset()
  }, [handoff, saveController])

  // US-7 AS-1: focus moves to the context bar's trusted heading on mount.
  useEffect(() => {
    if (handoff !== null) headingRef.current?.focus()
  }, [handoff])

  if (handoff === null) return null

  const source = sourceRef.current
  const closeViewer = () => {
    // The seam's onBack owns disposal, revocation and the focus-return
    // fallback (US-7 AS-2) — the viewer only stops rendering.
    controlsRef.current?.onBack()
    controlsRef.current = null
    sourceRef.current = null
    setHandoff(null)
  }
  const byteResource = source !== null ? source.resolveResource(source.byteResource)?.url ?? null : null
  const isImage = source?.contentType.startsWith('image/') === true

  return (
    /* Covers the panel while the temporary preview is open; Back returns to
       Mail (US-6 AS-4). Escape is the keyboard exit (US-7 AS-5). */
    <div
      data-testid="mail-attachment-viewer"
      role="region"
      aria-label="Attachment preview"
      className="absolute inset-0 z-40 flex flex-col bg-[var(--color-surface-0)]"
      onKeyDown={(e) => {
        if (e.key === 'Escape') closeViewer()
      }}
    >
      {/* The context bar — outside the rendered content (US-6 AS-3); the
          "·" separators keep the bar's text reading exactly the pinned
          "From mail: <subject> · Back to mail · Save to Library". */}
      <div className="flex shrink-0 flex-wrap items-center gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2)]">
        <h2
          ref={headingRef}
          tabIndex={-1}
          className="min-w-0 truncate text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]"
        >
          {`${MAIL_CONTEXT_BAR_PREFIX}${handoff.context.subject}`}
        </h2>
        <span aria-hidden="true" className="text-[var(--color-muted)]">{' · '}</span>
        <Button variant="outline" size="sm" onClick={closeViewer}>
          Back to mail
        </Button>
        <span aria-hidden="true" className="text-[var(--color-muted)]">{' · '}</span>
        <Button
          size="sm"
          disabled={saveStatus.stage === 'loading'}
          aria-busy={saveStatus.stage === 'loading' || undefined}
          onClick={() => {
            void saveController.save({
              workspaceId,
              agentId,
              folder,
              messageRef,
              partIndex: handoff.source.attachment.part_index,
            }).catch(() => undefined)
          }}
        >
          Save to Library
        </Button>
        {saveStatus.stage === 'saved' && (
          <Button
            variant="outline"
            size="sm"
            data-testid="mail-attachment-viewer-open-in-library"
            onClick={() => {
              useUiStore.getState().openPanel('library', { workspaceId, path: saveStatus.response.path })
            }}
          >
            Open in Library
          </Button>
        )}
      </div>
      {/* US-7 AS-4 / S-20: the stored-file actions render disabled with
          their explanation in place — visible without hover, screen-reader
          exposed, and tab-discoverable (aria-disabled, not removed from
          the tab order). */}
      <div className="flex shrink-0 flex-wrap items-center gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2)]">
        {STORED_FILE_ACTIONS.map((action) => (
          <Button
            key={action}
            variant="ghost"
            size="sm"
            aria-disabled="true"
            aria-describedby={SAVED_FIRST_NOTE_ID}
            onClick={(e) => e.preventDefault()}
          >
            {action}
          </Button>
        ))}
        <p id={SAVED_FIRST_NOTE_ID} data-testid="mail-attachment-save-first-note" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          {`${MAIL_SAVE_FIRST_EXPLANATION}.`}
        </p>
      </div>
      {/* The preview content: the minted byte resource only, resolved
          through the source's policy (US-8). The ADR-W8 renderer work
          replaces this minimal frame per kind. */}
      <div className="min-h-0 flex-1 overflow-auto p-[var(--space-3)]">
        {byteResource !== null && isImage ? (
          <img src={byteResource} alt={source?.filename ?? ''} className="max-h-full max-w-full" />
        ) : byteResource !== null ? (
          <embed src={byteResource} type={source?.contentType} title={source?.filename ?? ''} className="h-full w-full" />
        ) : null}
      </div>
    </div>
  )
}
