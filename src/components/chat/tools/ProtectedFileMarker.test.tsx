// #1284 fix round 1 — the structured protected-internal-file marker.
// Oracle: contracts/asyncapi.yaml PermissionDenied.code ("Present with
// exactly this value when the refusal is Omnipus's protected-internal-file
// denial … Absent for every other permission denial") and the server's
// producer (pkg/tools/fserrors.go::ProtectedInternalFileResult: the same
// PermissionDenied shape, permanent:true, code protected_internal_file).
// The marker decides; the prose fallback stays until S1 assembly.
import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { MessagePartStatus } from '@assistant-ui/react'
import type { PermissionDenied } from '@/lib/api/generated/asyncapi-types'
import { GenericToolCall } from './GenericToolCall'
import { resolveToolResult } from './toolResultDisplay'
import { isProtectedInternalFileDenial } from './toolResultSentinels'

// A reason with NONE of the fallback phrases: only the code can classify it.
const NEUTRAL_REASON = 'access denied for /home/u/.omnipus/sessions/a.jsonl'
const marked: PermissionDenied = {
  error: 'permission_denied', message: 'Access to this action is denied.', tool: 'exec',
  reason: NEUTRAL_REASON, permanent: true, code: 'protected_internal_file',
}
const ordinary: PermissionDenied = {
  error: 'permission_denied', message: 'Access to this action is denied.', tool: 'read_file',
  reason: 'outside the workspace', permanent: true,
}
const DONE: MessagePartStatus = { type: 'complete' }

describe('#1284 structured marker', () => {
  it('the generated code alone classifies the refusal, with the server reason kept', () => {
    expect(isProtectedInternalFileDenial(marked)).toBe(true)
    expect(resolveToolResult(marked)).toEqual({ kind: 'protectedFile', message: NEUTRAL_REASON })
  })

  it('a permission denial without the code stays an ordinary permission denial', () => {
    expect(isProtectedInternalFileDenial(ordinary)).toBe(false)
    expect(resolveToolResult(ordinary)).toEqual({ kind: 'permissionDenied', failure: ordinary })
  })

  it('a payload that is not a valid PermissionDenied is not trusted, even with the code', () => {
    const malformed = { ...marked, permanent: 'yes' }
    expect(isProtectedInternalFileDenial(malformed)).toBe(false)
    expect(resolveToolResult(malformed).kind).not.toBe('protectedFile')
  })

  it('the tool row shows the protected-file block, not the ordinary permission-denied block', () => {
    render(<GenericToolCall toolName="exec" result={marked} status={DONE} isError />)
    fireEvent.click(screen.getByRole('button'))
    const block = screen.getByTestId('result-protected-file')
    expect(block).toHaveTextContent('Protected Omnipus file')
    expect(block).toHaveTextContent(NEUTRAL_REASON)
    expect(screen.queryByTestId('result-permission-denied')).toBeNull()
  })

  it('control: an unmarked denial keeps the ordinary permission-denied block', () => {
    render(<GenericToolCall toolName="read_file" result={ordinary} status={DONE} isError />)
    fireEvent.click(screen.getByRole('button'))
    expect(screen.getByTestId('result-permission-denied')).toBeInTheDocument()
    expect(screen.queryByTestId('result-protected-file')).toBeNull()
  })

  it('prose fallback (until S1 assembly): the plain-text wording is still recognised', () => {
    const text = 'failed to read file: access denied: path is a protected carve-out ($OMNIPUS_HOME-anchored)'
    expect(resolveToolResult(text)).toEqual({ kind: 'protectedFile', message: text })
    expect(resolveToolResult(undefined, text)).toEqual({ kind: 'protectedFile', message: text })
  })
})
