// Omnipus — regression coverage for the 2026-09-13 UAT finding D-99: two
// wikilinks a reader sees as the identical string "Dashboards/café.png"
// resolved differently — the NFC spelling mounted the file, the NFD spelling
// (what macOS writes to disk) said no file in the collection matched.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// "café" with a precomposed é (U+00E9) — what a keyboard or a generator
	// usually produces.
	cafeNFC = "café"
	// "café" as "e" + combining acute (U+0301) — what macOS/APFS stores.
	cafeNFD = "café"
)

func d99Link(target string) Link { return Link{Kind: LinkWikilink, Target: target} }

// TestUAT_D99_NFDLinkResolvesToNFCFile — U-50's exact case: the file on disk
// is NFC, the link is NFD. Both spellings must resolve, and the resolved
// target must be the file's OWN bytes, never the link's.
func TestUAT_D99_NFDLinkResolvesToNFCFile(t *testing.T) {
	onDisk := "Dashboards/" + cafeNFC + ".png"
	ni := NewNoteIndex([]string{onDisk, "Dashboards/Unicode.md"})

	for _, target := range []string{
		"Dashboards/" + cafeNFD + ".png", // full path, NFD
		cafeNFD + ".png",                 // bare basename, NFD
		cafeNFD,                          // bare stem, NFD
		"Dashboards/" + cafeNFC + ".png", // the control: NFC still works
	} {
		res := ni.Resolve("Dashboards/Unicode.md", d99Link(target))
		require.Equalf(t, ResolveResolved, res.State, "D-99: %q must resolve (reason %q)", target, res.Reason)
		require.Equalf(t, onDisk, res.To, "D-99: the resolved path must be the on-disk bytes for %q", target)
		require.Falsef(t, res.Ambiguous, "one file, no ambiguity for %q", target)
	}
	require.True(t, ni.Has("Dashboards/"+cafeNFD+".png"), "Has must compare in NFC too")
}

// TestUAT_D99_NFCLinkResolvesToNFDFile — the mirror image: a file dragged in
// from Finder (NFD on disk) reached by a link typed in NFC.
func TestUAT_D99_NFCLinkResolvesToNFDFile(t *testing.T) {
	onDisk := "Assets/" + cafeNFD + " résumé.md"
	ni := NewNoteIndex([]string{onDisk, "Home.md"})

	res := ni.Resolve("Home.md", d99Link(cafeNFC+" résumé"))
	require.Equal(t, ResolveResolved, res.State, "reason %q", res.Reason)
	require.Equal(t, onDisk, res.To, "the on-disk (NFD) bytes are the target, so the file can be opened")

	res = ni.Resolve("Home.md", d99Link("Assets/"+cafeNFC+" résumé.md"))
	require.Equal(t, ResolveResolved, res.State, "reason %q", res.Reason)
	require.Equal(t, onDisk, res.To)
}

// TestUAT_D99_NormalisationTwinsAreBothKeptAndReportedAmbiguous — on Linux
// two files can differ ONLY in normalisation. Neither may be dropped: an
// exact-bytes link gets its own file, and a bare link is ambiguous between
// the two (FR-041) rather than silently picking one.
func TestUAT_D99_NormalisationTwinsAreBothKeptAndReportedAmbiguous(t *testing.T) {
	nfc := "Dashboards/" + cafeNFC + ".png"
	nfd := "Dashboards/" + cafeNFD + ".png"
	ni := NewNoteIndex([]string{nfd, nfc})

	require.Len(t, ni.Paths(), 2, "both twins are indexed")

	exactNFC := ni.Resolve("Home.md", d99Link(nfc))
	require.Equal(t, nfc, exactNFC.To, "the byte-identical twin wins the exact-path stage")
	exactNFD := ni.Resolve("Home.md", d99Link(nfd))
	require.Equal(t, nfd, exactNFD.To, "the byte-identical twin wins the exact-path stage")

	bare := ni.Resolve("Home.md", d99Link(cafeNFC+".png"))
	require.Equal(t, ResolveResolved, bare.State)
	require.True(t, bare.Ambiguous, "a bare link between normalisation twins is ambiguous, not a silent pick")
	require.ElementsMatch(t, []string{nfc, nfd}, bare.Candidates)
}
