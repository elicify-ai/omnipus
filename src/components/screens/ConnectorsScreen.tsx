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
import { channelSlug } from '@/components/ui/channel-logo'
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
import { logError } from '@/lib/telemetry'

// Slug validation: [a-z0-9-]{1,32} lowercase only, as per FR-017
const SLUG_PATTERN = /^[a-z0-9-]{1,32}$/
const SLUG_HINT = '1–32 characters, lowercase letters, numbers, and hyphens only'

const WEBCHAT_ID = 'webchat'

/**
 * Derives the base channel type from a ChannelEntry. For bare-type entries
 * (e.g. "telegram") the id is the type. For namespaced entries (e.g.
 * "whatsapp.eu") the type is the pre-dot segment. `instance_id` (when
 * present) equals the config map key and is preferred over `id`, since the
 * backend's static "available but unconfigured" placeholder rows only set `id`.
 */
function deriveBaseType(channel: ChannelEntry): string {
  const key = channel.instance_id ?? channel.id
  const dotIdx = key.indexOf('.')
  return dotIdx >= 0 ? key.slice(0, dotIdx) : key
}

// ── Configured-instance typing (A6) ───────────────────────────────────────────
//
// A ChannelEntry is "configured" (FR-008) iff the backend set `instance_id`
// (only done for entries backed by a real config.Channels[] map key) AND it is
// not a DefaultConfig template stub. DefaultConfig seeds a DISABLED, UNBOUND
// stub for every base type under its bare-type key (telegram, discord, … — 12
// of 13 types), so `instance_id !== undefined` alone misclassifies a fresh
// install as "12 configured channels" and the roster never renders (the
// channels twin of the provider template bug, found in live UAT against real
// backend data). A bare-key entry that is neither enabled nor workspace-bound
// is a template — "available", not configured; the moment the operator enables
// it, binds it, or creates a namespaced <type>.<slug> instance it counts as
// real. Known trade-off: a token saved on a bare stub left disabled AND
// unbound keeps the type in the roster — acceptable, since Save & Enable is
// the configure flow's primary action.

type ConfiguredChannelEntry = ChannelEntry & { instance_id: string }

function isTemplateStub(channel: ChannelEntry): boolean {
  // Bare-type key = no "." (ADR-029 instance grammar is <type>.<slug>; only
  // DefaultConfig and legacy single-instance configs use bare keys). A
  // namespaced instance is always operator-created, so it is never a template
  // even while disabled and unbound.
  return (
    channel.instance_id !== undefined &&
    !channel.instance_id.includes('.') &&
    !channel.enabled &&
    channel.identity === undefined
  )
}

function isConfigured(channel: ChannelEntry): channel is ConfiguredChannelEntry {
  return channel.instance_id !== undefined && !isTemplateStub(channel)
}

/** "Available but unconfigured" — the static placeholder rows (no instance_id)
 *  plus the DefaultConfig bare-type template stubs (see isTemplateStub). */
type UnconfiguredChannelEntry = ChannelEntry

function isUnconfigured(channel: ChannelEntry): channel is UnconfiguredChannelEntry {
  return channel.instance_id === undefined || isTemplateStub(channel)
}

// ── Channel instance row (US-9 AS-2: binding-first title) ────────────────────

// Hoisted to module scope (A10) — a fixed lookup table, not per-render state.
type ConnectionStatus = 'degraded' | 'enabled' | 'unconfigured'

const STATUS_BADGE: Record<ConnectionStatus, { variant: 'error' | 'success' | 'muted'; label: string }> = {
  degraded:     { variant: 'error',   label: 'Failed to start' },
  enabled:      { variant: 'success', label: 'Enabled' },
  unconfigured: { variant: 'muted',   label: 'Not configured' },
}

function connectionStatusOf(channel: ChannelEntry): ConnectionStatus {
  if (channel.degraded === true) return 'degraded'
  return channel.enabled ? 'enabled' : 'unconfigured'
}

