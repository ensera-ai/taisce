# Decision register

Every decision, closed. **Nothing in this document is an open question.** If another document appears
to leave a choice, that is a defect in that document — this register is the authority.

Each entry says what was decided, why, and **what was rejected**. The rejections are the point: a
decision recorded without its alternatives gets reopened by whoever has only heard the winning
argument.

Entries are added as decisions are made. An entry is written when a `decision`-labelled issue closes,
or when building something forces a choice that a later reader would otherwise have to re-derive.

---

## Substrate

### D1 — PostgreSQL is the only required dependency

Not a vector database, graph engine, message broker, cache or object store.

Each addition is a second place data lives, a second thing to operate, and — decisively — **a second
sweep an erasure has to prove it covered**. G6 rests on deleting through a registered dependency and
counting against the same predicate in the same transaction. That count means "nothing survives"
only while there is one place for something to survive in.

**Rejected:** a dedicated vector store (the vector is a column on the row it describes, and
separating them means two erasure paths and a consistency problem between them); a graph engine (the
fact table with resolved entity ends *is* the edge list, and a graph store is weakest at exactly the
temporal work D3 makes central); a broker (the log is already an ordered, durable, replayable
sequence).

### D2 — An extension ships only with a reader in this codebase

An extension runs inside the backend process, so a fault in one takes the cluster, and the cluster is
the product. The test is not *"might we want this"* but *"what reads it today"*.

Currently: `vector` (`chunk.embedding` and the per-partition HNSW), `btree_gist` (the traversal
indexes and D4's constraint), `pg_stat_statements` (the only way to answer "which query got slower"
after a deploy without adding an instrumentation path to the server).

**Rejected:** a lexical/BM25 extension — no lexical index exists in this schema and none is designed,
since retrieval anchors on entities and reads passages as evidence (G1), and all three embedded
surfaces (G5b) are vector. It is also the most expensive thing the image could carry: a from-source
compile plus a `shared_preload_libraries` entry. If hybrid retrieval is wanted it returns **with the
query that reads it**. Also rejected: `pgcrypto`, because `gen_random_uuid()` has been core since
PostgreSQL 13 and that was its only use here.

### D3 — Time is a range, not a pair of columns with a sentinel

`valid` and `known` as `tstzrange`, GiST-indexed.

The alternative is scalar start/end columns where a sentinel means "open", which puts
`(end = 0 OR end > $1)` in every query and makes the end column a poor leading key in every index —
a representation problem surfacing as an indexing problem. A range is one column, one containment
operator, one index.

**Rejected:** sentinel-encoded bigints (above); `NULL` as open (correct, but a three-valued
comparison in every predicate is the same defect wearing a nicer hat).

### D4 — Supersession is a database constraint, not an application lock

A single-cardinality predicate cannot hold two overlapping valid intervals for one subject, enforced
by `EXCLUDE USING gist`.

A lock is correct only while every write path remembers to take it — including one written next year
by someone who has not read this. A constraint is correct because the database will not store the
violation.

**Rejected:** a fact-slot advisory lock (correct today, and one new write path from being wrong;
also the hottest contention point on the write path); optimistic retry on a unique index (a unique
index cannot express interval overlap, which is the actual invariant).

### D5 — Entity identity is `(scope, normalized_name)`. Type is an attribute

Putting the type in the identity key makes one thing extracted once as `place` and once as `thing`
into **two nodes holding two halves of its facts** — a silent under-merge on every hop, invisible
because both rows look correct on their own.

**Rejected:** `(scope, entity_type, normalized_name)`. A disagreement about type should be a
disagreement about one node, not the creation of a second.

### D6 — Resolution merges by normalised name only; meaning is a separate surface

Lowercase, trim, collapse whitespace. Nothing cleverer at this layer.

