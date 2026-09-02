// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"bufio"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// Containerisation is detected INDEPENDENTLY of the memory limit (FR-076), and
// that independence is the entire point.
//
// The condition worth warning an operator about is "you are in a container and
// you did NOT set a memory limit". If containerisation were inferred from the
// presence of a limit, that condition would be undetectable by construction:
// no limit would mean not containerised, and the one case that needs the
// warning would be the one case that never produces it. So this predicate must
// never consult memory.max, memory.limit_in_bytes or /proc/meminfo — and
// TestContainerDetect_DoesNotConsultTheLimit makes those readers panic if it
// tries.
//
// isRunningInDocker (pkg/gateway/sandbox_apply.go) is EXPLICITLY REJECTED as
// the implementation here even though it looks like the same question. It
// answers a narrower one — "is this Docker specifically" — for a different
// purpose, and a Kubernetes pod on containerd is not Docker. Reusing it would
// silently exclude the deployment this warning exists for.

// Detection seams. Package-level vars, following the same pattern as
// procMeminfoPath and cgroupRoot, purely so tests can point them at fixtures.
// Production code never mutates them.
var (
	// containerOverrideEnv lets an operator declare containerisation when
	// nothing else can detect it.
	//
	// LOAD-BEARING, not cosmetic. A cgroup-v2 pod in its own cgroup namespace
	// sees /proc/self/cgroup as the bare "0::/" — the namespace root, with the
	// kubepods path invisible from inside. If KUBERNETES_SERVICE_HOST is also
	// unset (a pod with automountServiceAccountToken disabled, or a bare
	// containerd/nerdctl container) and /.dockerenv is absent (anything not
	// Docker), then NOTHING here detects it, and this env var is the only
	// coverage that exists. That residual is documented rather than hidden.
	containerOverrideEnv = "OMNIPUS_CONTAINERIZED"

	// kubernetesServiceHostEnv is injected into every pod by the kubelet
	// unless service links are explicitly disabled.
	kubernetesServiceHostEnv = "KUBERNETES_SERVICE_HOST"

	// dockerenvPath is the marker file the Docker daemon creates at the root
	// of every container it starts.
	containerDockerenvPath = "/.dockerenv"

	// procSelfCgroupPath is this process's cgroup membership. On cgroup v1 and
	// on v2 WITHOUT a cgroup namespace, a container's path names its runtime.
	procSelfCgroupPath = "/proc/self/cgroup"
)

// containerCgroupPathMarkers are the substrings in /proc/self/cgroup that name
// a container runtime.
//
// Note what is NOT here: a bare "0::/" is deliberately NOT containerisation.
// That is what a cgroup-v2 process sees at the root of its own namespace, and
// it is equally what a plain process on a systemd bare-metal host can see. A
// predicate that treated it as a container would fire on every developer
// laptop and every bare-metal server, and a warning that fires everywhere is a
// warning everyone learns to ignore — which costs more than the case it would
// have caught.
var containerCgroupPathMarkers = []string{
	"kubepods", // Kubernetes, all runtimes
	"docker",   // Docker / Moby
	"lxc",      // LXC / LXD
	"containerd",
}

// RunningInContainer reports whether this process is running inside a
// container, using only signals that are INDEPENDENT of any memory limit.
//
// Any one signal is sufficient; they are checked cheapest-first. A false
// answer means "none of these four signals fired", which is not the same as
// "definitely bare metal" — see containerOverrideEnv for the residual.
func RunningInContainer() bool {
	// 1. The operator's explicit declaration wins, and is the only coverage
	//    for a cgroup-v2 pod in its own namespace.
	if v := strings.TrimSpace(os.Getenv(containerOverrideEnv)); v != "" {
		return v != "0" && !strings.EqualFold(v, "false")
	}
	// 2. Kubernetes injects this into every pod by default.
	if os.Getenv(kubernetesServiceHostEnv) != "" {
		return true
	}
	// 3. Docker's own marker file.
	if _, err := os.Stat(containerDockerenvPath); err == nil {
		return true
	}
	// 4. A container runtime named in this process's cgroup path.
	return cgroupPathNamesAContainer(procSelfCgroupPath)
}

