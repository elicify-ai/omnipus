// DemoWorkspace.tsx — the demo's chat column (the shell's `chat` child):
// a mock chat (interactive — the founder demo rules require clicking to do
// something), the six-panel tab strip (US-5/MAJ-007 toggle semantics via
// aria-pressed), and a two-workspace switcher that demonstrates SP-13's
// per-workspace width memory (open Library in A, resize, switch to B, open
// Library — B has its OWN remembered width).

import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { setLibraryEditorDirty } from '@/components/library/preview/unsavedGuard'
import { cn } from '@/lib/utils'
import { SidePanelShell } from '../SidePanelShell'
import { usePanelShell, PANEL_TRIGGER_ATTR } from '../usePanelShell'
import { DEMO_PANELS } from './demoPanelRegistry'
import { DEMO_USERNAME } from './fixtures'
import type { PanelId } from '../types'

const PANEL_BUTTONS: Array<{ id: PanelId; label: string }> = [
  { id: 'library', label: 'Library' },
  { id: 'browser', label: 'Browser' },
  { id: 'mail', label: 'Mail' },
  { id: 'tasks', label: 'Tasks' },
  { id: 'team', label: 'Team' },
  { id: 'calendar', label: 'Calendar' },
]

const WORKSPACES = [
  { id: 'ws-alpha', label: 'Alpha Research' },
  { id: 'ws-beta', label: 'Beta Notes' },
]

export function DemoWorkspace() {
  const shell = usePanelShell(DEMO_PANELS, DEMO_USERNAME)
  const [workspaceId, setWorkspaceId] = useState(WORKSPACES[0]!.id)
  const [messages, setMessages] = useState<Array<{ id: number; text: string }>>([
    { id: 1, text: 'Mock chat — send a message, then open a side panel.' },
  ])
  const [draft, setDraft] = useState('')

  const openPanel = (id: PanelId) => {
    shell.requestToggle(id, { workspaceId })
  }

  const sendMessage = () => {
    const text = draft.trim()
    if (text === '') return
    setMessages((m) => [...m, { id: m.length + 1, text }])
    setDraft('')
  }

  return (
    <SidePanelShell panels={DEMO_PANELS} username={DEMO_USERNAME} chat={
      <div className="flex h-full min-h-0 flex-col">
        <div
          data-testid="demo-toolbar"
          className="flex min-h-10 shrink-0 flex-wrap items-center gap-[var(--space-1)] border-b border-[var(--color-border)] px-[var(--space-2)]"
        >
          {WORKSPACES.map((w) => (
            <Button
              key={w.id}
              size="sm"
              variant={workspaceId === w.id ? 'secondary' : 'ghost'}
              aria-pressed={workspaceId === w.id}
              data-testid={`workspace-switch-${w.id}`}
              onClick={() => setWorkspaceId(w.id)}
            >
              {w.label}
            </Button>
          ))}
          <span className="grow" />
          <Button
            size="sm"
            variant="ghost"
            data-testid="demo-set-dirty"
            onClick={() => setLibraryEditorDirty(true)}
          >
            Mark Library dirty
          </Button>
          {PANEL_BUTTONS.map((p) => {
          const pressed = shell.activePanel?.id === p.id
            return (
              <Button
                key={p.id}
                size="sm"
                variant={pressed ? 'secondary' : 'ghost'}
                aria-pressed={pressed}
                data-testid={`panel-trigger-${p.id}`}
                {...{ [PANEL_TRIGGER_ATTR]: p.id }}
                onClick={() => openPanel(p.id)}
              >
                {p.label}
              </Button>
            )
          })}
        </div>

        <div data-testid="chat-messages" className="min-h-0 flex-1 overflow-y-auto p-[var(--space-3)]">
          {messages.map((m) => (
            <div
              key={m.id}
              className={cn(
                'mb-[var(--space-2)] max-w-[70%] rounded-lg px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-body-compact-size)]',
                m.id === 1
                  ? 'bg-[var(--color-surface-2)] text-[var(--color-muted)]'
                  : 'ml-auto bg-[var(--color-accent)] text-[var(--color-primary)]',
              )}
            >
              {m.text}
            </div>
          ))}
        </div>

        <div
          data-testid="composer-card"
          className={cn(
            'flex shrink-0 items-center gap-[var(--space-2)] border-t border-[var(--color-border)] p-[var(--space-2)]',
            'focus-within:border-[var(--color-accent)]',
          )}
        >
          <Textarea
            data-testid="chat-input"
            data-no-focus-ring=""
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                sendMessage()
              }
            }}
            placeholder="Type a message (Enter to send)"
            className="min-h-8 flex-1 resize-none bg-[var(--color-surface-2)] text-[length:var(--type-body-compact-size)]"
          />
          <Button size="sm" data-testid="chat-send" onClick={sendMessage}>
            Send
          </Button>
        </div>
      </div>
    } />
  )
}
