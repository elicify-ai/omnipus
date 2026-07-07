import { CheckCircle, XCircle, CaretRight, Globe } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { BrandIcon } from '@/components/ui/brand-icon'
import { providerCatalogMode } from '@/lib/agents/providerCatalog'
import { ProviderValidationBanner } from '@/components/providers/ProviderValidationBanner'
import type { ProviderValidation, Provider } from '@/lib/api/generated/openapi-types'
import type { ProviderCatalogEntry } from '@/lib/api/generated/openapi-types'

// ---------------------------------------------------------------------------
// Sub-component: a single configured-provider row (flat or inside a group)
// ---------------------------------------------------------------------------

export function ProviderRow({
  provider,
  entry,
  viaAnthropicId,
  title,
  showIcon,
  onConfigure,
  testValidation,
}: {
  provider: Provider
  entry?: ProviderCatalogEntry
  viaAnthropicId?: boolean
  title: string
  showIcon: boolean
  onConfigure: () => void
  testValidation?: ProviderValidation
}) {
  const connected = provider.status === 'connected'
  const catalogMode = providerCatalogMode(provider)
  const subtitle = entry?.subtitle

  return (
    <div
      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
      data-testid={`provider-row-${provider.id}`}
    >
      {/* Test-validation warning banner */}
      {testValidation && (
        <ProviderValidationBanner
          validation={testValidation}
          data-testid={`test-validation-banner-${provider.id}`}
        />
      )}

      <div className="flex items-center gap-3 px-4 py-3">
        {showIcon && (
          entry ? (
            <BrandIcon slug={entry.logoSlug} size={22} decorative className="shrink-0" />
          ) : (
            <Globe size={22} className="text-[var(--color-muted)] shrink-0" aria-hidden="true" />
          )
        )}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span
              className="text-sm font-medium text-[var(--color-secondary)]"
              data-testid={`provider-row-title-${provider.id}`}
            >
              {title}
            </span>
            {/* Anthropic-endpoint chip (FIX-5) — only when configured under anthropic_id */}
            {viaAnthropicId && (
              <Badge
                variant="muted"
                className="font-normal text-[10px]"
                data-testid={`anthropic-endpoint-chip-${provider.id}`}
              >
                Anthropic endpoint
              </Badge>
            )}
            {connected ? (
              <Badge data-testid={`connected-badge-${provider.id}`} variant="success" className="gap-1">
                <CheckCircle size={10} weight="fill" /> Connected
              </Badge>
            ) : provider.status === 'error' ? (
              <Badge
                variant="error"
                className="gap-1"
                data-testid={`error-badge-${provider.id}`}
              >
                <XCircle size={10} weight="fill" /> Error
              </Badge>
            ) : (
              <Badge variant="muted" data-testid={`disconnected-badge-${provider.id}`}>
                Not configured
              </Badge>
            )}
            {connected && (
              <Badge variant="muted" className="font-normal">
                {catalogMode === 'live' ? 'Live model list' : 'Manual models'}
              </Badge>
            )}
          </div>
          {subtitle && (
            <p className="text-xs text-[var(--color-muted)] mt-0.5">
              {subtitle}
            </p>
          )}
          {provider.models && provider.models.length > 0 && (
            <p className="text-xs text-[var(--color-muted)] mt-0.5 font-mono">
              {provider.models.slice(0, 3).join(', ')}{provider.models.length > 3 ? ` +${provider.models.length - 3}` : ''}
            </p>
          )}
          {provider.error && (
            <p className="text-xs text-[var(--color-error)] mt-0.5">{provider.error}</p>
          )}
        </div>

        <div className="flex items-center gap-2 shrink-0">
          <Button
            size="sm"
            onClick={onConfigure}
            className="h-7 px-3 text-xs"
            data-testid={`configure-btn-${provider.id}`}
          >
            {connected ? 'Edit' : (
              <><CaretRight size={11} /> Configure</>
            )}
          </Button>
        </div>
      </div>
    </div>
  )
}
