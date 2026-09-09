<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Feature coverage and execution plan

Reviewed 2026-09-08 at `225a37c`. Tracking: [#83](https://github.com/althunibat/taisce/issues/83).
GitHub issues remain the source of truth; this is a dated implementation assessment.
New development work is tracked in private `althunibat/taisce`. Fully qualified links to existing
organization issues preserve their original meaning; their numbers are not interchangeable with
the development tracker.

## Development assumption

Taisce is **greenfield and nowhere deployed**, as confirmed by the project owner. Optimize the
unreleased schema and contracts for correctness, performance, functional quality and security.
Do not schedule hypothetical client migrations, backward-compatibility shims or mixed-version
rollouts as prerequisites. Fresh provisioning and local tests remain required. D80 records this
policy; D78/D79 remove the unnecessary history cutoff and older-binary token format.

Recovery/rebuild remains product work for derived state and model changes. Before the first supported
release, review the schema baseline and define actual upgrade/support guarantees. Development stays
in private `althunibat/taisce`; public Apache-2.0 `x.y.0` snapshots remain a separate release action.

## Delivery progress — 2026-09-09

Private implementation branch: `codex/backlog-hardening`. The assessment and capability table below
record the original review; this section records subsequent delivery. A server-side implementation
does not complete an issue whose adapter, recovery or operational acceptance remains unproved.

| Work | Delivery and evidence | Remaining scope |
|---|---|---|
| Terminal presentation [#126](https://github.com/althunibat/taisce/issues/126) | D111 adds responsive branded help, aggregate health panels, measured rebuild summaries, sanitized interactive failures, and bounded stderr activity for long rebuild/recovery operations. JSON/exit contracts remain stable; color, ASCII and non-TTY controls are explicit. Full regression passes at 91.7% coverage and representative 48/110-column output was reviewed. | None in this presentation issue. |
| Entity embedding candidates [#139](https://github.com/althunibat/taisce/issues/139) | D110 adds source-owned, model-identified entity generations over bounded names and verbatim evidence, exact/optional ANN candidate retrieval, finite snapshot invalidation, repair and authenticated HTTP/CLI access. Full regression passes at 91.8% coverage, focused PostgreSQL/API/CLI race checks pass, and the configured local provider persists and retrieves its actual 2560-dimensional vectors. | Semantic quality and performance evidence remain separate. Automatic merge policy remains #10/#11; community-report embeddings remain #19. |
| Community-report embeddings [#19](https://github.com/althunibat/taisce/issues/19) | D112 adds a distinct source-owned report generation over bounded aggregate prose, exact/optional ANN thematic candidates, finite snapshot invalidation, repair and authenticated HTTP/CLI access. | Cross-surface semantic fixtures, quality measurement and controlled ANN/capacity evidence remain in #19/#101/#103. |
| Disposable GPU qualification [#138](https://github.com/althunibat/taisce/issues/138) | D109 deploys PostgreSQL, API/worker and pinned vLLM models through Compose on a fresh eight-A100 node. The deployed lifecycle and all seven gates pass; coverage is 92.0%. Logical wipe and provider termination were verified. | Workload-specific quality, capacity and failover acceptance remain separate; OpenRouter verification is still incomplete. |
| Saved message references [#136](https://github.com/althunibat/taisce/issues/136) | D108 adds digest-bound UTF-8 source windows, a unique authoritative UUID index, read-only access and content-free audit. HTTP/PostgreSQL tests cover generation-independent lookup, source changes, projection loss and deletion. | Full regression passes at 91.9% coverage, targeted race checks and configured-provider regression pass. Document segmentation and ingestion remain #91. |
| Incremental message embeddings [#134](https://github.com/althunibat/taisce/issues/134) | D107 adds bounded catch-up and an explicit runtime-role watch process, separates target from completed coverage, and fences publication against activation and source deletion. PostgreSQL/HTTP/CLI journey checks pass. | Full regression passes at 91.9% coverage, targeted race checks pass, and the local 2560-dimensional provider serves newly indexed evidence. Deployment orchestration and entity/report targets remain separate scope. |
| Authenticated passage search [#133](https://github.com/althunibat/taisce/issues/133) | D106 exposes bounded source evidence through project credentials, explicit model binding, read-only authorization and content-free audit. PostgreSQL/HTTP tests cover erasure and generation changes during provider calls. | Local provider exact/ANN HTTP compatibility passes at 2560 dimensions; the full suite passes at 91.9% coverage. Incremental indexing is tracked in #134; general source lookup and semantic/capacity qualification remain separate scope. |
| Entity semantic input bounds [#132](https://github.com/althunibat/taisce/issues/132) | D105 enforces the complete representation budget, rejects unsupported canonical identities and bounds collection work. The reproduced budget bypass is covered; the semantic package passes at 100% coverage under the race detector. | Configured-provider regression and the full 91.9% coverage gate pass; persistent entity embeddings and safe semantic resolution remain #10/#11/#19. |
| Message embedding generations [#131](https://github.com/althunibat/taisce/issues/131) | D104 adds model-identified persisted messages, bounded build/repair, explicit activation, source-owned erasure/export/expiry and exact/optional ANN retrieval. Local provider compatibility passes at 2560 dimensions. | Continuous embedding, entity/report surfaces and controlled-host semantic/capacity qualification remain #19/#101/#103. |
| Embedding boundary [#130](https://github.com/althunibat/taisce/issues/130) | D103 bounds requests/replies and refuses zero, oversized or non-finite vectors before ranking. Regressions reproduce the previous zero/NaN behavior; race checks cover valid boundaries and reply overflow. | Persistent model/dimension identity, three embedding surfaces and generation cutover remain #101/#19. |
| Entity inspection [#129](https://github.com/althunibat/taisce/issues/129) | D102 exposes bounded entity inventory and exact identity lookup under project read authorization, with source-owned aliases, speaker binding, audit and erasure behavior. PostgreSQL verifies direct project/UUID index access. | Entity-purge preview/execution and feedback remain #89; workload qualification remains #103. |
| Message chunk recovery [#128](https://github.com/althunibat/taisce/issues/128) | D101 retains source-owned chunk UUIDs and restores missing chunks/registrations through bounded, audited operator pages. Conflicts refuse atomically; embeddings survive and erased sources cannot revive. | Changed segmentation and embedding regeneration remain #58/#19; controlled workloads remain #103. |
| Source-owned name variants [#127](https://github.com/althunibat/taisce/issues/127) | D100 retains source-owned spellings, maintains bounded alias caches, reconstructs them during fact recovery, and removes expired/erased contributions while preserving shared support. HTTP bounds and database concurrency/rollback tests cover the lifecycle. | Canonical reassignment/semantic resolution generations remain #58; controlled-host alias lookup and support-scan measurements remain #102/#103. |
| Entity recovery identity [#125](https://github.com/althunibat/taisce/issues/125) | D99 validates retained endpoint identity under row locks before missing-fact recovery or support repair. Conflicting snapshots refuse the transaction; compatible shared entities keep their IDs and aliases. | D100/#127 delivers source-owned alias reconstruction; semantic identity generations remain #58. |
| Strict request parsing [#87](https://github.com/althunibat/taisce/issues/87) | Closed; `62c931c`. Complete-object decoding, duplicate/case-alias rejection, UTF-8/surrogate validation and nesting/body bounds. HTTP/PostgreSQL refusal tests prove no writes. | None in this issue. |
| Refused-auth audit [#88](https://github.com/althunibat/taisce/issues/88) | Closed; `16582d4`. No supplied credential text enters the ledger; bounded per-process sampling, suppression counts and race tests. | Fleet-wide admission remains [#93](https://github.com/althunibat/taisce/issues/93). |
| Empty-project freshness [#105](https://github.com/althunibat/taisce/issues/105) | Closed; `9438cba`. Absent stored watermark is null; the first accepted offset is zero. Full lifecycle tested. | None in this issue. |
| Observation retries [#85](https://github.com/althunibat/taisce/issues/85) | Server implemented; `2ad0a16`. Project-scoped UUID keys, stable receipt, conflict refusal, atomic persistence and erased-key tombstones. Twenty-four concurrent HTTP attempts produce one observation and one extraction call. | Stable keys in actual adapters and shared conformance. Issue remains open. |
| Atomic message groups [#86](https://github.com/althunibat/taisce/issues/86) | Server implemented; `33b1d44`. Explicit contiguous groups, safe ordinary-message defaults, tool-group requirement and export identity. | Compaction and adapter conformance. Issue remains open. |
| Public snapshot ancestry [#104](https://github.com/althunibat/taisce/issues/104) | Closed; `a18394a`. Separate preparation/publish phases, exact destinations, atomic compare-and-swap publication, independent private version markers. Disposable Git tests include a concurrent push race. | Signing, artifacts and release readiness remain [#37](https://github.com/althunibat/taisce/issues/37). No public release or visibility change was performed. |
| Speaker binding [#84](https://github.com/althunibat/taisce/issues/84) | Core implemented; `651cf61`. Stored subject/user-role binding, separate named/speaker identities, subject-filtered recursive recall, multilingual formation, corrections, export and erasure. | Derived-state generation/rebuild workflow and complete nonlocal profile verification. Issue remains open. |
| Report input identity [#108](https://github.com/althunibat/taisce/issues/108) | Closed; `651cf61`. Opaque entity references distinguish equal speaker labels in model context and child substitution. | Generated prose quality remains subject to inference corpus evaluation. |
| Quickstart contract [#107](https://github.com/althunibat/taisce/issues/107) | Closed; `651cf61`. Examples use credential-bound project and explicit subject attribution. | None in this issue. |
| Database integrity [#100](https://github.com/althunibat/taisce/issues/100) | Implemented; `d66537a` establishes deferred project-consistent references, runtime catalog restrictions, exact report/child provenance, shared retention and source-evidence removal. A gated real-PostgreSQL test now overlaps formation, project rebuild and erasure, checks surviving shared identities and another project, and refuses dangling graph results. | No remaining implementation scope in this issue. No RLS confidentiality claim; D68 records the assessment. Controlled workloads remain #103. |
| Single-tenant provisioning [#99](https://github.com/althunibat/taisce/issues/99) | Closed; `17ac077`. One default memory namespace, compatible instance-wide override, explicit provisioning names, fresh/restart/upgrade tests, reconciled service-role flags. | Effective privilege drift from existing grants/memberships/ownership is tracked in [#109](https://github.com/althunibat/taisce/issues/109). |
| Effective runtime privileges [#109](https://github.com/althunibat/taisce/issues/109) | Closed; `bab1c0b`. Bootstrap and each new pool connection check direct/inherited privileges, SET ROLE, ownership and protected catalogs. Registry runtime is read-only. | No remaining scope in this issue. D70 documents remediation and connection-admission limits. |
| Atomic project storage [#110](https://github.com/althunibat/taisce/issues/110) | Closed; `1309e72`. One name contract, transactional DDL/row creation, bounded collision-resistant physical names, catalog validation and legacy reuse. | None in this issue; vector strategy/scale measurements remain #101. |
| Exact anchor admission [#102](https://github.com/althunibat/taisce/issues/102) | Core implemented; `3b3a3bb`. Separate indexed name/alias arms, question/term/match limits, explicit refusal before traversal, real-database plan and attribution/erasure tests. | D100/#127 delivers source-owned alias ownership and concurrent registration. Normalized-relation comparison and controlled-host workload measurements remain. Issue remains open. |
| Shared request admission [#93](https://github.com/althunibat/taisce/issues/93) | Request admission `cd6c76c`; durable backlog reservations `1b3b1fe`. Size and concurrency bounds, protected accounting across processes, atomic release on formation/erasure/retention, and retryable HTTP refusal. | Controlled-host workloads, deployment-wide request admission policy and live operational metrics remain open. |
| Operator formation recovery [#113](https://github.com/althunibat/taisce/issues/113) | Closed; `fa844ba`. Bounded metadata pagination and project-scoped retry with atomic audit and watermark correction. | None in this issue. |
| Agent state/files [#59](https://github.com/althunibat/taisce/issues/59) | Project-owned opaque artifacts, versioned writes, literal metadata search, erasure-safe creation retries, bounded expiry and protected quota accounting (D92). Binary round trips, source exclusion, export/erasure, rollback, concurrency and runtime privilege checks cover the journey. | Session/history/checkpoint protocols remain #23; framework adapters remain #26. |
| Retention scheduling [#122](https://github.com/althunibat/taisce/issues/122) | Due stored deadlines schedule sweeps under an indefinite project policy, including artifact expiry and quota release (D93). Driver-level regression reproduced the missing deletion and now passes. | Controlled workload measurements remain #103. |
| Subject registry [#95](https://github.com/althunibat/taisce/issues/95) | Optional random subject IDs, exact external-reference lookup/rotation, bounded inventory, version checks and erasure-safe retries (D94). Registry export, counted erasure, source-aware expiry and quiet-project scheduling are implemented. HTTP speaker attribution survives rotation and missing-fact recovery. | Full projection generations remain #58; metadata/tombstone capacity measurements remain #93/#103. |
| Retained-record support recovery [#58](https://github.com/althunibat/taisce/issues/58) | Operator recovery repairs missing original evidence, temporal history and erasure registrations while preserving retained record state (D95). It reports repaired records separately and refuses conflicting support. | Full projection generations and changed-model reinterpretation remain open. |
| Extraction provenance [#58](https://github.com/althunibat/taisce/issues/58) | Configured model/prompt/rule/vocabulary fingerprints reach evidence and refusals; source pins refuse mixed retries, including failed/empty attempts (D96). Recovery retains original stamps. | Explicit changed-model cutover and other projection generations remain open. |
| Single-source generation cutover [#58](https://github.com/althunibat/taisce/issues/58) | `taisce rebuild facts` prepares one retained turn outside the database transaction, then atomically reinterprets it under the current extractor identity. Old derived facts remain historical, new facts receive generation-scoped IDs, human corrections and retractions are protected, and citations/export/erasure include lineage (D97). Real PostgreSQL tests cover rollback, changed-source refusal, concurrent cutover, no-model retry, temporal boundaries and role policy. Missing current facts are retired through their receipts (#123). | Project fact orchestration is D98/#124, source-owned name recovery is D100/#127 and message chunk repair is D101/#128. Semantic identity generations, embeddings, reports and model-response caching remain open. |
| Project fact rebuild [#124](https://github.com/althunibat/taisce/issues/124) | Bounded resumable pages over a captured log range, stable extraction identity/source keys, durable progress, fenced runners and audited cancellation (D98). PostgreSQL tests cover lost acknowledgement, erasure, cancellation with live/orphaned workers, changed configuration and concurrent admission; CLI status/cancel need no provider. | Other projection rebuilds remain #58; controlled workload/capacity measurements remain #103. |
| Curated record lifecycle [#89](https://github.com/althunibat/taisce/issues/89) | Explicit restricted issuance, shared permission gate, content-free denied-operation audit with registry-authoritative access using one opaque token format (D79). | Bounded inventory/history inspection is implemented under D81. D84 adds source-backed retractions, versions and atomic batches; D85 adds current-record corrections with independent authored sources and no-model replay. D91 adds independent authored assertions with atomic batches and erasure-safe retry keys. Feedback, entity purge, finer human authorization and complete rebuild remain open. |
| Temporal knowledge [#116](https://github.com/althunibat/taisce/issues/116) | Preserved validity versions, stable fact IDs, recorded supersession sources, causal erasure/retention and history from the first assertion (D78). | Mixed-workload measurement and generation/rebuild integration remain open. |
| Report freshness [#117](https://github.com/althunibat/taisce/issues/117) | Source fact revisions, atomic stale-write refusal and invalidation of direct/parent summaries (D83). Database concurrency, rollback and prompt-metadata regressions cover the boundary. | None in this issue; curated retractions/rebuild remain #89/#58. |
| Content access [#96](https://github.com/althunibat/taisce/issues/96) | D90 selects separate projects for different content access. Unused classification fields are removed; an HTTP/PostgreSQL lifecycle test proves the deployment pattern. | No in-project clearance model is claimed or required. |
| Faithful fact recovery [#121](https://github.com/althunibat/taisce/issues/121) | Source-owned admitted outcomes and temporal receipts, exact byte validation, bounded atomic operator pages and original provenance (D88). | Full model/rule reinterpretation, other projection generations and controlled-host workloads remain #58/#101/#103. |
| Rejected proposal replay [#120](https://github.com/althunibat/taisce/issues/120) | Stable exact-diagnostic identity, transactional registration and concurrent replay (D87). Recovery retains the identifier; distinct proposals and source ownership are preserved. | None in this issue; resumable source recovery remains #58. |
| Ordered knowledge transitions [#119](https://github.com/althunibat/taisce/issues/119) | Automatic supersession selects a strictly later logical boundary after wall-clock rollback (D86). A deterministic PostgreSQL fixture checks retained history and aligned archive/prior/successor intervals. | None in this issue; full generation integration remains #58/#116. |
| Source-claim replay [#118](https://github.com/althunibat/taisce/issues/118) | Deterministic source/structural-claim identity and per-source serialization (D84). Concurrent retries keep one fact/evidence row, version and temporal ranges; derived-row recovery retains the ID. | None in this issue; explicit reinterpretation and complete rebuild remain #58. |
| Recall controls [#92](https://github.com/althunibat/taisce/issues/92) | Complete; per-request character ceilings, explicit labeled source roles, effective hop/row limits and hard Unicode content accounting (D82). HTTP conformance and concurrent-client checks cover filters and truncation. | None in this issue. SDK runners reuse the fixture under adapter conformance #26; document ingestion remains #91. |
| Saved citations [#90](https://github.com/althunibat/taisce/issues/90) | Independent project-scoped resolution, exact source-byte verification and bounded evidence pagination (D77). Temporal history and successor/retraction relationships are retained (D78/D84); fact generations preserve old citation identities, record lineage and restore their receipts (D97/D98). | None in this issue. Other projection rebuilds remain #58. |
| Operational health [#94](https://github.com/althunibat/taisce/issues/94) | Bounded dependency readiness, actual worker progress, content-free operator aggregates and Compose probes. D76 records scope and limits. | Helm integration depends on #76; issue remains open. |

Older issue reconciliation at `ff0a851` separates completed work from superseded proposals:

| Issue | Verified resolution | Work still tracked separately |
|---|---|---|
| #6 | Bounded recursive traversal returns ordered chains; both depth and second-hop authorization tests pass. D56. | Controlled graph workloads: #7/#16/#103. |
| #8 | Valid/known ranges, original knowledge history and current/past reads pass the PostgreSQL temporal suite. D3/D54/D78. | Complete generations and workload measurements: #58/#116/#103. |
| #13 | Structured report validation, pure context construction and child substitution pass. | Broader live-model quality and complete regeneration: #15/#58. |
| #14 | D57 decides stored, source-registered reports, deleted and counted when any contributor is erased. | Operational generation and measurements remain separate. |
| #35 | D36/D41/D44/D45 settle license, naming, repository layout and contribution policy; D49 specifies private/public version histories. | Legal review of contribution documents and release enforcement: #37. |
| #70 | Required/forbidden live extraction corpus and run comparison exist; the earlier local run passed 19 fixtures. D89 records its existing variance policy. | OpenRouter/GPU verification and corpus-wide quality remain incomplete. |
| #12 | The randomized clustering proposal is superseded by D58's deterministic density/refinement implementation. Connectedness, input-order independence and parent-subset tests pass. This is not a claim of algorithm parity. | Quality comparisons and scaling remain #15/#16/#103. |
| #66 | The client checkpoint protocol is superseded by D50's operator-held seal/verification contract for a self-hosted instance. | Release and operational procedures remain #37/#76. |

Issue #18 is a standing obligation for the life of the repository, not a finite feature awaiting
implementation. Parent issues #83/#98 stay open while their actual child acceptance remains unfinished.

The complete uncached real-PostgreSQL suite through message embedding generations and semantic input bounds passes at **91.9%**,
meeting the enforced **91.6%** floor. Targeted race checks and `go vet ./...` pass. These
are correctness checks, not production latency or scaling measurements.

Inference profiles are tracked separately. The full local inference suite at `d9448d1` passed in
2696.859 seconds. The new persisted message-vector journey also passed against the configured local
provider with its actual 2560-dimensional output, including exact and approximate retrieval. The targeted description/thematic/speaker rerun
against `17ac077` passed in 462.482 seconds. Known semantic separation limitations remain explicit
in the tests. OpenRouter stopped on upstream HTTP 429; a later bounded retry was also rate-limited.
Its verification is incomplete. The disposable eight-A100 GPU profile now passes the full inference suite and persisted
embedding/passage journeys with both vLLM models deployed through Compose (#138, D109).
Its complete regression passes at 92.0% coverage; see `docs/23-gpu-qualification.md`. A running
GPU endpoint is provisioned for each run and terminated afterward, rather than kept as a prerequisite.

Operator observability (#94) now includes bounded readiness, worker progress, local probes and
content-free operator aggregates. Compose consumes the probe contract; Helm integration awaits #76.
Temporal preservation (#116) now archives validity under its original knowledge interval (D78),
with stable fact IDs, causal erasure/retention and history from the first assertion. Controlled workload
measurement and rebuild integration remain open. Record browsing and history (#89) now expose bounded project/subject inventory and archived intervals
(D81). D84 adds source-backed retractions, atomic batches and versions. D85 adds attributable corrections and authored-source replay; D91 adds direct authored assertions and stable retries. D86 bounds automatic supersession timestamps against retained knowledge. Replay identity (#118) and source-revision validation (#117) support the remaining curated lifecycle and rebuild work (#89/#58).
Anchoring’s ownership and workload criteria stay in #102. The remaining projection lifecycle and nonlocal profile verification keep #84 open; #100 is closed with joint lifecycle evidence. Product and adapter journeys retain their acceptance criteria
and remain in the execution order below.

## Assessment

The service has a working PostgreSQL-backed memory spine, with five public operations: observations,
freshness, recalls, erasures and exports. Role-aware evidence, exact quote spans, two-hop paths,
range-based temporal storage, projection registration, retention, project credentials and audit sealing
are implemented. The implementation is not feature-complete: substantial product functionality exists
only as internal mechanisms or open issues.

The audit covered the write/formation/read/erasure paths, API contracts, identity, operational entry
points, deployment/test controls and integration inventory. It is a code-and-test review, not an
exhaustive security certification or a production performance measurement. No external inference was
called and no production instance was changed. Architectural choices are assessed under the current
register; route counts alone are not a functional equivalence test.

## Verification

- `make test COVER_PROFILE=/private/tmp/taisce-review-coverage.out` passed against the running test
  PostgreSQL substrate. Coverage was **88.2%**, meeting the current floor.
- Additional isolated database/HTTP probes exposed behavior not covered by that suite:
  - two different data subjects asserted a first-person current residence: 2 facts, only 1 current;
  - identical repeated observations returned different IDs and offsets 0 and 1;
  - a supplied message group ordinal was refused with HTTP 400;
  - trailing top-level JSON and duplicate question properties both returned HTTP 200;
  - an arbitrary synthetic bearer prefix was copied verbatim into the audit principal.
- A hops=999 probe returned 200. This is intentional clamping in Recaller, not classified as a new
  defect. The requested/effective control contract should be made explicit in [#92](https://github.com/althunibat/taisce/issues/92).
- Probes used disposable fixtures in the test database and were removed from the source tree after
  recording results. They were not weakening edits to the existing suite.
- No claim is made that entity resolution, semantic retrieval, throughput, p95/p99 latency or
  failover meets a production target. Those measurements remain tracked separately.

## Execution order

1. **Identity and security:** [#84](https://github.com/althunibat/taisce/issues/84) first-person binding, [#88](https://github.com/althunibat/taisce/issues/88) audit content, [#87](https://github.com/althunibat/taisce/issues/87) JSON ambiguity,
   with [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85)/[ensera-ai/taisce#94](https://github.com/ensera-ai/taisce/issues/94) privilege design. These protect attribution and the boundaries all later APIs use.
2. **Reliable writes:** [#85](https://github.com/althunibat/taisce/issues/85) idempotency, [#86](https://github.com/althunibat/taisce/issues/86) message groups, [ensera-ai/taisce#58](https://github.com/ensera-ai/taisce/issues/58) rebuild/generation lifecycle,
   [ensera-ai/taisce#91](https://github.com/ensera-ai/taisce/issues/91) concurrency semantics. Resolve before adapters encode the contract.
3. **Resource and operational bounds:** [ensera-ai/taisce#101](https://github.com/ensera-ai/taisce/issues/101) statement deadlines, [#93](https://github.com/althunibat/taisce/issues/93) admission controls,
   [#94](https://github.com/althunibat/taisce/issues/94) readiness/metrics, [ensera-ai/taisce#109](https://github.com/ensera-ai/taisce/issues/109) timing fixture, [ensera-ai/taisce#110](https://github.com/ensera-ai/taisce/issues/110) embeddings/indexes.
4. **Quality:** [ensera-ai/taisce#98](https://github.com/ensera-ai/taisce/issues/98) historical belief semantics; [ensera-ai/taisce#105](https://github.com/ensera-ai/taisce/issues/105)/[ensera-ai/taisce#106](https://github.com/ensera-ai/taisce/issues/106) reachable semantic/thematic results;
   [ensera-ai/taisce#43](https://github.com/ensera-ai/taisce/issues/43)/[ensera-ai/taisce#45](https://github.com/ensera-ai/taisce/issues/45) extraction quality; [ensera-ai/taisce#11](https://github.com/ensera-ai/taisce/issues/11)/[ensera-ai/taisce#15](https://github.com/ensera-ai/taisce/issues/15)/[ensera-ai/taisce#16](https://github.com/ensera-ai/taisce/issues/16)/[ensera-ai/taisce#7](https://github.com/ensera-ai/taisce/issues/7) measured resolution, answer quality and scaling.
5. **Usable product:** [#89](https://github.com/althunibat/taisce/issues/89) curated memory, [#90](https://github.com/althunibat/taisce/issues/90) citation inspection, [#91](https://github.com/althunibat/taisce/issues/91) ingestion, [#92](https://github.com/althunibat/taisce/issues/92) recall
   controls, [#95](https://github.com/althunibat/taisce/issues/95) subject lifecycle, then the session/file/MCP/adapter/portal journeys below.
6. **Decisions:** D90 resolves sensitivity through projects; [#97](https://github.com/althunibat/taisce/issues/97) Go integration scope. Preserve D35/D39/D50 unless
   a deliberate decision changes the single-tenant, noncommercial or audit-proof model.

Each issue has its own acceptance evidence. This review does not mark implementation issues closed.

## Database design backlog

[Database design epic #98](https://github.com/althunibat/taisce/issues/98) retains PostgreSQL as the
working substrate and tracks the evidence needed for one tenant per deployment, with projects as
logical boundaries. This is planned work, not a claim that the schema has already been hardened.

1. [#99](https://github.com/althunibat/taisce/issues/99): align provisioning and terminology with a single tenant.
2. [#100](https://github.com/althunibat/taisce/issues/100): enforce project-consistent graph and evidence relationships.
3. [#101](https://github.com/althunibat/taisce/issues/101): measure vector partitioning and correct parent-index assumptions.
4. [#102](https://github.com/althunibat/taisce/issues/102): measure and index name/alias anchoring.
5. [#103](https://github.com/althunibat/taisce/issues/103): validate mixed-workload capacity, quality and operational limits.

Resolve project integrity alongside identity/security work, then measure indexes and partitioning
before choosing a production layout. The epic also links the existing temporal, rebuild and scaling
work. [Release issue #37](https://github.com/althunibat/taisce/issues/37) tracks private `x.y.z`
development and the separate Apache-2.0 public `x.y.0` snapshot publication journey.

## Newly identified work

| Priority | Issue | Scope |
|---|---|---|
| P1 | [#84](https://github.com/althunibat/taisce/issues/84) | Bind first-person claims to the data subject before entity resolution and supersession |
| P1 | [#85](https://github.com/althunibat/taisce/issues/85) | Make observation retries idempotent at the public write boundary |
| P1 | [#86](https://github.com/althunibat/taisce/issues/86) | Preserve atomic message groups through the observation API |
| P1 | [#87](https://github.com/althunibat/taisce/issues/87) | Reject ambiguous and trailing JSON before dispatching a request |
| P1 | [#88](https://github.com/althunibat/taisce/issues/88) | Keep caller-controlled authentication text out of the permanent audit ledger |
| P2 | [#89](https://github.com/althunibat/taisce/issues/89) | Provide supported memory inspection, correction, deletion and history operations |
| P2 | [#90](https://github.com/althunibat/taisce/issues/90) | Resolve stored citations independently with complete authorized provenance |
| P2 | [#91](https://github.com/althunibat/taisce/issues/91) | Add a bounded document-ingestion and bulk-import journey with precise source provenance |
| P2 | [#92](https://github.com/althunibat/taisce/issues/92) | Expose per-request recall budgets and explicit source selection |
| P1 | [#93](https://github.com/althunibat/taisce/issues/93) | Bound request amplification, ingestion backlog and shared resource consumption |
| P2 | [#94](https://github.com/althunibat/taisce/issues/94) | Expose operational readiness and formation health without memory content |
| P2 | [#95](https://github.com/althunibat/taisce/issues/95) | Define subject inventory and identifier handling for governed memory |
| P2 | [#96](https://github.com/althunibat/taisce/issues/96) | Decide and enforce sensitivity-aware access within a project |
| P2 | [#97](https://github.com/althunibat/taisce/issues/97) | Decide and track a Go client and agent adapter under the shared conformance contract |

## Capability inventory

These 79 reference operation names identify user capabilities, not a requirement to reproduce their
spelling or routing. Equivalent target behavior is credited only at its actual exposed boundary.
“Implemented” means code plus exercised test coverage for the core behavior; unresolved related
issues still apply. “Excluded” records a current decision, not a missing implementation disguised as
completion.

| Capability reference | Status | Target behavior / gap | Tracking |
|---|---|---|---|
| `recall` | Partial | POST /v1/recalls: anchored facts and temporal/hop controls; semantic/thematic reach and per-call controls incomplete. | [ensera-ai/taisce#105](https://github.com/ensera-ai/taisce/issues/105), [ensera-ai/taisce#106](https://github.com/ensera-ai/taisce/issues/106), [#92](https://github.com/althunibat/taisce/issues/92) |
| `assert` | Implemented | POST /v1/records/assert: attributable authored sources, atomic 1–20 batches, closed vocabulary and erasure-safe retry keys (D91). | [#89](https://github.com/althunibat/taisce/issues/89) |
| `get_watermark` | Implemented | GET /v1/freshness exposes stored/formed offsets and parked count. | [ensera-ai/taisce#48](https://github.com/ensera-ai/taisce/issues/48), [#94](https://github.com/althunibat/taisce/issues/94) |
| `resolve_citation` | Partial | Independent authorized lookup verifies exact message bytes with bounded multi-source pagination (D77). D78 adds preserved temporal views and successor/source links; D84 adds attributable retractions; complete rebuild mappings remain incomplete. | [#90](https://github.com/althunibat/taisce/issues/90) |
| `memory_create` | Implemented | POST /v1/records/assert creates independent authored records with exact source evidence (D91). | [#89](https://github.com/althunibat/taisce/issues/89) |
| `memory_get` | Implemented | POST /v1/citations/resolve reads a retained record, versions, temporal state and paginated exact evidence (D77–D85). | [#89](https://github.com/althunibat/taisce/issues/89) |
| `memory_list` | Implemented | POST /v1/records/list provides bounded project/subject inventory with immutable-ID cursors (D81). | [#89](https://github.com/althunibat/taisce/issues/89) |
| `memory_update` | Implemented | POST /v1/records/correct replaces a current record with attributable evidence and optimistic version checks (D85). | [#89](https://github.com/althunibat/taisce/issues/89) |
| `memory_delete` | Implemented | POST /v1/records/retract withdraws a claim while retaining source evidence/history; subject erasure physically removes owned data (D84). | [#89](https://github.com/althunibat/taisce/issues/89) |
| `memory_history` | Implemented | POST /v1/records/history pages archived validity/knowledge intervals and transition sources (D81). | [#89](https://github.com/althunibat/taisce/issues/89) |
| `memory_feedback` | Missing | Feedback signals and their retention/authority contract remain unimplemented. | [#89](https://github.com/althunibat/taisce/issues/89) |
| `memory_batch_update` | Implemented | POST /v1/records/correct applies an atomic batch of 1–20 versioned corrections; any conflict rolls back the batch (D85). | [#89](https://github.com/althunibat/taisce/issues/89) |
| `memory_batch_delete` | Implemented | POST /v1/records/retract applies an atomic batch of 1–20 versioned withdrawals (D84). | [#89](https://github.com/althunibat/taisce/issues/89) |
| `memory_query` | Partial | Recall and record inventory expose temporal, project and subject filters; no arbitrary record query language is served. | [#89](https://github.com/althunibat/taisce/issues/89) |
| `session_put` | Missing | Session/history/checkpoint/provider-state contracts are not served. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23), [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59), [ensera-ai/taisce#26](https://github.com/ensera-ai/taisce/issues/26) |
| `session_get` | Missing | Session/history/checkpoint/provider-state contracts are not served. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23), [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59), [ensera-ai/taisce#26](https://github.com/ensera-ai/taisce/issues/26) |
| `session_delete` | Missing | Session/history/checkpoint/provider-state contracts are not served. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23), [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59), [ensera-ai/taisce#26](https://github.com/ensera-ai/taisce/issues/26) |
| `history_append` | Missing | Session/history/checkpoint/provider-state contracts are not served. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23), [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59), [ensera-ai/taisce#26](https://github.com/ensera-ai/taisce/issues/26) |
| `history_get` | Missing | Session/history/checkpoint/provider-state contracts are not served. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23), [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59), [ensera-ai/taisce#26](https://github.com/ensera-ai/taisce/issues/26) |
| `checkpoint_put` | Missing | Session/history/checkpoint/provider-state contracts are not served. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23), [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59), [ensera-ai/taisce#26](https://github.com/ensera-ai/taisce/issues/26) |
| `checkpoint_get` | Missing | Session/history/checkpoint/provider-state contracts are not served. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23), [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59), [ensera-ai/taisce#26](https://github.com/ensera-ai/taisce/issues/26) |
| `checkpoint_index` | Missing | Session/history/checkpoint/provider-state contracts are not served. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23), [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59), [ensera-ai/taisce#26](https://github.com/ensera-ai/taisce/issues/26) |
| `file_write` | Implemented | POST /v1/artifacts/put stores opaque bytes as kind=file, with versions and attributable writes (D92). | [#59](https://github.com/althunibat/taisce/issues/59), [#23](https://github.com/althunibat/taisce/issues/23), [#26](https://github.com/althunibat/taisce/issues/26) |
| `file_read` | Implemented | POST /v1/artifacts/get returns exact bytes and current metadata within the authorized project (D92). | [#59](https://github.com/althunibat/taisce/issues/59), [#23](https://github.com/althunibat/taisce/issues/23), [#26](https://github.com/althunibat/taisce/issues/26) |
| `file_delete` | Implemented | POST /v1/artifacts/delete checks the current version, removes bytes/source and releases allowance (D92). | [#59](https://github.com/althunibat/taisce/issues/59), [#23](https://github.com/althunibat/taisce/issues/23), [#26](https://github.com/althunibat/taisce/issues/26) |
| `file_list` | Implemented | POST /v1/artifacts/list returns bounded metadata pages with owner, kind and literal name-prefix filters (D92). | [#59](https://github.com/althunibat/taisce/issues/59), [#23](https://github.com/althunibat/taisce/issues/23), [#26](https://github.com/althunibat/taisce/issues/26) |
| `file_exists` | Implemented | Artifact lookup returns 200 for a retained accessible object and identical 404 for unknown, foreign, expired or deleted IDs (D92). | [#59](https://github.com/althunibat/taisce/issues/59), [#23](https://github.com/althunibat/taisce/issues/23), [#26](https://github.com/althunibat/taisce/issues/26) |
| `file_search` | Implemented (metadata) | Literal case-sensitive name-prefix and kind filters search metadata; opaque payload contents are never searched or formed (D92). | [#59](https://github.com/althunibat/taisce/issues/59), [#23](https://github.com/althunibat/taisce/issues/23), [#26](https://github.com/althunibat/taisce/issues/26) |
| `state_get` | Implemented (opaque state) | Artifact lookup reads exact kind=state bytes. Framework session/checkpoint semantics remain #23/#26. | [#59](https://github.com/althunibat/taisce/issues/59), [#23](https://github.com/althunibat/taisce/issues/23), [#26](https://github.com/althunibat/taisce/issues/26) |
| `state_put` | Implemented (opaque state) | Artifact put stores versioned, owned kind=state bytes without inference. Framework session/checkpoint semantics remain #23/#26. | [#59](https://github.com/althunibat/taisce/issues/59), [#23](https://github.com/althunibat/taisce/issues/23), [#26](https://github.com/althunibat/taisce/issues/26) |
| `list_plans` | Excluded by decision | D35/D39: one self-hosted deployment, no subscriptions or commercial entitlements. | Decision register |
| `get_billing` | Excluded by decision | D35/D39: one self-hosted deployment, no subscriptions or commercial entitlements. | Decision register |
| `change_plan` | Excluded by decision | D35/D39: one self-hosted deployment, no subscriptions or commercial entitlements. | Decision register |
| `attach_payment_method` | Excluded by decision | D35/D39: one self-hosted deployment, no subscriptions or commercial entitlements. | Decision register |
| `cancel_subscription` | Excluded by decision | D35/D39: one self-hosted deployment, no subscriptions or commercial entitlements. | Decision register |
| `list_invoices` | Excluded by decision | D35/D39: one self-hosted deployment, no subscriptions or commercial entitlements. | Decision register |
| `get_brand` | Decision / portal pending | No branding API. Commercial brand features are outside D39; instance presentation belongs with the portal/site scope. | [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77), [ensera-ai/taisce#83](https://github.com/ensera-ai/taisce/issues/83) |
| `set_brand` | Decision / portal pending | No branding API. Commercial brand features are outside D39; instance presentation belongs with the portal/site scope. | [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77), [ensera-ai/taisce#83](https://github.com/ensera-ai/taisce/issues/83) |
| `observe` | Partial | POST /v1/observations stores role/time-bearing messages; retry identity and groups are missing. | [#85](https://github.com/althunibat/taisce/issues/85), [#86](https://github.com/althunibat/taisce/issues/86), [#91](https://github.com/althunibat/taisce/issues/91) |
| `forget` | Implemented | POST /v1/erasures uses registered projection deletion and residuals; permission refinement pending. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#94](https://github.com/ensera-ai/taisce/issues/94), [#84](https://github.com/althunibat/taisce/issues/84) |
| `reprocess` | Partial | `taisce rebuild facts` publishes one source; `taisce rebuild project` checkpoints bounded pages across a fixed project log range. Other projection rebuilds remain open. | [ensera-ai/taisce#58](https://github.com/ensera-ai/taisce/issues/58), [ensera-ai/taisce#108](https://github.com/ensera-ai/taisce/issues/108) |
| `reprocess_status` | Partial | `taisce rebuild status` reads durable project progress; `taisce rebuild cancel` stops later source admission and settles orphaned checkpoints without inference. | [ensera-ai/taisce#58](https://github.com/ensera-ai/taisce/issues/58), [ensera-ai/taisce#108](https://github.com/ensera-ai/taisce/issues/108) |
| `retry_formation` | Internal / CLI partial | Parking/unparking and failure state exist; full authorized API/operator recovery journey is unfinished. | [ensera-ai/taisce#48](https://github.com/ensera-ai/taisce/issues/48), [ensera-ai/taisce#58](https://github.com/ensera-ai/taisce/issues/58), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `skip_formation` | Internal / CLI partial | Parking/unparking and failure state exist; full authorized API/operator recovery journey is unfinished. | [ensera-ai/taisce#48](https://github.com/ensera-ai/taisce/issues/48), [ensera-ai/taisce#58](https://github.com/ensera-ai/taisce/issues/58), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `create_project` | Partial / decision | Project provisioning, settings and suspension exist in CLI. No project-list/public-sharing API; grants require a deliberate identity design. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `grant_access` | Missing | Project-bound tokens exist; human grant lifecycle remains open. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#94](https://github.com/ensera-ai/taisce/issues/94) |
| `revoke_access` | Missing | Project-bound tokens exist; human grant lifecycle remains open. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#94](https://github.com/ensera-ai/taisce/issues/94) |
| `set_project_public` | Partial / decision | Project provisioning, settings and suspension exist in CLI. No project-list/public-sharing API; grants require a deliberate identity design. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `list_my_projects` | Partial / decision | Project provisioning, settings and suspension exist in CLI. No project-list/public-sharing API; grants require a deliberate identity design. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `list_documents` | Missing | No document ingestion/listing journey. | [#91](https://github.com/althunibat/taisce/issues/91) |
| `list_memory_entities` | Partial | Entities are stored and traversed; supported entity inventory/purge and progress are missing. | [#89](https://github.com/althunibat/taisce/issues/89), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `delete_memory_entity` | Partial | Entities are stored and traversed; supported entity inventory/purge and progress are missing. | [#89](https://github.com/althunibat/taisce/issues/89), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `memory_entity_purge_status` | Partial | Entities are stored and traversed; supported entity inventory/purge and progress are missing. | [#89](https://github.com/althunibat/taisce/issues/89), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `list_memory_events` | Partial / changed contract | Audit ledger and CLI inspection/verification exist; portal evidence inspection is pending. | [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77), [ensera-ai/taisce#94](https://github.com/ensera-ai/taisce/issues/94) |
| `get_memory_event` | Partial / changed contract | Audit ledger and CLI inspection/verification exist; portal evidence inspection is pending. | [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77), [ensera-ai/taisce#94](https://github.com/ensera-ai/taisce/issues/94) |
| `get_consistency_proof` | Excluded by decision | D50 replaces a client-side proof protocol with a locally verified batch-seal chain; this is not equivalent to an externally held signed checkpoint. | Decision register |
| `create_api_key` | CLI implemented / API pending | Project-scoped credential issue/list/revoke in CLI; human-authorized management surface pending. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `list_api_keys` | CLI implemented / API pending | Project-scoped credential issue/list/revoke in CLI; human-authorized management surface pending. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `revoke_api_key` | CLI implemented / API pending | Project-scoped credential issue/list/revoke in CLI; human-authorized management surface pending. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `invite_user` | Missing | No human account/session/invitation lifecycle. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85) |
| `list_users` | Missing | No human account/session/invitation lifecycle. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85) |
| `change_user_role` | Missing | No human account/session/invitation lifecycle. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85) |
| `suspend_user` | Missing | No human account/session/invitation lifecycle. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85) |
| `reactivate_user` | Missing | No human account/session/invitation lifecycle. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85) |
| `withdraw_invitation` | Missing | No human account/session/invitation lifecycle. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85) |
| `get_usage` | Partial | Freshness and logs exist; resource/formation metrics are incomplete. Billing usage is excluded under D39. | [#93](https://github.com/althunibat/taisce/issues/93), [#94](https://github.com/althunibat/taisce/issues/94) |
| `list_sessions` | Missing | No session list or session store public contract. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23) |
| `list_parked` | Internal / CLI partial | Parking/unparking and failure state exist; full authorized API/operator recovery journey is unfinished. | [ensera-ai/taisce#48](https://github.com/ensera-ai/taisce/issues/48), [ensera-ai/taisce#58](https://github.com/ensera-ai/taisce/issues/58), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77) |
| `register_subject` | Implemented | POST /v1/subjects/register creates a stable random ID with an optional external mapping and erasure-safe retry identity (D94). | [#95](https://github.com/althunibat/taisce/issues/95) |
| `list_subjects` | Implemented | POST /v1/subjects/list provides bounded live UUID pagination within the authenticated project (D94). | [#95](https://github.com/althunibat/taisce/issues/95) |
| `get_subject` | Implemented | POST /v1/subjects/get resolves one exact ID or external reference without crossing projects (D94). | [#95](https://github.com/althunibat/taisce/issues/95) |
| `update_subject` | Implemented | POST /v1/subjects/update rotates current metadata with version checks while preserving source attribution (D94). | [#95](https://github.com/althunibat/taisce/issues/95) |
| `export_subject` | Implemented | POST /v1/exports traverses observations/messages and registered projections; principal authorization remains coarse. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#94](https://github.com/ensera-ai/taisce/issues/94) |
| `create_webhook` | Missing / tracked | Durable signed formation notifications and endpoint lifecycle are not served. | [ensera-ai/taisce#49](https://github.com/ensera-ai/taisce/issues/49) |
| `list_webhooks` | Missing / tracked | Durable signed formation notifications and endpoint lifecycle are not served. | [ensera-ai/taisce#49](https://github.com/ensera-ai/taisce/issues/49) |
| `update_webhook` | Missing / tracked | Durable signed formation notifications and endpoint lifecycle are not served. | [ensera-ai/taisce#49](https://github.com/ensera-ai/taisce/issues/49) |
| `delete_webhook` | Missing / tracked | Durable signed formation notifications and endpoint lifecycle are not served. | [ensera-ai/taisce#49](https://github.com/ensera-ai/taisce/issues/49) |
| `list_webhook_deliveries` | Missing / tracked | Durable signed formation notifications and endpoint lifecycle are not served. | [ensera-ai/taisce#49](https://github.com/ensera-ai/taisce/issues/49) |
| `redeliver_webhook` | Missing / tracked | Durable signed formation notifications and endpoint lifecycle are not served. | [ensera-ai/taisce#49](https://github.com/ensera-ai/taisce/issues/49) |

## Cross-cutting coverage

| Area | Current state | Required evidence / tracking |
|---|---|---|
| Person, agent and run identity | Subject attribution is stored; first-person nodes are bound to project-scoped subject identity. An optional governed registry supplies random subject IDs and exact external-reference mappings (D94); agent/run protocols remain open. | [#84](https://github.com/althunibat/taisce/issues/84), [ensera-ai/taisce#93](https://github.com/ensera-ai/taisce/issues/93), [#95](https://github.com/althunibat/taisce/issues/95); multi-person correction and erasure tests. |
| Temporal history and rebuilding | Supersession preserves earlier intervals (D78); bounded receipt recovery preserves missing admitted outcomes (D88); single-source generation cutover preserves old interpretations and records lineage (D97). Project fact orchestration is available (D98); other projection generations remain open. | [ensera-ai/taisce#98](https://github.com/ensera-ai/taisce/issues/98), [ensera-ai/taisce#58](https://github.com/ensera-ai/taisce/issues/58), [ensera-ai/taisce#108](https://github.com/ensera-ai/taisce/issues/108), [ensera-ai/taisce#109](https://github.com/ensera-ai/taisce/issues/109); late-arrival and knowledge-history proofs. |
| Semantic/profile/thematic reach | Source-owned entity embedding generations now expose bounded semantic candidates without automatic merging; communities/reports exist but have no persisted semantic retrieval surface. Public recall still begins with exact anchors. | [#139](https://github.com/althunibat/taisce/issues/139), [ensera-ai/taisce#105](https://github.com/ensera-ai/taisce/issues/105), [ensera-ai/taisce#106](https://github.com/ensera-ai/taisce/issues/106), [ensera-ai/taisce#110](https://github.com/ensera-ai/taisce/issues/110); paraphrase, near-miss, personal preference, absent-answer and multi-hop measurements. |
| Ranking and relevance | Bundle reports reach rather than uncalibrated relevance confidence; ranking/salience remains a measurement decision. | D52, [ensera-ai/taisce#92](https://github.com/ensera-ai/taisce/issues/92), [ensera-ai/taisce#11](https://github.com/ensera-ai/taisce/issues/11), [ensera-ai/taisce#15](https://github.com/ensera-ai/taisce/issues/15); do not introduce an uncalibrated cutoff. |
| Evidence and trust | Quotes, spans, source roles and context windows exist; bounded source-role controls and independent citation/history lookup are served (D77–D85). Complete rebuild mappings remain open. | [#90](https://github.com/althunibat/taisce/issues/90), [#92](https://github.com/althunibat/taisce/issues/92), [ensera-ai/taisce#67](https://github.com/ensera-ai/taisce/issues/67), [ensera-ai/taisce#45](https://github.com/ensera-ai/taisce/issues/45). |
| Formation recovery | Attempts, backoff, parking, unpark and contiguous freshness exist; admin/API workflow incomplete. | [ensera-ai/taisce#48](https://github.com/ensera-ai/taisce/issues/48), [ensera-ai/taisce#58](https://github.com/ensera-ai/taisce/issues/58), [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77), [#94](https://github.com/althunibat/taisce/issues/94); restore a parked turn without silent data loss. |
| Erasure and retention | Registered projection walk, residual counting and clock-triggered retention implemented and tested. | Re-prove for every new store; [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59), [#89](https://github.com/althunibat/taisce/issues/89), [#95](https://github.com/althunibat/taisce/issues/95). |
| Authentication and privileges | Project-bound random tokens, hashed storage, revocation and separated DB identities exist. Human sessions and fine-grained capabilities absent. | [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#94](https://github.com/ensera-ai/taisce/issues/94), [#88](https://github.com/althunibat/taisce/issues/88), [#96](https://github.com/althunibat/taisce/issues/96). |
| Operational performance | Bounded fact rows and hop fanout exist; term/anchor/admission bounds are implemented; controlled-host scaling and deployment-wide capacity remain open. | [ensera-ai/taisce#101](https://github.com/ensera-ai/taisce/issues/101), [#93](https://github.com/althunibat/taisce/issues/93), [#94](https://github.com/althunibat/taisce/issues/94), [ensera-ai/taisce#7](https://github.com/ensera-ai/taisce/issues/7), [ensera-ai/taisce#16](https://github.com/ensera-ai/taisce/issues/16). |
| Model quality/egress | Closed ontology, role/polarity checks, allowlist and regression corpus exist; inference transport/routing and quality gaps remain. | [ensera-ai/taisce#43](https://github.com/ensera-ai/taisce/issues/43), [ensera-ai/taisce#45](https://github.com/ensera-ai/taisce/issues/45), [ensera-ai/taisce#103](https://github.com/ensera-ai/taisce/issues/103), [ensera-ai/taisce#104](https://github.com/ensera-ai/taisce/issues/104), [ensera-ai/taisce#111](https://github.com/ensera-ai/taisce/issues/111). |
| Sessions/history/checkpoints/opaque files | Opaque project-owned state/files are served under D92. Session/history/checkpoint protocols remain open. | [ensera-ai/taisce#23](https://github.com/ensera-ai/taisce/issues/23), [ensera-ai/taisce#59](https://github.com/ensera-ai/taisce/issues/59); identity, versioning, group and erasure conformance. |
| MCP/coding-agent integration | Not served. | [ensera-ai/taisce#31](https://github.com/ensera-ai/taisce/issues/31); uses the same domain authorization and message contract. |
| Python/.NET/Java/TypeScript integrations | Planned as independent repositories under D44; no accessible published adapter repository found during this review. | [ensera-ai/taisce#20](https://github.com/ensera-ai/taisce/issues/20)–[ensera-ai/taisce#30](https://github.com/ensera-ai/taisce/issues/30) and [ensera-ai/taisce#26](https://github.com/ensera-ai/taisce/issues/26) conformance; [#97](https://github.com/althunibat/taisce/issues/97) considers Go explicitly. |
| Notifications | Not served. | [ensera-ai/taisce#49](https://github.com/ensera-ai/taisce/issues/49); signed, durable, content-free delivery, bounded retry and SSRF defense. |
| Portal/site/identity administration | Operator CLI exists; portal, human grant/session flows and public-copy guards incomplete. | [ensera-ai/taisce#77](https://github.com/ensera-ai/taisce/issues/77), [ensera-ai/taisce#83](https://github.com/ensera-ai/taisce/issues/83), [ensera-ai/taisce#85](https://github.com/ensera-ai/taisce/issues/85), [ensera-ai/taisce#94](https://github.com/ensera-ai/taisce/issues/94). |
| Delivery and supply chain | Compose works; private/public snapshot tooling and CI gates exist; signing/artifacts and measured HA packaging remain open. | [ensera-ai/taisce#37](https://github.com/ensera-ai/taisce/issues/37), [ensera-ai/taisce#76](https://github.com/ensera-ai/taisce/issues/76), [ensera-ai/taisce#84](https://github.com/ensera-ai/taisce/issues/84); signing, scan, reproducibility, backup/restore and failover evidence. |
| Commercial features | No subscription, invoices, plan entitlements, tenant signup or dormant paid/free-account reclamation. | Explicitly excluded by D35/D39. Operational resource bounds still required under [#93](https://github.com/althunibat/taisce/issues/93). |
| Verification protocol | Local batch-sealed audit chain and CLI verification exist; no client-held signed checkpoint/consistency proof protocol. | Explicit D50 tradeoff; do not claim equivalence to external verification. |

## Limits and follow-through

The service test pass is meaningful evidence of the current tested spine, not proof that all advertised
or planned product surfaces exist. Runtime guardrails do not establish performance without a workload;
quote containment does not establish entailment; an audit chain does not prevent an operator who owns
the database from rebuilding the chain. Those limits must remain visible in product claims.

Existing issue ownership was retained for already-tracked gaps rather than filing duplicates. New
issues contain only standalone requirements, target code evidence, impact and acceptance criteria.
