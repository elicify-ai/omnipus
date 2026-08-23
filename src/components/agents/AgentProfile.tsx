import { useState, useEffect, useRef, useMemo } from 'react'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import {
  X,
  CaretDown,
  CaretUp,
  Sparkle,
  Star,
  Lightning,
  ArrowUp,
  ArrowDown,
  Warning,
  Lock,
  WarningCircle,
  Trash,
  Brain,
} from '@phosphor-icons/react'
import { useAutoSave } from '@/hooks/useAutoSave'
import { useFocusRestore } from '@/hooks/useFocusRestore'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { ModelSelector } from '@/components/ui/model-selector'
import { useModelToProvider } from '@/lib/agents/modelToProvider'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import {
  Accordion,
  AccordionItem,
  AccordionTrigger,
  AccordionContent,
} from '@/components/ui/accordion'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { ToolsAndPermissions } from './ToolsAndPermissions'
import { ShellDenyPatternsEditor } from './ShellDenyPatternsEditor'
import { ExecutorSelector } from './ExecutorSelector'
import { BehaviorFields, AvatarColorPicker, IconPicker, AvatarHeader, UploadMdButton } from './AgentFormFields'
import { CliPathValidationHint } from './CliPathValidationHint'
import { CommandPreview } from './CommandPreview'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import {
  fetchAgent,
  fetchWorkspace,
  updateAgent,
  updateWorkspace,
  deleteAgent,
  fetchProviders,
  fetchActivity,
  fetchSkills,
  testAgentRunner,
  workspacesQueryKeys,
  type ActivityEvent,
  type AgentToolsCfg,
  type Skill,
  type ExecutorConfig,
  type ExecutorCommandPreviewRequest,
  type WorkspaceMemberConfig,
} from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import { formatTokens } from '@/lib/formatTokens'
import { logDiagnostic } from '@/lib/telemetry'
import { useUiStore } from '@/store/ui'
import type { FallbackModel } from '@/lib/api/generated/openapi-types'
import { type IconName, getIconComponent } from '@/lib/agentIcons'
import { avatarColorName } from '@/lib/constants'
import { agentKindFlags } from '@/lib/agentKind'
import { cliValidationBlocked, useCliPathValidation } from '@/hooks/useCliPathValidation'
import { useCliDetect } from '@/hooks/useCliDetect'
import { buildExecutorPreviewRequest } from '@/hooks/useCommandPreview'
import { detectEntryFor, resolveCliDetectHint } from '@/lib/cliDetect'
import { CONTEXT_WINDOW_SOURCE_LABEL } from '@/components/settings/ContextSection'

/** Editor's fallback entry — `FallbackModel` from the contract with `provider` narrowed to required (the editor always populates it at hydration). */
type FallbackEntry = FallbackModel & { provider: string }

/**
 * Echo-race fix, item 3 (belt-and-braces): true when the currently focused
 * element is a text input/textarea inside THIS profile's own slide-over.
 * Used only to add a second, independent line of defense around the
 * hydration effect's primary `isDirtyRef` guard — a hydration must never
 * clobber text the operator is actively typing into right now, even in a
 * hypothetical gap where `isDirtyRef` was not (yet) set for the field being
 * typed into.
 *
 * Item 11 (focus-check scoping): scoped via a React ref to THIS instance's
 * own SheetContent DOM node — passed in by the caller — rather than
 * `document.querySelector('[data-testid="agent-profile-sheet"]')`, which
 * would resolve to the FIRST matching element in the whole document
 * regardless of which AgentProfile instance owns it. The sheet renders
 * through a Radix portal, but a ref still resolves to the real DOM node
 * wherever it's portaled to, so scoping this way works cleanly.
 */
function isFocusedInAgentProfileForm(sheetEl: HTMLElement | null): boolean {
  if (typeof document === 'undefined') return false
  const active = document.activeElement
  if (!active || (active.tagName !== 'INPUT' && active.tagName !== 'TEXTAREA')) return false
  return !!sheetEl && sheetEl.contains(active)
}

interface AgentProfileProps {
  /**
   * Explicit agent id (wins over the store-driven `editAgentId`). Used by
   * tests and direct renders; the primary path is the UI store.
   */
  agentId?: string
}

// ADR-066 D9: context-window sizes render EXACT with thousands separators
// (1,048,576) — unlike the abbreviated `formatTokens` ("1.0M") used for
// usage stats, an operator comparing an override against a clamp needs the
// real number.
const windowFormatter = new Intl.NumberFormat('en-US')
function formatWindowTokens(n: number): string {
  return windowFormatter.format(n)
}

