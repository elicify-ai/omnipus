// OmnipusComposer — the chat's message box (moved out of ChatScreen.tsx for
// #1449 FE-0; behaviour unchanged for a normal chat).
//
// #1449 ADR-20261028 §11 C-I4: the message box reads ONLY a
// ComposerConversation — enablement, the running state, the send/stop/command
// behaviour and the Escape scope — provided by the surface that renders it.
// The normal chat's conversation (useSessionComposerConversation, below)
// wraps exactly the reads the composer made before the move: replay state,
// the pending question lock, the workspace-entry gate, cancelStream and
// useCancelState (the ONLY place the document-level Escape listener is
// installed). A group chat builds its own conversation
// (src/components/rooms/useRoomComposerConversation.ts) from the room alone,
// so the group's box never reads, sends to, stops or is locked by any chat
// session. Which controls exist is the conversation's ComposerProfile.

import React, { useCallback, useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  ComposerPrimitive,
  AttachmentPrimitive,
  useAttachment,
  useComposerRuntime,
} from '@assistant-ui/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import { PaperPlaneRight, Stop, Plus, WarningCircle } from '@phosphor-icons/react'
import { UnavailableChatNotice } from '../UnavailableChatNotice'
import { ActivityBar } from '../ActivityBar'
import { ModelPicker } from './ModelPicker'
import { AutoApprovePicker } from './AutoApprovePicker'
import { TokenCounter } from './TokenCounter'
import { ComposerMenu } from './ComposerMenu'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { useChatStore } from '@/store/chat'
import { pendingRedirectSids } from '@/store/chat/runtime-state'
import type { RedirectFrame } from '@/lib/api/generated/asyncapi-types'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { workspaceEntryBlocksSend } from '@/lib/nav/workspaceEntry'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { isGoalRunning } from '@/lib/goalActivity'
import { fetchAgents } from '@/lib/api'
import { AttachmentCard, AttachmentRemoveX, useFilePreview } from '../AttachmentCard'
import { ComposerMediaLibraryButton, LibraryAttachmentChips } from '../ComposerMediaLibrary'
import { cn } from '@/lib/utils'
import { useSlashMenu, SECTION_CAP } from '@/hooks/useSlashMenu'
import type { UseSlashMenuResult } from '@/hooks/useSlashMenu'
import { recipientAddressBlocked } from '@/store/chat/recipientSend'
import { useFileUpload } from '@/hooks/useFileUpload'
import { useCancelState } from '@/hooks/useCancelState'
import type { UseCancelStateResult } from '@/hooks/useCancelState'

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
  // #1449 US-3 AS 3.3: an upload that failed is marked ON its chip (the room
  // adapter uploads on add and reports the failure as an incomplete status);
  // the message that would carry it is not sent until it is removed.
  const failed = attachment.status.type === 'incomplete'
  const failure = attachment.status.type === 'incomplete' ? attachment.status.message : undefined

  const card = (
    <AttachmentCard
      filename={attachment.name}
      contentType={contentType}
      imageUrl={imageUrl}
      className={cn('group', !!imageUrl && 'w-16 h-16')}
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
  if (!failed) return card
  return (
    <div className="flex flex-col" data-attachment-failed="true">
      {card}
      <p role="alert" className="flex max-w-[220px] items-center gap-[var(--space-1)] pt-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-error)]">
        <WarningCircle aria-hidden="true" size={12} weight="fill" />
        <span className="truncate">{failure ? `Upload failed: ${failure}` : `Upload failed: ${attachment.name}`}</span>
      </p>
    </div>
  )
}

/**
 * Which controls a message box has (ADR-20261028 A2.2). The normal chat has
 * all of them; a group chat has the card, attach, the workspace library and
 * send — no model picker, Auto-approve, token counter, skills, helper
 * activity pills or Stop (founder D4 as amended by D21).
 */
