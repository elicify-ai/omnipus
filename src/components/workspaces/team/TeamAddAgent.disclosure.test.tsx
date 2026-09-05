// TeamAddAgent.disclosure.test.tsx — ADR-075 FR-047, test 64.
//
// Adding an agent to a workspace team grants it that workspace's browser, and
// under ADR-075 that browser is one Chrome with one cookie jar that is already
// signed in to whatever the operator signed it in to. So the add is an
// elevation of privilege, and since D1.10 it is a larger one than when FR-047
// was written: unattended work — scheduled turns, heartbeats — inherits the
// same logins with nobody watching.
//
// FR-047 therefore requires the disclosure AT THE POINT OF ADDING, before the
// action takes effect. It explicitly rules out release notes, and this test
// additionally pins the two other cheap placements that would satisfy the
// letter and not the point: a tooltip (needs a hover nobody performs, and does
// not exist on touch) and a post-hoc toast (the privilege is already granted).
//
// The shipped flow has NO confirmation step — clicking a candidate calls onAdd
// immediately — so the candidate button IS the confirm action, and "before the
// confirm action" means "on screen at the same time as the candidate list".
// That is what these tests assert.
//
// Radix Popover is stubbed to render its content inline (the convention this
// directory's other picker tests use) so the popover body is reachable without
// a real portal or pointer capture in jsdom.

import * as React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AddAgentPicker } from './AddAgentPicker'
import type { Agent } from '@/lib/api'
import { makeAgent } from '@/test/factories'

vi.mock('@/components/ui/popover', () => {
  return {
    Popover: ({ children }: { children: React.ReactNode }) =>
      React.createElement(React.Fragment, null, children),
    PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) => {
      if (asChild && React.isValidElement(children)) return children
      return React.createElement('div', null, children)
    },
    PopoverContent: ({ children }: { children: React.ReactNode }) =>
      React.createElement('div', { 'data-testid': 'popover-content' }, children),
  }
})

const JIM = makeAgent({ id: 'jim', name: 'Jim', type: 'core', description: 'Orchestrator' })
const MARS = makeAgent({ id: 'mars', name: 'Mars', type: 'Main', description: 'Ops lead' })

function renderPicker(overrides: Partial<Parameters<typeof AddAgentPicker>[0]> = {}) {
  const onAdd = vi.fn()
  const utils = render(
    <AddAgentPicker
      agents={[JIM, MARS] as Agent[]}
      memberIds={new Set<string>()}
      onAdd={onAdd}
      {...overrides}
    />,
  )
  return { onAdd, ...utils }
}

describe('FR-047 — adding an agent to a team discloses the browser access it grants', () => {
  it('renders the disclosure in the picker, alongside the candidates the operator is about to click', () => {
    renderPicker()

    const disclosure = screen.getByTestId('team-add-agent-disclosure')
    expect(disclosure).toBeInTheDocument()

    // "Before the confirm action" has a concrete meaning in a flow with no
    // confirm step: the disclosure and the thing that grants the privilege are
    // on screen together, so the operator cannot reach one without the other.
    expect(screen.getByTestId('team-add-agent-option-jim')).toBeInTheDocument()

    const popover = screen.getByTestId('popover-content')
    expect(within(popover).getByTestId('team-add-agent-disclosure')).toBe(disclosure)
  })

  it('names the real consequence — the workspace browser, its live logins, and unattended turns', () => {
    renderPicker()
    const text = screen.getByTestId('team-add-agent-disclosure').textContent ?? ''

    // Each of these is a distinct fact an operator needs, and none of them can
    // be inferred from the others. "Gets browser access" alone reads like a
    // capability toggle; the point is WHOSE browser and what it is already
    // signed in to.
    expect(text).toMatch(/browser/i)
    expect(text).toMatch(/signed in/i)
    // D1.10's escalation: this is the part that got worse, and the part an
    // operator is least likely to imagine on their own.
    expect(text).toMatch(/scheduled|background|nobody is watching/i)
  })

  it('is not deferred to a tooltip: it is real text, not a title/aria-label on the trigger', () => {
    renderPicker()
    const disclosure = screen.getByTestId('team-add-agent-disclosure')

    expect((disclosure.textContent ?? '').trim().length).toBeGreaterThan(40)

    const trigger = screen.getByTestId('team-add-agent')
    expect(trigger).not.toHaveAttribute('title')
    // A tooltip hidden behind the trigger's accessible name would also pass a
    // naive "is the text present" check.
    expect(trigger.getAttribute('aria-label') ?? '').not.toMatch(/signed in/i)
  })

  it('is on screen BEFORE the add fires, not surfaced after it', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderPicker()

    // Nothing has been added yet, and the warning is already there.
    expect(onAdd).not.toHaveBeenCalled()
    expect(screen.getByTestId('team-add-agent-disclosure')).toBeInTheDocument()

    await user.click(screen.getByTestId('team-add-agent-option-mars'))
    expect(onAdd).toHaveBeenCalledWith('mars')

    // And it was not a post-hoc toast: the disclosure existed before the click
    // rather than appearing as a consequence of it. Asserted by the ordering
    // above; re-asserting presence after the click would pass for a toast too.
  })

  it('survives the empty-team case, which is the flow a first-time operator actually takes', () => {
    // WorkspaceTeamTab renders this picker twice — in the header and in the
    // "No agents on this team yet" empty state. The disclosure lives in the
    // picker precisely so a placement in the tab could not cover one site and
    // miss the other. This is the second site's props.
    renderPicker({ memberIds: new Set<string>() })
    expect(screen.getByTestId('team-add-agent-disclosure')).toBeInTheDocument()
  })

  it('is shown even when there is nothing to add, so it is never conditional on the list', () => {
    // A disclosure rendered inside the candidate-list branch would vanish the
    // moment the list is empty — and then reappear, unread, the moment a new
    // agent exists.
    renderPicker({ agents: [JIM] as Agent[], memberIds: new Set(['jim']) })
    expect(screen.getByText('Every agent is already on this team.')).toBeInTheDocument()
    expect(screen.getByTestId('team-add-agent-disclosure')).toBeInTheDocument()
  })
})
