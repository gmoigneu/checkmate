package httpapi_test

import (
	"net/http"
	"net/url"
	"testing"
)

// urlEscape encodes a query value.
func urlEscape(s string) string { return url.QueryEscape(s) }

// ---------------------------------------------------------------------------
// PATCH /v1/me
// ---------------------------------------------------------------------------

// TestUpdateProfileTimezone is the gap this endpoint closes: timezone decides which
// day "today" is, and an account provisioned with the wrong default previously had
// no way to correct it from any client.
func TestUpdateProfileTimezone(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	before := h.do(http.MethodGet, "/v1/me", u.Token, nil).expect(http.StatusOK).decode()
	if before["timezone"] != "UTC" {
		t.Fatalf("timezone = %v, want the seeded UTC", before["timezone"])
	}

	after := h.do(http.MethodPatch, "/v1/me", u.Token, map[string]any{
		"timezone": "Europe/Paris",
		"name":     "Guillaume",
	}).expect(http.StatusOK).decode()

	if after["timezone"] != "Europe/Paris" {
		t.Errorf("timezone = %v, want Europe/Paris", after["timezone"])
	}

	if after["name"] != "Guillaume" {
		t.Errorf("name = %v, want Guillaume", after["name"])
	}

	// The change is durable and the shape matches GET, so a client can swap what
	// it cached without reconciling two representations.
	reread := h.do(http.MethodGet, "/v1/me", u.Token, nil).expect(http.StatusOK).decode()
	for _, field := range []string{"user_id", "email", "name", "timezone", "auth_via"} {
		if reread[field] != after[field] {
			t.Errorf("%s differs between PATCH and GET: %v vs %v",
				field, after[field], reread[field])
		}
	}

	// And the brief now uses it, which is the whole point.
	brief := h.brief(u, "")
	if brief.Timezone != "Europe/Paris" {
		t.Errorf("brief timezone = %q, want the updated account timezone", brief.Timezone)
	}
}

func TestUpdateProfileValidation(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	cases := map[string]struct {
		body  map[string]any
		field string
	}{
		"unknown timezone": {map[string]any{"timezone": "Mars/Olympus"}, "timezone"},
		"null timezone":    {map[string]any{"timezone": nil}, "timezone"},
		"blank name":       {map[string]any{"name": "   "}, "name"},
		"null name":        {map[string]any{"name": nil}, "name"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := h.do(http.MethodPatch, "/v1/me", u.Token, tc.body)
			res.expect(http.StatusUnprocessableEntity)

			if res.fields()[tc.field] == "" {
				t.Errorf("error names %v, want an entry for %s", res.fields(), tc.field)
			}
		})
	}

	// Email is not editable here: it is the join key for a federated identity, so
	// changing it would move which account owns the data.
	h.do(http.MethodPatch, "/v1/me", u.Token, map[string]any{
		"email": "someone-else@example.com",
	}).expect(http.StatusBadRequest)

	// An empty patch is a no-op, not an error.
	h.do(http.MethodPatch, "/v1/me", u.Token, map[string]any{}).expect(http.StatusOK)
}

func TestUpdateProfileNeedsWriteScope(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	readOnly := h.tokenWithScopes(u, "read")

	h.do(http.MethodPatch, "/v1/me", readOnly, map[string]any{"name": "nope"}).
		expect(http.StatusForbidden)
}

// ---------------------------------------------------------------------------
// blocked_by_id filter
// ---------------------------------------------------------------------------

