// Preserve AsyncAPI-owned array item validators across the OpenAPI/AsyncAPI seam.
// --check validates existing output without rewriting it (also used by mutation probes).
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';

const args = process.argv.slice(2);
const checkOnly = args[0] === '--check';
if (checkOnly) args.shift();
assert.equal(args.length, 3, 'usage: _gen-ts-shared-refs.mjs [--check] <schemas.ts> <contracts> <node_modules>');
const [generatedPath, contracts, nodeModules] = args.map((arg) => resolve(arg));
const require = createRequire(resolve(nodeModules, 'package.json'));
const yaml = require('js-yaml');

function sharedArrayRefs() {
  const load = (path) => yaml.load(readFileSync(path, 'utf8'));
  const openapi = load(resolve(contracts, 'openapi.yaml'));
  const asyncapi = load(resolve(contracts, 'asyncapi.yaml'));
  const refs = [];
  for (const [owner, component] of Object.entries(openapi.components.schemas)) {
    const schemaPath = component.$ref && !component.$ref.startsWith('#')
      ? resolve(contracts, component.$ref) : undefined;
    const schema = schemaPath ? load(schemaPath) : component;
    for (const [property, field] of Object.entries(schema.properties ?? {})) {
      const target = field.items?.['x-go-type'];
      if (field.type !== 'array' || !field.items?.$ref || !asyncapi.components.schemas[target]) continue;
      // OpenAPI-root names already have their own generation path. Only bridge
      // external references to names owned exclusively by the AsyncAPI fragment.
      if (openapi.components.schemas[target]) continue;
      assert.match(target, /^[A-Za-z_][A-Za-z0-9_]*$/, `${owner}.${property}: invalid named type`);
      assert.ok(schemaPath && !field.items.$ref.startsWith('#'), `${owner}.${property}: expected external item ref`);
      const source = load(resolve(dirname(schemaPath), field.items.$ref));
      assert.equal(source.title, target, `${owner}.${property}: external ref must name ${target}`);
      refs.push({ owner, property, target });
    }
  }
  return refs;
}

function reuseSharedArrayRefs(refs) {
  const ts = require('typescript');
  let source = readFileSync(generatedPath, 'utf8');
  const tree = ts.createSourceFile(generatedPath, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  assert.equal(tree.parseDiagnostics.length, 0, 'shared refs: generated TypeScript must parse');
  const declarations = new Map(tree.statements.filter(ts.isVariableStatement)
    .flatMap((statement) => statement.declarationList.declarations.map((node) => [node.name.getText(tree), node.initializer])));
  const edits = [];
  // Follow only the outer method chain, never a nested object's array fields.
  function call(initializer, method) {
    let node = initializer;
    while (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)) {
      if (node.expression.name.text === method && node.expression.expression.getText(tree) === 'z') return node;
      node = node.expression.expression;
    }
    assert.fail(`shared refs: expected z.${method}(...) in generated schema`);
  }
  for (const { owner, property, target } of refs) {
    assert.ok(declarations.has(owner) && declarations.has(target), `${owner}.${property}: missing named declarations`);
    const object = call(declarations.get(owner), 'object').arguments[0];
    assert.ok(ts.isObjectLiteralExpression(object), `${owner}: expected object properties`);
    const field = object.properties.find((node) => ts.isPropertyAssignment(node) && node.name.getText(tree) === property);
    assert.ok(field, `${owner}.${property}: missing array property`);
    const array = call(field.initializer, 'array');
    assert.equal(array.arguments.length, 1, `${owner}.${property}: expected one array item`);
    // RoomEntry is declared in the appended AsyncAPI fragment, after REST
    // endpoints are constructed; a lazy reference avoids early evaluation.
    edits.push({ start: array.arguments[0].getStart(tree), end: array.arguments[0].end,
      text: `z.lazy(() => ${target})` });
  }
  for (const edit of edits.sort((a, b) => b.start - a.start)) {
    source = source.slice(0, edit.start) + edit.text + source.slice(edit.end);
  }
  writeFileSync(generatedPath, source);
}

function checkRoomEntry(named, item) {
  // Fixture fields come from RoomEntry.yaml. Timestamp is an equivalence probe,
  // not a new format rule: this seam must preserve the named validator exactly.
  const entry = {
    id: 'entry-1', room_id: 'room-1', seq: 1, author: { kind: 'user' },
    text: 'history', addressees: [], after_pause: false,
    deliveries: [{ agent_id: 'agent-1', request_id: 'request-1', state: 'recorded' }],
    timestamp: '2026-10-18T00:00:00Z',
  };
  const missing = { ...entry };
  delete missing.after_pause;
  const cases = [
    ['canonical', entry, true],
    ['unknown_entry_field', { ...entry, unexpected: true }, false],
    ['unknown_author_field', { ...entry, author: { ...entry.author, unexpected: true } }, false],
    ['unknown_delivery_field', { ...entry, deliveries: [{ ...entry.deliveries[0], unexpected: true }] }, false],
    ['missing_required_after_pause', missing, false],
    ['invalid_timestamp', { ...entry, timestamp: 'not-a-date' }, undefined],
  ];
  const normalize = (result) => result.success ? result.data : result.error.issues;
  const results = cases.map(([name, input, expected]) => {
    const source = named.safeParse(input), history = item.safeParse(input);
    console.log(JSON.stringify({ case: name, named_accepted: source.success, history_accepted: history.success }));
    if (expected !== undefined) assert.equal(source.success, expected, `RoomEntry control: ${name}`);
    return { name, source, history };
  });
  for (const { name, source, history } of results) {
    assert.equal(history.success, source.success, `RoomHistoryResponse.entries mismatch: ${name}`);
    assert.deepEqual(normalize(history), normalize(source), `RoomHistoryResponse.entries result mismatch: ${name}`);
  }
}

async function checkSharedArrayRefs(refs) {
  const schemas = await import(pathToFileURL(generatedPath).href);
  for (const { owner, property, target } of refs) {
    const named = schemas[target];
    const item = schemas[owner]?.shape?.[property]?.element;
    assert.ok(named && item, `${owner}.${property}: missing generated validators`);
    if (target === 'RoomEntry') checkRoomEntry(named, item);
    assert.equal(item.schema ?? item, named, `${owner}.${property} must reuse the named ${target} validator`);
    if (owner === 'RoomHistoryResponse') {
      const valid = { entries: [], has_older: false };
      assert.equal(schemas[owner].safeParse(valid).success, true, 'canonical empty history');
      for (const [name, input] of [
        ['unknown envelope field', { ...valid, unexpected: true }],
        ['null entries', { ...valid, entries: null }],
        ['missing has_older', { entries: [] }],
        ['zero cursor', { ...valid, next_before_seq: 0 }],
      ]) assert.equal(schemas[owner].safeParse(input).success, false, name);
    }
    console.log(`shared validator PASS: ${owner}.${property} === ${target}; envelope controls PASS`);
  }
  console.log(`shared validator self-check PASS: ${refs.length} binding(s)`);
}

const refs = sharedArrayRefs();
if (!checkOnly) reuseSharedArrayRefs(refs);
await checkSharedArrayRefs(refs);
