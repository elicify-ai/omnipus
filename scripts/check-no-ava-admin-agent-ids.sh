#!/usr/bin/env bash
# WU-11 (omnipus-system-agent spec §6.2 "Scope", OR-MIN-013): Omnipus replaced
# the Ava and Admin agents. No production Go or TS file may carry them as AGENT
# ids. Exit 0: clean; 1: agent-id traces found; 2: the scan could not run.
# REPO_ROOT is the fixture override used by the .test.sh companion.
#
# What counts (an agent id, in an agent-id position):
#   - the identifiers IDAva, IDAdmin, AgentIDAva, AgentIDAdmin;
#   - a quoted "ava" anywhere in production source (it has no other meaning);
#   - a quoted "admin" in an agent-id position: an agent_id / agentId / AgentID
#     (or *_agent_id / *AgentId) field, assignment or comparison; an agent
#     route parameter (/agents/admin, /admin/chat); a seed list or roster that
#     names it next to a core agent id ("mia", "jim", "ray", "gp", "omnipus").
# What does not (spec §6.2): usernames, RBAC roles, login placeholders, owner /
# created_by values and generated fixtures. Tests and generated code are not
# production source and are not scanned.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" || exit 2
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
python3 -I - "$REPO_ROOT" <<'PY'
import os
import re
import sys
from pathlib import Path

name = "check-no-ava-admin-agent-ids"
root = Path(sys.argv[1]).resolve()
for directory in ("pkg", "cmd", "src"):
    if not (root / directory).is_dir():
        print(f"{name}: cannot scan: missing required root {root / directory}", file=sys.stderr)
        sys.exit(2)

scan_roots = ("pkg", "cmd", "internal", "src", "packages")
pruned = {".git", "node_modules", "dist", ".cache", "__pycache__", "generated"}
q = r"[\"'`]"
ident = re.compile(r"\b(?:IDAva|IDAdmin|AgentIDAva|AgentIDAdmin)\b")
quoted_ava = re.compile(q + r"ava" + q)
agent_key = r"\b\w*(?i:agent_?ids?)\b"
admin_field = re.compile(agent_key + r"\s*(?::|=|==|===|!=|!==|:=)\s*" + q + r"admin" + q)
admin_compare = re.compile(q + r"admin" + q + r"\s*(?:==|===|!=|!==)\s*" + agent_key)
admin_route = re.compile(r"/agents/admin\b|/admin/chat\b")
core = r"(?:mia|jim|ray|gp|omnipus)"
admin_roster = re.compile(q + core + q + r"[^\n]*" + q + r"admin" + q + r"|" + q + r"admin" + q + r"[^\n]*" + q + core + q)


def production(path):
    if path.endswith(".go"):
        return not path.endswith("_test.go")
    if path.endswith((".ts", ".tsx")):
        return not re.search(r"\.(?:test|spec)\.tsx?$|\.stories\.tsx?$|\.fixture\.tsx?$|Fixtures?\.tsx?$", path)
    return False


def read_error(error):
    raise error


hits = []
scanned = 0
try:
    for top in scan_roots:
        base = root / top
        if not base.is_dir():
            continue
        for directory, dirs, files in os.walk(base, onerror=read_error, followlinks=False):
            relative_dir = Path(directory).relative_to(root).as_posix()
            dirs[:] = sorted(d for d in dirs if d not in pruned and not (
                relative_dir == "pkg/gateway" and d == "spa"
            ))
            for filename in sorted(files):
                file = Path(directory) / filename
                path = file.relative_to(root).as_posix()
                if not production(path):
                    continue
                scanned += 1
                text = file.read_text(encoding="utf-8")
                for number, line in enumerate(text.splitlines(), 1):
                    for label, pattern in (
                        ("agent-id identifier", ident),
                        ("quoted ava", quoted_ava),
                        ("admin as agent id", admin_field),
                        ("admin as agent id", admin_compare),
                        ("admin agent route", admin_route),
                        ("admin in an agent roster", admin_roster),
                    ):
                        if pattern.search(line):
                            hits.append(f"{path}:{number}: [{label}] {line.strip()}")
                            break
except (OSError, UnicodeDecodeError) as error:
    print(f"{name}: cannot scan: {error}", file=sys.stderr)
    sys.exit(2)

if scanned == 0:
    print(f"{name}: cannot scan: no production Go or TS file found under {root}", file=sys.stderr)
    sys.exit(2)
if hits:
    print(f"{name}: FAIL: Ava/Admin agent ids in production source (spec §6.2; Omnipus replaced them):", file=sys.stderr)
    for hit in hits:
        print("  " + hit, file=sys.stderr)
    sys.exit(1)
print(f"{name}: OK ({scanned} production Go/TS files, no Ava/Admin agent ids)")
PY
