// PermissionCatalog — one tab's list on Skills & Tools: one search field and
// two flat filters above collapsed groups (FR-002), Set all per group
// (FR-003), the empty state (FR-004), rows leaving a filter (FR-005) with
// focus kept on a neighbour and the result announced (FR-052, S-100).

import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { CaretDown, ShieldWarning } from '@phosphor-icons/react'
import type { ModelVisibility, ToolPolicy } from '@/lib/api/generated/openapi-types'
import { Button } from '@/components/ui/button'
import { SearchField } from '@/components/ui/search-field'
import { FilterMenu } from '@/components/ui/filter-menu'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { Skeleton } from '@/components/ui/skeleton'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { PermissionSaveQueue } from '@/lib/permissionSaveQueue'
import { PermissionRow } from './PermissionRow'
import {
  LEVELS,
  LEVEL_WORDS,
  POLICIES,
  POLICY_WORDS,
  filtersActive,
  groupItems,
  levelOf,
  policyOf,
  rowMatches,
  type CatalogFilters,
  type CatalogItem,
} from './skillsToolsModel'

const AUTONOMOUS_PLAN_TOOL = 'execute_plan'

type PolicyChange = { item: CatalogItem; policy: ToolPolicy }

interface PermissionCatalogProps {
  items: readonly CatalogItem[]
  queue: PermissionSaveQueue
  /** Plural noun of the rows: "tools", "skills". */
  noun: string
  testIdPrefix: 'tool' | 'skill'
  loading: boolean
  /** Writing is off while anything the page needs is missing (S-66, S-67). */
  disabled: boolean
  announce: (text: string) => void
  /** Extra controls in an open group's header line (Web & Search → Settings). */
  groupActions?: (groupId: string) => ReactNode
  /** Controls always in a group's header line (an external service's switch, Test, Edit, Remove). */
  groupControls?: (groupId: string) => ReactNode
  /** Shown at the top of an open group (an external service's error with Retry and Edit). */
  groupNotice?: (groupId: string) => ReactNode
  /** Groups shown even with no rows (an external service that reports no tools). */
  extraGroups?: ReadonlyArray<{ id: string; label: string }>
  /** A row's own menu (More actions on a skill row). */
  rowActions?: (item: CatalogItem) => ReactNode
  /** The tab's main action beside the search line (Add service). */
  toolbarAction?: ReactNode
  /** Shown above the list on its tab (the trust block on Skills). */
  children?: ReactNode
  /** What the tab says while it loads (default "Loading <noun>…"). */
  loadingText?: string
  /** The tab's empty state, shown when the list loaded with nothing in it. */
  empty?: ReactNode
}

function emptyFilters(): CatalogFilters {
  return { search: '', policy: null, level: null }
}

