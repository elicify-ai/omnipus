#!/usr/bin/env bash
# Proof-of-failure companion for check-no-ava-admin-agent-ids.sh, discovered
# by scripts/guards.sh. Fixtures live in a temporary tree, never in the repo.
# Spec §6.2 requires: a fixture containing IDAdmin must make the guard FAIL,
# and a fixture with Username: "admin" must PASS.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" || exit 2
python3 -I - "$SCRIPT_DIR/check-no-ava-admin-agent-ids.sh" <<'PY'
import os
import subprocess
import sys
import tempfile
from pathlib import Path

check = Path(sys.argv[1]).resolve()
failures = []
count = 0
av, ad = "a" + "va", "ad" + "min"

with tempfile.TemporaryDirectory(prefix="wu11-ava-admin-selfcheck-", dir=os.environ.get("TMPDIR")) as temporary:
    base = Path(temporary)

    def fresh():
        global count
        count += 1
        tree = base / str(count)
        for directory in ("pkg", "cmd", "src"):
            (tree / directory).mkdir(parents=True, exist_ok=True)
        (tree / "pkg/kept.go").write_text('package kept\nconst agent = "mia"\n', encoding="utf-8")
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

    expect(fresh(), "clean tree", 0)

    must_fail = {
        "Go identifier IDAdmin (spec §6.2)": ("pkg/x.go", "package x\nvar a = coreagent.ID" + "Admin\n"),
        "Go identifier AgentIDAva": ("pkg/x.go", "package x\nvar a = AgentID" + "Ava\n"),
        "Go quoted ava": ("pkg/x.go", 'package x\nvar a = "' + av + '"\n'),
        "Go AgentID field admin": ("cmd/x.go", 'package x\nvar m = Msg{AgentID: "' + ad + '"}\n'),
        "Go agent comparison admin": ("pkg/x.go", 'package x\nfunc f(agentID string) bool { return agentID == "' + ad + '" }\n'),
        "TS agentId field admin": ("src/x.ts", "export const m = { agentId: '" + ad + "' }\n"),
        "TS agent_id field admin": ("src/x.ts", "export const m = { agent_id: '" + ad + "' }\n"),
        "TS reversed comparison admin": ("src/x.ts", "export const f = (agentId: string) => '" + ad + "' === agentId\n"),
        "TS roster with admin": ("src/x.ts", "const ids = new Set(['mia', 'jim', '" + ad + "'])\n"),
        "TS route /agents/admin": ("src/x.tsx", "navigate({ to: '/agents/" + ad + "' })\n"),
        "TS route /admin/chat": ("src/x.tsx", "navigate({ to: '/" + ad + "/chat' })\n"),
        "TS quoted ava": ("src/x.tsx", "const id = `" + av + "`\n"),
    }
    for label, (path, content) in must_fail.items():
        tree = fresh()
        put(tree, path, content)
        expect(tree, label, 1)

    must_pass = {
        "Go Username admin (spec §6.2)": ("pkg/x.go", 'package x\nvar u = UserConfig{Username: "' + ad + '"}\n'),
        "Go RBAC role admin": ("pkg/x.go", 'package x\nconst role = "' + ad + '"\n'),
        "TS login placeholder admin": ("src/x.tsx", 'export const i = <input placeholder="' + ad + '" />\n'),
        "TS owner / created_by admin": ("src/x.ts", "export const p = { owner: '" + ad + "', created_by: '" + ad + "' }\n"),
        "Go test file IDAdmin": ("pkg/x_test.go", "package x\nvar a = coreagent.ID" + "Admin\n"),
        "TS test file agentId admin": ("src/x.test.ts", "const m = { agentId: '" + ad + "' }\n"),
        "generated code ava": ("pkg/api/generated/x.go", 'package generated\nvar a = "' + av + '"\n'),
        "word inside a longer token": ("src/x.ts", "export const lava = 'java'; const administration = 1\n"),
    }
    for label, (path, content) in must_pass.items():
        tree = fresh()
        put(tree, path, content)
        expect(tree, label, 0)

    tree = base / "missing"
    tree.mkdir()
    expect(tree, "missing required roots cannot be scanned", 2)

if failures:
    print(f"check-no-ava-admin-agent-ids.test.sh: {len(failures)} self-check case(s) failed", file=sys.stderr)
    sys.exit(1)
print("check-no-ava-admin-agent-ids.test.sh: OK")
PY
