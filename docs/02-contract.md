# The v1 contract

Generated from the surface it describes. Do not edit: run `make contract`.

Every operation is reached over HTTP with a bearer credential, and the credential decides which project the operation acts on — a request never names one.

The version is in the path. It is permanent from the day the first client ships: a field is only ever added, and anything else is a new version.

Requests must contain one complete UTF-8 JSON object followed only by whitespace. Duplicate keys (including case aliases), unknown fields, invalid Unicode escapes and nesting beyond 128 levels are refused with HTTP 400 and `invalid_body`. The 1 MiB limit includes all bytes of the body, including trailing whitespace.

Recall questions are limited to 8192 UTF-8 bytes, 512 distinct candidate names and 256 matching entity/name pairs. Exceeding a limit returns HTTP 400 and `invalid_question`, with no partial bundle. The match count includes the selected speaker. Fact-budget cuts remain successful responses with `truncated: true`.

Observations admit at most 64 messages, 65536 UTF-8 content bytes per message and 262144 content bytes per turn. Exceeding these limits returns HTTP 400 and `invalid_turn` before storage. Busy authentication or project/credential capacity returns HTTP 429 and `rate_limited`, with `Retry-After: 1`. Retry with backoff and the same observation idempotency key; a refusal has no stored receipt. Request concurrency limits are per API process; durable unfinished-turn limits are shared by the instance and also return `rate_limited`. Existing keyed receipts can be replayed at backlog capacity. Health checks remain outside request admission.

Recall `max_characters` must be positive and no larger than the server's configured budget. Omission uses that budget. The count is Unicode code points in returned fact subjects, predicates, objects, statements, source-role labels, quotes, contexts, paths and path predicates. Identifiers, anchors, dates and JSON syntax are excluded. A first fact that cannot fit returns no facts, zero characters and `truncated: true`. This is a content allowance, not a serialized-response or token limit.

Recall `source_roles` defaults to `["user"]`. An explicit nonempty list may contain `user`, `assistant`, `system` and `tool`, without duplicates. Unknown roles, empty lists and invalid budgets return HTTP 400 and `invalid_recall_controls`. There is no `document` role; fetched document text uses its stored message role. Source selection preserves project, subject, erasure and temporal filters. Hops are clamped to 1–2. Every successful response reports effective limits, roles and hops in `controls`, including empty bundles.

Record retraction accepts `records`, an atomic array of 1–20 objects with `id` and `expected_version`. Versions come from inventory, history or citation inspection. Invalid requests return `400 invalid_record_mutation`; any stale or already-withdrawn record returns `409 record_conflict`. Unknown, foreign and erased records return the same `404 not_found`. Batches exceeding 128 supporting evidence rows return `413 record_mutation_limit`. Any refusal leaves the whole batch unchanged.

Retraction closes knowledge at the server time while retaining valid time and exact citations. Current recall excludes the record; earlier knowledge remains inspectable. The response supplies new versions and credential-attributed retraction metadata. Source-backed instructions prevent the same structural claim from reappearing when its source message is re-extracted. A new independent observation can assert the claim again. Retraction is not source erasure. After an uncertain response, inspect the record instead of retrying with a newly fetched version.

Record correction accepts the same atomic `records` batch and support limits, adding `object` (1–1024 UTF-8 bytes), `statement` (1–16384 UTF-8 bytes) and optional `valid_from` per item. Blank, NUL-containing or invalid UTF-8 text is refused. Correction requires current known/valid state; it preserves subject, relation and attribution, withdraws the original and creates a separate authored source and replacement record. Omitted valid-from uses server time. Each result returns the original and replacement IDs, source observation ID and new version. The original's retraction metadata links to the replacement while its source survives. Curated evidence reports `curated/v1` and `authored_by`; its stored claim replays without model extraction. A correction is an accountable assertion, not a truth verification or permission to edit historical transcripts.

## Operations

| Operation | Method | Path | On success |
|---|---|---|---|
| `observe` | POST | `/v1/observations` | 201 |
| `freshness` | GET | `/v1/freshness` | 200 |
| `recall` | POST | `/v1/recalls` | 200 |
| `erase` | POST | `/v1/erasures` | 200 |
| `export` | POST | `/v1/exports` | 200 |
| `citation.resolve` | POST | `/v1/citations/resolve` | 200 |
| `record.list` | POST | `/v1/records/list` | 200 |
| `record.history` | POST | `/v1/records/history` | 200 |
| `record.assert` | POST | `/v1/records/assert` | 200 |
| `record.correct` | POST | `/v1/records/correct` | 200 |
| `record.retract` | POST | `/v1/records/retract` | 200 |

