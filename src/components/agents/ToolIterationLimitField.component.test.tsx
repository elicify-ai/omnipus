// ToolIterationLimitField.component.test.tsx — #904 tool-iteration-limit
// spec, frontend-lead's own component tests for the extracted per-agent
// control (FR-003, FR-004, FR-016, D1, D9) and the wizard's Advanced step
// (US-3 AS-2, D14). Expected strings come from the spec's "UI Screens and
// States" table and BDD scenarios, not from the implementation.
// (qa-lead's RED pack owns ToolIterationLimitField.test.tsx.)

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { useState } from 'react'
import { ToolIterationLimitField, type ToolIterationLimitServerState } from './ToolIterationLimitField'
import { Advanced } from './wizard/Advanced'
import type { WizardSubmitPayload } from './CreateAgentWizard'

function input(testId = 'agent-max-tool-calls-input') {
  return screen.getByTestId(testId) as HTMLInputElement
}

const rideGlobal200: ToolIterationLimitServerState = {
  effective: 200, source: 'global', overrideIgnored: false,
}

describe('ToolIterationLimitField — server-computed state (FR-003)', () => {
  it('no own value: empty input, placeholder and source line name the global', () => {
    render(<ToolIterationLimitField value={null} onChange={vi.fn()} globalLimit={200} server={rideGlobal200} />)
    expect(input().value).toBe('')
    expect(input().placeholder).toBe('Global limit (200)')
    expect(screen.getByTestId('tool-iteration-limit-source').textContent).toBe('Using the global limit (200)')
    expect(screen.queryByTestId('tool-iteration-limit-reset')).toBeNull()
  })

  it('own value lowers the limit: "Lowered for this agent: 50 (global limit 200)" and a reset', () => {
    render(
      <ToolIterationLimitField
        value={50}
        onChange={vi.fn()}
        globalLimit={200}
        server={{ effective: 50, source: 'agent', overrideIgnored: false, storedOverride: 50 }}
      />,
    )
    expect(input().value).toBe('50')
    expect(screen.getByTestId('tool-iteration-limit-source').textContent).toBe('Lowered for this agent: 50 (global limit 200)')
    expect(screen.getByTestId('tool-iteration-limit-reset').textContent).toBe('Use global limit')
  })

  it('D1: an own value above the global is flagged as having no effect, with a reset', () => {
    render(
      <ToolIterationLimitField
        value={500}
        onChange={vi.fn()}
        globalLimit={200}
        server={{ effective: 200, source: 'global', overrideIgnored: true, storedOverride: 500 }}
      />,
    )
    expect(input().value).toBe('500')
    expect(screen.getByTestId('tool-iteration-limit-source').textContent)
      .toBe('Own value 500 is above the global limit (200) and has no effect')
    expect(screen.getByTestId('tool-iteration-limit-reset')).toBeInTheDocument()
  })

  it('renders the server value verbatim, never a client-side minimum', () => {
    // A server that says source "agent" / effective 70 while the global is
    // 60 is inconsistent — the field must still show 70, not min(60, 70).
    render(
      <ToolIterationLimitField
        value={70}
        onChange={vi.fn()}
        globalLimit={60}
        server={{ effective: 70, source: 'agent', overrideIgnored: false, storedOverride: 70 }}
      />,
    )
    expect(screen.getByTestId('tool-iteration-limit-source').textContent).toBe('Lowered for this agent: 70 (global limit 60)')
  })

  it('FR-004: with the global unknown no number is invented', () => {
    render(<ToolIterationLimitField value={null} onChange={vi.fn()} />)
    expect(input().placeholder).toBe('Global limit')
    expect(document.body.textContent).not.toMatch(/\d/)
  })
})

