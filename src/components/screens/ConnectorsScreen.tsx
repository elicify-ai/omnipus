import { useState, useEffect, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Gear, Envelope, Plus, Trash, Warning } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from '@/components/ui/alert-dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { BrandIcon } from '@/components/ui/brand-icon'
import { BrandDisclaimer } from '@/components/ui/brand-disclaimer'
import {
  fetchChannels,
  enableChannel,
  disableChannel,
  createChannelInstance,
  deleteChannelInstance,
  getChannelRouting,
  fetchWorkspaces,
  fetchAgents,
  isApiError,
  EMAIL_CHANNEL_ID,
} from '@/lib/api'
import type { ChannelEntry, ChannelCreateResponse } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { ChannelConfigPanel } from '@/components/skills/ChannelConfigPanel'
import { EmailMailboxPanel } from '@/components/connectors/EmailMailboxPanel'
import { SkeletonList, ErrorState } from '@/components/shared/ListStates'
import { ScreenHeader } from '@/components/layout/ScreenHeader'

// Slug validation: [a-z0-9-]{1,32} lowercase only, as per FR-017
const SLUG_PATTERN = /^[a-z0-9-]{1,32}$/
const SLUG_HINT = '1–32 characters, lowercase letters, numbers, and hyphens only'

const WEBCHAT_ID = 'webchat'

/** Derives the base channel type from a ChannelEntry.
 * For bare-type entries (e.g. "telegram") the id is the type.
 * For namespaced entries (e.g. "whatsapp.eu") the type is the pre-dot segment.
 * The backend also provides `instance_id` which equals the config map key.
 */
function deriveBaseType(channel: ChannelEntry): string {
  // instance_id is the config map key; for bare-type channels id == instance_id == type
  const key = channel.instance_id ?? channel.id
  const dotIdx = key.indexOf('.')
  return dotIdx >= 0 ? key.slice(0, dotIdx) : key
}

// Channel base type -> <BrandIcon> logoSlug (ADR-031 Track 2, US-9). Only the
// following channel marks are vendored: telegram, discord, matrix, whatsapp,
// googlechat, line, wechat, tencentqq (src/assets/brand-logos/c_*.svg). Any
// other base type (slack, feishu, dingtalk, wecom, irc) has no vendored asset
// and falls back to <BrandIcon>'s lettermark automatically — never throws.
function channelSlug(baseType: string): string {
  switch (baseType) {
    case 'google-chat':
      return 'googlechat'
    case 'weixin':
      return 'wechat'
    case 'qq':
      return 'tencentqq'
    default:
      return baseType
  }
}

// ── Channel instance row (US-9 AS-2: binding-first title) ────────────────────

interface ChannelInstanceRowProps {
  channel: ChannelEntry
  workspaceNameById: Map<string, string>
  agentNameById: Map<string, string>
  onConfigure: () => void
  onToggle: () => void
  onDelete: () => void
}

