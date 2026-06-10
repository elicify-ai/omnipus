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
