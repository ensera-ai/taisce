// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// What a caller may hold open at once, and how much of it they may send. The list bound is the
// record page's, because a client that can page records can page the feedback about them with the
// same loop; the note and object bounds are the correction's, because a promotion hands exactly
// these two values to one.
const (
	DefaultFeedbackPage = 20
	MaxFeedbackPage     = 100
	MaxFeedbackNote     = 16384
	MaxFeedbackObject   = 1024
)

var (
	ErrInvalidFeedback  = errors.New("invalid feedback")
	ErrFeedbackNotFound = errors.New("feedback not found")
	// ErrFeedbackPromoted is separate from ErrFeedbackNotFound so a second promotion is answered
	// with what actually happened. Folding it into "not found" would tell a caller retrying after a
	// timeout that their feedback vanished, when it succeeded.
	ErrFeedbackPromoted = errors.New("feedback was already promoted")
)

// FeedbackStore holds reports about records that assert nothing.
//
// It owns no opinion about truth. The one operation that changes what the system believes —
// Promote — does it by running the correction or the retraction a caller could have run by hand,
// through RecordStore, in the promotion's own transaction. So this type can be read in full without
// learning a second set of rules for how a fact comes to exist.
type FeedbackStore struct {
	pool    *pgxpool.Pool
	schema  Schema
	records *RecordStore
}

func NewFeedbackStore(pool *pgxpool.Pool, schema Schema, records *RecordStore) *FeedbackStore {
	return &FeedbackStore{pool: pool, schema: schema, records: records}
}

// Feedback is what was reported, and what became of it.
type Feedback struct {
	ID             string     `json:"id"`
	RecordID       string     `json:"record_id"`
	Note           string     `json:"note"`
	ProposedObject *string    `json:"proposed_object,omitempty"`
	RecordedBy     string     `json:"recorded_by"`
	RecordedAt     time.Time  `json:"recorded_at"`
	PromotedBy     *string    `json:"promoted_by,omitempty"`
	PromotedAt     *time.Time `json:"promoted_at,omitempty"`
	// ReplacementID is the record a promoted correction produced. Absent on a promoted retraction,
	// which withdraws without replacing, and on anything not yet promoted.
	ReplacementID *string `json:"replacement_record_id,omitempty"`
}

// FeedbackRequest records one report. The credential is taken from the grant and never from here:
// a body that could name its own author would make attribution a claim rather than a fact.
type FeedbackRequest struct {
	RecordID       string  `json:"record_id"`
	Note           string  `json:"note"`
	ProposedObject *string `json:"proposed_object,omitempty"`
}

type FeedbackCursor struct {
	RecordedAt time.Time `json:"recorded_at"`
	ID         string    `json:"id"`
}

type FeedbackPage struct {
	Feedback []Feedback      `json:"feedback"`
	Next     *FeedbackCursor `json:"next,omitempty"`
}

// PromotionRequest names one feedback to act on.
type PromotionRequest struct {
	ID string `json:"id"`
	// ExpectedVersion is the target record's version, not the feedback's. Feedback does not change
	// after it is written, so there is nothing about it to be stale about — but the record it
	// disputes moves, and promoting against a version the reporter never saw would apply somebody's
	// judgement to a claim they did not read. The same optimistic check a hand-written correction
	// makes, required at the same place.
	ExpectedVersion string `json:"expected_version"`
}

// Promotion says what the promotion did, in the vocabulary of the operation it performed.
type Promotion struct {
	ID       string `json:"id"`
	RecordID string `json:"record_id"`
	// Operation is "record.correct" or "record.retract" — the ledger's own name for what ran, so a
	// caller reading this and a reviewer reading the audit table are looking at one identifier.
	Operation     string  `json:"operation"`
	ReplacementID *string `json:"replacement_record_id,omitempty"`
	// Version is the replacement's version for a correction, and the withdrawn record's closing
	// version for a retraction. Either way it is what the caller sends next.
	Version    string    `json:"version"`
	PromotedAt time.Time `json:"promoted_at"`
}

