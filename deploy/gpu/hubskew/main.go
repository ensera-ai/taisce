// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Command hubskew measures what a hub costs on the read path (#7).
//
// # What it measures, and why it is not a benchmark of the service
//
// Every recall under this architecture starts at an entity and expands, so the shape of the degree
// distribution is the shape of the read path's cost. A uniform-degree graph says nothing about that:
// real corpora are skewed, and the question is whether the traversal degrades non-linearly where the
// degree is highest. So this reads the graph that is actually there, ranks entities by degree, and
// times the real traversal — `RecallStore.FactsAboutForSubject`, not a copy of its SQL, because a
// copy measures a query nobody runs.
//
// It times one statement against one database. It is not an end-to-end latency figure: no model
// call, no HTTP, no concurrency. Those belong to the scaling work, and quoting this as though it
// were them would be the failure rule 9 exists to prevent.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "hubskew:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	schemaName := flag.String("schema", "", "the memory schema to measure")
	repeats := flag.Int("repeats", 25, "timed traversals per sampled entity")
	sample := flag.Int("sample", 8, "entities sampled at each percentile")
	limit := flag.Int("limit", 50, "the bundle's row budget, as recall applies it")
	out := flag.String("out", "hub-skew.json", "where the measurement is written")
	flag.Parse()

	dsn := strings.TrimSpace(os.Getenv("TAISCE_DSN"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("TAISCE_TEST_DSN"))
	}
	if dsn == "" || *schemaName == "" {
		return fmt.Errorf("TAISCE_DSN and -schema are required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	schema, err := pg.NewSchema(*schemaName)
	if err != nil {
		return err
	}

	graph, err := readGraph(ctx, pool, schema)
	if err != nil {
		return err
	}
	if len(graph.degrees) == 0 {
		return fmt.Errorf("schema %s holds no facts, so there is nothing to measure", schema)
	}

	store := pg.NewRecallStore(pool, schema)
	report := Report{
		MeasuredAt:   time.Now().UTC().Format(time.RFC3339),
		Schema:       schema.String(),
		Scopes:       graph.scopes,
		Entities:     len(graph.degrees),
		Facts:        graph.facts,
		Fanout:       domain.Fanout,
		Repeats:      *repeats,
		SampleSize:   *sample,
		RowBudget:    *limit,
		Distribution: distribution(graph.degrees),
	}

	// The percentiles #7 names, and the two ends. The maximum is the whole question: if the read
	// path is going to break anywhere it is at the node everything else points at.
	for _, p := range []float64{50, 90, 95, 99, 99.9, 100} {
		bucket, err := measure(ctx, store, graph, p, *sample, *repeats, *limit)
		if err != nil {
			return err
		}
		report.ByDegree = append(report.ByDegree, bucket)
	}

	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(body, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println(string(body))
	return nil
}

// Report is the measurement as it is written down.
type Report struct {
	MeasuredAt   string       `json:"measured_at"`
	Schema       string       `json:"schema"`
	Scopes       []string     `json:"scopes"`
	Entities     int          `json:"entities"`
	Facts        int          `json:"facts"`
	Fanout       int          `json:"fanout_per_direction_per_hop"`
	Repeats      int          `json:"repeats_per_entity"`
	SampleSize   int          `json:"entities_per_percentile"`
	RowBudget    int          `json:"row_budget"`
	Distribution Distribution `json:"degree_distribution"`
	ByDegree     []Bucket     `json:"by_degree"`
}

// Distribution is the shape of the graph: how many edges meet at a node, across every node.
type Distribution struct {
	Min  int     `json:"min"`
	P50  int     `json:"p50"`
	P90  int     `json:"p90"`
	P95  int     `json:"p95"`
	P99  int     `json:"p99"`
	P999 int     `json:"p99_9"`
	Max  int     `json:"max"`
	Mean float64 `json:"mean"`
	// Skew is the maximum over the median: one number for how far the worst node is from the
	// ordinary one, which is the thing a uniform-degree benchmark cannot see.
	Skew float64 `json:"max_over_p50"`
}

// Bucket is one percentile of degree and what a traversal from there costs.
type Bucket struct {
	Percentile float64 `json:"percentile"`
	Degree     int     `json:"degree"`
	Entities   int     `json:"entities_measured"`
	OneHop     Timing  `json:"one_hop"`
	TwoHops    Timing  `json:"two_hops"`
}

// Timing is milliseconds over every repeat of every sampled entity at this degree.
type Timing struct {
	Samples  int     `json:"samples"`
	RowsMean float64 `json:"rows_mean"`
	P50      float64 `json:"p50_ms"`
	P95      float64 `json:"p95_ms"`
	P99      float64 `json:"p99_ms"`
	Max      float64 `json:"max_ms"`
}

type graph struct {
	scopes  []string
	facts   int
	degrees []entityDegree
}

type entityDegree struct {
	id     string
	scope  string
	name   string
	degree int
}

// readGraph counts the edges meeting at every entity. An entity is an endpoint of a fact in either
// direction, and both count: the traversal expands forward and backward, so a node that is only ever
// an object is as expensive to arrive at as one that is only ever a subject.
func readGraph(ctx context.Context, pool *pgxpool.Pool, schema pg.Schema) (graph, error) {
	var g graph
	rows, err := pool.Query(ctx, schema.SQL(`
		SELECT e.entity_id::text, e.scope, e.canonical_name,
		       (SELECT count(*) FROM {schema}.fact f
		         WHERE f.scope = e.scope
		           AND (f.subject_entity_id = e.entity_id OR f.object_entity_id = e.entity_id)
		           AND upper_inf(f.valid) AND upper_inf(f.known))::int AS degree
		  FROM {schema}.entity e`))
	if err != nil {
		return g, fmt.Errorf("read degrees: %w", err)
	}
	defer rows.Close()
	scopes := map[string]bool{}
	for rows.Next() {
		var d entityDegree
		if err := rows.Scan(&d.id, &d.scope, &d.name, &d.degree); err != nil {
			return g, err
		}
		scopes[d.scope] = true
		// A node nothing reaches is not on the read path and would only flatten the distribution.
		if d.degree > 0 {
			g.degrees = append(g.degrees, d)
		}
	}
	if err := rows.Err(); err != nil {
		return g, err
	}
	for s := range scopes {
		g.scopes = append(g.scopes, s)
	}
	sort.Strings(g.scopes)
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*)::int FROM {schema}.fact WHERE upper_inf(valid) AND upper_inf(known)`)).Scan(&g.facts); err != nil {
		return g, err
	}
	sort.Slice(g.degrees, func(i, j int) bool { return g.degrees[i].degree < g.degrees[j].degree })
	return g, nil
}

func distribution(sorted []entityDegree) Distribution {
	total := 0
	for _, d := range sorted {
		total += d.degree
	}
	return Distribution{
		Min:  sorted[0].degree,
		P50:  at(sorted, 50).degree,
		P90:  at(sorted, 90).degree,
		P95:  at(sorted, 95).degree,
		P99:  at(sorted, 99).degree,
		P999: at(sorted, 99.9).degree,
		Max:  sorted[len(sorted)-1].degree,
		Mean: float64(total) / float64(len(sorted)),
		Skew: float64(sorted[len(sorted)-1].degree) / float64(max(1, at(sorted, 50).degree)),
	}
}

func at(sorted []entityDegree, percentile float64) entityDegree {
	i := int(percentile / 100 * float64(len(sorted)-1))
	return sorted[min(max(i, 0), len(sorted)-1)]
}

// measure times the traversal from the entities at one percentile of degree.
//
// The sample runs downward from the percentile so the top bucket is the hubs themselves rather than
// the one node at the very top, which would be a single observation dressed as a percentile.
func measure(ctx context.Context, store *pg.RecallStore, g graph, percentile float64,
	sample, repeats, limit int) (Bucket, error) {
	end := int(percentile / 100 * float64(len(g.degrees)-1))
	start := max(0, end-sample+1)
	chosen := g.degrees[start : end+1]
	bucket := Bucket{Percentile: percentile, Degree: g.degrees[end].degree, Entities: len(chosen)}

	for _, hops := range []int{1, 2} {
		var timings []float64
		rows := 0
		for _, e := range chosen {
			for i := 0; i < repeats; i++ {
				began := time.Now()
				facts, err := store.FactsAboutForSubject(ctx, []string{e.scope}, []string{e.id},
					limit, []string{"user"}, domain.AsOf{}, hops, "")
				elapsed := time.Since(began)
				if err != nil {
					return bucket, fmt.Errorf("traverse from %s: %w", e.name, err)
				}
				timings = append(timings, float64(elapsed.Microseconds())/1000)
				rows += len(facts)
			}
		}
		t := summarise(timings)
		t.RowsMean = float64(rows) / float64(max(1, len(timings)))
		if hops == 1 {
			bucket.OneHop = t
		} else {
			bucket.TwoHops = t
		}
	}
	return bucket, nil
}

func summarise(ms []float64) Timing {
	if len(ms) == 0 {
		return Timing{}
	}
	sort.Float64s(ms)
	pick := func(p float64) float64 { return ms[min(int(p/100*float64(len(ms))), len(ms)-1)] }
	return Timing{
		Samples: len(ms),
		P50:     round(pick(50)),
		P95:     round(pick(95)),
		P99:     round(pick(99)),
		Max:     round(ms[len(ms)-1]),
	}
}

func round(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }
