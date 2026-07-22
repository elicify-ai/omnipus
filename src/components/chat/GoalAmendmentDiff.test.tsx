// GoalAmendmentDiff.test.tsx — ADR-053 FE-8 / US-3 / N-6.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GoalAmendmentDiff, type GoalAmendmentDiffData } from './GoalAmendmentDiff'

describe('GoalAmendmentDiff', () => {
  it('renders added/changed/dropped sections', () => {
    const diff: GoalAmendmentDiffData = {
      added: ['all tests pass'],
      changed: [{ from: 'lint clean', to: 'lint + typecheck clean' }],
      dropped: ['manual QA sign-off'],
    }
    render(<GoalAmendmentDiff diff={diff} />)

    expect(screen.getByTestId('goal-amendment-added')).toHaveTextContent('all tests pass')
    expect(screen.getByTestId('goal-amendment-changed')).toHaveTextContent('lint clean')
    expect(screen.getByTestId('goal-amendment-changed')).toHaveTextContent('lint + typecheck clean')
    expect(screen.getByTestId('goal-amendment-dropped')).toHaveTextContent('manual QA sign-off')
  })

  it('renders the confirm-or-reject prompt (no buttons/form)', () => {
    const diff: GoalAmendmentDiffData = {
      added: ['c1'],
      changed: [],
      dropped: [],
    }
    render(<GoalAmendmentDiff diff={diff} />)
    expect(screen.getByText(/Reply to confirm the amendment/)).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('shows the empty-diff message when nothing changed', () => {
    render(<GoalAmendmentDiff diff={{ added: [], changed: [], dropped: [] }} />)
    expect(screen.getByTestId('goal-amendment-empty')).toHaveTextContent(/No changes detected/)
    expect(screen.queryByTestId('goal-amendment-added')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-amendment-changed')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-amendment-dropped')).not.toBeInTheDocument()
  })

  it('renders only the dropped section when only criteria are removed', () => {
    render(<GoalAmendmentDiff diff={{ added: [], changed: [], dropped: ['old criterion'] }} />)
    expect(screen.queryByTestId('goal-amendment-added')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-amendment-changed')).not.toBeInTheDocument()
    expect(screen.getByTestId('goal-amendment-dropped')).toHaveTextContent('old criterion')
  })
})
