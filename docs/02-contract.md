# The v1 contract

Generated from the surface it describes. Do not edit: run `make contract`.

Every operation is reached over HTTP with a bearer credential, and the credential decides which project the operation acts on — a request never names one.

The version is in the path. It is permanent from the day the first client ships: a field is only ever added, and anything else is a new version.

Version 1 is frozen. The freeze is `internal/api/testdata/contract-v1.json`: every operation, its method, path and success status, every request and response field with its type, and every refusal code listed there is served by every later build of this version. What may change: a response field may be added, a request field may be added if a request without it is still accepted, a refusal code may be added, and an operation may be added. Anything else is `/v2`, served beside `/v1`, and `/v1` stops only by a decision recorded in the register, never by a patch.

Requests must contain one complete UTF-8 JSON object followed only by whitespace. Duplicate keys (including case aliases), unknown fields, invalid Unicode escapes and nesting beyond 128 levels are refused with HTTP 400 and `invalid_body`. The 1 MiB limit includes all bytes of the body, including trailing whitespace.

Recall questions are limited to 8192 UTF-8 bytes, 512 distinct candidate names and 256 matching entity/name pairs. Exceeding a limit returns HTTP 400 and `invalid_question`, with no partial bundle. The match count includes the selected speaker. Fact-budget cuts remain successful responses with `truncated: true`.

Observations admit at most 64 messages, 65536 UTF-8 content bytes per message and 262144 content bytes per turn. Exceeding these limits returns HTTP 400 and `invalid_turn` before storage. Busy authentication or project/credential capacity returns HTTP 429 and `rate_limited`, with `Retry-After: 1`. Retry with backoff and the same observation idempotency key; a refusal has no stored receipt. Request concurrency limits are per API process; durable unfinished-turn limits are shared by the instance and also return `rate_limited`. Existing keyed receipts can be replayed at backlog capacity. Health checks remain outside request admission.

Recall `max_characters` must be positive and no larger than the server's configured budget. Omission uses that budget. The count is Unicode code points in returned fact subjects, predicates, objects, statements, source-role labels, quotes, contexts, paths and path predicates. Identifiers, anchors, dates and JSON syntax are excluded. A first fact that cannot fit returns no facts, zero characters and `truncated: true`. This is a content allowance, not a serialized-response or token limit.

Recall `source_roles` defaults to `["user"]`. An explicit nonempty list may contain `user`, `assistant`, `system` and `tool`, without duplicates. Unknown roles, empty lists and invalid budgets return HTTP 400 and `invalid_recall_controls`. There is no `document` role; fetched document text uses its stored message role. Source selection preserves project, subject, erasure and temporal filters. Hops are clamped to 1–2. Every successful response reports effective limits, roles and hops in `controls`, including empty bundles.

Subjects are optional project-authorized identity bookkeeping. Register with a required UUID `idempotency_key`, optional exact `external_reference` (at most 1024 UTF-8 bytes) and `label` (at most 256). Registration returns a random stable UUID to use as `data_subject_id`. Get accepts exactly one of `id` and `external_reference`; list defaults to 20 and permits 1–100 entries, with `after` taken from `next.id`. Unknown, foreign, erased and inactive unused subjects return `404 not_found`. Invalid fields return `400 invalid_subject`.

Subject updates require `id` and `expected_version` and replace the current reference and label; omitted/empty metadata clears it. References are exact and case-sensitive, unique within a project; there is no automatic normalization or merging. An immediate identical update retry is replayed; stale competing versions, conflicting references and changed/erased registration keys return `409 subject_conflict`. Original registration retries return current metadata. Use the stable UUID for memory operations; rotating metadata never rewrites attribution. Read-only keys can get/list but cannot register/update. Export/erasure include the registry and live retry metadata.

A subject's `inactive_after` is stamped from project retention at registration. A null value means retain until erasure. Once due, a mapping is unavailable and eligible for physical expiry only when no attributed observation remains, including artifact sources. Updates do not extend this deadline. Expiry/erasure clears mapping and retry payload metadata but retains a retry-key digest tombstone. New collection requires a new registration key and receives a new subject ID. Existing application-managed opaque IDs remain valid; the registry does not authenticate a human or grant subject-specific permissions. See docs/19-subject-registry.md.

