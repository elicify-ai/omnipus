// useExternalServices — the External tools tab's service management (spec
// User Story 8, S-59 to S-63): Add service, and on each service group header
// the on/off switch (acts at once), Test, Edit and Remove; a service that
// cannot connect shows its reason with Retry and Edit. Adding and editing use
// McpServerModal, whose local-program confirm is the S-63 dialog.

import { useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleNotch, PencilSimple, PlugsConnected, Plus, Trash } from '@phosphor-icons/react'
import { deleteMcpServer, fetchMcpServers, isApiError, testMcpServer, updateMcpServer, type McpServer } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Callout, CalloutActions, CalloutDescription } from '@/components/ui/callout'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { IconButton } from '@/components/ui/icon-button'
import { Switch } from '@/components/ui/switch'
import { useUiStore } from '@/store/ui'
import { McpServerModal } from './McpServerModal'

/** The group id the catalog gives a service's tools (skillsToolsModel::toolItems). */
export function serviceGroupId(serverId: string): string {
  return `mcp:${serverId}`
}

const ICON_CLASS = 'h-7 w-7 text-[var(--color-muted)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]'

function reasonOf(err: unknown): string {
  if (isApiError(err)) return err.userMessage
  return err instanceof Error ? err.message : String(err)
}

export function useExternalServices(disabled: boolean) {
  const client = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const serversQuery = useQuery({ queryKey: ['mcp-servers'], queryFn: fetchMcpServers })
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<McpServer | null>(null)
  const [removing, setRemoving] = useState<McpServer | null>(null)
  const [testing, setTesting] = useState<string | null>(null)

  const servers = serversQuery.data ?? []
  const byGroup = new Map(servers.map((server) => [serviceGroupId(server.id), server]))

  // A service change reconnects it on the server, which changes the tool
  // lists every screen reads (mirrors McpServerModal's add/edit refresh).
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['mcp-servers'] })
    void client.invalidateQueries({ queryKey: ['registry-tools'] })
    void client.invalidateQueries({ queryKey: ['agent-tools'] })
    void client.invalidateQueries({ queryKey: ['tools'] })
  }

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => updateMcpServer(id, { enabled }),
    onSuccess: refresh,
    onError: (err: unknown, { enabled }) => {
      addToast({ message: `Could not turn the service ${enabled ? 'on' : 'off'}: ${reasonOf(err).replace(/\.+$/, '')}.`, variant: 'error' })
    },
  })

  const remove = useMutation({
    mutationFn: (server: McpServer) => deleteMcpServer(server.id),
    onSuccess: (_answer, server) => {
      refresh()
      addToast({ message: `Removed ${server.name}.`, variant: 'success' })
    },
    onError: (err: unknown, server) => {
      addToast({ message: `Could not remove ${server.name}: ${reasonOf(err).replace(/\.+$/, '')}.`, variant: 'error' })
    },
  })

  const test = async (server: McpServer) => {
    setTesting(server.id)
    try {
      const result = await testMcpServer(server.id)
      refresh()
      if (result.success) {
        const count = result.tool_count ?? 0
        addToast({ message: `${server.name} connected: ${count} tool${count === 1 ? '' : 's'}.`, variant: 'success' })
      } else {
        addToast({ message: `${server.name} did not connect: ${result.message}`, variant: 'error' })
      }
    } catch (err) {
      addToast({ message: `Could not test ${server.name}: ${reasonOf(err).replace(/\.+$/, '')}.`, variant: 'error' })
    } finally {
      setTesting(null)
    }
  }

  const edit = (server: McpServer | null) => {
    setEditing(server)
    setModalOpen(true)
  }

  const extraGroups = servers.map((server) => ({ id: serviceGroupId(server.id), label: server.id }))

  const controls = (groupId: string): ReactNode => {
    const server = byGroup.get(groupId)
    if (!server) return null
    const on = server.enabled !== false
    return (
      <div className="flex shrink-0 items-center gap-[var(--space-1)]">
        {/* UI states, External tools Partial: said in the header, so it shows while the group is closed. */}
        {server.status !== 'connected' && (
          <span className="text-[length:var(--type-caption-size)] text-[var(--color-warning)]">Not connected</span>
        )}
        <Switch
          checked={on}
          disabled={disabled || toggle.isPending}
          onCheckedChange={(checked) => toggle.mutate({ id: server.id, enabled: checked })}
          aria-label={`${server.name} on`}
          data-testid={`service-switch-${server.id}`}
        />
        <IconButton aria-label={`Test ${server.name}`} disabled={disabled || testing === server.id} onClick={() => void test(server)} className={ICON_CLASS}>
          {testing === server.id ? <CircleNotch size={14} className="animate-spin" /> : <PlugsConnected size={14} />}
        </IconButton>
        <IconButton aria-label={`Edit ${server.name}`} disabled={disabled} onClick={() => edit(server)} className={ICON_CLASS}>
          <PencilSimple size={14} />
        </IconButton>
        <IconButton aria-label={`Remove ${server.name}`} disabled={disabled} onClick={() => setRemoving(server)} className={ICON_CLASS}>
          <Trash size={14} />
        </IconButton>
      </div>
    )
  }

  const notice = (groupId: string): ReactNode => {
    const server = byGroup.get(groupId)
    if (!server || server.status !== 'error') return null
    return (
      <Callout variant="error" className="my-[var(--space-2)]">
        <CalloutDescription>{server.status_reason ?? `${server.name} cannot connect.`}</CalloutDescription>
        <CalloutActions>
          <Button size="sm" variant="outline" disabled={disabled || testing === server.id} onClick={() => void test(server)}>Retry</Button>
          <Button size="sm" variant="ghost" disabled={disabled} onClick={() => edit(server)}>Edit</Button>
        </CalloutActions>
      </Callout>
    )
  }

  const addButton = (
    <Button size="sm" variant="outline" disabled={disabled} className="gap-[var(--space-1)] pointer-coarse:min-h-[var(--target-touch-minimum)]" onClick={() => edit(null)}>
      <Plus size={13} aria-hidden="true" />
      Add service
    </Button>
  )

  const dialogs = (
    <>
      <McpServerModal
        open={modalOpen}
        onOpenChange={(open) => {
          setModalOpen(open)
          if (!open) setEditing(null)
        }}
        initialServer={editing ?? undefined}
      />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => { if (!open) setRemoving(null) }}
        title={removing ? `Remove ${removing.name}?` : 'Remove service?'}
        description="Remove this service? Agents that use it lose its tools."
        confirmLabel="Remove"
        destructive
        onConfirm={() => {
          const server = removing
          setRemoving(null)
          if (server) remove.mutate(server)
        }}
      />
    </>
  )

  return { serversQuery, extraGroups, controls, notice, addButton, dialogs }
}
