// #1428 step 2 (spec C r9, 4.4.3): the per-chat draft store.
//
// The store owns what the person typed and attached for each chat, keyed by
// the canonical chat key (a saved chat's id, or the new-chat key K of a "no
// chat yet" screen; an alias K -> S is written once when S is created for K).
// The session composer mirrors the open draft into the assistant-ui runtime;
// it never reads the runtime back as the source of truth (spec 4.5).
//
// It also holds the recovery state of spec 4.7-4.9 (the "not sent" and
// "may not have been delivered" items, the current-tab not-stopped notices
// and the typed command records) and the auth generation that a sign-out
// advances. Page lifetime only (Q5 = A): nothing here is persisted.

import { useSessionStore } from '@/store/session'
import type { SendOrigin } from '@/lib/api/generated/asyncapi-types'
import type { UseBoundStore, StoreApi } from 'zustand'
import type { ChatStore } from '@/store/chat/types'
import { NO_CHAT_KEY, initialDraftState } from '@/store/chat/draft-state'
import type { Draft, DraftFile, DraftState, LibraryPick, NewChatInfo, UnsentItem } from '@/store/chat/draft-state'

export * from '@/store/chat/draft-state'

type DraftPatch = Partial<DraftState> | ((state: DraftState) => Partial<DraftState>)

/**
 * The chat store that holds the draft state. The chat store binds itself
 * here once it is created (store.ts), so this module never imports the
 * store at load time: the session store reaches this module through
 * newChatKey.ts, and an import of the chat store from here would load the
 * chat store in the middle of the session store's own load.
 */
let host: UseBoundStore<StoreApi<ChatStore>> | null = null

export function bindDraftHost(store: UseBoundStore<StoreApi<ChatStore>>): void {
  host = store
}

function chat(): UseBoundStore<StoreApi<ChatStore>> {
  if (!host) throw new Error('drafts: the chat store has not bound the draft state yet')
  return host
}

/** Reader/writer of the draft state held in the chat store (page lifetime). */
export const useDraftStore = {
  getState: (): DraftState => chat().getState().draftState,
  setState: (patch: DraftPatch, replace = false): void => {
    chat().setState((state) => {
      const next = typeof patch === 'function' ? patch(state.draftState) : patch
      return { draftState: replace ? (next as DraftState) : { ...state.draftState, ...next } }
    })
  },
}

/** React subscription to a slice of the draft state. */
export function useDrafts<T>(selector: (state: DraftState) => T): T {
  return chat()((state) => selector(state.draftState))
}

const EMPTY_DRAFT: Draft = Object.freeze({ text: '', files: [], libraryPicks: [], updatedAt: 0, workspaceId: null }) as Draft

/** The canonical key: a K that has a created chat resolves to that chat. */
export function canonical(key: string | null | undefined): string {
  if (!key) return NO_CHAT_KEY
  return useDraftStore.getState().aliasByKey[key] ?? key
}

/** The key of the chat the person is looking at (spec 4.4.1). */
export function openChatKey(): string | null {
  const { activeSessionId, newChatKey } = useSessionStore.getState()
  if (activeSessionId && activeSessionId !== '__pending') return activeSessionId
  return newChatKey ?? null
}

/** The open chat's canonical draft key (never null; see NO_CHAT_KEY). */
export function openDraftKey(): string {
  return canonical(openChatKey())
}

/** A draft by (canonical) key. Missing drafts read as empty (A8). */
export function getDraft(key: string): Draft {
  return useDraftStore.getState().drafts[canonical(key)] ?? EMPTY_DRAFT
}

export function isDraftEmpty(draft: Draft | undefined): boolean {
  return !draft || (draft.text.length === 0 && draft.files.length === 0 && draft.libraryPicks.length === 0 && !draft.recovery)
}

/** Writes one draft; empty drafts are deleted (4.4.3). */
export function updateDraft(key: string, update: (draft: Draft) => Draft): void {
  const k = canonical(key)
  useDraftStore.setState((state) => {
    const next = update(state.drafts[k] ?? { ...EMPTY_DRAFT, workspaceId: state.newChats[k]?.workspaceId ?? null })
    const drafts = { ...state.drafts }
    if (isDraftEmpty(next)) delete drafts[k]
    else drafts[k] = { ...next, updatedAt: Date.now() }
    return { drafts }
  })
}

