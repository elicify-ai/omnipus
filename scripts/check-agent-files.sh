#!/usr/bin/env bash
# check-agent-files.sh
#
# Guard: the agent-file and dev-team-skill guard required by
# docs/internal/design/dev-team-setup-design-2026-09-25.md section 8
# ("Guard script design"). Implements all thirteen checks from 8.1 and the
# false-positive handling of 8.2.
#
# WHAT IS CHECKED (design 8.1, checks 1-13)
#
#   1  Agent frontmatter is valid: unique `name` matching the filename, a
#      non-empty `description` (handles `>-`/`|` block scalars).
#   2  Every repo-relative path cited in an agent file or a dev-team skill
#      body exists on disk.
#   3  Every skill named in an agent file exists under .claude/skills/ (any
#      depth) or is on the user-level allowlist; every agent file names
#      omnipus-shared-rules.
#   4  Banned command patterns are absent (untagged `go build|vet|test
#      ./...`, `npx tsc --noEmit`, bare `git stash`/`git stash pop`,
#      `gh pr merge --admin|--auto`, `--dangerously-skip`, AI co-author
#      trailer instructions).
#   5  Retired-surface names are absent from ownership/instruction lines.
#   6  Role-skill isolation: an agent file only names skills its role is
#      mapped to; the shared skill exists; root CLAUDE.md references it.
#   7  Every `teammates:` entry is a real agent file, a plugin-reviewer
#      allowlist name, or marked `(external)`.
#   8  Every agent file and every non-vendored dev-team skill carries a
#      `Last reviewed: YYYY-MM-DD` line.
#   9  Vendored skills (elicify-test-writing, test-integrity-audit) carry a
#      complete SOURCE.yaml; upstream drift is a best-effort WARN, never a
#      failure, driven by an optional `local_clone:` path compared with
#      `git rev-parse <source_branch>` against the pinned `source_commit`.
#  10  No `release/vN...`-shaped literal anywhere under .claude/agents/ or
#      .claude/skills/.
#  14  Every `tools:` allow-list in an agent file's frontmatter names
#      `Skill` (design 4.2) — otherwise an on-demand skill load is
#      unreachable for that role. New check surfaced by review C9.
#  11  Developer/reviewer discipline blocks are present where the design's
#      classification table requires them, and byte-identical to the
#      canonical source .claude/templates/agent-discipline.md.
#  12  .claude/templates/plugin-reviewer-dispatch.md exists, carries the
#      reviewer-discipline block in sync with the canonical source, and the
#      instruction to load omnipus-shared-rules with the Skill tool;
#      team-lead.md names it.
#  13  No `model:` frontmatter key in any agent file (frontmatter-scoped —
#      a body-text example, such as prometheus-prompt-engineer.md's
#      skeleton line, cannot false-positive).
#
# VENDORED-DIRECTORY EXEMPTION SCOPE (review C3, narrowed from the earlier
# blanket exemption): a vendored directory (one carrying a SOURCE.yaml) is
# exempt from check 8 ("Last reviewed" header) ONLY — its date lives in
# SOURCE.yaml instead (design lead default C4). It is NOT exempt from
# checks 2, 4, 5, 6 or 10: those scan every file in the vendored tree (not
# just the top-level SKILL.md), because the real violations review C3
# found — a `git stash` teaching line and an untagged whole-repo `go test`
# listing — lived in `knowledge/*.md` files a SKILL.md-only scan would
# still miss even without the blanket skip.
#
# ASSUMPTION — discipline-block markers (checks 11, 12). The design names
# the canonical source (.claude/templates/agent-discipline.md) and the three
# sections it holds (shared traits, developer rules, reviewer rules) but
# does not specify how a section's boundaries are marked in the file. This
# guard adopts the convention:
#   <!-- agent-discipline:<section>:start -->  ...  <!-- agent-discipline:<section>:end -->
# with <section> one of shared-traits, developer-rules, reviewer-rules. This
# is an open question for whoever authors the canonical template
# (prometheus-prompt-engineer, per governance section 9) — reported as a
# finding/ambiguity, not silently assumed away. Until that file exists,
# check 11/12 report it missing rather than crash (see below).
#
# FALSE-POSITIVE HANDLING (design 8.2)
#
#   - Line-level marker `# agent-guard: allow` exempts a single line from
#     checks 2, 4, 5, 6 and 10 — never a whole file.
#   - Check 2 only matches path-shaped strings that open with a known repo
#     root immediately followed by `/`, and are not themselves preceded by
#     a word character, `/` or `.` — this is what keeps URLs, `~/.omnipus/`
#     runtime paths, and the coordination ledger's absolute outside-repo
#     path from ever matching (embedded inside a longer path, they are
#     always preceded by `/`).
#   - Check 3 passes a named skill against the user-level allowlist
#     (webapp-testing, elicify-ui-ux-design) before failing it as phantom.
#   - Check 7 reads only the explicit `teammates:` frontmatter list —
#     prose mentions of another role's name are never checked.
#   - Checks 1 and 13 parse the YAML frontmatter block only, so a
#     `description: >-` block scalar (check 1) or a body-text `model:`
#     example (check 13) cannot false-positive.
#   - Check 9's upstream-drift comparison is best-effort and warn-only: it
#     never turns the guard red, and prints nothing when the upstream
#     cannot be reached locally.
#
# OUTPUT CONTRACT (mirrors scripts/check-agents-md-sync.sh)
#
#   Exactly one line per finding, "check<N>: <file-or-scope>: <problem>".
#   Exit 0 clean, 1 on any finding, 2 on internal error (can't run).
#
# DISCOVERY (scripts/guards.sh conventions)
#
#   This file is discovered by scripts/guards.sh's scripts/check-*.sh glob.
#   Its proof-of-failure companion is scripts/check-agent-files.test.sh (a
#   <name>.test.sh companion, subtracted from the guard list and run before
#   this guard, per scripts/guards.sh's own rules). No wiring changes
#   anywhere else — adding this guard is these two files, nothing more.
#
# Usage:
#   bash scripts/check-agent-files.sh
#
# Overrides (for the companion test only — never set these in CI):
#   REPO_ROOT   — repo root to scan. Defaults to this script's own parent
#                 directory's parent.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"

