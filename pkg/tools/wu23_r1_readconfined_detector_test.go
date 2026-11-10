package tools

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// Called by the legacy detectors themselves, so CHECK's M14/M16 anchored
// commands exercise these independent non-inventory refusal rows too.
func wu23R1ReadConfinedDetector(t *testing.T, ctx context.Context, policy fspolicy.FSPolicy, tr readConfinedTree, confined bool) {
	t.Helper()
	for _, row := range []struct {
		name string
		op   FSOp
		path string
	}{
		{"external_read", FSOpRead, tr.outsideFile},
		{"external_list", FSOpList, filepath.Dir(tr.outsideFile)},
		{"external_send", FSOpSend, tr.outsideFile},
	} {
		t.Run(row.name, func(t *testing.T) {
			handle, err := ResolvePath(ctx, policy, "wu23-r1", "posture-detector", row.op, row.path)
			if confined {
				if handle != nil {
					handle.Close()
				}
				if !errors.Is(err, ErrOutsideScope) {
					t.Fatalf("context-derived ReadConfined must refuse NON-inventory %s with ErrOutsideScope: %v", row.op, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unconfined %s must still reach non-inventory external control: %v", row.op, err)
			}
			defer handle.Close()
			if row.op == FSOpList {
				entries, readErr := handle.ReadDir()
				if readErr != nil || len(entries) != 1 || entries[0].Name() != "unrelated.txt" {
					t.Fatalf("external directory exact control: entries=%v err=%v", entries, readErr)
				}
			} else {
				data, readErr := handle.ReadFile()
				if readErr != nil || string(data) != "unrelated content" {
					t.Fatalf("external %s exact bytes=%q err=%v", row.op, data, readErr)
				}
			}
		})
	}
	t.Run("own_read_send_exact_controls", func(t *testing.T) {
		for _, op := range []FSOp{FSOpRead, FSOpSend} {
			handle, err := ResolvePath(ctx, policy, "wu23-r1", "own-control", op, tr.insideFile)
			if err != nil {
				t.Fatalf("own %s must remain permitted: %v", op, err)
			}
			data, readErr := handle.ReadFile()
			handle.Close()
			if readErr != nil || string(data) != "the work under review" {
				t.Fatalf("own %s exact bytes=%q err=%v", op, data, readErr)
			}
		}
	})
	t.Run("explicit_mount_read_list_controls", func(t *testing.T) {
		mounted := policy
		mounted.AllowedRoots = []string{filepath.Dir(tr.outsideFile)}
		for _, row := range []struct {
			op   FSOp
			path string
		}{{FSOpRead, tr.outsideFile}, {FSOpList, filepath.Dir(tr.outsideFile)}} {
			handle, err := ResolvePath(ctx, mounted, "wu23-r1", "mount-control", row.op, row.path)
			if err != nil {
				t.Fatalf("read-confined explicit mount %s must remain permitted: %v", row.op, err)
			}
			if row.op == FSOpRead {
				data, readErr := handle.ReadFile()
				if readErr != nil || string(data) != "unrelated content" {
					handle.Close()
					t.Fatalf("mount exact bytes=%q err=%v", data, readErr)
				}
			} else {
				entries, readErr := handle.ReadDir()
				if readErr != nil || len(entries) != 1 || entries[0].Name() != "unrelated.txt" {
					handle.Close()
					t.Fatalf("mount directory exact entries=%v err=%v", entries, readErr)
				}
			}
			handle.Close()
		}
	})
}
