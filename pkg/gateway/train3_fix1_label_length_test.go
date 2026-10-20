package gateway

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// Gate round 1, architect F3 / silent-failure SF-06: the origin label's
// limit is the contract's maxLength (200 CHARACTERS, contracts
// ToolApprovalRequiredFrame.origin_label), not 200 UTF-8 bytes. A valid room
// name of up to 80 characters (pkg/rooms.maxRoomNameUnits) must keep its
// "for group <name>" label even when it is far longer than 200 bytes.
func TestTrain3Fix1_OriginLabelLimitCountsCharactersNotBytes(t *testing.T) {
	name := strings.Repeat("界", 70) // a valid 70-character room name
	label := "for group " + name
	require.Equal(t, 80, utf8.RuneCountInString(label), "fixture: 80 characters")
	require.Greater(t, len(label), approvalOriginLabelMaxLen, "fixture: more than 200 bytes")

	got := approvalOriginLabel(&approvalEntry{OriginLabel: label})
	require.NotNil(t, got, "a contract-valid label must not be dropped for its byte length")
	require.Equal(t, label, *got)

	// Control: a label genuinely over 200 characters is still omitted.
	over := "for group " + strings.Repeat("a", 191)
	require.Equal(t, 201, utf8.RuneCountInString(over))
	require.Nil(t, approvalOriginLabel(&approvalEntry{OriginLabel: over}))
}
