// MailPanel.tsx — the workspace Mail panel CONTENT (email-mail-view-spec.md
// §16, US-3..US-8): folder rail, message list, reading pane, compose, the
// watcher banner and per-state error surfaces. It plugs into the shared
// side-panel shell (SP-8): the shell renders it through Mail's
// PanelDefinition (mailPanelDefinition.tsx — registered in the production
// registry at wave 2, §10) — Mail is panel CONTENT, never a shell edit.
//
// Oracle contract (MailPanel.states.test.tsx): props { workspaceId,
// mailboxId? }; reads
// fetchMailboxes from '@/lib/api' and the mail-only operations from
// '@/lib/api/mail'; folders refetch every 30s while mounted, never after unmount
// (D25); error shows the error CLASS (e.g. connect_refused) + Retry, never
// the empty-state text; the US-6 read-by-agent tag appears exactly once per
// flagged message; watcher backoff renders "Retrying at …" (D29/R2-8).
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import { isApiError } from '@/lib/api-error'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  fetchMailboxes,
  fetchAgents,
} from '@/lib/api'
import {
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  markMailSeen,
  fetchMailSummary,
  sendMailMessage,
  saveMailDraft,
  sendMailDraft,
  discardMailDraft,
  mintMailHtmlPreviewToken,
  fetchMailAttachment,
  mailUidRef,
} from '@/lib/api/mail'
import type {
  MailboxNewMailSummary,
  MailMessage,
  MailMessageSummary,
} from '@/lib/api'
import { MailFolderRail } from './MailFolderRail'
import { MailMessageList } from './MailMessageList'
import { MailPreviewPane, type MailPreviewPaneProps } from './MailPreviewPane'
import { MailHtmlFrame } from './MailHtmlFrame'
import { MailComposeDialog } from './MailComposeDialog'
import type { MailComposeBody } from './MailComposeDialog'
import { readMailPanelIntent, writeMailPanelIntent } from './mailPanelIntent'
import type { MailPanelIntent } from './mailPanelIntent'
import { formatMailDate, formatMailTime, formatMailBytes } from './mail-format'
import { ListPreviewLayout, ListPreviewRegion } from '@/components/panel-shell/ListPreviewLayout'
import type { ListPreviewLayoutMode } from '@/components/panel-shell/ListPreviewLayout'
import {
  getMailDiscardConfirmDialogOpen,
  mailDiscardConfirmDialogHostUnmounted,
  resolveMailDiscardConfirmDialog,
  subscribeMailDiscardConfirmDialog,
} from './mailUnsavedGuard'

/** Folders refetch cadence — D25: while mounted only (refetchInterval is
 * observer-bound, so unmount stops it). */
const FOLDERS_REFETCH_MS = 30_000
/** The watcher banner refreshes with the folders cadence. */
const SUMMARY_REFETCH_MS = 30_000

/** Extract the error CLASS for display (US-3 AS-4): ApiError.code when
 * present, else the message. Never a generic string alone — the class IS
 * the diagnosis. */
function mailErrorCode(err: unknown): string {
  if (err instanceof Error && 'code' in err && typeof (err as { code?: unknown }).code === 'string') {
    return (err as { code: string }).code
  }
  if (err instanceof Error && err.message) return err.message
  return 'unknown_error'
}

function isMailConnectionFailure(errorClass: string | null): boolean {
  return errorClass === 'connect_refused' || errorClass === 'timeout' || errorClass === 'dns' || errorClass === 'tls'
}

/** IMAP envelopes can omit the angle brackets that the mid: route requires. */
function draftMidRef(messageId: string | null): string | null {
  if (messageId === null) return null
  const value = messageId.trim()
  const inner = value.startsWith('<') && value.endsWith('>') ? value.slice(1, -1) : value
  if (!inner.includes('@') || /[<>\r\n]/.test(inner) || inner.trim() !== inner || `<${inner}>`.length > 998) return null
  return `mid:<${inner}>`
}

/** File → base64 (MC-32 attach path) — chunked btoa to dodge call-stack
 * limits on large files. */
function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result
      if (typeof result !== 'string') {
        reject(new Error('Could not read attachment'))
        return
      }
      const base64 = result.slice(result.indexOf(',') + 1)
      resolve(base64)
    }
    reader.onerror = () => reject(new Error('Could not read attachment'))
    reader.readAsDataURL(file)
  })
}

