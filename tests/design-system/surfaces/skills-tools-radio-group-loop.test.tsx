// Unit test of the catalogued RadioGroup's `loop` prop. Fix round 4, Part I, team-lead Q3.
// This file is NOT a registered check of the design-system catalog (it names no surface). It is
// written from the ruling alone, by the test author; the developer's own test of the prop lives in
// src/components/ui/radio-group.test.tsx and is not the oracle of this one.
//
// Oracle: the architect ruling of 2026-11-26 on the Skills & Tools trust block
// (/workspace/logs/skills-tools-1519f/arch/ruling-trust-arrows.md), section "How to build it":
//   "Add one optional prop to RadioGroup: loop?: boolean, default true, so every existing caller
//    keeps its behaviour. With loop={false}, ArrowRight/Down on the last option and ArrowLeft/Up
//    on the first do nothing: no focus move, no onValueChange. Home and End are unchanged."
// and its rows 2 to 7, which describe a group of three options (Allow, Ask, Deny; First, Middle
// and Last here, because the component is generic): an arrow inside the range moves focus and
// selects, Home selects the first option and End the last, and the option in between is skipped
// and never reported.
//
// What is not pinned, because the ruling does not say it: whether a handled key calls
// preventDefault, and whether Home on the first option or End on the last reports its own value
// again (the page decides that: ruling row 8).
//
// The component's code was read only to learn how to render and drive it (its props and roles);
// no expected value comes from it.
//
// RED proof: against the frontend head c6f7f29b6, where the arrows wrap round whatever `loop`
// says; receipt in the Part I pack. Five tests are RED there, each for the wrap: the four tests of
// an arrow at an end, and the walk test, which runs into both ends. The other twenty (the arrows
// inside the range, Home and End, and the default and loop={true} wrap) pin behaviour that already
// exists and stay green there; they are the guard that "every existing caller keeps its
// behaviour". GREEN, mutation and proof of failability: deferred to CHECK.
import { describe, expect, it } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'

const OPTIONS = ['first', 'middle', 'last'] as const
type Option = (typeof OPTIONS)[number]
const NAME: Record<Option, string> = { first: 'First', middle: 'Middle', last: 'Last' }

type Key = 'ArrowRight' | 'ArrowDown' | 'ArrowLeft' | 'ArrowUp' | 'Home' | 'End'

// A real controlled group, as every caller uses it: the checked value lives in state, and every
// value the group reports through onValueChange is recorded, in order.
function Group({ initial, loop, record }: { initial: Option; loop: boolean | undefined; record: Option[] }) {
  const [value, setValue] = useState<string>(initial)
  return (
    <RadioGroup
      aria-label="Choice"
      value={value}
      loop={loop}
      onValueChange={(next) => {
        record.push(next as Option)
        setValue(next)
      }}
    >
      {OPTIONS.map((option) => (
        <RadioGroupItem key={option} value={option}>
          {NAME[option]}
        </RadioGroupItem>
      ))}
    </RadioGroup>
  )
}

// Renders the group with `initial` checked and focus on it, as a person who tabbed in has it.
function openGroup(initial: Option, loop: boolean | undefined) {
  const record: Option[] = []
  render(<Group initial={initial} loop={loop} record={record} />)
  const radio = (option: Option) => screen.getByRole('radio', { name: NAME[option] })
  const reading = () => ({
    checked: OPTIONS.filter((option) => radio(option).getAttribute('aria-checked') === 'true'),
    focused: OPTIONS.find((option) => radio(option) === document.activeElement) ?? 'elsewhere',
  })
  act(() => radio(initial).focus())
  expect(reading(), 'instrument: the group starts with the option checked and focused').toEqual({ checked: [initial], focused: initial })
  return { record, reading, user: userEvent.setup() }
}

async function press(user: ReturnType<typeof userEvent.setup>, key: Key) {
  await user.keyboard(`{${key}}`)
}

// A short real wait after a key that must do nothing, so that a move that comes a moment after the
// key event (a deferred focus) is seen as well.
const settle = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)) })