Record retraction accepts `records`, an atomic array of 1–20 objects with `id` and `expected_version`. Versions come from inventory, history or citation inspection. Invalid requests return `400 invalid_record_mutation`; any stale or already-withdrawn record returns `409 record_conflict`. Unknown, foreign and erased records return the same `404 not_found`. Batches exceeding 128 supporting evidence rows return `413 record_mutation_limit`. Any refusal leaves the whole batch unchanged.

Retraction closes knowledge at the server time while retaining valid time and exact citations. Current recall excludes the record; earlier knowledge remains inspectable. The response supplies new versions and credential-attributed retraction metadata. Source-backed instructions prevent the same structural claim from reappearing when its source message is re-extracted. A new independent observation can assert the claim again. Retraction is not source erasure. After an uncertain response, inspect the record instead of retrying with a newly fetched version.

Record correction accepts the same atomic `records` batch and support limits, adding `object` (1–1024 UTF-8 bytes), `statement` (1–16384 UTF-8 bytes) and optional `valid_from` per item. Blank, NUL-containing or invalid UTF-8 text is refused. Correction requires current known/valid state; it preserves subject, relation and attribution, withdraws the original and creates a separate authored source and replacement record. Omitted valid-from uses server time. Each result returns the original and replacement IDs, source observation ID and new version. The original's retraction metadata links to the replacement while its source survives. Curated evidence reports `curated/v1` and `authored_by`; its stored claim replays without model extraction. A correction is an accountable assertion, not a truth verification or permission to edit historical transcripts.

## Operations

| Operation | Method | Path | On success |
|---|---|---|---|
| `message.get` | POST | `/v1/messages/get` | 200 |
| `embedding.search` | POST | `/v1/passages/search` | 200 |
| `entity_embedding.search` | POST | `/v1/entities/candidates` | 200 |
| `report_embedding.search` | POST | `/v1/reports/candidates` | 200 |
| `entity.list` | POST | `/v1/entities/list` | 200 |
| `entity.get` | POST | `/v1/entities/get` | 200 |
| `subject.register` | POST | `/v1/subjects/register` | 200 |
| `subject.get` | POST | `/v1/subjects/get` | 200 |
| `subject.list` | POST | `/v1/subjects/list` | 200 |
| `subject.update` | POST | `/v1/subjects/update` | 200 |
| `artifact.put` | POST | `/v1/artifacts/put` | 200 |
| `artifact.get` | POST | `/v1/artifacts/get` | 200 |
| `artifact.list` | POST | `/v1/artifacts/list` | 200 |
| `artifact.delete` | POST | `/v1/artifacts/delete` | 200 |
| `observe` | POST | `/v1/observations` | 201 |
| `freshness` | GET | `/v1/freshness` | 200 |
| `recall` | POST | `/v1/recalls` | 200 |
| `context.assemble` | POST | `/v1/contexts` | 200 |
| `erase` | POST | `/v1/erasures` | 200 |
| `export` | POST | `/v1/exports` | 200 |
| `citation.resolve` | POST | `/v1/citations/resolve` | 200 |
| `notification.register` | POST | `/v1/notifications/endpoints/register` | 200 |
| `notification.list` | POST | `/v1/notifications/endpoints/list` | 200 |
| `notification.disable` | POST | `/v1/notifications/endpoints/disable` | 200 |
| `notification.deliveries` | POST | `/v1/notifications/deliveries/list` | 200 |
| `entity.purge` | POST | `/v1/entities/purge` | 200 |
| `record.list` | POST | `/v1/records/list` | 200 |
| `record.history` | POST | `/v1/records/history` | 200 |
| `record.assert` | POST | `/v1/records/assert` | 200 |
| `record.correct` | POST | `/v1/records/correct` | 200 |
| `record.retract` | POST | `/v1/records/retract` | 200 |
| `feedback.record` | POST | `/v1/feedback/record` | 200 |
| `feedback.list` | POST | `/v1/feedback/list` | 200 |
| `feedback.promote` | POST | `/v1/feedback/promote` | 200 |

The operation name is the same identifier the audit ledger records, so an entry in an operator's ledger and a call in a client name the same thing.

