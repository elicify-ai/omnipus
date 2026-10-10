//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// MAJ-CW-003 breadcrumb plan: persist real Skip via TruncateHistory, confirm
// that metadata and the live window, then independently vary the history passed
// into assembleMessages. Its length must never be the archive-address oracle.
// The evicted prefix starts with assistant/tool records, with no user boundary.
// Exact boundary addresses come from fixture identities, not generated text.
func cwRangeBreadcrumbs(t *testing.T) {
	for _, skip := range []int{0, 2, 4} {
		for _, variant := range []string{"actual_window", "shortened_assembled_history", "extra_assembled_messages"} {
			t.Run(fmt.Sprintf("skip_%d_%s", skip, variant), func(t *testing.T) {
				const key = "cw-breadcrumb"
				lines := cwRangeBreadcrumbLines(2, "archived literal result ")
				store, path := cwRangeStore(t, key, lines...)
				if err := store.TruncateHistory(context.Background(), key, len(lines)-skip); err != nil {
					t.Fatalf("persist actual Skip: %v", err)
				}
				cwAssertPersistedSkip(t, path, skip, len(lines))
				backend := store
				live := backend.GetHistory(key)
				if len(live) != len(lines)-skip {
					t.Fatalf("fixture actual live count: want %d got %d", len(lines)-skip, len(live))
				}
				history := append([]providers.Message(nil), live...)
				switch variant {
				case "shortened_assembled_history":
					history = history[len(history)-2:]
				case "extra_assembled_messages":
					history = append(history,
						providers.Message{Role: "user", Content: "CURRENT transient control"},
						providers.Message{Role: "assistant", Content: "CURRENT transient answer"},
					)
				}
				crumb := cwAssembledBreadcrumb(t, backend, key, history)
				if skip == 0 {
					if crumb != "" {
						t.Fatalf("Skip=0 must not fabricate an evicted prefix from shortened history: %q", crumb)
					}
					return
				}
				cwAssertBreadcrumbRange(t, crumb, 0, skip-1)
				cwAssertBreadcrumbPointer(t, crumb, "bc-call-0", 1)
				if !strings.Contains(crumb, "archived literal result 0") {
					t.Errorf("breadcrumb must preserve useful literal result snippet, not invent a recap: %q", crumb)
				}
				if skip == 2 && strings.Contains(crumb, "archived literal result 1") {
					t.Errorf("breadcrumb claimed a result still inside the actual live window: %q", crumb)
				}
				if utf8.RuneCountInString(crumb) > breadcrumbTokenCap*breadcrumbCharsPerToken {
					t.Errorf("breadcrumb exceeds existing cap: runes=%d cap=%d", utf8.RuneCountInString(crumb), breadcrumbTokenCap*breadcrumbCharsPerToken)
				}
			})
		}
	}
	t.Run("range_address_survives_large_prefix_cap", func(t *testing.T) {
		const key, pairs = "cw-breadcrumb-cap", 64
		// Many results and useful literal prefixes; not a recap request. Even
		// when entries/snippets are capped, the entire evicted address stays.
		lines := cwRangeBreadcrumbLines(pairs, "literal-prefix-"+strings.Repeat("q", 256))
		store, path := cwRangeStore(t, key, lines...)
		if err := store.TruncateHistory(context.Background(), key, 2); err != nil {
			t.Fatal(err)
		}
		cwAssertPersistedSkip(t, path, 2*pairs, len(lines))
		backend := store
		crumb := cwAssembledBreadcrumb(t, backend, key, backend.GetHistory(key))
		cwAssertBreadcrumbRange(t, crumb, 0, 2*pairs-1)
		if n := utf8.RuneCountInString(crumb); n > breadcrumbTokenCap*breadcrumbCharsPerToken {
			t.Fatalf("large-prefix breadcrumb exceeds existing cap: %d", n)
		}
	})
}

func cwRangeBreadcrumbLines(pairs int, snippet string) []string {
	lines := make([]string, 0, 2*pairs+2)
	for i := 0; i < pairs; i++ {
		id := fmt.Sprintf("bc-call-%d", i)
		lines = append(lines,
			fmt.Sprintf("{\"role\":\"assistant\",\"tool_calls\":[{\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{}\"}}]}\n", id),
			fmt.Sprintf("{\"role\":\"tool\",\"tool_call_id\":%q,\"content\":%q}\n", id, fmt.Sprintf("%s%d", snippet, i)),
		)
	}
	return append(lines,
		"{\"role\":\"user\",\"content\":\"CURRENT live control\"}\n",
		"{\"role\":\"assistant\",\"content\":\"CURRENT live answer\"}\n",
	)
}