export interface ComposerProfile { // not-wire-format: React composition flags
  /** The context row above the card: model picker, Auto-approve, token counter. */
  contextRow: boolean
  /** The session's unavailable-chat notice above the box. */
  unavailableNotice: boolean
  /** The Stop button and the mid-response Send that sits beside it. */
  stop: boolean
  /** The helper/command activity pills below the card. */
  activityBar: boolean
  /** The workspace-library picker and its staged chips (normal chat only: its staging feeds the session send). */
  mediaLibrary: boolean
}

export const SESSION_COMPOSER_PROFILE: ComposerProfile = {
  contextRow: true,
  unavailableNotice: true,
  stop: true,
  activityBar: true,
  mediaLibrary: true,
}

/**
 * Everything the shared message box reads (ADR-20261028 §11 C-I4). Built by
 * the rendering surface; the box itself reads no store of its own except the
 * assistant-ui composer runtime it is mounted under.
 */
export interface ComposerConversation { // not-wire-format: React props for the shared message box
  profile: ComposerProfile
  /** A reply is streaming in THIS conversation (the group never streams). */
  isStreaming: boolean
  /** FR-I-014: the conversation is replaying its history (never in a group). */
  isReplaying: boolean
  isConnected: boolean
  reconnectPhase: ReturnType<typeof useConnectionStore.getState>['reconnectPhase']
  /** The textarea accepts input. */
  inputEnabled: boolean
  /**
   * The Send button may send. Defaults to `inputEnabled` (the normal chat
   * blocks typing and sending together). The group keeps the box typeable
   * while sending is blocked (FR-013): the text waits for the block to lift.
   */
  sendEnabled?: boolean
  /** Attach, drag-drop and the library picker are blocked. */
  attachDisabled: boolean
  /** The conversation is read-only because its agent was removed (normal chat only). */
  agentRemoved: boolean
  placeholder: string
  /** The textarea's accessible name (normal chat: "Message input"). */
  inputLabel?: string
  /** The attach button's accessible name and tooltip (normal chat: "Add files or context"). */
  attachLabel?: string
  /** The Stop machine; the group gets an inert one (D21), so Escape cancels nothing there. */
  cancelState: UseCancelStateResult
  /** G1: Stop stays reachable between goal turns (normal chat only). */
  goalRunning: boolean
  slashMenu: UseSlashMenuResult
  /** True when the current text must not be sent (the caller keeps the text). */
  sendBlocked: () => boolean
  /** Notices shown in the box's notice area, above the card (group chat). */
  notices?: React.ReactNode
}

/** The group chat has no Stop (D21): an inert stop machine that never sends a cancel. */
export const INERT_CANCEL_STATE: UseCancelStateResult = {
  stopLabel: 'stop',
  stopAllArmed: false,
  cancelIfStreaming: () => {},
  cancelUnconditional: () => {},
  cancelAllTreeScoped: () => {},
}

/**
 * The normal chat's conversation: exactly the reads OmnipusComposer made
 * before #1449 moved it out of ChatScreen.tsx (behaviour-neutral, FE-0 gate).
 */