## `message.get`

POST `/v1/messages/get` → 200

### Request

| Field | Type |
|---|---|
| `chunk_id` | string |
| `byte_start` | integer |
| `byte_limit` | integer, optional |
| `expected_digest` | string |

### Response

| Field | Type |
|---|---|
| `chunk_id` | string |
| `source_id` | string |
| `ordinal` | integer |
| `role` | string |
| `occurred_at` | timestamp (RFC 3339) |
| `content_digest` | string |
| `message_bytes` | integer |
| `byte_start` | integer |
| `byte_end` | integer |
| `text` | string |
| `complete` | boolean |
| `next_byte_start` | integer, optional |


## `embedding.search`

POST `/v1/passages/search` → 200

### Request

| Field | Type |
|---|---|
| `question` | string |
| `limit` | integer, optional |
| `candidates` | integer |
| `data_subject_id` | string |
| `source_role` | string |

### Response

| Field | Type |
|---|---|
| `generation_id` | string |
| `through_offset` | integer |
| `covered_through_offset` | integer |
| `build_state` | string |
| `approximate` | boolean |
| `passages` | array of {chunk_id: string, source_id: string, ordinal: integer, role: string, preview: string, similarity: number, occurred_at: timestamp (RFC 3339), message_bytes: integer, preview_complete: boolean, preview_byte_end: integer, content_digest: string} |


## `entity_embedding.search`

POST `/v1/entities/candidates` → 200

### Request

| Field | Type |
|---|---|
| `question` | string |
| `limit` | integer, optional |
| `candidates` | integer |

### Response

| Field | Type |
|---|---|
| `generation_id` | string |
| `target_count` | integer |
| `build_state` | string |
| `approximate` | boolean |
| `candidates` | array of {entity_id: string, identity_kind: string, name_preview: string, name_bytes: integer, name_truncated: boolean, similarity: number} |


## `report_embedding.search`

POST `/v1/reports/candidates` → 200

### Request

| Field | Type |
|---|---|
| `question` | string |
| `limit` | integer, optional |
| `candidates` | integer |

### Response

| Field | Type |
|---|---|
| `generation_id` | string |
| `target_count` | integer |
| `build_state` | string |
| `approximate` | boolean |
| `candidates` | array of {report_id: string, community_id: string, title_preview: string, title_bytes: integer, title_truncated: boolean, summary_preview: string, summary_bytes: integer, summary_truncated: boolean, importance: number, similarity: number} |


## `entity.list`

POST `/v1/entities/list` → 200

### Request

| Field | Type |
|---|---|
| `after` | {id: string}, optional |
| `limit` | integer, optional |

### Response

| Field | Type |
|---|---|
| `entities` | array of {id: string, identity_kind: string, name_preview: string, name_bytes: integer, name_truncated: boolean, alias_count: integer, first_seen_at: timestamp (RFC 3339)} |
| `next` | {id: string}, optional |


## `entity.get`

POST `/v1/entities/get` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `identity_kind` | string |
| `canonical_name` | string |
| `aliases` | array of string |
| `speaker_subject_id` | string, optional |
| `first_seen_at` | timestamp (RFC 3339) |


## `subject.register`

POST `/v1/subjects/register` → 200

### Request

| Field | Type |
|---|---|
| `idempotency_key` | string |
| `external_reference` | string |
| `label` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `version` | string |
| `external_reference` | string |
| `label` | string |
| `updated_by` | string |
| `created_at` | timestamp (RFC 3339) |
| `updated_at` | timestamp (RFC 3339) |
| `inactive_after` | timestamp (RFC 3339), optional |
| `replayed` | boolean |


## `subject.get`

POST `/v1/subjects/get` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |
| `external_reference` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `version` | string |
| `external_reference` | string |
| `label` | string |
| `updated_by` | string |
| `created_at` | timestamp (RFC 3339) |
| `updated_at` | timestamp (RFC 3339) |
| `inactive_after` | timestamp (RFC 3339), optional |


## `subject.list`

POST `/v1/subjects/list` → 200

### Request

| Field | Type |
|---|---|
| `after` | string |
| `limit` | integer |

### Response