// Record writes one report and registers it for erasure.
//
// # What it deliberately does not do
//
// It writes no fact, advances no watermark and touches no entity. That is the property the whole
// feature exists for: a caller reporting a doubt must be able to do so without changing an answer,
// and a test names it.
//
// # How erasure reaches it
//
// By copying the target record's own registrations: one row per registration the fact has, with the
// same source observation and the same data subject. So feedback is reachable by exactly the
// erasures that reach the record it is about — a claim that is true by construction rather than by
// two pieces of code agreeing about which observation counts as the source.
//
// It is declared `survives_sharing = false` where the fact is true, and that difference is
// deliberate. A fact two people contributed to is kept when one of them leaves, because it is also
// the other's. The feedback about it is not: one person wrote those words, and a second
// contributor to the record does not make them theirs to keep.
func (s *FeedbackStore) Record(ctx context.Context, scope, principal string, request FeedbackRequest) (Feedback, error) {
	var out Feedback
	if _, err := NewSchema(scope); err != nil {
		return out, ErrInvalidFeedback
	}
	actor, err := uuid.Parse(principal)
	if err != nil || actor == uuid.Nil {
		return out, ErrInvalidFeedback
	}
	target, err := uuid.Parse(request.RecordID)
	if err != nil || target == uuid.Nil {
		return out, ErrInvalidFeedback
	}
	if !boundedText(request.Note, 1, MaxFeedbackNote) {
		return out, ErrInvalidFeedback
	}
	if request.ProposedObject != nil && !boundedText(*request.ProposedObject, 1, MaxFeedbackObject) {
		return out, ErrInvalidFeedback
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT true FROM {schema}.fact
            WHERE scope=$1 AND fact_id=$2::uuid`), scope, target.String()).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRecordNotFound
			}
			return err
		}
		if err := tx.QueryRow(ctx, s.schema.SQL(`INSERT INTO {schema}.memory_feedback
            (scope,target_fact_id,note,proposed_object,recorded_by)
            VALUES($1,$2::uuid,$3,$4,$5::uuid) RETURNING feedback_id::text,recorded_at`),
			scope, target.String(), request.Note, request.ProposedObject, actor.String()).
			Scan(&out.ID, &out.RecordedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.projection_dependency
            (source_observation_id,scope,projection_kind,projection_id,data_subject_id)
            SELECT d.source_observation_id,$1,'feedback',$2,d.data_subject_id
              FROM {schema}.projection_dependency d
             WHERE d.scope=$1 AND d.projection_kind='fact' AND d.projection_id=$3
            ON CONFLICT DO NOTHING`), scope, out.ID, target.String()); err != nil {
			return err
		}
		// A record with no registration of its own has nothing to copy, which would leave the
		// feedback unreachable by erasure. Refusing is the fail-closed answer: the alternative is a
		// row the residual count reports as zero because nothing ever selects it.
		var registered int
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT count(*) FROM {schema}.projection_dependency
            WHERE scope=$1 AND projection_kind='feedback' AND projection_id=$2`), scope, out.ID).Scan(&registered); err != nil {
			return err
		}
		if registered == 0 {
			return ErrRecordNotFound
		}
		_, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.audit_entry
            (operation,principal,principal_kind,project,magnitude,outcome) VALUES($1,$2,$3,$4,1,$5)`),
			domain.AuditFeedbackRecord, actor.String(), domain.PrincipalCredential, scope, domain.OutcomeAllowed)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrRecordNotFound) || errors.Is(err, ErrInvalidFeedback) {
			return Feedback{}, err
		}
		return Feedback{}, fmt.Errorf("record feedback: %w", err)
	}
	out.RecordID = target.String()
	out.Note = request.Note
	out.ProposedObject = request.ProposedObject
	out.RecordedBy = actor.String()
	return out, nil
}

