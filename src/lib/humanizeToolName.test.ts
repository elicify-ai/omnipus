import { describe, it, expect } from 'vitest'
import { humanizeToolName } from './humanizeToolName'

describe('humanizeToolName — explicit map', () => {
  it.each([
    ['remember', 'Remember'],
    ['recall_memory', 'Recall memory'],
    ['handoff', 'Hand off'],
    ['browser.navigate', 'Navigate browser'],
    ['browser.screenshot', 'Take screenshot'],
    ['web_search', 'Search the web'],
  ])('maps %s → %s', (id, expected) => {
    expect(humanizeToolName(id)).toBe(expected)
  })

  // ADR-063 FR-7.2 / operator-approved copy: the raw tool id stays
  // `request_mount` (label-only rename), but the approval UI must never say
  // "mount" — see ToolApprovalModal.tsx's RequestMountApprovalPreview.
  it('maps request_mount → Add folder', () => {
    expect(humanizeToolName('request_mount')).toBe('Add folder')
  })
})

describe('humanizeToolName — ADR-036 bash/delegate consolidation', () => {
  it.each([
    // New canonical names
    ['bash', 'Run command'],
    ['delegate', 'Delegate task'],
    // Legacy names retired by the consolidation — kept mapped so old,
    // already-persisted session transcripts (never migrated) still render a
    // readable label instead of falling through to the raw id.
    ['exec', 'Run command'],
    ['workspace_shell', 'Run shell command'],
    ['workspace_shell_bg', 'Run shell (background)'],
    ['workspace.shell', 'Run shell command'],
    ['workspace.shell_bg', 'Run shell (background)'],
    ['spawn', 'Spawn subagent'],
    ['run_subagent', 'Run subagent'],
    ['check_spawn_status', 'Check spawn status'],
  ])('maps %s → %s', (id, expected) => {
    expect(humanizeToolName(id)).toBe(expected)
  })
})

// ADR-071 D1 / spec FR-015 (W-D1 test 6): the discovery capability's display
// label must be reachable under BOTH the new canonical name ("ToolSearch")
// and the retired pre-rename name ("load_tool"), so a conversation
// transcript recorded before the rename still renders a readable label
// instead of falling through to the raw identifier.
describe('humanizeToolName — ADR-071 D1 discovery-tool rename', () => {
  it('maps the new canonical name ToolSearch → Find & load tools', () => {
    expect(humanizeToolName('ToolSearch')).toBe('Find & load tools')
  })

  it('retains the legacy load_tool alias so pre-rename transcripts still render readably', () => {
    expect(humanizeToolName('load_tool')).toBe('Find & load tools')
  })
})

// ADR-071 D4: `hand_off` and `return_to_default` are merged into one tool,
// `switch_agent(target, note?)`. The new canonical name gets its own label.
// Of the two retired predecessors, only `hand_off` needs an explicit alias
// entry — `handoff` is that same predecessor's other historical spelling
// (no underscore), not a second distinct tool. Both spellings stay mapped so
// old, already-persisted session transcripts (never migrated) still render a
// readable label instead of falling through to the raw id. `return_to_default`
// (the actual second retired tool) is deliberately left unmapped — it relies
// on the generic humanization fallback instead (a legitimate design choice,
// pinned by its own assertion below).
describe('humanizeToolName — ADR-071 D4 switch_agent merge', () => {
  it('maps the new canonical name switch_agent → Switch agent', () => {
    expect(humanizeToolName('switch_agent')).toBe('Switch agent')
  })

  it('retains the legacy hand_off alias so pre-merge transcripts still render readably', () => {
    expect(humanizeToolName('hand_off')).toBe('Hand off')
  })

  it('retains the legacy handoff alias (hand_off\'s other historical spelling) so pre-merge transcripts still render readably', () => {
    expect(humanizeToolName('handoff')).toBe('Hand off')
  })

  it('return_to_default has no explicit alias and relies on the generic fallback', () => {
    expect(humanizeToolName('return_to_default')).toBe('Return to default')
  })
})

describe('humanizeToolName — generic fallback', () => {
  it('strips a leading namespace and title-cases the remainder', () => {
    // system.task.update → "Task update"
    expect(humanizeToolName('system.task.update')).toBe('Task update')
  })

  it('handles snake_case without a namespace', () => {
    expect(humanizeToolName('custom_mcp_tool')).toBe('Custom mcp tool')
  })

  it('handles a single dotted namespace + verb', () => {
    expect(humanizeToolName('system.navigate')).toBe('Navigate')
  })

  it('handles hyphenated ids', () => {
    expect(humanizeToolName('do-a-thing')).toBe('Do a thing')
  })

  it('keeps minor connector words lowercase mid-label', () => {
    expect(humanizeToolName('system.return_to_default')).toBe('Return to default')
  })

  it('title-cases a bare verb', () => {
    expect(humanizeToolName('summarize')).toBe('Summarize')
  })
})

describe('humanizeToolName — edge cases', () => {
  it('returns empty string for empty input', () => {
    expect(humanizeToolName('')).toBe('')
  })

  it('falls back to the raw id when nothing meaningful can be derived', () => {
    // Only separators — split yields no words, so the raw id is returned.
    expect(humanizeToolName('...')).toBe('...')
  })
})
