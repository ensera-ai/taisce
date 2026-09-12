// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package community_test

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/community"
)

// clique returns every pair among the named entities, which is the densest thing a group can be.
func clique(prefix string, n int) []community.Edge {
	var out []community.Edge
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			out = append(out, community.Edge{
				Source: fmt.Sprintf("%s%d", prefix, i),
				Target: fmt.Sprintf("%s%d", prefix, j),
				Weight: 1,
			})
		}
	}
	return out
}

func graphOf(t *testing.T, edges []community.Edge) *community.Graph {
	t.Helper()
	g, err := community.NewGraph(edges)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	return g
}

// groupsAsSets renders a partition as comparable text, so a failure says what the grouping was rather
// than that two slices differed.
func groupsAsSets(g *community.Graph, p community.Partition) []string {
	var out []string
	for _, members := range p.Groups {
		names := make([]string, 0, len(members))
		for _, node := range members {
			names = append(names, g.Name(node))
		}
		sort.Strings(names)
		out = append(out, strings.Join(names, ","))
	}
	sort.Strings(out)
	return out
}

// ── The property the whole design rests on ────────────────────────────────────────────────────
//
// A group that is two pieces joined by nothing is two subjects with one name, and everything
// downstream treats it as one: one summary, one embedding of that summary, one answer a caller reads.
// These are the graphs built to produce that.

func TestEveryGroupIsInternallyConnected(t *testing.T) {
	for name, edges := range map[string][]community.Edge{
		// A barbell: two dense ends joined by a single edge. The bridge is the node that can leave
		// and take the connection with it.
		"barbell": append(append(clique("a", 5), clique("b", 5)...),
			community.Edge{Source: "a0", Target: "b0", Weight: 1}),
		// A ring: every node has exactly two neighbours, so no group is obviously right and a naive
		// pass can cut one into arcs that do not touch.
		"ring": ring("r", 12),
		// A star: one hub, many leaves, and every leaf's only route to another leaf is the hub.
		"star": star("s", 15),
		// Two cliques joined by a path through nodes that belong to neither.
		"dumbbell with a corridor": append(append(clique("x", 4), clique("y", 4)...),
			community.Edge{Source: "x0", Target: "c1", Weight: 1},
			community.Edge{Source: "c1", Target: "c2", Weight: 1},
			community.Edge{Source: "c2", Target: "y0", Weight: 1}),
	} {
		t.Run(name, func(t *testing.T) {
			g := graphOf(t, edges)
			p := community.Detect(g)
			for i, members := range p.Groups {
				if !connected(g, members) {
					t.Fatalf("group %d is not internally connected: %v", i, groupsAsSets(g, p))
				}
			}
		})
	}
}

// Every entity is in exactly one group. A partition that covers less than the graph loses whatever it
// left out, silently — the answers that remain look fine.
func TestThePartitionCoversTheGraphExactlyOnce(t *testing.T) {
	g := graphOf(t, append(append(clique("a", 6), clique("b", 4)...),
		community.Edge{Source: "a0", Target: "b0", Weight: 1}))
	p := community.Detect(g)

	seen := map[int]int{}
	for _, members := range p.Groups {
		for _, node := range members {
			seen[node]++
		}
	}
	if len(seen) != g.Order() {
		t.Fatalf("%d of %d entities are in a group", len(seen), g.Order())
	}
	for node, times := range seen {
		if times != 1 {
			t.Fatalf("%q is in %d groups", g.Name(node), times)
		}
	}
	for node, group := range p.Of {
		if !contains(p.Groups[group], node) {
			t.Fatalf("%q is assigned to a group that does not hold it", g.Name(node))
		}
	}
}

// ── Determinism ───────────────────────────────────────────────────────────────────────────────
//
// A customer whose themes silently regrouped cannot tell that from a defect, and neither can we. The
// partition has to depend on the graph and on nothing else — not on the order edges arrived in, not
// on map iteration, and not on the version of the toolchain that compiled it.

func TestTheSameGraphAlwaysPartitionsTheSameWay(t *testing.T) {
	edges := append(append(clique("a", 7), clique("b", 6)...),
		community.Edge{Source: "a0", Target: "b0", Weight: 1})

	first := groupsAsSets(graphOf(t, edges), community.Detect(graphOf(t, edges)))
	for run := 0; run < 25; run++ {
		got := groupsAsSets(graphOf(t, edges), community.Detect(graphOf(t, edges)))
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d partitioned differently:\n first: %v\n   got: %v", run, first, got)
		}
	}
}

