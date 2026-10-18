#!/usr/bin/env bash
# Proof-of-failure companion for check-no-unbound-channel-routing.sh,
# discovered by scripts/guards.sh. Fixtures live in a temporary tree.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" || exit 2
python3 -I - "$SCRIPT_DIR/check-no-unbound-channel-routing.sh" <<'PY'
import os
import subprocess
import sys
import tempfile
from pathlib import Path

check = Path(sys.argv[1]).resolve()
failures = []
count = 0
names = ("filter" + "Bindings", "findChannel" + "WildcardMatch", "resolveDefault" + "AgentID",
         "findPeer" + "Match", "findGuild" + "Match", "findTeam" + "Match", "findAccount" + "Match",
         "matches" + "AccountID", "DefaultAgentID" + "ForTest")

with tempfile.TemporaryDirectory(prefix="wu11-unbound-routing-selfcheck-", dir=os.environ.get("TMPDIR")) as temporary:
    base = Path(temporary)

    def fresh():
        global count
        count += 1
        tree = base / str(count)
        for directory in ("pkg", "cmd"):
            (tree / directory).mkdir(parents=True, exist_ok=True)
        (tree / "pkg/routing.go").write_text(
            "package routing\n// bound-only resolver\nfunc (r *RouteResolver) ResolveRoute() {}\n"
            "type AgentBinding struct{}\n", encoding="utf-8")
        return tree

    def put(tree, path, content):
        file = tree / path
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_text(content, encoding="utf-8")

    def expect(tree, label, want):
        result = subprocess.run(["bash", str(check)], env=dict(os.environ, REPO_ROOT=str(tree)),
                                capture_output=True, text=True, check=False)
        if result.returncode != want:
            failures.append(label)
            print(f"selfcheck FAIL: {label}: wanted exit {want}, got {result.returncode}", file=sys.stderr)
            print(result.stdout + result.stderr, file=sys.stderr)
        else:
            print(f"selfcheck PASS: {label} exit={result.returncode}")

    expect(fresh(), "clean bound-only tree (AgentBinding type still allowed until landing 2)", 0)
    for symbol in names:
        tree = fresh()
        put(tree, "pkg/routing/x.go", "package routing\nfunc (r *RouteResolver) " + symbol + "() {}\n")
        expect(tree, symbol + " resurrected", 1)
    tree = fresh()
    put(tree, "pkg/routing/x_test.go", "package routing\n// " + names[0] + " history in a test\n")
    expect(tree, "name in a test file", 0)
    tree = base / "missing"
    tree.mkdir()
    expect(tree, "missing required roots cannot be scanned", 2)

if failures:
    print(f"check-no-unbound-channel-routing.test.sh: {len(failures)} self-check case(s) failed", file=sys.stderr)
    sys.exit(1)
print("check-no-unbound-channel-routing.test.sh: OK")
PY
