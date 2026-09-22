/**
 * SkillBrowser — Search & install skills from the ClawHub registry.
 *
 * Primary flow (v0.1.0): a debounced search box queries the live ClawHub
 * marketplace via `searchSkills` (GET /api/v1/skills/search). Each result can
 * be installed by its slug via `installSkillBySlug` (POST /api/v1/skills/install).
 * On a successful install the `['skills']` query is invalidated so the
 * installed-skills list refreshes, and the row is marked "Installed".
 *
 * Secondary flow: drag-drop / file picker install of a local SKILL.md package
 * via `installSkillFromFile`, gated behind a capabilities + "unverified" confirm
 * step (US-E4 / #340). Revision conflicts and other errors surface as toasts.
 */

import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  MagnifyingGlass,
  UploadSimple,
  Warning,
  ShieldWarning,
  CircleNotch,
  Package,
  CheckCircle,
} from '@phosphor-icons/react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import {
  installSkillFromFile,
  installSkillBySlug,
  searchSkills,
  fetchSkillMarketplaceStatus,
  fetchSkills,
  isApiError,
} from '@/lib/api'
import type { SkillSearchResult, SkillMarketplaceStatus } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { generateId } from '@/lib/constants'

interface SkillBrowserProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface PendingInstall {
  file: File
  /** Revision of the installed skill shown when this file was selected. */
  revision?: string
  /** Detected capabilities extracted from SKILL.md frontmatter (best-effort). */
  capabilities: string[] | null
}

/**
 * Best-effort capability extraction from SKILL.md content.
 * Looks for a `capabilities:` YAML list in the frontmatter block.
 * Returns an empty array if nothing is found — the confirm step still shows.
 */
function extractCapabilities(text: string): string[] {
  // Match capabilities list in YAML frontmatter (--- ... ---)
  const frontmatter = text.match(/^---\n([\s\S]*?)\n---/)
  if (!frontmatter) return []
  const block = frontmatter[1]
  // Match `capabilities:` followed by list items
  const capMatch = block.match(/capabilities:\s*\n((?:\s*-[^\n]+\n?)*)/)
  if (!capMatch) return []
  return capMatch[1]
    .split('\n')
    .map((l) => l.replace(/^\s*-\s*/, '').trim())
    .filter(Boolean)
}

/** useDebouncedValue returns `value` after it has been stable for `delayMs`. */
function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(id)
  }, [value, delayMs])
  return debounced
}

// ── Featured skills (shown when search box is empty) ──────────────────────────

// Popular/featured search terms curated for the empty state of the ClawHub
// browser. Clicking a suggestion fills the search box and triggers a live query.
const FEATURED_SUGGESTIONS: { label: string; query: string; description: string }[] = [
  { label: 'Web search', query: 'web search', description: 'Search the internet from your agent' },
  { label: 'Summarize', query: 'summarize', description: 'Condense documents and text' },
  { label: 'Code review', query: 'code review', description: 'Review and audit code' },
  { label: 'Email', query: 'email', description: 'Read, compose and send emails' },
  { label: 'Calendar', query: 'calendar', description: 'Manage meetings and events' },
  { label: 'Data analysis', query: 'data analysis', description: 'Analyse spreadsheets and datasets' },
]

function FeaturedSkillSuggestions({ onSelect }: { onSelect: (query: string) => void }) {
  return (
    <div className="py-[var(--space-3)] space-y-[var(--space-3)]" data-testid="skill-featured-suggestions">
      <div className="space-y-[var(--space-1)]">
        <p className="text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-secondary)]">Popular categories</p>
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Select a category or type to search the ClawHub registry.</p>
      </div>
      <div className="grid grid-cols-2 gap-[var(--space-2)]">
        {FEATURED_SUGGESTIONS.map((s) => (
          <Button
            key={s.query}
            type="button"
            variant="ghost"
            data-testid={`featured-suggestion-${s.query.replace(/\s+/g, '-')}`}
            onClick={() => onSelect(s.query)}
            className="h-auto flex-col items-start justify-start whitespace-normal gap-[var(--space-0-5)] rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] text-left font-[var(--font-weight-regular)] hover:bg-[var(--color-surface-2)] hover:border-[var(--color-accent)]/40 transition-colors"
          >
            <span className="text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-secondary)]">{s.label}</span>
            <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-snug">{s.description}</span>
          </Button>
        ))}
      </div>
    </div>
  )
}

