import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { syncBuiltinESMExports } from 'node:module';

// Derive the forbidden name only. NEVER stat/read/discover the live data root.
export const FORBIDDEN = path.join(os.homedir(), '.omnipus');
const inside = (root, value) => value === root || value.startsWith(root + path.sep);
export function absolute(value, name) {
  if (!value || !path.isAbsolute(value)) throw new Error(`${name} must be an explicit absolute path`);
  const clean = path.resolve(value);
  if (inside(FORBIDDEN, clean)) throw new Error(`${name} refuses the founder live-data root`);
  return clean;
}
export function safeOutput(value) {
  const clean = absolute(value, '--output');
  if (!inside('/Users/danielpiatkowski/AI-Agent-Workspace', clean) && process.platform === 'darwin') throw new Error('--output must be in the agent workspace');
  let current = path.parse(clean).root;
  for (const component of clean.slice(current.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, component);
    try { if (fs.lstatSync(current).isSymbolicLink()) throw new Error('--output refuses symlinked path'); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
  return clean;
}
export function optionalCopy(value, inputRoot, name, io = fs) {
  if (value === undefined) return null; // No discovery, not even an existence check.
  const source = absolute(value, name);
  const root = absolute(inputRoot, '--input-root');
  if (!inside(root, source) || root === source) throw new Error(`${name} must be a child of the explicit isolated --input-root`);
  // Refuse symlinks in each input component, including an alias into live data.
  let current = path.parse(source).root;
  for (const component of source.slice(current.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, component);
    const stat = io.lstatSync(current); // ENOENT is a refusal, never a fallback.
    if (stat.isSymbolicLink()) throw new Error(`${name} refuses symlinked input`);
  }
  const real = io.realpathSync(source);
  if (inside(FORBIDDEN, real) || !inside(io.realpathSync(root), real)) throw new Error(`${name} resolves outside its isolated input root`);
  if (!io.statSync(real).isDirectory()) throw new Error(`${name} must be a directory`);
  return real;
}
export function walkFiles(directory, io = fs) {
  const result = [];
  for (const entry of io.readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    if (entry.isSymbolicLink()) throw new Error('input tree contains a symlink; refused before copying');
    if (entry.isDirectory()) result.push(...walkFiles(file, io));
    else if (entry.isFile()) result.push(file);
    else throw new Error('input contains a non-regular file');
  }
  return result.sort();
}
export function installReadAudit() {
  const reads = new Set();
  const blocked = [];
  const undo = [];
  const inspect = value => {
    if (typeof value === 'number' || value === undefined) return;
    const raw = value instanceof URL ? fileURLToPath(value) : Buffer.isBuffer(value) ? value.toString() : value;
    if (typeof raw !== 'string') return;
    const file = path.resolve(raw);
    if (inside(FORBIDDEN, file)) { blocked.push(file); throw new Error('B25 FS AUDIT: forbidden live-data access'); }
    reads.add(file);
  };
  for (const name of ['openSync', 'readFileSync', 'readdirSync', 'statSync', 'lstatSync', 'realpathSync', 'createReadStream',
    'open', 'readFile', 'readdir', 'stat', 'lstat', 'realpath']) {
    const original = fs[name];
    fs[name] = function(file, ...args) { inspect(file); return original.call(this, file, ...args); };
    undo.push(() => { fs[name] = original; });
  }
  for (const name of ['open', 'readFile', 'readdir', 'stat', 'lstat', 'realpath']) {
    const original = fs.promises[name];
    fs.promises[name] = function(file, ...args) { inspect(file); return original.call(this, file, ...args); };
    undo.push(() => { fs.promises[name] = original; });
  }
  syncBuiltinESMExports();
  return { snapshot: () => ({ forbidden_root_opened: blocked.length !== 0, blocked_attempts: blocked.length,
    read_path_count: reads.size, read_paths: [...reads].sort(), scope: 'Node orchestration/browser API filesystem calls; gateway/fixture children get isolated HOME and OMNIPUS_HOME' }),
  restore: () => { for (const fn of undo.reverse()) fn(); syncBuiltinESMExports(); } };
}
