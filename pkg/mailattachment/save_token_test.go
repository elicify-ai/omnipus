package mailattachment_test

// RED pack (w4 spec §9.1 row 3; DS-TOKEN; grill M-02, F-9; US-2.AC-6):
// the bounded save-operation-token reconciliation. Receipts live for the
// process lifetime with NO eviction; a restart is the only bound, and a
// post-restart retry is a NEW save that lands numbered and says so.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/mailattachment"
)

func TestReceiptStoreLookupAfterRecord(t *testing.T) {
	s := mailattachment.NewSaveReceiptStore()
	r := mailattachment.SaveReceipt{Token: "tok", Path: "mail/x/y.pdf", SizeBytes: 3, AuditStatus: mailattachment.AuditRecorded}
	if err := s.Record(r); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, ok := s.Lookup("tok")
	if !ok {
		t.Fatalf("Lookup after Record: not found")
	}
	if got.Path != r.Path || got.Token != "tok" {
		t.Fatalf("Lookup returned %+v, want the recorded receipt", got)
	}
	if _, ok := s.Lookup("unknown"); ok {
		t.Fatalf("Lookup for an unknown token returned a receipt (an unknown token is the normal first-attempt path)")
	}
}

func TestReceiptStoreRefusesDuplicateCommit(t *testing.T) {
	s := mailattachment.NewSaveReceiptStore()
	if err := s.Record(mailattachment.SaveReceipt{Token: "tok"}); err != nil {
		t.Fatalf("first Record: %v", err)
	}
	if err := s.Record(mailattachment.SaveReceipt{Token: "tok"}); !errors.Is(err, mailattachment.ErrDuplicateTokenCommit) {
		t.Fatalf("second Record under one token = %v, want ErrDuplicateTokenCommit (a second commit would defeat the reconciliation's whole point)", err)
	}
}

// F-9: a receipt is never dropped to make room — a late legitimate retry
// always finds its receipt while the process lives.
func TestReceiptsLiveForTheProcessLifetime(t *testing.T) {
	s := mailattachment.NewSaveReceiptStore()
	for i := 0; i < 500; i++ {
		if err := s.Record(mailattachment.SaveReceipt{Token: fmt.Sprintf("tok-%d", i)}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	for i := range 500 {
		if _, ok := s.Lookup(fmt.Sprintf("tok-%d", i)); !ok {
			t.Fatalf("receipt %d evicted — receipts must live for the process lifetime with no eviction (grill-2 F-9)", i)
		}
	}
}

// DS-TOKEN restart row: after a restart (a fresh store), the same token is a
// NEW save that lands numbered and reports no prior receipt.
func TestAfterRestartSameTokenIsANewNumberedSave(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("restart.pdf", "restart-bytes")}
	audit := &fakeAudit{status: "recorded"}
	first := mailattachment.NewService(reader, mailattachment.RootWriter{Root: w.root}, audit, mailattachment.NewSaveReceiptStore(), func() string { return "user@ex.com" })
	r1, err := first.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-restart"})
	if err != nil {
		t.Fatalf("first save: %v", err)
	}

	// (The gateway process restarts: a fresh receipt store.)
	second := mailattachment.NewService(reader, mailattachment.RootWriter{Root: w.root}, audit, mailattachment.NewSaveReceiptStore(), func() string { return "user@ex.com" })
	r2, err := second.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-restart"})
	if err != nil {
		t.Fatalf("post-restart save: %v", err)
	}
	if r2.FromReceipt {
		t.Fatalf("post-restart retry claims a prior receipt — the restart is the only bound (DS-TOKEN); the retry is a new save")
	}
	if !strings.EqualFold(filepath.Base(r2.Path), "restart (1).pdf") {
		t.Fatalf("post-restart save landed %q, want the numbered suffix (lands numbered and says so)", r2.Path)
	}
	if r1.Path == r2.Path {
		t.Fatalf("post-restart save reused the original path %q — that would overwrite", r1.Path)
	}
	month := time.Now().UTC().Format("2006-01")
	entries, _ := os.ReadDir(filepath.Join(w.dir, "mail", "user@ex.com", month))
	if len(entries) != 2 {
		t.Fatalf("%d files exist, want 2 (the original and the numbered post-restart save)", len(entries))
	}
}
