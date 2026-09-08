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

Recovery preserves original extraction and pipeline stamps. It does not run newer extraction rules,
restore a lost receipt, regenerate summaries or embeddings, restore unrecorded additional evidence or
entity-only loss, or recover unsupported direct SQL inserts. Back up observations, authored input,
withdrawal instructions and formation receipts together. D88 records the source-receipt boundary and
D95 adds retained-record support repair; #58 tracks the remaining generation workflows.
