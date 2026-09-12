<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Managing the ingestion backlog

Every conversation turn you send is stored at once and formed into memory later. The backlog budget
caps how many unformed turns can wait, so a model outage fills a known amount of space instead of the
whole disk.

## Check the backlog

Connect as the operator (`TAISCE_ADMIN_DSN`). The examples use the default `memory` schema; use your
`TAISCE_SCHEMA` value if you changed it.

```sql
SELECT pending, max_pending, max_pending_per_project FROM memory.ingestion_budget;
SELECT scope, pending FROM memory.ingestion_project_usage ORDER BY scope;
```

`taisce health` shows the same totals without SQL. See [readiness and health](07-operational-health.md).

## Change the limits

The defaults are 4096 unformed turns across the instance and 512 per project. Run the update in its
own transaction:

```sql
UPDATE memory.ingestion_budget
SET max_pending = 8192, max_pending_per_project = 1024
WHERE singleton;
```

Both limits must be between 1 and 1,000,000, and the per-project limit cannot be larger than the
global one. Pick limits from the disk space and model capacity you actually have. They are a cap, not
a disk reservation.

## What clients see at capacity

When the backlog is full, `POST /v1/observations` returns `429` with the error code `rate_limited`
and the header `Retry-After: 1`. Nothing from the refused request is kept.

- Retry with backoff and **the same idempotency key**.
- A turn that was already accepted under that key still returns its original receipt, even while
  the backlog is full.
- If a request timed out, you don't know whether it committed. Retry with the same key rather than
  inventing a new one.

## Recover parked turns

A turn the worker could not form after several attempts is *parked*. This usually means the model
provider was down. Fix the provider first, then look at what is parked:

```sh
taisce formation parked --project my_project --limit 100
```

```json
{"items":[{"observation_id":"550e8400-e29b-41d4-a716-446655440000","log_offset":123,"formation_attempts":3,"parked_at":"2026-09-10T14:02:11Z"}],"next_after":123}
```

The listing contains metadata only: no message content, subject IDs or provider errors. `--limit`
accepts 1 to 200. To get the next page, pass `next_after` as `--after`:

```sh
taisce formation parked --project my_project --after 123 --limit 100
```

Retry one turn by name. Put the flags before the UUID:

```sh
taisce formation unpark --project my_project 550e8400-e29b-41d4-a716-446655440000
```

```json
{"changed":true,"audit_principal":"9d1c2f0e-7a4b-4c1e-9a55-3f0f6b1c2d7e"}
```

Both commands need `TAISCE_ADMIN_DSN` and give up after ten seconds.

## Watch out for

- **Parking does not free space.** A parked turn still counts against the budget until it forms, or
  until erasure or retention removes it.
- **Don't edit the counters.** Never update `pending`, delete reservations, or truncate the
  accounting tables to clear a backlog. The counters must match the turns they count.
- **Lowering a limit keeps what is there.** Turns already accepted stay. New writes are refused
  until usage drops below the new limit.
- **Don't use erasure to clear a queue.** Erasure and retention are data-governance tools, not a way
  to hide a backlog.
- **The listing is live.** A turn parked behind your cursor only shows up on a fresh scan. Start
  again without `--after` to see it.
- **Unpark can happen twice safely.** Running it again returns `changed: false`. If the provider is
  still failing, the worker may park the turn again.
- **Suspending a project does not pause formation.** Suspension blocks client access only.
- **The audit principal is not a person.** `audit_principal` is a random ID for that run of the
  command. Who ran it is known only from your own access controls on the operator connection.

## Check the accounting

To confirm the counters match reality, run this as one statement so all three counts come from the
same snapshot:

```sql
SELECT (SELECT pending FROM memory.ingestion_budget) AS accounted,
       (SELECT count(*) FROM memory.ingestion_reservation) AS reservations,
       (SELECT count(*) FROM memory.observation WHERE kind='turn' AND formed_at IS NULL) AS unfinished;
```

If they disagree, stop ingestion and investigate before changing anything.

## How it works

Each unformed turn holds one reservation row. Formation, subject erasure and retention release the
reservation in the same transaction that finishes or removes the turn, so a turn can't finish without
its space coming back. The counters live in PostgreSQL, so every API process on the instance shares
one budget. Request rate limits, by contrast, are per process.

The budget caps unformed work only. Formed memory and the audit log grow separately, so plan their
storage separately.

Unparking one turn also fixes the project's freshness watermark in the same transaction, and writes
a `formation.unpark` audit entry holding the project, the result and a count. The entry never holds
the observation or subject ID. If writing the audit entry fails, the unpark rolls back.
