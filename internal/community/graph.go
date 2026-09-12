// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package community groups an entity graph into the subjects it contains.
//
// A question with no anchor — "what have I been going back and forth on this month" — names no entity
// and resembles no single passage, so nothing in the read path can start. The answer is a property of
// the graph rather than of any row in it: a set of entities densely connected to each other and
// sparsely connected to everything else is a subject somebody has, whether or not they ever named it.
//
// # What this package is allowed to decide
//
// How the graph is partitioned, and nothing else. It has no I/O, no clock and no database: it takes
// edges and returns groups. Whether a group is summarised, whether the summary is stored, and what an
// erasure does to it are decisions that live above this, and keeping them out is what lets them change
// without touching how a partition is made.
//
// It also does not decide what an edge means. The caller supplies relations somebody asserted, which
// is what makes a group a set of things that genuinely bear on each other rather than a set of things
// whose text is alike.
package community

import (
	"fmt"
	"sort"
)

// Edge is an asserted relation between two entities, with how much it should count.
//
// Weight is a count rather than a strength: two people connected by four relations are more connected
// than two joined by one, and nothing here judges which relation matters more. A judgement about that
// would be a ranking stage, and it would have to be justified by a measurement it improves.
type Edge struct {
	Source string
	Target string
	Weight float64
}

// Graph is an undirected weighted graph over entity identifiers.
//
// # Why direction is dropped
//
// The relations are directed and the grouping is not. "Hamza manages Marta" and "Marta reports to
// Hamza" are the same connection seen from two ends, and counting them as two edges would make a pair
// of people look twice as connected as they are — which is a difference the partition acts on.
//
// # Why it is built rather than accepted
//
// Determinism is a property of this package, and it starts here: the same set of edges in a different
// order has to produce the same graph, so identifiers are sorted and given indices, duplicates are
// summed rather than repeated, and self-loops are kept out. A partition built over an adjacency list
// whose order depended on the caller's iteration order would be stable in tests and unstable in
// production, which is the worst combination.
type Graph struct {
	// names holds the entity identifiers in sorted order. An index into this slice is how a node is
	// named everywhere else, so every loop over nodes is a loop in a fixed order.
	names []string
	index map[string]int
	// adjacency[i] holds i's neighbours. Each edge appears in both endpoints' lists.
	adjacency [][]neighbour
}

type neighbour struct {
	node   int
	weight float64
}

