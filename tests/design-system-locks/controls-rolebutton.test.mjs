// Design-system lock scanner tests — controls.mjs, FIX-ROLEBUTTON extension.
//
// The literal raw-button/raw-dialog checks in controls.mjs match only the
// <button>/<dialog> tag, so a control hand-built from a non-component element
// (a div, an img, a lowercase-member animation-wrapper tag like motion.div)
// carrying role="button" (or the same shape for role="radio"/"switch"/"tab")
// is invisible to them. This suite proves the extension added for that gap
// under FIX-ROLEBUTTON: dist/design-system-baseline/cli-lanes/c1-prep/C2-PREP/
// per-area-summary.md, "Handmade controls NOT in the ledger".
//
// Specification sources (expected values derive from these, never from the
// implementation under test):
//   - design-system/enforcement/contract.json — finding shape, "syntax:
//     nonempty canonical offending syntax", fingerprint = SHA256([ruleId,
//     path, syntax]) (position never participates).
//   - docs/internal/design/design-system-definition.md E1/D5 — raw controls
//     must route through the Button primitive (or the matching composite for
//     radio/switch/tab); "our own catalogued components are not violations."
//   - FIX-ROLEBUTTON brief: any JSX element that is not one of our
//     components and carries role="button"/"radio"/"switch"/"tab" is a
//     hand-built control reported under the existing raw-control rule
//     (controls/raw-button), with a syntax string naming the element and
//     role; a case that cannot be judged fails closed rather than staying
//     silent.
//
// Fixtures for the five real, previously-unledgered shapes are modeled
// directly on the source files the inventory lane found them in (per-area-
// summary.md), not just abstracted "div with role" shapes, so a regression
// in any one of the five real files' structure is caught by name.

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import { scan } from '../../scripts/design-system-locks/controls.mjs'

const DEFAULT_PATH = 'src/features/fixture.tsx'
const CATALOG = JSON.parse(readFileSync(new URL('../../design-system/catalog.json', import.meta.url), 'utf8'))

function scanSource(source, path = DEFAULT_PATH) {
  return scan({ path, source, policy: {}, catalog: CATALOG })
}

function ruleIds(source, path) {
  return scanSource(source, path).map((finding) => finding.ruleId)
}

function findingsFor(source, ruleId, path) {
  return scanSource(source, path).filter((finding) => finding.ruleId === ruleId)
}

function syntaxes(source, ruleId, path) {
  return findingsFor(source, ruleId, path).map((finding) => finding.syntax)
}

// ---------------------------------------------------------------------------
// FORBIDDEN — the five real, previously-invisible shapes
// ---------------------------------------------------------------------------

test('FIX-ROLEBUTTON: flags a div-as-disclosure-toggle with role="button" (DiagnosticsSection.tsx shape)', () => {
  const source = [
    'function IssueRow({ expanded, onToggle }) {',
    '  return (',
    '    <div',
    '      role="button"',
    '      tabIndex={0}',
    '      aria-expanded={expanded}',
    '      onClick={onToggle}',
    '    >',
    '      Detail',
    '    </div>',
    '  )',
    '}',
  ].join('\n')
  assert.deepEqual(ruleIds(source), ['controls/raw-button'])
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<div role="button">'])
})

test('FIX-ROLEBUTTON: flags an img-as-lightbox-trigger with role="button" and hand-rolled Enter handling (ChatImage.tsx shape)', () => {
  const source = [
    'function ChatImage({ src, alt, enlarge }) {',
    '  return (',
    '    <div className="relative">',
    '      <img',
    '        src={src}',
    '        alt={alt}',
    '        onClick={enlarge}',
    '        role="button"',
    '        tabIndex={0}',
    '        onKeyDown={(e) => {',
    "          if (e.key === 'Enter') enlarge()",
    '        }}',
    '      />',
    '    </div>',
    '  )',
    '}',
  ].join('\n')
  assert.deepEqual(ruleIds(source), ['controls/raw-button'])
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<img role="button">'])
})

