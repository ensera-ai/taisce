<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Document ingestion

`taisce ingest` loads files into memory: notes, articles, exported chats, anything that is text. It
cuts each file into pieces, sends each piece as an observation, and writes a manifest so you can find
and erase them later.

A document is not a new kind of memory. Each piece is sent through `POST /v1/observations`, exactly as
an adapter sends a conversation turn. So every rule about roles, subjects, sizes, backlog limits and
audit applies unchanged, and every citation that comes out is one a client could have made.

## Load some files

```sh
export TAISCE_API=http://127.0.0.1:8080
export TAISCE_TOKEN=<project-write-credential>

taisce ingest --role tool --wait 10m ./articles/*.md ./notes/meeting.txt
```

Name each file you want to send; the command does not read a directory for you, but your shell can
expand a pattern. The credential comes only from `TAISCE_TOKEN`, never from a flag, because a flag shows up in the
process list. The address comes from `--api` or `TAISCE_API`.

Flags:

| Flag | Default | What it does |
|---|---|---|
| `--role` | `tool` | Who is speaking. `tool` for third-party material, `user` for the person's own writing |
| `--data-subject` | none | Store under this data subject. Without one, the document belongs to the whole project |
| `--occurred-at` | none | RFC 3339 time for documents that do not give their own |
| `--segment-bytes` | 8 KiB | Largest piece, in UTF-8 bytes. One piece is one model call. Allowed range is 256 bytes to 64 KiB |
| `--max-file-bytes` | 16 MiB | Refuse a file larger than this, before reading it |
| `--manifest-dir` | `taisce-ingest` | Where one manifest per document is written |
| `--capacity-wait` | 5 minutes | How long to wait for backlog space before giving up on a document |
| `--wait` | 0 (off) | After sending, wait up to this long for formation to catch up |

## What files it reads

- `.txt` and `.md` are read as UTF-8 text.
- `.json` is read as one object with `title`, `text` and `occurred_at` (RFC 3339). Only `text` is
  required. An unknown field is refused rather than ignored, so a typo like `occurredAt` does not
  silently lose a date.

The document's text is the file with trailing whitespace trimmed, or, for JSON, the title, a blank
line and the text. Nothing is decompressed, so an archive is simply an unsupported format.

## What gets refused

A file is refused whole, before anything is sent, when:

- it is larger than `--max-file-bytes` (checked before reading and again after, so a file that grew
  in between is caught, not cut);
- its text is not valid UTF-8 or contains a NUL byte;
- it is empty after trimming;
- its extension is not one of the above;
- its JSON is malformed or has trailing content.

A refused file is reported by name. Its manifest records the reason and no receipts. The run finishes
the other files and then exits non-zero.

## How a file is cut

Each piece is at most `--segment-bytes`. Taisce looks for a place to cut in this order:

1. the end of a paragraph (a blank line, kept with the paragraph before it);
2. the end of a sentence (`. ! ?`, the Arabic `؟ ۔`, or the CJK `。！？`, followed by whitespace);
3. any whitespace;
4. the last whole character that fits.

Put the pieces back together and you get the original text byte for byte. No piece is empty, and no
piece is cut inside a character. A run of whitespace longer than one piece is refused as
unsegmentable, because the API refuses a message with no words, and the pieces after it would leave
a hole in the document.

## What one piece becomes

One piece is one observation with one message:

- `group_ordinal` 0;
- the role you chose;
- the data subject, if you named one;
- `occurred_at` from the JSON document, else `--occurred-at`, else nothing, in which case the API
  stamps the time it received it, as it does for a conversation.

So one piece is one model call in formation, one unit to retry, and one unit of citation. Evidence is
a byte range inside the piece, and the piece's `byte_start` in the manifest maps it back to the file.

## Sending twice stores once

Each piece's idempotency key is built from the document's SHA-256, the piece's position, the role and
the data subject. Send the same document again, from any machine, and you get the same keys, and the
API answers each with the receipt it already has. There is no resume file: the API's idempotency is
the resume.

