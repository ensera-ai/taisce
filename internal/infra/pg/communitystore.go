// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/community"
	"github.com/ensera-ai/taisce/internal/report"
)

// ProjectionCommunityReport is the projection kind a report registers as.
//
// Declared in migration 0020 with `survives_sharing = false`, which is the first kind to answer that
// question with no: an entity is a shared identity and keeping it discloses nothing, while a report is
// shared text and keeping it because somebody else also contributed leaves the departing subject's
// words in a row the erasure counted as retained.
const ProjectionCommunityReport = "community_report"

var ErrReportSourceUnavailable = errors.New("report source provenance is missing, stale, erased, or outside the project")

// CommunityStore reads the graph a partition is made over and stores what came back.
type CommunityStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

// NewCommunityStore binds the store to one tenant, for the reason the recall store is bound: a tenant
// chosen per call is a tenant that can be chosen wrongly per call.
func NewCommunityStore(pool *pgxpool.Pool, schema Schema) *CommunityStore {
	return &CommunityStore{pool: pool, schema: schema}
}

// Graph reads the currently-valid relations in a scope as an undirected weighted graph.
//
// # Why every source, unlike a recall
//
// A recall admits only what the principal said, because a fact from a fetched page must not be
// returned as the person's own. A community is a shape rather than an assertion: leaving out
// tool-sourced relations would partition a graph that is not the graph, and the subjects it found
// would be about a memory the system does not have. What the source filter protects is what is
// RETURNED, and that filter still applies when a thematic answer fetches the facts behind a subject.
//
// # Why the weight is a count
//
// Two entities joined by four relations are more connected than two joined by one, and nothing here
// judges which relation matters more — that would be a ranking stage needing a measurement behind it.
func (s *CommunityStore) Graph(ctx context.Context, scope string) ([]community.Edge, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(selectGraphSQL), scope)
	if err != nil {
		return nil, fmt.Errorf("read the graph: %w", err)
	}
	defer rows.Close()

	var out []community.Edge
	for rows.Next() {
		var e community.Edge
		if err := rows.Scan(&e.Source, &e.Target, &e.Weight); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const selectGraphSQL = `
SELECT f.subject_entity_id::text, f.object_entity_id::text, count(*)::float8
  FROM {schema}.fact f
 WHERE f.scope = $1
   AND f.subject_entity_id IS NOT NULL
   AND f.object_entity_id IS NOT NULL
   AND upper_inf(f.valid) AND upper_inf(f.known)
 GROUP BY f.subject_entity_id, f.object_entity_id`

// subjectNamespace is the fixed namespace a community's identifier is derived in.
//
// A constant, generated once and never changed: it is what makes an identifier a function of the
// membership rather than of when the row was written, so two deployments that formed the same subject
// from the same facts name it the same thing.
var subjectNamespace = uuid.MustParse("6f2a1c0e-9d3b-5a47-8e21-4c7b0f9d63a5")

// Identity is a community's identifier, derived from what is in it.
//
// # Why derived rather than assigned
//
// A partition is recomputed from the whole graph on every pass, and a fresh identifier per pass would
// make every subject a new subject — so every report would be discarded and rewritten on every tick,
// forever, for material that had not moved. A model call per subject per tick is not a cost anybody
// would choose and it is invisible until the bill arrives.
//
// Derived from the sorted membership and the level, so a subject that did not change keeps its name
// and keeps what was written about it. It is also what makes a rebuild reproduce identifiers: the same
// facts produce the same partition (the algorithm is deterministic) and the same partition produces
// the same identifiers, so a restored instance does not rename everything a caller had stored.
//
// The parent is deliberately NOT in the key. A group whose members are unchanged is the same subject
// whether or not the tree above it moved, and what was written about it is still true.
func Identity(level int, members []string) uuid.UUID {
	sorted := append([]string(nil), members...)
	sort.Strings(sorted)
	return uuid.NewSHA1(subjectNamespace,
		[]byte(strconv.Itoa(level)+"\x00"+strings.Join(sorted, "\x00")))
}

// Replace reconciles a scope's partition with the one just computed.
//
// # Why reconcile rather than replace outright
//
// A partition is a function of the whole graph — one new fact can move an entity between subjects,
// merge two or split one, and no incremental edit produces what re-partitioning would say. So the
// PARTITION is recomputed wholesale, and that is not in question.
//
// What must not be recomputed is the identity. A subject whose membership is unchanged is the same
// subject, keeps its identifier, and keeps the prose written about it; a subject that is gone takes
// its report with it by cascade, which is right because a report describes a set of members that no
// longer exists. Deleting everything and reinserting would be simpler and would rewrite every report
// on every tick.
func (s *CommunityStore) Replace(ctx context.Context, scope string, h community.Hierarchy) error {
	ids := make(map[int]uuid.UUID, len(h.Communities))
	surviving := make([]string, 0, len(h.Communities))
	for _, c := range h.Communities {
		ids[c.ID] = Identity(int(c.Level), c.Members)
		surviving = append(surviving, ids[c.ID].String())
	}

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Everything the new partition does not contain, and its reports by cascade.
		if _, err := tx.Exec(ctx, s.schema.SQL(
			`DELETE FROM {schema}.community
			  WHERE scope = $1 AND NOT (community_id::text = ANY($2))`),
			scope, surviving); err != nil {
			return fmt.Errorf("remove subjects the partition no longer holds: %w", err)
		}

		for _, c := range h.Communities {
			var parent any
			if c.Parent >= 0 {
				parent = ids[c.Parent]
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(insertCommunitySQL),
				scope, ids[c.ID], c.Level, parent); err != nil {
				return fmt.Errorf("store community %d: %w", c.ID, err)
			}
			for _, member := range c.Members {
				if _, err := tx.Exec(ctx, s.schema.SQL(insertMemberSQL),
					scope, ids[c.ID], member); err != nil {
					return fmt.Errorf("store a member of community %d: %w", c.ID, err)
				}
			}
		}
		return nil
	})
}