test('FIX-ROLEBUTTON: flags a whole-row div with role="button", no drag ref (LibraryEntryRow.tsx shape)', () => {
  const source = [
    'function LibraryEntryRow({ entry, onSelectFile }) {',
    '  function handleActivate() {',
    '    onSelectFile(entry)',
    '  }',
    '  return (',
    '    <div',
    '      role="button"',
    '      tabIndex={0}',
    '      data-testid={`library-row-${entry.path}`}',
    '      onClick={handleActivate}',
    '    >',
    '      {entry.name}',
    '    </div>',
    '  )',
    '}',
  ].join('\n')
  assert.deepEqual(ruleIds(source), ['controls/raw-button'])
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<div role="button">'])
})

test('FIX-ROLEBUTTON: flags a div-as-card with role="button" WITH a drag-and-drop activator ref (TaskCard.tsx shape)', () => {
  const source = [
    'function TaskCard({ drag, onClick }) {',
    '  return (',
    '    <div',
    '      ref={drag?.activatorRef}',
    '      role="button"',
    '      tabIndex={0}',
    "      aria-pressed={drag?.attributes['aria-pressed']}",
    '      onClick={onClick}',
    '    >',
    '      Task',
    '    </div>',
    '  )',
    '}',
  ].join('\n')
  assert.deepEqual(ruleIds(source), ['controls/raw-button'])
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<div role="button">'])
})

test('FIX-ROLEBUTTON: flags a Framer-Motion motion.div with role="button" inside a graph node (TaskNode.tsx shape)', () => {
  const source = [
    "import { motion } from 'framer-motion'",
    'function TaskNode({ task, onOpen }) {',
    '  return (',
    '    <motion.div',
    '      initial={{ opacity: 0 }}',
    '      animate={{ opacity: 1 }}',
    '      role="button"',
    '      tabIndex={0}',
    '      aria-label={task.title}',
    '      onKeyDown={(e) => {',
    "        if (e.key === 'Enter') onOpen(task)",
    '      }}',
    '    >',
    '      {task.title}',
    '    </motion.div>',
    '  )',
    '}',
  ].join('\n')
  assert.deepEqual(ruleIds(source), ['controls/raw-button'])
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<motion.div role="button">'])
})

// ---------------------------------------------------------------------------
// PERMITTED
// ---------------------------------------------------------------------------

