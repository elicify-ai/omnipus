// SettingsScreen — the Settings shell (Settings redesign #1519).
//
// 11 sections in 3 labelled groups listed down the left side (FR-001), one
// section at a time on a phone with a "Settings" back button (FR-008). The
// open section is named in the address (`?tab=`), each switch adds one
// history entry (FR-002). The shell owns what every section shares: the
// search box (FR-006/FR-007), the gateway-offline notice (FR-067), the restart
// banner (FR-035/FR-039), the "Restart now?" window (FR-033), the one password
// prompt per Save (FR-026), the in-app leave question (FR-014), the skip link
// (FR-068) and the page's polite live region (FR-018/FR-071).

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useBlocker, useRouter } from '@tanstack/react-router'
import { ArrowClockwise, CaretLeft, CaretRight } from '@phosphor-icons/react'
import { ScreenHeader } from '@/components/layout/ScreenHeader'
import { Button } from '@/components/ui/button'
import { Callout, CalloutActions, CalloutDescription, CalloutTitle } from '@/components/ui/callout'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { SearchField } from '@/components/ui/search-field'
import { fetchAboutInfo } from '@/lib/api'
import { usePendingRestart } from '@/hooks/restart'
import { cn } from '@/lib/utils'
import { GatewayRestartModal } from '@/components/settings/GatewayRestartModal'
import { SettingsShellContext, type SettingsShellApi } from '@/components/settings/SettingsShellContext'
import { useSavePasswordPrompt } from '@/components/settings/useSavePasswordPrompt'
import {
  SETTINGS_GROUPS,
  SETTINGS_SECTIONS,
  searchSettings,
  sectionInfo,
  type SettingsSectionId,
} from '@/components/settings/settingsSections'
import { SettingsSectionBody } from '@/components/settings/SettingsSectionBody'
import { RestartBanner, toBannerEntries } from '@/components/settings/RestartBanner'

export interface SettingsScreenProps { // not-wire-format: React props from the route's validated search params
  /** The open section, already resolved from `?tab=` (FR-003). */
  section?: SettingsSectionId
  /** The raw `?tab=` value; on a phone a named section opens directly. */
  requestedTab?: string
  /** `?provider=&model=` — pre-fill a Limits → Model overrides row (FR-004). */
  prefillOverride?: { provider: string; model: string }
  /** `?focus=god-mode` — focus the God Mode button in Protection (FR-005). */
  focusGodMode?: boolean
  /** The old backup or restore link was followed (FR-003). */
  backupRemoved?: boolean
  /** Landed here after a port change restart (FR-063). */
  restarted?: boolean
  /** `?row=` — the setting row to scroll to and highlight (FR-075). */
  targetRow?: string
}

// How long Stay waits for the history's own restore (its step forward) to
// arrive before deciding nothing moved. Only used where the browser has no
// Navigation API to answer the question up front.
const STAY_RESTORE_WAIT_MS = 300
// U6-5: when the history's own undo is exact (the Navigation API says it moves,
// or both entries carry router state), Stay never steps back itself; it waits
// for that undo's address change. This is only a safety net so the question
// cannot stay open if the change never arrives: it closes the question and
// moves nothing. Generous, because a slow device has been seen taking ~3.5 s.
const STAY_ROUTER_UNDO_SAFETY_MS = 5000

/**
 * After Stay, will the history's own restore bring the address back? The
 * router's history undoes a blocked move by stepping the same count the other
 * way (`history.go(-delta)`). That fits a Back onto an older entry; for an
 * address typed or pasted into the bar (the newest entry, nothing ahead of
 * it) it does nothing. Undefined when the browser cannot say (no Navigation
 * API).
 */
function restoreWillMove(): boolean | undefined {
  const nav: unknown = Reflect.get(window, 'navigation')
  if (nav === null || typeof nav !== 'object' || !('canGoForward' in nav)) return undefined
  const canGoForward: unknown = Reflect.get(nav, 'canGoForward')
  return typeof canGoForward === 'boolean' ? canGoForward : undefined
}

