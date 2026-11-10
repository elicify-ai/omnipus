import { expect } from '@playwright/test';
import { AgentToolsResponse } from '../../../../src/lib/api/generated/schemas';
import type { AgentToolEntry } from '../../../../src/lib/api/generated/openapi-types';
import type { GatewayProcess } from '../gateway-process';

/** Observe the supported live policy readback, not optional fields in the
 * Agent create projection. The sparse create grant must really be stored and
 * remain confined by the global ceiling before a native tool can be tested. */
export async function assertNativeBashPolicy(
  gateway: GatewayProcess,
  agentId: string,
  configured: AgentToolEntry['configured_policy'],
  effective: AgentToolEntry['effective_policy'],
) {
  const result = await gateway.apiFetch('GET', `/api/v1/agents/${agentId}/tools`);
  expect(result.status, `PREREQUISITE: read the real agent's supported tool configuration: ${result.raw}`).toBe(200);
  const policy = AgentToolsResponse.parse(result.body);
  expect(policy.override_names, 'PREREQUISITE: bash is an explicit override on this actual agent, not an assumed inherited grant')
    .toContain('bash');
  expect(policy.tools.filter(tool => tool.name === 'bash').map(tool => ({
    name: tool.name, configured: tool.configured_policy, effective: tool.effective_policy,
  })), 'PREREQUISITE: real configured and strictest-wins effective bash policies match the intended native-tool path')
    .toEqual([{ name: 'bash', configured, effective }]);
  return policy;
}