export interface MailPanelProps {
  workspaceId: string
  /**
   * The shell's mailbox directive (SP-23, via mailPanelDefinition ← panel
   * context): string = land on that mailbox (`?panel=mail&agent=…`);
   * null = EXPLICIT choose-a-mailbox — the deep link named `panel=mail` with
   * no agent, so no mailbox is auto-selected and Mail starts nothing costly
   * (§8.2 Mail bullet, §12 row 8); undefined = no directive (tab-strip
   * toggle, plain opens) — the panel keeps its own posture: a valid session
   * intent wins, else a single configured mailbox opens live (US-3 AS-1),
   * else the picker faces the user (US-3 AS-3).
   */
  mailboxId?: string | null
  /** Full-page routes use split; the docked panel defaults to Library's stacked layout. */
  layout?: ListPreviewLayoutMode
  /** URL-provided initial folder for the full-page route. */
  initialFolder?: string
  /** URL-provided initial message for the full-page route. */
  initialMessageRef?: string
  /** Reports the live address so the shell's Expand action carries it over. */
  onLocationChange?: (location: MailPanelLocation) => void
}

export interface MailPanelLocation {
  mailboxId: string | null
  folder: string
  messageRef: string | null
}

const FOLDERS_KEY = ['mail-folders'] as const
const MESSAGES_KEY = ['mail-messages'] as const
const DETAIL_KEY = ['mail-detail'] as const

