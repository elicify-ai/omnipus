#!/usr/bin/env bash
# Installs the Office inspector test packages into a private virtualenv and
# puts it first on the job's PATH, so `python3` in the Go tests finds them.
# Fails if any package cannot be imported afterwards.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
: "${RUNNER_TEMP:?RUNNER_TEMP is required}"
: "${GITHUB_PATH:?GITHUB_PATH is required}"
venv="$RUNNER_TEMP/office-test-python"

python3 -m venv "$venv"
"$venv/bin/python3" -m pip install --disable-pip-version-check -r "$here/requirements.txt"
"$venv/bin/python3" -I -B -c 'import openpyxl, pptx, pypdf'
echo "$venv/bin" >> "$GITHUB_PATH"
