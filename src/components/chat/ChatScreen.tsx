import React, { useEffect, useRef, useState, useCallback, useMemo } from 'react'
import { useSettledFlag } from '@/hooks/useSettledFlag'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useQuery } from '@tanstack/react-query'
import {
  ThreadPrimitive,
  MessagePrimitive,
  ComposerPrimitive,
  AttachmentPrimitive,
  useAttachment,
  ActionBarPrimitive,
  AuiIf,
  useComposerRuntime,
  useMessage,
  useThreadViewportStore,
} from '@assistant-ui/react'
import {
  ArrowCounterClockwise,
  User,
  Robot,
  PaperPlaneRight,
  Stop,
  Copy,
  Check,
  ListChecks,
  Plus,
  File,
  WifiSlash,
  ArrowClockwise,
  Clock,
  Lightning,
} from '@phosphor-icons/react'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { Wordmark } from '@/components/shared/Wordmark'
import { GenericToolCall } from './tools/GenericToolCall'
import { detectToolResultSentinels } from './tools/toolResultSentinels'
import { WebServeBlock } from './tools/WebServeUI'
import { BrowserToolReplayBlock, isReplayBrowserToolName } from './tools/BrowserTool'
import { RateLimitIndicator } from './RateLimitIndicator'
import { GoalIndicator } from './GoalIndicator'
import { GoalPillTray } from './GoalPillTray'
import { GoalThreadTailCards } from './GoalThreadTailCards'
import { JudgeVerdictThreadCard } from './JudgeVerdictThreadCard'
import { ActivityBar } from './ActivityBar'
import { AgentPicker } from './composer/AgentPicker'
import { ModelPicker } from './composer/ModelPicker'
import { TokenCounter } from './composer/TokenCounter'
import { MarkdownText } from './markdown-text'
import { SubagentBlock } from './SubagentBlock'
import { ModelFooter } from './ModelFooter'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { useChatStore } from '@/store/chat'
import type { ChatMessage, PositionedToolCall, SubagentSpan } from '@/store/chat'
import type { MessagePartStatus } from '@assistant-ui/react'
import { splitMessageParts } from '@/lib/messageParts'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderSubagentSpan, shouldRenderToolCall, shouldRenderJudgeVerdictInThread } from '@/lib/toolVisibility'
import { fetchAgents, fetchSessionMessages, fetchCommands, fetchSkills } from '@/lib/api'
import type { SlashCommand, Skill, Agent } from '@/lib/api'
import { AttachmentCard, AttachmentRemoveX, useFilePreview } from './AttachmentCard'
import { ComposerMediaLibraryButton, LibraryAttachmentChips } from './ComposerMediaLibrary'
import { cn, initialOf } from '@/lib/utils'
import { HistoricalMessageMarkdown } from './historical-markdown'
import { ChatImage } from './ChatImage'
import { useSlashMenu, SECTION_CAP } from '@/hooks/useSlashMenu'
import { useFileUpload } from '@/hooks/useFileUpload'
import { useCancelState } from '@/hooks/useCancelState'

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
      <div className="rounded-xl px-4 py-3 text-sm leading-relaxed bg-[var(--color-surface-2)] text-[var(--color-secondary)] rounded-tr-sm flex flex-col gap-1">
        {skillMatch[2] ? (
          <>
            <p className="whitespace-pre-wrap break-words">{skillMatch[2]}</p>
            <span className="flex items-center gap-1 text-[10px] text-[var(--color-muted)]">
              <Lightning size={10} weight="fill" className="text-[var(--color-accent)]" />
              skill: {matchedSkill.name}
            </span>
          </>
        ) : (
          <span className="flex items-center gap-1 text-xs">
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

function UserMessage() {
  const message = useMessage()
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

  const content = (() => {
    const parts = message.content
    if (!parts || parts.length === 0) return null
    const textPart = parts.find((p: { type: string }) => p.type === 'text')
    if (!textPart || typeof (textPart as { text?: string }).text !== 'string') return null
    return (textPart as { text: string }).text
  })()

  const commandLabels = commandLabelsWithAliases(commands)

  return (
    <MessagePrimitive.Root data-testid="user-message" data-message-id={message.id} className="group flex gap-3 px-4 py-3 flex-row-reverse">
      <div className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center bg-[var(--color-accent)]/20 text-[var(--color-accent)]">
        <User size={14} weight="bold" />
      </div>
      <div className="flex flex-col items-end gap-1 max-w-[85%] min-w-0">
        {renderSkillAwareContent(content, skills, commandLabels, () => (
          <div className="rounded-xl px-4 py-3 text-sm leading-relaxed bg-[var(--color-surface-2)] text-[var(--color-secondary)] rounded-tr-sm">
            <MessagePrimitive.Parts>
              {({ part }) => {
                if (part.type !== 'text') return null
                return <p className="whitespace-pre-wrap break-words">{part.text}</p>
              }}
            </MessagePrimitive.Parts>
          </div>
        ))}
      </div>
    </MessagePrimitive.Root>
  )
}

function SystemMessage() {
  return (
    <MessagePrimitive.Root className="flex justify-center py-2">
      <div className="text-xs text-[var(--color-muted)] bg-[var(--color-surface-2)] px-3 py-1 rounded-full">
        <MessagePrimitive.Parts>
          {({ part }) => {
            if (part.type !== 'text') return null
            return <span>{part.text}</span>
          }}
        </MessagePrimitive.Parts>
      </div>
    </MessagePrimitive.Root>
  )
}

// Animated thinking indicator with rotating status messages
const THINKING_MESSAGES = [
  'Thinking…',
  'Composing response…',
  'Processing your request…',
  'Analyzing…',
  'Generating…',
]

// ADR-051 — cap on the verbose-only "Technical details" disclosure content
// in VirtualAssistantMessageRow (historical/replay render path). Mirrors
// MessageItem.tsx's cap (live render path) so live and replay show the same
// truncated length when a provider's error payload is verbose.
const ERROR_DETAIL_MAX_CHARS = 512

function ThinkingIndicator() {
  const [msgIndex, setMsgIndex] = useState(0)

  useEffect(() => {
    const interval = setInterval(() => {
      setMsgIndex((i) => (i + 1) % THINKING_MESSAGES.length)
    }, 2000)
    return () => clearInterval(interval)
  }, [])

  return (
    <span className="text-[var(--color-muted)] italic flex items-center gap-2.5 py-1">
      <span className="flex gap-1">
        <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-bounce" style={{ animationDelay: '0ms' }} />
        <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-bounce" style={{ animationDelay: '150ms' }} />
        <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-bounce" style={{ animationDelay: '300ms' }} />
      </span>
      <span className="text-xs transition-opacity duration-300">{THINKING_MESSAGES[msgIndex]}</span>
    </span>
  )
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
  return (
    <TextPartErrorBoundary>
      <div>
        <MarkdownText />
      </div>
    </TextPartErrorBoundary>
  )
}

// Shows thinking dots inside the assistant message while it is still running.
// Stays visible the entire turn — including between tool-call steps after some
// text has streamed — so the user always knows the agent is still working.
// Uses useMessage() for reactive state (not getState() which is a snapshot).
function InlineThinkingIndicator() {
  const message = useMessage()
  const isRunning = message.status?.type === 'running'
  if (!isRunning) return null
  return <ThinkingIndicator />
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
  isError?: boolean
}) {
  const storeToolCalls = useChatStore((s) => s.toolCalls)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const liveCall = storeToolCalls[props.toolCallId]
  return (
    <GenericToolCall
      toolName={props.toolName}
      args={props.args}
      result={liveCall?.result ?? props.result}
      status={props.status}
      isError={props.isError ?? liveCall?.status === 'error'}
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
    <button tabIndex={0}
      type="button"
      onClick={handleRetry}
      aria-label="Retry — resend the last user message"
      className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-error)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
      title="Retry — resend the last user message"
    >
      <ArrowCounterClockwise size={11} />
      <span>Retry</span>
    </button>
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
    <div className="flex flex-col gap-2 mt-2">
      {storeMsg.media.map((m, i) =>
        m.type === 'image' ? (
          <div key={`${m.url}-${i}`} className="max-w-2xl">
            <ChatImage
              src={m.url}
              alt={m.caption || m.filename}
              filename={m.filename}
            />
            {m.caption && (
              <p className="text-xs text-[var(--color-muted)] px-2 py-1">{m.caption}</p>
            )}
          </div>
        ) : (
          <a tabIndex={0}
            key={`${m.url}-${i}`}
            href={m.url}
            download={m.filename}
            className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-[var(--color-border)] text-xs text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
          >
            <File size={14} />
            {m.filename}
          </a>
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
 * F2: derives the replay MessagePartStatus object from the store's resolved
 * ToolCall.status, instead of the old hardcoded `{type:'complete'}`.
 * BrowserToolReplayBlock and GenericToolCall both consult `status` (via
 * isCancelledStatus) to decide whether to render the cancelled treatment —
 * hardcoding 'complete' meant isCancelledStatus could never be true on
 * replay, so a genuinely cancelled call rendered as a plain success. The
 * `isError` prop each of those two components also takes is passed
 * separately and explicitly at each call site (issue #617), so widening
 * `status.type` to 'incomplete' here does not reopen #617 — it is never
 * consulted for the error bit once an explicit `isError` prop is supplied.
 */
function replayPartStatus(status: 'running' | 'success' | 'error' | 'cancelled'): MessagePartStatus {
  return status === 'cancelled' ? { type: 'incomplete', reason: 'cancelled' } : { type: 'complete' }
}

/**
 * Mirrors the per-tool-call visibility gate GenericToolCall/BashOutput/
 * ToolCallBadge each apply at render time (Fix 3, 2026-07-16). Used ONLY to
 * decide whether a message has any VISIBLE content, so the ghost-bubble
 * empty-placeholder / bare-Copy-bar logic below doesn't unmask a bubble
 * whose only content is a hidden delegation or background-bash dispatch
 * (the D-fix UAT defect resurfacing once the thread started hiding
 * delegation/background-bash by default — toolVisibility.ts). Reuses the
 * same sentinel-detection semantics and the shouldRenderToolCall classifier
 * those components call directly.
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
  const isError = errorFlag || isMarshalErrorSentinel(result) || detectToolResultSentinels(result).any
  return shouldRenderToolCall(tool, params, verboseChatEnabled, isError)
}

/**
 * Resolves a subagent span's delegate kind for SubagentBlock's W3 "no live
 * progress" notice — '3p' only for a resolved external-CLI (subagent_3p)
 * delegate, undefined otherwise (unresolvable agentId included — an unknown
 * agent must not be guessed as either kind).
 *
 * Unlike useRunningActivity.ts's resolveSpanAgentId, this does NOT apply that
 * hook's originating-delegate-call fallback — and doesn't need to.
 * resolveSpanAgentId exists to fix which agent's AVATAR/NAME displays, where
 * getting the exact agent wrong is visibly wrong; resolveSpanAgentType only
 * needs the delegate's KIND (native vs subagent_3p), which it derives by
 * resolving `span.agentId` against the agents list. Per ADR-032, agent
 * identity flows from the resolved target for BOTH dispatch kinds —
 * `spawnSubTurn` (pkg/agent/subturn.go) sets `agent.ID = execSource.ID`
 * unconditionally, native or external-CLI, with no per-dispatch-kind
 * exception — so `span.agentId` reliably carries the resolved delegate id
 * here regardless of which kind it turns out to be.
 */
function resolveSpanAgentType(span: SubagentSpan, agents: Agent[]): '3p' | 'native' | undefined {
  if (!span.agentId) return undefined
  const agent = agents.find((a) => a.id === span.agentId)
  if (!agent) return undefined
  return agent.type === 'subagent_3p' ? '3p' : 'native'
}

// Renders subagent spans attached to the current message (FR-H-008).
// useMessage().id corresponds to the store message's id (set in omnipus-runtime convertMessage).
export function SubagentSpansRenderer() {
  const message = useMessage()
  // Perf (chat UI freeze under heavy subagent/delegation
  // activity): select the ONE message by id from `messagesById` instead of
  // subscribing to the whole `messages` array + `.find()`ing it every
  // render. `messages` gets a brand-new array identity on every WS frame
  // (bucketToForeground rebuilds it every bucket mutation), so the old
  // `useChatStore((s) => s.messages)` re-rendered this component on every
  // frame of the ENTIRE turn, not just frames touching this message; the
  // `.find()` then re-scanned the whole array on top of that. `messagesById`
  // returns the SAME object reference across renders unless THIS message
  // was the one touched by the last mutation (Immer structural sharing —
  // see ChatStore.messagesById's doc comment), so Zustand's default
  // Object.is equality correctly skips re-rendering otherwise. The `??`
  // fallback is a defensive O(N) scan — mirrors attachStepToSpan's
  // established "O(1) lookup first, O(N) fallback" idiom (src/store/chat.ts)
  // — for the narrow case of a hand-rolled test fixture that sets `messages`
  // on the store directly without also setting `messagesById`; real app
  // flows always keep the two in sync (bucketToForeground derives both from
  // the same bucket, and getMessages() filters `messages` down to exactly
  // the ids present in `messagesById`), so this fallback is never reached
  // outside such fixtures.
  const storeMsg = useChatStore((s) => s.messagesById[message.id] ?? s.messages.find((m) => m.id === message.id))
  // Fix 2 (user-approved 2026-07-16): delegation cards are hidden from the
  // thread by default — verbose chat is the only way to bring them back
  // here (shouldRenderSubagentSpan, src/lib/toolVisibility.ts). Selector
  // pattern mirrors ToolCallBadge.tsx's use of the same store (a plain hook
  // call inside a component, not getState()) so this stays reactive to the
  // preference toggling live.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  // W3: reused ['agents'] query (prefetched by AppShell, staleTime 30s
  // elsewhere) — resolves each span's agentId to native/3p so a running
  // external-CLI delegate's card can show the "no live progress" notice.
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const spans = (storeMsg?.spans ?? []).filter((span) => shouldRenderSubagentSpan(span, verboseChatEnabled))
  if (spans.length === 0) return null
  return (
    <>
      {spans.map((span) => (
        <SubagentBlock key={span.spanId} span={span} agentType={resolveSpanAgentType(span, agents)} />
      ))}
    </>
  )
}

// Bug 2 (UAT, ADR-040 browser-panel round): renders the agent the CALLER
// already resolved per-message (`message.agentId ?? activeAgentId`), passed in
// as `agent`. It must NOT re-derive from the live activeAgentId — otherwise
// switching the agent picker mid-turn would instantly repaint the currently
// streaming bubble's avatar to the newly-picked agent while its (correctly
// per-message-scoped) name label kept the original: a live, user-visible
// mismatch that only self-corrected on reload. Taking the resolved agent as a
// prop keeps a single source of truth between the label and the avatar.
function AssistantMessageAvatar({ agent }: { agent?: Agent }) {
  return (
    <div
      className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center text-[var(--color-secondary)]"
      style={{ backgroundColor: agent?.color ?? 'var(--color-surface-3)' }}
      title={agent?.name}
    >
      {agent?.icon ? (
        <IconRenderer icon={agent.icon} size={14} />
      ) : (
        <Robot size={14} weight="bold" />
      )}
    </div>
  )
}


// FR-21: Renders (interrupted) status markers for assistant messages that have
// status:'interrupted' in the Zustand store.
//
// This component is rendered OUTSIDE ThreadPrimitive.Root and outside the
// scrollable Viewport. This guarantees two properties:
//   1. It subscribes directly to Zustand (bypasses AssistantUI rendering).
//   2. It is not inside any overflow-clipped container (Playwright can see it).
//
// Each marker is rendered as a visually small but non-zero text span so that
// E2E tests can locate it with page.locator('text=(interrupted)') combined
// with toBeVisible(). The span has non-zero height because it contains text.
//
// The visible (interrupted) label rendered inside AssistantMessage handles
// the correct visual positioning within the message bubble for human users.
// This component is the reliable E2E-detectable fallback.
function InterruptedMessageMarkers() {
  const messages = useChatStore((s) => s.messages)
  const interrupted = messages.filter(
    (m) => m.role === 'assistant' && m.status === 'interrupted'
  )
  if (interrupted.length === 0) return null
  return (
    <>
      {interrupted.map((m) => (
        <div
          key={m.id}
          data-testid="interrupted-marker"
          data-message-id={m.id}
          className="text-[10px] text-[var(--color-muted)] italic text-center pb-1"
        >
          (interrupted)
        </div>
      ))}
    </>
  )
}

// ── Standalone message row components (virtualizer) ──────────────────────────
// Render ChatMessage from props (no AssistantUI context) for use by the virtualizer.

/** Inline retry button for user messages that failed to send (#253b). */
function UserMessageRetryButton({ message }: { message: ChatMessage }) {
  const sendMessage = useChatStore((s) => s.sendMessage)
  const isStreaming = useChatStore((s) => s.isStreaming)

  if (message.status !== 'error' || isStreaming) return null

  function handleRetry() {
    // #253(c): resend the original user message content.
    sendMessage(message.content)
  }

  return (
    <div className="flex items-center justify-end gap-2 mt-1">
      <span className="text-[10px] text-[var(--color-error)]">Send failed</span>
      <button tabIndex={0}
        type="button"
        data-testid="user-message-retry"
        onClick={handleRetry}
        aria-label="Retry — resend this message"
        className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-error)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
        title="Retry — resend this message"
      >
        <ArrowCounterClockwise size={11} />
        <span>Retry</span>
      </button>
    </div>
  )
}

/** Standalone user message row for the virtualizer. */
// useSkillChipData fetches the skills + web commands ONCE per message list (not
// per row) for the R2 skill-chip detection. Cache hits are free (staleTime 60s).
function useSkillChipData(): { skills: Skill[]; commandLabels: string[] } {
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
}: {
  message: ChatMessage
  skills: Skill[]
  commandLabels: string[]
}) {
  const isError = message.status === 'error'

  return (
    <div
      data-testid="user-message"
      data-message-role="user"
      data-message-id={message.id}
      data-status={message.status}
      className="group flex gap-3 px-4 py-3 flex-row-reverse"
    >
      <div className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center bg-[var(--color-accent)]/20 text-[var(--color-accent)]">
        <User size={14} weight="bold" />
      </div>
      <div className="flex flex-col items-end gap-1.5 max-w-[85%] min-w-0">
        {/* Attachments the user sent — image thumbnails + colour-coded file
            cards, shown above the text like ChatGPT. */}
        {message.media && message.media.length > 0 && (
          <div className="flex flex-wrap gap-2 justify-end">
            {message.media.map((m, i) =>
              m.type === 'image' ? (
                // Sent images (screenshots) get the full ChatImage treatment — hover
                // toolbar (copy/share/download/enlarge) + click-to-enlarge lightbox.
                <ChatImage key={`${m.url}-${i}`} src={m.url} alt={m.filename} filename={m.filename} className="max-w-[240px]" />
              ) : (
                <AttachmentCard key={`${m.url}-${i}`} filename={m.filename} contentType={m.contentType} />
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
              <div className={cn(
                "rounded-xl px-4 py-3 text-sm leading-relaxed rounded-tr-sm",
                isError
                  ? "bg-[var(--color-error)]/10 border border-[var(--color-error)]/30 text-[var(--color-secondary)]"
                  : "bg-[var(--color-surface-2)] text-[var(--color-secondary)]"
              )}>
                <p className="whitespace-pre-wrap break-words">{message.content}</p>
              </div>
            ),
          )
        )}
        {/* #253(b): show error + Retry when message failed to send */}
        <UserMessageRetryButton message={message} />
      </div>
    </div>
  )
}

/** Standalone system message row for the virtualizer. */
function VirtualSystemMessageRow({ message }: { message: ChatMessage }) {
  return (
    <div
      data-message-role="system"
      data-message-id={message.id}
      className="flex justify-center py-2"
    >
      <div className="text-xs text-[var(--color-muted)] bg-[var(--color-surface-2)] px-3 py-1 rounded-full">
        <span>{message.content}</span>
      </div>
    </div>
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
    <button tabIndex={0}
      type="button"
      aria-label="Copy message"
      onClick={handleCopy}
      className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
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
    </button>
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
const VirtualAssistantMessageRow = React.memo(function VirtualAssistantMessageRow({ message, liteMode }: { message: ChatMessage; liteMode: boolean }) {
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const toolCalls = useChatStore((s) => s.toolCalls)
  // Fix 2 (user-approved 2026-07-16): same thread-gating rule as the live
  // path's SubagentSpansRenderer, applied here for the historical/virtualized
  // render tree. This component is React.memo'd, so the selector MUST be
  // called unconditionally at the top with every other hook (Rules of
  // Hooks) — reading getState() instead would silently freeze this row's
  // gating at whatever the preference was on its last actual re-render.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)

  const messageAgentId = message.agentId ?? activeAgentId
  const agent = agents.find((a) => a.id === messageAgentId)
  const agentDisplayName = agent?.name ?? (messageAgentId || null)
  const isInterrupted = message.status === 'interrupted'

  // Render media attachments.
  const mediaItems = message.media ?? []

  // Build tool-call parts from the message's stored tool_calls list.
  // In lite mode, tool calls start collapsed (expanded=false by default in GenericToolCall).
  const positionedToolCalls = (message.tool_calls ?? []) as PositionedToolCall[]

  // Perf (gate 5 MEDIUM): memoized on the message's OWN content/tool_calls
  // fields (not the derived `positionedToolCalls`, which gets a brand-new
  // `[]` identity every render whenever message.tool_calls is undefined —
  // keying on that would defeat the memo, recomputing every render exactly
  // like before). splitMessageParts does real work (a sort + a content
  // slice per positioned call) that was previously re-run inline in JSX on
  // every render of this row, including renders triggered by state this
  // row doesn't even display (e.g. liteMode toggling on OTHER rows, before
  // the React.memo above cut most of those). This memo is still valuable
  // even with the row memoized: message.content mutates in place many
  // times per second while a turn is streaming (each `token` frame), so
  // this row itself legitimately re-renders often — the memo just stops
  // re-running the split when a re-render was triggered by something OTHER
  // than a content/tool_calls change (e.g. liteMode).
  const messageParts = useMemo(
    () => splitMessageParts(message.content ?? '', positionedToolCalls),
     
    // message's raw fields (stable references unless the message actually
    // changed), not the freshly-allocated `positionedToolCalls` derived
    // above — see comment.
    [message.content, message.tool_calls],
  )

  // D-fix: this row is also used by PlainMessageList (the ResizeObserver-
  // unavailable fallback), which — unlike VirtualizedMessageListInner — does
  // NOT split the live streaming placeholder out into ThreadPrimitive.Messages;
  // it renders every message, including an in-flight one, through this
  // component. Without this guard, a message with isStreaming:true and empty
  // content would show the same "avatar + Copy, nothing to copy" broken bubble
  // as the live path (see AssistantMessage's showEmptyPlaceholder).
  //
  // Fix 3 (2026-07-16): emptiness is judged on VISIBLE content only — a
  // message whose only content is a hidden delegation/background-bash
  // dispatch (default thread policy, toolVisibility.ts) must not render an
  // empty bubble with a bare Copy action bar (the D-fix UAT defect
  // resurfacing once the thread started hiding those by default).
  // visibleToolCalls/visibleSpans are also what's actually rendered below —
  // hoisted here so both the emptiness check and the render loop share one
  // computation instead of drifting into two different notions of "visible".
  const hasContent = !!message.content?.trim().length
  // F4 (second review wave on branch fix/615-617-618-hardening): `tc` here
  // is a baked PositionedToolCall, which — like every other #617-era call
  // site in this file — carries BOTH a `.status` and a (frequently empty)
  // `.error` string. `!!tc.error` alone is exactly the signal #617
  // established is empty for an ordinary failure (pkg/gateway/websocket.go
  // deliberately leaves `Result.Error` unset when `Result` still holds the
  // text). This call's two siblings already moved off that proxy — the live
  // path below passes `!!part.isError`, and the actual render for this same
  // `tc` uses `tc.status === 'error'` — but this one was left on it, so a
  // failed tool call with status:'error' and no `error` string (e.g. a
  // failed ToolSearch) computed isError:false here while the row itself
  // (whose own visibility gate reads `tc.status === 'error'` directly)
  // still rendered — producing a ghost ThinkingIndicator ABOVE the visible
  // failed row (see hasVisibleToolCalls/showEmptyPlaceholder below).
  const visibleToolCalls = positionedToolCalls.filter((tc) =>
    wouldToolCallBeVisible(tc.tool, tc.params, tc.result, tc.status === 'error' || !!tc.error, verboseChatEnabled),
  )
  const hasVisibleToolCalls = visibleToolCalls.length > 0
  const hasMedia = mediaItems.length > 0
  // Fix 2 (user-approved 2026-07-16): the actual render list, filtered
  // through the thread gate (shouldRenderSubagentSpan).
  const visibleSpans = (message.spans ?? []).filter((span) => shouldRenderSubagentSpan(span, verboseChatEnabled))
  const showEmptyPlaceholder =
    !!message.isStreaming && !hasContent && !hasVisibleToolCalls && !hasMedia && !visibleSpans.length

  return (
    <div
      data-testid="assistant-message"
      data-message-role="assistant"
      data-message-id={message.id}
      data-status="complete"
      className="group flex gap-3 px-4 py-3"
    >
      <div
        className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center text-[var(--color-secondary)]"
        style={{ backgroundColor: agent?.color ?? 'var(--color-surface-3)' }}
        title={agent?.name}
      >
        {agent?.icon ? (
          <IconRenderer icon={agent.icon} size={14} />
        ) : (
          <Robot size={14} weight="bold" />
        )}
      </div>
      <div className="flex flex-col gap-1 max-w-[85%] min-w-0 flex-1">
        {agentDisplayName && (
          <span data-testid="agent-label" className="text-[10px] text-[var(--color-muted)]">
            {agentDisplayName}
          </span>
        )}
        <div className="text-sm leading-relaxed text-[var(--color-secondary)]">
          {showEmptyPlaceholder && <ThinkingIndicator />}
          {/* Media attachments */}
          {!showEmptyPlaceholder && mediaItems.length > 0 && (
            <div className="flex flex-col gap-2 mb-2">
              {mediaItems.map((m, i) =>
                m.type === 'image' ? (
                  <div key={`${m.url}-${i}`} className="max-w-2xl">
                    <ChatImage
                      src={m.url}
                      alt={m.caption || m.filename}
                      filename={m.filename}
                    />
                    {m.caption && (
                      <p className="text-xs text-[var(--color-muted)] px-2 py-1">{m.caption}</p>
                    )}
                  </div>
                ) : (
                  <a tabIndex={0}
                    key={`${m.url}-${i}`}
                    href={m.url}
                    download={m.filename}
                    className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-[var(--color-border)] text-xs text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
                  >
                    <File size={14} />
                    {m.filename}
                  </a>
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
            // Parity with the live AssistantUI dispatch in OmnipusRuntimeProvider:
            // web_serve / serve_workspace / run_in_workspace go through WebServeBlock
            // here too, so replayed sessions render the preview link (or the malformed
            // result block) instead of a collapsed generic badge.
            if (tc.tool === 'serve_workspace' || tc.tool === 'run_in_workspace' || tc.tool === 'web_serve') {
              return (
                <WebServeBlock
                  key={callId}
                  args={(tc.params ?? {}) as { path?: string; command?: string; port?: number; duration_seconds?: number }}
                  result={tc.result ?? null}
                  isRunning={false}
                  // Issue #617: replay must reflect the tool call's real
                  // outcome, mirroring GenericToolCall's `error={tc.error}`
                  // below — previously this row always rendered "Done"
                  // regardless of whether the call actually failed.
                  isError={tc.status === 'error'}
                  // F2: same fix for the CANCELLED case — WebServeBlock has no
                  // `status` prop to derive cancellation from (unlike
                  // BrowserToolReplayBlock/GenericToolCall), so it needs the
                  // outcome threaded explicitly.
                  isCancelled={tc.status === 'cancelled'}
                  toolName={tc.tool}
                />
              )
            }
            // B-fix: the six browser.*/browser_* tools also have a registered
            // live UI (BrowserToolBlock, dispatched via makeAssistantToolUI in
            // OmnipusRuntimeProvider) that parses the tool's result into a
            // screenshot/text/error card instead of showing raw JSON. Route
            // replay through the same block for live/replay parity — see
            // BrowserToolReplayBlock's doc comment for the full story.
            //
            // Issue #617: `status` used to be hardcoded to `{type:'complete'}`
            // with no error signal threaded at all, so every replayed browser
            // tool call rendered "OK" unconditionally — mirror the
            // GenericToolCall branch below (`error={tc.error}`) by passing the
            // store's real outcome as `isError`.
            //
            // #617 follow-up (F2): `status` itself was ALSO hardcoded to
            // `{type:'complete'}`, so a CANCELLED replayed call (tc.status ===
            // 'cancelled') fell through the isError check (false, correctly)
            // but still rendered as a plain success — isCancelledStatus(status)
            // can only ever be true when status.type is 'incomplete' with
            // reason 'cancelled', never 'complete'. replayPartStatus derives
            // the real status object so both BrowserToolReplayBlock's and
            // GenericToolCall's own isCancelledStatus checks see it.
            if (isReplayBrowserToolName(tc.tool)) {
              return (
                <BrowserToolReplayBlock
                  key={callId}
                  toolName={tc.tool}
                  args={tc.params}
                  result={tc.result}
                  status={replayPartStatus(tc.status)}
                  isError={tc.status === 'error'}
                />
              )
            }
            return (
              <GenericToolCall
                key={callId}
                toolName={tc.tool}
                args={tc.params}
                result={tc.result}
                status={replayPartStatus(tc.status)}
                error={tc.error}
                // Issue #617: pass the store's real outcome explicitly rather
                // than letting GenericToolCall infer it from `status`/`error`
                // — see GenericToolCallProps.isError's doc comment for why the
                // inferred fallback misses a real failure whose `result` was
                // offloaded/replaced server-side with `error` left empty.
                isError={tc.status === 'error'}
                durationMs={tc.duration_ms}
                defaultCollapsed={liteMode}
                sessionId={activeSessionId ?? ''}
              />
            )
          })}

          {/* Subagent spans — pre-filtered via visibleSpans above (Fix 2).
              W3: agentType resolved from the `agents` list already fetched
              above (for the avatar) — see resolveSpanAgentType's doc comment. */}
          {visibleSpans.map((span) => (
            <SubagentBlock key={span.spanId} span={span} agentType={resolveSpanAgentType(span, agents)} />
          ))}
        </div>

        {/* Action bar — always visible at reduced opacity, fully opaque on hover.
            Suppressed while showEmptyPlaceholder (D-fix): nothing has streamed
            in yet, so there is nothing to copy. */}
        {!showEmptyPlaceholder && (
          <div className="flex items-center gap-1 opacity-70 hover:opacity-100 transition-opacity duration-150">
            <StaticCopyButton text={message.content ?? ''} />
          </div>
        )}

        {/* Interrupted label */}
        {isInterrupted && (
          <span className="text-[10px] text-[var(--color-muted)] italic px-1">(interrupted)</span>
        )}

        {/* ADR-051 — verbose-only "Technical details" disclosure for typed
            LLM errors. Parity with MessageItem.tsx's live-render disclosure:
            mounted ONLY when (status==='error' && errorCode && verbose &&
            errorDetail), ABSENT from the DOM otherwise. Replay frames carry
            no errorDetail (gateway strips it before persisting), so on the
            historical path this effectively fires only when a live error
            bubble with detail set is still in the ring buffer after a
            reload. `verboseChatEnabled` is already read at the top of this
            row (the tool-call visibility gate uses the same hook). */}
        {message.status === 'error'
          && message.errorCode
          && verboseChatEnabled
          && message.errorDetail
          && message.errorDetail.length > 0 && (
          <details className="px-1 mt-1" data-testid="error-detail-disclosure">
            <summary
              tabIndex={0}
              className={cn(
                'text-[10px] text-[var(--color-muted)] cursor-pointer select-none',
                'inline-flex items-center gap-1 hover:text-[var(--color-secondary)] transition-colors',
              )}
            >
              Technical details
            </summary>
            <pre
              className={cn(
                'mt-1 px-2 py-1.5 rounded-md',
                'bg-[var(--color-surface-2)] text-[var(--color-secondary)]',
                'font-mono text-[10px] leading-relaxed whitespace-pre-wrap break-all',
                'max-h-40 overflow-y-auto',
              )}
            >
              {message.errorDetail.slice(0, ERROR_DETAIL_MAX_CHARS)}
            </pre>
          </details>
        )}

        {/* Per-turn model record (FR-014). Mirrors MessageItem.tsx so
            replay sessions show the same model footer as the live
            AssistantUI render. */}
        {message.role === 'assistant' && <ModelFooter model={message.model} />}
      </div>
    </div>
  )
})

/** Plain (non-virtualized) message list — fallback when ResizeObserver is unavailable. */
function PlainMessageList({ messages, liteMode }: { messages: ChatMessage[]; liteMode: boolean }) {
  const { skills, commandLabels } = useSkillChipData()
  // ADR-049 SD-C10: judge-verdict thread visibility (panel-only by default,
  // verbose-only inline) — same store read as every other verbose-gated row.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  return (
    <div
      data-testid="virtualized-message-list"
      className="flex-1 overflow-y-auto overscroll-contain pt-4 pb-2"
    >
      <div className="max-w-4xl mx-auto w-full">
        {messages.map((msg) => {
          // ADR-049 D2/SD-C10: a persisted `Message.type: judge_verdict`
          // entry never renders via the normal role-based rows — it is
          // panel-only by default (ActivityPanel's judge row, fed live by
          // the judge_verdict WS frame — see chat.ts), inline in the thread
          // ONLY under verbose chat.
          if (msg.type === 'judge_verdict') {
            if (!shouldRenderJudgeVerdictInThread(verboseChatEnabled) || !msg.verdict) return null
            return <JudgeVerdictThreadCard key={msg.id} verdict={msg.verdict} />
          }
          if (msg.role === 'user')
            return (
              <VirtualUserMessageRow
                key={msg.id}
                message={msg}
                skills={skills}
                commandLabels={commandLabels}
              />
            )
          if (msg.role === 'system') return <VirtualSystemMessageRow key={msg.id} message={msg} />
          return <VirtualAssistantMessageRow key={msg.id} message={msg} liteMode={liteMode} />
        })}
      </div>
    </div>
  )
}

let _resizeObserverWarnEmitted = false

/**
 * Virtualized historical message list (AssistantUI constraint: the live
 * streaming message stays in ThreadPrimitive.Messages below for full context).
 */
function VirtualizedMessageList({
  messages,
  liteMode,
}: {
  messages: ChatMessage[]
  liteMode: boolean
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
    return <PlainMessageList messages={messages} liteMode={liteMode} />
  }
  return <VirtualizedMessageListInner messages={messages} liteMode={liteMode} />
}

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
function VirtualizerAutoFollow({
  scrollRef,
  contentRef,
}: {
  scrollRef: React.RefObject<HTMLDivElement | null>
  contentRef: React.RefObject<HTMLDivElement | null>
}) {
  const viewportStore = useThreadViewportStore()
  useEffect(() => {
    const el = scrollRef.current
    const content = contentRef.current
    if (!el || !content) return undefined
    let raf = 0
    const ro = new ResizeObserver(() => {
      // Only follow if the engine still considers us at the bottom — this reuses
      // its scrollTop-direction "did the user scroll up" detection, so we never
      // fight a user reading history. isAtBottom is unchanged by a content-growth
      // resize (the engine updates it on scroll events, not on resize), so it
      // still reflects the pre-growth position here.
      if (!viewportStore.getState().isAtBottom) return
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => {
        el.scrollTop = el.scrollHeight
      })
    })
    ro.observe(content)
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
    }
  }, [viewportStore, scrollRef, contentRef])
  return null
}

function VirtualizedMessageListInner({
  messages,
  liteMode,
}: {
  messages: ChatMessage[]
  liteMode: boolean
}) {
  const isStreaming = useChatStore((s) => s.isStreaming)
  const scrollContainerRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const { skills, commandLabels } = useSkillChipData()
  // ADR-049 SD-C10: judge-verdict thread visibility (panel-only by default, verbose-only inline).
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)

  // Separate the live streaming message from completed history.
  const hasStreamingMessage = isStreaming && messages.length > 0 && messages[messages.length - 1]?.isStreaming
  const historicalMessages = hasStreamingMessage ? messages.slice(0, messages.length - 1) : messages

  const virtualizer = useVirtualizer({
    count: historicalMessages.length,
    getScrollElement: () => scrollContainerRef.current,
    estimateSize: () => 80,
    overscan: 5,
    // measureElement default (getBoundingClientRect().height) is correct; no override needed.
  })

  const virtualItems = virtualizer.getVirtualItems()

  const rowForMessage = (msg: ChatMessage) => {
    // ADR-049 D2/SD-C10: see PlainMessageList's identical branch above.
    if (msg.type === 'judge_verdict') {
      if (!shouldRenderJudgeVerdictInThread(verboseChatEnabled) || !msg.verdict) return null
      return <JudgeVerdictThreadCard verdict={msg.verdict} />
    }
    if (msg.role === 'user')
      return <VirtualUserMessageRow message={msg} skills={skills} commandLabels={commandLabels} />
    if (msg.role === 'system') return <VirtualSystemMessageRow message={msg} />
    return <VirtualAssistantMessageRow message={msg} liteMode={liteMode} />
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
      autoScroll
      data-testid="virtualized-message-list"
      className="flex-1 overflow-y-auto overscroll-contain pt-4 pb-2"
      style={{ position: 'relative' }}
    >
      <VirtualizerAutoFollow scrollRef={scrollContainerRef} contentRef={contentRef} />
      <div ref={contentRef} className="max-w-4xl mx-auto w-full">
        <div
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
              transform: `translateY(${virtualItems[0]?.start ?? 0}px)`,
            }}
          >
            {virtualItems.map((virtualRow) => {
              const msg = historicalMessages[virtualRow.index]
              if (!msg) return null
              return (
                <div
                  key={virtualRow.key}
                  data-index={virtualRow.index}
                  ref={virtualizer.measureElement}
                >
                  {rowForMessage(msg)}
                </div>
              )
            })}
          </div>
        </div>

        {/* Live streaming message — kept in ThreadPrimitive.Messages for full
            AssistantUI context (streaming primitives, registered tool UIs). */}
        {hasStreamingMessage && (
          <div data-testid="streaming-message-anchor">
            <ThreadPrimitive.Messages>
              {({ message }) => {
                const isLast = message.id === messages[messages.length - 1]?.id
                if (!isLast) return null
                if (message.role === 'user') return <UserMessage />
                if (message.role === 'system') return <SystemMessage />
                return <AssistantMessage />
              }}
            </ThreadPrimitive.Messages>
          </div>
        )}
      </div>
    </ThreadPrimitive.Viewport>
  )
}

function AssistantMessage() {
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
  // FR-21: show (interrupted) suffix when the store marks this message interrupted.
  const isInterrupted = storeMsg?.status === 'interrupted'

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
  // hidden delegation/background-bash dispatch must not count as "content"
  // for the ghost-bubble guard below). part.isError is the closest
  // available proxy for this surface's outcome signal — see
  // wouldToolCallBeVisible's own doc comment for why.
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
  const visibleSpans = (storeMsg?.spans ?? []).filter((span) => shouldRenderSubagentSpan(span, verboseChatEnabled))
  const showEmptyPlaceholder =
    isRunning && !hasVisibleText && !hasVisibleToolCall && !hasMedia && !visibleSpans.length

  return (
    <MessagePrimitive.Root
      data-testid="assistant-message"
      data-message-id={message.id}
      data-status={message.status?.type ?? 'complete'}
      className="group flex gap-3 px-4 py-3"
    >
      <AssistantMessageAvatar agent={agent} />
      <div className="flex flex-col gap-1 max-w-[85%] min-w-0 flex-1">
        {agentDisplayName && (
          <span data-testid="agent-label" className="text-[10px] text-[var(--color-muted)]">{agentDisplayName}</span>
        )}
        <div className="text-sm leading-relaxed text-[var(--color-secondary)]">
          {showEmptyPlaceholder ? (
            // Nothing has streamed in yet — show only the thinking indicator,
            // not an empty text bubble + Copy affordance (D-fix).
            <InlineThinkingIndicator />
          ) : (
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
                  tools: {
                    Fallback: FallbackToolUI as unknown as import('@assistant-ui/react').ToolCallMessagePartComponent,
                  },
                }}
              />
              {/* Subagent spans — rendered per-message, keyed by span_id (FR-H-008) */}
              <SubagentSpansRenderer />
              {/* Trailing thinking indicator — sits at the bottom of the bubble
                  while the turn is running so the user always sees a "still
                  working" cue at the position where the next text/tool will
                  appear. Once a token streams in, the streamed text renders
                  above the indicator and pushes it further down. */}
              <InlineThinkingIndicator />
            </>
          )}
        </div>

        {/* Action bar — Copy + Retry buttons, always visible at reduced opacity.
            Suppressed while showEmptyPlaceholder (D-fix): nothing has streamed
            in yet, so there is nothing to copy or retry. */}
        {!showEmptyPlaceholder && (
          <ActionBarPrimitive.Root className="flex items-center gap-1 opacity-70 hover:opacity-100 transition-opacity duration-150">
            <ActionBarPrimitive.Copy asChild>
              <button tabIndex={0}
                type="button"
                aria-label="Copy message"
                className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
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
              </button>
            </ActionBarPrimitive.Copy>
            <AssistantMessageRetryButton />
          </ActionBarPrimitive.Root>
        )}
        {/* FR-21: interrupted status label — shown when the turn was cancelled */}
        {isInterrupted && (
          <span className="text-[10px] text-[var(--color-muted)] italic px-1">(interrupted)</span>
        )}
      </div>
    </MessagePrimitive.Root>
  )
}

// ── Composer ──────────────────────────────────────────────────────────────────

function composerPlaceholder(
  canSendOrQueue: boolean,
  isStreaming: boolean,
  isReplaying: boolean,
  agentName: string,
  gaveUp: boolean,
): string {
  if (gaveUp) return 'Connection lost — click Reconnect now above'
  if (!canSendOrQueue) return 'Connecting to gateway...'
  if (isReplaying) return 'Loading session history...'
  if (isStreaming) return 'Waiting for response...'
  return `Message ${agentName}…`
}

/**
 * Renders one pending composer attachment as a ChatGPT-style card — an image
 * preview (from the local File) or a colour-coded, type-specific file card —
 * via the native useAttachment() context that ComposerPrimitive.Attachments
 * provides. Removal uses the native AttachmentPrimitive.Remove.
 */
function ComposerAttachmentChip() {
  const attachment = useAttachment()
  const file = attachment.file
  const contentType = attachment.contentType ?? file?.type
  const imageUrl = useFilePreview(file, contentType)

  return (
    <AttachmentCard
      filename={attachment.name}
      contentType={contentType}
      imageUrl={imageUrl}
      className={cn('group', imageUrl && 'w-16 h-16')}
      removeButton={
        <AttachmentPrimitive.Remove
          tabIndex={0}
          className="absolute -top-1.5 -right-1.5 opacity-0 group-hover:opacity-100 focus:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity"
          aria-label={`Remove ${attachment.name}`}
        >
          <AttachmentRemoveX />
        </AttachmentPrimitive.Remove>
      }
    />
  )
}

// Exported for unit testing (TDD row 22).
export function OmnipusComposer({ agentRemoved = false }: { agentRemoved?: boolean }) {
  const isStreaming = useChatStore((s) => s.isStreaming)
  // FR-I-014: disable send while replay frames are still arriving
  const isReplaying = useChatStore((s) => s.isReplaying)
  const isConnected = useConnectionStore((s) => s.isConnected)
  const reconnectPhase = useConnectionStore((s) => s.reconnectPhase)
  const reconnectAttempt = useConnectionStore((s) => s.reconnectAttempt)
  const reconnect = useConnectionStore((s) => s.reconnect)
  // Operator report 2026-07-31: a red "Disconnected" banner flashed top-left
  // for a split second whenever the socket blipped and immediately recovered.
  // The drop is real (the gateway logs 1006 abnormal closures on the
  // Batam<->Frankfurt path) but a self-healing sub-second reconnect is not
  // actionable, and an error-coloured banner for it reads as a fault. Gate
  // BOTH transient banners — the amber "Reconnecting…" and the red
  // reconnectPhase===null limbo one — on the disconnect having actually
  // persisted. Reconnect logic itself is untouched and still fires instantly;
  // only the rendering waits. A genuine outage still surfaces after 2s, and
  // the terminal "gave_up" banner below is deliberately NOT gated — that one
  // is already late by construction and always needs to be seen.
  const showTransientDisconnect = useSettledFlag(
    !isConnected || reconnectPhase === 'reconnecting' || reconnectPhase === 'slow',
    2000,
  )
  const outboundQueue = useChatStore((s) => s.outboundQueue)
  // BUG FIX (2026-07): messages moved out of outboundQueue by drainOutboundQueue
  // are sent one at a time (see maybeDrainNext in store/chat.ts) rather than all
  // at once, so a still-draining batch must keep counting here — otherwise this
  // banner would drop to 0 the instant reconnect fires even though several
  // messages are still waiting for their turn to go out.
  const pendingDrainQueue = useChatStore((s) => s.pendingDrainQueue)
  const cancelStream = useChatStore((s) => s.cancelStream)
  const appendMessage = useChatStore((s) => s.appendMessage)
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const startNewSession = useSessionStore((s) => s.startNewSession)
  const composerRuntime = useComposerRuntime()

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentName = agents.find((a) => a.id === activeAgentId)?.name ?? 'Omnipus'

  // Tracks whether we already warned for the current large-input threshold crossing,
  // so we only fire one toast per paste/input event that exceeds 1MB.
  const hasWarnedLargeInput = useRef(false)

  // Input is enabled unless: agent removed, replaying, or gave up on reconnect.
  // While reconnecting (fast or slow phase), input stays enabled — messages go to the
  // outbound queue (useChatStore's outboundQueue/enqueueOutboundMessage) and are sent
  // automatically once the connection recovers (drainOutboundQueue, wired in
  // OmnipusRuntimeProvider's onConnected). See the outbound-queue-indicator banner
  // below and composerPlaceholder's `canSendOrQueue` argument, both of which already
  // treated "connected OR reconnecting/slow" as usable — but this flag itself required
  // strict `isConnected`, so the actual `disabled` attribute silently blocked the very
  // typing/sending those UI affordances promised, leaving the whole queue mechanism
  // unreachable from real user interaction (#105). Only a fully-lost connection with no
  // more automatic retries (`gave_up`), or the brief pre-first-connect / just-dropped
  // window (isConnected:false with reconnectPhase still null — "Connecting to
  // gateway..."), leaves the composer disabled; see tests/e2e/chat.spec.ts "(f)
  // queue-on-disconnect" and ChatScreen.outbound-queue.test.tsx for regression coverage.
  const inputEnabled =
    !agentRemoved &&
    !isReplaying &&
    !(reconnectPhase === 'gave_up') &&
    (isConnected || reconnectPhase === 'reconnecting' || reconnectPhase === 'slow')

  // Attach affordance gate — shared by the AddAttachment button AND the
  // drag-drop handlers/overlay below (same read-only conditions: agent
  // removed, replaying, gateway unreachable, or gave up on reconnect). A
  // single source of truth keeps the two from drifting: without this, a
  // drag-drop in a read-only (agentRemoved / gave_up) session could attach a
  // chip that can never be sent, even though the attach button itself was
  // correctly disabled.
  //
  // Deliberately does NOT include isStreaming (bugfixes3 follow-up): pasted
  // images already ride a mid-stream steer correctly end-to-end (paste ->
  // useFileUpload's commitFiles -> composerRuntime.addAttachment ->
  // BaseComposerRuntimeCore.send() -> onNew -> store sendMessage's steer
  // branch, which threads opts.mediaRefs into the WS `media` field
  // unconditionally on isStreaming — see src/store/chat.ts's `if
  // (isStreaming)` steer branch). The AddAttachment button and drag-drop
  // funnel into that exact same commitFiles -> addAttachment call (see
  // useFileUpload.ts) — there is no separate mid-stream attachment code
  // path to diverge from paste, so gating the button/drag-drop on
  // isStreaming was just an inconsistent affordance (paste allowed it, the
  // button forbade the identical action) rather than a real safety gate.
  const attachDisabled = !isConnected || isReplaying || reconnectPhase === 'gave_up' || agentRemoved

  // The 3 previously-tangled composer concerns (slash/skill palette, file
  // upload incl. harmful-file confirm, stop/cancel state machine) each own
  // their own state via a dedicated hook — see src/hooks/useSlashMenu.ts,
  // useFileUpload.ts, useCancelState.ts. cancelState is instantiated first
  // because the slash menu's `/cancel` command delegates to it.
  // Focus discipline: the chat input is the page's home position. Focus it
  // (1) on arrival at the chat page (mount), (2) when the search modal
  // closes, and (3) when the slash menu closes — the user should never have
  // to click back into the input after any of these.
  // Ref-based (not a `[data-testid="chat-input"]` querySelector): the testid
  // stays on the element for tests, but a testid rename would otherwise kill
  // this whole focus-discipline feature silently (no type error, no runtime
  // error — just a `null` element and a focus() that quietly no-ops). The
  // ref is threaded straight to the ComposerPrimitive.Input textarea below.
  const chatInputRef = useRef<HTMLTextAreaElement>(null)
  const focusChatInput = useCallback(() => {
    // rAF: run after Radix's own focus-restore (Dialog returns focus to its
    // trigger on close) so ours wins.
    requestAnimationFrame(() => {
      chatInputRef.current?.focus()
    })
  }, [])
  useEffect(() => {
    focusChatInput()
  }, [focusChatInput])
  const searchModalOpenForFocus = useUiStore((s) => s.searchModalOpen)
  const prevSearchOpenRef = useRef(searchModalOpenForFocus)
  useEffect(() => {
    if (prevSearchOpenRef.current && !searchModalOpenForFocus) focusChatInput()
    prevSearchOpenRef.current = searchModalOpenForFocus
  }, [searchModalOpenForFocus, focusChatInput])

  const cancelState = useCancelState(isStreaming, cancelStream)
  const fileUpload = useFileUpload(composerRuntime)
  const slashMenu = useSlashMenu({
    isStreaming,
    isReplaying,
    inputEnabled,
    composerRuntime,
    appendMessage,
    startNewSession,
    cancelIfStreaming: cancelState.cancelIfStreaming,
  })

  // Fix E (bugfixes3 sign-off): `shouldShowSlash` alone is not "the menu
  // has content to render" — the LOW S8 commandsError carve-out
  // (useSlashMenu.ts) deliberately keeps it true even when `slashItems` is
  // empty, so the container below could mount with NEITHER a real item NOR
  // the error row inside it (e.g. "/skills" + commandsError + zero skills:
  // the error row hides itself via `!isSkillsFilter` failing, and there are
  // no skill items either) — an empty bordered box with nothing in it.
  // Gate on whatever will actually render: at least one item, OR the exact
  // condition the error row's own JSX uses below (kept identical on
  // purpose so the two can't drift apart).
  const hasMenuContent =
    slashMenu.slashItems.length > 0 ||
    (slashMenu.commandsError && !slashMenu.isSkillsFilter && !slashMenu.isMentionMode)

  // Deferred item 2: single source of truth for "the menu is actually
  // mounted right now" — reused by the render gate below, the combobox
  // `aria-expanded`/`aria-controls`/`aria-activedescendant` on the textarea,
  // and the scroll-into-view effect just below. Previously this exact
  // three-way `&&` was duplicated inline at the JSX render site; centralizing
  // it means the ARIA attributes literally cannot drift out of sync with
  // what's actually rendered.
  const menuIsRendered = slashMenu.shouldShowSlash && slashMenu.slashOpen && hasMenuContent

  // ARIA clamp (gates 2+4): `slashMenu.slashHighlight` can point at an index
  // that no longer exists in `slashItems` for exactly one render — the
  // highlight is reset to 0 by a useEffect INSIDE useSlashMenu.ts (keyed on
  // the joined item keys), which necessarily runs AFTER the render where the
  // list already narrowed (e.g. a keystroke shrinks slashItems from 5 rows
  // to 2 while slashHighlight was still 3 from the previous, longer list).
  // Between that render and the effect's correction, an UNCLAMPED
  // `aria-activedescendant`/`aria-selected`/`data-highlighted` would
  // reference a row that doesn't exist in the DOM this render — invisible to
  // sighted users (no row LOOKS highlighted either, since the loop below
  // never finds `globalIndex === 3` in a 2-item list) but a broken/absent
  // announcement for screen-reader users, who rely on activedescendant
  // pointing at a real, currently-rendered option. Clamping at render time
  // (rather than waiting for the effect) means these ARIA attributes are
  // always consistent with what's actually in the DOM THIS render,
  // regardless of when the highlight-reset effect catches up. `undefined`
  // when there are zero items — there is no valid index to clamp to.
  const clampedSlashHighlight =
    slashMenu.slashItems.length > 0
      ? Math.min(slashMenu.slashHighlight, slashMenu.slashItems.length - 1)
      : undefined

  // Cap-footer copy (gate 2 LOW) + SR gap (gate 4 MODERATE): single source of
  // truth for the currently-active capped section's overflow state, shared
  // by the VISIBLE "+N more" footer row below AND its sr-only announcement
  // mirror (menuFooterAnnouncement) — computed once so the two can never say
  // different things. Only one capped section can be showing a footer at a
  // time: "/" mode caps skills (commands has no cap), "@" mode caps agents,
  // and the two triggers are mutually exclusive by construction (see
  // useSlashMenu.ts's file header), so `isMentionMode` alone picks the right
  // count.
  const activeSectionHiddenCount = slashMenu.isMentionMode ? slashMenu.agentsHiddenCount : slashMenu.skillsHiddenCount
  // Cap-footer copy (gate 2 LOW): in the "/skills" special-filter state (D9
  // — exact input "/skills" shows every skill, capped, ignoring "skills"
  // itself as a filter string), "keep typing to narrow" is impossible advice
  // — ANY further keystroke changes inputValue away from the exact
  // "/skills" string, which ENDS this special view rather than narrowing it
  // (the next filter is evaluated as a normal skill-name prefix/substring
  // match against the new text, not a continuation of "showing everything").
  // Switch to a purely informational count in that state instead of a CTA
  // that can't do what it claims.
  const footerCopy =
    activeSectionHiddenCount > 0
      ? slashMenu.isSkillsFilter
        ? `Showing ${SECTION_CAP} of ${SECTION_CAP + activeSectionHiddenCount} skills`
        : `+${activeSectionHiddenCount} more — keep typing to narrow`
      : null
  // SR gap (gate 4 MODERATE): sr-only mirror of the same cap state, worded
  // for a listener with no visual context of the row list above it. Reuses
  // the exact same activeSectionHiddenCount/isSkillsFilter inputs as
  // `footerCopy` so the two can never drift on the underlying numbers, even
  // though the wording is deliberately more explicit for a non-visual
  // medium.
  const menuFooterAnnouncement =
    activeSectionHiddenCount > 0
      ? slashMenu.isSkillsFilter
        ? `${SECTION_CAP} of ${SECTION_CAP + activeSectionHiddenCount} skills shown`
        : `${SECTION_CAP} options shown, ${activeSectionHiddenCount} more — keep typing`
      : null

  // Deferred item 2: replaces the old inline ref-callback scroll hack (which
  // called `el.scrollIntoView()` from INSIDE a per-row `ref` callback on
  // every render where that row happened to be the highlighted one — a side
  // effect smuggled into a ref callback, re-running on every re-render of
  // the menu, not just on highlight changes). A `useEffect` keyed on the
  // highlight index (and whether the menu is even mounted) runs exactly
  // once per actual highlight change; the row's `id` (composer-option-N,
  // assigned in the render loop below) makes the lookup a plain
  // `getElementById` — no ref plumbing needed since ids are already unique
  // within this single-instance composer (verified: OmnipusComposer has
  // exactly one call site, ChatScreen.tsx's own render below).
  //
  // Scroll effect (gate 2 LOW): also keyed on the item-SET (joined item
  // keys), not just the numeric highlight index. Without this, a keystroke
  // that changes WHICH items are at each index (e.g. a filter narrowing from
  // one set of skills to a different set, where the highlighted numeric
  // index happens to stay the same) never re-ran this effect — the row now
  // highlighted at that index could be freshly scrolled off-screen with
  // nothing to bring it back into view, since neither `menuIsRendered` nor
  // `slashHighlight` (the number) changed.
  const menuItemKeysForScroll = slashMenu.slashItems.map((i) => i.key).join(',')
  useEffect(() => {
    if (!menuIsRendered) return
    const highlighted = document.getElementById(`composer-option-${slashMenu.slashHighlight}`)
    highlighted?.scrollIntoView({ block: 'nearest' })
  }, [menuIsRendered, slashMenu.slashHighlight, menuItemKeysForScroll])

  // Top-level keydown orchestration.
  //
  // Fix 3 (precedence): menu-close now wins over stream-cancel. Previously
  // cancel-Escape (below) ran BEFORE slashMenu.handleKeyDown, so pressing
  // Escape with the "/" or "@" menu open mid-stream killed the generation
  // AND left the menu open — the menu was unreachable-by-Escape the entire
  // time a turn was streaming. When the menu is showing, Escape now closes
  // ONLY the menu; a second Escape (menu now closed) falls through to the
  // cancel branch exactly as before. Applies identically to "/" and "@"
  // mode — `shouldShowSlash` already covers both.
  //
  // Correctness of the stopPropagation guard: useCancelState also owns a
  // document-level Escape listener (`document.addEventListener('keydown',
  // ...)`, no `capture: true` — see useCancelState's global-escape effect,
  // src/hooks/useCancelState.ts) so a turn can be
  // cancelled even when the input isn't focused. That listener is
  // BUBBLE-phase on `document`. This app mounts via `createRoot(#root)`
  // (src/main.tsx), and React 17+ (including the React 19 used here)
  // delegates synthetic events at the ROOT CONTAINER (`#root`), not at
  // `document` — `document` is a plain DOM ANCESTOR of that container, not
  // where React's own listener lives. When a synthetic handler calls
  // `e.stopPropagation()`, React synchronously calls the underlying native
  // event's `stopPropagation()` too, which happens while the native event is
  // still bubbling — at the `#root` container, on its way up to `document`.
  // That halts the native bubble right there, so useCancelState's
  // document-level listener never sees the keydown at all. No extra
  // isMenuOpen plumbing into useCancelState is needed; `e.stopPropagation()`
  // here is sufficient and provably correct for this app's actual mount
  // topology (verified against src/main.tsx and useCancelState.ts, not
  // assumed).
  // Mid-turn steering send (bugfixes3 — enable mid-turn queued sends in the
  // composer): the actual send/append mechanism used by BOTH (1) Enter
  // pressed mid-stream, below, and (2) the mid-stream Send button rendered
  // next to Stop, further down. Verified (not assumed — read against the
  // installed @assistant-ui/react@0.14.14 / @assistant-ui/core@0.2.10
  // sources under node_modules) that AssistantUI's OWN submission paths are
  // both unusable mid-stream for this app's runtime:
  //   - ComposerPrimitive.Input's internal Enter handling
  //     (node_modules/@assistant-ui/react/dist/primitives/composer/
  //     ComposerInput.js, handleKeyPress) hard-blocks submission whenever
  //     `thread.isRunning && !thread.capabilities.queue`.
  //   - ComposerPrimitive.Root's internal onSubmit (ComposerRoot.js) sends
  //     via `useComposerSend()` (@assistant-ui/core's React hook), which
  //     computes `disabled = !composer.canSend || (thread.isRunning &&
  //     !thread.capabilities.queue)` — so the callback is `null` while
  //     running and `handleSubmit`'s `if (!send) return` silently no-ops.
  //     `form.requestSubmit()` therefore does nothing mid-stream.
  // Our runtime is built via `useExternalStoreRuntime` (an ExternalStoreAdapter
  // — see src/lib/omnipus-runtime.ts). `ExternalStoreThreadRuntimeCore`
  // (node_modules/@assistant-ui/core/dist/runtimes/external-store/
  // external-store-thread-runtime-core.js) hardcodes `queue: false` into
  // `capabilities` unconditionally on every adapter update — there is no
  // ExternalStoreAdapter option that flips it (`unstable_capabilities` only
  // covers `copy`), so `thread.capabilities.queue` can never be true here.
  //
  // The fix bypasses that UI-only isRunning gate by calling the composer
  // RUNTIME's own `.send()` instance method directly (`composerRuntime` from
  // `useComposerRuntime()` above — a `ComposerRuntimeImpl`), NOT the
  // `useComposerSend()` hook `ComposerPrimitive.Send`/`.Root` use internally.
  // `ComposerRuntimeImpl.send()` (node_modules/@assistant-ui/core/dist/
  // runtime/api/composer-runtime.js) forwards straight to
  // `BaseComposerRuntimeCore.send()` (.../runtime/base/
  // base-composer-runtime-core.js), which is gated ONLY by `canSend`
  // (`!isEmpty && !isSendDisabled`) — there is no `isRunning` check anywhere
  // in that call path, and `ExternalStoreThreadRuntimeCore.append()` →
  // `adapter.onNew()` (src/lib/omnipus-runtime.ts's `onNew`, which forwards
  // straight to `useChatStore.getState().sendMessage()`) has none either.
  // This is therefore a genuine, intentional use of a real public API, not a
  // workaround around business logic: the `isRunning` gate exists only to
  // disable the PRESENTATIONAL button/keyboard-submit for runtimes that
  // don't support mid-run sends — ours explicitly does (the gateway queues
  // and injects mid-turn messages server-side), so this drives the send
  // explicitly instead of going through the gated presentational path.
  function submitMidStreamMessage() {
    // Mirrors ComposerPrimitive.Root's onSubmit below: a typed client
    // command (e.g. "/cancel") still intercepts locally instead of steering
    // into the running turn as chat text.
    if (slashMenu.interceptClientCommand()) return
    // BaseComposerRuntimeCore.send() no-ops on its own `canSend` check
    // (empty composer / isSendDisabled, which trims) — no separate emptiness
    // guard is needed before calling it, matching the native Enter/click-Send
    // behavior when idle. But that means a whitespace-only composer makes
    // `.send()` below a no-op too, leaving the composer's actual text
    // untouched — so the mirror-clear must be gated the same way: read
    // whether there's real (non-whitespace) text BEFORE sending, and only
    // clear the slash-menu mirror when there was something to actually send.
    // Unconditionally clearing it here would desync the mirror from the
    // real, unchanged composer text (still whitespace) — hasComposerText
    // (which reads the mirror) would wrongly flip false, hiding the
    // mid-stream Send button/hint even though the composer visually still
    // holds that whitespace.
    const hadSendableText = slashMenu.inputValue.trim().length > 0
    composerRuntime.send()
    if (hadSendableText) {
      // Fix-4 mirror resync — see the identical call + comment in onSubmit
      // below; the runtime clears its own text on a successful send without
      // firing the textarea's onChange.
      slashMenu.onInputChange('')
    }
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Escape' && slashMenu.slashOpen && slashMenu.shouldShowSlash) {
      slashMenu.handleKeyDown(e) // closes the menu (its own Escape branch)
      e.preventDefault()
      e.stopPropagation()
      return
    }

    // US-1.4 / FR-23: Escape cancels a turn.
    // Only morph the button to "Stopping..." if the turn is actively streaming
    // (same logic as the /cancel command and the stop button click). Pressing
    // Escape on a completed turn still marks the message as interrupted via
    // cancelStream() → markLastMessageInterrupted(), but there is no streaming
    // button to morph — setting 'stopping' when isStreaming is false would leave
    // the button stuck because the reset effect does not fire again.
    if (e.key === 'Escape' && (isStreaming || cancelState.stopLabel === 'stopping')) {
      e.preventDefault()
      cancelState.cancelIfStreaming()
      return
    }

    // Mid-turn steering send: Enter pressed WHILE a turn is streaming no
    // longer swallows the keystroke — it sends, and the gateway steers the
    // message into the RUNNING turn (see submitMidStreamMessage's doc
    // comment above for the full, verified mechanism). Still deferred to
    // the slash/mention menu below when it's actually VISIBLE (menu-open
    // Enter selects the highlighted row, not "send") — `slashOpen` alone is
    // not enough (Fix F, bugfixes3 sign-off): it can be true while the menu
    // renders nothing at all (e.g. "/zzz"/"@zzz" typed — no match,
    // `shouldShowSlash` false), so this checks `shouldShowSlash` too. Also
    // deferred on Shift+Enter — that is universally "insert a newline" in
    // this composer (mirrors useSlashMenu.ts's own Fix 8 for the in-menu
    // case), including mid-stream: falls through to the textarea's native
    // handling below via `slashMenu.handleKeyDown(e)` (a no-op when the menu
    // isn't open) and, past that, the internal ComposerPrimitive.Input
    // handling, which inserts the newline.
    //
    // Explicit `inputEnabled` check (defense-in-depth): the textarea's own
    // native `disabled` attribute (set from `!inputEnabled`, see the
    // ComposerPrimitive.Input below) already stops a real browser from ever
    // dispatching a keydown into a read-only composer, but that native
    // attribute is the ONLY thing currently gating this branch against
    // replay / agentRemoved / gave_up-reconnect — none of which clear
    // `isStreaming`. Guarding here too means this branch stays correct even
    // if the textarea is ever rendered without `disabled` wired up (e.g. a
    // future refactor, or a test that dispatches the event directly against
    // the element), instead of relying solely on the DOM to enforce it.
    if (e.key === 'Enter' && isStreaming && inputEnabled && !e.shiftKey && !(slashMenu.slashOpen && slashMenu.shouldShowSlash)) {
      e.preventDefault()
      submitMidStreamMessage()
      return
    }

    slashMenu.handleKeyDown(e)
  }

  // Mid-turn Send affordance gate: the plain Send button rendered next to
  // Stop while streaming (below), and the "Sends into the running response"
  // hint line, both only show once there's actually something to steer.
  // Reuses the slash-menu's own live text mirror (`inputValue`, kept in sync
  // via the textarea's onChange) rather than a fresh subscription — trimmed
  // to match `canSend`'s own emptiness semantics (whitespace-only text is
  // not sendable).
  const hasComposerText = slashMenu.inputValue.trim().length > 0

  return (
    // @container on the composer root: TokenCounter's `@2xl:flex` gate in the
    // context row below reads THIS element's width. It sat on the card while
    // the context row lived inside it; the row now renders outside the card
    // (bare, on the black shell), so the query context moved up here.
    <div
      className="relative @container"
      onDragOver={attachDisabled ? undefined : fileUpload.onDragOver}
      onDragLeave={attachDisabled ? undefined : fileUpload.onDragLeave}
      onDrop={attachDisabled ? undefined : fileUpload.onDrop}
    >
      {/* a11y HIGH: "@" mention-selection announcement. Selecting an agent
          via "@" silently empties the composer and silently changes where
          the next message routes — zero non-visual feedback for a
          screen-reader user. Mirrors the sr-only aria-live pattern in
          ChatScreen (see the message-list's own `aria-live="polite"`
          region near the top of the ChatScreen component) so a mention
          selection reads the same way a new assistant response does.
          Content-change-triggers-announcement: re-selecting the SAME agent
          leaves the text unchanged, so it does not re-announce (see
          useSlashMenu.ts's mentionAnnouncement doc comment) — acceptable,
          nothing actually changed. */}
      <div aria-live="polite" aria-atomic="true" className="sr-only" data-testid="agent-mention-announcement">
        {slashMenu.mentionAnnouncement && <span>Now chatting with {slashMenu.mentionAnnouncement}</span>}
      </div>

      {/* SR gap (gate 4 MODERATE): the "Commands unavailable" error row
          inside the listbox below is deliberately `role="presentation"` —
          excluded from the listbox's accessible option children (see that
          row's own comment) — which means it is entirely silent to screen
          readers: a sighted user sees the italic error text, but nothing is
          ever announced. Mirror it (and the cap-footer's overflow state —
          see `menuFooterAnnouncement` above) into a dedicated sr-only
          `role="status"` region OUTSIDE the listbox, reusing the same
          sr-only live-region pattern as the "@" mention announcement just
          above, so both states reach assistive tech without changing a
          single visible pixel of the rows themselves. Unconditionally
          mounted (content conditionally populated) — a live region must
          already exist in the DOM before its content changes for most
          screen readers to reliably announce the change; a freshly-mounted
          region with content already inside it is not guaranteed to fire.
          `role="status"` carries an implicit `aria-live="polite"` +
          `aria-atomic="true"`, matching the pattern above explicitly for
          clarity. */}
      <div role="status" aria-atomic="true" className="sr-only" data-testid="slash-menu-status">
        {menuIsRendered && slashMenu.commandsError && !slashMenu.isSkillsFilter && !slashMenu.isMentionMode && (
          <span>Commands unavailable</span>
        )}
        {menuIsRendered && menuFooterAnnouncement && <span>{menuFooterAnnouncement}</span>}
      </div>

      {fileUpload.isDragging && !attachDisabled && (
        <div className="absolute inset-0 z-50 flex items-center justify-center bg-[var(--color-primary)]/80 border-2 border-dashed border-[var(--color-accent)] rounded-lg">
          <p className="text-[var(--color-accent)] font-medium">Drop files here</p>
        </div>
      )}

      {/* Fix 2: multi-phase reconnect banner.
          gave_up   → error banner with "Reconnect now" CTA (input locked).
          reconnecting/slow → amber pulsing indicator with attempt counter.
          null (connected) → nothing shown. */}
      {reconnectPhase === 'gave_up' && (
        <div
          data-testid="reconnect-banner"
          className="mb-2 rounded-lg px-3 py-2 bg-[var(--color-error)]/10 border border-[var(--color-error)]/20 flex items-center gap-2"
        >
          <WifiSlash size={14} className="text-[var(--color-error)] shrink-0" />
          <span className="text-xs text-[var(--color-error)] flex-1">
            Connection lost after all retry attempts.
          </span>
          <button tabIndex={0}
            type="button"
            onClick={reconnect}
            className="flex items-center gap-1 px-2 py-1 rounded text-xs font-medium bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30 transition-colors shrink-0"
            aria-label="Reconnect now"
          >
            <ArrowClockwise size={12} weight="bold" />
            Reconnect now
          </button>
        </div>
      )}
      {showTransientDisconnect && (reconnectPhase === 'reconnecting' || reconnectPhase === 'slow') && (
        <div
          data-testid="reconnect-banner"
          className="mb-2 text-xs text-[var(--color-warning)] flex items-center gap-1.5"
        >
          <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-warning)] inline-block animate-pulse" />
          {reconnectPhase === 'slow'
            ? `Reconnecting… (attempt ${reconnectAttempt} — slow retry)`
            : `Reconnecting… (attempt ${reconnectAttempt})`}
        </div>
      )}
      {showTransientDisconnect && !isConnected && reconnectPhase === null && (
        <div data-testid="reconnect-banner" className="mb-2 text-xs text-[var(--color-error)] flex items-center gap-1">
          <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-error)] inline-block" />
          Disconnected — reconnecting...
        </div>
      )}
      {/* Fix 3: outbound queue indicator — shown while messages are buffered
          (outboundQueue, while offline) or actively being drained one at a
          time after reconnect (pendingDrainQueue — see maybeDrainNext in
          store/chat.ts). Without the pendingDrainQueue branch this banner
          would disappear the instant reconnect fires even though several
          messages are still waiting for their turn to go out.
          Task 4 fix: on a flaky connection both arrays can be simultaneously
          non-empty (e.g. some messages bounced back to outboundQueue on a
          mid-drain disconnect while others are still parked in
          pendingDrainQueue — see the chat.ts store's own doc comments on
          drainOutboundQueue/maybeDrainNext). The previous if/else only ever
          showed ONE of the two counts, undercounting the true total still
          queued. Sum both counts whenever both are non-empty. */}
      {(outboundQueue.length > 0 || pendingDrainQueue.length > 0) && (
        <div
          data-testid="outbound-queue-indicator"
          className="mb-2 text-xs text-[var(--color-warning)] flex items-center gap-1.5"
        >
          <Clock size={12} className="shrink-0" />
          {(() => {
            const total = outboundQueue.length + pendingDrainQueue.length
            if (outboundQueue.length > 0 && pendingDrainQueue.length > 0) {
              return total === 1
                ? '1 message queued — will send on reconnect'
                : `${total} messages queued — will send on reconnect`
            }
            if (outboundQueue.length > 0) {
              return outboundQueue.length === 1
                ? '1 message queued — will send on reconnect'
                : `${outboundQueue.length} messages queued — will send on reconnect`
            }
            return pendingDrainQueue.length === 1
              ? '1 queued message sending…'
              : `${pendingDrainQueue.length} queued messages sending…`
          })()}
        </div>
      )}

      {/* Slash command + skills + "@" agent-mention partitioned dropdown
          (FR-005). One container/list renders three different sections
          depending on the trigger: "/" opens Commands+Skills, "@" opens
          Agents (see useSlashMenu.ts's isMentionMode) — the two triggers
          are mutually exclusive by construction, so `slashItems` is always
          either [commands, skills] or [agents], never a mix (J.4
          correction, bugfixes3 sign-off — this comment previously only
          mentioned the "/" side).
          F6: unified slashItems.map() — emits a section header on each section
          transition, making the `section` field load-bearing and removing the
          duplicate render blocks + the off-by-one `globalIndex` variable.
          FR-014/R3: skill items show their argument_hint as muted help text.
          Fix E / deferred item 2: gated on `menuIsRendered` (declared above,
          alongside `slashMenu`), which folds in `hasMenuContent` —
          `shouldShowSlash` alone can be true with nothing to actually
          render; see that constant's own comment.

          Deferred item 2 (W3C APG combobox pattern): this container is the
          combobox's `listbox` — `role="listbox"`, a stable `id` the
          textarea's `aria-controls` points to, and an `aria-label` since the
          listbox has no visible heading of its own (its rows have section
          headers, but the LISTBOX itself doesn't). Rows below are
          `role="option"` with `tabIndex={-1}` (NOT 0 — see the row's own
          comment) and `aria-selected`; the textarea (ChatScreen.tsx's
          ComposerPrimitive.Input, below) carries
          `aria-activedescendant={composer-option-${slashHighlight}}` so
          screen readers announce the highlighted row without moving DOM
          focus off the input, per APG's combobox-with-listbox-popup
          pattern. */}
      {menuIsRendered && (
        <div
          data-testid="slash-menu"
          id="composer-slash-menu"
          role="listbox"
          aria-label="Commands, skills and agents"
          // max-h + scroll: the menu opens UPWARD from the composer, so a tall
          // list (many commands + skills) pushed its top rows above the
          // viewport. 40dvh caps it to the visible area; overflow-y scrolls.
          className="mb-2 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-0)] overflow-hidden shadow-2xl max-h-[40dvh] overflow-y-auto overscroll-contain"
        >
          {/* LOW S8: fetchCommands('web') errored — surface the gap instead
              of letting every backend command silently vanish from the
              palette. Hidden during the "/skills" filter (D9 already hides
              the whole Commands section there, so an error row would be a
              non-sequitur). Muted styling matches SearchModal's own
              query-error row (src/components/search/SearchModal.tsx).
              Fix 1: also hidden in "@" mention mode — a commands-fetch
              error has nothing to do with the agent-mention menu, and this
              render gate must match useSlashMenu's own shouldShowSlash
              fallback (useSlashMenu.ts), which already excludes
              isMentionMode from the "keep the menu open on error" carve-out.
              Without this clause the row leaked into the "@" menu whenever
              the commands query happened to be erroring, even though the
              "@" menu has nothing to do with commands.
              Deferred item 2: `role="presentation"` — this row is
              informational text, not a selectable listbox option; without
              this it would silently pollute the `listbox`'s accessible
              children with a non-option row. */}
          {slashMenu.commandsError && !slashMenu.isSkillsFilter && !slashMenu.isMentionMode && (
            <div
              data-testid="slash-commands-error"
              role="presentation"
              className="px-3 py-2 text-xs text-[var(--color-error)] italic"
            >
              Commands unavailable
            </div>
          )}
          {slashMenu.slashItems.map((item, globalIndex) => {
            const prevSection = globalIndex > 0 ? slashMenu.slashItems[globalIndex - 1].section : null
            const isFirstInSection = item.section !== prevSection
            const nextSection = globalIndex < slashMenu.slashItems.length - 1 ? slashMenu.slashItems[globalIndex + 1].section : null
            const isLastInSection = item.section !== nextSection
            // Deferred item 3: the section's overflow count, rendered as a
            // muted footer row right after that section's last visible row.
            // Only skills/agents are capped (commands has no cap — see
            // useSlashMenu.ts) — 0 for commands, so the footer simply never
            // renders there.
            const sectionHiddenCount =
              item.section === 'skills' ? slashMenu.skillsHiddenCount :
              item.section === 'agents' ? slashMenu.agentsHiddenCount : 0
            return (
              <React.Fragment key={item.key}>
                {isFirstInSection && (
                  <div
                    role="presentation"
                    className={cn(
                      'px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]',
                      globalIndex === 0
                        ? 'border-b border-[var(--color-border)]'
                        : 'border-t border-[var(--color-border)]',
                    )}>
                    {item.section === 'commands' ? 'Commands' : item.section === 'skills' ? 'Skills' : 'Agents'}
                  </div>
                )}
                <button
                  type="button"
                  id={`composer-option-${globalIndex}`}
                  role="option"
                  aria-selected={globalIndex === clampedSlashHighlight}
                  // Deferred item 2 (APG combobox pattern, amends the eb4de2a2
                  // blanket tabIndex=0 stamp for THIS widget specifically):
                  // options inside a combobox's listbox popup are NOT
                  // separately tabbable — focus stays on the textarea the
                  // whole time the menu is open (that's the entire point of
                  // aria-activedescendant), and ArrowUp/ArrowDown move the
                  // active-descendant highlight without ever moving real DOM
                  // focus. tabIndex=0 here was actively harmful: Tab-focusing
                  // a row started the textarea's 150ms blur-close timer
                  // (onInputBlur, useSlashMenu.ts), which unmounted the menu
                  // out from under the just-focused row and dropped focus to
                  // `body` — a keyboard dead end. Selection still works via
                  // onMouseDown (mouse) and Enter (keyboard, through the
                  // textarea's own onKeyDown → slashMenu.handleKeyDown) —
                  // neither depends on this button ever receiving focus.
                  tabIndex={-1}
                  // "@" mention menu rows only — mirrors the slash-command/skill
                  // rows' shared markup exactly, so this testid is additive
                  // rather than a fork of the row.
                  data-testid={item.section === 'agents' ? 'agent-mention-item' : undefined}
                  // Fix 11: semantic highlight marker — lets a test assert the
                  // highlighted row without depending on the visual class
                  // string below (which stays purely presentational; `undefined`
                  // when not highlighted, so the attribute doesn't render at all
                  // rather than serializing as `data-highlighted="false"`).
                  data-highlighted={globalIndex === clampedSlashHighlight || undefined}
                  className={cn(
                    'w-full flex items-baseline gap-3 px-3 py-2 text-left transition-colors',
                    globalIndex === clampedSlashHighlight
                      ? 'bg-[var(--color-accent)]/10 text-[var(--color-secondary)]'
                      : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
                  )}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    item.onSelect()
                    // Click-selecting a slash item must land focus back in the
                    // input (keyboard selection never loses it; mouse would).
                    focusChatInput()
                  }}
                  onMouseEnter={() => slashMenu.onHoverItem(globalIndex)}
                >
                  {/* Agent rows only — same avatar-dot markup as AgentPicker's
                      DropdownMenuItem rows (composer/AgentPicker.tsx) so the
                      mention menu and the picker dropdown read as the same
                      "agent row" everywhere in the composer.
                      Fix 9: aria-hidden — the initial/icon is decorative; the
                      row BUTTON's accessible name should come from the label/
                      description text, not this dot's text content. Initial is
                      derived from `item.agentName` (astral-safe via
                      `initialOf`), not `label.charAt(1)` — the old approach
                      assumed `label` was always "@" + exactly one BMP
                      character, which breaks for a name whose first character
                      is outside the BMP (e.g. an emoji). */}
                  {item.section === 'agents' && (
                    <div
                      aria-hidden="true"
                      className="w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold shrink-0"
                      style={{ backgroundColor: item.agentColor ?? 'var(--color-surface-3)' }}
                    >
                      {item.agentIcon
                        ? <IconRenderer icon={item.agentIcon} size={11} />
                        : initialOf(item.agentName ?? '')}
                    </div>
                  )}
                  {/* Fixed-width label column so descriptions align across rows
                      (a two-column table, not per-row flow). 9.5rem fits the
                      longest current label; truncate guards outliers. */}
                  <span className="w-[9.5rem] shrink-0 truncate font-mono text-xs text-[var(--color-accent)]">{item.label}</span>
                  <span className="text-[11px] flex-1 min-w-0 truncate">{item.description}</span>
                  {/* FR-014/R3: show argument_hint as muted help text for skills;
                      SD-C7 extends this to `delivery: agent` commands (e.g. /goal, /loop). */}
                  {(item.section === 'skills' || item.section === 'commands') && item.argumentHint && (
                    <span className="ml-auto text-[10px] text-[var(--color-muted)] opacity-70 font-mono shrink-0">
                      {item.argumentHint}
                    </span>
                  )}
                  {/* Mirrors AgentPicker's "active" marker (composer/AgentPicker.tsx) */}
                  {item.section === 'agents' && item.isActiveAgent && (
                    <span className="ml-auto shrink-0 text-[var(--color-success)] text-[10px]">active</span>
                  )}
                </button>
                {/* Deferred item 3: "+N more" footer — rendered right after
                    the LAST visible row of a capped section that still has
                    overflow. Deliberately `role="presentation"` and OUTSIDE
                    `slashMenu.slashItems` entirely (not just visually
                    de-emphasized) — it is not part of `globalIndex`'s
                    sequence, so it can never receive an
                    `id="composer-option-N"`, can never be `aria-selected`,
                    and ArrowUp/ArrowDown (which cycle modulo
                    `slashItems.length`) can never land the highlight on it. */}
                {isLastInSection && sectionHiddenCount > 0 && (
                  <div
                    data-testid="slash-menu-footer"
                    role="presentation"
                    className="px-3 py-1.5 text-[10px] text-[var(--color-muted)] italic"
                  >
                    {/* Cap-footer copy fix (gate 2 LOW): `footerCopy` (declared
                        above, alongside `menuIsRendered`) already accounts for
                        the "/skills" special-filter state, where "keep typing
                        to narrow" is impossible advice — see its own comment.
                        `sectionHiddenCount > 0` here is guaranteed to equal the
                        `activeSectionHiddenCount` `footerCopy` was computed
                        from (only one capped section can be rendering a footer
                        at a time — "/" caps skills, "@" caps agents, and the
                        two triggers are mutually exclusive), so reusing the
                        single shared value here (rather than re-deriving the
                        copy per-row) is exactly correct, not a coincidence. */}
                    {footerCopy}
                  </div>
                )}
              </React.Fragment>
            )
          })}
        </div>
      )}

      {/* Context row — per-message scope (attach · agent · model · tokens).
          Renders BARE on the black shell, deliberately outside the card
          frame (operator direction: only the input surface reads as a card;
          the scope controls float above it). Agent/model/tokens relocated
          here from the workspace top-bar (Gestalt proximity); attach moved
          up from the old input row. */}
      {/* py-0.5 (not overflow-clipping padding-bottom only): the row uses
          overflow-x-auto, which also clips vertically — without symmetric
          vertical breathing room the focus ring on the controls was cut off
          at the top. Compact h-7 controls + py-0.5 keeps the ring visible. */}
      <div
        className="flex items-center gap-1.5 min-w-0 overflow-x-auto px-1 py-1"
        style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as React.CSSProperties}
      >
        {/* Agent → model. Attach lives INSIDE the input card (leading the
            textarea, ChatGPT/Claude-style) — it was visually lost up here. */}
        <AgentPicker disabled={agentRemoved} tabIndex={3} />
        <ModelPicker disabled={agentRemoved} tabIndex={4} />
        <span className="flex-1" />
        {/* Token counter — status; hidden below @2xl of the composer root's
            @container (~42rem). */}
        <TokenCounter className="hidden @2xl:flex" />
      </div>

      {/* Composer card — now just the input surface: a single
          ComposerPrimitive.Root (textarea + send/stop on one items-end
          baseline) plus pending-attachment chips. The context row above and
          the activity pills below render outside the frame on the shell
          background. */}
      <div
        data-testid="composer-card"
        // focus-within gold border: the textarea opts out of the central ring
        // (data-no-focus-ring) BECAUSE this card is its focus surface — this
        // class is the promised replacement (WCAG 2.4.7). Border swap, not an
        // outline: the card's overflow-hidden would clip an inset outline.
        className="rounded-2xl border border-[var(--color-border)] focus-within:border-[var(--color-accent)] bg-[var(--color-surface-2)] overflow-hidden transition-colors"
      >
        {/* Input row — single ComposerPrimitive.Root, items-end: textarea and
            send/stop share one flex container + one bottom baseline. */}
        <ComposerPrimitive.Root
          className="flex items-end gap-2 p-2"
          onSubmit={(e) => {
            // Mid-turn steering: this native form-submit path is NEVER
            // actually reached while streaming (verified — see
            // submitMidStreamMessage's doc comment above): Enter mid-stream
            // is fully handled by handleKeyDown's own branch (which calls
            // e.preventDefault(), so ComposerPrimitive.Input's internal
            // handleKeyPress — the only thing that would call
            // form.requestSubmit() — never runs), and the mid-stream Send
            // button below is `type="button"`, so a click never fires this
            // form's submit event either. The isStreaming preventDefault
            // that used to sit here was therefore removed for coherence, not
            // because it was blocking a live path — leaving it in place
            // would misleadingly suggest Enter/Send still route through here
            // while a turn is running.
            // Send-path interception: if the typed text is exactly a client-delivery
            // slash command (e.g. "/new", "/help", "/model", "/cancel"), handle it
            // locally and prevent it from reaching the backend. This converges the
            // typed+Enter path with the palette selection path.
            if (slashMenu.interceptClientCommand()) {
              e.preventDefault()
              return
            }
            // Fix 4: resync the text mirror after a real send. assistant-ui's
            // composerRuntime clears its own text on a successful submit
            // WITHOUT firing the textarea's onChange (no synthetic change
            // event is dispatched for the runtime-driven clear), so
            // `slashMenu.inputValue` — which only updates via onInputChange —
            // kept the just-sent text (e.g. "@mia hello everyone") even
            // though the visible textarea was now empty. A subsequent
            // ArrowDown in the (visually empty) textarea then read the STALE
            // "@..." mirror, reopened the full agent-mention menu, and Enter
            // silently switched the active agent with no text on screen to
            // explain why. Only reached here when the send actually
            // proceeded (neither the isStreaming guard nor
            // interceptClientCommand() intercepted it above), so this can't
            // clear the mirror out from under a blocked/intercepted send.
            slashMenu.onInputChange('')
          }}
        >
          {/* Attach — leading the input, ChatGPT/Claude-style. Plus (add
              context: files, images, anything) instead of the email-era
              paperclip. items-end row → mb-1.5 = (40px single-line input − 28px)/2 centers it against
              the single-line textarea baseline. */}
          <ComposerPrimitive.AddAttachment
            disabled={attachDisabled}
            tabIndex={5}
            className="shrink-0 h-7 w-7 mb-1.5 rounded-full flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-3)] transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
            aria-label="Add files or context"
            title="Add files or context"
          >
            <Plus size={16} />
          </ComposerPrimitive.AddAttachment>

          {/* ADR-051 Rev 4 (Slice H) — attach an existing workspace-library
              entry without re-uploading (FR-022). Disabled alongside the file
              attach button in read-only / disconnected sessions. */}
          <ComposerMediaLibraryButton disabled={attachDisabled} tabIndex={5} />

          {/* Ghost text wrapper — positioned overlay approach */}
          <div className="relative flex-1 min-w-0">
            <ComposerPrimitive.Input
              ref={chatInputRef}
              data-testid="chat-input"
              data-no-focus-ring
              // Composer tab ring — see the full map in ChatControls.tsx.
              tabIndex={2}
              placeholder={agentRemoved ? 'Agent has been removed — this session is read-only' : composerPlaceholder(isConnected || reconnectPhase === 'reconnecting' || reconnectPhase === 'slow', isStreaming, isReplaying, activeAgentName, reconnectPhase === 'gave_up')}
              // FR-3a: the slash menu must be reachable mid-stream, which means the
              // textarea has to accept keystrokes during streaming. Submission is
              // blocked elsewhere: the ComposerPrimitive.Root's onSubmit handler
              // above preventDefaults while isStreaming, and the composer's
              // onKeyDown handler (handleKeyDown) swallows Enter while streaming
              // unless the slash menu is open. So gate visual-only
              // (cursor-not-allowed/opacity) on isStreaming via the className
              // below, not the disabled attribute.
              disabled={!inputEnabled}
              // aria-disabled (gate 4 LOW): deliberately does NOT include
              // isStreaming. `disabled` (native, above) is likewise gated on
              // `!inputEnabled` alone — the textarea genuinely accepts
              // keystrokes mid-stream by design (FR-3a: the slash menu must
              // stay reachable while a turn is streaming). Including
              // isStreaming here previously told assistive tech the input
              // was disabled mid-turn even though it demonstrably was not —
              // a screen-reader user would be misinformed that they could
              // not type, when they could. Submission-blocking during a
              // stream is conveyed by the Stop button replacing Send (see
              // the ternary below), not by marking the TEXT INPUT disabled.
              aria-disabled={!inputEnabled || undefined}
              rows={1}
              cancelOnEscape={false}
              className={cn(
                // Transparent + borderless: the card is the visible surface now,
                // so the textarea blends in (flat) instead of drawing its own box.
                // `block`: textareas are inline-block by default, which leaves a
                // ~6px descender line-box gap below the element inside its block
                // wrapper — the wrapper (not the textarea) is what items-end
                // aligns, so the gap read as send sitting below the input.
                'block w-full resize-none bg-transparent px-3 py-2 text-sm text-[var(--color-secondary)] outline-none',
                'placeholder:text-[var(--color-muted)] min-h-[24px] max-h-[200px] leading-6 overflow-hidden',
                'focus:outline-none focus:ring-0',
                (!inputEnabled || isStreaming) && 'opacity-60 cursor-not-allowed',
              )}
              aria-label="Message input"
              // Deferred item 2 (W3C APG combobox pattern — combobox with
              // listbox popup). Verified against node_modules/@assistant-ui/
              // react/dist/primitives/composer/ComposerInput.js: the real
              // ComposerPrimitive.Input destructures a fixed, small prop list
              // (autoFocus/asChild/render/disabled/onChange/onKeyDown/onPaste/
              // onSelect/submitOnEnter/submitMode/cancelOnEscape/unstable_*/
              // addAttachmentOnPaste) and spreads everything ELSE — including
              // `role` and any `aria-*` prop — via `...rest` straight onto the
              // underlying `TextareaAutosize` element (only overridden if this
              // app used `Unstable_TriggerPopoverRoot`, which it doesn't — that
              // context is optional and unset here). So `role="combobox"` and
              // the aria-* props below reach the real DOM node unmodified; no
              // fallback is needed.
              role="combobox"
              aria-expanded={menuIsRendered}
              aria-controls={menuIsRendered ? 'composer-slash-menu' : undefined}
              // Undefined (not rendered) when the menu is closed OR has zero
              // navigable items (the LOW S8 commandsError carve-out can leave
              // shouldShowSlash true with slashItems empty — see
              // useSlashMenu.ts's Fix D) — there is no real option for the id
              // to point at in either case.
              aria-activedescendant={
                menuIsRendered && clampedSlashHighlight !== undefined
                  ? `composer-option-${clampedSlashHighlight}`
                  : undefined
              }
              aria-autocomplete="list"
              onChange={(e) => {
                const val = (e.target as HTMLTextAreaElement).value
                slashMenu.onInputChange(val)
                if (val.length > 1_000_000) {
                  if (!hasWarnedLargeInput.current) {
                    hasWarnedLargeInput.current = true
                    useUiStore.getState().addToast({
                      message: `Large input (${(val.length / 1_000_000).toFixed(1)}MB). This may be slow to process.`,
                      variant: 'default',
                    })
                  }
                } else {
                  hasWarnedLargeInput.current = false
                }
              }}
              onKeyDown={handleKeyDown}
              onBlur={slashMenu.onInputBlur}
              onPaste={fileUpload.onPaste}
            />
            {/* Ghost text overlay — shown when value is exactly `/<skillId> ` after skill selection.
                F3-frontend/FR-006/R3: shows the skill's argument_hint when declared, else `<message>`.
                The hint comes from the skill selected from the menu. */}
            {slashMenu.showGhostText && (
              <div
                aria-hidden="true"
                className="pointer-events-none absolute inset-0 px-3 py-2.5 text-sm leading-6 flex items-start"
                data-testid="ghost-text"
              >
                <span className="invisible whitespace-pre">{slashMenu.inputValue}</span>
                <span className="text-[var(--color-muted)] opacity-60">
                  {slashMenu.ghostText}
                </span>
              </div>
            )}
          </div>

          {isStreaming || cancelState.stopLabel === 'stopping' ? (
            // tabIndex={6}: this button replaces Send in the exact same ring
            // slot mid-stream (see the composer tab-ring map in
            // ChatControls.tsx) — it must keep Send's slot, not default to 0,
            // or the ring silently drops a stop the whole time a turn is
            // streaming. Mid-turn steering (below): the plain Send button
            // that now sits next to Stop shares this same slot 6 (both are
            // "the send/stop action cluster") — cancel must stay reachable
            // in exactly one keystroke even when steering is also available,
            // so Stop keeps its own dedicated element rather than being
            // folded into a menu/toggle.
            <>
              <button tabIndex={6}
                type="button"
                data-testid="stop-btn"
                onClick={() => {
                  // EC-15 / FR-21: cancelUnconditional() sets the label synchronously
                  // so the UI updates within the same React render tick, before the
                  // cancel network round-trip starts (no perceived latency). It does
                  // NOT guard on isStreaming — see useCancelState's doc comment for
                  // why guarding here would silently no-op on the render/click race.
                  cancelState.cancelUnconditional()
                }}
                className={cn(
                  // h-9 + mb-0.5: same footprint as the send button it replaces
                  // mid-stream (keeps the row from jumping on morph).
                  'shrink-0 rounded-lg mb-0.5 flex items-center justify-center transition-colors',
                  cancelState.stopLabel === 'stopping'
                    ? 'px-3 h-9 gap-1.5 text-xs font-medium bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30'
                    : 'w-9 h-9',
                  isStreaming
                    ? 'bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30'
                    : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-wait',
                )}
                aria-label={cancelState.stopLabel === 'stopping' ? 'Stopping...' : 'Stop generation'}
                title="Stop (Escape)"
              >
                <Stop size={15} weight="fill" />
                {cancelState.stopLabel === 'stopping' && <span>Stopping...</span>}
              </button>
              {/* Mid-turn steering Send — a PLAIN button, deliberately not
                  ComposerPrimitive.Send: verified (see submitMidStreamMessage's
                  doc comment above) that the primitive's own useComposerSend()
                  hook hard-disables it whenever thread.isRunning &&
                  !thread.capabilities.queue, and our ExternalStoreAdapter
                  runtime always reports capabilities.queue === false — so the
                  primitive would render permanently disabled here no matter
                  what `disabled` prop it's given. `type="button"` (not
                  "submit"): a click must NOT fire ComposerPrimitive.Root's
                  form submit event — submitMidStreamMessage() drives the send
                  directly. Only rendered once there's text to steer — an
                  empty composer has nothing to send, and Stop alone already
                  covers "I have nothing more to add, stop the response." */}
              {hasComposerText && (
                <button tabIndex={6}
                  type="button"
                  data-testid="chat-send-mid-stream"
                  disabled={!inputEnabled}
                  onClick={submitMidStreamMessage}
                  className={cn(
                    'shrink-0 w-9 h-9 mb-0.5 rounded-lg flex items-center justify-center transition-colors',
                    inputEnabled
                      ? 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent-hover)]'
                      : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-not-allowed',
                  )}
                  aria-label="Send into the running response"
                  title="Send into the running response"
                  aria-describedby="mid-stream-send-hint"
                >
                  <PaperPlaneRight size={15} weight="bold" />
                </button>
              )}
            </>
          ) : (
            // FR-I-014: also disabled during replay so user cannot send out-of-order.
            // Fix 3: when reconnecting (fast or slow), allow send — messages go to
            // the outbound queue and drain automatically on reconnect.
            //
            // Native ComposerPrimitive.Send (bugfixes3 sign-off — J correction;
            // FIXED bugfixes3 deferred item 1, below): verified against
            // node_modules/@assistant-ui/react/dist/utils/createActionButton.js +
            // primitives/composer/ComposerSend.js — the Send button is built via
            // createActionButton, which renders a plain `type="button"` whose
            // onClick is `composeEventHandlers(primitiveProps.onClick, callback)`
            // (from `@radix-ui/primitive`, checkForDefaultPrevented=true by
            // default — verified in node_modules/@radix-ui/primitive/dist/
            // index.js): OUR `onClick` prop below runs FIRST; `callback` (which
            // invokes `composer.send()` DIRECTLY, NOT `form.requestSubmit()`) only
            // runs afterward if our handler did NOT call `e.preventDefault()`. A
            // mouse click on Send therefore never fires this ComposerPrimitive.
            // Root's onSubmit above — Send and Enter do NOT share a code path —
            // so the interception below intentionally duplicates (not reuses)
            // onSubmit's `interceptClientCommand()` check for this button's own
            // click path, converging typed-"/new"-then-click-Send with
            // typed-"/new"-then-Enter.
            //
            // No isStreaming guard here (unlike onSubmit's own preventDefault
            // check): this button is swapped for the Stop button above whenever
            // `isStreaming || cancelState.stopLabel === 'stopping'` is true, so
            // ComposerPrimitive.Send is never even mounted while a turn is
            // streaming — verified against the ternary a few lines up — there is
            // no click path to guard against.
            //
            // The Fix-4 text-mirror resync (onSubmit, above) is NOT duplicated
            // here either: it is unconditionally handled by useSlashMenu.ts's Fix
            // A, which subscribes directly to composerRuntime so `inputValue`
            // stays correct after ANY out-of-band clear — click-Send included,
            // not just the keyboard path. Send still auto-disables when the
            // composer is empty (no text and no attachments) via the runtime's
            // canSend, so no manual empty-check is needed here either.
            <ComposerPrimitive.Send
              disabled={!inputEnabled || isReplaying}
              data-testid="chat-send"
              tabIndex={6}
              onClick={(e) => {
                if (slashMenu.interceptClientCommand()) {
                  e.preventDefault()
                }
              }}
              className={cn(
                'shrink-0 w-9 h-9 mb-0.5 rounded-lg flex items-center justify-center transition-colors',
                inputEnabled && !isReplaying
                  ? reconnectPhase === 'reconnecting' || reconnectPhase === 'slow'
                    // Muted accent while reconnecting — functional but visually signals
                    // the message will queue rather than send immediately.
                    ? 'bg-[var(--color-accent)]/50 text-[var(--color-primary)] hover:bg-[var(--color-accent)]/70'
                    : 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent-hover)] disabled:bg-[var(--color-surface-3)] disabled:text-[var(--color-muted)] disabled:cursor-not-allowed'
                  : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-not-allowed',
              )}
              aria-label={
                reconnectPhase === 'reconnecting' || reconnectPhase === 'slow'
                  ? 'Queue message (will send on reconnect)'
                  : 'Send message'
              }
              aria-disabled={isReplaying || undefined}
            >
              <PaperPlaneRight size={15} weight="bold" />
            </ComposerPrimitive.Send>
          )}
        </ComposerPrimitive.Root>

        {/* Mid-turn steering affordance: the empty-composer case already says
            "Waiting for response..." via the placeholder (composerPlaceholder,
            above) — but a NATIVE placeholder only shows on an empty input, so
            once the user has actually typed something mid-stream it goes
            invisible right when the affordance matters most. This small,
            muted line (tokens only, no emoji) fills that gap: it appears
            only once there's live text to steer with, right where the Send
            button next to Stop just appeared. Microcopy unified with that
            button's own title/aria-label (one phrase, three surfaces); `id`
            is targeted by the button's `aria-describedby` above so a
            screen-reader user gets the same explanation the sighted hint
            line provides. Safe to reference unconditionally from the button
            (no dangling id): the button only renders when hasComposerText is
            also true (its own parent condition additionally requires
            isStreaming), which is exactly this line's render condition too. */}
        {isStreaming && hasComposerText && (
          <div
            id="mid-stream-send-hint"
            data-testid="mid-stream-send-hint"
            className="px-3 pb-1.5 -mt-1 text-[10px] text-[var(--color-muted)]"
          >
            Send into the running response
          </div>
        )}

        {/* Pending attachments — native AssistantUI composer attachments. Shows a
            chip for each attached file (via the attach (+) button, drag-drop, or
            paste); the AttachmentAdapter (src/lib/attachment-adapter.ts) uploads
            them on send and threads the media:// ref into our transport via onNew.
            LibraryAttachmentChips renders staged workspace-library reuse refs
            (ADR-051 Rev 4 / Slice H) alongside them. */}
        <div className="flex flex-wrap gap-2 px-2 empty:hidden [&:has(*)]:pb-2">
          <ComposerPrimitive.Attachments components={{ Attachment: ComposerAttachmentChip }} />
          <LibraryAttachmentChips />
        </div>

      </div>

      {/* Live background activity (delegate spans + background bash runs) —
          pills render BELOW the card, bare on the shell background (operator
          direction), Claude-Code style. Opens the ActivityPanel slide-out on
          click. ActivityBar renders null when idle, so `empty:hidden`
          collapses this wrapper — no stray padding when nothing runs. */}
      <div className="px-1 pt-1.5 empty:hidden">
        <ActivityBar />
      </div>

      {/* Harmful-file upload double-confirm — replaces the native window.confirm pair.
          Stage 1 warns and lists the flagged files; stage 2 is the second
          confirmation. Files are only attached after the user confirms stage 2. */}
      <AlertDialog
        open={fileUpload.harmfulConfirm !== null}
        onOpenChange={(open) => {
          if (!open) fileUpload.dismissHarmfulConfirm()
        }}
      >
        <AlertDialogContent>
          {fileUpload.harmfulStage === 1 ? (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>Potentially harmful file(s)</AlertDialogTitle>
                <AlertDialogDescription>
                  {fileUpload.harmfulConfirm
                    ? `${fileUpload.harmfulConfirm.harmfulNames.join(', ')} may be potentially harmful file(s).\n\nAre you sure you want to upload?`
                    : ''}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  onClick={fileUpload.advanceHarmfulStage}
                >
                  Continue
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          ) : (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>Confirm upload</AlertDialogTitle>
                <AlertDialogDescription>
                  {fileUpload.harmfulConfirm
                    ? `Please confirm again: Upload ${fileUpload.harmfulConfirm.harmfulNames.length} potentially harmful file(s)?`
                    : ''}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  onClick={fileUpload.confirmHarmfulUpload}
                >
                  Upload
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          )}
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

