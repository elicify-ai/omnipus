#!/usr/bin/env node
// Omnipus — synthetic knowledge-vault corpus generator + answer key.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// docs/internal/uat/knowledge-tools-uat-plan.md §3: "Search accuracy needs
// KNOWN answers. A real vault cannot provide them: no one can say with
// certainty that exactly 7 notes are about a topic."
//
// So this builds a vault whose answers are known BY CONSTRUCTION, and writes
// the answer key at the same time from the same in-memory model. §1.1's
// false-green rule forbids ever deriving the key from a run's output: the key
// here is computed from the record model that produced the files, before any
// tool has seen them.
//
// Everything is seeded. A fixed --seed produces a byte-identical corpus, so two
// runs are comparable and a regression is a real difference rather than noise.
// There is no unseeded randomness anywhere in this file — no Math.random(), no
// Date.now(), no filesystem-order iteration.
//
// Node built-ins only. This repo forbids new runtime dependencies (CLAUDE.md
// Hard Constraint #1), and a UAT fixture generator is not the place to make an
// exception.
// ---------------------------------------------------------------------------

import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

// ---------------------------------------------------------------------------
// Vault layout constants — read out of the Go source, not guessed.
//
//   pkg/knowledge/marker.go   MarkerDirName  = ".omnipus-vault"
//                             markerFileName = "vault.json"
//   pkg/records/schema.go     RecordsDirName = "records"
//                             SchemaDir(root) = <root>/.omnipus-vault/records
// ---------------------------------------------------------------------------
const MARKER_DIR_NAME = '.omnipus-vault';
const MARKER_FILE_NAME = 'vault.json';
const RECORDS_DIR_NAME = 'records';

// SUPPORTED_SCHEMA_VERSION mirrors records.SupportedSchemaVersion. A schema
// file without it is rejected outright (FR-002), so every schema written here
// carries it.
const SUPPORTED_SCHEMA_VERSION = 1;

// PROPERTY_TYPES mirrors records.PropertyTypes — the CLOSED set of eight
// (FR-004 as amended by FR-004c). The plan's §3 "type coverage" bullet is
// graded against this list: each one must appear scalar, many, and empty.
const PROPERTY_TYPES = ['text', 'enum', 'relation', 'date', 'integer', 'decimal', 'person', 'checkbox'];