export function useSessionComposerConversation(agentRemoved: boolean, composerRuntime: ComposerRuntime): ComposerConversation {
  const isStreaming = useChatStore((s) => s.isStreaming)
  // FR-I-014: disable send while replay frames are still arriving
  const isReplaying = useChatStore((s) => s.isReplaying)
  const isConnected = useConnectionStore((s) => s.isConnected)
  const reconnectPhase = useConnectionStore((s) => s.reconnectPhase)
  const cancelStream = useChatStore((s) => s.cancelStream)
  const appendMessage = useChatStore((s) => s.appendMessage)
  const activeAgentId = useSessionStore((s) => s.activeAgentId)

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentName = agents.find((a) => a.id === activeAgentId)?.name ?? 'Omnipus'

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
  // AskUserQuestion v3 US-1 S1: pending locks free-form answers to the card; its Cancel remains available, and answered/cancelled states release the lock.
  const askLocked = useChatStore((s) => s.pendingAsk?.status === 'pending')
  const entryWorkspaceId = useWorkspacesStore((state) => state.activeWorkspaceId)
  const chatUnavailable = useSessionStore((state) => workspaceEntryBlocksSend(state, entryWorkspaceId))

  const inputEnabled =
    !chatUnavailable &&
    !agentRemoved &&
    !isReplaying &&
    !askLocked &&
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
  const attachDisabled = !isConnected || isReplaying || reconnectPhase === 'gave_up' || agentRemoved || chatUnavailable

  const cancelState = useCancelState(isStreaming, cancelStream)
  // G1: Stop stays reachable in the idle gap between goal turns (see goalActivity.ts).
  const goalRunning = useChatStore((s) => isGoalRunning(s.goalStatus))
  // D9: the /stop-redirect transport — send the dedicated generated
  // RedirectFrame over the WS, with a VISIBLE error when it cannot be sent
  // (the same toast pattern cancelStream uses for a failed cancel send).
  // The frame never touches message intake, which lets this chat redirect
  // mid-stream; a swallowed failure would leave the user believing its
  // turn was stopped when it is still running.
  const sendRedirectFrame = useCallback((frame: RedirectFrame) => {
    const { connection } = useConnectionStore.getState()
    if (!connection) {
      useUiStore.getState().addToast({
        message: 'Could not send the redirect — the gateway connection is down. Reconnect and run /stop-redirect again.',
        variant: 'error',
      })
      return
    }
    const sent = connection.send(frame)
    if (!sent) {
      useUiStore.getState().addToast({
        message: 'Could not send the redirect — connection dropped. This chat\'s turn was NOT stopped; run /stop-redirect again.',
        variant: 'error',
      })
      return
    }
    // The server's turn_canceled answer carries no cause; remember this send
    // so the live view finalises the partial as a redirected turn (no
    // "(interrupted)"), see runtime-state.ts::pendingRedirectSids.
    // Only a chat with a streaming turn has anything for the redirect to stop.
    if (useChatStore.getState().sessionsById[frame.session_id]?.isStreaming) {
      pendingRedirectSids.add(frame.session_id)
    }
  }, [])
  const slashMenu = useSlashMenu({
    isStreaming,
    isReplaying,
    inputEnabled,
    composerRuntime,
    appendMessage,
    // /stop is one Stop-button activation; /cancel stays immediate tree.
    activateStop: cancelState.cancelUnconditional,
    cancelIfStreaming: cancelState.cancelAllTreeScoped,
    sendRedirectFrame,
  })

  const sendBlocked = useCallback(() => {
    // @-recipient no-session block (founder decision 2026-10-11 Q1 B): see
    // ComposerBody's submit paths. Read the live composer text, falling back
    // to the slash-menu mirror.
    const text = composerRuntime.getState().text ?? slashMenu.inputValue
    return recipientAddressBlocked(text) !== null
  }, [composerRuntime, slashMenu.inputValue])

  return {
    profile: SESSION_COMPOSER_PROFILE,
    isStreaming,
    isReplaying,
    isConnected,
    reconnectPhase,
    inputEnabled,
    attachDisabled,
    agentRemoved,
    placeholder: agentRemoved ? 'Agent has been removed — this session is read-only' : composerPlaceholder(isConnected || reconnectPhase === 'reconnecting' || reconnectPhase === 'slow', isStreaming, isReplaying, activeAgentName, reconnectPhase === 'gave_up'),
    cancelState,
    goalRunning,
    slashMenu,
    sendBlocked,
  }
}

// Exported for unit testing (TDD row 22). The normal chat's message box.
export function OmnipusComposer({ agentRemoved = false }: { agentRemoved?: boolean }) {
  const composerRuntime = useComposerRuntime()
  const conversation = useSessionComposerConversation(agentRemoved, composerRuntime)
  return <ComposerBody conversation={conversation} composerRuntime={composerRuntime} />
}

/**
 * The shared message box (#1449 D4: one composer for the normal and the group
 * chat). Everything it needs comes from `conversation`; the controls it shows
 * come from `conversation.profile`.
 */
