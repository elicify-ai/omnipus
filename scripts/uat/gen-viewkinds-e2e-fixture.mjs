#!/usr/bin/env node
// Omnipus — the browser-side fixture for the view-kinds E2E specs.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// ---------------------------------------------------------------------------
// WHY A SEPARATE FIXTURE FROM VAULT B
//
// The agent UAT (run-viewkinds-agent-uat.mjs) grades what an AGENT produces,
// so its vault must start with no views at all. The browser specs grade what
// the RENDERER draws, so they need views that already exist and whose contents
// are known exactly. Sharing one vault would mean each suite mutating the
// other's premise.
//
// This fixture is vault B's recipes (mixed g / kg / cup — the same non-money
// unit case) plus four things the browser specs need and the agent suite does
// not:
//
//   1. `06-Bases/Recipes.base` with TWO imported views carrying
//      `source: 06-Bases/Recipes.base`, so clicking the base file has tabs to
//      draw. The SPA no longer parses `.base` itself — the server enumerates
//      the views (fix/vk-enumeration) — so a base file WITHOUT imported view
//      files would legitimately show zero tabs. The `source:` line is what
//      makes this fixture exercise the real path.
//   2. `06-Bases/Orphan.base`, a base file with NO imported views, for the
//      escape-hatch spec: zero views must still offer "View raw".
//   3. A `metric` record type whose values GO NEGATIVE, for the chart
//      clipping regression. Nothing in the recipes has a negative weight, and
//      a regression test for negative rendering needs negatives.
//   4. A pre-built `summary` view over the mixed units, so the subtotal /
//      per-unit-total / excluded-counter specs have a fixed target rather
//      than whatever an agent happened to name its view.
//
// Deterministic and literal throughout, for the reason the recipes generator
// gives: a browser assertion on "480" is only meaningful if 480 is fixed.
// ---------------------------------------------------------------------------

import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { execFileSync } from 'node:child_process';

const HERE = path.dirname(new URL(import.meta.url).pathname);

function arg(name) {
  const i = process.argv.indexOf(`--${name}`);
  return i > -1 ? process.argv[i + 1] : undefined;
}

const outDir = path.resolve(arg('out') ?? (() => {
  process.stderr.write('gen-viewkinds-e2e-fixture.mjs --out <dir> [--force]\n');
  process.exit(2);
})());
const force = process.argv.includes('--force');

if (fs.existsSync(outDir)) {
  if (!force) throw new Error(`${outDir} already exists; pass --force to replace it`);
  fs.rmSync(outDir, { recursive: true, force: true });
}

// Start from the recipes vault, so the browser and the agent suites are
// looking at the SAME numbers and a disagreement between them is a real one.
execFileSync(process.execPath,
  [path.join(HERE, 'gen-recipes-vault.mjs'), '--out', outDir, '--force'],
  { stdio: 'inherit' });

const vault = path.join(outDir, 'vault');
const viewsDir = path.join(vault, '.omnipus-vault', 'views');
const recordsDir = path.join(vault, '.omnipus-vault', 'records');
fs.mkdirSync(viewsDir, { recursive: true });
fs.mkdirSync(path.join(vault, '06-Bases'), { recursive: true });

// ---------------------------------------------------------------------------
// 1 + 4. The base file and its two imported views
// ---------------------------------------------------------------------------

fs.writeFileSync(path.join(vault, '06-Bases', 'Recipes.base'), `filters:
  and:
    - type == "recipe"
views:
  - type: table
    name: By Cuisine
    groupBy:
      property: cuisine
      direction: ASC
    summaries:
      weight: Sum
  - type: table
    name: All Recipes
`);

// The summary view: figures (whole-result per-unit totals) then a grouped
// table (per-group, per-unit subtotals). Both halves carry the G3 excluded
// rows, which is what the excluded-counter spec reads.
fs.writeFileSync(path.join(viewsDir, 'recipes--by-cuisine.yaml'), `# Fixture for the view-kinds E2E specs — generated, do not edit by hand.
name: recipes--by-cuisine
type: recipe
label: By Cuisine
kind: summary
layout: table
parts:
  - part: figures
    number: weight
    unit: weight_unit
    aggregate: sum
  - part: table
    grouping:
      - property: cuisine
        direction: asc
    subtotals:
      weight: sum
properties:
  - file.name
  - cuisine
  - weight
  - weight_unit
source: 06-Bases/Recipes.base
`);

fs.writeFileSync(path.join(viewsDir, 'recipes--all-recipes.yaml'), `# Fixture for the view-kinds E2E specs — generated, do not edit by hand.
name: recipes--all-recipes
type: recipe
label: All Recipes
kind: table
layout: table
properties:
  - file.name
  - cuisine
  - main_ingredient
  - weight
  - weight_unit
source: 06-Bases/Recipes.base
`);

// ---------------------------------------------------------------------------
// 2. A base file with no imported views — the escape-hatch case
// ---------------------------------------------------------------------------

fs.writeFileSync(path.join(vault, '06-Bases', 'Orphan.base'), `# This base file was never imported, so no view file names it as its source.
# The SPA must still let a reader see the bytes.
filters:
  and:
    - type == "recipe"
views:
  - type: table
    name: Never Imported
`);

