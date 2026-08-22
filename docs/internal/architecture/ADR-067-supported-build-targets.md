# ADR-067 — Four binaries, one container image; everything else is dropped

- **Status:** Accepted (2026-08-22) — founder decided the target set directly; the
  Intel-Mac reversal in §4 was decided in the same session.
- **Date:** 2026-08-22
- **Deciders:** founder (decided the target set), lead (inventory, mechanism, CI impact)
- **Related:** [ADR-062 — Reads and execute default open](ADR-062-filesystem-read-exec-model-inversion.md)
  (the `permissive` sandbox default the container image ships with, see §7);
  `.goreleaser.yaml` (release matrix); `Makefile::build-all` (developer matrix);
  `docker/` (five Dockerfiles, reduced to one).
- **Evidence level:** claims marked **[VERIFIED]** were read from the repository at
  commit `c271781d` or executed on this host (darwin/amd64, x86_64). Claims marked
  **[INFERRED]** are reasoned, not run.
- **Supersedes in part:** the `darwin/amd64` `ignore` block in `.goreleaser.yaml`;
  the five surplus targets in `Makefile::build-all`; `build-lite` and the `lite`
  build tag; four of the five files in `docker/`.

---

## 1. Decision

Omnipus ships **four binaries and one container image**:

| Target | Audience |
|---|---|
| `linux/amd64` | Servers, VPS, most self-hosting |
| `linux/arm64` | Raspberry Pi, ARM cloud instances |
| `darwin/arm64` | Apple Silicon desktop |
| `darwin/amd64` | Intel Mac desktop — **and the project's primary development host** |
| Container image | Multi-arch `amd64` + `arm64`, built from the current `docker/Dockerfile.heavy` |

Everything else is dropped: `windows/amd64`, `linux/arm` (v6), `linux/armv7`,
`linux/loong64`, `linux/riscv64`, `linux/mipsle`, the `lite` build variant, and four
of the five Dockerfiles.

**CI tests exactly this set — no more, no less.**

## 2. Context

Three inventories disagreed with each other. **[VERIFIED]**

| Inventory | Produces |
|---|---|
| `.goreleaser.yaml` (what actually ships) | 3 binaries |
| `Makefile::build-all` (what developers build) | 9 binaries |
| `docker/` | 5 Dockerfiles |

Six targets were built by the Makefile and never released. Windows was the starkest
case: Windows-specific sandbox code exists (Job Objects, Restricted Tokens, DACL),
`GOOS=windows go build` succeeds, no CI job exercises it, and no binary ships. The
`lite` variant was built weekly by `lite-build-weekly.yml` and could not be downloaded
by anyone.

A target that is compiled but neither tested nor shipped is the worst of the three
states: it constrains every change, and protects no user.

## 3. Why each dropped target was dropped

**Windows** — descoped for now. Not a judgement that Windows is unimportant; a
judgement that half-support is worse than either alternative. Revisit as its own
decision, with a CI leg, or delete `pkg/sandbox`'s Windows backend.

**`linux/arm` (v6) and `linux/armv7`** — superseded by `arm64`. Every Raspberry Pi
from the 3B onwards runs a 64-bit kernel.

**`loong64`, `riscv64`, `mipsle`** — no evidence of users, and a real code cost.
`mipsle` alone forces `GOFLAGS_NO_GOOLM` (a Matrix-less build) and contributes to the
build constraint `!lite && !mipsle && !netbsd && !(freebsd && arm)` **[VERIFIED]**,
which every contributor has to reason about forever to serve a platform nobody has
asked for. Anyone who needs one can `go build`.

**The `lite` variant** — saved roughly 58 MB by dropping whatsmeow. It was never
published, so the saving reached no one, while `!lite` conditions spread through the
codebase (36 files carry `//go:build !lite` **[VERIFIED]**). Not worth a second
variant and a second tag matrix.

