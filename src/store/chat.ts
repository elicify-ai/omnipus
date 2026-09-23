

import { syncChatForeground, useChatStore } from './chat/store'

import { registerChatSetReplaying, registerChatResetForReplay } from '@/store/session'

import { registerSyncChatForeground } from '@/store/session'

import { registerChatLastAppliedSeq, registerChatPrepareForReplay } from '@/store/session'

// Register callbacks with the session store to break circular imports.
registerChatSetReplaying((value) => useChatStore.getState().setReplaying(value))

registerChatResetForReplay((sessionId) => useChatStore.getState().resetSessionForReplay(sessionId))

// #823 phase 2: the attach sites in session.ts need the session's applied-frame
// cursor and the wipe-or-keep decision that depends on it, both of which live
// in this store.
registerChatPrepareForReplay((sessionId) => useChatStore.getState().prepareSessionForReplay(sessionId))

registerChatLastAppliedSeq((sessionId) => useChatStore.getState().getLastAppliedSeq(sessionId))

registerSyncChatForeground(syncChatForeground)

// ── Split modules (2026-09-16) ──────────────────────────────────────────────────
//
// api.ts is a barrel: every name below was declared in this file before the
// split and is re-exported here, so no caller had to change. The modules own
// the code; this file owns the public surface.
export * from './chat/frames'
export * from './chat/messages'
export * from './chat/store'
export type { ChatMessage, ClientTruncatedResult, MediaAttachment, OutboundQueueItem, PositionedToolCall, QueuedOutboundMessage, RateLimitEventData, SessionChatState, SpanStep, SubagentSpan, SubagentSpanRunning, SubagentSpanTerminal } from './chat/types'

// F-S8: removed flat→bucket bidirectional sync subscriber.
// Tests now seed sessionsById directly (see resetStores() in test files).
// The subscriber was only needed for test scaffolding that set messages on the flat state;
// that pattern is no longer used.

// Detect direct useSessionStore.setState({activeSessionId: ...}) bypasses (used in tests).
// We intentionally do NOT auto-sync foreground here because it would overwrite flat fields
// (like isStreaming) that tests set directly before switching sessions. Foreground sync
// happens only through the store actions (setActiveSession, attachToSession, startNewSession).
// This comment documents the intentional gap for future maintainers.
