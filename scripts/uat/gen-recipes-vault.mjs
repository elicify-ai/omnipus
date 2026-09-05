#!/usr/bin/env node
// Omnipus — UAT Vault B: a synthetic NON-FINANCIAL vault for view-kinds.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// view-kinds-design-2026-09-03 §8 item 5 requires the UAT to run on TWO
// vaults: the founder's own import AND a synthetic vault of a DIFFERENT
// DOMAIN. That second vault is the only thing that can test the design's
// vault-agnosticism ruling (§1, "No rule may mention a domain … Money is
// merely 'a number with a unit'; grams, hours and euros obey the same law").
// A test suite run only against invoices cannot distinguish "G2 is a general
// rule about units" from "G2 is a rule about currency that happens to work".
//
// So this builds recipes. Nothing in it is money. The number that must never
// be combined across units is a WEIGHT, held in grams, kilograms and cups —
// three units, one property, and no currency anywhere in the vault.
//
// ---------------------------------------------------------------------------
// THE ANSWER KEY IS COMPUTED FROM THE MODEL, NEVER FROM A RUN
//
// docs/internal/uat/knowledge-tools-uat-plan.md §1.1: "Ground truth is fixed
// BEFORE the run … A search test whose expected answers were read off the
// result set measures nothing." The same rule governs a TOTAL. Every figure
// in answer-key.json below is summed here, in this file, from the same
// in-memory rows that produced the note files — and derived from the DESIGN's
// wording of G2/G3, not from reading what pkg/gateway computed:
//
//   G2  a number-with-unit totals once per unit value, never across units.
//   G3  a row whose unit is missing/unconfirmed is SHOWN, excluded from every
//       total, and counted separately.
//
// Concretely: `weight_grams_total` here is the sum of the weights of exactly
// those rows whose weight_unit is the single value "g". No row with an absent
// unit contributes to any total, and there is deliberately no
// `weight_total_all_units` key — the key cannot express the wrong answer, in
// the same way ViewUnitTotal's list shape cannot.
//
// Node built-ins only (CLAUDE.md Hard Constraint #1). Fully deterministic:
// every value below is written out literally, so there is no PRNG to seed and
// two runs are byte-identical by construction.
// ---------------------------------------------------------------------------

import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

// Vault layout constants, read out of the Go source rather than guessed:
//   pkg/knowledge/marker.go  MarkerDirName = ".omnipus-vault", vault.json
//   pkg/records/schema.go    SchemaDir(root) = <root>/.omnipus-vault/records
const MARKER_DIR_NAME = '.omnipus-vault';
const MARKER_FILE_NAME = 'vault.json';
const RECORDS_DIR_NAME = 'records';
const SUPPORTED_SCHEMA_VERSION = 1;

// ---------------------------------------------------------------------------
// The schemas
// ---------------------------------------------------------------------------

/**
 * `recipe` — the type every positive view-kind case is authored against.
 *
 * The property set is chosen so that the availability block has a KNOWN
 * expected answer for all eight kinds (design §2.3):
 *
 *   table      always
 *   list       always
 *   tiles      NEVER — D5, no image-capable property type exists
 *   board      cuisine is an enum of 5 (<= 8)
 *   calendar   prep_date
 *   summary    weight / servings are numbers
 *   trend      prep_date + a number
 *   breakdown  cuisine + main_ingredient + a number
 *
 * TWO number-with-unit pairs, deliberately:
 *   weight   (decimal) -> weight_unit  — MIXED across rows: g, kg, cup.
 *                                        This is the G2 killer case.
 *   servings (integer) -> portion_type — a unit that is not a mass at all,
 *                                        which is the point: the rule is
 *                                        about units, not about measurement.
 *
 * `difficulty` is TEXT holding digits ("3", "4"). It exists only so G4 has a
 * target on this vault: text is never totalled even when it parses.
 */
