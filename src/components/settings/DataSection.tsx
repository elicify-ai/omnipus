import { useState, useEffect, useRef, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Database, Archive, ArrowCounterClockwise, Trash } from '@phosphor-icons/react'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Separator } from '@/components/ui/separator'
import { fetchConfig, updateConfig, fetchStorageStats, createBackup, fetchBackups, restoreBackup, clearAllSessions, isApiError } from '@/lib/api'
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
  const [restoreTarget, setRestoreTarget] = useState<string | null>(null)

  const { data: config, isLoading: configLoading } = useQuery({
    queryKey: ['config'],
    queryFn: fetchConfig,
  })

  const { data: stats, isLoading: statsLoading } = useQuery({
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

  const { data: backups = [], isLoading: backupsLoading, isError: backupsError } = useQuery({
    queryKey: ['backups'],
    queryFn: fetchBackups,
    retry: false,
  })

  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  const [retentionDays, setRetentionDays] = useState('90')

  useEffect(() => {
    if (!config) return
    if (isDirtyRef.current) return
    setRetentionDays(config.data.session_retention_days.toString())
  }, [config])

  const dataFormData = useMemo(() => ({
    session_retention_days: parseInt(retentionDays, 10) || 90,
  }), [retentionDays])

  const { status: saveStatus, error: saveError } = useAutoSave(
    dataFormData,
    async (data) => {
      await updateConfig({ data })
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['config'] })
    },
    { disabled: !config },
  )

  const { mutate: doBackup, isPending: isCreatingBackup } = useMutation({
    mutationFn: createBackup,
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['backups'] })
      addToast({ message: `Backup created: ${res.path}`, variant: 'success' })
    },
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Backup failed', variant: 'error' }),
  })

  const { mutate: doRestore, isPending: isRestoring } = useMutation({
    mutationFn: (filename: string) => restoreBackup(filename),
    onSuccess: () => {
      addToast({ message: 'Restore complete. Restart gateway to apply.', variant: 'success' })
      setRestoreTarget(null)
    },
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Restore failed', variant: 'error' }),
  })

  const { mutate: doClearSessions, isPending: isClearing } = useMutation({
    mutationFn: clearAllSessions,
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['storage-stats'] })
      if (res.warnings && res.warnings.length > 0) {
        addToast({
          message: `Sessions cleared, but ${res.warnings.length} could not be removed — see logs for details.`,
          variant: 'warning',
        })
      } else {
        addToast({ message: 'All sessions cleared', variant: 'success' })
      }
      setClearConfirmOpen(false)
    },
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Clear failed', variant: 'error' }),
  })

  const isLoading = configLoading || statsLoading

  if (isLoading) return <div className="text-sm text-[var(--color-muted)]">Loading...</div>

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Data & Backup</h2>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">
            Manage session retention, storage, and backups.
          </p>
        </div>
        <AutoSaveIndicator status={saveStatus} error={saveError} />
      </div>

      {/* Storage stats */}
      <section className="space-y-2">
        <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">Storage</h3>
        <div className="grid grid-cols-3 gap-3">
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

      {/* Session retention */}
      <section className="space-y-2">
        <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">Session Retention</h3>
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-[var(--color-secondary)]">Retention period</p>
              <p className="text-xs text-[var(--color-muted)]">Days to keep session transcripts before auto-deletion</p>
            </div>
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min="1"
                max="365"
                value={retentionDays}
                onChange={(e) => { markDirty(); setRetentionDays(e.target.value) }}
                className="w-20 h-8 text-xs font-mono"
              />
              <span className="text-xs text-[var(--color-muted)]">days</span>
            </div>
          </div>
        </div>
      </section>

      <Separator />

      {/* Backup & Restore */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">Backup & Restore</h3>
          <Button
            size="sm"
            variant="outline"
            className="h-7 px-2 gap-1 text-xs"
            onClick={() => doBackup()}
            disabled={isCreatingBackup}
          >
            <Archive size={11} />
            {isCreatingBackup ? 'Creating...' : 'Create backup'}
          </Button>
        </div>

        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] divide-y divide-[var(--color-border)]">
          {backupsLoading && (
            <div className="p-4 text-sm text-[var(--color-muted)]">Loading backups...</div>
          )}
          {backupsError && (
            <div className="p-4 text-sm text-red-400">Failed to load backups. Please try again.</div>
          )}
          {!backupsLoading && !backupsError && backups.length === 0 && (
            <div className="p-4 text-sm text-[var(--color-muted)]">No backups yet.</div>
          )}
          {backups.map((b) => (
            <div key={b.filename} className="flex items-center justify-between px-4 py-2.5">
              <div>
                <p className="text-xs font-mono text-[var(--color-secondary)]">{b.filename}</p>
                <p className="text-[10px] text-[var(--color-muted)]">
                  {formatBytes(b.size_bytes)} &middot; {new Date(b.created_at).toLocaleString()}
                </p>
              </div>
              <Button
                variant="outline"
                size="sm"
                className="h-7 px-2 gap-1 text-xs"
                onClick={() => setRestoreTarget(b.filename)}
              >
                <ArrowCounterClockwise size={11} />
                Restore
              </Button>
            </div>
          ))}
        </div>
      </section>

      {/* Danger zone */}
      <section className="space-y-3">
        <h3 className="text-xs font-semibold text-[var(--color-error)] uppercase tracking-wider">Danger Zone</h3>
        <div className="rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-surface-1)] p-4 flex items-center justify-between">
          <div>
            <p className="text-sm text-[var(--color-secondary)]">Clear all sessions</p>
            <p className="text-xs text-[var(--color-muted)]">Permanently delete all session transcripts. Cannot be undone.</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="h-8 gap-1.5 text-xs text-[var(--color-error)] border-[var(--color-error)]/40 hover:bg-[var(--color-error)]/10"
            onClick={() => setClearConfirmOpen(true)}
          >
            <Trash size={12} />
            Clear sessions
          </Button>
        </div>
      </section>

      {/* Restore confirmation */}
      <Dialog open={!!restoreTarget} onOpenChange={() => setRestoreTarget(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="font-headline text-base">Restore backup?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-[var(--color-muted)] py-2">
            Restore from <span className="font-mono text-[var(--color-secondary)]">{restoreTarget}</span>?
            Current data will be overwritten. Gateway restart required after restore.
          </p>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setRestoreTarget(null)}>Cancel</Button>
            <Button
              size="sm"
              onClick={() => restoreTarget && doRestore(restoreTarget)}
              disabled={isRestoring}
            >
              {isRestoring ? 'Restoring...' : 'Restore'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Clear sessions confirmation */}
      <Dialog open={clearConfirmOpen} onOpenChange={setClearConfirmOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="font-headline text-base">Clear all sessions?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-[var(--color-muted)] py-2">
            This will permanently delete all session transcripts. This action cannot be undone.
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
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3">
      {icon && <div className="text-[var(--color-muted)] mb-1">{icon}</div>}
      <div className="font-headline font-bold text-base text-[var(--color-secondary)]">{value}</div>
      <div className="text-[10px] text-[var(--color-muted)] mt-0.5">{label}</div>
    </div>
  )
}
