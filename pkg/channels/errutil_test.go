package channels

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClassifySendError(t *testing.T) {
	raw := fmt.Errorf("some API error")

	tests := []struct {
		name       string
		statusCode int
		wantIs     error
		wantNil    bool
	}{
		{"429 -> ErrRateLimit", 429, ErrRateLimit, false},
		{"500 -> ErrTemporary", 500, ErrTemporary, false},
		{"502 -> ErrTemporary", 502, ErrTemporary, false},
		{"503 -> ErrTemporary", 503, ErrTemporary, false},
		{"400 -> ErrSendFailed", 400, ErrSendFailed, false},
		{"403 -> ErrSendFailed", 403, ErrSendFailed, false},
		{"404 -> ErrSendFailed", 404, ErrSendFailed, false},
		{"200 -> raw error", 200, nil, false},
		{"201 -> raw error", 201, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ClassifySendError(tt.statusCode, raw)
			if err == nil {
				t.Fatal("expected non-nil error")
			}
			if tt.wantIs != nil {
				if !errors.Is(err, tt.wantIs) {
					t.Errorf("errors.Is(err, %v) = false, want true; err = %v", tt.wantIs, err)
				}
			} else {
				// Should return the raw error unchanged
				if err != raw {
					t.Errorf("expected raw error to be returned unchanged for status %d, got %v", tt.statusCode, err)
				}
			}
		})
	}
}

func TestClassifySendErrorNoFalsePositive(t *testing.T) {
	raw := fmt.Errorf("some error")

	// 429 should NOT match ErrTemporary or ErrSendFailed
	err := ClassifySendError(429, raw)
	if errors.Is(err, ErrTemporary) {
		t.Error("429 should not match ErrTemporary")
	}
	if errors.Is(err, ErrSendFailed) {
		t.Error("429 should not match ErrSendFailed")
	}

	// 500 should NOT match ErrRateLimit or ErrSendFailed
	err = ClassifySendError(500, raw)
	if errors.Is(err, ErrRateLimit) {
		t.Error("500 should not match ErrRateLimit")
	}
	if errors.Is(err, ErrSendFailed) {
		t.Error("500 should not match ErrSendFailed")
	}

	// 400 should NOT match ErrRateLimit or ErrTemporary
	err = ClassifySendError(400, raw)
	if errors.Is(err, ErrRateLimit) {
		t.Error("400 should not match ErrRateLimit")
	}
	if errors.Is(err, ErrTemporary) {
		t.Error("400 should not match ErrTemporary")
	}
}

func TestClassifyNetError(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		if err := ClassifyNetError(nil); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("non-nil error wraps as ErrTemporary", func(t *testing.T) {
		raw := fmt.Errorf("connection refused")
		err := ClassifyNetError(raw)
		if err == nil {
			t.Fatal("expected non-nil error")
		}
		if !errors.Is(err, ErrTemporary) {
			t.Errorf("errors.Is(err, ErrTemporary) = false, want true; err = %v", err)
		}
	})
}

// TestClassifyMediaSendError is the unit-level regression coverage for the
// CRITICAL sendfile-fix review finding: Telegram/Feishu/Matrix/Slack's
// per-part SendMedia loops used to return a bare ErrTemporary on any
// mid-loop send/upload failure, regardless of how many earlier parts in the
// same message had already been delivered. Because
// Manager.sendMediaWithRetry retries the ENTIRE message with the same,
// unmutated bus.OutboundMediaMessage (no per-part resume), that made a
// retry re-resolve and re-send every part a failed attempt already
// delivered — duplicating media for the user, silently, since a
// subsequent successful retry returned nil.
func TestClassifyMediaSendError(t *testing.T) {
	raw := fmt.Errorf("upload failed: connection reset")

	t.Run("nil error returns nil", func(t *testing.T) {
		if err := ClassifyMediaSendError("telegram", 0, nil); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("sentCount == 0 classifies ErrTemporary (safe to retry)", func(t *testing.T) {
		err := ClassifyMediaSendError("telegram", 0, raw)
		if err == nil {
			t.Fatal("expected non-nil error")
		}
		if !errors.Is(err, ErrTemporary) {
			t.Errorf("errors.Is(err, ErrTemporary) = false, want true; err = %v", err)
		}
		if errors.Is(err, ErrSendFailed) {
			t.Error("sentCount==0 must not be classified permanent")
		}
	})

	t.Run("sentCount > 0 classifies ErrSendFailed (retry would duplicate)", func(t *testing.T) {
		err := ClassifyMediaSendError("telegram", 1, raw)
		if err == nil {
			t.Fatal("expected non-nil error")
		}
		if !errors.Is(err, ErrSendFailed) {
			t.Errorf("errors.Is(err, ErrSendFailed) = false, want true; err = %v", err)
		}
		if errors.Is(err, ErrTemporary) {
			t.Error("a failure after a partial delivery must not also match ErrTemporary " +
				"(sendMediaWithRetry checks ErrSendFailed/ErrNotRunning first and breaks, " +
				"but the error must not be ambiguously retryable)")
		}
	})

	t.Run("real underlying error is preserved, not flattened", func(t *testing.T) {
		for _, sentCount := range []int{0, 1, 2} {
			err := ClassifyMediaSendError("slack", sentCount, raw)
			if !strings.Contains(err.Error(), "connection reset") {
				t.Errorf(
					"sentCount=%d: expected the real cause in the message, got %q (flattened to a bare sentinel)",
					sentCount, err.Error(),
				)
			}
			if !errors.Is(err, raw) {
				t.Errorf("sentCount=%d: errors.Is(err, raw) = false, want true (real error must stay in the chain)",
					sentCount)
			}
		}
	})
}
