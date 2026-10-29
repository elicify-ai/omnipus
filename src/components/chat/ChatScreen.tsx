import React, { useContext, useEffect, useRef, useState, useCallback, useMemo } from 'react'
import { VirtualizedMessageList } from './ChatMessageList'
import { useQuery } from '@tanstack/react-query'
import {
  ThreadPrimitive,
  MessagePrimitive,
  ActionBarPrimitive,
  AuiIf,
  useMessage,
  useThreadViewportStore,
} from '@assistant-ui/react'
import {
  ArrowCounterClockwise,
  User,
  Copy,
  Check,
  Lightning,
} from '@phosphor-icons/react'
import { OMNIPUS_MARK_URL as OmnipusAvatar } from '@/lib/brandAssets'
import { Wordmark } from '@/components/shared/Wordmark'
import { GenericToolCall } from './tools/GenericToolCall'
import { resolvedToolPartStatus } from '@/lib/assistant-tool-status'
import { detectToolResultSentinels } from './tools/toolResultSentinels'
import { renderHistoricalToolCall } from './tools/HistoricalToolCallBlock'
import { classifySetGoalCall } from './tools/SetGoalToolUI'
import { RateLimitIndicator } from './RateLimitIndicator'
import { ProviderRetryIndicator } from './ProviderRetryIndicator'
import { GoalIndicator } from './GoalIndicator'
import { GoalPillTray } from './GoalPillTray'
import { AskUserQuestionThreadTail } from './AskUserQuestionCard'
import { SecretRequestThreadTail } from './SecretRequestCard'
import { ToolApprovalThreadTail } from '@/components/agents/ToolApprovalPanel'
import { OmnipusComposer } from './composer/OmnipusComposer'
import { MarkdownText } from './markdown-text'
import { CATCH_UP_SMOOTH, ThreadRevealContext } from './useOpenReveal'
import { ModelFooter } from './ModelFooter'
import { Button } from '@/components/ui/button'
import { isClearCommandText } from '@/lib/clearMarker'
import { ErrorDetailDisclosure } from './ErrorDetailDisclosure'
import { AgentReplyLabel, ParticipantMessageRow, useParticipantLabelParts } from './ParticipantLabels'
import { isSendChipHiddenByReply, replyHeaderParts } from './participantLabel'
import { useChatStore } from '@/store/chat'
import { stampToolCallOffset } from '@/store/chat/session'
import type { ChatMessage, PositionedToolCall, QueuedOutboundMessage } from '@/store/chat'
import type { DelegationEvent } from '@/lib/delegationEvents.types'
import { splitMessageParts } from '@/lib/messageParts'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { workspaceEntryBlocksSend } from '@/lib/nav/workspaceEntry'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall, shouldRenderContextWindowNoticeInThread } from '@/lib/toolVisibility'
import {
  liveSnapshotIds,
  splitAnchoredDelegationEvents,
  type DelegationPlacementMessage,
} from '@/lib/delegationEventPlacement'
import { DelegationEventLineList, DelegationInlineProvider, DelegationLiveTail, DelegationToolGroup, useClaimedCallIds } from './DelegationEventLine'
import { isGoalRecordEmpty } from '@/lib/goalSetupState'
import { messageSetsGoal } from '@/lib/goalCommandMessage'
import { getMessageStatusSuffix } from '@/lib/truncation'
import { GoalCommandMarker } from '@/components/chat/GoalCommandMarker'
import { GoalSetupFailureLine } from './tools/GoalSetupFailureLine'
import { fetchAgents, fetchCommands, fetchSkills } from '@/lib/api'
import { INLINE_TOOL_APPROVALS_ENABLED, SECRET_CARDS_ENABLED } from '@/lib/featureSwitches'
import type { SlashCommand, Skill } from '@/lib/api'
import {
  AgentStatusIndicator,
  feedMarkLabel,
  ReplyStatusFallbackProvider,
  showsFeedMark,
  useReplySlotPhase,
} from './AgentStatusIndicator'
import { useChatAgents } from './useChatAgents'
import { HistoricalMessageMarkdown } from './historical-markdown'
import { ChatImage } from './ChatImage'
import {
  AssistantMessageConnectionStatus,
  UnansweredUserMessageStatus,
  ChatConnectionNotice,
  RestartInterruptedNotice,
} from './ConnectionStatus'
import { RowDeliveryStatus } from './RowDeliveryStatus'
import { useChatHistory, HistoryOpenState } from './useChatHistory'
import { useConversationKind, useConversationSource } from './conversationSource'
import { ChatFileAttachment } from './ChatFileAttachment'

// #1449 FR-005: a group row never reads the foreground session's live state.
const NO_LIVE_TOOL_CALLS: ReturnType<typeof useChatStore.getState>['toolCalls'] = {}
const NO_LIVE_TOOL_ORDER: string[] = []
const NO_LIVE_TEXT_OFFSETS: Record<string, string> = {}

export function placementOf(message: ChatMessage): DelegationPlacementMessage {
  return {
    spans: message.spans,
    toolCalls: message.tool_calls as DelegationPlacementMessage['toolCalls'],
  }
}

// ── Skill-aware message content renderer (R2/F1/F7/F9) ───────────────────────

// renderSkillAwareContent — shared helper used by both the live AssistantUI
// UserMessage (which has MessagePrimitive.Parts) and VirtualUserMessageRow
// (historical/reloaded messages). Keeps the chip logic in one place so that
// reloaded messages display identically to live ones (F1).
// Exported for focused unit tests (F1 test requirement).
//
// F7: a leading /token that matches a known built-in command label (e.g.
// /new, /help, /model) must NOT produce a chip — built-ins win (D3). Hidden
// aliases (e.g. "clear" for "/new") must be excluded too — see
// commandLabelsWithAliases below. We receive the `commandLabels` list and
// use it to exclude those tokens.
//
// F9: the regex uses [\s\S]* so multi-line bodies (foo\nbar) are captured.
//
// Returns the JSX for the inner message bubble content; the caller wraps it in
// the appropriate outer container (MessagePrimitive.Root or a plain div).
export function renderSkillAwareContent(
  content: string | null,
  skills: Skill[],
  // Command label strings, e.g. ['/new', '/help', '/model'] — used to apply
  // the F7 built-in precedence gate. Pass commandLabelsWithAliases(commands)
  // so hidden aliases (e.g. "/clear") are excluded from skill-chip matching
  // too; callers that can't access the store pass [].
  commandLabels: string[],
  // Fallback plain-text renderer — used by live UserMessage (which has access
  // to MessagePrimitive.Parts) vs historical messages (which use a simple <p>).
  fallbackRenderer: () => React.ReactNode,
): React.ReactNode {
  // F9: [\s\S]* captures newlines; without the s/dotAll flag this was broken
  // for multi-line messages.
  const skillMatch = content?.match(/^\/(\S+)(?:\s+([\s\S]*))?$/)

  // F7: skip chip when the leading /token is a known built-in command.
  const isBuiltin = skillMatch
    ? commandLabels.some((lbl) => lbl.toLowerCase() === `/${skillMatch[1].toLowerCase()}`)
    : false

  const matchedSkill =
    skillMatch && !isBuiltin
      ? skills.find((s) => s.id.toLowerCase() === skillMatch[1].toLowerCase())
      : null

  if (matchedSkill && skillMatch) {
    // R2: compact skill indicator — show message text + chip, or chip alone
    return (
      <div className="rounded-xl px-[var(--space-3)] py-[var(--space-2-5)] text-[length:var(--type-body-compact-size)] leading-relaxed bg-[var(--color-surface-2)] text-[var(--color-secondary)] rounded-tr-sm flex flex-col gap-[var(--space-1)]">
        {skillMatch[2] ? (
          <>
            <p className="whitespace-pre-wrap break-words">{skillMatch[2]}</p>
            <span className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
              <Lightning size={10} weight="fill" className="text-[var(--color-accent)]" />
              skill: {matchedSkill.name}
            </span>
          </>
        ) : (
          <span className="flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]">
            <Lightning size={12} weight="fill" className="text-[var(--color-accent)]" />
            {matchedSkill.name}
          </span>
        )}
      </div>
    )
  }

  return fallbackRenderer()
}

// M3/F7: build the commandLabels list renderSkillAwareContent gates on, from
// a fetched SlashCommand[] — including hidden aliases (e.g. "clear" for
// "/new") prefixed with "/", so a typed "/clear" is excluded from
// skill-chip matching exactly like the canonical "/new" label is. Without
// this, a hidden alias would fall through the F7 gate and (if a skill
// happened to share that id) render as a skill chip instead of plain text.
export function commandLabelsWithAliases(commands: SlashCommand[]): string[] {
  const labels: string[] = []
  for (const c of commands) {
    labels.push(c.label)
    for (const alias of c.aliases ?? []) labels.push(`/${alias}`)
  }
  return labels
}

// ── Message components ────────────────────────────────────────────────────────

// Exported for focused unit tests of the LIVE render path (the
// VirtualUserMessageRow precedent a few hundred lines down). The two
// components extract their text differently — this one from AssistantUI's
// `message.content` parts array, the row from a store `ChatMessage.content`
// string — so a test that only drives the row cannot see a marker regression
// here: see ChatScreen.goalCommandMarker.live.test.tsx.
export function UserMessage() {
  const message = useMessage()
  const storeMessage = useChatStore((state) => state.messagesById[message.id])
  const activeAgentId = useSessionStore((state) => state.activeAgentId)
  const { data: skills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    queryFn: () => fetchSkills(),
    staleTime: 60_000,
  })
  const { data: commands = [] } = useQuery<SlashCommand[]>({
    queryKey: ['commands', 'web'],
    queryFn: () => fetchCommands('web'),
    staleTime: 60_000,
  })
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const agentName = agents.find((agent) => agent.id === activeAgentId)?.name ?? 'Omnipus'

  const content = (() => {
    const parts = message.content
    if (!parts || parts.length === 0) return null
    const textPart = parts.find((p: { type: string }) => p.type === 'text')
    if (!textPart || typeof (textPart as { text?: string }).text !== 'string') return null
    return (textPart as { text: string }).text
  })()

  const commandLabels = commandLabelsWithAliases(commands)

  // UAT defect B: a `/goal …` message is the ONE thing about an uncompiled
  // goal that the transcript actually persists (the gateway appends the raw
  // inbound text before the command rewrite; replay applies no slash-command
  // filter). Marking it keeps a goal visible in the thread after a reload
  // that lands before — or without — any live `goal_status` frame.
  const setsGoal = messageSetsGoal(content)

  return (
    <MessagePrimitive.Root
      data-testid="user-message"
      data-message-id={message.id}
      data-goal-command={setsGoal ? 'true' : undefined}
      className="group flex gap-[var(--space-2-5)] px-[var(--space-3)] py-[var(--space-2-5)] flex-row-reverse"
    >
      <div className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center bg-[var(--color-accent)]/20 text-[var(--color-accent)]">
        <User size={14} weight="bold" />
      </div>
      <div className="flex flex-col items-end gap-[var(--space-1)] max-w-[85%] min-w-0">
        {setsGoal && <GoalCommandMarker />}
        {renderSkillAwareContent(content, skills, commandLabels, () => (
          <div className="rounded-xl px-[var(--space-3)] py-[var(--space-2-5)] text-[length:var(--type-body-compact-size)] leading-relaxed bg-[var(--color-surface-2)] text-[var(--color-secondary)] rounded-tr-sm">
            <MessagePrimitive.Parts>
              {({ part }) => {
                if (part.type !== 'text') return null
                return <p className="whitespace-pre-wrap break-words">{part.text}</p>
              }}
            </MessagePrimitive.Parts>
          </div>
        ))}
        {storeMessage?.deliveryStatus && (storeMessage.firstSendStatus || storeMessage.deliveryStatus !== 'sending') && (
          <RowDeliveryStatus
            content={storeMessage.content}
            state={storeMessage.deliveryStatus}
            firstSendStatus={storeMessage.firstSendStatus}
            agentName={agentName}
            latest
            isClearCommand={isClearCommandText(storeMessage.content)}
            onRetry={() => useChatStore.getState().resendMessage(storeMessage.id)}
            onGenerateAgain={() => useChatStore.getState().generateFirstSendAgain(storeMessage.id)}
          />
        )}
        {(!storeMessage?.firstSendStatus || storeMessage.firstSendStatus === 'saved') && <UnansweredUserMessageStatus messageId={message.id} agentName={agentName} />}
      </div>
    </MessagePrimitive.Root>
  )
}