describe('ToolIterationLimitField — editing', () => {
  it('commits a valid whole number and never commits an emptied field (restore mode)', () => {
    const onChange = vi.fn()
    const onEdit = vi.fn()
    render(<ToolIterationLimitField value={80} onChange={onChange} onEdit={onEdit} globalLimit={200} />)
    fireEvent.change(input(), { target: { value: '' } })
    expect(onChange).not.toHaveBeenCalled()
    fireEvent.change(input(), { target: { value: '50' } })
    expect(onChange).toHaveBeenLastCalledWith(50)
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onEdit).toHaveBeenCalledTimes(2)
  })

  it.each(['0', '-5', '2.5', '1001'])('US-2 AS-5: %s is refused with the 1–1000 bound and not committed', (raw) => {
    const onChange = vi.fn()
    render(<ToolIterationLimitField value={null} onChange={onChange} globalLimit={200} server={rideGlobal200} />)
    fireEvent.change(input(), { target: { value: raw } })
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.getByRole('alert').textContent).toBe('Enter a whole number from 1 to 1000.')
    expect(input().getAttribute('aria-invalid')).toBe('true')
  })

  it('accepts both bounds, 1 and 1000', () => {
    const onChange = vi.fn()
    render(<ToolIterationLimitField value={null} onChange={onChange} />)
    fireEvent.change(input(), { target: { value: '1' } })
    fireEvent.change(input(), { target: { value: '1000' } })
    expect(onChange.mock.calls).toEqual([[1], [1000]])
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('D9: "Use global limit" commits null, empties the input and keeps focus on it', () => {
    function Harness({ onChange }: { onChange: (v: number | null) => void }) {
      const [value, setValue] = useState<number | null>(50)
      return (
        <ToolIterationLimitField
          value={value}
          onChange={(v) => { setValue(v); onChange(v) }}
          globalLimit={200}
          server={{ effective: 50, source: 'agent', overrideIgnored: false, storedOverride: 50 }}
        />
      )
    }
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    fireEvent.click(screen.getByTestId('tool-iteration-limit-reset'))
    expect(onChange).toHaveBeenCalledWith(null)
    expect(input().value).toBe('')
    expect(document.activeElement).toBe(input())
    expect(screen.queryByTestId('tool-iteration-limit-reset')).toBeNull()
  })

  it('blur with an emptied draft restores the committed own value', () => {
    render(<ToolIterationLimitField value={80} onChange={vi.fn()} />)
    fireEvent.change(input(), { target: { value: '' } })
    fireEvent.blur(input())
    expect(input().value).toBe('80')
  })

  it('D10: a server refusal is shown inline and linked to the input', () => {
    const msg = 'max_tool_iterations 300 is above the global limit (200); lower it, or raise the global limit in Settings → Performance'
    render(<ToolIterationLimitField value={300} onChange={vi.fn()} globalLimit={200} server={rideGlobal200} serverError={msg} />)
    const alert = screen.getByRole('alert')
    expect(alert.textContent).toBe(msg)
    expect(input().getAttribute('aria-describedby')).toContain(alert.id)
  })

  it('disabled (non-editable field): no reset offered', () => {
    render(<ToolIterationLimitField value={50} onChange={vi.fn()} disabled />)
    expect(input().disabled).toBe(true)
    expect(screen.queryByTestId('tool-iteration-limit-reset')).toBeNull()
  })
})

describe('wizard Advanced — Max tool calls per turn (US-3 AS-2, D14)', () => {
  // Placeholder-from-server is covered by qa-lead's
  // wizard/Advanced.toolIterationLimit.test.tsx; this covers the payload
  // edits and the query-client-free render the wizard steps rely on.
  function renderAdvanced(initialType: WizardSubmitPayload['type']) {
    const setField = vi.fn()
    const payload = { type: initialType, name: '', description: '', color: '', model: '', soul: '' } as WizardSubmitPayload
    render(<Advanced payload={payload} setField={setField} initialType={initialType} />)
    fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))
    return setField
  }

  it.each(['Main', 'Subagent', 'subagent_3p'] as const)('%s: renders without a QueryClientProvider; a value is set, clearing unsets it', (type) => {
    const setField = renderAdvanced(type)
    const field = input('wizard-max-tool-calls-input')
    expect(field.value).toBe('')
    expect(field.placeholder).toBe('Global limit')
    fireEvent.change(field, { target: { value: '30' } })
    expect(setField).toHaveBeenLastCalledWith('max_tool_iterations', 30)
    fireEvent.change(field, { target: { value: '' } })
    expect(setField).toHaveBeenLastCalledWith('max_tool_iterations', undefined)
  })
})
