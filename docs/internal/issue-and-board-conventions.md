# Issue & Project Board Conventions

How issues are classified and tracked in this repo. **Read this before filing or triaging an issue** — humans and agents alike. The conventions below are enforced partly by automation and partly by discipline; following them keeps the board honest.

Repo: `elicify-ai/omnipus` · Project board: **#3 "Omnipus Development"** (`https://github.com/orgs/elicify-ai/projects/3`).

## The two classification axes

Classification is split into two independent axes. **Do not mix them.**

### Type — *what kind of work this is* (exactly one, mutually exclusive)

Every issue has exactly one **issue Type** (a GitHub org-level Issue Type, not a label):

| Type | Use for |
|---|---|
| **Bug** | An unexpected problem or broken behavior. |
| **Feature** | New functionality, an enhancement, or a capability request. |
| **Task** | A specific piece of work that isn't a bug or feature — chores, tech-debt, docs, CI, refactors, tests. |
| **Epic** | A container of related issues tracked via sub-issues (sprints, milestone rollups, multi-issue features). |

Type is the **single source of truth for "kind."** The old `bug` / `enhancement` labels are **retired and deleted** — never recreate or apply them; set the Type instead.

### Labels — *cross-cutting facets* (zero or more, can combine)

Labels describe orthogonal attributes that can apply to any Type:

- **Priority** (set one): `priority:P0-critical`, `priority:P1-high`, `priority:P2-medium`, `priority:P3-low`
- **Area** (set one or more): `area:gateway`, `area:agent-loop`, `area:channels`, `area:tools`, `area:credentials`, `area:sandbox`, `area:audit`, `area:memory`, `area:spa`, `area:contracts`, `area:ci`, `area:docs`
- **Cross-cutting**: `security`, `tech-debt`, `test-coverage`, `documentation`
- **PR/changelog only** (do **not** put on issues): `type:fix`, `type:feature`, `type:performance`, `type:refactor`, `type:breaking-change`, `dependencies`, `github_actions`. These are applied to **pull requests** (auto-derived from conventional-commit titles) and drive `release-drafter`. They are *not* the issue-kind axis — that's Type.

## Milestones

Assign the release the work belongs to (or leave unset for triage):

`v0.1 — Stabilize` · `v0.2 — Security Hardening` · `v0.3 — Rooms` · `v1.0 — Feature Parity`

When unsure which release, **leave the milestone empty** rather than guessing — an untriaged issue is better than a mis-scoped one.

## The project board

Board **#3** auto-tracks issues. Relevant fields:

- **Status** (single-select): `Backlog` → `Ready` → `In Progress` → `In Review` → `Done`.
- **Priority**: mirrors the priority label.
- **Sprint** (single-select: `Sprint 1`, `Sprint 2`, …): the planning axis. There is **no iteration field** — sprints are this single-select plus Epic bundling. New sprints are added as options to this field.
- **Estimate**, **Target date**: optional.

### Automation — what you must NOT do by hand

- **New issues auto-add to the board as `Backlog`.** Do not manually add an issue to the board, and do not set its initial status.
- **Closing an issue auto-moves it to `Done`.** Do not set `Done` by hand.
- You *do* set: **Sprint** (when planning), and promote **Status** (`Backlog`→`Ready`→`In Progress`→`In Review`) as work proceeds.

## How to file an issue (agent recipe)

`gh` (2.88) has **no `--type` flag**, so the Type is set via GraphQL after creation. Humans using the issue **templates** get the Type automatically (the templates set `type:` in frontmatter) — prefer templates in the UI. Programmatically:

```bash
REPO=elicify-ai/omnipus

# 1. Create with title + labels (priority + area). Use a body file with the
#    template sections (Summary / Steps / Actual / Expected for bugs).
URL=$(gh issue create --repo "$REPO" \
  --title "Clear, specific, action-oriented title" \
  --body-file /tmp/body.md \
  --label priority:P2-medium --label area:tools)
N=${URL##*/}

# 2. Set the Type (no gh flag exists — use GraphQL). Type IDs below.
NID=$(gh issue view "$N" --repo "$REPO" --json id --jq .id)
gh api graphql -f query='mutation{updateIssueIssueType(input:{issueId:"'"$NID"'",issueTypeId:"<TYPE_ID>"}){issue{number issueType{name}}}}'

# 3. (Optional) milestone — only if you're confident of the release.
gh issue edit "$N" --repo "$REPO" --milestone "v0.2 — Security Hardening"

# The board auto-adds it as Backlog — do not add it manually.
```

