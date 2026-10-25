// R8/M2 guard: active workspace producers share one key AND one signal-aware
// queryFn. Fixtures exercise the guard itself; the repository sweep remains real.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import ts from 'typescript'
import { describe, expect, it } from 'vitest'

const ROOT = join(__dirname, '..')
const CENTRAL = join(ROOT, 'lib', 'workspaceQueries.ts')
const INDEPENDENT = 'independent active-workspace producer'
const OWN_FN = 'own queryFn overrides shared options'
const OWN_KEY = 'own queryKey overrides shared options'
function sources(dir: string): string[] {
  return readdirSync(dir).flatMap(name => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sources(path)
    return /\.(ts|tsx)$/.test(name) && !/\.(test|spec|stories)\.tsx?$/.test(name) ? [path] : []
  })
}

function unwrap(expression: ts.Expression): ts.Expression {
  while (ts.isParenthesizedExpression(expression) || ts.isAsExpression(expression)
    || ts.isSatisfiesExpression(expression) || ts.isTypeAssertionExpression(expression) || ts.isNonNullExpression(expression)) {
    expression = expression.expression
  }
  return expression
}

function propertyName(name: ts.PropertyName): string | undefined {
  if (ts.isIdentifier(name) || ts.isStringLiteralLike(name)) return name.text
  if (ts.isComputedPropertyName(name)) {
    const value = unwrap(name.expression)
    if (ts.isStringLiteralLike(value)) return value.text
  }
  return undefined
}

function lastName(expression: ts.Expression): string | undefined {
  const value = unwrap(expression)
  if (ts.isIdentifier(value)) return value.text
  if (ts.isPropertyAccessExpression(value)) return value.name.text
  if (ts.isElementAccessExpression(value)) {
    const key = unwrap(value.argumentExpression)
    if (ts.isStringLiteralLike(key)) return key.text
  }
  return undefined
}

function propertyValue(object: ts.ObjectLiteralExpression, name: string): ts.Expression | undefined {
  const property = object.properties.find((item) => !ts.isSpreadAssignment(item) && propertyName(item.name) === name)
  return property && ts.isPropertyAssignment(property) ? unwrap(property.initializer) : undefined
}

function activeParameters(expression: ts.Expression | undefined): boolean {
  if (!expression) return false
  const value = unwrap(expression)
  const status = ts.isObjectLiteralExpression(value) ? propertyValue(value, 'status') : undefined
  return !!status && ts.isStringLiteralLike(status) && status.text === 'active'
}

function activeKey(expression: ts.Expression | undefined): boolean {
  if (!expression) return false
  const value = unwrap(expression)
  if (ts.isArrayLiteralExpression(value)) {
    const first = value.elements[0] && unwrap(value.elements[0])
    return value.elements.length === 2 && !!first && ts.isStringLiteralLike(first)
      && first.text === 'workspaces' && activeParameters(value.elements[1])
  }
  if (!ts.isCallExpression(value) || lastName(value.expression) !== 'list') return false
  const callee = unwrap(value.expression)
  const owner = ts.isPropertyAccessExpression(callee) || ts.isElementAccessExpression(callee) ? callee.expression : undefined
  return !!owner && lastName(owner) === 'workspacesQueryKeys' && activeParameters(value.arguments[0])
}

const PRODUCERS = new Set(['useQuery', 'useSuspenseQuery', 'useInfiniteQuery', 'useSuspenseInfiniteQuery',
  'fetchQuery', 'prefetchQuery', 'ensureQueryData', 'fetchInfiniteQuery', 'prefetchInfiniteQuery', 'ensureInfiniteQueryData'])
function directProducerOptions(object: ts.ObjectLiteralExpression): boolean {
  let current: ts.Node = object
  let parent = current.parent
  while (parent && (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent)
    || ts.isTypeAssertionExpression(parent) || ts.isNonNullExpression(parent))) {
    current = parent
    parent = current.parent
  }
  return !!parent && ts.isCallExpression(parent) && parent.arguments.some(argument => argument === current)
    && PRODUCERS.has(lastName(parent.expression) ?? '')
}

