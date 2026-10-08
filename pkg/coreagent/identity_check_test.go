package coreagent

import "testing"

func TestNearestPaletteFixtures(t *testing.T) {
	cases := []struct{ in, want string }{
		{"#D4AF37", "#FB923C"},
		{"#7B1FA2", "#E879F9"},
		{"#AD1457", "#F472B6"},
		{"#A0A079", "#9CA3AF"},
		{"#A0A078", "#FB923C"},
		{"#A0A077", "#FB923C"},
		{"#000000", "#9CA3AF"},
		{"#FFFFFF", "#9CA3AF"},
		{"#E2E8F0", "#9CA3AF"},
		{"#3B82F6", "#3B82F6"},
		{"#3b82f6", "#3B82F6"},
		{"nope", "#9CA3AF"},
		{"#22D3EE", "#22D3EE"},
		{"#38BDF8", "#38BDF8"},
		{"#818CF8", "#818CF8"},
		{"#A78BFA", "#A78BFA"},
		{"#C084FC", "#C084FC"},
		{"#E879F9", "#E879F9"},
		{"#F472B6", "#F472B6"},
		{"#FB923C", "#FB923C"},
		{"#9CA3AF", "#9CA3AF"},
		{"#64748B", "#3B82F6"},
		{"#22C55E", "#22D3EE"},
		{"#D4AF37", "#FB923C"},
		{"#F97316", "#FB923C"},
		{"#0EA5E9", "#38BDF8"},
		{"#8B5CF6", "#A78BFA"},
		{"#6B7280", "#9CA3AF"},
		{"#0F766E", "#22D3EE"},
	}
	for _, c := range cases {
		if got := NearestPalette(c.in); got != c.want {
			t.Errorf("NearestPalette(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestRoleFromLegacyIcon(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Code", "developer"},
		{"code", "developer"},
		{"Chat", "general"},
		{"MagnifyingGlass", "researcher"},
		{"magnifyingglass", "researcher"},
		{"magnifying-glass", "general"},
		{"PencilSimple", "writer"},
		{"Shield", "security"},
		{"Robot", "general"},
		{"developer", "developer"},
		{"", "general"},
		{"graph", "general"},
	}
	for _, c := range cases {
		if got := RoleFromLegacyIcon(c.in); got != c.want {
			t.Errorf("RoleFromLegacyIcon(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