/** Router index the history keeps in an entry's state; typed entries have none. */
function routerIndex(state: unknown): number | undefined {
  if (state === null || typeof state !== 'object') return undefined
  const index: unknown = Reflect.get(state, '__TSR_index')
  return typeof index === 'number' ? index : undefined
}

/** The browser's own Navigation API object, when it has one. */
function navigationApi(): object | undefined {
  const nav: unknown = Reflect.get(window, 'navigation')
  return nav !== null && typeof nav === 'object' ? nav : undefined
}

/** The browser's key for the current entry (Navigation API), if any. */
function currentEntryKey(): string | undefined {
  const entry: unknown = Reflect.get(navigationApi() ?? {}, 'currentEntry')
  if (entry === null || typeof entry !== 'object') return undefined
  const key: unknown = Reflect.get(entry, 'key')
  return typeof key === 'string' ? key : undefined
}

/**
 * Exact steps from the current entry to the entry with `key`, from the
 * browser's own entry list (Navigation API). Undefined when the browser has
 * no Navigation API or the entry is gone.
 */
function stepsToEntry(key: string | undefined): number | undefined {
  const nav = navigationApi()
  if (key === undefined || nav === undefined) return undefined
  const current: unknown = Reflect.get(nav, 'currentEntry')
  const entries: unknown = Reflect.get(nav, 'entries')
  if (current === null || typeof current !== 'object' || typeof entries !== 'function') return undefined
  const list: unknown = Reflect.apply(entries, nav, [])
  if (!Array.isArray(list)) return undefined
  const home: unknown = list.find(
    (entry: unknown) => entry !== null && typeof entry === 'object' && Reflect.get(entry, 'key') === key,
  )
  const from: unknown = Reflect.get(current, 'index')
  const to: unknown = home !== null && typeof home === 'object' ? Reflect.get(home, 'index') : undefined
  return typeof from === 'number' && typeof to === 'number' ? to - from : undefined
}

/**
 * U5-1: the router's history undoes a blocked browser move with
 * `history.go(-delta)`, where delta is the difference of the router index in
 * the two entries' state. An entry typed or pasted into the address bar has
 * no router state and reads as index 0, so that count can be wrong: 0 reloads
 * the page (and the browser asks its own leave question), and any other count
 * can land on the wrong entry. For this one undo, `step` replaces the router's
 * count. The replacement removes itself on the undo call or on the router
 * history's next notification (it notifies right after its undo), so no timer
 * decides when it ends; the returned function also removes it.
 */
function replaceUndoStep(step: number, subscribe: (listener: () => void) => () => void): () => void {
  const history = window.history
  const own = Object.getOwnPropertyDescriptor(history, 'go')
  const original = history.go
  let released = false
  let unsubscribe = () => {}
  const release = () => {
    if (released) return
    released = true
    unsubscribe()
    if (own) Object.defineProperty(history, 'go', own)
    else Reflect.deleteProperty(history, 'go')
  }
  Object.defineProperty(history, 'go', {
    configurable: true,
    writable: true,
    value: () => {
      release()
      original.call(history, step)
    },
  })
  unsubscribe = subscribe(release)
  return release
}

