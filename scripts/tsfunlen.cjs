#!/usr/bin/env node
// tsfunlen: list every TS/TSX function-like node under src/, and every
// MJS/JS/CJS function-like node under scripts/ and tests/, whose line span
// exceeds a threshold, for the function-size budget gate
// (scripts/check-function-budget.sh). Companion to scripts/funlen (Go).
//
// The scripts/ and tests/ roots (not docs/ or spikes/) mirror
// check-file-budget.sh's own scope decision for hand-written .mjs/.js/.cjs
// tooling — see that script's header. The TypeScript compiler API parses
// plain JS/MJS/CJS the same way it parses TS: ts.createSourceFile infers
// ts.ScriptKind.JS from the .mjs/.cjs/.js extension with no extra flag, so
// the same walk, the same function-like node kinds, and the same anonymous-
// callback/component rules below apply to both — a .tsx-only check keeps
// every .mjs/.js/.cjs function classified as "function", never "component",
// which is correct: none of them render JSX.
//
// Counted node kinds: FunctionDeclaration, MethodDeclaration,
// FunctionExpression, ArrowFunction, ConstructorDeclaration, GetAccessor,
// SetAccessor.
//
// Anonymous callbacks count (docs/internal/architecture/draft-module-map.md,
// "How we enforce it"): the body passed to useEffect/create/produce/
// withBucket and the like is a function and is reported as
// "(arg of useEffect)" — without this the largest functions in the repo
// (src/store/chat.ts's store `create` call, `handleFrame`) are invisible.
//
// Component detection: a function is a "component" when (a) its file ends
// in .tsx, (b) its own name, or the name of the variable it is assigned to
// — looking through memo(...)/forwardRef(...)/React.memo/React.forwardRef —
// starts with an uppercase letter, and (c) its body contains a `return`
// whose expression is a JSX element, self-closing element, or fragment, or
// a parenthesised/conditional/logical expression containing one. Anything
// else — including use* hooks and anonymous callbacks inside a component —
// is a plain "function". This is a stricter test than the report heuristic
// in size-budget-grandfather-2026-09-15.md (name-starts-uppercase alone),
// so a handful of entries there are expected to move between the two kinds.
//
// Output mirrors scripts/funlen: `lines<TAB>file:line<TAB>name<TAB>kind`,
// split into "## Production" and "## Tests" sections, longest first. Lines
// starting with "#" are commentary; the gate only trusts lines that start
// with a digit followed by a tab.
//
// Usage: node scripts/tsfunlen.cjs [--root .] [--limit 120] [--ts-module <path>]
//
// TypeScript resolution: plain `require('typescript')` walks up from this
// file's own directory (scripts/node_modules, then the repo root's
// node_modules) — the repo root's copy is what `npm ci` installs, so the
// default works unmodified in CI. `--ts-module <path>` forces an explicit
// module path for a checkout with no node_modules of its own (e.g. this
// worktree); NODE_PATH=<dir> also works, since plain `require()` honors it.

'use strict';

const fs = require('fs');
const path = require('path');

const SKIP_DIRS = new Set(['node_modules', 'dist', '.gitnexus']);
const SKIP_PATH_PREFIXES = ['lib/api/generated'];
const REACT_WRAP_RE = /^(React\.)?(memo|forwardRef)$/;

function parseArgs(argv) {
  const opts = { root: '.', limit: 120, tsModule: null };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--root') opts.root = argv[++i];
    else if (a === '--limit') opts.limit = Number(argv[++i]);
    else if (a === '--ts-module') opts.tsModule = argv[++i];
    else throw new Error(`tsfunlen: unknown argument: ${a}`);
  }
  return opts;
}

function loadTypeScript(tsModule) {
  if (tsModule) return require(path.resolve(tsModule));
  return require('typescript'); // resolves via node_modules walk-up, or NODE_PATH
}

function isSkippedFile(rel) {
  if (rel.endsWith('.d.ts')) return true;
  const norm = rel.split(path.sep).join('/');
  return SKIP_PATH_PREFIXES.some((p) => norm === p || norm.startsWith(p + '/'));
}

function isTestPath(rel) {
  const norm = rel.split(path.sep).join('/');
  return (
    /\.(test|spec)\.(ts|tsx|mjs|js|cjs)$/.test(norm) ||
    norm.includes('/__tests__/') ||
    norm.startsWith('e2e/') ||
    norm.startsWith('tests/')
  );
}

