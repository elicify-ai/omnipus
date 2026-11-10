#!/usr/bin/env bash
# WU-1 B2 / WU-11 (omnipus-system-agent spec FR-253, FR-120..FR-133, ADR-029
# legacy routing removed): a channel message is routed only through its bound
# (workspace, main agent); unbound or unroutable messages drop loudly. The
# deleted binding cascade and default-agent ladder of pkg/routing must not come
# back through a stale merge. This guard bans their exact deleted names in
# non-test Go. It does NOT ban config.AgentBinding / cfg.Bindings /
# channel_policies routing rules: those are the landing-2 FR-253 items (channel
# person routing, BDD-16.5) and still exist by design until landing 2.
# Exit 0: clean; 1: a deleted name is back; 2: the scan could not run.
# REPO_ROOT is the fixture override used by the .test.sh companion.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" || exit 2
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
python3 -I - "$REPO_ROOT" <<'PY'
import os
import re
import sys
from pathlib import Path

name = "check-no-unbound-channel-routing"
root = Path(sys.argv[1]).resolve()
for directory in ("pkg", "cmd"):
    if not (root / directory).is_dir():
        print(f"{name}: cannot scan: missing required root {root / directory}", file=sys.stderr)
        sys.exit(2)
# The RouteResolver methods deleted by 31722760e (WU-1 B2).
banned = re.compile(
    r"\b(?:filterBindings|matchesAccountID|findPeerMatch|findGuildMatch|findTeamMatch|"
    r"findAccountMatch|findChannelWildcardMatch|resolveDefaultAgentID|DefaultAgentIDForTest)\b"
)
pruned = {".git", "node_modules", "dist", ".cache", "generated"}


def read_error(error):
    raise error


hits = []
scanned = 0
try:
    for top in ("pkg", "cmd", "internal"):
        base = root / top
        if not base.is_dir():
            continue
        for directory, dirs, files in os.walk(base, onerror=read_error, followlinks=False):
            dirs[:] = sorted(d for d in dirs if d not in pruned)
            for filename in sorted(files):
                if not filename.endswith(".go") or filename.endswith("_test.go"):
                    continue
                file = Path(directory) / filename
                scanned += 1
                for number, line in enumerate(file.read_text(encoding="utf-8").splitlines(), 1):
                    if banned.search(line):
                        hits.append(f"{file.relative_to(root).as_posix()}:{number}: {line.strip()}")
except (OSError, UnicodeDecodeError) as error:
    print(f"{name}: cannot scan: {error}", file=sys.stderr)
    sys.exit(2)

if scanned == 0:
    print(f"{name}: cannot scan: no non-test Go file found under {root}", file=sys.stderr)
    sys.exit(2)
if hits:
    print(f"{name}: FAIL: the deleted unbound/default routing cascade is back (spec FR-253; routing is bound-only):", file=sys.stderr)
    for hit in hits:
        print("  " + hit, file=sys.stderr)
    sys.exit(1)
print(f"{name}: OK ({scanned} non-test Go files)")
PY
