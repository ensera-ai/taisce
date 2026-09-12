// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package community

import "sort"

// MaxSize is how many entities a group may hold before it is split again.
//
// PROVISIONAL, like Resolution, and for the same reason. What it controls is whether a level
// exists at all: a bound larger than the biggest group means one level and no hierarchy, and a small
// one means levels of groups too small to have a theme. The number that is right depends on how much
// a summary can hold about a subject, which is a measurement nobody has taken here.
const MaxSize = 24

// Level is one depth of the hierarchy: zero is the partition over the whole graph.
type Level int

// Community is one group at one level, with the group it was split out of.
type Community struct {
	// ID is unique across the hierarchy. Assigned in level order and then by lowest member, so the
	// same graph always produces the same identifiers.
	ID    int
	Level Level
	// Parent is the community this was split out of, or -1 at the root. Explicit rather than inferred
	// from membership overlap: a walk that has to work out where it came from by comparing member
	// sets gets it wrong the moment two siblings share nothing but their parent, and it does that
	// work on every walk.
	Parent int
	// Members are entity identifiers, sorted.
	Members []string
}

// Hierarchy is every community at every level.
//
// # Why there are levels at all
//
// A question has a scale. "What is going on at work" and "what is going on with the migration" are
// both thematic and they are not the same question, and one partition answers one of them. A level
// exists exactly where a group was too large to be one subject and was split again — so the depth of
// the tree is a property of the memory rather than a number somebody chose.
type Hierarchy struct {
	Communities []Community
}

// finer is the ladder of densities a split tries, as multiples of the base.
//
// # Why a ladder rather than one multiplier
//
// A group is split by asking a stricter question of it: how dense does a set of things have to be
// before it counts as one subject. How much stricter depends on the group — two subjects joined by a
// handful of relations come apart at barely above the base, and two joined by dozens need much more —
// and a single multiplier chosen once would divide the first group into fragments and leave the second
// whole.
//
// So the split takes the COARSEST density that actually divides the group, which makes the multiplier
// a property of the group rather than a parameter. The ladder is short and bounded because past a
// point every set of things is its own subject, and a partition of singletons is not a split: it is
// the group saying it has no structure inside it.
var finer = []float64{2, 4, 8, 16}

// Build partitions a graph and splits any group too large to be one subject.
//
// # Splitting is a partition of the group alone
//
// A group that is too large is re-partitioned over the subgraph induced on its own members, not over
// the whole graph again. Over the whole graph its members could leave for communities the parent does
// not contain, and the result would not be a refinement of the parent — which is what makes the tree
// a tree rather than a set of unrelated partitions stacked on top of each other.
//
// # A split that does not split stops
//
// A group whose partition is itself — dense enough that nothing improves by dividing it — is left
// alone even though it is over the bound. The alternative is dividing it arbitrarily, which produces
// two summaries of half a subject each and no way to tell that is what happened.
func Build(g *Graph) Hierarchy {
	var out Hierarchy
	root := Detect(g)

	type pending struct {
		graph   *Graph
		members []int // in the coordinates of `graph`
		level   Level
		parent  int
	}
	var queue []pending

	for _, members := range root.Groups {
		id := len(out.Communities)
		out.Communities = append(out.Communities, Community{
			ID: id, Level: 0, Parent: -1, Members: namesOf(g, members),
		})
		if len(members) > MaxSize {
			queue = append(queue, pending{graph: g, members: members, level: 1, parent: id})
		}
	}

	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]

		sub, back := next.graph.Subgraph(next.members)
		partition, divided := split(sub)
		if !divided {
			// Nothing divides it at any density that leaves subjects behind. Cutting it anyway to
			// satisfy a bound produces two summaries of half a subject and no way to tell that is
			// what happened.
			continue
		}

		// A member whose every edge left the group has no place in the induced graph. It is not noise
		// and it is not dropped: it is a group of one at this level, which is what a thing connected
		// only to other subjects is.
		var pieces [][]int
		for _, members := range partition.Groups {
			mapped := make([]int, 0, len(members))
			for _, node := range members {
				mapped = append(mapped, back[node])
			}
			sort.Ints(mapped)
			pieces = append(pieces, mapped)
		}
		for _, node := range next.graph.Isolated(next.members, back) {
			pieces = append(pieces, []int{node})
		}
		sort.Slice(pieces, func(i, j int) bool { return pieces[i][0] < pieces[j][0] })

		if len(pieces) < 2 {
			continue
		}
		for _, members := range pieces {
			id := len(out.Communities)
			out.Communities = append(out.Communities, Community{
				ID: id, Level: next.level, Parent: next.parent, Members: namesOf(next.graph, members),
			})
			if len(members) > MaxSize {
				queue = append(queue, pending{
					graph: next.graph, members: members, level: next.level + 1, parent: id,
				})
			}
		}
	}
	return out
}

// Depth is how many levels the hierarchy has.
func (h Hierarchy) Depth() int {
	deepest := -1
	for _, c := range h.Communities {
		if int(c.Level) > deepest {
			deepest = int(c.Level)
		}
	}
	return deepest + 1
}

func namesOf(g *Graph, members []int) []string {
	out := make([]string, 0, len(members))
	for _, node := range members {
		out = append(out, g.Name(node))
	}
	sort.Strings(out)
	return out
}

// split finds the coarsest density that divides a group into subjects.
//
// A partition of singletons is rejected rather than accepted as a split: it means the density has been
// raised past the point where anything is a group, and dividing a subject into its individual members
// is not a finer view of it. A group that only ever shatters is one with no structure inside — a set
// of things all equally connected to each other — and it stays whole.
func split(sub *Graph) (Partition, bool) {
	for _, factor := range finer {
		p := DetectAt(sub, Resolution*factor)
		if len(p.Groups) < 2 {
			continue
		}
		if allSingletons(p) {
			// Every coarser density already failed to divide it, and every finer one will shatter it
			// further. There is nothing between the two.
			return Partition{}, false
		}
		return p, true
	}
	return Partition{}, false
}

func allSingletons(p Partition) bool {
	for _, members := range p.Groups {
		if len(members) > 1 {
			return false
		}
	}
	return true
}
