import React, { useCallback, useLayoutEffect, useMemo, useRef } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ThreadPrimitive } from '@assistant-ui/react'
import type { ChatMessage } from '@/store/chat'
import type { DelegationEvent } from '@/lib/delegationEvents.types'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderJudgeVerdictInThread } from '@/lib/toolVisibility'
import { ROOMS_ENABLED } from '@/lib/featureSwitches'
import { collectPairedReplyToIds } from './participantLabel'
import { useChatDelegationEvents } from './useChatDelegationEvents'
import { delegationEventsAtEnd, splitAnchoredDelegationEvents } from '@/lib/delegationEventPlacement'
import { DelegationEventLineList } from './DelegationEventLine'
import { RoomTrafficMessage } from './RoomTrafficEntry'
import { JudgeVerdictThreadCard } from './JudgeVerdictThreadCard'
import { SystemMessage, VirtualSystemMessageRow } from './SystemMessageRows'
import { placementOf, useSkillChipData, VirtualUserMessageRow, VirtualAssistantMessageRow, StreamingDelegation, VirtualizerAutoFollow, UserMessage, AssistantMessage } from './ChatScreen'
import { HistoryBoundary, HistoryReaderStatus } from './HistoryBoundary'
import { drawnHistoryRows, useHistoryViewport } from './useHistoryViewport'

function delegationEventsOutsideRow(events: readonly DelegationEvent[], message: ChatMessage): DelegationEvent[] {
  // The assistant row owns both inline and trailing events using its live-call
  // projection. Splitting again from the unbaked message would draw them twice.
  if (message.role === 'assistant' && message.type !== 'judge_verdict' && !(ROOMS_ENABLED && message.roomRef)) return []
  return splitAnchoredDelegationEvents(events, message.id, placementOf(message)).trailing
}

/**
 * A saved assistant record with nothing of its own to draw -- the content-less
 * turn-start bookkeeping row every agent turn writes -- is still a counted
 * history source group, but it draws no bubble (not even a name header).
 * Anything that can still show output (streaming, owned tool calls, media,
 * attachments, a truncation or a summary) keeps its row.
 */
function isSilentHistoryRecord(msg: ChatMessage, owners: Record<string, string> | undefined): boolean {
  // Not every Message variant declares these optional fields; absent is "none".
  const extra = msg as { summary?: string; attachments?: unknown[] }
  return msg.role === 'assistant' && msg.type !== 'judge_verdict' && !!msg.historyPosition && !msg.isStreaming && msg.status !== 'streaming'
    && !msg.content && !extra.summary && !msg.tool_calls?.length && !extra.attachments?.length && !msg.media?.length && !msg.spans?.length
    && !msg.truncated && !msg.providerFallbackNote && !Object.values(owners ?? {}).includes(msg.id)
}

