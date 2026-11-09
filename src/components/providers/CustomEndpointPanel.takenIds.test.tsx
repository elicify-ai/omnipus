/**
 * CustomEndpointPanel.takenIds.test.tsx — WP-1 (#1700) design R23, extra cases.
 *
 * Added by the frontend developer during the mutation round, alongside (never
 * editing) the test author's CustomEndpointPanel.test.tsx. Each case kills a
 * mutation that the pre-written cases let survive:
 *   - the check runs on the TRIMMED id (design §3: "When the trimmed id is in it");
 *   - the match is exact, same as the server — case is not folded;
 *   - the refusal is tied to the Provider id field for assistive technology.
 *
 * Oracle: the design's M1 text (§2.5), verbatim.
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

describe('CustomEndpointPanel — taken ids, extra cases (design R23)', () => {
  it('checks the trimmed id: "  azure  " is refused with the M1 text for "azure"', () => {
    const onSubmit = vi.fn()
    render(<CustomEndpointPanel onSubmit={onSubmit} takenIds={new Set(['azure'])} />)

    fillPanel('  azure  ')

    expect(screen.getByTestId('custom-endpoint-id-taken')).toHaveTextContent(M1_AZURE)
    expect(screen.getByTestId('custom-endpoint-submit')).toBeDisabled()
    fireEvent.submit(screen.getByTestId('custom-endpoint-panel'))
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('matches exactly, like the server: "Azure" is not a listed id and is accepted', () => {
    const onSubmit = vi.fn()
    render(<CustomEndpointPanel onSubmit={onSubmit} takenIds={new Set(['azure'])} />)

    fillPanel('Azure')

    expect(screen.queryByTestId('custom-endpoint-id-taken')).not.toBeInTheDocument()
    expect(screen.getByTestId('custom-endpoint-submit')).not.toBeDisabled()
  })

  it('a submit by Enter is also blocked while the id is taken', () => {
    const onSubmit = vi.fn()
    render(<CustomEndpointPanel onSubmit={onSubmit} takenIds={new Set(['azure'])} />)

    fillPanel('azure')
    fireEvent.submit(screen.getByTestId('custom-endpoint-panel'))

    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('marks the Provider id field invalid and points it at the refusal', () => {
    render(<CustomEndpointPanel onSubmit={vi.fn()} takenIds={new Set(['azure'])} />)

    fillPanel('azure')

    const field = screen.getByTestId('custom-endpoint-id')
    const alert = screen.getByTestId('custom-endpoint-id-taken')
    expect(field).toHaveAttribute('aria-invalid', 'true')
    expect(alert.id).not.toBe('')
    expect(field).toHaveAttribute('aria-describedby', alert.id)

    fireEvent.change(field, { target: { value: 'my-azure' } })
    expect(field).not.toHaveAttribute('aria-invalid')
    expect(field).not.toHaveAttribute('aria-describedby')
  })
})
