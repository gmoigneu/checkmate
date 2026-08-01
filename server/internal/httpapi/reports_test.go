package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nls/checkmate/server/internal/config"
)

func TestReportPreviewReconstructsEndStateAndClassifiesWork(t *testing.T) {
	h := newHarness(t)
	u := h.user("historical-report@example.com")
	selectedContext := h.firstContextID(u)
	otherContext := h.createContextID(u, "Other context")

	setLastActivity := func(taskID, occurredAt string) {
		t.Helper()
		result, err := h.store.DB().ExecContext(context.Background(), `
			UPDATE task_activity SET occurred_at = ?
			WHERE id = (SELECT max(id) FROM task_activity WHERE task_id = ?)`,
			occurredAt, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			t.Fatalf("changed %d activity rows for %s", changed, taskID)
		}
	}
	create := func(title, createdAt string, extra map[string]any) string {
		t.Helper()
		body := map[string]any{"title": title, "context_id": selectedContext}
		for key, value := range extra {
			body[key] = value
		}
		id := h.do(http.MethodPost, "/v1/tasks", u.Token, body).
			expect(http.StatusCreated).id()
		setLastActivity(id, createdAt)
		return id
	}
	patch := func(taskID, occurredAt string, body map[string]any) {
		t.Helper()
		h.do(http.MethodPatch, "/v1/tasks/"+taskID, u.Token, body).
			expect(http.StatusOK)
		setLastActivity(taskID, occurredAt)
	}

	completedDeleted := create("Completed then deleted", "2026-07-01T10:00:00.000Z", nil)
	patch(completedDeleted, "2026-07-02T10:00:00.000Z", map[string]any{"status": "done"})
	h.do(http.MethodDelete, "/v1/tasks/"+completedDeleted, u.Token, nil).
		expect(http.StatusNoContent)
	setLastActivity(completedDeleted, "2026-07-03T10:00:00.000Z")

	openDeleted := create("Open then deleted", "2026-07-01T11:00:00.000Z", nil)
	h.do(http.MethodDelete, "/v1/tasks/"+openDeleted, u.Token, nil).
		expect(http.StatusNoContent)
	setLastActivity(openDeleted, "2026-07-04T10:00:00.000Z")

	reopened := create("Reopened work", "2026-07-01T12:00:00.000Z", nil)
	patch(reopened, "2026-07-02T12:00:00.000Z", map[string]any{"status": "done"})
	patch(reopened, "2026-07-05T12:00:00.000Z", map[string]any{"status": "todo"})

	create("Due in range", "2026-06-20T10:00:00.000Z", map[string]any{"due_on": "2026-07-06"})
	create("Irrelevant backlog", "2026-06-01T10:00:00.000Z", map[string]any{"due_on": "2026-08-20"})
	create("Blocked work", "2026-07-02T09:00:00.000Z", map[string]any{"status": "blocked"})
	movedOut := create("Moved elsewhere", "2026-07-02T08:00:00.000Z", nil)
	patch(movedOut, "2026-07-06T08:00:00.000Z", map[string]any{"context_id": otherContext})

	preview := h.do(http.MethodPost, "/v1/reports/preview", u.Token, map[string]any{
		"start_on": "2026-07-01", "end_on": "2026-07-07",
		"context_ids": []string{selectedContext}, "include_inbox": false,
	}).expect(http.StatusOK).decode()
	metrics := preview["metrics"].(map[string]any)
	for key, want := range map[string]float64{
		"completed": 1, "open": 3, "blocked": 1, "dropped": 1,
	} {
		if metrics[key] != want {
			t.Fatalf("metrics[%s] = %v, want %v; all metrics: %#v", key, metrics[key], want, metrics)
		}
	}
	tasks := preview["tasks"].([]any)
	if len(tasks) != 5 {
		t.Fatalf("preview tasks = %#v", tasks)
	}
	categories := map[string]string{}
	for _, raw := range tasks {
		task := raw.(map[string]any)
		categories[task["title"].(string)] = task["category"].(string)
	}
	for title, want := range map[string]string{
		"Completed then deleted": "completed", "Open then deleted": "dropped",
		"Reopened work": "pending", "Due in range": "pending", "Blocked work": "blocked",
	} {
		if categories[title] != want {
			t.Fatalf("category for %q = %q, want %q; all: %#v", title, categories[title], want, categories)
		}
	}
	if _, ok := categories["Irrelevant backlog"]; ok {
		t.Fatal("irrelevant backlog was included")
	}
	if _, ok := categories["Moved elsewhere"]; ok {
		t.Fatal("task outside the selected context at the end boundary was included")
	}
}

