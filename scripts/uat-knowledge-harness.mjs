#!/usr/bin/env node
// Omnipus — UAT harness: drives real agents over the gateway API as testers.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Implements the setup and turn loop that
// docs/internal/uat/knowledge-tools-uat-plan.md depends on. Node built-ins
// only (global WebSocket, Node >= 22) — this repo forbids new runtime deps.
//
// WHAT THIS FILE IS CAREFUL ABOUT, because the plan's Suite Z says a broken
// run must not look green:
//   * `done.stats.turn_failed` is checked on EVERY turn. Without it a failed
//     turn is byte-identical to a successful one at this layer.
//   * A `tool_approval_required` frame is a HARNESS ERROR by default, not
//     something to auto-approve silently: it means a policy resolved to `ask`
//     and the run would otherwise hang. Auto-approval is opt-in.
//   * An empty knowledge scope is the plan's most dangerous failure — the
//     agent is told in prose that no knowledge base exists, and every scenario
//     then passes by finding nothing. `preflight` asserts a real read.

import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const DEFAULT_BASE = 'http://127.0.0.1:5000';

// --------------------------------------------------------------------------
// low-level REST
// --------------------------------------------------------------------------

export function cliToken(home) {
  // The gateway mints this on every `omnipus start`; it is a machine
  // credential decoupled from any human account, which is why it is the right
  // one to script with rather than a login token.
  return readFileSync(join(home, 'cli.token'), 'utf8').trim();
}

