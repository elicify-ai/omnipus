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
 * step (US-E4 / #340). Hash-mismatch (409 with {expected,got} body) shows a
 * dedicated dialog; other errors surface as toasts.
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
  isApiError,
} from '@/lib/api'
import type { SkillSearchResult, SkillMarketplaceStatus } from '@/lib/api'
import { useUiStore } from '@/store/ui'

interface SkillBrowserProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface HashMismatchError {
  expected?: string
  got?: string
}

interface PendingInstall {
  file: File
  text: string
  /** Detected capabilities extracted from SKILL.md frontmatter (best-effort). */
  capabilities: string[]
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

export function SkillBrowser({ open, onOpenChange }: SkillBrowserProps) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [hashMismatch, setHashMismatch] = useState<HashMismatchError | null>(null)
  const [isInstalling, setIsInstalling] = useState(false)
  const [pendingInstall, setPendingInstall] = useState<PendingInstall | null>(null)

  // ── Search state ──────────────────────────────────────────────────────────
  const [query, setQuery] = useState('')
  const debouncedQuery = useDebouncedValue(query.trim(), 300)
  // Track which slugs have been installed in this session so the row flips to
  // "Installed" even before the search list is re-fetched.
  const [installedSlugs, setInstalledSlugs] = useState<Set<string>>(new Set())

  // If a search/install returns 409 mid-session (the marketplace was disabled
  // after the dialog opened), flip to file-only locally so we stop offering a
  // dead browse experience until the status query refetches.
  const [marketplaceDisabledLocal, setMarketplaceDisabledLocal] = useState(false)