// Caps a context-specific thinking label (a bash `description`) to a single
// line and ~48 chars — long enough to be informative, short enough to read
// as a status word rather than a wrapped paragraph.
const THINKING_LABEL_MAX_CHARS = 48

/** Trims `value` to its first line and caps it at THINKING_LABEL_MAX_CHARS, appending an ellipsis when cut. */
function truncateThinkingLabel(value: string): string {
  const firstLine = value.split(/\r?\n/, 1)[0]?.trim() ?? ''
  if (firstLine.length === 0) return ''
  if (firstLine.length <= THINKING_LABEL_MAX_CHARS) return firstLine
  return `${firstLine.slice(0, THINKING_LABEL_MAX_CHARS).trimEnd()}…`
}

// Maps the first token of a background `bash` command to a short verb
// phrase for the thinking indicator (deriveBashThinkingLabel's step 2).
// Deliberately closed/exact-match — an unrecognized command falls through
// to the generic "Running a command…" rather than guessing.
const BASH_COMMAND_VERBS: Record<string, string> = {
  git: 'Running git…',
  npm: 'Running npm…',
  npx: 'Running npm…',
  pnpm: 'Running npm…',
  yarn: 'Running npm…',
  go: 'Running Go…',
  python: 'Running a script…',
  python3: 'Running a script…',
  curl: 'Fetching…',
  wget: 'Fetching…',
  docker: 'Running Docker…',
  make: 'Building…',
  bash: 'Running a script…',
  sh: 'Running a script…',
}

/**
 * Derives the thinking-indicator label for an in-progress `bash` call that
 * is hidden from the thread (background dispatch, or the poll/read
 * sub-case on an already-running background session — toolVisibility.ts).
 * NEVER renders the raw `command` string (length + secret-leak risk) —
 * only the call's own `description` (capped to one line/~48 chars) or a
 * verb mapped from the command's first token.
 */
function deriveBashThinkingLabel(args: Record<string, unknown> | undefined): string {
  const description = typeof args?.description === 'string' ? args.description : ''
  const truncatedDescription = truncateThinkingLabel(description)
  if (truncatedDescription) return truncatedDescription

  const command = typeof args?.command === 'string' ? args.command.trim() : ''
  if (command) {
    const firstToken = command.split(/\s+/)[0] ?? ''
    const verbKey = firstToken.split('/').pop() ?? firstToken
    return BASH_COMMAND_VERBS[verbKey] ?? 'Running a command…'
  }

  return 'Working in the background…'
}

// `deriveDelegateThinkingLabel` stays deleted. A hidden `delegate` call
// (every action, unless verbose chat is on) has no tool-specific label.
// Its running status still selects Working, with the generic working copy.

/**
 * Finds the LAST tool-call part in a live message's `content` whose live
 * status (looked up in the store's resolved ToolCall record, keyed by
 * toolCallId — the same lookup FallbackToolUI uses) is still 'running', and
 * — only when that call is hidden from the thread per toolVisibility.ts's
 * shouldRenderToolCall — derives a specific, stable label for it.
 *
 * Returns null when: the tool is visible (its own chip already shows progress),
 * it's a hidden `delegate` call (no specific label — the event line carries
 * that), it's ToolSearch, any other hidden tool with no specific-label rule,
 * or nothing is currently running. This selects copy only, not the phase:
 * runningToolNamesFromLiveContent supplies the correlated execution signal. Defensive: never throws — an unexpected
 * message/part shape supplies no specific copy via the null return,
 * exactly like "nothing found".
 */
function deriveHiddenRunningToolLabel(
  content: unknown,
  storeToolCalls: Record<string, { status?: string }>,
  verboseChatEnabled: boolean,
): string | null {
  try {
    if (!Array.isArray(content)) return null
    for (let i = content.length - 1; i >= 0; i--) {
      const part = content[i] as
        | { type?: string; toolCallId?: string; toolName?: string; args?: unknown }
        | undefined
      if (!part || part.type !== 'tool-call') continue
      const { toolCallId, toolName } = part
      if (typeof toolCallId !== 'string' || typeof toolName !== 'string') continue

      const liveStatus = storeToolCalls[toolCallId]?.status
      if (liveStatus !== 'running') continue // not the current in-progress step

      const args = part.args as Record<string, unknown> | undefined
      if (shouldRenderToolCall(toolName, args, verboseChatEnabled, false)) {
        // Visible — its own chip already communicates progress.
        return null
      }

      if (toolName === 'bash') {
        return deriveBashThinkingLabel(args)
      }
      return null // ToolSearch, any delegate action, or another hidden tool without specific copy.
    }
    return null
  } catch {
    return null
  }
}

/** Plain/replay twin of deriveHiddenRunningToolLabel. The virtual row has no AssistantUI parts. */
function derivePositionedHiddenRunningLabel(
  calls: readonly { tool: string; params?: Record<string, unknown>; status?: string }[],
  verboseChatEnabled: boolean,
): string | null {
  try {
    for (let i = calls.length - 1; i >= 0; i--) {
      const call = calls[i]
      if (!call || call.status !== 'running') continue
      if (shouldRenderToolCall(call.tool, call.params, verboseChatEnabled, false)) return null
      if (call.tool === 'bash') return deriveBashThinkingLabel(call.params)
      return null
    }
    return null
  } catch {
    return null
  }
}

/**
 * Goal-aware override for the thinking-indicator label (operator-reported
 * UX fix, 2026-09-08 — see goalSetupState.ts's file doc comment for the
 * full "17 minutes of a silent spinner" repro). While a session's goal is
 * ACTIVE and its record is still EMPTY (isGoalRecordEmpty), the generic
 * rotating pool is replaced with a purposeful, goal-specific phrase:
 * "Setting acceptance criteria" while `set_goal` itself is the currently-
 * running step, "Framing your goal" otherwise (including the very first
 * beat, before any tool call has started). Returns null once the record is
 * populated — this override is scoped to the empty-record window only, and
 * the caller falls through to its existing label logic (deriveHiddenRunningToolLabel
 * / the plain rotating pool) in that case.
 *
 * `runningToolNames` is deliberately a plain string array rather than a
 * shared "call" shape — the live path derives it from AssistantUI message
 * parts, the replay path from `PositionedToolCall[]`, and those two shapes
 * have nothing else in common worth unifying for this one check.
 */
function deriveGoalAwareThinkingLabel(runningToolNames: string[], goalRecordEmpty: boolean): string | null {
  if (!goalRecordEmpty) return null
  return runningToolNames.includes('set_goal') ? 'Setting acceptance criteria' : 'Framing your goal'
}

/**
 * Reads a producing message's goal association off an AssistantUI message
 * (FR-039) — carried in `metadata.custom.goalId` by `convertMessage`
 * (src/lib/omnipus-runtime.ts). Returns undefined for anything that is not a
 * non-empty string, so an unknown/malformed value stays NEUTRAL.
 */
function messageGoalIdFromAssistantUi(message: {
  metadata?: { custom?: { goalId?: unknown } }
}): string | undefined {
  const value = message.metadata?.custom?.goalId
  return typeof value === 'string' && value.length > 0 ? value : undefined
}

/** One execution discriminator for both the live and plain/replay reply slots.
 * Visibility and label selection must not change whether a tool is running. */
function runningToolNamesForReply(
  calls: readonly Pick<PositionedToolCall, 'tool' | 'status'>[],
): string[] {
  return calls.filter((call) => call.status === 'running').map((call) => call.tool)
}

/** Adapts live AssistantUI parts to the same execution records used by replay.
 * Status comes from the store record correlated by toolCallId; an unexpected
 * content shape yields no calls, not a guessed execution phase. */
function runningToolNamesFromLiveContent(
  content: unknown,
  storeToolCalls: Record<string, Pick<PositionedToolCall, 'status'>>,
): string[] {
  if (!Array.isArray(content)) return []
  const calls: Pick<PositionedToolCall, 'tool' | 'status'>[] = []
  try {
    for (const part of content) {
      const p = part as { type?: string; toolCallId?: string; toolName?: string } | undefined
      if (!p || p.type !== 'tool-call') continue
      if (typeof p.toolCallId !== 'string' || typeof p.toolName !== 'string') continue
      const call = storeToolCalls[p.toolCallId]
      if (call) calls.push({ tool: p.toolName, status: call.status })
    }
  } catch {
    return []
  }
  return runningToolNamesForReply(calls)
}

// Custom text renderer with streaming cursor.
// Wrapped in ErrorBoundary because MessagePartPrimitive.InProgress throws
// "MessagePartText can only be used inside text or reasoning message parts"
// when AssistantUI calls this component for a tool-call-only message.
class TextPartErrorBoundary extends React.Component<{ children: React.ReactNode }, { hasError: boolean }> {
  constructor(props: { children: React.ReactNode }) {
    super(props)
    this.state = { hasError: false }
  }
  static getDerivedStateFromError() { return { hasError: true } }
  componentDidCatch(error: Error) {
    // Only suppress the known AssistantUI error; log everything else.
    if (!error.message?.includes('MessagePartText')) {
      console.error('[TextPartErrorBoundary] Unexpected error:', error)
    }
  }
  render() { return this.state.hasError ? null : this.props.children }
}