function violations(source: string, file = join(ROOT, 'guard-fixture.tsx')): string[] {
  if (file === CENTRAL) return []
  const syntax = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  // Current installed compiler exposes parseDiagnostics on SourceFile at
  // runtime. Fail closed if it vanishes or parsing fails; never trust an empty
  // walk of malformed input. Reflect keeps this internal field type-narrowed.
  const diagnostics: unknown = Reflect.get(syntax, 'parseDiagnostics')
  if (!Array.isArray(diagnostics) || diagnostics.length) throw new SyntaxError(`Cannot parse workspace producer source: ${file}`)
  const found: string[] = []
  const visit = (node: ts.Node) => {
    if (ts.isObjectLiteralExpression(node)) {
      const ownNames = new Set(node.properties.flatMap(property => ts.isSpreadAssignment(property) ? [] : [propertyName(property.name)]))
      const shared = node.properties.some(property => ts.isSpreadAssignment(property) && lastName(property.expression) === 'activeWorkspacesQueryOptions')
      if (shared) {
        // A local key can evade cancellation just as a local function can
        // discard its signal. Property order/quotes do not change ownership.
        if (ownNames.has('queryFn')) found.push(OWN_FN)
        if (ownNames.has('queryKey')) found.push(OWN_KEY)
      } else if (activeKey(propertyValue(node, 'queryKey')) && (ownNames.has('queryFn') || directProducerOptions(node))) {
        found.push(INDEPENDENT)
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(syntax)
  return found
}

const NEGATIVE_CASES = [
  {
    name: 'shared spread with an own signal-less queryFn (r13 m06 spread-nosignal)',
    source: `useQuery({ ...activeWorkspacesQueryOptions, queryFn: () => activeWorkspacesQueryOptions.queryFn({ signal: undefined as unknown as AbortSignal }), enabled: open })`,
    expected: OWN_FN,
  },
  {
    name: 'shared spread with a hand-built fetchWorkspaces queryFn (founder A)',
    source: `useQuery({ ...activeWorkspacesQueryOptions, queryFn: () => fetchWorkspaces({status:'active'}) })`,
    expected: OWN_FN,
  },
  {
    name: 'own queryFn before the shared spread',
    source: `useQuery({ queryFn: () => fetchWorkspaces({status:'active'}), ...activeWorkspacesQueryOptions })`,
    expected: OWN_FN,
  },
  {
    name: 'factory key after queryFn (founder B)',
    source: `useQuery({ queryFn: () => fetchWorkspaces({status:'active'}), queryKey: workspacesQueryKeys.list({status:'active'}) })`,
    expected: INDEPENDENT,
  },
  {
    name: 'literal active key after queryFn (r13 m06 reordered)',
    source: `useQuery({ queryFn: () => checkFetchWorkspaces({ status: 'active' }), queryKey: ['workspaces', { status: 'active' }], enabled: open })`,
    expected: INDEPENDENT,
  },
  {
    name: 'double-quoted factory key and property names (founder C)',
    source: `useQuery({ "queryFn": () => fetchWorkspaces({"status":"active"}), "queryKey": workspacesQueryKeys.list({"status":"active"}) })`,
    expected: INDEPENDENT,
  },
  {
    name: 'double-quoted literal active key (r13 m06 double-quoted)',
    source: `useQuery({ queryKey: ["workspaces", { status: "active" }], queryFn: () => checkFetchWorkspaces({ status: "active" }), enabled: open })`,
    expected: INDEPENDENT,
  },
  {
    name: 'private key overriding a shared spread (r13 m06 private-key)',
    source: `useQuery({ ...activeWorkspacesQueryOptions, queryKey: ['r13-private-workspaces-SearchModal'], enabled: open })`,
    expected: OWN_KEY,
  },
  {
    name: 'factory active key used as independent useQuery options without an own queryFn',
    source: `useQuery({ queryKey: workspacesQueryKeys.list({status:'active'}) })`,
    expected: INDEPENDENT,
  },
  {
    name: 'literal active key used as independent fetchQuery options without an own queryFn',
    source: `client.fetchQuery({ queryKey: ["workspaces", {status:"active"}] })`,
    expected: INDEPENDENT,
  },
  {
    name: 'computed-string queryFn method overriding a shared spread',
    source: `useQuery({ ...activeWorkspacesQueryOptions, ["queryFn"]() { return fetchWorkspaces({status:'active'}) } })`,
    expected: OWN_FN,
  },
] as const

const POSITIVE_CASES = [
  { name: 'intended shared spread plus enabled', source: `useQuery({ ...activeWorkspacesQueryOptions, enabled: open })` },
  { name: 'other workspace status', source: `useQuery({ queryKey: ['workspaces', {status:'archived'}], queryFn: () => fetchWorkspaces({status:'archived'}) })` },
  { name: 'active-key invalidation without a producer', source: `client.invalidateQueries({ queryKey: workspacesQueryKeys.list({status:'active'}) })` },
  { name: 'a harmless string and comment', source: `const example = "queryKey: ['workspaces', {status:'active'}], queryFn: () => x()"; // useQuery({ ...activeWorkspacesQueryOptions, queryFn: () => x() })` },
] as const

describe('active workspace list query producers', () => {
  it('positive control: the scanner sees a hand-built producer', () => {
    expect(violations(`useQuery({ queryKey: workspacesQueryKeys.list({ status: 'active' }), queryFn: () => fetchWorkspaces({ status: 'active' }) })`)).toStrictEqual([INDEPENDENT])
    expect(violations(`useQuery({ queryKey: ['workspaces', { status: 'active' }],\n  queryFn: () => x() })`)).toStrictEqual([INDEPENDENT])
  })
  it.each(NEGATIVE_CASES)('rejects $name', ({ source, expected }) => {
    expect(violations(source)).toStrictEqual([expected])
  })
  it.each(POSITIVE_CASES)('allows $name', ({ source }) => {
    expect(violations(source)).toStrictEqual([])
  })
  it('allows only the central module to own the active query definition', () => {
    const source = `const options = { queryKey: workspacesQueryKeys.list({status:'active'}), queryFn: () => fetchWorkspaces({status:'active'}) }`
    expect(violations(source, CENTRAL)).toStrictEqual([])
    expect(violations(source, join(ROOT, 'not-the-central-owner.ts'))).toStrictEqual([INDEPENDENT])
  })
  it('no source file builds that query with its own queryFn', () => {
    const files = sources(ROOT)
    expect(files.length, 'positive control: the scan reads the source tree').toBeGreaterThan(100)
    // The shared definition itself is the one legitimate producer.
    const offenders = files.map(file => file.slice(ROOT.length + 1))
      .filter(file => file !== join('lib', 'workspaceQueries.ts') && violations(readFileSync(join(ROOT, file), 'utf8'), join(ROOT, file)).length > 0)
    expect(offenders, 'use activeWorkspacesQueryOptions from @/lib/workspaceQueries').toStrictEqual([])
  })
})
