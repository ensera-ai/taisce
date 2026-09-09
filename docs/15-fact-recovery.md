<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Recover recorded facts

Formation retains source-owned receipts for admitted record state, exact evidence, entity identity
and temporal history. Recovery uses those receipts without calling a model. It preserves statements,
citation identifiers, recorded time intervals, authored corrections and withdrawals.

Run with the operator database connection configured:

```sh
taisce recover facts --project default --limit 100
```

The JSON result contains `examined`, `restored`, `repaired`, an optional `next` cursor and `audit_principal`.
When `next` is present, pass it as `--after <fact-uuid>` on the next invocation. No `next` means that
pass reached the end. `--source <observation-uuid>` restricts a pass to one surviving observation.
The command checks project existence and uses a 30-second deadline. A failed page commits nothing.
If output fails after commit, repeat the same page: retained records are not rewritten.

Each page handles 1–100 receipts atomically. Reads continue throughout; completed pages add recovered
records. Successor links reconnect when their targets have been recovered, including across pages.
Recorded validity and knowledge boundaries remain exact even while a successor is absent. A link
repair can change a record's optimistic version, so editors must refresh after a version conflict.

Concurrent additions below a cursor may require a new complete pass. This is a live scan, not a
point-in-time backup or a durable background job. Receipt/source deletion makes a record unavailable
for recovery. Source bytes that no longer agree with the receipt cause refusal, and erased or expired
sources cannot be reconstructed. A foreign source filter returns no records for this project.

This operation restores **missing admitted facts** and their evidence, entity identities and temporal
history. `restored` counts newly recovered fact rows; `repaired` counts retained records whose missing
original evidence, temporal history or erasure registrations were repaired. Support repair itself preserves fact statements,
intervals, versions and human withdrawals. Missing-successor link recovery still has the version
behavior described above. Both original and causal history owners
are registered. Existing conflicting evidence, history, ownership or pipeline stamps cause an atomic
refusal rather than being overwritten. Restoring evidence advances its source revision and invalidates
reports through the normal database mechanism; repeating a complete page does not invalidate them.

Entity endpoints must match the saved identity, including project, canonical/normalized name and
named/speaker attribution (D99). A conflicting retained UUID refuses the page; it is never silently
reused or overwritten. Compatible retained entities keep their identity metadata. D100 extends the
recovery lock to permit alias-cache repair from surviving source-owned name records; a changed cache
counts toward `repaired`. Those records must be backed up with observations and fact receipts. Old
unrecorded spellings cannot be reconstructed. Semantic identity changes remain a separate operation.

Recovery preserves original extraction and pipeline stamps. It does not run newer extraction rules,
restore a lost receipt, regenerate summaries or embeddings, restore unrecorded additional evidence or
entity-only loss, or recover unsupported direct SQL inserts. Back up observations, authored input,
withdrawal instructions and formation receipts together. D88 records the source-receipt boundary and
D95 adds retained-record support repair; #58 tracks the remaining generation workflows.


Extraction configuration and retries
------------------------------------

Runtime extraction evidence uses a configuration fingerprint (`extract/v2:<sha256>`). Recovery
preserves this stamp; it does not stamp the operator's current model onto old evidence. The
`source_extraction` control row is authoritative source metadata and must be backed up with the
observation/receipts, not dropped as a projection. Failed and empty extraction attempts also pin a
configuration. A changed retry is refused; restore the original binary/configuration to finish an
unfinished source. Deleting a pin is not a supported reset. Single-source changed-model generation
cutover is available through `taisce rebuild facts`; it keeps the old interpretation historical and
records lineage. Project fact orchestration is available through `taisce rebuild project` (D98/#124); other
projection rebuilds remain #58. Response caching is tracked in
[ensera-ai/taisce#108](https://github.com/ensera-ai/taisce/issues/108). D96
defines the fingerprint inputs and the limits of model aliases and routing.

## Message chunk recovery

`taisce recover chunks --project p1 --limit 100` restores message chunks from retained messages and
their source-owned UUIDs (D101). Add `--source UUID` to restrict it to one observation. Output contains
`examined`, `restored`, `repaired`, an audit principal and, when another page exists, `next` with
`log_offset` and `ordinal`. Resume with both `--after-offset` and `--after-ordinal`. Without those
flags the command starts before the first message. Each invocation requires the operator database
configuration; it does not need an inference endpoint.

A page permits 1–100 messages and at most 16 MiB of source text. Reduce `--limit` on a size refusal.
The page commits atomically with its audit entry; retry the same cursor if the process loses its
output. Existing conflicting data refuses the whole page. `repaired` counts compatible retained
chunks with missing erasure registrations. Existing embeddings survive; missing embeddings are not
regenerated. Erased/expired messages are never recovered. Back up the message `chunk_id` alongside
the message content; a UUID lost before migration 0044 recorded it is not recoverable.