function AssistantTextPart() {
  // A reply already in progress when the chat opened must show its text so
  // far, not re-type it from empty (the open flicker): catch-up pacing while
  // the opened conversation is covered, normal pacing after the reveal.
  const revealed = useContext(ThreadRevealContext)
  return (
    <TextPartErrorBoundary>
      <div>
        <MarkdownText smooth={revealed ? true : CATCH_UP_SMOOTH} />
      </div>
    </TextPartErrorBoundary>
  )
}

// Motion follows correlated execution; all work renderers share the status
// episode owned above their split, including the identity-loading fallback.
function InlineThinkingIndicator({ agentId }: { agentId: string | null }) {
  const message = useMessage()
  const turnId = useChatStore((state) => state.messagesById[message.id]?.turnId ?? null)
  const isRunning = message.status?.type === 'running'
  const storeToolCalls = useChatStore((s) => s.toolCalls)
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  // Operator-reported UX fix, 2026-09-08: goal-aware override, checked
  // BEFORE the hidden-tool label — see deriveGoalAwareThinkingLabel's doc
  // comment. FR-039 (DEL-F39–40): the override joins the PRODUCING message's
  // own goal_id (carried on the AssistantUI message's metadata.custom) to the
  // exact keyed criteria in `goalPills` — never the session's latest-goal
  // scalar. Subscribing to `goalPills` keeps the label reactive when a
  // `goal_status` frame changes the record. An unknown association (no
  // goalId) is neutral.
  const goalPills = useChatStore((s) => s.goalPills)
  const goalId = messageGoalIdFromAssistantUi(message)
  const goalRecordEmpty = isGoalRecordEmpty(goalId, goalPills)
  const runningToolNames = runningToolNamesFromLiveContent(message.content, storeToolCalls)
  const goalLabel = goalRecordEmpty
    ? deriveGoalAwareThinkingLabel(runningToolNames, true)
    : null
  const toolLabel = deriveHiddenRunningToolLabel(message.content, storeToolCalls, verboseChatEnabled)
  const phase = useReplySlotPhase({
    streaming: isRunning,
    hasRunningTool: runningToolNames.length > 0,
    toolLabel,
    goalLabel,
    idleEligible: false,
  })

  if (!showsFeedMark(phase)) return null
  const label = feedMarkLabel(phase)
  return (
    <React.Suspense
      fallback={<AgentStatusIndicator phase={phase.kind} label={label} turnId={turnId} />}
    >
      <ResolvedAgentMark agentId={agentId} phase={phase.kind} label={label} turnId={turnId} />
    </React.Suspense>
  )
}

function ResolvedAgentMark({
  agentId,
  phase,
  label,
  turnId,
}: {
  agentId: string | null
  phase: 'thinking' | 'working' | 'waiting' | 'unavailable'
  label: string | null
  turnId: string | null
}) {
  const agents = useChatAgents()
  const agent = agents.find((item) => item.id === agentId)
  return (
    <AgentStatusIndicator
      phase={phase}
      label={label}
      turnId={turnId}
      figure={agent?.figure}
      role={agent?.role}
      color={agent?.color}
      name={agent?.name ?? ''}
    />
  )
}

// Fallback tool UI for tools without a registered makeAssistantToolUI component.
// ToolCallMessagePartProps passes: toolCallId, toolName, args, result, status,
// isError.
//
// issue #617: this is the LIVE path for every tool with no registered UI —
// all system.*, every MCP tool, skills — so it is the widest single surface
// the bug touched, and it was the last one still broken. It must pass isError
// explicitly. Without it GenericToolCall falls back to
// `status.type === 'incomplete' || !!error`, and BOTH halves are false for an
// ordinary failure: the part carries a result so its status is 'complete',
// and pkg/gateway/websocket.go deliberately leaves the frame's `error` unset
// when Result still holds the plain text ("setting Error would ship the
// identical text twice in one frame"). The result rendered a green "Done".
//
// F3 (second review wave on branch fix/615-617-618-hardening): `isError={
// props.isError ?? liveCall?.status === 'error'}` below — the PROP wins, the
// store is only a fallback for when the prop is undefined. This is the
// correct precedence, not an inversion: FallbackToolUI is mounted ONLY via
// AssistantUI's `MessagePrimitive.Parts` `tools.Fallback` slot, which itself
// renders ONLY inside the LIVE `ThreadPrimitive.Messages` (the "Live
// streaming message" block near the bottom of this file) — replayed/
// historical messages render through VirtualAssistantMessageRow instead,
// a wholly separate code path that never touches this component. On that
// single live surface, omnipus-runtime.ts's convertMessage always
// constructs every tool-call part with `isError: resolved.status ===
// "error"` — a defined boolean, never `undefined` (see pushHistoryParts's
// and buildContentParts's own construction sites) — so `props.isError` is
// never undefined here in practice; the `?? liveCall?.status === 'error'`
// fallback is inert on this surface. It is kept anyway as a defensive
// default (this prop is optional in the type, and a future AssistantUI
// internal change to when/how Fallback parts are constructed could
// plausibly omit it) — not because today's live path ever needs it.
function FallbackToolUI(props: {
  toolCallId: string
  toolName: string
  args: unknown
  result: unknown
  status: import('@assistant-ui/react').MessagePartStatus
  artifact?: unknown
  isError?: boolean
}) {
  const storeToolCalls = useChatStore((s) => s.toolCalls)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  // Operator-reported UX fix, 2026-09-08: see GoalSetupFailureLine.tsx's
  // file doc comment. FR-039: the failure line is gated on the PRODUCING
  // message's own goal_id (the AssistantUI message this tool part belongs
  // to) joined to the exact keyed criteria in `goalPills` — never the
  // session's latest-goal scalar.
  const message = useMessage()
  const goalPills = useChatStore((s) => s.goalPills)
  const goalId = messageGoalIdFromAssistantUi(message)
  const liveCall = storeToolCalls[props.toolCallId]
  const isError = props.isError ?? liveCall?.status === 'error'

  // Narrow override: a FAILED call that would otherwise render visibly
  // (respects toolVisibility.ts's hidden-tool contract — a failed
  // background delegate/bash call is still never surfaced here), outside
  // verbose chat (which already shows the raw call in full), while a goal
  // is active with an empty record. `set_goal` itself never reaches this
  // Fallback (it has its own registered makeAssistantToolUI), but the
  // exclusion is kept explicit rather than assumed.
  if (
    isError &&
    !verboseChatEnabled &&
    props.toolName !== 'set_goal' &&
    isGoalRecordEmpty(goalId, goalPills) &&
    shouldRenderToolCall(props.toolName, props.args as Record<string, unknown> | undefined, false, true)
  ) {
    return (
      <GoalSetupFailureLine
        toolName={props.toolName}
        result={liveCall?.result ?? props.result}
        error={liveCall?.error}
      />
    )
  }

  return (
    <GenericToolCall
      toolName={props.toolName}
      args={props.args}
      result={liveCall?.result ?? props.result}
      status={resolvedToolPartStatus(props.status, props.artifact)}
      isError={isError}
      error={liveCall?.error}
      durationMs={liveCall?.duration_ms}
      sessionId={activeSessionId ?? ''}
    />
  )
}

function AssistantMessageRetryButton() {
  const message = useMessage()
  const sendMessage = useChatStore((s) => s.sendMessage)
  const messages = useChatStore((s) => s.messages)
  const isStreaming = useChatStore((s) => s.isStreaming)

  const status = message.status?.type
  // AssistantUI maps our store's 'error' and 'interrupted' statuses both to
  // { type: 'incomplete' } via buildMessageStatus in omnipus-runtime.ts.
  const isErrorOrIncomplete = status === 'incomplete'
  const hasUserMessage = messages.some((m) => m.role === 'user')

  if (!isErrorOrIncomplete || isStreaming || !hasUserMessage) return null

  function handleRetry() {
    const lastUserMsg = [...messages].reverse().find((m) => m.role === 'user')
    if (lastUserMsg) {
      sendMessage(lastUserMsg.content)
    }
  }

  return (
    <Button
      variant="ghost"
      onClick={handleRetry}
      aria-label="Retry — resend the last user message"
      className="h-auto gap-[var(--space-1)] px-[var(--space-2)] py-[var(--space-1)] rounded font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-error)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]"
      title="Retry — resend the last user message"
    >
      <ArrowCounterClockwise size={11} />
      <span>Retry</span>
    </Button>
  )
}

// Renders inline media attachments (images, files) attached to the assistant
// message this component is mounted inside. Keyed by message id so each
// message bubble only shows its own media — not the most recent assistant's,
// which would double-render the screenshot on every follow-up turn.
function InlineMedia() {
  const message = useMessage()
  const messages = useChatStore((s) => s.messages)
  const storeMsg = messages.find((m) => m.id === message.id)

  if (!storeMsg?.media?.length) return null

  return (
    <div className="flex flex-col gap-[var(--space-2)] mt-[var(--space-2)]">
      {storeMsg.media.map((m, i) =>
        m.type === 'image' ? (
          <div key={`${m.url}-${i}`} className="max-w-2xl">
            <ChatImage
              src={m.url}
              alt={m.caption || m.filename}
              filename={m.filename}
            />
            {m.caption && (
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] px-[var(--space-2)] py-[var(--space-1)]">{m.caption}</p>
            )}
          </div>
        ) : (
          // FR-081: the same file card as the settled rows.
          <ChatFileAttachment key={`${m.url}-${i}`} media={m} />
        ),
      )}
    </div>
  )
}

/**
 * Returns true when the result is the marshal-error sentinel from replay.go.
 * Mirrors GenericToolCall.tsx's/ToolCallBadge.tsx's detector of the same
 * name. Kept as a local duplicate (not imported) because it is NOT one of
 * the three sentinels the shared ./tools/toolResultSentinels module covers
 * (that module is deliberately scoped to the three STRUCTURED-FAILURE
 * sentinels — delegation-denied, file-exists refusal, permission-denied; a
 * marshal error is a serialization glitch, not a domain failure) — see that
 * module's own file header.
 */
function isMarshalErrorSentinel(value: unknown): boolean {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as Record<string, unknown>)['_marshal_error'] === 'string'
  )
}