export function AgentProfile({ agentId: agentIdProp }: AgentProfileProps = {}) {
  const editAgentId = useUiStore((s) => s.editAgentId)
  const closeEditAgentSlideOver = useUiStore((s) => s.closeEditAgentSlideOver)
  const addToast = useUiStore((s) => s.addToast)
  // FR-018 / A5: workspace context for the conditional Heartbeat tab (US-5).
  // Set when the slide-over is opened from a Team tab; null on the global
  // Agents screen, which suppresses the Heartbeat tab (FR-016 / FR-025).
  const editAgentWorkspaceId = useUiStore((s) => s.editAgentWorkspaceId)
  const agentId = agentIdProp ?? editAgentId
  const isOpen = agentId !== null

  const queryClient = useQueryClient()

  const { data: agent, isLoading, isError, error: agentError, refetch: refetchAgent } = useQuery({
    queryKey: ['agent', agentId],
    queryFn: () => fetchAgent(agentId as string),
    enabled: agentId !== null,
  })

  const { data: providers = [], isError: providersError } = useQuery({
    queryKey: ['providers'],
    queryFn: fetchProviders,
  })

  const { data: allActivityResp, isError: activityError } = useQuery({
    queryKey: ['activity'],
    queryFn: fetchActivity,
    staleTime: 30_000,
  })
  // GET /activity returns ActivityEventsResponse ({ events, warning? }), not a
  // bare array — unwrap to the events list for the filter/slice below.
  const allActivity = allActivityResp?.events ?? []


  // US-E6: fetch available (installed) skills so the picker can show them.
  const { data: availableSkills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    queryFn: fetchSkills,
    staleTime: 60_000,
  })

  // FR-016 / US-5: fetch the workspace when opened from a Team tab, so the
  // Heartbeat tab can read + write member_configs for this (workspace, agent).
  // Disabled when there is no workspace context (global Agents screen) — in
  // that case the Heartbeat tab is hidden and no fetch is needed.
  // M-5 / fix-1: use the canonical workspacesQueryKeys.detail key so the
  // onSuccess setQueryData in saveHeartbeatMutation lands on the same cache
  // entry this tab reads (prior singular 'workspace' key caused a cache miss).
  // fix-1 (DATA-LOSS guard): also destructure isError + isLoading so the
  // Heartbeat tab can block Save when the workspace failed to load or is still
  // loading — preventing a save with an empty member_configs base that would
  // wipe every other member's heartbeat config.
  const {
    data: workspaceData,
    isError: isWorkspaceError,
    isLoading: isWorkspaceLoading,
  } = useQuery({
    queryKey: editAgentWorkspaceId
      ? workspacesQueryKeys.detail(editAgentWorkspaceId)
      : workspacesQueryKeys.detail('__none__'),
    queryFn: () => fetchWorkspace(editAgentWorkspaceId as string),
    enabled: !!editAgentWorkspaceId,
    staleTime: 30_000,
  })

  const recentActivity = allActivity
    .filter((e) => e.agent_id === agentId)
    .slice(0, 5)

  const connectedProviders = providers.filter((p) => p.status === 'connected')
  const availableModels = connectedProviders.flatMap((p) => p.models ?? [])
  const providerGroups = connectedProviders
    .filter((p) => (p.models ?? []).length > 0)
    .map((p) => ({ providerName: p.display_name ?? p.name ?? p.id, providerId: p.id, models: p.models ?? [] }))

  // W6-C2 / I9: model → provider-id lookup. Extracted to
  // `src/lib/agents/modelToProvider.ts` so the same helper is
  // reusable + unit-testable. Returns the **provider id** (the wire
  // routing key per FR-007 / FallbackModel.yaml), NOT the display
  // name — pre-C2 the editor was storing `display_name ?? name ?? id`
  // and emitting that to the wire, which silently downgraded
  // `provider` to a brand label and broke runtime resolution.
  const { lookup: modelToProvider } = useModelToProvider(connectedProviders)

  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  // Finding A (7-reviewer-gate, fix/uat-v0.1.1-defects): captured (mid-render,
  // synchronously, in the Finding-B `prevAgentIdForReset` block below) with
  // whichever agentId we are switching AWAY from, the instant a switch is
  // detected — BEFORE `prevAgentIdForReset` itself is overwritten to the new
  // value. Read later by the `onDisabledWithPendingChanges` callback passed
  // to `useAutoSave()` (a plain ref read, not a closure — the callback's OWN
  // closure would otherwise observe the ALREADY-caught-up `prevAgentIdForReset`
  // by the time the hook's effect actually invokes it, since that effect
  // fires on the "restarted" render triggered by the very same mid-render
  // `setPrevAgentIdForReset` call).
  const switchedAwayFromAgentIdRef = useRef<string | null>(null)

  // Item 11: ref to THIS instance's own SheetContent DOM node, so the
  // focus-check belt-and-braces guard (`isFocusedInAgentProfileForm`) scopes
  // to the right sheet instead of a document-wide first-match query.
  const sheetContentRef = useRef<HTMLDivElement>(null)

  // Tracks whether the initial hydration from the server has completed.
  // Guards auto-save from firing with default (empty) state before the first fetch resolves.
  const hasHydrated = useRef(false)

  // D3 / UAT spurious-PUT fix: reactive counterpart to `hasHydrated` above.
  // A `useRef` read at render time is NOT reactive — useAutoSave's own
  // "skip first render" baseline-capture logic runs off its `disabled`
  // option, gated purely by React's commit/effect cycle, so the flag it
  // reads has to be component STATE, flipped at the END of the hydration
  // effect below, so the exact commit where the hydrated field values land
  // is also the commit where `disabled` flips false. That's the only way
  // useAutoSave captures the HYDRATED data as its baseline instead of the
  // hardcoded useState defaults every field below starts at (name='',
  // description='', model='', ...) before the agent's real data has
  // loaded — the root cause of the spurious-PUT bug (mount → hydrate →
  // unsolicited PUT echoing the server's own data back). Reset to false in
  // the `[agentId]` effect below — required because this component
  // persists across agent switches (it does NOT unmount on sheet close —
  // see `handleCloseAgentSheet`'s comment), so without the reset the NEXT
  // agent inherits "ready" a render too early and reproduces the same bug
  // on every subsequent agent open.
  const [formHydrated, setFormHydrated] = useState(false)

  // I12: cache the executor kind+cli+cli_path we've already passed the
  // runner-test for, so the auto-save debounce (500 ms) doesn't re-fire the
  // test on every unrelated keystroke while the user has external-cli
  // selected. We re-test when the kind, cli, OR cli_path changes (or the
  // agent id changes) — code-review m2: `cli_path` MUST be part of the
  // signature, otherwise a cli_path-only edit (kind/cli unchanged) would
  // never re-trigger the server-side runner test before the new path
  // persists, silently autosaving an unverified path.
  type ExecutorSig = { kind: ExecutorConfig['kind']; cli?: ExecutorConfig['cli']; cli_path?: string; agentId: string | null }
  const testedExecutorSig = useRef<ExecutorSig | null>(null)

  // I2: conflict guard. Set in the 409 Conflict catch and cleared once the
  // refetchAgent() triggered by the "Refresh" toast action lands. While set,
  // the saveFn early-returns so subsequent debounced saves don't fire (and
  // just 409 again) against the stale local state — the form is re-hydrated
  // from the server's current row before saves resume.
  const conflictRef = useRef(false)

  // UAT data-loss fix (fallback_models passive-repro, round 2): tracks the
  // `updated_at` of the most recent `agent` snapshot we've actually
  // incorporated into form state — via either the initial hydration or our
  // own last successful save. The hydration effect below (which normally
  // fires whenever the `agent` object reference changes and the form is
  // not dirty) uses this to reject a hydration source that is NOT strictly
  // newer than what we already have.
  //
  // Why this is needed even after making the save-success `setQueryData`
  // patch use the FULL PUT response (see that call site's comment): the
  // very next line still calls `queryClient.invalidateQueries(['agent',
  // agentId])` to reconcile with a fresh GET. If THAT refetch resolves
  // with a snapshot that is not newer than what this save already applied
  // — a stale response racing in from network reordering, a read-replica
  // lag, or (as a live regression test caught) simply a caller whose GET
  // mock/cache hasn't caught up yet — the `agent` reference still changes,
  // `isDirtyRef.current` is STILL already `false` (cleared by this same
  // save's own success path), and the hydration effect would otherwise
  // re-hydrate from that stale snapshot and revert the field just saved —
  // reopening the exact same class of silent data loss one level further
  // out. Comparing `updated_at` timestamps closes the window regardless of
  // WHERE the stale `agent` reference came from.
  const lastIncorporatedUpdatedAtRef = useRef<string | undefined>(undefined)

  // Hydrate heartbeat state from workspace member_configs when the workspace
  // data loads (or when we navigate to a different agent or workspace).
  useEffect(() => {
    if (!workspaceData || !agentId) return
    if (hbDirtyRef.current) return // Don't overwrite in-flight edits
    const cfg: WorkspaceMemberConfig | undefined =
      workspaceData.member_configs?.[agentId]
    const hb = cfg?.heartbeat
    const interval = hb?.interval_minutes ?? 30
    setHbEnabled(hb?.enabled ?? false)
    setHbIntervalMinutes(interval)
    setHbIntervalDraft(String(interval))
    setHbBody(hb?.body ?? '')
  }, [workspaceData, agentId])

  // W6-B1 / I7 (WCAG 2.4.3): restore focus to the element that triggered the
  // slide-over (typically the AgentCard button — also covers the
  // /agents/:id route mount) on close.
  //
  // Wave 6 / B-fix: extracted to `useFocusRestore` hook so the same
  // proven pattern (capture-in-onOpenAutoFocus + restore-via-RAF + try/
  // catch) is shared with the modal in CreateAgentModal. Future Wave C
  // consumers (ModelFooter slide-over) will adopt the hook instead of
  // forking this block.
  const { onOpenAutoFocus: handleOpenAutoFocus } = useFocusRestore(isOpen)

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [model, setModel] = useState('')
  // O3 two-field: explicit provider routing key for the primary model.
  // Paired with model via onPairChange on the ModelSelector. Empty = resolve
  // via default provider (back-compat with pre-O3 agents).
  const [primaryProvider, setPrimaryProvider] = useState('')
  const [selectedColor, setSelectedColor] = useState<string | undefined>(undefined)
  const [selectedIcon, setSelectedIcon] = useState<IconName>('Robot')
  // W6-B4 / G3: `default` flag mirrors Agent.default on the wire. At most one
  // agent is default across the roster; the backend enforces that on PUT.
  // The toggle in the Identity strip is the only way to flip it from the
  // Edit profile (previously, users had to go back to the Agents list).
  const [isDefault, setIsDefault] = useState(false)
  // Fallback chain — the wire shape is `[{model, provider}]`; the editor
  // tracks it 1:1 with `provider` always populated (see FallbackEntry
  // type). Provider is needed for the chip badge; the wire payload in
  // `formData` below emits the same shape.
  const [fallbackModels, setFallbackModels] = useState<FallbackEntry[]>([])
  const [temperature, setTemperature] = useState(1.0)
  const [maxTokens, setMaxTokens] = useState(4096)
  const [topP, setTopP] = useState(1.0)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [useGlobalRateLimits, setUseGlobalRateLimits] = useState(true)
  const [maxLlmCallsPerHour, setMaxLlmCallsPerHour] = useState<number | ''>('')
  const [maxToolCallsPerMinute, setMaxToolCallsPerMinute] = useState<number | ''>('')
  const [maxCostPerDay, setMaxCostPerDay] = useState<number | ''>('')
  const [soul, setSoul] = useState('')
  // ADR-052 FR-039: per-agent memory-injection gate. Defaults to true (the
  // wire default for ordinary agents); the seeded Judge (and any future
  // verifier-role System Agent) is seeded false server-side and re-enforced
  // on every boot — reproducible, impartial verdicts require the same
  // evidence to always yield the same verdict, which injected memory would
  // undermine. "Allowed on all agents" per AgentUpdateRequest.yaml, so this
  // is NOT stripped from the locked-agent payload below like soul/name/etc.
  const [memoryEnabled, setMemoryEnabled] = useState(true)
  // W6-B4 / G1: per-agent persona voice identifier (TTS voice name or model ID).
  // Schema-pinned on Agent.voice; not active until v0.2.0 TTS. Empty string
  // means "not configured" — the wire payload omits the field entirely.
  const [voice, setVoice] = useState('')
  const [timeoutSeconds, setTimeoutSeconds] = useState(0)
  const [maxToolIterations, setMaxToolIterations] = useState(200)
  // Draft strings for the two number inputs. A controlled number input backed
  // directly by the committed number turns "clear the field to type" into
  // Number('') === 0 — and because this form AUTO-SAVES on state change, the 0
  // was persisted mid-keystroke (P0, 2026-07-03: five agents + the global
  // default were zero-clobbered on a live install). The draft absorbs
  // in-progress typing; only a VALID value commits (and autosaves); blur with
  // an invalid/empty draft restores the last committed value.
  const [timeoutDraft, setTimeoutDraft] = useState('0')
  const [maxToolIterationsDraft, setMaxToolIterationsDraft] = useState('200')
  // ADR-066 D9 (T068-30): per-agent context-window override (D2 rung 1,
  // lower-only — the backend clamps to the model's capability). Three-valued
  // on purpose: `undefined` = the wire omitted it and the operator has not
  // touched it (the PUT omits the field → backend leaves it unchanged);
  // `null` = the operator cleared it (the PUT sends null → backend clears);
  // a number = set. Same draft pattern as the two inputs above so clearing
  // the field to type never autosaves 0 (the wire minimum is 1).
  const [contextWindowOverride, setContextWindowOverride] = useState<number | null | undefined>(undefined)
  const [contextWindowOverrideDraft, setContextWindowOverrideDraft] = useState('')
  // Wave 5 / spec §6.1 BDD #15: Edit slide-over footer Delete agent.
  // Opens an AlertDialog; the confirm mutation invalidates the list and
  // closes the slide-over. Locked agents do not render the trigger.
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [toolsCfg, setToolsCfg] = useState<AgentToolsCfg>({
    builtin: { policies: {} },
  })
  // US-E6: per-agent skill assignment (opt-in, default none).
  const [agentSkills, setAgentSkills] = useState<string[]>([])
  const [shellDenyPatterns, setShellDenyPatterns] = useState<string[]>([])
  const [shellAdvancedOpen, setShellAdvancedOpen] = useState(false)
  // Spec-4 FR-4.1: sub-agent executor (native default / external-cli / remote-a2a).
  const [executor, setExecutor] = useState<ExecutorConfig | undefined>(undefined)

  // external-executor-cli-path-detection spec (ADR-030) / US-5: same
  // detect→prefill + validate-on-blur as the create wizard, because
  // `cli_path` is mutable on edit (`cli` itself stays locked). Detection is
  // shared with the create wizard via `useCliDetect` (Simplify F1), probed
  // once while the Runtime tab's agent is a subagent_3p; `cliValidation`
  // drives the inline status line and gates autosave the same way the
  // wizard gates Create (FR-008/FR-018/FR-019).
  const cliDetect = useCliDetect(agent?.type === 'subagent_3p')
  const cliValidation = useCliPathValidation()

  // FR-016 / US-5: Heartbeat tab state — per-(workspace, agent) config, NOT
  // part of the agent autosave. Only meaningful when editAgentWorkspaceId is set.
  const [hbEnabled, setHbEnabled] = useState(false)
  const [hbIntervalMinutes, setHbIntervalMinutes] = useState(30)
  // Draft string for the interval input (mirrors `timeoutDraft`/
  // `maxToolIterationsDraft` above) — a controlled number input backed
  // directly by the committed number turned "clear the field to retype"
  // into an immediate snap-to-5 on every keystroke (Number('') coerced via
  // the old `raw === ''` branch), which fought the operator mid-edit: clear
  // "10" meaning to type "15" and the field already reads "5" before the "1"
  // even lands, producing "51" instead. The draft absorbs in-progress
  // typing; only a valid (>= 5) value commits, and blur restores the last
  // committed value into the draft.
  const [hbIntervalDraft, setHbIntervalDraft] = useState('30')
  const [hbBody, setHbBody] = useState('')
  // Tracks whether the heartbeat form has been modified since last save.
  const hbDirtyRef = useRef(false)
  const markHbDirty = () => { hbDirtyRef.current = true }

  // Finding B (7-reviewer-gate, fix/uat-v0.1.1-defects): reset flags when
  // navigating to a different agent — via a MID-RENDER state adjustment
  // (https://react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes),
  // NOT a plain `useEffect(..., [agentId])` as this used to be.
  //
  // Root cause the plain-effect version had: it and the hydration effect
  // below (`[agentId, agent]`, which flips `formHydrated` back to `true`)
  // are BOTH passive effects. On an ordinary agent switch where `agent` is
  // NOT yet cached (fetched fresh), TanStack Query returns `undefined` for
  // at least one render, so the hydration effect's `if (!agent) return`
  // guard bails on that first pass — the reset effect's `setFormHydrated(false)`
  // commits on its own, `disabled` (`!formHydrated`) genuinely renders as
  // `true`, and useAutoSave's `wasDisabledRef` re-arm (see useAutoSave.ts)
  // correctly engages once the hydration effect flips it back to `true`
  // later. But on a REVISIT of an agent whose data is ALREADY in the
  // QueryClient cache — e.g. A→B→A — `agent` resolves SYNCHRONOUSLY on the
  // very same render `agentId` changes on. Both effects then fire back to
  // back in the SAME passive-effect flush with no commit in between:
  // `setFormHydrated(false)` immediately followed by `setFormHydrated(true)`
  // batch into a single re-render, so `disabled` never actually commits as
  // `true` — useAutoSave's re-arm never engages, and the revisited agent's
  // freshly-(re)hydrated data is diffed against the PREVIOUS agent's stale
  // baseline: indistinguishable from a genuine edit, arming the debounce and
  // firing a spurious echo PUT. Confirmed empirically — see
  // `AgentProfile.test.tsx`'s "Finding B" regression test (fails without
  // this fix, passes with it).
  //
  // A mid-render `setFormHydrated(false)` call makes React discard and
  // restart the CURRENT render immediately, before ANY effect for this
  // commit has run — so the reset is guaranteed to land in its own real,
  // committed render (with `disabled === true`) strictly BEFORE the
  // hydration effect (a passive effect, which can only run after a commit)
  // has any chance to flip it back. This closes the coalescing window
  // regardless of whether `agent` happens to be cache-resolved or not.
  // Precedent: `WorkspaceSettingsTab.tsx`'s `prevWorkspaceIdForReset` /
  // `identityHydrated` pair uses the exact same pattern for the same reason.
  //
  // Positioned here (after every ref/state it touches has been declared,
  // not up near `formHydrated`'s own declaration) because a mid-render
  // mutation executes synchronously as this function runs top-to-bottom —
  // unlike the refs/effects above and below, which only ever read these via
  // deferred closures, so their textual position relative to a `const`
  // declared later in the same render doesn't matter.
  const [prevAgentIdForReset, setPrevAgentIdForReset] = useState(agentId)
  if (agentId !== prevAgentIdForReset) {
    // Finding A: capture the OLD agentId before it's overwritten below —
    // see `switchedAwayFromAgentIdRef`'s own doc comment above.
    switchedAwayFromAgentIdRef.current = prevAgentIdForReset
    setPrevAgentIdForReset(agentId)
    isDirtyRef.current = false
    hasHydrated.current = false
    conflictRef.current = false
    hbDirtyRef.current = false
    lastIncorporatedUpdatedAtRef.current = undefined
    // D3 fix: re-arm the hydration-readiness gate for the new agent — see
    // `formHydrated`'s own doc comment above for why this reset is required.
    setFormHydrated(false)
  }

  useEffect(() => {
    if (!agent) return
    // Echo-race fix, item 3 (belt-and-braces): never let a hydration reset
    // text the operator is actively typing into RIGHT NOW while our own
    // save cycle is what triggered it. Independent of (and checked ahead
    // of) the isDirtyRef guard immediately below — also covers a save that
    // is still in flight (`saveStatusRef.current === 'saving'`), where a
    // field the operator hasn't touched yet could otherwise still be
    // clobbered mid-keystroke by a same-tick cache patch from a DIFFERENT
    // field's save. Keep this ahead of, not instead of, the isDirtyRef
    // check — it deliberately duplicates `isDirtyRef.current` in its own
    // condition so it stays correct even if the primary guard below is
    // ever refactored.
    if (isFocusedInAgentProfileForm(sheetContentRef.current) && (isDirtyRef.current || saveStatusRef.current === 'saving')) return
    // isDirtyRef prevents background refetch from overwriting unsaved user edits.
    // We depend on the stable agentId prop (not agent?.id which can be undefined
    // during loading) so the effect re-runs reliably on agent navigation.
    if (isDirtyRef.current) return
    // UAT data-loss fix (fallback_models passive-repro, round 2): reject a
    // hydration source that is NOT strictly newer than what we've already
    // incorporated (see `lastIncorporatedUpdatedAtRef`'s doc comment above
    // for the exact race this closes — a same-tick optimistic cache patch
    // or an `invalidateQueries` refetch resolving with a snapshot no newer
    // than the save that just landed, either of which would otherwise
    // silently revert a just-saved field). A missing `updated_at` on
    // either side (e.g. brand-new agent, or a fixture/test double that
    // doesn't model it) falls through to hydrate as before — this guard
    // only ever SKIPS, never blocks the very first hydration.
    if (lastIncorporatedUpdatedAtRef.current !== undefined && agent.updated_at !== undefined) {
      const incomingTime = new Date(agent.updated_at).getTime()
      const incorporatedTime = new Date(lastIncorporatedUpdatedAtRef.current).getTime()
      // 7-reviewer-gate fix (3 independent reviewers): fail CLOSED, not
      // open, when either timestamp fails to parse. `NaN <= NaN` (and any
      // comparison involving NaN) evaluates to `false` in JS, so the
      // original `new Date(x).getTime() <= new Date(y).getTime()` check
      // would silently PROCEED to hydrate whenever either side was
      // malformed/unparseable — backwards for a guard whose entire purpose
      // is "never silently revert to stale data". When we can't confidently
      // order the two snapshots we reject the hydration (and warn loudly)
      // rather than gamble on it being newer.
      if (Number.isNaN(incomingTime) || Number.isNaN(incorporatedTime)) {
        console.warn('agentProfile.updatedAtGuardUnparseable', {
          agentId,
          incoming: agent.updated_at,
          incorporated: lastIncorporatedUpdatedAtRef.current,
        })
        logDiagnostic('agentProfileUpdatedAtGuardUnparseable', {
          agentId,
          incoming: agent.updated_at,
          incorporated: lastIncorporatedUpdatedAtRef.current,
        })
        return
      }
      if (incomingTime === incorporatedTime) {
        // Wave-3 hotfix: an incoming snapshot EQUAL to what's already
        // incorporated is not a conflict — it's the expected echo of an
        // `invalidateQueries` background refetch (or the save-success
        // `setQueryData` patch above) resolving with the exact same row
        // this component already applied. It used to fall into the "<="
        // branch below and log a rejected-hydration telemetry error on
        // every profile open and after every successful autosave — a false
        // positive with no real conflict. Skip re-hydrating (nothing
        // changed) silently; only a STRICTLY older incoming snapshot is a
        // genuine race worth a breadcrumb.
        return
      }
      if (incomingTime < incorporatedTime) {
        // 7-reviewer-gate fix: this guard used to be a bare early-`return`
        // with no signal when it actually rejected a hydration. If this
        // guard ever incorrectly rejects a legitimate hydration (e.g. clock
        // skew between client and server), the UI would otherwise silently
        // keep showing stale data with zero indication — exactly the
        // "looks fine but is actually outdated" failure mode this whole
        // track exists to prevent. Always leave a breadcrumb.
        console.warn('agentProfile.updatedAtGuardRejectedHydration', {
          agentId,
          incoming: agent.updated_at,
          incorporated: lastIncorporatedUpdatedAtRef.current,
        })
        logDiagnostic('agentProfileUpdatedAtGuardRejectedHydration', {
          agentId,
          incoming: agent.updated_at,
          incorporated: lastIncorporatedUpdatedAtRef.current,
        })
        return
      }
    }
    lastIncorporatedUpdatedAtRef.current = agent.updated_at
    setName(agent.name ?? '')
    setDescription(agent.description ?? '')
    setModel(agent.model ?? '')
    // O3 two-field: hydrate the explicit provider routing key.
    setPrimaryProvider(agent.provider ?? '')
    setSelectedColor(agent.color)
    setSelectedIcon((agent.icon as IconName) ?? 'Robot')
    // W6-B4 / G3: hydrate the `default` flag from the agent response. The
    // wire field is a plain boolean; absent = false. The Identity strip
    // shows the current state of this flag and lets the user flip it.
    setIsDefault(agent.default ?? false)
    // Hydrate the fallback chain from the wire. `provider` is optional on
    // the wire type; narrow to required by looking up via modelToProvider,
    // else empty string (renders as a muted dash in the chip).
    const rawFallbacks = agent.fallback_models ?? []
    setFallbackModels(
      rawFallbacks.map((entry) => ({
        ...entry,
        provider: entry.provider || modelToProvider(entry.model) || '',
      })),
    )
    setTemperature(agent.model_params?.temperature ?? 1.0)
    setMaxTokens(agent.model_params?.max_tokens ?? 4096)
    setTopP(agent.model_params?.top_p ?? 1.0)
    setUseGlobalRateLimits(agent.rate_limits?.use_global_defaults ?? true)
    setMaxLlmCallsPerHour(agent.rate_limits?.max_llm_calls_per_hour ?? '')
    setMaxToolCallsPerMinute(agent.rate_limits?.max_tool_calls_per_minute ?? '')
    setMaxCostPerDay(agent.rate_limits?.max_cost_per_day ?? '')
    setSoul(agent.soul ?? '')
    // ADR-052 FR-039: the wire field is required (non-nullable) on GET, but
    // hydrate defensively for any test double / legacy snapshot that omits it.
    setMemoryEnabled(agent.memory_enabled ?? true)
    // W6-B4 / G1: hydrate the persona voice. The wire field is nullable;
    // `null` and absent both render as the empty string in the input.
    // W6-B-fix: trim existing whitespace on disk so the input never
    // renders 3 spaces and the wire round-trip is clean (whitespace was
    // silently reaching the server).
    setVoice((agent.voice ?? '').trim())
    setTimeoutSeconds(agent.timeout_seconds ?? 0)
    setTimeoutDraft(String(agent.timeout_seconds ?? 0))
    setMaxToolIterations(agent.max_tool_iterations ?? 200)
    setMaxToolIterationsDraft(String(agent.max_tool_iterations ?? 200))
    setContextWindowOverride(agent.context_window_override ?? undefined)
    setContextWindowOverrideDraft(agent.context_window_override != null ? String(agent.context_window_override) : '')
    setShellDenyPatterns(agent.shell_policy?.custom_deny_patterns ?? [])
    // Spec-4: hydrate executor (absent → native default, modelled as undefined).
    setExecutor(agent.executor)
    if (agent.tools_cfg) setToolsCfg((prev) => ({
      builtin: agent.tools_cfg?.builtin ?? prev.builtin,
      mcp: agent.tools_cfg?.mcp as AgentToolsCfg['mcp'],
    }))
    // US-E6: hydrate agent skills from the API response (default none).
    setAgentSkills(agent.skills ?? [])
    hasHydrated.current = true
    // D3 fix: flip the reactive readiness flag as the LAST line of this
    // effect, after every setState call above — React batches all of these
    // into a single commit, so `formHydrated` becomes true in the exact
    // same commit the hydrated field values land. See `formHydrated`'s own
    // doc comment (near `hasHydrated`'s declaration) for the full rationale.
    setFormHydrated(true)
  }, [agentId, agent])

  // US-1/US-5 AC-1: OFFER the detected absolute path when cli_path is EMPTY —
  // purely derived (no state, no effect), so detection can NEVER itself mark
  // the form dirty or commit into `executor`/`formData`. code-review M2a:
  // the previous effect called `setExecutor` + `markDirty()` as soon as
  // detection resolved, which meant merely OPENING a subagent_3p profile
  // (before the operator touched anything) silently autosaved the detected
  // path. The offered value is only committed into `executor.cli_path` when
  // the operator actually edits the field (onChange) or leaves it with the
  // offered value still showing (onBlur "confirms" it) — see the Input
  // below. Never overwrites a non-empty value (US-4).
  const detectedCliEntry = agent?.type === 'subagent_3p' ? detectEntryFor(cliDetect, executor?.cli) : undefined
  const detectedCliPath = detectedCliEntry?.installed && detectedCliEntry.path ? detectedCliEntry.path : undefined
  const offeredCliPath = (executor?.cli_path ?? '').trim().length === 0 ? detectedCliPath : undefined

  // Live command-line preview (executor-command-preview): built from the
  // same executor state the Runtime tab edits, plus the agent's top-level
  // `model` (ExecutorConfig carries no model field on the wire — the model
  // an external CLI runs with is `AgentConfig.model`, the same field every
  // agent type uses). `runtimePanel` (below) only ever renders for
  // subagent_3p agents (`isExternalAgent`), and `formData` above shows that
  // tier EXCLUDES `max_tool_iterations` from the wire payload entirely
  // (agent-types-field-matrix.md Decisions #1 — the external CLI runs its
  // own tool loop) — so it is deliberately omitted here too, letting the
  // preview fall back to the same server-side default (50) real dispatch
  // uses for this tier.
  const commandPreviewRequest: ExecutorCommandPreviewRequest | undefined = executor?.cli
    ? buildExecutorPreviewRequest(executor.cli, model, executor.cli_path, executor.cli_args)
    : undefined

  const timeoutPayload = timeoutSeconds > 0 ? timeoutSeconds : undefined
  const formData = useMemo(() => {
    // Spec-4 FR-4.1: subagent_3p agents run inside an external CLI, so many
    // Omnipus-native fields are irrelevant or explicitly rejected by the
    // backend. Build a restricted payload for that tier.
    const isSubagent3p = agent?.type === 'subagent_3p'
    const identity = isSubagent3p
      ? { name, description, color: selectedColor, icon: selectedIcon }
      : { name, description, color: selectedColor, icon: selectedIcon, default: isDefault }
    const rateLimits = {
      use_global_defaults: useGlobalRateLimits,
      max_llm_calls_per_hour: maxLlmCallsPerHour !== '' ? maxLlmCallsPerHour : undefined,
      max_tool_calls_per_minute: maxToolCallsPerMinute !== '' ? maxToolCallsPerMinute : undefined,
      max_cost_per_day: maxCostPerDay !== '' ? maxCostPerDay : undefined,
    }
    if (isSubagent3p) {
      // agent-types-field-matrix.md, Decisions #1 (resolved 2026-07-03):
      // excluded — subagent_3p EXCLUDES max_tool_iterations — the external
      // CLI runs its own tool loop, and the backend now rejects the field
      // for this type. timeout_seconds STAYS (process-level kill for a
      // hung CLI).
      return {
        ...identity,
        model,
        // O3 two-field: include provider only when non-empty.
        provider: primaryProvider.trim() !== '' ? primaryProvider.trim() : undefined,
        soul,
        // ADR-052 FR-039: allowed on all agents, including subagent_3p — not
        // one of firstForbiddenSubagent3pField's rejected fields
        // (pkg/gateway/agent_field_rules.go).
        memory_enabled: memoryEnabled,
        rate_limits: rateLimits,
        timeout_seconds: timeoutPayload,
        executor,
      }
    }
    return {
      ...identity,
      model,
      // O3 two-field: include provider only when non-empty.
      provider: primaryProvider.trim() !== '' ? primaryProvider.trim() : undefined,
      // Editor state matches the wire shape 1:1; emit `undefined` for
      // empty (treated as "no fallbacks" by the backend).
      fallback_models: fallbackModels.length > 0 ? fallbackModels : undefined,
      model_params: { temperature, max_tokens: maxTokens, top_p: topP },
      rate_limits: rateLimits,
      soul,
      // ADR-052 FR-039: "Allowed on all agents" per AgentUpdateRequest.yaml —
      // including locked/system agents (the Judge). Always sent (not
      // conditionally omitted) so a deliberate toggle-off round-trips like
      // every other boolean field on this form.
      memory_enabled: memoryEnabled,
      // W6-B4 / G1: voice is optional — emit only when non-empty so the backend
      // can leave the field unchanged when the user hasn't set it. An empty
      // string and `undefined` are semantically equivalent for the wire (both
      // mean "no override"); sending `null` explicitly would clear an existing
      // value, which is the right semantics for "Clear voice" but not for an
      // untouched field. We send `undefined` (omitted) for the empty case.
      // W6-B-fix: trim on the wire so whitespace-only inputs collapse to
      // "no voice configured" rather than persisting a literal "   " that
      // breaks TTS lookup at v0.2.0 release.
      //
      // Trim-vs-raw isCurrent gap (item 2, WorkspaceSettingsTab fix):
      // `voice` (and `provider` above) are trimmed HERE, inside the object
      // useAutoSave actually watches — in principle the same class of bug
      // WorkspaceSettingsTab had (isCurrent's deep-equal compares trimmed
      // values, so a same-tick hydration could clobber raw trailing
      // whitespace the operator hasn't committed yet). Left as-is rather
      // than moved to raw-in/trim-in-saveFn: unlike WorkspaceSettingsTab,
      // this form's hydration effect is ALSO gated by the `updated_at`
      // monotonic guard (`lastIncorporatedUpdatedAtRef`) — a same-or-stale
      // refetch echo is rejected regardless of `isDirtyRef`, so the gap is
      // shielded here. `provider` is additionally never free-typed (it's
      // set only via ModelSelector's onPairChange), so trailing whitespace
      // can't occur for it in practice. Aligning to raw-in/trim-in-saveFn
      // would also require branching the trim by tier inside saveFn (this
      // form's `data` shape differs for subagent_3p vs. not) for marginal
      // benefit given the existing shield — not done.
      voice: voice.trim() !== '' ? voice.trim() : undefined,
      timeout_seconds: timeoutPayload,
      max_tool_iterations: maxToolIterations,
      // ADR-066 D9 / FR-037: omitted when untouched-and-absent (undefined is
      // dropped by JSON.stringify), null to clear, number to set. Not sent
      // for subagent_3p (the external CLI owns its own window; the agent
      // is an exempt row with context_window_effective 0).
      ...(contextWindowOverride !== undefined ? { context_window_override: contextWindowOverride } : {}),
      shell_policy: {
        custom_deny_patterns: shellDenyPatterns.filter((p) => p.trim() !== ''),
      },
      // tools_cfg is intentionally OMITTED here. Tool policies are saved via the
      // dedicated PUT /agents/{id}/tools endpoint (re-auth gated) inside
      // ToolsAndPermissions — including it in the main agent PUT would bypass the
      // re-auth gate and cause spurious saves whenever ToolsAndPermissions syncs
      // server-hydrated config into local state. The backend treats an absent
      // tools_cfg as "leave unchanged" (if req.ToolsCfg != nil guard in rest.go).
      // US-E6: include the per-agent skill list in the auto-save payload.
      // Send the current list (may be empty, meaning no skills). The backend
      // treats an absent field as "leave unchanged" and an explicit empty
      // array as "remove all skills" — we always send the current value so
      // a deliberate clear (removing the last skill) is persisted correctly.
      skills: agentSkills,
      // Spec-4 FR-4.1: persist the executor only when explicitly configured.
      // Omitting it (undefined) leaves the backend on its "native" default
      // rather than forcing an empty value over the wire.
      executor,
    }
  }, [
    agent?.type, name, description, model, primaryProvider, selectedColor, selectedIcon, isDefault, fallbackModels,
    temperature, maxTokens, topP, useGlobalRateLimits, maxLlmCallsPerHour,
    maxToolCallsPerMinute, maxCostPerDay, soul, memoryEnabled, voice,
    timeoutPayload, timeoutSeconds, maxToolIterations, contextWindowOverride,
    shellDenyPatterns,
    agentSkills, executor,
  ])

  const {
    status: saveStatus,
    error: saveError,
    lastSavedAt: saveLastSavedAt,
    saveNow: saveAgentNow,
    hasPendingChanges: hasPendingAgentChanges,
  } = useAutoSave(
    formData,
    async (data) => {
      // Do not save before the server data has been hydrated into state —
      // saving before hydration would overwrite real data with empty defaults.
      if (!hasHydrated.current) return
      // Form is mounted at the layout level; its state can outlive a
      // closed sheet. Refuse a save with no id rather than PUT /null.
      if (agentId === null) return
      // I2: a 409 Conflict was raised on a prior save and the refetch is in
      // flight. Block subsequent debounced saves until the refetch lands and
      // the form is re-hydrated from the server's current state — otherwise
      // the stale form would just 409 again on every debounce tick.
      if (conflictRef.current) return
      // external-executor-cli-path-detection spec FR-008/FR-018/FR-019:
      // block autosave when the last blur-triggered validate() call
      // returned a definitive missing-binary/handshake-failed verdict for
      // the CURRENT cli_path. Editing the path resets `cliValidation` to
      // idle (see the Input's onChange below), so an unchecked/pending/
      // errored state never blocks (no trap). This runs BEFORE the I12
      // runner-test (which requires the agent to already be persisted) so a
      // known-bad path never even reaches `updateAgent`.
      if (data.executor?.kind === 'external-cli' && cliValidationBlocked(cliValidation.status)) {
        const reason = cliValidation.status.kind === 'result' ? cliValidation.status.result.reason : 'unknown'
        addToast({
          message: `CLI path failed validation (${reason}). Fix the path before saving.`,
          variant: 'error',
        })
        throw new Error(`CLI path validation failed: ${reason}`)
      }
      // I12: when the agent's executor is external-cli, run the runner-test
      // BEFORE allowing the save to commit. We re-test when the kind, cli, OR
      // cli_path actually changes (or the agent id changes) so the auto-save
      // debounce doesn't re-fire on every unrelated keystroke — but a path
      // edit alone (code-review m2) MUST still re-fire the test, otherwise a
      // definitively-bad path could silently persist unverified. On a
      // failure, throw — useAutoSave catches and surfaces the error inline,
      // the save is blocked, and the form stays dirty so the user can fix
      // and retry.
      if (data.executor?.kind === 'external-cli' && agentId) {
        const sig: ExecutorSig = {
          kind: data.executor.kind,
          cli: data.executor.cli,
          cli_path: data.executor.cli_path,
          agentId,
        }
        const last = testedExecutorSig.current
        const needsTest = !last
          || last.agentId !== sig.agentId
          || last.kind !== sig.kind
          || last.cli !== sig.cli
          || last.cli_path !== sig.cli_path
        if (needsTest) {
          let result
          try {
            result = await testAgentRunner(agentId)
          } catch (err) {
            // Network / 5xx failure — surface the message inline. The user
            // can retry the save (or run the explicit Test Connection button).
            const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : String(err)
            addToast({
              message: `Runner test failed before save: ${msg}`,
              variant: 'error',
            })
            throw new Error(`Runner test failed: ${msg}`, { cause: err })
          }
          if (!result.ok) {
            // Backend returned a structured failure (missing-binary,
            // unauthenticated, handshake-failed, unknown-cli, not-external-cli).
            // Block the save — operator needs to fix the runner first.
            addToast({
              message: `Runner test failed: ${result.message}. Fix the runner before saving.`,
              variant: 'error',
            })
            throw new Error(`Runner test failed (${result.reason || 'unknown'}): ${result.message}`)
          }
          // Cache the signature only on success — a failed test keeps us
          // ready to retry on the next change.
          testedExecutorSig.current = sig
        }
      }
      // Locked agents: strip every field the backend actually treats as
      // immutable for the locked roster (`pkg/gateway/rest.go`'s locked
      // reject-set, ~2458: Name / Description / Soul / Color / Icon /
      // Skills only — "cannot modify locked agent identity or prompt").
      // Sending one of THOSE yields a 403, and the autosave indicator would
      // surface a spurious error. Skills are stripped here (B-2
      // defense-in-depth on the frontend side): the Skills picker is
      // rendered disabled for locked agents, so this strip is the
      // belt-and-suspenders path for any state that may survive hydration.
      // shell_policy is NOT stripped (live bug fix, 2026-07-03):
      // it was never in the backend's reject-set either, so stripping it
      // here silently discarded every locked-agent shell-deny-pattern edit
      // before it reached the wire — the editor rendered interactive but
      // nothing ever persisted.
      // Note: tools_cfg is no longer in formData (it has its own re-auth-gated
      // endpoint via ToolsAndPermissions) so it does not need stripping here.
      //
      // ADR-052 FR-038 (soul/rubric unification): `soul` is EXEMPT from this
      // strip for a System Agent (the Judge) — the backend's updateAgent
      // carve-out (`foundAgent.IsSystem()`, pkg/gateway/rest.go) now accepts
      // `soul` for a locked System Agent while every other identity field
      // stays rejected. Stripping it here unconditionally for every locked
      // agent (the pre-Fix-Wave-2 behaviour) would silently drop the exact
      // edit the now-editable soul textarea invites the operator to make —
      // the same "dead interaction" class this strip exists to prevent in
      // the other direction.
      const stripSoulToo = !(agent && agentKindFlags(agent).isSystem)
      const stripLockedIdentityFields = (raw: Record<string, unknown>): Record<string, unknown> => {
        const rest = { ...raw }
        const soulField = rest.soul
        delete rest.name
        delete rest.description
        delete rest.soul
        delete rest.color
        delete rest.icon
        delete rest.skills
        delete rest.executor
        return stripSoulToo ? rest : { ...rest, soul: soulField }
      }
      const stripped = agent?.locked
        ? stripLockedIdentityFields(data as Record<string, unknown>)
        : data
      // W6-contracts: include updated_at from the last GET response so the
      // backend can reject stale writes with 409 Conflict.
      const payload = { ...stripped, updated_at: agent?.updated_at }
      try {
        const resp = await updateAgent(agentId, payload)
        // I2 / UAT data-loss fix (passive fallback_models repro): seed the
        // query cache with the FULL PUT response — `PUT /agents/{id}`
        // contractually "returns the complete updated agent object"
        // (contracts/openapi.yaml), identical in shape to the GET response —
        // not just `updated_at`. The earlier version only patched
        // `updated_at` via `{ ...old, updated_at: resp.updated_at }`, which
        // left every OTHER field (including `fallback_models`) pointing at
        // the STALE pre-save `old` snapshot. That partial patch still swaps
        // in a brand-new `agent` object reference, which re-triggers the
        // `[agentId, agent]` hydration effect below on React's next render —
        // and by the time that effect actually runs, `isDirtyRef.current`
        // has ALREADY been cleared to `false` by this same save's own
        // success path (a few lines down), so the hydration guard does not
        // block it. The effect then re-hydrates EVERY field (not just
        // updated_at) from that Frankenstein object, silently reverting
        // `fallbackModels` state back to its pre-edit value. That reversion
        // is a real `formData` change from useAutoSave's point of view, so
        // it fires a second, correctly-serialized (not overlapping) debounce
        // cycle roughly one `debounceMs` later — carrying the now-reverted,
        // fallback_models-less payload — which silently clobbers the
        // first save. Using the full response keeps the optimistic cache
        // patch fully consistent with what the server now actually has, so
        // if the hydration effect races and re-runs before the
        // `invalidateQueries` refetch below lands, it reproduces the SAME
        // correct state and no spurious second save fires. The
        // `invalidateQueries` call below still runs to reconcile with a
        // fresh GET (e.g. server-side derived fields), but this is no
        // longer the only thing standing between a save and a stale cache.
        //
        // Round 2 of this same regression test (see
        // `lastIncorporatedUpdatedAtRef`'s doc comment above): even with
        // the full-response cache patch, a STALE `invalidateQueries`
        // refetch immediately below can still swap in an `agent` snapshot
        // that isn't newer than what we just saved — so the hydration
        // effect's own `updated_at` monotonic check is the layer that
        // actually closes the window. Set the ref here too (synchronously,
        // before `isDirtyRef.current` is even cleared below) so there is
        // no gap between "we know what we just saved" and "the hydration
        // effect is willing to trust it."
        if (resp) {
          queryClient.setQueryData(['agent', agentId], resp)
          if (resp.updated_at) lastIncorporatedUpdatedAtRef.current = resp.updated_at
        }
      } catch (err) {
        // W6-contracts: on a 409 Conflict, surface a toast with a Refresh
        // action that refetches the agent state and drops pending edits.
        if (isApiError(err) && err.status === 409) {
          // I2: arm the conflict guard so subsequent debounced saves don't
          // fire (and re-409) while the refetch is in flight. Cleared in the
          // refetchAgent().then() callback below once the fresh state lands.
          conflictRef.current = true
          addToast({
            message: 'This agent was changed elsewhere. Refresh to load the latest version.',
            variant: 'error',
            action: {
              label: 'Refresh',
              onClick: () => {
                refetchAgent().then(() => {
                  if (isDirtyRef.current) isDirtyRef.current = false
                  // I2: refetch landed — re-arm the save path. The form will
                  // re-hydrate from the fresh GET and the next edit debounces
                  // normally against the server's current updated_at.
                  conflictRef.current = false
                })
              },
            },
          })
        }
        throw err
      }
      // Draft-ownership rule: dirty clears via useAutoSave's `onSaved`
      // callback below (only when the save snapshot still equals the live
      // draft), NOT unconditionally here — see that callback for why.
      queryClient.invalidateQueries({ queryKey: ['agent', agentId] })
      queryClient.invalidateQueries({ queryKey: ['agents'] })
    },
    // Locked agents can still save model and tool changes — locked status
    // itself must NEVER disable auto-save (an `agent.locked` guard is a
    // confirmed-wrong fix for the D3 spurious-PUT bug below — it fires for
    // every agent, locked or not). The `disabled` key in the options object
    // is unrelated to locked status: it gates on hydration readiness only.
    {
      // D3 / UAT spurious-PUT fix: block useAutoSave's own change-detection
      // entirely until `formHydrated` flips true at the end of the
      // hydration effect above. Without this, useAutoSave's "skip first
      // render" baseline-capture logic seeds itself from the hardcoded
      // useState defaults every field above starts at (name='',
      // description='', model='', ...) — captured on the FIRST commit
      // where `disabled` is false, which used to be the very first render,
      // before the agent's real data had loaded. The LATER hydration
      // commit then looks like a genuine user edit relative to that
      // all-defaults baseline, arming the debounce and firing a `PUT` that
      // just echoes the server's own data back (bumps `updated_at`, shows
      // "✓ Saved just now" to a user who touched nothing — reproduced by 2
      // UAT testers opening the read-only core agent Mia). `formHydrated`
      // is real component state, not a ref read at render time, so it
      // flips false→true in the SAME commit the hydrated field values
      // land — useAutoSave's baseline is captured from the HYDRATED data
      // instead.
      disabled: !formHydrated,
      // Long-form surface (SOUL and other multi-line fields live on this
      // form) — raised from the 500ms default so a normal typing cadence
      // doesn't fire a save (and its own invalidateQueries echo) on nearly
      // every pause. Small-field edits (e.g. the Default toggle) still
      // reach the server within one debounce cycle. There is no "blur
      // flush" — the hook has no blur hook at all. The real mechanisms that
      // can flush a pending edit ahead of the 1500ms debounce are: the
      // debounce timer itself, the hook's unmount flush, the pagehide/
      // beforeunload/visibilitychange keepalive beacon (`flushUrl` below),
      // and — since this sheet does NOT unmount on close (it lives at the
      // route level; see `handleCloseAgentSheet` below) — the explicit
      // flush-on-close this component now performs itself.
      debounceMs: 1500,
      // Draft-ownership rule — see useAutoSave onSaved docs; clear only
      // when isCurrent.
      onSaved: (_saved, isCurrent) => {
        // Mirror the saveFn's own no-op early-returns above (not hydrated
        // yet / no agent selected / a 409 conflict refetch still pending):
        // none of those paths actually persisted anything, so a resolved
        // (non-throwing) saveFn call in any of those states must not touch
        // the dirty flag — clearing it here would let an UNRELATED
        // hydration open back up while local edits are still genuinely
        // unsaved (most importantly during an active 409 conflict, where
        // the debounce keeps firing no-op saves until the operator clicks
        // "Refresh").
        if (!hasHydrated.current || agentId === null || conflictRef.current) return
        if (isCurrent) isDirtyRef.current = false
      },
      // I13: best-effort flush of pending edits on tab close / page hide /
      // unload. Auth is the omnipus-session HttpOnly cookie (US-5 / FR-010),
      // sent automatically by the browser (useAutoSave sets
      // credentials:'include' + echoes the CSRF cookie on this PUT) —
      // sendBeacon can't carry either, which is why useAutoSave uses fetch
      // keepalive instead. No client-side token to pass through anymore.
      flushUrl: agentId !== null ? `/api/v1/agents/${agentId}` : undefined,
      // Finding A (7-reviewer-gate, fix/uat-v0.1.1-defects):
      // `openEditAgentSlideOver` (the raw Zustand setter behind
      // `editAgentId`) has THREE call sites — the deep-link route
      // (`src/routes/_app/agents.$agentId.tsx`), AgentCard, and
      // WorkspaceTeamTab — and NONE of them go through this component's own
      // `handleCloseAgentSheet` flush-before-switch (below). Since
      // AgentProfile is mounted once at the layout level and never unmounts
      // between agents, any of those call sites switching `editAgentId`
      // while THIS agent has a genuinely pending (debounced, not yet sent)
      // edit bypasses the flush entirely. Confirmed empirically — see
      // `AgentProfile.test.tsx`'s "Finding A" tests (switching `editAgentId`
      // directly, exactly as the deep-link route does, silently loses a
      // debounced SOUL edit with no error, no toast, no log — until this
      // callback).
      //
      // useAutoSave itself now detects this generically (see
      // `onDisabledWithPendingChanges`'s doc comment on
      // `UseAutoSaveOptions`) — an AUTOMATIC flush was deliberately rejected
      // there (render-phase/StrictMode hazards, and this form's `saveFn` has
      // real side-effecting preconditions — the I12 runner-test-before-save
      // gate, locked-agent field stripping, 409 handling — that a hand-rolled
      // "deferred flush" would have to reimplement correctly or risk a WORSE
      // bug than the one being fixed). This callback only SURFACES the loss:
      // a toast telling the operator to redo the edit, plus a diagnostic
      // breadcrumb — "no silent errors" — instead of leaving it silently
      // discarded.
      //
      // `switchedAwayFromAgentIdRef` (set mid-render, in the Finding-B
      // `prevAgentIdForReset` block above, the instant a switch away from a
      // DIFFERENT agent is detected) supplies which agent this was — `data`
      // alone doesn't carry an id.
      onDisabledWithPendingChanges: () => {
        const lostAgentId = switchedAwayFromAgentIdRef.current
        console.warn(
          'AgentProfile: switched away from an agent with an unsaved pending edit — the edit was not saved',
          { agentId: lostAgentId },
        )
        logDiagnostic('agentProfileSwitchDiscardedPendingEdit', { agentId: lostAgentId })
        addToast({
          message: 'Your last change was not saved before you switched agents. Reopen it and re-enter the change.',
          variant: 'error',
        })
      },
    },
  )

  // Always-fresh mirror of `saveStatus` for effects that must read the
  // CURRENT save status without depending on it as a re-render trigger —
  // used by the hydration effect's focused-field belt-and-braces check
  // below (item 3 of the echo-race fix). Plain per-render assignment (not a
  // `useEffect`) so it is guaranteed up to date before ANY effect in this
  // commit runs, regardless of declaration order.
  const saveStatusRef = useRef(saveStatus)
  saveStatusRef.current = saveStatus

  // Item 1 (MERGE-BLOCKER — sheet-close data loss): AgentProfile is mounted
  // at the route level and does NOT unmount when the sheet closes (only
  // `editAgentId` flips to null) — so useAutoSave's own unmount-flush never
  // fires here. Without this, a debounce timer armed less than 1500ms
  // before close fires AFTER close, lands in saveFn's `agentId === null`
  // early-return, and useAutoSave records that as a SUCCESS (advances
  // `lastSavedJsonRef`, shows 'Saved') — the edit is silently lost even
  // though nothing was ever sent to the server.
  //
  // Fix: flush explicitly on close, BEFORE the store write that clears
  // `editAgentId`. Ordering is load-bearing, not cosmetic: `saveNow()` is
  // fire-and-forget, but calling it synchronously captures the CURRENT
  // render's `saveFn` closure (via useAutoSave's internal `saveFnRef`,
  // reassigned every render) and the current `agentId` — both while they
  // still refer to this agent. If `closeEditAgentSlideOver()` ran FIRST,
  // the store update would (on the next render) flip `agentId` to null and
  // the `[agentId]` reset effect would clear `isDirtyRef`/`hasHydrated`
  // — but that reassignment happens strictly AFTER this synchronous call
  // returns (React state updates from a store action are not applied
  // mid-function), so calling `saveNow()` before the store write is safe
  // either way; doing it in this order is simply the same "flush before
  // teardown" pattern the hook's own unmount cleanup uses, applied to the
  // one teardown moment (sheet close) that isn't a real unmount.
  function handleCloseAgentSheet() {
    if (hasPendingAgentChanges()) {
      saveAgentNow()
    }
    closeEditAgentSlideOver()
  }

  // W6-B4 / G3 fix: the "Default agent" toggle used to fire its success
  // toast (and invalidate ['agents']) the instant the Switch was flipped —
  // BEFORE the debounced autosave had actually persisted the flag.
  // A slow network or a failed save left the toast lying: the operator saw
  // "X is now the default agent" while the server still had the old value.
  // This ref records the pending default-flag change; the effect below only
  // fires the toast once `saveStatus` confirms it actually landed (and
  // silently drops the pending flag on error — the AutoSaveIndicator already
  // surfaces the failure, and a wrong-tense success toast would be worse).
  const pendingDefaultToastRef = useRef<{ next: boolean; label: string } | null>(null)
  useEffect(() => {
    if (saveStatus === 'saved' && pendingDefaultToastRef.current) {
      const { next, label } = pendingDefaultToastRef.current
      pendingDefaultToastRef.current = null
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      addToast({
        message: next ? `${label} is now the default agent` : `${label} is no longer the default agent`,
        variant: 'success',
      })
    } else if (saveStatus === 'error') {
      pendingDefaultToastRef.current = null
    }
  }, [saveStatus, queryClient, addToast])

  // Add a fallback from the <ModelSelector> dropdown. Pairs the picked
  // model with the provider that owns it (via modelToProvider). De-dupes
  // by model — the fallback list cannot have the same model twice
  // (US-3). Free-text picks (model not in any connected provider) are
  // accepted but surface a warning toast.
  function addFallbackFromSelector(modelSlug: string) {
    const trimmed = modelSlug.trim()
    if (!trimmed) return
    const knownProvider = modelToProvider(trimmed)
    if (!knownProvider) {
      addToast({
        message: `"${trimmed}" isn't listed by any connected provider — saving anyway, but the fallback may not work.`,
        variant: 'warning',
      })
    }
    setFallbackModels((prev) => {
      if (prev.some((f) => f.model === trimmed)) return prev
      return [...prev, { model: trimmed, provider: knownProvider ?? '' }]
    })
  }

  function removeFallback(modelSlug: string) {
    setFallbackModels((prev) => prev.filter((f) => f.model !== modelSlug))
  }

  // W6-C2 / I9: per-chip provider picker. Picking a different provider
  // for an existing fallback updates the entry's `provider` field
  // (the wire routing key — see FR-007 / FallbackModel.yaml). Picking
  // "—" (empty) clears the provider so the chip flips into the
  // "Provider not connected" state (I11). No-op when the value is
  // unchanged.
  function setFallbackProvider(modelSlug: string, providerId: string) {
    setFallbackModels((prev) =>
      prev.map((f) => (f.model === modelSlug ? { ...f, provider: providerId } : f)),
    )
  }

  // W6-C2 / I10: reorder helper. The wire contract for `fallback_models`
  // is "tried in the order they appear in the array" (see
  // FallbackModel.yaml description); order matters semantically.
  // Up/down arrow buttons keep the move explicit and keyboard-friendly
  // (no native HTML5 drag-and-drop needed, no extra library, accessible
  // to screen readers and keyboard-only users).
  function moveFallback(modelSlug: string, direction: -1 | 1) {
    setFallbackModels((prev) => {
      const idx = prev.findIndex((f) => f.model === modelSlug)
      if (idx < 0) return prev
      const target = idx + direction
      if (target < 0 || target >= prev.length) return prev
      const next = prev.slice()
      const [item] = next.splice(idx, 1)
      next.splice(target, 0, item)
      return next
    })
  }

  // UploadButton moved to the shared UploadMdButton in AgentFormFields.tsx
  // (create/edit parity, P3 2026-07-03) — one implementation for both dialogs.

  // Wave 5 / spec §6.1 BDD #15: Delete agent confirmation: the mutation invalidates
  // the list cache on success, surfaces the API error inline on failure,
  // and closes the slide-over only on success (so a network blip keeps
  // the operator on the same page). The button itself is hidden for
  // locked agents (see SheetFooter below).
  const deleteAgentMutation = useMutation({
    mutationFn: (id: string) => deleteAgent(id),
    onSuccess: () => {
      // Drop the deleted agent from the list cache immediately so no
      // per-id GET refetch fires for a resource that no longer exists.
      queryClient.setQueryData(['agents'], (prev: unknown) => {
        if (!Array.isArray(prev)) return prev
        return prev.filter((a) => (a as { id?: string }).id !== agentId)
      })
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      queryClient.invalidateQueries({ queryKey: ['agent', agentId] })
      setDeleteOpen(false)
      closeEditAgentSlideOver()
      addToast({ message: 'Agent deleted', variant: 'success' })
    },
    onError: (err: unknown) => {
      const msg = isApiError(err)
        ? err.userMessage
        : err instanceof Error
          ? err.message
          : 'Delete failed'
      addToast({ message: `Delete failed: ${msg}`, variant: 'error' })
      setDeleteOpen(false)
    },
  })

  // FR-016 / A2/F-09: Heartbeat tab saves to the WORKSPACE — a separate mutation
  // from the agent autosave. The tab opts out of the agent autosave flow entirely.
  // fix-1 (DATA-LOSS guard): hard-block the mutation if the workspace failed to
  // load or is still loading. Saving against an undefined/empty member_configs
  // base would wipe every other member's heartbeat config in the workspace.
  const saveHeartbeatMutation = useMutation({
    mutationFn: async () => {
      if (!editAgentWorkspaceId || !agentId) throw new Error('No workspace context')
      if (isWorkspaceError || isWorkspaceLoading || workspaceData === undefined) {
        throw new Error('Workspace data not available — please retry loading the workspace before saving.')
      }
      const existingMemberConfigs = workspaceData.member_configs ?? {}
      const updatedMemberConfigs = {
        ...existingMemberConfigs,
        [agentId]: {
          heartbeat: {
            enabled: hbEnabled,
            interval_minutes: hbIntervalMinutes,
            body: hbBody,
          },
        } satisfies WorkspaceMemberConfig,
      }
      return updateWorkspace(editAgentWorkspaceId, {
        member_configs: updatedMemberConfigs as Record<string, WorkspaceMemberConfig>,
      } as Parameters<typeof updateWorkspace>[1])
    },
    onSuccess: (updated) => {
      hbDirtyRef.current = false
      // Refresh the workspace cache so the Heartbeat tab re-reads the latest.
      queryClient.setQueryData(workspacesQueryKeys.detail(editAgentWorkspaceId as string), updated)
      // I-3 (US-7/US-8/FR-021): invalidate the sessions list so the session
      // lists (SearchModal, sidebar accordion) re-fetch and reflect the new
      // heartbeat state (newly pinned session appears on enable; delete
      // control re-enables/disables on toggle).
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
      addToast({ message: 'Heartbeat saved', variant: 'success' })
    },
    onError: (err: unknown) => {
      const msg = isApiError(err)
        ? err.userMessage
        : err instanceof Error
          ? err.message
          : 'Heartbeat save failed'
      addToast({ message: `Heartbeat save failed: ${msg}`, variant: 'error' })
    },
  })

  if (isLoading) {
    return (
      <ProfileSheet
        isOpen={isOpen}
        onClose={handleCloseAgentSheet}
        title="Edit agent"
        onOpenAutoFocus={handleOpenAutoFocus}
        contentRef={sheetContentRef}
      >
        <div className="flex flex-1 items-center justify-center text-[var(--color-muted)] text-sm">
          Loading agent...
        </div>
      </ProfileSheet>
    )
  }

  if (isError || !agent) {
    // Distinguish "this agent does not exist" (404) from transient errors so
    // the user gets the right copy and the right path forward. The previous
    // version lumped every error into "Agent not found", which misled users
    // on 500s / 401s / 502s and gave them no retry affordance.
    const isNotFound = isApiError(agentError) && agentError.status === 404
    const title = isNotFound ? 'Agent not found' : "Couldn't load agent"
    const detail = isNotFound
      ? 'This agent may have been deleted.'
      : isApiError(agentError)
        ? agentError.userMessage
        : agentError instanceof Error
          ? agentError.message
          : 'Check your connection and try again.'
    return (
      <ProfileSheet
        isOpen={isOpen}
        onClose={handleCloseAgentSheet}
        title={isNotFound ? 'Agent not found' : "Couldn't load agent"}
        onOpenAutoFocus={handleOpenAutoFocus}
        contentRef={sheetContentRef}
      >
        <div className="flex flex-1 flex-col items-center justify-center gap-3 px-8 text-center">
          <p className="text-sm font-medium text-[var(--color-secondary)]">{title}</p>
          <p className="text-xs text-[var(--color-muted)] max-w-sm">{detail}</p>
          <div className="flex gap-2">
            {!isNotFound && (
              <Button variant="outline" size="sm" onClick={() => refetchAgent()}>
                Retry
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={handleCloseAgentSheet}>
              Back to Agents
            </Button>
          </div>
        </div>
      </ProfileSheet>
    )
  }

  // Kind derivation — single source of truth is agentKindFlags (per
  // docs/internal/architecture/agent-types-field-matrix.md). isExternal /
  // isNativeWorker are TYPE-based (agent.type), not derived from the
  // locally-editable executor state: a `Subagent` is always native and a
  // `subagent_3p` is always external — only the legacy `worker` type is
  // ambiguous and falls back to the fetched agent's executor.kind.
  const { isLocked, isWorker: isWorkerAgent, isExternal: isExternalAgent, isNativeWorker: isNativeWorkerAgent, isSystem: isSystemAgent } = agentKindFlags(agent)
  // Past the early returns, `agent` is non-null and `agentId` is the id used
  // to fetch it. Narrow once for child components that take `string`.
  const resolvedAgentId = agentId as string
  // Tier-branched form. Workers are delegation-only labour agents: never a chat
  // target, no heartbeat, never the default, and carry an executor (the worker's
  // defining property). Base agents (core/custom/system) run native/in-process
  // only — no third-party executor is selectable for them. The locked concept
  // (`.preview-doc/agents.html`) makes the worker-vs-base split a property of
  // the agent itself, so we branch once here and let the JSX ask `isWorkerAgent`
  // to decide which accordions render. See the contract schema for the
  // `worker` type value: contracts/components/schemas/Agent.yaml.
  // Field matrix: native Subagents may set Tools / Skills
  // explicitly OR inherit them from the delegating caller — only
  // subagent_3p (isExternalAgent) hides them, since the external runner
  // manages its own tools/skills/isolation.
  // FR-016 / FR-025 / US-5: show the Heartbeat tab only when the slide-over
  // was opened from a workspace Team tab (editAgentWorkspaceId is set) AND the
  // agent is not a worker (`!isWorker()` per A2/F-08). Workers are
  // delegation-only and have no heartbeat concept.
  const showHeartbeatTab = editAgentWorkspaceId !== null && !isWorkerAgent

  // Operator decision (agent-types-field-matrix.md): locked core agents now
  // get editable Rate Limits + Execution knobs too, so the Advanced tab
  // always has more than the Activity feed — the tab is always "Advanced".
  const advancedTabLabel = 'Advanced'


  // Section panels shared by desktop Tabs and mobile Accordion.
  // basics panel
  const basicsPanel = (
    <div className="space-y-6">

          {/* Identity — always rendered; read-only for locked (core) agents */}
          <section className="space-y-3">
            <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Identity</p>
            <div className="space-y-3">
              <div className="space-y-2">
                <Input
                  data-testid="agent-name-input"
                  value={name}
                  disabled={isLocked}
                  readOnly={isLocked}
                  onChange={isLocked ? undefined : (e) => { markDirty(); setName(e.target.value) }}
                  placeholder="Agent name"
                  className="text-sm"
                />
                {/* Operator decision 2026-07-03: description becomes visible
                    READ-ONLY for locked core agents (previously hidden
                    entirely) — mirrors the name input's disabled+readOnly
                    treatment above. */}
                <Textarea
                  data-testid="agent-description-input"
                  value={description}
                  disabled={isLocked}
                  readOnly={isLocked}
                  onChange={isLocked ? undefined : (e) => { markDirty(); setDescription(e.target.value) }}
                  placeholder="Short description of this agent's purpose"
                  rows={2}
                  className="text-sm resize-none"
                />
              </div>
              {/* W6-B4 / G3: Default agent toggle. Locked core agents keep
                  this editable (operator decision 2026-07-03 — execution/
                  identity display knobs unhide for locked). Hidden for
                  workers — the locked concept makes "default" a non-worker
                  concept. ADR-049 D3/SD-C18: also hidden for System agents
                  (the Judge) — unlike core, a System agent is never
                  ★-eligible at all. */}
              {!isWorkerAgent && !isSystemAgent && (
                <div
                  data-testid="default-toggle-row"
                  className="flex items-center justify-between gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5"
                >
                  <div className="flex items-center gap-2 min-w-0">
                    <Star
                      size={14}
                      weight={isDefault ? 'fill' : 'regular'}
                      className={isDefault ? 'text-[var(--color-accent)] shrink-0' : 'text-[var(--color-muted)] shrink-0'}
                      aria-hidden="true"
                    />
                    <div className="min-w-0">
                      <p className="text-sm text-[var(--color-secondary)]">Default agent</p>
                      <p className="text-[11px] text-[var(--color-muted)] leading-snug">
                        Handles inbound messages with no more-specific routing rule. Only one agent is default at a time.
                      </p>
                    </div>
                  </div>
                  <Switch
                    data-testid="default-toggle"
                    checked={isDefault}
                    onCheckedChange={(v) => {
                      markDirty()
                      setIsDefault(v)
                      // Toast is deferred to the autosave success path (see
                      // `pendingDefaultToastRef` effect above) — this flag
                      // has not persisted yet.
                      pendingDefaultToastRef.current = { next: v, label: name || agent.name }
                    }}
                    aria-label={isDefault ? 'Unset as default agent' : 'Set as default agent'}
                  />
                </div>
              )}
              {/* Operator decision 2026-07-03: color/icon become visible
                  READ-ONLY for locked core agents (previously hidden
                  entirely) — a static swatch/icon+label, not the
                  interactive picker (which has no readOnly mode). */}
              <div className="space-y-1.5">
                <p className="text-xs text-[var(--color-muted)]">Avatar color</p>
                {isLocked ? (
                  <div className="flex items-center gap-2" data-testid="avatar-color-readonly">
                    <span
                      className="w-7 h-7 rounded-full shrink-0 border border-[var(--color-border)]"
                      style={{ backgroundColor: selectedColor || 'var(--color-surface-3)' }}
                      aria-hidden="true"
                    />
                    <span className="text-xs text-[var(--color-secondary)]">
                      {avatarColorName(selectedColor)}
                    </span>
                  </div>
                ) : (
                  <AvatarColorPicker
                    value={selectedColor ?? ''}
                    onChange={(color) => { markDirty(); setSelectedColor(color) }}
                    testIdPrefix="avatar-color"
                  />
                )}
              </div>
              <div className="space-y-1.5">
                <p className="text-xs text-[var(--color-muted)]">Avatar icon</p>
                {isLocked ? (
                  <div className="flex items-center gap-2" data-testid="avatar-icon-readonly">
                    {(() => {
                      const ReadOnlyIcon = getIconComponent(selectedIcon)
                      return <ReadOnlyIcon size={18} className="text-[var(--color-secondary)]" aria-hidden="true" />
                    })()}
                    <span className="text-xs text-[var(--color-secondary)]">{selectedIcon}</span>
                  </div>
                ) : (
                  <IconPicker
                    value={selectedIcon}
                    onChange={(icon) => { markDirty(); setSelectedIcon(icon) }}
                    triggerTestId="avatar-icon-trigger"
                  />
                )}
              </div>
            </div>
          </section>

          <Separator />

          {/* Model Configuration — picker, unresolved-slug indicator, fallback editor */}
          <section className="space-y-3">
            <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Model</p>
            {providersError && !isExternalAgent && (
              <p className="text-xs text-[var(--color-warning)]">
                Could not load providers. You can still enter a model slug manually.
              </p>
            )}
            {isExternalAgent ? (
              /* External CLI workers: FREE TEXT, never the connected-model
                 catalogue. The model slug is handed to the runner as --model
                 (ADR-032) and resolved by the CLI's OWN provider and auth —
                 the Omnipus provider catalogue is the wrong universe for it
                 (operator finding, 2026-07-03). */
              <div className="space-y-1.5">
                <Input
                  data-testid="external-model-input"
                  value={model}
                  onChange={(e) => { markDirty(); setModel(e.target.value) }}
                  placeholder="claude-sonnet-4-6"
                  className="text-sm font-mono"
                />
                <p className="text-xs text-[var(--color-muted)]">
                  Passed to the external CLI as its model flag. The runner uses its
                  own provider and authentication — enter any model slug the CLI
                  supports, independent of the providers connected here.
                </p>
              </div>
            ) : (
            /* UAT model-catalog fix: CONSTRAINED picker fed the connected-
                provider catalogue. Selection is limited to real models, so an
                unresolvable slug cannot be saved here and the inline
                "unresolved" warning can no longer occur. */
            <ModelSelector
              models={availableModels}
              value={model}
              onChange={(v) => { markDirty(); setModel(v) }}
              onPairChange={({ provider: p }) => { setPrimaryProvider(p) }}
              placeholder="Provider default"
              providerGroups={providerGroups}
              constrainToCatalog
              // When the provider list failed to load the catalogue is empty
              // through no fault of the operator — fall back to free-text entry
              // so the "enter a model slug manually" copy above is actually
              // honoured (and the currently-saved model stays visible/editable
              // instead of being hidden behind a disabled placeholder).
              allowFreeTextWhenEmpty={providersError}
              emptyCatalogHint="Connect a provider in Settings to pick a model"
            />
            )}
            {/* Sampling parameters — collapsed disclosure. Operator decision
                2026-07-03: editable for locked core agents too (model,
                sampling, rate limits, and execution knobs ARE mutable on the
                backend for locked agents — only identity/soul/skills are
                403'd). Hidden for subagent_3p: model_params is a
                runner-side concern for that type (field matrix) and the
                formData branch for subagent_3p never sends it — rendering
                this disclosure without the gate let an operator "edit" and
                "save" sampling params that silently reverted on refetch
                (live bug, 2026-07-03). */}
            {!isExternalAgent && (
            <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden">
              <button tabIndex={0}
                type="button"
                onClick={() => setAdvancedOpen((o) => !o)}
                className="flex items-center justify-between w-full px-3 py-2.5 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
                aria-expanded={advancedOpen}
              >
                <span>Sampling parameters</span>
                {advancedOpen ? <CaretUp size={13} /> : <CaretDown size={13} />}
              </button>
              {advancedOpen && (
                <div className="px-3 pb-3 space-y-4 border-t border-[var(--color-border)]">
                  <RangeField
                    label="Temperature"
                    caption="Higher = more creative / less predictable (0–2, default 1)"
                    value={temperature}
                    min={0}
                    max={2}
                    step={0.05}
                    onChange={(v) => { markDirty(); setTemperature(v) }}
                    format={(v) => v.toFixed(2)}
                  />
                  <RangeField
                    label="Max tokens"
                    caption="Maximum length of each reply"
                    value={maxTokens}
                    min={256}
                    max={32768}
                    step={256}
                    onChange={(v) => { markDirty(); setMaxTokens(v) }}
                    format={(v) => v.toLocaleString()}
                  />
                  <RangeField
                    label="Top P"
                    caption="Nucleus sampling mass — 1.0 disables it (default 1)"
                    value={topP}
                    min={0}
                    max={1}
                    step={0.01}
                    onChange={(v) => { markDirty(); setTopP(v) }}
                    format={(v) => v.toFixed(2)}
                  />
                </div>
              )}
            </div>
            )}
          </section>

          {/* Fallback models — item 1 reorg: relocated from the Tools tab to
              sit directly below the Model section on Basics (visually
              adjacent to the primary model). Hidden for subagent_3p (the
              runner manages its own retries; the field is not settable for
              this type per the field matrix). The two locked-only read-only
              summaries that used to live separately on Basics and Tools are
              CONSOLIDATED into this single copy — testids keep the
              `-basics` suffix since this is now the only surface. */}
          {!isExternalAgent && (
          <section className="space-y-3">
            <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Fallback models</p>
            <p className="text-xs text-[var(--color-muted)]">Tried in order if the primary model fails.</p>
            {isLocked ? (
              <div
                data-testid="fallback-summary-locked-basics"
                className="space-y-2 p-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)]"
              >
                <div className="flex items-center gap-2 text-[var(--color-muted)]">
                  <Lock size={12} weight="fill" aria-hidden="true" />
                  <p className="text-[11px]">
                    Locked: fallback models are inherited from the locked core config.
                  </p>
                </div>
                {fallbackModels.length === 0 ? (
                  <p className="text-xs text-[var(--color-muted)]">No fallback chain configured.</p>
                ) : (
                  <ol className="space-y-1" data-testid="fallback-summary-locked-basics-list">
                    {fallbackModels.map((entry, idx) => (
                      <li
                        key={entry.model}
                        className="flex items-center gap-2 text-xs font-mono text-[var(--color-secondary)]"
                      >
                        <span className="text-[var(--color-muted)] w-4 shrink-0 text-right">{idx + 1}.</span>
                        <span
                          data-testid={`fallback-summary-provider-${entry.model}`}
                          className="inline-flex items-center px-1.5 rounded text-[10px] font-semibold"
                          style={{
                            backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                            color: 'var(--color-accent)',
                            border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                          }}
                        >
                          {entry.provider || '—'}
                        </span>
                        <span data-testid={`fallback-summary-model-${entry.model}`}>{entry.model}</span>
                      </li>
                    ))}
                  </ol>
                )}
              </div>
            ) : (
              <div className="flex flex-wrap gap-1.5 p-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] min-h-[36px]">
                {fallbackModels.map((entry, idx) => {
                  const providerMissing = entry.provider === ''
                  const providerLabel = providerMissing
                    ? '—'
                    : (connectedProviders.find((p) => p.id === entry.provider)?.display_name
                        ?? connectedProviders.find((p) => p.id === entry.provider)?.name
                        ?? entry.provider)
                  return (
                    <span
                      key={entry.model}
                      data-testid={`fallback-chip-model-${entry.model}`}
                      className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-mono bg-[var(--color-surface-2)] text-[var(--color-secondary)] border border-[var(--color-border)]"
                    >
                      <span
                        data-testid={`fallback-chip-provider-${entry.model}`}
                        className="inline-flex items-center px-1 rounded text-[9px] font-semibold"
                        style={{
                          backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                          color: 'var(--color-accent)',
                          border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                        }}
                      >
                        {providerLabel}
                      </span>
                      <span className="relative inline-block">
                        <select tabIndex={0}
                          data-testid={`fallback-provider-select-${entry.model}`}
                          aria-label={`Provider for fallback ${entry.model}`}
                          value={entry.provider}
                          onChange={(e) => { markDirty(); setFallbackProvider(entry.model, e.target.value) }}
                          className="appearance-none bg-transparent text-[var(--color-muted)] hover:text-[var(--color-secondary)] pl-1 pr-3 py-0 text-[9px] focus-visible:border-[var(--color-accent)] rounded cursor-pointer"
                        >
                          <option value="" data-testid={`fallback-provider-option-empty-${entry.model}`}>—</option>
                          {connectedProviders.map((p) => (
                            <option
                              key={p.id}
                              value={p.id}
                              data-testid={`fallback-provider-option-${p.id}-${entry.model}`}
                            >
                              {p.display_name ?? p.name ?? p.id}
                            </option>
                          ))}
                        </select>
                        <CaretDown
                          size={9}
                          className="pointer-events-none absolute right-0.5 top-1/2 -translate-y-1/2 text-[var(--color-muted)]"
                          aria-hidden="true"
                        />
                      </span>
                      <span>{entry.model}</span>
                      <button tabIndex={0}
                        type="button"
                        data-testid={`fallback-chip-up-${entry.model}`}
                        aria-label={`Move fallback ${entry.model} up`}
                        disabled={idx === 0}
                        onClick={() => { markDirty(); moveFallback(entry.model, -1) }}
                        className="inline-flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-30 disabled:cursor-not-allowed disabled:hover:text-[var(--color-muted)] pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
                      >
                        <ArrowUp size={10} />
                      </button>
                      <button tabIndex={0}
                        type="button"
                        data-testid={`fallback-chip-down-${entry.model}`}
                        aria-label={`Move fallback ${entry.model} down`}
                        disabled={idx === fallbackModels.length - 1}
                        onClick={() => { markDirty(); moveFallback(entry.model, 1) }}
                        className="inline-flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-30 disabled:cursor-not-allowed disabled:hover:text-[var(--color-muted)] pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
                      >
                        <ArrowDown size={10} />
                      </button>
                      <button tabIndex={0}
                        type="button"
                        data-testid={`fallback-chip-remove-${entry.model}`}
                        aria-label={`Remove fallback ${entry.model}`}
                        onClick={() => { markDirty(); removeFallback(entry.model) }}
                        className="inline-flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
                      >
                        <X size={10} />
                      </button>
                      {providerMissing && (
                        <span
                          data-testid={`fallback-chip-warning-${entry.model}`}
                          role="img"
                          aria-label="Provider not connected — fallback will not be used at runtime"
                          title="Provider not connected — fallback will not be used at runtime"
                          className="inline-flex items-center text-amber-400"
                        >
                          <Warning size={11} weight="fill" />
                        </span>
                      )}
                    </span>
                  )
                })}
                <div className="min-w-[220px] flex-1">
                  {/* UAT model-catalog fix: fallbacks must be real catalogue
                      models, so this picker is constrained too. */}
                  <ModelSelector
                    models={availableModels}
                    value=""
                    onChange={(v) => { markDirty(); addFallbackFromSelector(v) }}
                    placeholder="Add fallback…"
                    providerGroups={providerGroups}
                    triggerTestId="fallback-add-trigger"
                    itemTestIdPrefix="fallback-add-item-"
                    constrainToCatalog
                    emptyCatalogHint="Connect a provider to add fallbacks"
                  />
                </div>
              </div>
            )}
          </section>
          )}

    </div>
  )

  // personality panel
  const personalityPanel = (
    <div className="space-y-5">

          {/* ADR-052 FR-039: per-agent memory-injection gate. Live/editable
              for every agent — memory_enabled is "allowed on all agents"
              server-side (rest.go explicitly exempts it from the
              locked-identity reject-set). The one exception is a System
              Agent (the Judge): its memory is forced OFF and re-enforced on
              EVERY boot (coreagent.seedSystemAgents) for reproducible,
              impartial verdicts — same evidence must always yield the same
              verdict — so an operator toggle here would just be silently
              reverted at next boot. Render it disabled + off with an
              explanatory note instead of a control that lies about being
              durable. */}
          <div
            data-testid="memory-toggle-row"
            className="flex items-center justify-between gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5"
          >
            <div className="flex items-center gap-2 min-w-0">
              <Brain
                size={14}
                weight={memoryEnabled ? 'fill' : 'regular'}
                className={memoryEnabled ? 'text-[var(--color-accent)] shrink-0' : 'text-[var(--color-muted)] shrink-0'}
                aria-hidden="true"
              />
              <div className="min-w-0">
                <p className="text-sm text-[var(--color-secondary)]">Memory</p>
                <p className="text-[11px] text-[var(--color-muted)] leading-snug">
                  {isSystemAgent
                    ? 'Verifier agents always run with memory off — the same evidence must always yield the same verdict.'
                    : "Lets this agent recall its workspace's shared memory across sessions. Off starts every turn from a clean slate."}
                </p>
              </div>
            </div>
            <Switch
              data-testid="memory-toggle"
              checked={memoryEnabled}
              disabled={isSystemAgent}
              onCheckedChange={isSystemAgent ? undefined : (v) => { markDirty(); setMemoryEnabled(v) }}
              aria-label={
                isSystemAgent
                  // Static, truthful label: this switch is permanently
                  // disabled for verifier agents (re-enforced every boot,
                  // see the note above), not a live toggle that merely
                  // happens to read "off" right now — the previous
                  // conditional label ('Turn memory on') falsely implied
                  // an interactive control the operator could act on.
                  ? 'Memory — locked off for verifier agents'
                  : memoryEnabled ? 'Turn memory off' : 'Turn memory on'
              }
            />
          </div>

          <BehaviorFields
            isWorker={isWorkerAgent}
            soul={soul}
            setSoul={(v) => { markDirty(); setSoul(v) }}
            voice={voice}
            setVoice={(v) => { markDirty(); setVoice(v) }}
            renderUploadButton={(_target, onUpload) => <UploadMdButton onUpload={(v) => { onUpload(v); markDirty() }} />}
            // ADR-052 FR-038 (soul/rubric unification): soul is read-only
            // for locked CORE agents (Mia/Jim/Ava/Ray) — their souls are
            // product identity, not a verifier rubric, and the backend's
            // updateAgent handler still rejects `soul` unconditionally for
            // them. System Agents (the Judge) are the carve-out: the
            // backend now allows `soul` for a locked agent when
            // `IsSystem()` is true (pkg/gateway/rest.go), because the
            // Judge's soul IS its operator-editable verification rubric —
            // identity (name/description/color/icon/skills) stays locked,
            // soul does not. See the System-agent banner below.
            soulReadOnly={isLocked && !isSystemAgent}
          />

          {/* Heartbeat — moved to per-workspace Heartbeat tab (spec A1/F-10).
              Heartbeat is now configured in the Workspace edit panel, not here. */}

    </div>
  )

  // tools panel — item 2 reorg: Tools & Permissions ONLY (Skills is now its
  // own tab; the Fallback models editor moved to Basics — item 1).
  const toolsPanel = (
    <div className="space-y-6">

          {/* Tools & Permissions — hidden for subagent_3p (the external
              runner has its own tools; per-tool CLI flags govern instead
              per the field matrix). Native workers (and every other kind)
              get the LIVE editor — no more read-only collapse. */}
          {!isExternalAgent && (
            <section className="space-y-3">
              <div className="flex items-center gap-2">
                <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Tools &amp; Permissions</p>
                {(() => {
                  const overrideCount = Object.keys(toolsCfg.builtin?.policies ?? {}).length
                  if (overrideCount === 0) return null
                  return (
                    <span className="text-xs text-[var(--color-muted)] font-normal">
                      {overrideCount} overrides
                    </span>
                  )
                })()}
              </div>
              <ToolsAndPermissions
                agentId={agentId}
                agentType={agent.type}
                isLocked={isLocked}
                tools={toolsCfg}
                onChange={setToolsCfg}
              />
            </section>
          )}

    </div>
  )

  // skills panel — item 2 reorg: split out of the former combined Tools tab
  // into its own tab. Same gating and content as before, just relocated.
  const skillsPanel = (
    <div className="space-y-6">

          {/* Skills — Main/core/Subagent (native worker). Per the field
              matrix a native Subagent may optionally be granted skills (or
              inherit); only subagent_3p (external-cli) hides this — an
              external runner can never load Omnipus skills, so offering the
              mapping was a lie (P3 bug, 2026-07-03; the old !isWorkerAgent
              gate over-corrected and hid this for native Subagents too). */}
          {!isExternalAgent && (
            <section className="space-y-3">
              <div className="flex items-center gap-2">
                <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Skills</p>
                {agentSkills.length > 0 && (
                  <span className="text-xs text-[var(--color-muted)] font-normal">
                    {agentSkills.length} granted
                  </span>
                )}
              </div>
              <div className="space-y-3">
                {isLocked ? (
                  <p className="text-xs text-[var(--color-muted)]">
                    Skill assignment is read-only for locked core agents.
                  </p>
                ) : (
                  <p className="text-xs text-[var(--color-muted)]">
                    Grant specific installed skills to this agent. Only skills listed here
                    are available during this agent's runs. Empty means no skills.
                  </p>
                )}
                {availableSkills.length === 0 ? (
                  <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-4 text-center">
                    <Sparkle size={16} className="text-[var(--color-muted)] mx-auto mb-1.5" />
                    <p className="text-xs text-[var(--color-muted)]">No skills installed.</p>
                    <p className="text-xs text-[var(--color-muted)]/70 mt-0.5">
                      Install skills from the Skills &amp; Tools screen.
                    </p>
                  </div>
                ) : (
                  <div className="space-y-1.5">
                    {availableSkills.map((skill) => {
                      const granted = agentSkills.includes(skill.id)
                      return (
                        <label
                          key={skill.id}
                          className={`flex items-start gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5 transition-colors ${isLocked ? 'cursor-not-allowed opacity-60' : 'cursor-pointer hover:bg-[var(--color-surface-3)]'}`}
                        >
                          <input tabIndex={0}
                            type="checkbox"
                            checked={granted}
                            disabled={isLocked}
                            onChange={isLocked ? undefined : (e) => {
                              markDirty()
                              if (e.target.checked) {
                                setAgentSkills((prev) => [...prev, skill.id])
                              } else {
                                setAgentSkills((prev) => prev.filter((s) => s !== skill.id))
                              }
                            }}
                            className="mt-0.5 shrink-0 accent-[var(--color-accent)] disabled:opacity-50"
                            data-testid={`skill-checkbox-${skill.id}`}
                          />
                          <div className="min-w-0">
                            <p className="text-sm font-medium text-[var(--color-secondary)] leading-tight">
                              {skill.name}
                            </p>
                            {skill.description && (
                              <p className="text-[11px] text-[var(--color-muted)] mt-0.5 leading-snug">
                                {skill.description}
                              </p>
                            )}
                            <div className="flex items-center gap-2 mt-1">
                              <span className="text-[10px] font-mono text-[var(--color-muted)]/70">
                                {skill.id}
                              </span>
                              {skill.verified && (
                                <span className="text-[9px] px-1 rounded bg-emerald-500/20 text-emerald-400 border border-emerald-500/30">
                                  verified
                                </span>
                              )}
                            </div>
                          </div>
                        </label>
                      )
                    })}
                  </div>
                )}
              </div>
            </section>
          )}

    </div>
  )

  // runtime panel
  const runtimePanel = (
    <div className="space-y-5">

            <section className="space-y-3">
              <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Runtime</p>
              {/* CLI — read-only badge. The kind+cli tuple is the agent's
                  defining property; the operator can change which CLI is
                  used by recreating the agent (post v0.3 the wizard will
                  surface this, per the spec matrix). */}
              <div
                data-testid="profile-cli-locked"
                className="flex items-center gap-2 px-3 py-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)]"
              >
                <span className="text-[10px] uppercase tracking-wider text-[var(--color-muted)]">CLI</span>
                <span className="font-mono text-xs text-[var(--color-secondary)]">
                  {executor?.cli ?? 'claude-code'}
                </span>
                <span className="text-[10px] text-[var(--color-muted)]">(locked)</span>
              </div>
              <div
                data-testid="profile-cli-path"
                className="space-y-1.5"
              >
                <div className="flex items-center gap-3">
                  <label htmlFor="profile-cli-path-input" className="text-xs text-[var(--color-muted)] w-44 shrink-0">CLI path</label>
                  <Input
                    id="profile-cli-path-input"
                    value={executor?.cli_path ?? offeredCliPath ?? ''}
                    onChange={(e) => {
                      markDirty()
                      // Editing invalidates any prior validate() verdict —
                      // reset to idle so a stale block never survives an
                      // edit (FR-019).
                      cliValidation.reset()
                      setExecutor((prev) => ({ ...(prev ?? { kind: 'external-cli', cli: executor?.cli ?? 'claude-code' }), cli_path: e.target.value }))
                    }}
                    onBlur={(e) => {
                      const cli = executor?.cli ?? 'claude-code'
                      // code-review M2a: commit an OFFERED (detected,
                      // not-yet-typed) value into `executor.cli_path` at
                      // blur — this is the "confirm" point. A field the
                      // operator never focused never blurs, so merely
                      // opening the profile still cannot autosave the
                      // detected path; only actually visiting (and leaving)
                      // the field counts as confirming it.
                      if ((executor?.cli_path ?? '').trim().length === 0 && offeredCliPath) {
                        markDirty()
                        setExecutor((prev) => ({ ...(prev ?? { kind: 'external-cli', cli }), cli_path: offeredCliPath }))
                      }
                      cliValidation.validate(cli, e.target.value)
                    }}
                    placeholder="/usr/local/bin/claude"
                    className="text-xs h-8 font-mono"
                    disabled={isLocked}
                  />
                </div>
                <CliPathValidationHint
                  validation={cliValidation.status}
                  detectHint={resolveCliDetectHint(cliDetect, executor?.cli)}
                  testId="profile-cli-path-status"
                />
              </div>
              <div data-testid="profile-env-overrides" className="space-y-2">
                <label className="text-xs text-[var(--color-muted)]">Environment overrides</label>
                <p className="text-[11px] text-[var(--color-muted)] leading-snug">
                  KEY=value pairs passed to the CLI process. Empty means no overrides.
                </p>
                <EnvironmentOverridesEditor
                  value={executor?.env_overrides ?? {}}
                  onChange={(next) => {
                    markDirty()
                    setExecutor((prev) => ({
                      ...(prev ?? { kind: 'external-cli', cli: executor?.cli ?? 'claude-code' }),
                      env_overrides: next,
                    }))
                  }}
                  disabled={isLocked}
                />
              </div>
              <div data-testid="profile-cli-args" className="space-y-1.5">
                <div className="flex items-center gap-3">
                  <label htmlFor="profile-cli-args-input" className="text-xs text-[var(--color-muted)] w-44 shrink-0">Additional CLI arguments</label>
                  <Input
                    id="profile-cli-args-input"
                    value={executor?.cli_args ?? ''}
                    onChange={(e) => {
                      markDirty()
                      setExecutor((prev) => ({ ...(prev ?? { kind: 'external-cli', cli: executor?.cli ?? 'claude-code' }), cli_args: e.target.value }))
                    }}
                    placeholder="e.g. --add-dir /extra/path"
                    className="text-xs h-8 font-mono"
                    disabled={isLocked}
                  />
                </div>
                <p className="text-[11px] text-[var(--color-muted)] leading-snug">
                  In addition to the flags Omnipus applies automatically when this agent runs — see the live command preview below. Any argument that would be silently ignored is called out there before you save.
                </p>
              </div>
              <CommandPreview req={commandPreviewRequest} agentId={resolvedAgentId} testId="profile-command-preview" />
            </section>

    </div>
  )

  // advanced panel
  const advancedPanel = (
    <div className="space-y-6">

          {/* Rate Limits — editable for ALL agents including locked core
              agents (operator decision 2026-07-03: rate_limits is mutable on
              the backend for locked agents — only identity/soul/skills are
              403'd). */}
          <section className="space-y-3">
              <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Rate Limits</p>
              <div className="space-y-3">
                <div className="flex items-center justify-between py-1">
                  <div>
                    <p className="text-sm text-[var(--color-secondary)]">Use global defaults</p>
                    <p className="text-xs text-[var(--color-muted)]">Inherit rate limits from global settings</p>
                  </div>
                  <Switch
                    checked={useGlobalRateLimits}
                    onCheckedChange={(v) => { markDirty(); setUseGlobalRateLimits(v) }}
                    aria-label={useGlobalRateLimits ? 'Stop using global rate-limit defaults' : 'Use global rate-limit defaults'}
                  />
                </div>
                {!useGlobalRateLimits && (
                  <div className="space-y-2">
                    <div className="flex items-center gap-3">
                      <label htmlFor="profile-rate-limit-llm-calls" className="text-xs text-[var(--color-muted)] w-44 shrink-0">LLM calls / hour</label>
                      <Input
                        id="profile-rate-limit-llm-calls"
                        type="number"
                        min={0}
                        value={maxLlmCallsPerHour}
                        onChange={(e) => { markDirty(); setMaxLlmCallsPerHour(e.target.value === '' ? '' : Number(e.target.value)) }}
                        placeholder="Unlimited"
                        className="text-xs h-8"
                      />
                    </div>
                    <div className="flex items-center gap-3">
                      <label htmlFor="profile-rate-limit-tool-calls" className="text-xs text-[var(--color-muted)] w-44 shrink-0">Tool calls / minute</label>
                      <Input
                        id="profile-rate-limit-tool-calls"
                        type="number"
                        min={0}
                        value={maxToolCallsPerMinute}
                        onChange={(e) => { markDirty(); setMaxToolCallsPerMinute(e.target.value === '' ? '' : Number(e.target.value)) }}
                        placeholder="Unlimited"
                        className="text-xs h-8"
                      />
                    </div>
                    <div className="flex items-center gap-3">
                      <label htmlFor="profile-rate-limit-max-cost" className="text-xs text-[var(--color-muted)] w-44 shrink-0">Max cost / day ($)</label>
                      <Input
                        id="profile-rate-limit-max-cost"
                        type="number"
                        min={0}
                        step={0.01}
                        value={maxCostPerDay}
                        onChange={(e) => { markDirty(); setMaxCostPerDay(e.target.value === '' ? '' : Number(e.target.value)) }}
                        placeholder="Unlimited"
                        className="text-xs h-8"
                      />
                    </div>
                  </div>
                )}
              </div>
          </section>

          {/* Execution — ALL agents including locked core agents (operator
              decision 2026-07-03: timeout/max-tool-iterations are
              mutable on the backend for locked agents). Max tool calls
              is further hidden for subagent_3p (see below). */}
          <section className="space-y-3">
              <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Execution</p>
              <div className="space-y-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4">
                <div className="flex items-center gap-3">
                  <label htmlFor="agent-timeout-input" className="text-xs text-[var(--color-muted)] w-44 shrink-0">
                    Turn timeout
                    <span className="block text-[10px] text-[var(--color-muted)]/70">
                      Max seconds per turn. 0 = no limit.
                    </span>
                  </label>
                  <Input
                    id="agent-timeout-input"
                    type="number"
                    min={0}
                    data-testid="agent-timeout-input"
                    value={timeoutDraft}
                    onChange={(e) => {
                      const raw = e.target.value
                      // Item 5 (draft-field dirty gap): mark dirty on EVERY
                      // keystroke, not only once the draft commits to a
                      // valid value. `markDirty()` only gates hydration —
                      // it triggers no save on its own — so this is safe;
                      // without it, an external hydration mid-edit (while
                      // the draft holds an invalid/partial value that
                      // hasn't committed to `timeoutSeconds` yet) could
                      // silently reset the field the operator is still
                      // typing into.
                      markDirty()
                      setTimeoutDraft(raw)
                      const parsed = Number(raw)
                      // Commit (and autosave) only a real value; in-progress
                      // typing (empty/partial input) never persists.
                      if (raw !== '' && Number.isInteger(parsed) && parsed >= 0) {
                        setTimeoutSeconds(parsed)
                      }
                    }}
                    onBlur={() => setTimeoutDraft(String(timeoutSeconds))}
                    className="text-xs h-8"
                  />
                </div>
                {/* Max tool calls per turn — excluded for subagent_3p
                    (agent-types-field-matrix.md, Decisions #1 (resolved
                    2026-07-03): excluded): the external CLI runs its own
                    tool loop, and the backend now rejects the field for
                    this type. */}
                {!isExternalAgent && (
                  <div className="flex items-center gap-3">
                    <label htmlFor="agent-max-tool-calls-input" className="text-xs text-[var(--color-muted)] w-44 shrink-0">
                      Max tool calls per turn
                      <span className="block text-[10px] text-[var(--color-muted)]/70">
                        Per single turn (one message, task, or heartbeat run) — the
                        turn pauses at the limit and can be continued. Default: 200.
                      </span>
                    </label>
                    <Input
                      id="agent-max-tool-calls-input"
                      type="number"
                      min={1}
                      data-testid="agent-max-tool-calls-input"
                      value={maxToolIterationsDraft}
                      onChange={(e) => {
                        const raw = e.target.value
                        // Item 5 (draft-field dirty gap): mark dirty on every
                        // keystroke — see the turn-timeout input's onChange
                        // above for why this must not be gated on validity.
                        markDirty()
                        setMaxToolIterationsDraft(raw)
                        const parsed = Number(raw)
                        // Commit (and autosave) only a real value >= 1; clearing
                        // the field to type never persists a 0 again.
                        if (raw !== '' && Number.isInteger(parsed) && parsed >= 1) {
                          setMaxToolIterations(parsed)
                        }
                      }}
                      onBlur={() => setMaxToolIterationsDraft(String(maxToolIterations))}
                      className="text-xs h-8"
                    />
                  </div>
                )}
                {/* ADR-066 D9 (T068-30): per-agent context window override
                    plus the read-only effective window · source line and
                    the clamp indicator (FR-037, FR-002). The three read-only
                    fields are optional on the wire (absent until the
                    resolver lands) — nothing renders for them when omitted.
                    Hidden for subagent_3p: the external CLI owns its own
                    window (exempt row, effective 0). */}
                {!isExternalAgent && (
                  <div className="space-y-1">
                    <div className="flex items-center gap-3">
                      <label htmlFor="agent-context-window-override-input" className="text-xs text-[var(--color-muted)] w-44 shrink-0">
                        Context window override
                        <span className="block text-[10px] text-[var(--color-muted)]/70">
                          Tokens. Lower-only — never above the model's own limit. Empty = use the model's window.
                        </span>
                      </label>
                      <Input
                        id="agent-context-window-override-input"
                        type="number"
                        min={1}
                        data-testid="agent-context-window-override-input"
                        value={contextWindowOverrideDraft}
                        placeholder="Model default"
                        onChange={(e) => {
                          const raw = e.target.value
                          markDirty()
                          setContextWindowOverrideDraft(raw)
                          if (raw === '') {
                            // Clearing is a real intent (send null → backend
                            // clears the override), not in-progress typing.
                            setContextWindowOverride(null)
                            return
                          }
                          const parsed = Number(raw)
                          // Commit (and autosave) only a real value >= 1; a
                          // partial/invalid draft never persists.
                          if (Number.isInteger(parsed) && parsed >= 1) {
                            setContextWindowOverride(parsed)
                          }
                        }}
                        onBlur={() => setContextWindowOverrideDraft(
                          contextWindowOverride != null ? String(contextWindowOverride) : '',
                        )}
                        className="text-xs h-8"
                      />
                    </div>
                    {agent?.context_window_effective !== undefined && (
                      <p
                        data-testid="agent-context-window-effective"
                        className="pl-[11.75rem] text-[11px] text-[var(--color-muted)]"
                      >
                        Effective window: {formatWindowTokens(agent.context_window_effective)} tokens
                        {agent.context_window_source
                          ? ` · Source: ${CONTEXT_WINDOW_SOURCE_LABEL[agent.context_window_source]}`
                          : ''}
                      </p>
                    )}
                    {agent?.context_window_clamped && (
                      <p
                        data-testid="agent-context-window-clamped"
                        role="status"
                        className="pl-[11.75rem] text-[11px] text-[var(--color-warning,#D4AF37)]"
                      >
                        Override clamped to the model's limit
                        {agent.context_window_effective !== undefined
                          ? ` (${formatWindowTokens(agent.context_window_effective)} tokens)`
                          : ''}
                        {' '}— the value above is higher than this model supports.
                      </p>
                    )}
                  </div>
                )}
              </div>
            </section>

          {/* Shell deny patterns — item 3 reorg: relocated from Basics into
              Advanced. A shell-hardening hint, independent of the (removed)
              per-agent sandbox-profile concept. Editable for ALL agents
              including locked core agents, and for native Subagents
              (matrix: O or inherit); hidden ONLY for subagent_3p
              (external-cli) — the external runner manages its own
              isolation. */}
          {!isExternalAgent && (
            <section className="space-y-3">
              <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden">
                <button tabIndex={0}
                  type="button"
                  onClick={() => setShellAdvancedOpen((o) => !o)}
                  className="flex items-center justify-between w-full px-3 py-2.5 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
                  aria-expanded={shellAdvancedOpen}
                >
                  <span className="font-headline font-semibold text-[14px]">Shell deny patterns</span>
                  {shellAdvancedOpen ? <CaretUp size={13} /> : <CaretDown size={13} />}
                </button>
                {shellAdvancedOpen && (
                  <div className="px-3 pb-3 border-t border-[var(--color-border)]">
                    <ShellDenyPatternsEditor
                      value={shellDenyPatterns}
                      onChange={(patterns) => { markDirty(); setShellDenyPatterns(patterns) }}
                    />
                  </div>
                )}
              </div>
            </section>
          )}

          {/* Executor summary — all workers (base + external). subagent_3p's
              full editor is in the Runtime tab. Locked core workers are
              handled by their locked-banner and field-level disable. The
              selector itself shows "native" for native workers; the
              native-worker-delegation-callout at the top of the body
              already explains why the editable Tools / Skills
              accordions are collapsed to a summary. */}
          {isNativeWorkerAgent && (
            <section className="space-y-3">
              <div className="flex items-center gap-2">
                <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Executor</p>
                {(executor?.kind === 'external-cli' || executor?.kind === 'remote-a2a') && (
                  <span className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-[var(--color-surface-3)] text-[var(--color-muted)] border border-[var(--color-border)]">
                    {executor.kind === 'external-cli' ? (executor.cli ?? 'external') : 'A2A'}
                  </span>
                )}
                {(!executor || executor.kind === 'native') && (
                  <span
                    data-testid="executor-native-badge"
                    className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-[var(--color-surface-3)] text-[var(--color-muted)] border border-[var(--color-border)]"
                  >
                    Native (in-process)
                  </span>
                )}
              </div>
              <ExecutorSelector
                value={executor}
                agentId={resolvedAgentId}
                disabled={isLocked}
                onChange={(next) => { markDirty(); setExecutor(next) }}
              />
            </section>
          )}

          {/* Activity */}
          <section className="space-y-3">
            <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Activity</p>
            {agent.stats && (
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                <StatCard label="Sessions" value={agent.stats.total_sessions.toString()} />
                <StatCard
                  label="Total tokens"
                  value={formatTokens(agent.stats.total_tokens)}
                />
                <StatCard
                  label="Last active"
                  value={
                    agent.stats.last_active
                      ? new Date(agent.stats.last_active).toLocaleDateString()
                      : '—'
                  }
                />
              </div>
            )}
            {allActivityResp?.warning && (
              // Task 2 fix: fetchActivity() returns a `warning` when a session
              // store was unreadable — the returned `events` list is a PARTIAL
              // result. Surface it inline (mirrors the workspaceUnavailable
              // banner pattern below) so a partial-data 200 isn't silently
              // indistinguishable from "no activity ever happened".
              <div
                data-testid="activity-warning-banner"
                className="rounded-md border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2 flex items-start gap-2"
                role="alert"
              >
                <Warning size={14} weight="fill" className="text-[var(--color-warning)] shrink-0 mt-0.5" aria-hidden="true" />
                <p className="text-xs text-[var(--color-warning)]">
                  Showing partial activity — {allActivityResp.warning}
                </p>
              </div>
            )}
            {activityError ? (
              <p className="text-sm text-[var(--color-error)]">Failed to load activity</p>
            ) : recentActivity.length === 0 ? (
              <p className="text-xs text-[var(--color-muted)]">No recent activity for this agent.</p>
            ) : (
              <div className="space-y-1">
                {recentActivity.map((event) => (
                  <ActivityRow key={event.id} event={event} />
                ))}
              </div>
            )}
          </section>
        
    </div>
  )

  // FR-016 / US-5: Heartbeat tab panel — only rendered when showHeartbeatTab.
  // Reads/writes member_configs[agentId].heartbeat on the workspace (separate
  // workspace mutation, NOT the agent autosave — A2/F-09).
  // fix-1 (DATA-LOSS guard): when the workspace query is still loading or has
  // errored, render a blocking error/retry state instead of the form so the
  // user cannot save against an empty member_configs base.
  const workspaceUnavailable = !!editAgentWorkspaceId && (isWorkspaceLoading || isWorkspaceError || workspaceData === undefined)

  const heartbeatPanel = showHeartbeatTab ? (
    <div className="space-y-5">
      {workspaceUnavailable && (
        <div
          data-testid="heartbeat-workspace-error"
          className="rounded-md border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 flex items-start gap-3"
          role="alert"
        >
          <WarningCircle size={16} weight="fill" className="text-[var(--color-error)] shrink-0 mt-0.5" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-[var(--color-error)]">
              {isWorkspaceLoading ? 'Loading workspace…' : 'Failed to load workspace'}
            </p>
            {isWorkspaceError && (
              <p className="text-[11px] text-[var(--color-muted)] mt-0.5">
                Heartbeat settings cannot be saved until the workspace reloads. Check your connection and retry.
              </p>
            )}
          </div>
        </div>
      )}
      <section className="space-y-3">
        <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">
          Heartbeat for this workspace
        </p>
        <p className="text-xs text-[var(--color-muted)]">
          Configure a recurring heartbeat prompt for this agent in this workspace.
          The heartbeat runs on the schedule you set and uses the body below as its
          prompt — independent of any other workspace this agent belongs to.
        </p>

        {/* Enable toggle */}
        <div
          data-testid="heartbeat-enabled-row"
          className="flex items-center justify-between gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5"
        >
          <div className="min-w-0">
            <p className="text-sm text-[var(--color-secondary)]">Enable heartbeat</p>
            <p className="text-[11px] text-[var(--color-muted)] leading-snug">
              Runs the agent on the interval below using the body as its prompt.
            </p>
          </div>
          <Switch
            data-testid="heartbeat-enabled-switch"
            checked={hbEnabled}
            onCheckedChange={(v) => { markHbDirty(); setHbEnabled(v) }}
            aria-label={hbEnabled ? 'Disable heartbeat' : 'Enable heartbeat'}
          />
        </div>

        {/* Interval */}
        <div className="flex items-center gap-3">
          <label
            htmlFor="heartbeat-interval"
            className="text-xs text-[var(--color-muted)] w-44 shrink-0"
          >
            Interval (minutes)
            <span className="block text-[10px] text-[var(--color-muted)]/70">
              Minimum 5 minutes
            </span>
          </label>
          <Input
            id="heartbeat-interval"
            data-testid="heartbeat-interval-input"
            type="number"
            min={5}
            value={hbIntervalDraft}
            onChange={(e) => {
              const raw = e.target.value
              // Item 5 (draft-field dirty gap): mark dirty on every
              // keystroke, not only once a valid value commits — see the
              // Basics-tab draft inputs for the same fix and rationale.
              markHbDirty()
              setHbIntervalDraft(raw)
              // Commit only a real, valid value; in-progress typing (empty,
              // or an intermediate value below the 5-minute floor on its way
              // to a larger number) never persists.
              if (raw !== '' && Number.isInteger(Number(raw)) && Number(raw) >= 5) {
                setHbIntervalMinutes(Number(raw))
              }
            }}
            onBlur={() => setHbIntervalDraft(String(hbIntervalMinutes))}
            className="text-xs h-8"
          />
        </div>

        {/* Body */}
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <label
              htmlFor="heartbeat-body"
              className="text-xs text-[var(--color-muted)]"
            >
              Heartbeat body
              {hbEnabled && (
                <span className="text-[var(--color-error)] ml-1" aria-label="required">*</span>
              )}
            </label>
            <UploadMdButton
              onUpload={(content: string) => { markHbDirty(); setHbBody(content) }}
            />
          </div>
          <Textarea
            id="heartbeat-body"
            data-testid="heartbeat-body-textarea"
            value={hbBody}
            onChange={(e) => { markHbDirty(); setHbBody(e.target.value) }}
            rows={6}
            placeholder="Periodic instruction prompt — e.g. 'Summarise overnight CI results and update the project board.'"
            className="text-sm resize-none"
            aria-required={hbEnabled}
            aria-describedby={hbEnabled && hbBody.trim() === '' ? 'heartbeat-body-required-hint' : undefined}
            maxLength={16384}
          />
          {hbEnabled && hbBody.trim() === '' && (
            <p
              id="heartbeat-body-required-hint"
              data-testid="heartbeat-body-required-hint"
              className="text-xs text-[var(--color-error)]"
            >
              Body is required when heartbeat is enabled.
            </p>
          )}
        </div>
      </section>

      {/* Save button — explicit (not autosave) per A2/F-09 */}
      {/* fix-1 (DATA-LOSS guard): disabled when workspace data is unavailable */}
      <div className="flex justify-end pt-2">
        <Button
          data-testid="heartbeat-save-button"
          onClick={() => {
            if (workspaceUnavailable) {
              addToast({ message: 'Workspace not loaded — cannot save heartbeat settings.', variant: 'error' })
              return
            }
            if (hbEnabled && hbBody.trim() === '') {
              addToast({ message: 'Heartbeat body is required when enabled.', variant: 'error' })
              return
            }
            saveHeartbeatMutation.mutate()
          }}
          disabled={saveHeartbeatMutation.isPending || workspaceUnavailable}
          className="px-4"
        >
          {saveHeartbeatMutation.isPending ? 'Saving…' : 'Save heartbeat'}
        </Button>
      </div>
    </div>
  ) : null

  return (
    <ProfileSheet
      isOpen={isOpen}
      onClose={handleCloseAgentSheet}
      title={`Edit ${agent.name}`}
      onOpenAutoFocus={handleOpenAutoFocus}
      contentRef={sheetContentRef}
    >
      {/* Title row locked to 44px chrome; badges/description sit below so the
          open panel aligns with the workspace top bar (flat shell chrome). */}
      <SheetHeader className="px-6 sm:px-8 pr-14">
        <div className="flex items-center gap-2.5 min-w-0">
          <AvatarHeader
            color={selectedColor}
            className="w-7 h-7 rounded-full flex items-center justify-center shrink-0 [&>svg]:!w-3.5 [&>svg]:!h-3.5"
          />
          <h1 className="font-headline text-sm font-semibold text-[var(--color-secondary)] truncate">
            {agent.name}
          </h1>
        </div>
      </SheetHeader>
      <div className="px-6 sm:px-8 pb-3 flex items-center gap-2 min-w-0 flex-wrap">
        <Badge variant={agent.type === 'core' ? 'secondary' : 'outline'}>
          {agent.type}
        </Badge>
        {agent.locked && (
          <Badge variant="outline" className="text-[var(--color-muted)] border-[var(--color-border)]">
            read-only
          </Badge>
        )}
        {agent.description && (
          <span className="text-xs text-[var(--color-muted)] truncate">{agent.description}</span>
        )}
      </div>

      {/* Wave 5 / spec §6 BDD #13 + §6.4: locked-banner for built-in core
          agents. Pinned at the top of the body, above the tab bar, so the
          operator sees it before any field interactions. Uses the same
          amber/warning visual language as the executor-external-cli
          callout (sibling concept — "this agent is special, read the
          caveat before editing"). Hidden for non-locked agents.
          ADR-049 SD-C18 / ADR-052 FR-038: extended to System agents (the
          Judge) too, with copy naming what IS still editable (model,
          provider, soul) rather than the core banner's blanket "most
          fields are read-only". Soul/rubric unification (FR-038) means
          there is no longer a separate "rubric" field — the Judge's soul
          IS its judging rubric, and the wire contract describes it as
          "editable while locked". Verified against the live backend
          (pkg/gateway/rest.go's updateAgent, Fix-Wave-2): the
          locked-identity reject-set now carves soul out for
          `IsSystem()` agents specifically — identity
          (name/description/color/icon/skills) stays locked, soul does
          not — so this copy states the true, current behaviour. */}
      {agent.type === 'core' && agent.locked && (
        <div
          role="alert"
          data-testid="locked-banner"
          className="mx-8 mt-4 rounded-md border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 flex items-start gap-3"
        >
          <WarningCircle className="h-5 w-5 text-[var(--color-error)] shrink-0 mt-0.5" weight="fill" aria-hidden="true" />
          <div className="text-sm">
            <div className="font-semibold text-[var(--color-error)]">This is a built-in core agent</div>
            <div className="text-[var(--color-muted)] mt-1">
              Most fields are read-only. To create your own chat colleague, use the + Add Main button.
            </div>
          </div>
        </div>
      )}
      {isSystemAgent && agent.locked && (
        <div
          role="alert"
          data-testid="locked-banner"
          className="mx-8 mt-4 rounded-md border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 flex items-start gap-3"
        >
          <WarningCircle className="h-5 w-5 text-[var(--color-error)] shrink-0 mt-0.5" weight="fill" aria-hidden="true" />
          <div className="text-sm">
            <div className="font-semibold text-[var(--color-error)]">System agent</div>
            <div className="text-[var(--color-muted)] mt-1">
              Identity (name, description, color, icon, skills) is locked.
              Model, provider, and its soul (below, in Personality) are
              editable — the soul defines this agent&apos;s verification
              standards and drives the next verification it runs.
            </div>
          </div>
        </div>
      )}

      {/* Scrollable body. Inner padding/width mirrors CreateAgentModal etc. */}
      <div className="flex-1 overflow-y-auto">
        <div className="max-w-2xl mx-auto px-8 py-6 space-y-4">
      {/* W6-B1 / I1: cap the visible-on-open section count at Miller's 7±2.
          Base agents open Identity + Shell deny patterns + Model Configuration
          + Behavior (4 accordions — the Identity strip header is also
          visible above, so the user sees 5 top-level chunks). Workers
          replace Behavior with Executor + Tools & Permissions (Tools is
          priority for a worker since it's their run-time surface;
          Behavior's persona/heartbeat sub-blocks don't apply). Schedules,
          Sessions, Activity stay collapsed — they're reference material,
          not editing surfaces. */}
      {/* W6-C1 / M11 (copy updated 2026-07-03 per agent-types-field-matrix.md):
          native-worker delegation-only callout. Native Subagents are
          delegation-only labour agents — they are not chat targets. Their
          Tools and Skills settings may be set explicitly below OR
          left to inherit from the delegating caller at run time (matrix: O
          or inherit). Tools and Skills are live-editable for
          native Subagents and take effect at the next delegated run.
          Surface this prominently at the top of the profile so the
          operator understands the delegation-only framing before touching
          the rest of the form. */}
      {isNativeWorkerAgent && (
        <div
          data-testid="native-worker-delegation-callout"
          role="note"
          aria-label="Delegation-only worker"
          className="rounded-md border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/10 px-4 py-3 flex items-start gap-3"
        >
          <Lightning
            size={16}
            weight="fill"
            className="text-[var(--color-accent)] mt-0.5 shrink-0"
            aria-hidden="true"
          />
          <div className="min-w-0">
            <p className="text-sm font-medium text-[var(--color-secondary)]">
              This is a delegation-only worker
            </p>
            <p className="text-[12px] text-[var(--color-muted)] leading-snug mt-0.5">
              Never a chat target — this agent only runs when another agent
              delegates a task to it. Tools and Skills below may be
              set explicitly or left to inherit from the delegating caller at
              run time.
            </p>
          </div>
        </div>
      )}
            <Tabs defaultValue="basics" className="hidden sm:block w-full">
        {/* Tab order (item 4 reorg): Basics, Personality, Tools (or Runtime
            for external), Skills, Heartbeat, Advanced. Heartbeat moves from
            visually-first to between Skills and Advanced; defaultValue
            stays 'basics'. */}
        <TabsList className="w-full justify-start overflow-x-auto">
          <TabsTrigger value="basics" data-testid="tab-basics" className="font-headline">Basics</TabsTrigger>
          <TabsTrigger value="personality" data-testid="tab-personality" className="font-headline">Personality</TabsTrigger>
          {!isExternalAgent && (
            <TabsTrigger value="tools" data-testid="tab-tools" className="font-headline">Tools</TabsTrigger>
          )}
          {isExternalAgent && (
            <TabsTrigger value="runtime" data-testid="tab-runtime" className="font-headline">Runtime</TabsTrigger>
          )}
          {!isExternalAgent && (
            <TabsTrigger value="skills" data-testid="tab-skills" className="font-headline">Skills</TabsTrigger>
          )}
          {showHeartbeatTab && (
            <TabsTrigger value="heartbeat" data-testid="tab-heartbeat" className="font-headline">Heartbeat</TabsTrigger>
          )}
          <TabsTrigger value="advanced" data-testid="tab-advanced" className="font-headline">{advancedTabLabel}</TabsTrigger>
        </TabsList>

        {/* ── BASICS TAB ─────────────────────────────────────────────────
            Identity (name/description/default toggle/delegation policy
            summary/avatar color/icon) + Model Configuration (model selector,
            sampling parameters) + Fallback models (item 1: relocated here,
            directly below Model). Shell deny patterns moved to Advanced
            (item 3). The Executor (Spec-4) is a worker-only
            concern — for subagent_3p it is the headline of the Runtime
            tab below; for native workers (no external-cli selected) the
            whole thing is inherited from the caller so it is shown as a
            read-only summary in Advanced. */}
        <TabsContent value="basics" className="space-y-6">{basicsPanel}</TabsContent>

        {/* ── PERSONALITY TAB ────────────────────────────────────────────
            BehaviorFields (SOUL.md / Task prompt + Voice), and the
            Heartbeat sub-block for base agents (workers are
            delegation-only labour agents and never run on a schedule).
            The Execution params (timeout / max_iter) live in
            the Advanced tab per the spec matrix. */}
        <TabsContent value="personality" className="space-y-5">{personalityPanel}</TabsContent>

        {/* ── TOOLS TAB (hidden for subagent_3p) ────────────────────────
            Tool policy editor only (item 2: Skills split into its own tab
            below). External CLI workers have no Omnipus tool chain (the
            runner brings its own tools), so this panel is out of scope
            for them and the whole tab is omitted. */}
        {!isExternalAgent && (
          <TabsContent value="tools" className="space-y-6">{toolsPanel}</TabsContent>
        )}

        {/* ── RUNTIME TAB (subagent_3p only) ─────────────────────────────
            Spec-4 / §6.4: the Runtime tab is rendered for
            `subagent_3p` agents only (external CLI workers). The CLI
            itself is shown as a read-only chip (locked concept — the
            runtime kind is a property of the agent, not editable in
            v0.1.0), while cli_path / env_overrides / cli_args are the
            operator-tunable inputs (F-14). */}
        {isExternalAgent && (
          <TabsContent value="runtime" className="space-y-5">{runtimePanel}</TabsContent>
        )}

        {/* ── SKILLS TAB (hidden for subagent_3p) ──────────────────────────
            Item 2 reorg: Skills picker, live-editable for Main and native
            Subagents alike, split out of the former combined Tools tab into
            its own tab so Tools & Permissions and Skills are each a single
            clear surface. */}
        {!isExternalAgent && (
          <TabsContent value="skills" className="space-y-6">{skillsPanel}</TabsContent>
        )}

        {/* ── HEARTBEAT TAB ─────────────────────────────────────────────
            FR-016 / US-5: conditional tab — only rendered when the agent
            is opened from a workspace Team tab (editAgentWorkspaceId is
            set) and the agent is not a worker (FR-025). The tab saves to
            the workspace (separate mutation, A2/F-09). */}
        {showHeartbeatTab && (
          <TabsContent value="heartbeat" className="space-y-5">{heartbeatPanel}</TabsContent>
        )}

        {/* ── ADVANCED TAB ──────────────────────────────────────────────
            Rate limits, Execution params (timeout / max_iter), Shell deny
            patterns (item 3: relocated here from Basics), Executor summary
            (workers only; subagent_3p gets the full editor in the Runtime
            tab), Activity. The Executor here is a compact summary for
            native workers; subagent_3p's editor is in Runtime. */}
        <TabsContent value="advanced" className="space-y-6">{advancedPanel}</TabsContent>
      </Tabs>
      <Accordion type="single" collapsible defaultValue="basics" className="block sm:hidden">
        <AccordionItem value="basics">
          <AccordionTrigger data-testid="accordion-basics" className="font-headline">Basics</AccordionTrigger>
          <AccordionContent>{basicsPanel}</AccordionContent>
        </AccordionItem>
        <AccordionItem value="personality">
          <AccordionTrigger data-testid="accordion-personality" className="font-headline">Personality</AccordionTrigger>
          <AccordionContent>{personalityPanel}</AccordionContent>
        </AccordionItem>
      {!isExternalAgent && (
        <AccordionItem value="tools">
          <AccordionTrigger data-testid="accordion-tools" className="font-headline">Tools</AccordionTrigger>
          <AccordionContent>{toolsPanel}</AccordionContent>
        </AccordionItem>
      )}
      {isExternalAgent && (
        <AccordionItem value="runtime">
          <AccordionTrigger data-testid="accordion-runtime" className="font-headline">Runtime</AccordionTrigger>
          <AccordionContent>{runtimePanel}</AccordionContent>
        </AccordionItem>
      )}
      {!isExternalAgent && (
        <AccordionItem value="skills">
          <AccordionTrigger data-testid="accordion-skills" className="font-headline">Skills</AccordionTrigger>
          <AccordionContent>{skillsPanel}</AccordionContent>
        </AccordionItem>
      )}
      {showHeartbeatTab && (
        <AccordionItem value="heartbeat">
          <AccordionTrigger data-testid="accordion-heartbeat" className="font-headline">Heartbeat</AccordionTrigger>
          <AccordionContent>{heartbeatPanel}</AccordionContent>
        </AccordionItem>
      )}
        <AccordionItem value="advanced">
          <AccordionTrigger data-testid="accordion-advanced" className="font-headline">{advancedTabLabel}</AccordionTrigger>
          <AccordionContent>{advancedPanel}</AccordionContent>
        </AccordionItem>
      </Accordion>
      </div>
      </div>

      {/* Sticky footer — Wave 5 / spec §6.1: the footer carries the auto-save
          indicator (left, data-testid="last-saved-indicator") and a
          destructive Delete button (right, data-testid="delete-agent-button").
          Per spec there is NO Apply button (autosave-only) and no separate
          Close button — the Radix X in the top-right corner is the dismiss
          affordance, and SheetContent's onOpenChange wires it back here. */}
      <div className="px-8 py-4 border-t border-[var(--color-border)] bg-[var(--color-surface-1)] shrink-0 flex items-center justify-between gap-3">
        <div className="flex flex-col gap-0.5 min-w-0">
          <div data-testid="last-saved-indicator">
            <AutoSaveIndicator status={saveStatus} error={saveError} lastSavedAt={saveLastSavedAt} />
          </div>
          {/* UAT 4b: make the autosave scope explicit — edits to a shared agent
              take effect in every chat / workspace / delegation it is used in. */}
          <p
            data-testid="autosave-scope-cue"
            className="text-[11px] text-[var(--color-muted)] leading-snug"
          >
            Changes save automatically and apply everywhere this agent is used.
          </p>
        </div>
        {!isLocked && (
          <Button
            variant="destructive"
            data-testid="delete-agent-button"
            onClick={() => setDeleteOpen(true)}
            className="ml-auto"
          >
            <Trash size={13} className="mr-1.5" />
            Delete agent
          </Button>
        )}
      </div>

      {/* Wave 5 / spec §6.1 BDD #15: Delete confirmation dialog
          (`AlertDialog` + `AlertDialogAction`) so the
          destructive-confirm flow is identical across the app. The confirm
          fires the deleteAgentMutation; on success the slide-over closes
          and the agent is removed from the list cache. */}
      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {formData.name || agent.name}?</AlertDialogTitle>
            <AlertDialogDescription>This cannot be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteAgentMutation.isPending}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleteAgentMutation.isPending}
              onClick={() => {
                if (agentId) deleteAgentMutation.mutate(agentId)
              }}
            >
              {deleteAgentMutation.isPending ? 'Deleting…' : 'Delete'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

    </ProfileSheet>
  )
}

