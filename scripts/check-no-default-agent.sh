#!/usr/bin/env bash
# WU-1/WU-11 (omnipus-system-agent spec §6.2 "Scope", US-13): there is no
# default agent. No non-test Go source may contain GetDefaultAgent,
# SetDefaultAgentOverride or DefaultAgentID. The only trace left is the
# loader that ignores a leftover agents.defaults.default_agent_id key with a
# warning (pkg/config/config.go::warnIgnoredDefaultAgentKey), which uses none
# of these names. The generated channel-routing field DefaultAgentId (lower
# "d", the bound main agent, K14) is a different name and is not matched.
# Exit 0: clean; 1: a banned name found; 2: the scan could not run.
# REPO_ROOT is the fixture override used by the .test.sh companion.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" || exit 2
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
python3 -I - "$REPO_ROOT" <<'PY'
import os
import re
import sys
from pathlib import Path

name = "check-no-default-agent"
root = Path(sys.argv[1]).resolve()
for directory in ("pkg", "cmd"):
    if not (root / directory).is_dir():
        print(f"{name}: cannot scan: missing required root {root / directory}", file=sys.stderr)
        sys.exit(2)
banned = re.compile(r"\b(?:GetDefaultAgent|SetDefaultAgentOverride|DefaultAgentID)\b")
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
    print(f"{name}: FAIL: default-agent symbols in non-test Go (there is no default agent, spec §6.2):", file=sys.stderr)
    for hit in hits:
        print("  " + hit, file=sys.stderr)
    sys.exit(1)
print(f"{name}: OK ({scanned} non-test Go files)")
PY
