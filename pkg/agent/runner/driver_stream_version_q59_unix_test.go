//go:build darwin || linux

package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Q59: a successful subprocess is not evidence of a version. Exercise both
// detection and pinning with real programs, not a stubbed detector or parser.
func TestDetectCLIVersion_UnversionedOutputNeverPinned_Q59(t *testing.T) {
	for _, tc := range []struct {
		name        string
		output      string
		wantVersion string
		wantKnown   bool
	}{
		{
			name:        "versioned_control",
			output:      "claude 1.2.3",
			wantVersion: "1.2.3",
			wantKnown:   true,
		},
		{
			name:        "unversioned_output",
			output:      "unversioned diagnostic sentinel",
			wantVersion: "",
			wantKnown:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := filepath.Join(t.TempDir(), "version-cli")
			marker := program + ".invocations"
			body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> \"$0.invocations\"\nprintf '%%s\\n' %q\nexit 0\n", tc.output)
			if err := os.WriteFile(program, []byte(body), 0o700); err != nil {
				t.Fatalf("write real exit-zero version fixture: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			version, err := detectCLIVersion(ctx, program)
			if err != nil {
				t.Fatalf("detectCLIVersion on real exit-zero program: %v", err)
			}
			if version != tc.wantVersion {
				t.Errorf("Q59: detectCLIVersion returned %q, want %q for output %q", version, tc.wantVersion, tc.output)
			}
			invocations, err := os.ReadFile(marker)
			if err != nil || string(invocations) != "--version\n" {
				t.Fatalf("instrument: direct detector must run the real child with --version exactly once; got %q, err=%v", invocations, err)
			}

			// The control's 1.2 prefix is known; output without any version must
			// remain ("", false), never carry its first line into the pin result.
			pinnedVersion, known := detectAndPinVersion(ctx, program, "test/q59", []string{"1.2"})
			if pinnedVersion != tc.wantVersion || known != tc.wantKnown {
				t.Errorf("Q59: detectAndPinVersion returned (%q, %t), want (%q, %t) for output %q", pinnedVersion, known, tc.wantVersion, tc.wantKnown, tc.output)
			}
			invocations, err = os.ReadFile(marker)
			if err != nil || string(invocations) != "--version\n--version\n" {
				t.Fatalf("instrument: pinning must perform a second real --version probe; got %q, err=%v", invocations, err)
			}
		})
	}
}