/**
 * Mirrors the per-tool-call visibility gate GenericToolCall/BashOutput/
 * ToolCallBadge each apply at render time (Fix 3, 2026-07-16). Used ONLY to
 * decide whether a message has any VISIBLE content, so the ghost-bubble
 * empty-placeholder / bare-Copy-bar logic below doesn't unmask a bubble
 * whose only content is a hidden delegate call or background-bash dispatch
 * (the D-fix UAT defect). Every delegate action is hidden in the non-verbose
 * thread (spec D2); the grey event line is the surface, so a message that is
 * only a delegation still exercises this path. Verbose chat shows every
 * delegate badge, and those calls then count as visible content.
 * Reuses the same sentinel-detection semantics and the shouldRenderToolCall
 * classifier those components call directly.
 *
 * F2 (second review wave on branch fix/615-617-618-hardening): the three
 * structured-failure sentinels (delegation-denied, file-exists refusal,
 * permission-denied) are now detected via the SAME shared module
 * (./tools/toolResultSentinels's detectToolResultSentinels) GenericToolCall.
 * tsx and ToolCallBadge.tsx use — not a local re-derivation. Previously this
 * function carried its own local copy of all three detectors, and the
 * permission-denied one had drifted: a loose 3-field check (`error`,
 * `message`, `reason`) against GenericToolCall's real detector, which uses
 * the generated Zod schema's `.strict()` validation requiring all FIVE
 * fields (including `tool` and `permanent`). A pre-#618 transcript payload
 * with only three fields used to count as an error for this visibility gate
 * while GenericToolCall/ToolCallBadge did NOT recognize it as a sentinel —
 * same object, two verdicts, in the one place a comment claimed there was
 * only one. Importing the real detector here (rather than re-deriving it)
 * makes that divergence structurally impossible: this file, GenericToolCall,
 * and ToolCallBadge now all agree by construction, at the cost of the same
 * false-negative-until-parsed 3-field payload now failing to force
 * visibility HERE too — matching what the other two renderers would show
 * for it (a raw JSON blob, not a sentinel chip), rather than the reverse
 * (all three recognizing it). isMarshalErrorSentinel stays a local
 * duplicate, not part of the shared module — see its own doc comment above
 * for why.
 *
 * `errorFlag` is the outcome signal each call site already carries under a
 * different name (a baked ToolCall's `.error` string, or a live/streaming
 * ToolCallMessagePart's `.isError` boolean) — see the two call sites below.
 * This is a close approximation, not a byte-for-byte replay of every
 * renderer's exact gate: e.g. BashOutputBlock's own live `isError` source is
 * AssistantUI's per-part MessagePartStatus, which isn't available at the
 * message.content-array level this runs at — the live ToolCallMessagePart's
 * own `isError` field is the closest available proxy. Every tool this
 * project's registered live tool UIs render OTHER than through
 * GenericToolCall (Fallback)/BashOutputUI never self-hides regardless of
 * tool name (only ToolSearch/delegate/bash are ever hidden — see
 * shouldRenderToolCall's switch), so calling shouldRenderToolCall uniformly
 * for every tool name here is accurate for those surfaces too, without
 * needing to special-case them.
 */
function wouldToolCallBeVisible(
  tool: string,
  params: Record<string, unknown> | undefined,
  result: unknown,
  errorFlag: boolean,
  verboseChatEnabled: boolean,
): boolean {
  if (tool === 'set_goal') {
    // ADR-082 D9: set_goal renders via its own dedicated UI
    // (SetGoalCardBlock), which pre-empts GenericToolCall on both paths.
    // "Visible" here must mean "that UI actually renders something", so
    // this consults the SAME decision table the block renders from
    // (classifySetGoalCall, review S4) — a record shows the card, with the
    // raw call added below it in verbose chat. Non-verbose failures show
    // their quiet failure line; a present-but-unparseable result shows a
    // chip (or the raw call in verbose chat). Only a still-running
    // call (no result yet) or a completed call with no result at all is
    // hidden, so a bubble consisting solely of one still shows the
    // ThinkingIndicator rather than a blank shell.
    return (
      classifySetGoalCall({
        args: params,
        result,
        isRunning: false,
        isError: errorFlag,
        verboseChatEnabled,
      }) !== 'hidden'
    )
  }
  const isError = errorFlag || isMarshalErrorSentinel(result) || detectToolResultSentinels(result).any
  return shouldRenderToolCall(tool, params, verboseChatEnabled, isError)
}

// ADR-091 D7/D10: `resolveSpanAgentType` and `SubagentSpansRenderer` (the
// per-message thread rendering of subagent spans via SubagentBlock) are
// deleted — a child's own frames never arrive in the parent's bucket any
// more (I-4), so the thread has no span content left to render at any
// verbosity. A delegation's status now lives only in the side panel
// (ActivityPanel.tsx, fed by useRunningActivity.ts reading message.spans
// directly) and in the child's own session, opened via its
// `childSessionId`.

// Bug 2 (UAT, ADR-040): the name label is the per-message agent
// (`message.agentId ?? activeAgentId`), never the picker selection. The
// avatar column is gone (names-only bubbles). The label's title keeps that
// same identity so a mid-turn picker change cannot repaint the row.

// ── Standalone message row components (virtualizer) ──────────────────────────
// Render ChatMessage from props (no AssistantUI context) for use by the virtualizer.

/** Inline retry button for user messages that failed to send (#253b). */
/** Standalone user message row for the virtualizer. */
// useSkillChipData fetches the skills + web commands ONCE per message list (not
// per row) for the R2 skill-chip detection. Cache hits are free (staleTime 60s).
export function useSkillChipData(): { skills: Skill[]; commandLabels: string[] } {
  const { data: skills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    queryFn: () => fetchSkills(),
    staleTime: 60_000,
  })
  const { data: commands = [] } = useQuery<SlashCommand[]>({
    queryKey: ['commands', 'web'],
    queryFn: () => fetchCommands('web'),
    staleTime: 60_000,
  })
  return { skills, commandLabels: commandLabelsWithAliases(commands) }
}

// F1/R2: skills + commandLabels are passed in by the parent list (fetched ONCE
// per list, not per row) so a 1000-row virtualized transcript doesn't spawn a
// query subscription per row. The shared renderSkillAwareContent makes reloaded
// messages show the skill chip identically to live messages (FR-013).
// Exported for focused unit tests (F1 test requirement — reload path).
export function VirtualUserMessageRow({
  message,
  skills,
  commandLabels,
  agentName = 'Omnipus',
  latest = false,
}: {
  message: ChatMessage
  skills: Skill[]
  commandLabels: string[]
  agentName?: string
  latest?: boolean
}) {
  // See UserMessage above — the virtualized row is the other half of the same
  // rendering and must carry the same goal marker, or the trace would appear
  // and disappear depending on which path renders the thread. Both paths are
  // covered: this one by ChatScreen.goalCommandMarker.test.tsx, the live one
  // by ChatScreen.goalCommandMarker.live.test.tsx.
  const setsGoal = messageSetsGoal(message.content)
  // #1449: a group row carries no session send state (the group's send
  // outcome lives in its message box notice area, ADR A3.2).
  const sessionRow = useConversationKind() === 'session'

  // U8/F15/F17: a user entry carrying a server-stamped participant is a
  // LEFT-side message from another participant (channel input, peer request) —
  // never the web user's own bubble, which is exactly why the backend stamps
  // no participant on it. Same row component every list path routes through.
  if (message.participant) return <ParticipantMessageRow message={message} />

  return (
    <article
      data-testid="user-message"
      data-message-role="user"
      data-message-id={message.id}
      data-status={message.status}
      data-goal-command={setsGoal ? 'true' : undefined}
      className="group flex gap-[var(--space-2-5)] px-[var(--space-3)] py-[var(--space-2-5)] flex-row-reverse"
    >
      <div className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center bg-[var(--color-accent)]/20 text-[var(--color-accent)]">
        <User size={14} weight="bold" />
      </div>
      <div className="flex flex-col items-end gap-[var(--space-1)] max-w-[85%] min-w-0">
        {setsGoal && <GoalCommandMarker />}
        {/* Attachments the user sent — image thumbnails + colour-coded file
            cards, shown above the text like ChatGPT. */}
        {message.media && message.media.length > 0 && (
          <div className="flex flex-wrap gap-[var(--space-2)] justify-end">
            {message.media.map((m, i) =>
              m.type === 'image' ? (
                // Sent images (screenshots) get the full ChatImage treatment — hover
                // toolbar (copy/share/download/enlarge) + click-to-enlarge lightbox.
                <ChatImage key={`${m.url}-${i}`} src={m.url} alt={m.filename} filename={m.filename} className="max-w-[240px]" />
              ) : (
                // FR-002: a keyboard-operable download with an inline error and Retry.
                <ChatFileAttachment key={`${m.url}-${i}`} media={m} />
              ),
            )}
          </div>
        )}
        {message.content.trim().length > 0 && (
          renderSkillAwareContent(
            message.content,
            skills,
            commandLabels,
            () => (
              <div className="rounded-xl rounded-tr-sm bg-[var(--color-surface-2)] px-[var(--space-3)] py-[var(--space-2-5)] text-[length:var(--type-body-compact-size)] leading-relaxed text-[var(--color-secondary)]">
                <p className="whitespace-pre-wrap break-words">{message.content}</p>
              </div>
            ),
          )
        )}
        {sessionRow && message.deliveryStatus && (message.firstSendStatus || message.deliveryStatus !== 'sending') && (
          <RowDeliveryStatus
            content={message.content}
            state={message.deliveryStatus}
            firstSendStatus={message.firstSendStatus}
            agentName={agentName}
            latest={latest}
            isClearCommand={isClearCommandText(message.content)}
            onRetry={() => useChatStore.getState().resendMessage(message.id)}
            onGenerateAgain={() => useChatStore.getState().generateFirstSendAgain(message.id)}
          />
        )}
        {sessionRow && (!message.firstSendStatus || message.firstSendStatus === 'saved') && <UnansweredUserMessageStatus messageId={message.id} agentName={agentName} />}
      </div>
    </article>
  )
}

/** Copy-to-clipboard helper for static assistant messages. */
function StaticCopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(text).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }, [text])
  return (
    <Button
      variant="ghost"
      aria-label="Copy message"
      onClick={handleCopy}
      className="h-auto gap-[var(--space-1)] px-[var(--space-2)] py-[var(--space-1)] rounded font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]"
      title="Copy message"
    >
      {copied ? (
        <>
          <Check size={11} weight="bold" className="text-[var(--color-success)]" />
          <span>Copied!</span>
        </>
      ) : (
        <>
          <Copy size={11} />
          <span>Copy</span>
        </>
      )}
    </Button>
  )
}

/**
 * Standalone assistant message row for the virtualizer (historical / completed messages).
 *
 * Perf (gate 5 MEDIUM): wrapped in React.memo. Prop-identity correctness —
 * `message` is a safe memo key: the chat store (src/store/chat.ts) keeps
 * every message in an Immer-backed `messagesById` map, and every mutation
 * path goes through `produce()` (or an equivalent single-key replace inside
 * `withBucket`/`applyMessageArray`). Immer's structural sharing means a
 * `produce()` call that touches ONE message only allocates a new object
 * identity for THAT message (and the containing `messagesById`/array
 * wrappers) — every sibling message keeps its exact prior object reference.
 * `getMessages()`/`bucketToForeground()` (chat.ts) rebuild the top-level
 * `messages` ARRAY on every store update (so the array itself always has a
 * new identity), but each ELEMENT of that array is the same reference as
 * before unless that specific message was baked/edited. Since this
 * component is invoked per-row with its own `message` element (never the
 * whole array), React.memo's default shallow prop comparison — Object.is on
 * `message` and `liteMode` — correctly skips re-rendering any row whose
 * underlying message object is referentially unchanged, even though the
 * parent's `messages` array (and therefore the JSX call each render) is
 * rebuilt every time. This is exactly the "replaces message objects by
 * reference on bake" contract the memo key relies on — verified against
 * chat.ts's `produce`/`withBucket`/`applyMessageArray`/`getMessages`, not
 * assumed.
 */
