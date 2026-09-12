package inference

import (
	"os"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
)

// Regenerates the golden after a DELIBERATE wording change, run with CAPTURE=1.
//
// It exists so that regenerating is an explicit act rather than something a failing test invites
// somebody to do by hand. The golden is not the guarantee — the corpus is — but it is what makes a
// prompt edit impossible to make silently.
func TestCaptureGolden(t *testing.T) {
	if os.Getenv("CAPTURE") == "" {
		t.Skip("regenerates the golden; run with CAPTURE=1 after a deliberate wording change")
	}
	v, err := domain.NewOntology([]domain.Predicate{
		{Name: "works_at", SemanticType: "identity", Cardinality: domain.CardinalityMany,
			ObjectKind: "organisation", Description: "An organisation the subject works for."},
		{Name: "lives_in", SemanticType: "identity", Cardinality: domain.CardinalityOne,
			ObjectKind: "place", Description: "Where the subject lives."},
		{Name: "occurred_on", SemanticType: "temporal", Cardinality: domain.CardinalityOne,
			ObjectKind: "time", Description: "When the subject happened."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("testdata/extraction_system_prompt.golden",
		[]byte(systemPrompt(v)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("golden regenerated; the corpus is what says the edit was an improvement")
}
