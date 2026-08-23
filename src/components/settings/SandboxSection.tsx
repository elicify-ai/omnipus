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
  Warning,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
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
  getErrorMessage,
} from '@/lib/api'
import type { SandboxStatus, SandboxConfigResponse } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SaveStatus, useSaveStatus } from './SaveStatus'
import { ShellDenyPatternsEditor } from '@/components/agents/ShellDenyPatternsEditor'
import { useReAuthGate, isReAuthCancelled } from './useReAuthGate'
import { AllowedPathsEditor } from './AllowedPathsEditor'
import { SsrfEditor, SSRF_PRESETS } from './SsrfEditor'

// ── Constants ─────────────────────────────────────────────────────────────────

const ABI4_BANNER_SESSION_KEY = 'omnipus:abi4-banner-dismissed'

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

// Reads the canonical backend key (shell_deny_patterns) off a sandbox-config
// response — see pkg/gateway/rest_sandbox_config.go GET response. Shared by
// the configData→state hydration effect and the re-auth-cancel revert so
// both read the server value the same way.
function extractShellDenyPatterns(data: { shell_deny_patterns?: string[] } | undefined | null): string[] {
  return Array.isArray(data?.shell_deny_patterns) ? data.shell_deny_patterns : []
}

// ── Workspace file limit (ADR-068 §6) ─────────────────────────────────────────
//
// The workspace path guard is a DIFFERENT boundary from the kernel sandbox,
// and the two have been routinely confused: an operator turns the sandbox
// off, finds commands still refused, and nothing on this page names the rule
// that refused them (UAT defect 002). The kernel sandbox decides how the
// operating system polices a child process that has already been started; the
// workspace limit is a check Omnipus makes in-process and earlier, over the
// paths an agent's file tools and the text of a bash command may name.
//
// ── Why this is read defensively rather than off the generated type ──
//
// When this control was written the setting had no place in the sandbox-config
// payload at all: it was environment-variable only (pkg/config/config.go tags
// AgentDefaults.RestrictToWorkspace `json:"-"`), and its exposure was landing
// in parallel. The generated zod schema is .passthrough(), so a key the SPA
// does not know about survives response validation and arrives here intact —
// which means this control lights up the moment a gateway starts sending it,
// with no SPA release.
//
// Two spellings are accepted because the backend key was still being named
// while this shipped: `workspace_path_guard` (the settled name — a dedicated
// sandbox-scoped key, chosen so it can only be written through this
// re-auth-gated endpoint) and `restrict_to_workspace` (the name of the agent
// default it resolves into, in case the REST layer echoes that instead).
//
// Whichever spelling the server used on the way IN is the one written back
// out. That is not tidiness: PUTting the other name would be accepted with a
// 200 and silently ignored, which is precisely the "control that looks like it
// worked and did nothing" this section exists to stop.
const WORKSPACE_LIMIT_KEYS = ['workspace_path_guard', 'restrict_to_workspace'] as const
type WorkspaceLimitKey = (typeof WORKSPACE_LIMIT_KEYS)[number]

// The environment escape hatch outranks the saved setting on the backend
// (OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE — the FR-001 ops hatch, kept
// deliberately at the top of the precedence order). While it is in force a
// saved change here would persist and change nothing at runtime, so the
// control must not be offered. The gateway is expected to say so with this
// flag; when it is absent the UI falls back to a standing caveat, which is the
// most honest thing available without a signal.
const WORKSPACE_LIMIT_ENV_OVERRIDE_KEY = 'workspace_path_guard_env_override'
const WORKSPACE_LIMIT_ENV_VAR = 'OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE'

type WorkspaceLimit = {
  /** The exact key the server sent — the only key we send back. */
  key: WorkspaceLimitKey
  value: boolean
  /** True when the environment variable is overriding the saved value. */
  envOverride: boolean
}

