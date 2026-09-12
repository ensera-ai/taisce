// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strconv"
	"strings"
	"testing"
)

// dispatchedCommands reads the command names out of dispatch's switch, so the tests below hold the
// help to the commands the binary actually serves rather than to a second list kept by hand.
func dispatchedCommands(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "commands.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatch" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			if tag, ok := sw.Tag.(*ast.IndexExpr); !ok || tag.X.(*ast.Ident).Name != "args" {
				return true
			}
			for _, clause := range sw.Body.List {
				for _, expr := range clause.(*ast.CaseClause).List {
					if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						name, _ := strconv.Unquote(lit.Value)
						names = append(names, name)
					}
				}
			}
			return false
		})
	}
	if len(names) < 20 {
		t.Fatalf("read %d commands out of dispatch; the parse no longer finds its switch", len(names))
	}
	return names
}

// Every command the binary serves is in `taisce help`, in its plain and its terminal form, and
// answers `taisce <command> --help` with its own lines. A command added to the switch and not
// to the help fails here.
func TestTheHelpNamesEveryCommandTheBinaryServes(t *testing.T) {
	terminal := renderHelp(cliTheme{width: 100, ascii: true})
	for _, name := range dispatchedCommands(t) {
		if strings.HasPrefix(name, "-") {
			if !strings.Contains(usage, name) {
				t.Errorf("the usage text does not mention %s", name)
			}
			continue
		}
		if !strings.Contains(usage, "  taisce "+name+" ") {
			t.Errorf("the usage text does not list %s", name)
		}
		if !strings.Contains(terminal, "    "+name+" ") {
			t.Errorf("the terminal help does not list %s", name)
		}
		var out bytes.Buffer
		if ok, err := commandHelp(&out, name); !ok || err != nil || !strings.Contains(out.String(), "taisce "+name+" ") {
			t.Errorf("taisce %s --help printed %q (%v)", name, out.String(), err)
		}
	}
	for _, sub := range []string{"operator issue", "credential list", "embeddings <op>", "--manage"} {
		if !strings.Contains(usage, sub) {
			t.Errorf("the usage text leaves out %s", sub)
		}
	}
}

// Help is answered before anything is configured or connected: with no database and no management
// surface, a command asked for help prints its own lines and succeeds, wherever the flag appears. A
// mistyped command still fails, and a direct credential command fails before it connects.
func TestAskingACommandForHelpPrintsItsLinesWithoutConnecting(t *testing.T) {
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	t.Setenv(envManageAPI, "")
	ctx := context.Background()
	for _, args := range [][]string{{"credential", "--help"}, {"credential", "issue", "-h"}, {"formation", "parked", "--project", "p1", "-help"}, {"ingest", "--help"}, {"help", "credential"}} {
		out, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), args) })
		command := args[0]
		if command == "help" {
			command = args[1]
		}
		if err != nil || !strings.Contains(out, "taisce "+command+" ") {
			t.Fatalf("%v printed %q (%v)", args, out, err)
		}
		if strings.Contains(out, "taisce bootstrap") {
			t.Fatalf("%v printed more than its own command: %q", args, out)
		}
	}
	for _, args := range [][]string{{"explode", "--help"}, {"help", "explode"}} {
		if _, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), args) }); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("%v must be refused as an unknown command, got %v", args, err)
		}
	}
	if _, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), []string{"ingest", "--", "--help"}) }); err == nil {
		t.Fatal("an argument after -- was read as a request for help")
	}
	for _, args := range [][]string{{"credential", "nonsense"}, {"credential", "list", "--bogus"}, {"credential", "list", "stray"}} {
		if err := dispatch(ctx, quiet(), args); err == nil || strings.Contains(err.Error(), "DSN") {
			t.Fatalf("%v must be refused before connecting, got %v", args, err)
		}
	}
}

// A command's failure goes to stderr and leaves stdout to the command's output, so a script parsing
// stdout never reads the failure as data. The server's failure stays in its own log.
func TestACommandFailureIsWrittenToStderrAndTheServersToItsLog(t *testing.T) {
	t.Setenv(envCLIOutput, "json")
	var serverLog, stderr bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&serverLog, nil))
	reportFailure([]string{"project", "list"}, errors.New("no such project"), logger, &stderr)
	if serverLog.Len() != 0 || !strings.Contains(stderr.String(), `"msg":"taisce failed"`) || !strings.Contains(stderr.String(), "no such project") {
		t.Fatalf("a command failure: log %q, stderr %q", serverLog.String(), stderr.String())
	}
	stderr.Reset()
	reportFailure([]string{"serve"}, errors.New("port taken"), logger, &stderr)
	reportFailure(nil, errors.New("no database"), logger, &stderr)
	if stderr.Len() != 0 || !strings.Contains(serverLog.String(), "port taken") || !strings.Contains(serverLog.String(), "no database") {
		t.Fatalf("a server failure: log %q, stderr %q", serverLog.String(), stderr.String())
	}
	t.Setenv(envCLIOutput, "fancy")
	reportFailure([]string{"project", "list"}, errors.New("raw provider text"), logger, &stderr)
	if stderr.Len() == 0 || strings.Contains(stderr.String(), `"msg"`) {
		t.Fatalf("in a terminal the failure is the error panel, got %q", stderr.String())
	}
}