export async function api(base, token, method, path, body) {
  const res = await fetch(base + path, {
    method,
    headers: {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(body ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json;
  try { json = text ? JSON.parse(text) : null; } catch { json = null; }
  // The status is returned rather than thrown on: several call sites treat a
  // specific non-2xx as success (409 "already onboarded" above all), and a
  // helper that throws would force them to catch and re-inspect anyway.
  return { status: res.status, ok: res.ok, json, text };
}

// --------------------------------------------------------------------------
// setup
// --------------------------------------------------------------------------

export async function ensureOnboarded(base, { apiKey, model, username, password }) {
  const state = await api(base, null, 'GET', '/api/v1/state');
  if (state.json?.onboarding_complete) return { already: true };

  const res = await api(base, null, 'POST', '/api/v1/onboarding/complete', {
    provider: { id: 'openrouter', api_key: apiKey, model },
    admin: { username, password },
  });
  // 409 means someone else got there first. That is success for our purposes,
  // but ONLY when the body says so — treating every 409 as fine would hide a
  // genuinely different conflict.
  if (res.status === 409 && /already/i.test(res.text)) return { already: true };
  if (!res.ok) {
    throw new Error(
      `onboarding failed (${res.status}): ${res.text}\n` +
      'Note: this endpoint performs a REAL, billable provider probe. A key the ' +
      'provider rejects returns 400 and persists nothing, so a placeholder will ' +
      'not work here. It is also rate limited to 3/IP/min.');
  }
  return { already: false, body: res.json };
}

export const KNOWLEDGE_TOOLS = [
  'knowledge_describe', 'knowledge_find', 'knowledge_read',
  'knowledge_edit', 'knowledge_restructure', 'knowledge_configure',
];

export async function createTester(base, token, { name, soul, model }) {
  // type: Main — Subagent/subagent_3p are delegation-only workers and are
  // structurally excluded from chat, so neither can be driven this way.
  //
  // The policy map is deliberately sparse: POST /agents seeds a complete
  // deny-everything map and merges these on top, so coverage is satisfied.
  // `allow` and never `ask` — an `ask` policy blocks the turn on an approval
  // frame, which for an unattended run is a hang.
  const policies = Object.fromEntries(KNOWLEDGE_TOOLS.map((t) => [t, 'allow']));
  const res = await api(base, token, 'POST', '/api/v1/agents', {
    type: 'Main',
    name,
    soul,
    model,
    provider: 'openrouter',
    tools_cfg: { builtin: { policies } },
  });
  if (!res.ok) throw new Error(`createTester failed (${res.status}): ${res.text}`);
  return res.json;
}

export async function createWorkspace(base, token, { name, agentID }) {
  // core_team membership is not cosmetic: the knowledge tools resolve their
  // scope from the turn's workspace, and an agent on no workspace gets an
  // EMPTY scope with no error at all.
  const res = await api(base, token, 'POST', '/api/v1/workspaces', {
    name, core_team: [agentID],
  });
  if (!res.ok) throw new Error(`createWorkspace failed (${res.status}): ${res.text}`);
  return res.json;
}

// --------------------------------------------------------------------------
// the turn loop
// --------------------------------------------------------------------------

export class Turn {
  constructor({ base, token, agentID, workspaceID, autoApprove = false }) {
    Object.assign(this, { base, token, agentID, workspaceID, autoApprove });
    this.sessionID = null;
  }

  /**
   * Sends one message and resolves when the turn's single `done` frame lands.
   * Returns everything a scenario needs to grade itself WITHOUT trusting the
   * agent's own narration: the text, the tool calls, the wall time, the raw
   * frames, and whether the engine considered the turn failed.
   */
  async ask(content, { timeoutMs = 180_000 } = {}) {
    const url = this.base.replace(/^http/, 'ws') + '/api/v1/chat/ws';
    const ws = new WebSocket(url);
    const frames = [];
    let text = '';
    const toolCalls = [];
    let authed = false;
    const started = Date.now();

    return await new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        try { ws.close(); } catch { /* already closing */ }
        reject(new Error(`turn timed out after ${timeoutMs}ms (frames: ${frames.length})`));
      }, timeoutMs);

      const finish = (fn) => { clearTimeout(timer); try { ws.close(); } catch { /* noop */ } fn(); };

      ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token: this.token }));

      ws.onmessage = (ev) => {
        let f;
        try { f = JSON.parse(ev.data); } catch { return; }
        frames.push(f);

        switch (f.type) {
          case 'session_started':
            // Also the proof that auth succeeded.
            authed = true;
            this.sessionID = f.session_id ?? this.sessionID;
            break;
          case 'token':
            authed = true;
            text += f.content ?? '';
            break;
          case 'tool_call_start':
            // FIELD NAMES ARE THE CONTRACT'S, NOT GUESSES. An earlier draft of
            // this file read `f.name` / `f.args`; the schema says `tool` and
            // `params` (contracts/components/schemas/ToolCallStartFrame.yaml).
            // Reading the wrong key yields `undefined` silently, which would
            // have made Z-03's "did it call knowledge_read?" check answer NO
            // on every run — a false failure indistinguishable from a real one.
            toolCalls.push({ tool: f.tool, params: f.params, callID: f.call_id });
            break;
          case 'tool_call_result': {
            const call = toolCalls.find((c) => c.callID === f.call_id);
            // Matched by call_id rather than by position: tool calls can
            // interleave (and nest, via parent_call_id), so "the last one" is
            // not reliably the one this result belongs to.
            if (call) Object.assign(call, { result: f.result, status: f.status, error: f.error, ms: f.duration_ms });
            break;
          }
          case 'tool_approval_required': {
            // A policy resolved to `ask`. Left unhandled this hangs the run,
            // so it is surfaced as an error unless the caller opted in.
            const id = f.approval_id;
            if (!this.autoApprove) {
              finish(() => reject(new Error(
                `tool_approval_required for "${f.tool_name}" — a policy resolved to ` +
                '`ask`, which hangs an unattended run. Seed the tool as `allow`, or pass ' +
                'autoApprove:true if the approval path is what you are testing.')));
              return;
            }
            // REST, deliberately: the legacy WS approval frame routes through a
            // different registry and stalls the turn for ~90s.
            api(this.base, this.token, 'POST', `/api/v1/tool-approvals/${id}`, { action: 'approve' })
              .catch(() => { /* the done/timeout path reports the consequence */ });
            break;
          }
          case 'error':
            if (!authed) {
              finish(() => reject(new Error(`auth rejected: ${f.message ?? JSON.stringify(f)}`)));
            }
            // A post-auth error does not close the socket; `done` still comes.
            break;
          case 'done':
            finish(() => resolve({
              text,
              toolCalls,
              frames,
              sessionID: this.sessionID,
              ms: Date.now() - started,
              // The plan requires this to be checked on every turn: set when
              // the engine used its error/limit fallback rather than a real
              // model response. Ignoring it makes a failed turn look identical
              // to a successful one.
              turnFailed: Boolean(f.stats?.turn_failed),
              stats: f.stats ?? null,
            }));
            break;
        }
      };

      ws.onerror = () => finish(() => reject(new Error('websocket error')));
      ws.onclose = () => {
        // Only meaningful if `done` never arrived; otherwise finish() already resolved.
        clearTimeout(timer);
        reject(new Error(`socket closed after ${frames.length} frame(s) without a done frame`));
      };

      const send = () => ws.send(JSON.stringify({
        type: 'message',
        content,
        agent_id: this.agentID,
        ...(this.sessionID ? { session_id: this.sessionID } : {}),
        metadata: { workspace_id: this.workspaceID },
      }));
      // The auth frame must land first; the server acks by streaming rather
      // than by a dedicated frame, so a short settle is simpler than racing.
      setTimeout(send, 150);
    });
  }
}