// SOURCE_ROOTS: each { dir, extensions } pair is walked independently and
// unioned. src/ scans TS/TSX only; scripts/ and tests/ scan MJS/JS/CJS
// only (never TS/TSX there — tests/e2e's TS/TSX, if any, stays out of
// scope). The three dirs never overlap, so no file is ever walked twice.
const SOURCE_ROOTS = [
  { dir: 'src', extensions: ['.ts', '.tsx'] },
  { dir: 'scripts', extensions: ['.mjs', '.js', '.cjs'] },
  { dir: 'tests', extensions: ['.mjs', '.js', '.cjs'] },
];

// walkSourceFiles collects every file matching SOURCE_ROOTS under <root>,
// skipping SKIP_DIRS and isSkippedFile() paths. A missing root dir (a
// fixture tree with no TS/JS at all) is not an error — it just yields no
// files from that root.
function walkSourceFiles(root) {
  const out = [];
  for (const { dir, extensions } of SOURCE_ROOTS) {
    const start = path.join(root, dir);
    if (!fs.existsSync(start)) continue;
    const stack = [start];
    while (stack.length > 0) {
      const cur = stack.pop();
      for (const entry of fs.readdirSync(cur, { withFileTypes: true })) {
        const p = path.join(cur, entry.name);
        if (entry.isDirectory()) {
          if (!SKIP_DIRS.has(entry.name)) stack.push(p);
          continue;
        }
        if (!extensions.includes(path.extname(entry.name))) continue;
        const rel = path.relative(root, p);
        if (!isSkippedFile(rel)) out.push({ abs: p, rel });
      }
    }
  }
  return out;
}

// isFunctionLikeNode reports whether n is one of the node kinds this
// scanner counts as its own function span (used both as the top-level
// filter and as the "stop descending" boundary when hunting for a JSX
// return inside an outer function).
function isFunctionLikeNode(ts, n) {
  return (
    ts.isFunctionDeclaration(n) ||
    ts.isMethodDeclaration(n) ||
    ts.isFunctionExpression(n) ||
    ts.isArrowFunction(n) ||
    ts.isConstructorDeclaration(n) ||
    ts.isGetAccessor(n) ||
    ts.isSetAccessor(n)
  );
}

// isJsxLike reports whether expr is, or wraps (via parens/conditional/
// logical operators), a JSX element, self-closing element, or fragment.
function isJsxLike(ts, expr) {
  if (!expr) return false;
  if (ts.isParenthesizedExpression(expr)) return isJsxLike(ts, expr.expression);
  if (ts.isJsxElement(expr) || ts.isJsxSelfClosingElement(expr) || ts.isJsxFragment(expr)) return true;
  if (ts.isConditionalExpression(expr)) {
    return isJsxLike(ts, expr.whenTrue) || isJsxLike(ts, expr.whenFalse);
  }
  if (ts.isBinaryExpression(expr)) {
    const op = expr.operatorToken.kind;
    if (op === ts.SyntaxKind.AmpersandAmpersandToken || op === ts.SyntaxKind.BarBarToken || op === ts.SyntaxKind.QuestionQuestionToken) {
      return isJsxLike(ts, expr.left) || isJsxLike(ts, expr.right);
    }
  }
  return false;
}

// hasJsxReturn reports whether fn's own body — not any nested function's
// body — returns something isJsxLike. A concise arrow body (no braces) is
// itself the returned expression.
function hasJsxReturn(ts, fn) {
  if (!fn.body) return false;
  if (!ts.isBlock(fn.body)) return isJsxLike(ts, fn.body);
  let found = false;
  const visit = (n) => {
    if (found || (n !== fn.body && isFunctionLikeNode(ts, n))) return;
    if (ts.isReturnStatement(n) && isJsxLike(ts, n.expression)) {
      found = true;
      return;
    }
    ts.forEachChild(n, visit);
  };
  visit(fn.body);
  return found;
}