// TestBlockedByFilter gives the reverse edge: which tasks are waiting on this one.
// Without it, a task detail screen cannot show what it is blocking.
func TestBlockedByFilter(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	blocker := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "the blocker", "context_id": contextID,
	}).expect(http.StatusCreated).id()

	var blocked []string

	for _, title := range []string{"waiting a", "waiting b"} {
		id := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
			"title": title, "context_id": contextID,
		}).expect(http.StatusCreated).id()

		h.do(http.MethodPatch, "/v1/tasks/"+id, u.Token, map[string]any{
			"blocked_by_id": blocker, "status": "blocked",
		}).expect(http.StatusOK)

		blocked = append(blocked, id)
	}

	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "unrelated", "context_id": contextID,
	}).expect(http.StatusCreated)

	items := h.do(http.MethodGet, "/v1/tasks?blocked_by_id="+blocker, u.Token, nil).
		expect(http.StatusOK).list()

	if len(items) != 2 {
		t.Fatalf("blocked_by_id returned %d tasks, want 2", len(items))
	}

	found := map[string]bool{}
	for _, item := range items {
		id, _ := item["id"].(string)
		found[id] = true
	}

	for _, id := range blocked {
		if !found[id] {
			t.Errorf("task %s blocked by the blocker was not returned", id)
		}
	}

	// The complement: tasks with no blocker at all.
	unblocked := h.do(http.MethodGet, "/v1/tasks?blocked_by_id=null", u.Token, nil).
		expect(http.StatusOK).list()

	if len(unblocked) != 2 {
		t.Errorf("blocked_by_id=null returned %d, want the blocker and the unrelated task",
			len(unblocked))
	}
}

// ---------------------------------------------------------------------------
// Context colour
// ---------------------------------------------------------------------------

func TestContextColorValidation(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	created := h.do(http.MethodPost, "/v1/contexts", u.Token, map[string]any{
		"name": "Valid", "color": "#6b46c1",
	}).expect(http.StatusCreated).decode()

	if created["color"] != "#6b46c1" {
		t.Errorf("color = %v", created["color"])
	}

	for name, colour := range map[string]string{
		"three-digit shorthand": "#fff",
		"eight-digit alpha":     "#6b46c1ff",
		"no hash":               "6b46c1",
		"named colour":          "purple",
		"not hex":               "#zzzzzz",
	} {
		t.Run(name, func(t *testing.T) {
			res := h.do(http.MethodPost, "/v1/contexts", u.Token, map[string]any{
				"name": "Ctx " + name, "color": colour,
			})
			res.expect(http.StatusUnprocessableEntity)

			if res.fields()["color"] == "" {
				t.Errorf("error names %v, want an entry for color", res.fields())
			}
		})
	}

	// The seeded contexts must already satisfy the rule, or the app ships data its
	// own API would reject. Checked before anything is cleared below.
	for _, item := range h.do(http.MethodGet, "/v1/contexts", u.Token, nil).
		expect(http.StatusOK).list() {
		colour, _ := item["color"].(string)
		if len(colour) != 7 || colour[0] != '#' {
			t.Errorf("seeded context %v has colour %q, which the API would now reject",
				item["name"], colour)
		}
	}

	contextID, _ := created["id"].(string)

	// Null clears it, which is how a client says "use the fallback".
	cleared := h.do(http.MethodPatch, "/v1/contexts/"+contextID, u.Token, map[string]any{
		"color": nil,
	}).expect(http.StatusOK).decode()

	if cleared["color"] != nil {
		t.Errorf("color = %v after clearing, want null", cleared["color"])
	}

	h.do(http.MethodPatch, "/v1/contexts/"+contextID, u.Token, map[string]any{
		"color": "#abc",
	}).expect(http.StatusUnprocessableEntity)
}

// ---------------------------------------------------------------------------
// Task restore
// ---------------------------------------------------------------------------

