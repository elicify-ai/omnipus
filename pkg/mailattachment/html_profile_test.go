package mailattachment_test

// RED pack (w4 spec §9.1 row 4; §5.4; founder Q5=A; grill-2 F-8; §9.3 M6):
// the saved mail-derived HTML profile and the marker's provenance.
//
// Two of these oracles are RED against the landed code and are the
// reproductions of findings reported to team-lead:
//   - F2a: the marker is never re-keyed when the file moves (no gateway
//     Library operation calls MarkerStore.Move/Copy/Delete), so a renamed
//     mail-derived HTML file loses its scripts-off posture.
//   - (F2, the serve half, is reproduced at the gateway level.)

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/mailattachment"
)

// newMarkerWS builds a workspace + a marker index OUTSIDE the work tree (a
// marker must never appear as a Library entry, §5.4/§2.3).
func newMarkerWS(t *testing.T) (wsRoot, *mailattachment.MarkerStore, string) {
	t.Helper()
	w := newWSRoot(t)
	markerPath := filepath.Join(t.TempDir(), "data", "mail-derived.json")
	store := mailattachment.NewMarkerStore(markerPath)
	return w, store, markerPath
}

func saveHTML(t *testing.T, w wsRoot, store *mailattachment.MarkerStore, token, filename, content string) *mailattachment.SaveReceipt {
	t.Helper()
	reader := &fakePartReader{part: textPart(filename, content)}
	svc := mailattachment.NewService(reader, mailattachment.RootWriter{Root: w.root, Marker: store}, &fakeAudit{status: "recorded"}, mailattachment.NewSaveReceiptStore(), func() string { return "user@ex.com" })
	r, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: token})
	if err != nil {
		t.Fatalf("Save %q: %v", filename, err)
	}
	return r
}

// Q5=A: the marker is written ONLY on explicit Save — never by Open — and
// only for HTML files; original bytes are kept.
func TestMarkerWrittenOnlyOnHTMLSave(t *testing.T) {
	w, store, _ := newMarkerWS(t)

	// Open (viewer bytes) writes nothing — and in particular never marks.
	reader := &fakePartReader{part: textPart("page.html", "<p>preview</p>")}
	viewer := mailattachment.NewService(reader, mailattachment.RootWriter{Root: w.root, Marker: store}, &fakeAudit{status: "recorded"}, nil, func() string { return "user@ex.com" })
	if _, err := viewer.ViewerBytes(context.Background(), "inbox", "uid:1:1", 1); err != nil {
		t.Fatalf("ViewerBytes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "mail")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open produced workspace content %v — Open writes nothing (US-1)", err)
	}

	// A non-HTML save is not marked.
	rmd := saveHTML(t, w, store, "tok-md", "notes.md", "# notes")
	st, err := store.Status(rmd.Path)
	if err != nil {
		t.Fatalf("Status(notes.md): %v", err)
	}
	if st != mailattachment.StatusOrdinary {
		t.Fatalf("non-HTML save marked mail-derived (founder Q-E: a saved non-HTML mail file is an ordinary workspace file)")
	}

	// The HTML save IS marked, and keeps its ORIGINAL bytes.
	content := "<html><body>original bytes</body></html>"
	rhtml := saveHTML(t, w, store, "tok-html", "page.html", content)
	st, err = store.Status(rhtml.Path)
	if err != nil {
		t.Fatalf("Status(page.html): %v", err)
	}
	if st != mailattachment.StatusMailDerived {
		t.Fatalf("saved mail-derived HTML is not marked (founder Q5=A: scripts off by default requires the marker): status=%v", st)
	}
	data, err := os.ReadFile(filepath.Join(w.dir, filepath.FromSlash(rhtml.Path)))
	if err != nil || string(data) != content {
		t.Fatalf("saved HTML bytes differ from the original (§5.4: Save keeps the original bytes): %q err=%v", data, err)
	}
}

// The marker store is NOT part of the workspace tree (§5.4: it never appears
// as a Library entry).
func TestMarkerLivesOutsideTheWorkspaceTree(t *testing.T) {
	w, store, _ := newMarkerWS(t)
	saveHTML(t, w, store, "tok-outside", "outside.html", "<p>x</p>")
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		t.Fatalf("read workspace root: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name()), "marker") || strings.Contains(strings.ToLower(e.Name()), "mail-derived") {
			t.Fatalf("marker store leaked into the workspace tree: %s (a marker must never appear as a Library entry)", e.Name())
		}
	}
}

