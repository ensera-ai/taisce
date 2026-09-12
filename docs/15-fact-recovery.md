<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Recovering facts and chunks

If derived rows are lost (a bad restore, a mistaken `DELETE`, a damaged table), Taisce can rebuild
them from the source records it keeps. Recovery never calls a model, so what comes back is exactly
what was there before: the same statements, citation IDs, time intervals, corrections and
withdrawals.

## Recover facts

With the operator database connection (`TAISCE_ADMIN_DSN`) set:

```sh
taisce recover facts --project default --limit 100
```

```json
{"examined":100,"restored":37,"repaired":2,"next":"9c0e…","audit_principal":"5b1d…"}
```

- **`restored`** counts facts that were missing and are now back.
- **`repaired`** counts facts that existed but had lost their evidence, history, erasure
  registrations or alias cache entries.
- **`next`**: when present, pass it as `--after` to continue. When it's missing, the pass is done.

```sh
taisce recover facts --project default --limit 100 --after 9c0e…
```

Add `--source <observation-uuid>` to recover from one message only. `--limit` takes 1 to 100. Each
page commits atomically or not at all, and the command gives up after 30 seconds.

## Recover message chunks

Chunks are the pieces of stored messages that passage search reads. To restore them from their
messages:

```sh
taisce recover chunks --project default --limit 100
```

```json
{"examined":100,"restored":100,"repaired":0,"next":{"log_offset":812,"ordinal":1},"audit_principal":"…"}
```

To continue, pass both parts of `next`:

```sh
taisce recover chunks --project default --limit 100 --after-offset 812 --after-ordinal 1
```

A page handles 1 to 100 messages and at most 16 MiB of text. If a page is refused for size, lower
`--limit`. Chunk recovery doesn't need a model endpoint. Existing embeddings are kept; missing ones
are not regenerated.

## Watch out for

- **Back up the sources, not just the facts.** Recovery needs observations, authored input,
  withdrawal records, formation receipts and the extraction settings pinned for each source
  (`source_extraction`). Back them up together. For chunks, back up each message's `chunk_id` with
  its content.
- **Erased and expired data stays gone.** Recovery never brings back anything erasure or retention
  removed.
- **Mismatches refuse the page.** If a source's bytes no longer match its receipt, or existing rows
  conflict with what would be restored, the whole page is refused. Nothing is overwritten.
- **It's a live scan.** Reads keep working during recovery. Rows added behind your cursor need a new
  full pass. This is not a point-in-time backup.
- **Versions can change.** Reconnecting a link to a superseding fact can change a record's version.
  If an edit then gets a version conflict, fetch the record again.
- **Safe to repeat.** If the command's output is lost after it committed, run the same page again.
  Retained records are not rewritten.

## What recovery does not do

- It doesn't run newer extraction rules. For that, [rebuild facts](20-generation-rebuild.md).
- It doesn't restore a lost receipt, evidence that was never recorded, or rows inserted directly
  with SQL.
- It doesn't regenerate reports or embeddings.
- It doesn't recover an entity with no facts, or a name spelling that was never recorded.
- It doesn't recover [agent artifacts](18-agent-artifacts.md), which have no derived copy.

## Extraction settings stay pinned

Each source records a fingerprint of the extraction setup that formed it (`extract/v2:<sha256>`).
Recovery keeps that fingerprint. It never stamps your current model onto old evidence.

Failed and empty extraction attempts pin the setup too. If you change the model or prompt while a
source is still unfinished, its retry is refused. Restore the original setup to finish it. Deleting
the pin is not a supported reset. To reinterpret sources under a new setup on purpose, use
[`taisce rebuild facts` or `taisce rebuild project`](20-generation-rebuild.md).

Taisce doesn't cache model responses. A retry makes a new model call.

## How it works

When formation admits a fact, it also writes a receipt owned by the source message. The receipt holds
the fact's identity, evidence, entity identity and time history. Recovery reads receipts and writes
back whatever is missing.

Entities must match their saved identity exactly: project, canonical name and speaker attribution. A
conflicting existing entity refuses the page; it is never reused or overwritten. Restoring evidence
invalidates affected reports through the normal database trigger. Repeating a completed page
invalidates nothing. Each page writes an audit entry in the same transaction. The `audit_principal`
is a random ID for that run, not a person.
