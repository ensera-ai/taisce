// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

const RecordListQueryForTesting = recordListSQL
const SubjectRecordListQueryForTesting = subjectRecordListSQL
const RecordHistoryQueryForTesting = recordHistorySQL

// FeedbackListQueryForTesting renders the list exactly as the store sends it, so a plan test
// measures the statement that runs rather than one written beside it.
func FeedbackListQueryForTesting(scope, recordID string, openOnly bool, after *FeedbackCursor, limit int) (string, []any, error) {
	return feedbackListQuery(scope, recordID, openOnly, after, limit)
}

// EntityRecordListQueryForTesting renders the entity's page exactly as the store sends it, and
// OrderedSeekForTesting is the statement the store runs before it in the same transaction. A plan
// taken without the second is the plan of a different read.
func EntityRecordListQueryForTesting(scope, entity, subject string, after *RecordCursor, limit int) (string, []any, error) {
	return entityRecordListQuery(scope, entity, subject, after, limit)
}

const OrderedSeekForTesting = orderedSeekSQL
