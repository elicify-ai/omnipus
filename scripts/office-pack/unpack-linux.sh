#!/usr/bin/env bash
# Unpacks the Linux component release files into the folders .goreleaser.yaml
# reads for the deb/rpm packages and the Docker image:
#
#   <components>/linux_amd64/{office,pdfium}/
#   <components>/linux_arm64/{office,pdfium}/
#
# usage: unpack-linux.sh <components dir>
#   <components dir>/release/ must hold the files office-components.yml built.
# The packages and images are built from the very files users download.
set -euo pipefail

components="${1:?components dir}"
release="$components/release"
[ -d "$release" ] || { echo "office-pack: $release missing" >&2; exit 1; }

for goarch in amd64 arm64; do
  dest="$components/linux_$goarch"
  rm -rf "$dest"
  for part in office pdfium; do
    file="$release/${part}_linux_${goarch}.tar.gz"
    [ -f "$file" ] || { echo "office-pack: $(basename "$file") missing from $release" >&2; exit 1; }
    mkdir -p "$dest/$part"
    tar -xzf "$file" -C "$dest/$part"
  done
  for f in office/manifest.json office/program/soffice.bin pdfium/manifest.json pdfium/omnipus-pdfium; do
    [ -f "$dest/$f" ] || { echo "office-pack: linux_$goarch/$f missing after unpacking" >&2; exit 1; }
  done
  echo "office-pack: linux_$goarch: $(find "$dest/office" -type f | wc -l | tr -d ' ') office files, $(find "$dest/pdfium" -type f | wc -l | tr -d ' ') renderer files"
done
