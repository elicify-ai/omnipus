#!/usr/bin/env node
// Omnipus — view-kinds agent UAT (view-kinds-design-2026-09-03 §8 item 5).
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// ---------------------------------------------------------------------------
// WHAT THIS MEASURES, AND WHAT IT DELIBERATELY DOES NOT
//
// The design's whole claim is "the tools hold the expertise, not the agent"
// (§1). So the question is not whether GLM is clever. It is whether an agent
// that knows nothing about this vault can, from PLAIN LANGUAGE, end up with a
// correct view — and whether the tool stops it when it cannot.
//
// Every scenario therefore grades on ARTEFACTS, never on the agent's own
// narration:
//
//   * which tools it called, with which arguments (from the WS frames);
//   * what appeared on disk under .omnipus-vault/views/;
//   * what the SERVER returns for that view (GET .../knowledge/view), which is
//     the only thing a user would ever see.
//
// An agent that says "I have created a summary view with per-currency totals"
// and wrote nothing scores FAIL here, which is the entire point.
//
// ---------------------------------------------------------------------------
// TOOL DEFECT vs AGENT-COMPREHENSION DEFECT
//
// Each scenario records which of the two a failure would be, because the
// design's claim makes them different findings:
//
//   TOOL        the tool refused something it should allow, allowed something
//               it should refuse, produced a wrong number, or wrote a file
//               that does not load.
//   COMPREHENSION  the tool would have done the right thing, but the agent
//               never reached for it, or reached for the raw escape hatch.
//               Under §1 this is STILL a finding about the tool — its
//               description is what is in front of the agent at call time —
//               but it is a different fix, so it is labelled differently.
//
// ---------------------------------------------------------------------------
// PROMPTS NAME NO TOOL ARGUMENTS
//
// §8 item 5's pass condition is "from plain instructions". A prompt that says
// `op=create_view kind=summary number=weight` tests nothing but the harness's
// own knowledge. The prompts below are what a person would type. The one
// exception is scenario (a), which asks the agent to find out what it CAN
// build — discovery is the behaviour under test there, so naming the question
// is legitimate; naming the answer would not be.

