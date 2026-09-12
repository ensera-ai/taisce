// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
)

// contextRequest asks for one subject's history under a budget: the newest turns verbatim, the
// rest as the segments the compaction pass wrote.
type contextRequest struct {
	DataSubjectID string `json:"data_subject_id"`
	// MaxCharacters is the content allowance, in Unicode code points of message text and summary
	// text; omitted means the server's recall budget.
	MaxCharacters *int `json:"max_characters"`
}

type contextResponse struct {
	// Segments stand in for the older history, oldest first; each names the range it covers.
	Segments []contextSegment `json:"segments"`
	// Turns are the newest turns as they were said, oldest first.
	Turns []contextTurn `json:"turns"`
	// Watermark is how far the deployment has formed, so a caller knows what the context cannot
	// yet include.
	Watermark  freshnessResponse `json:"watermark"`
	Characters int               `json:"characters"`
	Truncated  bool              `json:"truncated"`
}

type contextSegment struct {
	SegmentID  string `json:"segment_id"`
	Level      int    `json:"level"`
	FromOffset int64  `json:"from_offset"`
	ToOffset   int64  `json:"to_offset"`
	Covered    int    `json:"covered"`
	Summary    string `json:"summary"`
}

type contextTurn struct {
	LogOffset  int64            `json:"log_offset"`
	OccurredAt time.Time        `json:"occurred_at"`
	Messages   []contextMessage `json:"messages"`
}

type contextMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// assembleContext answers with no model call: the planner's choice over what the store holds.
func (s *Server) assembleContext(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req contextRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.DataSubjectID) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidContext, "a data subject is required: a context is one subject's history")
		return
	}
	budget := s.recaller.Budget().Characters
	if req.MaxCharacters != nil {
		if *req.MaxCharacters <= 0 || *req.MaxCharacters > budget {
			writeError(w, http.StatusBadRequest, codeInvalidContext, "max_characters must be positive and no larger than the server's budget")
			return
		}
		budget = *req.MaxCharacters
	}
	if s.contexts == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "contexts could not be assembled")
		return
	}
	assembled, err := s.contexts.Context(r.Context(), grant.Project, req.DataSubjectID, budget)
	if err != nil {
		s.record(r, domain.AuditContextAssemble, grant, domain.OutcomeRefused, 0)
		s.failed(w, err, domain.AuditContextAssemble, grant.Project, "the context could not be assembled")
		return
	}
	fresh, err := s.observations.Freshness(r.Context(), s.schema, grant.Project)
	if err != nil {
		s.failed(w, err, "read freshness", grant.Project, "the context could not be assembled")
		return
	}
	out := contextResponse{Segments: []contextSegment{}, Turns: []contextTurn{}, Characters: assembled.Characters, Truncated: assembled.Truncated,
		Watermark: freshnessResponse{Scope: fresh.Scope, Parked: fresh.Parked}}
	if fresh.HasStored {
		stored := fresh.Stored
		out.Watermark.Stored = &stored
	}
	if fresh.HasFormed {
		formed := fresh.Formed
		out.Watermark.Formed = &formed
	}
	for _, seg := range assembled.Segments {
		out.Segments = append(out.Segments, contextSegment{SegmentID: seg.ID, Level: seg.Level, FromOffset: seg.From, ToOffset: seg.To, Covered: seg.Covered, Summary: seg.Summary})
	}
	for _, t := range assembled.Turns {
		turn := contextTurn{LogOffset: t.Offset, OccurredAt: t.OccurredAt, Messages: []contextMessage{}}
		for _, m := range t.Messages {
			turn.Messages = append(turn.Messages, contextMessage{Role: m.Role, Content: m.Content})
		}
		out.Turns = append(out.Turns, turn)
	}
	s.record(r, domain.AuditContextAssemble, grant, domain.OutcomeAllowed, len(out.Segments)+len(out.Turns))
	writeJSON(w, http.StatusOK, out)
}
