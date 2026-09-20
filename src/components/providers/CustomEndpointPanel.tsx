// CustomEndpointPanel.tsx — ADR-068 FR-024's Custom endpoint form.
//
// The picker's last row opens this panel. It collects exactly what a custom row
// needs: an operator-chosen `id`, the `api_base`, the wire `protocol` and the
// key. The saved row is recognised everywhere afterwards by `Provider.custom:
// true` — never by a literal id (X-13), which is why the id here is free text
// the operator names rather than a value the SPA reserves.
//
// The protocol control is a native `<select>` on purpose: it is two options, it
// is keyboard-operable and screen-reader-labelled with no extra ARIA, and it
// needs no popover. A Radix Select would add both.

import * as React from 'react'
import { Plug } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'

/** The wire protocols a custom endpoint may declare (generated enum subset). */
export const CUSTOM_ENDPOINT_PROTOCOLS = ['openai-compatible', 'anthropic'] as const

export type CustomEndpointProtocol = (typeof CUSTOM_ENDPOINT_PROTOCOLS)[number]

/** What the panel emits. Mapped to `ProviderUpdateRequest` by the caller. */
export interface CustomEndpointDraft {
  id: string
  api_base: string
  protocol: CustomEndpointProtocol
  api_key: string
}

export interface CustomEndpointPanelProps {
  onSubmit: (draft: CustomEndpointDraft) => void
  onCancel?: () => void
  /** Server-side failure copy, rendered as an assertive alert (4.1.3). */
  error?: string | null
  submitting?: boolean
  'data-testid'?: string
}

export function CustomEndpointPanel({
  onSubmit,
  onCancel,
  error,
  submitting = false,
  'data-testid': testId = 'custom-endpoint-panel',
}: CustomEndpointPanelProps) {
  const [id, setId] = React.useState('')
  const [apiBase, setApiBase] = React.useState('')
  const [protocol, setProtocol] = React.useState<CustomEndpointProtocol>('openai-compatible')
  const [apiKey, setApiKey] = React.useState('')

  const complete = id.trim().length > 0 && apiBase.trim().length > 0 && apiKey.trim().length > 0

  return (
    <form
      data-testid={testId}
      aria-label="Custom endpoint"
      className="flex flex-col gap-[var(--space-2-5)] rounded-md border p-[var(--space-2-5)]"
      style={{ borderColor: 'var(--color-border)' }}
      onSubmit={(event) => {
        event.preventDefault()
        if (!complete || submitting) return
        onSubmit({
          id: id.trim(),
          api_base: apiBase.trim(),
          protocol,
          api_key: apiKey,
        })
      }}
    >
      <div className="flex items-center gap-[var(--space-2)] text-[length:var(--type-body-compact-size)]" style={{ color: 'var(--color-secondary)' }}>
        <Plug size={14} aria-hidden="true" />
        Custom endpoint
      </div>

      <label className="flex flex-col gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]" htmlFor={`${testId}-id`}>
        Provider id
        <input
          id={`${testId}-id`}
          tabIndex={0}
          data-testid="custom-endpoint-id"
          value={id}
          onChange={(e) => setId(e.target.value)}
          className="min-h-[32px] rounded border px-[var(--space-2)] text-[length:var(--type-body-compact-size)]"
          style={{ borderColor: 'var(--color-border)' }}
        />
      </label>

      <label className="flex flex-col gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]" htmlFor={`${testId}-api-base`}>
        API base URL
        <input
          id={`${testId}-api-base`}
          tabIndex={0}
          data-testid="custom-endpoint-api-base"
          value={apiBase}
          onChange={(e) => setApiBase(e.target.value)}
          className="min-h-[32px] rounded border px-[var(--space-2)] text-[length:var(--type-body-compact-size)]"
          style={{ borderColor: 'var(--color-border)' }}
        />
      </label>

      <label className="flex flex-col gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]" htmlFor={`${testId}-protocol`}>
        Protocol
        <select
          id={`${testId}-protocol`}
          tabIndex={0}
          data-testid="custom-endpoint-protocol"
          value={protocol}
          onChange={(e) => setProtocol(e.target.value as CustomEndpointProtocol)}
          className="min-h-[32px] rounded border px-[var(--space-2)] text-[length:var(--type-body-compact-size)]"
          style={{ borderColor: 'var(--color-border)' }}
        >
          {CUSTOM_ENDPOINT_PROTOCOLS.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
      </label>

      <label className="flex flex-col gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]" htmlFor={`${testId}-api-key`}>
        API key
        <input
          id={`${testId}-api-key`}
          tabIndex={0}
          data-testid="custom-endpoint-api-key"
          type="password"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          className="min-h-[32px] rounded border px-[var(--space-2)] text-[length:var(--type-body-compact-size)]"
          style={{ borderColor: 'var(--color-border)' }}
        />
      </label>

      {error && (
        <div role="alert" aria-live="assertive" data-testid="custom-endpoint-error" className="text-[length:var(--type-utility-xs-size)]">
          {error}
        </div>
      )}

      <div className="flex items-center gap-[var(--space-2)]">
        <Button
          type="submit"
          variant="default"
          data-testid="custom-endpoint-submit"
          disabled={!complete || submitting}
          className="h-auto min-h-[32px] rounded px-[var(--space-2-5)] text-[length:var(--type-body-compact-size)]"
        >
          Add endpoint
        </Button>
        {onCancel && (
          <Button
            type="button"
            variant="ghost"
            data-testid="custom-endpoint-cancel"
            onClick={onCancel}
            className="h-auto min-h-[32px] rounded px-[var(--space-2-5)] text-[length:var(--type-body-compact-size)]"
          >
            Cancel
          </Button>
        )}
      </div>
    </form>
  )
}
