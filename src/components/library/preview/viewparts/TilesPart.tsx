// TilesPart — grid with images (view-kinds-design-2026-09-03 §2.2 tiles;
// wireframe "Cards — for notes with a face"). Each record is a card: an
// image slot fed by the part's declared image property, the record's name,
// and the G3 mark when the record is excluded from totals.
//
// The image cell holds a vault-relative path this component cannot resolve
// to a servable URL without the collection root, which the result does not
// carry — so the slot renders as a neutral surface with the filename when it
// cannot render the picture. A wrong guess at a URL would 404 into a broken-
// image glyph on every card; a placeholder is honest and quiet.

import type { VaultFindRow, ViewResultPart } from '@/lib/api/generated/openapi-types'
import { cellValue, rowExcludedFromTotals } from './viewResultData'
import { ExcludedRowMark, TotalsFooter } from './PartChrome'

export function TilesPart({
  part,
  rows,
  resolveImageUrl,
}: {
  part: ViewResultPart
  rows: VaultFindRow[]
  /** Optional: vault-relative image path → servable URL. BasePreview passes
   *  one when it knows the collection root; absent, the slot is a placeholder. */
  resolveImageUrl?: (vaultPath: string) => string | undefined
}) {
  const imageProperty = part.source.image
  return (
    <div className="flex min-h-0 flex-col" data-testid="viewpart-tiles">
      <div className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-2 p-3">
        {rows.map((row) => {
          const imagePath = imageProperty === undefined ? '' : cellValue(row, imageProperty)
          const url = imagePath === '' ? undefined : resolveImageUrl?.(imagePath)
          return (
            <div
              key={row.path}
              className="flex flex-col gap-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] p-2"
              data-testid="viewpart-tile"
            >
              {url !== undefined ? (
                <img src={url} alt="" className="h-20 w-full rounded object-cover" loading="lazy" />
              ) : (
                <div
                  className="flex h-20 w-full items-center justify-center rounded bg-[var(--color-surface-3)] px-2 text-center text-[10px] text-[var(--color-muted)]"
                  data-testid="viewpart-tile-placeholder"
                >
                  {imagePath === '' ? 'No image' : imagePath.split('/').pop()}
                </div>
              )}
              <span className="flex items-baseline truncate text-[12px] text-[var(--color-secondary)]">
                {row.title}
                {rowExcludedFromTotals(row, part) && <ExcludedRowMark />}
              </span>
            </div>
          )
        })}
      </div>
      <TotalsFooter
        totals={part.totals ?? []}
        excludedCount={part.excluded_count}
        excludedReason={part.excluded_reason}
      />
    </div>
  )
}
