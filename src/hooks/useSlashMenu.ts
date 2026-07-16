// useSlashMenu — partitioned slash-command + skill palette for the chat
// composer (FR-005/FR-006/FR-009/FR-014/D9/R3), plus the ghost-text hint
// shown after selecting a skill. Extracted out of OmnipusComposer's
// ~862-line body (Wave 3 structural refactor) — behavior is unchanged, only
// ownership moved.
//
// Also owns the `@` agent-mention menu — a second leading-character trigger
// that reuses this hook's plumbing rather than living in a parallel hook,
// because this hook is already the single owner of the composer's text
// mirror AND the menu's keyboard nav; a second hook would need to duplicate
// both (and would race this one for the mirror) to add its own trigger. The
// two triggers are mutually exclusive by construction — `inputValue` cannot
// simultaneously start with "/" and "@" — so `slashItems` simply swaps its
// source list depending on which (if either) fired.
//
// Owns the composer's text mirror (`inputValue`) — AssistantUI's
// `composerRuntime` is the actual source of truth for what gets sent, but
// this hook needs a React-reactive copy of the current text to: gate
// whether the menu should be showing (`inputValue.startsWith('/')` or
// `startsWith('@')`), decide whether the ghost-text overlay should render,
// and filter the palette. Every place that needs to change the composer's
// text (selecting a command/skill/agent, running a client command, the
// plain onChange handler) goes through this hook's actions so
// `composerRuntime.setText(...)` and the local mirror never drift apart —
// see each action below.
//
// The `/cancel` client command needs to drive the Stop button's visual
// state, which is owned by the sibling `useCancelState` hook — the caller
// wires that hook's `cancelIfStreaming` in as a parameter rather than this
// hook reaching across to import it, keeping the two hooks independently
// testable.

import { useEffect, useRef, useState } from 'react'
import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { ComposerRuntime } from '@assistant-ui/react'
import { generateId } from '@/lib/constants'
import { fetchCommands, fetchSkills } from '@/lib/api'
import type { SlashCommand, Skill, Agent } from '@/lib/api'
import type { ChatMessage } from '@/store/chat'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useChatAgents } from '@/hooks/useChatAgents'

// ── Slash/skill/agent palette item shape ─────────────────────────────────────

export interface SlashItem {
  key: string
  label: string
  description: string
  section: 'commands' | 'skills' | 'agents'
  argumentHint?: string
  /** Agent-row-only (section === 'agents') — the avatar dot's background color. */
  agentColor?: string
  /** Agent-row-only — Phosphor icon name for the avatar; falls back to the agent's initial when unset. */
  agentIcon?: string
  /**
   * Agent-row-only — the agent's display name, for the render layer's
   * avatar-initial computation (Fix 9). Carrying the name explicitly (not
   * deriving the initial from `label.charAt(1)`, which assumes `label` is
   * always exactly "@" + one BMP character) keeps the initial correct for
   * astral-plane first characters and survives any future label formatting
   * change without a silent initial regression.
   */
  agentName?: string
  /** Agent-row-only — true when this row is the currently active chat agent (renders the "active" marker). */
  isActiveAgent?: boolean
  onSelect: () => void
}

export interface UseSlashMenuParams {
  isStreaming: boolean
  isReplaying: boolean
  /** Agent removed / gave-up-reconnect / replaying / disconnected — see OmnipusComposer's `inputEnabled`. */
  inputEnabled: boolean
  composerRuntime: ComposerRuntime
  appendMessage: (message: ChatMessage) => void
  startNewSession: () => void
  /** `/cancel` delegates here — see useCancelState.cancelIfStreaming's doc comment for why this variant (not the unconditional one) is correct for a client command. */
  cancelIfStreaming: () => void
}

