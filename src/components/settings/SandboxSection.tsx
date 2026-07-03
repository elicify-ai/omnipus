import { useState, useEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ShieldCheck,
  ShieldWarning,
  Shield,
  ArrowsClockwise,
  CaretDown,
  CaretUp,
  XCircle,
  Cpu,
  Trash,
  Plus,
  Warning,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import {
  fetchSandboxStatus,
  fetchSandboxConfig,
  updateSandboxConfig,
  isApiError,
} from '@/lib/api'
import type { SandboxStatus, SandboxProfile } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SaveStatus, useSaveStatus } from './SaveStatus'
import { SandboxProfileSelector } from '@/components/agents/SandboxProfileSelector'
import { ShellDenyPatternsEditor } from '@/components/agents/ShellDenyPatternsEditor'
import { useReAuthGate } from './useReAuthGate'

// ── Constants ─────────────────────────────────────────────────────────────────

const ABI4_BANNER_SESSION_KEY = 'omnipus:abi4-banner-dismissed'

// SSRF preset definitions
const SSRF_PRESETS = [
  { label: 'Block all', list: [] as string[] },
  { label: 'Allow loopback only', list: ['127.0.0.1', '::1'] },
  {
    label: 'Allow RFC1918 + loopback',
    list: ['127.0.0.1', '::1', '10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', 'fc00::/7'],
  },
] as const

// SSRF entry validation — hostname/IP/CIDR check matching server-side rules.
function isValidSsrfEntry(entry: string): boolean {
  const trimmed = entry.trim()
  if (!trimmed) return false
  if (trimmed.includes('/')) {
    const slashIdx = trimmed.lastIndexOf('/')
    const ip = trimmed.slice(0, slashIdx)
    const prefixStr = trimmed.slice(slashIdx + 1)
    const prefixNum = parseInt(prefixStr, 10)
    if (isNaN(prefixNum) || prefixNum < 0 || prefixStr === '') return false
    const ipv4Re = /^(\d{1,3}\.){3}\d{1,3}$/
    if (ipv4Re.test(ip) && prefixNum <= 32) return true
    if (ip.includes(':') && prefixNum <= 128) return true
    return false
  }
  const ipv4Re = /^(\d{1,3}\.){3}\d{1,3}$/
  if (ipv4Re.test(trimmed)) return true
  if (trimmed.includes(':')) return true
  const hostnameRe = /^[A-Za-z0-9][A-Za-z0-9.-]*$/
  return hostnameRe.test(trimmed)
}

function isWildcardEntry(entry: string): boolean {
  return entry === '0.0.0.0/0' || entry === '::/0'
}

function listsMatch(a: string[], b: readonly string[]): boolean {
  if (a.length !== b.length) return false
  const sortedA = [...a].sort()
  const sortedB = [...b].sort()
  return sortedA.every((v, i) => v === sortedB[i])
}

// ── Status dot ────────────────────────────────────────────────────────────────

type DotVariant = 'green' | 'amber' | 'red'

function StatusDot({ variant }: { variant: DotVariant }) {
  const colors: Record<DotVariant, string> = {
    green: 'var(--color-success)',
    amber: 'var(--color-warning)',
    red: 'var(--color-error)',
  }
  return (
    <span
      className="inline-block w-2 h-2 rounded-full flex-shrink-0"
      style={{ backgroundColor: colors[variant] }}
      aria-hidden="true"
    />
  )
}

// ── Capability badge ──────────────────────────────────────────────────────────

function CapBadge({ children }: { children: React.ReactNode }) {
  return (
    <span className="inline-block rounded px-1.5 py-0.5 text-[10px] font-mono border border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-secondary)]">
      {children}
    </span>
  )
}

// Read-only badge with tooltip for allowed_paths rows
function ReadOnlyBadge() {
  const [tip, setTip] = useState(false)
  return (
    <span className="relative inline-block">
      <button
        type="button"
        className="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-mono border border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] cursor-default"
        onMouseEnter={() => setTip(true)}
        onMouseLeave={() => setTip(false)}
        onFocus={() => setTip(true)}
        onBlur={() => setTip(false)}
        tabIndex={0}
        aria-describedby={tip ? 'ro-tip' : undefined}
      >
        read-only
      </button>
      {tip && (
        <span
          id="ro-tip"
          role="tooltip"
          className="absolute bottom-full left-0 mb-1 z-50 w-64 rounded border border-[var(--color-border)] bg-[var(--color-surface-1)] px-2 py-1.5 text-[10px] text-[var(--color-muted)] shadow-lg pointer-events-none"
        >
          AllowedPaths entries grant read-only access. Write access is never available via this editor.
        </span>
      )}
    </span>
  )
}

// ── Skeleton ──────────────────────────────────────────────────────────────────

function SandboxSkeleton() {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3 animate-pulse">
      <div className="flex items-center gap-2">
        <div className="w-2 h-2 rounded-full bg-[var(--color-border)]" />
        <div className="h-4 w-32 rounded bg-[var(--color-border)]" />
      </div>
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
      <div className="h-3 w-2/3 rounded bg-[var(--color-border)]" />
    </div>
  )
}

// ── Capabilities detail ───────────────────────────────────────────────────────