if [ ! -d "$REPO_ROOT" ]; then
  echo "check-agent-files: ERROR — REPO_ROOT is not a directory: $REPO_ROOT" >&2
  exit 2
fi

OUT="$(mktemp "${TMPDIR:-/tmp}/check-agent-files.XXXXXX")" || exit 2
trap 'rm -f "$OUT"' EXIT

python3 - "$REPO_ROOT" > "$OUT" <<'PYEOF'
import os
import re
import subprocess
import sys

REPO_ROOT = os.path.abspath(sys.argv[1])
AGENTS_DIR = os.path.join(REPO_ROOT, ".claude", "agents")
SKILLS_DIR = os.path.join(REPO_ROOT, ".claude", "skills")
TEMPLATES_DIR = os.path.join(REPO_ROOT, ".claude", "templates")
CANONICAL_DISCIPLINE = os.path.join(TEMPLATES_DIR, "agent-discipline.md")
DISPATCH_TEMPLATE = os.path.join(TEMPLATES_DIR, "plugin-reviewer-dispatch.md")
CLAUDE_MD = os.path.join(REPO_ROOT, "CLAUDE.md")

ALLOW_MARKER = "# agent-guard: allow"

DEV_TEAM_SKILLS = {
    "omnipus-shared-rules",
    "omnipus-backend-rules",
    "omnipus-frontend-rules",
    "omnipus-failure-triage",
    "elicify-test-writing",
    "test-integrity-audit",
    "omnipus-planning-orchestration",
    # Spec-process rewrite (S1/S2/S3/S4, hand-off to S5): the four fixed
    # spec skills are repo-owned dev-team skills, not vendored (no
    # SOURCE.yaml — the "Last reviewed" header check therefore DOES apply
    # to them, same as every other non-vendored entry above). Before this
    # addition they were invisible to every content check (2, 4, 5, 10)
    # and to check 8's header requirement — a false green (S1/S3/S4
    # hand-off, spec-process rewrite).
    "plan-spec",
    "grill-spec",
    "grill-code",
    "spec-sync",
}
VENDORED_SKILLS = {"elicify-test-writing", "test-integrity-audit"}
USER_LEVEL_ALLOWLIST = {"webapp-testing", "elicify-ui-ux-design"}
PLUGIN_REVIEWERS = {
    "pr-review-toolkit:code-reviewer",
    "pr-review-toolkit:code-simplifier",
    "pr-review-toolkit:comment-analyzer",
    "pr-review-toolkit:pr-test-analyzer",
    "pr-review-toolkit:silent-failure-hunter",
    "pr-review-toolkit:type-design-analyzer",
}
DEVELOPER_SIDE = {"backend-lead", "frontend-lead", "uat-tester", "prometheus-prompt-engineer"}
REVIEWER_SIDE = {"security-lead", "uat-validator", "docs-verifier"}
BOTH_SIDE = {"qa-lead", "architect"}
ORCH_SHARED_ONLY = {"squad-lead"}
# team-lead is exempt from check 11 (design 8.1 check 11).

findings = []
warnings = []


def relpath(path):
    return os.path.relpath(path, REPO_ROOT)


def finding(check, msg):
    findings.append("check%d: %s" % (check, msg))


def warn(check, msg):
    warnings.append("WARN check%d: %s" % (check, msg))


def read_text(path):
    with open(path, "r", encoding="utf-8", errors="replace") as f:
        return f.read()


def allowed_line(line):
    return ALLOW_MARKER in line


def split_frontmatter(text):
    """Return (frontmatter_text_or_None, body_text)."""
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        return None, text
    for i in range(1, len(lines)):
        if lines[i].strip() == "---":
            return "\n".join(lines[1:i]), "\n".join(lines[i + 1:])
    return None, text


