// MailInteractiveDemo — the clickable demo shell for the D35 mail prototype
// ("Mail/Demo — Interactive" story). It wraps the REAL mail components
// (folder rail, message list, reading pane, draft panel, compose dialog,
// signature editor, connection banner, mailbox picker) with local React
// state and sample data, so the founder can click through every interaction
// in Storybook: open + read messages, load remote images, switch folders and
// mailboxes, edit/discard/send the draft, compose and send a message, edit
// the signature with its live preview, and clear the connection error.
// Demo-only state — no backend, no endpoints, no persistence (D35 posture).
import { useState } from 'react'
import { CheckCircle, Tray, X } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { MailboxPicker } from './MailPanel'
import { MailComposeDialog, type MailComposeValues } from './MailComposeDialog'
import { MailDraftPanel, type DraftValues } from './MailDraftPanel'
import { MailConnectionBanner } from './MailConnectionBanner'
import { MailFolderRail } from './MailFolderRail'
import { MailMessageList } from './MailMessageList'
import { MailReadingPane } from './MailReadingPane'
import { MailSignatureEditor } from './MailSignatureEditor'
import {
  composeSampleBodyHtml,
  sampleExternalDraft,
  sampleMailData,
} from './sampleMail'
import type {
  ConnectionSample,
  DraftSample,
  FolderKey,
  FolderSummary,
  MailSampleData,
  MessageDetail,
} from './sampleMail'

export interface MailInteractiveDemoProps {
  /** Storybook control: start with the connection-error banner visible. */
  connectionError?: boolean
}

