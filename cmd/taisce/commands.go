package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/credential"
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
  taisce bootstrap                 create the schema, run every migration, and mint the first credential
  taisce project create <name>     create a project and its partition
  taisce project list              list the projects that exist
  taisce credential issue <name>   mint a credential; the token is printed once and never stored
  taisce credential revoke <id>    stop a credential resolving

Configuration is read from the environment. TAISCE_MEMORY_DSN and TAISCE_REGISTRY_DSN are the two
database connections, and they are separate so that a memory query cannot read a credential.
TAISCE_SCHEMA names this instance's schema.
`

// dispatch routes a subcommand, defaulting to serving.
//
// Defaulting matters: the container's command is the common case, and a deployment that has to
// remember to say "serve" is one that fails at start with a usage message.
func dispatch(ctx context.Context, log *slog.Logger, args []string) error {
	if len(args) == 0 {
		return run(ctx, log)
	}
	switch args[0] {
	case "serve":
		return run(ctx, log)
	case "bootstrap":
		return bootstrap(ctx, log, args[1:])
	case "project":
		return project(ctx, args[1:])
	case "credential":
		return credentialCommand(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
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

	if err := migrate.ProvisionTenant(ctx, admin, schema.String()); err != nil {
		return fmt.Errorf("provision the schema: %w", err)
	}
	log.Info("schema provisioned", "schema", schema.String())

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
	if existing > 0 {
		log.Info("this project can already be reached; no credential minted",
			"project", *project, "credentials", existing)
		return nil
	}

	token, grant, err := store.Issue(ctx, *credentialName, []string{*project})
	if err != nil {
		return fmt.Errorf("mint the first credential: %w", err)
	}
	// Printed to stdout rather than logged, so that it can be captured by a script without the log
	// format getting in the way — and said once, because it is not recoverable.
	fmt.Printf("\ncredential %s (%s) grants: %s\n", grant.Name, grant.CredentialID,
		strings.Join(grant.Scopes, ", "))
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
		  WHERE revoked_at IS NULL AND $1 = ANY(scopes)`, project).Scan(&n); err != nil {
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
		// Read from the partitions that exist rather than from a list somebody maintains: two
		// definitions of which projects exist is one that drifts, and the one that drifts is the one
		// no write touches.
		rows, err := admin.Query(ctx, schema.SQL(
			`SELECT DISTINCT scope FROM {schema}.watermark ORDER BY scope`))
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var scope string
			if err := rows.Scan(&scope); err != nil {
				return err
			}
			fmt.Println(scope)
		}
		return rows.Err()

	default:
		return fmt.Errorf("unknown project command %q", args[0])
	}
}

// credentialCommand mints and revokes credentials.
func credentialCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taisce credential issue <name> [-scopes a,b] | taisce credential revoke <id>")
	}
	admin, _, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	store := credential.NewStore(admin, string(migrate.ControlSchema))

	switch args[0] {
	case "issue":
		flags := flag.NewFlagSet("issue", flag.ContinueOnError)
		scopes := flags.String("scopes", "default", "comma-separated projects this credential may reach")
		if len(args) < 2 {
			return fmt.Errorf("usage: taisce credential issue <name> [-scopes a,b]")
		}
		name := args[1]
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		token, grant, err := store.Issue(ctx, name, splitScopes(*scopes))
		if err != nil {
			return err
		}
		fmt.Printf("credential %s (%s) grants: %s\ntoken: %s\n",
			grant.Name, grant.CredentialID, strings.Join(grant.Scopes, ", "), token)
		fmt.Print("\nThis token is shown once and cannot be recovered.\n")
		return nil

	case "revoke":
		if len(args) < 2 {
			return fmt.Errorf("usage: taisce credential revoke <id>")
		}
		if err := store.Revoke(ctx, args[1]); err != nil {
			return err
		}
		fmt.Printf("credential %s revoked\n", args[1])
		return nil

	default:
		return fmt.Errorf("unknown credential command %q", args[0])
	}
}

// splitScopes turns a comma-separated list into a set, dropping empties so that a trailing comma
// does not become a project named "".
func splitScopes(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
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
	tenant := strings.TrimSpace(os.Getenv(envTenant))
	if tenant == "" {
		return nil, "", fmt.Errorf("%s is not set", envTenant)
	}
	schema, err := pg.NewSchema(tenant)
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