interface ChannelInstanceRowProps {
  channel: ConfiguredChannelEntry
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
  const instanceId = channel.instance_id

  // Resolve the ADR-029 workspace→agent binding via the same routing endpoint
  // ChannelConfigPanel uses (shared TanStack Query cache key). A fetch failure
  // must never crash the row — it renders a distinct error state (A1) instead
  // of masquerading as "No workspace bound" (those are different facts: one
  // means "we don't know", the other means "we know there's nothing").
  const { data: routing, isLoading: routingLoading, isError: routingIsError } = useQuery({
    queryKey: ['channel-routing', instanceId],
    queryFn: () => getChannelRouting(instanceId),
    retry: false,
  })

  useEffect(() => {
    if (routingIsError) {
      logError({ event: 'channelRoutingFetchFailed', instanceId })
    }
  }, [routingIsError, instanceId])

  const workspaceId = routing?.workspace_id
  const agentId = routing?.default_agent_id
  // Fall back to the raw id when the name lookup misses (e.g. an archived
  // workspace absent from the resolved list, or a deleted agent) — never hide
  // the binding just because a name couldn't be resolved.
  const workspaceName = workspaceId ? workspaceNameById.get(workspaceId) ?? workspaceId : undefined
  const agentName = agentId ? agentNameById.get(agentId) ?? agentId : undefined

  const connectionStatus = connectionStatusOf(channel)
  const isDegraded = connectionStatus === 'degraded'