// The same edges in a different order are the same graph. This is the failure that hides: map
// iteration and caller-supplied ordering are both stable enough in a small test to look fine.
func TestTheOrderEdgesArriveInDoesNotChangeTheAnswer(t *testing.T) {
	edges := append(append(clique("a", 6), clique("b", 5)...),
		community.Edge{Source: "a0", Target: "b0", Weight: 1})
	want := groupsAsSets(graphOf(t, edges), community.Detect(graphOf(t, edges)))

	rng := rand.New(rand.NewPCG(1, 2))
	for shuffle := 0; shuffle < 25; shuffle++ {
		mixed := append([]community.Edge(nil), edges...)
		rng.Shuffle(len(mixed), func(i, j int) { mixed[i], mixed[j] = mixed[j], mixed[i] })
		g := graphOf(t, mixed)
		if got := groupsAsSets(g, community.Detect(g)); !reflect.DeepEqual(want, got) {
			t.Fatalf("shuffle %d changed the partition:\n want: %v\n  got: %v", shuffle, want, got)
		}
	}
}

// And the group numbers are stable too, not only the grouping. Anything that stores a group id
// depends on it, and a test comparing set contents would pass while the numbers moved underneath.
func TestGroupNumbersAreStableAcrossRuns(t *testing.T) {
	edges := append(clique("a", 5), clique("b", 5)...)
	g := graphOf(t, edges)
	first := community.Detect(g)
	for run := 0; run < 10; run++ {
		if got := community.Detect(g); !reflect.DeepEqual(first.Of, got.Of) {
			t.Fatalf("run %d renumbered the groups:\n first: %v\n   got: %v", run, first.Of, got.Of)
		}
	}
}

// ── Every component, not the largest ──────────────────────────────────────────────────────────

// A person's memory is not one giant component with noise around it. Work, family, a hobby and a trip
// share no entity at all, and keeping only the largest would drop three quarters of somebody's life
// while the remaining answers looked fine.
func TestFourUnconnectedSubjectsAreFourSubjects(t *testing.T) {
	var edges []community.Edge
	for _, prefix := range []string{"work", "family", "hobby", "trip"} {
		edges = append(edges, clique(prefix, 4)...)
	}
	g := graphOf(t, edges)
	p := community.Detect(g)

	if len(p.Groups) != 4 {
		t.Fatalf("four unconnected subjects produced %d groups: %v", len(p.Groups), groupsAsSets(g, p))
	}
	for _, set := range groupsAsSets(g, p) {
		names := strings.Split(set, ",")
		prefix := strings.TrimRight(names[0], "0123456789")
		for _, n := range names {
			if !strings.HasPrefix(n, prefix) {
				t.Fatalf("a group mixes subjects that share no relation: %s", set)
			}
		}
	}
}

// An entity mentioned once, connected to nothing, is a subject with one member rather than something
// to discard. Dropping it would make the partition cover less than the graph.
func TestAnUnconnectedEntityIsItsOwnSubject(t *testing.T) {
	g := graphOf(t, append(clique("a", 4),
		community.Edge{Source: "lonely", Target: "alsolonely", Weight: 1}))
	p := community.Detect(g)
	if len(p.Groups) != 2 {
		t.Fatalf("expected the clique and the pair, got %v", groupsAsSets(g, p))
	}
}

// ── What the grouping is actually for ─────────────────────────────────────────────────────────

// Two dense subjects joined by one relation are two subjects. This is the case the whole thing exists
// to get right: a single edge between two groups of things somebody talks about is a mention, not a
// merger.
func TestOneRelationBetweenTwoDenseSubjectsDoesNotMergeThem(t *testing.T) {
	g := graphOf(t, append(append(clique("work", 6), clique("family", 6)...),
		community.Edge{Source: "work0", Target: "family0", Weight: 1}))
	p := community.Detect(g)

	if len(p.Groups) != 2 {
		t.Fatalf("one bridging relation merged two subjects into %d groups: %v",
			len(p.Groups), groupsAsSets(g, p))
	}
}

// ── The hierarchy ─────────────────────────────────────────────────────────────────────────────

