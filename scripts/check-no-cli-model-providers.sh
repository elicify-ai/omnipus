#!/usr/bin/env bash
# WU-24 (founder 2026-10-14): no subprocess-backed MODEL providers.
# Exit 0: clean; 1: retired source/file-name traces; 2: scan could not run.
# REPO_ROOT is the fixture override used by the .test.sh companion.
# KEEP: openai-chatgpt HTTP/OAuth, subagent_3p and external CLI drivers.
# The runner-side Codex credential pre-check is explicitly allowed by SYMBOL,
# not by exempting the runner directory from provider-id / mechanism checks.
#
# Historical decision records (ADRs, dated internal specs and their reviews,
# research notes) may still name the retired kinds AS HISTORY: rewriting a
# past decision's text to erase a name is falsifying the record, not removing
# a trace — the same ruling as ADR-068 §2.4 for its own deletions. Those
# records are enumerated per-path in scripts/no-cli-model-providers.allow
# (data file, absent = no exemptions, so the self-check's fixture trees need
# nothing); every row there is a hole in the guard, so new live product or
# user docs never belong in it. Adjust it only with a stated reason in the
# same commit, never to hide a remaining live reference.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" || exit 2
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
python3 -I - "$REPO_ROOT" <<'PY'
import os
import re
import sys
from pathlib import Path

name = "check-no-cli-model-providers"
root = Path(sys.argv[1]).resolve()
required = ("pkg", "cmd", "src", "contracts", "config", "docs", "scripts", "tests")
for directory in required:
    if not (root / directory).is_dir():
        print(f"{name}: cannot scan: missing required root {root / directory}", file=sys.stderr)
        sys.exit(2)

# Exact provider ids, not CLI executable names or version output such as
# "codex-cli 0.4.0". Bare YAML and REST provider paths are source traces too.
ids = r"(?:codex-cli|github-copilot)"
patterns = (
    re.compile(r"[\"'`]" + ids + r"(?:[\"'`/]|\\[\"'])", re.I),
    re.compile(r"^\s*(?:id|provider|example|const)\s*:\s*" + ids + r"(?:\s|$)", re.I),
    re.compile(r"/providers/" + ids + r"(?:/|$)", re.I),
    re.compile(r"cli_login|cli_kind|CliLogin|CliKind|ProtocolCLI|NewCliProviderForKind", re.I),
    re.compile(r"(?:codex|copilot)_cli_provider|(?:Codex|Copilot)CliProvider", re.I),
    re.compile(r"ReadCodexCliCredentials|CreateCodexCliTokenSource|CodexCliAuth|resolveCodexAuthPath|CodexHomeEnvVar"),
)
# Explicit founder KEEP boundary: these credential-reader symbols may move
# INTO the runner package, where TestConnection still needs them. A provider
# id, login method, CLI protocol or model-provider class there still fails.
runner_credential_allowlist = {
    "ReadCodexCliCredentials", "CodexCliAuth", "resolveCodexAuthPath", "CodexHomeEnvVar",
}
provider_filename = re.compile(r"(?:codex|copilot)_cli_", re.I)
model_filename = re.compile(r"(?:codex|copilot)_cli_provider", re.I)
archive_prefixes = ("docs/internal/_archive/", "docs/_archive/")
guard_path = "scripts/check-no-cli-model-providers.sh"
# These are generated/dependency/tool-state directories, not product source.
pruned = {".git", ".gitnexus", "node_modules", "dist", ".cache", "__pycache__"}


def read_error(error):
    raise error


allow_path = root / "scripts" / "no-cli-model-providers.allow"
allow_res = []
if allow_path.is_file():
    try:
        for raw_line in allow_path.read_text(encoding="utf-8").splitlines():
            entry = raw_line.strip()
            if not entry or entry.startswith("#"):
                continue
            allow_res.append(re.compile("^(" + entry + ")$"))
    except re.error as error:
        print(f"{name}: bad allow-list entry in {allow_path}: {error}", file=sys.stderr)
        sys.exit(2)


def historical(path):
    if path == guard_path or path.startswith(archive_prefixes):
        return True
    return any(pattern.match(path) for pattern in allow_res)


def retirement_notice(path, line, retired_section):
    # Root CLAUDE and its required AGENTS twin: only the tagged retirement
    # bullet under Retired surfaces, never either whole file.
    return path in {"CLAUDE.md", "AGENTS.md"} and retired_section and "WU-24" in line and bool(
        re.match(r"^-\s+\*\*CLI(?:-backed)? model providers", line, re.I)
    )


hits = []
scanned = 0
try:
    for directory, dirs, files in os.walk(root, onerror=read_error, followlinks=False):
        relative_dir = Path(directory).relative_to(root).as_posix()
        dirs[:] = sorted(d for d in dirs if d not in pruned and not (
            relative_dir == "pkg/gateway" and d == "spa"
        ))
        for filename in sorted(files):
            file = Path(directory) / filename
            path = file.relative_to(root).as_posix()
            if historical(path):
                continue
            if model_filename.search(filename) or (
                path.startswith("pkg/providers/") and provider_filename.search(filename)
            ):
                hits.append(f"{file} [retired model-provider file name]")
            raw = file.read_bytes()
            if b"\x00" in raw:
                continue  # compiled/binary data, not source text
            text = raw.decode("utf-8")
            scanned += 1
            retired_section = False
            for number, line in enumerate(text.splitlines(), 1):
                if line.startswith("## "):
                    retired_section = line.startswith("## Retired surfaces")
                if retirement_notice(path, line, retired_section):
                    continue
                for pattern in patterns:
                    for match in pattern.finditer(line):
                        if path.startswith("pkg/agent/runner/") and match.group() in runner_credential_allowlist:
                            continue
                        hits.append(f"{file}:{number} [{match.group()}] {line.strip()}")
except (OSError, UnicodeError) as error:
    print(f"{name}: scan failed: {error}", file=sys.stderr)
    sys.exit(2)

if hits:
    print(f"{name}: FOUND retired CLI model-provider traces (scanned {scanned} text files)", file=sys.stderr)
    for hit in dict.fromkeys(hits):
        print(hit, file=sys.stderr)
    sys.exit(1)
print(f"{name}: OK (scanned {scanned} text files; HTTP model providers and 3p workers retained)")
PY