export const VirtualAssistantMessageRow = React.memo(function VirtualAssistantMessageRow({
  message,
  liteMode,
  delegationEvents,
  pairedReplyToIds,
}: {
  message: ChatMessage
  liteMode: boolean
  delegationEvents: readonly DelegationEvent[]
  /** F17 (1): reply_to ids that have a reply bubble in the thread — the send_message chips paired with one are hidden. */
  pairedReplyToIds: ReadonlySet<string>
}) {
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  // #1449 FR-005 / ADR A3.2: a group row never shows the live tool calls or
  // status of whichever chat session is in the foreground.
  const liveRow = useConversationKind() === 'session'
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const activeSessionId = useSessionStore((s) => (liveRow ? s.activeSessionId : null))
  const toolCalls = useChatStore((s) => (liveRow ? s.toolCalls : NO_LIVE_TOOL_CALLS))
  const toolCallOrder = useChatStore((s) => (liveRow ? s.toolCallOrder : NO_LIVE_TOOL_ORDER))
  const textAtToolCallStart = useChatStore((s) => (liveRow ? s.textAtToolCallStart : NO_LIVE_TEXT_OFFSETS))
  const toolCallOwners = useChatStore((s) =>
    activeSessionId ? s.sessionsById[activeSessionId]?.toolCallOwnerMessageId : undefined,
  )
  // Fix 2 (user-approved 2026-07-16): same thread-gating rule as the live
  // path's SubagentSpansRenderer, applied here for the historical/virtualized
  // render tree. This component is React.memo'd, so the selector MUST be
  // called unconditionally at the top with every other hook (Rules of
  // Hooks) — reading getState() instead would silently freeze this row's
  // gating at whatever the preference was on its last actual re-render.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  // Operator-reported UX fix, 2026-09-08: goal-aware thinking-indicator
  // label + goal-setup failure line (replay/historical path) — see
  // deriveGoalAwareThinkingLabel's and GoalSetupFailureLine's doc comments.
  // FR-039 (DEL-F39–40): the row joins this message's OWN goal_id
  // (`message.goalId`) to the exact keyed criteria in `goalPills` — never the
  // session's latest-goal scalar. Subscribing to `goalPills` keeps the row
  // reactive when a `goal_status` frame changes the record. An unknown
  // association (no goalId) is neutral.
  const goalPills = useChatStore((s) => s.goalPills)
  const goalRecordEmpty = isGoalRecordEmpty(message.goalId, goalPills)
  const isLastAssistant = useChatStore((s) => {
    if (!liveRow) return false
    const list = s.messages
    for (let i = list.length - 1; i >= 0; i--) {
      if (list[i]?.role === 'assistant') return list[i]?.id === message.id
    }
    return false
  })

  // Replay finalizes saved bubbles even when their producing turn is still
  // running. Keep the existing indicator on the last confirmed active reply,
  // without waiting for another token or marking older replies as running.
  const recoveredRunning = useChatStore((state) => {
    if (!isLastAssistant || !activeSessionId || !message.turnId) return false
    const bucket = state.sessionsById[activeSessionId]
    return bucket?.isStreaming === true && !bucket.isReplaying && bucket.activeTurnId === message.turnId
  })
  const replyStreaming = !!message.isStreaming || recoveredRunning

  const messageAgentId = message.agentId ?? activeAgentId
  const agent = agents.find((a) => a.id === messageAgentId)
  const agentDisplayName = agent?.name ?? (messageAgentId || null)
  // U8/F15/F17 — the reply header ("Ava → Alice · Telegram") replaces the
  // plain author label on a reply bubble; the label parts (workspaces +
  // current workspace) come from the same cached lookups the participant
  // rows use.
  const { workspaces: labelWorkspaces, currentWorkspaceId } = useParticipantLabelParts()
  const replyHeader = replyHeaderParts(message, agents, currentWorkspaceId, labelWorkspaces)
  // ADR-087 D1 — the muted footer suffix: "(interrupted)" (FR-21, unchanged)
  // or "(cut off at the output limit)" for a max_output_tokens truncation.
  // getMessageStatusSuffix owns the precedence (interrupted/cancelled wins).
  const statusSuffix = getMessageStatusSuffix(message)
  // #1449 FR-004 (FR-170/FR-217): a group answer posted after a pause keeps its muted mark.
  const afterPause = message.roomMarks?.afterPause === true

  // Render media attachments.
  const mediaItems = message.media ?? []

  // PlainMessageList receives raw messages, bypassing the AssistantUI live
  // projection. Pending calls live separately until turn-end bake: project
  // only this reply's calls, without mutating the stored message. Unowned
  // legacy calls follow the runtime adapter's last-assistant fallback.
  const positionedToolCalls = useMemo(() => {
    const calls = new Map<string, PositionedToolCall>()
    for (const call of (message.tool_calls ?? []) as PositionedToolCall[]) {
      const live = toolCalls[call.id]
      calls.set(call.id, live
        ? stampToolCallOffset(call.id, live, textAtToolCallStart, call.textOffset)
        : call)
    }
    for (const id of toolCallOrder) {
      if (calls.has(id)) continue
      const call = toolCalls[id]
      const owner = toolCallOwners?.[id]
      if (call && (owner === message.id || (owner === undefined && isLastAssistant))) {
        calls.set(id, stampToolCallOffset(id, call, textAtToolCallStart, undefined))
      }
    }
    // F17 (1): a send_message chip whose reply_to names a request some reply
    // bubble in the thread answers is hidden — the reply bubble IS that
    // chip's display. Render-only: the persisted transcript keeps every call.
    return [...calls.values()].filter((tc) => !isSendChipHiddenByReply(tc.tool, tc.params, pairedReplyToIds))
  }, [message.id, message.tool_calls, toolCalls, toolCallOrder, textAtToolCallStart, toolCallOwners, isLastAssistant, pairedReplyToIds])

  // Live results must invalidate the split even without a message bake.
  const messageParts = useMemo(
    () => splitMessageParts(message.content ?? '', positionedToolCalls),
    [message.content, positionedToolCalls],
  )
  const delegationSplit = splitAnchoredDelegationEvents(delegationEvents, message.id, {
    spans: message.spans,
    toolCalls: positionedToolCalls,
  })

  // D-fix: this row is also used by PlainMessageList (the ResizeObserver-
  // unavailable fallback), which — unlike VirtualizedMessageListInner — does
  // NOT split the live streaming placeholder out into ThreadPrimitive.Messages;
  // it renders every message, including an in-flight one, through this
  // component. Without this guard, a message with isStreaming:true and empty
  // content would show the same "avatar + Copy, nothing to copy" broken bubble
  // as the live path (see AssistantMessage's showEmptyPlaceholder).
  //
  // Fix 3 (2026-07-16): emptiness is judged on VISIBLE content only — a
  // message whose only content is a hidden delegate status poll or
  // background-bash dispatch (default thread policy, toolVisibility.ts) must
  // not render an empty bubble with a bare Copy action bar (the D-fix UAT
  // defect resurfacing once the thread started hiding those by default). A
  // `delegate` call is hidden unless verbose chat is on (the event line is
  // the normal-thread surface), so a message that is only a delegation does
  // trigger this guard — the line renders beside the row, not inside it.
  // visibleToolCalls is also what's actually rendered below — hoisted here
  // so both the emptiness check and the render loop share one computation
  // instead of drifting into two different notions of "visible". Subagent
  // spans no longer factor in at all (ADR-091 D7/D10): a child's own frames
  // never arrive in the parent's bucket any more, so the thread has no span
  // content to render or to judge emptiness against — the side panel
  // (ActivityPanel.tsx, fed by useRunningActivity.ts reading message.spans
  // directly) is where a delegation's status lives now.
  const hasContent = !!message.content?.trim().length
  // F4: ordinary failures can carry status:'error' with no error string.
  // Visibility must use the same resolved outcome as the tool renderer,
  // otherwise a failed ToolSearch shares its row with a ghost Thinking mark.
  const visibleToolCalls = positionedToolCalls.filter((tc) =>
    wouldToolCallBeVisible(tc.tool, tc.params, tc.result, tc.status === 'error' || !!tc.error, verboseChatEnabled),
  )
  const hasVisibleToolCalls = visibleToolCalls.length > 0
  const hasMedia = mediaItems.length > 0
  const isEmptyContent = !hasContent && !hasVisibleToolCalls && !hasMedia
  const showEmptyPlaceholder = !!message.isStreaming && isEmptyContent
  // D-fix, terminal-empty variant (integrate, kept through the 2026-09-15
  // merge next to release's goal-aware label): a message that finished
  // normally yet holds nothing to show has nothing to copy either.
  const isTerminalEmpty =
    !message.isStreaming && isEmptyContent && message.status !== 'interrupted' && message.status !== 'error'
  // Operator-reported UX fix, 2026-09-08: same override as the live path's
  // InlineThinkingIndicator, applied to the historical/virtualized "still
  // streaming" placeholder (PlainMessageList renders an in-flight message
  // through THIS row too — see the D-fix comment on hasContent above).
  const runningToolNames = runningToolNamesForReply(positionedToolCalls)
  const emptyPlaceholderLabel = goalRecordEmpty
    ? deriveGoalAwareThinkingLabel(runningToolNames, true)
    : null
  const positionedToolLabel = derivePositionedHiddenRunningLabel(positionedToolCalls, verboseChatEnabled)
  const replyPhase = useReplySlotPhase({
    streaming: replyStreaming,
    hasRunningTool: runningToolNames.length > 0,
    toolLabel: positionedToolLabel,
    goalLabel: emptyPlaceholderLabel,
    idleEligible: isLastAssistant && !replyStreaming,
  })
  // 1b (founder UI feedback 2026-10-11): while THIS reply is streaming, the
  // inline indicator never disappears — streamed text, a visible tool card,
  // or a reconnecting link change only the phrase and the motion, never the
  // mark's presence (previously the mark was gated to the empty-placeholder /
  // waiting / unavailable windows and vanished the moment real content
  // showed). A settled row paints only a still-pending approval/question:
  // no "Idle" word and no "Unavailable/reconnecting" word on a finished
  // reply — connection state has its own surfaces
  // (ChatConnectionStatusLine / UnavailableChatNotice).
  const showMark = showsFeedMark(replyPhase) && (replyPhase.kind === 'waiting' || replyStreaming)

  const row = (
    <article
      data-testid="assistant-message"
      data-message-role="assistant"
      data-message-id={message.id}
      data-status="complete"
      className="group flex gap-[var(--space-2-5)] px-[var(--space-3)] py-[var(--space-2-5)]"
    >
      <div className="flex flex-col gap-[var(--space-1)] max-w-[85%] min-w-0 flex-1">
        {(replyHeader || agentDisplayName) && (
          // Founder UI feedback 2026-10-11: the label is the small agent mark
          // beside the name (or the U8 reply header) — and nothing else. The
          // old inline "Idle" status word next to the name is gone.
          <AgentReplyLabel agent={agent} name={agentDisplayName ?? ''} header={replyHeader} />
        )}
        <div className="text-[length:var(--type-body-compact-size)] leading-relaxed text-[var(--color-secondary)]">
          {/* Media attachments */}
          {!showEmptyPlaceholder && mediaItems.length > 0 && (
            <div className="flex flex-col gap-[var(--space-2)] mb-[var(--space-2)]">
              {mediaItems.map((m, i) =>
                m.type === 'image' ? (
                  <div key={`${m.url}-${i}`} className="max-w-2xl">
                    <ChatImage
                      src={m.url}
                      alt={m.caption || m.filename}
                      filename={m.filename}
                    />
                    {m.caption && (
                      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] px-[var(--space-2)] py-[var(--space-1)]">{m.caption}</p>
                    )}
                  </div>
                ) : (
                  // FR-002: a keyboard-operable download with an inline error and Retry.
                  <ChatFileAttachment key={`${m.url}-${i}`} media={m} />
                ),
              )}
            </div>
          )}

          {/* Text + tool calls, INTERLEAVED via splitMessageParts (bug fix):
              this used to render `message.content` as one whole
              HistoricalMessageMarkdown block, then map ALL of
              storeToolCallIds after it — so a finished/replayed message
              always showed "all text, then all tool calls", collapsing
              whatever interleaved order the assistant actually streamed in.
              splitMessageParts (src/lib/messageParts.ts) uses each baked
              call's `textOffset` (stamped by the store — see
              PositionedToolCall in src/store/chat.ts) to rebuild that order:
              text slices and tool calls now alternate in the same sequence
              they happened live. Calls with no offset (legacy bakes /
              reconnect edge — offset genuinely unknown) fall back to the
              old "after all text" placement, exactly as before.

              Each text slice renders as its OWN HistoricalMessageMarkdown
              block rather than one block for the whole message — markdown
              is parsed per-slice, so a slice boundary that falls mid-
              paragraph (because a tool call started there) breaks that
              paragraph across two blocks. This is not a regression: a slice
              boundary only ever falls where a tool call started, and the
              LIVE renderer (omnipus-runtime.ts's pushHistoryParts /
              buildContentParts) already treats that exact point as a
              text-segment boundary — so the finished view now matches what
              was on screen while it was still streaming, instead of
              (incorrectly) rendering the paragraph as one unbroken block. */}
          {messageParts.map((part, idx) => {
            if (part.type === 'text') {
              return <HistoricalMessageMarkdown key={`text-${idx}`} content={part.text} />
            }
            const callId = part.call.id
            const tc = toolCalls[callId] ?? part.call
            return <div key={callId} data-history-tool-row={callId}>{renderHistoricalToolCall({
              tc,
              callId,
              inlineEvents: delegationSplit.byCall.get(callId),
              activeSessionId,
              verboseChatEnabled,
              goalRecordEmpty,
              liteMode,
            })}</div>
          })}
          {/* Inline indicator — 1b (founder UI feedback 2026-10-11): rendered
              AFTER the parts, pinned at the bottom of the running reply, in
              the position where the next text/tool will appear. While
              message.isStreaming it never unmounts (see showMark above). */}
          {showMark && (
            <AgentStatusIndicator
              phase={replyPhase.kind}
              label={feedMarkLabel(replyPhase)}
              turnId={message.turnId}
              figure={agent?.figure}
              role={agent?.role}
              color={agent?.color}
              name={agent?.name ?? ''}
            />
          )}
        </div>

        {/* Action bar — always visible at reduced opacity, fully opaque on hover.
            Suppressed while showEmptyPlaceholder (D-fix): nothing has streamed
            in yet, so there is nothing to copy. Also suppressed for the
            terminal-empty variant (isTerminalEmpty): the turn finished with
            nothing to show, so there is still nothing to copy. */}
        {!showEmptyPlaceholder && !isTerminalEmpty && (
          <div className="flex items-center gap-[var(--space-1)] opacity-70 hover:opacity-100 transition-opacity duration-150">
            <StaticCopyButton text={message.content ?? ''} />
          </div>
        )}

        {/* Status suffix — "(interrupted)" (FR-21) or ADR-087 D1's
            "(cut off at the output limit)"; getMessageStatusSuffix decides
            which (or neither), never both. */}
        {statusSuffix && (
          <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] italic px-[var(--space-1)]">{statusSuffix}</span>
        )}
        {afterPause && (
          <span data-testid="after-pause-mark" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] italic px-[var(--space-1)]">after pause</span>
        )}

        {/* ADR-051 — verbose-only "Technical details" disclosure (replay
            path). Extracted verbatim to ErrorDetailDisclosure.tsx; same mount
            gate, same DOM. */}
        <ErrorDetailDisclosure message={message} verboseChatEnabled={verboseChatEnabled} />

        {/* Per-turn model record (FR-014). Mirrors MessageItem.tsx so
            replay sessions show the same model footer as the live
            AssistantUI render. */}
        {message.role === 'assistant' && <ModelFooter model={message.model} />}
        {liveRow && <AssistantMessageConnectionStatus messageId={message.id} agentName={agentDisplayName ?? 'Omnipus'} />}
      </div>
    </article>
  )
  return (
    <>
      {row}
      <DelegationEventLineList events={delegationSplit.trailing} />
    </>
  )
})

