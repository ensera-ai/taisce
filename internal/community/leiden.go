// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package community

import (
	"fmt"
	"sort"
)

// Resolution is how dense a group has to be to be one subject.
//
// PROVISIONAL, and it must not be read as anything else — #102 holds what would settle it. Set too
// low it produces a handful of enormous groups, each summarised as though it were one subject; too
// high, dozens too small to have a theme. Both return answers and neither looks wrong from outside,
// which is exactly why the number needs a measurement against a real scope rather than a plausible
// default.
const Resolution = 0.05

// quality scores a group by internal weight against how dense a group of that size has to be.
//
// # Why density against a resolution, and not modularity
//
// Modularity compares a group's internal edges against what a random graph with the same degrees
// would hold, and that comparison has a resolution limit: below a size that depends on how big the
// WHOLE graph is, a genuine small group is always better merged into a neighbour. The same subject —
// four entities, densely joined — would be kept in a small scope and dissolved in a large one, and the
// dissolving would happen as somebody's memory grew. A retrieval surface that degrades with use is the
// failure mode that cannot be found in testing, because testing is done on small inputs.
//
// Density is local: a group's fate depends on the group, so nothing about it changes when unrelated
// memory is added elsewhere. It also makes the parameter a number somebody can reason about — how
// connected a set of things has to be before it is a subject — rather than one whose meaning moves.
func quality(internal, size, resolution float64) float64 {
	return internal - resolution*size*(size-1)/2
}

// Partition is an assignment of every node to a group.
type Partition struct {
	// Of[node] is the group a node belongs to. Group numbers are contiguous from zero, assigned in
	// order of each group's lowest member, so the same partition always carries the same numbers.
	Of []int
	// Groups holds each group's members, sorted, indexed by group number.
	Groups [][]int
}

// Detect partitions a graph into the subjects it contains, at the default density.
func Detect(g *Graph) Partition { return DetectAt(g, Resolution) }

// DetectAt partitions at a stated density, which is what a level of the hierarchy varies.
//
// A subject at one scale is several at a finer one — "work", and inside it "the migration" — and the
// only difference between those two answers is how dense a set of things has to be before it counts as
// one subject. Splitting a group by running the SAME density over it again returns it whole, which is
// no surprise: that density is what chose the group.
//
// # Three phases, and each is there for a different reason
//
// **Moving** takes each node to whichever group its presence improves most, in a fixed order, until
// nothing moves. A node is only considered for a group it has an edge to: a move into a group it does
// not touch cannot improve density and would put a thing into a subject it has nothing to do with.
//
// **Splitting** breaks every group into its connected pieces. Moving alone can leave a group
// internally disconnected — a node that joined early can be the only thing holding two halves
// together, and when it later moves away nothing puts them back, because each half is already where it
// wants to be. That group is two subjects with one name, and whatever summarises it writes one summary
// about two things. Splitting makes connectedness a property of the output rather than a hope, and it
// needs no parameter and no randomness.
//
// **Aggregating** collapses each group to a single node and starts again. This is not an optimisation:
// moving can only relocate one node at a time, so it can NEVER merge two groups, however much merging
// them would improve. Two dense subjects joined by sixteen relations stay separate at every density
// without this — measured, on exactly that graph. Aggregation is what makes a merge a single move.
//
// # Why there is no randomness
//
// The published form of this algorithm makes the splitting phase a randomised search for
// sub-communities, which both guarantees connectedness and helps escape the arrangement the first pass
// settles in. Only the first is a correctness property, and taking it directly — split into connected
// pieces — makes it a property of the code. That removes the determinism problem rather than solving
// it: there is no generator to pin, no stream a toolchain upgrade can change, and no way for a scope's
// themes to reorganise between two runs of the same input.
//
// What it costs is the escape from local optima. The day a measurement says the partition is worse
// than it should be, a randomised refinement is what to add — and determinism stops being free
// at that point, so it will need a generator whose stream is fixed by its own definition.
//
// # Components are partitioned independently
//
// No move can cross between two parts of the graph with no edge between them, so partitioning them
// together is wasted work — and keeping only the largest, which suits a document corpus, would
// silently discard most of a person's memory: work, family, a hobby and a trip share no entity at all.
func DetectAt(g *Graph, resolution float64) Partition {
	if g.Order() == 0 {
		return Partition{}
	}
	// Every entity starts as its own group, so a component of one — an entity connected to nothing —
	// is already answered and nothing has to write it.
	membership := make([]int, g.Order())
	for i := range membership {
		membership[i] = i
	}
	for _, component := range g.Components() {
		partitionComponent(g, component, membership, resolution)
	}
	return finalise(g, membership)
}