Merging "the Dublin office" with "our Dublin site" needs either an embedding comparison with a
threshold or a model call per entity at write time. **Over-merging puts two people's facts on one
node, which is a governance failure; under-merging costs recall on a hop.** That asymmetry decides
it: the conservative rule is the default, and meaning is added as the entity's embedded surface (G2)
where it can be measured and tuned against a held-out set rather than guessed.

**Rejected:** similarity-threshold merging at write time (a threshold nobody can justify, applied
irreversibly); a model call per entity (a write-time artefact with a confidence score attached).

### D7 — The vector index is per project partition, never one index over all of them

`chunk` is LIST-partitioned on scope, with an HNSW built inside each partition.

A single graph behind a scope predicate is unreachable in both directions. The planner prefers an
exact sort and never enters it; and post-filtering an approximate result loses recall in proportion
to how little of the corpus belongs to the caller. Pruning first and scanning inside one project's
graph gives an exact-correct result with no `ef_search` to tune.

A partition is DDL per project at a controlled moment — project creation — not something issued from
a request or a worker. Because a tenant is a schema (D8), the partition count per table is bounded by
projects-per-tenant.

**Rejected:** one HNSW on the partitioned parent (unreachable, and maintained on every insert for
nothing); no ANN index at all with an exact scan per scope (correct, and linear in project size,
which is the thing G1 exists to remove); iterative/relaxed approximate scanning (tuning a recall
knob to compensate for an index built over the wrong set).

### D8 — A tenant is a schema; a project is a column

**Hard and soft, and they are not the same kind of boundary.** A cross-tenant read is impossible
because the other tenant's name is not in the statement — not because a predicate excluded it. There
is no tenant column to forget. Cross-project reads are *intended*, since a principal granted several
projects gets one bundle across them, so scope is a column and the boundary is a permission checked
on every read.

A schema name cannot be a bind parameter, so it is interpolated — which is why it comes from a
validated type that exists only after construction. Forgetting to validate is a compile error rather
than an injection.

**Rejected:** a `tenant_id` column with row-level security as the primary control (one missing
predicate from a cross-customer leak, and the guarantee lives in policy rather than in the absence of
a name); a schema per project (a multi-project recall becomes a cross-schema union that cannot be
planned as one query); a database per tenant (connection pooling and migration both scale badly).

## Erasure

### D9 — Erasure walks a registered dependency, not a list of tables

Every projection registers what it derived and from which observation. Erasure walks that, so
`COUNT(*)` afterwards means *"nothing derived from that observation survives"* rather than *"nothing
survives in the tables somebody remembered to check"*.

A projection kind that does not register is invisible to erasure, so the registration is asserted by
a test that fails when a kind is written without one.

**Rejected:** a hand-maintained delete list (correct until the next projection is added); a
containment index over source ids on each projection (works, and puts the burden on every future
table to remember the same pattern).

### D10 — The residual count is a column, not a log line

`erasure_request.residual` holds the per-projection-kind count after the delete, in the same
transaction, with a constraint that it is present exactly when the request is complete.

This is the artefact the product is sold on. Something a customer may be asked to rely on should be
queryable and constrained, not recoverable by grepping.

**Rejected:** emitting it to the audit log only (unqueryable, and a log is not covered by a
constraint that says an incomplete erasure cannot claim a residual).

### D11 — A confidence is reported; a salience is computed. They are separate columns

Salience is derived — recency, frequency, centrality. Confidence is what the producer of a claim
asserted about it. Collapsed into one number, a weakly-extracted fact that is mentioned often becomes
indistinguishable from a certain one, and extraction quality becomes unreviewable after the fact.

**Rejected:** one score (above); confidence as metadata on the evidence row rather than the fact
(the fact is what gets ranked, so the fact is what needs the number).

## Write path

### D12 — Every observation carries its speaker, from the first migration

`source_role` is `NOT NULL`.

The role policy — an assistant's claim never becomes a user's fact — can only be enforced if every
write surface carries the speaker. A role that is optional at the bottom becomes optional everywhere
above it, and the surface that skips it is usually the one written last and used most.

