

import { syncChatForeground, useChatStore } from './chat/store'

import { registerChatSetReplaying, registerChatResetForReplay } from '@/store/session'

import { registerSyncChatForeground } from '@/store/session'

// Register callbacks with the session store to break circular imports.
registerChatSetReplaying((value) => useChatStore.getState().setReplaying(value))

registerChatResetForReplay((sessionId) => useChatStore.getState().resetSessionForReplay(sessionId))

registerSyncChatForeground(syncChatForeground)

// ── Split modules (2026-09-16) ──────────────────────────────────────────────────
//
// api.ts is a barrel: every name below was declared in this file before the
// split and is re-exported here, so no caller had to change. The modules own
// the code; this file owns the public surface.
export * from './chat/frames'
export * from './chat/messages'
export * from './chat/store'
export type { ChatMessage, ClientTruncatedResult, MediaAttachment, PositionedToolCall, RateLimitEventData, SessionChatState, SpanStep, SubagentSpan, SubagentSpanRunning, SubagentSpanTerminal } from './chat/types'

// F-S8: removed flat→bucket bidirectional sync subscriber.
// Tests now seed sessionsById directly (see resetStores() in test files).
// The subscriber was only needed for test scaffolding that set messages on the flat state;
// that pattern is no longer used.

// Detect direct useSessionStore.setState({activeSessionId: ...}) bypasses (used in tests).
// We intentionally do NOT auto-sync foreground here because it would overwrite flat fields
// (like isStreaming) that tests set directly before switching sessions. Foreground sync
// happens only through the store actions (setActiveSession, attachToSession, startNewSession).
// This comment documents the intentional gap for future maintainers.
