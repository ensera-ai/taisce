// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// The operator's commands.
//
// These are not request handlers, deliberately. Creating a project builds a table partition, and a
// partition name is interpolated because DDL cannot take a bind value; DDL also takes locks a
// request path must never hold. So they are controlled operations invoked at a controlled moment,
// and nothing a memory write does can trigger one by discovering a project name it has not seen.
//
// They are subcommands of the same binary rather than a second tool, because a second binary is a
// second thing to build, ship, version and keep in step with the schema it migrates.

const usage = `taisce — memory for agents

  taisce serve                     serve memory over HTTP and form the backlog (default)
  taisce bootstrap                 create the schema, run every migration, and mint the first credentials
  taisce project create <name>     create a project and its partition
  taisce project list              list the projects that exist
  taisce project suspend <name>    make a project's memory unreachable, reversibly
  taisce project resume <name>     return a suspended project to service
  taisce project retention <name>  how long new turns are kept: a number of days, or indefinite
  taisce credential issue <name>   mint a credential (--project, --read-only); the token is shown once
  taisce credential list           list credentials (--project; without it, the operator credentials)
  taisce credential revoke <id>    stop a credential resolving
  taisce operator issue <name>     mint an operator credential for the management surface; shown once
  taisce audit verify              recompute the ledger's chain and report what it covers
  taisce audit seal                seal everything written since the last seal
  taisce formation parked          inspect a bounded page of parked turn metadata (--project required)
  taisce formation unpark          retry one parked turn (--project required, then the observation id)
  taisce artifact limits           inspect or replace project artifact storage allowances
  taisce recover facts             restore a bounded page of missing facts (--project required)
  taisce recover chunks            restore message chunks with stable IDs (--project required)
  taisce rebuild facts             reinterpret one source atomically (--project, --source, --key required)
  taisce rebuild project           run a resumable page of fact rebuilds (--project, --key required)
  taisce rebuild status            inspect a project rebuild (--project, --key required)
  taisce rebuild cancel            cancel a project rebuild (--project, --key required)
  taisce rebuild reports           write the reports a project is missing now (--project required)
  taisce health                    show content-free operational aggregates using the operator connection
  taisce ingest <file>...          observe documents through the API as segments (--api, TAISCE_TOKEN)
  taisce conformance               run the adapter conformance suite against a deployment (--driver or --reference)
  taisce embeddings <op>           start, build, follow, status, activate, cancel, prune, repair or search message generations
  taisce entity-embeddings <op>    the same for entity candidate generations, without follow
  taisce report-embeddings <op>    the same for thematic report generations, without follow
  taisce probe                     check local readiness (--live, --worker, --manage)
  taisce version                   what this binary is (also --version)
  taisce help [<command>]          this list (also -h, --help); taisce <command> --help prints one command's lines

Configuration is read from the environment. TAISCE_MEMORY_DSN and TAISCE_REGISTRY_DSN are the two
database connections, and they are separate so that a memory query cannot read a credential.
TAISCE_SCHEMA optionally selects this instance's memory namespace (default: memory). With
TAISCE_MANAGE_API and TAISCE_OPERATOR_TOKEN set, project, credential, audit and formation speak the
management surface instead of the database.
`

// wantsHelp reports whether a command's arguments ask for help. An argument after "--" is a value,
// not a flag, so it does not count.
func wantsHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