**Rejected:** a nullable role with a default (the default is a lie about who spoke); role inferred
from the payload shape (guessing at the one thing the policy depends on).

### D13 — The log offset is assigned per scope, not by a global sequence

The freshness watermark is the highest offset with **no gap below it**. A globally-issued sequence
has holes in every scope by construction, so "contiguous" would be unanswerable and the watermark
would have to mean something weaker than it says.

**Rejected:** a global identity column (above); a timestamp as the watermark (clock skew and equal
timestamps make "everything up to here" unprovable).

## The graph

### D15 — The predicate vocabulary is closed, and a foreign key is what closes it

Thirty-nine relation types, seeded per tenant into `{schema}.predicate`, with `fact.predicate`
referencing it. Each carries a **cardinality** (`one` — at most one currently-valid fact per subject
and predicate; `many` — several coexist), an **object kind**, and a **semantic type**.

An extractor asked for a relation invents one per sentence: `works_at`, `employed_by`,
`is_employed_at`, `has_employer` — four edge types describing one relation. A traversal asking where
somebody works matches one and silently misses three quarters of what was extracted, and every row
involved looks correct. It also makes the stages after it unbuildable: supersession asks "is there a
current fact with this subject and this predicate", which only reconciles when two assertions chose
the same word, and a cardinality rule cannot be written for a vocabulary nobody can enumerate.

Cardinality is declared **when the predicate is defined**, because at supersession time all that is
visible is two rows and the question is no longer answerable. Its enforcer is the exclusion
constraint in #9; declaring it now is what lets that arrive as a constraint rather than as a redesign.

The size is bounded at 30–50 and asserted by a test. Too small and real relations have nowhere to go.
Too large and the near-duplicates are back, spread over a set nobody can hold in their head — which
is harder to see than an open vocabulary, not easier.

**Rejected:** an open vocabulary (above). A list in application code rather than a foreign key — that
is a convention every write path has to remember, and the path that forgets is the one written last;
as a constraint an unknown relation cannot be stored by any path, including a hand-written `INSERT`
during an incident. A generic `has_value` or `has_attribute` — a wildcard makes the closed set
vacuous, since anything can be phrased as a value of a compound object, and every claim the ontology
was meant to refuse arrives through it wearing an accepted name. Inverse predicates such as
`reports_to` alongside `manages` — edges are directed and traversal follows both ends, so an inverse
is a second spelling of one relation and reintroduces exactly the split this decision prevents. A
`mentioned` or `co_occurs_with` relation — an edge between two things that appeared near each other
has no direction and no meaning, so a multi-hop question gets no hops out of it. `ON DELETE CASCADE`
on the reference — retiring a predicate would delete the memory in order to permit a schema edit, so
it is `RESTRICT` and the change fails loudly.

### D16 — A relation outside the vocabulary is recorded as unmapped, never admitted as an edge

Not dropped, and not admitted.

Nothing is lost by refusing it, because the refusal is narrower than it looks: the message is already
stored as a row and retrievable as a passage, so what is refused is **the typed edge**, not the
memory. This is the same shape as the role policy — an assistant's sentence is still stored, it
simply does not become something the person is recorded as having said.

Admitting it is what cannot be allowed. One relation admitted outside the set means the set is
advisory, and an advisory vocabulary is an open one with extra steps.

The unmapped claim is kept with its evidence so the rate is a query rather than an estimate: a high
unmapped rate is evidence the vocabulary is wrong, and that argument needs a number. D18 is where
that record lives, and it holds the other silent refusal alongside this one.

**Rejected:** dropping unmapped claims silently (loses the evidence that the vocabulary is wrong,
and the failure is invisible precisely when it is worst); admitting them as edges with a flag (a flag
is a filter every future query has to remember, so the vocabulary is closed only in the queries
somebody remembered); mapping them onto the nearest admitted relation by similarity (records a
relation nobody asserted, and the wrongness is unrecoverable because the original wording is gone).