def parse_yaml_field(fm_text, key):
    """Minimal YAML subset parser for one top-level key.

    Returns (kind, value): kind in {"missing", "scalar", "blockscalar",
    "list"}. value is str for scalar/blockscalar, list[str] for list.
    """
    if fm_text is None:
        return "missing", None
    lines = fm_text.splitlines()
    pat = re.compile(r"^%s:\s*(.*)$" % re.escape(key))
    for idx, line in enumerate(lines):
        m = pat.match(line)
        if not m:
            continue
        rest = m.group(1).strip()
        if rest in (">-", ">", "|", "|-", ">+", "|+"):
            block = []
            j = idx + 1
            while j < len(lines) and (lines[j].strip() == "" or lines[j].startswith((" ", "\t"))):
                if lines[j].strip() != "":
                    block.append(lines[j].strip())
                j += 1
            return "blockscalar", "\n".join(block)
        if rest.startswith("[") and rest.endswith("]"):
            inner = rest[1:-1]
            items = [x.strip().strip("\"'") for x in inner.split(",") if x.strip()]
            return "list", items
        if rest == "":
            items = []
            j = idx + 1
            while j < len(lines) and (lines[j].strip() == "" or lines[j].startswith((" ", "\t"))):
                s = lines[j].strip()
                if s.startswith("- "):
                    items.append(s[2:].strip().strip("\"'"))
                j += 1
            if items:
                return "list", items
            return "scalar", ""
        return "scalar", rest.strip("\"'")
    return "missing", None


def extract_section(text, section):
    start_re = re.compile(r"<!--\s*agent-discipline:%s:start\s*-->" % re.escape(section))
    end_re = re.compile(r"<!--\s*agent-discipline:%s:end\s*-->" % re.escape(section))
    sm = start_re.search(text)
    if not sm:
        return None
    em = end_re.search(text, sm.end())
    if not em:
        return None
    return text[sm.end():em.start()]


# ─── Discovery ──────────────────────────────────────────────────────────────

agent_files = []
if os.path.isdir(AGENTS_DIR):
    for name in sorted(os.listdir(AGENTS_DIR)):
        p = os.path.join(AGENTS_DIR, name)
        if os.path.isfile(p) and name.endswith(".md"):
            agent_files.append(p)

skill_md_files = []  # (skill_name, path)
if os.path.isdir(SKILLS_DIR):
    for root, _dirs, files in os.walk(SKILLS_DIR):
        for fn in files:
            if fn == "SKILL.md":
                p = os.path.join(root, fn)
                skill_md_files.append((os.path.basename(root), p))

discovered_skill_names = {name for name, _p in skill_md_files}
dev_skill_files = [(n, p) for n, p in skill_md_files if n in DEV_TEAM_SKILLS]

# Vendored directories (design 6.4, lead default C4): any directory under
# .claude/skills/ that carries a SOURCE.yaml at its root is a vendored copy,
# byte-for-byte from an upstream repo. Detected generically by SOURCE.yaml's
# presence, not by the VENDORED_SKILLS name list, so a future vendored
# skill is picked up automatically.
#
# Review C3: the exemption from content checks (2, 4, 5, 10) that used to
# apply inside these directories is NOT in design 8.2 and hid real
# violations (a `git stash` teaching line and an untagged whole-repo `go
# test` listing, both inside knowledge/*.md files). Narrowed to check 8
# only (8.1's stated exemption) — see the file-header note above.


def is_text_file(path):
    try:
        with open(path, "rb") as f:
            chunk = f.read(4096)
        if b"\x00" in chunk:
            return False
        return True
    except OSError:
        return False


VENDORED_ROOT_DIRS = []
if os.path.isdir(SKILLS_DIR):
    for root, _dirs, files in os.walk(SKILLS_DIR):
        if "SOURCE.yaml" in files:
            VENDORED_ROOT_DIRS.append(os.path.abspath(root))


def is_vendored_path(path):
    ap = os.path.abspath(path)
    for d in VENDORED_ROOT_DIRS:
        if ap == d or ap.startswith(d + os.sep):
            return True
    return False


all_files_under_agents_and_skills = list(agent_files)
if os.path.isdir(SKILLS_DIR):
    for root, _dirs, files in os.walk(SKILLS_DIR):
        for fn in files:
            all_files_under_agents_and_skills.append(os.path.join(root, fn))

# cited_files feeds checks 2, 4, 5: every agent file, every non-vendored
# dev-team skill's SKILL.md (design's "dev-team skill body" scope), and —
# because a vendored skill is exempt from check 8 only, not from content
# checks — every file inside a vendored skill directory except SOURCE.yaml
# itself (pure provenance metadata, not instructional text).
cited_files = list(agent_files)
for _n, p in dev_skill_files:
    if not is_vendored_path(p):
        cited_files.append(p)
for _d in VENDORED_ROOT_DIRS:
    for root, _dirs, files in os.walk(_d):
        for fn in files:
            if fn == "SOURCE.yaml":
                continue
            p = os.path.join(root, fn)
            if is_text_file(p):
                cited_files.append(p)

agent_basenames = {os.path.splitext(os.path.basename(p))[0] for p in agent_files}

