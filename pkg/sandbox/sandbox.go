// Package sandbox provides kernel-level and application-level process sandboxing.
//
// It implements SEC-01 (Landlock), SEC-02 (seccomp), and SEC-03 (child process
// inheritance) from the Omnipus BRD. Two backends are provided:
//
//   - LinuxBackend: Landlock + seccomp on Linux 5.13+ (sandbox_linux.go)
//   - FallbackBackend: application-level path checks on all platforms
//
// Backend selection follows a capability cascade: detect platform, detect kernel
// features, select the highest-capability backend, log the active enforcement level.
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Access permission flags for PathRule.
const (
	AccessRead    uint64 = 1 << 0
	AccessWrite   uint64 = 1 << 1
	AccessExecute uint64 = 1 << 2
)

// PathRule describes filesystem access for a single path.
type PathRule struct {
	Path   string
	Access uint64
}

// NetPortRule describes a single TCP port that the policy whitelists for the
// sandboxed process. Landlock ABI v4 expresses bind rules per single port —
// the kernel does not accept ranges — so callers expand any port range into
// one NetPortRule per port. Used by SandboxPolicy.BindPortRules.
type NetPortRule struct {
	Port uint16
}

// SandboxPolicy describes sandbox restrictions to apply.
//
// BindPortRules are honored by the Landlock backend on kernels exposing ABI v4
// (5.19+ for the syscalls, 6.7+ for the net access rights). They are
// leave-empty-for-no-restriction: a nil/empty list means the kernel does NOT
// install handled_access_net at all, so legacy ABI < 4 kernels retain
// unrestricted networking. When non-empty, the kernel denies any bind() to a
// TCP port not enumerated here.
//
// ConnectPortRules — re-introduced in v0.2 (#155 item 4) — install kernel-
// enforced port-level outbound allow-listing via Landlock NET_CONNECT_TCP on
// ABI v4+. A non-empty list activates connect filtering: connect(2) to any
// destination port not enumerated here returns EACCES from the kernel. The
// list applies process-wide and is inherited by every forked child, so a
// hardened-exec child that issues a raw `socket()+connect()` syscall sequence
// to e.g. 127.0.0.1:1 is denied at the kernel layer rather than at the
// userspace egress-proxy layer (which it would otherwise bypass).
//
// Limitations of the kernel mechanism:
//   - Port-level only. Landlock NET_CONNECT_TCP cannot filter by destination
//     IP/CIDR. A child connecting to https://192.168.1.1/ on port 443 (a
//     port we must allow for legitimate LLM/HTTPS traffic) is permitted.
//     CIDR-level filtering for the same code paths is enforced separately
//     by the Go-side SSRFChecker (cfg.Sandbox.EgressAllowCIDRs and the SSRF
//     allow_internal list); however, the SSRFChecker only applies to HTTP
//     clients we control — a compiled binary spawned via workspace.shell
//     bypasses it.
//   - Pre-ABI v4 kernels silently degrade: ConnectPortRules are computed but
//     not enforced. A boot-time WARN documents this.
type SandboxPolicy struct {
	FilesystemRules   []PathRule
	BindPortRules     []NetPortRule
	ConnectPortRules  []NetPortRule
	InheritToChildren bool
}

// Mode selects how the sandbox enforces policy. Sprint J / BRD SEC-01..03.
// The zero value ("") is treated as ModeEnforce by ParseMode so that partial
// struct literals in tests don't accidentally disable enforcement.
type Mode string

const (
	// ModeEnforce enforces policy at the kernel layer (Landlock+seccomp on
	// Linux 5.13+). Violating syscalls return EACCES (filesystem) or EPERM
	// (seccomp). This is the production default on capable kernels.
	ModeEnforce Mode = "enforce"

	// ModePermissive computes and audit-logs policy without enforcing it.
	// Seccomp uses SECCOMP_RET_LOG rather than SECCOMP_RET_ERRNO.
	// On Linux < 6.12 (no native permissive Landlock), landlock_restrict_self
	// is skipped entirely and the mode effectively degrades to audit-only.
	// Intended for pre-enforcement audit in production rollouts. A prominent
	// stderr banner repeats every 60 seconds while the gateway runs.
	ModePermissive Mode = "permissive"

	// ModeOff disables the sandbox completely. Apply and Install are not
	// called. Intended for local development with debuggers and tracers.
	// When combined with OMNIPUS_ENV=production, a WARN banner repeats every
	// 60 seconds to alert operators.
	ModeOff Mode = "off"
)

// ParseMode normalizes a string to a Mode value. Empty string and the legacy
// "enabled"/"disabled" aliases are accepted for backwards compatibility.
// Returns an error for any other unrecognized value so CLI parsing can reject
// typos with an explicit "usage error" exit (code 2).
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "enforce", "enabled":
		return ModeEnforce, nil
	case "permissive", "audit":
		return ModePermissive, nil
	case "off", "disabled", "false", "none":
		return ModeOff, nil
	}
	return "", fmt.Errorf("invalid sandbox mode %q; must be one of: enforce, permissive, off", s)
}

