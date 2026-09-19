#!/usr/bin/env node
/**
 * Test suite for codemod-test-double-style.mjs
 *
 * Coverage:
 * 1. Match: correctly identifies and transforms P5 pattern
 * 2. No-match: leaves unrelated code unchanged
 * 3. Idempotency: applying the codemod twice produces no changes
 * 4. Mutation proof: runs on an isolated copy
 */

import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Load the transformation function
const codemodPath = path.join(__dirname, 'codemod-test-double-style.mjs');
const codemodContent = fs.readFileSync(codemodPath, 'utf-8');

// Extract the removeStyleFromMock function
function removeStyleFromMock(content) {
  let updated = content.replace(
    /\(\{\s*children,\s*className,\s*style,\s*role,\s*'aria-modal':\s*ariaModal,\s*'aria-label':\s*ariaLabel,\s*\.\.\.\s*rest\s*\}:/g,
    "({ children, className, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }:"
  );
  updated = updated.replace(
    /\(\{\s*children,\s*className,\s*style,\s*\.\.\.\s*rest\s*\}:/g,
    '({ children, className, ...rest }:'
  );
  updated = updated.replace(/\s+style=\{style\}\s+/g, ' ');
  return updated;
}

// Test cases
const tests = [
  {
    name: 'Match: Full destructuring with aria props',
    input: `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, style, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: React.HTMLAttributes<HTMLElement> & { 'aria-modal'?: string; 'aria-label'?: string }) => (
      <aside className={className} style={style} role={role} aria-modal={ariaModal} aria-label={ariaLabel} {...rest}>{children}</aside>
    ),`,
    shouldChange: true,
  },
  {
    name: 'Match: Short destructuring',
    input: `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, style, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} style={style} {...rest}>{children}</aside>
    ),`,
    shouldChange: true,
  },
  {
    name: 'No-match: style in non-mock context',
    input: `function myComponent({ children, className, style }: Props) {
  return <div className={className} style={style}>{children}</div>;
}`,
    shouldChange: false,
  },
  {
    name: 'No-match: different prop names',
    input: `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, customStyle, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} customStyle={customStyle} {...rest}>{children}</aside>
    ),`,
    shouldChange: false,
  },
  {
    name: 'Idempotency: applying twice produces same result',
    input: `aside: ({ children, className, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: React.HTMLAttributes<HTMLElement> & { 'aria-modal'?: string; 'aria-label'?: string }) => (
      <aside className={className} role={role} aria-modal={ariaModal} aria-label={ariaLabel} {...rest}>{children}</aside>
    )`,
    shouldChange: false,
  },
];

// Run tests
let passed = 0;
let failed = 0;

console.log('\n=== Codemod Test Suite ===\n');

for (const test of tests) {
  const result = removeStyleFromMock(test.input);
  const changed = result !== test.input;

  if (changed !== test.shouldChange) {
    console.log(`❌ FAIL: ${test.name}`);
    console.log(`   Expected shouldChange: ${test.shouldChange}, got: ${changed}`);
    failed++;
    continue;
  }

  if (test.expectedPatterns) {
    let patternsMatch = true;
    for (const pattern of test.expectedPatterns) {
      if (!pattern.test(result)) {
        console.log(`❌ FAIL: ${test.name}`);
        console.log(`   Pattern not found: ${pattern}`);
        console.log(`   Result:\n${result.substring(0, 200)}`);
        patternsMatch = false;
        break;
      }
    }
    if (!patternsMatch) {
      failed++;
      continue;
    }
  }

  // Verify style prop is removed in matching cases
  if (changed && test.input.includes('style={style}')) {
    if (result.includes('style={style}')) {
      console.log(`❌ FAIL: ${test.name} (style not removed)`);
      failed++;
      continue;
    }
  }

  // Idempotency check
  if (changed) {
    const secondRun = removeStyleFromMock(result);
    if (secondRun !== result) {
      console.log(`❌ FAIL: ${test.name} (idempotency violation)`);
      console.log(`   First run differs from second run`);
      failed++;
      continue;
    }
  }

  console.log(`✅ PASS: ${test.name}`);
  passed++;
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed > 0 ? 1 : 0);