| Field | Type |
|---|---|
| `subjects` | array of {id: string, version: string, external_reference: string, label: string, updated_by: string, created_at: timestamp (RFC 3339), updated_at: timestamp (RFC 3339), inactive_after: timestamp (RFC 3339), optional} |
| `next` | {id: string}, optional |


## `subject.update`

POST `/v1/subjects/update` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |
| `expected_version` | string |
| `external_reference` | string |
| `label` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `version` | string |
| `external_reference` | string |
| `label` | string |
| `updated_by` | string |
| `created_at` | timestamp (RFC 3339) |
| `updated_at` | timestamp (RFC 3339) |
| `inactive_after` | timestamp (RFC 3339), optional |
| `replayed` | boolean |


## `artifact.put`

POST `/v1/artifacts/put` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |
| `expected_version` | string |
| `data_subject_id` | string |
| `kind` | string |
| `name` | string |
| `content` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `version` | string |
| `data_subject_id` | string |
| `kind` | string |
| `name` | string |
| `source_observation_id` | string |
| `bytes` | integer |
| `authored_by` | string |
| `created_at` | timestamp (RFC 3339) |
| `updated_at` | timestamp (RFC 3339) |
| `expires_at` | timestamp (RFC 3339) |
| `replayed` | boolean |


## `artifact.get`

POST `/v1/artifacts/get` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |
| `data_subject_id` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `version` | string |
| `data_subject_id` | string |
| `kind` | string |
| `name` | string |
| `source_observation_id` | string |
| `bytes` | integer |
| `authored_by` | string |
| `created_at` | timestamp (RFC 3339) |
| `updated_at` | timestamp (RFC 3339) |
| `expires_at` | timestamp (RFC 3339) |
| `content` | string |


## `artifact.list`

POST `/v1/artifacts/list` → 200

### Request

| Field | Type |
|---|---|
| `kind` | string |
| `name_prefix` | string |
| `data_subject_id` | string |
| `after` | string |
| `limit` | integer |

### Response

| Field | Type |
|---|---|
| `artifacts` | array of {id: string, version: string, data_subject_id: string, kind: string, name: string, source_observation_id: string, bytes: integer, authored_by: string, created_at: timestamp (RFC 3339), updated_at: timestamp (RFC 3339), expires_at: timestamp (RFC 3339)} |
| `next` | {id: string}, optional |


## `artifact.delete`

POST `/v1/artifacts/delete` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |
| `expected_version` | string |
| `data_subject_id` | string |

### Response

| Field | Type |
|---|---|
| `deleted` | boolean |


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
| `rebuilding` | {reinterpreted_through: integer, reinterpreting_through: integer, sources_acknowledged: integer, sources_skipped: integer}, optional |


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
| `surfaces` | array of string |
| `themes` | boolean |

### Response

| Field | Type |
|---|---|
| `controls` | {max_characters: integer, max_rows: integer, source_roles: array of string, hops: integer, surfaces: array of string, themes: boolean} |
| `anchors` | array of {entity_id: string, name: string, type: string, matched: string} |
| `facts` | array of {fact_id: string, subject: string, predicate: string, object: string, statement: string, confidence: number, valid_from: timestamp (RFC 3339), valid_until: timestamp (RFC 3339), optional, anchored_on: string, source_role: string, hops: integer, path: array of string, via: array of string, co_derived_with: array of string, evidence: {observation_id: string, source_ordinal: integer, quote: string, byte_start: integer, byte_end: integer, context: string, context_start: integer, context_complete: boolean}} |
| `reports` | array of {community_id: string, report_id: string, title: string, summary: string, importance: number, similarity: number, level: integer, parent: string, sources: array of string} |
| `passages` | array of {chunk_id: string, source_id: string, ordinal: integer, role: string, quote: string, similarity: number, occurred_at: timestamp (RFC 3339), complete: boolean} |
| `truncated` | boolean |
| `characters` | integer |
| `degraded` | array of string |
| `reach` | {terms: integer, anchored: integer, named_nothing_known: boolean, facts_per_anchor: object of integer} |


## `context.assemble`

POST `/v1/contexts` → 200

### Request

| Field | Type |
|---|---|
| `data_subject_id` | string |
| `max_characters` | integer, optional |

