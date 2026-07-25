package httpapi_test

import (
	"net/http"
	"testing"
)

// ---------------------------------------------------------------------------
// PATCH semantics
// ---------------------------------------------------------------------------

// TestPatchDistinguishesAbsentFromNull is the reason patch.Field exists: an
// omitted key must leave a value alone while an explicit null must clear it.
func TestPatchDistinguishesAbsentFromNull(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title":      "with a due date",
		"context_id": h.firstContextID(u),
		"due_on":     "2026-08-01",
		"details":    "some notes",
	}).expect(http.StatusCreated).id()

	path := "/v1/tasks/" + taskID

	// Patching an unrelated field leaves due_on alone.
	body := h.do(http.MethodPatch, path, u.Token, map[string]any{
		"title": "renamed",
	}).expect(http.StatusOK).decode()

	if body["due_on"] != "2026-08-01" {
		t.Errorf("due_on = %v after an unrelated patch, want it untouched", body["due_on"])
	}

	if body["details"] != "some notes" {
		t.Errorf("details = %v after an unrelated patch, want it untouched", body["details"])
	}

	// An explicit null clears it.
	body = h.do(http.MethodPatch, path, u.Token, map[string]any{
		"due_on": nil,
	}).expect(http.StatusOK).decode()

	if body["due_on"] != nil {
		t.Errorf("due_on = %v after an explicit null, want nil", body["due_on"])
	}

	if body["details"] != "some notes" {
		t.Errorf("details = %v, want it still untouched", body["details"])
	}
}

func TestPatchRejectsUnknownFields(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "a task", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated).id()

	// A misspelled field on PATCH is indistinguishable from a successful no-op
	// unless it is rejected, which is why DisallowUnknownFields is on.
	h.do(http.MethodPatch, "/v1/tasks/"+taskID, u.Token, map[string]any{
		"planed_on": "2026-08-01",
	}).expect(http.StatusBadRequest)
}

func TestPatchCannotSetRecurrenceFields(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "a task", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated).id()

	// The spawner owns these; a client rewriting them would break the
	// idempotency of materializing occurrences.
	for _, field := range []string{"recurrence_id", "occurrence_on", "rev", "kind"} {
		h.do(http.MethodPatch, "/v1/tasks/"+taskID, u.Token, map[string]any{
			field: "whatever",
		}).expect(http.StatusBadRequest)
	}
}

func TestEmptyPatchIsANoOp(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	created := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "unchanged", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated).decode()

	id, _ := created["id"].(string)

	after := h.do(http.MethodPatch, "/v1/tasks/"+id, u.Token, map[string]any{}).
		expect(http.StatusOK).decode()

	if after["title"] != created["title"] {
		t.Errorf("title changed on an empty patch: %v -> %v", created["title"], after["title"])
	}

	if after["rev"] != created["rev"] {
		t.Errorf("rev moved on an empty patch: %v -> %v; a no-op should not appear in a sync delta",
			created["rev"], after["rev"])
	}
}

// ---------------------------------------------------------------------------
// Status transitions
// ---------------------------------------------------------------------------

func TestCompletedAtTracksStatus(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "finish me", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated).id()

	path := "/v1/tasks/" + taskID

	body := h.do(http.MethodPatch, path, u.Token, map[string]any{"status": "done"}).
		expect(http.StatusOK).decode()

	if body["completed_at"] == nil {
		t.Error("completed_at is nil after moving to done")
	}

	// Reopening clears it, so the timestamp never describes a status the task no
	// longer has.
	body = h.do(http.MethodPatch, path, u.Token, map[string]any{"status": "todo"}).
		expect(http.StatusOK).decode()

	if body["completed_at"] != nil {
		t.Errorf("completed_at = %v after reopening, want nil", body["completed_at"])
	}

	body = h.do(http.MethodPatch, path, u.Token, map[string]any{"status": "cancelled"}).
		expect(http.StatusOK).decode()

	if body["cancelled_at"] == nil {
		t.Error("cancelled_at is nil after cancelling")
	}
}

