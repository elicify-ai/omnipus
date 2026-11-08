/**
 * CustomEndpointPanel.test.tsx — WP-1 (#1700) design R23 (panel half).
 *
 * Specification: /workspace/omnipus-coordination/designs-20261029/onboarding-custom-endpoint-design.md §3.
 * The Custom endpoint form takes a new optional `takenIds` prop (the loaded
 * catalog's provider ids, passed by the shared ProviderPicker). When the
 * trimmed id is in it, the form shows M1 inline under the Provider id field
 * (role="alert", test id custom-endpoint-id-taken) and keeps Add endpoint
 * disabled. Exact match, same as the server. Without takenIds (catalog down
 * or absent) any id is accepted — the server's M1 is then the backstop.
 *
 * The M1 text below is the design §2.5 oracle, verbatim, not read from any
 * implementation. These tests were observed failing before the prop existed.
 */

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { CustomEndpointPanel } from './CustomEndpointPanel'

const M1_AZURE =
  '"azure" is the name of a listed provider. Choose a different name for your custom endpoint, for example "my-azure".'

function fillPanel(id: string) {
  fireEvent.change(screen.getByTestId('custom-endpoint-id'), { target: { value: id } })
  fireEvent.change(screen.getByTestId('custom-endpoint-api-base'), {
    target: { value: 'https://azure.example.com/v1' },
  })
  fireEvent.change(screen.getByTestId('custom-endpoint-api-key'), { target: { value: 'sk-azure' } })
}

describe('CustomEndpointPanel — taken provider ids (design R23)', () => {
  it('blocks a listed provider name with M1 inline and keeps Add endpoint disabled', () => {
    const onSubmit = vi.fn()
    render(<CustomEndpointPanel onSubmit={onSubmit} takenIds={new Set(['azure'])} />)

    fillPanel('azure')

    const alert = screen.getByTestId('custom-endpoint-id-taken')
    expect(alert).toHaveAttribute('role', 'alert')
    expect(alert).toHaveTextContent(M1_AZURE)
    expect(screen.getByTestId('custom-endpoint-submit')).toBeDisabled()
    fireEvent.click(screen.getByTestId('custom-endpoint-submit'))
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('clears the refusal and re-enables Add endpoint once the id is free', () => {
    render(<CustomEndpointPanel onSubmit={vi.fn()} takenIds={new Set(['azure'])} />)

    fillPanel('azure')
    expect(screen.getByTestId('custom-endpoint-id-taken')).toHaveTextContent(M1_AZURE)
    expect(screen.getByTestId('custom-endpoint-submit')).toBeDisabled()

    fireEvent.change(screen.getByTestId('custom-endpoint-id'), { target: { value: 'my-azure' } })

    expect(screen.queryByTestId('custom-endpoint-id-taken')).not.toBeInTheDocument()
    expect(screen.getByTestId('custom-endpoint-submit')).not.toBeDisabled()
  })

  it('accepts any id when no takenIds are given — the server is the backstop', () => {
    const onSubmit = vi.fn()
    render(<CustomEndpointPanel onSubmit={onSubmit} />)

    fillPanel('azure')

    expect(screen.queryByTestId('custom-endpoint-id-taken')).not.toBeInTheDocument()
    expect(screen.getByTestId('custom-endpoint-submit')).not.toBeDisabled()
    fireEvent.click(screen.getByTestId('custom-endpoint-submit'))
    expect(onSubmit).toHaveBeenCalledWith({
      id: 'azure',
      api_base: 'https://azure.example.com/v1',
      protocol: 'openai-compatible',
      api_key: 'sk-azure',
    })
  })
})