// --------------------------------------------------------------------------
// Suite Z
// --------------------------------------------------------------------------

export async function preflight(base, token, { agentID, workspaceID, model, knownNotePath }) {
  const checks = [];
  const record = (id, ok, detail) => checks.push({ id, ok, detail });

  const agent = await api(base, token, 'GET', `/api/v1/agents/${agentID}`);
  record('Z-01', agent.json?.model === model && agent.json?.provider === 'openrouter',
    `model=${agent.json?.model} provider=${agent.json?.provider} (want ${model}/openrouter)`);

  const tools = await api(base, token, 'GET', `/api/v1/agents/${agentID}/tools`);
  const list = tools.json?.tools ?? tools.json ?? [];
  const byName = new Map((Array.isArray(list) ? list : []).map((t) => [t.name, t]));
  const bad = KNOWLEDGE_TOOLS
    .map((n) => [n, byName.get(n)?.effective_policy])
    .filter(([, p]) => p !== 'allow');
  // effective_policy, never configured_policy: resolution is
  // most-restrictive-wins across global x agent, so a global ceiling can
  // silently overrule a per-agent allow.
  record('Z-02', bad.length === 0,
    bad.length ? `not allow: ${bad.map(([n, p]) => `${n}=${p ?? 'missing'}`).join(', ')}` : 'all six allow');

  const turn = new Turn({ base, token, agentID, workspaceID });
  let readOK = false; let readDetail = '';
  try {
    const r = await turn.ask(
      `Use knowledge_read to read exactly this note and reply with its first line only: ${knownNotePath}`);
    readDetail = r.turnFailed ? 'turn_failed' : r.text.slice(0, 160).replace(/\s+/g, ' ');
    readOK = !r.turnFailed && r.toolCalls.some((c) => c.tool === 'knowledge_read');
  } catch (e) { readDetail = String(e.message); }
  // The plan's most dangerous failure: an empty scope produces a polite "no
  // knowledge base is available" and every later suite passes by finding
  // nothing. Asserting a real read is what turns that into a caught error.
  record('Z-03', readOK, readDetail);

  return { ok: checks.every((c) => c.ok), checks };
}

// --------------------------------------------------------------------------
// CLI
// --------------------------------------------------------------------------

const HELP = `omnipus knowledge-tools UAT harness

  --home <dir>        OMNIPUS_HOME of the target gateway (for cli.token)
  --base <url>        gateway base URL (default ${DEFAULT_BASE})
  --agent <id>        tester agent id
  --workspace <id>    workspace id (REQUIRED: knowledge scope comes from it)
  --note <path>       a known-present note, for the Z-03 scope check
  --model <slug>      expected model slug (default z-ai/glm-5.3-flash)

  preflight           run Suite Z
  ask "<prompt>"      one turn; prints text, tool calls, timing, turn_failed
`;

function arg(name, dflt) {
  const i = process.argv.indexOf(`--${name}`);
  return i > -1 ? process.argv[i + 1] : dflt;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const cmd = process.argv.find((a) => a === 'preflight' || a === 'ask');
  if (!cmd || process.argv.includes('--help')) { console.log(HELP); process.exit(cmd ? 0 : 1); }

  const base = arg('base', DEFAULT_BASE);
  const home = arg('home', process.env.OMNIPUS_HOME);
  const token = cliToken(home);
  const agentID = arg('agent');
  const workspaceID = arg('workspace');
  const model = arg('model', 'z-ai/glm-5.3-flash');

  if (cmd === 'preflight') {
    const r = await preflight(base, token, {
      agentID, workspaceID, model, knownNotePath: arg('note'),
    });
    for (const c of r.checks) console.log(`${c.ok ? 'PASS' : 'FAIL'}  ${c.id}  ${c.detail}`);
    process.exit(r.ok ? 0 : 1);
  } else {
    const prompt = process.argv[process.argv.indexOf('ask') + 1];
    const r = await new Turn({ base, token, agentID, workspaceID }).ask(prompt);
    console.log(`--- text (${r.ms}ms, turn_failed=${r.turnFailed}) ---\n${r.text}`);
    console.log(`--- tools ---\n${r.toolCalls.map((c) => `${c.tool}${c.status ? `(${c.status})` : ''}`).join(', ') || '(none)'}`);
    // A failed turn must exit non-zero, or a broken run scores as a good one.
    process.exit(r.turnFailed ? 1 : 0);
  }
}
