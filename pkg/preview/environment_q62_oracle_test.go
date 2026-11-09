package preview

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/childenv"
	"github.com/stretchr/testify/require"
)

// Expected values come from Q62 A / AD-7 / FR-009 / MV-2, not child output.
func q62EnvironmentMismatch(actual, expected []string, windows bool) error {
	got, err := q62EnvironmentMap(actual, windows)
	if err != nil {
		return err
	}
	want, err := q62EnvironmentMap(expected, windows)
	if err != nil {
		return fmt.Errorf("invalid independent oracle: %w", err)
	}
	for key, value := range got {
		expectedValue, exists := want[key]
		if !exists {
			return fmt.Errorf("unexpected key %q", key)
		}
		if value != expectedValue {
			return fmt.Errorf("value mismatch for %q: got %q, want %q", key, value, expectedValue)
		}
	}
	for key := range want {
		if _, exists := got[key]; !exists {
			return fmt.Errorf("missing key %q", key)
		}
	}
	return nil
}

func q62EnvironmentMap(entries []string, windows bool) (map[string]string, error) {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, valid := strings.Cut(entry, "=")
		if !valid || key == "" {
			return nil, fmt.Errorf("malformed entry %q", entry)
		}
		if windows {
			key = strings.ToUpper(key)
		}
		if _, duplicate := out[key]; duplicate {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		out[key] = value
	}
	return out, nil
}

func q62AssertEnvironment(t *testing.T, actual, expected []string) {
	t.Helper()
	require.NoError(t, q62EnvironmentMismatch(actual, expected, runtime.GOOS == "windows"))
}

func TestPreviewQ62_ExactEnvironmentOracle(t *testing.T) {
	cases := []struct {
		name             string
		actual, expected []string
		windows          bool
		want             string
	}{
		{"empty", []string{}, []string{}, false, ""},
		{"exact_values", []string{"HOME=/scratch/home", "LANG=en_US.UTF-8"}, []string{"LANG=en_US.UTF-8", "HOME=/scratch/home"}, false, ""},
		{"case_insensitive_systemroot", []string{"sYsTeMrOoT=C:\\Windows"}, []string{"SYSTEMROOT=C:\\Windows"}, true, ""},
		{"undeclared_key", []string{"EXTRA=x"}, []string{}, false, "unexpected key"},
		{"raw_parent_key", []string{"OPENAI_API_KEY=synthetic"}, []string{}, false, "unexpected key"},
		{"generic_kept_key", []string{"USER=synthetic"}, []string{}, false, "unexpected key"},
		{"duplicate_override", []string{"PATH=/usr/bin:/bin", "PATH=unsafe"}, []string{"PATH=/usr/bin:/bin"}, false, "duplicate key"},
		{"windows_duplicate_override", []string{"SystemRoot=C:\\Windows", "SYSTEMROOT=other"}, []string{"SYSTEMROOT=C:\\Windows"}, true, "duplicate key"},
		{"wrong_scratch_provenance", []string{"HOME=/parent"}, []string{"HOME=/scratch/home"}, false, "value mismatch"},
		{"missing_key", []string{}, []string{"TMPDIR=/scratch/tmp"}, false, "missing key"},
		{"malformed_entry", []string{"MISSING_EQUALS"}, []string{}, false, "malformed entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := q62EnvironmentMismatch(tc.actual, tc.expected, tc.windows)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}
}

func q62PlantParentSentinels(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{
		"OPENAI_API_KEY": "q62-synthetic-credential", "USER": "q62-parent-user",
		"HTTP_PROXY": "http://127.0.0.1:9/q62", "LC_Q62_SENTINEL": "q62-locale",
		"XDG_Q62_SENTINEL": "q62-config", "OMNIPUS_CHILD_Q62_SENTINEL": "q62-pass-through",
	} {
		t.Setenv(key, value)
	}
	generic := childenv.Filter()
	for _, entry := range []string{"USER=q62-parent-user", "HTTP_PROXY=http://127.0.0.1:9/q62", "LC_Q62_SENTINEL=q62-locale", "XDG_Q62_SENTINEL=q62-config", "OMNIPUS_CHILD_Q62_SENTINEL=q62-pass-through"} {
		require.Contains(t, generic, entry, "instrument: a generic scrubber would retain this planted value")
	}
}

func q62ConverterExpected(t *testing.T, c *call, registry string) []string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(c.dir)
	require.NoError(t, err)
	home, tmp := filepath.Join(dir, "home"), filepath.Join(dir, "tmp")
	expected := []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "HOME=" + home, "TMPDIR=" + tmp}
	if runtime.GOOS == "darwin" {
		expected = append(expected, "OSL_SOCKET_PATH="+c.socketDir)
		if registry != "" {
			// Registry path is a file URL; the fixture's space must be percent-encoded.
			location := url.URL{Scheme: "file", Path: filepath.ToSlash(registry)}
			expected = append(expected, "URE_MORE_SERVICES=<"+location.String()+">*")
		}
	}
	if runtime.GOOS == "windows" {
		expected = append(expected, "TEMP="+tmp, "TMP="+tmp, "USERPROFILE="+home, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	return expected
}