The operation name is the same identifier the audit ledger records, so an entry in an operator's ledger and a call in a client name the same thing.

## `observe`

POST `/v1/observations` → 201

### Request

| Field | Type |
|---|---|
| `idempotency_key` | string |
| `data_subject_id` | string |
| `occurred_at` | timestamp (RFC 3339), optional |
| `messages` | array of {group_ordinal: integer, optional, role: string, content: string, occurred_at: timestamp (RFC 3339), optional} |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `scope` | string |
| `log_offset` | integer |


## `freshness`

GET `/v1/freshness` → 200

Takes no body.

### Response

| Field | Type |
|---|---|
| `scope` | string |
| `stored` | integer, optional |
| `formed` | integer, optional |
| `parked` | integer |


## `recall`

POST `/v1/recalls` → 200

### Request

| Field | Type |
|---|---|
| `data_subject_id` | string |
| `question` | string |
| `as_of` | timestamp (RFC 3339), optional |
| `as_known_at` | timestamp (RFC 3339), optional |
| `max_characters` | integer, optional |
| `source_roles` | array of string |
| `hops` | integer |

### Response

| Field | Type |
|---|---|
| `controls` | {max_characters: integer, max_rows: integer, source_roles: array of string, hops: integer} |
| `anchors` | array of {entity_id: string, name: string, type: string, matched: string} |
| `facts` | array of {fact_id: string, subject: string, predicate: string, object: string, statement: string, confidence: number, valid_from: timestamp (RFC 3339), valid_until: timestamp (RFC 3339), optional, anchored_on: string, source_role: string, hops: integer, path: array of string, via: array of string, co_derived_with: array of string, evidence: {observation_id: string, source_ordinal: integer, quote: string, byte_start: integer, byte_end: integer, context: string, context_start: integer, context_complete: boolean}} |
| `truncated` | boolean |
| `characters` | integer |
| `degraded` | array of string |
| `reach` | {terms: integer, anchored: integer, named_nothing_known: boolean, facts_per_anchor: object of integer} |


## `erase`

POST `/v1/erasures` → 200

### Request

| Field | Type |
|---|---|
| `data_subject_id` | string |
| `reason` | string |

### Response

| Field | Type |
|---|---|
| `request_id` | string |
| `scope` | string |
| `data_subject_id` | string |
| `completed_at` | timestamp (RFC 3339) |
| `deleted` | object of integer |
| `residual` | object of integer |
| `clean` | boolean |


## `export`

POST `/v1/exports` → 200

### Request

| Field | Type |
|---|---|
| `data_subject_id` | string |

### Response

| Field | Type |
|---|---|
| `scope` | string |
| `data_subject_id` | string |
| `sections` | object of array of opaque JSON |
| `rows` | object of integer |


## `citation.resolve`

POST `/v1/citations/resolve` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |
| `after` | {source_observation_id: string, source_ordinal: integer, byte_start: integer}, optional |
| `limit` | integer, optional |

### Response

| Field | Type |
|---|---|
| `version` | string |
| `retraction` | {operation_id: string, principal_id: string, retracted_at: timestamp (RFC 3339), replacement_source_observation_id: string, optional, replacement_record_id: string, optional}, optional |
| `id` | string |
| `superseded_by` | string, optional |
| `supersession_source_observation_id` | string, optional |
| `scope` | string |
| `subject_entity_id` | string, optional |
| `object_entity_id` | string, optional |
| `predicate` | string |
| `statement` | string |
| `confidence` | number |
| `source_role` | string |
| `recorded_at` | timestamp (RFC 3339) |
| `valid` | {from: timestamp (RFC 3339), optional, until: timestamp (RFC 3339), optional, from_inclusive: boolean, until_inclusive: boolean} |
| `known` | {from: timestamp (RFC 3339), optional, until: timestamp (RFC 3339), optional, from_inclusive: boolean, until_inclusive: boolean} |
| `status` | string |
| `evidence` | array of {authored_by: string, optional, source_observation_id: string, source_ordinal: integer, log_offset: integer, role: string, occurred_at: timestamp (RFC 3339), quote: string, byte_start: integer, byte_end: integer, extractor_version: string, context: string, context_byte_start: integer, context_byte_end: integer, context_complete: boolean} |
| `next` | {source_observation_id: string, source_ordinal: integer, byte_start: integer}, optional |


