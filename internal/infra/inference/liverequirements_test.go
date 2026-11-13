//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A requirements document, written for this measurement.
//
// It is not a real tender. It was written to read like one — parties, obligations stated with
// "shall" and "must", dates, criteria with weights, things that have already happened — because
// the vocabulary and the extraction prompt were written for conversation, and the question is what
// they keep of text that is none of that. The author of the document also wrote the list of what
// is in it, below, before running anything; that is a bias, and the write-up says so.
var requirementsDocument = []string{
	`1. Purpose. The Northern Health Authority is seeking proposals for a patient appointment service. The Authority operates forty-two clinics. The service will replace the booking system the clinics use today, which is called Calendra. The contract is for three years.`,

	`2. Parties and contacts. The procurement lead is Dervla Quinn. Dervla Quinn works at the Northern Health Authority. Questions must be sent to the procurement office. The technical lead for the Authority is Tomas Reilly, who is responsible for the integration with the patient record system.`,

	`3. Timetable. Questions from bidders are due on 14 March 2027. Proposals are due on 4 April 2027. Shortlisted bidders will present on 20 April 2027. The contract is scheduled to start on 1 June 2027. The Authority issued addendum 1 on 2 February 2027.`,

	`4. Functional requirements. The contractor shall provide single sign-on for clinic staff. The service must send appointment reminders by text message. The service must let a patient cancel an appointment without calling the clinic. The contractor shall provide an interface to the patient record system. The service shall support Irish and English.`,

	`5. Security and data. The contractor must store patient data within the European Union. The contractor shall encrypt data at rest. The contractor must report a security incident to the Authority within twenty-four hours. The service must keep an audit record of every change to an appointment. The contractor is responsible for penetration testing once a year.`,

	`6. Service levels. The service must be available 99.9 percent of the time during clinic hours. The contractor shall respond to a critical fault within one hour. The contractor shall provide a support desk located in Ireland. Planned maintenance must be scheduled outside clinic hours.`,

	`7. Evaluation. Proposals will be evaluated by a panel of five. The technical criterion has a weight of fifty percent. The price criterion has a weight of thirty percent. The implementation plan has a weight of twenty percent. The panel is chaired by Dervla Quinn. A bidder that does not meet the data residency requirement will be excluded.`,

	`8. Commercial terms. The bidder must hold professional indemnity insurance of five million euro. Payment is due thirty days after an invoice. The contractor shall migrate existing appointments from Calendra before the start date. The Authority owns all data in the service. The contract can be extended for two further years.`,
}

// What the document says, written before anything was run: every obligation, date, criterion,
// event and plain statement a reader would list. A requirement counts as kept when one stored
// fact's statement or quote holds every one of its words — crude, and the same for every run.
var requirementsGold = []struct {
	kind  string
	words []string
}{
	// Plain statements about the parties and the thing.
	{"statement", []string{"authority", "forty-two", "clinics"}},
	{"statement", []string{"replace", "calendra"}},
	{"statement", []string{"contract", "three years"}},
	{"statement", []string{"dervla quinn", "procurement lead"}},
	{"statement", []string{"dervla quinn", "works", "northern health authority"}},
	{"statement", []string{"tomas reilly", "technical lead"}},
	{"statement", []string{"tomas reilly", "responsible", "integration"}},
	{"statement", []string{"authority", "owns", "data"}},
	{"statement", []string{"panel", "chaired", "dervla quinn"}},
	{"statement", []string{"panel", "five"}},
	// Dates.
	{"date", []string{"questions", "14 march 2027"}},
	{"date", []string{"proposals", "4 april 2027"}},
	{"date", []string{"present", "20 april 2027"}},
	{"date", []string{"start", "1 june 2027"}},
	// Something that already happened.
	{"event", []string{"addendum 1", "2 february 2027"}},
	// Obligations.
	{"obligation", []string{"single sign-on"}},
	{"obligation", []string{"reminders", "text message"}},
	{"obligation", []string{"cancel", "appointment"}},
	{"obligation", []string{"interface", "patient record system"}},
	{"obligation", []string{"irish", "english"}},
	{"obligation", []string{"patient data", "european union"}},
	{"obligation", []string{"encrypt", "at rest"}},
	{"obligation", []string{"security incident", "twenty-four hours"}},
	{"obligation", []string{"audit record"}},
	{"obligation", []string{"penetration testing"}},
	{"obligation", []string{"available", "99.9"}},
	{"obligation", []string{"critical fault", "one hour"}},
	{"obligation", []string{"support desk", "ireland"}},
	{"obligation", []string{"maintenance", "outside clinic hours"}},
	{"obligation", []string{"indemnity insurance", "five million"}},
	{"obligation", []string{"migrate", "calendra"}},
	{"obligation", []string{"questions", "procurement office"}},
	// Criteria and their weights.
	{"criterion", []string{"technical", "fifty percent"}},
	{"criterion", []string{"price", "thirty percent"}},
	{"criterion", []string{"implementation plan", "twenty percent"}},
	{"criterion", []string{"data residency", "excluded"}},
	// Terms.
	{"term", []string{"payment", "thirty days"}},
	{"term", []string{"extended", "two further years"}},
}

