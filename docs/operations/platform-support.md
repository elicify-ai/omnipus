# Platform support

Omnipus is a single static Go binary. Most Go cross-compile targets will build, but only a subset is exercised in CI and shipped as a signed release artifact. This page tracks what is covered by the v0.1 release matrix, what is planned but deferred, and what to do if you need an unlisted platform.

The minimum supported Go toolchain is `go 1.26.3` (`go.mod:3`). All listed platforms build with `CGO_ENABLED=0` and the standard release build tags `goolm,stdjson`.

## Officially supported in v0.1 (CI-tested)

The `cross-platform.yml` workflow is a required PR gate and runs on every push to `main`. It builds, tests, and smoke-boots the binary on three runner targets:

| OS | Architecture | Runner | Sandbox |
|---|---|---|---|
| Linux | amd64 (`x86_64`) | `ubuntu-latest` | Full Landlock + seccomp on kernel 5.13+. Landlock ABI is detected at boot; ABI v4 enables kernel-level `NET_BIND_TCP` and `NET_CONNECT_TCP` rules (kernel 6.7+). |
| Linux | arm64 (`aarch64`) | `ubuntu-24.04-arm` | Same as amd64. The `seccomp_linux_arm64.go` BPF emitter is exercised here. |
| macOS | arm64 (Apple Silicon) | `macos-latest` | Application-level `FallbackBackend` only — no LSM. Path checks are enforced in Go. |

Source of truth: `.github/workflows/cross-platform.yml:24-34`. Each job runs `go build`, `go test -short`, and a smoke boot of `omnipus start --allow-empty` that hits `/health`. Signed release binaries are produced for these same three targets by `.goreleaser.yaml:52-64`.

## Planned but not in v0.1

Each of these targets is deferred from v0.1; the linked tracking issue (where one exists) captures the work required to add it to CI and to the release matrix.

- **macOS amd64 (Intel)** — Cross-compile path works (`GOOS=darwin GOARCH=amd64 go build ./cmd/omnipus`) and is explicitly excluded from `.goreleaser.yaml:60-64`. Adding a `macos-13` smoke runner to `cross-platform.yml` is a v0.1.1 task. The sandbox uses `FallbackBackend` identically to arm64.
- **Windows amd64** — Tracked as [#113](https://github.com/elicify-ai/omnipus/issues/113). Roughly 15 unit tests assume POSIX semantics (file mode bits, advisory `flock`, fork-time signals). The kernel-sandbox story (Job Objects + Restricted Tokens + DACL) described in `docs/internal/BRD/Omnipus Windows BRD appendic.md` is specified but unimplemented; on Windows the sandbox falls back to application-level checks like on macOS.
- **Linux riscv64, loong64, armv7, mipsle** — Go has cross-compile targets but there are no GitHub Actions runners and no smoke tests. Build with `GOOS=linux GOARCH=<arch> go build -tags goolm,stdjson ./cmd/omnipus`. The seccomp BPF emitter is currently architecture-gated to amd64 and arm64 (`pkg/sandbox/seccomp_linux_amd64.go`, `pkg/sandbox/seccomp_linux_arm64.go`); on other architectures the gateway should fall back gracefully but the path is not exercised.
- **FreeBSD, NetBSD** — Go has the cross-compile target but no GitHub Actions runner is available. `FallbackBackend` is the only sandbox option (Landlock is Linux-only).

## Building for an unsupported platform

For any target Go supports, the standard build steps work:

```bash
npm run build
rm -rf pkg/gateway/spa/assets && cp -r dist/spa/* pkg/gateway/spa/
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -tags goolm,stdjson -o omnipus-<os>-<arch> ./cmd/omnipus/
```

The kernel sandbox is Linux-only by construction (`//go:build linux` on `pkg/sandbox/sandbox_linux.go` and `pkg/sandbox/seccomp_linux.go`). On every other GOOS, `sandbox.SelectBackend()` returns the application-level `FallbackBackend`. The gateway will still boot, the SPA will still serve, and tool path-guard checks still apply in Go — but security guarantees are best-effort and the threat model documented in `docs/operations/sandbox-limitations.md` applies in full.

There is no plan to backport Landlock-equivalent enforcement to non-Linux platforms; the upstream LSM does not exist outside the Linux kernel. The Windows story will be addressed via Job Objects + Restricted Tokens once #113 closes.

## Reporting platform-specific issues

For platform-specific bugs, file an issue on [github.com/elicify-ai/omnipus/issues](https://github.com/elicify-ai/omnipus/issues) with:

- Output of `omnipus doctor` — runs pre-flight configuration checks and exits non-zero if any warning is raised (`cmd/omnipus/internal/doctor/command.go:42-63`). Current checks cover DM-policy gaps, exec-egress configuration, and preview-port collisions.
- Output of `curl -s http://localhost:5000/api/v1/security/sandbox-status` (admin-authenticated) — confirms the active backend, kernel ABI, and which Landlock features the kernel reports as supported.
- The contents of `~/.omnipus/logs/gateway_panic.log` if the gateway exited silently on boot.
- Output of `uname -a` on Linux to identify the kernel version; on macOS, `sw_vers`; on Windows, `winver`.

If the issue reproduces on Linux amd64 or arm64 (the two officially-supported Linux targets), please flag that explicitly — those reproductions block release and get prioritised over the deferred-platform tracking issues.
