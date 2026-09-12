// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The CLI over the management surface. Where TAISCE_MANAGE_API and TAISCE_OPERATOR_TOKEN
// are set, the operator commands speak HTTP to the manage role and need no database connection;
// otherwise they take the direct path over TAISCE_ADMIN_DSN, which is what bootstrap uses before a
// credential exists. The two paths print the same lines, so a script written against one keeps
// working on the other.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	envManageAPI     = "TAISCE_MANAGE_API"
	envOperatorToken = "TAISCE_OPERATOR_TOKEN"
	envManageAddr    = "TAISCE_MANAGE_ADDR"
	envPortal        = "TAISCE_PORTAL"
	roleManage       = "manage"
)

// manageClient is the CLI's view of the management surface.
type manageClient struct {
	base  string
	token string
	http  *http.Client
}

// managementFromEnv is the switch between the two paths. The token comes from the environment
// only, never a flag: a flag is in the shell history and the process list.
func managementFromEnv() (*manageClient, bool) {
	base := strings.TrimSpace(os.Getenv(envManageAPI))
	token := strings.TrimSpace(os.Getenv(envOperatorToken))
	if base == "" {
		return nil, false
	}
	return &manageClient{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 30 * time.Second}}, true
}

func (c *manageClient) post(ctx context.Context, path string, body any, into any) error {
	if c.token == "" {
		return fmt.Errorf("%s is set and %s is not; the management surface needs an operator credential", envManageAPI, envOperatorToken)
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+api.ManagementPrefix+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("management surface unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var refusal struct {
			Error struct {
				Code, Message string
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &refusal)
		if refusal.Error.Code == "" {
			return fmt.Errorf("the management surface answered %d", resp.StatusCode)
		}
		return fmt.Errorf("%d %s: %s", resp.StatusCode, refusal.Error.Code, refusal.Error.Message)
	}
	if into != nil {
		return json.Unmarshal(raw, into)
	}
	return nil
}

// ── The commands, over the surface ────────────────────────────────────────────────────────────

func (c *manageClient) project(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: taisce project create <name> | list | suspend <name> | resume <name> | retention <name> <days|indefinite>")
	}
	switch args[0] {
	case "create", "suspend", "resume":
		if len(args) < 2 {
			return fmt.Errorf("usage: taisce project %s <name>", args[0])
		}
		if err := c.post(ctx, "/projects/"+args[0], map[string]any{"name": args[1]}, nil); err != nil {
			return fmt.Errorf("%s project %q: %w", args[0], args[1], err)
		}
		switch args[0] {
		case "create":
			fmt.Printf("project %q created\n", args[1])
		case "suspend":
			fmt.Printf("project %q suspended; its credentials will be refused until it is resumed\n", args[1])
		default:
			fmt.Printf("project %q resumed\n", args[1])
		}
		return nil
	case "retention":
		days, err := retentionArgument(args)
		if err != nil {
			return err
		}
		var out struct {
			Retention string `json:"retention"`
		}
		if err := c.post(ctx, "/projects/retention", map[string]any{"name": args[1], "retention_days": days}, &out); err != nil {
			return fmt.Errorf("set retention for project %q: %w", args[1], err)
		}
		fmt.Printf("project %q keeps new turns for %s\n", args[1], out.Retention)
		return nil
	case "list":
		var out struct {
			Projects []pg.Listed `json:"projects"`
		}
		if err := c.post(ctx, "/projects/list", nil, &out); err != nil {
			return fmt.Errorf("list projects: %w", err)
		}
		for _, p := range out.Projects {
			printProject(p.Scope, p.Surfaces, p.Retention, p.Suspended)
		}
		return nil
	default:
		return fmt.Errorf("unknown project command %q", args[0])
	}
}

func printProject(scope string, surfaces []string, retention string, suspended bool) {
	state := ""
	if suspended {
		state = "  [suspended]"
	}
	active := "none"
	if len(surfaces) > 0 {
		active = strings.Join(surfaces, ",")
	}
	if retention == "" {
		retention = "indefinite"
	}
	fmt.Printf("%-20s surfaces=%-24s retention=%s%s\n", scope, active, retention, state)
}

