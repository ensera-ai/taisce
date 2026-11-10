// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// Two management operations over the closed relation vocabulary: extend it, and read what the
// instance admits.
//
// # Why this exists at all
//
// The vocabulary is the one surface that fits a deployment to its own domain. A deployment whose
// conversations are about clinical visits or shipments meets relations the seeded set cannot name,
// every one of them is refused as unmapped, and until these two operations existed the only way to
// widen it was to connect to the database as an identity none of the serving processes holds. That
// is not a boundary; it is the absence of a mechanism, and it priced fitting the vocabulary to a
// deployment at one release per deployment.
//
// # What this file is allowed to decide
//
// Transport, attribution and the shape of a refusal. It decides nothing about whether a declaration
// is acceptable: the name's shape and the three closed axes are CHECK constraints on the table, the
// uniqueness of a name is its primary key, and all five declarations are required because each
// column is NOT NULL. Every one of them is left to refuse, and what this file adds is naming which
// one did — a rule enforced here as well would be the same rule in two places, and the copy no
// constraint enforces is the one that drifts.
//
// # What the vocabulary is scoped to, stated plainly
//
// It is the instance's, not the project's. `predicate` is one table per tenant namespace, keyed by
// the relation name alone, and `fact.predicate` references it from every project in the namespace.
// So a relation added here is admitted for every project in this instance, immediately.
//
// The project an operation names is therefore what the operator says they are acting on and what
// the ledger records, not a partition of the vocabulary. It is still required, and still has to be
// an active project: an operation attributed to a project that does not exist is attributed to
// nothing, which is the same rule that stops a credential being minted for a name no project uses.
//
// # What adding one costs
//
// The extractor's identity folds the vocabulary in, so adding a relation changes the identity every
// fact formed afterwards carries. That is not a side effect to hide — it is the record of what
// changed, and it is what a rebuild reads to find the facts formed under the older vocabulary.
//
// # When it takes effect, and what it does not cover
//
// A formation worker reads the vocabulary at the top of every turn, so a relation added here is in
// the list the model is given for the next turn that scope forms. No restart, and nothing for an
// operator to remember.
//
// What it does not cover is a turn already in flight. A turn is stamped with its extractor identity
// before its first model call and the vocabulary is folded into that identity, so a turn that has
// already been attempted — and failed — cannot complete under a changed vocabulary: it is refused
// as a changed extraction and parks, and forming it needs a rebuild. That is the same rule that
// stops a turn being half one model's reading and half another's, and the limit is stated where an
// operator reads it, beside the command in the operator guide.
package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// vocabularyAddRequest is the five declarations, and the project the operator is acting on.
//
// No field has a default and none is optional. `cardinality` is why: it decides whether a later
// fact closes this one or coexists with it, it cannot be worked out afterwards because by then all
// that is visible is two rows, and a default would be a silent wrong answer months later.
type vocabularyAddRequest struct {
	Project      string `json:"project"`
	Predicate    string `json:"predicate"`
	SemanticType string `json:"semantic_type"`
	Cardinality  string `json:"cardinality"`
	ObjectKind   string `json:"object_kind"`
	Description  string `json:"description"`
}

type vocabularyListRequest struct {
	Project string `json:"project"`
}

// vocabularyPredicate is a relation as the table holds it, which is not quite as it was declared:
// `event` is the column's default rather than one of the five, and it is reported so that what an
// operator sees is the row and not their own request read back to them.
type vocabularyPredicate struct {
	Predicate    string `json:"predicate"`
	SemanticType string `json:"semantic_type"`
	Cardinality  string `json:"cardinality"`
	ObjectKind   string `json:"object_kind"`
	Description  string `json:"description"`
	Event        bool   `json:"event"`
}

type vocabularyAddResponse struct {
	Project   string              `json:"project"`
	Predicate vocabularyPredicate `json:"predicate"`
}

type vocabularyListResponse struct {
	Project    string                `json:"project"`
	Predicates []vocabularyPredicate `json:"predicates"`
}

func asVocabularyPredicate(p domain.Predicate) vocabularyPredicate {
	return vocabularyPredicate{Predicate: p.Name, SemanticType: p.SemanticType,
		Cardinality: string(p.Cardinality), ObjectKind: p.ObjectKind,
		Description: p.Description, Event: p.Event}
}

