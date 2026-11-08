import * as React from 'react'
import { cn } from '@/lib/utils'

// PageHeader — the kit's page title row (W2-1 contract §2.4; R40, R36, R38).
// Presentational on purpose: it is NOT a header/banner landmark (pages must
// never carry two banners) and it reads no app state — the drawer menu
// button arrives through `leading` from the page, because `@omnipus/ui`
// ships this file without the application stores. ScreenHeader stays as is
// until the page trains swap to this component (W2-2/3/4) and W2-7 deletes it.
export interface PageHeaderProps {
  title: string
  description?: React.ReactNode
  /** R36: at most one, a default-variant Button. Always rendered last. */
  primaryAction?: React.ReactNode
  /** Secondary actions (outline or ghost buttons), rendered before the primary action. */
  actions?: React.ReactNode
  /** Content before the title, e.g. the sidebar show button in drawer mode. */
  leading?: React.ReactNode
  titleId?: string
  className?: string
  'data-testid'?: string
}

export function PageHeader({ title, description, primaryAction, actions, leading, titleId, className, 'data-testid': testId }: PageHeaderProps) {
  const hasActions = actions != null || primaryAction != null
  return (
    <div data-slot="page-header" data-testid={testId} className={cn('flex flex-wrap items-start gap-[var(--space-3)]', className)}>
      {leading}
      <div className="min-w-0 flex-1">
        <h1
          id={titleId}
          className="font-[family-name:var(--type-page-title-family)] text-[length:var(--type-page-title-size)] font-[var(--type-page-title-weight)] leading-[var(--type-page-title-line-height)] tracking-[var(--type-page-title-letter-spacing)] text-[var(--color-secondary)]"
        >
          {title}
        </h1>
        {description != null ? (
          <p className="mt-[var(--space-0-5)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">{description}</p>
        ) : null}
      </div>
      {hasActions ? (
        <div className="ml-auto flex flex-wrap items-center gap-[var(--space-2)]">
          {actions}
          {primaryAction}
        </div>
      ) : null}
    </div>
  )
}
