import { useCallback, useState, useEffect, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
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
import { Card } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import {
  fetchSandboxStatus,
  fetchSandboxConfig,
  updateSandboxConfig,
  getErrorMessage,
} from '@/lib/api'
import type { SandboxStatus, SandboxConfigResponse } from '@/lib/api'
import { Callout, CalloutDescription, CalloutTitle } from '@/components/ui/callout'
import { PasswordBadge, RestartBadge } from './SaveBar'
import { checkFormPart, toWriteResponse, type PartCheck } from './saveOutcome'
import type { FormSavePart } from './useFormSaveBar'
import { useSectionSavePart } from './SectionSaveScope'
import { AllowedPathsEditor } from './AllowedPathsEditor'
import { SsrfEditor, SSRF_PRESETS } from './SsrfEditor'
import { settingsRow } from './settingsSections'
import { SectionLoading } from './SectionLoading'

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

const EMPTY_LIST: string[] = []

/** Same entries in the same order (a removed or added row is a change). */
function sameList(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i])
}

function listsMatch(a: string[], b: readonly string[]): boolean {
  if (a.length !== b.length) return false
  const sortedA = [...a].sort()
  const sortedB = [...b].sort()
  return sortedA.every((v, i) => v === sortedB[i])
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
    <span className="inline-block rounded px-[var(--space-1)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] font-mono border border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-secondary)]">
      {children}
    </span>
  )
}

// ── Skeleton ──────────────────────────────────────────────────────────────────

function SandboxSkeleton() {
  return (
    <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)] animate-pulse">
      <div className="flex items-center gap-[var(--space-2)]">
        <div className="w-2 h-2 rounded-full bg-[var(--color-border)]" />
        <div className="h-4 w-32 rounded bg-[var(--color-border)]" />
      </div>
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
      <div className="h-3 w-2/3 rounded bg-[var(--color-border)]" />
    </Card>
  )
}

// ── Capabilities detail ───────────────────────────────────────────────────────

