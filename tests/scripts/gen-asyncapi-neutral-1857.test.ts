// @vitest-environment node
/**
 * gen-asyncapi-neutral-1857.test.ts — #1857 frontend round, fix round 1 (CHECK F3).
 *
 * RED tests for design-note decision D-B B1, TypeScript-generator half:
 * "Both generators check slot vocabulary and that the neutral_variants keys
 * equal the variants keys. They hard-fail when the chat form or a variant
 * contains a first-person token (\bI\b, I', I’, \bme\b, \bmy\b) and has no
 * neutral twin. ... TS validates only and emits nothing."
 * Oracle: /workspace/logs/1857/arch/design-note-frontend-round.md, D-B B1 and
 * "Test hooks"; spec notifications-spec.md §13.8.4 rule 4.
 *
 * scripts/_gen-asyncapi-types.mjs has no exported entry point and runs as a
 * script, so these tests run the REAL script in a child process against an
 * isolated fixture contract: a temp root holding a copy of the script, a copy
 * of the real contracts/asyncapi.yaml whose LLMError schema is replaced by a
 * small synthetic catalogue, and a node_modules symlink for js-yaml. Nothing in
 * the repository is read for writing; outputs go to the temp root.
 *
 * Controls prove the harness: the unmodified real contract and a valid
 * neutral-bearing fixture both exit 0. Sentences below are synthetic fixtures,
 * not product wording.
 */

import { spawnSync } from 'node:child_process'
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'

const REPO = resolve(fileURLToPath(new URL('../../', import.meta.url)))

type Entry = Record<string, unknown>

let root = ''

// Helper run inside the temp root: loads the real contract with js-yaml,
// swaps in the fixture catalogue (or keeps the real one) and writes the
// fixture contract the generator copy reads.
const MAKE_FIXTURE = `
import { createRequire } from 'node:module'
import { readFileSync, writeFileSync } from 'node:fs'
const require = createRequire(import.meta.url)
const yaml = require('js-yaml')
const [src, dst, entriesFile] = process.argv.slice(2)
const doc = yaml.load(readFileSync(src, 'utf8'))
if (entriesFile !== '-') {
  const entries = JSON.parse(readFileSync(entriesFile, 'utf8'))
  const schema = doc.components.schemas.LLMError
  schema.properties.code.enum = Object.keys(entries)
  schema['x-user-message-attributions'] = ['config', 'unknown']
  schema['x-user-messages'] = entries
}
writeFileSync(dst, yaml.dump(doc), 'utf8')
`

beforeAll(() => {
  root = mkdtempSync(join(tmpdir(), 'gen-neutral-1857-'))
  mkdirSync(join(root, 'scripts'), { recursive: true })
  mkdirSync(join(root, 'contracts'), { recursive: true })
  mkdirSync(join(root, 'src', 'lib', 'api', 'generated'), { recursive: true })
  copyFileSync(join(REPO, 'scripts', '_gen-asyncapi-types.mjs'), join(root, 'scripts', '_gen-asyncapi-types.mjs'))
  symlinkSync(join(REPO, 'node_modules'), join(root, 'node_modules'), 'dir')
  writeFileSync(join(root, 'make-fixture.mjs'), MAKE_FIXTURE, 'utf8')
})

afterAll(() => {
  if (root) rmSync(root, { recursive: true, force: true })
})

interface Run {
  status: number | null
  stderr: string
  stdout: string
}

/** Build the fixture contract (real one when entries is null) and run the generator on it. */
function runGenerator(entries: Record<string, Entry> | null): Run {
  const entriesFile = join(root, 'entries.json')
  if (entries) writeFileSync(entriesFile, JSON.stringify(entries), 'utf8')
  const make = spawnSync(
    process.execPath,
    [
      join(root, 'make-fixture.mjs'),
      join(REPO, 'contracts', 'asyncapi.yaml'),
      join(root, 'contracts', 'asyncapi.yaml'),
      entries ? entriesFile : '-',
    ],
    { cwd: root, encoding: 'utf8' },
  )
  expect(make.status, `SETUP: building the fixture contract failed: ${make.stderr}`).toBe(0)
  const gen = spawnSync(process.execPath, [join(root, 'scripts', '_gen-asyncapi-types.mjs')], {
    cwd: root,
    encoding: 'utf8',
  })
  return { status: gen.status, stderr: gen.stderr, stdout: gen.stdout }
}

function entry(message: string, extra: Entry = {}): Entry {
  return { message, attribution: 'config', ...extra }
}

