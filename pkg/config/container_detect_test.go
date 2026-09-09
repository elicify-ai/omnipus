// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// withContainerFixtures points every containerisation seam at a controlled
// fixture and clears the environment, so a test's answer depends only on what
// that test set up — not on whether the machine running it happens to be in a
// container. (It very often is: this project's own CI runs in one.)
func withContainerFixtures(t *testing.T) (cgroupFile string, dockerenvFile string) {
	t.Helper()
	dir := t.TempDir()
	cgroupFile = filepath.Join(dir, "cgroup")
	dockerenvFile = filepath.Join(dir, "dockerenv-absent")

	oldCgroup, oldDockerenv := procSelfCgroupPath, containerDockerenvPath
	procSelfCgroupPath = cgroupFile
	containerDockerenvPath = dockerenvFile
	t.Cleanup(func() {
		procSelfCgroupPath = oldCgroup
		containerDockerenvPath = oldDockerenv
	})

	t.Setenv(containerOverrideEnv, "")
	t.Setenv(kubernetesServiceHostEnv, "")
	return cgroupFile, dockerenvFile
}

// TestContainerDetect_PodDockerAndBareMetal is the three-fixture table.
//
// The bare-metal case is the load-bearing one. A predicate that answered true
// unconditionally would satisfy both container cases, and the warning it gates
// would then fire on every developer laptop — at which point everyone learns to
// ignore it, and it stops working for the deployment it exists for.
func TestContainerDetect_PodDockerAndBareMetal(t *testing.T) {
	cases := []struct {
		name          string
		k8sHost       string
		cgroup        string
		makeDockerenv bool
		want          bool
	}{
		{
			// A Kubernetes pod, with NO /.dockerenv — most clusters do not run
			// Docker any more. A predicate built on the Docker marker alone
			// misses every containerd/CRI-O cluster in existence.
			name:    "kubernetes pod (no /.dockerenv)",
			k8sHost: "10.96.0.1",
			cgroup:  "0::/kubepods/besteffort/pod1234abcd/9f8e7d6c\n",
			want:    true,
		},
		{
			// A plain Docker container, with no Kubernetes environment at all.
			name:          "docker container (marker file only)",
			makeDockerenv: true,
			cgroup:        "0::/\n",
			want:          true,
		},
		{
			// Bare metal under systemd. This is what a developer laptop and an
			// ordinary VM look like.
			name:   "bare metal (systemd slice)",
			cgroup: "0::/user.slice/user-1000.slice/session-2.scope\n",
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cgroupFile, dockerenvFile := withContainerFixtures(t)
			if err := os.WriteFile(cgroupFile, []byte(tc.cgroup), 0o600); err != nil {
				t.Fatalf("write cgroup fixture: %v", err)
			}
			if tc.makeDockerenv {
				if err := os.WriteFile(dockerenvFile, nil, 0o600); err != nil {
					t.Fatalf("write dockerenv fixture: %v", err)
				}
			}
			if tc.k8sHost != "" {
				t.Setenv(kubernetesServiceHostEnv, tc.k8sHost)
			}

			if got := RunningInContainer(); got != tc.want {
				t.Fatalf("RunningInContainer() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestContainerDetect_BareCgroupV2RootIsNotContainerisation pins the deliberate
// negative.
//
// "0::/" is what a cgroup-v2 process sees at the root of its own namespace —
// which is true inside a pod AND true for an ordinary process on a systemd
// host. Treating it as containerisation would make the predicate fire
// everywhere. The cost of that choice is a real coverage gap (a cgroup-v2 pod
// in its own namespace with no Kubernetes env and no /.dockerenv is invisible),
// which is exactly why the override env var exists and is documented as
// load-bearing rather than cosmetic.
func TestContainerDetect_BareCgroupV2RootIsNotContainerisation(t *testing.T) {
	cgroupFile, _ := withContainerFixtures(t)
	if err := os.WriteFile(cgroupFile, []byte("0::/\n"), 0o600); err != nil {
		t.Fatalf("write cgroup fixture: %v", err)
	}

	if RunningInContainer() {
		t.Fatal("RunningInContainer() = true for a bare \"0::/\" cgroup path. That is the cgroup-v2 namespace root and it is equally what a plain process on a systemd host sees — treating it as containerisation makes the node-memory warning fire on every developer laptop and every bare-metal server, and a warning that fires everywhere is one everyone learns to ignore.")
	}

	// And the override is the documented coverage for precisely this case.
	t.Setenv(containerOverrideEnv, "1")
	if !RunningInContainer() {
		t.Fatal("OMNIPUS_CONTAINERIZED=1 did not force a true answer — it is the ONLY coverage for a cgroup-v2 pod in its own namespace with no Kubernetes env and no /.dockerenv, so it is load-bearing rather than a convenience")
	}
}

// TestContainerDetect_DoesNotConsultTheLimit is the independence assertion, and
// it is the reason this predicate exists as its own function rather than being
// folded into the memory reader.
//
// If containerisation were inferred from the presence of a memory limit, then
// "containerised AND no limit" — the exact condition FR-077 warns about — would
// be undetectable BY CONSTRUCTION: no limit would mean not containerised, and
// the one case needing the warning would be the one case that never produced
// it.
//
// So the cgroup-limit reader is replaced with one that PANICS. Not a stub that
// returns something implausible — a panic, because a stub's return value can be
// quietly absorbed by a predicate that reads it and then ignores it, and that
// predicate is still coupled to the limit even though the test passes.
func TestContainerDetect_DoesNotConsultTheLimit(t *testing.T) {
	cgroupFile, _ := withContainerFixtures(t)
	if err := os.WriteFile(cgroupFile, []byte("0::/kubepods/pod-abc\n"), 0o600); err != nil {
		t.Fatalf("write cgroup fixture: %v", err)
	}

	restore := SetCgroupBudgetProviderForTest(func() (uint64, uint64, bool) {
		panic("RunningInContainer read the cgroup memory limit. Containerisation must be detected INDEPENDENTLY of the limit (FR-076), or \"containerised and unlimited\" — the exact condition the node-memory warning exists for — becomes undetectable by construction.")
	})
	defer restore()

	if !RunningInContainer() {
		t.Fatal("RunningInContainer() = false for a cgroup path naming kubepods")
	}
}

// TestContainerDetect_UsesItsOwnPredicateNotIsRunningInDocker records the
// EXPLICIT rejection of pkg/gateway's isRunningInDocker as the implementation
// here.
//
// It looks like the same question and it is not. isRunningInDocker answers "is
// this Docker specifically" for a different purpose; a Kubernetes pod on
// containerd is not Docker. Reusing it would silently exclude the single
// deployment shape this warning exists for — a pod on a big node with no memory
// limit — and the exclusion would be invisible, because the predicate would
// still look correct and still answer true in local Docker testing.
func TestContainerDetect_UsesItsOwnPredicateNotIsRunningInDocker(t *testing.T) {
	cgroupFile, _ := withContainerFixtures(t)
	// A containerd-backed Kubernetes pod: no /.dockerenv anywhere, and the
	// Kubernetes env deliberately unset (a pod with service links disabled) so
	// the ONLY signal left is the cgroup path.
	if err := os.WriteFile(cgroupFile, []byte("0::/kubepods/burstable/pod9f8e/abcd1234\n"), 0o600); err != nil {
		t.Fatalf("write cgroup fixture: %v", err)
	}

	if !RunningInContainer() {
		t.Fatal("a containerd-backed Kubernetes pod with no /.dockerenv and no KUBERNETES_SERVICE_HOST was not detected as containerised. A Docker-only predicate answers false here — and this is precisely the deployment the node-memory warning exists for.")
	}
}