describe('RadioGroup loop={false}: the arrows stop at the first and the last option (ruling 2026-11-26)', () => {
  it.each(['ArrowRight', 'ArrowDown'] as const)('%s on the last option does nothing: focus stays on it and no value is reported', async (key) => {
    const { record, reading, user } = openGroup('last', false)

    await press(user, key)
    await settle()

    expect(reading(), `${key} on the last option, loop={false}`).toEqual({ checked: ['last'], focused: 'last' })
    expect(record, `${key} on the last option reports no value`).toEqual([])
  })

  it.each(['ArrowLeft', 'ArrowUp'] as const)('%s on the first option does nothing: focus stays on it and no value is reported', async (key) => {
    const { record, reading, user } = openGroup('first', false)

    await press(user, key)
    await settle()

    expect(reading(), `${key} on the first option, loop={false}`).toEqual({ checked: ['first'], focused: 'first' })
    expect(record, `${key} on the first option reports no value`).toEqual([])
  })
})

describe('RadioGroup loop={false}: inside the range an arrow moves focus and selects; Home and End are unchanged (ruling rows 2 to 7)', () => {
  it.each([
    ['ArrowRight', 'first', 'middle'],
    ['ArrowDown', 'first', 'middle'],
    ['ArrowRight', 'middle', 'last'],
    ['ArrowDown', 'middle', 'last'],
    ['ArrowLeft', 'last', 'middle'],
    ['ArrowUp', 'last', 'middle'],
    ['ArrowLeft', 'middle', 'first'],
    ['ArrowUp', 'middle', 'first'],
  ] as const)('%s on the %s option checks and focuses the %s option and reports it once', async (key, from, to) => {
    const { record, reading, user } = openGroup(from, false)

    await press(user, key)

    expect(reading(), `${key} from ${from}`).toEqual({ checked: [to], focused: to })
    expect(record, `${key} from ${from} reports ${to}, once`).toEqual([to])
  })

  it.each([
    ['Home', 'last', 'first'],
    ['Home', 'middle', 'first'],
    ['End', 'first', 'last'],
    ['End', 'middle', 'last'],
  ] as const)('%s on the %s option checks and focuses the %s option and reports only that one (the option in between is skipped)', async (key, from, to) => {
    const { record, reading, user } = openGroup(from, false)

    await press(user, key)

    expect(reading(), `${key} from ${from}`).toEqual({ checked: [to], focused: to })
    expect(record, `${key} from ${from} reports ${to}, once`).toEqual([to])
  })

  it('a walk that runs into both ends reports exactly the moves that happened, in order', async () => {
    const { record, reading, user } = openGroup('middle', false)
    // Right (to last), Right (end: nothing), Left, Left (to first), Left (end: nothing), End, Home.
    const steps: ReadonlyArray<readonly [Key, Option]> = [
      ['ArrowRight', 'last'],
      ['ArrowRight', 'last'],
      ['ArrowLeft', 'middle'],
      ['ArrowLeft', 'first'],
      ['ArrowLeft', 'first'],
      ['End', 'last'],
      ['Home', 'first'],
    ]
    for (const [index, [key, expected]] of steps.entries()) {
      await press(user, key)
      await settle()
      expect(reading(), `after step ${index + 1} of ${steps.length} (${key})`).toEqual({ checked: [expected], focused: expected })
    }

    expect(record, 'only the keys that moved reported a value; the two keys at an end reported none').toEqual(['last', 'middle', 'first', 'last', 'first'])
  })
})

describe('RadioGroup default and loop={true}: the arrows still wrap round (ruling: default true, every existing caller keeps its behaviour)', () => {
  const MODES = [
    ['the prop left out', undefined],
    ['loop={true}', true],
  ] as const

  it.each(MODES.flatMap(([mode, loop]) => (['ArrowRight', 'ArrowDown'] as const).map((key) => [mode, key, loop] as const)))(
    'with %s, %s on the last option moves to the first option and reports it',
    async (_mode, key, loop) => {
      const { record, reading, user } = openGroup('last', loop)

      await press(user, key)

      expect(reading(), `${key} on the last option wraps to the first`).toEqual({ checked: ['first'], focused: 'first' })
      expect(record, `${key} on the last option reports the first`).toEqual(['first'])
    },
  )

  it.each(MODES.flatMap(([mode, loop]) => (['ArrowLeft', 'ArrowUp'] as const).map((key) => [mode, key, loop] as const)))(
    'with %s, %s on the first option moves to the last option and reports it',
    async (_mode, key, loop) => {
      const { record, reading, user } = openGroup('first', loop)

      await press(user, key)

      expect(reading(), `${key} on the first option wraps to the last`).toEqual({ checked: ['last'], focused: 'last' })
      expect(record, `${key} on the first option reports the last`).toEqual(['last'])
    },
  )
})
