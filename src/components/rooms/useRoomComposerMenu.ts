// useRoomComposerMenu — the group chat's "/" and "@" menus in the shared
// message box (#1449 US-2 AS 2.4/2.5; FR-010, FR-011; ports RoomComposer's
// rules exactly, ADR-20261028 A2.2).
//
// "/" (leading) lists the six group commands; picking one inserts it as room
// text — the room service parses commands from the person's posts only
// (FR-168), and refuses every other command with links to the members'
// chats (FR-205). "@" (anywhere, before the caret) offers `@all` and every
// CURRENT member, muted ones included (FR-158). A pick inserts a token bound
// to the member's agent id; the ids of picks whose token is still in the
// text are sent as `mentions` and stay authoritative through a rename
// (§6.13). Deleting a picked token removes only that explicit addressing;
// typed names are plain text the server resolves by the exact-name rule.
//
// Returns the same shape the session menu (useSlashMenu) returns, so the
// shared ComposerMenu renders it unchanged.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import type { ComposerRuntime } from '@assistant-ui/react'
import type { Agent } from '@/lib/api'
import type { SlashItem, UseSlashMenuResult } from '@/hooks/useSlashMenu'

/** The six commands a group accepts (FR-205), in menu order. */
export const ROOM_COMMAND_NAMES = ['pause', 'resume', 'mute', 'unmute', 'members', 'help'] as const

const ALL = 'all'

interface Pick { id: string; token: string } // not-wire-format: local addressing bookkeeping

/** The "@word" being typed right before the caret, or null. */
export function activeMentionQuery(text: string, caret: number): string | null {
  const match = /(?:^|\s)@([^\s@]*)$/.exec(text.slice(0, caret))
  return match ? match[1] ?? '' : null
}

/** The explicit picks whose token is still in the text (deduplicated ids). */
export function mentionsOf(text: string, picks: readonly Pick[]): string[] {
  return [...new Set(picks.filter((pick) => text.includes(pick.token)).map((pick) => pick.id))]
}

function caretOf(fallback: number): number {
  const active = typeof document !== 'undefined' ? document.activeElement : null
  return active instanceof HTMLTextAreaElement && typeof active.selectionStart === 'number' ? active.selectionStart : fallback
}

function placeCaret(position: number): void {
  requestAnimationFrame(() => {
    const input = document.querySelector<HTMLTextAreaElement>('[data-testid="chat-input"]')
    if (!input) return
    input.focus()
    input.setSelectionRange(position, position)
  })
}

export interface RoomComposerMenu extends UseSlashMenuResult {
  /** Explicit `mentions` for a message about to be sent with `text`. */
  mentionsFor: (text: string) => string[]
  /** The message left the box: its picks are spent. */
  clearPicks: () => void
}

