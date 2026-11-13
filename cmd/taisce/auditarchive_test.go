// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAnOperatorArchivesASealedRangeAndAnybodyCanCheckTheFile is the operator's side of bounding
// the ledger, by the commands an operator types.
//
// A range is archived to a file. The file is then checked with no database, against the digest
// that was printed when the range was sealed — the value an operator is told to keep somewhere the
// database cannot reach. The ledger that remains verifies. And each way the command must refuse is
// made to happen: a file that already exists, nothing old enough, an altered file, a digest the
// file does not end at.
func TestAnOperatorArchivesASealedRangeAndAnybodyCanCheckTheFile(t *testing.T) {
	ctx := context.Background()
	const name = "cmd_audit_archive"
	env := complete(t)
	env[envSchema] = name
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, name)
	defer pool.Exec(ctx, "DROP SCHEMA "+name+" CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, name); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(name)
	audit := pg.NewAuditStore(pool, schema)
	for i := 0; i < 7; i++ {
		if err := audit.Append(ctx, domain.AuditEntry{Operation: domain.AuditRecall, Principal: "credential-1",
			PrincipalKind: domain.PrincipalCredential, Project: "p1", Magnitude: i, Outcome: domain.OutcomeAllowed}); err != nil {
			t.Fatal(err)
		}
	}
	seal, err := audit.Seal(ctx)
	if err != nil || seal.Entries != 7 {
		t.Fatalf("seal: %+v %v", seal, err)
	}
	head := hex.EncodeToString(seal.Digest)
	dir := t.TempDir()
	file := filepath.Join(dir, "ledger-0001.jsonl")

	// Nothing sealed is a year old.
	var out bytes.Buffer
	if err := auditArchiveCommand(ctx, []string{"--older-than", "8760h", "--out", file}, &out); err != nil || !strings.Contains(out.String(), "nothing to archive") {
		t.Fatalf("an archive of nothing: %q %v", out.String(), err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("an archive of nothing left %d files behind", len(entries))
	}

	out.Reset()
	if err := auditArchiveCommand(ctx, []string{"--older-than", "0s", "--out", file}, &out); err != nil {
		t.Fatalf("archive: %v", err)
	}
	var result archiveResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out.String())
	}
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(written)
	if result.Seals != 1 || result.Entries != 7 || result.Head != head || result.FileSHA256 != hex.EncodeToString(sum[:]) || result.AuditPrincipal == "" {
		t.Fatalf("the report does not describe the file that was written: %+v", result)
	}
	if _, err := os.Stat(file + ".partial"); err == nil {
		t.Fatal("the unfinished file was left beside the finished one")
	}
	// The checkpoint holds the same file digest, so the file can later be shown to be the one.
	var held []byte
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT archive_digest FROM {schema}.audit_checkpoint`)).Scan(&held); err != nil || !bytes.Equal(held, sum[:]) {
		t.Fatalf("the checkpoint does not hold the file's digest: %x %v", held, err)
	}
	if v, err := audit.Verify(ctx); err != nil || !v.Valid || v.Archived != 7 || hex.EncodeToString(v.Head) != head {
		t.Fatalf("the ledger that remains: %+v %v", v, err)
	}

	// Checked with no database: the environment that reaches one is taken away first.
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	out.Reset()
	if err := auditVerifyArchiveCommand([]string{"--head", head, file}, &out); err != nil || !strings.Contains(out.String(), "verified: 1 seals covering 7 entries") ||
		strings.Contains(out.String(), "against itself only") {
		t.Fatalf("the file against the head recorded at sealing: %q %v", out.String(), err)
	}
	out.Reset()
	if err := auditVerifyArchiveCommand([]string{file}, &out); err != nil || !strings.Contains(out.String(), "against itself only") {
		t.Fatalf("a check with no outside digest must say what it did not check: %q %v", out.String(), err)
	}
	out.Reset()
	if err := auditVerifyArchiveCommand([]string{"--head", strings.Repeat("ab", 32), file}, &out); err == nil || !strings.Contains(out.String(), "FAILED") {
		t.Fatalf("the file against another digest: %q %v", out.String(), err)
	}
	altered := filepath.Join(dir, "altered.jsonl")
	if err := os.WriteFile(altered, bytes.Replace(written, []byte("|allowed|"), []byte("|refused|"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := auditVerifyArchiveCommand([]string{"--head", head, altered}, &out); err == nil || !strings.Contains(out.String(), "FAILED: seal") {
		t.Fatalf("an altered file: %q %v", out.String(), err)
	}
	for _, args := range [][]string{nil, {"--head", "not-hex", file}, {file, "extra"}, {"--head", head, filepath.Join(dir, "absent")}, {"--unknown", file}} {
		if err := auditVerifyArchiveCommand(args, io.Discard); err == nil {
			t.Errorf("verify-archive accepted %v", args)
		}
	}

	// What the archive command refuses before it touches anything.
	withEnv(t, env)
	if err := auditArchiveCommand(ctx, []string{"--older-than", "0s", "--out", file}, io.Discard); err == nil || !strings.Contains(err.Error(), "never written over") {
		t.Fatalf("an archive over an existing file: %v", err)
	}
	for _, args := range [][]string{nil, {"--out", file + "2"}, {"--older-than", "0s"}, {"--older-than", "-1h", "--out", file + "2"}, {"--older-than", "0s", "--out", file + "2", "extra"}} {
		if err := auditArchiveCommand(ctx, args, io.Discard); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("archive accepted %v: %v", args, err)
		}
	}
	if err := auditArchiveCommand(ctx, []string{"--older-than", "0s", "--out", filepath.Join(dir, "no-such-directory", "x")}, io.Discard); err == nil {
		t.Error("an archive into a directory that is not there was accepted")
	}
	// The dispatcher sends both to these commands whatever else the environment names.
	if err := dispatch(ctx, quiet(), []string{"audit", "verify-archive"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("the dispatcher does not reach verify-archive: %v", err)
	}
	if err := dispatch(ctx, quiet(), []string{"audit", "archive"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("the dispatcher does not reach archive: %v", err)
	}
}
