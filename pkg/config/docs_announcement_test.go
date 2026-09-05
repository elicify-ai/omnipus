// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// These tests assert on the CONTENT of two operator-facing artefacts —
// CHANGELOG.md and docs/configuration.md — because for this change the words
// ARE the deliverable for a large part of it. An operator's entire model of
// what happened to their concurrency settings comes from the release note, and
// a wrong sentence there is a support burden that outlives the code.
//
// They live in pkg/config and reach the repository root through repoRoot (see
// no_per_agent_constant_test.go), since a Go test's working directory is its
// own package directory.

// TestDocs_NoComputedDefaultIsAnnounced.
//
// THE NEGATIVE HALF IS THE LOAD-BEARING ONE, and it is here because both the
// spec's own §0.6a and the ADR's D1.5c prescribed, IN WRITING, a release note
// saying "the macOS default moved 2 → 2000". That sentence is false in a
// specific and damaging way: it describes a capacity increase, so an operator
// reads it as "my Mac can now run two thousand agents" and plans accordingly.
// What actually happened is that the computed default was DELETED, and 2000 is
// a physical OS-thread backstop that no longer represents any claim about the
// machine. Writing the prescribed sentence is the single likeliest failure of
// this whole pass, which is why it is asserted against rather than trusted.
func TestDocs_NoComputedDefaultIsAnnounced(t *testing.T) {
	root := repoRoot(t)
	changelog := readRepoFile(t, filepath.Join(root, "CHANGELOG.md"))
	configDoc := readRepoFile(t, filepath.Join(root, "docs", "configuration.md"))

	// --- The positive: both artefacts must SAY it. ---
	for _, doc := range []struct{ name, body string }{
		{"CHANGELOG.md", changelog},
		{"docs/configuration.md", configDoc},
	} {
		if !strings.Contains(doc.body, "no longer a computed default") {
			t.Errorf("%s does not contain the phrase \"no longer a computed default\". An operator whose concurrency behaviour changed has to be able to find out that it did, and why.", doc.name)
		}
		if !strings.Contains(doc.body, "max_parallel_agents") {
			t.Errorf("%s does not name performance.max_parallel_agents, so the note cannot be found by anyone searching for the setting they changed", doc.name)
		}
	}

	// --- The negative: neither may say the default MOVED. ---
	forbidden := []struct{ phrase, why string }{
		{"2 → 2000", "the prescribed \"the macOS default moved 2 → 2000\" sentence. It describes a capacity increase; an operator reads it as \"my Mac can now run two thousand agents\". The default was DELETED, and 2000 is an OS-thread backstop that makes no claim about the machine."},
		{"2 -> 2000", "the ASCII spelling of the same sentence"},
		{"default moved", "any \"the default moved\" phrasing — nothing moved, the computed default no longer exists"},
		{"new default is 2000", "2000 is not a default, it is a backstop"},
		{"default of 2000", "2000 is not a default, it is a backstop"},
	}
	for _, doc := range []struct{ name, body string }{
		{"CHANGELOG.md", changelog},
		{"docs/configuration.md", configDoc},
	} {
		lower := strings.ToLower(doc.body)
		for _, f := range forbidden {
			if strings.Contains(lower, strings.ToLower(f.phrase)) {
				t.Errorf("%s contains %q — %s", doc.name, f.phrase, f.why)
			}
		}
	}

	// The SPA's wording must match the release note's, or an operator reading
	// one and looking at the other concludes they are about different things.
	if !strings.Contains(configDoc, "automatic — bounded by available memory") {
		t.Error("docs/configuration.md does not quote the exact phrase the Settings panel renders. An operator who reads the doc and then looks at the UI must recognise what they are seeing.")
	}
}

