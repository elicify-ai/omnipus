/**
 * llm-error.slots.test.ts — #1857 frontend round, RED tests for the SPA half
 * of design-note decision D-A ("trust the server-filled sentence through the
 * existing provider_message flag, with a fail-closed slot guard in the SPA").
 *
 * Oracles (never a sentence literal; W1–W21 are under founder review):
 *   - spec docs/internal/specs/notifications-spec.md §13.2 (a rendered
 *     sentence never carries a literal slot) and §11.4 (the browser never
 *     composes wording from facts; it shows the server's finished sentence or
 *     the catalogue copy);
 *   - design note /workspace/logs/1857/arch/design-note-frontend-round.md,
 *     D-A "SPA layer" and its "Test hooks": codeToMessage returns
 *     codeToDisplay.unknown for any code whose catalogue sentence has a slot;
 *     getLLMErrorDisplay rejects a trusted message that still matches
 *     /\{[a-z_]+\}/; both write one console.warn('llm_error.slot_guard',
 *     {code}) line with no message text.
 *   - expected texts are derived from the GENERATED catalogue
 *     (codeToDisplay, llmErrorMessageVariants) filled with the test facts
 *     below, never typed in.
 *
 * Every assertion message names the design-note item it enforces. These tests
 * are written RED: they fail on the current tree because codeToMessage and
 * getLLMErrorDisplay have no slot guard.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  llmErrorCodes,
  llmErrorMessageVariants,
} from './api/generated/llm-error-messages'
import {
  codeToAttribution,
  codeToDisplay,
  codeToMessage,
  getLLMErrorDisplay,
  readLLMErrorFromFrame,
  readLLMErrorFromReplayFrame,
  type LLMErrorCode,
} from './llm-error'

const SLOT = /\{[a-z_]+\}/
const GUARD_TAG = 'llm_error.slot_guard'

// Test facts. The slot names are the contract's closed message vocabulary
// ({cli} {model} {n} {minutes}); the values are test inputs, not catalogue text.
const FACTS: Record<string, string> = {
  cli: 'Claude Code',
  model: 'test-model-1857',
  n: '7',
  minutes: '3',
}

/** Fill every slot of a catalogue template from FACTS; an unknown slot is a test setup error. */
function fill(template: string): string {
  return template.replace(/\{([a-z_]+)\}/g, (_m, name: string) => {
    const value = FACTS[name]
    if (value === undefined) {
      throw new Error(`SETUP: catalogue slot {${name}} is outside the test facts`)
    }
    return value
  })
}

/** Every catalogue sentence of a code: its message plus its variants. */
function templatesOf(code: LLMErrorCode): string[] {
  const variants = llmErrorMessageVariants[code] ?? {}
  return [codeToDisplay[code], ...Object.values(variants)]
}

/** A code is slotted when any of its catalogue sentences carries a slot. */
function isSlotted(code: LLMErrorCode): boolean {
  return templatesOf(code).some((t) => SLOT.test(t))
}

/** The slot-free text a guard must fall back to for a code (design note D-A). */
function guardedFallback(code: LLMErrorCode): string {
  return SLOT.test(codeToDisplay[code]) ? codeToDisplay.unknown : codeToDisplay[code]
}

const slottedCodes = llmErrorCodes.filter((c) => isSlotted(c as LLMErrorCode)) as LLMErrorCode[]

type MessageKind = 'filled' | 'literal_slot' | 'empty'

/** A message of the given kind for a code, built from the generated catalogue. */
function messageFor(code: LLMErrorCode, kind: MessageKind, variantIndex = 0): string {
  if (kind === 'empty') return ''
  const templates = templatesOf(code)
  const template = templates[variantIndex % templates.length]
  if (kind === 'filled') return fill(template)
  // A literal-slot message: the catalogue template as stored (slots unfilled)
  // for a slotted code; for a slot-free code a synthetic sentence with a slot.
  return SLOT.test(template) ? template : 'The run in {cli} stopped.'
}

type Path = 'live' | 'replay'

