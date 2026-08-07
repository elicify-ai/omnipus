// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package gitevidence is the workspace git evidence layer (ADR-053 §3d,
// spec "unified-goal-plan-subagent-spec.md" §Group H, FR-151..FR-159, User
// Story 10/11). It embeds go-git (github.com/go-git/go-git/v5, the
// wf/gogit-spike embedding-gate spike's verdict: GO, with three caveats
// this package's design honors — see below) to auto-init a
// hidden git repository under a workspace's work/ directory, take
// write-set-scoped, serialized boundary commits at task/attempt/agent
// boundaries, and serve per-attempt diffs as rung-1 evidence to the plan
// Judge.
//
// # Scope
//
// This package owns:
//   - Open — the work/-dir auto-init hook (idempotent; degrades, never
//     double-inits, on a pre-existing user repo at or above the dir).
//   - (*Repo).Commit — write-set-scoped, mutex-serialized boundary commits,
//     with contention detection (an out-of-write-set dirty path is
//     surfaced, never silently swallowed — G-15/CRIT-2), a size guard
//     (OBS-3), and a sensitive-value-registry exclusion scan (MIN-5).
//   - (*Repo).Diff — path-scoped diff evidence between two commits, the
//     shape the Judge consumes (G-3/G-12).
//   - (*Repo).CheckIntegrity — HEAD-divergence detection ("evidence
//     integrity lost", D17/G-15).
//   - OpenIsolatedCheckout / SelectRung — the isolation ladder helper
//     (system-git worktree → go-git clone → subdir, FR-154/FR-157),
//     returning which rung is actually active so callers (the planner,
//     the Judge) know the runtime's capability.
//
// This package explicitly does NOT own: the `.git` deny-by-operation agent
// tool surface or its Landlock/bash-policy pairing (security-lead,
// FR-152 caveat 3 — this package only creates a byte-for-byte standard
// `.git` directory; it is on the sandbox layer to keep an agent's shell
// from reaching it directly); plan-lint's write-set-disjointness rejection
// at approve (FR-156, a different wave); and `last_unmet_terminal_signature`
// / any plan-record persistence (that belongs to the plan record, a
// different wave) — this package is stateless evidence plumbing over a
// single `work/` git repository, not a plan-state store.
//
// # The three spike caveats this package's design deliberately honors
//
//  1. go-git has no `git worktree add` equivalent (Repository.Worktree()
//     returns exactly one Worktree, fixed at Init/Open/Clone time). The
//     isolation ladder therefore treats a go-git "clone" rung as a genuine
//     full local copy (~1.27s / full independent object store for a 20 MB
//     repo, zero shared inodes) — never as a cheap linked-worktree
//     substitute — and prefers a real `git worktree add` (system-git rung)
//     when the `git` binary is available, since a repo produced purely via
//     go-git is proven byte-for-byte compatible with the real git CLI.
//  2. Media blobs bloat `.git` ~1:1 with changed bytes; RepackObjects
//     (go-git's `git gc` analogue) consolidates loose objects into
//     packfiles but does NOT shrink total bytes for incompressible
//     re-encoded media. The SizeGuard exists precisely because git-level
//     compaction cannot be relied on — callers should keep write-sets
//     scoped to source/output paths and route large generated media
//     through Omnipus's existing file-based artifact storage instead of
//     the auto-commit path.
//  3. A repo committed purely via go-git is indistinguishable on disk from
//     one built by the real `git` binary — `git commit --amend` against a
//     go-git-managed repo succeeds trivially with no in-process gate
//     involved. This package's Commit method is the ONLY writer this
//     package exposes (no exec of `git commit`/`amend`/`rebase`/`rm`
//     anywhere in this package); closing the loophole end-to-end requires
//     the sandbox-level `.git/` block that is explicitly out of scope
//     here (see "Scope" above).
package gitevidence
