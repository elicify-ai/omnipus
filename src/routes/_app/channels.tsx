import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Hash, Gear } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { fetchChannels, enableChannel, disableChannel, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { ChannelConfigPanel } from '@/components/skills/ChannelConfigPanel'
import { SkeletonList, EmptyState, ErrorState } from '@/components/shared/ListStates'

function ChannelsScreen() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [configuringChannel, setConfiguringChannel] = useState<{
    id: string
    name: string
    nativeAvailable?: boolean
  } | null>(null)

  const { data: channels = [], isLoading, isError } = useQuery({
    queryKey: ['channels'],
    queryFn: fetchChannels,
  })

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
    <div className="absolute inset-0 overflow-y-auto">
      <div className="max-w-4xl mx-auto px-4 py-6">
        {/* Header */}
        <div className="mb-6">
          <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Channels</h1>
          <p className="text-sm text-[var(--color-muted)] mt-0.5">
            Connect Telegram, Discord, Slack and more, and choose which agent answers each.
          </p>
        </div>

        {/* Content */}
        {isError ? (
          <ErrorState message="Could not load channels." />
        ) : isLoading ? (
          <SkeletonList />
        ) : channels.length === 0 ? (
          <EmptyState
            icon={<Hash size={40} weight="thin" />}
            message="No channels configured."
          />
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
                    {channel.id !== 'webchat' && (
                      <button
                        type="button"
                        onClick={() =>
                          setConfiguringChannel({
                            id: channel.id,
                            name: channel.name,
                            nativeAvailable: channel.native_available,
                          })
                        }
                        className="flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors font-medium"
                        aria-label={`Configure ${channel.name}`}
                      >
                        <Gear size={13} />
                        Configure
                      </button>
                    )}
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

        {/* Channel config slide-over */}
        {configuringChannel && (
          <ChannelConfigPanel
            channelId={configuringChannel.id}
            channelName={configuringChannel.name}
            nativeAvailable={configuringChannel.nativeAvailable}
            open={true}
            onOpenChange={(open) => {
              if (!open) setConfiguringChannel(null)
            }}
          />
        )}
      </div>
    </div>
  )
}

export const Route = createFileRoute('/_app/channels')({
  component: ChannelsScreen,
})
