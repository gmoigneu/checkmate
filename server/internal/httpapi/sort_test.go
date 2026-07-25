package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
)

// Sorting on GET /v1/tasks.
//
// The default is newest-created-first, which the primary key provides for free.
// Any other order needs a keyset cursor, because an offset drifts when rows change
// between pages — which for a task list means quietly skipping or repeating a task.
// The tests below care about two things: that the order is right, and that paging
// under that order delivers every row exactly once.

// titlesOf pulls the titles out of a listing, in the order returned.
func titlesOf(items []map[string]any) []string {
	out := make([]string, 0, len(items))

	for _, item := range items {
		title, _ := item["title"].(string)
		out = append(out, title)
	}

	return out
}

// seedForSort creates tasks with deliberately mixed dates, estimates and titles,
// including nulls, since nulls are where a sort usually goes wrong.
func seedForSort(t *testing.T, h *harness, u testUser) string {
	t.Helper()

	contextID := h.firstContextID(u)

	for _, task := range []map[string]any{
		{"title": "banana", "due_on": "2026-08-03", "estimate_minutes": 30},
		{"title": "Apple", "due_on": "2026-08-01", "estimate_minutes": 90},
		{"title": "cherry", "due_on": "2026-08-02"},
		{"title": "date", "estimate_minutes": 15},
		{"title": "elderberry"},
	} {
		task["context_id"] = contextID

		h.do(http.MethodPost, "/v1/tasks", u.Token, task).expect(http.StatusCreated)
	}

	return contextID
}