export function PermissionCatalog({
  items, queue, noun, testIdPrefix, loading, disabled, announce, groupActions, groupControls, groupNotice,
  extraGroups, rowActions, toolbarAction, children, loadingText, empty,
}: PermissionCatalogProps) {
  const [filters, setFilters] = useState<CatalogFilters>(emptyFilters)
  /** The note a Set all leaves on its group: what it skipped and why (C-20). */
  const [skipNote, setSkipNote] = useState<{ group: string; text: string } | null>(null)
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set())
  const [pendingAllow, setPendingAllow] = useState<PolicyChange[] | null>(null)
  const active = filtersActive(filters)
  const groups = useMemo(() => {
    const withRows = groupItems(items)
    const missing = (extraGroups ?? []).filter((extra) => !withRows.some((group) => group.id === extra.id))
    return [...withRows, ...missing.map((extra) => ({ ...extra, items: [] as CatalogItem[] }))]
      .sort((a, b) => a.label.localeCompare(b.label))
  }, [items, extraGroups])
  const visibleGroups = groups
    .map((group) => ({ ...group, rows: group.items.filter((item) => rowMatches(queue, item, filters)) }))
    .filter((group) => !active || group.rows.length > 0)
  const shown = visibleGroups.reduce((sum, group) => sum + group.rows.length, 0)

  // A search or filter opens the groups with matches, and they stay open
  // when it is cleared; Escape / the clear control collapses them (S-47).
  const signature = `${filters.search}|${filters.policy}|${filters.level}`
  useEffect(() => {
    if (!active) return
    setExpanded((current) => {
      const next = new Set(current)
      for (const group of visibleGroups) next.add(group.id)
      return next
    })
    // Only a change of the search or filters opens groups.
  }, [signature])

  const baseId = useId()
  const focusRef = useFocusKeeper({ visibleGroups, signature, queue, announce, shown, testIdPrefix, policyFiltered: filters.policy !== null })

  const change = (changes: PolicyChange[]) => {
    if (changes.length === 0) return
    if (changes.some(({ item, policy }) => item.key === AUTONOMOUS_PLAN_TOOL && policy === 'allow')) {
      setPendingAllow(changes)
      return
    }
    queue.change(changes.map(({ item, policy }) => ({ section: item.policySection, name: item.key, value: policy })))
  }
  const changeLevel = (item: CatalogItem, level: ModelVisibility) => {
    queue.change([{ section: item.levelSection, name: item.key, value: level }])
  }
  const setAll = (groupId: string, rows: readonly CatalogItem[], policy: ToolPolicy) => {
    const { eligible, skipped } = setAllRows(rows, policy)
    change(eligible.map((item) => ({ item, policy })))
    const note = skipped.length > 0
      ? `${skipped.length} skipped: ${skipped.map((row) => row.label).join(', ')} always ${skipped.length === 1 ? 'asks' : 'ask'}.`
      : null
    // The note is its own polite status line, so it is not announced twice.
    setSkipNote(note ? { group: groupId, text: note } : null)
  }
  const clearAll = () => {
    setFilters(emptyFilters())
    setExpanded(new Set())
  }
  const toggleGroup = (id: string, open: boolean) => {
    setExpanded((current) => {
      const next = new Set(current)
      if (open) next.add(id)
      else next.delete(id)
      return next
    })
  }

  const searchDisabled = loading
  return (
    <div className="flex flex-col gap-[var(--space-3)]">
      {children}
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        <SearchField
          value={filters.search}
          onValueChange={(search) => {
            if (search === '' && filters.search !== '') setExpanded(new Set())
            setFilters((current) => ({ ...current, search }))
          }}
          placeholder={`Search ${noun}…`}
          aria-label={`Search ${noun}`}
          disabled={searchDisabled}
          className="min-w-[12rem] flex-1"
        />
        <FilterMenu
          mode="single"
          label={filters.policy ? `Permission: ${POLICY_WORDS[filters.policy]}` : 'Permission'}
          aria-label="Permission filter"
          clearLabel="All"
          value={filters.policy}
          onChange={(value) => setFilters((current) => ({ ...current, policy: value as ToolPolicy | null }))}
          options={POLICIES.map((value) => ({ value, label: POLICY_WORDS[value] }))}
          disabled={searchDisabled}
        />
        <FilterMenu
          mode="single"
          label={filters.level ? `Sees: ${LEVEL_WORDS[filters.level]}` : 'What the model sees'}
          aria-label="What the model sees"
          clearLabel="All levels"
          value={filters.level}
          onChange={(value) => setFilters((current) => ({ ...current, level: value as ModelVisibility | null }))}
          options={LEVELS.map((value) => ({ value, label: LEVEL_WORDS[value] }))}
          disabled={searchDisabled}
        />
        {toolbarAction}
      </div>
      {filters.level !== null && (
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          Denied items are not loaded, so this filter leaves them out. Use the Permission filter to find them.
        </p>
      )}
      {loading ? (
        <div className="flex flex-col gap-[var(--space-2)]">
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{loadingText ?? `Loading ${noun}…`}</p>
          {[0, 1, 2].map((n) => <Skeleton key={n} data-testid="skeleton-row" className="h-10 w-full" />)}
        </div>
      ) : empty && !active && groups.length === 0 ? (
        empty
      ) : active && shown === 0 ? (
        <div className="flex flex-col items-start gap-[var(--space-2)]">
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
            {filters.search.trim() ? `No ${noun} match "${filters.search.trim()}"` : `No ${noun} match the filters`}
          </p>
          <Button variant="outline" size="sm" className="pointer-coarse:min-h-[var(--target-touch-minimum)]" onClick={clearAll}>Clear search and filters</Button>
        </div>
      ) : (
        <div ref={focusRef} className="flex flex-col gap-[var(--space-1)]">
          {visibleGroups.map((group, index) => {
            const open = active || expanded.has(group.id)
            const bodyId = `${baseId}-group-${index}`
            return (
              <section key={group.id} data-group-id={group.id} className="rounded-md border border-[var(--color-border)] px-[var(--space-2)]">
                <div className="flex flex-wrap items-center gap-[var(--space-2)]">
                  <DisclosureRow expanded={open} expandable onExpandedChange={(next) => toggleGroup(group.id, next)} aria-controls={bodyId} data-group-header="">
                    <span className="font-medium text-[var(--color-secondary)]">{group.label}</span>
                    <span className="text-[var(--color-muted)]">{active ? `${group.rows.length} of ${group.items.length}` : group.items.length}</span>
                  </DisclosureRow>
                  {groupControls?.(group.id)}
                  {open && groupActions?.(group.id)}
                  {open && group.items.length > 0 && (
                    <SetAllMenu
                      rows={group.rows}
                      label={setAllLabel(group.rows, active)}
                      disabled={disabled}
                      onChoose={(policy) => setAll(group.id, group.rows, policy)}
                    />
                  )}
                </div>
                {/* Always mounted, so the header's aria-controls names a real element. */}
                <div id={bodyId}>
                  {open && skipNote?.group === group.id && (
                    <p role="status" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{skipNote.text}</p>
                  )}
                  {open && groupNotice?.(group.id)}
                  {open && group.rows.map((item) => (
                    <PermissionRow
                      key={item.key}
                      item={item}
                      queue={queue}
                      disabled={disabled}
                      testIdPrefix={testIdPrefix}
                      onPolicy={(row, policy) => change([{ item: row, policy }])}
                      onLevel={changeLevel}
                      onRowFocus={(key, choice) => { focusRef.lastFocus.current = { key, choice } }}
                      actions={rowActions?.(item)}
                    />
                  ))}
                </div>
              </section>
            )
          })}
        </div>
      )}
      <ConfirmDialog
        open={pendingAllow !== null}
        onOpenChange={(open) => { if (!open) setPendingAllow(null) }}
        title={(
          <span className="flex items-center gap-[var(--space-2)]">
            <ShieldWarning size={18} weight="bold" className="text-[var(--color-warning)]" />
            Confirm autonomous plan execution
          </span>
        )}
        description="Setting execute_plan to Allow enables autonomous multi-task execution without an approval prompt — once this resolves to Allow, the agent can author and run a full task plan end-to-end with no human interlock. Guardrails (concurrency cap, per-task attempt limit, idle expiry, required acceptance criteria) still apply, but no operator approval prompt will be raised."
        confirmLabel="Allow autonomous execution"
        onConfirm={() => {
          const changes = pendingAllow ?? []
          setPendingAllow(null)
          queue.change(changes.map(({ item, policy }) => ({ section: item.policySection, name: item.key, value: policy })))
        }}
      />
    </div>
  )
}

