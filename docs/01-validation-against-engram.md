# Validated against Engram

**2026-09-04.** [`00-goals.md`](00-goals.md) was written before any code. Before building on it, the
schema was checked against `zyndax-platform/kernel/src/platform/engram` — a working .NET memory core
on PostgreSQL, by the same author, solving the same problem.

This records what that comparison found. It is a check against a running system, not a source of
decisions: where Taisce differs, the reason is in the goals rather than here.

---

## 1. What it confirmed about the shape

Engram stores bi-temporal facts as a real table with real indexes — `scope`, `subject`, `predicate`,
`object`, the temporal columns, and four indexes including a partial one over the live rows.

That is `memory.fact` here, and the agreement is worth recording precisely because the two arrived
independently: G3 says one typed edge list and G4 says the temporal columns are the hard part, and a
system that had already run in production had put both in the same place.

## 2. Three gaps it found in migration 0001, all fixed

**The supersession lookup had no index.** Engram has `fact_current_reconcile_idx` on
`(scope, subject, predicate)` restricted to live rows, serving the question every append asks first:
*is there a current fact this one contradicts?* Taisce's traversal indexes lead with the entity and
end with the validity range, so finding one predicate among a subject's facts meant reading all of
them — on the hottest write path, once per extracted triple.

Added as `fact_reconcile_idx`, partial on `upper_inf(valid)`. It is also what G4's exclusion
constraint will rest on.

**Freshness and erasure had no tables.** Engram has a per-scope `watermark` and a `tombstone`
registry with `requested_at` / `completed_at`. Added as `memory.watermark` and
`memory.erasure_request` — the latter carrying `residual` as a jsonb column, because the residual
count per projection kind is the artefact G6 is built around and it belongs in a column rather than a
log line.

**Confidence and salience were collapsed.** Engram separates them and it is right to: salience is
*computed* — recency, frequency, centrality — while confidence is *reported* by whatever produced the
claim. Collapsed together, a weakly-extracted fact that is mentioned often becomes indistinguishable
from a certain one, and there is then no way to review extraction quality afterwards.

## 3. One operational note worth keeping

Engram's migrations carry a hard-won detail about extension-created schemas: an extension that
creates a schema named after something the migrating role is also named after will capture every
unqualified `CREATE TABLE` that follows, through `"$user"` on the `search_path`. A table went into
the wrong schema and failed at runtime with `42P01`, and only on a from-empty run — which is exactly
what a fresh environment does.

Taisce's migrations are schema-templated and never rely on `search_path`, so the class of failure
does not arise. Recorded because it is not obvious and the failure is silent until it is not.
