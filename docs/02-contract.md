# The v1 contract

Generated from the surface it describes. Do not edit: run `make contract`.

Every operation is reached over HTTP with a bearer credential, and the credential decides which project the operation acts on — a request never names one.

The version is in the path. It is permanent from the day the first client ships: a field is only ever added, and anything else is a new version.

## Operations

| Operation | Method | Path | On success |
|---|---|---|---|
| `observe` | POST | `/v1/observations` | 201 |
| `freshness` | GET | `/v1/freshness` | 200 |
| `recall` | POST | `/v1/recalls` | 200 |
| `erase` | POST | `/v1/erasures` | 200 |
| `export` | POST | `/v1/exports` | 200 |

The operation name is the same identifier the audit ledger records, so an entry in an operator's ledger and a call in a client name the same thing.

## `observe`

POST `/v1/observations` → 201

### Request

| Field | Type |
|---|---|
| `data_subject_id` | string |
| `occurred_at` | timestamp (RFC 3339), optional |
| `messages` | array of {role: string, content: string, occurred_at: timestamp (RFC 3339), optional} |

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
| `stored` | integer |
| `formed` | integer, optional |
| `parked` | integer |


## `recall`

POST `/v1/recalls` → 200

### Request

| Field | Type |
|---|---|
| `question` | string |

### Response

| Field | Type |
|---|---|
| `anchors` | array of {entity_id: string, name: string, type: string, matched: string} |
| `facts` | array of {fact_id: string, subject: string, predicate: string, object: string, statement: string, confidence: number, anchored_on: string, source_role: string, co_derived_with: array of string, evidence: {observation_id: string, source_ordinal: integer, quote: string, byte_start: integer, byte_end: integer}} |
| `truncated` | boolean |


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

## Liveness

`GET /health` answers `200` with `{"status": "ok"}`. It takes no credential and reports nothing about the instance.