// tier is one level of aggregation: a graph whose nodes stand for groups of the original.
type tier struct {
	graph *Graph
	// size[i] is how many original entities node i stands for, and internal[i] is the weight of the
	// edges already inside it. Both are needed by the quality function, which is about a set of
	// entities rather than about a node.
	size     []float64
	internal []float64
	// members[i] lists the original node indices behind node i.
	members [][]int
}

// partitionComponent runs moving, splitting and aggregating over one connected component, writing
// the result into membership in the original graph's coordinates.
//
// Membership is written on every round rather than only at the end, so the answer is complete
// whichever round turns out to be the last.
func partitionComponent(g *Graph, component []int, membership []int, resolution float64) {
	current, ok := seedTier(g, component)
	if !ok {
		// A component of one entity. It is already its own group, which membership says by default.
		return
	}

	for round := 0; round <= len(component)+16; round++ {
		nodes := make([]int, current.graph.Order())
		local := make([]int, current.graph.Order())
		for i := range nodes {
			nodes[i], local[i] = i, i
		}

		move(current, nodes, local, resolution)
		splitDisconnected(current.graph, nodes, local)

		groups := groupsOf(local)
		for _, members := range groups {
			// Labelled by the lowest original entity in the group, so the label means the same thing
			// at every level of aggregation and two runs produce the same one.
			label := current.members[members[0]][0]
			for _, node := range members {
				for _, original := range current.members[node] {
					membership[original] = label
				}
			}
		}

		if len(groups) == current.graph.Order() {
			// Nothing merged. Aggregating would rebuild the same graph and the next round would make
			// the same decisions, so this is the fixed point — and stopping here is what makes the
			// loop terminate rather than an optimisation.
			return
		}
		if len(groups) == 1 {
			// The whole component is one subject. There is nothing left to merge it with.
			return
		}
		next, err := aggregate(current, groups)
		if err != nil {
			// The aggregate has nothing to move, which is the same fixed point reached another way.
			return
		}
		current = next
	}
}

// seedTier is the starting point: every entity alone.
//
// Starting from singletons rather than from one group means the first pass builds subjects up rather
// than carving them out, and a thing that belongs nowhere stays alone rather than having to escape.
func seedTier(g *Graph, component []int) (tier, bool) {
	var edges []Edge
	within := make(map[int]bool, len(component))
	for _, node := range component {
		within[node] = true
	}
	for _, node := range component {
		for _, nb := range g.adjacency[node] {
			if nb.node > node && within[nb.node] {
				edges = append(edges, Edge{Source: label(node), Target: label(nb.node), Weight: nb.weight})
			}
		}
	}
	if len(edges) == 0 {
		// A component of one entity: no edges to build a graph from, and nothing to partition.
		return tier{}, false
	}
	sub, err := NewGraph(edges)
	if err != nil {
		panic("community: a component of a valid graph was invalid: " + err.Error())
	}
	t := tier{
		graph:    sub,
		size:     make([]float64, sub.Order()),
		internal: make([]float64, sub.Order()),
		members:  make([][]int, sub.Order()),
	}
	for i := 0; i < sub.Order(); i++ {
		t.size[i] = 1
		t.members[i] = []int{unlabel(sub.Name(i))}
	}
	return t, true
}

