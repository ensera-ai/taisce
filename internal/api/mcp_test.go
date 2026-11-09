// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
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

// The tool list is exactly the six, in the order declared, and none of them deletes: a host reads
// the list as a menu, and erasure, export and feedback promotion are deliberately absent from it.
func TestMCPListsExactlyTheSixToolsAndNoneDeletes(t *testing.T) {
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
		if tool.Name != "observe" && tool.Name != "report_feedback" && !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s must be marked read-only", tool.Name)
		}
	}
	// The host lists them sorted; the declaration is the set.
	want := []string{"context", "freshness", "observe", "recall", "report_feedback", "resolve_citation"}
	declared := append([]string(nil), api.MCPToolNames()...)
	sort.Strings(declared)
	if strings.Join(names, ",") != strings.Join(want, ",") || strings.Join(declared, ",") != strings.Join(want, ",") {
		t.Fatalf("expected exactly %v, got %v", want, names)
	}
	// Promotion is the one that would let a model change the graph, so it is named here rather than
	// left to the set comparison: a tool added later under that name has to fail this too.
	for _, absent := range []string{"erase", "export", "forget", "delete", "promote_feedback", "correct_record"} {
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
	// A context is the same assembly a REST caller gets: the observed turn verbatim, under the
	// caller's credential, with no model call.
	assembled, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "context", Arguments: map[string]any{"data_subject_id": "subject-1"}})
	if err != nil || assembled.IsError {
		t.Fatalf("context: %v %+v", err, assembled)
	}
	var history struct {
		Turns    []json.RawMessage `json:"turns"`
		Segments []json.RawMessage `json:"segments"`
	}
	if err := json.Unmarshal(mustJSON(t, assembled.StructuredContent), &history); err != nil || len(history.Turns) != 1 || len(history.Segments) != 0 {
		t.Fatalf("a context over one turn is that turn verbatim and no segment, got %v", assembled.StructuredContent)
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

	// An agent holding a citation can say the record is wrong, and saying so changes no answer.
	// That is the whole reason this tool is allowed on a surface whose rule is that nothing
	// overwrites or deletes.
	reported, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "report_feedback", Arguments: map[string]any{
		"record_id": bundle.Facts[0].FactID, "note": "the city here is out of date", "proposed_object": "Oslo"}})
	if err != nil || reported.IsError {
		t.Fatalf("report_feedback: %v %+v", err, reported)
	}
	if !strings.Contains(string(mustJSON(t, reported.StructuredContent)), `"record_id"`) {
		t.Fatalf("the report must come back naming the record, got %v", reported.StructuredContent)
	}
	again, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "recall", Arguments: map[string]any{"data_subject_id": "subject-1", "question": "Where do I live?"}})
	if err != nil || again.IsError {
		t.Fatalf("recall after feedback: %v %+v", err, again)
	}
	if string(mustJSON(t, again.StructuredContent)) != string(mustJSON(t, recalled.StructuredContent)) {
		t.Fatal("reporting feedback through a tool changed what recall answers")
	}
	// And its refusals are the operation's, not the protocol's.
	blank, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "report_feedback", Arguments: map[string]any{
		"record_id": bundle.Facts[0].FactID, "note": "   "}})
	if err != nil || !blank.IsError {
		t.Fatalf("a blank note must be a tool error, got %v %+v", err, blank)
	}
	if text := blank.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "400 invalid_feedback") {
		t.Fatalf("the tool error must carry the operation's code, got %q", text)
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

// /mcp is a public interface, and the protocol revision a host negotiates is part of it: a
// dependency bump that moves the SDK to a newer revision changes what every connected host sees
// without a line of this repository changing. The literals below are the revision the surface was
// reviewed against, so a bump fails here and is read rather than absorbed.
//
// The caching hints that revision adds are asserted too: ttlMs is the SDK's 0, inherited because
// the list is static and no lifetime has been measured as needed, and cacheScope is "private"
// because the SDK's "public" would let an intermediary share a credential-scoped answer (#150).
func TestTheMCPSurfaceNegotiatesTheProtocolRevisionItWasReviewedAgainst(t *testing.T) {
	const reviewed = "2026-07-28"
	h := newHarness(t, "api_mcp_revision")
	session := connectMCP(t, h, h.token)

	init := session.InitializeResult()
	if init == nil {
		t.Fatal("the session carries no initialize result, so no revision was negotiated")
	}
	if init.ProtocolVersion != reviewed {
		t.Fatalf("the /mcp surface now negotiates protocol revision %q; it was reviewed against %q. "+
			"A new revision changes what every connected host sees: review its changes to tools/list, "+
			"tools/call and caching, then update this test and the revision recorded in docs/developers/mcp.md.",
			init.ProtocolVersion, reviewed)
	}

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if listed.TTLMs != 0 || listed.CacheScope != "private" {
		t.Fatalf("tools/list now carries ttlMs=%d cacheScope=%q; reviewed as ttlMs=0 (SDK default, deliberately inherited) and cacheScope=\"private\" (set by MCPPrivateCacheScope, #150). Update this test and docs/developers/mcp.md if either is changed on purpose.",
			listed.TTLMs, listed.CacheScope)
	}
}

// The point of the middleware is that a result type registered later does not silently inherit the
// SDK's "public", so the property is held against every cacheable result, including the ones this
// server does not serve. A throwaway prompt, resource and template exist only here, and a spy
// outside the middleware records what actually leaves the chain, so the assertion is on what a
// host would receive rather than on what the test hopes the middleware did.
func TestEveryCacheableMCPResultCarriesAPrivateScopeWhateverIsRegisteredLater(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "throwaway", Version: "0"}, nil)
	server.AddPrompt(&mcp.Prompt{Name: "p"}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	})
	server.AddResource(&mcp.Resource{Name: "r", URI: "test://r"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "test://r", Text: "x"}}}, nil
	})
	server.AddResourceTemplate(&mcp.ResourceTemplate{Name: "t", URITemplate: "test://t/{id}"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{}, nil
	})
	server.AddTool(&mcp.Tool{Name: "x", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	server.AddReceivingMiddleware(api.MCPPrivateCacheScope)
	seen := map[string]string{}
	var mu sync.Mutex
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if c, ok := res.(mcp.CacheableResult); ok {
				mu.Lock()
				seen[fmt.Sprintf("%T", res)] = c.GetCacheScope()
				mu.Unlock()
			}
			return res, err
		}
	})

	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	ctx := context.Background()
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListPrompts(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListResources(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListResourceTemplates(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "test://r"}); err != nil {
		t.Fatal(err)
	}
	t.Logf("observed: %v", seen)
	for _, typ := range []string{"*mcp.ListToolsResult", "*mcp.ListPromptsResult", "*mcp.ListResourcesResult",
		"*mcp.ListResourceTemplatesResult", "*mcp.ReadResourceResult", "*mcp.DiscoverResult"} {
		scope, ok := seen[typ]
		if !ok {
			t.Errorf("%s was never produced by the test client, so its scope was not observed", typ)
			continue
		}
		if scope != "private" {
			t.Errorf("%s carries cacheScope %q; every cacheable result must be private", typ, scope)
		}
	}
}