// A level exists exactly where a group was too large to be one subject and was split again, so the
// depth of the tree is a property of the memory rather than a number somebody chose.
func TestALevelExistsOnlyWhereAGroupWasTooLarge(t *testing.T) {
	small := graphOf(t, clique("a", 5))
	if depth := community.Build(small).Depth(); depth != 1 {
		t.Fatalf("a graph with nothing to split has %d levels", depth)
	}

	// Three dense subjects, joined into one loose group by single relations: the group exceeds the
	// bound, and splitting it finds the three subjects inside it.
	var edges []community.Edge
	for _, prefix := range []string{"p", "q", "r"} {
		edges = append(edges, clique(prefix, community.MaxSize/2)...)
	}
	edges = append(edges,
		community.Edge{Source: "p0", Target: "q0", Weight: 3},
		community.Edge{Source: "q0", Target: "r0", Weight: 3},
	)
	h := community.Build(graphOf(t, edges))
	if h.Depth() < 1 {
		t.Fatal("no communities at all")
	}
	for _, c := range h.Communities {
		if c.Level == 0 && c.Parent != -1 {
			t.Fatalf("a root community has a parent: %+v", c)
		}
		if c.Level > 0 && c.Parent == -1 {
			t.Fatalf("a community below the root has no parent: %+v", c)
		}
	}
}

// A child is a subset of its parent. Without this the tree is a stack of unrelated partitions, and a
// walk that descends from a theme lands somewhere the theme does not contain.
func TestEveryChildIsASubsetOfItsParent(t *testing.T) {
	h := community.Build(graphOf(t, twoSubjectsSharingOneTheme()))

	byID := map[int]community.Community{}
	for _, c := range h.Communities {
		byID[c.ID] = c
	}
	children := 0
	for _, c := range h.Communities {
		if c.Parent == -1 {
			continue
		}
		children++
		parent := byID[c.Parent]
		if int(c.Level) != int(parent.Level)+1 {
			t.Fatalf("community %d is at level %d under a parent at %d", c.ID, c.Level, parent.Level)
		}
		held := map[string]bool{}
		for _, m := range parent.Members {
			held[m] = true
		}
		for _, m := range c.Members {
			if !held[m] {
				t.Fatalf("community %d holds %q, which its parent %d does not", c.ID, m, parent.ID)
			}
		}
	}
	if children == 0 {
		t.Fatal("nothing was split, so this test asserts nothing")
	}
}

// A group nothing divides is left alone even when it is over the bound. Cutting it to satisfy a number
// produces two summaries of half a subject and no way to tell that is what happened.
func TestAGroupNothingDividesIsLeftWhole(t *testing.T) {
	// One clique, larger than the bound, with no internal structure to find.
	h := community.Build(graphOf(t, clique("dense", community.MaxSize+8)))
	if h.Depth() != 1 {
		t.Fatalf("an indivisible group was cut into %d levels: %+v", h.Depth(), h.Communities)
	}
	if len(h.Communities) != 1 || len(h.Communities[0].Members) != community.MaxSize+8 {
		t.Fatalf("the group did not come back whole: %+v", h.Communities)
	}
}

// The hierarchy is as reproducible as the partition under it, including the identifiers — a stored
// community id that moved between runs would point at a different subject.
func TestTheHierarchyIsReproducible(t *testing.T) {
	edges := twoSubjectsSharingOneTheme()
	first := community.Build(graphOf(t, edges))
	for run := 0; run < 10; run++ {
		if got := community.Build(graphOf(t, edges)); !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d produced a different hierarchy", run)
		}
	}
}

// ── What a graph refuses to be built from ─────────────────────────────────────────────────────