// SystemRestrictedPaths is the canonical list of path prefixes that user
// AllowedPaths entries may NOT grant write access to. When a user rule
// overlaps any of these, DefaultPolicy strips the Write bit and keeps only
// Read. Keeping this list narrow prevents sandboxed code from modifying the
// system (SSH keys, init scripts, kernel modules, etc) even if an operator
// mistakenly whitelists them for read access.
//
// Order-independent. Each entry matches itself and any child path (prefix
// match on the directory boundary).
var SystemRestrictedPaths = []string{
	"/etc",
	"/proc",
	"/sys",
	"/dev",
	"/boot",
	"/root",
}

// isSystemRestricted returns true if path equals or lies under any entry in
// SystemRestrictedPaths. The path is cleaned first so that traversal sequences
// like "../../../etc" resolve to /etc before the check.
func isSystemRestricted(path string) bool {
	clean := filepath.Clean(path)
	for _, restricted := range SystemRestrictedPaths {
		if clean == restricted || strings.HasPrefix(clean, restricted+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// DefaultConnectPorts is the baseline outbound TCP port allow-list installed
// by DefaultPolicy via the Landlock NET_CONNECT_TCP rule type (kernel ABI v4+).
// Any connect(2) to a destination port not in this list is denied with EACCES
// at the kernel layer for the gateway and every forked child.
//
// The set is intentionally minimal:
//   - 53  — DNS over TCP (resolver fallback when UDP is truncated).
//   - 80  — plain HTTP (egress-proxy upstreams, package registries).
//   - 443 — HTTPS (LLM provider calls, all production HTTP traffic).
//
// Operators who need additional outbound ports (e.g. SMTP, custom registries)
// extend this set via the gateway's connect-port computation in
// pkg/gateway/sandbox_apply.go — DevServerPortRange entries are appended so
// children can reach loopback dev servers and the egress proxy.
//
// Threat note: this allow-list is port-level only. Landlock NET_CONNECT_TCP
// cannot filter by destination IP, so a child can still reach 192.168.1.1:443
// or 127.0.0.1:80. CIDR-level filtering for code paths the gateway controls
// is layered on top via pkg/security/SSRFChecker.
var DefaultConnectPorts = []uint16{53, 80, 443}

// SecretFilesRelative is the list of file basenames under $OMNIPUS_HOME that
// hold cryptographic key material or other root-of-trust state. Tool-exec
// children (and the redteam carve-out test) MUST NOT have these paths in
// their Landlock allow-tree even when the rest of $OMNIPUS_HOME is granted.
// Closes pentest items C1 (master.key exfil) and C2 (credentials.json
// exfil) per v0.2 #155 item 8.
var SecretFilesRelative = []string{
	"master.key",
	"credentials.json",
}

// DefaultChildPolicy is the narrowed policy DESIGN for tool-exec children.
// It returns the same shape as DefaultPolicy but with the
// SecretFilesRelative carve-out applied to $OMNIPUS_HOME — the home root
// is NOT granted as a single tree; instead each existing subdirectory is
// granted RWX individually, and top-level files matching
// SecretFilesRelative are skipped. This relies on Landlock's hierarchical
// allow-tree semantics: a file not under any granted tree is unreachable.
//
// **Production wiring is NOT yet active.** As of v0.2 (#155 item 8) this
// function is exercised only by the redteam tests and unit tests, which
// apply it directly to a re-execed test child. The production sandbox-
// apply path (pkg/gateway/sandbox_apply.go) calls DefaultPolicy at gateway
// boot, and tool-exec children inherit that policy unchanged across
// fork+exec. The kernel-level path-guard against C1/C2 therefore depends
// on:
//   - The shell-guard regex in pkg/tools/shell.go (blocks `master.key` and
//     `credentials.json` literal tokens in commands)
//   - HardenGatewaySelf (pkg/sandbox/self_hardening_linux.go) preventing
//     same-uid /proc/<gateway>/environ reads
//   - The kernel sandbox itself (Landlock + seccomp) blocking arbitrary
//     filesystem traversal via syscalls outside DefaultPolicy
//
// Wiring DefaultChildPolicy into production via per-thread Landlock
// re-restriction is tracked as a v0.3 follow-up (#156 architectural
// work). The pattern is described in `RestrictCurrentThread`'s contract.
// Until that lands, this function exists for testing and to document the
// intended structure.
//
// Documents (does not close at the kernel layer): pentest items C1, C2.
func DefaultChildPolicy(
	homePath string,
	allowedPaths []string,
	warnFn func(msg string, path string),
	bindPorts []uint16,
) SandboxPolicy {
	policy := DefaultPolicy(homePath, allowedPaths, warnFn, bindPorts)
	if homePath == "" {
		return policy
	}
	cleanHome := filepath.Clean(homePath)

	// Strip the original $OMNIPUS_HOME RWX rule.
	filtered := policy.FilesystemRules[:0]
	for _, r := range policy.FilesystemRules {
		if filepath.Clean(r.Path) == cleanHome {
			continue
		}
		filtered = append(filtered, r)
	}
	policy.FilesystemRules = filtered

	entries, err := os.ReadDir(cleanHome)
	if err != nil {
		if warnFn != nil {
			warnFn("DefaultChildPolicy: cannot enumerate $OMNIPUS_HOME for carve-out", cleanHome)
		}
		return policy
	}

	secretSet := make(map[string]struct{}, len(SecretFilesRelative))
	for _, name := range SecretFilesRelative {
		secretSet[name] = struct{}{}
	}

	for _, e := range entries {
		name := e.Name()
		if _, isSecret := secretSet[name]; isSecret {
			continue
		}
		path := filepath.Join(cleanHome, name)
		if e.IsDir() {
			policy.FilesystemRules = append(policy.FilesystemRules, PathRule{
				Path:   path,
				Access: AccessRead | AccessWrite | AccessExecute,
			})
			continue
		}
		policy.FilesystemRules = append(policy.FilesystemRules, PathRule{
			Path:   path,
			Access: AccessRead | AccessWrite,
		})
	}
	return policy
}

// DefaultPolicy builds the workspace SandboxPolicy from the Omnipus home
// directory and an optional list of user-declared additional allowed paths.
// Implements FR-J-003 and FR-J-013 (user read wins, write unconditionally
// stripped on system-restricted paths).
//
// The returned policy grants:
//   - RWX on $OMNIPUS_HOME and /tmp (workspace + scratch)
//   - R on common system library and CA cert directories (/proc/self,
//     /lib, /lib64, /usr/lib, /usr/lib64, /usr/bin, /etc/ssl, /etc/ca-certificates,
//     /sys/devices/system/cpu) — gateway needs to read shared objects, DNS
//     resolver config, and TLS trust store at runtime.
//   - R-only on any user-declared path that overlaps SystemRestrictedPaths
//     (Write bit silently stripped, WARN logged via the provided warnFn).
//   - RW on any user-declared path that does NOT overlap system-restricted
//     paths.
//
// warnFn may be nil; when set, it is invoked once per stripped rule with a
// human-readable message. The gateway passes slog.Warn here so the message
// lands in the structured log alongside the sandbox.applied event.
//
// Duplicate paths are NOT deduplicated — Landlock silently accepts duplicates
// and takes the union of access rights per path.
//
// bindPorts populates SandboxPolicy.BindPortRules. Each uint16 entry becomes
// one NetPortRule. Pass nil (the historical caller default) to leave bind
// rules empty, which preserves pre-ABI-v4 behavior of unrestricted bind. The
// gateway expands cfg.Sandbox.DevServerPortRange into bindPorts so dev
// servers can bind their assigned port.
//
// ConnectPortRules are populated unconditionally with DefaultConnectPorts
// (v0.2 #155 item 4 — default-deny outbound). On Landlock ABI v4+ this
// activates kernel-level enforcement: connect(2) to any port outside the
// allow-list returns EACCES. On older kernels the field is computed but not
// enforced (a boot-time WARN documents the degradation). Callers that need
// to extend or replace the connect-port set should call DefaultPolicy first
// and then mutate the returned policy's ConnectPortRules slice.
func DefaultPolicy(
	homePath string,
	allowedPaths []string,
	warnFn func(msg string, path string),
	bindPorts []uint16,
) SandboxPolicy {
	rules := make([]PathRule, 0, 16+len(allowedPaths))

	// Workspace: full RWX on $OMNIPUS_HOME. This is where agents write
	// sessions, credentials, config, skills, and state.
	if homePath != "" {
		rules = append(rules, PathRule{
			Path:   filepath.Clean(homePath),
			Access: AccessRead | AccessWrite | AccessExecute,
		})
	}

	// Scratch: /tmp is the POSIX convention for transient files. Agents,
	// exec tools, and temp-file helpers all write here.
	rules = append(rules, PathRule{
		Path:   "/tmp",
		Access: AccessRead | AccessWrite | AccessExecute,
	})

	// Read-only system dependencies required by the gateway at runtime.
	// Missing paths (e.g. /lib64 on ARM64) are silently skipped by
	// Apply() with a warning log; the remaining rules still succeed.
	readOnlySystem := []string{
		"/proc", // Chromium needs /proc/sys/fs/inotify/* and /proc/<pid>/* across processes.
		"/proc/self",
		"/lib",
		"/lib64",
		"/usr/lib",
		"/usr/lib64",
		"/usr/bin",
		"/opt",              // Chromium and other vendor binaries (e.g. /opt/google/chrome) live here.
		"/etc/alternatives", // /usr/bin/google-chrome resolves through /etc/alternatives.
		"/etc/ssl",
		"/etc/ca-certificates",
		"/etc/resolv.conf",
		"/etc/hosts",
		"/etc/nsswitch.conf",
		"/sys/devices/system/cpu",
		"/dev/urandom", // RNG source used by libc, OpenSSL, Chromium, etc.
		"/dev/random",
	}
	for _, p := range readOnlySystem {
		rules = append(rules, PathRule{
			Path:   p,
			Access: AccessRead | AccessExecute, // exec bit lets dynamic loader mmap .so files
		})
	}

	// Universally writable device files required by Chromium/headless tools
	// and any process that redirects stdio. /dev/null and /dev/shm are safe
	// to expose RW because they are well-known sinks/scratch areas, not real
	// hardware. Without these, browser.* tools fail with
	// "open /dev/null: permission denied" under the workspace+net profile.
	rules = append(rules,
		PathRule{Path: "/dev/null", Access: AccessRead | AccessWrite},
		PathRule{Path: "/dev/shm", Access: AccessRead | AccessWrite},
	)

	// User-declared additional paths (FR-J-013).
	for _, raw := range allowedPaths {
		if raw == "" {
			continue
		}
		clean := filepath.Clean(raw)
		if isSystemRestricted(clean) {
			// Strip Write bit — user intent (read) is preserved, but
			// write access to /etc, /proc, /sys, /dev, /boot, /root and
			// their children is unconditionally denied.
			if warnFn != nil {
				warnFn(
					"User sandbox policy allows read on restricted system path; write access is still denied.",
					clean,
				)
			}
			rules = append(rules, PathRule{
				Path:   clean,
				Access: AccessRead,
			})
			continue
		}
		rules = append(rules, PathRule{
			Path:   clean,
			Access: AccessRead | AccessWrite,
		})
	}

	var bindRules []NetPortRule
	if len(bindPorts) > 0 {
		bindRules = make([]NetPortRule, 0, len(bindPorts))
		for _, p := range bindPorts {
			bindRules = append(bindRules, NetPortRule{Port: p})
		}
	}

	// v0.2 (#155 item 4): default-deny outbound TCP via Landlock
	// NET_CONNECT_TCP. The kernel installs the allow-list once and inherits
	// it to every child forked from the restricted thread, so a hardened-
	// exec child issuing raw socket()+connect() to e.g. 127.0.0.1:1 is
	// denied at the kernel layer rather than reaching the network stack.
	// We allocate a fresh slice (not aliased to DefaultConnectPorts) so a
	// caller mutating policy.ConnectPortRules cannot pollute the package-
	// level baseline used by future DefaultPolicy() invocations.
	connectRules := make([]NetPortRule, 0, len(DefaultConnectPorts))
	for _, p := range DefaultConnectPorts {
		connectRules = append(connectRules, NetPortRule{Port: p})
	}

	return SandboxPolicy{
		FilesystemRules:   rules,
		BindPortRules:     bindRules,
		ConnectPortRules:  connectRules,
		InheritToChildren: true,
	}
}

// SandboxBackend is the interface for platform-specific sandbox enforcement.
type SandboxBackend interface {
	Name() string
	Available() bool
	Apply(policy SandboxPolicy) error
	ApplyToCmd(cmd *exec.Cmd, policy SandboxPolicy) error
}

// ABIResult describes Landlock ABI detection results.
type ABIResult struct {
	Available bool
	Version   int
	Features  []string
}

// DetectLandlockABI returns ABI information based on a probed or mocked ABI version.
// Pass 0 for unavailable, 1-3 for specific ABI versions.
func DetectLandlockABI(abiVersion int) ABIResult {
	if abiVersion <= 0 {
		return ABIResult{Available: false, Version: 0}
	}

	features := []string{
		"EXECUTE", "WRITE_FILE", "READ_FILE", "READ_DIR",
		"REMOVE_DIR", "REMOVE_FILE", "MAKE_CHAR", "MAKE_DIR",
		"MAKE_REG", "MAKE_SOCK", "MAKE_FIFO", "MAKE_BLOCK",
		"MAKE_SYM", "REFER",
	}
	if abiVersion >= 2 {
		features = append(features, "TRUNCATE")
	}
	if abiVersion >= 3 {
		features = append(features, "IOCTL_DEV")
	}
	if abiVersion >= 4 {
		features = append(features, "NET_BIND_TCP", "NET_CONNECT_TCP")
	}

	return ABIResult{
		Available: true,
		Version:   abiVersion,
		Features:  features,
	}
}

// BlockedSyscall pairs a syscall name with its Linux syscall number.
// This is the single source of truth for blocked syscalls — both name-based
// queries and numeric BPF filters derive from this list.
type BlockedSyscall struct {
	Name string
	Nr   uint32 // Linux amd64 syscall number; 0 on non-Linux (populated by seccomp_linux.go)
}

// blockedSyscallNames is the canonical list of blocked syscall names.
// Platform-specific code (seccomp_linux.go) populates Nr values.
var blockedSyscallNames = []string{
	"ptrace",
	"mount",
	"umount2",
	"init_module",
	"finit_module",
	"create_module",
	"delete_module",
	"reboot",
	"swapon",
	"swapoff",
	"pivot_root",
	"kexec_load",
	"kexec_file_load",
	"bpf",
	"perf_event_open",
}

// SeccompProgram represents an assembled seccomp BPF filter program.
type SeccompProgram struct {
	syscalls []BlockedSyscall
	useTSync bool
	// mode controls the BPF return action for blocked syscalls. In
	// ModeEnforce, the filter returns SECCOMP_RET_ERRNO(EPERM) — the
	// syscall fails with EPERM and the process continues. In
	// ModePermissive, the filter returns SECCOMP_RET_LOG — the syscall
	// proceeds to the kernel but an entry is written to the audit log.
	// RET_LOG has been in the kernel since 4.14 so it is always available
	// on our 5.13+ support floor.
	mode Mode
}

// BuildSeccompProgram assembles the seccomp BPF program with all blocked syscalls.
// The program blocks privilege-escalation syscalls with EPERM (not SIGKILL).
func BuildSeccompProgram() *SeccompProgram {
	return BuildSeccompProgramWithMode(ModeEnforce)
}

// BuildSeccompProgramWithMode is the mode-aware variant. ModeEnforce produces
// the same program as BuildSeccompProgram. ModePermissive produces a program
// that logs denied syscalls via SECCOMP_RET_LOG but lets them proceed.
// ModeOff is rejected — callers must not install any seccomp program when
// the sandbox is off.
func BuildSeccompProgramWithMode(mode Mode) *SeccompProgram {
	syscalls := make([]BlockedSyscall, len(blockedSyscallNames))
	for i, name := range blockedSyscallNames {
		syscalls[i] = BlockedSyscall{Name: name}
	}
	return &SeccompProgram{syscalls: syscalls, useTSync: true, mode: mode}
}

// Mode returns the effective mode of the program. ModeEnforce when the
// program was built without an explicit mode.
func (sp *SeccompProgram) Mode() Mode {
	if sp == nil || sp.mode == "" {
		return ModeEnforce
	}
	return sp.mode
}

// Blocks returns true if the given syscall name is blocked by this program.
func (sp *SeccompProgram) Blocks(syscall string) bool {
	for _, sc := range sp.syscalls {
		if sc.Name == syscall {
			return true
		}
	}
	return false
}

// BlockedSyscalls returns the list of blocked syscalls.
func (sp *SeccompProgram) BlockedSyscalls() []BlockedSyscall {
	return sp.syscalls
}

// UsesTSync returns true if the program uses SECCOMP_FILTER_FLAG_TSYNC (SEC-03).
func (sp *SeccompProgram) UsesTSync() bool {
	return sp.useTSync
}

// allowedEntry pairs a path with its permitted access flags.
type allowedEntry struct {
	path   string
	access uint64
}

// FallbackBackend provides application-level path checking when kernel
// sandboxing is unavailable.
type FallbackBackend struct {
	entries []allowedEntry
}

// NewFallbackBackend creates a FallbackBackend.
func NewFallbackBackend() *FallbackBackend {
	return &FallbackBackend{}
}

func (f *FallbackBackend) Name() string    { return "fallback" }
func (f *FallbackBackend) Available() bool { return true }

// Apply records allowed paths and their access flags for application-level enforcement.
// Each path is canonicalized via canonicalizePath (which resolves symlinks with
// filepath.EvalSymlinks) so that macOS /var → /private/var symlinks and similar
// platform conventions are normalized before storage. This keeps CheckPath
// comparisons correct when the OS returns a different representation of the same path.
func (f *FallbackBackend) Apply(policy SandboxPolicy) error {
	f.entries = nil
	for _, rule := range policy.FilesystemRules {
		canonical, err := canonicalizePath(rule.Path)
		if err != nil {
			return fmt.Errorf("sandbox fallback: cannot resolve path %q: %w", rule.Path, err)
		}
		f.entries = append(f.entries, allowedEntry{path: canonical, access: rule.Access})
	}
	return nil
}

// ApplyToCmd injects sandbox policy as environment variables on the child
// process. This is the application-level counterpart to Landlock/seccomp: when
// kernel sandboxing is unavailable (non-Linux platforms, Linux < 5.13, Termux,
// etc.), cooperative child processes (such as Omnipus helper scripts) can read
// OMNIPUS_SANDBOX_PATHS and self-enforce path restrictions.
//
// Threat model: this mechanism ONLY covers cooperative processes. Arbitrary
// uncooperative binaries are NOT contained — that requires kernel enforcement
// (SEC-01 Landlock). This fulfills ADR W-1 by replacing the previous no-op
// with a real mechanism, while documenting the gap that Landlock closes on
// supported kernels.
//
// The filesystem rules are resolved to absolute paths before being passed to
// the child so that relative workspace paths remain meaningful across cwd
// changes inside the child process.
func (f *FallbackBackend) ApplyToCmd(cmd *exec.Cmd, pol SandboxPolicy) error {
	if cmd == nil {
		return fmt.Errorf("sandbox fallback: nil cmd")
	}
	if len(pol.FilesystemRules) == 0 {
		return nil
	}
	paths := make([]string, 0, len(pol.FilesystemRules))
	for _, rule := range pol.FilesystemRules {
		abs, err := filepath.Abs(rule.Path)
		if err != nil {
			return fmt.Errorf("sandbox fallback: cannot resolve path %q: %w", rule.Path, err)
		}
		paths = append(paths, abs)
	}
	// Inherit existing env if the caller has not populated it yet. This matches
	// the stdlib convention where a nil Env means "use os.Environ()".
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env,
		"OMNIPUS_SANDBOX_MODE=fallback",
		"OMNIPUS_SANDBOX_PATHS="+strings.Join(paths, string(os.PathListSeparator)),
	)
	return nil
}

// CheckPath verifies that path is under one of the allowed paths (ignoring access flags).
func (f *FallbackBackend) CheckPath(path string) error {
	resolved, err := canonicalizePath(path)
	if err != nil {
		return fmt.Errorf("sandbox fallback: cannot resolve path %q: %w", path, err)
	}
	for _, entry := range f.entries {
		if pathIsUnder(resolved, entry.path) {
			return nil
		}
	}
	return fmt.Errorf("sandbox fallback: path %q is outside allowed paths", path)
}

// CheckPathAccess verifies that path is under an allowed path with the requested access flags.
func (f *FallbackBackend) CheckPathAccess(path string, access uint64) error {
	resolved, err := canonicalizePath(path)
	if err != nil {
		return fmt.Errorf("sandbox fallback: cannot resolve path %q: %w", path, err)
	}
	for _, entry := range f.entries {
		if pathIsUnder(resolved, entry.path) && (entry.access&access) == access {
			return nil
		}
	}
	return fmt.Errorf("sandbox fallback: path %q is outside allowed paths or lacks required access", path)
}

// canonicalizePath resolves a path to its absolute, symlink-free form.
// This is necessary on macOS where t.TempDir() returns /var/folders/... but
// /var is a symlink to /private/var. Without canonicalization, an allowlist
// entry stored as /var/folders/... would not match the resolved path
// /private/var/folders/... returned by EvalSymlinks on the input.
//
// When the path does not yet exist (e.g. a file being created), EvalSymlinks
// fails. In that case the nearest existing ancestor is resolved and the
// remaining path components are appended. This preserves correctness for
// pre-creation checks while still normalizing the ancestor portion.
func canonicalizePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	// Path does not fully exist yet (file being created, or deep missing tree).
	// Walk up the ancestor chain until we find an existing component, resolve
	// symlinks on that ancestor, then rejoin the remaining tail path segments.
	// This ensures that /var/folders/x/y/z correctly resolves to
	// /private/var/folders/x/y/z on macOS even when x/y/z don't exist yet.
	tail := []string{}
	current := abs
	for {
		parent := filepath.Dir(current)
		if parent == current {
			// Hit the filesystem root without finding any existing ancestor.
			return "", fmt.Errorf("canonicalizePath: no existing ancestor found for %q", abs)
		}
		tail = append([]string{filepath.Base(current)}, tail...)
		current = parent
		resolvedParent, parentErr := filepath.EvalSymlinks(current)
		if parentErr == nil {
			// Found the deepest existing ancestor; rejoin the tail.
			result := resolvedParent
			for _, seg := range tail {
				result = filepath.Join(result, seg)
			}
			return result, nil
		}
	}
}

// pathIsUnder checks if child is under or equal to parent directory.
func pathIsUnder(child, parent string) bool {
	parentDir := parent
	if !strings.HasSuffix(parentDir, string(filepath.Separator)) {
		parentDir += string(filepath.Separator)
	}
	return child == parent || strings.HasPrefix(child, parentDir)
}

// Status describes the active sandbox configuration for operator reporting.
//
// Important distinction between "capability" and "enforcement":
//   - Capable means the backend CAN provide kernel-level enforcement if
//     Apply() is called and seccomp is installed.
//   - Enforcing means Apply() has actually run successfully on the current
//     process and seccomp has been installed.
//
// The fields split the state so the UI cannot mistake capability for runtime
// enforcement. This matters: an earlier version of this type conflated the
// two and would report "Seccomp enabled" whenever Landlock was capable, even
// though neither had ever been installed on the Omnipus process.
type Status struct {
	Backend          string   `json:"backend"`
	Available        bool     `json:"available"`
	KernelLevel      bool     `json:"kernel_level"`
	ABIVersion       int      `json:"abi_version,omitempty"`
	BlockedSyscalls  []string `json:"blocked_syscalls,omitempty"`
	SeccompEnabled   bool     `json:"seccomp_enabled"`
	LandlockFeatures []string `json:"landlock_features,omitempty"`
	// PolicyApplied reports whether Apply() has successfully run on the
	// current process. When false, the Landlock/seccomp capability is
	// available but not actively enforcing — see the package comment for
	// the wiring status.
	PolicyApplied bool `json:"policy_applied"`
	// Mode is the effective sandbox mode ("enforce", "permissive", "off").
	// Empty string means the caller has not set the mode (e.g. describe was
	// called before Apply wiring was done).
	Mode Mode `json:"mode,omitempty"`
	// DisabledBy is populated when Mode == ModeOff to explain why the
	// sandbox was disabled: "cli_flag" (--sandbox=off), "config"
	// (gateway.sandbox.mode = "off"), or "kernel_unsupported" (no fallback
	// path, kernel too old). Empty when the sandbox is active.
	DisabledBy string `json:"disabled_by,omitempty"`
	// LandlockEnforced and SeccompEnforced are distinct from PolicyApplied
	// to make permissive-mode state readable: in permissive mode on a pre-
	// 6.12 kernel, policy is computed and logged but nothing is actually
	// enforced by the kernel. Clients that need to know "is this process
	// actually locked down" should check both.
	LandlockEnforced bool `json:"landlock_enforced,omitempty"`
	SeccompEnforced  bool `json:"seccomp_enforced,omitempty"`
	// AuditOnly is true when the sandbox is installed in audit/log-only mode
	// (permissive on a kernel without native permissive-Landlock support).
	AuditOnly bool `json:"audit_only,omitempty"`
	// Notes carries operator-facing explanations of the current state, such
	// as "sandbox not applied at startup" or "kernel older than 5.13". It
	// is empty when everything is healthy.
	Notes []string `json:"notes,omitempty"`
}

// abiReporter is implemented by backends that expose a Landlock ABI version
// (i.e. LinuxBackend). Declared at package scope so DescribeBackend and tests
// can share the interface.
type abiReporter interface {
	ABIVersion() int
}

// policyApplyReporter is implemented by backends that track whether their
// Apply() method has been called successfully. Used by DescribeBackend to
// distinguish capability from runtime enforcement.
type policyApplyReporter interface {
	PolicyApplied() bool
}

// ApplyState captures the gateway's Sprint-J boot-time decisions about the
// sandbox so the status endpoint can reflect them: which mode is effective,
// whether Apply/Install actually ran (not just whether they would have
// succeeded), whether the kernel degraded permissive to audit-only, and why
// the sandbox was disabled. Populated by the gateway's sandbox-apply step
// and read by DescribeBackendWithState.
type ApplyState struct {
	// Mode is the resolved sandbox mode for this process lifetime.
	Mode Mode
	// DisabledBy is set when Mode == ModeOff: "cli_flag", "config", or
	// "kernel_unsupported". Empty otherwise.
	DisabledBy string
	// LandlockEnforced is true when Apply() was invoked in enforce mode on
	// a kernel that actually enforces restrictions. False in permissive
	// mode (on current kernels that lack native permissive Landlock) and
	// when the sandbox is disabled.
	LandlockEnforced bool
	// SeccompEnforced is true when Install() was invoked with
	// SECCOMP_RET_ERRNO. False when mode=permissive (RET_LOG) or disabled.
	SeccompEnforced bool
	// AuditOnly captures the permissive-on-old-kernel degradation (policy
	// is computed and logged, but not enforced by the kernel).
	AuditOnly bool
	// ExtraNotes carries gateway-level notes that don't originate from the
	// backend itself (e.g. "permissive mode degraded to audit-only on
	// kernel 6.8").
	ExtraNotes []string
}

// DescribeBackend returns the operator-facing status of the given backend.
// It uses type assertions against narrow interfaces (abiReporter,
// policyApplyReporter) so this function stays build-tag free and forward-
// compatible with future kernel backends (Windows Job Objects, BSD pledge).
//
// The returned Status distinguishes capability from enforcement:
//   - KernelLevel=true means the backend CAN apply kernel policy.
//   - PolicyApplied=true means Apply() has actually run on this process.
//
// When the backend is capable but Apply has not been called, PolicyApplied
// is false and a note is added to Notes to surface the gap to operators.
//
// Callers who have also invoked Install() and know the effective Mode
// should prefer DescribeBackendWithState, which reflects mode and
// DisabledBy in the response.
func DescribeBackend(backend SandboxBackend) Status {
	return DescribeBackendWithState(backend, ApplyState{})
}

// DescribeBackendWithState is DescribeBackend extended with gateway-level
// state (mode, disabled_by, landlock_enforced, seccomp_enforced, audit_only,
// extra notes). The gateway owns this state because it orchestrates Apply()
// and Install() and knows whether the operator asked for enforce/permissive/off.
//
// When state.Mode is empty (zero value), the function degrades to the
// legacy DescribeBackend output for backward compatibility.
func DescribeBackendWithState(backend SandboxBackend, state ApplyState) Status {
	if backend == nil {
		return Status{Backend: "none", Available: false}
	}
	status := Status{
		Backend:    backend.Name(),
		Available:  backend.Available(),
		Mode:       state.Mode,
		DisabledBy: state.DisabledBy,
		AuditOnly:  state.AuditOnly,
	}

	rep, ok := backend.(abiReporter)
	if !ok {
		// Non-kernel backend (e.g. FallbackBackend). KernelLevel stays false.
		// Preserve any gateway-level notes (e.g. "kernel too old").
		if len(state.ExtraNotes) > 0 {
			status.Notes = append(status.Notes, state.ExtraNotes...)
		}
		return status
	}

	// Capable of kernel-level enforcement.
	status.KernelLevel = true
	status.ABIVersion = rep.ABIVersion()
	status.LandlockFeatures = DetectLandlockABI(status.ABIVersion).Features
	status.BlockedSyscalls = append([]string(nil), blockedSyscallNames...)

	// Distinguish capability from enforcement. A backend that tracks its
	// own applied state wins; otherwise conservatively assume not applied.
	applied, hasApplied := backend.(policyApplyReporter)
	if hasApplied && applied.PolicyApplied() {
		status.PolicyApplied = true
		switch state.Mode {
		case ModeEnforce:
			// Kernel-enforced on both axes.
			status.SeccompEnabled = true
			status.LandlockEnforced = true
			status.SeccompEnforced = true
		case ModePermissive:
			// Policy computed and logged, not enforced. Operators
			// care about the distinction when checking
			// /api/v1/security/sandbox-status.
			status.SeccompEnabled = true // seccomp filter is installed (RET_LOG)
			status.LandlockEnforced = state.LandlockEnforced
			status.SeccompEnforced = state.SeccompEnforced
		case "":
			// Legacy caller (DescribeBackend without state).
			// Preserve the pre-Sprint-J contract: when the backend
			// reports PolicyApplied, seccomp is reported enabled
			// because historically Apply and Install ran together.
			status.SeccompEnabled = true
			status.LandlockEnforced = true
			status.SeccompEnforced = true
		default:
			// Explicit mode other than enforce/permissive with
			// PolicyApplied=true is surprising but should not crash.
			status.SeccompEnabled = state.SeccompEnforced
			status.LandlockEnforced = state.LandlockEnforced
			status.SeccompEnforced = state.SeccompEnforced
		}
	} else {
		status.PolicyApplied = false
		status.SeccompEnabled = false
		// Phrase the note differently depending on WHY the kernel sandbox
		// isn't active. An operator who passed --sandbox=off is deliberately
		// disabling enforcement; surfacing the scary "Apply has not been
		// called" warning in that case implies the gateway is misconfigured
		// when the operator explicitly chose this state. Reserve the gap
		// warning for the unexpected-disabled case (DisabledBy empty), which
		// really does mean Apply failed or wasn't wired.
		switch state.DisabledBy {
		case "cli_flag":
			status.Notes = append(
				status.Notes,
				"Sandbox disabled via --sandbox CLI flag (operator choice). Pass --sandbox=enforce or set gateway.sandbox.mode to re-enable.",
			)
		case "config":
			status.Notes = append(status.Notes,
				"Sandbox disabled via gateway.sandbox.mode=off in config.json (operator choice).")
		default:
			// Unexpected: capable kernel, no DisabledBy marker, but Apply
			// didn't succeed. That's the original failure mode — keep the
			// loud warning.
			status.Notes = append(
				status.Notes,
				"sandbox backend is capable of kernel-level enforcement but Apply() has not been called on the Omnipus process; child processes are not currently restricted by Landlock or seccomp",
			)
		}
	}

	if len(state.ExtraNotes) > 0 {
		status.Notes = append(status.Notes, state.ExtraNotes...)
	}
	return status
}

// SelectBackend detects platform capabilities and returns the highest-capability
// sandbox backend available, along with its name.
func SelectBackend() (SandboxBackend, string) {
	return selectBackendPlatform()
}

// ProbeLandlockABI probes the actual kernel for Landlock ABI version.
// Returns 0 if unavailable. Platform-specific implementation in sandbox_linux.go.
func ProbeLandlockABI() int {
	return probeLandlockABIPlatform()
}