export function MailInteractiveDemo({ connectionError = false }: MailInteractiveDemoProps) {
  const [data, setData] = useState<MailSampleData>(() => buildDemoData(connectionError))
  // The drafts folder's rows come from these DraftSamples (the sample data
  // keeps the draft separate from the message list — see sampleMail.ts).
  const [drafts, setDrafts] = useState<DraftSample[]>(() => [
    structuredClone(sampleMailData.draft),
    structuredClone(sampleExternalDraft),
  ])
  const [folder, setFolder] = useState<FolderKey>('inbox')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [draftOpenId, setDraftOpenId] = useState<string | null>(null)
  const [composeOpen, setComposeOpen] = useState(false)
  const [signatureOpen, setSignatureOpen] = useState(false)
  const [mailboxId, setMailboxId] = useState(data.activeMailboxId)
  const [notice, setNotice] = useState<string | null>(null)

  const activeMailbox = data.mailboxes.find((m) => m.id === mailboxId)
  const selected = selectedId ? data.messages.find((m) => m.id === selectedId) ?? null : null
  const draftOpen = draftOpenId ? drafts.find((d) => d.id === draftOpenId) ?? null : null
  const draftRows = drafts.map(toDraftRow)

  const listFor = (key: FolderKey): MessageDetail[] =>
    key === 'drafts' ? draftRows : data.messages.filter((m) => m.folder === key)

  // Unread counts and totals stay live as the demo mutates its state.
  const folders: FolderSummary[] = data.folders.map((f) => {
    const list = listFor(f.key)
    return { ...f, unread: list.filter((m) => m.unread).length, total: list.length }
  })

  const handleFolderChange = (key: FolderKey) => {
    setFolder(key)
    setSelectedId(null)
    setDraftOpenId(null)
  }

  const handleSelect = (message: MessageDetail) => {
    if (message.folder === 'drafts') {
      // Draft rows open the DraftPanel, not the reading pane.
      setDraftOpenId(message.id)
      setSelectedId(null)
      return
    }
    setDraftOpenId(null)
    setSelectedId(message.id)
    setNotice(null)
    if (message.unread) {
      setData((prev) => ({
        ...prev,
        messages: prev.messages.map((m) => (m.id === message.id ? { ...m, unread: false } : m)),
      }))
    }
  }

  // D17: loading the blocked remote images swaps the sample body to its
  // images-loaded variant (the real feature re-mints its token server-side,
  // FR-019 — the prototype simulates that client-side).
  const handleLoadImages = () => {
    if (!selectedId) return
    setData((prev) => ({
      ...prev,
      messages: prev.messages.map((m) =>
        m.id === selectedId ? { ...m, bodyHtml: composeSampleBodyHtml(true), remoteImagesBlocked: false } : m,
      ),
    }))
  }

  const handleDraftDiscard = (values: DraftValues) => {
    if (!draftOpenId) return
    setDrafts((prev) => prev.filter((d) => d.id !== draftOpenId))
    setDraftOpenId(null)
    setNotice(`Draft "${values.subject}" discarded`)
  }

  // Panel Send IS the approval (D12): the draft leaves Drafts and appears in
  // Sent, and the demo shows a visible confirmation state.
  const handleDraftSend = (values: DraftValues) => {
    if (!draftOpen) return
    const mailboxAddress = activeMailbox?.address ?? ''
    const sent: MessageDetail = {
      id: `sent-${draftOpen.id}-${Date.now()}`,
      folder: 'sent',
      fromName: activeMailbox?.label ?? 'Me',
      fromAddress: mailboxAddress,
      toNames: values.to.length > 0 ? values.to : ['(no recipient)'],
      subject: values.subject,
      preview: firstLine(values.body),
      date: new Date().toISOString(),
      unread: false,
      readByAgent: false,
      hasAttachments: draftOpen.attachments.length > 0,
      ccNames: [],
      bodyKind: 'text',
      bodyHtml: '',
      bodyText: values.body,
      remoteImagesBlocked: false,
      attachments: draftOpen.attachments,
    }
    setData((prev) => ({ ...prev, messages: [...prev.messages, sent] }))
    setDrafts((prev) => prev.filter((d) => d.id !== draftOpen.id))
    setDraftOpenId(null)
    setNotice(`Draft "${values.subject}" sent to ${values.to.join(', ') || '—'}`)
  }

  // D9/US-5: a manual compose send lands in the Sent folder.
  const handleComposeSend = (values: MailComposeValues) => {
    const recipients = values.to
      .split(',')
      .map((entry) => entry.trim())
      .filter(Boolean)
    const sent: MessageDetail = {
      id: `sent-compose-${Date.now()}`,
      folder: 'sent',
      fromName: activeMailbox?.label ?? 'Me',
      fromAddress: activeMailbox?.address ?? '',
      toNames: recipients.length > 0 ? recipients : ['(no recipient)'],
      subject: values.subject.trim() || '(no subject)',
      preview: firstLine(values.body),
      date: new Date().toISOString(),
      unread: false,
      readByAgent: false,
      hasAttachments: false,
      ccNames: [],
      bodyKind: 'text',
      bodyHtml: '',
      bodyText: values.body,
      remoteImagesBlocked: false,
      attachments: [],
    }
    setData((prev) => ({ ...prev, messages: [...prev.messages, sent] }))
    setNotice(`Message "${sent.subject}" sent to ${recipients.join(', ') || '—'}`)
  }

  return (
    <aside
      data-testid="mail-demo"
      aria-label="Mail panel"
      className="flex h-full w-full flex-col overflow-hidden border-l border-[var(--color-border)] bg-[var(--color-surface-0)]"
    >
      {/* Toolbar — MailPanel's chrome shape; the Signature entry point is the
          demo's visible way into the signature editor. */}
      <header className="flex h-chrome-header shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)]">
        <Tray size={16} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
        <h2 className="shrink-0 text-[length:var(--type-body-compact-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
          Mail
        </h2>
        <div className="min-w-0 flex-1">
          <MailboxPicker mailboxes={data.mailboxes} value={mailboxId} onChange={setMailboxId} />
        </div>
        <Button variant="outline" size="sm" onClick={() => setSignatureOpen(true)} className="shrink-0">
          Signature
        </Button>
      </header>
      {/* Last-checked line (FR-033/D32), as in MailPanel. */}
      <p className="shrink-0 border-b border-[var(--color-border)] bg-[var(--color-surface-0)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
        {activeMailbox ? `${activeMailbox.address} · ` : ''}
        Last checked {new Date(data.lastChecked).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
      </p>
      {data.connection.status !== 'ok' && data.connection.message && (
        <MailConnectionBanner
          status={data.connection.status}
          message={data.connection.message}
          nextRetryAt={data.connection.nextRetryAt}
          onRetry={() => setData((prev) => ({ ...prev, connection: { status: 'ok' } }))}
        />
      )}
      {/* Send/discard confirmation (role="status") — visible until dismissed. */}
      {notice && (
        <div
          role="status"
          data-testid="mail-demo-confirmation"
          className="flex items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-status-done-background)] px-[var(--space-2-5)] py-[var(--space-2)] text-[var(--color-status-done)]"
        >
          <CheckCircle size={16} weight="fill" aria-hidden="true" className="shrink-0" />
          <p className="min-w-0 flex-1 text-[length:var(--type-body-compact-size)]">{notice}</p>
          <IconButton aria-label="Dismiss confirmation" size="sm" variant="ghost" onClick={() => setNotice(null)}>
            <X size={12} aria-hidden="true" />
          </IconButton>
        </div>
      )}
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <MailFolderRail folders={folders} active={folder} onFolderChange={handleFolderChange} />
        <MailMessageList
          folderLabel={data.folders.find((f) => f.key === folder)?.label ?? 'Mail'}
          messages={listFor(folder)}
          selectedId={draftOpen ? undefined : selectedId ?? undefined}
          onSelect={handleSelect}
          onCompose={() => setComposeOpen(true)}
          className="border-l border-[var(--color-border)]"
        />
        {draftOpen ? (
          <MailDraftPanel
            key={draftOpen.id}
            draft={draftOpen}
            onClose={() => setDraftOpenId(null)}
            onDiscard={handleDraftDiscard}
            onSend={handleDraftSend}
          />
        ) : selected ? (
          <MailReadingPane
            key={selected.id}
            message={selected}
            onReply={() => setComposeOpen(true)}
            onLoadImages={handleLoadImages}
            onClose={() => setSelectedId(null)}
          />
        ) : null}
      </div>
      <MailComposeDialog
        open={composeOpen}
        onOpenChange={setComposeOpen}
        mailbox={activeMailbox}
        onSend={handleComposeSend}
      />
      <Dialog open={signatureOpen} onOpenChange={setSignatureOpen}>
        <DialogContent className="flex max-h-[85vh] flex-col gap-[var(--space-3)] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Mailbox signature</DialogTitle>
            <DialogDescription>
              {activeMailbox
                ? `Editing the signature of ${activeMailbox.label} (${activeMailbox.address}).`
                : 'Choose a mailbox first.'}
            </DialogDescription>
          </DialogHeader>
          <MailSignatureEditor initialHtml={data.signatureHtml} maxChars={data.signatureMaxChars} />
        </DialogContent>
      </Dialog>
    </aside>
  )
}

// ---------------------------------------------------------------------------
// Sample-data helpers

/** Fresh sample data per demo mount; optional connection-error variant. */
function buildDemoData(connectionError: boolean): MailSampleData {
  const data: MailSampleData = structuredClone(sampleMailData)
  const connection: ConnectionSample = connectionError
    ? { status: 'error', message: 'Mail server unreachable — connection timed out' }
    : { status: 'ok' }
  return { ...data, connection }
}

/** One DraftSample as a message-list row in the Drafts folder. */
function toDraftRow(draft: DraftSample): MessageDetail {
  return {
    id: draft.id,
    folder: 'drafts',
    fromName: draft.origin === 'agent' ? 'Draft · created by agent' : 'Draft · from your mail program',
    fromAddress: draft.to[0] ?? '',
    toNames: draft.to,
    subject: draft.subject,
    preview: firstLine(draft.markdownBody),
    date: draftRowDate(draft),
    unread: false,
    readByAgent: false,
    hasAttachments: draft.attachments.length > 0,
    ccNames: [],
    bodyKind: 'text',
    bodyHtml: '',
    bodyText: draft.markdownBody,
    remoteImagesBlocked: false,
    attachments: draft.attachments,
  }
}

/** Drafts have no real timestamp in the sample; derive a stable demo one. */
function draftRowDate(draft: DraftSample): string {
  return draft.origin === 'agent' ? new Date('2026-09-26T09:00:00Z').toISOString() : new Date('2026-09-26T08:30:00Z').toISOString()
}

/** First non-empty line, for previews. */
function firstLine(text: string): string {
  return text.split('\n').find((line) => line.trim() !== '') ?? ''
}