// ── Welcome state ─────────────────────────────────────────────────────────────

function WelcomeState({ hasAgent }: { hasAgent: boolean }) {
  return (
    <div className="flex flex-col items-center justify-center h-full min-h-[60vh] gap-8 p-8">
      <div className="flex flex-col items-center gap-6 text-center max-w-md">
        <img
          src={OmnipusAvatar}
          alt="omnipus.ai mark"
          className="h-20 w-20 drop-shadow-lg"
        />
        <div>
          <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)] mb-2">
            Welcome to <Wordmark className="text-2xl" />
          </h1>
          <p className="text-[var(--color-muted)] text-sm">
            {hasAgent
              ? 'Your agent is ready. Start a conversation below.'
              : 'Select an agent in the session bar to get started.'}
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
  // ADR-049 D6/US-12/SD-C9: loop indicator state, same session-scoped
  // foreground field as rateLimitEvent above. (Goal pill state is read
  // directly by GoalPillTray via its own useChatStore subscription — FE-1.)
  const loopStatus = useChatStore((s) => s.loopStatus ?? null)
  const setMessages = useChatStore((s) => s.setMessages)
  const attachedSessionType = useSessionStore((s) => s.attachedSessionType)
  const attachedTaskTitle = useSessionStore((s) => s.attachedTaskTitle)
  // For the ARIA live region: track the last assistant message id for screen reader announcements.
  // Select the pre-derived single id from the store (companion to `messagesById`)
  // rather than subscribing to the whole `messages` array + reversing/scanning it per
  // frame — the id only changes when the LAST assistant message itself changes (e.g. a
  // new turn starts), not on every WS frame of the turn. The full message object (for
  // the status read below) is then looked up via `messagesById[id]` — the same
  // O(1)-lookup idiom AssistantMessage/SubagentSpansRenderer use.
  const messages = useChatStore((s) => s.messages)
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

  // Load message history when session changes
  const isConnected = useConnectionStore((s) => s.isConnected)

  const {
    data: historyData,
    isError: historyError,
    refetch: refetchHistory,
  } = useQuery({
    queryKey: ['messages', activeSessionId],
    queryFn: () => fetchSessionMessages(activeSessionId!),
    // '__pending' is the optimistic-send sentinel (store/chat.ts) used before the
    // real session_id arrives over WS — it has no server-side history, so fetching
    // it 404s (spurious console errors + a historyError that tears down the composer).
    // Mirror the same guard used in attachment-adapter.ts.
    enabled: !!activeSessionId && activeSessionId !== '__pending',
    gcTime: 0,
    // Never re-fetch on window focus — the WebSocket delivers live updates
    refetchOnWindowFocus: false,
    // Skip re-fetch on mount when the WebSocket is already connected and delivering messages
    refetchOnMount: !isConnected,
  })

  // WS attach_session + streamReplay is the authoritative history loader.
  // Skip the REST-based setMessages overwrite when WS replay is active OR has already
  // populated the store — otherwise the filter below strips tool_call frames already
  // attached by the reducer and historical tool-call-badge elements disappear.
  const isReplaying = useChatStore((s) => s.isReplaying)
  const storeMessageCount = useChatStore((s) => s.messages.length)
  // replayCompletedForSession tracks whether WS replay finished for the active session.
  // When set, the REST fallback is skipped even if the store has 0 messages (empty session).
  const replayCompletedForSession = useChatStore((s) => s.replayCompletedForSession)
  useEffect(() => {
    if (!historyData) return
    // Don't overwrite during replay — WS frames are the source of truth.
    if (isReplaying) return
    // Don't overwrite if WS replay already completed for this session.
    // This gates the fallback more precisely than storeMessageCount > 0 alone —
    // an empty session would pass the count check but still had a successful replay.
    if (replayCompletedForSession === activeSessionId) return
    // Don't overwrite if the store already has messages for this session (replay done).
    if (storeMessageCount > 0) return
    // Fallback: REST fetched history before WS replay fired (e.g., WS unavailable).
    // Filter out tool_call entries that have no role — they crash AssistantUI's convertMessages.
    const validMessages = historyData.filter(
      (m: { role?: string }) => m.role === 'user' || m.role === 'assistant' || m.role === 'system',
    )
    setMessages(validMessages)
  }, [historyData, isReplaying, storeMessageCount, replayCompletedForSession, activeSessionId, setMessages])

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
    <div
      className="flex flex-col absolute inset-0 overflow-hidden"
      data-active-session-id={activeSessionId ?? ''}
    >
      {/* Agent-removed banner — shown when the session's agent has been deleted (#103) */}
      {agentRemoved && (
        <div
          data-testid="agent-removed-banner"
          className="px-4 py-2 bg-[var(--color-error)]/10 border-b border-[var(--color-error)]/20 flex items-center gap-2"
        >
          <span className="text-xs text-[var(--color-error)] flex-1">
            Agent removed — this session is read-only
          </span>
        </div>
      )}

      {/* Task session banner — shown when viewing a task execution transcript */}
      {attachedSessionType === 'task' && (
        <div className="px-4 py-2 bg-[var(--color-surface-2)] border-b border-[var(--color-border)] flex items-center gap-2">
          <ListChecks size={14} className="text-[var(--color-accent)] shrink-0" />
          <span className="text-xs text-[var(--color-secondary)] flex-1 truncate">
            Task: {attachedTaskTitle ?? 'Task Execution'}
          </span>
        </div>
      )}

      {/* History fetch error */}
      {historyError ? (
        <div className="flex flex-col items-center justify-center flex-1 gap-3 text-sm text-[var(--color-muted)]">
          <p>Could not load messages.</p>
          <Button variant="outline" size="sm" onClick={() => refetchHistory()}>
            <ArrowCounterClockwise size={14} /> Retry
          </Button>
        </div>
      ) : (
        <ThreadPrimitive.Root className="flex flex-col flex-1 min-h-0">
          {/* ARIA live region: announces new assistant messages to screen readers.
              Only fires when a genuinely new message ID arrives and is complete. */}
          <div aria-live="polite" aria-atomic="true" className="sr-only">
            {shouldAnnounce && lastAssistantMessage?.status === 'done' && (
              <span>New response from {activeAgentName}</span>
            )}
          </div>

          {/* Virtualized message list — only visible rows (+ 5-message overscan) are mounted as DOM nodes. */}
          {messages.length === 0 ? (
            <div className="flex-1 overflow-y-auto pt-4 pb-2">
              <WelcomeState hasAgent={!!activeAgentId} />
            </div>
          ) : (
            <VirtualizedMessageList messages={messages} liteMode={liteMode} />
          )}

          {/* FR-21: Interrupted-message status markers — rendered inside
              ThreadPrimitive.Root but OUTSIDE the scrollable Viewport. This
              position has guaranteed non-zero height because it's in the
              non-scrolling flex layout between the Viewport and the composer.
              Playwright locates these elements via text=(interrupted). */}
          <InterruptedMessageMarkers />

          {/* Rate-limit indicator — shown above composer. Tool-approval requests
              (including `bash`) are handled by the global ToolApprovalModal
              (ADR-036 §3.4 retired the dedicated exec-only approval flow). */}
          {rateLimitEvent && (
            <div className="px-4 space-y-2 pb-2">
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

          {/* Goal/loop indicator — ADR-049 D6/US-12/SD-C9: the goal pill was
              RELOCATED to the bottom-right GoalPillTray (FE-1, ADR-053 US-14);
              this above-composer slot now carries LOOP status only. Pass
              goalStatus=null so GoalIndicator's goal branch is dormant.
              GoalIndicator renders nothing when loopStatus is also absent. */}
          {loopStatus && (
            <div className="px-4 space-y-2 pb-2">
              <GoalIndicator goalStatus={null} loopStatus={loopStatus} />
            </div>
          )}

          {/* Goal echo / amendment cards — ADR-053 FE-8: the compiled goal is
              echoed IN CHAT (no form/modal) when a pill is in `queued` state
              (newly compiled, awaiting the user's chat confirmation). Renders
              nothing when no queued pills exist. */}
          <GoalThreadTailCards />

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
            {/* Gradient fade above composer */}
            <div className="absolute -top-8 left-0 right-0 h-8 bg-gradient-to-t from-[var(--color-primary)] to-transparent pointer-events-none" />
            <div className="w-full max-w-3xl mx-auto px-4 pt-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]">
              <OmnipusComposer agentRemoved={agentRemoved} />
            </div>
          </div>
        </ThreadPrimitive.Root>
      )}
    </div>
  )
}