// commandHelp prints the usage lines of one command, and reports whether it has any.
func commandHelp(out io.Writer, command string) (bool, error) {
	var lines []string
	for _, line := range strings.Split(usage, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed == "taisce "+command || strings.HasPrefix(trimmed, "taisce "+command+" ") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return false, nil
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return true, err
}

// dispatch routes a subcommand, defaulting to serving.
//
// Defaulting matters: the container's command is the common case, and a deployment that has to
// remember to say "serve" is one that fails at start with a usage message.
func dispatch(ctx context.Context, log *slog.Logger, args []string) error {
	if len(args) == 0 {
		return run(ctx, log)
	}
	// Help is answered before anything is configured or connected, wherever it appears among a
	// command's arguments, from the same text `taisce help` prints so the two cannot disagree.
	if len(args) > 1 && wantsHelp(args[1:]) {
		if ok, err := commandHelp(os.Stdout, args[0]); ok || err != nil {
			return err
		}
	}
	switch args[0] {
	case "serve":
		return run(ctx, log)
	case "bootstrap":
		return bootstrap(ctx, log, args[1:])
	case "project":
		if c, ok := managementFromEnv(); ok {
			return c.project(ctx, args[1:])
		}
		return project(ctx, args[1:])
	case "credential":
		if c, ok := managementFromEnv(); ok {
			return c.credential(ctx, args[1:])
		}
		return credentialCommand(ctx, args[1:])
	case "operator":
		return operatorCommand(ctx, args[1:])
	case "audit":
		if c, ok := managementFromEnv(); ok {
			return c.audit(ctx, args[1:])
		}
		return auditCommand(ctx, args[1:])
	case "formation":
		if c, ok := managementFromEnv(); ok {
			return c.formation(ctx, args[1:], os.Stdout)
		}
		return formationCommand(ctx, args[1:], os.Stdout)
	case "artifact":
		return artifactCommand(ctx, args[1:], os.Stdout)
	case "recover":
		return recoveryCommand(ctx, args[1:], os.Stdout)
	case "rebuild":
		return rebuildCommand(ctx, args[1:], os.Stdout)
	case "health":
		return healthCommand(ctx, args[1:], os.Stdout)
	case "ingest":
		return ingestCommand(ctx, args[1:], os.Stdout)
	case "conformance":
		return conformanceCommand(ctx, args[1:], os.Stdout)
	case "embeddings":
		return embeddingsCommand(ctx, args[1:], os.Stdout)
	case "entity-embeddings":
		return entityEmbeddingsCommand(ctx, args[1:], os.Stdout)
	case "report-embeddings":
		return reportEmbeddingsCommand(ctx, args[1:], os.Stdout)
	case "probe":
		return probeCommand(ctx, args[1:])
	case "version", "--version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		if len(args) > 1 {
			ok, err := commandHelp(os.Stdout, args[1])
			if err == nil && !ok {
				err = fmt.Errorf("unknown command %q — run `taisce help` for the list", args[1])
			}
			return err
		}
		if fancyOutput(os.Stdout) {
			fmt.Print(renderHelp(currentTheme()))
		} else {
			fmt.Print(usage)
		}
		return nil
	default:
		// The error alone, not the usage text. A wrong command is a message on stderr; printing a
		// page of help to stdout on top of it buries the one line that says what happened.
		return fmt.Errorf("unknown command %q — run `taisce help` for the list", args[0])
	}
}