export function SkillBrowser({ open, onOpenChange }: SkillBrowserProps) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const fileSelectionRef = useRef(0)
  const installAbortRef = useRef<AbortController | null>(null)
  const { addToast } = useUiStore()
  const activeSessionId = useSessionStore((state) => state.activeSessionId)
  const queryClient = useQueryClient()

  const [isInstalling, setIsInstalling] = useState(false)
  const [pendingInstall, setPendingInstall] = useState<PendingInstall | null>(null)

  // ── Search state ──────────────────────────────────────────────────────────
  const [query, setQuery] = useState('')
  const debouncedQuery = useDebouncedValue(query.trim(), 300)
  // Track which slugs have been installed in this session so the row flips to
  // "Installed" even before the search list is re-fetched.
  const [installedSlugs, setInstalledSlugs] = useState<Set<string>>(new Set())
  const { data: installedSkills = [] } = useQuery({
    queryKey: ['skills'],
    queryFn: fetchSkills,
    enabled: open,
  })

  // If a search/install returns 409 mid-session (the marketplace was disabled
  // after the dialog opened), flip to file-only locally so we stop offering a
  // dead browse experience until the status query refetches.
  const [marketplaceDisabledLocal, setMarketplaceDisabledLocal] = useState(false)

  // Reset transient state when the dialog closes so a re-open starts clean.
  useEffect(() => {
    if (!open) {
      fileSelectionRef.current += 1
      installAbortRef.current?.abort()
      installAbortRef.current = null
      setPendingInstall(null)
      setIsInstalling(false)
      setQuery('')
      setInstalledSlugs(new Set())
      setMarketplaceDisabledLocal(false)
    }
  }, [open])

  // ── Marketplace gating ──────────────────────────────────────────────────────
  // Fetch whether any skill marketplace is enabled. When disabled, the backend
  // returns 409 for /skills/search and /skills/install, so we render only the
  // file-install path.
  const {
    data: marketplaceStatus,
    isLoading: isStatusLoading,
  } = useQuery<SkillMarketplaceStatus>({
    queryKey: ['skill-marketplace'],
    queryFn: fetchSkillMarketplaceStatus,
    enabled: open,
    retry: false,
    staleTime: 30_000,
  })

  // Marketplace is considered enabled only when the status query has resolved
  // with enabled:true and no mid-session 409 has downgraded us to file-only.
  const marketplaceEnabled = marketplaceStatus?.enabled === true && !marketplaceDisabledLocal

  const {
    data: results = [],
    isFetching: isSearching,
    isError: isSearchError,
    error: searchError,
  } = useQuery<SkillSearchResult[]>({
    queryKey: ['skill-search', debouncedQuery],
    queryFn: () => searchSkills(debouncedQuery),
    // Only hit the registry once there's a non-empty query (an empty `q` would
    // 400 server-side) AND a marketplace is enabled.
    enabled: open && marketplaceEnabled && debouncedQuery.length > 0,
    retry: false,
    staleTime: 30_000,
  })

  // Defense: a 409 from search means the marketplace was disabled mid-session.
  // Surface a clear toast and downgrade to file-only.
  useEffect(() => {
    if (isSearchError && isApiError(searchError) && searchError.status === 409) {
      setMarketplaceDisabledLocal(true)
      addToast({ message: 'Skill marketplace is not enabled.', variant: 'error' })
    }
  }, [isSearchError, searchError, addToast])

  const searchErrorMessage = useMemo(() => {
    if (!isSearchError) return null
    if (isApiError(searchError) && searchError.status === 409) {
      return 'Skill marketplace is not enabled.'
    }
    if (isApiError(searchError) && searchError.status === 502) {
      return 'Skill registry is unavailable, try again.'
    }
    if (isApiError(searchError)) return searchError.userMessage
    if (searchError instanceof Error) return searchError.message
    return 'Search failed. Try again.'
  }, [isSearchError, searchError])

  const { mutate: doInstallBySlug, variables: installingSlug, isPending: isSlugInstalling } =
    useMutation({
      mutationFn: ({ slug, version }: { slug: string; version?: string }) =>
        installSkillBySlug(
          slug,
          version,
          installedSkills.find((skill) => skill.id === slug)?.revision,
        ),
      onSuccess: (_skill, { slug }) => {
        setInstalledSlugs((prev) => new Set(prev).add(slug))
        queryClient.invalidateQueries({ queryKey: ['skills'] })
        addToast({ message: `Skill "${slug}" installed successfully.`, variant: 'success' })
      },
      onError: (err: unknown, { slug }) => {
        // A 409 whose body mentions "marketplace" means the marketplace was
        // disabled mid-session — downgrade to file-only and use a clear message
        // distinct from the "already installed" 409.
        if (isApiError(err) && err.status === 409 && /marketplace/i.test(err.body ?? '')) {
          setMarketplaceDisabledLocal(true)
          addToast({ message: 'Skill marketplace is not enabled.', variant: 'error' })
          return
        }
        let msg: string
        if (isApiError(err) && err.status === 409) {
          msg = `Skill "${slug}" is already installed.`
        } else if (isApiError(err) && err.status === 502) {
          msg = 'Skill registry is unavailable, try again.'
        } else if (isApiError(err)) {
          msg = err.userMessage
        } else if (err instanceof Error) {
          msg = err.message
        } else {
          msg = String(err)
        }
        addToast({ message: `Failed to install "${slug}": ${msg}`, variant: 'error' })
      },
    })

  // ── File install ──────────────────────────────────────────────────────────
  async function handleFileSelected(file: File) {
    const extension = file.name.slice(file.name.lastIndexOf('.')).toLowerCase()
    if (extension !== '.md' && extension !== '.zip') {
      addToast({
        message: 'Unsupported skill file. Choose a Markdown (.md) or ZIP (.zip) package.',
        variant: 'error',
      })
      if (fileInputRef.current) fileInputRef.current.value = ''
      return
    }
    const selection = ++fileSelectionRef.current
    let capabilities: string[] | null = null
    if (extension === '.md') {
      try {
        capabilities = extractCapabilities(await file.text())
      } catch {
        addToast({ message: 'Could not read the selected file.', variant: 'error' })
        if (fileInputRef.current) fileInputRef.current.value = ''
        return
      }
    }
    if (selection !== fileSelectionRef.current || !open) return
    const skillId = file.name.slice(0, file.name.lastIndexOf('.'))
    const revision = installedSkills.find((skill) => skill.id === skillId)?.revision
    setPendingInstall({ file, capabilities, revision })
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  async function handleConfirmInstall() {
    if (!pendingInstall || isInstalling) return
    const { file, revision } = pendingInstall
    const controller = new AbortController()
    installAbortRef.current = controller
    setIsInstalling(true)
    try {
      await installSkillFromFile(
        file,
        activeSessionId ?? generateId(),
        revision,
        controller.signal,
      )
      if (controller.signal.aborted) return
      setPendingInstall(null)
      queryClient.invalidateQueries({ queryKey: ['skills'] })
      addToast({ message: `Skill "${file.name}" installed successfully.`, variant: 'success' })
    } catch (err: unknown) {
      if (controller.signal.aborted) return
      if (isApiError(err) && err.status === 409) {
        addToast({
          message: 'This installed skill changed after you reviewed it. Close and reopen the browser before replacing it.',
          variant: 'error',
        })
      } else {
        // Surface all other errors as a toast (no more silent swallow)
        const msg = isApiError(err)
          ? err.userMessage
          : err instanceof Error
          ? err.message
          : String(err)
        addToast({
          message: `Failed to install skill: ${msg || 'Unknown error'}`,
          variant: 'error',
        })
      }
    } finally {
      if (installAbortRef.current === controller) {
        installAbortRef.current = null
        setIsInstalling(false)
      }
    }
  }

  function cancelPendingInstall() {
    fileSelectionRef.current += 1
    installAbortRef.current?.abort()
    installAbortRef.current = null
    setIsInstalling(false)
    setPendingInstall(null)
  }

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>Browse Skills</DialogTitle>
            <DialogDescription>
              {isStatusLoading
                ? 'Checking the skill marketplace…'
                : marketplaceEnabled
                  ? 'Search and install skills from the ClawHub registry'
                  : 'Install a skill from a file'}
            </DialogDescription>
          </DialogHeader>

          {isStatusLoading ? (
            /* Marketplace status loading — show a spinner, never flash the
               search box before we know whether a marketplace is enabled. */
            <div
              className="flex flex-col items-center justify-center py-[var(--space-7)] gap-[var(--space-2)] text-center"
              data-testid="skill-marketplace-loading"
            >
              <CircleNotch size={26} className="animate-spin text-[var(--color-accent)]" />
              <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Checking marketplace…</p>
            </div>
          ) : marketplaceEnabled ? (
            <>
          {/* Search box */}
          <div className="relative">
            <MagnifyingGlass
              size={15}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-muted)] pointer-events-none"
            />
            <Input
              type="search"
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search skills (e.g. web search, summarize)…"
              className="pl-[var(--space-5)]"
              aria-label="Search skills"
              data-testid="skill-search-input"
            />
          </div>

          {/* Results area */}
          <div
            className="min-h-[180px] max-h-[360px] overflow-y-auto -mx-[var(--space-1)] px-[var(--space-1)]"
            data-testid="skill-search-results"
          >
            {debouncedQuery.length === 0 ? (
              <FeaturedSkillSuggestions onSelect={(term) => setQuery(term)} />
            ) : isSearchError ? (
              <div
                className="flex flex-col items-center justify-center py-[var(--space-7)] gap-[var(--space-2)] text-center"
                data-testid="skill-search-error"
              >
                <Warning size={28} weight="fill" className="text-[var(--color-error)]" />
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">{searchErrorMessage}</p>
              </div>
            ) : isSearching ? (
              <div
                className="flex flex-col items-center justify-center py-[var(--space-7)] gap-[var(--space-2)] text-center"
                data-testid="skill-search-loading"
              >
                <CircleNotch size={26} className="animate-spin text-[var(--color-accent)]" />
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Searching…</p>
              </div>
            ) : results.length === 0 ? (
              <div
                className="flex flex-col items-center justify-center py-[var(--space-7)] gap-[var(--space-2)] text-center"
                data-testid="skill-search-empty"
              >
                <Package size={28} weight="thin" className="text-[var(--color-border)]" />
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
                  No skills found for “{debouncedQuery}”.
                </p>
              </div>
            ) : (
              <ul className="flex flex-col gap-[var(--space-2)] py-[var(--space-1)]">
                {results.map((r) => {
                  const installed = installedSlugs.has(r.slug)
                  const rowInstalling = isSlugInstalling && installingSlug?.slug === r.slug
                  return (
                    <li
                      key={r.slug}
                      data-testid={`skill-result-${r.slug}`}
                      className="flex items-start justify-between gap-[var(--space-2-5)] rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)]"
                    >
                      <div className="min-w-0 space-y-[var(--space-1)]">
                        <div className="flex items-center gap-[var(--space-2)]">
                          <p className="font-medium text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] truncate">
                            {r.display_name || r.slug}
                          </p>
                          {r.version && (
                            <Badge variant="muted" className="shrink-0 font-mono">
                              v{r.version}
                            </Badge>
                          )}
                        </div>
                        {r.summary && (
                          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] line-clamp-2">
                            {r.summary}
                          </p>
                        )}
                        {r.owner_handle && (
                          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]/70">
                            by {r.owner_handle}
                          </p>
                        )}
                      </div>
                      <Button
                        size="sm"
                        variant={installed ? 'outline' : 'default'}
                        disabled={installed || rowInstalling}
                        onClick={() =>
                          doInstallBySlug({ slug: r.slug, version: r.version })
                        }
                        className="shrink-0 gap-[var(--space-1)]"
                        data-testid={`skill-install-${r.slug}`}
                      >
                        {installed ? (
                          <>
                            <CheckCircle size={14} weight="fill" /> Installed
                          </>
                        ) : rowInstalling ? (
                          <>
                            <CircleNotch size={14} className="animate-spin" /> Installing…
                          </>
                        ) : (
                          'Install'
                        )}
                      </Button>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>
            </>
          ) : (
            /* Marketplace disabled — file-install only. */
            <div
              className="flex flex-col items-center justify-center py-[var(--space-6)] gap-[var(--space-2)] text-center"
              data-testid="skill-marketplace-disabled"
            >
              <Package size={32} weight="thin" className="text-[var(--color-border)]" />
              <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)] max-w-xs">
                No skill marketplace is enabled — install a skill from a{' '}
                <span className="font-mono">SKILL.md</span>/zip file.
              </p>
            </div>
          )}

          {/* File install — secondary option when a marketplace is enabled,
              the only option when none is. Hidden during the status check. */}
          {!isStatusLoading && (
            <DialogFooter className="sm:justify-between items-center border-t border-[var(--color-border)] pt-[var(--space-2-5)]">
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                {marketplaceEnabled ? (
                  <>
                    Or install a local <span className="font-mono">SKILL.md</span> package.
                  </>
                ) : (
                  <>
                    Install a local <span className="font-mono">SKILL.md</span> package.
                  </>
                )}
              </p>
              <Button
                size="sm"
                variant="outline"
                disabled={isInstalling}
                onClick={() => fileInputRef.current?.click()}
                className="gap-[var(--space-2)]"
              >
                <UploadSimple size={14} />
                {isInstalling ? 'Installing…' : 'Install from file'}
              </Button>
              <input tabIndex={0}
                ref={fileInputRef}
                type="file"
                accept=".md,.zip,text/markdown,application/zip"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0]
                  if (file) void handleFileSelected(file)
                }}
              />
            </DialogFooter>
          )}
        </DialogContent>
      </Dialog>

      {/* Install confirmation dialog — shows capabilities + unverified notice */}
      <Dialog
        open={pendingInstall !== null}
        onOpenChange={(o) => {
          if (!o) cancelPendingInstall()
        }}
      >
        <DialogContent data-testid="skill-install-confirm-dialog" className="max-w-md">
          <DialogHeader>
            <DialogTitle>Install skill</DialogTitle>
            <DialogDescription>
              Review this skill before installing. Unverified skills run on your server
              and can access tools allowed by your agent policy.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-[var(--space-2-5)]">
            {/* Unverified notice */}
            <div
              className="flex items-start gap-[var(--space-2)] rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-[var(--space-2-5)] py-[var(--space-2)]"
              data-testid="unverified-notice"
            >
              <ShieldWarning size={15} weight="fill" className="text-[var(--color-warning)] mt-[var(--space-0-5)] shrink-0" />
              <div className="text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)] leading-relaxed">
                <span className="font-semibold">Unverified skill.</span> This skill has not been
                reviewed or signed by the Omnipus team. Only install skills you trust.
              </div>
            </div>

            {/* File name */}
            <div className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              File:{' '}
              <span className="font-mono text-[var(--color-secondary)]">
                {pendingInstall?.file.name}
              </span>
            </div>

            {/* Capabilities */}
            {pendingInstall?.capabilities === null ? (
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                Capabilities will be inspected by the server from the ZIP package during installation.
              </p>
            ) : pendingInstall && pendingInstall.capabilities.length > 0 ? (
              <div className="space-y-[var(--space-1)]">
                <p className="text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-secondary)]">
                  Declared capabilities
                </p>
                <ul className="space-y-[var(--space-0-5)]">
                  {pendingInstall.capabilities.map((cap) => (
                    <li
                      key={cap}
                      className="flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
                    >
                      <Warning size={11} className="text-[var(--color-warning)] shrink-0" />
                      {cap}
                    </li>
                  ))}
                </ul>
              </div>
            ) : (
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                No capabilities declared in this skill file.
              </p>
            )}
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={cancelPendingInstall}
            >
              Cancel
            </Button>
            <Button
              size="sm"
              data-testid="confirm-install-btn"
              onClick={() => void handleConfirmInstall()}
              disabled={isInstalling}
            >
              {isInstalling ? 'Installing...' : 'Install skill'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

    </>
  )
}
