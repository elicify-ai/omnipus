import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Hash, Gear, Envelope } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { fetchChannels, enableChannel, disableChannel, isApiError, EMAIL_CHANNEL_ID } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { ChannelConfigPanel } from '@/components/skills/ChannelConfigPanel'
import { EmailMailboxPanel } from '@/components/connectors/EmailMailboxPanel'
import { SkeletonList, ErrorState } from '@/components/shared/ListStates'
import { ScreenHeader } from '@/components/layout/ScreenHeader'

export function ConnectorsScreen() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [configuringChannel, setConfiguringChannel] = useState<{ id: string; name: string; nativeAvailable?: boolean; enabled?: boolean } | null>(null)
  const [mailboxPanelOpen, setMailboxPanelOpen] = useState(false)

  const { data: allChannels = [], isLoading, isError } = useQuery({
    queryKey: ['channels'],
    queryFn: fetchChannels,
  })

  // Split channels: email is surfaced separately as a "mailbox account" (M11 decision —
  // email is a tool, not a conversational channel). The email entry is excluded from the
  // channels list so it doesn't appear twice.
  const channels = allChannels.filter((c) => c.id !== EMAIL_CHANNEL_ID)
  const emailChannel = allChannels.find((c) => c.id === EMAIL_CHANNEL_ID)

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

  return (
    <div className="absolute inset-0 flex flex-col">
      <ScreenHeader title="Connectors" />
      <div className="flex-1 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <div className="max-w-4xl mx-auto px-4 py-6">
        {/* Header */}
        <div className="mb-6">
          <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Connectors</h1>
          <p className="text-sm text-[var(--color-muted)] mt-0.5">
            Connect Telegram, Discord, Slack and more, and choose which agent answers each.
          </p>
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
                const isDegraded = channel.degraded === true
                const connectionStatus = isDegraded ? 'degraded' : channel.enabled ? 'enabled' : 'unconfigured'
                const statusBadge: Record<'degraded' | 'enabled' | 'unconfigured', { variant: 'error' | 'success' | 'muted'; label: string }> = {
                  degraded:     { variant: 'error',   label: 'Failed to start' },
                  enabled:      { variant: 'success', label: 'Enabled' },
                  unconfigured: { variant: 'muted',   label: 'Not configured' },
                }
                return (
                  <div
                    key={channel.id}
                    data-testid={`channel-card-${channel.id}`}
                    className="flex items-center gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
                  >
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-medium text-sm text-[var(--color-secondary)]">
                          {channel.name}
                        </span>
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
                            id: channel.id,
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
                        onClick={() => doToggleChannel({ id: channel.id, enabled: channel.enabled })}
                        className="text-xs text-[var(--color-accent)] hover:text-[var(--color-accent-hover)] transition-colors font-medium"
                      >
                        {channel.enabled ? 'Disable' : 'Enable'}
                      </button>
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
      </div>
      </div>
    </div>
  )
}