export function MailPanel({ workspaceId, mailboxId, layout = 'stacked', initialFolder, initialMessageRef, onLocationChange }: MailPanelProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  // CRIT-001: the ONE discard-unsaved-edits dialog for both of Mail's
  // unsaved-text surfaces (compose, draft editor) — hosted here because
  // MailPanel stays mounted for as long as either surface could be open,
  // in both docked and full-screen presentation (mailUnsavedGuard.ts).
  const discardDialogOpen = useSyncExternalStore(subscribeMailDiscardConfirmDialog, getMailDiscardConfirmDialogOpen)
  useEffect(() => mailDiscardConfirmDialogHostUnmounted, [])

  // ── Mailbox resolution (FR-010 / SP-23) ───────────────────────────────
  const mailboxesQuery = useQuery({
    queryKey: ['mailboxes'],
    queryFn: fetchMailboxes,
    staleTime: 60_000,
  })
  const agentsQuery = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })
  // F3: Mail can be re-adopted for a DIFFERENT workspace while it stays
  // mounted (a same-page hash navigation — usePanelDeepLink re-opens the
  // panel with a new `context.workspaceId`, it does not remount this
  // component). `mailboxesQuery` is a single app-wide list with a 60s
  // `staleTime` and no per-workspace key, so without this it would keep
  // answering with whichever workspace's roster it last fetched — a
  // mailbox configured for the newly routed workspace while this panel
  // was open elsewhere would be silently missed. Treat a workspace change
  // as a fresh look and invalidate, the same way a mutation here already
  // invalidates FOLDERS_KEY/MESSAGES_KEY.
  const workspaceIdRef = useRef(workspaceId)
  useEffect(() => {
    if (workspaceIdRef.current === workspaceId) return
    workspaceIdRef.current = workspaceId
    void queryClient.invalidateQueries({ queryKey: ['mailboxes'] })
  }, [workspaceId, queryClient])
  const workspaceMailboxes = useMemo(() => {
    const list = mailboxesQuery.data
    if (!list) return []
    return list.filter((mb) => mb.enabled && mb.configured && mb.workspace_id === workspaceId)
  }, [mailboxesQuery.data, workspaceId])
  const agentNamesById = useMemo(
    () => new Map((agentsQuery.data ?? []).map((agent) => [agent.id, agent.name])),
    [agentsQuery.data],
  )

  // Per-workspace intent (FR-010): sessionStorage-backed selection.
  const [intent, setIntent] = useState<MailPanelIntent>(() => {
    const stored = readMailPanelIntent(workspaceId)
    return { ...stored, folder: initialFolder ?? stored.folder }
  })
  const agentId = useMemo(() => {
    // SP-23's explicit choose directive: the deep link named panel=mail with
    // no agent — the picker faces the user, nothing costly starts, and a
    // stored selection does not override the link's directive.
    if (mailboxId === null) return null
    if (typeof mailboxId === 'string') {
      // A named mailbox can open Compose while the list is still loading;
      // once it settles, only enabled/configured mailboxes survive the filter.
      if (mailboxesQuery.isPending && mailboxId !== '') return mailboxId
      const named = workspaceMailboxes.find((mb) => mb.agent_id === mailboxId)
      if (named !== undefined) return mailboxId
    }
    if (intent.agentId !== null) {
      const stored = workspaceMailboxes.find((mb) => mb.agent_id === intent.agentId)
      if (stored !== undefined) return intent.agentId
    }
    return workspaceMailboxes[0]?.agent_id ?? null
  }, [mailboxId, intent.agentId, mailboxesQuery.isPending, workspaceMailboxes])
  const selectedMailbox = workspaceMailboxes.find((mailbox) => mailbox.agent_id === agentId)
  const senderName = agentId === null ? undefined : agentNamesById.get(agentId) ?? agentId
  const senderAddress = selectedMailbox?.username ?? 'Address unavailable'
  const folder: string = intent.folder ?? 'inbox'
  // The open message ref — session state, not persisted (a fresh panel opens
  // with the list, not a message). Reset when folder/mailbox changes.
  // The open message ref — `uid:…` from a list click, or a deep-link ref
  // (uid:… / mid:…) consumed ONCE from the per-workspace intent at mount.
  const [selectedRef, setSelectedRef] = useState<string | null>(() =>
    initialMessageRef ?? readMailPanelIntent(workspaceId).messageRef,
  )
  // not-wire-format: remembers only the clicked row so a renumbered draft can
  // be resolved by its Message-ID without applying another workspace's row.
  const [selectedRow, setSelectedRow] = useState<{
    message: MailMessageSummary; workspaceId: string; agentId: string | null
  } | null>(null)
  const activeSelectedRow = selectedRow?.workspaceId === workspaceId
    && selectedRow.agentId === agentId
    && selectedRow.message.folder === folder
    && selectedRef === mailUidRef(selectedRow.message.uidvalidity, selectedRow.message.uid)
    ? selectedRow.message : null
  const [draftEditingRef, setDraftEditingRef] = useState<string | null>(null)

  // Persist the per-workspace intent whenever mailbox/folder selection moves
  // (FR-010). Effect-based so programmatic and click-driven changes persist.
  useEffect(() => {
    // messageRef: null — the consume-once deep-link ref was already lifted
    // into selectedRef at mount; persisting it again would re-select the same
    // message on the panel's next open (FR-010 intent must not replay).
    writeMailPanelIntent(workspaceId, { agentId, folder, messageRef: null })
  }, [workspaceId, agentId, folder])

  useEffect(() => {
    // Before the mailbox list resolves, agentId is only a loading placeholder.
    // Reporting it as null would overwrite a deep link's requested mailbox
    // while the full-screen shell registers its live context getter.
    if (!mailboxesQuery.isSuccess) return
    onLocationChange?.({ mailboxId: agentId, folder, messageRef: selectedRef })
  }, [agentId, folder, selectedRef, onLocationChange, mailboxesQuery.isSuccess])

  // ── Queries ───────────────────────────────────────────────────────────
  // Human-initiated dialing (D29/R2-9, MC-33): a human gesture that must
  // reach the IMAP connection (the banner's "Retry now", a Retry button on
  // an error surface, an attachment download) marks the affected queries'
  // NEXT fetch human — the queryFn consumes the mark once and sends the
  // contract's `retry=true` query parameter, which bypasses the mailbox
  // watcher's backoff gate for that one request. Everything else (the 30s
  // cadence, invalidate-driven refetches) fetches WITHOUT the marker — the
  // contract's automatic-poll posture (503 code=backoff while backing off).
  const foldersHumanRef = useRef(false)
  const messagesHumanRef = useRef(false)
  const detailHumanRef = useRef(false)

  const foldersQuery = useQuery({
    queryKey: [...FOLDERS_KEY, workspaceId, agentId],
    queryFn: () => {
      const retry = foldersHumanRef.current
      foldersHumanRef.current = false
      return fetchMailFolders(workspaceId, agentId as string, { retry })
    },
    enabled: agentId !== null,
    refetchInterval: FOLDERS_REFETCH_MS,
    refetchIntervalInBackground: false,
  })
  const messagesQuery = useQuery({
    queryKey: [...MESSAGES_KEY, workspaceId, agentId, folder],
    queryFn: () => {
      const retry = messagesHumanRef.current
      messagesHumanRef.current = false
      return fetchMailMessages(workspaceId, agentId as string, folder, { retry })
    },
    enabled: agentId !== null && foldersQuery.isSuccess && folder !== null,
    retry: false,
  })
  const summaryQuery = useQuery({
    queryKey: ['mail-summary', workspaceId],
    queryFn: () => fetchMailSummary(workspaceId),
    refetchInterval: SUMMARY_REFETCH_MS,
    refetchIntervalInBackground: false,
  })

  // ── Detail query + mark-seen (B-27) ──────────────────────────────────
  const detailQuery = useQuery({
    queryKey: [...DETAIL_KEY, workspaceId, agentId, folder, selectedRef],
    queryFn: async () => {
      const retry = detailHumanRef.current
      detailHumanRef.current = false
      try {
        return await fetchMailMessage(workspaceId, agentId as string, folder, selectedRef as string, { retry })
      } catch (error) {
        if (!isApiError(error) || error.status !== 404 || folder !== 'drafts' || activeSelectedRow === null) throw error
        const midRef = draftMidRef(activeSelectedRow.message_id)
        const duplicateIds = midRef !== null && (messagesQuery.data?.messages.filter((message) => draftMidRef(message.message_id) === midRef).length ?? 0) > 1
        if (midRef !== null && !duplicateIds) {
          try {
            const updated = await fetchMailMessage(workspaceId, agentId as string, folder, midRef, { retry })
            void queryClient.invalidateQueries({ queryKey: [...MESSAGES_KEY, workspaceId, agentId, folder] })
            return updated
          } catch (recoveryError) {
            if (!isApiError(recoveryError) || recoveryError.status !== 404) throw recoveryError
          }
        }
        // The old UID is gone and no surviving copy resolved. Refresh before
        // explaining that the clicked row disappeared; a failed refresh remains
        // a visible fetch error rather than a claim that the list was refreshed.
        const refreshed = await fetchMailMessages(workspaceId, agentId as string, folder)
        queryClient.setQueryData([...MESSAGES_KEY, workspaceId, agentId, folder], refreshed)
        throw new Error('This draft was changed or deleted elsewhere. The list has been refreshed.', { cause: error })
      }
    },
    enabled: agentId !== null && folder !== null && selectedRef !== null,
    retry: false,
  })
  const detail: MailMessage | null = detailQuery.data ?? null
  useEffect(() => {
    if (!detailQuery.isSuccess || detail === null || selectedRef === null || activeSelectedRow === null) return
    const resolvedRef = mailUidRef(detail.uidvalidity, detail.uid)
    const rowMidRef = draftMidRef(activeSelectedRow.message_id)
    if (resolvedRef === selectedRef || rowMidRef === null || draftMidRef(detail.message_id) !== rowMidRef) return
    queryClient.setQueryData([...DETAIL_KEY, workspaceId, agentId, folder, resolvedRef], detail)
    setSelectedRow({ message: detail, workspaceId, agentId })
    setSelectedRef(resolvedRef)
  }, [detailQuery.isSuccess, detail, selectedRef, activeSelectedRow, queryClient, workspaceId, agentId, folder])
  const headerDate = detail?.date && new Date(detail.date).getUTCFullYear() > 1
    ? formatMailDate(detail.date)
    : ''
  const compactDraftList = layout === 'stacked' && detail?.is_draft === true
    && draftEditingRef === `${agentId}:${folder}:${selectedRef}`
  const seenMutation = useMutation({
    mutationFn: (ref: string) => markMailSeen(workspaceId, agentId as string, folder, ref),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: FOLDERS_KEY })
      void queryClient.invalidateQueries({ queryKey: MESSAGES_KEY })
    },
  })

  // ── Draft actions (US-7, D12/D23) ────────────────────────────────────
  const draftSave = useMutation({
    mutationFn: async (next: Parameters<MailPreviewPaneProps['onSave']>[0]) => {
      const d = detail as MailMessage
      const newAttachments = await Promise.all((next.attachments ?? []).map(async (file) => ({
        filename: file.name,
        content_type: file.type || 'application/octet-stream',
        data_base64: await fileToBase64(file),
      })))
      return saveMailDraft(workspaceId, agentId as string, selectedRef as string, {
        to: next.to.split(',').map((s) => s.trim()).filter(Boolean),
        cc: next.cc ?? d.cc,
        bcc: next.bcc ?? d.bcc ?? [],
        subject: next.subject,
        body_markdown: next.bodyMarkdown,
        uidvalidity: d.uidvalidity,
        uid: d.uid,
        attachments: newAttachments.length > 0 ? newAttachments : undefined,
        keep_attachment_parts: next.keepAttachmentParts ?? d.attachments.map((item) => item.part_index),
      })
    },
    onSuccess: (updated) => {
      const updatedRef = mailUidRef(updated.uidvalidity, updated.uid)
      const updatedDetailKey = [...DETAIL_KEY, workspaceId, agentId, 'drafts', updatedRef]
      queryClient.setQueryData(updatedDetailKey, updated)
      void queryClient.invalidateQueries({ queryKey: updatedDetailKey })
      setSelectedRow({ message: updated, workspaceId, agentId })
      setSelectedRef(updatedRef)
      void queryClient.invalidateQueries({ queryKey: FOLDERS_KEY })
      void queryClient.invalidateQueries({ queryKey: MESSAGES_KEY })
      addToast({ message: updated.draft_cleanup_warning ?? 'Draft saved', variant: updated.draft_cleanup_warning ? 'warning' : 'success' })
    },
    onError: (err) => addToast({ message: mailErrorCode(err), variant: 'error' })
  })

  const draftSend = useMutation({
    mutationFn: () => {
      const d = detail as MailMessage
      return sendMailDraft(workspaceId, agentId as string, selectedRef as string, {
        to: d.to,
        cc: d.cc,
        bcc: d.bcc ?? [],
        subject: d.subject,
        body_markdown: d.body_markdown ?? '',
        uidvalidity: d.uidvalidity,
        uid: d.uid,
        keep_attachment_parts: d.attachments.map((item) => item.part_index),
      })
    },
    onSuccess: (res) => {
      setSelectedRow(null)
      setSelectedRef(null)
      void queryClient.invalidateQueries({ queryKey: FOLDERS_KEY })
      void queryClient.invalidateQueries({ queryKey: MESSAGES_KEY })
      addToast({
        message: res.draft_cleanup_warning ?? 'Draft sent',
        variant: res.draft_cleanup_warning ? 'warning' : 'success',
      })
    },
    onError: (err) => addToast({ message: mailErrorCode(err), variant: 'error' })
  })

  const draftDiscard = useMutation({
    mutationFn: () => discardMailDraft(workspaceId, agentId as string, selectedRef as string),
    onSuccess: () => {
      setSelectedRow(null)
      setSelectedRef(null)
      void queryClient.invalidateQueries({ queryKey: MESSAGES_KEY })
      void queryClient.invalidateQueries({ queryKey: FOLDERS_KEY })
      addToast({ message: 'Draft discarded', variant: 'success' })
    },
    onError: (err) => addToast({ message: mailErrorCode(err), variant: 'error' })
  })

  // Mark seen on open (B-27, US-6): after a successful detail fetch of an
  // unseen message outside Drafts. Invalidates folders + list (unread count
  // drops) via the seen mutation's onSuccess.
  useEffect(() => {
    if (detailQuery.isSuccess && detail !== null && detail.seen === false && folder !== 'drafts' && selectedRef !== null && !seenMutation.isPending) {
      seenMutation.mutate(selectedRef)
    }
  }, [detailQuery.isSuccess, detail?.seen, folder, selectedRef])

  // Compose dialog state: null = closed. reply carries the open message's
  // identity for In-Reply-To (US-5 AS-3).
  const [compose, setCompose] = useState<{ mode: 'new' | 'reply' } | null>(null)

  // ── Compose send (US-5) ───────────────────────────────────────────────
  const composeSend = useMutation({
    mutationFn: async (body: MailComposeBody) => {
      const attachments = await Promise.all(
        body.attachments.map(async (file) => ({
          filename: file.name,
          content_type: file.type || 'application/octet-stream',
          data_base64: await fileToBase64(file),
        })),
      )
      return sendMailMessage(workspaceId, agentId as string, {
        to: body.to,
        cc: body.cc.length > 0 ? body.cc : undefined,
        bcc: body.bcc.length > 0 ? body.bcc : undefined,
        subject: body.subject,
        body_markdown: body.body_markdown,
        in_reply_to: body.in_reply_to,
        attachments: attachments.length > 0 ? attachments : undefined,
      })
    },
    onSuccess: (res) => {
      void queryClient.invalidateQueries({ queryKey: FOLDERS_KEY })
      void queryClient.invalidateQueries({ queryKey: MESSAGES_KEY })
      addToast({
        message: res.save_warning ?? 'Message sent',
        variant: res.save_warning ? 'warning' : 'success',
      })
      // F11: only a confirmed send closes Compose. A failed send (onError,
      // below) must leave the dialog — and everything the user typed — in
      // place instead of discarding it on an optimistic close.
      setCompose(null)
    },
    onError: (err) => addToast({ message: mailErrorCode(err), variant: 'error' })
  })

  // ── HTML preview (D13/D17): token minted per body, re-minted on Load
  // images (load_remote: true). Never a local boolean — the parent owns the
  // re-mint, so remote content stays server-gated.
  const [loadRemote, setLoadRemote] = useState(false)
  useEffect(() => { setLoadRemote(false) }, [selectedRef])
  const htmlTokenQuery = useQuery({
    queryKey: ['mail-html-token', workspaceId, agentId, folder, selectedRef, loadRemote],
    queryFn: () => mintMailHtmlPreviewToken({
      workspace_id: workspaceId,
      agent_id: agentId as string,
      // The folder rail only offers the contract's three folders
      // (MailFolderRail), so the narrowing is guaranteed by construction.
      folder: folder as 'inbox' | 'sent' | 'drafts',
      message_ref: selectedRef as string,
      load_remote: loadRemote,
    }),
    enabled: detail?.has_html === true,
    staleTime: 0,
    gcTime: 0,
  })

  // ── Watcher banner data (D29/R2-8) ───────────────────────────────────
  const watcherItem: MailboxNewMailSummary | null = useMemo(() => {
    const items = summaryQuery.data?.items ?? []
    if (agentId !== null) {
      const mine = items.find((item) => item.agent_id === agentId)
      if (mine !== undefined) return mine
    }
    return items.length > 0 ? (items[0] as MailboxNewMailSummary) : null
  }, [summaryQuery.data, agentId])

  /** Retry now (D29/R2-9, MC-33): a human gesture that must actually DIAL —
   * marks the dialing queries' next fetches human (`retry=true`, which
   * bypasses the watcher's backoff gate) and refetches them, instead of the
   * old invalidate-only path whose follow-up fetches arrived WITHOUT the
   * marker and were refused with 503 code=backoff while backing off. The
   * summary GET is not a dialing route (it reads saved watcher state, never
   * dials IMAP — D29/R2-5), so it just refetches. Guards mirror each
   * query's `enabled` — refetch() forces a fetch even for a disabled query.
   */
  const refreshWatcher = () => {
    if (agentId !== null) {
      foldersHumanRef.current = true
      void foldersQuery.refetch()
      if (foldersQuery.isSuccess) {
        messagesHumanRef.current = true
        void messagesQuery.refetch()
      }
      if (selectedRef !== null) {
        detailHumanRef.current = true
        void detailQuery.refetch()
      }
    }
    void summaryQuery.refetch()
  }

  const replyTarget = detail === null ? null : { from: detail.from ?? '', subject: detail.subject ?? '', messageId: detail.message_id ?? '' }
  const folderErrorCode = foldersQuery.isError ? mailErrorCode(foldersQuery.error) : null
  // A 503 backoff is a retry posture, not the IMAP cause. Use the selected
  // mailbox's saved watcher class when it is available; never borrow another mailbox's class.
  const folderErrorClass = folderErrorCode === 'backoff' && watcherItem?.agent_id === agentId
    ? watcherItem.last_error_class ?? folderErrorCode
    : folderErrorCode

  return (
    <div data-testid="mail-panel" className="flex h-full min-h-0 w-full flex-col bg-[var(--color-surface-0)]">
      <div className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2)]">
        <Select
          value={agentId ?? ''}
          onValueChange={(next) => { setIntent((prev) => ({ ...prev, agentId: next })); setSelectedRow(null); setSelectedRef(null) }}
        >
          <SelectTrigger
            aria-label="Mailbox"
            className="h-8 w-[220px] shrink-0 text-[length:var(--type-body-compact-size)]"
          >
            <SelectValue
              placeholder={
                mailboxId === null || !mailboxesQuery.isLoading
                  ? 'Choose a mailbox'
                  : 'Loading mailboxes…'
              }
            />
          </SelectTrigger>
          <SelectContent>
            {workspaceMailboxes.map((mb) => (
              <SelectItem key={mb.agent_id} value={mb.agent_id}>
                {agentNamesById.get(mb.agent_id) ?? mb.agent_id}
                {mb.username === undefined ? '' : ` · ${mb.username}`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <div className="min-w-0 flex-1" />
        <Button
          size="sm"
          className="gap-[var(--space-1)]"
          disabled={agentId === null}
          aria-describedby={workspaceMailboxes.length === 0 && mailboxesQuery.isSuccess ? 'mail-no-mailbox-help' : undefined}
          onClick={() => setCompose({ mode: 'new' })}
        >
          Compose
        </Button>
      </div>
      {watcherItem?.watcher_state === 'backoff' && (
        <div
          role="status"
          data-testid="mail-connection-banner"
          className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] px-[var(--space-3)] py-[var(--space-2)]"
        >
          <p className="min-w-0 flex-1 text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
            {watcherItem.last_error_class === null
              ? 'Mail watcher is retrying'
              : isMailConnectionFailure(watcherItem.last_error_class)
                ? `Can't connect to this mailbox · ${watcherItem.last_error_class}`
                : `Mail watcher error: ${watcherItem.last_error_class}`}
            {watcherItem.next_attempt_at !== null && (
              <> — retrying at {formatMailTime(watcherItem.next_attempt_at)}</>
            )}
          </p>
          <Button variant="outline" size="sm" className="shrink-0" onClick={refreshWatcher}>
            Retry now
          </Button>
        </div>
      )}
      {mailboxesQuery.isError && (
        <div role="alert" className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[color-mix(in_srgb,var(--color-error)_10%,transparent)] px-[var(--space-3)] py-[var(--space-2)]">
          <p className="min-w-0 flex-1 text-[length:var(--type-caption-size)] text-[var(--color-error)]">
            {mailErrorCode(mailboxesQuery.error)}
          </p>
          <Button variant="outline" size="sm" className="shrink-0" onClick={() => void mailboxesQuery.refetch()}>
            Retry
          </Button>
        </div>
      )}
      {workspaceMailboxes.length === 0 && !mailboxesQuery.isError && !mailboxesQuery.isLoading && (
        <div
          data-testid="mail-choose-mailbox"
          className="flex min-h-0 flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-5)] text-center"
        >
          <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
            No mailbox is configured for this workspace yet.
          </p>
          <p id="mail-no-mailbox-help" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
            On the Connectors screen, in Email choose Add mailbox to use Mail here.
          </p>
          <Button asChild size="sm">
            <a href="#/connectors">Connect mailbox</a>
          </Button>
        </div>
      )}
      {agentId !== null && (
        <ListPreviewLayout layout={layout} testId="mail-list-preview-layout">
          <ListPreviewRegion
            layout={layout}
            region="list"
            previewVisible
            compactList={compactDraftList}
            surface="mail-list"
            testId="mail-list-region"
          >
            <MailFolderRail
              folders={foldersQuery.data?.folders ?? []}
              active={folder}
              onFolderChange={(slug) => { setIntent((prev) => ({ ...prev, folder: slug })); setSelectedRow(null); setSelectedRef(null) }}
            />
            <div className="flex min-h-0 min-w-0 flex-1 flex-col" data-testid="mail-list-zone">
            {foldersQuery.isError ? (
              <div
                role="alert"
                data-testid="mail-folders-error"
                className="flex flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-4)]"
              >
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
                  {isMailConnectionFailure(folderErrorClass) ? "Can't connect to this mailbox" : 'Could not load this mailbox'}
                </p>
                <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                  Error class: {folderErrorClass}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    foldersHumanRef.current = true
                    void foldersQuery.refetch()
                  }}
                >
                  Retry
                </Button>
              </div>
            ) : (
              <>
                {messagesQuery.isPending && (
                  <div className="flex-1 p-[var(--space-3)]" data-testid="mail-list-loading">
                    <ListSkeleton />
                  </div>
                )}
                {messagesQuery.isError && (
                  <div
                    role="alert"
                    data-testid="mail-messages-error"
                    className="flex flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-4)]"
                  >
                    <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
                      {mailErrorCode(messagesQuery.error)}
                    </p>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        messagesHumanRef.current = true
                        void messagesQuery.refetch()
                      }}
                    >
                      Retry
                    </Button>
                  </div>
                )}
                {messagesQuery.isSuccess && (
                  <MailMessageList
                    messages={messagesQuery.data.messages}
                    selectedRef={selectedRef}
                    onSelect={(message) => {
                      setSelectedRow({ message, workspaceId, agentId })
                      setSelectedRef(mailUidRef(message.uidvalidity, message.uid))
                    }}
                  />
                )}
              </>
            )}
            </div>
          </ListPreviewRegion>
          <ListPreviewRegion
            layout={layout}
            region="preview"
            previewVisible
            compactList={compactDraftList}
            surface="mail-preview"
            testId="mail-reading-zone"
          >
            {selectedRef === null && (
              <div className="flex flex-1 items-center justify-center p-[var(--space-5)]">
                <p className="text-center text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
                  Select a message to read
                </p>
              </div>
            )}
            {selectedRef !== null && detailQuery.isPending && (
              <div className="flex flex-1 items-center justify-center p-[var(--space-5)]">
                <div className="h-6 w-6 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin" aria-label="Loading message" />
              </div>
            )}
            {selectedRef !== null && detailQuery.isError && (
              <div className="flex flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-4)]">
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
                  {mailErrorCode(detailQuery.error)}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    detailHumanRef.current = true
                    void detailQuery.refetch()
                  }}
                >
                  Retry
                </Button>
              </div>
            )}
            {detail !== null && detailQuery.isSuccess && (
              <div className="flex min-h-0 flex-1 flex-col">
                <div className="flex shrink-0 items-start gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2)]">
                  {detail.is_draft !== true && folder !== 'sent' ? (
                    <div className="min-w-0 flex-1">
                      <h3 className="break-words text-[length:var(--type-section-title-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
                        {detail.subject || '(No subject)'}
                      </h3>
                      <p className="mt-[var(--space-1)] break-words text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                        From: {detail.from_name ? `${detail.from_name} · ` : ''}{detail.from || 'unknown'}
                      </p>
                      <p className="break-words text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                        To: {detail.to.length > 0 ? detail.to.join(', ') : 'No recipient'}
                      </p>
                      {detail.cc.length > 0 && (
                        <p className="break-words text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                          Cc: {detail.cc.join(', ')}
                        </p>
                      )}
                      {formatMailDate(detail.date) && (
                        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                          Date: {formatMailDate(detail.date)}
                        </p>
                      )}
                    </div>
                  ) : (
                    <p className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                      From {detail.from ?? 'unknown'}{headerDate ? ` · ${headerDate}` : ''}
                    </p>
                  )}
                  {folder !== 'drafts' && (
                    <Button
                      variant="secondary"
                      size="sm"
                      className="shrink-0"
                      onClick={() => setCompose({ mode: 'reply' })}
                    >
                      Reply
                    </Button>
                  )}
                </div>
                {detail.is_draft === true ? (
                  <MailPreviewPane
                    state={detail.is_omnipus_draft === false ? 'foreign' : 'draft'}
                    subject={detail.subject ?? ''}
                    bodyMarkdown={detail.body_markdown ?? detail.body_text ?? ''}
                    to={detail.to.join(', ')}
                    cc={detail.cc}
                    bcc={detail.bcc}
                    attachments={detail.attachments}
                    senderName={senderName}
                    senderAddress={senderAddress}
                    signatureHtml={selectedMailbox?.signature_html}
                    sentOn={folder === 'sent' ? formatMailDate(detail.date) : undefined}
                    onSave={(next) => draftSave.mutateAsync(next).then(() => true, () => false)}
                    onEditingChange={(editing) => setDraftEditingRef(editing ? `${agentId}:${folder}:${selectedRef}` : null)}
                    onSend={() => draftSend.mutate()}
                    onDiscard={() => draftDiscard.mutate()}
                  />
                ) : folder === 'sent' ? (
                  <MailPreviewPane
                    state="sent"
                    subject={detail.subject ?? ''}
                    bodyMarkdown={detail.body_markdown ?? detail.body_text ?? ''}
                    to={detail.to.join(', ')}
                    sentOn={formatMailDate(detail.date)}
                    onSave={() => undefined}
                    onSend={() => undefined}
                    onDiscard={() => undefined}
                  />
                ) : (
                  <div className="min-h-0 flex-1 overflow-y-auto">
                    {detail.has_html === true && htmlTokenQuery.data !== undefined ? (
                      <MailHtmlFrame
                        tokenUrl={`/mail-preview/html/${htmlTokenQuery.data.token}`}
                        onLoadImages={() => setLoadRemote(true)}
                        showLoadImages={!loadRemote}
                        title="Mail body"
                      />
                    ) : (
                      <pre className="whitespace-pre-wrap p-[var(--space-3)] text-[length:var(--type-body-size)] text-[var(--color-secondary)]">
                        {detail.body_markdown ?? detail.body_text ?? ''}
                      </pre>
                    )}
                    {detail.attachments.length > 0 && (
                      <div className="border-t border-[var(--color-border)] p-[var(--space-3)]">
                        <AttachmentList
                          workspaceId={workspaceId}
                          agentId={agentId as string}
                          folder={folder}
                          messageRef={selectedRef as string}
                          attachments={detail.attachments}
                        />
                      </div>
                    )}
                  </div>
                )}
              </div>
            )}
          </ListPreviewRegion>
        </ListPreviewLayout>
      )}
      <MailComposeDialog
        open={compose !== null}
        mode={compose?.mode ?? 'new'}
        replyTo={compose?.mode === 'reply' && replyTarget !== null ? replyTarget : undefined}
        senderName={senderName}
        senderAddress={senderAddress}
        signatureHtml={selectedMailbox?.signature_html}
        onSend={(body) => {
          composeSend.mutate(body)
        }}
        onClose={() => setCompose(null)}
      />
      <ConfirmDialog
        open={discardDialogOpen}
        onOpenChange={(next) => {
          if (!next) resolveMailDiscardConfirmDialog(false)
        }}
        title="Discard unsaved changes?"
        description="You have unsaved changes in Mail. Leaving now will discard them. Continue?"
        confirmLabel="Discard"
        destructive
        onConfirm={() => resolveMailDiscardConfirmDialog(true)}
      />
    </div>
  )
}

