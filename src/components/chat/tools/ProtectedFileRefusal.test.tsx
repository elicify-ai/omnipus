// ProtectedFileRefusal.test.tsx — #1284 refusal shown in the UI (L1 plan §9
// item 3; amendment §2). Oracle: the server's two refusal wordings
// (pkg/tools/shell_path_guard.go; pkg/tools/fserrors.go::ErrCarveOut at
// WU-23 ee6a17f79) and docs/security.md's "never grantable … God Mode
// included". A protected-file refusal reads as its own block, wherever the
// text arrives (result or error).
import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { MessagePartStatus } from '@assistant-ui/react'
import { GenericToolCall } from './GenericToolCall'
import { PROTECTED_FILE_REFUSAL_TEXT, resolveToolResult } from './toolResultDisplay'

const SHELL_REFUSAL = 'Command blocked: "/home/u/.omnipus/sessions/a.jsonl" is inside Omnipus\'s protected internal files, which no agent command may read or change in any mode — God Mode included. This is never grantable; work inside the workspace and use the dedicated internal tools where one exists.'
const FILE_REFUSAL = 'failed to read file: access denied: path is a protected carve-out ($OMNIPUS_HOME-anchored)'
const DONE: MessagePartStatus = { type: 'complete' }

describe('#1284 protected-file refusal', () => {
  it.each([SHELL_REFUSAL, FILE_REFUSAL])('resolves the refusal as its own kind from the result or the error: %s', (text) => {
    expect(resolveToolResult(text).kind).toBe('protectedFile')
    expect(resolveToolResult(undefined, text).kind).toBe('protectedFile')
  })

  it('an ordinary failure is not mistaken for it', () => {
    expect(resolveToolResult('failed to read file: access denied: permission denied').kind).toBe('text')
  })

  it('the tool row explains it is never grantable, including in God Mode, and keeps the server text', () => {
    render(<GenericToolCall toolName="read_file" error={FILE_REFUSAL} status={DONE} isError />)
    fireEvent.click(screen.getByRole('button'))
    const block = screen.getByTestId('result-protected-file')
    expect(block).toHaveTextContent('Protected Omnipus file')
    expect(block).toHaveTextContent(PROTECTED_FILE_REFUSAL_TEXT)
    expect(block).toHaveTextContent(/God Mode included/)
    expect(block).toHaveTextContent(FILE_REFUSAL)
  })
})