// move takes each node to the group it does most good in, until nothing moves.
//
// Nodes are visited in index order, which is sorted label order. The visit order changes the outcome,
// so it has to be fixed rather than whatever order the caller supplied edges in.
func move(t tier, nodes []int, membership []int, resolution float64) {
	g := t.graph
	sizes := map[int]float64{}
	internal := map[int]float64{}
	for _, node := range nodes {
		sizes[membership[node]] += t.size[node]
		internal[membership[node]] += t.internal[node]
	}
	for _, node := range nodes {
		for _, nb := range g.adjacency[node] {
			if nb.node > node && membership[nb.node] == membership[node] {
				internal[membership[node]] += nb.weight
			}
		}
	}

	for improved, pass := true, 0; improved && pass <= len(nodes)+16; pass++ {
		improved = false
		for _, node := range nodes {
			from := membership[node]
			links := linksToGroups(g, membership, node)

			// Out first, so staying is measured the same way as leaving: against a group this node is
			// not currently in.
			sizes[from] -= t.size[node]
			internal[from] -= links[from] + t.internal[node]

			best := from
			bestGain := gainOfJoining(internal[from], sizes[from], links[from], t, node, resolution)
			for _, to := range sortedKeys(links) {
				if to == from {
					continue
				}
				if gain := gainOfJoining(internal[to], sizes[to], links[to], t, node, resolution); gain > bestGain {
					best, bestGain = to, gain
				}
			}

			sizes[best] += t.size[node]
			internal[best] += links[best] + t.internal[node]
			if best != from {
				membership[node] = best
				improved = true
			}
		}
	}
}

// gainOfJoining is what one node adds to a group by being in it, counting what it already holds.
func gainOfJoining(internal, size, links float64, t tier, node int, resolution float64) float64 {
	return quality(internal+links+t.internal[node], size+t.size[node], resolution) -
		quality(internal, size, resolution)
}

// splitDisconnected breaks every group into its connected pieces.
//
// The guarantee. A group that is two pieces joined by nothing is two subjects with one name, and
// everything downstream treats it as one. A piece keeps the lowest node in it as its label, so the
// same split always produces the same labels.
func splitDisconnected(g *Graph, nodes []int, membership []int) {
	byGroup := map[int][]int{}
	for _, node := range nodes {
		byGroup[membership[node]] = append(byGroup[membership[node]], node)
	}
	for _, group := range sortedIntKeys(byGroup) {
		members := byGroup[group]
		if len(members) < 2 {
			continue
		}
		inGroup := make(map[int]bool, len(members))
		for _, node := range members {
			inGroup[node] = true
		}
		seen := map[int]bool{}
		for _, start := range members {
			if seen[start] {
				continue
			}
			piece := reachableWithin(g, start, inGroup, seen)
			for _, node := range piece {
				membership[node] = piece[0]
			}
		}
	}
}

// reachableWithin walks the part of a group reachable from one member without leaving it.
func reachableWithin(g *Graph, start int, inGroup map[int]bool, seen map[int]bool) []int {
	queue := []int{start}
	seen[start] = true
	var piece []int
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		piece = append(piece, node)
		for _, nb := range g.adjacency[node] {
			if inGroup[nb.node] && !seen[nb.node] {
				seen[nb.node] = true
				queue = append(queue, nb.node)
			}
		}
	}
	sort.Ints(piece)
	return piece
}

