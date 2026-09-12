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