test('PERMITTED: the Button primitive rendered with an explicit role attribute is not double-flagged', () => {
  const source = [
    "import { Button } from '@/components/ui/button'",
    'export const X = () => <Button role="button">go</Button>',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

test('PERMITTED: a catalogued component (IconButton) rendered with role is not flagged', () => {
  const source = [
    "import { IconButton } from '@/components/ui/icon-button'",
    'export const X = () => <IconButton role="button" aria-label="Close" />',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

test('PERMITTED: an ordinary div with neither role nor click/keyboard handlers is not flagged', () => {
  const source = 'export const X = () => <div className="wrapper">hello</div>'
  assert.deepEqual(ruleIds(source), [])
})

// ---------------------------------------------------------------------------
// The literal <button>/<dialog> intrinsics are not double-counted
// ---------------------------------------------------------------------------

test('a real <button> carrying an explicit role="button" is one finding, not two', () => {
  const source = 'export const X = () => <button type="button" role="button">go</button>'
  const findings = findingsFor(source, 'controls/raw-button')
  assert.equal(findings.length, 1)
  assert.equal(findings[0].syntax, '<button>')
})

test('a real <dialog> carrying role="dialog" is unaffected (not a governed role) and reports once as raw-dialog', () => {
  const source = 'export const X = () => <dialog role="dialog" open>hi</dialog>'
  assert.deepEqual(ruleIds(source), ['controls/raw-dialog'])
})

// ---------------------------------------------------------------------------
// Member-expression exemption: an uppercase member segment is a real
// component reference (Radix-style composition, third-party primitives)
// ---------------------------------------------------------------------------

test('an uppercase-member JSX tag (e.g. a Radix-style composition slot) carrying role="button" is not flagged', () => {
  assert.deepEqual(ruleIds('export const X = () => <SwitchPrimitives.Root role="button" data-x="" />'), [])
})

test('a third-party action-button component reference (lowercase-free member, e.g. ComposerPrimitive.Send) is not flagged even with tabIndex+onClick and no role', () => {
  const source = [
    "import { ComposerPrimitive } from '@assistant-ui/react'",
    'export const X = () => <ComposerPrimitive.Send tabIndex={6} onClick={() => {}} />',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

// ---------------------------------------------------------------------------
// role="radio"/"switch"/"tab" — same gap, same rule, on a non-component element
// ---------------------------------------------------------------------------

test('flags a hand-built div with role="radio"', () => {
  const source = 'export const X = () => <div role="radio" tabIndex={0} aria-checked={false} onClick={() => {}} />'
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<div role="radio">'])
})

test('flags a hand-built div with role="switch"', () => {
  const source = 'export const X = () => <div role="switch" tabIndex={0} aria-checked={false} onClick={() => {}} />'
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<div role="switch">'])
})

test('flags a hand-built div with role="tab"', () => {
  const source = 'export const X = () => <div role="tab" tabIndex={0} aria-selected={false} onClick={() => {}} />'
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<div role="tab">'])
})

test('does not flag a hand-built div with an ungoverned role (e.g. role="status")', () => {
  assert.deepEqual(ruleIds('export const X = () => <div role="status">Saved</div>'), [])
})

// ---------------------------------------------------------------------------
// Fail-closed on a dynamic role attribute (WorkspaceTeamGraph.tsx shape: a
// real 6th instance found during FIX-ROLEBUTTON's own investigation, via
// `role={cond ? "button" : undefined}` — outside the five the inventory
// lane found by grepping only literal role="button").
// ---------------------------------------------------------------------------

test('flags a ternary role attribute when the reachable branch is a governed role (WorkspaceTeamGraph.tsx shape)', () => {
  const source = [
    'function TeamNode({ data }) {',
    '  return (',
    '    <div',
    '      role={data.onOpenAgent ? "button" : undefined}',
    '      tabIndex={data.onOpenAgent ? 0 : undefined}',
    '      onClick={data.onOpenAgent}',
    '    >',
    '      {data.name}',
    '    </div>',
    '  )',
    '}',
  ].join('\n')
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<div role="button">'])
})

test('does not flag a ternary role attribute whose every branch is a provably ungoverned role (brand-icon.tsx shape)', () => {
  const source = 'export const X = ({ decorative }) => <span role={decorative ? undefined : "img"} />'
  assert.deepEqual(ruleIds(source), [])
})

test('does not flag a ternary role attribute whose every branch is a provably ungoverned role, both literal (ProviderValidationBanner.tsx shape)', () => {
  const source = 'export const X = ({ isBlocking }) => <div role={isBlocking ? "alert" : "status"} />'
  assert.deepEqual(ruleIds(source), [])
})

test('fails closed on an unresolvable dynamic role attribute rather than staying silent (toast-container.tsx shape)', () => {
  const source = [
    "const toastRole = 'status'",
    'export const X = () => <div role={toastRole} />',
  ].join('\n')
  const findings = findingsFor(source, 'controls/raw-button')
  assert.equal(findings.length, 1)
  assert.equal(findings[0].syntax, '<div role={expr}>')
  assert.match(findings[0].message, /cannot be statically excluded/)
})

test('fails closed on a ternary role attribute with one unresolvable branch', () => {
  const source = 'export const X = ({ pick }) => <div role={pick ? "button" : someRole} />'
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['<div role={expr}>'])
})

// ---------------------------------------------------------------------------
// Spread does not, by itself, imply an injected role on an arbitrary element
// (narrow scope decision — see the doc comment in controls.mjs)
// ---------------------------------------------------------------------------

test('does not flag tabIndex+onClick+spread on a div with no explicit role attribute', () => {
  const source = 'export const X = (props) => <div tabIndex={0} onClick={() => {}} {...props} />'
  assert.deepEqual(ruleIds(source), [])
})
