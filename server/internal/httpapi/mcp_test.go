package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// MCP test helpers
//
// These drive the real endpoint over HTTP with real JSON-RPC bodies rather than
// calling tool functions directly, so the transport, the SDK's dispatch, the
// bearer middleware and the scope checks are all in the path being tested.
// ---------------------------------------------------------------------------

const protocolVersion = "2025-11-25"

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// rpc sends one JSON-RPC message to /mcp and returns the raw HTTP response.
func (h *harness) rpc(token, method string, id any, params any) response {
	h.t.Helper()

	body := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		body["id"] = id
	}

	if params != nil {
		body["params"] = params
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		h.t.Fatalf("marshal rpc body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(encoded)))
	req.Header.Set("Content-Type", "application/json")

	// The spec requires clients to accept both, and the SDK checks.
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return h.send(req)
}

// decodeRPC pulls the JSON-RPC envelope out of a response, handling both the
// plain JSON and SSE framings the transport is allowed to use.
func decodeRPC(t *testing.T, res response) rpcResponse {
	t.Helper()

	payload := res.Body

	if strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		payload = nil

		for _, line := range strings.Split(string(res.Body), "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				payload = []byte(data)
			}
		}

		if payload == nil {
			t.Fatalf("no data event in the SSE stream: %s", res.Body)
		}
	}

	var out rpcResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode rpc response %q: %v", payload, err)
	}

	return out
}

// initialize performs the handshake and returns the negotiated result.
func (h *harness) initialize(token string) map[string]any {
	h.t.Helper()

	res := h.rpc(token, "initialize", 1, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "1.0.0"},
	}).expect(http.StatusOK)

	envelope := decodeRPC(h.t, res)
	if envelope.Error != nil {
		h.t.Fatalf("initialize failed: %+v", envelope.Error)
	}

	var result map[string]any
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		h.t.Fatalf("decode initialize result: %v", err)
	}

	return result
}

// callTool invokes a tool and returns the CallToolResult.
func (h *harness) callTool(token, name string, args map[string]any) map[string]any {
	h.t.Helper()

	res := h.rpc(token, "tools/call", 2, map[string]any{
		"name":      name,
		"arguments": args,
	}).expect(http.StatusOK)

	envelope := decodeRPC(h.t, res)
	if envelope.Error != nil {
		h.t.Fatalf("tools/call %s returned a protocol error: %+v", name, envelope.Error)
	}

	var result map[string]any
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		h.t.Fatalf("decode %s result: %v", name, err)
	}

	return result
}

// structured returns a tool result's structuredContent, failing if the call
// reported a tool error.
func structured(t *testing.T, result map[string]any) map[string]any {
	t.Helper()

	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("tool reported an error: %s", resultText(result))
	}

	content, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("no structuredContent in result: %+v", result)
	}

	return content
}

// resultText concatenates the text content blocks.
func resultText(result map[string]any) string {
	blocks, _ := result["content"].([]any)

	var sb strings.Builder

	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if text, ok := block["text"].(string); ok {
			sb.WriteString(text)
		}
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// Transport and lifecycle
// ---------------------------------------------------------------------------

func TestMCPInitialize(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	result := h.initialize(u.Token)

	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, want %s", result["protocolVersion"], protocolVersion)
	}

	capabilities, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %v, want an object", result["capabilities"])
	}

	// A server exposing tools MUST declare the tools capability.
	if _, declared := capabilities["tools"]; !declared {
		t.Errorf("capabilities = %v, want the tools capability declared", capabilities)
	}

	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo = %v, want an object", result["serverInfo"])
	}

	if serverInfo["name"] != "checkmate" {
		t.Errorf("serverInfo.name = %v, want checkmate", serverInfo["name"])
	}

	// Instructions land in the model's context and are what teach it the
	// vocabulary, so their absence is a real regression.
	instructions, _ := result["instructions"].(string)
	if !strings.Contains(instructions, "inbox") {
		t.Errorf("instructions do not explain the inbox: %q", instructions)
	}

	for _, concept := range []string{"open-ended", "daily_brief"} {
		if !strings.Contains(instructions, concept) {
			t.Errorf("instructions do not mention %q: %q", concept, instructions)
		}
	}

	if len(instructions) > 600 {
		t.Errorf("instructions use %d bytes, want at most 600", len(instructions))
	}
}

