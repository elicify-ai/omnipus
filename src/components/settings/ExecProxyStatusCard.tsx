import { useRef, useState } from 'react'
import { ReadErrorCallout } from './ReadErrorCallout'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Globe, ArrowsClockwise, Warning, CheckCircle, XCircle, ArrowCounterClockwise } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'
import { fetchExecProxyStatus, updateConfig } from '@/lib/api'
import { checkFormPart, toWriteResponse, type PartCheck } from './saveOutcome'
import { useSectionSavePart } from './SectionSaveScope'
import type { FormSavePart } from './useFormSaveBar'

const PROXY_LABEL = 'Network proxy for shell commands'

// ── Status indicator ──────────────────────────────────────────────────────────

type StatusVariant = 'running' | 'disabled' | 'stopped'

function resolveStatus(enabled: boolean, running: boolean): StatusVariant {
  if (!enabled) return 'disabled'
  if (running) return 'running'
  return 'stopped'
}

const STATUS_CONFIG: Record<
  StatusVariant,
  { label: string; dotColor: string; textColor: string; Icon: typeof CheckCircle }
> = {
  running: {
    label: 'Running',
    dotColor: 'var(--color-success)',
    textColor: 'var(--color-success)',
    Icon: CheckCircle,
  },
  disabled: {
    label: 'Disabled',
    dotColor: 'var(--color-muted)',
    textColor: 'var(--color-muted)',
    Icon: XCircle,
  },
  stopped: {
    label: 'Stopped',
    dotColor: 'var(--color-warning)',
    textColor: 'var(--color-warning)',
    Icon: Warning,
  },
}

// ── Component ─────────────────────────────────────────────────────────────────