function CapabilitiesPanel({ data }: { data: SandboxStatus }) {
  const hasFeatures = data.landlock_features && data.landlock_features.length > 0
  const hasSyscalls = data.blocked_syscalls && data.blocked_syscalls.length > 0

  return (
    <div className="border-t border-[var(--color-border)] pt-3 space-y-3 mt-3">
      <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
        Capabilities
      </p>

      {data.abi_version != null && (
        <div className="flex items-center justify-between">
          <span className="text-xs text-[var(--color-muted)]">Landlock ABI version</span>
          <CapBadge>{data.abi_version}</CapBadge>
        </div>
      )}

      {hasFeatures && (
        <div className="space-y-1.5">
          <span className="text-xs text-[var(--color-muted)]">Landlock features</span>
          <div className="flex flex-wrap gap-1">
            {data.landlock_features!.map((f) => (
              <CapBadge key={f}>{f}</CapBadge>
            ))}
          </div>
        </div>
      )}

      <div className="flex items-center justify-between">
        <span className="text-xs text-[var(--color-muted)]">Seccomp-BPF</span>
        <span
          className="text-xs font-medium"
          style={{ color: data.seccomp_enabled ? 'var(--color-success)' : 'var(--color-muted)' }}
        >
          {data.seccomp_enabled ? 'Enabled' : 'Disabled'}
        </span>
      </div>

      {hasSyscalls && (
        <div className="space-y-1.5">
          <span className="text-xs text-[var(--color-muted)]">
            Blocked syscalls ({data.blocked_syscalls!.length})
          </span>
          <div
            className="flex flex-wrap gap-1 max-h-28 overflow-y-auto pr-1"
            style={{ scrollbarWidth: 'thin' }}
          >
            {data.blocked_syscalls!.map((sc) => (
              <CapBadge key={sc}>{sc}</CapBadge>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// ── ABI v4 Banner ─────────────────────────────────────────────────────────────

function Abi4Banner({
  abiVersion,
  issueRef,
  onDismiss,
}: {
  abiVersion: number
  issueRef: string
  onDismiss: () => void
}) {
  return (
    <div
      role="alert"
      className="flex flex-col sm:flex-row sm:items-start gap-2 rounded-lg border border-yellow-500/40 bg-yellow-500/10 px-3 py-2.5"
    >
      <Warning size={14} className="mt-0.5 shrink-0 text-yellow-400" weight="fill" />
      <p className="flex-1 text-xs text-yellow-200 leading-relaxed">
        Your Linux kernel uses Landlock v{abiVersion}, which is not yet supported (issue {issueRef}).
        Enforce mode will exit with code 78 at boot. Use &lsquo;permissive&rsquo; or &lsquo;off&rsquo; until Landlock support is upgraded.
      </p>
      <button
        type="button"
        onClick={onDismiss}
        className="shrink-0 text-[10px] text-yellow-400 underline hover:text-yellow-300 focus:outline-none focus:ring-1 focus:ring-yellow-400 rounded"
      >
        Dismiss for session
      </button>
    </div>
  )
}

// ── Allowed Paths Editor ──────────────────────────────────────────────────────

interface AllowedPathsEditorProps {
  paths: string[]
  rowErrors: Record<number, string>
  restartedRows: Set<number>
  onDelete: (index: number) => void
  newPath: string
  onNewPathChange: (v: string) => void
  onAdd: () => void
  addError: string | null
}

function AllowedPathsEditor({
  paths,
  rowErrors,
  restartedRows,
  onDelete,
  newPath,
  onNewPathChange,
  onAdd,
  addError,
}: AllowedPathsEditorProps) {
  return (
    <div className="space-y-2">
      <p className="text-xs font-semibold text-[var(--color-secondary)]">
        Filesystem paths the sandbox may read
      </p>

      {paths.length === 0 && (
        <p className="text-xs text-[var(--color-muted)] italic">No allowed paths configured.</p>
      )}

      <div className="space-y-1">
        {paths.map((p, i) => (
          <div key={i} className="flex flex-col gap-0.5">
            <div className="flex items-center gap-2 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1.5">
              <span className="flex-1 text-xs font-mono text-[var(--color-secondary)] break-all">
                {p}
              </span>
              <ReadOnlyBadge />
              {restartedRows.has(i) && (
                <span className="inline-block rounded px-1.5 py-0.5 text-[10px] border border-yellow-500/40 bg-yellow-500/10 text-yellow-400">
                  restart required
                </span>
              )}
              <button
                type="button"
                aria-label={`Delete path ${p}`}
                className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] rounded"
                onClick={() => onDelete(i)}
              >
                <Trash size={12} />
              </button>
            </div>
            {rowErrors[i] && (
              <p className="text-[10px] text-[var(--color-error)] pl-2">{rowErrors[i]}</p>
            )}
          </div>
        ))}
      </div>

      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <Input
            value={newPath}
            onChange={(e) => onNewPathChange(e.target.value)}
            placeholder="/var/data/shared"
            className="h-7 text-xs font-mono flex-1"
            aria-label="New allowed path"
            onKeyDown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); onAdd() }
            }}
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-7 px-2 gap-1 text-xs shrink-0"
            onClick={onAdd}
            aria-label="Add path"
          >
            <Plus size={11} />
            Add
          </Button>
        </div>
        {addError && (
          <p className="text-[10px] text-[var(--color-error)]">{addError}</p>
        )}
      </div>
    </div>
  )
}

// ── SSRF Editor ───────────────────────────────────────────────────────────────

interface SsrfEditorProps {
  list: string[]
  activePreset: number | null
  advancedOpen: boolean
  onAdvancedToggle: () => void
  onPresetClick: (idx: number) => void
  advancedErrors: Record<number, string>
  onDeleteAdvanced: (idx: number) => void
  newSsrfEntry: string
  onNewSsrfEntryChange: (v: string) => void
  onAddSsrfEntry: () => void
  ssrfAddError: string | null
}

function SsrfEditor({
  list,
  activePreset,
  advancedOpen,
  onAdvancedToggle,
  onPresetClick,
  advancedErrors,
  onDeleteAdvanced,
  newSsrfEntry,
  onNewSsrfEntryChange,
  onAddSsrfEntry,
  ssrfAddError,
}: SsrfEditorProps) {
  return (
    <div className="space-y-2 border-t border-[var(--color-border)] pt-3">
      <p className="text-xs font-semibold text-[var(--color-secondary)]">
        SSRF internal-network policy
      </p>

      <div className="flex flex-wrap gap-2">
        {SSRF_PRESETS.map((preset, idx) => (
          <button
            key={preset.label}
            type="button"
            onClick={() => onPresetClick(idx)}
            className={[
              'rounded border px-3 py-1 text-xs transition-colors focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] cursor-pointer',
              activePreset === idx
                ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
                : 'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:border-[var(--color-accent)]/50',
            ].join(' ')}
            aria-pressed={activePreset === idx}
          >
            {preset.label}
          </button>
        ))}
      </div>

      <button
        type="button"
        onClick={onAdvancedToggle}
        className="flex items-center gap-1 text-[10px] text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors focus:outline-none"
        aria-expanded={advancedOpen}
      >
        {advancedOpen ? <CaretUp size={10} /> : <CaretDown size={10} />}
        Advanced (custom list)
      </button>

      {advancedOpen && (
        <div className="space-y-1 pl-3 border-l border-[var(--color-border)]">
          {list.length === 0 && (
            <p className="text-xs text-[var(--color-muted)] italic">Empty — all internal traffic blocked.</p>
          )}
          {list.map((entry, i) => (
            <div key={i} className="flex flex-col gap-0.5">
              <div className="flex items-center gap-2 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1">
                <span className="flex-1 text-xs font-mono text-[var(--color-secondary)] break-all">
                  {entry}
                </span>
                <button
                  type="button"
                  aria-label={`Delete SSRF entry ${entry}`}
                  className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] rounded"
                  onClick={() => onDeleteAdvanced(i)}
                >
                  <Trash size={12} />
                </button>
              </div>
              {advancedErrors[i] && (
                <p className="text-[10px] text-[var(--color-error)] pl-2">{advancedErrors[i]}</p>
              )}
            </div>
          ))}

          <div className="space-y-1 pt-1">
            <div className="flex items-center gap-2">
              <Input
                value={newSsrfEntry}
                onChange={(e) => onNewSsrfEntryChange(e.target.value)}
                placeholder="10.0.0.0/8 or internal.corp"
                className="h-7 text-xs font-mono flex-1"
                aria-label="New SSRF allow entry"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { e.preventDefault(); onAddSsrfEntry() }
                }}
              />
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="h-7 px-2 gap-1 text-xs shrink-0"
                onClick={onAddSsrfEntry}
                aria-label="Add SSRF entry"
              >
                <Plus size={11} />
                Add
              </Button>
            </div>
            {ssrfAddError && (
              <p className="text-[10px] text-[var(--color-error)]">{ssrfAddError}</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// ── Main component ────────────────────────────────────────────────────────────

export function SandboxSection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  // PUT /api/v1/security/sandbox-config is re-auth gated (Spec-6 FR-12.2). Every
  // sandbox-config mutation (mode toggle, agent-defaults autosave, paths/SSRF
  // save) routes its PUT through runGated, which replays a single-use consent
  // token via updateSandboxConfig's header arg when the server demands re-auth —
  // same dialog/copy as IntegrationsSection.
  const { runGated: runGatedSandbox, dialog: reAuthDialog } = useReAuthGate({
    title: 'Confirm to change sandbox',
    description: 'Re-type your password to change the sandbox security configuration.',
  })

  // ── Status query ───────────────────────────────────────────────────────────
  const [statusExpanded, setStatusExpanded] = useState(false)
  const {
    data: statusData,
    isLoading: statusLoading,
    isError: statusIsError,
    error: statusError,
    refetch: statusRefetch,
    isFetching: statusFetching,
  } = useQuery({
    queryKey: ['sandbox-status'],
    queryFn: fetchSandboxStatus,
  })

  // ── Config query ──────────────────────────────────────────────────────────
  const { data: configData, isLoading: configLoading } = useQuery({
    queryKey: ['sandbox-config'],
    queryFn: fetchSandboxConfig,
  })

  // ── Agents query (for default profile selector — we pick the first/system agent) ─
  // The "default for new agents" profile is a global concept; we store it via
  // a special placeholder. Since the backend does not yet expose a dedicated
  // endpoint for this, we use the config.agents.defaults pathway. For now the
  // UI controls are rendered and the save wires through updateConfig.
  const [defaultProfile, setDefaultProfile] = useState<SandboxProfile | undefined>(undefined)
  const [globalDenyPatterns, setGlobalDenyPatterns] = useState<string[]>([])
  const [agentDefaultsSaving, setAgentDefaultsSaving] = useState(false)

  const { mutate: saveAgentDefaults } = useMutation({
    mutationFn: async (data: { sandbox_profile?: SandboxProfile; shell_deny_patterns?: string[] }) => {
      // Persist via PUT /api/v1/security/sandbox-config under the canonical
      // backend keys (default_profile, shell_deny_patterns) defined in
      // pkg/gateway/rest_sandbox_config.go::sandboxConfigPutBody.
      await runGatedSandbox((token) =>
        updateSandboxConfig({
          default_profile: data.sandbox_profile,
          shell_deny_patterns: data.shell_deny_patterns,
        }, token),
      )
    },
    onMutate: () => setAgentDefaultsSaving(true),
    onSuccess: () => {
      setAgentDefaultsSaving(false)
      void queryClient.invalidateQueries({ queryKey: ['sandbox-config'] })
    },
    onError: (err: Error) => {
      setAgentDefaultsSaving(false)
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' })
    },
  })

  // Sync defaultProfile + globalDenyPatterns from configData. Reads the
  // canonical backend keys (default_profile, shell_deny_patterns) — see
  // pkg/gateway/rest_sandbox_config.go GET response.
  useEffect(() => {
    if (!configData) return
    const raw = configData as Record<string, unknown>
    if (typeof raw.default_profile === 'string' && raw.default_profile !== '') {
      setDefaultProfile(raw.default_profile as SandboxProfile)
    }
    if (Array.isArray(raw.shell_deny_patterns)) {
      setGlobalDenyPatterns(raw.shell_deny_patterns as string[])
    }
  }, [configData])

  // Debounced autosave for default profile + global deny patterns. The flag
  // distinguishes hydration writes (where the effect should NOT fire a PUT)
  // from user edits. Without the debounce, ShellDenyPatternsEditor's per-
  // keystroke onChange would issue one PUT per character.
  const agentDefaultsTouched = useRef(false)
  const markAgentDefaultsTouched = () => { agentDefaultsTouched.current = true }
  useEffect(() => {
    if (!agentDefaultsTouched.current) return
    const handle = setTimeout(() => {
      saveAgentDefaults({
        sandbox_profile: defaultProfile,
        shell_deny_patterns: globalDenyPatterns.filter((x) => x.trim() !== ''),
      })
    }, 400)
    return () => clearTimeout(handle)
  }, [defaultProfile, globalDenyPatterns, saveAgentDefaults])

  // ── Mode state ─────────────────────────────────────────────────────────────
  const [currentMode, setCurrentMode] = useState<'enforce' | 'permissive' | 'off' | undefined>()
  const savedMode = configData?.mode as 'enforce' | 'permissive' | 'off' | undefined

  const restartPending = !!(
    configData &&
    configData.applied_mode !== undefined &&
    configData.applied_mode !== '' &&
    configData.mode !== configData.applied_mode
  )

  // ── ABI v4 banner state ────────────────────────────────────────────────────
  const [bannerDismissed, setBannerDismissed] = useState(() => {
    if (typeof sessionStorage === 'undefined') return false
    return sessionStorage.getItem(ABI4_BANNER_SESSION_KEY) === 'dismissed'
  })

  function handleBannerDismiss() {
    sessionStorage.setItem(ABI4_BANNER_SESSION_KEY, 'dismissed')
    localStorage.setItem(ABI4_BANNER_SESSION_KEY, new Date().toISOString())
    setBannerDismissed(true)
  }

  const showAbi4Banner =
    !bannerDismissed &&
    typeof statusData?.abi_version === 'number' &&
    statusData.abi_version >= 4 &&
    typeof (statusData as SandboxStatus & { issue_ref?: string }).issue_ref === 'string'

  // ── Paths/SSRF editor state ───────────────────────────────────────────────
  const [pathList, setPathList] = useState<string[]>([])
  const [newPath, setNewPath] = useState('')
  const [pathAddError, setPathAddError] = useState<string | null>(null)
  const [pathRowErrors, setPathRowErrors] = useState<Record<number, string>>({})
  const [pathRestartedRows, setPathRestartedRows] = useState<Set<number>>(new Set())

  const [ssrfList, setSsrfList] = useState<string[]>([])
  const [ssrfActivePreset, setSsrfActivePreset] = useState<number | null>(null)
  const [ssrfAdvancedOpen, setSsrfAdvancedOpen] = useState(false)
  const [ssrfAdvancedErrors, setSsrfAdvancedErrors] = useState<Record<number, string>>({})
  const [newSsrfEntry, setNewSsrfEntry] = useState('')
  const [ssrfAddError, setSsrfAddError] = useState<string | null>(null)

  // ── Modal state ───────────────────────────────────────────────────────────
  const [showWildcardModal, setShowWildcardModal] = useState(false)
  const [showEnforceModal, setShowEnforceModal] = useState(false)
  const pendingSaveRef = useRef<(() => void) | null>(null)

  // ── Save status ────────────────────────────────────────────────────────────
  const { state: saveState, setState: setSaveState, errorMessage, setErrorMessage } = useSaveStatus()

  // ── Sync from config query ─────────────────────────────────────────────────
  useEffect(() => {
    if (!configData) return
    const paths = configData.allowed_paths ?? []
    setPathList(paths)
    setPathRestartedRows(new Set())

    const allowInternal = configData.ssrf?.allow_internal ?? []
    setSsrfList(allowInternal)

    const matchedPreset = SSRF_PRESETS.findIndex((p) => listsMatch(allowInternal, p.list))
    if (matchedPreset >= 0) {
      setSsrfActivePreset(matchedPreset)
      setSsrfAdvancedOpen(false)
    } else {
      setSsrfActivePreset(null)
      setSsrfAdvancedOpen(true)
    }

    // Sync mode
    setCurrentMode(configData.mode as 'enforce' | 'permissive' | 'off' | undefined)
  }, [configData])

  // ── Mode save mutation ────────────────────────────────────────────────────
  const { mutate: doSaveMode } = useMutation({
    mutationFn: (body: Parameters<typeof updateSandboxConfig>[0]) =>
      runGatedSandbox((token) => updateSandboxConfig(body, token)),
    onMutate: () => setSaveState('saving'),
    onSuccess: (saved) => {
      setSaveState('saved')
      queryClient.setQueryData(['sandbox-config'], saved)
      void queryClient.invalidateQueries({ queryKey: ['sandbox-config'] })
    },
    onError: (err: Error) => {
      setSaveState('error')
      const msg = isApiError(err) ? err.userMessage : err.message
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
      // Revert to server mode
      setCurrentMode(savedMode)
    },
  })

  // ── Paths/SSRF save mutation ──────────────────────────────────────────────
  const saveMutation = useMutation({
    mutationFn: (body: Parameters<typeof updateSandboxConfig>[0]) =>
      runGatedSandbox((token) => updateSandboxConfig(body, token)),
    onMutate: () => setSaveState('saving'),
    onSuccess: (resp) => {
      setSaveState('saved')
      void queryClient.invalidateQueries({ queryKey: ['sandbox-config'] })
      setPathAddError(null)
      setSsrfAddError(null)
      setPathRowErrors({})
      setSsrfAdvancedErrors({})
      if (resp.requires_restart) {
        setPathRestartedRows(new Set(pathList.map((_, i) => i)))
      }
    },
    onError: (err: Error) => {
      setSaveState('error')
      const msg = isApiError(err) ? err.userMessage : String(err)
      setErrorMessage(msg)
      const pathRowMatch = /allowed_paths\[(\d+)\]:\s*(.+)/.exec(msg)
      if (pathRowMatch) {
        const rowIdx = parseInt(pathRowMatch[1], 10)
        setPathRowErrors((prev) => ({ ...prev, [rowIdx]: pathRowMatch[2] }))
        return
      }
      const ssrfRowMatch = /ssrf\.allow_internal\[(\d+)\]:\s*(.+)/.exec(msg)
      if (ssrfRowMatch) {
        const rowIdx = parseInt(ssrfRowMatch[1], 10)
        setSsrfAdvancedErrors((prev) => ({ ...prev, [rowIdx]: ssrfRowMatch[2] }))
        return
      }
      setPathAddError(msg)
    },
  })

  // ── Commit helper (paths + SSRF) ──────────────────────────────────────────
  function commitPathsSsrf(paths: string[], ssrf: string[]) {
    setPathRowErrors({})
    setSsrfAdvancedErrors({})
    saveMutation.mutate({
      allowed_paths: paths,
      ssrf: { allow_internal: ssrf },
    })
  }

  function commitPathsSsrfWithWildcardCheck(paths: string[], ssrf: string[]) {
    const hasWildcard = ssrf.some(isWildcardEntry)
    if (hasWildcard) {
      pendingSaveRef.current = () => commitPathsSsrf(paths, ssrf)
      setShowWildcardModal(true)
      return
    }
    commitPathsSsrf(paths, ssrf)
  }

  // ── Allowed paths handlers ─────────────────────────────────────────────────
  function handleDeletePath(index: number) {
    const next = pathList.filter((_, i) => i !== index)
    setPathList(next)
    setPathRowErrors({})
    commitPathsSsrf(next, ssrfList)
  }

  function handleAddPath() {
    const trimmed = newPath.trim()
    if (!trimmed) {
      setPathAddError('Path cannot be empty.')
      return
    }
    setPathAddError(null)
    const next = [...pathList, trimmed]
    setPathList(next)
    setNewPath('')
    commitPathsSsrf(next, ssrfList)
  }

  // ── SSRF handlers ─────────────────────────────────────────────────────────
  function handlePresetClick(idx: number) {
    const nextSsrf = [...SSRF_PRESETS[idx].list]
    setSsrfActivePreset(idx)
    setSsrfList(nextSsrf)
    setSsrfAdvancedErrors({})
    commitPathsSsrfWithWildcardCheck(pathList, nextSsrf)
  }

  function handleDeleteSsrfEntry(idx: number) {
    const next = ssrfList.filter((_, i) => i !== idx)
    setSsrfList(next)
    setSsrfAdvancedErrors({})
    const matchedPreset = SSRF_PRESETS.findIndex((p) => listsMatch(next, p.list))
    setSsrfActivePreset(matchedPreset >= 0 ? matchedPreset : null)
    commitPathsSsrf(pathList, next)
  }

  function handleAddSsrfEntry() {
    const trimmed = newSsrfEntry.trim()
    if (!trimmed) {
      setSsrfAddError('Entry cannot be empty.')
      return
    }
    if (!isValidSsrfEntry(trimmed)) {
      setSsrfAddError('invalid entry — expected hostname, IP, or CIDR')
      return
    }
    setSsrfAddError(null)
    const next = [...ssrfList, trimmed]
    setSsrfList(next)
    setNewSsrfEntry('')
    const matchedPreset = SSRF_PRESETS.findIndex((p) => listsMatch(next, p.list))
    setSsrfActivePreset(matchedPreset >= 0 ? matchedPreset : null)
    commitPathsSsrfWithWildcardCheck(pathList, next)
  }

  // ── Wildcard modal handlers ───────────────────────────────────────────────
  function handleWildcardConfirm() {
    setShowWildcardModal(false)
    if (pendingSaveRef.current) {
      pendingSaveRef.current()
      pendingSaveRef.current = null
    }
  }

  function handleWildcardCancel() {
    setShowWildcardModal(false)
    pendingSaveRef.current = null
    // Revert SSRF list to server state
    if (configData) {
      const serverSsrf = configData.ssrf?.allow_internal ?? []
      setSsrfList(serverSsrf)
      const matchedPreset = SSRF_PRESETS.findIndex((p) => listsMatch(serverSsrf, p.list))
      setSsrfActivePreset(matchedPreset >= 0 ? matchedPreset : null)
      setSsrfAdvancedOpen(matchedPreset < 0)
    }
  }

  // ── Enforce modal handlers ────────────────────────────────────────────────
  function handleEnforceModalConfirm() {
    setShowEnforceModal(false)
    if (pendingSaveRef.current) {
      pendingSaveRef.current()
      pendingSaveRef.current = null
    }
  }

  function handleEnforceModalCancel() {
    setShowEnforceModal(false)
    pendingSaveRef.current = null
    // Revert to previous mode
    setCurrentMode(savedMode)
  }

  // ── Mode change handler ────────────────────────────────────────────────────
  function handleModeChange(mode: 'enforce' | 'permissive' | 'off') {
    if (mode === currentMode) return

    setCurrentMode(mode)

    const abiVersion = statusData?.abi_version
    const issueRef = (statusData as (SandboxStatus & { issue_ref?: string }) | undefined)?.issue_ref
    const isAbi4Incompatible =
      mode === 'enforce' &&
      typeof abiVersion === 'number' &&
      abiVersion >= 4 &&
      typeof issueRef === 'string'

    if (isAbi4Incompatible) {
      pendingSaveRef.current = () => doSaveMode({ mode })
      setShowEnforceModal(true)
      return
    }

    doSaveMode({ mode })
  }

  // ── Status display helpers ────────────────────────────────────────────────

  function resolveDotVariant(): DotVariant {
    if (!statusData) return 'red'
    if (statusData.kernel_level) return 'green'
    if (statusData.available) return 'amber'
    return 'red'
  }

  function resolveBackendLabel(): string {
    if (!statusData) return 'Unknown'
    return statusData.kernel_level ? statusData.backend : 'Application fallback'
  }

  function resolveHeaderIcon(): typeof Shield {
    if (statusData?.kernel_level) return ShieldCheck
    if (statusData?.available) return ShieldWarning
    return Shield
  }

  function resolveDescription(): string | null {
    if (!statusData) return null
    if (statusData.kernel_level) {
      return 'Child processes are restricted at the kernel level using Linux Landlock and seccomp-BPF. This provides strong isolation.'
    }
    return 'Kernel-level sandboxing is unavailable on this platform. Falling back to cooperative environment-variable enforcement — uncooperative binaries are NOT contained.'
  }

  const dotVariant = resolveDotVariant()
  const backendLabel = resolveBackendLabel()
  const HeaderIcon = resolveHeaderIcon()
  const description = resolveDescription()
  const backendColor = statusData?.kernel_level ? 'var(--color-accent)' : 'var(--color-muted)'

  const hasCapabilities = !!(
    statusData &&
    (statusData.abi_version != null ||
      (statusData.landlock_features && statusData.landlock_features.length > 0) ||
      (statusData.blocked_syscalls && statusData.blocked_syscalls.length > 0) ||
      statusData.seccomp_enabled)
  )

  const SANDBOX_MODES: Array<{ value: 'enforce' | 'permissive' | 'off'; label: string; desc: string }> = [
    { value: 'enforce', label: 'Enforce', desc: 'Kernel-level Landlock + seccomp denies violating syscalls.' },
    { value: 'permissive', label: 'Permissive', desc: 'Policy computed and logged; violations not blocked (audit-only).' },
    { value: 'off', label: 'Off', desc: 'Sandbox disabled. Development only; production banner will fire.' },
  ]

  const effectiveMode = currentMode ?? savedMode

  function renderStatusBody(): React.ReactNode {
    if (statusLoading) return <SandboxSkeleton />

    if (statusIsError) {
      const errorDetail = statusError instanceof Error ? statusError.message : undefined
      return (
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 flex items-start gap-2">
          <XCircle size={14} style={{ color: 'var(--color-error)' }} className="mt-0.5 shrink-0" />
          <div className="flex-1 min-w-0">
            <p className="text-sm text-[var(--color-error)]">Failed to load sandbox status</p>
            {errorDetail && (
              <p className="mt-0.5 text-xs font-mono text-[var(--color-muted)] break-words">
                {errorDetail}
              </p>
            )}
          </div>
          <Button
            size="sm"
            variant="outline"
            className="h-7 px-2 text-xs shrink-0"
            onClick={() => { void statusRefetch() }}
            disabled={statusFetching}
          >
            Retry
          </Button>
        </div>
      )
    }

    if (!statusData) return null

    return (
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4">
        <div className="flex items-center gap-2 mb-3">
          <StatusDot variant={dotVariant} />
          <HeaderIcon
            size={14}
            style={{ color: statusData.kernel_level ? 'var(--color-accent)' : 'var(--color-muted)' }}
            weight="duotone"
          />
          <span className="text-sm font-semibold font-mono" style={{ color: backendColor }}>
            {backendLabel}
          </span>
        </div>

        {description && (
          <p className="text-xs text-[var(--color-muted)] leading-relaxed">{description}</p>
        )}

        {statusData.notes && statusData.notes.length > 0 && (
          <div className="mt-2 rounded-md border border-yellow-500/30 bg-yellow-500/5 p-2 space-y-1">
            {statusData.notes.map((note, i) => (
              <p key={i} className="text-[10px] text-yellow-400 leading-relaxed">
                <span className="font-semibold">Note:</span> {note}
              </p>
            ))}
          </div>
        )}

        {hasCapabilities && (
          <>
            <button
              type="button"
              onClick={() => setStatusExpanded((e) => !e)}
              className="mt-3 flex items-center gap-1 text-[10px] text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
              aria-expanded={statusExpanded}
            >
              {statusExpanded ? <CaretUp size={10} /> : <CaretDown size={10} />}
              {statusExpanded ? 'Hide capabilities' : 'Show capabilities'}
            </button>
            {statusExpanded && <CapabilitiesPanel data={statusData} />}
          </>
        )}
      </div>
    )
  }

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-[var(--color-secondary)] flex items-center gap-1.5">
          <Cpu size={14} className="text-[var(--color-muted)]" />
          Process Sandbox
        </h3>
        <div className="flex items-center gap-2">
          <SaveStatus state={saveState} errorMessage={errorMessage} />
          <Button
            size="sm"
            variant="outline"
            className="h-7 px-2 gap-1 text-xs"
            aria-label="Refresh sandbox status"
            onClick={() => { void statusRefetch() }}
            disabled={statusFetching}
          >
            <ArrowsClockwise size={11} className={statusFetching ? 'animate-spin' : undefined} />
          </Button>
        </div>
      </div>

      {/* ABI v4 compatibility banner */}
      {showAbi4Banner && (
        <Abi4Banner
          abiVersion={statusData!.abi_version!}
          issueRef={(statusData as SandboxStatus & { issue_ref?: string }).issue_ref!}
          onDismiss={handleBannerDismiss}
        />
      )}

      {/* Status display */}
      {renderStatusBody()}

      {/* Config editor — only shown when status loaded successfully */}
      {!statusLoading && !statusIsError && (
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
          {/* ── Mode radio — top of config section ── */}
          <div className="space-y-2">
            <p className="text-xs font-semibold text-[var(--color-secondary)]">Sandbox mode</p>

            {/* Restart pending notice */}
            {restartPending && (
              <div
                className="flex items-start gap-2 rounded-md border p-2.5"
                style={{
                  borderColor: 'rgba(234,179,8,0.35)',
                  backgroundColor: 'rgba(234,179,8,0.08)',
                }}
                role="status"
              >
                <Warning size={14} weight="fill" style={{ color: 'var(--color-warning)' }} className="mt-0.5 shrink-0" />
                <p className="text-xs leading-relaxed text-[var(--color-secondary)]">
                  <span className="font-semibold" style={{ color: 'var(--color-warning)' }}>
                    Restart required.
                  </span>{' '}
                  Saved mode is{' '}
                  <code className="font-mono">{configData?.mode}</code> but the gateway is
                  currently running with{' '}
                  <code className="font-mono">{configData?.applied_mode || 'none'}</code>.
                  Restart the gateway for the change to take effect.
                </p>
              </div>
            )}

            {configLoading ? (
              <div className="space-y-2 animate-pulse">
                <div className="h-3 w-3/4 rounded bg-[var(--color-border)]" />
                <div className="h-3 w-1/2 rounded bg-[var(--color-border)]" />
              </div>
            ) : (
              <fieldset className="space-y-2">
                <legend className="sr-only">Sandbox mode</legend>
                {SANDBOX_MODES.map((m) => (
                  <label
                    key={m.value}
                    className={`flex items-start gap-2 p-2 rounded-md border cursor-pointer transition-colors ${
                      effectiveMode === m.value
                        ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/5'
                        : 'border-[var(--color-border)] hover:bg-[var(--color-surface-2)]'
                    }`}
                  >
                    <input
                      type="radio"
                      name="sandbox-mode"
                      value={m.value}
                      checked={effectiveMode === m.value}
                      onChange={() => handleModeChange(m.value)}
                      className="mt-0.5 accent-[var(--color-accent)]"
                      aria-label={`Sandbox mode: ${m.label}`}
                    />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium text-[var(--color-secondary)]">{m.label}</p>
                      <p className="text-xs text-[var(--color-muted)] leading-snug">{m.desc}</p>
                    </div>
                  </label>
                ))}
              </fieldset>
            )}
          </div>

          {/* ── Paths / SSRF editor ── */}
          <div className="space-y-4 border-t border-[var(--color-border)] pt-4">
            <p className="text-xs font-semibold text-[var(--color-secondary)]">Sandbox configuration</p>

            {configLoading ? (
              <div className="space-y-2 animate-pulse">
                <div className="h-3 w-3/4 rounded bg-[var(--color-border)]" />
                <div className="h-3 w-1/2 rounded bg-[var(--color-border)]" />
              </div>
            ) : (
              <>
                <AllowedPathsEditor
                  paths={pathList}
                  rowErrors={pathRowErrors}
                  restartedRows={pathRestartedRows}
                  onDelete={handleDeletePath}
                  newPath={newPath}
                  onNewPathChange={(v) => { setNewPath(v); setPathAddError(null) }}
                  onAdd={handleAddPath}
                  addError={pathAddError}
                />

                <SsrfEditor
                  list={ssrfList}
                  activePreset={ssrfActivePreset}
                  advancedOpen={ssrfAdvancedOpen}
                  onAdvancedToggle={() => setSsrfAdvancedOpen((v) => !v)}
                  onPresetClick={handlePresetClick}
                  advancedErrors={ssrfAdvancedErrors}
                  onDeleteAdvanced={handleDeleteSsrfEntry}
                  newSsrfEntry={newSsrfEntry}
                  onNewSsrfEntryChange={(v) => { setNewSsrfEntry(v); setSsrfAddError(null) }}
                  onAddSsrfEntry={handleAddSsrfEntry}
                  ssrfAddError={ssrfAddError}
                />

                {saveMutation.isError && (
                  <p className="text-xs text-[var(--color-error)]">
                    {isApiError(saveMutation.error)
                      ? saveMutation.error.userMessage
                      : saveMutation.error instanceof Error
                        ? saveMutation.error.message
                        : 'Save failed'}
                  </p>
                )}
              </>
            )}
          </div>

          {/* ── Default profile for new agents ── */}
          <div className="space-y-3 border-t border-[var(--color-border)] pt-4">
            <div className="flex items-center justify-between">
              <p className="text-xs font-semibold text-[var(--color-secondary)]">Default profile for new agents</p>
              {agentDefaultsSaving && (
                <span className="text-[10px] text-[var(--color-muted)]">Saving...</span>
              )}
            </div>
            <p className="text-[10px] text-[var(--color-muted)]">
              Applied to newly created custom agents that do not pick their own sandbox profile.
              Existing agents keep whatever profile they were configured with; the locked
              core agents (Mia, Jim, Ava, Ray, Max) always use their built-in profiles.
              Restart required to take effect.
            </p>
            {configLoading ? (
              <div className="h-3 w-2/3 rounded bg-[var(--color-border)] animate-pulse" />
            ) : (
              <SandboxProfileSelector
                value={defaultProfile}
                agentName="new agents"
                onChange={(p) => {
                  markAgentDefaultsTouched()
                  setDefaultProfile(p)
                }}
              />
            )}
          </div>

          {/* ── Global shell deny patterns ── */}
          <div className="space-y-3 border-t border-[var(--color-border)] pt-4">
            <p className="text-xs font-semibold text-[var(--color-secondary)]">Global shell deny patterns</p>
            <p className="text-[10px] text-[var(--color-muted)]">
              Fallback patterns applied to all agents that do not override them. One regex per line.
            </p>
            {configLoading ? (
              <div className="h-3 w-2/3 rounded bg-[var(--color-border)] animate-pulse" />
            ) : (
              <ShellDenyPatternsEditor
                value={globalDenyPatterns}
                onChange={(patterns) => {
                  markAgentDefaultsTouched()
                  setGlobalDenyPatterns(patterns)
                }}
              />
            )}
          </div>
        </div>
      )}

      {/* Wildcard SSRF confirmation modal */}
      <Dialog
        open={showWildcardModal}
        onOpenChange={(open) => { if (!open) handleWildcardCancel() }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Disable SSRF protection?</DialogTitle>
            <DialogDescription>
              This would disable SSRF protection entirely — continue?
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" onClick={handleWildcardCancel}>
              Cancel
            </Button>
            <Button
              type="button"
              size="sm"
              onClick={handleWildcardConfirm}
              style={{ background: 'var(--color-error)', color: '#fff' }}
            >
              Save anyway
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ABI v4 enforce-mode confirmation modal */}
      <Dialog
        open={showEnforceModal}
        onOpenChange={(open) => { if (!open) handleEnforceModalCancel() }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Kernel incompatibility warning</DialogTitle>
            <DialogDescription>
              {statusData && typeof statusData.abi_version === 'number' && statusData.abi_version >= 4
                ? `Your kernel reports Landlock ABI v${statusData.abi_version} (issue ${(statusData as SandboxStatus & { issue_ref?: string }).issue_ref ?? ''}). Enforce mode will cause the gateway to exit with code 78 at next boot. Save anyway?`
                : 'Enforce mode may cause issues with your current kernel configuration. Save anyway?'}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" onClick={handleEnforceModalCancel}>
              Cancel
            </Button>
            <Button
              type="button"
              size="sm"
              onClick={handleEnforceModalConfirm}
              style={{ background: 'var(--color-error)', color: '#fff' }}
            >
              Save anyway
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {reAuthDialog}
    </section>
  )
}
