package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type historyR5ReadCut struct { // not-wire-format: private scanner-input fixture.
	name, doc string
	cut       int
}

func historyR5ReadCuts() []historyR5ReadCut {
	var cuts []historyR5ReadCut
	for _, n := range []string{"1", "-12", "12.5", "6e+2"} {
		doc := `{"a":` + n + "}\n"
		cuts = append(cuts, historyR5ReadCut{"number_" + n, doc, len(`{"a":`) + len(n)})
	}
	for _, r := range []string{"é", "€", "🙂"} {
		prefix := `{"a":"pre`
		doc := prefix + r + `post"}` + "\n"
		for split := 1; split < len(r); split++ {
			cuts = append(cuts, historyR5ReadCut{fmt.Sprintf("utf8_%d_bytes_split_%d", len(r), split), doc, len(prefix) + split})
		}
	}
	const pair = "\x5cuD83D\x5cuDE42" // One scalar, twelve encoded ASCII bytes; hex writes the two backslashes.
	const prefix = `{"a":"pre`
	for split := 1; split < len(pair); split++ {
		cuts = append(cuts, historyR5ReadCut{fmt.Sprintf("surrogate_pair_split_%d", split), prefix + pair + `post"}` + "\n", len(prefix) + split})
	}
	return cuts
}

// Return each fragment once. Unlike a reader that advances on a short copy,
// this clean control preserves unread bytes for every caller buffer size.
func historyR5CleanFragments(tc historyR5ReadCut) io.Reader {
	return io.MultiReader(strings.NewReader(tc.doc[:tc.cut]), strings.NewReader(tc.doc[tc.cut:]))
}

// SEC-R5-2 / A2.8: clean fragmentation is not a storage failure. All internal
// rune/surrogate splits must still equal full encoding/json decoding exactly.
func TestHistoryStreamScanner_R5CleanFragmentsEqualFullDecode(t *testing.T) {
	for _, tc := range historyR5ReadCuts() {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, json.Valid([]byte(tc.doc)), "independent control: original document is valid JSON")
			var want any
			require.NoError(t, json.Unmarshal([]byte(tc.doc), &want))
			reduced, err := historyStreamRecord(historyR5CleanFragments(tc), newHistoryStandIns())
			require.NoError(t, err, "SEC-R5-2 clean fragmentation must remain readable")
			var got any
			require.NoError(t, json.Unmarshal(reduced, &got))
			require.Equal(t, want, got, "SEC-R5-2 fragmentation must not replace or split a valid Unicode scalar")
		})
	}
}

// Verify the existing historyFlakyReader instrument independently: the error
// really occurs ONCE and the unchanged original bytes remain recoverable with
// their exact SHA-256. This is NOT an executed historyStreamArchive/digest gate.
func historyR5CheckRecoverableReader(t *testing.T, tc historyR5ReadCut, fault error) {
	t.Helper()
	parts := []string{tc.doc[:tc.cut], tc.doc[tc.cut:]}
	var buf [256]byte
	for _, part := range parts {
		require.LessOrEqual(t, len(part), len(buf), "existing flaky reader must not discard a short-buffer remainder")
	}
	r := &historyFlakyReader{parts: parts, err: fault}
	var recovered bytes.Buffer
	failures := 0
	for reads := 0; ; reads++ {
		require.Less(t, reads, 5, "two fragments, one injected error, then EOF; instrument must terminate")
		n, err := r.Read(buf[:])
		recovered.Write(buf[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			require.ErrorIs(t, err, fault, "instrument emitted exactly the commissioned non-EOF error")
			failures++
		}
	}
	require.Equal(t, 1, failures, "instrument positive control: one transient non-EOF error")
	require.Equal(t, tc.doc, recovered.String(), "original canonical candidate bytes all recover after the error")
	require.Equal(t, len(tc.doc), recovered.Len(), "original encoded byte count is unchanged")
	require.Equal(t, sha256.Sum256([]byte(tc.doc)), sha256.Sum256(recovered.Bytes()), "a raw-byte digest alone cannot detect a swallowed read error")
}

// SEC-R5-2 / A2.8: a non-EOF storage error after a number, inside any
// multibyte-rune split, or inside a surrogate escape MUST fail closed. In
// particular, consuming a bufio error during number/Peek lookahead must not
// let later recovered bytes masquerade as a successful display record.
func TestHistoryStreamScanner_R5TransientReadErrorsFailClosed(t *testing.T) {
	for _, tc := range historyR5ReadCuts() {
		t.Run(tc.name, func(t *testing.T) {
			fault := errors.New("synthetic archive read failure")
			historyR5CheckRecoverableReader(t, tc, fault)
			r := &historyFlakyReader{parts: []string{tc.doc[:tc.cut], tc.doc[tc.cut:]}, err: fault}
			reduced, err := historyStreamRecord(r, newHistoryStandIns())
			require.ErrorIs(t, err, errHistoryStreamUnreadable, "SEC-R5-2 transient read error must not produce a successful or altered display document: %q", reduced)
			require.Equal(t, "history display stream: unreadable record", err.Error(), "content-free display failure contract")
			require.Nil(t, reduced, "unreadable scanner input must not publish partial recovered JSON")
		})
	}
}
