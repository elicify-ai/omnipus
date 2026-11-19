// #1428 step 2 (spec C r9, 4.7, 4.9, 4.10): the recovery region above the
// session composer.
//
// - The current-tab not-stopped notice ("Not stopped. Press Stop again."),
//   persistent, with Dismiss (4.7).
// - The version banner while the connection is `unsupported` (4.10).
// - The in-box line of the open draft that holds restored input (4.9).
// - The Unsent list: "Not sent" items from other chats, "May not have been
//   delivered" and "Deleted chat" items, the pending first message of a new
//   chat that is not open (4.11), and, while unsupported, every draft (Copy).
//
// Composition only: Callout, DisclosureRow, CalloutActions, Button,
// CopyButton and ConfirmDialog (spec 4.9, F-ff). No new control type.

import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Callout, CalloutActions, CalloutDescription, CalloutTitle } from '@/components/ui/callout'
import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/ui/copy-button'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { fetchAgents, fetchSessions, fetchWorkspaces, type Session } from '@/lib/api'
import { sessionDisplayTitle } from '@/lib/nav/sessionDisplayTitle'
import { SEND_TEXT, currentSendCapability } from '@/lib/bound-send'
import { subscribeConnectionEvents } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { enterNewChat } from '@/store/session/newChatKey'
import {
  canonical,
  clearNotStopped,
  discardDraftInput,
  discardUnsentItem,
  hasDraftInput,
  isNewChatKey,
  mergeRestoredText,
  mirrorOpenDraft,
  removeUnsentItem,
  updateDraft,
  useDrafts,
  type UnsentItem,
} from '@/store/chat/drafts'
import { useOpenDraftKey } from './useSessionDraftBinding'
import { openSavedChat } from './openUnsentChat'

type Action = 'open' | 'openAndSend' | 'copy' | 'discard' | 'move'

interface Row {
  id: string
  title: string
  text: string
  lines: string[]
  actions: Action[]
  item?: UnsentItem
  queued?: { id: string; key: string; workspaceId: string | null; agentId: string | null }
}

const NARROW_PX = 640
const UNTITLED = 'Untitled session'

function subscribeCapability(callback: () => void): () => void {
  const offEvents = subscribeConnectionEvents(callback)
  const offStore = useConnectionStore.subscribe(callback)
  return () => {
    offEvents()
    offStore()
  }
}

/** The store connection's send state, re-read on every socket event. */
export function useSendState(): ReturnType<typeof currentSendCapability> {
  return useSyncExternalStore(subscribeCapability, currentSendCapability, currentSendCapability)
}

function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && window.innerWidth < NARROW_PX)
  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth < NARROW_PX)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])
  return narrow
}

function preview(text: string): string {
  return text.length > 80 ? `${text.slice(0, 80)}…` : text
}

function useNames() {
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000 })
  const { data: workspaces = [] } = useQuery({ queryKey: ['workspaces'], queryFn: () => fetchWorkspaces(), staleTime: 30_000 })
  const { data: sessions = [] } = useQuery({ queryKey: ['sessions'], queryFn: () => fetchSessions(), staleTime: 15_000 })
  return {
    agent: (id: string | null) => agents.find((a) => a.id === id)?.name ?? id ?? 'the agent',
    workspace: (id: string | null) => workspaces.find((w) => w.id === id)?.name ?? id ?? 'this workspace',
    // The sidebar's name for the chat: a main chat has no stored title of its own,
    // an untitled or unlisted one reads "Untitled session" (Defect 3, SessionRow.tsx).
    chat: (id: string) => {
      const session = sessions.find((s) => s.id === id)
      if (!session) return UNTITLED
      const agentName = agents.find((a) => a.id === session.agent_id)?.name
      return sessionDisplayTitle(session, agentName, UNTITLED, session.agent_id) ?? UNTITLED
    },
    sessionOf: (id: string) => sessions.find((s) => s.id === id),
  }
}

