package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests guard #96: JSON responses were served as text/plain on 201-Created
// paths because Content-Type was set AFTER w.WriteHeader (which silently drops
// header mutations). Every JSON helper must emit application/json regardless of
// the status code it writes.

func TestWriteJSON_ContentTypeAcrossStatusCodes(t *testing.T) {
	for _, code := range []int{
		http.StatusOK,
		http.StatusCreated,
		http.StatusAccepted,
		http.StatusBadRequest,
		http.StatusInternalServerError,
	} {
		rec := httptest.NewRecorder()
		writeJSON(rec, code, map[string]any{"ok": true})

		if rec.Code != code {
			t.Errorf("status %d: got code %d", code, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("status %d: Content-Type = %q, want application/json", code, ct)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("status %d: body not valid JSON: %v (body=%q)", code, err, rec.Body.String())
		}
	}
}

func TestJSONOK_Is200JSON(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonOK(rec, map[string]string{"hello": "world"})

	if rec.Code != http.StatusOK {
		t.Errorf("jsonOK status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("jsonOK Content-Type = %q, want application/json", ct)
	}
}

func TestJSONCreated_Is201JSON(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonCreated(rec, map[string]string{"id": "abc"})

	if rec.Code != http.StatusCreated {
		t.Errorf("jsonCreated status = %d, want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("jsonCreated Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("jsonCreated body not valid JSON: %v", err)
	}
	if body["id"] != "abc" {
		t.Errorf("jsonCreated body = %v, want id=abc", body)
	}
}

func TestJSONErr_Is4xxJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonErr(rec, http.StatusBadRequest, "bad")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("jsonErr status = %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("jsonErr Content-Type = %q, want application/json", ct)
	}
}

// TestNoWriteHeaderBeforeJSONOK documents the regression: the buggy pattern was
// `w.WriteHeader(201)` followed by `jsonOK(w, ...)`, which drops the Content-Type.
// jsonCreated exists precisely to avoid that ordering. This asserts the fixed
// helper does NOT require a preceding WriteHeader and still lands a 201.
func TestJSONCreated_NoPrecedingWriteHeaderNeeded(t *testing.T) {
	rec := httptest.NewRecorder()
	// No w.WriteHeader call before jsonCreated — the helper owns the status.
	jsonCreated(rec, struct {
		Name string `json:"name"`
	}{Name: "x"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
}