export interface UseSlashMenuResult {
  /**
   * Mirrors the composer's current text. Exposed read-only — needed by the
   * ghost-text render check and as `interceptClientCommand`'s fallback
   * source of truth when `composerRuntime.getState().text` is nullish.
   */
  inputValue: string
  slashOpen: boolean
  slashHighlight: number
  shouldShowSlash: boolean
  slashItems: SlashItem[]
  /**
   * True when `fetchCommands('web')` errored. The palette still renders
   * (the synthetic client-only `/resume` entry and any skills survive), but
   * every backend-served command is silently missing — the caller should
   * surface a "Commands unavailable" row rather than letting the gap pass
   * unnoticed. `shouldShowSlash` accounts for this: it stays true even when
   * `slashItems` is otherwise empty, so the error row has somewhere to
   * render.
   */
  commandsError: boolean
  /** D9: true when the input is exactly "/skills" — Commands section (and its error row) are hidden while this filter is active. */
  isSkillsFilter: boolean
  /** True when the input starts with "@" (leading position, same gate as the slash trigger) — `slashItems` is agent rows only while this is true. */
  isMentionMode: boolean
  /**
   * Fix 2 (a11y HIGH): the just-selected agent's display name, set the
   * instant `selectMentionAgent` fires — null before any selection has
   * happened this session, AND reset to null whenever `activeAgentId`
   * changes through a path OTHER than a mention selection (e.g. the
   * AgentPicker dropdown) — see the Fix B reconciliation effect declared
   * right after `activeAgentId` below. The composer silently empties and
   * routing silently changes on a mention selection, which was zero
   * non-visual feedback for a screen-reader user; the caller renders this
   * in an sr-only `aria-live="polite"` element so the switch is announced.
   * Re-selecting the SAME agent twice in a row with no external switch in
   * between leaves this string unchanged (no state transition), so the
   * live region does not re-announce — nothing actually changed. But once
   * an external switch has cleared it back to null, re-selecting that SAME
   * agent via "@" IS a real `null -> name` transition and DOES announce —
   * the reconciliation effect exists specifically so that case isn't
   * silently swallowed, and so a stale "Now chatting with X" string can't
   * linger in the a11y tree describing a switch that already moved on.
   */
  mentionAnnouncement: string | null
  /** True when the ghost-text overlay should render (value is exactly `/<skillId> ` right after that skill was selected from the menu). */
  showGhostText: boolean
  /** The ghost text to display — the skill's argument_hint if it declared one, else the generic placeholder. */
  ghostText: string
  /** Composer textarea onChange — updates the mirror, clears a stale ghost, and opens/closes the menu on the leading "/" or "@". */
  onInputChange: (val: string) => void
  /** Composer textarea onBlur — clears ghost text and closes the menu (delayed so a mouseDown on a menu item fires first). */
  onInputBlur: () => void
  /** Menu item onMouseEnter — moves the keyboard highlight to the hovered item. */
  onHoverItem: (index: number) => void
  /**
   * ArrowUp/ArrowDown/Enter-select/Escape-close — no-ops when the menu
   * isn't showing. Enter-blocked-while-streaming is handled by the caller
   * BEFORE this (ChatScreen.handleKeyDown's `isStreaming` guard). Escape is
   * different (J.1 correction, bugfixes3 sign-off): when the menu is open,
   * ChatScreen.handleKeyDown now routes Escape here FIRST — menu-close wins
   * over stream-cancel — and only falls through to its own cancel-Escape
   * branch on a SECOND Escape, once this hook's own Escape branch (below)
   * has already closed the menu. See ChatScreen.handleKeyDown's Fix 3 doc
   * comment for the full precedence rationale.
   */
  handleKeyDown: (e: ReactKeyboardEvent) => void
  /**
   * Send-path interception: if the composer's current text is exactly a
   * client-delivery slash command (e.g. "/new", or its legacy alias
   * "/clear"), runs it locally and returns true — caller must
   * preventDefault() so the message never reaches the backend. Makes typing
   * "/new"+Enter (or "/clear"+Enter) behave identically to selecting it
   * from the palette.
   */
  interceptClientCommand: () => boolean
}

const GHOST_TEXT_PLACEHOLDER = '<message>'