func TestDelegationRequiresADelegate(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	// Delegating with nobody named is refused as a validation error, not a raw
	// constraint violation.
	res := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "chase the invoice", "context_id": contextID, "status": "delegated",
	})
	res.expect(http.StatusUnprocessableEntity)

	if res.fields()["delegated_to_id"] == "" {
		t.Errorf("expected a delegated_to_id problem, got %v", res.fields())
	}

	personID := h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{
		"name": "Marc",
	}).expect(http.StatusCreated).id()

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "chase the invoice", "context_id": contextID,
		"status": "delegated", "delegated_to_id": personID,
	}).expect(http.StatusCreated).id()

	// Clearing the delegate while the task is still delegated is the mirror
	// case, and must be refused the same way.
	res = h.do(http.MethodPatch, "/v1/tasks/"+taskID, u.Token, map[string]any{
		"delegated_to_id": nil,
	})
	res.expect(http.StatusUnprocessableEntity)

	if res.fields()["delegated_to_id"] == "" {
		t.Errorf("expected a delegated_to_id problem, got %v", res.fields())
	}

	// Clearing both at once is coherent and allowed.
	h.do(http.MethodPatch, "/v1/tasks/"+taskID, u.Token, map[string]any{
		"delegated_to_id": nil, "status": "todo",
	}).expect(http.StatusOK)
}

func TestDelegateByName(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	// One call delegates to a person who does not exist yet.
	body := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "ask Marc about the quote", "context_id": h.firstContextID(u),
		"delegated_to": "Marc", "status": "delegated",
	}).expect(http.StatusCreated).decode()

	delegatedTo, _ := body["delegated_to_id"].(string)
	if delegatedTo == "" {
		t.Fatal("delegated_to_id is empty after delegating by name")
	}

	person := h.do(http.MethodGet, "/v1/people/"+delegatedTo, u.Token, nil).
		expect(http.StatusOK).decode()

	if person["name"] != "Marc" {
		t.Errorf("created person name = %v, want Marc", person["name"])
	}

	// A second mention reuses the same person rather than making a duplicate.
	second := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "and about the invoice", "context_id": h.firstContextID(u),
		"delegated_to": "marc",
	}).expect(http.StatusCreated).decode()

	if second["delegated_to_id"] != delegatedTo {
		t.Errorf("second delegation created a new person: %v vs %v",
			second["delegated_to_id"], delegatedTo)
	}

	people := h.do(http.MethodGet, "/v1/people", u.Token, nil).expect(http.StatusOK).list()
	if len(people) != 1 {
		t.Errorf("people count = %d, want 1", len(people))
	}
}

func TestDelegateByNameConflictsWithID(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	personID := h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{
		"name": "Marc",
	}).expect(http.StatusCreated).id()

	res := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "ambiguous", "context_id": h.firstContextID(u),
		"delegated_to": "Someone Else", "delegated_to_id": personID,
	})
	res.expect(http.StatusUnprocessableEntity)

	if res.fields()["delegated_to"] == "" {
		t.Errorf("expected a delegated_to problem, got %v", res.fields())
	}
}

// ---------------------------------------------------------------------------
// Graph edges
// ---------------------------------------------------------------------------

func TestCyclesRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	newTask := func(title string) string {
		return h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
			"title": title, "context_id": contextID,
		}).expect(http.StatusCreated).id()
	}

	for _, column := range []string{"parent_id", "blocked_by_id"} {
		a, b, c := newTask("a"), newTask("b"), newTask("c")

		// Self-reference.
		res := h.do(http.MethodPatch, "/v1/tasks/"+a, u.Token, map[string]any{column: a})
		res.expect(http.StatusUnprocessableEntity)

		if res.fields()[column] == "" {
			t.Errorf("%s self-reference: expected a %s problem, got %v", column, column, res.fields())
		}

		// A two-hop cycle: b -> a, then a -> b.
		h.do(http.MethodPatch, "/v1/tasks/"+b, u.Token, map[string]any{column: a}).
			expect(http.StatusOK)

		res = h.do(http.MethodPatch, "/v1/tasks/"+a, u.Token, map[string]any{column: b})
		if res.Status != http.StatusUnprocessableEntity {
			t.Errorf("%s two-hop cycle: status = %d, want 422; body %s", column, res.Status, res.Body)
		}

		// A three-hop cycle: c -> b -> a, then a -> c.
		h.do(http.MethodPatch, "/v1/tasks/"+c, u.Token, map[string]any{column: b}).
			expect(http.StatusOK)

		res = h.do(http.MethodPatch, "/v1/tasks/"+a, u.Token, map[string]any{column: c})
		if res.Status != http.StatusUnprocessableEntity {
			t.Errorf("%s three-hop cycle: status = %d, want 422; body %s", column, res.Status, res.Body)
		}
	}
}

