// G2-F4, Settings #1519. Oracle: FR-035 (labels, both values and reason),
// C-2/C-6 and docs/settings.md, Saving changes. The key INPUTS, not the
// expected labels, are enumerated from these gateway writer sources:
//   pkg/gateway/rest_settings_retention.go::retentionUpdateKeys
//   pkg/gateway/rest_rate_limits.go::HandleRateLimits
//   pkg/gateway/rest_context_settings.go::contextUpdateKeys
//   pkg/gateway/rest_prompt_guard.go::HandlePromptGuard
//   pkg/gateway/rest_third_party_settings.go::writeThirdPartyRows
//   pkg/gateway/rest_apply_failed.go::performancePendingApplyConfigKeys
//   pkg/gateway/rest_pending_restart.go::RestartGatedKeys
//   pkg/config/keys.go (the restart-gated constant values)
//   pkg/gateway/rest_config.go::updateConfig (Gateway's hot settings).
// Generic /config can also record arbitrary operator keys; those have no
// specified Settings label. This matrix covers the known Settings families.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { PendingRestartEntry } from '@/lib/api'
import { RestartBanner, toBannerEntries } from './RestartBanner'
import { renderG1 } from '../../../tests/helpers/settings-gate-g1'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

// FR-035 requires human labels, not a particular grouped-vs-field spelling.
// Match the spec's human setting/category names; never accept a dotted key.
const writerLabels: [string, RegExp][] = [
  ['storage.retention.session_days', /chat history/i],
  ['storage.retention.memory_retros_days', /memory/i],
  ['storage.retention.memory_retros_forever', /keep.*forever|memory.*forever/i],
  ['sandbox.rate_limits.max_agent_llm_calls_per_hour', /rate limits|model calls per hour/i],
  ['sandbox.rate_limits.max_agent_tool_calls_per_minute', /rate limits|tool calls per minute/i],
  ['sandbox.prompt_injection_level', /defense level|prompt injection/i],
  ...['mcp_result_cap', 'builtin_success_cap', 'builtin_failure_cap', 'tool_result_share_fraction', 'ingest_bound_bytes', 'default_context_window', 'model_overrides']
    .map((field): [string, RegExp] => [`context.${field}`, /context|tool results|overrides|output|ingest|window/i]),
  ...['claude-code', 'codex', 'opencode'].flatMap((cli) => ['path', 'preset_args']
    .map((field): [string, RegExp] => [`sandbox.third_party_agents.clis.${cli}.${field}`, /third.party agents|program path|parameters/i])),
  ['performance.max_parallel_agents', /parallel agents/i],
  ['tools.manifest.compressed', /tool loading|load tools on demand/i],
  ['planning.goal_max_rounds', /tries per goal/i],
  ['planning.goal_reminder_quiet_seconds', /idle reminder|quiet window/i],
  ['planning.goal_stalled_checkin_enabled', /check.in/i],
  ['planning.goal_stalled_checkin_minutes', /check.in interval/i],
  ['agents.defaults.max_tool_iterations', /tool calls per turn/i],
  ['gateway.host', /bind address/i],
  ['gateway.port', /^port$/i],
  ['gateway.public_url', /public address/i],
  ['gateway.log_level', /log level/i],
  ['gateway.preview_enabled', /preview server/i],
  ['sandbox.mode', /sandbox mode/i],
  ['sandbox.filesystem_model', /filesystem model/i],
  ['sandbox.workspace_path_guard', /shell workspace limit/i],
  ['sandbox.allowed_paths', /allowed folders/i],
  ['sandbox.audit_log', /audit log/i],
  ['tools.exec.enable_proxy', /network proxy/i],
  ['session.dm_scope', /direct message scope/i],
  ['tools.run_in_workspace.warmup_timeout_seconds', /workspace warm.up time limit/i],
  ...['issuer', 'client_id', 'instance_id', 'keys']
    .map((field): [string, RegExp] => [`security.platform_auth.${field}`, /platform sign.in/i]),
]

it('G2-F4 FR-035 — Show changes labels every known recordable writer key instead of exposing raw configuration paths', async () => {
  expect(new Set(writerLabels.map(([key]) => key)).size, 'instrument: no duplicate can hide an omitted writer case').toBe(writerLabels.length)
  const entries: PendingRestartEntry[] = writerLabels.map(([key], index) => ({
    key,
    applied_value: `running-${index}`,
    persisted_value: `saved-${index}`,
    reason: index % 2 === 0 ? 'apply_failed' : 'restart_required',
  }))
  renderG1(<RestartBanner entries={toBannerEntries(entries)} onRetry={() => {}} onRestartNow={() => {}} />)
  const showChanges = await screen.findByRole('button', { name: 'Show changes' })
  expect(screen.queryByRole('list')).not.toBeInTheDocument()
  await userEvent.click(showChanges)
  const rows = within(screen.getByRole('list')).getAllByRole('listitem')
  expect(rows, 'FR-035: every pending entry must be disclosed').toHaveLength(entries.length)
  for (const [index, [key, expectedLabel]] of writerLabels.entries()) {
    const row = rows[index]
    const label = row.querySelector('span')?.textContent
    expect.soft(label, `G2-F4 ${key}: use the spec's human setting/category label`).toMatch(expectedLabel)
    expect.soft(row, `G2-F4 ${key}: raw configuration paths are not user-facing labels`).not.toHaveTextContent(key)
    expect.soft(row, `G2-F4 ${key}: running value must survive label conversion`).toHaveTextContent(`running running-${index}`)
    expect.soft(row, `G2-F4 ${key}: saved value must survive label conversion`).toHaveTextContent(`after the restart saved-${index}`)
    expect.soft(row, `G2-F4 ${key}: preserve the actual pending reason`).toHaveTextContent(index % 2 === 0 ? 'saved, not applied' : 'needs a restart')
  }
})
