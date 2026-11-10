// capabilities.test.ts — RED tests for the served-capability → presentation
// classification (dataset D3, FR-052).
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
//
// RED reason: src/lib/effort/capabilities.ts is scaffolding and throws — every
// classification assertion fails until GREEN implements the D3 rules.

import { describe, it, expect } from 'vitest'
import { classifyEffortPresentation, type ServedReasoning } from './capabilities'

// Traces to: effort-levels-spec-r10.md D3 (rows 1-13), S-07 (presentation
// examples), FR-052.
describe('classifyEffortPresentation — dataset D3, served capability to presentation', () => {
  it.each([
    // D3 rows are keyed by their "#" column; `served` is the object the SPA
    // receives after the server's route-encoder filter.
    { row: 1, name: 'levels low, medium, high', served: { levels: ['low', 'medium', 'high'], switch: false }, expected: 'chip' },
    { row: 2, name: 'levels none, low, high, max (as published)', served: { levels: ['none', 'low', 'high', 'max'], switch: false }, expected: 'chip' },
    { row: 3, name: 'levels [none] only', served: { levels: ['none'] }, expected: 'chip' },
    { row: 4, name: 'levels [] with no switch', served: { levels: [], switch: false }, expected: 'muted' },
    { row: 5, name: 'switch only (OpenRouter)', served: { levels: [], switch: true }, expected: 'chip' },
    { row: 6, name: 'budget + switch (OpenRouter)', served: { levels: [], switch: true }, expected: 'chip' },
    { row: 7, name: 'levels low, high plus switch', served: { levels: ['low', 'high'], switch: true }, expected: 'chip' },
    { row: 8, name: 'budget only', served: { levels: [], switch: false }, expected: 'muted' },
    { row: 9, name: 'reasoning flagged, no control', served: { levels: [], switch: false }, expected: 'muted' },
    { row: 11, name: 'levels with no route encoder (other Bedrock family)', served: { levels: [], switch: false }, expected: 'muted' },
    { row: 12, name: 'switch with a generic encoder that cannot encode switch', served: { levels: [], switch: false }, expected: 'muted' },
    { row: 13, name: 'openai-chatgpt copy of openai (chip)', served: { levels: ['low', 'medium', 'high'], switch: false }, expected: 'chip' },
  ])('D3 row $row ($name) → $expected', ({ served, expected }) => {
    expect(classifyEffortPresentation(served)).toBe(expected)
  })
})

// Traces to: effort-levels-spec-r10.md D3 row 10, S-07 (no reasoning object /
// no catalog row → no chip).
describe('classifyEffortPresentation — absent served object', () => {
  it.each([
    { name: 'undefined (no reasoning object)', served: undefined },
    { name: 'null (no reasoning object)', served: null },
    { name: 'custom or local model with no catalog row', served: undefined },
  ])('$name → none', ({ served }) => {
    expect(classifyEffortPresentation(served)).toBe('none')
  })
})

// Traces to: effort-levels-spec-r10.md S-07 (object present, no levels, no
// switch → muted; levels/switch members absent from a present object behave
// like D3 rows 8/9), FR-052.
describe('classifyEffortPresentation — present object with missing members', () => {
  it('treats a present object with no members as muted (budget-only shape)', () => {
    expect(classifyEffortPresentation({} as ServedReasoning)).toBe('muted')
  })

  it('treats null levels with a served switch as chip (switch-only)', () => {
    expect(classifyEffortPresentation({ levels: null, switch: true })).toBe('chip')
  })

  it('treats null levels without a switch as muted (nothing sendable)', () => {
    expect(classifyEffortPresentation({ levels: null, switch: false })).toBe('muted')
  })
})