// TestDocs_WindowsAcceptedGVisorSupported.
//
// Both hosts take the SAME ok=false code path and hold at the SAME floor, so
// the temptation to describe them together in one line is strong and the
// resulting sentence would be technically accurate. It would also be wrong
// where it matters: they are different KINDS of thing, and an operator needs to
// know which they are looking at.
//
//   - A Linux host with an unreadable /proc/meminfo is a SUPPORTED deployment
//     that happens to be unmeasurable. It is a consequence of a choice the
//     operator made (gVisor, distroless, hardened seccomp) and can unmake.
//   - Windows is unmeasurable because NOBODY WROTE THE READER. It is a gap in
//     this codebase, not a deployment choice, and no amount of RAM will change
//     it.
//
// Grouping them as one platform class tells a Windows operator their situation
// is a configuration they can fix. It is not.
func TestDocs_WindowsAcceptedGVisorSupported(t *testing.T) {
	root := repoRoot(t)
	changelog := readRepoFile(t, filepath.Join(root, "CHANGELOG.md"))
	configDoc := readRepoFile(t, filepath.Join(root, "docs", "configuration.md"))

	for _, doc := range []struct{ name, body string }{
		{"CHANGELOG.md", changelog},
		{"docs/configuration.md", configDoc},
	} {
		lower := strings.ToLower(doc.body)

		// Windows: recorded as unsupported/degraded, not as a configuration.
		if !strings.Contains(lower, "windows") {
			t.Errorf("%s does not mention Windows at all. Its browser and concurrency behaviour is degraded there and an operator deploying on it is entitled to know before they find out.", doc.name)
			continue
		}
		if !strings.Contains(lower, "unsupported") {
			t.Errorf("%s does not record Windows as UNSUPPORTED. \"Degraded\" alone reads as a performance note; the point is that no reader exists and no configuration will produce one.", doc.name)
		}

		// gVisor / a /proc-less Linux host: recorded SEPARATELY, and as
		// supported.
		mentionsProcless := strings.Contains(lower, "gvisor") ||
			strings.Contains(lower, "/proc/meminfo") ||
			strings.Contains(lower, "procfs")
		if !mentionsProcless {
			t.Errorf("%s does not mention a /proc-less Linux host (gVisor, distroless, hardened seccomp) at all. It takes the same floor as Windows for a completely different reason, and an operator who sees the floor needs to know which of the two they are in.", doc.name)
		}
		if !strings.Contains(lower, "supported deployment") {
			t.Errorf("%s does not record the /proc-less Linux host as a SUPPORTED DEPLOYMENT. Left undistinguished it reads as the same class as Windows — which tells a Windows operator their gap is a configuration they can fix, and it is not.", doc.name)
		}

		// The two must be distinguishable in the text, not collapsed into one
		// "unsupported platforms" bucket. A single line naming both with one
		// verdict is the specific failure this test exists for.
		for _, collapsed := range []string{
			"windows and gvisor",
			"gvisor and windows",
			"windows, gvisor",
			"gvisor, windows",
		} {
			if strings.Contains(lower, collapsed) {
				t.Errorf("%s groups Windows and gVisor together (%q). They reach the same floor for different reasons and carry different verdicts; one sentence covering both cannot say that.", doc.name, collapsed)
			}
		}
	}
}

// TestDocs_UnmeasurableFloorRefusesToGrowNotToRun.
//
// The floor is the part of this change an operator is most likely to
// misunderstand, and the misunderstanding is expensive in both directions:
// reading it as "Omnipus does not work on Windows" is wrong, and reading it as
// "nothing changed" is also wrong. The docs must state the distinction, in
// words, not leave it to be inferred from a number.
func TestDocs_UnmeasurableFloorRefusesToGrowNotToRun(t *testing.T) {
	root := repoRoot(t)
	configDoc := readRepoFile(t, filepath.Join(root, "docs", "configuration.md"))

	// Normalise markdown emphasis and whitespace so the assertion is on the
	// SENTENCE, not on how it happens to be marked up.
	normalised := strings.Join(strings.Fields(strings.ReplaceAll(configDoc, "*", "")), " ")

	// The exact contrast, as one sentence. A weaker check (does the word
	// "grow" appear anywhere) passes against a doc that says "the system stops
	// there" and mentions growth in an unrelated paragraph — verified by
	// mutation: that is exactly what happened on the first attempt at this
	// test, and it went green.
	if !strings.Contains(normalised, "refuses to grow, never to run") {
		t.Errorf("docs/configuration.md does not contain the sentence \"refuses to grow, never to run\".\n\n" +
			"This is the part of the change an operator is most likely to get wrong, and it is expensive in BOTH directions: " +
			"reading the floor as \"Omnipus does not work on this host\" is wrong, and reading it as \"nothing changed\" is also wrong. " +
			"The contrast has to be stated in one sentence, not inferred from a number.")
	}

	if !strings.Contains(strings.ToLower(configDoc), "floor of **two**") &&
		!strings.Contains(strings.ToLower(configDoc), "floor of two") {
		t.Error("docs/configuration.md does not say what the floor actually IS. An operator seeing their third concurrent turn refused needs to recognise the behaviour as the documented floor rather than as a bug.")
	}

	if !strings.Contains(configDoc, "explicitly") {
		t.Error("docs/configuration.md does not tell an operator on an unmeasurable host that setting performance.max_parallel_agents explicitly is the way out. The floor is not the end of the story and the doc should not leave it as one.")
	}
}

// TestDocs_ContainerBlindSpotIsDeclared.
//
// SC-030's A29 residual. The containerisation predicate deliberately does not
// treat a bare "0::/" cgroup path as a container, which means one real
// deployment shape — a cgroup-v2 pod in its own namespace, service links
// disabled, no /.dockerenv — gets no warning even though the condition applies
// to it. A blind spot nobody wrote down is one an operator discovers by being
// OOM-killed.
func TestDocs_ContainerBlindSpotIsDeclared(t *testing.T) {
	root := repoRoot(t)
	configDoc := readRepoFile(t, filepath.Join(root, "docs", "configuration.md"))
	changelog := readRepoFile(t, filepath.Join(root, "CHANGELOG.md"))

	for _, doc := range []struct{ name, body string }{
		{"docs/configuration.md", configDoc},
		{"CHANGELOG.md", changelog},
	} {
		if !strings.Contains(doc.body, "OMNIPUS_CONTAINERIZED") {
			t.Errorf("%s does not name OMNIPUS_CONTAINERIZED. It is the ONLY coverage for a cgroup-v2 pod in its own namespace, so it is load-bearing rather than a convenience, and an operator cannot use an override they have never heard of.", doc.name)
		}
	}

	if !strings.Contains(strings.ToLower(configDoc), "cgroup-v2 pod in its own") {
		t.Error("docs/configuration.md does not describe the specific shape the container detection misses. \"Detection is best-effort\" is not actionable; naming the shape is.")
	}
}