function useRows(openKey: string, unsupported: boolean): Row[] {
  const unsent = useDrafts((s) => s.unsent)
  const drafts = useDrafts((s) => s.drafts)
  const newChats = useDrafts((s) => s.newChats)
  const pending = useChatStore((s) => s.pendingFirstSend)
  const outbound = useChatStore((s) => s.outboundQueue)
  const held = useChatStore((s) => s.pendingDrainQueue)
  const names = useNames()
  const rows: Row[] = []
  for (const item of unsent) {
    const key = canonical(item.key)
    const chatName = isNewChatKey(key) ? `New chat with ${names.agent(item.agentId)} in ${names.workspace(item.workspaceId)}` : names.chat(key)
    if (item.kind === 'uncertain') {
      // Spec 4.7 close row / 4.9: the item names its origin chat; it is listed in every chat.
      // Chief ruling 2026-11-19: Open leads back to that chat (Defect 3).
      rows.push({ id: item.id, title: 'May not have been delivered', text: item.text, lines: [chatName, SEND_TEXT.uncertainLine], actions: ['open', 'copy', 'discard'], item })
    } else if (item.kind === 'deleted') {
      rows.push({ id: item.id, title: 'Deleted chat', text: item.text, lines: [SEND_TEXT.upload404], actions: ['copy', 'discard', 'move'], item })
    } else if (key !== openKey) {
      const title = chatName
      rows.push({ id: item.id, title, text: item.text, lines: item.reason ? [item.reason] : [], actions: ['open', 'copy', 'discard'], item })
    }
  }
  const pendingKey = pending?.payload.origin?.new_chat_key
  if (pending && pendingKey && canonical(pendingKey) !== openKey) {
    const info = newChats[pendingKey]
    const agentName = names.agent(info?.agentId ?? pending.payload.agent_id ?? null)
    const unsaved = pending.status === 'unconfirmed' || pending.status === 'not_saved' || pending.status === 'check_failed'
    rows.push({
      id: `first-send-${pending.clientMessageId}`,
      title: unsaved ? `New chat with ${agentName}, not saved yet` : `New chat with ${agentName} in ${names.workspace(info?.workspaceId ?? pending.workspaceId)}`,
      text: pending.payload.content,
      lines: [],
      actions: unsaved ? ['openAndSend', 'copy', 'discard'] : ['open', 'copy', 'discard'],
    })
  }
  // 4.11 / UAT-16: a first message for K queued while the socket was down is
  // held in the queue (never sent while K is not open); it is listed here.
  for (const queued of [...held, ...outbound]) {
    if (typeof queued === 'string' || queued.submission?.origin.kind !== 'new_chat') continue
    const sub = queued.submission
    const key = canonical(sub.origin.new_chat_key ?? null)
    if (key === openKey) continue
    rows.push({
      id: `queued-${queued.id}`,
      title: `New chat with ${names.agent(sub.agentId)}, not saved yet`,
      text: queued.content,
      lines: [],
      actions: ['openAndSend', 'copy', 'discard'],
      queued: { id: queued.id, key, workspaceId: sub.workspaceId, agentId: sub.agentId },
    })
  }
  if (unsupported) {
    for (const [key, draft] of Object.entries(drafts)) {
      if (!hasDraftInput(draft)) continue
      rows.push({ id: `draft-${key}`, title: isNewChatKey(key) ? 'New chat' : names.chat(key), text: draft.text, lines: [], actions: ['copy'] })
    }
  }
  return rows
}

function openRow(row: Row, sessionOf: (id: string) => Session | undefined): void {
  const session = useSessionStore.getState()
  const pending = useChatStore.getState().pendingFirstSend
  if (row.queued) {
    // 4.11: re-enter K with its original workspace and agent; the queue then drains it.
    enterNewChat(row.queued.workspaceId, row.queued.agentId, undefined, row.queued.key)
    useChatStore.getState().drainOutboundQueue()
    return
  }
  if (!row.item) {
    const key = pending?.payload.origin?.new_chat_key
    if (!pending || !key) return
    const info = useChatStore.getState().draftState.newChats[key]
    enterNewChat(info?.workspaceId ?? pending.workspaceId, info?.agentId ?? pending.payload.agent_id ?? null, undefined, key)
    if (row.actions.includes('openAndSend')) {
      // 4.11: re-enter K with its original workspace and agent; the send then runs.
      session.setActiveSession('__pending', info?.agentId ?? pending.payload.agent_id ?? undefined)
      useChatStore.getState().retryFirstSend()
    }
    return
  }
  const key = canonical(row.item.key)
  if (isNewChatKey(key)) {
    const info = useChatStore.getState().draftState.newChats[key]
    enterNewChat(info?.workspaceId ?? row.item.workspaceId, info?.agentId ?? row.item.agentId, undefined, key)
  } else {
    const saved = sessionOf(key)
    openSavedChat({ sessionId: key, workspaceId: row.item.workspaceId ?? saved?.workspace_id ?? null, type: saved?.type, title: saved?.title }, row.item.agentId ?? session.activeAgentId ?? undefined)
  }
}

function discardRow(row: Row): void {
  if (row.queued) {
    useChatStore.getState().discardQueuedMessage(row.queued.id)
    return
  }
  if (row.item) {
    discardUnsentItem(row.item.id)
    return
  }
  useChatStore.getState().abandonPendingFirstSend()
}

function moveHere(row: Row, openKey: string): void {
  if (!row.item) return
  updateDraft(openKey, (draft) => ({ ...draft, text: mergeRestoredText(row.item?.text ?? '', draft.text) }))
  removeUnsentItem(row.item.id)
  mirrorOpenDraft('restore')
}

const ACTION_LABEL: Record<Exclude<Action, 'copy'>, string> = {
  open: 'Open',
  openAndSend: 'Open and send',
  discard: 'Discard',
  move: 'Move text here',
}