/**
 * Which rows a Set all to `policy` changes (C-20): a row with a fixed
 * permission has no value to set and is not counted; a row that cannot take
 * the value (an always-ask tool and Allow) is skipped and named.
 */
function setAllRows(rows: readonly CatalogItem[], policy: ToolPolicy) {
  const settable = rows.filter((row) => !row.fixedSentence)
  return {
    settable,
    eligible: settable.filter((row) => !row.refused.includes(policy)),
    skipped: settable.filter((row) => row.refused.includes(policy)),
  }
}

/** The group's Set all button (C-20, D4 rows 1 and 2). */
function setAllLabel(rows: readonly CatalogItem[], active: boolean): string {
  const count = rows.filter((row) => !row.fixedSentence).length
  return active ? `Set ${count} of ${rows.length} matching` : `Set all ${count}`
}

/** A Set all item: the value alone, or "Set 11 of 12 to Allow" when a row is skipped (C-20, S-84). */
function setAllItemLabel(rows: readonly CatalogItem[], policy: ToolPolicy): string {
  const { settable, eligible } = setAllRows(rows, policy)
  if (eligible.length === settable.length) return POLICY_WORDS[policy]
  return `Set ${eligible.length} of ${settable.length} to ${POLICY_WORDS[policy]}`
}