const RECIPE_SCHEMA = {
  type: 'recipe',
  label: 'Recipe',
  properties: [
    { name: 'cuisine', type: 'enum', values: ['italian', 'thai', 'mexican', 'japanese', 'french'] },
    {
      name: 'main_ingredient', type: 'enum',
      values: ['chicken', 'tofu', 'beef', 'lentils', 'salmon', 'mushroom'],
    },
    { name: 'prep_date', type: 'date' },
    { name: 'servings', type: 'integer', unit_property: 'portion_type' },
    { name: 'portion_type', type: 'enum', values: ['plate', 'bowl', 'jar'] },
    { name: 'weight', type: 'decimal', unit_property: 'weight_unit' },
    { name: 'weight_unit', type: 'enum', values: ['g', 'kg', 'cup'] },
    { name: 'difficulty', type: 'text' },
    { name: 'status', type: 'enum', values: ['draft', 'tested', 'published'] },
  ],
};

/**
 * `technique` — a type with NO number property at all.
 *
 * Its whole job is to be the subject of an impossible request that is NOT
 * tiles: design §3 G1 says a refusal must NAME the missing property, so
 * "make me a trend of techniques over time" must come back naming the absent
 * number rather than producing an empty chart. A vault where every type has a
 * number cannot test that.
 */
const TECHNIQUE_SCHEMA = {
  type: 'technique',
  label: 'Technique',
  properties: [
    { name: 'learned_on', type: 'date' },
    { name: 'source', type: 'text' },
    { name: 'notes', type: 'text' },
  ],
};

const SCHEMAS = [RECIPE_SCHEMA, TECHNIQUE_SCHEMA];

// ---------------------------------------------------------------------------
// The rows
//
// Written out literally rather than generated, because every one of them is
// load-bearing for a specific assertion and a reader must be able to check
// the answer key by hand. `weight_unit: null` means the KEY IS ABSENT from
// the note — a G3 row: shown, excluded, counted.
// ---------------------------------------------------------------------------