/** Run a typed error through the real reader, then the real display function. */
function display(
  code: string,
  message: string,
  providerMessage: boolean | undefined,
  path: Path,
): { message: string; detail?: string } {
  const wire = {
    code,
    message,
    retryable: false,
    ...(providerMessage === undefined ? {} : { provider_message: providerMessage }),
  }
  const frame = { payload: { llm_error: wire } }
  const typed = path === 'live' ? readLLMErrorFromFrame(frame) : readLLMErrorFromReplayFrame(frame)
  expect(typed, `SETUP: the ${path} reader must accept a typed payload`).toBeDefined()
  return getLLMErrorDisplay(typed as NonNullable<typeof typed>, false)
}

let warn: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
})

afterEach(() => {
  warn.mockRestore()
})

/** The guard's own console.warn calls (tag first), ignoring any other warning. */
function guardCalls(): unknown[][] {
  return warn.mock.calls.filter((args: unknown[]) => args[0] === GUARD_TAG)
}

describe('#1857 D-A SPA layer — test setup is not vacuous', () => {
  it('finds the slotted codes in the generated catalogue', () => {
    // The backend round made nine codes slotted ({cli}/{model}/{n}/{minutes}).
    expect(slottedCodes.length, 'D-A SPA: the generated catalogue must expose slotted codes').toBeGreaterThanOrEqual(9)
    expect(slottedCodes).toContain('external_cli_invalid_args')
    expect(slottedCodes).toContain('agent_request_limit')
  })
})

describe('#1857 D-A SPA layer — codeToMessage never returns a slotted sentence', () => {
  it.each(llmErrorCodes as readonly LLMErrorCode[])('%s', (code) => {
    const out = codeToMessage(code)
    expect(
      SLOT.test(out),
      `D-A SPA layer: codeToMessage("${code}") returned a literal slot: ${out}`,
    ).toBe(false)
    expect(out, `D-A SPA layer: codeToMessage("${code}") must return the guarded fallback`).toBe(guardedFallback(code))
    const fired = guardCalls()
    if (SLOT.test(codeToDisplay[code])) {
      expect(fired, `D-A SPA layer: the guard must write exactly one ${GUARD_TAG} line for "${code}"`).toHaveLength(1)
      expect(fired[0][1], 'D-A SPA layer: the guard line carries {code} only, no message text').toEqual({ code })
    } else {
      expect(fired, `D-A SPA layer: no guard line for the slot-free code "${code}"`).toHaveLength(0)
    }
  })
})

describe('#1857 D-A SPA layer — property: no raw slot ever reaches the screen', () => {
  const flags: Array<boolean | undefined> = [true, undefined]
  const kinds: MessageKind[] = ['filled', 'literal_slot', 'empty']
  const paths: Path[] = ['live', 'replay']

  it.each(llmErrorCodes as readonly LLMErrorCode[])('%s', (code) => {
    for (const flag of flags) {
      for (const kind of kinds) {
        for (const path of paths) {
          warn.mockClear()
          const message = messageFor(code, kind)
          const where = `code=${code} provider_message=${String(flag)} message=${kind} path=${path}`
          const out = display(code, message, flag, path).message

          // Property: output never matches the slot pattern (D-A, both layers).
          expect(SLOT.test(out), `D-A SPA layer: literal slot reached the display (${where}): ${out}`).toBe(false)

          // Expected value, derived from the design note's rules and the
          // existing trust gates (judge_conclusion_failed never trusted;
          // delegated_task_limit trusted without the flag).
          const trustedByFlag = flag === true && message.trim().length > 0 && code !== 'judge_conclusion_failed'
          const trustedByCode = code === 'delegated_task_limit' && message.trim().length > 0
          const trusted = trustedByFlag || trustedByCode
          const messageHasSlot = SLOT.test(message)
          const want = trusted && !messageHasSlot ? message : guardedFallback(code)
          expect(out, `D-A SPA layer: wrong text (${where})`).toBe(want)

          // The guard fires iff a trusted message carried a slot, or the
          // catalogue fallback itself is slotted; exactly one line either way.
          const guardFires = (trusted && messageHasSlot) || (!(trusted && !messageHasSlot) && SLOT.test(codeToDisplay[code]))
          const fired = guardCalls()
          expect(
            fired.length,
            `D-A SPA layer: expected ${guardFires ? 'exactly one' : 'no'} ${GUARD_TAG} line (${where}), got ${fired.length}`,
          ).toBe(guardFires ? 1 : 0)
          if (guardFires) {
            expect(fired[0][1], `D-A SPA layer: guard line carries {code} only (${where})`).toEqual({ code })
            const serialized = JSON.stringify(warn.mock.calls)
            if (message.length > 0) {
              expect(
                serialized.includes(message),
                `D-A SPA layer: the guard line leaked the message text (${where})`,
              ).toBe(false)
            }
          }
        }
      }
    }
  })
})