# ─── Check 1: frontmatter validity ────────────────────────────────────────

seen_names = {}
for path in agent_files:
    base = os.path.splitext(os.path.basename(path))[0]
    text = read_text(path)
    fm, _body = split_frontmatter(text)
    if fm is None:
        finding(1, "%s: no YAML frontmatter block found" % relpath(path))
        continue

    nkind, nval = parse_yaml_field(fm, "name")
    if nkind == "missing" or not nval:
        finding(1, "%s: missing or empty 'name' in frontmatter" % relpath(path))
    elif nkind == "list":
        finding(1, "%s: 'name' parsed as a list — malformed" % relpath(path))
    else:
        if nval != base:
            finding(1, "%s: frontmatter name '%s' does not match filename '%s'" % (relpath(path), nval, base))
        if nval in seen_names:
            finding(1, "%s: duplicate name '%s' also used by %s" % (relpath(path), nval, relpath(seen_names[nval])))
        else:
            seen_names[nval] = path

    dkind, dval = parse_yaml_field(fm, "description")
    if dkind == "missing":
        finding(1, "%s: missing 'description' in frontmatter" % relpath(path))
    elif dkind == "list":
        finding(1, "%s: 'description' parsed as a list — malformed" % relpath(path))
    elif not dval or not dval.strip():
        finding(1, "%s: empty 'description' in frontmatter" % relpath(path))

# ─── Check 13: no model: key (frontmatter-scoped) ─────────────────────────

model_re = re.compile(r"^model:\s*\S")
for path in agent_files:
    text = read_text(path)
    fm, _body = split_frontmatter(text)
    if fm is None:
        continue
    for line in fm.splitlines():
        if model_re.match(line):
            finding(13, "%s: frontmatter carries a banned 'model:' key" % relpath(path))
            break

# ─── Check 2: path citations exist ────────────────────────────────────────

ROOT_NAMES = ["docs", "pkg", "src", "scripts", "contracts", "cmd", "internal",
              "tests", "deploy", r"\.claude", "packages", "design-system",
              "ui", r"\.github"]
PATH_RE = re.compile(
    r"(?<![\w/.])(?:%s)/[A-Za-z0-9_.\-/]*" % "|".join(ROOT_NAMES)
)
TRAIL_STRIP = ".,)('\":;`"


def scan_paths_in_file(check_no, path):
    if not os.path.isfile(path):
        return
    for i, line in enumerate(read_text(path).splitlines(), start=1):
        if allowed_line(line):
            continue
        for m in PATH_RE.finditer(line):
            cand = m.group(0)
            while cand and cand[-1] in TRAIL_STRIP:
                cand = cand[:-1]
            if not cand or cand.endswith("/") and len(cand) <= 1:
                continue
            full = os.path.join(REPO_ROOT, cand)
            if not os.path.exists(full):
                finding(check_no, "%s:%d: cites path '%s' which does not exist" % (relpath(path), i, cand))


for f in cited_files:
    scan_paths_in_file(2, f)

# ─── Checks 3, 6: skill citations, existence, isolation ───────────────────

BACKTICK_RE = re.compile(r"`([a-zA-Z][a-zA-Z0-9_-]*)`")
SKILL_SHAPE_RE = re.compile(r"^[a-z][a-z0-9]*(-[a-z0-9]+)+$")


# Spec-process rewrite skills (S1/S2/S3/S4, hand-off to S5): named here so
# check 6's role-isolation gate applies to them at all — without this they
# fall outside isolation_universe() entirely and any role could cite any of
# the four with no check6 finding. LANES.md S5 task 5 fixes the mapping:
# team-lead and squad-lead may cite all four; architect may cite grill-spec
# only; qa-lead and the developers (backend-lead, frontend-lead) may cite
# grill-code only.
SPEC_PROCESS_SKILLS = {"plan-spec", "grill-spec", "grill-code", "spec-sync"}


def isolation_universe(name):
    if name.startswith("omnipus-"):
        return True
    if name.startswith("gitnexus"):
        return True
    if name in {"ux-heuristics-review", "elicify-ui-ux-design", "elicify-test-writing", "test-integrity-audit"}:
        return True
    if name in SPEC_PROCESS_SKILLS:
        return True
    return False


def role_allowed(role, skill):
    if skill == "omnipus-shared-rules":
        return True
    if role == "backend-lead":
        if skill in {"omnipus-backend-rules", "omnipus-failure-triage", "grill-code"}:
            return True
        return skill.startswith("gitnexus")
    if role == "frontend-lead":
        if skill in {"omnipus-frontend-rules", "omnipus-failure-triage", "omnipus-design-system",
                      "ux-heuristics-review", "elicify-ui-ux-design", "grill-code"}:
            return True
        return skill.startswith("gitnexus")
    if role == "security-lead":
        return skill.startswith("gitnexus")
    if role == "qa-lead":
        if skill in {"elicify-test-writing", "test-integrity-audit", "grill-code"}:
            return True
        return skill.startswith("gitnexus")
    if role == "architect":
        if skill in {"ux-heuristics-review", "elicify-ui-ux-design", "grill-spec"}:
            return True
        return skill.startswith("gitnexus")
    if role in {"team-lead", "squad-lead"}:
        if skill == "omnipus-planning-orchestration":
            return True
        return skill in SPEC_PROCESS_SKILLS
    # uat-tester, uat-validator, docs-verifier, prometheus-prompt-engineer:
    # only the shared skill (handled above).
    return False


