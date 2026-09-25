# scripts/hooks/

Git hooks for the dev-team coordination machinery (design
`docs/internal/design/dev-team-setup-design-2026-09-25.md` section 5.9, "The
pre-push hook (N4)"). Nothing here is installed by this commit — see
"Installing" below. Installation is a founder decision at rollout (design
10.1 stage 1e).

## Files

| File | Purpose |
|---|---|
| `pre-push-ledger-check` | git `pre-push` hook. Blocks a push to the integration branch when the ledger shows an active hold covering it, or when the pusher holds no landing lock for it. Passes through every other branch untouched. Full behaviour and the exact ledger line formats it parses are documented in the script's own header comment. |

## Installing (NOT done by this commit)

Worktrees of one checkout share hooks — that is exactly the reach wanted
(one ledger, every worktree of this checkout). Two ways to wire it in, once
the founder approves activation:

1. **Shared hooks directory (recommended — survives new worktrees and new
   clones of the same remote configuration automatically once set per
   checkout):**
   ```sh
   git config core.hooksPath scripts/hooks
   ```
   Git will then run `scripts/hooks/pre-push-ledger-check` directly on every
   `git push` from this checkout (all its worktrees), no copy needed. The
   file must stay executable (`chmod +x`, already set in this commit).

2. **Per-checkout copy (fallback if `core.hooksPath` is not wanted for some
   other reason):**
   ```sh
   cp scripts/hooks/pre-push-ledger-check "$(git rev-parse --git-common-dir)/hooks/pre-push"
   chmod +x "$(git rev-parse --git-common-dir)/hooks/pre-push"
   ```
   `--git-common-dir` (not `--git-dir`) is deliberate: in a worktree,
   `--git-dir` points at the worktree's private administrative area, but
   hooks live in the *common* `.git/hooks/`, shared by every worktree of the
   checkout.

Either way, every session's shell must export
`OMNIPUS_INTEGRATION_BRANCH=<current integration branch>` before pushing —
the hook checks nothing at all when this is unset (see the script header for
why: the integration branch name is never hard-coded anywhere in the repo,
design 5.7).

## Uninstalling / bypassing

Bypassing the hook is a founder-only act (design 5.9: "The hook is a
backstop, not the rule: bypassing it is a founder-only act, and a hook block
is always visible, never silent"). `git push --no-verify` skips all hooks
including this one; `git config --unset core.hooksPath` removes the wiring
entirely.

## Self-test

`scripts/hooks/pre-push-ledger-check.selftest.sh` proves the hook in a
temporary scratch git repository (never against this checkout or the real
ledger) — see that script's header for what it proves and how to run it.