func TestReportWorkflowUsesValidatedPrivateSourceReferences(t *testing.T) {
	var (
		mu       sync.Mutex
		requests [][]byte
	)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("provider path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-openrouter-key" {
			t.Errorf("provider authorization = %q", r.Header.Get("Authorization"))
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(request)
		mu.Lock()
		requests = append(requests, raw)
		mu.Unlock()

		report := map[string]any{
			"summary": "I completed the selected work and have a clear next step.",
			"completed": []any{map[string]any{
				"text": "I completed the launch task.", "source_ids": []string{"S1"},
			}},
			"pending": []any{}, "blockers": []any{}, "delegated": []any{},
			"dropped": []any{}, "triage": []any{},
			"priorities": []any{},
		}
		content, _ := json.Marshal(report)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}},
			"usage":   map[string]any{"prompt_tokens": 321, "completion_tokens": 123},
		})
	}))
	defer provider.Close()

	h := newHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.OpenRouterAPIKey = "test-openrouter-key"
		cfg.OpenRouterBaseURL = provider.URL
		cfg.ReportGenerationTimeout = 5 * time.Second
	})
	u := h.user("reporter@example.com")
	contextID := h.firstContextID(u)
	taskID := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "Ship the launch", "details": "Customer-ready rollout",
		"context_id": contextID, "priority": "high",
	}).expect(http.StatusCreated).id()
	h.do(http.MethodPatch, "/v1/tasks/"+taskID, u.Token, map[string]any{
		"status": "done",
	}).expect(http.StatusOK)

	today := time.Now().UTC().Format("2006-01-02")
	request := map[string]any{
		"start_on": today, "end_on": today, "context_ids": []string{contextID},
		"include_inbox": false, "focus": "Keep it useful",
	}
	preview := h.do(http.MethodPost, "/v1/reports/preview", u.Token, request).
		expect(http.StatusOK).decode()
	metrics := preview["metrics"].(map[string]any)
	if metrics["completed"] != float64(1) {
		t.Fatalf("preview metrics = %#v", metrics)
	}

	generated := h.do(http.MethodPost, "/v1/reports/generate", u.Token, request).
		expect(http.StatusCreated).decode()
	reportID := generated["id"].(string)
	versions := generated["versions"].([]any)
	if len(versions) != 1 {
		t.Fatalf("versions = %#v", versions)
	}
	content := versions[0].(map[string]any)["content_markdown"].(string)
	if !strings.Contains(content, "[Ship the launch](/t/"+taskID+")") {
		t.Fatalf("generated markdown has no validated task link: %s", content)
	}

	mu.Lock()
	firstRequest := string(requests[0])
	mu.Unlock()
	if strings.Contains(firstRequest, taskID) || strings.Contains(firstRequest, u.Email) {
		t.Fatalf("external request leaked an internal id or email: %s", firstRequest)
	}
	if !strings.Contains(firstRequest, `"model":"deepseek/deepseek-v4-flash"`) ||
		!strings.Contains(firstRequest, `"response_format"`) {
		t.Fatalf("external request lacks fixed model or schema: %s", firstRequest)
	}

	edited := content + "\nMy meeting note.\n"
	updated := h.do(http.MethodPatch, "/v1/reports/"+reportID, u.Token, map[string]any{
		"title": "Launch one-on-one", "content_markdown": edited, "version_number": 1,
	}).expect(http.StatusOK).decode()
	if updated["title"] != "Launch one-on-one" {
		t.Fatalf("updated report = %#v", updated)
	}

	regenerated := h.do(http.MethodPost, "/v1/reports/"+reportID+"/regenerate", u.Token, nil).
		expect(http.StatusCreated).decode()
	if regenerated["latest_version"] != float64(2) || len(regenerated["versions"].([]any)) != 2 {
		t.Fatalf("regenerated report = %#v", regenerated)
	}

	list := h.do(http.MethodGet, "/v1/reports", u.Token, nil).expect(http.StatusOK).list()
	if len(list) != 1 || list[0]["id"] != reportID {
		t.Fatalf("report list = %#v", list)
	}
	h.do(http.MethodDelete, "/v1/reports/"+reportID, u.Token, nil).
		expect(http.StatusNoContent)
	h.do(http.MethodGet, "/v1/reports/"+reportID, u.Token, nil).
		expect(http.StatusNotFound)
}

func TestReportGenerationRequiresConfiguration(t *testing.T) {
	h := newHarness(t)
	u := h.user("no-model@example.com")
	today := time.Now().UTC().Format("2006-01-02")
	h.do(http.MethodPost, "/v1/reports/generate", u.Token, map[string]any{
		"start_on": today, "end_on": today,
		"context_ids": []string{h.firstContextID(u)},
	}).expect(http.StatusServiceUnavailable)
}

func TestReportGenerationRetriesInvalidSourcesWithoutSaving(t *testing.T) {
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		invalid := map[string]any{
			"summary": "Unverifiable", "completed": []any{map[string]any{
				"text": "Invented", "source_ids": []string{"S999"},
			}},
			"pending": []any{}, "blockers": []any{}, "delegated": []any{},
			"dropped": []any{}, "triage": []any{}, "priorities": []any{},
		}
		content, _ := json.Marshal(invalid)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}},
		})
	}))
	defer provider.Close()

	h := newHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.OpenRouterAPIKey = "test-key"
		cfg.OpenRouterBaseURL = provider.URL
		cfg.ReportGenerationTimeout = 5 * time.Second
	})
	u := h.user("invalid-report@example.com")
	contextID := h.firstContextID(u)
	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "Real task", "context_id": contextID, "status": "done",
	}).expect(http.StatusCreated)
	today := time.Now().UTC().Format("2006-01-02")
	h.do(http.MethodPost, "/v1/reports/generate", u.Token, map[string]any{
		"start_on": today, "end_on": today, "context_ids": []string{contextID},
	}).expect(http.StatusBadGateway)
	if requests != 2 {
		t.Fatalf("provider requests = %d, want one strict retry", requests)
	}
	if got := h.do(http.MethodGet, "/v1/reports", u.Token, nil).expect(http.StatusOK).list(); len(got) != 0 {
		t.Fatalf("invalid report was saved: %#v", got)
	}
}