def is_known_skill_shape(tok):
    # "Known skill names" (review C4): a backtick token counts as a skill
    # citation when it either names a discovered/allowlisted skill outright,
    # or matches a recognised skill-naming pattern (omnipus-*, gitnexus*) —
    # the latter is what lets a *typo'd* skill name (e.g. a missing 's' on
    # omnipus-frontend-rules) still get caught by check 3 as phantom. This
    # is narrower than "any hyphenated backtick token", which would
    # false-positive on ordinary role/file mentions like `backend-lead`.
    return isolation_universe(tok) or tok in discovered_skill_names or tok in USER_LEVEL_ALLOWLIST


# design 6.1's preload mapping (review C5): the skill preload IS the
# delivery mechanism — checked against the parsed `skills:` frontmatter
# list itself, not "the name appears somewhere in the file text".
def required_preloads_for(role):
    req = {"omnipus-shared-rules"}
    if role in {"team-lead", "squad-lead"}:
        req.add("omnipus-planning-orchestration")
    if role == "frontend-lead":
        req.add("omnipus-frontend-rules")
        req.add("omnipus-design-system")
    if role == "backend-lead":
        req.add("omnipus-backend-rules")
    return req


for path in agent_files:
    role = os.path.splitext(os.path.basename(path))[0]
    text = read_text(path)
    fm, body = split_frontmatter(text)
    skind, sval = parse_yaml_field(fm, "skills")
    fm_skills = sval if skind == "list" else ([] if skind != "scalar" or not sval else [sval])
    fm_skills_set = set(fm_skills)

    # cited: every backtick token that looks like a known skill citation,
    # used for check 3 (existence) — the allow-marker does NOT cover check 3
    # (design 8.2 lists checks 2, 4, 5, 6 and 10 only). cited_unmarked: the
    # subset cited on a line without `# agent-guard: allow` — used for
    # check 6 (isolation), which the marker does cover, e.g. a role's file
    # that legitimately *describes* another role's on-demand skill in prose
    # rather than loading it itself.
    #
    # Review C4: previously a token only counted as "cited" when the same
    # line also contained the word "skill" — a foreign skill mentioned
    # without that word escaped isolation, and a phantom skill escaped the
    # existence check. Every line's backtick tokens are now checked,
    # restricted to the known-skill-shape gate above to avoid flagging
    # ordinary backtick mentions of agent/role names.
    cited = set(fm_skills)
    cited_unmarked = set(fm_skills)
    for i, line in enumerate(text.splitlines(), start=1):
        line_marked = allowed_line(line)
        for tok in BACKTICK_RE.findall(line):
            if not SKILL_SHAPE_RE.match(tok):
                continue
            if not is_known_skill_shape(tok):
                continue
            cited.add(tok)
            if not line_marked:
                cited_unmarked.add(tok)

    if "omnipus-shared-rules" not in text:
        finding(3, "%s: does not name omnipus-shared-rules" % relpath(path))

    for req_skill in sorted(required_preloads_for(role)):
        if req_skill not in fm_skills_set:
            finding(3, "%s: '%s' is not preloaded in the skills: frontmatter list (design 6.1)" % (relpath(path), req_skill))

    for name in sorted(cited):
        exists = name in discovered_skill_names or name in USER_LEVEL_ALLOWLIST
        if not exists:
            finding(3, "%s: cites skill '%s' which does not exist under .claude/skills/ and is not on the user-level allowlist" % (relpath(path), name))

        if isolation_universe(name) and not role_allowed(role, name) and name in cited_unmarked:
            finding(6, "%s: cites skill '%s' outside role '%s' allowed set" % (relpath(path), name, role))

if "omnipus-shared-rules" not in discovered_skill_names:
    finding(6, "omnipus-shared-rules skill directory not found under .claude/skills/")

if os.path.isfile(CLAUDE_MD):
    if "omnipus-shared-rules" not in read_text(CLAUDE_MD):
        finding(6, "root CLAUDE.md does not reference omnipus-shared-rules")
else:
    finding(6, "root CLAUDE.md not found at %s" % relpath(CLAUDE_MD))

# ─── Check 7: teammates list ───────────────────────────────────────────────

external_re = re.compile(r"^(.*)\(external\)\s*$")
for path in agent_files:
    text = read_text(path)
    fm, _body = split_frontmatter(text)
    tkind, tval = parse_yaml_field(fm, "teammates")
    entries = tval if tkind == "list" else []
    for entry in entries:
        entry = entry.strip()
        if not entry:
            continue
        m = external_re.match(entry)
        if m:
            continue  # marked external — never checked further
        if entry in PLUGIN_REVIEWERS:
            continue
        if entry in agent_basenames:
            continue
        finding(7, "%s: teammates list cites phantom teammate '%s'" % (relpath(path), entry))