func TestMCPRequiresAuthentication(t *testing.T) {
	h := newHarness(t)

	res := h.rpc("", "initialize", 1, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "anon", "version": "1"},
	})

	res.expect(http.StatusUnauthorized)

	// The challenge is how a client discovers where to authenticate.
	challenge := res.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, "resource_metadata=") {
		t.Errorf("challenge %q does not carry resource_metadata", challenge)
	}
}

func TestMCPRejectsBadCredentials(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	for label, token := range map[string]string{
		"nonsense":      "cm_not-a-real-token",
		"mangled":       u.Token + "x",
		"refresh token": "cmrt_" + strings.Repeat("a", 40),
	} {
		res := h.rpc(token, "initialize", 1, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "x", "version": "1"},
		})

		if res.Status != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", label, res.Status)
		}
	}
}

// TestMCPRejectsOAuthTokenForAnotherResource is the audience check MCP requires
// of a resource server.
func TestMCPRejectsOAuthTokenForAnotherResource(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	_, accessToken, _ := h.fullFlow(u)

	// The token works before its audience is tampered with.
	h.initialize(accessToken)

	if _, err := h.store.DB().Exec(
		`UPDATE oauth_access_tokens SET audience = 'https://elsewhere.example.com'`,
	); err != nil {
		t.Fatalf("rewrite audience: %v", err)
	}

	res := h.rpc(accessToken, "tools/list", 3, nil)
	if res.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a token minted for another resource", res.Status)
	}
}

func TestMCPCookiesAreNotAccepted(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	// A cookie is attached by a browser automatically, so accepting one here
	// would let any web page drive the endpoint.
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.AddCookie(&http.Cookie{Name: "checkmate_session", Value: h.session(u)})

	h.send(req).expect(http.StatusUnauthorized)
}

func TestMCPOAuthAccessTokenWorks(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	_, accessToken, _ := h.fullFlow(u)

	// The whole point of the OAuth work: a token obtained through the authorize
	// flow can drive the MCP endpoint.
	result := h.initialize(accessToken)
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}

	tools := structured(t, h.callTool(accessToken, "list_contexts", map[string]any{}))
	if _, ok := tools["contexts"].([]any); !ok {
		t.Errorf("list_contexts returned %+v", tools)
	}
}

// ---------------------------------------------------------------------------
// Tool discovery
// ---------------------------------------------------------------------------

