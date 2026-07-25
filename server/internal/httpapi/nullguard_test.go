package httpapi_test

import (
	"net/http"
	"testing"
)

// TestPatchNullOnNonNullableColumns probes fields exposed as nullable in a PATCH
// body whose column is NOT NULL. Each must be a 422 naming the field, never a 500:
// a client mistake has to be reported as a client mistake.
func TestPatchNullOnNonNullableColumns(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	contextID := h.firstContextID(u)

	recurrenceID := h.do(http.MethodPost, "/v1/recurrences", u.Token, map[string]any{
		"context_id": contextID, "title": "weekly", "rrule": "FREQ=WEEKLY",
		"starts_on": "2026-07-25",
	}).expect(http.StatusCreated).id()

	cases := map[string]struct {
		path  string
		body  map[string]any
		field string
	}{
		"context sort_order": {
			"/v1/contexts/" + contextID, map[string]any{"sort_order": nil}, "sort_order",
		},
		"recurrence lead_days": {
			"/v1/recurrences/" + recurrenceID, map[string]any{"lead_days": nil}, "lead_days",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := h.do(http.MethodPatch, tc.path, u.Token, tc.body)

			if res.Status == http.StatusInternalServerError {
				t.Fatalf("null on %s returned 500; a client mistake must not read as a "+
					"server fault. body: %s", tc.field, res.Body)
			}

			res.expect(http.StatusUnprocessableEntity)

			if res.fields()[tc.field] == "" {
				t.Errorf("error names %v, want an entry for %s", res.fields(), tc.field)
			}
		})
	}
}