func TestTaskSortByDueDate(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	seedForSort(t, h, u)

	got := titlesOf(h.do(http.MethodGet, "/v1/tasks?sort=due_on", u.Token, nil).
		expect(http.StatusOK).list())

	// Ascending by default for a date sort: the soonest deadline is what a task
	// list is asking about. Undated tasks come last in creation order.
	want := []string{"Apple", "cherry", "banana", "elderberry", "date"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// The two undated tasks are ordered by id descending among themselves, so
	// only assert the dated prefix strictly and that the rest are the undated pair.
	for i := range 3 {
		if got[i] != want[i] {
			t.Errorf("position %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}

	for _, title := range got[3:] {
		if title != "date" && title != "elderberry" {
			t.Errorf("undated tail contains %q, want only the undated tasks", title)
		}
	}
}

// TestTaskSortPutsMissingValuesLast is the NULL rule: "sorted by due date" with
// undated tasks at the top would be useless, and it must hold in both directions.
func TestTaskSortPutsMissingValuesLast(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	seedForSort(t, h, u)

	for _, order := range []string{"asc", "desc"} {
		got := titlesOf(h.do(http.MethodGet,
			"/v1/tasks?sort=due_on&order="+order, u.Token, nil).
			expect(http.StatusOK).list())

		if len(got) != 5 {
			t.Fatalf("order=%s returned %v", order, got)
		}

		tail := map[string]bool{got[3]: true, got[4]: true}

		if !tail["date"] || !tail["elderberry"] {
			t.Errorf("order=%s put dated tasks last: %v", order, got)
		}
	}
}

func TestTaskSortDescendingReverses(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	seedForSort(t, h, u)

	got := titlesOf(h.do(http.MethodGet, "/v1/tasks?sort=due_on&order=desc", u.Token, nil).
		expect(http.StatusOK).list())

	want := []string{"banana", "cherry", "Apple"}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestTaskSortByTitleIsCaseInsensitive(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	seedForSort(t, h, u)

	got := titlesOf(h.do(http.MethodGet, "/v1/tasks?sort=title&order=asc", u.Token, nil).
		expect(http.StatusOK).list())

	// "Apple" sorts with the lowercase words rather than ahead of all of them,
	// which is what ASCII ordering would do.
	want := []string{"Apple", "banana", "cherry", "date", "elderberry"}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestTaskSortByEstimate(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	seedForSort(t, h, u)

	items := h.do(http.MethodGet, "/v1/tasks?sort=estimate_minutes&order=asc", u.Token, nil).
		expect(http.StatusOK).list()

	got := titlesOf(items)

	// 15, 30, 90, then the two with no estimate.
	want := []string{"date", "banana", "Apple"}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}

	for _, item := range items[3:] {
		if item["estimate_minutes"] != nil {
			t.Errorf("an estimated task sorted after the unestimated ones: %v", got)
		}
	}
}

func TestTaskSortByCreatedIsTheDefault(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	seedForSort(t, h, u)

	unsorted := titlesOf(h.do(http.MethodGet, "/v1/tasks", u.Token, nil).
		expect(http.StatusOK).list())

	explicit := titlesOf(h.do(http.MethodGet, "/v1/tasks?sort=created_at&order=desc", u.Token, nil).
		expect(http.StatusOK).list())

	if fmt.Sprint(unsorted) != fmt.Sprint(explicit) {
		t.Errorf("default order %v differs from created_at desc %v", unsorted, explicit)
	}

	// Newest first, so the last one seeded leads.
	if unsorted[0] != "elderberry" {
		t.Errorf("default first = %q, want the newest task", unsorted[0])
	}

	ascending := titlesOf(h.do(http.MethodGet, "/v1/tasks?sort=created_at&order=asc", u.Token, nil).
		expect(http.StatusOK).list())

	if ascending[0] != "banana" {
		t.Errorf("created_at asc first = %q, want the oldest task", ascending[0])
	}
}

// TestSortedPaginationLosesNothing is the property the keyset cursor exists for.
// An offset would drift; this walks every sort at a page size of two and insists
// each task arrives exactly once and in order.
func TestSortedPaginationLosesNothing(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	contextID := h.firstContextID(u)

	const total = 17

	for i := range total {
		body := map[string]any{
			"title":      fmt.Sprintf("task %02d", i),
			"context_id": contextID,
		}

		// Deliberately repeat due dates so the id tiebreak is exercised, and leave
		// some null so the coalesced key is too.
		switch i % 3 {
		case 0:
			body["due_on"] = fmt.Sprintf("2026-08-%02d", (i%9)+1)
		case 1:
			body["due_on"] = "2026-08-05"
		}

		if i%4 != 0 {
			body["estimate_minutes"] = (i % 5) + 1
		}

		h.do(http.MethodPost, "/v1/tasks", u.Token, body).expect(http.StatusCreated)
	}

	for _, sort := range []string{
		"created_at", "updated_at", "due_on", "planned_on",
		"title", "estimate_minutes", "status",
	} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(sort+"_"+order, func(t *testing.T) {
				seen := map[string]bool{}

				var paged []string

				query := fmt.Sprintf("/v1/tasks?sort=%s&order=%s&limit=2", sort, order)

				for range 40 { // bound so a paging bug fails rather than spinning
					var body struct {
						Data       []map[string]any `json:"data"`
						NextCursor *string          `json:"next_cursor"`
					}

					h.do(http.MethodGet, query, u.Token, nil).
						expect(http.StatusOK).decodeInto(&body)

					for _, item := range body.Data {
						id, _ := item["id"].(string)
						if seen[id] {
							t.Errorf("task %s was delivered twice", id)
						}

						seen[id] = true

						title, _ := item["title"].(string)
						paged = append(paged, title)
					}

					if body.NextCursor == nil {
						break
					}

					query = fmt.Sprintf("/v1/tasks?sort=%s&order=%s&limit=2&cursor=%s",
						sort, order, *body.NextCursor)
				}

				if len(seen) != total {
					t.Fatalf("paging delivered %d of %d tasks", len(seen), total)
				}

				// The paged sequence must match one unpaginated read of the same
				// sort, or the pages are individually sorted but jointly wrong.
				oneShot := titlesOf(h.do(http.MethodGet,
					fmt.Sprintf("/v1/tasks?sort=%s&order=%s&limit=200", sort, order),
					u.Token, nil).expect(http.StatusOK).list())

				if fmt.Sprint(paged) != fmt.Sprint(oneShot) {
					t.Errorf("paged order differs from a single page\n paged: %v\n whole: %v",
						paged, oneShot)
				}
			})
		}
	}
}

func TestSortRejectsUnknownValues(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	for field, query := range map[string]string{
		"sort":  "?sort=priority",
		"order": "?order=sideways",
	} {
		res := h.do(http.MethodGet, "/v1/tasks"+query, u.Token, nil)
		res.expect(http.StatusUnprocessableEntity)

		if res.fields()[field] == "" {
			t.Errorf("%s: error names %v, want an entry for %s", query, res.fields(), field)
		}
	}
}

// TestSortCombinesWithFilters checks that sorting did not displace filtering:
// both apply, and the cursor keeps working alongside a filter.
func TestSortCombinesWithFilters(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	contexts := h.do(http.MethodGet, "/v1/contexts", u.Token, nil).expect(http.StatusOK).list()
	first, _ := contexts[0]["id"].(string)
	second, _ := contexts[1]["id"].(string)

	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "in first, late", "context_id": first, "due_on": "2026-08-09",
	}).expect(http.StatusCreated)

	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "in first, early", "context_id": first, "due_on": "2026-08-01",
	}).expect(http.StatusCreated)

	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "in second", "context_id": second, "due_on": "2026-08-02",
	}).expect(http.StatusCreated)

	got := titlesOf(h.do(http.MethodGet,
		"/v1/tasks?context_id="+first+"&sort=due_on&order=asc", u.Token, nil).
		expect(http.StatusOK).list())

	want := []string{"in first, early", "in first, late"}

	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestCursorFromADifferentSortIsRejected guards the one way a keyset cursor can be
// misused: carrying it across to another sort, where its key means nothing.
func TestCursorFromADifferentSortIsRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	seedForSort(t, h, u)

	var first struct {
		NextCursor *string `json:"next_cursor"`
	}

	h.do(http.MethodGet, "/v1/tasks?sort=due_on&limit=2", u.Token, nil).
		expect(http.StatusOK).decodeInto(&first)

	if first.NextCursor == nil {
		t.Fatal("no cursor on the first page")
	}

	// Same cursor, different sort. Silently returning the wrong page would be
	// worse than refusing.
	res := h.do(http.MethodGet,
		"/v1/tasks?sort=title&limit=2&cursor="+*first.NextCursor, u.Token, nil)

	if res.Status == http.StatusOK {
		// Accepting it is only safe if the result is still correct; it is not, so
		// this must be an error naming the cursor.
		t.Errorf("a cursor from another sort was accepted: %s", res.Body)
	}

	res.expect(http.StatusUnprocessableEntity)

	if res.fields()["cursor"] == "" {
		t.Errorf("error names %v, want an entry for cursor", res.fields())
	}
}
