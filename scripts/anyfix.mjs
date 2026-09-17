#!/usr/bin/env node
// anyfix — one-round `any` → `unknown` migrator for lane fix/ts-no-any.
//
// Founder decision: `@typescript-eslint/no-explicit-any` is an ERROR, and the
// ~115 sites get refactored in ONE round — no pin list, no warning phase.
//
// The compiler decides what is safe: `unknown` forces narrowing at every use
// site, so a swap that breaks the build is a site that needs a real type, not
// a cleverer escape. This helper automates exactly that decision:
//
//   1. Find every no-explicit-any site via `npx eslint src --format json`.
//   2. Per file (see strategy note below), swap `any` → `unknown` and run
//      `npx tsc -b --noEmit`. Keep the file's swaps only if typecheck passes;
//      otherwise binary-search the file's sites to isolate the rejects.
//   3. Print SWAPPED (done) and NEEDS REAL TYPING (compiler rejected
//      `unknown`), with file:line:col per site.
//   4. `--dry-run` performs the entire decision loop with transient on-disk
//      edits and restores every file byte-for-byte at the end (verified).
//
// Batching strategy — per-file sequential with in-file bisection, NOT one
// typecheck per site and NOT one global batch. Rationale:
//   - Per site (~115 tsc runs): needlessly slow.
//   - Global batch: a swap on a declaration in file X breaks the USE site in
//     file Y, and tsc reports Y — so error-file attribution misfires across
//     files and bisection logic gets subtle.
//   - Per file: each tsc run differs from a known-green state by exactly one
//     undecided set of swaps within one file, so a red run attributes
//     airtight to that set; within the file, halving isolates individual
//     rejects. `tsc -b` is incremental, so each check recompiles only the
//     touched project — cheap.
//
// Safety properties:
//   - Swaps always apply to the ORIGINAL file content: ESLint's (line, col)
//     positions are captured once and never drift.
//   - Every swap verifies the token at the reported position is a standalone
//     `any` (word-boundary checked) before writing; anything else is routed
//     to the manual list untouched.
//   - Sync fs only, so `--dry-run` restore cannot be raced by the event loop.
//
// Exits 0 if the loop completed (regardless of how many sites need manual
// typing — that split is this tool's product); non-zero on tool failure or a
// non-green final verification (applied mode only).

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const DRY_RUN = process.argv.includes('--dry-run');
const ROOT = path.resolve(import.meta.dirname, '..');

/** File paths this helper must never rewrite (generated code). */
const DO_NOT_TOUCH = ['src/lib/api/generated/', 'src/routeTree.gen.ts'];

