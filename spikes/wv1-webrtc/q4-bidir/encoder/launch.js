// Chrome launcher for the spike. Fresh user-data-dir per launch, under the session scratchpad.
'use strict';
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');

const CHROME = '/tmp/omnipus-preview-home/browser/chromium/151.0.7922.34/chrome-linux64/chrome';
const SCRATCH = '/tmp/claude-1000/-home-dev-omnipus3/941f24ff-77e5-46c8-9352-697afc13d4f1/scratchpad';
const PORT = 9333; // CDP port, localhost only

// Flag parity with omnipus managedExecAllocatorOpts (pkg/tools/browser/exec_resolver.go)
// + spike-specific capture/audio flags passed by each test.
function baseFlags(userDataDir) {
  return [
    `--user-data-dir=${userDataDir}`,
    '--headless=new',
    '--no-sandbox',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-crash-reporter',
    '--disable-breakpad',
    '--disable-dev-shm-usage',
    '--enable-automation=false',
    '--disable-blink-features=AutomationControlled',
    '--lang=en-US',
    '--window-size=1280,720',
    '--autoplay-policy=no-user-gesture-required',
  ];
}

let seq = 0;
function launch(extraFlags = [], tag = 'run') {
  const dir = path.join(SCRATCH, `q2-profile-${tag}-${Date.now()}-${seq++}`);
  fs.mkdirSync(dir, { recursive: true });
  const args = [...baseFlags(dir), `--remote-debugging-port=${PORT}`, ...extraFlags, 'about:blank'];
  const logPath = path.join(SCRATCH, `q2-chrome-${tag}.log`);
  const log = fs.openSync(logPath, 'w');
  const child = spawn(CHROME, args, { stdio: ['ignore', log, log] });
  console.log(`[launch] chrome pid=${child.pid} tag=${tag} log=${logPath}`);
  console.log(`[launch] flags: ${extraFlags.join(' ') || '(base only)'}`);
  return { child, dir, logPath, port: PORT };
}

// Pipe-mode launch for Extensions.loadUnpacked (requires --remote-debugging-pipe
// + --enable-unsafe-extension-debugging; the Extensions domain refuses port mode).
function launchPipe(extraFlags = [], tag = 'run') {
  const dir = path.join(SCRATCH, `q2-profile-${tag}-${Date.now()}-${seq++}`);
  fs.mkdirSync(dir, { recursive: true });
  const args = [
    ...baseFlags(dir),
    '--remote-debugging-pipe',
    '--enable-unsafe-extension-debugging',
    ...extraFlags,
    'about:blank',
  ];
  const logPath = path.join(SCRATCH, `q2-chrome-${tag}.log`);
  const log = fs.openSync(logPath, 'w');
  const child = spawn(CHROME, args, { stdio: ['ignore', log, log, 'pipe', 'pipe'] });
  console.log(`[launch-pipe] chrome pid=${child.pid} tag=${tag} log=${logPath}`);
  console.log(`[launch-pipe] flags: ${extraFlags.join(' ') || '(base only)'}`);
  return { child, dir, logPath };
}

async function kill(child) {
  if (!child || child.exitCode !== null) return;
  child.kill('SIGTERM');
  await new Promise((r) => {
    const t = setTimeout(() => { try { child.kill('SIGKILL'); } catch {} r(); }, 3000);
    child.once('exit', () => { clearTimeout(t); r(); });
  });
}

module.exports = { launch, launchPipe, kill, PORT, CHROME };