const RECIPES = [
  // --- weight in grams (7 rows, sum 3450) ---------------------------------
  { name: 'Ragu Bianco', cuisine: 'italian', main_ingredient: 'beef', prep_date: '2026-01-14', servings: 4, portion_type: 'plate', weight: 600, weight_unit: 'g', difficulty: '3', status: 'published' },
  { name: 'Miso Aubergine', cuisine: 'japanese', main_ingredient: 'mushroom', prep_date: '2026-01-28', servings: 2, portion_type: 'plate', weight: 320, weight_unit: 'g', difficulty: '2', status: 'published' },
  { name: 'Green Curry', cuisine: 'thai', main_ingredient: 'chicken', prep_date: '2026-02-09', servings: 4, portion_type: 'bowl', weight: 750, weight_unit: 'g', difficulty: '4', status: 'tested' },
  { name: 'Tinga de Pollo', cuisine: 'mexican', main_ingredient: 'chicken', prep_date: '2026-02-22', servings: 6, portion_type: 'plate', weight: 900, weight_unit: 'g', difficulty: '3', status: 'published' },
  { name: 'Ratatouille', cuisine: 'french', main_ingredient: 'mushroom', prep_date: '2026-03-05', servings: 4, portion_type: 'bowl', weight: 480, weight_unit: 'g', difficulty: '2', status: 'tested' },
  { name: 'Pad Krapow', cuisine: 'thai', main_ingredient: 'tofu', prep_date: '2026-03-18', servings: 2, portion_type: 'plate', weight: 260, weight_unit: 'g', difficulty: '2', status: 'draft' },
  { name: 'Salmon Teriyaki', cuisine: 'japanese', main_ingredient: 'salmon', prep_date: '2026-04-02', servings: 1, portion_type: 'plate', weight: 140, weight_unit: 'g', difficulty: '1', status: 'published' },

  // --- weight in kilograms (4 rows, sum 9.4) ------------------------------
  // Deliberately the SAME property as the grams rows. A total that combined
  // them would read "3459.4", a number that looks entirely plausible and is
  // meaningless — precisely the silent failure §1 of the design describes.
  { name: 'Cassoulet', cuisine: 'french', main_ingredient: 'beef', prep_date: '2026-01-21', servings: 8, portion_type: 'bowl', weight: 3.2, weight_unit: 'kg', difficulty: '5', status: 'published' },
  { name: 'Birria', cuisine: 'mexican', main_ingredient: 'beef', prep_date: '2026-02-14', servings: 8, portion_type: 'bowl', weight: 2.8, weight_unit: 'kg', difficulty: '5', status: 'tested' },
  { name: 'Osso Buco', cuisine: 'italian', main_ingredient: 'beef', prep_date: '2026-03-27', servings: 6, portion_type: 'plate', weight: 2.1, weight_unit: 'kg', difficulty: '4', status: 'published' },
  { name: 'Pot au Feu', cuisine: 'french', main_ingredient: 'beef', prep_date: '2026-04-15', servings: 6, portion_type: 'bowl', weight: 1.3, weight_unit: 'kg', difficulty: '3', status: 'draft' },

  // --- weight in cups (4 rows, sum 9.5) -----------------------------------
  // A third unit, and one that is a VOLUME. Nothing in the rules may notice.
  { name: 'Dal Tarka', cuisine: 'thai', main_ingredient: 'lentils', prep_date: '2026-01-07', servings: 4, portion_type: 'bowl', weight: 3, weight_unit: 'cup', difficulty: '2', status: 'published' },
  { name: 'Risotto Milanese', cuisine: 'italian', main_ingredient: 'mushroom', prep_date: '2026-02-28', servings: 4, portion_type: 'plate', weight: 2.5, weight_unit: 'cup', difficulty: '4', status: 'tested' },
  { name: 'Congee', cuisine: 'japanese', main_ingredient: 'chicken', prep_date: '2026-03-11', servings: 3, portion_type: 'bowl', weight: 2, weight_unit: 'cup', difficulty: '1', status: 'published' },
  { name: 'Pozole Verde', cuisine: 'mexican', main_ingredient: 'chicken', prep_date: '2026-04-23', servings: 5, portion_type: 'bowl', weight: 2, weight_unit: 'cup', difficulty: '3', status: 'draft' },

  // --- G3: a weight with NO unit (3 rows) ---------------------------------
  // These carry a perfectly readable NUMBER and no unit. They must appear in
  // the rows of every view that lists recipes, must be in NO total, and must
  // be counted and named in excluded_paths.
  { name: 'Nonna Sauce', cuisine: 'italian', main_ingredient: 'beef', prep_date: '2026-02-03', servings: 4, portion_type: 'plate', weight: 500, weight_unit: null, difficulty: '2', status: 'draft' },
  { name: 'Larb Salad', cuisine: 'thai', main_ingredient: 'tofu', prep_date: '2026-03-02', servings: 2, portion_type: 'bowl', weight: 220, weight_unit: null, difficulty: '2', status: 'draft' },
  { name: 'Tarte Tatin', cuisine: 'french', main_ingredient: 'mushroom', prep_date: '2026-04-08', servings: 6, portion_type: 'plate', weight: 800, weight_unit: null, difficulty: '4', status: 'tested' },
];

const TECHNIQUES = [
  { name: 'Blanching', learned_on: '2026-01-05', source: 'McGee', notes: 'brief boil, ice bath' },
  { name: 'Confit', learned_on: '2026-01-19', source: 'Escoffier', notes: 'slow cook submerged in fat' },
  { name: 'Velveting', learned_on: '2026-02-11', source: 'Kwok', notes: 'starch and egg white marinade' },
  { name: 'Deglazing', learned_on: '2026-02-25', source: 'McGee', notes: 'dissolve fond with liquid' },
  { name: 'Tempering', learned_on: '2026-03-09', source: 'Corriher', notes: 'raise temperature gradually' },
  { name: 'Emulsifying', learned_on: '2026-03-30', source: 'McGee', notes: 'suspend fat in water' },
];

// ---------------------------------------------------------------------------
// Emitting
// ---------------------------------------------------------------------------

