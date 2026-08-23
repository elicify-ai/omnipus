import { CaretRight, MagnifyingGlass } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from '@/components/ui/sheet'
import { BrandIcon } from '@/components/ui/brand-icon'
import { BrandDisclaimer } from '@/components/ui/brand-disclaimer'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'
import { catalogLabel, catalogSubtitle } from '@/lib/catalogDisplay'

// See ProvidersSection.tsx's file header for the FIX-N legend (this file
// implements FIX-3).

// ---------------------------------------------------------------------------
// Sub-component: Provider picker Sheet (FIX-3) — search + catalog grouped by
// company, excluding already-configured entries. Selecting an entry hands off
// to the connect form (ProviderConfigSheet, mode: connect).
// ---------------------------------------------------------------------------

// Catalog entries grouped by company for this picker's rendering — built by
// ProvidersSection's buildCatalogGroups() (already-configured ids excluded,
// filtered by a free-text search over company name/label/alias) and passed
// in via the `groups` prop below. Owned here rather than in ProvidersSection
// because this file's JSX is the one reading `company`/`logoSlug`/`entries`;
// ProvidersSection only builds the data and forwards it through.
export interface CatalogGroup {
  company: string
  logoSlug: string
  entries: CatalogProvider[]
}

interface ProviderPickerSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  query: string
  onQueryChange: (query: string) => void
  groups: CatalogGroup[]
  allConfigured: boolean
  onSelect: (entry: CatalogProvider) => void
}

export function ProviderPickerSheet({
  open,
  onOpenChange,
  query,
  onQueryChange,
  groups,
  allConfigured,
  onSelect,
}: ProviderPickerSheetProps) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        widthClass="w-[90vw] sm:max-w-lg"
        className="flex h-full flex-col p-0"
        data-testid="provider-picker-sheet"
      >
        <SheetHeader className="px-6 pr-14">
          <SheetTitle>Connect a provider</SheetTitle>
        </SheetHeader>
        <SheetDescription className="px-6 pt-3 shrink-0">
          Choose a provider to connect an API key.
        </SheetDescription>

        {/* Header + search stay fixed; only the provider list below scrolls.
            (`overflow-y-auto` needs a bounded height — the previous wrapper had
            none, so long lists just clipped instead of scrolling.) */}
        <div className="flex min-h-0 flex-1 flex-col gap-4 pt-4 px-6">
          <div className="relative shrink-0">
            <MagnifyingGlass
              size={13}
              className="absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-[var(--color-muted)]"
            />
            <Input
              type="search"
              value={query}
              onChange={(e) => onQueryChange(e.target.value)}
              placeholder="Search providers…"
              className="pl-8 text-sm h-8"
              aria-label="Search providers"
              data-testid="picker-search-input"
            />
          </div>

          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto pr-1" data-testid="picker-scroll-region">
          {groups.length === 0 ? (
            <p className="text-sm text-[var(--color-muted)] text-center py-6">
              {allConfigured
                ? 'All available providers are already configured.'
                : `No providers match "${query}".`}
            </p>
          ) : (
            groups.map((group) => (
              <div key={group.company} data-testid={`picker-group-${group.company}`}>
                <div className="flex items-center gap-2 mb-1.5">
                  <BrandIcon slug={group.logoSlug} size={16} decorative />
                  <span className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wide">
                    {group.company}
                  </span>
                </div>
                <div className="space-y-1">
                  {group.entries.map((entry) => (
                    <button tabIndex={0}
                      key={entry.id}
                      type="button"
                      onClick={() => onSelect(entry)}
                      className="w-full flex items-center justify-between gap-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-left transition-colors hover:border-[var(--color-accent)]"
                      data-testid={`picker-entry-${entry.id}`}
                    >
                      <span className="min-w-0">
                        <span className="block text-sm font-medium text-[var(--color-secondary)] truncate">
                          {catalogLabel(entry)}
                        </span>
                        <span className="block text-xs text-[var(--color-muted)] truncate">
                          {catalogSubtitle(entry)}
                        </span>
                      </span>
                      <CaretRight size={12} className="shrink-0 text-[var(--color-muted)]" aria-hidden />
                    </button>
                  ))}
                </div>
              </div>
            ))
          )}

          <BrandDisclaimer className="mt-2" />
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