describe('#1857 D-A SPA layer — a filled, flagged message is shown byte for byte', () => {
  it.each(slottedCodes)('%s (message and every variant)', (code) => {
    const templates = templatesOf(code)
    for (let i = 0; i < templates.length; i++) {
      for (const path of ['live', 'replay'] as Path[]) {
        warn.mockClear()
        const filled = fill(templates[i])
        const out = display(code, filled, true, path).message
        expect(out, `D-A SPA layer: the server-filled sentence must be shown as sent (variant ${i}, ${path})`).toBe(filled)
        expect(guardCalls(), 'D-A SPA layer: a correctly filled sentence must not fire the guard').toHaveLength(0)
      }
    }
  })

  it.each(slottedCodes)('%s without the flag shows the generic sentence, never a slot', (code) => {
    const filled = fill(codeToDisplay[code])
    const out = display(code, filled, undefined, 'live').message
    expect(
      out,
      'D-A SPA layer: with no provider_message flag the SPA must not trust the text; a slotted code shows codeToDisplay.unknown',
    ).toBe(codeToDisplay.unknown)
  })
})

describe('#1857 D-A SPA layer — each guard has its own mutation check', () => {
  it('getLLMErrorDisplay rejects a trusted slotted message even for a slot-free code', () => {
    // codeToMessage("rate_limited") is slot-free, so removing the guard inside
    // getLLMErrorDisplay (while keeping the one in codeToMessage) must fail here.
    const out = display('rate_limited', 'Wait {minutes} minutes.', true, 'live').message
    expect(out, 'D-A SPA layer: getLLMErrorDisplay must refuse a trusted message that still has a slot').toBe(
      codeToDisplay.rate_limited,
    )
    expect(guardCalls(), 'D-A SPA layer: exactly one guard line').toHaveLength(1)
    expect(guardCalls()[0][1]).toEqual({ code: 'rate_limited' })
  })

  it('an unrecognised code with a trusted slotted message falls back to the unknown sentence', () => {
    const out = display('something_new_in_v2', 'Stopped in {cli}.', true, 'replay').message
    expect(out, 'D-A SPA layer: forward-compat code with a slot must show the generic sentence').toBe(
      codeToDisplay.unknown,
    )
    expect(SLOT.test(out)).toBe(false)
  })
})

describe('#1857 C3 / R6 — copy rules on the chat form of every new variant', () => {
  const banned = ['switch models', 'switch model.', 'different model', 'another model', 'pick a model']

  it('config- and product-attributed catalogue variants never push model switching; config ones never say retry', () => {
    let checked = 0
    for (const code of llmErrorCodes as readonly LLMErrorCode[]) {
      const attribution = codeToAttribution[code]
      if (attribution !== 'config' && attribution !== 'product') continue
      for (const sentence of templatesOf(code)) {
        const lower = sentence.toLowerCase()
        checked++
        for (const phrase of banned) {
          expect(lower.includes(phrase), `C3: "${code}" (${attribution}) tells the user to shop for a model: ${sentence}`).toBe(false)
        }
        if (attribution === 'config') {
          for (const phrase of ['retry', 'try again']) {
            expect(lower.includes(phrase), `C3: "${code}" is a config fault but says ${phrase}: ${sentence}`).toBe(false)
          }
        }
      }
    }
    expect(checked, 'C3: the rule must see real sentences').toBeGreaterThan(10)
  })
})