// The level and the parent are updated on a subject that already exists, because a group with the same
// members can sit at a different depth once the tree around it changes — and a stale parent is a walk
// that descends into the wrong place. The membership cannot differ: it is what the identifier is made
// from.
const insertCommunitySQL = `
INSERT INTO {schema}.community (scope, community_id, level, parent_id)
VALUES ($1, $2, $3, $4::uuid)
ON CONFLICT (scope, community_id) DO UPDATE
   SET level = EXCLUDED.level, parent_id = EXCLUDED.parent_id`

const insertMemberSQL = `
INSERT INTO {schema}.community_member (scope, community_id, entity_id)
VALUES ($1, $2, $3::uuid)
ON CONFLICT DO NOTHING`

// Stored is one community as it came back from the database.
type Stored struct {
	ID      string
	Level   int
	Parent  string
	Members []string
}

// Unwritten returns the communities in a scope that have no report yet, deepest first.
//
// # Why deepest first
//
// A parent too large to describe from its own facts is described from its children's reports, so the
// children have to exist first. Ordering the work by descending level is what makes one pass enough
// rather than needing a second to fill in what the first could not.
// Unwritten returns the communities that need a report from this writer: the ones with none, and the
// ones whose report something else wrote.
func (s *CommunityStore) Unwritten(ctx context.Context, scope string, limit int, writer string) ([]Stored, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(selectUnwrittenSQL), scope, limit, writer)
	if err != nil {
		return nil, fmt.Errorf("read unwritten communities: %w", err)
	}
	defer rows.Close()

	var out []Stored
	for rows.Next() {
		var c Stored
		var parent *string
		if err := rows.Scan(&c.ID, &c.Level, &parent, &c.Members); err != nil {
			return nil, err
		}
		if parent != nil {
			c.Parent = *parent
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// A community needs a report when it has none, or when the one it has was written by something else.
//
// The second half is what makes a prompt edit reach stored prose. The invalidation trigger fires on
// a changed FACT; a changed WRITER was invisible, so a deployment could tune its report prompt and
// keep serving paragraphs written under the old wording forever, with nothing recording which was
// which. Comparing the stored digest to the current one is the whole mechanism, and it costs one
// equality per row.
// ScopesOwingReports names the projects holding a community whose report is missing or was written
// by something else, up to a bound.
//
// The driver needs this because a project with nothing left to form is never offered again, and a
// report is deleted when the facts under it change: an erasure or a correction in a quiet project
// would otherwise leave a hole nobody fills. One indexed pass over communities and their
// reports, which is why it names scopes rather than counting what each one owes.
func (s *CommunityStore) ScopesOwingReports(ctx context.Context, writer string, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(selectScopesOwingReportsSQL), writer, limit)
	if err != nil {
		return nil, fmt.Errorf("list projects owing a report: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, fmt.Errorf("scan a project owing a report: %w", err)
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}

const selectScopesOwingReportsSQL = `
SELECT DISTINCT c.scope
  FROM {schema}.community c
  LEFT JOIN {schema}.community_report r
         ON r.scope = c.scope AND r.community_id = c.community_id
 WHERE r.report_id IS NULL OR r.written_by <> $1
 ORDER BY c.scope
 LIMIT $2`

const selectUnwrittenSQL = `
SELECT c.community_id::text, c.level, c.parent_id::text,
       coalesce(array_agg(m.entity_id::text ORDER BY m.entity_id)
                FILTER (WHERE m.entity_id IS NOT NULL), '{}')
  FROM {schema}.community c
  LEFT JOIN {schema}.community_member m
         ON m.scope = c.scope AND m.community_id = c.community_id
  LEFT JOIN {schema}.community_report r
         ON r.scope = c.scope AND r.community_id = c.community_id
 WHERE c.scope = $1 AND (r.report_id IS NULL OR r.written_by <> $3)
 GROUP BY c.community_id, c.level, c.parent_id
 ORDER BY c.level DESC, c.community_id
 LIMIT $2`

// Children returns the communities a subject was split into, with their members.
//
// A parent too large to describe from its own facts is described from its children's reports, so this
// is what the roll-up walks. Ordered by identifier, so a parent's context is assembled the same way on
// every rebuild.
func (s *CommunityStore) Children(ctx context.Context, scope, parentID string) ([]Stored, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(selectChildrenSQL), scope, parentID)
	if err != nil {
		return nil, fmt.Errorf("read children: %w", err)
	}
	defer rows.Close()

	var out []Stored
	for rows.Next() {
		var c Stored
		if err := rows.Scan(&c.ID, &c.Level, &c.Members); err != nil {
			return nil, err
		}
		c.Parent = parentID
		out = append(out, c)
	}
	return out, rows.Err()
}

const selectChildrenSQL = `
SELECT c.community_id::text, c.level,
       coalesce(array_agg(m.entity_id::text ORDER BY m.entity_id)
                FILTER (WHERE m.entity_id IS NOT NULL), '{}')
  FROM {schema}.community c
  LEFT JOIN {schema}.community_member m
         ON m.scope = c.scope AND m.community_id = c.community_id
 WHERE c.scope = $1 AND c.parent_id = $2::uuid
 GROUP BY c.community_id, c.level
 ORDER BY c.community_id`

// Material returns the facts among a set of entities, with the words behind them.
//
// The quotes travel with the statements because a report written from statements alone is a summary
// of summaries: the extractor already compressed a sentence into a triple, and compressing that again
// loses the thing a reader would check.
func (s *CommunityStore) Material(ctx context.Context, scope string, entityIDs []string) (
	[]string, []report.Fact, error) {

	rows, err := s.pool.Query(ctx, s.schema.SQL(selectMaterialSQL), scope, entityIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("read community material: %w", err)
	}
	defer rows.Close()

	var facts []report.Fact
	for rows.Next() {
		var f report.Fact
		if err := rows.Scan(&f.SourceObservationID, &f.SourceRevision, &f.SubjectID, &f.Subject, &f.Predicate, &f.ObjectID, &f.Object, &f.Statement, &f.Quote); err != nil {
			return nil, nil, err
		}
		facts = append(facts, f)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	names, err := s.names(ctx, scope, entityIDs)
	if err != nil {
		return nil, nil, err
	}
	return names, facts, nil
}

// Both ends inside the community, because a relation reaching out of it is about the neighbour rather
// than about this subject — and a report written from it would describe something it does not contain.
const selectMaterialSQL = `
SELECT e.source_observation_id::text, source.fact_revision, s.entity_id::text, s.canonical_name, f.predicate, o.entity_id::text, o.canonical_name, f.statement, e.quote
  FROM {schema}.fact f
  JOIN {schema}.entity s ON s.entity_id = f.subject_entity_id AND s.scope = f.scope
  JOIN {schema}.entity o ON o.entity_id = f.object_entity_id  AND o.scope = f.scope
  JOIN {schema}.fact_evidence e ON e.fact_id = f.fact_id
  JOIN {schema}.observation source ON source.scope=e.scope AND source.observation_id=e.source_observation_id
 WHERE f.scope = $1
   AND f.subject_entity_id = ANY($2::uuid[])
   AND f.object_entity_id  = ANY($2::uuid[])
   AND upper_inf(f.valid) AND upper_inf(f.known)
 ORDER BY f.predicate, f.fact_id`

func (s *CommunityStore) names(ctx context.Context, scope string, entityIDs []string) ([]string, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT canonical_name FROM {schema}.entity
		 WHERE scope = $1 AND entity_id = ANY($2::uuid[]) ORDER BY canonical_name`),
		scope, entityIDs)
	if err != nil {
		return nil, fmt.Errorf("read entity names: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Report reads a community's report, for a parent that has to be written from its children's.
func (s *CommunityStore) Report(ctx context.Context, scope, communityID string) (report.Report, bool, error) {
	var r report.Report
	var findings []byte
	err := s.pool.QueryRow(ctx, s.schema.SQL(`
		SELECT title, summary, importance, importance_reason, findings,
               ARRAY(SELECT DISTINCT d.source_observation_id::text
                       FROM {schema}.projection_dependency d
                      WHERE d.scope=r.scope AND d.projection_kind='community_report'
                        AND d.projection_id=r.report_id::text ORDER BY d.source_observation_id::text),
               coalesce((SELECT jsonb_object_agg(d.source_observation_id::text,d.report_source_revision)
                       FROM {schema}.projection_dependency d
                      WHERE d.scope=r.scope AND d.projection_kind='community_report'
                        AND d.projection_id=r.report_id::text),'{}'::jsonb)
          FROM {schema}.community_report r WHERE scope = $1 AND community_id = $2::uuid`),
		scope, communityID).Scan(&r.Title, &r.Summary, &r.Importance, &r.ImportanceReason, &findings, &r.SourceObservationIDs, &r.SourceRevisions)
	if err == pgx.ErrNoRows {
		return report.Report{}, false, nil
	}
	if err != nil {
		return report.Report{}, false, fmt.Errorf("read report: %w", err)
	}
	if err := json.Unmarshal(findings, &r.Findings); err != nil {
		return report.Report{}, false, fmt.Errorf("decode findings: %w", err)
	}
	return r, true, nil
}

// Write stores a report and registers it against every observation that contributed to it.
//
// # The registration is the erasure, and it is in the same transaction
//
// A report holds what several people said. It is registered per contributing observation and, because
// its kind declares that sharing does not save it (0019), an erasure by any contributor removes it —
// counted in the same receipt as everything else. Registering in a second transaction would leave a
// window in which the prose exists and nothing knows it has to be erased.
//
// The contributing observations are derived from the evidence behind the facts the report was written
// from, which is the only honest answer to "who contributed": the report was written from those
// quotes, and those quotes came from those messages.
// Write stores one community's report, stamped with what wrote it.
//
// The writer identity is what makes a prompt edit reach stored prose. An existing report by the same
// writer is left alone — the material has not changed and neither would the paragraph — and one by a
// different writer is replaced in place, keeping its own identity so that every registration already
// pointing at it stays correct.
func (s *CommunityStore) Write(ctx context.Context, scope, communityID string, r report.Report,
	c report.Context, writer string) error {

	findings, err := json.Marshal(r.Findings)
	if err != nil {
		return fmt.Errorf("encode findings: %w", err)
	}
	sources := map[string]int64{}
	addSource := func(id string, revision int64) bool {
		if id == "" || revision < 1 {
			return false
		}
		if previous, found := sources[id]; found && previous != revision {
			return false
		}
		sources[id] = revision
		return true
	}
	for _, f := range c.Facts {
		if !addSource(f.SourceObservationID, f.SourceRevision) {
			return ErrReportSourceUnavailable
		}
	}
	for _, child := range c.Children {
		if len(child.SourceObservationIDs) == 0 || len(child.SourceRevisions) != len(child.SourceObservationIDs) {
			return ErrReportSourceUnavailable
		}
		for _, id := range child.SourceObservationIDs {
			if !addSource(id, child.SourceRevisions[id]) {
				return ErrReportSourceUnavailable
			}
		}
	}
	if len(sources) == 0 {
		return ErrReportSourceUnavailable
	}
	sourceIDs := make([]string, 0, len(sources))
	for id := range sources {
		sourceIDs = append(sourceIDs, id)
	}
	sort.Strings(sourceIDs)

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Lock the exact source set until the report and its registrations commit. A source erased
		// during generation is a refusal, not an INSERT SELECT that silently registers fewer people.
		rows, err := tx.Query(ctx, s.schema.SQL(`SELECT observation_id::text,fact_revision FROM {schema}.observation
            WHERE scope=$1 AND observation_id=ANY($2::uuid[]) ORDER BY observation_id FOR SHARE`), scope, sourceIDs)
		if err != nil {
			return fmt.Errorf("lock report sources: %w", err)
		}
		count := 0
		for rows.Next() {
			var sourceID string
			var revision int64
			if err := rows.Scan(&sourceID, &revision); err != nil {
				rows.Close()
				return err
			}
			if sources[sourceID] != revision {
				rows.Close()
				return ErrReportSourceUnavailable
			}
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if count != len(sourceIDs) {
			return ErrReportSourceUnavailable
		}
		// A lost race is a no-op, and so is a rewrite by the same writer: the update's WHERE clause
		// returns no row unless the writer actually differs, and a report rewritten in place keeps
		// its own identity so the registrations already pointing at it stay correct. Never register
		// a freshly generated ID when the conflict stored no row.
		var id string
		err = tx.QueryRow(ctx, s.schema.SQL(insertReportSQL), scope, uuid.New(), communityID, r.Title, r.Summary,
			r.Importance, r.ImportanceReason, findings, c.Substituted, c.Dropped, writer).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("store report: %w", err)
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(registerReportSQL), scope, id, sourceIDs, ProjectionCommunityReport); err != nil {
			return fmt.Errorf("register report: %w", err)
		}
		return nil
	})
}

const insertReportSQL = `
INSERT INTO {schema}.community_report
    (scope, report_id, community_id, title, summary, importance, importance_reason, findings,
     substituted, dropped, written_by)
VALUES ($1, $2, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (scope, community_id) DO UPDATE
   SET title = EXCLUDED.title, summary = EXCLUDED.summary, importance = EXCLUDED.importance,
       importance_reason = EXCLUDED.importance_reason, findings = EXCLUDED.findings,
       substituted = EXCLUDED.substituted, dropped = EXCLUDED.dropped,
       written_by = EXCLUDED.written_by, written_at = now()
 WHERE {schema}.community_report.written_by <> EXCLUDED.written_by
RETURNING report_id::text`

// One registration per contributing observation AND per data subject on it, because the erasure
// predicate selects on the pair. An observation with no data subject registers with none, which is
// correct: nobody can erase it by subject and it goes when the observation does.
const registerReportSQL = `
INSERT INTO {schema}.projection_dependency
    (source_observation_id, scope, projection_kind, projection_id, data_subject_id, report_source_revision)
SELECT o.observation_id, $1, $4, $2, o.data_subject_id, o.fact_revision
  FROM {schema}.observation o
 WHERE o.scope=$1 AND o.observation_id=ANY($3::uuid[])
ON CONFLICT DO NOTHING`