export function useSlashMenu(params: UseSlashMenuParams): UseSlashMenuResult {
  const { isStreaming, isReplaying, inputEnabled, composerRuntime, appendMessage, startNewSession, cancelIfStreaming } = params

  const [inputValue, setInputValue] = useState('')
  const [slashOpen, setSlashOpen] = useState(false)
  const [slashHighlight, setSlashHighlight] = useState(0)
  // Ghost text after skill selection — shown when value is exactly
  // `/<skill-id> `. F3-frontend: also store the skill's argument_hint so
  // the ghost can show it instead of the generic `<message>` when the
  // skill declares one.
  const [ghostSkillId, setGhostSkillId] = useState<string | null>(null)
  const [ghostArgumentHint, setGhostArgumentHint] = useState<string | null>(null)
  // Fix A (bugfixes3 sign-off) — ref mirror of `ghostSkillId`, kept current
  // every render (not inside an effect). The composerRuntime subscription
  // effect below is registered once per `composerRuntime` identity, not
  // once per render, so its callback can't close over a same-render
  // `ghostSkillId` the way `onInputChange` (called directly from the
  // textarea's onChange) safely can — it would go stale the moment
  // `ghostSkillId` changed without the composer identity also changing.
  // Reading through a ref sidesteps that without forcing a
  // resubscribe/unsubscribe pair on every ghost-text change.
  const ghostSkillIdRef = useRef(ghostSkillId)
  ghostSkillIdRef.current = ghostSkillId
  // Fix 2: last-announced mention selection — see mentionAnnouncement's doc
  // comment on UseSlashMenuResult.
  const [mentionAnnouncement, setMentionAnnouncement] = useState<string | null>(null)
  // Fix B (bugfixes3 sign-off) — the id of the agent THIS hook last
  // announced via selectMentionAgent (set there, synchronously, alongside
  // `setMentionAnnouncement`). The reconciliation effect below (watching
  // `activeAgentId`) compares against this to tell "activeAgentId changed
  // because of a mention selection" (ref already matches — leave the
  // announcement alone) apart from "activeAgentId changed via some OTHER
  // surface, e.g. AgentPicker" (ref is stale — clear the announcement).
  const lastAnnouncedAgentIdRef = useRef<string | null>(null)

  // US-4 / FR-008: fetch the web-surface command list from the single
  // source of truth. On error the palette shows nothing crash-wise (per
  // integration boundary spec) — `commandsError` lets the caller render a
  // visible "Commands unavailable" row instead of a silent empty gap
  // (LOW S8).
  const { data: commands = [], isError: commandsError } = useQuery<SlashCommand[]>({
    queryKey: ['commands', 'web'],
    queryFn: () => fetchCommands('web'),
    staleTime: 60_000,
    enabled: inputEnabled,
  })

  // Merge frontend-only client commands with the backend-served list so the
  // synthetic entries participate in palette filtering, /help, and the
  // send-path interception identically to a real backend command.
  // /resume is web-client-only: opens the cross-workspace session search
  // modal (the same one the sidebar search icon opens) to resume a session.
  const allCommands: SlashCommand[] = [
    {
      name: 'resume',
      label: '/resume',
      description: 'Resume a session — search across all workspaces',
      delivery: 'client',
      available_while_streaming: true,
    },
    ...commands,
  ]

  // Skills query: always enabled when input is enabled (not gated on
  // skill-arg mode). staleTime of 60s matches the commands query — skills
  // change rarely.
  const { data: skills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    queryFn: () => fetchSkills(),
    staleTime: 60_000,
    enabled: inputEnabled,
  })

  // "@" agent-mention trigger — shares this hook's text mirror + keyboard
  // nav (see file header). `chatAgents` reuses AgentPicker's exact
  // status/worker/core_team scoping via useChatAgents so the mention menu
  // never offers an agent the picker itself wouldn't.
  const { chatAgents } = useChatAgents()
  // Fix 5: reactive slice, not a whole-store subscription. `useSessionStore()`
  // (no selector) re-rendered this hook — and therefore the whole composer —
  // on ANY write to the session store, including ones this hook doesn't
  // care about (e.g. attachedTaskTitle churn from an unrelated deep-link).
  // Only `activeAgentId` needs to be reactive here (it drives the
  // `isActiveAgent` marker recomputed on every render); `activeSessionId`
  // and `setActiveSession` are read fresh from `.getState()` at write time
  // inside `selectMentionAgent` below — the same "fresh-state-in-write-path"
  // pattern AgentPicker's own auto-select effect documents (composer/
  // AgentPicker.tsx) — which also closes the stale-closure window where a
  // captured `activeSessionId` could be stale by the time the user actually
  // selects an agent.
  const activeAgentId = useSessionStore((s) => s.activeAgentId)

  // Fix A (bugfixes3 sign-off — resolves a reviewer split on whether the
  // Send BUTTON's click path goes through ComposerPrimitive.Root's
  // onSubmit): verified against node_modules/@assistant-ui/react/dist/
  // utils/createActionButton.js + primitives/composer/ComposerSend.js —
  // ComposerPrimitive.Send renders a plain `type="button"` whose onClick
  // calls `composer.send()` DIRECTLY (via useComposerSend's callback), NOT
  // `form.requestSubmit()`. A mouse click on Send therefore NEVER fires
  // ComposerPrimitive.Root's onSubmit (ChatScreen.tsx) — the isStreaming
  // guard, `interceptClientCommand()`, and the Fix-4 mirror resync that
  // live there only ever ran on a keyboard Enter submit. AssistantUI's
  // composerRuntime clears its own text on a successful send either way,
  // WITHOUT dispatching a synthetic onChange for the textarea, so a
  // click-Send left `inputValue` above holding the just-sent text with
  // nothing downstream to correct it (a subsequent ArrowDown then read the
  // stale mirror and reopened the full "@" menu over a visually-empty
  // input).
  //
  // Rather than patch the click-Send path specifically, subscribe directly
  // to the runtime so the mirror is self-healing against ANY out-of-band
  // clear — not just this one — the instant the runtime's own text
  // diverges from what this hook believes it is. Registered once per
  // `composerRuntime` identity (assistant-ui hands back a stable runtime
  // object for the lifetime of a composer), so the callback below cannot
  // rely on this render's closures for anything that changes between
  // renders: `ghostSkillIdRef` (kept fresh every render, above) stands in
  // for `ghostSkillId`; everything else it touches is a useState setter,
  // which React guarantees is referentially stable.
  useEffect(() => {
    return composerRuntime.subscribe(() => {
      const runtimeText = composerRuntime.getState().text ?? ''
      setInputValue((mirrored) => (mirrored === runtimeText ? mirrored : runtimeText))
      const currentGhostSkillId = ghostSkillIdRef.current
      if (currentGhostSkillId && runtimeText !== `/${currentGhostSkillId} `) {
        setGhostSkillId(null)
        setGhostArgumentHint(null)
      }
      if (runtimeText.startsWith('/') || runtimeText.startsWith('@')) {
        setSlashOpen(true)
      } else {
        closeSlash()
      }
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [composerRuntime])

  // Fix B (bugfixes3 sign-off) — reconciliation for `mentionAnnouncement`.
  // It was never cleared, so an agent switch made through a DIFFERENT
  // surface (e.g. AgentPicker) left a STALE "Now chatting with X" string
  // sitting in the a11y live region describing a switch that didn't happen
  // through "@" — false state, not just a visual no-op. It also meant
  // re-selecting the SAME agent via "@" a second time, after switching away
  // and back through that other surface, silently no-op'd: mentionAnnouncement
  // still held that agent's name from the FIRST selection, so the second
  // `selectMentionAgent` call set the identical string again and React's
  // setState bailed (no transition, no re-announce) even though the user
  // just made a fresh selection worth announcing. Clearing on any
  // externally-driven `activeAgentId` change fixes both: the stale text is
  // removed, and the NEXT mention selection of that agent becomes a real
  // `null -> name` transition that DOES announce.
  useEffect(() => {
    if (activeAgentId !== lastAnnouncedAgentIdRef.current) {
      setMentionAnnouncement(null)
    }
  }, [activeAgentId])

  // FR-005: partitioned slash menu — Commands + Skills sections. Triggered
  // when input starts with "/" (no old skill-arg gate).
  const menuFilter = (() => {
    if (!inputValue.startsWith('/') || isReplaying || !inputEnabled) return null
    return inputValue.slice(1).toLowerCase() // the text after "/"
  })()

  // Mirrors menuFilter for the "@" trigger — leading position only, same
  // isReplaying/inputEnabled gate. "hello @x" must NOT trigger: only a
  // leading "@" (index 0 of the raw input) opens the mention menu.
  const mentionFilter = (() => {
    if (!inputValue.startsWith('@') || isReplaying || !inputEnabled) return null
    return inputValue.slice(1).toLowerCase() // the text after "@"
  })()

  const isSkillsFilter = menuFilter === 'skills'
  const isMentionMode = mentionFilter !== null

  // Commands section — hidden when typing "/skills" (D9)
  const visibleCommandItems: SlashItem[] = (() => {
    if (menuFilter === null || isSkillsFilter) return []
    const all = allCommands.filter((cmd) => {
      const cmdName = cmd.label.slice(1).toLowerCase() // strip leading /
      return menuFilter === '' || cmdName.startsWith(menuFilter)
    })
    const filtered = isStreaming ? all.filter((cmd) => cmd.available_while_streaming === true) : all
    return filtered.map((cmd) => ({
      key: cmd.label,
      label: cmd.label,
      description: cmd.description,
      section: 'commands' as const,
      onSelect: () => executeSlashCommand(cmd.label),
    }))
  })()

  // Skills section — always shown when "/" typed, unless empty
  const visibleSkillMenuItems: SlashItem[] = (() => {
    if (menuFilter === null) return []
    const lower = isSkillsFilter ? '' : menuFilter
    const filtered = skills.filter((s) =>
      lower === '' ||
      s.id.toLowerCase().startsWith(lower) ||
      s.name.toLowerCase().startsWith(lower),
    )
    return filtered.slice(0, 8).map((s) => {
      // F3-frontend/FR-014/R3: the skill's argument_hint drives the menu
      // help text and the inline ghost (Skill.argument_hint is on the
      // generated wire type).
      const argHint: string | undefined = s.argument_hint
      return {
        key: s.id,
        label: `/${s.id}`,
        description: s.name + (s.description ? ` — ${s.description}` : ''),
        section: 'skills' as const,
        argumentHint: argHint,
        onSelect: () => completeSkillName(s.id, argHint),
      }
    })
  })()

  // Agent section — the "@" mention menu's sole content. Mirrors the
  // skills filter's startsWith convention (case-insensitive prefix match;
  // empty filter ⇒ every scoped chat agent).
  //
  // Fix 7: NAME prefix only — no id clause. User-created agent ids are
  // UUIDs (or otherwise arbitrary), so matching `id.startsWith(filter)` too
  // surfaced agents into "@a" whose UUID happened to start with "a" but
  // whose visible name had nothing to do with what was typed — noise
  // unrelated to the label the user is actually reading in the menu.
  const effectiveActiveAgentId = activeAgentId || chatAgents[0]?.id
  const agentItems: SlashItem[] = (() => {
    if (mentionFilter === null) return []
    const filtered = chatAgents.filter((a) =>
      mentionFilter === '' || a.name.toLowerCase().startsWith(mentionFilter),
    )
    // Fix 6: cap at 8, matching visibleSkillMenuItems' own cap. Agents can
    // create agents, so the roster is unbounded in principle — without a
    // cap, a large roster would rebuild N full rows (each with its own
    // avatar/description) on every keystroke, and ArrowDown/ArrowUp
    // wrap-around nav over a very long list stops being usable from the
    // keyboard.
    return filtered.slice(0, 8).map((agent) => ({
      key: agent.id,
      label: `@${agent.name}`,
      description: agent.description || agent.model || '',
      section: 'agents' as const,
      agentColor: agent.color ?? undefined,
      agentIcon: agent.icon ?? undefined,
      agentName: agent.name,
      isActiveAgent: agent.id === effectiveActiveAgentId,
      onSelect: () => selectMentionAgent(agent),
    }))
  })()

  // Unified list for keyboard nav. The "/" and "@" triggers are mutually
  // exclusive (inputValue can't start with both characters at once), so
  // mention mode simply swaps in agentItems as the whole list rather than
  // concatenating — commands/skills never mix with agent rows.
  const slashItems: SlashItem[] = isMentionMode
    ? agentItems
    : [...visibleCommandItems, ...visibleSkillMenuItems]
  // LOW S8: keep the menu open on a commands-query error even when it would
  // otherwise have zero items (e.g. no skills match either) — the caller's
  // "Commands unavailable" row needs somewhere to render. `menuFilter !==
  // null` reuses the same gate (starts with "/", not replaying, enabled) so
  // this never opens the menu for reasons unrelated to a "/" being typed —
  // in particular, it must NOT fire in mention mode: a commands-fetch error
  // has nothing to do with "@" and must not force that menu open when zero
  // agents match. `slashItems.length > 0` alone already covers "agents
  // matched" for mention mode, since agentItems IS slashItems there.
  const shouldShowSlash =
    (slashItems.length > 0 || (commandsError && menuFilter !== null)) && !isReplaying && inputEnabled

  // Reset highlight to 0 when the visible list changes (length or content)
  // so the cursor never points out-of-bounds as the filter narrows.
  const slashItemKeys = slashItems.map((i) => i.key).join(',')
  useEffect(() => { setSlashHighlight(0) }, [slashItemKeys])

  function closeSlash() {
    setSlashOpen(false)
    setSlashHighlight(0)
  }

  // runClientCommand — shared handler for client-delivery slash commands.
  // Called both from palette selection (executeSlashCommand) and from the
  // send-path interception so that typing "/new"+Enter (or its legacy
  // alias "/clear"+Enter) converges with selecting /new from the palette —
  // both run client-side, never reaching the backend.
  //
  // Returns true when the command was handled (caller must NOT send the
  // text), false when the name is not a known client command (caller
  // should fall through to inserting as text — Issue 3 fallback).
  function runClientCommand(name: string): boolean {
    if (name === 'new' || name === 'clear') {
      // Renamed /clear → /new (the palette advertises /new; 'clear' survives
      // as a hidden backend alias for CLI/channel muscle memory). Starts a
      // new conversation (startNewSession), not just a local wipe.
      startNewSession()
      return true
    }

    if (name === 'help') {
      // US-4/AC-2: build the help text from the fetched command list.
      const helpLines = allCommands
        .map((c) => `- \`${c.label}\` — ${c.description}`)
        .join('\n')
      const helpText = `**Omnipus commands:**\n${helpLines}\n\n**Tips:**\n- Press **Enter** to send, **Shift+Enter** for newline\n- Type **@** at the start of the input to switch agents\n- Click tool call headers to expand/collapse details\n- Hover over messages to copy them`
      appendMessage({
        id: generateId(),
        role: 'system',
        content: helpText,
        timestamp: new Date().toISOString(),
        status: 'done',
      })
      return true
    }

    if (name === 'model') {
      // US-4/AC-2: open the model selector in the composer card
      // (composer/ModelPicker.tsx).
      // Setting modelSelectorOpen=true drives the controlled Popover in
      // ModelSelector without the user having to click it directly. Per A4:
      // web-only client action; opens the chat model selector, not the
      // server agent default.
      useUiStore.getState().setModelSelectorOpen(true)
      return true
    }

    if (name === 'agents') {
      // Open the agent selector in the composer card (composer/AgentPicker.tsx) via the ui store flag.
      useUiStore.getState().setAgentSelectorOpen(true)
      return true
    }

    if (name === 'skills') {
      // D9: set input to "/skills" to trigger the skills-only filter in the
      // menu. The menu handles this: when inputValue === "/skills",
      // isSkillsFilter is true and only the Skills section shows. Re-open
      // the menu after the clear.
      composerRuntime.setText('/skills')
      setInputValue('/skills')
      setSlashOpen(true)
      return true
    }

    if (name === 'cancel') {
      // FR-3a: /cancel uses the same cancelIfStreaming() as the local
      // Escape handler — only morph the button to "Stopping..." if the turn
      // is actively streaming.
      cancelIfStreaming()
      return true
    }

    if (name === 'resume') {
      // Web-only: open the cross-workspace session search modal to pick a
      // session to resume — same single instance the sidebar icon opens.
      useUiStore.getState().openSearchModal()
      return true
    }

    // Issue 3 fallback: unknown client command — do NOT silently drop.
    // Return false so the caller inserts it as text rather than clearing
    // the composer.
    return false
  }

  // executeSlashCommand — called when the user selects a palette entry.
  // `label` is the full label string from the SlashCommand (e.g. "/new").
  // FR-009: dispatch by `delivery`:
  //   - 'client' → run the local handler via runClientCommand; do NOT send.
  //   - 'agent'  → insert the label as text into the composer so the user
  //                can complete it and forward it via the message frame on
  //                send.
  function executeSlashCommand(label: string) {
    closeSlash()

    // Look up the full command definition by label to get the name + delivery.
    // Aliases (e.g. "clear" for "/new") never appear as separate palette
    // entries, but match here too so a caller resolving a typed alias label
    // still finds the real definition. LOW S7/C3: lowercase both sides —
    // commands are ASCII, so a caller-resolved label of a different case
    // (e.g. from a typed "/NEW") must still match the canonical entry.
    const labelLower = label.toLowerCase()
    const aliasLower = label.slice(1).toLowerCase()
    const def = allCommands.find(
      (c) => c.label.toLowerCase() === labelLower || c.aliases?.some((a) => a.toLowerCase() === aliasLower),
    )

    if (!def) {
      // Unknown label (shouldn't happen with API-driven palette, but be safe).
      return
    }

    if (def.delivery === 'agent') {
      // Insert "/name " as text so the user can complete it and send.
      composerRuntime.setText(`${def.label} `)
      setInputValue(`${def.label} `)
      return
    }

    // delivery === 'client': clear the composer then run the handler.
    composerRuntime.setText('')
    setInputValue('')

    const handled = runClientCommand(def.name)
    if (!handled) {
      // Issue 3: unknown client command — insert text so it isn't silently lost.
      composerRuntime.setText(`${def.label} `)
      setInputValue(`${def.label} `)
    }
  }

  // completeSkillName — called when the user selects a skill from the
  // partitioned skill menu. Sets the input to `/<id> ` and shows ghost
  // text. Accepts the skill's argument_hint so the ghost shows it instead
  // of the generic `<message>` when the skill declares one (FR-006/R3).
  function completeSkillName(id: string, argumentHint?: string) {
    const text = `/${id} `
    composerRuntime.setText(text)
    setInputValue(text)
    setGhostSkillId(id)
    setGhostArgumentHint(argumentHint ?? null)
    closeSlash()
  }

  // selectMentionAgent — called when the user selects an agent from the "@"
  // mention menu (Enter or click). Same `setActiveSession` contract as
  // AgentPicker's handleAgentSelect (src/components/chat/composer/
  // AgentPicker.tsx): preserves activeSessionId (passing it through
  // unchanged) rather than detaching whatever session is currently active.
  // Unlike completeSkillName, the `@query` text is fully cleared (not left
  // as `@name `) — the mention is a one-shot agent switch, not something the
  // user continues typing after.
  //
  // Fix 5: reads activeSessionId/setActiveSession fresh via `.getState()`
  // rather than closing over the render-time values — this is the write
  // path, so the freshest state wins (matches AgentPicker's own documented
  // pattern) and there is no stale-closure window on a captured
  // activeSessionId between render and the user's actual click/Enter.
  function selectMentionAgent(agent: Agent) {
    const { activeSessionId, setActiveSession } = useSessionStore.getState()
    setActiveSession(activeSessionId, agent.id, agent.type ?? null)
    composerRuntime.setText('')
    setInputValue('')
    closeSlash()
    // Fix 2: announce the switch for screen-reader users — see
    // mentionAnnouncement's doc comment on UseSlashMenuResult. Fix B: track
    // the id THIS selection announced so the reconciliation effect (right
    // after `activeAgentId`, above) can tell this activeAgentId change
    // apart from one driven by a different surface (e.g. AgentPicker).
    lastAnnouncedAgentIdRef.current = agent.id
    setMentionAnnouncement(agent.name)
  }

  // Send-path interception: before AssistantUI's onNew fires, check if the
  // trimmed input is exactly a client-delivery slash command (e.g.
  // "/new", or its legacy alias "/clear"). If it is, run it locally and
  // prevent the message from reaching the backend. This makes typing
  // "/new"+Enter (or "/clear"+Enter) behave identically to palette
  // selection.
  //
  // Deliberately NOT wrapped in useCallback: it (transitively, via
  // runClientCommand) closes over appendMessage/startNewSession/
  // cancelIfStreaming, none of which are guaranteed referentially stable
  // across renders (cancelIfStreaming in particular gets a new identity
  // whenever isStreaming toggles — see useCancelState). None of this hook's
  // returned functions are consumed by a memoized child or an effect
  // dependency list, so there is no performance case for memoizing them —
  // only a staleness risk from an incomplete dependency array. A plain
  // function always closes over the current render's values, matching the
  // original (pre-extraction) composer's own behavior exactly.
  function interceptClientCommand(): boolean {
    const currentText = composerRuntime.getState().text ?? inputValue
    const trimmed = currentText.trim()
    if (!trimmed.startsWith('/')) return false
    const typedName = trimmed.slice(1)
    // LOW S7/C3: lowercase both sides — commands are ASCII, so "/Clear" or
    // "/NEW" + Enter must intercept identically to the canonical-case form
    // instead of silently falling through to the LLM as chat text.
    const trimmedLower = trimmed.toLowerCase()
    const typedNameLower = typedName.toLowerCase()
    const def = allCommands.find(
      (c) => c.delivery === 'client' && (c.label.toLowerCase() === trimmedLower || c.aliases?.some((a) => a.toLowerCase() === typedNameLower)),
    )
    if (!def) return false
    composerRuntime.setText('')
    setInputValue('')
    runClientCommand(def.name)
    return true
  }

  function onInputChange(val: string) {
    setInputValue(val)
    // Clear ghost if value no longer exactly matches `/<ghostSkillId> `
    if (ghostSkillId && val !== `/${ghostSkillId} `) {
      setGhostSkillId(null)
      setGhostArgumentHint(null)
    }
    // Leading "/" opens the command/skill palette, leading "@" opens the
    // agent-mention menu — mid-text "@" (e.g. "hello @x") must NOT trigger,
    // hence startsWith rather than includes. Every keystroke re-runs this
    // (including the first character after "@"), so the second typed
    // character already filters — there is no separate "arm" step.
    if (val.startsWith('/') || val.startsWith('@')) {
      setSlashOpen(true)
    } else {
      closeSlash()
    }
  }

  function onInputBlur() {
    // Delay so mouseDown on slash item fires first
    setGhostSkillId(null)
    setGhostArgumentHint(null)
    setTimeout(closeSlash, 150)
  }

  function onHoverItem(index: number) {
    setSlashHighlight(index)
  }

  function handleKeyDown(e: ReactKeyboardEvent) {
    if (!shouldShowSlash) return

    // Fix D (bugfixes3 sign-off): shouldShowSlash stays true even when
    // slashItems is empty (the LOW S8 commandsError carve-out — see
    // shouldShowSlash's own comment above), which happens for real in
    // slash mode when the commands query has errored AND the typed prefix
    // matches no skill either (e.g. "/nomatch" — the "Commands
    // unavailable"-row-only menu). With zero items: ArrowUp/Down computed
    // `% 0` (NaN highlight), and Enter called preventDefault() then
    // no-op'd — the user could not send until Escape. There is nothing to
    // navigate or select, so skip straight to the Escape branch below;
    // Enter deliberately does NOT preventDefault here, so it falls through
    // to the caller's normal submit path instead of silently swallowing
    // the keystroke.
    if (slashItems.length === 0) {
      if (e.key === 'Escape') closeSlash()
      return
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSlashHighlight((h) => (h + 1) % slashItems.length)
      setSlashOpen(true)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSlashHighlight((h) => (h - 1 + slashItems.length) % slashItems.length)
      setSlashOpen(true)
    } else if (e.key === 'Enter' && slashOpen && !e.shiftKey && !e.nativeEvent?.isComposing) {
      // Fix 8: Shift+Enter is universally "insert a newline" in this
      // composer (and everywhere else) — intercepting it to select from the
      // menu instead silently ate the newline and wiped/mutated multiline
      // drafts that happened to start with "/" or "@". Falls through to the
      // textarea's native handling: the newline gets inserted, and the
      // menu's filter re-runs on the next onChange against text that now
      // contains that embedded "\n" — since no command/skill/agent name
      // ever contains a newline, that almost always matches nothing, so
      // the menu typically CLOSES rather than staying open and refiltering
      // (J.2 correction, bugfixes3 sign-off). That's fine: the user is
      // mid-multiline-draft at that point, not browsing the palette.
      //
      // Fix C (bugfixes3 sign-off): also bail while an IME composition is
      // in progress (`e.nativeEvent.isComposing`) — the Enter that COMMITS
      // a CJK/Japanese/Korean composition must never be read as "select
      // the highlighted row," or it silently selects an agent/command and
      // wipes whatever the user was still composing. This matters more
      // here than it might look: agent names (the "@" mention menu's
      // whole content) are far likelier to be non-ASCII than command
      // names, which are a fixed, ASCII, developer-authored list.
      e.preventDefault()
      slashItems[slashHighlight]?.onSelect()
    } else if (e.key === 'Escape') {
      closeSlash()
    }
  }

  const showGhostText = ghostSkillId !== null && inputValue === `/${ghostSkillId} `
  const ghostText = ghostArgumentHint ?? GHOST_TEXT_PLACEHOLDER

  return {
    inputValue,
    slashOpen,
    slashHighlight,
    shouldShowSlash,
    slashItems,
    commandsError,
    isSkillsFilter,
    isMentionMode,
    mentionAnnouncement,
    showGhostText,
    ghostText,
    onInputChange,
    onInputBlur,
    onHoverItem,
    handleKeyDown,
    interceptClientCommand,
  }
}
