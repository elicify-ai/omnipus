# scripts/hooks/

Git hooks for the dev-team coordination machinery (design
`docs/internal/design/dev-team-setup-design-2026-09-25.md` section 5.9, "The
pre-push hook (N4)"). Nothing here is installed by this commit — see
"Installing" below. Installation is a founder decision at rollout (design
10.1 stage 1e).

## Files

| File | Purpose |
|---|---|
| `pre-push-ledger-check` | git `pre-push` hook. Blocks a push to the integration branch when the ledger shows an active hold covering it, when the pusher holds no landing lock for it, or when the lock is held by a different squad (`OMNIPUS_SQUAD_ID` unset or mismatched — A4). Passes through every other branch untouched. Full behaviour and the exact ledger line formats it parses are documented in the script's own header comment. |

## Installing (NOT done by this commit)

Worktrees of one checkout share hooks — that is exactly the reach wanted
(one ledger, every worktree of this checkout). **Recommended install — copy
into the common hooks directory:**

```sh
cp scripts/hooks/pre-push-ledger-check "$(git rev-parse --git-common-dir)/hooks/pre-push"
chmod +x "$(git rev-parse --git-common-dir)/hooks/pre-push"
```

`--git-common-dir` (not `--git-dir`) is deliberate: in a worktree,
`--git-dir` points at the worktree's private administrative area, but
hooks live in the *common* `.git/hooks/`, shared by every worktree of the
checkout. This is the recommended path because it composes safely with
whatever else already lives in `.git/hooks/` — including the existing
mutation-guard `pre-commit` hook, which this install never touches.

**Do NOT install via `git config core.hooksPath scripts/hooks`.** That
setting makes git look in `scripts/hooks/` for a file named exactly
`pre-push` — this file is named `pre-push-ledger-check`, so `core.hooksPath`
alone would run nothing at all, silently. Setting `core.hooksPath` also
replaces the *entire* hooks lookup, so it stops the existing `pre-commit`
mutation-guard hook (currently installed in `.git/hooks/`) from running,
unless that hook is copied into `scripts/hooks/pre-commit` too in the same
change. If `core.hooksPath` is ever wanted for its other benefits (it
survives new worktrees and clones automatically), a
`scripts/hooks/pre-push` wrapper that execs `pre-push-ledger-check`, plus a
copy of the mutation-guard hook alongside it, must land together — not this
copy-based install, which needs neither.

Either way, every session's shell must export
`OMNIPUS_INTEGRATION_BRANCH=<current integration branch>` **and**
`OMNIPUS_SQUAD_ID=<your squad's id, or `team-lead` for team-lead's own
direct-work landings>` before pushing — the hook checks nothing at all when
the branch variable is unset (see the script header for why: the
integration branch name is never hard-coded anywhere in the repo, design
5.7), and it blocks the push outright when the squad variable is unset,
because it otherwise cannot tell the pusher apart from any other squad
holding a different lock (A4).

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
