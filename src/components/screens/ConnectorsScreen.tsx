import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Hash, Gear, Envelope, Plus, Trash, Warning } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogDescription,
} from '@/components/ui/dialog'
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
import {
  fetchChannels,
  enableChannel,
  disableChannel,
  createChannelInstance,
  deleteChannelInstance,
  isApiError,
  EMAIL_CHANNEL_ID,
} from '@/lib/api'
import type { ChannelEntry } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { ChannelConfigPanel } from '@/components/skills/ChannelConfigPanel'
import { EmailMailboxPanel } from '@/components/connectors/EmailMailboxPanel'
import { SkeletonList, ErrorState } from '@/components/shared/ListStates'
import { ScreenHeader } from '@/components/layout/ScreenHeader'

// Slug validation: [a-z0-9-]{1,32} lowercase only, as per FR-017
const SLUG_PATTERN = /^[a-z0-9-]{1,32}$/
const SLUG_HINT = '1–32 characters, lowercase letters, numbers, and hyphens only'

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

// ── Add-instance dialog ───────────────────────────────────────────────────────

interface AddInstanceDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Pre-selected channel type when opened from a per-type action. */
  defaultType?: string
  /** List of known base channel types for the type selector. */
  knownTypes: string[]
}

function AddInstanceDialog({ open, onOpenChange, defaultType, knownTypes }: AddInstanceDialogProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [selectedType, setSelectedType] = useState(defaultType ?? '')
  const [slug, setSlug] = useState('')
  const [slugError, setSlugError] = useState<string | null>(null)
  const [serverError, setServerError] = useState<string | null>(null)

  const slugValid = SLUG_PATTERN.test(slug)
  const canSubmit = selectedType !== '' && slugValid

  const { mutate: doCreate, isPending } = useMutation({
    mutationFn: () => createChannelInstance({ type: selectedType, slug }),
    onSuccess: (created) => {
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      addToast({ message: `Channel instance "${created.id}" created`, variant: 'success' })
      onOpenChange(false)
      // Reset internal state for the next open
      setSelectedType(defaultType ?? '')
      setSlug('')
      setSlugError(null)
      setServerError(null)
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
      setSelectedType(defaultType ?? '')
      setSlug('')
      setSlugError(null)
      setServerError(null)
    }
    onOpenChange(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-md" data-testid="add-instance-dialog">
        <DialogHeader>
          <DialogTitle className="font-headline text-[var(--color-secondary)]">
            Add channel instance
          </DialogTitle>
          <DialogDescription className="text-sm text-[var(--color-muted)]">
            Create a new instance of a channel type. The instance key will be{' '}
            <span className="font-mono text-xs">&lt;type&gt;.&lt;slug&gt;</span>
            {' '}(e.g. <span className="font-mono text-xs">whatsapp.eu</span>).
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4 pt-2">
          {/* Channel type */}
          <div className="space-y-1.5">
            <Label htmlFor="channel-type-select" className="text-xs font-medium text-[var(--color-secondary)]">
              Channel type
            </Label>
            <Select
              value={selectedType}
              onValueChange={(v) => { setSelectedType(v); setServerError(null) }}
            >
              <SelectTrigger
                id="channel-type-select"
                data-testid="add-instance-type-select"
                className="w-full text-sm"
              >
                <SelectValue placeholder="Select a channel type" />
              </SelectTrigger>
              <SelectContent>
                {knownTypes.map((t) => (
                  <SelectItem key={t} value={t}>
                    {t}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Slug */}
          <div className="space-y-1.5">
            <Label htmlFor="channel-slug-input" className="text-xs font-medium text-[var(--color-secondary)]">
              Slug
            </Label>
            <Input
              id="channel-slug-input"
              data-testid="add-instance-slug-input"
              value={slug}
              onChange={(e) => handleSlugChange(e.target.value)}
              placeholder="e.g. eu"
              className="text-sm font-mono"
              aria-describedby={slugError ? 'slug-error' : 'slug-hint'}
              autoComplete="off"
              spellCheck={false}
            />
            {slugError ? (
              <p id="slug-error" className="text-xs text-red-400" data-testid="add-instance-slug-error">
                {slugError}
              </p>
            ) : (
              <p id="slug-hint" className="text-xs text-[var(--color-muted)]" data-testid="add-instance-slug-hint">
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
              data-testid="add-instance-server-error"
            >
              <Warning size={14} className="text-red-400 mt-0.5 shrink-0" />
              <p className="text-xs text-red-400">{serverError}</p>
            </div>
          )}

          <DialogFooter className="pt-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => handleOpenChange(false)}
              disabled={isPending}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              size="sm"
              disabled={!canSubmit || isPending}
              data-testid="add-instance-submit-btn"
            >
              {isPending ? 'Creating…' : 'Add instance'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
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

  // Add-instance dialog state
  const [addInstanceOpen, setAddInstanceOpen] = useState(false)
  const [addInstanceDefaultType, setAddInstanceDefaultType] = useState<string | undefined>(undefined)

  // Delete confirmation state
  const [channelToDelete, setChannelToDelete] = useState<ChannelEntry | null>(null)

  const { data: allChannels = [], isLoading, isError } = useQuery({
    queryKey: ['channels'],
    queryFn: fetchChannels,
  })

  // Split channels: email is surfaced separately as a "mailbox account" (M11 decision —
  // email is a tool, not a conversational channel). The email entry is excluded from the
  // channels list so it doesn't appear twice.
  const channels = allChannels.filter((c) => c.id !== EMAIL_CHANNEL_ID)
  const emailChannel = allChannels.find((c) => c.id === EMAIL_CHANNEL_ID)

  // Derive the set of known base types from the channel list for the add-instance type selector.
  // Exclude webchat (not user-configurable) and email (handled via mailbox panel).
  const knownBaseTypes = Array.from(
    new Set(
      allChannels
        .filter((c) => c.id !== EMAIL_CHANNEL_ID && c.id !== 'webchat')
        .map(deriveBaseType),
    ),
  ).sort()

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

  function openAddInstanceFor(baseType?: string) {
    setAddInstanceDefaultType(baseType)
    setAddInstanceOpen(true)
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
          {/* Global add-instance action (shown when channel list is loaded) */}
          {!isLoading && !isError && knownBaseTypes.length > 0 && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="shrink-0 gap-1.5"
              onClick={() => openAddInstanceFor(undefined)}
              data-testid="add-channel-instance-btn"
              aria-label="Add channel instance"
            >
              <Plus size={14} />
              Add instance
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

        {/* ── Channels (conversational) ── */}
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
          ) : channels.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-10 gap-3 text-center">
              <div className="text-[var(--color-border)]">
                <Hash size={36} weight="thin" />
              </div>
              <p className="text-sm text-[var(--color-muted)]">No channels configured.</p>
            </div>
          ) : (
            <div className="space-y-2">
              {channels.map((channel) => {
                const instanceId = channel.instance_id ?? channel.id
                const isDegraded = channel.degraded === true
                const connectionStatus = isDegraded ? 'degraded' : channel.enabled ? 'enabled' : 'unconfigured'
                const statusBadge: Record<'degraded' | 'enabled' | 'unconfigured', { variant: 'error' | 'success' | 'muted'; label: string }> = {
                  degraded:     { variant: 'error',   label: 'Failed to start' },
                  enabled:      { variant: 'success', label: 'Enabled' },
                  unconfigured: { variant: 'muted',   label: 'Not configured' },
                }
                // An instance is namespaced when its id contains a dot
                const isNamespaced = instanceId.includes('.')
                // Only deletable instances can be deleted (not built-in webchat)
                const isDeletable = channel.id !== 'webchat'

                return (
                  <div
                    key={instanceId}
                    data-testid={`channel-card-${instanceId}`}
                    className="flex items-center gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
                  >
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-medium text-sm text-[var(--color-secondary)]">
                          {channel.name}
                        </span>
                        {isNamespaced && (
                          <Badge variant="outline" className="text-[10px] font-mono">
                            {instanceId}
                          </Badge>
                        )}
                        <Badge variant="outline" className="text-[10px] font-mono">
                          {channel.transport}
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
                        onClick={() =>
                          setConfiguringChannel({
                            id: instanceId,
                            name: channel.name,
                            nativeAvailable: channel.native_available,
                            enabled: channel.enabled,
                          })
                        }
                        className="flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors font-medium"
                        aria-label={`Configure ${channel.name}`}
                      >
                        <Gear size={13} />
                        Configure
                      </button>
                      <button
                        type="button"
                        onClick={() => doToggleChannel({ id: instanceId, enabled: channel.enabled })}
                        className="text-xs text-[var(--color-accent)] hover:text-[var(--color-accent-hover)] transition-colors font-medium"
                      >
                        {channel.enabled ? 'Disable' : 'Enable'}
                      </button>
                      {isDeletable && (
                        <button
                          type="button"
                          onClick={() => setChannelToDelete(channel)}
                          className="flex items-center gap-0.5 text-xs text-[var(--color-muted)] hover:text-red-400 transition-colors"
                          aria-label={`Delete ${channel.name} instance`}
                          data-testid={`channel-delete-btn-${instanceId}`}
                        >
                          <Trash size={13} />
                        </button>
                      )}
                    </div>
                  </div>
                )
              })}
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

        {/* Add-instance dialog */}
        <AddInstanceDialog
          open={addInstanceOpen}
          onOpenChange={setAddInstanceOpen}
          defaultType={addInstanceDefaultType}
          knownTypes={knownBaseTypes}
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