/** Plain (non-virtualized) message list — fallback when ResizeObserver is unavailable. */
function PlainMessageList({ messages, liteMode, agentName }: { messages: ChatMessage[]; liteMode: boolean; agentName: string }) {
  const scrollContainerRef = useRef<HTMLDivElement>(null)
  const viewport = useHistoryViewport(scrollContainerRef, messages)
  const { skills, commandLabels } = useSkillChipData()
  // ADR-049 SD-C10: judge-verdict thread visibility (panel-only by default,
  // verbose-only inline) — same store read as every other verbose-gated row.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const delegationEvents = useChatDelegationEvents(sessionId)
  const toolCallOwners = useChatStore((s) => (sessionId ? s.sessionsById[sessionId]?.toolCallOwnerMessageId : undefined))
  const messageIds = useMemo(() => new Set(messages.map((message) => message.id)), [messages])
  const latestUserMessageId = [...messages].reverse().find((message) => message.role === 'user')?.id
  // F17 (1): the reply_to ids that have a reply bubble in the thread —
  // memoized on a pairing key so the Set's identity (and therefore the rows'
  // React.memo) only changes when a pairing actually appears or disappears.
  const replyPairKey = messages.map((m) => (m.replyToMessageId ? '1' : '0')).join('')
  const pairedReplyToIds = useMemo(() => collectPairedReplyToIds(messages), [replyPairKey])
  return (
    <div
      ref={scrollContainerRef}
      onScroll={viewport.onScroll}
      role="region" aria-label="Conversation history" tabIndex={-1} aria-busy={viewport.busy || undefined}
      data-testid="virtualized-message-list"
      className="flex-1 overflow-y-auto overscroll-contain [overflow-anchor:none] pt-[var(--space-3)] pb-[var(--space-2)]"
    >
      <div className="max-w-4xl mx-auto w-full">
        <HistoryReaderStatus />
        <HistoryBoundary action="older" average={viewport.average} />
        <div data-history-content>
        {messages.map((msg) => {
          // ADR-049 D2/SD-C10: a persisted `Message.type: judge_verdict`
          // entry never renders via the normal role-based rows — it is
          // panel-only by default (ActivityPanel's judge row, fed live by
          // the judge_verdict WS frame — see chat.ts), inline in the thread
          // ONLY under verbose chat.
          let row: React.ReactNode
          if (msg.type === 'judge_verdict') {
            row = shouldRenderJudgeVerdictInThread(verboseChatEnabled) && msg.verdict
              ? <JudgeVerdictThreadCard verdict={msg.verdict} />
              : null
          } else if (ROOMS_ENABLED && msg.roomRef) {
            // FR-202: collapsed group-chat traffic — hidden until WU-15 (stage 3), #1314.
            row = <RoomTrafficMessage message={msg} />
          } else if (msg.role === 'user') {
            row = (
              <VirtualUserMessageRow
                message={msg}
                skills={skills}
                commandLabels={commandLabels}
                agentName={agentName}
                latest={msg.id === latestUserMessageId}
              />
            )
          } else if (msg.role === 'system') {
            row = <VirtualSystemMessageRow message={msg} />
          } else if (!isSilentHistoryRecord(msg, toolCallOwners)) {
            row = <VirtualAssistantMessageRow message={msg} liteMode={liteMode} delegationEvents={delegationEvents} pairedReplyToIds={pairedReplyToIds} />
          }
          const trailing = delegationEventsOutsideRow(delegationEvents, msg)
          return (
            <div key={msg.historyPosition ?? msg.id} data-history-position={msg.historyPosition} data-history-anchor={msg.historyPosition ?? `live:${msg.id}`}>
              {row}
              <DelegationEventLineList events={trailing} />
            </div>
          )
        })}
        </div>
        <HistoryBoundary action="newer" average={viewport.average} />
        {!viewport.historical && <DelegationEventLineList events={delegationEventsAtEnd(delegationEvents, messageIds)} />}
      </div>
    </div>
  )
}

let _resizeObserverWarnEmitted = false

/**
 * Virtualized historical message list (AssistantUI constraint: the live
 * streaming message stays in ThreadPrimitive.Messages below for full context).
 */
export function VirtualizedMessageList({
  messages,
  liteMode,
  agentName,
}: {
  messages: ChatMessage[]
  liteMode: boolean
  agentName: string
}) {
  // Feature-detect ResizeObserver at render time (not module load) so test
  // environments that stub it in beforeEach are detected correctly.
  // iOS < 13.4 and some enterprise Android WebViews lack it — use the plain
  // fallback to avoid a white-screen crash.
  if (typeof ResizeObserver === 'undefined') {
    if (!_resizeObserverWarnEmitted) {
      _resizeObserverWarnEmitted = true
      console.warn('[chat] ResizeObserver unavailable — rendering full message list without virtualization')
    }
    return <PlainMessageList messages={messages} liteMode={liteMode} agentName={agentName} />
  }
  return <VirtualizedMessageListInner messages={messages} liteMode={liteMode} agentName={agentName} />
}

