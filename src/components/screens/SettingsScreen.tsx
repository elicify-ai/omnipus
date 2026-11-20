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
import { Link, useBlocker } from '@tanstack/react-router'
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
import { RestartBanner } from '@/components/settings/RestartBanner'

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
}

export function SettingsScreen({
  section = 'providers',
  requestedTab,
  prefillOverride,
  focusGodMode,
  backupRemoved,
  restarted,
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
  const announce = useCallback((text: string) => {
    setAnnouncement('')
    setTimeout(() => setAnnouncement(text), 30)
  }, [])

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
    }),
    // pending.refetch is stable per query; listing it keeps the lint honest.
    [announce, pending.refetch, requestPassword, saveBarVisible, setDraftCount, setSaveBarVisible],
  )

  // ── Leave guard inside the app (FR-014) ────────────────────────────────
  const blocker = useBlocker({
    shouldBlockFn: () => draftCount > 0,
    withResolver: true,
    // useSaveBar registers the browser's own prompt while drafts exist.
    enableBeforeUnload: false,
  })

  // ── Phone: list first (FR-008) ─────────────────────────────────────────
  const [phoneShowsSection, setPhoneShowsSection] = useState(requestedTab !== undefined || !!prefillOverride || !!focusGodMode)
  useEffect(() => {
    if (requestedTab !== undefined) setPhoneShowsSection(true)
  }, [requestedTab, section])

  // ── Search (FR-006/FR-007) ─────────────────────────────────────────────
  const [query, setQuery] = useState('')
  const results = useMemo(() => searchSettings(query), [query])
  const searching = query.trim().length > 0

  // Focus the heading when the open section changes (not on first paint).
  const firstPaint = useRef(true)
  useEffect(() => {
    if (firstPaint.current) {
      firstPaint.current = false
      return
    }
    headingRef.current?.focus({ preventScroll: true })
  }, [section])

  const focusAfterRestartWindow = useCallback(() => {
    // FR-035: after Later or Escape, focus goes to the banner's Restart now.
    setTimeout(() => (bannerRestartRef.current ?? headingRef.current)?.focus(), 0)
  }, [])

  const gatewayDown = about.isError

  const nav = (
    <nav aria-label="Settings sections" className="space-y-[var(--space-3)]">
      <div>
        <SearchField
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
          <ul className="space-y-[var(--space-1)]" data-testid="settings-search-results">
            {results.map((r, i) => (
              <li key={`${r.label}-${i}`}>
                {r.section ? (
                  <Link tabIndex={0}
                    to="/settings"
                    search={{ tab: r.section }}
                    onClick={() => {
                      setQuery('')
                      setPhoneShowsSection(true)
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
                    onClick={() => setPhoneShowsSection(true)}
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
        <a tabIndex={0}
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
                  <Button variant="ghost" size="sm" onClick={() => setPhoneShowsSection(false)}>
                    <CaretLeft size={13} aria-hidden="true" />
                    Settings
                  </Button>
                </div>

                <RestartBanner
                  entries={pending.entries}
                  isError={pending.isError}
                  onRetry={() => void pending.refetch()}
                  onRestart={() => {
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
                  key={section}
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
        />

        <ConfirmDialog
          open={blocker.status === 'blocked'}
          onOpenChange={(open) => {
            if (!open && blocker.status === 'blocked') blocker.reset()
          }}
          title={`Leave without saving ${draftCount} ${draftCount === 1 ? 'change' : 'changes'}?`}
          description={`${draftCount} unsaved ${draftCount === 1 ? 'change' : 'changes'} on this page will be lost.`}
          cancelLabel="Stay"
          confirmLabel="Leave"
          destructive
          onConfirm={() => {
            if (blocker.status === 'blocked') blocker.proceed()
          }}
        />
      </div>
    </SettingsShellContext.Provider>
  )
}

// Kept for callers that still import the old name for the section id type.
export type SettingsTab = SettingsSectionId