describe('#1857 D-B B1 — TS generator neutral-form validation (child process)', () => {
  it('control: the unmodified real contract generates cleanly in the isolated root', () => {
    const run = runGenerator(null)
    expect(run.status, `SETUP: the harness must run the real script green: ${run.stderr}`).toBe(0)
    expect(existsSync(join(root, 'src', 'lib', 'api', 'generated', 'llm-error-messages.ts'))).toBe(true)
  })

  it('control: a first-person chat form WITH a neutral twin and matching neutral_variants passes and emits no neutral text', () => {
    const neutral = 'Neutral twin sentence for {cli}.'
    const neutralVariant = 'Neutral variant sentence at {n}.'
    const run = runGenerator({
      alpha: entry('I could not start {cli}. Check the setting.', {
        neutral,
        variants: { v1: 'I stopped {cli} at {n} turns.' },
        neutral_variants: { v1: neutralVariant },
      }),
    })
    expect(run.status, `D-B control: a valid neutral-bearing contract must pass: ${run.stderr}`).toBe(0)
    const out = readFileSync(join(root, 'src', 'lib', 'api', 'generated', 'llm-error-messages.ts'), 'utf8')
    // D-B B1: "TS validates only and emits nothing" for the neutral form.
    expect(out.includes(neutral), 'D-B B1: the TS catalogue must not emit the neutral sentence').toBe(false)
    expect(out.includes(neutralVariant), 'D-B B1: the TS catalogue must not emit the neutral variant').toBe(false)
  })

  it('control: a chat form with no first-person token needs no twin (Item / IP are not the pronoun)', () => {
    const run = runGenerator({
      alpha: entry('{cli} is not signed in. Sign in to {cli}, then retry.'),
      beta: entry('The Item list for the IP address is empty.'),
    })
    expect(run.status, `D-B control: no first person, no twin needed: ${run.stderr}`).toBe(0)
  })

  const firstPerson: Record<string, string> = {
    I_space: 'I could not start {cli}. Check the setting.',
    I_straight_quote: "I'll stop at {n} turns. Check the setting.",
    I_curly_quote: 'I’ve reached the limit of {n} turns. Check the setting.',
    me: 'Tell me when {cli} is ready again.',
    my: 'Check my setting for {cli}.',
    sentence_start_My: 'Open the page. My settings for {cli} are on it.',
  }
  for (const [name, chat] of Object.entries(firstPerson)) {
    it(`first-person chat form without neutral fails the TS generator (${name})`, () => {
      const run = runGenerator({ alpha: entry(chat) })
      expect(run.status, `D-B TS generator: a first-person chat form without a neutral twin must fail, got exit 0 for ${JSON.stringify(chat)}`).not.toBe(0)
      expect(run.stderr, 'D-B TS generator: the error must name the code').toContain('alpha')
      expect(run.stderr.toLowerCase(), 'D-B TS generator: the error must name the missing neutral form').toContain('neutral')
    })
  }

  it('first-person variant without a neutral_variants twin fails the TS generator', () => {
    const run = runGenerator({
      alpha: entry('{cli} stopped with an error.', { variants: { v1: 'I could not read the output of {cli}.' } }),
    })
    expect(run.status, 'D-B TS generator: a first-person variant without a neutral_variants twin must fail').not.toBe(0)
  })

  for (const [name, neutralVariants] of Object.entries({
    missing_key: { other: '{cli} was stopped.' },
    extra_key: { v1: '{cli} was stopped.', v2: '{cli} was stopped again.' },
    empty_set: {},
  })) {
    it(`neutral_variants keys differing from variants keys fail the TS generator (${name})`, () => {
      const run = runGenerator({
        alpha: entry('I could not start {cli}.', {
          neutral: '{cli} could not be started.',
          variants: { v1: 'I stopped {cli}.' },
          neutral_variants: neutralVariants,
        }),
      })
      expect(run.status, 'D-B TS generator: mismatched neutral_variants keys must fail').not.toBe(0)
    })
  }

  for (const bad of ['{provider} could not be started.', '{oops} stopped.', '{cli} could not start {host}.']) {
    it(`neutral form with an out-of-vocabulary slot fails the TS generator (${bad})`, () => {
      const run = runGenerator({ alpha: entry('I could not start {cli}.', { neutral: bad }) })
      expect(run.status, `D-B TS generator: out-of-vocabulary slot in neutral must fail, got exit 0 for ${bad}`).not.toBe(0)
    })
  }

  it('neutral_variants sentence with an out-of-vocabulary slot fails the TS generator', () => {
    const run = runGenerator({
      alpha: entry('I could not start {cli}.', {
        neutral: '{cli} could not be started.',
        variants: { v1: 'I stopped {cli}.' },
        neutral_variants: { v1: '{provider} was stopped.' },
      }),
    })
    expect(run.status, 'D-B TS generator: out-of-vocabulary slot in a neutral variant must fail').not.toBe(0)
  })

  it('a neutral form that is itself first person fails the TS generator', () => {
    const run = runGenerator({
      alpha: entry('I could not start {cli}.', { neutral: 'I could not start {cli} either.' }),
    })
    expect(run.status, 'D-B TS generator: a neutral form with a first-person token must fail').not.toBe(0)
  })
})