// F-8: the allowance is keyed to the saved FILE, never to the content bytes
// — byte-identical twins are independent.
func TestAllowanceIsFileKeyedNeverContentKeyed(t *testing.T) {
	w, store, _ := newMarkerWS(t)
	payload := "<html><body>identical bytes</body></html>"
	a := saveHTML(t, w, store, "tok-twin-a", "twin.html", payload)
	b := saveHTML(t, w, store, "tok-twin-b", "twin.html", payload)
	if a.Path == b.Path {
		t.Fatalf("byte-identical twin saved over the same path %q (two saves are two files)", a.Path)
	}
	if err := store.AllowScripts(a.Path, true); err != nil {
		t.Fatalf("AllowScripts on copy A: %v", err)
	}
	allowed, derived, err := store.ScriptsAllowed(a.Path)
	if err != nil || !allowed || !derived {
		t.Fatalf("copy A allowance = (%v,%v,%v), want (true,true,nil)", allowed, derived, err)
	}
	allowedB, derivedB, err := store.ScriptsAllowed(b.Path)
	if err != nil {
		t.Fatalf("ScriptsAllowed(copy B): %v", err)
	}
	if derivedB != true || allowedB != false {
		t.Fatalf("byte-identical twin B = (allowed=%v, mailDerived=%v), want (false,true) — a content-keyed mechanism would have merged the twins (grill-2 F-8)", allowedB, derivedB)
	}
}

// Provenance mechanics of the store itself: Move re-keys; Copy duplicates
// with a fresh scripts-off allowance (§5.4: a copy starts un-answered).
func TestMarkerMoveAndCopySemantics(t *testing.T) {
	w, store, _ := newMarkerWS(t)
	r := saveHTML(t, w, store, "tok-move", "moved.html", "<p>m</p>")
	if err := store.AllowScripts(r.Path, true); err != nil {
		t.Fatalf("AllowScripts: %v", err)
	}

	newPath := "mail/user@ex.com/" + time.Now().UTC().Format("2006-01") + "/renamed.html"
	if err := store.Move(r.Path, newPath); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if st, err := store.Status(newPath); err != nil || st != mailattachment.StatusMailDerived {
		t.Fatalf("after Move the new path is not marked: (%v,%v)", st, err)
	}
	if st, err := store.Status(r.Path); err != nil || st != mailattachment.StatusOrdinary {
		t.Fatalf("after Move the old path is still marked: (%v,%v)", st, err)
	}
	// The allowance does NOT silently travel with the move's checkbox — the
	// file is still mail-derived; the moved entry starts scripts-off.
	allowed, derived, err := store.ScriptsAllowed(newPath)
	if err != nil || derived != true {
		t.Fatalf("moved file posture = (%v,%v,%v), want mail-derived", allowed, derived, err)
	}

	copyPath := "mail/user@ex.com/" + time.Now().UTC().Format("2006-01") + "/copied.html"
	if cerr := store.Copy(newPath, copyPath); cerr != nil {
		t.Fatalf("Copy: %v", cerr)
	}
	allowedC, derivedC, err := store.ScriptsAllowed(copyPath)
	if err != nil || derivedC != true || allowedC != false {
		t.Fatalf("copied file = (%v,%v,%v), want (false,true,nil) — a copy starts un-answered (§5.4)", allowedC, derivedC, err)
	}
}

// A corrupt marker index fails SAFE: the store reports Unknown, never a
// guess (§5.4 marker-unavailable rule).
func TestCorruptMarkerFailsSafe(t *testing.T) {
	w, store, markerPath := newMarkerWS(t)
	saveHTML(t, w, store, "tok-corrupt", "corrupt.html", "<p>c</p>")
	if err := os.WriteFile(markerPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt the index: %v", err)
	}
	st, err := store.Status("mail/user@ex.com/anything.html")
	if err == nil || st != mailattachment.StatusUnknown {
		t.Fatalf("corrupt index produced (%v,%v), want (Unknown, err) — the store reports the corruption and never guesses entries back", st, err)
	}
	if _, _, err := store.ScriptsAllowed("mail/user@ex.com/anything.html"); err == nil {
		t.Fatalf("ScriptsAllowed on a corrupt index returned no error — the serve layer needs the failure to fail safe (scripts off)")
	}
}

// §5.4's traced-and-proved obligation, stated at the user-visible level:
// after the user renames the saved file, its mail-derived posture FOLLOWS
// the file. RED against the landed code = finding F2a (no gateway Library
// operation re-keys the marker).
func TestMarkerSurvivesLibraryRename(t *testing.T) {
	w, store, _ := newMarkerWS(t)
	r := saveHTML(t, w, store, "tok-rename", "survivor.html", "<p>s</p>")
	newPath := "renamed-in-library.html"
	if _, err := w.root.Rename(r.Path, newPath); err != nil {
		t.Fatalf("Library rename: %v", err)
	}
	st, err := store.Status(newPath)
	if err != nil {
		t.Fatalf("Status after rename: %v", err)
	}
	if st != mailattachment.StatusMailDerived {
		t.Fatalf("renamed mail-derived HTML lost its marker (status=%v) — a known mail-derived file silently upgraded toward the script-permitting profile (§5.4 forbids; finding F2a)", st)
	}
}