func cwAssertPersistedSkip(t *testing.T, archivePath string, skip, count int) {
	t.Helper()
	path := filepath.Join(filepath.Dir(archivePath), strings.TrimSuffix(filepath.Base(archivePath), ".jsonl")+".meta.json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Internal disk metadata is not a gateway wire format. Read only the two
	// source identities needed for this fixture, independently of assembly.
	var meta struct {
		Skip  int `json:"skip"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(encoded, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Skip != skip || meta.Count != count {
		t.Fatalf("fixture actual metadata: want Skip=%d Count=%d got %+v", skip, count, meta)
	}
}

func cwAssembledBreadcrumb(t *testing.T, backend *session.JSONLBackend, key string, history []providers.Message) string {
	t.Helper()
	al := &AgentLoop{cfg: &config.Config{Context: config.DefaultContextSettings()}}
	agent := &AgentInstance{ID: "cw-breadcrumb-agent", Sessions: backend, ContextBuilder: NewContextBuilder(t.TempDir())}
	ts := &turnState{agent: agent, sessionKey: key}
	messages := al.assembleMessages(context.Background(), ts, history, "CURRENT next user message", nil, nil)
	if len(messages) == 0 || messages[0].Role != "system" {
		t.Fatalf("real assembly must have pinned system message: %+v", messages)
	}
	// With no active skills/recall span this real builder has two baseline
	// parts (static + dynamic) and an optional distinct breadcrumb part.
	parts := messages[0].SystemParts
	if len(parts) == 2 {
		return ""
	}
	if len(parts) != 3 {
		t.Fatalf("instrument cannot identify distinct breadcrumb block: parts=%d", len(parts))
	}
	return parts[2].Text
}

func cwAssertBreadcrumbRange(t *testing.T, text string, from, to int) {
	t.Helper()
	if text == "" {
		t.Fatal("MAJ-CW-003: nonempty evicted prefix without a user boundary must have a discoverable breadcrumb")
	}
	// Accept a directly usable archive_range object, or a human-readable
	// archive-line range. Exact prose was deliberately not prescribed.
	normalized := strings.ToLower(strings.ReplaceAll(text, "\"", ""))
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`archive_range\s*[:=]?\s*\{[^}\n]*\bfrom\s*:\s*([0-9]+)\s*,\s*to\s*:\s*([0-9]+)`),
		regexp.MustCompile(`archive(?:_range|[ _-]+lines?(?:[ _-]+range)?|[ _-]+range)[^0-9\n]{0,100}([0-9]+)\s*(?:–|-|…|\.\.)\s*([0-9]+)`),
	}
	for _, re := range patterns {
		for _, match := range re.FindAllStringSubmatch(normalized, -1) {
			gotFrom, _ := strconv.Atoi(match[1])
			gotTo, _ := strconv.Atoi(match[2])
			if gotFrom == from && gotTo == to {
				return
			}
		}
	}
	t.Fatalf("breadcrumb must address actual zero-based inclusive evicted lines %d..%d (not archive/window length subtraction): %q", from, to, text)
}

func cwAssertBreadcrumbPointer(t *testing.T, text, callID string, line int) {
	t.Helper()
	// A usable addressed-result pointer needs BOTH identity fields associated
	// with this exact record; mere occurrence of an id anywhere is not enough.
	normalized := strings.ReplaceAll(text, "\"", "")
	id := `tool_call_id\s*[:=]\s*` + regexp.QuoteMeta(callID) + `\b`
	address := `archive_line\s*[:=]\s*` + strconv.Itoa(line) + `\b`
	forward := regexp.MustCompile(id + `[^\n]{0,100}` + address)
	reverse := regexp.MustCompile(address + `[^\n]{0,100}` + id)
	if !forward.MatchString(normalized) && !reverse.MatchString(normalized) {
		t.Fatalf("breadcrumb must include exact addressed pointer (tool_call_id=%s, archive_line=%d): %q", callID, line, text)
	}
}