function yamlQuote(s) {
  return `"${String(s).replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
}

function emitSchemaYAML(schema) {
  const lines = [
    '# Generated by scripts/uat/gen-recipes-vault.mjs — do not edit by hand.',
    '# UAT Vault B for view-kinds-design-2026-09-03 §8 item 5.',
    `schema_version: ${SUPPORTED_SCHEMA_VERSION}`,
    `type: ${schema.type}`,
    `label: ${yamlQuote(schema.label)}`,
    'properties:',
  ];
  for (const p of schema.properties) {
    lines.push(`  ${p.name}:`);
    lines.push(`    type: ${p.type}`);
    // `unit_property` is design §5's declaration: a number's companion unit is
    // DECLARED on the record type, never inferred from a nearby enum.
    if (p.unit_property) lines.push(`    unit_property: ${p.unit_property}`);
    if (p.values) {
      lines.push('    values:');
      for (const v of p.values) lines.push(`      - ${yamlQuote(v)}`);
    }
  }
  return lines.join('\n') + '\n';
}

/** A note's frontmatter omits a key entirely when its value is null — that is
 *  what makes a G3 row a MISSING unit rather than an empty-string one. */
function renderNote(recordType, row, body) {
  const lines = ['---', `type: ${recordType}`];
  for (const [k, v] of Object.entries(row)) {
    if (v === null || v === undefined) continue;
    lines.push(`${k}: ${typeof v === 'number' ? v : yamlQuote(v)}`);
  }
  lines.push('---', '', body, '');
  return lines.join('\n');
}

function slug(name) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
}

// ---------------------------------------------------------------------------
// The answer key — derived HERE, from the rows above and the design's rules
// ---------------------------------------------------------------------------

/**
 * Sums with integer cents-style scaling rather than raw float addition, so
 * `3.2 + 2.8 + 2.1 + 1.3` is 9.4 and not 9.399999999999999. The server states
 * totals as exact decimal TEXT (ViewUnitTotal.value: "never a JSON number"),
 * so a key computed in binary floats would disagree with a CORRECT server.
 */
function exactSum(values) {
  const scale = 1000;
  const total = values.reduce((acc, v) => acc + Math.round(v * scale), 0);
  const whole = total / scale;
  return Number.isInteger(whole) ? String(whole) : String(whole);
}

function buildAnswerKey() {
  const byUnit = new Map();
  const excluded = [];
  for (const r of RECIPES) {
    const p = `recipes/${slug(r.name)}.md`;
    // G3, stated as the design states it: a MISSING unit excludes the row
    // from every total. It is not an error and not a zero.
    if (r.weight_unit === null) { excluded.push(p); continue; }
    if (!byUnit.has(r.weight_unit)) byUnit.set(r.weight_unit, []);
    byUnit.get(r.weight_unit).push({ path: p, weight: r.weight });
  }

  // G2: ONE entry per unit value. There is deliberately no combined key.
  const weight_totals = [...byUnit.entries()]
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(([unit, rows]) => ({
      unit,
      op: 'sum',
      value: exactSum(rows.map((r) => r.weight)),
      count: rows.length,
    }));

  const servingsByPortion = new Map();
  for (const r of RECIPES) {
    if (!r.portion_type) continue;
    if (!servingsByPortion.has(r.portion_type)) servingsByPortion.set(r.portion_type, []);
    servingsByPortion.get(r.portion_type).push(r.servings);
  }
  const servings_totals = [...servingsByPortion.entries()]
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(([unit, vals]) => ({
      unit, op: 'sum', value: exactSum(vals), count: vals.length,
    }));

  const cuisines = [...new Set(RECIPES.map((r) => r.cuisine))].sort();
  const byCuisine = Object.fromEntries(
    cuisines.map((c) => [c, RECIPES.filter((r) => r.cuisine === c).length]));

  return {
    generated_by: 'scripts/uat/gen-recipes-vault.mjs',
    design: 'view-kinds-design-2026-09-03 §8 item 5 (UAT vault B)',
    domain: 'recipes — deliberately non-financial; no currency exists in this vault',
    counts: { recipe: RECIPES.length, technique: TECHNIQUES.length },

    // The killer assertion's ground truth (§3 G2 + G3).
    weight: {
      unit_property: 'weight_unit',
      // One entry per unit. A correct server emits exactly these three and
      // NOTHING that sums across them.
      totals: weight_totals,
      excluded_count: excluded.length,
      excluded_paths: excluded.sort(),
      forbidden_combined_value: exactSum(
        RECIPES.filter((r) => r.weight_unit !== null).map((r) => r.weight)),
      forbidden_combined_note:
        'The value a G2 violation would print. It must appear NOWHERE in a view result. '
        + 'Recorded so the check is an equality against a known wrong number rather than '
        + 'a guess about what wrongness would look like.',
    },

    servings: { unit_property: 'portion_type', totals: servings_totals },

    // The availability block's expected answer (§2.3), for the discovery test.
    expected_kind_availability: {
      recipe: {
        available: ['table', 'list', 'board', 'calendar', 'summary', 'trend', 'breakdown'],
        unavailable: { tiles: 'no image-capable property type exists yet' },
      },
      technique: {
        available: ['table', 'list', 'calendar'],
        unavailable: {
          tiles: 'no image-capable property type exists yet',
          board: 'no enum property',
          summary: 'no number property',
          trend: 'no number property',
          breakdown: 'no number property',
        },
      },
    },

    board: { property: 'cuisine', values: cuisines, counts: byCuisine },
    g4_text_that_parses: { property: 'difficulty', declared_type: 'text' },
  };
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

function main() {
  const args = process.argv.slice(2);
  const outIdx = args.indexOf('--out');
  if (outIdx === -1 || !args[outIdx + 1]) {
    process.stderr.write(
      'gen-recipes-vault.mjs — build UAT Vault B (recipes) and its answer key.\n\n'
      + '  --out <dir>   output directory; <dir>/vault is the vault root\n'
      + '  --force       replace <dir> if it exists\n');
    process.exit(2);
  }
  const outDir = path.resolve(args[outIdx + 1]);
  const force = args.includes('--force');

  if (fs.existsSync(outDir)) {
    if (!force) throw new Error(`${outDir} already exists; pass --force to replace it`);
    fs.rmSync(outDir, { recursive: true, force: true });
  }
  const vaultDir = path.join(outDir, 'vault');
  fs.mkdirSync(path.join(vaultDir, MARKER_DIR_NAME, RECORDS_DIR_NAME), { recursive: true });
  fs.writeFileSync(
    path.join(vaultDir, MARKER_DIR_NAME, MARKER_FILE_NAME),
    JSON.stringify({ display_name: 'UAT Vault B — Recipes' }, null, 2) + '\n');

  for (const s of SCHEMAS) {
    fs.writeFileSync(
      path.join(vaultDir, MARKER_DIR_NAME, RECORDS_DIR_NAME, `${s.type}.yaml`),
      emitSchemaYAML(s));
  }

  fs.mkdirSync(path.join(vaultDir, 'recipes'), { recursive: true });
  for (const r of RECIPES) {
    const { name, ...rest } = r;
    fs.writeFileSync(
      path.join(vaultDir, 'recipes', `${slug(name)}.md`),
      renderNote('recipe', { name, ...rest },
        `## ${name}\n\nA ${r.cuisine} dish built around ${r.main_ingredient}.`));
  }
  fs.mkdirSync(path.join(vaultDir, 'techniques'), { recursive: true });
  for (const t of TECHNIQUES) {
    const { name, ...rest } = t;
    fs.writeFileSync(
      path.join(vaultDir, 'techniques', `${slug(name)}.md`),
      renderNote('technique', { name, ...rest }, `## ${name}\n\n${t.notes}.`));
  }

  const key = buildAnswerKey();
  fs.writeFileSync(path.join(outDir, 'answer-key.json'), JSON.stringify(key, null, 2) + '\n');

  process.stdout.write(
    `wrote ${RECIPES.length} recipe + ${TECHNIQUES.length} technique notes to ${vaultDir}\n`
    + `answer key: ${path.join(outDir, 'answer-key.json')}\n`
    + `weight totals (G2, one per unit): ${key.weight.totals.map((t) => `${t.value} ${t.unit} (n=${t.count})`).join(', ')}\n`
    + `G3 excluded rows: ${key.weight.excluded_count}\n`
    + `a combined figure would read ${key.weight.forbidden_combined_value} — it must never appear\n`);
}

main();
