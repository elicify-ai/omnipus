// MailPanel.tsx — the workspace Mail panel CONTENT (email-mail-view-spec.md
// §16 + the W3 live-access spec): folder rail, message list, reading pane,
// compose, the watcher banner and per-state error surfaces — now cache-first
// (US-1), honestly labelled (US-2), paged to the 200-row ceiling with a
// reachable search path (US-3), presence-reporting (US-5) and attachment-
// handing-off to the Library viewer (US-6/US-7).
//
// Event model (FR-W3-2, founder Q-C — no repeating panel timer exists):
// every eligible event (panel open with a mailbox resolved, folder switch,
// manual Refresh, the panel's own successful action) runs ONE cache-first
// read plus AT MOST ONE mode=live refresh, gated by the panel-local
// absent-or-older-than-five-minutes rule (mailCacheView.ts); manual Refresh
// and own actions always refresh. The watcher banner's 30-second saved-state
// summary poll is the ONLY cadence and never dials mail (FR-W3-3).
//
// It plugs into the shared side-panel shell (SP-8) through Mail's
// PanelDefinition (mailPanelDefinition.tsx) — Mail is panel CONTENT, never
// a shell edit.
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import { useConnectionStore } from '@/store/connection'
import { isApiError } from '@/lib/api-error'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
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
  fetchMailReplyContext,
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
  MailMessagePage,
  MailMessageSummary,
} from '@/lib/api'
import type { MailUnavailableError } from '@/lib/api/generated/openapi-types'
import { MailFolderRail } from './MailFolderRail'
import { MailMessageList } from './MailMessageList'
import { MailPreviewPane, type MailPreviewPaneProps } from './MailPreviewPane'
import { MailHtmlFrame } from './MailHtmlFrame'
import { MailComposeDialog } from './MailComposeDialog'
import type { MailComposeBody } from './MailComposeDialog'
import { readMailPanelIntent, writeMailPanelIntent } from './mailPanelIntent'
import type { MailPanelIntent } from './mailPanelIntent'
import {
  formatMailDate,
  formatMailTime,
  formatMailBytes,
  formatMailFreshnessLine,
  formatMailRefreshFailedLine,
  hasMailDate,
} from './mail-format'
import {
  createMailFolderView,
  mailViewMetaFrom,
  beginCacheRead,
  onCacheReadSettled,
  onLiveSettled,
  onLiveFailed,
  markLocalMutation,
  MAIL_UNKNOWN_PROVENANCE_META,
  type MailCacheEvent,
  type MailFolderViewState,
} from './mailCacheView'
import { createMailPanelPresence, type MailPanelPresence } from './mailPanelPresence'
import {
  openMailAttachment,
  createMailAttachmentSaveController,
  announceMailHandoff,
  openingAnnouncement,
  busyCopy,
  isMailAttachmentOverCap,
  type MailOpenAttachmentOutcome,
  type MailReturnFocus,
} from './mailAttachmentHandoff'
import { ListPreviewLayout, ListPreviewRegion } from '@/components/panel-shell/ListPreviewLayout'
import type { ListPreviewLayoutMode } from '@/components/panel-shell/ListPreviewLayout'
import {
  getMailDiscardConfirmDialogOpen,
  mailDiscardConfirmDialogHostUnmounted,
  resolveMailDiscardConfirmDialog,
  subscribeMailDiscardConfirmDialog,
} from './mailUnsavedGuard'

/** The watcher banner refreshes with its 30-second saved-state cadence —
 * the panel's ONLY repeating timer; it reads saved watcher state and never
 * dials mail (ADR P1.4 Refresh boundary, FR-W3-3). */
const SUMMARY_REFETCH_MS = 30_000
/** The panel always sends the page size explicitly — never the server
 * default (the landed `limit` prose still reads default-20 pending its
 * amendment; contracts-wave-check F4). */
const MAIL_PAGE_SIZE = 25

/** S-11's pinned text — the reading-pane stale-message state and the
 * stale-draft recovery throw (§11; qa-lead re-pins the legacy staleDraft
 * oracles per §8.8). */
const S_MESSAGE_CHANGED = 'This message changed or was deleted. Refresh the list.'

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

/** Read the typed 503 MailUnavailableError reason off a failed request so
 * the busy copy can name the cause (S-9, US-10 AS-3). */
function busyReasonFromError(err: unknown): MailUnavailableError['reason'] | null {
  if (!isApiError(err) || err.status !== 503 || typeof err.body !== 'string') return null
  try {
    const parsed: unknown = JSON.parse(err.body)
    if (parsed === null || typeof parsed !== 'object') return null
    return (parsed as { reason?: MailUnavailableError['reason'] }).reason ?? null
  } catch {
    return null
  }
}

/** The failure surface's headline: busy refusals name their cause (S-9);
 * connection failures keep the existing shape (S-10). */