/**
 * Re-pins the scroll container to the bottom when the VIRTUALIZER grows the
 * transcript via the spacer's inline `style` height — row measurement, the
 * live→virtualized finalize hand-off, and late-rendering mermaid/images. The
 * assistant-ui Viewport autoscroll observes content via a subtree MutationObserver
 * that deliberately ignores style-only mutations (so the virtualizer's translateY
 * can't cause a feedback loop), which means it never sees that height growth and
 * the tail of a message can sit below the fold — most visibly on slower devices
 * (iPad) and with diagrams that render after the stream completes. A ResizeObserver
 * on the content fires on the height growth (and NOT on transforms, which don't
 * change size), so we re-pin — but only when the engine still reports we're at the
 * bottom (reused via the Viewport store), so a user who scrolled up to read is
 * never yanked down. MUST render inside ThreadPrimitive.Viewport to read its store.
 */
export function VirtualizerAutoFollow({
  scrollRef,
  contentRef,
}: {
  scrollRef: React.RefObject<HTMLDivElement | null>
  contentRef: React.RefObject<HTMLDivElement | null>
}) {
  const viewportStore = useThreadViewportStore()
  // #1449 FE-1: follow the conversation on screen — a group never replays,
  // and its reader mode is its own, not the last visited session's.
  const source = useConversationSource()
  const sessionList = source.kind === 'session'
  const isReplaying = useChatStore((s) => s.isReplaying) && sessionList
  const sourceRef = useRef(source)
  sourceRef.current = source
  const pin = useRef<number | null>(null)
  const userScrolledUp = useRef(false)
  useEffect(() => {
    const el = scrollRef.current
    const content = contentRef.current
    if (!el || !content) return undefined
    let raf = 0
    let pointerActive = false
    const observeScroll = () => {
      if (pointerActive && pin.current !== null && el.scrollTop < pin.current) userScrolledUp.current = true
      if (el.scrollHeight - el.clientHeight - el.scrollTop <= 1) userScrolledUp.current = false
    }
    const onWheel = (event: WheelEvent) => { if (isReplaying && event.deltaY < 0) userScrolledUp.current = true }
    const onKeyDown = (event: KeyboardEvent) => {
      if (isReplaying && (['ArrowUp', 'PageUp', 'Home'].includes(event.key) || (event.key === ' ' && event.shiftKey))) userScrolledUp.current = true
    }
    const onPointerDown = () => { pointerActive = isReplaying }
    const onPointerUp = () => { pointerActive = false }
    const viewMode = () => (sourceRef.current.kind === 'session' ? useChatStore.getState().history.viewMode : sourceRef.current.read()?.history.viewMode)
    const canFollow = () => viewMode() !== 'historical' && !userScrolledUp.current && (viewportStore.getState().isAtBottom || el.scrollTop === pin.current)
    // Keep explicit upward intent across the replay-completion effect. Row
    // measurement can move scrollTop too, so position changes alone are not
    // evidence of a user scroll. Never prevent any native input here.
    el.addEventListener('scroll', observeScroll, { passive: true })
    el.addEventListener('wheel', onWheel, { passive: true })
    el.addEventListener('keydown', onKeyDown)
    el.addEventListener('pointerdown', onPointerDown, { passive: true })
    window.addEventListener('pointerup', onPointerUp, { passive: true })
    const follow = () => {
      // A measurement can grow the spacer after a successful pin and dispatch
      // a scroll event before ResizeObserver: the engine then reports "not at
      // bottom" even though scrollTop never moved. Preserve that known pin,
      // not an old offset after the user has actually scrolled away from it.
      if (!canFollow()) return
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => {
        // Recheck after scheduling: an upward scroll must win over this resize.
        if (!canFollow()) return
        el.scrollTop = el.scrollHeight
        pin.current = el.scrollTop
      })
    }
    const ro = new ResizeObserver(follow)
    ro.observe(content)
    // A buffered attach replaces the whole transcript at once. Recheck its
    // measured bottom on completion, even if the virtualizer reused a spacer
    // of the same size. Keep the existing viewport's user-follow preference.
    if (!isReplaying) follow()
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
      el.removeEventListener('scroll', observeScroll)
      el.removeEventListener('wheel', onWheel)
      el.removeEventListener('keydown', onKeyDown)
      el.removeEventListener('pointerdown', onPointerDown)
      window.removeEventListener('pointerup', onPointerUp)
    }
  }, [viewportStore, scrollRef, contentRef, isReplaying])
  return null
}


export function StreamingDelegation({
  message,
  events,
  snapshots,
  owners,
  children,
}: {
  message: ChatMessage
  events: readonly DelegationEvent[]
  snapshots: Readonly<Record<string, string>> | undefined
  owners: Readonly<Record<string, string>> | undefined
  children: React.ReactNode
}) {
  const split = splitAnchoredDelegationEvents(
    events,
    message.id,
    placementOf(message),
    liveSnapshotIds(message.id, snapshots, owners),
  )
  // The tool group reports which call ids it drew. Anything still unclaimed
  // (a snapshot whose part never arrived) renders after the bubble, not nowhere.
  const { claimed, reportMatched } = useClaimedCallIds()
  return (
    <DelegationInlineProvider byCall={split.byCall} reportMatched={reportMatched}>
      {children}
      <DelegationLiveTail byCall={split.byCall} trailing={split.trailing} claimed={claimed} />
    </DelegationInlineProvider>
  )
}