### Response

| Field | Type |
|---|---|
| `segments` | array of {segment_id: string, level: integer, from_offset: integer, to_offset: integer, covered: integer, summary: string} |
| `turns` | array of {log_offset: integer, occurred_at: timestamp (RFC 3339), messages: array of {role: string, content: string}} |
| `watermark` | {scope: string, stored: integer, optional, formed: integer, optional, parked: integer, rebuilding: {reinterpreted_through: integer, reinterpreting_through: integer, sources_acknowledged: integer, sources_skipped: integer}, optional} |
| `characters` | integer |
| `truncated` | boolean |


## `erase`

POST `/v1/erasures` → 200

### Request

| Field | Type |
|---|---|
| `data_subject_id` | string |
| `source_observation_ids` | array of string |
| `reason` | string |

### Response

| Field | Type |
|---|---|
| `request_id` | string |
| `scope` | string |
| `data_subject_id` | string |
| `source_observation_ids` | array of string |
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
| `source_observation_ids` | array of string |

### Response

| Field | Type |
|---|---|
| `scope` | string |
| `data_subject_id` | string |
| `source_observation_ids` | array of string |
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
| `generation` | {generation_id: string, extractor_version: string, applied_at: timestamp (RFC 3339)}, optional |
| `reinterpreted_by` | {generation_id: string, extractor_version: string, applied_at: timestamp (RFC 3339)}, optional |
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


## `notification.register`

POST `/v1/notifications/endpoints/register` → 200

### Request

| Field | Type |
|---|---|
| `url` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `url` | string |
| `created_at` | timestamp (RFC 3339) |
| `disabled_at` | timestamp (RFC 3339), optional |
| `secret` | string |


## `notification.list`

POST `/v1/notifications/endpoints/list` → 200

### Request

| Field | Type |
|---|---|

### Response

| Field | Type |
|---|---|
| `endpoints` | array of {id: string, url: string, created_at: timestamp (RFC 3339), disabled_at: timestamp (RFC 3339), optional} |


## `notification.disable`

POST `/v1/notifications/endpoints/disable` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |

### Response

| Field | Type |
|---|---|
| `disabled` | boolean |


## `notification.deliveries`

POST `/v1/notifications/deliveries/list` → 200

### Request

| Field | Type |
|---|---|
| `limit` | integer |

### Response

| Field | Type |
|---|---|
| `deliveries` | array of {id: string, endpoint_id: string, url: string, formed_through: integer, stored_through: integer, attempts: integer, delivered_at: timestamp (RFC 3339), optional, parked_at: timestamp (RFC 3339), optional, last_status: integer, optional, last_error: string, optional, created_at: timestamp (RFC 3339)} |


## `entity.purge`

POST `/v1/entities/purge` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |
| `reason` | string |
| `confirm` | boolean |

### Response

| Field | Type |
|---|---|
| `entity_id` | string |
| `canonical_name` | string |
| `reason` | string |
| `previewed` | boolean |
| `removed` | object of integer |
| `residual` | object of integer |
| `contributing_subjects` | integer |
| `claims_from_unattributed_turns` | integer |
| `source_turns_kept` | integer |
| `completed_at` | timestamp (RFC 3339) |


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


## `feedback.record`

POST `/v1/feedback/record` → 200

### Request

| Field | Type |
|---|---|
| `record_id` | string |
| `note` | string |
| `proposed_object` | string, optional |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `record_id` | string |
| `note` | string |
| `proposed_object` | string, optional |
| `recorded_by` | string |
| `recorded_at` | timestamp (RFC 3339) |
| `promoted_by` | string, optional |
| `promoted_at` | timestamp (RFC 3339), optional |
| `replacement_record_id` | string, optional |


## `feedback.list`

POST `/v1/feedback/list` → 200

### Request

| Field | Type |
|---|---|
| `record_id` | string |
| `open_only` | boolean |
| `after` | {recorded_at: timestamp (RFC 3339), id: string}, optional |
| `limit` | integer, optional |

### Response

| Field | Type |
|---|---|
| `feedback` | array of {id: string, record_id: string, note: string, proposed_object: string, optional, recorded_by: string, recorded_at: timestamp (RFC 3339), promoted_by: string, optional, promoted_at: timestamp (RFC 3339), optional, replacement_record_id: string, optional} |
| `next` | {recorded_at: timestamp (RFC 3339), id: string}, optional |