// resolveName finds the reported name for a function-like node: its own
// name; the react-wrap-call it's passed to unwrapped to the variable it's
// assigned to; the variable/property it's assigned to directly; or
// "(arg of <callee>)" / "(anonymous)" as a last resort. `target` is the
// name used for uppercase/component classification — null when there is
// none (an anonymous callback can never be a component).
function resolveName(ts, sf, fn) {
  if (fn.name) {
    const n = fn.name.getText(sf);
    return { name: n, target: n };
  }
  const p = fn.parent;
  if (p && ts.isVariableDeclaration(p)) {
    const n = p.name.getText(sf);
    return { name: n, target: n };
  }
  if (p && ts.isPropertyAssignment(p)) {
    const n = p.name.getText(sf);
    return { name: n, target: n };
  }
  if (p && ts.isCallExpression(p)) return resolveCallArgName(ts, sf, p);
  return { name: '(anonymous)', target: null };
}

function resolveCallArgName(ts, sf, call) {
  const callee = call.expression.getText(sf).trim();
  if (REACT_WRAP_RE.test(callee)) {
    const outer = call.parent;
    if (outer && ts.isVariableDeclaration(outer)) {
      const n = outer.name.getText(sf);
      return { name: n, target: n };
    }
    if (outer && ts.isPropertyAssignment(outer)) {
      const n = outer.name.getText(sf);
      return { name: n, target: n };
    }
  }
  return { name: `(arg of ${callee.slice(0, 40)})`, target: null };
}

function classifyKind(ts, sf, isTsx, fn, target) {
  if (!isTsx || !target || !/^[A-Z]/.test(target)) return 'function';
  return hasJsxReturn(ts, fn) ? 'component' : 'function';
}

// scanFile parses one TS/TSX file and returns every counted function-like
// node whose line span exceeds `limit`, plus the total count seen.
function scanFile(ts, file, limit) {
  const isTsx = file.abs.endsWith('.tsx');
  const src = fs.readFileSync(file.abs, 'utf8');
  const sf = ts.createSourceFile(file.abs, src, ts.ScriptTarget.Latest, true);
  const rows = [];
  let total = 0;
  const visit = (n) => {
    if (isFunctionLikeNode(ts, n) && n.body) {
      total++;
      const start = sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1;
      const end = sf.getLineAndCharacterOfPosition(n.getEnd()).line + 1;
      const lines = end - start + 1;
      if (lines > limit) {
        const { name, target } = resolveName(ts, sf, n);
        const kind = classifyKind(ts, sf, isTsx, n, target);
        rows.push({ file: file.rel, name, kind, start, lines });
      }
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return { rows, total };
}

function scanRepo(ts, root, limit) {
  const files = walkSourceFiles(root);
  const rows = [];
  let totalProd = 0;
  let totalTest = 0;
  for (const file of files) {
    const isTest = isTestPath(file.rel);
    const { rows: fileRows, total } = scanFile(ts, file, limit);
    if (isTest) totalTest += total;
    else totalProd += total;
    for (const r of fileRows) rows.push({ ...r, test: isTest });
  }
  return { rows, totalProd, totalTest };
}

function printSection(title, rows) {
  console.log(`## ${title}`);
  console.log('lines\tfile:line\tname\tkind');
  for (const r of rows) console.log(`${r.lines}\t${r.file}:${r.start}\t${r.name}\t${r.kind}`);
}

function printReport(rows, totalProd, totalTest, limit) {
  rows.sort((a, b) => b.lines - a.lines);
  const prod = rows.filter((r) => !r.test);
  const test = rows.filter((r) => r.test);
  console.log(`# TS/TSX functions longer than ${limit} lines under src/ (span incl. signature and closing brace)`);
  console.log(`# scanned: ${totalProd} production funcs, ${totalTest} test funcs`);
  console.log(`# over limit: ${prod.length} production, ${test.length} test\n`);
  printSection('Production', prod);
  console.log('');
  printSection('Tests', test);
}

function main() {
  const opts = parseArgs(process.argv.slice(2));
  const ts = loadTypeScript(opts.tsModule);
  const { rows, totalProd, totalTest } = scanRepo(ts, opts.root, opts.limit);
  printReport(rows, totalProd, totalTest, opts.limit);
}

if (require.main === module) main();

module.exports = {
  parseArgs,
  isSkippedFile,
  isTestPath,
  walkSourceFiles,
  isJsxLike,
  hasJsxReturn,
  resolveName,
  classifyKind,
  scanFile,
  scanRepo,
};
