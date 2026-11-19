// #1428 step 2 (spec C r9, 4.4.3): the shape of the per-chat draft state.
// It lives in the chat store (field draftState) so it shares the chat store's
// page lifetime; drafts.ts holds every reader and writer. No imports from the
// store here, so store.ts can seed it without an import cycle.

import type { SendOrigin } from '@/lib/api/generated/asyncapi-types'

/** A file staged in a draft: still local, or already uploaded (spec 4.4.3). */
export type DraftFile =
  | { kind: 'local'; id: string; file: File }
  | { kind: 'uploaded'; id: string; ref: string; workspaceId: string | null; name: string; mime: string; previewUrl: string }

/** A workspace-library entry staged for the next message of this chat. */
export interface LibraryPick { // not-wire-format: client-side staged library entry
  id: string
  mediaId: string
  ref: string
  filename: string
  contentType: string
}

/** The in-box recovery line of a draft that holds restored input (spec 4.9). */
export interface DraftRecovery { // not-wire-format: client-side recovery marker
  reason: string | null
}

export interface Draft { // not-wire-format: client-side draft
  text: string
  files: DraftFile[]
  libraryPicks: LibraryPick[]
  updatedAt: number
  workspaceId: string | null
  recovery?: DraftRecovery
}

/**
 * One entry of the Unsent list (spec 4.9).
 * - not_sent: restored into its origin draft; listed from other chats.
 * - uncertain: a command whose connection closed before a receipt (list only).
 * - deleted: a confirmed 404 for its origin chat (list only).
 * - new_chat_pending: a first message for K handed over, awaiting confirmation.
 * - new_chat_unsaved: a first message for K that was never handed over.
 */
export type UnsentKind = 'not_sent' | 'uncertain' | 'deleted' | 'new_chat_pending' | 'new_chat_unsaved'

export interface UnsentItem { // not-wire-format: client-side recovery item
  id: string
  kind: UnsentKind
  /** Canonical origin key at the time the item was made. */
  key: string
  text: string
  files: DraftFile[]
  libraryPicks: LibraryPick[]
  reason: string | null
  agentId: string | null
  workspaceId: string | null
  createdAt: number
}

/** A typed `/stop-redirect`, `/cancel`, `/stop` or a Stop press (spec 4.7). */
export interface CommandRecord { // not-wire-format: client-side command record
  clientFrameId: string
  kind: 'redirect' | 'cancel' | 'stop'
  origin: SendOrigin
  originKey: string
  target: string
  text: string
  connection: unknown
  connectionGeneration: number | null
  turnId: string | null
  state: 'sent' | 'unconfirmed'
  /** Set once the target-running reconciliation showed the notice. */
  reconciled?: boolean
}

/** What K was opened for: its workspace and agent (spec 4.4.1). */
export interface NewChatInfo { // not-wire-format: client-side new-chat metadata
  workspaceId: string | null
  agentId: string | null
}

/** Stand-in key while no chat is open and no K exists (the blocked state, C5). */
export const NO_CHAT_KEY = '\u0000no-chat'

export interface DraftState { // not-wire-format: client-side draft state
  drafts: Record<string, Draft>
  aliasByKey: Record<string, string>
  keyBySession: Record<string, string>
  newChats: Record<string, NewChatInfo>
  unsent: UnsentItem[]
  notStopped: Record<string, true>
  commands: Record<string, CommandRecord>
}

export const initialDraftState = (): DraftState => ({
  drafts: {},
  aliasByKey: {},
  keyBySession: {},
  newChats: {},
  unsent: [],
  notStopped: {},
  commands: {},
})