# ─── Check 4: banned command patterns ─────────────────────────────────────

BANNED_LITERALS = ["gh pr merge --admin", "gh pr merge --auto", "--dangerously-skip"]
# Review C7: whole-repo `go build|vet|test` is banned whether or not it
# carries `-tags` — the old "-tags on the line exempts it" logic let a
# tagged full-suite run (still forbidden: "never run whole-repo Go builds
# or tests") slip through. GO_CMD_RE now only spots the command verb; a
# separate check for a standalone "./..." target (not part of a longer
# path like "./pkg/foo/...") decides whether it is the whole-repo form.
GO_CMD_RE = re.compile(r"\bgo (build|vet|test)\b")
WHOLE_REPO_TARGET_RE = re.compile(r"(?<![\w./])\./\.\.\.(?![\w])")
# Review C7: a bare `tsc --noEmit` (no `-b`) is a silent no-op (CLAUDE.md's
# typecheck trap); `npm run typecheck` wraps the correct `tsc -b --noEmit`
# form, so the presence of `-b` on the line is what exempts it.
TSC_NOEMIT_RE = re.compile(r"\btsc\b[^\n]*--noEmit")
TSC_DASH_B_RE = re.compile(r"(^|\s)-b(\s|$)")
# Review C7: match the AI co-author trailer instruction case-insensitively
# — "Co-authored-by" (lowercase) previously slipped past the literal-case
# substring check.
AI_TRAILER_RE = re.compile(r"co-authored-by", re.IGNORECASE)
GIT_STASH_RE = re.compile(r"\bgit stash\b(?:\s+(\S+))?")
GIT_STASH_SAFE = {"push", "list", "show", "apply", "drop", "branch", "clear", "create", "export"}


def scan_banned(path):
    if not os.path.isfile(path):
        return
    for i, line in enumerate(read_text(path).splitlines(), start=1):
        if allowed_line(line):
            continue
        reasons = []
        for lit in BANNED_LITERALS:
            if lit in line:
                reasons.append(lit)
        gm = GO_CMD_RE.search(line)
        if gm and WHOLE_REPO_TARGET_RE.search(line):
            reasons.append("go %s ./... (whole-repo target)" % gm.group(1))
        tm = TSC_NOEMIT_RE.search(line)
        if tm and not TSC_DASH_B_RE.search(line):
            reasons.append("tsc --noEmit without -b (bare/no-op invocation)")
        if AI_TRAILER_RE.search(line) and ("claude" in line.lower() or "anthropic" in line.lower()):
            reasons.append("AI co-author trailer instruction")
        sm = GIT_STASH_RE.search(line)
        if sm:
            nxt = sm.group(1)
            if nxt is None:
                reasons.append("bare 'git stash'")
            elif nxt not in GIT_STASH_SAFE:
                reasons.append("'git stash %s'" % nxt)
        for r in reasons:
            finding(4, "%s:%d: banned pattern — %s" % (relpath(path), i, r))


for f in cited_files:
    scan_banned(f)

# ─── Check 5: retired-surface names ───────────────────────────────────────

RETIRED_NAMES = [
    "Command Center", "exec allowlist", "allowed_binaries", "shell_deny_patterns",
    "ExecApprovalManager", "deny-by-default backfill", "JPEG screencast", "goal confirm-gate",
]


def scan_retired(path):
    if not os.path.isfile(path):
        return
    for i, line in enumerate(read_text(path).splitlines(), start=1):
        if allowed_line(line):
            continue
        for name in RETIRED_NAMES:
            if name in line:
                finding(5, "%s:%d: retired-surface name '%s' in an instruction/ownership line" % (relpath(path), i, name))


for f in cited_files:
    scan_retired(f)

# ─── Check 8: Last reviewed header ────────────────────────────────────────

LAST_REVIEWED_RE = re.compile(r"Last reviewed:\s*\d{4}-\d{2}-\d{2}")
for path in agent_files:
    if not LAST_REVIEWED_RE.search(read_text(path)):
        finding(8, "%s: missing 'Last reviewed: YYYY-MM-DD' header" % relpath(path))
for name, path in dev_skill_files:
    if name in VENDORED_SKILLS:
        continue
    if not LAST_REVIEWED_RE.search(read_text(path)):
        finding(8, "%s: missing 'Last reviewed: YYYY-MM-DD' header" % relpath(path))

# ─── Check 9: vendored skill provenance ───────────────────────────────────

SOURCE_KEY_ALIASES = {
    "repo": ["source_repo", "source_path", "repo"],
    "branch": ["source_branch", "branch"],
    "commit": ["source_commit", "commit_sha", "commit", "sha"],
    "subpath": ["upstream_subpath", "subpath"],
    "date": ["copy_date", "date"],
    "flag": ["copy_or_derived", "derivation", "type", "flag"],
}