export function useRoomComposerMenu({
  composerRuntime,
  members,
  muted,
  prefill,
}: {
  composerRuntime: ComposerRuntime
  members: readonly Agent[]
  muted: readonly string[]
  /** Ask again (FR-036): pre-address one member; a new `nonce` per request. */
  prefill?: { agentId: string; nonce: number }
}): RoomComposerMenu {
  const [inputValue, setInputValue] = useState('')
  const [caret, setCaret] = useState(0)
  const [highlight, setHighlight] = useState(0)
  const [dismissed, setDismissed] = useState(false)
  const [announcement, setAnnouncement] = useState<string | null>(null)
  const picks = useRef<Pick[]>([])
  const mirror = useRef('')
  mirror.current = inputValue

  // The runtime clears its own text on send without a change event: keep the
  // mirror in step with it (the session menu's Fix A, same reason).
  useEffect(() => composerRuntime.subscribe(() => {
    const text = composerRuntime.getState().text ?? ''
    if (text !== mirror.current) {
      setInputValue(text)
      setCaret(text.length)
    }
  }), [composerRuntime])

  const setText = useCallback((next: string, position: number) => {
    composerRuntime.setText(next)
    setInputValue(next)
    setCaret(position)
    setHighlight(0)
    placeCaret(position)
  }, [composerRuntime])

  // Ask again: address the member; nothing is sent (FR-036).
  useEffect(() => {
    if (!prefill) return
    const member = members.find((candidate) => candidate.id === prefill.agentId)
    if (!member) return
    const token = `@${member.name}`
    picks.current = [{ id: member.id, token }]
    setText(`${token} `, token.length + 1)
    // Only a new Ask again re-addresses; a roster refresh must not.
  }, [prefill?.nonce])

  const commandQuery = /^\/(\S*)$/.exec(inputValue)?.[1]
  const mentionQuery = commandQuery === undefined ? activeMentionQuery(inputValue, caret) : null

  const items = useMemo<SlashItem[]>(() => {
    if (commandQuery !== undefined) {
      const prefix = commandQuery.toLowerCase()
      return ROOM_COMMAND_NAMES.filter((name) => name.startsWith(prefix)).map((name) => ({
        key: `room-command-${name}`,
        label: name,
        description: '',
        section: 'commands' as const,
        onSelect: () => setText(`/${name} `, name.length + 2),
      }))
    }
    if (mentionQuery === null) return []
    const options = [
      { id: ALL, label: '@all', description: 'Everyone in the group', agent: undefined as Agent | undefined },
      ...members.map((member) => ({
        id: member.id,
        label: `@${member.name}`,
        description: muted.includes(member.id) ? 'muted' : '',
        agent: member as Agent | undefined,
      })),
    ]
    const lower = `@${mentionQuery.toLowerCase()}`
    return options.filter((option) => option.label.toLowerCase().startsWith(lower)).map((option) => ({
      key: `room-mention-${option.id}`,
      label: option.label,
      description: option.description,
      section: 'agents' as const,
      ...(option.agent ? { agent: option.agent } : {}),
      onSelect: () => {
        const start = caret - mentionQuery.length - 1
        const next = `${inputValue.slice(0, start)}${option.label} ${inputValue.slice(caret)}`
        picks.current = [...picks.current, { id: option.id, token: option.label }]
        setAnnouncement(option.id === ALL ? 'everyone' : option.label.slice(1))
        setText(next, start + option.label.length + 1)
      },
    }))
  }, [commandQuery, mentionQuery, members, muted, caret, inputValue, setText])

  const open = !dismissed && items.length > 0
  const clamped = items.length > 0 ? Math.min(highlight, items.length - 1) : 0

  const handleKeyDown = useCallback((event: ReactKeyboardEvent) => {
    if (!open) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const step = event.key === 'ArrowDown' ? 1 : items.length - 1
      setHighlight((current) => (Math.min(current, items.length - 1) + step) % items.length)
      return
    }
    if ((event.key === 'Enter' && !event.shiftKey) || event.key === 'Tab') {
      event.preventDefault()
      items[clamped]?.onSelect()
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      setDismissed(true)
    }
  }, [open, items, clamped])

  return {
    inputValue,
    slashOpen: open,
    slashHighlight: clamped,
    shouldShowSlash: items.length > 0,
    slashItems: items,
    commandsError: false,
    isSkillsFilter: false,
    isMentionMode: mentionQuery !== null,
    skillsHiddenCount: 0,
    agentsHiddenCount: 0,
    mentionAnnouncement: announcement,
    showGhostText: false,
    ghostText: '',
    onInputChange: (value: string) => {
      setInputValue(value)
      setCaret(caretOf(value.length))
      setDismissed(false)
      setHighlight(0)
    },
    // Delayed so a mouse-down on a menu row selects before the menu closes.
    onInputBlur: () => { setTimeout(() => setDismissed(true), 150) },
    onHoverItem: setHighlight,
    handleKeyDown,
    // Every group command is room text; nothing runs locally.
    interceptClientCommand: () => false,
    mentionsFor: (text: string) => mentionsOf(text, picks.current),
    clearPicks: () => { picks.current = [] },
  }
}