/** Local wrapper for the slide-over primitives. Keeps the three call sites
 *  (loading / not-found / main) in sync on `open`, `onOpenChange`, side, and
 *  the column layout that lets the body and footer stick inside the sheet. */
function ProfileSheet({
  isOpen,
  onClose,
  title,
  /** W6-B1 / I7: forwarded to SheetContent's onOpenAutoFocus so the parent
   *  can capture the trigger element before Radix shifts focus. Used by
   *  AgentProfile to restore focus to the AgentCard on close. */
  onOpenAutoFocus,
  /** Item 11: forwarded to SheetContent so the parent can scope its own
   *  focus-check belt-and-braces guard to THIS instance's DOM node instead
   *  of a document-wide selector. */
  contentRef,
  children,
}: {
  isOpen: boolean
  onClose: () => void
  /**
   * Accessible title for screen readers. Rendered as a visually-hidden
   * SheetTitle so Radix Dialog's aria-labelledby points at something
   * meaningful — the visible h1 in the body is a sibling, not the
   * Dialog.Title, so without this sr-only label the dialog opens
   * with no announced name.
   */
  title: string
  onOpenAutoFocus?: (event: Event) => void
  contentRef?: React.Ref<HTMLDivElement>
  children: React.ReactNode
}) {
  return (
    <Sheet open={isOpen} onOpenChange={(o) => { if (!o) onClose() }}>
      {/* W6-B1 / I2: dropped the `widthClass="w-full sm:max-w-3xl"` literal so
          this picks up sheet.tsx's right-side default (`w-[90vw] sm:max-w-2xl`,
          i.e. 90vw on mobile, 672 px on desktop). Caps the Edit slide-over at
          ~32rem/90vw instead of 768 px / 47% of a 1440 viewport. */}
      <SheetContent
        ref={contentRef}
        side="right"
        className="flex flex-col gap-0 p-0"
        onOpenAutoFocus={onOpenAutoFocus}
        // Kept for tests/tooling that still target it by selector; the
        // focus-check guard itself now uses `contentRef` (item 11), not
        // this selector.
        data-testid="agent-profile-sheet"
      >
        <SheetTitle className="sr-only">{title}</SheetTitle>
        {children}
      </SheetContent>
    </Sheet>
  )
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 text-center">
      <div className="font-headline font-bold text-base text-[var(--color-secondary)]">{value}</div>
      <div className="text-xs text-[var(--color-muted)] mt-0.5">{label}</div>
    </div>
  )
}