**Type IDs** (look them up dynamically if in doubt — they're org-stable but not guaranteed forever):

```bash
gh api graphql -f query='query{organization(login:"elicify-ai"){issueTypes(first:20){nodes{id name}}}}'
```

| Type | ID (as of 2026-06) |
|---|---|
| Task | `IT_kwDOD-80Xs4B5NmM` |
| Bug | `IT_kwDOD-80Xs4B5NmN` |
| Feature | `IT_kwDOD-80Xs4B5NmO` |
| Epic | `IT_kwDOD-80Xs4CBoCr` |

### What a good issue body contains

- **Bug**: one-line summary, environment, numbered repro steps, actual vs expected behavior, and — when you can — `file:line` root-cause pointers.
- **Feature/Task**: the goal/use-case, proposed approach, and a clear definition of done (acceptance criteria).
- **Epic**: the goal + a checklist of child issues; then link them as real sub-issues (below).

## Epics and sub-issues

Bundle related work under an Epic (Type = Epic) and attach children as **sub-issues** (this drives the board's sub-issue progress bars):

```bash
PARENT=$(gh issue view <epic#>  --repo "$REPO" --json id --jq .id)
CHILD=$( gh issue view <child#> --repo "$REPO" --json id --jq .id)
gh api graphql -f query='mutation{addSubIssue(input:{issueId:"'"$PARENT"'",subIssueId:"'"$CHILD"'"}){issue{number}}}'
```

Sprints are tracked this way: a Sprint epic with its work items as sub-issues, all assigned to the same **Sprint** field value.

## Closing issues via PRs (mandatory)

**Every PR that resolves issues MUST auto-close them on merge via closing keywords.** Issues do not get closed by hand after a fix ships — the PR closes them. The only acceptable reason an issue stays open after its fixing PR merges is that the work genuinely isn't complete.

Put the keywords in the **PR description** (the body), with **one keyword per issue**:

```
Closes #264, closes #289, closes #283
```

Three rules that GitHub enforces — get any of them wrong and the issues silently stay open:

1. **One keyword per issue.** `Closes #264, #289` closes only `#264`; the rest are merely linked. Repeat the keyword: `Closes #264, closes #289`. Valid keywords: `close` / `closes` / `closed`, `fix` / `fixes` / `fixed`, `resolve` / `resolves` / `resolved`.
2. **Auto-close fires only when the PR merges into the *default* branch (`main`).** A PR merged into a non-default base (a release or `hotfix/*` branch) **links but does not close**. Closure then happens when that branch later merges to `main` — and only if the closing keywords are present in *that* merge's PR body. If you want the issues closed on this merge, target `main`.
3. **Keywords belong in the PR *description*, not only commit messages.** On **squash-merge** the commit-message keywords are unreliable; only the PR body is honored. (Real example: Sprint #258 / PR #292 shipped fixes for 8 issues but left them all open because the squash-merge omitted body-level keywords — they had to be closed manually afterward.)

If a PR genuinely can't auto-close (e.g. it must merge into a non-`main` base), it MUST still reference every issue it resolves, and whoever performs the eventual `main` merge is responsible for ensuring the keywords carry through — or closing the issues manually with a comment citing the PR.

## Quick don't-list

- ❌ Don't use `bug` / `enhancement` labels — they're retired; set the **Type**.
- ❌ Don't put `type:*` labels on issues — those are PR/changelog only.
- ❌ Don't manually add issues to the board or set initial/`Done` status — automation does it.
- ❌ Don't encode "kind" in both a label and the Type — Type is authoritative.
- ❌ Don't open a PR that resolves issues without `Closes #N` (one keyword per issue) in the **PR body** — and remember auto-close only fires on merge to `main`.
- ✅ Do set: Type (exactly one), a priority label, an area label, and a milestone when confident.
