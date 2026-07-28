package httpapi_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestTaskActivityIsScopedAndPaginated(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")
	contextID := h.firstContextID(alice)

	first := h.do(http.MethodPost, "/v1/tasks", alice.Token, map[string]any{
		"title": "first", "context_id": contextID,
	}).expect(http.StatusCreated).id()
	h.do(http.MethodPatch, "/v1/tasks/"+first, alice.Token, map[string]any{
		"status": "done",
	}).expect(http.StatusOK)
	h.do(http.MethodPost, "/v1/tasks", alice.Token, map[string]any{
		"title": "second", "context_id": contextID,
	}).expect(http.StatusCreated)
	h.do(http.MethodPost, "/v1/tasks", bob.Token, map[string]any{
		"title": "bob's task", "context_id": h.firstContextID(bob),
	}).expect(http.StatusCreated)

	type activityPage struct {
		Data []struct {
			TaskTitle     string   `json:"task_title"`
			Action        string   `json:"action"`
			ChangedFields []string `json:"changed_fields"`
			StatusBefore  *string  `json:"status_before"`
			StatusAfter   *string  `json:"status_after"`
		} `json:"data"`
		NextCursor *string `json:"next_cursor"`
	}

	var page1 activityPage
	h.do(http.MethodGet, "/v1/activity?limit=2", alice.Token, nil).
		expect(http.StatusOK).decodeInto(&page1)

	if len(page1.Data) != 2 || page1.NextCursor == nil {
		t.Fatalf("first page = %#v, want two rows and a cursor", page1)
	}
	if page1.Data[0].TaskTitle != "second" || page1.Data[0].Action != "created" {
		t.Errorf("newest activity = %#v, want second created", page1.Data[0])
	}
	if page1.Data[1].TaskTitle != "first" || page1.Data[1].Action != "updated" {
		t.Errorf("next activity = %#v, want first updated", page1.Data[1])
	}
	if len(page1.Data[1].ChangedFields) != 1 || page1.Data[1].ChangedFields[0] != "status" {
		t.Errorf("changed fields = %v, want [status]", page1.Data[1].ChangedFields)
	}
	if page1.Data[1].StatusBefore == nil || *page1.Data[1].StatusBefore != "todo" ||
		page1.Data[1].StatusAfter == nil || *page1.Data[1].StatusAfter != "done" {
		t.Errorf("status change = %v -> %v, want todo -> done",
			page1.Data[1].StatusBefore, page1.Data[1].StatusAfter)
	}

	var page2 activityPage
	h.do(
		http.MethodGet,
		"/v1/activity?limit=2&cursor="+url.QueryEscape(*page1.NextCursor),
		alice.Token,
		nil,
	).expect(http.StatusOK).decodeInto(&page2)

	if len(page2.Data) != 1 || page2.NextCursor != nil {
		t.Fatalf("second page = %#v, want one final row", page2)
	}
	if page2.Data[0].TaskTitle != "first" || page2.Data[0].Action != "created" {
		t.Errorf("oldest activity = %#v, want first created", page2.Data[0])
	}
}

func TestTaskActivityRejectsInvalidCursor(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	res := h.do(http.MethodGet, "/v1/activity?cursor=not-a-cursor", u.Token, nil).
		expect(http.StatusUnprocessableEntity)
	if res.fields()["cursor"] == "" {
		t.Errorf("invalid cursor errors = %v, want cursor", res.fields())
	}
}