import { readFileSync, existsSync, readdirSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { Turn } from '../uat-knowledge-harness.mjs';

function arg(name, dflt) {
  const i = process.argv.indexOf(`--${name}`);
  return i > -1 ? process.argv[i + 1] : dflt;
}

const BASE = arg('base');
const TOKEN = readFileSync(arg('token-file'), 'utf8').trim();
const AGENT = arg('agent');
const WS = arg('workspace');
const COLLECTION = arg('collection');
const VAULT = arg('vault');
const LABEL = arg('label', '?');
const OUT = arg('out', '.');
const TIMEOUT = Number(arg('timeout', '300')) * 1000;
const ONLY = arg('only', null);

const VIEWS_DIR = join(VAULT, '.omnipus-vault', 'views');

const listViews = () => (existsSync(VIEWS_DIR) ? readdirSync(VIEWS_DIR).sort() : []);

async function viewResult(name) {
  const url = `${BASE}/api/v1/library/${WS}/knowledge/view`
    + `?collection_id=${encodeURIComponent(COLLECTION)}&view=${encodeURIComponent(name)}`;
  const res = await fetch(url, { headers: { Authorization: `Bearer ${TOKEN}` } });
  const text = await res.text();
  let json = null;
  try { json = text ? JSON.parse(text) : null; } catch { /* reported as status+text */ }
  return { status: res.status, json, text };
}

/** Every knowledge_configure call the turn made, with its op and view name. */
function configureCalls(turn) {
  return turn.toolCalls
    .filter((c) => c.tool === 'knowledge_configure')
    .map((c) => ({
      op: c.params?.op,
      view: c.params?.view,
      kind: c.params?.kind,
      params: c.params,
      status: c.status,
      error: c.error,
      result: typeof c.result === 'string' ? c.result : JSON.stringify(c.result ?? null),
    }));
}

const findings = [];
const results = [];

function record(id, title, verdict, evidence, defect = null) {
  results.push({ id, title, verdict, evidence, defect });
  const mark = verdict === 'PASS' ? 'PASS' : verdict === 'FAIL' ? 'FAIL' : verdict;
  console.log(`${mark}  ${LABEL}/${id}  ${title}`);
  for (const line of evidence) console.log(`        ${line}`);
  if (defect) { console.log(`        DEFECT [${defect.kind}] ${defect.summary}`); findings.push({ id, ...defect }); }
}

const turn = () => new Turn({ base: BASE, token: TOKEN, agentID: AGENT, workspaceID: WS });

const transcripts = {};
async function ask(id, prompt, t = null) {
  const conv = t ?? turn();
  const r = await conv.ask(prompt, { timeoutMs: TIMEOUT });
  transcripts[id] = (transcripts[id] ?? []).concat([{
    prompt,
    text: r.text,
    turnFailed: r.turnFailed,
    ms: r.ms,
    toolCalls: r.toolCalls.map((c) => ({
      tool: c.tool, params: c.params, status: c.status, error: c.error,
      result: typeof c.result === 'string' ? c.result.slice(0, 6000) : c.result,
    })),
  }]);
  return { r, conv };
}

const want = (id) => !ONLY || ONLY.split(',').includes(id);

// ---------------------------------------------------------------------------
// (a) DISCOVERY — the agent must ASK what it can build, and the answer must
//     be what informs its next move (§6.3).
// ---------------------------------------------------------------------------
async function scenarioA(recordType) {
  const id = 'A-discover';
  const { r } = await ask(id,
    `I keep records of type "${recordType}" in this vault. What kinds of view can you `
    + `build for them, and what can you not build? List each one and, where you cannot, `
    + `say what is missing.`);

  const describes = r.toolCalls.filter((c) => c.tool === 'knowledge_describe');
  const raw = describes.map((c) => (typeof c.result === 'string' ? c.result : JSON.stringify(c.result ?? ''))).join('\n');

  // The availability block is the artefact under test, so it is looked for in
  // the TOOL RESULT, not in the agent's prose. An agent that recites the eight
  // kinds from its own priors while the tool said nothing must not pass.
  const blockPresent = /views you can create|available views|view kinds/i.test(raw);
  const namesKinds = ['table', 'list', 'tiles', 'board', 'calendar', 'summary', 'trend', 'breakdown']
    .filter((k) => new RegExp(`\\b${k}\\b`, 'i').test(raw));
  const tilesReason = /no image-capable property type exists yet/i.test(raw);

  const ev = [
    `knowledge_describe calls: ${describes.length}`,
    `kinds named in the TOOL RESULT: ${namesKinds.join(', ') || '(none)'}`,
    `tiles refusal wording present in tool result: ${tilesReason}`,
    `agent reply (first 300 chars): ${r.text.slice(0, 300).replace(/\s+/g, ' ')}`,
  ];

  if (r.turnFailed) return record(id, 'discovery via knowledge_describe', 'FAIL', ev,
    { kind: 'TOOL', summary: 'turn failed outright' });
  if (describes.length === 0) return record(id, 'discovery via knowledge_describe', 'FAIL', ev,
    { kind: 'COMPREHENSION', summary: 'the agent never called knowledge_describe; it answered from priors' });
  if (!blockPresent || namesKinds.length < 8) return record(id, 'discovery via knowledge_describe', 'FAIL', ev,
    { kind: 'TOOL', summary: `knowledge_describe returned no complete availability block (found ${namesKinds.length}/8 kinds)` });
  return record(id, 'discovery via knowledge_describe', 'PASS', ev);
}

// ---------------------------------------------------------------------------
// (b) THE THREE POSITIVE KINDS, from plain language, on ONE conversation so
//     the availability block from (a) is genuinely in front of the agent.
// ---------------------------------------------------------------------------
async function scenarioB(spec) {
  const conv = turn();
  // Prime the conversation exactly as §6.3 describes the flow: discovery
  // first, then compose. The prompt still names no tool argument.
  //
  // It asks about the TYPE rather than about "this vault". An earlier, opener
  // wording ("what can you tell me about the X records in this vault?") sent
  // the agent into twelve `list_directory` calls across the founder's 759-note
  // import and timed the turn out at 300s without ever reaching a view. That
  // is a real observation about open-ended questions on a large vault, and it
  // is NOT what §8 item 5 asks this suite to measure — the pass condition is
  // about composing a view from a plain-language request, so the priming step
  // is scoped to the record type the request will be about.
  await ask('B-prime', `Describe the "${spec.type}" record type: its properties and what views it supports.`, conv);

  for (const step of spec.steps) {
    const id = `B-${step.kind}`;
    if (!want(id)) continue;
    const before = listViews();
    const { r } = await ask(id, step.prompt, conv);
    const after = listViews();
    const created = after.filter((f) => !before.includes(f));
    const calls = configureCalls(r);
    const createCalls = calls.filter((c) => c.op === 'create_view');
    const writeCalls = calls.filter((c) => c.op === 'write_view');

    const ev = [
      `knowledge_configure ops: ${calls.map((c) => `${c.op}(${c.kind ?? '-'}) -> ${c.status ?? '?'}`).join(', ') || '(none)'}`,
      `view files created: ${created.join(', ') || '(none)'}`,
    ];

    if (created.length === 0) {
      ev.push(`agent reply: ${r.text.slice(0, 400).replace(/\s+/g, ' ')}`);
      const lastErr = calls.map((c) => c.error || c.result).filter(Boolean).slice(-1)[0];
      if (lastErr) ev.push(`last tool outcome: ${String(lastErr).slice(0, 600).replace(/\s+/g, ' ')}`);
      record(id, `plain-language ${step.kind}`, 'FAIL', ev, {
        kind: createCalls.length > 0 ? 'TOOL' : 'COMPREHENSION',
        summary: createCalls.length > 0
          ? `create_view was called ${createCalls.length}x and no file appeared`
          : 'the agent never called op=create_view',
      });
      continue;
    }

    // The file exists. Now the three things §4 requires OF the file, read from
    // the SERVER's answer rather than by re-parsing YAML here — a view that
    // the server cannot serve is not a working view however well it parses.
    const viewName = created[0].replace(/\.ya?ml$/, '');
    const vr = await viewResult(viewName);
    const yaml = readFileSync(join(VIEWS_DIR, created[0]), 'utf8');
    const hasKind = /^kind:\s*\S+/m.test(yaml);
    const hasParts = /^parts:/m.test(yaml);
    const parts = vr.json?.parts ?? [];
    const partNames = parts.map((p) => p.part);
    const refusal = vr.json?.refusal;

    ev.push(`file carries kind: ${hasKind}, parts: ${hasParts}`);
    ev.push(`server HTTP ${vr.status}; parts served: ${partNames.join(' -> ') || '(none)'}`);
    if (refusal) ev.push(`server refusal: ${refusal.code} — ${refusal.reason}`);

    const expected = step.expectParts;
    const partsOK = expected.every((p) => partNames.includes(p));
    if (writeCalls.length > 0) {
      record(id, `plain-language ${step.kind}`, 'FAIL', ev, {
        kind: 'COMPREHENSION',
        summary: 'the agent used the raw write_view escape hatch instead of create_view',
      });
    } else if (vr.status !== 200 || refusal || !partsOK || !hasKind || !hasParts) {
      record(id, `plain-language ${step.kind}`, 'FAIL', ev, {
        kind: 'TOOL',
        summary: `the created view does not serve as a ${step.kind}: expected parts ${expected.join('+')}, got ${partNames.join('+') || 'none'}`,
      });
    } else {
      record(id, `plain-language ${step.kind}`, 'PASS', ev);
    }

    // Carry the served result forward for the unit checks in (d).
    spec.served ??= {};
    spec.served[step.kind] = vr;
  }
}

// ---------------------------------------------------------------------------
// (c) THE IMPOSSIBLE REQUEST — a refusal that NAMES the missing requirement,
//     and NO file (G1 + G6: "never a partial file").
// ---------------------------------------------------------------------------
async function scenarioC(step) {
  const id = `C-${step.id}`;
  const before = listViews();
  const { r } = await ask(id, step.prompt);
  const after = listViews();
  const created = after.filter((f) => !before.includes(f));
  const calls = configureCalls(r);
  const toolText = calls.map((c) => `${c.error ?? ''} ${c.result ?? ''}`).join(' ');
  const said = `${toolText} ${r.text}`;

  const named = step.mustName.some((s) => new RegExp(s, 'i').test(said));
  const ev = [
    `knowledge_configure ops: ${calls.map((c) => `${c.op}(${c.kind ?? '-'}) -> ${c.status ?? '?'}`).join(', ') || '(none)'}`,
    `view files created: ${created.join(', ') || '(none)'}`,
    `missing-requirement wording present: ${named} (looked for: ${step.mustName.join(' | ')})`,
    `agent reply: ${r.text.slice(0, 400).replace(/\s+/g, ' ')}`,
  ];

  if (created.length > 0) {
    return record(id, step.title, 'FAIL', ev, {
      kind: 'TOOL', summary: `a view file was written for an impossible request: ${created.join(', ')}`,
    });
  }
  if (!named) {
    return record(id, step.title, 'FAIL', ev, {
      kind: calls.length === 0 ? 'COMPREHENSION' : 'TOOL',
      summary: calls.length === 0
        ? 'the agent refused on its own without consulting the tool, so the tool\'s gate was never exercised'
        : 'the tool refused but named no missing requirement',
    });
  }
  return record(id, step.title, 'PASS', ev);
}

// ---------------------------------------------------------------------------
// (d) THE KILLER ASSERTION — per-unit totals only, from the SERVER.
//
// Graded against the answer key computed in gen-recipes-vault.mjs BEFORE this
// run, never against what the server happened to return.
// ---------------------------------------------------------------------------
async function scenarioD(key) {
  const id = 'D-units';
  const before = listViews();
  const { r } = await ask(id,
    'Give me a saved report of the total weight of my recipes, broken down by cuisine. '
    + 'Call it recipe-weight-report. I want to see the totals.');
  const after = listViews();
  const created = after.filter((f) => !before.includes(f));
  const calls = configureCalls(r);

  const ev = [
    `knowledge_configure ops: ${calls.map((c) => `${c.op}(${c.kind ?? '-'}) -> ${c.status ?? '?'}`).join(', ') || '(none)'}`,
    `view files created: ${created.join(', ') || '(none)'}`,
  ];
  if (created.length === 0) {
    ev.push(`agent reply: ${r.text.slice(0, 500).replace(/\s+/g, ' ')}`);
    const lastErr = calls.map((c) => c.error || c.result).filter(Boolean).slice(-1)[0];
    if (lastErr) ev.push(`last tool outcome: ${String(lastErr).slice(0, 800).replace(/\s+/g, ' ')}`);
    return record(id, 'mixed-unit total is per-unit only', 'FAIL', ev, {
      kind: calls.some((c) => c.op === 'create_view') ? 'TOOL' : 'COMPREHENSION',
      summary: 'no view was produced for a plain-language total request',
    });
  }

  const viewName = created[0].replace(/\.ya?ml$/, '');
  const vr = await viewResult(viewName);
  ev.push(`server HTTP ${vr.status} for view ${viewName}`);
  if (vr.status !== 200) {
    return record(id, 'mixed-unit total is per-unit only', 'FAIL', ev,
      { kind: 'TOOL', summary: `the created view does not serve (HTTP ${vr.status}): ${vr.text.slice(0, 300)}` });
  }

  const parts = vr.json?.parts ?? [];
  // FIELD NAMES ARE THE CONTRACT'S, NOT GUESSES. A part's whole-result figures
  // are `totals` (ViewResultPart.totals); a GROUP's per-group figures are
  // `subtotals` under `key`, not `totals` under `value`
  // (ViewResultGroup.yaml). Reading the wrong key yields `undefined` silently,
  // which would have made this scenario report "no totals" on a perfectly
  // correct server — a false failure indistinguishable from a real one.
  const allTotals = parts.flatMap((p) => (p.totals ?? []).map((t) => ({ ...t, part: p.part })));
  const groupTotals = parts.flatMap((p) => (p.groups ?? []).flatMap((g) => (g.subtotals ?? [])));
  const totals = [...allTotals, ...groupTotals];
  const weightTotals = totals.filter((t) => t.property === 'weight');
  const excludedCounts = parts.map((p) => p.excluded_count).filter((n) => n !== undefined);
  const excludedPaths = parts.flatMap((p) => p.excluded_paths ?? []);

  ev.push(`weight totals returned: ${weightTotals.map((t) => `${t.op}=${t.value}${t.unit ? ` ${t.unit}` : ' <NO UNIT>'}(n=${t.count})`).join(', ') || '(none)'}`);
  ev.push(`excluded_count per part: ${excludedCounts.join(', ') || '(none)'}; excluded_paths: ${excludedPaths.length}`);

  const problems = [];

  // 1. THE forbidden number, checked as an equality against a value computed
  //    before the run. Not "does it look combined" — the exact wrong figure.
  const forbidden = key.weight.forbidden_combined_value;
  const serialized = JSON.stringify(vr.json);
  if (serialized.includes(`"${forbidden}"`)) {
    problems.push(`the combined-across-units figure ${forbidden} appears in the result`);
  }

  // 2. Every weight total must carry a unit. A unit-less total over a
  //    unit-carrying number IS the combined figure, whatever its value.
  const unitless = weightTotals.filter((t) => t.unit === undefined || t.unit === null);
  if (weightTotals.length > 0 && unitless.length > 0) {
    problems.push(`${unitless.length} weight total(s) carry no unit`);
  }

  // 3. Where the whole-result figures row is present, its per-unit values must
  //    match the key exactly.
  const figures = parts.find((p) => p.part === 'figures');
  if (figures) {
    const sums = (figures.totals ?? []).filter((t) => t.property === 'weight' && t.op === 'sum');
    for (const expected of key.weight.totals) {
      const got = sums.find((t) => t.unit === expected.unit);
      if (!got) { problems.push(`no sum for unit ${expected.unit}`); continue; }
      if (got.value !== expected.value) {
        problems.push(`sum for ${expected.unit}: server ${got.value}, key ${expected.value}`);
      }
      if (got.count !== expected.count) {
        problems.push(`count for ${expected.unit}: server ${got.count}, key ${expected.count}`);
      }
    }
    const exc = figures.excluded_count ?? 0;
    if (exc !== key.weight.excluded_count) {
      problems.push(`excluded_count: server ${exc}, key ${key.weight.excluded_count}`);
    }
  } else {
    problems.push('no figures part in the served result, so no whole-result per-unit totals to check');
  }

  if (problems.length > 0) {
    ev.push(...problems.map((p) => `PROBLEM: ${p}`));
    return record(id, 'mixed-unit total is per-unit only', 'FAIL', ev,
      { kind: 'TOOL', summary: problems[0] });
  }
  ev.push(`matches the pre-computed key: ${key.weight.totals.map((t) => `${t.value} ${t.unit}`).join(', ')}; ${key.weight.excluded_count} excluded`);
  return record(id, 'mixed-unit total is per-unit only', 'PASS', ev);
}

// ---------------------------------------------------------------------------

const PLANS = {
  // Vault A — the founder's own import. `task` has dates and small enums but
  // NO number (surveyed from the imported schemas before the run), which makes
  // it both the board/calendar subject and the honest "trend is impossible"
  // subject. `decision` carries `version`, a number, plus dates.
  A: {
    discoverType: 'task',
    b: {
      type: 'decision',
      steps: [
        {
          kind: 'summary',
          prompt: 'Build me a saved report of my decisions grouped by status, with a total on each group. Call it uat-decisions-by-status.',
          expectParts: ['figures', 'table'],
        },
        {
          kind: 'trend',
          prompt: 'Now save me a view that shows how my decisions have moved over time. Call it uat-decisions-over-time.',
          expectParts: ['figures', 'chart', 'table'],
        },
        {
          kind: 'board',
          prompt: 'Save a view of my tasks laid out as columns by their status, like a kanban board. Call it uat-tasks-board. Use the task records for this one.',
          expectParts: ['columns'],
        },
      ],
    },
    c: [
      {
        id: 'tiles',
        title: 'tiles is refused everywhere (D5)',
        prompt: 'Save me a view of my decisions as a grid of picture tiles. Call it uat-decision-tiles.',
        mustName: ['image', 'no image-capable property type'],
      },
      {
        id: 'trend-no-number',
        title: 'a trend on a type with no number is refused, naming the number',
        prompt: 'Save me a view charting my tasks over time with a running total. Call it uat-task-trend.',
        mustName: ['number', 'no number'],
      },
    ],
  },

  // Vault B — recipes. Nothing here is money, which is the whole point.
  B: {
    discoverType: 'recipe',
    b: {
      type: 'recipe',
      steps: [
        {
          kind: 'summary',
          prompt: 'Build me a saved report of my recipes grouped by cuisine, with the totals on each group. Call it uat-recipes-by-cuisine.',
          expectParts: ['figures', 'table'],
        },
        {
          kind: 'trend',
          prompt: 'Save a view showing how my recipe cooking has gone over time. Call it uat-recipes-over-time.',
          expectParts: ['figures', 'chart', 'table'],
        },
        {
          kind: 'board',
          prompt: 'Save a view of my recipes as columns, one column per cuisine, like a board. Call it uat-recipes-board.',
          expectParts: ['columns'],
        },
      ],
    },
    c: [
      {
        id: 'tiles',
        title: 'tiles is refused everywhere (D5)',
        prompt: 'Save me a view of my recipes as a grid of photo tiles. Call it uat-recipe-tiles.',
        mustName: ['image', 'no image-capable property type'],
      },
      {
        id: 'trend-no-number',
        title: 'a trend on techniques (no number) is refused, naming the number',
        prompt: 'Save me a view charting my cooking techniques over time with totals. Call it uat-technique-trend.',
        mustName: ['number', 'no number'],
      },
    ],
  },
};

const plan = PLANS[LABEL];
if (!plan) { console.error(`no plan for label ${LABEL}`); process.exit(2); }

mkdirSync(OUT, { recursive: true });

try {
  if (want('A-discover')) await scenarioA(plan.discoverType);
  await scenarioB(plan.b);
  for (const c of plan.c) if (want(`C-${c.id}`)) await scenarioC(c);
  if (LABEL === 'B' && want('D-units')) {
    const key = JSON.parse(readFileSync(arg('answer-key'), 'utf8'));
    await scenarioD(key);
  }
} finally {
  writeFileSync(join(OUT, `transcripts-${LABEL}.json`), JSON.stringify(transcripts, null, 2) + '\n');
  writeFileSync(join(OUT, `results-${LABEL}.json`), JSON.stringify({ results, findings }, null, 2) + '\n');
}

const failed = results.filter((r) => r.verdict !== 'PASS');
console.log(`\n${LABEL}: ${results.length - failed.length}/${results.length} passed`);
// A non-zero exit on any failure, so a broken run cannot be mistaken for a
// good one by a caller that only reads the status.
process.exit(failed.length === 0 ? 0 : 1);