func (c *manageClient) credential(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: taisce credential issue <name> [-project <name>] [--read-only] | list [-project <name>] | revoke <id>")
	}
	switch args[0] {
	case "issue":
		flags := flag.NewFlagSet("issue", flag.ContinueOnError)
		project := flags.String("project", "default", "the project this credential reaches")
		readOnly := flags.Bool("read-only", false, "allow reads and exports, but no memory mutations")
		if len(args) < 2 {
			return errors.New("usage: taisce credential issue <name> [-project <name>] [--read-only]")
		}
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected credential arguments; flags must follow the credential name")
		}
		access := credential.ReadWrite
		if *readOnly {
			access = credential.ReadOnly
		}
		var issued struct {
			ID, Name, Project, Access, Token string
		}
		if err := c.post(ctx, "/credentials/issue", map[string]any{"name": args[1], "project": *project, "access": string(access)}, &issued); err != nil {
			return err
		}
		fmt.Printf("credential %s (%s) reaches project %s with %s access\ntoken: %s\n", issued.Name, issued.ID, issued.Project, issued.Access, issued.Token)
		fmt.Print("\nThis token is shown once and cannot be recovered.\n")
		return nil
	case "list":
		flags := flag.NewFlagSet("list", flag.ContinueOnError)
		project := flags.String("project", "", "the project whose credentials to list; empty lists the operator credentials")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		var out struct {
			Credentials []credential.Listed `json:"credentials"`
		}
		if err := c.post(ctx, "/credentials/list", map[string]any{"project": *project}, &out); err != nil {
			return err
		}
		printCredentials(out.Credentials)
		return nil
	case "revoke":
		if len(args) < 2 {
			return errors.New("usage: taisce credential revoke <id>")
		}
		if err := c.post(ctx, "/credentials/revoke", map[string]any{"id": args[1]}, nil); err != nil {
			return err
		}
		fmt.Printf("credential %s revoked\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown credential command %q", args[0])
	}
}

// retentionArgument reads a retention the same way on both paths: whole days, or the word
// indefinite. Nil means indefinite, which is what a project starts with.
func retentionArgument(args []string) (*int, error) {
	if len(args) != 3 {
		return nil, errors.New("usage: taisce project retention <name> <days|indefinite>")
	}
	if args[2] == "indefinite" {
		return nil, nil
	}
	days, err := strconv.Atoi(args[2])
	if err != nil || days < 1 || days > 36500 {
		return nil, errors.New("retention is a whole number of days from 1 to 36500, or indefinite")
	}
	return &days, nil
}

// printCredentials is the credential listing both paths print.
func printCredentials(listed []credential.Listed) {
	for _, l := range listed {
		state := ""
		if l.RevokedAt != nil {
			state = "  [revoked " + l.RevokedAt.UTC().Format(time.RFC3339) + "]"
		}
		target := "operator"
		if l.Kind == credential.KindProject {
			target = "project " + l.Project + " " + string(l.Access)
		}
		fmt.Printf("%s  %-24s %s… %s%s\n", l.CredentialID, l.Name, l.TokenPrefix, target, state)
	}
}

// formation speaks the surface's formation operations. The arguments go through the parser
// the direct path uses, so both refuse the same input before anything is sent, and the answers are
// printed as the direct path prints them.
func (c *manageClient) formation(ctx context.Context, args []string, out io.Writer) error {
	f, err := parseFormation(args)
	if err != nil {
		return err
	}
	if f.op == "parked" {
		body := map[string]any{"project": f.project, "limit": f.limit}
		if f.after >= 0 {
			body["after"] = f.after
		}
		var page pg.ParkedPage
		if err := c.post(ctx, "/formation/parked", body, &page); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(page)
	}
	var result unparkResult
	if err := c.post(ctx, "/formation/unpark", map[string]any{"project": f.project, "observation_id": f.observationID}, &result); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(result)
}

func (c *manageClient) audit(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "seal" && args[0] != "verify") {
		return errors.New("usage: taisce audit seal | taisce audit verify")
	}
	// Decoded into the same types the direct path holds and printed by the same functions, so the
	// two paths say the same lines and fail the same way.
	if args[0] == "seal" {
		var seal domain.AuditSeal
		if err := c.post(ctx, "/audit/seal", nil, &seal); err != nil {
			return err
		}
		printSeal(seal)
		return nil
	}
	var verification domain.AuditVerification
	if err := c.post(ctx, "/audit/verify", nil, &verification); err != nil {
		return err
	}
	return reportVerification(verification)
}

// ── The operator credential's direct path ─────────────────────────────────────────────────────

// operatorCommand mints an operator credential over the administrative connection: the one
// operation that has to exist before the management surface can be spoken to.
func operatorCommand(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "issue" {
		return errors.New("usage: taisce operator issue <name>")
	}
	admin, _, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	token, grant, err := credential.NewStore(admin, string(migrate.ControlSchema)).IssueOperator(ctx, args[1])
	if err != nil {
		return err
	}
	fmt.Printf("operator credential %s (%s) reaches the management surface and no project\ntoken: %s\n", grant.Name, grant.CredentialID, token)
	fmt.Print("\nThis token is shown once and cannot be recovered.\n")
	return nil
}

// operatorCredentials counts the live operator credentials, for bootstrap to know whether the
// management surface can be reached by anyone.
func operatorCredentials(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM `+string(migrate.ControlSchema)+`.credential WHERE revoked_at IS NULL AND kind = 'operator'`).Scan(&n)
	return n, err
}