export function setDraftText(key: string, text: string): void {
  if (getDraft(key).text === text) return
  updateDraft(key, (draft) => ({ ...draft, text }))
}

export function addDraftFiles(key: string, files: File[]): void {
  if (files.length === 0) return
  updateDraft(key, (draft) => ({
    ...draft,
    files: [...draft.files, ...files.map((file) => ({ kind: 'local' as const, id: newLocalId(), file }))],
  }))
}

export function removeDraftFile(key: string, id: string): void {
  updateDraft(key, (draft) => ({ ...draft, files: draft.files.filter((file) => file.id !== id) }))
}

export function addLibraryPick(key: string, pick: Omit<LibraryPick, 'id'>): string {
  const id = `lib-${newLocalId()}`
  updateDraft(key, (draft) => ({ ...draft, libraryPicks: [...draft.libraryPicks, { id, ...pick }] }))
  return id
}

export function removeLibraryPick(key: string, id: string): void {
  updateDraft(key, (draft) => ({ ...draft, libraryPicks: draft.libraryPicks.filter((pick) => pick.id !== id) }))
}

export function clearLibraryPicks(key: string): void {
  if (getDraft(key).libraryPicks.length === 0) return
  updateDraft(key, (draft) => ({ ...draft, libraryPicks: [] }))
}

/** Clears the recovery line of a draft (the in-box Discard keeps nothing). */
export function clearDraftRecovery(key: string): void {
  if (!getDraft(key).recovery) return
  updateDraft(key, (draft) => ({ ...draft, recovery: undefined }))
}

let localIdCounter = 0
function newLocalId(): string {
  localIdCounter += 1
  return `f${Date.now().toString(36)}${localIdCounter}`
}

/** Merge rule of BR-7b: restored text first, then the newer text; identical text once. */
export function mergeRestoredText(restored: string, existing: string): string {
  if (!restored) return existing
  if (!existing || existing === restored) return restored
  if (existing.startsWith(`${restored}\n`)) return existing
  return `${restored}\n${existing}`
}

function sameFile(a: DraftFile, b: DraftFile): boolean {
  if (a.id === b.id) return true
  if (a.kind === 'local' && b.kind === 'local') return a.file === b.file
  if (a.kind === 'uploaded' && b.kind === 'uploaded') return a.ref === b.ref
  return false
}

/** Restores input into its origin draft once (spec 4.8, BR-7b). */
export function restoreIntoDraft(
  key: string,
  input: { text: string; files: DraftFile[]; libraryPicks: LibraryPick[] },
  reason: string | null,
): void {
  updateDraft(key, (draft) => ({
    ...draft,
    text: mergeRestoredText(input.text, draft.text),
    files: [...input.files.filter((file) => !draft.files.some((have) => sameFile(have, file))), ...draft.files],
    libraryPicks: [
      ...input.libraryPicks.filter((pick) => !draft.libraryPicks.some((have) => have.ref === pick.ref)),
      ...draft.libraryPicks,
    ],
    recovery: { reason },
  }))
}

// ── Runtime mirror (spec 4.5 rule 6) ────────────────────────────────────────

export type MirrorReason = 'key-transition' | 'submit-clear' | 'mount-sync' | 'slash-menu' | 'restore'
type MirrorWriter = (text: string, reason: MirrorReason) => void
let mirrorWriter: MirrorWriter | null = null

/** The mounted session composer registers the one runtime text writer. */
export function setDraftMirror(writer: MirrorWriter | null): void {
  mirrorWriter = writer
}

/** Writes the open draft's text into the composer runtime, and nothing else. */
export function mirrorOpenDraft(reason: MirrorReason): void {
  mirrorWriter?.(getDraft(openDraftKey()).text, reason)
}

// ── Keys and aliases ────────────────────────────────────────────────────────

export function registerNewChat(key: string, info: NewChatInfo): void {
  useDraftStore.setState((state) => ({ newChats: { ...state.newChats, [key]: info } }))
}

export function isNewChatKey(key: string): boolean {
  return key in useDraftStore.getState().newChats
}

