<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Rebuilding facts

When you change the model, prompt or vocabulary, facts already formed keep the old interpretation. A
rebuild re-reads stored conversations under the new setup and publishes new facts. The old facts stay
in history.

All rebuild commands need the operator database connection (`TAISCE_ADMIN_DSN`). The ones that call
a model also need the model configuration. In a terminal, `rebuild project`, `status`, `cancel` and
`rebuild reports` print a formatted summary. Pipe them, or set `TAISCE_OUTPUT=json`, to get the JSON
shown below.

## Rebuild one source

```sh
taisce rebuild facts --project <project> --source <observation-uuid> --key <operation-uuid>
```

- `--source` is one stored conversation turn. Authored and corrected input can't be rebuilt; it
  never came from a model.
- `--key` is a UUID you choose. Keep it. Running the command again with the same key and setup
  returns the committed result without another model call.

Taisce runs the model first, then checks nothing changed, then swaps in the new facts in one
transaction. A source is limited to 256 messages and 1 MiB.

## Rebuild a whole project

```sh
taisce rebuild project --project <project> --key <operation-uuid> --limit 10
taisce rebuild status  --project <project> --key <operation-uuid>
taisce rebuild cancel  --project <project> --key <operation-uuid>
```

Run `rebuild project` with the same key until `status` is `completed`. Each run handles up to
`--limit` sources (default 10, maximum 100), with five minutes per source.

```json
{"job_id":"…","extractor_version":"extract/v2:…","through_offset":412,"after_offset":118,
 "sources_rebuilt":37,"sources_skipped":2,"status":"active","cancel_requested":false,
 "created_at":"…","updated_at":"…"}
```

The first run records where the log ends and which extraction setup to use. Turns added later are
left out, and a run under a different setup is refused. `status` and `cancel` don't need a model.

### Clients can see a rebuild in progress

A project rebuild publishes one source at a time, so a project can briefly hold both old and new
interpretations. While a job is active, `GET /v1/freshness` includes a `rebuilding` object:

```json
{"scope":"acme","stored":412,"formed":412,"parked":0,
 "rebuilding":{"reinterpreted_through":118,"reinterpreting_through":412,
               "sources_acknowledged":37,"sources_skipped":2}}
```

- `reinterpreted_through`: every source below this log offset has the new interpretation.
- `reinterpreting_through`: where the job will stop.
- The two counts are sources the job has finished with. They are not a total, so you can't work out
  a remainder from them.

The field is missing when no job is active. A client that needs a consistent answer can poll
freshness and wait; one that doesn't can carry on. Only offsets and counts are shown, never source
IDs, subjects or message text.

## Write missing reports

```sh
taisce rebuild reports --project <project> --limit 4
```

```json
{"project":"acme","entities":240,"communities":31,"reports_written":4,"reports_failed":0}
```

Reports are short summaries of groups of related facts. Changing facts removes the affected reports,
and the worker writes them back a few at a time. This command writes the missing ones now, up to
`--limit` (default 4, maximum 100; each one is a model call). Repeat until `reports_written` is 0.

It's the worker's own report pass, so it's safe to run while the worker is running. A group the model
refuses counts in `reports_failed` and is retried on the next run. If you changed the report prompt,
this also rewrites reports under the new prompt.

## What a rebuild leaves behind

A fact rebuild only replaces facts. When other things fall out of date, the command's output adds a
`stale` section and a `what_to_do` list:

```json
{
  "stale": {
    "unreferenced_entities": 12,
    "unreferenced_name_receipts": 19,
    "communities_without_a_report": 5,
    "message_embeddings": {"active": true, "covers_through_offset": 300, "current_offset": 412, "behind": 112},
    "entity_embeddings": {"active": true, "stale": true},
    "report_embeddings": {"active": false}
  },
  "what_to_do": ["…"]
}
```

- **Unreferenced entities** are names no current fact uses. Recall can still match them.
- **Missing reports** come back on their own, or right away with `taisce rebuild reports`.
- **Embeddings** built before the rebuild no longer match. Build and activate a new generation with
  `taisce embeddings`, `taisce entity-embeddings` or `taisce report-embeddings`.

Taisce doesn't fix these for you inside a rebuild, because each needs a different step. The numbers
are read when you run the command, so they're current. A project that was never rebuilt, or a surface
with no embeddings, shows nothing.

## Watch out for

- **Human edits win.** A correction blocks generated claims for the same subject and relation from
  that message. A withdrawal still blocks its claim.
- **There's no project-wide atomic switch.** Sources change one at a time; watch `rebuilding` on
  freshness.
- **Cancel may take a moment.** A source already started can finish, so a job can show
  `cancel_requested: true` with `status: active`. If the runner has gone, run `cancel` again; it
  finishes the bookkeeping without a model.
- **One job per project.** A second runner takes over, and the old one can no longer publish.
- **The operator pool needs at least two connections.** One holds the job lock while the other does
  the work. A smaller pool is refused before anything starts.
- **Failures leave nothing half-done.** A changed source, missing source, damaged receipt or failed
  model call is refused without partial publication. A failure before publication may need another
  model call. Model output is not cached.
- **Erased sources are skipped.**

## How it works

Each published source records a new generation, with a disposition for every fact it touched (at most
100). Citations show the generation that admitted a fact (`generation`) and the one that retired it
(`reinterpreted_by`). Exports and erasure include the same lineage. The previous interpretation stays
queryable through [historical recall](09-temporal-history.md).

If a current fact is missing when a source is rebuilt, it's restored from its receipt first and then
retired with the rest. A damaged receipt refuses publication.

The job's cursor and counters live in PostgreSQL. If publishing succeeds but recording progress
fails, the next run replays the source's stable key without another model call. Job records hold log
positions and counts only, not source IDs, subjects or text. They stay after subject erasure, like
the log watermark. Starting and cancelling are audited. The recorded principal is a random ID per
run, not a person.

To restore lost facts exactly as they were, without reinterpreting them, use
[fact recovery](15-fact-recovery.md) instead.
