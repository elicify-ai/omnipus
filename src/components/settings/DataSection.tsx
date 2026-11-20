import { useState, useEffect, useRef } from 'react'
import { ReadErrorCallout } from './ReadErrorCallout'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Database, Trash } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Separator } from '@/components/ui/separator'
import {
  fetchStorageStats,
  clearAllSessions,
  getErrorMessage,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

export function DataSection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const [clearConfirmOpen, setClearConfirmOpen] = useState(false)

  const {
    data: stats,
    isLoading: statsLoading,
    isError: statsError,
    error: statsReadError,
    refetch: refetchStats,
    isFetching: statsFetching,
  } = useQuery({
    queryKey: ['storage-stats'],
    queryFn: fetchStorageStats,
  })

  // Task 3 fix: fetchStorageStats()'s `warnings` field (non-fatal per-agent
  // collection errors — the stats are still a PARTIAL result) was previously
  // discarded. Apply the same warnings→toast treatment already established
  // in this file for clearAllSessions' onSuccess below. This is a query (not
  // a mutation), so it's driven by an effect with a ref-guard: warnings are
  // re-delivered on every successful refetch (staleTime refresh, window
  // refocus, etc) and we only want to toast once per distinct warning set,
  // not spam the user on every background refetch.
  const lastStorageWarningsRef = useRef<string | null>(null)
  useEffect(() => {
    const warnings = stats?.warnings
    if (!warnings || warnings.length === 0) {
      lastStorageWarningsRef.current = null
      return
    }
    const key = warnings.join('\n')
    if (lastStorageWarningsRef.current === key) return
    lastStorageWarningsRef.current = key
    addToast({
      message: `Storage stats may be incomplete — ${warnings.length} store${warnings.length === 1 ? '' : 's'} could not be read. See logs for details.`,
      variant: 'warning',
    })
  }, [stats?.warnings, addToast])

  const { mutate: doClearSessions, isPending: isClearing } = useMutation({
    mutationFn: clearAllSessions,
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['storage-stats'] })
      if (res.warnings && res.warnings.length > 0) {
        addToast({
          message: `Chat history was cleared, but ${res.warnings.length} could not be removed. The gateway log names them.`,
          variant: 'warning',
        })
      } else {
        addToast({ message: 'All chat history was cleared.', variant: 'success' })
      }
      setClearConfirmOpen(false)
    },
    onError: (err: unknown) => addToast({ message: getErrorMessage(err, 'Clear failed'), variant: 'error' }),
  })

  const isLoading = statsLoading

  if (isLoading) return <div className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Loading storage…</div>

  return (
    <div className="space-y-[var(--space-4)]">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="font-headline font-bold text-base text-[var(--color-secondary)]">Storage</h3>
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
            What Omnipus stores on this computer.
          </p>
        </div>
      </div>

      {/* Storage stats */}
      <section className="space-y-[var(--space-2)]">
        {/* FR-066: the counts show their own error; retention still saves. */}
        {statsError && (
          <ReadErrorCallout
            title="Failed to load the storage counts"
            error={statsReadError}
            onRetry={() => void refetchStats()}
            retrying={statsFetching}
            blocksSaving={false}
            data-testid="storage-stats-load-error"
          />
        )}
        <div className="grid grid-cols-3 gap-[var(--space-2-5)]">
          <StatBox
            icon={<Database size={16} />}
            label="Workspace"
            value={stats ? formatBytes(stats.workspace_size_bytes) : '—'}
          />
          <StatBox
            label="Sessions"
            value={stats?.session_count.toString() ?? '—'}
          />
          <StatBox
            label="Memory entries"
            value={stats?.memory_entry_count.toString() ?? '—'}
          />
        </div>
      </section>

      <Separator />

      {/* Danger zone */}
      <section className="space-y-[var(--space-2-5)]">
        <h3 className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-error)] uppercase tracking-wider">Danger Zone</h3>
        <Card className="border-[var(--color-error)]/30 p-[var(--space-3)] flex items-center justify-between">
          <div>
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Chat history</p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Deletes every chat transcript. This cannot be undone.</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="h-8 gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-error)] border-[var(--color-error)]/40 hover:bg-[var(--color-error)]/10"
            onClick={() => setClearConfirmOpen(true)}
          >
            <Trash size={12} />
            Clear all chat history
          </Button>
        </Card>
      </section>

      {/* Clear sessions confirmation */}
      <Dialog open={clearConfirmOpen} onOpenChange={setClearConfirmOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="font-headline text-base">Clear all chat history?</DialogTitle>
          </DialogHeader>
          <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)] py-[var(--space-2)]">
            Every chat transcript is deleted. This cannot be undone.
          </p>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setClearConfirmOpen(false)}>Cancel</Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => doClearSessions()}
              disabled={isClearing}
            >
              {isClearing ? 'Clearing...' : 'Clear all'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function StatBox({
  label,
  value,
  icon,
}: {
  label: string
  value: string
  icon?: React.ReactNode
}) {
  return (
    <Card className="p-[var(--space-2-5)]">
      {icon && <div className="text-[var(--color-muted)] mb-[var(--space-1)]">{icon}</div>}
      <div className="font-headline font-bold text-base text-[var(--color-secondary)]">{value}</div>
      <div className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">{label}</div>
    </Card>
  )
}