/** Write-once alias K -> S; K's draft moves to S (4.4.1). */
export function aliasNewChat(key: string, sessionId: string): void {
  const state = useDraftStore.getState()
  if (state.aliasByKey[key] || key === sessionId) return
  const moving = state.drafts[key]
  useDraftStore.setState((s) => {
    const drafts = { ...s.drafts }
    delete drafts[key]
    return {
      aliasByKey: { ...s.aliasByKey, [key]: sessionId },
      keyBySession: { ...s.keyBySession, [sessionId]: key },
      drafts,
      unsent: s.unsent.map((item) => (item.key === key ? { ...item, key: sessionId } : item)),
    }
  })
  if (moving) restoreMoved(sessionId, moving)
}

function restoreMoved(sessionId: string, moving: Draft): void {
  updateDraft(sessionId, (draft) => ({
    ...draft,
    text: mergeRestoredText(moving.text, draft.text),
    files: [...moving.files, ...draft.files],
    libraryPicks: [...moving.libraryPicks, ...draft.libraryPicks],
    recovery: moving.recovery ?? draft.recovery,
  }))
}

/** The origin a submission from this (open) key carries (spec 4.2, 4.6.1). */
export function originForKey(key: string): SendOrigin {
  const state = useDraftStore.getState()
  if (key in state.newChats) return { kind: 'new_chat', new_chat_key: key }
  const k = state.keyBySession[key]
  if (k) return { kind: 'new_chat', new_chat_key: k }
  return { kind: 'chat', session_id: key }
}

/** The canonical draft key an origin belongs to. */
export function keyOfOrigin(origin: SendOrigin): string {
  if (origin.kind === 'new_chat') return canonical(origin.new_chat_key ?? null)
  return canonical(origin.session_id ?? null)
}

// ── Unsent items ────────────────────────────────────────────────────────────

let itemCounter = 0

export function addUnsentItem(item: Omit<UnsentItem, 'id' | 'createdAt'>): string {
  itemCounter += 1
  const id = `unsent-${itemCounter}`
  useDraftStore.setState((state) => ({ unsent: [...state.unsent, { ...item, id, createdAt: Date.now() }] }))
  return id
}

export function removeUnsentItem(id: string): void {
  useDraftStore.setState((state) => ({ unsent: state.unsent.filter((item) => item.id !== id) }))
}

/**
 * A later submission from a key that carries a restored item's text settles
 * that item: the person sent it again (spec 4.9 "Send it again").
 */
export function settleItemsSentAgain(key: string, sentText: string): void {
  const k = canonical(key)
  const { unsent, drafts } = useDraftStore.getState()
  const keep = unsent.filter((item) =>
    !(item.kind === 'not_sent' && canonical(item.key) === k && (item.text === '' || sentText.includes(item.text))))
  if (keep.length !== unsent.length) useDraftStore.setState({ unsent: keep })
  if (drafts[k]?.recovery && !keep.some((item) => item.kind === 'not_sent' && canonical(item.key) === k)) clearDraftRecovery(k)
}

// ── Not-stopped notices (spec 4.7) ──────────────────────────────────────────

type NoticeListener = (sessionId: string) => void
const noticeListeners = new Set<NoticeListener>()

/** Synchronous hook for B-27: the Stop machine disarms in the same step. */
export function onNotStoppedEstablished(listener: NoticeListener): () => void {
  noticeListeners.add(listener)
  return () => { noticeListeners.delete(listener) }
}

export function establishNotStopped(sessionId: string): void {
  useDraftStore.setState((state) => ({ notStopped: { ...state.notStopped, [sessionId]: true } }))
  for (const listener of noticeListeners) listener(sessionId)
}

export function clearNotStopped(sessionId: string): void {
  if (!useDraftStore.getState().notStopped[sessionId]) return
  useDraftStore.setState((state) => {
    const notStopped = { ...state.notStopped }
    delete notStopped[sessionId]
    return { notStopped }
  })
}

// ── Sign-out (4.4.3) ────────────────────────────────────────────────────────

/** Drops every draft, item, record, alias and notice and advances the auth generation. */
let authGeneration = 0

export function resetDraftsForSignOut(): void {
  authGeneration += 1
  useDraftStore.setState(initialDraftState(), true)
}

export function currentAuthGeneration(): number {
  return authGeneration
}

/** True while any non-empty draft or Unsent item exists (B-23 leave warning). */
export function hasUnsavedInput(): boolean {
  const { drafts, unsent } = useDraftStore.getState()
  return unsent.length > 0 || Object.values(drafts).some((draft) => draft.text.trim().length > 0 || draft.files.length > 0)
}