function mailFailureHeadline(err: unknown): string {
  const busy = busyReasonFromError(err)
  if (busy !== null) return busyCopy(busy)
  const errorClass = mailErrorCode(err)
  return isMailConnectionFailure(errorClass) ? "Can't connect to this mailbox" : `Could not load (${errorClass})`
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

type MailReadMode = 'cache_first' | 'live'

/** Accumulated paging state beyond page 1 (US-3): each Load more appends
 * one 25-row page; the LAST page's has_more/view_limit_reached decide the
 * footer. Released on view exit (US-3 AS-7). */
interface MailLoadedPages {
  pages: MailMessagePage[]
  loadingMore: boolean
  loadMoreError: string | null
}

const EMPTY_LOADED_PAGES: MailLoadedPages = { pages: [], loadingMore: false, loadMoreError: null }

/** The folder-scoped search view (US-3 AS-4): replaces the browse list,
 * runs LIVE (never cache-served), same 25/200 discipline, "Back to <folder>"
 * restores the browse view with its rows intact (Q3). */
interface MailSearchState {
  query: string
  rows: MailMessageSummary[]
  nextCursor: string | null
  hasMore: boolean
  viewLimitReached: boolean
  loading: boolean
  loadingMore: boolean
  error: string | null
}

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
  // answering with whichever workspace's roster it last fetched. Treat a
  // workspace change as a fresh look and invalidate.
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
  // A bare link ignores saved intent until a picker gesture in this context.
  // Scope that acknowledgement so re-adopting Mail cannot dial before reset.
  const [mailboxChoice, setMailboxChoice] = useState<{
    workspaceId: string; directive: MailPanelProps['mailboxId']
  } | null>(null)
  useEffect(() => { setMailboxChoice(null) }, [workspaceId, mailboxId])
  const hasMailboxChoice = mailboxChoice !== null
    && mailboxChoice.workspaceId === workspaceId && mailboxChoice.directive === mailboxId
  const agentId = useMemo(() => {
    // SP-23's explicit choose directive: the deep link named panel=mail with
    // no agent — the picker faces the user, nothing costly starts.
    if (mailboxId === null && !hasMailboxChoice) return null
    if (typeof mailboxId === 'string') {
      if (mailboxesQuery.isPending && mailboxId !== '') return mailboxId
      const named = workspaceMailboxes.find((mb) => mb.agent_id === mailboxId)
      if (named !== undefined) return mailboxId
    }
    if (intent.agentId !== null) {
      const stored = workspaceMailboxes.find((mb) => mb.agent_id === intent.agentId)
      if (stored !== undefined) return intent.agentId
    }
    return workspaceMailboxes[0]?.agent_id ?? null
  }, [mailboxId, hasMailboxChoice, intent.agentId, mailboxesQuery.isPending, workspaceMailboxes])
  const selectedMailbox = workspaceMailboxes.find((mailbox) => mailbox.agent_id === agentId)
  const senderName = agentId === null ? undefined : agentNamesById.get(agentId) ?? agentId
  const senderAddress = selectedMailbox?.username ?? 'Address unavailable'
  const folder: string = intent.folder ?? 'inbox'
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
  // (FR-010). messageRef: null — the consume-once deep-link ref was already
  // lifted into selectedRef at mount (intent must not replay).
  useEffect(() => {
    writeMailPanelIntent(workspaceId, { agentId, folder, messageRef: null })
  }, [workspaceId, agentId, folder])

  useEffect(() => {
    if (!mailboxesQuery.isSuccess) return
    onLocationChange?.({ mailboxId: agentId, folder, messageRef: selectedRef })
  }, [agentId, folder, selectedRef, onLocationChange, mailboxesQuery.isSuccess])

  // ── Panel presence (US-5, FR-W3-12) ───────────────────────────────────
  // The adapter rides the shared authenticated socket (useConnectionStore);
  // mount = visible (the shell unmounts panel content on close), unmount =
  // close, workspace change while mounted = close old + open new. Every
  // send is best-effort; reads never depend on presence (US-5 AS-7).
  const presence = useMemo<MailPanelPresence>(() => createMailPanelPresence({
    // CONTRACT GAP (see mailPanelPresence.ts): until W0's regeneration adds
    // the send operation, ClientFrame does not carry the observer frame, so
    // the live connection is wrapped at this ONE documented site — the
    // runtime send is a JSON.stringify of the frame. This wrapper (and the
    // seam's generated-frame typing) collapses back to a plain `conn` when
    // the union carries the frame.
    getSender: () => {
      const conn = useConnectionStore.getState().connection
      if (conn === null) return null
      return {
        // The double cast is the contract gap made explicit: the generated
        // union does not yet carry the frame, so no typed overlap exists.
        // Single site; removed when W0's regeneration lands.
        send: (frame) => conn.send(frame as unknown as Parameters<typeof conn.send>[0]),
      }
    },
    isConnected: () => useConnectionStore.getState().isConnected,
    subscribeConnection: (listener) => useConnectionStore.subscribe(listener),
  }), [])
  const presenceRef = useRef(presence)
  presenceRef.current = presence
  useEffect(() => {
    if (workspaceId === '') return
    presence.open(workspaceId)
    return () => {
      presence.close()
    }
  }, [presence, workspaceId])
  useEffect(() => {
    const onHide = () => presence.handlePagehide()
    window.addEventListener('pagehide', onHide)
    return () => window.removeEventListener('pagehide', onHide)
  }, [presence])
  useEffect(() => () => presence.dispose(), [presence])

  // ── Event refs (FR-W3-2) ──────────────────────────────────────────────
  // Human-initiated dialing keeps the D29/R2-9 `retry=true` marker refs
  // (MC-33; A-6: bypasses backoff only, never capacity or security, and an
  // automatic request never carries it — MC-W3-9). The event refs carry the
  // W3 refresh semantics: the MODE the next fetch runs in (cache_first for
  // open/switch events, live for manual refresh and own actions) and the
  // eligible event it serves. Everything defaults to the cache-first open
  // event; triggers set refs BEFORE refetch/invalidate.
  const foldersHumanRef = useRef(false)
  const messagesHumanRef = useRef(false)
  const detailHumanRef = useRef(false)
  const foldersModeRef = useRef<MailReadMode>('cache_first')
  const messagesModeRef = useRef<MailReadMode>('cache_first')
  const foldersRefreshMappingRef = useRef(false)
  const foldersEventRef = useRef<Extract<MailCacheEvent, { kind: 'open' | 'manual_refresh' | 'own_action' }>>({ kind: 'open' })
  const messagesEventRef = useRef<MailCacheEvent>({ kind: 'open' })

  // ── Cache-view state (mailCacheView.ts — the §3.1 freeze) ─────────────
  const [foldersView, setFoldersView] = useState<MailFolderViewState>(createMailFolderView)
  const foldersViewRef = useRef(foldersView)
  foldersViewRef.current = foldersView
  const [listView, setListView] = useState<MailFolderViewState>(createMailFolderView)
  const listViewRef = useRef(listView)
  listViewRef.current = listView
  const [loadedPages, setLoadedPages] = useState<MailLoadedPages>(EMPTY_LOADED_PAGES)
  const [search, setSearch] = useState<MailSearchState | null>(null)
  const [cacheNoticeDismissed, setCacheNoticeDismissed] = useState(false)
  const [staleCursorNotice, setStaleCursorNotice] = useState(false)

  const applyFoldersView = (next: MailFolderViewState) => {
    foldersViewRef.current = next
    setFoldersView(next)
  }
  const applyListView = (next: MailFolderViewState) => {
    listViewRef.current = next
    setListView(next)
  }

  // ── Queries ───────────────────────────────────────────────────────────
  // No refetchInterval anywhere on mail reads (MC-W3-10: FOLDERS_REFETCH_MS
  // is gone); no window-focus/reconnect refetches — every request belongs
  // to an eligible event (US-1 AS-3, MC-W3-2). staleTime 0: a remount IS a
  // new open event and must actually read.
  const foldersQuery = useQuery({
    queryKey: [...FOLDERS_KEY, workspaceId, agentId],
    queryFn: async () => {
      const retry = foldersHumanRef.current
      foldersHumanRef.current = false
      const mode = foldersModeRef.current
      foldersModeRef.current = 'cache_first'
      const refreshMapping = foldersRefreshMappingRef.current
      foldersRefreshMappingRef.current = false
      const event = foldersEventRef.current
      foldersEventRef.current = { kind: 'open' }
      const observerId = presenceRef.current.currentObserverId() ?? undefined
      const started = beginCacheRead(foldersViewRef.current)
      applyFoldersView(mode === 'live' ? { ...started.view, checking: true } : started.view)
      const cacheRes = await fetchMailFolders(workspaceId, agentId as string, {
        retry,
        mode,
        ...(refreshMapping ? { refresh_mapping: true } : {}),
        ...(observerId !== undefined ? { observer_id: observerId } : {}),
      })
      if (mode === 'live') {
        // Manual refresh / own action: the read IS the refresh.
        const meta = cacheRes.metadata ? mailViewMetaFrom(cacheRes.metadata) : null
        const applied = onLiveSettled(foldersViewRef.current, started.seq, meta ?? {
          source: 'live', last_validated_at: null, stale: false, refresh_needed: false, notice_code: null, publication_revision: null,
        })
        applyFoldersView(applied.view)
        return cacheRes
      }
      const meta = cacheRes.metadata ? mailViewMetaFrom(cacheRes.metadata) : null
      const settled = onCacheReadSettled(foldersViewRef.current, started.seq, meta, event, new Date())
      applyFoldersView(settled.view)
      if (!settled.live) return cacheRes
      // The SAME event's one live leg (FR-W3-2; U5's fall-through for
      // source=none). The cache-first rows are published to the query cache
      // NOW so they render immediately and survive a failed live leg
      // (US-1 AS-1: rows update in place when the refresh settles).
      queryClient.setQueryData([...FOLDERS_KEY, workspaceId, agentId], cacheRes)
      const liveSeq = settled.view.issuedSeq
      try {
        const liveRes = await fetchMailFolders(workspaceId, agentId as string, {
          mode: 'live',
          ...(observerId !== undefined ? { observer_id: observerId } : {}),
        })
        const liveMeta = liveRes.metadata ? mailViewMetaFrom(liveRes.metadata) : null
        const applied = onLiveSettled(settled.view, liveSeq, liveMeta ?? {
          source: 'live', last_validated_at: null, stale: false, refresh_needed: false, notice_code: null, publication_revision: null,
        })
        applyFoldersView(applied.view)
        return liveRes
      } catch (err) {
        applyFoldersView(onLiveFailed(settled.view, liveSeq))
        throw err
      }
    },
    enabled: agentId !== null,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })

  const messagesQuery = useQuery({
    queryKey: [...MESSAGES_KEY, workspaceId, agentId, folder],
    queryFn: async () => {
      const retry = messagesHumanRef.current
      messagesHumanRef.current = false
      const mode = messagesModeRef.current
      messagesModeRef.current = 'cache_first'
      const event = messagesEventRef.current
      messagesEventRef.current = { kind: 'open' }
      const observerId = presenceRef.current.currentObserverId() ?? undefined
      const started = beginCacheRead(listViewRef.current)
      applyListView(mode === 'live' ? { ...started.view, checking: true } : started.view)
      // Release the working set beyond the reusable newest page: a new
      // first-page read starts the view over (US-3 AS-7).
      setLoadedPages(EMPTY_LOADED_PAGES)
      const cacheRes = await fetchMailMessages(workspaceId, agentId as string, folder, {
        limit: MAIL_PAGE_SIZE,
        retry,
        mode,
        ...(observerId !== undefined ? { observer_id: observerId } : {}),
      })
      if (mode === 'live') {
        const meta = cacheRes.metadata ? mailViewMetaFrom(cacheRes.metadata) : null
        const applied = onLiveSettled(listViewRef.current, started.seq, meta ?? {
          source: 'live', last_validated_at: null, stale: false, refresh_needed: false, notice_code: null, publication_revision: null,
        })
        applyListView(applied.view)
        return cacheRes
      }
      const meta = cacheRes.metadata ? mailViewMetaFrom(cacheRes.metadata) : null
      const settled = onCacheReadSettled(listViewRef.current, started.seq, meta, event, new Date())
      applyListView(settled.view)
      if (!settled.live) return cacheRes
      // Publish the cache-first rows now: they render immediately and stay
      // visible (labelled with the failed line) if the live leg fails
      // (US-1 AS-1; US-2 AS-3/AS-4).
      queryClient.setQueryData([...MESSAGES_KEY, workspaceId, agentId, folder], cacheRes)
      const liveSeq = settled.view.issuedSeq
      try {
        const liveRes = await fetchMailMessages(workspaceId, agentId as string, folder, {
          limit: MAIL_PAGE_SIZE,
          mode: 'live',
          ...(observerId !== undefined ? { observer_id: observerId } : {}),
        })
        const liveMeta = liveRes.metadata ? mailViewMetaFrom(liveRes.metadata) : null
        const applied = onLiveSettled(settled.view, liveSeq, liveMeta ?? {
          source: 'live', last_validated_at: null, stale: false, refresh_needed: false, notice_code: null, publication_revision: null,
        })
        applyListView(applied.view)
        return liveRes
      } catch (err) {
        applyListView(onLiveFailed(settled.view, liveSeq))
        throw err
      }
    },
    enabled: agentId !== null && foldersQuery.isSuccess && folder !== null,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })

  const summaryQuery = useQuery({
    queryKey: ['mail-summary', workspaceId],
    queryFn: () => fetchMailSummary(workspaceId),
    refetchInterval: SUMMARY_REFETCH_MS,
    refetchIntervalInBackground: false,
  })

  // ── Paging: Load more (US-3, MC-W3-1) ─────────────────────────────────
  // Pages beyond the cached newest window always run live (the contract's
  // cache-first description); each Load more issues exactly one request
  // carrying the page's cursor and appends exactly its rows.
  const loadMore = async () => {
    const first = messagesQuery.data
    if (first === undefined || agentId === null) return
    const lastPage = loadedPages.pages.at(-1) ?? first
    if (lastPage.next_cursor === null || lastPage.next_cursor === undefined) return
    const observerId = presenceRef.current.currentObserverId() ?? undefined
    setLoadedPages((prev) => ({ ...prev, loadingMore: true, loadMoreError: null }))
    try {
      const res = await fetchMailMessages(workspaceId, agentId, folder, {
        limit: MAIL_PAGE_SIZE,
        cursor: lastPage.next_cursor,
        ...(observerId !== undefined ? { observer_id: observerId } : {}),
      })
      setLoadedPages((prev) => ({ ...prev, pages: [...prev.pages, res], loadingMore: false }))
    } catch (err) {
      if (isApiError(err) && err.status === 409) {
        // Typed stale cursor: reset the view to the folder's first page with
        // the visible notice — no spin, no silent retry, no replay (FR-W3-9).
        setStaleCursorNotice(true)
        setLoadedPages(EMPTY_LOADED_PAGES)
        messagesModeRef.current = 'cache_first'
        messagesEventRef.current = { kind: 'open' }
        await messagesQuery.refetch()
        return
      }
      setLoadedPages((prev) => ({ ...prev, loadingMore: false, loadMoreError: mailErrorCode(err) }))
    }
  }

  // ── Folder-scoped search (US-3 AS-4, FR-W3-8) ─────────────────────────
  const runSearch = async (rawQuery: string) => {
    const query = rawQuery.trim()
    if (query === '' || agentId === null) return
    const observerId = presenceRef.current.currentObserverId() ?? undefined
    setSearch({ query, rows: [], nextCursor: null, hasMore: false, viewLimitReached: false, loading: true, loadingMore: false, error: null })
    try {
      const res = await fetchMailMessages(workspaceId, agentId, folder, {
        limit: MAIL_PAGE_SIZE,
        search: query,
        ...(observerId !== undefined ? { observer_id: observerId } : {}),
      })
      setSearch((prev) => prev === null ? prev : {
        ...prev,
        rows: res.messages,
        nextCursor: res.next_cursor ?? null,
        hasMore: res.has_more === true && res.view_limit_reached !== true,
        viewLimitReached: res.view_limit_reached === true,
        loading: false,
      })
    } catch (err) {
      if (isApiError(err) && err.status === 409) {
        setStaleCursorNotice(true)
        setSearch(null)
        messagesModeRef.current = 'cache_first'
        messagesEventRef.current = { kind: 'open' }
        await messagesQuery.refetch()
        return
      }
      setSearch((prev) => prev === null ? prev : { ...prev, loading: false, error: mailErrorCode(err) })
    }
  }

  const loadMoreSearch = async () => {
    if (search === null || search.nextCursor === null || agentId === null) return
    const observerId = presenceRef.current.currentObserverId() ?? undefined
    setSearch((prev) => prev === null ? prev : { ...prev, loadingMore: true, error: null })
    try {
      const res = await fetchMailMessages(workspaceId, agentId, folder, {
        limit: MAIL_PAGE_SIZE,
        search: search.query,
        cursor: search.nextCursor,
        ...(observerId !== undefined ? { observer_id: observerId } : {}),
      })
      setSearch((prev) => prev === null ? prev : {
        ...prev,
        rows: [...prev.rows, ...res.messages],
        nextCursor: res.next_cursor ?? null,
        hasMore: res.has_more === true && res.view_limit_reached !== true,
        viewLimitReached: res.view_limit_reached === true,
        loadingMore: false,
      })
    } catch (err) {
      if (isApiError(err) && err.status === 409) {
        setStaleCursorNotice(true)
        setSearch(null)
        return
      }
      setSearch((prev) => prev === null ? prev : { ...prev, loadingMore: false, error: mailErrorCode(err) })
    }
  }

  // ── Manual Refresh (US-10 AS-1) ───────────────────────────────────────
  // One folder-list request with mode=live and refresh_mapping=true, plus
  // the folder's one live list refresh — no other folder is contacted.
  const refreshAll = () => {
    if (agentId === null) return
    foldersEventRef.current = { kind: 'manual_refresh' }
    foldersModeRef.current = 'live'
    foldersRefreshMappingRef.current = true
    void foldersQuery.refetch()
    if (foldersQuery.isSuccess) {
      messagesEventRef.current = { kind: 'manual_refresh' }
      messagesModeRef.current = 'live'
      void messagesQuery.refetch()
    }
    void summaryQuery.refetch()
  }

  /** Own-action refresh (US-1 AS-6): exactly one live refresh of the
   * affected folder after a successful mark-seen/send/draft action — the
   * invalidation's follow-up fetches ARE that refresh, marked live. */
  const refreshAfterOwnAction = () => {
    foldersEventRef.current = { kind: 'own_action' }
    foldersModeRef.current = 'live'
    messagesEventRef.current = { kind: 'own_action' }
    messagesModeRef.current = 'live'
    applyFoldersView(markLocalMutation(foldersViewRef.current))
    applyListView(markLocalMutation(listViewRef.current))
    void queryClient.invalidateQueries({ queryKey: FOLDERS_KEY })
    void queryClient.invalidateQueries({ queryKey: MESSAGES_KEY })
  }

  /** Retry now (D29/R2-9, MC-33): the banner's human gesture — the dialing
   * queries' next fetches carry `retry=true` (bypasses backoff only) and
   * re-run their events. The summary GET reads saved state, never dials. */
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
        await fetchMailMessages(workspaceId, agentId as string, folder, { retry })
        throw new Error(S_MESSAGE_CHANGED, { cause: error })
      }
    },
    enabled: agentId !== null && folder !== null && selectedRef !== null,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
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
  const detailDate = detail?.date && hasMailDate(detail.date) ? formatMailDate(detail.date) : ''
  const compactDraftList = layout === 'stacked' && detail?.is_draft === true
    && draftEditingRef === `${agentId}:${folder}:${selectedRef}`

  // S-11's pinned text lives at module scope (S_MESSAGE_CHANGED).

  const seenMutation = useMutation({
    mutationFn: (ref: string) => markMailSeen(workspaceId, agentId as string, folder, ref),
    onSuccess: () => {
      refreshAfterOwnAction()
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
      refreshAfterOwnAction()
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
      refreshAfterOwnAction()
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
      refreshAfterOwnAction()
      addToast({ message: 'Draft discarded', variant: 'success' })
    },
    onError: (err) => addToast({ message: mailErrorCode(err), variant: 'error' })
  })

  // Mark seen on open (B-27, US-6): after a successful detail fetch of an
  // unseen message outside Drafts. The seen mutation's onSuccess performs
  // the own-action refresh (unread count drops; exactly one folder refresh).
  useEffect(() => {
    if (detailQuery.isSuccess && detail !== null && detail.seen === false && folder !== 'drafts' && selectedRef !== null && !seenMutation.isPending) {
      seenMutation.mutate(selectedRef)
    }
  }, [detailQuery.isSuccess, detail?.seen, folder, selectedRef])

  // ── Compose (US-5) with the F5 reply context ─────────────────────────
  // mode reply / reply_all captures the target ref at open; the generated
  // MailReplyContextResponse (To/Cc/subject/escaped quote) prefills the
  // dialog; a failed context fetch degrades to the legacy minimal prefill
  // rather than blocking compose.
  const [compose, setCompose] = useState<{ mode: 'new' | 'reply' | 'reply_all'; ref: string | null } | null>(null)
  const replyContextQuery = useQuery({
    queryKey: ['mail-reply-context', workspaceId, agentId, folder, compose?.ref, compose?.mode],
    queryFn: () => fetchMailReplyContext(workspaceId, agentId as string, folder, compose?.ref as string, {
      mode: compose?.mode === 'reply_all' ? 'reply_all' : 'reply',
    }),
    enabled: compose !== null && (compose.mode === 'reply' || compose.mode === 'reply_all') && compose.ref !== null && agentId !== null,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })

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
      refreshAfterOwnAction()
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

  const replyTarget = detail === null ? null : { from: detail.from ?? '', subject: detail.subject ?? '', messageId: detail.message_id ?? '' }
  const folderErrorCode = foldersQuery.isError ? mailErrorCode(foldersQuery.error) : null
  // A 503 backoff is a retry posture, not the IMAP cause. Use the selected
  // mailbox's saved watcher class when it is available; never borrow another
  // mailbox's class.
  const folderErrorClass = folderErrorCode === 'backoff' && watcherItem?.agent_id === agentId
    ? watcherItem.last_error_class ?? folderErrorCode
    : folderErrorCode

  // The active folder's discovery state (US-4 / S-7 / S-8): the rail keeps
  // the role visible in every state; the list zone renders the per-state
  // explanation. Unknown never claims absence; absent is never an error.
  const activeFolderState = foldersQuery.data?.folders.find((f) => f.slug === folder) ?? null

  // Freshness line inputs (US-2 / S-3..S-6): the list read's metadata, the
  // in-flight checking indicator and the failed-refresh state. The LOCAL
  // rule governed issuing already; these flags only decide presentation. A
  // metadata-less response (transitional window) presents as unknown via
  // MAIL_UNKNOWN_PROVENANCE_META — never "just checked", never a fabricated
  // zero.
  const listMeta = listView.meta ?? MAIL_UNKNOWN_PROVENANCE_META
  const listFreshness = listView.refreshFailed
    ? formatMailRefreshFailedLine(listMeta.last_validated_at)
    : formatMailFreshnessLine(listMeta, listView.checking)
  const showCacheNotice = listView.meta?.notice_code === 'cache_unavailable' && !cacheNoticeDismissed

  // Browse rows: page 1 + appended pages (US-3).
  const browseRows: MailMessageSummary[] = useMemo(() => {
    const first = messagesQuery.data?.messages ?? []
    return [...first, ...loadedPages.pages.flatMap((page) => page.messages)]
  }, [messagesQuery.data, loadedPages.pages])
  // A live fill with nothing to show yet (source=none's fall-through, or a
  // refresh of an empty view) is a LOAD IN PROGRESS, not an empty mailbox —
  // US-1 AS-4: source=none is never displayed as an empty mailbox, so the
  // S-1 skeleton replaces the list until rows exist or the event settles.
  const listCheckingEmpty = listView.checking && browseRows.length === 0
  const lastBrowsePage = loadedPages.pages.at(-1) ?? messagesQuery.data ?? null
  const browseHasMore = lastBrowsePage !== null
    && lastBrowsePage.has_more === true
    && lastBrowsePage.view_limit_reached !== true
    && lastBrowsePage.next_cursor !== null
    && lastBrowsePage.next_cursor !== undefined
  const browseCeiling = lastBrowsePage?.view_limit_reached === true

  const selectMessage = (message: MailMessageSummary) => {
    setSelectedRow({ message, workspaceId, agentId })
    setSelectedRef(mailUidRef(message.uidvalidity, message.uid))
  }

  const onFolderChange = (slug: string) => {
    setIntent((prev) => ({ ...prev, folder: slug }))
    setSelectedRow(null)
    setSelectedRef(null)
    setSearch(null)
    setStaleCursorNotice(false)
    setCacheNoticeDismissed(false)
    // Folder switch: a fresh eligible event for the new folder's list
    // (US-1 AS-2); the rail read itself is not per-folder.
    messagesEventRef.current = { kind: 'folder_switch' }
    messagesModeRef.current = 'cache_first'
  }

  const [searchDraft, setSearchDraft] = useState('')

  return (
    <div data-testid="mail-panel" className="flex h-full min-h-0 w-full flex-col bg-[var(--color-surface-0)]">
      <div className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2)]">
        <Select
          value={agentId ?? ''}
          onValueChange={(next) => {
            setMailboxChoice({ workspaceId, directive: mailboxId })
            setIntent((prev) => ({ ...prev, agentId: next }))
            setSelectedRow(null)
            setSelectedRef(null)
            setSearch(null)
            setStaleCursorNotice(false)
            setCacheNoticeDismissed(false)
          }}
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
          variant="outline"
          className="gap-[var(--space-1)]"
          disabled={agentId === null}
          onClick={refreshAll}
          data-testid="mail-refresh"
        >
          Refresh
        </Button>
        <Button
          size="sm"
          className="gap-[var(--space-1)]"
          disabled={agentId === null}
          aria-describedby={workspaceMailboxes.length === 0 && mailboxesQuery.isSuccess ? 'mail-no-mailbox-help' : undefined}
          onClick={() => setCompose({ mode: 'new', ref: null })}
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
              onFolderChange={onFolderChange}
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
                    foldersModeRef.current = 'cache_first'
                    void foldersQuery.refetch()
                  }}
                >
                  Retry
                </Button>
              </div>
            ) : (
              <>
                {/* Freshness line (S-3..S-6) + notices (S-12 / S-23) — the
                    list header line; stale rows stay visible beneath it. */}
                {(listFreshness !== null || showCacheNotice || staleCursorNotice) && (
                  <div className="flex shrink-0 flex-col gap-[var(--space-1)] border-b border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-1)]">
                    {staleCursorNotice && (
                      <p role="status" data-testid="mail-stale-cursor-notice" className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
                        The folder changed. Showing the newest messages.
                      </p>
                    )}
                    {showCacheNotice && (
                      <p
                        role="status"
                        data-testid="mail-cache-notice"
                        className="flex items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
                      >
                        <span className="min-w-0 flex-1">Mail cache unavailable; using live access.</span>
                        <Button variant="ghost" size="sm" className="shrink-0" onClick={() => setCacheNoticeDismissed(true)}>
                          Dismiss
                        </Button>
                      </p>
                    )}
                    {listFreshness !== null && (
                      <p
                        data-testid="mail-freshness-line"
                        className="flex items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
                      >
                        <span className="min-w-0 flex-1">
                          {listFreshness}
                          {listView.checking && (
                            <span
                              aria-hidden="true"
                              className="ml-[var(--space-1)] inline-block h-2 w-2 rounded-full border border-[var(--color-accent)] border-t-transparent animate-spin align-middle"
                            />
                          )}
                        </span>
                        {listView.refreshFailed && (
                          <Button
                            variant="outline"
                            size="sm"
                            className="shrink-0"
                            data-testid="mail-refresh-retry"
                            onClick={() => {
                              messagesModeRef.current = 'live'
                              messagesEventRef.current = { kind: 'manual_refresh' }
                              void messagesQuery.refetch()
                            }}
                          >
                            Retry
                          </Button>
                        )}
                      </p>
                    )}
                  </div>
                )}
                {/* Search control (US-3 AS-4): always reachable in the browse
                    view; at the ceiling it is the path onward (S-21). */}
                <form
                  className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-1)]"
                  onSubmit={(e) => {
                    e.preventDefault()
                    void runSearch(searchDraft)
                  }}
                >
                  <Input
                    data-testid="mail-search-input"
                    aria-label={`Search ${folder}`}
                    value={searchDraft}
                    onChange={(e) => setSearchDraft(e.target.value)}
                    placeholder={`Search in ${foldersQuery.data?.folders.find((f) => f.slug === folder)?.display_name ?? folder}`}
                    className="h-7 min-w-0 flex-1 text-[length:var(--type-caption-size)]"
                  />
                  <Button type="submit" variant="outline" size="sm" className="shrink-0" disabled={searchDraft.trim() === ''}>
                    Search
                  </Button>
                </form>
                {activeFolderState?.availability === 'absent' ? (
                  /* S-7: confirmed absence explains; never an error surface. */
                  <div className="flex flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-4)] text-center" data-testid="mail-folder-absent">
                    <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">No messages</p>
                    <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                      {activeFolderState.slug === 'drafts'
                        ? 'Your mail server has no Drafts folder. You can set the folder name in mailbox settings.'
                        : 'Your mail server has no Sent folder. You can set the folder name in mailbox settings.'}
                    </p>
                    <Button asChild variant="outline" size="sm">
                      <a href="#/connectors">Open mailbox settings</a>
                    </Button>
                  </div>
                ) : activeFolderState?.availability === 'unknown' ? (
                  /* S-8: unresolved is NOT absence — the settings prompt. */
                  <div className="flex flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-4)] text-center" data-testid="mail-folder-unknown">
                    <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
                      {activeFolderState.slug === 'drafts'
                        ? "Couldn't confirm the Drafts folder on this server."
                        : "Couldn't confirm the Sent folder on this server."}
                    </p>
                    <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                      Set the folder name in mailbox settings.
                    </p>
                    <Button asChild variant="outline" size="sm">
                      <a href="#/connectors">Open mailbox settings</a>
                    </Button>
                  </div>
                ) : search !== null ? (
                  /* ── The search view (US-3 AS-4) ── */
                  <div className="flex min-h-0 flex-1 flex-col" data-testid="mail-search-view">
                    <div className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-1)]">
                      <p className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                        {search.loading ? 'Searching…' : `Results for “${search.query}”`}
                      </p>
                      <Button
                        variant="outline"
                        size="sm"
                        className="shrink-0"
                        data-testid="mail-search-exit"
                        onClick={() => setSearch(null)}
                      >
                        Back to {foldersQuery.data?.folders.find((f) => f.slug === folder)?.display_name ?? folder}
                      </Button>
                    </div>
                    {search.error !== null ? (
                      <div role="alert" className="flex flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-4)]">
                        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">{search.error}</p>
                        <Button variant="outline" size="sm" onClick={() => void runSearch(search.query)}>Retry</Button>
                      </div>
                    ) : search.loading ? (
                      <div className="flex-1 p-[var(--space-3)]"><ListSkeleton /></div>
                    ) : search.rows.length === 0 ? (
                      <p className="p-[var(--space-4)] text-center text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]" data-testid="mail-search-empty">
                        {`No messages match "${search.query}".`}
                      </p>
                    ) : (
                      <>
                        <MailMessageList
                          messages={search.rows}
                          selectedRef={selectedRef}
                          onSelect={selectMessage}
                          hasMore={search.hasMore}
                          loadingMore={search.loadingMore}
                          onLoadMore={() => void loadMoreSearch()}
                        />
                        {search.viewLimitReached && (
                          <p className="shrink-0 border-t border-[var(--color-border)] p-[var(--space-2)] text-center text-[length:var(--type-caption-size)] text-[var(--color-muted)]" data-testid="mail-search-ceiling">
                            You're viewing the newest 200 matches. Refine your search to find older messages.
                          </p>
                        )}
                      </>
                    )}
                  </div>
                ) : (
                  <>
                    {(messagesQuery.isPending || listCheckingEmpty) && (
                      <div className="flex-1 p-[var(--space-3)]" data-testid="mail-list-loading" aria-label="Loading messages">
                        <ListSkeleton />
                      </div>
                    )}
                    {messagesQuery.isError && messagesQuery.data === undefined && (
                      <div
                        role="alert"
                        data-testid="mail-messages-error"
                        className="flex flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-4)]"
                      >
                        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
                          {mailFailureHeadline(messagesQuery.error)}
                        </p>
                        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                          Error class: {mailErrorCode(messagesQuery.error)}
                        </p>
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => {
                            messagesHumanRef.current = true
                            messagesModeRef.current = 'cache_first'
                            void messagesQuery.refetch()
                          }}
                        >
                          Retry
                        </Button>
                      </div>
                    )}
                    {(messagesQuery.isSuccess || (messagesQuery.isError && messagesQuery.data !== undefined)) && !listCheckingEmpty && (
                      <>
                        <MailMessageList
                          messages={browseRows}
                          selectedRef={selectedRef}
                          onSelect={selectMessage}
                          hasMore={browseHasMore}
                          loadingMore={loadedPages.loadingMore}
                          onLoadMore={() => void loadMore()}
                        />
                        {browseCeiling && (
                          <p className="shrink-0 border-t border-[var(--color-border)] p-[var(--space-2)] text-center text-[length:var(--type-caption-size)] text-[var(--color-muted)]" data-testid="mail-ceiling-message">
                            You're viewing the newest 200 messages. Search to find older ones.
                          </p>
                        )}
                        {loadedPages.loadMoreError !== null && (
                          <div role="alert" className="flex shrink-0 items-center gap-[var(--space-2)] border-t border-[var(--color-border)] p-[var(--space-2)]">
                            <p className="min-w-0 flex-1 text-[length:var(--type-caption-size)] text-[var(--color-error)]">
                              Couldn't load older messages — {loadedPages.loadMoreError}
                            </p>
                            <Button variant="outline" size="sm" className="shrink-0" onClick={() => void loadMore()}>
                              Retry
                            </Button>
                          </div>
                        )}
                      </>
                    )}
                  </>
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
            {selectedRef !== null && detailQuery.isError && (detailIsStaleReference(detailQuery.error) ? (
              /* S-11: a stale row click can never render as a successful open. */
              <div className="flex flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-4)]" data-testid="mail-message-changed">
                <p className="text-center text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
                  {S_MESSAGE_CHANGED}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setSelectedRow(null)
                    setSelectedRef(null)
                    messagesEventRef.current = { kind: 'own_action' }
                    messagesModeRef.current = 'live'
                    void messagesQuery.refetch()
                  }}
                >
                  Refresh list
                </Button>
              </div>
            ) : (
              <div className="flex flex-1 flex-col items-center justify-center gap-[var(--space-2)] p-[var(--space-4)]">
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
                  {mailFailureHeadline(detailQuery.error)}
                </p>
                <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                  Error class: {mailErrorCode(detailQuery.error)}
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
            ))}
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
                      {detailDate !== '' && (
                        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                          Date: {detailDate}
                        </p>
                      )}
                    </div>
                  ) : (
                    <p className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                      From {detail.from ?? 'unknown'}{detailDate ? ` · ${detailDate}` : ''}
                    </p>
                  )}
                  {folder !== 'drafts' && (
                    <div className="flex shrink-0 gap-[var(--space-1)]">
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => setCompose({ mode: 'reply', ref: selectedRef })}
                      >
                        Reply
                      </Button>
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => setCompose({ mode: 'reply_all', ref: selectedRef })}
                      >
                        Reply all
                      </Button>
                    </div>
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
                          folderName={foldersQuery.data?.folders.find((f) => f.slug === folder)?.display_name ?? folder}
                          messageRef={selectedRef as string}
                          subject={detail.subject ?? ''}
                          attachments={detail.attachments}
                          presenceRef={presenceRef}
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
        replyTo={compose !== null && compose.mode !== 'new' && replyTarget !== null ? replyTarget : undefined}
        replyContext={
          compose !== null && compose.mode !== 'new' && replyContextQuery.data !== undefined
            ? replyContextQuery.data
            : undefined
        }
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

