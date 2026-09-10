// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &mcpConfig); err != nil {
		t.Fatal(err)
	}
	server, ok := mcpConfig.Servers["taisce"]
	if !ok || !strings.HasSuffix(server.URL, api.MCPPath) || !strings.HasPrefix(server.Headers["Authorization"], "Bearer ") {
		t.Fatalf("the plugin must reach %s with a bearer credential, got %+v", api.MCPPath, mcpConfig)
	}
	if _, err := os.Stat(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
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
