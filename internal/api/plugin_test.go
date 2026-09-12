// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
)

const pluginRoot = "../../plugins/claude-code"

// The plugin's declarations agree with the server: the MCP server it configures is this route, with
// the credential as a bearer, and every command names only tools that exist.
func TestThePluginNamesThisRouteAndOnlyToolsThatExist(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(pluginRoot, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mcpConfig struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &mcpConfig); err != nil {
		t.Fatal(err)
	}
	server, ok := mcpConfig.Servers["taisce"]
	// Claude Code reads an entry without a type as a stdio server and skips one that has a URL, so
	// without "http" the plugin loads and its tools never do.
	if !ok || server.Type != "http" || !strings.HasSuffix(server.URL, api.MCPPath) || !strings.HasPrefix(server.Headers["Authorization"], "Bearer ") {
		t.Fatalf("the plugin must reach %s with a bearer credential, got %+v", api.MCPPath, mcpConfig)
	}
	manifest, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var plugin struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(manifest, &plugin); err != nil || plugin.Name == "" {
		t.Fatalf("plugin.json names no plugin: %v", err)
	}
	// The install route is a marketplace, so the repository's manifest must list this plugin under
	// its own name, at this directory.
	raw, err = os.ReadFile(filepath.Join(pluginRoot, "..", "..", ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var marketplace struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &marketplace); err != nil || marketplace.Name == "" || len(marketplace.Plugins) != 1 ||
		marketplace.Plugins[0].Name != plugin.Name || marketplace.Plugins[0].Source != "./plugins/claude-code" {
		t.Fatalf("the marketplace must list %q at ./plugins/claude-code, got %+v (%v)", plugin.Name, marketplace, err)
	}
	readme, err := os.ReadFile(filepath.Join(pluginRoot, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range api.MCPToolNames() {
		if !strings.Contains(string(readme), "`"+name+"`") {
			t.Errorf("the README does not name the %s tool", name)
		}
	}
	// A plugin's commands are named after the plugin, so a bare /remember reaches nothing.
	bare := regexp.MustCompile(`(^|[^:\w])/(recall|remember|memory-status)\b`)
	for _, path := range []string{"README.md", "scripts/session_start.py", "skills/taisce-memory/SKILL.md", ".claude-plugin/plugin.json", "../../docs/examples/coding-agent-memory.md", "../../docs/developers/mcp.md"} {
		body, err := os.ReadFile(filepath.Join(pluginRoot, path))
		if err != nil {
			t.Fatal(err)
		}
		if found := bare.FindString(string(body)); found != "" {
			t.Errorf("%s names a command without the plugin's name: %q", path, found)
		}
	}
	tools := map[string]bool{}
	for _, name := range api.MCPToolNames() {
		tools[name] = true
	}
	commands, _ := filepath.Glob(filepath.Join(pluginRoot, "commands", "*.md"))
	skill, _ := filepath.Glob(filepath.Join(pluginRoot, "skills", "*", "SKILL.md"))
	if len(commands) != 3 || len(skill) != 1 {
		t.Fatalf("expected three commands and one skill, got %d and %d", len(commands), len(skill))
	}
	for _, path := range append(commands, skill...) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"`erase`", "`forget`", "`export`"} {
			if strings.Contains(string(body), forbidden+" tool") {
				t.Fatalf("%s names a tool that must not exist: %s", path, forbidden)
			}
		}
	}
}

// The hooks run against a live deployment with the plugin's settings in the environment: the
// session-start hook says how far behind memory is and never blocks; the capture hook records the
// user's message only when capture is on, under a key that makes a second firing a replay.
func TestThePluginHooksRunAgainstALiveDeployment(t *testing.T) {
	h := newHarness(t, "api_plugin_hooks")
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("the hooks need python3, which every machine running Claude Code has")
	}
	env := append(os.Environ(),
		"CLAUDE_PLUGIN_OPTION_ENDPOINT="+h.server.URL,
		"CLAUDE_PLUGIN_OPTION_TOKEN="+h.token,
		"CLAUDE_PLUGIN_OPTION_DATA_SUBJECT_ID=subject-1")
	run := func(script string, extraEnv []string, stdin string) string {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "python3", filepath.Join(pluginRoot, "scripts", script))
		cmd.Env = append(append([]string{}, env...), extraEnv...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
		return string(out)
	}
	start := run("session_start.py", nil, "")
	var hook struct {
		Output struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(start), &hook); err != nil || !strings.Contains(hook.Output.Context, "holds nothing yet") {
		t.Fatalf("expected the empty-memory line, got %q %v", start, err)
	}
	// An unreachable deployment says nothing and blocks nothing.
	if out := run("session_start.py", []string{"CLAUDE_PLUGIN_OPTION_ENDPOINT=http://127.0.0.1:1"}, ""); strings.TrimSpace(out) != "" {
		t.Fatalf("an unreachable deployment must produce no output, got %q", out)
	}
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	lines := `{"type":"assistant","message":{"content":"earlier"}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"text","text":"We decided to keep PostgreSQL as the only store."}]}}` + "\n" +
		`{"type":"assistant","message":{"content":"noted"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	event := `{"session_id":"s-1","transcript_path":"` + transcript + `"}`
	before := storedOffset(t, h)
	run("capture_turn.py", nil, event)
	if storedOffset(t, h) != before {
		t.Fatal("capture must record nothing while it is off")
	}
	run("capture_turn.py", []string{"CLAUDE_PLUGIN_OPTION_AUTO_CAPTURE=true"}, event)
	run("capture_turn.py", []string{"CLAUDE_PLUGIN_OPTION_AUTO_CAPTURE=true"}, event)
	if storedOffset(t, h) != before+1 {
		t.Fatalf("capture on must record the turn once, got %d new", storedOffset(t, h)-before)
	}
	start = run("session_start.py", nil, "")
	if !strings.Contains(start, "has formed 0 of them") && !strings.Contains(start, "is current") {
		t.Fatalf("expected the watermark line after a turn, got %q", start)
	}

	// A turn that used a tool ends with the tool's result, which the transcript records as a
	// user-type entry with no text, followed by a meta entry. The message that started the turn is
	// what is recorded.
	capture := []string{"CLAUDE_PLUGIN_OPTION_AUTO_CAPTURE=true"}
	transcriptOf := func(lines ...string) string {
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	tools := transcriptOf(
		`{"type":"user","message":{"content":"Keep the retry fingerprint free of times."}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`{"type":"user","isMeta":true,"message":{"content":"Caveat: added by the client"}}`,
		`{"type":"assistant","message":{"content":"done"}}`)
	before = storedOffset(t, h)
	if out := run("capture_turn.py", capture, `{"session_id":"s-2","transcript_path":"`+tools+`"}`); strings.TrimSpace(out) != "" {
		t.Fatalf("a recorded turn reports nothing, got %q", out)
	}
	if storedOffset(t, h) != before+1 {
		t.Fatal("a turn that used a tool recorded nothing")
	}
	// A turn started by Claude Code itself, such as a background task's notice, is no person's words.
	notice := transcriptOf(`{"type":"user","message":{"content":"<task-notification>build finished</task-notification>"}}`)
	run("capture_turn.py", capture, `{"session_id":"s-3","transcript_path":"`+notice+`"}`)
	if storedOffset(t, h) != before+1 {
		t.Fatal("a notice was recorded as a person's words")
	}
	// A failed write is reported to the person, and the hook still exits 0 so the session stops.
	out := run("capture_turn.py", append(capture, "CLAUDE_PLUGIN_OPTION_ENDPOINT=http://127.0.0.1:1"), `{"session_id":"s-4","transcript_path":"`+tools+`"}`)
	var report struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil || !strings.Contains(report.SystemMessage, "did not record this turn: the deployment could not be reached") {
		t.Fatalf("a failed capture must say so, got %q (%v)", out, err)
	}
	if strings.Contains(out, h.token) {
		t.Fatal("the failure report carries the credential")
	}
	out = run("capture_turn.py", append(capture, "CLAUDE_PLUGIN_OPTION_TOKEN=tsk_not_a_credential"), `{"session_id":"s-5","transcript_path":"`+tools+`"}`)
	if !strings.Contains(out, "did not record this turn: the deployment answered 401") {
		t.Fatalf("a refused capture must carry the status, got %q", out)
	}
}

// storedOffset reads the deployment's stored offset over the contract, plus one, or zero before
// the first turn: what a hook's write changes, seen the way a client sees it.
func storedOffset(t *testing.T, h *harness) int64 {
	t.Helper()
	var f struct {
		Stored *int64 `json:"stored"`
	}
	h.do(t, "GET", "/v1/freshness", nil, 200, &f)
	if f.Stored == nil {
		return 0
	}
	return *f.Stored + 1
}