function CapabilitiesPanel({ data }: { data: SandboxStatus }) {
  const hasFeatures = data.landlock_features && data.landlock_features.length > 0
  const hasSyscalls = data.blocked_syscalls && data.blocked_syscalls.length > 0

  return (
    <div className="border-t border-[var(--color-border)] pt-[var(--space-2-5)] space-y-[var(--space-2-5)] mt-[var(--space-2-5)]">
      <p className="text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
        Capabilities
      </p>

      {data.abi_version != null && (
        <div className="flex items-center justify-between">
          <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Landlock ABI version</span>
          <CapBadge>{data.abi_version}</CapBadge>
        </div>
      )}

      {hasFeatures && (
        <div className="space-y-[var(--space-1)]">
          <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Landlock features</span>
          <div className="flex flex-wrap gap-[var(--space-1)]">
            {data.landlock_features!.map((f) => (
              <CapBadge key={f}>{f}</CapBadge>
            ))}
          </div>
        </div>
      )}

      <div className="flex items-center justify-between">
        <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Seccomp-BPF</span>
        <span
          className="text-[length:var(--type-utility-xs-size)] font-medium"
          style={{ color: data.seccomp_enabled ? 'var(--color-success)' : 'var(--color-muted)' }}
        >
          {data.seccomp_enabled ? 'Enabled' : 'Disabled'}
        </span>
      </div>

      {hasSyscalls && (
        <div className="space-y-[var(--space-1)]">
          <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
            Blocked syscalls ({data.blocked_syscalls!.length})
          </span>
          <div
            className="flex flex-wrap gap-[var(--space-1)] max-h-28 overflow-y-auto pr-[var(--space-1)]"
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
      className="flex flex-col sm:flex-row sm:items-start gap-[var(--space-2)] rounded-lg border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-[var(--space-2-5)] py-[var(--space-2)]"
    >
      <Warning size={14} className="mt-[var(--space-0-5)] shrink-0 text-[var(--color-warning)]" weight="fill" />
      <p className="flex-1 text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)] leading-relaxed">
        Your Linux kernel uses Landlock v{abiVersion}, which is not yet supported (issue {issueRef}).
        Enforce mode will exit with code 78 at boot. Use &lsquo;permissive&rsquo; or &lsquo;off&rsquo; until Landlock support is upgraded.
      </p>
      <Button
        variant="link"
        type="button"
        onClick={onDismiss}
        className="h-auto w-auto shrink-0 p-0 text-[length:var(--type-caption-size)] text-[var(--color-warning)] underline hover:text-[var(--color-warning)]/80"
      >
        Dismiss for session
      </Button>
    </div>
  )
}

// ── Main component ────────────────────────────────────────────────────────────

export function SandboxSection(): React.ReactElement {
  const queryClient = useQueryClient()

  // Settings redesign #1519 (FR-010, FR-019, FR-025 to FR-027): every value
  // here is a draft on the section's Save bar. Save sends the changed values
  // in ONE PUT /security/sandbox-config after ONE password prompt that lists
  // each changed setting by name. Nothing is sent while the person edits.

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
  const {
    data: configData,
    isLoading: configLoading,
    isError: configIsError,
    error: configError,
    refetch: configRefetch,
  } = useQuery({
    queryKey: ['sandbox-config'],
    queryFn: fetchSandboxConfig,
  })

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
  const savedPaths = configData?.allowed_paths ?? EMPTY_LIST
  const savedSsrf = configData?.ssrf?.allow_internal ?? EMPTY_LIST

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
  // A refusal of the whole write that names no single row.
  const [saveError, setSaveError] = useState<string | null>(null)

  // ── Modal state ───────────────────────────────────────────────────────────
  // Both confirms run when the value is picked (wireframe: "asks you to
  // confirm first"); Confirm makes it a draft, Cancel leaves the draft as it was.
  const [showWildcardModal, setShowWildcardModal] = useState(false)
  const [showEnforceModal, setShowEnforceModal] = useState(false)
  const pendingDraftRef = useRef<(() => void) | null>(null)

  // ── Sync drafts from the saved configuration ──────────────────────────────
  const syncFromServer = useCallback((data: SandboxConfigResponse) => {
    setPathList(data.allowed_paths ?? [])
    const allowInternal = data.ssrf?.allow_internal ?? []
    setSsrfList(allowInternal)
    const matchedPreset = SSRF_PRESETS.findIndex((p) => listsMatch(allowInternal, p.list))
    if (matchedPreset >= 0) {
      setSsrfActivePreset(matchedPreset)
      setSsrfAdvancedOpen(false)
    } else {
      setSsrfActivePreset(null)
      setSsrfAdvancedOpen(true)
    }
    setCurrentMode(data.mode as 'enforce' | 'permissive' | 'off' | undefined)
    setCurrentFsModel(data.filesystem_model as 'confined' | 'open' | undefined)
    setCurrentWorkspaceLimit(readWorkspaceLimit(data)?.value)
  }, [])

  // After a "changed somewhere else" check the read becomes the saved
  // configuration, but the sent drafts stay drafts (FR-012, FR-020).
  const syncOverrideRef = useRef<SandboxConfigResponse | null>(null)
  // What the last Save sent (set by the part's write below).
  const sentRef = useRef<SandboxUpdateBody | null>(null)
  // The page's drafts as of the last render, read when the saved
  // configuration changes (FR-010, FR-011, F03).
  const draftsRef = useRef<{
    mode?: { changed: boolean; value: string | undefined }
    fs?: { changed: boolean; value: string | undefined }
    ws?: { changed: boolean; value: boolean | undefined }
    paths?: { changed: boolean; value: string[] }
    ssrf?: { changed: boolean; value: string[] }
    // The two add inputs, read by a Save's late refusal (FR-015a).
    pathInput?: string
    ssrfInput?: string
  }>({})
  useEffect(() => {
    if (!configData) return
    const incoming = syncOverrideRef.current ?? configData
    syncOverrideRef.current = null
    // A new saved configuration replaces a field only when the field holds
    // no newer draft: a draft typed after the Save went out (not sent, or
    // different from what was sent) stays a draft (FR-010, FR-011, F03).
    const sent = sentRef.current ?? {}
    const d = draftsRef.current
    const newer = <T,>(draft: { changed: boolean; value: T } | undefined, sentValue: T | undefined, same: (a: T, b: T) => boolean) =>
      !!draft && draft.changed && (sentValue === undefined || !same(draft.value, sentValue))
    const eq = <T,>(a: T, b: T) => a === b
    const eqList = (a: string[], b: string[]) => sameList(a, b)
    let merged: SandboxConfigResponse = incoming
    if (newer(d.mode, sent.mode, eq)) merged = { ...merged, mode: d.mode!.value } as SandboxConfigResponse
    if (newer(d.fs, sent.filesystem_model, eq)) merged = { ...merged, filesystem_model: d.fs!.value } as SandboxConfigResponse
    const wsKey = readWorkspaceLimit(incoming)?.key
    if (wsKey && newer(d.ws, sent[wsKey], eq)) merged = { ...merged, [wsKey]: d.ws!.value } as SandboxConfigResponse
    if (newer(d.paths, sent.allowed_paths, eqList)) merged = { ...merged, allowed_paths: d.paths!.value }
    if (newer(d.ssrf, sent.ssrf?.allow_internal, eqList)) {
      merged = { ...merged, ssrf: { ...merged.ssrf, allow_internal: d.ssrf!.value } } as SandboxConfigResponse
    }
    syncFromServer(merged)
    setPathRestartedRows(new Set())
  }, [configData, syncFromServer])

  // ── Drafts ────────────────────────────────────────────────────────────────
  const modeChanged = currentMode !== undefined && currentMode !== savedMode
  const fsChanged = currentFsModel !== undefined && currentFsModel !== savedFsModel
  const wsChanged =
    workspaceLimitSupported && !workspaceLimitEnvLocked && currentWorkspaceLimit !== undefined && currentWorkspaceLimit !== savedWorkspaceLimit
  const pathsChanged = !sameList(pathList, savedPaths)
  const ssrfChanged = !sameList(ssrfList, savedSsrf)
  // Declared after the sync effect on purpose: effects run in order, so the
  // sync reads the drafts measured against the saved configuration they
  // were typed over, not against the incoming one.
  useEffect(() => {
    draftsRef.current = {
      mode: { changed: modeChanged, value: currentMode },
      fs: { changed: fsChanged, value: currentFsModel },
      ws: { changed: wsChanged, value: currentWorkspaceLimit },
      paths: { changed: pathsChanged, value: pathList },
      ssrf: { changed: ssrfChanged, value: ssrfList },
      pathInput: newPath,
      ssrfInput: newSsrfEntry,
    }
  })

  // Visible label, changed?, needs a gateway restart? (FR-025, FR-037)
  const rows: [string, boolean, boolean][] = [
    ['Sandbox mode', modeChanged, true],
    ['Filesystem model', fsChanged, true],
    ['Shell workspace limit', wsChanged, true],
    ['Allowed folders', pathsChanged, true],
    ['Internal network addresses', ssrfChanged, false],
  ]
  const changedLabels = rows.filter(([, changed]) => changed).map(([label]) => label)
  const restartLabels = rows.filter(([, changed, restart]) => changed && restart).map(([label]) => label)

  function buildBody(): SandboxUpdateBody {
    const body: SandboxUpdateBody = {}
    if (modeChanged) body.mode = currentMode
    if (fsChanged) body.filesystem_model = currentFsModel
    // The key is echoed back, never hardcoded — see WORKSPACE_LIMIT_KEYS.
    if (wsChanged && serverWorkspaceLimit) body[serverWorkspaceLimit.key] = currentWorkspaceLimit
    if (pathsChanged) body.allowed_paths = pathList
    if (ssrfChanged) body.ssrf = { allow_internal: ssrfList }
    return body
  }
  // The saved configuration when the Save started: the C-6 check tells
  // "before" from a third value with it.
  const sentBeforeRef = useRef<SandboxConfigResponse | null>(null)

  const part: FormSavePart = {
    id: 'sandbox-config',
    count: changedLabels.length,
    invalidCount: 0,
    restartLabels,
    // FR-025: every value of this section is password-marked.
    passwordLabels: changedLabels,
    write: ({ reauthToken }) => {
      const body = buildBody()
      sentRef.current = body
      sentBeforeRef.current = configData ?? null
      setSaveError(null)
      setPathRowErrors({})
      setSsrfAdvancedErrors({})
      return toWriteResponse(() => updateSandboxConfig(body, reauthToken))
    },
    // C-6: PUT /security/sandbox-config does not record failed applies, so a
    // read equal to the value before the Save never proves "not saved"; it
    // stays unknown. A third value is "changed somewhere else" (FR-020).
    check: async (): Promise<PartCheck> => {
      const sent = sentRef.current
      const before = sentBeforeRef.current
      if (!sent || !before) return { kind: 'unknown' }
      type Item = {
        key: string; label: string; draft: unknown; before: unknown; recorded: false
        pick: (d: SandboxConfigResponse) => unknown
        /** Writes a saved value into a configuration copy. */
        put: (d: SandboxConfigResponse, v: unknown) => SandboxConfigResponse
      }
      const items: Item[] = []
      const add = (key: string, label: string, draft: unknown, pick: Item['pick'], put: Item['put']) =>
        items.push({ key, label, draft, before: pick(before), recorded: false, pick, put })
      if (sent.mode !== undefined) {
        add('sandbox.mode', 'Sandbox mode', sent.mode, (d) => d.mode, (d, v) => ({ ...d, mode: v } as SandboxConfigResponse))
      }
      if (sent.filesystem_model !== undefined) {
        add('sandbox.filesystem_model', 'Filesystem model', sent.filesystem_model, (d) => d.filesystem_model,
          (d, v) => ({ ...d, filesystem_model: v } as SandboxConfigResponse))
      }
      for (const k of WORKSPACE_LIMIT_KEYS) {
        if (sent[k] !== undefined) {
          add(`sandbox.${k}`, 'Shell workspace limit', sent[k], (d) => readWorkspaceLimit(d)?.value,
            (d, v) => ({ ...d, [k]: v } as SandboxConfigResponse))
        }
      }
      if (sent.allowed_paths !== undefined) {
        add('sandbox.allowed_paths', 'Allowed folders', sent.allowed_paths, (d) => d.allowed_paths ?? [],
          (d, v) => ({ ...d, allowed_paths: v } as SandboxConfigResponse))
      }
      if (sent.ssrf?.allow_internal !== undefined) {
        add('sandbox.ssrf.allow_internal', 'Internal network addresses', sent.ssrf.allow_internal, (d) => d.ssrf?.allow_internal ?? [],
          (d, v) => ({ ...d, ssrf: { ...d.ssrf, allow_internal: v } } as SandboxConfigResponse))
      }
      const fresh: { read: SandboxConfigResponse | null } = { read: null }
      const result = await checkFormPart(items, async (key) => {
        const read = fresh.read ?? (fresh.read = await fetchSandboxConfig())
        const item = items.find((i) => i.key === key)
        if (!item) throw new Error(`no saved-state read for ${key}`)
        return item.pick(read)
      })
      if (result.kind === 'saved' && fresh.read) queryClient.setQueryData(['sandbox-config'], fresh.read)
      if (result.kind !== 'changed-elsewhere') return result
      // "Changed somewhere else": the saved configuration must be refreshed
      // even when every changed value came from the restart list and no
      // server read ran. One read, as on the normal path; if it fails,
      // nothing is known and the result stays unknown with Retry (C-6).
      let read: SandboxConfigResponse
      try {
        read = fresh.read ?? (await fetchSandboxConfig())
      } catch {
        return { kind: 'unknown' }
      }
      // A listed key's saved value is its persisted_value, not the running
      // read (C-6), so the check's saved values are laid onto the read.
      let saved = read
      for (const item of items) {
        if (item.key in result.saved) saved = item.put(saved, result.saved[item.key])
      }
      // The sent values stay drafts on top of the saved configuration.
      const { ssrf: sentSsrf, ...sentTop } = sent
      syncOverrideRef.current = {
        ...saved,
        ...sentTop,
        ...(sentSsrf ? { ssrf: { ...saved.ssrf, ...sentSsrf } } : {}),
      } as SandboxConfigResponse
      queryClient.setQueryData(['sandbox-config'], saved)
      return result
    },
    onOutcome: (outcome) => {
      if (outcome.kind === 'saved' || outcome.kind === 'saved-not-in-use') {
        const body = outcome.body as SandboxConfigResponse | undefined
        if (body && typeof body === 'object' && 'mode' in body) queryClient.setQueryData(['sandbox-config'], body)
        if (sentRef.current?.allowed_paths && (outcome.kind === 'saved-not-in-use' || body?.requires_restart)) {
          setPathRestartedRows(new Set(sentRef.current.allowed_paths.map((_, i) => i)))
        }
        void queryClient.invalidateQueries({ queryKey: ['sandbox-config'] })
        return
      }
      if (outcome.kind !== 'not-saved') return
      // FR-015a (release B09): a refusal naming one row belongs to what this
      // Save sent. A refused path that was a new draft in this Save leaves
      // the list and goes back into its add input with the server's message
      // under it, so a later Save does not send it again; every other draft
      // stays. A refused path that was already saved keeps its row and the
      // message. When the add input holds newer text or the row no longer
      // holds what was sent (4a7d2f269, SFH-03), nothing moves: the message
      // shows under the whole sandbox configuration.
      const sent = sentRef.current
      const savedBefore = sentBeforeRef.current
      const drafts = draftsRef.current
      const refusedRow = (sentList: string[] | undefined, current: string[] | undefined, idx: number) =>
        sentList !== undefined && idx < sentList.length && current?.[idx] === sentList[idx] ? sentList[idx] : null
      const pathRowMatch = /allowed_paths\[(\d+)\]:\s*(.+)/.exec(outcome.message)
      if (pathRowMatch) {
        const idx = parseInt(pathRowMatch[1], 10)
        const refused = refusedRow(sent?.allowed_paths, drafts.paths?.value, idx)
        if (refused === null) {
          setSaveError(outcome.message)
          return
        }
        if ((savedBefore?.allowed_paths ?? []).includes(refused)) {
          setPathRowErrors({ [idx]: pathRowMatch[2] })
          return
        }
        if ((drafts.pathInput ?? '') !== '') {
          setSaveError(outcome.message)
          return
        }
        setPathList((list) => (list[idx] === refused ? list.filter((_, i) => i !== idx) : list))
        setNewPath(refused)
        setPathAddError(pathRowMatch[2])
        return
      }
      const ssrfRowMatch = /ssrf\.allow_internal\[(\d+)\]:\s*(.+)/.exec(outcome.message)
      if (ssrfRowMatch) {
        const idx = parseInt(ssrfRowMatch[1], 10)
        const refused = refusedRow(sent?.ssrf?.allow_internal, drafts.ssrf?.value, idx)
        if (refused === null) {
          setSaveError(outcome.message)
          return
        }
        if ((savedBefore?.ssrf?.allow_internal ?? []).includes(refused)) {
          setSsrfAdvancedOpen(true)
          setSsrfAdvancedErrors({ [idx]: ssrfRowMatch[2] })
          return
        }
        if ((drafts.ssrfInput ?? '') !== '') {
          setSaveError(outcome.message)
          return
        }
        const current = drafts.ssrf?.value ?? []
        applySsrf(current.filter((_, i) => i !== idx))
        // The add input lives in the advanced editor.
        setSsrfAdvancedOpen(true)
        setNewSsrfEntry(refused)
        setSsrfAddError(ssrfRowMatch[2])
        return
      }
      setSaveError(outcome.message)
    },
    discard: () => {
      if (configData) syncFromServer(configData)
      setNewPath('')
      setNewSsrfEntry('')
      setPathAddError(null)
      setSsrfAddError(null)
      setPathRowErrors({})
      setSsrfAdvancedErrors({})
      setSaveError(null)
    },
  }

  // One Save bar for the section with the proxy switch (CR-02, FR-010).
  const { outcome: sandboxOutcome, ownBar } = useSectionSavePart(part)

  // ── Allowed paths handlers ─────────────────────────────────────────────────
  function handleDeletePath(index: number) {
    setPathList((list) => list.filter((_, i) => i !== index))
    setPathRowErrors({})
  }

  function handleAddPath() {
    const trimmed = newPath.trim()
    if (!trimmed) {
      setPathAddError('Path cannot be empty.')
      return
    }
    setPathAddError(null)
    setPathList((list) => [...list, trimmed])
    setNewPath('')
  }

  // ── SSRF handlers ─────────────────────────────────────────────────────────
  function applySsrf(next: string[]) {
    setSsrfList(next)
    setSsrfAdvancedErrors({})
    const matchedPreset = SSRF_PRESETS.findIndex((p) => listsMatch(next, p.list))
    setSsrfActivePreset(matchedPreset >= 0 ? matchedPreset : null)
  }

  function applySsrfWithWildcardCheck(next: string[]) {
    if (next.some(isWildcardEntry)) {
      pendingDraftRef.current = () => applySsrf(next)
      setShowWildcardModal(true)
      return
    }
    applySsrf(next)
  }

  function handlePresetClick(idx: number) {
    applySsrfWithWildcardCheck([...SSRF_PRESETS[idx].list])
  }

  function handleDeleteSsrfEntry(idx: number) {
    applySsrf(ssrfList.filter((_, i) => i !== idx))
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
    setNewSsrfEntry('')
    applySsrfWithWildcardCheck([...ssrfList, trimmed])
  }

  // ── Confirm modal handlers ────────────────────────────────────────────────
  function confirmPendingDraft() {
    setShowWildcardModal(false)
    setShowEnforceModal(false)
    pendingDraftRef.current?.()
    pendingDraftRef.current = null
  }

  function cancelPendingDraft() {
    setShowWildcardModal(false)
    setShowEnforceModal(false)
    pendingDraftRef.current = null
  }

  // ── Mode, filesystem model and workspace limit handlers ──────────────────
  function handleFilesystemModelChange(model: 'confined' | 'open') {
    setCurrentFsModel(model)
  }

  function handleWorkspaceLimitChange(next: boolean) {
    // Belt and braces: the radios are not rendered at all when the gateway
    // does not report the setting, or when the environment variable is
    // overriding it. Guarding here as well means no future refactor can turn
    // this into a draft for a key the server ignores.
    if (!serverWorkspaceLimit || workspaceLimitEnvLocked) return
    setCurrentWorkspaceLimit(next)
  }

  function handleModeChange(mode: 'enforce' | 'permissive' | 'off') {
    if (mode === currentMode) return

    const abiVersion = statusData?.abi_version
    const issueRef = (statusData as (SandboxStatus & { issue_ref?: string }) | undefined)?.issue_ref
    const isAbi4Incompatible =
      mode === 'enforce' &&
      typeof abiVersion === 'number' &&
      abiVersion >= 4 &&
      typeof issueRef === 'string'

    if (isAbi4Incompatible) {
      pendingDraftRef.current = () => setCurrentMode(mode)
      setShowEnforceModal(true)
      return
    }

    setCurrentMode(mode)
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
      desc: 'No operating-system protection: a program an agent starts can reach whatever your own account can reach. Development only \u2014 the production warning banner will fire. It does not switch off the shell workspace limit below or the shell command rules in your configuration file; those are separate rules.',
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
    desc: 'Shell commands may name files anywhere your own account can reach. The shell command rules in your configuration file and the process sandbox above still apply \u2014 this setting lifts neither.',
  },
]

  const effectiveMode = currentMode ?? savedMode

  function renderStatusBody(): React.ReactNode {
    if (statusLoading) return <SandboxSkeleton />

    if (statusIsError) {
      const errorDetail = statusError instanceof Error ? statusError.message : undefined
      return (
        <Card className="p-[var(--space-3)] flex items-start gap-[var(--space-2)]">
          <XCircle size={14} style={{ color: 'var(--color-error)' }} className="mt-[var(--space-0-5)] shrink-0" />
          <div className="flex-1 min-w-0">
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">Failed to load sandbox status</p>
            {errorDetail && (
              <p className="mt-[var(--space-0-5)] text-[length:var(--type-utility-xs-size)] font-mono text-[var(--color-muted)] break-words">
                {errorDetail}
              </p>
            )}
          </div>
          <Button
            size="sm"
            variant="outline"
            className="h-7 px-[var(--space-2)] text-[length:var(--type-utility-xs-size)] shrink-0"
            onClick={() => { void statusRefetch() }}
            disabled={statusFetching}
          >
            Retry
          </Button>
        </Card>
      )
    }

    if (!statusData) return null

    return (
      <Card className="p-[var(--space-3)]">
        <div className="flex items-center gap-[var(--space-2)] mb-[var(--space-2-5)]">
          <StatusDot variant={dotVariant} />
          <HeaderIcon
            size={14}
            style={{ color: statusData.kernel_level ? 'var(--color-accent)' : 'var(--color-muted)' }}
            weight="duotone"
          />
          <span className="text-[length:var(--type-body-compact-size)] font-semibold font-mono" style={{ color: backendColor }}>
            {backendLabel}
          </span>
        </div>

        {description && (
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">{description}</p>
        )}

        {statusData.notes && statusData.notes.length > 0 && (
          <div className="mt-[var(--space-2)] rounded-md border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/5 p-[var(--space-2)] space-y-[var(--space-1)]">
            {statusData.notes.map((note, i) => (
              <p key={i} className="text-[length:var(--type-caption-size)] text-[var(--color-warning)] leading-relaxed">
                <span className="font-semibold">Note:</span> {note}
              </p>
            ))}
          </div>
        )}

        {hasCapabilities && (
          <>
            <Button
              variant="ghost"
              type="button"
              onClick={() => setStatusExpanded((e) => !e)}
              className="mt-[var(--space-2-5)] h-auto w-auto gap-[var(--space-1)] p-0 text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
              aria-expanded={statusExpanded}
            >
              {statusExpanded ? <CaretUp size={10} /> : <CaretDown size={10} />}
              {statusExpanded ? 'Hide capabilities' : 'Show capabilities'}
            </Button>
            {statusExpanded && <CapabilitiesPanel data={statusData} />}
          </>
        )}
      </Card>
    )
  }

  // FR-065: the section's loading frame until both of its reads answer.
  if (statusLoading || configLoading) return <SectionLoading label="Sandbox & network" />

  return (
    <section className="space-y-[var(--space-2-5)]">
      <div className="flex items-center justify-between">
        <h3 className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)] flex items-center gap-[var(--space-1)]">
          <Cpu size={14} className="text-[var(--color-muted)]" />
          Process Sandbox
        </h3>
        <div className="flex items-center gap-[var(--space-2)]">
          <Button
            size="sm"
            variant="outline"
            className="h-7 px-[var(--space-2)] gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]"
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

      {/* FR-066: the settings need only the sandbox-config read; a failed
          status read shows its own error above and blocks nothing here. */}
      {configIsError && (
        <Callout variant="error" data-testid="sandbox-config-error">
          <CalloutTitle>The sandbox settings could not be loaded</CalloutTitle>
          <CalloutDescription>
            {getErrorMessage(configError, 'The gateway did not answer.')} Check that the gateway is running, then retry.
          </CalloutDescription>
          <Button variant="outline" size="sm" onClick={() => void configRefetch()}>
            Retry
          </Button>
        </Callout>
      )}

      {!configIsError && (
        <Card className="p-[var(--space-3)] space-y-[var(--space-3)]">
          {/* ── Mode radio — top of config section ── */}
          <div className="space-y-[var(--space-2)]" {...settingsRow('Sandbox mode')}>
            <p className="flex flex-wrap items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)]">
              Sandbox mode
              <RestartBadge />
              <PasswordBadge />
            </p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-snug">
              Whether the operating system polices the programs an agent starts. It does not decide
              which files a shell command may name &mdash; that is the shell workspace limit further
              down, which has its own separate setting.
            </p>

            {/* Restart pending notice */}
            {restartPending && (
              <div
                className="flex items-start gap-[var(--space-2)] rounded-md border p-[var(--space-2)]"
                style={{
                  borderColor: 'color-mix(in srgb, var(--color-warning) 35%, transparent)',
                  backgroundColor: 'color-mix(in srgb, var(--color-warning) 8%, transparent)',
                }}
                role="status"
              >
                <Warning size={14} weight="fill" style={{ color: 'var(--color-warning)' }} className="mt-[var(--space-0-5)] shrink-0" />
                <p className="text-[length:var(--type-utility-xs-size)] leading-relaxed text-[var(--color-secondary)]">
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
              <div className="space-y-[var(--space-2)] animate-pulse">
                <div className="h-3 w-3/4 rounded bg-[var(--color-border)]" />
                <div className="h-3 w-1/2 rounded bg-[var(--color-border)]" />
              </div>
            ) : (
              <fieldset className="space-y-[var(--space-2)]">
                <legend className="sr-only">Sandbox mode</legend>
                {SANDBOX_MODES.map((m) => (
                  <Label
                    key={m.value}
                    className={`flex pointer-coarse:min-h-[var(--target-touch-minimum)] items-start gap-[var(--space-2)] p-[var(--space-2)] rounded-md border cursor-pointer transition-colors ${
                      effectiveMode === m.value
                        ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/5'
                        : 'border-[var(--color-border)] hover:bg-[var(--color-surface-2)]'
                    }`}
                  >
                    {/* U8-A / FR-070: coarse-pointer tap area around the glyph (globals.css [data-radio-target]). */}
                    <span data-radio-target="" className="relative mt-[var(--space-0-5)] inline-flex shrink-0">
                      <input tabIndex={0}
                        type="radio"
                        name="sandbox-mode"
                        value={m.value}
                        checked={effectiveMode === m.value}
                        onChange={() => handleModeChange(m.value)}
                        className="accent-[var(--color-accent)]"
                        aria-label={`Sandbox mode: ${m.label}`}
                      />
                    </span>
                    <div className="flex-1 min-w-0">
                      <p className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">{m.label}</p>
                      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-snug">{m.desc}</p>
                    </div>
                  </Label>
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
              <fieldset className="space-y-[var(--space-2)] mt-[var(--space-3)] border-t border-[var(--color-border)] pt-[var(--space-3)]" {...settingsRow('Filesystem model')}>
                <legend className="sr-only">Filesystem model</legend>
                <p className="flex flex-wrap items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)]">
                  Filesystem model
                  <RestartBadge />
                  <PasswordBadge />
                </p>
                <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-snug">
                  What an agent may READ and RUN. Neither option changes what it may WRITE —
                  writes stay inside the workspace and any folders you have mounted.
                </p>
                {FILESYSTEM_MODELS.map((m) => (
                  <Label
                    key={m.value}
                    className={`flex pointer-coarse:min-h-[var(--target-touch-minimum)] items-start gap-[var(--space-2)] p-[var(--space-2)] rounded-md border cursor-pointer transition-colors ${
                      currentFsModel === m.value
                        ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/5'
                        : 'border-[var(--color-border)] hover:bg-[var(--color-surface-2)]'
                    }`}
                  >
                    {/* U8-A / FR-070: coarse-pointer tap area around the glyph (globals.css [data-radio-target]). */}
                    <span data-radio-target="" className="relative mt-[var(--space-0-5)] inline-flex shrink-0">
                      <input
                        tabIndex={0}
                        type="radio"
                        name="filesystem-model"
                        value={m.value}
                        checked={currentFsModel === m.value}
                        onChange={() => handleFilesystemModelChange(m.value)}
                        className="accent-[var(--color-accent)]"
                        aria-label={`Filesystem model: ${m.label}`}
                        data-testid={`sandbox-fs-model-${m.value}`}
                      />
                    </span>
                    <div className="flex-1 min-w-0">
                      <p className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">{m.label}</p>
                      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-snug">{m.desc}</p>
                    </div>
                  </Label>
                ))}
                <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
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
            className="space-y-[var(--space-2)] border-t border-[var(--color-border)] pt-[var(--space-3)]"
            data-testid="workspace-limit-section"
            {...settingsRow('Shell workspace limit')}
          >
            <p className="flex flex-wrap items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)]">
              Shell workspace limit
              <RestartBadge />
              <PasswordBadge />
            </p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-snug">
              A different boundary from the sandbox above, with its own setting. The sandbox is an
              operating-system protection wrapped around the programs an agent starts. This is a
              check Omnipus makes on the command text itself, before anything starts: it decides
              whether a shell command may name a file outside the agent&rsquo;s working folder.
              Turning the sandbox off does not turn this off.
            </p>

            {configLoading ? (
              <div className="space-y-[var(--space-2)] animate-pulse">
                <div className="h-3 w-3/4 rounded bg-[var(--color-border)]" />
                <div className="h-3 w-1/2 rounded bg-[var(--color-border)]" />
              </div>
            ) : !workspaceLimitSupported ? (
              <div
                className="flex items-start gap-[var(--space-2)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] p-[var(--space-2)]"
                role="status"
                data-testid="workspace-limit-unavailable"
              >
                <Warning
                  size={14}
                  weight="fill"
                  style={{ color: 'var(--color-muted)' }}
                  className="mt-[var(--space-0-5)] shrink-0"
                />
                <p className="text-[length:var(--type-utility-xs-size)] leading-relaxed text-[var(--color-secondary)]">
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
              <fieldset className="space-y-[var(--space-2)]">
                <legend className="sr-only">Shell workspace limit</legend>
                {WORKSPACE_LIMIT_OPTIONS.map((o) => {
                  const selected = currentWorkspaceLimit === (o.value === 'on')
                  return (
                    <Label
                      key={o.value}
                      className={`flex pointer-coarse:min-h-[var(--target-touch-minimum)] items-start gap-[var(--space-2)] p-[var(--space-2)] rounded-md border cursor-pointer transition-colors ${
                        selected
                          ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/5'
                          : 'border-[var(--color-border)] hover:bg-[var(--color-surface-2)]'
                      }`}
                    >
                      {/* U8-A / FR-070: coarse-pointer tap area around the glyph (globals.css [data-radio-target]). */}
                      <span data-radio-target="" className="relative mt-[var(--space-0-5)] inline-flex shrink-0">
                        <input
                          tabIndex={0}
                          type="radio"
                          name="workspace-limit"
                          value={o.value}
                          checked={selected}
                          onChange={() => handleWorkspaceLimitChange(o.value === 'on')}
                          className="accent-[var(--color-accent)]"
                          aria-label={`Shell workspace limit: ${o.label}`}
                          data-testid={`sandbox-workspace-limit-${o.value}`}
                        />
                      </span>
                      <div className="flex-1 min-w-0">
                        <p className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">{o.label}</p>
                        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-snug">{o.desc}</p>
                      </div>
                    </Label>
                  )
                })}
              </fieldset>
            )}
          </div>

          {/* ── Paths / SSRF editor ── */}
          <div className="space-y-[var(--space-3)] border-t border-[var(--color-border)] pt-[var(--space-3)]">
            <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)]">Sandbox configuration</p>

            {configLoading ? (
              <div className="space-y-[var(--space-2)] animate-pulse">
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
                  badges={
                    <>
                      <RestartBadge />
                      <PasswordBadge />
                    </>
                  }
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
                  badges={<PasswordBadge />}
                />

                {saveError && (
                  <p role="alert" className="text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]">
                    {saveError}
                  </p>
                )}
                {sandboxOutcome}
              </>
            )}
          </div>
        </Card>
      )}

      {/* Wildcard SSRF confirmation modal */}
      <ConfirmDialog
        open={showWildcardModal}
        onOpenChange={(open) => { if (!open) cancelPendingDraft() }}
        title="Disable SSRF protection?"
        description="Allowing every address lets agents reach any machine on your network and this server's own services, such as its admin pages."
        confirmLabel="Allow every address"
        destructive
        onConfirm={confirmPendingDraft}
      />

      {/* ABI v4 enforce-mode confirmation, asked when Enforce is picked */}
      <ConfirmDialog
        open={showEnforceModal}
        onOpenChange={(open) => { if (!open) cancelPendingDraft() }}
        title="Kernel incompatibility warning"
        description={
          statusData && typeof statusData.abi_version === 'number' && statusData.abi_version >= 4
            ? `This server's kernel (Landlock ABI v${statusData.abi_version}) cannot apply every Enforce rule. With Enforce the gateway may stop at its next start.`
            : "Enforce may not work with this server's kernel."
        }
        cancelLabel="Keep the current mode"
        confirmLabel="Choose Enforce anyway"
        onConfirm={confirmPendingDraft}
      />

      {ownBar}
    </section>
  )
}
