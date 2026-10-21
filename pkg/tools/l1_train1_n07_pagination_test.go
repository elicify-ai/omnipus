package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrain1N07AgentHomePaginationCountsOnlyVisibleEntries(t *testing.T) {
	// N07 / CHECK F03 / docs/security.md "What reading, searching and listing
	// can reach": hidden entries do not count toward listing pages. These
	// five ordinary fixture names give total=5, limit=2 -> next=2,4,end.
	// Expected numbers and order come from the fixture, not policy arrays or
	// an unfiltered listing. A post-pagination filter must fail these rows.
	framing := regexp.MustCompile(`^\[listing: entries ([0-9]+)-([0-9]+) of ([0-9]+), offset=([0-9]+), next_offset=([0-9]+|end)\]\n$`)
	for _, restrict := range []bool{true, false} {
		t.Run(fmt.Sprintf("restrict_%t", restrict), func(t *testing.T) {
			registry, ctx, agentHome := train1N07ListingFixture(t, restrict)
			for _, name := range []string{"alpha.txt", "report.txt", "zulu.txt"} {
				require.NoError(t, os.WriteFile(filepath.Join(agentHome, name), []byte("ordinary paging fixture\n"), 0o600))
			}
			var concatenated []string
			for _, row := range []struct {
				name     string
				offset   int
				names    []string
				pageInfo []string // start, end, visible total, current offset, next offset
			}{
				{"first_page", 0, []string{"alpha.txt", "notes.txt"}, []string{"0", "2", "5", "0", "2"}},
				{"nonzero_offset", 2, []string{"projects", "report.txt"}, []string{"2", "4", "5", "2", "4"}},
				{"short_terminal_page", 4, []string{"zulu.txt"}, []string{"4", "5", "5", "4", "end"}},
			} {
				t.Run(row.name, func(t *testing.T) {
					result := registry.Execute(ctx, "list_directory", map[string]any{"path": ".", "limit": 2, "offset": row.offset})
					require.False(t, result.IsError, "N07 listing must succeed with protected and ordinary entries mixed: %s", result.ForLLM)
					body, info, found := strings.Cut(result.ForLLM, "[listing:")
					require.True(t, found, "partial page must expose visible-only total and next offset: %q", result.ForLLM)
					parts := framing.FindStringSubmatch("[listing:" + info)
					require.Len(t, parts, 6, "page framing must state start/end, total, offset and next_offset: %q", info)
					assert.Equal(t, row.pageInfo, parts[1:], "N07: hidden entries consume neither totals nor offsets")
					names := train1N07ListedNames(t, body)
					assert.Equal(t, row.names, names, "limit and offset apply AFTER hiding protected paths")
					concatenated = append(concatenated, names...)
					for _, name := range []string{".omnipus", "memory", "sessions", "skills", "SOUL.md", "AGENT.md", "HEARTBEAT.md", "MEMORY.md"} {
						assert.NotContains(t, result.ForLLM, name, "protected name leaked in model-facing page")
						assert.NotContains(t, result.ForUser, name, "protected name leaked in user-facing page")
					}
				})
			}
			assert.Equal(t, []string{"alpha.txt", "notes.txt", "projects", "report.txt", "zulu.txt"}, concatenated,
				"paging must preserve every ordinary name exactly once, in order, without gaps from hidden entries")
		})
	}
}