function ChannelInstanceRow({
  channel,
  workspaceNameById,
  agentNameById,
  onConfigure,
  onToggle,
  onDelete,
}: ChannelInstanceRowProps) {
  const instanceId = channel.instance_id ?? channel.id

  // Resolve the ADR-029 workspace→agent binding via the same routing endpoint
  // ChannelConfigPanel uses (shared TanStack Query cache key). A fetch failure
  // must never crash the row — it degrades to the "No workspace bound" fallback.
  const { data: routing, isLoading: routingLoading } = useQuery({
    queryKey: ['channel-routing', instanceId],
    queryFn: () => getChannelRouting(instanceId),
    retry: false,
  })

  const workspaceId = routing?.workspace_id
  const agentId = routing?.default_agent_id
  // Fall back to the raw id when the name lookup misses (e.g. an archived
  // workspace absent from the resolved list, or a deleted agent) — never hide
  // the binding just because a name couldn't be resolved.
  const workspaceName = workspaceId ? workspaceNameById.get(workspaceId) ?? workspaceId : undefined
  const agentName = agentId ? agentNameById.get(agentId) ?? agentId : undefined

  const isDegraded = channel.degraded === true
  const connectionStatus = isDegraded ? 'degraded' : channel.enabled ? 'enabled' : 'unconfigured'
  const statusBadge: Record<'degraded' | 'enabled' | 'unconfigured', { variant: 'error' | 'success' | 'muted'; label: string }> = {
    degraded:     { variant: 'error',   label: 'Failed to start' },
    enabled:      { variant: 'success', label: 'Enabled' },
    unconfigured: { variant: 'muted',   label: 'Not configured' },
  }

  return (
    <div
      data-testid={`channel-card-${instanceId}`}
      className="flex items-center gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
    >
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          {routingLoading ? (
            <span className="text-sm text-[var(--color-muted)]" data-testid={`channel-binding-${instanceId}`}>
              Loading binding…
            </span>
          ) : workspaceName ? (
            <span
              className="font-medium text-sm text-[var(--color-secondary)]"
              data-testid={`channel-binding-${instanceId}`}
            >
              {workspaceName} → {agentName ?? 'unassigned agent'}
            </span>
          ) : (
            <span
              className="font-medium text-sm text-[var(--color-muted)] italic"
              data-testid={`channel-binding-${instanceId}`}
            >
              No workspace bound
            </span>
          )}
          <Badge variant="outline" className="text-[10px] font-mono">
            {instanceId}
          </Badge>
          <Badge
            variant={statusBadge[connectionStatus].variant}
            className="text-[10px]"
          >
            {statusBadge[connectionStatus].label}
          </Badge>
        </div>
        {isDegraded && channel.degraded_reason && (
          <p className="mt-0.5 text-[10px] text-[var(--color-muted)] truncate max-w-xs">
            {channel.degraded_reason}
          </p>
        )}
      </div>
      <div className="flex items-center gap-2 shrink-0">
        <button
          type="button"
          onClick={onConfigure}
          className="flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors font-medium"
          aria-label={`Configure ${instanceId}`}
        >
          <Gear size={13} />
          Configure
        </button>
        <button
          type="button"
          onClick={onToggle}
          className="text-xs text-[var(--color-accent)] hover:text-[var(--color-accent-hover)] transition-colors font-medium"
        >
          {channel.enabled ? 'Disable' : 'Enable'}
        </button>
        <button
          type="button"
          onClick={onDelete}
          className="flex items-center gap-0.5 text-xs text-[var(--color-muted)] hover:text-red-400 transition-colors"
          aria-label={`Delete ${instanceId} instance`}
          data-testid={`channel-delete-btn-${instanceId}`}
        >
          <Trash size={13} />
        </button>
      </div>
    </div>
  )
}

// ── Channel type group (US-9 AS-1: type-grouped rows) ─────────────────────────

interface ChannelTypeGroupProps {
  baseType: string
  displayName: string
  instances: ChannelEntry[]
  workspaceNameById: Map<string, string>
  agentNameById: Map<string, string>
  onConfigure: (channel: ChannelEntry) => void
  onToggle: (channel: ChannelEntry) => void
  onDelete: (channel: ChannelEntry) => void
  onAddAnother: (baseType: string) => void
}

function ChannelTypeGroup({
  baseType,
  displayName,
  instances,
  workspaceNameById,
  agentNameById,
  onConfigure,
  onToggle,
  onDelete,
  onAddAnother,
}: ChannelTypeGroupProps) {
  return (
    <div data-testid={`channel-type-group-${baseType}`}>
      <div className="flex items-center justify-between gap-2 mb-2">
        <div className="flex items-center gap-2">
          <BrandIcon slug={channelSlug(baseType)} size={18} label={displayName} />
          <h3 className="text-sm font-semibold text-[var(--color-secondary)]">{displayName}</h3>
        </div>
        <button
          type="button"
          onClick={() => onAddAnother(baseType)}
          data-testid={`channel-type-add-another-${baseType}`}
          className="flex items-center gap-1 text-xs text-[var(--color-accent)] hover:text-[var(--color-accent)]/80 transition-colors font-medium"
        >
          <Plus size={12} />
          Add another…
        </button>
      </div>
      <div className="space-y-2">
        {instances.map((channel) => {
          const instanceId = channel.instance_id ?? channel.id
          return (
            <ChannelInstanceRow
              key={instanceId}
              channel={channel}
              workspaceNameById={workspaceNameById}
              agentNameById={agentNameById}
              onConfigure={() => onConfigure(channel)}
              onToggle={() => onToggle(channel)}
              onDelete={() => onDelete(channel)}
            />
          )
        })}
      </div>
    </div>
  )
}

