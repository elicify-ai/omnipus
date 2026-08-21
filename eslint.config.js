// ESLint 9+ flat config.
//
// Baseline scope (deliberately minimal — see issue #494 for why this file
// exists at all: this repo had NO eslint config, so there was nowhere to
// put a cross-cutting UI rule). Enabled rule sets, at their recommended
// severities, unmodified:
//   - @eslint/js recommended
//   - typescript-eslint recommended (non-type-checked variant — the
//     type-checked variant is too slow/noisy to be a useful CI baseline)
//
// This config does NOT downgrade, disable, or "warn"-ify any rule to make
// the current tree pass. Pre-existing violations are fixed in application
// code, not silenced in config — see the parallel cleanup work tracked
// against this branch. (An earlier draft of this file did soften several
// rules to reach a false green; that approach was rejected — see repo
// .golangci.yaml's 59-linter `disable:` list for what that pattern turns
// into over time.)
//
// Formatting/style is intentionally out of scope: this repo has no
// Prettier/Biome config, so no formatter "owns" style yet, and this config
// does not try to fill that gap either — it only covers correctness rules.
//
// TODO(#494): add a custom rule (or a scoped `no-restricted-syntax` /
// import-boundary rule) here that enforces every tool-UI registration
// consults the visibility gate before rendering. This is the rule the
// audit asked for; it needs this config file to exist first.

import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import jsxA11y from 'eslint-plugin-jsx-a11y';

export default tseslint.config(
  {
    // This repo is a monorepo: the root package.json (@omnipus/ui) owns
    // `src/` (app + component library source) and `tests/` (Playwright
    // e2e, wired to the `test:e2e` script). Everything else at the repo
    // root belongs to other tooling/ownership and is not this package's
    // source — excluding it is a scope boundary, not a rule weakening:
    //   - pkg/**            Go backend; pkg/gateway/spa/assets/** is its
    //                       embedded, already-minified frontend build
    //                       output, not source
    //   - dist/**, build/**, coverage/**, playwright-report/**,
    //     test-results/**   build/test output, not source
    //   - docs/**           documentation + one-off Node scripts, not
    //                       part of the UI package
    //   - scripts/**, spikes/**  ad-hoc/prototype scripts, unmaintained
    //     to lint standards
    //   - packages/**       has its own package.json (@omnipus/ui
    //                       component library) and should get its own
    //                       lint config when it needs one
    //   - system/**, workspace/**, contracts/**, config/**, deploy/**,
    //     docker/**         non-JS/TS project areas
    ignores: [
      'dist/**',
      'build/**',
      'pkg/**',
      'docs/**',
      'scripts/**',
      'spikes/**',
      'packages/**',
      'system/**',
      'workspace/**',
      'contracts/**',
      'config/**',
      'deploy/**',
      'docker/**',
      'src/lib/api/generated/**',
      'node_modules/**',
      '*.config.*',
      'coverage/**',
      'playwright-report/**',
      'test-results/**',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    // Registered but deliberately NOT configured with any rules: the
    // existing codebase already carries `// eslint-disable-next-line
    // react-hooks/exhaustive-deps` and `jsx-a11y/*` comments from a prior
    // (now-absent) lint setup. Without these plugins registered, ESLint's
    // disable-directive validation hard-errors with "Definition for rule
    // '<name>' was not found" on every one of those comments — a config
    // resolution failure, not a lint finding. Registering the plugin makes
    // the rule name resolvable (the pre-existing directive then reads as an
    // "unused disable directive" warning, same as any other rule outside
    // our enabled set) without turning on react-hooks/jsx-a11y linting
    // itself — that stays out of scope per the "enable only @eslint/js +
    // typescript-eslint" baseline. Do not add rules here without
    // deliberately expanding scope.
    plugins: {
      'react-hooks': reactHooks,
      'jsx-a11y': jsxA11y,
    },
  },
  // Node-run scripts and harnesses execute under Node, not in a browser, so
  // `process`/`console`/`fetch`/`AbortSignal` are legitimately defined there.
  // Without this, the base `no-undef` rule judges them by browser globals and
  // reports 27 false positives (all in tests/sandbox-uat/outside-playwright.mjs).
  // A false RED is as corrosive as a false green — it trains people to ignore
  // the linter. This declares the real runtime; it relaxes no rule.
  {
    files: ['**/*.mjs', '**/*.cjs', 'scripts/**/*.js'],
    languageOptions: {
      globals: {
        process: 'readonly',
        console: 'readonly',
        fetch: 'readonly',
        AbortSignal: 'readonly',
        AbortController: 'readonly',
        URL: 'readonly',
        setTimeout: 'readonly',
        clearTimeout: 'readonly',
        Buffer: 'readonly',
        __dirname: 'readonly',
        __filename: 'readonly',
        require: 'readonly',
        TextEncoder: 'readonly',
        TextDecoder: 'readonly',
        structuredClone: 'readonly',
      },
    },
  },
);
