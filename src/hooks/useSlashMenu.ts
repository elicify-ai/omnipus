// useSlashMenu — partitioned slash-command + skill palette for the chat
// composer (FR-005/FR-006/FR-009/FR-014/D9/R3), plus the ghost-text hint
// shown after selecting a skill. Extracted out of OmnipusComposer's
// ~862-line body (Wave 3 structural refactor) — behavior is unchanged, only
// ownership moved.
//
// Owns the composer's text mirror (`inputValue`) — AssistantUI's
// `composerRuntime` is the actual source of truth for what gets sent, but
// this hook needs a React-reactive copy of the current text to: gate
// whether the menu should be showing (`inputValue.startsWith('/')`), decide
// whether the ghost-text overlay should render, and filter the palette.
// Every place that needs to change the composer's text (selecting a
// command/skill, running a client command, the plain onChange handler)
// goes through this hook's actions so `composerRuntime.setText(...)` and
// the local mirror never drift apart — see each action below.
//
// The `/cancel` client command needs to drive the Stop button's visual
// state, which is owned by the sibling `useCancelState` hook — the caller
// wires that hook's `cancelIfStreaming` in as a parameter rather than this
// hook reaching across to import it, keeping the two hooks independently
// testable.

import { useEffect, useState } from 'react'
import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { ComposerRuntime } from '@assistant-ui/react'
import { generateId } from '@/lib/constants'
import { fetchCommands, fetchSkills } from '@/lib/api'
import type { SlashCommand, Skill } from '@/lib/api'
import type { ChatMessage } from '@/store/chat'
import { useUiStore } from '@/store/ui'

// ── Slash/skill palette item shape ───────────────────────────────────────────

export interface SlashItem {
  key: string
  label: string
  description: string
  section: 'commands' | 'skills'
  argumentHint?: string
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
  /** True when the ghost-text overlay should render (value is exactly `/<skillId> ` right after that skill was selected from the menu). */
  showGhostText: boolean
  /** The ghost text to display — the skill's argument_hint if it declared one, else the generic placeholder. */
  ghostText: string
  /** Composer textarea onChange — updates the mirror, clears a stale ghost, and opens/closes the menu on the leading "/". */
  onInputChange: (val: string) => void
  /** Composer textarea onBlur — clears ghost text and closes the menu (delayed so a mouseDown on a menu item fires first). */
  onInputBlur: () => void
  /** Menu item onMouseEnter — moves the keyboard highlight to the hovered item. */
  onHoverItem: (index: number) => void
  /** ArrowUp/ArrowDown/Enter-select/Escape-close — no-ops when the menu isn't showing. Cancel-Escape and Enter-blocked-while-streaming are handled by the caller BEFORE this, per the original composer's precedence. */
  handleKeyDown: (e: ReactKeyboardEvent) => void
  /**
   * Send-path interception: if the composer's current text is exactly a
   * client-delivery slash command (e.g. "/clear"), runs it locally and
   * returns true — caller must preventDefault() so the message never
   * reaches the backend. Makes typing "/clear"+Enter behave identically to
   * selecting it from the palette.
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

  // US-4 / FR-008: fetch the web-surface command list from the single
  // source of truth. On error the palette shows nothing — no crash (per
  // integration boundary spec).
  const { data: commands = [] } = useQuery<SlashCommand[]>({
    queryKey: ['commands', 'web'],
    queryFn: () => fetchCommands('web'),
    staleTime: 60_000,
    enabled: inputEnabled,
  })

  // Skills query: always enabled when input is enabled (not gated on
  // skill-arg mode). staleTime of 60s matches the commands query — skills
  // change rarely.
  const { data: skills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    queryFn: () => fetchSkills(),
    staleTime: 60_000,
    enabled: inputEnabled,
  })

  // FR-005: partitioned slash menu — Commands + Skills sections. Triggered
  // when input starts with "/" (no old skill-arg gate).
  const menuFilter = (() => {
    if (!inputValue.startsWith('/') || isReplaying || !inputEnabled) return null
    return inputValue.slice(1).toLowerCase() // the text after "/"
  })()

  const isSkillsFilter = menuFilter === 'skills'

  // Commands section — hidden when typing "/skills" (D9)
  const visibleCommandItems: SlashItem[] = (() => {
    if (menuFilter === null || isSkillsFilter) return []
    const all = commands.filter((cmd) => {
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

  // Unified list for keyboard nav — commands first, then skills
  const slashItems: SlashItem[] = [...visibleCommandItems, ...visibleSkillMenuItems]
  const shouldShowSlash = slashItems.length > 0 && !isReplaying && inputEnabled

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
  // send-path interception so that typing "/clear"+Enter converges with
  // selecting /clear from the palette — both run client-side, never
  // reaching the backend.
  //
  // Returns true when the command was handled (caller must NOT send the
  // text), false when the name is not a known client command (caller
  // should fall through to inserting as text — Issue 3 fallback).
  function runClientCommand(name: string): boolean {
    if (name === 'clear') {
      // US-4/AC-2: start a new conversation per spec — aligned to the "new
      // conversation" semantic (startNewSession) rather than just wiping
      // the local message list.
      startNewSession()
      return true
    }

    if (name === 'help') {
      // US-4/AC-2: build the help text from the fetched command list.
      const helpLines = commands
        .map((c) => `- \`${c.label}\` — ${c.description}`)
        .join('\n')
      const helpText = `**Omnipus commands:**\n${helpLines}\n\n**Tips:**\n- Press **Enter** to send, **Shift+Enter** for newline\n- Click tool call headers to expand/collapse details\n- Hover over messages to copy them`
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

    // Issue 3 fallback: unknown client command — do NOT silently drop.
    // Return false so the caller inserts it as text rather than clearing
    // the composer.
    return false
  }

  // executeSlashCommand — called when the user selects a palette entry.
  // `label` is the full label string from the SlashCommand (e.g. "/clear").
  // FR-009: dispatch by `delivery`:
  //   - 'client' → run the local handler via runClientCommand; do NOT send.
  //   - 'agent'  → insert the label as text into the composer so the user
  //                can complete it and forward it via the message frame on
  //                send.
  function executeSlashCommand(label: string) {
    closeSlash()

    // Look up the full command definition by label to get the name + delivery.
    const def = commands.find((c) => c.label === label)

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

  // Send-path interception: before AssistantUI's onNew fires, check if the
  // trimmed input is exactly a client-delivery slash command (e.g.
  // "/clear"). If it is, run it locally and prevent the message from
  // reaching the backend. This makes typing "/clear"+Enter behave
  // identically to palette selection.
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
    const def = commands.find((c) => c.delivery === 'client' && c.label === trimmed)
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
    if (val.startsWith('/')) {
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

    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSlashHighlight((h) => (h + 1) % slashItems.length)
      setSlashOpen(true)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSlashHighlight((h) => (h - 1 + slashItems.length) % slashItems.length)
      setSlashOpen(true)
    } else if (e.key === 'Enter' && slashOpen) {
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
    showGhostText,
    ghostText,
    onInputChange,
    onInputBlur,
    onHoverItem,
    handleKeyDown,
    interceptClientCommand,
  }
}
