// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearing adds the credential to every request, the way a host configured with a key does.
type bearing struct {
	token string
	next  http.RoundTripper
}

func (b bearing) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func connectMCP(t *testing.T, h *harness, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-host", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: h.server.URL + api.MCPPath,
		HTTPClient: &http.Client{Transport: bearing{token: token, next: http.DefaultTransport}}, DisableStandaloneSSE: true, MaxRetries: -1}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// The tool list is exactly the four, in the order declared, and none of them deletes: a host reads
// the list as a menu, and erasure and export are deliberately absent from it.
func TestMCPListsExactlyTheFourToolsAndNoneDeletes(t *testing.T) {
	h := newHarness(t, "api_mcp_tools")
	session := connectMCP(t, h, h.token)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Fatalf("tool %s must say it is not destructive", tool.Name)
		}
		if tool.Name != "observe" && !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s must be marked read-only", tool.Name)
		}
	}
	// The host lists them sorted; the declaration is the set.
	want := []string{"freshness", "observe", "recall", "resolve_citation"}
	declared := append([]string(nil), api.MCPToolNames()...)
	sort.Strings(declared)
	if strings.Join(names, ",") != strings.Join(want, ",") || strings.Join(declared, ",") != strings.Join(want, ",") {
		t.Fatalf("expected exactly %v, got %v", want, names)
	}
	for _, absent := range []string{"erase", "export", "forget", "delete"} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: absent, Arguments: map[string]any{}})
		if err == nil && !result.IsError {
			t.Fatalf("%s must not be a tool", absent)
		}
	}
}

// A turn observed through a tool is the same observation a REST caller makes: freshness sees it,
// recall answers from it, and a citation resolves to it, each through the same credential and the
// same ledger. A refused argument comes back as a tool error carrying the operation's own code.
func TestMCPObserveFreshnessRecallAndCitationAreTheSameOperations(t *testing.T) {
	h := newHarness(t, "api_mcp_journey")
	session := connectMCP(t, h, h.token)
	ctx := context.Background()
	observed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "observe", Arguments: map[string]any{
		"idempotency_key": uuid.NewString(), "data_subject_id": "subject-1",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}},
	}})
	if err != nil || observed.IsError {
		t.Fatalf("observe: %v %+v", err, observed)
	}
	var receipt struct {
		ID        string `json:"id"`
		LogOffset int64  `json:"log_offset"`
	}
	if err := json.Unmarshal(mustJSON(t, observed.StructuredContent), &receipt); err != nil || receipt.ID == "" {
		t.Fatalf("a receipt with an id is expected, got %v", observed.StructuredContent)
	}
	fresh, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "freshness", Arguments: map[string]any{}})
	if err != nil || fresh.IsError {
		t.Fatalf("freshness: %v %+v", err, fresh)
	}
	var f struct {
		Stored *int64 `json:"stored"`
	}
	if err := json.Unmarshal(mustJSON(t, fresh.StructuredContent), &f); err != nil || f.Stored == nil || *f.Stored != receipt.LogOffset {
		t.Fatalf("freshness must see the observed offset %d, got %v", receipt.LogOffset, fresh.StructuredContent)
	}
	h.form(t)
	recalled, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "recall", Arguments: map[string]any{"data_subject_id": "subject-1", "question": "Where do I live?"}})
	if err != nil || recalled.IsError {
		t.Fatalf("recall: %v %+v", err, recalled)
	}
	var bundle struct {
		Facts []struct {
			FactID    string `json:"fact_id"`
			Predicate string `json:"predicate"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(mustJSON(t, recalled.StructuredContent), &bundle); err != nil || len(bundle.Facts) == 0 {
		t.Fatalf("recall must answer from the observed turn, got %v", recalled.StructuredContent)
	}
	resolved, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "resolve_citation", Arguments: map[string]any{"id": bundle.Facts[0].FactID}})
	if err != nil || resolved.IsError {
		t.Fatalf("resolve_citation: %v %+v", err, resolved)
	}
	if !strings.Contains(string(mustJSON(t, resolved.StructuredContent)), `"evidence"`) {
		t.Fatalf("a resolved citation carries its evidence, got %v", resolved.StructuredContent)
	}
	// The text content is the same JSON, for a host that only shows text.
	if text, ok := recalled.Content[0].(*mcp.TextContent); !ok || !strings.Contains(text.Text, `"facts"`) {
		t.Fatalf("the text content must carry the same answer, got %+v", recalled.Content[0])
	}
	// A refused argument is a tool error with the operation's code, never a protocol error.
	refused, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "recall", Arguments: map[string]any{"question": ""}})
	if err != nil || !refused.IsError {
		t.Fatalf("an empty question must be a tool error, got %v %+v", err, refused)
	}
	if text := refused.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "400 invalid_question") {
		t.Fatalf("the tool error must carry the operation's code, got %q", text)
	}
	unknownField, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "observe", Arguments: map[string]any{"idempotency_key": uuid.NewString(), "content": "a blob"}})
	if err != nil || !unknownField.IsError {
		t.Fatalf("an unattributed blob must be refused as the operation refuses it, got %v %+v", err, unknownField)
	}
}

// Without a credential the door is closed before the protocol is spoken, and a credential the
// deployment does not know is refused the same way: one answer, no distinction for a stranger.
func TestMCPRefusesWithoutACredentialBeforeAnyToolRuns(t *testing.T) {
	h := newHarness(t, "api_mcp_auth")
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	for _, token := range []string{"", "not-a-credential"} {
		req, _ := http.NewRequest(http.MethodPost, h.server.URL+api.MCPPath, strings.NewReader(initialize))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "unauthenticated") {
			t.Fatalf("token %q: expected 401 unauthenticated, got %d %s", token, resp.StatusCode, body)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