export function AssistantMessage() {
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const message = useMessage()
  // Perf (chat UI freeze under heavy subagent/delegation
  // activity): select the ONE message by id from `messagesById` instead of
  // subscribing to the whole `messages` array + `.find()`ing it every
  // render — see SubagentSpansRenderer's identical fix above (and
  // ChatStore.messagesById's doc comment) for the full rationale. This is
  // the component that renders the live streaming bubble, so it used to
  // re-render (and re-scan `messages`) on literally every WS frame of the
  // whole turn regardless of which message the frame actually touched. The
  // `??` fallback is a defensive O(N) scan for a hand-rolled test fixture
  // that sets `messages` directly without `messagesById` — see
  // SubagentSpansRenderer's identical fallback comment for why it's never
  // reached in real app flows.
  const storeMsg = useChatStore((s) => s.messagesById[message.id] ?? s.messages.find((m) => m.id === message.id))
  // Fix 3 (2026-07-16): needed to judge tool-call/span emptiness on VISIBLE
  // content only — see the showEmptyPlaceholder computation below.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)

  // Prefer the per-message agentId (set during transcript replay) over the
  // session-level activeAgentId. This makes multi-agent transcripts show the
  // correct per-turn agent label instead of the current session agent.
  const messageAgentId = storeMsg?.agentId ?? activeAgentId
  const agent = agents.find((a) => a.id === messageAgentId)
  // Fallback to the raw agentId string if the agent isn't in the list yet
  const agentDisplayName = agent?.name ?? (messageAgentId || null)
  // FR-21 / ADR-087 D1: the muted footer suffix — "(interrupted)" or a
  // max_output_tokens cutoff notice. getMessageStatusSuffix owns precedence.
  const statusSuffix = getMessageStatusSuffix(storeMsg ?? {})

  // D-fix: the chat store's optimistic assistant placeholder starts as
  // content:'' / status:'streaming' the instant a message is sent (store/chat.ts
  // sendMessage), so this component mounts and renders for the window before the
  // first token/tool-call/media/span arrives. Unconditionally rendering the Copy
  // (+ Retry) action bar in that window shows a "Copy" affordance for literally
  // nothing to copy — two live UAT testers flagged the resulting empty-bubble-
  // with-Copy-button as looking broken. While running with nothing to show yet,
  // render only the thinking indicator and suppress the action bar; both return
  // the moment any real content lands (text, tool call, media, or subagent span).
  const isRunning = message.status?.type === 'running'
  const hasVisibleText = message.content?.some(
    (part) => part.type === 'text' && part.text.trim().length > 0,
  )
  // Fix 3 (2026-07-16): VISIBLE tool calls only — mirrors the historical
  // path's wouldToolCallBeVisible check (same function, same rationale: a
  // hidden delegate call or background-bash dispatch must not count as
  // "content" for the ghost-bubble guard below). Every delegate action is
  // hidden in the non-verbose thread (spec D2); the grey event line is the
  // surface. Verbose chat shows every delegate badge, and those calls then
  // count as content. part.isError is the closest available proxy for this
  // surface's outcome signal — see wouldToolCallBeVisible's own doc comment
  // for why.
  const hasVisibleToolCall = message.content?.some(
    (part) =>
      part.type === 'tool-call' &&
      wouldToolCallBeVisible(
        part.toolName,
        part.args as Record<string, unknown> | undefined,
        part.result,
        !!part.isError,
        verboseChatEnabled,
      ),
  )
  const hasMedia = !!storeMsg?.media?.length
  // ADR-091 D7/D10: subagent spans no longer factor into thread emptiness —
  // a child's own frames never arrive in the parent's bucket any more, so
  // there is no span content in the thread to judge emptiness against.
  const isEmptyContent = !hasVisibleText && !hasVisibleToolCall && !hasMedia
  // FR-21: show (interrupted) suffix when the store marks this message interrupted.
  const isInterrupted = storeMsg?.status === 'interrupted'
  const showEmptyPlaceholder = isRunning && isEmptyContent
  // D-fix, terminal-empty variant: the turn ended (not running) — normally
  // (not interrupted, not a typed error, both of which already have their
  // own distinct treatment: the '(interrupted)' label and the error
  // action-bar/detail-disclosure below) — yet still holds nothing to show.
  // This is reachable when the engine's own empty-response fallback text
  // (pkg/agent/loop.go's `defaultResponse` sentinel: "The model returned an
  // empty response...") fails to land on THIS message — see the 'token'
  // handler's lastMsgIsEmptyTerminal guard in store/chat.ts, which merges
  // that fallback delivery back into this exact placeholder in the normal
  // case. Whatever left this message here empty and terminal, an empty
  // bubble has nothing to copy — do not show the Copy affordance for it,
  // matching the streaming-case guard immediately above.
  const isTerminalEmpty = !isRunning && isEmptyContent && !isInterrupted && storeMsg?.status !== 'error'

  return (
    <MessagePrimitive.Root
      data-testid="assistant-message"
      data-message-id={message.id}
      data-status={message.status?.type ?? 'complete'}
      className="group flex gap-[var(--space-2-5)] px-[var(--space-3)] py-[var(--space-2-5)]"
    >
      <div className="flex flex-col gap-[var(--space-1)] max-w-[85%] min-w-0 flex-1">
        {agentDisplayName && (
          // Founder UI feedback 2026-10-11: same label as the settled rows —
          // small agent mark beside the name, no status word.
          <AgentReplyLabel agent={agent} name={agentDisplayName} header={null} />
        )}
        <div className="text-[length:var(--type-body-compact-size)] leading-relaxed text-[var(--color-secondary)]">
          {!showEmptyPlaceholder && !isTerminalEmpty && (
            <>
              {/* Media (screenshots, files) renders BEFORE the parts so the image
                  shows directly under the tool-call pill that produced it, with
                  streamed assistant text appearing below the image. Without this
                  order the image gets pinned to the bottom of the bubble while
                  new text streams above it — visually disconnecting the
                  screenshot from the "Here's your screenshot…" caption. */}
              <InlineMedia />
              {/* Use components prop so AssistantUI can inject registered tool UIs
                  (from makeAssistantToolUI) automatically by tool name. Unregistered
                  tools fall through to FallbackToolUI (generic JSON badge). */}
              <MessagePrimitive.Parts
                components={{
                  Text: AssistantTextPart,
                  ToolGroup: DelegationToolGroup,
                  tools: {
                    Fallback: FallbackToolUI as unknown as import('@assistant-ui/react').ToolCallMessagePartComponent,
                  },
                }}
              />
            </>
          )}
          {/* One mount point before and after the first visible token/tool.
              Text can appear above it without reselecting the fixed phrase
              or restarting the synchronized icon/text animation clocks. */}
          {!isTerminalEmpty && <InlineThinkingIndicator agentId={messageAgentId ?? null} />}
        </div>

        {/* Action bar — Copy + Retry buttons, always visible at reduced opacity.
            Suppressed while showEmptyPlaceholder (D-fix): nothing has streamed
            in yet, so there is nothing to copy or retry. Also suppressed for
            the terminal-empty variant (isTerminalEmpty): the turn is over
            and there is still nothing to copy or retry. */}
        {!showEmptyPlaceholder && !isTerminalEmpty && (
          <ActionBarPrimitive.Root className="flex items-center gap-[var(--space-1)] opacity-70 hover:opacity-100 transition-opacity duration-150">
            <ActionBarPrimitive.Copy asChild>
              <Button
                variant="ghost"
                aria-label="Copy message"
                className="h-auto gap-[var(--space-1)] px-[var(--space-2)] py-[var(--space-1)] rounded font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]"
                title="Copy message"
              >
                <AuiIf condition={(s) => s.message.isCopied}>
                  <Check size={11} weight="bold" className="text-[var(--color-success)]" />
                  <span>Copied!</span>
                </AuiIf>
                <AuiIf condition={(s) => !s.message.isCopied}>
                  <Copy size={11} />
                  <span>Copy</span>
                </AuiIf>
              </Button>
            </ActionBarPrimitive.Copy>
            <AssistantMessageRetryButton />
          </ActionBarPrimitive.Root>
        )}
        {/* FR-21 / ADR-087 D1: status suffix — shown when the turn was
            cancelled ("(interrupted)") or cut off at the provider's
            output-token limit ("(cut off at the output limit)"). */}
        {statusSuffix && (
          <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] italic px-[var(--space-1)]">{statusSuffix}</span>
        )}
        <AssistantMessageConnectionStatus messageId={message.id} agentName={agentDisplayName ?? 'Omnipus'} />
      </div>
    </MessagePrimitive.Root>
  )
}

// ── Composer ──────────────────────────────────────────────────────────────────
// #1449 FE-0: the message box lives in composer/OmnipusComposer.tsx. Re-exported
// here because existing tests and ActivityBar import it from this module.
export { OmnipusComposer }

// ── Welcome state ─────────────────────────────────────────────────────────────

function WelcomeState({ hasAgent }: { hasAgent: boolean }) {
  const workspaceId = useWorkspacesStore((state) => state.activeWorkspaceId)
  const entryBlocked = useSessionStore((state) => workspaceEntryBlocksSend(state, workspaceId))
  if (entryBlocked) return null
  return (
    <div className="flex flex-col items-center justify-center h-full min-h-[60vh] gap-[var(--space-5)] p-[var(--space-5)]">
      <div className="flex flex-col items-center gap-[var(--space-4)] text-center max-w-md">
        <img
          src={OmnipusAvatar}
          alt="omnipus.ai mark"
          className="h-20 w-20 drop-shadow-lg"
        />
        <div>
          <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)] mb-[var(--space-2)]">
            Welcome to <Wordmark className="text-2xl" />
          </h1>
          <p className="text-[var(--color-muted)] text-[length:var(--type-body-compact-size)]">
            {hasAgent
              ? 'Your agent is ready. Start a conversation below.'
              : 'Select an agent in the sidebar list to get started.'}
          </p>
        </div>
      </div>
    </div>
  )
}

// ── Main screen ───────────────────────────────────────────────────────────────