// undefined means "this gateway does not report the setting" — NOT "off". A
// non-boolean (null, a string, a number) is treated the same way: a value the
// server never actually asserted must never be rendered as a chosen one.
function readWorkspaceLimit(data: SandboxConfigResponse | undefined | null): WorkspaceLimit | undefined {
  const raw = data as unknown as Record<string, unknown> | undefined | null
  if (!raw) return undefined
  for (const key of WORKSPACE_LIMIT_KEYS) {
    const value = raw[key]
    if (typeof value === 'boolean') {
      return { key, value, envOverride: raw[WORKSPACE_LIMIT_ENV_OVERRIDE_KEY] === true }
    }
  }
  return undefined
}

// PUT body for a sandbox-config mutation. SandboxConfigUpdate (generated from
// contracts/components/schemas/SandboxConfigUpdate.yaml) does not carry either
// workspace-limit spelling yet; the intersection keeps every other field
// checked against the real contract while letting this one through until the
// contract catches up.
type SandboxUpdateBody = Parameters<typeof updateSandboxConfig>[0] &
  Partial<Record<WorkspaceLimitKey, boolean>>

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
      <button tabIndex={0}
        type="button"
        onClick={onDismiss}
        className="shrink-0 text-[10px] text-yellow-400 underline hover:text-yellow-300 focus:outline-none focus:ring-yellow-400 rounded"
      >
        Dismiss for session
      </button>
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

  // ── Global shell deny patterns (independent autosave) ──────────────────────
  const [globalDenyPatterns, setGlobalDenyPatterns] = useState<string[]>([])
  const [denyPatternsSaving, setDenyPatternsSaving] = useState(false)

  const { mutate: saveDenyPatterns } = useMutation({
    mutationFn: async (data: { shell_deny_patterns: string[] }) => {
      // Persist via PUT /api/v1/security/sandbox-config under the canonical
      // backend key (shell_deny_patterns) defined in
      // pkg/gateway/rest_sandbox_config.go::sandboxConfigPutBody.
      await runGatedSandbox((token) =>
        updateSandboxConfig({
          shell_deny_patterns: data.shell_deny_patterns,
        }, token),
      )
    },
    onMutate: () => setDenyPatternsSaving(true),
    onSuccess: () => {
      setDenyPatternsSaving(false)
      void queryClient.invalidateQueries({ queryKey: ['sandbox-config'] })
    },
    onError: (err: Error) => {
      setDenyPatternsSaving(false)
      if (isReAuthCancelled(err)) {
        // user dismissed the password prompt — revert the optimistic edit so
        // the textarea doesn't keep showing an unsaved change with no
        // indicator that it was never persisted. denyPatternsTouched is
        // cleared first (same technique the configData hydration effect
        // uses) so this programmatic revert does not itself re-trigger the
        // debounced autosave below.
        denyPatternsTouched.current = false
        setGlobalDenyPatterns(extractShellDenyPatterns(configData))
        return
      }
      addToast({ message: getErrorMessage(err, 'Failed to save deny patterns'), variant: 'error' })
    },
  })

  // Sync globalDenyPatterns from configData.
  useEffect(() => {
    if (!configData) return
    setGlobalDenyPatterns(extractShellDenyPatterns(configData))
  }, [configData])

  // Debounced autosave for the global deny patterns. The flag distinguishes
  // hydration writes (where the effect should NOT fire a PUT) from user
  // edits. Without the debounce, ShellDenyPatternsEditor's per-keystroke
  // onChange would issue one PUT per character.
  const denyPatternsTouched = useRef(false)
  const markDenyPatternsTouched = () => { denyPatternsTouched.current = true }
  useEffect(() => {
    if (!denyPatternsTouched.current) return
    const handle = setTimeout(() => {
      saveDenyPatterns({
        shell_deny_patterns: globalDenyPatterns.filter((x) => x.trim() !== ''),
      })
    }, 400)
    return () => clearTimeout(handle)
  }, [globalDenyPatterns, saveDenyPatterns])

  // ── Mode state ─────────────────────────────────────────────────────────────
  const [currentMode, setCurrentMode] = useState<'enforce' | 'permissive' | 'off' | undefined>()
  // ADR-062 filesystem model. Separate from `mode`: mode decides whether the
  // kernel enforces at all, the model decides WHAT it enforces for reads and
  // execution. Both are restart-gated, and neither changes what may be written.
  const [currentFsModel, setCurrentFsModel] = useState<'confined' | 'open' | undefined>()
  // Workspace file limit — see readWorkspaceLimit above. Held as
  // `boolean | undefined` on purpose: undefined is not "off", it is "this
  // gateway never told us", and the two render very differently.
  const [currentWorkspaceLimit, setCurrentWorkspaceLimit] = useState<boolean | undefined>()
  const savedMode = configData?.mode as 'enforce' | 'permissive' | 'off' | undefined
  const savedFsModel = configData?.filesystem_model as 'confined' | 'open' | undefined
  const serverWorkspaceLimit = readWorkspaceLimit(configData)
  const savedWorkspaceLimit = serverWorkspaceLimit?.value
  const workspaceLimitSupported = serverWorkspaceLimit !== undefined
  const workspaceLimitEnvLocked = serverWorkspaceLimit?.envOverride === true

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
    setCurrentFsModel(configData.filesystem_model as 'confined' | 'open' | undefined)
    setCurrentWorkspaceLimit(readWorkspaceLimit(configData)?.value)
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
      if (isReAuthCancelled(err)) {
        // user dismissed the password prompt — no-op, not a toast-worthy
        // error, but the mode was already flipped optimistically in
        // handleModeChange before this mutation fired. It MUST be reverted
        // here too (same as the real-error branch below) — otherwise the
        // radio is left pointing at an unsaved target mode with no error
        // indicator (saveState goes back to 'idle', which SaveStatus renders
        // as nothing), and because handleModeChange early-returns when
        // `mode === currentMode`, re-clicking the same (already-selected but
        // unsaved) radio would not even refire onChange — no recovery path.
        setSaveState('idle')
        setCurrentMode(savedMode)
        return
      }
      setSaveState('error')
      const msg = getErrorMessage(err, 'Save failed')
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
      // Revert to server mode
      setCurrentMode(savedMode)
    },
  })

  // ── Filesystem-model save mutation ────────────────────────────────────────
  const { mutate: doSaveFsModel } = useMutation({
    mutationFn: (body: Parameters<typeof updateSandboxConfig>[0]) =>
      runGatedSandbox((token) => updateSandboxConfig(body, token)),
    onMutate: () => setSaveState('saving'),
    onSuccess: (saved) => {
      setSaveState('saved')
      queryClient.setQueryData(['sandbox-config'], saved)
      void queryClient.invalidateQueries({ queryKey: ['sandbox-config'] })
    },
    onError: (err: Error) => {
      if (isReAuthCancelled(err)) {
        setSaveState('idle')
        setCurrentFsModel(savedFsModel)
        return
      }
      setSaveState('error')
      const msg = getErrorMessage(err, 'Save failed')
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
      setCurrentFsModel(savedFsModel)
    },
  })

  // ── Workspace file limit save mutation ────────────────────────────────────
  // Same contract as doSaveFsModel: optimistic in the handler, reverted here on
  // both failure paths. The revert is mandatory, not tidiness — the handler's
  // equality guard means a stuck optimistic value can never be re-submitted by
  // clicking the same radio again, so without it there is no recovery path.
  const { mutate: doSaveWorkspaceLimit } = useMutation({
    mutationFn: (body: SandboxUpdateBody) =>
      runGatedSandbox((token) => updateSandboxConfig(body, token)),
    onMutate: () => setSaveState('saving'),
    onSuccess: (saved) => {
      setSaveState('saved')
      queryClient.setQueryData(['sandbox-config'], saved)
      void queryClient.invalidateQueries({ queryKey: ['sandbox-config'] })
    },
    onError: (err: Error) => {
      if (isReAuthCancelled(err)) {
        setSaveState('idle')
        setCurrentWorkspaceLimit(savedWorkspaceLimit)
        return
      }
      setSaveState('error')
      const msg = getErrorMessage(err, 'Save failed')
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
      setCurrentWorkspaceLimit(savedWorkspaceLimit)
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
      if (isReAuthCancelled(err)) {
        // user dismissed the password prompt — no-op, not an error; but the
        // path/SSRF add or delete that triggered this save already applied
        // optimistically to pathList/ssrfList. Revert both to the last
        // known-saved server state so the UI doesn't keep showing a "phantom"
        // unsaved entry with no indicator that it was never persisted
        // (mirrors doSaveMode's revert-on-cancel for the mode radio).
        setSaveState('idle')
        revertPathsSsrfToServer()
        return
      }
      setSaveState('error')
      const msg = getErrorMessage(err, 'Save failed')
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

  // Reverts pathList/ssrfList (and the derived SSRF preset/advanced-open
  // state) back to the last known-saved server config. Used both when the
  // wildcard-SSRF confirmation is backed out of and when the re-auth prompt
  // for a paths/SSRF save is cancelled — in both cases an optimistic edit
  // must not keep appearing applied when it was never persisted.
  function revertPathsSsrfToServer() {
    if (!configData) return
    setPathList(configData.allowed_paths ?? [])
    const serverSsrf = configData.ssrf?.allow_internal ?? []
    setSsrfList(serverSsrf)
    const matchedPreset = SSRF_PRESETS.findIndex((p) => listsMatch(serverSsrf, p.list))
    setSsrfActivePreset(matchedPreset >= 0 ? matchedPreset : null)
    setSsrfAdvancedOpen(matchedPreset < 0)
  }

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
    revertPathsSsrfToServer()
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
  function handleFilesystemModelChange(model: 'confined' | 'open') {
    if (model === currentFsModel) return
    // Optimistic, then reverted by doSaveFsModel's onError — same contract as
    // handleModeChange. Without the revert the radio would sit on an unsaved
    // value with no error indicator, and re-clicking it would not refire
    // onChange because of the equality guard above.
    setCurrentFsModel(model)
    doSaveFsModel({ filesystem_model: model })
  }

  function handleWorkspaceLimitChange(next: boolean) {
    // Belt and braces: the radios are not rendered at all when the gateway
    // does not report the setting, or when the environment variable is
    // overriding it, so this can only fire from a gateway that will honour
    // it. Guarding on the server value here as well means no future refactor
    // can turn this into a control that PUTs a key the server ignores.
    if (!serverWorkspaceLimit || workspaceLimitEnvLocked) return
    if (next === currentWorkspaceLimit) return
    setCurrentWorkspaceLimit(next)
    // The key is echoed back, never hardcoded — see WORKSPACE_LIMIT_KEYS.
    doSaveWorkspaceLimit({ [serverWorkspaceLimit.key]: next } as SandboxUpdateBody)
  }

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

  
/**
 * The two ADR-062 filesystem models, described by CONSEQUENCE rather than by
 * mechanism — an operator choosing between them cares what an agent can reach,
 * not how the profile is rendered.
 */
const FILESYSTEM_MODELS: { value: 'confined' | 'open'; label: string; desc: string }[] = [
  {
    value: 'confined',
    label: 'Confined',
    desc: 'Agents can only read and run things in places you have listed. Safest, and the most likely to break a tool that needs a file you did not anticipate.',
  },
  {
    value: 'open',
    label: 'Open',
    desc: 'Agents can read and run anything on this machine except Omnipus\u2019s own secrets. Tools work without a list to maintain; anything readable by you is readable by them.',
  },
]

/**
 * The three kernel-sandbox modes, described by CONSEQUENCE and each one
 * explicit about the boundary it does NOT cover.
 *
 * The previous copy named only the mechanism ("Landlock + seccomp denies
 * violating syscalls"), which is how this switch came to be read as the single
 * master control over what an agent may touch: operators turned it off, stayed
 * blocked by the shell workspace limit below, and had nothing on the page to
 * explain the contradiction (UAT defect 002, ADR-068 §6).
 */
const SANDBOX_MODES: Array<{ value: 'enforce' | 'permissive' | 'off'; label: string; desc: string }> = [
    {
      value: 'enforce',
      label: 'Enforce',
      desc: 'The operating system itself stops a program that reaches outside what you have allowed \u2014 even a program that ignores Omnipus\u2019s own rules. Uses Linux kernel Landlock and seccomp.',
    },
    {
      value: 'permissive',
      label: 'Permissive',
      desc: 'The same policy is worked out and every violation is written to the audit log, but nothing is blocked. Useful for seeing what enforcing would break before you commit to it.',
    },
    {
      value: 'off',
      label: 'Off',
      desc: 'No operating-system protection: a program an agent starts can reach whatever your own account can reach. Development only \u2014 the production warning banner will fire. This does not switch off the shell workspace limit or the blocked command patterns below; those are separate rules with their own settings.',
    },
  ]

/**
 * The two postures of the shell workspace limit (ADR-068 §6), deliberately
 * described without the sandbox's vocabulary. The entire point of the control
 * is that an operator can tell it apart from the kernel switch above it.
 */
const WORKSPACE_LIMIT_OPTIONS: Array<{ value: 'on' | 'off'; label: string; desc: string }> = [
  {
    value: 'on',
    label: 'Confined to the working folder',
    desc: 'A shell command naming a file outside the agent\u2019s working folder is refused before it runs. Folders you have mounted into the workspace count as inside.',
  },
  {
    value: 'off',
    label: 'Any path on this machine',
    desc: 'Shell commands may name files anywhere your own account can reach. Blocked command patterns and the process sandbox above still apply \u2014 this setting lifts neither.',
  },
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
            <button tabIndex={0}
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
            <p className="text-xs text-[var(--color-muted)] leading-snug">
              Whether the operating system polices the programs an agent starts. It does not decide
              which files a shell command may name &mdash; that is the shell workspace limit further
              down, which has its own separate setting.
            </p>

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
                    <input tabIndex={0}
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

            {/* ── ADR-062 filesystem model ──
                Separate control from mode because they answer different
                questions: mode is WHETHER the kernel enforces, the model is
                WHAT it enforces for reads and execution. Surfaced because the
                two postures are indistinguishable from behaviour — an operator
                cannot tell whether a read succeeded because the model is open
                or because that path happened to be enumerated. Before this the
                only way to change it was to hand-edit config.json. */}
            {!configLoading && (
              <fieldset className="space-y-2 mt-4 border-t border-[var(--color-border)] pt-4">
                <legend className="sr-only">Filesystem model</legend>
                <p className="text-xs font-semibold text-[var(--color-secondary)]">
                  Filesystem model
                </p>
                <p className="text-xs text-[var(--color-muted)] leading-snug">
                  What an agent may READ and RUN. Neither option changes what it may WRITE —
                  writes stay inside the workspace and any folders you have mounted.
                </p>
                {FILESYSTEM_MODELS.map((m) => (
                  <label
                    key={m.value}
                    className={`flex items-start gap-2 p-2 rounded-md border cursor-pointer transition-colors ${
                      currentFsModel === m.value
                        ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/5'
                        : 'border-[var(--color-border)] hover:bg-[var(--color-surface-2)]'
                    }`}
                  >
                    <input
                      tabIndex={0}
                      type="radio"
                      name="filesystem-model"
                      value={m.value}
                      checked={currentFsModel === m.value}
                      onChange={() => handleFilesystemModelChange(m.value)}
                      className="mt-0.5 accent-[var(--color-accent)]"
                      aria-label={`Filesystem model: ${m.label}`}
                      data-testid={`sandbox-fs-model-${m.value}`}
                    />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium text-[var(--color-secondary)]">{m.label}</p>
                      <p className="text-xs text-[var(--color-muted)] leading-snug">{m.desc}</p>
                    </div>
                  </label>
                ))}
                <p className="text-xs text-[var(--color-muted)]">
                  Takes effect after a restart — the kernel profile is built once at startup.
                </p>
              </fieldset>
            )}
          </div>

          {/* ── Shell workspace limit (ADR-068 §6) ──
              Its own block, not another fieldset inside the sandbox-mode
              group, because the defect being fixed is precisely that operators
              could not tell the two boundaries apart. It gets a rule above it,
              its own heading, and copy that names the difference in the first
              sentence.

              When the gateway does not report restrict_to_workspace the block
              renders an explanation instead of radios. Rendering a disabled
              pair of radios was rejected: with nothing to check they would
              have had to show an invented default, and a control showing a
              value the server never sent is exactly the lie this section is
              here to stop telling. */}
          <div
            className="space-y-2 border-t border-[var(--color-border)] pt-4"
            data-testid="workspace-limit-section"
          >
            <p className="text-xs font-semibold text-[var(--color-secondary)]">Shell workspace limit</p>
            <p className="text-xs text-[var(--color-muted)] leading-snug">
              A different boundary from the sandbox above, with its own setting. The sandbox is an
              operating-system protection wrapped around the programs an agent starts. This is a
              check Omnipus makes on the command text itself, before anything starts: it decides
              whether a shell command may name a file outside the agent&rsquo;s working folder.
              Turning the sandbox off does not turn this off.
            </p>

            {configLoading ? (
              <div className="space-y-2 animate-pulse">
                <div className="h-3 w-3/4 rounded bg-[var(--color-border)]" />
                <div className="h-3 w-1/2 rounded bg-[var(--color-border)]" />
              </div>
            ) : !workspaceLimitSupported ? (
              <div
                className="flex items-start gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] p-2.5"
                role="status"
                data-testid="workspace-limit-unavailable"
              >
                <Warning
                  size={14}
                  weight="fill"
                  style={{ color: 'var(--color-muted)' }}
                  className="mt-0.5 shrink-0"
                />
                <p className="text-xs leading-relaxed text-[var(--color-secondary)]">
                  <span className="font-semibold">Cannot be changed from here.</span> This gateway
                  does not report the workspace limit, so Omnipus cannot show you whether it is on
                  or off, and a setting here would have no effect. On this version it is set only by
                  the{' '}
                  <code className="font-mono break-all">{WORKSPACE_LIMIT_ENV_VAR}</code>{' '}
                  environment variable, which is read once when the gateway starts. Upgrade the
                  gateway to manage it here.
                </p>
              </div>
            ) : (
              <fieldset className="space-y-2">
                <legend className="sr-only">Shell workspace limit</legend>
                {WORKSPACE_LIMIT_OPTIONS.map((o) => {
                  const selected = currentWorkspaceLimit === (o.value === 'on')
                  return (
                    <label
                      key={o.value}
                      className={`flex items-start gap-2 p-2 rounded-md border cursor-pointer transition-colors ${
                        selected
                          ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/5'
                          : 'border-[var(--color-border)] hover:bg-[var(--color-surface-2)]'
                      }`}
                    >
                      <input
                        tabIndex={0}
                        type="radio"
                        name="workspace-limit"
                        value={o.value}
                        checked={selected}
                        onChange={() => handleWorkspaceLimitChange(o.value === 'on')}
                        className="mt-0.5 accent-[var(--color-accent)]"
                        aria-label={`Shell workspace limit: ${o.label}`}
                        data-testid={`sandbox-workspace-limit-${o.value}`}
                      />
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium text-[var(--color-secondary)]">{o.label}</p>
                        <p className="text-xs text-[var(--color-muted)] leading-snug">{o.desc}</p>
                      </div>
                    </label>
                  )
                })}
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
                    {getErrorMessage(saveMutation.error, 'Save failed')}
                  </p>
                )}
              </>
            )}
          </div>

          {/* ── Global shell deny patterns ── */}
          <div className="space-y-3 border-t border-[var(--color-border)] pt-4">
            <div className="flex items-center justify-between">
              <p className="text-xs font-semibold text-[var(--color-secondary)]">Global shell deny patterns</p>
              {denyPatternsSaving && (
                <span className="text-[10px] text-[var(--color-muted)]">Saving...</span>
              )}
            </div>
            <p className="text-[10px] text-[var(--color-muted)]">
              Fallback patterns applied to all agents that do not override them. One regex per line.
            </p>
            {configLoading ? (
              <div className="h-3 w-2/3 rounded bg-[var(--color-border)] animate-pulse" />
            ) : (
              <ShellDenyPatternsEditor
                value={globalDenyPatterns}
                onChange={(patterns) => {
                  markDenyPatternsTouched()
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
