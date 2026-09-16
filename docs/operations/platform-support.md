# Platform support

Omnipus is a single static Go binary. Most Go cross-compile targets will build, but only a subset is exercised in CI and shipped as a signed release artifact. This page tracks what is covered by the v0.1 release matrix, what is planned but deferred, and what to do if you need an unlisted platform.

Omnipus supports three operating systems: **Linux, macOS, and Windows**. What "supported" means differs per platform, and the differences are stated below rather than averaged away. The BSD family (FreeBSD, OpenBSD, NetBSD) is **not supported**: no CI leg builds it, no release artifact ships for it, and nothing is tested there. A binary you cross-compile for a BSD target yourself is untested and unsupported.

The minimum supported Go toolchain is `go 1.26.4` (`go.mod:3`). All listed platforms build with `CGO_ENABLED=0` and the standard release build tags `goolm,stdjson`.

## Officially supported in v0.1 (CI-tested)

The `cross-platform.yml` workflow is a required PR gate and runs on every push to `main`. It builds, tests, and smoke-boots the binary on three runner targets:

| OS | Architecture | Runner | Sandbox |
|---|---|---|---|
| Linux | amd64 (`x86_64`) | `ubuntu-latest` | Full Landlock + seccomp on kernel 5.13+. Landlock ABI is detected at boot; ABI v4 enables kernel-level `NET_BIND_TCP` and `NET_CONNECT_TCP` rules (kernel 6.7+). |
| Linux | arm64 (`aarch64`) | `ubuntu-24.04-arm` | Same as amd64. The `seccomp_linux_arm64.go` BPF emitter is exercised here. |
| macOS | arm64 (Apple Silicon) | `macos-latest` | Seatbelt confines every process Omnipus starts; the gateway process itself is not confined. Application-level checks apply to the gateway, and to children too only when `sandbox-exec` is missing or disabled. |

Source of truth: `.github/workflows/cross-platform.yml:24-34`. Each job runs `go build`, `go test -short`, and a smoke boot of `omnipus start` that hits `/health`. Signed release binaries are produced for these same three targets by `.goreleaser.yaml:52-64`.

## Planned but not in v0.1

Each of these targets is deferred from v0.1; the linked tracking issue (where one exists) captures the work required to add it to CI and to the release matrix.

### macOS amd64 (Intel)

The cross-compile path works (`GOOS=darwin GOARCH=amd64 go build ./cmd/omnipus`) and is explicitly excluded from `.goreleaser.yaml:60-64`. Adding a `macos-13` smoke runner to `cross-platform.yml` is a v0.1.1 task. The sandbox behaves as on arm64: Seatbelt confines spawned children, and the gateway process itself is not confined.

### Windows amd64

Tracked as [#113](https://github.com/elicify-ai/omnipus/issues/113). Roughly 15 unit tests assume POSIX semantics (file mode bits, advisory `flock`, fork-time signals), so the full suite does not run on Windows yet. CI does cover Windows in two ways today: the `windows-compile` job cross-compiles the production code for `windows/amd64` on every PR, and the `windows-daemon-tests` job runs the daemon package's tests on a real Windows runner.

There is no kernel sandbox on Windows: `selectBackendPlatform` returns the application-level fallback everywhere Windows runs. Job Objects are used, but only to cap a child's memory and to kill children when the gateway dies — not for confinement. A real confinement design exists (a low-integrity token plus deny rules on secrets, no admin rights needed) and is deliberately deferred; #113 tracks Windows test coverage, not the sandbox backend.

Windows also has no available-memory reader. Agent concurrency therefore stays at its unmeasurable-host floor unless you set `performance.max_parallel_agents` explicitly, and the browser pool permits one browser for the whole host. No amount of physical RAM raises those automatic floors. Windows support is degraded and unsupported for these memory-governed features.

This differs from Linux with an unreadable `/proc/meminfo`, such as a gVisor, distroless, or hardened-seccomp deployment. That remains a supported deployment, but Omnipus cannot measure its available memory and therefore applies the same conservative floors. Restore a readable procfs or set the agent limit explicitly when the deployment can support more concurrency.

### Linux riscv64, loong64, armv7, mipsle

Go has cross-compile targets but there are no GitHub Actions runners and no smoke tests. Build with `GOOS=linux GOARCH=<arch> go build -tags goolm,stdjson ./cmd/omnipus`. The seccomp BPF emitter is currently architecture-gated to amd64 and arm64 (`pkg/sandbox/seccomp_linux_amd64.go`, `pkg/sandbox/seccomp_linux_arm64.go`); on other architectures the gateway should fall back gracefully but the path is not exercised.

## Building for an unsupported platform

For any target Go supports, the standard build steps work:

```bash
npm run build
rm -rf pkg/gateway/spa/assets && cp -r dist/spa/* pkg/gateway/spa/
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -tags goolm,stdjson -o omnipus-<os>-<arch> ./cmd/omnipus/
```

The kernel sandbox is Linux-only by construction (`//go:build linux` on `pkg/sandbox/sandbox_linux.go` and `pkg/sandbox/seccomp_linux.go`). On macOS, Seatbelt confines the processes Omnipus starts, but never the gateway itself. On Windows and every other GOOS there is no kernel confinement at all: `sandbox.SelectBackend()` returns the application-level `FallbackBackend`. The gateway will still boot, the SPA will still serve, and tool path-guard checks still apply in Go — but security guarantees are best-effort and the threat model documented in `docs/operations/sandbox-limitations.md` applies in full.

The BSD family (FreeBSD, OpenBSD, NetBSD) falls under this section: it is not a supported platform, nothing is built or tested there, and whatever a cross-compile produces is yours alone.

There is no plan to backport Landlock-equivalent enforcement to non-Linux platforms; the upstream LSM does not exist outside the Linux kernel. The deferred Windows confinement design is described above under "Windows amd64".

## Reporting platform-specific issues

For platform-specific bugs, file an issue on [github.com/elicify-ai/omnipus/issues](https://github.com/elicify-ai/omnipus/issues) with the following information.

**`omnipus doctor` output** — runs pre-flight configuration checks and exits non-zero if any warning is raised (`cmd/omnipus/internal/doctor/command.go:42-63`). Current checks cover DM-policy gaps, exec-egress configuration, and preview-port collisions.

**`/api/v1/security/sandbox-status` response** — run `curl -s http://localhost:5000/api/v1/security/sandbox-status` (admin-authenticated) to confirm the active backend, kernel ABI, and which Landlock features the kernel reports as supported.

**`~/.omnipus/logs/gateway_panic.log`** — include this file's contents if the gateway exited silently on boot.

**Kernel/OS version** — run `uname -a` on Linux; `sw_vers` on macOS; `winver` on Windows.

If the issue reproduces on Linux amd64 or arm64 (the two officially-supported Linux targets), please flag that explicitly — those reproductions block release and get prioritised over the deferred-platform tracking issues.
