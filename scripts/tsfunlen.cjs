#!/usr/bin/env node
// tsfunlen: list every TS/TSX function-like node under src/, and every
// MJS/JS/CJS function-like node under scripts/ (and TS/TSX/MJS/JS/CJS under
// tests/), whose line span
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
// Line count is code lines only (founder decision 2026-11-07, option C).
// The span is still from the signature to the closing brace, but a line
// counts only when a non-comment token covers a non-whitespace character
// on it. Blank lines and comment-only lines do not. See
// docs/internal/architecture/draft-module-map.md, "Function length is code
// lines only".
//
// A function may carry a directive in the // comment block immediately
// above it (no blank line between that block and the function):
//
//   // budget:exception <one-line reason>
//   //budget:exception <one-line reason>
//
// Both spellings are accepted in TS, TSX, JS, MJS and CJS. An empty reason
// is reported so the gate can fail it. React components ignore the
// directive (the gate does; this scanner still reports it). The directive
// sits outside the span, so it is not itself a counted line.
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
// Output mirrors scripts/funlen:
// `lines<TAB>file:line<TAB>name<TAB>kind<TAB>exception`,
// split into "## Production" and "## Tests" sections, longest first. The
// exception column is `0` (none), `1` (directive present, reason empty), or
// `2` plus the reason. Lines starting with "#" are commentary; the gate only
// trusts lines that start with a digit followed by a tab.
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
// unioned. src/ scans TS/TSX only; scripts/ scans MJS/JS/CJS only; tests/
// scans TS/TSX AND MJS/JS/CJS (the size-budget ruling applies to test code
// too, and tests/e2e is TypeScript — a 317-line e2e describe slipped past this
// scanner while tests/ was JS-only). The three dirs never overlap, so no file
// is ever walked twice.
const SOURCE_ROOTS = [
  { dir: 'src', extensions: ['.ts', '.tsx'] },
  { dir: 'scripts', extensions: ['.mjs', '.js', '.cjs'] },
  { dir: 'tests', extensions: ['.ts', '.tsx', '.mjs', '.js', '.cjs'] },
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

// lfLines numbers physical lines the way scripts/funlen does: by LF only. The
// TypeScript parser's own line table also breaks at a bare CR, U+2028 and U+2029,
// which would give one source line two coordinates; this table is used for every
// line number below so the Go and TS counts agree. It answers the two calls the
// scanner makes on a SourceFile.
function lfLines(text) {
  const starts = [0];
  for (let i = text.indexOf('\n'); i >= 0; i = text.indexOf('\n', i + 1)) starts.push(i + 1);
  return {
    getLineAndCharacterOfPosition(pos) {
      let lo = 0;
      let hi = starts.length - 1;
      while (lo < hi) {
        const mid = (lo + hi + 1) >> 1;
        if (starts[mid] <= pos) lo = mid;
        else hi = mid - 1;
      }
      return { line: lo, character: pos - starts[lo] };
    },
    getPositionOfLineAndCharacter(line) {
      return starts[line];
    },
  };
}

// analyzeSource walks the parsed tree's leaf tokens (so template tails, regular
// expressions and JSX text are tokenised with the parser's own context, which a
// context-free scanner cannot do) and returns:
//   codeLines     0-based physical lines holding a non-whitespace character of a
//                 real token. Comments and blank lines are not tokens, so they do
//                 not count; a blank line inside a multi-line string stays blank.
//   firstOnLine   0-based line -> start offset of the first token touching it, or
//                 -1 when that token began on an earlier line (a string tail).
//   lineComments  0-based line -> {text, only} for every real `//` comment found
//                 between tokens; `only` is true when nothing but whitespace
//                 precedes it on its line. Text that merely looks like a comment
//                 inside a string or template never appears here.
function analyzeSource(ts, sf, text) {
  const lf = lfLines(text);
  const codeLines = new Set();
  const firstOnLine = new Map();
  const lineComments = new Map();
  let lastEnd = 0;
  const isJsDoc = (n) => n.kind >= ts.SyntaxKind.FirstJSDocNode && n.kind <= ts.SyntaxKind.LastJSDocNode;
  const visit = (n) => {
    if (isJsDoc(n)) return;
    const kids = n.getChildren(sf);
    if (kids.length > 0) {
      for (const k of kids) visit(k);
      return;
    }
    if (n.kind === ts.SyntaxKind.EndOfFileToken) return;
    const start = n.kind === ts.SyntaxKind.JsxText ? n.pos : n.getStart(sf);
    const end = n.end;
    if (!(end > start) || start < lastEnd) return;
    collectGapComments(lf, text, lineComments, lastEnd, start);
    markTokenLines(lf, text, codeLines, firstOnLine, start, end);
    lastEnd = end;
  };
  visit(sf);
  collectGapComments(lf, text, lineComments, lastEnd, text.length);
  return { codeLines, firstOnLine, lineComments, lf };
}

// collectGapComments parses the pure trivia text[from,to) between two tokens:
// whitespace, newlines, // comments and /* */ comments are the only things there.
function collectGapComments(lf, text, lineComments, from, to) {
  let i = from;
  while (i < to) {
    const c = text[i];
    if (c === '/' && text[i + 1] === '/') {
      let e = text.indexOf('\n', i);
      if (e < 0 || e > to) e = to;
      const line = lf.getLineAndCharacterOfPosition(i).line;
      const lineStart = lf.getPositionOfLineAndCharacter(line, 0);
      lineComments.set(line, {
        text: trimBlank(text.slice(i, e)),
        only: !hasNonSpace(text, lineStart, i),
      });
      i = e;
    } else if (c === '/' && text[i + 1] === '*') {
      const e = text.indexOf('*/', i + 2);
      i = e < 0 || e + 2 > to ? to : e + 2;
    } else if (c === '#' && text[i + 1] === '!' && i === 0) {
      const e = text.indexOf('\n', i);
      i = e < 0 || e > to ? to : e;
    } else {
      i++;
    }
  }
}

function markTokenLines(lf, text, lines, firstOnLine, start, end) {
  let i = start;
  while (i < end) {
    let lineEnd = text.indexOf('\n', i);
    if (lineEnd < 0 || lineEnd > end) lineEnd = end;
    if (hasNonSpace(text, i, lineEnd)) {
      const line = lf.getLineAndCharacterOfPosition(i).line;
      lines.add(line);
      if (!firstOnLine.has(line)) firstOnLine.set(line, i === start ? start : -1);
    }
    if (lineEnd >= end) break;
    i = lineEnd + 1;
  }
}

// Blank-line whitespace, one explicit set shared with scripts/funlen/main.go (keep
// the two in step; scripts/check-function-budget-selfcheck.sh runs every member
// through both). It is exactly the Unicode White_Space property:
//   U+0009-U+000D, U+0020, U+0085, U+00A0, U+1680, U+2000-U+200A,
//   U+2028, U+2029, U+202F, U+205F, U+3000
// U+FEFF (byte order mark), U+200B (zero width space) and U+180E (Mongolian vowel
// separator) are not White_Space: they count as code. JavaScript's own \s is not
// used because it includes U+FEFF and omits U+0085.
const BLANK_CLASS = '\\t-\\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000';
const NON_SPACE = new RegExp('[^' + BLANK_CLASS + ']');
const BLANK_EDGES = new RegExp('^[' + BLANK_CLASS + ']+|[' + BLANK_CLASS + ']+$', 'g');
// trimBlank trims with the same set, so a reason made only of blank-set characters
// is empty in both scanners (JavaScript's own trim() keeps U+0085, Go's strips it).
function trimBlank(str) {
  return str.replace(BLANK_EDGES, '');
}
function hasNonSpace(text, start, end) {
  return NON_SPACE.test(text.slice(start, end));
}

// countCodeLines counts 1-based inclusive [startLine, endLine] against a
// 0-based set from analyzeSource.
function countCodeLines(codeLines, startLine, endLine) {
  let n = 0;
  for (let l = startLine; l <= endLine; l++) {
    if (codeLines.has(l - 1)) n++;
  }
  return n;
}

const NONE = { kind: 0, reason: '' };
const BUDGET_DIRECTIVE = new RegExp('^//[' + BLANK_CLASS + ']*budget:exception(?:[' + BLANK_CLASS + '](.*))?$', 's');

// exceptionAbove returns {kind: 0 none, 1 empty reason, 2 reason, reason}.
// The directive must be a real // comment (analyzeSource only records those) in
// the comment-only block that ends on the line immediately above the function's
// start line (1-based), and the function (or an enclosing node that starts the
// same statement) must be the first token on its own line, so a marker can never
// reach a function through a string tail or another statement. A blank or
// non-comment line ends the block.
function exceptionAbove(ts, info, claimed, fn, sf, startLine) {
  for (let l = startLine - 2; l >= 0; l--) {
    const c = info.lineComments.get(l);
    if (!c || !c.only) break;
    const m = BUDGET_DIRECTIVE.exec(c.text);
    if (!m) continue;
    if (!firstTokenBelongsTo(ts, info, fn, sf, startLine - 1)) return NONE;
    // One marker, one function: the first (outermost) function that attaches on
    // this line takes it; a function nested on the same line gets nothing.
    if (claimed.has(startLine)) return NONE;
    claimed.add(startLine);
    const reason = trimBlank(m[1] || '');
    return reason === '' ? { kind: 1, reason: '' } : { kind: 2, reason };
  }
  return NONE;
}

// firstTokenBelongsTo: the first token on line0 starts the function itself or an
// enclosing node (e.g. `describe('x', () => {` starts the call that holds the
// arrow). Which of several functions on that line gets the marker is decided by
// the claim in exceptionAbove: the outermost one, so a nested helper never does.
function firstTokenBelongsTo(ts, info, fn, sf, line0) {
  const first = info.firstOnLine.get(line0);
  if (first === undefined || first < 0) return false;
  for (let n = fn; n && n.kind !== ts.SyntaxKind.SourceFile; n = n.parent) {
    if (n.getStart(sf) === first) return true;
  }
  return false;
}

function formatException(exc) {
  if (exc.kind === 0) return '0';
  if (exc.kind === 1) return '1';
  return '2' + String(exc.reason).replace(/[\t\r\n]/g, ' ');
}

// scanFile parses one TS/TSX/JS file and returns every counted function-like
// node whose code-line count exceeds `limit` (or that carries an exception
// directive), plus the total count seen.
function scanFile(ts, file, limit) {
  const isTsx = file.abs.endsWith('.tsx');
  const src = fs.readFileSync(file.abs, 'utf8');
  const sf = ts.createSourceFile(file.abs, src, ts.ScriptTarget.Latest, true);
  const info = analyzeSource(ts, sf, src);
  const claimed = new Set();
  const rows = [];
  let total = 0;
  const visit = (n) => {
    if (isFunctionLikeNode(ts, n) && n.body) {
      total++;
      const start = info.lf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1;
      const end = info.lf.getLineAndCharacterOfPosition(n.getEnd()).line + 1;
      const lines = countCodeLines(info.codeLines, start, end);
      const exc = exceptionAbove(ts, info, claimed, n, sf, start);
      if (lines > limit || exc.kind !== 0) {
        const { name, target } = resolveName(ts, sf, n);
        const kind = classifyKind(ts, sf, isTsx, n, target);
        rows.push({ file: file.rel, name, kind, start, lines, exc });
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
  console.log('lines\tfile:line\tname\tkind\texception');
  for (const r of rows) {
    console.log(`${r.lines}\t${r.file}:${r.start}\t${r.name}\t${r.kind}\t${formatException(r.exc)}`);
  }
}

function printReport(rows, totalProd, totalTest, limit) {
  rows.sort((a, b) => b.lines - a.lines);
  const prod = rows.filter((r) => !r.test);
  const test = rows.filter((r) => r.test);
  console.log(`# TS/TSX/JS functions over ${limit} code lines (blank and comment-only lines excluded)`);
  console.log(`# scanned: ${totalProd} production funcs, ${totalTest} test funcs`);
  console.log(`# reported: ${prod.length} production, ${test.length} test (over the limit, or carrying an exception directive)\n`);
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