// cgroupPathNamesAContainer reports whether the cgroup membership file at path
// contains a path naming a container runtime.
func cgroupPathNamesAContainer(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.ToLower(scanner.Text())
		for _, marker := range containerCgroupPathMarkers {
			if strings.Contains(line, marker) {
				return true
			}
		}
	}
	return false
}

// nodeMemoryWarnLogged makes the FR-077 warning fire at most once per process.
var nodeMemoryWarnLogged atomic.Bool

// nodeMemoryWarnOnce guards the whole CHECK, not just the log line.
//
// The check costs a Getenv, a Stat and a file read. WarnOnMemoryMechanismFirstUse
// is called from the live memory accessor, which runs on every admission
// decision, so without this the predicate's filesystem work would land on a hot
// path forever on every bare-metal host — where it can never warn, because the
// answer is a permanent no.
var nodeMemoryWarnOnce sync.Once

// WarnIfContainerHasNoMemoryLimit emits ONE startup WARN when this process is
// containerised (FR-076) AND no cgroup memory limit is configured, and is
// silent in the other three cases.
//
// WHY THIS IS WORTH A WARNING. Inside an unlimited container the memory
// mechanism has nothing container-shaped to read, so it falls back to the
// host-wide figures — which are the NODE's, not this container's. On a
// 256 GiB Kubernetes node running thirty pods, this process sees 256 GiB of
// headroom and sizes itself against a machine it does not have to itself. It
// will happily admit browsers and agent turns until the kernel OOM-kills the
// pod, and from inside the pod that looks like an unexplained restart with
// nothing in the log.
//
// IT IS NEVER A REFUSAL. Startup succeeds in all four cases. An operator who
// has deliberately given a container the whole node is not doing anything
// wrong, and refusing to boot over a sizing heuristic would be far worse than
// the problem. The remedy named (resources.limits.memory) is one that actually
// exists and actually fixes it — the cgroup reader picks the limit up
// immediately, with no configuration in this process at all.
func WarnIfContainerHasNoMemoryLimit() {
	if !RunningInContainer() {
		return
	}
	if _, _, ok := cgroupBudgetProvider(); ok {
		// A limit IS configured: the memory mechanism is reading this
		// container's own budget. Nothing to say.
		return
	}
	if nodeMemoryWarnLogged.Swap(true) {
		return
	}
	// slog, not pkg/logger, deliberately. pkg/logger writes to zerolog through
	// package globals with no capture seam, so a warning emitted through it
	// cannot be asserted on — and the three SILENT cases are most of this
	// requirement. An operator-facing warning nobody can test that it does NOT
	// fire is how a warning ends up firing everywhere and being ignored. It
	// also matches the sibling memory warning in pkg/agent's admission path, so
	// both halves of the one mechanism log the same way.
	slog.Warn("running in a container with no container memory limit set — sizing against node memory. Available-memory readings will reflect the whole host, not this container's share, so concurrency may be admitted well past what this container can actually use and the kernel may OOM-kill it. Set a memory limit on the container (resources.limits.memory in Kubernetes, --memory in Docker) and it will be picked up automatically with no configuration change here.",
		"remedy", "resources.limits.memory")
}

// WarnOnMemoryMechanismFirstUse runs the FR-077 check once, the first time the
// live memory mechanism is consulted.
//
// It is hooked here rather than at gateway boot on purpose. The condition is a
// property of the memory mechanism's INPUTS, so tying it to the mechanism's
// first use means it cannot be missed by a code path that reads memory without
// going through boot (a CLI subcommand, a test harness, a future embedder), and
// it needs no wiring in the gateway's startup sequence at all. The cost is that
// it fires on first READ rather than at boot, which for a process that reads
// memory on every admission decision is the same instant in practice.
func WarnOnMemoryMechanismFirstUse() {
	nodeMemoryWarnOnce.Do(WarnIfContainerHasNoMemoryLimit)
}

// resetNodeMemoryWarnOnceForTest re-arms the sync.Once. Tests only.
func resetNodeMemoryWarnOnceForTest() {
	nodeMemoryWarnOnce = sync.Once{}
}

// resetNodeMemoryWarnForTest clears the once-latch. Tests only.
func resetNodeMemoryWarnForTest() {
	nodeMemoryWarnLogged.Store(false)
}
