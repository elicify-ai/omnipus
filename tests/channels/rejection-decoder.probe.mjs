// S-90 / T-30c cross-language probe. Run ONLY after the real Go producer
// exported short-envelope.json in a fresh directory; see run-rejection-bridge.sh.
// No copied server fixture, no nested test process, no implementation mutation.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const bodyPath = process.argv[2]
assert.ok(bodyPath, 'BLOCKED: pass the fresh Go-produced short-envelope body; a fixture is not S-90 proof')
const raw = readFileSync(bodyPath, 'utf8')
const produced = JSON.parse(raw)
assert.equal(produced.code, 'invalid_targets')
assert.equal(produced.target_errors.length, 1)
assert.equal(produced.target_errors[0].index, 0)
assert.equal(produced.target_errors[0].target, '')
assert.equal(produced.target_errors[0].code, 'invalid_id')
assert.equal(typeof produced.target_errors[0].reason, 'string')
assert.ok(produced.target_errors[0].reason.length > 0, 'S-90: the indexed reason must survive')
assert.ok(Buffer.byteLength(raw) < 4096, 'S-90: actual server body stays below the decoder cap')

// Load the actual current decoder source, stripping types only. ApiError has
// no runtime imports today; if that changes, module loading fails loudly rather
// than silently switching to a hand-copied decoder. transpileModule is NOT a
// type-check claim; npm run typecheck remains the separate compiler gate.
const decoderPath = fileURLToPath(new URL('../../src/lib/api-error.ts', import.meta.url))
const decoderSource = readFileSync(decoderPath, 'utf8')
const compiled = ts.transpileModule(decoderSource, {
  fileName: decoderPath,
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
  reportDiagnostics: true,
})
assert.deepEqual(compiled.diagnostics ?? [], [], 'instrument: decoder transpilation must not hide diagnostics')
const { ApiError } = await import(`data:text/javascript;base64,${Buffer.from(compiled.outputText).toString('base64')}`)
assert.equal(typeof ApiError.fromResponse, 'function', 'instrument: call the real decoder, not a substitute')
const response = (body) => new globalThis.Response(body, { status: 400, headers: { 'Content-Type': 'application/json' } })

const intact = await ApiError.fromResponse(response(raw))
assert.ok(intact instanceof ApiError)
assert.equal(intact.status, 400)
assert.equal(intact.code, 'invalid_targets')
assert.equal(intact.userMessage, produced.error)
assert.equal(intact.body, raw, 'S-90: preserve every actual server byte, including its newline')
assert.deepEqual(JSON.parse(intact.body).target_errors, produced.target_errors)

// A consumer negative control, not a production mutation: change just the
// actual produced item's hint to the original raw DEL prefix. It must expose
// the specified binary-sniff failure mode in this very envelope.
const rawEcho = JSON.stringify({
  ...produced,
  target_errors: [{ ...produced.target_errors[0], target: '\u007f'.repeat(20) }],
})
const sample = rawEcho.slice(0, 256)
const dels = [...sample].filter((character) => character === '\u007f').length
assert.equal(dels, 20, 'instrument: all twenty DELs are in the real decoder sample')
assert.ok(dels / sample.length > 0.05, 'instrument: raw echo must cross the 5% sniff threshold')
const discarded = await ApiError.fromResponse(response(rawEcho))
assert.equal(discarded.status, 400)
assert.equal(discarded.code, undefined)
assert.equal(discarded.body, undefined)
assert.notEqual(discarded.userMessage, produced.error)

console.log(JSON.stringify({
  result: 'PASS', scope: 'S-90 actual producer body -> real ApiError.fromResponse; raw-echo consumer control',
  bodyPath, bodyBytes: Buffer.byteLength(raw),
  bodySHA256: createHash('sha256').update(raw).digest('hex'),
  decoderPath, decoderSHA256: createHash('sha256').update(decoderSource).digest('hex'),
}))