function VirtualizedMessageListInner({
  messages,
  liteMode,
  agentName,
}: {
  messages: ChatMessage[]
  liteMode: boolean
  agentName: string
}) {
  const isStreaming = useChatStore((s) => s.isStreaming)
  const scrollContainerRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const viewport = useHistoryViewport(scrollContainerRef, messages, virtualAnchorOffset)
  const { skills, commandLabels } = useSkillChipData()
  // ADR-049 SD-C10: judge-verdict thread visibility (panel-only by default, verbose-only inline).
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const delegationEvents = useChatDelegationEvents(sessionId)
  const textAtToolCallStart = useChatStore((s) => s.textAtToolCallStart)
  const toolCallOwners = useChatStore((s) => (sessionId ? s.sessionsById[sessionId]?.toolCallOwnerMessageId : undefined))
  const messageIds = useMemo(() => new Set(messages.map((message) => message.id)), [messages])
  const latestUserMessageId = [...messages].reverse().find((message) => message.role === 'user')?.id
  // F17 (1): the reply_to ids that have a reply bubble in the thread —
  // memoized on a pairing key so the Set's identity (and therefore the rows'
  // React.memo) only changes when a pairing actually appears or disappears.
  const replyPairKey = messages.map((m) => (m.replyToMessageId ? '1' : '0')).join('')
  const pairedReplyToIds = useMemo(() => collectPairedReplyToIds(messages), [replyPairKey])

  // Separate the live streaming message from completed history.
  const hasStreamingMessage = isStreaming && messages.length > 0 && messages[messages.length - 1]?.isStreaming
  const historicalMessages = useMemo(() => hasStreamingMessage ? messages.slice(0, messages.length - 1) : messages, [hasStreamingMessage, messages])
  const getItemKey = useCallback((index: number) => historicalMessages[index]?.historyPosition ?? historicalMessages[index]?.id ?? index, [historicalMessages])

  const virtualizer = useVirtualizer({
    count: historicalMessages.length,
    getScrollElement: () => scrollContainerRef.current,
    estimateSize: index => Math.max(1, drawnHistoryRows(historicalMessages[index], verboseChatEnabled)) * viewport.average,
    getItemKey,
    scrollMargin: viewport.prefix,
    overscan: 5,
    // measureElement default (getBoundingClientRect().height) is correct; no override needed.
  })

  virtualizer.shouldAdjustScrollPositionOnItemSizeChange = () => !viewport.historical
  useLayoutEffect(() => { virtualizer.measure() }, [virtualizer, viewport.average, viewport.widthGeneration, verboseChatEnabled])
  useLayoutEffect(() => {
    const kept = new Set(historicalMessages.map(message => message.historyPosition ?? message.id))
    for (const key of virtualizer.itemSizeCache.keys()) if (!kept.has(String(key))) virtualizer.itemSizeCache.delete(key)
  }, [virtualizer, historicalMessages])
  const virtualItems = virtualizer.getVirtualItems()

  function virtualAnchorOffset(position: string): number | undefined {
    const index = historicalMessages.findIndex(message => (message.historyPosition ?? `live:${message.id}`) === position)
    const item = index >= 0 ? virtualizer.measurementsCache[index] : undefined
    const scroller = scrollContainerRef.current
    const content = scroller?.querySelector<HTMLElement>('[data-history-content]')
    if (!item || !scroller || !content) return undefined
    const physicalPrefix = content.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop
    return physicalPrefix + item.start - virtualizer.options.scrollMargin
  }

  const rowForMessage = (msg: ChatMessage) => {
    // ADR-049 D2/SD-C10: see PlainMessageList's identical branch above.
    if (msg.type === 'judge_verdict') {
      if (!shouldRenderJudgeVerdictInThread(verboseChatEnabled) || !msg.verdict) return null
      return <JudgeVerdictThreadCard verdict={msg.verdict} />
    }
    // FR-202: collapsed group-chat traffic — hidden until WU-15 (stage 3), #1314.
    if (ROOMS_ENABLED && msg.roomRef) return <RoomTrafficMessage message={msg} />
    if (msg.role === 'user')
      return <VirtualUserMessageRow message={msg} skills={skills} commandLabels={commandLabels} agentName={agentName} latest={msg.id === latestUserMessageId} />
    if (msg.role === 'system') return <VirtualSystemMessageRow message={msg} />
    if (isSilentHistoryRecord(msg, toolCallOwners)) return null
    return <VirtualAssistantMessageRow message={msg} liteMode={liteMode} delegationEvents={delegationEvents} pairedReplyToIds={pairedReplyToIds} />
  }

  // Stick-to-bottom is delegated to assistant-ui's Viewport engine
  // (useThreadViewportAutoScroll + useOnResizeContent, via ThreadPrimitive
  // .Viewport). It attaches a ResizeObserver + a subtree MutationObserver to THIS
  // scroll container, so it follows ALL content growth — streamed tokens,
  // tool-call blocks, inline images/mermaid that render late, our virtualizer's
  // row add/remove, AND the live→virtualized finalize hand-off — and scrolls to
  // the bottom only while the user is already at the bottom. isAtBottom is keyed
  // off real scrollTop movement (not a touch-gesture heuristic), so a user
  // scroll-up correctly suspends auto-follow on touch/momentum — the failure mode
  // of the previous hand-rolled engine. We keep our custom virtualizer for row
  // rendering and only let Viewport own the scroll element + autoscroll. The
  // Viewport self-provides its viewport context; it runs within the chat's
  // AssistantRuntimeProvider + ThreadPrimitive.Root (the live ThreadPrimitive
  // .Messages below needs the runtime). The parent feature-detects ResizeObserver
  // and falls back to PlainMessageList where it is unavailable.
  //
  // VirtualizerAutoFollow (rendered inside the Viewport) covers the one growth the
  // engine can't see: the virtualizer grows the transcript by setting the spacer's
  // inline `style` height (row measurement, the live→virtualized finalize, and
  // late mermaid/image render), and the engine's MutationObserver deliberately
  // ignores style-only mutations (to avoid a translateY feedback loop). Without
  // this, the tail of a finalized/diagram message sits below the fold on slower
  // devices (iPad). It re-pins only when the engine still reports isAtBottom.
  return (
    <ThreadPrimitive.Viewport
      ref={scrollContainerRef}
      autoScroll={!viewport.historical}
      scrollToBottomOnRunStart={!viewport.historical}
      scrollToBottomOnInitialize={!viewport.historical}
      scrollToBottomOnThreadSwitch={!viewport.historical}
      onScroll={viewport.onScroll}
      role="region" aria-label="Conversation history" tabIndex={-1} aria-busy={viewport.busy || undefined}
      data-testid="virtualized-message-list"
      className="flex-1 overflow-y-auto overscroll-contain [overflow-anchor:none] pt-[var(--space-3)] pb-[var(--space-2)]"
      style={{ position: 'relative' }}
    >
      <VirtualizerAutoFollow scrollRef={scrollContainerRef} contentRef={contentRef} />
      <div ref={contentRef} className="max-w-4xl mx-auto w-full">
        <HistoryReaderStatus />
        <HistoryBoundary action="older" average={viewport.average} />
        <div
          data-history-content
          style={{
            height: `${virtualizer.getTotalSize()}px`,
            position: 'relative',
          }}
        >
          <div
            style={{
              position: 'absolute',
              top: 0,
              left: 0,
              width: '100%',
              transform: `translateY(${(virtualItems[0]?.start ?? viewport.prefix) - viewport.prefix}px)`,
            }}
          >
            {virtualItems.map((virtualRow) => {
              const msg = historicalMessages[virtualRow.index]
              if (!msg) return null
              return (
                <div
                  key={virtualRow.key}
                  data-index={virtualRow.index}
                  data-history-position={msg.historyPosition} data-history-anchor={msg.historyPosition ?? `live:${msg.id}`}
                  ref={virtualizer.measureElement}
                >
                  {rowForMessage(msg)}
                  <DelegationEventLineList
                    events={delegationEventsOutsideRow(delegationEvents, msg)}
                  />
                </div>
              )
            })}
          </div>
        </div>

        {/* Live streaming message — kept in ThreadPrimitive.Messages for full
            AssistantUI context (streaming primitives, registered tool UIs). */}
        {hasStreamingMessage && messages[messages.length - 1] && (
          <StreamingDelegation
            message={messages[messages.length - 1]}
            events={delegationEvents}
            snapshots={textAtToolCallStart}
            owners={toolCallOwners}
          >
            <div data-testid="streaming-message-anchor" data-history-position={messages[messages.length - 1].historyPosition} data-history-anchor={messages[messages.length - 1].historyPosition ?? `live:${messages[messages.length - 1].id}`}>
              <ThreadPrimitive.Messages>
                {({ message }) => {
                  const isLast = message.id === messages[messages.length - 1]?.id
                  if (!isLast) return null
                  const last = messages[messages.length - 1]
                  // FR-202 group-chat traffic — hidden until WU-15 (stage 3), #1314.
                  if (ROOMS_ENABLED && last?.roomRef) return <RoomTrafficMessage message={last} />
                  if (message.role === 'user') return <UserMessage />
                  if (message.role === 'system') return <SystemMessage />
                  return <AssistantMessage />
                }}
              </ThreadPrimitive.Messages>
            </div>
          </StreamingDelegation>
        )}
        <HistoryBoundary action="newer" average={viewport.average} />
        {!viewport.historical && <DelegationEventLineList events={delegationEventsAtEnd(delegationEvents, messageIds)} />}
      </div>
    </ThreadPrimitive.Viewport>
  )
}