func TestKindDerivedThroughAPI(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	parent := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "a long one", "context_id": contextID,
	}).expect(http.StatusCreated)

	// Before it has children it is short.
	if kind := parent.decode()["kind"]; kind != "short" {
		t.Errorf("kind = %v before any subtask, want short", kind)
	}

	parentID := parent.id()

	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "a subtask", "context_id": contextID, "parent_id": parentID,
	}).expect(http.StatusCreated)

	// Adding a child flips it to long with no extra write, which is the payoff
	// for deriving kind instead of storing it.
	body := h.do(http.MethodGet, "/v1/tasks/"+parentID, u.Token, nil).
		expect(http.StatusOK).decode()

	if body["kind"] != "long" {
		t.Errorf("kind = %v after adding a subtask, want long", body["kind"])
	}
}

// ---------------------------------------------------------------------------
// Cascades
// ---------------------------------------------------------------------------

func TestDeleteContextMovesTasksToInbox(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	projectID := h.do(http.MethodPost, "/v1/projects", u.Token, map[string]any{
		"context_id": contextID, "name": "a project",
	}).expect(http.StatusCreated).id()

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "survives its context", "context_id": contextID, "project_id": projectID,
	}).expect(http.StatusCreated).id()

	h.do(http.MethodDelete, "/v1/contexts/"+contextID, u.Token, nil).expect(http.StatusNoContent)

	// Losing a bucket must not lose the work inside it.
	body := h.do(http.MethodGet, "/v1/tasks/"+taskID, u.Token, nil).
		expect(http.StatusOK).decode()

	if body["context_id"] != nil {
		t.Errorf("context_id = %v after deleting the context, want nil", body["context_id"])
	}

	if body["project_id"] != nil {
		t.Errorf("project_id = %v after deleting the context, want nil", body["project_id"])
	}

	// The project went with the context.
	h.do(http.MethodGet, "/v1/projects/"+projectID, u.Token, nil).expect(http.StatusNotFound)

	// And the task is reachable as inbox.
	items := h.do(http.MethodGet, "/v1/tasks?context_id=null", u.Token, nil).
		expect(http.StatusOK).list()

	if len(items) != 1 {
		t.Errorf("inbox has %d tasks, want 1", len(items))
	}
}

func TestDeleteTaskTombstonesSubtreeAndUnblocks(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	newTask := func(title string, extra map[string]any) string {
		body := map[string]any{"title": title, "context_id": contextID}
		for k, v := range extra {
			body[k] = v
		}

		return h.do(http.MethodPost, "/v1/tasks", u.Token, body).
			expect(http.StatusCreated).id()
	}

	parent := newTask("parent", nil)
	child := newTask("child", map[string]any{"parent_id": parent})
	grandchild := newTask("grandchild", map[string]any{"parent_id": child})

	waiting := newTask("waiting on the parent", map[string]any{
		"parent_id": nil, "blocked_by_id": parent, "status": "blocked",
	})

	h.do(http.MethodDelete, "/v1/tasks/"+parent, u.Token, nil).expect(http.StatusNoContent)

	// The tombstone has to reach every depth, not just direct children.
	for label, id := range map[string]string{
		"parent": parent, "child": child, "grandchild": grandchild,
	} {
		if got := h.do(http.MethodGet, "/v1/tasks/"+id, u.Token, nil).Status; got != http.StatusNotFound {
			t.Errorf("%s after delete = %d, want 404", label, got)
		}
	}

	// Anything that was waiting on it must not wait forever.
	body := h.do(http.MethodGet, "/v1/tasks/"+waiting, u.Token, nil).
		expect(http.StatusOK).decode()

	if body["blocked_by_id"] != nil {
		t.Errorf("blocked_by_id = %v after its blocker was deleted, want nil", body["blocked_by_id"])
	}

	if body["status"] != "todo" {
		t.Errorf("status = %v after its blocker was deleted, want todo", body["status"])
	}
}