/** Run a command, returning { code, stdout, stderr } — never throws. */
function run(cmd, args) {
  try {
    const stdout = execFileSync(cmd, args, {
      cwd: ROOT,
      encoding: 'utf8',
      maxBuffer: 256 * 1024 * 1024,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    return { code: 0, stdout, stderr: '' };
  } catch (err) {
    return {
      code: err.status ?? 1,
      stdout: err.stdout ?? '',
      stderr: err.stderr ?? '',
    };
  }
}

/**
 * @typedef {{ file: string, line: number, col: number }} Site
 */

/** @returns {Site[]} every no-explicit-any finding under src/ */
function findSites() {
  const res = run('npx', ['eslint', 'src', '--format', 'json']);
  if (!res.stdout.trim().startsWith('[')) {
    console.error(`eslint failed (exit ${res.code}):\n${res.stderr || res.stdout}`);
    process.exit(1);
  }
  /** @type {{ filePath: string, messages: { ruleId: string | null, line: number, column: number }[] }[]} */
  const results = JSON.parse(res.stdout);
  const sites = [];
  for (const result of results) {
    const rel = path.relative(ROOT, result.filePath);
    for (const msg of result.messages) {
      if (msg.ruleId === '@typescript-eslint/no-explicit-any') {
        sites.push({ file: rel, line: msg.line, col: msg.column });
      }
    }
  }
  sites.sort((a, b) =>
    a.file < b.file ? -1 : a.file > b.file ? 1 : a.line - b.line || a.col - b.col,
  );
  return sites;
}

/** Original file contents, captured on first touch and reused for every
 *  rewrite so ESLint positions stay valid. key = repo-relative path. */
const originals = new Map();

function originalFor(file) {
  if (!originals.has(file)) {
    originals.set(file, fs.readFileSync(path.join(ROOT, file), 'utf8'));
  }
  return originals.get(file);
}

const isWordChar = (ch) => /[\w$]/.test(ch);

/**
 * Rewrite `file` on disk so that exactly the sites in `active` (a subset of
 * this file's site list) read `unknown` instead of `any`. Composed from the
 * ORIGINAL content, so call order never matters. Sites that do not sit on a
 * standalone `any` token are returned as `skipped` and left unswapped.
 * @param {string} file
 * @param {Site[]} active
 * @returns {{ applied: Site[], skipped: Site[] }}
 */
function applySwaps(file, active) {
  const orig = originalFor(file);
  const lines = orig.split('\n');
  /** @type {Record<string, Site[]>} 1-based line number → sites on that line */
  const byLine = {};
  for (const s of active) (byLine[s.line] ??= []).push(s);

  const skipped = [];
  for (const lineNo of Object.keys(byLine).map(Number)) {
    const sitesOnLine = byLine[lineNo].sort((a, b) => a.col - b.col);
    const text = lines[lineNo - 1];
    let rebuilt = '';
    let cursor = 0;
    for (const s of sitesOnLine) {
      const start = s.col - 1;
      const before = start > 0 ? text[start - 1] : '';
      const after = text[start + 3] ?? '';
      const standalone =
        text.slice(start, start + 3) === 'any' &&
        !isWordChar(before) &&
        before !== '.' &&
        !isWordChar(after);
      if (!standalone) {
        skipped.push(s);
        continue;
      }
      rebuilt += text.slice(cursor, start) + 'unknown';
      cursor = start + 3;
    }
    lines[lineNo - 1] = rebuilt + text.slice(cursor);
  }

  const applied = active.filter((s) => !skipped.some((k) => k === s));
  fs.writeFileSync(path.join(ROOT, file), lines.join('\n'));
  return { applied, skipped };
}

/** One `tsc -b --noEmit` oracle run. */
function typecheck() {
  const res = run('npx', ['tsc', '-b', '--noEmit']);
  return { green: res.code === 0, code: res.code };
}

const rel = (s) => `${s.file}:${s.line}:${s.col}`;

// ── Main loop ────────────────────────────────────────────────────────────────

const allSites = findSites();
const inScope = allSites.filter((s) => !DO_NOT_TOUCH.some((p) => s.file.startsWith(p)));
const outOfScope = allSites.filter((s) => !inScope.includes(s));

console.log(`sites found by eslint: ${allSites.length}`);
if (outOfScope.length > 0) {
  console.log(`generated / do-not-touch (left alone): ${outOfScope.length}`);
}
const files = [...new Set(inScope.map((s) => s.file))];
console.log(`files in scope: ${files.length}`);
console.log(`strategy: per-file swap, in-file bisection on tsc rejection`);

/** @type {Site[]} */ const swapped = [];
/** @type {Site[]} */ const manual = [];
const touched = new Set();
let tscRuns = 0;

try {
  for (const file of files) {
    const fileSites = inScope.filter((s) => s.file === file);
    const { applied, skipped } = applySwaps(file, fileSites);
    touched.add(file);
    if (skipped.length > 0) {
      manual.push(...skipped);
      console.log(`  ${file}: ${skipped.length} site(s) not on a standalone any token → manual`);
    }
    if (applied.length === 0) continue;

    /** This file's accepted swaps — the only state applySwaps is ever called
     *  with, so the on-disk file always equals original + `accepted`. */
    let accepted = [];
    const setAccepted = (sites) => {
      accepted = sites;
      applySwaps(file, sites);
    };

    const check = () => {
      tscRuns += 1;
      return typecheck().green;
    };

    setAccepted(applied);
    if (check()) {
      swapped.push(...applied);
      console.log(`  ${file}: swapped ${applied.length}/${applied.length}`);
      continue;
    }

    // Whole-file batch rejected — bisect `undecided` against the green
    // baseline (everything accepted so far, including this file's `accepted`).
    // Every check's delta versus a green state is exactly the half under
    // test, so a red run always indicts that half.
    const bisect = (undecided) => {
      if (undecided.length === 0) return;
      if (undecided.length === 1) {
        manual.push(undecided[0]);
        console.log(`  ${rel(undecided[0])}: needs real typing`);
        return; // stays unswapped: `accepted` already excludes it
      }
      const half = undecided.slice(0, Math.floor(undecided.length / 2));
      const rest = undecided.slice(half.length);
      setAccepted([...accepted, ...half]);
      if (check()) {
        swapped.push(...half);
        accepted = [...accepted, ...half];
        bisect(rest);
      } else {
        setAccepted(accepted); // drop `half` back to unswapped and retry it
        bisect(half);
        bisect(rest);
      }
    };
    console.log(`  ${file}: batch rejected — bisecting ${applied.length} site(s)`);
    bisect(applied);
    if (accepted.length > 0) {
      setAccepted(accepted); // final on-disk state = original + accepted
    }
  }
} finally {
  if (DRY_RUN) {
    for (const file of touched) {
      fs.writeFileSync(path.join(ROOT, file), originalFor(file));
    }
    const restoredOk = [...touched].every(
      (f) => fs.readFileSync(path.join(ROOT, f), 'utf8') === originalFor(f),
    );
    console.log(`dry-run restore verified byte-for-byte: ${restoredOk ? 'yes' : 'NO — FILES LEFT MODIFIED'}`);
    if (!restoredOk) process.exitCode = 1;
  }
}

console.log('\n── summary ──');
console.log(`swapped (any → unknown): ${swapped.length}`);
console.log(`needs real typing:       ${manual.length}`);
for (const s of manual) console.log(`  ${rel(s)}`);
console.log(`tsc runs: ${tscRuns}`);
console.log(`mode: ${DRY_RUN ? 'dry-run (tree restored)' : 'applied'}`);

if (!DRY_RUN) {
  const finalTsc = typecheck();
  const lintJson = run('npx', ['eslint', 'src', '--format', 'json']);
  const remaining =
    lintJson.stdout.trim().startsWith('[')
      ? JSON.parse(lintJson.stdout)
          .flatMap((r) => r.messages)
          .filter((m) => m.ruleId === '@typescript-eslint/no-explicit-any').length
      : -1;
  console.log(`final tsc -b --noEmit exit: ${finalTsc.code}`);
  console.log(`final eslint exit: ${lintJson.code}; remaining no-explicit-any sites: ${remaining}`);
  if (remaining !== 0 || !finalTsc.green) process.exitCode = 1;
}