The same words under another role or another subject are a different observation, just as they would
be over the API.

The time is not part of the key, and it is not part of what the API compares when it sees a key
again. If you send the same document again with a different `--occurred-at`, the API answers with the
observation it already holds, and the first time stands. To correct a time, erase the earlier
observation and send the document again.

## The manifest

One JSON file per document in `--manifest-dir`. It holds the document's name, digest, byte count,
role, subject, time and piece size, and for each piece its position, byte range, key, observation ID
and log offset. It sets `refused` when the document stopped early. It holds no text, so you can keep
it with the documents or apart from them.

## When the backlog is full

If the API answers `429 rate_limited` because the backlog is full, the command waits and retries with
backoff, up to `--capacity-wait`. Then it stops that document with the receipts it has. The next run
picks up where it left off, thanks to idempotency. See
[The ingestion backlog budget](06-ingestion-budget.md).

With `--wait`, the command polls `GET /v1/freshness` until formation reaches the highest offset it
sent, reports how many turns were parked, and exits non-zero if formation did not get there in time.

## Erasing a document

Use the manifest. Each piece's observation ID is in it. Send them to `POST /v1/erasures` as
`source_observation_ids`, with a credential for the same project:

```http
POST /v1/erasures
Authorization: Bearer <project-write-credential>
Content-Type: application/json

{"source_observation_ids":["<observation-id>","<observation-id>"]}
```

You get the same receipt as a subject erasure:

- everything built only from those observations is removed;
- anything another observation also supports stays;
- what is left over is counted in the same transaction.

An ID from another project matches nothing. A document stored under a data subject can be erased
with that subject, or by its sources; either way reaches everything it fed.

If you lost the manifest, recall citations name the observation each passage came from, and you can
erase by those. There is no "erase everything without a subject": that would be deleting a whole
project under another name, and an operator drops a project directly.

## What was measured, and what was not

The 8 KiB default comes from a measurement. On the news corpus in
[Vocabulary coverage](31-vocabulary-coverage-ap-news.md), the articles that ran past a two-minute
model timeout, on a server shared twelve ways, averaged 10.7 KB; the rest, averaging 4.5 KB, did not.
8 KiB sits safely below the ones that ran over. If you have measured your own model, change it.

The number of model calls per document is the number of pieces, and the manifest shows it. Peak
memory is one file at a time, bounded by `--max-file-bytes` plus its pieces. Throughput for loading
and forming a document corpus has not been measured on steady hardware and is not claimed here.

## How it is tested

In `internal/ingest`: `TestSegmentsAreTheDocumentAndQuotePositionsRoundTrip`,
`TestTheCutPrefersParagraphThenSentenceThenSpaceThenCodePoint`,
`TestUnsegmentableTextAndBadCeilingsAreRefused`, `TestADocumentIsRefusedWholeBeforeAnythingIsSent`,
`TestAReadDocumentCarriesItsTextTimeAndDigest` and
`TestASegmentKeyIsStableAndSensitiveToSpeakerAndSubject`.

Against a live API, in `cmd/taisce`: `TestIngestStoresADocumentAsSegmentsExactlyOnceAndRefusesWhole`,
`TestIngestWaitsForCapacityThenResumesFromTheReceiptsItHolds`,
`TestIngestReportsRefusalsByNameAndEndsTheWaitWhenFormed` and
`TestIngestRefusesAMisconfiguredRunBeforeReadingAnything`.

Erasure by source: `TestADocumentObservedWithoutASubjectIsErasedByItsSourcesWithAResidualOfZero` and
`TestADocumentIsErasedByItsSourceObservationsOverTheWire`.

The code is in [`internal/ingest`](../internal/ingest/) (reading, cutting, keys) and
[`cmd/taisce/ingest.go`](../cmd/taisce/ingest.go) (sending, the manifest).
