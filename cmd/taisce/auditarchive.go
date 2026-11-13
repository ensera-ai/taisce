// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/ledgerarchive"
	"github.com/google/uuid"
)

// `taisce audit archive` moves sealed ranges of the ledger into a file, and `taisce audit
// verify-archive` checks such a file with no database.
//
// # What this file is allowed to decide, and what it must not
//
// It decides the order in which a range leaves: written, made durable, named, and only then
// removed. It does not decide which entries may leave or whether a file verifies — the schema holds
// the first (a checkpoint covers only what is sealed) and the ledgerarchive package the second.
//
// # Why the order is what it is
//
// The one outcome that must not happen is an entry that is in neither the ledger nor a file. So
// the file is complete and on disk under its final name before the transaction that removes the
// range begins. Every failure before that leaves the ledger whole. A failure after it leaves the
// ledger whole and a file beside it that verifies, which is harmless and says so.
//
// # Both commands talk to the database directly, or to none
//
// Neither goes through the management surface. An archive writes a file where the operator is,
// which a request to a service cannot do, and a verification that asked the service whether the
// service's own record was sound would be asking the wrong party.

type archiveResult struct {
	File string `json:"file"`
	// FileSHA256 is what the checkpoint holds for this file. Record it with the head.
	FileSHA256     string `json:"file_sha256"`
	Seals          int    `json:"seals"`
	Entries        int64  `json:"entries"`
	ThroughSeal    int64  `json:"through_seal"`
	ThroughEntry   int64  `json:"through_entry"`
	PreviousDigest string `json:"previous_digest"`
	Head           string `json:"head"`
	AuditPrincipal string `json:"audit_principal"`
}

func auditArchiveCommand(ctx context.Context, args []string, out io.Writer) error {
	const usage = "usage: taisce audit archive --older-than <duration, such as 2160h> --out <file that does not exist>"
	flags := flag.NewFlagSet("audit archive", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	age := flags.Duration("older-than", -1, "archive ranges sealed longer ago than this")
	path := flags.String("out", "", "the file to write")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *age < 0 || *path == "" {
		return errors.New(usage)
	}
	if _, err := os.Lstat(*path); err == nil {
		// Never over another archive: the file that would be replaced may be the only copy of a
		// range that has already left.
		return fmt.Errorf("%s exists, and an archive is never written over a file", *path)
	}
	admin, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	audit := pg.NewAuditStore(admin, schema)

	partial := *path + ".partial"
	file, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create the archive: %w", err)
	}
	discard := func() {
		file.Close()
		os.Remove(partial)
	}
	sum := sha256.New()
	archive, err := audit.Export(ctx, *age, io.MultiWriter(file, sum))
	if errors.Is(err, pg.ErrNothingToArchive) {
		discard()
		fmt.Fprintln(out, "nothing to archive: no sealed range is that old")
		return nil
	}
	if err != nil {
		discard()
		return err
	}
	// Durable under its final name before anything is removed.
	if err := file.Sync(); err != nil {
		discard()
		return fmt.Errorf("make the archive durable: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(partial)
		return fmt.Errorf("close the archive: %w", err)
	}
	if err := os.Rename(partial, *path); err != nil {
		os.Remove(partial)
		return fmt.Errorf("name the archive: %w", err)
	}
	if dir, err := os.Open(filepath.Dir(*path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}

	principal := uuid.NewString()
	if err := audit.Checkpoint(ctx, archive, sum.Sum(nil), principal); err != nil {
		return fmt.Errorf("%w. Nothing was removed from the ledger; %s is a complete export of a range that is still in it", err, *path)
	}
	return json.NewEncoder(out).Encode(archiveResult{
		File: *path, FileSHA256: hex.EncodeToString(sum.Sum(nil)), Seals: archive.Seals, Entries: archive.Entries,
		ThroughSeal: archive.ThroughSeal, ThroughEntry: archive.ThroughEntry,
		PreviousDigest: hex.EncodeToString(archive.PreviousDigest), Head: hex.EncodeToString(archive.Head), AuditPrincipal: principal,
	})
}

func auditVerifyArchiveCommand(args []string, out io.Writer) error {
	const usage = "usage: taisce audit verify-archive [--head <the digest logged when the range was sealed>] <file>"
	flags := flag.NewFlagSet("audit verify-archive", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	given := flags.String("head", "", "a digest recorded outside the database")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		return errors.New(usage)
	}
	var head []byte
	if *given != "" {
		raw, err := hex.DecodeString(*given)
		if err != nil || len(raw) != sha256.Size {
			return fmt.Errorf("--head is not a SHA-256 digest")
		}
		head = raw
	}
	file, err := os.Open(flags.Arg(0))
	if err != nil {
		return fmt.Errorf("open the archive: %w", err)
	}
	defer file.Close()
	v, err := ledgerarchive.Verify(file, head)
	if err != nil {
		return err
	}
	if !v.Valid {
		fmt.Fprintf(out, "FAILED: %s\n", v.Failure)
		return fmt.Errorf("the archive does not verify")
	}
	fmt.Fprintf(out, "verified: %d seals covering %d entries (%d-%d)\nfollows: %x\nhead: %x\n",
		v.Seals, v.Entries, v.FirstEntry, v.LastEntry, v.PreviousDigest, v.Head)
	if head == nil {
		// Said every time. A file that agrees with itself is what anybody able to write a file
		// can produce.
		fmt.Fprint(out, "\nThis file was checked against itself only. Give --head the digest the service logged "+
			"when this range was sealed, or the head of the archive that follows it, to check it against a record the database could not have rewritten.\n")
	}
	return nil
}