skill_dirs_by_name = {}
for name, path in skill_md_files:
    skill_dirs_by_name.setdefault(name, []).append(os.path.dirname(path))

for name in sorted(VENDORED_SKILLS):
    dirs = skill_dirs_by_name.get(name, [])
    if not dirs:
        finding(9, "%s: vendored skill directory not found under .claude/skills/" % name)
        continue
    for d in dirs:
        source_yaml = os.path.join(d, "SOURCE.yaml")
        if not os.path.isfile(source_yaml):
            finding(9, "%s: missing SOURCE.yaml at %s" % (name, relpath(source_yaml)))
            continue
        text = read_text(source_yaml)
        missing = []
        found_repo_value = None
        for concept, aliases in SOURCE_KEY_ALIASES.items():
            found = False
            for alias in aliases:
                m = re.search(r"^%s:\s*(\S.*)$" % re.escape(alias), text, re.MULTILINE)
                if m:
                    found = True
                    if concept == "repo":
                        found_repo_value = m.group(1).strip().strip("\"'")
                    break
            if not found:
                missing.append(concept)
        if missing:
            finding(9, "%s: SOURCE.yaml incomplete — missing %s" % (name, ", ".join(missing)))

        # Review C2: the old drift check tested os.path.isdir(source_repo),
        # but source_repo is an https:// URL by design (it is the citable
        # provenance record) — that test was always False, so the drift
        # WARN this guard is supposed to give could never fire. A separate,
        # optional `local_clone:` key names an actual local checkout; when
        # present and reachable, compare its `source_branch` tip against
        # the pinned `source_commit` with git — still best-effort and
        # WARN-only (design 8.1 check 9: never fails, silent when
        # unreachable).
        local_clone_value = None
        m = re.search(r"^local_clone:\s*(\S.*)$", text, re.MULTILINE)
        if m:
            local_clone_value = m.group(1).strip().strip("\"'")
        if local_clone_value:
            candidate = os.path.expanduser(local_clone_value)
            branch_val = None
            for alias in SOURCE_KEY_ALIASES["branch"]:
                bm = re.search(r"^%s:\s*(\S.*)$" % re.escape(alias), text, re.MULTILINE)
                if bm:
                    branch_val = bm.group(1).strip().strip("\"'")
                    break
            commit_val = None
            for alias in SOURCE_KEY_ALIASES["commit"]:
                cm = re.search(r"^%s:\s*(\S.*)$" % re.escape(alias), text, re.MULTILINE)
                if cm:
                    commit_val = cm.group(1).strip().strip("\"'")
                    break
            if os.path.isdir(candidate) and branch_val and commit_val:
                try:
                    rev = subprocess.run(
                        ["git", "-C", candidate, "rev-parse", branch_val],
                        capture_output=True, text=True, timeout=5,
                    )
                    if rev.returncode == 0:
                        upstream_sha = rev.stdout.strip()
                        if upstream_sha and not upstream_sha.startswith(commit_val) and not commit_val.startswith(upstream_sha):
                            warn(9, "%s: local clone %s branch '%s' is at %s, SOURCE.yaml pins %s — re-copy and diff if the upstream skill changed" % (name, local_clone_value, branch_val, upstream_sha[:12], commit_val))
                    # A non-zero rev-parse (branch missing, detached clone,
                    # etc.) is "cannot check" — silent, per 8.1 check 9.
                except (OSError, subprocess.SubprocessError):
                    pass  # unreachable clone — silent, best-effort only

# ─── Check 10: no hard-coded integration branch ───────────────────────────

BRANCH_RE = re.compile(r"\brelease/v\d+(?:\.\d+){0,2}\b")
for path in all_files_under_agents_and_skills:
    if not os.path.isfile(path) or not is_text_file(path):
        continue
    # Review C3: vendored directories are exempt from check 8 only — this
    # check now scans them too (the old blanket "is_vendored_path" skip is
    # removed).
    for i, line in enumerate(read_text(path).splitlines(), start=1):
        if allowed_line(line):
            continue
        if BRANCH_RE.search(line):
            finding(10, "%s:%d: hard-coded integration branch literal" % (relpath(path), i))

# ─── Checks 11, 12: discipline sync + dispatch template ───────────────────


def required_sections_for(base):
    # Returns (required_sections, classified). Review C6: a new agent file
    # that is not in any of the role lists used to return an empty
    # required-set indistinguishable from team-lead's deliberate exemption
    # — it silently skipped the discipline check instead of failing. Only
    # team-lead is a genuine, named exemption; every other unclassified
    # file is now itself a finding ("role not classified").
    if base == "team-lead":
        return set(), True
    if base in ORCH_SHARED_ONLY:
        return {"shared-traits"}, True
    if base in DEVELOPER_SIDE:
        return {"shared-traits", "developer-rules"}, True
    if base in REVIEWER_SIDE:
        return {"shared-traits", "reviewer-rules"}, True
    if base in BOTH_SIDE:
        return {"shared-traits", "developer-rules", "reviewer-rules"}, True
    return set(), False