/** Attachments of an open message (D28): filename, size, download via the
 * part_index-addressed endpoint (stable part references, never positions). */
function AttachmentList({ workspaceId, agentId, folder, messageRef, attachments }: {
  workspaceId: string
  agentId: string
  folder: string
  messageRef: string
  attachments: MailMessage['attachments']
}) {
  const addToast = useUiStore((s) => s.addToast)

  return (
    <ul aria-label="Attachments" className="flex flex-col gap-[var(--space-1)]">
      {attachments.map((attachment) => (
        <li key={attachment.part_index} className="flex items-center gap-[var(--space-2)]">
          <span className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
            {attachment.filename}
          </span>
          <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
            {formatMailBytes(attachment.size_bytes)}
          </span>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              downloadMailAttachment({ workspaceId, agentId, folder, messageRef, partIndex: attachment.part_index, filename: attachment.filename, retry: true }).catch(
                (err: unknown) => addToast({ message: mailErrorCode(err), variant: 'error' }),
              )
            }}
          >
            Download
          </Button>
        </li>
      ))}
    </ul>
  )
}

/** Download one attachment: fetch the blob (part_index-addressed), save via
 * a temporary object URL. `retry: true` — a download is human-initiated
 * (D29/R2-9): it must dial even while the watcher is backing off. */
async function downloadMailAttachment(args: {
  workspaceId: string
  agentId: string
  folder: string
  messageRef: string
  partIndex: number
  filename: string
  retry?: boolean
}): Promise<void> {
  const blob = await fetchMailAttachment(args.workspaceId, args.agentId, args.folder, args.messageRef, args.partIndex, { retry: args.retry })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = args.filename
  anchor.click()
  URL.revokeObjectURL(url)
}

/** Three shimmer rows for the list-loading state. */
function ListSkeleton() {
  return (
    <div aria-hidden="true" className="flex flex-col gap-[var(--space-2)]">
      {[0, 1, 2].map((row) => (
        <Skeleton key={row} className="h-12" />
      ))}
    </div>
  )
}
