package mailattachment_test

// RED pack (w4 spec §9.1 row 2; US-2.AC-2/AC-3/AC-7; DS-NAMES/DS-SAVE-PATHS):
// the sanitize→validate chain, parent creation, publication discipline.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/mailattachment"
	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

func monthDir() string { return "mail/user@ex.com/" + time.Now().UTC().Format("2006-01") }

func TestSaveEmptyFilenameDefaultsToAttachment(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("", "nonamed")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	receipt, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "t-noname"})
	if err != nil {
		t.Fatalf("Save with no declared filename: %v", err)
	}
	if filepath.Base(receipt.Path) != "attachment" {
		t.Fatalf("empty declared name landed %q, want attachment (the download route's documented default, reused by Save)", receipt.Path)
	}
}

func TestSaveDotNameNeutralized(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("..", "dotdot")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	receipt, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "t-dotdot"})
	if err != nil {
		t.Fatalf("Save with dot-name: %v", err)
	}
	if filepath.Base(receipt.Path) != "attachment" {
		t.Fatalf("dot-name landed %q, want the neutralized default (§2.1: dot/dot-dot neutralized; empty → attachment)", receipt.Path)
	}
}

// DS-NAMES: the Library's create-name validation is the authorization
// (US-2.AC-3) — Save's outcome for a name must AGREE with the validation
// primitive the spec names (pkg/library's per-OS rules; Windows reserves
// device stems, POSIX sets do not — the project's stage-0 split). The save
// chain is conformant when it refuses exactly what the active rule set
// refuses, with the typed unsafe-name error and no partial file.
func TestSaveOutcomeAgreesWithCreateNameValidation(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("con.pdf", "reserved")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	before := snap(t, w)

	validationRefuses := pathsafe.ValidateComponent("con.pdf") != nil
	receipt, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "t-con"})
	if validationRefuses {
		if err == nil {
			t.Fatalf("the active rule set rejects con.pdf but Save landed %q — the save chain must refuse what create-name validation refuses (US-2.AC-3)", receipt.Path)
		}
		if !errors.Is(err, mailattachment.ErrUnsafeName) {
			t.Fatalf("refusal must be the typed unsafe-name error, got %v", err)
		}
		assertNoWrites(t, before, snap(t, w))
		return
	}
	// On rule sets that accept the stem, the file saves INSIDE the mail
	// folder with the sanitized name — and that is conformant.
	if err != nil {
		t.Fatalf("POSIX rule set accepts con.pdf; Save refused anyway: %v", err)
	}
	if !strings.HasPrefix(receipt.Path, monthDir()+"/") {
		t.Fatalf("saved outside the mail folder: %q", receipt.Path)
	}
}

// DS-NAMES: a 200-char name is ordinary content and must save.
func TestSaveLongNameWithinLimitsSaves(t *testing.T) {
	w := newWSRoot(t)
	long := strings.Repeat("n", 200-4) + ".pdf" // 200 chars total
	reader := &fakePartReader{part: textPart(long, "longname")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	receipt, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "t-long"})
	if err != nil {
		t.Fatalf("200-char declared name must save (DS-NAMES): %v", err)
	}
	if len(filepath.Base(receipt.Path)) != 200 {
		t.Fatalf("stored name length = %d, want 200", len(filepath.Base(receipt.Path)))
	}
}

// DS-SAVE-PATHS: a file occupying a component of the save hierarchy is
// refused with the destination-write class, and nothing partial remains
// (US-2.AC-7).
func TestSaveParentOccupiedByFileRefused(t *testing.T) {
	w := newWSRoot(t)
	// Occupy mail/user@ex.com with a file before any save created the tree.
	blockerRel := "mail/user@ex.com"
	if err := os.MkdirAll(filepath.Join(w.dir, "mail"), 0o700); err != nil {
		t.Fatalf("fixture mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.dir, filepath.FromSlash(blockerRel)), []byte("a file where a directory must go"), 0o600); err != nil {
		t.Fatalf("fixture write blocker: %v", err)
	}
	reader := &fakePartReader{part: textPart("blocked.pdf", "blocked")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	before := snap(t, w)
	_, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "t-blocked"})
	if err == nil {
		t.Fatalf("save over a file-occupied path component must be refused (US-2.AC-7: parent-file conflict)")
	}
	if !errors.Is(err, mailattachment.ErrDestinationWrite) {
		t.Fatalf("parent-conflict refusal must be the destination-write class, got %v", err)
	}
	assertNoWrites(t, before, snap(t, w))
}