func boundedText(s string, min, max int) bool {
	return len(s) >= min && len(s) <= max && utf8.ValidString(s) &&
		strings.TrimSpace(s) != "" && !strings.ContainsRune(s, 0)
}

// List pages feedback newest first, optionally narrowed to one record or to what is still open.
//
// The cursor is (recorded_at, feedback_id) rather than an offset, for the reason the record page
// gives: an OFFSET rescans what it already returned and shifts every later page when a row ahead of
// it is deleted, which an erasure does. The id breaks ties so two reports written in the same
// microsecond cannot hide each other.
func (s *FeedbackStore) List(ctx context.Context, scope, recordID string, openOnly bool, after *FeedbackCursor, limit int) (FeedbackPage, error) {
	out := FeedbackPage{Feedback: []Feedback{}}
	if _, err := NewSchema(scope); err != nil || limit < 1 || limit > MaxFeedbackPage {
		return out, ErrInvalidFeedback
	}
	query, args, err := feedbackListQuery(scope, recordID, openOnly, after, limit)
	if err != nil {
		return out, err
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(query), args...)
	if err != nil {
		return out, fmt.Errorf("list feedback: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item Feedback
		if err := rows.Scan(&item.ID, &item.RecordID, &item.Note, &item.ProposedObject,
			&item.RecordedBy, &item.RecordedAt, &item.PromotedBy, &item.PromotedAt, &item.ReplacementID); err != nil {
			return FeedbackPage{}, fmt.Errorf("list feedback: %w", err)
		}
		out.Feedback = append(out.Feedback, item)
	}
	if err := rows.Err(); err != nil {
		return FeedbackPage{}, fmt.Errorf("list feedback: %w", err)
	}
	if len(out.Feedback) > limit {
		last := out.Feedback[limit-1]
		out.Feedback = out.Feedback[:limit]
		out.Next = &FeedbackCursor{RecordedAt: last.RecordedAt, ID: last.ID}
	}
	return out, nil
}

// feedbackListSQL is the shape every list runs, with its filters rendered into {where}.
//
// Separate from List so a plan test can EXPLAIN exactly what the store sends. A plan test holding a
// query written beside it rather than the one that runs proves the index works for a statement
// nothing issues, which is the drift a measured plan exists to rule out.
const feedbackListSQL = `SELECT f.feedback_id::text,f.target_fact_id::text,f.note,
        f.proposed_object,f.recorded_by::text,f.recorded_at,f.promoted_by::text,f.promoted_at,f.promotion_fact_id::text
          FROM {schema}.memory_feedback f WHERE {where}
         ORDER BY f.recorded_at DESC,f.feedback_id DESC LIMIT $2`

// feedbackListQuery renders the filters and binds them. Identifiers are fixed text; everything a
// caller supplied is a parameter, so a filter cannot become part of the statement.
func feedbackListQuery(scope, recordID string, openOnly bool, after *FeedbackCursor, limit int) (string, []any, error) {
	args := []any{scope, limit + 1}
	where := []string{"f.scope=$1"}
	if recordID != "" {
		id, err := uuid.Parse(recordID)
		if err != nil || id == uuid.Nil {
			return "", nil, ErrInvalidFeedback
		}
		args = append(args, id.String())
		where = append(where, fmt.Sprintf("f.target_fact_id=$%d::uuid", len(args)))
	}
	if openOnly {
		where = append(where, "f.promoted_at IS NULL")
	}
	if after != nil {
		id, err := uuid.Parse(after.ID)
		if err != nil || id == uuid.Nil || after.RecordedAt.IsZero() {
			return "", nil, ErrInvalidFeedback
		}
		args = append(args, after.RecordedAt, id.String())
		where = append(where, fmt.Sprintf("(f.recorded_at,f.feedback_id) < ($%d,$%d::uuid)", len(args)-1, len(args)))
	}
	return strings.Replace(feedbackListSQL, "{where}", strings.Join(where, " AND "), 1), args, nil
}

// Promote merges one feedback into the graph, as the operation the feedback describes.
//
// A proposed object makes it a correction of the target; no proposed object makes it a retraction.
// That branch is the entire difference between the two outcomes, and it is read from the single
// nullable column the writer set — so a caller cannot ask for one and get the other, and there is no
// third path by which a fact can come into existence.
//
// # Why it is one transaction
//
// The correction and the promotion mark commit together. Two transactions would leave a window in
// which the graph holds the correction and the feedback still reads as open, and the next promotion
// would apply it again — withdrawing a record nobody disputed. The feedback row is taken FOR UPDATE
// before anything else, so two concurrent promotions of one feedback serialise and the second finds
// it promoted rather than promoting it twice.
func (s *FeedbackStore) Promote(ctx context.Context, scope, principal string, request PromotionRequest) (Promotion, error) {
	var out Promotion
	if _, err := NewSchema(scope); err != nil {
		return out, ErrInvalidFeedback
	}
	actor, err := uuid.Parse(principal)
	if err != nil || actor == uuid.Nil {
		return out, ErrInvalidFeedback
	}
	id, err := uuid.Parse(request.ID)
	if err != nil || id == uuid.Nil {
		return out, ErrInvalidFeedback
	}
	version, err := uuid.Parse(request.ExpectedVersion)
	if err != nil || version == uuid.Nil {
		return out, ErrInvalidFeedback
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var target, note string
		var object *string
		var promoted *time.Time
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT target_fact_id::text,note,proposed_object,promoted_at
            FROM {schema}.memory_feedback WHERE scope=$1 AND feedback_id=$2::uuid FOR UPDATE`),
			scope, id.String()).Scan(&target, &note, &object, &promoted); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrFeedbackNotFound
			}
			return err
		}
		if promoted != nil {
			return ErrFeedbackPromoted
		}
		mutation := RecordMutation{ID: target, ExpectedVersion: version.String()}
		var replacement *string
		if object != nil {
			corrected, err := s.records.correctTx(ctx, tx, scope, principal,
				[]RecordCorrection{{RecordMutation: mutation, Object: *object, Statement: note}})
			if err != nil {
				return err
			}
			out.Operation, out.Version = domain.AuditRecordCorrect, corrected.Records[0].Version
			replacement = &corrected.Records[0].ID
		} else {
			withdrawn, err := s.records.retractTx(ctx, tx, scope, principal, []RecordMutation{mutation})
			if err != nil {
				return err
			}
			out.Operation, out.Version = domain.AuditRecordRetract, withdrawn.Records[0].Version
			out.PromotedAt = withdrawn.Records[0].RetractedAt
		}
		if err := tx.QueryRow(ctx, s.schema.SQL(`UPDATE {schema}.memory_feedback
            SET promoted_by=$3::uuid,promoted_at=now(),promotion_fact_id=$4::uuid
            WHERE scope=$1 AND feedback_id=$2::uuid RETURNING promoted_at`),
			scope, id.String(), actor.String(), replacement).Scan(&out.PromotedAt); err != nil {
			return err
		}
		out.ID, out.RecordID, out.ReplacementID = id.String(), target, replacement
		_, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.audit_entry
            (operation,principal,principal_kind,project,magnitude,outcome) VALUES($1,$2,$3,$4,1,$5)`),
			domain.AuditFeedbackPromote, actor.String(), domain.PrincipalCredential, scope, domain.OutcomeAllowed)
		return err
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrFeedbackNotFound), errors.Is(err, ErrFeedbackPromoted),
			errors.Is(err, ErrInvalidFeedback), errors.Is(err, ErrRecordNotFound),
			errors.Is(err, ErrRecordConflict), errors.Is(err, ErrInvalidRecordMutation),
			errors.Is(err, ErrEntityNameLimit):
			return Promotion{}, err
		}
		return Promotion{}, fmt.Errorf("promote feedback: %w", err)
	}
	return out, nil
}