interface RangeFieldProps {
  label: string
  /** #335: plain language caption shown below the label */
  caption?: string
  value: number
  min: number
  max: number
  step: number
  onChange: (v: number) => void
  format: (v: number) => string
}

function RangeField({ label, caption, value, min, max, step, onChange, format }: RangeFieldProps) {
  return (
    <div className="space-y-1 pt-3">
      <div className="flex items-center justify-between">
        <div>
          <span className="text-xs text-[var(--color-muted)]">{label}</span>
          {caption && (
            <p className="text-[10px] text-[var(--color-muted)]/70 leading-snug mt-0.5">{caption}</p>
          )}
        </div>
        <span className="text-xs font-mono text-[var(--color-secondary)]">{format(value)}</span>
      </div>
      <input tabIndex={0}
        type="range"
        data-testid={`range-field-${label.toLowerCase().replace(/\s+/g, '-')}`}
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full h-1.5 rounded-full appearance-none cursor-pointer"
        style={{
          background: `linear-gradient(to right, var(--color-accent) 0%, var(--color-accent) ${((value - min) / (max - min)) * 100}%, var(--color-border) ${((value - min) / (max - min)) * 100}%, var(--color-border) 100%)`,
        }}
      />
    </div>
  )
}

function ActivityRow({ event }: { event: ActivityEvent }) {
  const date = new Date(event.timestamp)
  return (
    <div className="flex items-start gap-3 px-3 py-2 rounded-md hover:bg-[var(--color-surface-1)] transition-colors">
      <span className="text-xs text-[var(--color-secondary)] flex-1 min-w-0 truncate">
        {event.summary}
      </span>
      <span className="text-[10px] text-[var(--color-muted)] shrink-0 mt-0.5">
        {date.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
      </span>
    </div>
  )
}

// ── Env override row helpers (mirrors wizard/Step1Identity.tsx's ExecutorInputs
// — see that file's comment for the full rationale) ─────────────────────────
// Keying rows off the KEY text itself (`key={k}-${idx}`) meant every
// keystroke in the KEY field changed the row's identity, remounting the
// input mid-word and dropping focus — and renaming a key to match an
// existing one silently MERGED the two rows into one (`Object.fromEntries`
// on a colliding key just keeps the last write). Rows below carry a
// synthetic `id` that's stable for the row's lifetime instead.
interface EnvRow {
  id: string
  key: string
  value: string
}

function envRecordToRows(record: Record<string, string>): EnvRow[] {
  return Object.entries(record).map(([key, value], i) => ({ id: `profile-env-init-${i}`, key, value }))
}

function envRowsToRecord(rows: EnvRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const row of rows) out[row.key] = row.value
  return out
}

