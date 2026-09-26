# Read boundary consistency — search, read and list follow one rule (founder interview record)

Status: Interview complete — input to the ADR amendments and `plan-spec`
Issue: https://github.com/elicify-ai/omnipus/issues/920
Captured at commit: d35e386 (`release/v0.1.1`)
Interview date: 2026-09-26 (team-lead, founder via question tool)

This is the founder-interview output. It records the founder's decisions and the as-is findings
they rest on. It is not the spec.

## As-is findings (architect investigation, 2026-09-26, spot-checked by team-lead)

- `read_file`, `list_directory`, `send_file` resolve through
  `pkg/tools/resolvepath.go::ResolvePath` (the single path chokepoint, ADR-063 D2): open
  anywhere except the secret set, unless `ReadConfined`.
- `grep` never calls `ResolvePath`: `pkg/tools/grep.go::validateGrepScope` refuses absolute
  paths and `..`; roots are `os.OpenRoot(WorkDir)` plus the workspace's mounts. It applies
  `fspolicy.IsCarveOut` via `carveOutFS`, but lacks the ADR-072 D10.3 skills-registry gate,
  the metadata guard, the `ReadConfined` branch and the path-refusal / file-read audit rows.
- The narrowing comes from ADR-081 ("Unified Library search, general file search, and the grep
  engine") D4 and `unified-search-and-grep-spec.md` FR-020 / US-3 AS-7, which never reconciled
  with ADR-062 / ADR-063 D2.
- Auto-approve classes (`pkg/tools/auto_approve.go::autoApproveClasses`): `grep` = `AutoRuns`;
  `read_file`, `list_directory` = `AutoRunsIfArgs` (ask outside workspace/mounts).
- Read-confined agents (Judge, Plan Supervisor): grep includes mounts, `read_file` refuses them.
- `grep`'s absolute-path check is `HasPrefix("/")` — not Windows-safe.

## Decisions Log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D1 | Direction | `grep` uses the same single read decision as `read_file`/`list_directory` (`ResolvePath`), including absolute paths and `..`; the secret set, other agents' and other workspaces' files stay unreachable | One read boundary defined once (issue ask); narrowing reads would reverse ADR-062/063 and is fiction while `bash` reads openly | Interview | 2026-09-26 |
| D2 | Approval for search outside the workspace | The configured tool policy decides. With Auto-approve on, search just runs — also outside the workspace | Founder: "it depends on the configured tool policy; when auto-approve is on it must just run" | Interview | 2026-09-26 |
| D3 | Consistency of the reading tools under Auto-approve | `read_file` and `list_directory` follow the same rule: with Auto-approve on they run outside the workspace without asking (no longer `AutoRunsIfArgs` for location) | All three read tools behave the same; team-lead raised the contradiction, founder chose consistency. Amends ADR-092 D9 (J2) — needs a dated correction | Interview | 2026-09-26 |
| D4 | Personal secret folders outside Omnipus (`~/.ssh`, `~/.aws`, `~/.gnupg`, browser profiles) | Out of scope for #920; tracked in a separate high-priority issue covering ALL tools | Global decision affecting every tool; gets its own security review. Risk raised by D2/D3 acknowledged | Interview | 2026-09-26 |
| D5 | Default search area when no `path` is given | Workspace plus its mounted folders (unchanged) | No accidental machine-wide walks | Interview | 2026-09-26 |
| D6 | Read-confined agents (Judge, Plan Supervisor) | Workspace plus its mounted folders, for both `grep` and `read_file`/`list_directory` | The reviewed work can live in a mount (ADR-090 "reviewed workspace under existing path/mount policy"); security-lead reviews the `ReadConfined` change | Interview | 2026-09-26 |
| D7 | Search capacity (2 walk slots shared with the Library search bar; 10 s / 50k files) | Keep current limits; revisit only on measured contention | No evidence of contention yet | Interview | 2026-09-26 |

## Security notes (must appear in the spec's Security section)

- D2 + D3 mean that under Auto-approve an agent can read and search anywhere outside the secret
  set without a prompt. Protected (secret-set) files stay blocked by `fspolicy.IsCarveOut`.
  Unprotected personal secrets outside `$OMNIPUS_HOME` are the accepted residual risk until the
  D4 issue lands. With a tool policy of `ask` and Auto-approve off, the user is still asked.
- The widened `grep` must close the gaps it would otherwise open: skills-registry instruction
  files (ADR-072 D10.3), metadata guard, symlink containment inside the walk, audit rows for
  refusals and searched roots, Windows absolute-path parsing (`filepath.IsAbs`).

## Documents to correct (dated corrections, architect)

- ADR-081 D4 and `unified-search-and-grep-spec.md` FR-020 / US-2 AS-5 / US-3 AS-7.
- ADR-092 D9 auto-approve table (J2 for `read_file`/`list_directory`; `grep` class).
- Tool `Description()` text for `grep`, `read_file`, `list_directory` (prometheus-prompt-engineer).

## Dependency Graph & Implementation Order

1. Architect: dated amendments to ADR-081 D4 and ADR-092 D9 → one `grill-spec` ADR-mode round
   → founder interview on its questions → one correction round.
2. Spec (`plan-spec`, or an amendment of `unified-search-and-grep-spec.md`) → two `grill-spec`
   rounds with founder interviews between.
3. RED (qa-lead): parity matrix grep vs read_file vs list_directory over ~20 path kinds, read-
   confined cases, auto-approve under Auto on/off with allow/ask/deny, skills gate, audit,
   Windows paths.
4. GREEN: backend-lead (`grep.go`, `auto_approve.go`, `resolvepath.go` ReadConfined,
   docsref regeneration) · prometheus-prompt-engineer (descriptions), in parallel.
5. CHECK (qa-lead, other instance) → 8-reviewer gate (security-lead mandatory) → founder's yes.
