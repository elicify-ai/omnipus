// packageErrorText — spec order 18 (issue #1967 B1). Oracle: the spec's
// "UI text for codes" table, transcribed verbatim; an unknown code passes the
// server's own `error` text through; `element_not_creatable` differs by
// wizard side; `agent_file_unreadable` appends the too-large sentence and the
// file name from details.
import { describe, expect, it } from 'vitest'
import { packageErrorTextFor } from './packages'

// The exact spec texts (the client map's expected content, asserted row by
// row — RED: the stub map is empty and the function throws).
const TEXTS: Array<[string, string]> = [
  ['not_a_package', 'This file is not an Omnipus bundle.'],
  ['package_too_new', 'This bundle was made by a newer Omnipus. Update this install to import it.'],
  ['element_schema_too_new', 'Part of this bundle was made by a newer Omnipus. Update this install to import it.'],
  ['package_too_large', 'This bundle is too large for this version (most is 64 MB, 256 MB unpacked).'],
  ['checksum_mismatch', 'This bundle is damaged: a file does not match its record. Export it again.'],
  ['manifest_mismatch', 'This bundle is damaged: a file does not match its record. Export it again.'],
  ['unsafe_path', 'This bundle contains something unsafe and was refused.'],
  ['unsafe_entry_type', 'This bundle contains something unsafe and was refused.'],
  ['no_supported_element', 'This bundle contains nothing this version can import.'],
  ['element_type_not_supported', 'This bundle contains nothing this version can import.'],
  ['too_many_agents', 'This bundle holds more than one agent. This version imports one agent per bundle.'],
  ['invalid_element', 'The agent in this bundle is not valid.'],
  ['external_worker_not_supported_yet', 'Third-party workers cannot be exported or imported yet.'],
  ['too_many_staged_imports', 'Too many imports are waiting. Each one expires 30 minutes after upload; finish or cancel one, then try again.'],
  ['staging_not_found', 'This upload expired or was already used. Choose the file again.'],
  ['staging_cleanup_failed', 'The upload was cancelled. Omnipus will try to remove its temporary files at a later upload.'],
  ['import_in_progress', 'Another import is running. Try again in a moment.'],
  ['workspace_not_found', 'That workspace no longer exists. Choose another.'],
  ['default_workspace_missing', 'Choose a workspace to import into.'],
  ['name_suffix_exhausted', 'Every "(imported N)" name is taken. Rename one of the existing agents, then try again.'],
  ['import_rolled_back', 'The import failed and was undone. Nothing was added.'],
  ['import_rollback_incomplete', 'The import failed and could not be fully undone. Restart Omnipus to finish the clean-up.'],
  ['core_agent_not_exportable', 'Built-in agents are on every install and cannot be exported.'],
  ['agent_prompt_missing', 'This agent has no prompt, so it cannot be exported.'],
  ['agent_not_found', 'This agent no longer exists.'],
  ['export_selection_invalid', 'Choose exactly one agent.'],
  ['plan_not_found', 'The review expired. Check your choice and review again.'],
  ['audit_failed', 'The export was stopped because the audit log could not be written. Nothing was downloaded.'],
]

describe('packageErrorTextFor — the UI text for codes', () => {
  for (const [code, text] of TEXTS) {
    it(`${code} maps to the exact spec text`, () => {
      expect(packageErrorTextFor(code, 'server text', { side: 'import' })).toBe(text)
    })
  }

  it('an unknown code passes the server error text through', () => {
    expect(packageErrorTextFor('some_future_code', 'The server said this.', { side: 'import' })).toBe(
      'The server said this.',
    )
  })

  it('element_not_creatable (import steps) names the new-agent rule and the field', () => {
    expect(packageErrorTextFor('element_not_creatable', 'server text', { side: 'import' })).toBe(
      'The agent in this bundle breaks a rule for new agents',
    )
    expect(packageErrorTextFor('element_not_creatable', 'server text', { side: 'import', field: 'description' })).toBe(
      'The agent in this bundle breaks a rule for new agents: description',
    )
  })

  it('element_not_creatable (export steps) names the export rule', () => {
    expect(packageErrorTextFor('element_not_creatable', 'server text', { side: 'export' })).toBe(
      'This agent breaks a rule for agents and cannot be exported',
    )
    expect(packageErrorTextFor('element_not_creatable', 'server text', { side: 'export', field: 'model.primary' })).toBe(
      'This agent breaks a rule for agents and cannot be exported: model.primary',
    )
  })

  it('agent_file_unreadable names the file and appends the too-large sentence', () => {
    expect(packageErrorTextFor('agent_file_unreadable', 'server text', { side: 'export', file: 'SOUL.md' })).toBe(
      'A file of this agent could not be read safely (SOUL.md).',
    )
    expect(packageErrorTextFor('agent_file_unreadable', 'server text', { side: 'export', check: 'too_large', file: 'SOUL.md' })).toBe(
      'A file of this agent could not be read safely (SOUL.md). It is over 1 MB.',
    )
  })
})
