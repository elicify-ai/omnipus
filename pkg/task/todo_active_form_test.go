package task

import (
	"encoding/json"
	"strings"
	"testing"
)

// The optional field and 500-character ceiling come from the 2026-10-21 dispatch.
func TestTodoActiveForm(t *testing.T) {
	t.Run("json_round_trip_and_absent_field", func(t *testing.T) {
		var td Todo
		withForm := `{"text":"Draft the Q4 summary","status":"in_progress","active_form":"Drafting the Q4 summary"}`
		if err := json.Unmarshal([]byte(withForm), &td); err != nil {
			t.Fatalf("decode todo with active_form: %v", err)
		}
		data, err := json.Marshal(td)
		if err != nil {
			t.Fatalf("encode todo with active_form: %v", err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatalf("decode round-trip: %v", err)
		}
		if fields["text"] != "Draft the Q4 summary" || fields["status"] != "in_progress" || fields["active_form"] != "Drafting the Q4 summary" || len(fields) != 3 {
			t.Errorf("round-trip lost or changed todo fields: %s", data)
		}

		// Reusing a decoder destination must not retain an earlier active form.
		withoutForm := `{"text":"Publish the summary","status":"completed"}`
		if err := json.Unmarshal([]byte(withoutForm), &td); err != nil {
			t.Fatalf("decode existing todo without active_form: %v", err)
		}
		data, err = json.Marshal(td)
		if err != nil {
			t.Fatalf("encode existing todo: %v", err)
		}
		if string(data) != withoutForm {
			t.Errorf("existing todo changed: got %s, want %s", data, withoutForm)
		}
	})

	t.Run("store_validation_character_limit", func(t *testing.T) {
		cases := []struct {
			name    string
			form    string
			wantErr string
		}{
			{name: "absent"},
			{name: "one", form: "a"},
			{name: "two", form: "ab"},
			{name: "499", form: strings.Repeat("a", 499)},
			{name: "500", form: strings.Repeat("a", 500)},
			{name: "500_unicode", form: strings.Repeat("界", 500)},
			{name: "501", form: strings.Repeat("a", 501), wantErr: "todo[0]: active_form must be 500 characters or fewer"},
			{name: "501_unicode", form: strings.Repeat("界", 501), wantErr: "todo[0]: active_form must be 500 characters or fewer"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				data, err := json.Marshal(map[string]any{"text": "Draft the Q4 summary", "status": "pending", "active_form": tc.form})
				if err != nil {
					t.Fatalf("encode input: %v", err)
				}
				var td Todo
				if err := json.Unmarshal(data, &td); err != nil {
					t.Fatalf("decode input: %v", err)
				}
				err = validateTodos([]Todo{td})
				if tc.wantErr == "" {
					if err != nil {
						t.Fatalf("valid active_form rejected: %v", err)
					}
					return
				}
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("validation error = %v, want %q", err, tc.wantErr)
				}
			})
		}
	})
}