**Four Dockerfiles** — `Dockerfile` (minimal), `Dockerfile.full`,
`Dockerfile.goreleaser`, `Dockerfile.goreleaser.launcher`. Five ways to package one
binary is four too many. `Dockerfile.heavy` is kept because a container is where
users least want to discover a missing dependency: it carries Node.js 24 for MCP
servers and the browser dependencies, which the minimal image does not.

## 4. Reversal: Intel Mac returns to the release set

`.goreleaser.yaml` carried an explicit exclusion **[VERIFIED]**:

```yaml
ignore:
  # darwin/amd64 (Intel Mac) is deferred to v0.1.1. Cross-compile target
  # works, but we haven't smoke-tested it in CI (cross-platform.yml uses
  # macos-latest = arm64). Adding macos-13 runner is a v0.1.1 task.
  - goos: darwin
    goarch: amd64
```

Both premises have expired:

1. **"Deferred to v0.1.1"** — this *is* v0.1.1.
2. **"Haven't smoke-tested it"** — Intel Mac is the most heavily exercised platform in
   the project. This host is `darwin/amd64` **[VERIFIED]**; every local verification
   during the 2026-08-22 lint-enablement work — the full `pkg/agent` suite, mutation
   tests, `golangci-lint` runs, the seccomp analysis — ran on it.

The exclusion's *mechanism* remains true and becomes an action item: `macos-latest` is
Apple Silicon, so no CI job has ever run on Intel Mac. Shipping the binary requires
adding the `macos-13` leg the comment already identified. Until that lands, we would be
releasing a binary no automated test has touched — heavily developed on, but not
automatically verified.

## 5. Consequences for CI

- **`cross-platform.yml` grows a fourth leg** (`macos-13`) and becomes the exact
  mirror of the release matrix. That symmetry is the argument for repairing it rather
  than deleting it — see §6.
- **`lite-build-weekly.yml` is deleted.** Its only purpose was the dropped variant.
- **`Makefile::build-all` drops five targets.** Developer cross-compilation cost falls.
- **Net runner cost is roughly neutral** — one Mac leg added, five cross-compile
  targets removed.
- **Build tags simplify.** `!lite` (36 files), `!mipsle`, `!netbsd` and
  `!(freebsd && arm)` exist only to serve dropped targets and can be removed as
  follow-up work.

## 6. Risks accepted

**A dropped platform's users have no binary.** Mitigated by the source build being a
single Go command with no CGo. **[INFERRED]** — we have no telemetry, so this is a
judgement about likely usage, not a measurement.

**Windows sandbox code becomes dead weight.** It stays compiling but untested and
unshipped. This ADR does not delete it; it records that it is now unowned and must be
either adopted or removed by a later decision.

**Intel Mac ships before its CI leg exists** if step ordering slips. The mitigation is
ordering, not hope: add the `macos-13` leg *before* removing the `ignore` block.

## 7. Note on the container image's sandbox default

`docker/Dockerfile.heavy` sets `ENV OMNIPUS_SANDBOX_MODE=permissive` **[VERIFIED]**.
This is defensible — the container boundary is the confinement, and in-process
sandboxing inside an already-isolated container buys little — but it means the shipped
image runs with kernel-level confinement disabled by default. Recording it here so the
choice is explicit rather than inherited. It interacts with ADR-062's reads-open model:
inside the image, both layers are permissive.

## 8. Implementation

1. Add the `macos-13` leg to `cross-platform.yml`. **Must precede step 2.**
2. Remove the `darwin/amd64` `ignore` block from `.goreleaser.yaml`.
3. Reduce `Makefile::build-all` from 9 targets to the four supported ones.
4. Delete `build-lite`, the `lite` tag, and `lite-build-weekly.yml`.
5. Delete `docker/Dockerfile`, `Dockerfile.full`, `Dockerfile.goreleaser`,
   `Dockerfile.goreleaser.launcher`; publish `Dockerfile.heavy` as a multi-arch
   manifest for `amd64` and `arm64`.
6. Follow-up (not blocking): remove the `!lite`, `!mipsle`, `!netbsd` and
   `!(freebsd && arm)` build constraints now that nothing needs them.