// TestWhatARequirementsDocumentKeeps measures, and asserts only that the measurement ran.
//
// The document goes through the path a deployment's would: each section stored as one message of
// a tool's turn with no data subject, then formed by the worker with the configured model. What is
// read back is what the database holds afterwards — facts, refusals with their reasons, and any
// fact the document's own later section closed. The numbers are logged for a dated write-up; they
// are a property of this document, this model and this prompt, and not a claim about another.
func TestWhatARequirementsDocumentKeeps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("inference is not configured, and this target exists to exercise it: %v", err)
	}
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Fatal("TAISCE_TEST_DSN is not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const name = "inf_requirements"
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+name+` CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionMemorySchema(ctx, pool, name); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, name, "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(name)
	vocabulary, err := pg.LoadOntology(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	observations := pg.NewObservationStore(pool)
	model := inference.NewModel(config)
	worker := formation.NewWorker(pool, observations, formation.NewFormer(observations, pg.NewFactStore(pool), extract.New(model, vocabulary)))

	// One turn per section, in order, so that a later section can close what an earlier one said —
	// which is the thing one of the findings this measures is about.
	sources := map[string]int{}
	for i, section := range requirementsDocument {
		stored, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", OccurredAt: time.Date(2027, 2, 10, 9, i, 0, 0, time.UTC),
			Messages: []domain.Message{{Role: domain.RoleTool, Content: section, OccurredAt: time.Date(2027, 2, 10, 9, i, 0, 0, time.UTC)}}})
		if err != nil {
			t.Fatalf("section %d: %v", i+1, err)
		}
		sources[stored.ID] = i + 1
	}
	if _, err := worker.Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("form: %v", err)
	}
	fresh, err := observations.Freshness(ctx, schema, "p1")
	if err != nil || fresh.Parked != 0 {
		t.Fatalf("NO MEASUREMENT — not every section formed: %+v %v", fresh, err)
	}

	type fact struct {
		Section                                      int
		Predicate, Subject, Object, Statement, Quote string
		Current                                      bool
	}
	rows, err := pool.Query(ctx, schema.SQL(`
		SELECT e.source_observation_id::text, f.predicate, coalesce(s.canonical_name,''), coalesce(o.canonical_name,''),
		       f.statement, e.quote, upper_inf(f.valid) AND upper_inf(f.known)
		  FROM {schema}.fact f
		  JOIN {schema}.fact_evidence e ON e.scope=f.scope AND e.fact_id=f.fact_id
		  LEFT JOIN {schema}.entity s ON s.scope=f.scope AND s.entity_id=f.subject_entity_id
		  LEFT JOIN {schema}.entity o ON o.scope=f.scope AND o.entity_id=f.object_entity_id
		 WHERE f.scope='p1' ORDER BY 1, f.predicate, f.statement`))
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var facts []fact
	var source string
	rows, _ = pool.Query(ctx, schema.SQL(`
		SELECT e.source_observation_id::text, f.predicate, coalesce(s.canonical_name,''), coalesce(o.canonical_name,''),
		       f.statement, e.quote, upper_inf(f.valid) AND upper_inf(f.known)
		  FROM {schema}.fact f
		  JOIN {schema}.fact_evidence e ON e.scope=f.scope AND e.fact_id=f.fact_id
		  LEFT JOIN {schema}.entity s ON s.scope=f.scope AND s.entity_id=f.subject_entity_id
		  LEFT JOIN {schema}.entity o ON o.scope=f.scope AND o.entity_id=f.object_entity_id
		 WHERE f.scope='p1' ORDER BY 1, f.predicate, f.statement`))
	for rows.Next() {
		var f fact
		if err := rows.Scan(&source, &f.Predicate, &f.Subject, &f.Object, &f.Statement, &f.Quote, &f.Current); err != nil {
			t.Fatal(err)
		}
		f.Section = sources[source]
		facts = append(facts, f)
	}
	rows.Close()

	perSection := map[int]int{}
	closed := 0
	sort.Slice(facts, func(i, j int) bool { return facts[i].Section < facts[j].Section })
	for _, f := range facts {
		perSection[f.Section]++
		state := ""
		if !f.Current {
			state = "  [CLOSED]"
			closed++
		}
		t.Logf("fact  §%d  %s(%s, %s)  %q%s", f.Section, f.Predicate, f.Subject, f.Object, f.Quote, state)
	}

	reasons := map[string]int{}
	rejected, err := pool.Query(ctx, schema.SQL(`SELECT source_observation_id::text, reason, predicate, coalesce(quote,'') FROM {schema}.rejected_claim WHERE scope='p1' ORDER BY 1,2`))
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for rejected.Next() {
		var reason, predicate, quote string
		if err := rejected.Scan(&source, &reason, &predicate, &quote); err != nil {
			t.Fatal(err)
		}
		reasons[reason]++
		total++
		t.Logf("refused  §%d  %s  %s  %q", sources[source], reason, predicate, quote)
	}
	rejected.Close()

	kept := map[string]int{}
	of := map[string]int{}
	var missed []string
	for _, g := range requirementsGold {
		of[g.kind]++
		found := false
		for _, f := range facts {
			text := strings.ToLower(f.Statement + " " + f.Quote + " " + f.Subject + " " + f.Object)
			all := true
			for _, w := range g.words {
				if !strings.Contains(text, w) {
					all = false
					break
				}
			}
			if all {
				found = true
				break
			}
		}
		if found {
			kept[g.kind]++
		} else {
			missed = append(missed, g.kind+": "+strings.Join(g.words, " + "))
		}
	}

	t.Logf("== conditions: model %s, extractor %s, %d sections, %d gold items, 1 run", config.Model, model.ExtractionIdentity()[:12], len(requirementsDocument), len(requirementsGold))
	t.Logf("== facts stored: %d (per section %v); closed by a later statement: %d", len(facts), perSection, closed)
	t.Logf("== refused: %d, by reason %v", total, reasons)
	keptTotal := 0
	for _, kind := range []string{"statement", "date", "event", "obligation", "criterion", "term"} {
		t.Logf("== kept, %-10s %d of %d", kind+":", kept[kind], of[kind])
		keptTotal += kept[kind]
	}
	t.Logf("== kept, all: %d of %d", keptTotal, len(requirementsGold))
	for _, m := range missed {
		t.Logf("missed  %s", m)
	}
	// One thing under several names. The exact probe finds an entity by its normalised name, so
	// "the contractor" and "contractor" are two entities and a question that names one walks only
	// that one's facts.
	names, err := pool.Query(ctx, schema.SQL(`
		SELECT e.normalized_name, count(f.fact_id) FROM {schema}.entity e
		  LEFT JOIN {schema}.fact f ON f.scope=e.scope AND f.subject_entity_id=e.entity_id AND upper_inf(f.valid) AND upper_inf(f.known)
		 WHERE e.scope='p1' AND (e.normalized_name LIKE '%contractor%' OR e.normalized_name LIKE '%service' OR e.normalized_name LIKE '%authority%' OR e.normalized_name LIKE '%bidder%')
		 GROUP BY 1 ORDER BY 1`))
	if err != nil {
		t.Fatal(err)
	}
	for names.Next() {
		var entity string
		var n int
		if err := names.Scan(&entity, &n); err != nil {
			t.Fatal(err)
		}
		t.Logf("entity  %q is the subject of %d current facts", entity, n)
	}
	names.Close()

	// What a question gets back. The document was stored as a tool's words, so the recall names
	// that source; a recall with the default sources returns a person's own statements only.
	recaller := recall.NewWithBudget(pg.NewRecallStore(pool, schema), recall.Budget{})
	for _, question := range []string{
		"When are proposals due?",
		"What must the contractor do?",
		"What is the service required to do?",
		"What is Dervla Quinn's role?",
		"List the security requirements.",
	} {
		bundle, err := recaller.RecallAsOf(ctx, []string{"p1"}, question, []string{string(domain.RoleTool)}, domain.AsOf{}, recall.DirectHop)
		if err != nil {
			t.Fatalf("recall %q: %v", question, err)
		}
		var anchors, got []string
		for _, a := range bundle.Anchors {
			anchors = append(anchors, a.Entity.CanonicalName)
		}
		for _, f := range bundle.Facts {
			got = append(got, f.Predicate)
		}
		t.Logf("recall  %q: anchors %q, %d facts %v, cut=%v", question, anchors, len(bundle.Facts), got, bundle.Truncated)
	}

	if len(facts) == 0 && total == 0 {
		t.Fatalf("NO MEASUREMENT — nothing was stored and nothing was refused from %d sections", len(requirementsDocument))
	}
}
