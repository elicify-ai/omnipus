#!/usr/bin/env node
/**
 * Codemod: Remove `style={style}` forwarding from framer-motion test doubles
 *
 * Pattern P5 — test double / mock component whole-style-object forwarding:
 * This codemod removes the `style` destructuring parameter and the `style={style}`
 * prop forwarding from framer-motion mock components in test files, since no test
 * assertions depend on the forwarded style.
 *
 * Files changed: five Sidebar test doubles
 * - src/components/layout/Sidebar.focus-trap.test.tsx
 * - src/components/layout/Sidebar.kb5.test.tsx
 * - src/components/layout/Sidebar.m5.test.tsx
 * - src/components/layout/Sidebar.orphan.test.tsx
 * - src/components/layout/Sidebar.test.tsx
 *
 * Proof: no assertions read the aside's style (toHaveStyle, .style., getComputedStyle,
 * snapshot matchers) in any test file.
 */

import fs from 'fs';

const files = [
  'src/components/layout/Sidebar.focus-trap.test.tsx',
  'src/components/layout/Sidebar.kb5.test.tsx',
  'src/components/layout/Sidebar.m5.test.tsx',
  'src/components/layout/Sidebar.orphan.test.tsx',
  'src/components/layout/Sidebar.test.tsx',
];

function removeStyleFromMock(content) {
  // Pattern 1: Full destructuring with named aria props
  // ({ children, className, style, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: ...)
  //   => ({ children, className, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: ...)
  let updated = content.replace(
    /\(\{\s*children,\s*className,\s*style,\s*role,\s*'aria-modal':\s*ariaModal,\s*'aria-label':\s*ariaLabel,\s*\.\.\.\s*rest\s*\}:/g,
    "({ children, className, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }:"
  );

  // Pattern 2: Short destructuring
  // ({ children, className, style, ...rest }: ...)
  //   => ({ children, className, ...rest }: ...)
  updated = updated.replace(
    /\(\{\s*children,\s*className,\s*style,\s*\.\.\.\s*rest\s*\}:/g,
    '({ children, className, ...rest }:'
  );

  // Remove `style={style}` from JSX — match spaces before and after
  updated = updated.replace(/\s+style=\{style\}\s+/g, ' ');

  return updated;
}

function processFile(filePath) {
  try {
    let content = fs.readFileSync(filePath, 'utf-8');
    const original = content;

    content = removeStyleFromMock(content);

    if (content === original) {
      return { file: filePath, changed: false };
    }

    if (process.argv.includes('--apply')) {
      fs.writeFileSync(filePath, content, 'utf-8');
      return { file: filePath, changed: true, applied: true };
    }

    return { file: filePath, changed: true, applied: false };
  } catch (err) {
    return { file: filePath, error: err.message };
  }
}

// Main
const results = files.map(processFile);

// Print results
console.log('\n=== Codemod Results ===\n');
let totalChanged = 0;
for (const result of results) {
  if (result.error) {
    console.log(`❌ ${result.file}: ${result.error}`);
  } else if (result.changed) {
    console.log(`✅ ${result.file}: modified ${result.applied ? '(APPLIED)' : '(dry-run)'}`);
    totalChanged++;
  } else {
    console.log(`⊘ ${result.file}: no changes`);
  }
}

console.log(`\nTotal changed: ${totalChanged}/${files.length}`);
console.log(`Mode: ${process.argv.includes('--apply') ? 'APPLY' : 'DRY RUN'}`);

process.exit(results.some(r => r.error) ? 1 : 0);