func TestDeletePersonUndelegates(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	personID := h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{
		"name": "Marc",
	}).expect(http.StatusCreated).id()

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "delegated to Marc", "context_id": h.firstContextID(u),
		"status": "delegated", "delegated_to_id": personID,
	}).expect(http.StatusCreated).id()

	h.do(http.MethodDelete, "/v1/people/"+personID, u.Token, nil).expect(http.StatusNoContent)

	body := h.do(http.MethodGet, "/v1/tasks/"+taskID, u.Token, nil).
		expect(http.StatusOK).decode()

	if body["delegated_to_id"] != nil {
		t.Errorf("delegated_to_id = %v after deleting the person, want nil", body["delegated_to_id"])
	}

	// The schema forbids a delegated task with no delegate, so the status had to
	// move too.
	if body["status"] != "todo" {
		t.Errorf("status = %v after deleting the delegate, want todo", body["status"])
	}
}

func TestMovingProjectMovesItsTasks(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	contexts := h.do(http.MethodGet, "/v1/contexts", u.Token, nil).expect(http.StatusOK).list()
	first, _ := contexts[0]["id"].(string)
	second, _ := contexts[1]["id"].(string)

	projectID := h.do(http.MethodPost, "/v1/projects", u.Token, map[string]any{
		"context_id": first, "name": "a project",
	}).expect(http.StatusCreated).id()

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "in the project", "context_id": first, "project_id": projectID,
	}).expect(http.StatusCreated).id()

	h.do(http.MethodPatch, "/v1/projects/"+projectID, u.Token, map[string]any{
		"context_id": second,
	}).expect(http.StatusOK)

	// Leaving the task behind would strand it in a context its project no
	// longer belongs to.
	body := h.do(http.MethodGet, "/v1/tasks/"+taskID, u.Token, nil).
		expect(http.StatusOK).decode()

	if body["context_id"] != second {
		t.Errorf("task context_id = %v after its project moved, want %v", body["context_id"], second)
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func TestValidationErrors(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"missing title", map[string]any{"context_id": contextID}, "title"},
		{"blank title", map[string]any{"title": "   ", "context_id": contextID}, "title"},
		{
			"impossible date",
			map[string]any{"title": "t", "context_id": contextID, "due_on": "2026-02-31"},
			"due_on",
		},
		{
			"wrong date format",
			map[string]any{"title": "t", "context_id": contextID, "due_on": "25/07/2026"},
			"due_on",
		},
		{
			"bad status",
			map[string]any{"title": "t", "context_id": contextID, "status": "procrastinating"},
			"status",
		},
		{
			"bad capture method",
			map[string]any{"title": "t", "context_id": contextID, "capture_method": "telepathy"},
			"capture_method",
		},
		{
			"zero estimate",
			map[string]any{"title": "t", "context_id": contextID, "estimate_minutes": 0},
			"estimate_minutes",
		},
		{
			"negative estimate",
			map[string]any{"title": "t", "context_id": contextID, "estimate_minutes": -5},
			"estimate_minutes",
		},
		{
			"relative reference url",
			map[string]any{"title": "t", "context_id": contextID, "reference_url": "/not/absolute"},
			"reference_url",
		},
		{
			"unknown source",
			map[string]any{"title": "t", "context_id": contextID, "source": "carrier_pigeon"},
			"source",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do(http.MethodPost, "/v1/tasks", u.Token, tc.body)
			res.expect(http.StatusUnprocessableEntity)

			if detail := res.fields()[tc.field]; detail == "" {
				t.Errorf("error names %v, want an entry for %s", res.fields(), tc.field)
			}
		})
	}
}

