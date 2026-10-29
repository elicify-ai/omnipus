// CliPathValidationHint.pathNotAllowed.test.tsx — T11 slice A, spec §8
// work plan "A-UI: the CLI-path hint shows path-not-allowed" and §3.2 C-A2.
//
// Oracle (slim spec, not the component): a Check CLI result whose reason is
// path-not-allowed shows that reason, and the visible text is the server
// detail (the message that names the failed check). It is not the unknown-
// reason fallback, which says the person can still save.
// Slice B owns whether Save is blocked; this test does not assert that.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CliPathValidationHint } from './CliPathValidationHint'
import type { CliValidateResponse } from '@/lib/api'

describe('CliPathValidationHint shows path-not-allowed', () => {
  it('shows the refusal reason and the server message, not the save-anyway fallback', () => {
    // Spec §3.2: the refusal message names the failed check. This string is
    // the fixture for that message; the hint must show it unchanged.
    const detail = 'refused: the program is inside a workspace'
    const result = {
      ok: false,
      // C-A2 adds this reason to the generated union. Until that commit, the
      // value is not a member, so the test names it and narrows for the type
      // checker. The rendered value is still path-not-allowed.
      reason: 'path-not-allowed' as unknown as CliValidateResponse['reason'],
      detail,
      resolved_path: null,
      version: null,
    } satisfies CliValidateResponse

    render(
      <CliPathValidationHint
        validation={{ kind: 'result', result }}
        detectHint={{ kind: 'unknown' }}
      />,
    )

    const hint = screen.getByRole('paragraph')
    // Spec §8 A-UI: the hint shows the reason path-not-allowed, not the
    // unknown-reason fallback.
    expect(hint).toHaveAttribute('data-cli-status', 'path-not-allowed')
    expect(hint).toHaveTextContent(detail)
    expect(hint.textContent ?? '').not.toMatch(/you can still save/i)
  })
})
