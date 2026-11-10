// RoomComposer — a group chat's message box with its @-picker
// (omnipus-system-agent FR-164, FR-175, FR-205; §6.13; K26).
//
// The @-picker offers `@all` and each CURRENT member (muted members too —
// they stay explicitly addressable, FR-158). Picking inserts a token bound to
// the member's agent id; `mentions` carries those ids and they are
// authoritative, even if the member is renamed before sending (§6.13). A
// picked token the user deletes stops addressing that member. Typed names
// are sent as plain text: the server resolves them by §6.13's exact-name rule.
// Slash commands are sent as room text; the room accepts six (FR-205) and the
// server refuses the rest. Nothing here sends a main-chat command frame.

import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { PaperPlaneRight } from '@phosphor-icons/react'
import type { Agent } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Command, CommandItem, CommandList } from '@/components/ui/command'
import { Textarea } from '@/components/ui/textarea'

const ALL = 'all'

interface MentionOption { id: string; label: string }
interface Pick { id: string; token: string }

export interface RoomComposerProps {
  members: Agent[]
  /** Why sending is not possible right now; undefined when it is. */
  blockedReason?: string
  onSend: (text: string, mentions: string[]) => Promise<boolean>
  /** "Ask again" (§6.11): pre-address one member; `nonce` changes per request. */
  prefill?: { agentId: string; nonce: number }
}

/** The "@word" being typed right before the caret, or null. */
function activeMentionQuery(text: string, caret: number): string | null {
  const match = /(?:^|\s)@([^\s@]*)$/.exec(text.slice(0, caret))
  return match ? match[1] ?? '' : null
}

function mentionsOf(text: string, picks: Pick[]): string[] {
  const ids = picks.filter((pick) => text.includes(pick.token)).map((pick) => pick.id)
  return [...new Set(ids)]
}

export function RoomComposer({ members, blockedReason, onSend, prefill }: RoomComposerProps) {
  const [text, setText] = useState('')
  const [caret, setCaret] = useState(0)
  const [picks, setPicks] = useState<Pick[]>([])
  const [highlight, setHighlight] = useState(0)
  const [dismissed, setDismissed] = useState(false)
  const [sending, setSending] = useState(false)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const pendingCaret = useRef<number | null>(null)

  const options = useMemo<MentionOption[]>(
    () => [{ id: ALL, label: '@all' }, ...members.map((member) => ({ id: member.id, label: `@${member.name}` }))],
    [members],
  )
  const query = dismissed ? null : activeMentionQuery(text, caret)
  const visible = query === null
    ? []
    : options.filter((option) => option.label.toLowerCase().startsWith(`@${query.toLowerCase()}`))
  const highlighted = visible[Math.min(highlight, visible.length - 1)]

  useEffect(() => {
    if (pendingCaret.current === null || !inputRef.current) return
    inputRef.current.focus()
    inputRef.current.setSelectionRange(pendingCaret.current, pendingCaret.current)
    pendingCaret.current = null
  }, [text])

  useEffect(() => {
    if (!prefill) return
    const member = members.find((candidate) => candidate.id === prefill.agentId)
    if (!member) return
    const token = `@${member.name}`
    setText(`${token} `)
    setPicks([{ id: member.id, token }])
    pendingCaret.current = token.length + 1
    // Only a new "ask again" request re-addresses; a roster refresh must not.
  }, [prefill?.nonce])

  function choose(option: MentionOption) {
    const start = caret - (query?.length ?? 0) - 1
    const next = `${text.slice(0, start)}${option.label} ${text.slice(caret)}`
    pendingCaret.current = start + option.label.length + 1
    setPicks((current) => [...current, { id: option.id, token: option.label }])
    setText(next)
    setCaret(pendingCaret.current)
    setHighlight(0)
  }

  async function submit() {
    const trimmed = text.trim()
    if (!trimmed || blockedReason || sending) return
    setSending(true)
    const leaves = await onSend(trimmed, mentionsOf(trimmed, picks))
    setSending(false)
    if (leaves) {
      setText('')
      setPicks([])
      setCaret(0)
    }
  }

  function onKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (visible.length > 0) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault()
        const step = event.key === 'ArrowDown' ? 1 : visible.length - 1
        setHighlight((current) => (Math.min(current, visible.length - 1) + step) % visible.length)
        return
      }
      if (event.key === 'Enter' && highlighted) {
        event.preventDefault()
        choose(highlighted)
        return
      }
      if (event.key === 'Escape') {
        event.preventDefault()
        setDismissed(true)
        return
      }
    }
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      void submit()
    }
  }

  return (
    <div className="flex flex-col gap-[var(--space-2)] border-t border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2)]">
      {visible.length > 0 && (
        <Command shouldFilter={false} value={highlighted?.id} label="Address a member" className="max-h-[40dvh] rounded-xl border border-[var(--color-border)]">
          <CommandList>
            {visible.map((option) => (
              <CommandItem key={option.id} value={option.id} onSelect={() => choose(option)}>
                {option.label}
              </CommandItem>
            ))}
          </CommandList>
        </Command>
      )}
      {blockedReason && (
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{blockedReason}</p>
      )}
      <div className="flex items-end gap-[var(--space-2)]">
        <Textarea
          ref={inputRef}
          aria-label="Message the group"
          placeholder="Message the group — @ to address members"
          rows={2}
          value={text}
          onChange={(event) => {
            setText(event.target.value)
            setCaret(event.target.selectionStart ?? event.target.value.length)
            setDismissed(false)
            setHighlight(0)
          }}
          onSelect={(event) => setCaret(event.currentTarget.selectionStart ?? 0)}
          onKeyDown={onKeyDown}
          className="flex-1 resize-none"
        />
        <Button type="button" onClick={() => void submit()} disabled={Boolean(blockedReason) || sending || text.trim().length === 0}>
          <PaperPlaneRight aria-hidden="true" size={16} />
          Send
        </Button>
      </div>
    </div>
  )
}