/** S-11's surface condition: the typed stale-reference 409, a 404 for a
 * message that no longer resolves, or the stale-draft recovery throw. */
function detailIsStaleReference(err: unknown): boolean {
  if (err instanceof Error && err.message === 'This message changed or was deleted. Refresh the list.') return true
  return isApiError(err) && (err.status === 409 || err.status === 404)
}

/** The save/open state shared by the attachment rows — one controller per
 * panel instance; the M-02 token semantics live in the controller. */
interface AttachmentListProps {
  workspaceId: string
  agentId: string
  folder: string
  folderName: string
  messageRef: string
  subject: string
  attachments: MailMessage['attachments']
  presenceRef: React.RefObject<MailPanelPresence>
}

/** Attachments of an open message (D28 + W3 US-6/US-7): filename, size
 * ("Size unknown" never "0 B"), and three actions — Open (mint + handoff),
 * Save to Library (M-02 token), Download (browser) — each with a distinct
 * accessible name including the filename (the focus-return target). */
function AttachmentList({ workspaceId, agentId, folder, folderName, messageRef, subject, attachments, presenceRef }: AttachmentListProps) {
  const addToast = useUiStore((s) => s.addToast)
  const saveController = useMemo(() => createMailAttachmentSaveController(), [])
  const saveStatus = useSyncExternalStore(saveController.subscribe, saveController.getStatus)
  useEffect(() => () => saveController.reset(), [saveController])

  /** The row-scoped Open failure state (S-26/S-27/S-28) and the S-29
   * in-flight marker — one mint per row at a time. */
  const [openState, setOpenState] = useState<Record<number, { stage: 'opening' } | { stage: 'failed'; outcome: Exclude<MailOpenAttachmentOutcome, { stage: 'opened' }> }>>({})

  const handleOpen = async (partIndex: number, filename: string) => {
    if (openState[partIndex]?.stage === 'opening') return
    const observerId = presenceRef.current?.currentObserverId() ?? undefined
    setOpenState((prev) => ({ ...prev, [partIndex]: { stage: 'opening' } }))
    announceMailHandoff(openingAnnouncement(filename))
    const returnFocus: MailReturnFocus = {
      kind: 'attachment-action',
      id: attachmentActionId(folder, messageRef, partIndex, 'open'),
    }
    try {
      const outcome = await openMailAttachment({
        workspaceId,
        agentId,
        folder: folder as 'inbox' | 'sent' | 'drafts',
        messageRef,
        partIndex,
        filename,
        subject,
        folderName,
        returnFocus,
        ...(observerId !== undefined ? { observerId } : {}),
      })
      if (outcome.stage === 'opened') {
        setOpenState((prev) => {
          const next = { ...prev }
          delete next[partIndex]
          return next
        })
        return
      }
      setOpenState((prev) => ({ ...prev, [partIndex]: { stage: 'failed', outcome } }))
    } catch (err) {
      // openMailAttachment classifies its own failures; this guard keeps an
      // unexpected throw from spinning the row (US-6 AS-8).
      setOpenState((prev) => ({
        ...prev,
        [partIndex]: { stage: 'failed', outcome: { stage: 'failed', errorClass: err instanceof Error ? err.message : 'unknown_error' } },
      }))
    }
  }

  return (
    <ul aria-label="Attachments" className="flex flex-col gap-[var(--space-1)]">
      {attachments.map((attachment) => {
        const overCap = isMailAttachmentOverCap(attachment.size_bytes)
        const filename = attachment.filename
        const openId = attachmentActionId(folder, messageRef, attachment.part_index, 'open')
        const opening = openState[attachment.part_index]?.stage === 'opening'
        const failedOutcome = openState[attachment.part_index]?.stage === 'failed'
          ? (openState[attachment.part_index] as { stage: 'failed'; outcome: Exclude<MailOpenAttachmentOutcome, { stage: 'opened' }> }).outcome
          : null
        const savedReceipt = saveStatus.stage === 'saved' ? saveStatus.response : null
        return (
          <li
            key={attachment.part_index}
            data-testid="mail-attachment-row"
            className="flex flex-col gap-[var(--space-1)] border-b border-[var(--color-border)] pb-[var(--space-1)]"
          >
            <div className="flex items-center gap-[var(--space-2)]">
              <span className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
                {filename}
              </span>
              <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
                {attachment.size_bytes === null || attachment.size_bytes === undefined ? 'Size unknown' : formatMailBytes(attachment.size_bytes)}
              </span>
              <Button
                variant="ghost"
                size="sm"
                data-mail-attachment-action={openId}
                disabled={overCap || opening}
                aria-busy={opening || undefined}
                aria-describedby={overCap ? `mail-attachment-overcap-${attachment.part_index}` : undefined}
                onClick={() => void handleOpen(attachment.part_index, filename)}
              >
                {opening ? `Opening ${filename}…` : `Open ${filename} attachment`}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                disabled={overCap || saveStatus.stage === 'loading'}
                aria-describedby={overCap ? `mail-attachment-overcap-${attachment.part_index}` : undefined}
                onClick={() => {
                  const observerId = presenceRef.current?.currentObserverId() ?? undefined
                  void saveController.save({
                    workspaceId,
                    agentId,
                    folder: folder as 'inbox' | 'sent' | 'drafts',
                    messageRef,
                    partIndex: attachment.part_index,
                    ...(observerId !== undefined ? { observerId } : {}),
                  }).catch(() => undefined)
                }}
              >
                {saveStatus.stage === 'loading' ? 'Saving…' : `Save ${filename} to Library`}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  downloadMailAttachment({ workspaceId, agentId, folder, messageRef, partIndex: attachment.part_index, filename, retry: true }).catch(
                    (err: unknown) => addToast({ message: mailErrorCode(err), variant: 'error' }),
                  )
                }}
              >
                {`Download ${filename}`}
              </Button>
            </div>
            {overCap && (
              <p
                id={`mail-attachment-overcap-${attachment.part_index}`}
                data-testid="mail-attachment-overcap"
                className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
              >
                This attachment is larger than the 25 MB preview limit. Use Download.
              </p>
            )}
            {failedOutcome !== null && (
              <p role="alert" data-testid="mail-attachment-open-failed" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">
                {openFailureText(failedOutcome)}
                {failedOutcome.stage !== 'over-cap' && failedOutcome.stage !== 'stale-reference' && (
                  <>
                    {' '}
                    <Button variant="link" size="sm" onClick={() => void handleOpen(attachment.part_index, filename)}>
                      Retry
                    </Button>
                  </>
                )}
              </p>
            )}
            {saveStatus.stage === 'saved' && savedReceipt !== null && (
              <div className="flex items-center gap-[var(--space-2)]">
                <p role="status" data-testid="mail-attachment-saved" className="min-w-0 flex-1 text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
                  {savedReceipt.warning_code !== null && savedReceipt.warning_code !== undefined
                    ? `${savedReceipt.warning_code}: Saved to Library as ${savedReceipt.entry.name}.`
                    : `Saved to Library as ${savedReceipt.entry.name}.`}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  data-testid="mail-attachment-open-in-library"
                  onClick={() => {
                    useUiStore.getState().openPanel('library', { workspaceId, path: savedReceipt.path })
                  }}
                >
                  Open in Library
                </Button>
              </div>
            )}
            {saveStatus.stage === 'failed' && (
              <div role="alert" className="flex items-center gap-[var(--space-2)]">
                <p className="min-w-0 flex-1 text-[length:var(--type-caption-size)] text-[var(--color-error)]" data-testid="mail-attachment-save-failed">
                  {`Could not save to Library. ${saveStatus.reason}`}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={() => {
                    const observerId = presenceRef.current?.currentObserverId() ?? undefined
                    void saveController.save({
                      workspaceId,
                      agentId,
                      folder: folder as 'inbox' | 'sent' | 'drafts',
                      messageRef,
                      partIndex: attachment.part_index,
                      ...(observerId !== undefined ? { observerId } : {}),
                    }).catch(() => undefined)
                  }}
                >
                  Retry
                </Button>
              </div>
            )}
            {saveStatus.stage === 'unknown' && (
              <div role="status" className="flex items-center gap-[var(--space-2)]">
                <p className="min-w-0 flex-1 text-[length:var(--type-caption-size)] text-[var(--color-secondary)]" data-testid="mail-attachment-save-unknown">
                  Save result unknown — checking whether it saved.
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  data-testid="mail-attachment-save-retry"
                  onClick={() => void saveController.retry()}
                >
                  Retry save
                </Button>
              </div>
            )}
          </li>
        )
      })}
    </ul>
  )
}

/** Stable identity for one attachment row's action — the focus-return
 * target the handoff resolves on Back (US-7 AS-2's first step). */
function attachmentActionId(folder: string, messageRef: string, partIndex: number, action: 'open' | 'save'): string {
  return `${folder}:${messageRef}:${partIndex}:${action}`
}

/** The pinned failed-Open row states (S-26/S-27/S-28 + the mount-less
 * fallback) — never a raw error string, never a spinner. */
function openFailureText(outcome: Exclude<MailOpenAttachmentOutcome, { stage: 'opened' }>): string {
  switch (outcome.stage) {
    case 'over-cap':
      return 'This attachment is larger than the 25 MB preview limit. Use Download.'
    case 'stale-reference':
      return 'This message changed or was deleted. Refresh the list.'
    case 'busy':
      return busyCopy(outcome.reason)
    default:
      return `Couldn't open the attachment (Error class: ${outcome.errorClass}).`
  }
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