## Extraction

### D17 — A quote is a search term, never evidence. Whitespace is tolerated and nothing else is

The extractor takes the model's quote, locates it in the message, and stores **the text it found
there**. Every claim satisfies `content[ByteStart:ByteEnd] == Quote` by construction rather than by
the model's cooperation, and a claim whose quote cannot be located is discarded — not a weaker claim,
an uncitable one, in a product whose argument is citation.

Matching is exact first, then whitespace-tolerant, with the span mapped back to the original bytes. A
model copying text routinely turns a line break inside a sentence into a space; the words are the
same words, so refusing that discards a correct citation over a formatting artefact. A span into the
normalised copy would be the same failure from the other side, so it is never returned.

Case is not tolerated. A model that changed the case retyped rather than copied, and a tolerance wide
enough to match `moved to dublin` against `moved to Dublin` is wide enough to match a sentence the
model composed.

A quote appearing twice takes the first occurrence: the same words in both places resolve to the same
text, only the instance differs, and nothing reads the instance.

**Rejected:** trusting the span the model returns (a span that is off by one character resolves to
the wrong words while looking correct, and there is no way to notice); exact matching only (discards
correct citations over a newline); case-insensitive or fuzzy matching (no line left to draw once
retyping is admitted); embedding similarity between quote and message (a threshold nobody can
justify, applied to the one thing that must be verbatim); storing the model's wording alongside a
located span (a quote its own span does not reproduce — self-inconsistent, and it looks fine).

### D18 — A claim outside the vocabulary or without a locatable quote is written down, not counted

`{schema}.rejected_claim` keeps both refusals with the model's own wording, and its `predicate`
column deliberately has no foreign key.

Both refusals are silent and both are claims about quality nobody can check without the text. A rate
of eleven percent cannot distinguish one missing relation asserted five hundred times — the
vocabulary is wrong and the fix is one row — from five hundred asserted once, which is the tail and
is noise. Those need opposite responses.

It registers for erasure like every other projection: a refused claim still carries somebody's words,
and a table of "things we did not store" that turns out to store them is the worst version of this
feature.

**Rejected:** a counter per reason (cannot distinguish the two cases above, which is the only reason
to measure); an error log (both refusals are ordinary, so they would be noise inside the record of
things that are not); normalising the recorded predicate (the near-duplicates that justify adding a
relation are visible only in the exact words proposed).

## Formation

### D19 — Forming happens behind the append, and freshness is two numbers

A turn is stored immediately and formed afterwards. Forming means a model call per message; held
inside the append, an agent's turn would block on its own memory write and the product would be
slower than having no memory at all.

What that costs is a window where a turn is stored and not yet recallable, so the window has to be
readable. `watermark.log_offset` advances on append and says the turn was stored — which the caller
already knows, because the append returned. `watermark.formed_offset` is the highest offset whose
turn has been through extraction **with nothing unformed below it**, and it is the number a recall
depends on.

It is derived from the backlog, not incremented per completion. An increment is correct only if
completions happen in order, and two turns forming concurrently finish in whichever order their model
calls return — so the number would run past a gap and a caller told "current to 40" would be missing
turn 12. That is the one failure a freshness number must not have, because it is acted on rather than
read.

`formed_offset` is NULL until the scope's first turn forms, not -1. Offsets start at zero, so a
sentinel is a value in the same domain as the data and every comparison carries a clause to exclude
it that one query eventually forgets.

One worker per scope, held by an advisory lock across the whole drain. Two workers taking the same
turn would assert every fact twice, and the duplicates would be indistinguishable afterwards from a
person saying the same thing in two conversations — a lock inside the transaction that picks the turn
is released before the model call it was meant to protect.