// NewGraph builds an undirected graph from asserted relations.
//
// Edges naming the same pair are summed. An edge from something to itself is dropped: a self-loop
// contributes to no grouping decision, because a node is already in its own community, and carrying
// one would distort the strength that every move is measured against.
func NewGraph(edges []Edge) (*Graph, error) {
	names := make([]string, 0, len(edges)*2)
	seen := map[string]struct{}{}
	for _, e := range edges {
		if e.Source == "" || e.Target == "" {
			return nil, fmt.Errorf("an edge with an unnamed end cannot be placed in a graph")
		}
		if e.Weight < 0 {
			return nil, fmt.Errorf("edge %q→%q has weight %v; a negative connection has no meaning here",
				e.Source, e.Target, e.Weight)
		}
		for _, n := range []string{e.Source, e.Target} {
			if _, ok := seen[n]; !ok {
				seen[n] = struct{}{}
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)

	g := &Graph{
		names:     names,
		index:     make(map[string]int, len(names)),
		adjacency: make([][]neighbour, len(names)),
	}
	for i, n := range names {
		g.index[n] = i
	}

	// Summed into a map first, so the same pair asserted five times is one edge of weight five rather
	// than five edges — which is the same connection either way and a different graph to walk.
	type pair struct{ a, b int }
	merged := map[pair]float64{}
	for _, e := range edges {
		a, b := g.index[e.Source], g.index[e.Target]
		if a == b {
			continue
		}
		if a > b {
			a, b = b, a
		}
		w := e.Weight
		if w == 0 {
			// An unweighted caller says "these are connected", which is weight one. Zero would be an
			// edge that exists and counts for nothing, which is not something anybody means.
			w = 1
		}
		merged[pair{a, b}] += w
	}

	keys := make([]pair, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	// Sorted, because map iteration is deliberately randomised in Go and the adjacency order reaches
	// the partition through the order neighbours are considered in.
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	for _, k := range keys {
		w := merged[k]
		g.adjacency[k.a] = append(g.adjacency[k.a], neighbour{k.b, w})
		g.adjacency[k.b] = append(g.adjacency[k.b], neighbour{k.a, w})
	}
	return g, nil
}

// Order is how many entities the graph holds.
func (g *Graph) Order() int { return len(g.names) }

// Name returns the identifier of a node.
func (g *Graph) Name(node int) string { return g.names[node] }

// Neighbours returns the nodes one node is connected to, in a fixed order.
//
// Exported so a caller can walk the graph the partition was made over — which is what a test asserting
// that a group is internally connected has to do, and what anything checking a chain against the graph
// would do. It exposes the shape, never the weights: a weight is an input to a grouping decision and
// nothing outside this package makes one.
func (g *Graph) Neighbours(node int) []int {
	out := make([]int, 0, len(g.adjacency[node]))
	for _, nb := range g.adjacency[node] {
		out = append(out, nb.node)
	}
	return out
}

// Components returns the connected components, each as a sorted list of node indices.
//
// # Why every component is kept
//
// A corpus of documents about one subject has a giant component with noise around it, so clustering
// only the largest is a reasonable simplification there. A person's memory is not shaped like that:
// work, family, a hobby and a trip share no entity at all and are components of similar weight.
// Keeping only the largest would silently drop most of somebody's life, and the loss would be
// invisible — the answers that remained would look fine.
//
// They are returned separately because partitioning them together is wasted work: no move between
// components can ever improve anything, since there is no edge to gain.
func (g *Graph) Components() [][]int {
	seen := make([]bool, len(g.names))
	var out [][]int
	for start := range g.names {
		if seen[start] {
			continue
		}
		// Breadth-first from the lowest unvisited node, so the components come back in a fixed order
		// and each one is sorted — both of which the hierarchy depends on to be reproducible.
		queue := []int{start}
		seen[start] = true
		var members []int
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			members = append(members, n)
			for _, nb := range g.adjacency[n] {
				if !seen[nb.node] {
					seen[nb.node] = true
					queue = append(queue, nb.node)
				}
			}
		}
		sort.Ints(members)
		out = append(out, members)
	}
	return out
}

// Subgraph returns the graph induced on a set of nodes, and a mapping back to this graph's indices.
//
// Used to split a group that was too large to be one subject: the split is a partition of the group
// alone, and running it over the whole graph again would let members leave for communities the parent
// does not contain.
func (g *Graph) Subgraph(nodes []int) (*Graph, []int) {
	within := make(map[int]bool, len(nodes))
	for _, n := range nodes {
		within[n] = true
	}
	sorted := append([]int(nil), nodes...)
	sort.Ints(sorted)

	var edges []Edge
	for _, n := range sorted {
		for _, nb := range g.adjacency[n] {
			// Once per pair. The adjacency holds each edge twice and NewGraph would sum them into
			// double weight, which changes every density the quality function computes.
			if nb.node > n && within[nb.node] {
				edges = append(edges, Edge{Source: g.names[n], Target: g.names[nb.node], Weight: nb.weight})
			}
		}
	}
	sub, err := NewGraph(edges)
	if err != nil {
		// Unreachable: the edges come from a graph that was already built, so they carry no unnamed
		// end and no negative weight.
		panic("community: a subgraph of a valid graph was invalid: " + err.Error())
	}
	// A node with no edges inside the group has no place in the induced graph, and dropping it
	// silently would lose a member of the community it belongs to. It is returned as its own group by
	// the caller, which is what an isolated member is.
	back := make([]int, sub.Order())
	for i, name := range sub.names {
		back[i] = g.index[name]
	}
	return sub, back
}

// Isolated returns the members of a node set that the induced subgraph does not contain.
//
// These are the members whose every edge left the group. They are not noise and they are not dropped:
// each is a group of one, which is what a thing connected only to other subjects is.
func (g *Graph) Isolated(nodes []int, kept []int) []int {
	keptSet := make(map[int]bool, len(kept))
	for _, n := range kept {
		keptSet[n] = true
	}
	var out []int
	for _, n := range nodes {
		if !keptSet[n] {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}
