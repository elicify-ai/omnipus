#!/usr/bin/env python3
"""Smoke test of a built PDF renderer release folder (office-preview AD-2).

usage: renderer-smoke.py [--exec "<command prefix>"] <renderer folder>

--exec runs the renderer through a command prefix instead of the folder's
executable, for example `docker run -i --rm --entrypoint <path> <image>`; the
manifest is still checked against the folder (a copy of the image's files).

Checks, and exits non-zero on the first failure:
  1. manifest.json is a packaged pdf-renderer manifest whose file
     fingerprints match the files in the folder;
  2. the renderer answers an "info" request for a one-page PDF with
     page_count 1 and its own build id;
  3. it renders page 1 as a PNG.

The request format mirrors native/omnipus-pdfium/omnipus_pdfium.c: a 32-byte
little-endian header ("OPDF", protocol, op, first, last, max edge, flags,
input length) followed by the PDF; answers are frames of
<type u8><length u32><payload>. stdin stays open until the answer is read,
because the renderer treats end-of-input during page work as "parent gone".
"""

import hashlib
import json
import os
import shlex
import struct
import subprocess
import sys

F_META, F_PAGE, F_TEXT, F_ERROR, F_DONE = 1, 2, 3, 4, 5
OP_INFO, OP_RENDER = 1, 2

# A minimal valid one-page PDF (US Letter, empty page). Offsets in the xref
# table are computed below, so the bytes stay valid.
def one_page_pdf():
    objs = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
    ]
    out = bytearray(b"%PDF-1.4\n")
    offsets = []
    for i, body in enumerate(objs, start=1):
        offsets.append(len(out))
        out += b"%d 0 obj\n" % i + body + b"\nendobj\n"
    xref = len(out)
    out += b"xref\n0 %d\n" % (len(objs) + 1)
    out += b"0000000000 65535 f \n"
    for off in offsets:
        out += b"%010d 00000 n \n" % off
    out += b"trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n" % (len(objs) + 1, xref)
    return bytes(out)


def fail(msg):
    print("renderer-smoke: FAIL: " + msg, file=sys.stderr)
    sys.exit(1)


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def check_manifest(folder):
    with open(os.path.join(folder, "manifest.json"), encoding="utf-8") as f:
        m = json.load(f)
    if m.get("component") != "pdf-renderer":
        fail("manifest component is %r" % m.get("component"))
    if m.get("trust") != "packaged":
        fail("manifest trust is %r, want packaged" % m.get("trust"))
    files = m.get("files") or {}
    if m.get("executable") not in files:
        fail("manifest does not fingerprint its executable")
    for name, want in sorted(files.items()):
        got = sha256_file(os.path.join(folder, name))
        if got != want:
            fail("%s fingerprint mismatch: %s != %s" % (name, got, want))
    lines = "".join("%s %s\n" % (n, files[n]) for n in sorted(files))
    if hashlib.sha256(lines.encode()).hexdigest() != m.get("file_list_sha256"):
        fail("file_list_sha256 does not match the files")
    return m


def run(argv, op, pdf, first=0, last=0, maxedge=0):
    header = b"OPDF" + struct.pack("<HHIIIIQ", 1, op, first, last, maxedge, 0, len(pdf))
    proc = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    proc.stdin.write(header + pdf)
    proc.stdin.flush()
    frames = []
    while True:
        head = proc.stdout.read(5)
        if len(head) < 5:
            break
        ftype, length = head[0], struct.unpack("<I", head[1:])[0]
        payload = proc.stdout.read(length)
        frames.append((ftype, payload))
        if ftype in (F_DONE, F_ERROR):
            break
    proc.stdin.close()
    err = proc.stderr.read()
    code = proc.wait(timeout=60)
    if code != 0:
        fail("renderer exited %d (op %d): %s %r" % (code, op, err.decode(errors="replace"), frames))
    return frames


def main():
    args = sys.argv[1:]
    prefix = None
    if len(args) == 3 and args[0] == "--exec":
        prefix = shlex.split(args[1])
        args = args[2:]
    if len(args) != 1:
        fail('usage: renderer-smoke.py [--exec "<command prefix>"] <renderer folder>')
    folder = os.path.abspath(args[0])
    m = check_manifest(folder)
    exe = prefix if prefix else [os.path.join(folder, m["executable"])]
    pdf = one_page_pdf()

    frames = run(exe, OP_INFO, pdf)
    metas = [json.loads(p) for t, p in frames if t == F_META]
    if not metas or metas[0].get("page_count") != 1:
        fail("info answered %r" % frames)
    if metas[0].get("build_id") != m.get("build_id"):
        fail("renderer build id %r differs from manifest %r" % (metas[0].get("build_id"), m.get("build_id")))
    if frames[-1][0] != F_DONE:
        fail("info did not end with DONE: %r" % frames)

    frames = run(exe, OP_RENDER, pdf, first=1, last=1, maxedge=800)
    pages = [p for t, p in frames if t == F_PAGE]
    if len(pages) != 1:
        fail("render answered %d pages: %r" % (len(pages), [t for t, _ in frames]))
    page_no, width, height = struct.unpack("<III", pages[0][:12])
    if page_no != 1 or width < 1 or height < 1 or pages[0][12:20] != b"\x89PNG\r\n\x1a\n":
        fail("page frame is not a PNG of page 1 (page %d, %dx%d)" % (page_no, width, height))
    print("renderer-smoke: OK %s: info page_count=1, page 1 rendered %dx%d PNG (%d bytes)"
          % (m.get("build_id"), width, height, len(pages[0]) - 12))


if __name__ == "__main__":
    main()