  // Reset transient state when the dialog closes so a re-open starts clean.
  useEffect(() => {
    if (!open) {
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
        installSkillBySlug(slug, version),
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
    // Read the file content first, then show the confirm step
    let text: string
    try {
      text = await file.text()
    } catch {
      addToast({ message: 'Could not read the selected file.', variant: 'error' })
      if (fileInputRef.current) fileInputRef.current.value = ''
      return
    }
    const capabilities = extractCapabilities(text)
    setPendingInstall({ file, text, capabilities })
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  async function handleConfirmInstall() {
    if (!pendingInstall) return
    const { text, file } = pendingInstall
    setPendingInstall(null)
    setIsInstalling(true)
    try {
      await installSkillFromFile(text, file.name)
      queryClient.invalidateQueries({ queryKey: ['skills'] })
      addToast({ message: `Skill "${file.name}" installed successfully.`, variant: 'success' })
    } catch (err: unknown) {
      // Hash-mismatch: the backend returns a 409 whose body carries
      // {"expected": "<sha>", "got": "<sha>"}. Branch on the ApiError type
      // (status === 409) and parse err.body — NOT err.message, which only
      // contains the generic userMessage ("This conflicts…").
      if (isApiError(err) && err.status === 409) {
        let expected: string | undefined
        let got: string | undefined
        try {
          if (err.body) {
            const parsed = JSON.parse(err.body) as {
              expected?: string
              got?: string
            }
            expected = parsed.expected
            got = parsed.got
          }
        } catch {
          // Body wasn't JSON — show the dialog with blank hashes rather than
          // misclassifying as a generic error.
        }
        setHashMismatch({ expected, got })
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
      setIsInstalling(false)
    }
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
              className="flex flex-col items-center justify-center py-12 gap-2 text-center"
              data-testid="skill-marketplace-loading"
            >
              <CircleNotch size={26} className="animate-spin text-[var(--color-accent)]" />
              <p className="text-sm text-[var(--color-muted)]">Checking marketplace…</p>
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
              className="pl-9"
              aria-label="Search skills"
              data-testid="skill-search-input"
            />
          </div>

          {/* Results area */}
          <div
            className="min-h-[180px] max-h-[360px] overflow-y-auto -mx-1 px-1"
            data-testid="skill-search-results"
          >
            {debouncedQuery.length === 0 ? (
              <div className="flex flex-col items-center justify-center py-12 gap-2 text-center">
                <MagnifyingGlass size={32} weight="thin" className="text-[var(--color-border)]" />
                <p className="text-sm text-[var(--color-muted)]">
                  Type to search the ClawHub registry.
                </p>
              </div>
            ) : isSearchError ? (
              <div
                className="flex flex-col items-center justify-center py-12 gap-2 text-center"
                data-testid="skill-search-error"
              >
                <Warning size={28} weight="fill" className="text-[var(--color-error)]" />
                <p className="text-sm text-[var(--color-error)]">{searchErrorMessage}</p>
              </div>
            ) : isSearching ? (
              <div
                className="flex flex-col items-center justify-center py-12 gap-2 text-center"
                data-testid="skill-search-loading"
              >
                <CircleNotch size={26} className="animate-spin text-[var(--color-accent)]" />
                <p className="text-sm text-[var(--color-muted)]">Searching…</p>
              </div>
            ) : results.length === 0 ? (
              <div
                className="flex flex-col items-center justify-center py-12 gap-2 text-center"
                data-testid="skill-search-empty"
              >
                <Package size={28} weight="thin" className="text-[var(--color-border)]" />
                <p className="text-sm text-[var(--color-muted)]">
                  No skills found for “{debouncedQuery}”.
                </p>
              </div>
            ) : (
              <ul className="flex flex-col gap-2 py-1">
                {results.map((r) => {
                  const installed = installedSlugs.has(r.slug)
                  const rowInstalling = isSlugInstalling && installingSlug?.slug === r.slug
                  return (
                    <li
                      key={r.slug}
                      data-testid={`skill-result-${r.slug}`}
                      className="flex items-start justify-between gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5"
                    >
                      <div className="min-w-0 space-y-1">
                        <div className="flex items-center gap-2">
                          <p className="font-medium text-sm text-[var(--color-secondary)] truncate">
                            {r.display_name || r.slug}
                          </p>
                          {r.version && (
                            <Badge variant="muted" className="shrink-0 font-mono">
                              v{r.version}
                            </Badge>
                          )}
                        </div>
                        {r.summary && (
                          <p className="text-xs text-[var(--color-muted)] line-clamp-2">
                            {r.summary}
                          </p>
                        )}
                        {r.owner_handle && (
                          <p className="text-[11px] text-[var(--color-muted)]/70">
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
                        className="shrink-0 gap-1.5"
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
              className="flex flex-col items-center justify-center py-10 gap-2 text-center"
              data-testid="skill-marketplace-disabled"
            >
              <Package size={32} weight="thin" className="text-[var(--color-border)]" />
              <p className="text-sm text-[var(--color-muted)] max-w-xs">
                No skill marketplace is enabled — install a skill from a{' '}
                <span className="font-mono">SKILL.md</span>/zip file.
              </p>
            </div>
          )}

          {/* File install — secondary option when a marketplace is enabled,
              the only option when none is. Hidden during the status check. */}
          {!isStatusLoading && (
            <DialogFooter className="sm:justify-between items-center border-t border-[var(--color-border)] pt-3">
              <p className="text-xs text-[var(--color-muted)]">
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
                className="gap-2"
              >
                <UploadSimple size={14} />
                {isInstalling ? 'Installing…' : 'Install from file'}
              </Button>
              <input
                ref={fileInputRef}
                type="file"
                accept=".zip,.json,.md"
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
          if (!o) setPendingInstall(null)
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

          <div className="space-y-3">
            {/* Unverified notice */}
            <div
              className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2.5"
              data-testid="unverified-notice"
            >
              <ShieldWarning size={15} weight="fill" className="text-amber-400 mt-0.5 shrink-0" />
              <div className="text-xs text-amber-300 leading-relaxed">
                <span className="font-semibold">Unverified skill.</span> This skill has not been
                reviewed or signed by the Omnipus team. Only install skills you trust.
              </div>
            </div>

            {/* File name */}
            <div className="text-xs text-[var(--color-muted)]">
              File:{' '}
              <span className="font-mono text-[var(--color-secondary)]">
                {pendingInstall?.file.name}
              </span>
            </div>

            {/* Capabilities */}
            {pendingInstall && pendingInstall.capabilities.length > 0 ? (
              <div className="space-y-1">
                <p className="text-xs font-medium text-[var(--color-secondary)]">
                  Declared capabilities
                </p>
                <ul className="space-y-0.5">
                  {pendingInstall.capabilities.map((cap) => (
                    <li
                      key={cap}
                      className="flex items-center gap-1.5 text-xs text-[var(--color-muted)]"
                    >
                      <Warning size={11} className="text-amber-400 shrink-0" />
                      {cap}
                    </li>
                  ))}
                </ul>
              </div>
            ) : (
              <p className="text-xs text-[var(--color-muted)]">
                No capabilities declared in this skill file.
              </p>
            )}
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPendingInstall(null)}
              disabled={isInstalling}
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

      {/* Hash mismatch dialog (#109) */}
      <Dialog open={hashMismatch !== null} onOpenChange={() => setHashMismatch(null)}>
        <DialogContent data-testid="skill-hash-mismatch-dialog" className="max-w-md">
          <DialogHeader>
            <DialogTitle>Integrity check failed</DialogTitle>
            <DialogDescription>
              The skill file could not be installed because its hash does not match the expected
              value.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2 text-sm">
            <p className="text-[var(--color-error)]">
              Hash mismatch — the skill file may have been tampered with or corrupted.
            </p>
            {hashMismatch?.expected && (
              <p className="text-xs text-[var(--color-muted)]">
                Expected: <span className="font-mono">{hashMismatch.expected}</span>
              </p>
            )}
            {hashMismatch?.got && (
              <p className="text-xs text-[var(--color-muted)]">
                Got: <span className="font-mono">{hashMismatch.got}</span>
              </p>
            )}
          </div>
          <div className="flex justify-end">
            <Button size="sm" variant="outline" onClick={() => setHashMismatch(null)}>
              Dismiss
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}