**Rejected:** synchronous formation (correct recall immediately, at the price of an append that waits
on a model); redefining the single watermark to mean formed (loses the answer to "did you receive
it", which is what an append is asked to confirm); incrementing the formed offset per completion
(above); a tenant-wide formation lock (one busy project would starve every quiet one beside it).

### D20 — The role applied to a claim is the message's, never the turn's

Formation passes each message's own speaker to the fact table.

A turn opened by the user does not make an assistant message three positions later speak for the
principal — and turns opened by the user are most turns, so passing the turn's role would quietly
promote nearly every model sentence in the system.

Every message is extracted, including ones whose claims will then be refused. Skipping them would
save a model call and produce identical rows, but only because formation would be applying the role
policy itself, in a second place, with reasoning that has to stay in step with the first. The policy
lives on the one road to the fact table so that a path which forgets it does not exist.

Refusals by role are counted and **not** written to `rejected_claim`. Unlike the other two refusals
this is not a quality signal — it is the policy working as designed, and its rate is already knowable
from the roles of the messages in the turn.

**Rejected:** the turn's lead role (above); a role on the observation only, with no per-message column
(the policy would be unenforceable for every message after the first); skipping extraction for
messages that cannot speak for the principal (duplicates the policy to save a call).

### D22 — Message content leaves only to a host somebody wrote down

`TAISCE_INFERENCE_ALLOWLIST` names the permitted hosts, separately from the endpoint, and a
disagreement between them is fatal at startup.

Extraction sends a customer's message text to a model. That is an egress of exactly the data this
product exists to hold, and the destination is a string in an environment variable — one typo, one
copied deployment file, and the content goes somewhere nobody chose. Unlike most misconfigurations it
cannot be undone once noticed.

Matched on host **and port**, never by prefix: a prefix match accepts
`https://inference.example.attacker.test` for a list containing `inference.example`, which is the
failure an allowlist exists for. Plaintext is refused anywhere but the local machine — listing a host
says where content may go, not that it may go there in the clear.

An empty allowlist permits nothing. Listing a host is a deliberate act, and that is the whole
mechanism: it cannot be arrived at by default, only by somebody writing the host down.

**Rejected:** deriving the permitted host from the endpoint (there is then nothing to disagree with,
so a typo configures its own approval); a warning rather than a refusal (read after the egress it was
meant to prevent); open when unset (one forgotten variable away from not being a guard); a single
combined variable (the same string cannot both propose a destination and authorise it).

### D21 — Evidence names the message it indexes, not only the turn

`fact_evidence.source_ordinal`, in the primary key.

A span indexes one message and an observation is a whole turn. Without the ordinal, resolving a
citation means joining to the turn's messages with nothing to choose between them — and a byte offset
taken against the wrong message does not fail. It returns a plausible fragment of a different
sentence and attributes it to the fact as its evidence, and the two disagree only if somebody reads
them side by side.

In the key rather than beside it, because one fact can be evidenced from two messages and two spans
starting at the same offset in different messages are different evidence. Outside the key the second
silently replaces the first.

**Rejected:** the ordinal as a plain column (a duplicate key would drop a citation); one chunk per
turn so the span indexes the turn (a span into a concatenation that exists nowhere); resolving by
searching the turn for the quote (the search is what produced the span in the first place, and it
would find the wrong occurrence exactly when two messages restate each other).

## Method

### D14 — Nothing is brought in from another codebase unless it is asked for

Not code, not decisions, not measurements. Somewhere something was tried is not a source of answers
here: those choices were made under constraints that are not these constraints, and a decision
carried across without being re-derived is one nobody can defend when it is challenged.

Where something **is** asked for, what transfers is the **idea**: the implementation is rewritten,
reviewed and validated here. The check that catches a copy is D2's — every part must have a reader in
this codebase. Something present because it was in the original, rather than because something here
uses it, is a cost with no mechanism behind it.

**Rejected:** importing a decision register wholesale (imports the frame that produced a different
architecture); "carry the decisions that still hold" (presumes they hold, and the presumption is the
error).
