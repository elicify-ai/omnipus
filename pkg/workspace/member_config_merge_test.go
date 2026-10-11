package workspace

import (
	"reflect"
	"testing"
)

// S2-08 (architect ruling): the hidden_from_sidebar tri-state merge lives here,
// in one function, so a second writer cannot silently un-hide members.
// ORACLE: ADR-hide-from-sidebar addendum 2026-10-11 ("hiding is only hiding"),
// quoted on MemberConfig: an incoming entry REPLACES the stored one except
// (1) flag absent => stored flag preserved, (2) pure hide/show (flag present,
// heartbeat absent) => stored heartbeat preserved.

func hb(enabled bool) *MemberHeartbeat {
	return &MemberHeartbeat{Enabled: enabled, IntervalMinutes: 10, Body: "b"}
}

func TestMergeMemberConfig(t *testing.T) {
	stored := MemberConfig{Heartbeat: hb(true), HiddenFromSidebar: true}
	cases := []struct {
		name         string
		in           IncomingMemberConfig
		storedExists bool
		want         MemberConfig
	}{
		{"heartbeat-only save never un-hides (flag absent)",
			IncomingMemberConfig{Config: MemberConfig{Heartbeat: hb(false)}, HeartbeatPresent: true},
			true, MemberConfig{Heartbeat: hb(false), HiddenFromSidebar: true}},
		{"explicit false un-hides and keeps the stored heartbeat (pure show)",
			IncomingMemberConfig{Config: MemberConfig{HiddenFromSidebar: false}, HiddenPresent: true},
			true, MemberConfig{Heartbeat: hb(true), HiddenFromSidebar: false}},
		{"pure hide keeps the stored heartbeat",
			IncomingMemberConfig{Config: MemberConfig{HiddenFromSidebar: true}, HiddenPresent: true},
			true, MemberConfig{Heartbeat: hb(true), HiddenFromSidebar: true}},
		{"flag and heartbeat both present: plain replace",
			IncomingMemberConfig{Config: MemberConfig{Heartbeat: hb(false), HiddenFromSidebar: false}, HiddenPresent: true, HeartbeatPresent: true},
			true, MemberConfig{Heartbeat: hb(false), HiddenFromSidebar: false}},
		{"empty entry clears the heartbeat but keeps the flag",
			IncomingMemberConfig{},
			true, MemberConfig{HiddenFromSidebar: true}},
		{"no stored entry: incoming taken as is (pure hide has nothing to preserve)",
			IncomingMemberConfig{Config: MemberConfig{HiddenFromSidebar: true}, HiddenPresent: true},
			false, MemberConfig{HiddenFromSidebar: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeMemberConfig(stored, c.storedExists, c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v (hb %+v), want %+v (hb %+v)", got, got.Heartbeat, c.want, c.want.Heartbeat)
			}
		})
	}
}

func TestIncomingMemberConfig_PureHideShow(t *testing.T) {
	if !(IncomingMemberConfig{HiddenPresent: true}).PureHideShow() {
		t.Fatal("flag present, heartbeat absent is a pure hide/show")
	}
	if (IncomingMemberConfig{HiddenPresent: true, HeartbeatPresent: true}).PureHideShow() {
		t.Fatal("a heartbeat makes it an ordinary save")
	}
	if (IncomingMemberConfig{}).PureHideShow() {
		t.Fatal("an empty entry is not a pure hide/show")
	}
}