// 2b. A base whose ONE imported view is BROKEN — the other half of the escape
// hatch. This is not the same fact as "no views were imported": the server
// reports unloadable_count > 0, the SPA must say the views could not be
// LOADED rather than that the file declares none, and "View raw" must still
// be there. A reader whose file the product cannot understand is exactly the
// reader who most needs to see the bytes.
fs.writeFileSync(path.join(vault, '06-Bases', 'Damaged.base'), `filters:
  and:
    - type == "recipe"
views:
  - type: table
    name: Damaged
`);
fs.writeFileSync(path.join(viewsDir, 'damaged--broken.yaml'),
  '# Fixture: deliberately unloadable. `type` names a record type that does not\n'
  + '# exist, and `parts` is a scalar where the schema requires a sequence.\n'
  + 'name: damaged--broken\n'
  + 'type: no-such-record-type\n'
  + 'layout: table\n'
  + 'parts: "this is not a list of parts"\n'
  + 'source: 06-Bases/Damaged.base\n');

// ---------------------------------------------------------------------------
// 3. The negative-value chart
//
// `delta` swings either side of zero. That is the whole point: the clipping
// bug (finding #8) drew a negative bar as a negative SVG height, which browsers
// drop, so the value silently vanished from the chart. A fixture whose numbers
// are all positive cannot catch its return.
// ---------------------------------------------------------------------------

fs.writeFileSync(path.join(recordsDir, 'metric.yaml'), `# Generated by scripts/uat/gen-viewkinds-e2e-fixture.mjs — do not edit by hand.
schema_version: 1
type: metric
label: "Metric"
properties:
  measured_on:
    type: date
  delta:
    type: decimal
`);

const METRICS = [
  { name: 'Week 01', measured_on: '2026-01-05', delta: 12 },
  { name: 'Week 02', measured_on: '2026-01-12', delta: -8 },
  { name: 'Week 03', measured_on: '2026-01-19', delta: 5 },
  { name: 'Week 04', measured_on: '2026-01-26', delta: -15 },
  { name: 'Week 05', measured_on: '2026-02-02', delta: 20 },
  { name: 'Week 06', measured_on: '2026-02-09', delta: -3 },
];
fs.mkdirSync(path.join(vault, 'metrics'), { recursive: true });
for (const m of METRICS) {
  fs.writeFileSync(
    path.join(vault, 'metrics', `${m.name.toLowerCase().replace(/\s+/g, '-')}.md`),
    `---\ntype: metric\nname: "${m.name}"\nmeasured_on: "${m.measured_on}"\ndelta: ${m.delta}\n---\n\n## ${m.name}\n`);
}

// The chart view is given a `source` and a base file of its own for a reason
// that is not cosmetic: `.base` files are how a person REACHES a view in the
// SPA. A view file with no base to open it from is served by the API and
// unreachable in the product — which would make a green browser test a
// statement about an endpoint rather than about anything a user can do.
fs.writeFileSync(path.join(vault, '06-Bases', 'Metrics.base'), `filters:
  and:
    - type == "metric"
views:
  - type: table
    name: Metric Trend
`);
fs.writeFileSync(path.join(viewsDir, 'metric-trend.yaml'), `# Fixture for the view-kinds E2E specs — generated, do not edit by hand.
name: metric-trend
type: metric
label: "Metric Trend"
kind: trend
layout: table
parts:
  - part: chart
    number: delta
    date: measured_on
  - part: table
properties:
  - file.name
  - measured_on
  - delta
source: 06-Bases/Metrics.base
`);

const facts = {
  generated_by: 'scripts/uat/gen-viewkinds-e2e-fixture.mjs',
  base_file: '06-Bases/Recipes.base',
  base_view_tabs: ['recipes--by-cuisine', 'recipes--all-recipes'],
  base_view_labels: ['By Cuisine', 'All Recipes'],
  orphan_base_file: '06-Bases/Orphan.base',
  damaged_base_file: '06-Bases/Damaged.base',
  chart_base_file: '06-Bases/Metrics.base',
  summary_view: 'recipes--by-cuisine',
  // Read straight out of the recipes answer key so the two files cannot drift.
  ...JSON.parse(fs.readFileSync(path.join(outDir, 'answer-key.json'), 'utf8')).weight
    ? {
      per_unit_totals: JSON.parse(fs.readFileSync(path.join(outDir, 'answer-key.json'), 'utf8')).weight.totals,
      excluded_count: JSON.parse(fs.readFileSync(path.join(outDir, 'answer-key.json'), 'utf8')).weight.excluded_count,
      forbidden_combined_value: JSON.parse(fs.readFileSync(path.join(outDir, 'answer-key.json'), 'utf8')).weight.forbidden_combined_value,
    }
    : {},
  chart_view: 'metric-trend',
  chart_values: METRICS.map((m) => m.delta),
  chart_has_negatives: METRICS.some((m) => m.delta < 0),
};
fs.writeFileSync(path.join(outDir, 'e2e-facts.json'), JSON.stringify(facts, null, 2) + '\n');

process.stdout.write(
  `e2e fixture at ${vault}\n`
  + `  base file with 2 imported views: ${facts.base_file}\n`
  + `  orphan base (no views): ${facts.orphan_base_file}\n`
  + `  summary view: ${facts.summary_view} — per-unit ${facts.per_unit_totals.map((t) => `${t.value} ${t.unit}`).join(', ')}, ${facts.excluded_count} excluded\n`
  + `  chart view: ${facts.chart_view} — values ${facts.chart_values.join(', ')}\n`
  + `  facts: ${path.join(outDir, 'e2e-facts.json')}\n`);
