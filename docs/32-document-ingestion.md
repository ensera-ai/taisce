<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Document ingestion

Tracking: [#91](https://github.com/althunibat/taisce/issues/91). Decision: D125. Code:
`internal/ingest` (reading, segmentation, keys) and `taisce ingest` in `cmd/taisce` (sending, the
manifest).

A document is not a new kind of memory. It is a sequence of turns whose speaker is a third party, sent
through `POST /v1/observations` exactly as an adapter sends a conversation, so every rule about roles,
subjects, sizes, admission and audit applies to it unchanged, and every citation it produces is one a
client could have made. The command adds three things the write contract does not decide: which files
are documents and how one is refused, where a long text is cut so each piece fits one message, and how
a piece names itself so that sending it twice stores it once.

## The contract

**Formats.** `.txt` and `.md` are read as UTF-8 text. `.json` is read as one object with `title`,
`text` and `occurred_at` (RFC 3339), all optional but the text; an unknown field is refused rather
than ignored, because a caller who wrote `occurredAt` has lost a date without being told. The
document's text is the file trimmed of trailing whitespace, or the title, a blank line and the text.
Nothing is decompressed, so an archive is an unsupported format rather than a bomb.

**Refused whole, before anything is sent:** a file over `--max-file-bytes` (default 16 MiB, checked
against the file's size before it is read, and again after, so a file that grew between the two is
caught rather than truncated), text that is not valid UTF-8 or carries a NUL byte, a file with no
text once trimmed, an extension not listed above, and malformed JSON or JSON with trailing content.
A refusal is reported by file name, its manifest carries the reason and no receipts, and the run exits
non-zero after finishing the other files.

**Segments.** The text is cut into segments of at most `--segment-bytes` (default 8 KiB, bounds 256
bytes to the 64 KiB message ceiling). Each cut prefers, in order, the end of a paragraph (a blank
line, kept with the paragraph it closes), the end of a sentence (`. ! ?`, the Arabic `؟ ۔`, the CJK
`。！？`, followed by whitespace, kept with the sentence), whitespace, and finally the last whole code
point that fits. The segments concatenate to the text byte for byte, none is empty, none is cut inside
a code point or a multibyte space. A run of whitespace longer than a segment is refused as
unsegmentable, because a message with no words is refused by the API and the segments after it would
be a document with a hole.

**One segment is one observation** with one message: `group_ordinal` 0, the role the operator names
(`--role tool`, the default, for third-party material; `user` for the person's own writing), the data
subject if `--data-subject` names one, and `occurred_at` from the JSON document, else `--occurred-at`,
else absent so the API stamps ingestion time as it does for a conversation that gives none. One segment
is therefore one model call in formation, one unit of retry, and one unit of citation: evidence is a
byte span against the chunk it indexes (D101), exact within the segment, and the segment's own
`byte_start` in the manifest maps it back to the file.

**Keys.** A segment's idempotency key is a version-5 UUID over the document's SHA-256, the segment's
ordinal, the role and the data subject. The same document sent from another machine, or again after a
failure, produces the same keys, and the API answers each with the receipt it already holds. The same
words as another speaker or under another subject are another observation, as the API would store
them. There is no resume file: the API's idempotency is the resume.

**The manifest.** One JSON file per document in `--manifest-dir` (default `taisce-ingest/`): the
document's name, digest, byte count, role, subject, time, segment ceiling, and per segment its
ordinal, byte range, key, observation id and log offset, with `refused` set when the document stopped.
It holds no text, so it can accompany the documents or travel without them.

**Backpressure.** A full backlog (`429 rate_limited`, D74) is waited out with bounded backoff up to
`--capacity-wait` (default five minutes); the document then stops with the receipts it has, and the
next run carries on from the API's own idempotency. Time is not part of a document's identity, so the
same document sent again with a different `--occurred-at` is the same key with a different body, which
the API refuses as a conflict (`409 idempotency_conflict`); the command reports it by file and stores
nothing. Erase the earlier observation, or send the time the earlier run sent. `--wait` polls
`GET /v1/freshness` until the formed watermark reaches the highest offset sent, reports the parked
count, and exits non-zero when formation did not get there.

**The credential** comes from `TAISCE_TOKEN` and never from a flag, because a flag is in the process
list. The address comes from `--api` or `TAISCE_API`.

## What was measured, and what was not

The segment ceiling is measured rather than guessed. On the news corpus in docs/31, the articles that
overran a two-minute model budget under a twelve-way shared server averaged 10.7 KB, and the rest,
averaging 4.5 KB, did not; 8 KiB sits under the ones that overran with margin, and an operator who has
measured their own model moves it.

Model calls per document are the segment count and are printed in the manifest. Peak memory is one
file at a time, bounded by `--max-file-bytes` plus its segments. Throughput of admission and of
formation on a document corpus is a GPU-day measurement, not one the development machine can give,
and is not claimed here.

Tests to re-run: `internal/ingest` (`TestSegmentsAreTheDocumentAndQuotePositionsRoundTrip`,
`TestTheCutPrefersParagraphThenSentenceThenSpaceThenCodePoint`,
`TestUnsegmentableTextAndBadCeilingsAreRefused`, `TestADocumentIsRefusedWholeBeforeAnythingIsSent`,
`TestAReadDocumentCarriesItsTextTimeAndDigest`, `TestASegmentKeyIsStableAndSensitiveToSpeakerAndSubject`)
and, against the live API, `TestIngestStoresADocumentAsSegmentsExactlyOnceAndRefusesWhole`,
`TestIngestWaitsForCapacityThenResumesFromTheReceiptsItHolds`,
`TestIngestReportsRefusalsByNameAndEndsTheWaitWhenFormed` and
`TestIngestRefusesAMisconfiguredRunBeforeReadingAnything` in `cmd/taisce`.

## Erasing a document

A document observed without a data subject is erased by its sources. The manifest holds each
segment's observation id, and `POST /v1/erasures` takes them as `source_observation_ids` under the
credential's project, with the same receipt as a subject erasure: every projection registered only
to those observations goes, a projection another observation also registered stays, and the
residual is counted against the same predicate in the same transaction. An id from another project
matches nothing. A document observed under a subject is erased with that subject, or by its sources;
either reaches every projection it fed. `TestADocumentObservedWithoutASubjectIsErasedByItsSourcesWithAResidualOfZero`
and `TestADocumentIsErasedByItsSourceObservationsOverTheWire` hold it (D138).

## Limits

Erasure by source names observations; it does not search for them. A document whose manifest was
lost is found through recall's citations, which name the observation each passage came from, and
erased by those. There is no "erase everything without a subject": that would be a project-wide
delete under another name, and a project is dropped by its operator.
