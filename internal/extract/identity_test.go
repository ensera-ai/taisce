// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package extract

import (
	"context"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
)

type identityModel string

func (m identityModel) ExtractionIdentity() string { return string(m) }
func (identityModel) Propose(context.Context, domain.Message, domain.Ontology) ([]Proposal, error) {
	return nil, nil
}

type anonymousModel struct{}

func (anonymousModel) Propose(context.Context, domain.Message, domain.Ontology) ([]Proposal, error) {
	return nil, nil
}

func TestExtractionFingerprintIncludesOrderedVocabularyAndAdmissionButNotSetOrder(t *testing.T) {
	predicates := []domain.Predicate{{Name: "works_at", Cardinality: domain.CardinalityMany, Description: "employment"}, {Name: "lives_in", Cardinality: domain.CardinalityOne}}
	makeExtractor := func(model string, ps []domain.Predicate, terms ...string) *Extractor {
		v, err := domain.NewOntology(ps)
		if err != nil {
			t.Fatal(err)
		}
		u := map[string]struct{}{}
		for _, s := range terms {
			u[s] = struct{}{}
		}
		return NewWith(identityModel(model), domain.Vocabulary{Ontology: v, Unresolvable: u})
	}
	base := makeExtractor("model-a", predicates, "he", "she")
	want := base.PipelineVersion()
	if !strings.HasPrefix(want, "extract/v2:") || len(want) != 75 {
		t.Fatal("fingerprint is not bounded")
	}
	if makeExtractor("model-a", predicates, "she", "he").PipelineVersion() != want {
		t.Fatal("set order changed the identity")
	}
	changed := append([]domain.Predicate{}, predicates...)
	changed[0].Description = "changed"
	for _, e := range []*Extractor{makeExtractor("model-b", predicates, "he", "she"), makeExtractor("model-a", predicates, "he"), makeExtractor("model-a", changed, "he", "she"), makeExtractor("model-a", []domain.Predicate{predicates[1], predicates[0]}, "he", "she")} {
		if e.PipelineVersion() == want {
			t.Fatal("changed extraction rules kept their identity")
		}
	}
	old := admissionImplementation
	admissionImplementation = append(append([]byte{}, old...), 'x')
	got := base.PipelineVersion()
	admissionImplementation = old
	if got == want {
		t.Fatal("changed admission implementation kept its identity")
	}
	if New(anonymousModel{}, domain.Ontology{}).PipelineVersion() != "extract/v1" || New(identityModel(""), domain.Ontology{}).PipelineVersion() != "extract/v1" {
		t.Fatal("unidentified model was represented as a known model")
	}
}