// ---------------------------------------------------------------------------
// Deterministic PRNG — mulberry32.
//
// Small, well-known, and fully determined by a 32-bit seed. Every draw in this
// file comes from one instance consumed in a strictly sequential order, which
// is what makes "same seed => same bytes" true rather than hoped for.
// ---------------------------------------------------------------------------
function mulberry32(seed) {
  let a = seed >>> 0;
  return function next() {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function makeRng(seed) {
  const next = mulberry32(seed);
  return {
    float: () => next(),
    int: (n) => Math.floor(next() * n),
    // intBetween is inclusive on both ends.
    intBetween: (lo, hi) => lo + Math.floor(next() * (hi - lo + 1)),
    pick: (arr) => arr[Math.floor(next() * arr.length)],
    chance: (p) => next() < p,
    // pickSome returns between min and max distinct members, in array order,
    // so the result is stable and never depends on Set iteration.
    pickSome: (arr, min, max) => {
      const want = min + Math.floor(next() * (max - min + 1));
      const chosen = [];
      for (let i = 0; i < arr.length && chosen.length < want; i++) {
        const remaining = arr.length - i;
        const needed = want - chosen.length;
        if (next() < needed / remaining) chosen.push(arr[i]);
      }
      return chosen;
    },
  };
}

// ---------------------------------------------------------------------------
// Reserved vocabulary — the planted terms.
//
// These words appear ONLY in notes this generator deliberately plants. Filler
// notes are checked against this list before anything is written: a filler that
// accidentally contained "orbital" would silently become an 18th distractor and
// quietly wreck the precision measurement it was supposed to leave alone.
// ---------------------------------------------------------------------------
const RESERVED_BODY_TERMS = [
  'quenlaris',   // SC-01 term
  'quenlarium',  // SC-01 near-token distractor
  'parathon',    // SC-03 code-fence-only term
  'vorlex',      // SC-04 property-scoped term
  'orbital',     // SC-02 phrase half
  'cache',       // SC-02 phrase half
  'thessaly',    // SC-17 invalid-note-only term
  'churned',     // SC-05: the WORD in prose, as opposed to the enum VALUE
];

// The frontmatter scope is the body scope MINUS `churned`, and the exception is
// the point rather than a loophole: `churned` is a declared value of
// company.status, so filler records must be free to hold it — SC-05's answer
// set is exactly the records that do. What must never happen is a filler note
// using the word in PROSE, because that is what SC-05's distractors are for.
// The two scopes are separate lists so this distinction is visible instead of
// being buried in a special case inside the checker.
const RESERVED_FRONTMATTER_TERMS = RESERVED_BODY_TERMS.filter((t) => t !== 'churned');

// ---------------------------------------------------------------------------
// Filler vocabulary — deliberately dull, and disjoint from RESERVED_TERMS.
// ---------------------------------------------------------------------------
const FILLER_NOUNS = [
  'pipeline', 'ledger', 'dispatch', 'rollup', 'manifest', 'quota', 'handover',
  'invoice', 'roster', 'digest', 'backlog', 'schedule', 'estimate', 'briefing',
  'shipment', 'contract', 'retainer', 'workshop', 'appraisal', 'forecast',
];
const FILLER_VERBS = [
  'reviewed', 'approved', 'deferred', 'consolidated', 'restated', 'audited',
  'circulated', 'scheduled', 'reconciled', 'annotated', 'escalated', 'archived',
];
const FILLER_ADJS = [
  'quarterly', 'provisional', 'regional', 'inbound', 'downstream', 'shared',
  'nominal', 'seasonal', 'interim', 'joint', 'residual', 'standing',
];
const FILLER_COMPANY_STEMS = [
  'Halbern', 'Merridew', 'Cottrell', 'Ashgrove', 'Pendleton', 'Quillane',
  'Rothsey', 'Skelmore', 'Trenholme', 'Withersby', 'Bramlow', 'Draycott',
  'Ferrisham', 'Godwyn', 'Harlowe', 'Ingleby', 'Keswick', 'Lambourn',
];
const FILLER_COMPANY_SUFFIXES = ['Works', 'Holdings', 'Partners', 'Systems', 'Group', 'Labs', 'Trading', 'Foundry'];
const FILLER_GIVEN = [
  'Ada', 'Bram', 'Cleo', 'Dara', 'Emrys', 'Fenna', 'Goran', 'Hesper', 'Ilse',
  'Jorun', 'Kestrel', 'Linnea', 'Morven', 'Nessa', 'Orin', 'Perrin', 'Quilla', 'Rowen',
];
const FILLER_FAMILY = [
  'Aldwych', 'Bexley', 'Carrow', 'Denholm', 'Eastwick', 'Fairhurst', 'Garrow',
  'Hollis', 'Inchbold', 'Jarrow', 'Kellaway', 'Loxley', 'Mardale', 'Norbury',
  'Otterby', 'Prendergast', 'Ravenhill', 'Standish',
];
// PROBE_ANCHOR / PROBE_PERSON_TARGET are the link targets the probe notes point
// at, and the targets SC-14 / SC-15 query for. They are their own names rather
// than reused planted or filler names so that a probe note can never become an
// accidental distractor for a scenario it has nothing to do with.
const PROBE_ANCHOR = 'Probe Anchor Alpha';
const PROBE_PERSON_TARGET = 'Probe Person Two';

const FILLER_PROJECT_STEMS = [
  'Silverpine', 'Blackthorn', 'Whitecliff', 'Redgate', 'Greenhalgh', 'Bluewater',
  'Amberley', 'Copperfield', 'Ironbridge', 'Stonegate', 'Elmhurst', 'Foxglove',
];

// ---------------------------------------------------------------------------
// Record-type schemas.
//
// One source of truth: these objects are serialised to YAML for the vault AND
// walked to build note frontmatter AND walked again to tally the answer key's
// per-property-type counts. They cannot drift from each other.
//
// Constraints taken from pkg/records/schema.go::Property.finalize:
//   - a `relation` MUST declare `to:` (person may, and one here does not, to
//     exercise the "link shape only" path);
//   - `values` is enum-only, and an enum must declare at least one;
//   - `unit` is legal only on integer/decimal.
// ---------------------------------------------------------------------------
const SCHEMAS = [
  {
    type: 'company',
    label: 'Company',
    prefix: 'CO',
    dir: 'companies',
    properties: [
      { name: 'name', type: 'text', required: true },
      { name: 'aliases', type: 'text', many: true },
      { name: 'summary', type: 'text' },
      { name: 'status', type: 'enum', values: ['prospect', 'active', 'churned'] },
      { name: 'industry', type: 'enum', many: true, values: ['saas', 'fintech', 'logistics', 'healthcare'] },
      { name: 'founded', type: 'date' },
      { name: 'headcount', type: 'integer' },
      { name: 'arr', type: 'decimal', unit: 'USD' },
      { name: 'quarterly_arr', type: 'decimal', many: true, unit: 'USD' },
      { name: 'primary_contact', type: 'person', to: 'person' },
      { name: 'is_customer', type: 'checkbox' },
      { name: 'related_projects', type: 'relation', many: true, to: 'project' },
    ],
  },
  {
    type: 'project',
    label: 'Project',
    prefix: 'PR',
    dir: 'projects',
    properties: [
      { name: 'title', type: 'text', required: true },
      { name: 'stage', type: 'enum', values: ['1-scoping', '2-build', '3-review', '4-done'] },
      { name: 'owner', type: 'person', to: 'person' },
      { name: 'contributors', type: 'person', many: true, to: 'person' },
      { name: 'start', type: 'date' },
      { name: 'milestones', type: 'date', many: true },
      { name: 'budget', type: 'decimal', unit: 'USD' },
      { name: 'priority', type: 'integer' },
      { name: 'sprint_numbers', type: 'integer', many: true },
      { name: 'gate_approvals', type: 'checkbox', many: true },
      { name: 'archived', type: 'checkbox' },
      { name: 'company', type: 'relation', to: 'company' },
    ],
  },
  {
    type: 'person',
    label: 'Person',
    prefix: 'PE',
    dir: 'people',
    properties: [
      { name: 'full_name', type: 'text', required: true },
      { name: 'email', type: 'text' },
      { name: 'skills', type: 'text', many: true },
      { name: 'team', type: 'enum', values: ['engineering', 'research', 'operations', 'sales'] },
      // No `to:` here on purpose — records.TypePerson documents that with no
      // target declared only the LINK SHAPE is validated, and that path needs
      // a corpus that exercises it.
      { name: 'manager', type: 'person' },
      { name: 'joined', type: 'date' },
      { name: 'seniority', type: 'integer' },
      { name: 'active', type: 'checkbox' },
      { name: 'projects', type: 'relation', many: true, to: 'project' },
    ],
  },
  {
    type: 'meeting',
    label: 'Meeting',
    prefix: 'ME',
    dir: 'meetings',
    properties: [
      { name: 'subject', type: 'text', required: true },
      { name: 'kind', type: 'enum', values: ['standup', 'review', 'retro', 'interview'] },
      { name: 'occurred', type: 'date' },
      { name: 'attendees', type: 'person', many: true, to: 'person' },
      { name: 'about', type: 'relation', many: true, to: 'project' },
      { name: 'duration_minutes', type: 'integer' },
      { name: 'cost', type: 'decimal', unit: 'USD' },
      { name: 'recorded', type: 'checkbox' },
    ],
  },
  {
    // `probe` exists so the plan's type-coverage bullet is provable rather than
    // argued: every one of the eight property types is declared here both
    // scalar and `many`, and the probe notes below cover present / explicitly
    // empty / absent for all sixteen. The four business types above give the
    // realistic distribution; this one gives the guarantee.
    type: 'probe',
    label: 'Type Probe',
    prefix: 'PB',
    dir: 'probes',
    properties: [
      { name: 'title', type: 'text', required: true },
      { name: 'probe_text', type: 'text' },
      { name: 'probe_text_many', type: 'text', many: true },
      { name: 'probe_enum', type: 'enum', values: ['alpha', 'beta', 'gamma'] },
      { name: 'probe_enum_many', type: 'enum', many: true, values: ['alpha', 'beta', 'gamma'] },
      { name: 'probe_relation', type: 'relation', to: 'company' },
      { name: 'probe_relation_many', type: 'relation', many: true, to: 'company' },
      { name: 'probe_date', type: 'date' },
      { name: 'probe_date_many', type: 'date', many: true },
      { name: 'probe_integer', type: 'integer' },
      { name: 'probe_integer_many', type: 'integer', many: true },
      { name: 'probe_decimal', type: 'decimal', unit: 'USD' },
      { name: 'probe_decimal_many', type: 'decimal', many: true, unit: 'USD' },
      { name: 'probe_person', type: 'person', to: 'person' },
      { name: 'probe_person_many', type: 'person', many: true, to: 'person' },
      { name: 'probe_checkbox', type: 'checkbox' },
      { name: 'probe_checkbox_many', type: 'checkbox', many: true },
    ],
  },
];

const SCHEMA_BY_TYPE = new Map(SCHEMAS.map((s) => [s.type, s]));

// Bucket sizes per tier. `notes` holds ORDINARY notes — no declared record
// type, or a type no schema declares — because FR-005 says those are the
// majority of any real vault and a corpus without them measures a vault nobody
// has.
const TIERS = {
  small: { notes: 55, companies: 38, projects: 35, people: 30, meetings: 30, probes: 12 },
  large: { notes: 600, companies: 400, projects: 380, people: 320, meetings: 288, probes: 12 },
};

// ---------------------------------------------------------------------------
// YAML emission
//
// Hand-rolled because the repo forbids new dependencies. It only has to emit
// the shapes this generator produces, and every one of them is verified to
// re-parse by the --verify pass (and, in CI-for-humans terms, by whatever YAML
// parser the reviewer points at the output).
// ---------------------------------------------------------------------------

function yamlQuote(s) {
  return '"' + String(s).replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
}

// yamlValueFor renders ONE scalar for a declared property type.
//   text / enum / relation / person -> double-quoted (D5.1 wants a relation
//                                      stored as a QUOTED wikilink)
//   date / integer / decimal / checkbox -> bare, which is how an operator
//                                      writes them and how a vault reads.
function yamlValueFor(type, raw) {
  switch (type) {
    case 'text':
    case 'enum':
    case 'relation':
    case 'person':
      return yamlQuote(raw);
    default:
      return String(raw);
  }
}

function emitSchemaYAML(schema) {
  const lines = [];
  lines.push('# Generated by scripts/uat/gen-knowledge-corpus.mjs — do not edit by hand.');
  lines.push(`schema_version: ${SUPPORTED_SCHEMA_VERSION}`);
  lines.push(`type: ${schema.type}`);
  lines.push(`label: ${yamlQuote(schema.label)}`);
  lines.push('identity:');
  lines.push(`  prefix: ${schema.prefix}`);
  lines.push('properties:');
  for (const p of schema.properties) {
    lines.push(`  ${p.name}:`);
    lines.push(`    type: ${p.type}`);
    if (p.many) lines.push('    many: true');
    if (p.required) lines.push('    required: true');
    if (p.to) lines.push(`    to: ${p.to}`);
    if (p.unit) lines.push(`    unit: ${yamlQuote(p.unit)}`);
    if (p.values) {
      lines.push('    values:');
      for (const v of p.values) lines.push(`      - ${yamlQuote(v)}`);
    }
  }
  return lines.join('\n') + '\n';
}

// ---------------------------------------------------------------------------
// The note model
//
// A note carries its frontmatter as an ORDERED list of entries, each tagged
// with the state the answer key needs to reason about:
//
//   present         one or more conforming values
//   empty_scalar    `key:` with nothing after it — FR-007's explicit null,
//                   which records treats as ABSENT
//   empty_list      `key: []`
//   nonconforming   something is written and it does not conform
//
// The state is recorded AT WRITE TIME, from the generator's own intent, rather
// than re-derived by parsing the file back. That is deliberate: re-parsing
// would make the key a measurement of this generator's parser, and the key has
// to be independent of any parser at all.
// ---------------------------------------------------------------------------

function note(opts) {
  return {
    path: opts.path,              // vault-relative, forward slashes
    recordType: opts.recordType,  // null for an ordinary note
    entries: opts.entries || [],  // [{key, type, many, state, values, rawLines?}]
    extraFrontmatter: opts.extraFrontmatter || [], // raw "k: v" lines (type/id/tags)
    body: opts.body,
    planted: opts.planted || null,
    invalid: opts.invalid || null,
  };
}

function entry(key, type, many, state, values) {
  return { key, type, many, state, values: values || [] };
}

function renderNote(n) {
  const fm = [];
  for (const raw of n.extraFrontmatter) fm.push(raw);
  for (const e of n.entries) {
    // modelOnly entries describe a property whose YAML was already written by
    // hand into extraFrontmatter (the deliberately-invalid notes). They exist
    // so the answer key can classify the property's STATE; emitting them would
    // write the key twice.
    if (e.modelOnly) continue;
    if (e.rawLines) {
      for (const l of e.rawLines) fm.push(l);
      continue;
    }
    if (e.state === 'empty_scalar') {
      fm.push(`${e.key}:`);
    } else if (e.state === 'empty_list') {
      fm.push(`${e.key}: []`);
    } else if (e.many) {
      fm.push(`${e.key}:`);
      for (const v of e.values) fm.push(`  - ${yamlValueFor(e.type, v)}`);
    } else {
      fm.push(`${e.key}: ${yamlValueFor(e.type, e.values[0])}`);
    }
  }
  return `---\n${fm.join('\n')}\n---\n\n${n.body}`;
}

// ---------------------------------------------------------------------------
// Body helpers
// ---------------------------------------------------------------------------

function fillerSentence(rng) {
  return `The ${rng.pick(FILLER_ADJS)} ${rng.pick(FILLER_NOUNS)} was ${rng.pick(FILLER_VERBS)} against the ${rng.pick(FILLER_ADJS)} ${rng.pick(FILLER_NOUNS)}.`;
}

function fillerParagraph(rng, sentences) {
  const out = [];
  for (let i = 0; i < sentences; i++) out.push(fillerSentence(rng));
  return out.join(' ');
}

function fillerCodeFence(rng) {
  return ['```sh', `# ${rng.pick(FILLER_VERBS)} the ${rng.pick(FILLER_NOUNS)}`, `run --${rng.pick(FILLER_ADJS)} ${rng.pick(FILLER_NOUNS)}`, '```'].join('\n');
}

function fillerBody(rng, heading) {
  const parts = [`# ${heading}`, ''];
  const paras = rng.intBetween(2, 4);
  for (let i = 0; i < paras; i++) {
    parts.push(fillerParagraph(rng, rng.intBetween(2, 4)));
    parts.push('');
  }
  if (rng.chance(0.25)) {
    parts.push(fillerCodeFence(rng));
    parts.push('');
  }
  return parts.join('\n');
}

// ---------------------------------------------------------------------------
// Decimal handling — exact, no floats.
//
// Every decimal this generator writes has exactly two fractional digits, and
// every comparison the answer key makes runs over BigInt cents. pkg/records
// went to considerable trouble to keep floats out of the value path; a key
// built on `parseFloat` would be scoring an exactness guarantee with an
// inexact instrument.
// ---------------------------------------------------------------------------
function centsOf(decimalString) {
  const s = String(decimalString);
  const neg = s.startsWith('-');
  const body = neg ? s.slice(1) : s;
  const [ip, fp = ''] = body.split('.');
  const frac = (fp + '00').slice(0, 2);
  const v = BigInt(ip) * 100n + BigInt(frac);
  return neg ? -v : v;
}

function decimalFromCents(cents) {
  const neg = cents < 0n;
  const abs = neg ? -cents : cents;
  const ip = abs / 100n;
  const fp = (abs % 100n).toString().padStart(2, '0');
  return `${neg ? '-' : ''}${ip}.${fp}`;
}

function isoDate(rng, startYear, endYear) {
  const y = rng.intBetween(startYear, endYear);
  const m = rng.intBetween(1, 12);
  const d = rng.intBetween(1, 28); // 28 keeps every month legal without a calendar
  return `${y}-${String(m).padStart(2, '0')}-${String(d).padStart(2, '0')}`;
}

function slugify(s) {
  return String(s).toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 48);
}

// ---------------------------------------------------------------------------
// Corpus construction
// ---------------------------------------------------------------------------

function buildCorpus(tier, seed) {
  const rng = makeRng(seed);
  const sizes = TIERS[tier];
  const notes = [];
  const scenarios = [];

  // Counters used for stable, zero-padded filenames per bucket.
  const seq = { notes: 0, companies: 0, projects: 0, people: 0, meetings: 0, probes: 0 };
  // label -> file stem, so a wikilink can be written in the ONE form that
  // resolves. Omnipus resolves `[[...]]` by FILENAME BASENAME only
  // (knowledge/links.go::NoteIndex.Resolve indexes path.Base and its stem and
  // nothing else) — never by a `name:` property, an `id:`, or a title.
  //
  // An earlier version emitted `[[${label}]]` while naming the file
  // `<prefix>-<seq>-<slug(label)>.md`, so EVERY filler relation dangled by
  // construction. Not cosmetic: the corpus then exercised none of relation
  // resolution, backlinks, or the rename cascade's frontmatter rewriting while
  // appearing to, and a UAT run mistook the resulting honest "0 backlinks" for
  // link corruption.
  const stemByLabel = new Map();
  const nextPath = (bucket, label) => {
    seq[bucket] += 1;
    const prefix = { notes: 'n', companies: 'co', projects: 'pr', people: 'pe', meetings: 'me', probes: 'pb' }[bucket];
    const stem = `${prefix}-${String(seq[bucket]).padStart(4, '0')}-${slugify(label)}`;
    // First writer wins: two notes sharing a label make a link spelling that
    // label ambiguous anyway, so pick one and stay deterministic.
    if (!stemByLabel.has(label)) stemByLabel.set(label, stem);
    return `${bucket}/${stem}.md`;
  };

  // A stable pool of link targets so relations point at plausible names.
  // Two pools, deliberately. `*Names` is every name in the corpus, used for
  // reporting; `filler*Names` is what a FILLER relation is allowed to point at.
  // Letting fillers link to the planted names put "[[Vorlex Orbital]]" into the
  // frontmatter of three filler projects — harmless for the property-scoped
  // scenarios, which are typed, but a contamination the corpus should not have
  // to reason about at all.
  const companyNames = [];
  const personNames = [];
  const projectNames = [];
  const fillerCompanyNames = [];
  const fillerPersonNames = [];
  const fillerProjectNames = [];

  // -------------------------------------------------------------------------
  // 1. PLANTED SEARCH NOTES — fixed, seed-independent, tier-independent.
  //
  // Seed-independence is a deliberate design choice, recorded in the key as
  // `seed_scope`. It means the planted answer sets are IDENTICAL at 200 notes
  // and at 2,000 notes, which is what makes the plan's B-01 ("latency vs corpus
  // size") a comparison of the same question at two scales rather than two
  // different questions.
  // -------------------------------------------------------------------------

  const plantedNotePaths = {
    sc01_match: [], sc01_codefence: [], sc01_neartoken: [],
    sc02_match: [], sc02_bagofwords: [],
    sc03_codefence: [],
    sc04_name: [], sc04_alias: [], sc04_body: [],
    sc05_bodyword: [],
  };

  // -- SC-01: an exact term in prose ---------------------------------------
  const SC01_MATCH_SUBJECTS = [
    'Quenlaris migration kickoff',
    'Quenlaris rollout risks',
    'Quenlaris data retention',
    'Quenlaris runbook review',
    'Quenlaris cutover window',
    'Quenlaris post-mortem',
  ];
  for (let i = 0; i < SC01_MATCH_SUBJECTS.length; i++) {
    const subject = SC01_MATCH_SUBJECTS[i];
    const p = nextPath('notes', subject);
    plantedNotePaths.sc01_match.push(p);
    notes.push(note({
      path: p,
      recordType: null,
      extraFrontmatter: ['title: ' + yamlQuote(subject), 'tags:', '  - "planted"'],
      body: [
        `# ${subject}`, '',
        `The quenlaris programme moved a step forward this week. Ownership of quenlaris sits with the platform group until the handover is signed.`,
        '',
        `A second reading of the quenlaris plan is scheduled for the following month.`,
        '',
      ].join('\n'),
      planted: { scenario: 'SC-01', role: 'match' },
    }));
  }

  for (let i = 0; i < 3; i++) {
    const subject = `Deployment snippet ${i + 1}`;
    const p = nextPath('notes', subject);
    plantedNotePaths.sc01_codefence.push(p);
    notes.push(note({
      path: p,
      recordType: null,
      extraFrontmatter: ['title: ' + yamlQuote(subject), 'tags:', '  - "planted"'],
      body: [
        `# ${subject}`, '',
        'The command below is copied verbatim from an operator transcript. Nothing in this note is about the subject the identifier names; it is a shell invocation.',
        '',
        '```sh',
        'export SERVICE_NAME=quenlaris',
        'omnipus deploy --service "$SERVICE_NAME" --wait',
        '```',
        '',
        'The surrounding prose deliberately avoids the identifier so that a match here can only have come from inside the fence.',
        '',
      ].join('\n'),
      planted: { scenario: 'SC-01', role: 'distractor', kind: 'code_fence' },
    }));
  }

  for (let i = 0; i < 3; i++) {
    const subject = `Quenlarium supply note ${i + 1}`;
    const p = nextPath('notes', subject);
    plantedNotePaths.sc01_neartoken.push(p);
    notes.push(note({
      path: p,
      recordType: null,
      extraFrontmatter: ['title: ' + yamlQuote(subject), 'tags:', '  - "planted"'],
      body: [
        `# ${subject}`, '',
        'Quenlarium is a mineral, not a programme. It shares a prefix with the identifier in the neighbouring notes and means something entirely different.',
        '',
        'A search that stems or prefix-matches will return this note. An exact-term search must not.',
        '',
      ].join('\n'),
      planted: { scenario: 'SC-01', role: 'distractor', kind: 'near_token' },
    }));
  }

  // -- SC-02: a phrase whose two words also occur apart --------------------
  for (let i = 0; i < 5; i++) {
    const subject = `Orbital cache design ${i + 1}`;
    const p = nextPath('notes', subject);
    plantedNotePaths.sc02_match.push(p);
    notes.push(note({
      path: p,
      recordType: null,
      extraFrontmatter: ['title: ' + yamlQuote(subject), 'tags:', '  - "planted"'],
      body: [
        `# ${subject}`, '',
        'The orbital cache is a named subsystem. This note discusses the orbital cache and its eviction policy in that sense and no other.',
        '',
        'Every occurrence of the phrase here is the subsystem.',
        '',
      ].join('\n'),
      planted: { scenario: 'SC-02', role: 'match' },
    }));
  }

  const SC02_BAG = [
    ['Orbital mechanics reading list', 'The orbital period of the smaller body was recalculated. Separately, a cache of spare parts was found in the eastern store.'],
    ['Spare parts inventory', 'A cache of manuals turned up during the audit. The orbital diagram on the wall was unrelated and is being returned.'],
    ['Two unrelated paragraphs', 'Orbital insertion is discussed in the first half of the report.\n\nA cache of correspondence is discussed in the second half. The two halves share no subject.'],
    ['Glossary fragment', 'Orbital: relating to an orbit. Cache: a hidden store. The two entries are adjacent alphabetically and mean nothing together.'],
    ['Meeting overflow', 'The orbital telemetry item ran long. The cache invalidation item was pushed to the following week.'],
    ['Procurement note', 'One line mentions an orbital survey. A later line mentions a cache of batteries. Nothing links them.'],
  ];
  for (const [subject, prose] of SC02_BAG) {
    const p = nextPath('notes', subject);
    plantedNotePaths.sc02_bagofwords.push(p);
    notes.push(note({
      path: p,
      recordType: null,
      extraFrontmatter: ['title: ' + yamlQuote(subject), 'tags:', '  - "planted"'],
      body: [`# ${subject}`, '', prose, ''].join('\n'),
      planted: { scenario: 'SC-02', role: 'distractor', kind: 'bag_of_words' },
    }));
  }

  // -- SC-03: a term that exists ONLY inside fences ------------------------
  for (let i = 0; i < 5; i++) {
    const subject = `Configuration sample ${i + 1}`;
    const p = nextPath('notes', subject);
    plantedNotePaths.sc03_codefence.push(p);
    notes.push(note({
      path: p,
      recordType: null,
      extraFrontmatter: ['title: ' + yamlQuote(subject), 'tags:', '  - "planted"'],
      body: [
        `# ${subject}`, '',
        'A configuration sample follows. The identifier inside it appears nowhere else in this corpus outside a fence.',
        '',
        '```yaml',
        'service: parathon',
        'replicas: 3',
        '```',
        '',
      ].join('\n'),
      planted: { scenario: 'SC-03', role: 'distractor', kind: 'code_fence' },
    }));
  }

  // -------------------------------------------------------------------------
  // 2. PLANTED COMPANY NOTES — property-scoped scenarios.
  // -------------------------------------------------------------------------

  const companySchema = SCHEMA_BY_TYPE.get('company');

  function companyNote(opts) {
    const p = nextPath('companies', opts.name);
    companyNames.push(opts.name);
    const entries = [];
    entries.push(entry('name', 'text', false, 'present', [opts.name]));
    if (opts.aliases) entries.push(entry('aliases', 'text', true, 'present', opts.aliases));
    if (opts.summary) entries.push(entry('summary', 'text', false, 'present', [opts.summary]));
    if (opts.status) entries.push(entry('status', 'enum', false, 'present', [opts.status]));
    if (opts.industry) entries.push(entry('industry', 'enum', true, 'present', opts.industry));
    if (opts.headcount !== undefined) entries.push(entry('headcount', 'integer', false, 'present', [String(opts.headcount)]));
    if (opts.arr !== undefined) entries.push(entry('arr', 'decimal', false, 'present', [opts.arr]));
    if (opts.isCustomer !== undefined) entries.push(entry('is_customer', 'checkbox', false, 'present', [String(opts.isCustomer)]));
    const n = note({
      path: p,
      recordType: 'company',
      extraFrontmatter: ['type: company', `id: CO-${String(seq.companies).padStart(4, '0')}`],
      entries,
      body: opts.body,
      planted: opts.planted,
    });
    notes.push(n);
    return n;
  }

  // SC-04 matches: "Vorlex" in `name`.
  const SC04_NAMES = ['Vorlex Orbital', 'Vorlex Freight', 'Northern Vorlex', 'Vorlex Analytics'];
  for (const nm of SC04_NAMES) {
    const n = companyNote({
      name: nm,
      status: 'active',
      industry: ['saas'],
      headcount: 240,
      arr: '820000.00',
      isCustomer: true,
      body: ['# ' + nm, '', 'A supplier record. The distinguishing token is in the name property, not in this prose.', ''].join('\n'),
      planted: { scenario: 'SC-04', role: 'match' },
    });
    plantedNotePaths.sc04_name.push(n.path);
  }

  // SC-04 distractors: "Vorlex" in `aliases` only.
  for (let i = 0; i < 3; i++) {
    const nm = `Halbern Freight ${i + 1}`;
    const n = companyNote({
      name: nm,
      aliases: ['Vorlex', `Halbern ${i + 1}`],
      status: 'prospect',
      headcount: 90,
      body: ['# ' + nm, '', 'The token appears in an alias, which is a different property from the one the query names.', ''].join('\n'),
      planted: { scenario: 'SC-04', role: 'distractor', kind: 'other_property' },
    });
    plantedNotePaths.sc04_alias.push(n.path);
  }

  // SC-04 distractors: "Vorlex" in the BODY only.
  for (let i = 0; i < 3; i++) {
    const nm = `Merridew Holdings ${i + 1}`;
    const n = companyNote({
      name: nm,
      status: 'prospect',
      headcount: 130,
      body: ['# ' + nm, '', 'This account was once a Vorlex reseller. The token is in the body, not in any property.', ''].join('\n'),
      planted: { scenario: 'SC-04', role: 'distractor', kind: 'body_only' },
    });
    plantedNotePaths.sc04_body.push(n.path);
  }

  // SC-05 distractors: the WORD "churned" in the body, while `status: active`.
  for (let i = 0; i < 3; i++) {
    const nm = `Cottrell Systems ${i + 1}`;
    const n = companyNote({
      name: nm,
      status: 'active',
      headcount: 410,
      arr: '1500000.00',
      isCustomer: true,
      body: ['# ' + nm, '', 'Two of their smaller subsidiaries churned last year; this account itself did not. The status property is the authority, not this sentence.', ''].join('\n'),
      planted: { scenario: 'SC-05', role: 'distractor', kind: 'text_not_property' },
    });
    plantedNotePaths.sc05_bodyword.push(n.path);
  }

  // -------------------------------------------------------------------------
  // 3. DELIBERATELY INVALID NOTES — fixed set, one recorded defect each.
  //
  // Fixed rather than scaled so the small and large tiers are comparable: the
  // plan's Z-03 wants per-type counts to match the key, and a tier-dependent
  // invalid count makes "the corpus loaded" and "the corpus is the same corpus"
  // two different questions.
  //
  // `expected_finding_code` is the records.FindingCode this defect should
  // raise. It is the generator's stated expectation, not a measurement — the
  // grader compares a real run against it, which is the whole point.
  // -------------------------------------------------------------------------

  const INVALID_SPECS = [
    { bucket: 'companies', recordType: 'company', label: 'Ashgrove Trading',
      base: ['type: company', 'name: "Ashgrove Trading"'],
      bad: ['founded: last spring'],
      property: 'founded', declaredType: 'date', writtenValue: 'last spring',
      why: 'prose written into a date property', code: 'not_a_date' },
    { bucket: 'companies', recordType: 'company', label: 'Pendleton Group',
      base: ['type: company', 'name: "Pendleton Group"'],
      bad: ['status: liquidated'],
      property: 'status', declaredType: 'enum', writtenValue: 'liquidated',
      why: 'a value outside the declared enum set [prospect, active, churned]', code: 'enum_value_not_permitted' },
    { bucket: 'companies', recordType: 'company', label: 'Quillane Labs',
      base: ['type: company', 'name: "Quillane Labs"'],
      bad: ['headcount: several hundred'],
      property: 'headcount', declaredType: 'integer', writtenValue: 'several hundred',
      why: 'prose written into an integer property', code: 'not_a_number' },
    { bucket: 'companies', recordType: 'company', label: 'Rothsey Works',
      base: ['type: company', 'name: "Rothsey Works"'],
      bad: ['headcount: 99999999999999999999999'],
      property: 'headcount', declaredType: 'integer', writtenValue: '99999999999999999999999',
      why: 'a whole number outside int64, which FR-013 refuses rather than saturating', code: 'integer_out_of_range' },
    { bucket: 'companies', recordType: 'company', label: 'Skelmore Partners',
      base: ['type: company', 'name: "Skelmore Partners"'],
      bad: ['headcount: 12.5'],
      property: 'headcount', declaredType: 'integer', writtenValue: '12.5',
      why: 'a fractional value in an integer property — a distinct fault from "not a number"', code: 'integer_not_whole' },
    { bucket: 'companies', recordType: 'company', label: 'Trenholme Foundry',
      base: ['type: company'],
      bad: [],
      property: 'name', declaredType: 'text', writtenValue: '(absent)',
      // This one defect is ABSENCE rather than a non-conforming value, and the
      // two are different states in pkg/records (StateAbsent vs
      // StateNonConforming) with different filter behaviour. Recording it as
      // "nonconforming" would have put a note in the wrong bucket of every
      // negation scenario.
      modelState: 'absent',
      why: 'a required property is missing entirely', code: 'missing_required_property' },
    { bucket: 'projects', recordType: 'project', label: 'Silverpine Rebuild',
      base: ['type: project', 'title: "Silverpine Rebuild"'],
      bad: ['company: Acme Limited'],
      property: 'company', declaredType: 'relation', writtenValue: 'Acme Limited',
      why: 'a relation written as bare text instead of a quoted wikilink (D5.1)', code: 'not_a_wikilink' },
    { bucket: 'projects', recordType: 'project', label: 'Blackthorn Audit',
      base: ['type: project', 'title: "Blackthorn Audit"'],
      bad: ['company: |', '  [[Acme]]'],
      property: 'company', declaredType: 'relation', writtenValue: 'a block scalar whose folded text reads [[Acme]]',
      why: 'FR-030a — a block scalar is a multi-line string, not a wikilink, however its folded text reads', code: 'not_a_wikilink' },
    { bucket: 'projects', recordType: 'project', label: 'Whitecliff Pilot',
      base: ['type: project', 'title: "Whitecliff Pilot"'],
      bad: ['start:', '  - 2026-01-05', '  - 2026-02-05'],
      property: 'start', declaredType: 'date', writtenValue: 'a list of two dates',
      why: 'a list written where the schema declares a scalar (FR-006 arity)', code: 'arity_violation' },
    { bucket: 'projects', recordType: 'project', label: 'Redgate Handover',
      base: ['type: project', 'title: "Redgate Handover"'],
      bad: ['milestones: 2026-04-01'],
      property: 'milestones', declaredType: 'date', writtenValue: '2026-04-01',
      why: 'a scalar written where the schema declares a list (FR-006 arity, the other direction)', code: 'arity_violation' },
    { bucket: 'people', recordType: 'person', label: 'Dara Carrow',
      base: ['type: person', 'full_name: "Dara Carrow"'],
      bad: ['active: maybe'],
      property: 'active', declaredType: 'checkbox', writtenValue: 'maybe',
      why: 'a checkbox holding a third spelling; the third state is ABSENCE, not a word', code: 'not_a_boolean' },
    { bucket: 'people', recordType: 'person', label: 'Emrys Denholm',
      base: ['type: person', 'full_name: "Emrys Denholm"'],
      bad: ['manager: Fenna Eastwick'],
      property: 'manager', declaredType: 'person', writtenValue: 'Fenna Eastwick',
      why: 'a person written as bare text instead of a quoted wikilink', code: 'not_a_wikilink' },
    { bucket: 'meetings', recordType: 'meeting', label: 'Quarterly Costing',
      base: ['type: meeting', 'subject: "Quarterly Costing"'],
      bad: ['cost: about twelve hundred'],
      property: 'cost', declaredType: 'decimal', writtenValue: 'about twelve hundred',
      why: 'prose written into a decimal property', code: 'not_a_number' },
    { bucket: 'meetings', recordType: 'meeting', label: 'Interview Debrief',
      base: ['type: meeting', 'subject: "Interview Debrief"'],
      bad: ['kind:', '  note: retro'],
      property: 'kind', declaredType: 'enum', writtenValue: 'a nested mapping',
      why: 'a mapping where a scalar was declared — no property type accepts one', code: 'wrong_shape' },
  ];

  const invalidRecords = [];
  for (const spec of INVALID_SPECS) {
    const p = nextPath(spec.bucket, spec.label);
    const fmLines = spec.base.concat(spec.bad);
    const n = note({
      path: p,
      recordType: spec.recordType,
      extraFrontmatter: fmLines,
      // The defective property is recorded in the MODEL as well as in the
      // rendered YAML, with modelOnly so renderNote does not write it twice.
      // Without it the classifier saw no entry at all and filed the record
      // under ABSENT — which put the `status: liquidated` note into SC-08's
      // must_match and its own must_not_match at the same time. The generator's
      // disjointness self-check is what surfaced that.
      entries: [{
        key: spec.property,
        type: spec.declaredType,
        many: false,
        state: spec.modelState || 'nonconforming',
        values: [],
        modelOnly: true,
      }],
      // SC-17's term lives ONLY here, so "search over a partly-invalid corpus"
      // has a query whose entire answer set is the invalid notes.
      body: [
        `# ${spec.label}`, '',
        `This note is deliberately invalid. The thessaly review flagged it: ${spec.why}.`,
        '',
      ].join('\n'),
      invalid: {
        property: spec.property,
        declared_type: spec.declaredType,
        written_value: spec.writtenValue,
        why: spec.why,
        expected_finding_code: spec.code,
      },
    });
    notes.push(n);
    invalidRecords.push(n);
    if (spec.recordType === 'company') companyNames.push(spec.label);
    if (spec.recordType === 'project') projectNames.push(spec.label);
    if (spec.recordType === 'person') personNames.push(spec.label);
  }

  // -------------------------------------------------------------------------
  // 4. PROBE NOTES — the type-coverage guarantee.
  //
  // Twelve notes: four with every property present, four with every property
  // explicitly EMPTY (`key:` for scalars, `key: []` for lists), four with every
  // optional property ABSENT. Together they give, for all eight property types,
  // a scalar case, a list case, an empty-scalar case, an empty-list case and an
  // absent case — which is the plan's bullet, made checkable.
  // -------------------------------------------------------------------------

  const probeSchema = SCHEMA_BY_TYPE.get('probe');
  const PROBE_ENUMS = ['alpha', 'beta', 'gamma'];

  function probeFilledEntries(i) {
    const out = [entry('title', 'text', false, 'present', [`Probe filled ${i + 1}`])];
    out.push(entry('probe_text', 'text', false, 'present', [`filled ${FILLER_NOUNS[i % FILLER_NOUNS.length]}`]));
    out.push(entry('probe_text_many', 'text', true, 'present', [FILLER_NOUNS[i % FILLER_NOUNS.length], FILLER_NOUNS[(i + 3) % FILLER_NOUNS.length]]));
    out.push(entry('probe_enum', 'enum', false, 'present', [PROBE_ENUMS[i % 3]]));
    out.push(entry('probe_enum_many', 'enum', true, 'present', [PROBE_ENUMS[i % 3], PROBE_ENUMS[(i + 1) % 3]]));
    // Probe link targets are DEDICATED names, never the planted SC-04 token.
    // An earlier draft pointed these at [[Vorlex Orbital]] and the generator's
    // own reserved-vocabulary check caught it: the probe notes had quietly
    // become nine extra SC-04 distractors nobody had accounted for.
    out.push(entry('probe_relation', 'relation', false, 'present', [`[[${PROBE_ANCHOR}]]`]));
    out.push(entry('probe_relation_many', 'relation', true, 'present', ['[[Probe Anchor Beta]]', '[[Probe Anchor Gamma]]']));
    // One probe carries an RFC-3339 INSTANT rather than a bare day, because
    // records.DateValue treats both as the same declared type and a corpus that
    // only ever writes days never exercises the other layout.
    out.push(entry('probe_date', 'date', false, 'present', [i === 0 ? '2026-03-04T09:30:00Z' : `2026-0${(i % 9) + 1}-1${i % 9}`]));
    out.push(entry('probe_date_many', 'date', true, 'present', ['2026-01-15', '2026-06-15']));
    out.push(entry('probe_integer', 'integer', false, 'present', [String(1000 + i)]));
    out.push(entry('probe_integer_many', 'integer', true, 'present', [String(i), String(i + 10)]));
    out.push(entry('probe_decimal', 'decimal', false, 'present', [decimalFromCents(BigInt(100000 + i * 137))]));
    out.push(entry('probe_decimal_many', 'decimal', true, 'present', ['1.05', '2.50']));
    out.push(entry('probe_person', 'person', false, 'present', ['[[Probe Person One]]']));
    // Only the even-indexed probes carry the SC-15 target, so the membership
    // scenario has a real non-empty answer AND a real non-empty complement
    // among notes that all DO write the property. A scenario whose complement
    // is only "notes that left it out" cannot distinguish a working membership
    // test from one that matches every record with any value at all.
    out.push(entry('probe_person_many', 'person', true, 'present',
      i % 2 === 0 ? [`[[${PROBE_PERSON_TARGET}]]`, '[[Probe Person Three]]'] : ['[[Probe Person Three]]', '[[Probe Person Four]]']));
    out.push(entry('probe_checkbox', 'checkbox', false, 'present', [i % 2 === 0 ? 'true' : 'false']));
    out.push(entry('probe_checkbox_many', 'checkbox', true, 'present', ['true', 'false']));
    return out;
  }

  function probeEmptyEntries(i) {
    const out = [entry('title', 'text', false, 'present', [`Probe empty ${i + 1}`])];
    for (const p of probeSchema.properties) {
      if (p.name === 'title') continue;
      out.push(entry(p.name, p.type, !!p.many, p.many ? 'empty_list' : 'empty_scalar', []));
    }
    return out;
  }

  for (let i = 0; i < 4; i++) {
    const label = `Probe filled ${i + 1}`;
    const p = nextPath('probes', label);
    notes.push(note({
      path: p, recordType: 'probe',
      extraFrontmatter: ['type: probe', `id: PB-${String(seq.probes).padStart(4, '0')}`],
      entries: probeFilledEntries(i),
      body: [`# ${label}`, '', 'Every declared property of the probe type carries a conforming value.', ''].join('\n'),
    }));
  }
  for (let i = 0; i < 4; i++) {
    const label = `Probe empty ${i + 1}`;
    const p = nextPath('probes', label);
    notes.push(note({
      path: p, recordType: 'probe',
      extraFrontmatter: ['type: probe', `id: PB-${String(seq.probes).padStart(4, '0')}`],
      entries: probeEmptyEntries(i),
      body: [`# ${label}`, '', 'Every optional property is written but EMPTY — `key:` for a scalar, `key: []` for a list. FR-007 keeps these distinct from absence.', ''].join('\n'),
    }));
  }
  for (let i = 0; i < 4; i++) {
    const label = `Probe absent ${i + 1}`;
    const p = nextPath('probes', label);
    notes.push(note({
      path: p, recordType: 'probe',
      extraFrontmatter: ['type: probe', `id: PB-${String(seq.probes).padStart(4, '0')}`],
      entries: [entry('title', 'text', false, 'present', [label])],
      body: [`# ${label}`, '', 'Only the required property is written. Every other declared property is absent — the third state.', ''].join('\n'),
    }));
  }

  // -------------------------------------------------------------------------
  // 5. FILLER NOTES — seeded, and where the tiers differ.
  // -------------------------------------------------------------------------

  // Name pools first, so relations can point at them.
  const fillerCompanyCount = sizes.companies - seq.companies;
  const fillerProjectCount = sizes.projects - seq.projects;
  const fillerPersonCount = sizes.people - seq.people;
  const fillerMeetingCount = sizes.meetings - seq.meetings;
  const fillerOrdinaryCount = sizes.notes - seq.notes;

  for (const [what, n] of [['companies', fillerCompanyCount], ['projects', fillerProjectCount], ['people', fillerPersonCount], ['meetings', fillerMeetingCount], ['notes', fillerOrdinaryCount]]) {
    if (n < 0) throw new Error(`tier ${tier}: bucket ${what} target is smaller than its fixed planted/invalid content (short by ${-n})`);
  }

  for (let i = 0; i < fillerCompanyCount; i++) {
    const nm = `${FILLER_COMPANY_STEMS[i % FILLER_COMPANY_STEMS.length]} ${FILLER_COMPANY_SUFFIXES[i % FILLER_COMPANY_SUFFIXES.length]} ${i + 1}`;
    companyNames.push(nm); fillerCompanyNames.push(nm);
  }
  for (let i = 0; i < fillerPersonCount; i++) {
    const nm = `${FILLER_GIVEN[i % FILLER_GIVEN.length]} ${FILLER_FAMILY[(i * 7) % FILLER_FAMILY.length]}`;
    personNames.push(nm); fillerPersonNames.push(nm);
  }
  for (let i = 0; i < fillerProjectCount; i++) {
    const nm = `${FILLER_PROJECT_STEMS[i % FILLER_PROJECT_STEMS.length]} ${FILLER_NOUNS[(i * 5) % FILLER_NOUNS.length]} ${i + 1}`;
    projectNames.push(nm); fillerProjectNames.push(nm);
  }

  // Emits the target's FILE STEM — the only form Omnipus resolves. A label
  // with no known stem falls back to the label and therefore dangles; that is
  // what the 'Unknown' case has always meant, and it must not be the norm.
  const linkTo = (pool, i) => {
    const label = pool.length ? pool[i % pool.length] : 'Unknown';
    return `[[${stemByLabel.get(label) ?? label}]]`;
  };

  // valueFor produces ONE conforming value for a declared property.
  function valueFor(prop, rng, idx) {
    switch (prop.type) {
      case 'text': return `${rng.pick(FILLER_ADJS)} ${rng.pick(FILLER_NOUNS)}`;
      case 'enum': return rng.pick(prop.values);
      case 'relation': return linkTo(prop.to === 'company' ? fillerCompanyNames : fillerProjectNames, rng.int(1024) + idx);
      case 'person': return linkTo(fillerPersonNames, rng.int(1024) + idx);
      case 'date': return isoDate(rng, 2023, 2026);
      case 'integer': return String(rng.intBetween(1, 4000));
      case 'decimal': return decimalFromCents(BigInt(rng.intBetween(1, 400000000)));
      case 'checkbox': return rng.chance(0.5) ? 'true' : 'false';
      default: throw new Error(`no value generator for property type ${prop.type}`);
    }
  }

  // fillerEntriesFor walks a schema's properties IN DECLARATION ORDER and picks
  // a state for each. Required properties are always present, because a corpus
  // whose fillers are half-invalid stops being a control group.
  function fillerEntriesFor(schema, rng, idx) {
    const out = [];
    for (const prop of schema.properties) {
      if (prop.required) {
        out.push(entry(prop.name, prop.type, !!prop.many, 'present', [valueFor(prop, rng, idx)]));
        continue;
      }
      const roll = rng.float();
      if (roll < 0.66) {
        const count = prop.many ? rng.intBetween(1, 3) : 1;
        const vals = [];
        for (let k = 0; k < count; k++) vals.push(valueFor(prop, rng, idx + k));
        out.push(entry(prop.name, prop.type, !!prop.many, 'present', vals));
      } else if (roll < 0.84) {
        // absent — the key is simply not written
        continue;
      } else if (roll < 0.92) {
        out.push(entry(prop.name, prop.type, !!prop.many, prop.many ? 'empty_list' : 'empty_scalar', []));
      } else {
        out.push(entry(prop.name, prop.type, !!prop.many, 'empty_scalar', []));
      }
    }
    return out;
  }

  function emitFillerRecords(schema, bucket, count, nameFn) {
    for (let i = 0; i < count; i++) {
      const label = nameFn(i);
      const p = nextPath(bucket, label);
      const entries = fillerEntriesFor(schema, rng, i);
      // Force the required property to the pool name so relations resolve to
      // something a reader can follow.
      const requiredProp = schema.properties.find((x) => x.required);
      if (requiredProp) {
        const e = entries.find((x) => x.key === requiredProp.name);
        if (e) e.values = [label];
      }
      const idField = `id: ${schema.prefix}-${String(seq[bucket]).padStart(4, '0')}`;
      notes.push(note({
        path: p,
        recordType: schema.type,
        extraFrontmatter: [`type: ${schema.type}`, idField],
        entries,
        body: fillerBody(rng, label),
      }));
    }
  }

  emitFillerRecords(companySchema, 'companies', fillerCompanyCount, (i) => fillerCompanyNames[i]);
  emitFillerRecords(SCHEMA_BY_TYPE.get('project'), 'projects', fillerProjectCount, (i) => fillerProjectNames[i]);
  emitFillerRecords(SCHEMA_BY_TYPE.get('person'), 'people', fillerPersonCount, (i) => fillerPersonNames[i]);
  emitFillerRecords(SCHEMA_BY_TYPE.get('meeting'), 'meetings', fillerMeetingCount, (i) => `${FILLER_ADJS[i % FILLER_ADJS.length]} ${FILLER_NOUNS[(i * 3) % FILLER_NOUNS.length]} review ${i + 1}`);

  // Ordinary notes — no `type:` at all for most, an UNDECLARED type for some.
  // FR-005 says both are plain notes and neither is an error; a corpus with
  // only records would never test that.
  for (let i = 0; i < fillerOrdinaryCount; i++) {
    const label = `${FILLER_ADJS[i % FILLER_ADJS.length]} ${FILLER_NOUNS[(i * 11) % FILLER_NOUNS.length]} ${i + 1}`;
    const p = nextPath('notes', label);
    const undeclared = rng.chance(0.3);
    const fm = undeclared
      ? ['type: journal', 'title: ' + yamlQuote(label)]
      : ['title: ' + yamlQuote(label)];
    notes.push(note({
      path: p,
      recordType: null,
      extraFrontmatter: fm,
      entries: [],
      body: fillerBody(rng, label),
    }));
  }

  return { notes, scenarios, plantedNotePaths, invalidRecords, rng, sizes };
}

// ---------------------------------------------------------------------------
// Answer-key computation
//
// Everything below reads the in-memory model, never the filesystem.
// ---------------------------------------------------------------------------

function propOf(n, key) {
  return n.entries.find((e) => e.key === key) || null;
}

// stateOf collapses the model's four write states onto the three states
// pkg/records uses (validate.go's PropertyState):
//   absent          key missing, or written with an explicit null
//   present         one or more conforming values
//   nonconforming   something written that does not conform
// The empty LIST case is deliberately reported separately, because `key: []`
// is a written value of length zero and the two are not obviously the same
// question — the key states which it used so a grader is not left guessing.
function stateOf(n, key) {
  const e = propOf(n, key);
  if (!e) return 'absent';
  if (e.state === 'absent') return 'absent';
  if (e.state === 'empty_scalar') return 'absent';
  if (e.state === 'empty_list') return 'empty_list';
  if (e.state === 'nonconforming') return 'nonconforming';
  return 'present';
}

function valuesOf(n, key) {
  const e = propOf(n, key);
  return e && e.state === 'present' ? e.values : [];
}

function sortPaths(arr) {
  return Array.from(new Set(arr)).sort();
}

function buildScenarios(corpus) {
  const { notes, plantedNotePaths, invalidRecords } = corpus;
  const byType = (t) => notes.filter((n) => n.recordType === t);
  const invalidPaths = sortPaths(invalidRecords.map((n) => n.path));
  const invalidByType = (t) => sortPaths(invalidRecords.filter((n) => n.recordType === t).map((n) => n.path));
  const invalidSet = new Set(invalidPaths);

  // dropInvalid removes the deliberately-invalid notes from an expected MATCH
  // set. Whether a partly-invalid record is returned by a query at all is the
  // open contract question SC-17 exists to settle; until it is settled, a
  // must_match set containing one is a demand this key is not entitled to make.
  // They are still named on each affected scenario, under invalid_in_scope, so
  // nothing is hidden.
  const dropInvalid = (paths) => sortPaths(paths.filter((p) => !invalidSet.has(p)));

  const INVALID_SCOPE_NOTE =
    'These records of the queried type each carry one deliberate schema violation. This key makes NO claim about whether a query returns them — that is the contract SC-17 is written to settle. They are excluded from must_match so a grader is never asked to assert an unsettled rule, and they are listed here so their handling is still reviewed rather than overlooked.';

  const S = [];

  S.push({
    id: 'SC-01',
    plan_ref: 'A-01',
    title: 'Exact-term search for a planted term in prose',
    query: { kind: 'search', text: 'quenlaris' },
    rule: 'A note matches iff the token "quenlaris" appears in its Markdown BODY as prose. Frontmatter is not searched by this scenario and no note carries the token there.',
    policy_dependent: true,
    policy_note: 'The code-fence distractors encode the plan §3 rule that text inside a fenced code block is not prose and must not match. If the product decides otherwise, this scenario is the place that decision becomes visible; it must not be graded as a pass either way by default.',
    must_match: sortPaths(plantedNotePaths.sc01_match),
    must_not_match: [
      { kind: 'code_fence', reason: 'the token appears only inside a fenced code block', paths: sortPaths(plantedNotePaths.sc01_codefence) },
      { kind: 'near_token', reason: '"quenlarium" shares a prefix and is a different word with a different meaning; an exact-term search must not return it', paths: sortPaths(plantedNotePaths.sc01_neartoken) },
    ],
  });

  S.push({
    id: 'SC-02',
    plan_ref: 'A-02',
    title: 'Phrase search where distractors share the vocabulary but not the meaning',
    query: { kind: 'search', text: '"orbital cache"', phrase: true },
    rule: 'A note matches iff the two-word phrase "orbital cache" occurs contiguously in its body. Distractors contain both words, never adjacent, in unrelated senses.',
    policy_dependent: false,
    must_match: sortPaths(plantedNotePaths.sc02_match),
    must_not_match: [
      { kind: 'bag_of_words', reason: 'both query words occur, separately and in unrelated senses; returning these is the precision failure the plan calls out', paths: sortPaths(plantedNotePaths.sc02_bagofwords) },
    ],
  });

  S.push({
    id: 'SC-03',
    plan_ref: 'A-01 / A-02',
    title: 'A term that exists ONLY inside fenced code blocks',
    query: { kind: 'search', text: 'parathon' },
    rule: 'The correct answer is the EMPTY SET: every occurrence of "parathon" in the corpus is inside a fenced code block.',
    policy_dependent: true,
    policy_note: 'This scenario is a direct test of the plan §3 code-fence rule and nothing else. A non-empty result is not automatically a defect — it is a product decision that must be stated. What IS a defect is the decision being unstated.',
    must_match: [],
    must_not_match: [
      { kind: 'code_fence', reason: 'the only occurrences are inside a fence', paths: sortPaths(plantedNotePaths.sc03_codefence) },
    ],
  });

  S.push({
    id: 'SC-04',
    plan_ref: 'A-03',
    title: 'Property-scoped find on a text property (the term also occurs elsewhere)',
    query: { kind: 'find', record_type: 'company', property: 'name', op: 'LIKE', value: '%Vorlex%' },
    rule: 'A record matches iff its `name` property contains "Vorlex". The token also occurs in `aliases` on three records and in the body of three others; neither is the `name` property.',
    policy_dependent: false,
    must_match: dropInvalid(plantedNotePaths.sc04_name),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('company') },
    must_not_match: [
      { kind: 'other_property', reason: 'the token is in `aliases`, a different property from the one queried', paths: sortPaths(plantedNotePaths.sc04_alias) },
      { kind: 'body_only', reason: 'the token is in the Markdown body, not in any property', paths: sortPaths(plantedNotePaths.sc04_body) },
    ],
  });

  // SC-05 / SC-07 / SC-08 all read `company.status`, so classify once.
  const companies = byType('company');
  const statusActive = [];
  const statusOther = [];
  const statusAbsent = [];
  const statusChurned = [];
  for (const n of companies) {
    const st = stateOf(n, 'status');
    if (st === 'present') {
      const v = valuesOf(n, 'status')[0];
      if (v === 'active') statusActive.push(n.path);
      else statusOther.push(n.path);
      if (v === 'churned') statusChurned.push(n.path);
    } else if (st === 'absent' || st === 'empty_list') {
      statusAbsent.push(n.path);
    }
  }
  // The invalid company whose status is `liquidated` is NON-CONFORMING, not
  // absent, and §8 R-4 excludes it from every operator and never re-includes it
  // by negation. It is therefore in none of the three lists above and is named
  // separately in each scenario that touches `status`.
  const statusNonconforming = sortPaths(
    invalidRecords.filter((n) => n.recordType === 'company' && n.invalid.property === 'status').map((n) => n.path)
  );
  // A company that is missing `name` still has no `status` written; it belongs
  // in the absent bucket, and it is there because stateOf saw no entry.

  S.push({
    id: 'SC-05',
    plan_ref: 'A-04',
    title: 'Filter on an enum value that EXISTS',
    query: { kind: 'find', record_type: 'company', property: 'status', op: '=', value: 'churned' },
    rule: 'A record matches iff its `status` property resolves to the declared enum value `churned`. Matching is case-insensitive over the folded value (FR-011a); this corpus writes the declared spelling only.',
    policy_dependent: false,
    must_match: dropInvalid(statusChurned),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('company') },
    must_not_match: [
      { kind: 'text_not_property', reason: 'the word "churned" appears in the body while `status` is `active`; a body hit is not a property match', paths: sortPaths(plantedNotePaths.sc05_bodyword) },
      { kind: 'nonconforming', reason: 'the value written is outside the declared enum, so §8 R-4 excludes the record from every operator', paths: statusNonconforming },
    ],
  });

  S.push({
    id: 'SC-06',
    plan_ref: 'A-05',
    title: 'Filter on an enum value that does NOT exist',
    query: { kind: 'find', record_type: 'company', property: 'status', op: '=', value: 'liquidated' },
    rule: 'REFUSAL EXPECTED. `liquidated` is not a declared value of company.status, so the request must be refused with the legal values named. Silently returning zero rows is the failure the plan calls the worst outcome, because it is indistinguishable from "no matches".',
    policy_dependent: false,
    expect_refusal: true,
    refusal_must_name_values: SCHEMA_BY_TYPE.get('company').properties.find((p) => p.name === 'status').values,
    must_match: [],
    must_not_match: [],
  });

  S.push({
    id: 'SC-07',
    plan_ref: 'A-06',
    title: 'Negation via the <> operator (absence EXCLUDED)',
    query: { kind: 'find', record_type: 'company', property: 'status', op: '<>', value: 'active' },
    rule: 'pkg/records/filter.go §8 R-2: a comparison where either side is absent is FALSE for every operator except IS NULL. So `status <> "active"` does NOT return records with no status. R-4 excludes the non-conforming record and negation never re-includes it.',
    policy_dependent: false,
    must_match: dropInvalid(statusOther),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('company') },
    must_not_match: [
      { kind: 'absent', reason: 'no `status` is written (missing key or explicit null); R-2 makes the comparison false', paths: sortPaths(statusAbsent) },
      { kind: 'nonconforming', reason: 'R-4: excluded and reported, never re-included by negation', paths: statusNonconforming },
      { kind: 'positive_complement', reason: 'these are the records that DO hold `active`', paths: sortPaths(statusActive) },
    ],
  });

  S.push({
    id: 'SC-08',
    plan_ref: 'A-06',
    title: 'Negation via a NOT wrapper (absence INCLUDED)',
    query: { kind: 'find', record_type: 'company', negate: true, property: 'status', op: '=', value: 'active' },
    rule: 'FR-008: a NEGATIVE FILTER includes records where the property is absent — the opposite of SC-07 by design. The pair exists because the two spellings of "not active" have different answers, and a build that gives them the same answer has one of them wrong.',
    policy_dependent: false,
    must_match: dropInvalid(statusOther.concat(statusAbsent)),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('company') },
    must_not_match: [
      { kind: 'nonconforming', reason: 'R-4 again: still excluded, even under negation', paths: statusNonconforming },
      { kind: 'positive_complement', reason: 'these hold `active`', paths: sortPaths(statusActive) },
    ],
    paired_with: 'SC-07',
    pair_note: 'SC-07 and SC-08 must return DIFFERENT sets whenever any record has an absent status. Identical results mean one of the two absence rules is not implemented.',
  });

  // SC-09 — IS NULL on a person-typed property.
  const projects = byType('project');
  const ownerAbsent = [];
  const ownerPresent = [];
  for (const n of projects) {
    const st = stateOf(n, 'owner');
    if (st === 'present') ownerPresent.push(n.path);
    else if (st === 'absent') ownerAbsent.push(n.path);
  }
  S.push({
    id: 'SC-09',
    plan_ref: 'A-03 (person) / A-06',
    title: 'IS NULL over a person property',
    query: { kind: 'find', record_type: 'project', property: 'owner', op: 'IS NULL' },
    rule: 'FR-007 / R-3: a missing key and an explicit `owner:` null are the SAME state — absent — and IS NULL is the one operator absence answers directly. A note whose `owner` holds a non-wikilink is non-conforming, not absent, and is excluded.',
    policy_dependent: false,
    must_match: dropInvalid(ownerAbsent),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('project') },
    must_not_match: [
      { kind: 'present', reason: 'a conforming owner is written', paths: sortPaths(ownerPresent) },
    ],
  });

  // SC-10 — date range over meetings.
  const meetings = byType('meeting');
  // The cutoff sits mid-range on purpose. Filler meeting dates are spread over
  // 2023-2026, so a late cutoff would leave a handful of matches out of
  // hundreds — and a scenario whose expected answer is "almost nothing" cannot
  // tell a working range filter apart from one that returns almost nothing
  // whatever it is asked. A roughly even split makes both failure directions
  // visible.
  const DATE_CUTOFF = '2025-01-01';
  const occurredOnOrAfter = [];
  const occurredBefore = [];
  const occurredAbsent = [];
  for (const n of meetings) {
    const st = stateOf(n, 'occurred');
    if (st !== 'present') { occurredAbsent.push(n.path); continue; }
    const v = valuesOf(n, 'occurred')[0];
    if (String(v) >= DATE_CUTOFF) occurredOnOrAfter.push(n.path); else occurredBefore.push(n.path);
  }
  S.push({
    id: 'SC-10',
    plan_ref: 'A-03 (date)',
    title: 'Range filter over a date property',
    query: { kind: 'find', record_type: 'meeting', property: 'occurred', op: '>=', value: DATE_CUTOFF },
    rule: `A record matches iff its \`occurred\` day is on or after ${DATE_CUTOFF}. Every meeting date in this corpus is a bare YYYY-MM-DD day, so lexical and chronological order coincide and the expected set needs no timezone reasoning.`,
    policy_dependent: false,
    must_match: dropInvalid(occurredOnOrAfter),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('meeting') },
    must_not_match: [
      { kind: 'out_of_range', reason: 'earlier than the cutoff', paths: sortPaths(occurredBefore) },
      { kind: 'absent', reason: 'no date written; R-2 makes an ordering comparison over absence false', paths: sortPaths(occurredAbsent) },
    ],
  });

  // SC-11 — integer range over companies.
  const HEADCOUNT_CUTOFF = 500n;
  const hcOver = [], hcUnder = [], hcAbsent = [];
  for (const n of companies) {
    const st = stateOf(n, 'headcount');
    if (st !== 'present') { hcAbsent.push(n.path); continue; }
    const raw = valuesOf(n, 'headcount')[0];
    let v;
    try { v = BigInt(raw); } catch { hcAbsent.push(n.path); continue; }
    if (v > HEADCOUNT_CUTOFF) hcOver.push(n.path); else hcUnder.push(n.path);
  }
  S.push({
    id: 'SC-11',
    plan_ref: 'A-03 (integer)',
    title: 'Range filter over an integer property',
    query: { kind: 'find', record_type: 'company', property: 'headcount', op: '>', value: '500' },
    rule: 'A record matches iff its `headcount` is strictly greater than 500. The expected set is computed with arbitrary-precision integers, never a float, so the out-of-range invalid note cannot be silently saturated into it.',
    policy_dependent: false,
    must_match: dropInvalid(hcOver),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('company') },
    must_not_match: [
      { kind: 'out_of_range', reason: '<= 500', paths: sortPaths(hcUnder) },
      { kind: 'absent_or_nonconforming', reason: 'no conforming headcount is written; this includes the FR-013 out-of-int64 note, which is refused rather than saturated', paths: sortPaths(hcAbsent.concat(invalidByType('company'))) },
    ],
  });

  // SC-12 — decimal comparison over companies.
  const ARR_CUTOFF = '1000000.50';
  const arrOver = [], arrUnder = [], arrAbsent = [];
  const arrCutoffCents = centsOf(ARR_CUTOFF);
  for (const n of companies) {
    const st = stateOf(n, 'arr');
    if (st !== 'present') { arrAbsent.push(n.path); continue; }
    const c = centsOf(valuesOf(n, 'arr')[0]);
    if (c >= arrCutoffCents) arrOver.push(n.path); else arrUnder.push(n.path);
  }
  S.push({
    id: 'SC-12',
    plan_ref: 'A-03 (decimal)',
    title: 'Comparison over an exact decimal property',
    query: { kind: 'find', record_type: 'company', property: 'arr', op: '>=', value: ARR_CUTOFF },
    rule: `A record matches iff its \`arr\` is >= ${ARR_CUTOFF}. Every decimal in this corpus has exactly two fractional digits and the expected set was computed over integer cents, so a float-based implementation that rounds will disagree with this key on a boundary value.`,
    policy_dependent: false,
    must_match: dropInvalid(arrOver),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('company') },
    must_not_match: [
      { kind: 'below_cutoff', reason: 'strictly less than the cutoff', paths: sortPaths(arrUnder) },
      { kind: 'absent', reason: 'no arr written', paths: sortPaths(arrAbsent) },
    ],
  });

  // SC-13 — checkbox.
  const cbTrue = [], cbFalse = [], cbAbsent = [];
  for (const n of companies) {
    const st = stateOf(n, 'is_customer');
    if (st !== 'present') { cbAbsent.push(n.path); continue; }
    (valuesOf(n, 'is_customer')[0] === 'true' ? cbTrue : cbFalse).push(n.path);
  }
  S.push({
    id: 'SC-13',
    plan_ref: 'A-03 (checkbox)',
    title: 'Equality over a checkbox property, with absence as the third state',
    query: { kind: 'find', record_type: 'company', property: 'is_customer', op: '=', value: true },
    rule: 'FR-004c: a checkbox has exactly two values and ABSENCE is the third state. `= true` returns neither the `false` records nor the absent ones.',
    policy_dependent: false,
    must_match: dropInvalid(cbTrue),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('company') },
    must_not_match: [
      { kind: 'false_value', reason: 'written false', paths: sortPaths(cbFalse) },
      { kind: 'absent', reason: 'the third state — unset — which is not false', paths: sortPaths(cbAbsent) },
    ],
  });

  // SC-14 — relation target.
  const REL_TARGET = PROBE_ANCHOR;
  const relHit = [], relMiss = [];
  for (const n of byType('probe')) {
    const st = stateOf(n, 'probe_relation');
    if (st === 'present' && valuesOf(n, 'probe_relation')[0] === `[[${REL_TARGET}]]`) relHit.push(n.path);
    else relMiss.push(n.path);
  }
  S.push({
    id: 'SC-14',
    plan_ref: 'A-03 (relation)',
    title: 'Equality over a relation property, compared by target',
    query: { kind: 'find', record_type: 'probe', property: 'probe_relation', op: '=', value: `[[${REL_TARGET}]]` },
    rule: '§8 R-8: a relation compares by TARGET, never by display text. Every matching note writes the link as a quoted wikilink, which is D5.1s on-disk form.',
    policy_dependent: false,
    must_match: sortPaths(relHit),
    must_not_match: [
      { kind: 'other_or_absent', reason: 'a different target, an empty list, or no value at all', paths: sortPaths(relMiss) },
    ],
  });

  // SC-15 — person list membership.
  const PERSON_TARGET = PROBE_PERSON_TARGET;
  const personHit = [], personMiss = [];
  for (const n of byType('probe')) {
    const st = stateOf(n, 'probe_person_many');
    if (st === 'present' && valuesOf(n, 'probe_person_many').includes(`[[${PERSON_TARGET}]]`)) personHit.push(n.path);
    else personMiss.push(n.path);
  }
  S.push({
    id: 'SC-15',
    plan_ref: 'A-03 (person, list-valued)',
    title: 'Membership over a LIST-valued person property',
    query: { kind: 'find', record_type: 'probe', property: 'probe_person_many', op: 'IN', value: [`[[${PERSON_TARGET}]]`] },
    rule: 'A record matches iff the named person appears anywhere in the list. R-13 refuses the ORDERING operators over a list; membership and equality are the operators this scenario uses.',
    policy_dependent: false,
    must_match: sortPaths(personHit),
    must_not_match: [
      { kind: 'other_or_absent', reason: 'the target is not in the list, the list is empty, or the property is absent', paths: sortPaths(personMiss) },
    ],
  });

  // SC-16 — list-valued text.
  const aliasHit = [], aliasMiss = [];
  for (const n of companies) {
    const st = stateOf(n, 'aliases');
    if (st === 'present' && valuesOf(n, 'aliases').some((v) => String(v).toLowerCase().includes('vorlex'))) aliasHit.push(n.path);
    else aliasMiss.push(n.path);
  }
  S.push({
    id: 'SC-16',
    plan_ref: 'A-03 (text, list-valued)',
    title: 'Substring match over a LIST-valued text property',
    query: { kind: 'find', record_type: 'company', property: 'aliases', op: 'LIKE', value: '%Vorlex%' },
    rule: 'A record matches iff ANY element of `aliases` contains "Vorlex". This is the mirror image of SC-04: the same token, the other property, and the two answer sets must be disjoint.',
    policy_dependent: false,
    must_match: dropInvalid(aliasHit),
    invalid_in_scope: { note: INVALID_SCOPE_NOTE, paths: invalidByType('company') },
    must_not_match: [
      { kind: 'other_property', reason: 'the token is in `name` or the body, not in `aliases`', paths: sortPaths(plantedNotePaths.sc04_name.concat(plantedNotePaths.sc04_body)) },
    ],
    paired_with: 'SC-04',
    pair_note: 'SC-04 and SC-16 must return disjoint sets. An implementation that searches all properties for a property-scoped query returns the union for both, which looks like a pass on recall and is a total failure on precision.',
  });

  // SC-17 — search over the invalid part of the corpus.
  S.push({
    id: 'SC-17',
    plan_ref: 'A-07',
    title: 'Search whose entire answer set is deliberately invalid notes',
    query: { kind: 'search', text: 'thessaly' },
    rule: 'The term "thessaly" is planted in the body of every deliberately-invalid note and nowhere else. Whatever the tool does here must be STATED: returning them, or skipping them with a named reason, are both defensible; refusing the whole request because one note is bad is the failure A-07 names.',
    policy_dependent: true,
    policy_note: 'Two gradings are legitimate and the run must declare which contract it is asserting. What is NOT legitimate: an empty result with no explanation, or a hard error for the whole query.',
    must_match: invalidPaths,
    must_not_match: [],
    unacceptable_outcomes: [
      'the request fails outright because the corpus contains invalid notes',
      'an empty result set with no statement that invalid notes were skipped',
    ],
  });

  return S;
}

