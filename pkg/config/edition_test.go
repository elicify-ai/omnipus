package config

import "testing"

func withEdition(t *testing.T, e string) {
	t.Helper()
	prev := Edition
	Edition = e
	t.Cleanup(func() { Edition = prev })
}

func TestEditionAuthMode_DerivesFromTheStamp(t *testing.T) {
	cases := map[string]AuthMode{
		EditionCore:    AuthModeLocal,
		EditionDesktop: AuthModePlatform,
		EditionHosted:  AuthModePlatform,
		"":             "",
		"enterprise":   "",
	}
	for e, want := range cases {
		withEdition(t, e)
		if got := EditionAuthMode(); got != want {
			t.Fatalf("edition %q: auth mode %q, want %q", e, got, want)
		}
	}
}

func TestEditionMisbuild_RefusesTheTwoDangerousShapes(t *testing.T) {
	withEdition(t, "enterprise")
	if EditionMisbuild("") == "" {
		t.Fatal("an unknown edition must be a misbuild")
	}
	withEdition(t, EditionCore)
	if EditionMisbuild("https://platform.example") == "" {
		t.Fatal("a core build with a platform trust anchor must be a misbuild")
	}
	if got := EditionMisbuild(""); got != "" {
		t.Fatalf("a plain core build must boot, got %q", got)
	}
	withEdition(t, EditionHosted)
	if got := EditionMisbuild("https://platform.example"); got != "" {
		t.Fatalf("a hosted build with its trust anchor must boot, got %q", got)
	}
}

func TestEdition_DefaultIsCore(t *testing.T) {
	if Edition != EditionCore {
		t.Fatalf("source default must be core so an unstamped open-source build behaves as upstream, got %q", Edition)
	}
}