// ── Empty-state roster (US-9 AS-3) ────────────────────────────────────────────

interface ChannelRosterProps {
  types: ChannelEntry[]
  onConnect: (baseType: string) => void
}

function ChannelRoster({ types, onConnect }: ChannelRosterProps) {
  return (
    <div data-testid="channel-roster" className="space-y-4">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
        {types.map((channel) => {
          const baseType = deriveBaseType(channel)
          return (
            <button
              key={baseType}
              type="button"
              onClick={() => onConnect(baseType)}
              data-testid={`channel-roster-connect-${baseType}`}
              className="flex items-center gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] hover:border-[var(--color-accent)]/50 transition-colors text-left"
            >
              <BrandIcon slug={channelSlug(baseType)} size={22} label={channel.name} />
              <span className="flex-1 min-w-0 font-medium text-sm text-[var(--color-secondary)]">
                {channel.name}
              </span>
              <span className="text-xs text-[var(--color-accent)] font-medium shrink-0">Connect</span>
            </button>
          )
        })}
      </div>
      <BrandDisclaimer />
    </div>
  )
}

// ── Create-channel Sheet (US-10 — replaces the modal AddInstanceDialog) ──────

interface CreateChannelSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Pre-selected + locked channel type when opened from a group's "Add another…" or a roster "Connect". */
  defaultType?: string
  /** List of known base channel types for the type selector (global "+ Add channel" entry point). */
  knownTypes: string[]
  typeDisplayName: Map<string, string>
  onCreated: (created: ChannelCreateResponse) => void
}