// ---------------------------------------------------------------------------
// Metadata: counts, per record type and per property type.
// ---------------------------------------------------------------------------

function buildCounts(corpus) {
  const perRecordType = {};
  let ordinary = 0;
  for (const n of corpus.notes) {
    if (!n.recordType) { ordinary += 1; continue; }
    perRecordType[n.recordType] = (perRecordType[n.recordType] || 0) + 1;
  }

  // Per property TYPE, across every note: how many notes write a conforming
  // scalar, a conforming list, an explicit empty scalar, an explicit empty
  // list. The plan's type-coverage bullet is exactly these four being non-zero
  // for all eight types.
  const perPropertyType = {};
  for (const t of PROPERTY_TYPES) {
    perPropertyType[t] = { scalar_present: 0, list_present: 0, empty_scalar: 0, empty_list: 0, declarations_scalar: 0, declarations_many: 0 };
  }
  for (const s of SCHEMAS) {
    for (const p of s.properties) {
      perPropertyType[p.type][p.many ? 'declarations_many' : 'declarations_scalar'] += 1;
    }
  }
  for (const n of corpus.notes) {
    for (const e of n.entries) {
      if (e.modelOnly) continue; // a defect's state, not a written value
      const b = perPropertyType[e.type];
      if (!b) continue;
      if (e.state === 'present') b[e.many ? 'list_present' : 'scalar_present'] += 1;
      else if (e.state === 'empty_scalar') b.empty_scalar += 1;
      else if (e.state === 'empty_list') b.empty_list += 1;
    }
  }

  return {
    total_notes: corpus.notes.length,
    ordinary_notes: ordinary,
    per_record_type: perRecordType,
    per_property_type: perPropertyType,
  };
}

