#!/usr/bin/env bash
# Proof-of-failure companion for check-no-default-agent.sh, discovered by
# scripts/guards.sh. Fixtures live in a temporary tree, never in the repo.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" || exit 2
python3 -I - "$SCRIPT_DIR/check-no-default-agent.sh" <<'PY'
import os
import subprocess
import sys
import tempfile
from pathlib import Path

check = Path(sys.argv[1]).resolve()
failures = []
count = 0
names = ("GetDefault" + "Agent", "SetDefaultAgent" + "Override", "DefaultAgent" + "ID")

with tempfile.TemporaryDirectory(prefix="wu11-default-agent-selfcheck-", dir=os.environ.get("TMPDIR")) as temporary:
    base = Path(temporary)

    def fresh():
        global count
        count += 1
        tree = base / str(count)
        for directory in ("pkg", "cmd"):
            (tree / directory).mkdir(parents=True, exist_ok=True)
        (tree / "pkg/kept.go").write_text("package kept\nfunc warnIgnoredDefaultAgentKey() {}\n", encoding="utf-8")
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

    expect(fresh(), "clean tree with the ignore-with-warning loader", 0)
    for symbol in names:
        for path in ("pkg/x.go", "cmd/x.go"):
            tree = fresh()
            put(tree, path, "package x\nvar _ = cfg." + symbol + "\n")
            expect(tree, f"{symbol} in {path}", 1)
    for label, path, content in (
        ("generated DefaultAgentId (K14 field)", "pkg/api/generated/x.go", "package generated\ntype R struct{ DefaultAgentId string }\n"),
        ("lower-d DefaultAgentId in handler", "pkg/x.go", "package x\nvar _ = req.DefaultAgentId\n"),
        ("banned name in a test file", "pkg/x_test.go", "package x\nvar _ = " + names[0] + "\n"),
    ):
        tree = fresh()
        put(tree, path, content)
        expect(tree, label, 0)
    tree = base / "missing"
    tree.mkdir()
    expect(tree, "missing required roots cannot be scanned", 2)

if failures:
    print(f"check-no-default-agent.test.sh: {len(failures)} self-check case(s) failed", file=sys.stderr)
    sys.exit(1)
print("check-no-default-agent.test.sh: OK")
PY