func TestMCPToolsList(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	res := h.rpc(u.Token, "tools/list", 2, nil).expect(http.StatusOK)

	envelope := decodeRPC(t, res)
	if envelope.Error != nil {
		t.Fatalf("tools/list failed: %+v", envelope.Error)
	}

	var result struct {
		Tools []struct {
			Name        string         `json:"name"`
			Title       string         `json:"title"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}

	byName := map[string]bool{}

	for _, tool := range result.Tools {
		byName[tool.Name] = true

		// Every tool needs a description: it is the only thing telling a model
		// when to reach for it.
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}

		// inputSchema MUST be a valid schema object, never null.
		if tool.InputSchema == nil {
			t.Errorf("tool %q has a null inputSchema", tool.Name)

			continue
		}

		if tool.InputSchema["type"] != "object" {
			t.Errorf("tool %q inputSchema type = %v, want object",
				tool.Name, tool.InputSchema["type"])
		}
	}

	for _, want := range []string{
		"daily_brief", "list_tasks", "get_task", "list_contexts", "list_projects",
		"list_people", "list_recurrences",
		"create_task", "update_task", "complete_task", "delete_task",
		"delegate_task", "triage_task", "create_project", "create_recurrence",
	} {
		if !byName[want] {
			t.Errorf("tool %q is missing from tools/list", want)
		}
	}
}

func TestMCPUnknownToolIsAProtocolError(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	res := h.rpc(u.Token, "tools/call", 3, map[string]any{
		"name":      "no_such_tool",
		"arguments": map[string]any{},
	}).expect(http.StatusOK)

	envelope := decodeRPC(t, res)

	// An unknown tool is a protocol error, not a tool error: the model cannot
	// fix a name that does not exist by retrying with different arguments.
	if envelope.Error == nil {
		t.Errorf("calling an unknown tool produced no protocol error: %s", envelope.Result)
	}
}

// ---------------------------------------------------------------------------
// Tools doing real work
// ---------------------------------------------------------------------------

func TestMCPTaskLifecycle(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	contexts := structured(t, h.callTool(u.Token, "list_contexts", map[string]any{}))

	list, _ := contexts["contexts"].([]any)
	if len(list) != 1 {
		t.Fatalf("contexts = %d, want the fixture context", len(list))
	}

	first, _ := list[0].(map[string]any)
	contextID, _ := first["id"].(string)

	// Create.
	created := structured(t, h.callTool(u.Token, "create_task", map[string]any{
		"title":            "Write the quarterly report",
		"context_id":       contextID,
		"due_on":           "2026-08-15",
		"priority":         "high",
		"estimate_minutes": 90,
		"source":           "slack",
	}))

	task, _ := created["task"].(map[string]any)

	taskID, _ := task["id"].(string)
	if taskID == "" {
		t.Fatalf("no task id in %+v", created)
	}

	if task["context_name"] != first["name"] {
		t.Errorf("context_name = %v, want the resolved name %v", task["context_name"], first["name"])
	}

	if task["kind"] != "short" {
		t.Errorf("kind = %v, want short", task["kind"])
	}
	if task["priority"] != "high" {
		t.Errorf("priority = %v, want high", task["priority"])
	}

	// Update.
	updated := structured(t, h.callTool(u.Token, "update_task", map[string]any{
		"task_id":    taskID,
		"planned_on": "2026-08-14",
		"details":    "Pull the numbers first",
		"status":     "in_progress",
		"priority":   "low",
	}))

	task, _ = updated["task"].(map[string]any)

	if task["planned_on"] != "2026-08-14" {
		t.Errorf("planned_on = %v", task["planned_on"])
	}

	if task["status"] != "in_progress" {
		t.Errorf("status = %v, want in_progress", task["status"])
	}
	if task["priority"] != "low" {
		t.Errorf("priority = %v, want low", task["priority"])
	}

	// Clearing a date needs the explicit flag, since an omitted field means
	// "leave alone".
	cleared := structured(t, h.callTool(u.Token, "update_task", map[string]any{
		"task_id":        taskID,
		"clear_due_on":   true,
		"clear_priority": true,
	}))

	task, _ = cleared["task"].(map[string]any)
	if task["due_on"] != nil && task["due_on"] != "" {
		t.Errorf("due_on = %v after clear_due_on, want empty", task["due_on"])
	}
	if task["priority"] != nil && task["priority"] != "" {
		t.Errorf("priority = %v after clear_priority, want empty", task["priority"])
	}

	// Complete.
	completed := structured(t, h.callTool(u.Token, "complete_task", map[string]any{
		"task_id": taskID,
	}))

	task, _ = completed["task"].(map[string]any)

	if task["status"] != "done" {
		t.Errorf("status = %v, want done", task["status"])
	}

	if task["completed_at"] == nil || task["completed_at"] == "" {
		t.Error("completed_at is empty after completing")
	}

	// Delete.
	deleted := structured(t, h.callTool(u.Token, "delete_task", map[string]any{
		"task_id": taskID,
	}))

	if deleted["deleted"] != true {
		t.Errorf("delete reported %+v", deleted)
	}

	gone := h.callTool(u.Token, "get_task", map[string]any{"task_id": taskID})
	if isError, _ := gone["isError"].(bool); !isError {
		t.Error("get_task on a deleted task did not report an error")
	}
}

func TestMCPCaptureToInboxThenTriage(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	// No context: the quick-capture path, which is what an assistant should use
	// when the area is not obvious.
	created := structured(t, h.callTool(u.Token, "create_task", map[string]any{
		"title": "Look into the thing Marc mentioned",
	}))

	task, _ := created["task"].(map[string]any)
	taskID, _ := task["id"].(string)

	if task["status"] != "inbox" {
		t.Errorf("status = %v, want inbox for a task with no context", task["status"])
	}

	// It is findable as untriaged.
	inbox := structured(t, h.callTool(u.Token, "list_tasks", map[string]any{"inbox_only": true}))
	if count, _ := inbox["count"].(float64); count != 1 {
		t.Errorf("inbox_only returned %v tasks, want 1", inbox["count"])
	}

	contexts := structured(t, h.callTool(u.Token, "list_contexts", map[string]any{}))
	list, _ := contexts["contexts"].([]any)
	first, _ := list[0].(map[string]any)
	contextID, _ := first["id"].(string)

	triaged := structured(t, h.callTool(u.Token, "triage_task", map[string]any{
		"task_id":    taskID,
		"context_id": contextID,
		"planned_on": "2026-08-01",
	}))

	task, _ = triaged["task"].(map[string]any)

	if task["context_id"] != contextID {
		t.Errorf("context_id = %v, want %v", task["context_id"], contextID)
	}

	// Triage has to move the status too, or the task stays untriaged forever.
	if task["status"] != "todo" {
		t.Errorf("status = %v after triage, want todo", task["status"])
	}

	inbox = structured(t, h.callTool(u.Token, "list_tasks", map[string]any{"inbox_only": true}))
	if count, _ := inbox["count"].(float64); count != 0 {
		t.Errorf("inbox still has %v tasks after triage", inbox["count"])
	}
}

// TestMCPInboxMeansAwaitingTriage pins a semantic the live run exposed as
// inconsistent: "inbox" is the status, not merely the absence of a context.
// Delegating an untriaged capture leaves it context-less while it is plainly no
// longer waiting to be sorted, so a filter keyed on the missing context would
// disagree with the daily brief about the same task.
func TestMCPInboxMeansAwaitingTriage(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	created := structured(t, h.callTool(u.Token, "create_task", map[string]any{
		"title": "Something Marc mentioned",
	}))

	task, _ := created["task"].(map[string]any)
	taskID, _ := task["id"].(string)

	inbox := structured(t, h.callTool(u.Token, "list_tasks", map[string]any{"inbox_only": true}))
	if count, _ := inbox["count"].(float64); count != 1 {
		t.Fatalf("inbox_only = %v, want the new capture", inbox["count"])
	}

	// Delegating it leaves the context empty but takes it out of the inbox.
	h.callTool(u.Token, "delegate_task", map[string]any{"task_id": taskID, "person": "Marc"})

	inbox = structured(t, h.callTool(u.Token, "list_tasks", map[string]any{"inbox_only": true}))
	if count, _ := inbox["count"].(float64); count != 0 {
		t.Errorf("inbox_only = %v after delegating, want 0", inbox["count"])
	}

	// And the brief agrees, which is the point of aligning them.
	brief := structured(t, h.callTool(u.Token, "daily_brief", map[string]any{}))

	totals, _ := brief["totals"].(map[string]any)
	if count, _ := totals["inbox"].(float64); count != 0 {
		t.Errorf("brief inbox total = %v, want 0 to match list_tasks", totals["inbox"])
	}

	if count, _ := totals["waiting_on"].(float64); count != 1 {
		t.Errorf("brief waiting_on = %v, want 1", totals["waiting_on"])
	}
}

func TestMCPDelegateCreatesThePerson(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	created := structured(t, h.callTool(u.Token, "create_task", map[string]any{
		"title": "Chase the invoice",
	}))

	task, _ := created["task"].(map[string]any)
	taskID, _ := task["id"].(string)

	delegated := structured(t, h.callTool(u.Token, "delegate_task", map[string]any{
		"task_id": taskID,
		"person":  "Marc",
	}))

	task, _ = delegated["task"].(map[string]any)

	if task["status"] != "delegated" {
		t.Errorf("status = %v, want delegated", task["status"])
	}

	if task["delegated_to"] != "Marc" {
		t.Errorf("delegated_to = %v, want the resolved name Marc", task["delegated_to"])
	}

	// The person was created on the fly.
	people := structured(t, h.callTool(u.Token, "list_people", map[string]any{}))

	list, _ := people["people"].([]any)
	if len(list) != 1 {
		t.Fatalf("people = %d, want 1 created by delegating", len(list))
	}

	// And the brief groups it under what the user is waiting on.
	brief := structured(t, h.callTool(u.Token, "daily_brief", map[string]any{}))

	waiting, _ := brief["waiting_on"].([]any)
	if len(waiting) != 1 {
		t.Fatalf("waiting_on = %+v, want one group", brief["waiting_on"])
	}

	group, _ := waiting[0].(map[string]any)
	if group["person"] != "Marc" {
		t.Errorf("waiting_on person = %v, want Marc", group["person"])
	}
}

func TestMCPDailyBrief(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	contexts := structured(t, h.callTool(u.Token, "list_contexts", map[string]any{}))
	list, _ := contexts["contexts"].([]any)
	first, _ := list[0].(map[string]any)
	contextID, _ := first["id"].(string)

	h.callTool(u.Token, "create_task", map[string]any{
		"title": "Overdue thing", "context_id": contextID, "due_on": "2020-01-01",
	})

	brief := structured(t, h.callTool(u.Token, "daily_brief", map[string]any{}))

	totals, _ := brief["totals"].(map[string]any)
	if overdue, _ := totals["overdue"].(float64); overdue != 1 {
		t.Errorf("totals.overdue = %v, want 1", totals["overdue"])
	}

	// The text summary is what a person sees in a transcript, so it should say
	// something rather than being a JSON dump.
	result := h.callTool(u.Token, "daily_brief", map[string]any{})
	if text := resultText(result); !strings.Contains(text, "overdue") {
		t.Errorf("brief summary text = %q, want a readable summary", text)
	}
}

func TestMCPCreateRecurrenceSpawnsImmediately(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	contexts := structured(t, h.callTool(u.Token, "list_contexts", map[string]any{}))
	list, _ := contexts["contexts"].([]any)
	first, _ := list[0].(map[string]any)
	contextID, _ := first["id"].(string)

	today := time.Now().UTC().Format("2006-01-02")

	created := structured(t, h.callTool(u.Token, "create_recurrence", map[string]any{
		"title":      "Daily standup",
		"context_id": contextID,
		"rrule":      "FREQ=DAILY",
		"starts_on":  today,
		"timezone":   "UTC",
	}))

	// The model can tell the user what appeared rather than promising it later.
	if spawned, _ := created["spawned"].(float64); spawned != 1 {
		t.Errorf("spawned = %v, want 1 occurrence created immediately", created["spawned"])
	}

	tasks := structured(t, h.callTool(u.Token, "list_tasks", map[string]any{}))

	items, _ := tasks["tasks"].([]any)
	if len(items) != 1 {
		t.Fatalf("tasks = %d, want the spawned occurrence", len(items))
	}

	task, _ := items[0].(map[string]any)
	if task["kind"] != "recurring" {
		t.Errorf("kind = %v, want recurring", task["kind"])
	}
}

// ---------------------------------------------------------------------------
// Ownership and scopes
// ---------------------------------------------------------------------------

// TestMCPToolsCannotReachAnotherAccount is the ownership guarantee, checked
// through the MCP path specifically: a tool receives a user id from the token and
// cannot be talked into using another.
func TestMCPToolsCannotReachAnotherAccount(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	aliceTask := h.do(http.MethodPost, "/v1/tasks", alice.Token, map[string]any{
		"title": "alice's task", "context_id": h.firstContextID(alice),
	}).expect(http.StatusCreated).id()

	h.initialize(bob.Token)

	// Bob cannot read it.
	result := h.callTool(bob.Token, "get_task", map[string]any{"task_id": aliceTask})
	if isError, _ := result["isError"].(bool); !isError {
		t.Errorf("bob read alice's task through MCP: %+v", result)
	}

	// Nor change it.
	result = h.callTool(bob.Token, "complete_task", map[string]any{"task_id": aliceTask})
	if isError, _ := result["isError"].(bool); !isError {
		t.Errorf("bob completed alice's task through MCP: %+v", result)
	}

	// Nor delete it.
	result = h.callTool(bob.Token, "delete_task", map[string]any{"task_id": aliceTask})
	if isError, _ := result["isError"].(bool); !isError {
		t.Errorf("bob deleted alice's task through MCP: %+v", result)
	}

	// Nor see it in a listing.
	tasks := structured(t, h.callTool(bob.Token, "list_tasks", map[string]any{}))
	if count, _ := tasks["count"].(float64); count != 0 {
		t.Errorf("bob's list_tasks returned %v of alice's tasks", tasks["count"])
	}

	// Alice's task is untouched.
	body := h.do(http.MethodGet, "/v1/tasks/"+aliceTask, alice.Token, nil).
		expect(http.StatusOK).decode()

	if body["status"] != "inbox" && body["status"] != "todo" {
		t.Errorf("alice's task status = %v, want it unchanged", body["status"])
	}
}

// TestMCPReadOnlyTokenCannotWrite covers the step-up path: a write tool called
// with a read-only token answers 403 with a scope challenge, so the client knows
// to obtain a wider token rather than treating it as a dead end.
func TestMCPReadOnlyTokenCannotWrite(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	readOnly := h.tokenWithScopes(u, "read")

	h.initialize(readOnly)

	// Reading is fine.
	h.callTool(readOnly, "list_contexts", map[string]any{})

	res := h.rpc(readOnly, "tools/call", 5, map[string]any{
		"name":      "create_task",
		"arguments": map[string]any{"title": "should not be created"},
	})

	res.expect(http.StatusForbidden)

	challenge := res.Header.Get("WWW-Authenticate")

	if !strings.Contains(challenge, `error="insufficient_scope"`) {
		t.Errorf("challenge %q should report insufficient_scope", challenge)
	}

	if !strings.Contains(challenge, "write") {
		t.Errorf("challenge %q should name the write scope", challenge)
	}

	// Nothing was created.
	if items := h.do(http.MethodGet, "/v1/tasks", u.Token, nil).
		expect(http.StatusOK).list(); len(items) != 0 {
		t.Errorf("a read-only token created %d task(s)", len(items))
	}
}

func TestMCPWriteScopeAllowsWriting(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	// The default token has both scopes.
	structured(t, h.callTool(u.Token, "create_task", map[string]any{
		"title": "created with a full token",
	}))
}

// ---------------------------------------------------------------------------
// Tool-level validation
// ---------------------------------------------------------------------------

// TestMCPValidationErrorsAreToolErrors checks the distinction the spec draws:
// something a model can fix by retrying differently belongs in the result with
// isError, not as a JSON-RPC error.
func TestMCPValidationErrorsAreToolErrors(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	cases := map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"impossible date": {
			"create_task", map[string]any{"title": "x", "due_on": "2026-02-31"}, "due_on",
		},
		"wrong date format": {
			"create_task", map[string]any{"title": "x", "planned_on": "31/12/2026"}, "planned_on",
		},
		"unknown source": {
			"create_task", map[string]any{"title": "x", "source": "carrier_pigeon"}, "source",
		},
		"blank title": {
			"create_task", map[string]any{"title": "   "}, "title",
		},
		"unknown context": {
			"create_task", map[string]any{"title": "x", "context_id": "nope"}, "context_id",
		},
		"unknown task": {
			"complete_task", map[string]any{"task_id": "nope"}, "not found",
		},
		"bad status": {
			"update_task", map[string]any{"task_id": "x", "status": "procrastinating"}, "status",
		},
		"delegating via update": {
			"update_task", map[string]any{"task_id": "x", "status": "delegated"}, "delegate_task",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result := h.callTool(u.Token, tc.tool, tc.args)

			isError, _ := result["isError"].(bool)
			if !isError {
				t.Fatalf("expected a tool error, got %+v", result)
			}

			// The message has to name what was wrong, or the model cannot correct
			// itself.
			if text := resultText(result); !strings.Contains(text, tc.want) {
				t.Errorf("error text = %q, want it to mention %q", text, tc.want)
			}
		})
	}
}

func TestMCPListTasksFilters(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	contextID := h.firstContextID(u)

	for i := range 3 {
		h.callTool(u.Token, "create_task", map[string]any{
			"title": fmt.Sprintf("task %d", i), "context_id": contextID,
		})
	}

	done := structured(t, h.callTool(u.Token, "create_task", map[string]any{
		"title": "finished", "context_id": contextID,
	}))

	task, _ := done["task"].(map[string]any)
	taskID, _ := task["id"].(string)

	h.callTool(u.Token, "complete_task", map[string]any{"task_id": taskID})

	// Completed work is excluded by default: a model asking "what is left"
	// should not have to filter history out itself.
	open := structured(t, h.callTool(u.Token, "list_tasks", map[string]any{}))
	if count, _ := open["count"].(float64); count != 3 {
		t.Errorf("default list returned %v tasks, want the 3 open ones", open["count"])
	}

	all := structured(t, h.callTool(u.Token, "list_tasks", map[string]any{"include_closed": true}))
	if count, _ := all["count"].(float64); count != 4 {
		t.Errorf("include_closed returned %v tasks, want 4", all["count"])
	}

	search := structured(t, h.callTool(u.Token, "list_tasks", map[string]any{"query": "task 1"}))
	if count, _ := search["count"].(float64); count != 1 {
		t.Errorf("query returned %v tasks, want 1", search["count"])
	}

	// An invalid filter is a tool error naming the field.
	bad := h.callTool(u.Token, "list_tasks", map[string]any{"status": []string{"nonsense"}})
	if isError, _ := bad["isError"].(bool); !isError {
		t.Error("an invalid status filter was accepted")
	}
}

func TestMCPGetTaskIncludesSubtasks(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	contextID := h.firstContextID(u)

	parent := structured(t, h.callTool(u.Token, "create_task", map[string]any{
		"title": "Ship the release", "context_id": contextID,
	}))

	parentTask, _ := parent["task"].(map[string]any)
	parentID, _ := parentTask["id"].(string)

	h.callTool(u.Token, "create_task", map[string]any{
		"title": "Write the changelog", "context_id": contextID, "parent_id": parentID,
	})

	fetched := structured(t, h.callTool(u.Token, "get_task", map[string]any{"task_id": parentID}))

	subtasks, _ := fetched["subtasks"].([]any)
	if len(subtasks) != 1 {
		t.Fatalf("subtasks = %+v, want 1", fetched["subtasks"])
	}

	task, _ := fetched["task"].(map[string]any)

	// Adding a child flips the derived kind with no extra write.
	if task["kind"] != "long" {
		t.Errorf("kind = %v after adding a subtask, want long", task["kind"])
	}
}

func TestMCPCreateProject(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	contextID := h.firstContextID(u)

	created := structured(t, h.callTool(u.Token, "create_project", map[string]any{
		"name": "Q3 launch", "context_id": contextID,
	}))

	project, _ := created["project"].(map[string]any)
	if project["name"] != "Q3 launch" {
		t.Errorf("project = %+v", project)
	}

	projects := structured(t, h.callTool(u.Token, "list_projects", map[string]any{}))

	list, _ := projects["projects"].([]any)
	if len(list) != 1 {
		t.Errorf("projects = %d, want 1", len(list))
	}
}

// TestMCPStructuredContentHasBothForms checks the backwards-compatibility rule:
// a tool returning structured content should also return text, since a client
// that ignores structuredContent still needs something to show.
func TestMCPStructuredContentHasBothForms(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	result := h.callTool(u.Token, "create_task", map[string]any{"title": "both forms"})

	if _, ok := result["structuredContent"].(map[string]any); !ok {
		t.Error("no structuredContent in the result")
	}

	if text := resultText(result); text == "" {
		t.Error("no text content alongside the structured result")
	}
}

func TestMCPNotificationGetsAccepted(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.initialize(u.Token)

	// A notification has no id, and the transport spec says the server answers
	// 202 with no body.
	res := h.rpc(u.Token, "notifications/initialized", nil, nil)

	if res.Status != http.StatusAccepted {
		t.Errorf("status = %d for a notification, want 202", res.Status)
	}
}

func TestMCPOriginAllowlistDisabledByDefault(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	// The harness sets no allowlist. A foreign origin is accepted because the
	// only credential is a bearer token, which no browser attaches by itself.
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+u.Token)
	req.Header.Set("Origin", "https://example.com")

	h.send(req).expect(http.StatusOK)
}