export function UnsentNotice({ focusComposer }: { focusComposer: () => void }) {
  const openKey = useOpenDraftKey()
  const state = useSendState()
  const unsupported = state === 'unsupported'
  const notStopped = useDrafts((s) => !!s.notStopped[openKey])
  const recovery = useDrafts((s) => s.drafts[openKey]?.recovery)
  const rows = useRows(openKey, unsupported)
  const { sessionOf } = useNames()
  const narrow = useNarrow()
  const [expanded, setExpanded] = useState(false)
  const [discarding, setDiscarding] = useState<Row | null>(null)
  const [focusAfter, setFocusAfter] = useState<string | null>(null)
  const firstAction = useRef(new Map<string, HTMLElement | null>())
  const toggleRef = useRef<HTMLButtonElement | null>(null)

  useEffect(() => {
    if (focusAfter === null) return
    setFocusAfter(null)
    if (focusAfter === 'composer') focusComposer()
    else if (focusAfter === 'toggle') toggleRef.current?.focus()
    else {
      const el = firstAction.current.get(focusAfter)
      const target = el?.matches('button') ? el : el?.querySelector('button')
      target?.focus()
    }
  }, [focusAfter, focusComposer])

  const shown = expanded ? rows : narrow ? [] : rows.slice(-1)

  function confirmDiscard(): void {
    const row = discarding
    setDiscarding(null)
    if (!row) return
    const index = rows.findIndex((r) => r.id === row.id)
    const successor = rows[index + 1] ?? rows[index - 1]
    discardRow(row)
    // 4.9 focus: next item's first action, else previous, else the toggle, else the composer.
    setFocusAfter(successor ? (shown.some((r) => r.id === successor.id) || expanded ? successor.id : 'toggle') : 'composer')
  }

  function renderAction(row: Row, action: Action, first: boolean) {
    const ref = first ? (el: HTMLElement | null) => { firstAction.current.set(row.id, el) } : undefined
    if (action === 'copy') {
      return (
        <span key="copy" ref={ref}>
          <CopyButton value={row.text} label="Copy" />
        </span>
      )
    }
    const onClick = action === 'discard'
      ? () => setDiscarding(row)
      : action === 'move'
        ? () => moveHere(row, openKey)
        : () => { openRow(row, sessionOf); focusComposer() }
    return (
      <Button key={action} ref={ref as never} size="sm" variant={action === 'discard' ? 'ghost' : 'outline'} onClick={onClick}>
        {ACTION_LABEL[action]}
      </Button>
    )
  }

  return (
    // Spec 4.9: the region is role="status" (implicitly aria-live="polite"). No
    // explicit aria-live attribute: the modal Discard confirm hides the page
    // behind it, and an [aria-live] element is exempt from that hiding.
    <div role="status" className="flex flex-col gap-[var(--space-1)] empty:hidden" data-testid="unsent-notice">
      {unsupported && (
        <Callout variant="warning">
          <CalloutDescription>{SEND_TEXT.unsupportedBanner}</CalloutDescription>
        </Callout>
      )}
      {notStopped && (
        // Spec 4.7 / B-24: a persistent status above the box, never an alert or a toast.
        <Callout variant="warning" role="status">
          <CalloutDescription>{SEND_TEXT.notStopped}</CalloutDescription>
          <CalloutActions>
            <Button size="sm" variant="ghost" onClick={() => clearNotStopped(openKey)}>Dismiss</Button>
          </CalloutActions>
        </Callout>
      )}
      {recovery && (
        <Callout variant="info">
          {recovery.reason && <CalloutDescription>{recovery.reason}</CalloutDescription>}
          <CalloutDescription>{SEND_TEXT.restoredLine}</CalloutDescription>
          <CalloutActions>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                discardDraftInput(openKey)
                mirrorOpenDraft('restore')
              }}
            >
              Discard
            </Button>
          </CalloutActions>
        </Callout>
      )}
      {rows.length > 0 && (
        <div className="flex flex-col gap-[var(--space-1)]">
          <DisclosureRow ref={toggleRef} expanded={expanded} onExpandedChange={setExpanded} expandable>
            <span>{`${rows.length} not sent`}</span>
          </DisclosureRow>
          {shown.map((row) => (
            <Callout key={row.id} variant="info" icon={false}>
              <CalloutTitle>{row.title}</CalloutTitle>
              {row.text && <CalloutDescription>{preview(row.text)}</CalloutDescription>}
              {row.lines.map((line) => <CalloutDescription key={line}>{line}</CalloutDescription>)}
              <CalloutActions className="flex-wrap">
                {row.actions.map((action, i) => renderAction(row, action, i === 0))}
              </CalloutActions>
            </Callout>
          ))}
        </div>
      )}
      <ConfirmDialog
        open={discarding !== null}
        onOpenChange={(open) => { if (!open) setDiscarding(null) }}
        title="Discard this text?"
        description="The text and files of this item are removed from this page. This cannot be undone."
        confirmLabel="Discard"
        destructive
        onConfirm={confirmDiscard}
      />
    </div>
  )
}