func TestRestoreTask(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "deleted by mistake", "context_id": contextID, "due_on": "2026-08-01",
	}).expect(http.StatusCreated).id()

	h.do(http.MethodDelete, "/v1/tasks/"+taskID, u.Token, nil).expect(http.StatusNoContent)
	h.do(http.MethodGet, "/v1/tasks/"+taskID, u.Token, nil).expect(http.StatusNotFound)

	restored := h.do(http.MethodPost, "/v1/tasks/"+taskID+"/restore", u.Token, nil).
		expect(http.StatusOK).decode()

	if restored["title"] != "deleted by mistake" {
		t.Errorf("restored title = %v", restored["title"])
	}

	// Everything it had comes back, not just the row.
	if restored["due_on"] != "2026-08-01" {
		t.Errorf("due_on = %v, want it preserved through the delete", restored["due_on"])
	}

	if restored["context_id"] != contextID {
		t.Errorf("context_id = %v, want it preserved", restored["context_id"])
	}

	h.do(http.MethodGet, "/v1/tasks/"+taskID, u.Token, nil).expect(http.StatusOK)

	// It is back in ordinary listings.
	if items := h.do(http.MethodGet, "/v1/tasks", u.Token, nil).
		expect(http.StatusOK).list(); len(items) != 1 {
		t.Errorf("listing shows %d tasks after restore, want 1", len(items))
	}
}

// TestRestoreBringsBackTheSubtree checks the grouping: a delete tombstones a whole
// subtree, so a restore has to bring the same set back rather than one row.
func TestRestoreBringsBackTheSubtree(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	mk := func(title string, parent string) string {
		body := map[string]any{"title": title, "context_id": contextID}
		if parent != "" {
			body["parent_id"] = parent
		}

		return h.do(http.MethodPost, "/v1/tasks", u.Token, body).
			expect(http.StatusCreated).id()
	}

	parent := mk("parent", "")
	child := mk("child", parent)
	grandchild := mk("grandchild", child)

	h.do(http.MethodDelete, "/v1/tasks/"+parent, u.Token, nil).expect(http.StatusNoContent)

	h.do(http.MethodPost, "/v1/tasks/"+parent+"/restore", u.Token, nil).
		expect(http.StatusOK)

	for label, id := range map[string]string{
		"parent": parent, "child": child, "grandchild": grandchild,
	} {
		h.do(http.MethodGet, "/v1/tasks/"+id, u.Token, nil).expect(http.StatusOK)

		_ = label
	}

	// The tree is intact, not flattened.
	body := h.do(http.MethodGet, "/v1/tasks/"+grandchild, u.Token, nil).
		expect(http.StatusOK).decode()

	if body["parent_id"] != child {
		t.Errorf("grandchild parent_id = %v, want %v", body["parent_id"], child)
	}
}

// TestRestoreOnlyUndoesOneDelete is the scoping rule: a child deleted separately
// and earlier must stay deleted, or restoring a parent silently resurrects work the
// user removed deliberately.
func TestRestoreOnlyUndoesOneDelete(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	parent := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "parent", "context_id": contextID,
	}).expect(http.StatusCreated).id()

	earlier := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "deleted earlier, on purpose", "context_id": contextID, "parent_id": parent,
	}).expect(http.StatusCreated).id()

	later := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "went down with the parent", "context_id": contextID, "parent_id": parent,
	}).expect(http.StatusCreated).id()

	// Two separate deletes.
	h.do(http.MethodDelete, "/v1/tasks/"+earlier, u.Token, nil).expect(http.StatusNoContent)
	h.do(http.MethodDelete, "/v1/tasks/"+parent, u.Token, nil).expect(http.StatusNoContent)

	h.do(http.MethodPost, "/v1/tasks/"+parent+"/restore", u.Token, nil).expect(http.StatusOK)

	h.do(http.MethodGet, "/v1/tasks/"+later, u.Token, nil).expect(http.StatusOK)

	if res := h.do(http.MethodGet, "/v1/tasks/"+earlier, u.Token, nil); res.Status != http.StatusNotFound {
		t.Errorf("the separately deleted child came back (status %d); a restore should "+
			"undo one delete, not every delete", res.Status)
	}
}