export function ExecProxyStatusCard(): React.ReactElement {
  const queryClient = useQueryClient()

  const {
    data,
    isLoading,
    isError,
    error,
    refetch,
    isFetching,
  } = useQuery({
    queryKey: ['exec-proxy-status'],
    queryFn: fetchExecProxyStatus,
    refetchInterval: 10_000,
  })

  // CR-02 / FR-010 / C-4: the switch is a draft on Sandbox & network's one
  // Save bar. Save writes tools.exec.enable_proxy through PUT /config (a
  // partial update: other exec settings stay); the change needs a gateway
  // restart, which the shared Save offers afterwards.
  const [draft, setDraft] = useState<boolean | null>(null)
  // The value stored by this page's last Save: the running status keeps the
  // old value until the gateway restarts.
  const [storedBySave, setStoredBySave] = useState<boolean | null>(null)
  const saved = storedBySave ?? data?.enabled ?? false
  const changed = draft !== null && !!data && draft !== saved
  const sent = useRef<{ value: boolean; before: boolean } | null>(null)
  const readFailed = useRef(false)
  readFailed.current = isError
  const part: FormSavePart = {
    id: 'exec-proxy',
    count: changed ? 1 : 0,
    invalidCount: 0,
    blocked: () => readFailed.current,
    restartLabels: changed ? [PROXY_LABEL] : [],
    write: () => {
      const value = draft as boolean
      sent.current = { value, before: saved }
      return toWriteResponse(() => updateConfig({ tools: { exec: { enable_proxy: value } } }))
    },
    check: async (): Promise<PartCheck> => {
      const s = sent.current
      if (!s) return { kind: 'unknown' }
      return checkFormPart(
        [{ key: 'tools.exec.enable_proxy', label: PROXY_LABEL, draft: s.value, before: s.before }],
        async () => (await fetchExecProxyStatus()).enabled,
      )
    },
    onOutcome: (outcome) => {
      if (outcome.kind !== 'saved' && outcome.kind !== 'saved-not-in-use') return
      const s = sent.current
      if (s) setStoredBySave(s.value)
      // A choice made while this Save was running stays a draft.
      setDraft((d) => (s && d === s.value ? null : d))
      void queryClient.invalidateQueries({ queryKey: ['exec-proxy-status'] })
      void queryClient.invalidateQueries({ queryKey: ['config'] })
    },
    discard: () => setDraft(null),
  }
  const { outcome, ownBar } = useSectionSavePart(part)
  const restartPending = storedBySave !== null && data !== undefined && storedBySave !== data.enabled

  const status = data ? resolveStatus(data.enabled, data.running) : null
  const cfg = status ? STATUS_CONFIG[status] : null

  // Optimistically reflect the pending toggle in the switch; fall back to server value
  const switchChecked = draft ?? saved

  return (
    <section className="space-y-[var(--space-2-5)]">
      <div className="flex items-center justify-between">
        <h3 className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)] flex items-center gap-[var(--space-1)]">
          <Globe size={14} className="text-[var(--color-muted)]" />
          Bash HTTP Proxy
        </h3>
        <Button
          size="sm"
          variant="outline"
          className="h-7 px-[var(--space-2)] gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]"
          aria-label="Refresh exec proxy status"
          onClick={() => { void refetch() }}
          disabled={isFetching}
        >
          <ArrowsClockwise
            size={11}
            className={isFetching ? 'animate-spin motion-reduce:animate-none' : undefined}
          />
        </Button>
      </div>

      <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)]">
        {/* Enable toggle row */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-[var(--space-2)]">
            <span className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Enable SSRF Proxy</span>
            {restartPending && (
              <span className="inline-flex items-center gap-[var(--space-1)] px-[var(--space-1)] py-[var(--space-0-5)] rounded text-[length:var(--type-caption-size)] font-medium bg-[color-mix(in_srgb,var(--color-warning)_20%,transparent)] text-[var(--color-warning)] border border-[color-mix(in_srgb,var(--color-warning)_40%,transparent)]">
                <ArrowCounterClockwise size={10} weight="bold" />
                Gateway restart required
              </span>
            )}
          </div>
          <Switch
            checked={switchChecked}
            onCheckedChange={(next) => setDraft(next)}
            disabled={isLoading || !data}
          />
        </div>

        {isLoading ? (
          <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Checking...</p>
        ) : isError ? (
          <ReadErrorCallout
            title="Failed to load the proxy status"
            error={error}
            onRetry={() => void refetch()}
            retrying={isFetching}
            blocksSaving={false}
            data-testid="exec-proxy-load-error"
          />
        ) : data && cfg ? (
          <>
            {/* Status row */}
            <div className="flex items-center gap-[var(--space-2)]">
              <span
                className="inline-block w-2 h-2 rounded-full flex-shrink-0"
                style={{ backgroundColor: cfg.dotColor }}
                aria-hidden="true"
              />
              <span className="text-[length:var(--type-body-compact-size)] font-medium" style={{ color: cfg.textColor }}>
                {cfg.label}
              </span>
              {data.running && data.address && (
                <span className="font-mono text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] ml-[var(--space-1)]">
                  {data.address}
                </span>
              )}
            </div>

            {/* Contextual message */}
            {status === 'stopped' && (
              <div className="flex items-start gap-[var(--space-2)] rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/8 p-[var(--space-2)]">
                <Warning
                  size={13}
                  weight="fill"
                  className="flex-shrink-0 mt-[var(--space-0-5)]"
                  style={{ color: 'var(--color-warning)' }}
                />
                <p className="text-[length:var(--type-utility-xs-size)] leading-relaxed" style={{ color: 'var(--color-warning)' }}>
                  Proxy failed to start — bash commands are not SSRF-protected.
                </p>
              </div>
            )}
          </>
        ) : null}

        {/* Description */}
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed border-t border-[var(--color-border)] pt-[var(--space-2)]">
          Routes bash tool child process HTTP/HTTPS traffic through an SSRF-protected loopback proxy (SEC-28).
        </p>
      </Card>

      {outcome}
      {ownBar}
    </section>
  )
}
