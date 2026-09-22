import { useState, useEffect, useRef, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Database, Archive, ArrowCounterClockwise, Trash } from '@phosphor-icons/react'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Separator } from '@/components/ui/separator'
import {
  fetchConfig,
  updateConfig,
  fetchStorageStats,
  fetchAppState,
  createBackup,
  fetchBackups,
  restoreBackup,
  clearAllSessions,
  getErrorMessage,
} from '@/lib/api'
import type { AppState } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useStepUp } from './useStepUp'
import { isReAuthCancelled } from './useReAuthGate'

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

// WP4 (ADR-0010): local backup is off in platform mode. `identity.mode` is a
// required field of the generated AppState (WP1); an absent state (query not
// answered yet) reads as not-local, i.e. the section stays hidden — the same
// fail-closed posture a misbuilt edition has on the server.
function isLocalMode(state: AppState | undefined): boolean {
  return state?.identity?.mode === 'local'
}

export function DataSection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const [clearConfirmOpen, setClearConfirmOpen] = useState(false)
  const [restoreTarget, setRestoreTarget] = useState<string | null>(null)
  // Restore overwrites the whole vault, so it takes the step-up gate (ADR-0010
  // WP3): the password prompt in local mode, which is the only mode where the
  // route exists.
  const stepUp = useStepUp()

  const { data: config, isLoading: configLoading } = useQuery({
    queryKey: ['config'],
    queryFn: fetchConfig,
  })

  const { data: stats, isLoading: statsLoading } = useQuery({
    queryKey: ['storage-stats'],
    queryFn: fetchStorageStats,
  })

  // Shared with AppShell/VideoEmbed's ['app-state'] query — same cache entry,
  // so this rarely triggers its own network round trip.
  const { data: appState } = useQuery({
    queryKey: ['app-state'],
    queryFn: fetchAppState,
  })
  const localMode = isLocalMode(appState)

  const { data: backups = [], isLoading: backupsLoading, isError: backupsError } = useQuery({
    queryKey: ['backups'],
    queryFn: fetchBackups,
    enabled: localMode,
    retry: false,
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

  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  const [retentionDays, setRetentionDays] = useState('90')
  // D3 / UAT spurious-PUT fix: reactive readiness flag, distinct from the
  // `!config` check useAutoSave's `disabled` option used to key off of.
  // `config` turns truthy in the SAME commit the hydration effect below is
  // SCHEDULED, but the effect's own `setRetentionDays` call doesn't land
  // until the NEXT commit — so `disabled: !config` flipped false one render
  // too early, letting useAutoSave capture the hardcoded '90' default as
  // its baseline instead of the real persisted value. `retentionHydrated`
  // is set at the END of the hydration effect, so it flips true in the same
  // commit the real value lands.
  const [retentionHydrated, setRetentionHydrated] = useState(false)

  useEffect(() => {
    if (!config) return
    if (isDirtyRef.current) return
    setRetentionDays(config.data.session_retention_days.toString())
    setRetentionHydrated(true)
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
    { disabled: !retentionHydrated },
  )

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
    onError: (err: unknown) => addToast({ message: getErrorMessage(err, 'Clear failed'), variant: 'error' }),
  })

  const { mutate: doBackup, isPending: isCreatingBackup } = useMutation({
    mutationFn: createBackup,
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['backups'] })
      addToast({ message: `Backup created: ${res.path}`, variant: 'success' })
    },
    onError: (err: unknown) => addToast({ message: getErrorMessage(err, 'Backup failed'), variant: 'error' }),
  })

  const { mutateAsync: doRestoreAsync, isPending: isRestoring } = useMutation({
    mutationFn: ({ filename, token }: { filename: string; token?: string }) =>
      token === undefined ? restoreBackup(filename) : restoreBackup(filename, token),
    onSuccess: () => {
      addToast({ message: 'Restore complete. Restart gateway to apply.', variant: 'success' })
      setRestoreTarget(null)
    },
    onError: (err: unknown) => addToast({ message: getErrorMessage(err, 'Restore failed'), variant: 'error' }),
  })

  const isLoading = configLoading || statsLoading

  if (isLoading) return <div className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Loading...</div>

  return (
    <div className="space-y-[var(--space-4)]">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">{localMode ? 'Data & Backup' : 'Data'}</h2>
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
            {localMode ? 'Manage session retention, storage, and backups.' : 'Manage session retention and storage.'}
          </p>
        </div>
        <AutoSaveIndicator status={saveStatus} error={saveError} />
      </div>

      {/* Storage stats */}
      <section className="space-y-[var(--space-2)]">
        <h3 className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider">Storage</h3>
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

      {/* Session retention */}
      <section className="space-y-[var(--space-2)]">
        <h3 className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider">Session Retention</h3>
        <Card className="p-[var(--space-3)]">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Retention period</p>
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Days to keep session transcripts before auto-deletion</p>
            </div>
            <div className="flex items-center gap-[var(--space-2)]">
              <Input
                type="number"
                min="1"
                max="365"
                value={retentionDays}
                onChange={(e) => { markDirty(); setRetentionDays(e.target.value) }}
                className="w-20 h-8 text-[length:var(--type-utility-xs-size)] font-mono"
              />
              <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">days</span>
            </div>
          </div>
        </Card>
      </section>

      {/* Backup & Restore — WP4 (ADR-0010): local backup, off in platform
          mode. Hidden entirely rather than shown-disabled: the server
          answers 404 for these three routes outside local mode, so a
          visible-but-broken control would be worse than no control. */}
      {localMode && (
        <section className="space-y-[var(--space-2-5)]">
          <div className="flex items-center justify-between">
            <h3 className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider">Backup & Restore</h3>
            <Button
              size="sm"
              variant="outline"
              className="h-7 px-[var(--space-2)] gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]"
              onClick={() => doBackup()}
              disabled={isCreatingBackup}
            >
              <Archive size={11} />
              {isCreatingBackup ? 'Creating...' : 'Create backup'}
            </Button>
          </div>

          <Card className="divide-y divide-[var(--color-border)]">
            {backupsLoading && (
              <div className="p-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Loading backups...</div>
            )}
            {backupsError && (
              <div className="p-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-text-error)]">Failed to load backups. Please try again.</div>
            )}
            {!backupsLoading && !backupsError && backups.length === 0 && (
              <div className="p-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">No backups yet.</div>
            )}
            {backups.map((b) => (
              <div key={b.filename} className="flex items-center justify-between px-[var(--space-3)] py-[var(--space-2)]">
                <div>
                  <p className="text-[length:var(--type-utility-xs-size)] font-mono text-[var(--color-secondary)]">{b.filename}</p>
                  <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                    {formatBytes(b.size_bytes)} &middot; {new Date(b.created_at).toLocaleString()}
                  </p>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 px-[var(--space-2)] gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]"
                  onClick={() => setRestoreTarget(b.filename)}
                >
                  <ArrowCounterClockwise size={11} />
                  Restore
                </Button>
              </div>
            ))}
          </Card>
        </section>
      )}

      <Separator />

      {/* Danger zone */}
      <section className="space-y-[var(--space-2-5)]">
        <h3 className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-error)] uppercase tracking-wider">Danger Zone</h3>
        <Card className="border-[var(--color-error)]/30 p-[var(--space-3)] flex items-center justify-between">
          <div>
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Clear all sessions</p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Permanently delete all session transcripts. Cannot be undone.</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="h-8 gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-error)] border-[var(--color-error)]/40 hover:bg-[var(--color-error)]/10"
            onClick={() => setClearConfirmOpen(true)}
          >
            <Trash size={12} />
            Clear sessions
          </Button>
        </Card>
      </section>

      {/* Clear sessions confirmation */}
      <Dialog open={clearConfirmOpen} onOpenChange={setClearConfirmOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="font-headline text-base">Clear all sessions?</DialogTitle>
          </DialogHeader>
          <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)] py-[var(--space-2)]">
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

      {/* Restore confirmation */}
      <Dialog open={!!restoreTarget} onOpenChange={() => setRestoreTarget(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="font-headline text-base">Restore backup?</DialogTitle>
          </DialogHeader>
          <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)] py-[var(--space-2)]">
            Restore from <span className="font-mono text-[var(--color-secondary)]">{restoreTarget}</span>?
            Current data will be overwritten. Gateway restart required after restore.
          </p>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setRestoreTarget(null)}>Cancel</Button>
            <Button
              size="sm"
              onClick={() => {
                if (!restoreTarget) return
                const filename = restoreTarget
                void stepUp
                  .gate((token) => doRestoreAsync({ filename, token }), {
                    title: 'Restore this backup?',
                    body: `Restore from ${filename}? Current data will be overwritten.`,
                    confirmLabel: 'Restore',
                  })
                  .catch((err: unknown) => {
                    // A cancelled gate sent nothing; a real failure already toasted.
                    if (!isReAuthCancelled(err)) return
                  })
              }}
              disabled={isRestoring}
            >
              {isRestoring ? 'Restoring...' : 'Restore'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {stepUp.dialogs}
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