  return (
    <div
      data-testid={`channel-card-${instanceId}`}
      className="flex items-center gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
    >
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          {routingIsError ? (
            <span
              className="font-medium text-sm text-[var(--color-error)]"
              data-testid={`channel-binding-${instanceId}`}
            >
              Couldn&apos;t load binding
            </span>
          ) : routingLoading ? (
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
            variant={STATUS_BADGE[connectionStatus].variant}
            className="text-[10px]"
          >
            {STATUS_BADGE[connectionStatus].label}
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
          aria-label={`${channel.enabled ? 'Disable' : 'Enable'} ${instanceId}`}
          data-testid={`channel-toggle-${instanceId}`}
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
  instances: ConfiguredChannelEntry[]
  workspaceNameById: Map<string, string>
  agentNameById: Map<string, string>
  onConfigure: (channel: ConfiguredChannelEntry) => void
  onToggle: (channel: ConfiguredChannelEntry) => void
  onDelete: (channel: ConfiguredChannelEntry) => void
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
          const instanceId = channel.instance_id
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
  types: UnconfiguredChannelEntry[]
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

// Discriminated selection state (A7): either the sheet was opened from a
// group's "Add another…"/a roster "Connect" (type locked, no picker), or from
// the global "+ Add channel" entry point (type open for picking from the
// known roster). Replaces the old `defaultType?: string` + `knownTypes`
// pairing, which let a caller pass both/neither with no compile-time check
// that they were used consistently.
type CreateChannelSelection =
  | { mode: 'locked'; baseType: string }
  | { mode: 'pickable'; knownTypes: string[] }

interface CreateChannelSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  selection: CreateChannelSelection
  typeDisplayName: Map<string, string>
  onCreated: (created: ChannelCreateResponse) => void
}

function CreateChannelSheet({
  open,
  onOpenChange,
  selection,
  typeDisplayName,
  onCreated,
}: CreateChannelSheetProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const lockedType = selection.mode === 'locked' ? selection.baseType : undefined

  // No `''` sentinel (A7) — "nothing chosen yet" is represented by `undefined`.
  const [selectedType, setSelectedType] = useState<string | undefined>(lockedType)
  const [slug, setSlug] = useState('')
  const [serverError, setServerError] = useState<string | null>(null)

  // Re-sync the locked/pre-filled type whenever the sheet is (re)opened for a
  // different entry point (a different group's "Add another…", a roster
  // "Connect", or the global "+ Add channel" which opens in pickable mode).
  useEffect(() => {
    if (open) setSelectedType(lockedType)
  }, [open, lockedType])

  const slugValid = SLUG_PATTERN.test(slug)
  // Derived, not stored (A9) — slug validity is a pure function of `slug`,
  // so a parallel `slugError` useState could only ever drift out of sync.
  const slugError = slug !== '' && !slugValid ? SLUG_HINT : null
  const canSubmit = selectedType !== undefined && slugValid

  const { mutate: doCreate, isPending } = useMutation({
    mutationFn: () => createChannelInstance({ type: selectedType ?? '', slug }),
    onSuccess: (created) => {
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      addToast({ message: `Channel instance "${created.id}" created`, variant: 'success' })
      setSlug('')
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
        // Never surface a raw Error.message (e.g. an ApiSchemaError's
        // internals — zod issue paths, raw response body) to the user (A5).
        setServerError('Unexpected response from the server. Please refresh and try again.')
        logError({ event: 'createChannelInstanceFailed', message: err.message })
      }
    },
  })

  function handleSlugChange(value: string) {
    setSlug(value)
    setServerError(null)
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit || isPending) return
    setServerError(null)
    doCreate()
  }

  function handleOpenChange(nextOpen: boolean) {
    if (!nextOpen) {
      setSlug('')
      setServerError(null)
    }
    onOpenChange(nextOpen)
  }

  const lockedTypeName = lockedType ? typeDisplayName.get(lockedType) ?? lockedType : null

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
            {selection.mode === 'locked' ? (
              <div
                className="flex items-center gap-2 p-2.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)]"
                data-testid="create-channel-type-locked"
              >
                <BrandIcon slug={channelSlug(selection.baseType)} size={18} label={lockedTypeName ?? selection.baseType} />
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
                  {selection.knownTypes.map((t) => (
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
  channel: ConfiguredChannelEntry | null
  onClose: () => void
}

function DeleteConfirmDialog({ channel, onClose }: DeleteConfirmDialogProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const [deleteError, setDeleteError] = useState<string | null>(null)

  const instanceId = channel?.instance_id ?? ''
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
  const [createChannelBaseType, setCreateChannelBaseType] = useState<string | undefined>(undefined)

  // Delete confirmation state
  const [channelToDelete, setChannelToDelete] = useState<ConfiguredChannelEntry | null>(null)

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
  const configuredInstances = channels.filter(isConfigured)
  const unconfiguredChannels = channels.filter(isUnconfigured)

  // Derive the set of known base types (both configured and connectable) for
  // the create-Sheet's type picker.
  const knownBaseTypes = Array.from(new Set(channels.map(deriveBaseType))).sort()

  // Single pass over `channels` (A12) — baseType -> display name, baseType ->
  // native_available (WhatsApp-only capability flag), and baseType -> its
  // configured instances all fall out of one iteration instead of three
  // separate O(n) useMemos with an identical [channels] dependency.
  const { typeDisplayName, nativeAvailableByType, groupedInstances } = useMemo(() => {
    const typeDisplayName = new Map<string, string>()
    const nativeAvailableByType = new Map<string, boolean | undefined>()
    const groupedInstances = new Map<string, ConfiguredChannelEntry[]>()
    for (const c of channels) {
      const baseType = deriveBaseType(c)
      if (!typeDisplayName.has(baseType)) typeDisplayName.set(baseType, c.name)
      if (c.native_available !== undefined) nativeAvailableByType.set(baseType, c.native_available)
      if (isConfigured(c)) {
        const arr = groupedInstances.get(baseType) ?? []
        arr.push(c)
        groupedInstances.set(baseType, arr)
      }
    }
    return { typeDisplayName, nativeAvailableByType, groupedInstances }
  }, [channels])

  const groupedTypeOrder = useMemo(() => Array.from(groupedInstances.keys()).sort(), [groupedInstances])

  // Workspace + agent name-resolution lists — same query keys ChannelConfigPanel
  // uses (agents) so the TanStack Query cache is shared; workspaces are fetched
  // with status "all" (not "active") so an archived-but-bound workspace still
  // resolves to a name instead of falling back to its raw id.
  const { data: workspaces = [], isError: workspacesError } = useQuery({
    queryKey: ['workspaces', { status: 'all' }],
    queryFn: () => fetchWorkspaces({ status: 'all' }),
    enabled: configuredInstances.length > 0,
  })
  const { data: agentsList = [], isError: agentsListError } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: configuredInstances.length > 0,
  })
  const workspaceNameById = useMemo(() => new Map(workspaces.map((w) => [w.id, w.name])), [workspaces])
  const agentNameById = useMemo(() => new Map(agentsList.map((a) => [a.id, a.name])), [agentsList])

  // A3 — a workspaces/agents fetch failure silently degrades every row's
  // binding to raw ids; log it once (per transition) rather than staying quiet.
  useEffect(() => {
    if (workspacesError) logError({ event: 'workspacesFetchFailed' })
  }, [workspacesError])
  useEffect(() => {
    if (agentsListError) logError({ event: 'agentsFetchFailed' })
  }, [agentsListError])

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
    setCreateChannelBaseType(baseType)
    setCreateChannelOpen(true)
  }

  function handleCreated(created: ChannelCreateResponse) {
    // The create flow is a deliberate two-step handoff (US-10 AS-2), not a
    // single combined form: CreateChannelSheet only mints the instance
    // (type + slug); this callback then immediately opens ChannelConfigPanel
    // for it so the operator sets its workspace→agent binding as the very
    // next step, without an extra click to find and open Configure themselves.
    setConfiguringChannel({
      id: created.id,
      name: typeDisplayName.get(created.type) ?? created.type,
      nativeAvailable: nativeAvailableByType.get(created.type),
      enabled: created.enabled,
    })
  }

  const createChannelSelection: CreateChannelSelection = createChannelBaseType
    ? { mode: 'locked', baseType: createChannelBaseType }
    : { mode: 'pickable', knownTypes: knownBaseTypes }

  // A11 — a single if/else render function instead of a 4-way nested ternary.
  // Composes with the outer isError gate (A2): the caller never invokes this
  // while `isError` is true (the whole content area is replaced by one
  // hoisted ErrorState instead), so it only has loading/empty/populated to handle.
  function renderChannelsBody() {
    if (isLoading) return <SkeletonList />
    if (configuredInstances.length === 0) {
      return <ChannelRoster types={unconfiguredChannels} onConnect={openCreateFor} />
    }
    return (
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
                id: channel.instance_id,
                name: channel.name,
                nativeAvailable: channel.native_available,
                enabled: channel.enabled,
              })
            }
            onToggle={(channel) =>
              doToggleChannel({ id: channel.instance_id, enabled: channel.enabled })
            }
            onDelete={(channel) => setChannelToDelete(channel)}
            onAddAnother={openCreateFor}
          />
        ))}
        <BrandDisclaimer />
      </div>
    )
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

        {/* A2 — a channels-list fetch failure must not silently degrade the
            Email/Web Chat sections (both read from the same `allChannels`
            query) into misleading "Not configured" / dropped states. One
            hoisted error banner replaces all three data sections; the panels
            and dialogs below stay mounted since they're independent overlays,
            not part of the list. */}
        {isError ? (
          <ErrorState message="Could not load channels." />
        ) : (
          <>
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

              {/* A3 — a workspaces/agents fetch failure silently degrades every
                  row's binding to raw ids; surface it once, near the heading. */}
              {(workspacesError || agentsListError) && (
                <p
                  className="text-xs text-[var(--color-error)] mb-2"
                  data-testid="channel-names-error-notice"
                >
                  Couldn&apos;t load workspace/agent names — showing raw IDs
                </p>
              )}

              {renderChannelsBody()}
            </div>
          </>
        )}

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
          selection={createChannelSelection}
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