func TestMalformedBodies(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	cases := map[string]struct {
		body   string
		status int
	}{
		"not json":         {"this is not json", http.StatusBadRequest},
		"empty":            {"", http.StatusBadRequest},
		"wrong type":       {`{"title": 42}`, http.StatusBadRequest},
		"two documents":    {`{"title": "a"} {"title": "b"}`, http.StatusBadRequest},
		"unknown field":    {`{"title": "a", "nonsense": 1}`, http.StatusBadRequest},
		"array not object": {`[{"title": "a"}]`, http.StatusBadRequest},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h.do(http.MethodPost, "/v1/tasks", u.Token, tc.body).expect(tc.status)
		})
	}
}

func TestRecurrenceValidation(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	base := func(extra map[string]any) map[string]any {
		body := map[string]any{
			"context_id": contextID, "title": "standup",
			"rrule": "FREQ=DAILY", "starts_on": "2026-07-25",
		}

		for k, v := range extra {
			body[k] = v
		}

		return body
	}

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"missing rrule", base(map[string]any{"rrule": ""}), "rrule"},
		{"rrule without freq", base(map[string]any{"rrule": "BYDAY=MO"}), "rrule"},
		{"rrule bad freq", base(map[string]any{"rrule": "FREQ=FORTNIGHTLY"}), "rrule"},
		{"rrule malformed part", base(map[string]any{"rrule": "FREQ=DAILY;NOEQUALS"}), "rrule"},
		{"missing starts_on", base(map[string]any{"starts_on": ""}), "starts_on"},
		{"unknown timezone", base(map[string]any{"timezone": "Mars/Olympus"}), "timezone"},
		{"ends before starts", base(map[string]any{"ends_on": "2026-07-01"}), "ends_on"},
		{"negative lead_days", base(map[string]any{"lead_days": -1}), "lead_days"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do(http.MethodPost, "/v1/recurrences", u.Token, tc.body)
			res.expect(http.StatusUnprocessableEntity)

			if detail := res.fields()[tc.field]; detail == "" {
				t.Errorf("error names %v, want an entry for %s", res.fields(), tc.field)
			}
		})
	}

	// A valid weekly rule with a BYDAY part is accepted.
	h.do(http.MethodPost, "/v1/recurrences", u.Token,
		base(map[string]any{"rrule": "FREQ=WEEKLY;BYDAY=MO", "timezone": "Europe/Paris"}),
	).expect(http.StatusCreated)
}

func TestDuplicateContextSlugRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.do(http.MethodPost, "/v1/contexts", u.Token, map[string]any{
		"name": "Side Project",
	}).expect(http.StatusCreated)

	res := h.do(http.MethodPost, "/v1/contexts", u.Token, map[string]any{
		"name": "side project",
	})
	res.expect(http.StatusUnprocessableEntity)

	if res.fields()["slug"] == "" {
		t.Errorf("expected a slug problem, got %v", res.fields())
	}

	// Another user is free to use the same name: the index is per user.
	other := h.user("other@example.com")
	h.do(http.MethodPost, "/v1/contexts", other.Token, map[string]any{
		"name": "Side Project",
	}).expect(http.StatusCreated)
}

func TestDuplicatePersonNameRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{"name": "Marc"}).
		expect(http.StatusCreated)

	res := h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{"name": "marc"})
	res.expect(http.StatusUnprocessableEntity)

	if res.fields()["name"] == "" {
		t.Errorf("expected a name problem, got %v", res.fields())
	}
}

// ---------------------------------------------------------------------------
// Listing, filtering, paging
// ---------------------------------------------------------------------------