## `feedback.promote`

POST `/v1/feedback/promote` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |
| `expected_version` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `record_id` | string |
| `operation` | string |
| `replacement_record_id` | string, optional |
| `version` | string |
| `promoted_at` | timestamp (RFC 3339) |


## Management operations

The management surface is served by the `manage` role on its own listener and reached with an operator credential, which opens no memory route; a project credential is refused at this door with the same answer a stranger gets. A request names the project it acts on. Every operation is on the ledger with the operator credential as its principal. Refusal counts carry no words; the words are read under a project credential through the memory routes.

| Operation | Method | Path | On success |
|---|---|---|---|
| `project.create` | POST | `/manage/v1/projects/create` | 201 |
| `project.list` | POST | `/manage/v1/projects/list` | 200 |
| `project.suspend` | POST | `/manage/v1/projects/suspend` | 200 |
| `project.resume` | POST | `/manage/v1/projects/resume` | 200 |
| `project.retention` | POST | `/manage/v1/projects/retention` | 200 |
| `credential.issue` | POST | `/manage/v1/credentials/issue` | 201 |
| `credential.list` | POST | `/manage/v1/credentials/list` | 200 |
| `credential.revoke` | POST | `/manage/v1/credentials/revoke` | 200 |
| `refusal.summary` | POST | `/manage/v1/refusals/summary` | 200 |
| `erasure.list` | POST | `/manage/v1/erasures/list` | 200 |
| `formation.status` | POST | `/manage/v1/formation/status` | 200 |
| `formation.parked` | POST | `/manage/v1/formation/parked` | 200 |
| `formation.unpark` | POST | `/manage/v1/formation/unpark` | 200 |
| `audit.seal` | POST | `/manage/v1/audit/seal` | 200 |
| `audit.verify` | POST | `/manage/v1/audit/verify` | 200 |

## `project.create`

POST `/manage/v1/projects/create` → 201

### Request

| Field | Type |
|---|---|
| `name` | string |

### Response

| Field | Type |
|---|---|
| `project` | string |
| `suspended` | boolean |


## `project.list`

POST `/manage/v1/projects/list` → 200

Takes no body.

### Response

| Field | Type |
|---|---|
| `projects` | array of {project: string, label: string, surfaces: array of string, retention: string, suspended: boolean} |


## `project.suspend`

POST `/manage/v1/projects/suspend` → 200

### Request

| Field | Type |
|---|---|
| `name` | string |

### Response

| Field | Type |
|---|---|
| `project` | string |
| `suspended` | boolean |


## `project.resume`

POST `/manage/v1/projects/resume` → 200

### Request

| Field | Type |
|---|---|
| `name` | string |

### Response

| Field | Type |
|---|---|
| `project` | string |
| `suspended` | boolean |


## `project.retention`

POST `/manage/v1/projects/retention` → 200

### Request

| Field | Type |
|---|---|
| `name` | string |
| `retention_days` | integer, optional |

### Response

| Field | Type |
|---|---|
| `project` | string |
| `retention` | string |


## `credential.issue`

POST `/manage/v1/credentials/issue` → 201

### Request

| Field | Type |
|---|---|
| `name` | string |
| `project` | string |
| `access` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `name` | string |
| `project` | string |
| `access` | string |
| `token` | string |


## `credential.list`

POST `/manage/v1/credentials/list` → 200

### Request

| Field | Type |
|---|---|
| `project` | string |
| `limit` | integer |

### Response

| Field | Type |
|---|---|
| `credentials` | array of {id: string, name: string, kind: string, project: string, access: string, token_prefix: string, created_at: timestamp (RFC 3339), revoked_at: timestamp (RFC 3339), optional} |


## `credential.revoke`

POST `/manage/v1/credentials/revoke` → 200

### Request

| Field | Type |
|---|---|
| `id` | string |

### Response

| Field | Type |
|---|---|
| `id` | string |
| `revoked` | boolean |


## `refusal.summary`

POST `/manage/v1/refusals/summary` → 200

### Request

