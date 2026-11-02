import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, type ComponentType } from 'react'
import { screen } from '@testing-library/react'
import { useToolApprovalStore } from '@/store/toolApproval'
import { ToolApprovalModal } from '@/components/agents/ToolApprovalModal'
import { redactAgentChangeArgs } from './AgentChangeApprovalPreview'
import { renderWithClient, loadApprovalPanel, resetSharedState, installApprovalNetwork, cleanupApprovalState } from '../../../../tests/l1-ui-red-u23/support'

// Oracle: tool-parity r17 SR-1 / 17.3 (CK-11). Real approval panel and modal,
// real preview registry and store. Fresh secret canaries sit in env_overrides
// containers nested under UNKNOWN arguments (object, array, letter-case
// variant); no card may render them, while the unknown names and an ordinary
// unknown value stay visible in the raw Arguments block.
let Panel: ComponentType
beforeEach(async () => {
  resetSharedState('sess-001')
  installApprovalNetwork()
  Panel = await loadApprovalPanel()
})
afterEach(() => cleanupApprovalState())

function freshCanary(label: string): string {
  return `t8-${label}-${crypto.randomUUID()}`
}

function nestedArgs() {
  const secrets = { top: freshCanary('top'), object: freshCanary('object'), array: freshCanary('array'), cased: freshCanary('cased') }
  const args = {
    id: 'worker',
    env_overrides: { TOKEN: secrets.top },
    cli_args: '--model x --verbose',
    zz_plain: 'must remain visible',
    zz_probe: { env_overrides: { TOKEN: secrets.object }, note: 'kept' },
    zz_list: ['plain item', { deeper: { env_overrides: { API_KEY: secrets.array } } }],
    zz_case: { Env_Overrides: { SECRET: secrets.cased } },
  }
  return { args, secrets }
}

function queue(toolName: string, args: Record<string, unknown>) {
  act(() => useToolApprovalStore.setState({ queue: [{
    approvalId: 't8-nested-approval', toolCallId: 't8-nested-call', toolName,
    agentId: 'agent-main', sessionId: 'sess-001', turnId: 'turn-001',
    expiresAt: Date.now() + 300_000, args,
  }] }))
}

function expectNoSecretAndFidelity(container: HTMLElement, secrets: Record<string, string>) {
  for (const secret of Object.values(secrets)) expect(container.innerHTML).not.toContain(secret)
  expect(screen.getByText(/zz_probe/)).toBeInTheDocument()
  expect(screen.getByText(/zz_list/)).toBeInTheDocument()
  expect(screen.getByText(/API_KEY/)).toBeInTheDocument()
  expect(screen.getByText(/must remain visible/)).toBeInTheDocument()
  expect(screen.getByText(/plain item/)).toBeInTheDocument()
}

describe('agent change card masks env_overrides nested under unknown arguments', () => {
  it.each(['create_agent', 'update_agent'])('panel for %s never renders a nested env value', (tool) => {
    const { args, secrets } = nestedArgs()
    queue(tool, args)
    const { container } = renderWithClient(<Panel />)
    expectNoSecretAndFidelity(container, secrets)
  })

  it.each(['create_agent', 'update_agent'])('modal for %s never renders a nested env value', (tool) => {
    const { args, secrets } = nestedArgs()
    queue(tool, args)
    const { container } = renderWithClient(<ToolApprovalModal />)
    expectNoSecretAndFidelity(container, secrets)
  })

  it('redaction keeps structure and unknown names, masks every nested container', () => {
    const { args, secrets } = nestedArgs()
    const shown = redactAgentChangeArgs(args)
    const encoded = JSON.stringify(shown)
    for (const secret of Object.values(secrets)) expect(encoded).not.toContain(secret)
    expect(shown).toEqual({
      id: 'worker',
      env_overrides: { TOKEN: '••••••' },
      cli_args: '--model x --verbose',
      zz_plain: 'must remain visible',
      zz_probe: { env_overrides: { TOKEN: '••••••' }, note: 'kept' },
      zz_list: ['plain item', { deeper: { env_overrides: { API_KEY: '••••••' } } }],
      zz_case: { Env_Overrides: { SECRET: '••••••' } },
    })
    expect(args.zz_probe.env_overrides.TOKEN, 'the store copy is never changed').toBe(secrets.object)
  })
})
