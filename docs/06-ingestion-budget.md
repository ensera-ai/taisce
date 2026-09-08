<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Operating the ingestion backlog budget

An unfinished conversation turn occupies one durable reservation. This includes parked turns.
Formation, subject erasure and retention release reservations atomically; changing a limit does
not delete or complete work. D74 records the design and limits.

Use the instance's operator database connection for configuration. The examples use the default
`memory` namespace; substitute the configured instance namespace when it differs. Runtime service
roles may inspect these tables but cannot change policy or counters.

```sql
SELECT pending, max_pending, max_pending_per_project FROM memory.ingestion_budget;
SELECT scope, pending FROM memory.ingestion_project_usage ORDER BY scope;
```

Defaults are 4096 unfinished turns globally and 512 per project. Set limits according to measured
storage headroom and provider recovery capacity; they are not a disk reservation. Use a standalone
operator transaction for the policy update:

```sql
UPDATE memory.ingestion_budget
SET max_pending = 4096, max_pending_per_project = 512
WHERE singleton;
```

Both limits must be positive and at most one million; the project limit cannot exceed the global
limit. Lowering a limit below current usage preserves accepted turns and refuses new writes until
usage falls below it. Migration 0025 likewise preserves pre-existing backlog above the defaults.
Do not edit `pending`, delete reservations, or truncate the accounting tables to clear a backlog.

At capacity, observation writes return `429 rate_limited` with `Retry-After: 1`. Clients should retain
the same idempotency key and retry with backoff. An already-accepted keyed observation can still
return its original receipt while capacity is full. A capacity refusal rolls back the entire new
write, including its receipt and log offset. A timeout or connection failure can have an uncertain
commit outcome; retry with the same key instead of inventing a new one.

During a provider outage, restore the provider configuration, inspect parked observations and
unpark work deliberately. With the instance operator connection configured as `TAISCE_ADMIN_DSN`:

```sh
taisce formation parked --project my_project --limit 100
taisce formation parked --project my_project --after 123 --limit 100
taisce formation unpark --project my_project 550e8400-e29b-41d4-a716-446655440000
```

The listing returns JSON metadata: observation UUID, log offset, attempt count and parked timestamp.
It never selects memory content, subject identifiers or stored provider errors. Page size is 1–200;
omit `--after` for the first page and use `next_after` from the response for the next one. This is a
live scan: newly parked turns behind a cursor require starting a fresh scan. Commands have a ten
second deadline, including connecting and waiting for locks. Put flags before the observation UUID.

Recovery changes only the named turn in the named project and corrects the freshness watermark in
the same transaction. It records a `formation.unpark` audit entry with the project, result and count.
The returned `audit_principal` is a generated invocation UUID, not the identity of an authenticated
human; access to the operator database connection supplies the authority. No observation or subject
identifier is retained in this audit entry. Human attribution depends on the operator's external
access/session controls. A failed audit insert rolls back recovery. Repeating a completed recovery
returns `changed: false`; if the command times out or output fails after commit, repeat the same
project/UUID to inspect the current result safely. A worker can subsequently park the turn again if
the provider is still failing. Project suspension blocks client access; it does not pause background
formation. Recovery does not change the project's suspension state.

Do not reset flags independently of the freshness watermark. Parking and unparking do not free
capacity. Erasure and retention remain data-governance operations and should not be used merely to
hide an operational queue. Raise a limit only with enough resource headroom.

For an accounting check, compare the global counter, reservation count and unfinished-turn count:

```sql
SELECT (SELECT pending FROM memory.ingestion_budget) AS accounted,
       (SELECT count(*) FROM memory.ingestion_reservation) AS reservations,
       (SELECT count(*) FROM memory.observation WHERE kind='turn' AND formed_at IS NULL) AS unfinished;
```

Run the check as one statement so concurrent commits cannot give it three different snapshots.
If counts disagree, stop ingestion and investigate owner-level changes or a defect; do not reset
counters while writers are active. Runtime invariants refuse inconsistent release operations.

Backlog reservations are shared across API processes using the instance namespace. Request
concurrency/authentication limits are still process-local. Completed memory and the audit ledger
have separate retention/storage costs; a bounded backlog does not cap total database size.
