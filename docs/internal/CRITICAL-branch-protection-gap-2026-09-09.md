# CRITICAL — `main`'s branch protection has been inert since it was configured

**Status:** OPEN. **This is a repository SETTINGS change, not a code change.**
It cannot be fixed on a branch and needs someone with admin rights.

Found by the silent-failure audit; **independently re-verified against the live
GitHub API** before being written down.

## The finding

`main` requires nine status checks. **GitHub emits eight of them under different
names, so they can never report.** Only `CLA Assistant` matches a real check.

Verified with `gh api repos/elicify-ai/omnipus/branches/main/protection/required_status_checks`:

```
contexts: ['typecheck', 'wire-types-lint', 'verify-contracts', 'lint',
           'test', 'security', 'perf-smoke', 'playwright', 'CLA Assistant']
```

Verified against the 55 check names actually emitted on merged PR #681
(`gh api repos/.../commits/8226731e.../check-runs`):

| Required context | Present in the 55 emitted names? |
|---|---|
| `typecheck` | **MISSING** |
| `wire-types-lint` | **MISSING** |
| `verify-contracts` | **MISSING** |
| `lint` | **MISSING** |
| `test` | **MISSING** |
| `security` | **MISSING** |
| `perf-smoke` | **MISSING** |
| `playwright` | **MISSING** |
| `CLA Assistant` | present |

GitHub names a check run after the job's `name:` field, and each of those jobs
sets a human-readable one — `TypeScript Type Check`, `Wire-Types Lint`,
`Verify Contracts`, `Linter`, `Tests`, `Security Tests`, `Perf Smoke`,
`E2E — <group>`. The required list uses the job IDs instead.

## Consequence

**`CLA Assistant` is the only functioning merge gate on `main`.** Every other
gate — including all of this branch's work — is advisory. The jobs run, go
green, and block nothing.

A required context that never reports leaves a PR pinned at *"Expected — waiting
for status"* forever. That is a coherent explanation for this repository's
history of `--admin` merges, including the v0.1.0 hotfix incident CLAUDE.md
records as unacceptable: the merge button could not turn green on its own, so
the bypass looked like the only way forward.

## The fix already exists in the repo and is not wired up

`.github/workflows/pr.yml:2330-2345` documents this exact defect, and the
aggregator job `ci-required` (display name **`CI`**, `pr.yml:2352`) exists
precisely to be the single required context. **`CI` is emitted on every PR and is
NOT in the required list.**

**Fix:** in Settings → Branches → `main`, require **`CI`** and remove the eight
names that never report. Keep `CLA Assistant`.

**Before doing that, check one thing:** `hermetic-build` is reported as absent
from `ci-required`'s `needs:` (`pr.yml:2354-2377`), so its failure would not turn
`CI` red. Requiring `CI` while that gap exists would make `CI` authoritative but
still blind to that one job. Verify and close that first, or accept it knowingly.

## Why this outranks every code finding

Every defect fixed on this branch is enforced by gates that currently cannot
block anything. Fixing the code without fixing this leaves the same class of
regression free to land again the moment nobody is watching by hand.
