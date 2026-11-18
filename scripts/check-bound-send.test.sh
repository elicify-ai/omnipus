#!/usr/bin/env bash
# Proof-of-failure companion for scripts/check-bound-send.sh (spec B-18, B-28,
# AL-1, AL-2). Fixtures live in a temporary tree. The checker itself is
# production code and is not part of this RED commit.
# If the checker is absent this script exits 1 with BLOCKED. It does not scan
# the real repo: after GREEN the real tree must be clean, and a RED run must
# not fail by finding today's code.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" || exit 2
CHECK="$SCRIPT_DIR/check-bound-send.sh"

if [ ! -f "$CHECK" ]; then
  echo "BLOCKED: scripts/check-bound-send.sh not implemented — required by spec 4.5 rule 7 / AL-1 / AL-2 / B-18 / B-28"
  exit 1
fi

python3 -I - "$CHECK" <<'PY'
import os, subprocess, sys, tempfile
from pathlib import Path

check = Path(sys.argv[1]).resolve()
failures = []

def fresh():
    tree = Path(tempfile.mkdtemp(prefix="bound-send-"))
    (tree / "src/lib").mkdir(parents=True)
    (tree / "src/components/chat/composer").mkdir(parents=True)
    (tree / "src/lib/bound-send.ts").write_text(
        "export function sendBound(){ send({ type: 'message' }) }\n", encoding="utf-8")
    (tree / "src/components/chat/composer/OmnipusComposer.tsx").write_text(
        "export const x = <ComposerPrimitive.Input cancelOnEscape={false} />\n", encoding="utf-8")
    (tree / "src/lib/enter.ts").write_text(
        "function enterNewChat(){ setActiveSession(null, null, null) }\n"
        "function enterBlockedNoChat(){ setActiveSession(null) }\n", encoding="utf-8")
    return tree

def put(tree, path, content):
    file = tree / path
    file.parent.mkdir(parents=True, exist_ok=True)
    file.write_text(content, encoding="utf-8")

def expect(tree, label, want):
    result = subprocess.run(
        ["bash", str(check)],
        env=dict(os.environ, REPO_ROOT=str(tree)),
        capture_output=True, text=True, check=False,
    )
    if result.returncode != want:
        failures.append(f"{label}: exit {result.returncode}, want {want}\n{result.stdout}\n{result.stderr}")

# Clean tree passes.
expect(fresh(), "clean", 0)

# Patterns inside tests pass.
tree = fresh()
put(tree, "src/lib/sample.test.ts",
    "composer.cancel()\nuseComposerCancel()\n.cancelRun()\nimport '@assistant-ui/core/internal'\n"
    "send({ type: 'message' })\nsetActiveSession(null)\nreassignToOpenChat\nDraftPersistence\n"
    "submission.origin = 1\nsubmission.target = 1\ncancelOnEscape={true}\n")
expect(tree, "test files are exempt", 0)

# Each violation fails. Spec 4.5 rule 7, AL-1, AL-2, B-18.
violations = {
    "cancelOnEscape not false": ("src/components/chat/composer/OmnipusComposer.tsx",
                                 "<ComposerPrimitive.Input cancelOnEscape={true} />\n"),
    "cancelOnEscape missing": ("src/components/chat/composer/OmnipusComposer.tsx",
                               "<ComposerPrimitive.Input />\n"),
    "ComposerPrimitive.Cancel": ("src/components/chat/composer/OmnipusComposer.tsx",
                                 "cancelOnEscape={false}\n<ComposerPrimitive.Cancel />\n"),
    "useComposerCancel": ("src/lib/escape.ts", "useComposerCancel()\n"),
    "cancelRun": ("src/lib/escape.ts", "thread.cancelRun()\n"),
    "composer.cancel": ("src/lib/escape.ts", "composer.cancel()\n"),
    "internal import": ("src/lib/escape.ts", "import '@assistant-ui/core/internal'\n"),
    "send outside bound-send": ("src/lib/other.ts", "send({ type: 'redirect' })\n"),
    "setActiveSession null": ("src/lib/other.ts", "setActiveSession(null, null, null)\n"),
    "reassignToOpenChat": ("src/lib/other.ts", "reassignToOpenChat()\n"),
    "DraftPersistence": ("src/lib/other.ts", "class DraftPersistence {}\n"),
    "submission.origin assign": ("src/lib/other.ts", "submission.origin = next\n"),
    "submission.target assign": ("src/lib/other.ts", "submission.target = next\n"),
}
for label, (path, content) in violations.items():
    tree = fresh()
    put(tree, path, content)
    expect(tree, label, 1)

# The same send inside bound-send.ts passes (AL-1).
tree = fresh()
put(tree, "src/lib/bound-send.ts", "send({ type: 'cancel' })\nsubmission.origin = frozen\n")
expect(tree, "bound-send.ts may build frames", 0)

if failures:
    print("FAIL")
    print("\n".join(failures))
    sys.exit(1)
print(f"ok {len(violations) + 3} fixture cases")
PY
