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

During a provider outage, inspect formation failures and parked observations, restore the provider
configuration, then unpark work deliberately. Unparking currently exists as the internal
`ObservationStore.Unpark` operation; the operator command/API is still tracked under the formation
recovery backlog. Do not reset its flags independently of the freshness watermark. Parking itself
does not free capacity. Erasure and retention remain data-governance operations and should not be
used merely to hide an operational queue. Raise a limit only with enough resource headroom.

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
