package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestViewportDiagnosticPreservesInputAdmissionDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lv := &LiveView{}
		lv.mu.Lock()
		state := lv.inputStateLocked()
		lv.mu.Unlock()
		state.gate <- struct{}{}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := lv.withViewportAdmission(ctx, context.Background(), func(context.Context) (bool, error) { t.Fatal("blocked admission ran work"); return false, nil }, nil)
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "input admission") {
			t.Fatalf("missing stage/deadline: %v", err)
		}
		<-state.gate
	})
}

func TestViewportDiagnosticPreservesWorkStageAfterCancellation(t *testing.T) {
	lv := &LiveView{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := lv.withViewportAdmission(ctx, context.Background(), func(context.Context) (bool, error) {
		cancel()
		return false, errors.New("initial geometry: command failed")
	}, nil)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "initial geometry") {
		t.Fatalf("stage lost on cancellation: %v", err)
	}
}