func TestTaskFilters(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	contexts := h.do(http.MethodGet, "/v1/contexts", u.Token, nil).expect(http.StatusOK).list()
	first, _ := contexts[0]["id"].(string)
	second, _ := contexts[1]["id"].(string)

	mk := func(body map[string]any) string {
		return h.do(http.MethodPost, "/v1/tasks", u.Token, body).
			expect(http.StatusCreated).id()
	}

	mk(map[string]any{"title": "todo in first", "context_id": first, "planned_on": "2026-08-01"})
	mk(map[string]any{"title": "done in first", "context_id": first, "status": "done"})
	mk(map[string]any{"title": "todo in second", "context_id": second, "due_on": "2026-09-01"})
	mk(map[string]any{"title": "untriaged"})

	cases := map[string]struct {
		query string
		want  int
	}{
		"all":                {"", 4},
		"by status":          {"?status=done", 1},
		"by two statuses":    {"?status=done,todo", 3},
		"by context":         {"?context_id=" + first, 2},
		"inbox":              {"?context_id=null", 1},
		"planned on a day":   {"?planned_on=2026-08-01", 1},
		"planned before":     {"?planned_before=2026-07-01", 0},
		"due after":          {"?due_after=2026-08-15", 1},
		"search":             {"?q=untriaged", 1},
		"search no match":    {"?q=nonexistent", 0},
		"by kind":            {"?kind=short", 4},
		"context and status": {"?context_id=" + first + "&status=todo", 1},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			items := h.do(http.MethodGet, "/v1/tasks"+tc.query, u.Token, nil).
				expect(http.StatusOK).list()

			if len(items) != tc.want {
				t.Errorf("GET /v1/tasks%s returned %d tasks, want %d", tc.query, len(items), tc.want)
			}
		})
	}
}

func TestInvalidFilterValues(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	cases := map[string]string{
		"status": "?status=procrastinating",
		"kind":   "?kind=medium",
		"limit":  "?limit=0",
		"due_on": "?due_on=nonsense",
	}

	for field, query := range cases {
		t.Run(field, func(t *testing.T) {
			res := h.do(http.MethodGet, "/v1/tasks"+query, u.Token, nil)
			res.expect(http.StatusUnprocessableEntity)

			if res.fields()[field] == "" {
				t.Errorf("error names %v, want an entry for %s", res.fields(), field)
			}
		})
	}
}

func TestPaginationWalksEveryRow(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	const total = 12

	for i := 0; i < total; i++ {
		h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
			"title": "task", "context_id": contextID,
		}).expect(http.StatusCreated)
	}

	seen := map[string]bool{}
	path := "/v1/tasks?limit=5"

	for range 10 { // generous bound so a paging bug fails instead of hanging
		var body struct {
			Data       []map[string]any `json:"data"`
			NextCursor *string          `json:"next_cursor"`
		}

		res := h.do(http.MethodGet, path, u.Token, nil).expect(http.StatusOK)
		res.decodeInto(&body)

		for _, item := range body.Data {
			id, _ := item["id"].(string)
			if seen[id] {
				t.Errorf("id %s appeared on two pages", id)
			}

			seen[id] = true
		}

		if body.NextCursor == nil {
			break
		}

		path = "/v1/tasks?limit=5&cursor=" + *body.NextCursor
	}

	if len(seen) != total {
		t.Errorf("paging visited %d of %d tasks", len(seen), total)
	}
}

func TestDeletedRowsAreExcludedButFetchable(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "to be deleted", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated).id()

	h.do(http.MethodDelete, "/v1/tasks/"+taskID, u.Token, nil).expect(http.StatusNoContent)

	if items := h.do(http.MethodGet, "/v1/tasks", u.Token, nil).expect(http.StatusOK).list(); len(items) != 0 {
		t.Errorf("default listing returned %d deleted tasks, want 0", len(items))
	}

	// Tombstones stay visible to a sync client that asks for them.
	items := h.do(http.MethodGet, "/v1/tasks?include_deleted=true", u.Token, nil).
		expect(http.StatusOK).list()

	if len(items) != 1 {
		t.Fatalf("include_deleted returned %d tasks, want 1", len(items))
	}

	if items[0]["deleted_at"] == nil {
		t.Error("tombstoned task has a nil deleted_at")
	}
}

func TestSourcesAreListable(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	items := h.do(http.MethodGet, "/v1/sources", u.Token, nil).expect(http.StatusOK).list()
	if len(items) != 6 {
		t.Errorf("sources = %d, want the 6 seeded", len(items))
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	// PUT is not wired anywhere; ServeMux answers 405 because the path exists
	// for other methods.
	h.do(http.MethodPut, "/v1/tasks", u.Token, map[string]any{"title": "x"}).
		expect(http.StatusMethodNotAllowed)
}