function SetAllMenu({ rows, label, disabled, onChoose }: {
  rows: readonly CatalogItem[]
  label: string
  disabled: boolean
  onChoose: (policy: ToolPolicy) => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          disabled={disabled || rows.every((row) => row.fixedSentence)}
          className="ml-auto h-7 shrink-0 gap-[var(--space-1)] text-[length:var(--type-caption-size)] pointer-coarse:min-h-[var(--target-touch-minimum)]"
        >
          {label}
          <CaretDown size={11} aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {POLICIES.map((policy) => (
          <DropdownMenuItem key={policy} onClick={() => onChoose(policy)}>{setAllItemLabel(rows, policy)}</DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

type FocusKeeperInput = {
  visibleGroups: Array<{ id: string; rows: CatalogItem[] }>
  signature: string
  queue: PermissionSaveQueue
  announce: (text: string) => void
  shown: number
  testIdPrefix: 'tool' | 'skill'
  policyFiltered: boolean
}

/**
 * When the focused row leaves the list because its saved value no longer
 * matches the filter, focus moves to the same control of the row before it
 * (else after it, else its group header) and the result is announced
 * (FR-052, S-100). A change of the filters themselves announces nothing.
 */
function useFocusKeeper({ visibleGroups, signature, queue, announce, shown, testIdPrefix, policyFiltered }: FocusKeeperInput) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const lastFocus = useRef<{ key: string; choice: string | null } | null>(null)
  const previous = useRef<{ order: Array<{ key: string; group: string; item: CatalogItem }>; signature: string }>({ order: [], signature })
  const order = visibleGroups.flatMap((group) => group.rows.map((item) => ({ key: item.key, group: group.id, item })))

  useLayoutEffect(() => {
    const before = previous.current
    previous.current = { order, signature }
    const focused = lastFocus.current
    if (!focused || before.signature !== signature) return
    if (order.some((entry) => entry.key === focused.key)) return
    const at = before.order.findIndex((entry) => entry.key === focused.key)
    if (at < 0) return
    const left = before.order[at]
    lastFocus.current = null
    const policy = policyOf(queue, left.item)
    const words = policyFiltered && policy ? POLICY_WORDS[policy] : LEVEL_WORDS[levelOf(queue, left.item)]
    announce(`${left.item.label} set to ${words}. It no longer matches the filter. ${shown} shown.`)
    const stillShown = new Set(order.map((entry) => entry.key))
    const neighbour = [...before.order.slice(0, at)].reverse().find((entry) => stillShown.has(entry.key)) ??
      before.order.slice(at + 1).find((entry) => stillShown.has(entry.key))
    const root = containerRef.current
    if (!root) return
    const active = document.activeElement
    if (active && active !== document.body && root.contains(active)) return
    const row = neighbour ? root.querySelector<HTMLElement>(`[data-testid="${testIdPrefix}-row-${CSS.escape(neighbour.key)}"]`) : null
    const target = row?.querySelector<HTMLElement>(`[data-choice="${focused.choice ?? ''}"]`) ??
      row?.querySelector<HTMLElement>('button') ??
      root.querySelector<HTMLElement>(`[data-group-id="${CSS.escape(left.group)}"] [data-group-header]`)
    target?.focus()
  })

  return Object.assign((node: HTMLDivElement | null) => { containerRef.current = node }, { lastFocus })
}