func TestAGraphRefusesWhatItCannotPlace(t *testing.T) {
	for name, edges := range map[string][]community.Edge{
		"an unnamed source": {{Source: "", Target: "a", Weight: 1}},
		"an unnamed target": {{Source: "a", Target: "", Weight: 1}},
		"a negative weight": {{Source: "a", Target: "b", Weight: -1}},
	} {
		if _, err := community.NewGraph(edges); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// The same pair asserted five times is one connection of weight five, not five connections. Counted
// as five it makes a pair look denser than it is, and density is what every decision here is made on.
func TestRepeatedRelationsBetweenOnePairAreOneEdge(t *testing.T) {
	repeated := []community.Edge{
		{Source: "a", Target: "b", Weight: 1},
		{Source: "b", Target: "a", Weight: 1},
		{Source: "a", Target: "b", Weight: 1},
	}
	g := graphOf(t, repeated)
	if g.Order() != 2 {
		t.Fatalf("two entities produced %d nodes", g.Order())
	}
	// Direction is dropped, so b→a is the same connection as a→b: three assertions, one edge of
	// weight three, and a partition of one group.
	p := community.Detect(g)
	if len(p.Groups) != 1 {
		t.Fatalf("a densely connected pair was split: %v", groupsAsSets(g, p))
	}
}

// A relation from something to itself informs no grouping decision — a node is already in its own
// group — and counting it would distort the density every move is measured against.
func TestARelationToItselfIsNotAnEdge(t *testing.T) {
	g := graphOf(t, []community.Edge{
		{Source: "a", Target: "a", Weight: 5},
		{Source: "a", Target: "b", Weight: 1},
	})
	p := community.Detect(g)
	if len(p.Groups) != 1 || len(p.Groups[0]) != 2 {
		t.Fatalf("a self-relation changed the partition: %v", groupsAsSets(g, p))
	}
}

// twoSubjectsSharingOneTheme is two dense groups tied together by many relations: one subject at the
// coarse scale and two at the finer one, which is the shape a hierarchy exists for. Sixteen ties are
// what it takes — with twelve they are two subjects at every scale, which is also correct and is why
// the number is here rather than being called "some".
func twoSubjectsSharingOneTheme() []community.Edge {
	edges := append(clique("a", 16), clique("b", 16)...)
	for i := 0; i < 16; i++ {
		edges = append(edges, community.Edge{
			Source: fmt.Sprintf("a%d", i), Target: fmt.Sprintf("b%d", i), Weight: 1,
		})
	}
	return edges
}

func ring(prefix string, n int) []community.Edge {
	var out []community.Edge
	for i := 0; i < n; i++ {
		out = append(out, community.Edge{
			Source: fmt.Sprintf("%s%d", prefix, i),
			Target: fmt.Sprintf("%s%d", prefix, (i+1)%n),
			Weight: 1,
		})
	}
	return out
}

func star(prefix string, leaves int) []community.Edge {
	var out []community.Edge
	for i := 0; i < leaves; i++ {
		out = append(out, community.Edge{
			Source: prefix + "hub",
			Target: fmt.Sprintf("%s%d", prefix, i),
			Weight: 1,
		})
	}
	return out
}

// connected reports whether a set of nodes is reachable from any one of them without leaving the set.
func connected(g *community.Graph, members []int) bool {
	if len(members) < 2 {
		return true
	}
	within := make(map[int]bool, len(members))
	for _, n := range members {
		within[n] = true
	}
	seen := map[int]bool{members[0]: true}
	queue := []int{members[0]}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, nb := range g.Neighbours(node) {
			if within[nb] && !seen[nb] {
				seen[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	return len(seen) == len(members)
}

func contains(members []int, node int) bool {
	for _, m := range members {
		if m == node {
			return true
		}
	}
	return false
}

// A graph with nothing in it partitions into nothing, rather than into one empty group. A caller with
// an empty scope asks this on their first thematic question.
func TestAnEmptyGraphHasNoSubjects(t *testing.T) {
	g := graphOf(t, nil)
	if g.Order() != 0 {
		t.Fatalf("an empty graph has %d nodes", g.Order())
	}
	if p := community.Detect(g); len(p.Groups) != 0 || len(p.Of) != 0 {
		t.Fatalf("an empty graph produced %+v", p)
	}
	if h := community.Build(g); len(h.Communities) != 0 || h.Depth() != 0 {
		t.Fatalf("an empty graph produced %d communities", len(h.Communities))
	}
}

// A member of a group whose every relation points outside it has no place in the induced subgraph, and
// dropping it would lose a member of the community it belongs to. It comes back as a group of one,
// which is what a thing connected only to other subjects is.
func TestAMemberConnectedOnlyOutwardsSurvivesASplit(t *testing.T) {
	edges := twoSubjectsSharingOneTheme()
	// One entity attached to a single member of each half: inside the parent group, and inside
	// neither half once the parent is split.
	edges = append(edges,
		community.Edge{Source: "between", Target: "a0", Weight: 1},
		community.Edge{Source: "between", Target: "b0", Weight: 1},
	)
	h := community.Build(graphOf(t, edges))

	found := false
	for _, c := range h.Communities {
		if c.Level == 0 {
			continue
		}
		for _, m := range c.Members {
			if m == "between" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("an entity connected only outwards was lost when its group was split: %+v", h.Communities)
	}
}
