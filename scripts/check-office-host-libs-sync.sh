#!/usr/bin/env bash
# One list of LibreOffice's Linux host libraries (office-preview spec §10):
# scripts/office-pack/libreoffice-host-libs.env. Three consumers must carry
# exactly that list, or one install route ships a LibreOffice that cannot
# start (dry run 44496809452: the Docker image lacked libX11-xcb.so.1):
#
#   scripts/install.sh       embeds the env file's assignment lines verbatim
#                            (it is fetched alone with curl, so it cannot read
#                            the file), between "# BEGIN libreoffice-host-libs"
#                            and "# END libreoffice-host-libs";
#   .goreleaser.yaml         the omnipus nfpm overrides: deb dependencies and
#                            recommends, rpm dependencies and recommends;
#   docker/Dockerfile.goreleaser  reads the env file itself (COPY), so it is
#                            checked to do that.
#
# Exit 0: in sync. 1: drift (each difference printed). 2: cannot check.
# Overrides for the self-test: HOST_LIBS_ENV, INSTALL_SH, GORELEASER_YAML,
# DOCKERFILE.
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="${HOST_LIBS_ENV:-$REPO_ROOT/scripts/office-pack/libreoffice-host-libs.env}"
INSTALL="${INSTALL_SH:-$REPO_ROOT/scripts/install.sh}"
GORELEASER="${GORELEASER_YAML:-$REPO_ROOT/.goreleaser.yaml}"
DOCKER="${DOCKERFILE:-$REPO_ROOT/docker/Dockerfile.goreleaser}"

for f in "$ENV_FILE" "$INSTALL" "$GORELEASER" "$DOCKER"; do
  [ -f "$f" ] || { echo "check-office-host-libs-sync: missing $f" >&2; exit 2; }
done
command -v python3 >/dev/null 2>&1 || { echo "check-office-host-libs-sync: python3 is required" >&2; exit 2; }

python3 - "$ENV_FILE" "$INSTALL" "$GORELEASER" "$DOCKER" <<'PY'
import re
import sys

env_path, install_path, goreleaser_path, docker_path = sys.argv[1:]
problems = []

assign = re.compile(r'^(OFFICE_(?:DEB|RPM)_(?:LIBS|RECOMMENDS))="([^"]*)"$')
env_lines = [l.rstrip("\n") for l in open(env_path, encoding="utf-8")]
lists = {}
assignment_lines = []
for line in env_lines:
    m = assign.match(line)
    if m:
        lists[m.group(1)] = m.group(2).split()
        assignment_lines.append(line)
for key in ("OFFICE_DEB_LIBS", "OFFICE_DEB_RECOMMENDS", "OFFICE_RPM_LIBS", "OFFICE_RPM_RECOMMENDS"):
    if not lists.get(key):
        print(f"check-office-host-libs-sync: {env_path} has no {key}=\"...\" line", file=sys.stderr)
        sys.exit(2)

# install.sh: the block between the markers must be the assignment lines.
text = open(install_path, encoding="utf-8").read()
m = re.search(r"^# BEGIN libreoffice-host-libs\n(.*?)^# END libreoffice-host-libs$", text, re.S | re.M)
if not m:
    problems.append(f"{install_path}: no '# BEGIN/END libreoffice-host-libs' block")
else:
    block = [l for l in m.group(1).splitlines() if l and not l.startswith("#")]
    if block != assignment_lines:
        problems.append(f"{install_path}: the libreoffice-host-libs block differs from {env_path}")
        for l in sorted(set(block) ^ set(assignment_lines)):
            problems.append(f"  {'only in install.sh' if l in block else 'only in the env file'}: {l}")

# .goreleaser.yaml: the nfpm `overrides:` block (the file has exactly one),
# read by indentation so the check needs no YAML library.
gl = open(goreleaser_path, encoding="utf-8").read().splitlines()
starts = [i for i, l in enumerate(gl) if l == "    overrides:"]
if len(starts) != 1:
    problems.append(f"{goreleaser_path}: expected one nfpm 'overrides:' block, found {len(starts)}")
else:
    found = {}
    fmt = field = None
    for l in gl[starts[0] + 1:]:
        if l.strip() == "" or l.lstrip().startswith("#"):
            continue
        indent = len(l) - len(l.lstrip())
        if indent <= 4:
            break
        if indent == 6 and l.strip().endswith(":"):
            fmt, field = l.strip()[:-1], None
        elif indent == 8 and l.strip().endswith(":"):
            field = l.strip()[:-1]
        elif indent == 10 and l.strip().startswith("- ") and fmt and field:
            item = l.strip()[2:].strip().strip('"')
            found.setdefault((fmt, field), []).append(re.sub(r"\s*\|\s*", "|", item))
    for fmt in ("deb", "rpm"):
        for field, key in (("dependencies", f"OFFICE_{fmt.upper()}_LIBS"), ("recommends", f"OFFICE_{fmt.upper()}_RECOMMENDS")):
            got, want = sorted(found.get((fmt, field), [])), sorted(lists[key])
            if got != want:
                problems.append(f"{goreleaser_path}: nfpm overrides.{fmt}.{field} differs from {key}")
                for x in sorted(set(got) ^ set(want)):
                    problems.append(f"  {'only in .goreleaser.yaml' if x in got else 'only in the env file'}: {x}")
if not re.search(r"^\s*- scripts/office-pack/libreoffice-host-libs\.env\s*$", "\n".join(gl), re.M):
    problems.append(f"{goreleaser_path}: dockers_v2 extra_files lacks scripts/office-pack/libreoffice-host-libs.env")

# Dockerfile: must install from the env file, not from its own list.
docker = open(docker_path, encoding="utf-8").read()
if "COPY scripts/office-pack/libreoffice-host-libs.env" not in docker or "OFFICE_DEB_LIBS" not in docker:
    problems.append(f"{docker_path}: does not install OFFICE_DEB_LIBS from scripts/office-pack/libreoffice-host-libs.env")

if problems:
    print("\n".join(problems))
    sys.exit(1)
n = sum(len(v) for v in lists.values())
print(f"check-office-host-libs-sync: OK ({n} entries; install.sh, .goreleaser.yaml and the Dockerfile agree)")
PY
