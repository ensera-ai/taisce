// Package recall answers a question from entities outward.
//
// Retrieval starts at the entities a question names and returns the facts about them. It does not
// search passages and it does not fan out over the corpus, which is the whole inversion: cost is
// proportional to a neighbourhood rather than to how much has been stored.
//
// This is the thinnest honest form of that: anchor, facts, bundle. Nothing ranks, nothing fuses,
// nothing traverses. Every one of those is a stage that can only be justified by a measurement it
// improves, and adding one before the plain path is measured makes it impossible to say which stage
// is doing the work — or which is doing harm.
package recall

import (
	"context"
	"strings"
	"unicode"

	"github.com/ensera-ai/taisce/internal/domain"
)

// longestAnchor is the most words an entity name is assumed to have.
//
// Four. Every window up to this length is looked up, so the cost of raising it is a longer candidate
// list on every recall and the cost of lowering it is entities whose names are simply unreachable.
// Names longer than this exist; they are reached by whichever shorter window matches an alias.
const longestAnchor = 4

// Store is the read this package needs. It is an interface here so that what recall DOES is
// separable from where the rows are — and so the anchoring rules can be read without reading SQL.
type Store interface {
	// Anchors resolves candidate terms to entities within the authorised scopes.
	Anchors(ctx context.Context, scopes, terms []string) ([]domain.Anchor, error)
	// FactsAbout returns the currently-valid facts naming any of the given entities, with their
	// evidence, up to limit+1 rows so the caller can tell a full result from a truncated one.
	FactsAbout(ctx context.Context, scopes, entityIDs []string, limit int) ([]domain.CitedFact, error)
}

// Recaller answers questions against one tenant's memory.
type Recaller struct {
	store Store
	// limit caps the bundle. It is a cap and not a selection: nothing has ranked the facts, so
	// cutting at the limit drops arbitrary members rather than the least useful ones.
	limit int
}

// New builds a recaller. A limit of zero or less takes the default.
func New(store Store, limit int) *Recaller {
	if limit <= 0 {
		limit = 50
	}
	return &Recaller{store: store, limit: limit}
}

// Recall resolves a question to entities and returns the facts naming them.
//
// # The authorised scopes are an argument, never a default
//
// A project is a permission inside one tenant, and a principal granted several recalls across them
// in one bundle. So the set comes from the caller on every read. There is no "all scopes" and no
// empty-means-everything: an empty set returns nothing, because a read that widens when its
// permission list is missing is the failure that permission list exists to prevent.
//
// # No anchor means an empty bundle, not an error
//
// A question naming nothing we know about is an ordinary outcome — it is most questions, early in a
// scope's life. A caller made to distinguish that from a failure will end up treating failures as
// ordinary.
func (r *Recaller) Recall(ctx context.Context, scopes []string, question string) (domain.Bundle, error) {
	if len(scopes) == 0 {
		return domain.Bundle{}, nil
	}
	terms := Terms(question)
	if len(terms) == 0 {
		return domain.Bundle{}, nil
	}

	anchors, err := r.store.Anchors(ctx, scopes, terms)
	if err != nil {
		return domain.Bundle{}, err
	}
	if len(anchors) == 0 {
		return domain.Bundle{}, nil
	}

	ids := make([]string, 0, len(anchors))
	for _, a := range anchors {
		ids = append(ids, a.Entity.ID)
	}

	// One more than the limit, so a full result is distinguishable from a truncated one without a
	// second count query — and without the caller having to guess from the row count whether they
	// are looking at everything.
	facts, err := r.store.FactsAbout(ctx, scopes, ids, r.limit+1)
	if err != nil {
		return domain.Bundle{}, err
	}
	bundle := domain.Bundle{Anchors: anchors, Facts: facts}
	if len(facts) > r.limit {
		bundle.Facts = facts[:r.limit]
		bundle.Truncated = true
	}
	return bundle, nil
}

// Terms returns the candidate entity names a question might be naming.
//
// # Why every window rather than one pass of named-entity recognition
//
// A model call per question would put a second inference hop in front of every read, on the path a
// caller is waiting on, to answer something a lookup already answers: an entity is in memory or it
// is not, and its name is a string we already hold. Windowing the question and asking the index is
// exact, has no vocabulary of its own, and costs one query.
//
// # Why the windows are normalised the same way entity names are
//
// The resolution key is `NormalizeName`, so anything that generates lookup candidates has to produce
// them in exactly that shape. Any other normalisation here would silently fail to match entities
// that are in fact stored — the worst kind of miss, because the entity is right there.
//
// Punctuation is dropped at token boundaries and nowhere else, so `work?` reaches `work` while
// `o'brien` and `co-op` survive as written.
func Terms(question string) []string {
	words := strings.FieldsFunc(question, func(r rune) bool {
		return unicode.IsSpace(r) || (unicode.IsPunct(r) && r != '\'' && r != '-')
	})
	for i, w := range words {
		words[i] = domain.NormalizeName(strings.Trim(w, "'-"))
	}

	seen := make(map[string]bool, len(words)*longestAnchor)
	var terms []string
	for start := range words {
		for length := 1; length <= longestAnchor && start+length <= len(words); length++ {
			term := strings.Join(words[start:start+length], " ")
			if term == "" || seen[term] {
				continue
			}
			seen[term] = true
			terms = append(terms, term)
		}
	}
	return terms
}
