#!/usr/bin/env bash
# check-bound-send.sh
#
# Guard for #1428 step 2 (spec C r9): the send-to-chat binding.
#
#   AL-1  Only src/lib/bound-send.ts builds or transports `message`,
#         `redirect` or `cancel` frame objects in production src/.
#   AL-2  No code changes a submission's origin or target; outside
#         enterNewChat / enterBlockedNoChat nothing calls
#         setActiveSession(null...). The retired shapes `reassignToOpenChat`
#         and `DraftPersistence` (B-18: no draft persistence, no reassignment
#         to whatever chat is open) never come back.
#   4.5 rule 7  Nothing in the session composer reaches the library's cancel
#         write-back: no ComposerPrimitive.Cancel, useComposerCancel,
#         composer cancel() or thread cancelRun(); no production import of
#         @assistant-ui/core/internal; and the session composer's
#         ComposerPrimitive.Input keeps the literal cancelOnEscape={false}.
#
# Scope: production files under src/ (.ts, .tsx, .js, .jsx). Test files
# (*.test.*, src/test/**), the generated contract types (src/lib/api/generated/,
# declarations only) and comment lines are exempt. The tree
# scanned is the WORKING DIRECTORY (spec 12.3 runs this from the repo root,
# as `make lint-guards` and CI do); its companion, check-bound-send.test.sh,
# runs it inside fixture trees the same way.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

if [ ! -d src ]; then
  echo "check-bound-send: no src/ under $(pwd) — run it from the repository root" >&2
  exit 2
fi

python3 -I - <<'PY'
import re
import sys
from pathlib import Path

BOUND_SEND = Path("src/lib/bound-send.ts")
SESSION_COMPOSER = Path("src/components/chat/composer/OmnipusComposer.tsx")
NEW_CHAT_ENTRIES = ("enterNewChat", "enterBlockedNoChat")

FRAME = re.compile(r"""\btype\s*:\s*['"](message|redirect|cancel)['"]""")
SUBMISSION_WRITE = re.compile(r"\bsubmission\.(origin|target)\s*=(?!=)")
SET_NULL = re.compile(r"\bsetActiveSession\(\s*null\b")
CANCEL_SHAPES = [
    (re.compile(r"\bComposerPrimitive\.Cancel\b"), "ComposerPrimitive.Cancel (spec 4.5 rule 7b)"),
    (re.compile(r"\buseComposerCancel\b"), "useComposerCancel (spec 4.5 rule 7b)"),
    (re.compile(r"\.cancelRun\s*\("), "thread cancelRun() (spec 4.5 rule 7b)"),
    (re.compile(r"\bcomposer\w*\.cancel\s*\("), "composer cancel() (spec 4.5 rule 7b)"),
    (re.compile(r"""['"]@assistant-ui/core/internal['"]"""), "@assistant-ui/core/internal import (spec 4.5 rule 7c)"),
]
RETIRED = [
    (re.compile(r"\breassignToOpenChat\b"), "reassignToOpenChat (B-18)"),
    (re.compile(r"\bDraftPersistence\b"), "DraftPersistence (B-18)"),
]
CANCEL_ON_ESCAPE = re.compile(r"cancelOnEscape\s*=\s*(\{[^}]*\}|\S+)")
FUNCTION_NAME = re.compile(r"\bfunction\s+([A-Za-z_$][\w$]*)|\b(?:const|let)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?\(")

GENERATED = Path("src/lib/api/generated")

def is_exempt(path):
    # Tests, and the generated contract types (type declarations, never builders).
    parts = path.parts
    if ".test." in path.name or (len(parts) > 1 and parts[0] == "src" and parts[1] == "test"):
        return True
    return GENERATED in path.parents

def is_comment(line):
    stripped = line.lstrip()
    return stripped.startswith("//") or stripped.startswith("*") or stripped.startswith("/*")

offenders = []
scanned = 0
for path in sorted(Path("src").rglob("*")):
    if path.suffix not in (".ts", ".tsx", ".js", ".jsx") or not path.is_file() or is_exempt(path):
        continue
    scanned += 1
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeDecodeError) as err:
        print(f"check-bound-send: cannot read {path}: {err}", file=sys.stderr)
        sys.exit(2)
    in_bound_send = path == BOUND_SEND
    enclosing = None
    for number, line in enumerate(lines, 1):
        if is_comment(line):
            continue
        named = FUNCTION_NAME.search(line)
        if named:
            enclosing = named.group(1) or named.group(2)
        where = f"{path}:{number}"
        if not in_bound_send:
            if FRAME.search(line):
                offenders.append(f"{where}: builds a message/redirect/cancel frame outside src/lib/bound-send.ts (AL-1)")
            if SUBMISSION_WRITE.search(line):
                offenders.append(f"{where}: changes a submission's origin or target (AL-2)")
        if SET_NULL.search(line) and enclosing not in NEW_CHAT_ENTRIES:
            offenders.append(f"{where}: setActiveSession(null...) outside enterNewChat/enterBlockedNoChat (AL-2)")
        for pattern, label in CANCEL_SHAPES + RETIRED:
            if pattern.search(line):
                offenders.append(f"{where}: {label}")
        if path == SESSION_COMPOSER:
            for value in CANCEL_ON_ESCAPE.findall(line):
                if value != "{false}":
                    offenders.append(f"{where}: cancelOnEscape={value}; the session composer keeps the literal {{false}} (spec 4.5 rule 7a)")

if SESSION_COMPOSER.is_file():
    text = SESSION_COMPOSER.read_text(encoding="utf-8")
    if "ComposerPrimitive.Input" in text and not re.search(r"cancelOnEscape\s*=\s*\{false\}", text):
        offenders.append(f"{SESSION_COMPOSER}: ComposerPrimitive.Input without cancelOnEscape={{false}} (spec 4.5 rule 7a)")

if scanned == 0:
    print("check-bound-send: no production source files under src/ — refusing a green verdict for an empty scan", file=sys.stderr)
    sys.exit(2)

if offenders:
    print("check-bound-send: FAIL")
    for line in offenders:
        print(f"  {line}")
    sys.exit(1)
print(f"check-bound-send: ok ({scanned} production files)")
PY