function CreateChannelSheet({
  open,
  onOpenChange,
  defaultType,
  knownTypes,
  typeDisplayName,
  onCreated,
}: CreateChannelSheetProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [selectedType, setSelectedType] = useState(defaultType ?? '')
  const [slug, setSlug] = useState('')
  const [slugError, setSlugError] = useState<string | null>(null)
  const [serverError, setServerError] = useState<string | null>(null)

  // Re-sync the locked/pre-filled type whenever the sheet is (re)opened for a
  // different entry point (a different group's "Add another…", a roster
  // "Connect", or the global "+ Add channel" which passes no defaultType).
  useEffect(() => {
    if (open) setSelectedType(defaultType ?? '')
  }, [open, defaultType])

  const slugValid = SLUG_PATTERN.test(slug)
  const canSubmit = selectedType !== '' && slugValid

  const { mutate: doCreate, isPending } = useMutation({
    mutationFn: () => createChannelInstance({ type: selectedType, slug }),
    onSuccess: (created) => {
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      addToast({ message: `Channel instance "${created.id}" created`, variant: 'success' })
      setSlug('')
      setSlugError(null)
      setServerError(null)
      onOpenChange(false)
      onCreated(created)
    },
    onError: (err: Error) => {
      if (isApiError(err) && err.status === 409) {
        setServerError(`An instance with that id already exists: "${selectedType}.${slug}"`)
      } else if (isApiError(err)) {
        setServerError(err.userMessage)
      } else {
        setServerError(err.message)
      }
    },
  })

  function handleSlugChange(value: string) {
    setSlug(value)
    setServerError(null)
    if (value === '') {
      setSlugError(null)
    } else if (!SLUG_PATTERN.test(value)) {
      setSlugError(SLUG_HINT)
    } else {
      setSlugError(null)
    }
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    setServerError(null)
    doCreate()
  }

  function handleOpenChange(nextOpen: boolean) {
    if (!nextOpen) {
      setSlug('')
      setSlugError(null)
      setServerError(null)
    }
    onOpenChange(nextOpen)
  }

  const lockedTypeName = defaultType ? typeDisplayName.get(defaultType) ?? defaultType : null

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent
        side="right"
        className="sm:w-[420px] bg-[var(--color-surface-1)] border-[var(--color-border)] overflow-y-auto"
        data-testid="create-channel-sheet"
      >
        <SheetHeader className="pb-4 border-b border-[var(--color-border)]">
          <SheetTitle className="font-headline text-base font-semibold text-[var(--color-secondary)]">
            Connect a channel
          </SheetTitle>
          <SheetDescription className="text-xs text-[var(--color-muted)] leading-relaxed">
            Create a new instance of a channel type. The instance key will be{' '}
            <span className="font-mono text-xs">&lt;type&gt;.&lt;slug&gt;</span>
            {' '}(e.g. <span className="font-mono text-xs">whatsapp.eu</span>).
          </SheetDescription>
        </SheetHeader>

        <form onSubmit={handleSubmit} className="space-y-4 pt-5">
          {/* Channel type — locked when opened from a group/roster entry point */}
          <div className="space-y-1.5">
            <Label htmlFor="channel-type-select" className="text-xs font-medium text-[var(--color-secondary)]">
              Channel type
            </Label>
            {defaultType ? (
              <div
                className="flex items-center gap-2 p-2.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)]"
                data-testid="create-channel-type-locked"
              >
                <BrandIcon slug={channelSlug(defaultType)} size={18} label={lockedTypeName ?? defaultType} />
                <span className="text-sm text-[var(--color-secondary)]">{lockedTypeName}</span>
              </div>
            ) : (
              <Select
                value={selectedType}
                onValueChange={(v) => { setSelectedType(v); setServerError(null) }}
              >
                <SelectTrigger
                  id="channel-type-select"
                  data-testid="create-channel-type-select"
                  className="w-full text-sm"
                >
                  <SelectValue placeholder="Select a channel type" />
                </SelectTrigger>
                <SelectContent>
                  {knownTypes.map((t) => (
                    <SelectItem key={t} value={t}>
                      {typeDisplayName.get(t) ?? t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          {/* Slug */}
          <div className="space-y-1.5">
            <Label htmlFor="channel-slug-input" className="text-xs font-medium text-[var(--color-secondary)]">
              Slug
            </Label>
            <Input
              id="channel-slug-input"
              data-testid="create-channel-slug-input"
              value={slug}
              onChange={(e) => handleSlugChange(e.target.value)}
              placeholder="e.g. eu"
              className="text-sm font-mono"
              aria-describedby={slugError ? 'slug-error' : 'slug-hint'}
              autoComplete="off"
              spellCheck={false}
            />
            {slugError ? (
              <p id="slug-error" className="text-xs text-red-400" data-testid="create-channel-slug-error">
                {slugError}
              </p>
            ) : (
              <p id="slug-hint" className="text-xs text-[var(--color-muted)]" data-testid="create-channel-slug-hint">
                {SLUG_HINT}
              </p>
            )}
            {selectedType && slug && !slugError && (
              <p className="text-xs text-[var(--color-muted)]">
                Instance key:{' '}
                <span className="font-mono text-[var(--color-accent)]">
                  {selectedType}.{slug}
                </span>
              </p>
            )}
          </div>

          {/* Server error */}
          {serverError && (
            <div
              className="flex items-start gap-2 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2"
              data-testid="create-channel-server-error"
            >
              <Warning size={14} className="text-red-400 mt-0.5 shrink-0" />
              <p className="text-xs text-red-400">{serverError}</p>
            </div>
          )}

          <div className="flex gap-2 pt-2 border-t border-[var(--color-border)]">
            <Button
              type="button"
              variant="outline"
              className="flex-1"
              onClick={() => handleOpenChange(false)}
              disabled={isPending}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              className="flex-1"
              disabled={!canSubmit || isPending}
              data-testid="create-channel-submit-btn"
            >
              {isPending ? 'Creating…' : 'Connect'}
            </Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  )
}

// ── Delete confirmation dialog ────────────────────────────────────────────────

interface DeleteConfirmDialogProps {
  channel: ChannelEntry | null
  onClose: () => void
}

function DeleteConfirmDialog({ channel, onClose }: DeleteConfirmDialogProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const [deleteError, setDeleteError] = useState<string | null>(null)

  const instanceId = channel?.instance_id ?? channel?.id ?? ''
  const isEnabled = channel?.enabled === true

  const { mutate: doDelete, isPending } = useMutation({
    mutationFn: () => deleteChannelInstance(instanceId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      addToast({ message: `Channel instance "${instanceId}" deleted`, variant: 'success' })
      setDeleteError(null)
      onClose()
    },
    onError: (err: Error) => {
      if (isApiError(err) && err.status === 404) {
        // Already gone — refresh the list and close
        queryClient.invalidateQueries({ queryKey: ['channels'] })
        setDeleteError(null)
        onClose()
      } else {
        // Keep the dialog open and show the error inline so a failed
        // destructive delete does not appear to succeed (dialog stays open).
        setDeleteError(isApiError(err) ? err.userMessage : err.message)
      }
    },
  })

  function handleOpenChange(open: boolean) {
    if (!open) {
      setDeleteError(null)
      onClose()
    }
  }

  return (
    <AlertDialog open={channel !== null} onOpenChange={handleOpenChange}>
      <AlertDialogContent data-testid="delete-instance-dialog">
        <AlertDialogHeader>
          <AlertDialogTitle className="font-headline text-[var(--color-secondary)]">
            Delete channel instance
          </AlertDialogTitle>
          <AlertDialogDescription className="text-sm text-[var(--color-muted)]">
            This will permanently remove{' '}
            <span className="font-mono text-xs font-semibold text-[var(--color-secondary)]">
              {instanceId}
            </span>{' '}
            including its configuration, credentials, and per-instance state. This cannot be undone.
          </AlertDialogDescription>
          {isEnabled && (
            <p className="mt-2 text-xs text-amber-400" data-testid="delete-instance-enabled-warning">
              This channel is enabled; deleting will stop it and remove its credentials and state.
            </p>
          )}
        </AlertDialogHeader>

        {/* Inline error — keep dialog open on failure so the user knows the delete did not succeed */}
        {deleteError && (
          <div
            className="flex items-start gap-2 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2"
            data-testid="delete-instance-error"
          >
            <Warning size={14} className="text-red-400 mt-0.5 shrink-0" />
            <p className="text-xs text-red-400">{deleteError}</p>
          </div>
        )}

        <AlertDialogFooter>
          <AlertDialogCancel
            disabled={isPending}
            onClick={() => { setDeleteError(null); onClose() }}
            data-testid="delete-instance-cancel-btn"
          >
            Cancel
          </AlertDialogCancel>
          <AlertDialogAction
            disabled={isPending}
            onClick={() => { setDeleteError(null); doDelete() }}
            className="bg-red-600 hover:bg-red-700 text-white"
            data-testid="delete-instance-confirm-btn"
          >
            {isPending ? 'Deleting…' : 'Delete'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// ── ConnectorsScreen ──────────────────────────────────────────────────────────

export function ConnectorsScreen() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [configuringChannel, setConfiguringChannel] = useState<{ id: string; name: string; nativeAvailable?: boolean; enabled?: boolean } | null>(null)
  const [mailboxPanelOpen, setMailboxPanelOpen] = useState(false)

  // Create-channel Sheet state (US-10)
  const [createChannelOpen, setCreateChannelOpen] = useState(false)
  const [createChannelDefaultType, setCreateChannelDefaultType] = useState<string | undefined>(undefined)

  // Delete confirmation state
  const [channelToDelete, setChannelToDelete] = useState<ChannelEntry | null>(null)

  const { data: allChannels = [], isLoading, isError } = useQuery({
    queryKey: ['channels'],
    queryFn: fetchChannels,
  })

  // Split channels: email is surfaced separately as a "mailbox account" (M11 decision —
  // email is a tool, not a conversational channel), and webchat is a singleton
  // built-in with no ADR-029 multi-instance/binding concept — both are excluded
  // from the type-grouped instance list so they don't appear twice or confuse
  // the "N instances per type" model.
  const channels = allChannels.filter((c) => c.id !== EMAIL_CHANNEL_ID && c.id !== WEBCHAT_ID)
  const emailChannel = allChannels.find((c) => c.id === EMAIL_CHANNEL_ID)
  const webchatChannel = allChannels.find((c) => c.id === WEBCHAT_ID)

  // "Configured" (FR-008) = has a persisted instance — the backend only sets
  // instance_id on entries backed by a real config.Channels[] map key; the
  // static "available but unconfigured" placeholder rows omit it.
  const configuredInstances = channels.filter((c) => c.instance_id !== undefined)
  const unconfiguredChannels = channels.filter((c) => c.instance_id === undefined)

  // Derive the set of known base types (both configured and connectable) for
  // the create-Sheet's type picker.
  const knownBaseTypes = Array.from(new Set(channels.map(deriveBaseType))).sort()

  // baseType -> human display name (identical across every instance of a type).
  const typeDisplayName = useMemo(() => {
    const map = new Map<string, string>()
    for (const c of channels) {
      const baseType = deriveBaseType(c)
      if (!map.has(baseType)) map.set(baseType, c.name)
    }
    return map
  }, [channels])

  // baseType -> native_available (WhatsApp-only capability flag) so a newly
  // created instance's Configure sheet knows whether native pairing is offered.
  const nativeAvailableByType = useMemo(() => {
    const map = new Map<string, boolean | undefined>()
    for (const c of channels) {
      if (c.native_available !== undefined) map.set(deriveBaseType(c), c.native_available)
    }
    return map
  }, [channels])

  const groupedInstances = useMemo(() => {
    const map = new Map<string, ChannelEntry[]>()
    for (const c of channels) {
      if (c.instance_id === undefined) continue
      const baseType = deriveBaseType(c)
      const arr = map.get(baseType) ?? []
      arr.push(c)
      map.set(baseType, arr)
    }
    return map
  }, [channels])

  const groupedTypeOrder = useMemo(() => Array.from(groupedInstances.keys()).sort(), [groupedInstances])

  // Workspace + agent name-resolution lists — same query keys ChannelConfigPanel
  // uses (agents) so the TanStack Query cache is shared; workspaces are fetched
  // with status "all" (not "active") so an archived-but-bound workspace still
  // resolves to a name instead of falling back to its raw id.
  const { data: workspaces = [] } = useQuery({
    queryKey: ['workspaces', { status: 'all' }],
    queryFn: () => fetchWorkspaces({ status: 'all' }),
    enabled: configuredInstances.length > 0,
  })
  const { data: agentsList = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: configuredInstances.length > 0,
  })
  const workspaceNameById = useMemo(() => new Map(workspaces.map((w) => [w.id, w.name])), [workspaces])
  const agentNameById = useMemo(() => new Map(agentsList.map((a) => [a.id, a.name])), [agentsList])

  const { mutate: doToggleChannel } = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      enabled ? disableChannel(id) : enableChannel(id),
    onSuccess: (_, { enabled }) => {
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      addToast({ message: enabled ? 'Channel disabled' : 'Channel enabled', variant: 'success' })
    },
    onError: (err: Error) =>
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' }),
  })

  function openCreateFor(baseType?: string) {
    setCreateChannelDefaultType(baseType)
    setCreateChannelOpen(true)
  }

  function handleCreated(created: ChannelCreateResponse) {
    // Immediately open Configure on the new instance so the operator sets its
    // workspace→agent binding without an extra click (US-10 AS-2).
    setConfiguringChannel({
      id: created.id,
      name: typeDisplayName.get(created.type) ?? created.type,
      nativeAvailable: nativeAvailableByType.get(created.type),
      enabled: created.enabled,
    })
  }

  return (
    <div className="absolute inset-0 flex flex-col">
      <ScreenHeader title="Connectors" />
      <div className="flex-1 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <div className="max-w-4xl mx-auto px-4 py-6">
        {/* Header */}
        <div className="mb-6 flex items-start justify-between gap-4">
          <div>
            <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Connectors</h1>
            <p className="text-sm text-[var(--color-muted)] mt-0.5">
              Connect Telegram, Discord, Slack and more, and choose which agent answers each.
            </p>
          </div>
          {/* Global create action (shown when the channel list is loaded) */}
          {!isLoading && !isError && knownBaseTypes.length > 0 && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="shrink-0 gap-1.5"
              onClick={() => openCreateFor(undefined)}
              data-testid="add-channel-instance-btn"
              aria-label="Add channel"
            >
              <Plus size={14} />
              Add channel
            </Button>
          )}
        </div>

        {/* ── Email Mailbox Account (M11 — email is a tool, not a channel) ── */}
        <div className="mb-8">
          <div className="flex items-center gap-2 mb-3">
            <h2 className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wider">
              Email
            </h2>
          </div>
          <div
            data-testid="email-mailbox-card"
            className="flex items-center gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
          >
            <Envelope size={20} weight="duotone" className="text-[var(--color-accent)] shrink-0" />
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="font-medium text-sm text-[var(--color-secondary)]">
                  Email Mailbox
                </span>
                <Badge variant="outline" className="text-[10px] font-mono">
                  imap + smtp
                </Badge>
                {emailChannel?.enabled ? (
                  <Badge variant="success" className="text-[10px]">
                    Active
                  </Badge>
                ) : (
                  <Badge variant="muted" className="text-[10px]">
                    Not configured
                  </Badge>
                )}
              </div>
              <p className="mt-0.5 text-[10px] text-[var(--color-muted)]">
                The agent reads its inbox on heartbeat. Unhandled mail becomes Board tasks.
              </p>
            </div>
            <div className="flex items-center gap-2 shrink-0">
              <button
                type="button"
                onClick={() => setMailboxPanelOpen(true)}
                className="flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors font-medium"
                aria-label="Configure email mailbox account"
                data-testid="email-mailbox-configure-btn"
              >
                <Gear size={13} />
                Configure
              </button>
            </div>
          </div>
        </div>

        {/* ── Web Chat (built-in, singleton, always on — no ADR-029 binding) ── */}
        {webchatChannel && (
          <div className="mb-8">
            <div className="flex items-center gap-2 mb-3">
              <h2 className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wider">
                Built-in
              </h2>
            </div>
            <div
              data-testid={`channel-card-${webchatChannel.id}`}
              className="flex items-center gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
            >
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="font-medium text-sm text-[var(--color-secondary)]">
                    {webchatChannel.name}
                  </span>
                  <Badge variant="success" className="text-[10px]">
                    Always on
                  </Badge>
                </div>
                <p className="mt-0.5 text-[10px] text-[var(--color-muted)]">
                  Built into every Omnipus install. No configuration needed.
                </p>
              </div>
            </div>
          </div>
        )}

        {/* ── Channels (conversational, type-grouped instances) ── */}
        <div>
          <div className="flex items-center gap-2 mb-3">
            <h2 className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wider">
              Channels
            </h2>
          </div>

          {isError ? (
            <ErrorState message="Could not load channels." />
          ) : isLoading ? (
            <SkeletonList />
          ) : configuredInstances.length === 0 ? (
            <ChannelRoster types={unconfiguredChannels} onConnect={openCreateFor} />
          ) : (
            <div className="space-y-6">
              {groupedTypeOrder.map((baseType) => (
                <ChannelTypeGroup
                  key={baseType}
                  baseType={baseType}
                  displayName={typeDisplayName.get(baseType) ?? baseType}
                  instances={groupedInstances.get(baseType) ?? []}
                  workspaceNameById={workspaceNameById}
                  agentNameById={agentNameById}
                  onConfigure={(channel) =>
                    setConfiguringChannel({
                      id: channel.instance_id ?? channel.id,
                      name: channel.name,
                      nativeAvailable: channel.native_available,
                      enabled: channel.enabled,
                    })
                  }
                  onToggle={(channel) =>
                    doToggleChannel({ id: channel.instance_id ?? channel.id, enabled: channel.enabled })
                  }
                  onDelete={(channel) => setChannelToDelete(channel)}
                  onAddAnother={openCreateFor}
                />
              ))}
              <BrandDisclaimer />
            </div>
          )}
        </div>

        {/* Channel config slide-over */}
        {configuringChannel && (
          <ChannelConfigPanel
            channelId={configuringChannel.id}
            channelName={configuringChannel.name}
            nativeAvailable={configuringChannel.nativeAvailable}
            enabled={configuringChannel.enabled}
            open={true}
            onOpenChange={(open) => {
              if (!open) setConfiguringChannel(null)
            }}
          />
        )}

        {/* Email mailbox account config slide-over */}
        <EmailMailboxPanel
          open={mailboxPanelOpen}
          onOpenChange={setMailboxPanelOpen}
        />

        {/* Create-channel Sheet (US-10) */}
        <CreateChannelSheet
          open={createChannelOpen}
          onOpenChange={setCreateChannelOpen}
          defaultType={createChannelDefaultType}
          knownTypes={knownBaseTypes}
          typeDisplayName={typeDisplayName}
          onCreated={handleCreated}
        />

        {/* Delete confirmation dialog */}
        <DeleteConfirmDialog
          channel={channelToDelete}
          onClose={() => setChannelToDelete(null)}
        />
      </div>
      </div>
    </div>
  )
}
