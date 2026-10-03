/**
 * MailAttachmentsPane.tsx — the attachment action row set for the Mail
 * reading pane (ADR-20261001 F1/F2/F7; w4 spec US-1/US-2). The panel wave
 * integrates this component (MailPanel.tsx is that wave's file); the
 * integration points are listed in the w4 build report.
 *
 * Actions per attachment: Open (temporary Library viewer — no file written),
 * Save to Library (the explicit export; the "Save result unknown" state and
 * its explicit same-token retry live here), and the browser Download
 * (unchanged behaviour — never a synonym for Save). Over-cap attachments
 * (known before any fetch, from the descriptor's size) offer Download only,
 * with the cap explanation visible and screen-reader readable.
 *
 * Accessibility (§5.3/D16): every action carries an accessible name that
 * names the attachment; the Save-first explanation on disabled actions is
 * rendered text, not a tooltip-only hint; Save outcomes announce through the
 * caller's live region without moving focus.
 */

import React, { useCallback, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { saveMailAttachmentToLibrary } from '@/lib/api/mail'
import type { MailMessage } from '@/lib/api/generated/openapi-types'

/** The 25 MiB preview/save cap — the same number the server enforces on
 *  actual decoded bytes; the descriptor's size decides the upfront
 *  availability (honest known-size form of US-1.AC-4). */
const MAIL_ATTACHMENT_PREVIEW_CAP_BYTES = 25 * 1024 * 1024

export interface MailAttachmentsPaneProps {
  workspaceId: string
  agentId: string
  folder: string
  messageRef: string
  attachments: MailMessage['attachments']
  /** Open: the panel mounts the temporary Library viewer (its half of the
   *  handoff — w3 owns the descriptor/contract; this pane only asks). */
  onOpen: (attachment: MailMessage['attachments'][number]) => void
  /** The existing browser Download — passed through so Download stays
   *  exactly what it is today. */
  onDownload: (attachment: MailMessage['attachments'][number]) => void
  /** Save succeeded: the panel offers "Open in Library" (the real entry). */
  onSaved?: (path: string) => void
}

type SaveState =
  | { kind: 'idle' }
  | { kind: 'saving' }
  | { kind: 'saved'; path: string }
  | { kind: 'unknown'; token: string; partIndex: number }
  | { kind: 'failed'; reason: string }

function formatBytes(n: number): string {
  if (n >= 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${n} B`
}

function attachmentLabel(name: string, action: string): string {
  return `${action} ${name}`
}

export function MailAttachmentsPane(props: MailAttachmentsPaneProps): React.JSX.Element {
  const { workspaceId, agentId, folder, messageRef, attachments, onOpen, onDownload, onSaved } = props
  const [saveState, setSaveState] = useState<SaveState>({ kind: 'idle' })
  const saveStateRef = useRef<SaveState>(saveState)
  saveStateRef.current = saveState

  const runSave = useCallback(
    async (partIndex: number, token: string) => {
      setSaveState({ kind: 'saving' })
      try {
        const receipt = await saveMailAttachmentToLibrary(
          workspaceId, agentId, folder, messageRef, partIndex,
          { save_operation_token: token },
        )
        setSaveState({ kind: 'saved', path: receipt.path })
        onSaved?.(receipt.path)
      } catch (err) {
        // A lost response (network failure after the server may have
        // committed) is the "Save result unknown" state: it claims NEITHER
        // success nor failure and never re-sends on its own (US-2.AC-6) —
        // only the explicit Retry button re-sends, carrying the SAME token.
        const lost = err instanceof TypeError || (err instanceof Error && /fetch|network/i.test(err.message))
        if (lost) {
          setSaveState({ kind: 'unknown', token, partIndex })
        } else {
          const reason = err instanceof Error ? err.message : 'the server refused the save'
          setSaveState({ kind: 'failed', reason })
        }
      }
    },
    [workspaceId, agentId, folder, messageRef, onSaved],
  )

  const beginSave = useCallback(
    (partIndex: number) => {
      // A Save click is ALWAYS a new deliberate action: fresh token (a
      // different token is a new request by contract). Only the explicit
      // "Retry save" control (retrySave below) re-sends the SAME token of
      // an unresolved save.
      const token = `sv-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
      void runSave(partIndex, token)
    },
    [runSave],
  )

  return (
    <div className="flex flex-col gap-[var(--space-2)]">
      {attachments.map((attachment) => {
        const name = attachment.filename
        const knownSize = attachment.size_bytes
        const overCap = knownSize > MAIL_ATTACHMENT_PREVIEW_CAP_BYTES
        const capTitle = overCap
          ? `Over the 25 MB preview cap (${formatBytes(knownSize)}). Use Download instead.`
          : undefined
        return (
          <div key={attachment.part_index} className="flex flex-wrap items-center gap-[var(--space-2)]">
            <span className="text-[length:var(--font-size-caption)]" aria-hidden="true">
              {name}
              {knownSize > 0 ? ` (${formatBytes(knownSize)})` : ''}
            </span>
            <Button
              variant="outline"
              size="sm"
              disabled={overCap}
              aria-disabled={overCap}
              aria-label={attachmentLabel(name, 'Open')}
              title={capTitle}
              onClick={() => onOpen(attachment)}
            >
              Open
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={saveState.kind === 'saving' || overCap}
              aria-disabled={overCap}
              aria-label={attachmentLabel(name, 'Save to Library')}
              title={capTitle}
              onClick={() => beginSave(attachment.part_index)}
            >
              {saveState.kind === 'saving' ? 'Saving…' : 'Save to Library'}
            </Button>
            <Button
              variant="outline"
              size="sm"
              aria-label={attachmentLabel(name, 'Download')}
              onClick={() => onDownload(attachment)}
            >
              Download
            </Button>
            {overCap && (
              <span className="text-[length:var(--font-size-caption)]" role="note">
                {capTitle}
              </span>
            )}
          </div>
        )
      })}
      {saveState.kind === 'unknown' && (
        <div className="flex flex-wrap items-center gap-[var(--space-2)]" role="status">
          <span className="text-[length:var(--font-size-caption)]">
            Save result unknown — the connection was lost before the result arrived. Nothing is
            duplicated by checking.
          </span>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              // The explicit retry re-sends the SAME token: the server
              // answers with the prior receipt when the save committed —
              // exactly one file, never a numbered duplicate.
              void runSave(saveState.partIndex, saveState.token)
            }}
          >
            Retry save
          </Button>
        </div>
      )}
      {saveState.kind === 'saved' && (
        <div className="text-[length:var(--font-size-caption)]" role="status">
          Saved to Library: {saveState.path}
        </div>
      )}
      {saveState.kind === 'failed' && (
        <div className="text-[length:var(--font-size-caption)]" role="alert">
          Could not save to Library — {saveState.reason}
        </div>
      )}
    </div>
  )
}