// addPredicate extends the vocabulary, and lets the table say no.
//
// The five declarations are passed through as they arrived. What is checked here is only what no
// constraint can check: that a project was named, and that it is one this instance is serving — so
// the row the ledger writes attributes the change to something real.
func (m *ManagementServer) addPredicate(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req vocabularyAddRequest
	if !decode(w, r, &req) {
		return
	}
	if !m.actingOn(w, r, grant, domain.AuditVocabularyAdd, req.Project) {
		return
	}
	added, err := m.stores.Vocabulary.Add(r.Context(), pg.NewPredicate{
		Predicate: req.Predicate, SemanticType: req.SemanticType, Cardinality: req.Cardinality,
		ObjectKind: req.ObjectKind, Description: req.Description,
	})
	var refused *pg.VocabularyRefusal
	switch {
	case errors.As(err, &refused) && refused.Duplicate:
		// Nothing about the declaration need be wrong: the relation is already admitted, and the
		// operator's next move is to use it rather than to correct anything. Its own code, so a
		// script can tell "already there" from "will not do".
		m.record(r, domain.AuditVocabularyAdd, grant, req.Project, domain.OutcomeRefused, 0)
		writeError(w, http.StatusConflict, codePredicateConflict, refused.Error())
		return
	case errors.As(err, &refused):
		// The store's own words, not a second wording of them: the refusal is the constraint's
		// name and a fixed phrase, which is what tells an operator which of the five declarations
		// to fix, and it is the substrate's vocabulary rather than anything the caller sent.
		m.record(r, domain.AuditVocabularyAdd, grant, req.Project, domain.OutcomeRefused, 0)
		writeError(w, http.StatusBadRequest, codeInvalidPredicate, refused.Error())
		return
	case err != nil:
		m.log.Error("extend the relation vocabulary", "error", err, "project", req.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "the relation could not be added")
		return
	}
	m.record(r, domain.AuditVocabularyAdd, grant, req.Project, domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusCreated, vocabularyAddResponse{Project: req.Project, Predicate: asVocabularyPredicate(added)})
}

// listPredicates is what this instance admits, in the order the extractor is told it.
func (m *ManagementServer) listPredicates(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req vocabularyListRequest
	if !decode(w, r, &req) {
		return
	}
	if !m.actingOn(w, r, grant, domain.AuditVocabularyList, req.Project) {
		return
	}
	predicates, err := m.stores.Vocabulary.List(r.Context())
	if err != nil {
		m.log.Error("read the relation vocabulary", "error", err, "project", req.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "the vocabulary could not be read")
		return
	}
	out := vocabularyListResponse{Project: req.Project, Predicates: make([]vocabularyPredicate, 0, len(predicates))}
	for _, p := range predicates {
		out.Predicates = append(out.Predicates, asVocabularyPredicate(p))
	}
	m.record(r, domain.AuditVocabularyList, grant, req.Project, domain.OutcomeAllowed, len(out.Predicates))
	writeJSON(w, http.StatusOK, out)
}

// actingOn resolves the project an operation is attributed to, and refuses it when there is none.
//
// Shared by both vocabulary operations so they cannot disagree about what a named project has to be.
// A refusal here is on the ledger too: a change attempted against a project that does not exist is
// exactly the attempt an operator wants to find afterwards, and it is the one a typo produces.
func (m *ManagementServer) actingOn(w http.ResponseWriter, r *http.Request, grant credential.Grant, operation, project string) bool {
	if strings.TrimSpace(project) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidProject, "a project is required")
		return false
	}
	if _, err := m.stores.Projects.Active(r.Context(), project); err != nil {
		if errors.Is(err, pg.ErrNoSuchProject) {
			m.record(r, operation, grant, project, domain.OutcomeRefused, 0)
			writeError(w, http.StatusNotFound, codeNotFound, "no such active project")
			return false
		}
		m.log.Error("resolve the project an operation names", "error", err, "project", project)
		writeError(w, http.StatusInternalServerError, codeInternal, "the request could not be served")
		return false
	}
	return true
}
