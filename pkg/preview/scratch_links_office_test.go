//go:build darwin || linux

package preview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-90 / D3.7: only the external converter is substituted. The actual
// launcher, retry decision, profile reseed, PDF reader and call.finish run.
// Outside means a separate test-owned temporary tree, NEVER a user folder.
type scratchLinkPlan struct {
	Case   string `json:"case"`
	Target string `json:"target"`
}

func scratchLinkOfficeProcess() int {
	raw, err := os.ReadFile("scratch-link-plan.json")
	if err != nil {
		return 70
	}
	var plan scratchLinkPlan
	if json.Unmarshal(raw, &plan) != nil {
		return 71
	}
	starts, err := os.ReadFile("scratch-starts")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 72
	}
	if os.WriteFile("scratch-starts", append(starts, 'x'), 0600) != nil {
		return 73
	}
	if len(starts) != 0 {
		if os.WriteFile(filepath.Join("out", "document.pdf"), officeRetryPDF(), 0600) != nil {
			return 74
		}
		return 0
	}
	var replaced string
	switch plan.Case {
	case "profile":
		replaced = "profile"
	case "settings":
		replaced = filepath.Join("profile", "user", "registrymodifications.xcu")
	case "out", "outside_valid_pdf":
		replaced = "out"
	case "output_file":
		replaced = filepath.Join("out", "document.pdf")
	case "finish_link":
		replaced = "cleanup-link"
	case "no_links":
		return 81
	default:
		return 75
	}
	if err := os.RemoveAll(replaced); err != nil {
		return 76
	}
	if err := os.Symlink(plan.Target, replaced); err != nil {
		return 77
	}
	if plan.Case == "outside_valid_pdf" {
		return 0
	}
	if plan.Case == "finish_link" {
		if os.WriteFile(filepath.Join("out", "document.pdf"), officeRetryPDF(), 0600) != nil {
			return 78
		}
		return 0
	}
	return 81
}

// Preserve complete recursive listings AND byte content, not only the named
// sentinel. This catches deletion/recreation elsewhere in the outside tree.
func scratchOutsideSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			state[rel+"/"] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		state[rel] = string(data)
		return nil
	})
	require.NoError(t, err)
	return state
}

func scratchSentinel(t *testing.T, root, name string, bytes []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, bytes, 0600))
	return path
}

func scratchPlan(t *testing.T, c *call, plan scratchLinkPlan) {
	t.Helper()
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(c.dir, "scratch-link-plan.json"), raw, 0600))
}

func TestOfficePreviewScratchLinksNeverEscape(t *testing.T) {
	cases := []struct{ name, mode, failure string }{
		{"a_profile_ancestor", "profile", "conversion failed: the LibreOffice profile settings could not be restored"},
		{"b_settings_file", "settings", "conversion failed: the LibreOffice profile settings could not be restored"},
		{"c_output_ancestor", "out", "conversion failed: scratch unavailable"},
		{"d_output_file", "output_file", "conversion failed: scratch unavailable"},
		{"e_outside_valid_pdf", "outside_valid_pdf", "conversion failed: LibreOffice produced no readable PDF"},
		{"f_end_of_call_link", "finish_link", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outside := t.TempDir()
			conv, c := fakeOfficeCall(t, t.TempDir(), "scratch_links")
			t.Cleanup(c.finish)
			var target string
			switch tc.mode {
			case "profile":
				scratchSentinel(t, outside, "E1/user/registrymodifications.xcu", []byte("S1: harmless original profile\n"))
				target = filepath.Join(outside, "E1")
			case "settings":
				target = scratchSentinel(t, outside, "S2.xcu", []byte("S2: harmless original file\n"))
			case "out", "outside_valid_pdf":
				data := []byte("S3: deliberately not a PDF\n")
				if tc.mode == "outside_valid_pdf" {
					data = officeRetryPDF()
				}
				scratchSentinel(t, outside, "E2/document.pdf", data)
				target = filepath.Join(outside, "E2")
			case "output_file":
				target = scratchSentinel(t, outside, "S4.pdf", []byte("S4: deliberately not a PDF\n"))
			case "finish_link":
				scratchSentinel(t, outside, "E5/S5", []byte("S5: cleanup must not follow the link\n"))
				target = filepath.Join(outside, "E5")
			}
			before := scratchOutsideSnapshot(t, outside)
			scratchPlan(t, c, scratchLinkPlan{Case: tc.mode, Target: target})
			pdf, _, err := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
			starts, readErr := os.ReadFile(filepath.Join(c.dir, "scratch-starts"))
			require.NoError(t, readErr, "instrument: the real launched converter wrote its start counter")
			// Assert preservation BEFORE other failures can stop this test.
			assert.Equal(t, before, scratchOutsideSnapshot(t, outside), "T-90: outside sentinels and every folder listing must be byte-identical after retry handling")
			assert.Equal(t, "x", string(starts), "T-90: outside link ends the call; exactly one target start")
			if tc.failure == "" {
				assert.NoError(t, err)
				assert.Equal(t, officeRetryPDF(), pdf)
			} else {
				assert.EqualError(t, err, tc.failure, "D3.7 exact visible failure")
				assert.Nil(t, pdf, "outside PDF bytes must never leave conversion for model media or thumbnail production")
			}
			c.finish()
			assert.NoDirExists(t, c.dir, "AD-8: end-of-call cleanup removes the scratch folder itself")
			assert.Equal(t, before, scratchOutsideSnapshot(t, outside), "T-90: cleanup must unlink links, never traverse their outside targets")
		})
	}
}

// D3.7 permits links wholly inside the original scratch root. These controls
// must perform the ordinary exit-81 retry and return the independent PDF.
func TestOfficePreviewScratchInsideLinksKeepConversion(t *testing.T) {
	for _, mode := range []string{"no_links", "profile", "settings", "out", "output_file"} {
		t.Run(mode, func(t *testing.T) {
			conv, c := fakeOfficeCall(t, t.TempDir(), "scratch_links")
			defer c.finish()
			target := filepath.Join(c.dir, "internal", mode)
			switch mode {
			case "profile":
				require.NoError(t, os.MkdirAll(filepath.Join(target, "user"), 0700))
			case "out":
				require.NoError(t, os.MkdirAll(target, 0700))
			case "settings", "output_file":
				scratchSentinel(t, c.dir, filepath.Join("internal", mode), []byte("inside original content"))
			}
			scratchPlan(t, c, scratchLinkPlan{Case: mode, Target: target})
			pdf, _, err := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
			require.NoError(t, err, "D3.7: links staying inside scratch act like ordinary paths")
			require.Equal(t, officeRetryPDF(), pdf)
			starts, err := os.ReadFile(filepath.Join(c.dir, "scratch-starts"))
			require.NoError(t, err)
			require.Equal(t, "xx", string(starts), "D3: exactly one retry, not unbounded restart")
		})
	}
}
