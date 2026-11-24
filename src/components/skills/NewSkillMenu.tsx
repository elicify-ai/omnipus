// NewSkillMenu — the "New skill" button of Skills & Tools (spec S-52 to
// S-54, C-8): write one with Omnipus, upload one, or browse the catalog.

import { useContext, useState } from 'react'
import { CaretDown, Plus } from '@phosphor-icons/react'
import { useRouter } from '@tanstack/react-router'
import { QueryClientContext } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { Session, Workspace } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import { queryClient as appQueryClient } from '@/lib/queryClient'
import { openOmnipusChat } from '@/store/omnipusChat'
import { useConnectionStore } from '@/store/connection'

const WRITE_TEXT = 'Write a new skill'

interface NewSkillMenuProps {
  /** Whether the live connection is up; read from the connection store when omitted. */
  connectionUp?: boolean
  onUpload?: () => void
  onBrowse?: () => void
}

export function NewSkillMenu({ connectionUp, onUpload, onBrowse }: NewSkillMenuProps) {
  const storeConnected = useConnectionStore((state) => state.isConnected)
  const connected = connectionUp ?? storeConnected
  const router = useRouter({ warn: false })
  const client = useContext(QueryClientContext) ?? appQueryClient
  const [offline, setOffline] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)

  const write = () => {
    if (!connected) {
      // Nothing is sent or queued: an offline workflow start would lose its card.
      setOffline(true)
      return
    }
    setOffline(false)
    const workspaces = client.getQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' })) ?? []
    const sessions = client.getQueryData<Session[]>(['sessions']) ?? []
    const outcome = openOmnipusChat({ workspaces, sessions, workflow: { workflow: 'W5', text: WRITE_TEXT } })
    if (outcome !== 'attach-failed') void router?.navigate({ to: '/omnipus' })
  }

  return (
    <div className="flex flex-col items-end gap-[var(--space-2)]">
      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger asChild>
          <Button
            // Radix opens on pointer-down and Enter/Space. A click with no
            // pointer press behind it (detail 0: assistive tech, scripted
            // clicks) opens the menu too; a mouse click was already handled.
            onClick={(event) => { if (event.detail === 0) setMenuOpen(true) }}
            size="sm"
            className="gap-[var(--space-1)] pointer-coarse:min-h-[var(--target-touch-minimum)]">
            <Plus size={14} weight="bold" aria-hidden="true" />
            New skill
            <CaretDown size={11} aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={write}>Write with Omnipus</DropdownMenuItem>
          <DropdownMenuItem onClick={() => onUpload?.()}>Upload a skill</DropdownMenuItem>
          <DropdownMenuItem onClick={() => onBrowse?.()}>Browse the catalog</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {offline && (
        <Callout variant="warning">
          <CalloutDescription>
            {`Not connected — "${WRITE_TEXT}" was not sent. Press the button again once you are reconnected.`}
          </CalloutDescription>
        </Callout>
      )}
    </div>
  )
}