export function ComposerBody({ conversation, composerRuntime }: { conversation: ComposerConversation; composerRuntime: ComposerRuntime }) {
  const {
    profile, isStreaming, isReplaying, reconnectPhase, inputEnabled, attachDisabled, agentRemoved,
    cancelState, goalRunning, slashMenu,
  } = conversation
  const sendEnabled = conversation.sendEnabled ?? inputEnabled

  // Tracks whether we already warned for the current large-input threshold crossing,
  // so we only fire one toast per paste/input event that exceeds 1MB.
  const hasWarnedLargeInput = useRef(false)

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
  // D8 / T24 B26: a reconnect replay (or another temporary lock) disables the
  // composer, through our inputEnabled prop OR assistant-ui's own thread
  // isDisabled. A real browser drops focus to <body> the moment a focused
  // control becomes disabled and never returns it. Remember a blur caused by
  // that lock and, when the textarea's own disabled attribute clears, give
  // focus back only if nothing else took it meanwhile; preventScroll keeps the
  // reader in place. Watching the element covers every lock source.
  const restoreFocusAfterLockRef = useRef(false)
  const releaseLockWatchRef = useRef<(() => void) | null>(null)
  const setChatInput = useCallback((node: HTMLTextAreaElement | null) => {
    chatInputRef.current = node
    releaseLockWatchRef.current?.()
    releaseLockWatchRef.current = null
    if (!node || typeof MutationObserver === 'undefined') return
    // Native listener: the browser's blur on disable is what drops focus.
    const blur = () => { restoreFocusAfterLockRef.current = node.disabled }
    node.addEventListener('blur', blur)
    const observer = new MutationObserver(() => {
      if (node.disabled || !restoreFocusAfterLockRef.current) return
      restoreFocusAfterLockRef.current = false
      const active = document.activeElement
      if (!active || active === document.body) node.focus({ preventScroll: true })
    })
    observer.observe(node, { attributes: true, attributeFilter: ['disabled'] })
    releaseLockWatchRef.current = () => { observer.disconnect(); node.removeEventListener('blur', blur) }
  }, [])


  const fileUpload = useFileUpload(composerRuntime)

  // Fix E (bugfixes3 sign-off): `shouldShowSlash` alone is not "the menu
  // has content to render" — the LOW S8 commandsError carve-out
  // (useSlashMenu.ts) deliberately keeps it true even when `slashItems` is
  // empty, so the container below could mount with NEITHER a real item NOR
  // the error row inside it (e.g. "/skills" + commandsError + zero skills:
  // the error row hides itself via `!isSkillsFilter` failing, and there are
  // no skill items either) — an empty bordered box with nothing in it.
  // Gate on whatever will actually render: at least one item, OR the exact
  // condition the error row's own JSX uses below (kept identical on
  // purpose so the two can't drift apart). The error row is also hidden in
  // "@" mention mode (restored — founder decision 2026-10-11 Q1 B): a
  // commands-fetch error has nothing to do with the agent list.
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
  // different things. Only skills and agents are capped (commands has no
  // cap), and the "/" and "@" triggers are mutually exclusive by construction
  // (see useSlashMenu.ts's file header), so `isMentionMode` alone picks the
  // right count.
  const activeSectionHiddenCount = slashMenu.isMentionMode
    ? slashMenu.agentsHiddenCount
    : slashMenu.skillsHiddenCount
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
  //
  // @-recipient no-session block (founder decision 2026-10-11 Q1 B): a
  // leading "@<Agent name>" address is only sendable FROM an existing chat —
  // the server refuses a recipient that arrives without session_id
  // (pkg/gateway/ws_recipient.go::refuseRecipient), and sending the message
  // unaddressed would silently drop the addressing. recipientAddressBlocked
  // resolves the address from the live composer text and, when this chat has
  // no session yet ('__pending' included), shows the composer's standard
  // blocked-send warning and returns non-null. The caller preventDefaults /
  // early-returns WITHOUT clearing anything: the runtime only empties the
  // composer on a send that actually proceeds (verified —
  // BaseComposerRuntimeCore.send calls _emptyTextAndAttachments before
  // handleSend, so a pre-submit refusal here is the only way to keep the
  // text), which is exactly "never silently drop the message or the @".
  function recipientAddressBlockedNow(): boolean {
    // The conversation decides what must not be sent: the normal chat's
    // @-recipient no-session block, or a group message whose attachment
    // upload failed or is still running. Either way the text is kept.
    return conversation.sendBlocked()
  }
  function submitMidStreamMessage() {
    // Mirrors ComposerPrimitive.Root's onSubmit below: a typed client
    // command (e.g. "/cancel") still intercepts locally instead of steering
    // into the running turn as chat text.
    if (slashMenu.interceptClientCommand()) return
    // A steer mid-FIRST-turn ('__pending') has no session for an @-recipient
    // to ride on — block visibly and keep the text (the ordinary first-send
    // block below covers the idle case).
    if (recipientAddressBlockedNow()) return
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
    // ADR-20260928 D9: an armed Stop-all window is itself a live
    // confirmation surface — the second Esc confirms (cancelIfStreaming
    // routes the armed case to the tree-scoped cancel) even though the
    // first activation already ended the turn locally (isStreaming false,
    // label reset). Higher-priority surfaces keep precedence: the
    // menu-close branch above runs first and stops propagation.
    if (e.key === 'Escape' && (isStreaming || goalRunning || cancelState.stopLabel === 'stopping' || cancelState.stopAllArmed)) {
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
      {profile.unavailableNotice && <UnavailableChatNotice />}
      {conversation.notices}
      {/* a11y HIGH (Fix 2, restored with the menu — founder decision
          2026-10-11 Q1 B): selecting an agent via "@" changes ONLY the
          composer text (the "@<Name> " address prefix — the recipient is
          derived from it at send time), which a screen reader does not
          announce on change. Mirrors the sr-only aria-live pattern of the
          message list so a mention selection is announced like any other
          chat event. Re-selecting the SAME agent leaves the string
          unchanged (no re-announce) — nothing actually changed. */}
      <div aria-live="polite" aria-atomic="true" className="sr-only" data-testid="agent-mention-announcement">
        {slashMenu.mentionAnnouncement && <span>Message addressed to {slashMenu.mentionAnnouncement}</span>}
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


      {/* Slash command + skills + agent-mention partitioned dropdown
          (FR-005; the "@" trigger restored by founder decision 2026-10-11
          Q1 B). Rendered by ComposerMenu — moved verbatim out of this file
          (2026-10-11), which sits over the size budget and may only shrink;
          the ARIA listbox row markup moved with it, including the reviewed
          raw-<button> retention now registered for ComposerMenu.tsx in the
          design-system ledger. */}
      <ComposerMenu
        slashMenu={slashMenu}
        menuIsRendered={menuIsRendered}
        clampedSlashHighlight={clampedSlashHighlight}
        footerCopy={footerCopy}
        focusChatInput={focusChatInput}
      />

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
      {profile.contextRow && <div
        className="flex items-center gap-[var(--space-1)] min-w-0 overflow-x-auto px-[var(--space-1)] py-[var(--space-1)]"
        style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as React.CSSProperties}
      >
        {/* Model. The agent picker is gone (FR-007). Attach lives INSIDE the
            input card (leading the textarea). */}
        <ModelPicker disabled={agentRemoved} tabIndex={4} />
        {/* ADR-092: per-chat Auto-approve quick switch. Deliberately NO
            explicit tabIndex — the closed 1-8 composer ring documented
            above (see ChatControls.tsx) stays exactly as numbered; this
            control falls into natural DOM tab order after it, same as the
            header tab menu. */}
        <AutoApprovePicker disabled={agentRemoved} />
        <span className="flex-1" />
        {/* Token counter — status; hidden below @2xl of the composer root's
            @container (~42rem). */}
        <TokenCounter className="hidden @2xl:flex" />
      </div>}

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
          className="flex items-end gap-[var(--space-2)] p-[var(--space-2)]"
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
            // local slash command (for example "/help", "/model", or
            // "/cancel"). /new and /clear are sent to the server.
            // locally and prevent it from reaching the backend. This converges the
            // typed+Enter path with the palette selection path.
            if (slashMenu.interceptClientCommand()) {
              e.preventDefault()
              return
            }
            // @-recipient no-session block (see recipientAddressBlockedNow's
            // comment above): refused visibly, text kept — never submitted.
            if (recipientAddressBlockedNow()) {
              e.preventDefault()
              return
            }
            // Fix 4: resync the text mirror after a real send. assistant-ui's
            // composerRuntime clears its own text on a successful submit
            // WITHOUT firing the textarea's onChange (no synthetic change
            // event is dispatched for the runtime-driven clear), so
            // `slashMenu.inputValue` — which only updates via onInputChange —
            // kept the just-sent text even though the visible textarea was
            // now empty. A subsequent ArrowDown then read that stale mirror
            // and reopened the slash menu (the "@" agent-mention menu —
            // restored 2026-10-11, founder decision Q1 B — included). Only
            // reached here when the send actually proceeded (neither the
            // isStreaming guard, interceptClientCommand(), nor the
            // @-recipient block intercepted it above), so this can't clear
            // the mirror out from under a blocked/intercepted send.
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
            className="shrink-0 h-7 w-7 mb-[var(--space-1)] rounded-full flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-3)] transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
            aria-label={conversation.attachLabel ?? 'Add files or context'}
            title={conversation.attachLabel ?? 'Add files or context'}
          >
            <Plus size={16} />
          </ComposerPrimitive.AddAttachment>

          {/* ADR-051 Rev 4 (Slice H) — attach an existing workspace-library
              entry without re-uploading (FR-022). Disabled alongside the file
              attach button in read-only / disconnected sessions. */}
          {profile.mediaLibrary && <ComposerMediaLibraryButton disabled={attachDisabled} tabIndex={5} />}

          {/* Ghost text wrapper — positioned overlay approach */}
          <div className="relative flex-1 min-w-0">
            <ComposerPrimitive.Input
              ref={setChatInput}
              data-testid="chat-input"
              data-no-focus-ring
              // Composer tab ring — see the full map in ChatControls.tsx.
              tabIndex={2}
              placeholder={conversation.placeholder}
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
                'block w-full resize-none bg-transparent px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] outline-none',
                'placeholder:text-[var(--color-muted)] min-h-[24px] max-h-[200px] leading-6 overflow-hidden',
                'focus:outline-none focus:ring-0',
                (!inputEnabled || isStreaming) ? 'opacity-60 cursor-not-allowed' : undefined,
              )}
              aria-label={conversation.inputLabel ?? 'Message input'}
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
                className="pointer-events-none absolute inset-0 px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-body-compact-size)] leading-6 flex items-start"
                data-testid="ghost-text"
              >
                <span className="invisible whitespace-pre">{slashMenu.inputValue}</span>
                <span className="text-[var(--color-muted)] opacity-60">
                  {slashMenu.ghostText}
                </span>
              </div>
            )}
          </div>

          {/* The same Stop button confirms a second activation for all 3 s,
              including after streaming and the 1 s stopping label reset.
              No separate Stop-all control (founder 2026-10-06). */}
          {profile.stop && (isStreaming || goalRunning || cancelState.stopLabel === 'stopping' || cancelState.stopAllArmed) ? (
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
              <Button
                tabIndex={6}
                variant="ghost"
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
                  'h-auto shrink-0 rounded-lg mb-[var(--space-0-5)]',
                  cancelState.stopLabel === 'stopping'
                    ? 'px-[var(--space-2-5)] h-9 gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] font-medium bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30 hover:text-[var(--color-error)]'
                    : 'w-9 h-9 p-0',
                  isStreaming || cancelState.stopAllArmed
                    ? 'bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30 hover:text-[var(--color-error)]'
                    : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-muted)] cursor-wait',
                )}
                aria-label={cancelState.stopLabel === 'stopping' ? 'Stopping...' : 'Stop generation'}
                title={cancelState.stopAllArmed
                  ? 'Press Stop or Escape again within 3 seconds to stop this chat and all its helpers'
                  : 'Stop (Escape)'}
              >
                <Stop size={15} weight="fill" />
                {cancelState.stopLabel === 'stopping' && <span>Stopping...</span>}
              </Button>
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
                <IconButton
                  tabIndex={6}
                  type="button"
                  data-testid="chat-send-mid-stream"
                  disabled={!inputEnabled}
                  onClick={submitMidStreamMessage}
                  className={cn(
                    'shrink-0 w-9 h-9 mb-[var(--space-0-5)] rounded-lg',
                    inputEnabled
                      ? 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent-hover)] hover:text-[var(--color-primary)]'
                      : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-muted)] cursor-not-allowed',
                  )}
                  aria-label="Send into the running response"
                  title="Send into the running response"
                  aria-describedby="mid-stream-send-hint"
                >
                  <PaperPlaneRight size={15} weight="bold" />
                </IconButton>
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
              disabled={!sendEnabled || isReplaying}
              data-testid="chat-send"
              tabIndex={6}
              onClick={(e) => {
                if (slashMenu.interceptClientCommand()) {
                  e.preventDefault()
                  return
                }
                // @-recipient no-session block (same contract as onSubmit's):
                // preventDefault stops the primitive's own composer.send()
                // (composeEventHandlers honours it — see the J correction
                // note above), so the composer text is kept.
                if (recipientAddressBlockedNow()) {
                  e.preventDefault()
                }
              }}
              className={cn(
                'shrink-0 w-9 h-9 mb-[var(--space-0-5)] rounded-lg flex items-center justify-center transition-colors',
                sendEnabled && !isReplaying
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
            className="px-[var(--space-2-5)] pb-[var(--space-1)] -mt-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
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
        <div className="flex flex-wrap gap-[var(--space-2)] px-[var(--space-2)] empty:hidden [&:has(*)]:pb-[var(--space-2)]">
          <ComposerPrimitive.Attachments components={{ Attachment: ComposerAttachmentChip }} />
          {profile.mediaLibrary && <LibraryAttachmentChips />}
        </div>

      </div>

      {/* Live background activity (delegate spans + background bash runs) —
          pills render BELOW the card, bare on the shell background (operator
          direction), Claude-Code style. Opens the ActivityPanel slide-out on
          click. ActivityBar renders null when idle, so `empty:hidden`
          collapses this wrapper — no stray padding when nothing runs. */}
      {profile.activityBar && <div className="px-[var(--space-1)] pt-[var(--space-1)] empty:hidden">
        <ActivityBar />
      </div>}

      {/* Harmful-file upload double-confirm — replaces the native window.confirm pair.
          Stage 1 warns and lists the flagged files; stage 2 is the second
          confirmation. Files are only attached after the user confirms stage 2.
          Two catalogued ConfirmDialogs, mutually exclusive on `harmfulStage`
          (only one is ever `open` at a time), rather than one dialog switching
          its content by stage — ConfirmDialog's closed API takes static
          title/description/confirmLabel per instance, not a stage switch. */}
      <ConfirmDialog
        open={fileUpload.harmfulConfirm !== null && fileUpload.harmfulStage === 1}
        onOpenChange={(open) => {
          if (!open) fileUpload.dismissHarmfulConfirm()
        }}
        title="Potentially harmful file(s)"
        description={
          fileUpload.harmfulConfirm
            ? `${fileUpload.harmfulConfirm.harmfulNames.join(', ')} may be potentially harmful file(s).\n\nAre you sure you want to upload?`
            : ''
        }
        confirmLabel="Continue"
        destructive
        onConfirm={fileUpload.advanceHarmfulStage}
      />
      <ConfirmDialog
        open={fileUpload.harmfulConfirm !== null && fileUpload.harmfulStage === 2}
        onOpenChange={(open) => {
          if (!open) fileUpload.dismissHarmfulConfirm()
        }}
        title="Confirm upload"
        description={
          fileUpload.harmfulConfirm
            ? `Please confirm again: Upload ${fileUpload.harmfulConfirm.harmfulNames.length} potentially harmful file(s)?`
            : ''
        }
        confirmLabel="Upload"
        destructive
        onConfirm={fileUpload.confirmHarmfulUpload}
      />
    </div>
  )
}