// bootstrap brings an empty database to a state that can serve.
//
// It is idempotent, because the compose file and the chart both run it on every start and a
// bootstrap that fails the second time is a deployment that cannot restart. Every step underneath it
// is already idempotent — migrations record their version, provisioning uses IF NOT EXISTS, and the
// roles are created only when absent — so this is composition rather than new logic.
//
// # Why it mints a credential
//
// A running instance nobody can authenticate to is a running instance nobody can use. The token is
// printed once, to stdout, and is not recoverable afterwards: only its digest is stored. That is the
// property being bought, so the message says it plainly rather than leaving an operator to discover
// it when they lose the value.
func bootstrap(ctx context.Context, log *slog.Logger, args []string) error {
	flags := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	project := flags.String("project", "default", "the project to create")
	credentialName := flags.String("credential", "bootstrap", "the name of the first credential")
	if err := flags.Parse(args); err != nil {
		return err
	}

	admin, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()

	// The two database identities and the registry schema behind its fence. Passwords come from the
	// environment because they are secrets, and a default would be a credential that works because
	// somebody forgot to set one.
	controlPassword := os.Getenv("TAISCE_CONTROL_PASSWORD")
	dataPassword := os.Getenv("TAISCE_DATA_PASSWORD")
	if controlPassword == "" || dataPassword == "" {
		return fmt.Errorf("TAISCE_CONTROL_PASSWORD and TAISCE_DATA_PASSWORD are required: the two " +
			"database identities exist so that a memory query cannot read a credential, and a " +
			"default password would be one that works because nobody chose it")
	}
	if err := migrate.EstablishPlanes(ctx, admin, controlPassword, dataPassword); err != nil {
		return fmt.Errorf("establish the registry and its boundary: %w", err)
	}
	log.Info("registry established")

	if err := migrate.ProvisionMemorySchema(ctx, admin, schema.String()); err != nil {
		return fmt.Errorf("provision the schema: %w", err)
	}
	log.Info("schema provisioned", "schema", schema.String())
	if err := migrate.ValidateRolePrivileges(ctx, admin, schema, migrate.DataRole, migrate.MemoryPlane); err != nil {
		return err
	}
	if err := migrate.ValidateRolePrivileges(ctx, admin, schema, migrate.ControlRole, migrate.RegistryPlane); err != nil {
		return err
	}

	if err := migrate.ProvisionScope(ctx, admin, schema.String(), *project); err != nil {
		return fmt.Errorf("provision project %q: %w", *project, err)
	}
	log.Info("project provisioned", "project", *project)

	// Only when nothing can already reach this project.
	//
	// The condition is deliberately "can anybody use this project" rather than "does any credential
	// exist": a restart must not mint a second key, and bootstrapping a NEW project on an instance
	// that already has others must not silently leave it unreachable. A global count would get the
	// first right and the second wrong.
	store := credential.NewStore(admin, string(migrate.ControlSchema))
	existing, err := credentialsForProject(ctx, admin, *project)
	if err != nil {
		return err
	}
	// The management surface needs an operator credential before anyone can speak to it,
	// and bootstrap is the one path that exists before then; one is minted when none is live.
	operators, err := operatorCredentials(ctx, admin)
	if err != nil {
		return err
	}
	if operators == 0 {
		token, grant, err := store.IssueOperator(ctx, "operator")
		if err != nil {
			return fmt.Errorf("mint the first operator credential: %w", err)
		}
		fmt.Printf("\noperator credential %s (%s) reaches the management surface and no project\n", grant.Name, grant.CredentialID)
		fmt.Printf("operator token: %s\n", token)
	}
	if existing > 0 {
		log.Info("this project can already be reached; no credential minted",
			"project", *project, "credentials", existing)
		return nil
	}

	token, grant, err := store.Issue(ctx, *credentialName, *project)
	if err != nil {
		return fmt.Errorf("mint the first credential: %w", err)
	}
	// Printed to stdout rather than logged, so that it can be captured by a script without the log
	// format getting in the way — and said once, because it is not recoverable.
	fmt.Printf("\ncredential %s (%s) reaches project %s\n",
		grant.Name, grant.CredentialID, grant.Project)
	fmt.Printf("token: %s\n", token)
	fmt.Print("\nThis token is shown once. Only its digest is stored, so it cannot be recovered — " +
		"mint another if it is lost.\n\n")
	return nil
}