// TestRestoreRepairsStaleReferences covers the awkward case: the world moved while
// the task was away, so restoring it verbatim would produce an incoherent row.
func TestRestoreRepairsStaleReferences(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	projectID := h.do(http.MethodPost, "/v1/projects", u.Token, map[string]any{
		"context_id": contextID, "name": "doomed project",
	}).expect(http.StatusCreated).id()

	personID := h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{
		"name": "Doomed",
	}).expect(http.StatusCreated).id()

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "will outlive its references", "context_id": contextID,
		"project_id": projectID, "status": "delegated", "delegated_to_id": personID,
	}).expect(http.StatusCreated).id()

	h.do(http.MethodDelete, "/v1/tasks/"+taskID, u.Token, nil).expect(http.StatusNoContent)

	// Now remove everything it pointed at.
	h.do(http.MethodDelete, "/v1/people/"+personID, u.Token, nil).expect(http.StatusNoContent)
	h.do(http.MethodDelete, "/v1/contexts/"+contextID, u.Token, nil).expect(http.StatusNoContent)

	restored := h.do(http.MethodPost, "/v1/tasks/"+taskID+"/restore", u.Token, nil).
		expect(http.StatusOK).decode()

	// Back in the inbox rather than pointing at a deleted context.
	if restored["context_id"] != nil {
		t.Errorf("context_id = %v, want null after its context was deleted", restored["context_id"])
	}

	if restored["project_id"] != nil {
		t.Errorf("project_id = %v, want null", restored["project_id"])
	}

	// The delegate is gone, so the status cannot still be delegated: the schema
	// forbids it, and leaving it would make the row unwritable.
	if restored["delegated_to_id"] != nil {
		t.Errorf("delegated_to_id = %v, want null", restored["delegated_to_id"])
	}

	if restored["status"] != "todo" {
		t.Errorf("status = %v, want todo once the delegate was gone", restored["status"])
	}
}

func TestRestoreRejections(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	other := h.user("other@example.com")

	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "alive", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated).id()

	// Restoring something that is not deleted is a validation error, not a
	// failure: the caller wanted it alive and it is.
	res := h.do(http.MethodPost, "/v1/tasks/"+taskID+"/restore", u.Token, nil)
	res.expect(http.StatusUnprocessableEntity)

	if res.fields()["id"] == "" {
		t.Errorf("error names %v, want an entry for id", res.fields())
	}

	h.do(http.MethodPost, "/v1/tasks/does-not-exist/restore", u.Token, nil).
		expect(http.StatusNotFound)

	// Another user's deleted task is not restorable, and is reported as missing.
	h.do(http.MethodDelete, "/v1/tasks/"+taskID, u.Token, nil).expect(http.StatusNoContent)
	h.do(http.MethodPost, "/v1/tasks/"+taskID+"/restore", other.Token, nil).
		expect(http.StatusNotFound)

	// Read-only cannot restore.
	readOnly := h.tokenWithScopes(u, "read")
	h.do(http.MethodPost, "/v1/tasks/"+taskID+"/restore", readOnly, nil).
		expect(http.StatusForbidden)
}

// ---------------------------------------------------------------------------
// Person merge
// ---------------------------------------------------------------------------