| Field | Type |
|---|---|
| `project` | string |
| `limit` | integer |

### Response

| Field | Type |
|---|---|
| `project` | string |
| `total` | integer |
| `counts` | array of {reason: string, predicate: string, count: integer} |


## `erasure.list`

POST `/manage/v1/erasures/list` → 200

### Request

| Field | Type |
|---|---|
| `project` | string |
| `limit` | integer |

### Response

| Field | Type |
|---|---|
| `erasures` | array of {id: string, project: string, selector: opaque JSON, reason: string, requested_at: timestamp (RFC 3339), completed_at: timestamp (RFC 3339), optional, residual: opaque JSON} |


## `formation.status`

POST `/manage/v1/formation/status` → 200

Takes no body.

### Response

| Field | Type |
|---|---|
| `scopes` | array of {project: string, suspended: boolean, stored: integer, optional, formed: integer, optional, parked: integer} |


## `formation.parked`

POST `/manage/v1/formation/parked` → 200

### Request

| Field | Type |
|---|---|
| `project` | string |
| `after` | integer, optional |
| `limit` | integer |

### Response

| Field | Type |
|---|---|
| `items` | array of {observation_id: string, log_offset: integer, attempts: integer, parked_at: timestamp (RFC 3339)} |
| `next_after` | integer, optional |


## `formation.unpark`

POST `/manage/v1/formation/unpark` → 200

### Request

| Field | Type |
|---|---|
| `project` | string |
| `observation_id` | string |

### Response

| Field | Type |
|---|---|
| `observation_id` | string |
| `unparked` | boolean |


## `audit.seal`

POST `/manage/v1/audit/seal` → 200

Takes no body.

### Response

| Field | Type |
|---|---|
| `From` | integer |
| `To` | integer |
| `Entries` | integer |
| `Digest` | string |


## `audit.verify`

POST `/manage/v1/audit/verify` → 200

Takes no body.

### Response

| Field | Type |
|---|---|
| `Valid` | boolean |
| `Seals` | integer |
| `Entries` | integer |
| `Unsealed` | integer |
| `Head` | string |
| `Failure` | string |


## Refusals

A refusal is a JSON body of the shape `{"error": {"code": "…", "message": "…"}}`. Branch on the code; the message is for a person reading a log and may change.

Any operation may answer with any of these.

| Code |
|---|
| `invalid_message_window` |
| `source_changed` |
| `invalid_passage_query` |
| `passage_unavailable` |
| `invalid_entity_candidate_query` |
| `entity_candidates_unavailable` |
| `invalid_report_candidate_query` |
| `report_candidates_unavailable` |
| `invalid_entity_page` |
| `unauthenticated` |
| `invalid_body` |
| `invalid_turn` |
| `invalid_question` |
| `no_subject` |
| `invalid_erasure` |
| `internal` |
| `idempotency_conflict` |
| `rate_limited` |
| `no_database_capacity` |
| `invalid_citation` |
| `citation_limit` |
| `invalid_export` |
| `export_too_large` |
| `not_found` |
| `invalid_destination` |
| `notifications_unavailable` |
| `forbidden` |
| `invalid_record_page` |
| `invalid_recall_controls` |
| `invalid_context` |
| `invalid_record_mutation` |
| `record_conflict` |
| `record_mutation_limit` |
| `invalid_feedback` |
| `feedback_already_promoted` |
| `invalid_artifact` |
| `artifact_conflict` |
| `storage_capacity` |
| `invalid_subject` |
| `subject_conflict` |
| `invalid_project` |
| `invalid_credential` |

## Liveness

`GET /health` answers `200` with `{"status": "ok"}`. It takes no credential and reports nothing about the instance.

`GET /ready` takes no credential and returns `200` with `{"status": "ready"}` or `503` with `{"status": "unavailable"}`. Dependency checks have a one-second total budget; only one runs at a time and results are cached for one second. No error, count or configuration is returned. Serving readiness checks memory and registry access. `TAISCE_REQUIRE_FORMATION=true` additionally requires a worker heartbeat within 330 seconds; the default permits intentionally API-only operation. This proves responsiveness, not inference quality or completion of every project. Worker loopback probes check their own processing progress and memory database access.
