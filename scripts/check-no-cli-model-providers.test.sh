#!/usr/bin/env bash
# Proof-of-failure companion, discovered by scripts/guards.sh. Fixtures never
# edit the feature worktree, and all retired spellings are assembled here.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" || exit 2
python3 -I - "$SCRIPT_DIR/check-no-cli-model-providers.sh" <<'PY'
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

check = Path(sys.argv[1]).resolve()
required = ("pkg", "cmd", "src", "contracts", "config", "docs", "scripts", "tests")
ids = ("codex" + "-cli", "github" + "-copilot")
failures = []
count = 0
probes = 0

with tempfile.TemporaryDirectory(prefix="wu24-guard-selfcheck-", dir=os.environ.get("TMPDIR")) as temporary:
    base = Path(temporary)

    def fresh():
        global count
        count += 1
        tree = base / str(count)
        for directory in required:
            (tree / directory).mkdir(parents=True, exist_ok=True)
        (tree / "pkg/kept.go").write_text(
            'package kept\nconst provider = "openai-chatgpt"\n'
            '// subagent_3p, NewClaudeDriver, NewCodexDriver, NewOpencodeDriver\n'
            '// vendor version: "' + ids[0] + ' 0.4.0"\n', encoding="utf-8"
        )
        return tree

    def put(tree, path, content):
        file = tree / path
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_text(content, encoding="utf-8")

    def expect(tree, label, want, sentinel=None):
        global probes
        probes += 1
        env = dict(os.environ, REPO_ROOT=str(tree))
        result = subprocess.run(["bash", str(check)], env=env, capture_output=True, text=True, check=False)
        observed = result.stdout + result.stderr
        if result.returncode != want or (sentinel is not None and sentinel not in observed):
            failures.append(label)
            print(f"selfcheck FAIL: {label}: wanted exit {want}, got {result.returncode}", file=sys.stderr)
            print(observed, file=sys.stderr)
        else:
            print(f"selfcheck PASS: {label} exit={result.returncode}")

    expect(fresh(), "clean synthetic tree with retained HTTP provider and external drivers", 0)

    for directory in required:
        for provider in ids:
            tree = fresh()
            put(tree, directory + "/fixture.txt", '"' + provider + '"\n')
            expect(tree, directory + " provider-id " + provider, 1, provider)

    for symbol in (
        "cli_" + "login", "cli_" + "kind", "Codex" + "CliProvider",
        "Codex" + "CLIProvider", "Copilot" + "CliProvider", "Copilot" + "CLIProvider",
        "NewCli" + "ProviderForKind", "Protocol" + "CLI",
        "codex_" + "cli_provider", "copilot_" + "cli_provider",
    ):
        tree = fresh()
        put(tree, "pkg/fixture.go", "package fixture\n// " + symbol + "\n")
        expect(tree, "retired symbol " + symbol, 1, symbol)

    for provider in ids:
        tree = fresh()
        put(tree, "contracts/fixture.yaml", "example: " + provider + "\n")
        expect(tree, "bare YAML provider-id " + provider, 1, provider)
        tree = fresh()
        put(tree, "docs/api.md", "/api/v1/providers/" + provider + "/sign-in\n")
        expect(tree, "REST path " + provider, 1, provider)

    # WU-24 CHECK F2: source resurrection must be caught even in the runner
    # package, whose surviving credential-reader symbols are allowed.
    deleted_uri = "/api/v1/providers/openai-chatgpt/sign-in/" + "im" + "port"
    for path, symbol, source in (
        ("pkg/agent/runner/wu24_guard_probe.go", "handleProviderSignIn" + "Import", "go"),
        ("src/wu24-guard-probe.ts", "importCodex" + "Login", "ts"),
    ):
        tree = fresh()
        put(tree, path, 'const endpoint = "' + deleted_uri + '"\n')
        expect(tree, source + " deleted credential-copy URI", 1, deleted_uri)
        tree = fresh()
        put(tree, path, "// forbidden helper resurrection: " + symbol + "\n")
        expect(tree, source + " deleted credential-copy helper", 1, symbol)

    for prefix in ("codex_" + "cli_", "copilot_" + "cli_"):
        for suffix in ("provider.go", "credentials.go", "provider_test.go"):
            tree = fresh()
            put(tree, "pkg/providers/" + prefix + suffix, "")
            expect(tree, "empty retired provider file " + suffix, 1, "file name")

    for path in ("docs/internal/_archive/wu24.md", "docs/_archive/wu24.md"):
        tree = fresh()
        put(tree, path, '"' + ids[0] + '" "' + ids[1] + '"\n')
        expect(tree, "historical archive " + path, 0)

    tree = fresh()
    put(tree, "scripts/check-no-cli-model-providers.sh", check.read_text(encoding="utf-8"))
    expect(tree, "the guard may name what it forbids", 0)

    for path in ("CLAUDE.md", "AGENTS.md"):
        tree = fresh()
        notice = '## Retired surfaces — keep the deletion\n- **CLI model providers** (WU-24): "' + ids[0] + '" and "' + ids[1] + '" removed.\n'
        put(tree, path, notice)
        expect(tree, "tagged root retirement notice " + path, 0)
        put(tree, path, notice + '\n## Elsewhere\nEnable "' + ids[0] + '".\n')
        expect(tree, "root document is NOT a whole-file exemption " + path, 1, ids[0])

    tree = fresh()
    put(tree, "pkg/agent/runner/codex_auth.go", "package runner\nfunc ReadCodex" + "CliCredentials() {}\n")
    expect(tree, "explicit retained runner credential symbol", 0)
    put(tree, "pkg/agent/runner/fixture.go", 'package runner\nconst provider = "' + ids[0] + '"\n')
    expect(tree, "runner is NOT exempt from model-provider ids", 1, ids[0])

    tree = fresh()
    put(tree, "pkg/fixture.go", "package fixture\nfunc ReadCodex" + "CliCredentials() {}\n")
    expect(tree, "old provider credential symbol outside the retained runner fails", 1, "ReadCodex" + "CliCredentials")

    tree = fresh()
    shutil.rmtree(tree / "src")
    expect(tree, "missing source root refuses a false green", 2, "missing required root")

    tree = fresh()
    (tree / "pkg/unreadable-text.txt").write_bytes(b"\xff")
    expect(tree, "undecodable source refuses a false green", 2, "scan failed")

if failures:
    print(f"selfcheck: FAILED ({len(failures)} of {probes} probes)", file=sys.stderr)
    sys.exit(1)
print(f"selfcheck: OK ({probes} probes over {count} fixture trees; source removal cannot pass on synthetic offenders)")
PY