## `record.list`

POST `/v1/records/list` → 200

### Request

| Field | Type |
|---|---|
| `data_subject_id` | string |
| `after` | {id: string}, optional |
| `limit` | integer, optional |

### Response

| Field | Type |
|---|---|
| `records` | array of {version: string, id: string, subject_entity_id: string, optional, object_entity_id: string, optional, predicate: string, statement_preview: string, statement_bytes: integer, preview_truncated: boolean, source_role: string, recorded_at: timestamp (RFC 3339), status: string} |
| `next` | {id: string}, optional |


## `record.history`

POST `/v1/records/history` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |
| `before` | {known_until: timestamp (RFC 3339), history_id: string}, optional |
| `limit` | integer, optional |

### Response

| Field | Type |
|---|---|
| `version` | string |
| `retraction` | {operation_id: string, principal_id: string, retracted_at: timestamp (RFC 3339), replacement_source_observation_id: string, optional, replacement_record_id: string, optional}, optional |
| `id` | string |
| `current` | {valid: {from: timestamp (RFC 3339), optional, until: timestamp (RFC 3339), optional, from_inclusive: boolean, until_inclusive: boolean}, known: {from: timestamp (RFC 3339), optional, until: timestamp (RFC 3339), optional, from_inclusive: boolean, until_inclusive: boolean}} |
| `previous` | array of {history_id: string, valid: {from: timestamp (RFC 3339), optional, until: timestamp (RFC 3339), optional, from_inclusive: boolean, until_inclusive: boolean}, known: {from: timestamp (RFC 3339), optional, until: timestamp (RFC 3339), optional, from_inclusive: boolean, until_inclusive: boolean}, ended_by_observation_id: string} |
| `next` | {known_until: timestamp (RFC 3339), history_id: string}, optional |


## `record.assert`

POST `/v1/records/assert` → 200

### Request

| Field | Type |
|---|---|
| `records` | array of {idempotency_key: string, data_subject_id: string, subject: string, predicate: string, object: string, statement: string, valid_from: timestamp (RFC 3339)} |

### Response

| Field | Type |
|---|---|
| `records` | array of {id: string, version: string, source_observation_id: string, replayed: boolean} |


## `record.correct`

POST `/v1/records/correct` → 200

### Request

| Field | Type |
|---|---|
| `records` | array of {id: string, expected_version: string, object: string, statement: string, valid_from: timestamp (RFC 3339)} |

### Response

| Field | Type |
|---|---|
| `records` | array of {original_id: string, id: string, version: string, source_observation_id: string} |


## `record.retract`

POST `/v1/records/retract` → 200

### Request

| Field | Type |
|---|---|
| `records` | array of {id: string, expected_version: string} |

### Response

| Field | Type |
|---|---|
| `records` | array of {id: string, version: string, operation_id: string, principal_id: string, retracted_at: timestamp (RFC 3339), replacement_source_observation_id: string, optional, replacement_record_id: string, optional} |


## Refusals

A refusal is a JSON body of the shape `{"error": {"code": "…", "message": "…"}}`. Branch on the code; the message is for a person reading a log and may change.

Any operation may answer with any of these.

| Code |
|---|
| `unauthenticated` |
| `invalid_body` |
| `invalid_turn` |
| `invalid_question` |
| `no_subject` |
| `internal` |
| `idempotency_conflict` |
| `rate_limited` |
| `invalid_citation` |
| `citation_limit` |
| `not_found` |
| `forbidden` |
| `invalid_record_page` |
| `invalid_recall_controls` |
| `invalid_record_mutation` |
| `record_conflict` |
| `record_mutation_limit` |

## Liveness

`GET /health` answers `200` with `{"status": "ok"}`. It takes no credential and reports nothing about the instance.

`GET /ready` takes no credential and returns `200` with `{"status": "ready"}` or `503` with `{"status": "unavailable"}`. Dependency checks have a one-second total budget; only one runs at a time and results are cached for one second. No error, count or configuration is returned. Serving readiness checks memory and registry access. `TAISCE_REQUIRE_FORMATION=true` additionally requires a worker heartbeat within 330 seconds; the default permits intentionally API-only operation. This proves responsiveness, not inference quality or completion of every project. Worker loopback probes check their own processing progress and memory database access.