// ---------------------------------------------------------------------------
// Self-checks — the generator's own falsifiability pass.
//
// docs/internal/false-green-patterns.md: a fixture that has never been checked
// against its own claims is a green light nobody earned. Each check below can
// FAIL, and each one has failed at least once during development.
// ---------------------------------------------------------------------------

function selfCheck(corpus, scenarios, counts) {
  const results = [];
  const fail = (name, detail) => results.push({ name, ok: false, detail });
  const pass = (name, detail) => results.push({ name, ok: true, detail });

  // 1. No unplanted note may contain a reserved term, in body or frontmatter.
  // No exemptions and no subtraction: EVERY note that this generator did not
  // deliberately plant is scanned, whole file, for every reserved term. An
  // earlier version of this check carved out the probe notes after the fact,
  // which is precisely the shape of a check adjusted until it passes. The
  // corpus was changed to satisfy the check instead.
  const plantedOrInvalid = new Set(corpus.notes.filter((n) => n.planted || n.invalid).map((n) => n.path));
  const leakExamples = [];
  let scanned = 0;
  for (const n of corpus.notes) {
    if (plantedOrInvalid.has(n.path)) continue;
    scanned += 1;
    const whole = renderNote(n);
    const frontmatter = whole.slice(0, whole.indexOf('\n---\n', 4) + 5).toLowerCase();
    const body = String(n.body).toLowerCase();
    for (const t of RESERVED_BODY_TERMS) {
      if (body.includes(t)) leakExamples.push(`${n.path} body contains "${t}"`);
    }
    for (const t of RESERVED_FRONTMATTER_TERMS) {
      if (frontmatter.includes(t)) leakExamples.push(`${n.path} frontmatter contains "${t}"`);
    }
  }
  if (leakExamples.length) fail('reserved terms do not leak into unplanted notes', `${leakExamples.length} leak(s): ${leakExamples.slice(0, 8).join('; ')}`);
  else pass('reserved terms do not leak into unplanted notes', `${scanned} unplanted notes scanned (body against ${RESERVED_BODY_TERMS.length} terms, frontmatter against ${RESERVED_FRONTMATTER_TERMS.length}), zero hits`);

  // 2. must_match and must_not_match must be disjoint within every scenario.
  let overlapProblems = 0;
  for (const sc of scenarios) {
    const m = new Set(sc.must_match);
    for (const group of sc.must_not_match) {
      for (const p of group.paths) {
        if (m.has(p)) { overlapProblems += 1; fail(`SC ${sc.id}: must_match / must_not_match disjoint`, `${p} is in both`); }
      }
    }
  }
  if (overlapProblems === 0) pass('every scenario keeps must_match and must_not_match disjoint', `${scenarios.length} scenarios checked`);

  // 3. Every path named in the key must exist in the corpus.
  const all = new Set(corpus.notes.map((n) => n.path));
  let missing = 0;
  for (const sc of scenarios) {
    for (const p of sc.must_match) if (!all.has(p)) { missing += 1; fail(`SC ${sc.id}: must_match path exists`, p); }
    for (const g of sc.must_not_match) for (const p of g.paths) if (!all.has(p)) { missing += 1; fail(`SC ${sc.id}: must_not_match path exists`, p); }
  }
  if (missing === 0) pass('every path named in the answer key exists in the corpus', `${all.size} notes`);

  // 4. Type coverage: all four cases non-zero for all eight property types.
  const gaps = [];
  for (const t of PROPERTY_TYPES) {
    const b = counts.per_property_type[t];
    for (const k of ['scalar_present', 'list_present', 'empty_scalar', 'empty_list']) {
      if (b[k] === 0) gaps.push(`${t}.${k}`);
    }
  }
  if (gaps.length) fail('all eight property types cover scalar / list / empty-scalar / empty-list', gaps.join(', '));
  else pass('all eight property types cover scalar / list / empty-scalar / empty-list', PROPERTY_TYPES.join(', '));

  // 5. SC-04 and SC-16 must be disjoint; SC-07 and SC-08 must differ.
  const sc04 = scenarios.find((s) => s.id === 'SC-04');
  const sc16 = scenarios.find((s) => s.id === 'SC-16');
  const inter = sc04.must_match.filter((p) => sc16.must_match.includes(p));
  if (inter.length) fail('SC-04 and SC-16 answer sets are disjoint', inter.join(', '));
  else pass('SC-04 and SC-16 answer sets are disjoint', `${sc04.must_match.length} vs ${sc16.must_match.length} notes`);

  const sc07 = scenarios.find((s) => s.id === 'SC-07');
  const sc08 = scenarios.find((s) => s.id === 'SC-08');
  if (sc07.must_match.length === sc08.must_match.length) {
    fail('SC-07 (<>) and SC-08 (NOT =) have different answer sets', `both have ${sc07.must_match.length} notes — the absence rule is untestable on this corpus`);
  } else {
    pass('SC-07 (<>) and SC-08 (NOT =) have different answer sets', `${sc07.must_match.length} vs ${sc08.must_match.length} notes`);
  }

  // 6. Frontmatter wikilinks must actually RESOLVE to a file in the corpus.
  //
  // Omnipus resolves `[[...]]` by filename basename only, so a link spelling a
  // note's LABEL rather than its file stem points at nothing. An earlier
  // version of this generator did exactly that for every filler relation, and
  // the corpus silently exercised none of relation resolution, backlinks, or
  // the rename cascade while appearing to — a UAT run then mistook the honest
  // "0 backlinks" that produced for link corruption.
  //
  // Scans entries[].values, which is where a frontmatter link actually lives;
  // the note's `body` is the markdown BELOW the frontmatter and never carries
  // the relation values this check exists for.
  //
  // The assertion is a FLOOR, not an exact count: some links dangle on purpose
  // (the deliberately-invalid notes, and probe anchors with no target). Zero
  // resolving links is the regression that matters, and it is what this catches.
  {
    const stems = new Set(corpus.notes.map((nt) => nt.path.replace(/^.*\//, '').replace(/\.md$/, '')));
    let resolved = 0;
    let dangling = 0;
    for (const nt of corpus.notes) {
      for (const e of nt.entries || []) {
        for (const v of e.values || []) {
          for (const m of String(v).matchAll(/\[\[([^\]]+)\]\]/g)) {
            if (stems.has(m[1])) resolved += 1; else dangling += 1;
          }
        }
      }
    }
    if (resolved === 0) {
      fail('frontmatter wikilinks resolve to real notes',
        `${dangling} frontmatter links, NONE of which resolve — links are spelling labels rather than file stems, so relation resolution, backlinks and the rename cascade are all untested`);
    } else {
      pass('frontmatter wikilinks resolve to real notes',
        `${resolved} resolve, ${dangling} dangle by design (invalid fixtures and unanchored probes)`);
    }
  }

  // 7. Every scenario with an empty must_match must say so deliberately.
  for (const sc of scenarios) {
    if (sc.must_match.length === 0 && !sc.expect_refusal && !sc.policy_dependent) {
      fail(`SC ${sc.id}: an empty expected set must be deliberate`, 'must_match is empty but the scenario is neither a refusal nor policy-dependent');
    }
  }

  return results;
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

function writeCorpus(outDir, corpus, key, force) {
  const vaultDir = path.join(outDir, 'vault');
  if (fs.existsSync(outDir)) {
    if (!force) throw new Error(`${outDir} already exists; pass --force to replace it`);
    fs.rmSync(outDir, { recursive: true, force: true });
  }
  fs.mkdirSync(path.join(vaultDir, MARKER_DIR_NAME, RECORDS_DIR_NAME), { recursive: true });

  // Vault marker (pkg/knowledge/marker.go::Marker). display_name is mandatory
  // and must be non-empty printable text, so it is written explicitly.
  fs.writeFileSync(
    path.join(vaultDir, MARKER_DIR_NAME, MARKER_FILE_NAME),
    JSON.stringify({ display_name: 'Omnipus Knowledge UAT Corpus' }, null, 2) + '\n'
  );

  for (const s of SCHEMAS) {
    fs.writeFileSync(path.join(vaultDir, MARKER_DIR_NAME, RECORDS_DIR_NAME, `${s.type}.yaml`), emitSchemaYAML(s));
  }

  // Sorted so the write ORDER is deterministic too, not merely the contents.
  const ordered = corpus.notes.slice().sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  for (const n of ordered) {
    const abs = path.join(vaultDir, n.path);
    fs.mkdirSync(path.dirname(abs), { recursive: true });
    fs.writeFileSync(abs, renderNote(n));
  }

  fs.writeFileSync(path.join(outDir, 'answer-key.json'), JSON.stringify(key, null, 2) + '\n');
  return vaultDir;
}

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

const HELP = `
gen-knowledge-corpus.mjs — build a synthetic Omnipus knowledge vault and its answer key.

  Implements docs/internal/uat/knowledge-tools-uat-plan.md §3.

USAGE
  node scripts/uat/gen-knowledge-corpus.mjs --out <dir> [options]

OPTIONS
  --out <dir>        Output directory. Required. Receives:
                       <dir>/vault/           the vault root — point the gateway here
                       <dir>/answer-key.json  the grading key
  --tier small|large Corpus size. small = 200 notes (fast iteration),
                     large = 2000 notes. Default: small.
  --seed <int>       PRNG seed. A fixed seed produces a byte-identical corpus.
                     Default: 20260901.
  --force            Replace <dir> if it already exists.
  --quiet            Print only the summary line.
  --help             This text.

WHAT IS AND IS NOT SEEDED
  The seed drives FILLER notes only — their prose, their property values and
  which of their properties are present / empty / absent.

  The planted search notes, the deliberately-invalid notes and the type-probe
  notes are FIXED: identical at every seed and at both tiers. That is on
  purpose. It makes the planted answer sets comparable across a 200-note and a
  2000-note run, which is what turns the plan's B-01 ("latency vs corpus size")
  into the same question asked twice rather than two different questions.

  Scenario answer sets that range over the whole corpus (enum, date, integer,
  decimal and checkbox filters) DO change with the seed and the tier, because
  they are computed from the corpus that was actually generated. The key is
  written from the same in-memory model that produced the files, never by
  reading them back.

EXAMPLES
  node scripts/uat/gen-knowledge-corpus.mjs --out /tmp/uat-corpus-small
  node scripts/uat/gen-knowledge-corpus.mjs --out /tmp/uat-corpus-large --tier large --seed 7
`;

function parseArgs(argv) {
  const out = { tier: 'small', seed: 20260901, force: false, quiet: false, help: false, out: null };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--help' || a === '-h') out.help = true;
    else if (a === '--force') out.force = true;
    else if (a === '--quiet') out.quiet = true;
    else if (a === '--out') out.out = argv[++i];
    else if (a === '--tier') out.tier = argv[++i];
    else if (a === '--seed') out.seed = Number(argv[++i]);
    else if (a.startsWith('--out=')) out.out = a.slice(6);
    else if (a.startsWith('--tier=')) out.tier = a.slice(7);
    else if (a.startsWith('--seed=')) out.seed = Number(a.slice(7));
    else throw new Error(`unknown argument ${a} (try --help)`);
  }
  return out;
}

function main() {
  let args;
  try {
    args = parseArgs(process.argv.slice(2));
  } catch (e) {
    console.error(String(e.message));
    process.exit(2);
  }
  if (args.help) { process.stdout.write(HELP.trimStart()); return; }
  if (!args.out) { console.error('--out is required (try --help)'); process.exit(2); }
  if (!TIERS[args.tier]) { console.error(`--tier must be one of ${Object.keys(TIERS).join(', ')}`); process.exit(2); }
  if (!Number.isInteger(args.seed)) { console.error('--seed must be an integer'); process.exit(2); }

  const corpus = buildCorpus(args.tier, args.seed);
  const scenarios = buildScenarios(corpus);
  const counts = buildCounts(corpus);
  const checks = selfCheck(corpus, scenarios, counts);

  const key = {
    format: 'omnipus-knowledge-uat-answer-key',
    format_version: 1,
    generator: 'scripts/uat/gen-knowledge-corpus.mjs',
    plan: 'docs/internal/uat/knowledge-tools-uat-plan.md §3',
    // Everything a grader needs WITHOUT re-reading the corpus lives here.
    corpus: {
      seed: args.seed,
      tier: args.tier,
      seed_scope: 'filler notes only; planted, invalid and probe notes are fixed at every seed and both tiers',
      vault_relative_root: 'vault',
      marker_dir: MARKER_DIR_NAME,
      schema_dir: `${MARKER_DIR_NAME}/${RECORDS_DIR_NAME}`,
      schema_version: SUPPORTED_SCHEMA_VERSION,
      record_types: SCHEMAS.map((s) => ({
        type: s.type,
        dir: s.dir,
        identity_prefix: s.prefix,
        properties: s.properties.map((p) => ({
          name: p.name, type: p.type, many: !!p.many, required: !!p.required,
          ...(p.values ? { values: p.values } : {}),
          ...(p.to ? { to: p.to } : {}),
          ...(p.unit ? { unit: p.unit } : {}),
        })),
      })),
      property_types_declared: PROPERTY_TYPES,
      counts,
    },
    invalid_records: {
      // The plan wants "a known, recorded number" of schema violations, and
      // exactly WHICH and WHY, so a tool's handling of a partly-invalid vault
      // can be graded rather than guessed at.
      count: corpus.invalidRecords.length,
      note: 'Each of these notes carries EXACTLY ONE deliberate defect. expected_finding_code is the pkg/records FindingCode the defect should raise; it is this generator\'s stated expectation, and a run that disagrees with it is a finding either way.',
      records: corpus.invalidRecords
        .slice()
        .sort((a, b) => (a.path < b.path ? -1 : 1))
        .map((n) => ({ path: n.path, record_type: n.recordType, ...n.invalid })),
    },
    scenarios,
    self_checks: checks,
  };

  const vaultDir = writeCorpus(args.out, corpus, key, args.force);

  const failed = checks.filter((c) => !c.ok);
  if (!args.quiet) {
    console.log(`corpus      ${vaultDir}`);
    console.log(`answer key  ${path.join(args.out, 'answer-key.json')}`);
    console.log(`tier=${args.tier} seed=${args.seed} notes=${counts.total_notes} (ordinary=${counts.ordinary_notes})`);
    console.log('per record type:');
    for (const t of Object.keys(counts.per_record_type).sort()) console.log(`  ${t.padEnd(10)} ${counts.per_record_type[t]}`);
    console.log('per property type (scalar / list / empty-scalar / empty-list):');
    for (const t of PROPERTY_TYPES) {
      const b = counts.per_property_type[t];
      console.log(`  ${t.padEnd(10)} ${String(b.scalar_present).padStart(5)} ${String(b.list_present).padStart(5)} ${String(b.empty_scalar).padStart(5)} ${String(b.empty_list).padStart(5)}`);
    }
    console.log(`scenarios   ${scenarios.length}`);
    console.log(`invalid     ${corpus.invalidRecords.length}`);
    console.log('self-checks:');
    for (const c of checks) console.log(`  [${c.ok ? 'PASS' : 'FAIL'}] ${c.name}${c.detail ? ' — ' + c.detail : ''}`);
  }
  if (failed.length) {
    console.error(`\n${failed.length} self-check(s) FAILED; the corpus was written but its answer key cannot be trusted.`);
    process.exit(1);
  }
  if (args.quiet) console.log(`ok tier=${args.tier} seed=${args.seed} notes=${counts.total_notes} scenarios=${scenarios.length} invalid=${corpus.invalidRecords.length}`);
}

main();
