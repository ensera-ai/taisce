// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package semantic_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/semantic"
)

// Two rebuilds of one entity produce one surface. The quotes arrive from a query whose order is a
// property of the plan rather than of the entity, and a surface that depended on it would embed
// differently on a rebuild for no reason anybody could name — which matters most when the rebuild
// follows an erasure and somebody is checking that what remains is what should remain.
func TestTheSameEntityAlwaysProducesTheSameSurface(t *testing.T) {
	quotes := []string{
		"the Dublin office is where we sit",
		"our site in Dublin has the lab",
		"Dublin HQ moved last year",
	}
	forwards := surfaceFor(t, "Dublin office", []string{"Dublin HQ", "the Dublin site"}, quotes)

	reversed := make([]string, len(quotes))
	for i := range quotes {
		reversed[i] = quotes[len(quotes)-1-i]
	}
	backwards := surfaceFor(t, "Dublin office", []string{"the Dublin site", "Dublin HQ"}, reversed)

	if forwards != backwards {
		t.Fatalf("two orderings of one entity produced two surfaces:\n%q\n%q", forwards, backwards)
	}
}

// The name comes first, so the thing being described is stated before the evidence for it — and when
// the budget cuts, it cuts evidence rather than identity.
func TestIdentityComesBeforeEvidenceAndSurvivesTheCut(t *testing.T) {
	var quotes []string
	for i := 0; i < 200; i++ {
		quotes = append(quotes, fmt.Sprintf("%d %s", i, strings.Repeat("something was said here ", 5)))
	}
	surface := surfaceFor(t, "Dublin office", []string{"Dublin HQ"}, quotes)

	if !strings.HasPrefix(surface, "Dublin office") {
		t.Fatalf("the canonical name is not first: %q", surface[:40])
	}
	if !strings.Contains(surface, "Dublin HQ") {
		t.Fatal("an alias was cut, so identity was traded for evidence")
	}
	if len(surface) > semantic.SurfaceBudget {
		t.Fatalf("the surface is %d characters against a budget of %d",
			len(surface), semantic.SurfaceBudget)
	}
}

// A quote is included whole or not at all. Half a sentence embeds as half a sentence, which is a worse
// representation of an entity than the sentence not being there.
func TestAQuoteIsNeverCutInHalf(t *testing.T) {
	long := strings.Repeat("a complete sentence that will not fit. ", 60)
	surface := surfaceFor(t, "Marta", nil, []string{"Marta works at Ensera", long})

	if strings.Contains(surface, long[:200]) && !strings.Contains(surface, long) {
		t.Fatal("a quote was included in part")
	}
	if !strings.Contains(surface, "Marta works at Ensera") {
		t.Fatal("the quote that fitted was dropped along with the one that did not")
	}
}

// A relation asserted five times stores five identical quotes, and a surface repeating one sentence
// five times is a surface mostly about that sentence.
func TestARepeatedQuoteIsSaidOnce(t *testing.T) {
	surface := surfaceFor(t, "Marta", []string{"Marta", "MARTA "}, []string{
		"Marta works at Ensera", "Marta works at Ensera", "Marta works at Ensera",
	})
	if n := strings.Count(surface, "Marta works at Ensera"); n != 1 {
		t.Fatalf("one sentence appears %d times", n)
	}
	// An alias equal to the canonical name adds nothing but length.
	if n := strings.Count(surface, "| Marta"); n != 0 {
		t.Fatalf("the canonical name was repeated as an alias: %q", surface)
	}
}

// An entity with nothing said about it is still findable by what it was called. A surface of only
// names is thin and it is honest — the alternative is describing an entity from material that does
// not exist.
func TestAnEntityWithNoQuotesIsStillItsNames(t *testing.T) {
	surface := surfaceFor(t, "Dublin office", []string{"Dublin HQ"}, nil)
	if surface != "Dublin office | Dublin HQ" {
		t.Fatalf("got %q", surface)
	}
}

func surfaceFor(t *testing.T, canonical string, aliases, quotes []string) string {
	t.Helper()
	surface, err := semantic.Surface(canonical, aliases, quotes)
	if err != nil {
		t.Fatal(err)
	}
	return surface
}
