package validation_test

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/validation"
)

// TestEntityID_BehaviorMatchesOldValidator pins the observable behavior of
// gateway.validateEntityID as it existed before FR-062 moved the function.
// Any change to this table is a contract change and must be reviewed.
func TestEntityID_BehaviorMatchesOldValidator(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
		errMsg  string
	}{
		{name: "empty rejected", in: "", wantErr: true, errMsg: "id must not be empty"},
		{name: "plain rejected", in: "abc123", wantErr: false},
		{name: "ulid shape allowed", in: "01HX9Y8ZABCDEFGHJKMNPQRSTV", wantErr: false},
		{name: "slash rejected", in: "a/b", wantErr: true, errMsg: "invalid id"},
		{name: "backslash rejected", in: "a\\b", wantErr: true, errMsg: "invalid id"},
		{name: "dotdot rejected", in: "..", wantErr: true, errMsg: "invalid id"},
		{name: "dotdot prefix rejected", in: "../etc", wantErr: true, errMsg: "invalid id"},
		{name: "embedded dotdot rejected", in: "foo..bar", wantErr: true, errMsg: "invalid id"},
		{name: "null byte rejected", in: "a\x00b", wantErr: true, errMsg: "invalid id"},
		{name: "unicode allowed", in: "αβγ", wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validation.EntityID(tc.in)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("EntityID(%q): got err=%v, want err=%v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatalf("EntityID(%q): error %q does not contain %q", tc.in, err.Error(), tc.errMsg)
			}
		})
	}
}