// credentialsForProject counts the live credentials that grant a project.
//
// This is the question bootstrap actually needs answered: not how many keys exist, but whether the
// project it just created can be reached by anyone.
func credentialsForProject(ctx context.Context, pool *pgxpool.Pool, project string) (int, error) {
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM `+string(migrate.ControlSchema)+`.credential
		  WHERE revoked_at IS NULL AND project = $1`, project).Scan(&n); err != nil {
		return 0, fmt.Errorf("count credentials for project: %w", err)
	}
	return n, nil
}

// project creates and lists projects.
//
// Deleting one is deliberately absent until it can be done with the guarantees erasure already
// makes: dropping a partition would remove memory without a counted residual, which is the one thing
// this product promises never to do quietly.
func project(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taisce project create <name> | taisce project list")
	}
	admin, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()

	switch args[0] {
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: taisce project create <name>")
		}
		if err := migrate.ProvisionScope(ctx, admin, schema.String(), args[1]); err != nil {
			return fmt.Errorf("create project %q: %w", args[1], err)
		}
		fmt.Printf("project %q created\n", args[1])
		return nil

	case "list":
		// Read from the project table rather than from the data.
		//
		// This used to read `watermark`, which meant existence was inferred from something having
		// been written — so a project created and not yet used did not appear, and an operator
		// checking their work saw nothing. A project is a row now, and its existence does not depend
		// on anybody having used it.
		rows, err := admin.Query(ctx, schema.SQL(
			`SELECT scope, surfaces,
			        coalesce(retention::text, 'indefinite'),
			        suspended_at IS NOT NULL
			   FROM {schema}.project ORDER BY scope`))
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var scope, retention string
			var surfaces []string
			var suspended bool
			if err := rows.Scan(&scope, &surfaces, &retention, &suspended); err != nil {
				return err
			}
			state := ""
			if suspended {
				state = "  [suspended]"
			}
			active := "none"
			if len(surfaces) > 0 {
				active = strings.Join(surfaces, ",")
			}
			fmt.Printf("%-20s surfaces=%-24s retention=%s%s\n", scope, active, retention, state)
		}
		return rows.Err()

	case "retention":
		// How long a project keeps what it is told from now on. It rewrites no deadline already
		// stamped on a turn: the policy in force when a turn arrived governs that turn, which is
		// what makes a deletion auditable afterwards.
		days, err := retentionArgument(args)
		if err != nil {
			return err
		}
		if err := pg.NewProjectStore(admin, schema).SetRetention(ctx, args[1], days); err != nil {
			return fmt.Errorf("set retention for %q: %w", args[1], err)
		}
		fmt.Printf("project %q keeps new turns for %s\n", args[1], retentionSaid(days))
		return nil

	case "suspend", "resume":
		// Suspension is the reversible half of a project's lifecycle: the memory is intact and
		// unreachable, and every credential for it stops working until it is resumed.
		//
		// There is deliberately no `project delete`. Removing a project would remove memory, and
		// removing memory is an erasure — with a receipt and a counted residual — rather than a
		// lifecycle verb that happens to destroy things.
		if len(args) < 2 {
			return fmt.Errorf("usage: taisce project %s <name>", args[0])
		}
		projects := pg.NewProjectStore(admin, schema)
		if args[0] == "suspend" {
			if err := projects.Suspend(ctx, args[1]); err != nil {
				return fmt.Errorf("suspend %q: %w", args[1], err)
			}
			fmt.Printf("project %q suspended; its credentials will be refused until it is resumed\n", args[1])
			return nil
		}
		if err := projects.Resume(ctx, args[1]); err != nil {
			return fmt.Errorf("resume %q: %w", args[1], err)
		}
		fmt.Printf("project %q resumed\n", args[1])
		return nil

	default:
		return fmt.Errorf("unknown project command %q", args[0])
	}
}

// credentialCommand mints and revokes credentials.
func credentialCommand(ctx context.Context, args []string) error {
	const usageLine = "usage: taisce credential issue <name> [-project <name>] [--read-only] | list [-project <name>] | revoke <id>"
	if len(args) == 0 {
		return errors.New(usageLine)
	}
	// Every argument is checked before connecting, so a mistyped command fails without opening the
	// administrative connection.
	var name, project string
	var readOnly bool
	switch args[0] {
	case "issue":
		flags := flag.NewFlagSet("issue", flag.ContinueOnError)
		projectFlag := flags.String("project", "default", "the project this credential reaches")
		readOnlyFlag := flags.Bool("read-only", false, "allow reads and exports, but no memory mutations")
		if len(args) < 2 {
			return fmt.Errorf("usage: taisce credential issue <name> [-project <name>] [--read-only]")
		}
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected credential arguments; flags must follow the credential name")
		}
		name, project, readOnly = args[1], *projectFlag, *readOnlyFlag
	case "list":
		flags := flag.NewFlagSet("list", flag.ContinueOnError)
		projectFlag := flags.String("project", "", "the project whose credentials to list; empty lists the operator credentials")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("usage: taisce credential list [-project <name>]")
		}
		project = *projectFlag
	case "revoke":
		if len(args) < 2 {
			return fmt.Errorf("usage: taisce credential revoke <id>")
		}
		name = args[1]
	default:
		return fmt.Errorf("unknown credential command %q", args[0])
	}

	admin, _, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	store := credential.NewStore(admin, string(migrate.ControlSchema))

	switch args[0] {
	case "issue":
		// Checked here rather than by a foreign key, because the project table is in the tenant
		// schema and this row is in the registry — behind the fence the memory service cannot
		// cross. This command holds a connection that can see both, so it is the only place the
		// reference CAN be validated, and without it a typo mints a credential that authenticates
		// and reaches nothing. A suspended project is refused for the same reason, as the
		// management surface refuses it.
		if err := requireActiveProject(ctx, admin, project); err != nil {
			return err
		}
		access := credential.ReadWrite
		if readOnly {
			access = credential.ReadOnly
		}
		token, grant, err := store.IssueWithAccess(ctx, name, project, access)
		if err != nil {
			return err
		}
		fmt.Printf("credential %s (%s) reaches project %s with %s access\ntoken: %s\n",
			grant.Name, grant.CredentialID, grant.Project, grant.Access, token)
		fmt.Print("\nThis token is shown once and cannot be recovered.\n")
		return nil
	case "list":
		// Zero is the store's default page, the one the management surface asks for when a
		// listing names no limit, so both paths list the same credentials.
		listed, err := store.List(ctx, project, 0)
		if err != nil {
			return err
		}
		printCredentials(listed)
		return nil
	default:
		if err := store.Revoke(ctx, name); err != nil {
			return err
		}
		fmt.Printf("credential %s revoked\n", name)
		return nil
	}
}

// retentionSaid says a policy the way the operator wrote it.
func retentionSaid(days *int) string {
	if days == nil {
		return "indefinite"
	}
	return fmt.Sprintf("%d days", *days)
}

// requireActiveProject is requireProject for minting: a project that exists but is suspended is
// refused too, because a credential for it would authenticate and reach nothing.
func requireActiveProject(ctx context.Context, admin *pgxpool.Pool, project string) error {
	if err := requireProject(ctx, admin, project); err != nil {
		return err
	}
	schema, err := configuredMemorySchema()
	if err != nil {
		return err
	}
	if _, err := pg.NewProjectStore(admin, schema).Active(ctx, project); err != nil {
		if errors.Is(err, pg.ErrNoSuchProject) {
			return fmt.Errorf("project %q is suspended; resume it with `taisce project resume %s` before minting a credential for it", project, project)
		}
		return fmt.Errorf("check project %q: %w", project, err)
	}
	return nil
}

// requireProject refuses to mint a credential for a project that does not exist.
//
// This is the check a foreign key would make, in the only place that can make it: the credential is
// in the control namespace and the project is in the memory namespace. This preflight keeps
// operator mistakes explicit; it is not a claim that PostgreSQL forbids cross-schema foreign keys.
//
// It is a weaker guarantee than a constraint — a credential minted some other way could still name
// nothing — and it catches the case that actually happens, which is a typo becoming a token that
// authenticates successfully and reaches no memory at all.
func requireProject(ctx context.Context, admin *pgxpool.Pool, project string) error {
	schema, err := configuredMemorySchema()
	if err != nil {
		return err
	}
	var exists bool
	if err := admin.QueryRow(ctx, schema.SQL(
		`SELECT EXISTS (SELECT 1 FROM {schema}.project WHERE scope = $1)`), project).Scan(&exists); err != nil {
		return fmt.Errorf("check project %q: %w", project, err)
	}
	if !exists {
		return fmt.Errorf("project %q does not exist; create it first with `taisce project create %s`",
			project, project)
	}
	return nil
}

// adminPool connects with the identity that may change the schema.
//
// The operator commands run DDL and touch the registry, which the serving identities deliberately
// cannot do. They are a different connection for that reason, and the separation is the same one the
// server relies on rather than a second mechanism.
func adminPool(ctx context.Context) (*pgxpool.Pool, pg.Schema, error) {
	dsn := strings.TrimSpace(os.Getenv(envAdminDSN))
	if dsn == "" {
		// Falling back to the memory connection would work on a laptop where everything is
		// superuser, and fail in a deployment where it is not — after the schema was half created.
		dsn = strings.TrimSpace(os.Getenv(envMemoryDSN))
	}
	if dsn == "" {
		return nil, "", fmt.Errorf("%s (or %s) is not set", envAdminDSN, envMemoryDSN)
	}
	schema, err := configuredMemorySchema()
	if err != nil {
		return nil, "", fmt.Errorf("schema: %w", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, "", fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, "", fmt.Errorf("database unreachable: %w", err)
	}
	return pool, schema, nil
}

// auditCommand is the operator's own check on their own ledger.
//
// Theirs rather than ours, and that is the whole point of the narrowed design: the earlier plan was a
// signed checkpoint a CLIENT verified, which existed so a customer could catch a vendor. Self-hosted
// there is no vendor — the operator holds the disk — and what remains is an operator demonstrating to
// their own auditor that they have not rewritten their own records.
func auditCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taisce audit verify | taisce audit seal")
	}
	admin, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	audit := pg.NewAuditStore(admin, schema)

	switch args[0] {
	case "seal":
		seal, err := audit.Seal(ctx)
		if err != nil {
			return err
		}
		printSeal(seal)
		return nil

	case "verify":
		v, err := audit.Verify(ctx)
		if err != nil {
			return err
		}
		return reportVerification(v)

	default:
		return fmt.Errorf("unknown audit command %q", args[0])
	}
}

// printSeal and reportVerification are the audit command's output, whichever path it took.
//
// One function for both paths, because the promise is that a script written against one keeps
// working against the other — and the first time that promise was only a comment, the management
// path printed raw JSON and exited 0 over a ledger that did not verify.
func printSeal(seal domain.AuditSeal) {
	if seal.Entries == 0 {
		fmt.Println("nothing to seal")
		return
	}
	fmt.Printf("sealed %d entries (%d-%d)\nhead: %x\n",
		seal.Entries, seal.From, seal.To, seal.Digest)
	fmt.Print("\nRecord that head somewhere this database cannot reach. A rebuilt ledger " +
		"cannot produce a head that agrees with one written down earlier, and that is the only " +
		"thing standing between this and somebody who can rewrite everything.\n")
}

// reportVerification prints what the check found and fails the command when the ledger does not
// verify: non-zero, because this is the one command whose result something should act on.
func reportVerification(v domain.AuditVerification) error {
	if !v.Valid {
		fmt.Printf("FAILED: %s\n", v.Failure)
		return fmt.Errorf("the audit ledger does not verify")
	}
	fmt.Printf("verified: %d seals covering %d entries\nhead: %x\n", v.Seals, v.Entries, v.Head)
	if v.Unsealed > 0 {
		// Said every time rather than only when it looks large. A verification that reports only
		// what it covers is a reassurance.
		fmt.Printf("\n%d entries written since the last seal are covered by nothing.\n", v.Unsealed)
	}
	return nil
}