/** Non-empty keys shared by more than one row. Empty keys (a freshly
 *  "+ Add"-ed row not yet named) are excluded from the check. */
function findEnvDuplicateKeys(rows: EnvRow[]): Set<string> {
  const seen = new Set<string>()
  const dupes = new Set<string>()
  for (const row of rows) {
    if (row.key === '') continue
    if (seen.has(row.key)) dupes.add(row.key)
    seen.add(row.key)
  }
  return dupes
}

// Wave 5 / spec §6.4: KEY=value editor for `ExecutorConfig.env_overrides`.
// The wire shape is `Record<string, string>` (per ExecutorConfig.yaml).
// Each row is two inputs (key, value) plus a remove button. New rows are
// appended; empty rows are dropped on save. Locked agents get a read-only
// static list. Renders inline within the Runtime tab.
function EnvironmentOverridesEditor({
  value,
  onChange,
  disabled,
}: {
  value: Record<string, string>
  onChange: (next: Record<string, string>) => void
  disabled?: boolean
}) {
  const [rows, setRows] = useState<EnvRow[]>(() => envRecordToRows(value))
  const rowIdCounter = useRef(rows.length)
  // What WE last pushed via `onChange` — used to tell an external reset
  // (agent switch, conflict-refresh discard) apart from the echo of our own
  // commit (e.g. the autosave round-trip re-hydrating identical data with a
  // new object reference). Only the former should resync local rows; the
  // latter would otherwise remount every row and drop focus right after an
  // autosave lands mid-typing.
  const lastPushedRef = useRef<Record<string, string>>(value)

  useEffect(() => {
    if (JSON.stringify(value) === JSON.stringify(lastPushedRef.current)) return
    lastPushedRef.current = value
    setRows(envRecordToRows(value))
     
  }, [value])

  const duplicateKeys = findEnvDuplicateKeys(rows)

  /** Pushes `rows` to the parent UNLESS a duplicate key exists — serializing
   *  a duplicate to the `Record<string,string>` wire shape would silently
   *  overwrite one entry, so a pending duplicate blocks the commit (leaving
   *  the last known-good value in place) instead of merging. */
  function commitIfValid(next: EnvRow[]) {
    if (findEnvDuplicateKeys(next).size > 0) return
    const record = envRowsToRecord(next)
    lastPushedRef.current = record
    onChange(record)
  }

  function addRow() {
    rowIdCounter.current += 1
    const next = [...rows, { id: `profile-env-${rowIdCounter.current}`, key: '', value: '' }]
    setRows(next)
    commitIfValid(next)
  }

  /** KEY edits stay local while typing — committed only on blur (see
   *  `commitKey`), which is what stops a transient duplicate from ever
   *  reaching the payload mid-rename. */
  function updateKeyDraft(id: string, key: string) {
    setRows(rows.map((row) => (row.id === id ? { ...row, key } : row)))
  }

  function commitKey() {
    commitIfValid(rows)
  }

  function updateValue(id: string, val: string) {
    const next = rows.map((row) => (row.id === id ? { ...row, value: val } : row))
    setRows(next)
    commitIfValid(next)
  }

  function removeRow(id: string) {
    const next = rows.filter((row) => row.id !== id)
    setRows(next)
    commitIfValid(next)
  }

  return (
    <div className="space-y-1.5">
      {rows.length === 0 ? (
        <p className="text-[11px] text-[var(--color-muted)] italic">No overrides configured.</p>
      ) : (
        rows.map((row, idx) => {
          const isDuplicate = row.key !== '' && duplicateKeys.has(row.key)
          return (
            <div key={row.id} className="space-y-1" data-testid={`profile-env-row-${idx}`}>
              <div className="flex items-center gap-2">
                <Input
                  value={row.key}
                  onChange={(e) => updateKeyDraft(row.id, e.target.value)}
                  onBlur={commitKey}
                  placeholder="KEY"
                  className="text-xs h-8 font-mono flex-1"
                  aria-label="Environment variable name"
                  aria-invalid={isDuplicate || undefined}
                  disabled={disabled}
                />
                <span className="text-[var(--color-muted)]">=</span>
                <Input
                  value={row.value}
                  onChange={(e) => updateValue(row.id, e.target.value)}
                  placeholder="value"
                  className="text-xs h-8 font-mono flex-1"
                  aria-label="Environment variable value"
                  disabled={disabled}
                />
                <button tabIndex={0}
                  type="button"
                  onClick={() => removeRow(row.id)}
                  className="inline-flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors disabled:opacity-50 pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
                  aria-label={`Remove env override ${row.key || 'entry'}`}
                  disabled={disabled}
                >
                  <X size={12} />
                </button>
              </div>
              {isDuplicate && (
                <p
                  className="text-[10px] text-[var(--color-error)]"
                  data-testid={`profile-env-duplicate-${idx}`}
                >
                  Duplicate key "{row.key}" — rename it before this change is saved.
                </p>
              )}
            </div>
          )
        })
      )}
      {!disabled && (
        <button tabIndex={0}
          type="button"
          data-testid="profile-env-add"
          onClick={addRow}
          className="text-[10px] text-[var(--color-accent)] hover:underline"
        >
          + Add override
        </button>
      )}
    </div>
  )
}