canonical_text = None
canonical_sections = {}
if os.path.isfile(CANONICAL_DISCIPLINE):
    canonical_text = read_text(CANONICAL_DISCIPLINE)
    for s in ("shared-traits", "developer-rules", "reviewer-rules"):
        canonical_sections[s] = extract_section(canonical_text, s)
        if canonical_sections[s] is None:
            finding(11, "canonical discipline source %s is missing '%s' section markers" % (relpath(CANONICAL_DISCIPLINE), s))
else:
    finding(11, "canonical discipline source missing: %s — cannot verify any agent file's discipline sync" % relpath(CANONICAL_DISCIPLINE))

for path in agent_files:
    base = os.path.splitext(os.path.basename(path))[0]
    req, classified = required_sections_for(base)
    if not classified:
        finding(11, "%s: role not classified in the guard's discipline role lists (team-lead/DEVELOPER_SIDE/REVIEWER_SIDE/BOTH_SIDE/ORCH_SHARED_ONLY) — add it to scripts/check-agent-files.sh" % relpath(path))
        continue
    if not req:
        continue
    text = read_text(path)
    for s in req:
        sect = extract_section(text, s)
        if sect is None:
            finding(11, "%s: missing '%s' discipline block" % (relpath(path), s))
            continue
        if canonical_sections.get(s) is not None:
            if sect.strip("\n") != canonical_sections[s].strip("\n"):
                finding(11, "%s: '%s' discipline block differs from canonical %s" % (relpath(path), s, relpath(CANONICAL_DISCIPLINE)))

if not os.path.isfile(DISPATCH_TEMPLATE):
    finding(12, "dispatch template missing: %s" % relpath(DISPATCH_TEMPLATE))
else:
    dtext = read_text(DISPATCH_TEMPLATE)
    # F1 hand-off (finding A13): the template carries the reviewer-side
    # discipline in full — shared-traits AND reviewer-rules, the same two
    # sections a REVIEWER_SIDE agent file embeds — not reviewer-rules alone.
    # Both are diffed byte-for-byte against the canonical source, exactly
    # as check 11 does for agent files.
    for dispatch_section in ("shared-traits", "reviewer-rules"):
        dsect = extract_section(dtext, dispatch_section)
        if dsect is None:
            finding(12, "%s: missing '%s' discipline block" % (relpath(DISPATCH_TEMPLATE), dispatch_section))
            continue
        if canonical_sections.get(dispatch_section) is not None:
            if dsect.strip("\n") != canonical_sections[dispatch_section].strip("\n"):
                finding(12, "%s: '%s' discipline block differs from canonical %s" % (relpath(DISPATCH_TEMPLATE), dispatch_section, relpath(CANONICAL_DISCIPLINE)))
    if "omnipus-shared-rules" not in dtext:
        finding(12, "%s: does not name omnipus-shared-rules" % relpath(DISPATCH_TEMPLATE))
    if not re.search(r"skill tool", dtext, re.IGNORECASE):
        finding(12, "%s: does not instruct loading with the Skill tool" % relpath(DISPATCH_TEMPLATE))

team_lead_path = os.path.join(AGENTS_DIR, "team-lead.md")
if not os.path.isfile(team_lead_path):
    finding(12, "team-lead.md not found — cannot verify it names the plugin-reviewer dispatch template")
else:
    ttext = read_text(team_lead_path)
    if "plugin-reviewer-dispatch.md" not in ttext:
        finding(12, "team-lead.md does not cite .claude/templates/plugin-reviewer-dispatch.md")

# ─── Check 14: every tools: allow-list names Skill (review C9) ────────────
#
# Design 4.2: any role's frontmatter `tools:` allow-list must name `Skill`
# explicitly, or that role's on-demand skill loads become unreachable —
# the harness enforces the allowlist structurally, so a missing `Skill`
# entry is a silent capability loss, not a prose gap. Repo policy omits
# `tools:` entirely for most roles (docs-verifier is the one documented
# exception); this check only fires when a role DOES carry an allow-list.

for path in agent_files:
    text = read_text(path)
    fm, _body = split_frontmatter(text)
    tkind, tval = parse_yaml_field(fm, "tools")
    if tkind == "missing":
        continue
    if tkind == "list":
        names = [n.strip() for n in tval]
    else:
        raw = tval if isinstance(tval, str) else ""
        names = [n.strip() for n in re.split(r"[,\n]", raw) if n.strip()]
    if "Skill" not in names:
        finding(14, "%s: 'tools:' allow-list does not name Skill" % relpath(path))

# ─── Output ─────────────────────────────────────────────────────────────

for w in warnings:
    print(w)

if findings:
    for line in findings:
        print(line)
    print("check-agent-files: %d finding(s)" % len(findings))
    sys.exit(1)

print("check-agent-files: OK — 0 findings")
sys.exit(0)
PYEOF
status=$?

cat "$OUT"

if [ "$status" -ne 0 ] && [ "$status" -ne 1 ]; then
  echo "check-agent-files: ERROR — python3 checker failed to run (exit $status)" >&2
  exit 2
fi

exit "$status"
