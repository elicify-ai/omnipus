package agent

import (
	"strconv"
	"strings"
	"testing"
)

// A named path may be literal or Go-%q quoted in the model-facing reason.
// Both preserve the exact supplied path; neither allows a generic reason.
// The production field-error formatter uses %q, which doubles Windows slashes.
func l11274ReasonNamesPath(message, path string) bool {
	return strings.Contains(message, path) || strings.Contains(message, strconv.Quote(path))
}

func TestL11274PathReasonPreservesExactRawOrQuotedPath(t *testing.T) {
	path := `C:\test install\codex.txt`
	for _, tc := range []struct {
		name, message string
		want          bool
	}{
		{"literal", `cli_path C:\test install\codex.txt is not executable`, true},
		{"quoted", `cli_path "C:\\test install\\codex.txt" is not executable`, true},
		{"generic", "cli_path is not executable", false},
		{"wrong-path", `cli_path "C:\\test install\\opencode.txt" is not executable`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := l11274ReasonNamesPath(tc.message, path); got != tc.want {
				t.Errorf("exact-path reason check = %v, want %v for %q", got, tc.want, tc.message)
			}
		})
	}
}