// aggregate collapses each group into one node, carrying what is already inside it.
//
// The internal weight and the size travel with the node, which is what lets the next round judge a
// merge of two groups by the same rule it judged a move of one entity: a group is a set of entities,
// and the quality function only ever asked about sets.
func aggregate(t tier, groups [][]int) (tier, error) {
	groupOf := make(map[int]int, t.graph.Order())
	for i, members := range groups {
		for _, node := range members {
			groupOf[node] = i
		}
	}

	size := make([]float64, len(groups))
	internal := make([]float64, len(groups))
	members := make([][]int, len(groups))
	for i, group := range groups {
		for _, node := range group {
			size[i] += t.size[node]
			internal[i] += t.internal[node]
			members[i] = append(members[i], t.members[node]...)
		}
		sort.Ints(members[i])
	}

	var edges []Edge
	for node := 0; node < t.graph.Order(); node++ {
		for _, nb := range t.graph.adjacency[node] {
			if nb.node <= node {
				continue
			}
			a, b := groupOf[node], groupOf[nb.node]
			if a == b {
				// An edge inside a group is no longer an edge: it is weight the group holds, and it
				// has to be counted there or a merge would be judged on a density that ignores it.
				internal[a] += nb.weight
				continue
			}
			edges = append(edges, Edge{Source: label(a), Target: label(b), Weight: nb.weight})
		}
	}
	if len(edges) == 0 {
		return tier{}, fmt.Errorf("aggregating produced a graph with no edges, which cannot happen inside a connected component")
	}
	graph, err := NewGraph(edges)
	if err != nil {
		return tier{}, err
	}
	if graph.Order() != len(groups) {
		return tier{}, fmt.Errorf("aggregating %d groups produced %d nodes", len(groups), graph.Order())
	}

	// NewGraph sorts by label, so the mapping back is by name rather than by position.
	next := tier{
		graph:    graph,
		size:     make([]float64, graph.Order()),
		internal: make([]float64, graph.Order()),
		members:  make([][]int, graph.Order()),
	}
	for i := 0; i < graph.Order(); i++ {
		from := unlabel(graph.Name(i))
		next.size[i] = size[from]
		next.internal[i] = internal[from]
		next.members[i] = members[from]
	}
	return next, nil
}

// groupsOf collects a membership into groups, each sorted, ordered by lowest member.
func groupsOf(membership []int) [][]int {
	byGroup := map[int][]int{}
	for node, group := range membership {
		byGroup[group] = append(byGroup[group], node)
	}
	labels := sortedIntKeys(byGroup)
	out := make([][]int, 0, len(labels))
	for _, l := range labels {
		members := byGroup[l]
		sort.Ints(members)
		out = append(out, members)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// finalise renumbers groups so a partition does not carry node indices as labels.
//
// Numbered by each group's lowest member, so two runs over one graph produce the same grouping AND the
// same numbers — which anything storing a group id depends on, and which makes a determinism test an
// equality rather than a comparison of set contents.
func finalise(g *Graph, membership []int) Partition {
	byGroup := map[int][]int{}
	for node := 0; node < g.Order(); node++ {
		byGroup[membership[node]] = append(byGroup[membership[node]], node)
	}
	labels := sortedIntKeys(byGroup)
	for _, l := range labels {
		sort.Ints(byGroup[l])
	}
	sort.SliceStable(labels, func(i, j int) bool {
		return byGroup[labels[i]][0] < byGroup[labels[j]][0]
	})

	out := Partition{Of: make([]int, g.Order()), Groups: make([][]int, 0, len(labels))}
	for number, label := range labels {
		for _, node := range byGroup[label] {
			out.Of[node] = number
		}
		out.Groups = append(out.Groups, byGroup[label])
	}
	return out
}

// linksToGroups sums the weight from one node into each group it touches.
func linksToGroups(g *Graph, membership []int, node int) map[int]float64 {
	links := map[int]float64{membership[node]: 0}
	for _, nb := range g.adjacency[node] {
		links[membership[nb.node]] += nb.weight
	}
	return links
}

// label and unlabel name an aggregate node.
//
// Zero-padded, because a graph sorts its nodes by name and "10" sorts before "2". Unpadded labels
// would make the aggregate graph's node order depend on how many groups there are, which is a
// different partition for the same input.
func label(i int) string { return fmt.Sprintf("%012d", i) }

func unlabel(s string) int {
	var i int
	if _, err := fmt.Sscanf(s, "%d", &i); err != nil {
		panic("community: an aggregate node carried an unreadable label: " + s)
	}
	return i
}

// sortedKeys and sortedIntKeys exist because Go randomises map iteration deliberately, and the order
// groups are considered in decides which of two equally good moves is taken.
func sortedKeys(m map[int]float64) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func sortedIntKeys(m map[int][]int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