// TestMergePeople is the way back from the duplicates that delegate-by-name
// inevitably creates.
func TestMergePeople(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	// Two spellings of the same person, both created by fast capture.
	first := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "ask about pricing", "context_id": contextID,
		"delegated_to": "Marc", "status": "delegated",
	}).expect(http.StatusCreated).decode()

	second := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "chase the invoice", "context_id": contextID,
		"delegated_to": "Marc D.", "status": "delegated",
	}).expect(http.StatusCreated).decode()

	keepID, _ := first["delegated_to_id"].(string)
	dropID, _ := second["delegated_to_id"].(string)

	if keepID == dropID {
		t.Fatal("the two names resolved to one person; nothing to merge")
	}

	result := h.do(http.MethodPost, "/v1/people/"+dropID+"/merge", u.Token, map[string]any{
		"into": keepID,
	}).expect(http.StatusOK).decode()

	if moved, _ := result["tasks_moved"].(float64); moved != 1 {
		t.Errorf("tasks_moved = %v, want 1", result["tasks_moved"])
	}

	// The duplicate is gone and both tasks now point at the survivor.
	h.do(http.MethodGet, "/v1/people/"+dropID, u.Token, nil).expect(http.StatusNotFound)

	people := h.do(http.MethodGet, "/v1/people", u.Token, nil).expect(http.StatusOK).list()
	if len(people) != 1 {
		t.Fatalf("people = %d after merge, want 1", len(people))
	}

	for _, title := range []string{"ask about pricing", "chase the invoice"} {
		items := h.do(http.MethodGet, "/v1/tasks?q="+urlEscape(title), u.Token, nil).
			expect(http.StatusOK).list()

		if len(items) != 1 {
			t.Fatalf("expected to find %q", title)
		}

		if items[0]["delegated_to_id"] != keepID {
			t.Errorf("%q points at %v, want the surviving person %v",
				title, items[0]["delegated_to_id"], keepID)
		}

		// Still delegated: the merge never left a task without a delegate, which
		// the schema would have refused.
		if items[0]["status"] != "delegated" {
			t.Errorf("%q status = %v, want delegated", title, items[0]["status"])
		}
	}

	// And the brief groups both under one person now.
	brief := h.brief(u, "?timezone=UTC")
	if len(brief.WaitingOn) != 1 {
		t.Errorf("waiting_on has %d groups after merge, want 1", len(brief.WaitingOn))
	}

	if brief.Totals.WaitingOn != 2 {
		t.Errorf("waiting_on total = %d, want both tasks", brief.Totals.WaitingOn)
	}
}

func TestMergePeopleRejections(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	other := h.user("other@example.com")

	a := h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{"name": "A"}).
		expect(http.StatusCreated).id()
	b := h.do(http.MethodPost, "/v1/people", u.Token, map[string]any{"name": "B"}).
		expect(http.StatusCreated).id()

	theirs := h.do(http.MethodPost, "/v1/people", other.Token, map[string]any{"name": "Theirs"}).
		expect(http.StatusCreated).id()

	t.Run("into is required", func(t *testing.T) {
		res := h.do(http.MethodPost, "/v1/people/"+a+"/merge", u.Token, map[string]any{})
		res.expect(http.StatusUnprocessableEntity)

		if res.fields()["into"] == "" {
			t.Errorf("error names %v, want into", res.fields())
		}
	})

	t.Run("cannot merge into self", func(t *testing.T) {
		res := h.do(http.MethodPost, "/v1/people/"+a+"/merge", u.Token,
			map[string]any{"into": a})
		res.expect(http.StatusUnprocessableEntity)

		if res.fields()["into"] == "" {
			t.Errorf("error names %v, want into", res.fields())
		}
	})

	t.Run("target must exist", func(t *testing.T) {
		res := h.do(http.MethodPost, "/v1/people/"+a+"/merge", u.Token,
			map[string]any{"into": "nobody"})
		res.expect(http.StatusUnprocessableEntity)

		if res.fields()["into"] == "" {
			t.Errorf("error names %v, want into", res.fields())
		}
	})

	t.Run("cannot merge across accounts", func(t *testing.T) {
		// Neither direction: the other user's person must be invisible.
		h.do(http.MethodPost, "/v1/people/"+a+"/merge", u.Token,
			map[string]any{"into": theirs}).expect(http.StatusUnprocessableEntity)

		h.do(http.MethodPost, "/v1/people/"+theirs+"/merge", u.Token,
			map[string]any{"into": a}).expect(http.StatusNotFound)

		// The other user still has their person.
		h.do(http.MethodGet, "/v1/people/"+theirs, other.Token, nil).expect(http.StatusOK)
	})

	t.Run("read-only cannot merge", func(t *testing.T) {
		readOnly := h.tokenWithScopes(u, "read")

		h.do(http.MethodPost, "/v1/people/"+a+"/merge", readOnly,
			map[string]any{"into": b}).expect(http.StatusForbidden)
	})
}