export function ChatScreen({ agentRemoved = false }: { agentRemoved?: boolean }) {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const rateLimitEvent = useChatStore((s) => s.rateLimitEvent)
  const clearRateLimitEvent = useChatStore((s) => s.clearRateLimitEvent)
  const providerRetryEvent = useChatStore((s) => s.providerRetryEvent)
  const isStreaming = useChatStore((s) => s.isStreaming)
  // ADR-049 D6/US-12/SD-C9: loop indicator state, same session-scoped
  // foreground field as rateLimitEvent above. (Goal pill state is read
  // directly by GoalPillTray via its own useChatStore subscription — FE-1.)
  const loopStatus = useChatStore((s) => s.loopStatus ?? null)
  // For the ARIA live region: track the last assistant message id for screen reader announcements.
  // Select the pre-derived single id from the store (companion to `messagesById`)
  // rather than subscribing to the whole `messages` array + reversing/scanning it per
  // frame — the id only changes when the LAST assistant message itself changes (e.g. a
  // new turn starts), not on every WS frame of the turn. The full message object (for
  // the status read below) is then looked up via `messagesById[id]` — the same
  // O(1)-lookup idiom AssistantMessage/SubagentSpansRenderer use.
  const messages = useChatStore((s) => s.messages)
  const outboundQueue = useChatStore((s) => s.outboundQueue)
  const pendingDrainQueue = useChatStore((s) => s.pendingDrainQueue)
  const queuedMessages = useMemo(() => {
    const existingIds = new Set(messages.map((message) => message.id))
    return [...pendingDrainQueue, ...outboundQueue]
      .filter((item): item is QueuedOutboundMessage => typeof item !== 'string' && !existingIds.has(item.id))
      .sort((left, right) => left.timestamp.localeCompare(right.timestamp))
      .map((item): ChatMessage => ({
        id: item.id,
        session_id: activeSessionId ?? undefined,
        role: 'user',
        content: item.content,
        timestamp: item.timestamp,
        status: 'done',
        deliveryStatus: 'queued',
      }))
  }, [activeSessionId, messages, outboundQueue, pendingDrainQueue])
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  // One presentation gate for live, REST and replay, before either list renders.
  // Hidden diagnostics remain in the bucket, with no empty virtualized row.
  const displayMessages = useMemo(
    () => [...messages, ...queuedMessages].filter((message) =>
      shouldRenderContextWindowNoticeInThread(message.type, verboseChatEnabled),
    ),
    [messages, queuedMessages, verboseChatEnabled],
  )
  const lastAssistantMessageId = useChatStore((s) => s.lastAssistantMessageId)
  const lastAssistantMessage = useChatStore((s) =>
    lastAssistantMessageId === null
      ? undefined
      : s.messagesById[lastAssistantMessageId] ?? s.messages.find((m) => m.id === lastAssistantMessageId),
  )
  const lastAnnouncedIdRef = useRef<string | null>(null)
  const shouldAnnounce = lastAssistantMessage?.id != null && lastAssistantMessage.id !== lastAnnouncedIdRef.current

  useEffect(() => {
    if (lastAssistantMessage?.id && lastAssistantMessage.status === 'done') {
      lastAnnouncedIdRef.current = lastAssistantMessage.id
    }
  }, [lastAssistantMessage?.id, lastAssistantMessage?.status])

  const { data: agentsForAria = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentName = agentsForAria.find((a) => a.id === activeAgentId)?.name ?? 'Omnipus'

  const historyRead = useChatHistory()
  const sessionGone = historyRead.sessionGone

  const liteMode = useConnectionStore((s) => s.liteMode)

  return (
    // data-active-session-id: deterministic DOM signal of WHICH session this
    // chat surface is currently bound to. Only empty on a cold load with no
    // session yet attached — during a route swap away from a previously
    // attached session it still holds the PREVIOUS session's id (stale)
    // until the new one lands, and it can transiently read '__pending' (the
    // optimistic-send sentinel, store/chat.ts) too. E2E deep-link
    // navigation needs it: a bare "composer is visible/enabled" wait is
    // satisfied by the PREVIOUS route's composer during the route swap, so
    // a test can type into the old surface and the message rides the stale
    // session binding — see openSessionByDeepLink in
    // tests/e2e/fixtures/session-setup.ts.
    <ReplyStatusFallbackProvider>
    <div
      className="flex flex-col absolute inset-0 overflow-hidden"
      data-active-session-id={activeSessionId ?? ''}
    >
      {/* Dead-session banner (D5 + C10). Two truths, keyed on what the server
          actually said: agentRemoved (the agent_removed signal from the
          session detail) is the only grounds for "Agent removed"; a confirmed
          404 from the history read proves only that the CHAT is gone, so the
          copy says exactly that — not "Agent removed". */}
      {(agentRemoved || sessionGone) && (
        <div
          data-testid="agent-removed-banner"
          className="px-[var(--space-3)] py-[var(--space-2)] bg-[var(--color-error)]/10 border-b border-[var(--color-error)]/20 flex items-center gap-[var(--space-2)]"
        >
          <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-error)] flex-1">
            {agentRemoved ? 'Agent removed — this session is read-only' : 'This chat no longer exists'}
          </span>
        </div>
      )}

      {/* #1449 D13 (founder 2026-10-28): the chat's own chrome — the kind
          label / helper breadcrumb and "Add participant" — lives in the
          workspace header line's right-end slot (ChatHeaderSlot), not in a
          row above the feed. */}

        <ThreadPrimitive.Root className="flex flex-col flex-1 min-h-0">
          {/* ARIA live region: announces new assistant messages to screen readers.
              Only fires when a genuinely new message ID arrives and is complete. */}
          <div aria-live="polite" aria-atomic="true" className="sr-only">
            {shouldAnnounce && lastAssistantMessage?.status === 'done' && !lastAssistantMessage.historyPosition && (
              <span>New response from {activeAgentName}</span>
            )}
          </div>

          <HistoryOpenState read={historyRead} />
          {/* Virtualized message list — only visible rows (+ 5-message overscan) are mounted as DOM nodes. */}
          {displayMessages.length === 0 && historyRead.showWelcome ? (
            <div className="flex-1 overflow-y-auto pt-[var(--space-3)] pb-[var(--space-2)]">
              <WelcomeState hasAgent={!!activeAgentId} />
            </div>
          ) : (
            /* key = active session: each chat gets a fresh scroll engine, so switching
                chats lands at the bottom instead of keeping the previous chat's offset. */
            <VirtualizedMessageList key={activeSessionId ?? '__none'} messages={displayMessages} liteMode={liteMode} agentName={activeAgentName} openFailed={historyRead.isError} />
          )}

          <ChatConnectionNotice />
          <RestartInterruptedNotice />

          {/* Rate-limit indicator — shown above composer. Tool-approval requests
              (including `bash`) are handled by the app-wide ToolApprovalModal
              in L1 stage 1, the inline ToolApprovalPanel later (ADR-036 §3.4 retired the dedicated exec-only approval flow). */}
          {rateLimitEvent && (
            <div className="px-[var(--space-3)] space-y-[var(--space-2)] pb-[var(--space-2)]">
              <RateLimitIndicator
                scope={rateLimitEvent.scope}
                resource={rateLimitEvent.resource}
                policyRule={rateLimitEvent.policyRule}
                retryAfterSeconds={rateLimitEvent.retryAfterSeconds}
                tool={rateLimitEvent.tool}
                onDismiss={clearRateLimitEvent}
              />
            </div>
          )}

          {/* Provider failover-chain indicator — provider-messages spec §8:
              the chain's provider_retry countdown, above the composer next to
              the own-limiter indicator. Parent-level removal only — no
              dismiss control; Stop is the chain's only early exit. */}
          {providerRetryEvent && isStreaming && (
            <div className="px-[var(--space-3)] pb-[var(--space-2)]">
              <ProviderRetryIndicator
                provider={providerRetryEvent.provider}
                model={providerRetryEvent.model}
                retryAt={providerRetryEvent.retryAt}
                sentAt={providerRetryEvent.sentAt}
                receivedAt={providerRetryEvent.receivedAt}
                attempt={providerRetryEvent.attempt}
                maxAttempts={providerRetryEvent.maxAttempts}
                previousProvider={providerRetryEvent.previousProvider}
                previousModel={providerRetryEvent.previousModel}
              />
            </div>
          )}

          {/* Goal/loop indicator — ADR-049 D6/US-12/SD-C9: the goal pill was
              RELOCATED to the bottom-right GoalPillTray (FE-1, ADR-053 US-14);
              this above-composer slot now carries LOOP status only. Pass
              goalStatus=null so GoalIndicator's goal branch is dormant.
              GoalIndicator renders nothing when loopStatus is also absent. */}
          {loopStatus && (
            <div className="px-[var(--space-3)] space-y-[var(--space-2)] pb-[var(--space-2)]">
              <GoalIndicator goalStatus={null} loopStatus={loopStatus} />
            </div>
          )}

          {/* Goal echo / amendment cards — ADR-053 FE-8: the compiled goal is
              echoed IN CHAT (no form/modal) when a pill is in `queued` state
              (newly compiled, awaiting the user's chat confirmation). Renders
              nothing when no queued pills exist.
              MOVED (operator report, 2026-09-07): this used to render here,
              in this non-scrolling slot between the message list and the
              composer — a long criteria/DoD ladder could overflow it with
              the Confirm/Amend/Cancel buttons pushed off screen and
              unreachable. It now renders INSIDE the scrollable message-list
              container instead (see PlainMessageList / VirtualizedMessageListInner
              below), so it scrolls with the transcript and the buttons are
              always reachable. AskUserQuestionThreadTail stays here — its
              tabbed one-question-at-a-time view is short and has no such
              overflow risk. */}

          {/* AskUserQuestion card — askuserquestion-tool-spec v3 (ADR-074
              D4b): the flat, tabbed question zone (pending) or the collapsed
              answer record (just-resolved), rendered at the thread tail.
              While pending, the composer below is locked (see
              OmnipusComposer's askLocked). */}
          <AskUserQuestionThreadTail />

          {/* Secure secret card (omnipus-system-agent K1, FR-061/FR-074):
              pending cards rooted in this chat, plus the latest result.
              WU-4 enables cards independently of WU-5's private-secrets list. */}
          {SECRET_CARDS_ENABLED && <SecretRequestThreadTail />}

          {/* Inline tool approvals rooted in this chat (#1263, L1 amendment
              §1.2 R6) — hidden until WU-8 (stage 2): founder design = card in
              parent chat, no banner, menu-bar attention animation. */}
          {INLINE_TOOL_APPROVALS_ENABLED && <ToolApprovalThreadTail />}

          {/* Composer — centered, ChatGPT-style floating layout. The context
              row and ActivityBar pills render bare on the shell (above /
              below the card); only the input surface reads as a card. */}
          <div className="relative w-full">
            {/* Goal pill tray — ADR-053 FE-1: bottom-right pills, one per
                goal-id, click-to-expand. Renders nothing when no goals are
                active (the tray component early-returns null on an empty
                map). Positioned just above the composer, right-aligned,
                pointer-events-none on the tray wrapper so it never blocks
                composer interaction (each pill re-enables pointer events). */}
            <GoalPillTray />
            {/* No gradient fade here: it painted the container's top edge (founder feedback 2026-10-11 p5; ChatScreen.composer-topedge.test.tsx). */}
            <div className="w-full max-w-3xl mx-auto px-[var(--space-3)] pt-[var(--space-2)] pb-[max(var(--space-2),env(safe-area-inset-bottom,var(--space-0)))]">
              <OmnipusComposer agentRemoved={agentRemoved || sessionGone} />
            </div>
          </div>
        </ThreadPrimitive.Root>
    </div>
    </ReplyStatusFallbackProvider>
  )
}