export function SettingsScreen({
  section = 'providers',
  requestedTab,
  prefillOverride,
  focusGodMode,
  backupRemoved,
  restarted,
  targetRow,
}: SettingsScreenProps = {}) {
  const info = sectionInfo(section)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)

  // ── Gateway reachability (FR-067) ──────────────────────────────────────
  const about = useQuery({ queryKey: ['about'], queryFn: fetchAboutInfo })

  // ── Restart banner + window (FR-033 to FR-035) ─────────────────────────
  const pending = usePendingRestart()
  const [restartLabels, setRestartLabels] = useState<string[] | null>(null)
  const [restartOpen, setRestartOpen] = useState(false)
  const bannerRestartRef = useRef<HTMLButtonElement>(null)

  // ── Live region (FR-018/FR-071) ────────────────────────────────────────
  const [announcement, setAnnouncement] = useState('')
  // One pending announcement at a time; a newer one replaces it, and none
  // may fire after the screen is gone.
  const announceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const announce = useCallback((text: string) => {
    setAnnouncement('')
    if (announceTimerRef.current !== null) clearTimeout(announceTimerRef.current)
    announceTimerRef.current = setTimeout(() => {
      announceTimerRef.current = null
      setAnnouncement(text)
    }, 30)
  }, [])
  useEffect(
    () => () => {
      if (announceTimerRef.current !== null) clearTimeout(announceTimerRef.current)
      announceTimerRef.current = null
    },
    [],
  )

  // ── Drafts of the open section (FR-014) and the Save bar's gold button ─
  // A section can hold more than one Save bar (Limits: Context and Agents and
  // goals); each reports under its own owner id and the page sums them.
  const [draftsByOwner, setDraftsByOwner] = useState<Record<string, number>>({})
  const [barsByOwner, setBarsByOwner] = useState<Record<string, boolean>>({})
  const draftCount = Object.values(draftsByOwner).reduce((a, b) => a + b, 0)
  const saveBarVisible = Object.values(barsByOwner).some(Boolean)
  const setDraftCount = useCallback((owner: string, count: number) => {
    setDraftsByOwner((prev) => {
      if ((prev[owner] ?? 0) === count) return prev
      const next = { ...prev }
      if (count) next[owner] = count
      else delete next[owner]
      return next
    })
  }, [])
  const setSaveBarVisible = useCallback((owner: string, visible: boolean) => {
    setBarsByOwner((prev) => {
      if (!!prev[owner] === visible) return prev
      const next = { ...prev }
      if (visible) next[owner] = true
      else delete next[owner]
      return next
    })
  }, [])

  const { requestPassword, dialog: passwordDialog } = useSavePasswordPrompt()

  // Counts row jumps (FR-075): a repeat click on the same search result
  // jumps again.
  const [rowJump, setRowJump] = useState(0)

  const shell = useMemo<SettingsShellApi>(
    () => ({
      announce,
      setDraftCount,
      offerRestart: (labels) => {
        setRestartLabels(labels)
        setRestartOpen(true)
      },
      refreshPendingRestart: () => void pending.refetch(),
      requestPassword,
      saveBarVisible,
      setSaveBarVisible,
      targetRow,
      targetRowJump: rowJump,
      focusHeading: () => headingRef.current?.focus(),
    }),
    // pending.refetch is stable per query; listing it keeps the lint honest.
    [announce, pending.refetch, requestPassword, saveBarVisible, setDraftCount, setSaveBarVisible, targetRow, rowJump],
  )

  // ── Leave guard inside the app (FR-014) ────────────────────────────────
  // US-2 AS6 / FR-014: Stay keeps the section, its changes and the address.
  // The router's history undoes a blocked browser move by stepping the same
  // count back. Its count is wrong when an entry was typed or pasted into the
  // address bar (no router state), so Stay picks the step itself (U5-1):
  //   1. the browser's own entry list (Navigation API): the exact step back
  //      to this Settings entry, however far the move went;
  //   2. otherwise the router's count, when both entries carry router state;
  //   3. otherwise one step forward, which undoes one Back.
  // An address typed into the bar is the newest entry, so a step forward does
  // nothing; Stay then steps back off it only when the address has not moved
  // (U1-9, U2-4, U4-1).
  // Known limits, only in browsers without the Navigation API:
  //   - a jump of several entries at once through the browser's history menu
  //     from or to a typed entry can end on the wrong entry (step 3 assumes
  //     one step);
  //   - when a typed entry (no router state) is involved, Stay waits
  //     STAY_RESTORE_WAIT_MS (300 ms) for that step forward. If a slow device
  //     delivers it later, Stay also steps back, the two moves cancel out and
  //     the leave question can appear a second time. Between two router
  //     entries Stay never steps back itself; it waits for the router's exact
  //     undo however late it comes (U6-5).
  const router = useRouter()
  const blocker = useBlocker({
    shouldBlockFn: () => draftCount > 0,
    withResolver: true,
    // useSaveBar registers the browser's own prompt while drafts exist.
    enableBeforeUnload: false,
  })
  // The address this section was last shown at, outside the leave question.
  const settledHref = useRef<string | null>(null)
  // The same entry's router index and browser key, to make Stay's undo exact.
  const settledIndex = useRef<number | undefined>(undefined)
  const settledKey = useRef<string | undefined>(undefined)
  // Removes a pending undo-step replacement if this screen goes away first.
  const releaseUndo = useRef<(() => void) | undefined>(undefined)
  // True from Stay until the history is back on this section; the question
  // stays on screen meanwhile, so the other address never flashes by.
  const [restoring, setRestoring] = useState(false)
  useEffect(() => {
    if (blocker.status !== 'blocked' && !restoring) {
      settledHref.current = window.location.href
      settledIndex.current = routerIndex(window.history.state)
      settledKey.current = currentEntryKey()
    }
  })
  useEffect(() => () => releaseUndo.current?.(), [])

  // ── Phone: list first (FR-008) ─────────────────────────────────────────
  const [phoneShowsSection, setPhoneShowsSection] = useState(
    requestedTab !== undefined || !!prefillOverride || !!focusGodMode || !!targetRow,
  )

  // ── Row jump (FR-075) ──────────────────────────────────────────────────
  // A search result or a `?row=` link names one setting row. Once the
  // section has drawn it, the row scrolls into view and is marked
  // (`data-settings-row-target`, `aria-current`); the stylesheet flashes it
  // once and leaves the flash out under reduced motion. Focus stays where
  // the section put it. A repeat click on the same result jumps again.
  useEffect(() => {
    const root = contentRef.current
    if (!root || !targetRow) return
    let marked: HTMLElement | null = null
    const mark = (): boolean => {
      const row = root.querySelector<HTMLElement>(`[data-settings-row="${targetRow}"]`)
      if (!row) return false
      marked = row
      row.removeAttribute('data-settings-row-target')
      // Reading layout restarts the one-shot flash on a repeat jump.
      void row.offsetWidth
      row.setAttribute('data-settings-row-target', '')
      row.setAttribute('aria-current', 'true')
      const reduce = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
      row.scrollIntoView?.({ block: 'center', behavior: reduce ? 'auto' : 'smooth' })
      return true
    }
    let observer: MutationObserver | null = null
    // The section's reads may still be loading: wait for the row to appear.
    if (!mark()) {
      observer = new MutationObserver(() => {
        if (mark()) observer?.disconnect()
      })
      observer.observe(root, { childList: true, subtree: true })
    }
    return () => {
      observer?.disconnect()
      const row = marked as HTMLElement | null
      row?.removeAttribute('data-settings-row-target')
      row?.removeAttribute('aria-current')
    }
  }, [section, targetRow, rowJump, phoneShowsSection])
  useEffect(() => {
    if (requestedTab !== undefined || prefillOverride) setPhoneShowsSection(true)
  }, [requestedTab, section, prefillOverride])

  // ── Search (FR-006/FR-007) ─────────────────────────────────────────────
  const [query, setQuery] = useState('')
  const results = useMemo(() => searchSettings(query), [query])
  const searching = query.trim().length > 0
  const resultsRef = useRef<HTMLUListElement>(null)
  const navRef = useRef<HTMLElement>(null)

  // A list link or search result only says where the person wants to go; the
  // phone shows the section and the search clears once the section really
  // changed, so Stay in the leave prompt keeps the list and the typed search
  // (FR-014 exit table). Stay returns focus to the control that was used.
  const navIntent = useRef<'list' | 'search' | null>(null)
  const stayFocus = useRef<HTMLElement | null>(null)
  const prevSection = useRef(section)
  useEffect(() => {
    if (prevSection.current === section) return
    prevSection.current = section
    if (navIntent.current) setPhoneShowsSection(true)
    if (navIntent.current === 'search') setQuery('')
    navIntent.current = null
    stayFocus.current = null
  }, [section])

  const searchInput = () => navRef.current?.querySelector<HTMLInputElement>('#settings-search-input') ?? null
  const resultLinks = () => Array.from(resultsRef.current?.querySelectorAll<HTMLElement>('a[href]') ?? [])

  // Arrow keys move between the search box and its results; Escape in a
  // result goes back to the search box (the box itself clears on Escape).
  const onSearchKeyDown = (e: React.KeyboardEvent) => {
    const links = resultLinks()
    if (!links.length) return
    const inBox = e.target === searchInput()
    const at = links.indexOf(e.target as HTMLElement)
    let next: HTMLElement | null | undefined
    if (e.key === 'ArrowDown') next = inBox ? links[0] : links[Math.min(at + 1, links.length - 1)]
    else if (e.key === 'ArrowUp' && at >= 0) next = at === 0 ? searchInput() : links[at - 1]
    else if (e.key === 'Home' && at >= 0) next = links[0]
    else if (e.key === 'End' && at >= 0) next = links[links.length - 1]
    else if (e.key === 'Escape' && at >= 0) next = searchInput()
    if (!next) return
    e.preventDefault()
    next.focus()
  }

  // ── Phone back button (FR-008, FR-014) ─────────────────────────────────
  const [phoneBackAsk, setPhoneBackAsk] = useState(false)
  const [bodyKey, setBodyKey] = useState(0)
  const phoneBackRef = useRef<HTMLButtonElement>(null)
  const showPhoneList = useCallback(() => {
    setPhoneShowsSection(false)
    // Focus lands on the open section's entry in the list.
    setTimeout(() => navRef.current?.querySelector<HTMLElement>(`[data-testid="settings-nav-${section}"]`)?.focus(), 0)
  }, [section])

  // Focus the heading when the open section changes (not on first paint).
  const firstPaint = useRef(true)
  useEffect(() => {
    if (firstPaint.current) {
      firstPaint.current = false
      return
    }
    headingRef.current?.focus({ preventScroll: true })
  }, [section])

  // FR-035 (U1-6): after Later or Escape, focus goes to the banner's Restart
  // now. Right after a Save the banner is often not drawn yet (the waiting
  // list is still being re-read), so the heading holds focus meanwhile and
  // Restart now takes it as soon as the banner appears — unless the person
  // has already moved focus elsewhere.
  const [bannerFocusPending, setBannerFocusPending] = useState(false)
  const focusAfterRestartWindow = useCallback(() => {
    setTimeout(() => {
      if (bannerRestartRef.current) {
        bannerRestartRef.current.focus()
        return
      }
      headingRef.current?.focus()
      setBannerFocusPending(true)
    }, 0)
  }, [])
  useEffect(() => {
    if (!bannerFocusPending || !bannerRestartRef.current) return
    setBannerFocusPending(false)
    const active = document.activeElement
    if (active === headingRef.current || active === document.body || active === null) bannerRestartRef.current.focus()
  }, [bannerFocusPending, pending.entries])
  useEffect(() => setBannerFocusPending(false), [section])

  const gatewayDown = about.isError

  const nav = (
    <nav ref={navRef} aria-label="Settings sections" className="space-y-[var(--space-3)]" onKeyDown={onSearchKeyDown}>
      <div>
        <SearchField
          id="settings-search-input"
          value={query}
          onValueChange={setQuery}
          placeholder="Search settings"
          aria-label="Search settings"
          data-testid="settings-search"
        />
      </div>
      <div className="sr-only" aria-live="polite" data-testid="settings-search-count">
        {searching ? `${results.length} ${results.length === 1 ? 'result' : 'results'}` : ''}
      </div>
      {searching ? (
        results.length ? (
          <ul ref={resultsRef} className="space-y-[var(--space-1)]" data-testid="settings-search-results">
            {results.map((r, i) => (
              <li key={`${r.label}-${i}`}>
                {r.section ? (
                  <Link tabIndex={0}
                    to="/settings"
                    search={r.row ? { tab: r.section, row: r.row } : { tab: r.section }}
                    onClick={() => {
                      navIntent.current = 'search'
                      setRowJump((n) => n + 1)
                      stayFocus.current = searchInput()
                      // FR-014: Stay from a search result returns to the
                      // search field. The leave prompt gives focus back to
                      // whatever had it when it opened, so hand focus to the
                      // field first when the prompt is about to ask.
                      if (draftCount > 0) stayFocus.current?.focus()
                      if (r.section === section) {
                        setQuery('')
                        setPhoneShowsSection(true)
                      }
                    }}
                    className="block rounded-md px-[var(--space-2)] py-[var(--space-2)] hover:bg-[var(--color-surface-2)]"
                  >
                    <span className="block text-[var(--color-secondary)]">{r.label}</span>
                    <span className="block text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{sectionInfo(r.section).label}</span>
                  </Link>
                ) : r.href ? (
                  <Link tabIndex={0} to={r.href} className="block rounded-md px-[var(--space-2)] py-[var(--space-2)] hover:bg-[var(--color-surface-2)]">
                    <span className="block text-[var(--color-secondary)]">{r.label}</span>
                    <span className="block text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{r.note}</span>
                  </Link>
                ) : (
                  <div className="px-[var(--space-2)] py-[var(--space-2)]">
                    <span className="block text-[var(--color-secondary)]">{r.label}</span>
                    <span className="block text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{r.note}</span>
                  </div>
                )}
              </li>
            ))}
          </ul>
        ) : (
          <Empty data-testid="settings-search-empty">
            <EmptyHeader>
              <EmptyTitle>No settings match “{query.trim()}”</EmptyTitle>
              <EmptyDescription>Try a shorter word, such as “key” or “retention”.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        )
      ) : (
        SETTINGS_GROUPS.map((group, gi) => (
          <div key={group} role="group" aria-labelledby={`settings-group-${gi}`}>
            <div
              id={`settings-group-${gi}`}
              className="px-[var(--space-2)] pb-[var(--space-1)] text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wide text-[var(--color-muted)]"
            >
              {group}
            </div>
            <ul>
              {SETTINGS_SECTIONS.filter((s) => s.group === gi).map((s) => (
                <li key={s.id}>
                  <Link tabIndex={0}
                    to="/settings"
                    search={{ tab: s.id }}
                    aria-current={s.id === section ? 'page' : undefined}
                    data-testid={`settings-nav-${s.id}`}
                    onClick={(e) => {
                      navIntent.current = 'list'
                      stayFocus.current = e.currentTarget
                      if (s.id === section) setPhoneShowsSection(true)
                    }}
                    className={cn(
                      'flex min-h-[var(--target-touch-minimum)] items-center justify-between rounded-md px-[var(--space-2)] text-[length:var(--type-body-compact-size)]',
                      s.id === section
                        ? 'bg-[var(--color-surface-2)] font-semibold text-[var(--color-secondary)]'
                        : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
                    )}
                  >
                    <span>{s.label}</span>
                    <CaretRight size={12} aria-hidden="true" className="md:hidden" />
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ))
      )}
    </nav>
  )

  return (
    <SettingsShellContext.Provider value={shell}>
      <div className="absolute inset-0 flex flex-col">
        {/* US-9 AS4 (U2-2): the first Tab stop on a fresh Settings address,
            pinned sidebar or not. The pinned sidebar comes earlier in the
            page, so this link takes the shell skip link's positive index 1
            (AppShell steps its own link aside on Settings). */}
        <a tabIndex={1}
          href="#settings-content"
          onClick={(e) => {
            // FR-068: moves focus without changing the address.
            e.preventDefault()
            contentRef.current?.focus()
          }}
          className="sr-only focus:not-sr-only focus:absolute focus:left-[var(--space-2)] focus:top-[var(--space-2)] focus:z-20 focus:rounded-md focus:bg-[var(--color-surface-1)] focus:px-[var(--space-3)] focus:py-[var(--space-2)]"
        >
          Skip to settings content
        </a>
        <ScreenHeader title="Settings" />
        <div className="flex-1 overflow-y-auto pb-[env(safe-area-inset-bottom,var(--space-0))]">
          <div className="mx-auto max-w-5xl px-[var(--space-3)] py-[var(--space-4)]">
            {gatewayDown && (
              <Callout variant="error" className="mb-[var(--space-3)]" data-testid="settings-gateway-offline">
                <CalloutTitle>Gateway information could not be loaded</CalloutTitle>
                <CalloutDescription>
                  {about.error instanceof Error && about.error.message ? `The gateway did not answer: ${about.error.message}. ` : 'The gateway did not answer. '}
                  Values may be out of date. Check that the gateway is running, then retry.
                </CalloutDescription>
                <CalloutActions>
                  <Button variant="outline" size="sm" onClick={() => void about.refetch()}>
                    <ArrowClockwise size={13} aria-hidden="true" />
                    Retry
                  </Button>
                </CalloutActions>
              </Callout>
            )}

            <div className="md:grid md:grid-cols-[minmax(0,14rem)_minmax(0,1fr)] md:gap-[var(--space-5)]">
              <div className={cn(phoneShowsSection ? 'hidden md:block' : 'block')}>{nav}</div>

              <div
                id="settings-content"
                ref={contentRef}
                tabIndex={-1}
                className={cn('min-w-0 outline-none', phoneShowsSection ? 'block' : 'hidden md:block')}
              >
                <div className="mb-[var(--space-2)] md:hidden">
                  <Button
                    ref={phoneBackRef}
                    variant="ghost"
                    size="sm"
                    // FR-070 (U1-5): 44x44 under a coarse pointer, the
                    // SegmentedControlItem pattern (design-system rule 12).
                    className="pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
                    onClick={() => {
                      if (draftCount > 0) setPhoneBackAsk(true)
                      else showPhoneList()
                    }}
                  >
                    <CaretLeft size={13} aria-hidden="true" />
                    Settings
                  </Button>
                </div>

                <RestartBanner
                  entries={toBannerEntries(pending.entries)}
                  error={pending.isError}
                  onRetry={() => void pending.refetch()}
                  onRestartNow={() => {
                    setRestartLabels(null)
                    setRestartOpen(true)
                  }}
                  restartButtonRef={bannerRestartRef}
                  restartedNotice={restarted}
                />

                <div className="mb-[var(--space-4)]">
                  <h2
                    ref={headingRef}
                    tabIndex={-1}
                    className="font-headline text-xl font-bold text-[var(--color-secondary)] outline-none"
                  >
                    {info.label}
                  </h2>
                  {info.description && (
                    <p className="mt-[var(--space-0-5)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">{info.description}</p>
                  )}
                </div>

                <SettingsSectionBody
                  key={`${section}-${bodyKey}`}
                  section={section}
                  prefillOverride={prefillOverride}
                  focusGodMode={focusGodMode}
                  backupRemoved={backupRemoved}
                />
              </div>
            </div>
          </div>
        </div>

        <div className="sr-only" aria-live="polite" data-testid="settings-announcer">
          {announcement}
        </div>

        {passwordDialog}

        <GatewayRestartModal
          open={restartOpen}
          labels={restartLabels ?? undefined}
          onClose={() => {
            setRestartOpen(false)
            focusAfterRestartWindow()
          }}
          onRestarted={() => setTimeout(() => headingRef.current?.focus(), 0)}
          // onClose and onRestarted place focus (FR-035); the window's own
          // return-to-opener would otherwise move it back to the heading.
          onCloseAutoFocus={(e) => e.preventDefault()}
        />

        <ConfirmDialog
          open={blocker.status === 'blocked' || phoneBackAsk || restoring}
          onOpenChange={(open) => {
            if (open || restoring) return
            if (blocker.status === 'blocked') {
              // Stay: nothing moves; focus goes back to the control used.
              const blockedHref = window.location.href
              const homeHref = settledHref.current
              // U5-1: a browser move (not an in-app link) is undone by the
              // router's history; make its step land on this exact entry.
              const browserMove = blocker.action === 'BACK' || blocker.action === 'FORWARD' || blocker.action === 'GO'
              const exactStep = browserMove ? stepsToEntry(settledKey.current) : undefined
              const countsKnown = routerIndex(window.history.state) !== undefined && settledIndex.current !== undefined
              const step = exactStep !== undefined && exactStep !== 0 ? exactStep : browserMove && !countsKnown ? 1 : undefined
              if (step !== undefined) releaseUndo.current = replaceUndoStep(step, (listener) => router.history.subscribe(listener))
              const willMove = exactStep !== undefined && exactStep !== 0 ? true : restoreWillMove()
              blocker.reset()
              navIntent.current = null
              const back = stayFocus.current
              stayFocus.current = null
              const done = () => {
                setRestoring(false)
                if (back) setTimeout(() => back.focus(), 0)
              }
              // U6-5: the history's own undo is exact when the browser says
              // it moves (willMove) or when both entries carry router state.
              const undoIsExact = willMove === true || (willMove === undefined && browserMove && countsKnown && step === undefined)
              if (homeHref === null || blockedHref === homeHref) {
                // An in-app move never left the address.
                done()
              } else if (undoIsExact) {
                // Wait for that undo however late it comes, and never add a
                // step back of our own: a late undo plus our step back used to
                // end one entry off, with a second question. Staying in the
                // restoring state until it lands also keeps the blocked entry
                // from being recorded as this section's home (U6-8: a second
                // Stay took its exact step from that wrong home).
                setRestoring(true)
                let finished = false
                const finish = () => {
                  if (finished) return
                  finished = true
                  window.removeEventListener('popstate', finish)
                  done()
                }
                window.addEventListener('popstate', finish)
                setTimeout(finish, STAY_ROUTER_UNDO_SAFETY_MS)
              } else {
                setRestoring(true)
                // The history ignores the next address change after a Stay;
                // when its own step forward moved nothing, this step back is
                // that change, so the section stays put.
                const stepBack = () => {
                  if (window.location.href !== blockedHref) {
                    done()
                    return
                  }
                  window.addEventListener('popstate', done, { once: true })
                  window.history.back()
                }
                if (willMove === false) {
                  setTimeout(stepBack, 0)
                } else {
                  let settled = false
                  const settle = () => {
                    if (settled) return
                    settled = true
                    window.removeEventListener('popstate', settle)
                    stepBack()
                  }
                  window.addEventListener('popstate', settle)
                  setTimeout(settle, STAY_RESTORE_WAIT_MS)
                }
              }
            }
            if (phoneBackAsk) {
              setPhoneBackAsk(false)
              setTimeout(() => phoneBackRef.current?.focus(), 0)
            }
          }}
          title={`Leave without saving ${draftCount} ${draftCount === 1 ? 'change' : 'changes'}?`}
          description={`${draftCount} unsaved ${draftCount === 1 ? 'change' : 'changes'} on this page will be lost.`}
          cancelLabel="Stay"
          confirmLabel="Leave"
          destructive
          onConfirm={() => {
            if (blocker.status === 'blocked') blocker.proceed()
            if (phoneBackAsk) {
              // Leave: the section's drafts are dropped and the list shows.
              setPhoneBackAsk(false)
              setBodyKey((k) => k + 1)
              showPhoneList()
            }
          }}
        />
      </div>
    </SettingsShellContext.Provider>
  )
}

// Kept for callers that still import the old name for the section id type.
export type SettingsTab = SettingsSectionId
